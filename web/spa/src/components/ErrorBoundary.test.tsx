import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ErrorBoundary } from './ErrorBoundary'

// The error boundary.
//
// A screen that throws and shows nothing is a screen the operator reads as
// "the console is down", and a console that is down is a different incident
// from a screen that has a bug in it. The boundary exists to say which one
// it is.
//
// React logs the error to the console on its way past. The log is React's
// business, and the test is about what the operator sees.

function Explodes(): never {
  throw new Error('the screen could not render')
}

describe('the error boundary', () => {
  it('shows the fallback instead of a blank screen', () => {
    // The blank screen is the defect. An operator who cannot tell a broken
    // screen from a broken console cannot report it.
    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    try {
      render(
        <ErrorBoundary>
          <Explodes />
        </ErrorBoundary>,
      )
      expect(
        screen.getByRole('heading', {
          level: 1,
          name: 'The console hit an error it could not recover from.',
        }),
      ).toBeInTheDocument()
      expect(screen.getByText('This screen did not render')).toBeInTheDocument()
      expect(screen.getByText('the screen could not render')).toBeInTheDocument()
    } finally {
      logged.mockRestore()
    }
  })

  it('renders its children when nothing throws', () => {
    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    try {
      render(
        <ErrorBoundary>
          <p>the queue</p>
        </ErrorBoundary>,
      )
      expect(screen.getByText('the queue')).toBeInTheDocument()
      expect(screen.queryByRole('heading', { level: 1 })).toBeNull()
    } finally {
      logged.mockRestore()
    }
  })
})
