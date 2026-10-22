package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/command"
	"github.com/Sergey-Bar/vision/internal/operatormetrics"
	"github.com/Sergey-Bar/vision/migrations"
)

// check is one preflight assertion. A preflight that prints "looks fine" without
// saying what it looked at is not a preflight; every line names a condition and
// a remedy.
type check struct {
	Name   string
	Detail string
	OK     bool
	// Fatal marks a condition that must stop an install. Advisory checks are
	// reported and the command continues, because a pilot site with 18% free
	// disk is still a site you can install onto and warn about.
	Fatal  bool
	Remedy string
}

type checkList []check

func (c checkList) add(name, detail string, ok bool, fatal bool, remedy string) checkList {
	return append(c, check{Name: name, Detail: detail, OK: ok, Fatal: fatal, Remedy: remedy})
}

func (c checkList) failed() int {
	n := 0
	for _, item := range c {
		if !item.OK && item.Fatal {
			n++
		}
	}
	return n
}

func (c checkList) warned() int {
	n := 0
	for _, item := range c {
		if !item.OK && !item.Fatal {
			n++
		}
	}
	return n
}

// preflightCommand answers one question: may this machine run this release?
//
// It is the first thing an installer runs and the last thing a support engineer
// asks for. Every check is a hard gate with a stated remedy, because an
// air-gapped site cannot go and look something up.
func preflightCommand() *command.Command {
	var (
		evidenceRoot string
		dataRoot     string
		requireDB    bool
	)
	return &command.Command{
		Name:    "preflight",
		Summary: "Check that this machine can run this release",
		Usage: `preflight — assert the conditions an install depends on.

Runs read-only. Changes nothing, contacts nothing.`,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&evidenceRoot, "evidence-root", "", "Evidence directory to check. Defaults to $SP_EVIDENCE_ROOT.")
			fs.StringVar(&dataRoot, "data-root", "", "Data directory to check. Defaults to $SP_DATA_ROOT.")
			fs.BoolVar(&requireDB, "require-db", true, "Fail if the database is unreachable.")
		},
		Run: func(ctx context.Context, env *command.Env, args []string) error {
			if len(args) > 0 {
				return printUsageError("preflight takes no positional arguments")
			}
			checks := runPreflight(ctx, evidenceRoot, dataRoot, requireDB)

			width := 0
			for _, item := range checks {
				if len(item.Name) > width {
					width = len(item.Name)
				}
			}
			for _, item := range checks {
				mark := "ok  "
				severity := ""
				if !item.OK {
					mark = "FAIL"
					if !item.Fatal {
						mark = "warn"
					}
					severity = " — " + item.Remedy
				}
				fmt.Fprintf(env.Stdout, "[%s] %-*s  %s%s\n", mark, width, item.Name, item.Detail, severity)
			}
			fmt.Fprintf(env.Stdout, "\n%d fatal, %d advisory\n", checks.failed(), checks.warned())
			if checks.failed() > 0 {
				return command.Refuse("%d fatal precondition(s) not met", checks.failed())
			}
			return nil
		},
	}
}

