//go:build !windows

package daemon

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

func TestDockerStopDeadline(t *testing.T) {
	for _, timeout := range []*int{nil, new(-1), new(10)} {
		name := "default"
		if timeout != nil {
			name = strconv.Itoa(*timeout)
		}
		t.Run(name, func(t *testing.T) {
			requested := make(chan string, 1)
			engine := forceEngine(t, func(_ http.ResponseWriter, r *http.Request) {
				requested <- r.URL.Query().Get("t")
				<-r.Context().Done()
			})
			if timeout == nil || *timeout < 0 {
				if _, err := engine.ContainerStop(context.Background(), "owned", client.ContainerStopOptions{Timeout: timeout}); err == nil {
					t.Fatal("unbounded stop accepted")
				}
				select {
				case <-requested:
					t.Fatal("unbounded stop reached engine")
				default:
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := engine.ContainerStop(ctx, "owned", client.ContainerStopOptions{Timeout: timeout})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("stalled stop lost caller deadline", err)
			}
			want := ""
			if timeout != nil {
				want = strconv.Itoa(*timeout)
			}
			select {
			case got := <-requested:
				if got != want {
					t.Fatal("Docker grace changed", got, want)
				}
			default:
				t.Fatal("stop did not reach engine")
			}
		})
	}
	t.Run("cancelled", func(t *testing.T) {
		reached := make(chan struct{})
		engine := forceEngine(t, func(_ http.ResponseWriter, r *http.Request) { close(reached); <-r.Context().Done() })
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, err := engine.ContainerStop(ctx, "owned", client.ContainerStopOptions{Timeout: new(10)})
			done <- err
		}()
		select {
		case <-reached:
		case <-time.After(5 * time.Second):
			t.Fatal("stop did not reach engine")
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal("stop lost cancellation", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("cancelled stop did not join")
		}
	})
}

func TestDockerRequestBounds(t *testing.T) {
	for _, stop := range []bool{false, true} {
		name := "ordinary-headers"
		if stop {
			name = "stop-without-caller-deadline"
		}
		t.Run(name, func(t *testing.T) {
			engine := forceEngine(t, func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
			done := make(chan error, 1)
			go func() {
				var err error
				if stop {
					_, err = engine.ContainerStop(context.Background(), "owned", client.ContainerStopOptions{Timeout: new(0)})
				} else {
					_, err = engine.ContainerInspect(context.Background(), "owned", client.ContainerInspectOptions{})
				}
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("stalled engine returned success")
				}
				if stop && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("stop request deadline lost", err)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("stalled engine exceeded request budget")
			}
		})
	}
}
