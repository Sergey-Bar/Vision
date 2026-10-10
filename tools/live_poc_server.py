#!/usr/bin/env python3
"""Local-only HTTP/WebSocket server for the live YouTube analytics POC.
Extended to support multi-camera fleet management, behavioral learning,
autonomous operator, and conversational search."""

from __future__ import annotations

import argparse
import asyncio
import json
import logging
import os
from pathlib import Path
import re
import sqlite3
import sys
from urllib.parse import urlparse

from aiohttp import WSMsgType, web

from live_knowledge import KnowledgeStore
from camera_fleet import CameraFleet, create_fleet_app
from behavioral_learning import BehavioralLearner
from autonomous_operator import AutonomousOperator, create_operator_app
from conversational_search import ConversationalSearch, create_conversational_search_app


def add_camera_handler(fleet: CameraFleet) -> Callable:
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


def delete_camera_handler(fleet: CameraFleet) -> Callable:
    async def handler(request: web.Request) -> web.Response:
        camera_id = request.match_info["camera_id"]
        ok = await fleet.remove_camera(camera_id)
        if not ok:
            raise web.HTTPNotFound(text="Camera not found")
        return web.Response(status=204)
    return handler


def get_camera_handler(fleet: CameraFleet) -> Callable:
    async def handler(request: web.Request) -> web.Response:
        camera_id = request.match_info["camera_id"]
        camera = await fleet.get_camera(camera_id)
        if not camera:
            raise web.HTTPNotFound(text="Camera not found")
        return web.json_response(camera.to_dict())
    return handler


def restart_camera_handler(fleet: CameraFleet) -> Callable:
    async def handler(request: web.Request) -> web.Response:
        camera_id = request.match_info["camera_id"]
        await fleet._stop_worker(camera_id)
        await fleet._start_worker(camera_id)
        return web.Response(status=204)
    return handler


ROOT = Path(__file__).resolve().parents[1]
STATIC = ROOT / "web" / "static"
DETECTOR = ROOT / "tools" / "youtube_live_detector.py"
VIDEO_ID = re.compile(r"(?:youtube\.com/watch\?v=|youtu\.be/|youtube\.com/embed/|youtube\.com/live/)([A-Za-z0-9_-]{11})|^([A-Za-z0-9_-]{11})$")
KNOWLEDGE_STORE = web.AppKey("knowledge_store", KnowledgeStore)
FLEET = web.AppKey("fleet", CameraFleet)
BEHAVIORAL_LEARNER = web.AppKey("behavioral_learner", BehavioralLearner)
AUTONOMOUS_OPERATOR = web.AppKey("autonomous_operator", AutonomousOperator)
CONVERSATIONAL_SEARCH = web.AppKey("conversational_search", ConversationalSearch)


class DetectorSession:
    def __init__(self, ws: web.WebSocketResponse, process: asyncio.subprocess.Process):
        self.ws = ws
        self.process = process
        self._stop_lock = asyncio.Lock()
        self._stopped = False

    async def stop(self) -> None:
        async with self._stop_lock:
            if self._stopped:
                return
            self._stopped = True
            if self.process.returncode is not None:
                return
            if os.name == "nt":
                killer = await asyncio.create_subprocess_exec(
                    "taskkill", "/PID", str(self.process.pid), "/T", "/F",
                    stdout=asyncio.subprocess.DEVNULL, stderr=asyncio.subprocess.DEVNULL,
                )
                await killer.wait()
            else:
                self.process.kill()
            try:
                await asyncio.wait_for(self.process.wait(), timeout=5)
            except TimeoutError:
                self.process.kill()


class ServerState:
    def __init__(self) -> None:
        self.lock = asyncio.Lock()
        self.active: DetectorSession | None = None

    async def replace(self, session: DetectorSession) -> None:
        async with self.lock:
            previous = self.active
            self.active = session
        if previous is not None:
            await previous.ws.close(code=4001, message=b"analysis moved to another tab")
            await previous.stop()

    async def clear(self, session: DetectorSession) -> None:
        async with self.lock:
            if self.active is session:
                self.active = None


STATE = ServerState()


@web.middleware
async def no_cache(request: web.Request, handler):
    response = await handler(request)
    if not response.prepared:
        response.headers["Cache-Control"] = "no-store"
    return response


def extract_video_id(raw: str) -> str:
    match = VIDEO_ID.search(raw.strip())
    if not match:
        raise ValueError("invalid YouTube video id or URL")
    return match.group(1) or match.group(2)


