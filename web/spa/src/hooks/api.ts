import {
  useQuery,
  useMutation,
  useQueryClient,
  QueryClient,
} from '@tanstack/react-query'
import { post, get, upload, ApiError } from '../lib/api'
import type {
  SearchResponse,
  ChatResponse,
  ChatParams,
  Clip,
  CriticalStream,
  Camera,
  VideoUploadResponse,
  VideoYouTubeRequest,
  VideoYouTubeResponse,
  RulesFromTextRequest,
  RulesFromTextResponse,
  AIRuntimeStatus,
} from '../api'

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      gcTime: 5 * 60_000,
      retry: (failureCount, error) => {
        if (error instanceof ApiError && error.status >= 400 && error.status < 500) {
          return false
        }
        return failureCount < 2
      },
    },
  },
})

// ---------- Search ----------

export function useSearch(q: string, enabled = true) {
  return useQuery({
    queryKey: ['search', q],
    queryFn: async () => {
      const url = new URL(`/v1/evidence`, window.location.origin)
      if (q) url.searchParams.set('q', q)
      const res = await get(url.pathname + url.search)
      const data = (await res.json()) as SearchResponse
      return data
    },
    enabled: enabled && q.length > 0,
  })
}

// ---------- Chat ----------

export function useChat() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (params: ChatParams) => {
      const res = await post('/api/chat', { question: params.question, videoId: params.videoId })
      return (await res.json()) as ChatResponse
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['search'] })
    },
  })
}

// ---------- Clips ----------

export function useClips() {
  return useQuery({
    queryKey: ['clips'],
    queryFn: async () => {
      const res = await get('/api/clips')
      const data = (await res.json()) as { clips: Clip[] }
      return data.clips
    },
  })
}

// ---------- Critical ----------

export function useCritical(limit = 5) {
  return useQuery({
    queryKey: ['critical', limit],
    queryFn: async () => {
      const res = await get(`/api/critical?limit=${limit}`)
      const data = (await res.json()) as { streams: CriticalStream[] }
      return data.streams
    },
  })
}

// ---------- Cameras ----------

export function useCameras() {
  return useQuery({
    queryKey: ['cameras'],
    queryFn: async () => {
      try {
        const res = await get('/api/cameras')
        const data = (await res.json()) as { cameras: Camera[] }
        return data.cameras
      } catch {
        // Fall back to demo cameras when the API endpoint is unavailable
        return DEMO_CAMERAS
      }
    },
  })
}

const DEMO_CAMERAS: Camera[] = [
  { id: 'cam-1', name: 'Entrance North', siteId: 'North Campus', status: 'online', rtsp: undefined },
  { id: 'cam-2', name: 'Entrance South', siteId: 'North Campus', status: 'online', rtsp: undefined },
  { id: 'cam-3', name: 'Aisle 4', siteId: 'West Warehouse', status: 'online', rtsp: undefined },
  { id: 'cam-4', name: 'Aisle 7', siteId: 'West Warehouse', status: 'degraded', rtsp: undefined },
  { id: 'cam-5', name: 'Loading Dock', siteId: 'Downtown Hub', status: 'offline', rtsp: undefined },
  { id: 'cam-6', name: 'Parking A', siteId: 'Downtown Hub', status: 'online', rtsp: undefined },
  { id: 'cam-7', name: 'Parking B', siteId: 'Downtown Hub', status: 'online', rtsp: undefined },
  { id: 'cam-8', name: 'Back Office', siteId: 'North Campus', status: 'offline', rtsp: undefined },
]

// ---------- Video Upload ----------

export function useVideoUpload() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (formData: FormData) => {
      const res = await upload('/v1/video/upload', formData)
      return (await res.json()) as VideoUploadResponse
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['clips'] })
      void client.invalidateQueries({ queryKey: ['cameras'] })
    },
  })
}

// ---------- YouTube Ingest ----------

export function useVideoYouTube() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (params: VideoYouTubeRequest) => {
      const res = await post('/v1/video/youtube', params)
      return (await res.json()) as VideoYouTubeResponse
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['clips'] })
      void client.invalidateQueries({ queryKey: ['cameras'] })
    },
  })
}

// ---------- Rules from Text ----------

export function useRulesFromText() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (params: RulesFromTextRequest) => {
      const res = await post('/v1/rules/from-text', params)
      return (await res.json()) as RulesFromTextResponse
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['rules'] })
    },
  })
}

// ---------- AI Runtime ----------

export function useAIRuntime() {
  return useQuery({
    queryKey: ['ai-runtime'],
    queryFn: async () => {
      const res = await get('/v1/health/ready')
      const data = (await res.json()) as { checks: Record<string, { ok: boolean; detail?: string }> }
      const ai = data.checks?.aiRuntime
      return {
        available: ai?.ok ?? false,
        reason: ai?.detail ?? null,
        model: null,
      } as AIRuntimeStatus
    },
    refetchInterval: 10_000,
  })
}
