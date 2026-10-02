// Command security-migrate applies the ordered Postgres migrations.
//
// It is a separate binary on purpose. The migrator is the one component that
// owns the schema, and giving the long-running core the ability to change the
// shape of its own tables under it is a standing invitation to an in-place
// upgrade that cannot be rolled back.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/migrate"
	"github.com/Sergey-Bar/Ai-Captain/migrations"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "security-migrate: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		dsn          = flag.String("dsn", "", "Postgres DSN without a password. SP_DATABASE_URL is the fallback.")
		passwordFlag = flag.String("password", "", "Database password. Prefer SP_DATABASE_PASSWORD; a CLI argument is visible in the process list.")
		asJSON       = flag.Bool("json", false, "Emit machine-readable output.")
		dryRun       = flag.Bool("dry-run", false, "Print the plan and change nothing.")
		timeout      = flag.Duration("timeout", 10*time.Minute, "Overall timeout. A migration that outruns this is a migration that needs a human.")
	)
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() == 0 {
		flag.Usage()
		return errors.New("a subcommand is required")
	}
	command := flag.Arg(0)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	ctx, cancel = context.WithTimeout(ctx, *timeout)
	defer cancel()

	cfg, err := connectConfig(*dsn, resolvePassword(*passwordFlag))
	if err != nil {
		return err
	}
	pool, err := dbx.Open(ctx, cfg, resolvePassword(*passwordFlag))
	if err != nil {
		return err
	}
	defer pool.Close()

	runner := &migrate.Runner{Pool: pool.Pool, DryRun: *dryRun}
	if !*asJSON {
		runner.Logf = func(format string, args ...any) { fmt.Printf(format+"\n", args...) }
	}

	switch command {
	case "up":
		plan, err := runner.Up(ctx)
		if err != nil {
			if *asJSON {
				_ = json.NewEncoder(os.Stderr).Encode(map[string]any{"ok": false, "error": err.Error()})
			}
			return err
		}
		return reportPlan(plan, *asJSON, *dryRun)
	case "status":
		report, err := runner.StatusReport(ctx)
		if err != nil {
			return err
		}
		// The phase on a migration says how it was *meant* to ship. A carried
		// violation says one of them was not, and the operator reading a status
		// report is entitled to know which version that is — otherwise "every
		// migration is an expand and every expand is safe" is something the tool
		// asserts about a tree where it is not true.
		report.CarriedViolations = migrations.CarriedContractViolations()
		if *asJSON {
			return json.NewEncoder(os.Stdout).Encode(report)
		}
		printStatus(report)
		return nil
	case "version":
		v, err := runner.CurrentVersion(ctx)
		if err != nil {
			return err
		}
		fmt.Println(v)
		return nil
	case "verify":
		if err := runner.VerifyChecksums(ctx); err != nil {
			return err
		}
		fmt.Println("migrations: all applied checksums match this build")
		return nil
	default:
		flag.Usage()
		return fmt.Errorf("unknown subcommand %q", command)
	}
}

// connectConfig resolves the database to migrate.
//
// It refuses rather than falling back to the compiled-in localhost default. The
// fallback was two lines and it was invisible: `security-migrate up` in a shell
// without SP_DATABASE_URL connected to 127.0.0.1:5432/security as security_core
// and, if that database existed, began changing its schema. The refusal names
// both ways to name a database, and `dbx.ErrNoTarget` is what a caller tests for.
//
// The second return value was the password flag and it was never used here; the
// password is resolved separately and passed to dbx.Open, because a flag argument
// is visible in the process list.
func connectConfig(dsnFlag, _ string) (dbx.Config, error) {
	return dbx.ConfigForTarget(dsnFlag, "SP_DATABASE_URL")
}

// resolvePassword prefers the dedicated environment variable over the flag. A
// flag argument is visible in the process list to anything running as the same
// user; the environment is not, and on a site install neither is printed.
func resolvePassword(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return os.Getenv("SP_DATABASE_PASSWORD")
}

func reportPlan(plan migrate.Plan, asJSON, dryRun bool) error {
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"ok":               true,
			"dryRun":           dryRun,
			"currentVersion":   plan.CurrentVersion,
			"targetVersion":    plan.TargetVersion,
			"pending":          len(plan.Pending),
			"minSchemaVersion": plan.Compat.MinSchemaVersion,
			"maxSchemaVersion": plan.Compat.MaxSchemaVersion,
		})
	}
	if len(plan.Pending) == 0 {
		fmt.Printf("schema is at version %d; nothing to apply\n", plan.CurrentVersion)
		return nil
	}
	if dryRun {
		fmt.Printf("dry run: %d migration(s) would be applied to reach version %d\n",
			len(plan.Pending), plan.TargetVersion)
		for _, m := range plan.Pending {
			fmt.Printf("  %s (%s)\n", m.FileName(), m.Phase)
		}
		return nil
	}
	fmt.Printf("schema advanced from version %d to %d\n", plan.CurrentVersion, plan.TargetVersion)
	return nil
}

func printStatus(s migrate.Status) {
	fmt.Printf("schema version : %d\n", s.CurrentVersion)
	fmt.Printf("this build     : %d\n", s.TargetVersion)
	fmt.Printf("supported range: [%d, %d]\n", s.Compat.MinSchemaVersion, s.Compat.MaxSchemaVersion)
	if s.Compatible {
		fmt.Println("compatibility  : OK")
	} else {
		fmt.Println("compatibility  : OUT OF RANGE — upgrade or roll back the binary")
	}
	if len(s.Pending) > 0 {
		fmt.Printf("pending (%d):\n", len(s.Pending))
		for _, p := range s.Pending {
			fmt.Printf("  %-40s %s\n", p.File, p.Phase)
		}
	} else {
		fmt.Println("pending        : none")
	}
	if len(s.Applied) > 0 {
		fmt.Println("applied:")
		for _, a := range s.Applied {
			fmt.Printf("  %-40s %-9s %s\n",
				fmt.Sprintf("%04d_%s.%s", a.Version, a.Name, a.Phase),
				a.AppliedAt.Format(time.RFC3339),
				a.Checksum[:12])
		}
	}
	// The phase column above says how a migration was meant to ship. This is the
	// list of the ones where that is not the whole truth, printed here because
	// this is the output an operator reads to decide whether a database is in the
	// state they think it is.
	if len(s.CarriedViolations) > 0 {
		fmt.Println("carried phase violations:")
		for file, why := range s.CarriedViolations {
			fmt.Printf("  %-40s %s\n", file, why)
		}
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `security-migrate — ordered Postgres migrations, forward-only.

Usage:
  security-migrate [flags] <command>

Commands:
  up         Apply every pending migration. One transaction per step.
  status     Report the current version, the pending set and the supported window.
  version    Print the current schema version and exit.
  verify     Confirm every applied migration still matches this build.

Flags:
  -dsn string        Postgres DSN without a password. Falls back to SP_DATABASE_URL.
                      Required: with neither named, this tool refuses rather than
                      guessing a database from the values compiled into it.
  -password string   Database password. SP_DATABASE_PASSWORD is preferred; a flag
                      argument is visible to anything that can list processes.
  -json              Emit machine-readable output.
  -dry-run           Print the plan and change nothing.
  -timeout duration  Overall timeout (default 10m).
  -version

Expand/contract discipline: an expand step ships alone, is exercised against a
live database, and only then is a later release allowed to ship the matching
contract step. This runner will not merge them for you.
`)
}
