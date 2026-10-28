import { afterEach, describe, expect, it, vi } from 'vitest'
import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '@/test/server'
import { renderWithProviders } from '@/test/render'
import { principal, videoUploadResponse } from '@/test/fixtures'
import { axe } from '@/test/axe'
import { UploadPage } from './Upload'

// The upload page.
//
// The page writes to the PRODUCT and to nothing else. It used to keep a list
// of "uploaded videos" and "uploaded rules" in the browser's localStorage, and
// the Live, Clips and Incidents pages read that list and drew tiles, clips and
// incidents from it -- every one of them a thing that existed only in that
// browser.
//
// So what is worth protecting here is the REQUEST. The page sends a file, a
// URL, a rule text and the site the ingest belongs to, and it hands back the
// refusal the product answered with. All of it has to survive the trip,
// because an ingest addressed to a site that does not exist, and a refusal
// the operator cannot read, are the two ways this page can be wrong without
// looking wrong.
//
// The site is the principal's. The console has no login, so the principal is
// the only thing that knows which site this console acts for, and a page that
// uploads to a constant, or to an empty site, addresses ingest to a place
// that does not exist.

// The wait for a reader that has GIVEN UP.
//
// The product's readers retry once before they give up, and the retry waits a
// second, so a reader that has given up is a second and a bit away. The wait
// is long enough to outlast the retry and short enough that the test finishes
// inside the suite's own five seconds.
const GAVE_UP_WAIT = { timeout: 2500 }

type User = ReturnType<typeof userEvent.setup>

// The calls the page made.
//
// The console's own `lib/api.ts` decides the URL, the method and the body, so
// a test that reads the request is reading the console's behaviour and not a
// mock's. The type is written out rather than imported so that the helper
// says what it needs and no more.
type FetchCall = [input: RequestInfo | URL, init?: RequestInit | undefined]

function callsTo(spy: { mock: { calls: FetchCall[] } }, path: string): FetchCall[] {
  return spy.mock.calls.filter(([input]) => input === path)
}

// The multipart body, read at the boundary.
//
// MSW cannot read it: the interceptor stringifies a FormData before the
// handler sees the request, so the parts are gone by the time the product
// would have received them. The spy is the last place the form exists, and
// what it reads is the page's own call.
function formOf(spy: { mock: { calls: FetchCall[] } }, path: string): FormData {
  const body = callsTo(spy, path).at(-1)?.[1]?.body
  if (!(body instanceof FormData)) {
    throw new Error(`the call to ${path} carried no form`)
  }
  return body
}

function fileOf(form: FormData, key: string): File {
  const value = form.get(key)
  if (!(value instanceof File)) {
    throw new Error(`the form carries no file under ${key}`)
  }
  return value
}

function fileInput(container: HTMLElement): HTMLInputElement {
  const input = container.querySelector<HTMLInputElement>('#file-input')
  if (!input) throw new Error('the page has no file input')
  return input
}

// Two megabytes, so the size the page renders is a real size and not a
// zero rounded to nothing.
const CLIP = new File([new Uint8Array(2 * 1024 * 1024)], 'loading-bay.mp4', {
  type: 'video/mp4',
})

// The principal has to have ARRIVED before anything is uploaded.
//
// The site the ingest is addressed to is the principal's, and a page that read
// the principal too late would address the upload to an empty site -- which is
// the defect two of the tests below are about.
async function renderUpload() {
  const view = renderWithProviders(<UploadPage />)
  await waitFor(() => expect(view.queryClient.getQueryData(['principal'])).toBeDefined())
  return view
}

// The operator's half of an upload: choose the file, then press the button.
async function chooseAndStart(user: User, container: HTMLElement, file: File = CLIP) {
  await user.upload(fileInput(container), file)
  await user.click(await screen.findByRole('button', { name: 'Start Upload' }))
}

afterEach(() => {
  vi.restoreAllMocks()
})