def allowed_origin(request: web.Request) -> bool:
    origin = request.headers.get("Origin", "")
    if not origin:
        return True
    parsed = urlparse(origin)
    return parsed.scheme in {"http", "https"} and parsed.hostname in {"localhost", "127.0.0.1"}


async def index(_: web.Request) -> web.StreamResponse:
    raise web.HTTPFound("/live")


async def live(_: web.Request) -> web.FileResponse:
    return web.FileResponse(STATIC / "live.html")


async def fleet_dashboard(_: web.Request) -> web.FileResponse:
    return web.FileResponse(STATIC / "fleet.html")


async def read_stderr(process: asyncio.subprocess.Process) -> str:
    body = await process.stderr.read()
    return body[-65536:].decode("utf-8", errors="replace").strip()


async def pump_detector(session: DetectorSession, video_id: str, store: KnowledgeStore) -> None:
    while True:
        line = await session.process.stdout.readline()
        if not line:
            detail = await read_stderr(session.process)
            await session.process.wait()
            if detail:
                await session.ws.send_json({"error": f"detector stopped: {detail}"})
            else:
                await session.ws.send_json({"error": "detector stopped before producing another frame"})
            return
        try:
            frame = json.loads(line)
        except json.JSONDecodeError:
            continue
        frame["videoId"] = video_id
        frame["backend"] = "cuda-yolo11x"
        try:
            observation_id = await asyncio.to_thread(store.ingest_frame, frame)
            frame["knowledge"] = {"state": "stored", "observationId": observation_id}
        except (ValueError, sqlite3.Error) as exc:
            logging.exception("knowledge storage failed")
            frame["knowledge"] = {"state": "degraded", "reason": str(exc)}
        await session.ws.send_json(frame, dumps=lambda value: json.dumps(value, separators=(",", ":")))


async def _process_frame_with_behavioral_learning(fleet: CameraFleet, learner: BehavioralLearner, 
                                                camera_id: str, frame: dict) -> None:
    """Process a frame through behavioral learning and handle anomalies."""
    try:
        # Process frame through behavioral learner
        anomalies = learner.check_anomalies(frame)
        
        # If anomalies detected, we could trigger alerts or other actions
        if anomalies:
            # In a full implementation, this would alert the autonomous operator
            # For now, we just log them
            for anomaly in anomalies:
                logging.info(f"Behavioral anomaly detected for camera {camera_id}: {anomaly.anomaly_type} (severity: {anomaly.severity})")
    except Exception as e:
        logging.exception(f"Error in behavioral learning processing for camera {camera_id}: {e}")


async def wait_for_disconnect(ws: web.WebSocketResponse) -> None:
    async for message in ws:
        if message.type in {WSMsgType.CLOSE, WSMsgType.CLOSED, WSMsgType.ERROR}:
            return


async def websocket(request: web.Request) -> web.WebSocketResponse:
    if not allowed_origin(request):
        raise web.HTTPForbidden(text="local dashboard origin required")
    ws = web.WebSocketResponse(heartbeat=20, max_msg_size=4 << 20)
    await ws.prepare(request)
    if request.query.get("protocol") != "2":
        await ws.close(code=4002, message=b"dashboard refresh required")
        return ws

    try:
        video_id = extract_video_id(request.query.get("url", "JQ_jwk_7OVE"))
    except ValueError as exc:
        await ws.send_json({"error": str(exc)})
        await ws.close(code=4003, message=b"invalid YouTube URL")
        return ws

    profile = "realtime" if request.query.get("profile") == "realtime" else "maximum"
    command = [
        sys.executable, str(DETECTOR), "--url", f"https://www.youtube.com/watch?v={video_id}",
        "--video-id", video_id,
    ]
    if profile == "realtime":
        command.extend(["--imgsz", "1280", "--confidence", "0.25"])
    process = await asyncio.create_subprocess_exec(
        *command, cwd=ROOT, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE,
        limit=4 << 20, creationflags=0x00000200 if os.name == "nt" else 0,
    )
    session = DetectorSession(ws, process)
    await STATE.replace(session)

    detector_task = asyncio.create_task(pump_detector(session, video_id, request.app[KNOWLEDGE_STORE]))
    disconnect_task = asyncio.create_task(wait_for_disconnect(ws))
    try:
        done, pending = await asyncio.wait(
            {detector_task, disconnect_task}, return_when=asyncio.FIRST_COMPLETED
        )
        for task in pending:
            task.cancel()
        await asyncio.gather(*done, return_exceptions=True)
        await asyncio.gather(*pending, return_exceptions=True)
    finally:
        await session.stop()
        await STATE.clear(session)
        if not ws.closed:
            await ws.close()
    return ws


