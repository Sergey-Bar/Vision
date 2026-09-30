#!/usr/bin/env python3
"""The AI sidecar: raw RGB frames in, boxes out. Nothing else.

The whole contract is deliberately small:

* It reads no video. It never opens a file, a socket to a camera, or a clock.
  Go owns the clock; this process owns inference.
* It holds no state between requests beyond the loaded model. There is no
  tracker, no zone logic, no memory of what it saw before.
* It needs numpy and onnxruntime and nothing else. No OpenCV, no Pillow, no
  capture library. If this file imports successfully on a machine with neither
  opencv-python nor Pillow, the design holds.

Wire format
-----------
POST /infer
  Content-Type: application/octet-stream
  body: exactly width*height*3 bytes of RGB8, row-major. No header, no prefix,
        no container. numpy.frombuffer is the whole decoder on this side, so no
        image library is needed and the wire format cannot drift with a codec
        version.

  Headers carry everything else:
    X-Frame-Width:  640
    X-Frame-Height: 384
    X-Sequence:     41
    X-Subformat:    rgb

  200 application/json
  {"seq":41,"boxes":[{"cls":"bird","score":0.87,"x1":0.1,"y1":0.2,"x2":0.3,"y2":0.4}],
   "width":640,"height":384,"model":"yolov8n","infer_ms":12.5,
   "telemetry":{...}}

  x/y are normalised to [0,1] against the ORIGINAL frame, so a model input size
  of 640x640 does not change what a box means.

  The telemetry block is not decoration. It is what makes a WRONG DECODE VISIBLE
  instead of a clean run: `coord_space` says whether the boxes were pixels or
  fractions, `has_objectness` says which layout was read, `identical_geometry`
  says how many boxes landed in exactly the same place, and `median_object_px`
  says how big the objects are in the frame's own pixels. Every one of those
  turned out to be a lie in a previous build and every one of them is now a
  number a reader can check.

  Anything this process cannot do is a non-200 with a plain-text reason. It
  never answers 200 with an empty box list to mean "something went wrong": the
  Go side treats an empty list as a real negative observation, and this process
  is not allowed to manufacture one.

GET /healthz -> {"ready":bool,"model":str,"input_width":int,"input_height":int,
                 "providers":[...], "export":{...}}

GET /probe -> the same as healthz plus the measured export contract, for
              `pocctl model-probe`.

Run:  python server.py --host 127.0.0.1 --port 8099 --model models/yolov8n.onnx
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

# The import guard is a feature, not a check to be removed: if someone adds a
# vision library here, the design is broken and this says so out loud.
try:
    import numpy as np
except ImportError as exc:  # pragma: no cover
    sys.stderr.write(
        "pyworker: numpy is required. Install it with: pip install -r requirements.txt\n"
    )
    raise SystemExit(2) from exc

for FORBIDDEN in ("cv2", "PIL", "imageio", "av", "decord"):
    if FORBIDDEN in sys.modules:
        sys.stderr.write(
            f"pyworker: {FORBIDDEN} is imported. This sidecar must not use an image "
            "or capture library: Go owns the clock and this process owns inference.\n"
        )
        raise SystemExit(2)


def load_onnx(path: str) -> tuple[Any, Any]:
    """Load the model and its session, or exit with a reason an operator can act on."""
    try:
        import onnxruntime as ort
    except ImportError as exc:  # pragma: no cover
        sys.stderr.write(
            "pyworker: onnxruntime is required. Install it with: pip install -r requirements.txt\n"
        )
        raise SystemExit(2) from exc

    if not os.path.exists(path):
        sys.stderr.write(
            f"pyworker: no model at {path}. Run 'pocctl fetch-model' first; a run that "
            "starts without a model dies in minute one and the report would show a dropout.\n"
        )
        raise SystemExit(2)

    # CPU only unless the machine proves otherwise. A CUDA provider that fails at
    # load time would take the whole sidecar down, and a CPU fallback that is
    # silent would make the p99 budget a different number than the operator
    # declared. The providers list is reported so the report can print it.
    try:
        available = ort.get_available_providers()
    except Exception:  # pragma: no cover
        available = ["CPUExecutionProvider"]
    providers = ["CPUExecutionProvider"]
    if any(p.startswith("CUDA") for p in available):
        candidates = [p for p in available if p.startswith("CUDA")] + ["CPUExecutionProvider"]
        try:
            sess = ort.InferenceSession(path, providers=candidates)
        except Exception as exc:
            sys.stderr.write(
                f"pyworker: CUDA provider failed to initialise ({exc}); falling back to CPU. "
                "The report will print the provider list, so the p99 is attributable.\n"
            )
            sess = ort.InferenceSession(path, providers=["CPUExecutionProvider"])
        providers = sess.get_providers()
    else:
        sess = ort.InferenceSession(path, providers=["CPUExecutionProvider"])
    return sess, (providers, ort)


# The floor below which the sidecar refuses to send a box, and the most it will
# send. Both are here so the sidecar never becomes the decision: the run's
# SP_POC_SCORE_THRESHOLD and its approved class list are the decision, and a
# reader can see them in the configuration. What the sidecar does is refuse to
# send noise.
#
# The two NMS thresholds are TWO, not one, and the reason is a specific failure.
# A single class-aware NMS at 0.45 leaves both a `car` box and a `truck` box on
# ONE object whenever the two class scores are close, and the sidecar then reports
# two vehicles where there is one. The second, class-agnostic pass is what removes
# it: the two boxes overlap each other completely, and a pass that does not care
# which class won collapses them to the higher score. A pass that cares about the
# class cannot.
#
# Both are declared configuration (SP_POC_NMS_IOU_CLASS, SP_POC_NMS_IOU) and both
# are echoed into the telemetry, because a threshold nobody can see is a threshold
# nobody can argue with.
MIN_EMITTED_SCORE = 0.05
MAX_EMITTED_BOXES = 100
NMS_IOU_CLASS = 0.45
NMS_IOU_GLOBAL = 0.75

# The area below which a box is noise and the aspect ratio outside which it is
# almost certainly a sliver of a shadow. Both are fractions of the FRAME, and
# both default to nothing: they are read from the environment when the supervisor
# passes them, and the values here are the documented fallbacks.
DEFAULT_MIN_BOX_AREA = 0.0005

# The COCO 80-class vocabulary, for the model zoo's yolov4 which embeds no names
# of its own. Without it every box arrives as the string "9".
#
# It lives HERE and not in the Go side, and the direction is one-way: names come
# from the MODEL, families come from the PRODUCT. This table is a fallback for an
# export that carries no names at all; it is not a taxonomy, and it must never
# grow one. `internal/classify` owns the family grouping and a test asserts that
# this file never mentions one.
COCO_CLASSES = [
    "person", "bicycle", "car", "motorcycle", "airplane", "bus", "train", "truck", "boat",
    "traffic light", "fire hydrant", "stop sign", "parking meter", "bench", "bird", "cat",
    "dog", "horse", "sheep", "cow", "elephant", "bear", "zebra", "giraffe", "backpack",
    "umbrella", "handbag", "tie", "suitcase", "frisbee", "skis", "snowboard", "sports ball",
    "kite", "baseball bat", "baseball glove", "skateboard", "surfboard", "tennis racket",
    "bottle", "wine glass", "cup", "fork", "knife", "spoon", "bowl", "banana", "apple",
    "sandwich", "orange", "broccoli", "carrot", "hot dog", "pizza", "donut", "cake", "chair",
    "couch", "potted plant", "bed", "dining table", "toilet", "tv", "laptop", "mouse",
    "remote", "keyboard", "cell phone", "microwave", "oven", "toaster", "sink",
    "refrigerator", "book", "clock", "vase", "scissors", "teddy bear", "hair drier",
    "toothbrush",
]

# Minimum aspect ratio and its reciprocal. A box 20 times wider than it is tall
# is not an object; it is the edge of a shadow, a railing, or a letterbox band.
MIN_ASPECT = 1.0 / 8.0
MAX_ASPECT = 8.0


def _sigmoid(x: np.ndarray) -> np.ndarray:
    """A numerically stable logistic, because a logit of -6 or +6 overflows
    exp() and the whole array becomes NaN, and a NaN is not a low score."""
    return 1.0 / (1.0 + np.exp(-np.clip(x, -60.0, 60.0)))


def _is_probability(a: np.ndarray) -> bool:
    """Whether a tensor already holds probabilities rather than logits.

    Decided on the RANGE, not on the model: a model that emits probabilities is
    left alone, because a sigmoid on a probability would squash every detection
    toward 0.5 and turn a good 0.9 into a useless 0.71.

    THIS FUNCTION IS ONLY EVER CALLED ON THE OBJECTNESS AND CLASS CHANNELS.
    That is the whole of D1 and it is worth saying loudly, because the previous
    version also ran it over the four box fields — and a YOLOv8 emits `cx,cy,w,h`
    in INPUT PIXELS, so `sigmoid(300)` is 1.0 to fifteen decimal places. Every
    box on every frame became `(0.5,0.5)-(1.0,1.0)` after clamping, every frame
    reported eight identical boxes, and the run came out clean. A box coordinate
    is a length. It is never a probability and nothing here may treat it as one.
    """
    if a.size == 0:
        return True
    lo = float(np.min(a))
    hi = float(np.max(a))
    return lo >= 0.0 and hi <= 1.0


def nms(boxes: np.ndarray, scores: np.ndarray, classes: np.ndarray, iou_thr: float) -> list[dict]:
    """Plain greedy NMS, per class. numpy only, and it is the only filtering here.

    Called TWICE per frame: once class-aware at NMS_IOU_CLASS and once
    class-agnostic at NMS_IOU_GLOBAL. See the note above the two constants for
    why one pass is not enough.
    """
    keep: list[dict] = []
    for cls in np.unique(classes):
        idx = np.flatnonzero(classes == cls)
        order = idx[np.argsort(-scores[idx])]
        taken: set[int] = set()
        for i in order:
            if int(i) in taken:
                continue
            keep.append({"cls": int(cls), "score": float(scores[i]), "box": list(boxes[i])})
            for j in order:
                if int(j) in taken or int(j) == int(i):
                    continue
                if iou(boxes[i], boxes[j]) > iou_thr:
                    taken.add(int(j))
    return keep


def nms_global(boxes: np.ndarray, scores: np.ndarray, iou_thr: float) -> list[dict]:
    """Class-agnostic greedy NMS.

    This is the pass that removes a `car` box and a `truck` box sitting on the
    same object. The class-aware pass above CANNOT do it, by construction: it only
    ever compares boxes of the same class, so two boxes of different classes that
    overlap completely both survive it, and the sidecar reports two vehicles where
    there is one. The engine's tracker then associates both with one track, the
    picture shows one object, and the report says two — which is the whole family
    of bug a reader cannot see.
    """
    order = np.argsort(-scores)
    keep: list[dict] = []
    taken: set[int] = set()
    for i in order:
        if int(i) in taken:
            continue
        keep.append({"cls": -1, "score": float(scores[i]), "box": list(boxes[i])})
        for j in order:
            if int(j) in taken or int(j) == int(i):
                continue
            if iou(boxes[i], boxes[j]) > iou_thr:
                taken.add(int(j))
    return keep


def iou(a: list[float], b: list[float]) -> float:
    ax1, ay1, ax2, ay2 = a
    bx1, by1, bx2, by2 = b
    ix1, iy1 = max(ax1, bx1), max(ay1, by1)
    ix2, iy2 = min(ax2, bx2), min(ay2, by2)
    iw, ih = max(0.0, ix2 - ix1), max(0.0, iy2 - iy1)
    inter = iw * ih
    if inter <= 0:
        return 0.0
    area_a = max(0.0, ax2 - ax1) * max(0.0, ay2 - ay1)
    area_b = max(0.0, bx2 - bx1) * max(0.0, by2 - by1)
    union = area_a + area_b - inter
    return inter / union if union > 0 else 0.0


def letterbox(rgb: np.ndarray, in_w: int, in_h: int, layout: str = "nchw") -> tuple[np.ndarray, int, int, int, int]:
    """Resize preserving aspect ratio, pad the rest, and put the result in the
    model's own layout.

    Returns `(tensor, nw, nh, pad_x, pad_y)` — the CONTENT size and the padding
    offset, not just a tensor. The caller NEEDS those to un-pad, and the previous
    version returned only a tensor and then divided by the INPUT size, which is
    D4: a 640x384 frame letterboxed into a 640x640 canvas has a content height of
    384, so dividing y by 640 squashes every box to 0.6x its true height. Every
    object in the picture was drawn short and every crossing happened early.

    The content is placed AT the pad offset, which is the whole point of returning
    it. The decoder subtracts `pad_x`/`pad_y` from the model's coordinates, and
    that is only correct if the content was drawn there: a top-left anchor with a
    centred subtraction shifts every box up by the pad on every frame, which looks
    like a detector that cannot find the road. The two halves have to agree and
    the way they are made to agree is by the content being where the offset says.

    The layout is a PROPERTY OF THE MODEL, not a convention: ONNX exports both
    `NCHW` (what Ultralytics writes) and `NHWC` (what the ONNX model zoo writes,
    and what the measured model in this repository wants). Feeding a NCHW tensor
    to an NHWC input produces confident nonsense, and confident nonsense is worse
    than nothing: it produces line crossings on objects that are not there.

    Nearest-neighbour resampling keeps the dependency list at numpy. On a 5 fps
    paced stream the cost is irrelevant, and the model's own accuracy on this
    material is not what this POC measures.
    """
    h, w, _ = rgb.shape
    scale = min(in_w / w, in_h / h)
    nw, nh = max(1, int(round(w * scale))), max(1, int(round(h * scale)))
    yi = (np.arange(nh) / scale).astype(np.int32).clip(0, h - 1)
    xi = (np.arange(nw) / scale).astype(np.int32).clip(0, w - 1)
    small = rgb[yi][:, xi]
    pad_x, pad_y = (in_w - nw) // 2, (in_h - nh) // 2
    out = np.zeros((in_h, in_w, 3), dtype=np.uint8)
    out[pad_y:pad_y + nh, pad_x:pad_x + nw] = small
    tensor = out.astype(np.float32) / 255.0
    if layout == "nhwc":
        return tensor, nw, nh, pad_x, pad_y
    return np.transpose(tensor, (2, 0, 1)), nw, nh, pad_x, pad_y


def detect_layout(shape) -> str:
    """Decide NCHW or NHWC from the declared input shape.

    The channel count is small and the spatial dimensions are large, so the
    position of the small one is the answer. The first model this POC ran against
    was NHWC, and a code path that only knew NCHW would have produced confident
    nonsense from it.
    """
    if len(shape) != 4:
        raise ValueError(f"expected a rank-4 input (N,?,?,?), got {shape}")
    channels = (1, 3, 4)
    if shape[1] in channels:
        return "nchw"
    if shape[3] in channels:
        return "nhwc"
    raise ValueError(
        f"cannot tell whether the input is NCHW or NHWC from {shape}: neither axis 1 nor "
        f"axis 3 holds a plausible channel count {channels}. Guessing here would put boxes "
        f"on the wrong objects, so the model is refused."
    )


def load_anchors(path: str) -> list[list[tuple[float, float]]]:
    """Read a darknet anchors file into one list per output scale.

    A darknet YOLO head emits coordinates RELATIVE to a per-cell anchor box, and
    the anchor boxes live in the model's `.cfg`, not in the ONNX graph. The model
    zoo's export does not embed them, so they are supplied from a file, and this
    function REFUSES a file it cannot read rather than substituting a default: a
    guessed anchor set produces boxes in the right place with the wrong sizes,
    which is the worst of both answers.

    Format: one `width,height` per line, blank lines and `#` comments ignored,
    grouped by output in the order the session declares them (finest stride
    first). The yolov4-416 set is 3 x 9 and the file that ships with this
    repository says so in its own header.
    """
    try:
        with open(path, "r", encoding="utf-8") as fh:
            lines = fh.readlines()
    except OSError as exc:
        raise ValueError(
            f"pyworker: the anchors file {path} could not be read: {exc}. A darknet export "
            f"needs its anchor boxes, which are NOT in the ONNX file, so this sidecar will "
            f"not guess them."
        ) from exc
    pairs: list[tuple[float, float]] = []
    for i, line in enumerate(lines, start=1):
        s = line.split("#", 1)[0].strip()
        if not s:
            continue
        parts = [p for p in s.replace(",", " ").split() if p]
        if len(parts) != 2:
            raise ValueError(
                f"pyworker: {path} line {i} is {line.strip()!r}, which is not a `width,height` "
                f"pair. The anchors are the model's own configuration and this sidecar will "
                f"not substitute a default for them."
            )
        try:
            pairs.append((float(parts[0]), float(parts[1])))
        except ValueError as exc:
            raise ValueError(
                f"pyworker: {path} line {i} has a non-numeric anchor {parts!r}."
            ) from exc
    if not pairs:
        raise ValueError(f"pyworker: {path} contains no anchors.")
    per = len(pairs) // 3
    if per == 0 or len(pairs) % 3 != 0:
        raise ValueError(
            f"pyworker: {path} has {len(pairs)} anchors, which does not divide into three "
            f"scales. A three-scale darknet head needs a multiple of 3."
        )
    return [pairs[i * per:(i + 1) * per] for i in range(3)]


def parse_class_names(raw: str, model_name: str) -> dict[int, str]:
    """Parse the `names` metadata an Ultralytics export carries.

    Ultralytics writes it as a Python dict REPR: `{0: 'person', 1: 'bicycle', ...}`.
    The previous parser did `int(kv.split(":", 1)[0])` on each comma-separated
    piece, which raises ValueError on the leading `{` of the first piece and killed
    the sidecar at load. A model that loads and then cannot be named is a model
    whose every box arrives as the string "9".

    Three forms are accepted because three exporters exist:

        {0: 'person', 1: 'bicycle'}   Ultralytics, with braces and quotes
        {0: person, 1: bicycle}       the same, without the quotes
        0:person,1:bicycle            a plain list of pairs

    Anything else is a REFUSAL with the raw string in the message, at load time,
    before any frame is sent. A class list that was silently half-parsed would
    shift every class index by one for the second half of the vocabulary, and
    nothing downstream could tell.
    """
    if not raw or not raw.strip():
        return {}
    s = raw.strip()
    if s.startswith("{"):
        s = s[1:]
    if s.endswith("}"):
        s = s[:-1]
    out: dict[int, str] = {}
    for piece in s.split(","):
        piece = piece.strip()
        if not piece:
            continue
        if ":" not in piece:
            raise ValueError(
                f"pyworker: {model_name} carries class names this sidecar cannot read. "
                f"The entry {piece!r} has no ':' separating an index from a name. "
                f"The raw metadata was: {raw!r}. Refusing here, at load, because a "
                f"half-parsed class list shifts every index after the first bad entry "
                f"and nothing downstream can tell."
            )
        key, _, value = piece.partition(":")
        try:
            idx = int(key.strip())
        except ValueError as exc:
            raise ValueError(
                f"pyworker: {model_name} carries class names this sidecar cannot read. "
                f"Index {key.strip()!r} is not an integer. The raw metadata was: {raw!r}. "
                f"Refusing at load rather than answering with box numbers."
            ) from exc
        name = value.strip().strip("'\"").strip()
        if not name:
            raise ValueError(
                f"pyworker: {model_name} has an empty class name at index {idx}. "
                f"The raw metadata was: {raw!r}."
            )
        out[idx] = name
    if not out:
        raise ValueError(
            f"pyworker: {model_name} has class metadata that parsed to nothing. "
            f"The raw metadata was: {raw!r}."
        )
    return out


class ExportSpec:
    """What the loaded model actually is, decided at load and never guessed later.

    D-1: the model is swappable through SP_POC_AI_MODEL with no code change, so
    every one of these is MEASURED from the loaded session and from the tensor it
    produced. The alternative is a set of assumptions about YOLOv8, and the
    previous build's four-bug collapse was four assumptions in a row:

      - `decoder_family` decides whether the coordinates need ANCHOR decoding at
        all. This is the case the plan did not enumerate and it is the one the
        SHIPPED model turns out to be: `models/yolov8n.onnx` is a three-output
        darknet YOLOv4 export converted by tf2onnx, despite its name. Its box
        fields are anchor-relative, so a "fused" decode of it does not produce
        wrong boxes, it produces NOTHING, and nothing is the hardest failure to
        tell from a quiet scene.
      - `has_objectness` decides whether column 4 is an objectness or the first
        class. Getting it wrong shifts EVERY class by one: `car` is read as
        `person` and `bird` as `dog`. That was D3.
      - `coord_space` decides whether the coordinates are pixels or fractions. It
        is decided on the RAW, untransformed values, which is D2 and the reason
        the decision has to happen before anything touches them.
      - `fields` is the width of one detection's record, counted from the tensor
        and never from a constant.
    """

    # The two families, and no others. A third would be a guess.
    FAMILY_FUSED = "fused"
    FAMILY_ANCHORED = "anchored"

    def __init__(self, layout: str, input_w: int, input_h: int, fields: int = 0,
                 has_objectness: bool | None = None, coord_space: str = "unknown",
                 classes: dict[int, str] | None = None,
                 decoder_family: str = FAMILY_FUSED) -> None:
        self.layout = layout
        self.input_w = input_w
        self.input_h = input_h
        self.fields = fields
        self.has_objectness = has_objectness
        self.coord_space = coord_space
        self.classes = classes or {}
        self.decoder_family = decoder_family

    @property
    def n_classes(self) -> int:
        if self.classes:
            return len(self.classes)
        return max(0, self.fields - (4 if self.has_objectness is False else 5))

    def decide_objectness(self, fields: int) -> bool | None:
        """Whether column 4 is an objectness, from the FIELD COUNT and the names.

        T-3.1: `fields == 5 + len(classes)` means objectness, `fields == 4 +
        len(classes)` means the classes start at column 4. When the model carries
        its own `names`, its length IS `len(classes)` and the arithmetic is exact.
        When it carries none, the COCO-80 shapes are the only two this sidecar
        knows how to read, and anything else returns None — which becomes
        `cannot_tell` on the Go side rather than a guess.

        A guess here is the single most expensive possible guess in this file: it
        does not produce a visibly wrong answer, it produces a VISIBLY RIGHT one.
        A `truck` reported as a `bench` is a box in the right place with the wrong
        word on it, which is exactly what a reader cannot catch by looking.
        """
        if self.classes:
            n = len(self.classes)
            if fields == 4 + n:
                return False
            if fields == 5 + n:
                return True
            return None
        if fields == 84:
            return False   # 4 box + 80 COCO classes: the YOLOv8 shape
        if fields == 85:
            return True    # 4 box + 1 objectness + 80 COCO classes: the yolov4 shape
        return None

    @staticmethod
    def decide_family(shapes: list[list]) -> str:
        """Whether the outputs are FUSED (Ultralytics) or ANCHORED (darknet).

        Decided on the output STRUCTURE, measured, because that is the only thing
        that differs:

          - Ultralytics exports ONE output: `(batch, 4+nc, N)`. The coordinates in
            it are already final — pixels or fractions of the input, no anchor
            step.
          - A darknet head exports one output PER SCALE, each five-dimensional
            `(batch, grid, grid, anchors, 4+1+nc)`. The coordinates in it are
            offsets from an anchor box that the graph does not carry.

        The five-dimensional, multi-output shape is not a subtle signature. A
        detector whose fields are on the last axis of a 5-D tensor and which
        publishes three of them is a darknet head, and reading it as a fused
        export is what makes every box vanish.
        """
        if len(shapes) > 1 and any(len(sh) >= 5 for sh in shapes):
            return ExportSpec.FAMILY_ANCHORED
        if len(shapes) == 1 and len(shapes[0]) >= 5:
            return ExportSpec.FAMILY_ANCHORED
        return ExportSpec.FAMILY_FUSED

    def to_json(self) -> dict:
        return {
            "layout": self.layout,
            "input_w": self.input_w,
            "input_h": self.input_h,
            "fields": self.fields,
            "has_objectness": self.has_objectness,
            "coord_space": self.coord_space,
            "decoder_family": self.decoder_family,
            "classes": len(self.classes),
        }

    @staticmethod
    def from_json(d: dict) -> "ExportSpec":
        return ExportSpec(
            layout=d.get("layout", "nchw"),
            input_w=int(d.get("input_w", 640)),
            input_h=int(d.get("input_h", 640)),
            fields=int(d.get("fields", 0)),
            has_objectness=d.get("has_objectness"),
            coord_space=d.get("coord_space", "unknown"),
            classes={int(k): v for k, v in (d.get("names") or {}).items()},
            decoder_family=d.get("decoder_family", ExportSpec.FAMILY_FUSED),
        )


class Engine:
    """The model, its input shape, its class names. Nothing else is kept."""

    def __init__(self, model_path: str, min_box_area: float = DEFAULT_MIN_BOX_AREA,
                 anchors_path: str = "") -> None:
        self.session, (self.providers, ort) = load_onnx(model_path)
        self.model_name = os.path.basename(model_path)
        self.min_box_area = min_box_area
        meta = self.session.get_inputs()[0]
        self.input_name = meta.name
        shape = list(meta.shape)
        self.layout = detect_layout(shape)
        spatial = shape[2:4] if self.layout == "nchw" else shape[1:3]
        self.input_h = int(spatial[0]) if isinstance(spatial[0], int) else 640
        self.input_w = int(spatial[1]) if isinstance(spatial[1], int) else 640
        if self.input_h != self.input_w:
            raise SystemExit(
                f"pyworker: {self.model_name} wants a {self.input_h}x{self.input_w} input, and this "
                f"sidecar only letterboxes to a square. A non-square model needs its own pre/post "
                f"processing and pretending otherwise would put boxes on the wrong pixels."
            )
        labels = self.session.get_modelmeta().custom_metadata_map.get("names", "")
        self.classes = parse_class_names(labels, self.model_name)

        out_metas = self.session.get_outputs()
        out_shapes = [list(m.shape) for m in out_metas]
        family = ExportSpec.decide_family(out_shapes)
        self.spec = ExportSpec(self.layout, self.input_w, self.input_h,
                               classes=dict(self.classes), decoder_family=family)
        self.output_names = [m.name for m in out_metas]
        self.declared_fields = 0
        for sh in out_shapes:
            if sh and isinstance(sh[-1], int):
                self.declared_fields = int(sh[-1])
                break
        self.anchors = None
        if family == ExportSpec.FAMILY_ANCHORED:
            if anchors_path:
                self.anchors = load_anchors(anchors_path)
                self.anchors_path = anchors_path
            else:
                self.anchors_path = ""
                # Refused HERE, at load, by name. The alternative is to run, find
                # nothing, and answer `no_objects` for every frame of a seven-day
                # run — a run that reports a clean scene on a camera with cars in
                # it, and one that a reader has no way to tell from a model that
                # genuinely sees nothing.
                raise SystemExit(
                    f"pyworker: {self.model_name} is an ANCHORED darknet export: it publishes "
                    f"{len(self.output_names)} scale outputs of rank "
                    f"{[len(s) for s in out_shapes]}, which means its box coordinates are offsets "
                    f"from per-cell ANCHOR boxes.\n"
                    f"  The anchor boxes are part of the model's .cfg and are NOT in the ONNX file, "
                    f"so this sidecar cannot decode {self.model_name} without them, and it will not "
                    f"guess them: wrong anchors produce boxes in the right place with the wrong sizes.\n"
                    f"  Supply them with --anchors <file> (one 'width,height' per line, three "
                    f"scales, finest stride first), or point SP_POC_AI_MODEL at a fused export such "
                    f"as an Ultralytics YOLOv8 ONNX, which needs no anchors.\n"
                    f"  'pocctl model-probe' prints the measured structure of this model."
                )

    # The shape normaliser, the one place that touches a tensor's axes.

    # A detector's RECORD is a handful of fields — 5 for a box plus a class, 85
    # for a box plus an objectness plus 80 classes. The number of CANDIDATES is
    # far larger: 8400 anchors for 84 fields on a YOLOv8, 8112 for 85 on the
    # measured zoo export, and never fewer than a few thousand on any real
    # export.
    MAX_FIELDS = 1000
    MIN_CANDIDATES = 1000

    def rows(self, raw) -> tuple[np.ndarray | None, str]:
        """Flatten a raw output tensor to `(N, F)` with FIELDS ON THE LAST AXIS.

        Two layouts exist in the wild and both are handled here, once:

          - the ONNX model zoo's yolov4 exports rank FIVE: `(1, 52, 52, 3, 85)`.
            The fields are the last axis and everything before it is a spatial
            grid that flattens away. This is the SHIPPED model in this repository,
            measured, not assumed: `models/yolov8n.onnx` really does come out as
            `(1, 52, 52, 3, 85)`.
          - Ultralytics' YOLOv8 exports rank three: `(1, 84, 8400)`, FIELDS FIRST.

        The previous version chose its path on the RANK, matching 3 or 4, so rank
        5 matched neither and every run reported zero detections — on the
        committed synthetic material AND on a real traffic camera at night. Both
        of those zero readings were this bug and not the material.

        The field axis is found in two tiers, and the first tier is the one that
        actually has evidence behind it:

        1. THE MODEL'S OWN VOCABULARY. An export with `n` class names has
           `4 + n` or `5 + n` fields and no other number, so an axis of exactly
           that length is the field axis. This is not a heuristic: it is the
           model's own metadata, and it is the only tier that can settle a tensor
           whose shape is genuinely ambiguous — `(84, 50)` is 84 records of 50
           fields or 50 records of 84 fields, and a shape like that is decided by
           how many classes the model says it has and by nothing else.

        2. STRUCTURE, for an export that carries no names. The field axis is
           narrow (5 to 1000) and everything outside it is at least 1000
           candidates wide, because no real detector has fewer than a few
           thousand anchors, and where more than one axis qualifies the LAST one
           wins because that is the axis a multi-scale darknet head puts them on.

        A tensor where neither tier finds an axis is REFUSED. That is the only
        safe answer available and it is what `probe_model.py` reports as
        `understood: false`, so the refusal is never silent.
        """
        arr = np.asarray(raw)
        if arr.ndim < 2:
            return None, "unrecognised"
        # Drop leading batch axes while the first axis is 1 and something remains.
        while arr.ndim > 2 and arr.shape[0] == 1:
            arr = arr[0]
        if arr.ndim < 2 or arr.size == 0:
            return None, "unrecognised"

        total = int(arr.size)
        chosen = -1
        n = len(self.classes)
        wanted = {4 + n, 5 + n} if n else set()
        if n:
            for axis in range(arr.ndim):
                if int(arr.shape[axis]) in wanted:
                    chosen = axis
            if chosen < 0:
                return None, (f"the model declares {n} class names, so a detection must have "
                              f"{sorted(wanted)} fields, and no axis of {arr.shape} is that wide")
        else:
            for axis in range(arr.ndim):
                a = int(arr.shape[axis])
                if 5 <= a <= Engine.MAX_FIELDS and total // a >= Engine.MIN_CANDIDATES:
                    chosen = axis
        if chosen < 0:
            return None, (f"no axis of {arr.shape} could be identified as a field count: none is "
                          f"between 5 and {Engine.MAX_FIELDS} and at least "
                          f"narrower than {Engine.MIN_CANDIDATES} candidates, and none is "
                          f"{sorted(wanted)} for a model with {n} class names"
                          if n else
                          f"no axis of {arr.shape} could be identified as a field count: none is "
                          f"between 5 and {Engine.MAX_FIELDS} and at least "
                          f"narrower than {Engine.MIN_CANDIDATES} candidates")
        width = int(arr.shape[chosen])
        if chosen == arr.ndim - 1:
            return arr.reshape(-1, width), "fields-last"
        moved = np.moveaxis(arr, chosen, -1)
        return moved.reshape(-1, width), "fields-first"
    # -- the decode -----------------------------------------------------------

    def _finalise(self, boxes_xy, scores, classes, w: int, h: int) -> tuple[list[dict], dict]:
        """NMS, clamp, gate, and shape the wire boxes. The ONE place that happens.

        Both decoder families end here, on purpose: a decode that filtered on one
        path and not the other would make `post_nms` and `dropped_tiny` mean two
        different things depending on which model is loaded, and a reader
        comparing two runs could not tell why the numbers differ.

        The order is CLAMP FIRST, then gate. It is the other way round in most
        decoders and it is wrong here for a specific reason: a box that sits
        entirely outside the frame has a perfectly healthy area BEFORE clamping
        and no area AT ALL after it, so a gate applied to the raw values waves it
        through and the wire then carries `{x1: 0, y1: 0, x2: 0, y2: 0}`. That is
        not a small thing: `x2 <= x1` makes the Go client reject the whole answer
        as `cannot_tell`, so ONE box off the top of the frame turns a frame with
        nineteen good detections into a frame the pipeline refuses to interpret.
        """
        if len(boxes_xy) == 0:
            return [], {"post_nms_class": 0, "post_nms": 0, "dropped_degenerate": 0, "dropped_tiny": 0}
        boxes_xy = np.asarray(boxes_xy, dtype=np.float64)
        scores = np.asarray(scores, dtype=np.float64)
        classes = np.asarray(classes, dtype=np.int64)
        after_class = nms(boxes_xy.tolist(), scores, classes, NMS_IOU_CLASS)
        if not after_class:
            return [], {"post_nms_class": 0, "post_nms": 0, "dropped_degenerate": 0, "dropped_tiny": 0}
        after_both = nms_global(
            np.array([s["box"] for s in after_class], dtype=np.float64),
            np.array([s["score"] for s in after_class], dtype=np.float64),
            NMS_IOU_GLOBAL)
        if after_both:
            by_box = {(round(s["box"][0], 9), round(s["box"][1], 9), round(s["box"][2], 9), round(s["box"][3], 9)): s["cls"]
                      for s in after_class}
            chosen = []
            for s in after_both:
                b = s["box"]
                key = (round(b[0], 9), round(b[1], 9), round(b[2], 9), round(b[3], 9))
                chosen.append({"cls": by_box.get(key, -1), "score": s["score"], "box": b})
        else:
            chosen = []
        chosen = chosen[:MAX_EMITTED_BOXES]

        out: list[dict] = []
        dropped_degenerate = 0
        dropped_tiny = 0
        for s in chosen:
            bx1, by1, bx2, by2 = s["box"]
            # Clamp, THEN measure. See the method comment: a box that clamps to
            # nothing must not be sent as a zero-size box, because the Go client
            # reads an inverted box as an uninterpretable answer.
            cx1 = min(max(bx1, 0.0), 1.0)
            cy1 = min(max(by1, 0.0), 1.0)
            cx2 = min(max(bx2, 0.0), 1.0)
            cy2 = min(max(by2, 0.0), 1.0)
            if cx2 - cx1 <= 1e-6 or cy2 - cy1 <= 1e-6:
                dropped_degenerate += 1
                continue
            area = (cx2 - cx1) * (cy2 - cy1)
            aspect = (cx2 - cx1) / max(1e-9, cy2 - cy1)
            if area < self.min_box_area or aspect < MIN_ASPECT or aspect > MAX_ASPECT:
                dropped_tiny += 1
                continue
            out.append({
                "cls": self.class_name(int(s["cls"])),
                "score": round(float(s["score"]), 6),
                "x1": round(cx1, 6),
                "y1": round(cy1, 6),
                "x2": round(cx2, 6),
                "y2": round(cy2, 6),
            })
        out.sort(key=lambda b: -b["score"])
        return out, {
            "post_nms_class": len(after_class),
            "post_nms": len(chosen),
            "dropped_degenerate": dropped_degenerate,
            "dropped_tiny": dropped_tiny,
        }

    def infer(self, seq: int, rgb: np.ndarray) -> dict:
        started = time.perf_counter()
        h, w, _ = rgb.shape
        blob, nw, nh, pad_x, pad_y = letterbox(rgb, self.input_w, self.input_h, self.layout)
        outputs = self.session.run(None, {self.input_name: blob[None, ...]})
        if self.spec.decoder_family == ExportSpec.FAMILY_ANCHORED:
            boxes, telemetry = self.decode_anchored(outputs, w, h, nw, nh, pad_x, pad_y)
        else:
            raw = np.asarray(outputs[0])
            boxes, telemetry = self.decode(raw, w, h, nw, nh, pad_x, pad_y)
        out = {
            "seq": seq,
            "boxes": boxes,
            "width": w,
            "height": h,
            "model": self.model_name,
            "infer_ms": (time.perf_counter() - started) * 1000.0,
            "telemetry": telemetry,
        }
        return out

    def decode_anchored(self, outputs, w: int, h: int, nw: int, nh: int, pad_x: int, pad_y: int):
        """Decode a multi-scale darknet head: one output per stride.

        The three outputs are the SAME detector at three strides — 8, 16 and 32
        for a 416 input — and each one's coordinates are offsets from its own
        anchor boxes. The transform, which is the whole of what an anchored export
        needs and what a fused export must NOT be given:

            cx = (sigmoid(tx) + col) / grid_w
            cy = (sigmoid(ty) + row) / grid_h
            w  = exp(tw) * anchor_w / input_w
            h  = exp(th) * anchor_h / input_h

        and then the letterbox pad is removed and the result is divided by the
        CONTENT size, because a 640x384 frame in a 416x416 canvas has a content
        height of 250 and dividing by 416 squashes every box to 0.6x.

        A sigmoid appears HERE, on `tx` and `ty` only, because in a darknet export
        those two genuinely are logits. It is exactly the transform D1 removed
        from the fused path, and the reason it is safe here and was not there is
        that the coordinate space is different: these are fractions of a CELL
        before the transform and pixels after it, and the fused path's numbers are
        pixels from the start.
        """
        cand_all: list[dict] = []
        candidates = 0
        pre_nms = 0
        specs = self._anchored_specs(outputs)
        if not specs:
            t = _empty_telemetry()
            t.update(self.spec.to_json())
            t["note"] = "the anchored export produced nothing this sidecar can read"
            return [], t
        for (grid_h, grid_w, anchors, tensor) in specs:
            arr = np.asarray(tensor)
            if arr.ndim != 5:
                continue
            arr = arr[0] if arr.shape[0] == 1 else arr
            gh, gw, na, fields = arr.shape
            if self.spec.fields != fields:
                # The same field-count arithmetic as the fused path, so the
                # telemetry means the same thing whichever export is loaded.
                self.spec.fields = fields
                self.spec.has_objectness = self.spec.decide_objectness(fields)
            if self.spec.has_objectness is False:
                t = _empty_telemetry()
                t.update(self.spec.to_json())
                t["note"] = (
                    f"an anchored export with {fields} fields per detection and "
                    f"{len(self.classes)} class names does not match the anchored layout, "
                    f"which is 4 box + 1 objectness + classes. Refused rather than read with "
                    f"every class shifted by one."
                )
                return [], t
            flat = arr.reshape(-1, fields)
            candidates += flat.shape[0]
            boxes, pre, dec = self._decode_anchored_scale(flat, grid_h, grid_w, anchors, w, h, nw, nh, pad_x, pad_y)
            pre_nms += pre
            for b in boxes:
                b["degenerate"] = dec
            cand_all.extend([b for b in boxes if not b["degenerate"]])
        self.spec.coord_space = "pixel"
        if not cand_all:
            t = _empty_telemetry()
            t.update(self.spec.to_json())
            t["candidates"] = candidates
            t["pre_nms"] = pre_nms
            t["note"] = "no candidate on any scale reached the emit floor"
            return [], t
        out, counts = self._finalise(
            [b["box"] for b in cand_all], [b["score"] for b in cand_all], [b["cls"] for b in cand_all], w, h)
        tel = self._telemetry(candidates, pre_nms, counts, out, w, h)
        tel["scales"] = len(specs)
        return out, tel

    def _anchored_specs(self, outputs):
        """Work out each scale's grid and anchors from the TENSOR, not the config."""
        specs = []
        anchors = self.anchors or []
        for i, tensor in enumerate(outputs):
            arr = np.asarray(tensor)
            if arr.ndim != 5:
                continue
            a = arr[0] if arr.shape[0] == 1 else arr
            gh, gw, na = a.shape[0], a.shape[1], a.shape[2]
            use = anchors[i] if i < len(anchors) else []
            if len(use) != na:
                # The anchor file and the model disagree. That is a REFUSAL, not
                # something to interpolate around: the wrong anchor set produces
                # plausible boxes of the wrong size and a reader cannot tell.
                return []
            specs.append((gh, gw, use, arr))
        return specs

    def _decode_anchored_scale(self, flat, grid_h, grid_w, anchors, w, h, nw, nh, pad_x, pad_y):
        obj = flat[:, 4].astype(np.float64)
        if not _is_probability(obj):
            obj = _sigmoid(obj)
        class_scores = flat[:, 5:].astype(np.float64)
        if class_scores.shape[1] == 0:
            return [], 0, True
        if not _is_probability(class_scores):
            class_scores = _sigmoid(class_scores)
        score = class_scores.max(axis=1) * obj
        which = class_scores.argmax(axis=1)
        # The objectness gate FIRST, before any anchor arithmetic. This is the one
        # place an early exit is exactly right: a cell with no object has anchors
        # that mean nothing, and exp() of a raw logit is not zero.
        live = obj >= MIN_EMITTED_SCORE
        pre = int(np.count_nonzero(live))
        if pre == 0:
            return [], 0, True
        idx = np.flatnonzero(live)
        tx = flat[idx, 0].astype(np.float64)
        ty = flat[idx, 1].astype(np.float64)
        tw = flat[idx, 2].astype(np.float64)
        th = flat[idx, 3].astype(np.float64)
        which = which[idx]
        score = score[idx]
        # flat is (grid_h*grid_w*na, F) in C order, so index = (row*grid_w + col)*na + a
        pos = idx // len(anchors)
        anchor_index = idx % len(anchors)
        rows = pos // grid_w
        cols = pos % grid_w
        cx = (_sigmoid(tx) + cols) / float(grid_w)
        cy = (_sigmoid(ty) + rows) / float(grid_h)
        aw = np.array([anchors[k][0] for k in anchor_index], dtype=np.float64)
        ah = np.array([anchors[k][1] for k in anchor_index], dtype=np.float64)
        bw = np.exp(np.clip(tw, -20, 20)) * aw / float(self.input_w)
        bh = np.exp(np.clip(th, -20, 20)) * ah / float(self.input_h)
        # Fractions of the letterboxed input -> fractions of the frame, with the
        # pad removed. Dividing by input_w/input_h here is D4.
        x1 = (cx - bw / 2) * self.input_w - pad_x
        y1 = (cy - bh / 2) * self.input_h - pad_y
        x2 = (cx + bw / 2) * self.input_w - pad_x
        y2 = (cy + bh / 2) * self.input_h - pad_y
        x1, y1, x2, y2 = x1 / float(nw), y1 / float(nh), x2 / float(nw), y2 / float(nh)
        boxes = []
        for i in range(len(idx)):
            a, b, c, d = x1[i], y1[i], x2[i], y2[i]
            if d - b <= 1e-6 or c - a <= 1e-6:
                continue
            boxes.append({"cls": int(which[i]), "score": float(score[i]), "box": [a, b, c, d]})
        return boxes, pre, False

    def decode(self, raw: np.ndarray, w: int, h: int, nw: int | None = None, nh: int | None = None,
               pad_x: int = 0, pad_y: int = 0) -> tuple[list[dict], dict]:
        """Decode one raw output tensor into normalised frame boxes and telemetry.

        `nw`/`nh` are the letterboxed CONTENT size. A test that calls this
        directly must pass them, because they are what the un-padding divides by
        and there is no other way to know them: dividing by the input size is D4
        and it is wrong for every non-square frame.
        """
        if nw is None:
            nw, nh = self.input_w, self.input_h
        if nh is None:
            nh = nw
        zero = _empty_telemetry()
        flat, how = self.rows(raw)
        if flat is None or flat.shape[0] == 0 or flat.shape[1] < 6:
            # The REFUSAL'S OWN REASON, not a generic one. "The output is not a
            # detector's output" sends an operator to look at the exporter; "the
            # model declares 80 class names, so a detection must have [84, 85]
            # fields, and no axis of (1, 100, 9) is that wide" tells them which of
            # the two halves is wrong.
            zero["note"] = f"the output tensor's shape {np.asarray(raw).shape} cannot be read: {how}"
            zero["candidates"] = 0
            return [], zero
        fields = int(flat.shape[1])
        if fields < 6:
            zero["note"] = f"{fields} fields is not a detector's output"
            return [], zero

        # The field count and the objectness decision are settled from the
        # tensor's own width and the model's own names. Not from a constant, and
        # not from the file name.
        if self.spec.fields != fields:
            self.spec.fields = fields
            self.spec.has_objectness = self.spec.decide_objectness(fields)
        if self.spec.has_objectness is None:
            t = _empty_telemetry()
            t["fields"] = fields
            t["classes"] = len(self.classes)
            t["has_objectness"] = None
            t["note"] = (
                f"this export has {fields} fields per detection and the model carries "
                f"{len(self.classes)} class names, which does not match 4+classes or "
                f"5+classes. Reading it either way would shift every class by one, so "
                f"this frame is refused rather than guessed at."
            )
            return [], t

        # --- the box channels, NEVER sigmoided (D1) ---------------------------
        cx, cy, bw, bh = (flat[:, i].astype(np.float64) for i in range(4))
        # T-3.3: coord_space is decided on the RAW, untransformed coordinates.
        # D2 is that the old `emits_pixels()` measured the ALREADY-sigmoided
        # values, which are all <= 1 by construction, so the answer was always
        # "fractions", so the pixel->frame division never ran, so a pixel-space
        # model kept its raw 0..640 numbers and every box was clamped to the far
        # edge. The decision must be made here, before anything touches them.
        self.spec.coord_space = "pixel" if _looks_like_pixels(cx, cy, self.input_w, self.input_h) else "fraction"
        if self.spec.coord_space == "pixel":
            cx = (cx - pad_x) / max(1.0, float(nw))
            cy = (cy - pad_y) / max(1.0, float(nh))
            bw = bw / max(1.0, float(nw))
            bh = bh / max(1.0, float(nh))
        # else: already fractions of the model's own input, which the pad offset
        # still has to be removed from.

        # --- the score channels, sigmoided only if they are logits (D3) -------
        first_score = 4
        obj = None
        if self.spec.has_objectness:
            obj = flat[:, 4].astype(np.float64)
            if not _is_probability(obj):
                obj = _sigmoid(obj)
            first_score = 5
        class_scores = flat[:, first_score:].astype(np.float64)
        if class_scores.shape[1] == 0:
            t = _empty_telemetry()
            t["fields"] = fields
            t["has_objectness"] = self.spec.has_objectness
            t["coord_space"] = self.spec.coord_space
            t["note"] = "the tensor has a box and a score but no class channels"
            return [], t
        if not _is_probability(class_scores):
            class_scores = _sigmoid(class_scores)

        best = class_scores.max(axis=1)
        if obj is not None:
            best = best * obj
        which = class_scores.argmax(axis=1)

        # A FLOOR, and it is not optional.
        #
        # The model emits every one of its candidate anchors with some non-zero
        # score, because a sigmoid of a logit is never exactly zero. Without a
        # floor, NMS keeps thousands of them and the sidecar answers with a
        # megabyte of noise that looks like a detection rate.
        #
        # The floor here is deliberately LOW: the sidecar is not the decision.
        # It is the detector; `SP_POC_SCORE_THRESHOLD` and the approved class
        # list are the decision, and they live in the run's configuration where a
        # reader can see them. What the sidecar must do is refuse to send noise.
        keep = best >= MIN_EMITTED_SCORE
        candidates = int(flat.shape[0])
        pre_nms = int(np.count_nonzero(keep))
        if not keep.any():
            t = _empty_telemetry()
            t.update(self.spec.to_json())
            t["candidates"] = candidates
            t["pre_nms"] = 0
            t["post_nms"] = 0
            t["note"] = f"nothing scored at or above the {MIN_EMITTED_SCORE} emit floor"
            return [], t
        cx, cy, bw, bh = cx[keep], cy[keep], bw[keep], bh[keep]
        best, which = best[keep], which[keep]

        boxes_xy = np.stack([cx - bw / 2, cy - bh / 2, cx + bw / 2, cy + bh / 2], axis=1)
        out, counts = self._finalise(boxes_xy.tolist(), best, which, w, h)
        tel = self._telemetry(candidates, pre_nms, counts, out, w, h)
        return out, tel

    def _telemetry(self, candidates, pre_nms, counts: dict, boxes, w, h) -> dict:
        t = dict(self.spec.to_json())
        t.update({
            "candidates": candidates,
            "pre_nms": pre_nms,
            "post_nms_class": counts["post_nms_class"],
            "post_nms": counts["post_nms"],
            "dropped_degenerate": counts["dropped_degenerate"],
            "dropped_tiny": counts["dropped_tiny"],
            "nms_class_iou": NMS_IOU_CLASS,
            "nms_global_iou": NMS_IOU_GLOBAL,
        })
        if boxes:
            areas = [(b["x2"] - b["x1"]) * (b["y2"] - b["y1"]) for b in boxes]
            heights_px = [(b["y2"] - b["y1"]) * h for b in boxes]
            t["median_box_area"] = round(float(np.median(areas)), 6)
            t["median_object_px"] = round(float(np.median(heights_px)), 2)
            t["identical_geometry"] = _count_identical(boxes)
        else:
            t["median_box_area"] = 0.0
            t["median_object_px"] = 0.0
            t["identical_geometry"] = 0
        return t

    def class_name(self, index: int) -> str:
        """The class's NAME, from the model's own metadata when it has it and
        from the COCO vocabulary when it does not.

        The model zoo's yolov4 embeds no names, so without this every box arrives
        as the string "9" and the report's class column is a column of integers.
        """
        if self.classes:
            return self.classes.get(index, str(index))
        return COCO_CLASSES[index] if 0 <= index < len(COCO_CLASSES) else str(index)


