//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	moby "github.com/moby/moby/client"
	"github.com/octacian/backlot/internal/gateway"
	bolt "go.etcd.io/bbolt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

func diagnosticCopyFixture(t *testing.T) (*store, string, diagnosticProbeRecord, []byte) {
	t.Helper()
	state, snap, _ := caReferenceState(t)
	data, ca, err := state.planDiagnosticCACopy(snap)
	if err != nil {
		t.Fatal(err)
	}
	key := newID()
	r := diagnosticProbeRecord{Resource: ownedResource{Kind: "container", Name: "backlot-doctor-" + key, Token: newID()}, CA: ca}
	if err := state.saveDiagnosticProbe(key, r); err != nil {
		t.Fatal(err)
	}
	return state, key, r, data
}

func TestDoctorPublicCACopySource(t *testing.T) {
	for _, change := range []string{"valid", "private PEM", "oversize", "symlink", "no pin", "changed pin", "foreign config", "unsafe mode"} {
		t.Run(change, func(t *testing.T) {
			state, snap, path := caReferenceState(t)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "private PEM":
				if err := os.WriteFile(path, append(before, []byte("\n-----BEGIN PRIVATE KEY-----\nAA==\n-----END PRIVATE KEY-----\n")...), 0600); err != nil {
					t.Fatal(err)
				}
			case "oversize":
				if err := os.WriteFile(path, []byte(strings.Repeat("x", 2<<20)), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".original", path); err != nil {
					t.Fatal(err)
				}
			case "unsafe mode":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "no pin", "changed pin":
				r, err := state.managed()
				if err != nil {
					t.Fatal(err)
				}
				r.RootHash = ""
				if change == "changed pin" {
					r.RootHash = strings.Repeat("a", 64)
				}
				if err := state.saveManaged(r); err != nil {
					t.Fatal(err)
				}
			case "foreign config":
				snap.Caddy.Endpoint = "http://127.0.0.1:9999"
			}
			data, c, err := state.planDiagnosticCACopy(snap)
			if change != "valid" {
				if err == nil {
					t.Fatal("unsafe source accepted")
				}
				return
			}
			if err != nil || c == nil || len(data) == 0 {
				t.Fatal(err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatal("source changed", err)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("source mode changed", err)
			}
		})
	}
}

func TestDoctorPublicCACopyJournal(t *testing.T) {
	state, key, r, data := diagnosticCopyFixture(t)
	if err := state.allocateDiagnosticCACopy(key, &r, data); err != nil {
		t.Fatal(err)
	}
	if !r.CA.Ready || r.CA.Identity == "" || !validDiagnosticCACopy(r) {
		t.Fatal("copy not durably ready", r.CA)
	}
	if err := state.verifyDiagnosticCACopy(key, r.CA); err != nil {
		t.Fatal(err)
	}
	// Reopen persisted state rather than trusting the caller's in-memory effect.
	records := readDoctorCopyRecords(t, state)
	if records[key].CA == nil || *records[key].CA != *r.CA {
		t.Fatal("ready metadata not persisted")
	}
	for _, mutate := range []func(*diagnosticProbeRecord){
		func(r *diagnosticProbeRecord) { r.CA.Parent = "../../other" },
		func(r *diagnosticProbeRecord) { r.CA.SHA256 = "not-a-hash" },
		func(r *diagnosticProbeRecord) { r.CA.Identity = "other" },
		func(r *diagnosticProbeRecord) { r.CA.Identity = "" },
		func(r *diagnosticProbeRecord) { r.CA.Ready = false; r.Resource.ID = strings.Repeat("a", 64) },
	} {
		bad := r
		copy := *r.CA
		bad.CA = &copy
		mutate(&bad)
		if validDiagnosticCACopy(bad) {
			t.Fatal("invalid metadata accepted", bad.CA)
		}
	}
	if !validDiagnosticCACopy(diagnosticProbeRecord{}) {
		t.Fatal("probe without CA copy metadata rejected")
	}
}

