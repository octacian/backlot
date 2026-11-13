// Package trust owns explicit foreground host trust effects and public receipts.
// It never opens daemon state, installs keys or mutates application trust.
package trust

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/localipc"
	"github.com/octacian/backlot/internal/tlsverify"
)

// Directory returns the account-local tracking location outside daemon erasure.
func Directory() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "backlot", "trust"), nil
}

func target() string {
	if runtime.GOOS == "darwin" {
		return "macos:admin:/Library/Keychains/System.keychain"
	}
	if runtime.GOOS == "linux" {
		return "linux:system:/usr/local/share/ca-certificates"
	}
	return "unsupported"
}

// Observe performs bounded root-specific fresh verification. Receipt damage and
// mutation locks cannot affect this informational operation.
func Observe(ctx context.Context, ca v1.GatewayCA, osTrust bool) v1.TrustObservation {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	observation := v1.TrustObservation{Status: "unknown", Store: target(), Context: "os", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Message: "root-specific OS trust could not be established; browser and application trust are separate"}
	if !osTrust {
		observation.Context = "effective-environment"
	}
	if ca.Validate() != nil || target() == "unsupported" {
		return observation
	}
	if osTrust {
		selected, err := selectedTrust(ctx, ca)
		if err != nil {
			observation.Message = err.Error()
			return observation
		}
		if !selected {
			observation.Status = "untrusted"
			observation.Message = "no exact supported TLS trust in selected OS domain; other domains/applications may differ"
			return observation
		}
	}
	result, err := tlsverify.Run(ctx, tlsverify.Request{RootPEM: ca.CertificatePEM}, osTrust)
	if err != nil {
		return observation
	}
	if result.Verified {
		observation.Status = "trusted"
		observation.Message = "exact root accepted by fresh Go verification in this context; browser/application acceptance is separate"
	}
	if result.Failure == "rejected" {
		observation.Status = "untrusted"
		observation.Message = "exact root rejected by fresh Go verification; explicitly install host trust or diagnose certificate dates and policy"
	}
	return observation
}

// Status observes a selected validated root without creating or locking tracking.
func Status(ctx context.Context, ca v1.GatewayCA) v1.GatewayTrustResult {
	return v1.GatewayTrustResult{APIVersion: v1.Version, Action: "status", CA: ca, Host: Observe(ctx, ca, true), Effective: ca.DaemonEffective, Outcome: "observed"}
}

func problem(message string) error { return &v1.PlanError{Code: "gateway_trust", Message: message} }

func command(ctx context.Context, executable string, args ...string) error {
	if os.Geteuid() != 0 {
		args = append([]string{"--", executable}, args...)
		executable = "/usr/bin/sudo"
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return errors.Join(ctx.Err(), problem("stock OS authorization/tool failed; inspect stderr and retained receipt, then use normal administrator authorization or manual recovery"))
	}
	return nil
}

func fail(r v1.GatewayTrustResult, err error) (v1.GatewayTrustResult, error) {
	var detail *v1.PlanError
	if !errors.As(err, &detail) {
		detail = &v1.PlanError{Code: "gateway_trust", Message: fmt.Sprintf("trust tracking failed: %v", err)}
	}
	r.Error = detail
	return r, err
}

// StoredStatus observes a retained exact public root after source-state erasure.
// Historical daemon environment observations are never presented as current.
func StoredStatus(ctx context.Context, dir, fingerprint string) (v1.GatewayTrustResult, error) {
	result := v1.GatewayTrustResult{APIVersion: v1.Version, Action: "status", Outcome: "unknown", Host: Observe(ctx, v1.GatewayCA{}, true), Effective: v1.TrustObservation{Status: "unknown", Store: target(), Context: "daemon-effective", Message: "no current source daemon environment observed"}}
	if err := localipc.Directory(dir, false); err != nil {
		return fail(result, err)
	}
	path, err := receiptPath(dir, fingerprint)
	if err != nil {
		return fail(result, err)
	}
	r, err := readReceipt(path)
	if err != nil {
		return fail(result, err)
	}
	r.CA.DaemonEffective = v1.TrustObservation{Status: "unknown", Store: target(), Context: "daemon-effective", Message: "retained receipt does not observe a live daemon environment"}
	result = Status(ctx, r.CA)
	result.Receipt = path
	return result, nil
}
