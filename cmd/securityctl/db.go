package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Sergey-Bar/Ai-Captain/internal/command"
	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/migrate"
	"github.com/Sergey-Bar/Ai-Captain/migrations"
)

// connect builds a pool from the site configuration's database block.
//
// The password is deliberately not a parameter and not a flag. It is read from
// the encrypted secret store at connect time, because a CLI that accepts a
// password as an argument teaches operators to type it into a shell history.
func connect(ctx context.Context, tenantID string) (*dbx.Pool, *dbx.Config, error) {
	cfg := dbx.DefaultConfig()
	if v := os.Getenv("SP_DATABASE_URL"); v != "" {
		if err := cfg.ApplyDSN(v); err != nil {
			return nil, nil, err
		}
	}
	pool, err := dbx.Open(ctx, cfg, os.Getenv("SP_DATABASE_PASSWORD"))
	if err != nil {
		return nil, &cfg, err
	}
	return pool, &cfg, nil
}

// migrateCommand mirrors `security-migrate` inside the operator CLI, because an
// engineer at a site with one binary on the path should not have to know which
// of the two to reach for.
func migrateCommand() *command.Command {
	m := &command.Command{
		Name:    "migrate",
		Summary: "Schema management",
	}
	m.Register(
		&command.Command{
			Name:    "up",
			Summary: "Apply pending migrations",
			Flags: func(fs *flag.FlagSet) {
				fs.Bool("dry-run", false, "Print the plan and change nothing.")
				fs.Bool("json", false, "Emit machine-readable output.")
			},
			Run: func(ctx context.Context, env *command.Env, args []string) error {
				if len(args) > 0 {
					return printUsageError("migrate up takes no positional arguments")
				}
				return runMigrate(ctx, env, "up")
			},
		},
		&command.Command{
			Name:    "status",
			Summary: "Report schema version, pending set and supported window",
			Flags: func(fs *flag.FlagSet) {
				fs.Bool("json", false, "Emit machine-readable output.")
			},
			Run: func(ctx context.Context, env *command.Env, args []string) error {
				return runMigrate(ctx, env, "status")
			},
		},
		&command.Command{
			Name:    "verify",
			Summary: "Confirm every applied migration still matches this build",
			Run: func(ctx context.Context, env *command.Env, args []string) error {
				pool, _, err := connect(ctx, "")
				if err != nil {
					return err
				}
				defer pool.Close()
				if err := (&migrate.Runner{Pool: pool.Pool}).VerifyChecksums(ctx); err != nil {
					return err
				}
				fmt.Fprintln(env.Stdout, "migrations: all applied checksums match this build")
				return nil
			},
		},
	)
	return m
}

func runMigrate(ctx context.Context, env *command.Env, mode string) error {
	pool, _, err := connect(ctx, "")
	if err != nil {
		return err
	}
	defer pool.Close()

	runner := &migrate.Runner{Pool: pool.Pool}
	switch mode {
	case "status":
		report, err := runner.StatusReport(ctx)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	default:
		plan, err := runner.Up(ctx)
		if err != nil {
			return err
		}
		if len(plan.Pending) == 0 {
			fmt.Fprintf(env.Stdout, "schema is at version %d; nothing to apply\n", plan.CurrentVersion)
			return nil
		}
		fmt.Fprintf(env.Stdout, "schema advanced from version %d to %d\n", plan.CurrentVersion, plan.TargetVersion)
		return nil
	}
}

// schemaWindow is what `preflight` and `doctor` print before anything else: is
// this database in a state this binary is allowed to touch?
func schemaWindow(ctx context.Context) (string, error) {
	compat, err := migrations.ReleaseCompatibility()
	if err != nil {
		return "", err
	}
	pool, _, err := connect(ctx, "")
	if err != nil {
		return "", err
	}
	defer pool.Close()

	current, err := (&migrate.Runner{Pool: pool.Pool}).CurrentVersion(ctx)
	if err != nil {
		return "", err
	}
	ok := current == 0 || (current >= compat.MinSchemaVersion && current <= compat.MaxSchemaVersion)
	state := "in range"
	if !ok {
		state = "OUT OF RANGE"
	}
	return fmt.Sprintf("schema version %d, supported [%d, %d] — %s (contracts %s)",
		current, compat.MinSchemaVersion, compat.MaxSchemaVersion, state, compat.ContractsVersion), nil
}
