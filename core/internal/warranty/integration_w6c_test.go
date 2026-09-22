//go:build integration

// integration_w6c_test.go, DIB-422 item 2: W5-B'S STUBBED OUTCOME, FLIPPED TO THE REAL PATH.
//
// -- WHAT WAS STUBBED, AND WHY IT COULD ONLY BE STUBBED ---------------------------------------
// W5-B built the CUSTOMER_ACCOMMODATION money path complete: the seam, the request shape, the
// idempotency key, the posting row, and TestMoneyPath_CustomerAccommodation_IssuesCreditMemoThrough
// BillingSeam proving the contract against a fakeCredit. What it could not do was ISSUE A CREDIT
// MEMO, because nothing in the tree could, billing had no writer outside the POS ring. So it shipped
// the honest partial: FALLBACK_GL, warranty posting the expense journal itself and saying, in its own
// status vocabulary, that the books were right and the document the customer expects did not exist.
//
// This file is that document existing. The unit test with the fake STAYS, it pins the contract, and
// it runs without a database. This adds the other half: the same settlement, through the real
// billing writer, against real Postgres, asserting the things only a real path can be wrong about.
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:5432/swarm6_c?sslmode=disable' \
//	  go test -tags=integration ./internal/warranty/... -run W6C -count=1
package warranty

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// realCreditIssuer is main.go's w6cWarrantyCreditIssuer, rebuilt here, the realGLPoster precedent.
type realCreditIssuer struct{ w *billing.InvoiceWriter }

func (a realCreditIssuer) IssueCustomerCredit(ctx context.Context, in CustomerCreditRequest) (CustomerCreditResult, error) {
	doc, err := a.w.IssueCreditMemo(ctx, billing.CreditMemoInput{
		CustomerID: in.CustomerID, BranchID: in.BranchID, AmountCents: in.AmountCents,
		Memo: in.Memo, IdempotencyKey: in.IdempotencyKey, SourceKind: billing.SourceWarrantyCredit,
	})
	if err != nil {
		return CustomerCreditResult{}, err
	}
	return CustomerCreditResult{
		CreditMemoID: doc.InvoiceID, CreditMemoNo: doc.InvoiceNo, GLJournalID: doc.GLJournalID,
	}, nil
}

// realCreditMemoPoster is main.go's w6cCreditMemoPoster: the REVERSING ENTRY, sides swapped.
type realCreditMemoPoster struct {
	svc   *gl.Service
	rules *gl.Repository
}

func (a realCreditMemoPoster) PostCreditMemoJournal(ctx context.Context, in billing.CreditMemoJournal) (string, error) {
	rules, err := a.rules.PostingAccounts(ctx, "invoice.finalized", in.BranchID)
	if err != nil {
		return "", err
	}
	var lines []gl.ExternalLine
	for _, leg := range in.ReversingLegs() {
		code := rules[leg.Role]
		if code == "" {
			return "", fmt.Errorf("w6c: no %q posting rule", leg.Role)
		}
		lines = append(lines, gl.ExternalLine{
			AccountCode: code, DebitCents: leg.DebitCents, CreditCents: leg.CreditCents, Memo: leg.Memo,
		})
	}
	return a.svc.PostExternal(ctx, gl.ExternalJournal{
		Source: gl.SourceInvoice, SourceRefID: in.CreditMemoID, IdempotencyKey: in.IdempotencyKey,
		Memo: in.Memo, BranchID: in.BranchID, Lines: lines,
	})
}

