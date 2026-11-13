package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

// shutdownOperation belongs to the server, outside the mutation set it joins.
// Like bulk stop, compact buffering separates cleanup from observer backpressure.
type shutdownOperation struct {
	mu       sync.Mutex
	records  []v1.InfraStopRecord
	changed  chan struct{}
	done     chan struct{}
	err      error
	accepted v1.InfraStopRecord
}

func (o *shutdownOperation) emit(r v1.InfraStopRecord) {
	o.mu.Lock()
	defer o.mu.Unlock()
	r.APIVersion, r.DaemonGeneration, r.OperationID = o.accepted.APIVersion, o.accepted.DaemonGeneration, o.accepted.OperationID
	o.records = append(o.records, r)
	close(o.changed)
	o.changed = make(chan struct{})
}
func (o *shutdownOperation) next(ctx context.Context, index int) (v1.InfraStopRecord, error) {
	for {
		o.mu.Lock()
		if index < len(o.records) {
			r := o.records[index]
			o.mu.Unlock()
			return r, nil
		}
		changed := o.changed
		o.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return v1.InfraStopRecord{}, ctx.Err()
		}
	}
}
func shutdownError(err error) *v1.PlanError {
	if err == nil {
		return nil
	}
	// Keep progress compact; detailed ownership and collection diagnostics remain in inspect.
	code := "cleanup_failed"
	var detail *v1.PlanError
	if errors.As(err, &detail) {
		code = detail.Code
	}
	return &v1.PlanError{Code: code, Message: "cleanup incomplete; inspect activity and recorded workloads in the same state directory before explicit recovery"}
}
func (o *shutdownOperation) phase(name string, run func() error) error {
	o.emit(v1.InfraStopRecord{Type: "phase", Phase: name, State: "running"})
	err := run()
	state := "succeeded"
	if err != nil {
		state = "failed"
	}
	o.emit(v1.InfraStopRecord{Type: "phase", Phase: name, State: state, Error: shutdownError(err)})
	return err
}
func (s *service) startShutdown() *shutdownOperation {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown != nil {
		return s.shutdown
	}
	s.closing = true
	o := &shutdownOperation{changed: make(chan struct{}), done: make(chan struct{}), accepted: v1.InfraStopRecord{APIVersion: v1.Version, Type: "accepted", DaemonGeneration: s.generation, OperationID: newID()}}
	s.shutdown = o
	o.emit(o.accepted)
	go func() {
		o.err = s.shutdownCleanup(o)
		if !s.ownersJoined() {
			o.err = errors.Join(o.err, problem("cleanup_failed", "owner remains unjoined; serving process retains storage and state ownership"))
		}
		o.emit(v1.InfraStopRecord{Type: "cleanup", Error: shutdownError(o.err)})
		close(o.done)
	}()
	return o
}
func (s *service) stop() error {
	o := s.startShutdown()
	<-o.done
	return o.err
}
func (s *service) writeShutdown(w http.ResponseWriter, r *http.Request, o *shutdownOperation) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	controller := http.NewResponseController(w)
	for index := 0; ; index++ {
		event, err := o.next(r.Context(), index)
		if err != nil {
			return
		}
		if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return
		}
		if json.NewEncoder(w).Encode(event) != nil || controller.Flush() != nil {
			return
		}
		if err := controller.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return
		}
		if event.Type == "cleanup" {
			return
		}
	}
}

func (s *service) shutdownTarget(id string, err error) {
	s.mu.Lock()
	o := s.shutdown
	s.mu.Unlock()
	if o == nil {
		return
	}
	outcome := "stopped"
	if err != nil {
		outcome = "failed"
	}
	o.emit(v1.InfraStopRecord{Type: "target", Target: &v1.InfraTarget{ID: id, Outcome: outcome, Error: shutdownError(err)}})
}
