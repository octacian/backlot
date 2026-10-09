package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
)

func TestDockerObservationFailureDoesNotInventExit(t *testing.T) {
	for _, collection := range []bool{false, true} {
		t.Run(map[bool]string{false: "inspection", true: "collection"}[collection], func(t *testing.T) {
			var fail atomic.Bool
			fail.Store(!collection)
			var inspections atomic.Int32
			token := newID()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/stop") {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				inspections.Add(1)
				if fail.Load() {
					http.Error(w, "injected inspection failure", http.StatusInternalServerError)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"Id": "owned", "Config": map[string]any{"Labels": map[string]string{"io.backlot.owner": token}}, "State": map[string]any{"Running": false, "ExitCode": 17}})
			}))
			defer server.Close()
			engine, err := client.New(client.WithHost(server.URL), client.WithAPIVersion("1.53"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = engine.Close() }()
			done := make(chan struct{})
			close(done)
			work := &dockerWork{engine: &dockerEngine{engine}, resource: ownedResource{Kind: "container", ID: "owned", Token: token}, logDone: done}
			if collection {
				work.logErr = errors.New("injected collector transport loss")
			}
			first := work.Result()
			if first == nil || first.Known {
				t.Fatal("observation failure invented original exit")
			}
			if collection && first.CollectionFailure == "" {
				t.Fatal("collection error missing")
			}
			fail.Store(false)
			if err := work.Stop(time.Millisecond); err != nil {
				t.Fatal("verified stop recovery", err)
			}
			recovered := work.Result()
			if recovered == nil || !recovered.Known || recovered.Code != 17 {
				t.Fatal("actual nonzero exit lost", recovered)
			}
			if collection && recovered.CollectionFailure == "" {
				t.Fatal("recovery cleared collection failure")
			}
			if inspections.Load() < 2 {
				t.Fatal("verified stop did not inspect recovered provider")
			}
		})
	}
}

func TestPublicationCancellationAndDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := awaitPublication(ctx, "https://example.invalid", "0s", nil); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation cause lost", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	err := awaitPublication(context.Background(), server.URL, "20ms", nil)
	var detail *v1.PlanError
	if !errors.As(err, &detail) || detail.Code != "publication_unreachable" || errors.Is(err, context.Canceled) {
		t.Fatal("genuine readiness deadline misclassified", err)
	}
}
