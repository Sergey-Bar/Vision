import { useEffect, useMemo, useState } from 'react'
import { usePrincipal } from '@/hooks/product'

// The site's clock.
//
// "The site's clock, never the host's" is a rule in this repository, and it
// is a rule about DISPLAY: an operator in one site and a reader of the
// record in another must not read the same timestamp as two different
// moments.
//
// The console used to use the BROWSER's zone, which means the same console
// showed a different time on two machines, and neither of them was
// necessarily the site's. The site's timezone lives in the site's row; the
// console asks the product for it.
//
// When the product has not told the console -- because the site has no
// timezone recorded, or because the product could not be reached -- the
// clock says UTC and says that it is UTC. It does not fall back to the
// browser's zone, because a fallback that is wrong is a fallback nobody
// notices.

// The offset, spelled out. `+03:00`, never `GMT+3`, and never nothing.
export function formatOffset(date: Date, timeZone: string): string {
  try {
    const parts = new Intl.DateTimeFormat('en-GB', {
      timeZone,
      timeZoneName: 'longOffset',
    }).formatToParts(date)
    const found = parts.find((p) => p.type === 'timeZoneName')
    return found ? found.value.replace('GMT', '') : ''
  } catch {
    return ''
  }
}

// The wall clock in a named zone, with its offset.
export function formatInZone(date: Date, timeZone: string): { clock: string; offset: string } {
  try {
    const clock = new Intl.DateTimeFormat('en-GB', {
      timeZone,
      hour12: false,
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    }).format(date)
    return { clock, offset: formatOffset(date, timeZone) }
  } catch {
    const clock = new Intl.DateTimeFormat('en-GB', {
      timeZone: 'UTC',
      hour12: false,
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    }).format(date)
    return { clock, offset: '+00:00' }
  }
}

// The clock, ticking.
//
// The tick lives here and nowhere else, so the top bar does not re-render
// every second.
export function useSiteClock(): { clock: string; zone: string; offset: string; known: boolean } {
  const { data: principal } = usePrincipal()
  const [now, setNow] = useState(() => new Date())

  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), 1000)
    return () => clearInterval(id)
  }, [])

  // The product's own timezone field, when the contract carries it. The
  // principal's scope is the site's, so the principal is where the zone
  // belongs.
  const zone = (principal as { timezone?: string } | undefined)?.timezone ?? ''
  const known = zone !== '' && zone !== 'UTC'

  return useMemo(() => {
    const effective = known ? zone : 'UTC'
    const { clock, offset } = formatInZone(now, effective)
    return { clock, zone: effective, offset, known }
  }, [now, zone, known])
}

// The site's local time, for a timestamp the console renders.
//
// A timestamp is rendered in the site's zone with the offset attached, and a
// timestamp the product recorded is never re-interpreted: it is rendered
// where it happened.
export function formatSiteTime(value: string | number | Date, timeZone?: string): string {
  const date = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  const zone = timeZone ?? 'UTC'
  try {
    const parts = new Intl.DateTimeFormat('en-GB', {
      timeZone: zone,
      year: 'numeric',
      month: 'short',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
      timeZoneName: 'longOffset',
    }).formatToParts(date)
    const get = (type: string) => parts.find((p) => p.type === type)?.value ?? ''
    return `${get('day')} ${get('month')} ${get('year')}, ${get('hour')}:${get('minute')}:${get('second')} ${get('timeZoneName').replace('GMT', '')}`
  } catch {
    return date.toISOString()
  }
}
