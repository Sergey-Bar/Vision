import {
  useQuery,
  useMutation,
  useQueryClient,
  QueryClient,
} from '@tanstack/react-query'
import { post, get, ApiError } from '../lib/api'
import type { SearchResponse, ChatResponse, ChatParams, Clip, CriticalStream, Camera } from '../api'

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
      const res = await post('/api/search/free', { q })
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
      const res = await get('/api/cameras')
      const data = (await res.json()) as { cameras: Camera[] }
      return data.cameras
    },
  })
}
