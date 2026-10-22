package procidentity

import (
	"errors"
	"syscall"
	"testing"

	"github.com/ebitengine/purego"
)

func TestDarwinAlteredTokenRejectsObservationAndSignal(t *testing.T) {
	child := startChild(t)
	sentinel := startChild(t)
	h := mustAcquire(t, child.cmd.Process.Pid)
	h.identity.token[7] ^= 1
	if _, err := h.Session(); !errors.Is(err, ErrChanged) {
		t.Fatalf("altered membership identity: %v", err)
	}
	if err := h.Signal(syscall.SIGTERM); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("altered signal identity: %v", err)
	}
	for _, c := range []*ownedChild{child, sentinel} {
		if err := mustAcquire(t, c.cmd.Process.Pid).Validate(); err != nil {
			t.Fatalf("preserved child: %v", err)
		}
	}
}

func TestDarwinTokenErrorsReleaseRights(t *testing.T) {
	for _, mode := range []string{"name", "info", "count", "pid", "release", "success"} {
		t.Run(mode, func(t *testing.T) {
			releases := 0
			api := &darwinAPI{self: 1,
				taskName: func(_ uint32, _ int32, port *uint32) int32 {
					if mode == "name" {
						return 5
					}
					*port = 2
					return 0
				},
				taskInfo: func(port, flavor uint32, token *[8]uint32, count *uint32) int32 {
					if port != 2 || flavor != 15 || *count != 8 {
						t.Fatal("token ABI/ordering")
					}
					token[5] = 123
					if mode == "info" {
						return 5
					}
					if mode == "count" {
						*count = 7
					}
					if mode == "pid" {
						token[5] = 124
					}
					return 0
				},
				deallocate: func(self, port uint32) int32 {
					if self != 1 || port != 2 {
						t.Fatal("release wrong right")
					}
					releases++
					if mode == "release" {
						return 5
					}
					return 0
				},
			}
			_, err := api.token(123)
			if (err == nil) != (mode == "success") {
				t.Fatalf("token error %v", err)
			}
			expected := 1
			if mode == "name" {
				expected = 0
			}
			if releases != expected {
				t.Fatalf("released %d rights, want %d", releases, expected)
			}
		})
	}
}

func TestDarwinMachRightReferencesBounded(t *testing.T) {
	child := startChild(t)
	api, err := loadDarwin()
	if err != nil {
		t.Fatal(err)
	}
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := purego.Dlclose(lib); err != nil {
			t.Error(err)
		}
	}()
	addr, err := purego.Dlsym(lib, "mach_port_get_refs")
	if err != nil {
		t.Fatal(err)
	}
	var refs func(uint32, uint32, uint32, *uint32) int32
	purego.RegisterFunc(&refs, addr)
	var port uint32
	if kr := api.taskName(api.self, int32(child.cmd.Process.Pid), &port); kr != 0 {
		t.Fatalf("task name %d", kr)
	}
	defer func() {
		if kr := api.deallocate(api.self, port); kr != 0 {
			t.Errorf("release %d", kr)
		}
	}()
	get := func() uint32 {
		var n uint32
		if kr := refs(api.self, port, 0, &n); kr != 0 {
			t.Fatalf("get send refs %d", kr)
		}
		return n
	}
	before := get()
	for range 256 {
		if _, err := api.token(child.cmd.Process.Pid); err != nil {
			t.Fatal(err)
		}
	}
	if after := get(); after != before {
		t.Fatalf("Mach send references grew: %d -> %d", before, after)
	}
}
