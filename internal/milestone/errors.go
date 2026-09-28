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

// describe renders a route for an error message or a probe report.
func describe(r Route) string {
	return fmt.Sprintf("%s %s", r.Method, r.Path)
}
