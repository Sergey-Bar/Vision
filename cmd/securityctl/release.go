package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Sergey-Bar/Ai-Captain/internal/command"
	"github.com/Sergey-Bar/Ai-Captain/internal/release"
	"github.com/Sergey-Bar/Ai-Captain/migrations"
)

// buildReleaseCmd builds the flag set and closes over the values, which is the
// shape the command tree parses with. A handler that builds its own FlagSet and
// parses a second time sees an empty argument list, because the tree has already
// consumed it.
func buildReleaseCmd() *command.Command {
	var (
		version, platform, out, spec, keyFile, keyID, gitCommit string
	)
	c := &command.Command{
		Name:    "build-release",
		Summary: "Build a signed offline release archive",
		Usage: `build-release --version <v> --files <spec.json> --signing-key <path>

The file list is explicit. The secret scan is a backstop; the list is the
control. A generated signing key is written owner-only and never printed.`,
	}
	c.Flags = func(fs *flag.FlagSet) {
		fs.StringVar(&version, "version", "", "Release version.")
		fs.StringVar(&platform, "platform", platformDefault(), "Target platform.")
		fs.StringVar(&out, "out", "dist", "Output directory.")
		fs.StringVar(&spec, "files", "", "JSON file list.")
		fs.StringVar(&keyFile, "signing-key", "", "Ed25519 signing key file.")
		fs.StringVar(&keyID, "key-id", "release-1", "Signing key identifier.")
		fs.StringVar(&gitCommit, "git-commit", "", "Commit this release was built from.")
	}
	c.Run = func(_ context.Context, env *command.Env, args []string) error {
		if len(args) > 0 {
			return &command.UsageError{Err: errors.New("pass options as flags, not positionals")}
		}
		if version == "" {
			return &command.UsageError{Err: errors.New("--version is required")}
		}

		sources, err := loadFileSpec(spec)
		if err != nil {
			return err
		}
		if len(sources) == 0 {
			return command.Refuse("the file list is empty; a release with no files is not a release")
		}

		key, trusted, err := loadSigningKey(keyFile)
		if err != nil {
			return err
		}
		defer zeroKey(key)

		compat, err := migrationCompatibility()
		if err != nil {
			return err
		}
		migrations, err := embeddedMigrations()
		if err != nil {
			return err
		}

		archivePath, manifest, err := release.Build(out, release.BuildInput{
			Version:    version,
			Platform:   platform,
			Files:      sources,
			Migrations: migrations,
			Compatibility: release.Compatibility{
				MinSchemaVersion: compat.MinSchemaVersion, MaxSchemaVersion: compat.MaxSchemaVersion,
				MinCoreVersion: compat.MinCoreVersion, MaxCoreVersion: compat.MaxCoreVersion,
				ContractsVersion: compat.ContractsVersion,
			},
			SBOM:      release.SBOM{Path: "sbom.spdx.json", Format: "spdx-json"},
			GitCommit: gitCommit,
			Builder:   "securityctl build-release",
		}, key, keyID)
		if err != nil {
			return err
		}

		fmt.Fprintf(env.Stdout, "built %s\n", archivePath)
		fmt.Fprintf(env.Stdout, "  %d file(s), schema window [%d, %d], signed by %s\n",
			len(manifest.Files), manifest.Compatibility.MinSchemaVersion,
			manifest.Compatibility.MaxSchemaVersion, keyID)
		fmt.Fprintf(env.Stdout, "  manifest: %s\n", archivePath+".manifest.json")
		fmt.Fprintf(env.Stdout, "\nverify it before trusting it, on the machine that will install it:\n"+
			"  securityctl verify-release --archive %s --trusted-key %s\n",
			filepath.Base(archivePath), hex.EncodeToString(trusted))
		return nil
	}
	return c
}

