package main

import (
	"os"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

func installPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []uint{unix.TIOCPTYGRANT, unix.TIOCPTYUNLK} {
		if err := unix.IoctlSetInt(int(master.Fd()), request, 0); err != nil {
			_ = master.Close()
			t.Fatal(err)
		}
	}
	var name [128]byte
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0])))
	if errno != 0 {
		_ = master.Close()
		t.Fatal(errno)
	}
	end := 0
	for end < len(name) && name[end] != 0 {
		end++
	}
	slave, err := os.OpenFile(string(name[:end]), os.O_RDWR, 0)
	if err != nil {
		_ = master.Close()
		t.Fatal(err)
	}
	return master, slave
}
