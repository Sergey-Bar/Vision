package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/command"
	"github.com/Sergey-Bar/Ai-Captain/internal/milestone"
)

// adapterProbeCommand is the plan's P0.5.
//
// It exists because "no Milestone server has ever been reachable from this
// codebase" is only useful if something turns it into confirmed-or-contradicted,
// and a person clicking through twenty endpoints with curl is a person who will
// get to fourteen of them and stop.
//
// It does not promote anything. A probe that marked its own homework would make
// the Unverified flag a claim the tool makes about itself, and this command
// prints exactly what a human has to read before they change a flag by hand.
func adapterProbeCommand() *command.Command {
	return &command.Command{
		Name:    "adapter-probe",
		Summary: "Reach every vendor route and report which assumptions hold",
		Usage: `adapter-probe — turn the vendor's assumed API into confirmed-or-contradicted.

Reaches every route this build assumes exists and reports, per route:

  confirmed     it answered in the declared shape
  contradicted  it answered, but not in the shape the adapter accepts
  unreachable   we could not get there, or could not authenticate
  skipped       a write-side route, which a read probe will not touch

This command NEVER changes a route's Unverified flag. The flag is a claim a
human makes after reading this report, and a probe that promoted its own
findings would make the flag a statement the tool makes about itself.

  --base-url    the vendor's API root
  --token       bearer credential, from the secret store
  --param       extra query parameter, name=value, repeatable. Use it to ask a
                question the documents state in prose and this build cannot check:
                  --param "analyticData.object.value=contains:'ABC123'"
                A filter that is documented but not honoured is a finding, and
                this is the only way to turn that sentence into evidence.
  --clock       also sample the vendor's clock and report the drift
  --timeout     per-route budget
  --json        machine-readable only

Examples:
  securityctl adapter-probe --base-url https://milestone.example:8080 --token "$SP_MILESTONE_TOKEN"
  securityctl adapter-probe --base-url "$MS" --token "$T" --clock --json
  securityctl adapter-probe --base-url "$MS" --token "$T" \
    --param "analyticData.object.value=contains:'ABC123'" --json`,
		Run: runAdapterProbe,
	}
}

func runAdapterProbe(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("adapter-probe", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	var (
		baseURL   = fs.String("base-url", "", "The vendor's API root.")
		token     = fs.String("token", "", "Bearer credential. Read from the environment or a secret store, never a file.")
		withClock = fs.Bool("clock", false, "Also sample the vendor's clock and report the drift.")
		timeout   = fs.Duration("timeout", 20*time.Second, "Per-route budget.")
		asJSON    = fs.Bool("json", false, "Machine-readable only.")
		params    = map[string]string{}
	)
	fs.Func("param", "Extra query parameter, name=value. Repeatable.", func(v string) error {
		name, value, split := strings.Cut(v, "=")
		name = strings.TrimSpace(name)
		if !split || name == "" {
			return fmt.Errorf("--param wants name=value; got %q", v)
		}
		params[name] = value
		return nil
	})
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return &command.UsageError{Err: fmt.Errorf("adapter-probe takes no positional arguments; got %q", fs.Arg(0))}
	}
	if strings.TrimSpace(*baseURL) == "" {
		return &command.UsageError{Err: fmt.Errorf("--base-url is required; there is no default, because a default " +
			"here is a probe pointed at somebody else's server by a typo")}
	}

	transport, err := milestone.NewHTTPTransport(*baseURL, *token, *timeout)
	if err != nil {
		return fmt.Errorf("adapter-probe: %w", err)
	}

	report := milestone.Probe(ctx, transport, milestone.ProbeOptions{
		Endpoint:     *baseURL,
		ItemLimit:    1,
		IncludeClock: *withClock,
		Params:       params,
	})

	if *withClock {
		if p, ok := transport.(milestone.ClockProbe); ok {
			drift, derr := milestone.SampleDrift(ctx, p, func() time.Time { return time.Now().UTC() })
			if derr != nil {
				report.ClockDrift = milestone.Drift{}
			} else {
				report.ClockDrift = drift
			}
		}
	}

	if *asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		printProbeReport(env, report)
	}

	// The exit code is the tool's whole value: a probe that prints a table and
	// exits zero is a thing a script calls after a problem, which is exactly when
	// it needs to say something.
	if report.Summary.Contradicted > 0 || report.Summary.Unreachable > 0 {
		return command.Refuse("adapter-probe: %d route(s) contradicted and %d were unreachable; "+
			"the remaining %d are not a clean bill of health",
			report.Summary.Contradicted, report.Summary.Unreachable, report.Summary.Confirmed)
	}
	return nil
}

func printProbeReport(env *command.Env, r milestone.ProbeReport) {
	w := env.Stdout
	fmt.Fprintf(w, "adapter probe — %s at %s\n", r.Endpoint, r.ProbedAt.Format(time.RFC3339))
	for _, res := range r.Results {
		fmt.Fprintf(w, "  %s\n", res)
	}

	s := r.Summary
	fmt.Fprintf(w, "\n%d route(s): %d confirmed, %d contradicted, %d unreachable, %d skipped\n",
		s.Total, s.Confirmed, s.Contradicted, s.Unreachable, s.Skipped)

	if len(r.Capabilities) > 0 {
		fmt.Fprintf(w, "capabilities reported by the deployment: %s\n", strings.Join(r.Capabilities, ", "))
	}
	if r.ClockDrift.Offset != 0 {
		fmt.Fprintf(w, "clock drift: %v (band %s)\n", r.ClockDrift.Offset.Round(time.Millisecond), r.ClockDrift.Band())
	}

	fmt.Fprintf(w, "\n%d route(s) are still marked Unverified. This command does not change that;\n", s.StillUnverified)
	fmt.Fprintf(w, "it prints what a human has to read before changing a flag by hand.\n")
	if s.Flaggable > 0 {
		fmt.Fprintf(w, "%d confirmed route(s) are marked Unverified and are candidates for promotion.\n", s.Flaggable)
		fmt.Fprintf(w, "Promotion is a commit to routes.go. Read the field names above against the\n")
		fmt.Fprintf(w, "adapter's expectations first: a route that returns the wrong records in the\n")
		fmt.Fprintf(w, "right shape is the failure this tool is most likely to report as a success.\n")
	}
	if s.Confirmed == 0 {
		fmt.Fprintf(w, "\nNothing was confirmed. Nothing here says the adapter works.\n")
	}
}
