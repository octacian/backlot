package native

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
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

// absentSession proves resource absence, not output collection. A guardian is a
// session leader and all supported descendants stay in that session. Once the
// original guardian has exited, an empty session cannot acquire new descendants.
// No observation here authorizes a signal, including when PIDs have been reused.
func absentSession(id Identity) error {
	actual, err := identify(id.PID)
	if err == nil && actual.Birth == id.Birth {
		return errors.New("original native guardian remains; restore its control authority")
	}
	if err != nil && !errors.Is(err, syscall.ESRCH) && !os.IsNotExist(err) {
		return fmt.Errorf("native guardian observation unavailable: %w", err)
	}
	// Ordinary system process churn is not an operator repair task. Retry under
	// a short finite budget, while requiring a complete proof on every attempt.
	deadline := time.Now().Add(time.Second)
	for {
		processes, snapshotErr := processSnapshot()
		err = snapshotErr
		if err == nil {
			err = verifyAbsentSession(id.PID, processes, sessionID, identify)
		}
		if err == nil {
			var after []Identity
			after, err = processSnapshot()
			if err == nil && !sameProcesses(processes, after) {
				err = errors.New("native process snapshot changed; retry cleanup")
			}
		}
		if err == nil {
			return nil
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("native session absence unverified: %w", err)
}

// Bracket session observations with unchanged process membership and birth
// identities. An omitted fork or a reused PID during enumeration forces a retry.
func sameProcesses(before, after []Identity) bool {
	if len(before) == 0 || len(before) != len(after) {
		return false
	}
	known := make(map[int]string, len(before))
	for _, id := range before {
		known[id.PID] = id.Birth
	}
	if len(known) != len(before) {
		return false
	}
	for _, id := range after {
		birth, found := known[id.PID]
		if !found || birth != id.Birth {
			return false
		}
		delete(known, id.PID)
	}
	return true
}

func verifyAbsentSession(sid int, processes []Identity, session func(int) (int, error), observe func(int) (Identity, error)) error {
	for _, original := range processes {
		currentSession, err := session(original.PID)
		if err != nil {
			return errors.New("native session observation changed or unavailable; retry cleanup")
		}
		current, err := observe(original.PID)
		// A disappearing/reused parent may have forked after the snapshot. Never
		// skip it: its child could be missing from the enumerated process list.
		if err != nil || current.Birth != original.Birth {
			return errors.New("native process snapshot changed; retry cleanup")
		}
		if currentSession == sid {
			return errors.New("native session members remain; restore authenticated cleanup")
		}
	}
	return nil
}
