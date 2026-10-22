import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import {
  AiUnavailableState,
  EmptyState,
  ErrorState,
  LoadingState,
  PartialState,
  Skeleton,
} from './States'

// The five states.
//
// Every region of every screen implements all five, and LOADING IS NEVER
// RENDERED AS DATA.
//
// The Cockpit used to render `0`, `unknown` and `not configured` while the
// request was still in flight. A zero that is really "I have not asked yet" is
// the most expensive kind of lie in an instrument panel, because it is
// indistinguishable from a measurement.
//
// The five are states and not five shades of grey: each says something
// different, in different words.

describe('the five states', () => {
  it('say five different things', () => {
    // The states are distinguishable by their WORDS. A screen that renders
    // five grey boxes is a screen that answers every question the same way.
    const texts = [
      render(<LoadingState />).container.textContent,
      render(<EmptyState title="Nothing here" detail="because nothing is" />).container.textContent,
      render(<ErrorState title="It did not load" detail="the product did not answer" />).container
        .textContent,
      render(<PartialState missing={[]}>what arrived</PartialState>).container.textContent,
      render(<AiUnavailableState reason="the model was not asked" />).container.textContent,
    ]
    expect(new Set(texts).size).toBe(5)
  })

  it('are five different pieces of markup, not five shades of grey', () => {
    // The classes are the surface, and five surfaces that differ only in
    // their grey are five states an operator has to read the words of.
    const classes = [
      render(<LoadingState />).container.firstElementChild?.getAttribute('class') ?? '',
      render(<EmptyState title="x" />).container.firstElementChild?.getAttribute('class') ?? '',
      render(<ErrorState />).container.firstElementChild?.getAttribute('class') ?? '',
      render(<PartialState missing={[]}>x</PartialState>).container.firstElementChild?.getAttribute(
        'class',
      ) ?? '',
      render(<AiUnavailableState reason="x" />).container.firstElementChild?.getAttribute('class') ?? '',
    ]
    expect(new Set(classes).size).toBe(5)
  })

  it('announce the loading and the error differently', () => {
    // A loading region is a status, an error region is an alert. One is
    // announced when it changes, the other interrupts.
    render(<LoadingState />)
    expect(screen.getByRole('status')).toBeInTheDocument()
    render(<ErrorState />)
    expect(screen.getByRole('alert')).toBeInTheDocument()
  })
})

describe('the loading state', () => {
  it('never renders a zero', () => {
    // The rule the file states in its own words: a zero that is really "I
    // have not asked yet" is indistinguishable from a measurement.
    const { container } = render(<LoadingState />)
    expect(container.textContent).not.toContain('0')
  })
})

describe('the empty state', () => {
  it('says what is empty, why, and what can be done', () => {
    // "No data" is not an answer: it does not say whether the thing is
    // absent, whether the request failed, or whether the product is down.
    render(
      <EmptyState
        title="No camera has reported"
        detail="The VMS has not answered since the install came up."
        action={<button type="button">Ask again</button>}
      />,
    )
    expect(screen.getByText('No camera has reported')).toBeInTheDocument()
    expect(screen.getByText('The VMS has not answered since the install came up.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Ask again' })).toBeInTheDocument()
  })
})

describe('the partial state', () => {
  it('gives every missing item its own reason', () => {
    // A gap is not an answer. One reason for four gaps tells the operator
    // that four things failed the same way, which nobody knows.
    render(
      <PartialState
        missing={[
          { label: 'Camera 3', reason: 'the camera did not answer' },
          { label: 'Camera 4', reason: 'the clip is still being written' },
        ]}
      >
        <p>what did arrive</p>
      </PartialState>,
    )
    expect(screen.getByText('Camera 3')).toBeInTheDocument()
    expect(screen.getByText('the camera did not answer')).toBeInTheDocument()
    expect(screen.getByText('Camera 4')).toBeInTheDocument()
    expect(screen.getByText('the clip is still being written')).toBeInTheDocument()
  })
})

describe('the AI-unavailable state', () => {
  it('says the observation is an absence and not a negative answer', () => {
    // The third invariant: a missing AI observation is never a negative
    // observation. The screen has to say which one it is showing.
    render(<AiUnavailableState reason="the runtime was not reachable" />)
    expect(screen.getByText(/This is an absence, not a negative answer/)).toBeInTheDocument()
    expect(screen.getByText('the runtime was not reachable')).toBeInTheDocument()
  })
})

describe('the skeleton', () => {
  it('is shape, not content', () => {
    // A skeleton is the loading state made of shape. It is not content, so
    // a screen reader must not read it.
    const { container } = render(<Skeleton />)
    const shape = container.firstElementChild
    expect(shape).toHaveAttribute('aria-hidden', 'true')
  })
})
