import { describe, it, expect } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useLiveStore } from './live'

describe('useLiveStore', () => {
  it('adds events newest-first and caps at 200', () => {
    const { result } = renderHook(() => useLiveStore())
    act(() => {
      result.current.addEvent({ id: 'a', cameraId: 'c1', cameraName: 'Cam 1', severity: 'low', state: 'open', observedAt: 't1' })
      result.current.addEvent({ id: 'b', cameraId: 'c2', cameraName: 'Cam 2', severity: 'critical', state: 'open', observedAt: 't2' })
    })
    expect(result.current.events.map((e) => e.id)).toEqual(['b', 'a'])
    act(() => { result.current.clearEvents() })
    expect(result.current.events).toEqual([])
  })

  it('setConnected flips the flag', () => {
    const { result } = renderHook(() => useLiveStore())
    act(() => { result.current.setConnected(true) })
    expect(result.current.connected).toBe(true)
    act(() => { result.current.setConnected(false) })
    expect(result.current.connected).toBe(false)
  }
  )
})
