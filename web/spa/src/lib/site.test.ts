import { describe, expect, it } from 'vitest'
import { waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { createElement } from 'react'
import type { QueryClient } from '@tanstack/react-query'
import { server } from '@/test/server'
import { principal, principalWithoutTimezone } from '@/test/fixtures'
import { renderWithProviders } from '@/test/render'
import { formatInZone, formatOffset, formatSiteTime, useSiteClock } from './site'

// The site's clock.
//
// "The site's clock, never the host's" is a repository rule, and it is a rule
// about what an operator reads: the same console must not show the same
// incident at two different moments on two different machines.
//
// The clock says which zone it is in, and it says it in the OFFSET as well,
// because a clock without an offset is a clock two people read differently.
// When the site's zone cannot be read the clock says UTC -- and says UTC in
// the offset too, rather than showing a time with nothing attached to it.
//
// The instants below are FIXED, so the assertions do not depend on the
// machine's own timezone or on the day the suite runs.

// 15 January 2026, 12:00:00 UTC. Europe/Berlin is UTC+1 in January, and
// America/New_York is UTC-5.
const NOON = new Date('2026-01-15T12:00:00Z')

describe('formatOffset', () => {
  it('spells UTC as +00:00', () => {
    // Not "GMT", and not nothing: an offset is how the reader knows which
    // moment this is.
    expect(formatOffset(NOON, 'UTC')).toBe('+00:00')
  })

  it('spells a zone east of UTC with its positive offset', () => {
    expect(formatOffset(NOON, 'Europe/Berlin')).toBe('+01:00')
  })

  it('spells a zone west of UTC with its negative offset', () => {
    expect(formatOffset(NOON, 'America/New_York')).toBe('-05:00')
  })

  it('says nothing at all about a zone that cannot be read', () => {
    // An unreadable zone is not an offset of zero, and it is not an
    // invented one either. The caller decides what to show instead.
    expect(formatOffset(NOON, 'Not/AZone')).toBe('')
  })
})

describe('formatInZone', () => {
  it('shows the wall clock in the named zone, with that zone\'s offset', () => {
    // The instant is noon UTC and the zone is an hour ahead, so the clock
    // reads one in the afternoon -- and the offset says why.
    expect(formatInZone(NOON, 'Europe/Berlin')).toEqual({ clock: '13:00:00', offset: '+01:00' })
  })

  it('shows the wall clock in the named zone west of UTC', () => {
    expect(formatInZone(NOON, 'America/New_York')).toEqual({ clock: '07:00:00', offset: '-05:00' })
  })

  it('falls back to UTC for a zone that cannot be read, and says that it is UTC', () => {
    // The offset MUST be +00:00 and not empty. A clock that reads 12:00:00
    // with nothing attached to it is a clock that two readers will read as
    // two different moments, which is the exact thing this file exists to
    // prevent.
    const { clock, offset } = formatInZone(NOON, 'Not/AZone')
    expect(clock).toBe('12:00:00')
    expect(offset).toBe('+00:00')
    expect(offset).not.toBe('')
  })
})

describe('formatSiteTime', () => {
  it('renders the instant in the zone with the offset attached', () => {
    expect(formatSiteTime(NOON, 'Europe/Berlin')).toBe('15 Jan 2026, 13:00:00 +01:00')
  })

  it('falls back to the ISO string for a zone that cannot be read', () => {
    // A timestamp the console cannot place in a zone is still a timestamp,
    // and the operator is better served by the exact instant than by a
    // second, differently formatted, guess.
    expect(formatSiteTime(NOON, 'Not/AZone')).toBe('2026-01-15T12:00:00.000Z')
  })

  it('renders a timestamp that is not a date as the em dash', () => {
    // The pages render a missing time as a dash. A console that rendered
    // "Invalid Date" instead would be showing the operator a defect.
    expect(formatSiteTime('not-a-date')).toBe('—')
  })
})

// The browser's own zone, made visible.
//
// The rule is that the clock is the SITE's. A zone that came from the browser
// would show a different moment on every machine, and the operator would read
// it as the site's. The recorder notes every zone the console asks for, so a
// call that fell back to the browser's zone -- a call with no zone at all --
// is visible rather than assumed.
function recordRequestedZones() {
  const zones: (string | undefined)[] = []
  const real = Intl.DateTimeFormat
  class Recorded extends real {
    constructor(locales?: string | string[], options?: Intl.DateTimeFormatOptions) {
      super(locales, options)
      zones.push(options?.timeZone)
    }
  }
  Object.defineProperty(Intl, 'DateTimeFormat', {
    value: Recorded,
    configurable: true,
    writable: true,
  })
  return {
    zones,
    restore: () => {
      Object.defineProperty(Intl, 'DateTimeFormat', {
        value: real,
        configurable: true,
        writable: true,
      })
    },
  }
}

// The clock, rendered under the harness's own providers.
//
// `renderWithProviders` is the harness's wrapper and it does not hand that
// wrapper out, so the clock is rendered THROUGH it and the values are read
// off the DOM -- which is where the top bar reads them from too.
function SiteClockProbe() {
  const { clock, zone, offset, known } = useSiteClock()
  return createElement(
    'div',
    {
      'data-testid': 'site-clock',
      'data-zone': zone,
      'data-offset': offset,
      'data-known': String(known),
    },
    clock,
  )
}

function renderSiteClock() {
  return renderWithProviders(createElement(SiteClockProbe))
}

// The principal has arrived.
//
// The clock renders UTC until the principal answers, so "the clock says UTC"
// is what a clock says both before the answer and after an answer that
// carries no zone. The distinction is the whole point of these tests, so the
// wait is on the answer rather than on the clock.
async function waitForPrincipal(queryClient: QueryClient) {
  await waitFor(() => {
    expect(queryClient.getQueryData(['principal'])).toBeDefined()
  })
}

describe('useSiteClock', () => {
  it('takes the zone from the principal, never from the browser', async () => {
    server.use(http.get('/v1/principal', () => HttpResponse.json(principal)))
    const recorder = recordRequestedZones()
    try {
      const view = renderSiteClock()
      await waitForPrincipal(view.queryClient)
      const clock = view.getByTestId('site-clock')
      await waitFor(() => expect(clock).toHaveAttribute('data-zone', 'Europe/Berlin'))

      expect(clock).toHaveAttribute('data-known', 'true')
      expect(clock).toHaveAttribute('data-offset', expect.stringMatching(/^[+-]\d{2}:\d{2}$/))

      // Every rendering asked for a NAMED zone. A call with no zone at all
      // is a call that used the browser's, and there was none.
      expect(recorder.zones.length).toBeGreaterThan(0)
      expect(recorder.zones.every((z) => z !== undefined)).toBe(true)
      expect(recorder.zones).toContain('Europe/Berlin')
    } finally {
      recorder.restore()
    }
  })

  it('says UTC and does not pretend to know when the principal has no timezone', async () => {
    server.use(
      http.get('/v1/principal', () => HttpResponse.json(principalWithoutTimezone)),
    )
    const view = renderSiteClock()
    await waitForPrincipal(view.queryClient)
    const clock = view.getByTestId('site-clock')

    expect(clock).toHaveAttribute('data-zone', 'UTC')
    expect(clock).toHaveAttribute('data-known', 'false')
    // The clock still carries an offset, and it says UTC.
    expect(clock).toHaveAttribute('data-offset', '+00:00')
  })

  it('does not claim to know a site that is in UTC', async () => {
    // UTC is a real zone and a real answer, and it is not a site timezone
    // the console has learned. The console says so, rather than calling an
    // unknown zone known.
    server.use(
      http.get('/v1/principal', () => HttpResponse.json({ ...principal, timezone: 'UTC' })),
    )
    const view = renderSiteClock()
    await waitForPrincipal(view.queryClient)
    const clock = view.getByTestId('site-clock')

    expect(clock).toHaveAttribute('data-zone', 'UTC')
    expect(clock).toHaveAttribute('data-known', 'false')
  })
})