// verifyReleaseCmd checks an archive before anything is installed from it.
//
// This is the command to run on a USB stick at a site with no network. It
// contacts nothing, and it is the only thing standing between a tampered archive
// and code execution on every machine that installs it.
func verifyReleaseCmd() *command.Command {
	var (
		archive, manifestPath, trustedKeyHex string
		asJSON                               bool
	)
	c := &command.Command{
		Name:    "verify-release",
		Summary: "Verify a signed offline release bundle",
		Usage: `verify-release --archive <a.tar.gz> [--trusted-key <hex>]

Refuses a tampered archive, an unsigned one, a manifest whose file hashes do not
match the contents, a path that escapes the archive root, credential-shaped
material, and a schema window this binary cannot support.

--trusted-key is what makes the signature mean something. Without it the archive
is checked against the key it carries, which proves only that whoever wrote the
archive signed it. Get the key out of band with 'securityctl release-key'.`,
	}
	c.Flags = func(fs *flag.FlagSet) {
		fs.StringVar(&archive, "archive", "", "Archive to verify.")
		fs.StringVar(&manifestPath, "manifest", "", "Manifest path. Defaults to the archive's own.")
		fs.StringVar(&trustedKeyHex, "trusted-key", "", "Trusted Ed25519 public key, hex.")
		fs.BoolVar(&asJSON, "json", false, "Emit machine-readable output.")
	}
	c.Run = func(_ context.Context, env *command.Env, args []string) error {
		if len(args) > 0 {
			return &command.UsageError{Err: errors.New("pass options as flags, not positionals")}
		}
		if archive == "" {
			return &command.UsageError{Err: errors.New("--archive is required")}
		}
		if manifestPath == "" {
			manifestPath = archive + ".manifest.json"
		}

		manifest, err := release.LoadManifest(manifestPath)
		if err != nil {
			return err
		}
		// The trusted key is required, not optional. `release verify` exists to
		// answer "did the party I trust build this", and with no key supplied the
		// only answer available is "did whoever wrote the archive sign their own
		// work". The previous version printed a warning and continued, which made
		// the warning the only thing standing between an operator and a self-signed
		// forgery on the one command whose job is to detect one.
		trusted, err := decodePublicKey(trustedKeyHex)
		if err != nil {
			return &command.UsageError{Err: fmt.Errorf(
				"--trusted-key is required: %w. Without it this command cannot distinguish a "+
					"genuine archive from one an attacker signed themselves", err)}
		}

		files, err := release.Verify(archive, manifest, trusted)
		if err != nil {
			if asJSON {
				_ = json.NewEncoder(env.Stdout).Encode(map[string]any{"ok": false, "error": err.Error()})
			}
			return command.Refuse("%v", err)
		}

		// The SBOM must actually be in the archive, or the manifest is naming
		// something that is not there.
		if manifest.SBOM.Path != "" && !containsPath(files, manifest.SBOM.Path) {
			return command.Refuse(
				"the manifest declares an SBOM at %q but the archive does not contain it. A build "+
					"that cannot say what it shipped is a build that cannot be asked what is running.",
				manifest.SBOM.Path)
		}

		// The local binary's own window is checked against the archive's, because
		// an installer that does not refuse an out-of-window archive will
		// half-upgrade a site and leave it unable to roll back.
		local, err := migrationCompatibility()
		if err != nil {
			return err
		}
		if local.MaxSchemaVersion < manifest.Compatibility.MinSchemaVersion ||
			local.MinSchemaVersion > manifest.Compatibility.MaxSchemaVersion {
			return command.Refuse(
				"this binary supports schema [%d, %d] and the archive needs [%d, %d]. The windows "+
					"do not overlap, so installing would leave the site in a state neither version "+
					"supports. Get the release that matches this build, or the build that matches "+
					"this release.",
				local.MinSchemaVersion, local.MaxSchemaVersion,
				manifest.Compatibility.MinSchemaVersion, manifest.Compatibility.MaxSchemaVersion)
		}

		if asJSON {
			enc := json.NewEncoder(env.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(map[string]any{
				"ok": true, "version": manifest.Version, "platform": manifest.Platform,
				"files": len(files), "minSchemaVersion": manifest.Compatibility.MinSchemaVersion,
				"maxSchemaVersion": manifest.Compatibility.MaxSchemaVersion,
			})
		}
		fmt.Fprintf(env.Stdout, "verified %s\n", manifest.Version)
		fmt.Fprintf(env.Stdout, "  product      : %s\n", manifest.Product)
		fmt.Fprintf(env.Stdout, "  platform     : %s\n", manifest.Platform)
		fmt.Fprintf(env.Stdout, "  built        : %s\n", manifest.CreatedAt.Format("2006-01-02T15:04:05Z"))
		fmt.Fprintf(env.Stdout, "  signed by    : %s\n", manifest.Signature.KeyID)
		fmt.Fprintf(env.Stdout, "  files        : %d, all hashes and sizes match\n", len(files))
		fmt.Fprintf(env.Stdout, "  sbom         : %s\n", manifest.SBOM.Path)
		fmt.Fprintf(env.Stdout, "  schema window: [%d, %d]\n",
			manifest.Compatibility.MinSchemaVersion, manifest.Compatibility.MaxSchemaVersion)
		fmt.Fprintf(env.Stdout, "  compatible   : yes, this build supports [%d, %d]\n",
			local.MinSchemaVersion, local.MaxSchemaVersion)
		return nil
	}
	return c
}

func platformDefault() string {
	return "windows-amd64"
}

func containsPath(files []release.FileEntry, want string) bool {
	for _, f := range files {
		if f.Path == want {
			return true
		}
	}
	return false
}

type fileSpec struct {
	ArchivePath string `json:"archivePath"`
	SourcePath  string `json:"sourcePath"`
	Mode        string `json:"mode"`
}

func loadFileSpec(path string) ([]release.SourceFile, error) {
	if path == "" {
		return nil, errors.New("--files is required; the file list is explicit on purpose")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file list: %w", err)
	}
	var specs []fileSpec
	if err := json.Unmarshal(raw, &specs); err != nil {
		return nil, fmt.Errorf("parse file list: %w", err)
	}
	out := make([]release.SourceFile, 0, len(specs))
	for _, s := range specs {
		out = append(out, release.SourceFile{
			ArchivePath: s.ArchivePath, SourcePath: s.SourcePath, Mode: s.Mode,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ArchivePath < out[j].ArchivePath })
	return out, nil
}

// loadSigningKey reads a signing key, or creates one if the file is absent.
//
// A generated key is written with owner-only permissions and never printed. The
// private half of a release signing key that is in a repository, a build log, or
// a support ticket is not a signing key any more.
func loadSigningKey(path string) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	if path == "" {
		return nil, nil, errors.New("--signing-key is required")
	}
	raw, err := os.ReadFile(path)
	if err == nil {
		key, decodeErr := hex.DecodeString(strings.TrimSpace(string(raw)))
		if decodeErr != nil || len(key) != ed25519.PrivateKeySize {
			return nil, nil, fmt.Errorf("the signing key file is not a %d-byte hex private key",
				ed25519.PrivateKeySize)
		}
		private := ed25519.PrivateKey(key)
		return private, private.Public().(ed25519.PublicKey), nil
	}
	if !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("read signing key: %w", err)
	}

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, nil, fmt.Errorf("generate signing key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, fmt.Errorf("create key directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(priv)), 0o600); err != nil {
		return nil, nil, fmt.Errorf("write signing key: %w", err)
	}
	return priv, pub, nil
}

