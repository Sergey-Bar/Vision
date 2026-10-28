import { describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { screen, fireEvent } from '@testing-library/react'
import { server } from '@/test/server'
import { renderWithProviders } from '@/test/render'
import type { DemoHafniaResponse } from '@/api'
import { DemoHafniaPage } from './DemoHafnia'

// The Hafnia demo page.
//
// It renders the SIMULATED-prefixed four-step bridge result against a local
// Hafnia test server, and never presents synthetic data as detection.

const structured: DemoHafniaResponse = {
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
  bookmark: { placed: true, ref: 'bmk-demo-00000000', refusal: null },
  simulated: true,
}

const degraded: DemoHafniaResponse = {
  ...structured,
  hafnia: {
    ...structured.hafnia,
    degraded: true,
    reason: 'answer_malformed',
    detail: 'reason=answer_malformed the provider\'s answer is not the shape it claims to be',
    structured: null,
  },
}

describe('the Hafnia demo page', () => {
  it('shows the SIMULATED prefix and the four not-yet-run steps', () => {
    renderWithProviders(<DemoHafniaPage />)

    expect(screen.getByText('SIMULATED')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Hafnia demo' })).toBeInTheDocument()
    // One action only: the header button.
    expect(screen.getAllByRole('button', { name: /run the bridge/i })).toHaveLength(1)
    expect(screen.getByText('The bridge has not run yet')).toBeInTheDocument()
  })

  it('runs the bridge and renders the four-step structured result', async () => {
    server.use(
      http.post('/api/demo-hafnia', () => HttpResponse.json(structured)),
    )

    renderWithProviders(<DemoHafniaPage />)

    fireEvent.click(screen.getAllByRole('button', { name: /run the bridge/i })[0])

    // Loading first, never data: while pending the steps still say not yet run
    // and no event fact has appeared.
    expect(screen.queryByText('evt-motion-001')).not.toBeInTheDocument()

    // Then the steps resolve: event ok, clip ok, hafnia answer, bookmark placed.
    expect(await screen.findByText('answer')).toBeInTheDocument()
    expect(await screen.findByText('bookmark placed')).toBeInTheDocument()

    // The event panel carries the simulated event facts.
    expect(await screen.findByText('evt-motion-001')).toBeInTheDocument()
    expect(await screen.findByText('motion')).toBeInTheDocument()

    // The structured Hafnia answer renders, not a fabricated negative.
    expect(await screen.findByText('a person crossed the restricted zone')).toBeInTheDocument()

    // The SIMULATED mark stays on the whole page.
    expect(screen.getByText('SIMULATED')).toBeInTheDocument()
  })

  it('renders a degraded Hafnia answer with reason= and no structured output', async () => {
    server.use(
      http.post('/api/demo-hafnia', () => HttpResponse.json(degraded)),
    )

    renderWithProviders(<DemoHafniaPage />)

    fireEvent.click(screen.getAllByRole('button', { name: /run the bridge/i })[0])

    // Hafnia step is degraded and the named reason shows.
    expect(await screen.findByText('reason=answer_malformed')).toBeInTheDocument()

    // No structured output is fabricated.
    expect(screen.queryByText('a person crossed the restricted zone')).not.toBeInTheDocument()

    // SIMULATED stays on the page.
    expect(screen.getByText('SIMULATED')).toBeInTheDocument()
  })
})