func TestDoctorPublicCACopyPreservationAndRecovery(t *testing.T) {
	for _, fault := range []string{"ready", "partial", "unidentified", "wrong inode", "wrong mode", "wrong bytes", "symlink", "hardlink", "parent replaced", "consumer uncertain"} {
		t.Run(fault, func(t *testing.T) {
			state, key, r, data := diagnosticCopyFixture(t)
			path := diagnosticCACopyPath(state.directory, key)
			if fault == "partial" || fault == "unidentified" {
				if err := os.WriteFile(path, []byte("partial"), 0600); err != nil {
					t.Fatal(err)
				}
				if fault == "partial" {
					info, err := os.Stat(path)
					if err != nil {
						t.Fatal(err)
					}
					r.CA.Identity, err = fileIdentity(info)
					if err != nil {
						t.Fatal(err)
					}
				}
			} else {
				if err := state.allocateDiagnosticCACopy(key, &r, data); err != nil {
					t.Fatal(err)
				}
			}
			switch fault {
			case "wrong inode":
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0444); err != nil {
					t.Fatal(err)
				}
			case "wrong mode":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "wrong bytes":
				if err := os.Chmod(path, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0444); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".original", path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, path+".link"); err != nil {
					t.Fatal(err)
				}
			case "parent replaced":
				r.CA.Parent = "0:0"
			}
			// This Engine fixture never grants mutation authority: it either proves
			// absence or models unavailable consumer inspection. It is joined on cleanup.
			socketRoot, err := os.MkdirTemp("/tmp", "bl-id-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.RemoveAll(socketRoot); err != nil {
					t.Error(err)
				}
			})
			socket := filepath.Join(socketRoot, "engine.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			uncertain := fault == "consumer uncertain"
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/_ping" {
					w.Header().Set("API-Version", "1.53")
					return
				}
				if req.Method != http.MethodGet {
					t.Error("absence/uncertainty must not authorize mutation", req.Method)
				}
				if uncertain {
					w.WriteHeader(http.StatusServiceUnavailable)
				} else {
					http.NotFound(w, req)
				}
			})}
			done := make(chan struct{})
			go func() { defer close(done); _ = server.Serve(listener) }()
			t.Cleanup(func() { _ = server.Close(); <-done })
			r.Endpoint = "unix://" + socket
			if err := state.saveDiagnosticProbe(key, r); err != nil {
				t.Fatal(err)
			}
			err = state.recoverDiagnosticProbes()
			clean := fault == "ready" || fault == "partial"
			if clean {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("copy not removed", err)
				}
				return
			}
			if err == nil {
				t.Fatal("uncertain copy/consumer accepted")
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatal("uncertain copy not preserved", err)
			}
			svc := service{store: state, directory: state.directory, diagnosticRecoveryErr: err}
			response, err := svc.doctor(context.Background(), v1.DoctorRequest{APIVersion: v1.Version})
			if err != nil || response.Healthy {
				t.Fatal("preserved journal must make doctor unhealthy", err)
			}
			assertDoctorCheck(t, response, "diagnostic_cleanup", v1.DoctorFailed)
			cause := "copy mismatch"
			switch fault {
			case "unidentified":
				cause = "unidentified allocation"
			case "parent replaced":
				cause = "parent mismatch"
			case "consumer uncertain":
				cause = "provider uncertainty"
			}
			if !strings.Contains(response.Checks[0].Message, diagnosticCACopyName(key)+": "+cause) {
				t.Fatal("no useful artifact recovery diagnostic", response.Checks)
			}
			if fault == "consumer uncertain" {
				// Restore provider access without changing file authority. The handler is
				// stopped/joined before changing behavior to avoid an unsynchronized flag.
				_ = server.Close()
				<-done
				return // Provider-uncertainty preservation is proven; existing Engine tests cover recovery.
			}
			if fault == "parent replaced" {
				r.CA.Parent = readDoctorCopyRecords(t, state)[key].CA.Parent
				return
			}
			// Model independent operator quarantine while the daemon is stopped. Product
			// code neither adopts nor removes the mismatched artifact. Once absent,
			// recovery still authenticates Engine absence before forgetting the journal.
			if err := os.Rename(path, path+".quarantined"); err != nil {
				t.Fatal(err)
			}
			if err := state.recoverDiagnosticProbes(); err != nil {
				t.Fatal(err)
			}
			pending, err := state.diagnosticPending(nil)
			if err != nil || pending {
				t.Fatal("externally absent copy did not reconcile", err)
			}
			svc.diagnosticRecoveryErr = nil // A new daemon receives the successful recovery result.
			response, err = svc.doctor(context.Background(), v1.DoctorRequest{APIVersion: v1.Version})
			if err != nil || !response.Healthy {
				t.Fatal("reconciled copy remains unhealthy", response, err)
			}
		})
	}
}

