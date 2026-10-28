import { beforeAll, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useLocation } from 'react-router-dom'
import { renderWithProviders } from '@/test/render'
import { axe } from '@/test/axe'
import { CommandPaletteProvider, usePalette } from './CommandPalette'
import { Shell } from './Shell'

// The command palette.
//
// ONE search. The console used to have two, over two different queries, with
// two different empty states and two different histories, and an operator who
// searched twice searched twice.
//
// It is opened three ways: ⌘K from anywhere, `/` from anywhere, and the
// always-visible input in the column. The results are the record's MOMENTS,
// and the commands are the PLACES the record can be read.

beforeAll(() => {
  // jsdom has no layout, and scrolling the selected row into view is one of
  // the layout calls it does not implement.
  Element.prototype.scrollIntoView = vi.fn()
  Element.prototype.scrollTo = vi.fn()
})

/** A probe that writes the router's path into the document. */
function LocationProbe() {
  const location = useLocation()
  return <span data-testid="path">{location.pathname}</span>
}

/** The harness: the palette's own API, which is how the top bar opens it. */
function Harness() {
  const palette = usePalette()
  return (
    <>
      <button type="button" onClick={palette.open}>
        open the palette
      </button>
      <LocationProbe />
    </>
  )
}

function renderPalette() {
  return renderWithProviders(
    <CommandPaletteProvider>
      <Harness />
    </CommandPaletteProvider>,
  )
}

describe('the command palette', () => {
  it('opens on the command key', async () => {
    // The operator's hands are on the keyboard, and the key is ⌘K. The
    // hotkey belongs to the screen, so the screen has to be mounted.
    const user = userEvent.setup()
    renderWithProviders(<Shell />, { route: '/' })
    await screen.findByRole('heading', { level: 1, name: 'VMS Workstation' })

    await user.keyboard('{Meta>}k{/Meta}')
    expect(await screen.findByRole('dialog', { name: 'Search the record' })).toBeInTheDocument()
  })

  it('opens on the slash', async () => {
    // The palette IS the search, so the search's key opens it too.
    const user = userEvent.setup()
    renderWithProviders(<Shell />, { route: '/' })
    await screen.findByRole('heading', { level: 1, name: 'VMS Workstation' })

    await user.keyboard('/')
    expect(await screen.findByRole('dialog', { name: 'Search the record' })).toBeInTheDocument()
  })

  it('filters as the operator types', async () => {
    // A palette that shows everything is a list, and a list of ten things is
    // a list the operator reads rather than searches.
    const user = userEvent.setup()
    renderPalette()
    await user.click(screen.getByRole('button', { name: 'open the palette' }))
    const dialog = await screen.findByRole('dialog', { name: 'Search the record' })
    const input = within(dialog).getByRole('textbox')

    expect(within(dialog).getByText('The operations page')).toBeInTheDocument()
    await user.type(input, 'audit')

    await waitFor(() => expect(within(dialog).queryByText('The operations page')).toBeNull())
    expect(within(dialog).getByText('The audit ledger')).toBeInTheDocument()
  })

  it('holds the record\'s moments and the places the record can be read', async () => {
    // Two different things in one list: what HAPPENED, and where to LOOK. A
    // palette with only the moments cannot answer "where do I see this", and
    // one with only the places cannot answer "what happened".
    const user = userEvent.setup()
    renderPalette()
    await user.click(screen.getByRole('button', { name: 'open the palette' }))
    const dialog = await screen.findByRole('dialog', { name: 'Search the record' })
    const input = within(dialog).getByRole('textbox')

    await user.type(input, 'record')
    await waitFor(() => expect(within(dialog).getByText('motion')).toBeInTheDocument())
    expect(within(dialog).getByText('The record')).toBeInTheDocument()
  })

  it('moves the selection with the arrow keys and opens what is selected', async () => {
    // The keyboard has to be able to choose as well as type. Enter opens the
    // row under the cursor, and the cursor is where the reader put it.
    const user = userEvent.setup()
    renderPalette()
    await user.click(screen.getByRole('button', { name: 'open the palette' }))
    const dialog = await screen.findByRole('dialog', { name: 'Search the record' })
    const input = within(dialog).getByRole('textbox')
    await waitFor(() => expect(input).toHaveFocus())

    const selected = () => dialog.querySelector('[data-selected="true"]')
    expect(selected()).toHaveTextContent('The operations page')

    await user.keyboard('{ArrowDown}')
    expect(selected()).toHaveTextContent('The camera wall')

    await user.keyboard('{Enter}')
    await waitFor(() => expect(screen.getByTestId('path')).toHaveTextContent('/console/live'))
  })

  it('closes on Escape', async () => {
    // The keyboard has to be able to leave.
    const user = userEvent.setup()
    renderPalette()
    await user.click(screen.getByRole('button', { name: 'open the palette' }))
    await screen.findByRole('dialog', { name: 'Search the record' })

    await user.keyboard('{Escape}')
    await waitFor(() =>
      expect(screen.queryByRole('dialog', { name: 'Search the record' })).toBeNull(),
    )
  })

  it('is an accessible dialog', async () => {
    // A modal over the page, and a modal that a screen reader cannot enter
    // is a modal that traps the reader without giving them anything.
    const user = userEvent.setup()
    renderPalette()
    await user.click(screen.getByRole('button', { name: 'open the palette' }))
    const dialog = await screen.findByRole('dialog', { name: 'Search the record' })
    const results = await axe(dialog)
    expect(results.violations).toEqual([])
  })
})
