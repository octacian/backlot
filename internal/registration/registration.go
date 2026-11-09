// Package registration publishes local logical markers, never resource authority.
package registration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
)

// Location names resolved source and administrative directories.
type Location struct {
	// Checkout is the canonical checkout root.
	Checkout string
	// Admin is its canonical administrative marker directory.
	Admin string
	// Common is the canonical common marker directory.
	Common string
	// Git reports whether locations are Git administrative directories.
	Git bool
}

// Identity contains distinct common and checkout registration roles.
type Identity struct {
	// CommonID identifies the complete common role, or is empty when missing.
	CommonID string `json:"common_id"`
	// CheckoutID identifies the complete checkout role, or is empty when missing.
	CheckoutID string `json:"checkout_id"`
}

// Extends reports benign first publication without changing a previously observed role.
func (i Identity) Extends(before Identity) bool {
	return (before.CommonID == "" || before.CommonID == i.CommonID) && (before.CheckoutID == "" || before.CheckoutID == i.CheckoutID)
}

type marker struct {
	Version int    `json:"version"`
	Role    string `json:"role"`
	ID      string `json:"id"`
}

// ValidID reports whether an ID is the complete canonical random token format.
func ValidID(id string) bool {
	b, err := hex.DecodeString(id)
	return err == nil && len(b) == 32 && strings.ToLower(id) == id
}

func markerPath(l Location, role string) string {
	dir := l.Common
	if role == "checkout" {
		dir = l.Admin
	}
	return filepath.Join(dir, "backlot-"+role+".json")
}

func read(path, role string) (string, error) {
	before, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() {
		return "", fmt.Errorf("registration marker must be a regular file: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	after, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(before, after) {
		return "", fmt.Errorf("registration marker changed while opening: %s", path)
	}
	b, err := io.ReadAll(io.LimitReader(f, 513))
	if err != nil {
		return "", err
	}
	var m marker
	if len(b) > 512 || manifest.Decode(b, ".json", &m) != nil || m.Version != 1 || m.Role != role || !ValidID(m.ID) {
		return "", fmt.Errorf("invalid %s registration marker; inspect and explicitly repair %s", role, path)
	}
	return m.ID, nil
}

// Read inspects markers without creating files; missing roles have empty IDs.
func Read(l Location) (i Identity, err error) {
	i.CommonID, err = read(markerPath(l, "common"), "common")
	if err == nil {
		i.CheckoutID, err = read(markerPath(l, "checkout"), "checkout")
	}
	if err == nil && i.CommonID != "" && i.CommonID == i.CheckoutID {
		err = fmt.Errorf("registration roles must have distinct IDs")
	}
	return i, err
}

// Lock serializes cooperating publishers and rotations, common before admin.
// Hold it before any selected-store lock; readers never acquire it.
func Lock(ctx context.Context, l Location) (func(), error) {
	return lock(ctx, l, true)
}

// LockRead fences admission against rotation without creating or writing source files.
func LockRead(ctx context.Context, l Location) (func(), error) { return lock(ctx, l, false) }

func lock(ctx context.Context, l Location, create bool) (func(), error) {
	var locks []*flock.Flock
	unlock := func() {
		for n := len(locks) - 1; n >= 0; n-- {
			_ = locks[n].Unlock()
		}
	}
	for n, dir := range []string{l.Common, l.Admin} {
		if n == 1 && l.Admin == l.Common {
			continue
		}
		if !l.Git && create {
			if err := os.Mkdir(dir, 0700); err == nil {
				if err := syncDir(filepath.Dir(dir)); err != nil {
					unlock()
					return nil, fmt.Errorf("registration directory completion uncertain; inspect before retrying: %w", err)
				}
			} else if !errors.Is(err, os.ErrExist) {
				unlock()
				return nil, err
			}
		}
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() {
			unlock()
			return nil, fmt.Errorf("registration directory unavailable: %s", dir)
		}
		path := filepath.Join(dir, "backlot-registration.lock")
		if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() || err != nil && !errors.Is(err, os.ErrNotExist) {
			unlock()
			return nil, fmt.Errorf("unsafe registration lock: %s", path)
		}
		lock := flock.New(path, flock.SetPermissions(0600))
		if !create {
			lock = flock.New(path, flock.SetFlag(os.O_RDONLY))
		}
		var ok bool
		if create {
			ok, err = lock.TryLockContext(ctx, 25*time.Millisecond)
		} else {
			ok, err = lock.TryRLockContext(ctx, 25*time.Millisecond)
		}
		if err != nil || !ok {
			unlock()
			return nil, fmt.Errorf("cannot lock registration: %w", errors.Join(err, ctx.Err()))
		}
		locks = append(locks, lock)
		if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
			unlock()
			return nil, fmt.Errorf("unsafe registration lock: %s", path)
		}
	}
	return unlock, nil
}

// Observe reads logical roles and their resolved locations without enrollment.
func Observe(l Location) (v1.RegistrationIdentity, error) {
	i, err := Read(l)
	if err != nil || i == (Identity{}) {
		return v1.RegistrationIdentity{}, err
	}
	return v1.RegistrationIdentity{CommonID: i.CommonID, CheckoutID: i.CheckoutID, CheckoutPath: l.Checkout, AdminPath: l.Admin, CommonPath: l.Common, Git: l.Git}, nil
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

func stage(path, role string) (temp, id string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return
	}
	id = hex.EncodeToString(b)
	f, err := os.CreateTemp(filepath.Dir(path), ".backlot-marker-*")
	if err != nil {
		return "", "", err
	}
	temp = f.Name()
	data, err := json.Marshal(marker{Version: 1, Role: role, ID: id})
	if err == nil {
		_, err = f.Write(data)
	}
	err = errors.Join(err, f.Sync(), f.Close())
	if err != nil {
		_ = os.Remove(temp)
		return "", "", err
	}
	return temp, id, nil
}

func publish(path, role string) (string, error) {
	if id, err := read(path, role); err != nil || id != "" {
		return id, err
	}
	temp, _, err := stage(path, role)
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(temp) }()
	if err := os.Link(temp, path); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return "", fmt.Errorf("marker publication completion uncertain; inspect before retrying: %w", err)
	}
	return read(path, role)
}

// Enroll completes absent roles without overwriting either marker. Caller holds Lock.
func Enroll(l Location) (i Identity, err error) {
	i.CommonID, err = publish(markerPath(l, "common"), "common")
	if err == nil {
		i.CheckoutID, err = publish(markerPath(l, "checkout"), "checkout")
	}
	if err == nil {
		i, err = Read(l)
	}
	return i, err
}

// Rotate replaces one reviewed role. Caller holds Lock until store publication.
// A successful marker rotation and selected-store acceptance are separate stages.
func Rotate(l Location, role, expected string) (string, error) {
	if (role != "common" && role != "checkout") || !ValidID(expected) {
		return "", fmt.Errorf("invalid registration rotation request")
	}
	path := markerPath(l, role)
	temp, id, err := stage(path, role)
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(temp) }()
	old, err := read(path, role)
	if err != nil {
		return "", err
	}
	if old != expected {
		return "", fmt.Errorf("registration changed; obtain a fresh preview")
	}
	if err := os.Rename(temp, path); err != nil {
		return "", err
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return "", fmt.Errorf("marker rotation completion uncertain; inspect before retrying: %w", err)
	}
	return id, nil
}
