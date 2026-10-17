import { useState } from 'react'
import { chat } from '../api'

interface ChatWidgetProps {
  videoId?: string
}

export function ChatWidget({ videoId }: ChatWidgetProps) {
  const [q, setQ] = useState('')
  const [messages, setMessages] = useState<{ role: 'user' | 'assistant'; text: string; citations: string[]; state: string }[]>([])
  const [loading, setLoading] = useState(false)

  const send = async (e: React.FormEvent) => {
    e.preventDefault()
    const text = q.trim()
    if (!text) return
    setQ('')
    setLoading(true)
    const userMsg = { role: 'user' as const, text, citations: [] as string[], state: '' }
    setMessages((m) => [...m, userMsg])
    try {
      const res = await chat(text, videoId)
      const botMsg = {
        role: 'assistant' as const,
        text: res.answer,
        citations: res.citations,
        state: res.state,
      }
      setMessages((m) => [...m, botMsg])
    } catch (err) {
      setMessages((m) => [...m, { role: 'assistant', text: 'Unavailable: could not reach the chat service.', citations: [], state: 'unavailable' }])
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="card" style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
      <div className="card-title">Chat</div>
      <div style={{ flex: 1, overflowY: 'auto', padding: '12px 0', display: 'flex', flexDirection: 'column', gap: 8 }}>
        {messages.map((m, i) => (
          <div key={i} className={`chat-bubble ${m.role}`}>
            {m.text}
            {m.citations.length > 0 && (
              <div className="citation">Sources: {m.citations.join(', ')}</div>
            )}
            <span className={`state-badge state-${m.state}`}>{m.state}</span>
          </div>
        ))}
        {loading && <div className="chat-bubble assistant">Searching stored observations&hellip;</div>}
      </div>
      <form onSubmit={send} style={{ padding: '8px 12px', borderTop: '1px solid var(--color-border)', display: 'flex', gap: 8 }}>
        <input
          className="input"
          placeholder="Ask about stored incidents & evidence…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          disabled={loading}
        />
        <button className="btn btn-primary btn-sm" disabled={loading} type="submit">
          Send
        </button>
      </form>
    </div>
  )
}
