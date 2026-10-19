export interface EventHit {
  eventId: string
  incidentId: string
  siteId: string
  eventType: string
  deviceId?: string
  zoneId?: string
  correlationId?: string
  observedAt: string
  normalizedAt: string
}

export interface SearchResponse {
  query: string
  hits: EventHit[]
  nextCursor: string
}

export interface SearchParams {
  q: string
  siteId?: string
  from?: string
  to?: string
  limit?: number
}

export interface ChatResponse {
  question: string
  answer: string
  state: 'answered' | 'partial' | 'unavailable'
  citations: string[]
  videoId?: string
  narrated: boolean
  unresolved: number
}

export interface ChatParams {
  question: string
  videoId?: string
}

export interface Clip {
  evidenceId: string
  siteId: string
  kind: string
  observedAt: string
  uri: string
  mediaType?: string
  sizeBytes?: number
  sha256?: string
  incidentId?: string
}

export interface CriticalStream {
  incidentId: string
  videoId: string
  siteId: string
  score: number
  reasons: string[]
  state: string
  severity: string
  title: string
  firstAt: string
  lastAt: string
  ruleId?: string
}

export interface Camera {
  id: string
  siteId: string
  name: string
  status: 'online' | 'offline' | 'degraded'
  rtsp?: string
  zones?: unknown[]
}

export interface VideoUploadResponse {
  videoId: string
  evidenceId: string
  eventId: string
  duplicate: boolean
  sizeBytes: number
  sha256: string
}

export interface VideoYouTubeRequest {
  url: string
  siteId?: string
}

export interface VideoYouTubeResponse {
  videoId: string
  evidenceId: string
  eventId: string
  duplicate: boolean
  source: string
}

export interface RulesFromTextRequest {
  text: string
  siteId?: string
}

export interface RulesFromTextResponse {
  rule: string
}

export interface Detection {
  label: string
  confidence: number
  bbox: number[]
}

export interface AIRuntimeStatus {
  available: boolean
  reason: string | null
  model: string | null
}
