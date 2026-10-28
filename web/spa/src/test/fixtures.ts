import type {
  Camera,
  ChatResponse,
  Clip,
  DemoHafniaResponse,
  EventHit,
  Principal,
  SearchResponse,
  VideoUploadResponse,
  VideoYouTubeResponse,
} from '@/api'
import type {
  AIChain,
  AttentionItem,
  Chief,
  Coverage,
  CoverageRow,
  DeviceRegistry,
  EvidenceHit,
  EvidenceMetadata,
  Handover,
  HealthState,
  IncidentDetail,
  IncidentExplanation,
  IncidentSummary,
  RecordingChain,
  RecordingClip,
  RecordingTimeline,
  Setup,
  RuleBundle,
} from '@/hooks/product'
import type { VMSCamera, VMSState } from '@/hooks/vms'

// The fixtures.
//
// One per interface the console consumes, and every one of them is a VALUE
// the product could have returned. There is no "generic" fixture and no
// `as any` cast: a fixture that does not satisfy the interface it stands for
// is a fixture that will let a shape drift through.
//
// The rule the fixtures obey: nothing here is invented at RENDER time. A
// fixture is a thing the product said. The anti-invention rule (RC-34/RC-35)
// is about the console's behaviour, and a fixture that is not typed is a
// fixture that has already broken it.

export const principal: Principal = {
  tenantId: 'tenant-demo',
  roles: ['operator'],
  siteScope: ['site-demo'],
  capabilities: ['incident.read', 'incident.write'],
  serviceAccount: true,
  timezone: 'Europe/Berlin',
}

export const principalWithoutTimezone: Principal = {
  tenantId: 'tenant-demo',
  roles: ['operator'],
  siteScope: ['site-demo'],
  capabilities: ['incident.read'],
  serviceAccount: true,
}

export const eventHit: EventHit = {
  eventId: 'evt-1',
  incidentId: 'inc-1',
  siteId: 'site-demo',
  eventType: 'motion',
  deviceId: 'cam-1',
  observedAt: '2026-10-21T08:00:00Z',
  normalizedAt: '2026-10-21T08:00:01Z',
}

export const searchResponse: SearchResponse = {
  query: 'motion',
  hits: [eventHit],
  nextCursor: '',
}

export const chatResponse: ChatResponse = {
  question: 'what happened?',
  answer: 'Motion in the loading bay.',
  state: 'answered',
  citations: ['evt-1'],
  narrated: false,
  unresolved: 0,
}

export const clip: Clip = {
  evidenceId: 'ev-1',
  siteId: 'site-demo',
  kind: 'video',
  observedAt: '2026-10-21T08:00:00Z',
  uri: '/v1/evidence/ev-1',
  mediaType: 'video/mp4',
  sizeBytes: 1024,
  sha256: 'a'.repeat(64),
}

export const camera: Camera = {
  id: 'cam-1',
  siteId: 'site-demo',
  name: 'Loading bay',
  status: 'online',
  rtsp: 'rtsp://example/stream',
}

export const videoUploadResponse: VideoUploadResponse = {
  videoId: 'vid-1',
  evidenceId: 'ev-1',
  eventId: 'evt-1',
  duplicate: false,
  sizeBytes: 1024,
  sha256: 'a'.repeat(64),
}

export const videoYouTubeResponse: VideoYouTubeResponse = {
  videoId: 'vid-2',
  evidenceId: 'ev-2',
  eventId: 'evt-2',
  duplicate: false,
  source: 'youtube',
}

export const attentionItem: AttentionItem = {
  incidentId: 'inc-1',
  siteId: 'site-demo',
  state: 'open',
  severity: 'high',
  title: 'Motion in the loading bay',
  ruleId: 'rule-motion',
  createdAt: '2026-10-21T08:00:00Z',
  degraded: false,
  rank: 1,
  threatScore: 87,
  why: {
    ruleId: 'rule-motion',
    reasonCode: 'motion_in_zone',
    replayedFromStoredFacts: true,
  },
}

