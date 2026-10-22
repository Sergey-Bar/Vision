import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/server'
import { createHookWrapper } from '@/test/render'
import {
  queryClient as singleton,
  useAIRuntime,
  useCameras,
  useChat,
  useClips,
  useRulesFromText,
  useSearch,
  useVideoUpload,
  useVideoYouTube,
} from './api'

// The console's own hooks.
//
// Nine of them, and they are the console's only way of holding anything. The
// rule they all obey is the rule the whole console obeys: a value on screen
// is a value the product returned, and an answer that is not there is said to
// be not there.
//
// Two of the nine carry the product:
//
//   `useCameras`    has NO fallback list. It used to answer with eight
//                   invented cameras at three invented sites when the endpoint
//                   was unreachable, and an invented camera list is the worst
//                   kind of answer: it looks exactly like a real one.
//
//   `useAIRuntime`  says `available: false` when the readiness probe does not
//                   carry an `aiRuntime` check, because a missing AI
//                   observation is NEVER a negative observation.
//
// The tests below run against the APPLICATION's own `QueryClient` -- the
// module-level singleton -- because it carries the retry predicate and that
// predicate is part of what is being tested: a 4xx is the product's answer
// and is not asked again, and a 5xx is the product's failure and is.

const wrapper = createHookWrapper(singleton)

beforeEach(() => {
  singleton.clear()
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('useSearch', () => {
  it('asks nothing while the query is empty', async () => {
    let calls = 0
    server.use(
      http.get('/v1/evidence', () => {
        calls++
        return HttpResponse.json({ hits: [] })
      }),
    )
    const { result } = renderHook(() => useSearch(''), { wrapper })
    await waitFor(() => expect(result.current.fetchStatus).toBe('idle'))
    expect(calls).toBe(0)
  })

  it('encodes the query into the request', async () => {
    let seen: string | null = null
    server.use(
      http.get('/v1/evidence', ({ request }) => {
        seen = new URL(request.url).searchParams.get('q')
        return HttpResponse.json({ hits: [] })
      }),
    )
    const { result } = renderHook(() => useSearch('a b&c=d'), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(seen).toBe('a b&c=d')
  })

  it('does not ask again after a refusal', async () => {
    // A 4xx is the product's ANSWER. Asking it again would be asking a
    // question the product has already answered, and the retry predicate is
    // what stops that.
    let calls = 0
    server.use(
      http.get('/v1/evidence', () => {
        calls++
        return HttpResponse.json({ error: 'no_active_bundle' }, { status: 409 })
      }),
    )
    const { result } = renderHook(() => useSearch('anything'), { wrapper })
    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(calls).toBe(1)
  })
})

describe('useClips and useCameras', () => {
  it('returns the clips the product sent', async () => {
    const { result } = renderHook(() => useClips(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toHaveLength(1)
    expect(result.current.data?.[0]?.evidenceId).toBe('ev-1')
  })

  it('returns the cameras the product sent', async () => {
    const { result } = renderHook(() => useCameras(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toHaveLength(1)
    expect(result.current.data?.[0]?.id).toBe('cam-1')
  })

  it('answers an empty inventory with an empty list, never a fixture', async () => {
    // The response has NO `cameras` key at all, so the hook's own `?? []` is
    // what answers. If a fallback list were added to this hook -- the defect
    // the hook exists to prevent -- this is the test that would catch it,
    // because a fallback would show up HERE and nowhere else.
    server.use(http.get('/api/cameras', () => HttpResponse.json({})))
    const { result } = renderHook(() => useCameras(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toEqual([])
  })

  it('answers an unreachable inventory with nothing at all', async () => {
    // Not an empty list: NO list. An unreachable endpoint says the inventory
    // is UNKNOWN, and an empty list says the inventory is EMPTY. Those are
    // different facts, and the console must not confuse them.
    //
    // A FRESH client is used here rather than the application's, so the
    // unreachable answer is not retried: the point of the test is what the
    // hook holds, and a retry changes nothing about that.
    server.use(
      http.get('/api/cameras', () => HttpResponse.json({ error: 'gone' }, { status: 503 })),
    )
    const { result } = renderHook(() => useCameras(), { wrapper: createHookWrapper() })
    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(result.current.data).toBeUndefined()
  })
})

describe('useChat', () => {
  it('posts the question and the video id to the chat route', async () => {
    let body: unknown = null
    let path = ''
    server.use(
      http.post('/api/chat', async ({ request }) => {
        path = new URL(request.url).pathname
        body = await request.json()
        return HttpResponse.json({
          question: 'what happened?',
          answer: 'Motion.',
          state: 'answered',
          citations: [],
          narrated: false,
          unresolved: 0,
        })
      }),
    )
    const { result } = renderHook(() => useChat(), { wrapper })
    await result.current.mutateAsync({ question: 'what happened?', videoId: 'vid-1' })
    expect(path).toBe('/api/chat')
    expect(body).toEqual({ question: 'what happened?', videoId: 'vid-1' })
  })

  it('invalidates the search when the answer arrives', async () => {
    // A chat answer can change what the search finds, so the search has to be
    // asked again. A hook that answered and left the old results on screen is
    // a hook that shows a stale record.
    const invalidate = vi.spyOn(singleton, 'invalidateQueries')
    const { result } = renderHook(() => useChat(), { wrapper })
    await result.current.mutateAsync({ question: 'what happened?' })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['search'] })
  })
})

describe('useVideoUpload', () => {
  it('uploads to the product and invalidates the clips and the cameras', async () => {
    let path = ''
    server.use(
      http.post('/v1/video/upload', ({ request }) => {
        path = new URL(request.url).pathname
        return HttpResponse.json({
          videoId: 'vid-1',
          evidenceId: 'ev-1',
          eventId: 'evt-1',
          duplicate: false,
          sizeBytes: 1,
          sha256: 'a'.repeat(64),
        })
      }),
    )
    const { result } = renderHook(() => useVideoUpload(), { wrapper })
    const form = new FormData()
    form.append('file', new Blob(['x']), 'x.mp4')
    const invalidate = vi.spyOn(singleton, 'invalidateQueries')
    const response = await result.current.mutateAsync(form)
    expect(response.videoId).toBe('vid-1')
    expect(path).toBe('/v1/video/upload')
    // The MULTIPART content type is not asserted here, and the reason is
    // worth stating: jsdom's `FormData` is not the one undici's `fetch`
    // recognises, so the boundary in this environment is jsdom's business and
    // not the console's. The console's own obligation -- that it does NOT set
    // `Content-Type` and so does not strip the boundary the browser sets --
    // is asserted in `src/lib/api.test.ts`, where the `fetch` call is the
    // thing under test.
    //
    // A new video changes the clips AND the camera inventory, because the
    // video becomes a camera once the VMS's source registrar picks it up.
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['clips'] })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['cameras'] })
  })
})

describe('useVideoYouTube', () => {
  it('posts the url and invalidates the clips and the cameras', async () => {
    let body: unknown = null
    server.use(
      http.post('/v1/video/youtube', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json({
          videoId: 'vid-2',
          evidenceId: 'ev-2',
          eventId: 'evt-2',
          duplicate: false,
          source: 'youtube',
        })
      }),
    )
    const { result } = renderHook(() => useVideoYouTube(), { wrapper })
    const invalidate = vi.spyOn(singleton, 'invalidateQueries')
    await result.current.mutateAsync({ url: 'https://example/watch?v=1', siteId: 'site-demo' })
    expect(body).toEqual({ url: 'https://example/watch?v=1', siteId: 'site-demo' })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['clips'] })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['cameras'] })
  })
})

