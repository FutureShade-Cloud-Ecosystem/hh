// edit_handler.go, the staff quote HTTP surface (DIB-56).
//
// The route guards are the COARSE first line only: the service re-checks the caller, the branch, the
// edit matrix and the expiry window on the resolved caller (edit.go), which is the single source of
// authority truth. Defence in depth, a route registered with the wrong guard is a bug, not a breach.
//
// Every route here is requireStaff. There is deliberately no requireManager rung anywhere on this
// surface: R1 ratifies quote editing as open to any active in-branch staff member, because a quote
// moves no money until Convert. That is the one place this surface intentionally diverges from the
// order-edit routes it otherwise mirrors, where line writes are manager-gated.
package quote

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"net/http"
	"strconv"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/customerwall"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// maxBodyBytes bounds a request body. A quote edit body is a handful of short fields; anything larger
// is a mistake or an attack, and either way it should be refused before it is parsed.
const maxBodyBytes = 8 << 10

// StaffHandler serves the staff quote builder.
type StaffHandler struct {
	svc                 *StaffService
	userLink            identity.UserIDLinker
	settingsGrantReader func(ctx context.Context) (SettingsGrant, error)
	reach               customerwall.Reach // the customer module's reach rule (UX-89)
}

// WithSettingsGrantReader wires the settings grant read behind the route. A handler
// without one answers the route unavailable, never "no": a read that cannot be made
// must not read as a refusal of the grant.
func (h *StaffHandler) WithSettingsGrantReader(f func(ctx context.Context) (SettingsGrant, error)) *StaffHandler {
	h.settingsGrantReader = f
	return h
}

func NewStaffHandler(svc *StaffService) *StaffHandler { return &StaffHandler{svc: svc} }

// RegisterStaffRoutes wires the staff quote endpoints. requireStaff is injected as a decorator rather
// than imported, so this module never depends on the identity package's guards.
func (h *StaffHandler) RegisterStaffRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("POST /api/v1/quotes", g.RequireStaff(h.staff(h.createQuote)))
	r.Handle("GET /api/v1/quotes", g.RequireStaff(h.staff(h.listQuotes)))
	r.Handle("GET /api/v1/quotes/{id}", g.RequireStaff(h.staff(h.readQuote)))
	r.Handle("PATCH /api/v1/quotes/{id}", g.RequireStaff(h.staff(h.setValidUntil)))
	r.Handle("POST /api/v1/quotes/{id}/lines", g.RequireStaff(h.staff(h.addLine)))
	r.Handle("PATCH /api/v1/quotes/{id}/lines/{lineNo}", g.RequireStaff(h.staff(h.editLineQty)))
	r.Handle("DELETE /api/v1/quotes/{id}/lines/{lineNo}", g.RequireStaff(h.staff(h.removeLine)))
	r.Handle("GET /api/v1/quotes/{id}/edits", g.RequireStaff(h.staff(h.editHistory)))
	// DIB-483, the COUNTER door onto quote→order conversion (M3-G7). requireStaff is the coarse
	// first line only, exactly as everywhere else on this surface: the SELLING-role gate is inside
	// the service (identity.CanSellOnCredit), because that is where the caller is actually resolved.
	// Registering this at requireManager instead would have been the wrong gate, inside/outside
	// sales are Tier-3 frontline roles and they are the people who do this.
	r.Handle("POST /api/v1/quotes/{id}/convert", g.RequireStaff(h.staff(h.convertQuote)))
	// The staff doors that record the customer's decision (staff_decision.go): the same selling-role
	// gate as convert, in the service, because that is where the caller is resolved.
	r.Handle("POST /api/v1/quotes/{id}/accept", g.RequireStaff(h.staff(h.acceptQuote)))
	r.Handle("POST /api/v1/quotes/{id}/reject", g.RequireStaff(h.staff(h.rejectQuote)))
	r.Handle("POST /api/v1/quotes/{id}/order-payment", g.RequireStaff(h.staff(h.takeOrderPayment)))
	// DIB-59 comparison. A POST because the alternates are a request BODY, not a filter, but it is a
	// strictly read-only operation: it writes nothing, to any table, including pricing_audit_log.
	r.Handle("POST /api/v1/quotes/{id}/lines/{lineNo}/compare", g.RequireStaff(h.staff(h.compareLine)))
	// The quote floor's request door (plan 1.1): for a line the caller may
	// edit whose asked discount lands below the floor, one approval request
	// bound to that quote, that line and that discount. requireStaff is the
	// coarse first line only; the edit ladder and the floor decision run in
	// the service, where the caller is actually resolved.
	r.Handle("POST /api/v1/quotes/{id}/lines/{lineNo}/floor-approval", g.RequireStaff(h.staff(h.requestFloorApproval)))
	// DIB-491, the related-documents lineage read (lineage.go): a quote's document family as
	// normalized rows, askable from EITHER end of the quote→order link. The orders route lives here
	// because the read model is the quote family, the same "one namespace for the operator, whatever
	// Go package implements it" reasoning the AR month-end routes state out loud.
	r.Handle("GET /api/v1/quotes/{id}/related-documents", g.RequireStaff(h.staff(h.quoteRelatedDocs)))
	r.Handle("GET /api/v1/orders/{id}/related-documents", g.RequireStaff(h.staff(h.orderRelatedDocs)))
	// DIB-491, the dealer's acceptance settings (acceptcontrols.go). requireStaff is the coarse
	// first line only, exactly as everywhere else on this surface: the Tier-1 GLOBAL admin gate runs
	// in the service (settingsAdmin), because that is where the caller is actually resolved, the
	// same in-service gate DIB-483's counter door uses for its selling-role check.
	r.Handle("GET /api/v1/quotes/settings/acceptance", g.RequireStaff(h.staff(h.acceptanceSettings)))
	r.Handle("PUT /api/v1/quotes/settings/acceptance", g.RequireStaff(h.staff(h.setAcceptanceSettings)))
	// The settings grant read (round 14 item 1): one question the Quote Desk asks before it
	// mounts its settings panels, in the payment-grant mould. requireStaff is the coarse first
	// line only; the predicates that answer the grant live in the service.
	r.Handle("GET /api/v1/quotes/settings-grant", g.RequireStaff(h.staff(h.settingsGrant)))
	// DIB-506/DIB-507, the work-area endpoints (policy, terms, margin glance) and the shared
	// settings kit's /api/v1/surfaces/… routes, mounted by this pilot consumer (builder_handler.go).
	h.registerBuilderRoutes(r, g)
}

