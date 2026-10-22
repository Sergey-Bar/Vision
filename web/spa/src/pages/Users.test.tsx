import { describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import { server } from '@/test/server'
import { renderWithProviders } from '@/test/render'
import { UsersPage } from './Users'

// The identity screen.
//
// There are no users here, and the page says so instead of listing four
// invented people. A page that showed names, avatars and an MFA badge for
// people who do not exist would be a page asserting that a specific human did
// a specific thing -- the one thing this product must never do.
//
// So the assertions are about what IS on the screen (the principal, its role,
// its scope, its capabilities) and about what is NOT (a list of people).

// The product's principal, in the shape the product sends it.
//
// `internal/api/handlers.go` writes `siteScope` as a LIST -- `p.SiteScope` is
// a []string -- and the page maps over it. The console's own `Principal` type
// in `src/api.ts` declares it as a `string`, which is a type the wire does not
// have, and the shared fixture follows that type; so this file sends the
// product's shape. The mismatch is reported, not papered over.
// The product's readers retry once before they give up, and the retry waits a
// second. The error state therefore arrives later than the default wait for
// an element, so the wait is longer here.
const ERROR_WAIT = { timeout: 5000 }

interface PrincipalBody {
  tenantId: string
  roles: string[]
  siteScope: string[]
  capabilities: string[]
  serviceAccount: boolean
  timezone?: string
}

const PRINCIPAL: PrincipalBody = {
  tenantId: 'tenant-demo',
  roles: ['operator'],
  siteScope: ['site-demo'],
  capabilities: ['incident.read', 'incident.write'],
  serviceAccount: true,
  timezone: 'Europe/Berlin',
}

describe('the identity page', () => {
  it('shows the principal the install acts as', async () => {
    server.use(http.get('/v1/principal', () => HttpResponse.json(PRINCIPAL)))

    renderWithProviders(<UsersPage />)

    expect(await screen.findByText('operator')).toBeInTheDocument()
    expect(screen.getByText(/tenant tenant-demo/)).toBeInTheDocument()
    expect(screen.getByText('incident.read')).toBeInTheDocument()
    expect(screen.getByText('incident.write')).toBeInTheDocument()
    expect(screen.getByText('site-demo')).toBeInTheDocument()
  })

  it('says the console has no login', () => {
    // No session, no password and no second factor, because there is no
    // person to authenticate. The page has to say it rather than leave an
    // operator looking for a sign-in button.
    server.use(http.get('/v1/principal', () => HttpResponse.json(PRINCIPAL)))

    renderWithProviders(<UsersPage />)

    expect(
      screen.getByText(/There is no login and no user/),
    ).toBeInTheDocument()
    expect(
      screen.getByText(/There is no session, no password and no second factor/),
    ).toBeInTheDocument()
  })

  it('shows no list of people', () => {
    // The page used to list four invented people. One principal, and no
    // roster: a table of names would be a set of claims about humans that
    // nothing in the product supports.
    server.use(http.get('/v1/principal', () => HttpResponse.json(PRINCIPAL)))

    renderWithProviders(<UsersPage />)

    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    expect(screen.queryByRole('list')).not.toBeInTheDocument()
  })

  it('says the API is not answering', async () => {
    server.use(
      http.get('/v1/principal', () =>
        HttpResponse.json({ error: 'site_not_assigned' }, { status: 403 }),
      ),
    )

    renderWithProviders(<UsersPage />)

    expect(
      await screen.findByText(
        /The API is not answering, so the console cannot say who it is acting as/,
        undefined,
        ERROR_WAIT,
      ),
    ).toBeInTheDocument()
    // The console cannot say who it is, so it does not say anything else
    // either: a half-drawn principal is a half-drawn person.
    expect(screen.queryByText('operator')).not.toBeInTheDocument()
  })
})
