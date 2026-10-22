import { describe, expect, it } from 'vitest'
import { http, HttpResponse, delay } from 'msw'
import { Route, Routes } from 'react-router-dom'
import { screen, within } from '@testing-library/react'
import { server } from '@/test/server'
import { renderWithProviders } from '@/test/render'
import { incidentDetail } from '@/test/fixtures'
import { axe } from '@/test/axe'
import type { IncidentDetail } from '@/hooks/product'
import { IncidentDetailPage } from './IncidentDetail'

// The incident.
//
// One record, and the console's whole job on this screen is to DRAW it and to
// say which parts of it are missing. The page is reached with an id, and the
// product is the only thing that knows what that id holds.
//
// So the tests are about three things:
//
//   the FACTS    the title, the state, the severity, the rule -- the product's
//                own values, drawn as the product wrote them
//   the RECORD   the events and the evidence the incident is made of, which
//                come from the incident the product returned
//   the GAPS     an empty list, a missing decision, a chain that did not
//                arrive -- each of them a sentence, never a zero and never a
//                dash where a sentence belongs
//
// A refusal is the fourth. The product answers an incident that is not yours
// with the same answer as an incident that does not exist, so the screen says
// that it could not read the incident and it draws NONE of the incident's
// fields: a page that showed the title it was not given is a page that
// invented a row.

// The product's readers retry once before they give up, and the retry waits a
// second. The error state therefore arrives later than the default wait for
// an element, so the wait is longer here.
const ERROR_WAIT = { timeout: 5000 }

// The wait for a reader that has GIVEN UP.
//
// The retry waits a second, so a reader that has given up is a second and a
// bit away. The wait is long enough to outlast the retry and short enough
// that the test finishes inside the suite's own five seconds.
const GAVE_UP_WAIT = { timeout: 2500 }

function renderIncident(id = 'inc-1') {
  return renderWithProviders(
    <Routes>
      <Route path="/incidents/:id" element={<IncidentDetailPage />} />
    </Routes>,
    { route: `/incidents/${id}` },
  )
}

// A region of the page, named by its heading.
//
// The page is a sequence of sections and each one says what it holds, so the
// assertions are scoped to the region they are about. A word that appears in
// two regions is two facts, and a test that cannot say which one it means is a
// test that will one day pass for the wrong reason.
function region(name: string): HTMLElement {
  const heading = screen.getByRole('heading', { level: 2, name })
  const section = heading.closest('section')
  if (!section) throw new Error(`the heading ${name} is not in a section`)
  return section
}

// The product's own word for a corroborated decision.
//
// The console knows one word -- 'verified' -- and the product has never said
// it: `internal/api` answers CORROBORATED, UNVERIFIED, CONTRADICTED,
// DUPLICATE or SUPPRESSED. A console that recognises a word the product does
// not use marks every real decision with the wrong glyph.
const CORROBORATED: IncidentDetail = {
  ...incidentDetail,
  verification: {
    verificationId: 'ver-1',
    state: 'CORROBORATED',
    reasonCode: 'replay_confirmed',
    explanation: 'The stored facts reproduce the incident.',
    supportingEventIds: ['evt-1'],
  },
}