func runPreflight(ctx context.Context, evidenceRoot, dataRoot string, requireDB bool) checkList {
	var checks checkList

	compat, err := migrations.ReleaseCompatibility()
	if err != nil {
		checks = checks.add("contracts", err.Error(), false, true, "reinstall the release; the embedded manifest is unreadable")
	} else {
		checks = checks.add("contracts",
			fmt.Sprintf("%s, schema window [%d, %d]", compat.ContractsVersion, compat.MinSchemaVersion, compat.MaxSchemaVersion),
			true, false, "")
	}

	latest, err := migrations.LatestVersion()
	if err != nil {
		checks = checks.add("migrations", err.Error(), false, true, "reinstall the release; the embedded migration set is invalid")
	} else {
		target, _ := migrations.ReleaseCompatibility()
		checks = checks.add("migrations",
			fmt.Sprintf("%d migration(s) embedded, target schema version %d", latest, target.MaxSchemaVersion),
			true, false, "")
	}

	checks = checks.add("platform",
		fmt.Sprintf("%s/%s, %d CPUs", runtime.GOOS, runtime.GOARCH, runtime.NumCPU()),
		true, false, "")

	evRoot := firstNonEmpty(evidenceRoot, os.Getenv("SP_EVIDENCE_ROOT"))
	if evRoot == "" {
		checks = checks.add("evidence root", "not configured", false, true,
			"set paths.evidence in the site configuration, or pass --evidence-root")
	} else {
		checks = checks.add("evidence root", checkDirUsable(evRoot), true, true, "")
	}

	dataDir := firstNonEmpty(dataRoot, os.Getenv("SP_DATA_ROOT"))
	if dataDir != "" {
		checks = checks.add("data root", checkDirUsable(dataDir), true, true, "")
	}

	if !requireDB {
		return checks
	}
	window, err := schemaWindow(ctx)
	switch {
	case err != nil:
		checks = checks.add("database", err.Error(), false, true,
			"check the host, port and credentials in the site configuration")
	case strings.Contains(window, "OUT OF RANGE"):
		checks = checks.add("database", window, false, true,
			"upgrade or roll back the binary; do not force a migration across the window")
	default:
		checks = checks.add("database", window, true, false, "")
	}
	return checks
}

func checkDirUsable(dir string) string {
	info, err := os.Stat(dir)
	switch {
	case err != nil:
		return fmt.Sprintf("%s: not present (%v)", dir, err)
	case !info.IsDir():
		return fmt.Sprintf("%s: exists but is not a directory", dir)
	default:
		probe := filepath.Join(dir, ".preflight-write-probe")
		if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
			return fmt.Sprintf("%s: exists but is not writable by this account (%v)", dir, err)
		}
		_ = os.Remove(probe)
		return fmt.Sprintf("%s: present and writable", dir)
	}
}

// doctorCommand reports the live state of a running install.
func doctorCommand() *command.Command {
	return &command.Command{
		Name:    "doctor",
		Summary: "Report the live state of this install",
		Usage: `doctor — read-only diagnostics for a running install.

Answers "where did the chain break" without changing anything and without
requiring the AI plane to be up.`,
		Run: func(ctx context.Context, env *command.Env, args []string) error {
			fmt.Fprintf(env.Stdout, "security-platform doctor\n")
			fmt.Fprintf(env.Stdout, "  binary      : %s/%s\n", runtime.GOOS, runtime.GOARCH)
			if window, err := schemaWindow(ctx); err == nil {
				fmt.Fprintf(env.Stdout, "  %s\n", window)
			} else {
				fmt.Fprintf(env.Stdout, "  database    : unreachable (%v)\n", err)
			}
			fmt.Fprintf(env.Stdout, "  ai plane    : not consulted. AI availability is not an SLO and is\n")
			fmt.Fprintf(env.Stdout, "                never a reason the core is unhealthy.\n")
			fmt.Fprintln(env.Stdout)
			fmt.Fprintf(env.Stdout, "  backlog, drift, dead letters and checkpoint age: see the\n")
			fmt.Fprintf(env.Stdout, "  /v1/ops endpoints and the metrics scrape endpoint.\n")
			// The rollup's freshness, because a stale report is not a wrong
			// report and the difference is worth a line. The database may be
			// unreachable -- the schema check above already said so -- and then
			// the freshness is unknown, which is said.
			fmt.Fprintln(env.Stdout)
			if pool, err := openPool(ctx); err == nil {
				defer pool.Close()
				reader := &operatormetrics.Reader{Pool: pool}
				freshest, err := reader.Freshest(ctx, configuredTenant())
				if err != nil {
					fmt.Fprintf(env.Stdout, "  rollup      : unknown (%v)\n", err)
				} else {
					fmt.Fprintf(env.Stdout, "  rollup      : %s\n",
						analyticsDoctorLine(freshest, time.Now().UTC()))
				}
			}
			return nil
		},
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
