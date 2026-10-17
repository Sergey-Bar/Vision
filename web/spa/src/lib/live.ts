type Listener = (event: unknown) => void

export interface LiveClientOptions {
  url: string
  reconnectInterval?: number
  pollInterval?: number
}

export class LiveClient {
  private ws: WebSocket | null = null
  private readonly url: string
  private readonly listeners = new Set<Listener>()
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private pollTimer: ReturnType<typeof setInterval> | null = null
  private connecting = false
  private readonly pollInterval: number

  constructor({ url, pollInterval = 15_000 }: LiveClientOptions) {
    this.url = url
    this.pollInterval = pollInterval
  }

  connect() {
    if (this.connecting || this.ws?.readyState === WebSocket.OPEN) return
    this.connecting = true
    try {
      this.ws = new WebSocket(this.url)
      this.ws.onopen = () => {
        this.connecting = false
        this.stopPoll()
        this.listeners.forEach((l) => l({ type: 'connected' }))
      }
      this.ws.onmessage = (ev) => {
        let data: unknown
        if (typeof ev.data === 'string') {
          try {
            data = JSON.parse(ev.data) as unknown
          } catch {
            data = { raw: ev.data }
          }
        } else {
          data = { raw: String(ev.data) }
        }
        this.listeners.forEach((l) => l(data))
      }
      this.ws.onclose = () => {
        this.ws = null
        this.listeners.forEach((l) => l({ type: 'disconnected' }))
        this.scheduleReconnect()
      }
      this.ws.onerror = () => {
        this.ws?.close()
      }
    } catch {
      this.connecting = false
      this.scheduleReconnect()
    }
    this.startPoll()
  }

  private scheduleReconnect() {
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer)
    this.reconnectTimer = setTimeout(() => {
      this.connecting = false
      this.connect()
    }, 3000)
  }

  private startPoll() {
    this.stopPoll()
    this.pollTimer = setInterval(() => {
      void fetch(`${this.url.replace(/^ws/, 'http')}/poll`, { cache: 'no-store' }).catch(() => {
        /* polling best-effort */
      })
    }, this.pollInterval)
  }

  private stopPoll() {
    if (this.pollTimer) clearInterval(this.pollTimer)
    this.pollTimer = null
  }

  subscribe(listener: Listener) {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  send(message: unknown) {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(message))
    }
  }

  disconnect() {
    this.stopPoll()
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer)
    this.ws?.close()
    this.ws = null
  }
}
