# migrations

Ordered, forward-only SQL that builds the Postgres 16 schema. Thirty-three files,
`0001` through `0033`, every one of them an `expand` step.

There is no `down`. There is no rollback of a migration. The only recovery
direction is a new, higher-numbered file â€” and the whole design exists so that
you should never need it.

## The naming and lifecycle

```
NNNN_name.phase.sql
```

- `NNNN` â€” a positive integer, zero-padded to four digits. The ordering key and
  the ledger's primary key.
- `name` â€” lowercase, may contain dots. `parseFileName` splits on the **last**
  dot, not the first, so `0003_events.and_graph.expand.sql` parses as name
  `events.and_graph`, phase `expand`.
- `phase` â€” exactly one of `expand`, `backfill`, `contract`.

The files, in order:

| Version | File | What it establishes |
|---|---|---|
| 0001 | `0001_tenancy_and_topology.expand.sql` | `tenants`, `sites`, `zones`, `devices`, `cameras`. Every composite key is `(tenant_id, ...)`. |
| 0002 | `0002_integration_and_durability.expand.sql` | Integration and durability substrate, including `outbox_events`. |
| 0003 | `0003_events_and_graph.expand.sql` | `canonical_events`, `inbox_events`, the partitioned event tables and the graph. |
| 0004 | `0004_cases.expand.sql` | `incidents` and the case lifecycle. |
| 0005 | `0005_ai_and_policy.expand.sql` | AI observation and policy storage, including the `AIDegradation` CHECK. |
| 0006 | `0006_evidence.expand.sql` | Evidence metadata, addressing and the `storage_ref` constraints. |
| 0007 | `0007_audit.expand.sql` | The append-only, hash-chained audit ledger and `audit_checkpoints`. |
| 0008 | `0008_identity_and_rls.expand.sql` | Identity, sessions and row-level security. |
| 0009 | `0009_outbox_idempotency_scope.expand.sql` | Re-scopes the outbox idempotency key from global to `(tenant_id, idempotency_key)`. |
| 0010 | `0010_drop_default_locale.expand.sql` | Drops `sites.default_locale` â€” by a new migration, **not** by editing 0001. |
| 0011 | `0011_source_clock_state.expand.sql` | Lets `device_clocks` hold a source system's clock, namespaced `source:<id>`. |
| 0012 | `0012_canonical_events_primary_key.expand.sql` | Gives `canonical_events` the primary key it never had. |
| 0013 | `0013_audit_insert_trigger_without_update_privilege.expand.sql` | Removes the locking read from the audit insert trigger, which needed an UPDATE privilege the application deliberately does not hold. |
| 0014 | `0014_edges_active_view_access.expand.sql` | Grants `security_core` SELECT on the `edges_active` view and marks it `security_invoker`, so a graph traversal is both permitted and tenant-scoped. |
| 0015 | `0015_partitions_and_correlation_indexes.expand.sql` | Monthly partitions and the correlation indexes, so intake stops accepting rows on 2027-01-01 and two hot paths stop doing a sequential scan. |
| 0016 | `0016_challenge_attempt_counter.expand.sql` | The MFA step had no brute-force control at all. This is the counter it needed. |
| 0017 | `0017_mfa_used_codes.expand.sql` | A TOTP code was accepted for as long as it was mathematically valid, and could be replayed inside that window. This is what remembers it. |
| 0018 | `0018_audit_subrange_verification.expand.sql` | `audit_verify_chain(tenant, p_from)` reported every sub-range as broken. This fixes the verification, not the ledger. |
| 0019 | `0019_outbox_dead_letter_reason.expand.sql` | `outbox_events` is the only durability table of the three that had no reason recorded on a dead letter. |
| 0020 | `0020_drop_evidence_export_from_the_role_seed.expand.sql` | The seeded role table claimed a capability no code enforces. This drops it from the seed. |
| 0021 | `0021_site_isolation_restrictive.expand.sql` | Makes `site_isolation` RESTRICTIVE, so the tenant policy and the site policy compose as AND rather than as OR. |
| 0022 | `0022_observation_hosted_attribution.expand.sql` | Expands `ai_observations` so a provider whose weights are not ours can be attributed honestly: hosted attribution, model hash, prompt version. |
| 0023 | `0023_site_isolation_for_every_site_bearing_table.expand.sql` | Gives EVERY site-bearing relation a `site_isolation` policy, derived from the catalogue rather than from a list of names. |
| 0024 | `0024_search_documents.expand.sql` | A `tsvector` for search, and a GIN index over it. |
| 0025 | `0025_future_partitions.expand.sql` | Extends the partition horizon from 2028-01 to 2031-01. |
| 0026 | `0026_camera_adjacency.expand.sql` | Camera adjacency, persisted: which cameras see which other cameras' ground. Site-scoped, because a camera's neighbours are as site-bound as the camera. |
| 0027 | `0027_event_embeddings.expand.sql` | A vector embedding per canonical event, for semantic retrieval. Nothing reads the table yet. |
| 0028 | `0028_incident_threat_score.expand.sql` | The threat score, on the incident: a score with a CHECK and the reason chain behind it, plus the index the attention queue reads. |
| 0029 | `0029_metric_rollups.expand.sql` | The durable metric rollup: one row per (metric, bucket, site, scope), with the metric vocabulary closed by a CHECK and `site_id` NOT NULL, because `app_site_visible(NULL)` is TRUE and a tenant-wide row would put one site's aggregate in another site's scope. |
| 0030 | `0030_device_registry.expand.sql` | The device registry: the MEASURED capability manifest on `devices` (a closed four-state vocabulary, and only `verified` counts), and the credential INVENTORY in `device_credentials`, where every column is a pointer into the keystore and the secret is never in the table. |
| 0031 | `0031_device_address.expand.sql` | Where a device IS: the base address the product probes it at. A host and a port and no credential, because a probe that takes its address from a request could be pointed at any device. |
| 0032 | `0032_recording_index.expand.sql` | The recording index: one row per segment (partitioned by the segment's start, with its uniqueness in a separate key table because Postgres requires the partition key in a partitioned table's unique index), the per-device-per-day chain that is sealed into the audit ledger, and the policy that says how long a segment is kept. |
| 0033 | `0033_ai_provider_chain.expand.sql` | The provider chain: which models may answer for a site, in what order, and whether the site may send anything off the machine -- plus the egress log that records what LEFT, by digest and by size, and never the content. |

