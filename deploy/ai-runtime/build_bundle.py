"""Build a signed model bundle the AI runtime will load.

The plane verifies and never signs -- a private key has no business inside it.
Signing is the vendor's job, done here on their side, and this tool is where a
bundle and its ``manifest.sig`` come from. It stamps the engine's own content
hash into the manifest as ``modelHash`` and signs the manifest bytes with the
vendor's secret key, so the hash ``describe()`` reports and the manifest the
runtime verifies are the same identity bound by a signature only the vendor could
produce.

The signer is the *affine* group arithmetic from RFC 8032 §3/§5.1.6, deliberately
a different formulation than the runtime's extended-coordinate *verifier*: a
bundle this builds that the runtime then accepts is proof the two independently
implement the curve, and a bundle that fails to load is a broken build rather than
a silently accepted tamper.

The secret key is never stored in this repository. Pass it at build time
(--secret / SP_BUNDLE_SECRET, hex); a build that omits it does not fall back to a
test key and produce a "signed" bundle nobody can replace.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import sys
from pathlib import Path
from typing import Any

# --- Affine Ed25519 signing, RFC 8032 Â§5.1.6 Â§3 ---------------------------
#
# Sign-only, and the counterpart to runtime.py's verify-only extended-coordinate
# arithmetic. Both follow the RFC; they are different formulas on purpose, so a
# sign here that verifies there is a cross-implementation check.
_P = 2**255 - 19
_L = 2**252 + 27742317777372353535851937790883648493
_D = (-121665 * pow(121666, _P - 2, _P)) % _P
_I = pow(2, (_P - 1) // 4, _P)


def _x_recover(y: int) -> int | None:
    xx = (y * y - 1) * pow(_D * y * y + 1, _P - 2, _P) % _P
    x = pow(xx, (_P + 3) // 8, _P)
    if (x * x - xx) % _P != 0:
        x = x * _I % _P
    if (x * x - xx) % _P != 0:
        return None
    return _P - x if x & 1 else x


def _edwards_add(p: tuple[int, int], q: tuple[int, int]) -> tuple[int, int]:
    x1, y1 = p
    x2, y2 = q
    x3 = (x1 * y2 + x2 * y1) * pow(1 + _D * x1 * x2 * y1 * y2, _P - 2, _P)
    y3 = (y1 * y2 + x1 * x2) * pow(1 - _D * x1 * x2 * y1 * y2, _P - 2, _P)
    return x3 % _P, y3 % _P


def _scalarmult(p: tuple[int, int], e: int) -> tuple[int, int]:
    if e == 0:
        return 0, 1
    q = _scalarmult(p, e // 2)
    q = _edwards_add(q, q)
    if e & 1:
        q = _edwards_add(q, p)
    return q


_G_y = 4 * pow(5, _P - 2, _P) % _P
_G_x = _x_recover(_G_y)
_G = (_G_x, _G_y)


def _secret_expand(secret: bytes) -> tuple[int, bytes]:
    if len(secret) != 32:
        raise ValueError("the vendor secret key must be 32 bytes")
    h = hashlib.sha512(secret).digest()
    a = int.from_bytes(h[:32], "little")
    a &= (1 << 254) - 8
    a |= 1 << 254
    # The second half stays BYTES: RFC 8032 prepends it to the message before
    # hashing, not as an integer. Turning it into an int here was the defect that
    # made signing add an int to the message bytes.
    return a, h[32:]


def _point_decompress(data: bytes) -> tuple[int, int]:
    y = int.from_bytes(data, "little")
    x = _x_recover(y)
    if x is None:
        raise ValueError("not a point on the curve")
    if (x & 1) != ((data[31] >> 7) & 1):
        x = _P - x
    return x, y


def _public_from_secret(secret: bytes) -> bytes:
    a, _ = _secret_expand(secret)
    point = _scalarmult(_G, a)
    return (point[1] | ((point[0] & 1) << 255)).to_bytes(32, "little")


def sign(secret: bytes, message: bytes) -> bytes:
    """Ed25519 sign (RFC 8032 §5.1.6). Deterministic, as the RFC requires."""
    a, prefix = _secret_expand(secret)
    public = _public_from_secret(secret)
    r = int.from_bytes(hashlib.sha512(prefix + message).digest(), "little") % _L
    r_point = _scalarmult(_G, r)
    r_bytes = (r_point[1] | ((r_point[0] & 1) << 255)).to_bytes(32, "little")
    k = int.from_bytes(hashlib.sha512(r_bytes + public + message).digest(), "little") % _L
    s = (r + k * a) % _L
    return r_bytes + s.to_bytes(32, "little")


# --- The bundle builder ----------------------------------------------------


def build_bundle(engine_path: Path, out_dir: Path, secret: bytes, *,
                 model: str = "vision-investigate-facts/1",
                 prompt_template_id: str = "investigate/v1",
                 prompt_version: str = "1",
                 extra_files: dict[str, Path] | None = None) -> Path:
    """Produce a signed bundle directory and return its root.

    ``modelHash`` is the engine file's sha256, the same value ``describe()``
    reports, so the manifest and the engine agree on the one identity the
    observation cites. The manifest bytes are signed; the engine and any extra
    files are copied in alongside. Returns the bundle root, which a runtime
    pointed at (SP_MODEL_BUNDLE) verifies before it loads anything.
    """
    engine_path = Path(engine_path)
    engine_bytes = engine_path.read_bytes()
    model_hash = hashlib.sha256(engine_bytes).hexdigest()

    out_dir = Path(out_dir)
    if out_dir.exists():
        shutil.rmtree(out_dir)
    out_dir.mkdir(parents=True)

    manifest = {
        "model": model,
        "modelHash": model_hash,
        "promptTemplateId": prompt_template_id,
        "promptVersion": prompt_version,
    }
    manifest_bytes = json.dumps(manifest, sort_keys=True).encode()
    (out_dir / "manifest.json").write_bytes(manifest_bytes)
    (out_dir / "manifest.sig").write_text(sign(secret, manifest_bytes).hex(), encoding="utf-8")
    shutil.copyfile(engine_path, out_dir / engine_path.name)
    for rel, src in (extra_files or {}).items():
        shutil.copyfile(src, out_dir / rel)
    return out_dir


def _parse_secret(value: str) -> bytes:
    cleaned = value.strip()
    try:
        raw = bytes.fromhex(cleaned)
    except ValueError as exc:
        raise SystemExit(f"--secret must be a hex-encoded 32-byte key: {exc}") from exc
    if len(raw) != 32:
        raise SystemExit(f"--secret is {len(raw)} bytes; Ed25519 wants 32")
    return raw


def _public_hex(secret: bytes) -> str:
    return _public_from_secret(secret).hex()


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Build a signed AI model bundle.")
    parser.add_argument("engine", type=Path, help="Path to the engine .py to bundle.")
    parser.add_argument("-o", "--out", type=Path, required=True, help="Bundle output directory.")
    parser.add_argument("--secret", default=os.environ.get("SP_BUNDLE_SECRET", ""),
                        help="Hex vendor secret key (or SP_BUNDLE_SECRET). Never committed.")
    parser.add_argument("--print-public", action="store_true",
                        help="Print the matching public key (for SP_MODEL_BUNDLE_KEY) and exit.")
    args = parser.parse_args(argv)
    if not args.secret:
        print("build-bundle: no vendor secret. Pass --secret or set SP_BUNDLE_SECRET; "
              "a bundle is never signed with a fallback key.", file=sys.stderr)
        return 2
    secret = _parse_secret(args.secret)
    if args.print_public:
        print(_public_hex(secret))
        return 0
    root = build_bundle(args.engine, args.out, secret)
    print(f"bundle at {root}")
    print(f"modelHash {hashlib.sha256(Path(args.engine).read_bytes()).hexdigest()}")
    print("set SP_MODEL_BUNDLE to this dir, SP_MODEL_BUNDLE_KEY to the matching public key, "
          "and SP_ENGINE_PATH to the engine inside it.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
