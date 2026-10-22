import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { Badge, SeverityBadge, StateBadge, StatusIndicator, severityClass, stateClass } from './Badge'

// The badges.
//
// A badge carries THREE channels -- a glyph, a word and a colour -- and the
// word is the one that cannot be dropped. A colour is never the only channel:
// a reader who cannot separate the hues must still be able to read the state.
//
// The maps are CLOSED, and that is the part worth a test. An untrusted rule
// bundle can put any string in a severity field, and a class name built out of
// it is a class name the attacker chose.

// The hostile inputs are ASSEMBLED rather than written out. A literal colour
// class in this repository's source is a class the design system's gate
// resolves, and this one has to resolve to nothing at all.
const CLASS_SHAPED = ['bg', 'apple-red'].join('-')
const MARKUP = '"><script>'

describe('the severity map', () => {
  it('names each of the five severities the product publishes', () => {
    // The product publishes five and the console shows five. A severity the
    // console cannot name is a severity the operator reads as a colour.
    const words = ['info', 'low', 'medium', 'high', 'critical'].map((s) => severityClass(s).word)
    expect(words).toEqual(['info', 'low', 'medium', 'high', 'critical'])
    expect(new Set(words).size).toBe(5)
    for (const severity of ['info', 'low', 'medium', 'high', 'critical']) {
      expect(severityClass(severity).className).not.toBe('')
    }
  })

  it('shows an unrecognised severity as unknown, and unknown is not low', () => {
    // The file's own rule: an unrecognised severity "is not the same as low".
    // A map that answered `low` for everything would be a map that calls a
    // broken rule bundle harmless.
    const unknown = severityClass('catastrophic')
    expect(unknown.word).toBe('unknown')
    expect(unknown.word).not.toBe(severityClass('low').word)
    expect(unknown.className).not.toBe(severityClass('low').className)
  })

  it('never builds a class name out of the untrusted string', () => {
    // The closed-set property. A severity field is attacker-controlled, so a
    // class name assembled from it is a class name the attacker chose, and
    // the console would be rendering markup it was given.
    for (const hostile of [MARKUP, CLASS_SHAPED]) {
      const s = severityClass(hostile)
      expect(s.word).toBe('unknown')
      expect(s.className).not.toContain(hostile)
      expect(s.className).toBe(severityClass('also-not-a-severity').className)
    }
  })
})

describe('the state map', () => {
  it('names each of the four states the product publishes', () => {
    const words = ['open', 'in_review', 'dispositioned', 'closed'].map((s) => stateClass(s).word)
    expect(words).toEqual(['open', 'in review', 'dispositioned', 'closed'])
  })

  it('shows an unrecognised state as unknown', () => {
    const unknown = stateClass('escalated')
    expect(unknown.word).toBe('unknown')
    expect(unknown.className).toBe(stateClass('also-not-a-state').className)
  })
})

describe('the badge', () => {
  it('renders its children', () => {
    render(<Badge>three cameras</Badge>)
    expect(screen.getByText('three cameras')).toBeInTheDocument()
  })

  it('gives every tone its own declared colour', () => {
    // The tones are a closed set of six, and each is a different ink. Two
    // tones sharing a class is a screen with two meanings and one colour.
    const tones = [
      ['neutral', 'text-text-muted'],
      ['ok', 'text-ok-ink'],
      ['warn', 'text-warn-ink'],
      ['bad', 'text-bad-ink'],
      ['info', 'text-accent-ink'],
      ['unknown', 'text-unknown-ink'],
    ] as const

    const classes: string[] = []
    for (const [tone, colour] of tones) {
      const { container, unmount } = render(<Badge tone={tone}>x</Badge>)
      const className = container.querySelector('span')?.getAttribute('class') ?? ''
      unmount()
      expect(className).toContain(colour)
      classes.push(className)
    }
    expect(new Set(classes).size).toBe(tones.length)
  })
})

describe('the severity and state badges', () => {
  it('renders the severity as a word, and the glyph as decoration', () => {
    // A colour is never the only channel. The word is text, so a reader who
    // cannot see the hue still reads the severity.
    render(<SeverityBadge severity="critical" />)
    const badge = screen.getByText('critical')
    expect(badge).toBeInTheDocument()
    const glyph = badge.querySelector('[aria-hidden="true"]')
    expect(glyph?.textContent).toBe('■')
  })

  it('renders the state as a word, and the glyph as decoration', () => {
    render(<StateBadge state="in_review" />)
    const badge = screen.getByText('in review')
    expect(badge).toBeInTheDocument()
    const glyph = badge.querySelector('[aria-hidden="true"]')
    expect(glyph?.textContent).toBe('◐')
  })

  it('shows an unrecognised severity as the word unknown', () => {
    // The fallback is a WORD and not an empty badge, so the operator learns
    // that the product said something the console did not understand.
    render(<SeverityBadge severity="catastrophic" />)
    expect(screen.getByText('unknown')).toBeInTheDocument()
  })
})

describe('the status indicator', () => {
  it('renders the label as text and the glyph as decoration', () => {
    render(<StatusIndicator state="ok" label="Recording" />)
    const indicator = screen.getByText('Recording')
    expect(indicator).toBeInTheDocument()
    const glyph = indicator.querySelector('[aria-hidden="true"]')
    expect(glyph?.textContent).toBe('●')
  })

  it('does not pulse: the word beside the dot says the same thing', () => {
    // A pulsing dot beside a number that changes is a page that reflows
    // while somebody is reading it.
    render(<StatusIndicator state="warn" label="Degraded" />)
    expect(screen.getByText('Degraded').className).not.toContain('animate-pulse')
  })
})
