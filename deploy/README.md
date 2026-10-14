# deploy

What runs on a customer's machine: the AI runtime, the service units, and the
verification an operator runs before and after an install.

| Path | What it is |
|---|---|
| `ai-runtime/runtime.py` | The AI plane's inference service. A separate process, standard library only, **no model**. |
| `ai-runtime/test_runtime.py` | 21 contract tests, no third-party dependencies either. |
| `systemd/security-core.service` | The Linux unit. A reference unit, not installed by any command. |
| `windows/SecurityPlatformCore.xml` | The Windows service definition. A design record, not an installable artefact. |

Full design, and the reasoning behind all of it:
[`docs/roadmap/engineering/release-sre-and-support.md`](../docs/roadmap/engineering/release-sre-and-support.md).

**Read this before you trust the directory.** Two things in it are not what the
roadmap says they are, and both are covered below:

- There is no `cmd/security-core` in the tree. `make build` produces
  `securityctl` and `security-migrate`, and nothing else. Both service units
  name a binary that does not exist yet.
- The AI runtime has no model, no weights and no GPU, by design and by
  documentation. It is not a stub that pretends.

## The AI runtime

`deploy/ai-runtime/runtime.py`. 526 lines, Python 3.11+ syntax, and the standard
library only — `base64`, `hashlib`, `json`, `os`, `time`, `dataclasses`,
`http.server`, `pathlib`, `typing`. CI enforces that by grepping the imports and
failing the build on anything else, because a service that runs on a customer's
site and touches camera footage is not the place to take a dependency it does
not need.

### Why it is a separate process

The plan's constraint is that AI is never on the availability or boot critical
path, and a library call inside the core cannot honour that: a hung inference
would hang the ingest pipeline. A separate process with its own service account
and its own database grants can be killed, restarted, or simply absent, and the
core keeps ingesting and verifying. Invariant 3 is a boundary, not a code path.

### What it does

- **Enforces its own request/response contract.** `API_VERSION = 1`,
  `RUNTIME_VERSION = "ai-runtime/1"`. A request whose `apiVersion` is not 1 is
  refused with 400 before anything is parsed. A silent field rename is how a
  version skew becomes a wrong observation.
- **Verifies the signed model bundle before loading anything from it.**
  `ModelBundle.verify()` requires `manifest.json`, requires `manifest.sig`,
  requires a non-empty public key, refuses an empty manifest, and reads
  `model`, `modelHash`, `promptTemplateId` and `promptVersion` out of it. A
  manifest that names no model hash is refused, because an observation citing it
  could not name the model. See the supply-chain caveat below — this check does
  less than it says.
- **Verifies every piece of evidence against its recorded hash** before it could
  be shown to a model. `Evidence.verify()` returns the reason an observation
  citing evidence the model never actually saw is worse than no observation: it
  looks corroborated.
- **Attributes everything.** `InferenceResult.to_response()` carries
  `model`, `modelHash`, `runtimeVersion`, `promptTemplateId`, `promptVersion`,
  `modelBundleSignature`, `inputEvidenceHashes`, `outputSchemaVersion`,
  `latencyMs`, `hardware` and `outputHash` — every field `AIObservation` in
  `contracts/protobuf/security/v1/observation.proto` requires. An observation
  with no attribution is not stored.
- **Computes a stable input hash.** `InferenceRequest.input_hash()` sorts
  evidence by `(evidence_id, sha256)` rather than by id alone. Evidence ids are
  primary keys in practice so the second component never decides anything, but a
  sort that is not a total order makes the hash depend on the order the caller
  happened to pass, and a hash that moves is a hash that cannot tell two runs
  apart.
- **Validates output against the profile's schema.** `validate_output()` checks
  the top-level field set and the `required` list — not types, because a full
  JSON Schema validator is a dependency the service deliberately does not take,
  and the field set is the part that matters: an undeclared field is one nothing
  downstream validates.
- **Returns a reason on every failure path.** `InferenceUnavailable` is
  *raised*, never returned, because every caller in the process has to handle it
  and a return value invites a caller that forgets. The reason travels with it
  so the observation records why the plane was absent — the difference between
  a pilot measuring AI availability and a pilot assuming it. The vocabulary is a
  closed set (`ALLOWED_DEGRADATIONS`): `none`, `model_unavailable`,
  `no_runtime`, `timeout`, `error`, `absent`. It is the same set the Go schema's
  CHECK constraint allows, and `InferenceUnavailable.__init__` raises `ValueError`
  on anything else.

