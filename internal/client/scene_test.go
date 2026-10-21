package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

func sceneResponse() v1.SceneResponse {
	return v1.SceneResponse{APIVersion: v1.Version, DaemonGeneration: strings.Repeat("a", 64), Outcome: v1.SceneNotFound, Checkout: &v1.CheckoutIdentity{ID: strings.Repeat("b", 64), Path: "/tmp/scene", GitDirectory: "/tmp/scene/.git"}}
}

func sceneInstance(id string) *v1.InstanceResponse {
	return &v1.InstanceResponse{APIVersion: v1.Version, Instance: v1.Instance{ID: strings.Repeat(id, 64), Checkout: *sceneResponse().Checkout, Status: v1.Prepared, CreatedAt: "2026-10-07T12:00:00Z", Operation: v1.Operation{ID: strings.Repeat("c", 64), Status: v1.Prepared, Allocations: []v1.AllocationRecord{{ID: strings.Repeat("d", 64), Kind: "private_snapshot", Phase: "effect"}}}, Plan: v1.PlanResponse{APIVersion: v1.Version, Project: "fixture", Scene: "dev", Lifetime: v1.Persistent, Components: []v1.PlannedComponent{}}}}
}

func sceneWire(t *testing.T, r *http.Request, response v1.SceneResponse) *http.Response {
	t.Helper()
	if r.URL.Path != "/v1/scenes/status" {
		t.Fatal("observer attempted another operation", r.URL.Path)
	}
	var request v1.SceneRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		t.Fatal(err)
	}
	if request.Target.Scene != "dev" || request.APIVersion != v1.Version {
		t.Fatal(request)
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}, "X-Backlot-Api-Version": {v1.Version}}, Body: io.NopCloser(strings.NewReader(string(data)))}
}

func sceneRequest() v1.SceneRequest {
	return v1.SceneRequest{APIVersion: v1.Version, Target: v1.SceneTarget{Scene: "dev", CWD: "/tmp/scene"}}
}

func TestSceneFollowAcrossAbsenceRecreationAndChangedPayload(t *testing.T) {
	c := New("unused")
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		response := sceneResponse()
		switch calls {
		case 1, 2:
			response.Outcome = v1.SceneFound
			response.Result = sceneInstance("e")
		case 3:
			response.Outcome = v1.SceneFound
			response.Result = sceneInstance("e")
			response.Result.ManifestDrift = true
		case 5:
			response.Outcome = v1.SceneFound
			response.Result = sceneInstance("f")
		}
		calls++
		return sceneWire(t, r, response), nil
	})
	var records []v1.SceneFollowRecord
	err := c.FollowScene(ctx, sceneRequest(), SceneFollowOptions{Interval: 100 * time.Millisecond, Duration: 2 * time.Second}, func(r v1.SceneFollowRecord) error {
		records = append(records, r)
		if r.Snapshot != nil && r.Snapshot.Result != nil && r.Snapshot.Result.Instance.ID == strings.Repeat("f", 64) {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || calls != 6 {
		t.Fatal(err, calls)
	}
	if len(records) != 6 || records[0].Snapshot.Outcome != v1.SceneNotFound || records[2].Snapshot.Result.ManifestDrift != true || records[3].Snapshot.Outcome != v1.SceneNotFound || records[5].Type != "complete" || records[5].Reason != "cancelled" {
		t.Fatalf("unexpected framing: %+v", records)
	}
}

func TestSceneFollowRejectsContinuityChangesEvenInitiallyAbsent(t *testing.T) {
	for _, changed := range []string{"checkout", "path", "git", "daemon"} {
		t.Run(changed, func(t *testing.T) {
			c := New("unused")
			defer c.Close()
			calls := 0
			c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				response := sceneResponse()
				if calls > 0 {
					switch changed {
					case "checkout":
						response.Checkout.ID = strings.Repeat("c", 64)
					case "path":
						response.Checkout.Path = "/tmp/moved"
					case "git":
						response.Checkout.GitDirectory = "/tmp/moved/.git"
					case "daemon":
						response.DaemonGeneration = strings.Repeat("c", 64)
					}
				}
				calls++
				return sceneWire(t, r, response), nil
			})
			var records []v1.SceneFollowRecord
			err := c.FollowScene(context.Background(), sceneRequest(), SceneFollowOptions{Interval: 100 * time.Millisecond, Duration: time.Second}, func(r v1.SceneFollowRecord) error { records = append(records, r); return nil })
			if err == nil || calls != 2 || len(records) != 2 || records[0].Snapshot == nil || records[1].Type != "error" {
				t.Fatal(err, calls, records)
			}
		})
	}
}

