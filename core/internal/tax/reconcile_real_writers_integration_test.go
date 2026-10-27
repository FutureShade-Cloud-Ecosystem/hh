//go:build integration

package tax

// reconcile_real_writers_integration_test.go, FW1-11 Round 2. Exercises the tax tie-out through the
// real production writers (billing.InvoiceWriter.IssueCreditMemo and pos.Service.Sale / Return) and
// their GL outbox consumers against the current open period.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	gl "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// fw11CreditMemoPoster adapts billing.CreditMemoPoster to gl.Service.PostExternal the way
// main.go's w6cCreditMemoPoster does: resolve the invoice.finalized roles for the branch and post
// in.ReversingLegs().
type fw11CreditMemoPoster struct {
	svc   *gl.Service
	rules *gl.Repository
}

func (a fw11CreditMemoPoster) PostCreditMemoJournal(ctx context.Context, in billing.CreditMemoJournal) (string, error) {
	rules, err := a.rules.PostingAccounts(ctx, "invoice.finalized", in.BranchID)
	if err != nil {
		return "", err
	}
	var lines []gl.ExternalLine
	for _, leg := range in.ReversingLegs() {
		lines = append(lines, gl.ExternalLine{
			AccountCode: rules[leg.Role],
			DebitCents:  leg.DebitCents,
			CreditCents: leg.CreditCents,
			Memo:        leg.Memo,
		})
	}
	return a.svc.PostExternal(ctx, gl.ExternalJournal{
		Source:         gl.SourceInvoice,
		SourceRefID:    in.CreditMemoID,
		IdempotencyKey: in.IdempotencyKey,
		Memo:           in.Memo,
		BranchID:       in.BranchID,
		Lines:          lines,
	})
}

// fw11InvoiceBridge adapts billing.Repository.InvoiceForGL to gl.InvoiceReader so gl's outbox
// consumer can post ar.invoice.finalized events emitted by pos.Service.Sale.
type fw11InvoiceBridge struct{ repo *billing.Repository }

func (b fw11InvoiceBridge) InvoiceForGL(ctx context.Context, invoiceID string) (gl.InvoiceAmounts, error) {
	a, err := b.repo.InvoiceForGL(ctx, invoiceID)
	if err != nil {
		return gl.InvoiceAmounts{}, err
	}
	return gl.InvoiceAmounts{
		SubtotalCents: a.SubtotalCents,
		TaxCents:      a.TaxCents,
		TotalCents:    a.TotalCents,
		HoldbackCents: a.HoldbackCents,
		BranchID:      a.BranchID,
		CustomerID:    a.CustomerID,
		InvoiceNo:     a.InvoiceNo,
		FinalizedAt:   a.FinalizedAt,
		SourceKind:    a.SourceKind,
	}, nil
}

// fw11SingleOutbox feeds one outbox event into notify.Dispatcher.DrainOnce so the real gl posting
// consumer settles it.
type fw11SingleOutbox struct{ ev notify.Event }

func (o fw11SingleOutbox) ProcessBatch(ctx context.Context, _ int, settle func(context.Context, notify.Event) notify.Settlement) (int, error) {
	s := settle(ctx, o.ev)
	if s.LastError != "" {
		return 0, fmt.Errorf("outbox settle %s: %s", o.ev.Topic, s.LastError)
	}
	return 1, nil
}