### What is missing

A model. No weights, no GPU, no vendored engine. What is **present** is the
engine seam and the investigator role that binds to it: `Runtime.bind_engine`
loads an operator-supplied module from `SP_ENGINE_PATH`, and
`Runtime.run_inference` calls its `investigate()` and refuses an answer that
cites a fact it was not given. With no engine bound, the same path produces
the typed refusal that used to be the whole of it:

```python
raise InferenceUnavailable(
    DEGRADED_NO_RUNTIME,
    "a signed bundle is present but no inference engine is bound (SP_ENGINE_PATH). ...",
)
```

The engine contract is small and documented where it is implemented:
`describe()` returns `{model, model_hash, hardware, prompt_template_id,
prompt_version}` and `investigate(request)` returns `{structured_output,
insufficient_evidence}`. A module that verifies but does not expose them, one
that fails to import, and one whose `model_hash` disagrees with the signed
bundle are all refusals — and a `SP_ENGINE_PATH` that is set but loads to
nothing is a **startup failure**: one reason line naming the variable, exit 2.
systemd's `Restart=on-failure` then restarts until the file is fixed, which is
the honest outcome for "the operator said a model is installed".

`Runtime.run()` returns typed refusals on every other path too: no evidence
supplied is `DEGRADED_ABSENT` ("absence, not a negative"), an evidence hash
mismatch is `DEGRADED_ERROR`, and a runtime that could not load its bundle
raises with whatever reason the load produced.

### What that means at run time

With no model bound, **the plane starts, reports itself unavailable, and every
observation is recorded as degraded.** That is not a failure mode to route
around; it is the behaviour the product promises when the AI plane is
unavailable.

Concretely, from `GET /healthz`:

```json
{"status": "ok", "apiVersion": 1, "runtimeVersion": "ai-runtime/1",
 "modelAvailable": false, "reason": "model_unavailable",
 "detail": "no model bundle is configured at SP_MODEL_BUNDLE", "uptimeSeconds": 0.0}
```

`status` is `ok` — the service is up. `modelAvailable` is `false` — the service
has nothing to say. A missing bundle is a supported production configuration,
not a startup error: `build_runtime()` returns `Runtime(None)` when
`SP_MODEL_BUNDLE` is unset and the server runs anyway.

`POST /v1/infer` answers **200, not 503**, with a degraded body:

```json
{"apiVersion": 1, "observationId": "...", "profileId": "...",
 "structuredOutput": {}, "insufficientEvidence": true,
 "degradedReason": "no_runtime", "degradedDetail": "...",
 "model": "", "modelHash": "", "inputEvidenceHashes": [],
 "outputSchemaVersion": 0, "latencyMs": 0, "hardware": "",
 "outputHash": "..."}
```

200 because the request *was* answered — the answer is that the plane could not
answer, and it carries a reason. A 503 would make a caller retry a condition
that will not change.

**`insufficientEvidence` is `true` on every degraded response**, not only on the
insufficient-evidence one. That is the load-bearing detail: a consumer that
ignores the `degradedReason` field entirely still fails closed, because the one
boolean a rule could read says "cannot support a determination", which produces
`UNVERIFIED` and never a negative.

### Why there is deliberately no stub

There is no placeholder that returns a plausible answer, and there deliberately
is not. A stub returning `{"restrictedZoneBreach": false}` would produce a
`CORROBORATED` incident on a fabricated negative — the single worst failure this
product can have, because it is indistinguishable from a correct one until
somebody is harmed by it. The tests assert the degraded path instead.

Everything around the missing body is written and tested: verification,
attribution, validation, degradation, the reason vocabulary, the evidence hash
check, the input hash, the output hash. What a model adds is one function.

### Configuration

