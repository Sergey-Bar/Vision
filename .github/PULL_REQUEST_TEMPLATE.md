<!--
Kept short on purpose. A long template gets skipped, and a skipped template is
worse than none. The four checkboxes below are the ones that have actually
caught mistakes in this codebase.
-->

## What this changes

<!-- One or two sentences. The commit message says how; this says why. -->

## Checklist

- [ ] I did not weaken an invariant in `AGENTS.md`. If I did, this PR contains
      an ADR and a test that fails without the change.
- [ ] Contracts changed in `contracts/` first, `make generate` was run, and the
      result is committed. `internal/contracts/` was not hand-edited.
- [ ] Any new migration is forward-only, declares its phase, and does **not**
      bundle a `contract` step with the `expand` step that makes it safe.
- [ ] `go test -p 1 -count=1 ./...` passes **with a database present**, and I
      confirmed the database-backed suites actually ran rather than skipped.

## Durability

- [ ] If this touches the ingest path, the outbox, job claiming, evidence writes
      or action execution, there is a `kill -9` fault-injection test at the stage
      it touches. This is a Definition of Done item, not a nice-to-have.
- [ ] If this touches an evidence path, there is a negative test for `../`,
      absolute paths, null bytes and symlink escapes.

## AI and the rule language

- [ ] Any rule consuming a Tier C signal declares `if_ai_unavailable`.
- [ ] No AI output is asserted as fact; it is an attributed record.
- [ ] No VLM capability was promoted on the strength of a demo.

## Security

- [ ] No secrets, tokens, credentials or raw biometrics in logs or fixtures.
- [ ] Rule bundles, release archives and model bundles are still treated as
      untrusted input at the point of execution.
- [ ] `tenant_id` is not read from a request body anywhere I added.

## Notes for the reviewer

<!-- Anything the diff does not make obvious: a deliberate shortcut, a piece of
     debt you are taking on knowingly, or a decision you want challenged. -->
