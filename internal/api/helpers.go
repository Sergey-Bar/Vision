package api

import (
	"io"
	"strconv"

	"github.com/Sergey-Bar/vision/internal/identity"
)

// primarySiteOf is the principal's own site.
//
// A principal with no scope yields "", which the RLS layer reads as "no site"
// and therefore as "nothing". That is right: an operator assigned to no site is
// not assigned to all of them, and the routes that list per-site resources
// already filter on `CanSeeSite` for the same reason.
func primarySiteOf(p identity.Principal) string {
	if len(p.SiteScope) == 0 {
		return ""
	}
	return p.SiteScope[0]
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func copyStream(dst io.Writer, src io.Reader, buf []byte) (int64, error) {
	return io.CopyBuffer(dst, struct{ io.Reader }{src}, buf)
}
