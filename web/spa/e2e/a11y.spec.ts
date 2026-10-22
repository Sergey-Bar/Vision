import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'
import { test, expect } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'

// The accessibility scan, in a real browser.
//
// The unit layer runs axe in jsdom, and there `color-contrast` is DISABLED
// because jsdom has no layout: it has no boxes, no painted values and no
// cascade, so the rule either throws or reports a violation for every element
// in the tree. A scan that is wrong on every page is a scan people learn to
// ignore, and a scan people learn to ignore is worse than no scan.
//
// This file is where the rule comes back. A browser has layout, the
// stylesheet is applied, the fonts are real, and the contrast can be
// COMPUTED. So `color-contrast` is enabled here and only here.
//
// # The scan found real violations, and they are written down
//
// The first run of this file found three: a dimmed text that does not reach
// the contrast ratio, a search button whose accessible name does not contain
// everything it displays, and an upload file input with no label at all.
//
// They are in `a11y-baseline.json` rather than in a comment, and the rule is
// the same as the rule about a refusal anywhere else in this repository:
//
//   the scan must find EXACTLY the recorded violations and no others, and an
//   entry that no longer matches anything is a FAILURE.
//
// The second half is what stops the record going stale. A violation that gets
// FIXED has to have its entry removed in the same commit, because an entry
// that matches nothing is an exemption nobody will ever look at again -- and
// it is also the thing that makes the scan's "no NEW violations" claim
// honest, because a baseline that could absorb anything would absorb
// everything.
//
// # The honest limit
//
// An axe scan is NOT accessibility. It catches the machine-detectable
// minority -- roughly a third of WCAG issues. Meaningful reading order,
// alt-text quality, error announcement and keyboard flow need a human with a
// screen reader. This gate's name says what it is: a machine scan, and the
// substitute for the human one is named in `docs/status.md`.

const CONSOLE_ROUTES = [
  '/console',
  '/console/live',
  '/console/events',
  '/console/incidents',
  '/console/clips',
  '/console/site',
  '/console/audit',
  '/console/users',
  '/console/upload',
  '/console/settings',
]

interface BaselineEntry {
  id: string
  target: string
  routes: string[]
  why: string
}

interface Baseline {
  note: string[]
  violations: BaselineEntry[]
}

const here = dirname(fileURLToPath(import.meta.url))
const baseline: Baseline = JSON.parse(
  readFileSync(resolve(here, 'a11y-baseline.json'), 'utf8'),
) as Baseline

/**
 * The key that identifies a node in the record.
 *
 * It is the node's `id`, then its `aria-label`, then its text, and only then
 * a fragment of its HTML. The order matters: axe's own `target` is a CSS
 * selector built from the element's CLASSES, and a record keyed on class
 * names has to be rewritten every time a style is refactored -- which is a
 * record nobody keeps, and a record nobody keeps is a record that absorbs
 * whatever it is given.
 */
function nodeKey(node: { html?: string }): string {
  const html = node.html ?? ''
  const id = /\sid="([^"]*)"/.exec(html)
  if (id?.[1]) return `id:${id[1]}`
  const label = /aria-label="([^"]*)"/.exec(html)
  if (label?.[1]) return `aria-label:${label[1]}`
  const text = />([^<>]*)</.exec(html)
  if (text?.[1]?.trim()) return `text:${text[1].trim()}`
  return `html:${html.slice(0, 80)}`
}

test('the console routes have no accessibility violation beyond the recorded ones', async ({
  page,
}) => {
  const found: { id: string; key: string; route: string }[] = []
  for (const route of CONSOLE_ROUTES) {
    await page.goto(route)
    await page.waitForLoadState('networkidle')
    const results = await new AxeBuilder({ page }).analyze()
    for (const violation of results.violations) {
      for (const node of violation.nodes) {
        found.push({ id: violation.id, key: nodeKey(node), route })
      }
    }
  }

  // Every violation that was found must be in the record.
  const unmatched = found.filter(
    (f) => !baseline.violations.some((b) => b.id === f.id && b.target === f.key),
  )
  expect(
    unmatched,
    'the scan found a violation that is not in a11y-baseline.json. Either it is new -- ' +
      'in which case it is a defect and the entry goes in the file -- or the entry is ' +
      'wrong.',
  ).toEqual([])

  // And every entry must still be matched by something.
  //
  // A violation that has been FIXED and whose entry has been left behind is a
  // stale exemption, and the gate says so rather than letting it sit there
  // absorbing the next violation of the same rule.
  const stale = baseline.violations.filter(
    (b) => !found.some((f) => f.id === b.id && f.key === b.target),
  )
  expect(
    stale.map((b) => `${b.id} (${b.target})`),
    'a recorded violation no longer happens. Delete the entry in the same commit that ' +
      'fixes it -- a record that has gone stale is an exemption nobody will revisit.',
  ).toEqual([])
})

test('every recorded violation says what closing it takes', () => {
  // A violation with no reason is a hole with a bookmark in it. The reason is
  // what tells the next reader whether this is a defect to fix, a design
  // decision to make, or a limitation of the tool.
  for (const entry of baseline.violations) {
    expect(entry.why.length, `${entry.id} needs a reason`).toBeGreaterThan(40)
    expect(entry.routes.length, `${entry.id} needs a route`).toBeGreaterThan(0)
  }
})