0010 and 0012 are the two worth reading as worked examples of the discipline
before you write anything. 0010's own comment: "Dropped by a new migration
rather than by editing 0001, which is the whole point of the forward-only rule: a
site that already ran version 1 keeps a ledger that matches the SQL that
actually ran."

The table above is checked against the directory by
`internal/migrationreadme`, the same way `internal/statuscheck` checks
`docs/status.md`'s counts: a file in the directory that the table does not name
is a failing test, not a sentence somebody notices while grepping.

### The three phases

| Phase | What it does | When it ships |
|---|---|---|
| `expand` | Adds new columns, tables, indexes, nullable columns, new constraints that the running binary does not yet read. | Immediately. Old binary keeps working. |
| `backfill` | Fills a new column with data derived from what already exists. | With or after its expand, once the writing code no longer omits the column. |
| `contract` | Removes or renames what the expand replaced â€” dropping a column, tightening a type, deleting an index. | **A later release**, and only after the expand has been exercised in the field. |

The phase is declared, not inferred. `Migration.IsValid` rejects a file whose
phase is not one of the three (`migrations/migrations.go:62`), and
`All()` therefore refuses the whole set rather than skipping the file.

**What the runner does with a file that has no phase suffix:** it refuses the
entire migration set. `parseFileName` returns `want NNNN_name.phase.sql`,
`All()` propagates the error, and `Up`, `status`, `version` and `verify` all
fail before a single statement runs.

