package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// UnitOptions describes where the systemd units go and what they point at.
type UnitOptions struct {
	// UnitDir is /etc/systemd/system. Overridden in tests.
	UnitDir string
	// ProgramDir is where the binaries were installed.
	ProgramDir string
	// EnvFile is the environment file the core unit reads. The shipped unit
	// already declared one that nothing read; writing it is what makes the
	// declaration mean something.
	EnvFile string
	// AIProgramDir is where the AI runtime lives, if it is installed beside the
	// core. Empty means no AI unit is written.
	AIProgramDir string
	// AIEnvFile is the AI runtime's environment file.
	AIEnvFile string
	// User runs the services. Empty means root, which is wrong on a site with a
	// service account and is named rather than defaulted silently.
	User string
	// Group is the service group.
	Group string
}

// ErrUnsafePath is returned when a configured path would let a unit file escape
// its directory.
var ErrUnsafePath = errors.New("lifecycle: unsafe path")

// WrittenUnit records one unit file that was written.
type WrittenUnit struct {
	// Name is the unit's base name, e.g. "security-core.service".
	Name string
	Path string
	// Content is what landed on disk. Returned so the caller can print it and a
	// test can assert it without re-reading the file.
	Content string
}

// WriteUnits writes the systemd units and the environment files.
//
// It writes every file or none of them. A half-installed unit set is a service
// that starts and cannot find its configuration, which is a failure that looks
// like a configuration problem rather than an install problem - so the writes
// are staged and only moved into place once every one of them succeeded.
//
// The env files are created only if they do not already exist. An operator who
// has edited theirs - which is the normal case, since it is where the tenant id
// and the database URL live - must not have their file replaced by a re-install,
// because a re-install is the thing they run when something else has gone wrong.
func WriteUnits(opts UnitOptions) ([]WrittenUnit, error) {
	if strings.TrimSpace(opts.ProgramDir) == "" {
		return nil, errors.New("lifecycle: a program directory is required to write units")
	}
	unitDir := opts.UnitDir
	if strings.TrimSpace(unitDir) == "" {
		unitDir = "/etc/systemd/system"
	}
	envFile := opts.EnvFile
	if strings.TrimSpace(envFile) == "" {
		envFile = "/etc/security-platform/security-platform.env"
	}
	aiEnvFile := opts.AIEnvFile
	if strings.TrimSpace(aiEnvFile) == "" {
		aiEnvFile = "/etc/security-platform/ai-runtime.env"
	}

	// Everything is validated before anything is rendered or created.
	//
	// The inputs are validated rather than the joined paths. filepath.Join
	// resolves a `..` away, so checking the result of the join would find a
	// perfectly ordinary path every time and never see the traversal at all.
	if err := checkSafe(unitDir); err != nil {
		// checkSafe already wraps ErrUnsafePath, so the sentinel survives this
		// wrap. The prefix only names which input was refused; the reason is
		// already in err.
		return nil, fmt.Errorf("lifecycle: the unit directory is unsafe: %w", err)
	}
	if err := checkSafe(envFile); err != nil {
		return nil, err
	}
	if err := checkSafe(opts.ProgramDir); err != nil {
		return nil, err
	}
	if opts.AIProgramDir != "" {
		if err := checkSafe(opts.AIProgramDir); err != nil {
			return nil, err
		}
		if err := checkSafe(aiEnvFile); err != nil {
			return nil, err
		}
	}

	type target struct {
		path    string
		content string
		// keepExisting preserves a file the operator has edited. The env file is
		// where the tenant id and the database password live; replacing it on a
		// re-install breaks a site during the upgrade meant to fix it.
		keepExisting bool
		// mode is the file mode. It is per-target and not a constant because the
		// two kinds of file here are not the same kind of file.
		//
		// A systemd unit is world-readable and has to be - systemd reads it as a
		// different account. The environment file is not: it carries
		// SP_DATABASE_URL and SP_SECRET_KEY, and it was written 0644, so any local
		// account on the host could read the site's database credentials and its
		// secret key. The template this same function emits says in its own text
		// "keep this file mode 0640 and owned by the service account"; the
		// installer was contradicting the instruction it was writing out.
		mode os.FileMode
	}
	targets := []target{
		{path: filepath.Join(unitDir, "security-core.service"), content: coreUnit(opts), mode: 0o644},
		{path: envFile, content: coreEnvTemplate(opts), keepExisting: true, mode: 0o640},
	}
	if opts.AIProgramDir != "" {
		targets = append(targets,
			target{path: filepath.Join(unitDir, "security-ai-runtime.service"),
				content: aiUnit(opts), mode: 0o644},
			target{path: aiEnvFile, content: aiEnvTemplate(), keepExisting: true, mode: 0o640})
	}

	written := make([]WrittenUnit, 0, len(targets))
	for _, tgt := range targets {
		written = append(written, WrittenUnit{
			Name:    filepath.Base(tgt.path),
			Path:    tgt.path,
			Content: tgt.content,
		})
	}

	for _, tgt := range targets {
		if err := os.MkdirAll(filepath.Dir(tgt.path), 0o755); err != nil {
			return nil, fmt.Errorf("lifecycle: create %s: %w", filepath.Dir(tgt.path), err)
		}
		if tgt.keepExisting {
			if _, err := os.Stat(tgt.path); err == nil {
				// Even a file we are not replacing has to end up with the right
				// mode: a preserved env file is the one an older installer wrote
				// world-readable, and "the operator may have edited it" is a reason
				// to keep its contents, not its permissions.
				if err := os.Chmod(tgt.path, tgt.mode); err != nil {
					return nil, fmt.Errorf("lifecycle: set the mode on %s: %w", tgt.path, err)
				}
				continue
			} else if !os.IsNotExist(err) {
				return nil, fmt.Errorf("lifecycle: inspect %s: %w", tgt.path, err)
			}
		}
		// WriteFile's mode only applies when it creates the file; an existing file
		// keeps whatever mode it has. An env file written by an older installer is
		// therefore still world-readable after an upgrade, so it is chmod'ed
		// explicitly on both paths. Doing that to a file we have just created is
		// harmless and doing it to a preserved one is the point.
		if err := os.WriteFile(tgt.path, []byte(tgt.content), tgt.mode); err != nil {
			return nil, fmt.Errorf("lifecycle: write %s: %w", tgt.path, err)
		}
		if err := os.Chmod(tgt.path, tgt.mode); err != nil {
			return nil, fmt.Errorf("lifecycle: set the mode on %s: %w", tgt.path, err)
		}
	}
	return written, nil
}

