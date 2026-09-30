"""Tests for the sidecar.

These run without a model. A model is a runtime dependency, not a test
dependency: what is being tested here is the wire format, the refusal to
manufacture an empty answer, and the claim that this process needs no image
library. The model's own accuracy is explicitly NOT what this POC measures
(plan §12), so a test that asserted accuracy would be asserting something the
report is forbidden to claim.
"""

from __future__ import annotations

import json
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import numpy as np  # noqa: E402

import server  # noqa: E402

PORT = 0


class StubEngine:
    """Stands in for a loaded model.

    The real decoding methods are borrowed from Engine. They decide where a box
    lands on a picture, and testing a copy of them would test nothing: a decoder
    that passed its own tests while the real one put boxes on the wrong objects
    is precisely the failure this exists to prevent.

    The methods borrowed are the ones that EXIST. `_to_normalised` and
    `emits_pixels` were deleted: the first divided by the input size instead of
    the letterboxed content (D4) and the second measured ALREADY-SIGMOIDED values,
    so it always answered "fractions" and the pixel-to-frame division never ran
    (D2). A test that borrowed them was a test of a bug.
    """

    model_name = "fake.onnx"
    input_w = 64
    input_h = 64
    layout = "nchw"
    providers = ["CPUExecutionProvider"]
    classes: dict = {}
    min_box_area = server.DEFAULT_MIN_BOX_AREA
    anchors = None
    anchors_path = ""
    output_names = ["out"]
    declared_fields = 0

    decode = server.Engine.decode
    decode_anchored = server.Engine.decode_anchored
    _finalise = server.Engine._finalise
    class_name = server.Engine.class_name
    rows = staticmethod(server.Engine.rows)

    def __init__(self, boxes=None):
        self.boxes = boxes or []
        self.calls = 0
        self.spec = server.ExportSpec("nchw", self.input_w, self.input_h)

    def infer(self, seq, rgb):
        self.calls += 1
        return {
            "seq": seq,
            "boxes": list(self.boxes),
            "width": int(rgb.shape[1]),
            "height": int(rgb.shape[0]),
            "model": self.model_name,
            "infer_ms": 1.0,
            "telemetry": server._empty_telemetry(),
        }


def raw_post(payload, headers):
    import http.client

    conn = http.client.HTTPConnection("127.0.0.1", PORT, timeout=10)
    conn.request("POST", "/infer", body=payload, headers=headers)
    resp = conn.getresponse()
    data = resp.read()
    conn.close()
    return resp.status, data


def post_frame(w, h, pixels=None, seq=1, ctype="application/octet-stream"):
    """The wire format: a bare octet stream, the shape in the headers."""
    body = b"\x00" * (w * h * 3) if pixels is None else pixels
    headers = {"Content-Type": ctype}
    if seq is not None:
        headers["X-Sequence"] = str(seq)
    if w is not None:
        headers["X-Frame-Width"] = str(w)
    if h is not None:
        headers["X-Frame-Height"] = str(h)
    return raw_post(body, headers)


