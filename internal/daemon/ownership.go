package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/localipc"
)

// stateOwnership holds the existing inode, never replacing a daemon's lock.
// It does not open state.db or grant resource cleanup authority.
type stateOwnership struct {
	lock        *flock.Flock
	directory   string
	root, inode os.FileInfo
}

func openStateOwnership(directory string, create bool) (*stateOwnership, error) {
	if err := localipc.Directory(directory, create); err != nil {
		return nil, err
	}
	root, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "daemon.lock")
	inode, err := os.Lstat(path)
	if err == nil {
		if err := localipc.File(path, false); err != nil {
			return nil, err
		}
	} else if !create || !os.IsNotExist(err) {
		return nil, err
	}
	flags := os.O_RDWR
	if create {
		flags |= os.O_CREATE
	}
	return &stateOwnership{lock: flock.New(path, flock.SetPermissions(0600), flock.SetFlag(flags)), directory: directory, root: root, inode: inode}, nil
}
func (o *stateOwnership) try() (bool, error) {
	held, err := o.lock.TryLock()
	if err == nil && o.inode == nil {
		o.inode, err = o.lock.Stat()
	}
	return held, err
}
func (o *stateOwnership) verify() error {
	if err := localipc.Directory(o.directory, false); err != nil {
		return err
	}
	root, err := os.Lstat(o.directory)
	if err != nil || !os.SameFile(o.root, root) {
		return problem("ownership_unknown", "private state directory changed; preserve state for diagnosis")
	}
	path := filepath.Join(o.directory, "daemon.lock")
	if err := localipc.File(path, false); err != nil {
		return err
	}
	inode, err := o.lock.Stat()
	current, pathErr := os.Lstat(path)
	if err != nil || pathErr != nil || o.inode == nil || !os.SameFile(o.inode, inode) || !os.SameFile(inode, current) {
		return problem("ownership_unknown", "daemon lock changed; preserve state for diagnosis")
	}
	if _, err := os.Lstat(SocketPath(o.directory)); !os.IsNotExist(err) {
		return problem("ownership_unknown", "daemon socket remains or cannot be inspected; preserve state for diagnosis")
	}
	return nil
}

// InfraStop observes daemon-owned cleanup and then holds ownership through its
// final completion callback. Cancellation never cancels accepted cleanup.
func InfraStop(ctx context.Context, directory string, emit func(v1.InfraStopRecord) error) (result error) {
	if emit == nil {
		emit = func(v1.InfraStopRecord) error { return nil }
	}
	completion := v1.InfraStopRecord{APIVersion: v1.Version, Type: "completion", Directory: directory, State: "unknown"}
	defer func() {
		completion.Error = infraError(result)
		result = errors.Join(result, emit(completion))
	}()
	ownership, err := openStateOwnership(directory, true)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, ownership.lock.Close()) }()
	held, err := ownership.try()
	if err != nil {
		return err
	}
	if held {
		if err := ownership.verify(); err != nil {
			return err
		}
		completion.State = "absent"
		return problem("cleanup_unknown", "daemon absent; retained resource cleanup is unknown; no database read or provider cleanup attempted")
	}
	cli := client.New(SocketPath(directory))
	defer cli.Close()
	if err := CheckAccess(directory); err != nil {
		return err
	}
	// Capture the private identities before acceptance, then keep observing only
	// the original generation. A replacement is diagnostic, never a new stop target.
	probe, err := cli.Infra(ctx, "status", v1.InfraRequest{APIVersion: v1.Version})
	if err != nil && probe.DaemonGeneration == "" {
		return err
	}
	completion.DaemonGeneration = probe.DaemonGeneration
	terminal, err := cli.InfraShutdown(ctx, probe.DaemonGeneration, func(r v1.InfraStopRecord) error {
		if r.DaemonGeneration != probe.DaemonGeneration {
			return problem("replacement_daemon", "daemon changed before shutdown acceptance")
		}
		completion.OperationID = r.OperationID
		return emit(r)
	})
	if err != nil {
		return err
	}
	if terminal.Error != nil {
		return terminal.Error
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return problem("completion_unknown", "shutdown cleanup succeeded; ownership observation cancelled; do not automatically retry")
		}
		held, err = ownership.try()
		if err != nil {
			return err
		}
		if held {
			if err := ownership.verify(); err != nil {
				return err
			}
			completion.State = "complete"
			// Emit under held ownership, including its private identity checks.
			completion.Error = nil
			err := emit(completion)
			emit = func(v1.InfraStopRecord) error { return nil }
			return err
		}
		observed, probeErr := cli.Infra(ctx, "status", v1.InfraRequest{APIVersion: v1.Version})
		if (probeErr == nil || observed.DaemonGeneration != "") && observed.DaemonGeneration != probe.DaemonGeneration {
			completion.ReplacementGeneration = observed.DaemonGeneration
			return problem("replacement_daemon", "observed replacement daemon; it was not stopped; original ownership completion is unknown")
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
		}
	}
}

// InfraAbsent verifies an existing private scope without opening the database or
// creating administrative state. Absence never establishes retained cleanup.
func InfraAbsent(directory string) error {
	if _, err := os.Lstat(directory); os.IsNotExist(err) {
		return nil
	}
	if err := localipc.Directory(directory, false); err != nil {
		return err
	}
	if err := localipc.File(filepath.Join(directory, "daemon.lock"), false); err != nil {
		return err
	}
	o, err := openStateOwnership(directory, false)
	if err != nil {
		return err
	}
	defer func() { _ = o.lock.Close() }()
	held, err := o.try()
	if err != nil {
		return err
	}
	if !held {
		return problem("ownership_unknown", "daemon socket missing but state remains owned; cleanup completion unknown")
	}
	return o.verify()
}