def _empty_telemetry() -> dict:
    """The telemetry of a frame that produced nothing, with every field PRESENT.

    A missing key and a zero are different facts: zero is a measurement, a missing
    key is a shape the decoder did not understand. The Go side reads
    `coord_space: "unknown"` and turns the whole answer into `cannot_tell` (T4.2),
    and it can only do that if the key is there.
    """
    return {
        "layout": "unknown",
        "input_w": 0,
        "input_h": 0,
        "fields": 0,
        "classes": 0,
        "has_objectness": None,
        "coord_space": "unknown",
        "decoder_family": "unknown",
        "candidates": 0,
        "pre_nms": 0,
        "post_nms_class": 0,
        "post_nms": 0,
        "dropped_degenerate": 0,
        "dropped_tiny": 0,
        "nms_class_iou": NMS_IOU_CLASS,
        "nms_global_iou": NMS_IOU_GLOBAL,
        "median_box_area": 0.0,
        "median_object_px": 0.0,
        "identical_geometry": 0,
    }


def _looks_like_pixels(cx, cy, input_w: int, input_h: int) -> bool:
    """Whether the coordinates are input pixels or fractions of the input.

    Decided on the RAW values, which is the whole of T-3.3 and D2. A pixel-space
    model puts centres anywhere in 0..input_w; a fraction-space model puts them in
    0..1. The threshold is 1.0 and NOT 2.0: the old value was chosen to be safely
    above the sigmoided range, and it was applied to sigmoided values, which is
    why it never fired. On raw values, anything above 1.0 can only be pixels.

    The two cases are asymmetric on purpose. A fraction-space model with a box
    slightly off-frame can report a centre of 1.02, and treating that as pixels
    would shrink the box by a factor of 640 — so a value must clear the input
    size to count as pixels, not merely 1.0.
    """
    if cx.size == 0 and cy.size == 0:
        return True
    peak = 0.0
    if cx.size:
        peak = max(peak, float(np.max(np.abs(cx))))
    if cy.size:
        peak = max(peak, float(np.max(np.abs(cy))))
    if peak <= 1.0 + 1e-6:
        return False
    # A fraction-space model cannot produce a coordinate anywhere near the input
    # size. If we are seeing one, these are pixels.
    return peak > 1.5 or peak >= min(input_w, input_h) / 2.0


