#!/usr/bin/env python3
"""Decode ONE tensor, so a Go test can compare the two decoders on the same bytes.

`scripts/verify.ps1` has run `go test -run TestCrossDecoderParity -tags parity
./internal/ortdetect/` as its "decoder parity" step for weeks. The test it named
did not exist. `go test -run` with a pattern that matches nothing prints "no
tests to run" and exits 0, so the step printed:

    the Go decoder and pyworker agree on the same tensor

every single time, over a table nobody had ever compared. This file is the other
half of that step.

WHY THE BYTES ARE HANDED OVER RATHER THAN REBUILT
------------------------------------------------
A parity test that builds the tensor twice is a test of two fixtures, not of two
decoders: the moment one side's `put_fused` writes an objectness channel and the
other's `row()` does not, the comparison measures the fixtures. So the Go side
sends the exact `float32` values it decoded, and this side reshapes them and
decodes them. Nothing is rebuilt here — no record is constructed, no class is
chosen, no coordinate is computed. The only thing this file contributes is the
Python decoder.

The fixture is the Go package's own, which is the one that has been checked for
the properties a parity fixture needs: several live candidates, a cross-class
duplicate so the global pass has something to collapse, a box too thin to
survive, and one straddling the frame edge. If that fixture ever stops being a
parity fixture, `TestTheFixtureExercisesBothNMSPasses` on the Go side goes red
first, and this comparison is then over a fixture that proves nothing — which is
worth knowing, because it is the way the previous parity claim was broken.

Usage:
    py -3 parity_decode.py <fixture.json>

The fixture is `{"dims": [...], "data": [...]}`. The answer is written to stdout
as `{"boxes": [{"cls": int, "name": str, "score": f, "x1": f, ...}, ...],
"telemetry": {...}}`. `cls` is the INDEX as decoded and `name` is what the
sidecar would put on the wire, so the Go side can compare the geometry against
the numbers and the class against its own vocabulary — a vocabulary disagreement
is itself a parity defect, and it is one neither side would notice alone.
"""

from __future__ import annotations

import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import numpy as np  # noqa: E402

import test_decode  # noqa: E402  -- the sidecar's own decoder test helpers
import server  # noqa: E402


def decode_fixture(path: str) -> dict:
    """Decode the tensor in `path` with the sidecar's own decode path."""
    with open(path, encoding="utf-8") as handle:
        fixture = json.load(handle)

    dims = [int(d) for d in fixture["dims"]]
    data = [float(v) for v in fixture["data"]]
    if int(np.prod(dims)) != len(data):
        raise SystemExit(
            f"fixture {path}: dims {dims} imply {int(np.prod(dims))} values and the file "
            f"carries {len(data)}; the two decoders would not be handed the same tensor"
        )
    # float64 of a float32's decimal form is the float32's exact value, so this
    # IS the tensor Go decoded rather than a rounding of it.
    arr = np.asarray(data, dtype=np.float64).reshape(dims)

    engine = test_decode.fused_engine()
    boxes, telemetry = engine.decode(arr, 640, 640, 640, 640)

    # Invert the sidecar's own name-to-index map so the index survives, because
    # the Go decoder reports classes by index and would otherwise be compared by
    # spelling.
    index_of = {name: i for i, name in engine.classes.items()}
    out = []
    for b in boxes:
        name = b.get("cls", "")
        out.append({
            "cls": index_of.get(name, -1),
            "name": name,
            "score": b["score"],
            "x1": b["x1"], "y1": b["y1"], "x2": b["x2"], "y2": b["y2"],
        })
    # The layout the flattener chose is the single most consequential decision in
    # the decode, and the Go side asserts on it. A sidecar that reported nothing
    # about it would leave the comparison asserting geometry only.
    flat, how = engine.rows(arr)
    telemetry = dict(telemetry)
    telemetry["layout"] = how
    telemetry["candidates"] = int(flat.shape[0]) if flat is not None else 0
    telemetry["fields"] = int(flat.shape[1]) if flat is not None else 0
    return {"boxes": out, "telemetry": telemetry}


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: parity_decode.py <fixture.json>")
    json.dump(decode_fixture(sys.argv[1]), sys.stdout)
    sys.stdout.write("\n")
