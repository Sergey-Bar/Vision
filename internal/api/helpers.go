package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strconv"

	"github.com/Sergey-Bar/Ai-Captain/internal/bundles"
	"github.com/Sergey-Bar/Ai-Captain/internal/identity"
)

// Small helpers kept in one file so the handler bodies read as handler bodies.

// primarySiteOf is the site a principal-scoped read is bound to.
//
// It is the first site in the principal's scope, and deliberately that rather
// than "every site they can see": since migration 21 `site_isolation` is
// RESTRICTIVE, so a read that names no site sees nothing at all. The product is
// single-site per install and `config.validate` already refuses a start with no
// `SP_SITE_ID`, so the first entry is the whole scope in practice — and when a
// principal is assigned to several, the listing narrows rather than widening,
// which is the direction a mistake in this function has to fail.
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

func decodeBase64(s string) ([]byte, error) {
	// Standard first, then URL-safe: an operator's tooling produces both and
	// refusing the second would be a support ticket rather than a control.
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawURLEncoding.DecodeString(s)
}

func jsonUnmarshal(raw []byte, dst any) error { return json.Unmarshal(raw, dst) }

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func copyStream(dst io.Writer, src io.Reader, buf []byte) (int64, error) {
	return io.CopyBuffer(dst, struct{ io.Reader }{src}, buf)
}

// asGate reports whether an error is a bundle activation gate refusal.
//
// errors.As, not a type assertion: the gate's error is wrapped on its way out
// of the store, and a type assertion against the wrapper's dynamic type would
// miss it and hand the caller the generic "activation failed" instead of the
// sentence that names which condition was not met. That sentence is the whole
// point of the gate, so losing it is losing the feature.
func asGate(err error, target **bundles.ErrGateNotSatisfied) bool {
	return errors.As(err, target)
}

// ctxContext aliases context.Context for the reader interfaces, so their
// signatures read as `ctx context.Context` without every implementation needing
// the import spelled out.
type ctxContext = context.Context
