import { describe, expect, it } from 'vitest'
import { Route, Routes } from 'react-router-dom'
import { screen } from '@testing-library/react'
import { renderWithProviders } from '@/test/render'
import { EvidencePage } from './Evidence'

// The evidence.
//
// One moment, as the product holds it. The id comes from the route, the bytes
// come from the product's own content route, and the METADATA comes from the
// product's own row.
//
// So the thing worth protecting is the URL the page points at -- a wrong one
// serves another moment -- and the digest. The digest is read from the
// product and never computed by the console: a digest rendered by the console
// is a digest nobody checked.

function renderEvidence(id: string) {
  return renderWithProviders(
    <Routes>
      <Route path="/evidence/:id" element={<EvidencePage />} />
    </Routes>,
    { route: `/evidence/${id}` },
  )
}

describe('the evidence page', () => {
  it('is named by the route it was reached at', () => {
    renderEvidence('ev-1')

    expect(screen.getByRole('heading', { level: 1, name: 'ev-1' })).toBeInTheDocument()
  })

  it('points at the product\'s own content route', () => {
    // The bytes are the product's, and a wrong id here serves a different
    // moment under this one's name.
    renderEvidence('ev-1')

    expect(screen.getByRole('link', { name: 'Open the content' })).toHaveAttribute(
      'href',
      '/v1/evidence/ev-1/content',
    )
    expect(screen.getByRole('img', { name: /the frame the product holds/i })).toHaveAttribute(
      'src',
      '/v1/evidence/ev-1/content',
    )
  })

  it('shows the metadata the product holds, not four dashes', async () => {
    // The page used to render a dash for the kind, the observed-at and the
    // sha-256, with a note explaining that the console did not carry the
    // metadata. The note was honest and the page was empty — and the digest
    // is the one field an operator has to be able to read BEFORE they trust a
    // clip.
    renderEvidence('ev-1')

    expect(await screen.findByText('clip')).toBeInTheDocument()
    expect(screen.getByText('a'.repeat(64))).toBeInTheDocument()
    expect(screen.getByText('1310720 bytes')).toBeInTheDocument()
  })

  it('offers the way back to the record that cites it', () => {
    renderEvidence('ev-1')

    expect(screen.getByRole('link', { name: 'Back to incidents' })).toHaveAttribute(
      'href',
      '/console/incidents',
    )
  })
})
