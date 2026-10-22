import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, renderHook } from '@testing-library/react'
import { isPaletteShortcut, isSearchShortcut, isTypingTarget, useChord, useHotkey } from './keys'

// The console is a keyboard instrument.
//
// Two rules make the rest of it work, and both of them are about a key that
// belongs to somebody else. A key pressed while the reader is TYPING belongs
// to the field, and a shortcut never fires while a modifier other than the
// one it names is held.
//
// The tests below are about the shortcuts not firing when they should not.
// A shortcut that fires one time too many takes a letter out of a search box
// and puts a page in front of an operator who was halfway through typing.

describe('isTypingTarget', () => {
  it('is true for the fields a reader types into', () => {
    expect(isTypingTarget(document.createElement('input'))).toBe(true)
    expect(isTypingTarget(document.createElement('textarea'))).toBe(true)
    expect(isTypingTarget(document.createElement('select'))).toBe(true)
  })

  it('is true for an element the reader is editing in place', () => {
    // jsdom does not implement contenteditable, so the flag the browser
    // would set is set here: the hook reads `isContentEditable`, and the
    // hook is what is under test.
    const editable = document.createElement('div')
    Object.defineProperty(editable, 'isContentEditable', { value: true })
    expect(isTypingTarget(editable)).toBe(true)
  })

  it('is false for an ordinary element', () => {
    expect(isTypingTarget(document.createElement('div'))).toBe(false)
  })

  it('is false for nothing at all', () => {
    expect(isTypingTarget(null)).toBe(false)
  })

  it('is false for a target that is not an element', () => {
    expect(isTypingTarget({} as unknown as EventTarget)).toBe(false)
  })
})

describe('isPaletteShortcut', () => {
  it('is the command key on a Mac', () => {
    expect(isPaletteShortcut(new KeyboardEvent('keydown', { key: 'k', metaKey: true }))).toBe(true)
  })

  it('is the control key on a PC, in either case', () => {
    expect(isPaletteShortcut(new KeyboardEvent('keydown', { key: 'k', ctrlKey: true }))).toBe(true)
    expect(isPaletteShortcut(new KeyboardEvent('keydown', { key: 'K', ctrlKey: true }))).toBe(true)
  })

  it('is not the shortcut while the alt key is held', () => {
    // Alt-K is a browser and OS shortcut. The console does not take it.
    expect(isPaletteShortcut(new KeyboardEvent('keydown', { key: 'k', metaKey: true, altKey: true }))).toBe(false)
    expect(isPaletteShortcut(new KeyboardEvent('keydown', { key: 'k', altKey: true }))).toBe(false)
  })

  it('is not the shortcut without a modifier', () => {
    expect(isPaletteShortcut(new KeyboardEvent('keydown', { key: 'k' }))).toBe(false)
  })

  it('is not the shortcut on any other key', () => {
    expect(isPaletteShortcut(new KeyboardEvent('keydown', { key: 'x', metaKey: true }))).toBe(false)
  })
})

describe('isSearchShortcut', () => {
  it('is the slash on its own', () => {
    expect(isSearchShortcut(new KeyboardEvent('keydown', { key: '/' }))).toBe(true)
  })

  it('is not the slash with a modifier held', () => {
    expect(isSearchShortcut(new KeyboardEvent('keydown', { key: '/', ctrlKey: true }))).toBe(false)
    expect(isSearchShortcut(new KeyboardEvent('keydown', { key: '/', metaKey: true }))).toBe(false)
    expect(isSearchShortcut(new KeyboardEvent('keydown', { key: '/', altKey: true }))).toBe(false)
  })

  it('is not any other key', () => {
    expect(isSearchShortcut(new KeyboardEvent('keydown', { key: 'k' }))).toBe(false)
    expect(isSearchShortcut(new KeyboardEvent('keydown', { key: '?' }))).toBe(false)
  })
})

describe('useChord', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  it('fires the handler with the lowercased second key', () => {
    const handler = vi.fn()
    renderHook(() => useChord('g', handler))

    fireEvent.keyDown(document, { key: 'g' })
    fireEvent.keyDown(document, { key: 'O' })

    // The handler is told `o` and not `O`: the chord is a sequence of
    // letters, and a capital O is the same letter.
    expect(handler).toHaveBeenCalledTimes(1)
    expect(handler).toHaveBeenCalledWith('o')
  })

  it('does not fire for a key typed into a field', () => {
    // Rule 1 of the file's contract. A chord that steals the letter "g" from
    // a search box breaks the search box, and the search box is what the
    // operator came for.
    const handler = vi.fn()
    renderHook(() => useChord('g', handler))
    const input = document.createElement('input')
    document.body.appendChild(input)

    fireEvent.keyDown(input, { key: 'g' })
    fireEvent.keyDown(input, { key: 'o' })

    expect(handler).not.toHaveBeenCalled()
    input.remove()
  })

  it('does not fire for the second key when the sequence was cancelled', () => {
    const handler = vi.fn()
    renderHook(() => useChord('g', handler))

    fireEvent.keyDown(document, { key: 'g' })
    fireEvent.keyDown(document, { key: 'Escape' })
    fireEvent.keyDown(document, { key: 'o' })

    // Escape throws the pending prefix away. Without the cancel this WOULD
    // have fired, and the reader who pressed Escape would still get a
    // navigation they did not ask for.
    expect(handler).not.toHaveBeenCalled()
  })

  it('forgets the prefix when the window has passed', () => {
    // A chord that waits for ever is a chord that eats the next keypress the
    // reader makes. The window is bounded, and this is the bound.
    vi.useFakeTimers()
    const handler = vi.fn()
    renderHook(() => useChord('g', handler))

    fireEvent.keyDown(document, { key: 'g' })
    act(() => {
      vi.advanceTimersByTime(1501)
    })
    fireEvent.keyDown(document, { key: 'o' })

    expect(handler).not.toHaveBeenCalled()
  })
})

describe('useHotkey', () => {
  it('fires the handler on a matching key', () => {
    const handler = vi.fn()
    renderHook(() => useHotkey((e) => e.key === 'j', handler))

    fireEvent.keyDown(document, { key: 'j' })

    expect(handler).toHaveBeenCalledTimes(1)
  })

  it('does not fire while the reader is typing in a field', () => {
    const handler = vi.fn()
    renderHook(() => useHotkey((e) => e.key === 'j', handler))
    const input = document.createElement('input')
    document.body.appendChild(input)

    fireEvent.keyDown(input, { key: 'j' })

    expect(handler).not.toHaveBeenCalled()
    input.remove()
  })

  it('does not fire when the key does not match', () => {
    const handler = vi.fn()
    renderHook(() => useHotkey((e) => e.key === 'j', handler))

    fireEvent.keyDown(document, { key: 'k' })

    expect(handler).not.toHaveBeenCalled()
  })

  it('consumes the shortcut it fires on', () => {
    // The file's contract: the shortcut is consumed. A shortcut that also
    // did the browser's own thing -- scrolling on the space bar, printing
    // on ⌘P -- would be two commands on one keypress.
    const handler = vi.fn()
    renderHook(() => useHotkey((e) => e.key === 'j', handler))

    const event = new KeyboardEvent('keydown', { key: 'j', cancelable: true, bubbles: true })
    const preventDefault = vi.spyOn(event, 'preventDefault')
    document.dispatchEvent(event)

    expect(handler).toHaveBeenCalledTimes(1)
    expect(preventDefault).toHaveBeenCalled()
  })
})
