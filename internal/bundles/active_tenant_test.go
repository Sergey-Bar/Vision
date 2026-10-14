package bundles_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/bundles"
	"github.com/Sergey-Bar/vision/internal/dbx"
)

// The core reads the active bundle for ITS tenant. This guards why that matters,
// without a database: an empty tenant is refused by dbx's no-tenant guard before a
// single row is read, so a caller passing "" gets that hard refusal and never
// ErrNoActiveBundle.
//
// This is the root of the defect that stopped the core booting. The startup
// dispatch-check read the active bundle with an EMPTY tenant, so a fresh
// deployment -- one with no active bundle, a supported state the check is meant to
// treat as a clean "nothing to do yet" start -- instead hit this refusal and
// refused to start. The fix passes the configured tenant; this test is the
// assertion that keeps the empty-tenant case a loud refusal rather than a silent
// clean start, which is the pair a caller has to tell apart.
func TestAnActiveBundleReadNeedsATenantAndSaysSo(t *testing.T) {
	svc := &bundles.Service{Pool: &dbx.Pool{}}
	_, _, _, err := svc.ActiveBundle(context.Background(), "")
	if err == nil {
		t.Fatal("reading the active bundle with an empty tenant was allowed; the core's " +
			"dispatch-check passes its own tenant precisely because this is refused")
	}
	// It is the no-tenant refusal, not "no active bundle": only the second would
	// let a caller start cleanly on a fresh install, and letting the first pass as
	// the second is the whole bug.
	if !errors.Is(err, dbx.ErrNoTenant) {
		t.Errorf("the empty-tenant read failed with %v, want the no-tenant refusal "+
			"(dbx.ErrNoTenant); if it read as ErrNoActiveBundle the boot would treat a "+
			"miswired read as a clean start", err)
	}
	if strings.Contains(err.Error(), "no active bundle") {
		t.Errorf("the empty-tenant read read as ErrNoActiveBundle: %v", err)
	}
}
