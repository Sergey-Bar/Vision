#!/usr/bin/env python3
"""Measure the model BEFORE writing a decoder against it.

`pocctl model-probe` runs this. It loads the ONNX through `server.Engine` — the
sidecar's OWN loading path, not a second one — runs ONE REAL frame through it, and
prints what came out:

  * the input's name, declared shape and layout
  * the output's name and declared shape
  * the field count, measured from the tensor and not from the declaration
  * whether column 4 is an objectness, and how that was decided
  * the `names` metadata VERBATIM, exactly as the model carries it
  * the observed min and max of the box fields (0-3) and of the score fields
    (4 onwards), separately, which is what tells a pixel export from a logit one
  * the decoded boxes, in normalised frame coordinates
  * the telemetry the decoder itself reports

It then writes `models/<name>.contract.json`, and it REFUSES to write it if the
output rank or the field count is not understood. A contract file that records a
guess is worse than no contract file: the next build reads it as a measurement.

Why this is T-0 and not T-3. `docs/findings.md` §2 records the last time this was
got wrong: 286 green Go tests, a real camera at night, and zero detections,
because the detection layer had only ever been tested against stubs. A test
fixture written from an ASSUMED shape is a test of the assumption. This script
produces the real numbers, and `test_decode.py` builds its tensors from the file
this writes, so a change in the export changes the fixture rather than invalidating
it.

Usage:
    python probe_model.py --model models/yolov4n.onnx --clip testdata/youtube/sTF-6_xinUU.mp4
    python probe_model.py --model models/yolov4n.onnx --write
"""

from __future__ import annotations

import argparse
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import numpy as np  # noqa: E402

import server  # noqa: E402


def first_frame_rgb(path: str, width: int, height: int) -> np.ndarray:
    """One real frame from a video, decoded WITHOUT an image library.

    The sidecar must not use OpenCV or Pillow, and this script is the sidecar's
    own tooling, so it does not either. The frame is pulled with ffmpeg into a
    rawvideo stream on stdout, which is a pipe and not a codec dependency: the
    dependency list stays numpy + onnxruntime, and the check that this file uses
    no image library keeps meaning something.
    """
    import subprocess

    cmd = [
        "ffmpeg", "-hide_banner", "-loglevel", "error",
        "-i", path,
        "-frames:v", "1",
        "-vf", f"scale={width}:{height}",
        "-f", "rawvideo", "-pix_fmt", "rgb24", "-",
    ]
    proc = subprocess.run(cmd, capture_output=True, timeout=120)
    if proc.returncode != 0:
        raise RuntimeError(
            f"ffmpeg could not produce a frame from {path}: {proc.stderr.decode('utf-8', 'replace')[:400]}"
        )
    want = width * height * 3
    if len(proc.stdout) < want:
        raise RuntimeError(f"ffmpeg produced {len(proc.stdout)} bytes for a {width}x{height} frame, want {want}")
    return np.frombuffer(proc.stdout[:want], dtype=np.uint8).reshape(height, width, 3)


def blank_frame(width: int, height: int) -> np.ndarray:
    """Mid grey, for when there is no clip to read.

    It is NOT a substitute for a real frame and the contract file records which
    one was used. A probe over mid grey still measures the TENSOR — the shape, the
    field count, the objectness decision, the coordinate space — because those are
    properties of the model. What it does not measure is whether the model finds
    anything, and a reader who mistakes one for the other is exactly the mistake
    the telemetry is meant to prevent.
    """
    return np.full((height, width, 3), 128, dtype=np.uint8)


