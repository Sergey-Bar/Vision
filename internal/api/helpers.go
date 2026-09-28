package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strconv"

	"github.com/Sergey-Bar/Ai-Captain/internal/bundles"
)

// Small helpers kept in one file so the handler bodies read as handler bodies.

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
