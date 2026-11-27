package v1

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"path/filepath"
)

// GatewayCARequest selects a read-only validated managed CA snapshot.
type GatewayCARequest struct {
	// APIVersion selects the shared contract version.
	APIVersion string `json:"api_version"`
}

// GatewayCA is public provisioning evidence validated by the owning daemon.
// It contains no private scope capability or signing key.
type GatewayCA struct {
	// APIVersion identifies the shared contract.
	APIVersion string `json:"api_version"`
	// StateDirectory identifies the source daemon state.
	StateDirectory string `json:"state_directory"`
	// CertificatePath identifies the protected managed public root source.
	CertificatePath string `json:"certificate_path"`
	// CertificatePEM contains exactly the validated public root.
	CertificatePEM string `json:"certificate_pem"`
	// Fingerprint is lowercase SHA-256 of certificate DER, matching the retained pin.
	Fingerprint string `json:"fingerprint"`
	// DaemonEffective is a fresh observation under the source daemon environment.
	DaemonEffective TrustObservation `json:"daemon_effective"`
	// Provenance records the daemon's completed managed-state validation.
	Provenance string `json:"provenance"`
}

// Validate checks public provenance structure and exact certificate identity.
// Expiry does not invalidate retained retirement evidence.
func (s GatewayCA) Validate() error {
	block, rest := pem.Decode([]byte(s.CertificatePEM))
	if s.APIVersion != Version || s.Provenance != "validated-managed-ca-pin" || !filepath.IsAbs(s.StateDirectory) || s.CertificatePath != filepath.Join(s.StateDirectory, "gateway", "data", "pki", "authorities", "local", "root.crt") || block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 || len(block.Headers) != 0 {
		return &PlanError{Code: "gateway_ca", Message: "invalid managed public CA provenance"}
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	hash := sha256.Sum256(block.Bytes)
	if err != nil || !cert.IsCA || !cert.BasicConstraintsValid || cert.CheckSignatureFrom(cert) != nil || s.Fingerprint != hex.EncodeToString(hash[:]) {
		return &PlanError{Code: "gateway_ca", Message: "managed public CA does not match its validated pin"}
	}
	if s.DaemonEffective.Status != "" {
		return s.DaemonEffective.Validate()
	}
	return nil
}

// TrustObservation reports root-specific host trust, independently of TLS readiness.
type TrustObservation struct {
	// Status is trusted, untrusted or unknown; failures never imply untrusted.
	Status string `json:"status"`
	// Store identifies the selected OS store and trust domain.
	Store string `json:"store"`
	// Context distinguishes OS trust from daemon-effective trust with overrides.
	Context string `json:"context"`
	// ObservedAt timestamps this observation; empty means unavailable.
	ObservedAt string `json:"observed_at,omitempty"`
	// Message describes the observation's limits or necessary follow-up.
	Message string `json:"message"`
}

// GatewayTrustResult reports foreground effects and retained public authority.
type GatewayTrustResult struct {
	// APIVersion identifies the shared contract.
	APIVersion string `json:"api_version"`
	// Action is status, install or retire.
	Action string `json:"action"`
	// CA identifies the exact managed root and validated public provenance.
	CA GatewayCA `json:"ca"`
	// Host reports root-specific selected OS trust, without certificate-file overrides.
	Host TrustObservation `json:"host"`
	// Effective reports the source daemon's fresh retained-environment observation.
	Effective TrustObservation `json:"effective"`
	// Outcome is observed, installed, preserved, retired, partial or unknown.
	Outcome string `json:"outcome"`
	// Receipt identifies retained public tracking outside daemon-state erasure.
	Receipt string `json:"receipt,omitempty"`
	// Entry reports certificate/source entry deletion separately from trust.
	Entry string `json:"entry,omitempty"`
	// Warnings describes known and unknown consumer impact and residual trust.
	Warnings []string `json:"warnings,omitempty"`
	// Retained identifies bounded receipt observations; damaged entries stay warnings.
	Retained []TrustReceiptSummary `json:"retained,omitempty"`
	// Error preserves truthful partial results alongside an actionable error.
	Error *PlanError `json:"error,omitempty"`
}

// TrustReceiptSummary identifies retained roots without implying live consumers.
type TrustReceiptSummary struct {
	// Fingerprint identifies exact certificate DER.
	Fingerprint string `json:"fingerprint"`
	// Sources contains known validated daemon state observations, not enrollment.
	Sources []string `json:"sources"`
	// Store identifies the exact provisioned target.
	Store string `json:"store"`
	// Phase reports retained intent/completion, not current trust.
	Phase string `json:"phase"`
	// OwnedEffects reports whether completed evidence may authorize exact retirement.
	OwnedEffects bool `json:"owned_effects"`
	// ObservedAt timestamps the retained observation.
	ObservedAt string `json:"observed_at"`
}

// Validate rejects invented trust states and ambiguous observation contexts.
func (o TrustObservation) Validate() error {
	if (o.Status != "trusted" && o.Status != "untrusted" && o.Status != "unknown") || o.Store == "" || o.Message == "" || (o.Context != "os" && o.Context != "effective-environment" && o.Context != "daemon-effective") {
		return &PlanError{Code: "gateway_trust", Message: "invalid trust observation"}
	}
	return nil
}
