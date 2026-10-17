#!/usr/bin/env python3
"""GPU YouTube live object detector for the web POC.

The web server uses the repository-local Python 3.11 environment:
  .venv-poc\Scripts\python.exe -m pip install ultralytics opencv-python yt-dlp
  .venv-poc\Scripts\python.exe -m pip install torch torchvision --index-url https://download.pytorch.org/whl/cu130

The script prints one JSON object per analysed frame. The Go web server forwards it
over WebSocket. If dependencies are missing, the web UI falls back to its demo feed.
"""

from __future__ import annotations

import argparse
import base64
from collections import Counter, defaultdict, deque
import json
import time
from datetime import datetime, timezone

from live_clips import ClipRecorder


VEHICLES = {"car", "truck", "bus", "motorcycle", "bicycle"}
ANIMALS = {"bird", "cat", "dog", "horse", "sheep", "cow", "elephant", "bear", "zebra", "giraffe"}
COLORS = {
    "red": ((0, 70, 50), (10, 255, 255)),
    "yellow": ((20, 70, 50), (35, 255, 255)),
    "green": ((36, 50, 40), (85, 255, 255)),
    "blue": ((86, 50, 40), (130, 255, 255)),
    "black": ((0, 0, 0), (180, 255, 55)),
    "white": ((0, 0, 200), (180, 40, 255)),
    "silver": ((0, 0, 80), (180, 45, 199)),
}


def zones():
    return [
        {"id": "crosswalk", "name": "Times Square crosswalk", "type": "watch", "points": [[0.04, 0.72], [0.82, 0.68], [0.96, 0.92], [0.06, 0.95]]},
        {"id": "no-zone", "name": "Restricted no-zone", "type": "restricted", "points": [[0.00, 0.48], [0.24, 0.45], [0.30, 0.74], [0.00, 0.80]]},
    ]


def point_in_polygon(x: float, y: float, points: list[list[float]]) -> bool:
    """Return whether a point is inside or on the boundary of a polygon."""
    inside = False
    for i, (x1, y1) in enumerate(points):
        x2, y2 = points[i - 1]
        cross = (x - x1) * (y2 - y1) - (y - y1) * (x2 - x1)
        if abs(cross) <= 1e-12 and min(x1, x2) <= x <= max(x1, x2) and min(y1, y2) <= y <= max(y1, y2):
            return True
        if (y1 > y) != (y2 > y) and x < (x2 - x1) * (y - y1) / (y2 - y1) + x1:
            inside = not inside
    return inside


def zone_for(x: float, y: float) -> str:
    configured = zones()
    # A restricted polygon wins where zones overlap: missing a restriction is
    # more costly than labelling the same point as a watched crosswalk.
    for zone in configured:
        if zone["type"] == "restricted" and point_in_polygon(x, y, zone["points"]):
            return zone["id"]
    for zone in configured:
        if point_in_polygon(x, y, zone["points"]):
            return zone["id"]
    return "public"


def category(label: str) -> str:
    if label in VEHICLES:
        return "vehicle"
    if label == "person" or label in ANIMALS:
        return "biological"
    return "other"


def vehicle_type(label: str) -> str:
    return label if label in VEHICLES else ""


def dominant_color(cv2, crop) -> str:
    if crop.size == 0:
        return "unknown"
    hsv = cv2.cvtColor(crop, cv2.COLOR_BGR2HSV)
    best = ("unknown", 0)
    for name, (lo, hi) in COLORS.items():
        mask = cv2.inRange(hsv, lo, hi)
        count = int(mask.sum())
        if count > best[1]:
            best = (name, count)
    return best[0]


def stream_url(raw_url: str, cookies: str = "") -> str:
    import yt_dlp
    from yt_dlp.utils import DownloadError

    # Try several YouTube format ladders. Some streams only expose HLS/DASH
    # manifests, some are region- or age-restricted, and the default client
    # is often throttled. Cycling player_clients and formats covers most of it.
    base_opts = {
        "quiet": True,
        "no_warnings": True,
        "skip_download": True,
        "extractor_args": {
            "youtube": {
                "player_client": ["web", "android", "tv_embedded"],
                "player_skip": ["configs", "webpage"],
            }
        },
    }
    if cookies:
        base_opts["cookiefile"] = cookies

    formats = [
        "270/232/231/best",
        "bestvideo[ext=mp4]+bestaudio[ext=m4a]/bestvideo+bestaudio/best",
        "best",
    ]
    last_err: Exception | None = None
    for fmt in formats:
        opts = dict(base_opts)
        opts["format"] = fmt
        try:
            with yt_dlp.YoutubeDL(opts) as ydl:
                info = ydl.extract_info(raw_url, download=False)
        except DownloadError as exc:
            last_err = exc
            continue
        except Exception as exc:  # noqa: BLE001 - report and try the next ladder
            last_err = exc
            continue
        url = info.get("url") or info.get("playback_url") or info.get("manifest_url")
        if url:
            return url
    raise RuntimeError(
        f"could not extract a playable stream URL from YouTube: {last_err}"
    )


