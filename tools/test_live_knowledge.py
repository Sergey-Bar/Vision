from datetime import datetime, timedelta, timezone
from pathlib import Path
import sqlite3
import tempfile
import unittest

from aiohttp.test_utils import TestClient, TestServer

from live_knowledge import KnowledgeStore, query_tokens
from live_poc_server import app


def frame(video_id: str, at: datetime, *, restricted: bool) -> dict:
    detection = {
        "id": "car-7", "label": "red car", "category": "vehicle", "color": "red",
        "vehicleType": "car", "confidence": 0.92, "bbox": [0.1, 0.5, 0.1, 0.1],
        "zone": "no-zone" if restricted else "public", "violation": restricted,
    }
    return {
        "videoId": video_id, "at": at.isoformat(), "frame": 12, "backend": "cuda-yolo11x",
        "profile": "yolo11x-1920", "people": 0, "vehicles": 1, "animals": 0, "other": 0,
        "restrictedCount": int(restricted), "occupancyPercent": 12.5, "averageConfidence": 0.92,
        "detections": [detection],
        "alerts": [{"rule": "no-zone-entry"}] if restricted else [],
        "imageData": "data:image/jpeg;base64,large-payload-must-not-be-stored",
        "clip": {"uri": "clips/red-car.mp4", "sha256": "abc123"},
    }


class KnowledgeStoreTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.path = Path(self.tmp.name) / "knowledge.sqlite3"
        self.store = KnowledgeStore(self.path)
        self.now = datetime.now(timezone.utc)

    def tearDown(self):
        self.tmp.cleanup()

    def test_ingest_builds_attributable_graph_without_storing_frame_image(self):
        observation_id = self.store.ingest_frame(frame("AAAAAAAAAAA", self.now, restricted=True))
        graph = self.store.graph("AAAAAAAAAAA")
        relations = {edge["relation"] for edge in graph["edges"]}
        self.assertEqual(relations, {"observed", "located_in", "triggered"})
        self.assertTrue(all(edge["citation"] == f"obs:{observation_id}" for edge in graph["edges"]))

        db = sqlite3.connect(self.path)
        try:
            payload, uri, digest, tenant = db.execute(
                "SELECT payload_json, artifact_uri, artifact_sha256, tenant_id FROM observations"
            ).fetchone()
        finally:
            db.close()
        self.assertNotIn("imageData", payload)
        self.assertEqual((uri, digest, tenant), ("clips/red-car.mp4", "abc123", "local"))

    def test_chat_is_grounded_and_missing_evidence_is_explicit(self):
        observation_id = self.store.ingest_frame(frame("AAAAAAAAAAA", self.now, restricted=True))
        answer = self.store.chat("show red car in no-zone", "AAAAAAAAAAA")
        self.assertEqual(answer["state"], "answered")
        self.assertIn(f"obs:{observation_id}", answer["citations"])
        self.assertIn("red car", answer["answer"])

        missing = self.store.chat("purple elephant", "AAAAAAAAAAA")
        self.assertEqual(missing, {
            "state": "unavailable", "answer": "No stored observation supports that answer.", "citations": []
        })

    def test_natural_language_normalization_supports_english_and_hebrew(self):
        self.store.ingest_frame(frame("AAAAAAAAAAA", self.now - timedelta(seconds=1), restricted=True))
        self.store.ingest_frame(frame("AAAAAAAAAAA", self.now, restricted=True))
        for question in (
            "Which red vehicles entered the restricted zone?",
            "כמה רכבים אדומים באזור המוגבל",
        ):
            with self.subTest(question=question):
                answer = self.store.chat(question, "AAAAAAAAAAA")
                self.assertEqual(answer["state"], "answered")
                self.assertTrue(answer["citations"])
                self.assertEqual(len(answer["results"]), 1)
        count = self.store.chat("כמה רכבים אדומים באזור המוגבל", "AAAAAAAAAAA")
        self.assertIn("1 unique track(s) across 2 matching stored observation(s)", count["answer"])
        self.assertEqual(query_tokens("Which red vehicles entered the restricted zone?"), ["red", "vehicle", "no-zone"])

    def test_critical_ranking_is_explainable_and_recency_aware(self):
        self.store.ingest_frame(frame("AAAAAAAAAAA", self.now - timedelta(seconds=5), restricted=True))
        self.store.ingest_frame(frame("BBBBBBBBBBB", self.now - timedelta(seconds=5), restricted=False))
        ranked = self.store.critical_streams(now=self.now)
        self.assertEqual([item["videoId"] for item in ranked], ["AAAAAAAAAAA", "BBBBBBBBBBB"])
        self.assertGreater(ranked[0]["score"], ranked[1]["score"])
        self.assertTrue(any("restricted-zone" in reason for reason in ranked[0]["reasons"]))

    def test_observation_cap_does_not_delete_clip_catalog(self):
        store = KnowledgeStore(self.path, max_observations=1)
        store.ingest_frame(frame("AAAAAAAAAAA", self.now - timedelta(minutes=1), restricted=True))
        next_frame = frame("AAAAAAAAAAA", self.now, restricted=False)
        next_frame.pop("clip")
        store.ingest_frame(next_frame)
        db = sqlite3.connect(self.path)
        try:
            observations = db.execute("SELECT COUNT(*) FROM observations").fetchone()[0]
            clips = db.execute("SELECT COUNT(*) FROM clips").fetchone()[0]
        finally:
            db.close()
        self.assertEqual(observations, 1)
        self.assertEqual(clips, 1)
        self.assertEqual(store.clips("AAAAAAAAAAA")[0]["sha256"], "abc123")

    def test_orphan_clip_is_recovered_after_crash_idempotently(self):
        clip_root = Path(self.tmp.name) / "clips"
        clip_root.mkdir()
        orphan = clip_root / "AAAAAAAAAAA-20260102T030405-deadbeef.mp4"
        orphan.write_bytes(b"completed clip")
        self.assertEqual(self.store.reconcile_clips(clip_root), 1)
        self.assertEqual(self.store.reconcile_clips(clip_root), 0)
        recovered = self.store.clips("AAAAAAAAAAA")
        self.assertEqual(len(recovered), 1)
        self.assertTrue(recovered[0]["metadata"]["recovered"])


