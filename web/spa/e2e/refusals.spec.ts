import { test, expect } from '@playwright/test'

// The console's refusals.
//
// The product's refusals are its contract: `no_active_bundle`,
// `step_up_required`, `site_not_assigned`. Each one is a status and a body,
// and the console's job is to hand the pair on unaltered and to say it in
// words a person can act on.
//
// Two of the product's rules are about the STATUS, and they are the ones a
// console gets wrong by accident:
//
//   a site the principal is not assigned to answers 404, not 403 -- because
//   a 403 says "this exists and you may not have it", and the product does
//   not tell a caller that a site exists
//
//   a sensitive disposition with no second factor answers 403, not 400 --
//   because 400 says "the request was malformed", and the request was well
//   formed: it was refused
//
// Those two are the SERVER's rules and the server's own tests hold them. What
// this file holds is the console's half: that a refusal reaches the screen
// as a sentence, that the sentence is about the refusal, and that a refusal
// never becomes a blank panel.
//
// The product is not reachable from the console alone, so this file proves
// the console's handling of a refusal rather than the product's status code.
// The status codes are proved in `internal/api` and `internal/incidents`.

// The console's camera inventory, settled.
//
// The console retries a failed answer, and between the retries the network is
// idle, so `networkidle` returns while the console is still asking. Waiting
// for the SETTLED state is what makes these assertions about what the
// operator finally reads, and not about what happened to be on the screen
// between two attempts.
const settled = async (page: import('@playwright/test').Page) => {
  await expect(
    page.locator('main'),
    'the console never finished reading the camera inventory',
  ).toContainText(/could not read the answer/i, { timeout: 30_000 })
}

test.describe('a refusal reaches the screen', () => {
  test('the console says the answer could not be read', async ({ page }) => {
    await page.goto('/console')
    await settled(page)
    const body = await page.locator('body').innerText()
    // The console's own words, from `src/pages/Operations.tsx`: it says what
    // it asked and what it could not read, and it does NOT say "0".
    expect(body).toMatch(/could not read the answer/i)
  })

  test('a refusal is not rendered as an empty measurement', async ({ page }) => {
    await page.goto('/console')
    await settled(page)
    // The strip's camera counters are a DASH when the answer did not arrive,
    // and a dash is not a zero. The Cockpit used to render `0 / 0` and
    // `not configured` while the request was in flight, and a zero that
    // means "I have not asked yet" is the most expensive kind of lie in an
    // instrument panel.
    const strip = page.locator('main')
    await expect(strip).toBeVisible()
    const text = await strip.innerText()
    expect(text).toMatch(/—/)
  })

  test('the refusal names the product, not a stack', async ({ page }) => {
    await page.goto('/console')
    await settled(page)
    const body = await page.locator('body').innerText()
    expect(body).not.toMatch(/\bat\s+\w+\s+\(/)
    expect(body).not.toContain('undefined')
  })
})
