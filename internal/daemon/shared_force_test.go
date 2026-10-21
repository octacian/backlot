//go:build !windows

package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	bolt "go.etcd.io/bbolt"
)

func TestForceWireStrictness(t *testing.T) {
	h := handler(&service{}, v1.DaemonStatusResponse{}, func() {})
	perform := func(path, body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("X-Backlot-API-Version", v1.Version)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code < 400 || strings.Contains(w.Body.String(), "private-sentinel") {
			t.Fatal("invalid force request accepted or echoed", path, w.Body.String())
		}
		var result v1.ErrorResponse
		if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Error.Code == "" {
			t.Fatal("invalid error envelope")
		}
	}
	for _, path := range []string{"/v1/runtime/stop", "/v1/shared/list", "/v1/shared/inspect", "/v1/shared/start", "/v1/shared/stop", "/v1/shared/restart", "/v1/reset", "/v1/restart", "/v1/run"} {
		for _, value := range []string{"true", "false"} {
			perform(path, `{"api_version":"v1","force":`+value+`}`)
		}
	}
	for _, path := range []string{"/v1/destroy", "/v1/shared/destroy"} {
		for _, fields := range []string{`"force":null`, `"force":"private-sentinel"`, `"force":1`, `"force":true,"force":false`, `"force":{},"secret":"private-sentinel"`} {
			perform(path, `{"api_version":"v1",`+fields+`}`)
		}
		perform(path, `{"api_version":"v2","force":true}`)
		perform(path, `{"api_version":"v1","force":true} {}`)
		perform(path, `{"api_version":"v1","force":true,"instance_id":"private-sentinel"}`)
	}
}

func TestForceCollisionAndDurableAuthority(t *testing.T) {
	state, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.db.Close() }()
	id, peer, serviceID := newID(), newID(), newID()
	target := sharedAllocation{ServiceID: serviceID, Request: v1.ManagerRequest{Version: v1.ManagerProtocolVersion, Operation: "release", AllocationID: newID(), Ownership: newID(), Database: "bl" + newID()[:32], Username: "bl" + newID()[:32], Password: newID()}}
	if err = state.saveSharedAllocations(id, map[string]sharedAllocation{"db": target}); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"database", "user", "sibling"} {
		other := target
		other.Request.AllocationID = newID()
		switch field {
		case "database":
			other.Request.Username = "bl" + newID()[:32]
		case "user":
			other.Request.Database = "bl" + newID()[:32]
		}
		if field == "sibling" {
			err = state.saveSharedAllocations(id, map[string]sharedAllocation{"db": target, "sibling": other})
		} else {
			err = state.saveSharedAllocations(peer, map[string]sharedAllocation{"db": other})
		}
		if err != nil {
			t.Fatal(err)
		}
		code(t, state.checkSharedReleaseCollision(id, "db", target), "shared_in_use")
		if err = state.db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("shared_allocations")).Delete([]byte(peer)) }); err != nil {
			t.Fatal(err)
		}
	}
	target.Request.Force = true
	code(t, state.saveSharedAllocations(id, map[string]sharedAllocation{"db": target}), "state_corrupt")
	raw, err := json.Marshal(map[string]sharedAllocation{"db": target})
	if err != nil {
		t.Fatal(err)
	}
	_, err = decodeSharedAllocations(raw)
	code(t, err, "state_corrupt")
	target.Request.Force = false
	if err = state.saveSharedAllocations(id, map[string]sharedAllocation{"db": target}); err != nil {
		t.Fatal(err)
	}
	err = state.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte("shared_allocations")).Get([]byte(id))
		if strings.Contains(string(raw), `"force"`) {
			t.Fatal("transient force persisted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Cancellation remains a pre-effect fence on forced cleanup too.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = state.releaseSharedAllocationsWithForce(ctx, id, true); err != context.Canceled {
		t.Fatal("force bypassed lock cancellation", err)
	}
}

