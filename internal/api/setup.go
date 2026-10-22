package api

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/identity"
)

// The setup path.
//
// # What this surface is for
//
// "Plug and play" is a promise about what happens on the FIRST day, and a
// promise nobody can check is a promise nobody keeps. So the product reports
// its own readiness as a list of STEPS, each one answered by the product
// rather than by the console.
//
// The steps are ordered because the order is the dependency: a site with a
// camera and no schema cannot record anything, and a site with a schema and
// no site has nothing to record for.
//
// # Every step names what to do next
//
// A step that is not done and does not say what to do is a complaint. Each
// unfinished step carries the ONE action that would finish it, in the
// operator's terms rather than the product's — because "run the migrations"
// is an instruction and "SP_RECORDING_ROOT is not set" is a diagnosis.

// SetupStep is one step of the setup path.
type SetupStep struct {
	// ID names the step, so a console can link to it and a test can
	// assert on it.
	ID string `json:"id"`
	// Title is what the step is, in the operator's words.
	Title string `json:"title"`
	// Done says whether the step is finished.
	Done bool `json:"done"`
	// Detail says what the product found, whatever the answer.
	Detail string `json:"detail"`
	// Action says what to do next, and it is empty when the step is done.
	Action string `json:"action,omitempty"`
}

// handleSetup answers: what is left to do before this site records anything?
//
// It is a READ of the product's own state, and it reads the product's own
// tables: the migration ledger, the sites, the device registry and the
// provider policy. It does not consult the console, the browser, or a
// checklist somebody wrote down.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	ctx := r.Context()
	steps := make([]SetupStep, 0, 6)

	// 1. The database.
	steps = append(steps, s.stepDatabase(ctx))

	// 2. The schema.
	steps = append(steps, s.stepSchema(ctx, d))

	// 3. The site.
	steps = append(steps, s.stepSite(ctx, p))

	// 4. The devices.
	steps = append(steps, s.stepDevices(ctx, p, d))

	// 5. The recorder.
	steps = append(steps, s.stepRecorder(d))

	// 6. The brain.
	steps = append(steps, s.stepChain(ctx, p, d))

	done := 0
	for _, step := range steps {
		if step.Done {
			done++
		}
	}
	// The next step is the FIRST one that is not done, because the steps
	// are ordered by dependency and the first unfinished one is the one
	// that is blocking the rest.
	next := ""
	for _, step := range steps {
		if !step.Done {
			next = step.ID
			break
		}
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"siteId":     firstSite(p),
		"steps":      steps,
		"doneCount":  done,
		"totalCount": len(steps),
		// "ready" is a claim about the PRODUCT and not about the
		// console: it says the product can record, and the console
		// reads it.
		"ready": done == len(steps),
		"next":  next,
	})
}

