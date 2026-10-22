import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SegmentedControl } from './SegmentedControl'

// The segmented control.
//
// The design system's rule is "at most one filled element per screen", and
// this is the control that rule is about: a row of tabs fills every tab it
// can, and the screen then has six filled things and no emphasis.
//
// The word is always present. An icon-only segment is a segment whose meaning
// lives in a shape.

const segments = [
  { value: 'all', label: 'all' },
  { value: 'motion', label: 'motion' },
  { value: 'people', label: 'people' },
] as const

describe('the segmented control', () => {
  it('is a tablist, and every segment is a tab', () => {
    // A control that is a row of buttons is a row of buttons: a screen
    // reader cannot say how many options there are or which one is chosen.
    render(
      <SegmentedControl segments={[...segments]} value="all" onChange={() => undefined} label="What to show" />,
    )
    expect(screen.getByRole('tablist', { name: 'What to show' })).toBeInTheDocument()
    expect(screen.getAllByRole('tab')).toHaveLength(3)
  })

  it('says which segment is selected', () => {
    render(
      <SegmentedControl segments={[...segments]} value="motion" onChange={() => undefined} label="What to show" />,
    )
    const selected = screen.getByRole('tab', { selected: true })
    expect(selected).toHaveTextContent('motion')
    expect(screen.getByRole('tab', { name: 'all' })).toHaveAttribute('aria-selected', 'false')
  })

  it('always shows the word', () => {
    // An icon-only segment is a segment whose meaning lives in a shape, and
    // a shape is the thing this design system refuses.
    render(
      <SegmentedControl segments={[...segments]} value="all" onChange={() => undefined} label="What to show" />,
    )
    for (const segment of segments) {
      expect(screen.getByRole('tab', { name: segment.label })).toHaveTextContent(segment.label)
    }
  })

  it('reports the segment that was chosen', async () => {
    // The VALUE, not the index: the segments are a set of values and the
    // caller should not have to know which one is where.
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(
      <SegmentedControl
        segments={[...segments]}
        value="all"
        onChange={onChange}
        label="What to show"
      />,
    )
    await user.click(screen.getByRole('tab', { name: 'people' }))
    expect(onChange).toHaveBeenCalledWith('people')
  })

  it('fills the selected segment and only that one', () => {
    // "At most one filled element per screen" asserted on the control that
    // exists to obey it. Two filled segments is a screen with no emphasis.
    const { rerender } = render(
      <SegmentedControl segments={[...segments]} value="all" onChange={() => undefined} label="What to show" />,
    )
    expect(screen.getByRole('tab', { name: 'all' })).toHaveClass('bg-accent')
    expect(screen.getByRole('tab', { name: 'motion' })).not.toHaveClass('bg-accent')

    rerender(
      <SegmentedControl
        segments={[...segments]}
        value="motion"
        onChange={() => undefined}
        label="What to show"
      />,
    )
    expect(screen.getByRole('tab', { name: 'motion' })).toHaveClass('bg-accent')
    expect(screen.getByRole('tab', { name: 'all' })).not.toHaveClass('bg-accent')
  })
})