func TestSceneFollowExpiryCancellationAndFailurePrecedence(t *testing.T) {
	for _, mode := range []string{"idle-expiry", "inflight-expiry", "cancel", "failure-after-cancel", "daemon-loss", "writer"} {
		t.Run(mode, func(t *testing.T) {
			c := New("unused")
			defer c.Close()
			calls := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sentinel := errors.New("output closed")
			c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				switch mode {
				case "inflight-expiry":
					<-r.Context().Done()
					return nil, r.Context().Err()
				case "cancel":
					cancel()
					return nil, context.Canceled
				case "failure-after-cancel":
					cancel()
					return nil, &v1.PlanError{Code: "daemon_unavailable", Message: "failed before local cancellation"}
				case "daemon-loss":
					if calls > 1 {
						return nil, errors.New("connection lost")
					}
				}
				return sceneWire(t, r, sceneResponse()), nil
			})
			var records []v1.SceneFollowRecord
			err := c.FollowScene(ctx, sceneRequest(), SceneFollowOptions{Interval: 100 * time.Millisecond, Duration: time.Second}, func(r v1.SceneFollowRecord) error {
				records = append(records, r)
				if mode == "writer" {
					return sentinel
				}
				return nil
			})
			last := records[len(records)-1]
			switch mode {
			case "idle-expiry", "inflight-expiry":
				if err != nil || last.Type != "complete" || last.Reason != "expired" {
					t.Fatal(err, records)
				}
			case "cancel":
				if !errors.Is(err, context.Canceled) || last.Reason != "cancelled" {
					t.Fatal(err, records)
				}
			case "writer":
				if !errors.Is(err, sentinel) || len(records) != 1 {
					t.Fatal(err, records)
				}
			default:
				if err == nil || last.Type != "error" || last.Error.Code != "daemon_unavailable" {
					t.Fatal(err, records)
				}
			}
			if mode == "idle-expiry" && len(records) != 2 {
				t.Fatal("idle polling emitted duplicates", records)
			}
		})
	}
}

func TestSceneResponseValidationRejectsMissingBaselineLeaksAndMismatches(t *testing.T) {
	for _, mode := range []string{"baseline", "generation", "found-empty", "missing-with-instance", "secret", "wrong-scene", "wrong-id", "wrong-checkout", "disposable", "wrong-version"} {
		t.Run(mode, func(t *testing.T) {
			response := sceneResponse()
			response.Outcome = v1.SceneFound
			response.Result = sceneInstance("e")
			target := sceneRequest().Target
			switch mode {
			case "baseline":
				response.Checkout = nil
			case "generation":
				response.DaemonGeneration = "invalid"
			case "found-empty":
				response.Result = nil
			case "missing-with-instance":
				response.Outcome = v1.SceneNotFound
			case "secret":
				response.Result.Instance.Operation.Allocations[0].OwnershipToken = strings.Repeat("a", 64)
			case "wrong-scene":
				response.Result.Instance.Plan.Scene = "other"
			case "wrong-id":
				target = v1.SceneTarget{InstanceID: strings.Repeat("f", 64)}
				response.Checkout = nil
			case "wrong-checkout":
				response.Checkout.ID = strings.Repeat("d", 64)
			case "disposable":
				response.Result.Instance.Plan.Lifetime = v1.Disposable
			case "wrong-version":
				response.Result.APIVersion = "v2"
			}
			if err := validateScene(response, target); err == nil {
				t.Fatal("invalid response accepted", mode)
			}
		})
	}
}

func TestSceneFollowBounds(t *testing.T) {
	for _, options := range []SceneFollowOptions{{Interval: -1}, {Interval: 99 * time.Millisecond}, {Interval: time.Minute + 1}, {Duration: -1}, {Duration: time.Second - 1}, {Duration: 24*time.Hour + 1}} {
		if err := options.Validate(); err == nil {
			t.Fatal("accepted invalid budget", options)
		}
	}
	for _, options := range []SceneFollowOptions{{}, {Interval: 100 * time.Millisecond, Duration: time.Second}, {Interval: time.Minute, Duration: 24 * time.Hour}} {
		if err := options.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}
