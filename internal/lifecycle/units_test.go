package lifecycle_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Sergey-Bar/Ai-Captain/internal/lifecycle"
)

// opts builds a UnitOptions rooted in a temporary directory.
func opts(t *testing.T) lifecycle.UnitOptions {
	t.Helper()
	root := t.TempDir()
	return lifecycle.UnitOptions{
		UnitDir:    filepath.Join(root, "units"),
		ProgramDir: filepath.Join(root, "program"),
		EnvFile:    filepath.Join(root, "etc", "security-platform.env"),
	}
}

func written(t *testing.T, opts lifecycle.UnitOptions, name string) lifecycle.WrittenUnit {
	t.Helper()
	units, err := lifecycle.WriteUnits(opts)
	if err != nil {
		t.Fatalf("WriteUnits: %v", err)
	}
	for _, u := range units {
		if u.Name == name {
			return u
		}
	}
	t.Fatalf("no unit named %q was written; wrote %v", name, units)
	return lifecycle.WrittenUnit{}
}

func TestTheCoreUnitIsWritten(t *testing.T) {
	o := opts(t)
	unit := written(t, o, "security-core.service")

	if unit.Path == "" || unit.Content == "" {
		t.Fatal("the unit has no path or no content")
	}
	body, err := os.ReadFile(unit.Path)
	if err != nil {
		t.Fatalf("the unit is not on disk: %v", err)
	}
	if string(body) != unit.Content {
		t.Error("what is on disk is not what was reported as written")
	}

	// It points at the program directory it was given, so an install into a
	// non-default location actually runs.
	exec := filepath.ToSlash(filepath.Join(o.ProgramDir, "security-core"))
	if !strings.Contains(unit.Content, "ExecStart="+exec) {
		t.Errorf("the unit does not exec %q:\n%s", exec, unit.Content)
	}

	// And it reads the env file the shipped unit already declared.
	if !strings.Contains(unit.Content, "EnvironmentFile=-"+filepath.ToSlash(o.EnvFile)) {
		t.Errorf("the unit does not read the environment file it was given:\n%s", unit.Content)
	}
}

// Every hardening directive in the shipped unit is preserved. This is the test
// that stops a from-scratch re-render quietly dropping one: a unit that loses
// ProtectSystem=strict is a unit that still starts and is no longer isolated.
func TestTheHardeningIsPreserved(t *testing.T) {
	unit := written(t, opts(t), "security-core.service")

	for _, directive := range []string{
		"NoNewPrivileges=true",
		"PrivateTmp=true",
		"PrivateDevices=true",
		"ProtectSystem=strict",
		"ProtectHome=true",
		"ProtectKernelTunables=true",
		"ProtectKernelModules=true",
		"ProtectControlGroups=true",
		"RestrictSUIDSGID=true",
		"LockPersonality=true",
		"MemoryDenyWriteExecute=true",
		"RestrictAddressFamilies=",
		"SystemCallArchitectures=native",
		"SystemCallFilter=@system-service",
		"CapabilityBoundingSet=",
		"AmbientCapabilities=",
	} {
		if !strings.Contains(unit.Content, directive) {
			t.Errorf("the unit is missing %q", directive)
		}
	}

	// The capability sets must be empty, not merely present. A line reading
	// `CapabilityBoundingSet=CAP_NET_BIND_SERVICE` passes a substring check and
	// is a much weaker unit.
	for _, line := range strings.Split(unit.Content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "CapabilityBoundingSet=") ||
			strings.HasPrefix(trimmed, "AmbientCapabilities=") {
			if strings.TrimPrefix(strings.TrimPrefix(trimmed, "CapabilityBoundingSet="),
				"AmbientCapabilities=") != "" {
				t.Errorf("a capability set is not empty: %q", trimmed)
			}
		}
	}
}

