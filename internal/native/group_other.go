//go:build !darwin && !linux

package native

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func supported() bool               { return false }
func anchorOwnGroup() bool          { return false }
func bootIdentity() (string, error) { return "", errors.New("native boot identity unsupported") }
func processSnapshot() ([]Identity, error) {
	return nil, errors.New("native session observation unsupported")
}
func sessionID(_ int) (int, error) { return 0, errors.New("native session observation unsupported") }
func configure(_ *exec.Cmd)        {}
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
