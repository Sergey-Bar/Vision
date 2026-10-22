import { afterEach, describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { renderWithProviders } from '@/test/render'
import { server } from '@/test/server'
import { chief, chiefDegraded } from '@/test/fixtures'
import { ChiefPanel } from './Chief'

// The Chief.
//
// The Chief is the product's answer to "what changed while I was away", and
// it is three lines: the delta, the headline, and what the system does not
// know.
//
// The third line is what these tests are mostly about. A Chief that is
// quieter than usual and does not say why is the failure this product exists
// to prevent, and a console that swallows a named degradation would turn the
// product's honest absence into the console's silence.

afterEach(() => {
  window.history.replaceState(null, '', '/')
})

describe('the Chief', () => {
  it('says what changed since the cursor', async () => {
    renderWithProviders(<ChiefPanel />)

    expect(await screen.findByText(/1 new incident since/)).toBeInTheDocument()
    expect(screen.getByText(/scored 90/)).toBeInTheDocument()
  })

  it('says how many are new, not just that some are', async () => {
    renderWithProviders(<ChiefPanel />)

    expect(await screen.findByText(/1 new since/)).toBeInTheDocument()
  })

  it('carries the cursor in the URL and nowhere else', async () => {
    // The cursor is durable because it is in the URL: a bookmark survives it
    // and a reload survives it.
    //
    // The browser-side storage ban is NOT asserted here. It is asserted by
    // `web/console_honesty_test.go`, which reads the console's own source and
    // fails on `localStorage`, `sessionStorage` and `indexedDB` -- and a test
    // that read the source would be a second implementation of the same rule
    // in a different language.
    renderWithProviders(<ChiefPanel />)
    await screen.findByText(/1 new incident since/)

    expect(window.location.search).toBe('')
  })

  it('marks as seen by putting the cursor in the URL', async () => {
    const user = userEvent.setup({ delay: null })
    renderWithProviders(<ChiefPanel />)
    await screen.findByText(/1 new incident since/)

    await user.click(screen.getByRole('button', { name: /Mark as seen/ }))

    expect(window.location.search).toMatch(/^\?since=/)
  })

  it('names a degradation rather than going quiet', async () => {
    server.use(http.get('/v1/chief', () => HttpResponse.json(chiefDegraded)))
    renderWithProviders(<ChiefPanel />)

    // The reason is named, and the headline says the Chief is degraded. Both
    // matter: the reason is for a reader who wants to know which absence, and
    // the headline is for a reader who is looking at a quieter screen than
    // usual and needs to know why.
    expect(await screen.findByText(/narration_unavailable/)).toBeInTheDocument()
    expect(screen.getAllByText(/degraded/).length).toBeGreaterThan(0)
  })

  it('shows the narration with its attribution', async () => {
    server.use(
      http.get('/v1/chief', () =>
        HttpResponse.json({
          ...chief,
          narration: {
            text: 'One incident needs you: motion in the loading bay.',
            provider: 'anthropic',
            model: 'claude-opus-4',
            contractVersion: 'investigator/v1',
            temperature: 0,
            inputDigest: 'sha256:abc',
            citations: ['inc-1'],
          },
        }),
      ),
    )
    renderWithProviders(<ChiefPanel />)

    expect(
      await screen.findByText('One incident needs you: motion in the loading bay.'),
    ).toBeInTheDocument()
    // A sentence nobody can replay may not be shown to an operator, so the
    // attribution is on the screen and not in a tooltip.
    expect(screen.getByText(/anthropic/)).toBeInTheDocument()
    expect(screen.getByText(/investigator\/v1/)).toBeInTheDocument()
  })

  it('shows the autonomy level it is permitted to act at', async () => {
    renderWithProviders(<ChiefPanel />)

    await screen.findByText(/1 new incident since/)
    expect(screen.getByText('A0')).toBeInTheDocument()
  })

  it('says when the product did not answer', async () => {
    server.use(http.get('/v1/chief', () => new HttpResponse(null, { status: 503 })))
    renderWithProviders(<ChiefPanel />)

    // The hook retries once, so the refusal takes a moment to settle. The
    // wait is generous on purpose: a test that raced the retry would pass
    // against a console that never shows the refusal.
    expect(
      await screen.findByText(
        /asked the product for the Chief and could not read the answer/,
        {},
        { timeout: 5000 },
      ),
    ).toBeInTheDocument()
  })
})
