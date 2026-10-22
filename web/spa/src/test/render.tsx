import { render } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import type { ReactElement, ReactNode } from 'react'

// The render helper.
//
// A page in this console is three providers deep: the query client, the
// router, and (for the pages that need it) nothing else. A test that renders a
// page without the query client gets a hook that throws, and a test that
// renders a page without the router gets a `Link` that throws, so the
// provider set is a fixture of its own.
//
// The query client is FRESH PER TEST. `src/hooks/api.ts` exports a module-level
// singleton, and a test that used it would share a cache with every other
// test in the file: one test's answer would be the next test's answer, and a
// test that asserts a fetch happened would pass because a previous test had
// already fetched it.
//
// `retry: false` and `staleTime: 0` are not conveniences. A query that
// retries is a query that makes three requests where the test expects one,
// and a query with a stale time keeps its answer across a re-render, so a
// test that changes the mock and re-renders sees the OLD answer. Both
// defaults make a test depend on the order it ran in.

export function createTestQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        staleTime: 0,
        gcTime: 0,
      },
      mutations: {
        retry: false,
      },
    },
  })
}

export function TestProviders({
  children,
  queryClient,
  route = '/',
}: {
  children: ReactNode
  queryClient: QueryClient
  route?: string
}) {
  return (
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[route]}>{children}</MemoryRouter>
    </QueryClientProvider>
  )
}

export interface RenderWithProvidersOptions {
  route?: string
  queryClient?: QueryClient
}

export function renderWithProviders(
  ui: ReactElement,
  { route = '/', queryClient = createTestQueryClient() }: RenderWithProvidersOptions = {},
) {
  const result = render(ui, {
    wrapper: ({ children }) => (
      <TestProviders queryClient={queryClient} route={route}>
        {children}
      </TestProviders>
    ),
  })
  return { ...result, queryClient }
}

// The wrapper a `renderHook` needs.
//
// It is the same provider set, in the same shape, so a hook tested here runs
// under the same conditions it runs under in the application -- which is the
// only way a hook test is evidence about the hook rather than about the test.
//
// The client defaults to a fresh one, and a test that needs the APPLICATION's
// client -- the one that carries the retry predicate -- passes it in.
export function createHookWrapper(queryClient: QueryClient = createTestQueryClient()) {
  return function HookWrapper({ children }: { children: ReactNode }) {
    return (
      <TestProviders queryClient={queryClient} route="/">
        {children}
      </TestProviders>
    )
  }
}
