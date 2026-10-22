import { afterEach, describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { DENSITIES, DENSITY_QUERY, isDensity, readDensity, useDensity } from './density'

// The density.
//
// The density is in the URL and nowhere else. The console holds no state of
// its own, so a density kept in the browser is a density that differs per
// browser, and the density of a screen is a property of the screen rather
// than of the machine somebody is reading it on.
//
// The URL is also a LINK: `?density=compact` can be pasted to a colleague
// and it survives a reload. And the change does not put an entry in the
// browser's history, because an operator who toggles the density three
// times and then presses Back expects to LEAVE the page and not to undo the
// third toggle.

const ORIGINAL_URL = '/'

function setSearch(search: string) {
  window.history.replaceState(null, '', `${ORIGINAL_URL}${search}`)
}

afterEach(() => {
  window.history.replaceState(null, '', ORIGINAL_URL)
})

describe('the density vocabulary', () => {
  it('is the two densities and no others', () => {
    // The control offers exactly what the reader accepts. A third density
    // in the list that `isDensity` refuses would be a control that offers
    // a choice the console cannot honour.
    expect(DENSITIES.every((d) => isDensity(d))).toBe(true)
    expect(DENSITIES).toContain('comfortable')
    expect(DENSITIES).toContain('compact')
  })

  it('names the URL parameter the density is read from', () => {
    // The parameter is the contract with whoever pastes a link.
    expect(DENSITY_QUERY).toBe('density')
  })

  it('recognises the two densities', () => {
    expect(isDensity('comfortable')).toBe(true)
    expect(isDensity('compact')).toBe(true)
  })

  it('refuses anything else', () => {
    expect(isDensity('cozy')).toBe(false)
    expect(isDensity('COMPACT')).toBe(false)
    expect(isDensity('')).toBe(false)
    expect(isDensity(null)).toBe(false)
  })
})

describe('readDensity', () => {
  it('reads the density out of the URL', () => {
    setSearch('?density=compact')
    expect(readDensity()).toBe('compact')
  })

  it('falls back to comfortable for a value it does not know', () => {
    // A hand-edited URL, or a link from an older console. An unknown
    // density is the comfortable one, not a broken screen.
    setSearch('?density=cozy')
    expect(readDensity()).toBe('comfortable')
  })

  it('falls back to comfortable when the URL says nothing about it', () => {
    setSearch('')
    expect(readDensity()).toBe('comfortable')
  })
})

describe('useDensity', () => {
  it('starts at the density the URL carries', () => {
    setSearch('?density=compact')
    const { result } = renderHook(() => useDensity())
    expect(result.current[0]).toBe('compact')
  })

  it('rewrites the URL when the density changes, without adding a history entry', () => {
    setSearch('')
    const before = window.history.length
    const { result } = renderHook(() => useDensity())

    act(() => {
      result.current[1]('compact')
    })

    expect(result.current[0]).toBe('compact')
    expect(window.location.search).toBe('?density=compact')
    // Replace, not push. An operator who toggles the density and then
    // presses Back expects to leave the page.
    expect(window.history.length).toBe(before)
  })

  it('deletes the parameter when the density goes back to comfortable', () => {
    setSearch('?density=compact')
    const { result } = renderHook(() => useDensity())

    act(() => {
      result.current[1]('comfortable')
    })

    expect(result.current[0]).toBe('comfortable')
    // The parameter is GONE, not set to `comfortable`. Comfortable is the
    // absence of a choice, and a URL that says `comfortable` is a URL that
    // says something the reader did not choose.
    expect(window.location.search).toBe('')
    expect(window.location.search).not.toContain(DENSITY_QUERY)
  })
})
