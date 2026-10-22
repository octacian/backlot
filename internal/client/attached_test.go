package client

import (
	"context"
	"encoding/json"
	v1 "github.com/octacian/backlot/api/v1"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAttachmentResponseAuthority(t *testing.T) {
	id := strings.Repeat("a", 64)
	valid := func() v1.InstanceResponse {
		return v1.InstanceResponse{Instance: v1.Instance{Plan: v1.PlanResponse{Lifetime: v1.Persistent}, Execution: &v1.ExecutionResult{Attempt: id, Components: []v1.ComponentResult{}}}, Attachment: &v1.Attachment{Owner: v1.AttachedOwner{DaemonGeneration: id, Attempt: id, Token: id}, ExpiresAt: time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano), LeaseDuration: "30s"}}
	}
	if err := validateAttachment(valid(), true); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*v1.InstanceResponse){
		func(r *v1.InstanceResponse) { r.Attachment = nil },
		func(r *v1.InstanceResponse) { r.Attachment.Owner.Attempt = strings.Repeat("b", 64) },
		func(r *v1.InstanceResponse) { r.Attachment.Owner.DaemonGeneration = "bad" },
		func(r *v1.InstanceResponse) { r.Attachment.Owner.Token = "bad" },
		func(r *v1.InstanceResponse) { r.Attachment.ExpiresAt = "bad" },
		func(r *v1.InstanceResponse) { r.Attachment.LeaseDuration = "0s" },
		func(r *v1.InstanceResponse) { r.Instance.Plan.Lifetime = v1.Disposable },
		func(r *v1.InstanceResponse) { r.Instance.Execution = nil },
	} {
		r := valid()
		mutate(&r)
		if err := validateAttachment(r, true); err == nil {
			t.Fatal("malformed authority accepted")
		}
	}
	if err := validateAttachment(valid(), false); err == nil {
		t.Fatal("ordinary response returned authority")
	}
}

func TestAttachedResponseCannotFollowReplacement(t *testing.T) {
	id := strings.Repeat("a", 64)
	request := v1.AttachedRequest{APIVersion: v1.Version, InstanceID: strings.Repeat("e", 64), Owner: v1.AttachedOwner{DaemonGeneration: id, Attempt: id, Token: id}}
	for _, changed := range []string{"none", "generation", "attempt", "authority"} {
		t.Run(changed, func(t *testing.T) {
			c := New("unused")
			defer c.Close()
			c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/v1/attached/renew" {
					t.Fatal("wrong attached operation", r.URL.Path)
				}
				var decoded v1.AttachedRequest
				if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil || decoded != request {
					t.Fatal("authority request changed", err)
				}
				response := v1.SceneResponse{APIVersion: v1.Version, DaemonGeneration: id, Outcome: v1.SceneFound, Result: sceneInstance("e")}
				response.Result.Instance.Status, response.Result.Instance.Operation.Status = v1.Stopped, v1.Stopped
				response.Result.Instance.Execution = &v1.ExecutionResult{Attempt: id, Components: []v1.ComponentResult{}}
				switch changed {
				case "generation":
					response.DaemonGeneration = strings.Repeat("b", 64)
				case "attempt":
					response.Result.Instance.Execution.Attempt = strings.Repeat("b", 64)
				case "authority":
					response.Result.Attachment = &v1.Attachment{}
				}
				data, err := json.Marshal(response)
				if err != nil {
					t.Fatal(err)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}, "X-Backlot-Api-Version": {v1.Version}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
			})
			_, err := c.Attached(context.Background(), request, false)
			if (err == nil) != (changed == "none") {
				t.Fatal("replacement response validation", err)
			}
		})
	}
}
