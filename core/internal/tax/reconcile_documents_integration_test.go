//go:build integration

package tax

// reconcile_documents_integration_test.go, FW1-11. The reconcile's collected side reads
// tax_decision_snapshot rows, which only relief sales write; the GL side reads one month's movement on
// 2200, which also carries the vendor bills' input tax credits. An ordinary month, a counter sale and a
// posted vendor bill, reads a false variance (the audit's LEDGER-TIEOUT finding). The rebuild reads the
// month's documents: sales HST on the invoices (credit memos subtract), input tax credits on the posted
// vendor bills, shown apart, netted, against 2200's month movement.

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
	"github.com/jackc/pgx/v5/pgxpool"
)

const fw11TRT = "5d1b3f6e-0000-4000-9000-000000000001"

// fw11Ensure2091 opens fiscal year 2091 for the GL legs and cleans its journals after.
func fw11Ensure2091(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO fiscal_periods (year, month) SELECT 2091, m FROM generate_series(1,12) AS m
		 ON CONFLICT (year, month) DO NOTHING`); err != nil {
		t.Fatalf("ensure periods 2091: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id=j.id AND j.fiscal_year=2091`)
		_, _ = pool.Exec(bg, `DELETE FROM gl_journals WHERE fiscal_year=2091`)
		_, _ = pool.Exec(bg, `DELETE FROM fiscal_periods WHERE year=2091`)
	})
}

// fw11Line is one planted leg: account code and its side.
type fw11Line struct {
	code          string
	debit, credit int64
}

