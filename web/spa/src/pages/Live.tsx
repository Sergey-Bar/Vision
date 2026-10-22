import { OperationsPage } from './Operations'

// The live page.
//
// It is a SCROLL TARGET and not a page. The wall used to be a page of its
// own, with its own header, its own filter row and its own copy of the
// camera list, and the operator had to choose between "the picture" and "the
// queue" rather than having both.
//
// The wall is now the left half of the operations page, and this route
// answers with the same page scrolled to the wall -- so a bookmark to
// `/console/live` and the rehearsal's URL list both keep working, and neither
// of them lands the operator on a second version of the truth.
export function LivePage() {
  return <OperationsPage focus="wall" />
}
