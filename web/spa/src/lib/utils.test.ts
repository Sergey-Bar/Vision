import { describe, it, expect } from 'vitest'
import { cn } from './utils'

// The class name helper.
//
// `clsx` decides WHICH classes are there and `tailwind-merge` decides which
// of them WIN. Both halves matter: a conflicting Tailwind class that was
// not merged would put two paddings on one element, and the one that wins
// would be decided by the order of the class attribute rather than by the
// order the caller wrote them in.

describe('cn', () => {
  it('merges classes and dedupes conflicting tailwind classes', () => {
    expect(cn('px-2', 'px-4')).toBe('px-4')
    expect(cn('text-red', 'text-blue')).toBe('text-blue')
    expect(cn(false && 'hidden', 'flex')).toBe('flex')
    expect(cn(null, undefined, 0, '')).toBe('')
    expect(cn('a', 'b')).toBe('a b')
  })

  it('lets the last of two conflicting tailwind classes win', () => {
    // The element must carry one padding, and it must be the one the caller
    // wrote last. Two paddings on one element is a layout nobody can reason
    // about.
    expect(cn('p-2', 'p-4')).toBe('p-4')
    expect(cn('p-4', 'p-2')).toBe('p-2')
  })

  it('is the empty string when there is nothing to join', () => {
    expect(cn()).toBe('')
  })

  it('drops the falsy values and keeps the rest', () => {
    // A conditional class is written as a condition, and the condition is
    // false half the time. `false` in a class attribute is a class
    // attribute that says nothing.
    expect(cn(false, undefined, null, 0, '', 'flex')).toBe('flex')
  })

  it('leaves a class that is not a tailwind one alone', () => {
    // The console's own class names are not Tailwind's, and `tailwind-merge`
    // must not invent an opinion about them.
    expect(cn('app-shell')).toBe('app-shell')
    expect(cn('app-shell', 'p-4')).toBe('app-shell p-4')
  })
})
