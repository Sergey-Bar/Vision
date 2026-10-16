#!/usr/bin/env python3
"""Summarize the lab's own knowledge store as one JSON object on stdout.

The showcase hub shells out to this tool, so the stdout shape is a contract:
camelCase key names, one pass over ``observations`` (each payload is parsed
once), and every database opened read-only. The exit code is 0 whenever a
report was produced, including ``"available": false``; 2 only when no report
can be produced at all.

Run:  py -3 tools/record_brief.py [--store PATH] [--behavioral PATH]
"""

from __future__ import annotations

import argparse
import json
import os
import sqlite3
import sys
from collections import Counter
from datetime import datetime, timezone

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DEFAULT_STORE = os.path.join(ROOT, "dist", "live-poc.sqlite3")
DEFAULT_BEHAVIORAL = os.path.join(ROOT, "dist", "behavioral.sqlite3")

# observations column -> key stem, whether the contract has an Avg/Max field.
SERIES_COLUMNS = (
    ("people", "people", True, True),
    ("vehicles", "vehicles", True, True),
    ("animals", "animals", False, True),
    ("restricted_count", "restricted", True, True),
    ("occupancy_percent", "occupancy", True, False),
)

LEARNER_TABLES = (
    "frame_stats",
    "zone_events",
    "motion_flow",
    "hourly_baselines",
    "zone_baselines",
    "anomalies",
)

ALERT_MESSAGE_LIMIT = 8


def _open_readonly(path: str) -> sqlite3.Connection:
    """Open ``path`` read-only; never creates a file and never writes."""
    db = sqlite3.connect("file:" + path + "?mode=ro", uri=True)
    db.execute("PRAGMA query_only = ON")
    return db


def _short_reason(exc: BaseException) -> str:
    text = " ".join(str(exc).split())
    return text or type(exc).__name__


def _payload_alerts(raw: object) -> list | None:
    """Return the payload's alerts list, or None when it is unusable."""
    try:
        payload = json.loads(raw)
    except (TypeError, ValueError):
        return None
    if not isinstance(payload, dict):
        return None
    alerts = payload.get("alerts")
    if not isinstance(alerts, list):
        return None
    return alerts


def _count_table(db: sqlite3.Connection, name: str) -> int:
    try:
        return db.execute("SELECT COUNT(*) FROM " + name).fetchone()[0]
    except sqlite3.Error:
        return 0


def _summarize_store(db: sqlite3.Connection, store_path: str, generated_at: str) -> dict:
    """Build the report body from one pass over the observations table."""
    db.row_factory = sqlite3.Row
    columns = {row[1] for row in db.execute("PRAGMA table_info(observations)")}
    if "observed_at" not in columns:
        raise sqlite3.OperationalError("observations table is missing observed_at")

    series_present = [entry for entry in SERIES_COLUMNS if entry[0] in columns]
    has_video = "video_id" in columns
    has_payload = "payload_json" in columns

    select = ["observed_at"]
    if has_video:
        select.append("video_id")
    select.extend(column for column, *_ in series_present)
    if has_payload:
        select.append("payload_json")
    sql = "SELECT " + ", ".join(select) + " FROM observations"

    frames = 0
    window_from = None
    window_to = None
    videos: Counter[str] = Counter()
    buckets: dict[str, dict] = {}
    alert_rules: Counter[str] = Counter()
    alert_messages: Counter[str] = Counter()

    for row in db.execute(sql):
        frames += 1
        observed = row["observed_at"]
        if has_video and row["video_id"] is not None:
            videos[row["video_id"]] += 1
        if observed is None:
            continue
        if window_from is None or observed < window_from:
            window_from = observed
        if window_to is None or observed > window_to:
            window_to = observed

        minute = observed[:16]
        bucket = buckets.get(minute)
        if bucket is None:
            bucket = buckets[minute] = {
                "frames": 0,
                "alerts": 0,
                "sums": {column: 0.0 for column, *_ in series_present},
                "counts": {column: 0 for column, *_ in series_present},
                "maxes": {column: None for column, *_ in series_present},
            }
        bucket["frames"] += 1

        for column, *_ in series_present:
            value = row[column]
            if value is None:
                continue
            bucket["sums"][column] += value
            bucket["counts"][column] += 1
            top = bucket["maxes"][column]
            if top is None or value > top:
                bucket["maxes"][column] = value

        if has_payload:
            alerts = _payload_alerts(row["payload_json"])
            if alerts:
                bucket["alerts"] += len(alerts)
                for alert in alerts:
                    if not isinstance(alert, dict):
                        continue
                    rule = alert.get("rule")
                    if isinstance(rule, str):
                        alert_rules[rule] += 1
                    message = alert.get("message")
                    if isinstance(message, str):
                        alert_messages[message] += 1

    series = []
    for minute in sorted(buckets):
        bucket = buckets[minute]
        entry: dict[str, object] = {"minute": minute, "frames": bucket["frames"]}
        for column, stem, has_avg, has_max in series_present:
            if has_avg:
                count = bucket["counts"][column]
                entry[stem + "Avg"] = round(bucket["sums"][column] / count, 1) if count else None
            if has_max:
                entry[stem + "Max"] = bucket["maxes"][column]
        entry["alerts"] = bucket["alerts"]
        series.append(entry)

    video_id = videos.most_common(1)[0][0] if videos else None

    return {
        "available": True,
        "storePath": store_path,
        "generatedAt": generated_at,
        "videoId": video_id,
        "videoCount": len(videos),
        "windowFrom": window_from,
        "windowTo": window_to,
        "frames": frames,
        "series": series,
        "alertRules": dict(alert_rules),
        "alertMessages": dict(alert_messages.most_common(ALERT_MESSAGE_LIMIT)),
        "clips": _count_table(db, "clips"),
        "entities": _count_table(db, "entities"),
        "relations": _count_table(db, "relations"),
    }


def _learner_report(path: str) -> dict:
    entry: dict[str, object] = {"path": path, "present": False, "rows": {}, "reason": ""}
    try:
        db = _open_readonly(path)
    except sqlite3.Error as exc:
        entry["reason"] = _short_reason(exc)
        return entry
    try:
        existing = {row[0] for row in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}
        entry["present"] = True
        entry["rows"] = {name: _count_table(db, name) for name in LEARNER_TABLES if name in existing}
    except sqlite3.Error as exc:
        entry["reason"] = _short_reason(exc)
    finally:
        db.close()
    return entry


def build_report(store_path: str, behavioral_path: str) -> dict:
    generated_at = datetime.now(timezone.utc).isoformat()
    try:
        db = _open_readonly(store_path)
    except sqlite3.Error as exc:
        return {"available": False, "storePath": store_path, "generatedAt": generated_at, "reason": _short_reason(exc)}
    try:
        report = _summarize_store(db, store_path, generated_at)
    except sqlite3.Error as exc:
        return {"available": False, "storePath": store_path, "generatedAt": generated_at, "reason": _short_reason(exc)}
    finally:
        db.close()
    report["learner"] = _learner_report(behavioral_path)
    return report


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Emit one JSON brief of the live POC knowledge store.")
    parser.add_argument("--store", default=DEFAULT_STORE, help="knowledge store database (opened read-only)")
    parser.add_argument("--behavioral", default=DEFAULT_BEHAVIORAL, help="behavioral learner database (opened read-only)")
    args = parser.parse_args(argv)
    try:
        report = build_report(args.store, args.behavioral)
    except Exception as exc:  # last resort: cannot even start
        print(f"record_brief: cannot produce a report: {exc}", file=sys.stderr)
        return 2
    print(json.dumps(report, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
