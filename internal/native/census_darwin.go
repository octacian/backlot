package native

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
)

const processTableAttempts = 3

// Darwin's public sysctl ABI uses a four-int kern.proc MIB and size_t byte
// lengths. Unlike SysctlKinfoProcSlice, this boundary never retries invisibly or
// allocates an unbounded kernel-supplied size. No asynchronous syscall owner.
var loadProcessCensus = sync.OnceValues(func() (func([4]int32, []unix.KinfoProc) (uintptr, error), error) {
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, err
	}
	var call func(*int32, uint32, unsafe.Pointer, *uintptr, unsafe.Pointer, uintptr) int32
	var errno func() *int32
	for _, binding := range []struct {
		name   string
		target any
	}{{"sysctl", &call}, {"__error", &errno}} {
		address, err := purego.Dlsym(lib, binding.name)
		if err != nil {
			_ = purego.Dlclose(lib)
			return nil, err
		}
		purego.RegisterFunc(binding.target, address)
	}
	// Keep the successful library loaded while its registered functions are used.
	return func(mib [4]int32, buffer []unix.KinfoProc) (uintptr, error) {
		n := uintptr(len(buffer)) * uintptr(unix.SizeofKinfoProc)
		var target unsafe.Pointer
		if len(buffer) > 0 {
			target = unsafe.Pointer(&buffer[0])
		}
		// errno belongs to the calling OS thread, not the goroutine.
		runtime.LockOSThread()
		result := call(&mib[0], uint32(len(mib)), target, &n, nil, 0)
		var err error
		if result != 0 {
			err = syscall.Errno(*errno())
		}
		runtime.UnlockOSThread()
		runtime.KeepAlive(buffer)
		return n, err
	}, nil
})

func darwinProcesses(ctx context.Context, selector, pid int32) ([]unix.KinfoProc, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	read, err := loadProcessCensus()
	if err != nil {
		return nil, err
	}
	// CTL_KERN / KERN_PROC (14) / KERN_PROC_ALL (0) or KERN_PROC_PID (1)
	// are the public Darwin sys/sysctl.h identifiers, not inferred process IDs.
	mib := [4]int32{unix.CTL_KERN, 14, selector, pid}
	return boundedProcessTable(ctx, func(buffer []unix.KinfoProc) (uintptr, error) { return read(mib, buffer) })
}

func boundedProcessTable(ctx context.Context, read func([]unix.KinfoProc) (uintptr, error)) ([]unix.KinfoProc, error) {
	size := uintptr(unix.SizeofKinfoProc)
	for range processTableAttempts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := read(nil)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if n%size != 0 || n/size > sessionCandidateLimit {
			return nil, errors.New("process census size budget exhausted or invalid")
		}
		if n == 0 {
			return nil, nil
		}
		buffer := make([]unix.KinfoProc, int(n/size))
		n, err = read(buffer)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, syscall.ENOMEM) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if n%size != 0 || n/size > uintptr(len(buffer)) {
			return nil, fmt.Errorf("process census returned invalid size %d", n)
		}
		return buffer[:n/size], nil
	}
	return nil, errors.New("process census growth retry budget exhausted")
}
