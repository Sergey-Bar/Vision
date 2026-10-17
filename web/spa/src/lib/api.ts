type WindowWithApiBase = Window & { __API_BASE__?: string }

export const API_BASE = (() => {
  if (typeof window === 'undefined') return ''
  return (window as WindowWithApiBase).__API_BASE__ ?? ''
})()

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
    this.name = 'ApiError'
  }
}

async function request(path: string, init: RequestInit = {}): Promise<Response> {
  const res = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...init.headers,
    },
  })
  if (!res.ok) {
    let body = ''
    try {
      body = await res.text()
    } catch {
      // ignore
    }
    throw new ApiError(res.status, body || res.statusText)
  }
  return res
}

export function get(path: string): Promise<Response> {
  return request(path)
}

export function post(path: string, body: unknown): Promise<Response> {
  return request(path, { method: 'POST', body: JSON.stringify(body) })
}
