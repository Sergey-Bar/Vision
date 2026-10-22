import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import { server } from '@/test/server'
import { renderWithProviders } from '@/test/render'
import { CommandPaletteProvider } from '@/components/CommandPalette'
import { EventsPage } from './Events'

// The events route.
//
// It is a SCROLL TARGET and not a page. The record used to be a page of its
// own with a search box of its own, and the Cockpit had a second search box
// over a second query -- so a console with one record had two searches, and
// the two answers did not have to agree.
//
// So what this route does is answer with the operations screen, scrolled to
// the record. The tests are about that: one screen, the record in it, and the
// moments in it coming from the product.

// The product's readers retry once before they give up, and the retry waits a
// second. The error state therefore arrives later than the default wait for
// an element, so the wait is longer here.
const ERROR_WAIT = { timeout: 5000 }

// jsdom has no layout, and `scrollIntoView` is one of the layout calls it
// does not implement, so a page that scrolls to a section throws on the way
// there. The stub records what was scrolled to, which is the whole of what
// this file asserts about the scroll target.
type ScrollIntoView = (options?: boolean | ScrollIntoViewOptions) => void
const elementProto = Element.prototype as unknown as { scrollIntoView?: ScrollIntoView }
const originalScrollIntoView = elementProto.scrollIntoView
const scrolled: string[] = []

beforeEach(() => {
  scrolled.length = 0
  elementProto.scrollIntoView = function scrollIntoViewStub(this: Element) {
    scrolled.push(this.id)
  }
})

afterEach(() => {
  elementProto.scrollIntoView = originalScrollIntoView
})

function renderEvents() {
  return renderWithProviders(
    <CommandPaletteProvider>
      <EventsPage />
    </CommandPaletteProvider>,
    { route: '/console/events' },
  )
}

describe('the events route', () => {
  it('answers with the operations screen, and not with a page of its own', async () => {
    renderEvents()

    expect(await screen.findByRole('heading', { level: 1, name: 'Operations' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'The record' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'The camera wall' })).toBeInTheDocument()
  })

  it('scrolls to the record', async () => {
    renderEvents()

    await waitFor(() => expect(scrolled).toContain('record'))
  })

  it('lists the moments the product holds', async () => {
    // The record is the product's canonical events: a time, a camera and an
    // event, which is the only shape a reader can go and check against the
    // picture.
    renderEvents()

    expect(await screen.findByText('motion')).toBeInTheDocument()
    expect(screen.getByText('cam-1')).toBeInTheDocument()
    const incident = screen.getByRole('link', { name: 'inc-1' })
    expect(incident).toHaveAttribute('href', '/console/incidents/inc-1')
  })

  it('says the platform has not seen anything yet', async () => {
    server.use(http.get('/v1/evidence', () => HttpResponse.json({ hits: [] })))

    renderEvents()

    expect(
      await screen.findByText(/Nothing yet\. The platform records what the VMS tells it/),
    ).toBeInTheDocument()
    // No moment that the product does not hold.
    expect(screen.queryByText('motion')).not.toBeInTheDocument()
  })

  it('says the product did not answer', async () => {
    server.use(
      http.get('/v1/evidence', () =>
        HttpResponse.json({ error: 'site_not_assigned' }, { status: 403 }),
      ),
    )

    renderEvents()

    const alert = await screen.findByRole('alert', undefined, ERROR_WAIT)
    expect(alert).toHaveTextContent(/The product did not answer/)
    // The list below is empty because the product did not say, not because
    // nothing happened, and the refusal is what the operator has to act on.
    expect(alert).toHaveTextContent('site_not_assigned')
    expect(alert.textContent).not.toContain('{"error"')
  })
})
