// Package native supervises cooperative applications in an owned session and strict system helpers in a pinned group.
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
	Protocol  int    `json:"protocol"`
	Purpose   string `json:"purpose"`
	Component string `json:"component,omitempty"`
	Attempt   string `json:"attempt,omitempty"`
	PID       int    `json:"pid"`
	Birth     string `json:"birth"`
	Control   string `json:"control"`
	Token     string `json:"token"`
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
	// Known distinguishes an observed root exit from an infrastructure observation failure.
	Known             bool   `json:"known,omitempty"`
	Code              int    `json:"code"`
	Error             string `json:"error,omitempty"`
	CollectionFailure string `json:"collection_failure,omitempty"`
	GuardianReady     bool   `json:"guardian_ready,omitempty"`
	AnchorPID         int    `json:"anchor_pid,omitempty"`
}

// Group joins one guardian authority and its status descriptors.
type Group struct {
	Identity          Identity
	control           *os.File
	done              chan struct{}
	statusDone        chan struct{}
	ready             chan struct{}
	anchorReady       chan struct{}
	mu                sync.Mutex
	exit              *Exit
	collectionFailure string
	cleanup           *v1.NativeCleanupResult
	pgid              int
}

// Start creates an inactive authority; no application starts before Activate.
func Start(executable string, stdout, stderr *os.File) (*Group, error) {
	return start(executable, stdout, stderr, "strict", "", "")
}

// StartApplication creates an inactive session authority for one application attempt.
func StartApplication(executable string, stdout, stderr *os.File, component, attempt string) (*Group, error) {
	if component == "" || attempt == "" {
		return nil, errors.New("application attempt identity missing")
	}
	group, err := start(executable, stdout, stderr, "application", component, attempt)
	if err != nil {
		err = &authorityError{message: "native application guardian could not start; preserve uncertain launch ownership", cause: err}
	}
	return group, err
}

func start(executable string, stdout, stderr *os.File, purpose, component, attempt string) (*Group, error) {
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
	id.Protocol, id.Purpose, id.Component, id.Attempt = 1, purpose, component, attempt
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
	if g.exit == nil && g.collectionFailure == "" {
		return nil
	}
	r := Exit{Error: "command status unavailable"}
	if g.exit != nil {
		r = *g.exit
	}
	if r.CollectionFailure == "" {
		r.CollectionFailure = g.collectionFailure
	}
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
func (g *Group) Stop(grace time.Duration) (err error) {
	defer func() {
		if err != nil {
			if g.Identity.Purpose == "application" {
				g.mu.Lock()
				if g.cleanup == nil || g.cleanup.Confidence == v1.ObservedEmpty {
					g.cleanup = sessionResult(g.Identity, v1.CleanupIncomplete)
				}
				g.mu.Unlock()
			}
			// A failed authority/collector join cannot attest complete evidence.
			// Keep this sticky even if a late root status later becomes available.
			g.mu.Lock()
			g.collectionFailure = "native output/status collection could not be verified"
			g.mu.Unlock()
		}
	}()
	_ = g.control.Close()
	result, stopErr := StopResult(g.Identity, grace)
	g.mu.Lock()
	if g.cleanup == nil || g.cleanup.Confidence == v1.ObservedEmpty {
		g.cleanup = result
	}
	g.mu.Unlock()
	if stopErr != nil {
		return stopErr
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

// CleanupResult returns the last session cleanup result; strict helpers return nil.
func (g *Group) CleanupResult() *v1.NativeCleanupResult {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cleanup == nil {
		return nil
	}
	r := *g.cleanup
	return &r
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
