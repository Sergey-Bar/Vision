#!/usr/bin/env python3
"""Behavioral learning per camera - baselines, anomaly detection, seasonal patterns.

Learns normal patterns per camera/scene and detects anomalies:
- Count baselines (people, vehicles, animals by hour/day)
- Spatial baselines (zone occupancy, track density heatmaps)
- Temporal patterns (rush hour, quiet hours, seasonal)
- Motion flow baselines (direction, speed, dwell time)
- Anomaly scoring with adaptive thresholds
"""

from __future__ import annotations

import json
import math
import sqlite3
from collections import defaultdict, deque
from contextlib import contextmanager
from dataclasses import asdict, dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any

import numpy as np


@dataclass
class HourlyBaseline:
    hour: int
    people_mean: float
    people_std: float
    vehicles_mean: float
    vehicles_std: float
    animals_mean: float
    animals_std: float
    occupancy_mean: float
    occupancy_std: float
    sample_count: int


@dataclass
class ZoneBaseline:
    zone_id: str
    zone_type: str
    entries_mean: float
    entries_std: float
    dwell_mean: float
    dwell_std: float
    violation_rate: float
    sample_count: int


@dataclass
class Anomaly:
    camera_id: str
    timestamp: datetime
    anomaly_type: str
    severity: float
    details: dict[str, Any]
    baseline_values: dict[str, float]
    observed_values: dict[str, float]


