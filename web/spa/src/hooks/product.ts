import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { get, post } from '../lib/api'

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
  // The reason the chain is not there, when it is not there. A missing
  // reason is an absence, and the screen says which.
  whyUnavailable?: string
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

// The coverage panel.
//
// Four rows, always the same four, each a count over a site-scoped table with
// the reason and the worst instance named. It is NOT a camera list: a camera
// the platform has heard of is a different fact from a camera that was
// watching, and the panel is about the second one.
//
// The scope is in the answer, because a count with no scope is a number nobody
// can check.
export interface CoverageRow {
  kind: string
  what: string
  count: number
  worst?: string
  since?: string
  seen: boolean
  breaking?: boolean
}

export interface Coverage {
  sites: string[]
  rows: CoverageRow[]
}

// The shift hand-over.
//
// The brief is the artifact the shift wrote, in its published `brief/v1`
// shape. `available` is false when the install has no artifact, and
// `unavailable` says why -- an empty hand-over and a missing one are different
// answers, and only one of them is true.
export interface HandoverBrief {
  shift?: string
  site_id?: string
  generated_at?: string
  schema?: string
  headline?: string
  fleet_degraded?: number
  incidents?: { ID?: string; Title?: string; Action?: string; AutonomyState?: string; ThreatScore?: number; Summary?: string }[]
  cameras?: { ID?: string; State?: string; Reason?: string }[]
  coverage?: { ZoneID?: string; DeclaredCameras?: string[]; DegradedCameras?: string[]; UnderObserved?: boolean; Unanswerable?: boolean }[]
  notes?: { TS?: string; Actor?: string; Text?: string; Camera?: string }[]
  open_items?: { ID?: string; Kind?: string; Summary?: string }[]
}

export interface Handover {
  siteId: string
  available: boolean
  unavailable?: string
  brief?: HandoverBrief
  dutyLog?: {
    path?: string
    shift?: string
    siteId?: string
    lines?: number
    headHash?: string
    verification?: string
    dump?: string
    dumpNote?: string
  }
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

// The coverage panel, read from the product.
//
// The product serves it under /v1 because it is the PRODUCT's view of what it
// is watching, and the console does not assemble it. The server-rendered
// console page and this route read the same rows through the same reader, so
// the two cannot disagree about what the product could not see.
export function useCoverage() {
  return useQuery({
    queryKey: ['coverage'],
    queryFn: async () => {
      const res = await get('/v1/coverage')
      return (await res.json()) as Coverage
    },
    refetchInterval: 10_000,
    retry: 1,
  })
}

// The shift hand-over, read from the product.
//
// The brief is the artifact the shift wrote. The console does not read the
// duty log itself: it is a file the product hashes, and a second reader of a
// hashed file is a second opinion about an integrity check.
export function useHandover() {
  return useQuery({
    queryKey: ['handover'],
    queryFn: async () => {
      const res = await get('/v1/handover')
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

// The Chief of Security.
//
// The Chief is served by the PRODUCT, and the CURSOR is carried in the URL.
//
// The cursor is not stored anywhere. Not in the browser's storage — this
// console has no browser-side persistence at all, and a cursor there would be
// a second reader of the record. Not on the server — that would be a table
// of what each operator looked at, which is a record of a person the product
// would then have to keep and chain.
//
// The URL is durable, bookmarkable, survives a reload and survives an
// interruption, and it is gone when the operator closes the tab. That is the
// correct lifetime for "where I was".
export interface ChiefItem {
  incidentId: string
  siteId: string
  state: string
  severity: string
  title: string
  ruleId: string
  createdAt: string
  degraded: boolean
  degradedReasons?: string[]
  threatScore: number
  queueScore: number
}

export interface ChiefNarration {
  text: string
  provider: string
  model: string
  modelIdentity?: string
  contractVersion: string
  temperature: number
  inputDigest: string
  citations: string[]
}

export interface ChiefDegradation {
  reason: string
  detail?: string
}

export interface ChiefDelta {
  since: string
  // measured is false when there was no cursor to measure against. A delta
  // with no cursor is the WHOLE PICTURE and is never reported as quiet.
  measured: boolean
  newIncidents: ChiefItem[]
  newActions: unknown[]
  newOpenItems: unknown[]
  quiet: boolean
  quietReason?: string
  note?: string
}

export interface Chief {
  schema: string
  tenantId?: string
  siteId?: string
  generatedAt: string
  since: string
  headline: string
  delta: ChiefDelta
  attention: ChiefItem[]
  brief?: unknown
  narration?: ChiefNarration
  degradation?: ChiefDegradation
  autonomy: 'A0' | 'A1' | 'A2'
  effectiveAutonomy: 'A0' | 'A1' | 'A2'
}

export function useChief(since: string | null) {
  return useQuery({
    queryKey: ['chief', since],
    queryFn: async () => {
      const url = new URL('/v1/chief', window.location.origin)
      if (since) url.searchParams.set('since', since)
      const res = await get(url.pathname + url.search)
      return (await res.json()) as Chief
    },
    refetchInterval: 10_000,
    retry: 1,
  })
}

// The device registry.
//
// The console reads the product's own registry. It does not hold a camera
// list of its own, because a camera list in the browser is a camera list
// nobody measured: the registry holds what was MEASURED, and the console
// would hold what somebody remembered.
export interface Device {
  deviceId: string
  siteId: string
  zoneId?: string
  kind: string
  displayName?: string
  enabled: boolean
  make?: string
  model?: string
  firmware?: string
  onvifDeviceId?: string
  rtspRef?: string
  substreamProfile?: string
  // probed is false for a device nobody has measured. It is NOT the same as
  // a device with no capabilities: an empty capability list reads as "we
  // looked and it can do nothing".
  probed: boolean
  probedAt?: string
  probeId?: string
  capabilities: string[]
}

export interface DeviceVocabulary {
  credentialPurposes: string[]
  capabilityStates: string[]
  onvifCapabilities: string[]
  onvifProfiles: string[]
}

export interface DeviceRegistry {
  available: boolean
  unavailable?: string
  items: Device[]
  vocabulary?: DeviceVocabulary
}

export function useDevices() {
  return useQuery({
    queryKey: ['devices'],
    queryFn: async () => {
      const res = await get('/v1/devices')
      return (await res.json()) as DeviceRegistry
    },
    refetchInterval: 15_000,
    retry: 1,
  })
}

// registerDevice records a device in the registry.
//
// It does NOT set a capability. A device acquires capabilities by being
// MEASURED, and a registration that could assert one would be a way for a
// caller to say a camera can do something it has never been asked about.
export function useRegisterDevice() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: { deviceId: string; address?: string; kind?: string; displayName?: string }) => {
      const res = await post('/v1/devices', input)
      return (await res.json()) as { deviceId: string; siteId: string; registered: boolean; probed: boolean }
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['devices'] })
    },
  })
}

