# Proposal: two contract changes the capture work needs

**Status: PROPOSED. Not applied.** `AGENTS.md` says an AI agent may not change a
published contract. This document is the reviewable form of two changes that
Phase 3 cannot be finished without, written so that a human can read the diff and
say yes or no rather than discover the gap later.

Both are **additive or a relaxation with a named replacement**. Neither removes a
guarantee, and neither is a migration that drops data.

---

## 1. `ai_observations.model_hash` — honest attribution for a hosted model

### The problem

`observations.Validate` refused an observation without a `ModelHash`, and its own
comment said *"a default here is a silent downgrade."* That is right for the
local runtime, whose Python process serves a model bundle with a content
signature we compute ourselves.

A hosted vision-language provider returns a model identifier, a response, and
nothing else. Its weights are not ours, are not addressable, and change on the
provider's schedule. So for that provider there is no hash to record, and the two
ways to make the column happy are both worse than leaving it empty:

- **Leave it empty and call the column required** — a lie about the schema, and
  every reader of the schema learns to ignore it.
- **Write a synthetic 64-character string** — a lie about the model. It satisfies
  a `NOT NULL` and a length check and defeats the one thing the column exists for:
  proving which weights answered.

### What the Go layer already does (and is verified)

Three honest fields, added to `observations.Observation` and
`airuntime.Result`:

| Field | Meaning |
|---|---|
| `model_identity` | What the provider reported it used. **A claim, not a verification.** |
| `model_contract_version` | This build's content hash over prompt template, prompt version, output schema version and API model name. Reproducible from the repository. |
| `temperature` | The sampling temperature that was requested. An answer at 0.9 is not the answer at 0.01. |

`Validate` now accepts an empty `ModelHash` **only** when `model_identity` and
`model_contract_version` are both present:

```go
case o.ModelHash == "" && (o.ModelIdentity == "" || o.ModelContractVersion == ""):
    return fmt.Errorf("%w: model hash is required unless the provider is named and pinned: "+
        "a model name is not an identity, and neither is a fabricated hash", ErrInvalid)
```

`TestTheAttributionNamesTheModelWithoutInventingAHash` red-checks this: replace
the empty hash with a zero hash and it fails. Verified.

### What is needed

**Migration** (expand only; no contract step bundled with it, per `AGENTS.md`).
The number is **0022**, not 0015: `0015_partitions_and_correlation_indexes.expand.sql`
has shipped, and the latest is `0021_site_isolation_restrictive.expand.sql`
(`migrations/compat.json` carries `maxSchemaVersion: 21`). A proposal that names an
applied number is a proposal whose migration cannot land, and it is the kind of
collision that is only visible to whoever reads the directory.

```sql
-- 0022_observation_hosted_attribution.expand.sql
ALTER TABLE ai_observations
    ADD COLUMN model_identity        TEXT NOT NULL DEFAULT '',
    ADD COLUMN model_contract_version TEXT NOT NULL DEFAULT '',
    ADD COLUMN temperature           DOUBLE PRECISION NOT NULL DEFAULT 0,
    ALTER COLUMN model_hash DROP NOT NULL;

-- A row with a hash must have kept the columns it always had. A row without one
-- must now say who answered and under which contract. The CHECK is the whole
-- point: it is what makes an invented hash impossible rather than discouraged.
ALTER TABLE ai_observations ADD CONSTRAINT ai_observations_attribution_ck CHECK (
       (model_hash <> '')
    OR (model_identity <> '' AND model_contract_version <> '')
);
```

Plus the matching contract step later, separately:
- `contracts/protobuf/security/v1/observation.proto`: `model_identity` (26),
  `model_contract_version` (27), `temperature` (28).
- `contracts/json-schema/` — the observation envelope, if one is published.
- `contracts/compat/baseline.json` — regenerate with `contractcheck -mode write`.

**Retrospective fill.** Existing rows keep their `model_hash` and get
`model_identity = ''`, which the CHECK accepts because the hash is non-empty. No
row is rewritten and no historical fact is touched.

### Why this is safe to review quickly

Nothing is removed. The column that was `NOT NULL` becomes nullable **only** for
rows that carry the two fields that replace it, and the `CHECK` makes that a
database-level invariant rather than a Go convention. A row that has neither a
hash nor a pinned identity cannot be written.

### The gap that remains until this lands

Until the migration is applied, an observation from a hosted provider cannot be
**stored** honestly: the column will reject the empty hash. The Go layer is
correct and the schema cannot yet hold the record it produces. The Stage 1 POC is
unaffected because it writes a *degraded* observation, which uses the documented
placeholder path.

---

## 2. `AIDegradation` — two first-class states the vocabulary cannot hold

### The problem

`observations.Degradations` is a **closed set pinned by a schema `CHECK`**. The
plan's P3.2 and P3.4 want two failures to be individually distinguishable:

| Plan item | Failure | What an operator must be able to do |
|---|---|---|
| P3.2 | the pinned region was refused | find out the site's data sovereignty blocked this, not that "something went wrong" |
| P3.4 | the site's daily query cap was reached | stop generating work, or raise the cap — a cap that is invisible is not a cap |

Neither is in the vocabulary, which currently holds: `none`, `model_unavailable`,
`no_runtime`, `timeout`, `error`, `absent`.

**The workaround in place, and it is a real one.** Every degraded result carries
a `reason=<code>` field in a fixed key=value shape, because the repository's own
rule is that nothing matches on prose. The codes are `cap_reached`,
`region_refused`, `region_unavailable`, `answer_malformed`, `asset_missing`,
`redirect_refused`, `timeout`, `error`.

So the *category* is shared and the *cause* is not. That is honest and it is
workable, and it is a worse product than a real enum: a consumer branching on
`Degradations[...]` sees one value, and only a consumer that also parses the
detail string can tell the two apart.

### What is needed

Two values added to the closed set:

| Value | Meaning |
|---|---|
| `cap_reached` | The site's budget is exhausted. Not a failure of the provider; a decision of the site, recorded as a state. |
| `region_refused` | A call would have left the pinned region, and was refused. A sovereignty decision, not a degradation of service. |

And the `reason=` field demoted from *the only* distinction to *a refinement*:
the category carries it, the detail explains it.

### Why it needs a migration

`AIDegradation` is an enum in the protobuf, a `CHECK` on a column, and a
`Degradations` map in Go. All three move together:

```sql
-- Same migration file as §1 is tempting and wrong. Expand and contract ship
-- separately, so this is its own expand step.
ALTER TABLE ai_observations DROP CONSTRAINT ai_observations_degraded_reason_ck;
ALTER TABLE ai_observations ADD CONSTRAINT ai_observations_degraded_reason_ck
  CHECK (degraded_reason IN (
    'none','model_unavailable','no_runtime','timeout','error','absent',
    'cap_reached','region_refused'));
```

The old values are untouched and no row is rewritten, so this is an expand step
with no contract step and nothing depends on the deploy order.

### The review question

Is a refusal the same kind of thing as a degradation? `region_refused` is
arguably not a degradation at all — it is a correct action, and the product is
*working* by declining. If so it should be a distinct concept rather than a new
member of a set called `Degradation`, and that is a naming decision worth making
deliberately rather than inheriting from my choice of enum.

---

## What I would do next, in order

1. Read §1's `CHECK` and say whether the replacement is the right one. It is the
   single change that unblocks storing a hosted model's answer honestly.
2. Decide §2's naming question — refusal versus degradation — before the SQL.
3. Then, and only then, apply. Both changes are mechanical once the shape is
   agreed, and the Go layer is already written and tested against it.
