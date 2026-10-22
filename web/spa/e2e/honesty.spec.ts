import { test, expect } from '@playwright/test'

// The console, honest.
//
// Four properties, and all four are things the console could get wrong in a
// way that a unit test in jsdom would not catch, because each of them is
// about the relationship between the browser and the server.
//
//   1. The response carries a Content-Security-Policy with NO `unsafe-inline`.
//      A policy that allows inline script is a policy that says "trust every
//      string that reaches this page", and the console renders operator
//      content: an incident title, a camera name, a rule id.
//   2. The console shows NO row the product did not produce. With the product
//      unreachable, every panel must say so. A panel with a camera in it that
//      the product never sent is the exact defect the console's own history
//      is full of: an invented camera list that looked exactly like a real
//      one.
//   3. The console does not leak the product's internals into the screen: no
//      stack trace, no raw JSON body, no internal URL.
//   4. The clock says which zone it is in. A time without a zone is a time
//      two people read differently.

test('the policy carries no unsafe-inline', async ({ request }) => {
  const response = await request.get('/console')
  const csp = response.headers()['content-security-policy']
  expect(csp, 'the response carries no Content-Security-Policy').toBeTruthy()
  expect(csp).not.toContain('unsafe-inline')
  expect(response.headers()['x-frame-options']).toBe('DENY')
})

test.describe('with the product unreachable', () => {
  // The console server proxies to the product. With the product not running,
  // every answer is a refusal, and the console must render the refusal.
  //
  // The point is not that the console survives an outage. The point is that
  // it survives an outage WITHOUT INVENTING, and that is a property of the
  // rendering and not of the network.

  test('no panel shows a row the product did not send', async ({ page }) => {
    await page.goto('/console')
    await page.waitForLoadState('networkidle')
    const body = (await page.locator('body').innerText()).toLowerCase()
    // The camera inventory is the one that used to be invented. There is no
    // camera, and no camera may appear.
    expect(body).not.toContain('entrance north')
    expect(body).not.toContain('loading bay')
  })

  test('a failed answer is a sentence, not a stack trace', async ({ page }) => {
    await page.goto('/console')
    await page.waitForLoadState('networkidle')
    const body = await page.locator('body').innerText()
    expect(body).not.toContain('at Object.')
    expect(body).not.toContain('goroutine ')
    expect(body).not.toContain('"error":')
  })

  test('the clock says which zone it is in', async ({ page }) => {
    await page.goto('/console')
    await page.waitForLoadState('networkidle')
    // The site zone is unknown when the product has not answered, and the
    // console says UTC RATHER than quietly using the browser's zone. A
    // fallback that is wrong is a fallback nobody notices.
    const body = await page.locator('body').innerText()
    expect(body).toMatch(/UTC/)
  })
})
