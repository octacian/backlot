//go:build !darwin && !linux

package native

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func supported() bool       { return false }
func configure(_ *exec.Cmd) {}
func identify(_ int) (Identity, error) {
	return Identity{}, errors.New("native execution unsupported on this platform")
}
func groupMembers(_ int) ([]int, error) {
	return nil, errors.New("native execution unsupported on this platform")
}
func signalGroup(_ int, _ syscall.Signal) error {
	return errors.New("native execution unsupported on this platform")
}

func anchorExited(_ int) (bool, error) { return false, errors.New("native execution unsupported") }

func configureAnchor(_ *exec.Cmd)      {}
func configureRoot(_ *exec.Cmd, _ int) {}

func controlInput() (*os.File, error) { return nil, errors.New("native execution unsupported") }
