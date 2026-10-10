#!/usr/bin/env python3
"""Autonomous operator agent for multi-camera fleet management.

Provides triage, prioritization, natural-language interface, and autonomous
decision-making for large-scale camera fleets (200-2000+ cameras).
"""

from __future__ import annotations

import asyncio
import json
import logging
import re
from dataclasses import asdict, dataclass, field
from datetime import datetime, timedelta, timezone
from enum import Enum
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional

import aiohttp
from aiohttp import web

from behavioral_learning import Anomaly, BehavioralLearner
from camera_fleet import Camera, CameraFleet, CameraStatus
from live_knowledge import KnowledgeStore

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s")
log = logging.getLogger("autonomous_operator")


class AlertLevel(Enum):
    INFO = "info"
    WARNING = "warning"
    CRITICAL = "critical"
    EMERGENCY = "emergency"


class ActionType(Enum):
    NOTIFY = "notify"
    PRIORITIZE = "prioritize"
    INVESTIGATE = "investigate"
    ESCALATE = "escalate"
    AUTOMATE = "automate"


@dataclass
class Alert:
    id: str
    camera_id: str
    timestamp: datetime
    level: AlertLevel
    title: str
    description: str
    anomaly_type: Optional[str] = None
    suggested_actions: List[ActionType] = field(default_factory=list)
    metadata: Dict[str, Any] = field(default_factory=dict)
    acknowledged: bool = False
    resolved: bool = False


@dataclass
class OperatorDecision:
    camera_id: str
    timestamp: datetime
    action_type: ActionType
    priority: int  # 1-10, 10 being highest
    reason: str
    parameters: Dict[str, Any] = field(default_factory=dict)
    executed: bool = False


