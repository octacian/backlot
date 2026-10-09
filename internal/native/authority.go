package native

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type cleanupRequest struct {
	Identity Identity
	Grace    time.Duration
	Crash    bool
}
type cleanupProof struct {
	Identity Identity
	Error    string
}
type authorityRequest struct {
	request cleanupRequest
	reply   chan cleanupProof
	done    chan struct{}
}

// Stop uses only the private authority. Process observations never authorize signals.
func Stop(id Identity, grace time.Duration) error {
	if id.PID <= 1 || id.Birth == "" || id.Control == "" || len(id.Token) != 64 {
		return errors.New("native control authority missing; preserve uncertain processes")
	}
	if grace < 0 {
		return errors.New("invalid cleanup grace")
	}
	var proof cleanupProof
	connection, err := net.DialTimeout("unix", id.Control, time.Second)
	if err == nil {
		_ = connection.SetDeadline(time.Now().Add(grace).Add(8 * time.Second))
		err = json.NewEncoder(connection).Encode(cleanupRequest{Identity: id, Grace: grace})
		if err == nil {
			err = json.NewDecoder(connection).Decode(&proof)
		}
		_ = connection.Close()
	}
	if err != nil {
		data, readErr := os.ReadFile(filepath.Join(filepath.Dir(id.Control), "receipt.json"))
		if readErr != nil || json.Unmarshal(data, &proof) != nil {
			return errors.New("native control authority unavailable; preserve uncertain processes")
		}
	}
	if proof.Identity != id {
		return errors.New("native cleanup response identity mismatch; preserve uncertain processes")
	}
	if proof.Error != "" {
		return errors.New(proof.Error)
	}
	// A receipt is emitted after anchor and collectors join, but success also requires
	// the outside authority's original process to have exited. Never signal this PID.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		actual, err := identify(id.PID)
		if errors.Is(err, syscall.ESRCH) || os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("guardian exit verification unavailable: %w", err)
		}
		if actual.Birth != id.Birth {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("guardian exit not verified after cleanup proof")
}

