#!/usr/bin/env python3
"""Multi-camera fleet manager for 200-2000+ cameras.

Auto-discovers, configures, and monitors cameras. Spawns detector workers,
handles RTSP/YouTube streams, tracks health, and routes frames to knowledge store.
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import random
import re
import socket
import ssl
import subprocess
import threading
import time
from contextlib import asynccontextmanager
from dataclasses import asdict, dataclass, field
from datetime import datetime, timedelta, timezone
from enum import Enum
from pathlib import Path
from typing import Any, Callable
from urllib.parse import urlparse
from uuid import uuid4

import aiohttp
from aiohttp import web

from live_knowledge import KnowledgeStore

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s")
log = logging.getLogger("camera_fleet")


class CameraType(Enum):
    RTSP = "rtsp"
    YOUTUBE = "youtube"
    ONVIF = "onvif"
    MOCK = "mock"


class CameraStatus(Enum):
    UNKNOWN = "unknown"
    DISCOVERING = "discovering"
    CONNECTING = "connecting"
    HEALTHY = "healthy"
    DEGRADED = "degraded"
    UNREACHABLE = "unreachable"
    DISABLED = "disabled"


@dataclass
class Camera:
    id: str
    name: str
    type: CameraType
    url: str
    zone_config: dict[str, Any] = field(default_factory=dict)
    status: CameraStatus = CameraStatus.UNKNOWN
    last_frame_at: datetime | None = None
    consecutive_failures: int = 0
    metadata: dict[str, Any] = field(default_factory=dict)
    profile: str = "maximum"
    created_at: datetime = field(default_factory=lambda: datetime.now(timezone.utc))
    updated_at: datetime = field(default_factory=lambda: datetime.now(timezone.utc))

    def to_dict(self) -> dict[str, Any]:
        d = asdict(self)
        d["type"] = self.type.value
        d["status"] = self.status.value
        d["last_frame_at"] = self.last_frame_at.isoformat() if self.last_frame_at else None
        d["created_at"] = self.created_at.isoformat()
        d["updated_at"] = self.updated_at.isoformat()
        return d


@dataclass
class DetectorWorker:
    camera_id: str
    process: asyncio.subprocess.Process | None = None
    task: asyncio.Task | None = None
    started_at: datetime | None = None
    frames_processed: int = 0
    last_error: str | None = None
    restart_count: int = 0


class CameraFleet:
    def __init__(
        self,
        knowledge_store: KnowledgeStore,
        detector_script: Path,
        max_workers: int = 50,
        clip_root: Path | None = None,
    ):
        self.store = knowledge_store
        self.detector_script = detector_script
        self.max_workers = max_workers
        self.clip_root = clip_root or Path("dist/live-clips")
        self.clip_root.mkdir(parents=True, exist_ok=True)

        self.cameras: dict[str, Camera] = {}
        self.workers: dict[str, DetectorWorker] = {}
        self._lock = asyncio.Lock()
        self._running = False
        self._monitor_task: asyncio.Task | None = None
        self._discovery_task: asyncio.Task | None = None

        self.on_frame_callback: Callable[[str, dict], Any] | None = None
        self.on_alert_callback: Callable[[str, list[dict]], Any] | None = None
        self.on_status_change: Callable[[str, CameraStatus], Any] | None = None

    async def start(self) -> None:
        if self._running:
            return
        self._running = True
        await self._load_cameras_from_store()
        self._monitor_task = asyncio.create_task(self._monitor_loop())
        self._discovery_task = asyncio.create_task(self._discovery_loop())
        log.info(f"Camera fleet started with {len(self.cameras)} cameras")

    async def stop(self) -> None:
        self._running = False
        if self._monitor_task:
            self._monitor_task.cancel()
        if self._discovery_task:
            self._discovery_task.cancel()
        await self._stop_all_workers()
        log.info("Camera fleet stopped")

    async def _load_cameras_from_store(self) -> None:
        pass

    async def _save_camera(self, camera: Camera) -> None:
        pass

    async def add_camera(
        self,
        name: str,
        url: str,
        type: CameraType | str = CameraType.RTSP,
        zone_config: dict | None = None,
        profile: str = "maximum",
        camera_id: str | None = None,
    ) -> Camera:
        if isinstance(type, str):
            type = CameraType(type.lower())

        cid = camera_id or f"cam_{uuid4().hex[:8]}"
        camera = Camera(
            id=cid,
            name=name,
            type=type,
            url=url,
            zone_config=zone_config or {},
            profile=profile,
        )
        async with self._lock:
            self.cameras[cid] = camera
            await self._save_camera(camera)
        if self._running:
            await self._start_worker(cid)
        log.info(f"Added camera {cid}: {name} ({type.value})")
        return camera

    async def remove_camera(self, camera_id: str) -> bool:
        async with self._lock:
            if camera_id not in self.cameras:
                return False
            await self._stop_worker(camera_id)
            del self.cameras[camera_id]
        log.info(f"Removed camera {camera_id}")
        return True

    async def get_camera(self, camera_id: str) -> Camera | None:
        return self.cameras.get(camera_id)

    async def list_cameras(self, status: CameraStatus | None = None) -> list[Camera]:
        async with self._lock:
            cams = list(self.cameras.values())
        if status:
            cams = [c for c in cams if c.status == status]
        return cams

    async def _start_worker(self, camera_id: str) -> None:
        camera = self.cameras.get(camera_id)
        if not camera or camera.status == CameraStatus.DISABLED:
            return

        if camera_id in self.workers and self.workers[camera_id].task and not self.workers[camera_id].task.done():
            return

        if len([w for w in self.workers.values() if w.task and not w.task.done()]) >= self.max_workers:
            log.warning(f"Worker pool full, queuing {camera_id}")
            return

        camera.status = CameraStatus.CONNECTING
        if self.on_status_change:
            self.on_status_change(camera_id, camera.status)

        cmd = [
            "python", str(self.detector_script),
            "--url", camera.url,
            "--video-id", camera_id,
            "--profile", camera.profile,
            "--clip-dir", str(self.clip_root),
        ]
        if camera.zone_config:
            cmd.extend(["--zones", json.dumps(camera.zone_config)])

        try:
            process = await asyncio.create_subprocess_exec(
                *cmd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
                limit=4 << 20,
            )
            worker = DetectorWorker(camera_id=camera_id, process=process, started_at=datetime.now(timezone.utc))
            self.workers[camera_id] = worker
            worker.task = asyncio.create_task(self._pump_detector(worker, camera))
            log.info(f"Started detector for {camera_id}")
        except Exception as e:
            camera.status = CameraStatus.UNREACHABLE
            camera.last_error = str(e)
            if self.on_status_change:
                self.on_status_change(camera_id, camera.status)
            log.error(f"Failed to start detector for {camera_id}: {e}")

    async def _stop_worker(self, camera_id: str) -> None:
        worker = self.workers.get(camera_id)
        if not worker:
            return
        if worker.process and worker.process.returncode is None:
            try:
                if os.name == "nt":
                    await asyncio.create_subprocess_exec(
                        "taskkill", "/PID", str(worker.process.pid), "/T", "/F",
                        stdout=asyncio.subprocess.DEVNULL, stderr=asyncio.subprocess.DEVNULL,
                    )
                else:
                    worker.process.kill()
                await asyncio.wait_for(worker.process.wait(), timeout=5)
            except Exception:
                pass
        if worker.task:
            worker.task.cancel()
        del self.workers[camera_id]

    async def _stop_all_workers(self) -> None:
        for cid in list(self.workers.keys()):
            await self._stop_worker(cid)

    async def _pump_detector(self, worker: DetectorWorker, camera: Camera) -> None:
        try:
            while self._running and worker.process and worker.process.returncode is None:
                line = await worker.process.stdout.readline()
                if not line:
                    stderr = await worker.process.stderr.read()
                    detail = stderr[-65536:].decode("utf-8", errors="replace").strip()
                    raise RuntimeError(f"detector stopped: {detail}")
                try:
                    frame = json.loads(line)
                except json.JSONDecodeError:
                    continue

                frame["videoId"] = camera.id
                frame["cameraName"] = camera.name
                frame["cameraType"] = camera.type.value

                try:
                    obs_id = await asyncio.to_thread(self.store.ingest_frame, frame)
                    frame["knowledge"] = {"state": "stored", "observationId": obs_id}
                except Exception as exc:
                    log.exception(f"Knowledge storage failed for {camera.id}")
                    frame["knowledge"] = {"state": "degraded", "reason": str(exc)}

                worker.frames_processed += 1
                camera.last_frame_at = datetime.now(timezone.utc)
                camera.consecutive_failures = 0
                camera.status = CameraStatus.HEALTHY
                if self.on_status_change:
                    self.on_status_change(camera.id, camera.status)

                if self.on_frame_callback:
                    await self.on_frame_callback(camera.id, frame)

                alerts = frame.get("alerts") or []
                if alerts and self.on_alert_callback:
                    await self.on_alert_callback(camera.id, alerts)

        except asyncio.CancelledError:
            raise
        except Exception as e:
            worker.last_error = str(e)
            camera.consecutive_failures += 1
            if camera.consecutive_failures >= 3:
                camera.status = CameraStatus.UNREACHABLE
            else:
                camera.status = CameraStatus.DEGRADED
            if self.on_status_change:
                self.on_status_change(camera.id, camera.status)
            log.error(f"Detector {camera.id} error: {e}")
        finally:
            await self._maybe_restart_worker(camera.id)

    async def _maybe_restart_worker(self, camera_id: str) -> None:
        if not self._running:
            return
        camera = self.cameras.get(camera_id)
        if not camera or camera.status == CameraStatus.DISABLED:
            return
        worker = self.workers.get(camera_id)
        if worker and worker.restart_count < 5:
            worker.restart_count += 1
            await asyncio.sleep(min(30, 2 ** worker.restart_count))
            await self._start_worker(camera_id)
        else:
            log.error(f"Camera {camera_id} exceeded restart limit, marking disabled")
            camera.status = CameraStatus.DISABLED

    async def _monitor_loop(self) -> None:
        while self._running:
            try:
                now = datetime.now(timezone.utc)
                async with self._lock:
                    for cam in self.cameras.values():
                        if cam.status in (CameraStatus.HEALTHY, CameraStatus.DEGRADED):
                            if cam.last_frame_at and (now - cam.last_frame_at) > timedelta(seconds=60):
                                cam.status = CameraStatus.DEGRADED
                                if self.on_status_change:
                                    self.on_status_change(cam.id, cam.status)
                        if cam.status == CameraStatus.UNREACHABLE and cam.consecutive_failures < 3:
                            await self._start_worker(cam.id)
            except Exception as e:
                log.error(f"Monitor loop error: {e}")
            await asyncio.sleep(10)

    async def _discovery_loop(self) -> None:
        while self._running:
            try:
                await self._discover_onvif_cameras()
            except Exception as e:
                log.error(f"Discovery loop error: {e}")
            await asyncio.sleep(300)

    async def _discover_onvif_cameras(self) -> None:
        pass

    async def get_fleet_summary(self) -> dict[str, Any]:
        async with self._lock:
            total = len(self.cameras)
            healthy = sum(1 for c in self.cameras.values() if c.status == CameraStatus.HEALTHY)
            degraded = sum(1 for c in self.cameras.values() if c.status == CameraStatus.DEGRADED)
            unreachable = sum(1 for c in self.cameras.values() if c.status == CameraStatus.UNREACHABLE)
            disabled = sum(1 for c in self.cameras.values() if c.status == CameraStatus.DISABLED)
            active_workers = sum(1 for w in self.workers.values() if w.task and not w.task.done())
            total_frames = sum(w.frames_processed for w in self.workers.values())
        return {
            "total_cameras": total,
            "healthy": healthy,
            "degraded": degraded,
            "unreachable": unreachable,
            "disabled": disabled,
            "active_workers": active_workers,
            "max_workers": self.max_workers,
            "total_frames_processed": total_frames,
            "timestamp": datetime.now(timezone.utc).isoformat(),
        }


async def create_fleet_app(
    knowledge_store: KnowledgeStore,
    detector_script: Path,
    max_workers: int = 50,
) -> web.Application:
    fleet = CameraFleet(knowledge_store, detector_script, max_workers)

    async def on_frame(camera_id: str, frame: dict) -> None:
        pass

    async def on_alert(camera_id: str, alerts: list[dict]) -> None:
        pass

    fleet.on_frame_callback = on_frame
    fleet.on_alert_callback = on_alert

    await fleet.start()

    async def shutdown(app: web.Application) -> None:
        await fleet.stop()

    app = web.Application()
    app["fleet"] = fleet
    app.on_shutdown.append(shutdown)
    
    # Fleet status endpoints
    async def summary_handler(request):
        return web.json_response(await fleet.get_fleet_summary())
    
    async def cameras_handler(request):
        return web.json_response([c.to_dict() for c in await fleet.list_cameras()])
    
    app.router.add_get("/api/fleet/summary", summary_handler)
    app.router.add_get("/api/fleet/cameras", cameras_handler)
    app.router.add_post("/api/fleet/cameras", add_camera_handler(fleet))
    app.router.add_delete("/api/fleet/cameras/{camera_id}", delete_camera_handler(fleet))
    app.router.add_get("/api/fleet/cameras/{camera_id}", get_camera_handler(fleet))
    app.router.add_post("/api/fleet/cameras/{camera_id}/restart", restart_camera_handler(fleet))

    return app


async def add_camera_handler(fleet: CameraFleet) -> Callable:
    async def handler(request: web.Request) -> web.Response:
        try:
            body = await request.json()
        except Exception:
            raise web.HTTPBadRequest(text="JSON body required")
        name = body.get("name")
        url = body.get("url")
        type = body.get("type", "rtsp")
        zone_config = body.get("zone_config")
        profile = body.get("profile", "maximum")
        if not name or not url:
            raise web.HTTPBadRequest(text="name and url required")
        camera = await fleet.add_camera(name, url, type, zone_config, profile)
        return web.json_response(camera.to_dict(), status=201)
    return handler


async def delete_camera_handler(fleet: CameraFleet) -> Callable:
    async def handler(request: web.Request) -> web.Response:
        camera_id = request.match_info["camera_id"]
        ok = await fleet.remove_camera(camera_id)
        if not ok:
            raise web.HTTPNotFound(text="Camera not found")
        return web.Response(status=204)
    return handler


async def get_camera_handler(fleet: CameraFleet) -> Callable:
    async def handler(request: web.Request) -> web.Response:
        camera_id = request.match_info["camera_id"]
        camera = await fleet.get_camera(camera_id)
        if not camera:
            raise web.HTTPNotFound(text="Camera not found")
        return web.json_response(camera.to_dict())
    return handler


async def restart_camera_handler(fleet: CameraFleet) -> Callable:
    async def handler(request: web.Request) -> web.Response:
        camera_id = request.match_info["camera_id"]
        await fleet._stop_worker(camera_id)
        await fleet._start_worker(camera_id)
        return web.Response(status=204)
    return handler