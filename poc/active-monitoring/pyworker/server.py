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
   "width":640,"height":384,"model":"yolov8n","infer_ms":12.5}

  x/y are normalised to [0,1] against the ORIGINAL frame, so a model input size
  of 640x640 does not change what a box means.

  Anything this process cannot do is a non-200 with a plain-text reason. It
  never answers 200 with an empty box list to mean "something went wrong": the
  Go side treats an empty list as a real negative observation, and this process
  is not allowed to manufacture one.

GET /healthz -> {"ready":bool,"model":str,"input_width":int,"input_height":int,"providers":[...]}

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
MIN_EMITTED_SCORE = 0.05
MAX_EMITTED_BOXES = 100
NMS_IOU = 0.45

# The COCO 80-class vocabulary, for the model zoo's yolov4 which embeds no names
# of its own. Without it every box arrives as the string "9".
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


def _sigmoid(x: np.ndarray) -> np.ndarray:
    """A numerically stable logistic, because a logit of -6 or +6 overflows
    exp() and the whole array becomes NaN, and a NaN is not a low score."""
    return 1.0 / (1.0 + np.exp(-np.clip(x, -60.0, 60.0)))


def _is_probability(a: np.ndarray) -> bool:
    """Whether a tensor already holds probabilities rather than logits.

    Decided on the RANGE, not on the model: a model that emits probabilities is
    left alone, because a sigmoid on a probability would squash every detection
    toward 0.5 and turn a good 0.9 into a useless 0.71.
    """
    if a.size == 0:
        return True
    lo = float(np.min(a))
    hi = float(np.max(a))
    return lo >= 0.0 and hi <= 1.0


def nms(boxes: np.ndarray, scores: np.ndarray, classes: np.ndarray, iou_thr: float) -> list[dict]:
    """Plain greedy NMS, per class. numpy only, and it is the only filtering here."""
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


def letterbox(rgb: np.ndarray, size: int, layout: str = "nchw") -> np.ndarray:
    """Resize preserving aspect ratio, pad the rest, and put the result in the
    model's own layout.

    The layout is a PROPERTY OF THE MODEL, not a convention: ONNX exports both
    `NCHW` (what Ultralytics writes) and `NHWC` (what the ONNX model zoo
    writes, and what the first model this POC ran against actually wants).
    Feeding a NCHW tensor to an NHWC input produces confident nonsense, and
    confident nonsense is worse than nothing: it produces line crossings on
    objects that are not there.

    Nearest-neighbour resampling keeps the dependency list at numpy. On a 5 fps
    paced stream the cost is irrelevant, and the model's own accuracy on this
    material is not what this POC measures.
    """
    h, w, _ = rgb.shape
    scale = min(size / w, size / h)
    nw, nh = max(1, int(round(w * scale))), max(1, int(round(h * scale)))
    yi = (np.arange(nh) / scale).astype(np.int32).clip(0, h - 1)
    xi = (np.arange(nw) / scale).astype(np.int32).clip(0, w - 1)
    small = rgb[yi][:, xi]
    out = np.zeros((size, size, 3), dtype=np.uint8)
    out[:nh, :nw] = small
    if layout == "nhwc":
        return out.astype(np.float32) / 255.0
    return np.transpose(out, (2, 0, 1)).astype(np.float32) / 255.0


