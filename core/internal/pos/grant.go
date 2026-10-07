package pos

// grant.go, the Counter Till payment grant read (GET /api/v1/pos/payment-grant): one question a
// client asks BEFORE it offers to take a payment. Does the signed-in user hold the grant the till's
// payment routes require?
//
// WHY A READ AND NOT A TRY. The payment routes (a till sale, a payment on a converted quote's order)
// answer a caller without the grant with the gate's 403, but only after the cashier has built the
// payment. The Quote Desk, where a person with a Quote Desk grant and no Counter Till grant (an
// outside salesperson) is ordinary, needs to know first so it can leave the action out rather than
// offer a button that is refused. There was no session or capabilities read to carry the answer:
// the staff reads that name the caller (the assistant's own allowance) say nothing of grants.
//
// THE ANSWER IS THE GATE'S AND THE DOOR'S. The reader behind it asks the role gate the question the gate asks
// of a request (authz.RoleGate.Holds, the same walk, the whole row of the caller's roles, the
// platform roles, the dealer's configuration), for the access the document annotates on the payment
// routes themselves, so a change to either moves this answer with it. It is a fact about the
// caller's grants and role and nothing else: not a till session, not a quote, not a policy of the dealer's,
// and not an authorisation, the routes still gate every call.
//
// WHO MAY ASK. Anyone holding Counter Till or Quote Desk at read: the surfaces whose people have
// a reason to ask. A caller with neither meets the gate's 403 before the question is reached; a
// caller with one of them and without payment rights is answered false, a 200, not a refusal,
// because "no" is the answer they came for.

import (
	"context"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// PaymentGrant is the answer: whether the caller holds the grant the till's payment routes need.
type PaymentGrant struct {
	CanTakePayment bool `json:"can_take_payment"`
}

// GrantReader answers the grant question for one caller. The composition root builds it over the
// role gate; the narrow interface keeps this package off the gate's internals and lets a test
// answer without a database.
type GrantReader interface {
	HoldsPayment(ctx context.Context, platformUserID, orgID string, caller identity.StaffContext) (bool, error)
}

// WithGrantReader wires the grant read behind the route. A handler without one answers the route
// unavailable, never "no": a read that cannot be made must not read as a refusal of payment.
func (h *Handler) WithGrantReader(g GrantReader) *Handler { h.grants = g; return h }

// RegisterGrantRoute registers the grant read. It is separate from RegisterRoutes because the role
// gate it reads is built in the composition root beside the registrar, after the module wiring.
func (h *Handler) RegisterGrantRoute(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/pos/payment-grant", g.RequireStaff(h.staff(h.paymentGrant)))
}

func (h *Handler) paymentGrant(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if h.grants == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "the payment grant could not be read")
		return
	}
	holds, err := h.grants.HoldsPayment(r.Context(), caller.PlatformUserID, caller.OrgID, identity.FromAuthority(caller.StaffAuthority))
	if err != nil {
		h.log.Error("pos: payment grant read failed", "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusServiceUnavailable, "the payment grant could not be read")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, PaymentGrant{CanTakePayment: holds})
}