// forceEngine is a private bounded Engine protocol fixture, not a real provider.
func forceEngine(t *testing.T, handle http.HandlerFunc) *dockerEngine {
	t.Helper()
	listener, err := net.Listen("unix", filepath.Join(shortTemp(t), "engine.sock"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/_ping" {
			w.Header().Set("API-Version", "1.53")
			return
		}
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/v1.53")
		handle(w, r)
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close(); _ = listener.Close(); <-done })
	d, err := newDocker("unix://" + listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestForceStorageAbsenceObservation(t *testing.T) {
	for _, scenario := range []struct {
		name           string
		server, volume int
		absent, failed bool
	}{
		{"both-absent", 404, 404, true, false}, {"container-only", 404, 200, false, false}, {"volume-only", 200, 404, false, false}, {"both-exist", 200, 200, false, false}, {"server-unavailable", 503, 404, false, true}, {"volume-unavailable", 404, 503, false, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			count := atomic.Int32{}
			d := forceEngine(t, func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				status := scenario.server
				if r.URL.Path == "/volumes/volume" {
					status = scenario.volume
				} else if r.URL.Path != "/containers/server/json" {
					t.Error("unexpected provider target", r.URL.Path)
				}
				w.WriteHeader(status)
				if status == 200 {
					_, _ = io.WriteString(w, `{"Id":"server","Name":"volume"}`)
				} else {
					_, _ = io.WriteString(w, `{"message":"private-sentinel"}`)
				}
			})
			record := sharedServiceRecord{Server: ownedResource{Kind: "container", ID: "server", Removed: true}, Volume: ownedResource{Kind: "volume", ID: "volume", Removed: true}}
			absent, err := sharedStorageAbsent(context.Background(), d, record)
			if absent != scenario.absent || (err != nil) != scenario.failed || count.Load() != 2 {
				t.Fatal("incorrect absence/error proof", absent, err, count.Load())
			}
			if err != nil && strings.Contains(err.Error(), "private-sentinel") {
				t.Fatal("provider payload leaked")
			}
		})
	}
}

func TestForceSharedTopology(t *testing.T) {
	for _, drift := range []string{"labels-only", "image", "arguments", "admin", "restart", "mount", "network", "nonloopback", "invalid-port"} {
		t.Run(drift, func(t *testing.T) {
			state, err := openStore(shortTemp(t))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = state.db.Close() }()
			record := sharedServiceRecord{ID: newID(), Server: ownedResource{Kind: "container", ID: newID(), Token: newID()}, Network: ownedResource{Kind: "network", ID: newID(), Name: "recorded-network", Token: newID()}, Volume: ownedResource{Kind: "volume", ID: "recorded-volume", Name: "recorded-volume", Token: newID()}, ImageID: "sha256:recorded", EffectiveArgs: []string{"original"}, AdminPassword: newID(), Declaration: v1.SharedService{ReadinessTimeout: "10ms"}, Port: 1}
			config := map[string]any{"Cmd": record.EffectiveArgs, "Env": []string{"MARIADB_ROOT_PASSWORD=" + record.AdminPassword}, "Labels": map[string]string{"io.backlot.owner": newID()}}
			mount := map[string]any{"Destination": "/var/lib/mysql", "Name": record.Volume.Name, "RW": true}
			networks := map[string]any{record.Network.Name: map[string]string{"NetworkID": record.Network.ID}}
			currentPort := 2
			if drift == "labels-only" {
				listener, listenErr := net.Listen("tcp", "127.0.0.1:0")
				if listenErr != nil {
					t.Fatal(listenErr)
				}
				currentPort = listener.Addr().(*net.TCPAddr).Port
				done := make(chan struct{})
				go func() {
					defer close(done)
					for {
						conn, acceptErr := listener.Accept()
						if acceptErr != nil {
							return
						}
						_ = conn.Close()
					}
				}()
				t.Cleanup(func() { _ = listener.Close(); <-done })
			}
			binding := map[string]string{"HostIp": "127.0.0.1", "HostPort": strconv.Itoa(currentPort)}
			payload := map[string]any{"Id": record.Server.ID, "Image": record.ImageID, "Config": config, "HostConfig": map[string]any{"RestartPolicy": map[string]string{"Name": "no"}}, "Mounts": []any{mount}, "NetworkSettings": map[string]any{"Networks": networks, "Ports": map[string]any{"3306/tcp": []any{binding}}}}
			switch drift {
			case "image":
				payload["Image"] = "sha256:other"
			case "arguments":
				config["Cmd"] = []string{"other"}
			case "admin":
				config["Env"] = []string{"MARIADB_ROOT_PASSWORD=private-sentinel"}
			case "restart":
				payload["HostConfig"] = map[string]any{"RestartPolicy": map[string]string{"Name": "always"}}
			case "mount":
				mount["Name"] = "foreign-volume"
			case "network":
				networks[record.Network.Name] = map[string]string{"NetworkID": "other"}
			case "nonloopback":
				binding["HostIp"] = "0.0.0.0"
			case "invalid-port":
				binding["HostPort"] = "private-sentinel"
			}
			d := forceEngine(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/containers/"+record.Server.ID+"/json" {
					if err := json.NewEncoder(w).Encode(payload); err != nil {
						t.Error(err)
					}
					return
				}
				if r.Method == http.MethodGet && (strings.HasPrefix(r.URL.Path, "/volumes/") || strings.HasPrefix(r.URL.Path, "/networks/")) {
					_, _ = io.WriteString(w, `{}`)
					return
				}
				if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/start") {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				t.Error("unexpected force provider mutation", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusBadRequest)
			})
			// Direct verifier proves label waiver independent of later readiness.
			err = state.verifySharedServerMode(context.Background(), d, record, true)
			structural := drift != "labels-only" && drift != "nonloopback" && drift != "invalid-port"
			if (err != nil) != structural {
				t.Fatal("force topology guard incorrect", err)
			}
			if err = state.verifySharedServer(context.Background(), d, record); err == nil {
				t.Fatal("default accepted label mismatch")
			}
			record.Endpoint = d.DaemonHost()
			err = state.startSharedMode(context.Background(), &record, true)
			if err == nil {
				t.Fatal("fixture has no SQL server; readiness must fail")
			}
			if drift == "labels-only" && record.Port != currentPort {
				t.Fatal("force used stale saved SQL port")
			}
			if drift == "nonloopback" && record.Port != 1 {
				t.Fatal("nonloopback binding reached SQL")
			}
			if strings.Contains(err.Error(), "private-sentinel") {
				t.Fatal("configuration diagnostic leaked secret")
			}
		})
	}
}

