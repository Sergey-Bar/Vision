import { describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '@/test/server'
import { renderWithProviders } from '@/test/render'
import { IncidentsPage } from './Incidents'

// The incident list.
//
// The incidents are the PRODUCT's, and this page used to add rows the BROWSER
// made up -- one per uploaded video, with the severity taken round-robin from
// a list -- and those rows sat in the same table as the real ones. So the
// assertions here are about rows that came from the product, about the
// severity being a WORD rather than a colour, and about the difference
// between a list that is empty and a list that is unknown.

// The product's readers retry once before they give up, and the retry waits a
// second. The error state therefore arrives later than the default wait for
// an element, so the wait is longer here.
const ERROR_WAIT = { timeout: 5000 }

const INCIDENT = {
  incidentId: 'inc-1',
  siteId: 'site-demo',
  state: 'open',
  severity: 'critical',
  title: 'Person at the loading bay',
  ruleId: 'rule-motion',
  createdAt: '2026-10-21T08:00:00Z',
  firstEventAt: '2026-10-21T07:59:58Z',
  degraded: false,
  ruleBundleVersion: 'bundle-3',
  policyVersion: 'policy-2',
}

describe('the incidents page', () => {
  it('draws the incidents the product raised', async () => {
    server.use(http.get('/v1/incidents', () => HttpResponse.json({ items: [INCIDENT] })))

    renderWithProviders(<IncidentsPage />)

    expect(await screen.findByText('Person at the loading bay')).toBeInTheDocument()
    expect(screen.getByText('site-demo')).toBeInTheDocument()
    expect(screen.getByText('rule-motion')).toBeInTheDocument()
  })

  it('shows the severity and the state as words', async () => {
    // A colour is never the only channel: a reader who cannot separate the
    // hues must still be able to read the state.
    server.use(http.get('/v1/incidents', () => HttpResponse.json({ items: [INCIDENT] })))

    renderWithProviders(<IncidentsPage />)

    expect(await screen.findByText('critical')).toBeInTheDocument()
    expect(screen.getByText('open')).toBeInTheDocument()
  })

  it('says the product has raised no incident', async () => {
    server.use(http.get('/v1/incidents', () => HttpResponse.json({ items: [] })))

    renderWithProviders(<IncidentsPage />)

    expect(
      await screen.findByText(/An install that has ingested nothing has nothing to show/),
    ).toBeInTheDocument()
    // An empty list is not a list of dashes: a dash here would be a
    // measurement of nothing wearing the clothes of a measurement.
    expect(screen.queryByText('—')).not.toBeInTheDocument()
    // And no incident that the product did not raise.
    expect(screen.queryByText('Person at the loading bay')).not.toBeInTheDocument()
  })

  it('says the product did not answer, and shows the refusal rather than the body', async () => {
    server.use(
      http.get('/v1/incidents', () =>
        HttpResponse.json({ error: 'site_not_assigned' }, { status: 403 }),
      ),
    )

    renderWithProviders(<IncidentsPage />)

    const alert = await screen.findByRole('alert', undefined, ERROR_WAIT)
    expect(alert).toHaveTextContent(/did not answer with the incident list/)
    // The refusal is the product's closed vocabulary, and it is what the
    // operator has to act on. The JSON around it is not.
    expect(alert).toHaveTextContent('site_not_assigned')
    expect(alert.textContent).not.toContain('{"error"')
    // An unknown list is not an empty list, so the table is not drawn.
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('searches the record from a labelled box', async () => {
    const user = userEvent.setup({ delay: null })
    server.use(http.get('/v1/incidents', () => HttpResponse.json({ items: [INCIDENT] })))

    renderWithProviders(<IncidentsPage />)

    // A search box announced only by its placeholder is a search box that
    // goes quiet the moment somebody types.
    const box = screen.getByLabelText('Search the incidents')
    await user.type(box, 'motion')
    await user.click(screen.getByRole('button', { name: 'Search' }))

    // The answers are the product's own moments: the device, the event and the
    // incident it belongs to.
    expect(await screen.findByRole('columnheader', { name: 'Camera / Zone' })).toBeInTheDocument()
    expect(screen.getByText('cam-1')).toBeInTheDocument()
    expect(screen.getByText('motion')).toBeInTheDocument()
  })
})
