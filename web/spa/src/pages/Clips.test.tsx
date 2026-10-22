import { afterEach, describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '@/test/server'
import { renderWithProviders } from '@/test/render'
import { ClipsPage } from './Clips'

// The clips.
//
// The clips are the platform's own stored evidence: the bytes the vendor's
// adapter fetched and the product kept. This file protects the clip the
// console did not fetch -- a clip with a real-looking time on it is the kind
// of thing a reader takes for a recording -- and the link, which must point
// at the product's own content route rather than at a copy the console made.

const CLIP = {
  evidenceId: 'ev-9',
  siteId: 'site-demo',
  kind: 'video',
  observedAt: '2026-10-21T08:00:00Z',
  uri: '/v1/evidence/ev-9',
  mediaType: 'video/mp4',
  sizeBytes: 2 * 1024 * 1024,
  sha256: 'd'.repeat(64),
}

// jsdom has no media stack, so a video element cannot play itself. The stub
// is here so that the console's own action can be asserted without the
// browser's decoder deciding the outcome.
type Play = () => Promise<void>
const mediaProto = HTMLMediaElement.prototype as unknown as { play?: Play }
const originalPlay = mediaProto.play
mediaProto.play = function playStub() {
  return Promise.resolve()
}

afterEach(() => {
  mediaProto.play = originalPlay
})

describe('the clips page', () => {
  it('draws the clip the platform stored', async () => {
    server.use(http.get('/api/clips', () => HttpResponse.json({ clips: [CLIP] })))

    renderWithProviders(<ClipsPage />)

    expect(await screen.findByText('ev-9')).toBeInTheDocument()
    expect(screen.getByText('site-demo')).toBeInTheDocument()
    expect(screen.getByText('video/mp4')).toBeInTheDocument()
    // The size is the stored size, not a zero and not a guess.
    expect(screen.getByText('2.00 MB')).toBeInTheDocument()
  })

  it('links to the product\'s own content route', async () => {
    // A second reader of the evidence bytes is a second opinion about an
    // integrity check, so the console links to the product's route and does
    // not carry a copy.
    server.use(http.get('/api/clips', () => HttpResponse.json({ clips: [CLIP] })))

    renderWithProviders(<ClipsPage />)

    const link = await screen.findByRole('link', { name: 'Download clip' })
    expect(link).toHaveAttribute('href', '/v1/evidence/ev-9/content')
  })

  it('plays the clip from the product\'s own content route', async () => {
    // The button does what the page says: the clip plays, and the bytes it
    // plays are the product's.
    const user = userEvent.setup({ delay: null })
    server.use(http.get('/api/clips', () => HttpResponse.json({ clips: [CLIP] })))

    renderWithProviders(<ClipsPage />)

    await user.click(await screen.findByRole('button', { name: 'Play' }))

    expect(document.querySelector('video')).toHaveAttribute('src', '/v1/evidence/ev-9/content')
  })

  it('says nothing has been fetched yet when there are no clips', async () => {
    server.use(http.get('/api/clips', () => HttpResponse.json({ clips: [] })))

    renderWithProviders(<ClipsPage />)

    expect(await screen.findByText('No clips stored')).toBeInTheDocument()
    expect(
      screen.getByText(/The platform stores a clip when the vendor gives it one/),
    ).toBeInTheDocument()
    // And no clip that the platform does not hold.
    expect(screen.queryByText('ev-9')).not.toBeInTheDocument()
  })

  it('says the clips did not load, and shows the refusal the product sent', async () => {
    server.use(
      http.get('/api/clips', () => HttpResponse.json({ error: 'no_active_bundle' }, { status: 409 })),
    )

    renderWithProviders(<ClipsPage />)

    expect(await screen.findByText(/Unable to load clips/)).toBeInTheDocument()
    // The refusal is the product's vocabulary and the operator has to be able
    // to read it and act on it.
    expect(screen.getByText(/no_active_bundle/)).toBeInTheDocument()
    // No stack frame: this is a screen an operator reads.
    expect(document.body.textContent).not.toMatch(/\bat \w+ \(/)
  })

  it.fails('does not print the failure body as raw JSON', async () => {
    // This is the only page in the console that prints the failure's body
    // verbatim instead of running it through `describeError`, so the operator
    // reads the JSON punctuation along with the refusal. The rule every other
    // page follows is the expectation below; it fails against the page as it
    // is written, and it is here so the fix cannot land unnoticed.
    server.use(
      http.get('/api/clips', () => HttpResponse.json({ error: 'no_active_bundle' }, { status: 409 })),
    )

    renderWithProviders(<ClipsPage />)

    await screen.findByText(/Unable to load clips/)
    expect(document.body.textContent).not.toContain('{"error"')
  })
})