// w6cCustomer seeds a customer for the credit to land on, and restores what it touched.
func w6cCustomer(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, payment_terms_days)
		 VALUES ($1, 'W6C Accommodation', 'CONTRACTOR', $2::uuid, 30) RETURNING id::text`,
		fmt.Sprintf("W6C-%d", time.Now().UnixNano()%1_000_000_000), trentonBranch).Scan(&id); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		// FIXTURES RESTORE WHAT THEY TOUCH, and here that is AR: a stray credit memo is a negative
		// balance on the aging report and in the 1100 tie-out for every later test in the suite.
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, id)
		_, _ = pool.Exec(c, `DELETE FROM invoice_lines WHERE invoice_id IN
			(SELECT id FROM invoices WHERE customer_id = $1::uuid)`, id)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, id)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, id)
	})
	return id
}

// TestIntegrationW6C_AccommodationIssuesARealCreditMemo is the flip: FALLBACK_GL becomes POSTED, and
// the customer gets a document.
func TestIntegrationW6C_AccommodationIssuesARealCreditMemo(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	glSvc := gl.NewService(gl.NewRepository(pool))

	writer := billing.NewInvoiceWriter(pool).
		WithCreditMemoPoster(realCreditMemoPoster{svc: glSvc, rules: gl.NewRepository(pool)})
	svc := w5bSvc(pool, staffCaller(demoOwner), &now).WithCustomerCredit(realCreditIssuer{w: writer})

	cust := w6cCustomer(t, pool)
	id := w5bClaim(t, svc, pool, ctx, ClaimInput{
		BranchID: trentonBranch, CustomerID: cust, Description: "W6C accommodation",
	})

	res, err := svc.RecordSettlement(ctx, id, SettlementInput{
		Outcome: OutcomeCustomerAccommodation, RealizedCents: 1_250_00,
	})
	if err != nil {
		t.Fatalf("settle: %v", err)
	}

	// -- THE FLIP --
	if res.PathStatus != PathPosted {
		t.Fatalf("with billing bound, an accommodation must POST, not fall back; got %q", res.PathStatus)
	}
	if res.MoneyPath != PathCustomerCredit || res.PathSeam != SeamBilling {
		t.Fatalf("path = %s/%s, want CUSTOMER_CREDIT/BILLING", res.MoneyPath, res.PathSeam)
	}
	// external_ref is the DOCUMENT. W5-B's fallback deliberately left it empty because a journal is
	// not a document; a non-empty one here is the whole point of the wave.
	if res.ExternalRef == "" {
		t.Fatal("the credit-memo number must be recorded on the settlement, a journal is not a document")
	}

	// -- THE DOCUMENT IS A REAL, CORRECTLY-SIGNED CREDIT MEMO --
	var docType, srcKind string
	var sub, tax, total, balance int64
	var reverses *string
	if err := pool.QueryRow(ctx, `
		SELECT doc_type, source_kind, subtotal_cents, tax_cents, total_cents, balance_cents,
		       reverses_invoice_id::text
		  FROM invoices WHERE invoice_no = $1`, res.ExternalRef).
		Scan(&docType, &srcKind, &sub, &tax, &total, &balance, &reverses); err != nil {
		t.Fatalf("read the credit memo: %v", err)
	}
	if docType != billing.DocCreditMemo {
		t.Errorf("doc_type = %q, want CREDIT_MEMO", docType)
	}
	if srcKind != billing.SourceWarrantyCredit {
		t.Errorf("source_kind = %q, want WARRANTY_CREDIT, a row must say which producer made it", srcKind)
	}
	// EVERY money column negative. 0146's ck_invoices_doc_sign would have refused the row otherwise,
	// so reaching here already proves it; asserting anyway names the property for a reader.
	if sub >= 0 || total >= 0 || balance >= 0 {
		t.Errorf("a credit memo is a NEGATIVE invoice: sub=%d tax=%d total=%d balance=%d",
			sub, tax, total, balance)
	}
	// An ACCOMMODATION reverses nothing: there is no original invoice, only goodwill.
	if reverses != nil {
		t.Errorf("an accommodation credit reverses no document; got %v", *reverses)
	}
	// The amount warranty named is the credit's SUBTOTAL, and tax is assessed on top, an
	// accommodation credits value the customer paid HST on.
	if sub != -1_250_00 {
		t.Errorf("subtotal = %d, want -125000 (warranty's realized amount, negated)", sub)
	}
	if tax >= 0 {
		t.Error("the customer must be credited the tax they paid on goods they did not get")
	}

	// -- THE REVERSING ENTRY: SIDES SWAPPED, EVERY LEG POSITIVE --
	// gl_journal_lines CHECKs debit_cents > 0 / credit_cents > 0, so a journal exists at all ONLY if
	// the sides were exchanged rather than the amounts negated. This is the assertion that would have
	// caught reusing ar.invoice.finalized.
	if res.GLJournalID == "" {
		t.Fatal("the credit memo must carry its reversing journal")
	}
	rows, err := pool.Query(ctx, `
		SELECT a.code, COALESCE(l.debit_cents,0), COALESCE(l.credit_cents,0)
		  FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id
		 WHERE l.journal_id = $1::uuid ORDER BY a.code`, res.GLJournalID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var drTotal, crTotal int64
	legs := map[string][2]int64{}
	for rows.Next() {
		var code string
		var dr, cr int64
		if err := rows.Scan(&code, &dr, &cr); err != nil {
			t.Fatal(err)
		}
		legs[code] = [2]int64{dr, cr}
		drTotal += dr
		crTotal += cr
	}
	if drTotal != crTotal || drTotal == 0 {
		t.Fatalf("the reversing journal must balance and be non-empty: DR %d CR %d", drTotal, crTotal)
	}
	// Revenue is DEBITED (the invoice CREDITS it) and AR is CREDITED (the invoice DEBITS it). That is
	// what "reversing" means, spelled in sides rather than in signs.
	if rev := legs["4000"]; rev[0] != 1_250_00 || rev[1] != 0 {
		t.Errorf("4000 revenue must be DEBITED %d and not credited; got DR %d CR %d", 1_250_00, rev[0], rev[1])
	}
	if ar := legs["1100"]; ar[1] == 0 || ar[0] != 0 {
		t.Errorf("1100 AR must be CREDITED and not debited; got DR %d CR %d", ar[0], ar[1])
	}
	if hst := legs["2200"]; hst[0] == 0 || hst[1] != 0 {
		t.Errorf("2200 HST must be DEBITED and not credited; got DR %d CR %d", hst[0], hst[1])
	}

	// -- WARRANTY DID NOT ALSO POST ITS OWN JOURNAL --
	// The double-book. Billing owns the credit memo's journal; warranty's fallback expense pair must
	// NOT also exist for this claim.
	var fallbacks int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM warranty_settlement_postings
		  WHERE claim_id = $1::uuid AND status = $2`, id, PathFallbackGL).Scan(&fallbacks); err != nil {
		t.Fatal(err)
	}
	if fallbacks != 0 {
		t.Errorf("warranty posted %d fallback journal(s) alongside billing's credit memo, that is the "+
			"double-book the seam exists to prevent", fallbacks)
	}
}

