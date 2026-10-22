import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Button, IconButton, type ButtonProps } from './Button'

// The button.
//
// ONE filled element per screen, and this is the only thing that can be one.
// The console used to fill the active nav item, every primary button, the
// brand mark and the clock pill at the same time, so nothing on the screen
// was the emphasis; everything was.
//
// The three variants and the two sizes are the whole scale. A third size is a
// decision nobody makes on purpose.

function buttonClass(props: ButtonProps): string {
  const { container, unmount } = render(<Button {...props}>Go</Button>)
  const className = container.querySelector('button')?.getAttribute('class') ?? ''
  unmount()
  return className
}

describe('the button', () => {
  it('gives the three variants three different looks', () => {
    // The variant is the emphasis. Two variants that render the same are one
    // variant with two names, and the reader cannot tell which button is
    // the one the screen wants pressed.
    const primary = buttonClass({ variant: 'primary' })
    const secondary = buttonClass({ variant: 'secondary' })
    const ghost = buttonClass({ variant: 'ghost' })
    expect(new Set([primary, secondary, ghost]).size).toBe(3)
    expect(primary).toContain('bg-accent')
    expect(secondary).toContain('border-line')
    expect(ghost).toContain('border-transparent')
  })

  it('gives the two sizes two different looks', () => {
    // A dense console needs a small control and a normal one. A third is a
    // decision nobody makes on purpose.
    const small = buttonClass({ size: 'sm' })
    const normal = buttonClass({ size: 'md' })
    expect(small).not.toBe(normal)
    expect(small).toContain('h-8')
    expect(normal).toContain('h-10')
  })

  it('is a real button that fires its handler', async () => {
    const user = userEvent.setup()
    const onClick = vi.fn()
    render(<Button onClick={onClick}>Acknowledge</Button>)
    const button = screen.getByRole('button', { name: 'Acknowledge' })
    expect(button.tagName).toBe('BUTTON')
    await user.click(button)
    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('does not fire when it is disabled', async () => {
    // A disabled control that still fires is a control that lies about being
    // disabled, and an operator pressing it is told the action was taken.
    const user = userEvent.setup()
    const onClick = vi.fn()
    render(
      <Button disabled onClick={onClick}>
        Acknowledge
      </Button>,
    )
    const button = screen.getByRole('button', { name: 'Acknowledge' })
    expect(button).toBeDisabled()
    await user.click(button)
    expect(onClick).not.toHaveBeenCalled()
  })

  it('fires from the keyboard', async () => {
    // The console is a keyboard instrument: a control that only answers the
    // mouse is a control the operator cannot reach.
    const user = userEvent.setup()
    const onClick = vi.fn()
    render(<Button onClick={onClick}>Acknowledge</Button>)
    const button = screen.getByRole('button', { name: 'Acknowledge' })
    button.focus()
    await user.keyboard('{Enter}')
    expect(onClick).toHaveBeenCalledTimes(1)
  })
})

describe('the icon button', () => {
  it('carries the same name in aria-label and in title', () => {
    // The icon has no text of its own, so the label is the ONLY name a screen
    // reader has. Two names are two names to keep in step.
    render(<IconButton label="Pause the camera" />)
    const button = screen.getByRole('button', { name: 'Pause the camera' })
    expect(button).toHaveAttribute('aria-label', 'Pause the camera')
    expect(button).toHaveAttribute('title', 'Pause the camera')
  })

  it('has no text of its own beyond that name', () => {
    // The name is the label's job. A glyph that also says something is a
    // second, unnamed channel.
    render(<IconButton label="Pause the camera" />)
    const button = screen.getByRole('button', { name: 'Pause the camera' })
    expect(button.textContent).toBe('')
  })
})
