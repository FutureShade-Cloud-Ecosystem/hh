//go:build integration

// received_payment_route_integration_test.go, THE RECEIVED PAYMENT ROUTE through the register's
// own handler: one cheque named against a list of the account's open invoices, the server's split
// in the answer, and every refusal in words with nothing applied.
//
// The money legs (locks, one payment row, the journal) live in the payments package; this file owns
// what the ROUTE adds: the request shape, the refusal words an operator reads, the branch reach a
// scoped caller carries, and the promise a closed invoice settles.
package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/jackc/pgx/v5/pgxpool"
)

// applyRouteWorld is one test owned account through the REAL service and money seam (the payments
// repository itself, the same binding the composition root wires).
type applyRouteWorld struct {
	pool       *pgxpool.Pool
	svc        *RegisterService
	h          *RegisterHandler
	customerID string
	otherID    string
	tag        string
	// last runs after the world's own rows are gone (a user those rows referenced).
	last []func(context.Context)
}

func newApplyRouteWorld(t *testing.T) *applyRouteWorld {
	t.Helper()
	pool := intPool(t)
	tag := "APRT-" + randomTag()
	w := &applyRouteWorld{pool: pool, tag: tag}
	mk := func(label string) string {
		var id string
		if err := pool.QueryRow(context.Background(),
			`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
			 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 0) RETURNING id::text`,
			tag+"-"+label, "Apply route "+tag+" "+label, trentonBranch).Scan(&id); err != nil {
			t.Fatalf("seed customer %s: %v", label, err)
		}
		return id
	}
	w.customerID = mk("main")
	w.otherID = mk("other")
	repo := NewRepository(pool)
	// The money seam carries the early payment discount's terms read (TM-2), the same binding the
	// composition root gives the register's door: a route leg that names a discount terms invoice
	// exercises the real discount, not a seam left unbound.
	w.svc = NewRegisterService(repo).
		WithMoney(NewPaymentsReceiptWriter(NewPaymentsRepository(pool, nil))).
		WithClock(func() time.Time { return d510Now })
	w.h = NewRegisterHandler(w.svc, repo, nil)
	// Every delete fails the leg loudly naming its table (FROM-PRINCIPAL 202), in dependency
	// order: approval rows before the payments and invoices they name, then the rest, then whatever
	// a leg registered to run last (a staff user the rows above referenced).
	t.Cleanup(func() {
		c := context.Background()
		del := func(table, q string, args ...any) {
			if _, err := pool.Exec(c, q, args...); err != nil {
				t.Errorf("cleanup of %s failed: %v", table, err)
			}
		}
		for _, cid := range []string{w.customerID, w.otherID} {
			del("invoice_discount_approvals", `DELETE FROM invoice_discount_approvals WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`, cid)
			del("ar_promises_to_pay", `DELETE FROM ar_promises_to_pay WHERE customer_id = $1::uuid`, cid)
			del("customer_transactions", `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, cid)
			del("events_outbox", `DELETE FROM events_outbox WHERE payload->>'customer_id' = $1`, cid)
			del("payment_allocations", `DELETE FROM payment_allocations WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`, cid)
			del("payments", `DELETE FROM payments WHERE customer_id = $1::uuid`, cid)
			del("invoices", `DELETE FROM invoices WHERE customer_id = $1::uuid`, cid)
			del("customers", `DELETE FROM customers WHERE id = $1::uuid`, cid)
		}
		for _, f := range w.last {
			f(c)
		}
	})
	return w
}

// invoice plants one open invoice at Trenton dated against the pinned now, unless branch is named.
func (w *applyRouteWorld) invoice(t *testing.T, customerID, label string, issueDays, dueDays int, balance int64, branch string) string {
	t.Helper()
	if branch == "" {
		branch = trentonBranch
	}
	issue := d510Now.AddDate(0, 0, issueDays*-1)
	due := d510Now.AddDate(0, 0, dueDays*-1)
	var id string
	if err := w.pool.QueryRow(context.Background(),
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		 VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $5::date, $6, 0, $6, $6, $4, 'INVOICE')
		 RETURNING id::text`,
		w.tag+"-"+label, customerID, branch,
		issue.Format("2006-01-02"), due.Format("2006-01-02"), balance).Scan(&id); err != nil {
		t.Fatalf("seed invoice %s: %v", label, err)
	}
	return id
}

// post drives the route's handler as the mux would, with the caller stamped the way the guard
// chain stamps it, and returns the recorder.
func (w *applyRouteWorld) post(t *testing.T, caller identity.StaffContext, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ar/payments/apply", strings.NewReader(string(raw)))
	req = req.WithContext(identity.ContextWithStaff(req.Context(), caller))
	rec := httptest.NewRecorder()
	w.h.applyReceived(rec, req, guards.StaffCaller{})
	return rec
}

func applyGlobalCaller() identity.StaffContext {
	return identity.StaffContext{UserID: "", Tier: identity.TierManager, Scope: identity.ScopeGlobal, Status: "ACTIVE"}
}

func applyTrentonCaller() identity.StaffContext {
	return identity.StaffContext{UserID: "", RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Status: "ACTIVE", Branches: map[string]bool{trentonBranch: true}}
}

func decodeApply(t *testing.T, rec *httptest.ResponseRecorder) (int, map[string]any) {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	out := map[string]any{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	if body.Error != "" {
		out["error"] = body.Error
	}
	return rec.Code, out
}

// The route takes one cheque across the account's open invoices and answers the server's split per
// invoice: applied, whether it closes or parts, and the balance after.
func TestApplyRoute_AnswersTheServersSplitPerInvoice(t *testing.T) {
	w := newApplyRouteWorld(t)
	old := w.invoice(t, w.customerID, "OLD", 90, 60, 30000, "")
	mid := w.invoice(t, w.customerID, "MID", 60, 30, 20000, "")

	code, body := decodeApply(t, w.post(t, applyGlobalCaller(), map[string]any{
		"customer_id": w.customerID, "method": "cheque", "amount_cents": 35000,
		"reference": "CHQ 909", "invoice_ids": []string{mid, old},
		"idempotency_key": w.tag, "preview": false,
	}))
	if code != http.StatusOK {
		t.Fatalf("the apply must land, got %d body %v", code, body)
	}
	lines, _ := body["lines"].([]any)
	if len(lines) != 2 {
		t.Fatalf("the answer carries one line per named invoice, got %v", body)
	}
	first, _ := lines[0].(map[string]any)
	second, _ := lines[1].(map[string]any)
	if first["invoice_id"] != old || first["applied_cents"] != float64(30000) || first["outcome"] != "CLOSES" || first["balance_after_cents"] != float64(0) {
		t.Fatalf("the oldest closes first: %v", first)
	}
	if second["invoice_id"] != mid || second["applied_cents"] != float64(5000) || second["outcome"] != "PART" || second["balance_after_cents"] != float64(15000) {
		t.Fatalf("the next takes the remainder in part: %v", second)
	}
	if body["amount_cents"] != float64(35000) || body["applied_cents"] != float64(35000) || body["unapplied_cents"] != float64(0) || body["on_account_cents"] != float64(0) {
		t.Fatalf("the whole payment is applied: %v", body)
	}
	if body["replayed"] != false {
		t.Fatalf("a fresh apply is not a replay: %v", body)
	}
	if id, _ := body["payment_id"].(string); id == "" {
		t.Fatalf("the answer names the payment: %v", body)
	}
}

// A malformed customer_id is refused in words at the route, 422, never the raw 500 the lock
// statement's uuid cast used to surface (the delta reviews' P3): the id is validated and
// canonicalised at the walk's entry together with the invoice ids.
func TestApplyRoute_AMalformedCustomerIDIsRefusedInWords(t *testing.T) {
	w := newApplyRouteWorld(t)
	open := w.invoice(t, w.customerID, "MCF", 90, 60, 10000, "")

	for _, preview := range []bool{false, true} {
		shape := "apply"
		if preview {
			shape = "preview"
		}
		t.Run(shape, func(t *testing.T) {
			code, body := decodeApply(t, w.post(t, applyGlobalCaller(), map[string]any{
				"customer_id": "not-a-uuid", "method": "CHEQUE", "amount_cents": 100,
				"invoice_ids": []string{open}, "idempotency_key": w.tag + "-cid-" + shape, "preview": preview,
			}))
			if code != http.StatusUnprocessableEntity {
				t.Fatalf("a malformed customer id answers 422, got %d body %v", code, body)
			}
			words, _ := body["error"].(string)
			if !strings.Contains(words, "customer id must be a uuid") {
				t.Fatalf("the refusal names the customer id in words: %q", words)
			}
		})
	}
	var payRows int
	if err := w.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM payments WHERE customer_id = $1::uuid`, w.customerID).Scan(&payRows); err != nil {
		t.Fatalf("count payments: %v", err)
	}
	if payRows != 0 {
		t.Fatalf("a malformed customer id leaves nothing applied, found %d payment rows", payRows)
	}
}

// Every refusal is in words and leaves nothing applied: another account's invoice, an invoice
// outside a scoped caller's branch reach (the same no such invoice as a missing one, no existence
// leak across the branch wall), an amount over the selected invoices, an invoice that is not open,
// and a list with nothing in it.
func TestApplyRoute_RefusalsInWordsLeaveNothingApplied(t *testing.T) {
	w := newApplyRouteWorld(t)
	ctx := context.Background()
	open := w.invoice(t, w.customerID, "OPEN", 90, 60, 10000, "")
	foreign := w.invoice(t, w.otherID, "FRGN", 30, 10, 10000, "")
	kingston := w.invoice(t, w.customerID, "KGN", 60, 30, 10000, kingstonBranch)

	cases := []struct {
		name   string
		caller identity.StaffContext
		body   map[string]any
		code   int
		words  string
	}{
		{
			name:   "an invoice of another account",
			caller: applyGlobalCaller(),
			body: map[string]any{"customer_id": w.customerID, "method": "CHEQUE", "amount_cents": 20000,
				"invoice_ids": []string{open, foreign}, "idempotency_key": w.tag + "-a"},
			code: http.StatusNotFound, words: "no such invoice",
		},
		{
			name:   "an invoice outside a scoped caller's branch reach",
			caller: applyTrentonCaller(),
			body: map[string]any{"customer_id": w.customerID, "method": "CHEQUE", "amount_cents": 10000,
				"invoice_ids": []string{kingston}, "idempotency_key": w.tag + "-b"},
			code: http.StatusNotFound, words: "no such invoice",
		},
		{
			name:   "an invoice that is not open",
			caller: applyGlobalCaller(),
			body: map[string]any{"customer_id": w.customerID, "method": "CASH", "amount_cents": 10000,
				"invoice_ids": []string{notOpenInvoice(t, w, "DRAFT")}, "idempotency_key": w.tag + "-d"},
			code: http.StatusUnprocessableEntity, words: "is not open",
		},
		{
			name:   "no invoice named at all",
			caller: applyGlobalCaller(),
			body: map[string]any{"customer_id": w.customerID, "method": "CASH", "amount_cents": 10000,
				"invoice_ids": []string{}, "idempotency_key": w.tag + "-e"},
			code: http.StatusUnprocessableEntity, words: "at least one invoice",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := decodeApply(t, w.post(t, tc.caller, tc.body))
			if code != tc.code {
				t.Fatalf("status %d body %v, want %d", code, body, tc.code)
			}
			words, _ := body["error"].(string)
			if !strings.Contains(words, tc.words) {
				t.Fatalf("words %q must name %q", words, tc.words)
			}
		})
	}
	var payRows int
	if err := w.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM payments WHERE customer_id = $1::uuid`, w.customerID).Scan(&payRows); err != nil {
		t.Fatalf("count payments: %v", err)
	}
	if payRows != 0 {
		t.Fatalf("every refusal leaves nothing applied, found %d payment rows", payRows)
	}
	if b, _ := invoiceBalance(t, w.pool, open); b != 10000 {
		t.Fatalf("the open invoice is untouched, balance %d", b)
	}

	// An amount over the named invoice is no longer a refusal (W3 item 0.10): the route answers the
	// split, the invoice closes, and the route's answer carries the unapplied cents the account now
	// holds as its on account credit.
	code, body := decodeApply(t, w.post(t, applyGlobalCaller(), map[string]any{
		"customer_id": w.customerID, "method": "CASH", "amount_cents": 10001,
		"invoice_ids": []string{open}, "idempotency_key": w.tag + "-over",
	}))
	if code != http.StatusOK {
		t.Fatalf("the over amount answers the split, got status %d body %v", code, body)
	}
	if got := body["applied_cents"]; got != float64(10000) || body["unapplied_cents"] != float64(1) || body["on_account_cents"] != float64(1) {
		t.Fatalf("the answer splits 10000 applied and 1 on account, got %v", body)
	}
	if b, s := invoiceBalance(t, w.pool, open); b != 0 || s != "PAID" {
		t.Fatalf("the invoice closed on its share, got %d %s", b, s)
	}
	var changeSum int64
	if err := w.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
		 WHERE source_type = 'receipt_change' AND customer_id = $1::uuid`, w.customerID).Scan(&changeSum); err != nil {
		t.Fatalf("read the change row: %v", err)
	}
	if changeSum != -1 {
		t.Fatalf("the one cent over rests on the account, got %d", changeSum)
	}
}

// notOpenInvoice plants a document whose status is not an open one, the shape a draft or a voided
// credit presents.
func notOpenInvoice(t *testing.T, w *applyRouteWorld, status string) string {
	t.Helper()
	issue := d510Now.AddDate(0, 0, -30)
	var id string
	if err := w.pool.QueryRow(context.Background(),
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, doc_type)
		 VALUES ($1, $2::uuid, $3::uuid, $4, $5::date, $5::date, 10000, 0, 10000, 10000, 'INVOICE')
		 RETURNING id::text`,
		w.tag+"-"+status, w.customerID, trentonBranch, status, issue.Format("2006-01-02")).Scan(&id); err != nil {
		t.Fatalf("seed %s invoice: %v", status, err)
	}
	return id
}

func invoiceBalance(t *testing.T, pool *pgxpool.Pool, invoiceID string) (int64, string) {
	t.Helper()
	var balance int64
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT balance_cents, status::text FROM invoices WHERE id = $1::uuid`, invoiceID).
		Scan(&balance, &status); err != nil {
		t.Fatalf("read invoice: %v", err)
	}
	return balance, status
}

// A key that already recorded a payment is refused in words when the request describes a
// different payment: the desk that retries with a corrected amount must not be told "replayed"
// with the old figure and a success.
func TestApplyRoute_TheSameKeyWithADifferentBodyIsRefusedInWords(t *testing.T) {
	w := newApplyRouteWorld(t)
	ctx := context.Background()
	inv := w.invoice(t, w.customerID, "BODY", 90, 60, 12000, "")
	first := w.post(t, applyGlobalCaller(), map[string]any{
		"customer_id": w.customerID, "method": "CASH", "amount_cents": 12000,
		"reference": "CHQ 500", "invoice_ids": []string{inv}, "idempotency_key": w.tag,
	})
	if code, _ := decodeApply(t, first); code != http.StatusOK {
		t.Fatalf("the first apply must land, got %d", code)
	}

	code, body := decodeApply(t, w.post(t, applyGlobalCaller(), map[string]any{
		"customer_id": w.customerID, "method": "CASH", "amount_cents": 9000,
		"reference": "CHQ 500", "invoice_ids": []string{inv}, "idempotency_key": w.tag,
	}))
	if code != http.StatusConflict {
		t.Fatalf("a different body under a spent key answers the conflict, got %d body %v", code, body)
	}
	words, _ := body["error"].(string)
	if !strings.Contains(words, "that idempotency key already recorded a different payment") {
		t.Fatalf("the refusal says what happened: %q", words)
	}
	var payRows int
	if err := w.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM payments WHERE customer_id = $1::uuid`, w.customerID).Scan(&payRows); err != nil {
		t.Fatalf("count payments: %v", err)
	}
	if payRows != 1 {
		t.Fatalf("only the first body's payment exists, found %d rows", payRows)
	}
	if b, _ := invoiceBalance(t, w.pool, inv); b != 0 {
		t.Fatalf("the first payment's balance move stands, balance %d", b)
	}
}

// The preview flag answers the same split through the route and writes nothing the ledger can see.
func TestApplyRoute_PreviewWritesNothing(t *testing.T) {
	w := newApplyRouteWorld(t)
	inv := w.invoice(t, w.customerID, "PVW", 90, 60, 12000, "")
	before := tableCount(t, w.pool, "SELECT COUNT(*) FROM payments WHERE customer_id = $1::uuid", w.customerID)

	code, body := decodeApply(t, w.post(t, applyGlobalCaller(), map[string]any{
		"customer_id": w.customerID, "method": "EFT", "amount_cents": 4000,
		"invoice_ids": []string{inv}, "idempotency_key": w.tag, "preview": true,
	}))
	if code != http.StatusOK {
		t.Fatalf("preview must answer, got %d %v", code, body)
	}
	if body["preview"] != true {
		t.Fatalf("the answer says preview: %v", body)
	}
	if _, has := body["payment_id"]; has {
		t.Fatalf("a preview names no payment: %v", body)
	}
	after := tableCount(t, w.pool, "SELECT COUNT(*) FROM payments WHERE customer_id = $1::uuid", w.customerID)
	if before != after {
		t.Fatalf("preview wrote %d payment rows", after-before)
	}
	if b, _ := invoiceBalance(t, w.pool, inv); b != 12000 {
		t.Fatalf("the invoice is untouched, balance %d", b)
	}
}

func tableCount(t *testing.T, pool *pgxpool.Pool, query string, arg string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, arg).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// A promise kept is settled by the close: an invoice that carried a live promise and closes with
// this payment has the promise marked KEPT, exactly as the per invoice route settles one.
func TestApplyRoute_AClosedInvoiceSettlesItsPromise(t *testing.T) {
	w := newApplyRouteWorld(t)
	inv := w.invoice(t, w.customerID, "PRM", 90, 60, 8000, "")
	if _, err := w.pool.Exec(context.Background(), `
		INSERT INTO ar_promises_to_pay (invoice_id, customer_id, promised_cents, promised_date, promised_by, note)
		 VALUES ($1::uuid, $2::uuid, 8000, $3::date, '', 'will pay by cheque')`,
		inv, w.customerID, d510Now.AddDate(0, 0, 5).Format("2006-01-02")); err != nil {
		t.Fatalf("plant promise: %v", err)
	}

	code, _ := decodeApply(t, w.post(t, applyGlobalCaller(), map[string]any{
		"customer_id": w.customerID, "method": "CHEQUE", "amount_cents": 8000,
		"invoice_ids": []string{inv}, "idempotency_key": w.tag,
	}))
	if code != http.StatusOK {
		t.Fatalf("the apply must land: %d", code)
	}
	var settled *string
	var status string
	if err := w.pool.QueryRow(context.Background(),
		`SELECT settled_at::text, status FROM ar_promises_to_pay WHERE invoice_id = $1::uuid`, inv).
		Scan(&settled, &status); err != nil {
		t.Fatalf("read promise: %v", err)
	}
	if settled == nil || status != "KEPT" {
		t.Fatalf("a closed invoice settles its promise KEPT, got settled %v status %s", settled, status)
	}
}

// The route answers the replay before its service level guards and the body validation: a spent key
// retried with a zero amount or an empty invoice list hears the replay verdict, not "the payment was
// not recorded" validation words that would end the desk's held key as a refusal (FP 186 and 187).
func TestApplyRoute_TheReplayJudgementRunsBeforeBodyValidation(t *testing.T) {
	w := newApplyRouteWorld(t)
	inv := w.invoice(t, w.customerID, "RBV", 90, 60, 12000, "")
	first := w.post(t, applyGlobalCaller(), map[string]any{
		"customer_id": w.customerID, "method": "CASH", "amount_cents": 12000,
		"reference": "CHQ 500", "invoice_ids": []string{inv}, "idempotency_key": w.tag,
	})
	if code, _ := decodeApply(t, first); code != http.StatusOK {
		t.Fatalf("the first apply must land, got %d", code)
	}

	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"a zero amount", map[string]any{
			"customer_id": w.customerID, "method": "CASH", "amount_cents": 0,
			"reference": "CHQ 500", "invoice_ids": []string{inv}, "idempotency_key": w.tag}},
		{"an empty invoice list", map[string]any{
			"customer_id": w.customerID, "method": "CASH", "amount_cents": 12000,
			"reference": "CHQ 500", "invoice_ids": []string{}, "idempotency_key": w.tag}},
		{"an unknown tender", map[string]any{
			"customer_id": w.customerID, "method": "BOGUS", "amount_cents": 12000,
			"reference": "CHQ 500", "invoice_ids": []string{inv}, "idempotency_key": w.tag}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := decodeApply(t, w.post(t, applyGlobalCaller(), tc.body))
			if code != http.StatusConflict {
				t.Fatalf("a spent key with %s answers the conflict, got %d body %v", tc.name, code, body)
			}
			words, _ := body["error"].(string)
			if !strings.Contains(words, "that idempotency key already recorded a different payment") {
				t.Fatalf("the spent key answers the replay verdict, got %q", words)
			}
		})
	}

	// A body mangled in flight cannot carry a key at all, so it is the route's own 400 for a body
	// it could not read, never a 422 the desk would read as the route refusing a body it checked
	// against the replay table.
	mangled := httptest.NewRequest(http.MethodPost, "/api/v1/ar/payments/apply", strings.NewReader("{not json"))
	mangled = mangled.WithContext(identity.ContextWithStaff(mangled.Context(), applyGlobalCaller()))
	mrec := httptest.NewRecorder()
	w.h.applyReceived(mrec, mangled, guards.StaffCaller{})
	if mrec.Code != http.StatusBadRequest {
		t.Fatalf("an unreadable body answers 400, got %d body %q", mrec.Code, mrec.Body.String())
	}
	if !strings.Contains(mrec.Body.String(), "the request body must be JSON") {
		t.Fatalf("the unreadable body refusal says what happened: %q", mrec.Body.String())
	}
}