| Variable | Default | Meaning |
|---|---|---|
| `SP_AI_LISTEN` | `127.0.0.1:8800` | `host:port`. A non-numeric port or a missing colon exits 2. |
| `SP_MODEL_BUNDLE` | unset | Directory holding `manifest.json` and `manifest.sig`. Unset means no model, which is a supported configuration. |
| `SP_MODEL_BUNDLE_SIGNATURE` | unset | Passed to `ModelBundle` as `signature`. |
| `SP_MODEL_BUNDLE_KEY` | unset | The vendor public key. **Never used beyond a non-empty check** — see below. |
| `SP_ENGINE_PATH` | unset | A Python module exposing `describe()` and `investigate()`. Unset means no engine, which is a supported production configuration: every observation degrades with `no_runtime` and the investigator answers with that named reason. Set but unloadable is a startup failure — one reason line, exit 2. **On an upgrade of an existing install the env file is preserved verbatim (`keepExisting` in `internal/lifecycle`), so this line is NOT added for you: add it by hand.** |
| `SP_HANDOVER_DIR` | unset (core) | Where the shift hand-over artifact is written and read. Unset means no artifact: the pipeline runs without it and the console's `/handover` page says so by name. **Same `keepExisting` rule applies on an upgrade.** |

`Handler.log_message` is overridden to return nothing, so the default
request-line logging (which can include a query string) never happens. Every
response carries `Cache-Control: no-store`: this service is on the site LAN and
holds camera footage in flight. Request bodies are capped at 32 MiB.

### Running its tests

```sh
cd deploy/ai-runtime && py -m unittest discover
```

CI runs the same suite with a path and verbosity, under Python 3.12:

```sh
py -m unittest discover -s deploy/ai-runtime -v
```

21 tests in six groups: `MissingBundle` (4 — a site with no model is supported,
not broken), `BundleVerification` (6 — tampered manifest, unsigned bundle, no
verification key, no model hash, and a verified bundle that still degrades
because there is no engine), `EvidenceIntegrity` (4), `OutputValidation` (4),
`DegradationVocabulary` (2), `RequestParsing` (1).

## Supply-chain caveat: the bundle check verifies a digest, not a signature

**This is an open finding, documented rather than fixed, and it is load-bearing
for the product's supply-chain claims.**

`ModelBundle.verify()` (`runtime.py:238`) has a docstring that reads "Check the
bundle's signature against the configured public key" and a failure message that
reads "the bundle signature does not match its manifest". Neither is what the
code does. It:

1. computes `sha256(manifest.json)`;
2. reads `manifest.sig` and compares its **contents** to that digest;
3. checks only that `self.public_key` is non-empty.

The key is never used. A bundle whose `.sig` file contains the manifest's SHA-256
is accepted exactly as if it were signed — and a bundle whose `.sig` contains a
real signature over the same bytes is rejected, because those bytes will not
match a hex digest. `test_a_correctly_signed_bundle_loads` writes the digest
into `manifest.sig`, so the test suite encodes the digest semantics as if they
were the intended ones.

The honest name for the current behaviour is *a digest pin*: it detects a
manifest edited after the `.sig` was written, which is a real protection, and it
is not a signature, which is what "the model bundle is signed" claims. It is
recorded as finding C2 in
[`docs/roadmap/master-roadmap.md`](../docs/roadmap/master-roadmap.md) §"What
this review got wrong", which calls it "the one finding that undermines a
supply-chain claim in a document that is otherwise careful about them", and
which names it alongside C3 as one of the two defects to fix this week.

Do not treat `verify()` as a signature check in any threat model, ADR, sales
conversation or customer security review until it is one. Do not "fix" it
without an ADR: real signature verification changes the deployment contract
(where the vendor key lives, how bundles are re-issued, what a rotation means),
and `AGENTS.md` requires the ADR before the change.

## The service units

Both files are reference artefacts. **No code in the tree reads either of them**,
and `securityctl install` does not consume them — the installer verifies the
archive, creates the program directory and applies the migrations.

**An upgrade preserves the env files verbatim.** `internal/lifecycle` marks both
env templates `keepExisting: true`, and `securityctl upgrade` never rewrites
them, so a variable added to the template in a later release is **not** present
on an upgraded site until an operator adds it by hand. That is the deliberate
trade — a rewritten env file would silently undo a site's own settings — but it
means the two variables this release added (`SP_ENGINE_PATH`,
`SP_HANDOVER_DIR`) must be added by hand on an upgrade, while a fresh install
gets them from the template. `securityctl preflight` says which of the required
variables are absent.