// fw11Post plants one POSTED 2091-05 journal with the given legs, the way the real writers post them
// (the deferred balance trigger asserts at COMMIT).
func fw11Post(t *testing.T, pool *pgxpool.Pool, memo string, lines []fw11Line) {
	t.Helper()
	ctx := context.Background()
	var periodID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM fiscal_periods WHERE year=2091 AND month=5`).Scan(&periodID); err != nil {
		t.Fatalf("period 2091-05: %v", err)
	}
	var jid string
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO gl_journals (fiscal_year, entry_no, period_id, source, memo, status, posted_at, branch_id)
		VALUES (2091, (SELECT COALESCE(MAX(entry_no),0)+1 FROM gl_journals WHERE fiscal_year=2091),
		        $1::uuid, 'ADJUSTMENT', $2, 'POSTED', $3, $4::uuid)
		RETURNING id::text`,
		periodID, memo, time.Date(2091, 5, 20, 12, 0, 0, 0, time.UTC), fw11TRT).Scan(&jid); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("plant journal: %v", err)
	}
	for _, l := range lines {
		if _, err := tx.Exec(ctx, `
			INSERT INTO gl_journal_lines (journal_id, line_no, account_id, debit_cents, credit_cents, memo)
			SELECT $1::uuid, (SELECT COALESCE(MAX(line_no),0)+1 FROM gl_journal_lines WHERE journal_id=$1::uuid),
			       a.id, NULLIF($2,0), NULLIF($3,0), $4 FROM gl_accounts a WHERE a.code = $5`,
			jid, l.debit, l.credit, memo, l.code); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("plant line %s: %v", l.code, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit journal: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id=$1`, jid)
		_, _ = pool.Exec(bg, `DELETE FROM gl_journals WHERE id=$1`, jid)
	})
}

// fw11GLBal reads the GL side through the real gl service, the provider seam the wiring uses.
type fw11GLBal struct{ svc *gl.Service }

func (a fw11GLBal) GLBalanceCents(ctx context.Context, code string, year, month int, branchID string) (int64, error) {
	return a.svc.GLBalanceCents(ctx, code, year, month, branchID)
}

// The red leg: an ordinary month with a counter sale's invoice and one posted vendor bill shows a false
// variance today, because the collected side reads relief snapshots (none here) and the GL side nets the
// bill's input tax credit against the sale's collected tax.
func TestIntegration_FW1_11_ReconcileReadsTheMonthsDocuments(t *testing.T) {
	pool := intPool(t)
	fw11Ensure2091(t, pool)
	// The reconcile reads its document sides through the owning modules' seams (billing's invoices,
	// purchasing's posted bills), wired here over the real repositories the way main.go wires them.
	svc := intSvc(t, pool).
		WithSalesHSTs(billing.NewRepository(pool)).
		WithBillITCs(purchasing.NewRepository(pool)).
		WithGLBalances(fw11GLBal{svc: gl.NewService(gl.NewRepository(pool))})
	ctx := context.Background()

	var customerID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers LIMIT 1`).Scan(&customerID); err != nil {
		t.Skipf("no seeded customer: %v", err)
	}
	// The sale: a 100000 + 13000 tax invoice finalized in the month, its journal posted (CR 2200).
	var inv string
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ('FW11-SALE', $1::uuid, $2::uuid, 'FINALIZED', '2091-05-10', '2091-05-10',
		        100000, 13000, 113000, 113000, '2091-05-10', 'INVOICE')
		RETURNING id::text`,
		customerID, fw11TRT).Scan(&inv); err != nil {
		t.Fatalf("plant invoice: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM invoices WHERE id=$1`, inv)
	})
	// The sale's journal the way ar.invoice.finalized posts it: DR 1100, CR 4000 revenue, CR 2200 tax.
	fw11Post(t, pool, "FW11 sale collected", []fw11Line{
		{"1100", 113000, 0},
		{"4000", 0, 100000},
		{"2200", 0, 13000},
	})

	// The vendor bill: 30000 + 3900 input tax, posted in the month (DR 2200).
	var vendorID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM vendors LIMIT 1`).Scan(&vendorID); err != nil {
		t.Skipf("no seeded vendor: %v", err)
	}
	var bill string
	if err := pool.QueryRow(ctx, `
		INSERT INTO purchasing_bills (bill_no, vendor_id, branch_id, status, match_state, bill_date,
		                              subtotal_cents, tax_cents, total_cents, posted_at)
		VALUES ('FW11-BILL', $1::uuid, $2::uuid, 'POSTED', 'MATCHED', '2091-05-20', 30000, 3900, 33900, $3)
		RETURNING id::text`,
		vendorID, fw11TRT, time.Date(2091, 5, 21, 12, 0, 0, 0, time.UTC)).Scan(&bill); err != nil {
		t.Fatalf("plant bill: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM purchasing_bills WHERE id=$1`, bill)
	})
	// The vendor bill's journal the way PostBill posts it: DR the cost, DR 2200 input tax, CR 2100 payable.
	fw11Post(t, pool, "FW11 bill input tax", []fw11Line{
		{"5250", 30000, 0},
		{"2200", 3900, 0},
		{"2100", 0, 33900},
	})

	rep, err := svc.ReconcileReport(ctx, 2091, 5, "")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// The collected side is the month's documents, not the relief snapshots: 13000 on the sale, the
	// bill's 3900 shown apart as input tax credits.
	if rep.CollectedCents != 13000 {
		t.Errorf("FW1-11: the reconcile reads collected %d for an ordinary month's documents, want 13000 (it used to read relief snapshots, which never wrote this month)", rep.CollectedCents)
	}
	if rep.InputTaxCreditsCents != 3900 {
		t.Errorf("FW1-11: the reconcile reads input tax credits %d, want 3900 on the posted bill", rep.InputTaxCreditsCents)
	}
	// The month ties: 2200's movement (13000 credit less 3900 debit = 9100) against the documents net
	// (13000 less 3900 of input tax).
	if rep.VarianceCents != 0 || !rep.TiedOut {
		t.Errorf("FW1-11: an ordinary month shows a variance of %d (GL %d against %d collected less %d of input tax), want a tie", rep.VarianceCents, rep.GLPayableCents, rep.CollectedCents, rep.InputTaxCreditsCents)
	}
}

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
	gotGL := rep.GLPayableCents - before.GLPayableCents
	gotVariance := rep.VarianceCents - before.VarianceCents
	if gotCollected != sale.TaxCents || gotGL != sale.TaxCents-ret.TaxCents || gotVariance != 0 {
		t.Errorf("FW1-11 P1-2 red: after POS sale (tax %d) and POS return (tax %d), collected delta = %d, GL 2200 delta = %d, variance delta = %d, want variance delta 0 with the return's tax netted",
			sale.TaxCents, ret.TaxCents, gotCollected, gotGL, gotVariance)
	}
}
