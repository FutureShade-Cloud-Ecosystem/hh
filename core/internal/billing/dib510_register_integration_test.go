//go:build integration

// dib510_register_integration_test.go, the register's SQL, against the real schema.
//
// The decisions are unit-tested next door; what is only provable here is the projection itself: that
// the chip counts and the filtered list are computed from the same predicates over the same rows,
// that the four LATERAL/EXISTS legs (dispute, promise, credit hold, last send) attach to the right
// document without multiplying it, that the account-position gate is BYTES and not a formatting
// choice, and that each write's idempotency is enforced by an index rather than by a Go variable.
//
// Every fact is created by the test inside its own customer and torn down LIFO. The reads are fenced
// by CustomerID so nothing the seed or another test wrote can move a number this file asserts.
//
// Run: go test -tags integration ./internal/billing/...
package billing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// d510Now is the instant every aging assertion in this file is made against. Fixed, so "68 days
// overdue" stays 68 whatever day somebody runs the suite.
var d510Now = time.Date(2026, time.August, 26, 10, 0, 0, 0, time.UTC)

// randomTag is 8 hex characters of entropy, enough to keep two concurrent runs off each other's
// UNIQUE invoice_no / account_no, with no clock involved.
func randomTag() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail in practice; if it ever did, a fixed tag fails loudly on the
		// UNIQUE index rather than silently sharing a namespace with another run.
		return "fixed"
	}
	return hex.EncodeToString(b[:])
}

type d510Fixture struct {
	pool       *pgxpool.Pool
	customerID string
	tag        string
	invoices   map[string]string // label → id
}

func newD510Fixture(t *testing.T) *d510Fixture {
	t.Helper()
	pool := intPool(t)
	ctx := context.Background()
	// THE NAMESPACE IS RANDOM, NOT TIMESTAMPED, and that is not fussiness. This file pins d510Now so
	// its aging assertions are deterministic; a fixture tag built from time.Now() would put a real
	// clock in the same file as a fixed date, which is exactly the shape testaudit's DIB-427 gate
	// refuses, and it refuses it because the next person to add an assertion here would not know the
	// two were unrelated.
	tag := "D510R-" + randomTag()

	f := &d510Fixture{pool: pool, tag: tag, invoices: map[string]string{}}
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents, payment_terms_days)
		 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 500000, 30) RETURNING id::text`,
		tag, "DIB-510 register "+tag, trentonBranch).Scan(&f.customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM ar_invoice_deliveries WHERE customer_id = $1::uuid`, f.customerID)
		_, _ = pool.Exec(c, `DELETE FROM ar_promises_to_pay WHERE customer_id = $1::uuid`, f.customerID)
		_, _ = pool.Exec(c, `DELETE FROM ar_invoice_disputes WHERE customer_id = $1::uuid`, f.customerID)
		_, _ = pool.Exec(c, `DELETE FROM credit_holds WHERE customer_id = $1::uuid`, f.customerID)
		_, _ = pool.Exec(c, `DELETE FROM payment_reapplications WHERE customer_id = $1::uuid`, f.customerID)
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'customer_id' = $1`, f.customerID)
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, f.customerID)
		_, _ = pool.Exec(c, `DELETE FROM payment_allocations WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`, f.customerID)
		_, _ = pool.Exec(c, `UPDATE payments SET reversed_by_payment_id = NULL WHERE customer_id = $1::uuid`, f.customerID)
		_, _ = pool.Exec(c, `DELETE FROM payment_allocations WHERE payment_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid)`, f.customerID)
		_, _ = pool.Exec(c, `DELETE FROM payments WHERE customer_id = $1::uuid`, f.customerID)
		_, _ = pool.Exec(c, `DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`, f.customerID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, f.customerID)
		_, _ = pool.Exec(c, `DELETE FROM customer_contacts WHERE customer_id = $1::uuid`, f.customerID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, f.customerID)
	})
	return f
}

// invoice creates one document dated RELATIVE TO d510Now, so the aging arithmetic is deterministic.
func (f *d510Fixture) invoice(t *testing.T, label, status string, dueOffsetDays int, total, balance int64, docType string) string {
	t.Helper()
	due := d510Now.AddDate(0, 0, dueOffsetDays)
	issue := due.AddDate(0, 0, -30)
	var id string
	if err := f.pool.QueryRow(context.Background(),
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		 VALUES ($1, $2::uuid, $3::uuid, $4, $5::date, $6::date, $7, 0, $7, $8, $9, $10)
		 RETURNING id::text`,
		f.tag+"-"+label, f.customerID, trentonBranch, status,
		issue.Format("2006-01-02"), due.Format("2006-01-02"), total, balance, issue, docType).Scan(&id); err != nil {
		t.Fatalf("seed invoice %s: %v", label, err)
	}
	f.invoices[label] = id
	return id
}

