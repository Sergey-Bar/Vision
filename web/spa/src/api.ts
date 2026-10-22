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

// The configured principal.
//
// The console has no login, so there is no user to show: this is the role
// and the site scope the API is configured with. `timezone` is the site's
// own, and the console renders every time in it.
export interface Principal {
  tenantId: string
  roles: string[]
  // The site scope is a LIST, and it was declared as a `string` here while
  // the product, the published OpenAPI contract, `src/hooks/product.ts`,
  // `src/pages/Users.tsx` and `src/pages/Upload.tsx` all treat it as one.
  // The contract says `type: array, items: { type: string }`, and
  // `internal/api/handlers.go` writes `p.SiteScope`, which is a `[]string`.
  //
  // So the type was the odd one out, and it was wrong in a way that is
  // invisible until the value arrives: `Users.tsx` maps over it, and a string
  // has no `.map`, so the page threw and the screen went blank. The screen
  // going blank is the console's most honest-looking failure mode, which is
  // exactly why it needed a test rather than a careful reader.
  siteScope: string[]
  capabilities: string[]
  serviceAccount: boolean
  timezone?: string
}