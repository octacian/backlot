package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/octacian/backlot/internal/localipc"
	"github.com/octacian/backlot/internal/manifest"
	"github.com/octacian/backlot/internal/plan"
)

// Optional v6 journal metadata. Paths are derived, never accepted from storage.
// Identity is saved before writing; Ready is saved before any Engine create.
// An interrupted create without a saved identity is preserved, never adopted.
type diagnosticCACopy struct {
	Parent   string `json:"parent"`
	Identity string `json:"identity,omitempty"`
	SHA256   string `json:"sha256"`
	Ready    bool   `json:"ready,omitempty"`
}

func diagnosticCACopyName(key string) string { return "doctor-public-ca-" + key + ".crt" }
func diagnosticCACopyPath(directory, key string) string {
	return filepath.Join(directory, diagnosticCACopyName(key))
}

func validDiagnosticCAIdentity(value string) bool {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil || strconv.FormatUint(n, 10) != part {
			return false
		}
	}
	return true
}
func validDiagnosticCACopy(r diagnosticProbeRecord) bool {
	c := r.CA
	if c == nil {
		return true
	} // Existing v6 records own only Engine resources.
	digest, err := hex.DecodeString(c.SHA256)
	return err == nil && len(digest) == sha256.Size && hex.EncodeToString(digest) == c.SHA256 && validDiagnosticCAIdentity(c.Parent) &&
		(c.Identity == "" || validDiagnosticCAIdentity(c.Identity)) && (!c.Ready || c.Identity != "") &&
		(c.Ready || (r.Resource.ID == "" && r.Network.ID == ""))
}

func diagnosticCAParent(directory, expected string) (*os.Root, error) {
	if err := localipc.Directory(directory, false); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	info, err := root.Lstat(".")
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	identity, identityErr := fileIdentity(info)
	pathInfo, pathErr := os.Lstat(directory)
	if err != nil || identityErr != nil || pathErr != nil || !os.SameFile(info, pathInfo) || !exportParentOwned(info) || info.Mode() != os.ModeDir|0700 || (expected != "" && identity != expected) {
		_ = root.Close()
		return nil, errors.New("diagnostic public CA private parent changed; preserve and inspect")
	}
	return root, nil
}