// Round 2 (P1-1, P2-2): a credit memo issued through billing.InvoiceWriter.IssueCreditMemo stores
// negative tax_cents on invoices and posts DR 2200 through CreditMemoPoster; DocumentSalesHSTCents
// must sum tax_cents as stored so the memo's HST is subtracted once, never negated twice.
func TestIntegration_FW1_11_CreditMemoThroughRealWriterSubtractsOnce(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	now := time.Now()
	year, month := now.Year(), int(now.Month())
	if _, err := pool.Exec(ctx,
		`INSERT INTO fiscal_periods (year, month) VALUES ($1, $2) ON CONFLICT (year, month) DO NOTHING`,
		year, month); err != nil {
		t.Fatalf("ensure period %d-%02d: %v", year, month, err)
	}

	glRepo := gl.NewRepository(pool)
	glSvc := gl.NewService(glRepo)
	svc := intSvc(t, pool).
		WithSalesHSTs(billing.NewRepository(pool)).
		WithReturnHSTs(pos.NewRepository(pool)).
		WithBillITCs(purchasing.NewRepository(pool)).
		WithGLBalances(fw11GLBal{svc: glSvc})

	before, err := svc.ReconcileReport(ctx, year, month, fw11TRT)
	if err != nil {
		t.Fatalf("baseline reconcile: %v", err)
	}

	var customerID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers LIMIT 1`).Scan(&customerID); err != nil {
		t.Skipf("no seeded customer: %v", err)
	}
	var invID string
	invNo := fmt.Sprintf("FW11-CM-ORIG-%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE, CURRENT_DATE,
		        100000, 13000, 113000, 113000, now(), 'INVOICE')
		RETURNING id::text`,
		invNo, customerID, fw11TRT).Scan(&invID); err != nil {
		t.Fatalf("plant original invoice: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_lines (invoice_id, line_no, description, qty_entered, uom_entered, qty_base,
		                           unit_price_cents, price_uom, line_total_cents, tax_cents)
		VALUES ($1::uuid, 1, 'FW11 original sale line', 1, 'EA', 1, 100000, 'EA', 100000, 13000)`,
		invID); err != nil {
		t.Fatalf("plant original invoice line: %v", err)
	}
	invJID, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		Source:         gl.SourceInvoice,
		SourceRefID:    invID,
		IdempotencyKey: "fw11-orig-inv:" + invID,
		Memo:           "Invoice " + invNo,
		BranchID:       fw11TRT,
		Lines: []gl.ExternalLine{
			{AccountCode: "1100", DebitCents: 113000, Memo: "AR"},
			{AccountCode: "4000", CreditCents: 100000, Memo: "Revenue"},
			{AccountCode: "2200", CreditCents: 13000, Memo: "HST"},
		},
	})
	if err != nil {
		t.Fatalf("post original invoice journal: %v", err)
	}

	writer := billing.NewInvoiceWriter(pool).
		WithCreditMemoPoster(fw11CreditMemoPoster{svc: glSvc, rules: glRepo})
	memo, err := writer.IssueCreditMemo(ctx, billing.CreditMemoInput{
		CustomerID:        customerID,
		BranchID:          fw11TRT,
		ReversesInvoiceID: invID,
		AmountCents:       20000,
		Memo:              "FW11 partial credit memo",
		IdempotencyKey:    fmt.Sprintf("fw11-cm-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("IssueCreditMemo: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		for _, jid := range []string{invJID, memo.GLJournalID} {
			if jid != "" {
				_, _ = pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id=$1::uuid`, jid)
				_, _ = pool.Exec(bg, `DELETE FROM gl_journals WHERE id=$1::uuid`, jid)
			}
		}
		_, _ = pool.Exec(bg, `DELETE FROM customer_transactions WHERE reference_id=$1::uuid`, memo.InvoiceID)
		_, _ = pool.Exec(bg, `DELETE FROM invoice_lines WHERE invoice_id IN ($1::uuid, $2::uuid)`, invID, memo.InvoiceID)
		_, _ = pool.Exec(bg, `DELETE FROM invoices WHERE id=$1::uuid`, memo.InvoiceID)
		_, _ = pool.Exec(bg, `DELETE FROM invoices WHERE id=$1::uuid`, invID)
	})
	if memo.GLJournalID == "" || memo.GLJournalError != "" {
		t.Fatalf("IssueCreditMemo did not post its reversing journal: id=%q err=%q", memo.GLJournalID, memo.GLJournalError)
	}
	if memo.TaxCents != -2600 {
		t.Fatalf("IssueCreditMemo tax_cents = %d, want -2600", memo.TaxCents)
	}

	rep, err := svc.ReconcileReport(ctx, year, month, fw11TRT)
	if err != nil {
		t.Fatalf("reconcile after credit memo: %v", err)
	}
	gotCollected := rep.CollectedCents - before.CollectedCents
	gotGL := rep.GLPayableCents - before.GLPayableCents
	gotVariance := rep.VarianceCents - before.VarianceCents
	// The 13000 invoice less the 2600 credit memo leaves 10400 on both sides and zero variance.
	if gotCollected != 10400 || gotGL != 10400 || gotVariance != 0 {
		t.Errorf("FW1-11 P1-1 red: after IssueCreditMemo (-2600 tax on a 13000 invoice), collected delta = %d, GL 2200 delta = %d, variance delta = %d, want 10400, 10400, 0 (credit memo HST was negated twice)",
			gotCollected, gotGL, gotVariance)
	}
}

