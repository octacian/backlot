package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestBuildImageDecodedProvenance(t *testing.T) {
	changes := map[string]func(*v1.ExecutionResult){
		"reused source attempt": func(*v1.ExecutionResult) {},
		"historical absence":    func(r *v1.ExecutionResult) { r.BuildImages = nil; r.ImageChangeWarnings = nil },
		"same ID distinct images": func(r *v1.ExecutionResult) {
			other := r.BuildImages[0]
			other.Name = "other"
			other.Tag = strings.TrimSuffix(other.Tag, "app") + "other"
			r.BuildImages = append(r.BuildImages, other)
		},
		"empty image":        func(r *v1.ExecutionResult) { r.BuildImages = []v1.BuildImageResult{{}} },
		"bad image name":     func(r *v1.ExecutionResult) { r.BuildImages[0].Name = "Bad" },
		"bad producer":       func(r *v1.ExecutionResult) { r.BuildImages[0].Producer = "" },
		"bad source attempt": func(r *v1.ExecutionResult) { r.BuildImages[0].Attempt = "unknown" },
		"mutable image":      func(r *v1.ExecutionResult) { r.BuildImages[0].ID = "app:latest" },
		"uppercase ID":       func(r *v1.ExecutionResult) { r.BuildImages[0].ID = "sha256:" + strings.Repeat("A", 64) },
		"foreign tag": func(r *v1.ExecutionResult) {
			r.BuildImages[0].Tag = strings.Replace(r.BuildImages[0].Tag, strings.Repeat("e", 64), strings.Repeat("f", 64), 1)
		},
		"duplicate image":        func(r *v1.ExecutionResult) { r.BuildImages = append(r.BuildImages, r.BuildImages[0]) },
		"orphan warning":         func(r *v1.ExecutionResult) { r.ImageChangeWarnings[0].Image = "missing" },
		"mismatched replacement": func(r *v1.ExecutionResult) { r.ImageChangeWarnings[0].ID = "sha256:" + strings.Repeat("d", 64) },
		"same previous ID":       func(r *v1.ExecutionResult) { r.ImageChangeWarnings[0].PreviousID = r.ImageChangeWarnings[0].ID },
		"bad previous ID":        func(r *v1.ExecutionResult) { r.ImageChangeWarnings[0].PreviousID = "app:old" },
		"bad consumer":           func(r *v1.ExecutionResult) { r.ImageChangeWarnings[0].Component = "" },
		"duplicate warning": func(r *v1.ExecutionResult) {
			r.ImageChangeWarnings = append(r.ImageChangeWarnings, r.ImageChangeWarnings[0])
		},
		"empty diagnostic":     func(r *v1.ExecutionResult) { r.ImageChangeWarnings[0].Message = "" },
		"multiline diagnostic": func(r *v1.ExecutionResult) { r.ImageChangeWarnings[0].Message = "line\nline" },
		"oversize diagnostic":  func(r *v1.ExecutionResult) { r.ImageChangeWarnings[0].Message = strings.Repeat("a", 257) },
	}
	for name, change := range changes {
		for _, route := range []string{"instance", "scene", "export"} {
			t.Run(name+"/"+route, func(t *testing.T) {
				response := sceneInstance("e")
				id := response.Instance.ID
				response.Instance.Status = v1.Failed
				response.Instance.Operation.Status = v1.Failed
				response.Instance.Execution = &v1.ExecutionResult{
					Attempt: strings.Repeat("b", 64), Components: []v1.ComponentResult{{Name: "current-build", Status: "completed"}},
					BuildImages: []v1.BuildImageResult{{Name: "app", Producer: "original-build", Attempt: strings.Repeat("c", 64), Tag: "backlot-build/" + id + ":" + strings.Repeat("c", 64) + "-app", ID: "sha256:" + strings.Repeat("a", 64)}},
					// Acceptance can warn before this consumer is launched; no manifest binding.
					ImageChangeWarnings: []v1.ImageChangeWarning{{Component: "pending-consumer", Image: "app", PreviousID: "sha256:" + strings.Repeat("b", 64), ID: "sha256:" + strings.Repeat("a", 64), Message: "application/data compatibility risk"}},
				}
				change(response.Instance.Execution)
				var wire any = response
				if route == "scene" {
					scene := sceneResponse()
					scene.Outcome = v1.SceneFound
					scene.Result = response
					wire = scene
				}
				if route == "export" {
					r := response.Instance.Execution
					wire = v1.ExportResponse{APIVersion: v1.Version, Published: true, Complete: true, Output: "/caller/bundle", Report: "report.json", HTML: "index.html", Result: v1.ExportResult{InstanceID: id, Attempt: r.Attempt, Status: v1.Failed, Components: r.Components, BuildImages: r.BuildImages, ImageChangeWarnings: r.ImageChangeWarnings}}
				}
				data, err := json.Marshal(wire)
				if err != nil {
					t.Fatal(err)
				}
				c := New("unused")
				defer c.Close()
				c.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}, "X-Backlot-Api-Version": {v1.Version}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
				})
				switch route {
				case "instance":
					_, err = c.Inspect(context.Background(), id)
				case "scene":
					_, err = c.SceneStatus(context.Background(), sceneRequest())
				case "export":
					_, err = c.Export(context.Background(), v1.ExportRequest{APIVersion: v1.Version, InstanceID: id, Output: "/caller/bundle"})
				}
				valid := name == "reused source attempt" || name == "historical absence" || name == "same ID distinct images"
				if valid {
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				var detail *v1.PlanError
				if !errors.As(err, &detail) || detail.Code != "invalid_response" {
					t.Fatal("malformed decoded provenance accepted", err)
				}
			})
		}
	}
}
