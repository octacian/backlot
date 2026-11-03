package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
	"github.com/octacian/backlot/internal/projectconfig"
	urfave "github.com/urfave/cli/v3"
)

func projectIdentityCommand(name string) *urfave.Command {
	flags := append(daemonFlags(false), &urfave.StringFlag{Name: "project", Usage: "Destination project directory or manifest"}, &urfave.StringFlag{Name: "config", Usage: "Machine configuration for selected destination scenes"}, &urfave.StringFlag{Name: "project-id", Usage: "Exact retained shared-project ID"}, &urfave.StringFlag{Name: "checkout-id", Usage: "Exact retained checkout registration ID"})
	if name == "reassociate" {
		flags = append(flags, &urfave.BoolFlag{Name: "apply", Usage: "Explicitly apply the acknowledged preview after consumers stop"}, &urfave.StringFlag{Name: "preview", Usage: "Exact digest from a fresh read-only preview"})
	}
	return &urfave.Command{Name: name, ArgsUsage: " ", Usage: "Diagnose retained identity and preview explicit reassociation", Description: "Without --apply, read identities and blockers without mutation. Reassociation preserves service/instance IDs, data, credentials and allocations; all affected consumers must be stopped. Private setup is a separate prerequisite. Relative fresh-only executable relocation remains unsupported.\nExamples:\n  backlot project diagnose --project PATH\n  backlot project reassociate --project PATH --project-id PROJECT_ID --checkout-id CHECKOUT_ID\n  backlot project reassociate --project PATH --project-id PROJECT_ID --checkout-id CHECKOUT_ID --apply --preview DIGEST", Flags: flags, Action: func(ctx context.Context, c *urfave.Command) error {
		if c.Args().Len() != 0 {
			return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "project identity commands take flags only"})
		}
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		project := c.String("project")
		if project == "" {
			project = cwd
		}
		project, err = filepath.Abs(project)
		if err != nil {
			return err
		}
		source, err := plan.Locate(project)
		if err != nil {
			return output(c, nil, err)
		}
		root, err := projectconfig.Root(source.CheckoutPath)
		if err != nil {
			return output(c, nil, err)
		}
		config := c.String("config")
		if config != "" {
			config, err = filepath.Abs(config)
			if err != nil {
				return err
			}
		}
		request := v1.ReassociationRequest{APIVersion: v1.Version, ProjectPath: source.ProjectPath, ConfigPath: config, RegistryRoot: root, ProjectID: c.String("project-id"), CheckoutID: c.String("checkout-id"), Apply: c.Bool("apply"), Preview: c.String("preview")}
		if err := request.Validate(); err != nil {
			return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "use exact project/checkout IDs; apply also requires the fresh preview digest"})
		}
		client, err := runtimeClient(c)
		if err != nil {
			return output(c, nil, err)
		}
		defer client.Close()
		response, err := client.Reassociate(ctx, request)
		if err != nil || c.Bool("json") {
			return output(c, response, err)
		}
		if _, err = fmt.Fprintf(c.Writer, "Requesting checkout: %s (%s)\nPer-worktree Git directory: %s\nResolved sharing anchor: %s (%s)\nRelative project: %s; declared project: %s; observed ID: %s\n", response.Checkout.Path, response.Checkout.ID, response.Checkout.GitDirectory, response.Current.Anchor, response.Current.Filesystem, response.Current.Relative, response.Current.Project, response.Current.ID); err != nil {
			return err
		}
		for _, fs := range response.Filesystems {
			if _, err = fmt.Fprintf(c.Writer, "Filesystem %s: device=%s inode=%s\n", fs.Path, fs.Device, fs.Inode); err != nil {
				return err
			}
		}
		for _, r := range response.Projects {
			if _, err = fmt.Fprintf(c.Writer, "Retained project %s: recorded anchor %s (%s); current binding %s (%s)\n", r.Origin.ID, r.Origin.Anchor, r.Origin.Filesystem, r.Current.Anchor, r.Current.Filesystem); err != nil {
				return err
			}
		}
		for _, i := range response.Instances {
			if _, err = fmt.Fprintf(c.Writer, "Instance %s (%s): recorded checkout %s; current %s; retained checkout ID %s; selected=%t; recorded checkout/Git device/inode: unrecorded\n", i.ID, i.Status, i.Recorded.Path, i.Association.Checkout.Path, i.Association.CheckoutParent, i.Selected); err != nil {
				return err
			}
		}
		for _, service := range response.Services {
			if _, err = fmt.Fprintf(c.Writer, "Retained service %s (%s): %s\n", service.ID, service.Name, service.Status); err != nil {
				return err
			}
		}
		for _, a := range response.Allocations {
			if _, err = fmt.Fprintf(c.Writer, "Allocation %s (%s/%s): service %s; released=%t\n", a.AllocationID, a.InstanceID, a.Resource, a.ServiceID, a.Released); err != nil {
				return err
			}
		}
		if _, err = fmt.Fprintf(c.Writer, "Private registry: reviewed %s; daemon runtime %s; association %s; recorded %s; generation %s; state %s\n", response.Private.OperationRoot, response.Private.RuntimeRoot, response.Private.Association, response.Private.RecordedAssociation, response.Private.GenerationPath, response.Private.State); err != nil {
			return err
		}
		for _, line := range response.Changes {
			if _, err = fmt.Fprintln(c.Writer, "Change:", line); err != nil {
				return err
			}
		}
		for _, line := range response.Preserved {
			if _, err = fmt.Fprintln(c.Writer, "Preserved:", line); err != nil {
				return err
			}
		}
		for _, line := range response.Blockers {
			if _, err = fmt.Fprintln(c.Writer, "Blocked:", line); err != nil {
				return err
			}
		}
		_, err = fmt.Fprintf(c.Writer, "Preview: %s\nApplied: %t\n", response.Preview, response.Applied)
		return err
	}}
}