// Supervise owns an unreaped anchor child throughout every workload-group signal.
func Supervise() error {
	if !supported() {
		return errors.New("native execution unsupported")
	}
	status := os.NewFile(3, "command-status")
	if status == nil {
		return errors.New("status descriptor missing")
	}
	defer func() { _ = status.Close() }()
	input, inputErr := controlInput()
	if inputErr != nil {
		return inputErr
	}
	defer func() { _ = input.Close() }()
	decoder := json.NewDecoder(input)
	var id Identity
	if err := decoder.Decode(&id); err != nil {
		return err
	}
	own, err := identify(os.Getpid())
	if err != nil {
		return err
	}
	if own.PID != id.PID || own.Birth != id.Birth || len(id.Token) != 64 {
		return errors.New("guardian initialization identity mismatch")
	}
	listener, err := net.Listen("unix", id.Control)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	if err := os.Chmod(id.Control, 0600); err != nil {
		return err
	}
	requests := make(chan authorityRequest)
	acceptDone := make(chan struct{})
	halt := make(chan struct{})
	var stopOnce sync.Once
	stopAuthority := func() { stopOnce.Do(func() { close(halt); _ = listener.Close() }); <-acceptDone }
	defer stopAuthority()
	go func() {
		defer close(acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(time.Second))
				var request cleanupRequest
				if json.NewDecoder(conn).Decode(&request) != nil {
					return
				}
				if request.Identity != id || subtle.ConstantTimeCompare([]byte(request.Identity.Token), []byte(id.Token)) != 1 {
					_ = json.NewEncoder(conn).Encode(cleanupProof{Error: "control identity mismatch"})
					return
				}
				if request.Crash {
					if os.Getenv("BACKLOT_NATIVE_TEST_FAULTS") != "1" {
						_ = json.NewEncoder(conn).Encode(cleanupProof{Identity: id, Error: "fixture self-fault disabled"})
						return
					}
					current, err := identify(os.Getpid())
					if err != nil || current.PID != id.PID || current.Birth != id.Birth {
						_ = json.NewEncoder(conn).Encode(cleanupProof{Identity: id, Error: "fixture receiver identity uncertain"})
						return
					}
					if err := json.NewEncoder(conn).Encode(cleanupProof{Identity: id}); err != nil {
						return
					}
					_ = conn.Close()
					// Self is live while this receiver executes; this PID cannot be reused.
					self, err := os.FindProcess(os.Getpid())
					if err != nil {
						return
					}
					_ = self.Kill()
					_ = self.Release()
					return
				}
				_ = conn.SetDeadline(time.Now().Add(request.Grace).Add(8 * time.Second))
				if request.Grace < 0 {
					return
				}
				reply := make(chan cleanupProof, 1)
				done := make(chan struct{})
				defer close(done)
				select {
				case requests <- authorityRequest{request, reply, done}:
				case <-halt:
					return
				}
				var proof cleanupProof
				select {
				case proof = <-reply:
				case <-halt:
					return
				}
				_ = json.NewEncoder(conn).Encode(proof)
			}()
		}
	}()
	var statusMu sync.Mutex
	publish := func(r Exit) { statusMu.Lock(); _ = json.NewEncoder(status).Encode(r); statusMu.Unlock() }
	publish(Exit{GuardianReady: true})
	specs := make(chan Spec, 1)
	specDone := make(chan struct{})
	decodeDone := specDone
	go func() {
		defer close(decodeDone)
		var spec Spec
		if decoder.Decode(&spec) == nil {
			specs <- spec
		}
	}()
	var workload *ownedWorkload
	for {
		select {
		case spec := <-specs:
			if workload != nil {
				return errors.New("duplicate activation")
			}
			workload, err = startWorkload(spec, publish)
			if err != nil {
				publish(Exit{Code: -1, Error: "native anchor could not start"})
			}
		case request := <-requests:
			cleanupErr := error(nil)
			if workload != nil {
				cleanupErr = workload.stop(request.request.Grace)
			}
			proof := cleanupProof{Identity: id}
			if cleanupErr != nil {
				proof.Error = cleanupErr.Error()
				request.reply <- proof
				continue
			}
			_ = input.Close()
			<-decodeDone
			if workload == nil {
				// Activation can no longer be read or executed by this authority.
				publish(Exit{Code: -1, Error: "command cancelled before activation"})
			}
			data, encodeErr := json.Marshal(proof)
			if encodeErr == nil {
				encodeErr = os.WriteFile(filepath.Join(filepath.Dir(id.Control), "receipt.json"), data, 0600)
			}
			if encodeErr != nil {
				proof.Error = "cleanup receipt could not persist"
				request.reply <- proof
				continue
			}
			request.reply <- proof
			<-request.done
			stopAuthority()
			return nil
		case <-specDone:
			if workload == nil {
				select {
				case spec := <-specs:
					workload, err = startWorkload(spec, publish)
					if err != nil {
						publish(Exit{Code: -1, Error: "native anchor could not start"})
					}
				default:
					// EOF before activation proves no workload can subsequently start.
					proof := cleanupProof{Identity: id}
					data, err := json.Marshal(proof)
					if err != nil {
						return err
					}
					if err := os.WriteFile(filepath.Join(filepath.Dir(id.Control), "receipt.json"), data, 0600); err != nil {
						return err
					}
					publish(Exit{Code: -1, Error: "command cancelled before activation"})
					return nil
				}
			}
			specDone = nil
		}
	}
}

type ownedWorkload struct {
	anchor       *exec.Cmd
	collected    chan error
	statusDone   chan struct{}
	reads        []*os.File
	publish      func(Exit)
	mu           sync.Mutex
	result       Exit
	released     bool
	beforeSignal func()
	scan         func(int) ([]int, error)
}

func startWorkload(spec Spec, publish func(Exit)) (*ownedWorkload, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	outRead, outWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	errRead, errWrite, err := os.Pipe()
	if err != nil {
		_ = outRead.Close()
		_ = outWrite.Close()
		return nil, err
	}
	anchor := exec.Command(executable, "__native-anchor")
	configureAnchor(anchor)
	if err := anchor.Start(); err != nil {
		_ = outRead.Close()
		_ = outWrite.Close()
		_ = errRead.Close()
		_ = errWrite.Close()
		return nil, err
	}
	w := &ownedWorkload{anchor: anchor, collected: make(chan error, 2), statusDone: make(chan struct{}), reads: []*os.File{outRead, errRead}, publish: publish, result: Exit{Code: -1, Error: "root command status unavailable", CollectionFailure: "root exit status unavailable"}}
	var logMu sync.Mutex
	for i, read := range w.reads {
		stream := []string{"stdout", "stderr"}[i]
		go func() {
			_, err := io.Copy(&logWriter{spec: spec, stream: stream, mu: &logMu}, read)
			_ = read.Close()
			w.collected <- err
		}()
	}
	// Both children remain in this guardian's session. Only the root joins the
	// anchor's distinct group; the guardian can reap root without releasing anchor.
	root := exec.Command(spec.Executable, spec.Args...)
	configureRoot(root, anchor.Process.Pid)
	root.Dir = spec.Directory
	root.Env = spec.Environment
	root.Stdout = outWrite
	root.Stderr = errWrite
	startErr := root.Start()
	_ = outWrite.Close()
	_ = errWrite.Close()
	publish(Exit{AnchorPID: anchor.Process.Pid})
	go func() {
		defer close(w.statusDone)
		result := Exit{}
		if startErr != nil {
			result.Code = -1
			result.Error = "native command could not start"
		} else {
			err := root.Wait()
			if root.ProcessState != nil {
				result.Known = true
				result.Code = root.ProcessState.ExitCode()
			}
			if err != nil {
				result.Error = "native command failed"
			}
		}
		w.mu.Lock()
		w.result = result
		w.mu.Unlock()
		publish(result)
	}()
	return w, nil
}

