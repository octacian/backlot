package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/trust"
)

// gatewayCA never accepts paths or pins from callers; only managed state grants provenance.
func (s *service) gatewayCA(ctx context.Context) (v1.GatewayCA, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return v1.GatewayCA{}, err
	}
	r, err := s.store.managed()
	if err != nil {
		return v1.GatewayCA{}, err
	}
	if !r.Initialized || r.RootHash == "" {
		return v1.GatewayCA{}, problem("gateway_ca", "start the managed gateway before selecting its public CA")
	}
	ca, err := s.managedPublicCA(r)
	if err == nil {
		ca.DaemonEffective = trust.Observe(ctx, ca, false)
	}
	return ca, err
}

func (s *service) managedPublicCA(r managedRecord) (v1.GatewayCA, error) {
	if err := validateManagedFiles(s.directory, r); err != nil {
		return v1.GatewayCA{}, err
	}
	_, _, storage := managedPaths(s.directory)
	path := filepath.Join(storage, "pki", "authorities", "local", "root.crt")
	data, err := os.ReadFile(path)
	if err != nil {
		return v1.GatewayCA{}, problem("gateway_ca", "cannot read validated public CA")
	}
	result := v1.GatewayCA{APIVersion: v1.Version, StateDirectory: s.directory, CertificatePath: path, CertificatePEM: string(data), Fingerprint: r.RootHash, Provenance: "validated-managed-ca-pin"}
	return result, result.Validate()
}

func trustInstallCommand(directory, fingerprint string) string {
	return "backlot gateway trust install --state-dir '" + strings.ReplaceAll(directory, "'", "'\\''") + "' --fingerprint " + fingerprint
}
