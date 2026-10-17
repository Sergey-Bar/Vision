import { describe, it, expect } from 'vitest'
import { cn } from './utils'

describe('cn', () => {
  it('merges classes and dedupes conflicting tailwind classes', () => {
    expect(cn('px-2', 'px-4')).toBe('px-4')
    expect(cn('text-red', 'text-blue')).toBe('text-blue')
    expect(cn(false && 'hidden', 'flex')).toBe('flex')
    expect(cn(null, undefined, 0, '')).toBe('')
    expect(cn('a', 'b')).toBe('a b')
  })
})
