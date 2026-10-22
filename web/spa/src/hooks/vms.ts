import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { get, post } from '../lib/api'

// The VMS's own view of its cameras.
//
// These are the VMS's facts, not the platform's: the frame is the picture the
// device is looking at, and the boxes are what its analytics engine asserted
// about that picture. The console draws them on the frame, so a box can be
// CHECKED against the thing it claims to describe -- which is the only way a
// detection is more than a number.
//
// The console reaches the VMS through the web server's `/vms` proxy, which
// keeps the device's credential on the server where it belongs.

export interface VMSDetection {
  class: string
  confidence: number
  x1: number
  y1: number
  x2: number
  y2: number
}

export interface VMSCamera {
  id: string
  name: string
  kind: string
  location: string
  fps: number
  frameCount: number
  measured: number
  latencyMs: number
  refusal?: string
  paused: boolean
  detections: VMSDetection[]
}

export interface VMSState {
  vendor: string
  now: string
  detector: {
    modelPath: string
    sha256: string
    bytes: number
    licence: string
    provider: string
    cudaDeviceID: number
    runtimeVersion: string
    classes: number
    classesDeclared: boolean
  } | null
}

export function useVMSCameras(refetchMs = 1000) {
  return useQuery({
    queryKey: ['vms-cameras'],
    queryFn: async () => {
      const res = await get('/vms/cameras')
      const data = (await res.json()) as { array: VMSCamera[] }
      return data.array
    },
    // A live view that updates once a minute is not a live view. The poll is
    // short enough to look live and long enough that the device is not asked
    // the same question fifty times a second.
    refetchInterval: refetchMs,
    retry: 1,
  })
}

export function useVMSState(refetchMs = 5000) {
  return useQuery({
    queryKey: ['vms-state'],
    queryFn: async () => {
      const res = await get('/vms/state')
      return (await res.json()) as VMSState
    },
    refetchInterval: refetchMs,
    retry: 1,
  })
}

// The frame URL for a camera.
//
// The cache-buster is the camera's frame count, so the browser fetches a NEW
// picture when the device has a new one and reuses the old one when it does not.
export function vmsFrameUrl(camera: VMSCamera): string {
  return `/vms/cameras/${encodeURIComponent(camera.id)}/frame.png?v=${camera.frameCount}`
}

// The control surface: play, pause, seek and the frame rate.
//
// These are the four things an operator does to a camera, and they are the
// DEVICE's to accept. The console does not keep a local "paused" flag and
// draw a picture that has stopped moving -- it asks the device to pause, and
// the device says whether it did.
//
// The console reaches the device through the web server's `/vms` proxy, which
// keeps the device's credential on the server.
export type VMSControl = 'play' | 'pause' | 'seek' | 'fps'

export function useVMSControl() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, control, value }: { id: string; control: VMSControl; value?: string }) => {
      const query = value === undefined ? '' : `?value=${encodeURIComponent(value)}`
      const res = await post(`/vms/cameras/${encodeURIComponent(id)}/${control}${query}`, {})
      return (await res.json()) as VMSCamera
    },
    onSuccess: () => {
      // The device's own state is the truth about whether it is playing, so
      // the camera list is refetched rather than patched in the browser.
      void client.invalidateQueries({ queryKey: ['vms-cameras'] })
    },
  })
}
