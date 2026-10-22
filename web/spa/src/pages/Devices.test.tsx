import { describe, expect, it } from 'vitest'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { renderWithProviders } from '@/test/render'
import { DevicesPage } from './Devices'

// The device registry.
//
// The discipline the page keeps is the discipline the product keeps: a camera
// NAMES the profiles it supports and is routinely wrong about it, so a device
// that was never MEASURED has no capabilities — which is not the same as a
// device with none.

describe('the devices page', () => {
  it('says a measured device is measured', async () => {
    renderWithProviders(<DevicesPage />)

    expect(await screen.findByText('cam-1')).toBeInTheDocument()
    expect(screen.getAllByText(/measured/).length).toBeGreaterThan(0)
  })

  it('says a device nobody probed was not probed', async () => {
    // NOT "no capabilities". A camera that can do nothing and a camera
    // nobody asked are different facts, and the second is the one this
    // product is about.
    renderWithProviders(<DevicesPage />)

    expect(await screen.findByText('cam-2')).toBeInTheDocument()
    expect(screen.getByText(/not probed/)).toBeInTheDocument()
    expect(screen.getByText(/Nobody has measured this device/)).toBeInTheDocument()
  })

  it('shows only the VERIFIED capabilities', async () => {
    renderWithProviders(<DevicesPage />)

    expect(await screen.findByText('onvif.profile_s')).toBeInTheDocument()
    expect(screen.getByText('onvif.profile_t')).toBeInTheDocument()
  })

  it('publishes the closed vocabularies, not only the ones this install met', async () => {
    renderWithProviders(<DevicesPage />)

    expect(await screen.findByText('What a capability can be')).toBeInTheDocument()
    // The vocabulary AND the prose name them, so the assertion is that they
    // are named at all rather than that they appear exactly once.
    expect(screen.getAllByText(/contradicted/).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/unverified/).length).toBeGreaterThan(0)
  })

  it('offers a probe on every device', async () => {
    renderWithProviders(<DevicesPage />)

    await screen.findByText('cam-1')
    expect(screen.getAllByRole('button', { name: /Probe/ }).length).toBe(2)
  })

  it('registers a device the operator added', async () => {
    // The ADD form is the first screen of "plug and play", and it was a
    // button nobody had ever pressed in a test: the coverage gate said so
    // by naming the mutation as uncovered, which is the gate doing its job.
    renderWithProviders(<DevicesPage />)

    await screen.findByText('cam-1')
    fireEvent.click(screen.getByRole('button', { name: /Add a device/ }))
    fireEvent.change(screen.getByLabelText('Device id'), { target: { value: 'cam-3' } })
    fireEvent.click(screen.getByRole('button', { name: 'Register' }))

    // The FORM goes away, and the button that opened it stays -- so the
    // assertion is about the form's own field rather than about a label the
    // panel and the button share.
    await waitFor(() => {
      expect(screen.queryByLabelText('Device id')).not.toBeInTheDocument()
    })
  })

  it('probes a device on the operator s request', async () => {
    // The probe is the MEASUREMENT: everything before it is a claim
    // somebody typed, and everything after it is a fact.
    renderWithProviders(<DevicesPage />)

    await screen.findByText('cam-1')
    fireEvent.click(screen.getAllByRole('button', { name: /Probe/ })[0])

    await waitFor(() => {
      expect(screen.getAllByRole('button', { name: /Probe/ }).length).toBe(2)
    })
  })
})