describe('the upload page', () => {
  it('sends the file the operator chose, to the site the principal names', async () => {
    // The two halves of the request, and they are the whole point of the
    // page: the operator's own bytes, and the site they belong to.
    const user = userEvent.setup({ delay: null })
    const spy = vi.spyOn(globalThis, 'fetch')

    const view = await renderUpload()
    await chooseAndStart(user, view.container)

    await waitFor(() => expect(callsTo(spy, '/v1/video/upload')).toHaveLength(1))
    const form = formOf(spy, '/v1/video/upload')
    // The file is the operator's, and it is the file they chose.
    const sent = fileOf(form, 'file')
    expect(sent.name).toBe('loading-bay.mp4')
    expect(sent.type).toBe('video/mp4')
    expect(sent.size).toBe(CLIP.size)
    // And the site is the PRODUCT's site, read from the principal, and not a
    // constant the page wrote down.
    expect(form.get('siteId')).toBe('site-demo')
  })

  it('shows the operator\'s file, and says the product took it', async () => {
    const user = userEvent.setup({ delay: null })
    server.use(http.post('/v1/video/upload', () => HttpResponse.json(videoUploadResponse)))

    const view = await renderUpload()
    await chooseAndStart(user, view.container)

    // The file is on the screen the way the operator left it.
    expect(await screen.findByText('loading-bay.mp4')).toBeInTheDocument()
    expect(screen.getByText(/video\/mp4 • 2\.00 MB/)).toBeInTheDocument()
    // And the outcome is the product's: the console does not decide that an
    // upload worked.
    expect(await screen.findByText('Accepted by the product')).toBeInTheDocument()
  })

  it('sends the YouTube URL and the site, and shows what the product answered', async () => {
    const user = userEvent.setup({ delay: null })
    let body: unknown = null
    server.use(
      http.post('/v1/video/youtube', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json({
          videoId: 'vid-2',
          evidenceId: 'ev-2',
          eventId: 'evt-2',
          duplicate: false,
          source: 'youtube',
        })
      }),
    )

    await renderUpload()
    await user.type(
      screen.getByPlaceholderText('https://www.youtube.com/watch?v=...'),
      'https://www.youtube.com/watch?v=abc',
    )
    await user.click(screen.getByRole('button', { name: 'Add' }))

    // The body is the one the product's route expects: the URL and the site.
    // A body with the URL and no site is a request the product cannot place.
    const answered = await screen.findByText(/YouTube ingested/)
    expect(body).toEqual({
      url: 'https://www.youtube.com/watch?v=abc',
      siteId: 'site-demo',
    })
    // And the answer is the product's own: its video id and its event id.
    expect(answered).toHaveTextContent('YouTube ingested: vid-2 (evt-2)')
  })

  it('hands a refusal on as the word the operator can act on', async () => {
    // The product's refusals are its contract: `no_active_bundle`,
    // `step_up_required`, `site_not_assigned`. The console's job is to hand
    // the pair on unaltered and to say it in words.
    const user = userEvent.setup({ delay: null })
    server.use(
      http.post('/v1/video/youtube', () =>
        HttpResponse.json({ error: 'step_up_required' }, { status: 403 }),
      ),
    )

    await renderUpload()
    await user.type(
      screen.getByPlaceholderText('https://www.youtube.com/watch?v=...'),
      'https://www.youtube.com/watch?v=abc',
    )
    await user.click(screen.getByRole('button', { name: 'Add' }))

    expect(await screen.findByText('step_up_required')).toBeInTheDocument()
    // The refusal is the vocabulary and the JSON around it is not.
    expect(document.body.textContent).not.toContain('{"error"')
    // No stack frame: this is a screen an operator reads.
    expect(document.body.textContent).not.toMatch(/\bat \w+ \(/)
  })

  it('says the upload failed, and does not claim the product took the file', async () => {
    const user = userEvent.setup({ delay: null })
    server.use(
      http.post('/v1/video/upload', () =>
        HttpResponse.json({ error: 'no_active_bundle' }, { status: 409 }),
      ),
    )

    const view = await renderUpload()
    await chooseAndStart(user, view.container)

    // The file is not accepted, and the page does not say it is.
    expect(await screen.findByText(/failed/i)).toBeInTheDocument()
    expect(screen.queryByText('Accepted by the product')).not.toBeInTheDocument()
    // The failure is a sentence an operator can read, and it is not the body.
    expect(document.body.textContent).not.toContain('{"error"')
    expect(document.body.textContent).not.toMatch(/\bat \w+ \(/)
  })

  it('shows nothing that has been uploaded before anything is uploaded', async () => {
    // The empty state of this page is a file list that holds nothing, and the
    // point is that it holds nothing SAYED: no result, and no count that a
    // reader could take for a measurement.
    await renderUpload()

    expect(
      screen.getByRole('button', { name: /Choose video file/ }),
    ).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Start Upload' })).not.toBeInTheDocument()
    expect(screen.queryByText('Accepted by the product')).not.toBeInTheDocument()
    expect(screen.queryByText(/YouTube ingested/)).not.toBeInTheDocument()
    // And no bare zero, which is the most expensive kind of lie in an
    // instrument panel.
    expect(screen.queryByText('0')).not.toBeInTheDocument()
  })

  it('removes the file the operator discarded', async () => {
    // The operator's own list, and the operator's own decision: a file they
    // put in the box and then took out is not uploaded behind their back.
    const user = userEvent.setup({ delay: null })
    const spy = vi.spyOn(globalThis, 'fetch')

    const view = await renderUpload()
    await user.upload(fileInput(view.container), CLIP)
    expect(await screen.findByText('loading-bay.mp4')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Remove' }))

    expect(screen.queryByText('loading-bay.mp4')).not.toBeInTheDocument()
    expect(callsTo(spy, '/v1/video/upload')).toHaveLength(0)
  })

  it('sends the rule text and the site, and shows the rule the product answered with', async () => {
    const user = userEvent.setup({ delay: null })
    let body: unknown = null
    server.use(
      http.post('/v1/rules/from-text', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json({ rule: 'rule: person at entrance\n  when: motion' })
      }),
    )

    await renderUpload()
    await user.type(
      screen.getByPlaceholderText(/person with bag at entrance/),
      'person with bag at entrance',
    )
    await user.click(screen.getByRole('button', { name: 'Compile' }))

    expect(await screen.findByText(/Compiled rule/)).toBeInTheDocument()
    // The body is the one the product's route expects, and the site is in it:
    // a rule compiled for no site is a rule for nowhere.
    expect(body).toEqual({ text: 'person with bag at entrance', siteId: 'site-demo' })
  })

  it.fails('hands the product\'s refusal on when the UPLOAD is refused', async () => {
    // The YouTube path runs its failure through `describeError`, which reads
    // the refusal out of the body. The upload path does not: it throws its own
    // 'Upload failed' and throws the body's words away, so the operator is
    // told that something broke and not what the product said. The refusal
    // IS the thing the operator can act on, and the rule every other page
    // follows is the expectation below -- it fails against the page as it is
    // written, and it is here so the fix cannot land unnoticed.
    const user = userEvent.setup({ delay: null })
    server.use(
      http.post('/v1/video/upload', () =>
        HttpResponse.json({ error: 'site_not_assigned' }, { status: 403 }),
      ),
    )

    const view = await renderUpload()
    await chooseAndStart(user, view.container)

    expect(await screen.findByText('site_not_assigned')).toBeInTheDocument()
  })

  it.fails('says the site is unknown rather than uploading to an empty site', async () => {
    // The principal carries no site scope, so there is no site to ingest
    // into, and the page has to say so in the product's own vocabulary. It
    // does not: it takes the empty string for the site and posts the file
    // to a place that does not exist, and the operator is told nothing.
    server.use(
      http.get('/v1/principal', () => HttpResponse.json({ ...principal, siteScope: [] })),
    )
    const user = userEvent.setup({ delay: null })
    const spy = vi.spyOn(globalThis, 'fetch')

    const view = await renderUpload()
    await chooseAndStart(user, view.container)

    // The page must say the install has no site to ingest into...
    expect(await screen.findByText('site_not_assigned')).toBeInTheDocument()
    // ...and it must not have posted the file to a site that does not exist.
    expect(callsTo(spy, '/v1/video/upload')).toHaveLength(0)
  })

  it.fails('says the principal was refused, rather than uploading to an empty site', async () => {
    // The same defect from the other side. The principal is the only thing
    // that knows the site, and a principal that could not be read leaves the
    // page with no site and no way to say so: the region has no error state
    // at all, and the upload would go to an empty site.
    server.use(
      http.get('/v1/principal', () =>
        HttpResponse.json({ error: 'site_not_assigned' }, { status: 403 }),
      ),
    )
    const spy = vi.spyOn(globalThis, 'fetch')

    const view = renderWithProviders(<UploadPage />)
    // Wait for the reader to have given up, so the absence below is the
    // page's answer and not the reader's patience.
    await waitFor(
      () => expect(view.queryClient.getQueryState(['principal'])?.status).toBe('error'),
      GAVE_UP_WAIT,
    )

    expect(
      await screen.findByText(/site_not_assigned/, undefined, GAVE_UP_WAIT),
    ).toBeInTheDocument()
    expect(callsTo(spy, '/v1/video/upload')).toHaveLength(0)
  })

  it('gives the file input a label, so it is announced by more than its type', async () => {
    // The chooser is a button, and the input behind it is a real control that
    // used to have no label: it was announced as "file" and nothing else. The
    // repository's own rule is that every control has a real accessible name,
    // and the input now carries an `aria-label` that says what it is for.
    const view = await renderUpload()

    const results = await axe(view.container)
    expect(results).toHaveNoViolations()
  })
})