func (f *d510Fixture) service(caller func(context.Context) (identity.StaffContext, bool)) *RegisterService {
	return NewRegisterService(NewRepository(f.pool)).
		WithClock(func() time.Time { return d510Now }).
		withCaller(caller)
}

func d510Manager() func(context.Context) (identity.StaffContext, bool) {
	return func(context.Context) (identity.StaffContext, bool) {
		return identity.StaffContext{UserID: "", Tier: identity.TierManager, Scope: identity.ScopeGlobal, Status: "ACTIVE"}, true
	}
}

func d510Frontline() func(context.Context) (identity.StaffContext, bool) {
	return func(context.Context) (identity.StaffContext, bool) {
		return identity.StaffContext{UserID: "", Tier: identity.TierFrontline, Scope: identity.ScopeBranch, Status: "ACTIVE",
			Branches: map[string]bool{trentonBranch: true}}, true
	}
}

// -- the projection -----------------------------------------------------------------------------

func TestRegister_ChipsCountTheSamePredicatesTheFilterApplies(t *testing.T) {
	f := newD510Fixture(t)
	ctx := context.Background()
	f.invoice(t, "OPEN", "FINALIZED", 40, 100000, 100000, "INVOICE") // open, beyond the window
	f.invoice(t, "SOON", "FINALIZED", 3, 50000, 50000, "INVOICE")    // open AND due soon
	f.invoice(t, "LATE1", "FINALIZED", -12, 70000, 70000, "INVOICE") // open AND overdue
	f.invoice(t, "LATE2", "PARTIALLY_PAID", -47, 80000, 30000, "INVOICE")
	f.invoice(t, "PAID", "PAID", -5, 20000, 0, "INVOICE")
	f.invoice(t, "CREDIT", "FINALIZED", -1, -9000, -9000, "CREDIT_MEMO")

	svc := f.service(d510Manager())
	page, err := svc.Register(ctx, RegisterFilter{CustomerID: f.customerID, ShowCredits: true, Limit: 50})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// Open is the WHOLE working set, every unpaid document, and due-soon/overdue are SUBSETS of it.
	if page.Counts.Open != 4 {
		t.Fatalf("open counts every unpaid document (4), got %d", page.Counts.Open)
	}
	if page.Counts.DueSoon != 1 {
		t.Fatalf("due soon = 1, got %d", page.Counts.DueSoon)
	}
	if page.Counts.Overdue != 2 {
		t.Fatalf("overdue = 2, got %d", page.Counts.Overdue)
	}
	if page.Counts.Paid != 1 {
		t.Fatalf("paid = 1, got %d", page.Counts.Paid)
	}
	if page.Counts.Credits != 1 {
		t.Fatalf("credits = 1, got %d", page.Counts.Credits)
	}

	// And the chip and the list agree, the property that makes a chip a chip.
	for chip, want := range map[string]int{
		StatusOpen: page.Counts.Open, StatusDueSoon: page.Counts.DueSoon,
		StatusOverdue: page.Counts.Overdue, StatusPaid: page.Counts.Paid,
	} {
		got, err := svc.Register(ctx, RegisterFilter{CustomerID: f.customerID, Chip: chip, Limit: 50})
		if err != nil {
			t.Fatalf("register %s: %v", chip, err)
		}
		if got.Total != want {
			t.Fatalf("chip %s says %d but the filtered list holds %d", chip, want, got.Total)
		}
	}
}

func TestRegister_ShowCreditsGovernsTheListNotTheCount(t *testing.T) {
	f := newD510Fixture(t)
	f.invoice(t, "INV", "FINALIZED", 10, 100000, 100000, "INVOICE")
	f.invoice(t, "CM", "FINALIZED", -1, -9000, -9000, "CREDIT_MEMO")
	svc := f.service(d510Manager())

	off, err := svc.Register(context.Background(), RegisterFilter{CustomerID: f.customerID, Limit: 50})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if off.Total != 1 {
		t.Fatalf("credits hidden: the list holds only the invoice, got %d", off.Total)
	}
	// The COUNT still says a credit exists, otherwise the toggle that would reveal it advertises zero.
	if off.Counts.Credits != 1 {
		t.Fatalf("the credits count is not suppressed by the toggle, got %d", off.Counts.Credits)
	}
	on, err := svc.Register(context.Background(), RegisterFilter{CustomerID: f.customerID, ShowCredits: true, Limit: 50})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if on.Total != 2 {
		t.Fatalf("credits shown: 2 documents, got %d", on.Total)
	}
}