One correction to the comment in `Makefile:77-79`, which describes the
historical intent rather than the current behaviour: the text says "A file
without one is not a migration this runner will apply, and silently skipping it
is how a schema and a binary drift apart." The implementation is stricter than
that â€” it is a hard error, not a skip. A file with a *present but unrecognised*
phase (say `0001_tenancy.migrated.sql`) parses fine and is then rejected by
`IsValid`, which is the same outcome. Either way, no silent skip. The danger the
Makefile comment describes is real; the code has since been tightened against it.

Two more loader rules, both errors rather than warnings: two files claiming the
same version (`migrations %q and %q both claim version %d`) and an empty body.

## The rules, and the failure each one prevents

### 1. Migrations are forward-only. Never edit an applied migration.

**Enforced by:** the SHA-256 checksum computed at load time
(`migrations/checksum.go`) and stored in the `schema_migrations` ledger row on
apply. `Up` calls `verifyChecksumsOn` before it plans or applies anything, and
`security-migrate verify` / `securityctl migrate verify` run it on demand.

**The failure it prevents:** a site that ran version N with one body and a later
release that runs a different body against the same version. The site's ledger
would then be a lie about its own history, and every "what happened to this
schema" question becomes unanswerable. The runner's message says it outright:

```
migrate: checksum mismatch for 0007_audit.expand.sql: applied 3f2a1c9e...,
build has 8b41d0aa.... Applied migrations are never edited; add a new migration instead
```

The related case â€” a version applied here that this build does not contain, i.e.
an **older binary pointed at a newer database** â€” is refused too:
`version %d (%s) is applied but absent from this build; this binary is older than
the database`.

**A gap worth knowing:** the checksum covers the file *body*, not the filename.
Renaming an applied file (`0001_tenancy.expand.sql` ->
`0001_tenancy_and_topology.expand.sql`) does not change the checksum, so
`VerifyChecksums` still passes while the ledger row's `name` column silently
diverges from the file. Do not rename applied migrations.

### 2. SQL lives in `migrations/`, never inline in Go.

**Enforced by:** the `//go:embed *.sql` in `migrations/migrations.go`, and the
architecture. There is no `CREATE TABLE` in the Go tree.

**The failure it prevents:** a schema change that lives in a `.go` file is
reviewed as code, applied by a different code path than the one every other site
used, and is not covered by the checksum, the ledger, or the release archive.
`security-migrate` is a separate binary for exactly this reason: giving the
long-running core the ability to reshape its own tables under itself "is a
standing invitation to an in-place upgrade that cannot be rolled back"
(`cmd/security-migrate/main.go:1`).

### 3. Expand and contract ship as separate steps. A contract step may never be bundled with the expand that makes it safe.

**Enforced by, in two places:**

- `TestM1ContainsNoContractStep` in `migrations/migrations_test.go` â€” M1 ships
  expand steps only.
- `internal/release/release.go:248`, which refuses to *build* an archive
  containing both: `the archive contains both expand and contract migrations. A
  contract step must ship alone, in a later release.`

**The failure it prevents:** the window between the expand and the contract is
the window in which the old and the new binary both have to work against the
same database. Collapse it and there is no window: a site that upgrades at 02:00
with no maintenance slot drops a column the currently-installed binary still
reads, and takes the ingest pipeline down until somebody with a restore
understood the sequence.

Note the asymmetry: a *mistakenly labelled* file is caught (the phase test and
the archive check both read the label), but a destructive statement **written
inside an `.expand.sql` file** is not caught by either. `0010` is exactly that
case â€” it drops a column and is labelled `expand`, which is defensible because
the dropped column was already dead, but nothing in the build would stop a
future file from doing the same to a live column. Review is the control there.

## How to add one

### 1. Pick the next version number

```sh
make migrate-status          # or: go run ./cmd/security-migrate status
```

