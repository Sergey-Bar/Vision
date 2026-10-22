import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, render, renderHook } from '@testing-library/react'
import { createElement } from 'react'
import type { RefObject } from 'react'
import { duration, useFlip, usePresence, useReducedMotion, useRouteTransition } from './motion'

// The motion helpers.
//
// Four durations, one curve, and no animation library. The rules the helpers
// enforce are that only `transform` and `opacity` animate, that the duration
// comes from the token file rather than from a number typed into a component,
// and that `prefers-reduced-motion` collapses all of it.
//
// The FLIP hook is the one that has been wrong before. It used to create a
// ref that nobody filled, read it as null on every run, and return before it
// did anything -- and a FLIP that never fires looks exactly like a queue that
// does not reorder. So the tests here are about the ref being USABLE.

// A media query the reader controls.
//
// jsdom does not implement `matchMedia` at all, and the hook has to survive
// that: no matchMedia means "the reader has not asked for less motion".
function stubMatchMedia(matches: boolean) {
  const addEventListener = vi.fn()
  const removeEventListener = vi.fn()
  const mql = {
    matches,
    media: '(prefers-reduced-motion: reduce)',
    onchange: null,
    addEventListener,
    removeEventListener,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    dispatchEvent: vi.fn(),
  } as unknown as MediaQueryList
  const matchMedia = vi.fn(() => mql)
  vi.stubGlobal('matchMedia', matchMedia)
  return { addEventListener, removeEventListener }
}

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('useReducedMotion', () => {
  it('is false when the browser has not been asked for less motion', () => {
    const { result } = renderHook(() => useReducedMotion())
    expect(result.current).toBe(false)
  })

  it('is true when the reader has asked for less motion', () => {
    stubMatchMedia(true)
    const { result } = renderHook(() => useReducedMotion())
    expect(result.current).toBe(true)
  })

  it('lets the listener go when the page goes away', () => {
    // A listener left behind on a media query outlives the component that
    // made it, and a page that cannot be unmounted is a page that leaks a
    // state update per media change.
    const { addEventListener, removeEventListener } = stubMatchMedia(false)
    const { unmount } = renderHook(() => useReducedMotion())

    expect(addEventListener).toHaveBeenCalledWith('change', expect.any(Function))
    unmount()
    expect(removeEventListener).toHaveBeenCalledWith('change', expect.any(Function))
  })
})

describe('duration', () => {
  it('reads the number out of the token', () => {
    // The token file writes `320ms`, and the number is the millisecond
    // count. The duration comes from ONE place, so a component cannot
    // animate for a length the rest of the product does not use.
    vi.spyOn(window, 'getComputedStyle').mockImplementation(
      () =>
        ({
          getPropertyValue: (name: string) => (name === '--dur-3' ? '320ms' : ''),
        }) as unknown as CSSStyleDeclaration,
    )
    expect(duration(3)).toBe(320)
  })

  it('falls back to 200 when the token is not there', () => {
    // The stylesheet has not loaded, or the property is not there. A NaN
    // duration in a `transition` is a transition that does not happen.
    expect(duration(2)).toBe(200)
  })

  it('falls back to 200 when the token cannot be read as a number', () => {
    vi.spyOn(window, 'getComputedStyle').mockImplementation(
      () =>
        ({
          getPropertyValue: () => 'soon',
        }) as unknown as CSSStyleDeclaration,
    )
    expect(duration(2)).toBe(200)
  })
})

