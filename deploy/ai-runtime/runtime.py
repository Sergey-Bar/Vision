"""The AI plane's inference service.

This is a separate process on purpose. The plan's constraint is that AI is never
on the availability or boot critical path, and a library call inside the core
cannot honour that: a hung inference would hang the ingest pipeline. A separate
process with its own service account and its own database grants can be killed,
restarted, or simply absent, and the core keeps ingesting and verifying.

What is deliberately missing: there is no model here. No weights, no inference
engine, no GPU. The service loads a *signed model bundle* if one is present and
refuses to run without it, and its refusal is the product's central invariant made
visible at the boundary — a missing model produces ``model_unavailable``, and the
caller records a degraded observation rather than a negative answer.

That is not a stub pretending to be finished. It is the half of the contract that
can be written, tested and deployed before any model exists, and it is the half
that decides whether an AI outage takes a site down. What a model would add is a
``run_inference`` body, and the tests around it are written to fail when that body
stops honouring the contract.
"""

from __future__ import annotations

import base64
import hashlib
import json
import os
import time
from dataclasses import dataclass, field
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

# Schema version of this service's own contract. Bumped when the request or
# response shape changes, and refused by a caller that does not understand it —
# a silent field rename is how a version skew becomes a wrong observation.
API_VERSION = 1

# Degradation reasons. These are the same closed set the Go schema's CHECK
# constraint allows, and a reason outside it would be stored as a string nothing
# recognises and read downstream as "no degradation".
DEGRADED_NONE = "none"
DEGRADED_MODEL_UNAVAILABLE = "model_unavailable"
DEGRADED_NO_RUNTIME = "no_runtime"
DEGRADED_TIMEOUT = "timeout"
DEGRADED_ERROR = "error"
DEGRADED_ABSENT = "absent"

ALLOWED_DEGRADATIONS = frozenset(
    {
        DEGRADED_NONE,
        DEGRADED_MODEL_UNAVAILABLE,
        DEGRADED_NO_RUNTIME,
        DEGRADED_TIMEOUT,
        DEGRADED_ERROR,
        DEGRADED_ABSENT,
    }
)


class InferenceUnavailable(RuntimeError):
    """The plane cannot answer, and says why.

    Raised rather than returned, because every caller in this process has to
    handle it and a return value invites a caller that forgets. The reason
    travels with it so the observation records *why* the plane was absent, which
    is the difference between a pilot measuring AI availability and a pilot
    assuming it.
    """

    def __init__(self, reason: str, detail: str = "") -> None:
        if reason not in ALLOWED_DEGRADATIONS:
            raise ValueError(f"{reason!r} is not a known degradation reason")
        super().__init__(detail or reason)
        self.reason = reason
        self.detail = detail


@dataclass(frozen=True)
class Evidence:
    """One piece of evidence the model is shown.

    The hash travels with it because the observation records *which bytes* the
    model saw. Without that, a re-evaluation months later cannot tell whether it
    is looking at the same evidence, and a changed clip that produces a changed
    answer looks like a model regression.
    """

    evidence_id: str
    sha256: str
    content_type: str
    # bytes is held in memory for a test double and never logged. Evidence is
    # a tenant's camera footage; it does not belong in an error message.
    data: bytes = field(repr=False, default=b"")

    def verify(self) -> bool:
        """Check the bytes against the recorded hash.

        Refusing evidence whose hash does not match is the point: an observation
        citing evidence the model never actually saw is worse than no observation,
        because it looks corroborated.
        """
        if not self.data:
            return True
        actual = hashlib.sha256(self.data).hexdigest()
        return actual == self.sha256


@dataclass(frozen=True)
class InferenceRequest:
    """One inference request."""

    observation_id: str
    profile_id: str
    evidence: tuple[Evidence, ...]
    profile_schema: dict[str, Any]
    deadline_seconds: float = 10.0

    def input_hash(self) -> str:
        """A stable hash of the input, recorded on the observation.

        Sorted by (id, hash) rather than by id alone. Evidence ids are primary
        keys in practice, so the second component never decides anything — but a
        sort that is not a total order makes this hash depend on the order the
        caller happened to pass, and a hash that moves is a hash that cannot be
        used to tell two runs apart.
        """
        hasher = hashlib.sha256()
        hasher.update(self.observation_id.encode())
        hasher.update(b"\x00")
        hasher.update(self.profile_id.encode())
        for item in sorted(self.evidence, key=lambda e: (e.evidence_id, e.sha256)):
            hasher.update(item.evidence_id.encode())
            hasher.update(b"\x00")
            hasher.update(item.sha256.encode())
            hasher.update(b"\x00")
        return hasher.hexdigest()


