import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '@/test/render'
import { CommandPaletteProvider } from '@/components/CommandPalette'
import { OperationsPage } from './Operations'
import { SettingsPage } from './Settings'

// The settings, and the one console preference.
//
// The settings screen says what the console is and what it is not: LTR, and
// with no login at all. It does not carry a density control -- the density
// lives in the operations screen's header, which is where the console's only
// preference is -- so the density contract is pinned here, because it is a
// SETTING and this is the file about settings.
//
// The density is a URL parameter and not a record in the browser. A density
// kept in the browser is a density that differs per browser, and the density
// of a screen is a property of the screen rather than of the machine somebody
// is reading it on.

// jsdom has no layout, and `scrollIntoView` is one of the layout calls it
// does not implement, so the operations screen throws on its way to the
// queue. The stub records where it was sent, which is the same contract the
// live route's test pins.
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
  window.history.replaceState(null, '', '/')
})

function renderOperations() {
  return renderWithProviders(
    <CommandPaletteProvider>
      <OperationsPage />
    </CommandPaletteProvider>,
    { route: '/console' },
  )
}

describe('the settings screen', () => {
  it('says what the console is', async () => {
    renderWithProviders(<SettingsPage />)

    expect(screen.getByRole('heading', { level: 1, name: 'Settings' })).toBeInTheDocument()
    // It used to say "Console preferences" and "Theme", which described a
    // page that read nothing. The page reads the PRODUCT now, and every cell
    // is a fact the product produced.
    expect(await screen.findByText('What this install is')).toBeInTheDocument()
    expect(await screen.findByText('The identity this console acts as')).toBeInTheDocument()
  })

  it('names the principal the console acts as, and does not invent one', async () => {
    renderWithProviders(<SettingsPage />)

    expect(await screen.findByText('tenant-demo')).toBeInTheDocument()
    expect(screen.getByText('operator')).toBeInTheDocument()
    // The site scope AND the shift artifact both name the site, so the
    // assertion is that the site is named at all rather than that it appears
    // exactly once.
    expect(screen.getAllByText('site-demo').length).toBeGreaterThan(0)
  })

  it('does not claim there is no auth, because the install has a principal', async () => {
    // This used to assert the sentence "No auth/session mechanism is
    // configured yet". That sentence was a statement about the product and it
    // was FALSE: the product has a configured principal, a capability model
    // on every route, a login rate limit and a step-up second factor.
    //
    // A page that states a falsehood about the install's security posture is
    // worse than no page, because an operator reads it. The test that pinned
    // the falsehood is replaced by one that pins the truth.
    renderWithProviders(<SettingsPage />)

    await screen.findByText('The identity this console acts as')
    expect(
      screen.queryByText(/No auth\/session mechanism is configured yet/),
    ).not.toBeInTheDocument()
    expect(
      screen.getByText(/This install has no in-app sign-in/),
    ).toBeInTheDocument()
  })

  it('says the AI plane is not an outage when it is not available', async () => {
    // A plane that is not available is not a failure: the deterministic core
    // raises the incident anyway, and every observation is recorded as
    // degraded with a named reason.
    renderWithProviders(<SettingsPage />)

    await screen.findByText('The identity this console acts as')
    expect(screen.getByText(/is not an outage/)).toBeInTheDocument()
  })
})

describe('the density control', () => {
  it('is a real labelled control', () => {
    renderOperations()

    const control = screen.getByRole('tablist', { name: 'Row density' })
    expect(control).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'comfortable' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'compact' })).toBeInTheDocument()
  })

  it('reads the density out of the URL', () => {
    // `?density=compact` is a link: it can be pasted to a colleague and it
    // survives a reload.
    window.history.replaceState(null, '', '/console?density=compact')

    renderOperations()

    expect(screen.getByRole('tab', { name: 'compact' })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('tab', { name: 'comfortable' })).toHaveAttribute(
      'aria-selected',
      'false',
    )
    expect(document.querySelector('[data-density]')).toHaveAttribute('data-density', 'compact')
  })

  it('deletes the parameter when the comfortable density is chosen', async () => {
    // Comfortable is the default, and the default is the absence of the
    // parameter: a URL that says `?density=comfortable` says something the
    // reader has to be told is the same as no parameter at all.
    const user = userEvent.setup({ delay: null })
    window.history.replaceState(null, '', '/console?density=compact')

    renderOperations()

    await user.click(screen.getByRole('tab', { name: 'comfortable' }))

    expect(window.location.search).toBe('')
    expect(screen.getByRole('tab', { name: 'comfortable' })).toHaveAttribute(
      'aria-selected',
      'true',
    )
    expect(document.querySelector('[data-density]')).toHaveAttribute('data-density', 'comfortable')
  })

  it('writes the parameter when the compact density is chosen', async () => {
    const user = userEvent.setup({ delay: null })

    renderOperations()

    await user.click(screen.getByRole('tab', { name: 'compact' }))

    expect(window.location.search).toBe('?density=compact')
  })
})