describe('usePresence', () => {
  it('mounts and shows when it is asked to be visible', () => {
    vi.useFakeTimers()
    const { result } = renderHook(() => usePresence(true))

    act(() => {
      vi.advanceTimersByTime(1)
    })

    expect(result.current.mounted).toBe(true)
    expect(result.current.shown).toBe(true)
  })

  it('keeps the element mounted for the length of the fade, and then unmounts it', () => {
    // The fade takes the token's duration -- 200ms for `--dur-2`, which is
    // also the fallback. The element is still there during it, or the fade
    // would be a pop.
    vi.useFakeTimers()
    const { result, rerender } = renderHook(
      ({ visible }: { visible: boolean }) => usePresence(visible),
      { initialProps: { visible: true } },
    )
    act(() => {
      vi.advanceTimersByTime(1)
    })
    expect(result.current.shown).toBe(true)

    rerender({ visible: false })

    expect(result.current.shown).toBe(false)
    expect(result.current.mounted).toBe(true)

    act(() => {
      vi.advanceTimersByTime(250)
    })

    expect(result.current.mounted).toBe(false)
  })

  it('unmounts at once when the reader has asked for less motion', () => {
    // There is no fade to wait for, so there is nothing to keep mounted.
    stubMatchMedia(true)
    vi.useFakeTimers()
    const { result, rerender } = renderHook(
      ({ visible }: { visible: boolean }) => usePresence(visible),
      { initialProps: { visible: true } },
    )

    rerender({ visible: false })

    expect(result.current.shown).toBe(false)
    expect(result.current.mounted).toBe(false)
  })
})

describe('useFlip', () => {
  // A queue that re-ranks, the way the operations page draws it.
  function FlipProbe({ dep }: { dep: string }) {
    const rows = useFlip<HTMLDivElement>([dep])
    return createElement(
      'div',
      { ref: rows, 'data-testid': 'flip-root' },
      createElement('div', { 'data-flip-id': 'one' }, 'one'),
      createElement('div', { 'data-flip-id': 'two' }, 'two'),
    )
  }

  it('hands back a ref the caller can fill', () => {
    // The ref is the whole of what the hook was missing. It must be an
    // object the caller can attach an element to, and not nothing.
    const { result } = renderHook(() => useFlip<HTMLDivElement>(['a']))
    expect(result.current).not.toBeNull()
    expect(result.current).toHaveProperty('current')
  })

  it('measures and plays once the ref is filled and the list changes', () => {
    // The effect reads the ref and gives up when it is empty. Filling it is
    // the caller's job, and the frame is only asked for afterwards.
    const raf = vi.spyOn(window, 'requestAnimationFrame').mockImplementation(() => 0)
    const { result, rerender } = renderHook(
      ({ dep }: { dep: string }) => useFlip<HTMLDivElement>([dep]),
      { initialProps: { dep: 'a' } },
    )

    const root = document.createElement('div')
    const row = document.createElement('div')
    row.setAttribute('data-flip-id', 'one')
    root.appendChild(row)
    result.current.current = root

    raf.mockClear()
    rerender({ dep: 'b' })

    expect(raf).toHaveBeenCalled()
  })

  it('plays the rows when the caller attaches the ref to a real element', () => {
    // The regression the file's own comment describes: the ref was created
    // here and filled by nobody, so the effect returned before it did
    // anything, and a FLIP that never fires looks like a queue that does
    // not reorder.
    const raf = vi.spyOn(window, 'requestAnimationFrame').mockImplementation(() => 0)
    const { rerender } = render(createElement(FlipProbe, { dep: 'a' }))

    raf.mockClear()
    rerender(createElement(FlipProbe, { dep: 'b' }))

    expect(raf).toHaveBeenCalled()
  })
})

describe('useRouteTransition', () => {
  it('fades the new screen in', () => {
    vi.useFakeTimers()
    const node = document.createElement('div')
    const ref: RefObject<HTMLElement | null> = { current: node }

    renderHook(() => useRouteTransition(ref))

    // The screen starts hidden, so the fade is a fade and not a jump.
    expect(node.style.opacity).toBe('0')

    act(() => {
      vi.advanceTimersByTime(1)
    })

    expect(node.style.opacity).toBe('1')
    expect(node.style.transform).toBe('')
  })

  it('does not move the screen when the reader has asked for less motion', () => {
    stubMatchMedia(true)
    vi.useFakeTimers()
    const node = document.createElement('div')
    const ref: RefObject<HTMLElement | null> = { current: node }

    renderHook(() => useRouteTransition(ref))

    expect(node.style.opacity).toBe('')
    expect(node.style.transform).toBe('')
  })
})