func TestForceSharedRemovalBoundaries(t *testing.T) {
	for _, scenario := range []string{"container-exact", "container-unacknowledged", "network-consumer", "volume-consumer"} {
		t.Run(scenario, func(t *testing.T) {
			kind, _, _ := strings.Cut(scenario, "-")
			resource := ownedResource{Kind: kind, ID: newID(), Name: "recorded-target", Token: newID()}
			if scenario == "container-unacknowledged" {
				resource.ID = ""
			}
			deletes := atomic.Int32{}
			d := forceEngine(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					deletes.Add(1)
					if !strings.Contains(r.URL.Path, resource.ID) || resource.ID == "" || r.URL.Query().Get("force") == "true" || r.URL.Query().Get("v") == "true" {
						t.Error("forced provider removal broadened target or removed attached volumes")
					}
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if r.Method != http.MethodGet {
					t.Error("unexpected consumer actuation", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if r.URL.Path == "/containers/json" {
					_, _ = io.WriteString(w, `[{"Id":"foreign"}]`)
					return
				}
				if r.URL.Path == "/containers/foreign/json" {
					_ = json.NewEncoder(w).Encode(map[string]any{"Id": "foreign", "Mounts": []any{map[string]string{"Name": resource.Name}}, "NetworkSettings": map[string]any{"Networks": map[string]any{"foreign": map[string]string{"NetworkID": resource.ID}}}})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"Id": resource.ID, "Name": resource.Name, "Labels": map[string]string{"io.backlot.owner": "foreign"}, "Config": map[string]any{"Labels": map[string]string{"io.backlot.owner": "foreign"}}, "State": map[string]bool{"Running": false}})
			})
			err := removeForcedSharedResource(context.Background(), d, resource)
			if scenario == "container-exact" {
				if err != nil || deletes.Load() != 1 {
					t.Fatal("explicit exact ID deletion rejected labels", err)
				}
			} else if err == nil || deletes.Load() != 0 {
				t.Fatal("force adopted unknown target or bypassed consumer fence", err, deletes.Load())
			}
		})
	}
}

