import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/server'
import { incidentSummary } from '@/test/fixtures'
import { createHookWrapper } from '@/test/render'
// The application's own client. It lives in `hooks/api.ts` and every hook in
// this file runs under it, because the client carries the retry predicate and
// the query keys these hooks share.
import { queryClient as singleton } from './api'
import {
  useAttention,
  useAuditLedger,
  useClockDrift,
  useCoverage,
  useDeadLetters,
  useEvidenceSearch,
  useHandover,
  useHealth,
  useIncident,
  useIncidentEvidence,
  useIncidentExplanation,
  useIncidents,
  usePrincipal,
  useReadiness,
  useRecentEvents,
  useRuleBundles,
} from './product'

// The product's own answers.
//
// Sixteen hooks, and they share one rule: the console asks the product for
// what the product holds and it never makes any of it up. A panel that cannot
// be filled says so, because a panel with invented numbers in it is worse than
// an empty one -- the numbers would be believed.
//
// The one that carries the most weight is `useAttention`.
//
// The attention queue is the PRODUCT's ranked list. The console does not rank
// it itself: two operators must see the same order for the same site, and a
// console that ranked in the browser would give them two. The score is the
// autonomy layer's answer to "which of these needs a person", and the severity
// is the rule's claim about how bad the incident would be. A queue that reads
// as a severity list throws the answer away and shows the claims, and the
// operator cannot tell the difference between an incident the product was sure
// about and one it was not.
//
// So the test below serves a queue whose PRODUCT order and SEVERITY order
// DISAGREE, and asserts the product's order survives.

const wrapper = createHookWrapper(singleton)

beforeEach(() => {
  singleton.clear()
})

afterEach(() => {
  vi.restoreAllMocks()
})

// A handler that records the URL it was asked for.
function capture(path: string, body: object) {
  const seen: string[] = []
  server.use(
    http.get(path, ({ request }) => {
      seen.push(request.url)
      return HttpResponse.json(body as Record<string, unknown>)
    }),
  )
  return seen
}

