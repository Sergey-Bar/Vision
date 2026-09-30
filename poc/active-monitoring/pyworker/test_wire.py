"""The sidecar's telemetry keys, measured and pinned.

This is the PYTHON half of a two-sided pin. `pyworker/fixtures/telemetry_keys.json`
records the key set the sidecar emits; this test asserts the live telemetry dict
still matches it, so a field added here without the Go side fails HERE rather
than silently refusing every reply in production.

The Go half is internal/ai/wire_test.go, which decodes a payload built from the
same file with the same strict decoder production uses.
"""

import json
import os
import unittest

FIXTURE = os.path.join(os.path.dirname(__file__), "fixtures", "telemetry_keys.json")


def recorded_keys() -> list[str]:
    with open(FIXTURE, encoding="utf-8") as fh:
        return json.load(fh)["keys"]


class TelemetryWire(unittest.TestCase):
    def telemetry(self) -> dict:
        """The telemetry the sidecar ACTUALLY emits, built the same way it builds
        it: from the spec's own JSON plus the runtime counters.

        It is assembled rather than captured from a live inference on purpose. A
        key set read off a running model is a key set that depends on a model
        being present and warm, and a pin that skips itself when it cannot run is
        a pin that skips itself exactly when a new field is being added.
        """
        import server

        spec = server.ExportSpec(
            layout="nhwc",
            input_w=416,
            input_h=416,
            fields=85,
            has_objectness=True,
            coord_space="pixel",
            classes={},
            decoder_family=server.ExportSpec.FAMILY_ANCHORED,
        )
        engine = server.Engine.__new__(server.Engine)
        engine.spec = spec
        tel = engine._telemetry(
            10647, 63,
            {"post_nms_class": 22, "post_nms": 22, "dropped_degenerate": 0, "dropped_tiny": 3},
            [{"cls": "car", "score": 0.84, "x1": 0.48, "y1": 0.51, "x2": 0.61, "y2": 0.58}],
            640, 360,
        )
        tel["scales"] = 3  # added by the multi-scale path, as production does
        return tel

    def test_the_recorded_keys_are_the_keys_the_sidecar_emits(self):
        live = sorted(self.telemetry())
        recorded = sorted(recorded_keys())
        self.assertEqual(
            live, recorded,
            "the sidecar's telemetry keys have changed. The Go client refuses unknown "
            "fields, so a key added here and not there makes EVERY reply unreadable and "
            "the run reports cannot_tell forever with nothing red. Update "
            "pyworker/fixtures/telemetry_keys.json AND internal/ai.Telemetry in the same "
            "commit.",
        )

    def test_the_fixture_is_not_empty(self):
        # A fixture that lost its keys would make the test above compare [] to []
        # and pass, which is the failure mode a pinned list exists to prevent.
        self.assertGreaterEqual(len(recorded_keys()), 15)


if __name__ == "__main__":
    unittest.main()