package trust

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gofrs/flock"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/localipc"
)

// receipt records only public authority and exact effects, never CA keys or
// daemon scope capabilities. Unknown effects never become retirement authority.
type receipt struct {
	Version         int          `json:"version"`
	CA              v1.GatewayCA `json:"ca"`
	Store           string       `json:"store"`
	Phase           string       `json:"phase"`
	EntryCreated    bool         `json:"entry_created"`
	TrustCreated    bool         `json:"trust_created"`
	Preexisting     bool         `json:"preexisting"`
	TrustSettings   string       `json:"trust_settings,omitempty"`
	Sources         []string     `json:"sources,omitempty"`
	LinkPreexisting bool         `json:"link_preexisting,omitempty"`
	LinkCreated     bool         `json:"link_created,omitempty"`
	LinkIdentity    string       `json:"link_identity,omitempty"`
	EntryIdentity   string       `json:"entry_identity,omitempty"`
	EntryOutcome    string       `json:"entry_outcome,omitempty"`
	ObservedAt      string       `json:"observed_at"`
}

func validFingerprint(s string) bool {
	data, err := hex.DecodeString(s)
	return err == nil && len(data) == 32 && hex.EncodeToString(data) == s
}

func receiptPath(dir, fingerprint string) (string, error) {
	if !validFingerprint(fingerprint) {
		return "", problem("select an exact lowercase DER SHA-256 fingerprint")
	}
	return filepath.Join(dir, fingerprint+".json"), nil
}

func readReceipt(path string) (receipt, error) {
	var r receipt
	if err := localipc.File(path, false); err != nil {
		return r, err
	}
	file, err := os.Open(path)
	if err != nil {
		return r, err
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	decodeErr := decoder.Decode(&r)
	if r.Version != 0 && r.Version != 1 {
		return r, &v1.PlanError{Code: "trust_receipt_version", Message: "unsupported receipt version preserved unchanged; use a compatible executable or a separate clean tracking location"}
	}
	if decodeErr != nil {
		return r, problem("damaged receipt preserved; restore compatible tracking or explicitly start clean tracking for future installs")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || r.Version != 1 || r.CA.Validate() != nil || r.Store != target() || (r.Phase != "intent" && r.Phase != "installed" && r.Phase != "partial" && r.Phase != "retired") {
		return r, problem("unsupported or invalid receipt preserved; it grants no removal authority")
	}
	if _, err := time.Parse(time.RFC3339Nano, r.ObservedAt); err != nil {
		return r, problem("invalid receipt observation time; preserved")
	}
	if len(r.Sources) > 64 {
		return r, problem("invalid known-source observations; preserved")
	}
	for _, source := range r.Sources {
		if !filepath.IsAbs(source) {
			return r, problem("invalid known-source observation; preserved")
		}
	}
	if filepath.Base(path) != r.CA.Fingerprint+".json" {
		return r, problem("receipt fingerprint mismatch; preserved")
	}
	return r, nil
}

func writeReceipt(path string, r receipt) (result error) {
	r.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".receipt-*")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.Remove(file.Name()); !os.IsNotExist(err) {
			result = errors.Join(result, err)
		}
	}()
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return err
	}
	// The rename consumed the temporary path; its absence is successful cleanup.
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func lockTracking(dir string) (*flock.Flock, error) {
	if err := localipc.Directory(dir, true); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "trust.lock")
	if _, err := os.Lstat(path); err == nil {
		if err := localipc.File(path, false); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	lock := flock.New(path, flock.SetPermissions(0600))
	acquired, err := lock.TryLock()
	if err != nil || !acquired {
		_ = lock.Close()
		return nil, problem("another foreground trust command owns tracking; retry after it finishes")
	}
	return lock, nil
}