// The env file is the thing the shipped unit referenced and nothing read. Every
// variable the core reads has to be in it, and the required ones have to be
// empty rather than carrying a placeholder.
func TestTheEnvFileNamesEveryVariableAndNoPlaceholders(t *testing.T) {
	unit := written(t, opts(t), "security-platform.env")

	for _, key := range []string{
		"SP_TENANT_ID=", "SP_SITE_ID=", "SP_INTEGRATION_ID=",
		"SP_EVIDENCE_ROOT=", "SP_DATABASE_URL=", "SP_LISTEN_ADDR=",
		"SP_TLS_CERT=", "SP_TLS_KEY=", "SP_POLL_INTERVAL=", "SP_CLAIM_BATCH=",
		"SP_LEASE_DURATION=", "SP_AI_ENDPOINT=", "SP_AI_DEADLINE=",
		"SP_MILESTONE_BASE_URL=", "SP_DATABASE_PASSWORD=", "SP_MILESTONE_TOKEN=",
		"SP_SECRET_KEY=", "SP_LOG_LEVEL=",
	} {
		if !strings.Contains(unit.Content, key) {
			t.Errorf("the env file does not declare %q", key)
		}
	}

	// A placeholder in a required variable would let the core start against a
	// tenant named CHANGE-ME, which is worse than refusing to start.
	for _, line := range strings.Split(unit.Content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		switch key {
		case "SP_TENANT_ID", "SP_SITE_ID", "SP_INTEGRATION_ID",
			"SP_DATABASE_PASSWORD", "SP_MILESTONE_TOKEN":
			if value != "" {
				t.Errorf("%s carries the placeholder %q; a required variable must be "+
					"empty so the core refuses to start with a reason", key, value)
			}
		}
		for _, forbidden := range []string{"CHANGE", "TODO", "FIXME", "example.com", "xxx"} {
			if strings.Contains(strings.ToLower(value), forbidden) {
				t.Errorf("%s contains %q: %s", key, forbidden, line)
			}
		}
	}
}

// An operator's edited file must survive a re-install. The env file is where the
// tenant id and the database password live; replacing it on every install is the
// kind of thing that breaks a site during the upgrade that was supposed to fix
// it.
func TestAnExistingEnvFileIsNotOverwritten(t *testing.T) {
	o := opts(t)
	if err := os.MkdirAll(filepath.Dir(o.EnvFile), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const edited = "SP_TENANT_ID=the-real-one\nSP_DATABASE_PASSWORD=not-in-a-template\n"
	if err := os.WriteFile(o.EnvFile, []byte(edited), 0o640); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := lifecycle.WriteUnits(o); err != nil {
		t.Fatalf("WriteUnits: %v", err)
	}

	got, err := os.ReadFile(o.EnvFile)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != edited {
		t.Errorf("a re-install replaced the operator's env file:\ngot:\n%s\nwant:\n%s", got, edited)
	}
}

// The AI unit is only written when the AI runtime is installed beside the core.
// A core with no AI plane is a supported configuration, and writing a unit for a
// program that is not there produces a failing service.
func TestTheAIUnitIsOnlyWrittenWhenTheRuntimeIsInstalled(t *testing.T) {
	o := opts(t)
	units, err := lifecycle.WriteUnits(o)
	if err != nil {
		t.Fatalf("WriteUnits: %v", err)
	}
	for _, u := range units {
		if strings.Contains(u.Name, "ai-runtime") {
			t.Errorf("an AI unit was written with no AI program directory: %s", u.Name)
		}
	}

	o.AIProgramDir = filepath.Join(t.TempDir(), "ai")
	o.AIEnvFile = filepath.Join(t.TempDir(), "ai.env")
	units, err = lifecycle.WriteUnits(o)
	if err != nil {
		t.Fatalf("WriteUnits with an AI directory: %v", err)
	}
	var found bool
	for _, u := range units {
		if u.Name == "security-ai-runtime.service" {
			found = true
			if !strings.Contains(u.Content, "runtime.py") {
				t.Errorf("the AI unit does not exec the runtime:\n%s", u.Content)
			}
			// The AI plane must be its own unit, killable without touching ingest.
			if !strings.Contains(u.Content, "[Service]") {
				t.Error("the AI unit has no service section")
			}
		}
	}
	if !found {
		t.Error("no AI unit was written even with an AI program directory configured")
	}
}

// Every path in a unit file is content systemd executes as root. A traversal
// segment or a null byte is a way to write outside the directory, so each is
// refused with a message that names the problem.
//
// The paths are built by string concatenation, not filepath.Join: Join cleans
// the `..` away, which would test a traversal-free path while claiming to test a
// traversal. The production check exists for exactly the case this builds.
// The environment file carries SP_DATABASE_URL and SP_SECRET_KEY. It was written
// 0644 - world-readable - while the template this same function emits says in
// its own text "keep this file mode 0640 and owned by the service account". Any
// local account on the host could read the site's database credentials and its
// secret key.
// requirePOSIXModes skips on a platform that has no POSIX permission bits.
//
// Windows has a read-only flag and nothing else: `os.Chmod` there cannot express
// 0640, and `os.Stat` reports a synthesised 0666 or 0444 regardless of what was
// asked for. These two assertions are therefore about a Linux install, which is
// the only place this installer runs, and a Windows run of them would be testing
// the Go standard library rather than this code. The test is not deleted on
// Windows - it is skipped, loudly, and it runs on the platform that matters.
func requirePOSIXModes(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX permission bits: os.Chmod cannot express 0640 and " +
			"os.Stat reports a synthesised mode. This control is about a Linux install")
	}
}

