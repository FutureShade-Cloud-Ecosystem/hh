package billing

// register_handler.go, the receivables register's HTTP surface (DIB-510).
//
// -- GATING, AND WHY IT DIFFERS FROM THE MONTH-END CONSOLE NEXT DOOR ----------------------------
// monthend_handler.go is manager-gated end to end, and its header explains why: every read there
// exposes customer financial DISTRESS, who is over their limit, who is ninety days late, who is on
// hold. That is the right rule for an aging worklist and the wrong rule for an invoice book.
//
// The register is the document list. A counter clerk needs to find an invoice, read it, print it,
// email it and take a cheque against it, and a screen that refused them would be refusing them access
// to paperwork the customer standing in front of them is already holding. So the READS and the
// per-invoice acts are requireStaff, in the same spirit as till's own Q-044 attribution reads ("who
// took this payment / what did it pay is a counter-level question").
//
// What stays manager-gated is COLLECTIONS: the bulk chase and the statement bundle are a deliberate
// campaign against an account, and the person who runs one owns the relationship consequences. What
// stays admin-gated is the dealer policy. And the ACCOUNT'S FINANCIAL POSITION, open balance, credit
// limit, headroom, is gated inside the payload rather than at the door (register_service.go
// gateAccount), because the same row is legitimately visible to both tiers and only one of them may
// see that column. Byte-absence, fail closed, never CSS.
//
// -- ONE PUBLIC ROUTE --------------------------------------------------------------------------
// The open-tracking pixel is unauthenticated by necessity: the thing fetching it is a customer's mail
// client, which has no staff token and never will. It is keyed on the unguessable id of the delivery
// row, it accepts no input, it writes one timestamp and a counter on one row, and it returns a 1×1
// GIF whatever happens, so it is neither an existence oracle nor a way to learn anything. The
// dispatch module's public will-call route is the precedent for a customer-facing endpoint living
// beside its module's staff routes.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/taxwords"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxRegisterBody = 1 << 20 // 1 MiB

// RegisterHandler serves the receivables register.
type RegisterHandler struct {
	userLink identity.UserIDLinker
	svc      *RegisterService
	repo     *Repository
	log      *slog.Logger
	branch   func(id string) string
	trail    PaymentTrailReader
	// taxWords is the dealer's tax word for the printed invoice's tax row. Nil is the Ontario dealer's "HST".
	taxWords taxwords.Source
}

// WithTaxLabel binds the dealer's tax word seam (platform/taxwords) for the printed invoice.
func (h *RegisterHandler) WithTaxLabel(src taxwords.Source) *RegisterHandler {
	h.taxWords = src
	return h
}

// NewRegisterHandler wires the handler. repo is used only for the two reads the handler itself needs
// (the document's branch name and the tracked-link id); everything with a decision in it goes through
// the service.
func NewRegisterHandler(svc *RegisterService, repo *Repository, log *slog.Logger) *RegisterHandler {
	if log == nil {
		log = slog.Default()
	}
	return &RegisterHandler{svc: svc, repo: repo, log: log}
}

// WithBranchNamer binds a branch-name lookup for the printed document. Unbound, the document prints
// the branch id, ugly but true, rather than a blank where a business name belongs.
func (h *RegisterHandler) WithBranchNamer(f func(id string) string) *RegisterHandler {
	h.branch = f
	return h
}

// WithPaymentTrail binds the reader that says whether a caller holds the payment trail reads. Unbound,
// no caller does: the invoice detail then leaves the PAYMENT entries out of every timeline (the
// least privilege answer, so an unwired composition withholds rather than shows).
func (h *RegisterHandler) WithPaymentTrail(t PaymentTrailReader) *RegisterHandler {
	h.trail = t
	return h
}