func (s *store) planDiagnosticCACopy(snap plan.Snapshot) ([]byte, *diagnosticCACopy, error) {
	record, err := s.managed()
	if err != nil {
		return nil, nil, err
	}
	if snap.Caddy == nil || !record.Initialized || record.RootHash == "" || !reflect.DeepEqual(*snap.Caddy, record.Config) {
		return nil, nil, errors.New("diagnostic public CA has no matching managed pin")
	}
	_, _, storage := managedPaths(s.directory)
	// Bound the existing managed key-pair parser's inputs too, before invoking it.
	// This is specific to diagnostic export; private-state validators stay strict.
	for _, name := range []string{"root.crt", "root.key", "intermediate.crt", "intermediate.key"} {
		path := filepath.Join(storage, "pki", "authorities", "local", name)
		if err := localipc.File(path, false); err != nil {
			return nil, nil, err
		}
		info, err := os.Lstat(path)
		if err != nil || info.Size() > manifest.MaxBytes {
			return nil, nil, errors.New("diagnostic managed CA input unavailable or exceeds bound")
		}
	}
	if err := validateManagedFiles(s.directory, record); err != nil {
		return nil, nil, err
	}
	path := filepath.Join(storage, "pki", "authorities", "local", "root.crt")
	// The private-file guard rejects symlinks and wrong owner/mode;
	// bind the bounded read to the inspected file, then validate the public bytes.
	if err := localipc.File(path, false); err != nil {
		return nil, nil, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) || !exportLogOwned(opened) || opened.Size() > manifest.MaxBytes {
		return nil, nil, errors.New("diagnostic public CA source changed or exceeds bound")
	}
	data, err := io.ReadAll(io.LimitReader(f, manifest.MaxBytes+1))
	if err != nil || len(data) > manifest.MaxBytes {
		return nil, nil, errors.New("diagnostic public CA read failed or exceeds bound")
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 || !bytes.Equal(bytes.TrimSpace(data), bytes.TrimSpace(pem.EncodeToMemory(block))) {
		return nil, nil, errors.New("diagnostic public CA must contain only the pinned public certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !cert.IsCA || time.Now().Before(cert.NotBefore) || time.Now().After(cert.NotAfter) || cert.CheckSignatureFrom(cert) != nil {
		return nil, nil, errors.New("diagnostic public CA is invalid")
	}
	pin := sha256.Sum256(cert.Raw)
	if hex.EncodeToString(pin[:]) != record.RootHash {
		return nil, nil, errors.New("diagnostic public CA pin changed")
	}
	root, err := diagnosticCAParent(s.directory, "")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(".")
	if err != nil {
		return nil, nil, err
	}
	parent, err := fileIdentity(info)
	if err != nil {
		return nil, nil, err
	}
	data = pem.EncodeToMemory(block)
	digest := sha256.Sum256(data)
	return data, &diagnosticCACopy{Parent: parent, SHA256: hex.EncodeToString(digest[:])}, nil
}

// The caller has durably saved intent and installed cleanup before allocation.
func (s *store) allocateDiagnosticCACopy(key string, r *diagnosticProbeRecord, data []byte) error {
	if !validID(key) || r.CA == nil || r.CA.Identity != "" || r.CA.Ready || !validDiagnosticCACopy(*r) {
		return errors.New("invalid diagnostic public CA allocation intent")
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != r.CA.SHA256 {
		return errors.New("diagnostic public CA planned bytes changed")
	}
	root, err := diagnosticCAParent(s.directory, r.CA.Parent)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	f, err := root.OpenFile(diagnosticCACopyName(key), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !diagnosticCAOwned(info) || info.Mode() != 0600 {
		return errors.New("diagnostic public CA allocation ownership uncertain")
	}
	identity, err := fileIdentity(info)
	if err != nil {
		return err
	}
	// If this commit fails, in-memory authority still permits this live owner to
	// clean its exact allocation; crash recovery without the commit preserves it.
	r.CA.Identity = identity
	if err := s.saveDiagnosticProbe(key, *r); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Chmod(0444); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	parent, err := root.Open(".")
	if err != nil {
		return err
	}
	err = parent.Sync()
	err = errors.Join(err, parent.Close())
	if err != nil {
		return err
	}
	r.CA.Ready = true
	if err := s.verifyDiagnosticCACopy(key, r.CA); err != nil {
		return err
	}
	return s.saveDiagnosticProbe(key, *r)
}

func (s *store) verifyDiagnosticCACopy(key string, c *diagnosticCACopy) error {
	if c == nil {
		return nil
	}
	if !validID(key) || !c.Ready {
		return errors.New("diagnostic public CA copy is not ready")
	}
	root, err := diagnosticCAParent(s.directory, c.Parent)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return checkDiagnosticCACopy(root, key, c)
}

func checkDiagnosticCACopy(root *os.Root, key string, c *diagnosticCACopy) error {
	name := diagnosticCACopyName(key)
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	identity, identityErr := fileIdentity(info)
	if identityErr != nil || !diagnosticCAOwned(info) || identity != c.Identity || (c.Ready && info.Mode() != 0444) || (!c.Ready && info.Mode() != 0600 && info.Mode() != 0444) || info.Size() > manifest.MaxBytes {
		return errors.New("diagnostic public CA copy identity/metadata changed; preserve and inspect")
	}
	if !c.Ready {
		return nil
	} // Authenticated partial allocation, not planned bytes.
	f, err := root.Open(name)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return errors.New("diagnostic public CA copy changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(f, manifest.MaxBytes+1))
	if err != nil || len(data) > manifest.MaxBytes {
		return errors.New("diagnostic public CA copy exceeds bound or cannot be read")
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != c.SHA256 {
		return errors.New("diagnostic public CA copy bytes changed; preserve and inspect")
	}
	return nil
}

// All relevant Engine consumers must be proven absent before the copy is released.
func (s *store) cleanupDiagnosticProbe(ctx context.Context, d *dockerEngine, key string, r diagnosticProbeRecord) error {
	if !validID(key) || !validDiagnosticCACopy(r) {
		return errors.New("invalid diagnostic public CA cleanup journal")
	}
	if err := cleanupDiagnosticProbe(ctx, d, r); err != nil {
		return err
	}
	if r.CA == nil {
		return nil
	}
	root, err := diagnosticCAParent(s.directory, r.CA.Parent)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	name := diagnosticCACopyName(key)
	if _, err := root.Lstat(name); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if !validID(key) || r.CA.Identity == "" {
		return errors.New("unidentified diagnostic public CA copy requires inspection; preserved")
	}
	if err := checkDiagnosticCACopy(root, key, r.CA); err != nil {
		return err
	}
	if err := root.Remove(name); err != nil {
		return fmt.Errorf("remove diagnostic public CA copy: %w", err)
	}
	parent, err := root.Open(".")
	if err != nil {
		return err
	}
	err = errors.Join(parent.Sync(), parent.Close())
	if err != nil {
		return err
	}
	if _, err := root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		return errors.New("diagnostic public CA copy absence not verified")
	}
	return nil
}
