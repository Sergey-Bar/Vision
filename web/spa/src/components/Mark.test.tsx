import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { Mark, Wordmark } from './Mark'

// The mark.
//
// There is ONE mark, and it is the hexagonal sensor chassis. The console
// used to draw a red rounded square with a "V" in it, the showcase drew a
// blue rounded square, the operations page drew a white hexagon on nothing,
// and the hub drew the real one: four marks for one product, and the
// console's was the same red as "critical", so the logo announced an
// emergency before anything had happened.
//
// So the mark is the mark, and the NAME is beside it. The mark itself is
// decoration: a screen reader is told the product's name, not the shape.

describe('the mark', () => {
  it('is one mark and not four', () => {
    // Four marks for one product is four things to keep in step, and the one
    // somebody forgets is the one that ships.
    const { container } = render(<Mark />)
    expect(container.querySelectorAll('svg')).toHaveLength(1)
  })

  it('is decoration, and the name is beside it', () => {
    // The shape carries the brand, not the meaning. A screen reader is told
    // "VISION", and a glyph that announced itself would be read out as
    // "graphic" on every page.
    const { container } = render(<Mark />)
    const shape = container.querySelector('svg')
    expect(shape).toHaveAttribute('aria-hidden', 'true')
  })

  it('is drawn once in the wordmark', () => {
    // The wordmark is the mark and the name together. Two marks in one
    // header is the defect the brand gate looks for.
    const { container } = render(<Wordmark />)
    expect(container.querySelectorAll('svg')).toHaveLength(1)
    expect(screen.getByText('VISION')).toBeInTheDocument()
  })

  it('says what the product is, under the name', () => {
    render(<Wordmark />)
    expect(screen.getByText('The system that says what it does not know.')).toBeInTheDocument()
  })
})