def _count_identical(boxes: list[dict]) -> int:
    """How many emitted boxes SHARE their exact geometry with another box.

    Zero is the answer for a decoder that is working, and it is the number the
    plan names in §4 step 5: "identical_geometry falls from 8 to 0". So the count
    is of the boxes that are duplicates, not of the largest identical group — a
    group of one is not a duplicate, and reporting it as one would put a `1` on
    every healthy frame and teach a reader to ignore the field.

    This is REPORTED, not used as a hard gate. A correct decoder can produce two
    identical boxes by accident on a genuinely ambiguous frame. The Go side
    refuses a frame above 2 as `cannot_tell` (T4.2), because above that the
    answer is about the arithmetic and not about the scene.
    """
    seen: dict[tuple, int] = {}
    for b in boxes:
        key = (b["x1"], b["y1"], b["x2"], b["y2"])
        seen[key] = seen.get(key, 0) + 1
    return sum(n - 1 for n in seen.values() if n > 1)


def _reattach_classes(after_class: list[dict], after_global: list[dict]) -> list[dict]:
    """Give the class-agnostic survivors their class ids back.

    The second pass works on geometry alone, so the returned records carry
    `cls = -1`. The class id is not lost, it is in `after_class`, and it is
    recovered by matching the surviving box: the global pass returns the boxes it
    kept, unaltered, so the coordinates ARE the key.
    """
    if not after_global:
        return []
    by_box = {(round(s["box"][0], 9), round(s["box"][1], 9), round(s["box"][2], 9), round(s["box"][3], 9)): s["cls"]
              for s in after_class}
    out = []
    for s in after_global:
        b = s["box"]
        key = (round(b[0], 9), round(b[1], 9), round(b[2], 9), round(b[3], 9))
        cls = by_box.get(key, -1)
        out.append({"cls": cls, "score": s["score"], "box": b})
    return out