func (w *ownedWorkload) stop(grace time.Duration) error {
	if w.released {
		return errors.New("anchor already released; no further signaling permitted")
	}
	pgid := w.anchor.Process.Pid
	scan := w.scan
	if scan == nil {
		scan = groupMembers
	}
	signal := func(sig syscall.Signal) error {
		if w.beforeSignal != nil {
			w.beforeSignal()
		}
		return signalGroup(pgid, sig)
	}
	// No Wait goroutine exists: this direct child's PID pins the group throughout
	// these signals and scans. No individual observed member is ever signaled.
	if err := signal(syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) && !errors.Is(err, syscall.EPERM) {
		return err
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		members, err := scan(pgid)
		if err != nil {
			return err
		}
		if onlyAnchor(members, pgid) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	verify := time.Now().Add(2 * time.Second)
	for {
		members, err := scan(pgid)
		if err != nil {
			return err
		}
		dead, err := anchorExited(pgid)
		if err != nil {
			return err
		}
		if onlyAnchor(members, pgid) && dead {
			break
		}
		signalErr := signal(syscall.SIGKILL)
		// Darwin may return EPERM when the pinned group contains only zombies.
		// This never counts as cleanup proof: require the zombie anchor alone below.
		if signalErr != nil && !errors.Is(signalErr, syscall.ESRCH) && !errors.Is(signalErr, syscall.EPERM) {
			return fmt.Errorf("pinned group escalation: %w", signalErr)
		}
		if time.Now().After(verify) {
			return fmt.Errorf("owned descendants remain after escalation; anchor retained: %v", signalErr)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The last signal is above. Reaping can only happen here, after no descendant
	// remains; this function must never signal or retry after entering this phase.
	w.released = true
	joined := make(chan struct{})
	go func() { _ = w.anchor.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(time.Second):
		return errors.New("anchor join unavailable")
	}
	select {
	case <-w.statusDone:
	case <-time.After(time.Second):
		return errors.New("root status collector did not join")
	}
	result := func() Exit { w.mu.Lock(); defer w.mu.Unlock(); return w.result }()
	for range 2 {
		select {
		case err := <-w.collected:
			if err != nil {
				result.CollectionFailure = "native output collection failed"
			}
		case <-time.After(time.Second):
			for _, r := range w.reads {
				_ = r.Close()
			}
			return errors.New("native output collectors did not join")
		}
	}
	w.publish(result)
	members, err := scan(pgid)
	if err != nil {
		return err
	}
	if len(members) != 0 {
		return errors.New("group absence not verified after anchor join")
	}
	// Enumeration can miss a fork during a scan. Kernel group existence is the
	// final authority after release; signal 0 observes only and never actuates.
	if err := signalGroup(pgid, syscall.Signal(0)); !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kernel group absence not verified after anchor join: %v", err)
	}
	return nil
}
func onlyAnchor(members []int, pid int) bool { return len(members) == 1 && members[0] == pid }

// Anchor pins a distinct process group in its parent's session until cleanup.
func Anchor() error {
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGTERM, os.Interrupt)
	defer signal.Stop(signals)
	for range signals {
	}
	return nil
}

// CrashForTest asks an explicitly fixture-enabled private guardian to crash itself.
// It verifies full receiver authority and original exit, never signals an observed
// process, and never claims workload cleanup. Callers retain separate fixture
// cleanup authority before invoking this diagnostic-only internal operation.
func CrashForTest(id Identity) error {
	if !supported() {
		return errors.New("native fixture self-fault unsupported")
	}
	if id.PID <= 1 || id.Birth == "" || id.Control == "" || len(id.Token) != 64 {
		return errors.New("fixture guardian authority missing")
	}
	conn, err := net.DialTimeout("unix", id.Control, time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if err := json.NewEncoder(conn).Encode(cleanupRequest{Identity: id, Crash: true}); err != nil {
		return err
	}
	var proof cleanupProof
	if err := json.NewDecoder(conn).Decode(&proof); err != nil {
		return err
	}
	if proof.Identity != id || proof.Error != "" {
		return errors.New("fixture self-fault rejected: " + proof.Error)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		actual, err := identify(id.PID)
		if errors.Is(err, syscall.ESRCH) || os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if actual.Birth != id.Birth {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("fixture guardian crash exit unverified")
}