// A SECOND attention item, deliberately with a LOWER threat score and a HIGHER
// severity word.
//
// The order of these two is the point of the fixture. The product's score
// decided the order and the console must not re-rank it, so a page that shows
// the high-severity item first has re-ranked the queue and is wrong.
export const attentionItemLowerScore: AttentionItem = {
  ...attentionItem,
  incidentId: 'inc-2',
  title: 'Camera offline',
  severity: 'critical',
  threatScore: 12,
  rank: 2,
  why: { ruleId: 'rule-offline', reasonCode: 'camera_offline' },
}

export const incidentSummary: IncidentSummary = {
  incidentId: 'inc-1',
  siteId: 'site-demo',
  state: 'open',
  severity: 'high',
  title: 'Motion in the loading bay',
  ruleId: 'rule-motion',
  createdAt: '2026-10-21T08:00:00Z',
  firstEventAt: '2026-10-21T07:59:58Z',
  degraded: false,
  ruleBundleVersion: 'bundle-3',
  policyVersion: 'policy-2',
}

export const incidentExplanation: IncidentExplanation = {
  incidentId: 'inc-1',
  reasons: ['motion in the loading bay zone'],
  chain: ['rule-motion', 'motion_in_zone'],
  ruleId: 'rule-motion',
  summary: 'A motion rule matched an event in the loading bay.',
}

export const incidentDetail: IncidentDetail = {
  incidentId: 'inc-1',
  siteId: 'site-demo',
  state: 'open',
  severity: 'high',
  title: 'Motion in the loading bay',
  ruleId: 'rule-motion',
  createdAt: '2026-10-21T08:00:00Z',
  firstEventAt: '2026-10-21T07:59:58Z',
  degraded: false,
  ruleBundleVersion: 'bundle-3',
  policyVersion: 'policy-2',
  verification: {
    verificationId: 'ver-1',
    state: 'confirmed',
    reasonCode: 'replay_confirmed',
    explanation: 'The stored facts reproduce the incident.',
    supportingEventIds: ['evt-1'],
  },
  events: [
    {
      eventId: 'evt-1',
      eventType: 'motion',
      deviceId: 'cam-1',
      observedAt: '2026-10-21T07:59:58Z',
      normalizedAt: '2026-10-21T08:00:01Z',
    },
  ],
  observations: [{ observationId: 'obs-1', model: 'demo-model', createdAt: '2026-10-21T08:00:02Z' }],
  evidence: [
    {
      evidenceId: 'ev-1',
      kind: 'video',
      uri: '/v1/evidence/ev-1',
      observedAt: '2026-10-21T08:00:00Z',
      sha256: 'a'.repeat(64),
    },
  ],
  dispositions: [],
}

export const healthState: HealthState = {
  status: 'ok',
  version: '0.0.0-test',
  slo: { evaluated: 1, status: 'ok' },
  checks: { database: { ok: true } },
}

export const evidenceHit: EvidenceHit = {
  eventId: 'evt-1',
  incidentId: 'inc-1',
  siteId: 'site-demo',
  eventType: 'motion',
  deviceId: 'cam-1',
  observedAt: '2026-10-21T08:00:00Z',
  normalizedAt: '2026-10-21T08:00:01Z',
  summary: 'Motion in the loading bay.',
  clipUrl: '/v1/evidence/ev-1',
}

export const coverageRow: CoverageRow = {
  kind: 'zone',
  what: 'Loading bay',
  count: 2,
  worst: 'Loading bay',
  seen: true,
}

export const coverage: Coverage = {
  sites: ['site-demo'],
  rows: [coverageRow],
}

export const handover: Handover = {
  siteId: 'site-demo',
  available: true,
  brief: {
    shift: 'day',
    site_id: 'site-demo',
    generated_at: '2026-10-21T08:00:00Z',
    schema: 'brief/v1',
    headline: 'Two incidents need a person.',
    incidents: [{ ID: 'inc-1', Title: 'Motion in the loading bay', Action: 'Review' }],
  },
}