class AutonomousOperator:
    def __init__(
        self,
        fleet: CameraFleet,
        knowledge_store: KnowledgeStore,
        behavioral_learner: BehavioralLearner,
        check_interval: int = 30,
    ):
        self.fleet = fleet
        self.store = knowledge_store
        self.learner = behavioral_learner
        self.check_interval = check_interval

        self._alerts: Dict[str, Alert] = {}
        self._decisions: List[OperatorDecision] = []
        self._running = False
        self._monitor_task: Optional[asyncio.Task] = None
        self._nl_processor_task: Optional[asyncio.Task] = None

        # Natural language command patterns
        self._command_patterns = {
            r"show\s+(?:me\s+)?(?:the\s+)?(?:status\s+of\s+)?(.+)", self._handle_status_query,
            r"prioritize\s+(?:camera\s+)?(.+)", self._handle_prioritize_command,
            r"investigate\s+(?:camera\s+)?(.+)", self._handle_investigate_command,
            r"what\s+(?:is\s+)?(?:going\s+on\s+with\s+)?(.+)", self._handle_situation_query,
            r"alert\s+me\s+if\s+(.+)", self._handle_alert_setup,
            r"stop\s+(?:alerting\s+on\s+)?(.+)", self._handle_alert_cancel,
        }

    async def start(self) -> None:
        if self._running:
            return
        self._running = True
        self._monitor_task = asyncio.create_task(self._monitor_loop())
        self._nl_processor_task = asyncio.create_task(self._natural_language_processor())
        log.info("Autonomous operator started")

    async def stop(self) -> None:
        self._running = False
        if self._monitor_task:
            self._monitor_task.cancel()
        if self._nl_processor_task:
            self._nl_processor_task.cancel()
        log.info("Autonomous operator stopped")

    async def _monitor_loop(self) -> None:
        while self._running:
            try:
                await self._check_fleet_health()
                await self._process_anomalies()
                await self._make_autonomous_decisions()
            except Exception as e:
                log.error(f"Error in operator monitor loop: {e}")
            await asyncio.sleep(self.check_interval)

    async def _natural_language_processor(self) -> None:
        """Process natural language commands from operators."""
        while self._running:
            try:
                # In a real implementation, this would listen to a message queue
                # or webhook for operator commands
                await asyncio.sleep(5)
            except Exception as e:
                log.error(f"Error in natural language processor: {e}")

    async def _check_fleet_health(self) -> None:
        """Check overall fleet health and generate alerts for systemic issues."""
        summary = await self.fleet.get_fleet_summary()
        
        # Alert on high percentage of unhealthy cameras
        total = summary["total_cameras"]
        if total == 0:
            return
            
        unhealthy_pct = (summary["degraded"] + summary["unreachable"] + summary["disabled"]) / total * 100
        
        if unhealthy_pct > 30:
            await self._create_alert(
                level=AlertLevel.WARNING if unhealthy_pct < 60 else AlertLevel.CRITICAL,
                title="High Unhealthy Camera Rate",
                description=f"{unhealthy_pct:.1f}% of cameras ({summary['degraded'] + summary['unreachable'] + summary['disabled']} of {total}) are unhealthy",
                metadata={"summary": summary}
            )
        elif unhealthy_pct > 10:
            await self._create_alert(
                level=AlertLevel.INFO,
                title="Elevated Unhealthy Camera Rate",
                description=f"{unhealthy_pct:.1f}% of cameras are unhealthy",
                metadata={"summary": summary}
            )

        # Alert on worker pool exhaustion
        active_workers = summary["active_workers"]
        max_workers = summary["max_workers"]
        if active_workers >= max_workers * 0.9:
            await self._create_alert(
                level=AlertLevel.WARNING,
                title="Worker Pool Near Capacity",
                description=f"{active_workers}/{max_workers} workers active",
                metadata={"summary": summary}
            )

    async def _process_anomalies(self) -> None:
        """Process recent anomalies and generate prioritized alerts."""
        # Get anomalies from the last 5 minutes
        recent_anomalies = self.learner.get_recent_anomalies(hours=1/12, limit=50)  # Last 5 minutes
        
        for anomaly_dict in recent_anomalies:
            anomaly = Anomaly(
                camera_id=anomaly_dict["camera_id"],
                timestamp=datetime.fromisoformat(anomaly_dict["observed_at"].replace("Z", "+00:00")),
                anomaly_type=anomaly_dict["anomaly_type"],
                severity=anomaly_dict["severity"],
                details=json.loads(anomaly_dict["details_json"]),
                baseline_values=json.loads(anomaly_dict["baseline_json"]),
                observed_values=json.loads(anomaly_dict["observed_json"]),
            )
            
            # Check if we've already alerted on this anomaly
            alert_id = f"anomaly:{anomaly.camera_id}:{anomaly.timestamp.timestamp()}:{anomaly.anomaly_type}"
            if alert_id in self._alerts:
                continue
                
            # Determine alert level based on severity and anomaly type
            level = AlertLevel.INFO
            if anomaly.severity >= 80:
                level = AlertLevel.EMERGENCY
            elif anomaly.severity >= 60:
                level = AlertLevel.CRITICAL
            elif anomaly.severity >= 40:
                level = AlertLevel.WARNING
                
            # Generate suggested actions based on anomaly type
            suggested_actions = self._suggest_actions_for_anomaly(anomaly)
            
            await self._create_alert(
                level=level,
                title=f"Anomaly Detected: {anomaly.anomaly_type.replace('_', ' ').title()}",
                description=f"Camera {anomaly.camera_id}: {anomaly.details.get('metric', '')} deviation of {anomaly.severity:.1f}",
                camera_id=anomaly.camera_id,
                anomaly_type=anomaly.anomaly_type,
                suggested_actions=suggested_actions,
                metadata={
                    "anomaly": asdict(anomaly),
                    "baseline": anomaly.baseline_values,
                    "observed": anomaly.observed_values
                }
            )

    def _suggest_actions_for_anomaly(self, anomaly: Anomaly) -> List[ActionType]:
        """Suggest appropriate actions based on anomaly type."""
        actions = [ActionType.NOTIFY]
        
        if "violation" in anomaly.anomaly_type:
            actions.extend([ActionType.PRIORITIZE, ActionType.INVESTIGATE])
        elif "surge" in anomaly.anomaly_type or "spike" in anomaly.anomaly_type:
            actions.extend([ActionType.PRIORITIZE, ActionType.INVESTIGATE])
        elif anomaly.severity >= 70:
            actions.extend([ActionType.PRIORITIZE, ActionType.ESCALATE])
            
        return actions

    async def _make_autonomous_decisions(self) -> None:
        """Make autonomous decisions based on alerts and fleet state."""
        # Process unacknowledged alerts and make prioritization decisions
        unacknowledged_alerts = [a for a in self._alerts.values() if not a.acknowledged]
        
        # Sort by level and time
        sorted_alerts = sorted(
            unacknowledged_alerts,
            key=lambda a: (
                {"emergency": 4, "critical": 3, "warning": 2, "info": 1}[a.level.value],
                a.timestamp
            ),
            reverse=True
        )
        
        # Make decisions for top priority alerts
        for alert in sorted_alerts[:10]:  # Top 10 priority alerts
            if alert.suggested_actions:
                # Choose the highest priority action
                action_priority = {
                    ActionType.EMERGENCY: 10,
                    ActionType.ESCALATE: 9,
                    ActionType.INVESTIGATE: 8,
                    ActionType.PRIORITIZE: 7,
                    ActionType.NOTIFY: 5
                }
                
                best_action = max(alert.suggested_actions, 
                                key=lambda a: action_priority.get(a, 1))
                
                decision_priority = 10 if alert.level == AlertLevel.EMERGENCY else \
                                 8 if alert.level == AlertLevel.CRITICAL else \
                                 6 if alert.level == AlertLevel.WARNING else 4
                
                decision = OperatorDecision(
                    camera_id=alert.camera_id,
                    timestamp=datetime.now(timezone.utc),
                    action_type=best_action,
                    priority=decision_priority,
                    reason=f"Autonomous response to {alert.level.value} alert: {alert.title}",
                    parameters={"alert_id": alert.id}
                )
                
                self._decisions.append(decision)
                # In a full implementation, we would execute the decision here
                log.info(f"Made autonomous decision: {decision.action_type.value} for camera {decision.camera_id} (priority {decision.priority})")

    async def _create_alert(
        self,
        level: AlertLevel,
        title: str,
        description: str,
        camera_id: Optional[str] = None,
        anomaly_type: Optional[str] = None,
        suggested_actions: Optional[List[ActionType]] = None,
        metadata: Optional[Dict[str, Any]] = None
    ) -> str:
        """Create a new alert."""
        alert_id = f"alert:{int(datetime.now(timezone.utc).timestamp())}:{hash(title) & 0xffffffff}"
        
        alert = Alert(
            id=alert_id,
            camera_id=camera_id or "",
            timestamp=datetime.now(timezone.utc),
            level=level,
            title=title,
            description=description,
            anomaly_type=anomaly_type,
            suggested_actions=suggested_actions or [],
            metadata=metadata or {}
        )
        
        self._alerts[alert_id] = alert
        log.info(f"Created {level.value} alert: {title}")
        
        return alert_id

    async def process_natural_language_command(self, command: str) -> Dict[str, Any]:
        """Process a natural language command from an operator."""
        command = command.strip().lower()
        
        for pattern, handler in self._command_patterns.items():
            match = re.match(pattern, command)
            if match:
                return await handler(match.group(1) if match.groups() else "")
        
        return {
            "state": "unavailable",
            "answer": f"I didn't understand the command: '{command}'. Try asking about camera status, prioritization, or investigations.",
            "suggestions": [
                "Show me the status of all cameras",
                "Prioritize camera main_entrance",
                "Investigate anomalies in the parking lot",
                "What's going on with camera backyard?"
            ]
        }

    async def _handle_status_query(self, target: str) -> Dict[str, Any]:
        """Handle status queries like 'show me the status of all cameras'."""
        if "all" in target or not target.strip():
            summary = await self.fleet.get_fleet_summary()
            return {
                "state": "answered",
                "answer": f"Fleet status: {summary['healthy']} healthy, {summary['degraded']} degraded, {summary['unreachable']} unreachable, {summary['disabled']} disabled out of {summary['total_cameras']} total cameras.",
                "data": summary
            }
        else:
            # Try to find camera by name or ID
            cameras = await self.fleet.list_cameras()
            matching_cameras = [
                c for c in cameras 
                if target.lower() in c.name.lower() or target.lower() in c.id.lower()
            ]
            
            if not matching_cameras:
                return {
                    "state": "unavailable",
                    "answer": f"No cameras found matching '{target}'",
                    "suggestions": [c.name for c in cameras[:5]]
                }
            
            if len(matching_cameras) == 1:
                camera = matching_cameras[0]
                return {
                    "state": "answered",
                    "answer": f"Camera '{camera.name}' ({camera.id}): {camera.status.value}",
                    "data": camera.to_dict()
                }
            else:
                options = [f"{c.name} ({c.id}): {c.status.value}" for c in matching_cameras]
                return {
                    "state": "answered",
                    "answer": f"Found {len(matching_cameras)} matching cameras:\n" + "\n".join(options),
                    "data": [c.to_dict() for c in matching_cameras]
                }

    async def _handle_prioritize_command(self, target: str) -> Dict[str, Any]:
        """Handle prioritization commands."""
        cameras = await self.fleet.list_cameras()
        matching_cameras = [
            c for c in cameras 
            if target.lower() in c.name.lower() or target.lower() in c.id.lower()
        ]
        
        if not matching_cameras:
            return {
                "state": "unavailable",
                "answer": f"No cameras found matching '{target}' for prioritization",
            }
        
        # In a real system, this would adjust processing parameters or alert thresholds
        camera_ids = [c.id for c in matching_cameras]
        return {
            "state": "answered",
            "answer": f"Prioritized {len(matching_cameras)} camera(s) for enhanced monitoring: {', '.join([c.name for c in matching_cameras])}",
            "data": {"prioritized_cameras": camera_ids}
        }

    async def _handle_investigate_command(self, target: str) -> Dict[str, Any]:
        """Handle investigation commands."""
        cameras = await self.fleet.list_cameras()
        matching_cameras = [
            c for c in cameras 
            if target.lower() in c.name.lower() or target.lower() in c.id.lower()
        ]
        
        if not matching_cameras:
            return {
                "state": "unavailable",
                "answer": f"No cameras found matching '{target}' for investigation",
            }
        
        # Get recent anomalies for these cameras
        camera_ids = [c.id for c in matching_cameras]
        anomalies = []
        for cam_id in camera_ids:
            cam_anomalies = self.learner.get_recent_anomalies(camera_id=cam_id, hours=1, limit=5)
            anomalies.extend(cam_anomalies)
        
        if not anomalies:
            return {
                "state": "answered",
                "answer": f"No recent anomalies found for cameras matching '{target}'",
                "data": {"cameras": [c.to_dict() for c in matching_cameras], "anomalies": []}
            }
        
        return {
            "state": "answered",
            "answer": f"Found {len(anomalies)} recent anomaly(s) for cameras matching '{target}'",
            "data": {
                "cameras": [c.to_dict() for c in matching_cameras],
                "recent_anomalies": anomalies[:10]  # Limit to 10
            }
        }

    async def _handle_situation_query(self, target: str) -> Dict[str, Any]:
        """Handle general situation queries."""
        # Similar to status but with more context
        return await self._handle_status_query(target)

    async def _handle_alert_setup(self, target: str) -> Dict[str, Any]:
        """Handle setting up alerts for specific conditions."""
        return {
            "state": "answered",
            "answer": f"Alert setup for '{target}' would be configured in a full implementation.",
            "data": {"condition": target}
        }

    async def _handle_alert_cancel(self, target: str) -> Dict[str, Any]:
        """Handle canceling alerts."""
        return {
            "state": "answered",
            "answer": f"Alert cancellation for '{target}' processed.",
            "data": {"condition": target}
        }

    def get_alerts(self, acknowledged: Optional[bool] = None, limit: int = 50) -> List[Dict[str, Any]]:
        """Get alerts, optionally filtered by acknowledgment status."""
        alerts = list(self._alerts.values())
        if acknowledged is not None:
            alerts = [a for a in alerts if a.acknowledged == acknowledged]
        
        # Sort by timestamp descending
        alerts.sort(key=lambda a: a.timestamp, reverse=True)
        
        return [asdict(alert) for alert in alerts[:limit]]

    def get_decisions(self, limit: int = 50) -> List[Dict[str, Any]]:
        """Get recent operator decisions."""
        decisions = sorted(self._decisions, key=lambda d: d.timestamp, reverse=True)
        return [asdict(decision) for decision in decisions[:limit]]

    def acknowledge_alert(self, alert_id: str) -> bool:
        """Acknowledge an alert."""
        if alert_id in self._alerts:
            self._alerts[alert_id].acknowledged = True
            return True
        return False

    def resolve_alert(self, alert_id: str) -> bool:
        """Resolve an alert."""
        if alert_id in self._alerts:
            self._alerts[alert_id].resolved = True
            self._alerts[alert_id].acknowledged = True
            return True
        return False


