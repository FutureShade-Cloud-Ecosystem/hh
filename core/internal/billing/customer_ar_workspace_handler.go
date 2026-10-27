package billing

// customer_ar_workspace_handler.go, the HTTP surface for the customer account workspace.
//
// -- WHAT THIS HANDLER SHIPS, AND WHAT IT DOESN'T -------------------------------------------------
// Two routes:
//   GET  /api/v1/billing/ar/customers/{customer_id}/workspace
//        the one-shot workspace read, one round trip so the panel renders in one paint.
//   POST /api/v1/billing/ar/customers/{customer_id}/notes
//        the activity-tab's "Add a note" writer, the seam behind customer_ar_notes.
//
// The handler holds a WorkspaceStore, never a Repository, by the same discipline the rest
// of the billing module follows: the composition root wires the seam, the handler tests
// can fake it without faking the SQL.
//
// -- GATING ---------------------------------------------------------------------------------------
// Both routes are requireStaff. The workspace carries the customer's financial picture
// (open balance, overdue, credit utilisation), the same one the invoice detail's account
// card carries, which the same register_handler.go header explains: an account position is
// operational, the person about to ring the account is entitled to know. The notes POST is
// staff so a counter clerk can leave a note for the next collections person on the same
// account, the same posture the rest of the AR register follows.
//
// -- WHERE THE AGING COMES FROM ------------------------------------------------------------------
// The 5-bucket aging the workspace header carries is the AR console's own read, by
// credit_service.go's CustomerAging. The workspace handler does not re-bucket the aging,
// for the same reason customer_ar_read.go's header spends thirty lines on: a second copy
// would drift the day one of them changed and the workspace would quote a different number
// from the register. The handler assembles the workspace, the service's Aging() does the
// bucket math, and the workspace inherits the rule.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// WorkspaceCreditSeam is the slice of CreditService the workspace handler needs:
// CustomerAging for the header's 5-bucket breakdown and CustomerActivity for the
// activity feed. A unit-test fake satisfies the interface, so the workspace handler
// runs without a database. Production wires the real CreditService, which already
// implements both methods.
type WorkspaceCreditSeam interface {
	CustomerAging(ctx context.Context, customerID string) (AgingRow, time.Time, bool, error)
	CustomerActivity(ctx context.Context, customerID string, limit int, withPayments bool) ([]ARActivity, error)
}

// WorkspaceHandler serves the customer account workspace. The store seam is the only
// field the handler needs; the route registration lives here too (mount in
// register_handler.go's RegisterRoutes is unchanged).
type WorkspaceHandler struct {
	store WorkspaceStore
	// credit is the optional seam for CustomerAging and CustomerActivity. A unit-test
	// fake that does not care about the 5-bucket reconciliation can leave it nil and the
	// handler skips the header's aging field, which is the same shape the workspace UI
	// shows as "n/a".
	credit WorkspaceCreditSeam
	log    *slog.Logger
	// userLink is the ERP users.id linker the caller stamp carries. Nil stamps authority
	// only; every id-consuming read fails closed.
	userLink identity.UserIDLinker
	// trail says whether a caller holds the payment trail reads (ruling 87); reach holds the
	// customer to the caller's branches (UX-93). Both fail closed when unbound.
	trail PaymentTrailReader
	reach WorkspaceReach
}

// NewWorkspaceHandler wires the handler. store is required; credit is optional (nil
// means the workspace does not fill the aging or activity field, by design for fakes).
func NewWorkspaceHandler(store WorkspaceStore, credit WorkspaceCreditSeam, log *slog.Logger) *WorkspaceHandler {
	if log == nil {
		log = slog.Default()
	}
	return &WorkspaceHandler{store: store, credit: credit, log: log}
}

// RegisterRoutes self-registers the workspace routes. Called from the register handler's
// composition root so the route map stays in one place.
func (h *WorkspaceHandler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/billing/ar/customers/{customer_id}/workspace", g.RequireStaff(h.staff(h.workspace)))
	r.Handle("POST /api/v1/billing/ar/customers/{customer_id}/notes", g.RequireStaff(h.staff(h.addNote)))
}

// Compile-time check: the production CreditService satisfies WorkspaceCreditSeam.
// A signature drift on either side fails the build rather than the route map.
var _ WorkspaceCreditSeam = (*CreditService)(nil)

// staff (the caller-stamp middleware) lives in caller_stamp_workspacehandler.go so the
// stamp pattern stays in one file per handler, the same shape
// caller_stamp_registerhandler.go follows.

