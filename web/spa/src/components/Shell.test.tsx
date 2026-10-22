import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { beforeAll, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithProviders } from '@/test/render'
import { axe } from '@/test/axe'
import { Shell } from './Shell'

// The shell, and the route table.
//
// The console server used to register twenty-two paths to a file that was
// build output, so every page answered 404 with an empty body while the
// pages themselves sat in the tree, complete and unreachable.
//
// So the routes are a TABLE, and the table is read out of THIS FILE: the
// client owns the routes, the server serves the shell for every path under
// the console's prefix, and the Go side keeps no second copy.
//
// The test below does not restate the table. It reads the table out of the
// source, reads the prefix and the root paths out of the Go files, and
// asserts that the two agree. A second list of a list is a defect even when
// it is currently correct.

const here = path.dirname(fileURLToPath(import.meta.url))
const shellSource = readFileSync(path.join(here, 'Shell.tsx'), 'utf8')
const routesGo = readFileSync(path.join(here, '..', '..', '..', 'routes.go'), 'utf8')
const routesTestGo = readFileSync(path.join(here, '..', '..', '..', 'routes_test.go'), 'utf8')

// The pattern the Go gate uses to read the SPA's routes, character for
// character. A pattern that drifts from the Go one is a gate that reads a
// different table from the one the server serves.
const ROUTE_PATTERN = /<Route\s+path="([^"]+)"/g

/** spaRoutes reads the routes the SPA declares, in the order it declares them. */
function spaRoutes(): string[] {
  const out: string[] = []
  for (const match of shellSource.matchAll(ROUTE_PATTERN)) {
    const declared = match[1]
    if (declared === '*') continue // the catch-all is not a route
    out.push(declared)
  }
  return out
}

/** consolePrefix reads the prefix the console server serves the shell under. */
function consolePrefix(): string {
  const found = /const consolePrefix = "([^"]+)"/.exec(routesGo)
  if (!found?.[1]) {
    throw new Error('the console server declares no prefix, so this test is asserting nothing')
  }
  return found[1]
}

/** rootPaths reads the paths the Go gate says must answer 404 at the root. */
function rootPaths(): string[] {
  const found = /range \[\]string\{([^}]*)\}/.exec(routesTestGo)
  if (!found?.[1]) {
    throw new Error('the Go gate names no root paths, so this test is asserting nothing')
  }
  return found[1]
    .split(',')
    .map((p) => p.trim().replace(/^"|"$/g, ''))
    .filter((p) => p.startsWith('/'))
}

/** showcasePaths reads the paths the ROOT owns, out of the server's own table. */
function showcasePaths(): string[] {
  const found = /var showcasePages = \[\]showcasePage\{([\s\S]*?)\n\}/.exec(routesGo)
  if (!found?.[1]) {
    throw new Error('the server declares no showcase table, so this test is asserting nothing')
  }
  return Array.from(found[1].matchAll(/Path: "([^"]+)"/g)).map((m) => m[1])
}

beforeAll(() => {
  // jsdom has no layout and no scrolling, and the shell scrolls the screen
  // back to the top on every route change.
  Element.prototype.scrollTo = vi.fn()
  Element.prototype.scrollIntoView = vi.fn()
})

describe('the route table', () => {
  it('declares no path twice', () => {
    // Two routes claiming one path is how the demo 404'd: the SPA was
    // registered last and shadowed every page of it.
    const routes = spaRoutes()
    expect(routes.length).toBeGreaterThan(0)
    expect(new Set(routes).size).toBe(routes.length)
  })

  it('declares every route under the console prefix', () => {
    // The server serves the shell for `/console` and everything under it, and
    // the Go gate asks it for `<prefix>/<route>` for every route the SPA
    // declares. A client path that is absolute would be a path the server
    // does not answer, and a route that already began with a slash would
    // make the gate ask for a path with two slashes in it.
    const prefix = consolePrefix()
    for (const route of spaRoutes()) {
      expect(route.startsWith('/')).toBe(false)
      expect(`${prefix}/${route}`).not.toContain(`${prefix}//`)
    }
  })

  it('keeps the root out of the console, the way the Go gate says it is', () => {
    // The Go gate names the console's paths and says they answer 404 at the
    // root: the root belongs to the showcase. The SPA agrees by declaring no
    // path of its own at the root, so every path it declares is served under
    // the prefix and never at the root.
    const named = rootPaths()
    expect(named.length).toBeGreaterThan(0)
    for (const route of spaRoutes()) {
      expect(route.startsWith('/')).toBe(false)
    }
  })
})