// RegisterRoutes self-registers the register's routes. See the header for the gating rule.
func (h *RegisterHandler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	// reads + the per-invoice acts a counter genuinely performs
	r.Handle("GET /api/v1/ar/register", g.RequireStaff(h.staff(h.register)))
	r.Handle("GET /api/v1/ar/register/policy", g.RequireStaff(h.staff(h.policy)))
	r.Handle("POST /api/v1/ar/register/selection", g.RequireStaff(h.staff(h.selection)))
	r.Handle("GET /api/v1/ar/register/export", g.RequireStaff(h.staff(h.export)))
	r.Handle("GET /api/v1/ar/register/invoices/{id}", g.RequireStaff(h.staff(h.detail)))
	r.Handle("GET /api/v1/ar/register/invoices/{id}/document", g.RequireStaff(h.staff(h.document)))
	r.Handle("POST /api/v1/ar/register/invoices/{id}/payments", g.RequireStaff(h.staff(h.recordPayment)))
	r.Handle("POST /api/v1/ar/payments/apply", g.RequireStaff(h.staff(h.applyReceived)))
	r.Handle("POST /api/v1/ar/register/invoices/{id}/send", g.RequireStaff(h.staff(h.sendInvoice)))
	r.Handle("POST /api/v1/ar/register/invoices/{id}/promise", g.RequireStaff(h.staff(h.addPromise)))
	r.Handle("POST /api/v1/ar/register/invoices/{id}/dispute", g.RequireStaff(h.staff(h.openDispute)))
	r.Handle("POST /api/v1/ar/register/invoices/{id}/dispute/resolve", g.RequireStaff(h.staff(h.resolveDispute)))

	// collections: a campaign against an account, not a counter act
	r.Handle("POST /api/v1/ar/register/reminders", g.RequireManager(h.staff(h.sendReminders)))
	r.Handle("POST /api/v1/ar/register/statements", g.RequireManager(h.staff(h.createStatements)))

	// the dealer's screen settings
	r.Handle("PUT /api/v1/ar/register/policy", g.RequireAdmin(h.staff(h.setPolicy)))

	// the customer's own mail client, see the header
	r.Handle("GET /api/public/v1/ar/invoice-opened/{deliveryID}", http.HandlerFunc(h.trackOpen))
}

// -- reads --------------------------------------------------------------------------------------