func TestTheEnvironmentFileIsNotWorldReadable(t *testing.T) {
	requirePOSIXModes(t)
	dir := t.TempDir()
	opts := lifecycle.UnitOptions{
		UnitDir:    filepath.Join(dir, "unit"),
		ProgramDir: dir,
		EnvFile:    filepath.Join(dir, "etc", "security-platform.env"),
	}
	written, err := lifecycle.WriteUnits(opts)
	if err != nil {
		t.Fatalf("WriteUnits: %v", err)
	}

	for _, w := range written {
		info, err := os.Stat(w.Path)
		if err != nil {
			t.Fatalf("stat %s: %v", w.Path, err)
		}
		switch filepath.Base(w.Path) {
		case "security-platform.env":
			if info.Mode().Perm()&0o007 != 0 {
				t.Errorf("%s is mode %o. It carries the database URL and the secret key, and "+
					"every local account on the host can read them", w.Path, info.Mode().Perm())
			}
			// The mask was `Perm() & 0o060 != 0o060`, which is not this test.
			//
			// `0o060` is OWNER read+write. On a file at 0640 the owner bits are 6 and
			// the group bits are 4, so `0o640 & 0o060` is `0o040` — the test failed on
			// a file that was exactly the mode it says it wants, and the error message
			// printed `is mode 640, want 0640`, which is the mode it had.
			//
			// It passed on a workstation for the whole life of this file because
			// `requirePOSIXModes` skips it on Windows, so the only platform the product
			// installs on is the only platform where it had never run. That is not a
			// Windows problem and not a Linux problem; it is a gate that had only ever
			// been executed on the one platform where its assertion is skipped.
			//
			// The requirement is group-readable and nothing else: `WriteUnits` writes
			// 0640 and the verifying operator runs as a different account in the same
			// group. Asserting the exact mode is clearer than a mask here, because the
			// mask version of this assertion is the thing that was wrong.
			if got := info.Mode().Perm(); got != 0o640 {
				t.Errorf("%s is mode %o, want 0640: the verifying operator runs as a "+
					"different account in the same group and has to be able to read it, "+
					"and nobody else may", w.Path, got)
			}
		default:
			// A systemd unit is read by systemd as a different account and has to
			// stay readable. This is the control on the other branch: tightening
			// everything would also be wrong.
			if info.Mode().Perm()&0o004 == 0 {
				t.Errorf("%s is mode %o; systemd reads the unit as a different "+
					"account and it has to stay world-readable", w.Path, info.Mode().Perm())
			}
		}
	}
}

