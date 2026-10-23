package procidentity

import (
	"fmt"
	"sync"
	"syscall"

	"github.com/ebitengine/purego"
)

// All bindings are pointer/integer-only public LP64 system ABIs. task_info's
// TASK_AUDIT_TOKEN flavor is 15, its count is eight natural_t words (32 bytes).
type darwinAPI struct {
	self       uint32
	taskName   func(uint32, int32, *uint32) int32
	taskInfo   func(uint32, uint32, *[8]uint32, *uint32) int32
	deallocate func(uint32, uint32) int32
	send       func(*[8]uint32, int32) int32
}

var loadDarwin = sync.OnceValues(func() (*darwinAPI, error) {
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, fmt.Errorf("load identity system library: %w", err)
	}
	// Keep the successful library loaded for process lifetime: registered function
	// pointers remain callable. On any symbol failure no pointers escape.
	api := &darwinAPI{}
	bindings := []struct {
		name   string
		target any
	}{
		{"task_name_for_pid", &api.taskName}, {"task_info", &api.taskInfo},
		{"mach_port_deallocate", &api.deallocate}, {"proc_signal_with_audittoken", &api.send},
	}
	for _, binding := range bindings {
		addr, err := purego.Dlsym(lib, binding.name)
		if err != nil {
			_ = purego.Dlclose(lib)
			return nil, fmt.Errorf("resolve %s: %w", binding.name, err)
		}
		purego.RegisterFunc(binding.target, addr)
	}
	addr, err := purego.Dlsym(lib, "mach_task_self")
	if err != nil {
		_ = purego.Dlclose(lib)
		return nil, fmt.Errorf("resolve mach_task_self: %w", err)
	}
	// The public mach_task_self trap avoids dereferencing a Dlsym global.
	var taskSelf func() uint32
	purego.RegisterFunc(&taskSelf, addr)
	api.self = taskSelf()
	return api, nil
})

type platformIdentity struct {
	api   *darwinAPI
	token [8]uint32
}

func acquire(pid int) (platformIdentity, error) {
	api, err := loadDarwin()
	if err != nil {
		return platformIdentity{}, err
	}
	token, err := api.token(pid)
	return platformIdentity{api: api, token: token}, err
}
func (a *darwinAPI) token(pid int) (token [8]uint32, err error) {
	var port uint32
	if kr := a.taskName(a.self, int32(pid), &port); kr != 0 {
		return token, fmt.Errorf("task_name_for_pid: kern_return %d", kr)
	}
	defer func() {
		if kr := a.deallocate(a.self, port); kr != 0 {
			err = fmt.Errorf("mach_port_deallocate: kern_return %d (prior error: %v)", kr, err)
		}
	}()
	count := uint32(8)
	if kr := a.taskInfo(port, 15, &token, &count); kr != 0 {
		return token, fmt.Errorf("TASK_AUDIT_TOKEN: kern_return %d", kr)
	}
	if count != 8 || token[5] != uint32(pid) {
		return token, fmt.Errorf("incompatible TASK_AUDIT_TOKEN ABI")
	}
	return token, nil
}
func (p platformIdentity) validate(pid int) error {
	token, err := p.api.token(pid)
	if err != nil {
		return err
	}
	if token != p.token {
		return ErrChanged
	}
	return nil
}
func (p platformIdentity) signal(sig syscall.Signal) error {
	token := p.token
	if code := p.api.send(&token, int32(sig)); code != 0 {
		return fmt.Errorf("proc_signal_with_audittoken: %w", syscall.Errno(code))
	}
	return nil
}
func (p platformIdentity) close() error { return nil } // No retained Mach rights.
