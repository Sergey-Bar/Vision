import { http, HttpResponse } from 'msw'
import {
  aiChain,
  attentionItem,
  attentionItemLowerScore,
  camera,
  chatResponse,
  chief,
  clip,
  coverage,
  deviceRegistry,
  evidenceHit,
  evidenceMetadata,
  eventHit,
  handover,
  healthState,
  incidentDetail,
  incidentExplanation,
  incidentSummary,
  principal,
  recordingChain,
  recordingTimeline,
  recordingClip,
  ruleBundle,
  setup,
  siteModel,
  videoUploadResponse,
  videoYouTubeResponse,
  vmsCamera,
  vmsState,
} from './fixtures'

// One handler per route the console reads.
//
// The console's HTTP surface is small and it is listed here in full, because a
// route that is NOT here is a route the console calls and the test harness
// does not know about. `server.listen({ onUnhandledRequest: 'error' })` turns
// that into a failure rather than a silence, which is the whole point: a test
// that passes against a mock the console never calls proves nothing.
//
// The responses are the fixtures, verbatim. A handler that rewrites the answer
// is a second source of truth, and the fixture is what the type describes.

export const handlers = [
  // ---------- the product ----------
  http.get('/v1/principal', () => HttpResponse.json(principal)),
  http.get('/v1/health', () => HttpResponse.json(healthState)),
  http.get('/v1/health/ready', () =>
    HttpResponse.json({
      status: 'ok',
      checks: { database: { ok: true }, aiRuntime: { ok: true, detail: 'ready' } },
    }),
  ),
  http.get('/v1/attention', () =>
    HttpResponse.json({ items: [attentionItem, attentionItemLowerScore] }),
  ),
  http.get('/v1/incidents', () => HttpResponse.json({ items: [incidentSummary] })),
  http.get('/v1/incidents/:id', ({ params }) => {
    if (params.id !== 'inc-1') {
      return HttpResponse.json({ error: 'not found' }, { status: 404 })
    }
    return HttpResponse.json(incidentDetail)
  }),
  http.get('/v1/incidents/:id/explanation', () => HttpResponse.json(incidentExplanation)),
  http.get('/v1/incidents/:id/evidence', () => HttpResponse.json({ items: [evidenceHit] })),
  http.get('/v1/coverage', () => HttpResponse.json(coverage)),
  http.get('/v1/handover', () => HttpResponse.json(handover)),
  // The Chief. The cursor is a query parameter and the handler answers the
  // same body for every cursor, because the console does not decide what the
  // delta is: the product measures it.
  http.get('/v1/chief', () => HttpResponse.json(chief)),
  // The device registry. The console reads the product's own registry; it
  // holds no camera list of its own, because a camera list in the browser is
  // a camera list nobody measured.
  http.get('/v1/devices', () => HttpResponse.json(deviceRegistry)),
  http.post('/v1/devices', () =>
    HttpResponse.json(
      { deviceId: 'cam-3', siteId: 'site-demo', registered: true, probed: false },
      { status: 201 },
    ),
  ),
  http.post('/v1/devices/:id/probe', () => HttpResponse.json({ manifest: {} })),
  // The recording index. The console reads the product's own index and the
  // product's own verdict: a page that decided for itself whether the
  // recording was continuous would be a second opinion about the one fact
  // the page exists to report.
  http.get('/v1/recordings/timeline', () => HttpResponse.json(recordingTimeline)),
  http.get('/v1/recordings/chain', () => HttpResponse.json(recordingChain)),
  http.get('/v1/audit/ledger', () => HttpResponse.json({ entries: [] })),
  // The setup path. The product answers it and the console renders the answer.
  http.get('/v1/setup', () => HttpResponse.json(setup)),
  // The provider chain and the clip for a window.
  http.get('/v1/ai/chain', () => HttpResponse.json(aiChain)),
  http.get('/v1/recordings/clip', () => HttpResponse.json(recordingClip)),
  http.get('/v1/rules/bundles', () => HttpResponse.json({ items: [ruleBundle] })),
  http.get('/v1/ops/dead-letters', () => HttpResponse.json({ items: [] })),
  http.get('/v1/ops/clock-drift', () => HttpResponse.json({ items: [] })),
  http.get('/v1/site', () => HttpResponse.json(siteModel)),
  http.get('/v1/evidence', ({ request }) => {
    const q = new URL(request.url).searchParams.get('q')
    if (!q) {
      return HttpResponse.json({ hits: [evidenceHit], results: [], items: [] })
    }
    return HttpResponse.json({ query: q, hits: [eventHit], nextCursor: '' })
  }),
  // One evidence row's metadata. The console reads the row from the product
  // rather than carrying a second copy of it: a digest rendered by the console
  // is a digest nobody checked.
  http.get('/v1/evidence/:id', () => HttpResponse.json(evidenceMetadata)),
  http.post('/v1/video/upload', () => HttpResponse.json(videoUploadResponse)),
  http.post('/v1/video/youtube', () => HttpResponse.json(videoYouTubeResponse)),
  http.post('/v1/rules/from-text', () => HttpResponse.json({ rule: 'rule-from-text' })),

  // ---------- the console's own server ----------
  http.get('/api/chat', () => HttpResponse.json(chatResponse)),
  http.post('/api/chat', () => HttpResponse.json(chatResponse)),
  http.get('/api/clips', () => HttpResponse.json({ clips: [clip] })),
  http.get('/api/cameras', () => HttpResponse.json({ cameras: [camera] })),

  // ---------- the VMS, through the server's proxy ----------
  http.get('/vms/cameras', () => HttpResponse.json({ array: [vmsCamera] })),
  http.get('/vms/state', () => HttpResponse.json(vmsState)),
  http.post('/vms/cameras/:id/:control', () => HttpResponse.json(vmsCamera)),
]
