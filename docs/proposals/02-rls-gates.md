# Proposal 02 — Generated and wider-catalog RLS gates

**Status:** proposal. Nothing is implemented. A human decides.

## The gap — stated honestly first

This repository **already has** static RLS analysis. `internal/rlsrole` contains
`static_test.go`, `canary_test.go`, `routes_test.go` and `duplication_test.go`,
and `scripts/verify.ps1` already runs an `rls-canary` gate against it. Commit
`8199afe` is titled "Row-level security under the role the product actually runs
as".

So this is **not** a proposal to add a category of coverage that is missing. It
is a proposal to widen an existing one, and to add the one dimension that
hand-written tests structurally cannot cover.

The dimension that is missing: **a table or policy that changes without anyone
editing a test.** `schemalaws` and `rlsrole` assert what their authors believed.
They cannot detect a new column, a new table, or a modified policy that a
belief never covered.

## Why it matters for this product specifically

`AGENTS.md` invariant 5 is that `tenant_id` is `NOT NULL` everywhere and never
read from a request body, and that background workers cross tenants only through
an explicit `app.system_worker` setting bounded by grants. `internal/schemalaws`
tests RLS isolation and `internal/rlsrole` tests the role the product runs as.

The failure mode these share is documented by the tools below as a recurring
one: RLS constrains **rows**, has no opinion about **columns**, and does not
apply to a table's **owner** unless `FORCE ROW LEVEL SECURITY` is set. All three
are live hazards in a schema with ~30 tables.

## Candidates

| Tool | Licence | What it adds that `rlsrole` does not |
|---|---|---|
| **`pgrls`** | MIT (Python 3.11+) | 67 static rules over the SQL in `migrations/`, 20 mechanically auto-fixable, `--sql-file` runs fully offline with no database. Includes `pgrls verify`, which detects the composition leak: RLS correctly denies a row, but a `SECURITY DEFINER` retrieval function returns it anyway with the owner's privileges. Also `snapshot`/`diff` for semantic policy diffing, and SARIF output. Go port announced at `go/v0.7.0` (step 1 only). |
| **`nysten/rlsautotest`** | verify before use | Generates a pgTAP suite *and its seed data* from the catalog, using reverse-predicate seeding: it works backwards from each policy predicate to the exact rows and identities that make it true and false. Emits a guard test that fails if a reachable table ships without RLS. Ships an offline shim, so it runs air-gapped. |
| **`matte97p/rlsgrid`** | verify before use | Fuzzes for real cross-tenant leaks by seeding synthetic tenants and actively attempting cross-tenant reads and writes, then emits pgTAP. Classifies every `role × table × operation` as allow / deny / conditional / **unrestricted**, surfacing RLS-off and `BYPASSRLS` explicitly. |

## Why this over the alternatives

The alternative to a generated suite is more hand-written tests. That does not
scale, and — more importantly — it does not fail when a *new* table appears.
The alternative to static analysis is reviewing every policy by eye, which is
exactly the review that misses inverted auth checks and a missing `WITH CHECK`.

## What we would build

1. `pgrls lint` over the concatenated `migrations/*.sql` in `verify.ps1`, as a
   non-blocking report first, then blocking once the existing debt is cleared.
2. A committed, regenerated `rlsautotest` pgTAP suite run by the existing
   `migration-phases`-style pattern, so a schema change that alters a policy
   produces a diff in the generated file and a human looks at it.
3. Nothing in `internal/rlsrole` deleted. It stays the hand-written canary.

## What we would not do

- **Not `pgrls fix` applied automatically.** Twenty rules are auto-fixable. Auto-
  applying a security fix to a migration that has already been applied violates
  `AGENTS.md` — applied migrations are immutable, and a contract step may never
  be bundled with the expand step that makes it safe. Emit the fix; a human
  writes a new numbered migration.
- **Not `pgrls verify` in the blocking gate yet.** It requires `pgvector`, which
  this schema does not use, and it is explicitly a **detector, not a prover**: a
  `LEAK` is sound, `NO LEAK` is a clean spot-check and not a proof of isolation
  for every argument. Admitting a tool that reports "not proven" into a gate
  named `verify` would be worse than not having it.
- **Not a third copy of the same assertions.** `rlsrole`, `schemalaws` and
  `rlsautotest` must have distinct jobs or the redundancy is noise. This
  proposal's job is generation and catalog drift; theirs is intent.

## Risks

- **False confidence.** A generated pgTAP suite proves the policies behave as
  written. It cannot prove the policies are the right policies. The
  `unrestricted` classification in `rlsgrid` is the honest counterweight.
- **Probe safety.** These tools connect to a database and write during seeding.
  Every one of them refuses production URLs by pattern, which is a string match,
  not a guarantee. They belong against a disposable database, in the same
  `-p 1` discipline the rest of the suite uses.
- **Toolchain growth.** Two Python tools in a Go repository is a real cost.
  `AGENTS.md` records that the AI runtime is stdlib-only; this would be a second
  Python dependency surface, in a different place, for a different purpose.

## Confidence

High that the gap is real and that the tools detect it. Medium on `pgrls` and
`rlsgrid` because they are young and were read from their READMEs, not run.
**Licences of `rlsautotest` and `rlsgrid` were not verified** and must be before
either is run.

## Decision required

Is a second toolchain language acceptable in the verification path, given the
AI runtime's stdlib-only constraint? If not, `pgrls` alone is still worth
considering on its static rules, and the generation dimension stays a gap.
