import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '@/test/render'
import { Sidebar } from './Sidebar'

function renderRail(view: string) {
  const onNavigate = vi.fn()
  const utils = renderWithProviders(
    <Sidebar view={view} onNavigate={onNavigate} open={false} onClose={() => undefined} />,
  )
  return { ...utils, onNavigate }
}

describe('the rail', () => {
  it('renders the unified primary rail and the more menu', () => {
    renderRail('wall')
    for (const item of ['Workstation', 'Incidents', 'Clips', 'Recording', 'Devices', 'Chain']) {
      expect(screen.getByLabelText(item)).toBeInTheDocument()
    }
    expect(screen.getByLabelText('More')).toBeInTheDocument()
  })

  it('marks the item that is showing', () => {
    renderRail('incidents')
    const current = document.querySelectorAll('[aria-current="page"]')
    expect(current).toHaveLength(1)
    expect(current[0]).toHaveAttribute('aria-label', 'Incidents')
  })

  it('switches the panel when a button is pressed', async () => {
    const user = userEvent.setup({ delay: null })
    const { onNavigate } = renderRail('wall')
    await user.click(screen.getByLabelText('Incidents'))
    expect(onNavigate).toHaveBeenCalledWith('incidents')
  })

  it('lights nothing for a view that does not exist', () => {
    renderRail('does-not-exist')
    expect(document.querySelectorAll('[aria-current="page"]')).toHaveLength(0)
  })
})
