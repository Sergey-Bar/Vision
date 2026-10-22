import { beforeAll, describe, expect, it, vi } from 'vitest'
import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '@/test/server'
import { incidentSummary, principal } from '@/test/fixtures'
import { renderWithProviders } from '@/test/render'
import { CommandPaletteProvider } from './CommandPalette'
import { TopBar } from './TopBar'

// The top bar.
//
// There is no user here, and the bar does not pretend there is. The console
// has no login: the API runs as a configured principal, and every action in
// the ledger is attributed to that principal rather than to a person.
//
// The request id is the SERVER's. `web/middleware.go` assigns one to every
// request the console server answers, echoes it back in the response header,
// and puts it in the access log; the product's API reads it off the request
// the console server forwards. The browser never sees it and never chooses
// it, because a correlation id the browser chose is an instruction rather
// than a fact -- the same rule as the tenant header, which the console
// stopped believing for exactly that reason.

interface Seen {
  path: string
  headers: Headers
}

// The bar's own actions are not what this file is about.
const noMenu = () => undefined

beforeAll(() => {
  // jsdom has no layout, and the palette scrolls the selected row into view
  // when it opens.
  Element.prototype.scrollIntoView = vi.fn()
})

describe('the top bar', () => {
  it('asks the console\'s own server, and names no id of its own', async () => {
    // The console must not hold the product's credential, so every call
    // goes to the console's own server, which is the process that knows the
    // tenant and assigns the request id.
    const seen: Seen[] = []
    server.use(
      http.get('*', ({ request }) => {
        const url = new URL(request.url)
        seen.push({ path: url.pathname, headers: request.headers })
        if (url.pathname === '/v1/health/ready') {
          return HttpResponse.json({
            status: 'ok',
            checks: { aiRuntime: { ok: true, detail: 'ready' } },
          })
        }
        if (url.pathname === '/v1/incidents') {
          return HttpResponse.json({ items: [incidentSummary] })
        }
        if (url.pathname === '/v1/principal') {
          return HttpResponse.json(principal)
        }
        return HttpResponse.json({})
      }),
    )

    renderWithProviders(
      <CommandPaletteProvider>
        <TopBar onMenu={noMenu} />
      </CommandPaletteProvider>,
    )
    await waitFor(() => expect(seen.length).toBeGreaterThan(0))

    for (const request of seen) {
      expect(request.path.startsWith('/v1/')).toBe(true)
      // No client-chosen correlation id and no client-chosen tenant: both
      // are the server's to hold, and both were instructions once they were
      // the browser's.
      expect(request.headers.get('x-request-id')).toBeNull()
      expect(request.headers.get('x-tenant-id')).toBeNull()
    }
  })

  it('has a search trigger that is a real button with a name', () => {
    // The icon has no text of its own, so the name is the only thing a
    // screen reader has to go on.
    renderWithProviders(
      <CommandPaletteProvider>
        <TopBar onMenu={noMenu} />
      </CommandPaletteProvider>,
    )
    const trigger = screen.getByRole('button', { name: 'Search the record' })
    expect(trigger.tagName).toBe('BUTTON')
  })

  it('opens the command palette from the search trigger', async () => {
    // The button exists for the operator whose hands are not on the keyboard.
    // The keyboard has ⌘K; this is the other way in, and it opens the SAME
    // palette -- two searches would be two answers.
    const user = userEvent.setup()
    renderWithProviders(
      <CommandPaletteProvider>
        <TopBar onMenu={noMenu} />
      </CommandPaletteProvider>,
    )
    const trigger = screen.getByRole('button', { name: 'Search the record' })
    await user.click(trigger)
    expect(await screen.findByRole('dialog', { name: 'Search the record' })).toBeInTheDocument()
  })

  it('does not offer a sign-out for a console that never signed in', async () => {
    // A sign-out button on a console that never signed in is a control that
    // lies about what the system is. The dropdown says so in words, and the
    // dropdown is where a sign-out would have lived.
    const user = userEvent.setup()
    renderWithProviders(
      <CommandPaletteProvider>
        <TopBar onMenu={noMenu} />
      </CommandPaletteProvider>,
    )
    await user.click(screen.getByRole('button', { name: /Service principal/ }))
    const menu = await screen.findByRole('menu')
    expect(menu).toHaveTextContent('there is no person to sign out of')
    expect(screen.queryByRole('menuitem', { name: /sign out/i })).toBeNull()
  })

  it('renders the site\'s clock with its offset, and says UTC when it does not know', async () => {
    // The clock is the SITE's, not the host's, and it says so: a time
    // without an offset is a time two people will read differently.
    renderWithProviders(
      <CommandPaletteProvider>
        <TopBar onMenu={noMenu} />
      </CommandPaletteProvider>,
    )
    await waitFor(() => expect(screen.getByText(/UTC \(site zone unknown\)/)).toBeInTheDocument())
  })
})