// decodeBody reads a bounded JSON body. A malformed body is a 400, never a silent zero-value edit -
// "qty_entered": "" arriving as a successful no-op would be the worst possible failure mode here.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		return httpx.BadRequestf("malformed request body")
	}
	return nil
}

// lineNoParam parses {lineNo}. A non-positive integer is a 400, not a 404: the caller sent nonsense,
// which is different from asking for a line that does not exist.
func lineNoParam(r *http.Request) (int, error) {
	n, err := strconv.Atoi(r.PathValue("lineNo"))
	if err != nil || n <= 0 {
		return 0, httpx.BadRequestf("line number must be a positive integer")
	}
	return n, nil
}

func (h *StaffHandler) createQuote(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body struct {
		CustomerID string `json:"customer_id"`
		BranchID   string `json:"branch_id"`
		ValidUntil string `json:"valid_until"`
		JobID      string `json:"job_id"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		h.writeErr(w, err)
		return
	}
	if !customerwall.Fence(w, r, h.reach, body.CustomerID) {
		return
	}
	view, err := h.svc.CreateQuote(r.Context(), QuoteCreate{
		CustomerID: body.CustomerID, BranchID: body.BranchID, ValidUntil: body.ValidUntil, JobID: body.JobID,
	})
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, view)
}

func (h *StaffHandler) listQuotes(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	// Cursor pagination (HH-3.2 B4). Stable-ordering basis: created_at
	// DESC, quote_no (unique), the cursor is the created_at/quote_no pair
	// of the last kept row. Fetch limit+1 to learn whether a further page
	// exists, drop the extra row, mint next_cursor from the last kept row.
	p, err := httpx.ParseCursorPage(r)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	created, quoteNo, ok := parseQuoteCursor(p.Cursor)
	if !ok {
		h.writeErr(w, httpx.BadRequestError{Msg: "cursor is malformed"})
		return
	}
	rows, err := h.svc.StaffQuoteList(r.Context(), Filter{
		Status: r.URL.Query().Get("status"), Limit: p.Limit + 1,
		AfterCursor: p.Cursor != "", CursorCreated: created, CursorQuoteNo: quoteNo,
	})
	if err != nil {
		h.writeErr(w, err)
		return
	}
	next := ""
	if len(rows) > p.Limit {
		rows = rows[:p.Limit]
		next = rows[len(rows)-1].cursorKey
	}
	httpx.WriteCursorPage(w, rows, next, p.Limit)
}

// parseQuoteCursor decodes a minted staff-list key; ok is false for an
// empty cursor (first page, with the pair zeroed) or one that does not
// carry the created_at/quote_no shape.
func parseQuoteCursor(key string) (created, quoteNo string, ok bool) {
	if key == "" {
		return "", "", true
	}
	i := strings.LastIndex(key, "/")
	if i <= 0 || i == len(key)-1 {
		return "", "", false
	}
	return key[:i], key[i+1:], true
}

func (h *StaffHandler) readQuote(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	view, err := h.svc.StaffQuoteRead(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

func (h *StaffHandler) setValidUntil(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body struct {
		ValidUntil string `json:"valid_until"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		h.writeErr(w, err)
		return
	}
	res, err := h.svc.SetValidUntil(r.Context(), r.PathValue("id"), body.ValidUntil)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *StaffHandler) addLine(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body struct {
		ProductID   string `json:"product_id"`
		Description string `json:"description"`
		QtyEntered  string `json:"qty_entered"`
		UOMEntered  string `json:"uom_entered"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		h.writeErr(w, err)
		return
	}
	res, err := h.svc.AddLine(r.Context(), r.PathValue("id"),
		body.ProductID, body.Description, body.QtyEntered, body.UOMEntered)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *StaffHandler) editLineQty(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	lineNo, err := lineNoParam(r)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	var body struct {
		QtyEntered string `json:"qty_entered"`
		// DIB-506: the optional per-line discount arm. Absent = the shipped qty path,
		// byte-identical. Present WITHOUT qty_entered = a discount-only write.
		DiscPct json.RawMessage `json:"disc_pct,omitempty"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		h.writeErr(w, err)
		return
	}
	if len(body.DiscPct) > 0 && body.QtyEntered == "" {
		pct, perr := parseDiscPct(body.DiscPct)
		if perr != nil {
			h.writeBuilderErr(w, perr)
			return
		}
		h.setLineDiscount(w, r, r.PathValue("id"), lineNo, pct)
		return
	}
	res, err := h.svc.EditLineQty(r.Context(), r.PathValue("id"), lineNo, body.QtyEntered)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *StaffHandler) removeLine(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	lineNo, err := lineNoParam(r)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	res, err := h.svc.RemoveLine(r.Context(), r.PathValue("id"), lineNo)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// requestFloorApproval is the request door (plan 1.1). The body carries
// the discount asked for and a bounded free text note; the subject and the
// detail are the server's own.
func (h *StaffHandler) requestFloorApproval(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	lineNo, err := lineNoParam(r)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	var body struct {
		DiscPct int    `json:"disc_pct"`
		Note    string `json:"note"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		h.writeErr(w, err)
		return
	}
	ans, err := h.svc.RequestFloorApproval(r.Context(), r.PathValue("id"), lineNo, body.DiscPct, body.Note)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, ans)
}

func (h *StaffHandler) compareLine(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	lineNo, err := lineNoParam(r)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	var body struct {
		Alternates []Alternate `json:"alternates"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		h.writeErr(w, err)
		return
	}
	out, err := h.svc.CompareLine(r.Context(), r.PathValue("id"), lineNo, body.Alternates)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *StaffHandler) editHistory(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	out, err := h.svc.QuoteEditHistory(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// convertQuote is the DIB-483 counter door: turn an ACCEPTED quote into an order from the staff
// surface, with the optional fulfilment choice as a control.
//
// The body is OPTIONAL. A bare POST with no body at all is a valid conversion with no fulfilment
// choice, the byte-identical default path, because the overwhelmingly common counter action is
// "make this an order" and it should not require a payload to say so.
func (h *StaffHandler) convertQuote(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body struct {
		Fulfillment string `json:"fulfillment"`
		JobID       string `json:"job_id"`
	}
	if r.ContentLength > 0 {
		if err := decodeBody(w, r, &body); err != nil {
			h.writeErr(w, err)
			return
		}
	}
	res, err := h.svc.ConvertQuote(r.Context(), r.PathValue("id"), ConvertOptions{Fulfillment: body.Fulfillment, JobID: body.JobID, IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

// acceptQuote records that the customer accepted by phone, text, email or in person. The body is
// required: how the customer said it, and the statement that they confirmed the quantities, are the
// record. quantities_confirmed is a pointer so a missing field is a 400 in words, not a silent false.
func (h *StaffHandler) acceptQuote(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body struct {
		Channel             string `json:"channel"`
		Note                string `json:"note"`
		QuantitiesConfirmed *bool  `json:"quantities_confirmed"`
		ExpectedTotalCents  *int64 `json:"expected_total_cents"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		h.writeErr(w, err)
		return
	}
	res, err := h.svc.AcceptOnBehalf(r.Context(), r.PathValue("id"), StaffAcceptIn{
		Channel: body.Channel, Note: body.Note,
		QuantitiesConfirmed: body.QuantitiesConfirmed != nil && *body.QuantitiesConfirmed,
		ExpectedTotalCents:  body.ExpectedTotalCents,
		IdempotencyKey:      r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// rejectQuote records that the customer declined: the channel, a reason code and a note.
func (h *StaffHandler) rejectQuote(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body struct {
		Channel    string `json:"channel"`
		ReasonCode string `json:"reason_code"`
		Note       string `json:"note"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		h.writeErr(w, err)
		return
	}
	res, err := h.svc.RejectOnBehalf(r.Context(), r.PathValue("id"), StaffRejectIn{
		Channel: body.Channel, ReasonCode: body.ReasonCode, Note: body.Note,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// takeOrderPayment is the QD-5 counter door: take payment on a converted quote's order (or, for a
// customer with no credit, convert and pay as one action). The body is required: a payment with no
// tender, amount and key is not a payment.
func (h *StaffHandler) takeOrderPayment(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body struct {
		Method         string `json:"method"`
		AmountCents    int64  `json:"amount_cents"`
		Reference      string `json:"reference"`
		IdempotencyKey string `json:"idempotency_key"`
		Fulfillment    string `json:"fulfillment"`
		JobID          string `json:"job_id"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		h.writeErr(w, err)
		return
	}
	res, err := h.svc.TakeOrderPayment(r.Context(), r.PathValue("id"), OrderPaymentInput{
		Method: body.Method, AmountCents: body.AmountCents, Reference: body.Reference,
		IdempotencyKey: body.IdempotencyKey, Fulfillment: body.Fulfillment, JobID: body.JobID,
	})
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// - DIB-491: related documents + acceptance settings -

func (h *StaffHandler) quoteRelatedDocs(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	rows, err := h.svc.RelatedDocumentsForQuote(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	if rows == nil {
		rows = []RelatedDocument{} // a nil slice serializes as null and makes every consumer null-check
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *StaffHandler) orderRelatedDocs(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	rows, err := h.svc.RelatedDocumentsForOrder(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	if rows == nil {
		rows = []RelatedDocument{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *StaffHandler) acceptanceSettings(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	settings, err := h.svc.AcceptanceSettings(r.Context())
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, settings)
}

// setAcceptanceSettings flips the multi-accept switch.
//
// `allow_multi_accept` is a *bool so a body that omits it is REFUSED rather than silently read as
// false, this one field decides whether a duplicate order can be minted, and a malformed body that
// flips a policy by accident is the worst available failure mode (the billing setAutoHold rule).
func (h *StaffHandler) setAcceptanceSettings(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body struct {
		AllowMultiAccept *bool  `json:"allow_multi_accept"`
		Reason           string `json:"reason"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		h.writeErr(w, err)
		return
	}
	if body.AllowMultiAccept == nil {
		h.writeErr(w, httpx.BadRequestf("allow_multi_accept must be true or false, say which"))
		return
	}
	settings, err := h.svc.SetAllowMultiAccept(r.Context(), *body.AllowMultiAccept, body.Reason)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, settings)
}

// settingsGrant answers the one grant read the Quote Desk asks before mounting its
// settings panels: whether the signed-in caller holds the grants the three panels need.
// Each boolean uses the very predicate the refusing route uses, so the answer cannot
// drift from the refusal.
func (h *StaffHandler) settingsGrant(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	if h.settingsGrantReader == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "the settings grant could not be read")
		return
	}
	grant, err := h.settingsGrantReader(r.Context())
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, grant)
}

// writeErr maps this surface's errors to HTTP. Each refusal carries its OWN message: a caller who is
// told only "409" has to guess whether the quote was accepted, converted or expired, and those need
// three different reactions.
func (h *StaffHandler) writeErr(w http.ResponseWriter, err error) {
	var notEditable *NotEditableError
	var expired *QuoteExpiredError
	var badRequest httpx.BadRequestError
	var creditBlocked ErrCreditBlocked
	var notSelling *NotSellingRoleError
	var alreadyAccepted *AlreadyAcceptedError
	var notSettingsAdmin *NotSettingsAdminError
	var notOwner *NotQuoteOwnerError
	var floorRefusal *FloorRefusalError
	var nothingToApprove *NothingToApproveError
	var paymentConflict *OrderPaymentConflictError
	var decisionRefused *DecisionRefusedError
	switch {
	case errors.Is(err, ErrQuantityStatementMissing):
		httpx.Error(w, http.StatusUnprocessableEntity, err.Error())
	case errors.As(err, &decisionRefused):
		httpx.Error(w, http.StatusConflict, decisionRefused.Error())
	case errors.Is(err, ErrIdempotencyKeyReused), errors.Is(err, ErrConvertKeyReused):
		httpx.Error(w, http.StatusConflict, err.Error())
	case errors.As(err, &paymentConflict):
		httpx.Error(w, http.StatusConflict, paymentConflict.Error())
	// - DIB-483 / M3-G7B: the credit refusal keeps its REASON -
	// This case sits ABOVE the generic ErrInvalidTransition case below, and the ordering is the
	// entire fix. ErrCreditBlocked.Unwrap() returns ErrInvalidTransition on purpose (it is how the
	// block earns its 409 rather than a 500), which means a generic `errors.Is(err,
	// ErrInvalidTransition)` case placed first will MATCH IT and answer "invalid state transition",
	// throwing away the policy and the detail the type is carrying. The person at the counter then
	// has no idea whether the customer is over their limit, on an NSF hold, or simply looking at a
	// quote that was already converted. Same 409, real words.
	case errors.As(err, &creditBlocked):
		httpx.Error(w, http.StatusConflict, CreditRefusalMessage(creditBlocked))
	// - DIB-491: a repeat acceptance keeps its DOCUMENT -
	// Same ordering rule as the credit case above, for the same reason: AlreadyAcceptedError
	// answers errors.Is for ErrInvalidTransition (that is how the portal door keeps its 409), so
	// the generic case below would match it and throw away the order number and the way forward.
	case errors.As(err, &alreadyAccepted):
		httpx.Error(w, http.StatusConflict, alreadyAccepted.Error())
	case errors.As(err, &notSettingsAdmin):
		httpx.Error(w, http.StatusForbidden, notSettingsAdmin.Error())
	case errors.As(err, &notOwner):
		// Plan 3.3: name the way forward, never a bare 403.
		httpx.Error(w, http.StatusForbidden, notOwner.Error())
	case errors.As(err, &floorRefusal):
		// The typed 409: the policy, who may approve, the branch manager
		// way forward, the caller's standing request, and the figures for
		// callers on the cost list.
		httpx.WriteJSON(w, http.StatusConflict, floorRefusal)
	case errors.As(err, &nothingToApprove):
		httpx.Error(w, http.StatusConflict, nothingToApprove.Error())
	case errors.Is(err, errFloorStateChanged):
		httpx.Error(w, http.StatusConflict, errFloorStateChanged.Error())
	case errors.As(err, &notSelling):
		httpx.Error(w, http.StatusForbidden, notSelling.Error())
	case errors.As(err, &notEditable):
		// The acceptance criterion: a blocked state answers 409 with the status NAMED.
		httpx.Error(w, http.StatusConflict, notEditable.Error())
	case errors.As(err, &expired):
		// D2b: name the date AND the way forward, not a bare "expired".
		httpx.Error(w, http.StatusConflict, expired.Error())
	case errors.As(err, &badRequest):
		httpx.Error(w, http.StatusBadRequest, badRequest.Error())
	case errors.Is(err, ErrInvalidTransition):
		httpx.Error(w, http.StatusConflict, "quote: invalid state transition")
	case errors.Is(err, ErrLineNotFound):
		httpx.Error(w, http.StatusNotFound, "quote line not found")
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "quote not found")
	case errors.Is(err, ErrUnauthorized):
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, ErrForbidden):
		httpx.Error(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, ErrEditNotConfigured):
		// A server wiring fault, deliberately NOT a generic 500, name the unwired seam so an operator
		// reports a deploy problem rather than a business one.
		httpx.Error(w, http.StatusServiceUnavailable,
			"quote editing is not configured on this server, pricing seams not wired")
	case errors.Is(err, ErrLineageNotConfigured), errors.Is(err, ErrAcceptSettingsNotConfigured):
		// DIB-491's two seams take the same posture: name the unwired seam, 503.
		httpx.Error(w, http.StatusServiceUnavailable, err.Error())
	default:
		httpx.Error(w, http.StatusInternalServerError, "internal error")
	}
}

// WithCustomerReach binds the customer module's reach rule (the one seam) the quote create hold their customer to
// (UX-89). CustomerReachWired is the boot check that it was.
func (h *StaffHandler) WithCustomerReach(c customerwall.Reach) *StaffHandler {
	h.reach = c
	return h
}

// CustomerReachWired reports whether the customer reach rule is bound.
func (h *StaffHandler) CustomerReachWired() bool { return h.reach != nil }
