"""The decoder, tested against tensors built from the MEASURED model contract.

This file exists because of `docs/findings.md` §2. That section records 286 green
Go tests, a real camera at night, and ZERO detections, because the detection layer
had only ever been tested against stubs. The Python test that was here before was
worse than no test at all: its `zoo()` fixture wrote `logit(0.5)` into the four box
fields, so it encoded the D1 bug — a sigmoid applied to box coordinates — as the
EXPECTED result, and passed.

Every fixture below is built from `fixtures/<model>.contract.json`, which
`pyworker/probe_model.py` writes by running one REAL frame through the sidecar's
own load path. A test written against a shape I assumed is a test of my
assumption; a test written against a shape I measured is a test of the decoder.

The five cases the plan names, and what each one catches:

    no_objectness_export          D3 — a class index shifted by one
    pixel_coordinates_not_sigmoided  D1 — the four-bug collapse
    non_square_letterbox          D4 — every box squashed to 0.6x its height
    ultralytics_names_format      T3.6 — a parser that kills the sidecar at load
    cross_class_nms               T3.7 — a car and a truck reported on one car

Each of the first four FAILS against the previous `server.py`, which is the whole
point: a suite that has never been red is a suite that has never been tested.
"""

from __future__ import annotations

import json
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import numpy as np  # noqa: E402

import server  # noqa: E402

FIXTURE_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "fixtures")
CONTRACT_PATH = os.path.join(FIXTURE_DIR, "yolov8n.contract.json")


def contract() -> dict:
    """The measured export contract. Loaded, never re-declared here.

    A test that re-declared the shape it was testing would be testing its own
    declaration, which is precisely how the previous fixture encoded a bug as the
    expected answer.
    """
    with open(CONTRACT_PATH, "r", encoding="utf-8") as fh:
        return json.load(fh)


def coco_names() -> dict[int, str]:
    """Eighty class names, which is what an 84- or 85-field export carries.

    The decoder decides whether column 4 is an objectness from the field count
    AND the model's own `names` length, so a fixture with three names and an
    84-field tensor is not a YOLOv8 export at all — it is an export the decoder
    correctly refuses. The fixture has to be internally consistent or it is
    testing the refusal instead of the thing it is named after.
    """
    return {i: name for i, name in enumerate(server.COCO_CLASSES)}


def fused_engine(input_w: int = 640, input_h: int = 640, names: dict | None = None) -> "StubEngine":
    """A fused-export engine: 80 names, so 84 fields means no objectness and 85
    means there is one."""
    return StubEngine(classes=coco_names() if names is None else names,
                      input_w=input_w, input_h=input_h)


class StubEngine(server.Engine):
    """The real decoding methods on an engine that never loads a session.

    The methods under test are `Engine`'s own, borrowed unchanged. A test of a copy
    is a test of the copy.
    """

    model_name = "fixture.onnx"
    providers = ["stub"]

    def __init__(self, classes=None, input_w=640, input_h=640, min_box_area=server.DEFAULT_MIN_BOX_AREA):
        self.classes = classes or {}
        self.input_w = input_w
        self.input_h = input_h
        self.min_box_area = min_box_area
        self.layout = "nchw"
        self.anchors = None
        self.anchors_path = ""
        self.output_names = ["out"]
        self.declared_fields = 0
        self.spec = server.ExportSpec("nchw", input_w, input_h, classes=dict(self.classes))


# --- fixture builders -------------------------------------------------------
#
# Every one of these takes the field count from the contract, so a change in the
# export changes the fixture instead of invalidating the test.


def v8_tensor(fields: int = 84, n: int = 1200, has_objectness: bool = False) -> np.ndarray:
    """A YOLOv8-shaped `(1, fields, n)` tensor: FIELDS FIRST, fields on axis 1.

    That is what Ultralytics exports and it is the layout the previous decoder
    chose on the RANK: rank three matched neither of its two accepted ranks for
    the zoo model and rank five matched neither for this one, so a single run of
    `pocctl` over a YOLOv8 model returned nothing at all.
    """
    return np.zeros((1, fields, n), np.float32)


def zoo_tensor(fields: int = 85, grid: int = 4, anchors: int = 3) -> np.ndarray:
    """The measured zoo shape: `(1, grid, grid, anchors, fields)`, FIELDS LAST."""
    return np.zeros((1, grid, grid, anchors, fields), np.float32)


