package api_test

// The product must not publish a camera inventory it made up.
//
// The product used to answer `GET /api/cameras` with eight cameras at three
// sites -- "North Campus", "West Warehouse", "Downtown Hub" -- that no
// installation has ever had.
//
// The camera inventory belongs to the VMS, which is the device that has the
// cameras, and to the platform's own `cameras` table, which is what the
// platform has been told about. The product holds neither a camera list nor a
// site list of its own, and a route that answers with an invented one is worse
// than a route that is absent: the absence is visible and the invention is
// not.
//
// So the route is gone, and this test is what says so.

import (
	"net/http"
	"testing"
)

func TestTheProductPublishesNoCameraInventory(t *testing.T) {
	s, _, _ := server(t)

	rec := call(t, s, http.MethodGet, "/api/cameras", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/cameras answered %d, want 404: the product holds no camera "+
			"inventory, and an invented one is a list that looks exactly like the real "+
			"one: %s", rec.Code, rec.Body)
	}
}
