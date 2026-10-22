import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithProviders } from '@/test/render'
import { Sidebar } from './Sidebar'

// The navigation.
//
// The groups are the operator's QUESTIONS, in the order they are asked:
// watch, decide, review, configure. An operator who has never seen the
// console can find the answer to a question by asking the question.
//
// The operations page is rendered at three paths -- the screen itself, the
// wall and the record -- and the rail lights ONE entry for all three. A rail
// that names one of them is a rail that cannot say where you are.
//
// The paths in these tests are the ROUTER's paths, with no `/console` prefix,
// because that is the space the router works in: the console is served under
// `/console` and the router's basename is what puts it back in front of a
// rendered link. A test that used the prefixed paths would have passed against
// a rail whose links were all `/console/console/...` and which never lit
// anything, and that is exactly what happened.

function renderRail(currentPath: string) {
  return renderWithProviders(
    <Sidebar currentPath={currentPath} open={false} onClose={() => undefined} />,
    { route: currentPath },
  )
}

describe('the rail', () => {
  it('is grouped by the operator\'s questions', () => {
    // Four groups, and a flat list of twelve entries sorted alphabetically
    // asks the reader to know the product's vocabulary first.
    renderRail('/')
    for (const group of ['Watch', 'Decide', 'Review', 'Configure']) {
      expect(screen.getByText(group)).toBeInTheDocument()
    }
  })

  it('marks the item that is showing', () => {
    // The active item is marked for a screen reader as well as for the eye.
    // A rail that only changes colour is a rail a screen reader cannot read.
    renderRail('/incidents')
    const current = document.querySelectorAll('[aria-current="page"]')
    expect(current).toHaveLength(1)
    expect(current[0]).toHaveTextContent('Incidents')
  })

  it('lights the operations item for all three of its paths', () => {
    // The operations page is rendered at the screen, at the wall and at the
    // record. Three entries pointing at one page is a navigation that cannot
    // say where you are -- so it is ONE entry, lit for all three.
    for (const path of ['/', '/live', '/events']) {
      const { unmount } = renderRail(path)
      const current = document.querySelectorAll('[aria-current="page"]')
      expect(current).toHaveLength(1)
      expect(current[0]).toHaveTextContent('Operations')
      unmount()
    }
  })

  it('lights nothing for a path the console does not serve', () => {
    // An item lit at a path it does not own is a rail that lies.
    renderRail('/nothing-here')
    expect(document.querySelectorAll('[aria-current="page"]')).toHaveLength(0)
  })

  it('renders links the router can resolve', () => {
    // The rendered href is the router's path with the basename in front of
    // it, and NOT the path with the basename in front of it twice.
    //
    // This is the assertion the unit tests could not make while the entries
    // carried the prefix themselves: the href is a function of the router's
    // basename, so the test has to render through a router that HAS one.
    const { container } = renderWithProviders(
      <Sidebar currentPath="/" open={false} onClose={() => undefined} />,
      { route: '/' },
    )
    const hrefs = Array.from(container.querySelectorAll('a')).map((a) =>
      a.getAttribute('href'),
    )
    expect(hrefs).toContain('/incidents')
    for (const href of hrefs) {
      expect(href).not.toContain('/console/console')
    }
  })
})
