package api_test

// This file holds the tests for the evidence *content* route's authorization and
// its ledger row. `handlers_test.go` and `api_test.go` belong to another
// participant, so everything here is additive and lands in a file of its own.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/audit"
	"github.com/Sergey-Bar/vision/internal/identity"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// The role table says a viewer may read evidence metadata and not take the bytes,
// and it says an operator may. The route said otherwise: it gated the raw stream
// on `evidence.read`, which `viewer` holds, so both `evidence.fetch` and
// `evidence.export` granted nothing anywhere in the tree and a read-only observer
// could stream raw video out of the filesystem.
//
// This is the authorization matrix for that one route, stated in the roles rather
// than in route names, because the roles are the claim being tested. Each half
// gets its own tenant, because the two halves differ only in the role and a
// principal holding two roles tests nothing.
func TestTheEvidenceContentRouteIsGatedOnFetchNotRead(t *testing.T) {
	t.Run("the roles grant fetch to some and not to others", func(t *testing.T) {
		// The claim being tested is a claim about the role table, so it is
		// asserted against the role table. `identity` owns those, and this is
		// here because it is the reason the route changed.
		for _, tc := range []struct {
			role  identity.Role
			hasIt bool
		}{
			{identity.RoleViewer, false},
			{identity.RoleOperator, true},
			{identity.RoleSecurityManager, true},
			{identity.RoleTenantAdmin, true},
		} {
			got := false
			for _, c := range identity.CapabilitiesFor(tc.role) {
				if c == identity.CapEvidenceFetch {
					got = true
				}
			}
			if got != tc.hasIt {
				t.Errorf("role %s holds evidence.fetch = %v, want %v", tc.role, got, tc.hasIt)
			}
		}
	})

	t.Run("a viewer is refused", func(t *testing.T) {
		s, tenant, site := server(t)
		// The bootstrap administrator holds identity.* but not evidence.fetch, so
		// testing "a principal without it" against the administrator asserts
		// nothing. Reduce it to the least-privileged role that can still sign in.
		reduceToViewer(t, s)
		evID := seedEvidence(t, tenant, site, "viewer-can-not-have-this")

		rec := call(t, s, http.MethodGet, "/v1/evidence/"+evID+"/content", nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("a viewer fetched raw evidence: the route returned %d, want 403.\n"+
				"The role table grants viewer evidence.read and not evidence.fetch, so a "+
				"route gated on evidence.read makes evidence.fetch grant nothing and lets a "+
				"read-only observer stream the bytes out of the filesystem: %s",
				rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "viewer-can-not-have-this") {
			t.Error("the refusal carried the evidence body. A capability check that runs " +
				"after the read is a check that has already paid the cost")
		}
	})

	t.Run("a principal holding it is served", func(t *testing.T) {
		// The bootstrap administrator is a tenant_admin, which the case above
		// shows holds evidence.fetch. This half is here so the refusal above is
		// not a route that refuses everybody: a gate nobody can pass is
		// indistinguishable from a correct one until an operator needs the
		// evidence.
		s, tenant, site := server(t)
		evID := seedEvidence(t, tenant, site, "the-admins-own-evidence")

		rec := call(t, s, http.MethodGet, "/v1/evidence/"+evID+"/content", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("a principal holding evidence.fetch was refused %d: %s", rec.Code, rec.Body)
		}
		if !strings.Contains(rec.Body.String(), "the-admins-own-evidence") {
			t.Errorf("the fetch did not return the bytes: %q", rec.Body.String())
		}
	})
}

// Releasing evidence bytes is the one action on this surface an incident
// responder asks about afterwards, and `audit.ActionEvidenceFetched` was declared
// in the ledger and never appended to by anything. Raw evidence could leave a
// building with no row naming who took it, which site, or which bytes.
func TestFetchingEvidenceIsWrittenToTheLedger(t *testing.T) {
	s, tenant, site := server(t)
	evID := seedEvidence(t, tenant, site, "bytes-that-were-taken")

	rec := call(t, s, http.MethodGet, "/v1/evidence/"+evID+"/content", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the fetch returned %d: %s", rec.Code, rec.Body)
	}

	var targetType, targetID, resultHash, actor, scope string
	if err := testutil.Pool(t).QueryRow(ctx(), `
		SELECT target_type, target_id, result_state_hash, actor, site_scope::text
		  FROM audit_ledger
		 WHERE action = $1 AND target_id = $2
		 ORDER BY sequence DESC LIMIT 1`,
		audit.ActionEvidenceFetched, evID).
		Scan(&targetType, &targetID, &resultHash, &actor, &scope); err != nil {
		t.Fatalf("no %q row was written for the fetch. Streaming evidence leaves the "+
			"building, and an exfiltration review has nothing to read: %v",
			audit.ActionEvidenceFetched, err)
	}

	if targetType != "evidence" {
		t.Errorf("the row's target_type is %q, want evidence", targetType)
	}
	// The digest of the bytes that were served, not of the row. The row's digest
	// is what should have been on disk; this route is where that stops being a
	// claim.
	if resultHash != rec.Header().Get("X-Content-SHA256") {
		t.Errorf("the row records %q but the response served %q. The ledger has to name "+
			"the bytes that actually left, or it is a record of an intention",
			resultHash, rec.Header().Get("X-Content-SHA256"))
	}
	if actor != "operator" {
		t.Errorf("the row's actor is %q, want the configured principal's name", actor)
	}
	if scope == "" || scope == "{}" {
		t.Errorf("the row carries site scope %q, so it does not say which site's "+
			"evidence was taken", scope)
	}
}

// A refused fetch must not leave a row saying evidence was released. The row
// records a release of bytes; there were none.
func TestARefusedEvidenceFetchLeavesNoLedgerRow(t *testing.T) {
	s, tenant, site := server(t)
	reduceToViewer(t, s)
	evID := seedEvidence(t, tenant, site, "not-released")

	if rec := call(t, s, http.MethodGet, "/v1/evidence/"+evID+"/content", nil); rec.Code == http.StatusOK {
		t.Fatal("a viewer fetched evidence; the capability change did not take")
	}

	var n int
	if err := testutil.Pool(t).QueryRow(ctx(), `
		SELECT count(*) FROM audit_ledger WHERE action = $1 AND target_id = $2`,
		audit.ActionEvidenceFetched, evID).Scan(&n); err != nil {
		t.Fatalf("count the ledger: %v", err)
	}
	if n != 0 {
		t.Errorf("a refused fetch wrote %d %q row(s). The row records that evidence "+
			"bytes were released to a principal, and a refusal released none",
			n, audit.ActionEvidenceFetched)
	}
}