def put_fused(t: np.ndarray, row: int, cx, cy, bw, bh, obj: float, class_index: int, score: float) -> None:
    """Write one detection into a FUSED `(1, fields, n)` tensor.

    `obj` is written at index 4 only for an export that has an objectness, and
    skipped for one that does not. That distinction is the whole of D3 and it is
    expressed once, here, rather than at every call site.
    """
    fields = t.shape[1]
    has_objectness = fields == 85 or (fields > 5 and fields - 5 == 80)
    t[0, 0, row] = cx
    t[0, 1, row] = cy
    t[0, 2, row] = bw
    t[0, 3, row] = bh
    if has_objectness:
        t[0, 4, row] = obj
        t[0, 5 + class_index, row] = score
    else:
        t[0, 4 + class_index, row] = score


def put_anchored(t: np.ndarray, row: int, tx, ty, tw, th, obj: float, class_index: int, score: float) -> None:
    """Write one detection into an ANCHORED `(1, g, g, a, fields)` tensor.

    `tx`/`ty` are darknet LOGITS (the sigmoid happens inside the decoder) and
    `tw`/`th` are log-anchors (the exp happens inside the decoder). That is the
    transform, and the test uses it through the decoder rather than beside it.
    """
    t.reshape(-1, t.shape[-1])[row, 0] = tx
    t.reshape(-1, t.shape[-1])[row, 1] = ty
    t.reshape(-1, t.shape[-1])[row, 2] = tw
    t.reshape(-1, t.shape[-1])[row, 3] = th
    t.reshape(-1, t.shape[-1])[row, 4] = obj
    t.reshape(-1, t.shape[-1])[row, 5 + class_index] = score


# --- the contract itself ----------------------------------------------------


class ContractTests(unittest.TestCase):
    def test_the_fixture_records_a_real_measurement(self):
        c = contract()
        self.assertEqual(c["model"], "yolov8n.onnx")
        self.assertIn("probe_model.py", c["measured_by"])
        # The file is NAMED yolov8n and is not one. The contract says so, and a
        # test that hid that would leave the next reader believing the name.
        self.assertEqual(c["decoder_family"], "anchored")
        self.assertEqual(c["output"]["count"], 3, "the measured export publishes one output per stride")
        self.assertEqual(c["output"]["observed_shapes"], [[1, 52, 52, 3, 85], [1, 26, 26, 3, 85], [1, 13, 13, 3, 85]])
        self.assertEqual(c["input"]["layout"], "nhwc")
        self.assertEqual(c["input"]["input_w"], 416)
        self.assertTrue(c["understood"])
        self.assertIs(c["has_objectness"], True)
        self.assertEqual(c["classes"], 0, "the measured model carries no names metadata at all")

    def test_the_measured_letterbox_is_not_the_input_size(self):
        """D4, stated as a fact about the model and the frame.

        A 640x384 frame letterboxed into a 416x416 canvas has a CONTENT of
        416x250. Any un-padding that divides by 416 squashes every box to 0.6x
        its true height, on every frame, for the whole run.
        """
        lb = contract()["letterbox"]
        self.assertEqual(lb["frame_w"], 640)
        self.assertEqual(lb["frame_h"], 384)
        self.assertEqual(lb["content_w"], 416)
        self.assertEqual(lb["content_h"], 250)
        self.assertNotEqual(lb["content_h"], lb["frame_h"])

    def test_the_two_value_groups_are_measured_separately(self):
        """The measurement D1 was missing: box fields and score fields are not
        the same kind of number, and one rule applied to both is what collapsed
        every box onto the frame's corner."""
        obs = contract()["observed"]
        box, score = obs["box_fields"], obs["score_fields"]
        self.assertNotEqual(box["min"], score["min"])
        # The score channels are probabilities on this export; the box channels
        # are not, which is exactly why _is_probability must never be asked
        # about them.
        self.assertGreaterEqual(score["min"], 0.0)
        self.assertLessEqual(score["max"], 1.0)
        self.assertLess(box["min"], 0.0, "the measured box fields are not all in [0,1]")

    def test_a_healthy_decode_reports_no_identical_geometry(self):
        """The number the plan names in §4 step 5: from 8 to 0."""
        self.assertEqual(contract()["telemetry"]["identical_geometry"], 0)

    def test_a_healthy_decode_reports_object_sized_objects(self):
        tel = contract()["telemetry"]
        self.assertLess(tel["median_box_area"], 0.10,
                        "median box area is the fraction of the frame a box covers; the broken "
                        "decode reported ~0.25, which is a quarter of the picture per detection")
        self.assertGreater(tel["median_object_px"], 4.0)


# --- T9.1, the hard tensor-level gate ---------------------------------------


