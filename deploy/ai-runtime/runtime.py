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
import binascii
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


# --- Ed25519 verification (RFC 8032), verify only -----------------------
#
# This is a signature verifier, not a digest comparison wearing the word
# "signature": a digest of the manifest next to the manifest proves only that
# the two files agree with each other, and anyone who can edit the bundle can
# regenerate that. The control here has to be one the vendor's private key can
# produce and nobody else's can, so it is real Ed25519 over the manifest bytes.
#
# Verify-only, and pure Python, on purpose. This service is air-gapped and
# stdlib-only by decision: a wheel in the release archive is a wheel to vet,
# sign and ship. Signing needs a private key and therefore does not belong in
# the plane at all — the vendor signs on their own side and we only check.
#
# The arithmetic follows RFC 8032 §5.1.4 (extended homogeneous coordinates,
# complete formulas) and §5.1.3 (point decoding). It is rounded against the
# RFC's own vectors in test_runtime.py, including the non-canonical encodings a
# malleable verifier would accept. If this ever needs changing, change it with
# those vectors still green; a verifier that rejects everything is a safe
# failure and one that accepts everything is not, so the reject tests matter
# more than the accept ones.
_ED_P = 2**255 - 19
_ED_L = 2**252 + 27742317777372353535851937790883648493
_ED_D = (-121665 * pow(121666, _ED_P - 2, _ED_P)) % _ED_P
_ED_SQRT_M1 = pow(2, (_ED_P - 1) // 4, _ED_P)


def _ed_recover_x(y: int, sign: int) -> int | None:
    """Recover the x-coordinate from y and the sign bit, or None if not a point.

    ``y >= p`` is rejected here and that rejection is load-bearing, not an
    edge case: a non-canonical y is a second encoding of a real point, and a
    verifier that accepts one accepts a signature nobody signed.
    """
    if y >= _ED_P:
        return None
    x2 = (y * y - 1) * pow(_ED_D * y * y + 1, _ED_P - 2, _ED_P) % _ED_P
    if x2 == 0:
        # x == 0, so the sign bit is the only thing distinguishing two encodings
        # of the same point, and a sign bit of 1 names a point that is not there.
        return None if sign else 0
    x = pow(x2, (_ED_P + 3) // 8, _ED_P)
    if (x * x - x2) % _ED_P != 0:
        x = x * _ED_SQRT_M1 % _ED_P
    if (x * x - x2) % _ED_P != 0:
        return None
    if (x & 1) != sign:
        x = _ED_P - x
    return x


def _ed_decode_point(encoded: bytes) -> tuple[int, int, int, int] | None:
    if len(encoded) != 32:
        return None
    y = int.from_bytes(encoded, "little")
    sign = y >> 255
    y &= (1 << 255) - 1
    x = _ed_recover_x(y, sign)
    if x is None:
        return None
    return (x, y, 1, x * y % _ED_P)


def _ed_add(p: tuple[int, int, int, int], q: tuple[int, int, int, int]) -> tuple[int, int, int, int]:
    x1, y1, z1, t1 = p
    x2, y2, z2, t2 = q
    a = (y1 - x1) * (y2 - x2) % _ED_P
    b = (y1 + x1) * (y2 + x2) % _ED_P
    c = 2 * t1 * t2 * _ED_D % _ED_P
    d = 2 * z1 * z2 % _ED_P
    e, f, g, h = b - a, d - c, d + c, b + a
    return (e * f % _ED_P, g * h % _ED_P, f * g % _ED_P, e * h % _ED_P)


def _ed_mul(scalar: int, point: tuple[int, int, int, int]) -> tuple[int, int, int, int]:
    result = (0, 1, 1, 0)
    while scalar > 0:
        if scalar & 1:
            result = _ed_add(result, point)
        point = _ed_add(point, point)
        scalar >>= 1
    return result


def _ed_equal(p: tuple[int, int, int, int], q: tuple[int, int, int, int]) -> bool:
    if (p[0] * q[2] - q[0] * p[2]) % _ED_P != 0:
        return False
    return (p[1] * q[2] - q[1] * p[2]) % _ED_P == 0


_ED_G = _ed_decode_point(bytes.fromhex(
    "5866666666666666666666666666666666666666666666666666666666666666"
))


def ed25519_verify(public_key: bytes, message: bytes, signature: bytes) -> bool:
    """True when ``signature`` is a valid Ed25519 signature over ``message``.

    Never raises: every malformed input is a False, because this runs on
    whatever bytes arrived in an untrusted bundle and a verifier that throws
    on bad input is a verifier whose failure mode depends on its caller.
    """
    if len(public_key) != 32 or len(signature) != 64:
        return False
    a = _ed_decode_point(public_key)
    if a is None:
        return False
    r_enc = signature[:32]
    r = _ed_decode_point(r_enc)
    if r is None:
        return False
    s = int.from_bytes(signature[32:], "little")
    if s >= _ED_L:
        # A scalar at or above the group order is a non-canonical encoding, and
        # accepting it is what makes a signature malleable: two byte strings, one
        # key, two "valid" bundles. RFC 8032 §5.1.7 step 1 rejects it.
        return False
    k = int.from_bytes(hashlib.sha512(r_enc + public_key + message).digest(), "little") % _ED_L
    # RFC 8032 §5.1.7 step 3. Checking [S]B == R + [k]A without the cofactor
    # multiplication is the form the RFC's own reference verifier uses.
    return _ed_equal(_ed_mul(s, _ED_G), _ed_add(r, _ed_mul(k, a)))


def _decode_key_material(text: str, what: str, expected: int) -> bytes:
    """Decode hex or base64 key material to exactly ``expected`` bytes."""
    cleaned = "".join(text.split())
    raw: bytes | None = None
    if len(cleaned) == expected * 2:
        try:
            raw = bytes.fromhex(cleaned)
        except ValueError:
            raw = None
    if raw is None:
        try:
            raw = base64.b64decode(cleaned, validate=True)
        except (ValueError, base64.binascii.Error) as exc:
            raise ValueError(f"{what} is neither hex nor base64: {exc}") from exc
    if len(raw) != expected:
        raise ValueError(f"{what} is {len(raw)} bytes, expected {expected}")
    return raw


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
        """Check the bundle's Ed25519 signature over the manifest bytes.

        The key comes from the deployment, the signature from the bundle, and
        the two meet here. Nothing else about the bundle is trusted, and
        nothing is loaded from it until this returns: a model file edited on
        the host is a tampered program, and every observation that model
        produces inherits that ambiguity.

        Every way this can fail is a refusal. A missing key, a key that is not
        a point on the curve, an unreadable signature, and a signature that does
        not check are the same answer to the operator — the bundle cannot be
        trusted — because a check that can be skipped is not a check.
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

        try:
            public_key = _decode_key_material(self.public_key, "the configured public key", 32)
        except ValueError as exc:
            raise InferenceUnavailable(
                DEGRADED_MODEL_UNAVAILABLE,
                f"the configured public key is not a usable Ed25519 key ({exc}); refusing rather "
                "than skipping the check",
            ) from exc
        if _ed_decode_point(public_key) is None:
            raise InferenceUnavailable(
                DEGRADED_MODEL_UNAVAILABLE,
                "the configured public key is not a point on the edwards25519 curve; it cannot "
                "verify anything, so the bundle is refused rather than assumed good",
            )

        try:
            declared = _decode_key_material(
                signature_file.read_text(encoding="utf-8").strip(),
                "the bundle signature",
                64,
            )
        except (ValueError, UnicodeDecodeError) as exc:
            raise InferenceUnavailable(
                DEGRADED_MODEL_UNAVAILABLE,
                f"the bundle signature is not a readable 64-byte Ed25519 signature ({exc})",
            ) from exc

        if not ed25519_verify(public_key, raw, declared):
            raise InferenceUnavailable(
                DEGRADED_MODEL_UNAVAILABLE,
                "the Ed25519 signature does not verify against the manifest under the configured "
                "public key; the bundle was modified after signing, or it was signed by a key "
                "this site does not trust",
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
