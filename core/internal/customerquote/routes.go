package customerquote

// The route surface (the registration batch, HH-3.6 C5). The document that
// landed (ADR 0021, Decision 2) carries exactly three customer-quote
// operations, and this registrar registers exactly those three: the dealer
// desk's send (POST /api/v1/customer-quotes/{id}/send, x-role requireStaff)
// and the public pair credentialed by the path token alone (GET and POST
// /api/public/v1/customer-quotes/{token}(/accept), x-role public with empty
// security). The tenant-side lifecycle (create, update, the tenant-shaped
// send, revise) keeps its service seams with no route: the document carries
// no tenant customer-quote operations, an undocumented registration is what
// the composition point's parity discipline exists to refuse, and their
// routes arrive, if ever, with their own document half (the posture
// RecordChangeRequest already carries). On the public leg the token in the
// path is the whole credential: no verification, no org resolution, and
// X-Org with zero effect (ADR-0020's public leg).

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
)

// maxBodyBytes bounds every request body this module reads.
const maxBodyBytes = 1 << 20

// RegisterRoutes registers the customer-quote surface on mux through the
// settled Guards signature: the send through the staff constructor, the
// public pair through the public wrapper.
func RegisterRoutes(mux *http.ServeMux, g *guards.Guards, svc *Service) {
	// The send is a dealer desk action (ADR 0021, Decision 2): any
	// authenticated staff member with an active grant in the request org
	// (requireStaff, the constructor the dealer share-link mints use); the
	// issuing contractor is the row's tenant, never a claim the caller
	// holds. The caller arrives as the guard's argument, the settled
	// Phase C shape. The stamp/linker bridge the carried modules need (the
	// StaffContext stamped onto the request, the ERP users.id linker) has
	// no consumer here: this module is greenfield, nothing downstream
	// reads a stamped request context, and the frozen customer_quote
	// schema records no sender column to link an id into, so the send
	// records no per-caller fact beyond the guard's own admission
	// decision. Should the audit re-point ever require send actorship,
	// the caller is in hand at this seam, and the column it would land in
	// is HH-3.7's question.
	mux.Handle("POST /api/v1/customer-quotes/{id}/send", g.RequireStaff(
		func(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
			out, err := svc.StaffSend(r.Context(), caller.OrgID, r.PathValue("id"))
			writeResult(w, out, err, http.StatusOK)
		}))

	// The public pair: the opaque token in the path is the credential.
	mux.Handle("GET /api/public/v1/customer-quotes/{token}", g.Public(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			out, err := svc.PublicView(r.Context(), r.PathValue("token"))
			writeResult(w, out, err, http.StatusOK)
		})))

	mux.Handle("POST /api/public/v1/customer-quotes/{token}/accept", g.Public(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			var in AcceptRequest
			if !readJSON(w, r, &in) {
				return
			}
			out, err := svc.PublicAccept(r.Context(), r.PathValue("token"), in)
			writeResult(w, out, err, http.StatusOK)
		})))
}

// readJSON decodes one bounded JSON body; a failure is the 400 shape family.
func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	if err := dec.Decode(dst); err != nil {
		writeError(w, &Error{Status: http.StatusBadRequest, Message: "request body did not parse"})
		return false
	}
	return true
}

// writeResult writes the module's one success or refusal shape. Refusals
// carry the Error envelope with the pinned family body.
func writeResult(w http.ResponseWriter, out any, err error, successStatus int) {
	if err != nil {
		var re *Error
		if !errors.As(err, &re) {
			re = &Error{Status: http.StatusServiceUnavailable, Message: MsgBusy}
		}
		writeError(w, re)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(successStatus)
	_ = json.NewEncoder(w).Encode(out)
}

func writeError(w http.ResponseWriter, e *Error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": e.Message})
}
