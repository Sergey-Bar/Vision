import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { PageHead, Panel, Section } from './Panel'

// The surfaces.
//
// A panel is a hairline and a tint step. There is no shadow: on this ground a
// shadow reads as a smudge.
//
// The headings are the other half of it. A console is a document an operator
// scans, and a document whose titles are not headings is a document a screen
// reader cannot navigate.

describe('the panel', () => {
  it('puts the title in a heading', () => {
    // The heading is what the reader navigates by, and a title in a <div> is
    // a title nobody can jump to.
    render(
      <Panel title="What needs you">
        <p>the queue</p>
      </Panel>,
    )
    expect(screen.getByRole('heading', { level: 2, name: 'What needs you' })).toBeInTheDocument()
  })

  it('renders the action', () => {
    render(
      <Panel title="What needs you" action={<button type="button">Acknowledge all</button>}>
        <p>the queue</p>
      </Panel>,
    )
    expect(screen.getByRole('button', { name: 'Acknowledge all' })).toBeInTheDocument()
  })

  it('renders no header at all when it has neither a title nor an action', () => {
    // A header with an empty title and an empty action is a band of space
    // that says nothing.
    const { container } = render(
      <Panel>
        <p>the queue</p>
      </Panel>,
    )
    expect(container.querySelector('header')).toBeNull()
    expect(container.querySelector('h2')).toBeNull()
  })
})

describe('the section', () => {
  it('renders the eyebrow and the title', () => {
    // The eyebrow says where the reader is; the title says what is here.
    render(
      <Section eyebrow="Watch" title="The camera wall">
        <p>the wall</p>
      </Section>,
    )
    expect(screen.getByText('Watch')).toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 2, name: 'The camera wall' })).toBeInTheDocument()
  })
})

describe('the page head', () => {
  it('has exactly one h1, and it is the title', () => {
    // One h1 per screen. The focus moves to it on every route change, and a
    // screen with two of them moves the focus to whichever the DOM happens
    // to put first.
    const { container } = render(
      <PageHead title="Operations" lede="What needs the operator now." />,
    )
    const headings = container.querySelectorAll('h1')
    expect(headings).toHaveLength(1)
    expect(headings[0]).toHaveTextContent('Operations')
  })

  it('renders the lede', () => {
    render(<PageHead title="Operations" lede="What needs the operator now." />)
    expect(screen.getByText('What needs the operator now.')).toBeInTheDocument()
  })
})
