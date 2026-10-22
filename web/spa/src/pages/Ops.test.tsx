import { describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import { server } from '@/test/server'
import { renderWithProviders } from '@/test/render'
import { OpsPage } from './Ops'

// The operations page.
//
// The things that are not working: what is stuck, and how far the clocks have
// drifted. The dead letters are first because a dead letter is the visible
// form of the repository's first invariant -- nothing that reaches the inbox
// is lost -- and the page's own sentence says why: an empty list and a broken
// reader look the same to an operator, and only one of them is true.

// The product's readers retry once before they give up, and the retry waits a
// second. The error state therefore arrives later than the default wait for
// an element, so the wait is longer here.
const ERROR_WAIT = { timeout: 5000 }

const DEAD_LETTER = {
  id: 'dl-1',
  kind: 'video.upload',
  siteId: 'site-demo',
  reason: 'the decoder refused the stream',
  receivedAt: '2026-10-21T08:00:00Z',
  attempts: 3,
}

const DRIFT = {
  source: 'milestone-main',
  observedAt: '2026-10-21T08:00:00Z',
  skewMs: 1200,
  corrected: true,
}

describe('the operations page', () => {
  it('shows both regions, whatever they say', async () => {
    // Two readers, two questions. A page that drew only one of them would
    // leave the operator guessing whether the other had anything to say.
    renderWithProviders(<OpsPage />)

    expect(await screen.findByText('Dead letters')).toBeInTheDocument()
    expect(screen.getByText('Clock drift')).toBeInTheDocument()
    expect(screen.getByText('Readiness')).toBeInTheDocument()
  })

  it('says that nothing is stuck when there are no dead letters', async () => {
    renderWithProviders(<OpsPage />)

    expect(await screen.findByText('Nothing is stuck')).toBeInTheDocument()
    expect(
      screen.getByText(/Every event that reached the inbox has reached a terminal state/),
    ).toBeInTheDocument()
  })

  it('draws the dead letter the product holds', async () => {
    server.use(
      http.get('/v1/ops/dead-letters', () => HttpResponse.json({ items: [DEAD_LETTER] })),
    )

    renderWithProviders(<OpsPage />)

    expect(await screen.findByText('video.upload')).toBeInTheDocument()
    expect(screen.getByText('the decoder refused the stream')).toBeInTheDocument()
    expect(screen.getByText('3')).toBeInTheDocument()
    expect(screen.queryByText('Nothing is stuck')).not.toBeInTheDocument()
  })

  it('draws the clock drift the product holds', async () => {
    server.use(http.get('/v1/ops/clock-drift', () => HttpResponse.json({ items: [DRIFT] })))

    renderWithProviders(<OpsPage />)

    expect(await screen.findByText('milestone-main')).toBeInTheDocument()
    expect(screen.getByText('1200 ms')).toBeInTheDocument()
    expect(screen.getByText(/corrected/)).toBeInTheDocument()
  })

  it('says the reader broke rather than that nothing is stuck', async () => {
    // The whole point of the region: an empty list and a broken reader look
    // the same, so a failed reader must not draw the empty answer.
    server.use(
      http.get('/v1/ops/dead-letters', () =>
        HttpResponse.json({ error: 'not_configured' }, { status: 503 }),
      ),
    )

    renderWithProviders(<OpsPage />)

    expect(
      await screen.findByText('The dead-letter reader did not answer', undefined, ERROR_WAIT),
    ).toBeInTheDocument()
    expect(screen.queryByText('Nothing is stuck')).not.toBeInTheDocument()
    expect(screen.queryByText(/read_failed|not_configured/)).not.toBeInTheDocument()
  })
})