// The Chief.
//
// The fixture is the product's own shape, so the console's rendering is
// exercised against what the product actually returns rather than against a
// shape invented for the test.
export const chief: Chief = {
  schema: 'chief/v1',
  tenantId: 'tenant-demo',
  siteId: 'site-demo',
  generatedAt: '2026-10-21T08:05:00Z',
  since: '2026-10-21T07:00:00Z',
  headline:
    '1 new incident since 2026-10-21 07:00:00Z. The most urgent is "Motion in the loading bay", scored 90. 1 camera is not watching.',
  delta: {
    since: '2026-10-21T07:00:00Z',
    measured: true,
    newIncidents: [
      {
        incidentId: 'inc-1',
        siteId: 'site-demo',
        state: 'open',
        severity: 'high',
        title: 'Motion in the loading bay',
        ruleId: 'core.motion',
        createdAt: '2026-10-21T08:04:00Z',
        degraded: false,
        threatScore: 90,
        queueScore: 88,
      },
    ],
    newActions: [],
    newOpenItems: [],
    quiet: false,
  },
  attention: [],
  autonomy: 'A0',
  effectiveAutonomy: 'A0',
}

// The Chief with a NAMED degradation. The third absence is the one that
// matters: a Chief that is quieter than usual must say why.
export const chiefDegraded: Chief = {
  ...chief,
  headline:
    'Nothing new since 2026-10-21 07:00:00Z. 1 incident is still open and waiting. The Chief is degraded: the AI plane was asked and could not answer.',
  degradation: {
    reason: 'narration_unavailable',
    detail: 'the AI plane could not answer: context deadline exceeded',
  },
}

// The recording index.
//
// The fixture is a recording with a HOLE in it, because the hole is the fact
// the page exists to publish. A fixture with no gap would let a page that
// rendered "continuous" pass whether or not it read the product's answer.
export const recordingTimeline: RecordingTimeline = {
  available: true,
  siteId: 'site-demo',
  deviceId: 'cam-1',
  from: '2026-10-22T08:00:00Z',
  to: '2026-10-22T09:00:00Z',
  gapThresholdMs: 1000,
  segments: [
    {
      segmentId: 'seg-1',
      deviceId: 'cam-1',
      seq: 1,
      startedAt: '2026-10-22T08:00:00Z',
      endedAt: '2026-10-22T08:00:10Z',
      gapBeforeMs: 0,
      bytes: 1024,
      digest: 'a'.repeat(64),
      codec: 'h264',
      width: 1920,
      height: 1080,
      storageRef: 'site-demo/cam-1/seg-1.mp4',
      state: 'complete',
      chainHash: 'b'.repeat(64),
    },
    {
      segmentId: 'seg-2',
      deviceId: 'cam-1',
      seq: 2,
      startedAt: '2026-10-22T08:01:10Z',
      endedAt: '2026-10-22T08:01:20Z',
      gapBeforeMs: 60000,
      bytes: 2048,
      digest: 'c'.repeat(64),
      codec: 'h264',
      width: 1920,
      height: 1080,
      storageRef: 'site-demo/cam-1/seg-2.mp4',
      state: 'complete',
      chainHash: 'd'.repeat(64),
    },
  ],
  gaps: [
    {
      deviceId: 'cam-1',
      startedAt: '2026-10-22T08:00:10Z',
      endedAt: '2026-10-22T08:01:10Z',
      durationMs: 60000,
    },
  ],
  continuity: {
    segmentCount: 2,
    coveredMs: 20000,
    windowMs: 3600000,
    gapCount: 1,
    gapMs: 60000,
  },
  verdict: {
    state: 'broken',
    detail: "This camera's recording has 1 hole(s) in this window.",
  },
}

// The chain, verified.
//
// The proof is the point of the recording index, so the fixture carries a
// verified chain and the page says so.
export const recordingChain: RecordingChain = {
  available: true,
  siteId: 'site-demo',
  deviceId: 'cam-1',
  day: '2026-10-22',
  verified: true,
  segmentCount: 2,
  recordedHead: 'e'.repeat(64),
  expectedHead: 'e'.repeat(64),
}