Read the current version, the target, the supported range and the pending set
before you write anything.

### 2. Write the file

`migrations/00NN_your_name.expand.sql`, with a header comment stating what it
does and **why**, in the voice of the existing twenty-eight. The headers are the
design record; `0010` and `0012` are the two to imitate.

The `NNN` you pick is one past the highest number in the directory above, and
`internal/migrationreadme` fails the build if the file you added is not in that
table.

### 3. Widen the schema window

`migrations/compat.json` currently declares:

```json
{
  "minSchemaVersion": 1,
  "maxSchemaVersion": 33,
  "minCoreVersion": "0.1.0",
  "maxCoreVersion": "0.x",
  "contractsVersion": "security/v1"
}
```

Raise `maxSchemaVersion` to your new number in the same commit.
`TestReleaseCompatibilityWindowCoversMigrations` fails otherwise, with the
reason stated: "a binary built from this tree would refuse to install its own
schema". The test also fails if `minSchemaVersion` is ever above 1, because a
window that excludes the first migration makes the initial install impossible.

`compat.json` is embedded with `//go:embed` **and** read from disk by the
release verifier, which must check the manifest inside an archive rather than
the one compiled into a binary it has not validated.
`TestEmbeddedAndOnDiskManifestsAgree` keeps the two copies identical. Edit the
file, do not hand-edit anything else that records the window.

Its own note is the rule: "Every release widens the window, never narrows it
inside an expand window: the previous binary must remain usable until the
matching contract step has shipped and been observed in the field."

### 4. Validate

```sh
make migration-phases
go test -p 1 ./migrations/...
go test -p 1 ./internal/migrate/...
go test -p 1 ./internal/schemalaws/...
```

`internal/schemalaws` is the suite that asserts the schema-level invariants â€”
`tenant_id NOT NULL` everywhere, RLS behaviour, the audit chain's
trigger-computed hash â€” against a real database. It resets and rebuilds the
schema, so it needs `SP_TEST_DATABASE_URL` and the `-p 1` discipline.

### 5. Exercise it against a real database before shipping

```sh
export SP_TEST_DATABASE_URL='postgres://postgres@127.0.0.1:55432/security_test?sslmode=disable'
go run ./cmd/security-migrate up
go run ./cmd/security-migrate status
go run ./cmd/security-migrate verify
```

### 6. Ship the expand alone

Not with the contract. Not with the code that reads the new column in a way that
requires it to be populated. The expand goes out, is observed, and only then
does the next release become eligible to carry the matching backfill and
contract steps.

## How a binary refuses to run against an out-of-window database

`migrations/compat.json` is the window. `internal/migrate` is the enforcement.

```go
func compatible(current int, c migrations.Compatibility) bool {
    if current == 0 {
        return true
    }
    return current >= c.MinSchemaVersion && current <= c.MaxSchemaVersion
}
```

- **Version 0** â€” a fresh database â€” is always compatible. There is no schema to
  be incompatible with, and refusing to install onto an empty cluster would make
  the first install of every site fail.
- **Anything else** must be inside `[minSchemaVersion, maxSchemaVersion]`.
  Outside it, `Up` returns an `*IncompatibleSchemaError` and applies nothing:

  ```
  schema version 30 is outside the supported window [1, 29]: the database sits
  outside this build's supported window. Upgrade or roll back the binary rather
  than forcing a migration.
  ```

  And `security-migrate status` prints the operator-facing form:

  ```
  schema version : 30
  this build     : 29
  supported range: [1, 29]
  compatibility  : OUT OF RANGE â€” upgrade or roll back the binary
  ```

  `securityctl preflight` reports the same condition as a fatal check with the
  remedy attached: "upgrade or roll back the binary; do not force a migration
  across the window".

