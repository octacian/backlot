package cli

import (
	"context"
	"fmt"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	urfave "github.com/urfave/cli/v3"
)

func startupSummary(ctx context.Context, cli *client.Client, response v1.InstanceResponse) *v1.StartupSummary {
	instance := response.Instance
	if instance.Plan.Lifetime != v1.Persistent || instance.Status != v1.RuntimeReady || len(instance.Plan.StartupDetails) == 0 {
		return nil
	}
	summary := &v1.StartupSummary{}
	unavailable := func() *v1.StartupSummary {
		summary.Details = nil
		summary.Warning = &v1.PlanError{Code: "startup_details_unavailable", Message: "Startup details unavailable; startup succeeded. Use fixtures or secret to inspect the recorded instance."}
		return summary
	}
	if instance.Execution == nil || instance.Execution.Attempt == "" {
		return unavailable()
	}
	summary.Attempt = instance.Execution.Attempt
	for _, selection := range instance.Plan.StartupDetails {
		var selected *v1.FixtureRecord
		for _, fixture := range instance.Execution.Fixtures {
			if fixture.Component == selection.Component && fixture.Name == selection.Fixture {
				if selected != nil {
					return unavailable()
				}
				selected = &fixture
			}
		}
		if selected == nil {
			return unavailable()
		}
		if selected.Sensitive {
			request := v1.FixturesRequest{APIVersion: v1.Version, InstanceID: instance.ID, ExpectedAttempt: summary.Attempt, Component: selection.Component, SecretName: selection.Fixture}
			private, err := cli.FixtureSecret(ctx, request)
			if err != nil {
				return unavailable()
			}
			selected = &private.Fixtures[0]
		}
		summary.Details = append(summary.Details, v1.StartupDetail{StartupDetailSelection: selection, Value: selected.Value, Sensitive: selected.Sensitive})
	}
	return summary
}

func startupOutput(ctx context.Context, c *urfave.Command, cli *client.Client, response v1.InstanceResponse) error {
	response.StartupDetails = startupSummary(ctx, cli, response)
	if err := output(c, response, nil); err != nil {
		return err
	}
	return startupDetailsOutput(c, response.StartupDetails)
}

func startupDetailsOutput(c *urfave.Command, summary *v1.StartupSummary) error {
	if summary == nil || c.Bool("json") {
		return nil
	}
	if summary.Warning != nil {
		_, err := fmt.Fprintln(c.ErrWriter, "Warning:", summary.Warning.Message)
		return err
	}
	for _, detail := range summary.Details {
		if _, err := fmt.Fprintf(c.Writer, "%s: %s\n", detail.Label, detail.Value); err != nil {
			return err
		}
	}
	return nil
}

func startupAttempt(instance v1.Instance) string {
	if instance.Execution == nil {
		return ""
	}
	return instance.Execution.Attempt
}

func startupConflict() error {
	return &v1.PlanError{Code: "startup_conflict", Message: "Accepted startup attempt was replaced before readiness; inspect the recorded instance."}
}
