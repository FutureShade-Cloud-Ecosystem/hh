package pos

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

const maxBodyBytes = 1 << 20 // 1 MiB

// Handler exposes the POS HTTP surface, all RequireStaff-gated. The deep FN-evidence view is an A-21
// admin route, not here.
type Handler struct {
	userLink identity.UserIDLinker
	svc      *Service
	log      *slog.Logger
	// grants answers the payment grant read (grant.go); nil answers it unavailable.
	grants GrantReader
}

// NewHandler wires the service.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes self-registers the POS routes. Staff for the counter surface, manager for the pickup
// queue transitions, admin for the policy write (the till handler's three-guard shape).
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("POST /api/v1/pos/sales", g.RequireStaff(h.staff(h.createSale)))
	// The read-only sale preview (the row 985 item): the sale's own body
	// without any tender, priced through the ring's own resolver, writing
	// nothing. Staff only, the same role walls the sale takes, so the
	// counter sees the total it is about to ring.
	r.Handle("POST /api/v1/pos/sale-preview", g.RequireStaff(h.staff(h.previewSale)))
	// DIB-165 past-sale / till-session lookup. The literal path binds ahead of nothing here, it is a
	// distinct pattern from /sales/{id} under Go's ServeMux rules, so both coexist.
	r.Handle("GET /api/v1/pos/sales", g.RequireStaff(h.staff(h.lookupSales)))
	r.Handle("GET /api/v1/pos/sales/{id}", g.RequireStaff(h.staff(h.getSale)))
	r.Handle("POST /api/v1/pos/assess-relief", g.RequireStaff(h.staff(h.assessRelief)))
	r.Handle("POST /api/v1/pos/signatures", g.RequireStaff(h.staff(h.capture)))
	r.Handle("GET /api/v1/pos/stock-glance", g.RequireStaff(h.staff(h.stockGlance)))
	// DIB-488 rev 6 (item D): the counter's salesperson selector, active staff, id + name only.
	r.Handle("GET /api/v1/pos/salespeople", g.RequireStaff(h.staff(h.listSalespeople)))
	// The managers a till person may name when giving a discount at the branch (discount.go): id,
	// display name and role label of the logins the dealer's own-branch clearing list admits.
	r.Handle("GET /api/v1/pos/discount-approvers", g.RequireStaff(h.staff(h.listDiscountApprovers)))
	// DIB-488 rev 8 (item 10): the role-gated margin glance, requireStaff at the door, the
	// dealer's margin_visibility gate at the MARSHAL layer inside (non-allowed roles get `{}`).
	r.Handle("POST /api/v1/pos/margin-glance", g.RequireStaff(h.staff(h.marginGlance)))
	// The counter's CUSTOMER-AWARE price picture (retail beside this account's price, plus the
	// saving and the nearest unreached bulk threshold). Staff-gated and read-only, it carries no
	// cost, margin or floor field, so unlike margin-glance there is nothing here to role-gate.
	r.Handle("POST /api/v1/pos/counter-prices", g.RequireStaff(h.staff(h.counterPrices)))
	// The quick-pick grid, the handful of products this yard sells all day, curated through the
	// shared settings kit and priced for the customer on the sale. Staff-gated; no customer, no
	// curation and no sales history are all ordinary answers, never errors.
	r.Handle("GET /api/v1/pos/quick-picks", g.RequireStaff(h.staff(h.quickPicks)))
	// The counter's key roster (dealer-configurable presentation curation, see hotkeys.go).
	r.Handle("GET /api/v1/pos/hotkeys", g.RequireStaff(h.staff(h.hotkeys)))
	// The Customer step's past-due FACT. requireStaff because the counter person is exactly who was
	// asked to raise it; the dealer's balance_visibility gate decides what the bytes carry.
	r.Handle("GET /api/v1/pos/account-standing", g.RequireStaff(h.staff(h.accountStanding)))
	// DIB-502 (rev-8 item 8): the suggested add-ons read, requireStaff; empty is an ordinary answer.
	r.Handle("GET /api/v1/pos/addon-suggestions", g.RequireStaff(h.staff(h.addonSuggestions)))

	// -- W7 counter-v2 --
	// The counter-readable tax rate (row 661): the rate the ring binds, read
	// through the ring's own assessor, and the configured round rule beside
	// it. Staff, not admin: every receipt shows the rate, it is not a secret.
	r.Handle("GET /api/v1/pos/tax-rate", g.RequireStaff(h.staff(h.getTaxRate)))
	r.Handle("GET /api/v1/pos/policy", g.RequireStaff(h.staff(h.getPolicy)))
	r.Handle("PUT /api/v1/pos/policy", g.RequireAdmin(h.staff(h.putPolicy)))
	// The counter settings change history. ADMIN, matching the write directly above rather than the
	// staff read: knowing who moved margin_visibility is knowing that it moves at all, and the people
	// it is withheld from are exactly the people who should not be told it is a dial.
	r.Handle("GET /api/v1/pos/policy/history", g.RequireAdmin(h.staff(h.policyHistory)))
	// -- DIB-488 rev 2: the caller's OWN workspace ergonomics (0166). Staff, not admin, a counter
	// person parking their divider is self-service; the DEALER defaults stay behind the admin
	// policy write above. The service keys every read/write on the authenticated caller only.
	r.Handle("GET /api/v1/pos/workspace", g.RequireStaff(h.staff(h.getWorkspace)))
	r.Handle("PUT /api/v1/pos/workspace", g.RequireStaff(h.staff(h.putWorkspace)))
	r.Handle("DELETE /api/v1/pos/workspace", g.RequireStaff(h.staff(h.resetWorkspace)))
	r.Handle("POST /api/v1/pos/returns", g.RequireStaff(h.staff(h.createReturn)))
	h.registerReturnRequestRoutes(r, g) // A-06 portal-return approval queue
	r.Handle("GET /api/v1/pos/returns/lookup", g.RequireStaff(h.staff(h.lookupReturn)))
	r.Handle("GET /api/v1/pos/returns/pickups", g.RequireStaff(h.staff(h.listPickups)))
	r.Handle("POST /api/v1/pos/returns/{id}/pickup", g.RequireManager(h.staff(h.advancePickup)))
	r.Handle("POST /api/v1/pos/parked", g.RequireStaff(h.staff(h.parkTicket)))
	r.Handle("GET /api/v1/pos/parked", g.RequireStaff(h.staff(h.listParked)))
	r.Handle("POST /api/v1/pos/parked/{id}/resume", g.RequireStaff(h.staff(h.resumeParked)))
	r.Handle("DELETE /api/v1/pos/parked/{id}", g.RequireStaff(h.staff(h.discardParked)))
	r.Handle("POST /api/v1/pos/pricing-orders", g.RequireStaff(h.staff(h.createPricingOrder)))
	r.Handle("GET /api/v1/pos/pricing-orders/{id}", g.RequireStaff(h.staff(h.getPricingOrder)))
	r.Handle("GET /api/v1/pos/pricing-orders", g.RequireStaff(h.staff(h.listPricingOrders)))
	// -- item 9.4: Pay balance at the counter till, staff only, branch fenced
	// (pay_balance.go). The find is a read of money owed; the take moves money
	// through the drawer and the card rail, both work level.
	r.Handle("GET /api/v1/pos/pay-balance", g.RequireStaff(h.staff(h.findPayBalance)))
	r.Handle("POST /api/v1/pos/pay-balance", g.RequireStaff(h.staff(h.takePayBalance)))
}

