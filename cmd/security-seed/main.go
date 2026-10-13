// Command security-seed builds a deterministic, populated site from the committed
// fixtures.
//
// # Why this program exists
//
// Until now the product could be started and could pass its gate, but there was
// no way to *see* it: a judge who ran `docker compose up` would have got an
// empty console and an empty acceptance report, and neither of those says
// anything about the product. This is the program that makes the demo's
// substance — a site with incidents, evidence with hashes, a reviewer's note, and
// an AI plane that is degraded on some rows and healthy on others.
//
// # Determinism is the requirement, not a nicety
//
// The same seed must produce the same incident ids on every run, or nothing
// downstream is citable and the golden tests cannot exist. Three things make that
// true here, and each one is a decision rather than a hope:
//
//   - The replay epoch is FIXED, and it is the same instant
//     `securityctl rules simulate` uses. A site seeded at a different wall-clock
//     instant than the one the activation digest was computed against would be a
//     site whose activation gate certified a different run.
//   - The adapter's clock is injected, so `received_at` — which is what the inbox
//     claims rows `ORDER BY received_at` — is a function of the fixture and not
//     of when the container started.
//   - Incident ids come from an INJECTED generator, because the default one is
//     `inc_<site>_<time.Now().UnixNano()>` and `CreateIncident` reads the wall
//     clock for it with no seam. The generator here is a counter, which is stable
//     exactly as long as the claim order is, and the claim order is
//     `ORDER BY received_at` over fixtures whose offsets are distinct.
//
// The seed prints the incident ids it produced. That list is what a golden test
// asserts against and what a demo transcript quotes.
//
// # It does not bypass a gate
//
// The rule bundle is stored and activated through `bundles.Service`, which means
// the simulate-digest gate runs. The seed acknowledges a digest computed from
// the report it built itself, in the same shape `internal/pipeline`'s test does —
// it does not write an `active = true` row behind the service's back, because a
// seed that bypassed activation would make the seeded site one the product could
// never have reached.
//
// # What it deliberately leaves out
//
// No camera. No VMS. No AI runtime. Every observation it writes is either
// attributed and healthy or explicitly degraded with a reason, and the degraded
// ones say which failure they are standing in for. A seed that quietly invented a
// model's opinion would be the one thing this repository exists not to do.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/clock"
	"github.com/Sergey-Bar/vision/internal/observability"
)

// replayEpoch is the wall-clock instant every fixture is rebased onto.
//
// It is 22:30 UTC on 1 March 2026, which is 00:30 in Asia/Jerusalem — after the
// `business-hours` schedule the shipped bundle names, so the one production rule
// in that bundle is reachable rather than dead. It is deliberately the SAME
// instant `securityctl rules simulate` uses, so the activation digest a human
// acknowledged describes the run the seed then reproduces.
//
// Fixed here rather than taken from a flag with a default, because a flag is a
// way for the determinism to be turned off by somebody who did not read this
// comment. `--epoch` exists and exists only so the drill can be re-run at another
// instant deliberately; the report prints which one was used.
var replayEpoch = time.Date(2026, 3, 1, 22, 30, 0, 0, time.UTC)

// options is the whole configuration surface. Every field is either a flag with a
// committed default or an environment variable the product already reads, and
// nothing is positional.
type options struct {
	tenant       string
	site         string
	timezone     string
	integration  string
	fixturesDir  string
	clipsDir     string
	bundlePath   string
	evidenceRoot string
	secretKey    string
	consoleTLS   string
	passphrase   string
	epoch        string
	json         bool
	reset        bool
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "security-seed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var o options
	fs := flag.NewFlagSet("security-seed", flag.ContinueOnError)
	fs.StringVar(&o.tenant, "tenant", envOr("SP_SEED_TENANT", "northwind"), "Tenant id to create.")
	fs.StringVar(&o.site, "site", envOr("SP_SEED_SITE", "north-gate"), "Site id to create.")
	fs.StringVar(&o.timezone, "timezone", envOr("SP_SEED_TIMEZONE", "Asia/Jerusalem"),
		"Site timezone. Schedules are evaluated in the site's zone, never the host's.")
	fs.StringVar(&o.integration, "integration", envOr("SP_SEED_INTEGRATION", "xprotect-north-gate"),
		"Integration id. This is the namespace the inbox dedupes within.")
	fs.StringVar(&o.fixturesDir, "fixtures", envOr("SP_SEED_FIXTURES", "testdata/fixtures"),
		"Committed source-event fixtures.")
	fs.StringVar(&o.clipsDir, "clips", envOr("SP_SEED_CLIPS", "poc/active-monitoring/testdata"),
		"Committed clips to store as evidence. The POC's testdata is the default because "+
			"the three named clips live there and they are committed fixtures of this repository.")
	fs.StringVar(&o.bundlePath, "bundle", envOr("SP_SEED_BUNDLE", "rules/builtin"),
		"Rule bundle directory or file to store and activate.")
	fs.StringVar(&o.evidenceRoot, "evidence-root", os.Getenv("SP_EVIDENCE_ROOT"),
		"Directory the evidence store writes into. Required, as it is for the core.")
	fs.StringVar(&o.secretKey, "secret-key", envOr("SP_SEED_SECRET_KEY", "/run/security-platform/secret.key"),
		"Where the MFA wrapping key is written. Refuses to overwrite an existing key.")
	fs.StringVar(&o.consoleTLS, "console-tls", envOr("SP_SEED_CONSOLE_TLS", "/run/security-platform/console"),
		"Directory the self-signed console certificate is written to.")
	fs.StringVar(&o.passphrase, "passphrase", os.Getenv("SP_SEED_PASSPHRASE"),
		"Passphrase for the seeded administrator. Printed in the report; a demo credential "+
			"with a stated value is not a secret, and a hidden one would be.")
	fs.StringVar(&o.epoch, "epoch", "",
		"Replay instant, RFC3339. Defaults to the fixed instant the activation digest assumes.")
	fs.BoolVar(&o.json, "json", false, "Print the summary as JSON and nothing else.")
	fs.BoolVar(&o.reset, "reset", false,
		"Delete this tenant's rows before seeding. A seed into a populated tenant is "+
			"refused rather than merged, because a half-reset site has two of everything.")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	s, err := newSeeder(ctx, o)
	if err != nil {
		return err
	}
	defer s.close()
	return s.run(ctx)
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func errf(format string, args ...any) error { return fmt.Errorf(format, args...) }

var errNoEvidenceRoot = errors.New("security-seed: --evidence-root (or SP_EVIDENCE_ROOT) is " +
	"required: the seed stores evidence bytes the console then serves, and a site whose " +
	"evidence root is a guess is a site that cannot find them again")

func clockPolicy() clock.Policy { return clock.DefaultPolicy() }

func newRegistry() *observability.Registry { return observability.NewRegistry() }

// absDir refuses a relative path for the two directories the seed writes into,
// for the same reason `config` refuses one for the evidence root: a relative path
// resolves against whatever the working directory happened to be, and a
// certificate written to the wrong directory is a certificate nobody trusts.
func absDir(path, what string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "" || clean == "." || !filepath.IsAbs(clean) {
		return "", errf("security-seed: %s must be an absolute path, got %q", what, path)
	}
	return clean, nil
}
