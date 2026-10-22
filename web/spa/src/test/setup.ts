import '@testing-library/jest-dom/vitest'
import { afterAll, afterEach, beforeAll, expect } from 'vitest'
import { toHaveNoViolations } from 'jest-axe'
import { server } from './server'

// The test setup.
//
// Three things, and the third is the one that matters.
//
// 1. jest-dom, so `toBeInTheDocument` and `toHaveTextContent` exist. The
//    `/vitest` entry point is the one that registers the matchers on
//    Vitest's `expect`; the default entry point registers them on Jest's,
//    which is not the one this project uses.
// 2. The axe matcher, so a component's accessibility is assertable in the same
//    test that asserts its behaviour. `jest-axe` exports the matcher as an
//    OBJECT -- `{ toHaveNoViolations: fn }` -- and `expect.extend` wants that
//    object, not the function inside it. Handing it the function registers a
//    matcher whose value is a function, and every assertion then throws.
// 3. The network.
//
// On (3): `onUnhandledRequest: 'error'` is the setting, and it is the
// frontend's version of this repository's `no-silent-skips` rule.
//
// A request the console makes that no handler covers is a FAILURE. Without it,
// an unhandled request is a warning in the console, a test that passes, and a
// reader who believes the page under test talked to the product when it did
// not. With it, the moment a page calls a route the harness does not know
// about, the suite goes red and the harness grows a handler.
//
// The same reasoning is why `resetHandlers` runs after every test: one test's
// override of a route must not answer the next test's request.

expect.extend(toHaveNoViolations)

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => server.resetHandlers())
afterAll(() => server.close())