// The provider chain.
//
// The fixture is a chain with TWO providers, because the ORDER is the policy
// and a chain of one has no order to show.
export const aiChain: AIChain = {
  available: true,
  siteId: 'site-demo',
  providers: [
    {
      position: 1,
      kind: 'local_bundle',
      model: 'qwen2.5-vl',
      baseUrl: 'http://127.0.0.1:8080',
      credentialConfigured: false,
    },
    {
      position: 2,
      kind: 'anthropic',
      model: 'claude-sonnet-4-5',
      baseUrl: 'https://api.anthropic.com',
      credentialConfigured: true,
    },
  ],
  egress: [
    {
      egressId: 'egr-1',
      provider: 'anthropic',
      observationId: 'obs-1',
      profileId: 'general-monitoring',
      evidenceCount: 1,
      evidenceBytes: 20480,
      evidenceHashes: ['a'.repeat(64)],
      requestedAt: '2026-10-22T09:00:00Z',
    },
  ],
  egressAllowed: true,
  policyAvailable: true,
  systemPromptConfigured: true,
  timeoutSeconds: 30,
  chainAvailable: true,
}

// The clip for a window, with its pre-roll.
//
// The fixture has a PRE-ROLL and a segment, because the pre-roll is the
// point: it is the seconds before the window, and a clip without it starts
// in the middle of the moment.
export const recordingClip: RecordingClip = {
  available: true,
  siteId: 'site-demo',
  deviceId: 'cam-1',
  from: '2026-10-22T08:59:00Z',
  to: '2026-10-22T09:00:00Z',
  segments: [
    {
      segmentId: 'seg-2',
      seq: 2,
      startedAt: '2026-10-22T08:59:10Z',
      endedAt: '2026-10-22T08:59:20Z',
      bytes: 2048,
      digest: 'c'.repeat(64),
      state: 'complete',
      contentUrl: '/v1/recordings/seg-2/content',
    },
  ],
  preRoll: {
    segmentId: 'seg-1',
    seq: 1,
    startedAt: '2026-10-22T08:59:00Z',
    endedAt: '2026-10-22T08:59:10Z',
    bytes: 1024,
    digest: 'b'.repeat(64),
    state: 'complete',
    contentUrl: '/v1/recordings/seg-1/content',
  },
  gaps: [],
  coveredMs: 10000,
  windowMs: 60000,
}

// The setup path.
//
// The fixture is a site with work LEFT to do, because that is the case the
// page exists for: a page that only ever renders "everything is done" is a
// page nobody can trust when something is not.
export const setup: Setup = {
  siteId: 'site-demo',
  doneCount: 2,
  totalCount: 6,
  ready: false,
  next: 'site',
  steps: [
    {
      id: 'database',
      title: 'The database answers',
      done: true,
      detail: 'The database answered.',
    },
    {
      id: 'schema',
      title: 'The schema is current',
      done: true,
      detail: 'The schema is at version 33.',
    },
    {
      id: 'site',
      title: 'The site exists',
      done: false,
      detail: 'This tenant has no site. Everything the product records belongs to one.',
      action: 'Run security-seed, or add a site in the console.',
    },
    {
      id: 'devices',
      title: 'A camera is registered',
      done: false,
      detail: 'No device is registered yet.',
      action: 'Add the camera on the Devices page, or run the discovery scan.',
    },
  ],
}

// The device registry.
//
// Two devices, and the difference between them is the whole point: one was
// MEASURED and one was not. A device nobody has probed has no capabilities,
// which is not the same as a device with none.
export const deviceRegistry: DeviceRegistry = {
  available: true,
  items: [
    {
      deviceId: 'cam-1',
      siteId: 'site-demo',
      kind: 'camera',
      displayName: 'Loading bay',
      enabled: true,
      make: 'Acme',
      model: 'Bullet-1',
      firmware: '2.4.1',
      probed: true,
      probedAt: '2026-10-21T07:00:00Z',
      probeId: 'probe-1',
      capabilities: ['onvif.profile_s', 'onvif.profile_t'],
    },
    {
      deviceId: 'cam-2',
      siteId: 'site-demo',
      kind: 'camera',
      enabled: true,
      probed: false,
      capabilities: [],
    },
  ],
  vocabulary: {
    credentialPurposes: ['rtsp', 'onvif', 'ftp', 'other'],
    capabilityStates: ['unverified', 'verified', 'contradicted', 'unsupported'],
    onvifCapabilities: [
      'onvif.device',
      'onvif.media',
      'onvif.analytics',
      'onvif.events',
      'onvif.imaging',
      'onvif.ptz',
    ],
    onvifProfiles: [
      'onvif.profile_s',
      'onvif.profile_t',
      'onvif.profile_g',
      'onvif.profile_m',
      'onvif.profile_a',
      'onvif.profile_d',
      'onvif.profile_ptz',
    ],
  },
}

