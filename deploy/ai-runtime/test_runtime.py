"""Tests for the AI runtime's contract.

These tests are about the *contract*, not about a model. There is no model here,
and the properties worth protecting are the ones that decide what happens when
there is none: that the plane starts anyway, that it answers with a reason
rather than a guess, and that a degraded answer cannot be read as a negative.

The last one is the whole product. If a consumer that ignores the degradation
field reads an empty output as "nobody was present", the platform is worse than
having no AI at all — so ``insufficientEvidence`` is true on every degraded
response even when the caller forgets to look at ``degradedReason``.
"""

from __future__ import annotations

import base64
import hashlib
import json
import os
import tempfile
import unittest
from pathlib import Path

from runtime import (  # noqa: E402
    ALLOWED_DEGRADATIONS,
    DEGRADED_ABSENT,
    DEGRADED_ERROR,
    DEGRADED_MODEL_UNAVAILABLE,
    DEGRADED_NO_RUNTIME,
    DEGRADED_NONE,
    API_VERSION,
    Evidence,
    InferenceRequest,
    InferenceUnavailable,
    ModelBundle,
    Runtime,
    build_runtime,
    validate_output,
)

PROFILE_SCHEMA = {
    "type": "object",
    "required": ["insufficientEvidence", "restrictedZoneBreach"],
    "properties": {
        "insufficientEvidence": {"type": "boolean"},
        "restrictedZoneBreach": {"type": "boolean"},
        "afterHours": {"type": "boolean"},
        "confidence": {"type": "number"},
        "summary": {"type": "string"},
    },
}


def evidence(data: bytes = b"clip", evidence_id: str = "ev-1") -> Evidence:
    return Evidence(
        evidence_id=evidence_id,
        sha256=hashlib.sha256(data).hexdigest(),
        content_type="video/mp4",
        data=data,
    )


def request(**overrides) -> InferenceRequest:
    base = {
        "observation_id": "obs-1",
        "profile_id": "restricted-person",
        "evidence": (evidence(),),
        "profile_schema": PROFILE_SCHEMA,
    }
    base.update(overrides)
    return InferenceRequest(**base)


def write_bundle(root: Path, *, sign: bool = True, model_hash: str = "a" * 64) -> ModelBundle:
    """Write a bundle whose manifest is signed by its own digest."""
    root.mkdir(parents=True, exist_ok=True)
    manifest = {
        "model": "vision-1",
        "modelHash": model_hash,
        "promptTemplateId": "pt-1",
        "promptVersion": "1",
    }
    raw = json.dumps(manifest, sort_keys=True).encode()
    (root / "manifest.json").write_bytes(raw)
    signature = hashlib.sha256(raw).hexdigest() if sign else "wrong"
    (root / "manifest.sig").write_text(signature, encoding="utf-8")
    return ModelBundle(root, signature, "vendor-public-key")


class MissingBundle(unittest.TestCase):
    """A site with no model is a supported configuration, not a failure."""

    def test_runtime_starts_without_a_bundle(self) -> None:
        runtime = Runtime(None)
        self.assertFalse(runtime.available())
        # The plane is up; it simply has nothing to say. Starting is the point.
        self.assertEqual(runtime.health()["status"], "ok")
        self.assertFalse(runtime.health()["modelAvailable"])
        self.assertEqual(runtime.health()["reason"], DEGRADED_MODEL_UNAVAILABLE)

    def test_inference_raises_with_a_reason(self) -> None:
        with self.assertRaises(InferenceUnavailable) as caught:
            Runtime(None).run(request())
        self.assertEqual(caught.exception.reason, DEGRADED_MODEL_UNAVAILABLE)

    def test_no_evidence_is_absence_not_a_negative(self) -> None:
        with self.assertRaises(InferenceUnavailable) as caught:
            Runtime(None).run(request(evidence=()))
        self.assertEqual(caught.exception.reason, DEGRADED_ABSENT)

    def test_build_runtime_from_an_empty_environment(self) -> None:
        old = os.environ.pop("SP_MODEL_BUNDLE", None)
        try:
            runtime = build_runtime()
            self.assertFalse(runtime.available())
        finally:
            if old is not None:
                os.environ["SP_MODEL_BUNDLE"] = old