class DesignTests(unittest.TestCase):
    def test_no_image_or_capture_library_is_imported(self):
        """The architecture depends on this, so it is a test and not a comment."""
        for mod in ("cv2", "PIL", "imageio", "av", "decord"):
            self.assertNotIn(mod, sys.modules, f"{mod} is imported; the sidecar must not use one")

    def test_letterbox_preserves_aspect_ratio_and_returns_chw(self):
        rgb = np.zeros((100, 200, 3), dtype=np.uint8)
        out, nw, nh, _, _ = server.letterbox(rgb, 64, 64, "nchw")
        self.assertEqual(out.shape, (3, 64, 64))
        self.assertEqual(out.dtype, np.float32)
        self.assertLessEqual(float(out.max()), 1.0)
        # Padding is zero and the content sits in the top-left block, because
        # the scale is width-limited for a 200x100 frame in a 64x64 box.
        self.assertTrue(np.allclose(out[:, 32:, :], 0.0))
        # And the CONTENT size is reported, because that is what the un-padding
        # divides by and dividing by the input size is D4.
        self.assertEqual(nw, 64)
        self.assertEqual(nh, 32)

    def test_letterbox_normalises_to_zero_one(self):
        rgb = np.full((32, 32, 3), 255, dtype=np.uint8)
        out, _, _, _, _ = server.letterbox(rgb, 16, 16, "nchw")
        self.assertAlmostEqual(float(out.max()), 1.0, places=5)

    def test_letterbox_honours_the_models_own_layout(self):
        # The first model this POC ran against wanted NHWC. Feeding it a NCHW
        # tensor produces confident nonsense, and confident nonsense produces
        # line crossings on objects that are not there.
        rgb = np.zeros((100, 200, 3), dtype=np.uint8)
        nchw, _, _, _, _ = server.letterbox(rgb, 64, 64, "nchw")
        nhwc, _, _, _, _ = server.letterbox(rgb, 64, 64, "nhwc")
        self.assertEqual(nchw.shape, (3, 64, 64))
        self.assertEqual(nhwc.shape, (64, 64, 3))
        self.assertTrue(np.array_equal(np.transpose(nhwc, (2, 0, 1)), nchw))

    def test_the_layout_is_detected_from_the_shape_and_never_guessed(self):
        self.assertEqual(server.detect_layout([1, 3, 640, 640]), "nchw")
        self.assertEqual(server.detect_layout([1, 640, 640, 3]), "nhwc")
        self.assertEqual(server.detect_layout([1, 1, 416, 416]), "nchw")
        self.assertEqual(server.detect_layout([1, 416, 416, 1]), "nhwc")
        # Anything else is refused: a wrong guess puts boxes on the wrong pixels.
        for bad in ([1, 2, 640, 640], [1, 640, 640], [640, 640, 3], [1, 5, 640, 640]):
            with self.assertRaises(ValueError, msg=f"{bad} was accepted"):
                server.detect_layout(bad)

    def test_the_refused_bind_is_loopback(self):
        # A sidecar holds video frames with no authentication. Binding it
        # anywhere but loopback would put them on the network.
        import io
        import contextlib

        err = io.StringIO()
        argv = sys.argv
        sys.argv = ["server.py", "--host", "0.0.0.0", "--model", "x.onnx"]
        try:
            with contextlib.redirect_stderr(err):
                code = server.main()
        finally:
            sys.argv = argv
        self.assertEqual(code, 2)
        self.assertIn("refusing to bind", err.getvalue())


class NmsTests(unittest.TestCase):
    def test_overlapping_boxes_of_the_same_class_collapse(self):
        boxes = np.array([[0.0, 0.0, 0.5, 0.5], [0.01, 0.01, 0.51, 0.51], [0.8, 0.8, 0.9, 0.9]])
        scores = np.array([0.9, 0.8, 0.7])
        classes = np.array([0, 0, 0])
        kept = server.nms(boxes, scores, classes, 0.45)
        self.assertEqual(len(kept), 2)
        self.assertAlmostEqual(kept[0]["score"], 0.9)

    def test_different_classes_never_suppress_each_other(self):
        boxes = np.array([[0.0, 0.0, 0.5, 0.5], [0.01, 0.01, 0.51, 0.51]])
        scores = np.array([0.9, 0.8])
        classes = np.array([0, 1])
        self.assertEqual(len(server.nms(boxes, scores, classes, 0.45)), 2)

    def test_iou_of_disjoint_boxes_is_zero(self):
        self.assertEqual(server.iou([0.0, 0.0, 0.1, 0.1], [0.5, 0.5, 0.6, 0.6]), 0.0)
        self.assertAlmostEqual(server.iou([0.0, 0.0, 1.0, 1.0], [0.0, 0.0, 1.0, 1.0]), 1.0)

if __name__ == "__main__":
    unittest.main()