// Install retains public daemon provenance and journals intent before stock OS
// effects. Fresh tracking preserves damaged evidence and grants no old authority.
func Install(ctx context.Context, dir string, ca v1.GatewayCA, fresh bool) (result v1.GatewayTrustResult, resultErr error) {
	result = Status(ctx, ca)
	result.Action = "install"
	result.Outcome = "unknown"
	if err := ca.Validate(); err != nil {
		return fail(result, err)
	}
	lock, err := lockTracking(dir)
	if err != nil {
		return fail(result, err)
	}
	defer func() { resultErr = errors.Join(resultErr, lock.Close()) }()
	path, err := receiptPath(dir, ca.Fingerprint)
	if err != nil {
		return fail(result, err)
	}
	result.Receipt = path
	old, err := readReceipt(path)
	var unsupported *v1.PlanError
	if errors.As(err, &unsupported) && unsupported.Code == "trust_receipt_version" {
		return fail(result, err)
	}
	if err == nil && !fresh {
		if old.Phase == "installed" {
			result.Outcome = "preserved"
			if err := validateEffects(ctx, &old); err != nil {
				return fail(result, err)
			}
			if result.Host.Status != "trusted" {
				return fail(result, problem("retained installation no longer proves OS trust; inspect drift or use explicit clean tracking without adopting old effects"))
			}
			if !slices.Contains(old.Sources, ca.StateDirectory) && len(old.Sources) < 64 {
				old.Sources = append(old.Sources, ca.StateDirectory)
				if err := writeReceipt(path, old); err != nil {
					return fail(result, err)
				}
			}
			result.Warnings = []string{"existing exact effects preserved; known sources are observations, not a complete consumer inventory"}
			return result, nil
		}
		return fail(result, problem("unfinished or retired tracking preserved; inspect exact effects or explicitly start clean tracking; old unknown effects stay manual"))
	}
	if err != nil && !os.IsNotExist(err) && !fresh {
		return fail(result, err)
	}
	if fresh {
		if _, err := os.Lstat(path); err == nil {
			// authz: archiving never transfers old effect ownership to the new receipt.
			if err := os.Rename(path, path+".preserved-"+time.Now().UTC().Format("20060102T150405.000000000")); err != nil {
				return fail(result, err)
			}
		} else if !os.IsNotExist(err) {
			return fail(result, err)
		}
	}
	r := receipt{Version: 1, CA: ca, Store: target(), Phase: "intent", Sources: []string{ca.StateDirectory}}
	staged, err := os.CreateTemp(dir, ".public-ca-*")
	if err != nil {
		return fail(result, err)
	}
	defer func() { resultErr = errors.Join(resultErr, os.Remove(staged.Name())) }()
	if _, err = io.Copy(staged, bytes.NewBufferString(ca.CertificatePEM)); err != nil {
		_ = staged.Close()
		return fail(result, err)
	}
	if err = staged.Close(); err != nil {
		return fail(result, err)
	}
	if err = install(ctx, path, staged.Name(), &r); err != nil {
		r.Phase = "partial"
		result.Host = Observe(ctx, ca, true)
		result.Outcome = "partial"
		return fail(result, errors.Join(err, writeReceipt(path, r)))
	}
	result.Host = Observe(ctx, ca, true)
	result.Effective = v1.TrustObservation{Status: "unknown", Store: target(), Context: "daemon-effective", Message: "refresh the source daemon observation after the OS mutation"}
	if result.Host.Status != "trusted" {
		r.Phase = "partial"
		result.Outcome = "partial"
		return fail(result, errors.Join(problem("OS trust was not proven after installation; preserve receipt and inspect policy/refresh; application/browser trust is separate"), writeReceipt(path, r)))
	}
	r.Phase = "installed"
	if err = writeReceipt(path, r); err != nil {
		result.Outcome = "partial"
		return fail(result, err)
	}
	result.Outcome = "installed"
	if r.Preexisting {
		result.Warnings = append(result.Warnings, "pre-existing exact entries/settings remain externally owned and will be preserved")
	}
	return result, nil
}

