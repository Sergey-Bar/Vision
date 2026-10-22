import { useEffect, useRef } from 'react'

// The console is a KEYBOARD INSTRUMENT.
//
// The keys are here, in one file, because a keyboard shortcut scattered across
// six components is six shortcuts that disagree with each other. The contract
// is:
//
//   ⌘K / Ctrl+K   the command palette
//   /             the search
//   j / k         move down and up the attention queue
//   Enter         open what is selected
//   Esc           close what is open
//   g then o/i/c  go to a section
//
// Two rules make the rest work:
//
//   1. A key pressed while the reader is TYPING belongs to the field. A
//      shortcut that steals the letter "j" from a search box is a shortcut
//      that breaks the search box, and the search box is the thing the
//      operator came for.
//   2. A shortcut never fires while a modifier other than the one it names is
//      held. ⌘K is the palette; Ctrl-K in a browser is "focus the search
//      bar", and the console must not steal it.

/** isTypingTarget reports whether the event came from a field being typed into. */
export function isTypingTarget(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el || typeof el.tagName !== 'string') return false
  const tag = el.tagName.toLowerCase()
  if (tag === 'input' || tag === 'textarea' || tag === 'select') return true
  return el.isContentEditable === true
}

/** isPaletteShortcut reports whether the key event asks for the command palette. */
export function isPaletteShortcut(e: KeyboardEvent): boolean {
  if (!e.metaKey && !e.ctrlKey) return false
  if (e.altKey) return false
  return e.key === 'k' || e.key === 'K'
}

/** isSearchShortcut reports whether the key event asks for the search. */
export function isSearchShortcut(e: KeyboardEvent): boolean {
  if (e.metaKey || e.ctrlKey || e.altKey) return false
  return e.key === '/'
}

// A chord is two keys in a row: `g` and then one more.
//
// The window is bounded, because a chord that waits for ever is a chord that
// eats the next keypress the reader makes. A second and a half is long enough
// for a deliberate sequence and short enough that a reader who typed "g" and
// changed their mind has not lost a letter.
const CHORD_WINDOW_MS = 1500

/**
 * useChord runs a two-key sequence.
 *
 * The handler is held in a ref rather than listed as a dependency, so a
 * component that passes a new closure every render does not unsubscribe and
 * resubscribe the listener on every render -- which would drop a keystroke
 * that arrived between the two.
 */
export function useChord(prefix: string, handler: (key: string) => void, enabled = true): void {
  const pending = useRef(false)
  const timer = useRef<number | undefined>(undefined)
  const latest = useRef(handler)
  latest.current = handler

  useEffect(() => {
    if (!enabled) return
    const onKey = (e: KeyboardEvent) => {
      if (isTypingTarget(e.target)) return
      if (e.key === 'Escape') {
        pending.current = false
        window.clearTimeout(timer.current)
        return
      }
      if (pending.current) {
        pending.current = false
        window.clearTimeout(timer.current)
        latest.current(e.key.toLowerCase())
        return
      }
      if (e.key.toLowerCase() !== prefix) return
      pending.current = true
      timer.current = window.setTimeout(() => {
        pending.current = false
      }, CHORD_WINDOW_MS)
    }
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('keydown', onKey)
      window.clearTimeout(timer.current)
    }
  }, [prefix, enabled])
}

/**
 * useHotkey runs a single-key shortcut.
 *
 * It is the same contract as the chord, minus the second key, and it exists
 * so that "open the palette" is one line rather than a document listener
 * written again in the top bar and in the page.
 */
export function useHotkey(
  matches: (e: KeyboardEvent) => boolean,
  handler: () => void,
  enabled = true,
): void {
  const latest = useRef(handler)
  latest.current = handler

  useEffect(() => {
    if (!enabled) return
    const onKey = (e: KeyboardEvent) => {
      if (isTypingTarget(e.target)) return
      if (!matches(e)) return
      e.preventDefault()
      latest.current()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [matches, enabled])
}
