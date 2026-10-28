import { beforeAll, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '@/test/render'
import { axe } from '@/test/axe'
import { Shell } from './Shell'

beforeAll(() => {
  Element.prototype.scrollTo = vi.fn()
  Element.prototype.scrollIntoView = vi.fn()
})

describe('the shell', () => {
  it('renders one screen: the rail, the top bar, and the wall by default', async () => {
    renderWithProviders(<Shell />)
    await waitFor(() =>
      expect(screen.getByRole('heading', { level: 1, name: 'VMS Workstation' })).toBeInTheDocument(),
    )
    expect(screen.getByRole('complementary', { name: 'Console navigation' })).toBeInTheDocument()
    expect(screen.getByRole('banner')).toBeInTheDocument()
  })

  it('switches panels in place, without changing the URL', async () => {
    const user = userEvent.setup({ delay: null })
    renderWithProviders(<Shell />)
    await waitFor(() =>
      expect(screen.getByRole('heading', { level: 1, name: 'VMS Workstation' })).toBeInTheDocument(),
    )
    await user.click(screen.getByLabelText('Incidents'))
    await waitFor(() =>
      expect(screen.getByRole('heading', { level: 1, name: /Incidents/i })).toBeInTheDocument(),
    )
  })

  it('lights exactly one rail entry at a time', async () => {
    renderWithProviders(<Shell />)
    await waitFor(() =>
      expect(screen.getByRole('heading', { level: 1, name: 'VMS Workstation' })).toBeInTheDocument(),
    )
    const rail = screen.getByRole('complementary', { name: 'Console navigation' })
    expect(rail.querySelectorAll('[aria-current="page"]')).toHaveLength(1)
  })

  it('keeps the showcase as the only external door', () => {
    renderWithProviders(<Shell />)
    const rail = screen.getByRole('complementary', { name: 'Console navigation' })
    const links = Array.from(rail.querySelectorAll('a[href]'))
    expect(links).toHaveLength(1)
    expect(links[0].getAttribute('href')).toBe('/')
  })

  it('passes the accessibility scan on the default screen', async () => {
    const { container } = renderWithProviders(<Shell />)
    await waitFor(() =>
      expect(screen.getByRole('heading', { level: 1, name: 'VMS Workstation' })).toBeInTheDocument(),
    )
    const results = await axe(container)
    expect(results.violations).toEqual([])
  })
})