describe('the incident page', () => {
  it('draws the incident the product holds', async () => {
    renderIncident()

    expect(
      await screen.findByRole('heading', { level: 1, name: 'Motion in the loading bay' }),
    ).toBeInTheDocument()
    const facts = within(region('The facts'))
    // The state and the severity are WORDS. A colour is never the only
    // channel, and a word the product chose is not a word the console
    // translated.
    expect(facts.getByText('open')).toBeInTheDocument()
    expect(facts.getByText('high')).toBeInTheDocument()
    // And the rest of the record's identity, the product's own.
    expect(facts.getByText('site-demo')).toBeInTheDocument()
    expect(facts.getByText('rule-motion')).toBeInTheDocument()
    expect(facts.getByText('bundle-3')).toBeInTheDocument()
    expect(facts.getByText('policy-2')).toBeInTheDocument()
  })

  it('shows the verification state as a word, and the reason it reached it', async () => {
    renderIncident()

    // The state is a word and not a mark. The word is the product's.
    expect(await screen.findByText('confirmed')).toBeInTheDocument()
    // And with it the reason the decision reached that state, which is the
    // part an operator acts on.
    expect(screen.getByText('replay_confirmed')).toBeInTheDocument()
    expect(screen.getByText('The stored facts reproduce the incident.')).toBeInTheDocument()
  })

  it('shows the product\'s own word for the verification state, untranslated', async () => {
    server.use(http.get('/v1/incidents/inc-1', () => HttpResponse.json(CORROBORATED)))

    renderIncident()

    expect(await screen.findByText('CORROBORATED')).toBeInTheDocument()
  })

  it('shows the chain the decision was made from', async () => {
    renderIncident()

    // The chain is what makes the decision checkable, and it is the
    // product's, step by step.
    expect(await screen.findByText('motion_in_zone')).toBeInTheDocument()
    expect(
      screen.getByText('A motion rule matched an event in the loading bay.'),
    ).toBeInTheDocument()
  })

  it('lists the events the incident is made of', async () => {
    renderIncident()
    await screen.findByRole('heading', { level: 1, name: 'Motion in the loading bay' })

    const events = within(region('Events'))
    expect(events.getByText('motion')).toBeInTheDocument()
    expect(events.getByText(/cam-1/)).toBeInTheDocument()
    // The moment is the product's, and it carries an offset: a timestamp with
    // nothing attached to it is a moment two readers read differently.
    expect(events.getByText(/21 Oct 2026/)).toBeInTheDocument()
  })

  it('lists the evidence the incident cites, and links to the product\'s own record of it', async () => {
    renderIncident()
    await screen.findByRole('heading', { level: 1, name: 'Motion in the loading bay' })

    const evidence = within(region('Evidence'))
    // The row is the product's evidence: its id, its kind and its digest.
    const link = await evidence.findByRole('link', { name: 'ev-1' })
    expect(link).toHaveAttribute('href', '/console/evidence/ev-1')
    expect(evidence.getByText(/video/)).toBeInTheDocument()
    expect(evidence.getByText(/^[a-f0-9]{64}$/)).toBeInTheDocument()
  })

  it('says the product has not recorded a decision yet', async () => {
    server.use(
      http.get('/v1/incidents/inc-1', () =>
        HttpResponse.json({ ...incidentDetail, verification: undefined }),
      ),
    )

    renderIncident()

    // No decision is not a dash and not a zero. It is a sentence that says
    // the decision is not there, and it says that it is a DIFFERENT answer
    // from a decision that says no.
    expect(await screen.findByText('No stored decision yet')).toBeInTheDocument()
    expect(
      screen.getByText(/has not recorded a verification for it/),
    ).toBeInTheDocument()
  })

  it('says each empty part of the record in words', async () => {
    server.use(
      http.get('/v1/incidents/inc-1', () =>
        HttpResponse.json({ ...incidentDetail, events: [], evidence: [], dispositions: [] }),
      ),
    )

    renderIncident()

    // Three empty lists, three sentences. A dash where a sentence belongs is
    // a number that means "I have not asked yet".
    expect(await screen.findByText('No events')).toBeInTheDocument()
    expect(screen.getByText('No evidence')).toBeInTheDocument()
    expect(screen.getByText('Nothing has been decided')).toBeInTheDocument()
    // And nothing is cited that the product did not cite.
    expect(screen.queryByRole('link', { name: 'ev-1' })).not.toBeInTheDocument()
  })

  it('says it is reading the incident, and draws nothing until the product has answered', async () => {
    server.use(
      http.get('/v1/incidents/inc-1', async () => {
        await delay(200)
        return HttpResponse.json(incidentDetail)
      }),
    )

    renderIncident()

    // The region says what it is doing...
    expect(await screen.findByRole('status')).toHaveTextContent('Reading the incident')
    // ...and the incident is not drawn from an answer that has not arrived.
    expect(screen.queryByText('Motion in the loading bay')).not.toBeInTheDocument()
    expect(screen.queryByText('rule-motion')).not.toBeInTheDocument()
  })

  it('says the product did not answer, and draws no part of the incident', async () => {
    server.use(
      http.get('/v1/incidents/inc-1', () =>
        HttpResponse.json({ error: 'site_not_assigned' }, { status: 403 }),
      ),
    )

    renderIncident()

    const alert = await screen.findByRole('alert', undefined, ERROR_WAIT)
    expect(alert).toHaveTextContent('The product did not answer with this incident')
    // The incident's own fields are NOT on the screen. A page that showed the
    // title it was not given is a page that invented a row.
    expect(screen.queryByText('Motion in the loading bay')).not.toBeInTheDocument()
    expect(screen.queryByText('open')).not.toBeInTheDocument()
    expect(screen.queryByText('high')).not.toBeInTheDocument()
    expect(screen.queryByText('rule-motion')).not.toBeInTheDocument()
    // And the answer is a sentence, not a body and not a stack.
    expect(alert.textContent).not.toContain('{"error"')
    expect(document.body.textContent).not.toMatch(/\bat \w+ \(/)
  })

  it('offers the way back to the incident list', async () => {
    server.use(
      http.get('/v1/incidents/inc-1', () =>
        HttpResponse.json({ error: 'site_not_assigned' }, { status: 403 }),
      ),
    )

    renderIncident()

    await screen.findByRole('alert', undefined, ERROR_WAIT)
    expect(screen.getByRole('link', { name: 'Back to incidents' })).toHaveAttribute(
      'href',
      '/console/incidents',
    )
  })

  it('says the decision is degraded, and why', async () => {
    server.use(
      http.get('/v1/incidents/inc-1', () =>
        HttpResponse.json({
          ...incidentDetail,
          degraded: true,
          degradedReasons: ['the AI runtime was not reachable'],
        }),
      ),
    )

    renderIncident()

    // A degraded decision is still a decision, and the screen says that it is
    // degraded and why rather than drawing it as an ordinary one.
    expect(await screen.findByText(/This decision is degraded/)).toBeInTheDocument()
    expect(screen.getByText(/the AI runtime was not reachable/)).toBeInTheDocument()
  })

  it('says why the reason chain is not there, rather than leaving it blank', async () => {
    server.use(
      http.get('/v1/incidents/inc-1/explanation', () =>
        HttpResponse.json({
          incidentId: 'inc-1',
          whyUnavailable: 'the rule bundle was not available when the decision was made',
        }),
      ),
    )

    renderIncident()

    expect(
      await screen.findByText(/the rule bundle was not available when the decision was made/),
    ).toBeInTheDocument()
    // And no chain that the product did not give.
    expect(screen.queryByText('motion_in_zone')).not.toBeInTheDocument()
  })

  it('has no accessibility violations', async () => {
    const { container } = renderWithProviders(
      <Routes>
        <Route path="/incidents/:id" element={<IncidentDetailPage />} />
      </Routes>,
      { route: '/incidents/inc-1' },
    )
    await screen.findByRole('heading', { level: 1, name: 'Motion in the loading bay' })

    const results = await axe(container)
    expect(results).toHaveNoViolations()
  })

  it.fails('marks a corroborated decision as corroborated, and not as a question', async () => {
    // The console knows one word -- 'verified' -- and the product has never
    // said it, so every real decision is marked with the glyph that means
    // "I do not know". The word beside it is right, and the mark is the
    // channel an operator reads first.
    server.use(http.get('/v1/incidents/inc-1', () => HttpResponse.json(CORROBORATED)))

    renderIncident()

    const badge = await screen.findByText('CORROBORATED')
    expect(badge.textContent).toContain('✓')
  })

  it.fails('says the reason chain did not arrive, rather than drawing a decision without one', async () => {
    // The chain is what makes the decision checkable, and it arrives on its
    // own request. When that request fails the page draws the decision with
    // no chain and no reason, so the screen cannot be told apart from one
    // where the chain was never needed. The five states are states.
    server.use(
      http.get('/v1/incidents/inc-1/explanation', () =>
        HttpResponse.json({ error: 'not_found' }, { status: 404 }),
      ),
    )

    renderIncident()

    expect(await screen.findByRole('alert', undefined, GAVE_UP_WAIT)).toBeInTheDocument()
    expect(screen.queryByText('motion_in_zone')).not.toBeInTheDocument()
  })
})