// probeDevice MEASURES a device and records what it established.
//
// Everything before this is a claim somebody typed; everything after it is a
// measurement.
export function useProbeDevice() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (deviceId: string) => {
      const res = await post(`/v1/devices/${encodeURIComponent(deviceId)}/probe`, {})
      return (await res.json()) as unknown
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['devices'] })
    },
  })
}

// The setup path.
//
// The product answers it, and the console renders the answer. A console that
// decided for itself what was left to do would be a console with its own
// opinion about the install, and its opinion would be wrong the moment the
// install changed.
export interface SetupStep {
  id: string
  title: string
  done: boolean
  detail: string
  // action says what to do next, and it is empty when the step is done. A
  // step that is not done and does not say what to do is a complaint.
  action?: string
}

export interface Setup {
  siteId?: string
  steps: SetupStep[]
  doneCount: number
  totalCount: number
  // ready is a claim about the PRODUCT: whether it can record.
  ready: boolean
  // next is the first unfinished step, because the steps are ordered by
  // dependency.
  next?: string
}

export function useSetup() {
  return useQuery({
    queryKey: ['setup'],
    queryFn: async () => {
      const res = await get('/v1/setup')
      return (await res.json()) as Setup
    },
    refetchInterval: 30_000,
    retry: 1,
  })
}

// The recording index.
//
// The console reads the product's own index, and the index publishes the
// MEASURED gap rather than a summary of it. A page that said "recording
// healthy" because no incident fired would be a page that cannot tell the
// operator the disk has been full since Tuesday.
//
// The three states of the verdict are three DIFFERENT facts, and the console
// renders them differently: a camera that recorded nothing, a camera whose
// recording has a hole in it, and a camera whose recording is continuous.
export interface RecordingSegment {
  segmentId: string
  deviceId: string
  seq: number
  startedAt: string
  endedAt: string
  gapBeforeMs: number
  bytes: number
  digest: string
  codec?: string
  width?: number
  height?: number
  storageRef: string
  state: 'complete' | 'partial'
  chainHash?: string
}

export interface RecordingGap {
  deviceId: string
  startedAt: string
  endedAt: string
  durationMs: number
}

export interface RecordingContinuity {
  segmentCount: number
  coveredMs: number
  windowMs: number
  gapCount: number
  gapMs: number
}

export interface RecordingVerdict {
  state: 'no_recording' | 'broken' | 'continuous'
  detail: string
}

export interface RecordingTimeline {
  available: boolean
  unavailable?: string
  siteId?: string
  deviceId?: string
  from?: string
  to?: string
  gapThresholdMs?: number
  segments: RecordingSegment[]
  gaps: RecordingGap[]
  continuity: RecordingContinuity
  verdict: RecordingVerdict
}