### `systemd/security-core.service`

| Declares | Value |
|---|---|
| Type | `Type=simple`. It was `Type=notify` with `NotifyAccess=main`, and nothing in the tree sends `READY=1` — see the caveat below. |
| Ordering | `After=network.target`, and **nothing else**. No `Requires=` on Postgres or on a network mount, deliberately: "a service that refuses to start because a network mount is slow is a service that takes a site down at boot". |
| Service account | `User=security`, `Group=security` — a dedicated unprivileged account, so a compromised core cannot read the signing keys or write the evidence root's parent. |
| Program directory | `WorkingDirectory=/opt/security-platform`, `ExecStart=/opt/security-platform/bin/security-core`, `ExecReload=/bin/kill -HUP $MAINPID`. |
| Environment | `EnvironmentFile=-/etc/security-platform/security-platform.env` (the leading `-` makes it optional), plus `SP_TENANT_ID=tenant-local` and `SP_PROGRAM_DIR`. No time server, no DNS — a site has no network, and time comes from the host's NTP discipline measured against the VMS. |
| Restart policy | `Restart=on-failure`, `RestartSec=5s`, `StartLimitBurst=5`, `StartLimitIntervalSec=60`. Crash-looping is worse than stopped: an operator needs a stopped service and a log. |
| Log directory | None declared. The unit writes to stdout/journald. |
| Hardening | `NoNewPrivileges`, `PrivateTmp`, `PrivateDevices`, `ProtectSystem=strict`, `ProtectHome`, `ProtectKernelTunables`, `ProtectKernelModules`, `ProtectControlGroups`, `RestrictNamespaces`, `RestrictRealtime`, `RestrictSUIDSGID`, `LockPersonality`, `MemoryDenyWriteExecute`, `SystemCallArchitectures=native`, `SystemCallFilter=@system-service`, empty `CapabilityBoundingSet` and `AmbientCapabilities`, `RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6`. |
| Filesystem access | `ReadWritePaths=/var/lib/security-platform /var/lib/security-platform/evidence`. `ReadOnlyPaths=/opt/security-platform /etc/security-platform`. |
| Resource limits | `MemoryMax=4G`, `TasksMax=512`, `LimitNOFILE=16384`. |

### The packaging caveat: the evidence directory must stay writable

`ProtectSystem=strict` means the service can write **nowhere** it has not been
named, and the two named paths are the data directory and the evidence root.

So:

- **The evidence root must be under one of the `ReadWritePaths`,** or the site
  must amend the unit to add it. The release-SRE topology (§2.1) draws the
  evidence store as a *separate box* from the application host; on any real
  layout where evidence lives on its own volume, that volume's mount point has
  to appear in `ReadWritePaths` or the core cannot write a single byte of
  evidence.
- **Hardening the evidence path — read-only mount, immutable attribute, a
  read-only bind mount — breaks evidence capture**, and with it the evidence an
  incident cites. `securityctl preflight` checks the evidence root is writable by
  this account and fails fatally when it is not (`checkDirUsable` writes and
  removes a `.preflight-write-probe` file), so this is caught before an install
  rather than after one.
- This is stated in the M1 execution plan (item M1-T28) as the acceptance
  criterion: "The evidence directory is the one path that must be writable;
  hardening that would break evidence is a bug, and the test should catch it."

### `windows/SecurityPlatformCore.xml`

Declares the service name `SecurityPlatformCore`, display name "Physical
Security Intelligence Layer", executable `security-core.exe`, the virtual service
account `NT SERVICE\SecurityPlatformCore` (no password, no interactive logon;
LocalSystem is rejected because it can read the release signing key), failure
actions of restart twice at 5 s then stop, a 24-hour failure reset, and an
**empty** dependency section for the same reason the systemd unit has no
`Requires=`.

It is a design record, not an installable artefact. The header states it is
installed with:

```
sc.exe create SecurityPlatformCore binPath= "C:\Program Files\SecurityPlatform\bin\security-core.exe" start= auto
```

