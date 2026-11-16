package cli

import (
	"bytes"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	urfave "github.com/urfave/cli/v3"
)

func TestInfraGatewayTrustOutput(t *testing.T) {
	var text, diagnostic bytes.Buffer
	g := v1.GatewayResponse{Mode: "managed", Status: "running", RootFingerprint: "exact-root", HostTrust: &v1.TrustObservation{Status: "unknown", Store: "selected-domain", Message: "configured trust does not prove application TLS"}, TrustCommand: "backlot gateway trust install --state-dir '/owned/state' --fingerprint exact-root", NativeOwnershipWarnings: []v1.NativeOwnershipWarning{{Message: "inspect unresolved ownership"}}}
	c := &urfave.Command{Writer: &text, ErrWriter: &diagnostic}
	r := v1.InfraResponse{Directory: "/owned/state", Daemon: "running", Ready: true, Gateway: &g, Guidance: "inspect selected scope"}
	if err := infraOutput(c, r); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Infrastructure", "Gateway running", "exact-root", "Host trust: unknown", "selected-domain", g.HostTrust.Message, g.TrustCommand, r.Guidance} {
		if !strings.Contains(text.String(), want) {
			t.Fatal(want, text.String())
		}
	}
	if !strings.Contains(diagnostic.String(), "inspect unresolved ownership") {
		t.Fatal(diagnostic.String())
	}
}
