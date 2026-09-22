// signature_typed_handler.go, the HTTP door for the typed-name attestation (DIB-386).
//
// It is a NEW route rather than a widening of POST /api/v1/pos/signatures because the old route's
// body (purpose, ref_type, ref_id) describes a capture that no longer exists: nothing in it says who
// signed or that they agreed. Registered from main.go's wiring block alongside the provider swap, so
// the door and the default provider land together.
package pos

import (
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"net/http"
)

// RegisterTypedSignatureRoutes self-registers the typed-attestation route (RequireStaff, a staff
// member witnesses the attestation, exactly as they do at the counter today).
func (h *Handler) RegisterTypedSignatureRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("POST /api/v1/pos/signatures/typed", g.RequireStaff(h.staff(h.captureTyped)))
}

func (h *Handler) captureTyped(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in TypedAttestation
	if !decodeJSON(w, r, &in) {
		return
	}
	ref, err := h.svc.CaptureTypedSignature(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"signature_ref": ref,
		"provider":      ProviderTypedNameV1,
	})
}
