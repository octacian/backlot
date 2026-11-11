//go:build !windows

package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	bolt "go.etcd.io/bbolt"
)

func TestContainerReleaseRequiresExactAbsence(t *testing.T) {
	for _, mode := range []string{"removed", "already-absent", "pending-name", "still-present", "inspection-unavailable", "foreign-token", "wrong-id", "collection-only", "collection-and-cleanup"} {
		t.Run(mode, func(t *testing.T) {
			id, token := newID(), newID()
			r := ownedResource{Kind: "container", ID: id, Name: "backlot-pending", Token: token, Component: "server", Attempt: newID()}
			if mode == "pending-name" {
				r.ID = ""
			}
			removed, deletes, finalInspections := false, 0, 0
			d := forceEngine(t, func(w http.ResponseWriter, req *http.Request) {
				switch {
				case strings.HasSuffix(req.URL.Path, "/json"):
					if removed {
						finalInspections++
					}
					if mode == "already-absent" || removed && mode != "still-present" && mode != "inspection-unavailable" && mode != "collection-and-cleanup" {
						w.WriteHeader(404)
						_, _ = w.Write([]byte(`{"message":"absent"}`))
						return
					}
					if removed && (mode == "inspection-unavailable" || mode == "collection-and-cleanup") {
						w.WriteHeader(503)
						_, _ = w.Write([]byte(`{"message":"private-provider-detail"}`))
						return
					}
					observedID, observedToken := id, token
					if mode == "wrong-id" {
						observedID = newID()
					}
					if mode == "foreign-token" {
						observedToken = newID()
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"Id": observedID, "Config": map[string]any{"Labels": map[string]string{"io.backlot.owner": observedToken}}, "State": map[string]any{"Running": false}})
				case strings.HasSuffix(req.URL.Path, "/logs"):
					w.WriteHeader(503)
					_, _ = w.Write([]byte(`{"message":"private-log-detail"}`))
				case strings.HasSuffix(req.URL.Path, "/stop"):
					if req.URL.Path != "/containers/"+id+"/stop" {
						t.Error("stop targeted an unpinned name")
					}
					w.WriteHeader(204)
				case req.Method == http.MethodDelete:
					if req.URL.Path != "/containers/"+id || req.URL.Query().Get("force") == "1" {
						t.Error("removal broadened target/authority")
					}
					removed = true
					deletes++
					w.WriteHeader(204)
				default:
					t.Error("unexpected request", req.Method, req.URL.Path)
					w.WriteHeader(500)
				}
			})
			var collection, cleanup error
			if strings.HasPrefix(mode, "collection-") {
				state := &store{directory: shortTemp(t)}
				collection, cleanup = state.recoverContainer(context.Background(), newID(), d, r)
				if collection == nil || collection.Error() != "recovery container logs could not be retained" {
					t.Fatal("collection gap missing or exposed raw detail")
				}
			} else {
				cleanup = removeContainer(context.Background(), d, r)
			}
			wantFailure := mode == "still-present" || mode == "inspection-unavailable" || mode == "foreign-token" || mode == "wrong-id" || mode == "collection-and-cleanup"
			if (cleanup != nil) != wantFailure {
				t.Fatal("incorrect cleanup result", cleanup)
			}
			noDelete := mode == "foreign-token" || mode == "wrong-id" || mode == "already-absent"
			if noDelete && deletes != 0 || !noDelete && (deletes != 1 || finalInspections == 0) {
				t.Fatal("ownership/removal/absence checks missing", deletes, finalInspections)
			}
		})
	}
}

func TestInterruptedRecoverySelectsOnlyDurableObligations(t *testing.T) {
	state, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.db.Close() }()
	id, attempt := newID(), newID()
	for _, mode := range []string{"empty-legacy", "pending-launch", "pending-container", "removed-container", "pending-network", "retained-volume", "ordinary-stopped", "no-execution"} {
		t.Run(mode, func(t *testing.T) {
			r := record{Instance: v1.Instance{ID: id, Status: v1.Interrupted, Execution: &v1.ExecutionResult{Attempt: attempt}}}
			journal := runtimeRecord{Attempt: attempt}
			generation := resourceGeneration{ID: newID(), Resources: map[string]ownedResource{}, Initialized: map[string]bool{}}
			switch mode {
			case "pending-launch":
				journal.LaunchPending = true
			case "pending-container", "removed-container":
				generation.Containers = []ownedResource{{Kind: "container", Name: "owned", Token: newID(), Attempt: attempt, Removed: mode == "removed-container"}}
			case "pending-network", "ordinary-stopped":
				generation.Resources["network"] = ownedResource{Kind: "network", Name: "owned", Token: newID()}
				if mode == "ordinary-stopped" {
					r.Instance.Status = v1.Stopped
				}
			case "retained-volume":
				generation.Resources["data"] = ownedResource{Kind: "volume", Name: "owned", Token: newID()}
			case "no-execution":
				r.Instance.Execution = nil
			}
			var needed bool
			if err := state.db.Update(func(tx *bolt.Tx) error {
				if err := put(tx, "runtime", id, journal); err != nil {
					return err
				}
				if err := put(tx, "resources", id, generation); err != nil {
					return err
				}
				var err error
				needed, err = runtimeRecoveryRequired(tx, r)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			want := mode == "pending-launch" || mode == "pending-container" || mode == "pending-network"
			if needed != want {
				t.Fatal("wrong recovery selection", needed, want)
			}
		})
	}
}
