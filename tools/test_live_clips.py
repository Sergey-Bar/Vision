from datetime import datetime, timezone
from pathlib import Path
import tempfile
import unittest

from live_clips import ClipRecorder


class FakeFrame:
    shape = (720, 1920, 3)


class FakeWriter:
    def __init__(self, path: str, opened: bool = True):
        self.path = Path(path)
        self.opened = opened
        self.frames = 0

    def isOpened(self):
        return self.opened

    def write(self, _frame):
        self.frames += 1

    def release(self):
        if self.opened:
            self.path.write_bytes(f"frames={self.frames}".encode())


class FakeCV2:
    INTER_AREA = 3

    def __init__(self, opened: bool = True):
        self.opened = opened

    @staticmethod
    def VideoWriter_fourcc(*_args):
        return 0

    def VideoWriter(self, path, _fourcc, _fps, _size):
        return FakeWriter(path, self.opened)

    @staticmethod
    def resize(frame, _size, interpolation):
        del interpolation
        return frame


class ClipRecorderTests(unittest.TestCase):
    def test_alert_clip_is_bounded_hashed_and_served_from_safe_name(self):
        with tempfile.TemporaryDirectory() as tmp:
            recorder = ClipRecorder(FakeCV2(), tmp, "../unsafe/video", fps=2, seconds=1)
            at = datetime(2026, 1, 2, 3, 4, 5, tzinfo=timezone.utc)
            self.assertIsNone(recorder.start(FakeFrame(), at))
            clip = recorder.write(FakeFrame())
            self.assertEqual(clip["frames"], 2)
            self.assertEqual(clip["durationSeconds"], 1.0)
            self.assertRegex(clip["sha256"], r"^[a-f0-9]{64}$")
            self.assertNotIn("..", clip["uri"])
            self.assertEqual(recorder.state, "stored")

    def test_writer_failure_is_explicit(self):
        with tempfile.TemporaryDirectory() as tmp:
            recorder = ClipRecorder(FakeCV2(opened=False), tmp, "stream", fps=25)
            self.assertIsNone(recorder.start(FakeFrame()))
            self.assertEqual(recorder.state, "degraded")
            self.assertIn("could not open", recorder.error)


if __name__ == "__main__":
    unittest.main()
