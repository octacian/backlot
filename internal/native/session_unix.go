//go:build darwin || linux

package native

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/octacian/backlot/internal/native/procidentity"
	"golang.org/x/sys/unix"
)

const sessionCandidateLimit = 16384
const sessionAcquisitionLimit = 4096
const sessionHandleLimit = 256

// Failed cleanup retains known identities under the existing guardian owner for
// a later authenticated retry. Guardian loss still loses this private authority.
type sessionCleanupState struct{ members map[int]*procidentity.Handle }

// Session authority exists only while this authenticated guardian executes.
func sessionAvailable() error {
	h, err := procidentity.Acquire(os.Getpid())
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()
	sid, err := h.Session()
	if err != nil {
		return err
	}
	if sid != os.Getpid() {
		return errors.New("guardian does not own its session")
	}
	return nil
}

// observeSession reads membership, execution state, then validates the acquired
// identity again. A Linux pidfd's signal-zero validation includes zombies.
func observeSession(h *procidentity.Handle, pid int) (bool, error) {
	sid, err := h.Session()
	if err != nil {
		return false, err
	}
	if sid != os.Getpid() {
		return false, nil
	}
	dead, err := anchorExited(pid)
	if err != nil {
		return false, err
	}
	if dead {
		return false, nil
	}
	sid, err = h.Session()
	return sid == os.Getpid(), err
}

func acquireSession(ctx context.Context, pid int, budget *int) (*procidentity.Handle, error) {
	for range 3 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		*budget--
		if *budget < 0 {
			return nil, errors.New("session identity budget exhausted")
		}
		h, err := procidentity.Acquire(pid)
		if err == nil {
			var live bool
			live, err = observeSession(h, pid)
			if err == nil && live {
				return h, nil
			}
			_ = h.Close()
			if err == nil {
				return nil, nil
			}
		}
		dead, stateErr := anchorExited(pid)
		if vanished(stateErr) || stateErr == nil && dead {
			return nil, nil
		}
		if errors.Is(err, procidentity.ErrChanged) {
			continue
		}
		if vanished(err) {
			return nil, nil
		}
		return nil, err
	}
	return nil, errors.New("session identity changed beyond retry budget")
}

// sessionListener keeps acquired identities through descriptor/family inspection,
// then validates the same handles again before returning the observation. All
// work executes inside the live session guardian; no handle crosses the protocol.
func sessionListener(ctx context.Context, address string, anchor int) (bool, bool, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return false, false, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return false, false, errors.New("invalid listener port")
	}
	held := map[int]*procidentity.Handle{}
	defer func() {
		for _, h := range held {
			_ = h.Close()
		}
	}()
	budget := sessionAcquisitionLimit
	owned, conflict, err := listenerStateWith(ctx, 1, port, host, func(pid int) (int, error) {
		if pid == os.Getpid() || pid == anchor {
			return 2, nil
		}
		if held[pid] == nil && len(held) >= sessionHandleLimit {
			return 0, errors.New("listener identity budget exhausted")
		}
		h, err := acquireSession(ctx, pid, &budget)
		if err != nil {
			return 0, err
		}
		if h == nil {
			dead, stateErr := anchorExited(pid)
			if vanished(stateErr) || stateErr == nil && dead {
				return 0, syscall.ESRCH
			}
			if stateErr != nil {
				return 0, stateErr
			}
			sid, stateErr := currentSession(pid)
			if stateErr != nil {
				return 0, stateErr
			}
			if sid == os.Getpid() {
				return 0, syscall.ESRCH
			}
			return 2, nil
		}
		if prior := held[pid]; prior != nil {
			_ = prior.Close()
		}
		held[pid] = h
		return 1, nil
	})
	if err != nil {
		return false, false, err
	}
	for pid, h := range held {
		live, err := observeSession(h, pid)
		if vanished(err) || errors.Is(err, procidentity.ErrChanged) || err == nil && !live {
			return false, false, nil
		}
		if err != nil {
			return false, false, err
		}
	}
	return owned, conflict, nil
}

func vanished(err error) bool { return errors.Is(err, syscall.ESRCH) || os.IsNotExist(err) }

// stopSession retains identities of positively observed live members until they
// exit/become zombies. Enumeration cannot erase a known live consumer. Bounded
// reacquisition handles Darwin exec; all handles close before anchor release.
func (w *ownedWorkload) stopSession(grace time.Duration) error {
	if err := sessionAvailable(); err != nil {
		return err
	}
	termEnd := time.Now().Add(grace)
	ctx, cancel := context.WithDeadline(context.Background(), termEnd.Add(sessionCleanupOverhead))
	defer cancel()
	if w.sessionState == nil {
		w.sessionState = &sessionCleanupState{members: map[int]*procidentity.Handle{}}
	}
	held := w.sessionState.members
	scan := w.scanSession
	if scan == nil {
		scan = sessionCandidates
	}
	budget := sessionAcquisitionLimit
	empty := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		pids, err := scan(ctx)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		seen := map[int]bool{}
		for _, pid := range pids {
			seen[pid] = true
		}
		for pid := range held {
			if !seen[pid] {
				pids = append(pids, pid)
			}
		}
		live := false
		sig := syscall.SIGTERM
		if !time.Now().Before(termEnd) {
			sig = syscall.SIGKILL
		}
		for _, pid := range pids {
			if pid == os.Getpid() || pid == w.anchor.Process.Pid {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			previous := held[pid]
			h := previous
			if h != nil {
				member, err := observeSession(h, pid)
				if err != nil || !member {
					dead, stateErr := anchorExited(pid)
					if vanished(err) || vanished(stateErr) || stateErr == nil && dead {
						_ = h.Close()
						delete(held, pid)
						continue
					}
					if errors.Is(err, procidentity.ErrChanged) {
						// Retain the known obligation while reacquisition is
						// unavailable; a later scan cannot forget this process.
						h = nil
					} else {
						if err != nil {
							return err
						}
						return errors.New("known live member left guardian session")
					}
				}
			}
			if h == nil {
				if previous == nil && len(held) >= sessionHandleLimit {
					return errors.New("session handle budget exhausted")
				}
				h, err = acquireSession(ctx, pid, &budget)
				if err != nil {
					return err
				}
				if h == nil {
					if previous != nil {
						dead, stateErr := anchorExited(pid)
						if !vanished(stateErr) && (stateErr != nil || !dead) {
							return errors.New("known member identity could not be reacquired")
						}
						_ = previous.Close()
						delete(held, pid)
					}
					continue
				}
				if previous != nil {
					_ = previous.Close()
				}
				held[pid] = h

			}
			live = true
			// observeSession immediately precedes identity-checked actuation. Kernel
			// rejection never falls back to a numeric PID or an enumerated group signal.
			if err := h.Signal(sig); err != nil && !vanished(err) && !errors.Is(err, procidentity.ErrChanged) {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !live && empty {
			break
		}
		empty = !live
		select {
		case <-ctx.Done():
			return errors.New("session cleanup observation deadline exhausted")
		case <-time.After(20 * time.Millisecond):
		}
	}
	// Retained direct-child authority, never a scan PID, terminates the anchor.
	// Common original-group proof retains its sole Wait and forbids later signals.
	// All retained member identities were retired as exited/zombie above.
	if err := w.anchor.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

func currentSession(pid int) (int, error) { return unix.Getsid(pid) }