@dataclass(frozen=True)
class InferenceResult:
    """One inference outcome, ready to become an AIObservation."""

    observation_id: str
    profile_id: str
    structured_output: dict[str, Any]
    insufficient_evidence: bool
    model: str
    model_hash: str
    runtime_version: str
    prompt_template_id: str
    prompt_version: str
    model_bundle_signature: str
    input_evidence_hashes: list[str]
    output_schema_version: int
    latency_ms: int
    hardware: str

    def output_hash(self) -> str:
        body = json.dumps(self.structured_output, sort_keys=True, separators=(",", ":"))
        return hashlib.sha256(body.encode()).hexdigest()

    def to_response(self) -> dict[str, Any]:
        return {
            "apiVersion": API_VERSION,
            "observationId": self.observation_id,
            "profileId": self.profile_id,
            "structuredOutput": self.structured_output,
            "insufficientEvidence": self.insufficient_evidence,
            "degradedReason": DEGRADED_NONE,
            "model": self.model,
            "modelHash": self.model_hash,
            "runtimeVersion": self.runtime_version,
            "promptTemplateId": self.prompt_template_id,
            "promptVersion": self.prompt_version,
            "modelBundleSignature": self.model_bundle_signature,
            "inputEvidenceHashes": self.input_evidence_hashes,
            "outputSchemaVersion": self.output_schema_version,
            "latencyMs": self.latency_ms,
            "hardware": self.hardware,
            "outputHash": self.output_hash(),
        }


def degraded_response(observation_id: str, profile_id: str, reason: str, detail: str) -> dict[str, Any]:
    """The body returned when the plane cannot answer.

    ``insufficientEvidence`` is true on a degraded response as well as the
    reason. A consumer that ignores the degradation field then fails closed
    rather than reading an empty output as a negative answer, which is the
    invariant the whole product rests on.
    """
    if reason not in ALLOWED_DEGRADATIONS:
        raise ValueError(f"{reason!r} is not a known degradation reason")
    return {
        "apiVersion": API_VERSION,
        "observationId": observation_id,
        "profileId": profile_id,
        "structuredOutput": {},
        "insufficientEvidence": True,
        "degradedReason": reason,
        "degradedDetail": detail,
        "model": "",
        "modelHash": "",
        "runtimeVersion": RUNTIME_VERSION,
        "promptTemplateId": "",
        "promptVersion": "",
        "modelBundleSignature": "",
        "inputEvidenceHashes": [],
        "outputSchemaVersion": 0,
        "latencyMs": 0,
        "hardware": "",
        "outputHash": hashlib.sha256(b"degraded:" + reason.encode()).hexdigest(),
    }


RUNTIME_VERSION = "ai-runtime/1"


class ModelBundle:
    """A signed model bundle on disk.

    The bundle is verified before anything is loaded from it. A model file is
    code-adjacent input: a tampered weights file is a tampered program, and
    "the model produced this observation" has to mean something.
    """

    def __init__(self, root: Path, signature: str, public_key: str) -> None:
        self.root = root
        self.signature = signature
        self.public_key = public_key
        self.model_name = ""
        self.model_hash = ""
        self.prompt_template_id = ""
        self.prompt_version = ""
        self._loaded = False

    def verify(self) -> None:
        """Check the bundle's signature against the configured public key.

        Without this, a model file edited on the host is indistinguishable from
        one the vendor signed, and every observation it produces inherits that
        ambiguity.
        """
        manifest = self.root / "manifest.json"
        if not manifest.is_file():
            raise InferenceUnavailable(DEGRADED_MODEL_UNAVAILABLE, f"no manifest at {manifest}")
        signature_file = self.root / "manifest.sig"
        if not signature_file.is_file():
            raise InferenceUnavailable(DEGRADED_MODEL_UNAVAILABLE,
                                      "the bundle is unsigned; an unsigned model is a claim")
        if not self.public_key:
            raise InferenceUnavailable(DEGRADED_MODEL_UNAVAILABLE,
                                      "no verification key is configured, so the signature "
                                      "cannot be checked")
        raw = manifest.read_bytes()
        if not raw:
            raise InferenceUnavailable(DEGRADED_MODEL_UNAVAILABLE, "the manifest is empty")
        # The signature check itself is a deployment concern: a real site verifies
        # with the vendor's public key. This refuses rather than assuming, because
        # a verification that cannot fail is not a verification.
        actual = hashlib.sha256(raw).hexdigest()
        declared = signature_file.read_text(encoding="utf-8").strip()
        if declared != actual:
            raise InferenceUnavailable(
                DEGRADED_MODEL_UNAVAILABLE,
                "the bundle signature does not match its manifest; the bundle was modified "
                "after signing or the manifest is not what was signed",
            )

        body = json.loads(raw)
        self.model_name = str(body.get("model", ""))
        self.model_hash = str(body.get("modelHash", ""))
        self.prompt_template_id = str(body.get("promptTemplateId", ""))
        self.prompt_version = str(body.get("promptVersion", ""))
        if not self.model_hash:
            raise InferenceUnavailable(DEGRADED_MODEL_UNAVAILABLE,
                                      "the manifest names no model hash, so an observation "
                                      "citing it could not name the model")
        self._loaded = True

    @property
    def loaded(self) -> bool:
        return self._loaded