async def shutdown(app: web.Application) -> None:
    async with STATE.lock:
        session = STATE.active
        STATE.active = None
    if session is not None:
        await session.stop()
    
    # Shutdown fleet
    fleet = app.get(FLEET)
    if fleet:
        await fleet.stop()
    
    # Shutdown autonomous operator
    operator = app.get(AUTONOMOUS_OPERATOR)
    if operator:
        await operator.stop()


async def chat(request: web.Request) -> web.Response:
    if not allowed_origin(request):
        raise web.HTTPForbidden(text="local dashboard origin required")
    try:
        body = await request.json()
    except (json.JSONDecodeError, TypeError):
        raise web.HTTPBadRequest(text="JSON body required") from None
    question = body.get("question") if isinstance(body, dict) else None
    if not isinstance(question, str) or not question.strip() or len(question) > 1000:
        raise web.HTTPBadRequest(text="question must be 1..1000 characters")
    video_id = body.get("videoId")
    if video_id is not None and (not isinstance(video_id, str) or not re.fullmatch(r"[A-Za-z0-9_-]{11}", video_id)):
        raise web.HTTPBadRequest(text="videoId must be an 11-character YouTube id")
    result = await asyncio.to_thread(request.app[KNOWLEDGE_STORE].chat, question, video_id)
    return web.json_response(result)


async def critical(request: web.Request) -> web.Response:
    try:
        limit = int(request.query.get("limit", "5"))
    except ValueError:
        raise web.HTTPBadRequest(text="limit must be an integer") from None
    result = await asyncio.to_thread(request.app[KNOWLEDGE_STORE].critical_streams, limit)
    return web.json_response({"streams": result})


async def knowledge_graph(request: web.Request) -> web.Response:
    video_id = request.query.get("videoId")
    if video_id is not None and not re.fullmatch(r"[A-Za-z0-9_-]{11}", video_id):
        raise web.HTTPBadRequest(text="videoId must be an 11-character YouTube id")
    result = await asyncio.to_thread(request.app[KNOWLEDGE_STORE].graph, video_id)
    return web.json_response(result)


async def clips(request: web.Request) -> web.Response:
    video_id = request.query.get("videoId")
    if video_id is not None and not re.fullmatch(r"[A-Za-z0-9_-]{11}", video_id):
        raise web.HTTPBadRequest(text="videoId must be an 11-character YouTube id")
    result = await asyncio.to_thread(request.app[KNOWLEDGE_STORE].clips, video_id)
    return web.json_response({"clips": result})


