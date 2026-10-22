import 'vitest'

// The accessibility matcher's types, on Vitest's `expect`.
//
// `jest-axe`'s matcher is declared in `jest-axe.d.ts`; this file is where it
// becomes a MATCHER -- the thing `expect(element).toHaveNoViolations()` can
// call. Without it the assertion is a compile error, and a compile error in a
// test file is a test file nobody can run.
//
// The matcher is declared here, and only here, so there is one place that
// says what the accessibility assertion is.
declare module 'vitest' {
  // The type parameter has to be named `T` and has to default to `any`:
  // TypeScript requires every declaration of an interface to have an
  // IDENTICAL type parameter list, and Vitest's own `Assertion<T = any>` is
  // the declaration being augmented. The two lint rules below are about a
  // name this file does not choose, so they are switched off here rather than
  // worked around by a cast that would change the interface's meaning.
  // eslint-disable-next-line @typescript-eslint/no-unused-vars, @typescript-eslint/no-explicit-any
  interface Assertion<T = any> {
    /**
     * Asserts that the given element has no axe accessibility violations.
     */
    toHaveNoViolations(): void
  }

  interface AsymmetricMatchersContaining {
    /**
     * An asymmetric matcher for "no axe accessibility violations".
     */
    toHaveNoViolations(): void
  }
}

export {}