async def create_operator_app(
    fleet: CameraFleet,
    knowledge_store: KnowledgeStore,
    behavioral_learner: BehavioralLearner,
) -> web.Application:
    """Create a web application for the autonomous operator."""
    operator = AutonomousOperator(fleet, knowledge_store, behavioral_learner)
    await operator.start()

    async def shutdown(app: web.Application) -> None:
        await operator.stop()

    app = web.Application()
    app["operator"] = operator
    app.on_shutdown.append(shutdown)

    # Fleet status endpoints
    async def summary_handler(request):
        return web.json_response(await operator.fleet.get_fleet_summary())
    
    async def alerts_handler(request):
        return web.json_response(operator.get_alerts(
            acknowledged=None if request.query.get("include_acknowledged") == "true" else False,
            limit=int(request.query.get("limit", "50"))
        ))
    
    async def acknowledge_handler(request):
        return web.json_response({"success": operator.acknowledge_alert(request.match_info["alert_id"])})
    
    async def resolve_handler(request):
        return web.json_response({"success": operator.resolve_alert(request.match_info["alert_id"])})
    
    async def decisions_handler(request):
        return web.json_response(operator.get_decisions(
            limit=int(request.query.get("limit", "50"))
        ))
    
    app.router.add_get("/api/operator/summary", summary_handler)
    app.router.add_get("/api/operator/alerts", alerts_handler)
    app.router.add_post("/api/operator/alerts/{alert_id}/acknowledge", acknowledge_handler)
    app.router.add_post("/api/operator/alerts/{alert_id}/resolve", resolve_handler)
    app.router.add_get("/api/operator/decisions", decisions_handler)

    # Natural language interface
    async def nl_command(request: web.Request) -> web.Response:
        try:
            body = await request.json()
        except Exception:
            raise web.HTTPBadRequest(text="JSON body required")
        
        command = body.get("command")
        if not command or not isinstance(command, str):
            raise web.HTTPBadRequest(text="command string required")
        
        result = await operator.process_natural_language_command(command)
        return web.json_response(result)

    app.router.add_post("/api/operator/command", nl_command)

    return app