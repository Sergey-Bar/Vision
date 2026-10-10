"""Durable, grounded knowledge storage for the local live-analytics POC."""

from __future__ import annotations

from contextlib import contextmanager
from datetime import datetime, timedelta, timezone
import hashlib
import json
from pathlib import Path
import re
import sqlite3
from typing import Any


TOKEN = re.compile(r"[\w-]+", re.UNICODE)
STOP_WORDS = {
    "a", "an", "and", "are", "at", "did", "enter", "entered", "find", "for", "in", "is", "me", "of", "on", "show", "the", "to", "what", "which",
    "את", "ב", "באזור", "האם", "הראה", "הצג", "ו", "כמה", "לי", "מה", "של",
}
SYNONYMS = {
    "cars": "vehicle", "vehicles": "vehicle", "trucks": "truck", "people": "person", "humans": "person",
    "רכב": "vehicle", "רכבים": "vehicle", "מכונית": "car", "מכוניות": "car",
    "אדם": "person", "אנשים": "person", "אדום": "red", "אדומה": "red", "אדומים": "red", "המוגבל": "no-zone",
}


def query_tokens(question: str) -> list[str]:
    normalized = question.casefold().replace("restricted zone", "no-zone").replace("no zone", "no-zone")
    return [SYNONYMS.get(token, token) for token in TOKEN.findall(normalized) if token not in STOP_WORDS][:8]


def _utc(value: str | None = None) -> datetime:
    if value:
        try:
            return datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(timezone.utc)
        except ValueError:
            pass
    return datetime.now(timezone.utc)


def risk_components(frame: dict[str, Any]) -> tuple[float, list[str]]:
    alerts = len(frame.get("alerts") or [])
    restricted = int(frame.get("restrictedCount") or 0)
    occupancy = max(0.0, min(100.0, float(frame.get("occupancyPercent") or 0)))
    score = min(100.0, min(60.0, alerts * 30.0) + min(30.0, restricted * 10.0) + min(10.0, occupancy * 0.1))
    reasons: list[str] = []
    if alerts:
        reasons.append(f"{alerts} active high-severity alert(s)")
    if restricted:
        reasons.append(f"{restricted} object(s) in a restricted zone")
    if occupancy >= 50:
        reasons.append(f"high frame occupancy ({occupancy:.1f}%)")
    if not reasons:
        reasons.append("no active critical signal")
    return score, reasons