func TestRegister_SearchFindsADocumentByEveryNumberSomebodyMightReadOut(t *testing.T) {
	f := newD510Fixture(t)
	id := f.invoice(t, "SEARCH", "FINALIZED", 10, 100000, 100000, "INVOICE")
	ctx := context.Background()

	// An order with a customer PO, linked to the invoice.
	var orderID string
	if err := f.pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, po_job_ref)
		 VALUES ($1, $2::uuid, $3::uuid, 'INVOICED', 'DELIVERY', $4) RETURNING id::text`,
		"SO-"+f.tag, f.customerID, trentonBranch, "PO-ZZZ-"+f.tag).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = f.pool.Exec(c, `UPDATE invoices SET order_id = NULL WHERE id = $1::uuid`, id)
		_, _ = f.pool.Exec(c, `DELETE FROM orders WHERE id = $1::uuid`, orderID)
	})
	if _, err := f.pool.Exec(ctx, `UPDATE invoices SET order_id = $2::uuid WHERE id = $1::uuid`, id, orderID); err != nil {
		t.Fatalf("link order: %v", err)
	}

	svc := f.service(d510Manager())
	for name, term := range map[string]string{
		"invoice number": f.tag + "-SEARCH",
		"customer name":  "DIB-510 register " + f.tag,
		"account number": f.tag,
		"order number":   "SO-" + f.tag,
		"customer PO":    "PO-ZZZ-" + f.tag,
	} {
		page, err := svc.Register(ctx, RegisterFilter{Search: term, Limit: 50})
		if err != nil {
			t.Fatalf("search by %s: %v", name, err)
		}
		found := false
		for _, r := range page.Items {
			if r.ID == id {
				found = true
			}
		}
		if !found {
			t.Fatalf("search by %s (%q) did not find the document", name, term)
		}
	}
}

func TestRegister_TheFourSideFactsAttachWithoutMultiplyingTheRow(t *testing.T) {
	f := newD510Fixture(t)
	ctx := context.Background()
	id := f.invoice(t, "SIDE", "FINALIZED", -68, 100000, 100000, "INVOICE")
	repo := NewRepository(f.pool)

	// a dispute, a promise, a hold, and TWO sends (the newest must win, and the row must not double)
	if _, _, _, err := repo.OpenDispute(ctx, DisputeWrite{
		InvoiceID: id, CustomerID: f.customerID, Reason: "SHORT_DELIVERY", AmountCents: 1000,
		IdempotencyKey: f.tag + "-disp"}); err != nil {
		t.Fatalf("dispute: %v", err)
	}
	if _, _, _, err := repo.AddPromise(ctx, PromiseWrite{
		InvoiceID: id, CustomerID: f.customerID, PromisedCents: 50000,
		PromisedDate:   d510Now.AddDate(0, 0, 5).Format("2006-01-02"),
		IdempotencyKey: f.tag + "-prom"}); err != nil {
		t.Fatalf("promise: %v", err)
	}
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO credit_holds (customer_id, status, reason, hold_kind) VALUES ($1::uuid,'ACTIVE','test','AR_AGING')`,
		f.customerID); err != nil {
		t.Fatalf("hold: %v", err)
	}
	for i, spec := range []struct {
		key    string
		opened bool
		ago    time.Duration
	}{{"-send-old", true, 72 * time.Hour}, {"-send-new", false, time.Hour}} {
		var did string
		did, _, err := repo.RecordSend(ctx, SendRow{
			InvoiceID: id, CustomerID: f.customerID, InvoiceNo: f.tag + "-SIDE", Kind: "INVOICE",
			Channel: "EMAIL", ToAddress: "a@b.example", IdempotencyKey: f.tag + spec.key})
		if err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		if _, err := f.pool.Exec(ctx, `UPDATE ar_invoice_deliveries SET sent_at = $2 WHERE id = $1::uuid`,
			did, d510Now.Add(-spec.ago)); err != nil {
			t.Fatalf("backdate send: %v", err)
		}
		if spec.opened {
			if _, err := repo.MarkOpened(ctx, did, d510Now.Add(-spec.ago).Add(time.Hour)); err != nil {
				t.Fatalf("mark opened: %v", err)
			}
		}
	}

	page, err := f.service(d510Manager()).Register(ctx, RegisterFilter{CustomerID: f.customerID, Limit: 50})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("ONE document with four side facts must produce ONE row, got %d", len(page.Items))
	}
	row := page.Items[0]
	if !row.Disputed || row.Status != StatusDisputed {
		t.Fatalf("the open dispute must reach the row and win the pill, got %+v", row.Status)
	}
	if row.DaysOverdue != 68 {
		t.Fatalf("the day count survives the dispute, got %d", row.DaysOverdue)
	}
	if row.PromisedCents != 50000 || row.PromisedDate == nil {
		t.Fatalf("the live promise must reach the row, got %d/%v", row.PromisedCents, row.PromisedDate)
	}
	if row.AccountStatus != AccountCreditHold {
		t.Fatalf("an active hold outranks the dispute on the ACCOUNT line, got %s", row.AccountStatus)
	}
	// The NEWEST send wins, and it was never opened, so the register must not claim it was.
	if row.Opened == nil || *row.Opened {
		t.Fatalf("the newest send is the one reported, and it is unopened: %v", row.Opened)
	}
}