def emit(obj) -> None:
    print(json.dumps(obj, separators=(",", ":")), flush=True)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", required=True)
    parser.add_argument("--query", default="")
    parser.add_argument("--model", default="yolo11x.pt")
    parser.add_argument("--every", type=int, default=1, help="analyse every Nth decoded frame")
    parser.add_argument("--imgsz", type=int, default=1920, help="detector input size; 1920 preserves the native live frame")
    parser.add_argument("--confidence", type=float, default=0.20)
    parser.add_argument("--max-frames", type=int, default=0, help="stop after N analysed frames; zero runs forever")
    parser.add_argument("--video-id", default="stream")
    parser.add_argument("--clip-dir", default="dist/live-clips")
    parser.add_argument("--clip-seconds", type=float, default=8.0)
    parser.add_argument("--cookies", default="", help="Netscape cookies.txt path for age/region-restricted YouTube streams")
    args = parser.parse_args()
    query = args.query.lower().strip()

    try:
        import cv2
        import numpy as np
        from ultralytics import YOLO
        import torch
    except Exception as exc:
        emit({"backend": "gpu-yolo", "detectorNote": f"missing dependency: {exc}", "at": datetime.now(timezone.utc).isoformat(), "zones": zones(), "detections": [], "alerts": []})
        return 2

    device = 0 if torch.cuda.is_available() else "cpu"
    if device == "cpu":
        raise RuntimeError("CUDA is required for the live high-accuracy detector")
    torch.backends.cudnn.benchmark = True
    torch.backends.cuda.matmul.allow_tf32 = True
    model = YOLO(args.model)
    # Pay model compilation and CUDA allocation before reading the live stream.
    warmup_frame = np.zeros((args.imgsz, args.imgsz, 3), dtype=np.uint8)
    model.predict(warmup_frame, imgsz=args.imgsz, device=device, half=True, verbose=False)
    cap = cv2.VideoCapture(stream_url(args.url, args.cookies))
    if not cap.isOpened():
        emit({"backend": "gpu-yolo", "detectorNote": "could not open YouTube stream", "at": datetime.now(timezone.utc).isoformat(), "zones": zones(), "detections": [], "alerts": []})
        return 3

    source_fps = float(cap.get(cv2.CAP_PROP_FPS) or 0)
    clip_recorder = ClipRecorder(cv2, args.clip_dir, args.video_id, source_fps, args.clip_seconds)
    pending_clip = None

    frame_no = 0
    analysed = 0
    read_failures = 0
    emitted_at = deque(maxlen=30)
    color_votes = defaultdict(lambda: deque(maxlen=12))
    prior_zones = {}
    observed_tracks = set()
    while True:
        ok, frame = cap.read()
        if not ok:
            read_failures += 1
            if read_failures >= 60:
                raise RuntimeError("live stream stopped producing frames")
            time.sleep(0.5)
            continue
        read_failures = 0
        frame_no += 1
        if clip_recorder.active:
            pending_clip = clip_recorder.write(frame) or pending_clip
        if frame_no % args.every:
            continue

        height, width = frame.shape[:2]
        inference_started = time.perf_counter()
        result = model.track(frame, imgsz=args.imgsz, conf=args.confidence, iou=0.5, device=device, half=True, persist=True, tracker="bytetrack.yaml", verbose=False)[0]
        inference_ms = (time.perf_counter() - inference_started) * 1000
        analysed += 1
        detections = []
        for i, box in enumerate(result.boxes):
            cls = int(box.cls[0])
            label = result.names[cls]
            conf = float(box.conf[0])
            x1, y1, x2, y2 = [float(v) for v in box.xyxy[0]]
            nx, ny, nw, nh = x1 / width, y1 / height, (x2 - x1) / width, (y2 - y1) / height
            z = zone_for(nx + nw / 2, ny + nh / 2)
            cat = category(label)
            track_id = int(box.id[0]) if box.id is not None else i
            observed_tracks.add(f"{label}-{track_id}")
            if cat == "vehicle":
                color_votes[track_id].append(dominant_color(cv2, frame[max(0, int(y1)):max(0, int(y2)), max(0, int(x1)):max(0, int(x2))]))
                color = Counter(color_votes[track_id]).most_common(1)[0][0]
            else:
                color = ""
            entered_zone = z == "no-zone" and prior_zones.get(track_id) != "no-zone"
            prior_zones[track_id] = z
            detections.append({
                "id": f"{label}-{track_id}", "label": (f"{color} {label}" if color else label), "category": cat,
                "color": color, "vehicleType": vehicle_type(label), "confidence": conf, "bbox": [nx, ny, nw, nh], "zone": z,
                "violation": entered_zone and cat in {"vehicle", "biological"},
            })

        alerts = [{"id": "alert-" + d["id"], "severity": "high", "rule": "no-zone-entry", "message": d["label"] + " entered Restricted no-zone", "at": datetime.now(timezone.utc).isoformat()} for d in detections if d["violation"]][:6]
        if alerts and not clip_recorder.active:
            pending_clip = clip_recorder.start(frame) or pending_clip
        hits = [d for d in detections if query and query in (d["label"] + " " + d["category"] + " " + d.get("color", "") + " " + d.get("vehicleType", "") + " " + d["zone"]).lower()]
        avg_confidence = sum(d["confidence"] for d in detections) / len(detections) if detections else 0
        occupancy = min(1.0, sum(d["bbox"][2] * d["bbox"][3] for d in detections))
        preview = frame
        if width > 1280:
            preview = cv2.resize(frame, (1280, round(height * 1280 / width)), interpolation=cv2.INTER_AREA)
        encode_started = time.perf_counter()
        ok, encoded = cv2.imencode(".jpg", preview, [cv2.IMWRITE_JPEG_QUALITY, 68])
        encode_ms = (time.perf_counter() - encode_started) * 1000
        image_data = "data:image/jpeg;base64," + base64.b64encode(encoded).decode("ascii") if ok else ""
        emitted_at.append(time.perf_counter())
        analysis_fps = (len(emitted_at) - 1) / max(0.001, emitted_at[-1] - emitted_at[0]) if len(emitted_at) > 1 else 0
        payload = {
            "backend": "gpu-yolo", "frame": frame_no, "at": datetime.now(timezone.utc).isoformat(),
            "people": sum(1 for d in detections if d["label"].endswith("person")),
            "vehicles": sum(1 for d in detections if d["category"] == "vehicle"),
            "animals": sum(1 for d in detections if d["label"] in ANIMALS),
            "other": sum(1 for d in detections if d["category"] == "other"),
            "detections": detections, "zones": zones(), "alerts": alerts, "searchHits": hits,
            "imageData": image_data, "inferenceMs": round(inference_ms, 1), "encodeMs": round(encode_ms, 1),
            "analysisFps": round(analysis_fps, 2), "sourceWidth": width, "sourceHeight": height,
            "transportKB": round(len(image_data) / 1024, 1),
            "profile": f"yolo11x-{args.imgsz}", "uniqueTracks": len(observed_tracks),
            "restrictedCount": sum(1 for d in detections if d["zone"] == "no-zone"),
            "averageConfidence": round(avg_confidence, 4), "occupancyPercent": round(occupancy * 100, 1),
            "clipState": clip_recorder.state,
        }
        if clip_recorder.error:
            payload["clipError"] = clip_recorder.error
        if pending_clip:
            payload["clip"] = pending_clip
            pending_clip = None
        emit(payload)
        if args.max_frames and analysed >= args.max_frames:
            break

    final_clip = clip_recorder.close()
    if final_clip:
        emit({
            "backend": "gpu-yolo", "frame": frame_no, "at": datetime.now(timezone.utc).isoformat(),
            "people": 0, "vehicles": 0, "animals": 0, "other": 0,
            "detections": [], "zones": zones(), "alerts": [], "searchHits": [],
            "imageData": "", "inferenceMs": 0, "encodeMs": 0, "analysisFps": 0,
            "sourceWidth": 0, "sourceHeight": 0, "transportKB": 0,
            "profile": f"yolo11x-{args.imgsz}", "uniqueTracks": len(observed_tracks),
            "restrictedCount": 0, "averageConfidence": 0, "occupancyPercent": 0,
            "clipState": "stored", "clip": final_clip,
        })
    cap.release()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