class KnowledgeStore:
    def __init__(
        self, path: str | Path, tenant_id: str = "local", retention_hours: int = 168,
        max_observations: int = 100_000,
    ):
        self.path = Path(path)
        self.tenant_id = tenant_id
        self.retention_hours = max(1, retention_hours)
        self.max_observations = max(1, max_observations)
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self._initialize()

    def _connect(self) -> sqlite3.Connection:
        db = sqlite3.connect(self.path, timeout=10)
        db.row_factory = sqlite3.Row
        db.execute("PRAGMA foreign_keys=ON")
        db.execute("PRAGMA synchronous=FULL")
        return db

    @contextmanager
    def _db(self):
        db = self._connect()
        try:
            with db:
                yield db
        finally:
            db.close()

    def _initialize(self) -> None:
        with self._db() as db:
            db.execute("PRAGMA journal_mode=WAL")
            db.executescript(
                """
                CREATE TABLE IF NOT EXISTS observations (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    tenant_id TEXT NOT NULL,
                    video_id TEXT NOT NULL,
                    observed_at TEXT NOT NULL,
                    frame_no INTEGER NOT NULL,
                    backend TEXT NOT NULL,
                    profile TEXT NOT NULL,
                    people INTEGER NOT NULL,
                    vehicles INTEGER NOT NULL,
                    animals INTEGER NOT NULL,
                    other INTEGER NOT NULL,
                    restricted_count INTEGER NOT NULL,
                    occupancy_percent REAL NOT NULL,
                    average_confidence REAL NOT NULL,
                    risk_score REAL NOT NULL,
                    risk_reasons_json TEXT NOT NULL,
                    artifact_uri TEXT,
                    artifact_sha256 TEXT,
                    payload_json TEXT NOT NULL
                );
                CREATE INDEX IF NOT EXISTS observations_stream_time
                    ON observations(tenant_id, video_id, observed_at DESC);

                CREATE TABLE IF NOT EXISTS clips (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    tenant_id TEXT NOT NULL,
                    video_id TEXT NOT NULL,
                    observed_at TEXT NOT NULL,
                    artifact_uri TEXT NOT NULL,
                    artifact_sha256 TEXT NOT NULL,
                    metadata_json TEXT NOT NULL,
                    UNIQUE(tenant_id, artifact_sha256)
                );
                CREATE INDEX IF NOT EXISTS clips_stream_time
                    ON clips(tenant_id, video_id, observed_at DESC);

                CREATE TABLE IF NOT EXISTS entities (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    tenant_id TEXT NOT NULL,
                    observation_id INTEGER NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
                    track_id TEXT NOT NULL,
                    label TEXT NOT NULL,
                    category TEXT NOT NULL,
                    color TEXT NOT NULL,
                    vehicle_type TEXT NOT NULL,
                    zone TEXT NOT NULL,
                    confidence REAL NOT NULL,
                    violation INTEGER NOT NULL CHECK (violation IN (0, 1)),
                    bbox_json TEXT NOT NULL
                );
                CREATE INDEX IF NOT EXISTS entities_search
                    ON entities(tenant_id, label, category, color, vehicle_type, zone);

                CREATE TABLE IF NOT EXISTS relations (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    tenant_id TEXT NOT NULL,
                    observation_id INTEGER NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
                    source_kind TEXT NOT NULL,
                    source_id TEXT NOT NULL,
                    relation TEXT NOT NULL,
                    target_kind TEXT NOT NULL,
                    target_id TEXT NOT NULL,
                    evidence_json TEXT NOT NULL
                );
                CREATE INDEX IF NOT EXISTS relations_lookup
                    ON relations(tenant_id, source_kind, source_id, relation, target_kind, target_id);
                """
            )

    def ingest_frame(self, frame: dict[str, Any]) -> int:
        video_id = str(frame.get("videoId") or "").strip()
        if not video_id:
            raise ValueError("videoId is required for an attributable observation")
        observed = _utc(str(frame.get("at") or "")).isoformat()
        risk, reasons = risk_components(frame)
        artifact = frame.get("clip") if isinstance(frame.get("clip"), dict) else {}
        payload = {key: value for key, value in frame.items() if key != "imageData"}
        detections = frame.get("detections") if isinstance(frame.get("detections"), list) else []
        cutoff = (_utc() - timedelta(hours=self.retention_hours)).isoformat()

        with self._db() as db:
            cursor = db.execute(
                """INSERT INTO observations(
                    tenant_id, video_id, observed_at, frame_no, backend, profile,
                    people, vehicles, animals, other, restricted_count,
                    occupancy_percent, average_confidence, risk_score,
                    risk_reasons_json, artifact_uri, artifact_sha256, payload_json
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
                (
                    self.tenant_id, video_id, observed, int(frame.get("frame") or 0),
                    str(frame.get("backend") or "unknown"), str(frame.get("profile") or "unknown"),
                    int(frame.get("people") or 0), int(frame.get("vehicles") or 0),
                    int(frame.get("animals") or 0), int(frame.get("other") or 0),
                    int(frame.get("restrictedCount") or 0), float(frame.get("occupancyPercent") or 0),
                    float(frame.get("averageConfidence") or 0), risk, json.dumps(reasons),
                    artifact.get("uri"), artifact.get("sha256"), json.dumps(payload, separators=(",", ":")),
                ),
            )
            observation_id = int(cursor.lastrowid)
            if artifact.get("uri") and artifact.get("sha256"):
                db.execute(
                    """INSERT INTO clips(
                        tenant_id, video_id, observed_at, artifact_uri, artifact_sha256, metadata_json
                    ) VALUES (?, ?, ?, ?, ?, ?)
                    ON CONFLICT(tenant_id, artifact_sha256) DO UPDATE SET
                        artifact_uri = excluded.artifact_uri,
                        metadata_json = excluded.metadata_json""",
                    (
                        self.tenant_id, video_id, observed, str(artifact["uri"]),
                        str(artifact["sha256"]), json.dumps(artifact, separators=(",", ":")),
                    ),
                )
            for detection in detections:
                if not isinstance(detection, dict):
                    continue
                track_id = str(detection.get("id") or "unknown")
                zone = str(detection.get("zone") or "public")
                violation = bool(detection.get("violation"))
                db.execute(
                    """INSERT INTO entities(
                        tenant_id, observation_id, track_id, label, category, color,
                        vehicle_type, zone, confidence, violation, bbox_json
                    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
                    (
                        self.tenant_id, observation_id, track_id, str(detection.get("label") or "unknown"),
                        str(detection.get("category") or "other"), str(detection.get("color") or ""),
                        str(detection.get("vehicleType") or ""), zone,
                        float(detection.get("confidence") or 0), int(violation),
                        json.dumps(detection.get("bbox") or []),
                    ),
                )
                evidence = json.dumps({"observation_id": observation_id, "confidence": detection.get("confidence")})
                db.execute(
                    "INSERT INTO relations VALUES (NULL, ?, ?, 'stream', ?, 'observed', 'entity', ?, ?)",
                    (self.tenant_id, observation_id, video_id, track_id, evidence),
                )
                db.execute(
                    "INSERT INTO relations VALUES (NULL, ?, ?, 'entity', ?, 'located_in', 'zone', ?, ?)",
                    (self.tenant_id, observation_id, track_id, zone, evidence),
                )
                if violation:
                    db.execute(
                        "INSERT INTO relations VALUES (NULL, ?, ?, 'entity', ?, 'triggered', 'alert', 'no-zone-entry', ?)",
                        (self.tenant_id, observation_id, track_id, evidence),
                    )
            maintenance_interval = min(100, self.max_observations)
            if observation_id % maintenance_interval == 0:
                db.execute(
                    "DELETE FROM observations WHERE tenant_id = ? AND observed_at < ?",
                    (self.tenant_id, cutoff),
                )
                db.execute(
                    """DELETE FROM observations WHERE tenant_id = ? AND id NOT IN (
                           SELECT id FROM observations WHERE tenant_id = ? ORDER BY id DESC LIMIT ?
                       )""",
                    (self.tenant_id, self.tenant_id, self.max_observations),
                )
        return observation_id

    def clips(self, video_id: str | None = None, limit: int = 100) -> list[dict[str, Any]]:
        conditions = ["tenant_id = ?"]
        args: list[Any] = [self.tenant_id]
        if video_id:
            conditions.append("video_id = ?")
            args.append(video_id)
        args.append(max(1, min(limit, 1000)))
        with self._db() as db:
            rows = db.execute(
                f"SELECT * FROM clips WHERE {' AND '.join(conditions)} ORDER BY observed_at DESC LIMIT ?",
                args,
            ).fetchall()
        return [{
            "id": row["id"], "videoId": row["video_id"], "observedAt": row["observed_at"],
            "uri": row["artifact_uri"], "sha256": row["artifact_sha256"],
            "metadata": json.loads(row["metadata_json"]),
        } for row in rows]

    def reconcile_clips(self, root: str | Path) -> int:
        """Catalog completed MP4 files left behind by a server crash."""
        root = Path(root)
        root.mkdir(parents=True, exist_ok=True)
        recovered = 0
        with self._db() as db:
            for path in root.glob("*.mp4"):
                if path.is_symlink() or not path.is_file():
                    continue
                digest = hashlib.sha256(path.read_bytes()).hexdigest()
                video_id = path.name.split("-", 1)[0]
                if not re.fullmatch(r"[A-Za-z0-9_-]{1,64}", video_id):
                    video_id = "unknown"
                observed = datetime.fromtimestamp(path.stat().st_mtime, timezone.utc).isoformat()
                cursor = db.execute(
                    """INSERT OR IGNORE INTO clips(
                        tenant_id, video_id, observed_at, artifact_uri, artifact_sha256, metadata_json
                    ) VALUES (?, ?, ?, ?, ?, ?)""",
                    (
                        self.tenant_id, video_id, observed, f"/clips/{path.name}", digest,
                        json.dumps({"uri": f"/clips/{path.name}", "sha256": digest, "recovered": True, "bytes": path.stat().st_size}),
                    ),
                )
                recovered += cursor.rowcount
        return recovered

    def critical_streams(self, limit: int = 5, now: datetime | None = None) -> list[dict[str, Any]]:
        now = now or _utc()
        cutoff = (now - timedelta(minutes=15)).isoformat()
        with self._db() as db:
            rows = db.execute(
                """SELECT video_id, MAX(observed_at) AS last_seen,
                          MAX(risk_score) AS peak_risk, AVG(risk_score) AS average_risk,
                          SUM(restricted_count) AS restricted_events,
                          SUM(CASE WHEN risk_score >= 30 THEN 1 ELSE 0 END) AS critical_frames,
                          COUNT(*) AS observations
                   FROM observations
                   WHERE tenant_id = ? AND observed_at >= ?
                   GROUP BY video_id""",
                (self.tenant_id, cutoff),
            ).fetchall()
        ranked = []
        for row in rows:
            age = max(0.0, (now - _utc(row["last_seen"])).total_seconds())
            recency = max(0.2, 1.0 - age / 900.0)
            score = min(100.0, (0.75 * row["peak_risk"] + 0.25 * row["average_risk"]) * recency)
            reasons = []
            if row["critical_frames"]:
                reasons.append(f"{row['critical_frames']} critical frame(s) in the last 15 minutes")
            if row["restricted_events"]:
                reasons.append(f"{row['restricted_events']} restricted-zone presence observation(s)")
            if not reasons:
                reasons.append("recent activity without an active critical signal")
            ranked.append({
                "videoId": row["video_id"], "score": round(score, 1), "lastSeen": row["last_seen"],
                "observations": row["observations"], "reasons": reasons,
            })
        ranked.sort(key=lambda item: (-item["score"], item["videoId"]))
        return ranked[: max(1, min(limit, 20))]

    def graph(self, video_id: str | None = None, limit: int = 200) -> dict[str, Any]:
        where = "WHERE r.tenant_id = ?"
        args: list[Any] = [self.tenant_id]
        if video_id:
            where += " AND o.video_id = ?"
            args.append(video_id)
        args.append(max(1, min(limit, 1000)))
        with self._db() as db:
            rows = db.execute(
                f"""SELECT r.*, o.video_id, o.observed_at FROM relations r
                     JOIN observations o ON o.id = r.observation_id
                     {where} ORDER BY r.id DESC LIMIT ?""",
                args,
            ).fetchall()
        nodes: dict[str, dict[str, Any]] = {}
        edges = []
        for row in rows:
            source = f"{row['source_kind']}:{row['source_id']}"
            target = f"{row['target_kind']}:{row['target_id']}"
            nodes[source] = {"id": source, "kind": row["source_kind"], "label": row["source_id"]}
            nodes[target] = {"id": target, "kind": row["target_kind"], "label": row["target_id"]}
            edges.append({
                "source": source, "target": target, "relation": row["relation"],
                "citation": f"obs:{row['observation_id']}", "observedAt": row["observed_at"],
            })
        return {"nodes": list(nodes.values()), "edges": edges}

    def chat(self, question: str, video_id: str | None = None, limit: int = 10) -> dict[str, Any]:
        raw = question.strip()
        if not raw:
            return {"state": "unavailable", "answer": "Enter a question to search stored observations.", "citations": []}
        lowered = raw.casefold()
        if any(word in lowered for word in ("critical", "risk", "urgent", "קריטי", "סיכון", "דחוף")):
            ranked = self.critical_streams(limit=limit)
            if not ranked:
                return {"state": "unavailable", "answer": "No recent stream observations are stored.", "citations": []}
            answer = "; ".join(f"{item['videoId']} ({item['score']}/100): {', '.join(item['reasons'])}" for item in ranked)
            return {"state": "answered", "answer": answer, "citations": [f"stream:{item['videoId']}" for item in ranked], "results": ranked}

        tokens = query_tokens(raw)
        conditions = ["e.tenant_id = ?"]
        args: list[Any] = [self.tenant_id]
        if video_id:
            conditions.append("o.video_id = ?")
            args.append(video_id)
        searchable = "lower(e.label || ' ' || e.category || ' ' || e.color || ' ' || e.vehicle_type || ' ' || e.zone)"
        for token in tokens:
            conditions.append(f"{searchable} LIKE ?")
            args.append(f"%{token}%")
        row_limit = max(1, min(limit, 50))
        with self._db() as db:
            total_observations, unique_tracks = db.execute(
                f"""SELECT COUNT(*), COUNT(DISTINCT o.video_id || ':' || e.track_id)
                     FROM entities e JOIN observations o ON o.id = e.observation_id
                     WHERE {' AND '.join(conditions)}""",
                args,
            ).fetchone()
            rows = db.execute(
                f"""SELECT * FROM (
                         SELECT e.*, o.video_id, o.observed_at, o.frame_no,
                                ROW_NUMBER() OVER (
                                    PARTITION BY o.video_id, e.track_id ORDER BY o.observed_at DESC
                                ) AS recency_rank
                         FROM entities e JOIN observations o ON o.id = e.observation_id
                         WHERE {' AND '.join(conditions)}
                     ) WHERE recency_rank = 1
                     ORDER BY observed_at DESC LIMIT ?""",
                [*args, row_limit],
            ).fetchall()
        if not rows:
            return {"state": "unavailable", "answer": "No stored observation supports that answer.", "citations": []}
        citations = [f"obs:{row['observation_id']}" for row in rows]
        if any(word in lowered for word in ("count", "how many", "כמה")):
            answer = f"Found {unique_tracks} unique track(s) across {total_observations} matching stored observation(s)."
        else:
            answer = "; ".join(
                f"{row['label']} in {row['zone']} on stream {row['video_id']} at {row['observed_at']}"
                for row in rows[:5]
            )
        results = [{
            "citation": f"obs:{row['observation_id']}", "videoId": row["video_id"],
            "observedAt": row["observed_at"], "frame": row["frame_no"], "trackId": row["track_id"],
            "label": row["label"], "category": row["category"], "zone": row["zone"],
            "confidence": row["confidence"], "violation": bool(row["violation"]),
        } for row in rows]
        return {"state": "answered", "answer": answer, "citations": citations, "results": results}