func TestRegister_NeverSentMeansOpenedIsAbsentNotFalse(t *testing.T) {
	f := newD510Fixture(t)
	f.invoice(t, "QUIET", "FINALIZED", -3, 1000, 1000, "INVOICE")
	page, err := f.service(d510Manager()).Register(context.Background(),
		RegisterFilter{CustomerID: f.customerID, Limit: 50})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if page.Items[0].Opened != nil {
		t.Fatal(`"not opened" is an accusation we have not earned if we never sent it, the field must be absent`)
	}
	blob, _ := json.Marshal(page.Items[0])
	if strings.Contains(string(blob), `"opened"`) {
		t.Fatalf("absent means no JSON key: %s", blob)
	}
}

// -- the gate, in bytes -------------------------------------------------------------------------

func TestRegister_AccountPositionIsByteAbsentForAFrontlineCaller(t *testing.T) {
	f := newD510Fixture(t)
	invID := f.invoice(t, "GATED", "FINALIZED", -10, 250000, 250000, "INVOICE")
	ctx := context.Background()

	mgr, err := f.service(d510Manager()).Register(ctx, RegisterFilter{CustomerID: f.customerID, Limit: 50})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if mgr.Items[0].Account == nil {
		t.Fatal("a manager receives the account position")
	}
	mgrJSON, _ := json.Marshal(mgr)
	for _, want := range []string{"credit_limit_cents", `"days_overdue"`, `"bucket_label"`, `"bucket_key"`} {
		if !strings.Contains(string(mgrJSON), want) {
			t.Fatalf("the manager payload carries %s: %s", want, mgrJSON)
		}
	}

	front, err := f.service(d510Frontline()).Register(ctx, RegisterFilter{CustomerID: f.customerID, Limit: 50})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	frontJSON, _ := json.Marshal(front)
	// THE ASSERTION THAT MATTERS: the bytes are not there. Not empty, not zero, not hidden, absent.
	for _, gated := range []string{"credit_limit_cents", "available_credit_cents", "open_balance_cents", `"account"`, `"days_overdue"`, `"bucket_label"`, `"bucket_key"`} {
		if strings.Contains(string(frontJSON), gated) {
			t.Errorf("a refused caller's register payload must not contain %q at all: %s", gated, frontJSON)
		}
	}
	// The invoice's OWN money still reaches them, the customer holds that document.
	for _, keep := range []string{`"invoice_no"`, `"issue_date"`, `"due_date"`, `"total_cents":250000`, `"balance_cents":250000`} {
		if !strings.Contains(string(frontJSON), keep) {
			t.Errorf("register row must keep invoice document figure %s: %s", keep, frontJSON)
		}
	}

	// Same rule on the invoice detail route (GET /api/v1/ar/register/invoices/{id}).
	detailFront, err := f.service(d510Frontline()).Detail(ctx, invID, false)
	if err != nil {
		t.Fatalf("invoice detail: %v", err)
	}
	detailFrontJSON, _ := json.Marshal(detailFront)
	for _, gated := range []string{"credit_limit_cents", "available_credit_cents", "open_balance_cents", `"account"`, `"days_overdue"`} {
		if strings.Contains(string(detailFrontJSON), gated) {
			t.Errorf("a refused caller's invoice detail payload must not contain %q at all: %s", gated, detailFrontJSON)
		}
	}
	for _, keep := range []string{`"invoice_no"`, `"issue_date"`, `"due_date"`, `"total_cents":250000`, `"balance_cents":250000`} {
		if !strings.Contains(string(detailFrontJSON), keep) {
			t.Errorf("invoice detail must keep invoice document figure %s: %s", keep, detailFrontJSON)
		}
	}

	// Round 2 (S6 P1-1): the account's OPEN CREDITS are an account level figure like the position,
	// so a caller the position policy refuses receives no open_credits bytes at all, while the
	// document's own credit_applications stay (they are this invoice's own acts). The manager sees
	// both.
	memoID := f.invoice(t, "CRED", "FINALIZED", -5, -15000, -15000, "CREDIT_MEMO")
	applySvc := NewCreditApplyService(NewRepository(f.pool))
	if _, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: f.customerID, CreditKind: CreditKindMemo, CreditID: memoID, InvoiceID: invID,
		AmountCents: 5000, Reason: "byte absence leg", IdempotencyKey: f.tag + "-apply",
	}, true); err != nil {
		t.Fatalf("apply memo to the gated invoice: %v", err)
	}
	mgrDetail2, err := f.service(d510Manager()).Detail(ctx, invID, false)
	if err != nil {
		t.Fatalf("manager detail with credits: %v", err)
	}
	mgrDetail2JSON, _ := json.Marshal(mgrDetail2)
	for _, want := range []string{`"open_credits"`, `"credit_applications"`} {
		if !strings.Contains(string(mgrDetail2JSON), want) {
			t.Errorf("an allowed manager's detail carries %s: %s", want, mgrDetail2JSON)
		}
	}
	frontDetail2, err := f.service(d510Frontline()).Detail(ctx, invID, false)
	if err != nil {
		t.Fatalf("frontline detail with credits: %v", err)
	}
	frontDetail2JSON, _ := json.Marshal(frontDetail2)
	if strings.Contains(string(frontDetail2JSON), `"open_credits"`) {
		t.Errorf("a refused caller's invoice detail must not contain %q at all: %s", "open_credits", frontDetail2JSON)
	}
	if !strings.Contains(string(frontDetail2JSON), `"credit_applications"`) {
		t.Errorf("the invoice's own credit applications stay on a refused caller's detail: %s", frontDetail2JSON)
	}
}

