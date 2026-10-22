import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/server'
import { createHookWrapper } from '@/test/render'
import { queryClient as singleton } from './api'
import { useVMSCameras, useVMSControl, useVMSState, vmsFrameUrl } from './vms'

// The VMS's own view of its cameras.
//
// The frame is the picture the device is looking at, and the boxes are what
// its analytics engine asserted about that picture. The console draws them on
// the frame, so a box can be CHECKED against the thing it claims to describe
// -- which is the only way a detection is more than a number.
//
// Three things are worth a test here.
//
//   The console does not keep a local "paused" flag and draw a picture that
//   has stopped moving. It asks the device to pause, and the device says
//   whether it did. So a control that changed the console's own state and
//   not the device's would be a control that lies.
//
//   The frame URL carries the camera's frame count as a cache-buster, so the
//   browser fetches a NEW picture when the device has a new one and reuses
//   the old one when it does not.
//
//   The device's credential stays on the server: the console reaches the VMS
//   through the web server's `/vms` proxy.

const wrapper = createHookWrapper(singleton)

beforeEach(() => {
  singleton.clear()
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('useVMSCameras', () => {
  it('holds the cameras the device reported', async () => {
    const { result } = renderHook(() => useVMSCameras(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toHaveLength(1)
    expect(result.current.data?.[0]?.id).toBe('cam-1')
    expect(result.current.data?.[0]?.detections).toHaveLength(1)
  })

  it('holds nothing when the device said nothing', async () => {
    server.use(http.get('/vms/cameras', () => HttpResponse.json({ array: [] })))
    const { result } = renderHook(() => useVMSCameras(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toEqual([])
  })
})

describe('useVMSState', () => {
  it('holds the detector the device reported', async () => {
    const { result } = renderHook(() => useVMSState(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.vendor).toBe('demo')
    expect(result.current.data?.detector?.classesDeclared).toBe(true)
  })

  it('holds a device with no detector as no detector', async () => {
    // A device with no detector is a fact, and it is not the same as a
    // detector that failed to load.
    server.use(http.get('/vms/state', () => HttpResponse.json({ vendor: 'demo', now: '', detector: null })))
    const { result } = renderHook(() => useVMSState(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.detector).toBeNull()
  })
})

describe('vmsFrameUrl', () => {
  it('carries the frame count, so a new picture is fetched and an old one is not', () => {
    const url = vmsFrameUrl({
      id: 'cam-1',
      name: 'Loading bay',
      kind: 'dome',
      location: 'Loading bay',
      fps: 25,
      frameCount: 42,
      measured: 25,
      latencyMs: 120,
      paused: false,
      detections: [],
    })
    expect(url).toBe('/vms/cameras/cam-1/frame.png?v=42')
  })

  it('encodes the camera id', () => {
    const url = vmsFrameUrl({
      id: 'cam/1 2',
      name: 'x',
      kind: 'dome',
      location: 'x',
      fps: 25,
      frameCount: 1,
      measured: 25,
      latencyMs: 1,
      paused: false,
      detections: [],
    })
    expect(url).toBe('/vms/cameras/cam%2F1%202/frame.png?v=1')
  })
})

describe('useVMSControl', () => {
  it('asks the device to pause and holds what the device said', async () => {
    let path = ''
    let query = ''
    server.use(
      http.post('/vms/cameras/:id/:control', ({ request, params }) => {
        path = `/${String(params.id)}/${String(params.control)}`
        query = new URL(request.url).search
        return HttpResponse.json({
          id: 'cam-1',
          name: 'Loading bay',
          kind: 'dome',
          location: 'Loading bay',
          fps: 25,
          frameCount: 43,
          measured: 25,
          latencyMs: 120,
          paused: true,
          detections: [],
        })
      }),
    )
    const { result } = renderHook(() => useVMSControl(), { wrapper })
    const camera = await result.current.mutateAsync({ id: 'cam-1', control: 'pause' })
    expect(path).toBe('/cam-1/pause')
    expect(query).toBe('')
    // The DEVICE said it is paused. The console does not decide that.
    expect(camera.paused).toBe(true)
  })

  it('carries the value of a control that takes one', async () => {
    let query = ''
    server.use(
      http.post('/vms/cameras/:id/:control', ({ request }) => {
        query = new URL(request.url).search
        return HttpResponse.json({
          id: 'cam-1',
          name: 'x',
          kind: 'dome',
          location: 'x',
          fps: 10,
          frameCount: 1,
          measured: 10,
          latencyMs: 1,
          paused: false,
          detections: [],
        })
      }),
    )
    const { result } = renderHook(() => useVMSControl(), { wrapper })
    await result.current.mutateAsync({ id: 'cam-1', control: 'fps', value: '10' })
    expect(query).toBe('?value=10')
  })

  it('refetches the camera list, because the device is the truth about its own state', async () => {
    const invalidate = vi.spyOn(singleton, 'invalidateQueries')
    const { result } = renderHook(() => useVMSControl(), { wrapper })
    await result.current.mutateAsync({ id: 'cam-1', control: 'play' })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['vms-cameras'] })
  })
})
