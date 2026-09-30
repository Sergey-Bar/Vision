"""The decoder, tested against the shape and value range READ OFF THE MODEL.

The ONNX model zoo's yolov4 exports rank FIVE — `(1, 52, 52, 3, 85)` for a 416
input — and the values are LOGITS in roughly -6..+6.

The first version of the decoder chose its path on the RANK, matching 3 or 4. Rank
5 matched neither, so it returned nothing for that model, and every run reported
zero detections: on the committed synthetic material AND on a real traffic camera
at night. Both of those zero readings were this bug and not the material.

These tests use the real shape. A test written against a shape I assumed is a test
of my assumption.
"""

from __future__ import annotations

import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import numpy as np  # noqa: E402

import server  # noqa: E402


class Engine(server.Engine):
    """The real decoding methods on an engine that never loads a session.

    The methods under test are Engine's own, borrowed unchanged. A test of a copy
    is a test of the copy.
    """

    model_name = "stub.onnx"
    input_w = 416
    input_h = 416
    providers = ["stub"]
    classes: dict = {}

    def __init__(self, classes=None):
        self.classes = classes or {}


def logit(p: float) -> float:
    """The logit whose sigmoid is p, so a test can write a box in FRACTIONS and
    have it mean the same thing the model means."""
    p = min(max(p, 1e-6), 1 - 1e-6)
    import math
    return math.log(p / (1 - p))


def zoo(cx=0.5, cy=0.5, bw=0.10, bh=0.10, obj=0.99, cls=0.99, object_index=2):
    """One confident object in the zoo's REAL output shape, with the REAL units.

    Measured off the model: columns 0-3 run -2.2..+2.4 and columns 4-84 run
    0.00..0.99. So the four box fields are LOGITS and everything after them is
    already a probability. A test that writes pixels into columns 0-3 is testing a
    layout the model does not have.
    """
    raw = np.zeros((1, 52, 52, 3, 85), np.float32)
    raw[..., 4:] = 0.001
    raw[0, 26, 26, 0, 0:4] = [logit(cx), logit(cy), logit(bw), logit(bh)]
    raw[0, 26, 26, 0, 4] = obj
    raw[0, 26, 26, 0, 5 + object_index] = cls
    return raw


