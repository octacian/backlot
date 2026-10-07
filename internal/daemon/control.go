package daemon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/localipc"
)

// CheckAccess verifies local administrative directory/socket permissions without mutation.
func CheckAccess(directory string) error {
	return localipc.Access(SocketPath(directory))
}

// Start backgrounds this executable's daemon and waits for typed API connectivity.
// A failed startup terminates/reaps only the child it created.
func Start(ctx context.Context, options Options) (v1.DaemonStatusResponse, error) {
	var empty v1.DaemonStatusResponse
	if options.LeaseDuration == 0 {
		options.LeaseDuration = DefaultLeaseDuration
	}
	if options.LeaseDuration < 100*time.Millisecond || options.LeaseDuration > 24*time.Hour {
		return empty, problem("invalid_lease", "lease duration must be between 100ms and 24h")
	}
	if err := localipc.Directory(options.Directory, true); err != nil {
		return empty, err
	}
	c := client.New(SocketPath(options.Directory))
	defer c.Close()
	if _, err := os.Lstat(SocketPath(options.Directory)); err == nil {
		if err := CheckAccess(options.Directory); err != nil {
			return empty, err
		}
		if status, err := c.Status(ctx); err == nil {
			return status, nil
		} else {
			var detail *v1.PlanError
			if !errors.As(err, &detail) || detail.Code != "daemon_unavailable" {
				return empty, err
			}
		}
	} else if !os.IsNotExist(err) {
		return empty, problem("permissions", "cannot inspect daemon socket")
	}
	executable, err := os.Executable()
	if err != nil {
		return empty, err
	}
	logPath := filepath.Join(options.Directory, "daemon.log")
	if _, err := os.Lstat(logPath); err == nil {
		if err := localipc.File(logPath, false); err != nil {
			return empty, err
		}
	} else if !os.IsNotExist(err) {
		return empty, err
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return empty, problem("permissions", "cannot open private daemon startup log")
	}
	command := exec.Command(executable, "daemon", "serve", "--state-dir", options.Directory, "--lease-duration", options.LeaseDuration.String())
	command.Dir = options.Directory
	command.Stdout = log
	command.Stderr = log
	detach(command)
	startErr := command.Start()
	closeErr := log.Close()
	if startErr != nil {
		return empty, errors.Join(problem("start_failed", "cannot spawn daemon; check executable permissions"), closeErr)
	}
	// Startup has a finite owner. After a successful handshake this explicit
	// background daemon is detached; it remains controlled through the socket.
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = command.Process.Kill()
			_ = command.Wait()
			return empty, problem("start_cancelled", "daemon start cancelled")
		case <-deadline.C:
			_ = command.Process.Kill()
			_ = command.Wait()
			return empty, problem("start_failed", "daemon did not become available; inspect private daemon.log")
		case <-ticker.C:
			if err := CheckAccess(options.Directory); err != nil {
				continue
			}
			status, err := c.Status(ctx)
			if err == nil {
				return status, errors.Join(closeErr, command.Process.Release())
			}
		}
	}
}

// Doctor inspects only local permissions, connectivity and versioned state access.
// It never opens the daemon database independently or contacts external providers.
func Doctor(ctx context.Context, directory string) v1.DoctorResponse {
	response := v1.DoctorResponse{APIVersion: v1.Version, Healthy: true, Checks: []v1.DoctorCheck{}}
	add := func(name string, err error, message string) {
		check := v1.DoctorCheck{Name: name, Status: "ok", Message: message}
		if err != nil {
			check.Status = "failed"
			check.Message = err.Error()
			response.Healthy = false
		}
		response.Checks = append(response.Checks, check)
	}
	err := CheckAccess(directory)
	add("permissions", err, "private user-owned daemon directory and socket")
	if err == nil {
		stateErr := localipc.File(filepath.Join(directory, "state.db"), false)
		add("state_permissions", stateErr, "private user-owned database")
		c := client.New(SocketPath(directory))
		defer c.Close()
		_, err = c.Status(ctx)
		add("connectivity_api_state", err, "v1 API and supported state format reachable")
	}
	response.Checks = append(response.Checks, v1.DoctorCheck{Name: "providers", Status: "deferred", Message: "Docker/Caddy checks are deferred; metadata preparation requires neither"})
	return response
}