// TestRegister_ARefusedCallerCarriesTheStoredStatusAndNoPastDueFlags pins round 6 (S5 R5.6, row
// 3172): the derived OVERDUE and DUE_SOON states and the overdue and due-soon counts are the past
// due flag told another way, so a caller the account position policy refuses receives the STORED
// status in `status` and a counts row with no overdue and no due_soon key, on the list route and on
// the invoice detail alike. The due date and the balance stay, the invoice's own document figures.
// An allowed manager still receives the derived state and both counts.
func TestRegister_ARefusedCallerCarriesTheStoredStatusAndNoPastDueFlags(t *testing.T) {
	f := newD510Fixture(t)
	ctx := context.Background()
	invID := f.invoice(t, "LATE", "FINALIZED", -12, 70000, 70000, "INVOICE") // overdue
	f.invoice(t, "SOON", "FINALIZED", 3, 50000, 50000, "INVOICE")            // due soon

	front, err := f.service(d510Frontline()).Register(ctx, RegisterFilter{CustomerID: f.customerID, Limit: 50})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	frontJSON, _ := json.Marshal(front)
	for _, gated := range []string{"OVERDUE", "DUE_SOON", `"overdue"`, `"due_soon"`} {
		if strings.Contains(string(frontJSON), gated) {
			t.Errorf("a refused clerk's register bytes must not carry %s at all: %s", gated, frontJSON)
		}
	}
	for _, keep := range []string{`"status":"FINALIZED"`, `"stored_status":"FINALIZED"`, `"due_date"`, `"balance_cents":70000`} {
		if !strings.Contains(string(frontJSON), keep) {
			t.Errorf("the register row keeps the stored status and the document figures, want %s in %s", keep, frontJSON)
		}
	}

	mgr, err := f.service(d510Manager()).Register(ctx, RegisterFilter{CustomerID: f.customerID, Limit: 50})
	if err != nil {
		t.Fatalf("manager register: %v", err)
	}
	mgrJSON, _ := json.Marshal(mgr)
	for _, want := range []string{"OVERDUE", "DUE_SOON", `"overdue":1`, `"due_soon":1`} {
		if !strings.Contains(string(mgrJSON), want) {
			t.Errorf("an allowed manager still receives %s: %s", want, mgrJSON)
		}
	}

	detailFront, err := f.service(d510Frontline()).Detail(ctx, invID, false)
	if err != nil {
		t.Fatalf("invoice detail: %v", err)
	}
	detailFrontJSON, _ := json.Marshal(detailFront)
	for _, gated := range []string{"OVERDUE", "DUE_SOON"} {
		if strings.Contains(string(detailFrontJSON), gated) {
			t.Errorf("a refused clerk's invoice detail must not carry %s at all: %s", gated, detailFrontJSON)
		}
	}
	if !strings.Contains(string(detailFrontJSON), `"status":"FINALIZED"`) {
		t.Errorf("the refused detail carries the stored status in status: %s", detailFrontJSON)
	}
	detailMgr, err := f.service(d510Manager()).Detail(ctx, invID, false)
	if err != nil {
		t.Fatalf("manager detail: %v", err)
	}
	if got := detailMgr.Status; got != StatusOverdue {
		t.Errorf("the manager's derived status for a 12 day late invoice is OVERDUE, got %s", got)
	}
}

