//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	moby "github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
)

func TestBuildImageHandoffRejectsStaleAttemptOrGeneration(t *testing.T) {
	for _, kind := range []string{"attempt", "generation", "cancelled", "invalid image", "collection failure"} {
		t.Run(kind, func(t *testing.T) {
			s, instance, _ := exportFixture(t)
			attempt := newID()
			result := &v1.ExecutionResult{Attempt: attempt}
			if kind == "collection failure" {
				result.CollectionFailure = "fixture collection gap"
			}
			if _, err := s.store.replaceTestExecution(instance.ID, v1.Starting, result); err != nil {
				t.Fatal(err)
			}
			g := resourceGeneration{ID: newID(), Resources: map[string]ownedResource{}, Initialized: map[string]bool{}}
			if err := s.store.saveResources(instance.ID, g); err != nil {
				t.Fatal(err)
			}
			entry := &execution{resources: &g, acceptImages: true}
			if kind == "attempt" {
				if _, err := s.store.replaceTestExecution(instance.ID, v1.Starting, &v1.ExecutionResult{Attempt: newID()}); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "generation" {
				copy := g
				entry.resources = &copy
				copy.ID = newID()
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if kind == "cancelled" {
					cancel()
				}
				id := "sha256:" + strings.Repeat("a", 64)
				if kind == "invalid image" {
					id = "mutable:tag"
				}
				if err := json.NewEncoder(w).Encode(map[string]string{"Id": id}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			engine, err := moby.New(moby.WithHost(server.URL), moby.WithAPIVersion("1.53"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := engine.Close(); err != nil {
					t.Error(err)
				}
			}()
			entry.docker = &dockerEngine{engine}
			snap := plan.Snapshot{Plan: v1.PlanResponse{BuildImages: map[string]v1.BuildImage{"app": {Producer: "build"}}}}
			before := stateContents(t, s.store)
			if err := s.completeBuildImages(ctx, instance.ID, "build", snap, entry, result); err == nil {
				t.Fatal("unsafe handoff accepted", kind)
			}
			if !reflect.DeepEqual(before, stateContents(t, s.store)) || len(result.BuildImages) != 0 || len(entry.resources.BuildImages) != 0 {
				t.Fatal("rejected handoff mutated retained state")
			}
		})
	}
}