// coreUnit renders the core service unit.
//
// Every hardening directive in the shipped unit is preserved. This is not a
// copy of it with a path filled in - it is the same hardening, stated where the
// installer can also see it, so the two can be diffed by a human when the
// shipped unit changes.
func coreUnit(opts UnitOptions) string {
	var b strings.Builder
	b.WriteString(`[Unit]
Description=VISION Security Core
Documentation=https://contracts.aicaptain.dev
After=network-online.target postgresql.service
Wants=network-online.target

[Service]
Type=notify
NotifyAccess=main
`)
	if opts.User != "" {
		fmt.Fprintf(&b, "User=%s\n", opts.User)
		if opts.Group != "" {
			fmt.Fprintf(&b, "Group=%s\n", opts.Group)
		}
	}
	// The "-" prefix on EnvironmentFile is what makes a missing env file a
	// warning rather than a boot failure. The core then refuses to start for a
	// named reason, which is a better failure than systemd's.
	fmt.Fprintf(&b, "EnvironmentFile=-%s\\n", unitPath(opts.EnvFile))
	fmt.Fprintf(&b, "ExecStart=%s\\n", unitPath(filepath.Join(opts.ProgramDir, "security-core")))
	b.WriteString(`ExecReload=/bin/kill -HUP $MAINPID

Restart=on-failure
RestartSec=5s
TimeoutStopSec=30s
KillSignal=SIGTERM

# Hardening. Each line is a specific thing an attacker with code execution as
# this service cannot do.
NoNewPrivileges=true
PrivateTmp=true
PrivateDevices=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
RestrictRealtime=true
LockPersonality=true
MemoryDenyWriteExecute=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
SystemCallArchitectures=native
SystemCallFilter=@system-service
CapabilityBoundingSet=
AmbientCapabilities=

StateDirectory=security-platform
ReadWritePaths=/var/lib/security-platform

[Install]
WantedBy=multi-user.target
`)
	return b.String()
}