// lateDiscountWorld is the apply route with the late discount approval wall bound: the holder the
// composition root binds to the role gate, on a double the leg controls.
type lateDiscountWorld struct {
	*applyRouteWorld
	repo      *Repository
	approvals *LateDiscountApprovals
	held      *bool
}

func newLateDiscountWorld(t *testing.T, held bool) *lateDiscountWorld {
	t.Helper()
	w := &lateDiscountWorld{applyRouteWorld: newApplyRouteWorld(t)}
	w.repo = NewRepository(w.pool)
	w.approvals = &LateDiscountApprovals{}
	w.approvals.Bind(lateDiscountFakeSource{w: w})
	h := held
	w.held = &h
	w.h = NewRegisterHandler(w.svc, w.repo, nil).WithLateDiscountApprovals(w.approvals)
	return w
}

type lateDiscountFakeSource struct{ w *lateDiscountWorld }

func (f lateDiscountFakeSource) HoldsLateDiscountApproval(ctx context.Context, platformUserID, orgID string) (bool, error) {
	return *f.w.held, nil
}

// discountInvoiceOnTerms plants one open invoice on 2/10 net 30 whose window closed yesterday.
func (w *lateDiscountWorld) discountInvoiceOnTerms(t *testing.T, label string, total int64, daysAgo int) string {
	t.Helper()
	today := time.Now().In(houseday.DefaultZone().Location())
	day := today.AddDate(0, 0, -daysAgo)
	dayText := day.Format("2006-01-02")
	var id string
	if err := w.pool.QueryRow(context.Background(),
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                       discount_date, terms_code,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		 VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $5::date, $6::date, '2-10-NET30',
		         $7, 0, $7, $7, $4, 'INVOICE')
		 RETURNING id::text`,
		w.tag+"-"+label, w.customerID, trentonBranch,
		dayText, day.AddDate(0, 0, 30).Format("2006-01-02"), dayText, total).Scan(&id); err != nil {
		t.Fatalf("seed discount invoice %s: %v", label, err)
	}
	return id
}

// TestApplyRoute_LateDiscountClerkRefusedManagerApproved is item 2 at the route: an ask the
// approval wall does not admit is refused in words with nothing applied; the same ask from a
// caller the wall admits posts the discount with the approval recorded.
func TestApplyRoute_LateDiscountClerkRefusedManagerApproved(t *testing.T) {
	// The clerk: the wall answers no, and the walk's refusal is the answer the desk reads.
	clerk := newLateDiscountWorld(t, false)
	inv := clerk.discountInvoiceOnTerms(t, "LATE-CLERK", 100000, 1)
	code, body := decodeApply(t, clerk.post(t, applyGlobalCaller(), map[string]any{
		"customer_id": clerk.customerID, "method": "cheque", "amount_cents": 98000,
		"reference": "CHQ 931", "invoice_ids": []string{inv},
		"idempotency_key": clerk.tag + "-clerk", "late_discount_reason": "customer phoned",
	}))
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("a clerk's late discount ask is refused in words, got %d body %v", code, body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "window closed") || !strings.Contains(msg, "manager") {
		t.Fatalf("the refusal names the closed window and asks for a manager, got %q", msg)
	}
	if balance, _ := clerkInvoiceState(t, clerk.pool, inv); balance != 100000 {
		t.Fatalf("the refused ask applied nothing, balance %d", balance)
	}

	// The manager: the same ask through the same route, the wall admitting the caller, with the
	// approver a real staff user the approval row names.
	manager := newLateDiscountWorld(t, true)
	inv2 := manager.discountInvoiceOnTerms(t, "LATE-MGR", 100000, 1)
	var approver string
	if err := manager.pool.QueryRow(context.Background(),
		`INSERT INTO users (email, display_name, status)
		 VALUES ($1, 'TM2 Route Approver', 'ACTIVE') RETURNING id::text`,
		manager.tag+"-approver@example.test").Scan(&approver); err != nil {
		t.Fatalf("seed the approver: %v", err)
	}
	manager.last = append(manager.last, func(c context.Context) {
		if _, err := manager.pool.Exec(c, `DELETE FROM users WHERE id = $1::uuid`, approver); err != nil {
			t.Errorf("cleanup of users failed: %v", err)
		}
	})
	caller := applyGlobalCaller()
	caller.UserID = approver
	code2, body2 := decodeApply(t, manager.post(t, caller, map[string]any{
		"customer_id": manager.customerID, "method": "cheque", "amount_cents": 98000,
		"reference": "CHQ 932", "invoice_ids": []string{inv2},
		"idempotency_key": manager.tag + "-mgr", "late_discount_reason": "cheque mailed inside the window",
	}))
	if code2 != http.StatusOK {
		t.Fatalf("a manager's approved late discount posts, got %d body %v", code2, body2)
	}
	lines, _ := body2["lines"].([]any)
	if len(lines) != 1 {
		t.Fatalf("the answer carries one line, got %v", body2["lines"])
	}
	line := lines[0].(map[string]any)
	if line["discount_cents"].(float64) != 2000 || line["outcome"] != "CLOSES" {
		t.Fatalf("the approved discount closes the line with the discount: %v", line)
	}
	var n int
	if err := manager.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM invoice_discount_approvals WHERE invoice_id = $1::uuid`, inv2).Scan(&n); err != nil {
		t.Fatalf("read the approval rows: %v", err)
	}
	if n != 1 {
		t.Fatalf("the approval is recorded once, found %d", n)
	}
}

// clerkInvoiceState reads one invoice's balance.
func clerkInvoiceState(t *testing.T, pool *pgxpool.Pool, invoiceID string) (int64, string) {
	t.Helper()
	var balance int64
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT balance_cents, status::text FROM invoices WHERE id = $1::uuid`, invoiceID).
		Scan(&balance, &status); err != nil {
		t.Fatalf("read invoice: %v", err)
	}
	return balance, status
}