def measure(engine: server.Engine, rgb: np.ndarray) -> dict:
    """Run one frame and collect the measured facts about the model."""
    h, w, _ = rgb.shape
    blob, nw, nh, pad_x, pad_y = server.letterbox(rgb, engine.input_w, engine.input_h, engine.layout)
    outputs = engine.session.run(None, {engine.input_name: blob[None, ...]})
    raw = np.asarray(outputs[0])

    anchored = engine.spec.decoder_family == server.ExportSpec.FAMILY_ANCHORED
    flat, how = engine.rows(raw)
    if flat is None and not anchored:
        raise RuntimeError(
            f"the model's output {raw.shape} is not something this decoder can read, so no "
            f"contract is written. A contract recording a guess is worse than no contract: "
            f"the next build would read it as a measurement."
        )
    fields = int(flat.shape[1]) if flat is not None else int(raw.shape[-1])
    n_names = len(engine.classes)
    has_objectness = engine.spec.decide_objectness(fields)

    # The two groups, measured SEPARATELY. This is the measurement that decides
    # whether the box channels are pixels and the score channels are
    # probabilities, and the previous build's whole failure was applying one rule
    # to both groups at once.
    box_fields = flat[:, 0:4]
    score_fields = flat[:, 4:]
    observed = {
        "box_fields": {
            "min": round(float(np.min(box_fields)), 6),
            "max": round(float(np.max(box_fields)), 6),
        },
        "score_fields": {
            "min": round(float(np.min(score_fields)), 6),
            "max": round(float(np.max(score_fields)), 6),
        },
        "row_min": round(float(np.min(flat)), 6),
        "row_max": round(float(np.max(flat)), 6),
    }

    # Decode it, and record the telemetry the decoder produced for itself.
    if anchored:
        boxes, telemetry = engine.decode_anchored(outputs, w, h, nw, nh, pad_x, pad_y)
    else:
        boxes, telemetry = engine.decode(raw, w, h, nw, nh, pad_x, pad_y)

    out_metas = engine.session.get_outputs()
    contract = {
        "model": engine.model_name,
        "measured_by": "pyworker/probe_model.py, through server.Engine's own load path",
        "input": {
            "name": engine.input_name,
            "declared_shape": list(engine.session.get_inputs()[0].shape),
            "layout": engine.layout,
            "input_w": engine.input_w,
            "input_h": engine.input_h,
        },
        "output": {
            "name": out_metas[0].name,
            "count": len(out_metas),
            "names": [m.name for m in out_metas],
            "declared_shapes": [list(m.shape) for m in out_metas],
            "observed_shapes": [list(np.asarray(o).shape) for o in outputs],
            "rows": how,
            "fields": fields,
        },
        "decoder_family": engine.spec.decoder_family,
        "decoder_family_note": (
            "ANCHORED: this export publishes one output per stride and its box coordinates are "
            "offsets from per-cell anchor boxes. The anchors are in the model's .cfg and are NOT "
            "in the ONNX file, so this export cannot be decoded without them. See "
            "models/yolov4n.anchors.txt and the --anchors flag."
            if anchored else
            "FUSED: this export publishes one output with final coordinates, so no anchor step is "
            "applied and none is needed."
        ),
        "anchors": engine.anchors_path or None,
        "has_objectness": has_objectness,
        "has_objectness_decided_by": (
            f"{fields} fields against {n_names} class names from the model's own metadata"
            if n_names else
            f"{fields} fields against the COCO-80 vocabulary (the model carries no names)"
        ),
        "classes": n_names,
        # VERBATIM. The point of a contract is to record what the model says, not
        # what this build would have said, and a paraphrase is a translation with
        # no second speaker to check it against.
        "names_verbatim": engine.session.get_modelmeta().custom_metadata_map.get("names", ""),
        "providers": engine.providers,
        "observed": observed,
        "letterbox": {
            "frame_w": w, "frame_h": h,
            "content_w": nw, "content_h": nh,
            "pad_x": pad_x, "pad_y": pad_y,
        },
        "telemetry": telemetry,
        "decoded_boxes": boxes,
        "understood": has_objectness is not None,
    }
    return contract


