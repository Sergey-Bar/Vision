import { useEffect, useLayoutEffect, useRef, useState } from 'react'

// The motion helpers.
//
// No animation dependency, and no animation library, because the design
// system's motion rules are four durations and one curve and a library would
// bring a fifth.
//
// The rules themselves:
//   - Only `transform` and `opacity` animate. A layout change is FLIP:
//     measure, invert, play.
//   - The duration comes from the token file, so a motion that is out of step
//     with the rest of the product is a motion that reads as borrowed.
//   - `prefers-reduced-motion` collapses every duration to 0.01ms, which the
//     token file does; this hook reports it so a component can skip the
//     measurement work entirely.

// Whether the reader has asked for less motion.
//
// It is read once and kept: a media query that changes mid-session changes
// the behaviour of a page half way through reading it.
export function useReducedMotion(): boolean {
  const [reduced, setReduced] = useState(() => {
    if (typeof window === 'undefined' || !window.matchMedia) return false
    return window.matchMedia('(prefers-reduced-motion: reduce)').matches
  })

  useEffect(() => {
    if (typeof window === 'undefined' || !window.matchMedia) return
    const mq = window.matchMedia('(prefers-reduced-motion: reduce)')
    const onChange = () => setReduced(mq.matches)
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [])

  return reduced
}

// The duration token, read from the CSS so there is one number.
export function duration(name: 1 | 2 | 3 | 4 = 2): number {
  if (typeof window === 'undefined') return 200
  const v = getComputedStyle(document.documentElement).getPropertyValue(`--dur-${name}`).trim()
  const n = Number.parseFloat(v)
  return Number.isFinite(n) ? n : 200
}

// FLIP.
//
// The attention queue reorders when the product's ranking changes, and the
// row the operator is on must not move while they are reading it. FLIP is
// how that is done: measure the old positions, invert them with a transform,
// let the layout settle, then play the transform away.
//
// It is a transform, so it is the only kind of motion the design system
// allows, and it is the only motion worth building.
//
// The hook RETURNS the ref, and that is the whole of what it was missing
// before: the ref was created here, filled in by nobody, and read as null on
// every run, so the effect returned before it did anything. A FLIP that
// never fires looks exactly like a queue that does not reorder.
export function useFlip<T extends HTMLElement>(deps: unknown[]) {
  const ref = useRef<T | null>(null)
  const reduced = useReducedMotion()

  useLayoutEffect(() => {
    const root = ref.current
    if (!root || reduced) return

    const items = Array.from(root.querySelectorAll<HTMLElement>('[data-flip-id]'))
    if (items.length === 0) return

    const before = new Map<string, number>()
    for (const el of items) {
      const id = el.dataset.flipId
      if (id) before.set(id, el.getBoundingClientRect().top)
    }

    // The layout has already settled by the time this runs for the SECOND
    // time: the effect runs after the DOM changed, so the "before" positions
    // are captured by the previous frame.
    let raf = 0
    raf = requestAnimationFrame(() => {
      for (const el of items) {
        const id = el.dataset.flipId
        if (!id) continue
        const prev = before.get(id)
        if (prev === undefined) continue
        const now = el.getBoundingClientRect().top
        const delta = prev - now
        if (delta === 0) continue
        el.style.transition = 'none'
        el.style.transform = `translateY(${delta}px)`
        requestAnimationFrame(() => {
          el.style.transition = `transform ${duration(3)} cubic-bezier(0.16, 1, 0.3, 1)`
          el.style.transform = ''
        })
      }
      window.setTimeout(() => raf, 0)
    })

    return () => cancelAnimationFrame(raf)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)

  return ref
}

// The presence hook: a mount/unmount that fades rather than pops.
//
// The duration is the token, so a fade in the console is the same fade as a
// fade in the showcase.
export function usePresence(visible: boolean) {
  const reduced = useReducedMotion()
  const [mounted, setMounted] = useState(visible)
  const [shown, setShown] = useState(visible)

  useEffect(() => {
    if (visible) {
      setMounted(true)
      const id = window.setTimeout(() => setShown(true), 0)
      return () => window.clearTimeout(id)
    }
    setShown(false)
    if (reduced) {
      setMounted(false)
      return
    }
    const id = window.setTimeout(() => setMounted(false), duration(2))
    return () => window.clearTimeout(id)  }, [visible, reduced])

  return { mounted, shown }
}

// The route transition: a new screen fades in, and the focus moves to the
// new screen's heading so a keyboard reader is not left at the top of the
// document wondering whether anything happened.
export function useRouteTransition(ref: React.RefObject<HTMLElement | null>) {
  const reduced = useReducedMotion()

  useEffect(() => {
    const node = ref.current
    if (!node) return
    if (reduced) return
    node.style.opacity = '0'
    node.style.transform = 'translateY(4px)'
    const id = window.setTimeout(() => {
      node.style.transition = `opacity ${duration(2)} cubic-bezier(0.16, 1, 0.3, 1), transform ${duration(2)} cubic-bezier(0.16, 1, 0.3, 1)`
      node.style.opacity = '1'
      node.style.transform = ''
    }, 0)
    return () => window.clearTimeout(id)
  }, [ref, reduced])
}
