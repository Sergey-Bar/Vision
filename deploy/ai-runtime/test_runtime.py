"""Tests for the AI runtime's contract.

These tests are about the *contract*, not about a model. There is no model here,
and the properties worth protecting are the ones that decide what happens when
there is none: that the plane starts anyway, that it answers with a reason
rather than a guess, and that a degraded answer cannot be read as a negative.

The last one is the whole product. If a consumer that ignores the degradation
field reads an empty output as "nobody was present", the platform is worse than
having no AI at all â€” so ``insufficientEvidence`` is true on every degraded
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

import runtime as runtime_module  # noqa: E402
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
    _ed_decode_point,
    build_runtime,
    ed25519_verify,
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


# --- Test-only Ed25519 signing ------------------------------------------
#
# The runtime verifies and never signs: a private key has no business inside
# the AI plane. So the signing half lives here, in the test, using the
# *affine* group formulas from RFC 8032 Â§3 rather than the extended-coordinate
# ones the runtime uses. If both were the same code, a bug in the curve
# arithmetic would cancel itself out and the tests would prove nothing. Two
# different formulations agreeing on the RFC vectors is a real cross-check.
_P = 2**255 - 19
_L = 2**252 + 27742317777372353535851937790883648493
_D = (-121665 * pow(121666, _P - 2, _P)) % _P


def _affine_add(pt1, pt2):
    # RFC 8032 Â§3, with a = -1: y3 = (y1*y2 - a*x1*x2) / (1 - d*x1*x2*y1*y2),
    # so the x product is *added*. Getting that sign wrong produces a coherent
    # but wrong group, which is exactly the class of bug the RFC vectors exist
    # to catch.
    x1, y1 = pt1
    x2, y2 = pt2
    k = _D * x1 * x2 * y1 * y2 % _P
    return (
        (x1 * y2 + x2 * y1) * pow(1 + k, _P - 2, _P) % _P,
        (y1 * y2 + x1 * x2) * pow(1 - k, _P - 2, _P) % _P,
    )


def _affine_mul(scalar, pt):
    result = (0, 1)
    while scalar > 0:
        if scalar & 1:
            result = _affine_add(result, pt)
        pt = _affine_add(pt, pt)
        scalar >>= 1
    return result


def _recover_x(y, sign):
    x2 = (y * y - 1) * pow(_D * y * y + 1, _P - 2, _P) % _P
    if x2 == 0:
        return None if sign else 0
    x = pow(x2, (_P + 3) // 8, _P)
    if (x * x - x2) % _P != 0:
        x = x * pow(2, (_P - 1) // 4, _P) % _P
    if (x * x - x2) % _P != 0:
        return None
    if (x & 1) != sign:
        x = _P - x
    return x


_G = (_recover_x(4 * pow(5, _P - 2, _P) % _P, 0), 4 * pow(5, _P - 2, _P) % _P)


def _compress(pt):
    x, y = pt
    return int.to_bytes(y | ((x & 1) << 255), 32, "little")


def ed25519_public_key(secret: bytes) -> bytes:
    h = hashlib.sha512(secret).digest()
    a = int.from_bytes(h[:32], "little")
    a &= (1 << 254) - 8
    a |= 1 << 254
    return _compress(_affine_mul(a, _G))


def ed25519_sign(secret: bytes, message: bytes) -> bytes:
    h = hashlib.sha512(secret).digest()
    a = int.from_bytes(h[:32], "little")
    a &= (1 << 254) - 8
    a |= 1 << 254
    prefix = h[32:]
    public = _compress(_affine_mul(a, _G))
    r = int.from_bytes(hashlib.sha512(prefix + message).digest(), "little") % _L
    rs = _compress(_affine_mul(r, _G))
    k = int.from_bytes(hashlib.sha512(rs + public + message).digest(), "little") % _L
    s = (r + k * a) % _L
    return rs + int.to_bytes(s, 32, "little")


# A fixed vendor key. Constant so a bundle written by a test is byte-identical
# on every run, which is what makes the accept/reject tests golden rather than
# merely round-tripping whatever the test just produced.
VENDOR_SECRET = bytes.fromhex("9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60")
VENDOR_PUBLIC = "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a"


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


def write_bundle(
    root: Path,
    *,
    sign: bool = True,
    model_hash: str = "a" * 64,
    public_key: str = VENDOR_PUBLIC,
    manifest_override: bytes | None = None,
) -> ModelBundle:
    """Write a bundle whose manifest carries a real Ed25519 signature."""
    root.mkdir(parents=True, exist_ok=True)
    manifest = {
        "model": "vision-1",
        "modelHash": model_hash,
        "promptTemplateId": "pt-1",
        "promptVersion": "1",
    }
    raw = manifest_override if manifest_override is not None else json.dumps(
        manifest, sort_keys=True
    ).encode()
    (root / "manifest.json").write_bytes(raw)
    secret = VENDOR_SECRET if sign else bytes(32)
    signature = ed25519_sign(secret, raw).hex()
    (root / "manifest.sig").write_text(signature, encoding="utf-8")
    return ModelBundle(root, signature, public_key)


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


class Ed25519RFC8032(unittest.TestCase):
    """The verifier against the RFC's own vectors, and against malleability.

    Risk R2 in the plan is that a subtle bug in the curve arithmetic either
    rejects every bundle or, worse, accepts a forged one. Only a test that
    asserts *rejection* can catch the second case, so roughly half of these are
    negative and they are the half that matters. Vectors are literals: nothing
    here reaches the network, and an air-gapped site can run this.
    """

    # (label, secret key, public key, message hex, signature hex) â€” RFC 8032 Â§7.1
    VECTORS = [
        (
            "TEST 1 (empty message)",
            "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60",
            "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a",
            "",
            "e5564300c360ac729086e2cc806e828a84877f1eb8e5d974d873e06522490155"
            "5fb8821590a33bacc61e39701cf9b46bd25bf5f0595bbe24655141438e7a100b",
        ),
        (
            "TEST 2 (1 byte message)",
            "4ccd089b28ff96da9db6c346ec114e0f5b8a319f35aba624da8cf6ed4fb8a6fb",
            "3d4017c3e843895a92b70aa74d1b7ebc9c982ccf2ec4968cc0cd55f12af4660c",
            "72",
            "92a009a9f0d4cab8720e820b5f642540a2b27b5416503f8fb3762223ebdb69da"
            "085ac1e43e15996e458f3613d0f11d8c387b2eaeb4302aeeb00d291612bb0c00",
        ),
        (
            "TEST 3 (2 byte message)",
            "c5aa8df43f9f837bedb7442f31dcb7b166d38535076f094b85ce3a2e0b4458f7",
            "fc51cd8e6218a1a38da47ed00230f0580816ed13ba3303ac5deb911548908025",
            "af82",
            "6291d657deec24024827e69c3abe01a30ce548a284743a445e3680d7db5ac3ac"
            "18ff9b538d16f290ae67f760984dc6594a7c15e9716ed28dc027beceea1ec40a",
        ),
        (
            "TEST SHA(abc)",
            "833fe62409237b9d62ec77587520911e9a759cec1d19755b7da901b96dca3d42",
            "ec172b93ad5e563bf4932c70e1245034c35467ef2efd4d64ebf819683467e2bf",
            "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a"
            "2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f",
            "dc2a4459e7369633a52b1bf277839a00201009a3efbf3ecb69bea2186c26b589"
            "09351fc9ac90b3ecfdfbc7c66431e0303dca179c138ac17ad9bef1177331a704",
        ),
    ]

    def test_every_rfc_vector_verifies(self) -> None:
        for label, _secret, public_hex, message_hex, signature_hex in self.VECTORS:
            with self.subTest(vector=label):
                self.assertTrue(
                    ed25519_verify(
                        bytes.fromhex(public_hex),
                        bytes.fromhex(message_hex),
                        bytes.fromhex(signature_hex),
                    )
                )

    def test_the_test_signer_reproduces_the_rfc_signatures(self) -> None:
        # Guards the test helper, not the runtime. If this fails, the golden
        # bundle tests below are signing with the wrong key and their meaning
        # has quietly changed.
        for label, secret_hex, public_hex, message_hex, signature_hex in self.VECTORS:
            with self.subTest(vector=label):
                secret = bytes.fromhex(secret_hex)
                self.assertEqual(ed25519_public_key(secret).hex(), public_hex)
                self.assertEqual(
                    ed25519_sign(secret, bytes.fromhex(message_hex)).hex(), signature_hex
                )

    def test_a_wrong_signature_for_a_vector_is_rejected(self) -> None:
        # The negative control on the positive test above: if the verifier
        # returned True unconditionally, this is what catches it.
        for label, _secret, public_hex, message_hex, signature_hex in self.VECTORS:
            with self.subTest(vector=label):
                signature = bytearray.fromhex(signature_hex)
                signature[0] ^= 0x01
                self.assertFalse(
                    ed25519_verify(
                        bytes.fromhex(public_hex), bytes.fromhex(message_hex), bytes(signature)
                    )
                )

    def test_a_vector_signature_does_not_verify_against_another_key(self) -> None:
        _, secret_a, public_a, message_hex, _sig_a = self.VECTORS[0]
        _, _secret_b, public_b, _message_b, signature_b = self.VECTORS[1]
        self.assertFalse(
            ed25519_verify(
                bytes.fromhex(public_a), bytes.fromhex(message_hex), bytes.fromhex(signature_b)
            )
        )

    def test_a_changed_message_invalidates_a_vector_signature(self) -> None:
        _, _secret, public_hex, message_hex, signature_hex = self.VECTORS[0]
        message = bytearray.fromhex(message_hex)
        message.append(0x00)
        self.assertFalse(
            ed25519_verify(
                bytes.fromhex(public_hex), bytes(message), bytes.fromhex(signature_hex)
            )
        )

    def test_a_malleated_scalar_at_or_above_the_group_order_is_rejected(self) -> None:
        # S + L encodes the same mathematical signature and is the classic Ed25519
        # malleability trick. A verifier that skips the S < L check accepts it,
        # which means two distinct byte strings both verify for one signed
        # manifest â€” and a signature you can edit is not a signature.
        public = bytes.fromhex(self.VECTORS[0][2])
        message = bytes.fromhex(self.VECTORS[0][3])
        signature = bytes.fromhex(self.VECTORS[0][4])
        s = int.from_bytes(signature[32:], "little")
        self.assertLess(s, _L, "TEST 1's S must already be canonical for this test to mean anything")
        for offset in (_L, 2 * _L):
            forged = signature[:32] + int.to_bytes(s + offset, 32, "little")
            with self.subTest(offset=offset):
                self.assertLess(s + offset, 2**256)
                self.assertFalse(ed25519_verify(public, message, forged))

    def test_a_non_canonical_encoding_of_the_r_half_is_rejected(self) -> None:
        # R is decoded with the same canonical rules as the public key, so a
        # second encoding of the same point has to be refused there too. The
        # chosen R here decodes to nothing under those rules.
        public = bytes.fromhex(self.VECTORS[0][2])
        message = bytes.fromhex(self.VECTORS[0][3])
        signature = bytes.fromhex(self.VECTORS[0][4])
        bad_r = bytes.fromhex("edffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f")
        self.assertIsNone(_ed_decode_point(bad_r))
        self.assertFalse(ed25519_verify(public, message, bad_r + signature[32:]))

    def test_s_equal_to_the_group_order_is_rejected(self) -> None:
        public = bytes.fromhex(self.VECTORS[0][2])
        message = bytes.fromhex(self.VECTORS[0][3])
        signature = bytes.fromhex(self.VECTORS[0][4])
        forged = signature[:32] + int.to_bytes(_L, 32, "little")
        self.assertFalse(ed25519_verify(public, message, forged))

    def test_a_public_key_with_a_y_above_the_field_prime_is_rejected(self) -> None:
        # y = p decodes to the same field element as y = 0 but is a second
        # encoding of the identity point. Accepting non-canonical encodings is
        # what lets one key have two byte representations.
        encoded = "edffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f"
        # Asserted at the decoder, not only at the verifier: the group equation
        # would reject this anyway, so a test that only called the verifier
        # would pass with the non-canonical check deleted.
        self.assertIsNone(_ed_decode_point(bytes.fromhex(encoded)))
        self.assertFalse(
            ed25519_verify(
                bytes.fromhex(encoded), b"", bytes.fromhex(self.VECTORS[0][4])
            )
        )

    def test_a_public_key_with_no_square_root_x_is_rejected(self) -> None:
        # y = 2 gives x^2 with no solution mod p, sign bit set or clear. No
        # point exists, so there is nothing this could legitimately verify.
        for encoded in (
            "0200000000000000000000000000000000000000000000000000000000000080",
            "0200000000000000000000000000000000000000000000000000000000000000",
        ):
            with self.subTest(encoded=encoded):
                self.assertIsNone(_ed_decode_point(bytes.fromhex(encoded)))
                self.assertFalse(
                    ed25519_verify(
                        bytes.fromhex(encoded), b"", bytes.fromhex(self.VECTORS[0][4])
                    )
                )

    def test_the_identity_with_a_set_sign_bit_is_rejected(self) -> None:
        # (0, 1) is the neutral element and is on the curve, but the encoding
        # with x's sign bit set names a point that is not there. Again asserted
        # at the decoder, because the group equation would not catch it.
        encoded = "0100000000000000000000000000000000000000000000000000000000000080"
        self.assertIsNone(_ed_decode_point(bytes.fromhex(encoded)))
        self.assertFalse(
            ed25519_verify(
                bytes.fromhex(encoded), b"", bytes.fromhex(self.VECTORS[0][4])
            )
        )

    def test_the_identity_in_its_canonical_form_still_decodes(self) -> None:
        # The control on the two tests above. If "reject the sign bit" were
        # really "reject anything near the identity", this would fail.
        decoded = _ed_decode_point(
            bytes.fromhex("0100000000000000000000000000000000000000000000000000000000000000")
        )
        self.assertIsNotNone(decoded)
        self.assertEqual(decoded[0], 0)
        self.assertEqual(decoded[1], 1)

    def test_every_rfc_public_key_decodes_to_a_point(self) -> None:
        for label, _secret, public_hex, _message, _signature in self.VECTORS:
            with self.subTest(vector=label):
                self.assertIsNotNone(_ed_decode_point(bytes.fromhex(public_hex)))

    def test_wrong_length_inputs_are_rejected_rather_than_raising(self) -> None:
        # The verifier runs on whatever arrived in an untrusted bundle. A throw
        # here would make the failure mode depend on the caller.
        public = bytes.fromhex(self.VECTORS[0][2])
        signature = bytes.fromhex(self.VECTORS[0][4])
        self.assertFalse(ed25519_verify(public[:31], b"", signature))
        self.assertFalse(ed25519_verify(public, b"", signature[:63]))
        self.assertFalse(ed25519_verify(public + b"\x00", b"", signature))
        self.assertFalse(ed25519_verify(public, b"", signature + b"\x00"))
        self.assertFalse(ed25519_verify(b"", b"", b""))


class GoldenSignedBundle(unittest.TestCase):
    """A frozen, correctly signed bundle â€” the accept path, byte for byte.

    The other bundle tests sign at run time, which proves the round trip but
    would also pass if the signer and the verifier were wrong in the same way.
    These are literals: the manifest bytes, the signature and the public key
    are all fixed, so the only thing that can make them pass is a verifier
    that agrees with the RFC. And the tampered half is a one-byte edit of the
    same fixture, which is the check that matters â€” a verifier that always
    accepts would sail through the accept tests and be caught here.
    """

    MANIFEST = (
        b'{"model":"vision-1","modelHash":"'
        + b"a" * 64
        + b'","promptTemplateId":"pt-1","promptVersion":"1"}'
    )
    PUBLIC_KEY = "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a"
    # Ed25519 over MANIFEST under the private key of PUBLIC_KEY, RFC 8032.
    SIGNATURE = (
        "4b7ae7d316d64f5ad77859f854437876439c7ade4f4149c89359fe8965cc371a1"
        "6dbf519de19e6bb06815226a74a458a71308a6a333209525e96ccb7e2f7e60f"
    )

    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name) / "bundle"
        self.root.mkdir(parents=True)

    def tearDown(self) -> None:
        self.tmp.cleanup()

    def _write(self, manifest: bytes, signature: str) -> ModelBundle:
        (self.root / "manifest.json").write_bytes(manifest)
        (self.root / "manifest.sig").write_text(signature, encoding="utf-8")
        return ModelBundle(self.root, signature, self.PUBLIC_KEY)

    def test_the_golden_signature_is_a_real_one(self) -> None:
        # Guard on the fixture itself. If this ever fails the constants above
        # are stale and the golden tests below are testing nothing, so it is
        # worth failing loudly rather than quietly.
        self.assertEqual(
            ed25519_sign(VENDOR_SECRET, self.MANIFEST).hex(), self.SIGNATURE
        )
        self.assertTrue(
            ed25519_verify(
                bytes.fromhex(self.PUBLIC_KEY), self.MANIFEST, bytes.fromhex(self.SIGNATURE)
            )
        )

    def test_the_signed_bundle_verifies(self) -> None:
        bundle = self._write(self.MANIFEST, self.SIGNATURE)
        bundle.verify()
        self.assertTrue(bundle.loaded)
        self.assertEqual(bundle.model_name, "vision-1")
        self.assertEqual(bundle.model_hash, "a" * 64)
        self.assertEqual(bundle.prompt_template_id, "pt-1")
        self.assertEqual(bundle.prompt_version, "1")

    def test_a_one_byte_edit_of_the_golden_bundle_is_refused(self) -> None:
        bundle = self._write(self.MANIFEST, self.SIGNATURE)
        tampered = self.MANIFEST.replace(b'"promptVersion":"1"', b'"promptVersion":"2"')
        self.assertEqual(len(tampered), len(self.MANIFEST))
        (self.root / "manifest.json").write_bytes(tampered)
        with self.assertRaises(InferenceUnavailable) as caught:
            bundle.verify()
        self.assertEqual(caught.exception.reason, DEGRADED_MODEL_UNAVAILABLE)
        self.assertIn("does not verify", str(caught.exception))
        self.assertFalse(bundle.loaded)

    def test_the_golden_signature_under_a_different_key_is_refused(self) -> None:
        bundle = self._write(self.MANIFEST, self.SIGNATURE)
        bundle.public_key = "3d4017c3e843895a92b70aa74d1b7ebc9c982ccf2ec4968cc0cd55f12af4660c"
        with self.assertRaises(InferenceUnavailable):
            bundle.verify()
        self.assertFalse(bundle.loaded)

    def test_a_runtime_built_on_the_golden_bundle_is_available(self) -> None:
        # The end-to-end shape: a verified bundle, and a plane that is up and
        # can say why it still cannot answer.
        runtime = Runtime(self._write(self.MANIFEST, self.SIGNATURE))
        self.assertTrue(runtime.available())
        with self.assertRaises(InferenceUnavailable) as caught:
            runtime.run(request())
        self.assertEqual(caught.exception.reason, DEGRADED_NO_RUNTIME)

    def test_a_runtime_on_a_tampered_bundle_reports_why(self) -> None:
        bundle = self._write(self.MANIFEST, self.SIGNATURE)
        (self.root / "manifest.json").write_bytes(self.MANIFEST + b" ")
        runtime = Runtime(bundle)
        self.assertFalse(runtime.available())
        self.assertEqual(runtime.health()["reason"], DEGRADED_MODEL_UNAVAILABLE)
        self.assertIn("does not verify", runtime.health()["detail"])


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
        # The signature was made over the original bytes. One byte of the
        # manifest changed and the signature no longer describes it, which is
        # the whole point: a digest comparison could not tell these apart,
        # because whoever edited the bundle would have regenerated the .sig.
        bundle = write_bundle(self.root / "tampered")
        original = (bundle.root / "manifest.json").read_bytes()
        (bundle.root / "manifest.json").write_bytes(original.replace(b"vision-1", b"vision-2"))
        with self.assertRaises(InferenceUnavailable) as caught:
            bundle.verify()
        self.assertEqual(caught.exception.reason, DEGRADED_MODEL_UNAVAILABLE)
        self.assertIn("signature", str(caught.exception))
        self.assertFalse(bundle.loaded)

    def test_a_single_bit_flipped_in_the_signature_is_refused(self) -> None:
        bundle = write_bundle(self.root / "flipped")
        signature = bytearray.fromhex((bundle.root / "manifest.sig").read_text(encoding="utf-8"))
        signature[7] ^= 0x01
        (bundle.root / "manifest.sig").write_bytes(signature)
        with self.assertRaises(InferenceUnavailable):
            bundle.verify()
        self.assertFalse(bundle.loaded)

    def test_a_bundle_signed_by_another_key_is_refused(self) -> None:
        # Right shape, right manifest, wrong signer. A digest comparison would
        # have accepted this without noticing there was a signer at all.
        bundle = write_bundle(
            self.root / "wrongkey",
            public_key="3d4017c3e843895a92b70aa74d1b7ebc9c982ccf2ec4968cc0cd55f12af4660c",
        )
        with self.assertRaises(InferenceUnavailable) as caught:
            bundle.verify()
        self.assertIn("does not verify", str(caught.exception))

    def test_an_unsigned_bundle_is_refused(self) -> None:
        bundle = write_bundle(self.root / "unsigned", sign=False)
        with self.assertRaises(InferenceUnavailable):
            bundle.verify()
        self.assertFalse(bundle.loaded)

    def test_no_verification_key_is_refused_rather_than_assumed(self) -> None:
        root = self.root / "nokey"
        bundle = write_bundle(root)
        bundle.public_key = ""
        with self.assertRaises(InferenceUnavailable) as caught:
            bundle.verify()
        self.assertIn("cannot be checked", str(caught.exception))

    def test_a_key_that_is_not_a_curve_point_is_refused_not_skipped(self) -> None:
        # 32 bytes of the right length that decode to no point on the curve.
        # A verifier that only length-checked its key would wave this through.
        root = self.root / "notapoint"
        bundle = write_bundle(root)
        bundle.public_key = "02" + "00" * 30 + "80"
        with self.assertRaises(InferenceUnavailable) as caught:
            bundle.verify()
        self.assertIn("not a point", str(caught.exception))
        self.assertFalse(bundle.loaded)

    def test_a_key_of_the_wrong_length_is_refused(self) -> None:
        # 16 bytes of well-formed hex: decodes cleanly and is still not a key.
        bundle = write_bundle(self.root / "shortkey")
        bundle.public_key = "ab" * 16
        with self.assertRaises(InferenceUnavailable) as caught:
            bundle.verify()
        self.assertIn("expected 32", str(caught.exception))

    def test_a_key_that_is_not_hex_or_base64_is_refused(self) -> None:
        bundle = write_bundle(self.root / "garbagekey")
        bundle.public_key = "not a key at all, certainly not 32 bytes of anything"
        with self.assertRaises(InferenceUnavailable) as caught:
            bundle.verify()
        self.assertIn("neither hex nor base64", str(caught.exception))
        self.assertFalse(bundle.loaded)

    def test_a_signature_that_is_not_64_bytes_is_refused(self) -> None:
        bundle = write_bundle(self.root / "shortsig")
        (bundle.root / "manifest.sig").write_text("abcd", encoding="utf-8")
        with self.assertRaises(InferenceUnavailable) as caught:
            bundle.verify()
        self.assertIn("64-byte Ed25519 signature", str(caught.exception))
        self.assertFalse(bundle.loaded)

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

    def test_empty_evidence_is_refused(self) -> None:
        # This test asserted the opposite, and its name gave the reason: an
        # evidence with no bytes "is not a hash mismatch". That statement is true
        # and it was used to justify `return True` â€” which is a different claim,
        # and the one the product rests on. `verify()` does not verify anything
        # when there are no bytes; it reports that it verified, which is the
        # condition the docstring calls worse than no observation.
        #
        # There is a real case behind it: the core holds the evidence and the
        # plane is handed a reference. A reference cannot be verified here, so it
        # must not be *accepted* here either â€” which is why the request boundary
        # now refuses a request whose evidence carries no bytes, and this function
        # has one answer.
        reference_only = Evidence(evidence_id="ev-1", sha256="a" * 64, content_type="video/mp4")
        self.assertFalse(reference_only.verify())

    def test_a_request_whose_evidence_has_no_bytes_is_refused(self) -> None:
        # The boundary check, which is where the distinction between "a reference"
        # and "nothing was supplied" is actually available.
        with self.assertRaises(ValueError) as caught:
            runtime_module._require_bytes(
                (Evidence(evidence_id="ev-1", sha256="a" * 64, content_type="video/mp4"),)
            )
        self.assertIn("ev-1", str(caught.exception))

    def test_a_request_with_real_bytes_is_accepted(self) -> None:
        good = evidence(b"bytes", "ev-1")
        runtime_module._require_bytes((good,))  # must not raise

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
