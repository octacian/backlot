package registration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPublisherProcess(t *testing.T) {
	root := os.Getenv("BACKLOT_MARKER_FIXTURE_ROOT")
	if root == "" {
		return
	}
	l := Location{Checkout: root, Admin: filepath.Join(root, ".backlot-local"), Common: filepath.Join(root, ".backlot-local")}
	unlock, err := Lock(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := Enroll(l); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrationPublicationAndRotation(t *testing.T) {
	root := t.TempDir()
	l, err := Locate(root, root)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Read(l); err != nil || got != (Identity{}) {
		t.Fatal(got, err)
	}
	if _, err := os.Stat(l.Common); !os.IsNotExist(err) {
		t.Fatal("read created metadata", err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			cmd := exec.Command(self, "-test.run=^TestPublisherProcess$")
			cmd.Env = append(os.Environ(), "BACKLOT_MARKER_FIXTURE_ROOT="+root)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("writer: %v %s", err, out)
			}
		})
	}
	wg.Wait()
	before, err := Read(l)
	if err != nil || !ValidID(before.CommonID) || !ValidID(before.CheckoutID) || before.CommonID == before.CheckoutID {
		t.Fatal(before, err)
	}
	unlock, err := Lock(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	again, err := Enroll(l)
	if err != nil || again != before {
		t.Fatal("publication overwrote winner", again, err)
	}
	if err := os.WriteFile(filepath.Join(l.Common, ".backlot-marker-partial"), []byte(`{"version":`), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := Read(l); err != nil || got != before {
		t.Fatal("unused sibling influenced reader", got, err)
	}
	newID, err := Rotate(l, "checkout", before.CheckoutID)
	if err != nil || newID == before.CheckoutID {
		t.Fatal(newID, err)
	}
	if _, err := Rotate(l, "checkout", before.CheckoutID); err == nil {
		t.Fatal("stale expected-old rotated marker")
	}
	got, err := Read(l)
	if err != nil || got.CommonID != before.CommonID || got.CheckoutID != newID {
		t.Fatal(got, err)
	}
	if err := os.Chmod(l.Common, 0500); err != nil {
		t.Fatal(err)
	}
	unlock()
	unlockRead, err := LockRead(context.Background(), l)
	if err != nil {
		t.Fatal("read-only admission lock required source writes", err)
	}
	unlockRead()
	t.Cleanup(func() { _ = os.Chmod(l.Common, 0700) })
	if got, err := Read(l); err != nil || got.CheckoutID != newID {
		t.Fatal("enrolled read required writable directory", got, err)
	}
}

func TestRegistrationStrictMarker(t *testing.T) {
	for _, body := range []string{`{}`, `{"version":1,"role":"common","id":"bad"}`, `{"version":2,"role":"common","id":"ID"}`, `{"version":1,"role":"checkout","id":"ID"}`, `{"version":1,"role":"common","id":"ID","id":"ID"}`, `{"version":1,"role":"common","id":"ID","extra":true}`, `{"version":1,"role":"common","id":"ID"} {}`, strings.Repeat(" ", 513)} {
		t.Run(body[:min(len(body), 40)], func(t *testing.T) {
			root := t.TempDir()
			l := Location{Common: root, Admin: root}
			path := markerPath(l, "common")
			if err := os.WriteFile(path, []byte(strings.ReplaceAll(body, "ID", strings.Repeat("a", 64))), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Read(l); err == nil {
				t.Fatal("invalid marker accepted")
			}
			before, _ := os.ReadFile(path)
			if _, err := Enroll(l); err == nil {
				t.Fatal("invalid marker replaced")
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("publication changed invalid marker")
			}
		})
	}
	root := t.TempDir()
	l := Location{Common: root, Admin: root}
	target := filepath.Join(root, "target")
	b, _ := json.Marshal(marker{1, "common", strings.Repeat("a", 64)})
	if err := os.WriteFile(target, b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, markerPath(l, "common")); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(l); err == nil {
		t.Fatal("symlink marker accepted")
	}
}

func TestRegistrationDeniedEnrollment(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission refusal requires an unprivileged test owner")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(root, 0700) }()
	l, err := Locate(root, root)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := Lock(context.Background(), l)
	if err == nil {
		unlock()
		t.Fatal("enrollment accepted unwritable source")
	}
	if _, err := os.Stat(l.Common); !os.IsNotExist(err) {
		t.Fatal("denied enrollment created metadata", err)
	}
}