describe('useRulesFromText', () => {
  it('posts the text and invalidates the rules', async () => {
    let body: unknown = null
    server.use(
      http.post('/v1/rules/from-text', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json({ rule: 'rule-from-text' })
      }),
    )
    const { result } = renderHook(() => useRulesFromText(), { wrapper })
    const invalidate = vi.spyOn(singleton, 'invalidateQueries')
    const response = await result.current.mutateAsync({ text: 'a person with a bag' })
    expect(response.rule).toBe('rule-from-text')
    expect(body).toEqual({ text: 'a person with a bag' })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['rules'] })
  })
})

describe('useAIRuntime', () => {
  it('says the AI is unavailable when the probe does not carry the check', async () => {
    // The readiness probe answered, and its answer did not mention the AI
    // runtime. That is an ABSENCE, and the console must say the AI is not
    // available rather than saying the AI is fine or the AI is broken.
    server.use(
      http.get('/v1/health/ready', () =>
        HttpResponse.json({ status: 'ok', checks: { database: { ok: true } } }),
      ),
    )
    const { result } = renderHook(() => useAIRuntime(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.available).toBe(false)
    expect(result.current.data?.reason).toBeNull()
  })

  it('passes the reason through', async () => {
    server.use(
      http.get('/v1/health/ready', () =>
        HttpResponse.json({
          status: 'degraded',
          checks: { aiRuntime: { ok: false, detail: 'the runtime is not reachable' } },
        }),
      ),
    )
    const { result } = renderHook(() => useAIRuntime(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.available).toBe(false)
    expect(result.current.data?.reason).toBe('the runtime is not reachable')
  })

  it('says the AI is available when the probe says it is', async () => {
    server.use(
      http.get('/v1/health/ready', () =>
        HttpResponse.json({ status: 'ok', checks: { aiRuntime: { ok: true } } }),
      ),
    )
    const { result } = renderHook(() => useAIRuntime(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.available).toBe(true)
  })

  it('asks again every ten seconds', async () => {
    // The AI plane can come up while the operator is watching, so the probe is
    // re-read on a timer. A console that read it once would leave an
    // operator looking at "the AI is not available" for an hour after the
    // runtime came back.
    const { result } = renderHook(() => useAIRuntime(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    const entry = singleton.getQueryCache().find({ queryKey: ['ai-runtime'] })
    expect(entry?.observers[0]?.options.refetchInterval).toBe(10_000)
  })
})
