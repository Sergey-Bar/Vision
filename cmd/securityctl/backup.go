package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/command"
	"github.com/Sergey-Bar/Ai-Captain/internal/lifecycle"
	"github.com/Sergey-Bar/Ai-Captain/internal/release"
)

const backupManifestName = "backup-manifest.json"

// runBackup writes a self-consistent backup and its manifest.
//
// The three things that have to travel together are the database, the evidence
// directory, and the checkpoint seals. Only the first two are recoverable from a
// database backup, and the third is the one an attacker who reaches the database
// would want gone — so a backup that omits any of them records that it did, and
// a restore reads the record rather than discovering the gap later.
func runBackup(env *command.Env, outDir string, allowPartial bool) (string, error) {
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return "", fmt.Errorf("lifecycle: create the backup directory: %w", err)
	}

	dumpPath := os.Getenv("SP_DATABASE_DUMP")
	if dumpPath == "" {
		return "", lifecycle.Refuse(
			"no database dump was given. Set SP_DATABASE_DUMP to a file produced by your " +
				"database tooling. This product does not shell out to pg_dump: a backup that " +
				"depends on a tool being present on the host is a backup that fails silently " +
				"on the site that needed it")
	}
	if _, err := os.Stat(dumpPath); err != nil {
		return "", fmt.Errorf("lifecycle: read the database dump: %w", err)
	}

	manifest := lifecycle.Backup{
		ManifestVersion: 1,
		TakenAt:         time.Now().UTC(),
		Product:         "security-platform",
		Version:         buildVersion(),
		Platform:        platformDefault(),
		Tenant:          configuredTenant(),
		DatabaseFile:    filepath.Base(dumpPath),
	}

	sum, size, err := lifecycle.HashFile(dumpPath, 0)
	if err != nil {
		return "", err
	}
	manifest.DatabaseSHA256 = sum
	manifest.DatabaseBytes = size

	if schema, err := currentSchemaVersion(); err == nil {
		manifest.DatabaseSchema = schema
	}

	// The dump is copied in, so the backup does not depend on the original still
	// being there when somebody needs it.
	target := filepath.Join(outDir, manifest.DatabaseFile)
	if err := copyFile(dumpPath, target); err != nil {
		return "", err
	}

	// Evidence. Its absence is recorded rather than papered over.
	evidenceRoot := os.Getenv("SP_EVIDENCE_ROOT")
	if evidenceRoot == "" {
		manifest.Complete = false
		manifest.Notes = "SP_EVIDENCE_ROOT was not set, so the evidence directory was not included. " +
			"Incidents in this backup cite evidence bytes that are not in it."
	} else {
		evidenceArchive := filepath.Join(outDir, "evidence.tar.gz")
		n, err := packDirectory(evidenceRoot, evidenceArchive)
		if err != nil {
			if !allowPartial {
				return "", fmt.Errorf("lifecycle: pack the evidence directory: %w", err)
			}
			manifest.Complete = false
			manifest.Notes = "the evidence directory could not be packed: " + err.Error()
		} else {
			manifest.EvidenceFile = "evidence.tar.gz"
			manifest.EvidenceBytes = n
			manifest.EvidenceDir = filepath.Base(evidenceRoot)
			// Hash the archive rather than naming its size alone, so a restore can
			// tell a tampered evidence tar from an intact one. An incident citing
			// evidence whose bytes have changed is not defensible.
			if sum, _, hashErr := lifecycle.HashFile(evidenceArchive, 0); hashErr == nil {
				manifest.EvidenceSHA256 = sum
			} else if manifest.Complete {
				manifest.Complete = false
				manifest.Notes = "the evidence archive could not be hashed: " + hashErr.Error()
			}
			manifest.Complete = manifest.Complete && manifest.EvidenceSHA256 != ""
		}
	}

	// Checkpoint seals. A database backup cannot restore them.
	if dir := os.Getenv("SP_CHECKPOINT_DIR"); dir != "" {
		sealArchive := filepath.Join(outDir, "checkpoints.tar.gz")
		if _, err := packDirectory(dir, sealArchive); err == nil {
			sum, _, hashErr := lifecycle.HashFile(sealArchive, 0)
			if hashErr != nil {
				return "", hashErr
			}
			manifest.CheckpointFile = "checkpoints.tar.gz"
			// The field is a hash, not a location. Recording the path here would
			// leave a restore unable to tell a tampered archive from an intact one,
			// which is the one thing a restore must be able to tell.
			manifest.CheckpointSHA = sum
		} else if manifest.Complete {
			manifest.Complete = false
			manifest.Notes = "the checkpoint seals could not be packed: " + err.Error()
		}
	} else if manifest.Complete {
		manifest.Complete = false
		manifest.Notes = "SP_CHECKPOINT_DIR was not set. The audit seals are the one thing a " +
			"database backup cannot restore, and they are the thing an attacker who reaches the " +
			"database would want gone."
	}

	if !manifest.Complete && !allowPartial {
		return "", lifecycle.Refuse(
			"the backup is incomplete: %s. Pass --allow-partial to take it anyway, and the "+
				"manifest will record that it is partial", manifest.Notes)
	}
	if err := manifest.Validate(); err != nil {
		return "", err
	}

	body, err := marshalIndented(manifest)
	if err != nil {
		return "", err
	}
	manifestPath := filepath.Join(outDir, backupManifestName)
	if err := os.WriteFile(manifestPath, body, 0o640); err != nil {
		return "", fmt.Errorf("lifecycle: write the backup manifest: %w", err)
	}
	_ = env
	return manifestPath, nil
}