export interface RecordingChain {
  available: boolean
  unavailable?: string
  siteId?: string
  deviceId?: string
  day?: string
  verified: boolean
  reason?: string
  segmentCount?: number
  recordedHead?: string
  expectedHead?: string
  brokenAtSeq?: number
}

// useRecordingTimeline reads one camera's recording and whether it is
// continuous.
//
// The camera is required, because a timeline is a CAMERA's timeline and a
// request that named nothing is a request that asked nothing.
export function useRecordingTimeline(deviceId: string | null) {
  return useQuery({
    queryKey: ['recordings', 'timeline', deviceId],
    queryFn: async () => {
      const url = new URL('/v1/recordings/timeline', window.location.origin)
      if (deviceId) url.searchParams.set('deviceId', deviceId)
      const res = await get(url.pathname + url.search)
      return (await res.json()) as RecordingTimeline
    },
    enabled: Boolean(deviceId),
    refetchInterval: 15_000,
    retry: 1,
  })
}

// useRecordingChain reads whether a camera's recording can be PROVED.
//
// The chain is recomputed by the product from the rows and compared with the
// head the product stored, so a mismatch is visible rather than inferred.
export function useRecordingChain(deviceId: string | null) {
  return useQuery({
    queryKey: ['recordings', 'chain', deviceId],
    queryFn: async () => {
      const url = new URL('/v1/recordings/chain', window.location.origin)
      if (deviceId) url.searchParams.set('deviceId', deviceId)
      const res = await get(url.pathname + url.search)
      return (await res.json()) as RecordingChain
    },
    enabled: Boolean(deviceId),
    retry: 1,
  })
}

// The clip for a window, and its pre-roll.
export interface RecordingClipSegment {
  segmentId: string
  seq: number
  startedAt: string
  endedAt: string
  bytes: number
  digest: string
  state: 'complete' | 'partial'
  contentUrl: string
}

export interface RecordingClip {
  available: boolean
  siteId?: string
  deviceId?: string
  from?: string
  to?: string
  segments: RecordingClipSegment[]
  // preRoll is the segment immediately BEFORE the window -- the lead-in. It
  // is null when the window starts at the beginning of what was recorded,
  // and that is a fact about the recording rather than a gap in the clip.
  preRoll?: RecordingClipSegment | null
  gaps: RecordingGap[]
  coveredMs: number
  windowMs: number
}

export function useRecordingClip(deviceId: string | null, from?: string, to?: string) {
  return useQuery({
    queryKey: ['recordings', 'clip', deviceId, from, to],
    queryFn: async () => {
      const url = new URL('/v1/recordings/clip', window.location.origin)
      if (deviceId) url.searchParams.set('deviceId', deviceId)
      if (from) url.searchParams.set('from', from)
      if (to) url.searchParams.set('to', to)
      const res = await get(url.pathname + url.search)
      return (await res.json()) as RecordingClip
    },
    enabled: Boolean(deviceId),
    retry: 1,
  })
}

// The provider chain: which models may answer, in what order, and what has
// left the site when they did.
//
// The ORDER is the policy, and the console shows it as a list in that order
// rather than as a set, because the order is what says which provider the
// site prefers and what it falls back to.
export interface AIProvider {
  position: number
  kind: 'openai_compatible' | 'anthropic' | 'local_bundle'
  model: string
  baseUrl?: string
  // credentialConfigured says whether a credential is configured. Never the
  // credential.
  credentialConfigured: boolean
}

export interface AIEgressRecord {
  egressId: string
  provider: string
  observationId: string
  profileId: string
  evidenceCount: number
  evidenceBytes: number
  // evidenceHashes are the DIGESTS of what left. The content is never in the
  // log, because a log of what left that kept what left would be a second
  // copy of the evidence.
  evidenceHashes: string[]
  requestedAt: string
}

export interface AIChain {
  available: boolean
  unavailable?: string
  siteId?: string
  providers: AIProvider[]
  egress: AIEgressRecord[]
  egressAllowed?: boolean
  policyAvailable?: boolean
  policyUnavailable?: string
  systemPromptConfigured?: boolean
  timeoutSeconds?: number
  chainAvailable?: boolean
  chainUnavailable?: string
}

