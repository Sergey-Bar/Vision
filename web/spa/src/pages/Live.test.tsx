import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '@/test/server'
import { renderWithProviders } from '@/test/render'
import { vmsCamera } from '@/test/fixtures'
import { CommandPaletteProvider } from '@/components/CommandPalette'
import { LivePage } from './Live'

// The live route.
//
// It is a SCROLL TARGET and not a page. The wall used to be a page of its
// own, with its own header and its own copy of the camera list, so the
// operator had to choose between "the picture" and "the queue" instead of
// having both.
//
// So what this route does is answer with the operations screen, scrolled to
// the wall. The tests are about that: one screen, the wall in it, and the
// wall's numbers coming from the device rather than from the console.

// The product's readers retry once before they give up, and the retry waits a
// second. The error state therefore arrives later than the default wait for
// an element, so the wait is longer here.
const ERROR_WAIT = { timeout: 5000 }

// jsdom has no layout, and `scrollIntoView` is one of the layout calls it
// does not implement, so a page that scrolls to a section throws on the way
// there. The stub records what was scrolled to, which is the whole of what
// this file asserts about the scroll target.
type ScrollIntoView = (options?: boolean | ScrollIntoViewOptions) => void
const elementProto = Element.prototype as unknown as { scrollIntoView?: ScrollIntoView }
const originalScrollIntoView = elementProto.scrollIntoView
const scrolled: string[] = []

beforeEach(() => {
  scrolled.length = 0
  elementProto.scrollIntoView = function scrollIntoViewStub(this: Element) {
    scrolled.push(this.id)
  }
})

afterEach(() => {
  elementProto.scrollIntoView = originalScrollIntoView
})

function renderLive() {
  return renderWithProviders(
    <CommandPaletteProvider>
      <LivePage />
    </CommandPaletteProvider>,
    { route: '/console/live' },
  )
}

describe('the live route', () => {
  it('answers with the operations screen, and not with a page of its own', async () => {
    renderLive()

    // One screen, three answers. A bookmark to this route must not land the
    // operator on a second version of the truth.
    expect(await screen.findByRole('heading', { level: 1, name: 'Operations' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'The camera wall' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'What needs the operator' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'The record' })).toBeInTheDocument()
  })

  it('scrolls to the wall', async () => {
    renderLive()

    await waitFor(() => expect(scrolled).toContain('wall'))
  })

  it('draws the picture and the boxes the device asserted', async () => {
    // The picture is the device's to give and the boxes are its own
    // assertions about it, so both come from the VMS and neither is invented
    // in the browser.
    renderLive()

    const frame = await screen.findByRole('img', { name: 'Loading bay live view' })
    expect(frame).toHaveAttribute('src', '/vms/cameras/cam-1/frame.png?v=42')
    expect(screen.getByText('PERSON 91%')).toBeInTheDocument()
  })

  it('counts the cameras the device is playing', async () => {
    renderLive()

    expect(await screen.findByText('Cameras live')).toBeInTheDocument()
    // The count arrives with the device's answer, so it is waited for: a
    // label that is on screen while the request is in flight says nothing
    // about how many cameras are playing.
    expect(await screen.findByText('1 / 1')).toBeInTheDocument()
  })

  it('asks the device to pause, and the device decides', async () => {
    // The control surface is the DEVICE's to accept. The console sends the
    // request and redraws from what the device said, so the assertion is on
    // the request the console made -- not on a flag the browser kept.
    const user = userEvent.setup({ delay: null })
    const control = vi.fn()
    server.use(
      http.post('/vms/cameras/:id/:control', (info) => {
        control(info.params)
        return HttpResponse.json({ ...vmsCamera, paused: true })
      }),
    )

    renderLive()

    await user.click(await screen.findByRole('button', { name: 'Loading bay' }))
    await user.click(await screen.findByRole('button', { name: 'Pause' }))

    await waitFor(() =>
      expect(control).toHaveBeenCalledWith(
        expect.objectContaining({ id: 'cam-1', control: 'pause' }),
      ),
    )
  })

  it('says the device is not playing any camera', async () => {
    // The picture is the device's to give, so an empty wall is the device's
    // answer and not the platform's inventory being lost.
    server.use(http.get('/vms/cameras', () => HttpResponse.json({ array: [] })))
    server.use(http.get('/api/cameras', () => HttpResponse.json({ cameras: [] })))

    renderLive()

    expect(await screen.findByText('The VMS is not playing any camera')).toBeInTheDocument()
    expect(screen.queryByText('Loading bay live view')).not.toBeInTheDocument()
  })

  it('says the inventory did not answer, and shows the refusal rather than the body', async () => {
    server.use(
      http.get('/api/cameras', () =>
        HttpResponse.json({ error: 'site_not_assigned' }, { status: 403 }),
      ),
    )

    renderLive()

    const alert = await screen.findByRole('alert', undefined, ERROR_WAIT)
    expect(alert).toHaveTextContent(/The camera inventory did not answer/)
    // The wall is empty because the inventory is unknown, not because the
    // product has no cameras, and the refusal is what the operator acts on.
    expect(alert).toHaveTextContent('site_not_assigned')
    expect(alert.textContent).not.toContain('{"error"')
  })
})