// Round 2 (P1-2, P2-2): a POS return committed through pos.Service.Return writes pos_returns and
// emits pos.return.committed (which debits 2200 in GL) without writing an invoices row; the
// reconcile must read the POS return's tax through pos's seam as the returns line and net it.
func TestIntegration_FW1_11_POSReturnThroughRealWriterLandsOnReturnsAndTies(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	now := time.Now()
	year, month := now.Year(), int(now.Month())
	if _, err := pool.Exec(ctx,
		`INSERT INTO fiscal_periods (year, month) VALUES ($1, $2) ON CONFLICT (year, month) DO NOTHING`,
		year, month); err != nil {
		t.Fatalf("ensure period %d-%02d: %v", year, month, err)
	}

	var userID, productID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT p.id::text FROM products p
		  JOIN pricing_list_prices lp ON lp.product_id = p.id AND lp.price_uom = 'SF'
		 WHERE p.is_active AND p.base_uom = 'SF' LIMIT 1`).Scan(&productID); err != nil {
		t.Skipf("no seeded SF product: %v", err)
	}

	suffix := fmt.Sprintf("fw11pos-%d", time.Now().UnixNano()%1_000_000_000)
	var custID, saleKey, retKey string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		VALUES ('FW11-'||$1, 'FW11 POS return customer', 'CONTRACTOR', 30, 5000000)
		RETURNING id::text, gen_random_uuid()::text, gen_random_uuid()::text`, suffix).Scan(&custID, &saleKey, &retKey); err != nil {
		t.Fatalf("plant customer: %v", err)
	}
	var saleOrderID, saleInvoiceID, retID string
	t.Cleanup(func() {
		bg := context.Background()
		if saleInvoiceID != "" || retID != "" {
			_, _ = pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE source_ref_id IN (NULLIF($1,'')::uuid, NULLIF($2,'')::uuid))`, saleInvoiceID, retID)
			_, _ = pool.Exec(bg, `DELETE FROM gl_journals WHERE source_ref_id IN (NULLIF($1,'')::uuid, NULLIF($2,'')::uuid)`, saleInvoiceID, retID)
		}
		if saleOrderID != "" || saleInvoiceID != "" || retID != "" {
			_, _ = pool.Exec(bg, `DELETE FROM events_outbox WHERE payload->>'order_id' = $1 OR payload->>'invoice_id' = $2 OR payload->>'return_id' = $3`, saleOrderID, saleInvoiceID, retID)
		}
		if retID != "" {
			_, _ = pool.Exec(bg, `DELETE FROM pos_return_lines WHERE return_id = $1::uuid`, retID)
			_, _ = pool.Exec(bg, `DELETE FROM pos_returns WHERE id = $1::uuid`, retID)
		}
		if saleOrderID != "" {
			_, _ = pool.Exec(bg, `DELETE FROM pos_sale_tenders WHERE pos_sale_id IN (SELECT id FROM pos_sale WHERE order_id = $1::uuid)`, saleOrderID)
			_, _ = pool.Exec(bg, `DELETE FROM pos_sale WHERE order_id = $1::uuid`, saleOrderID)
			_, _ = pool.Exec(bg, `DELETE FROM order_lines WHERE order_id = $1::uuid`, saleOrderID)
			_, _ = pool.Exec(bg, `DELETE FROM orders WHERE id = $1::uuid`, saleOrderID)
		}
		_, _ = pool.Exec(bg, `DELETE FROM return_notes WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(bg, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, custID)
		if saleInvoiceID != "" {
			_, _ = pool.Exec(bg, `DELETE FROM invoice_lines WHERE invoice_id = $1::uuid`, saleInvoiceID)
			_, _ = pool.Exec(bg, `DELETE FROM invoices WHERE id = $1::uuid`, saleInvoiceID)
		}
		_, _ = pool.Exec(bg, `DELETE FROM customers WHERE id = $1::uuid`, custID)
	})

	glRepo := gl.NewRepository(pool)
	glSvc := gl.NewService(glRepo).WithInvoiceReader(fw11InvoiceBridge{repo: billing.NewRepository(pool)})
	svc := intSvc(t, pool).
		WithSalesHSTs(billing.NewRepository(pool)).
		WithReturnHSTs(pos.NewRepository(pool)).
		WithBillITCs(purchasing.NewRepository(pool)).
		WithGLBalances(fw11GLBal{svc: glSvc})

	before, err := svc.ReconcileReport(ctx, year, month, fw11TRT)
	if err != nil {
		t.Fatalf("baseline reconcile: %v", err)
	}

	var doc roleconfig.Document
	if err := json.Unmarshal(roleconfig.DefaultData(), &doc); err != nil {
		t.Fatalf("roleconfig default data: %v", err)
	}
	doc.Settings[roleconfig.SettingRefundApprovalLimitEnabled] = json.RawMessage(`false`)
	rcfg, err := roleconfig.Validate(&doc)
	if err != nil {
		t.Fatalf("roleconfig validate: %v", err)
	}

	cat := product.NewService(product.NewRepository(pool))
	priceSvc := pricing.NewService(pricing.NewRepository(pool)).
		WithCategoryResolver(cat).
		WithUOMConverter(cat).
		WithMarginGate(pricing.NewIdentityMarginGate())
	tillSvc := till.NewService(till.NewRepository(pool))
	posSvc := pos.NewService(pos.NewRepository(pool)).
		WithLineResolver(priceSvc).
		WithTenderTaker(tillSvc).
		WithCreditGate(pos.NewIdentityCreditGate()).
		WithDepositDocuments(billing.NewInvoiceWriter(pool)).
		WithRoleConfig(rcfg).
		WithOpenQuantityReleaser(order.NewRepository(pool)).
		WithClock(time.Now)

	staffCtx := identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID: userID, RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: "ACTIVE",
	})

	sale, err := posSvc.Sale(staffCtx, pos.SaleInput{
		IdempotencyKey: saleKey,
		BranchID:       fw11TRT,
		CustomerID:     custID,
		SaleType:       pos.SaleCollectLater,
		Tender:         pos.TenderAccount,
		Lines:          []pos.LineInput{{ProductID: productID, QtyEntered: 4, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("pos.Sale: %v", err)
	}
	saleOrderID, saleInvoiceID = sale.OrderID, sale.InvoiceID
	var orderLineID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM order_lines WHERE order_id = $1::uuid ORDER BY line_no LIMIT 1`, sale.OrderID).Scan(&orderLineID); err != nil {
		t.Fatalf("order line: %v", err)
	}

	drainTopic := func(topic, keyField, keyVal string) {
		t.Helper()
		var payload []byte
		if err := pool.QueryRow(ctx,
			`SELECT payload FROM events_outbox WHERE topic = $1 AND payload->>$2 = $3 ORDER BY created_at DESC LIMIT 1`,
			topic, keyField, keyVal).Scan(&payload); err != nil {
			t.Fatalf("read %s outbox payload for %s=%s: %v", topic, keyField, keyVal, err)
		}
		disp := notify.NewDispatcher(fw11SingleOutbox{ev: notify.Event{EventID: "1", Topic: topic, Payload: payload}}, slog.New(slog.DiscardHandler))
		glSvc.RegisterPostingHandlers(disp)
		if n := disp.DrainOnce(ctx); n != 1 {
			t.Fatalf("drain %s for %s=%s: settled %d rows, want 1", topic, keyField, keyVal, n)
		}
	}

	drainTopic("ar.invoice.finalized", "invoice_id", sale.InvoiceID)

	ret, err := posSvc.Return(staffCtx, pos.ReturnInput{
		IdempotencyKey: retKey,
		OrderID:        sale.OrderID,
		RefundMethod:   pos.RefundAccountCredit,
		Restock:        true,
		Lines:          []pos.ReturnLineInput{{OrderLineID: orderLineID, QtyBase: 2, ReasonCode: "OTHER"}},
	})
	if err != nil {
		t.Fatalf("pos.Return: %v", err)
	}
	retID = ret.ReturnID
	if ret.TaxCents <= 0 {
		t.Fatalf("pos.Return tax_cents = %d, want > 0", ret.TaxCents)
	}

	drainTopic("pos.return.committed", "return_id", ret.ReturnID)

	rep, err := svc.ReconcileReport(ctx, year, month, fw11TRT)
	if err != nil {
		t.Fatalf("reconcile after POS return: %v", err)
	}
	gotCollected := rep.CollectedCents - before.CollectedCents
	gotReturns := rep.ReturnTaxCents - before.ReturnTaxCents
	gotGL := rep.GLPayableCents - before.GLPayableCents
	gotVariance := rep.VarianceCents - before.VarianceCents
	if gotCollected != sale.TaxCents || gotReturns != ret.TaxCents || gotGL != sale.TaxCents-ret.TaxCents || gotVariance != 0 {
		t.Errorf("FW1-11 P1-2: after POS sale (tax %d) and POS return (tax %d), collected delta = %d, returns delta = %d, GL 2200 delta = %d, variance delta = %d, want returns delta %d and variance delta 0",
			sale.TaxCents, ret.TaxCents, gotCollected, gotReturns, gotGL, gotVariance, ret.TaxCents)
	}
}

// Round 2b, PART 3 (FROM-PRINCIPAL 177): a document carrying the opening balance marker is not this
// period's business. The reconcile must leave it out of collected and name it on the report (count
// and HST), so wave 2's opening balances are visible without disturbing the tie. The marker is
// explicit on the invoice, never inferred from a missing journal.
func TestIntegration_FW1_11_MarkedOpeningInvoiceIsExcludedAndListed(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	now := time.Now()
	year, month := now.Year(), int(now.Month())
	if _, err := pool.Exec(ctx,
		`INSERT INTO fiscal_periods (year, month) VALUES ($1, $2) ON CONFLICT (year, month) DO NOTHING`,
		year, month); err != nil {
		t.Fatalf("ensure period %d-%02d: %v", year, month, err)
	}

	svc := intSvc(t, pool).
		WithSalesHSTs(billing.NewRepository(pool)).
		WithReturnHSTs(pos.NewRepository(pool)).
		WithBillITCs(purchasing.NewRepository(pool)).
		WithGLBalances(fw11GLBal{svc: gl.NewService(gl.NewRepository(pool))})

	before, err := svc.ReconcileReport(ctx, year, month, fw11TRT)
	if err != nil {
		t.Fatalf("baseline reconcile: %v", err)
	}

	var customerID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers LIMIT 1`).Scan(&customerID); err != nil {
		t.Skipf("no seeded customer: %v", err)
	}
	var invID string
	invNo := fmt.Sprintf("FW11-OPEN-%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type, metadata)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE, CURRENT_DATE,
		        30000, 5000, 35000, 35000, now(), 'INVOICE', '{"opening_balance": true}'::jsonb)
		RETURNING id::text`,
		invNo, customerID, fw11TRT).Scan(&invID); err != nil {
		t.Fatalf("plant the marked opening invoice: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM invoice_lines WHERE invoice_id = $1::uuid`, invID)
		_, _ = pool.Exec(bg, `DELETE FROM invoices WHERE id = $1::uuid`, invID)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_lines (invoice_id, line_no, description, qty_entered, uom_entered, qty_base,
		                           unit_price_cents, price_uom, line_total_cents, tax_cents)
		VALUES ($1::uuid, 1, 'FW11 opening balance line', 1, 'EA', 1, 30000, 'EA', 30000, 5000)`, invID); err != nil {
		t.Fatalf("plant the marked opening invoice line: %v", err)
	}

	rep, err := svc.ReconcileReport(ctx, year, month, fw11TRT)
	if err != nil {
		t.Fatalf("reconcile with the marked opening invoice: %v", err)
	}
	gotCollected := rep.CollectedCents - before.CollectedCents
	gotGL := rep.GLPayableCents - before.GLPayableCents
	gotVariance := rep.VarianceCents - before.VarianceCents
	gotCount := rep.OpeningBalanceCount - before.OpeningBalanceCount
	gotOpening := rep.OpeningBalanceHSTCents - before.OpeningBalanceHSTCents
	if gotCount != 1 || gotOpening != 5000 || gotCollected != 0 || gotGL != 0 || gotVariance != 0 {
		t.Errorf("FW1-11 PART 3 red: a marked opening invoice (tax 5000) must sit outside collected and on the opening balances line, got collected delta = %d, GL delta = %d, variance delta = %d, opening count delta = %d, opening HST delta = %d, want 0, 0, 0, 1, 5000",
			gotCollected, gotGL, gotVariance, gotCount, gotOpening)
	}
}

// Round 2b, PART 3 (FROM-PRINCIPAL 177): the exclusion is the MARKER, never a missing journal. An
// invoice of this period with no journal of its own (FW1-02's defect class, an unposted document)
// must still show as a variance, and must not be listed as an opening balance.
func TestIntegration_FW1_11_UnpostedInvoiceWithoutTheMarkerStillShowsAsVariance(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	now := time.Now()
	year, month := now.Year(), int(now.Month())
	if _, err := pool.Exec(ctx,
		`INSERT INTO fiscal_periods (year, month) VALUES ($1, $2) ON CONFLICT (year, month) DO NOTHING`,
		year, month); err != nil {
		t.Fatalf("ensure period %d-%02d: %v", year, month, err)
	}

	svc := intSvc(t, pool).
		WithSalesHSTs(billing.NewRepository(pool)).
		WithReturnHSTs(pos.NewRepository(pool)).
		WithBillITCs(purchasing.NewRepository(pool)).
		WithGLBalances(fw11GLBal{svc: gl.NewService(gl.NewRepository(pool))})

	before, err := svc.ReconcileReport(ctx, year, month, fw11TRT)
	if err != nil {
		t.Fatalf("baseline reconcile: %v", err)
	}

	var customerID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers LIMIT 1`).Scan(&customerID); err != nil {
		t.Skipf("no seeded customer: %v", err)
	}
	var invID string
	invNo := fmt.Sprintf("FW11-UNPOSTED-%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type, metadata)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE, CURRENT_DATE,
		        40000, 7000, 47000, 47000, now(), 'INVOICE', '{}'::jsonb)
		RETURNING id::text`,
		invNo, customerID, fw11TRT).Scan(&invID); err != nil {
		t.Fatalf("plant the unposted invoice: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM invoice_lines WHERE invoice_id = $1::uuid`, invID)
		_, _ = pool.Exec(bg, `DELETE FROM invoices WHERE id = $1::uuid`, invID)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_lines (invoice_id, line_no, description, qty_entered, uom_entered, qty_base,
		                           unit_price_cents, price_uom, line_total_cents, tax_cents)
		VALUES ($1::uuid, 1, 'FW11 unposted line', 1, 'EA', 1, 40000, 'EA', 40000, 7000)`, invID); err != nil {
		t.Fatalf("plant the unposted invoice line: %v", err)
	}

	rep, err := svc.ReconcileReport(ctx, year, month, fw11TRT)
	if err != nil {
		t.Fatalf("reconcile with the unposted invoice: %v", err)
	}
	gotVariance := rep.VarianceCents - before.VarianceCents
	gotCount := rep.OpeningBalanceCount - before.OpeningBalanceCount
	if gotVariance != -7000 || gotCount != 0 {
		t.Errorf("FW1-11 PART 3: an unposted invoice with no marker is this period's missing journal, got variance delta = %d and opening count delta = %d, want -7000 and 0",
			gotVariance, gotCount)
	}
}
