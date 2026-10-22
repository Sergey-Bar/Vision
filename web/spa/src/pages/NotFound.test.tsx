import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithProviders } from '@/test/render'
import { NotFound } from './NotFound'

// The 404.
//
// The route the operator asked for is not one the console has, and the page
// has to say THAT rather than showing an empty screen or a list of links that
// are not the links they wanted. It also has to offer the way back, because a
// page that only says "no" is a page that leaves the operator to guess.

describe('the page for a route that does not exist', () => {
  it('says the route is not one the console has', () => {
    renderWithProviders(<NotFound />)

    expect(screen.getByText('404')).toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 1, name: 'Page not found' })).toBeInTheDocument()
    expect(
      screen.getByText(/The route you requested doesn't exist in the console/),
    ).toBeInTheDocument()
  })

  it('offers a way back', () => {
    renderWithProviders(<NotFound />)

    const back = screen.getByRole('link', { name: 'Return to Live' })
    expect(back).toHaveAttribute('href', '/')
  })

  it('does not pretend to be a screen of the console', () => {
    // A 404 that renders a table of rows it made up would be a second copy of
    // the truth, on the one screen where the operator already knows they are
    // lost.
    renderWithProviders(<NotFound />)

    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
  })
})