and configured by the installer. The XML's own shape
(`<configuration><system.service>`) is not something `sc.exe` consumes; it
documents the intended configuration for whoever implements the installer's
service-creation step. The `<stoptype>windows9x</stoptype>` and
`installation="disabled"` attributes look like leftovers from a generic template
and mean nothing to Windows.

### What a real installation still has to decide

- **The capture path is not in the service.** `cmd/security-core` exists,
  builds, and runs: it reads and validates configuration, serves the API, runs
  the job loops, and shuts them down in a defined order. What it does not do is
  capture video. The RTSP receiver, the muxer and the detector live in
  `internal/rtsp`, `internal/mp4` and `internal/detector`, and today only
  `securityctl poc` drives them. Every `ExecStart` names a binary that is real
  and resident; the question the install still has to answer is where the camera
  loop runs - in this process, or beside it. `internal/dbx` still sets the
  Postgres `application_name` to `"security-core"`, which is the naming remnant.
- **`Type=notify` is fixed: the unit now declares `Type=simple`.** The caveat was
  that there is no `sd_notify` call and no `NOTIFY_SOCKET` handling anywhere in the
  tree, so a unit declaring `Type=notify` whose process never sends `READY=1` waits
  out systemd's start timeout, is killed, and burns a `StartLimitBurst=5` attempt —
  a service that never starts for a reason that is invisible in the unit file.
  `Type=simple` considers the process started once it is forked, and readiness is
  then what `/v1/health/ready` is for. The `NotifyAccess` line went with it.
  `cmd/securityctl`'s `deploy_unit_test.go` asserts both, so the unit cannot drift
  back into a state where it waits for a message nothing sends. If a notifier is
  ever added, the correct change is the notifier *and* `Type=notify`; a test will
  then say so rather than this comment.
- **Binding a model runtime into this unit requires revisiting the hardening.**
  `MemoryDenyWriteExecute=yes` forbids writable-executable mappings, which a GPU
  runtime or any JIT needs; `SystemCallFilter=@system-service` will not pass a
  vendor driver; `PrivateDevices=yes` hides `/dev/nvidia*` and the rest. The
  current hardening is correct for a core with no model and will have to be
  revisited as a deliberate, reviewed change when one is bound.
- Where each of the six site-config paths lands: `program`, `data`, `evidence`,
  `logs`, `secrets`, `checkpoints` are separate directories with separate
  permissions, and the unit's `ReadWritePaths`/`ReadOnlyPaths` have to be edited
  to match a real site's layout.
- The account, the `security` group and the ACLs — the unit assumes they exist.

## Offline and air-gapped

### What works today, with no network

- `securityctl verify-release --archive <a.tar.gz> --trusted-key <hex>` — the
  command to run on a USB stick at an isolated site. It "contacts nothing, and
  it is the only thing standing between a tampered archive and code execution on
  every machine that installs it" (`cmd/securityctl/release.go:110`). It refuses
  a tampered archive, an unsigned one, a manifest whose file hashes do not match
  the contents, a path that escapes the archive root, credential-shaped
  material, and a schema window this binary cannot support.
- `securityctl audit verify-seals --sink <dir> --trusted-key <hex>` — no
  database, no network. The air-gapped audit path.
- `securityctl preflight` — read-only, "changes nothing, contacts nothing".
- The AI runtime — no dependencies to resolve, no telemetry, no update check, no
  licence check. It reads a bundle off local disk or nothing at all.
- The release archive is self-contained: binaries, `migrations/`,
  `rules/builtin/bundle.yaml`, `contracts/`, the SBOM, the manifest and its
  signature.

The design rule is absolute: **no runtime component fetches anything at run
time.** No CDN, no licence server, no telemetry, no crash reporter, no update
check. A product that phones home is a product an air-gapped site cannot run.

The trusted public key is a **separate transfer** — different media, different
person, or already on the site from a prior install. An air-gapped site whose
only copy of the signing key arrives inside the archive it is meant to verify is
not verifying anything. `securityctl release-key --signing-key <path>` is what
produces the hex half to distribute.

### Open problems

