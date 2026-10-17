const API = typeof window !== 'undefined' ? (window as any).__API_BASE__ || '' : ''

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

export interface ChatResponse {
  question: string
  answer: string
  state: 'answered' | 'partial' | 'unavailable'
  citations: string[]
  videoId?: string
  narrated: boolean
  unresolved: number
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

export async function searchFree(q: string, opts?: {
  siteId?: string
  from?: string
  to?: string
  limit?: number
}): Promise<SearchResponse> {
  const body: any = { q }
  if (opts?.siteId) body.siteId = opts.siteId
  if (opts?.from) body.from = opts.from
  if (opts?.to) body.to = opts.to
  if (opts?.limit) body.limit = opts.limit
  const res = await fetch(`${API}/api/search/free`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) throw new Error(await res.text())
  return res.json()
}

export async function chat(question: string, videoId?: string): Promise<ChatResponse> {
  const res = await fetch(`${API}/api/chat`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ question, videoId }),
  })
  if (!res.ok) throw new Error(await res.text())
  return res.json()
}

export async function listClips(videoId?: string): Promise<Clip[]> {
  const res = await fetch(`${API}/api/clips?videoId=${encodeURIComponent(videoId ?? '')}`, { cache: 'no-store' })
  if (!res.ok) throw new Error(await res.text())
  const data: { clips: Clip[] } = await res.json()
  return data.clips
}

export async function listCritical(limit = 5): Promise<CriticalStream[]> {
  const params = new URLSearchParams({ limit: String(limit) })
  const res = await fetch(`${API}/api/critical?${params}`, { cache: 'no-store' })
  if (!res.ok) throw new Error(await res.text())
  const data: { streams: CriticalStream[] } = await res.json()
  return data.streams
}
