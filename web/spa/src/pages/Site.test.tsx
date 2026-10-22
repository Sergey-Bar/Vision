import { describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import { server } from '@/test/server'
import { renderWithProviders } from '@/test/render'
import { SitePage } from './Site'

// The site.
//
// Two DIFFERENT facts, drawn side by side:
//
//   the DRAWING   the operator's own site model -- the YAML they walked the
//                 site with, served back by the product
//   the INVENTORY what the platform has been told about
//
// A camera can be in one and not the other, and the difference is exactly the
// thing an operator is looking for. So the tests here are about the page
// keeping the two apart -- and about the page not losing the drawing when the
// inventory is empty.

const SITE = {
  siteId: 'site-demo',
  zones: [
    {
      id: 'zone-loading',
      type: 'restricted',
      name: 'Loading bay',
      polygon: [
        { x: 0, y: 0 },
        { x: 100, y: 0 },
        { x: 100, y: 100 },
        { x: 0, y: 100 },
      ],
    },
  ],
  cameras: [{ id: 'cam-1', zoneIds: ['zone-loading'], position: { x: 50, y: 50 } }],
  routes: [
    {
      id: 'route-1',
      points: [
        { x: 0, y: 0 },
        { x: 100, y: 100 },
      ],
      allowedDirection: 'both',
    },
  ],
}

describe('the site page', () => {
  it('draws the operator\'s own site model', async () => {
    server.use(http.get('/v1/site', () => HttpResponse.json(SITE)))

    renderWithProviders(<SitePage />)

    // The subtitle carries the drawing's own counts: the zones and the
    // cameras are the model's, not the platform's.
    expect(await screen.findByText('site-demo · 1 zones · 1 cameras')).toBeInTheDocument()
    expect(screen.getAllByText('Loading bay').length).toBeGreaterThan(0)
    expect(screen.getByText('restricted')).toBeInTheDocument()
    expect(screen.getAllByText('cam-1').length).toBeGreaterThan(0)
    expect(screen.getByText('zone-loading')).toBeInTheDocument()
  })

  it('says the platform has been told about no camera', async () => {
    // The platform's inventory is empty while the drawing has a camera on it.
    // The two are different facts and the page has to say which one is empty.
    server.use(http.get('/v1/site', () => HttpResponse.json(SITE)))
    server.use(http.get('/api/cameras', () => HttpResponse.json({ cameras: [] })))

    renderWithProviders(<SitePage />)

    expect(
      await screen.findByText(
        /The platform has been told about no camera\. That is not the same as the site having none/,
      ),
    ).toBeInTheDocument()
    // The drawing is untouched by the empty inventory: it is the operator's
    // own, and it is the only thing that can put a camera on the map.
    expect(screen.getAllByText('cam-1').length).toBeGreaterThan(0)
  })

  it('lists the platform\'s inventory when it has one', async () => {
    server.use(http.get('/v1/site', () => HttpResponse.json(SITE)))
    server.use(
      http.get('/api/cameras', () =>
        HttpResponse.json({
          cameras: [{ id: 'cam-9', siteId: 'site-demo', name: 'Gate', status: 'online' }],
        }),
      ),
    )

    renderWithProviders(<SitePage />)

    expect(await screen.findByText('The inventory the platform has')).toBeInTheDocument()
    expect(screen.getByText('cam-9')).toBeInTheDocument()
    expect(
      screen.queryByText(/The platform has been told about no camera/),
    ).not.toBeInTheDocument()
  })

  it('says the install has no site model when the drawing cannot be read', async () => {
    server.use(
      http.get('/v1/site', () => HttpResponse.json({ error: 'not_configured' }, { status: 503 })),
    )

    renderWithProviders(<SitePage />)

    expect(
      await screen.findByText(/This install has no site model, so there is no site to draw/),
    ).toBeInTheDocument()
    // No drawing is drawn from a model the console does not have.
    expect(screen.queryByText('The inventory the platform has')).not.toBeInTheDocument()
  })
})