describe('useAttention', () => {
  it('keeps the order the product sent', async () => {
    // The fixture's first item has the HIGHER threat score and the LOWER
    // severity word. A hook that re-ranked by severity would put the second
    // one first, and the operator would be shown the claims instead of the
    // product's judgement.
    const { result } = renderHook(() => useAttention(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    const items = result.current.data ?? []
    expect(items).toHaveLength(2)
    expect(items[0]?.incidentId).toBe('inc-1')
    expect(items[0]?.threatScore).toBe(87)
    expect(items[0]?.severity).toBe('high')
    expect(items[1]?.incidentId).toBe('inc-2')
    expect(items[1]?.threatScore).toBe(12)
    expect(items[1]?.severity).toBe('critical')
  })

  it('asks the product for the limit it was given', async () => {
    const seen = capture('/v1/attention', { items: [] })
    const { result } = renderHook(() => useAttention(7), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(seen[0]).toContain('limit=7')
  })

  it('holds the reason chain the product sent', async () => {
    // The chain beside the score is what makes the order CHECKABLE. A queue
    // that carried the score and dropped the chain would be an assertion
    // nobody could check.
    const { result } = renderHook(() => useAttention(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.[0]?.why?.reasonCode).toBe('motion_in_zone')
    expect(result.current.data?.[0]?.why?.replayedFromStoredFacts).toBe(true)
  })

  it('answers an empty queue with an empty queue', async () => {
    server.use(http.get('/v1/attention', () => HttpResponse.json({})))
    const { result } = renderHook(() => useAttention(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toEqual([])
  })
})

describe('useIncidents', () => {
  it('asks the product for the incidents and holds what it sent', async () => {
    const seen = capture('/v1/incidents', { items: [incidentSummary] })
    const { result } = renderHook(() => useIncidents(5), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(seen[0]).toContain('limit=5')
    expect(result.current.data).toHaveLength(1)
    expect(result.current.data?.[0]?.incidentId).toBe('inc-1')
  })

  it('answers an absent list with an empty list', async () => {
    server.use(http.get('/v1/incidents', () => HttpResponse.json({})))
    const { result } = renderHook(() => useIncidents(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toEqual([])
  })
})

describe('useIncidentExplanation', () => {
  it('asks nothing without an incident', async () => {
    const seen = capture('/v1/incidents/:id/explanation', {})
    const { result } = renderHook(() => useIncidentExplanation(null), { wrapper })
    await waitFor(() => expect(result.current.fetchStatus).toBe('idle'))
    expect(seen).toHaveLength(0)
  })

  it('asks for the incident and holds the reason', async () => {
    const { result } = renderHook(() => useIncidentExplanation('inc-1'), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.reasons).toEqual(['motion in the loading bay zone'])
  })
})

describe('useHealth', () => {
  it('holds the state the product reported', async () => {
    const { result } = renderHook(() => useHealth(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.status).toBe('ok')
  })

  it('distinguishes a measured state from an absent one', async () => {
    // The five states are states. A health answer that does not carry a
    // status is an ABSENCE, and the console must not turn it into "ok".
    server.use(http.get('/v1/health', () => HttpResponse.json({})))
    const { result } = renderHook(() => useHealth(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.status).toBeUndefined()
  })
})

describe('useReadiness', () => {
  it('holds the checks the probe reported', async () => {
    const { result } = renderHook(() => useReadiness(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.status).toBe('ok')
    expect(result.current.data?.checks?.aiRuntime?.ok).toBe(true)
  })

  it('distinguishes a degraded probe from a healthy one', async () => {
    server.use(
      http.get('/v1/health/ready', () =>
        HttpResponse.json({
          status: 'degraded',
          checks: { aiRuntime: { ok: false, detail: 'not reachable' } },
        }),
      ),
    )
    const { result } = renderHook(() => useReadiness(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.status).toBe('degraded')
    expect(result.current.data?.checks?.aiRuntime?.ok).toBe(false)
  })
})

describe('useCoverage', () => {
  it('holds the rows and the scope', async () => {
    // A count with no scope is a number nobody can check, so the scope comes
    // in the answer and the hook must not drop it.
    const { result } = renderHook(() => useCoverage(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.sites).toEqual(['site-demo'])
    expect(result.current.data?.rows).toHaveLength(1)
    expect(result.current.data?.rows[0]?.count).toBe(2)
  })
})

describe('useHandover', () => {
  it('holds the brief and says it is available', async () => {
    const { result } = renderHook(() => useHandover(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.available).toBe(true)
    expect(result.current.data?.brief?.headline).toBe('Two incidents need a person.')
  })

  it('says a missing hand-over is missing, and why', async () => {
    // An empty hand-over and a missing one are different answers, and only
    // one of them is true.
    server.use(
      http.get('/v1/handover', () =>
        HttpResponse.json({ siteId: 'site-demo', available: false, unavailable: 'no artifact' }),
      ),
    )
    const { result } = renderHook(() => useHandover(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.available).toBe(false)
    expect(result.current.data?.unavailable).toBe('no artifact')
    expect(result.current.data?.brief).toBeUndefined()
  })
})

describe('useAuditLedger', () => {
  it('holds the entries', async () => {
    server.use(
      http.get('/v1/audit/ledger', () => HttpResponse.json({ entries: [{ id: 'a-1' }] })),
    )
    const { result } = renderHook(() => useAuditLedger(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toHaveLength(1)
  })

  it('answers an absent list with an empty list', async () => {
    const { result } = renderHook(() => useAuditLedger(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toEqual([])
  })
})

describe('usePrincipal', () => {
  it('holds the configured principal, including the site\'s timezone', async () => {
    const { result } = renderHook(() => usePrincipal(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.tenantId).toBe('tenant-demo')
    expect(result.current.data?.timezone).toBe('Europe/Berlin')
  })
})

describe('useRecentEvents', () => {
  it('holds the events the product sent', async () => {
    const { result } = renderHook(() => useRecentEvents(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toHaveLength(1)
    expect(result.current.data?.[0]?.eventId).toBe('evt-1')
  })
})

describe('useEvidenceSearch', () => {
  it('asks nothing for an empty question', async () => {
    const seen = capture('/v1/evidence', { hits: [] })
    const { result } = renderHook(() => useEvidenceSearch('   '), { wrapper })
    await waitFor(() => expect(result.current.fetchStatus).toBe('idle'))
    expect(seen).toHaveLength(0)
  })

  it('encodes the question', async () => {
    const seen = capture('/v1/evidence', { hits: [] })
    const { result } = renderHook(() => useEvidenceSearch('a b'), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(seen[0]).toContain('q=a+b')
  })
})

describe('useIncident', () => {
  it('asks nothing without an incident', async () => {
    const { result } = renderHook(() => useIncident(null), { wrapper })
    await waitFor(() => expect(result.current.fetchStatus).toBe('idle'))
    expect(result.current.fetchStatus).toBe('idle')
  })

  it('holds the record for the incident', async () => {
    const { result } = renderHook(() => useIncident('inc-1'), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.incidentId).toBe('inc-1')
    expect(result.current.data?.verification?.state).toBe('confirmed')
  })

  it('holds a refusal rather than an empty record', async () => {
    server.use(
      http.get('/v1/incidents/:id', () => HttpResponse.json({ error: 'not found' }, { status: 404 })),
    )
    const { result } = renderHook(() => useIncident('inc-nope'), { wrapper })
    // The timeout is generous because these hooks carry `retry: 1`, which
    // overrides the client's "a 4xx is not asked again" predicate and so the
    // refusal surfaces after a second attempt. That inconsistency between the
    // hook and the client is a FINDING, recorded in docs/status.md; the test
    // pins the behaviour that exists rather than the behaviour that was
    // meant.
    await waitFor(() => expect(result.current.isError).toBe(true), { timeout: 5000 })
    expect(result.current.data).toBeUndefined()
  })
})

describe('useIncidentEvidence', () => {
  it('holds the evidence the incident cites', async () => {
    const { result } = renderHook(() => useIncidentEvidence('inc-1'), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toHaveLength(1)
  })
})

describe('useRuleBundles', () => {
  it('holds the bundles the product stored', async () => {
    const { result } = renderHook(() => useRuleBundles(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toHaveLength(1)
    expect(result.current.data?.[0]?.version).toBe('bundle-3')
  })
})

describe('useDeadLetters', () => {
  it('holds the dead letters', async () => {
    server.use(
      http.get('/v1/ops/dead-letters', () => HttpResponse.json({ items: [{ id: 'dl-1' }] })),
    )
    const { result } = renderHook(() => useDeadLetters(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toHaveLength(1)
  })
})

describe('useClockDrift', () => {
  it('holds the drift the product measured', async () => {
    server.use(
      http.get('/v1/ops/clock-drift', () =>
        HttpResponse.json({ items: [{ source: 'cam-1', skewMs: 1200, corrected: true }] }),
      ),
    )
    const { result } = renderHook(() => useClockDrift(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.[0]?.skewMs).toBe(1200)
  })
})
