import { describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import { server } from '@/test/server'
import { renderWithProviders } from '@/test/render'
import { RulesPage } from './Rules'

// The rule bundles.
//
// STORE is not ACTIVATE. A bundle on disk is a proposal; a bundle that is
// running is a decision, and an operator who cannot tell the two apart will
// believe that a rule that was written is a rule that is running.
//
// So the page has two blocks and never one table, and the tests here are
// about which of the two a version appears in.

// The product's readers retry once before they give up, and the retry waits a
// second. The error state therefore arrives later than the default wait for
// an element, so the wait is longer here.
const ERROR_WAIT = { timeout: 5000 }

const STORED = {
  version: 'bundle-4',
  activatedAt: '2026-10-20T08:00:00Z',
  ruleCount: 3,
  digest: 'e'.repeat(64),
  active: false,
}

const ACTIVE = {
  version: 'bundle-3',
  activatedAt: '2026-10-20T08:00:00Z',
  ruleCount: 12,
  digest: 'b'.repeat(64),
  active: true,
}

describe('the rules page', () => {
  it('names the bundle that is running', async () => {
    server.use(http.get('/v1/rules/bundles', () => HttpResponse.json({ items: [ACTIVE] })))

    renderWithProviders(<RulesPage />)

    // The version is in both blocks: it is the bundle that is running AND it
    // is a bundle on disk. Both are true, and the page says both.
    expect((await screen.findAllByText('bundle-3')).length).toBe(2)
    expect(screen.getByText('12 rules')).toBeInTheDocument()
    // The word is in both blocks too, for the same reason.
    expect(screen.getAllByText('running').length).toBeGreaterThan(0)
  })

  it('does not present a stored bundle as the active one', async () => {
    // The defect this file exists for: a bundle on disk that is not running
    // must not appear in the block that says what is running.
    server.use(http.get('/v1/rules/bundles', () => HttpResponse.json({ items: [STORED] })))

    renderWithProviders(<RulesPage />)

    expect(await screen.findByText('No bundle is active')).toBeInTheDocument()
    expect(
      screen.getByText(/The product has bundles on disk and none of them is running/),
    ).toBeInTheDocument()
    // The stored bundle is in the stored table and nowhere else: one copy, in
    // the block that says "stored", and the word "running" is absent.
    expect(screen.getAllByText('bundle-4')).toHaveLength(1)
    expect(screen.getByText('stored')).toBeInTheDocument()
    expect(screen.queryByText('running')).not.toBeInTheDocument()
  })

  it('says no bundles are stored when there are none', async () => {
    server.use(http.get('/v1/rules/bundles', () => HttpResponse.json({ items: [] })))

    renderWithProviders(<RulesPage />)

    expect(await screen.findByText('No bundles are stored')).toBeInTheDocument()
    expect(screen.getByText('No bundle is active')).toBeInTheDocument()
    // Writing a bundle is a human's job, and the console does neither.
    const write = screen.getByRole('button', { name: 'Write a bundle' })
    expect(write).toBeDisabled()
  })

  it('says the product did not answer with the rule bundles', async () => {
    server.use(
      http.get('/v1/rules/bundles', () =>
        HttpResponse.json({ error: 'no_active_bundle' }, { status: 409 }),
      ),
    )

    renderWithProviders(<RulesPage />)

    const alert = await screen.findByRole('alert', undefined, ERROR_WAIT)
    expect(alert).toHaveTextContent(/did not answer with the rule bundles/)
    // The console keeps no copy of the rules, so without the product's answer
    // there is nothing to draw -- and nothing is drawn.
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })
})
