package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/Sergey-Bar/vision/internal/command"
	"github.com/Sergey-Bar/vision/internal/sitemodel"
)

// cameraAdjacencyCommand is the operator's handle on the adjacency table: the
// plan's C3 backfill and the console's answer to "which cameras are adjacent to
// this one?".
//
// It exists as a command rather than as a background loop because the table is
// a projection of the operator's YAML. The YAML is edited by hand, by a person,
// on a schedule a person owns — so the recompute is run when that person says
// so, and the answer to "why does this table say these two cameras are
// adjacent" is always "because the file at this path said so, on this run".
func cameraAdjacencyCommand() *command.Command {
	c := &command.Command{
		Name:    "camera-adjacency",
		Summary: "Derive and read camera adjacency from the site model",
		Usage: `camera-adjacency — which cameras see which other cameras' ground.

  camera-adjacency apply --site <yaml> --tenant <id> [--site <id>] [--version <label>]
  camera-adjacency list  --tenant <id> --site <id> --camera <id>

The table is a projection of the site model, or of nothing: an unmeasured
field of view produces no overlap, because a defaulted bearing would draw a
corridor between two cameras that have never seen each other's ground. An
operator's hand-asserted rows survive an apply; the model is a projection of
what was measured, not a replacement for what a person knows.`,
	}
	c.Children = []*command.Command{
		cameraAdjacencyApplyCommand(),
		cameraAdjacencyListCommand(),
	}
	return c
}

// adjacencyOptions are the flags the two subcommands bind, so a read and the
// apply it reads after cannot be pointed at different inputs.
type adjacencyOptions struct {
	tenant  string
	siteID  string
	version string
}

func cameraAdjacencyApplyCommand() *command.Command {
	var o adjacencyOptions
	var siteFile string
	c := &command.Command{
		Name:    "apply",
		Summary: "Recompute a site's derived adjacency from its site model",
	}
	c.Flags = func(fs *flag.FlagSet) {
		// `--site` names the YAML, the way `site-config --site` does: the file
		// is the input, and the site id it declares is the id it is applied to.
		fs.StringVar(&siteFile, "site", "", "Site model YAML: cameras, zones and routes.")
		fs.StringVar(&o.tenant, "tenant", "", "Tenant id (required).")
		fs.StringVar(&o.version, "version", "", "Version label recorded on the derived rows.")
	}
	c.Run = func(ctx context.Context, env *command.Env, args []string) error {
		if len(args) > 0 {
			return &command.UsageError{Err: errors.New("pass options as flags, not positionals")}
		}
		if siteFile == "" {
			return &command.UsageError{Err: errors.New("--site is required")}
		}
		if o.tenant == "" {
			return &command.UsageError{Err: errors.New("--tenant is required")}
		}
		model, err := sitemodel.LoadFromFile(siteFile)
		if err != nil {
			return err
		}
		if model.ID == "" {
			return &command.UsageError{Err: errors.New("the site model declares no id")}
		}
		label := o.version
		if label == "" {
			label = siteFile
		}

		pool, err := connect(ctx)
		if err != nil {
			return err
		}
		defer pool.Close()
		svc := &sitemodel.Service{Pool: pool}
		if err := svc.ReplaceFromModel(ctx, o.tenant, model.ID, label, model); err != nil {
			return err
		}
		derived := model.AdjacentCameras()
		fmt.Fprintf(env.Stdout,
			"camera-adjacency: %d derived relation(s) recorded for site %s\n",
			len(derived), model.ID)
		// The pairs are printed, because the count alone is a number and an
		// operator checking a recompute is checking that the right cameras were
		// linked.
		for _, adj := range derived {
			fmt.Fprintf(env.Stdout, "  %s ~ %s (%s)\n", adj.CameraID, adj.NeighbourID, adj.Relation)
		}
		if len(derived) == 0 {
			fmt.Fprintln(env.Stdout,
				"  none. That is the honest answer for a site whose cameras share no zone and have declared no view")
		}
		return nil
	}
	return c
}

func cameraAdjacencyListCommand() *command.Command {
	var o adjacencyOptions
	var camera string
	c := &command.Command{
		Name:    "list",
		Summary: "List the cameras adjacent to one camera",
	}
	c.Flags = func(fs *flag.FlagSet) {
		fs.StringVar(&o.tenant, "tenant", "", "Tenant id (required).")
		fs.StringVar(&o.siteID, "site", "", "Site id (required).")
		fs.StringVar(&camera, "camera", "", "Camera id (required).")
	}
	c.Run = func(ctx context.Context, env *command.Env, args []string) error {
		if len(args) > 0 {
			return &command.UsageError{Err: errors.New("pass options as flags, not positionals")}
		}
		if o.tenant == "" || o.siteID == "" || camera == "" {
			return &command.UsageError{Err: errors.New("--tenant, --site and --camera are all required")}
		}
		pool, err := connect(ctx)
		if err != nil {
			return err
		}
		defer pool.Close()
		svc := &sitemodel.Service{Pool: pool}
		neighbours, err := svc.Neighbours(ctx, o.tenant, o.siteID, camera)
		if err != nil {
			return err
		}
		if len(neighbours) == 0 {
			fmt.Fprintf(env.Stdout, "camera %s has no recorded neighbours at site %s\n", camera, o.siteID)
			return nil
		}
		fmt.Fprintf(env.Stdout, "camera %s is adjacent to %d camera(s) at site %s:\n", camera, len(neighbours), o.siteID)
		for _, n := range neighbours {
			fmt.Fprintf(env.Stdout, "  %s\n", n)
		}
		return nil
	}
	return c
}