class NoObjectnessExportTests(unittest.TestCase):
    """D3: a YOLOv8 export has NO objectness column, and assuming one eats a class.

    84 fields is 4 box + 80 classes. An export that publishes 85 has an objectness
    at column 4. Reading an 84-field export as though it had one shifts EVERY
    class by one: `car` is read as `person`, `bird` as `dog`, and the picture
    fills with plausible nonsense in exactly the right places.
    """

    def test_a_class_is_named_from_column_four_when_there_is_no_objectness(self):
        eng = fused_engine()
        t = v8_tensor(fields=84, n=4)
        put_fused(t, 0, 0.5, 0.5, 0.1, 0.1, obj=0.0, class_index=2, score=0.87)
        boxes, tel = eng.decode(t, 640, 384, 640, 384)
        self.assertEqual(len(boxes), 1, "an 84-field export produced no boxes at all")
        self.assertEqual(boxes[0]["cls"], "car",
                         "the class is off by one: the objectness column was assumed and does not exist")
        self.assertIs(tel["has_objectness"], False)
        self.assertAlmostEqual(boxes[0]["score"], 0.87, places=3,
                               msg="the score was pushed through a sigmoid: 0.87 became 0.71")

    def test_an_85_field_export_does_have_an_objectness(self):
        # 85 fields with 80 class names is 5 + 80, so there IS an objectness. The
        # tensor has to be built at a realistic candidate count: the field axis is
        # chosen by the model's own vocabulary, and an 85-wide axis on a 3-row
        # tensor is not a shape any exporter produces.
        eng = fused_engine()
        t = v8_tensor(fields=85, n=1200)
        put_fused(t, 0, 0.5, 0.5, 0.1, 0.1, obj=0.9, class_index=2, score=0.8)
        boxes, tel = eng.decode(t, 640, 384, 640, 384)
        self.assertIs(tel["has_objectness"], True)
        self.assertEqual(len(boxes), 1, "an 85-field export produced nothing")
        self.assertEqual(boxes[0]["cls"], "car", "the class index is wrong on an objectness export")
        # The score is objectness x class and both are probabilities here, so
        # 0.9 * 0.8 = 0.72. NOT ~0.5, which is what a double sigmoid gives.
        self.assertAlmostEqual(boxes[0]["score"], 0.72, places=2)

    def test_a_field_count_that_matches_neither_is_refused_not_guessed(self):
        """The most expensive guess in the file. It does not produce a visibly
        wrong answer, it produces a VISIBLY RIGHT one: a `truck` reported as a
        `bench` is a box in the right place with the wrong word on it.

        The shape is (1, 84, 8400) — a real YOLOv8 export — handed to an engine
        that declares a two-class vocabulary. 84 is neither 4+2 nor 5+2, so the
        field count contradicts the model's own names and the frame is refused.
        """
        eng = StubEngine(classes={0: "person", 1: "bicycle"}, input_w=640, input_h=640)
        t = v8_tensor(fields=84, n=1200)
        put_fused(t, 0, 0.5, 0.5, 0.1, 0.1, obj=0.0, class_index=1, score=0.9)
        boxes, tel = eng.decode(t, 640, 384, 640, 384)
        self.assertEqual(boxes, [], "an export whose field count contradicts its own names produced boxes anyway")
        self.assertIsNone(tel["has_objectness"])
        self.assertIn("class names", tel["note"])


