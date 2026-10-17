import { useEffect, useRef, useState } from 'react'
import { searchFree } from '../api'

interface SearchBarProps {
  onResults: (hits: any[]) => void
}

export function SearchBar({ onResults }: SearchBarProps) {
  const [q, setQ] = useState('')
  const [loading, setLoading] = useState(false)
  const [results, setResults] = useState<any[]>([])
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    let t: any
    if (!q.trim()) {
      setResults([])
      onResults([])
      return
    }
    setLoading(true)
    t = setTimeout(async () => {
      try {
        const res = await searchFree(q.trim(), { limit: 20 })
        setResults(res.hits)
        onResults(res.hits)
      } catch (e) {
        console.error('search failed', e)
        setResults([])
        onResults([])
      } finally {
        setLoading(false)
      }
    }, 250)
    return () => clearTimeout(t)
  }, [q, onResults])

  return (
    <div className="search-bar">
      <span className="search-icon">&#x1F50D;</span>
      <input
        ref={inputRef}
        className="search-input"
        placeholder="Search events — person, red car, zone, time…"
        value={q}
        onChange={(e) => setQ(e.target.value)}
        autoFocus
      />
      {loading && <span className="text-sm text-muted" style={{ marginLeft: 8 }}>&hellip;</span>}
      {results.length > 0 && (
        <div className="text-sm text-muted mt-2">
          {results.length} result(s)
        </div>
      )}
    </div>
  )
}