def app(store: KnowledgeStore | None = None) -> web.Application:
    application = web.Application(middlewares=[no_cache])
    clip_root = ROOT / "dist" / "live-clips"
    clip_root.mkdir(parents=True, exist_ok=True)
    if store is None:
        db_path = Path(os.environ.get("VISION_POC_DB", ROOT / "dist" / "live-poc.sqlite3"))
        retention = int(os.environ.get("VISION_POC_RETENTION_HOURS", "168"))
        max_observations = int(os.environ.get("VISION_POC_MAX_OBSERVATIONS", "100000"))
        store = KnowledgeStore(db_path, retention_hours=retention, max_observations=max_observations)
    store.reconcile_clips(clip_root)
    application[KNOWLEDGE_STORE] = store

    # Initialize behavioral learner
    behavioral_learner_db = Path(os.environ.get("VISION_BEHAVIORAL_DB", ROOT / "dist" / "behavioral.sqlite3"))
    behavioral_learner = BehavioralLearner(behavioral_learner_db)
    application[BEHAVIORAL_LEARNER] = behavioral_learner

    # Initialize camera fleet
    fleet = CameraFleet(store, DETECTOR, max_workers=int(os.environ.get("VISION_MAX_WORKERS", "50")))
    fleet.on_frame_callback = lambda camera_id, frame: asyncio.create_task(
        _process_frame_with_behavioral_learning(fleet, behavioral_learner, camera_id, frame)
    )
    application[FLEET] = fleet

    # Initialize autonomous operator
    operator = AutonomousOperator(fleet, store, behavioral_learner)
    application[AUTONOMOUS_OPERATOR] = operator

    # Initialize conversational search
    conversational_search = ConversationalSearch(store, behavioral_learner, fleet)
    application[CONVERSATIONAL_SEARCH] = conversational_search

    # Mount sub-applications
    # Main POC application routes
    application.router.add_get("/", index)
    application.router.add_get("/live", live)
    application.router.add_get("/fleet", fleet_dashboard)
    application.router.add_get("/ws/live", websocket)
    application.router.add_post("/api/chat", chat)
    application.router.add_get("/api/critical", critical)
    application.router.add_get("/api/graph", knowledge_graph)
    application.router.add_get("/api/clips", clips)
    application.router.add_static("/clips", clip_root)
    application.router.add_static("/static", STATIC)

    # Fleet management APIs (mounted under /api/fleet)
    fleet_app = web.Application()
    fleet_app["fleet"] = fleet
    fleet_app.on_shutdown.append(lambda app: fleet.stop())
    
    async def fleet_summary_handler(request):
        return web.json_response(await fleet.get_fleet_summary())
    
    async def fleet_cameras_handler(request):
        return web.json_response([c.to_dict() for c in await fleet.list_cameras()])
    
    fleet_app.router.add_get("/summary", fleet_summary_handler)
    fleet_app.router.add_get("/cameras", fleet_cameras_handler)
    fleet_app.router.add_post("/cameras", add_camera_handler(fleet))
    fleet_app.router.add_delete("/cameras/{camera_id}", delete_camera_handler(fleet))
    fleet_app.router.add_get("/cameras/{camera_id}", get_camera_handler(fleet))
    fleet_app.router.add_post("/cameras/{camera_id}/restart", restart_camera_handler(fleet))
    application.add_subapp("/api/fleet/", fleet_app)
    # Autonomous operator APIs (mounted under /api/operator)
    operator_app = web.Application()
    operator_app["operator"] = operator
    operator_app.on_shutdown.append(lambda app: operator.stop())
    
    async def operator_summary_handler(request):
        return web.json_response(await fleet.get_fleet_summary())
    
    async def operator_alerts_handler(request):
        return web.json_response(operator.get_alerts(
            acknowledged=None if request.query.get("include_acknowledged") == "true" else False,
            limit=int(request.query.get("limit", "50"))
        ))
    
    async def operator_acknowledge_handler(request):
        return web.json_response({"success": operator.acknowledge_alert(request.match_info["alert_id"])})
    
    async def operator_resolve_handler(request):
        return web.json_response({"success": operator.resolve_alert(request.match_info["alert_id"])})
    
    async def operator_decisions_handler(request):
        return web.json_response(operator.get_decisions(
            limit=int(request.query.get("limit", "50"))
        ))
    
    async def operator_command_handler(request):
        try:
            body = await request.json()
        except Exception:
            raise web.HTTPBadRequest(text="JSON body required")
        
        command = body.get("command")
        if not command or not isinstance(command, str):
            raise web.HTTPBadRequest(text="command string required")
        
        result = await operator.process_natural_language_command(command)
        return web.json_response(result)
    
    operator_app.router.add_get("/summary", operator_summary_handler)
    operator_app.router.add_get("/alerts", operator_alerts_handler)
    operator_app.router.add_post("/alerts/{alert_id}/acknowledge", operator_acknowledge_handler)
    operator_app.router.add_post("/alerts/{alert_id}/resolve", operator_resolve_handler)
    operator_app.router.add_get("/decisions", operator_decisions_handler)
    operator_app.router.add_post("/command", operator_command_handler)
    application.add_subapp("/api/operator/", operator_app)

    # Conversational search APIs (mounted under /api/search)
    search_app = web.Application()
    search_app["search"] = conversational_search
    
    async def search_chat_handler(request):
        try:
            body = await request.json()
        except Exception:
            return web.json_response({"state": "unavailable", "answer": "Invalid JSON", "citations": []})
        
        question = body.get("question", "")
        video_id = body.get("videoId")
        try:
            limit = int(body.get("limit", "10"))
        except ValueError:
            limit = 10
        
        # Create task for the async chat function and wait for it
        chat_task = asyncio.create_task(conversational_search.chat(question, video_id, limit))
        result = await chat_task
        return web.json_response(result)
    
    search_app.router.add_post("/chat", search_chat_handler)
    search_app.router.add_get("/health", lambda r: web.json_response({"status": "ok", "service": "conversational_search"}))
    application.add_subapp("/api/search/", search_app)

    application.on_shutdown.append(shutdown)
    return application


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=8080)
    args = parser.parse_args()
    print(f"Live POC: http://127.0.0.1:{args.port}/live", flush=True)
    print(f"  Fleet Management: http://127.0.0.1:{args.port}/api/fleet/", flush=True)
    print(f"  Autonomous Operator: http://127.0.0.1:{args.port}/api/operator/", flush=True)
    print(f"  Conversational Search: http://127.0.0.1:{args.port}/api/search/", flush=True)
    web.run_app(app(), host="127.0.0.1", port=args.port, print=None)


if __name__ == "__main__":
    main()