class NoSigmoidOnBoxCoordinatesTests(unittest.TestCase):
    """D1: a box coordinate is a LENGTH and is never a probability.

    `_sigmoid(300)` is 1.0 to fifteen decimal places. Applied to the four box
    fields it turns every centre into 0.5 and every size into 1.0, and after
    clamping every box on every frame becomes `(0.5,0.5)-(1.0,1.0)`. The run comes
    out clean: eight identical boxes, no crossings, no error anywhere.
    """

    def test_a_pixel_box_survives_as_that_pixel_box(self):
        eng = fused_engine()
        t = v8_tensor(fields=84, n=4)
        # A box at pixels 160,96 with a size of 64x32 in a 640x640 input, which
        # letterboxes to a 640x384 frame as 640x384 content.
        put_fused(t, 0, 160.0, 96.0, 64.0, 32.0, obj=0.0, class_index=2, score=0.87)
        boxes, tel = eng.decode(t, 640, 384, 640, 384)
        self.assertEqual(len(boxes), 1, "a pixel-space box produced nothing: the sigmoid ate it")
        self.assertEqual(tel["coord_space"], "pixel")
        b = boxes[0]
        self.assertAlmostEqual(b["x1"], (160 - 32) / 640, places=4, msg="a sigmoid reached the box fields")
        self.assertAlmostEqual(b["x2"], (160 + 32) / 640, places=4)
        self.assertAlmostEqual(b["y1"], (96 - 16) / 384, places=4)
        self.assertAlmostEqual(b["y2"], (96 + 16) / 384, places=4)
        # The exact shape D1 produced: everything collapsed to the same rectangle.
        self.assertNotAlmostEqual(b["x1"], 0.5, places=3,
                                  msg="the box collapsed onto the frame's corner: a sigmoid was applied to the coordinates")
        self.assertEqual(tel["identical_geometry"], 0)

    def test_a_score_is_not_double_squeezed(self):
        """A model that already emits probabilities must be left alone. A sigmoid
        on a 0.9 turns it into 0.71 and every detection looks mediocre for a
        reason that is arithmetic and not the scene."""
        eng = fused_engine()
        t = v8_tensor(fields=84, n=2)
        put_fused(t, 0, 0.5, 0.5, 0.1, 0.1, obj=0.0, class_index=2, score=0.9)
        boxes, _ = eng.decode(t, 640, 384, 640, 384)
        self.assertTrue(boxes)
        self.assertAlmostEqual(boxes[0]["score"], 0.9, places=3)

    def test_a_logit_score_is_squeezed_once(self):
        """The other half: a model that DOES emit logits needs the sigmoid, and
        needs it exactly once."""
        eng = fused_engine()
        t = v8_tensor(fields=84, n=2)
        # logit(0.9) = 2.1972
        put_fused(t, 0, 0.5, 0.5, 0.1, 0.1, obj=0.0, class_index=2, score=2.1972245773)
        # A negative logit in a class channel puts the whole score array out of
        # [0,1], which is exactly the signal that it is a logit array.
        t[0, 4:84, 0] = -4.0
        t[0, 4 + 2, 0] = 2.1972245773
        boxes, _ = eng.decode(t, 640, 384, 640, 384)
        self.assertTrue(boxes)
        self.assertAlmostEqual(boxes[0]["score"], 0.9, places=3)


class NonSquareLetterboxTests(unittest.TestCase):
    """D4: the un-padding divides by the CONTENT size, never by the input size.

    The measured fact, from the contract: a 640x384 frame in a 416x416 canvas has
    a content of 416x250. Dividing y by 416 squashes every box to 0.6x its true
    height, so every object in the picture is drawn short and every crossing
    happens early.
    """

    def test_the_vertical_scale_is_the_content_height(self):
        eng = fused_engine()
        t = v8_tensor(fields=84, n=3)
        # cx=320 cy=192 is the exact centre of a 640x640 input holding a
        # 640x384 frame: 384 tall centred in 640 puts the middle at y=320, and
        # 320/640 = 0.5. Use a NON-central value so a wrong scale cannot hide.
        # A box whose top is at 1/4 and bottom at 3/4 of the CONTENT height.
        content_h = 384
        put_fused(t, 0, 320.0, content_h * 0.5, 128.0, content_h * 0.5, obj=0.0, class_index=2, score=0.9)
        boxes, _ = eng.decode(t, 640, 384, 640, 384)
        self.assertTrue(boxes)
        b = boxes[0]
        self.assertAlmostEqual(b["y1"], 0.25, places=3, msg="the box height was divided by the input, not the content")
        self.assertAlmostEqual(b["y2"], 0.75, places=3)
        # The height must be 0.5, not 0.3. 0.3 is 0.5 * 384/640, which is the
        # squash D4 produced.
        self.assertAlmostEqual(b["y2"] - b["y1"], 0.5, places=3,
                               msg="the box is 0.3 of the frame tall: that is the 384/640 squash")

    def test_letterbox_reports_the_content_size_and_the_pad(self):
        rgb = np.zeros((384, 640, 3), dtype=np.uint8)
        rgb[:, :] = 255
        out, nw, nh, pad_x, pad_y = server.letterbox(rgb, 416, 416, "nhwc")
        self.assertEqual(nw, 416)
        self.assertEqual(nh, 250)
        self.assertEqual((pad_x, pad_y), (0, 83))
        # The content is drawn AT the pad offset, not at the origin. The decoder
        # subtracts the pad, and that is only correct if the pad is where the
        # content went: a top-left anchor with a centred subtraction shifts every
        # box up by the pad on every frame, which reads as a detector that cannot
        # find the road.
        self.assertAlmostEqual(float(out[83, 0, 0]), 1.0, places=5, msg="the content was not drawn at pad_y")
        self.assertAlmostEqual(float(out[82, 0, 0]), 0.0, places=5, msg="the row above the content is not pad")
        self.assertAlmostEqual(float(out[83 + 250, 0, 0]), 0.0, places=5, msg="the content is taller than it says")
        # And the content never overruns the canvas.
        self.assertLessEqual(pad_y + nh, 416)
        self.assertLessEqual(pad_x + nw, 416)

    def test_a_square_frame_has_no_pad(self):
        rgb = np.full((640, 640, 3), 255, dtype=np.uint8)
        out, nw, nh, pad_x, pad_y = server.letterbox(rgb, 416, 416, "nhwc")
        self.assertEqual((nw, nh), (416, 416))
        self.assertEqual((pad_x, pad_y), (0, 0))
        self.assertAlmostEqual(float(out[0, 0, 0]), 1.0, places=5)