def render(contract: dict) -> str:
    out = []
    w = out.append
    w("# model probe\n")
    w(f"- **model**: `{contract['model']}`")
    w(f"- **measured by**: {contract['measured_by']}")
    w(f"- **providers**: {', '.join(contract['providers'])}\n")
    w("## input\n")
    w(f"- name: `{contract['input']['name']}`")
    w(f"- declared shape: `{contract['input']['declared_shape']}`")
    w(f"- layout: `{contract['input']['layout']}`")
    w(f"- size: {contract['input']['input_w']}x{contract['input']['input_h']}\n")
    w("## output\n")
    w(f"- name: `{contract['output']['name']}`")
    w(f"- outputs published: {contract['output']['count']}")
    w(f"- declared shapes: `{contract['output']['declared_shapes']}`")
    w(f"- observed shapes: `{contract['output']['observed_shapes']}`")
    w(f"- fields per detection: **{contract['output']['fields']}** ({contract['output']['rows']})")
    w(f"- decoder family: **{contract['decoder_family']}** — {contract['decoder_family_note']}")
    if contract["anchors"]:
        w(f"- anchors file: `{contract['anchors']}`")
    w(f"- has objectness: **{contract['has_objectness']}** — {contract['has_objectness_decided_by']}")
    w(f"- class names: {contract['classes']}\n")
    w("## observed value ranges\n")
    w("The two groups are measured separately, because one rule applied to both at")
    w("once is what collapsed every box onto the frame's corner.\n")
    w("| group | min | max |\n|---|---|---|")
    w(f"| box fields 0-3 | {contract['observed']['box_fields']['min']} | {contract['observed']['box_fields']['max']} |")
    w(f"| score fields 4+ | {contract['observed']['score_fields']['min']} | {contract['observed']['score_fields']['max']} |")
    w("")
    w("## names metadata, verbatim\n")
    w("```")
    w(contract["names_verbatim"] or "(the model carries no names; the COCO-80 fallback applies)")
    w("```\n")
    w("## letterbox on the frame measured\n")
    lb = contract["letterbox"]
    w(f"- frame: {lb['frame_w']}x{lb['frame_h']}")
    w(f"- content in the canvas: {lb['content_w']}x{lb['content_h']}")
    w(f"- padding offset: ({lb['pad_x']}, {lb['pad_y']})\n")
    w("The un-padding divides by the CONTENT size, never by the input size. A 640x384")
    w("frame in a 640x640 canvas has a content height of 384, and dividing by 640")
    w("squashes every box to 0.6x its true height.\n")
    w("## decoded boxes, in normalised frame coordinates\n")
    if contract["decoded_boxes"]:
        w("| class | score | x1 | y1 | x2 | y2 |\n|---|---|---|---|---|---|")
        for b in contract["decoded_boxes"][:20]:
            w(f"| {b['cls']} | {b['score']} | {b['x1']} | {b['y1']} | {b['x2']} | {b['y2']} |")
    else:
        w("**none.** The model found nothing in this frame. That is a fact about the")
        w("model and the material, and it is not evidence about the decoder: the")
        w("telemetry above is.")
    w("")
    w("## telemetry\n")
    w("```json")
    w(json.dumps(contract["telemetry"], indent=2))
    w("```")
    return "\n".join(out)


def main() -> int:
    ap = argparse.ArgumentParser(description="Measure an ONNX export before decoding it.")
    ap.add_argument("--model", default="models/yolov4n.onnx")
    ap.add_argument("--anchors", default="", help="the darknet anchor file, for an anchored export")
    ap.add_argument("--clip", default="", help="a real clip to take one frame from; empty means mid grey")
    ap.add_argument("--width", type=int, default=640)
    ap.add_argument("--height", type=int, default=384)
    ap.add_argument("--out", default="", help="where to write the contract JSON; default models/<name>.contract.json")
    ap.add_argument("--write", action="store_true", help="write the contract file")
    ap.add_argument("--force", action="store_true", help="write even if the export is not fully understood")
    args = ap.parse_args()

    if not os.path.exists(args.model):
        sys.stderr.write(f"probe_model: no model at {args.model}. Run 'pocctl fetch-model' first.\n")
        return 2

    try:
        engine = server.Engine(args.model, anchors_path=args.anchors)
    except SystemExit as exc:
        # The Engine refuses an anchored export with no anchors, and the refusal is
        # the answer. The probe's job here is to print it, not to work around it.
        sys.stderr.write(f"{exc}\n")
        return 2
    if args.clip:
        rgb = first_frame_rgb(args.clip, args.width, args.height)
        source_note = f"one real frame from {args.clip}"
    else:
        rgb = blank_frame(args.width, args.height)
        source_note = f"mid grey, {args.width}x{args.height} — NO REAL FRAME, so this measures the tensor and not the material"

    try:
        contract = measure(engine, rgb)
    except Exception as exc:
        sys.stderr.write(f"probe_model: the model could not be measured: {exc}\n")
        return 2

    contract["frame_source"] = source_note
    print(render(contract))

    if not args.write:
        print("\n(probe only. Pass --write to record this as models/<name>.contract.json.)")
        return 0

    if not contract["understood"] and not args.force:
        sys.stderr.write(
            "probe_model: NOT writing the contract. The export's field count does not match "
            "either 4+classes or 5+classes, so whether column 4 is an objectness is not known. "
            "Writing it anyway would record a guess, and the next build would read a guess as a "
            "measurement. Use --force only if you have established the layout by hand.\n"
        )
        return 1

    out = args.out or os.path.join("models", os.path.splitext(os.path.basename(args.model))[0] + ".contract.json")
    os.makedirs(os.path.dirname(out) or ".", exist_ok=True)
    with open(out, "w", encoding="utf-8") as fh:
        json.dump(contract, fh, indent=2, sort_keys=True)
        fh.write("\n")
    print(f"\nwrote {out}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