// Retire removes only exact effects with retained completed provisioning proof.
// Acknowledgement accepts consumer impact, never missing ownership evidence.
func Retire(ctx context.Context, dir, fingerprint string, ack bool, replacement *v1.GatewayCA) (result v1.GatewayTrustResult, resultErr error) {
	result = v1.GatewayTrustResult{APIVersion: v1.Version, Action: "retire", Outcome: "unknown", Host: Observe(ctx, v1.GatewayCA{}, true), Effective: v1.TrustObservation{Status: "unknown", Store: target(), Context: "daemon-effective", Message: "no current source daemon environment observed"}}
	if !ack {
		return fail(result, problem("retirement can break known and unknown consumers; inspect source/dependencies and use --acknowledge-impact to proceed"))
	}
	path, err := receiptPath(dir, fingerprint)
	if err != nil {
		return fail(result, err)
	}
	lock, err := lockTracking(dir)
	if err != nil {
		return fail(result, err)
	}
	defer func() { resultErr = errors.Join(resultErr, lock.Close()) }()
	r, err := readReceipt(path)
	if err != nil {
		return fail(result, err)
	}
	result = Status(ctx, r.CA)
	result.Action = "retire"
	result.Effective = v1.TrustObservation{Status: "unknown", Store: target(), Context: "daemon-effective", Message: "retained receipt is not a current daemon environment observation"}
	result.Receipt = path
	result.Outcome = "unknown"
	result.Warnings = []string{"known sources: " + strings.Join(r.Sources, ", "), "consumer inventory is incomplete; retirement may break other scopes, browsers or applications", "other stores/domains or explicit certificate-file overrides may retain trust"}
	if replacement != nil {
		if replacement.Validate() != nil || replacement.Fingerprint == fingerprint || Observe(ctx, *replacement, true).Status != "trusted" {
			return fail(result, problem("selected replacement must be a different validated managed root with verified OS trust before retirement"))
		}
	}
	if r.Phase == "retired" {
		result.Outcome = "retired"
		return result, nil
	}
	if r.Phase != "installed" {
		return fail(result, problem("incomplete provisioning evidence cannot authorize automated retirement; preserve receipt and use exact manual OS recovery"))
	}
	if !r.EntryCreated && !r.TrustCreated {
		result.Outcome = "preserved"
		result.Entry = "external-preserved"
		return result, nil
	}
	// Intent retains completed ownership fields; uncertain completion is never replayed.
	r.Phase = "intent"
	if err = writeReceipt(path, r); err != nil {
		return fail(result, err)
	}
	if err = retire(ctx, &r); err != nil {
		r.Phase = "partial"
		result.Host = Observe(ctx, r.CA, true)
		result.Entry = "unknown"
		result.Outcome = "partial"
		return fail(result, errors.Join(err, writeReceipt(path, r)))
	}
	r.Phase = "retired"
	result.Host = Observe(ctx, r.CA, true)
	result.Effective = v1.TrustObservation{Status: "unknown", Store: target(), Context: "daemon-effective", Message: "retained public evidence does not establish a live source daemon or its current environment"}
	result.Entry = "owned-effects-removed"
	if !r.EntryCreated {
		result.Entry = "external-entry-preserved"
	}
	if r.EntryOutcome != "" {
		result.Entry = r.EntryOutcome
	}
	if r.EntryOutcome == "public-entry-preserved-manual-deletion" {
		result.Warnings = append(result.Warnings, "admin TLS trust was withdrawn; the public certificate entry remains because exact current entry ownership cannot be proven by stock observations; inspect it before any manual deletion")
	}
	if strings.HasPrefix(r.Store, "linux:") && strings.Contains(r.EntryOutcome, "preserved") {
		result.Warnings = append(result.Warnings, "pre-existing or unowned store links were preserved; dangling references may require explicit manual review")
	}
	result.Outcome = "retired"
	if err = writeReceipt(path, r); err != nil {
		result.Outcome = "partial"
		return fail(result, err)
	}
	return result, nil
}

// Retained reads a bounded foreground receipt inventory without locks or effects.
// Damage stays visible and never grants old retirement authority.
func Retained(dir string) ([]v1.TrustReceiptSummary, []string) {
	if err := localipc.Directory(dir, false); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		if _, statErr := os.Lstat(dir); os.IsNotExist(statErr) {
			return nil, nil
		}
		return nil, []string{"trust tracking unavailable or unsafe; status remains usable; no old removal authority inferred"}
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, []string{"cannot read trust tracking; no old removal authority inferred"}
	}
	var summaries []v1.TrustReceiptSummary
	var warnings []string
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		if len(summaries)+len(warnings) >= 128 {
			warnings = append(warnings, "receipt observation limited to 128 entries; inspect tracking directory manually")
			break
		}
		r, err := readReceipt(filepath.Join(dir, file.Name()))
		if err != nil {
			warnings = append(warnings, "damaged/unsupported receipt preserved: "+file.Name())
			continue
		}
		summaries = append(summaries, v1.TrustReceiptSummary{Fingerprint: r.CA.Fingerprint, Sources: r.Sources, Store: r.Store, Phase: r.Phase, OwnedEffects: r.Phase == "installed" && (r.EntryCreated || r.TrustCreated), ObservedAt: r.ObservedAt})
	}
	return summaries, warnings
}