class RealShapeTests(unittest.TestCase):
    def setUp(self):
        self.eng = Engine()

    def test_the_real_zoo_shape_decodes(self):
        boxes, _, _ = self.eng.decode(zoo(), 640, 360)
        self.assertTrue(boxes, "the real zoo shape produced no boxes at all")
        top = boxes[0]
        self.assertEqual(top["cls"], "car")
        self.assertGreater(top["score"], 0.9, "logits were not passed through a sigmoid")
        # 208 of 416 is the centre; 40 of 416 is the width.
        self.assertAlmostEqual(top["x1"], 0.45, places=2)
        self.assertAlmostEqual(top["y1"], 0.45, places=1)
        self.assertAlmostEqual(top["x2"], 0.55, places=2)
        self.assertAlmostEqual(top["y2"], 0.55, places=1)
        self.assertAlmostEqual(top["x2"] - top["x1"], 0.10, places=2, msg="the box width is wrong" "the box has no width: the frame scaling is wrong")
        self.assertGreater(top["y2"] - top["y1"], 0.05, "the box has no height")

    def test_a_probability_model_is_not_squashed_toward_half(self):
        """A model that already emits probabilities must be left alone. A sigmoid
        on a 0.9 would turn it into 0.71 and every detection would look
        mediocre for a reason that is arithmetic and not the scene."""
        raw = np.zeros((1, 84, 4), np.float32)
        raw[0, 0:4, 0] = [0.5, 0.5, 0.2, 0.2]
        raw[0, 4, 0] = 0.9
        raw[0, 5 + 2, 0] = 0.95
        boxes, _, _ = self.eng.decode(raw, 640, 360)
        self.assertTrue(boxes, "a probability-space model produced nothing")
        self.assertGreater(boxes[0]["score"], 0.8, "a probability was pushed through a sigmoid")

    def test_logits_and_probabilities_are_told_apart(self):
        self.assertTrue(server._is_probability(np.array([0.1, 0.9], np.float32)))
        self.assertFalse(server._is_probability(np.array([-5.0, 5.0], np.float32)))

    def test_no_floor_sends_thousands_of_candidates(self):
        """The model emits all ~8,112 anchors with some non-zero score, because a
        sigmoid of a logit is never exactly zero. Without a floor the sidecar
        answers with a megabyte of noise that looks like a detection rate.

        The fixture is built here rather than shared: a shared builder that is
        out of step with the decoder is exactly how a test ends up asserting
        something the code does not do.
        """
        raw = np.zeros((1, 52, 52, 3, 85), np.float32)
        raw[..., 4:] = 0.02  # every class, including the one the argmax will pick
        raw[0, 26, 26, 0, 4] = 0.02  # objectness
        # 0.02 x 0.02 = 4e-4, three orders of magnitude below the floor.
        boxes, _, _ = self.eng.decode(raw, 640, 360)
        self.assertEqual(boxes, [], "low-confidence candidates were sent")

    def test_at_most_one_hundred_boxes_are_sent(self):
        """The sidecar is not the decision, but it is also not a fire hose. The
        run's SP_POC_SCORE_THRESHOLD and approved class list are the decision."""
        raw = np.full((1, 52, 52, 3, 85), -6.0, np.float32)
        for y in range(0, 52, 4):
            for x in range(0, 52, 4):
                raw[0, y, x, 0, 0:4] = [float(x * 8), float(y * 8), 20.0, 20.0]
                raw[0, y, x, 0, 4] = 5.0
                raw[0, y, x, 0, 5 + 2] = 5.0
        boxes, _, _ = self.eng.decode(raw, 640, 360)
        self.assertLessEqual(len(boxes), server.MAX_EMITTED_BOXES)

    def test_an_unrecognised_shape_yields_nothing_rather_than_a_guess(self):
        self.assertEqual(self.eng.decode(np.zeros((1,), np.float32), 640, 360)[0], [])
        # Two fields is not a detector's output.
        self.assertEqual(self.eng.decode(np.zeros((1, 100, 2), np.float32), 640, 360)[0], [])

    def test_every_box_is_inside_the_frame(self):
        """A box that runs off the edge is CLAMPED, not discarded: the object is
        partly in frame, and dropping it would make it invisible to the
        line-crossing machine — a missed crossing presented as nothing there."""
        boxes, _, _ = self.eng.decode(zoo(cx=2.0, cy=2.0, bw=400.0, bh=400.0), 640, 360)
        self.assertTrue(boxes, "a box partly in frame was discarded instead of clamped")
        for b in boxes:
            for k in ("x1", "y1", "x2", "y2"):
                self.assertGreaterEqual(b[k], 0.0, f"{k} below the frame: {b}")
                self.assertLessEqual(b[k], 1.0, f"{k} above the frame: {b}")

    def test_the_layout_is_decided_on_the_last_axis_not_the_rank(self):
        """Every rank the decoder must survive: the zoo's 5, YOLOv8's 3, and a
        flattened 2."""
        for shape in [(1, 52, 52, 3, 85), (1, 84, 5000), (1, 5000, 84), (1, 5000 * 84,)]:
            raw = np.full(shape, -6.0, np.float32)
            flat = raw.reshape(-1, raw.shape[-1])
            flat[0, 0:4] = [0.5, 0.5, 0.2, 0.2]
            flat[0, 4] = 6.0
            flat[0, 7] = 6.0
            boxes, _, _ = self.eng.decode(raw, 640, 360)
            self.assertTrue(boxes, f"shape {shape} produced nothing")


if __name__ == "__main__":
    unittest.main()
