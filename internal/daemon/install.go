//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gofrs/flock"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/localipc"
	"github.com/octacian/backlot/internal/manifest"
)

// InstallOptions selects one executable and state root; Consent must use a terminal.
type InstallOptions struct {
	Directory, Stage, Destination, Version string
	Consent                                func([]v1.Instance) error
}

var installVersion = regexp.MustCompile(`^v0\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// Install replaces a verified staged executable while holding selected-state ownership.
// It never opens the database; only the recognized old CLI may request cleanup.
func Install(ctx context.Context, o InstallOptions) (result error) {
	if !installVersion.MatchString(o.Version) {
		return fmt.Errorf("invalid stable release")
	}
	if !filepath.IsAbs(o.Destination) || !filepath.IsAbs(o.Stage) || o.Stage == o.Destination {
		return fmt.Errorf("distinct absolute stage and destination required")
	}
	parent := filepath.Dir(o.Destination)
	if filepath.Dir(filepath.Dir(o.Stage)) != parent {
		return fmt.Errorf("stage must be inside a private destination-local directory")
	}
	if err := installDirectory(parent); err != nil {
		return err
	}
	if err := localipc.Directory(filepath.Dir(o.Stage), false); err != nil {
		return err
	}
	staged, err := installFile(o.Stage)
	if err != nil {
		return err
	}
	stageHash, err := installHash(o.Stage)
	if err != nil {
		return err
	}
	var target v1.VersionResponse
	if err := installCLI(ctx, o.Stage, &target, "version"); err != nil {
		return err
	}
	if target.Version != o.Version || target.APIVersion != v1.Version || target.StateVersion <= 0 {
		return fmt.Errorf("staged executable identity mismatch")
	}
	old, err := os.Lstat(o.Destination)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var oldHash [32]byte
	var identity v1.VersionResponse
	if old != nil {
		if _, err = installFile(o.Destination); err != nil {
			return err
		}
		oldHash, err = installHash(o.Destination)
		if err != nil {
			return err
		}
		if err = installCLI(ctx, o.Destination, &identity, "version"); err != nil {
			return err
		}
		if identity.APIVersion != v1.Version || identity.StateVersion <= 0 || !installVersion.MatchString(identity.Version) {
			return fmt.Errorf("unrecognized installed release; use manual installation")
		}
		if installOlder(o.Version, identity.Version) {
			return fmt.Errorf("refusing downgrade from %s", identity.Version)
		}
		if identity.Version == o.Version {
			return nil
		}
	}
	if err := localipc.Directory(o.Directory, true); err != nil {
		return err
	}
	lockPath := filepath.Join(o.Directory, "daemon.lock")
	lockInfo, err := os.Lstat(lockPath)
	if err == nil {
		if err := localipc.File(lockPath, false); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	lock := flock.New(lockPath, flock.SetPermissions(0600))
	defer func() { result = errors.Join(result, lock.Close()) }()
	held, err := lock.TryLock()
	if err != nil {
		return err
	}
	if lockInfo == nil {
		lockInfo, err = lock.Stat()
		if err != nil {
			return err
		}
	}
	if !held {
		if old == nil {
			return fmt.Errorf("selected state is owned; use its matching installed CLI before installation")
		}
		var status v1.DaemonStatusResponse
		args := []string{"--state-dir", o.Directory}
		if err := installCLI(ctx, o.Destination, &status, append([]string{"daemon", "status"}, args...)...); err != nil {
			return err
		}
		if !installStatus(status, identity.StateVersion, "running") {
			return fmt.Errorf("daemon status is incompatible or uncertain")
		}
		instances, err := installInstances(ctx, o.Destination, o.Directory)
		if err != nil {
			return err
		}
		if o.Consent == nil {
			return fmt.Errorf("shutdown requires terminal consent")
		}
		if err := o.Consent(instances); err != nil {
			return err
		}
		if err := installUnchanged(o.Destination, old, oldHash); err != nil {
			return err
		}
		runningPID := status.PID
		status = v1.DaemonStatusResponse{}
		if err := installCLI(ctx, o.Destination, &status, append([]string{"daemon", "stop"}, args...)...); err != nil {
			return err
		}
		if !installStatus(status, identity.StateVersion, "stopping") || status.PID != runningPID {
			return fmt.Errorf("daemon cleanup not acknowledged successfully")
		}
		deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		held, err = lock.TryLockContext(deadline, 50*time.Millisecond)
		if err != nil || !held {
			return fmt.Errorf("daemon ownership not released: %w", errors.Join(err, deadline.Err()))
		}
	}
	// Ownership and absence are both required; a stale or changing socket is ambiguous.
	if _, err := os.Lstat(SocketPath(o.Directory)); !os.IsNotExist(err) {
		return fmt.Errorf("daemon socket remains or cannot be inspected; preserve state for diagnosis")
	}
	if err := localipc.Directory(o.Directory, false); err != nil {
		return err
	}
	if err := localipc.File(lockPath, false); err != nil {
		return err
	}
	lockedInfo, err := lock.Stat()
	pathInfo, pathErr := os.Lstat(lockPath)
	if err != nil || pathErr != nil || !os.SameFile(lockedInfo, pathInfo) || !os.SameFile(lockInfo, lockedInfo) {
		return fmt.Errorf("daemon lock changed; preserve state for diagnosis")
	}
	if err := installDirectory(parent); err != nil {
		return err
	}
	if err := localipc.Directory(filepath.Dir(o.Stage), false); err != nil {
		return err
	}
	if err := installUnchanged(o.Stage, staged, stageHash); err != nil {
		return err
	}
	if err := installUnchanged(o.Destination, old, oldHash); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(o.Stage, o.Destination)
}

func installDirectory(path string) error {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical != filepath.Clean(path) {
		return fmt.Errorf("destination parent must be canonical and not a symlink")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || !installOwned(info) || info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("destination parent must be user-owned and not writable by others")
	}
	return nil
}
func installOwned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}
func installFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !installOwned(info) || info.Mode().Perm()&0022 != 0 || info.Mode().Perm()&0100 == 0 {
		return nil, fmt.Errorf("executable must be owned, regular and not writable by others")
	}
	metadata, err := buildinfo.ReadFile(path)
	if err != nil || metadata.Path != "github.com/octacian/backlot/cmd/backlot" || metadata.Main.Path != "github.com/octacian/backlot" {
		return nil, fmt.Errorf("unrecognized Backlot executable; use manual installation")
	}
	return info, nil
}
func installHash(path string) ([32]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return [32]byte{}, err
	}
	defer func() { _ = f.Close() }()
	hash := sha256.New()
	_, err = io.Copy(hash, f)
	var sum [32]byte
	copy(sum[:], hash.Sum(nil))
	return sum, err
}
func installUnchanged(path string, before os.FileInfo, hash [32]byte) error {
	after, err := os.Lstat(path)
	if before == nil && os.IsNotExist(err) {
		return nil
	}
	if err != nil || before == nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
		return fmt.Errorf("installation file changed; retry after diagnosis")
	}
	sum, err := installHash(path)
	if err != nil || sum != hash {
		return fmt.Errorf("installation bytes changed; retry after diagnosis")
	}
	return nil
}
func installOlder(target, old string) bool {
	a, b := strings.Split(target[3:], "."), strings.Split(old[3:], ".")
	for i := range a {
		// Canonical decimal strings compare without integer overflow.
		if a[i] != b[i] {
			return len(a[i]) < len(b[i]) || len(a[i]) == len(b[i]) && a[i] < b[i]
		}
	}
	return false
}

type installOutput struct{ bytes.Buffer }

func (b *installOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > manifest.MaxBytes {
		return 0, fmt.Errorf("old CLI output exceeds installation limit")
	}
	return b.Buffer.Write(p)
}
func installCLI(ctx context.Context, binary string, result any, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, append(args, "--json")...)
	var data installOutput
	command.Stdout = &data
	command.WaitDelay = time.Second
	if err := command.Run(); err != nil {
		return fmt.Errorf("compatible CLI %s failed; preserve selected state and diagnose with old executable: %w", args[0], err)
	}
	if err := manifest.Decode(data.Bytes(), ".json", result); err != nil {
		return fmt.Errorf("invalid old CLI response: %w", err)
	}
	return nil
}
func installStatus(s v1.DaemonStatusResponse, state int, status string) bool {
	lease, err := time.ParseDuration(s.LeaseDuration)
	return s.APIVersion == v1.Version && s.StateVersion == state && s.Status == status && s.PID > 0 && err == nil && lease >= 100*time.Millisecond && lease <= 24*time.Hour
}

func installInstances(ctx context.Context, binary, directory string) ([]v1.Instance, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	affected := []v1.Instance{}
	after := ""
	for count := 0; count < 10000; {
		var page v1.ListResponse
		if err := installCLI(ctx, binary, &page, "list", "--state-dir", directory, "--limit", "100", "--after", after); err != nil {
			return nil, err
		}
		if err := client.ValidateList(page); err != nil {
			return nil, err
		}
		for _, summary := range page.Instances {
			if summary.ID <= after {
				return nil, fmt.Errorf("invalid instance identity or continuation")
			}
			after = summary.ID
			count++
			var response v1.InstanceResponse
			if err := installCLI(ctx, binary, &response, "inspect", "--state-dir", directory, summary.ID); err != nil {
				return nil, err
			}
			i := response.Instance
			if response.APIVersion != v1.Version || response.Attachment != nil || i.ID != summary.ID {
				return nil, fmt.Errorf("invalid instance inspection")
			}
			if err := client.ValidateInstance(response); err != nil {
				return nil, err
			}

			active := i.Status == v1.Starting || i.Status == v1.RuntimeReady || i.Status == v1.Stopping
			if active || i.Execution != nil && (i.Execution.Kept || i.Execution.CleanupFailure != "") {
				affected = append(affected, i)
			}
		}
		if page.NextAfterID == "" {
			return affected, nil
		}
	}
	return nil, fmt.Errorf("instance inspection limit exceeded")
}

// InstallDescription renders bounded terminal-safe instance details.
func InstallDescription(i v1.Instance) string {
	cleanup := ""
	if i.Execution != nil && i.Execution.CleanupFailure != "" {
		cleanup = " (unresolved cleanup)"
	}
	return fmt.Sprintf("%s %s/%s %s%s", strconv.QuoteToASCII(i.ID), strconv.QuoteToASCII(installShort(i.Plan.Project)), strconv.QuoteToASCII(installShort(i.Plan.Scene)), i.Status, cleanup)
}
func installShort(s string) string {
	if len(s) > 160 {
		return s[:160] + "..."
	}
	return s
}