class Handler(BaseHTTPRequestHandler):
    engine: Engine = None  # type: ignore[assignment]
    protocol_version = "HTTP/1.1"
    server_version = "pyworker"

    def log_message(self, fmt: str, *args: Any) -> None:  # noqa: D102
        # Deliberately quiet: the Go side owns the log, and a line per frame at
        # 5 fps would drown the run's own messages.
        pass

    def _send(self, code: int, body: bytes, ctype: str = "application/json") -> None:
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _fail(self, code: int, reason: str) -> None:
        self._send(code, reason.encode("utf-8"), "text/plain; charset=utf-8")

    def do_GET(self) -> None:  # noqa: N802
        if self.path == "/healthz":
            body = {
                "ready": True,
                "model": self.engine.model_name,
                "input_width": self.engine.input_w,
                "input_height": self.engine.input_h,
                "providers": self.engine.providers,
                "export": self.engine.spec.to_json(),
            }
            self._send(200, json.dumps(body).encode("utf-8"))
            return
        if self.path == "/probe":
            body = {
                "model": self.engine.model_name,
                "input_name": self.engine.input_name,
                "input_width": self.engine.input_w,
                "input_height": self.engine.input_h,
                "layout": self.engine.layout,
                "providers": self.engine.providers,
                "export": self.engine.spec.to_json(),
                "declared_fields": self.engine.declared_fields,
                "classes": self.engine.classes,
            }
            self._send(200, json.dumps(body).encode("utf-8"))
            return
        self._fail(404, f"no such path: {self.path}")

    def do_POST(self) -> None:  # noqa: N802
        if self.path != "/infer":
            self._fail(404, f"no such path: {self.path}")
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
        except ValueError:
            self._fail(400, "Content-Length is not a number")
            return
        if length <= 0:
            self._fail(400, "empty body")
            return

        # The frame's shape is in the headers and the pixels are the body. The
        # body is a bare octet stream on purpose: a header line inside the
        # payload would be one more thing to parse on the side that has the
        # least reason to parse anything.
        ctype = (self.headers.get("Content-Type") or "").split(";")[0].strip()
        if ctype != "application/octet-stream":
            self._fail(415, f"Content-Type is {ctype!r}; this endpoint takes application/octet-stream and nothing else, so a caller that sends a different one is refused rather than guessed at")
        try:
            seq = int(self.headers.get("X-Sequence", ""))
            w = int(self.headers.get("X-Frame-Width", ""))
            h = int(self.headers.get("X-Frame-Height", ""))
        except ValueError:
            self._fail(400, "X-Sequence, X-Frame-Width and X-Frame-Height are required and must be integers")
            return
        if w <= 0 or h <= 0:
            self._fail(400, f"bad frame size {w}x{h}")
            return

        want = w * h * 3
        if length != want:
            self._fail(400, f"body is {length} bytes, want {want} for {w}x{h} RGB8. A short body is not a short frame: it is a corrupt one, and it is refused rather than padded")
        pixels = self.rfile.read(length)
        if len(pixels) != want:
            self._fail(400, f"body arrived in {len(pixels)} of {want} bytes for frame {seq}")
            return
        try:
            arr = np.frombuffer(pixels, dtype=np.uint8).reshape(h, w, 3)
            result = self.engine.infer(seq, arr)
        except Exception as exc:
            # A failure here is a non-200, never a 200 with no boxes. The Go
            # side must see `degraded`, not an empty scene.
            self._fail(500, f"inference failed on frame {seq}: {exc}")
            return
        self._send(200, json.dumps(result).encode("utf-8"))


