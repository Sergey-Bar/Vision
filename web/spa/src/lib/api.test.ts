import { afterEach, describe, expect, it, vi } from 'vitest'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/server'
import { ApiError, API_BASE, get, post, upload } from './api'

// The console's HTTP layer.
//
// `lib/api.ts` is the only place the console talks to the product, so it is
// the only place a request can be wrong in a way the pages cannot see. The
// four things it decides are: the URL, the method, the body, and -- on a
// failure -- the error the operator reads.
//
// The error is the part that carries the product's refusals. `no_active_bundle`,
// `step_up_required`, `site_not_assigned`: each of them is a status and a
// body, and the console's job is to hand that pair on unaltered. A failure
// that loses the body becomes "Forbidden" in the UI, and an operator cannot
// act on "Forbidden".

describe('the request helpers', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('answers a 200 with the response itself', async () => {
    server.use(
      http.get('/v1/thing', () => HttpResponse.json({ ok: true })),
    )
    const res = await get('/v1/thing')
    expect(res.ok).toBe(true)
    await expect(res.json()).resolves.toEqual({ ok: true })
  })

  it('carries the status and the body of a refusal', async () => {
    server.use(
      http.get('/v1/thing', () =>
        HttpResponse.json({ error: 'no_active_bundle' }, { status: 409 }),
      ),
    )
    await expect(get('/v1/thing')).rejects.toBeInstanceOf(ApiError)
    const err = await get('/v1/thing').catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(409)
    expect((err as ApiError).message).toContain('no_active_bundle')
  })

  it('names the error ApiError, so a caller can tell it from anything else', async () => {
    server.use(http.get('/v1/thing', () => HttpResponse.json({}, { status: 500 })))
    const err = await get('/v1/thing').catch((e: unknown) => e)
    expect((err as ApiError).name).toBe('ApiError')
    expect(err).toBeInstanceOf(Error)
  })

  it('falls back to the status text when the refusal has no body', async () => {
    server.use(
      http.get('/v1/thing', () => new HttpResponse(null, { status: 403, statusText: 'Forbidden' })),
    )
    const err = await get('/v1/thing').catch((e: unknown) => e)
    expect((err as ApiError).status).toBe(403)
    expect((err as ApiError).message).toBe('Forbidden')
  })

  it('still raises the refusal when the body cannot be read', async () => {
    // The body read is inside a try/catch, and the catch is the branch that
    // matters: a body that fails to read is a broken proxy, and a broken
    // proxy must not turn a refusal into a success.
    const stub = vi.fn().mockResolvedValue({
      ok: false,
      status: 502,
      statusText: 'Bad Gateway',
      text: () => Promise.reject(new Error('the body stream failed')),
    })
    vi.stubGlobal('fetch', stub)
    const err = await get('/v1/thing').catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(502)
    expect((err as ApiError).message).toBe('Bad Gateway')
  })

  it('posts the body as JSON', async () => {
    const stub = vi.fn<(url: string, init: RequestInit) => Promise<unknown>>().mockResolvedValue({
      ok: true,
      status: 200,
      statusText: 'OK',
      json: () => Promise.resolve({ ok: true }),
    })
    vi.stubGlobal('fetch', stub)
    await post('/api/thing', { question: 'what happened?' })
    const [url, init] = stub.mock.calls[0] ?? []
    expect(url).toBe('/api/thing')
    expect(init?.method).toBe('POST')
    expect(init?.body).toBe(JSON.stringify({ question: 'what happened?' }))
    // The header is read through a `Headers` rather than by indexing, so the
    // assertion does not depend on the console's header bag being a plain
    // object: `lib/api.ts` builds one, and a reader of the test should not
    // have to know that.
    expect(new Headers(init?.headers ?? {}).get('Content-Type')).toBe('application/json')
  })

  it('uploads multipart without setting Content-Type', async () => {
    // The browser sets the multipart boundary, and a Content-Type set here
    // would be a boundary-less `multipart/form-data` -- which the server
    // cannot parse. This is the kind of defect that passes a unit test that
    // asserts the request "went out" and fails in a browser.
    const stub = vi.fn<(url: string, init: RequestInit) => Promise<unknown>>().mockResolvedValue({
      ok: true,
      status: 200,
      statusText: 'OK',
      json: () => Promise.resolve({ videoId: 'vid-1' }),
    })
    vi.stubGlobal('fetch', stub)
    const form = new FormData()
    form.append('file', new Blob(['x']), 'x.mp4')
    await upload('/v1/video/upload', form)
    const [, init] = stub.mock.calls[0] ?? []
    expect(init?.method).toBe('POST')
    expect(init?.body).toBe(form)
    expect(init?.headers).toBeUndefined()
  })

  it('raises the refusal from an upload too', async () => {
    server.use(
      http.post('/v1/video/upload', () =>
        HttpResponse.json({ error: 'site_not_assigned' }, { status: 403 }),
      ),
    )
    const form = new FormData()
    const err = await upload('/v1/video/upload', form).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(403)
    expect((err as ApiError).message).toContain('site_not_assigned')
  })
})

describe('the API base', () => {
  it('is empty when the page has not set one', () => {
    // The default is the console's own server, which proxies `/v1`, `/api` and
    // `/vms` to the product. A console that called the product directly would
    // need a credential in the browser, and the browser must never hold one.
    expect(API_BASE).toBe('')
  })

  it('is the page\'s base when the page has set one', async () => {
    // The base is read once, at module load, so the module has to be loaded
    // again to see a different value. `resetModules` is the mechanism.
    vi.resetModules()
    ;(window as Window & { __API_BASE__?: string }).__API_BASE__ = '/console-proxy'
    try {
      const mod = await import('./api')
      expect(mod.API_BASE).toBe('/console-proxy')
    } finally {
      delete (window as Window & { __API_BASE__?: string }).__API_BASE__
      vi.resetModules()
    }
  })
})