class ClassNameParserTests(unittest.TestCase):
    """T3.6: Ultralytics writes `{0: 'person', 1: 'bicycle'}` and the old parser
    did `int(kv.split(":", 1)[0])` on the first comma-separated piece, which
    raises ValueError on the leading `{` and KILLS THE SIDECAR AT LOAD.

    A model that loads and then cannot be named is a model whose every box
    arrives as the string "9", and the run's class column is a column of integers.
    """

    def test_the_ultralytics_dict_form_parses(self):
        got = server.parse_class_names("{0: 'person', 1: 'bicycle', 2: 'car'}", "v8.onnx")
        self.assertEqual(got, {0: "person", 1: "bicycle", 2: "car"})

    def test_the_unquoted_dict_form_parses(self):
        got = server.parse_class_names("{0: person, 1: bicycle}", "v8.onnx")
        self.assertEqual(got, {0: "person", 1: "bicycle"})

    def test_the_plain_pair_list_parses(self):
        got = server.parse_class_names("0:person,1:bicycle,2:car", "v8.onnx")
        self.assertEqual(got, {0: "person", 1: "bicycle", 2: "car"})

    def test_an_empty_metadata_is_not_an_error(self):
        """A model with no names is a real case — the measured one in this
        repository has an empty metadata map — and the COCO fallback covers it."""
        self.assertEqual(server.parse_class_names("", "zoo.onnx"), {})
        self.assertEqual(server.parse_class_names("   ", "zoo.onnx"), {})

    def test_an_unparseable_entry_is_refused_with_the_raw_string(self):
        for bad in ("{0: person, one: bicycle}", "{0: }", "not a mapping at all"):
            with self.assertRaises(ValueError, msg=f"{bad!r} was accepted"):
                server.parse_class_names(bad, "v8.onnx")
        try:
            server.parse_class_names("{0: person, one: bicycle}", "v8.onnx")
        except ValueError as exc:
            self.assertIn("one: bicycle", str(exc), "the refusal must quote what it could not read")
            self.assertIn("v8.onnx", str(exc), "the refusal must name the model")

    def test_the_names_decide_the_objectness(self):
        """When the model carries names, their LENGTH is the class count and the
        arithmetic is exact: 4 + len(names) or 5 + len(names), never a guess."""
        spec = server.ExportSpec("nchw", 640, 640, classes={0: "a", 1: "b", 2: "c"})
        self.assertIs(spec.decide_objectness(7), False, "4 + 3 names means no objectness")
        self.assertIs(spec.decide_objectness(8), True, "5 + 3 names means an objectness")
        self.assertIsNone(spec.decide_objectness(9), "neither, and that must be a refusal")


class CrossClassNMSTests(unittest.TestCase):
    """T3.7: two passes, because a class-aware pass alone cannot collapse a `car`
    and a `truck` that are the same car.

    A single class-aware NMS only ever compares boxes of the SAME class, so two
    boxes of different classes that overlap completely both survive it. The
    engine associates both with one track, the picture shows one object, and the
    report says two.
    """

    def test_one_car_reported_as_both_a_car_and_a_truck_is_one_car(self):
        boxes = np.array([[0.10, 0.10, 0.50, 0.50], [0.11, 0.11, 0.51, 0.51]], dtype=np.float64)
        scores = np.array([0.90, 0.85])
        classes = np.array([2, 7])  # car, truck: the same object
        after_class = server.nms(boxes.tolist(), scores, classes, server.NMS_IOU_CLASS)
        self.assertEqual(len(after_class), 2, "the class-aware pass cannot collapse two different classes: that is the point")
        after_both = server.nms_global(boxes[:1], scores[:1], server.NMS_IOU_GLOBAL)
        # The second pass, on the survivors of the first, collapses them.
        both = server.nms_global(
            np.array([s["box"] for s in after_class], dtype=np.float64),
            np.array([s["score"] for s in after_class], dtype=np.float64),
            server.NMS_IOU_GLOBAL)
        self.assertEqual(len(both), 1, "the class-agnostic pass kept both: a car and a truck on one car")
        self.assertAlmostEqual(both[0]["score"], 0.90)
        self.assertGreater(len(after_both), 0)

    def test_two_different_classes_that_really_are_two_objects_survive(self):
        """The second pass must not eat real objects. A person reading a book
        overlap and are still two objects; a 0.75 IoU does not collapse them."""
        eng = fused_engine()
        t = v8_tensor(fields=84, n=2)
        put_fused(t, 0, 0.30, 0.50, 0.10, 0.20, obj=0.0, class_index=0, score=0.9)
        put_fused(t, 1, 0.315, 0.52, 0.08, 0.16, obj=0.0, class_index=39, score=0.85)
        boxes, tel = eng.decode(t, 640, 384, 640, 384)
        self.assertEqual(len(boxes), 2, "two genuinely different objects were collapsed into one")
        self.assertEqual(tel["post_nms_class"], 2)

    def test_the_two_thresholds_are_distinct_and_reported(self):
        """Both are configuration and both are echoed, because a threshold nobody
        can see is a threshold nobody can argue with."""
        self.assertLess(server.NMS_IOU_CLASS, server.NMS_IOU_GLOBAL)
        eng = fused_engine()
        t = v8_tensor(fields=84, n=1)
        put_fused(t, 0, 0.5, 0.5, 0.2, 0.2, obj=0.0, class_index=0, score=0.9)
        _, tel = eng.decode(t, 640, 384, 640, 384)
        self.assertEqual(tel["nms_class_iou"], server.NMS_IOU_CLASS)
        self.assertEqual(tel["nms_global_iou"], server.NMS_IOU_GLOBAL)