def main() -> int:
    ap = argparse.ArgumentParser(description="AI sidecar: RGB in, boxes out. No video, no state, no clock.")
    ap.add_argument("--host", default="127.0.0.1", help="bind address; loopback only by design")
    ap.add_argument("--port", type=int, default=8099)
    ap.add_argument("--model", default="models/yolov8n.onnx")
    ap.add_argument("--anchors", default=os.environ.get("SP_POC_AI_ANCHORS", ""),
                    help="the darknet anchor boxes for an anchored export, one 'width,height' per line, "
                         "three scales, finest stride first. Required by, and only by, an export that "
                         "publishes one output per stride; the anchors are in the model's .cfg and are "
                         "not in the ONNX file")
    ap.add_argument("--min-box-area", type=float, default=DEFAULT_MIN_BOX_AREA,
                    help="SP_POC_MIN_BOX_AREA: a box smaller than this fraction of the frame is noise, not an object")
    args = ap.parse_args()

    if args.host not in ("127.0.0.1", "localhost", "::1"):
        sys.stderr.write(
            f"pyworker: refusing to bind {args.host}. The sidecar holds video frames with no "
            "authentication; it is a loopback service and nothing else.\n"
        )
        return 2

    Handler.engine = Engine(args.model, min_box_area=args.min_box_area, anchors_path=args.anchors)
    sys.stderr.write(
        f"pyworker: model {Handler.engine.model_name} "
        f"input={Handler.engine.input_w}x{Handler.engine.input_h} "
        f"layout={Handler.engine.layout} "
        f"family={Handler.engine.spec.decoder_family} "
        f"classes={len(Handler.engine.classes)} "
        f"providers={','.join(Handler.engine.providers)} "
        f"listening on http://{args.host}:{args.port}\n"
    )
    sys.stderr.flush()
    ThreadingHTTPServer((args.host, args.port), Handler).serve_forever()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