// TestIntegrationW6C_AccommodationIsIdempotentAcrossTheSeam, warranty's own key
// ("warranty-credit:<claimID>") is passed through unchanged and made BINDING by billing's UNIQUE
// index. A replayed settlement must not mint a second credit.
func TestIntegrationW6C_AccommodationIsIdempotentAcrossTheSeam(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	writer := billing.NewInvoiceWriter(pool)
	cust := w6cCustomer(t, pool)

	issuer := realCreditIssuer{w: writer}
	key := fmt.Sprintf("warranty-credit:w6c-%d", time.Now().UnixNano())
	req := CustomerCreditRequest{
		CustomerID: cust, BranchID: trentonBranch, AmountCents: 400_00,
		Memo: "W6C replay", IdempotencyKey: key,
	}

	first, err := issuer.IssueCustomerCredit(ctx, req)
	if err != nil {
		t.Fatalf("first credit: %v", err)
	}
	second, err := issuer.IssueCustomerCredit(ctx, req)
	if err != nil {
		t.Fatalf("replayed credit: %v", err)
	}
	if second.CreditMemoID != first.CreditMemoID {
		t.Fatalf("a replay must return the SAME document: %s then %s", first.CreditMemoID, second.CreditMemoID)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM invoices WHERE idempotency_key = $1`, key).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("a replayed settlement minted %d credit memos; money leaving the company must not "+
			"duplicate on a retry", n)
	}
}

// compile-time assertion that the rebuilt adapters are the shapes the composition root binds.
var (
	_ CustomerCreditIssuer     = realCreditIssuer{}
	_ billing.CreditMemoPoster = realCreditMemoPoster{}
	_ identity.StaffContext    = identity.StaffContext{}
)