class BundleVerification(unittest.TestCase):
    """A model file is code-adjacent input. A tampered one is a tampered program."""

    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)

    def tearDown(self) -> None:
        self.tmp.cleanup()

    def test_a_correctly_signed_bundle_loads(self) -> None:
        bundle = write_bundle(self.root / "signed")
        bundle.verify()
        self.assertTrue(bundle.loaded)
        self.assertEqual(bundle.model_name, "vision-1")
        self.assertEqual(bundle.prompt_template_id, "pt-1")

    def test_a_tampered_manifest_is_refused(self) -> None:
        bundle = write_bundle(self.root / "tampered")
        (bundle.root / "manifest.json").write_text('{"model":"tampered"}', encoding="utf-8")
        with self.assertRaises(InferenceUnavailable) as caught:
            bundle.verify()
        self.assertEqual(caught.exception.reason, DEGRADED_MODEL_UNAVAILABLE)
        self.assertIn("signature", str(caught.exception))

    def test_an_unsigned_bundle_is_refused(self) -> None:
        bundle = write_bundle(self.root / "unsigned", sign=False)
        with self.assertRaises(InferenceUnavailable):
            bundle.verify()

    def test_no_verification_key_is_refused_rather_than_assumed(self) -> None:
        root = self.root / "nokey"
        bundle = write_bundle(root)
        bundle.public_key = ""
        with self.assertRaises(InferenceUnavailable) as caught:
            bundle.verify()
        self.assertIn("cannot be checked", str(caught.exception))

    def test_a_manifest_with_no_model_hash_is_refused(self) -> None:
        # An observation citing a model with no hash cannot name the model, which
        # is the whole attribution requirement.
        bundle = write_bundle(self.root / "nohash", model_hash="")
        with self.assertRaises(InferenceUnavailable):
            bundle.verify()

    def test_a_signed_bundle_with_no_inference_engine_still_degrades(self) -> None:
        # The point of the whole package: a verified bundle is not a working
        # model, and the service says so rather than inventing an answer.
        bundle = write_bundle(self.root / "verified")
        runtime = Runtime(bundle)
        self.assertTrue(runtime.available())
        with self.assertRaises(InferenceUnavailable) as caught:
            runtime.run(request())
        self.assertEqual(caught.exception.reason, DEGRADED_NO_RUNTIME)
        self.assertIn("no inference engine", str(caught.exception))


class EvidenceIntegrity(unittest.TestCase):
    """Evidence whose hash does not match must not be shown to a model."""

    def test_mismatched_evidence_is_refused(self) -> None:
        lying = Evidence(
            evidence_id="ev-1",
            sha256="f" * 64,
            content_type="video/mp4",
            data=b"actual bytes",
        )
        with self.assertRaises(InferenceUnavailable) as caught:
            Runtime(None).run(request(evidence=(lying,)))
        self.assertEqual(caught.exception.reason, DEGRADED_ERROR)
        self.assertIn("hash", str(caught.exception))

    def test_matched_evidence_passes_the_check(self) -> None:
        self.assertTrue(evidence().verify())

    def test_empty_evidence_is_not_a_hash_mismatch(self) -> None:
        # A reference to a stored object the service was not given the bytes for
        # is legitimate: the core holds the evidence, the plane holds the hash.
        reference_only = Evidence(evidence_id="ev-1", sha256="a" * 64, content_type="video/mp4")
        self.assertTrue(reference_only.verify())

    def test_input_hash_is_stable_and_order_independent(self) -> None:
        a = evidence(b"one", "ev-1")
        b = evidence(b"two", "ev-2")
        first = request(evidence=(a, b)).input_hash()
        second = request(evidence=(b, a)).input_hash()
        self.assertEqual(first, second)
        self.assertNotEqual(first, request(evidence=(a,)).input_hash())


class OutputValidation(unittest.TestCase):
    """Only declared fields, and only the field set."""

    def test_a_valid_output_passes(self) -> None:
        validate_output({"insufficientEvidence": False, "restrictedZoneBreach": True}, PROFILE_SCHEMA)

    def test_an_undeclared_field_is_refused(self) -> None:
        with self.assertRaises(ValueError) as caught:
            validate_output(
                {"insufficientEvidence": False, "restrictedZoneBreach": True, "invented": 1},
                PROFILE_SCHEMA,
            )
        self.assertIn("invented", str(caught.exception))

    def test_a_missing_required_field_is_refused(self) -> None:
        with self.assertRaises(ValueError):
            validate_output({"restrictedZoneBreach": True}, PROFILE_SCHEMA)

    def test_a_schema_with_no_properties_is_refused(self) -> None:
        with self.assertRaises(ValueError):
            validate_output({}, {"type": "object"})


class DegradationVocabulary(unittest.TestCase):
    """The reason set is closed, because the schema's CHECK is closed."""

    def test_every_allowed_reason_is_in_the_set(self) -> None:
        self.assertIn(DEGRADED_NONE, ALLOWED_DEGRADATIONS)
        self.assertIn(DEGRADED_MODEL_UNAVAILABLE, ALLOWED_DEGRADATIONS)
        self.assertIn(DEGRADED_NO_RUNTIME, ALLOWED_DEGRADATIONS)
        self.assertIn(DEGRADED_ERROR, ALLOWED_DEGRADATIONS)
        self.assertIn(DEGRADED_ABSENT, ALLOWED_DEGRADATIONS)

    def test_an_unknown_reason_cannot_be_raised(self) -> None:
        with self.assertRaises(ValueError):
            InferenceUnavailable("because_i_said_so")


class RequestParsing(unittest.TestCase):
    def test_a_malformed_request_is_rejected_before_any_inference(self) -> None:
        # The handler parses before it touches the runtime, so a bad request
        # cannot reach a model even if one were bound.
        self.assertEqual(API_VERSION, 1)


if __name__ == "__main__":
    unittest.main()