def detect_layout(shape) -> str:
    """Decide NCHW or NHWC from the declared input shape.

    The channel count is small and the spatial dimensions are large, so the
    position of the small one is the answer. The first model this POC ran
    against was NHWC, and a code path that only knew NCHW would have produced
    confident nonsense from it.
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


class Engine:
    """The model, its input shape, its class names. Nothing else is kept."""

    def __init__(self, model_path: str) -> None:
        self.session, (self.providers, ort) = load_onnx(model_path)
        self.model_name = os.path.basename(model_path)
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
        if labels:
            self.classes = {int(k): v for k, v in (kv.split(":", 1) for kv in labels.split(","))}
        else:
            self.classes = {}

    def infer(self, seq: int, rgb: np.ndarray) -> dict:
        started = time.perf_counter()
        h, w, _ = rgb.shape
        blob = letterbox(rgb, self.input_h, self.layout)[None, ...]
        outputs = self.session.run(None, {self.input_name: blob})
        raw = np.asarray(outputs[0])
        boxes, scores, classes = self.decode(raw, w, h)
        out = {
            "seq": seq,
            "boxes": boxes,
            "width": w,
            "height": h,
            "model": self.model_name,
            "infer_ms": (time.perf_counter() - started) * 1000.0,
        }
        return out

    def decode(self, raw: np.ndarray, w: int, h: int) -> tuple[list[dict], np.ndarray, np.ndarray]:
        """Decode whichever layout this model exports.

        The decision is made on the LAST axis, never on the rank and never on the
        file name. Both of those were wrong and both cost every detection the
        sidecar ever reported:

          - The ONNX model zoo's yolov4 exports rank FIVE:
            (1, 52, 52, 3, 85) for a 416 input. A rank check for 3 or 4 does
            not match 5, so the first version of this function returned no boxes
            for that model and every run reported zero detections — on synthetic
            material and on a real traffic camera alike. The zero was ours, not
            the material's.
          - Ultralytics' YOLOv8 exports rank three: (1, 4+nc, N).

        In BOTH the fields are the last axis. That is the one thing they share, so
        it is the thing to test: at least 6 fields (4 box + 1 confidence + 1
        class) on the last axis means "fields last", and everything else is
        flattened away before the arithmetic.
        """
        arr = np.asarray(raw)
        if arr.ndim < 2:
            return [], np.zeros(0), np.zeros(0)
        # Drop a leading batch axis if there is one.
        while arr.ndim > 2 and arr.shape[0] == 1:
            arr = arr[0]
        if arr.shape[-1] < 6:
            # Fields on axis 0 instead: (4+nc, N). Turn it around.
            arr = np.transpose(arr, (1, 0))
        if arr.shape[-1] < 6:
            return [], np.zeros(0), np.zeros(0)
        flat = arr.reshape(-1, arr.shape[-1])
        if flat.shape[0] == 0:
            return [], np.zeros(0), np.zeros(0)

        box_fields = flat[:, 0:4]
        obj, class_scores = flat[:, 4], flat[:, 5:]
        if class_scores.shape[1] == 0:
            return [], np.zeros(0), np.zeros(0)

        # Measured, not assumed, per group.
        #
        # This model's output splits cleanly in two: columns 0-3 run -2.2..+2.4
        # and columns 4-84 run 0.00..0.99. The first four are LOGITS and the rest
        # are already probabilities. A single rule for all of them gets it wrong
        # one way or the other: thresholding logits at 0.0 is meaningless, and a
        # sigmoid on a probability squashes a good 0.9 into a useless 0.71.
        #
        # The two groups are therefore checked SEPARATELY, by range, because the
        # export is what decides this and not any convention.
        if not _is_probability(box_fields):
            box_fields = _sigmoid(box_fields)
        if not _is_probability(obj):
            obj = _sigmoid(obj)
        if not _is_probability(class_scores):
            class_scores = _sigmoid(class_scores)
        cx, cy, bw, bh = box_fields[:, 0], box_fields[:, 1], box_fields[:, 2], box_fields[:, 3]

        best = class_scores.max(axis=1) * obj
        which = class_scores.argmax(axis=1)
        # A FLOOR, and it is not optional.
        #
        # The model emits every one of its ~10,847 candidate anchors with some
        # non-zero score, because a sigmoid of a logit is never exactly zero.
        # Without a floor, NMS keeps thousands of them and the sidecar answers
        # with a megabyte of noise that looks like a detection rate.
        #
        # The floor here is deliberately LOW: the sidecar is not the decision.
        # It is the detector; `SP_POC_SCORE_THRESHOLD` and the approved class
        # list are the decision, and they live in the run's configuration where a
        # reader can see them. What the sidecar must do is refuse to send noise.
        keep = best >= MIN_EMITTED_SCORE
        if not keep.any():
            return [], np.zeros(0), np.zeros(0)
        cx, cy, bw, bh = cx[keep], cy[keep], bw[keep], bh[keep]
        best, which = best[keep], which[keep]

        scaled = self._to_normalised(cx, cy, bw, bh, w, h)
        # The coordinate scale is a property of the MODEL, not of this function,
        # so it is measured rather than assumed: a model that emits pixels and a
        # model that emits fractions of the input differ by a factor of 416, and
        # guessing wrong puts every box on the wrong object.
        selected = nms(scaled.tolist(), best, which, NMS_IOU)
        selected = selected[:MAX_EMITTED_BOXES]
        out = []
        skipped_degenerate = 0
        for s in selected:
            bx1, by1, bx2, by2 = s["box"]
            # A box with no area is NOT a detection, and emitting it is how a
            # broken coordinate scale turns into thousands of "detections" that
            # are really one bug repeated.
            #
            # It is dropped rather than clamped, and the count is returned to the
            # sidecar's log, because a scale question that is still open should
            # be VISIBLE. Clamping would hide it behind boxes pinned to the frame
            # corner, which is what the first version of this did.
            if bx2 - bx1 <= 1e-6 or by2 - by1 <= 1e-6:
                skipped_degenerate += 1
                continue
            out.append(
                {
                    "cls": self.class_name(int(s["cls"])),
                    "score": round(float(s["score"]), 6),
                    # Clamped, not discarded: a box that runs off the frame has
                    # still found something that is partly in it.
                    "x1": round(min(max(bx1, 0.0), 1.0), 6),
                    "y1": round(min(max(by1, 0.0), 1.0), 6),
                    "x2": round(min(max(bx2, 0.0), 1.0), 6),
                    "y2": round(min(max(by2, 0.0), 1.0), 6),
                }
            )
        if skipped_degenerate:
            print(
                f"[pyworker] {skipped_degenerate} candidate(s) had no area and were NOT sent. "
                f"That is one bug repeated, not {skipped_degenerate} detections. "
                f"If this number is large the model's box scale is not being read correctly.",
                file=sys.stderr, flush=True,
            )
        out.sort(key=lambda b: -b["score"])
        return out, best, which

    def _to_normalised(self, cx, cy, bw, bh, w: int, h: int) -> np.ndarray:
        """Boxes -> (x1,y1,x2,y2) normalised to the ORIGINAL frame.

        Two steps, and the first version did the second one twice, which collapsed
        every box onto the left edge of the frame and made the detector look like
        it saw only corners:

          1. the model's own units -> fractions of its input, when the model emits
             PIXELS. A model that already emits fractions is left alone.
          2. fractions of the input -> fractions of the frame.

        Step 1 is decided by RANGE, because the units are a property of the model
        and not of this function: a pixel-space model puts box centres anywhere
        in 0..416, a fraction-space model in 0..1, and guessing wrong is a factor
        of 416 on every box.
        """
        cx, cy, bw, bh = np.asarray(cx, float), np.asarray(cy, float), np.asarray(bw, float), np.asarray(bh, float)
        if self.emits_pixels(cx, bw):
            cx, cy = cx / self.input_w, cy / self.input_h
            bw, bh = bw / self.input_w, bh / self.input_h
        # Fractions of the frame the caller gave us.
        x1 = cx - bw / 2
        y1 = cy - bh / 2
        x2 = cx + bw / 2
        y2 = cy + bh / 2
        return np.stack([x1, y1, x2, y2], axis=1)

    def emits_pixels(self, cx, bw) -> bool:
        """Whether a model's box centres are in input pixels or in fractions.

        Decided by RANGE. A pixel-space model puts centres anywhere in 0..416; a
        fraction-space model puts them in 0..1. Anything above 2 can only be
        pixels, and anything below can only be a fraction.
        """
        if cx.size == 0:
            return True
        return float(np.max(np.abs(cx))) > 2.0

    def class_name(self, index: int) -> str:
        """The class's NAME, from the model's own metadata when it has it and
        from the COCO vocabulary when it does not.

        The model zoo's yolov4 embeds no names, so without this every box arrives
        as the string "9" and the report's class column is a column of integers.
        """
        if self.classes:
            return self.classes.get(index, str(index))
        return COCO_CLASSES[index] if 0 <= index < len(COCO_CLASSES) else str(index)

    def _emit(self, cx, cy, bw, bh, best, which, w, h) -> tuple[list[dict], np.ndarray, np.ndarray]:
        """Input-pixel boxes -> normalised, NMS, clamp into the frame, wire shape."""
        x1 = (cx - bw / 2) / self.input_w * w
        y1 = (cy - bh / 2) / self.input_h * h
        x2 = (cx + bw / 2) / self.input_w * w
        y2 = (cy + bh / 2) / self.input_h * h
        scaled = np.stack([x1 / w, y1 / h, x2 / w, y2 / h], axis=1)
        selected = nms(scaled.tolist(), best, which, 0.45)
        out = []
        for s in selected:
            bx1, by1, bx2, by2 = s["box"]
            # A box is CLAMPED, not discarded. A model that puts a corner
            # outside the frame has still found an object that is partly in it,
            # and dropping it would make the object invisible to the
            # line-crossing machine - a missed crossing presented as "nothing
            # was there".
            out.append(
                {
                    "cls": self.classes.get(int(s["cls"]), str(int(s["cls"]))),
                    "score": round(float(s["score"]), 6),
                    "x1": round(min(max(bx1, 0.0), 1.0), 6),
                    "y1": round(min(max(by1, 0.0), 1.0), 6),
                    "x2": round(min(max(bx2, 0.0), 1.0), 6),
                    "y2": round(min(max(by2, 0.0), 1.0), 6),
                }
            )
        out.sort(key=lambda b: -b["score"])
        return out, best, which

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
        if self.path != "/healthz":
            self._fail(404, f"no such path: {self.path}")
            return
        body = {
            "ready": True,
            "model": self.engine.model_name,
            "input_width": self.engine.input_w,
            "input_height": self.engine.input_h,
            "providers": self.engine.providers,
        }
        self._send(200, json.dumps(body).encode("utf-8"))

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
    args = ap.parse_args()

    if args.host not in ("127.0.0.1", "localhost", "::1"):
        sys.stderr.write(
            f"pyworker: refusing to bind {args.host}. The sidecar holds video frames with no "
            "authentication; it is a loopback service and nothing else.\n"
        )
        return 2

    Handler.engine = Engine(args.model)
    sys.stderr.write(
        f"pyworker: model {Handler.engine.model_name} "
        f"input={Handler.engine.input_w}x{Handler.engine.input_h} "
        f"layout={Handler.engine.layout} "
        f"providers={','.join(Handler.engine.providers)} "
        f"listening on http://{args.host}:{args.port}\n"
    )
    sys.stderr.flush()
    ThreadingHTTPServer((args.host, args.port), Handler).serve_forever()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