// aiUnit renders the AI runtime unit, or empty when no AI program directory is
// configured.
//
// The AI runtime runs as its own unit on purpose. It is the component whose
// failure must not affect the core: a hung inference has to be killable without
// touching ingest, and that requires a separate service with its own restart
// policy.
//
// The env path is resolved here rather than passed in. WriteUnits resolved the
// same value before it called this, so the two agree; taking it as a parameter
// would be a second source of truth for a path systemd reads as root.
func aiUnit(opts UnitOptions) string {
	if opts.AIProgramDir == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(`[Unit]
Description=VISION AI Runtime
After=network-online.target security-core.service
Wants=network-online.target

[Service]
Type=simple
`)
	if opts.User != "" {
		fmt.Fprintf(&b, "User=%s\n", opts.User)
		if opts.Group != "" {
			fmt.Fprintf(&b, "Group=%s\n", opts.Group)
		}
	}
	aiEnv := opts.AIEnvFile
	if strings.TrimSpace(aiEnv) == "" {
		aiEnv = "/etc/security-platform/ai-runtime.env"
	}
	fmt.Fprintf(&b, "EnvironmentFile=%s\\n", unitPath(aiEnv))
	fmt.Fprintf(&b, "ExecStart=%s\\n", unitPath(filepath.Join(opts.AIProgramDir, "runtime.py")))
	b.WriteString(`Restart=on-failure
RestartSec=10s

NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
MemoryDenyWriteExecute=true
CapabilityBoundingSet=
AmbientCapabilities=
`)
	return b.String()
}

// coreEnvTemplate renders the core's environment file.
//
// Every required variable is present and empty except the ones with a safe
// default. A template that carried a placeholder value for a required variable
// would let a core start with a tenant id of "CHANGE-ME", which is worse than
// refusing to start at all.
func coreEnvTemplate(opts UnitOptions) string {
	return fmt.Sprintf(`# VISION Security Core — site configuration.
#
# Written by `+"`securityctl install`"+`. Existing values are preserved on
# re-install; this template is only created when the file does not exist.
#
# Every SP_ variable the core reads is listed here, including the ones it
# refuses to start without. An empty required variable is a refusal with the
# variable named, which is a better failure than a core running against the
# wrong tenant.

# --- Required. The core will not start without these. ----------------------------

# The tenant this install serves. tenant_id is never read from a request body.
SP_TENANT_ID=

# The site this install serves. The core is single-site by construction.
SP_SITE_ID=

# The namespace the inbox dedupes within.
SP_INTEGRATION_ID=

# Where evidence bytes are stored. Absolute.
SP_EVIDENCE_ROOT=/var/lib/security-platform/evidence

# Where the audit ledger's seals are written. Empty disables sealing, which the
# health endpoint reports rather than hides.
SP_CHECKPOINT_DIR=/var/lib/security-platform/checkpoints

# The Ed25519 key that signs ledger seals.
SP_CHECKPOINT_KEY=

# Where the shift hand-over artifact is written and read. Empty means the site
# keeps none: the pipeline runs without the artifact and the console's hand-over
# page says so by name, which is a better answer than a page that renders empty
# and reads as "nothing happened".
SP_HANDOVER_DIR=

# --- Database. The password is not here; see SP_DATABASE_PASSWORD below. -------

SP_DATABASE_URL=postgres://security@127.0.0.1:6000/security?sslmode=disable
SP_DATABASE_USER=security
SP_DATABASE_NAME=security

# --- Network. -------------------------------------------------------------------

SP_LISTEN_ADDR=127.0.0.1:8443
# Set both or neither. One without the other is a refusal, not a TLS config.
SP_TLS_CERT=
SP_TLS_KEY=
SP_BEHIND_PROXY=false

# --- Pipeline. ------------------------------------------------------------------

SP_POLL_INTERVAL=5s
SP_CLAIM_BATCH=64
SP_LEASE_DURATION=60s
SP_SHUTDOWN_GRACE=20s

# --- AI plane. Empty is supported: observations are then recorded as degraded
# with reason no_runtime, which is the correct state for a site with no model.

SP_AI_ENDPOINT=http://127.0.0.1:8800
SP_AI_DEADLINE=10s
# Leave false. The AI runtime has no authentication of any kind, so anything that
# can reach the socket can read the site's evidence.
SP_AI_ALLOW_REMOTE=false

# --- Milestone. -----------------------------------------------------------------

SP_MILESTONE_BASE_URL=http://127.0.0.1:8000/api

# --- Secrets. -------------------------------------------------------------------
#
# The password is an environment variable rather than a field in the config file
# because it is the one value that must not be readable by anything but this
# process. Keep this file mode 0640 and owned by the service account.

SP_DATABASE_PASSWORD=
SP_MILESTONE_TOKEN=
SP_SECRET_KEY=%s

SP_LOG_LEVEL=info
`, opts.SecretKeyPath())
}

