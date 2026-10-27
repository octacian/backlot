package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/daemon"
	urfave "github.com/urfave/cli/v3"
)

func attachedRecord(c *urfave.Command, record v1.AttachedRecord) error {
	if c.Bool("json") {
		return json.NewEncoder(c.Writer).Encode(record)
	}
	if record.Log != nil {
		return writeLogRecord(c, *record.Log)
	}
	if record.Error != nil {
		if _, err := fmt.Fprintln(c.ErrWriter, record.Error); err != nil {
			return err
		}
	}
	if record.Gap != "" {
		_, err := fmt.Fprintln(c.ErrWriter, record.Gap)
		return err
	}
	if _, err := fmt.Fprintln(c.Writer, "Attached:", record.Type); err != nil {
		return err
	}
	if record.Result != nil {
		if err := output(c, *record.Result, nil); err != nil {
			return err
		}
	}
	return nil
}

// cleanupWait outlasts the daemon's owner join so its typed result, not a client
// deadline, reports slow cleanup.
func cleanupWait(grace time.Duration) time.Duration { return daemon.OwnerJoin(grace) + 10*time.Second }

func runAttached(ctx context.Context, c *urfave.Command, cli *client.Client, graceText string, start func(context.Context) (v1.InstanceResponse, error)) (err error) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	// Acceptance returns authority even when a signal arrives during the request.
	response, err := start(context.WithoutCancel(ctx))
	if err != nil {
		return output(c, nil, err)
	}
	manifestDrift, configDrift := response.ManifestDrift, response.ConfigDrift
	a := response.Attachment
	response.Attachment = nil
	request := v1.AttachedRequest{APIVersion: v1.Version, InstanceID: response.Instance.ID, Owner: a.Owner}
	grace, _ := time.ParseDuration(graceText)
	cleanupAttempted := false
	cleanup := func() (v1.SceneResponse, error) {
		cleanupAttempted = true
		bounded, cancel := context.WithTimeout(context.Background(), cleanupWait(grace))
		defer cancel()
		return cli.Attached(bounded, request, true)
	}
	// Errors (including a closed output pipe) never leave a renewing owner behind.
	defer func() {
		if err == nil {
			return
		}
		var stopped v1.SceneResponse
		var cleanupErr error
		if !cleanupAttempted {
			stopped, cleanupErr = cleanup()
		}
		if cleanupErr == nil && stopped.Result != nil && stopped.Result.Instance.ID == request.InstanceID && startupAttempt(stopped.Result.Instance) == request.Owner.Attempt {
			response = *stopped.Result
			response.ManifestDrift, response.ConfigDrift = manifestDrift, configDrift
		}
		var exit *ExitError
		if !errors.As(err, &exit) || cleanupErr != nil {
			cause := err
			if cleanupErr != nil {
				cause = cleanupErr
			}
			observed := lastObserved(response, cause)
			observed.Error = nil
			if writeErr := attachedRecord(c, v1.AttachedRecord{APIVersion: v1.Version, Type: "failure", Result: &observed, Error: observationError(cause)}); writeErr != nil {
				err = errors.Join(err, writeErr)
			}
		}
		if cleanupErr != nil {
			err = fmt.Errorf("attached cleanup result unavailable: %w", cleanupErr)
		}
	}()
	emit := func(kind string, result *v1.InstanceResponse, log *v1.LogRecord, gap string) error {
		return attachedRecord(c, v1.AttachedRecord{APIVersion: v1.Version, Type: kind, Result: result, Log: log, Gap: gap})
	}
	lease, _ := time.ParseDuration(a.LeaseDuration)
	interval := min(100*time.Millisecond, lease/4)
	logs := v1.LogsRequest{APIVersion: v1.Version, InstanceID: request.InstanceID}
	previous := v1.InstanceStatus("")
	pendingCode := 0
	for {
		code := pendingCode
		pendingCode = 0
		select {
		case received := <-signals:
			code = 130
			if received == syscall.SIGTERM {
				code = 143
			}
		default:
			if ctx.Err() != nil {
				code = 130
			}
		}
		if code != 0 {
			stopped, stopErr := cleanup()
			if stopErr != nil {
				return stopErr
			}
			if stopped.Result == nil || stopped.Result.Instance.ID != request.InstanceID || startupAttempt(stopped.Result.Instance) != request.Owner.Attempt {
				return startupConflict()
			}
			response = *stopped.Result
			response.ManifestDrift, response.ConfigDrift = manifestDrift, configDrift
		}
		instance := response.Instance
		result := instance.Execution
		terminal := instance.Status != v1.Starting && instance.Status != v1.RuntimeReady && instance.Status != v1.Stopping
		failed := result == nil || result.Failure != "" || result.CollectionFailure != "" || result.CleanupFailure != "" || instance.Status == v1.Failed || instance.Status == v1.Interrupted
		if instance.Status != previous && !terminal {
			kind := string(instance.Status)
			if kind != "starting" && kind != "ready" {
				kind = "stopping"
			}
			if err = emit(kind, &response, nil, ""); err != nil {
				return err
			}
			previous = instance.Status
		}
		drainCtx := ctx
		if terminal {
			bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			drainCtx = bounded
		}
		// One bounded page per live iteration; a settled attempt drains available pages.
		for {
			bounded, cancel := context.WithTimeout(drainCtx, min(lease/4, 5*time.Second))
			page, logErr := cli.Logs(bounded, logs)
			cancel()
			if logErr != nil {
				return logErr
			}
			for _, record := range page.Records {
				if record.Attempt == request.Owner.Attempt {
					if err = emit("log", nil, &record, ""); err != nil {
						return err
					}
				}
			}
			if page.Gap != "" {
				if err = emit("gap", nil, nil, page.Gap); err != nil {
					return err
				}
			}
			for _, warning := range page.NativeOwnershipWarnings {
				if err = emit("warning", nil, nil, warning.Message); err != nil {
					return err
				}
			}
			before := logs.Offset
			logs.Offset = page.NextOffset
			if !terminal || logs.Offset == before {
				break
			}
		}
		if terminal {
			kind := "stopped"
			if failed {
				kind = "failure"
			}
			if err = emit(kind, &response, nil, ""); err != nil {
				return err
			}
			if failed {
				return &ExitError{Code: 1}
			}
			if code != 0 {
				return &ExitError{Code: code}
			}
			if instance.Status != v1.Stopped && instance.Status != v1.Destroyed {
				return &ExitError{Code: 1}
			}
			return nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-timer.C:
		case received := <-signals:
			timer.Stop()
			pendingCode = 130
			if received == syscall.SIGTERM {
				pendingCode = 143
			}
		case <-ctx.Done():
			timer.Stop()
		}
		if pendingCode != 0 || ctx.Err() != nil {
			continue
		}
		bounded, cancel := context.WithTimeout(ctx, min(lease/4, 5*time.Second))
		next, renewErr := cli.Attached(bounded, request, false)
		cancel()
		if renewErr != nil {
			if ctx.Err() != nil {
				continue
			}
			return renewErr
		}
		if next.Result == nil || next.Result.Instance.ID != request.InstanceID || startupAttempt(next.Result.Instance) != request.Owner.Attempt {
			return startupConflict()
		}
		response = *next.Result
		response.ManifestDrift, response.ConfigDrift = manifestDrift, configDrift
	}
}