| Problem | State |
|---|---|
| **Offline licence activation** | Not decided. release-sre §3.3 lays out five options and recommends **E** — perpetual per-site licence with a contractual renewal date tracked in the fleet view and never enforced on site. The reasoning is that a security product that goes dark because a licence lapsed is the one failure a customer cannot tolerate. Until the ADR lands, every design partner's agreement says in writing that the licence is perpetual and the product will not be disabled for non-payment. |
| **Model bundle distribution** | Designed, not implemented. release-sre §3.4 specifies `securityctl model-bundle install <path>.tar.gz --key trusted.pub` with six ordered steps (verify signature under the trusted key, verify every file is listed and hashed, verify the declared hardware profile matches *this* host, verify permitted uses, extract to a versioned directory, do **not** activate). That command does not exist in `cmd/securityctl`. Today a bundle reaches the runtime by pointing `SP_MODEL_BUNDLE` at a directory. Also: deleting a bundle whose observations are still cited breaks a replay, which is an invariant-2 problem before it is a disk-space one. |
| **Update package distribution** | release-sre §3.5. Air-gapped: encrypted USB or physical handover, checksum printed, `verify-release` first. Intermittent WAN: the customer pulls it and verifies locally — "the product never fetches it itself". Connected (M5 fleet): the central plane lists versions, and "the site still verifies the signature locally before installing". One rule, no exceptions: **nobody's site ever receives a binary that has not been verified against a local trusted key.** |
| **Supported OS / hardware matrix** | release-sre §3.6. Windows Server 2019 and 2022 (x86-64) and Ubuntu 22.04 / 24.04 LTS (x86-64) are supported in M1. Windows 10/11 Pro is for the operator workstation and `securityctl` only, M2. Windows Server 2016 and macOS are not supported for the service. 32-bit x86: never. ARM64 is **gated, decision by M2** — if a design partner or reseller names an ARM site by the end of M2 it is an ADR and an M3-quarter project; if not, it stays unsupported and is stated as such. The rule for adding a platform: a release artefact, a service unit, a path-traversal negative test on that platform's filesystem semantics, and a customer. Anything short of all four is "not supported", said as such, rather than "probably works". |
| **The Go client for the AI runtime** | Built and answering. `internal/airuntime.Client` POSTs `/v1/infer`; `VLMWorker` consumes `QueueVLM`, fetches the cited evidence with the investigate schema, and records the observation. `deploy/ai-runtime/engines/fact_investigator.py` is the shipped engine (deterministic, over stored facts) and `build_bundle.py` signs a bundle the runtime verifies before loading. The `boot` gate records a live, non-degraded, cited `investigate/v1` observation end to end. What remains: the GPU-backed model behind the same `investigate` (plan item 1.2), and the `ai.*` fields in `site-config.schema.json` wired to `SP_AI_ENDPOINT` in the install path — the core reads the env var directly today. |
| **Automated database restore** | Deliberately absent. `restore --verify` checks the backup's hashes and reports what a restore would do, then refuses to perform it, "because a restore that guesses at a connection string, a role and a password fails silently on a site nobody is watching". Restoring the dump is a deliberate act with the operator's own tooling. |
| **No OCI profile** | Deliberately absent, and §4.1 explains why: containers on an isolated site need either a registry we cannot reach or a bespoke image the customer's team maintains, and "a site that has to maintain our container is a site that will not upgrade". systemd and the Windows Service cover the market. |

## How to verify an install

Every command below is a real subcommand with the flags it actually accepts.
Run them in this order on the target host. The first three need no database.

### 1. The release signature and the manifest

On the networked machine that produced the artefact:

```sh
securityctl release-key --signing-key /path/to/private.ed25519.hex
```

At the site, against a **trusted key obtained out of band**:

```sh
securityctl verify-release --archive release-<version>-<platform>.tar.gz --trusted-key <hex>
```

Add `--json` for machine-readable output. It also accepts `--manifest` if the
manifest is not beside the archive. If it fails, nothing has been written and
nothing has been touched. Without `--trusted-key` the archive is checked against
the key it carries, which proves only that whoever wrote the archive signed it —
the command warns you about this.

### 2. Preconditions

```sh
securityctl preflight --evidence-root /var/lib/security-platform/evidence --data-root /var/lib/security-platform
```