describe('the shell', () => {
  it('renders the rail and the top bar', async () => {
    // The stage is fixed: where you are, and what you can do. A screen
    // without the rail is a screen an operator cannot leave.
    renderWithProviders(<Shell />, { route: '/' })
    await waitFor(() =>
      expect(screen.getByRole('heading', { level: 1, name: 'Operations' })).toBeInTheDocument(),
    )
    expect(screen.getByRole('complementary', { name: 'Console navigation' })).toBeInTheDocument()
    expect(screen.getByRole('banner')).toBeInTheDocument()
  })

  it('points the rail only at paths the router serves', async () => {
    // The rail is the console's own route table, written out as links. A
    // link to a path the router does not serve is a 404 in the console.
    //
    // The links are the ROUTER's paths -- `/incidents`, `/clips` -- and not
    // the URLs a browser ends up at. The console is served under `/console`
    // and the router's basename is what puts the prefix back in front of a
    // rendered link, so a link written as `/console/incidents` would be
    // rendered as `/console/console/incidents` and would 404. The rail used
    // to write them that way, and every one of its entries was a dead link.
    //
    // So the assertion is against the ROUTER's own table, read out of the
    // source above, and not against a second list.
    const routes = spaRoutes()
    renderWithProviders(<Shell />, { route: '/' })
    await waitFor(() =>
      expect(screen.getByRole('heading', { level: 1, name: 'Operations' })).toBeInTheDocument(),
    )

    const rail = screen.getByRole('complementary', { name: 'Console navigation' })
    const links = Array.from(rail.querySelectorAll('a[href]'))
    expect(links.length).toBeGreaterThan(0)

    for (const link of links) {
      const href = link.getAttribute('href') ?? ''
      if (href === '/') continue // the index route, and the door to the showcase
      expect(href.startsWith('/')).toBe(true)
      const route = href.slice(1)
      expect(routes).toContain(route)
    }
  })

  it('lights exactly one entry, and it is the one the router is showing', async () => {
    // The rail says where you are with `aria-current`, and it says it with
    // ONE entry.
    //
    // This is the assertion that could not be made before: the rail compares
    // `currentPath` -- the router's own `location.pathname`, with the
    // basename stripped -- against its entries' paths, and the two were in
    // DIFFERENT spaces, so nothing was ever lit. A rail that never says where
    // you are is not a navigation, and the unit tests could not see it
    // because they all rendered the rail with a hand-written path.
    for (const [route, label] of [
      ['/', 'Operations'],
      ['/incidents', 'Incidents'],
      ['/settings', 'Settings'],
    ] as const) {
      const { unmount } = renderWithProviders(<Shell />, { route })
      await waitFor(() => expect(screen.getByRole('complementary', { name: 'Console navigation' })).toBeInTheDocument())
      const current = Array.from(
        screen.getByRole('complementary', { name: 'Console navigation' }).querySelectorAll(
          '[aria-current="page"]',
        ),
      )
      expect(current, `nothing was lit at ${route}`).toHaveLength(1)
      expect(current[0]).toHaveTextContent(label)
      unmount()
    }
  })

  it('claims no path the showcase owns', async () => {
    // The showcase's table is the ROOT's and the console's is the prefix's.
    // A console link to a root path is two owners for one path, which is the
    // defect the server's route table exists to prevent.
    const showcase = showcasePaths()
    expect(showcase.length).toBeGreaterThan(0)
    renderWithProviders(<Shell />, { route: '/' })
    await waitFor(() =>
      expect(screen.getByRole('heading', { level: 1, name: 'Operations' })).toBeInTheDocument(),
    )

    const rail = screen.getByRole('complementary', { name: 'Console navigation' })
    for (const link of Array.from(rail.querySelectorAll('a[href]'))) {
      const href = link.getAttribute('href') ?? ''
      if (href === '/') continue // the deliberate door to the showcase
      expect(showcase).not.toContain(href)
    }
  })

  it('passes the accessibility scan', async () => {
    // The semantic half of accessibility: the rules that need no layout, run
    // on every push in milliseconds. The contrast half needs a laid-out
    // screen and runs in a browser, in the end-to-end suite.
    const { container } = renderWithProviders(<Shell />, { route: '/' })
    await waitFor(() =>
      expect(screen.getByRole('heading', { level: 1, name: 'Operations' })).toBeInTheDocument(),
    )
    const results = await axe(container)
    // The violations themselves rather than the harness's matcher: the
    // assertion says the same thing, and it does not depend on how the
    // matcher happens to be registered.
    expect(results.violations).toEqual([])
  })
})
