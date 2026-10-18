package daemon

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// The Engine fixture replaces a name immediately after returning its authenticated
// inspection. It models both an original renamed aside and an original removed
// at each later request, without depending on scheduler timing or real Engine races.
func TestDoctorRecoveryPinsEngineIdentity(t *testing.T) {
	for _, kind := range []string{"container", "network"} {
		for _, scenario := range []string{"renamed", "gone-before-reverify", "gone-at-stop", "gone-at-state", "gone-at-remove", "uncertain", "recorded", "recorded-mismatch", "invalid-id", "wrong-label"} {
			if kind == "network" && (scenario == "gone-at-stop" || scenario == "gone-at-state") {
				continue
			}
			t.Run(kind+"/"+scenario, func(t *testing.T) {
				root, err := os.MkdirTemp("/tmp", "bl-id-")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.RemoveAll(root); err != nil {
						t.Error(err)
					}
				})
				state, err := openStore(root)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := state.db.Close(); err != nil {
						t.Error(err)
					}
				})
				key, token := newID(), newID()
				originalID, replacementID := strings.Repeat("a", 64), strings.Repeat("b", 64)
				resource := ownedResource{Kind: kind, Name: "backlot-doctor-" + key, Token: token}
				if kind == "network" {
					resource.Name = "backlot-doctor-network-" + key
				}
				if strings.HasPrefix(scenario, "recorded") {
					resource.ID = originalID
				}
				var mu sync.Mutex
				var requests []string
				originalPresent, originalRunning := true, kind == "container"
				replacementPresent, replacementRunning := false, kind == "container"
				replacementLabels := map[string]string{"io.backlot.owner": newID()}
				foreignLabels := map[string]string{"io.backlot.owner": replacementLabels["io.backlot.owner"]}
				observed := false
				inspections := 0
				listener, err := net.Listen("unix", filepath.Join(root, "engine.sock"))
				if err != nil {
					t.Fatal(err)
				}
				server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					if r.URL.Path == "/_ping" {
						w.Header().Set("API-Version", "1.53")
						return
					}
					path := r.URL.Path
					if strings.HasPrefix(path, "/v1.53/") {
						path = strings.TrimPrefix(path, "/v1.53")
					}
					prefix := "/containers/"
					if kind == "network" {
						prefix = "/networks/"
					}
					if !strings.HasPrefix(path, prefix) {
						http.NotFound(w, r)
						return
					} // absent probe container in network-only journal
					tail := strings.TrimPrefix(path, prefix)
					handle, action, _ := strings.Cut(tail, "/")
					requests = append(requests, r.Method+" "+handle+"/"+action)
					if observed && handle != originalID {
						t.Errorf("request after authentication used mutable/wrong handle: %s %s", r.Method, path)
					}
					isInspect := r.Method == http.MethodGet
					if isInspect {
						inspections++
					}
					if observed && scenario == "uncertain" {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					if observed && ((scenario == "gone-before-reverify" && inspections == 2) || (scenario == "gone-at-stop" && action == "stop") || (scenario == "gone-at-state" && inspections == 3) || (scenario == "gone-at-remove" && r.Method == http.MethodDelete)) {
						originalPresent = false
					}
					isReplacement := handle == replacementID || (handle == resource.Name && replacementPresent)
					if (isReplacement && !replacementPresent) || (!isReplacement && !originalPresent) {
						http.NotFound(w, r)
						return
					}
					if isInspect {
						id, labels, running := originalID, map[string]string{"io.backlot.owner": token}, originalRunning
						if isReplacement {
							id, labels, running = replacementID, replacementLabels, replacementRunning
						}
						if !observed {
							switch scenario {
							case "recorded-mismatch":
								id = replacementID
							case "invalid-id":
								id = "short-id"
							case "wrong-label":
								labels = replacementLabels
							}
						}
						var response any = map[string]any{"Id": id, "Labels": labels}
						if kind == "container" {
							response = map[string]any{"Id": id, "Config": map[string]any{"Labels": labels}, "State": map[string]any{"Running": running}}
						}
						_ = json.NewEncoder(w).Encode(response)
						if !observed {
							observed = true
							replacementPresent = true
						}
						return
					}
					switch {
					case r.Method == http.MethodPost && action == "stop":
						if isReplacement {
							replacementRunning = false
						} else {
							originalRunning = false
						}
						w.WriteHeader(http.StatusNoContent)
					case r.Method == http.MethodDelete:
						if isReplacement {
							replacementPresent = false
						} else {
							originalPresent = false
						}
						w.WriteHeader(http.StatusNoContent)
					default:
						t.Errorf("unexpected Engine request %s %s", r.Method, path)
						http.NotFound(w, r)
					}
				})}
				served := make(chan struct{})
				go func() { defer close(served); _ = server.Serve(listener) }()
				t.Cleanup(func() { _ = server.Close(); <-served })
				record := diagnosticProbeRecord{Endpoint: "unix://" + filepath.Join(root, "engine.sock"), Resource: ownedResource{Kind: "container", Name: "backlot-doctor-" + key, Token: newID()}}
				if kind == "container" {
					record.Resource = resource
				} else {
					record.Network = resource
				}
				if err := state.saveDiagnosticProbe(key, record); err != nil {
					t.Fatal(err)
				}
				recoveryErr := state.recoverDiagnosticProbes()
				pending, err := state.diagnosticPending(nil)
				if err != nil {
					t.Fatal(err)
				}
				uncertain := scenario == "uncertain" || scenario == "recorded-mismatch" || scenario == "invalid-id" || scenario == "wrong-label"
				if (recoveryErr != nil) != uncertain || pending != uncertain {
					t.Fatalf("recovery error=%v pending=%v want uncertainty=%v", recoveryErr, pending, uncertain)
				}
				mu.Lock()
				defer mu.Unlock()
				if !replacementPresent || replacementRunning != (kind == "container") || !reflect.DeepEqual(replacementLabels, foreignLabels) {
					t.Fatal("same-name foreign replacement was changed")
				}
				if originalPresent != uncertain {
					t.Fatalf("original presence=%v uncertainty=%v", originalPresent, uncertain)
				}
				if !observed || len(requests) == 0 {
					t.Fatal("ownership observation did not run")
				}
				firstHandle := resource.Name
				if resource.ID != "" {
					firstHandle = resource.ID
				}
				suffix := "/"
				if kind == "container" {
					suffix = "/json"
				}
				expected := []string{"GET " + firstHandle + suffix}
				if scenario == "renamed" || scenario == "recorded" {
					expected = append(expected, "GET "+originalID+suffix)
					if kind == "container" {
						expected = append(expected, "POST "+originalID+"/stop", "GET "+originalID+"/json")
					}
					expected = append(expected, "DELETE "+originalID+"/")
				} else if scenario == "uncertain" || scenario == "gone-before-reverify" {
					expected = append(expected, "GET "+originalID+suffix)
				} else if strings.HasPrefix(scenario, "gone-at-") {
					expected = append(expected, "GET "+originalID+suffix)
					if kind == "container" {
						expected = append(expected, "POST "+originalID+"/stop")
						if scenario != "gone-at-stop" {
							expected = append(expected, "GET "+originalID+"/json")
						}
					}
					if scenario == "gone-at-remove" {
						expected = append(expected, "DELETE "+originalID+"/")
					}
				}
				if !reflect.DeepEqual(requests, expected) {
					t.Fatalf("Engine requests\n got %v\nwant %v", requests, expected)
				}
			})
		}
	}
}