func readDoctorCopyRecords(t *testing.T, state *store) map[string]diagnosticProbeRecord {
	t.Helper()
	records := map[string]diagnosticProbeRecord{}
	if err := state.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("meta")).ForEach(func(k, data []byte) error {
			if strings.HasPrefix(string(k), "doctor_probe/") {
				var r diagnosticProbeRecord
				if err := json.Unmarshal(data, &r); err != nil {
					return err
				}
				records[strings.TrimPrefix(string(k), "doctor_probe/")] = r
			}
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	return records
}

// Install a separate pinned managed fixture into this owned diagnostic store.
// It is never started: the fault suite needs valid public/private CA inputs,
// while its deliberately unavailable canonical origin exercises probe failures.
func doctorCopyManagedFixture(t *testing.T, state *store) *v1.CaddyConfig {
	t.Helper()
	source, _, _ := caReferenceState(t)
	record, err := source.managed()
	if err != nil {
		t.Fatal(err)
	}
	_, _, sourceStorage := managedPaths(source.directory)
	_, saved, storage := managedPaths(state.directory)
	caDir := filepath.Join(storage, "pki", "authorities", "local")
	for _, dir := range []string{filepath.Dir(saved), caDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"root.crt", "root.key", "intermediate.crt", "intermediate.key"} {
		data, err := os.ReadFile(filepath.Join(sourceStorage, "pki", "authorities", "local", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(caDir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	config, err := gateway.ManagedConfig(managedCaddy(record), storage)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(saved, config, 0600); err != nil {
		t.Fatal(err)
	}
	if err := state.saveManaged(record); err != nil {
		t.Fatal(err)
	}
	return &record.Config
}

func assertDoctorCopiesAbsent(t *testing.T, directory string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(directory, "doctor-public-ca-*.crt"))
	if err != nil || len(matches) != 0 {
		t.Fatal("diagnostic public copies remain", matches, err)
	}
}

func assertDoctorCopyMount(t *testing.T, state *store, d *dockerEngine) {
	t.Helper()
	records := readDoctorCopyRecords(t, state)
	if len(records) != 1 {
		t.Fatalf("expected one owned copy journal, got %d", len(records))
	}
	for key, r := range records {
		if r.CA == nil || !r.CA.Ready {
			t.Fatal("probe started before durable ready copy")
		}
		if err := state.verifyDiagnosticCACopy(key, r.CA); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		observed, err := d.ContainerInspect(ctx, r.Resource.ID, moby.ContainerInspectOptions{})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		c := observed.Container
		if c.Config.User != "0:0" || !c.HostConfig.ReadonlyRootfs || !reflect.DeepEqual(c.HostConfig.CapDrop, []string{"ALL"}) || !slices.Contains(c.HostConfig.SecurityOpt, "no-new-privileges") {
			t.Fatal("diagnostic hardening changed", c.HostConfig)
		}
		if len(c.Mounts) != 1 || c.Mounts[0].Source != diagnosticCACopyPath(state.directory, key) || c.Mounts[0].Destination != "/backlot-public-ca.crt" || c.Mounts[0].RW {
			t.Fatal("copy must be the only read-only file mount", c.Mounts)
		}
		if !strings.Contains(c.Config.Entrypoint[2], "timeout 5 openssl s_client") {
			t.Fatal("TLS deadline changed")
		}
		t.Log("verified owned ready certificate-only bind and unchanged secured probe config")
	}
}

func TestDoctorPublicCACopyCorruptJournalPreserved(t *testing.T) {
	state, key, r, _ := diagnosticCopyFixture(t)
	r.CA.Identity = "../../foreign"
	if err := state.saveDiagnosticProbe(key, r); err != nil {
		t.Fatal(err)
	}
	var before []byte
	if err := state.db.View(func(tx *bolt.Tx) error {
		before = bytes.Clone(tx.Bucket([]byte("meta")).Get([]byte("doctor_probe/" + key)))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	err := state.recoverDiagnosticProbes()
	var detail *v1.PlanError
	if !errors.As(err, &detail) || detail.Code != "state_corrupt" {
		t.Fatal("malformed metadata did not fail closed", err)
	}
	if err := state.db.View(func(tx *bolt.Tx) error {
		if !bytes.Equal(before, tx.Bucket([]byte("meta")).Get([]byte("doctor_probe/"+key))) {
			t.Fatal("corrupt journal changed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

type doctorCopyStatInfo struct {
	os.FileInfo
	stat syscall.Stat_t
}

func (info doctorCopyStatInfo) Sys() any { return &info.stat }

func TestDoctorPublicCACopyRejectsForeignOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "copy.crt")
	if err := os.WriteFile(path, []byte("public"), 0444); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !diagnosticCAOwned(info) {
		t.Fatal("owned regular public file rejected")
	}
	foreign := doctorCopyStatInfo{FileInfo: info, stat: *info.Sys().(*syscall.Stat_t)}
	foreign.stat.Uid++
	if diagnosticCAOwned(foreign) {
		t.Fatal("foreign UID accepted as public copy cleanup authority")
	}
}