class BehavioralLearner:
    def __init__(
        self,
        db_path: str | Path,
        tenant_id: str = "local",
        learning_window_days: int = 14,
        min_samples_for_baseline: int = 50,
        anomaly_threshold_sigma: float = 3.0,
    ):
        self.db_path = Path(db_path)
        self.tenant_id = tenant_id
        self.learning_window = timedelta(days=learning_window_days)
        self.min_samples = min_samples_for_baseline
        self.anomaly_threshold = anomaly_threshold_sigma

        self.db_path.parent.mkdir(parents=True, exist_ok=True)
        self._init_db()

        self._hourly_cache: dict[str, dict[int, HourlyBaseline]] = {}
        self._zone_cache: dict[str, dict[str, ZoneBaseline]] = {}
        self._last_rebuild: dict[str, datetime] = {}

    def _connect(self) -> sqlite3.Connection:
        db = sqlite3.connect(self.db_path, timeout=30)
        db.row_factory = sqlite3.Row
        db.execute("PRAGMA foreign_keys=ON")
        db.execute("PRAGMA journal_mode=WAL")
        db.execute("PRAGMA synchronous=NORMAL")
        return db

    @contextmanager
    def _db(self):
        db = self._connect()
        try:
            with db:
                yield db
        finally:
            db.close()

    def _init_db(self) -> None:
        with self._db() as db:
            db.executescript("""
                CREATE TABLE IF NOT EXISTS frame_stats (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    tenant_id TEXT NOT NULL,
                    camera_id TEXT NOT NULL,
                    observed_at TEXT NOT NULL,
                    hour_of_day INTEGER NOT NULL,
                    day_of_week INTEGER NOT NULL,
                    people INTEGER NOT NULL,
                    vehicles INTEGER NOT NULL,
                    animals INTEGER NOT NULL,
                    other INTEGER NOT NULL,
                    occupancy_percent REAL NOT NULL,
                    restricted_count INTEGER NOT NULL,
                    unique_tracks INTEGER NOT NULL,
                    avg_confidence REAL NOT NULL
                );
                CREATE INDEX IF NOT EXISTS frame_stats_camera_time
                    ON frame_stats(tenant_id, camera_id, observed_at DESC);

                CREATE TABLE IF NOT EXISTS zone_events (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    tenant_id TEXT NOT NULL,
                    camera_id TEXT NOT NULL,
                    zone_id TEXT NOT NULL,
                    zone_type TEXT NOT NULL,
                    observed_at TEXT NOT NULL,
                    track_id TEXT NOT NULL,
                    label TEXT NOT NULL,
                    dwell_seconds REAL,
                    violation INTEGER NOT NULL CHECK (violation IN (0, 1)),
                    entry_x REAL, entry_y REAL,
                    exit_x REAL, exit_y REAL
                );
                CREATE INDEX IF NOT EXISTS zone_events_camera_time
                    ON zone_events(tenant_id, camera_id, observed_at DESC);

                CREATE TABLE IF NOT EXISTS motion_flow (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    tenant_id TEXT NOT NULL,
                    camera_id TEXT NOT NULL,
                    observed_at TEXT NOT NULL,
                    track_id TEXT NOT NULL,
                    label TEXT NOT NULL,
                    start_x REAL, start_y REAL,
                    end_x REAL, end_y REAL,
                    speed_px_per_sec REAL,
                    direction_deg REAL,
                    duration_sec REAL
                );
                CREATE INDEX IF NOT EXISTS motion_flow_camera_time
                    ON motion_flow(tenant_id, camera_id, observed_at DESC);

                CREATE TABLE IF NOT EXISTS hourly_baselines (
                    tenant_id TEXT NOT NULL,
                    camera_id TEXT NOT NULL,
                    hour INTEGER NOT NULL,
                    people_mean REAL, people_std REAL,
                    vehicles_mean REAL, vehicles_std REAL,
                    animals_mean REAL, animals_std REAL,
                    occupancy_mean REAL, occupancy_std REAL,
                    sample_count INTEGER,
                    updated_at TEXT NOT NULL,
                    PRIMARY KEY (tenant_id, camera_id, hour)
                );

                CREATE TABLE IF NOT EXISTS zone_baselines (
                    tenant_id TEXT NOT NULL,
                    camera_id TEXT NOT NULL,
                    zone_id TEXT NOT NULL,
                    zone_type TEXT NOT NULL,
                    entries_mean REAL, entries_std REAL,
                    dwell_mean REAL, dwell_std REAL,
                    violation_rate REAL,
                    sample_count INTEGER,
                    updated_at TEXT NOT NULL,
                    PRIMARY KEY (tenant_id, camera_id, zone_id)
                );

                CREATE TABLE IF NOT EXISTS anomalies (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    tenant_id TEXT NOT NULL,
                    camera_id TEXT NOT NULL,
                    observed_at TEXT NOT NULL,
                    anomaly_type TEXT NOT NULL,
                    severity REAL NOT NULL,
                    details_json TEXT NOT NULL,
                    baseline_json TEXT NOT NULL,
                    observed_json TEXT NOT NULL,
                    acknowledged INTEGER DEFAULT 0
                );
                CREATE INDEX IF NOT EXISTS anomalies_camera_time
                    ON anomalies(tenant_id, camera_id, observed_at DESC);
            """)

    def ingest_frame(self, frame: dict[str, Any]) -> None:
        camera_id = frame.get("videoId") or frame.get("cameraId")
        if not camera_id:
            return
        observed = frame.get("at") or datetime.now(timezone.utc).isoformat()
        try:
            dt = datetime.fromisoformat(observed.replace("Z", "+00:00"))
        except Exception:
            dt = datetime.now(timezone.utc)

        people = frame.get("people", 0)
        vehicles = frame.get("vehicles", 0)
        animals = frame.get("animals", 0)
        other = frame.get("other", 0)
        occupancy = frame.get("occupancyPercent", 0.0)
        restricted = frame.get("restrictedCount", 0)
        unique_tracks = frame.get("uniqueTracks", 0)
        avg_conf = frame.get("averageConfidence", 0.0)

        with self._db() as db:
            db.execute(
                """INSERT INTO frame_stats(
                    tenant_id, camera_id, observed_at, hour_of_day, day_of_week,
                    people, vehicles, animals, other, occupancy_percent,
                    restricted_count, unique_tracks, avg_confidence
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
                (
                    self.tenant_id, camera_id, dt.isoformat(),
                    dt.hour, dt.weekday(),
                    people, vehicles, animals, other, occupancy,
                    restricted, unique_tracks, avg_conf,
                ),
            )

        detections = frame.get("detections", [])
        for det in detections:
            zone = det.get("zone", "public")
            track_id = det.get("id", "")
            label = det.get("label", "")
            violation = 1 if det.get("violation") else 0
            bbox = det.get("bbox", [0, 0, 0, 0])
            cx, cy = bbox[0] + bbox[2] / 2, bbox[1] + bbox[3] / 2

            with self._db() as db:
                db.execute(
                    """INSERT INTO zone_events(
                        tenant_id, camera_id, zone_id, zone_type, observed_at,
                        track_id, label, violation, entry_x, entry_y
                    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
                    (self.tenant_id, camera_id, zone, "restricted" if zone == "no-zone" else "watch",
                     dt.isoformat(), track_id, label, violation, cx, cy),
                )

    def rebuild_baselines(self, camera_id: str | None = None) -> int:
        cams = [camera_id] if camera_id else self._get_all_camera_ids()
        rebuilt = 0
        for cam in cams:
            if self._rebuild_camera_baselines(cam):
                rebuilt += 1
                self._last_rebuild[cam] = datetime.now(timezone.utc)
        return rebuilt

    def _get_all_camera_ids(self) -> list[str]:
        with self._db() as db:
            rows = db.execute(
                "SELECT DISTINCT camera_id FROM frame_stats WHERE tenant_id = ?",
                (self.tenant_id,)
            ).fetchall()
        return [r["camera_id"] for r in rows]

    def _rebuild_camera_baselines(self, camera_id: str) -> bool:
        cutoff = (datetime.now(timezone.utc) - self.learning_window).isoformat()

        with self._db() as db:
            rows = db.execute(
                """SELECT hour_of_day, people, vehicles, animals, occupancy_percent
                   FROM frame_stats
                   WHERE tenant_id = ? AND camera_id = ? AND observed_at >= ?
                """,
                (self.tenant_id, camera_id, cutoff),
            ).fetchall()

        if len(rows) < self.min_samples:
            return False

        by_hour: dict[int, list[dict]] = defaultdict(list)
        for r in rows:
            by_hour[r["hour_of_day"]].append(dict(r))

        with self._db() as db:
            for hour, samples in by_hour.items():
                if len(samples) < 5:
                    continue
                people = [s["people"] for s in samples]
                vehicles = [s["vehicles"] for s in samples]
                animals = [s["animals"] for s in samples]
                occupancy = [s["occupancy_percent"] for s in samples]

                db.execute(
                    """INSERT OR REPLACE INTO hourly_baselines(
                        tenant_id, camera_id, hour,
                        people_mean, people_std,
                        vehicles_mean, vehicles_std,
                        animals_mean, animals_std,
                        occupancy_mean, occupancy_std,
                        sample_count, updated_at
                    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
                    (
                        self.tenant_id, camera_id, hour,
                        float(np.mean(people)), float(np.std(people, ddof=1) or 1.0),
                        float(np.mean(vehicles)), float(np.std(vehicles, ddof=1) or 1.0),
                        float(np.mean(animals)), float(np.std(animals, ddof=1) or 1.0),
                        float(np.mean(occupancy)), float(np.std(occupancy, ddof=1) or 1.0),
                        len(samples), datetime.now(timezone.utc).isoformat(),
                    ),
                )

        zone_rows = db.execute(
            """SELECT zone_id, zone_type, track_id, violation, dwell_seconds
               FROM zone_events
               WHERE tenant_id = ? AND camera_id = ? AND observed_at >= ?
            """,
            (self.tenant_id, camera_id, cutoff),
        ).fetchall()

        if zone_rows:
            by_zone: dict[str, list[dict]] = defaultdict(list)
            for r in zone_rows:
                by_zone[r["zone_id"]].append(dict(r))

            for zone_id, samples in by_zone.items():
                if len(samples) < 3:
                    continue
                entries = len(set(s["track_id"] for s in samples))
                violations = sum(1 for s in samples if s["violation"])
                dwells = [s["dwell_seconds"] for s in samples if s["dwell_seconds"]]
                zone_type = samples[0]["zone_type"]

                db.execute(
                    """INSERT OR REPLACE INTO zone_baselines(
                        tenant_id, camera_id, zone_id, zone_type,
                        entries_mean, entries_std,
                        dwell_mean, dwell_std,
                        violation_rate, sample_count, updated_at
                    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
                    (
                        self.tenant_id, camera_id, zone_id, zone_type,
                        float(entries), 1.0,
                        float(np.mean(dwells)) if dwells else 0.0,
                        float(np.std(dwells, ddof=1)) if len(dwells) > 1 else 1.0,
                        violations / max(1, entries), len(samples),
                        datetime.now(timezone.utc).isoformat(),
                    ),
                )
        return True

    def check_anomalies(self, frame: dict[str, Any]) -> list[Anomaly]:
        camera_id = frame.get("videoId") or frame.get("cameraId")
        if not camera_id:
            return []

        observed = frame.get("at") or datetime.now(timezone.utc).isoformat()
        try:
            dt = datetime.fromisoformat(observed.replace("Z", "+00:00"))
        except Exception:
            dt = datetime.now(timezone.utc)

        anomalies = []

        with self._db() as db:
            hb = db.execute(
                """SELECT * FROM hourly_baselines
                   WHERE tenant_id = ? AND camera_id = ? AND hour = ?""",
                (self.tenant_id, camera_id, dt.hour),
            ).fetchone()

        if hb:
            people = frame.get("people", 0)
            vehicles = frame.get("vehicles", 0)
            animals = frame.get("animals", 0)
            occupancy = frame.get("occupancyPercent", 0.0)

            for metric, observed_val, mean_key, std_key in [
                ("people", people, "people_mean", "people_std"),
                ("vehicles", vehicles, "vehicles_mean", "vehicles_std"),
                ("animals", animals, "animals_mean", "animals_std"),
                ("occupancy", occupancy, "occupancy_mean", "occupancy_std"),
            ]:
                mean = hb[mean_key]
                std = max(hb[std_key], 1.0)
                if std > 0:
                    zscore = abs(observed_val - mean) / std
                    if zscore >= self.anomaly_threshold:
                        anomalies.append(Anomaly(
                            camera_id=camera_id,
                            timestamp=dt,
                            anomaly_type=f"count_spike_{metric}",
                            severity=min(100.0, zscore * 10),
                            details={"metric": metric, "zscore": round(zscore, 2)},
                            baseline_values={"mean": mean, "std": std},
                            observed_values={metric: observed_val},
                        ))

        detections = frame.get("detections", [])
        zone_counts: dict[str, int] = defaultdict(int)
        for det in detections:
            zone = det.get("zone", "public")
            zone_counts[zone] += 1

        with self._db() as db:
            zbs = db.execute(
                """SELECT * FROM zone_baselines
                   WHERE tenant_id = ? AND camera_id = ?""",
                (self.tenant_id, camera_id),
            ).fetchall()

        for zb in zbs:
            zone_id = zb["zone_id"]
            observed_count = zone_counts.get(zone_id, 0)
            mean = zb["entries_mean"]
            std = max(zb["entries_std"], 1.0)
            zscore = abs(observed_count - mean) / std
            if zscore >= self.anomaly_threshold and observed_count > mean:
                anomalies.append(Anomaly(
                    camera_id=camera_id,
                    timestamp=dt,
                    anomaly_type=f"zone_surge_{zone_id}",
                    severity=min(100.0, zscore * 10),
                    details={"zone": zone_id, "zscore": round(zscore, 2), "zone_type": zb["zone_type"]},
                    baseline_values={"mean": mean, "std": std},
                    observed_values={"count": observed_count},
                ))

            if zb["violation_rate"] > 0:
                current_violations = sum(1 for d in detections if d.get("zone") == zone_id and d.get("violation"))
                expected = zb["violation_rate"] * max(1, zone_counts.get(zone_id, 0))
                if current_violations > expected * 3 and current_violations > 2:
                    anomalies.append(Anomaly(
                        camera_id=camera_id,
                        timestamp=dt,
                        anomaly_type=f"violation_spike_{zone_id}",
                        severity=min(100.0, 50 + current_violations * 5),
                        details={"zone": zone_id, "violations": current_violations, "expected": round(expected, 1)},
                        baseline_values={"violation_rate": zb["violation_rate"]},
                        observed_values={"violations": current_violations},
                    ))

        if anomalies:
            self._store_anomalies(anomalies)

        return anomalies

    def _store_anomalies(self, anomalies: list[Anomaly]) -> None:
        with self._db() as db:
            for a in anomalies:
                db.execute(
                    """INSERT INTO anomalies(
                        tenant_id, camera_id, observed_at, anomaly_type, severity,
                        details_json, baseline_json, observed_json
                    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)""",
                    (
                        self.tenant_id, a.camera_id, a.timestamp.isoformat(),
                        a.anomaly_type, a.severity,
                        json.dumps(a.details), json.dumps(a.baseline_values), json.dumps(a.observed_values),
                    ),
                )

    def get_recent_anomalies(self, camera_id: str | None = None, hours: int = 24, limit: int = 100) -> list[dict]:
        cutoff = (datetime.now(timezone.utc) - timedelta(hours=hours)).isoformat()
        with self._db() as db:
            if camera_id:
                rows = db.execute(
                    """SELECT * FROM anomalies
                       WHERE tenant_id = ? AND camera_id = ? AND observed_at >= ?
                       ORDER BY observed_at DESC LIMIT ?""",
                    (self.tenant_id, camera_id, cutoff, limit),
                ).fetchall()
            else:
                rows = db.execute(
                    """SELECT * FROM anomalies
                       WHERE tenant_id = ? AND observed_at >= ?
                       ORDER BY observed_at DESC LIMIT ?""",
                    (self.tenant_id, cutoff, limit),
                ).fetchall()
        return [dict(r) for r in rows]

    def get_baseline_summary(self, camera_id: str) -> dict[str, Any]:
        with self._db() as db:
            hb = db.execute(
                "SELECT * FROM hourly_baselines WHERE tenant_id = ? AND camera_id = ?",
                (self.tenant_id, camera_id),
            ).fetchall()
            zb = db.execute(
                "SELECT * FROM zone_baselines WHERE tenant_id = ? AND camera_id = ?",
                (self.tenant_id, camera_id),
            ).fetchall()
            stats = db.execute(
                """SELECT COUNT(*) as samples, MIN(observed_at) as first, MAX(observed_at) as last
                   FROM frame_stats WHERE tenant_id = ? AND camera_id = ?""",
                (self.tenant_id, camera_id),
            ).fetchone()
        return {
            "camera_id": camera_id,
            "hourly_baselines": [dict(r) for r in hb],
            "zone_baselines": [dict(r) for r in zb],
            "sample_stats": dict(stats) if stats else {},
            "has_sufficient_data": (stats["samples"] if stats else 0) >= self.min_samples,
        }

    def prune_old_data(self, max_days: int = 30) -> int:
        cutoff = (datetime.now(timezone.utc) - timedelta(days=max_days)).isoformat()
        with self._db() as db:
            for table in ["frame_stats", "zone_events", "motion_flow", "anomalies"]:
                cur = db.execute(
                    f"DELETE FROM {table} WHERE tenant_id = ? AND observed_at < ?",
                    (self.tenant_id, cutoff),
                )
        return 0


class MultiCameraBehavioralEngine:
    def __init__(self, learner: BehavioralLearner):
        self.learner = learner
        self._camera_states: dict[str, dict] = {}

    async def process_frame(self, camera_id: str, frame: dict) -> list[Anomaly]:
        self.learner.ingest_frame(frame)
        anomalies = self.learner.check_anomalies(frame)

        if camera_id not in self._camera_states:
            self._camera_states[camera_id] = {
                "last_rebuild": datetime.now(timezone.utc) - timedelta(hours=2),
                "anomaly_count": 0,
            }
        state = self._camera_states[camera_id]
        state["anomaly_count"] += len(anomalies)

        if (datetime.now(timezone.utc) - state["last_rebuild"]).total_seconds() > 3600:
            self.learner.rebuild_baselines(camera_id)
            state["last_rebuild"] = datetime.now(timezone.utc)

        return anomalies

    def get_fleet_anomaly_summary(self, hours: int = 24) -> dict[str, Any]:
        anomalies = self.learner.get_recent_anomalies(hours=hours)
        by_camera: dict[str, list] = defaultdict(list)
        by_type: dict[str, int] = defaultdict(int)
        for a in anomalies:
            by_camera[a["camera_id"]].append(a)
            by_type[a["anomaly_type"]] += 1

        top_cameras = sorted(
            [(cam, len(anoms)) for cam, anoms in by_camera.items()],
            key=lambda x: -x[1]
        )[:20]

        return {
            "total_anomalies": len(anomalies),
            "by_type": dict(by_type),
            "top_cameras": top_cameras,
            "period_hours": hours,
            "timestamp": datetime.now(timezone.utc).isoformat(),
        }