import { useCallback, useEffect, useState } from 'react'

// The density.
//
// Two of them: comfortable and compact. They are a TOKEN SCALE, not a
// component variant, so a region that is dense is dense in its rows and its
// cell padding and nothing else.
//
// # It is a URL parameter, and that is not a detail
//
// RC-35 bans `localStorage`, `sessionStorage` and `indexedDB` in the console's
// front end, and the reason is the repository's own rule: the console holds
// no state of its own. A density kept in the browser is a density that
// differs per browser, and the density of a screen is a property of the
// screen rather than of the machine somebody is reading it on.
//
// So the density is in the URL. `?density=compact` is a link. It can be
// pasted to a colleague, it survives a reload, and it is gone when the
// parameter is removed -- which is the same as the comfortable default.

export type Density = 'comfortable' | 'compact'

/** DENSITIES is the closed set, in the order the control shows them. */
export const DENSITIES: readonly Density[] = ['comfortable', 'compact'] as const

/** DENSITY_QUERY is the URL parameter the density is read from. */
export const DENSITY_QUERY = 'density'

/** isDensity reports whether a string is one of the two densities. */
export function isDensity(value: string | null): value is Density {
  return value === 'comfortable' || value === 'compact'
}

/** readDensity reads the density out of the current URL. */
export function readDensity(): Density {
  if (typeof window === 'undefined') return 'comfortable'
  const raw = new URLSearchParams(window.location.search).get(DENSITY_QUERY)
  return isDensity(raw) ? raw : 'comfortable'
}

/**
 * useDensity is the density, and the way to change it.
 *
 * Changing it rewrites the URL with `replaceState`, so the change does not
 * put an entry in the browser's history: an operator who toggles the density
 * three times and then presses Back expects to leave the page, not to undo
 * the third toggle.
 */
export function useDensity(): [Density, (next: Density) => void] {
  const [density, setDensity] = useState<Density>(readDensity)

  useEffect(() => {
    const onPop = () => setDensity(readDensity())
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])

  const set = useCallback((next: Density) => {
    setDensity(next)
    const url = new URL(window.location.href)
    if (next === 'comfortable') url.searchParams.delete(DENSITY_QUERY)
    else url.searchParams.set(DENSITY_QUERY, next)
    window.history.replaceState(null, '', url.toString())
  }, [])

  return [density, set]
}
