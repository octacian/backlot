package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestGatewayTrustArgumentsAndPartialOutput(t *testing.T) {
	for _, args := range [][]string{{"status", "extra"}, {"install"}, {"install", "--fingerprint", "private-canary", "--fingerprint", "other"}, {"retire", "--fingerprint", "abc", "extra"}, {"status", "--acknowledge-impact"}} {
		var out, diagnostic bytes.Buffer
		err := NewCommand(v1.VersionResponse{}, &out, &diagnostic).Run(context.Background(), append([]string{"backlot", "--json", "gateway", "trust"}, args...))
		var response v1.ErrorResponse
		if err == nil || json.Unmarshal(out.Bytes(), &response) != nil || response.Error.Code != "invalid_arguments" || strings.Contains(out.String(), "private-canary") {
			t.Fatal(args, err, out.String())
		}
	}
	r := v1.GatewayTrustResult{Action: "install", Outcome: "partial", CA: v1.GatewayCA{Fingerprint: "exact-root", StateDirectory: "/known-state"}, Host: v1.TrustObservation{Status: "unknown", Store: "selected-domain"}, Effective: v1.TrustObservation{Status: "unknown", Message: "native standalone root acceptance unavailable"}, Entry: "external-preserved", Warnings: []string{"known and unknown consumer impact"}, Error: &v1.PlanError{Code: "gateway_trust", Message: "stock effect incomplete"}}
	var text bytes.Buffer
	if err := gatewayTrustOutput(&text, r); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"exact-root", "partial", "unknown", "external-preserved", "known and unknown consumer impact", "stock effect incomplete", "native standalone root acceptance unavailable"} {
		if !strings.Contains(text.String(), want) {
			t.Fatal(want, text.String())
		}
	}
}

func TestGatewayTrustRetirementDiagnosticFraming(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out, diagnostic bytes.Buffer
	err := NewCommand(v1.VersionResponse{}, &out, &diagnostic).Run(context.Background(), []string{"backlot", "--json", "gateway", "trust", "retire", "--fingerprint", strings.Repeat("a", 64), "--acknowledge-impact"})
	var result v1.GatewayTrustResult
	if err == nil || json.Unmarshal(out.Bytes(), &result) != nil || result.Action != "retire" || result.Error == nil {
		t.Fatal("refusal lost typed JSON result", err, out.String())
	}
	if runtime.GOOS == "darwin" {
		for _, want := range []string{"interactive administrator authentication even under sudo", "headless retirement is unproven", "preserve the receipt"} {
			if !strings.Contains(diagnostic.String(), want) {
				t.Fatal("missing foreground warning", want, diagnostic.String())
			}
		}
	} else if diagnostic.Len() != 0 {
		t.Fatal("macOS warning on another platform", diagnostic.String())
	}
}
