package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestTaskWireBindingAndUnknownAcceptance(t *testing.T) {
	for _, failure := range []string{"valid", "parent", "task", "missing", "lost"} {
		t.Run(failure, func(t *testing.T) {
			c := New("unused")
			defer c.Close()
			calls := 0
			q := v1.TaskRunRequest{APIVersion: v1.Version, Target: v1.SceneTarget{InstanceID: strings.Repeat("a", 64)}, Task: "admin", Args: []string{"two words", "--literal"}}
			c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var payload any
				if r.URL.Path == "/v1/status" {
					payload = v1.DaemonStatusResponse{APIVersion: v1.Version, StateVersion: v1.StateVersion, PID: 1, Status: "running", LeaseDuration: "1m"}
				} else {
					calls++
					if r.URL.Path != "/v1/tasks/run" {
						t.Fatal(r.URL.Path)
					}
					var got v1.TaskRunRequest
					if json.NewDecoder(r.Body).Decode(&got) != nil || got.Task != q.Task || len(got.Args) != 2 || got.Args[0] != "two words" {
						t.Fatal(got)
					}
					if failure == "lost" {
						return nil, io.ErrUnexpectedEOF
					}
					response := *sceneInstance("b")
					i := &response.Instance
					i.Plan.Lifetime = v1.Disposable
					i.Status = v1.Starting
					i.Operation.Status = v1.Starting
					i.Execution = &v1.ExecutionResult{Attempt: strings.Repeat("c", 64), Components: []v1.ComponentResult{}}
					i.LeaseExpiresAt = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
					response.LeaseToken = strings.Repeat("d", 64)
					i.Task = &v1.TaskBinding{InstanceID: q.Target.InstanceID, Attempt: strings.Repeat("e", 64), Generation: strings.Repeat("f", 64), Name: q.Task}
					switch failure {
					case "parent":
						i.Task.InstanceID = strings.Repeat("e", 64)
					case "task":
						i.Task.Name = "other"
					case "missing":
						i.Task = nil
					}
					payload = response
				}
				data, _ := json.Marshal(payload)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}, "X-Backlot-Api-Version": {v1.Version}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
			})
			_, err := c.RunTask(context.Background(), q)
			if (err == nil) != (failure == "valid") {
				t.Fatal(err)
			}
			if failure != "valid" && (!strings.Contains(err.Error(), "unknown") || !strings.Contains(err.Error(), "do not automatically retry")) {
				t.Fatal("missing uncertainty", err)
			}
			if calls != 1 {
				t.Fatal("mutation retried", calls)
			}
		})
	}
}
