import { create } from 'zustand'

export interface LiveEvent {
  id: string
  cameraId: string
  cameraName: string
  severity: 'low' | 'medium' | 'high' | 'critical'
  state: string
  observedAt: string
  thumbnail?: string
}

interface LiveStore {
  events: LiveEvent[]
  connected: boolean
  addEvent: (event: LiveEvent) => void
  setConnected: (connected: boolean) => void
  clearEvents: () => void
}

export const useLiveStore = create<LiveStore>((set) => ({
  events: [],
  connected: false,
  addEvent: (event) =>
    set((state) => ({
      events: [event, ...state.events].slice(0, 200),
    })),
  setConnected: (connected) => set({ connected }),
  clearEvents: () => set({ events: [] }),
}))