// stepDatabase asks whether the database answers.
func (s *Server) stepDatabase(ctx context.Context) SetupStep {
	step := SetupStep{ID: "database", Title: "The database answers"}
	if s.Pool == nil {
		step.Detail = "This install has no database configured."
		step.Action = "Set SP_DATABASE_URL and SP_DATABASE_PASSWORD, and run security-migrate."
		return step
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.Pool.Ping(pingCtx); err != nil {
		step.Detail = "The database did not answer: " + err.Error()
		step.Action = "Check that Postgres is running and that SP_DATABASE_URL names it."
		return step
	}
	step.Done = true
	step.Detail = "The database answered."
	return step
}

// stepSchema asks whether the migrations are applied.
//
// It compares the database's version with the SHIPPED one, and it says which
// it is: a site running an older schema is a site whose product refuses
// work, and the refusal is far easier to fix when it is named.
func (s *Server) stepSchema(ctx context.Context, d Deps) SetupStep {
	step := SetupStep{ID: "schema", Title: "The schema is current"}
	if s.Pool == nil {
		step.Detail = "There is no database to read the schema version from."
		return step
	}
	var applied int
	if err := s.Pool.QueryRow(ctx,
		`SELECT COALESCE(max(version), 0) FROM schema_migrations`).Scan(&applied); err != nil {
		step.Detail = "The migration ledger could not be read: " + err.Error()
		step.Action = "Run security-migrate up."
		return step
	}
	shipped := d.SchemaVersion
	if shipped <= 0 {
		shipped = applied
	}
	if applied >= shipped {
		step.Done = true
		step.Detail = "The schema is at version " + itoa(int64(applied)) + "."
		return step
	}
	step.Detail = "The schema is at version " + itoa(int64(applied)) +
		" and this build ships version " + itoa(int64(shipped)) + "."
	step.Action = "Run security-migrate up."
	return step
}

// stepSite asks whether the tenant has a site.
//
// A site is the scope everything else is written in: a camera, a recording, a
// policy. A tenant with no site has nothing to record FOR.
func (s *Server) stepSite(ctx context.Context, p identity.Principal) SetupStep {
	step := SetupStep{ID: "site", Title: "The site exists"}
	if s.Pool == nil {
		step.Detail = "There is no database to read the sites from."
		return step
	}
	var n int
	err := s.Pool.WithSites(p.TenantID, "setup", p.SiteScope).QueryRow(ctx,
		`SELECT count(*) FROM sites WHERE tenant_id = $1`, p.TenantID).Scan(&n)
	if err != nil {
		step.Detail = "The sites could not be read: " + err.Error()
		return step
	}
	if n == 0 {
		step.Detail = "This tenant has no site. Everything the product records belongs to one."
		step.Action = "Run security-seed, or add a site in the console."
		return step
	}
	step.Done = true
	step.Detail = "The tenant has " + itoa(int64(n)) + " site(s)."
	return step
}

// stepDevices asks whether anything is registered.
//
// A registry with no device is not a fault: it is a site that has not been
// told about its cameras yet. The step says so, and it says what to do.
func (s *Server) stepDevices(ctx context.Context, p identity.Principal, d Deps) SetupStep {
	step := SetupStep{ID: "devices", Title: "A camera is registered"}
	if d.Devices == nil {
		step.Detail = "This install has no device registry configured."
		step.Action = "Configure the device registry, or install a build that has one."
		return step
	}
	devices, err := d.Devices.List(ctx, p.TenantID, p.SiteScope)
	if err != nil {
		step.Detail = "The device registry could not be read: " + err.Error()
		return step
	}
	if len(devices) == 0 {
		step.Detail = "No device is registered yet. A camera that has not been told about is " +
			"a camera nobody is recording."
		step.Action = "Add the camera on the Devices page, or run the discovery scan."
		return step
	}
	probed := 0
	for _, dev := range devices {
		if dev.Manifest != nil {
			probed++
		}
	}
	step.Done = true
	step.Detail = itoa(int64(len(devices))) + " device(s) registered, " +
		itoa(int64(probed)) + " of them measured."
	return step
}

// stepRecorder asks whether the recorder is configured.
//
// The recorder is a SEPARATE binary with its own configuration, and the core
// cannot see the recorder's environment. So the core is told, and the
// answer it reports is the one the operator gave it.
//
// A recorder that is NOT configured is a site that records nothing, and the
// site says so on the front door rather than in a log.
func (s *Server) stepRecorder(d Deps) SetupStep {
	step := SetupStep{ID: "recorder", Title: "The recorder is configured"}
	if d.RecorderConfigured == nil {
		step.Detail = "This install does not say whether a recorder is configured."
		step.Action = "Run the core with the recorder configured, or check that it shares the core environment."
		return step
	}
	if !d.RecorderConfigured() {
		step.Detail = "No recorder is configured: SP_RECORDING_ROOT and SP_RECORDER_SOURCES are " +
			"not both set, so nothing is being written to disk."
		step.Action = "Set SP_RECORDING_ROOT and SP_RECORDER_SOURCES, and run security-recorder."
		return step
	}
	step.Done = true
	step.Detail = "A recorder is configured and has somewhere to write."
	return step
}

// stepChain asks whether the site has a brain.
//
// A site with no provider chain runs the local runtime, which is a supported
// configuration — so this step is about the CHAIN and not about a model.
func (s *Server) stepChain(ctx context.Context, p identity.Principal, d Deps) SetupStep {
	step := SetupStep{ID: "chain", Title: "The site has a provider chain"}
	if d.AIChain == nil {
		step.Detail = "This install has no AI provider chain configured."
		step.Action = "Configure the provider chain, or leave it to the local runtime."
		return step
	}
	policy, err := d.AIChain.Policy(ctx, p.TenantID, firstSite(p))
	if err != nil {
		step.Detail = "The provider policy could not be read: " + err.Error()
		step.Action = "Check that the database is reachable and that the migration is applied."
		return step
	}
	if len(policy.Providers) == 0 {
		step.Detail = "The site's chain is empty. The local runtime answers instead, which is " +
			"supported — a named chain is what lets a site fall back."
		step.Action = "Name the site's providers in ai_policies, or leave it to the local runtime."
		return step
	}
	step.Done = true
	step.Detail = "The site has " + itoa(int64(len(policy.Providers))) + " provider(s) in its chain."
	return step
}

// RecorderConfigured reads the recorder's configuration from the environment.
//
// It is exported because the CORE is what the operator asks — the recorder is
// a separate binary with its own environment and the core cannot see it — and
// because a function the console cannot reach is a function nobody asks.
//
// The question is about the recorder, so the answer comes from the recorder's
// own configuration rather than from a row the core writes about it.
func RecorderConfigured() bool {
	root := strings.TrimSpace(os.Getenv("SP_RECORDING_ROOT"))
	sources := strings.TrimSpace(os.Getenv("SP_RECORDER_SOURCES"))
	return root != "" && sources != ""
}
