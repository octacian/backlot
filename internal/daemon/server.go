package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gofrs/flock"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/localipc"
	"github.com/octacian/backlot/internal/manifest"
)

// Options selects private state placement and the bounded disposable client lease.
type Options struct {
	Directory     string
	LeaseDuration time.Duration
}

// DefaultLeaseDuration is the disposable disconnect grace period.
const DefaultLeaseDuration = 30 * time.Second

// Serve owns the socket, database and lease sweeper until shutdown or cancellation.
// Only the daemon opens state.db; it owns all accepted native execution.
func Serve(ctx context.Context, options Options) (resultErr error) {
	if options.LeaseDuration == 0 {
		options.LeaseDuration = DefaultLeaseDuration
	}
	if options.LeaseDuration < 100*time.Millisecond || options.LeaseDuration > 24*time.Hour {
		return problem("invalid_lease", "lease duration must be between 100ms and 24h")
	}
	if err := localipc.Directory(options.Directory, true); err != nil {
		return err
	}
	lockPath := filepath.Join(options.Directory, "daemon.lock")
	if _, err := os.Lstat(lockPath); err == nil {
		if err := localipc.File(lockPath, false); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return problem("permissions", "cannot inspect daemon lock")
	}
	lock := flock.New(lockPath, flock.SetPermissions(0600))
	closeOwnership := true
	defer func() {
		if closeOwnership {
			resultErr = errors.Join(resultErr, lock.Close())
		}
	}()
	acquired, err := lock.TryLock()
	if err != nil {
		return problem("lock_unavailable", "cannot acquire daemon lock")
	}
	if !acquired {
		return problem("already_running", "a daemon already owns this state directory")
	}
	state, err := openStore(options.Directory)
	if err != nil {
		return err
	}
	defer func() {
		if closeOwnership {
			resultErr = errors.Join(resultErr, state.db.Close())
		}
	}()
	path := SocketPath(options.Directory)
	if staleInfo, err := os.Lstat(path); err == nil {
		if err := localipc.File(path, true); err != nil {
			return err
		}
		connection, dialErr := net.DialTimeout("unix", path, 200*time.Millisecond)
		if dialErr == nil {
			return errors.Join(problem("socket_in_use", "socket has a live listener; it was not removed"), connection.Close())
		}
		// Only connection-refused proves a stale Unix socket. Permission/timeout and
		// other uncertain failures never authorize unlinking a listener.
		if !connectionRefused(dialErr) {
			return problem("socket_uncertain", "cannot prove socket stale; preserve it and diagnose permissions/connectivity")
		}
		current, err := os.Lstat(path)
		if err != nil || !os.SameFile(staleInfo, current) {
			return problem("socket_uncertain", "socket changed during stale check; preserve it for diagnosis")
		}
		if err := os.Remove(path); err != nil {
			return problem("permissions", "cannot remove stale owned socket")
		}
	} else if !os.IsNotExist(err) {
		return problem("permissions", "cannot inspect daemon socket")
	}
	if err := state.recoverRuntime(); err != nil {
		return err
	}
	if err := state.reconcile("daemon recovery interrupted preparation", false); err != nil {
		return err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return problem("socket_unavailable", "cannot bind local socket; check path length and permissions")
	}
	listener.SetUnlinkOnClose(false)
	defer func() {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	defer func() {
		current, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return
		}
		if err != nil {
			resultErr = errors.Join(resultErr, err)
			return
		}
		if os.SameFile(info, current) {
			resultErr = errors.Join(resultErr, os.Remove(path))
		}
	}()
	if err := os.Chmod(path, 0600); err != nil {
		return problem("permissions", "cannot protect daemon socket")
	}
	svc := &service{store: state, lease: options.LeaseDuration, directory: options.Directory}
	stopping := make(chan struct{})
	var once sync.Once
	status := v1.DaemonStatusResponse{APIVersion: v1.Version, StateVersion: v1.StateVersion, Status: "running", PID: os.Getpid(), LeaseDuration: options.LeaseDuration.String()}
	server := &http.Server{Handler: handler(svc, status, func() { once.Do(func() { close(stopping) }) }), ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 0, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	ticker := time.NewTicker(min(options.LeaseDuration/4, time.Second))
	defer ticker.Stop()
	var serveErr error
	servedRead := false
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-stopping:
			break loop
		case serveErr = <-served:
			servedRead = true
			break loop
		case <-ticker.C:
			err := svc.expire()
			if err != nil {
				serveErr = err
				break loop
			}
		}
	}
	stopErr := svc.stop()
	if !svc.ownersJoined() {
		// A failed finite join must not close storage underneath its owner. Retain
		// the database and lock until that owner finishes; shutdown remains an error.
		closeOwnership = false
		stopErr = errors.Join(stopErr, problem("cleanup_failed", "execution owner remains unjoined; state ownership retained"))
		go func() { svc.waitOwners(); _ = state.db.Close(); _ = lock.Close() }()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, server.Close())
	}
	if !servedRead {
		transportErr := <-served
		if !errors.Is(transportErr, http.ErrServerClosed) {
			serveErr = errors.Join(serveErr, transportErr)
		}
	} else if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(serveErr, stopErr, shutdownErr)
}