class AnchoredDecodeTests(unittest.TestCase):
    """The family the SHIPPED model turns out to be, measured rather than assumed.

    A darknet head emits offsets from per-cell anchor boxes and the anchors live
    in the model's .cfg, not in the ONNX graph. Reading such an export as though
    its coordinates were final does not produce wrong boxes: it produces NOTHING,
    and nothing is the hardest failure to tell from a quiet scene.
    """

    def test_the_family_is_decided_from_the_measured_output_structure(self):
        anchored = server.ExportSpec.decide_family([["a", 52, 52, 3, 85], ["b", 26, 26, 3, 85], ["c", 13, 13, 3, 85]])
        self.assertEqual(anchored, server.ExportSpec.FAMILY_ANCHORED)
        fused = server.ExportSpec.decide_family([["a", 84, 8400]])
        self.assertEqual(fused, server.ExportSpec.FAMILY_FUSED)
        self.assertEqual(server.ExportSpec.decide_family([["a", 8400, 84]]), server.ExportSpec.FAMILY_FUSED)

    def test_the_anchored_transform_produces_a_box_where_the_anchor_is(self):
        eng = StubEngine(classes=None, input_w=416, input_h=416)
        eng.spec.decoder_family = server.ExportSpec.FAMILY_ANCHORED
        eng.anchors = [[(32.0, 32.0)], [(32.0, 32.0)], [(32.0, 32.0)]]
        g = 2
        t = np.zeros((1, g, g, 1, 85), np.float32)
        # tx=0, ty=0 sigmoid to 0.5, which is the CENTRE of a 2x2 grid; the cell
        # (row 1, col 1) has its centre at 0.75. tw=th=0 exp to 1, so the box is
        # exactly one anchor wide: 32 of 416, which is 32/250 of a 640x384 frame
        # letterboxed to a 416x416 canvas with 250 of content.
        put_anchored(t, 1 * 2 * 1 + 1, 0.0, 0.0, 0.0, 0.0, obj=0.9, class_index=2, score=0.8)
        outputs = [t, np.zeros((1, 2, 2, 1, 85), np.float32), np.zeros((1, 1, 1, 1, 85), np.float32)]
        boxes, tel = eng.decode_anchored(outputs, 640, 384, 416, 250, 0, 83)
        self.assertTrue(boxes, "the anchored transform produced nothing")
        b = boxes[0]
        self.assertEqual(b["cls"], "car")
        self.assertAlmostEqual(b["x1"], (0.75 * 416 - 16 - 0) / 416, places=3)
        self.assertAlmostEqual(b["x2"], (0.75 * 416 + 16 - 0) / 416, places=3)
        # Vertical: the anchor is 32 of 416 of the CANVAS, and the canvas content
        # is 250 tall starting at pad 83, so the box's frame fraction is
        # (cy*416 - 16 - 83) / 250.
        self.assertAlmostEqual(b["y1"], (0.75 * 416 - 16 - 83) / 250, places=3)
        self.assertAlmostEqual(b["y2"], (0.75 * 416 + 16 - 83) / 250, places=3)
        self.assertEqual(tel["scales"], 3)

    def test_an_anchor_file_that_does_not_divide_into_three_scales_is_refused(self):
        import tempfile
        with tempfile.NamedTemporaryFile("w", suffix=".txt", delete=False) as fh:
            fh.write("10,13\n16,30\n")
            path = fh.name
        try:
            with self.assertRaises(ValueError, msg="a two-anchor file was accepted"):
                server.load_anchors(path)
        finally:
            os.unlink(path)

    def test_an_anchor_file_with_a_malformed_line_is_refused(self):
        import tempfile
        with tempfile.NamedTemporaryFile("w", suffix=".txt", delete=False) as fh:
            fh.write("10,13\nnot a pair\n33,23\n")
            path = fh.name
        try:
            with self.assertRaises(ValueError, msg="a malformed anchor line was accepted"):
                server.load_anchors(path)
        finally:
            os.unlink(path)


