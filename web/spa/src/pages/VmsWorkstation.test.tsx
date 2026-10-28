import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '@/test/render'
import { classifyScene, extractYouTubeId } from '@/lib/video'
import { VmsWorkstation } from './VmsWorkstation'

describe('extractYouTubeId', () => {
  it('accepts watch, embed, shorts, live and youtu.be forms', () => {
    expect(extractYouTubeId('https://www.youtube.com/watch?v=JQ_jwk_7OVE')).toBe('JQ_jwk_7OVE')
    expect(extractYouTubeId('https://youtu.be/JQ_jwk_7OVE')).toBe('JQ_jwk_7OVE')
    expect(extractYouTubeId('https://www.youtube.com/embed/JQ_jwk_7OVE')).toBe('JQ_jwk_7OVE')
    expect(extractYouTubeId('https://www.youtube.com/shorts/JQ_jwk_7OVE')).toBe('JQ_jwk_7OVE')
  })

  it('refuses a non-YouTube url', () => {
    expect(extractYouTubeId('https://example.com/watch?v=abc')).toBe('')
  })
})

describe('classifyScene', () => {
  it('returns a closed, deterministic, clearly-simulated result', async () => {
    const a = await classifyScene('src-a', 'clip a')
    const b = await classifyScene('src-a', 'clip a')
    expect(a.simulated).toBe(true)
    expect(a.detections.length).toBeGreaterThan(0)
    expect(a.detections).toEqual(b.detections)
    for (const d of a.detections) {
      expect(d.confidence).toBeGreaterThan(0)
      expect(d.confidence).toBeLessThanOrEqual(1)
    }
  })
})

describe('the VMS workstation', () => {
  it('renders the wall, the load affordance and the help popup', () => {
    renderWithProviders(<VmsWorkstation />, { route: '/workstation' })
    expect(screen.getByRole('heading', { level: 1, name: 'VMS Workstation' })).toBeInTheDocument()
    expect(screen.getAllByText('Load video').length).toBeGreaterThan(0)
    expect(screen.getByRole('button', { name: 'Help' })).toBeInTheDocument()
  })

  it('loads a local file into a tile and analyses it as simulated', async () => {
    const user = userEvent.setup({ delay: null })
    renderWithProviders(<VmsWorkstation />, { route: '/workstation' })
    await user.click(screen.getAllByText('Load video')[0])
    const input = screen.getByLabelText('Choose a video file')
    const file = new File(['x'], 'entrance.mp4', { type: 'video/mp4' })
    await user.upload(input, file)
    await waitFor(() => expect(screen.getByText('Replace')).toBeInTheDocument())
    await user.click(screen.getByText('Analyse'))
    expect(await screen.findByText(/SIMULATED/)).toBeInTheDocument()
  })

  it('opens and closes the help popup', async () => {
    const user = userEvent.setup({ delay: null })
    renderWithProviders(<VmsWorkstation />, { route: '/workstation' })
    await user.click(screen.getByRole('button', { name: 'Help' }))
    expect(screen.getByText('The wall')).toBeInTheDocument()
    await user.click(screen.getAllByRole('button', { name: 'Close' })[0])
    expect(screen.queryByText('The wall')).not.toBeInTheDocument()
  })

  it('opens the settings popup', async () => {
    const user = userEvent.setup({ delay: null })
    renderWithProviders(<VmsWorkstation />, { route: '/workstation' })
    await user.click(screen.getByRole('button', { name: 'Settings' }))
    expect(screen.getAllByText('2×2 wall').length).toBeGreaterThan(0)
  })
})
