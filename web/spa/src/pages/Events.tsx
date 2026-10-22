import { OperationsPage } from './Operations'

// The events page.
//
// It is a SCROLL TARGET and not a page. The record used to be a page of its
// own with a search box of its own, and the Cockpit had a second search box
// over a second query -- so a console with one record had two searches, and
// the two answers did not have to agree.
//
// The record is now the middle of the operations page's column, over the same
// index and the same query the command palette uses, and this route answers
// with the same page scrolled to the record.
export function EventsPage() {
  return <OperationsPage focus="record" />
}