// One evidence row's metadata, as the product holds it.
export const evidenceMetadata: EvidenceMetadata = {
  evidenceId: 'ev-1',
  kind: 'clip',
  tier: 'A',
  storageRef: 'ab/cd/abcdef',
  sha256: 'a'.repeat(64),
  sizeBytes: 1310720,
  capturedAt: '2026-10-21T08:00:00Z',
  storedAt: '2026-10-21T08:00:01Z',
  legalHold: false,
}

export const ruleBundle: RuleBundle = {
  version: 'bundle-3',
  activatedAt: '2026-10-20T08:00:00Z',
  ruleCount: 12,
  digest: 'b'.repeat(64),
  active: true,
}

export const vmsCamera: VMSCamera = {
  id: 'cam-1',
  name: 'Loading bay',
  kind: 'dome',
  location: 'Loading bay',
  fps: 25,
  frameCount: 42,
  measured: 25,
  latencyMs: 120,
  paused: false,
  detections: [{ class: 'person', confidence: 0.91, x1: 10, y1: 20, x2: 60, y2: 180 }],
}

export const vmsState: VMSState = {
  vendor: 'demo',
  now: '2026-10-21T08:00:00Z',
  detector: {
    modelPath: '/models/detector.onnx',
    sha256: 'c'.repeat(64),
    bytes: 1024,
    licence: 'demo',
    provider: 'cpu',
    cudaDeviceID: -1,
    runtimeVersion: '1.0.0',
    classes: 1,
    classesDeclared: true,
  },
}

// The operator's own drawing of the site.
//
// The shape is the one `src/pages/Site.tsx` declares, and it is the SHAPE, not
// a product contract: the site model is the operator's YAML, served back by
// the product. It is here because the page needs something to draw.
export const siteModel = {
  siteId: 'site-demo',
  zones: [
    {
      id: 'zone-loading',
      type: 'restricted',
      name: 'Loading bay',
      polygon: [
        { x: 0, y: 0 },
        { x: 100, y: 0 },
        { x: 100, y: 100 },
        { x: 0, y: 100 },
      ],
    },
  ],
  cameras: [{ id: 'cam-1', zoneIds: ['zone-loading'], position: { x: 50, y: 50 } }],
  routes: [
    {
      id: 'route-1',
      points: [
        { x: 0, y: 0 },
        { x: 100, y: 100 },
      ],
      allowedDirection: 'both',
    },
  ],
}

// The demo bridge answer the local Hafnia test server returns.
//
// Every field is synthetic/demo data; the response carries `simulated: true`.
export const demoHafnia: DemoHafniaResponse = {
  event: {
    id: 'evt-motion-001',
    type: 'motion',
    camera: 'cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c',
    occurredAt: '2026-07-01T12:00:00Z',
  },
  hafnia: {
    degraded: false,
    reason: 'none',
    detail: '',
    structured: {
      summary: 'a person crossed the restricted zone',
      sceneDescription: 'A person is crossing the restricted zone at the entrance camera.',
      confidence: 0.9,
      abnormalEvent: true,
      hazardVisible: false,
      entities: ['person'],
      recommendedAction: 'Review the restricted-zone footage.',
    },
    model: 'hafnia-8b',
  },
  bookmark: {
    placed: true,
    ref: 'bmk-demo-00000000',
    refusal: null,
  },
  simulated: true,
}