// findPayBalance answers the orders that owe money and match the term: order
// number, customer name or phone. Another branch's order is simply not found.
func (h *Handler) findPayBalance(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	out, err := h.svc.PayBalanceFind(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}

// takePayBalance takes the whole balance of one order by CASH or CARD. 201 on
// the first take, 200 with the same shape on a replay.
func (h *Handler) takePayBalance(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in PayBalanceTakeInput
	if !decodeJSON(w, r, &in) {
		return
	}
	out, err := h.svc.PayBalanceTake(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	status := http.StatusCreated
	if out.Replayed {
		status = http.StatusOK
	}
	httpx.WriteJSON(w, status, out)
}

// -- W7 handlers -------------------------------------------------------------

// getTaxRate serves the counter's tax-rate read: the rate the ring binds and
// the configured round rule (taxrate.go holds the by-construction story).
func (h *Handler) getTaxRate(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	out, err := h.svc.TaxRate(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) getPolicy(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	p, err := h.svc.Policy(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) putPolicy(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in PosPolicy
	if !decodeJSON(w, r, &in) {
		return
	}
	p, err := h.svc.UpdatePolicy(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

// policyHistory serves the counter settings change trail, newest first.
//
// The envelope is {"items": [...]} rather than a bare array, the house shape every list read on this
// surface uses, and the one that leaves room for a cursor later without breaking a client.
func (h *Handler) policyHistory(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := h.svc.PolicyHistory(r.Context(), r.URL.Query().Get("branch_id"), limit)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": rows})
}

// -- DIB-488 rev 2: workspace handlers --

func (h *Handler) getWorkspace(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	out, err := h.svc.Workspace(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) putWorkspace(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in WorkspaceSaveInput
	if !decodeJSON(w, r, &in) {
		return
	}
	out, err := h.svc.SaveWorkspace(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) resetWorkspace(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if err := h.svc.ResetWorkspace(r.Context()); err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) createReturn(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in ReturnInput
	if !decodeJSON(w, r, &in) {
		return
	}
	// The approver's credential rides its own header beside the clerk's
	// bearer (row 482): read once here, carried verbatim, never logged
	// anywhere on this path.
	in.ApproverCredential = r.Header.Get(approverAuthorizationHeader)
	ret, err := h.svc.Return(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	status := http.StatusCreated
	if ret.Replayed {
		status = http.StatusOK
	}
	httpx.WriteJSON(w, status, ret)
}

func (h *Handler) lookupReturn(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	out, err := h.svc.LookupReturn(r.Context(), q.Get("order_id"), q.Get("order_no"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) listPickups(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	out, err := h.svc.ListReturnPickups(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) advancePickup(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in struct {
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	out, err := h.svc.AdvancePickup(r.Context(), r.PathValue("id"), in.Status)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) parkTicket(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in ParkInput
	if !decodeJSON(w, r, &in) {
		return
	}
	t, err := h.svc.Park(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, t)
}

func (h *Handler) listParked(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	out, err := h.svc.ListParked(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) resumeParked(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	t, err := h.svc.ResumeParked(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) discardParked(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if err := h.svc.DiscardParked(r.Context(), r.PathValue("id")); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "DISCARDED"})
}

func (h *Handler) createPricingOrder(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in PricingOrderInput
	if !decodeJSON(w, r, &in) {
		return
	}
	po, err := h.svc.CreatePricingOrder(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, po)
}

func (h *Handler) getPricingOrder(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	po, err := h.svc.GetPricingOrder(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, po)
}

func (h *Handler) listPricingOrders(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	out, err := h.svc.ListPricingOrders(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) createSale(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in SaleInput
	if !decodeJSON(w, r, &in) {
		return
	}
	sale, err := h.svc.Sale(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	status := http.StatusCreated
	if sale.Replayed {
		status = http.StatusOK
	}
	httpx.WriteJSON(w, status, sale)
}

// previewSale answers the read-only sale preview: the same body the ring
// takes minus every tender, answered with the totals the ring would compute
// for it. No row is written; the walls are the sale's own.
func (h *Handler) previewSale(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in SalePreviewInput
	if !decodeJSON(w, r, &in) {
		return
	}
	out, err := h.svc.SalePreview(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// lookupSales serves the past-sale search (DIB-165). Branch-fenced in the service.
func (h *Handler) lookupSales(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	limit := 0
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	out, err := h.svc.LookupSales(r.Context(), SaleLookupQuery{
		BranchID:      strings.TrimSpace(q.Get("branch_id")),
		TillSessionID: strings.TrimSpace(q.Get("till_session_id")),
		Q:             strings.TrimSpace(q.Get("q")),
		From:          strings.TrimSpace(q.Get("from")),
		To:            strings.TrimSpace(q.Get("to")),
		Limit:         limit,
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if out == nil {
		out = []SaleSummary{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Handler) getSale(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	sale, err := h.svc.GetSale(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sale)
}

func (h *Handler) assessRelief(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in ReliefAssessInput
	if !decodeJSON(w, r, &in) {
		return
	}
	out, err := h.svc.AssessRelief(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) capture(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req struct {
		Purpose string `json:"purpose"`
		RefType string `json:"ref_type"`
		RefID   string `json:"ref_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ref, err := h.svc.CaptureSignature(r.Context(), req.Purpose, req.RefType, req.RefID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"signature_ref": ref})
}

// listSalespeople answers the counter's salesperson selector (DIB-488 rev 6, item D).
func (h *Handler) listSalespeople(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	out, err := h.svc.ListSalespeople(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}

// listDiscountApprovers answers the counter's "Approved by" picker for one branch.
func (h *Handler) listDiscountApprovers(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	out, err := h.svc.ListDiscountApprovers(r.Context(), strings.TrimSpace(r.URL.Query().Get("branch_id")))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}

// marginGlance answers the rev-8 item-10 margin read. The service returns the zero value unless
// BOTH dealer knobs allow, the configuration's cost and margin list considers the caller and the
// policy's margin_visibility lets the answer leave, and the marshal below then writes `{}` with
// every margin/profit field BYTE-ABSENT (all omitempty; the commander's gating ruling).
func (h *Handler) marginGlance(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in MarginGlanceInput
	if !decodeJSON(w, r, &in) {
		return
	}
	out, err := h.svc.MarginGlance(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// counterPrices answers the counter's price picture for a screen's worth of products. A walk-in
// (no customer_id) is not a special case: the same read returns retail alone.
func (h *Handler) counterPrices(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in CounterPriceInput
	if !decodeJSON(w, r, &in) {
		return
	}
	out, err := h.svc.CounterPrices(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// quickPicks answers the tap-grid. Every parameter is optional: an empty query is a walk-in at the
// default branch, and it gets a perfectly good grid.
func (h *Handler) accountStanding(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	out, err := h.svc.Standing(r.Context(), AccountStandingInput{
		BranchID:   q.Get("branch_id"),
		CustomerID: q.Get("customer_id"),
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) hotkeys(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	out, err := h.svc.Hotkeys(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) quickPicks(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	out, err := h.svc.QuickPicks(r.Context(), QuickPickInput{
		BranchID:   q.Get("branch_id"),
		CustomerID: q.Get("customer_id"),
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// addonSuggestions answers the DIB-502 strip. product_ids is the ticket's product ids,
// comma-separated; an empty ticket (or an empty answer) is `{"suggestions":[]}`, never an error.
func (h *Handler) addonSuggestions(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var ids []string
	if raw := strings.TrimSpace(r.URL.Query().Get("product_ids")); raw != "" {
		for _, p := range strings.Split(raw, ",") {
			if t := strings.TrimSpace(p); t != "" {
				ids = append(ids, t)
			}
		}
	}
	out, err := h.svc.AddonSuggestions(r.Context(), ids, 3)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) stockGlance(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	g, err := h.svc.StockGlance(r.Context(), r.URL.Query().Get("product_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, g)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(dst); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

// writeErr maps the POS error taxonomy to HTTP. The till has no price_blocked answer (D48 row
// 481, D49): the counter resolve never blocks a line, so there is nothing to map. A ring that
// took a card capture answers with the card charge design's words (3.3 to 3.7), never the bare
// 500 a rolled-back card ring used to carry.
func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ce clientError
	var cb ErrCreditBlocked
	var nt ErrNoOpenTill
	var ra ErrRefundApprovalRequired
	var mgr ErrCardChargeNeedsManager
	var sm ErrSplitTotalMismatch
	var asn ErrAccountShapeNotAvailable
	var dcn ErrDepositNotCollectNow
	var dor ErrDepositOutOfRange
	var pba ErrPayBalancePartAmount
	var cnt ErrCODNotForTermsAccount
	var sdn ErrSupplierDepositNotCredit
	var sdo ErrSupplierDepositOverPretax
	switch {
	case errors.As(err, &pba):
		// Item 9.4, the part amount refusal, the problem body style again.
		httpx.ErrorWith(w, http.StatusUnprocessableEntity, "pay_balance_whole_only", map[string]any{
			"sent_cents":    pba.SentCents,
			"balance_cents": pba.BalanceCents,
			"message":       payBalancePartMessage(pba),
		})
	case errors.As(err, &asn):
		// Item 9.2, the on account shape refusal: the problem body style
		// split_total_mismatch uses, a stable word the till switches on, the
		// money as integer cents fields, and a message in dollars.
		httpx.ErrorWith(w, http.StatusUnprocessableEntity, "on_account_not_available", map[string]any{
			"amount_cents":       asn.AmountCents,
			"credit_limit_cents": asn.CreditLimitCents,
			"message":            accountShapeMessage(asn),
		})
	case errors.As(err, &dcn):
		// Item 9.2, the deposit on a collect now sale, same problem body style.
		httpx.ErrorWith(w, http.StatusUnprocessableEntity, "deposit_not_collect_now", map[string]any{
			"total_cents":   dcn.TotalCents,
			"deposit_cents": dcn.DepositCents,
			"message":       depositNotCollectNowMessage(dcn.DepositCents, dcn.TotalCents),
		})
	case errors.As(err, &dor):
		// The deposit's money bound (run D's finding), the problem body style
		// again: the stable word, the two amounts as integer cents fields, and
		// the dollar sentence.
		httpx.ErrorWith(w, http.StatusUnprocessableEntity, "deposit_out_of_range", map[string]any{
			"deposit_cents": dor.DepositCents,
			"total_cents":   dor.TotalCents,
			"message":       depositOutOfRangeMessage(dor),
		})
	case errors.As(err, &sdn):
		// Sub item 9.5, the supplier deposit's shape refusal, same problem body style.
		httpx.ErrorWith(w, http.StatusUnprocessableEntity, "supplier_deposit_not_credit", map[string]any{
			"message": supplierDepositNotCreditMessage(sdn),
		})
	case errors.As(err, &sdo):
		// Sub item 9.5, the deposit above the sale's pre-tax amount.
		httpx.ErrorWith(w, http.StatusUnprocessableEntity, "supplier_deposit_over_pretax", map[string]any{
			"deposit_cents":  sdo.DepositCents,
			"subtotal_cents": sdo.SubtotalCents,
			"message":        supplierDepositOverPretaxMessage(sdo),
		})
	case errors.As(err, &cnt):
		// Item 9 review P3-7: the pay before release sale type refused for an
		// account that pays on terms, a stable word and one plain sentence.
		httpx.ErrorWith(w, http.StatusUnprocessableEntity, "cod_not_for_terms_account", map[string]any{
			"message": codNotForTermsMessage(),
		})
	case errors.As(err, &sm):
		// The split-tender sum refusal (DIB-165) is a problem body a person
		// can read: the stable word, the two amounts as integer cents
		// fields, and the dollar sentence. The till renders from the
		// fields; no message ever prints cents.
		httpx.ErrorWith(w, http.StatusUnprocessableEntity, "split_total_mismatch", map[string]any{
			"tendered_cents": sm.TenderedCents,
			"total_cents":    sm.TotalCents,
			"message":        splitMismatchMessage(sm.TenderedCents, sm.TotalCents),
		})
	case errors.Is(err, ErrSaleFailedCardReversed):
		// The sale did not go through and every card charge it took was reversed: the client
		// clears its key and rings again.
		httpx.Error(w, http.StatusServiceUnavailable, "sale_failed_card_reversed")
	case errors.As(err, &mgr):
		// The money may be held with no sale: the attempts' short references ride beside the
		// word so the manager knows which rows to read.
		httpx.ErrorWith(w, http.StatusConflict, "card_charge_needs_manager",
			map[string]any{"attempts": mgr.Refs})
	case errors.Is(err, ErrCardChargeCardDeclined):
		// A card leg was declined, any card already charged for the sale was reversed (the
		// design's split decline answer).
		httpx.Error(w, http.StatusPaymentRequired, "card_charge_card_declined")
	case errors.Is(err, ErrCardChargeUnresolvedOnTill):
		httpx.Error(w, http.StatusConflict, "card_charge_unresolved_on_till")
	case errors.Is(err, ErrCardChargeInFlight):
		// The same-key double submit's loser: keep the key, ring again shortly.
		httpx.Error(w, http.StatusConflict, "card_charge_in_flight")
	case errors.Is(err, ErrTaxRailUnavailable):
		// The tax rail is down (the engine reports no standard rate row in
		// effect): nothing tax-bound can be answered untaxed (a sale, a
		// pricing order, a relief assessment, the rate read), so the counter
		// gets the 503 it can act on, in the card-refund 502 arm's wording
		// shape: what failed, what it means for the ticket. Never a bare 500,
		// and never a 200 with a zero rate.
		httpx.Error(w, http.StatusServiceUnavailable, ErrTaxRailUnavailable.Error())
	case errors.Is(err, ErrStandingUnavailable):
		// The standing could not be read (a seam unwired, an AR, hold or policy read failed): the
		// 503 the caller can act on, with the cause in the log. Never a 200 that reads as "clear".
		h.log.Error("pos: account standing unavailable", "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusServiceUnavailable, ErrStandingUnavailable.Error())
	case errors.As(err, &cb):
		httpx.ErrorWith(w, http.StatusConflict, "credit_blocked", map[string]any{"policy": cb.Policy, "queue": "staff_approvals"})
	case errors.As(err, &ra):
		// The contract's PosRefundApprovalRequired: the Error envelope whose
		// error string discriminates the approval refusal, the approval body
		// beside it naming the configured amount and the approver role keys
		// in the configuration's order, so the till's screen can show the
		// manager dialog (D49, X7).
		httpx.ErrorWith(w, http.StatusConflict, refundApprovalRequiredError, map[string]any{
			"approval": map[string]any{"amount_cents": ra.AmountCents, "approvers": ra.Approvers},
		})
	case errors.As(err, &nt):
		httpx.Error(w, http.StatusConflict, nt.Error())
	case errors.As(err, &ce):
		httpx.Error(w, ce.status, ce.msg)
	case errors.Is(err, ErrUnknownTaxReason):
		// The ring named a tax reason the engine refuses to bind: the cashier's client-correctable 400,
		// the engine's words (the offending reason) riding behind the sentinel's, never the bare 500 the
		// raw sentinel would fall to. Sits beside the sibling refusal arms out of the same adapter.
		httpx.Error(w, http.StatusBadRequest, err.Error())
	default:
		h.log.Error("pos: internal error", "method", r.Method, "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal error")
	}
}
