//go:build darwin && arm64

package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"
)

// The recorded endpoint is an owned private symlink. Its initial target is the
// fault proxy; an explicit recovery handoff can point it at the verified original
// Docker socket while preserving existing proxy connections until safe release.
type providerPath struct {
	endpoint, socket, upstream string
	linkProof, socketProof     os.FileInfo
	receiptProof               os.FileInfo
	target                     string
}

func listenProvider(endpoint, upstream string) (net.Listener, *providerPath, error) {
	socket := endpoint + ".proxy"
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return nil, nil, err
	}
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	proof, err := os.Lstat(socket)
	if err != nil {
		_ = listener.Close()
		return nil, nil, err
	}
	p := &providerPath{endpoint: endpoint, socket: socket, upstream: upstream, socketProof: proof, target: socket}
	if err = os.Chmod(socket, 0600); err == nil {
		err = os.Symlink(socket, endpoint)
	}
	if err != nil {
		_ = listener.Close()
		_ = removeProvenPath(socket, proof)
		return nil, nil, err
	}
	p.linkProof, err = os.Lstat(endpoint)
	if err != nil {
		_ = listener.Close()
		_ = removeProvenPath(socket, proof)
		return nil, nil, fmt.Errorf("provider link proof unavailable; uncertain path preserved at %s: %w", endpoint, err)
	}
	return listener, p, nil
}
func removeProvenPath(path string, proof os.FileInfo) error {
	actual, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if proof == nil || !os.SameFile(proof, actual) {
		return fmt.Errorf("uncertain provider path preserved: %s", path)
	}
	return os.Remove(path)
}
func (p *providerPath) handoff() error {
	actual, err := os.Lstat(p.endpoint)
	if err != nil {
		return err
	}
	target, err := os.Readlink(p.endpoint)
	if err != nil {
		return err
	}
	if !os.SameFile(actual, p.linkProof) || target != p.target {
		return errors.New("foreign provider endpoint preserved; recovery handoff blocked")
	}
	if p.target == p.upstream {
		return errors.Join(p.verifyHandoff(), p.completeReceipt())
	}
	if err = removeProvenPath(p.endpoint, p.linkProof); err != nil {
		return err
	}
	// Symlink creation is exclusive: a concurrent foreign path is never replaced.
	if err = os.Symlink(p.upstream, p.endpoint); err != nil {
		return err
	}
	p.linkProof, err = os.Lstat(p.endpoint)
	if err != nil {
		return err
	}
	p.target = p.upstream
	return errors.Join(p.verifyHandoff(), p.completeReceipt())
}
func (p *providerPath) receiptPath() string { return p.endpoint + ".recovery.json" }

// Receipt retries may complete only a file created and retained by this owner.
// A pre-existing or replaced obstruction is reported, never overwritten.
func (p *providerPath) completeReceipt() (result error) {
	data, err := json.MarshalIndent(map[string]any{"endpoint": p.endpoint, "verified_upstream": p.upstream, "link_inode": p.linkProof.Sys().(*syscall.Stat_t).Ino, "link_device": p.linkProof.Sys().(*syscall.Stat_t).Dev, "recovery": "recorded endpoint now reaches original provider; use public daemon start/list/destroy/stop with retained private state; remove only owned link after destruction and owner join"}, "", "  ")
	if err != nil {
		return err
	}
	name := p.receiptPath()
	var file *os.File
	if p.receiptProof == nil {
		file, err = os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fmt.Errorf("recovery receipt incomplete; obstruction preserved: %w", err)
		}
		p.receiptProof, err = file.Stat()
	} else {
		actual, statErr := os.Lstat(name)
		if statErr != nil {
			return fmt.Errorf("owned recovery receipt unavailable: %w", statErr)
		}
		if !os.SameFile(actual, p.receiptProof) || !actual.Mode().IsRegular() || actual.Mode().Perm() != 0600 {
			return errors.New("uncertain recovery receipt preserved")
		}
		file, err = os.OpenFile(name, os.O_RDWR, 0)
	}
	if file != nil {
		defer func() { result = errors.Join(result, file.Close()) }()
	}
	if err != nil {
		return err
	}
	actual, err := file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(actual, p.receiptProof) || !actual.Mode().IsRegular() || actual.Mode().Perm() != 0600 {
		return errors.New("recovery receipt identity/mode changed; preserved")
	}
	existing, err := io.ReadAll(file)
	if err != nil {
		return err
	}
	if bytes.Equal(existing, data) {
		return file.Sync()
	}
	if err = file.Truncate(0); err != nil {
		return err
	}
	if _, err = file.WriteAt(data, 0); err != nil {
		return err
	}
	return file.Sync()
}

