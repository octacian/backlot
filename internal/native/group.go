// Package native supervises cooperative commands in a pinned process group.
package native

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	v1 "github.com/octacian/backlot/api/v1"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"
)

// Identity identifies a private guardian authority, never a signal target.
type Identity struct {
	PID     int    `json:"pid"`
	Birth   string `json:"birth"`
	Control string `json:"control"`
	Token   string `json:"token"`
}

// Spec is the private activation payload after ownership journaling.
type Spec struct {
	Executable  string
	Args        []string
	Environment []string
	Directory   string
	InstanceID  string
	Component   string
	Attempt     string
}

// Exit preserves original root status independently from collection.
type Exit struct {
	Code              int    `json:"code"`
	Error             string `json:"error,omitempty"`
	CollectionFailure string `json:"collection_failure,omitempty"`
	GuardianReady     bool   `json:"guardian_ready,omitempty"`
	AnchorPID         int    `json:"anchor_pid,omitempty"`
}

// Group joins one guardian authority and its status descriptors.
type Group struct {
	Identity    Identity
	control     *os.File
	done        chan struct{}
	statusDone  chan struct{}
	ready       chan struct{}
	anchorReady chan struct{}
	mu          sync.Mutex
	exit        *Exit
	pgid        int
}

// Start creates an inactive authority; no application starts before Activate.
func Start(executable string, stdout, stderr *os.File) (*Group, error) {
	dir, err := os.MkdirTemp("/tmp", "blctl-")
	if err != nil {
		return nil, err
	}
	token := make([]byte, 32)
	if _, err = rand.Read(token); err != nil {
		return nil, err
	}
	input, control, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	events, output, err := os.Pipe()
	if err != nil {
		_ = input.Close()
		_ = control.Close()
		return nil, err
	}
	cmd := exec.Command(executable, "__native-supervisor")
	configure(cmd)
	cmd.Stdin = input
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.ExtraFiles = []*os.File{output}
	err = cmd.Start()
	_ = input.Close()
	_ = output.Close()
	if err != nil {
		_ = control.Close()
		_ = events.Close()
		return nil, err
	}
	id, identityErr := identify(cmd.Process.Pid)
	if identityErr != nil {
		id.PID = cmd.Process.Pid
	}
	id.Control = filepath.Join(dir, "control.sock")
	id.Token = hex.EncodeToString(token)
	g := &Group{Identity: id, control: control, done: make(chan struct{}), statusDone: make(chan struct{}), ready: make(chan struct{}), anchorReady: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(g.done) }()
	go func() {
		defer close(g.statusDone)
		defer func() { _ = events.Close() }()
		dec := json.NewDecoder(events)
		ready, anchor := false, false
		for {
			var r Exit
			if err := dec.Decode(&r); err != nil {
				g.mu.Lock()
				if g.exit == nil {
					g.exit = &Exit{Code: -1, Error: "command status unavailable", CollectionFailure: "guardian status authority lost"}
				}
				g.mu.Unlock()
				return
			}
			g.mu.Lock()
			if r.GuardianReady {
				if !ready {
					close(g.ready)
					ready = true
				}
			} else if r.AnchorPID > 0 {
				g.pgid = r.AnchorPID
				if !anchor {
					close(g.anchorReady)
					anchor = true
				}
			} else {
				g.exit = &r
			}
			g.mu.Unlock()
		}
	}()
	if identityErr != nil {
		_ = control.Close()
		select {
		case <-g.done:
			<-g.statusDone
			return nil, identityErr
		case <-time.After(2 * time.Second):
			return g, errors.New("inactive guardian identity and join unavailable; preserve launch uncertainty")
		}
	}
	if err := json.NewEncoder(control).Encode(id); err != nil {
		_ = control.Close()
		return g, err
	}
	select {
	case <-g.ready:
		return g, nil
	case <-g.done:
		_ = control.Close()
		return g, errors.New("inactive guardian exited")
	case <-time.After(2 * time.Second):
		_ = control.Close()
		return g, errors.New("guardian handshake unavailable; preserve launch uncertainty")
	}
}

// Activate starts application work after durable ownership recording.
func (g *Group) Activate(spec Spec) error {
	if err := json.NewEncoder(g.control).Encode(spec); err != nil {
		return err
	}
	select {
	case <-g.anchorReady:
		return nil
	case <-g.done:
		return errors.New("guardian exited during activation")
	case <-time.After(2 * time.Second):
		return errors.New("anchor activation unavailable")
	}
}

// PGID returns the workload group for read-only listener ownership probes.
func (g *Group) PGID() int { g.mu.Lock(); defer g.mu.Unlock(); return g.pgid }

// Result returns a copy of the root result when available.
func (g *Group) Result() *Exit {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.exit == nil {
		return nil
	}
	r := *g.exit
	return &r
}

// Alive reports whether the original guardian is still joined to this daemon.
func (g *Group) Alive() bool {
	select {
	case <-g.done:
		return false
	default:
		return true
	}
}

// Stop obtains verified cleanup and joins this daemon's guardian descriptors.
func (g *Group) Stop(grace time.Duration) error {
	_ = g.control.Close()
	if err := Stop(g.Identity, grace); err != nil {
		return err
	}
	_ = g.control.Close()
	select {
	case <-g.done:
	case <-time.After(2 * time.Second):
		return errors.New("guardian did not join after cleanup proof")
	}
	select {
	case <-g.statusDone:
	case <-time.After(time.Second):
		return errors.New("status collection did not join")
	}
	return nil
}

// Wait observes root completion under the caller's execution deadline.
func (g *Group) Wait(ctx context.Context) (*Exit, error) {
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		if r := g.Result(); r != nil {
			return r, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-g.done:
			return nil, errors.New("guardian exited unexpectedly")
		case <-tick.C:
		}
	}
}

type logWriter struct {
	spec   Spec
	stream string
	mu     *sync.Mutex
}

func (w *logWriter) Write(data []byte) (int, error) {
	size := len(data)
	w.mu.Lock()
	defer w.mu.Unlock()
	for len(data) > 0 {
		n := min(len(data), 8192)
		r := v1.LogRecord{InstanceID: w.spec.InstanceID, Component: w.spec.Component, Attempt: w.spec.Attempt, Time: time.Now().UTC().Format(time.RFC3339Nano), Stream: w.stream, Message: string(data[:n])}
		if !utf8.Valid(data[:n]) {
			r.Message = ""
			r.Data = base64.StdEncoding.EncodeToString(data[:n])
		}
		if err := json.NewEncoder(os.Stdout).Encode(r); err != nil {
			return 0, err
		}
		data = data[n:]
	}
	return size, nil
}