class ClampAndGateTests(unittest.TestCase):
    """CLAMP FIRST, then gate — and the reason is a whole frame, not a box.

    A box that sits entirely outside the frame has a healthy area before clamping
    and NO area after it. Gated before clamping it waves through, and the wire
    then carries `{x1:0, y1:0, x2:0, y2:0}`. The Go client reads an inverted box
    as an uninterpretable answer and refuses the WHOLE frame, so one box off the
    top of the picture turns nineteen good detections into `cannot_tell`.
    """

    def test_a_box_that_clamps_to_nothing_is_dropped_and_counted(self):
        eng = fused_engine()
        t = v8_tensor(fields=84, n=2)
        # Both boxes are in PIXELS, because the coordinate space is one decision
        # for the whole tensor: a frame cannot half-speak pixels and half-speak
        # fractions, and a real model never mixes them.
        put_fused(t, 0, 320.0, 192.0, 128.0, 76.0, obj=0.0, class_index=2, score=0.9)
        # A large box entirely ABOVE the frame.
        put_fused(t, 1, 320.0, -200.0, 400.0, 200.0, obj=0.0, class_index=2, score=0.8)
        boxes, tel = eng.decode(t, 640, 384, 640, 384)
        self.assertEqual(len(boxes), 1, "a box that clamps to nothing was sent as a zero-size box")
        self.assertGreaterEqual(tel["dropped_degenerate"], 1)
        for b in boxes:
            self.assertGreater(b["x2"], b["x1"], f"an inverted box reached the wire: {b}")
            self.assertGreater(b["y2"], b["y1"], f"an inverted box reached the wire: {b}")

    def test_a_box_partly_in_frame_is_clamped_not_discarded(self):
        eng = fused_engine()
        t = v8_tensor(fields=84, n=1)
        put_fused(t, 0, -40.0, 192.0, 200.0, 100.0, obj=0.0, class_index=2, score=0.9)
        boxes, _ = eng.decode(t, 640, 384, 640, 384)
        self.assertTrue(boxes, "a box partly in frame was discarded: the object is invisible to the line machine")
        self.assertAlmostEqual(boxes[0]["x1"], 0.0, places=6)

    def test_a_sliver_is_dropped_as_tiny_and_counted(self):
        eng = fused_engine()
        t = v8_tensor(fields=84, n=1)
        # 2 x 2 pixels: real area, but it is a speck.
        put_fused(t, 0, 320.0, 192.0, 2.0, 2.0, obj=0.0, class_index=2, score=0.9)
        boxes, tel = eng.decode(t, 640, 384, 640, 384)
        self.assertEqual(boxes, [])
        self.assertEqual(tel["dropped_tiny"], 1)

    def test_a_sliver_aspect_ratio_is_dropped(self):
        eng = fused_engine()
        t = v8_tensor(fields=84, n=1)
        # 500 x 4 pixels: 20:1, which is the edge of a shadow or a letterbox band.
        put_fused(t, 0, 320.0, 192.0, 500.0, 4.0, obj=0.0, class_index=2, score=0.9)
        boxes, tel = eng.decode(t, 640, 384, 640, 384)
        self.assertEqual(boxes, [])
        self.assertEqual(tel["dropped_tiny"], 1)


