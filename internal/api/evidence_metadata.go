package api

import (
	"errors"
	"net/http"

	"github.com/Sergey-Bar/vision/internal/identity"
	"github.com/Sergey-Bar/vision/internal/incidents"
)

// handleEvidenceMetadata reads ONE evidence row's metadata.
//
// The BYTES are served by the content route, and the incident's evidence list
// names the row, but the detail page had nowhere to read the row itself from
// and rendered a dash for every field. A dash is a dash and not a measurement
// of nothing — but a page that shows four dashes is a page that shows
// nothing, and the digest in particular is the one field an operator has to
// be able to read before they trust a clip.
//
// The site scope is the PRINCIPAL's and is applied in the query. A row at a
// site the principal may not see is a row that does not exist for them, and
// the answer is the same 404 either way: a different one is a disclosure.
func (s *Server) handleEvidenceMetadata(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if len(p.SiteScope) == 0 {
		WriteError(w, http.StatusNotFound, "not_found", "No such evidence.", requestID(r))
		return
	}
	view, err := d.Incidents.Evidence(r.Context(), p.TenantID, r.PathValue("evidenceId"), p.SiteScope)
	if err != nil {
		if errors.Is(err, incidents.ErrNoSuchEvidence) {
			WriteError(w, http.StatusNotFound, "not_found",
				"No such evidence, or you are not assigned to its site.", requestID(r))
			return
		}
		WriteError(w, http.StatusInternalServerError, "not_read", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, view)
}
