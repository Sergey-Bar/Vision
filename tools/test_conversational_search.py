from dataclasses import asdict
from datetime import datetime, timezone
from pathlib import Path
import tempfile
import unittest

from conversational_search import (
    ConversationalResponse,
    ConversationalSearch,
    SearchResult,
)
from live_knowledge import KnowledgeStore


def frame(video_id: str, *, restricted: bool) -> dict:
    detection = {
        "id": "car-7", "label": "red car", "category": "vehicle", "color": "red",
        "vehicleType": "car", "confidence": 0.92, "bbox": [0.1, 0.5, 0.1, 0.1],
        "zone": "no-zone" if restricted else "public", "violation": restricted,
    }
    return {
        "videoId": video_id, "at": datetime.now(timezone.utc).isoformat(), "frame": 12,
        "backend": "cuda-yolo11x", "profile": "yolo11x-1920", "people": 0, "vehicles": 1,
        "animals": 0, "other": 0, "restrictedCount": int(restricted),
        "occupancyPercent": 12.5, "averageConfidence": 0.92, "detections": [detection],
        "alerts": [{"rule": "no-zone-entry"}] if restricted else [],
    }


class ConversationalSearchTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.store = KnowledgeStore(Path(self.tmp.name) / "conv.sqlite3")
        self.store.ingest_frame(frame("AAAAAAAAAAA", restricted=True))
        self.search = ConversationalSearch(self.store)

    async def asyncTearDown(self):
        self.tmp.cleanup()

    def test_search_result_uses_snake_case_fields(self):
        sr = SearchResult(
            citation="obs:1", video_id="v", observed_at="t", frame=1, track_id="t1",
            label="car", category="vehicle", zone="z", confidence=0.9, violation=True,
        )
        self.assertEqual(sr.video_id, "v")
        asdict(sr)

    async def test_chat_returns_serializable_conversational_response(self):
        result = await self.search.chat("red", "AAAAAAAAAAA")
        self.assertEqual(result.state, "answered")
        self.assertTrue(result.results)
        self.assertEqual(result.results[0].video_id, "AAAAAAAAAAA")
        payload = asdict(result)
        self.assertIn("results", payload)
        self.assertEqual(payload["results"][0]["video_id"], "AAAAAAAAAAA")


if __name__ == "__main__":
    unittest.main()