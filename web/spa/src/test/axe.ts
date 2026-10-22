import { configureAxe } from 'jest-axe'

// The accessibility scan, in jsdom.
//
// `color-contrast` is DISABLED here, and the reason is worth stating because a
// reader will otherwise assume the scan covers contrast.
//
// jsdom has no layout. It has no boxes, no painted values, no font metrics and
// no cascade from a stylesheet the browser did not apply. axe's contrast rule
// needs the resolved foreground and background colours of a laid-out element;
// with neither, it either throws or reports a violation for every element in
// the tree. The result is a scan that is wrong on every page, and a scan that
// is wrong on every page is a scan people learn to ignore.
//
// So the rule is recovered where it can be computed: in a real browser, by
// `@axe-core/playwright`, in `web/spa/e2e/a11y.spec.ts`. The split is
// deliberate. The semantic and attribute rules -- the ones that need no
// layout -- run on every component in this file, on every push, in
// milliseconds. The contrast rule runs on every console route, in a browser,
// with the stylesheet actually applied.
//
// The two together are more than the one, and the one alone would have been
// less than the truth.
export const axe = configureAxe({
  rules: {
    'color-contrast': { enabled: false },
  },
})