// A create/ack gap has no stored container ID, but the original capability can
// authenticate the effect. Pin that ID durably before any stop/delete; a foreign
// same-name replacement (or malformed identity) must never gain this authority.
func TestForceSharedDestroyRecoversPendingContainer(t *testing.T) {
	for _, scenario := range []string{"owned", "foreign", "invalid-id"} {
		t.Run(scenario, func(t *testing.T) {
			state, err := openStore(shortTemp(t))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = state.db.Close() }()
			record := sharedLockRecord(newID())
			record.Server = ownedResource{Kind: "container", Name: "pending-owned-server", Token: newID()}
			record.Volume = ownedResource{Kind: "volume", Name: "pending-owned-volume", ID: "pending-owned-volume", Token: newID()}
			record.Network.Removed = true
			actualID := newID()
			present, running := atomic.Bool{}, atomic.Bool{}
			present.Store(true)
			running.Store(true)
			stops, deletes := atomic.Int32{}, atomic.Int32{}
			pinned, ackPending, ackSaved := false, false, false
			state.sharedCheckpoint = func(point string) error {
				saved, loadErr := state.sharedService(record.ID)
				if loadErr != nil {
					return loadErr
				}
				if point == "stop-server" {
					pinned = saved.Server.ID == actualID && saved.Destroying
					if !pinned {
						t.Error("pending container identity not saved before stop")
					}
				}
				if point == "remove-effect:container" {
					ackPending = saved.Server.ID == actualID && !saved.Server.Removed
					if !ackPending {
						t.Error("effect was acknowledged before independent absence observation")
					}
				}
				if point == "remove-effect:volume" {
					ackSaved = saved.Server.ID == actualID && saved.Server.Removed
					if !ackSaved {
						t.Error("container removal acknowledgment was not saved after verified absence")
					}
				}
				return nil
			}
			d := forceEngine(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					if r.URL.Path == "/volumes/"+record.Volume.ID {
						w.WriteHeader(http.StatusNotFound)
						_, _ = io.WriteString(w, `{"message":"absent"}`)
						return
					}
					if r.URL.Path != "/containers/"+record.Server.Name+"/json" && r.URL.Path != "/containers/"+actualID+"/json" {
						t.Error("unexpected inspection target", r.URL.Path)
					}
					if !present.Load() {
						w.WriteHeader(http.StatusNotFound)
						_, _ = io.WriteString(w, `{"message":"absent"}`)
						return
					}
					token, id := record.Server.Token, actualID
					if scenario == "foreign" {
						token = "foreign-capability"
					}
					if scenario == "invalid-id" {
						id = "short"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"Id": id, "Config": map[string]any{"Labels": map[string]string{"io.backlot.owner": token}}, "State": map[string]bool{"Running": running.Load()}})
					return
				}
				saved, loadErr := state.sharedService(record.ID)
				if loadErr != nil || saved.Server.ID != actualID || !saved.Destroying {
					t.Error("mutation before durable exact-ID pin", loadErr)
				}
				if scenario != "owned" {
					t.Error("foreign/invalid pending identity was actuated")
				}
				if r.Method == http.MethodPost && r.URL.Path == "/containers/"+actualID+"/stop" {
					stops.Add(1)
					running.Store(false)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if r.Method == http.MethodDelete && r.URL.Path == "/containers/"+actualID {
					if running.Load() || r.URL.Query().Get("force") == "true" || r.URL.Query().Get("v") == "true" {
						t.Error("pending container removed live or with broadened authority")
					}
					deletes.Add(1)
					present.Store(false)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				t.Error("actuation used mutable name or unexpected target", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusBadRequest)
			})
			record.Endpoint = d.DaemonHost()
			record.Configuration = sharedDigest(record.Declaration, record.Endpoint)
			if err = state.saveShared(record); err != nil {
				t.Fatal(err)
			}
			svc := &service{store: state}
			result, err := svc.sharedDestroy(context.Background(), v1.SharedDestroyRequest{APIVersion: v1.Version, ServiceID: record.ID, Force: true})
			if scenario == "owned" {
				if err != nil || stops.Load() != 1 || deletes.Load() != 1 || present.Load() || !pinned || !ackPending || !ackSaved {
					t.Fatal("pending exact effect was not fully reconciled", err, stops.Load(), deletes.Load(), pinned, ackPending, ackSaved)
				}
				if len(result.Services) != 1 || result.Services[0].Status != "destroyed" {
					t.Fatal("pending cleanup did not settle service result")
				}
				ids, listErr := state.sharedIDs()
				if listErr != nil || len(ids) != 0 {
					t.Fatal("verified deletion retained service obligation", listErr)
				}
			} else {
				if err == nil || stops.Load() != 0 || deletes.Load() != 0 || !present.Load() {
					t.Fatal("unsafe pending target cleanup", err)
				}
				saved, loadErr := state.sharedService(record.ID)
				if loadErr != nil || saved.Server.ID != "" || saved.Server.Removed || !saved.Destroying {
					t.Fatal("uncertain pending target lost obligation or adopted identity", loadErr)
				}
			}
		})
	}
}