func handler(s *service, status v1.DaemonStatusResponse, stop func()) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Backlot-API-Version", v1.Version)
		if r.Header.Get("X-Backlot-API-Version") != v1.Version {
			writeError(w, problem("api_version", "unsupported API version; use a v1 client"))
			return
		}
		if r.URL.RawQuery != "" {
			writeError(w, problem("invalid_request", "query parameters are not supported"))
			return
		}
		if r.URL.Path == "/v1/status" && r.Method == http.MethodGet {
			writeJSON(w, status)
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, problem("invalid_request", "use POST for this operation or GET /v1/status"))
			return
		}
		switch r.URL.Path {
		case "/v1/run", "/v1/restart":
			var request v1.RunRequest
			if !decode(w, r, &request) || !compatible(w, request.APIVersion) {
				return
			}
			if r.URL.Path == "/v1/restart" && !validID(request.InstanceID) {
				writeError(w, problem("invalid_request", "restart requires recorded instance_id"))
				return
			}
			if r.URL.Path == "/v1/run" && request.InstanceID != "" {
				writeError(w, problem("invalid_request", "run does not accept instance_id"))
				return
			}
			response, err := s.run(r.Context(), request)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, response)
		case "/v1/runtime/stop":
			var request v1.InstanceRequest
			if !decode(w, r, &request) || !compatible(w, request.APIVersion) {
				return
			}
			if !validID(request.InstanceID) {
				writeError(w, problem("invalid_request", "recorded instance_id required"))
				return
			}
			instance, err := s.stopRuntime(request.InstanceID)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, v1.InstanceResponse{APIVersion: v1.Version, Instance: instance})
		case "/v1/logs":
			var request v1.LogsRequest
			if !decode(w, r, &request) || !compatible(w, request.APIVersion) {
				return
			}
			response, err := s.logs(request)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, response)
		case "/v1/prepare":
			var request v1.PrepareRequest
			if !decode(w, r, &request) || !compatible(w, request.APIVersion) {
				return
			}
			response, err := s.prepare(r.Context(), request)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, response)
		case "/v1/inspect", "/v1/cancel":
			var request v1.InstanceRequest
			if !decode(w, r, &request) || !compatible(w, request.APIVersion) {
				return
			}
			if !validID(request.InstanceID) {
				writeError(w, problem("invalid_request", "instance_id must be a recorded 64-character hex ID"))
				return
			}

			var instance v1.Instance
			var err error
			if r.URL.Path == "/v1/cancel" {
				instance, err = s.cancelRuntime(request.InstanceID)
			} else {
				instance, err = s.store.inspect(request.InstanceID)
			}
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, v1.InstanceResponse{APIVersion: v1.Version, Instance: instance})
		case "/v1/renew":
			var request v1.LeaseRequest
			if !decode(w, r, &request) || !compatible(w, request.APIVersion) {
				return
			}
			if !validID(request.InstanceID) || !validID(request.LeaseToken) {
				writeError(w, problem("invalid_request", "instance_id and lease_token must be 64-character hex IDs"))
				return
			}
			s.mu.Lock()
			instance, err := s.store.renew(request.InstanceID, request.LeaseToken, s.lease)
			s.mu.Unlock()
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, v1.InstanceResponse{APIVersion: v1.Version, Instance: instance})
		case "/v1/stop":
			var request v1.ControlRequest
			if !decode(w, r, &request) || !compatible(w, request.APIVersion) {
				return
			}
			if err := s.stop(); err != nil {
				writeError(w, err)
				stop()
				return
			}
			response := status
			response.Status = "stopping"
			writeJSON(w, response)
			stop()
		default:
			writeError(w, problem("not_found", "unknown v1 endpoint"))
		}
	})
}
func compatible(w http.ResponseWriter, version string) bool {
	if version != v1.Version {
		writeError(w, problem("api_version", "unsupported API version; use a v1 request"))
		return false
	}
	return true
}
func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Header.Get("Content-Type") != "application/json" {
		writeError(w, problem("invalid_request", "Content-Type must be application/json"))
		return false
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, manifest.MaxBytes+1))
	if err != nil || len(data) > manifest.MaxBytes {
		writeError(w, problem("invalid_request", "request body unavailable or exceeds 1 MiB limit"))
		return false
	}
	if err := manifest.Decode(data, ".json", target); err != nil {
		writeError(w, err)
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, value any) {
	// Encoding named contracts cannot expose private persistence/snapshot models.
	if err := json.NewEncoder(w).Encode(value); err != nil {
		return
	}
}
func writeError(w http.ResponseWriter, err error) {
	detail := &v1.PlanError{Code: "internal", Message: "daemon state operation failed; preserve state for diagnosis"}
	var typed *v1.PlanError
	if errors.As(err, &typed) {
		detail = typed
	}
	status := http.StatusBadRequest
	switch detail.Code {
	case "internal", "state_corrupt", "state_unavailable":
		status = http.StatusInternalServerError
	case "not_found":
		status = http.StatusNotFound
	case "conflict", "checkout_moved", "checkout_replaced", "checkout_changed", "lease_expired", "shutting_down":
		status = http.StatusConflict
	case "api_version":
		status = http.StatusUpgradeRequired
	case "invalid_lease", "permissions":
		status = http.StatusForbidden
	}
	w.WriteHeader(status)
	writeJSON(w, v1.ErrorResponse{APIVersion: v1.Version, Error: *detail})
}