class KnowledgeAPITests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.store = KnowledgeStore(Path(self.tmp.name) / "api.sqlite3")
        self.store.ingest_frame(frame("AAAAAAAAAAA", datetime.now(timezone.utc), restricted=True))
        self.client = TestClient(TestServer(app(self.store)))
        await self.client.start_server()

    async def asyncTearDown(self):
        await self.client.close()
        self.tmp.cleanup()

    async def test_chat_critical_and_graph_endpoints(self):
        response = await self.client.post("/api/chat", json={"question": "red car", "videoId": "AAAAAAAAAAA"})
        self.assertEqual(response.status, 200)
        self.assertEqual((await response.json())["state"], "answered")

        response = await self.client.get("/api/critical")
        self.assertEqual(response.status, 200)
        self.assertEqual((await response.json())["streams"][0]["videoId"], "AAAAAAAAAAA")

        response = await self.client.get("/api/graph?videoId=AAAAAAAAAAA")
        body = await response.json()
        self.assertEqual(response.status, 200)
        self.assertTrue(body["nodes"])
        self.assertTrue(body["edges"])

        response = await self.client.get("/api/clips?videoId=AAAAAAAAAAA")
        self.assertEqual(response.status, 200)
        self.assertEqual((await response.json())["clips"][0]["sha256"], "abc123")

    async def test_chat_rejects_unattributable_input(self):
        response = await self.client.post("/api/chat", json={"question": "red car", "videoId": "bad"})
        self.assertEqual(response.status, 400)


if __name__ == "__main__":
    unittest.main()
