package native

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// legacyIdentity is the exact merged-main6/8 tuple, including field order for
// persisted managed claim hashes. It authorizes strict cleanup only.
type legacyIdentity struct {
	PID     int    `json:"pid"`
	Birth   string `json:"birth"`
	Control string `json:"control"`
	Token   string `json:"token"`
	Boot    string `json:"boot,omitempty"`
}

func legacyTuple(id Identity) legacyIdentity {
	return legacyIdentity{id.PID, id.Birth, id.Control, id.Token, id.Boot}
}

type legacyCleanupRequest struct {
	Identity    legacyIdentity
	Grace       time.Duration
	Crash       bool
	AnchorCrash bool
}
type legacyCleanupProof struct {
	Identity legacyIdentity
	Error    string
}

func stopLegacy(id Identity, grace time.Duration) (collected bool, err error) {
	if id.PID <= 1 || id.Birth == "" || id.Control == "" || len(id.Token) != 64 {
		return false, errors.New("native control authority missing; preserve uncertain processes")
	}
	if grace < 0 {
		return false, errors.New("invalid cleanup grace")
	}
	if id.Boot != "" {
		previous, err := PreviousBoot(id.Boot)
		if err != nil {
			return false, errors.New("native boot identity unavailable or invalid; preserve uncertain processes")
		}
		if previous {
			// Reboot proves process absence, never successful output collection.
			return false, nil
		}
	}
	var proof legacyCleanupProof
	connection, err := net.DialTimeout("unix", id.Control, time.Second)
	if err == nil {
		_ = connection.SetDeadline(time.Now().Add(grace).Add(8 * time.Second))
		err = json.NewEncoder(connection).Encode(legacyCleanupRequest{Identity: legacyTuple(id), Grace: grace})
		if err == nil {
			err = decodeLegacyProof(connection, &proof)
		}
		_ = connection.Close()
	}
	if err != nil {
		data, readErr := os.ReadFile(filepath.Join(filepath.Dir(id.Control), "receipt.json"))
		if os.IsNotExist(readErr) {
			return false, errors.New("native control authority and receipt unavailable; preserve uncertain processes or explicitly stop --force")
		}
		if readErr != nil || decodeLegacyProof(bytes.NewReader(data), &proof) != nil {
			return false, errors.New("native control authority unavailable; preserve uncertain processes")
		}
	}
	if proof.Identity != legacyTuple(id) {
		return false, errors.New("native cleanup response identity mismatch; preserve uncertain processes")
	}
	if proof.Error != "" {
		return false, errors.New(proof.Error)
	}
	// A receipt is emitted after anchor and collectors join, but success also requires
	// the outside authority's original process to have exited. Never signal this PID.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		actual, err := identify(id.PID)
		if errors.Is(err, syscall.ESRCH) || os.IsNotExist(err) {
			return true, nil
		}
		if err != nil {
			return false, fmt.Errorf("guardian exit verification unavailable: %w", err)
		}
		if actual.Birth != id.Birth {
			return true, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false, errors.New("guardian exit not verified after cleanup proof")
}

func decodeLegacyProof(reader io.Reader, proof *legacyCleanupProof) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(proof); err != nil {
		return err
	}
	if proof.Identity.PID <= 1 || proof.Identity.Birth == "" || proof.Identity.Control == "" || len(proof.Identity.Token) != 64 {
		return errors.New("invalid legacy cleanup receipt identity")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("invalid legacy cleanup receipt framing")
	}
	return nil
}