// workspace is the GET handler. It composes the header (with the 5-bucket aging when a
// CreditService is wired), the three invoice lists, payments, credit notes, statements,
// notes and the activity feed.
//
// Reconciliation to the penny is checked here. A bucket set that does not sum to OpenCents
// (or whose late buckets do not sum to OverdueCents) is logged and the handler returns
// 500 with the error in the body; the panel cannot be rendered with numbers that do not
// add up, because the moment two consumers compare the workspace to the AR console they
// will quote different numbers to the same customer.
//
// Returns 404 when the account does not exist (header scan returns zero-value), 400 when
// the customer_id fails the uuid alphabet guard, 500 on a database error, and 200 with
// the composed panel otherwise.
func (h *WorkspaceHandler) workspace(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	customerID := strings.TrimSpace(r.PathValue("customer_id"))
	if customerID == "" {
		httpx.Error(w, http.StatusBadRequest, "customer_id is required")
		return
	}
	if !looksLikeUUIDish(customerID) {
		// Same posture as CustomerActivity: a non-uuid that reaches SQL as an uncastable
		// literal comes back as a 500 for what is really "no such account". The handler
		// short-circuits to 400 because the route did name an id, just not a uuid.
		httpx.Error(w, http.StatusBadRequest, "customer_id is not a uuid")
		return
	}

	reachable, err := h.reachable(r.Context(), customerID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if !reachable {
		httpx.Error(w, http.StatusNotFound, "customer not found")
		return
	}
	held, err := h.holdsTrail(r.Context(), caller.PlatformUserID, caller.OrgID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}

	out := CustomerWorkspace{
		InvoicesOpen:     []CustomerWorkspaceInvoice{},
		InvoicesClosed:   []CustomerWorkspaceInvoice{},
		InvoicesDisputed: []CustomerWorkspaceInvoice{},
		Payments:         []CustomerWorkspacePayment{},
		CreditNotes:      []CustomerWorkspaceCreditNote{},
		Statements:       []CustomerWorkspaceStatement{},
		Notes:            []CustomerWorkspaceNote{},
		Activity:         []ARActivity{},
	}
	out.PaymentsWithheld = !held
	hdr, err := h.store.WorkspaceHeader(r.Context(), customerID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if hdr.CustomerID == "" {
		// Header scan returned the zero value: the customer does not exist. 404 is the
		// right answer; 200 with an empty header would be a panel that lies.
		httpx.Error(w, http.StatusNotFound, "customer not found")
		return
	}
	if !held {
		// The last payment's date and amount are the payment trail too (ruling 87).
		hdr.LastPaymentAt = nil
		hdr.LastPaymentCents = 0
	}
	out.Header = hdr

	// The aging field: filled only when a CreditService is wired. The handler does not
	// re-bucket; the service's Aging() does, and the workspace inherits the result.
	if h.credit != nil {
		row, asOf, found, err := h.credit.CustomerAging(r.Context(), customerID)
		if err != nil {
			h.writeErr(w, r, err)
			return
		}
		if found {
			out.Header.Aging = CustomerWorkspaceAging{
				CurrentCents:    row.CurrentCents,
				Days30Cents:     row.Days30Cents,
				Days60Cents:     row.Days60Cents,
				Days90Cents:     row.Days60To90Cents, // the service's 61-to-90 band
				Days90PlusCents: row.Days90PlusCents,
				OpenCents:       row.TotalCents,
				OverdueCents:    row.OverdueCents(),
			}
		}
		out.AsOf = asOf
	} else {
		// No credit service wired: the workspace cannot fill the aging field, but it
		// still returns the header so a screen that wants the rest of the panel renders.
		if hdr.LastPaymentAt != nil {
			out.AsOf = *hdr.LastPaymentAt
		}
	}

	// Reconcile to the penny. The AR console's own read is the source of truth, and the
	// five buckets MUST sum to OpenCents, the four late buckets MUST sum to OverdueCents.
	// A drift is logged and the handler returns 500, the panel cannot be rendered with
	// numbers that do not add up.
	if h.credit != nil && out.Header.OpenCents != 0 && !out.Header.Aging.ReconcilesToThePenny() {
		h.log.Error("workspace aging drift",
			"customer_id", customerID,
			"open_cents", out.Header.Aging.OpenCents,
			"sum_buckets", out.Header.Aging.CurrentCents+out.Header.Aging.Days30Cents+out.Header.Aging.Days60Cents+out.Header.Aging.Days90Cents+out.Header.Aging.Days90PlusCents,
			"overdue_cents", out.Header.Aging.OverdueCents,
			"sum_late", out.Header.Aging.Days30Cents+out.Header.Aging.Days60Cents+out.Header.Aging.Days90Cents+out.Header.Aging.Days90PlusCents,
		)
		httpx.Error(w, http.StatusInternalServerError, "workspace aging does not reconcile to the penny")
		return
	}
	// Same reconciliation for the header. The header's OpenCents reads the same union the aging
	// reads (open invoices plus the grouped non invoice AR rows), computed by its own query, so a
	// drift means one of the two reads is wrong or the two reads straddled a write. Either way,
	// the screen is unsafe.
	if h.credit != nil && out.Header.Aging.OpenCents != out.Header.OpenCents {
		h.log.Error("workspace header drift",
			"customer_id", customerID,
			"header_open", out.Header.OpenCents,
			"aging_open", out.Header.Aging.OpenCents,
		)
		httpx.Error(w, http.StatusInternalServerError, "workspace header does not reconcile to the penny")
		return
	}

	// Available = limit - open, the same arithmetic the account position card on the
	// invoice detail carries. Done here, after the reconciliation check, so a bad
	// reconciliation can never produce a screen with a clean available figure and a
	// dirty open figure (the dishonest answer).
	out.Header.AvailableCreditCents = out.Header.CreditLimitCents - out.Header.OpenCents
	out.Header.UtilizationBps = bpsOfLimit(out.Header.OpenCents, out.Header.CreditLimitCents)

	open, closed, disputed, err := h.store.WorkspaceInvoices(r.Context(), customerID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	out.InvoicesOpen = open
	out.InvoicesClosed = closed
	out.InvoicesDisputed = disputed

	if held {
		if out.Payments, err = h.store.WorkspacePayments(r.Context(), customerID); err != nil {
			h.writeErr(w, r, err)
			return
		}
	} else {
		out.Payments = nil
	}
	if out.CreditNotes, err = h.store.WorkspaceCreditNotes(r.Context(), customerID); err != nil {
		h.writeErr(w, r, err)
		return
	}
	if out.Statements, err = h.store.WorkspaceStatements(r.Context(), customerID); err != nil {
		h.writeErr(w, r, err)
		return
	}
	if out.Notes, err = h.store.WorkspaceNotes(r.Context(), customerID, 50); err != nil {
		h.writeErr(w, r, err)
		return
	}
	// Activity: the last 10 rows across invoices/payments/credits/promises/disputes. The
	// service's CustomerActivity already gives us that; reaching it through the service
	// means the workspace and the invoice detail share one definition of "the activity
	// timeline", and neither can drift.
	if h.credit != nil {
		acts, err := h.credit.CustomerActivity(r.Context(), customerID, 10, held)
		if err != nil && !errors.Is(err, ErrCustomerActivityNotConfigured) {
			h.writeErr(w, r, err)
			return
		}
		if acts != nil {
			out.Activity = acts
		}
	}

	httpx.WriteJSON(w, http.StatusOK, out)
}

// addNote is the POST handler for /api/v1/billing/ar/customers/{customer_id}/notes. The
// body is an AddNoteInput; the response is AddNoteOutput with the saved note. Pinned=true
// is enforced by 0405's partial unique index, and the seam runs the unpin-else-insert
// pair inside one transaction so a failure between them leaves the account pinned the
// way it was.
func (h *WorkspaceHandler) addNote(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	customerID := strings.TrimSpace(r.PathValue("customer_id"))
	if customerID == "" {
		httpx.Error(w, http.StatusBadRequest, "customer_id is required")
		return
	}
	if !looksLikeUUIDish(customerID) {
		httpx.Error(w, http.StatusBadRequest, "customer_id is not a uuid")
		return
	}
	reachable, err := h.reachable(r.Context(), customerID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if !reachable {
		httpx.Error(w, http.StatusNotFound, "customer not found")
		return
	}
	var in AddNoteInput
	if err := decode(r, &in); err != nil {
		h.writeErr(w, r, err)
		return
	}
	// The author is the linked staff row (users.id, a uuid), never the platform id: FB Console ids are
	// not uuids, and the reach check above has already refused a caller with no linked row.
	authorID := strings.TrimSpace(identity.StaffUserIDFromContext(r.Context()))
	note, err := h.store.AddNote(r.Context(), customerID, authorID, in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, AddNoteOutput{Note: note})
}

// writeErr is the billing handler's error translation. The seam here is the same one
// register_handler.go uses, so the workspace and the register render the same error
// envelope and the same status codes.
func (h *WorkspaceHandler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		httpx.WriteJSON(w, http.StatusOK, struct{}{})
		return
	}
	// Translation: the seamed errors that have a public word (httpx.BadRequestError and
	// friends) carry their own status; everything else is 500. We do not invent 400s for
	// messages that don't carry one, because a wrong status code is a worse lie than a
	// generic one.
	var bad httpx.BadRequestError
	if errors.As(err, &bad) {
		httpx.Error(w, http.StatusUnprocessableEntity, bad.Msg)
		return
	}
	h.log.Error("billing: workspace handler error", "path", r.URL.Path, "err", err.Error())
	httpx.Error(w, http.StatusInternalServerError, "workspace request failed")
}

// bpsOfLimit is the basis-points figure the utilization bar renders against. A zero limit
// gets 0; an over-limit account gets 10000+ (the bar reads "over limit" past 10000, an
// honest answer).
func bpsOfLimit(open, limit int64) int {
	if limit <= 0 {
		return 0
	}
	return int(open * 10000 / limit)
}

// _ pins the time package import when the workspace handler is built without the
// AsOf branch using it; the import is load-bearing for tests that fake now().
var _ time.Time