func (p *providerPath) verifyHandoff() error {
	status, err := unixCall(p.endpoint, http.MethodGet, "/_ping")
	if err != nil {
		return fmt.Errorf("provider handoff path retained but availability unverified: %w", err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("provider handoff ping status %d", status)
	}
	return nil
}
func (p *providerPath) remove() error {
	target, err := os.Readlink(p.endpoint)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && target != p.target {
		return errors.New("foreign provider link target preserved")
	}
	return errors.Join(removeProvenPath(p.endpoint, p.linkProof), removeProvenPath(p.socket, p.socketProof))
}

type providerDependency struct {
	owner               *harness
	pending             map[string]bool
	uncertain, released bool
	path                *providerPath
	closeProxy          func() error
	recover             func() error
}

func (h *harness) dependency(owner *harness, p *providerPath, server *http.Server, done <-chan error, transport *http.Transport) *providerDependency {
	d := &providerDependency{owner: owner, pending: map[string]bool{}, path: p}
	consumed := false
	d.closeProxy = func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			return err
		}
		if !consumed {
			select {
			case err := <-done:
				consumed = true
				if err != nil && !errors.Is(err, http.ErrServerClosed) {
					return err
				}
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		transport.CloseIdleConnections()
		return p.remove()
	}
	d.recover = func() error {
		owner := d.owner
		if owner.ownerUnresolved || owner.daemon != nil {
			return errors.New("provider recovery needs verified prior owner join")
		}
		if err := owner.startDaemon(10 * time.Second); err != nil {
			return err
		}
		var failures error
		for id := range d.pending {
			if _, err := owner.instance("destroy", id); err != nil {
				failures = errors.Join(failures, err)
			}
		}
		// A fresh owner has no live collector from the original daemon. Historical
		// instance collection results remain in state and do not predict this exit.
		owner.stop()
		if owner.daemon != nil {
			failures = errors.Join(failures, errors.New("recovery owner remains unjoined"))
		}
		return failures
	}
	h.dependencies = append(h.dependencies, d)
	if owner != h {
		owner.dependencies = append(owner.dependencies, d)
	}
	return d
}
func (d *providerDependency) track(id string) {
	if id == "" {
		d.uncertain = true
	} else {
		d.pending[id] = true
	}
}
func (d *providerDependency) releasable() bool {
	return !d.uncertain && len(d.pending) == 0 && d.owner.daemon == nil && !d.owner.ownerUnresolved
}
func (d *providerDependency) finish() error {
	if d.released || !d.releasable() {
		return nil
	}
	if err := d.closeProxy(); err != nil {
		return err
	}
	d.released = true
	return nil
}
func (d *providerDependency) cleanup() error {
	if d.released {
		return nil
	}
	var recovery error
	if len(d.pending) > 0 && !d.owner.ownerUnresolved && d.owner.daemon == nil {
		recovery = d.recover()
	}
	if d.releasable() {
		return errors.Join(recovery, d.finish())
	}
	handoff := d.path.handoff()
	if handoff != nil {
		return errors.Join(recovery, handoff, fmt.Errorf("unresolved provider dependency retained at %s", d.path.endpoint))
	}
	return errors.Join(recovery, fmt.Errorf("unresolved allocations/owner; usable provider handoff retained at %s; private recovery receipt beside endpoint", d.path.endpoint))
}
func (h *harness) releaseLog() {
	if h.logClosed || h.daemonLog == nil {
		return
	}
	if h.daemon != nil || h.ownerUnresolved {
		return
	}
	for _, d := range h.dependencies {
		if !d.released {
			return
		}
	}
	if err := h.daemonLog.Close(); err != nil {
		h.t.Error("owned log close", err)
		return
	}
	h.logClosed = true
}

// Late recovery belongs to the active cleanup owner, not a completed subtest.
func (h *harness) cleanupDependencies() error {
	var result error
	for _, d := range h.dependencies {
		previous := d.owner.t
		d.owner.t = h.t
		result = errors.Join(result, d.cleanup())
		d.owner.releaseLog()
		d.owner.t = previous
	}
	return result
}
