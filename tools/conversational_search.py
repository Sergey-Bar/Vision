#!/usr/bin/env python3
"""Conversational search interface for multi-camera analytics.

Provides ChatGPT-style natural language querying across cameras and time
with grounding, citations, and cross-correlation capabilities.
"""

from __future__ import annotations

import json
import re
from dataclasses import asdict, dataclass
from datetime import datetime, timedelta, timezone
from typing import Any, Dict, List, Optional

from live_knowledge import KnowledgeStore


@dataclass
class SearchResult:
    citation: str
    video_id: str
    observed_at: str
    frame: int
    track_id: str
    label: str
    category: str
    zone: str
    confidence: float
    violation: bool
    metadata: Dict[str, Any] = None


@dataclass
class ConversationalResponse:
    state: str  # "answered", "unavailable", "clarifying"
    answer: str
    citations: List[str]
    results: List[SearchResult] = None
    follow_up_questions: List[str] = None
    metadata: Dict[str, Any] = None


class ConversationalSearch:
    def __init__(
        self,
        knowledge_store: KnowledgeStore,
        behavioral_learner: Optional[Any] = None,  # To avoid circular import
        fleet_manager: Optional[Any] = None,      # To avoid circular import
    ):
        self.store = knowledge_store
        self.learner = behavioral_learner
        self.fleet = fleet_manager

        # Extended stop words for conversational queries
        self.STOP_WORDS = {
            "a", "an", "and", "are", "at", "did", "enter", "entered", "find", "for", "in", "is", "me", "of", "on", "show", "the", "to", "what", "which",
            "את", "ב", "באזור", "האם", "הראה", "הצג", "ו", "כמה", "לי", "מה", "של",
            # Conversational extensions
            "please", "can", "you", "tell", "me", "about", "show", "me", "what", "is", "the", "for", "of", "with", "without",
            "any", "some", "many", "few", "ever", "never", "always", "often", "sometimes", "recently", "lately",
            "today", "yesterday", "last", "this", "that", "these", "those", "here", "there", "where", "when"
        }

        # Extended synonyms
        self.SYNONYMS = {
            "cars": "vehicle", "vehicles": "vehicle", "trucks": "truck", "people": "person", "humans": "person",
            "רכב": "vehicle", "רכבים": "vehicle", "מכונית": "car", "מכוניות": "car",
            "אדם": "person", "אנשים": "person", "אדום": "red", "אדומה": "red", "אדומים": "red", "המוגבל": "no-zone",
            # Conversational extensions
            "crowd": "people", "group": "people", "individual": "person",
            "car": "vehicle", "automobile": "vehicle", "truck": "vehicle", "van": "vehicle",
            "motion": "movement", "activity": "action", "incident": "event",
            "camera": "video_id", "feed": "video_id", "stream": "video_id",
            "incident": "event", "alert": "anomaly", "violation": "violation",
            "person": "people", "human": "people", "individual": "people",
            "car": "vehicles", "truck": "vehicles", "vehicle": "vehicles",
            "zone": "area", "region": "area", "location": "area",
            "time": "timestamp", "date": "timestamp", "moment": "timestamp",
            "happening": "event", "going on": "event", "occurring": "event"
        }

        # Temporal expressions
        self.TEMPORAL_PATTERNS = {
            r"today": lambda: datetime.now(timezone.utc).replace(hour=0, minute=0, second=0, microsecond=0),
            r"yesterday": lambda: (datetime.now(timezone.utc) - timedelta(days=1)).replace(hour=0, minute=0, second=0, microsecond=0),
            r"last\s+hour": lambda: datetime.now(timezone.utc) - timedelta(hours=1),
            r"last\s+(\d+)\s+hours": lambda m: datetime.now(timezone.utc) - timedelta(hours=int(m.group(1))),
            r"last\s+day": lambda: datetime.now(timezone.utc) - timedelta(days=1),
            r"last\s+(\d+)\s+days": lambda m: datetime.now(timezone.utc) - timedelta(days=int(m.group(1))),
            r"last\s+week": lambda: datetime.now(timezone.utc) - timedelta(weeks=1),
            r"last\s+(\d+)\s+weeks": lambda m: datetime.now(timezone.utc) - timedelta(weeks=int(m.group(1))),
            r"this\s+hour": lambda: datetime.now(timezone.utc).replace(minute=0, second=0, microsecond=0),
            r"past\s+hour": lambda: datetime.now(timezone.utc) - timedelta(hours=1),
            r"past\s+(\d+)\s+hours": lambda m: datetime.now(timezone.utc) - timedelta(hours=int(m.group(1))),
        }

    def _extract_temporal_constraints(self, question: str) -> Optional[datetime]:
        """Extract temporal constraints from question."""
        question_lower = question.lower()
        
        for pattern, func in self.TEMPORAL_PATTERNS.items():
            match = re.search(pattern, question_lower)
            if match:
                try:
                    if match.groups():
                        return func(match)
                    else:
                        return func()
                except Exception:
                    continue
        
        return None

    def _apply_temporal_filter(self, base_conditions: List[str], base_args: List[Any], 
                              start_time: Optional[datetime] = None,
                              end_time: Optional[datetime] = None) -> tuple[List[str], List[Any]]:
        """Apply temporal filters to SQL conditions."""
        conditions = base_conditions.copy()
        args = base_args.copy()
        
        if start_time:
            conditions.append("o.observed_at >= ?")
            args.append(start_time.isoformat())
        
        if end_time:
            conditions.append("o.observed_at <= ?")
            args.append(end_time.isoformat())
            
        return conditions, args

    def _enhanced_query_tokens(self, question: str) -> List[str]:
        """Enhanced token extraction for conversational queries."""
        # Handle temporal expressions first
        temporal_constraint = self._extract_temporal_constraints(question)
        
        # Normalize question
        normalized = question.casefold().replace("restricted zone", "no-zone").replace("no zone", "no-zone")
        
        # Apply synonyms
        tokens = []
        for token in re.findall(r"[\w-]+", normalized, re.UNICODE):
            if token not in self.STOP_WORDS:
                normalized_token = self.SYNONYMS.get(token, token)
                tokens.append(normalized_token)
        
        # Limit tokens but keep important ones
        return tokens[:12]

    def _build_cross_camera_query(self, tokens: List[str], 
                                 video_id: Optional[str] = None,
                                 start_time: Optional[datetime] = None,
                                 end_time: Optional[datetime] = None) -> tuple[str, List[Any]]:
        """Build a query that can search across multiple cameras."""
        conditions = ["e.tenant_id = ?"]
        args: List[Any] = [self.store.tenant_id]
        
        if video_id:
            conditions.append("o.video_id = ?")
            args.append(video_id)
        
        # Apply temporal filters
        conditions, args = self._apply_temporal_filter(conditions, args, start_time, end_time)
        
        # Add searchable text conditions
        searchable = "lower(e.label || ' ' || e.category || ' ' || e.color || ' ' || e.vehicle_type || ' ' || e.zone)"
        for token in tokens:
            conditions.append(f"{searchable} LIKE ?")
            args.append(f"%{token}%")
        
        # Add ranking and limit
        args.append(min(50, 100))  # Inner limit for ranking
        
        query = f"""SELECT * FROM (
                          SELECT e.*, o.video_id, o.observed_at, o.frame_no,
                                 ROW_NUMBER() OVER (
                                     PARTITION BY o.video_id, e.track_id ORDER BY o.observed_at DESC
                                 ) AS recency_rank
                          FROM entities e JOIN observations o ON o.id = e.observation_id
                          WHERE {' AND '.join(conditions)}
                      ) WHERE recency_rank = 1
                      ORDER BY observed_at DESC LIMIT ?"""
        
        return query, args

    async def chat(self, question: str, video_id: Optional[str] = None, 
                   limit: int = 10) -> ConversationalResponse:
        """Main conversational interface - ChatGPT style."""
        raw = question.strip()
        if not raw:
            return ConversationalResponse(
                state="unavailable",
                answer="Enter a question to search stored observations across all cameras and time.",
                citations=[],
                follow_up_questions=[
                    "What people were detected today?",
                    "Show me vehicles in the parking lot yesterday",
                    "Any incidents in the last hour?",
                    "How many people entered the restricted zone?"
                ]
            )
        
        lowered = raw.casefold()
        
        # Handle critical/urgent queries
        if any(word in lowered for word in ("critical", "risk", "urgent", "emergency", "alert", "안전", "危险")):
            ranked = self.store.critical_streams(limit=limit)
            if not ranked:
                return ConversationalResponse(
                    state="unavailable",
                    answer="No recent stream observations are stored.",
                    citations=[],
                    results=[]
                )
            
            answer_parts = []
            for item in ranked:
                answer_parts.append(
                    f"{item['videoId']} ({item['score']}/100): {', '.join(item['reasons'])}"
                )
            answer = "; ".join(answer_parts)
            
            return ConversationalResponse(
                state="answered",
                answer=answer,
                citations=[f"stream:{item['videoId']}" for item in ranked],
                results=[],
                metadata={"type": "critical_analysis", "streams": ranked}
            )

        # Handle counting queries
        if any(word in lowered for word in ("count", "how many", "количество", "сколько", "total", "sum")):
            return await self._handle_counting_query(question, video_id)

        # Handle temporal/trend queries
        if any(word in lowered for word in ("trend", "over time", "timeline", "history", "pattern", " tendency")):
            return await self._handle_trend_query(question, video_id)

        # Handle comparative queries
        if any(word in lowered for word in ("compare", "versus", "vs", "difference", "more", "less", "than")):
            return await self._handle_comparative_query(question, video_id)

        # Handle spatial/zone queries
        if any(word in lowered for word in ("zone", "area", "region", "location", "where", "spatial")):
            return await self._handle_spatial_query(question, video_id)

        # Default to enhanced entity search
        return await self._handle_entity_search(question, video_id, limit)

    async def _handle_counting_query(self, question: str, 
                                   video_id: Optional[str] = None) -> ConversationalResponse:
        """Handle counting queries like 'how many people were detected today?'."""
        tokens = self._enhanced_query_tokens(question)
        start_time = self._extract_temporal_constraints(question)
        
        conditions = ["e.tenant_id = ?"]
        args: List[Any] = [self.store.tenant_id]
        
        if video_id:
            conditions.append("o.video_id = ?")
            args.append(video_id)
        
        if start_time:
            conditions.append("o.observed_at >= ?")
            args.append(start_time.isoformat())
        
        # Add searchable text conditions
        searchable = "lower(e.label || ' ' || e.category || ' ' || e.color || ' ' || e.vehicle_type || ' ' || e.zone)"
        for token in tokens:
            conditions.append(f"{searchable} LIKE ?")
            args.append(f"%{token}%")
        
        with self.store._db() as db:
            # Get total observations and unique tracks
            total_obs, unique_tracks = db.execute(
                f"""SELECT COUNT(DISTINCT o.id), COUNT(DISTINCT o.video_id || ':' || e.track_id)
                     FROM entities e JOIN observations o ON o.id = e.observation_id
                     WHERE {' AND '.join(conditions)}""",
                args,
            ).fetchone()
            
            # Get breakdown by label
            breakdown_rows = db.execute(
                f"""SELECT e.label, COUNT(DISTINCT o.video_id || ':' || e.track_id) as track_count
                     FROM entities e JOIN observations o ON o.id = e.observation_id
                     WHERE {' AND '.join(conditions)}
                     GROUP BY e.label
                     ORDER BY track_count DESC""",
                args,
            ).fetchall()
        
        if not total_obs:
            return ConversationalResponse(
                state="unavailable",
                answer="No stored observations match your criteria.",
                citations=[],
                results=[]
            )
        
        total_observations = total_obs
        total_unique_tracks = unique_tracks
        
        # Build answer
        time_ref = "today" if start_time and start_time.date() == datetime.now(timezone.utc).date() else \
                  "the specified time period" if start_time else "all time"
        
        answer_parts = [f"Found {total_unique_tracks} unique object tracks across {total_observations} observations {time_ref}."]
        
        if breakdown_rows:
            answer_parts.append("Breakdown by object type:")
            for label, count in breakdown_rows[:5]:  # Top 5
                answer_parts.append(f"  • {label}: {count}")
        
        answer = " ".join(answer_parts)
        
        # Get detailed results for citations
        detailed_results = []
        with self.store._db() as db:
            rows = db.execute(
                f"""SELECT e.*, o.video_id, o.observed_at, o.frame_no
                     FROM entities e JOIN observations o ON o.id = e.observation_id
                     WHERE {' AND '.join(conditions)}
                     ORDER BY o.observed_at DESC LIMIT 20""",
                [*args, 20],
            ).fetchall()
            
            for row in rows:
                detailed_results.append(SearchResult(
                    citation=f"obs:{row['observation_id']}",
                    video_id=row["video_id"],
                    observed_at=row["observed_at"],
                    frame=row["frame_no"],
                    track_id=row["track_id"],
                    label=row["label"],
                    category=row["category"],
                    zone=row["zone"],
                    confidence=row["confidence"],
                    violation=bool(row["violation"]),
                    metadata={"bbox": json.loads(row["bbox_json"]) if row["bbox_json"] else []}
                ))
        
        return ConversationalResponse(
            state="answered",
            answer=answer,
            citations=[r.citation for r in detailed_results],
            results=detailed_results,
            follow_up_questions=[
                f"Show me the {tokens[0] if tokens else 'objects'} detected {time_ref}",
                f"What was the average confidence of these detections?",
                f"Which cameras saw the most {tokens[0] if tokens else 'activity'}?"
            ]
        )

    async def _handle_trend_query(self, question: str, 
                                video_id: Optional[str] = None) -> ConversationalResponse:
        """Handle trend analysis queries."""
        # For now, provide a simplified trend response
        tokens = self._enhanced_query_tokens(question)
        start_time = self._extract_temporal_constraints(question)
        
        # Default to last 24 hours if no temporal constraint
        if not start_time:
            start_time = datetime.now(timezone.utc) - timedelta(hours=24)
        
        conditions = ["e.tenant_id = ?", "o.observed_at >= ?"]
        args: List[Any] = [self.store.tenant_id, start_time.isoformat()]
        
        if video_id:
            conditions.append("o.video_id = ?")
            args.append(video_id)
        
        # Add searchable text conditions
        searchable = "lower(e.label || ' ' || e.category || ' ' || e.color || ' ' || e.vehicle_type || ' ' || e.zone)"
        for token in tokens:
            conditions.append(f"{searchable} LIKE ?")
            args.append(f"%{token}%")
        
        with self.store._db() as db:
            # Get hourly counts for trend analysis
            hourly_counts = db.execute(
                f"""SELECT 
                        strftime('%Y-%m-%d %H:00:00', o.observed_at) as hour,
                        COUNT(*) as detection_count,
                        COUNT(DISTINCT e.track_id) as unique_tracks
                     FROM entities e JOIN observations o ON o.id = e.observation_id
                     WHERE {' AND '.join(conditions)}
                     GROUP BY hour
                     ORDER BY hour""",
                args,
            ).fetchall()
        
        if not hourly_counts:
            return ConversationalResponse(
                state="unavailable",
                answer=f"No data available for trend analysis in the specified period.",
                citations=[],
                results=[]
            )
        
        # Build trend answer
        total_detections = sum(row[1] for row in hourly_counts)
        total_unique = sum(row[2] for row in hourly_counts)
        hours_covered = len(hourly_counts)
        
        answer_parts = [
            f"Over the past {hours_covered} hour(s):",
            f"• {total_detections} total detections",
            f"• {total_unique} unique object-track combinations",
            f"• Average of {total_detections/hours_covered:.1f} detections per hour"
        ]
        
        # Show trend direction if we have enough data points
        if len(hourly_counts) >= 3:
            recent_avg = sum(row[1] for row in hourly_counts[-3:]) / 3
            earlier_avg = sum(row[1] for row in hourly_counts[:3]) / 3
            trend = "increasing" if recent_avg > earlier_avg * 1.2 else \
                   "decreasing" if recent_avg < earlier_avg * 0.8 else \
                   "stable"
            answer_parts.append(f"• Trend: {trend} activity")
        
        answer = " ".join(answer_parts)
        
        return ConversationalResponse(
            state="answered",
            answer=answer,
            citations=[],
            results=[],
            follow_up_questions=[
                "Show me the busiest hour in this period",
                "Which objects contributed most to this trend?",
                "Compare this period to the previous 24 hours"
            ]
        )

    async def _handle_comparative_query(self, question: str, 
                                      video_id: Optional[str] = None) -> ConversationalResponse:
        """Handle comparative queries."""
        # Simplified comparative analysis
        tokens = self._enhanced_query_tokens(question)
        
        # Look for comparison indicators like "more than", "less than", "vs"
        comparison_indicators = ["more than", "less than", "greater than", "fewer than", "vs", "versus"]
        has_comparison = any(indicator in question.lower() for indicator in comparison_indicators)
        
        if not has_comparison:
            # Fall back to entity search
            return await self._handle_entity_search(question, video_id, 10)
        
        # For now, provide a placeholder comparative response
        return ConversationalResponse(
            state="answered",
            answer=f"Comparative analysis for '{question}' would show differences between specified conditions in a full implementation. Currently showing general search results.",
            citations=[],
            results=[],
            follow_up_questions=[
                "Show me the baseline for comparison",
                "What statistical significance would this difference have?",
                "Which time periods show the largest differences?"
            ]
        )

    async def _handle_spatial_query(self, question: str, 
                                  video_id: Optional[str] = None) -> ConversationalResponse:
        """Handle spatial/zone-based queries."""
        tokens = self._enhanced_query_tokens(question)
        
        # Look for zone-specific terms
        zone_terms = ["zone", "area", "region", "location", "entrance", "exit", "parking", "lobby", "hallway"]
        mentions_zone = any(term in question.lower() for term in zone_terms)
        
        if mentions_zone:
            # Add zone-specific handling
            pass  # For now, fall back to general search
        
        return await self._handle_entity_search(question, video_id, 10)

    async def _handle_entity_search(self, question: str, 
                                  video_id: Optional[str] = None,
                                  limit: int = 10) -> ConversationalResponse:
        """Handle standard entity search with enhanced conversational features."""
        tokens = self._enhanced_query_tokens(question)
        start_time = self._extract_temporal_constraints(question)
        
        # Build query using enhanced token extraction
        conditions = ["e.tenant_id = ?"]
        args: List[Any] = [self.store.tenant_id]
        
        if video_id:
            conditions.append("o.video_id = ?")
            args.append(video_id)
        
        if start_time:
            conditions.append("o.observed_at >= ?")
            args.append(start_time.isoformat())
        
        # Add searchable text conditions
        searchable = "lower(e.label || ' ' || e.category || ' ' || e.color || ' ' || e.vehicle_type || ' ' || e.zone)"
        for token in tokens:
            conditions.append(f"{searchable} LIKE ?")
            args.append(f"%{token}%")
        
        row_limit = max(1, min(limit, 50))
        
        with self.store._db() as db:
            total_observations, unique_tracks = db.execute(
                f"""SELECT COUNT(*), COUNT(DISTINCT o.video_id || ':' || e.track_id)
                     FROM entities e JOIN observations o ON o.id = e.observation_id
                     WHERE {' AND '.join(conditions)}""",
                args,
            ).fetchone()
            
            if not total_observations:
                return ConversationalResponse(
                    state="unavailable",
                    answer="No stored observation supports that answer.",
                    citations=[],
                    results=[],
                    follow_up_questions=[
                        "Try a more general query",
                        "Check if data exists for the specified time period",
                        "Verify camera/spelling in your query"
                    ]
                )
            
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
            return ConversationalResponse(
                state="unavailable",
                answer="No stored observation supports that answer.",
                citations=[],
                results=[],
                follow_up_questions=[
                    "Try different keywords",
                    "Broaden your time range",
                    "Check spelling of object names"
                ]
            )
        
        # Build answer
        citations = [f"obs:{row['observation_id']}" for row in rows]
        
        if any(word in question.lower() for word in ("count", "how many", "сколько")):
            answer = f"Found {unique_tracks} unique track(s) across {total_observations} matching stored observation(s)."
        else:
            answer_parts = []
            for i, row in enumerate(rows[:5]):  # Show top 5 in answer
                answer_parts.append(
                    f"{row['label']} in {row['zone']} on stream {row['video_id']} at {row['observed_at']}"
                )
            
            if len(rows) > 5:
                answer_parts.append(f"and {len(rows) - 5} more...")
            
            answer = "; ".join(answer_parts)
        
        # Build detailed results
        results = []
        for row in rows:
            results.append(SearchResult(
                citation=f"obs:{row['observation_id']}",
                video_id=row["video_id"],
                observed_at=row["observed_at"],
                frame=row["frame_no"],
                track_id=row["track_id"],
                label=row["label"],
                category=row["category"],
                zone=row["zone"],
                confidence=row["confidence"],
                violation=bool(row["violation"]),
                metadata={
                    "bbox": json.loads(row["bbox_json"]) if row["bbox_json"] else [],
                    "color": row["color"],
                    "vehicle_type": row["vehicle_type"]
                }
            ))
        
        # Generate follow-up questions
        follow_up = []
        if tokens:
            follow_up.append(f"Show me more details about the {tokens[0]} detections")
        if start_time:
            follow_up.append(f"What happened before {start_time.strftime('%Y-%m-%d %H:%M')}?")
        else:
            follow_up.append("Show me results from the last hour")
        follow_up.append("Which cameras had the most detections?")
        
        return ConversationalResponse(
            state="answered",
            answer=answer,
            citations=citations,
            results=results,
            follow_up_questions=follow_up[:3],  # Limit to 3 follow-ups
            metadata={
                "query_tokens": tokens,
                "temporal_constraint": start_time.isoformat() if start_time else None,
                "total_observations": total_observations,
                "unique_tracks": unique_tracks
            }
        )


async def create_conversational_search_app(
    knowledge_store: KnowledgeStore,
    behavioral_learner: Optional[Any] = None,
    fleet_manager: Optional[Any] = None,
) -> web.Application:
    """Create a web application for conversational search."""
    search = ConversationalSearch(knowledge_store, behavioral_learner, fleet_manager)

    async def shutdown(app: web.Application) -> None:
        pass  # No cleanup needed for search alone

    app = web.Application()
    app["search"] = search
    app.on_shutdown.append(shutdown)

    async def chat_handler(request: web.Request) -> web.Response:
        try:
            body = await request.json()
        except Exception:
            raise web.HTTPBadRequest(text="JSON body required")
        
        question = body.get("question")
        video_id = body.get("videoId")
        limit = body.get("limit", 10)
        
        if not question or not isinstance(question, str):
            raise web.HTTPBadRequest(text="question string required")
        
        if not isinstance(limit, int) or limit < 1 or limit > 100:
            limit = 10
        
        result = await search.chat(question, video_id, limit)
        return web.json_response(asdict(result))

    app.router.add_post("/api/search/chat", chat_handler)
    app.router.add_get("/api/search/health", lambda r: web.json_response({"status": "ok", "service": "conversational_search"}))
    
    return app