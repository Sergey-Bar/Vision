import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithProviders } from '@/test/render'
import { SetupPage } from './Setup'

// The setup path.
//
// "Plug and play" is a promise about the FIRST day, and a promise nobody can
// check is a promise nobody keeps. The page asks the PRODUCT what is left to
// do and renders what the product says — so the tests are about the page not
// making anything up and about an unfinished step always saying what to do.

describe('the setup page', () => {
  it('shows what the product says is left', async () => {
    renderWithProviders(<SetupPage />)

    expect(await screen.findByText('The site exists')).toBeInTheDocument()
    expect(screen.getByText(/This tenant has no site/)).toBeInTheDocument()
  })

  it('says what to do next for every unfinished step', async () => {
    // A step that is not done and does not say what to do is a complaint.
    renderWithProviders(<SetupPage />)

    await screen.findByText('The site exists')
    expect(screen.getByText(/Run security-seed/)).toBeInTheDocument()
    expect(screen.getByText(/Add the camera on the Devices page/)).toBeInTheDocument()
  })

  it('shows the steps that are done separately', async () => {
    renderWithProviders(<SetupPage />)

    expect(await screen.findByText('What is done')).toBeInTheDocument()
    expect(screen.getByText('The database answers')).toBeInTheDocument()
  })

  it('names the next step, which is the first unfinished one', async () => {
    renderWithProviders(<SetupPage />)

    expect(await screen.findByText(/next: site/)).toBeInTheDocument()
  })

  it('counts the steps', async () => {
    renderWithProviders(<SetupPage />)

    expect(await screen.findByText(/2 of 6 steps/)).toBeInTheDocument()
  })
})
