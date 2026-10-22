import { beforeAll, describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from './dropdown-menu'

// The dropdown menu.
//
// It is Radix, and Radix's behaviour is the behaviour: the content is not in
// the document until the trigger is activated, Escape closes it, and the
// items are reachable without a mouse.
//
// The stubs below are for jsdom, which has no layout and no pointer capture.
// They are the ENVIRONMENT's gaps, not the menu's: a menu that could only be
// opened in a browser with a layout would be a menu the test suite cannot
// reach at all.

beforeAll(() => {
  if (typeof globalThis.ResizeObserver === 'undefined') {
    globalThis.ResizeObserver = class {
      observe(): void {
        return undefined
      }
      unobserve(): void {
        return undefined
      }
      disconnect(): void {
        return undefined
      }
    }
  }
  if (typeof Element.prototype.getBoundingClientRect !== 'function') {
    Element.prototype.getBoundingClientRect = () =>
      ({ x: 0, y: 0, width: 0, height: 0, top: 0, left: 0, right: 0, bottom: 0 }) as DOMRect
  }
  if (typeof window.HTMLElement.prototype.hasPointerCapture !== 'function') {
    window.HTMLElement.prototype.hasPointerCapture = () => false
    window.HTMLElement.prototype.setPointerCapture = () => undefined
    window.HTMLElement.prototype.releasePointerCapture = () => undefined
  }
})

function Menu() {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger>Actions</DropdownMenuTrigger>
      <DropdownMenuContent>
        <DropdownMenuItem onSelect={() => undefined}>Acknowledge</DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => undefined}>Escalate</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

describe('the dropdown menu', () => {
  it('keeps the content out of the document until the trigger is activated', async () => {
    // A menu that is in the document before it is opened is a menu a screen
    // reader walks through on every page, and a menu whose items are in the
    // tab order of a page that has no menu open.
    const user = userEvent.setup()
    render(<Menu />)
    expect(screen.queryByRole('menu')).toBeNull()
    expect(screen.queryByRole('menuitem', { name: 'Acknowledge' })).toBeNull()

    await user.click(screen.getByText('Actions'))
    expect(await screen.findByRole('menu')).toBeInTheDocument()
  })

  it('shows the items when it opens', async () => {
    // The items are the reason the menu exists.
    const user = userEvent.setup()
    render(<Menu />)
    await user.click(screen.getByText('Actions'))
    expect(await screen.findByRole('menuitem', { name: 'Acknowledge' })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: 'Escalate' })).toBeInTheDocument()
  })

  it('closes on Escape', async () => {
    // The keyboard has to be able to leave. A menu that cannot be closed
    // with the keyboard is a trap with a menu in it.
    const user = userEvent.setup()
    render(<Menu />)
    await user.click(screen.getByText('Actions'))
    expect(await screen.findByRole('menuitem', { name: 'Acknowledge' })).toBeInTheDocument()

    await user.keyboard('{Escape}')
    await expect(screen.findByRole('menu')).rejects.toThrow()
    expect(screen.queryByRole('menuitem', { name: 'Acknowledge' })).toBeNull()
  })

  it('puts the items in the keyboard\'s reach', async () => {
    // The menu opens with the CONTENT focused and the arrow key walks the
    // items. A menu whose items cannot be walked is a menu the keyboard
    // cannot read.
    const user = userEvent.setup()
    render(<Menu />)
    await user.click(screen.getByText('Actions'))
    await screen.findByRole('menuitem', { name: 'Acknowledge' })

    await user.keyboard('{ArrowDown}')
    const first = screen.getByRole('menuitem', { name: 'Acknowledge' })
    expect(first).toHaveFocus()
    expect(first).toHaveAttribute('data-highlighted')

    await user.keyboard('{ArrowDown}')
    expect(screen.getByRole('menuitem', { name: 'Escalate' })).toHaveFocus()
  })
})
