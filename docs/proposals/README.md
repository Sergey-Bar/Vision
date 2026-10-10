# Proposals

**None of this is implemented. Nothing here is a decision.**

These documents are proposals only. Each was written during a review of
free and open-source projects against the gaps in `docs/status.md` and the traps
in `internal/README.md`. No dependency was added, no file outside this directory
was touched, and no capability was activated.

`AGENTS.md`: an AI agent "may not change an invariant, change a published
contract, activate a capability, contact anyone, or commit to a date. It may
write code, tests and documentation, and it may propose. A human signs every
exit gate." That is the whole authority under which this directory exists.

## The proposals

| # | Proposal | Closes | Confidence in the gap |
|---|---|---|---|
| [01](01-adapter-probe.md) | `securityctl adapter probe` | The eight `Unverified: true` routes; DoD item 1 | **High** — the command is referenced in five places in the code and registered in none |
| [02](02-rls-gates.md) | Generated and wider-catalog RLS gates | Drift in a schema whose policies are tested only by their authors' beliefs | High, but see the caveat |
| [03](03-webauthn-attestation.md) | Attestation trust chain for WebAuthn | A gap `docs/status.md` states outright | High on the gap, **low on the recommendation** |
| [04](04-openapi-breaking-change-gate.md) | Breaking-change gate for `openapi.yaml` | A published contract with no checker at all | **High** |
| [05](05-macavity-fault-injection.md) | Deterministic fault injection | `kill -9` proves the process dies; nothing proves the *database* fails safely | High |
| [06](06-buf-proto-toolchain.md) | `buf` lint and format for protobuf | No schema lint or format gate | Medium — buys consistency, not safety |
| [07](07-imageserver-h264-evidence.md) | ImageServer H.264 for evidence | Evidence volume, and a dual-token reality the route table does not model | High on the architecture, **low on the efficiency claim** |
| [08](08-vlm-model-evaluation.md) | Evaluating a third-party VLM against the existing gates | DoD item 3 | **Low on the models, high on the process** |
| [09](09-evaluated-and-rejected.md) | — | A record of seven plausible projects that would make the product worse | High |

## Reading order

If you read one, read **01**. It is the only proposal that closes a Definition
of Done item, and the code already contains a field, an exported accessor and
five references for a command that does not exist.

If you read two, read **09** next. It is the one that says no — seven times, to
projects that are all good and all popular, each with the reason the obvious
comparison is the wrong one.

## Standing rules observed by everything in this directory

- **No dependency is added.** Every proposal that names a library says so, and
  several conclude the library should not be adopted.
- **No benchmark number is asserted.** Where a project claims a result, it is
  attributed as that project's claim, with the source named. `README.md`
  requires unverified benchmark numbers to be marked unverified with a source
  and a date. That rule is applied to third parties here, not only to vendors.
- **No licence is assumed.** Where a licence was not confirmed, the document
  says so and makes it a blocker. An earlier draft of this review asserted MIT
  for two projects that turned out to be Apache-2.0 and BSD-3-Clause; the
  correct claims are in these files and the incorrect ones are not.
- **No invariant is proposed to be weakened.** Where a proposal would trade an
  invariant for convenience, the document says so and recommends against it.
  Several proposals end by recommending the status quo.
- **Nothing is committed to a date.** Ordering is given as dependency order
  where it exists, and as reading order where it does not.

## Status of this directory

Proposal. Adding it, and the order of it, is itself a human decision. It could
reasonably be rejected in full: the argument for it is that `AGENTS.md` already
requires proposals to be written down, and a proposal that lives only in a chat
window is lost the next time someone asks.
