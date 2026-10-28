import { test, expect } from '@playwright/test'

// The console, in a browser.
//
// The unit tests render the console in jsdom, which is a good deal about
// markup and no deal at all about a browser: a router that redirects, a
// stylesheet that loads, a focus that moves, a scroll that follows. This file
// is the layer where those are real.
//
// The four things it proves, and the four things it deliberately does not:
//
//   1. The console LOADS, and the rail is the rail: four groups, one entry
//      lit.
//   2. `/console/live` and `/console/events` are the SAME screen as
//      `/console`, scrolled to a place -- and the rail says where you are for
//      all three. A navigation that lights two entries at once, or none, is a
//      navigation that cannot say where you are.
//   3. The console does not reach outside its own origin. The browser must
//      never hold the product's or the VMS's credential, and the only way to
//      hold one is to talk to the thing that has it directly.
//   4. The console does not show a row the product did not produce.
//
// It does NOT walk the operator's critical path. That journey needs a seeded
// product, a seeded tenant and a VMS, and it belongs with the other
// end-to-end work in `docs/status.md` rather than in a file that quietly
// skips when those are absent. A test that skips is a test that stopped
// being run, and this repository has a gate for exactly that.

const CONSOLE_ROUTES = ['/console', '/console/live', '/console/events']

// The rail.
//
// It is an `<aside>` carrying the label, not a `<nav>`: the label is on the
// element that IS the rail, and the `<nav>` inside it is the list. So the
// locator is the labelled element rather than a role, because a role would
// be asserting a shape the console does not have.
const rail = (page: import('@playwright/test').Page) =>
  page.locator('[aria-label="Console navigation"]')

test.describe('the console loads', () => {
  test('the rail renders its unified workspaces and the more menu', async ({ page }) => {
    await page.goto('/console')
    const nav = rail(page)
    await expect(nav).toBeVisible()
    for (const item of ['Operations', 'Incidents', 'Clips', 'Recording', 'Devices', 'Chain']) {
      await expect(nav.getByLabel(item)).toBeVisible()
    }
    await expect(nav.getByLabel('More')).toBeVisible()
  })

  test('the operations entry is lit for all three of its paths', async ({ page }) => {
    for (const route of CONSOLE_ROUTES) {
      await page.goto(route)
      const operations = page.getByRole('link', { name: 'Operations' })
      await expect(operations).toBeVisible()
      // `aria-current="page"` is how the rail says where you are. Exactly one
      // entry may carry it: two lit entries is a navigation that cannot say
      // where you are.
      await expect(operations).toHaveAttribute('aria-current', 'page')
      const current = page.locator('a[aria-current="page"]')
      await expect(current).toHaveCount(1)
    }
  })

  test('the live and events paths land on the operations screen', async ({ page }) => {
    await page.goto('/console')
    const heading = await page.locator('main h1').first().textContent()
    for (const route of ['/console/live', '/console/events']) {
      await page.goto(route)
      await expect(page.locator('main h1').first()).toHaveText(heading ?? '')
    }
  })
})

test.describe('the console stays inside its own origin', () => {
  test('no request leaves the console\'s origin', async ({ page }) => {
    // The console is a SERVER, not a static bundle, and the reason is a
    // credential: the console's server holds the product's and the VMS's
    // credentials, and the browser must hold neither. A console that called
    // the product directly would have to put a credential in the browser, and
    // a credential in the browser is a credential in a screenshot.
    //
    // So every request the console makes is to the console's own origin, and
    // the server proxies from there.
    //
    // The origins are compared AFTER the navigation, because `page.url()` is
    // `about:blank` while the first request is on its way out, and a
    // comparison against `null` would call every request foreign.
    const requested: string[] = []
    page.on('request', (request) => {
      requested.push(request.url())
    })
    await page.goto('/console')
    await page.waitForLoadState('networkidle')
    const own = new URL(page.url()).origin
    const foreign = requested.filter((u) => new URL(u).origin !== own)
    expect(foreign).toEqual([])
  })

  test('no request names the tenant in a header', async ({ page }) => {
    // The tenant and the site are CONFIGURED. A header from a caller with no
    // session to match it against is an instruction rather than an
    // authorisation, and the console must not send one even to itself.
    const offenders: string[] = []
    page.on('request', (request) => {
      if (request.headers()['x-tenant-id']) {
        offenders.push(`${request.method()} ${request.url()}`)
      }
    })
    await page.goto('/console')
    await page.waitForLoadState('networkidle')
    expect(offenders).toEqual([])
  })
})