func decodePublicKey(s string) (ed25519PublicKey, error) {
	raw, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("the trusted key is not hex: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("the trusted key is %d bytes, want %d",
			len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// zeroKey overwrites the signing key in memory once the build is done.
func zeroKey(key ed25519.PrivateKey) {
	for i := range key {
		key[i] = 0
	}
}

func migrationCompatibility() (release.Compatibility, error) {
	compat, err := migrations.ReleaseCompatibility()
	if err != nil {
		return release.Compatibility{}, err
	}
	return release.Compatibility{
		MinSchemaVersion: compat.MinSchemaVersion, MaxSchemaVersion: compat.MaxSchemaVersion,
		MinCoreVersion: compat.MinCoreVersion, MaxCoreVersion: compat.MaxCoreVersion,
		ContractsVersion: compat.ContractsVersion,
	}, nil
}

// embeddedMigrations lists the migrations this build carries, so the manifest
// declares what will actually be applied rather than what somebody remembered.
func embeddedMigrations() ([]release.MigrationEntry, error) {
	all, err := migrations.All()
	if err != nil {
		return nil, err
	}
	out := make([]release.MigrationEntry, 0, len(all))
	for _, m := range all {
		out = append(out, release.MigrationEntry{
			Version: m.Version, Name: m.Name, Phase: string(m.Phase),
		})
	}
	return out, nil
}
