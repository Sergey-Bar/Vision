package milestone

import (
	"errors"
	"fmt"
)

// asNotFound is a small helper so IsNotFound reads cleanly and the sentinel
// wiring stays in one place.
func asNotFound(err error, target **NotFoundError) bool {
	return errors.As(err, target)
}

// ErrWriteBack is returned when a bookmark could not be placed.
//
// It is its own sentinel because "the bookmark is not there" and "the feed is
// not there" are different operational states: one means an operator following
// an incident will find nothing, the other means the pipeline is healthy and
// there was simply nothing to read.
var ErrWriteBack = errors.New("milestone: write-back failed")

// ErrClockProbe is returned when the vendor's clock could not be sampled.
//
// Separate from ErrWriteBack because a clock that cannot be read is a *drift
// measurement* that has failed, and a window selected without it is a window
// that is guessed. The distinction is what tells an operator whether to look at
// the network or at the installer.
var ErrClockProbe = errors.New("milestone: clock probe failed")

// StatusError is a response the adapter cannot use.
//
// It is typed rather than a formatted string because a caller has to be able to
// tell three things apart, and each sends an operator somewhere different:
//
//   - the endpoint answered with a status it should not have (this), which means
//     the assumption about *this route* is wrong;
//   - the endpoint could not be reached at all, which means the base URL, the
//     network or the credentials are wrong;
//   - the endpoint does not have this route, which means the vendor's API is not
//     the one this adapter was written against.
//
// A probe that collapses all three reports a wrong base URL as a vendor bug, and
// an integrator who believes that spends a day filing it.
type StatusError struct {
	Route  Route
	Status int
	Body   string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("milestone: %s returned %d: %s", describe(e.Route), e.Status, e.Body)
}

// AsStatus extracts a StatusError from a chain.
func AsStatus(err error) (*StatusError, bool) {
	var se *StatusError
	if errors.As(err, &se) {
		return se, true
	}
	return nil, false
}

// describe renders a route for an error message or a probe report.
func describe(r Route) string {
	return fmt.Sprintf("%s %s", r.Method, r.Path)
}