// An env file left behind by an older installer is world-readable, and a
// re-install preserves it. "The operator may have edited it" is a reason to keep
// its contents, not its permissions.
func TestAPreservedEnvironmentFileIsTightenedOnReinstall(t *testing.T) {
	requirePOSIXModes(t)
	dir := t.TempDir()
	opts := lifecycle.UnitOptions{
		UnitDir:    filepath.Join(dir, "unit"),
		ProgramDir: dir,
		EnvFile:    filepath.Join(dir, "etc", "security-platform.env"),
	}
	written, err := lifecycle.WriteUnits(opts)
	if err != nil {
		t.Fatalf("WriteUnits: %v", err)
	}

	var envPath string
	for _, w := range written {
		if filepath.Base(w.Path) == "security-platform.env" {
			envPath = w.Path
		}
	}
	if envPath == "" {
		t.Fatal("no environment file was written")
	}

	// An operator edited it, and a previous version of the installer had it
	// world-readable.
	if err := os.WriteFile(envPath, []byte("SP_TENANT_ID=edited\n"), 0o666); err != nil {
		t.Fatalf("simulate the old mode: %v", err)
	}
	if _, err := lifecycle.WriteUnits(opts); err != nil {
		t.Fatalf("re-install: %v", err)
	}

	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm()&0o007 != 0 {
		t.Errorf("the preserved env file is still mode %o after a re-install. A secrets file "+
			"whose permissions are never revisited is a secrets file that is never fixed",
			info.Mode().Perm())
	}
	body, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "edited") {
		t.Error("the re-install replaced the operator's edited env file; preserving it is why " +
			"keepExisting exists")
	}
}

func TestUnsafePathsAreRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(o *lifecycle.UnitOptions)
	}{
		{
			name: "traversal in the unit directory",
			build: func(o *lifecycle.UnitOptions) {
				o.UnitDir += "/../escaped"
			},
		},
		{
			name: "traversal in the env file",
			build: func(o *lifecycle.UnitOptions) {
				o.EnvFile += "/../../escaped.env"
			},
		},
		{
			name: "a null byte in the env file",
			build: func(o *lifecycle.UnitOptions) {
				o.EnvFile += "\x00.txt"
			},
		},
		{
			name: "a null byte in the unit directory",
			build: func(o *lifecycle.UnitOptions) {
				o.UnitDir += "\x00"
			},
		},
		{
			name: "traversal in the program directory",
			build: func(o *lifecycle.UnitOptions) {
				o.ProgramDir += "/../elsewhere"
			},
		},
		{
			name: "traversal in the AI env file",
			build: func(o *lifecycle.UnitOptions) {
				o.AIProgramDir = filepath.Join(t.TempDir(), "ai")
				o.AIEnvFile += "/../escaped.env"
			},
		},
		{
			name: "traversal in the AI program directory",
			build: func(o *lifecycle.UnitOptions) {
				o.AIProgramDir = filepath.Join(t.TempDir(), "ai") + "/../elsewhere"
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := opts(t)
			tc.build(&o)

			_, err := lifecycle.WriteUnits(o)
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), "unsafe path") {
				t.Errorf("the refusal %q does not say the path was unsafe", err)
			}
		})
	}
}

// An install with no program directory cannot write a unit that runs anything,
// so it refuses rather than writing a unit pointing at a relative path.
func TestNoProgramDirectoryIsRefused(t *testing.T) {
	o := opts(t)
	o.ProgramDir = ""
	if _, err := lifecycle.WriteUnits(o); err == nil {
		t.Fatal("a unit was written with no program directory")
	}
}

// Nothing is written when anything is refused. A half-installed unit set is a
// service that starts and cannot find its configuration.
func TestNothingIsWrittenWhenOnePathIsRefused(t *testing.T) {
	o := opts(t)
	o.EnvFile += "\x00"

	if _, err := lifecycle.WriteUnits(o); err == nil {
		t.Fatal("a bad env file was accepted")
	}

	// The core unit is rendered before the env file is validated, so check the
	// directory the unit would have landed in is absent.
	if _, err := os.Stat(filepath.Join(o.UnitDir, "security-core.service")); err == nil {
		t.Error("the core unit was written even though a later target was refused")
	}
}