`Up` also takes a session-level advisory lock (`pg_advisory_lock(0x1F0CA11_5EC0011)`)
for the whole run, so two `security-migrate up` processes â€” or one racing the
installer â€” serialise instead of interleaving DDL. Each migration runs in its
own transaction together with its ledger row, so a failed step leaves the
earlier steps committed and does not record the failed one
(`TestFailedStepLeavesEarlierStepsCommitted`). A partial application is how a
site ends up with a version number that does not describe its schema.

## The test that enforces it

```sh
make migration-phases
# go test -p 1 ./migrations/... -run TestAllLoadsAndValidates -count=1
```

`TestAllLoadsAndValidates` (`migrations/migrations_test.go:8`) asserts:

- `All()` loads every embedded `.sql` with no error â€” so an unparseable name, a
  duplicate version, an unknown phase or an empty body fails here.
- The set is non-empty: "the release would install an empty schema".
- Every migration passes `IsValid()`: positive version, non-empty name, a known
  phase, a non-empty body.
- Versions are strictly ascending, with no duplicates.
- Every checksum is 64 hex characters.

It needs no database, so it runs in CI with no Postgres service.

**One thing to be precise about:** `make migration-phases` runs *only* that
test. The expand/contract guard is a different test,
`TestM1ContainsNoContractStep`, and it is reached by `make test` / `make
test-race` (`go test -p 1 ./...`), not by `migration-phases`. Both are in `make
ci`. If you want the phase guard on its own:

```sh
go test -p 1 ./migrations/... -run TestM1ContainsNoContractStep -count=1
```

## Worked example: adding a `NOT NULL` column

Adding a required column to a table a running binary is already using. The
naive version is one `ALTER TABLE ... ADD COLUMN ... NOT NULL`, which either
rewrites the whole table under a live workload or fails outright on a table
with rows. Four steps, each independently safe against the binary that shipped
before it.

The numbers below are `0031` to `0034` on purpose: `0013` to `0016` are real
migrations in the table above, and an example that names a file that exists is
an example somebody copies by accident.

### Step 1 â€” `0031_incident_detected_by.expand.sql`

```sql
-- 0031_incident_detected_by.expand.sql
--
-- Add incidents.detected_by.
--
-- Nullable, with no default and no constraint. The running binary does not
-- write it and does not read it; adding a column the old binary has never
-- heard of is invisible to it. A DEFAULT would not be: Postgres 11+ can add
-- one without a table rewrite, but the value would then differ between rows
-- written before the migration and rows written after, and that difference is
-- exactly the sort of thing a replay has to explain.

ALTER TABLE incidents
    ADD COLUMN IF NOT EXISTS detected_by TEXT;
```

**Ship this alone.** The code that writes `detected_by` should be written to
tolerate `NULL`, because for the whole life of this column there is a window in
which rows have it null.

### Step 2 â€” `0032_backfill_incident_detected_by.backfill.sql`

```sql
-- 0032_backfill_incident_detected_by.backfill.sql
--
-- Backfill the rows the previous step left null.
--
-- In production this runs in bounded batches with a sleep between them, so a
-- site with a large incidents table is not locked while the rewrite happens.
-- The whole file is one transaction to the runner, so the batching is inside
-- the statement, not across migrations.

UPDATE incidents
   SET detected_by = 'rule-engine'
 WHERE detected_by IS NULL;
```

**Why now and not with step 1:** the backfill is expensive and it is safe only
once nothing new is writing nulls. A single release that both adds the column
and fills it means the fill runs against a table that is still growing.

### Step 3 â€” `0033_incident_detected_by_not_null.expand.sql`

```sql
-- 0033_incident_detected_by_not_null.expand.sql
--
-- Now the column can be NOT NULL.
--
-- Only safe once 0014 has been observed in the field: the precondition is that
-- no row anywhere has detected_by IS NULL, and the backfill is what makes that
-- true. Adding the constraint earlier would fail on the oldest row in the
-- table, which is the row the backfill has not reached yet.

ALTER TABLE incidents
    ALTER COLUMN detected_by SET NOT NULL;
```

