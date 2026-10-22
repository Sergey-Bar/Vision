import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithProviders } from '@/test/render'
import { ChainPage } from './Chain'

// The provider chain.
//
// The page's claim is the one an auditor asks about: the ORDER of the
// providers is the policy, and the log says what left the site by DIGEST
// and never by content.

describe('the chain page', () => {
  it('shows the providers in the order the policy names them', async () => {
    renderWithProviders(<ChainPage />)

    // The provider's name appears TWICE — once in the chain and once in the
    // egress log — so the assertion is about the ORDER of the two in the
    // rendered page rather than about a unique element.
    await screen.findByText('local_bundle')
    const body = document.body.textContent ?? ''
    expect(body.indexOf('local_bundle')).toBeLessThan(body.indexOf('anthropic'))
  })

  it('says what left the site and by how much', async () => {
    renderWithProviders(<ChainPage />)

    expect(
      await screen.findByText(/1 piece\(s\) of evidence, 20480 bytes/),
    ).toBeInTheDocument()
  })

  it('says that the log carries digests and never the content', async () => {
    // A log that carried the content would be a second copy of the evidence
    // somewhere nobody chose to put it.
    renderWithProviders(<ChainPage />)

    expect(await screen.findByText(/DIGESTS of what left and never the content/)).toBeInTheDocument()
  })

  it('says whether anything may leave the machine', async () => {
    renderWithProviders(<ChainPage />)

    expect(await screen.findByText('What may leave the machine')).toBeInTheDocument()
    expect(screen.getByText('allowed')).toBeInTheDocument()
  })

  it('never renders a credential', async () => {
    // The chain names a credential by an environment variable and never
    // carries one, so the page can only ever say whether one is set.
    renderWithProviders(<ChainPage />)

    await screen.findByText('local_bundle')
    expect(screen.getByText('credential set')).toBeInTheDocument()
    expect(screen.getByText('no credential')).toBeInTheDocument()
    expect(document.body.textContent).not.toMatch(/sk-ant|api[_-]?key["':= ]/i)
  })
})