// TestRegister_ThePastDueChipsRefuseACallerTheGateRefuses pins the filter half of round 6: the
// overdue and past due chips ask for the withheld predicate AS A LIST, so the service refuses them
// in words for a caller the account position policy refuses, while the chips that name no derived
// predicate still answer.
func TestRegister_ThePastDueChipsRefuseACallerTheGateRefuses(t *testing.T) {
	f := newD510Fixture(t)
	f.invoice(t, "LATE", "FINALIZED", -12, 70000, 70000, "INVOICE")
	ctx := context.Background()

	for _, chip := range []string{StatusOverdue, ChipPastDue, "past_due"} {
		_, err := f.service(d510Frontline()).Register(ctx, RegisterFilter{CustomerID: f.customerID, Chip: chip, Limit: 50})
		if err == nil {
			t.Fatalf("chip %s must refuse a caller the account position policy refuses", chip)
		}
	}
	// The chips that name no past due predicate keep answering the same caller.
	page, err := f.service(d510Frontline()).Register(ctx, RegisterFilter{CustomerID: f.customerID, Chip: StatusOpen, Limit: 50})
	if err != nil {
		t.Fatalf("the open chip still answers a refused caller: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("the open chip reads the book, got total %d", page.Total)
	}
	// And a manager filters by the same chips still.
	if _, err := f.service(d510Manager()).Register(ctx, RegisterFilter{CustomerID: f.customerID, Chip: StatusOverdue, Limit: 50}); err != nil {
		t.Fatalf("the overdue chip still answers an allowed manager: %v", err)
	}
}

// TestRegister_TheCurrentAndDueSoonChipsRefuseACallerTheGateRefuses pins item 220 (S5 R6 P3): the
// current chip is the complement of the past due predicate, so ALL less CURRENT lists the overdue
// accounts by subtraction, and due soon is the one derived predicate left as a server list. A
// caller the account position policy refuses hears the refusal in words and receives no rows for
// either, so the refused status filter offers stored statuses only. The manager keeps every chip.
func TestRegister_TheCurrentAndDueSoonChipsRefuseACallerTheGateRefuses(t *testing.T) {
	f := newD510Fixture(t)
	ctx := context.Background()
	f.invoice(t, "LATE", "FINALIZED", -12, 70000, 70000, "INVOICE") // overdue
	f.invoice(t, "SOON", "FINALIZED", 3, 50000, 50000, "INVOICE")   // due soon

	for _, chip := range []string{"CURRENT", ChipCurrent, StatusDueSoon, "due_soon"} {
		_, err := f.service(d510Frontline()).Register(ctx, RegisterFilter{CustomerID: f.customerID, Chip: chip, Limit: 50})
		if err == nil {
			t.Fatalf("chip %s must refuse a caller the account position policy refuses", chip)
		}
	}
	// The manager still gets both, one row each.
	for _, chip := range []string{"CURRENT", StatusDueSoon} {
		page, err := f.service(d510Manager()).Register(ctx, RegisterFilter{CustomerID: f.customerID, Chip: chip, Limit: 50})
		if err != nil {
			t.Fatalf("chip %s must still answer an allowed manager: %v", chip, err)
		}
		if page.Total != 1 {
			t.Fatalf("chip %s reads its one row, got total %d", chip, page.Total)
		}
	}
}

// -- the writes ---------------------------------------------------------------------------------

func TestRegisterWrites_IdempotencyIsTheIndexNotAVariable(t *testing.T) {
	f := newD510Fixture(t)
	ctx := context.Background()
	id := f.invoice(t, "IDEM", "FINALIZED", -30, 100000, 100000, "INVOICE")
	repo := NewRepository(f.pool)

	// a send, twice on one key
	if _, replayed, err := repo.RecordSend(ctx, SendRow{InvoiceID: id, CustomerID: f.customerID,
		InvoiceNo: "x", Kind: "REMINDER", Tone: ToneFirm, Channel: "EMAIL", IdempotencyKey: f.tag + "-s"}); err != nil || replayed {
		t.Fatalf("first send: err=%v replayed=%v", err, replayed)
	}
	if _, replayed, err := repo.RecordSend(ctx, SendRow{InvoiceID: id, CustomerID: f.customerID,
		InvoiceNo: "x", Kind: "REMINDER", Tone: ToneFirm, Channel: "EMAIL", IdempotencyKey: f.tag + "-s"}); err != nil || !replayed {
		t.Fatalf("second send must replay: err=%v replayed=%v", err, replayed)
	}
	var sends int
	_ = f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM ar_invoice_deliveries WHERE invoice_id = $1::uuid`, id).Scan(&sends)
	if sends != 1 {
		t.Fatalf("one key, one send row, found %d", sends)
	}

	// a promise, twice on one key, then a SECOND promise on a different key, refused as a conflict
	if _, _, _, err := repo.AddPromise(ctx, PromiseWrite{InvoiceID: id, CustomerID: f.customerID,
		PromisedCents: 1000, PromisedDate: d510Now.AddDate(0, 0, 3).Format("2006-01-02"),
		IdempotencyKey: f.tag + "-p"}); err != nil {
		t.Fatalf("promise: %v", err)
	}
	if _, _, conflict, err := repo.AddPromise(ctx, PromiseWrite{InvoiceID: id, CustomerID: f.customerID,
		PromisedCents: 1000, PromisedDate: d510Now.AddDate(0, 0, 3).Format("2006-01-02"),
		IdempotencyKey: f.tag + "-p"}); err != nil || conflict {
		t.Fatalf("a replay is a replay, not a conflict: err=%v conflict=%v", err, conflict)
	}
	if _, _, conflict, err := repo.AddPromise(ctx, PromiseWrite{InvoiceID: id, CustomerID: f.customerID,
		PromisedCents: 2000, PromisedDate: d510Now.AddDate(0, 0, 9).Format("2006-01-02"),
		IdempotencyKey: f.tag + "-p2"}); err != nil || !conflict {
		t.Fatalf("a SECOND live promise must be refused as a conflict: err=%v conflict=%v", err, conflict)
	}
	var promises int
	_ = f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM ar_promises_to_pay WHERE invoice_id = $1::uuid`, id).Scan(&promises)
	if promises != 1 {
		t.Fatalf("one open promise per invoice, found %d", promises)
	}

	// settling it makes room for the next one, the conflict is about LIVE promises, not history
	if ok, err := repo.SettlePromise(ctx, id, "BROKEN", "date passed"); err != nil || !ok {
		t.Fatalf("settle: err=%v ok=%v", err, ok)
	}
	if _, _, conflict, err := repo.AddPromise(ctx, PromiseWrite{InvoiceID: id, CustomerID: f.customerID,
		PromisedCents: 2000, PromisedDate: d510Now.AddDate(0, 0, 9).Format("2006-01-02"),
		IdempotencyKey: f.tag + "-p3"}); err != nil || conflict {
		t.Fatalf("a settled promise leaves room for the next: err=%v conflict=%v", err, conflict)
	}
	_ = f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM ar_promises_to_pay WHERE invoice_id = $1::uuid`, id).Scan(&promises)
	if promises != 2 {
		t.Fatalf("the broken promise is KEPT as history, not deleted; expected 2 rows, found %d", promises)
	}
}

func TestRegisterWrites_MarkOpenedStampsFirstOpenAndCountsEvery(t *testing.T) {
	f := newD510Fixture(t)
	ctx := context.Background()
	id := f.invoice(t, "OPENS", "FINALIZED", -5, 1000, 1000, "INVOICE")
	repo := NewRepository(f.pool)
	did, _, err := repo.RecordSend(ctx, SendRow{InvoiceID: id, CustomerID: f.customerID,
		InvoiceNo: "x", Kind: "INVOICE", Channel: "EMAIL", IdempotencyKey: f.tag + "-o"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	first := d510Now.Add(-2 * time.Hour)
	if ok, err := repo.MarkOpened(ctx, did, first); err != nil || !ok {
		t.Fatalf("first open: err=%v ok=%v", err, ok)
	}
	if ok, err := repo.MarkOpened(ctx, did, d510Now); err != nil || !ok {
		t.Fatalf("second open: err=%v ok=%v", err, ok)
	}
	var openedAt time.Time
	var count int
	if err := f.pool.QueryRow(ctx, `SELECT opened_at, open_count FROM ar_invoice_deliveries WHERE id = $1::uuid`, did).
		Scan(&openedAt, &count); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !openedAt.UTC().Equal(first.UTC()) {
		t.Fatalf("opened_at is the FIRST open and never moves, got %v want %v", openedAt, first)
	}
	if count != 2 {
		t.Fatalf("every open is counted, got %d", count)
	}
	// A fabricated id writes nothing and says so.
	if ok, err := repo.MarkOpened(ctx, "00000000-0000-4000-8000-000000000000", d510Now); err != nil || ok {
		t.Fatalf("an unknown delivery id is discarded: err=%v ok=%v", err, ok)
	}
}

func TestRegisterPolicy_ResolvesTheDealerDefaultAndItIsRestrictive(t *testing.T) {
	pool := intPool(t)
	p, err := NewRepository(pool).RegisterPolicyFor(context.Background(), "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	if p.AccountPositionVisibility != VisibilityManagerUp {
		t.Fatalf("the shipped default must be the restrictive one, got %s", p.AccountPositionVisibility)
	}
	if p.DueSoonDays <= 0 {
		t.Fatalf("the due-soon window must be a real number of days, got %d", p.DueSoonDays)
	}
	if len(p.ReminderTones) == 0 {
		t.Fatal("the dealer default offers tones")
	}
}

// -- the detail's totals ------------------------------------------------------------------------

func TestDetail_AppliedIsTheAllocationAggregateNotTotalMinusBalance(t *testing.T) {
	f := newD510Fixture(t)
	ctx := context.Background()
	id := f.invoice(t, "APPLIED", "PARTIALLY_PAID", -20, 100000, 30000, "INVOICE")

	// A payment for 40000, deliberately NOT 70000. total − balance would say 70000; the truth about
	// PAYMENTS RECEIVED is 40000, and the difference here stands in for a credit memo or an NSF
	// reversal having moved the balance without a payment behind it.
	var payID string
	if err := f.pool.QueryRow(ctx,
		`INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id)
		 VALUES (nextval('payment_no_seq')::text, $1::uuid, 'CHEQUE', 40000, 'RECEIVED', $2::uuid)
		 RETURNING id::text`, f.customerID, trentonBranch).Scan(&payID); err != nil {
		t.Fatalf("seed payment: %v", err)
	}
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents) VALUES ($1::uuid,$2::uuid,40000)`,
		payID, id); err != nil {
		t.Fatalf("seed allocation: %v", err)
	}

	d, err := f.service(d510Manager()).Detail(ctx, id, true)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if d.Totals.AppliedCents != 40000 {
		t.Fatalf("applied is SUM(payment_allocations) = 40000, got %d", d.Totals.AppliedCents)
	}
	if d.Totals.BalanceCents != 30000 {
		t.Fatalf("the balance is the stored column, got %d", d.Totals.BalanceCents)
	}
	if d.Totals.PayableCents != 30000 {
		t.Fatalf("payable is balance − holdback, got %d", d.Totals.PayableCents)
	}
	if d.Totals.TotalCents != 100000 {
		t.Fatalf("the total is the stored column, got %d", d.Totals.TotalCents)
	}
}