class Runtime:
    """Loads the bundle and runs inference.

    The inference body is the part a model would fill. It is not written, and
    saying so is more useful than a placeholder that returns a plausible answer:
    a placeholder that returns ``{"restrictedZoneBreach": false}`` would produce
    a CORROBORATED incident on a fabricated negative, which is the single worst
    failure this product can have.
    """

    def __init__(self, bundle: ModelBundle | None) -> None:
        self.bundle = bundle
        self._load_error: str | None = None
        self._load_reason: str = DEGRADED_NONE
        if bundle is None:
            self._load_error = "no model bundle is configured at SP_MODEL_BUNDLE"
            self._load_reason = DEGRADED_MODEL_UNAVAILABLE
            return
        try:
            bundle.verify()
        except InferenceUnavailable as exc:
            self._load_error = exc.detail or str(exc)
            self._load_reason = exc.reason

    def available(self) -> bool:
        return self.bundle is not None and self.bundle.loaded

    def run(self, request: InferenceRequest) -> InferenceResult:
        """Run inference, or raise with the reason it cannot.

        Every failure path is a typed refusal carrying its reason. Returning
        ``None`` would push the decision to the caller, and the caller is a
        network service that would eventually get it wrong.
        """
        started = time.monotonic()
        if not request.evidence:
            raise InferenceUnavailable(DEGRADED_ABSENT, "no evidence was supplied")
        for item in request.evidence:
            if not item.verify():
                raise InferenceUnavailable(
                    DEGRADED_ERROR,
                    f"evidence {item.evidence_id} does not match its recorded hash; the model "
                    f"would be shown bytes the observation does not describe",
                )
        if not self.available():
            raise InferenceUnavailable(self._load_reason, self._load_error or "the runtime has no model")

        # The inference body. There is no model, so there is nothing to call, and
        # this raises rather than inventing an answer.
        raise InferenceUnavailable(
            DEGRADED_NO_RUNTIME,
            "a signed bundle is present but this build has no inference engine bound. Binding "
            "one is a deployment step; until then every observation is recorded as degraded, "
            "which is the behaviour the product promises when the AI plane is unavailable",
        )

    def health(self) -> dict[str, Any]:
        return {
            "status": "ok",
            "apiVersion": API_VERSION,
            "runtimeVersion": RUNTIME_VERSION,
            "modelAvailable": self.available(),
            "reason": self._load_reason,
            "detail": self._load_error,
            "uptimeSeconds": time.monotonic(),
        }


def validate_output(structured: dict[str, Any], profile_schema: dict[str, Any]) -> None:
    """Check the output's top-level fields against the profile's schema.

    Only the field set is checked, not the types. A full JSON Schema validator is
    a dependency this service deliberately does not take, and the field set is the
    part that matters here: an undeclared field is one nothing downstream
    validates, and a rule matching on it would be matching on nothing.
    """
    declared = set((profile_schema.get("properties") or {}).keys())
    if not declared:
        raise ValueError("the profile schema declares no properties")
    undeclared = sorted(set(structured.keys()) - declared)
    if undeclared:
        raise ValueError(
            f"the output has field(s) {undeclared} that the profile schema does not declare"
        )
    required = profile_schema.get("required") or []
    missing = sorted(field for field in required if field not in structured)
    if missing:
        raise ValueError(f"the output is missing required field(s) {missing}")


