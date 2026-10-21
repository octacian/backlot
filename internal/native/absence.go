package native

import (
	"encoding/hex"
	"errors"
)

func validBootIdentity(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(value[:8] + value[9:13] + value[14:18] + value[19:23] + value[24:])
	return err == nil
}

// CurrentBoot returns the kernel's unique boot-session identity. It is private
// ownership metadata, not a wall-clock timestamp or a cleanup capability.
func CurrentBoot() (string, error) {
	boot, err := bootIdentity()
	if err != nil || !validBootIdentity(boot) {
		return "", errors.New("native boot identity unavailable or invalid")
	}
	return boot, nil
}

// PreviousBoot reports whether recorded names a different kernel boot session.
// Missing, invalid or unreadable identities never establish resource absence.
func PreviousBoot(recorded string) (bool, error) {
	boot, err := CurrentBoot()
	if err != nil || !validBootIdentity(recorded) {
		return false, errors.New("native boot identity unavailable or invalid")
	}
	return boot != recorded, nil
}
