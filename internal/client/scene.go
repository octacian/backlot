package client

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

// StartScene accepts persistent startup through the existing daemon coordinator.
func (c *Client) StartScene(ctx context.Context, request v1.SceneStartRequest) (v1.SceneResponse, error) {
	var response v1.SceneResponse
	err := c.call(ctx, http.MethodPost, "/v1/scenes/start", request, &response)
	if err == nil {
		err = validateScene(response, v1.SceneTarget{Scene: request.Plan.Scene, CWD: request.CWD, ProjectPath: request.Plan.ProjectPath})
	}
	if err == nil && (response.Outcome != v1.SceneFound || response.Result == nil) {
		err = responseError()
	}
	return response, err
}

// SceneStatus reads recorded persistent state without current manifest inputs.
func (c *Client) SceneStatus(ctx context.Context, request v1.SceneRequest) (v1.SceneResponse, error) {
	return c.sceneCall(ctx, "status", request)
}

// StopScene stops one daemon-resolved persistent target, retaining data.
func (c *Client) StopScene(ctx context.Context, request v1.SceneRequest) (v1.SceneResponse, error) {
	return c.sceneCall(ctx, "stop", request)
}

func (c *Client) sceneCall(ctx context.Context, action string, request v1.SceneRequest) (v1.SceneResponse, error) {
	var response v1.SceneResponse
	if err := request.Target.Validate(); err != nil {
		return response, err
	}
	err := c.call(ctx, http.MethodPost, "/v1/scenes/"+action, request, &response)
	if err == nil {
		err = validateScene(response, request.Target)
	}
	if err == nil && (action == "status" && response.Outcome != v1.SceneFound && response.Outcome != v1.SceneNotFound || action == "stop" && response.Outcome == v1.SceneFound) {
		err = responseError()
	}
	return response, err
}

func validateScene(response v1.SceneResponse, target v1.SceneTarget) error {
	if response.APIVersion != v1.Version || !validID(response.DaemonGeneration) {
		return responseError()
	}
	switch response.Outcome {
	case v1.SceneFound, v1.SceneStopped, v1.SceneAlreadyStopped:
		if response.Result == nil {
			return responseError()
		}
	case v1.SceneNotFound:
		if response.Result != nil {
			return responseError()
		}
	default:
		return responseError()
	}
	if target.InstanceID == "" {
		checkout := response.Checkout
		if checkout == nil || !validID(checkout.ID) || !filepath.IsAbs(checkout.Path) || checkout.GitDirectory != "" && !filepath.IsAbs(checkout.GitDirectory) {
			return responseError()
		}
	} else if response.Checkout != nil {
		return responseError()
	}
	if response.Result != nil {
		if response.Result.APIVersion != v1.Version {
			return responseError()
		}
		if err := validateInstance(*response.Result); err != nil {
			return err
		}
		i := response.Result.Instance
		if i.Plan.Lifetime != v1.Persistent || response.Result.LeaseToken != "" || target.InstanceID != "" && i.ID != target.InstanceID || target.Scene != "" && i.Plan.Scene != target.Scene {
			return responseError()
		}
		if response.Checkout != nil && !checkoutEqual(*response.Checkout, i.Checkout) {
			return responseError()
		}
	}
	return nil
}

func checkoutEqual(a, b v1.CheckoutIdentity) bool {
	return a.ID == b.ID && a.Path == b.Path && a.GitDirectory == b.GitDirectory
}

// SceneFollowOptions bounds sampled observation independently of runtime lifetime.
// Zero values select 1s intervals and a 30m total budget.
type SceneFollowOptions struct{ Interval, Duration time.Duration }

// Validate checks finite observation bounds after applying defaults.
func (o SceneFollowOptions) Validate() error {
	if o.Interval == 0 {
		o.Interval = time.Second
	}
	if o.Duration == 0 {
		o.Duration = 30 * time.Minute
	}
	if o.Interval < 100*time.Millisecond || o.Interval > time.Minute || o.Duration < time.Second || o.Duration > 24*time.Hour {
		return &v1.PlanError{Code: "invalid_observation", Message: "follow interval must be 100ms through 1m and duration 1s through 24h"}
	}
	return nil
}

var errObservationExpired = errors.New("observation duration expired")

// FollowScene emits initial and changed sampled states, then a terminal record.
// Named targets can observe new instances in the same checkout; explicit IDs stay
// pinned. Closing/cancelling the observer never issues a lifecycle mutation.
func (c *Client) FollowScene(ctx context.Context, request v1.SceneRequest, options SceneFollowOptions, emit func(v1.SceneFollowRecord) error) error {
	if err := options.Validate(); err != nil {
		return err
	}
	if options.Interval == 0 {
		options.Interval = time.Second
	}
	if options.Duration == 0 {
		options.Duration = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeoutCause(ctx, options.Duration, errObservationExpired)
	defer cancel()
	terminal := func(err error) error {
		record := v1.SceneFollowRecord{APIVersion: v1.Version, Time: time.Now().UTC().Format(time.RFC3339Nano), Type: "complete"}
		if errors.Is(err, errObservationExpired) {
			record.Reason = "expired"
			err = nil
		} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			record.Reason = "cancelled"
		} else {
			record.Type = "error"
			record.Error = &v1.PlanError{Code: "observation_failed", Message: "status observation failed"}
			var detail *v1.PlanError
			if errors.As(err, &detail) {
				record.Error = detail
			}
		}
		if writeErr := emit(record); writeErr != nil {
			return writeErr
		}
		return err
	}
	var previous *v1.SceneResponse
	for {
		if err := context.Cause(ctx); err != nil {
			return terminal(err)
		}
		response, err := c.SceneStatus(ctx, request)
		if err != nil {
			return terminal(err)
		}
		if previous != nil {
			if response.DaemonGeneration != previous.DaemonGeneration {
				return terminal(&v1.PlanError{Code: "daemon_changed", Message: "daemon generation changed; start a new observation"})
			}
			if request.Target.InstanceID == "" && !checkoutEqual(*previous.Checkout, *response.Checkout) {
				return terminal(&v1.PlanError{Code: "checkout_changed", Message: "checkout changed during observation; use recorded instance ID"})
			}
		}
		if previous == nil || !reflect.DeepEqual(*previous, response) {
			if err := emit(v1.SceneFollowRecord{APIVersion: v1.Version, Type: "snapshot", Time: time.Now().UTC().Format(time.RFC3339Nano), Snapshot: &response}); err != nil {
				return err
			}
		}
		previous = &response
		timer := time.NewTimer(options.Interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return terminal(context.Cause(ctx))
		case <-timer.C:
		}
	}
}