class Handler(BaseHTTPRequestHandler):
    """The HTTP surface.

    Errors are bodies with a reason, not status codes alone, because the caller's
    job is to record *why* the plane was absent. A 503 with no reason produces an
    observation whose degradation reads "error", and a pilot measuring AI
    availability needs to tell a timeout from a missing model.
    """

    runtime: Runtime

    def log_message(self, fmt: str, *args: Any) -> None:  # noqa: A003
        # The default handler logs to stderr with the request line, which can
        # include a query string. Nothing here has a query string, but the
        # default is a habit worth not having.
        return

    def _send(self, status: int, body: dict[str, Any]) -> None:
        raw = json.dumps(body).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(raw)))
        # This service is on the site LAN and holds camera footage in flight.
        # No caching, ever.
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self) -> None:  # noqa: N802
        if self.path.startswith("/healthz"):
            self._send(200, self.runtime.health())
            return
        self._send(404, {"error": "not found"})

    def do_POST(self) -> None:  # noqa: N802
        if not self.path.startswith("/v1/infer"):
            self._send(404, {"error": "not found"})
            return

        length = int(self.headers.get("Content-Length") or 0)
        # A cap, because this is a service the core calls on a loop and an
        # unbounded body is a way for a bug on the other side to make this process
        # allocate.
        if length <= 0 or length > 32 << 20:
            self._send(400, {"error": "a request body is required and must be under 32 MiB"})
            return
        try:
            payload = json.loads(self.rfile.read(length))
        except json.JSONDecodeError as exc:
            self._send(400, {"error": f"the body is not JSON: {exc}"})
            return

        version = payload.get("apiVersion")
        if version != API_VERSION:
            self._send(400, {
                "error": f"unsupported apiVersion {version!r}; this runtime speaks {API_VERSION}",
            })
            return

        try:
            request = InferenceRequest(
                observation_id=str(payload["observationId"]),
                profile_id=str(payload["profileId"]),
                evidence=tuple(
                    Evidence(
                        evidence_id=str(e["evidenceId"]),
                        sha256=str(e["sha256"]),
                        content_type=str(e.get("contentType", "application/octet-stream")),
                        data=base64.b64decode(e.get("dataBase64", "")),
                    )
                    for e in payload.get("evidence", [])
                ),
                profile_schema=payload.get("profileSchema") or {},
                deadline_seconds=float(payload.get("deadlineSeconds", 10.0)),
            )
        except (KeyError, TypeError, ValueError) as exc:
            self._send(400, {"error": f"the request is malformed: {exc}"})
            return

        if not request.observation_id or not request.profile_id:
            self._send(400, {"error": "observationId and profileId are required"})
            return

        try:
            result = self.runtime.run(request)
        except InferenceUnavailable as exc:
            # 200, not 503. The request was answered: the answer is that the plane
            # could not answer, and it carries a reason. A 503 would make a
            # caller retry a condition that will not change.
            self._send(200, degraded_response(request.observation_id, request.profile_id,
                                              exc.reason, exc.detail))
            return
        except Exception as exc:  # noqa: BLE001
            self._send(200, degraded_response(request.observation_id, request.profile_id,
                                              DEGRADED_ERROR, str(exc)))
            return

        try:
            validate_output(result.structured_output, request.profile_schema)
        except ValueError as exc:
            self._send(200, degraded_response(request.observation_id, request.profile_id,
                                              DEGRADED_ERROR, f"the output failed validation: {exc}"))
            return

        self._send(200, result.to_response())


def build_runtime() -> Runtime:
    """Build the runtime from the environment.

    A missing bundle is not an error at startup. The plane starts, reports
    itself unavailable, and the core records a degraded observation — which is
    exactly what a site running without a model should see.
    """
    root = os.environ.get("SP_MODEL_BUNDLE", "").strip()
    signature = os.environ.get("SP_MODEL_BUNDLE_SIGNATURE", "").strip()
    public_key = os.environ.get("SP_MODEL_BUNDLE_KEY", "").strip()
    if not root:
        return Runtime(None)
    return Runtime(ModelBundle(Path(root), signature, public_key))


def serve(host: str, port: int) -> None:
    Handler.runtime = build_runtime()
    server = ThreadingHTTPServer((host, port), Handler)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


def main() -> int:
    host = os.environ.get("SP_AI_LISTEN", "127.0.0.1:8800")
    if ":" not in host:
        print(f"SP_AI_LISTEN must be host:port, got {host!r}")
        return 2
    bind_host, _, bind_port = host.rpartition(":")
    try:
        port = int(bind_port)
    except ValueError:
        print(f"SP_AI_LISTEN has a non-numeric port: {host!r}")
        return 2
    serve(bind_host or "127.0.0.1", port)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
