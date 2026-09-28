package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

func asGate(err error, target **bundles.ErrGateNotSatisfied) bool {
	g, ok := err.(*bundles.ErrGateNotSatisfied)
	if ok {
		*target = g
	}
	return ok
}

// ctxContext aliases context.Context for the reader interfaces, so their
// signatures read as `ctx context.Context` without every implementation needing
// the import spelled out.
type ctxContext = context.Context
