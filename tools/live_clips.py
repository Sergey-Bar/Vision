"""Bounded event-clip recording for the live detector."""

from __future__ import annotations

from datetime import datetime, timezone
import hashlib
from pathlib import Path
import re
from typing import Any
from uuid import uuid4


SAFE_ID = re.compile(r"[^A-Za-z0-9_-]")


class ClipRecorder:
    def __init__(
        self, cv2: Any, root: str | Path, video_id: str, fps: float,
        seconds: float = 8.0, max_width: int = 1280,
    ):
        self.cv2 = cv2
        self.root = Path(root)
        self.video_id = SAFE_ID.sub("_", video_id)[:64] or "stream"
        self.fps = fps if 1 <= fps <= 120 else 25.0
        self.seconds = max(1.0, min(seconds, 60.0))
        self.max_width = max(320, max_width)
        self.writer = None
        self.path: Path | None = None
        self.width = 0
        self.height = 0
        self.frames = 0
        self.remaining = 0
        self.state = "idle"
        self.error = ""

    @property
    def active(self) -> bool:
        return self.writer is not None

    def start(self, frame, at: datetime | None = None) -> dict[str, Any] | None:
        if self.active:
            return None
        at = at or datetime.now(timezone.utc)
        source_h, source_w = frame.shape[:2]
        scale = min(1.0, self.max_width / max(1, source_w))
        self.width = max(2, int(source_w * scale) // 2 * 2)
        self.height = max(2, int(source_h * scale) // 2 * 2)
        self.root.mkdir(parents=True, exist_ok=True)
        name = f"{self.video_id}-{at.strftime('%Y%m%dT%H%M%S')}-{uuid4().hex[:8]}.mp4"
        self.path = self.root / name
        fourcc = self.cv2.VideoWriter_fourcc(*"mp4v")
        writer = self.cv2.VideoWriter(str(self.path), fourcc, self.fps, (self.width, self.height))
        if not writer.isOpened():
            writer.release()
            self.state = "degraded"
            self.error = "OpenCV could not open the MP4 writer"
            self.path = None
            return None
        self.writer = writer
        self.frames = 0
        self.remaining = max(1, round(self.fps * self.seconds))
        self.state = "recording"
        self.error = ""
        return self.write(frame)

    def write(self, frame) -> dict[str, Any] | None:
        if not self.active:
            return None
        prepared = frame
        if frame.shape[1] != self.width or frame.shape[0] != self.height:
            prepared = self.cv2.resize(frame, (self.width, self.height), interpolation=self.cv2.INTER_AREA)
        self.writer.write(prepared)
        self.frames += 1
        self.remaining -= 1
        if self.remaining > 0:
            return None
        return self.finish()

    def finish(self) -> dict[str, Any] | None:
        if not self.active or self.path is None:
            return None
        self.writer.release()
        self.writer = None
        digest = hashlib.sha256(self.path.read_bytes()).hexdigest()
        metadata = {
            "uri": f"/clips/{self.path.name}", "sha256": digest,
            "frames": self.frames, "fps": self.fps,
            "durationSeconds": round(self.frames / self.fps, 3),
            "width": self.width, "height": self.height,
        }
        self.state = "stored"
        return metadata

    def close(self) -> dict[str, Any] | None:
        return self.finish()
