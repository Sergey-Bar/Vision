import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithProviders } from '@/test/render'
import { RecordingsPage } from './Recordings'

// The recording.
//
// The question the page answers is the one a VMS exists to answer: "is it
// recording?". The discipline it keeps is the product's discipline:
//
//   - a camera that recorded NOTHING says "no recording", and NOT "healthy" —
//     a camera nobody asked and a camera that failed are different facts;
//   - a camera whose recording has a HOLE says so, and publishes the hole
//     with its SIZE, because "broken" without a size is not actionable;
//   - the chain is the PROOF, and the page reports the product's own verdict
//     on it rather than deciding for itself.

describe('the recordings page', () => {
  it('says a recording with a hole in it is broken', async () => {
    renderWithProviders(<RecordingsPage />)

    expect(await screen.findByText(/broken/)).toBeInTheDocument()
    expect(screen.getByText(/1 hole\(s\)/)).toBeInTheDocument()
  })

  it('publishes the hole with its size', async () => {
    // A gap with no size is not actionable, and the page is the only place
    // the operator learns that the camera was quiet.
    renderWithProviders(<RecordingsPage />)

    await screen.findByText(/broken/)
    expect(screen.getByText(/1 min of silence/)).toBeInTheDocument()
  })

  it('says the recording is verified when the chain verifies', async () => {
    renderWithProviders(<RecordingsPage />)

    expect(await screen.findByText(/verified/)).toBeInTheDocument()
  })

  it('offers the camera whose recording is being read', async () => {
    // A timeline is a CAMERA's timeline, so the page names the camera and
    // lets the operator choose another one.
    renderWithProviders(<RecordingsPage />)

    expect(await screen.findByLabelText('Camera')).toBeInTheDocument()
  })
})