Read-only. It asserts the embedded contract version and schema window, the
number of embedded migrations, the platform, that the evidence and data
directories exist and are **writable by this account**, and the database's schema
window. It prints `N fatal, M advisory` and exits non-zero on any fatal. On a
host with no database yet, `--require-db=false` skips that one check.

### 3. The schema window

```sh
security-migrate status          # human-readable
securityctl migrate status       # JSON, same report
```

Look for `compatibility  : OK` and `pending        : none` after a completed
install. `OUT OF RANGE` means the binary and the database disagree about the
schema; see [`migrations/README.md`](../migrations/README.md).

```sh
security-migrate verify          # or: securityctl migrate verify
```

Confirms every applied migration still matches this build. The output is
`migrations: all applied checksums match this build`.

### 4. Install

```sh
securityctl install --archive release-<version>-<platform>.tar.gz \
                    --program-dir <dir> \
                    --trusted-key <hex> \
                    --dry-run
```

Read the plan, then drop `--dry-run`. The command verifies the signature, the
manifest, every file hash, the schema window and the expand/contract discipline
before extracting a byte, and extraction re-checks every archive entry
independently.

### 5. Live state

```sh
securityctl doctor
```

Read-only diagnostics: binary platform, the schema window, and a standing
statement that the AI plane is not consulted. `doctor` deliberately does not
require the AI runtime — "AI availability is not an SLO and is never a reason
the core is unhealthy."

### 6. The audit chain

```sh
securityctl audit verify                      # from=1 by default; --json available
securityctl audit checkpoints
```

`audit verify` recomputes the hash chain and reports the first break. On a break
it tells you not to repair the rows — "a repair destroys the only evidence that
something was wrong" — and exits non-zero.

### 7. Seal the checkpoint

```sh
securityctl audit seal --signing-key <ed25519.hex> --sink <dir> --key-id checkpoint-1
```

`--signing-key` defaults to `$SP_CHECKPOINT_KEY`, `--sink` to `$SP_CHECKPOINT_DIR`,
and both are required; the command refuses with the reason if either is missing:
"the sink must not be writable by anyone who can reach the database, or a seal
proves nothing." It prints the verification command to run next, with the key
fingerprint filled in.

### 8. Verify the seal offline

```sh
securityctl audit verify-seals --sink <dir> --trusted-key <hex> --latest <highest-sequence>
```

No database required. It checks contiguity across the seal set and warns if the
seals reach further than the database you pointed it at. Without `--trusted-key`
it warns that each seal is being checked against the key it carries, "which
proves only that whoever wrote the seal signed it".

### 9. The rest of the lifecycle

```sh
securityctl upgrade --archive <a.tar.gz> --program-dir <dir> --trusted-key <hex> \
                    --backup-dir <dir> [--dry-run] [--skip-backup]
securityctl rollback --program-dir <dir>
securityctl backup --out <dir> --dump <dump.sql> \
                   [--evidence-dir <dir>] [--checkpoints <dir>]
securityctl restore --backup <dir> [--program-dir <dir>] [--verify]
```

`upgrade` is backup, then stage, then swap, and it keeps the previous program
directory rather than deleting it, so a rollback does not first have to rebuild
the version it is rolling back to. `--skip-backup` exists and says the operator
accepts the risk. `rollback` reads the *previous build's own* declared schema
window and refuses when the database has moved past it, "because a half-downgraded
site is the state this product must never be in". `backup` is self-consistent or
it is recorded as partial with the reason; a dump without the evidence directory
is "an archive whose incidents cite bytes that are not in it". `restore` verifies
the manifest and **recomputes** the database hash rather than trusting the
recorded one, then refuses to restore.

### 10. The AI runtime, if you are running one

```sh
cd deploy/ai-runtime && py -m unittest discover    # 73 tests, standard library only

SP_AI_LISTEN=127.0.0.1:8800 python3 deploy/ai-runtime/runtime.py
curl -s http://127.0.0.1:8800/healthz
```

Expect `"status": "ok"` and `"modelAvailable": false` with a reason. That is a
correct, supported configuration. Anything that reports `modelAvailable: true`
today is a build with a bound engine that is not in this repository.

Remember finding C2 above: whatever bundle you point `SP_MODEL_BUNDLE` at has had
its **manifest digest** checked, not its signature verified.