export function useAIChain() {
  return useQuery({
    queryKey: ['ai', 'chain'],
    queryFn: async () => {
      const res = await get('/v1/ai/chain')
      return (await res.json()) as AIChain
    },
    refetchInterval: 30_000,
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
        // The site's own timezone. The console renders every time in it, and
        // says UTC when the product has not said.
        timezone?: string
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

// ---------- one incident, and its parts ---------------------------------------

// The incident, as the product holds it.
//
// The detail is the whole record for one incident: the summary, the stored
// decision and its reason, the events, the observations, the evidence and
// the dispositions. The console draws it; it does not assemble it.
export interface IncidentDetail {
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
  verification?: {
    verificationId: string
    state: string
    reasonCode: string
    explanation?: string
    supportingEventIds?: string[]
    contradictingEventIds?: string[]
    aiDegradation?: string
    evaluatedAt?: string
  }
  events?: {
    eventId: string
    eventType: string
    deviceId?: string
    zoneId?: string
    observedAt: string
    normalizedAt?: string
  }[]
  observations?: { observationId: string; model?: string; createdAt?: string; degraded?: boolean }[]
  evidence?: { evidenceId: string; kind?: string; uri?: string; observedAt?: string; sha256?: string }[]
  dispositions?: { dispositionId: string; outcome: string; agreement: string; actor?: string; at?: string; note?: string }[]
}

export function useIncident(incidentId: string | null) {
  return useQuery({
    queryKey: ['incident', incidentId],
    queryFn: async () => {
      const res = await get(`/v1/incidents/${encodeURIComponent(incidentId ?? '')}`)
      return (await res.json()) as IncidentDetail
    },
    enabled: !!incidentId,
    retry: 1,
  })
}

// The evidence the incident cites, with its own reason.
export function useIncidentEvidence(incidentId: string | null) {
  return useQuery({
    queryKey: ['incident-evidence', incidentId],
    queryFn: async () => {
      const res = await get(`/v1/incidents/${encodeURIComponent(incidentId ?? '')}/evidence`)
      const data = (await res.json()) as { items?: unknown[] }
      return data.items ?? []
    },
    enabled: !!incidentId,
    retry: 1,
  })
}

// ---------- one evidence row ---------------------------------------------------

// One evidence row's metadata: the kind, when it was captured, the digest and
// the size.
//
// The BYTES are the product's and the console links to them. The METADATA is
// the product's too, and the console does not carry a second copy of it: a
// digest rendered by the console is a digest nobody checked.
export interface EvidenceMetadata {
  evidenceId: string
  kind: string
  tier: string
  storageRef?: string
  sha256: string
  sizeBytes?: number
  capturedAt?: string
  storedAt?: string
  legalHold?: boolean
}

export function useEvidenceMetadata(evidenceId: string | null) {
  return useQuery({
    queryKey: ['evidence-metadata', evidenceId],
    queryFn: async () => {
      const res = await get(`/v1/evidence/${encodeURIComponent(evidenceId ?? '')}`)
      return (await res.json()) as EvidenceMetadata
    },
    enabled: !!evidenceId,
    retry: 1,
  })
}

// ---------- the rule bundles ---------------------------------------------------

// The bundles that are STORED.
//
// Stored is not active. The bundle list says what is on disk; the active
// bundle says what is running. The two are different facts and the screen
// that shows both must not let one look like the other.
export interface RuleBundle {
  version: string
  activatedAt?: string
  ruleCount?: number
  digest?: string
  active?: boolean
}

export function useRuleBundles() {
  return useQuery({
    queryKey: ['rule-bundles'],
    queryFn: async () => {
      const res = await get('/v1/rules/bundles')
      const data = (await res.json()) as { items?: RuleBundle[]; bundles?: RuleBundle[] }
      return data.items ?? data.bundles ?? []
    },
    retry: 1,
  })
}

// ---------- operations ----------------------------------------------------------

// The dead letters: the events that reached a terminal state that is not
// "handled".
export interface DeadLetter {
  id?: string
  kind?: string
  siteId?: string
  reason?: string
  receivedAt?: string
  attempts?: number
}

export function useDeadLetters(limit = 50) {
  return useQuery({
    queryKey: ['dead-letters', limit],
    queryFn: async () => {
      const res = await get(`/v1/ops/dead-letters?limit=${limit}`)
      const data = (await res.json()) as { items?: DeadLetter[] }
      return data.items ?? []
    },
    retry: 1,
  })
}

// The clock drift: how far each source's clock is from the one the platform
// trusts.
export interface ClockDrift {
  source?: string
  observedAt?: string
  skewMs?: number
  corrected?: boolean
  reason?: string
}

export function useClockDrift() {
  return useQuery({
    queryKey: ['clock-drift'],
    queryFn: async () => {
      const res = await get('/v1/ops/clock-drift')
      const data = (await res.json()) as { items?: ClockDrift[] }
      return data.items ?? []
    },
    retry: 1,
  })
}

// The readiness probe: what the product says it can do right now.
export function useReadiness() {
  return useQuery({
    queryKey: ['readiness'],
    queryFn: async () => {
      const res = await get('/v1/health/ready')
      return (await res.json()) as {
        status?: string
        checks?: Record<string, { ok: boolean; detail?: string }>
      }
    },
    refetchInterval: 10_000,
    retry: 1,
  })
}