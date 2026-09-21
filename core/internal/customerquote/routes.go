package customerquote

// The route surface. Route spellings follow the modelled shapes of the
// reviewed design; where the OpenAPI document that lands differs in
// spelling, the document is the arbiter and the authority semantics here are
// not the document's to reshape. The tenant operations register under the
// tenant guard (the scope arrives as an argument, stamped by the middleware,
// never read from a header); the public operations register under the public
// wrapper with x-role public and empty security, and on them the token in
// the path is the whole credential: no verification, no org resolution, and
// X-Org with zero effect (ADR-0020's public leg).

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// maxBodyBytes bounds every request body this module reads.
const maxBodyBytes = 1 << 20

// RegisterRoutes registers the customer-quote surface on mux through the
// settled Guards signature.
func RegisterRoutes(mux *http.ServeMux, g *guards.Guards, svc *Service) {
	create := g.RequireTenant(func(w http.ResponseWriter, r *http.Request, scope authz.TenantScope) {
		var in CreateInput
		if !readJSON(w, r, &in) {
			return
		}
		out, err := svc.Create(r.Context(), Scope{OrgID: scope.OrgID, TenantID: scope.TenantID}, in)
		writeResult(w, out, err, http.StatusCreated)
	})
	mux.Handle("POST /api/v1/tenant/customer-quotes", create)

	mux.Handle("PATCH /api/v1/tenant/customer-quotes/{id}", g.RequireTenant(
		func(w http.ResponseWriter, r *http.Request, scope authz.TenantScope) {
			var in UpdateInput
			if !readJSON(w, r, &in) {
				return
			}
			out, err := svc.Update(r.Context(), Scope{OrgID: scope.OrgID, TenantID: scope.TenantID}, r.PathValue("id"), in)
			writeResult(w, out, err, http.StatusOK)
		}))

	mux.Handle("POST /api/v1/tenant/customer-quotes/{id}/send", g.RequireTenant(
		func(w http.ResponseWriter, r *http.Request, scope authz.TenantScope) {
			out, err := svc.Send(r.Context(), Scope{OrgID: scope.OrgID, TenantID: scope.TenantID}, r.PathValue("id"))
			writeResult(w, out, err, http.StatusOK)
		}))

	mux.Handle("POST /api/v1/tenant/customer-quotes/{id}/revise", g.RequireTenant(
		func(w http.ResponseWriter, r *http.Request, scope authz.TenantScope) {
			out, err := svc.Revise(r.Context(), Scope{OrgID: scope.OrgID, TenantID: scope.TenantID}, r.PathValue("id"))
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