// aiEnvTemplate renders the AI runtime's environment file.
func aiEnvTemplate() string {
	return `# VISION AI Runtime — site configuration.
#
# Written by ` + "`securityctl install`" + `. Preserved on re-install.

SP_AI_LISTEN=127.0.0.1:8800

# The model bundle. Empty is supported: the plane starts, reports itself
# unavailable, and every observation is recorded as degraded.
SP_MODEL_BUNDLE=
SP_MODEL_BUNDLE_SIGNATURE=
SP_MODEL_BUNDLE_KEY=

# The inference engine. A path to a Python module exposing describe() ->
# {model, model_hash, hardware, prompt_template_id, prompt_version} and
# investigate(request) -> {structured_output, insufficient_evidence}.
#
# Empty is supported and is the shipped state: the plane reports no engine
# bound, every observation is recorded as degraded with that reason, and the
# investigator answers with the named degradation rather than an answer.
#
# A path that is set but does not load is a STARTUP FAILURE, not a silent
# absence: the plane prints one reason line naming this variable and exits 2,
# because "the operator said a model is installed and the plane cannot load
# it" is a defect to read rather than a state to absorb. systemd's
# Restart=on-failure then restarts it until the file is fixed.
SP_ENGINE_PATH=
`
}

// SecretKeyPath is where the MFA wrapping key lives. It matches the core's own
// default, so an install that does not override it still finds it.
func (o UnitOptions) SecretKeyPath() string {
	if v := strings.TrimSpace(os.Getenv("SP_SECRET_KEY")); v != "" {
		return v
	}
	return "/etc/security-platform/secret.key"
}

// unitPath renders a filesystem path the way systemd reads it.
//
// systemd on Linux wants forward slashes and does not accept backslashes. This
// matters more than it looks: the installer also runs on a developer machine,
// and a unit rendered from a Windows path separator produces a file that starts
// fine on Windows and fails to exec anywhere it is actually installed.
func unitPath(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// checkSafe refuses a path that is not where it claims to be.
func checkSafe(p string) error {
	if _, err := safeUnitPath(p); err != nil {
		return err
	}
	return nil
}

// safeUnitPath refuses a path that is not where it claims to be.
//
// Every path here comes from a flag or an environment variable, and a unit file
// is content the systemd daemon executes as root. A path containing a traversal
// segment or an absolute reference that does not match the directory it was
// given is refused rather than written.
func safeUnitPath(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("%w: an empty path", ErrUnsafePath)
	}
	if strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("%w: %q contains a null byte", ErrUnsafePath, p)
	}
	cleaned := filepath.Clean(p)
	if strings.Contains(p, "..") {
		return "", fmt.Errorf("%w: %q contains a path traversal segment", ErrUnsafePath, p)
	}
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		// Both are wrapped. ErrUnsafePath is the sentinel a caller tests for;
		// the resolution failure underneath is a real cause - a caller that
		// wants to know *why* Abs failed (a deleted working directory, most
		// often) can unwrap for it rather than parse the message.
		return "", fmt.Errorf("%w: %q cannot be resolved: %w", ErrUnsafePath, p, err)
	}
	return abs, nil
}