// -- ruling 87: the timeline's payment entries ----------------------------------------------------

func TestDetail_PaymentEntriesReachOnlyACallerWhoAsksForThem(t *testing.T) {
	f := newD510Fixture(t)
	ctx := context.Background()
	id := f.invoice(t, "TRAIL", "PARTIALLY_PAID", -20, 100000, 60000, "INVOICE")
	var payID string
	if err := f.pool.QueryRow(ctx,
		`INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id)
		 VALUES (nextval('payment_no_seq')::text, $1::uuid, 'CHEQUE', 40000, 'RECEIVED', $2::uuid)
		 RETURNING id::text`, f.customerID, trentonBranch).Scan(&payID); err != nil {
		t.Fatalf("seed payment: %v", err)
	}
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents) VALUES ($1::uuid,$2::uuid,40000)`,
		payID, id); err != nil {
		t.Fatalf("seed allocation: %v", err)
	}
	kinds := func(withPayments bool) map[string]int {
		d, err := f.service(d510Manager()).Detail(ctx, id, withPayments)
		if err != nil {
			t.Fatalf("detail: %v", err)
		}
		out := map[string]int{}
		for _, e := range d.Activity {
			out[e.Kind]++
		}
		return out
	}
	if got := kinds(true); got["PAYMENT"] != 1 {
		t.Fatalf("a caller who holds the trail sees the payment entry once, got %v", got)
	}
	without := kinds(false)
	if _, present := without["PAYMENT"]; present {
		t.Fatalf("the payment entry is absent for a caller who does not hold the trail, got %v", without)
	}
	if without["ISSUED"] != 1 {
		t.Fatalf("the rest of the timeline is unchanged, got %v", without)
	}
}

func TestCustomerActivity_PaymentRowsReachOnlyACallerWhoAsksForThem(t *testing.T) {
	f := newD510Fixture(t)
	ctx := context.Background()
	for _, txn := range []string{"INVOICE", "PAYMENT", "NSF"} {
		if _, err := f.pool.Exec(ctx,
			`INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, occurred_at)
			 VALUES ($1::uuid, $2, 100, 100, 'test', gen_random_uuid(), $3)`, f.customerID, txn, d510Now); err != nil {
			t.Fatalf("seed %s row: %v", txn, err)
		}
	}
	repo := NewRepository(f.pool)
	count := func(withPayments bool) map[string]int {
		rows, err := repo.CustomerActivity(ctx, f.customerID, 10, withPayments)
		if err != nil {
			t.Fatalf("activity: %v", err)
		}
		out := map[string]int{}
		for _, r := range rows {
			out[r.Kind]++
		}
		return out
	}
	if got := count(true); got["PAYMENT"] != 1 || got["NSF"] != 1 || got["INVOICE"] != 1 {
		t.Fatalf("a caller who holds the trail sees every row, got %v", got)
	}
	got := count(false)
	if got["PAYMENT"] != 0 || got["NSF"] != 0 || got["INVOICE"] != 1 {
		t.Fatalf("payment and NSF rows are absent for a caller without the trail, got %v", got)
	}
}

// The dealer default is read with an empty branch, and the query must not cast that empty string to a uuid: SQL's
// OR does not short circuit, and once the driver's prepared statement has turned into a generic plan (after five
// uses on a connection with real branch ids, as the register's own reads give it) the cast is evaluated for the
// empty branch and the read fails with an invalid uuid. Found by the assistant's account position claim (slice 7c),
// which reads the default beside the branches on every request. The connection forces the generic plan from its
// first use, so the test does not depend on the pool handing the same connection back.
func TestRegisterPolicy_TheDealerDefaultResolvesAfterTheStatementHasBeenUsedWithRealBranches(t *testing.T) {
	pool := genericPlanPool(t)
	repo := NewRepository(pool)
	branch := aBranchID(t, pool)
	for i := 0; i < 12; i++ {
		if _, err := repo.RegisterPolicyFor(context.Background(), branch); err != nil {
			t.Fatalf("call %d for a branch: %v", i+1, err)
		}
		if _, err := repo.RegisterPolicyFor(context.Background(), ""); err != nil {
			t.Fatalf("call %d for the dealer default: %v", i+1, err)
		}
	}
}