func readBackupManifest(dir string) (lifecycle.Backup, error) {
	var b lifecycle.Backup
	raw, err := os.ReadFile(filepath.Join(dir, backupManifestName))
	if err != nil {
		return b, fmt.Errorf("lifecycle: read the backup manifest: %w", err)
	}
	if err := unmarshalJSON(raw, &b); err != nil {
		return b, fmt.Errorf("lifecycle: parse the backup manifest: %w", err)
	}
	return b, nil
}

// backupToPlan renders a backup as a readable block, using the same shape the
// install plan uses so an operator reads one format.
func backupToPlan(b lifecycle.Backup) string {
	out := fmt.Sprintf("backup of %s, taken %s\n", b.Product, b.TakenAt.Format("2006-01-02T15:04:05Z"))
	out += fmt.Sprintf("  version     : %s (%s)\n", b.Version, b.Platform)
	out += fmt.Sprintf("  tenant      : %s\n", b.Tenant)
	out += fmt.Sprintf("  database    : %s, %d bytes, schema %d\n",
		b.DatabaseFile, b.DatabaseBytes, b.DatabaseSchema)
	if b.EvidenceFile != "" {
		out += fmt.Sprintf("  evidence    : %s, %d bytes\n", b.EvidenceFile, b.EvidenceBytes)
	} else {
		out += "  evidence    : NOT INCLUDED\n"
	}
	if b.CheckpointFile != "" {
		out += fmt.Sprintf("  checkpoints : %s\n", b.CheckpointFile)
	} else {
		out += "  checkpoints : NOT INCLUDED\n"
	}
	if !b.Complete {
		out += fmt.Sprintf("  INCOMPLETE  : %s\n", b.Notes)
	}
	return out
}

func buildVersion() string {
	if v := os.Getenv("SP_VERSION"); v != "" {
		return v
	}
	return "development"
}

func copyFile(from, to string) error {
	body, err := os.ReadFile(from)
	if err != nil {
		return fmt.Errorf("lifecycle: read %s: %w", from, err)
	}
	// 0640, not 0755: a backup holds a database dump and a tenant's evidence,
	// and neither is world-readable on a machine an operator walks past.
	if err := os.WriteFile(to, body, 0o640); err != nil {
		return fmt.Errorf("lifecycle: write %s: %w", to, err)
	}
	return nil
}

func currentSchemaVersion() (int, error) {
	pool, err := openPool(background())
	if err != nil {
		return 0, err
	}
	defer pool.Close()
	var v int
	if err := pool.QueryRow(background(),
		`SELECT COALESCE(max(version), 0) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, fmt.Errorf("lifecycle: read the schema version: %w", err)
	}
	return v, nil
}

// marshalIndented renders a manifest for a human to read.
//
// It does not fall back to compact JSON on failure. A backup manifest is read by
// a restore and by an auditor, and a silently-unformatted one is a signal that
// something is wrong that a restore would then have to diagnose from the dump
// instead.
func marshalIndented(v any) ([]byte, error) {
	body, err := marshalJSON(v)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: encode: %w", err)
	}
	var pretty []byte
	if err := jsonIndent(&pretty, body); err != nil {
		return nil, fmt.Errorf("lifecycle: format the manifest: %w", err)
	}
	return append(pretty, '\n'), nil
}

// releaseManifestInto writes a release manifest beside a program directory, so a
// rollback can read the previous build's declared schema window rather than
// guessing it.
func releaseManifestInto(programDir string, m release.Manifest) error {
	body, err := marshalIndented(m)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(programDir, "release-manifest.json"), body, 0o640)
}
