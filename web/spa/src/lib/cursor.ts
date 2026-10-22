import { useCallback, useEffect, useState } from 'react'

// The Chief's cursor.
//
// It is a URL parameter, and that is not a detail.
//
// RC-35 bans `localStorage`, `sessionStorage` and `indexedDB` in the console's
// front end, and the reason is the repository's own rule: the console holds
// no state of its own. A cursor kept in the browser is a cursor that differs
// per browser, and "where I was" is a property of the operator rather than of
// the machine they are reading it on.
//
// It is not on the server either. A cursor on the server is a table of what
// each operator looked at, which is a record of a person that the product
// would then have to keep and chain, and the cursor is not worth a row.
//
// So the cursor is in the URL. `?since=...` is a link. It can be pasted to a
// colleague, it survives a reload, it survives an interruption, and it is
// gone when the operator closes the tab — which is the correct lifetime for
// "where I was".
//
// This is the same mechanism `lib/density.ts` uses for the density, and it is
// the same mechanism on purpose: a console with two ways to carry a URL
// parameter has two ways to be wrong about one.

/** CURSOR_QUERY is the URL parameter the cursor is read from. */
export const CURSOR_QUERY = 'since'

/** readCursor reads the cursor out of the current URL. */
export function readCursor(): string | null {
  if (typeof window === 'undefined') return null
  return new URLSearchParams(window.location.search).get(CURSOR_QUERY)
}

/**
 * useCursor is the cursor, and the way to move it.
 *
 * Moving it rewrites the URL with `replaceState`, so the change does not put
 * an entry in the browser's history: an operator who marks as seen three
 * times and then presses Back expects to leave the page, not to undo the
 * third mark.
 *
 * The cursor moves to NOW and not to the moment of the last incident, because
 * the cursor is "where I was looking", not "the last thing that happened". A
 * cursor that pointed at the last incident would hide every incident between
 * it and the moment the operator actually looked, which is exactly the set the
 * Chief exists to report.
 */
export function useCursor(): [string | null, () => void] {
  const [cursor, setCursor] = useState<string | null>(readCursor)

  useEffect(() => {
    const onPop = () => setCursor(readCursor())
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])

  const markSeen = useCallback(() => {
    const url = new URL(window.location.href)
    url.searchParams.set(CURSOR_QUERY, new Date().toISOString())
    window.history.replaceState(null, '', url.toString())
    setCursor(url.searchParams.get(CURSOR_QUERY))
  }, [])

  return [cursor, markSeen]
}