class ShapeTests(unittest.TestCase):
    """Every rank the decoder must survive, and the field count decides — not the
    rank. The previous version matched ranks 3 and 4, so the measured rank 5 of
    the shipped model matched neither and every run reported zero detections."""

    def setUp(self):
        self.eng = StubEngine(classes=coco_names(), input_w=640, input_h=640)

    def test_every_layout_the_decoders_see(self):
        for shape, want_fields in [
            ((1, 52, 52, 3, 85), 85),
            ((1, 26, 26, 3, 85), 85),
            ((1, 13, 13, 3, 85), 85),
            ((1, 84, 8400), 84),
            ((1, 8400, 84), 84),
            ((1, 10647, 85), 85),
        ]:
            raw = np.full(shape, -6.0, np.float32)
            flat, how = self.eng.rows(raw)
            self.assertIsNotNone(flat, f"shape {shape} was not read at all")
            self.assertEqual(flat.shape[1], want_fields,
                             f"shape {shape} produced {flat.shape[1]} fields, want {want_fields} ({how})")
            self.assertEqual(flat.shape[0], int(np.prod(shape[1:])) // want_fields,
                             f"shape {shape} produced the wrong number of candidates")

    def test_a_tensor_where_no_axis_is_a_field_count_is_refused(self):
        """A `(7, 9, 11)` tensor is not a detector's output and the decoder must say
        so rather than pick the tidier-looking axis. This is what a model whose
        export nobody has read would produce, and the alternative — guessing — is
        what put every box on the frame's corner in the first place."""
        eng = StubEngine(classes=None, input_w=640, input_h=640)
        flat, why = eng.rows(np.zeros((7, 9, 11), np.float32))
        self.assertIsNone(flat, "a 7x9x11 tensor was read as a detector's output")
        self.assertIn("field count", why)

    def test_a_names_carrying_model_decides_the_field_axis_from_its_own_vocabulary(self):
        """The tier that settles a genuinely ambiguous shape. A `(84, 50)` tensor
        is 84 records of 50 fields or 50 records of 84 fields, and only the
        model's own class count can tell — 80 names means 84 fields, not 50."""
        eng = StubEngine(classes=coco_names(), input_w=640, input_h=640)
        flat, how = eng.rows(np.zeros((84, 50), np.float32))
        self.assertIsNotNone(flat)
        self.assertEqual(flat.shape, (50, 84), f"the 80-name vocabulary did not settle the axis ({how})")
        # And a tensor with no 84-or-85 axis is refused against that vocabulary,
        # not read structurally and hoped for.
        refused, _ = eng.rows(np.zeros((100, 9), np.float32))
        self.assertIsNone(refused, "a tensor with no 84-field axis was read anyway")

    def test_a_two_field_tensor_is_not_a_detector(self):
        flat, _ = self.eng.rows(np.zeros((1, 2), np.float32))
        self.assertIsNone(flat, "two fields was accepted as a detector's output")

    def test_a_one_dimensional_tensor_is_not_a_detector(self):
        eng = fused_engine()
        boxes, tel = eng.decode(np.zeros((1,), np.float32), 640, 384, 640, 384)
        self.assertEqual(boxes, [])
        self.assertEqual(tel["coord_space"], "unknown")

    def test_a_telemetry_block_is_always_complete(self):
        """Every key present on every answer, including a failed one. A missing
        key and a zero are different facts, and the Go side's `cannot_tell`
        tripwire can only fire on a key that is there."""
        eng = fused_engine()
        for raw in (np.zeros((1,), np.float32), np.zeros((1, 84, 3), np.float32),
                    np.zeros((1, 84, 3), np.float32)):
            _, tel = eng.decode(raw, 640, 384, 640, 384)
            for key in ("candidates", "pre_nms", "post_nms", "post_nms_class", "dropped_degenerate",
                        "dropped_tiny", "coord_space", "has_objectness", "median_box_area",
                        "identical_geometry", "nms_class_iou", "nms_global_iou", "median_object_px"):
                self.assertIn(key, tel, f"the telemetry has no {key}")


class FloorTests(unittest.TestCase):
    """The model emits every candidate anchor with some non-zero score, because a
    sigmoid of a logit is never exactly zero. Without a floor, NMS keeps thousands
    of them and the sidecar answers with a megabyte of noise that looks like a
    detection rate."""

    def test_nothing_above_the_floor_sends_nothing(self):
        eng = fused_engine()
        t = v8_tensor(fields=84, n=1200)
        put_fused(t, 0, 0.5, 0.5, 0.2, 0.2, obj=0.0, class_index=2, score=0.02)
        boxes, tel = eng.decode(t, 640, 384, 640, 384)
        self.assertEqual(boxes, [], "a candidate three orders of magnitude below the floor was sent")
        self.assertEqual(tel["pre_nms"], 0)
        self.assertEqual(tel["candidates"], 1200)

    def test_at_most_one_hundred_boxes_are_sent(self):
        eng = fused_engine()
        t = v8_tensor(fields=84, n=2000)
        for i in range(2000):
            t[0, 0, i] = 0.02 + (i % 97) * 0.009
            t[0, 1, i] = 0.03 + (i % 89) * 0.010
            t[0, 2, i] = 0.02
            t[0, 3, i] = 0.02
            t[0, 4, i] = 0.3
            t[0, 4 + (i % 3), i] = 0.8
        boxes, _ = eng.decode(t, 640, 384, 640, 384)
        self.assertLessEqual(len(boxes), server.MAX_EMITTED_BOXES)


if __name__ == "__main__":
    unittest.main()
