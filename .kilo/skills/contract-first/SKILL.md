---
name: contract-first
description: Enforce the repository's contract-first change order - contracts/ before owner code, before tests. Use when adding or changing a canonical event type, a rule construct, a VLM profile, an audit field, an API endpoint, a migration, a dispatch action, a new VMS adapter, or any wire/proto/JSON Schema field. Also use when a change touches a published contract and you need to know whether to change contracts/ or internal/contracts/ first, or when contractcheck or contract-sync failed and you need to know which copy has drifted. Not for changes with no contract surface.
---

# Contract-first

Every row follows the same order: **contract first, then owner, then test.**

That sentence is `internal/README.md`, and it is the order this repository
changes things in. This skill is the procedure; the table there is the data.

## The table

Reproduced from `internal/README.md` so it can be read without opening the
file. **If it disagrees with that file, that file is right** — it is the map, and
a copy in a skill that silently drifts is worse than no copy. Say so and fix it.

| Change | Owner | Contract first | Test that must exist |
|---|---|---|---|
| A new canonical event type | `internal/milestone` (`normalizeEventType`) **and** `internal/rules` (`KnownEventTypes`) — both, or the rule silently never matches | `contracts/json-schema/rule-bundle.schema.json` (the `eventType` vocabulary) plus its embedded copy; `contracts/protobuf/security/v1/event.proto` if a field is added | `internal/milestone/adapter_test.go` and `internal/rules/compile_test.go`. Run `make contract-sync` |
| A new VMS adapter | a new package mirroring `internal/milestone`: implement `Transport`, produce `RawRecord`, own its route table with `Unverified` flags | `contracts/json-schema/` only if it adds a wire type | Adapter package tests, plus the non-adapter diff (nothing outside the adapter changes) |
| A new rule construct | `internal/rules` in five places: the Go type in `bundle.go`, the JSON Schema, the compiler check in `compile.go`, the semantic rule in `semantic.go`, the evaluator in `match.go`/`engine.go` | `contracts/json-schema/rule-bundle.schema.json`, then copy to `internal/rules/schema/` (`make contract-sync` fails otherwise) | `compile_test.go` and `engine_test.go`. A new operator needs a closed-set entry in `FieldOperators`/`DispatchTypes`/`Severities` and a test that an unbounded pattern cannot be expressed |
| A new VLM profile | `internal/observations` (`M1Profiles`, `Fields`) and `internal/evaluation` (its gates) | `contracts/json-schema/ai-observations/<profile>.schema.json`; `observation.proto` only for a new top-level field | `observations_test.go` (shadow vs production, attribution mandatory) and `evaluation_test.go`. Start it `Shadow: true` |
| A new safe action (dispatch) | `internal/rules` for the vocabulary; `internal/outbox` or `internal/jobs` for the effect; a `securityctl` command for the operator half | `contracts/protobuf/security/v1/action.proto` | A red test that the action cannot reach a shell, SQL, the network or dynamic code, and a test that it is idempotent on a retry |
| A new audit field | `internal/audit` (`Entry`, `Validate`) **and** a new migration — the chain hash is trigger-computed over a length-prefixed encoding that must cover the new column | the migration. Never edit `0007_audit.expand.sql`; add `NNNN_*.<phase>.sql` | `audit_test.go` and `schemalaws_test.go`: a chain still verifies, an edited row still breaks it |
| A new API endpoint | `internal/api/handlers.go` (`RegisterRoutes`) and `readmodels.go` if it needs a new read model | `contracts/openapi/v1/openapi.yaml` | `internal/api/api_test.go`. Declare a capability via `requireCapability`, take a CSRF token if it changes state, never take `tenant_id` from a body |
| A new migration | `migrations/NNNN_name.<phase>.sql`, applied by `internal/migrate` | n/a — the migration *is* the change | `make migration-phases` and `migrate_test.go`. `TestVerifyChecksumsCatchesDrift` must still pass |

## Procedure

1. **Identify the row.** If no row matches, stop and say so. An unlisted change
   with a contract surface is a gap in the table, and the right response is to
   add the row, not to improvise an order.

2. **Edit `contracts/` first.** Nothing else yet. Not the Go type, not the
   test. The published contract is the source of truth and everything else
   follows it.

3. **Sync the embedded copies.** `contracts/json-schema/rule-bundle.schema.json`
   is also embedded at `internal/rules/schema/rule-bundle.schema.json`, and six
   schemas are embedded under `internal/contracts/schemasync`.

   ```powershell
   & "C:\Program Files\Go\bin\go.exe" test -p 1 -count=1 ./internal/contracts/...
   ```

   A bundle validated against a contract nobody published is worse than no
   validation.

4. **Regenerate, never hand-edit.** `internal/contracts/securityv1/*.pb.go` is
   protoc output. Hand-editing it is the one thing that silently breaks the wire:
   protoc overwrites the change on the next generate, and until then the Go type
   and the `.proto` disagree with no compile error anywhere.

   ```powershell
   protoc -I contracts/protobuf --go_out=. --go_opt=module=github.com/Sergey-Bar/Ai-Captain contracts/protobuf/security/v1/*.proto
   ```

5. **Then the owner package.** Only now.

6. **Then the test.** Named in the table. If the change adds a control, red-check
   it — see the `red-check` skill. A new test that has only ever passed is a
   comment.

7. **Gate it.** The checks that prove steps 3 and 4 are already gates; run them.

   ```powershell
   & "C:\Program Files\Go\bin\go.exe" run ./tools/contractcheck -mode check -baseline contracts/compat/baseline.json -root contracts/protobuf
   & "C:\Program Files\Go\bin\go.exe" run ./tools/contractcheck -mode check -baseline contracts/compat/baseline.json -root contracts/json-schema
   & "C:\Program Files\Go\bin\go.exe" run ./tools/contractcheck -mode check -baseline contracts/compat/baseline.json -root contracts/openapi
   ```

## The two copies, and which one to believe

There are exactly two non-obvious copies, and they fail differently:

| Copy | Failure if you edit the wrong one |
|---|---|
| `internal/contracts/securityv1/*.pb.go` | Silent. Regenerated away, no compile error |
| `internal/rules/schema/rule-bundle.schema.json` | Loud. `make contract-sync` fails |

`contractcheck -mode write` re-baselines. **Use it only when a new contract
version is intended**, because it makes every breaking change compatible by
definition — which is the opposite of what the gate is for.

## Rules

- **Never edit `internal/contracts/`.** It contains only
  `contractcompat_test.go`. If you are looking for the `Event` type, it is
  `internal/contracts/securityv1/event.pb.go`, which is generated.
- **Never edit an applied migration.** Add a higher-numbered one.
  `TestVerifyChecksumsCatchesDrift` exists to catch exactly this.
- **Expand and contract ship in separate releases.** A contract step may never be
  bundled with the expand step that makes it safe.
- **Contracts, migrations, invariants, dependencies, auth, crypto and the rule
  language require a human to read the diff before they land.** This skill
  produces the order and the evidence; it does not replace the signature.
  `docs/proposals/hosted-model-attribution.md` is the worked example of doing
  everything that is doable and stopping at the line.