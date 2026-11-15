package daemon

import (
	"context"
	"errors"
	"fmt"

	v1 "github.com/octacian/backlot/api/v1"
)

func (s *service) infra(ctx context.Context, action string, q v1.InfraRequest) v1.InfraResponse {
	out := v1.InfraResponse{APIVersion: v1.Version, DaemonGeneration: s.generation, Directory: s.directory, Daemon: "running", Guidance: fmt.Sprintf("Inspect recorded blockers: backlot activity --state-dir %q; inspect workload IDs before explicit recovery", s.directory)}
	gateway, err := s.gatewayControlSelected(ctx, action, v1.GatewayRequest{APIVersion: v1.Version, ConfigPath: q.ConfigPath, DefaultConfigPath: q.DefaultConfigPath}, true)
	if err != nil {
		out.Error = infraError(err)
		gateway, _ = s.gatewayControl(ctx, "status", v1.GatewayRequest{APIVersion: v1.Version})
	}
	if gateway.APIVersion == v1.Version {
		if gateway.Mode == "managed" && gateway.Status == "unconfigured" && gateway.Message == "" {
			gateway.Message = "No retained managed gateway configuration; external gateway readiness is unknown and remains operator-owned"
		}
		out.Gateway = &gateway
	}
	activity, readErr := s.activity(ctx, v1.ActivityRequest{APIVersion: v1.Version, Limit: 100})
	if readErr == nil {
		out.Activity = &activity
	} else if out.Error == nil {
		out.Error = infraError(readErr)
	}
	out.Ready = out.Error == nil && !activity.AdmissionClosed && (gateway.Status == "running" || gateway.Status == "unconfigured" || gateway.Mode == "external" || gateway.Mode == "none")
	return out
}
func infraError(err error) *v1.PlanError {
	if err == nil {
		return nil
	}
	var detail *v1.PlanError
	if errors.As(err, &detail) {
		return detail
	}
	return &v1.PlanError{Code: "infra_failed", Message: "infrastructure operation failed; inspect activity in the same scope"}
}
