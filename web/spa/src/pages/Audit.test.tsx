import { describe, expect, it } from 'vitest'
import { delay, http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import { server } from '@/test/server'
import { renderWithProviders } from '@/test/render'
import { AuditPage } from './Audit'

// The audit ledger.
//
// The ledger is the PRODUCT's record: append-only, hash-chained, and written
// by the thing that did the work. This file protects the three ways a console
// can lie about it -- inventing a row, calling an unanswered ledger an empty
// one, and printing the ledger's internals on a screen an operator reads.

// The product's readers retry once before they give up, and the retry waits a
// second. The error state therefore arrives later than the default wait for
// an element, so the wait is longer here.
const ERROR_WAIT = { timeout: 5000 }

const ENTRY = {
  sequence: 7,
  ts: '2026-10-21T08:00:00Z',
  actor: 'sp-service-account',
  actorType: 'service_account',
  action: 'incident.opened',
  targetType: 'incident',
  targetId: 'inc-1',
  resultStateHash: 'abcdef0123456789',
}

describe('the audit log', () => {
  it('draws the rows the ledger returned', async () => {
    // A row that is not the product's is not a row at all, so the assertion is
    // on the values the product sent: the actor, the action and the target.
    server.use(http.get('/v1/audit/ledger', () => HttpResponse.json({ entries: [ENTRY] })))

    renderWithProviders(<AuditPage />)

    expect(await screen.findByText('sp-service-account')).toBeInTheDocument()
    expect(screen.getByText('incident.opened')).toBeInTheDocument()
    expect(screen.getByText('inc-1')).toBeInTheDocument()
    // The hash is a prefix, and a prefix is still a hash an operator can match
    // the product's own against.
    expect(screen.getByText('abcdef01…')).toBeInTheDocument()
  })

  it('says the ledger is empty rather than drawing a row of dashes', async () => {
    server.use(http.get('/v1/audit/ledger', () => HttpResponse.json({ entries: [] })))

    renderWithProviders(<AuditPage />)

    expect(await screen.findByText(/The ledger is empty/)).toBeInTheDocument()
    // A dash is what the page prints for a field a row did not carry. With no
    // rows there are no fields, so a dash here would be a zero wearing a
    // dash's clothes.
    expect(screen.queryByText('—')).not.toBeInTheDocument()
    // And no row the ledger did not return.
    expect(screen.queryByText('sp-service-account')).not.toBeInTheDocument()
  })

  it('says the ledger is not answering, and does not print its internals', async () => {
    server.use(
      http.get('/v1/audit/ledger', () =>
        HttpResponse.json(
          { error: 'read_failed', message: 'pq: relation "audit_ledger" does not exist' },
          { status: 500 },
        ),
      ),
    )

    renderWithProviders(<AuditPage />)

    expect(await screen.findByText(/The ledger is not answering/, undefined, ERROR_WAIT)).toBeInTheDocument()
    // The product's internal wording is not the operator's. The page says what
    // failed in a sentence and leaves the rest to the product's logs.
    expect(screen.queryByText(/read_failed/)).not.toBeInTheDocument()
    expect(screen.queryByText(/relation/)).not.toBeInTheDocument()
    // A ledger that did not answer is an unknown ledger, so there is no table
    // of nothing pretending to be a table of something.
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it.fails('does not call an unanswered ledger empty', () => {
    // The five states are states. A ledger that has not answered yet is
    // LOADING, and the page has no loading branch, so it draws the empty
    // ledger's sentence while the request is still in flight -- which tells
    // the operator that nothing has ever been recorded.
    //
    // The expectation below is the rule; it fails against the page as it is
    // written, and it is here so that a fix cannot land without the test
    // noticing that the defect it was written for has gone.
    server.use(
      http.get('/v1/audit/ledger', async () => {
        await delay(50)
        return HttpResponse.json({ entries: [] })
      }),
    )

    renderWithProviders(<AuditPage />)

    expect(screen.queryByText(/The ledger is empty/)).not.toBeInTheDocument()
  })
})