**Still an `expand`, not a `contract`.** It is additive from the old binary's
point of view: the old binary never wrote a null into this column, because when
it wrote, the column did not exist and the new rows it inserts after the
migration get whatever the application supplies. The constraint only rejects
values that should not be there.

### Step 4 â€” `0034_drop_incident_legacy_source.contract.sql`

```sql
-- 0034_drop_incident_legacy_source.contract.sql
--
-- Drop the column this replaced. Shipped alone, in a later release, after
-- every deployed binary reads detected_by and none reads legacy_source.
--
-- This is the only one of the four that can break a running binary, and that
-- is the entire reason it is in a separate release.

ALTER TABLE incidents
    DROP COLUMN IF EXISTS legacy_source;
```

### The ordering, stated as a rule

Each step is safe against the binary that shipped **before** it. That is the
whole discipline: a site may be running any of the four binaries against any of
the four schemas.

- Steps 1-3 are invisible to an older binary. A column it does not know about
  costs it nothing; a constraint it violates is a constraint on values it never
  produces.
- Step 4 is the only irreversible one. It is a `contract` step, which
  `TestM1ContainsNoContractStep` and `internal/release` both refuse to ship
  alongside an expand.
- Steps 1-3 and the code that uses the column ship together. Step 4 ships alone,
  later, with nothing else in the archive.

## What breaks

| Mistake | What it does to a running site |
|---|---|
| Editing an applied `.sql` file â€” even a comment | Checksum mismatch. Every subsequent `security-migrate up` aborts before applying anything, `security-migrate verify` fails, and the site is stuck on a binary that refuses to migrate. Detected on the first run, which is the good case. |
| Renaming an applied file | **Not detected.** The checksum covers the body, not the name, so verification passes while the ledger's `name` column diverges from the file. |
| A `.sql` file with no phase suffix | `All()` errors; every migrate command fails before touching the database. Loud, not silent â€” but the message points at the filename, which is where the fix is. |
| A destructive statement written inside an `.expand.sql` | The old binary starts, and the column it reads is gone. This is the one the phase test cannot catch, because the file is correctly labelled and correctly named. Review is the control. |
| Shipping the contract step in the same archive as the expand | `internal/release` refuses to build it. Force it past the build and the site drops a column the installed binary reads, on the upgrade that was supposed to be safe. |
| Reusing a version number | `All()` errors: two migrations both claim version N. The ordering is ambiguous and the runner stops. |
| Forgetting to raise `maxSchemaVersion` in `compat.json` | `TestReleaseCompatibilityWindowCoversMigrations` fails at build time. Ship it anyway and the site that reaches the new version is refused by its own binary: `OUT OF RANGE â€” upgrade or roll back the binary`, with no way to advance. |
| Narrowing `minSchemaVersion` to drop old sites | A site below the new minimum is refused. `TestReleaseCompatibilityWindowCoversMigrations` pins `minSchemaVersion` at 1 so the first install stays possible; the release train's "widen, never narrow inside an expand window" is the same rule for the other end. |
| **A site already on the new schema while an old binary is still installed** | **The one that matters.** It is the failure every rule above exists to make unreachable, and it has two distinct shapes. If the old binary's declared `maxSchemaVersion` is below where the database now sits, everything refuses loudly and correctly: `preflight` fails fatally, `migrate up` returns `IncompatibleSchemaError`, and `securityctl rollback` reads *the previous build's own* declared window and refuses to half-downgrade a site. But if the schema change was itself a contract step, there is no window check that helps â€” the old binary starts happily and fails on a missing column, at 02:00, with no rollback that does not first require a restore. That asymmetry is why the contract step is a separate release: the window check can only stop you from a version skew, and only the release separation stops you from the second shape. |

The last row is also why `docs/status.md` records that a real release cycle was
built, installed, upgraded and rolled back by hand, with the pre-upgrade backup
verified offline and the wrong signing key refused at every step. The
discipline is only worth what its rehearsal cost.