func (h *RegisterHandler) register(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	f := RegisterFilter{
		Search:      strings.TrimSpace(q.Get("q")),
		Chip:        strings.ToUpper(strings.TrimSpace(q.Get("chip"))),
		BranchID:    q.Get("branch_id"),
		CustomerID:  q.Get("customer_id"),
		Salesperson: q.Get("salesperson_id"),
		ShowCredits: q.Get("show_credits") == "1" || q.Get("show_credits") == "true",
		Limit:       atoiOr(q.Get("limit"), 50),
		Offset:      atoiOr(q.Get("offset"), 0),
	}
	var err error
	if f.IssuedFrom, err = optDate(q.Get("issued_from")); err != nil {
		h.writeErr(w, r, err)
		return
	}
	if f.IssuedTo, err = optDate(q.Get("issued_to")); err != nil {
		h.writeErr(w, r, err)
		return
	}
	page, err := h.svc.Register(r.Context(), f)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	// A nil slice would serialize as null and make every consumer write a null check (the aging
	// handler's rule, three lines above this one in spirit).
	if page.Items == nil {
		page.Items = []RegisterRow{}
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *RegisterHandler) policy(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	p, err := h.svc.Policy(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

// selection is a POST because a selection is a list the caller owns, and a list of forty invoice ids
// in a query string is a URL nobody can log or debug. It changes nothing.
func (h *RegisterHandler) selection(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var body struct {
		InvoiceIDs []string `json:"invoice_ids"`
	}
	if err := decode(r, &body); err != nil {
		h.writeErr(w, r, err)
		return
	}
	sum, n, err := h.svc.Selection(r.Context(), body.InvoiceIDs)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"count": n, "balance_cents": sum})
}

func (h *RegisterHandler) detail(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	withPayments := false
	if h.trail != nil {
		held, err := h.trail.HoldsPaymentTrail(r.Context(), caller.PlatformUserID, caller.OrgID)
		if err != nil {
			h.writeErr(w, r, err)
			return
		}
		withPayments = held
	}
	d, err := h.svc.Detail(r.Context(), r.PathValue("id"), withPayments)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if d.Lines == nil {
		d.Lines = []DetailLine{}
	}
	if d.Activity == nil {
		d.Activity = []ActivityEntry{}
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

// document renders the printable invoice. It DOES NOT mark an open, see RegisterService.MarkOpened.
func (h *RegisterHandler) document(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	d, err := h.svc.Detail(r.Context(), r.PathValue("id"), false)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	name := d.BranchID
	if h.branch != nil {
		if n := h.branch(d.BranchID); n != "" {
			name = n
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Never cached: an invoice document whose balance is a week old is a document that will be waved
	// at somebody in an argument.
	w.Header().Set("Cache-Control", "no-store")
	// A failed read of the dealer's word prints the neutral "Tax": true for every dealer.
	word, _ := taxwords.Resolve(r.Context(), h.taxWords)
	_, _ = w.Write([]byte(renderInvoiceHTMLWithTax(d, name, word)))
}

// export writes the CURRENT VIEW as CSV, the same filter, the same order, the same figures.
//
// The same filter is the point. An export button that quietly exported everything, or exported a
// different sort, produces a spreadsheet that disagrees with the screen it came from, and the
// spreadsheet is the one that gets emailed to somebody who never saw the screen.
func (h *RegisterHandler) export(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	f := RegisterFilter{
		Search:      strings.TrimSpace(q.Get("q")),
		Chip:        strings.ToUpper(strings.TrimSpace(q.Get("chip"))),
		BranchID:    q.Get("branch_id"),
		CustomerID:  q.Get("customer_id"),
		Salesperson: q.Get("salesperson_id"),
		ShowCredits: q.Get("show_credits") == "1" || q.Get("show_credits") == "true",
		// Every row of the view in one statement: the repository caps one page, and the export is
		// promised to carry the whole view. One statement is one snapshot, so a payment landing
		// mid-export cannot shift a row between pages (an offset walk could skip or repeat one).
		All: true,
	}
	page, err := h.svc.Register(r.Context(), f)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	items := page.Items
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="receivables.csv"`)
	var b strings.Builder
	b.WriteString("Invoice,Customer,Account,Order,Customer PO,Issued,Due,Status,Days overdue,Total,Balance\n")
	for _, v := range items {
		b.WriteString(strings.Join([]string{
			csvCell(v.InvoiceNo), csvCell(v.CustomerName), csvCell(v.AccountNo),
			csvCell(v.OrderNo), csvCell(v.CustomerPO),
			v.IssueDate.Format("2006-01-02"), v.DueDate.Format("2006-01-02"),
			csvCell(v.Status), strconv.Itoa(v.DaysOverdue),
			centsCell(v.TotalCents), centsCell(v.BalanceCents),
		}, ","))
		b.WriteString("\n")
	}
	_, _ = w.Write([]byte(b.String()))
}

// -- writes -------------------------------------------------------------------------------------

func (h *RegisterHandler) recordPayment(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in ReceiptInput
	if err := decode(r, &in); err != nil {
		h.writeErr(w, r, err)
		return
	}
	in.InvoiceID = r.PathValue("id")
	res, err := h.svc.RecordPayment(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	// The invoice's balance AFTER the settle, read back rather than computed: the screen must show
	// what the ledger says, not what the client expected the ledger to become.
	if hdr, hErr := h.repo.InvoiceHeader(r.Context(), in.InvoiceID); hErr == nil {
		res.BalanceCents = hdr.BalanceCents
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// applyReceived records ONE received payment (amount, method, reference) against a list of the
// account's open invoices. The server allocates it oldest first in one transaction and answers the
// split per invoice; the money and the split are never the caller's to compute. The route is the
// replacement for the app splitting one cheque into a request per invoice, which was neither atomic
// nor one payment row.
func (h *RegisterHandler) applyReceived(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in ApplyPaymentInput
	if err := decode(r, &in); err != nil {
		h.writeErr(w, r, err)
		return
	}
	res, err := h.svc.ApplyReceivedPayment(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *RegisterHandler) sendInvoice(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := decode(r, &body); err != nil {
		h.writeErr(w, r, err)
		return
	}
	res, err := h.svc.SendInvoice(r.Context(), r.PathValue("id"), body.IdempotencyKey)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *RegisterHandler) sendReminders(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in ReminderInput
	if err := decode(r, &in); err != nil {
		h.writeErr(w, r, err)
		return
	}
	res, err := h.svc.SendReminder(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// createStatements runs the shipped INTERIM statement generator over the DISTINCT ACCOUNTS in a
// selection, see RegisterService.StatementsForSelection for why the unit is the account.
func (h *RegisterHandler) createStatements(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var body struct {
		InvoiceIDs     []string `json:"invoice_ids"`
		IdempotencyKey string   `json:"idempotency_key"`
	}
	if err := decode(r, &body); err != nil {
		h.writeErr(w, r, err)
		return
	}
	out, err := h.svc.StatementsForSelection(r.Context(), body.InvoiceIDs, body.IdempotencyKey)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *RegisterHandler) addPromise(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in PromiseInput
	if err := decode(r, &in); err != nil {
		h.writeErr(w, r, err)
		return
	}
	in.InvoiceID = r.PathValue("id")
	view, conflict, err := h.svc.AddPromise(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if conflict {
		// 409 with the live promise attached: the caller's next move is to look at the one that
		// already exists, so it is in the response rather than behind another request.
		httpx.ErrorWith(w, http.StatusConflict,
			"this invoice already carries a live promise; settle it before recording another",
			map[string]any{"promise": view})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

func (h *RegisterHandler) openDispute(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in DisputeInput
	if err := decode(r, &in); err != nil {
		h.writeErr(w, r, err)
		return
	}
	in.InvoiceID = r.PathValue("id")
	view, conflict, err := h.svc.OpenDispute(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if conflict {
		httpx.ErrorWith(w, http.StatusConflict,
			"this invoice already has an open dispute",
			map[string]any{"dispute": view})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

func (h *RegisterHandler) resolveDispute(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var body struct {
		Outcome string `json:"outcome"`
	}
	if err := decode(r, &body); err != nil {
		h.writeErr(w, r, err)
		return
	}
	ok, err := h.svc.ResolveDispute(r.Context(), r.PathValue("id"), body.Outcome)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"resolved": ok})
}

func (h *RegisterHandler) setPolicy(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var p RegisterPolicy
	if err := decode(r, &p); err != nil {
		h.writeErr(w, r, err)
		return
	}
	if err := h.svc.SetPolicy(r.Context(), p); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

// trackPixel is a 1×1 transparent GIF. Returned on EVERY path, success, unknown id, database
// trouble, so the response body can never tell a stranger whether an id was real.
var trackPixel, _ = base64.StdEncoding.DecodeString("R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7")

func (h *RegisterHandler) trackOpen(w http.ResponseWriter, r *http.Request) {
	if _, err := h.svc.MarkOpened(r.Context(), r.PathValue("deliveryID")); err != nil {
		// Logged, never surfaced. A mail client is not a caller we can ask to retry.
		h.log.WarnContext(r.Context(), "ar register: open tracking write failed", "err", err)
	}
	w.Header().Set("Content-Type", "image/gif")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	_, _ = w.Write(trackPixel)
}

// -- plumbing -----------------------------------------------------------------------------------

// errBodyNotJSON is the route's answer to a body it could not read at all. It is a 400, not a 422:
// on the received payment route a 422 is exclusively the pre-write refusal for a JSON body the route
// read and checked, so a desk holding an idempotency key never mistakes a body mangled in flight for
// the route refusing a body it saw (FP 186 and 187).
var errBodyNotJSON = errors.New("the request body must be JSON")

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxRegisterBody))
	if err := dec.Decode(v); err != nil {
		return errBodyNotJSON
	}
	return nil
}

func (h *RegisterHandler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var bad httpx.BadRequestError
	var sd *SupplierDepositNotCreditable
	var nsi payments.NoSuchInvoiceError
	var rc payments.ReplayConflictError
	switch {
	case errors.As(err, &nsi):
		// A missing invoice, another account's, and one outside a scoped caller's branch reach all
		// answer the same 404: no existence leak across the account or branch walls, the posture
		// the till's pay balance door holds.
		httpx.Error(w, http.StatusNotFound, "no such invoice: "+nsi.InvoiceID)
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "no such invoice")
	case errors.Is(err, ErrBranchForbidden):
		httpx.Error(w, http.StatusForbidden, BranchForbiddenWords)
	case errors.As(err, &sd):
		// Item 9 delta review P2-C: a credit memo against a deposit document is
		// refused in words, with the stable code a client can switch on beside
		// them. The register carries no memo issuing route today; the arm is the
		// door's answer for the day one lands.
		httpx.ErrorWith(w, http.StatusConflict, SupplierDepositNotCreditableWords,
			map[string]any{"code": SupplierDepositNotCreditableCode})
	case errors.As(err, &rc):
		// The replay judgement's own refusal (a spent key on a different payment or another
		// account) answers 409 Conflict, so a client reads a spent key by status alone, never by
		// matching words in the error string.
		httpx.Error(w, http.StatusConflict, rc.Msg)
	case errors.Is(err, errBodyNotJSON):
		httpx.Error(w, http.StatusBadRequest, errBodyNotJSON.Error())
	case errors.As(err, &bad):
		httpx.Error(w, http.StatusUnprocessableEntity, bad.Msg)
	default:
		h.log.ErrorContext(r.Context(), "ar register", "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "the receivables register is not answering right now")
	}
}

func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

func optDate(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, httpx.BadRequestf("a date filter must be a calendar date (YYYY-MM-DD)")
	}
	return &d, nil
}

// csvCell quotes a field the way every spreadsheet expects. A customer called "Bay of Quinte
// Landscaping, Inc." must not become two columns.
func csvCell(s string) string {
	if strings.ContainsAny(s, `",`+"\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// centsCell writes money as a plain decimal, no currency mark, no thousands separator, because the
// destination is a spreadsheet cell somebody will sum, not a sentence somebody will read.
func centsCell(cents int64) string {
	neg := ""
	if cents < 0 {
		neg, cents = "-", -cents
	}
	return fmt.Sprintf("%s%d.%02d", neg, cents/100, cents%100)
}
