// The axe matcher's types.
//
// `jest-axe` ships no type declarations of its own, and the ones it used to
// ship augmented `jest.Matchers`, which is not the namespace Vitest reads.
// Without this file `expect(...).toHaveNoViolations()` is a compile error, and
// a compile error in a test file is a test file nobody can run.
//
// This file is AMBIENT on purpose: it declares a module that has no types,
// which is only possible in a file with no top-level import or export. The
// matcher for Vitest's own `expect` lives in `vitest-matchers.d.ts`, because
// augmenting a module requires that module to be imported.

declare module 'jest-axe' {
  export interface AxeNodeResult {
    target: string[]
    html: string
    failureSummary: string
  }

  export interface AxeResult {
    id: string
    impact: string | null
    tags: string[]
    description: string
    help: string
    helpUrl: string
    nodes: AxeNodeResult[]
  }

  export interface AxeResults {
    violations: AxeResult[]
    passes: AxeResult[]
    incomplete: AxeResult[]
    inapplicable: AxeResult[]
    testEngine: { name: string; version: string }
    testRunner: { name: string; version: string }
    testEnvironment: { name: string; version: string }
    timestamp: string
    url: string
    toolOptions: Record<string, unknown>
  }

  // `toHaveNoViolations` is the MATCHER OBJECT that `expect.extend` takes,
  // not the matcher function. The distinction is not a detail: `expect.extend`
  // wants `{ name: fn }`, and handing it the function alone registers a
  // matcher called "toHaveNoViolations" whose value is the function, and every
  // `expect(results).toHaveNoViolations()` then throws.
  export const toHaveNoViolations: {
    toHaveNoViolations(results: AxeResults): { pass: boolean; message: () => string }
  }

  export function configureAxe(options?: Record<string, unknown>): (
    element: Element | Element[] | string,
    options?: Record<string, unknown>,
  ) => Promise<AxeResults>

  export const axe: (
    element: Element | Element[] | string,
    options?: Record<string, unknown>,
  ) => Promise<AxeResults>
}
