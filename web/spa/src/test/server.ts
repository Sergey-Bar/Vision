import { setupServer } from 'msw/node'
import { handlers } from './handlers'

// The network, stood up for the test.
//
// The console reads the product over HTTP and the VMS over the server's
// `/vms` proxy, so a test that renders a page needs something answering those
// paths. This is that something.
//
// It is an MSW server rather than a set of `vi.fn()` mocks because the point
// is the REQUEST: the console's own `lib/api.ts` decides the URL, the method
// and the body, and a mock at the module boundary would let the console change
// its mind about all three without anything noticing.
export const server = setupServer(...handlers)
