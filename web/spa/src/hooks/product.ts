import { useQuery } from '@tanstack/react-query'
import { get } from '../lib/api'

// The product's own answers.
//
// The console asks the product for what the product holds and the VMS for what
// the VMS holds, and it never makes either up. A panel that cannot be filled
// says so, because a panel with invented numbers in it is worse than an empty
// one: the numbers would be believed.

export interface IncidentSummary {
  incidentId: string
  siteId: string
  state: string
  severity: string
  title: string
  ruleId: string
  createdAt: string
  firstEventAt: string
  degraded: boolean
  degradedReasons?: string[]
  ruleBundleVersion: string
  policyVersion: string
}

export interface IncidentExplanation {
  incidentId?: string
  reasons?: string[]
  chain?: string[]
  ruleId?: string
  summary?: string
}

export interface HealthState {
  status?: string
  version?: string
  slo?: { evaluated: number; status: string; breached?: string[] }
  checks?: Record<string, { ok: boolean; detail?: string }>
}

export interface EvidenceHit {
  eventId: string
  incidentId?: string
  siteId?: string
  eventType: string
  deviceId?: string
  observedAt?: string
  normalizedAt?: string
  summary?: string
  clipUrl?: string
  // evidenceId is the clip the moment was captured in, when the incident has
  // one. The content is served by the product's evidence route, so the console
  // links to it and does not carry the bytes.
  evidenceId?: string
}

export interface CoverageEntry {
  cameraId?: string
  camera?: string
  siteId?: string
  zone?: string
  state?: string
  covered?: boolean
  reason?: string
  lastEventAt?: string
}

export interface Handover {
  siteId?: string
  generatedAt?: string
  summary?: string
  openIncidents?: number
  notes?: string[]
}

// The attention queue: the product's own incidents, newest first.
//
// The ORDER is the product's. The console does not re-rank them: a queue the
// console re-ranks is a queue whose order nobody can explain, and "why this,
// why now" is a question about the product's decision, not about the panel.
// The attention queue: the PRODUCT's ranked list, with the reason chain.
//
// It is one call, and the order is the product's. The console does not rank it
// itself: two operators must see the same order for the same site, and a
// console that ranked in the browser would give them two.
export interface AttentionItem {
  incidentId: string
  siteId: string
  state: string
  severity: string
  title: string
  ruleId: string
  createdAt: string
  degraded: boolean
  degradedReasons?: string[]
  rank: number
  // threatScore is the autonomy layer's score, 0..100, and the queue is
  // ordered by it. The chain beside it is what makes the order checkable.
  threatScore: number
  threatReasonChain?: { key?: string; rule?: string; factor?: number; evidence?: string; at?: string }[]
  why?: {
    ruleId?: string
    reasonCode?: string
    replayedFromStoredFacts?: boolean
    steps?: { key?: string; rule?: string; factor?: number; evidence?: string }[]
  } | null
  whyUnavailable?: string
}

export function useAttention(limit = 20) {
  return useQuery({
    queryKey: ['attention', limit],
    queryFn: async () => {
      const res = await get(`/v1/attention?limit=${limit}`)
      const data = (await res.json()) as { items: AttentionItem[] }
      return data.items ?? []
    },
    refetchInterval: 5_000,
    retry: 1,
  })
}

export function useIncidents(limit = 20) {
  return useQuery({
    queryKey: ['incidents', limit],
    queryFn: async () => {
      const res = await get(`/v1/incidents?limit=${limit}`)
      const data = (await res.json()) as { items: IncidentSummary[] }
      return data.items ?? []
    },
    refetchInterval: 5_000,
    retry: 1,
  })
}

export function useIncidentExplanation(incidentId: string | null) {
  return useQuery({
    queryKey: ['incident-explanation', incidentId],
    queryFn: async () => {
      const res = await get(`/v1/incidents/${encodeURIComponent(incidentId ?? '')}/explanation`)
      return (await res.json()) as IncidentExplanation
    },
    enabled: !!incidentId,
    retry: 1,
  })
}

export function useHealth() {
  return useQuery({
    queryKey: ['health'],
    queryFn: async () => {
      const res = await get('/v1/health')
      return (await res.json()) as HealthState
    },
    refetchInterval: 5_000,
    retry: 1,
  })
}

export function useCoverage() {
  return useQuery({
    queryKey: ['coverage'],
    queryFn: async () => {
      const res = await get('/coverage')
      return (await res.json()) as { cameras?: CoverageEntry[]; entries?: CoverageEntry[] }
    },
    refetchInterval: 10_000,
    retry: 1,
  })
}

export function useHandover() {
  return useQuery({
    queryKey: ['handover'],
    queryFn: async () => {
      const res = await get('/handover')
      return (await res.json()) as Handover
    },
    refetchInterval: 30_000,
    retry: 1,
  })
}

export function useAuditLedger(limit = 20) {
  return useQuery({
    queryKey: ['audit-ledger', limit],
    queryFn: async () => {
      const res = await get(`/v1/audit/ledger?limit=${limit}`)
      const data = (await res.json()) as { entries?: unknown[] }
      return data.entries ?? []
    },
    retry: 1,
  })
}

// The principal the API is configured to act as.
//
// The console has no login, so this is not "who am I" -- it is "what is this
// install acting as", and the console shows it rather than implying a person.
export function usePrincipal() {
  return useQuery({
    queryKey: ['principal'],
    queryFn: async () => {
      const res = await get('/v1/principal')
      return (await res.json()) as {
        tenantId: string
        roles: string[]
        siteScope: string[]
        capabilities: string[]
        serviceAccount: boolean
      }
    },
    retry: 1,
  })
}

// The recent record: the structured path over the canonical events, newest
// first.
//
// No model and no prose: every row is an event with a time on it, which is the
// only shape a reader can go and check against the picture.
export function useRecentEvents(limit = 20) {
  return useQuery({
    queryKey: ['recent-events', limit],
    queryFn: async () => {
      const res = await get(`/v1/evidence?limit=${limit}`)
      const data = (await res.json()) as { hits?: EvidenceHit[]; results?: EvidenceHit[]; items?: EvidenceHit[] }
      return data.hits ?? data.results ?? data.items ?? []
    },
    refetchInterval: 5_000,
    retry: 1,
  })
}

// The search: a question, and the moments it found.
//
// The free-text path is the deterministic one: the product's structured search
// over the canonical record. The moments are what comes back -- events with
// their times and their cameras -- and not prose about them.
export function useEvidenceSearch(q: string, enabled = true) {
  return useQuery({
    queryKey: ['evidence-search', q],
    queryFn: async () => {
      const url = new URL(`/v1/evidence`, window.location.origin)
      if (q) url.searchParams.set('q', q)
      const res = await get(url.pathname + url.search)
      const data = (await res.json()) as { hits?: EvidenceHit[]; results?: EvidenceHit[]; items?: EvidenceHit[] }
      return data.hits ?? data.results ?? data.items ?? []
    },
    enabled: enabled && q.trim().length > 0,
    retry: 1,
  })
}
