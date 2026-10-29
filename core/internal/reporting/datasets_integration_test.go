//go:build integration

package reporting

// datasets_integration_test.go, the ties and the fence for the report writer's curated data sets.
//
// THE RULE (W3-RW1): every set reads through its owning module's own seam, and every set's total ties
// to that owner's own answer. The sources wired here are the REAL owner services over the integration
// pool, through the same conversions the composition root binds, so a projection that drops or doubles
// a row fails here and not on a manager's screen.
//
// The fixtures are islands: a private stretch of 1971 for the dated sets (the aging reads as of a day
// whose books hold only the fixture's rows), a private product for stock, and private account codes
// are NOT needed for GL because the tie is against the trial balance by account over a fiscal period
// the world has no other journals in. Cleanup is registered per insert and runs LIFO.

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The dataset fixture window: a private stretch of March 1971. The world seeds nothing this early, so
// an as-of read inside the window answers the fixture's rows alone.
var (
	dsDay1 = "1971-03-01"
	dsDay9 = "1971-03-09"
	dsAsOf = "1971-03-05"
)

// dsInstant parses a fixture day for calls that take a time.Time.
func dsInstant(day string) time.Time {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		panic(err)
	}
	return t.Add(12 * time.Hour)
}

// -- the sources, converted exactly as the composition root converts them -----------------------

// dsSources is the test's copy of the composition root's adapter: each read goes to the owning
// module's own service and the conversion is a field copy, nothing else. If these conversions drift
// from cmd/core's, the cmd/core wiring leg fails; if the OWNER's read drifts from its own answer, the
// ties here fail.
type dsSources struct {
	credit   *billing.CreditService
	invoices *billing.Repository
	ap       *purchasing.Service
	gl       *gl.Service
	stock    *inventory.Service
}

func (s dsSources) ReadARAging(ctx context.Context, asOf time.Time) ([]ARAgingLine, error) {
	rows, err := s.credit.Aging(ctx, asOf, "")
	if err != nil {
		return nil, err
	}
	out := make([]ARAgingLine, 0, len(rows))
	for _, r := range rows {
		out = append(out, ARAgingLine{
			CustomerID:       r.CustomerID,
			AccountNo:        r.AccountNo,
			CustomerName:     r.CustomerName,
			BranchID:         r.BranchID,
			TermsDays:        r.TermsDays,
			CreditLimitCents: r.CreditLimitCents,
			CurrentCents:     r.CurrentCents,
			Days30Cents:      r.Days30Cents,
			Days60Cents:      r.Days60Cents,
			Days60To90Cents:  r.Days60To90Cents,
			Days90PlusCents:  r.Days90PlusCents,
			TotalCents:       r.TotalCents,
		})
	}
	return out, nil
}

func (s dsSources) ReadAPAging(ctx context.Context, asOf time.Time) ([]APAgingLine, error) {
	raw := ""
	if !asOf.IsZero() {
		raw = asOf.Format(time.RFC3339)
	}
	page, err := s.ap.APAgingForCaller(ctx, raw, "")
	if err != nil {
		return nil, err
	}
	out := make([]APAgingLine, 0)
	for _, row := range page.Items {
		for _, it := range row.OpenItems {
			out = append(out, APAgingLine{
				VendorID:   it.VendorID,
				VendorName: it.VendorName,
				BranchID:   it.BranchID,
				Kind:       it.Kind,
				Bucket:     it.Bucket,
				RefNo:      it.RefNo,
				BillDate:   it.BillDate,
				DueDate:    it.DueDate,
				TotalCents: it.TotalCents,
				PaidCents:  it.PaidCents,
				OpenCents:  it.OpenCents,
			})
		}
	}
	return out, nil
}

func (s dsSources) ReadGLJournalLines(ctx context.Context, year, month int) ([]GLJournalLine, error) {
	lines, err := s.gl.DatasetJournalLines(ctx, year, month)
	if err != nil {
		return nil, err
	}
	out := make([]GLJournalLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, GLJournalLine{
			AccountCode: l.AccountCode,
			AccountName: l.AccountName,
			FiscalYear:  l.FiscalYear,
			FiscalMonth: l.FiscalMonth,
			BranchID:    l.BranchID,
			EntryNo:     l.EntryNo,
			JournalMemo: l.JournalMemo,
			DebitCents:  l.DebitCents,
			CreditCents: l.CreditCents,
		})
	}
	return out, nil
}

func (s dsSources) ReadSalesLines(ctx context.Context, from, to time.Time) ([]SalesInvoiceLine, error) {
	lines, err := s.invoices.DatasetInvoiceLines(ctx, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]SalesInvoiceLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, SalesInvoiceLine{
			InvoiceID:      l.InvoiceID,
			InvoiceNo:      l.InvoiceNo,
			CustomerID:     l.CustomerID,
			BranchID:       l.BranchID,
			ProductID:      l.ProductID,
			SourceKind:     l.SourceKind,
			InvoiceDate:    l.InvoiceDate,
			Description:    l.Description,
			QtyBase:        l.QtyBase,
			UnitPriceCents: l.UnitPriceCents,
			LineTotalCents: l.LineTotalCents,
			LineTaxCents:   l.LineTaxCents,
		})
	}
	return out, nil
}

func (s dsSources) ReadStockOnHand(ctx context.Context) ([]StockOnHandLine, error) {
	lines, err := s.stock.DatasetStockOnHand(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]StockOnHandLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, StockOnHandLine{
			ProductID:     l.ProductID,
			BranchID:      l.BranchID,
			OnHandBase:    l.OnHandBase,
			UnitCostCents: l.UnitCostCents,
			ValueCents:    l.ValueCents,
		})
	}
	return out, nil
}

// dsService builds the reporting service over the real sources, with the caller stamped onto every
// context the test passes, exactly as the HTTP guard stamps it.
func dsService(t *testing.T, pool *pgxpool.Pool) *Service {
	t.Helper()
	src := dsSources{
		credit:   billing.NewCreditService(billing.NewRepository(pool)),
		invoices: billing.NewRepository(pool),
		ap:       purchasing.NewService(purchasing.NewRepository(pool)),
		gl:       gl.NewService(gl.NewRepository(pool)),
		stock:    inventory.NewService(inventory.NewRepository(pool)),
	}
	return NewService(NewRepository(pool)).
		WithDatasetARSource(src).
		WithDatasetAPSource(src).
		WithDatasetGLSource(src).
		WithDatasetSalesSource(src).
		WithDatasetStockSource(src)
}

func dsGlobalCtx(ctx context.Context, t *testing.T, pool *pgxpool.Pool) context.Context {
	t.Helper()
	return identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID: fixtureActor(t, pool), RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: "ACTIVE",
	})
}

func dsScopedCtx(ctx context.Context, t *testing.T, pool *pgxpool.Pool, branchID string) context.Context {
	t.Helper()
	return identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID: fixtureActor(t, pool), RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Status: "ACTIVE",
		Branches: map[string]bool{branchID: true},
	})
}

// dsOpenInvoice writes a FINALIZED invoice with an open balance, dated inside the fixture window, with
// created_at inside it too so an as-of read counts it. This is the row the aging and the sales set
// both read, each through its own owner.
func (f *fixture) dsOpenInvoice(customerID, branchID, date string, subtotal, tax int64) string {
	id := f.newID()
	f.del(`DELETE FROM invoice_lines WHERE invoice_id=$1::uuid`, id)
	f.del(`DELETE FROM invoices WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO invoices (id, invoice_no, customer_id, branch_id, status, issue_date, due_date,
	          subtotal_cents, tax_cents, holdback_cents, total_cents, balance_cents, source_kind,
	          created_at, finalized_at)
	        VALUES ($1::uuid, $2, $3::uuid, $4::uuid, 'FINALIZED', $5::date, $5::date,
	                $6, $7, 0, $8, $8, 'ORDER', $9::timestamptz, $9::timestamptz)`,
		id, f.tag+"-DS-"+id[:6], customerID, branchID, date, subtotal, tax, subtotal+tax,
		dsInstant(date))
	return id
}

// dsHomeBranch pins a customer's home branch, the branch the aging row reports.
func (f *fixture) dsHomeBranch(customerID, branchID string) {
	f.exec(`UPDATE customers SET home_branch_id=$2::uuid WHERE id=$1::uuid`, customerID, branchID)
}

// dsPostedBill writes a POSTED vendor bill with no posting journal, the legacy rule: the bill counts
// from its own bill date, so an as-of read inside the window holds exactly the fixture's bills.
func (f *fixture) dsPostedBill(vendorID, branchID, billDate string, totalCents int64) string {
	id := f.newID()
	f.del(`DELETE FROM purchasing_bills WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO purchasing_bills (id, bill_no, vendor_id, branch_id, status, match_state,
	          bill_date, due_date, subtotal_cents, tax_cents, total_cents)
	        VALUES ($1::uuid, $2, $3::uuid, $4::uuid, 'POSTED', 'MATCHED',
	                $5::date, $5::date, $6, 0, $6)`,
		id, f.tag+"-DSBILL-"+id[:6], vendorID, branchID, billDate, totalCents)
	return id
}

// dsFiscalPeriod opens a private fiscal period and returns its id.
func (f *fixture) dsFiscalPeriod(year, month int) string {
	var id string
	if err := f.pool.QueryRow(f.ctx,
		`SELECT id::text FROM fiscal_periods WHERE year=$1 AND month=$2`, year, month).Scan(&id); err == nil {
		return id // the period exists; the world opened it. Nothing to clean up.
	}
	id = f.newID()
	f.del(`DELETE FROM fiscal_periods WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO fiscal_periods (id, year, month, status) VALUES ($1::uuid, $2, $3, 'OPEN')`,
		id, year, month)
	return id
}

// dsJournal posts one balanced journal into a period, optionally stamped to a branch. The rows go in
// as DRAFT first: the audit invariant refuses a POSTED journal that arrives without its lines, so the
// lines land, then the journal posts.
func (f *fixture) dsJournal(periodID, branchID, postedAt string, entryNo int, legs []struct {
	accountCode           string
	debitCents, creditCts int64
}) string {
	jid := f.newID()
	f.del(`DELETE FROM gl_journals WHERE id=$1::uuid`, jid)
	f.exec(`INSERT INTO gl_journals (id, fiscal_year, entry_no, period_id, source, memo, status, branch_id)
	        VALUES ($1::uuid,
	                (SELECT year FROM fiscal_periods WHERE id=$2::uuid),
	                $3, $2::uuid, 'MANUAL', $4, 'DRAFT', NULLIF($5,'')::uuid)`,
		jid, periodID, entryNo, f.tag+" dataset journal", branchID)
	lineNo := 0
	for _, leg := range legs {
		lineNo++
		// The lines check demands one side and one side only: the zero side goes in as NULL.
		f.exec(`INSERT INTO gl_journal_lines (journal_id, line_no, account_id, branch_id, debit_cents, credit_cents)
		        VALUES ($1::uuid, $2, (SELECT id FROM gl_accounts WHERE code=$3), NULLIF($4,'')::uuid,
		                NULLIF($5, 0), NULLIF($6, 0))`,
			jid, lineNo, leg.accountCode, branchID, leg.debitCents, leg.creditCts)
	}
	f.exec(`UPDATE gl_journals SET status='POSTED', posted_at=$2::timestamptz WHERE id=$1::uuid`,
		jid, postedAt)
	return jid
}

// dsBinLocation opens a bin location under a branch, so a fixture can hold one product on two
// locations of the same branch.
func (f *fixture) dsBinLocation(branchID, code string) string {
	id := f.newID()
	f.del(`DELETE FROM locations WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name)
	        VALUES ($1::uuid, $2::uuid, $2::uuid, 'BIN', $3, $4)`,
		id, branchID, f.tag+"-"+code, f.tag+" "+code)
	return id
}

func TestIntegration_Dataset_AR_TiesToTheAging(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	ctx := dsGlobalCtx(context.Background(), t, pool)
	svc := dsService(t, pool)

	custA := f.customer("aging north")
	f.dsHomeBranch(custA, trentonBranch)
	custB := f.customer("aging south")
	f.dsHomeBranch(custB, kingstonBranch)
	f.dsOpenInvoice(custA, trentonBranch, dsDay1, 30_000, 0)
	// Both invoices are on the books before the as-of day: the aging reads what existed at the
	// instant, and a row created after it is another day's answer.
	f.dsOpenInvoice(custA, trentonBranch, dsDay1, 12_345, 0)
	f.dsOpenInvoice(custB, kingstonBranch, dsDay1, 5_000, 0)

	asOf := dsInstant(dsAsOf)
	res, err := svc.RunDataset(ctx, "ar", DatasetQuery{
		Columns: []string{"branch_id", "current_cents", "days_1_30_cents", "total_cents"},
		GroupBy: []string{"branch_id"},
		AsOf:    dsAsOf,
	})
	if err != nil {
		t.Fatalf("ar read: %v", err)
	}
	// The tie in words: the set's total is the arithmetic the fixture wrote down, and the set's total
	// is the AR aging's own answer for the same instant, the same read the month-end screen renders.
	wantTotal := int64(47_345)
	if got := res.Totals["total_cents"]; got != wantTotal {
		t.Fatalf("the ar set totals %v, the fixture wrote %v", got, wantTotal)
	}
	aging, err := billing.NewCreditService(billing.NewRepository(pool)).Aging(ctx, asOf, "")
	if err != nil {
		t.Fatalf("the owner's aging read: %v", err)
	}
	var agingTotal int64
	for _, r := range aging {
		agingTotal += r.TotalCents
	}
	if agingTotal != wantTotal {
		t.Fatalf("the aging itself answers %v, the fixture wrote %v", agingTotal, wantTotal)
	}
	// Due on the issue day, the fixture's invoices are four days past due at the as-of day, so the
	// money sits in the 1 to 30 bucket: the buckets are the aging's own arithmetic, and the set reads
	// them as the aging computes them.
	if len(res.Rows) != 2 {
		t.Fatalf("two home branches, two rows, got %v", res.Rows)
	}
	byBranch := map[string]any{}
	for _, row := range res.Rows {
		byBranch[row[0].(string)] = row[2]
	}
	if byBranch[trentonBranch] != int64(42_345) || byBranch[kingstonBranch] != int64(5_000) {
		t.Fatalf("the buckets do not sit by home branch: %v", res.Rows)
	}
	if res.Totals["days_1_30_cents"] != wantTotal || res.Totals["current_cents"] != int64(0) {
		t.Fatalf("at %s the balances are four days past due: 1-30 totals %v, current totals %v",
			dsAsOf, res.Totals["days_1_30_cents"], res.Totals["current_cents"])
	}
}

func TestIntegration_Dataset_AP_TiesToTheAPAging(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	ctx := dsGlobalCtx(context.Background(), t, pool)
	svc := dsService(t, pool)

	vendorN := f.vendor("payables north")
	vendorS := f.vendor("payables south")
	f.dsPostedBill(vendorN, trentonBranch, dsDay1, 21_500)
	f.dsPostedBill(vendorS, kingstonBranch, dsDay1, 9_750)

	res, err := svc.RunDataset(ctx, "ap", DatasetQuery{
		Columns: []string{"branch_id", "open_cents"},
		GroupBy: []string{"branch_id"},
		AsOf:    dsAsOf,
	})
	if err != nil {
		t.Fatalf("ap read: %v", err)
	}
	wantTotal := int64(31_250)
	if got := res.Totals["open_cents"]; got != wantTotal {
		t.Fatalf("the ap set totals %v, the fixture wrote %v", got, wantTotal)
	}
	page, err := purchasing.NewService(purchasing.NewRepository(pool)).APAgingForCaller(ctx, dsAsOf, "")
	if err != nil {
		t.Fatalf("the owner's aging read: %v", err)
	}
	if page.TotalCents != wantTotal {
		t.Fatalf("the ap aging itself answers %v, the fixture wrote %v", page.TotalCents, wantTotal)
	}
	byBranch := map[string]any{}
	for _, row := range res.Rows {
		byBranch[row[0].(string)] = row[1]
	}
	if byBranch[trentonBranch] != int64(21_500) || byBranch[kingstonBranch] != int64(9_750) {
		t.Fatalf("the open cents do not sit by branch: %v", res.Rows)
	}
}

func TestIntegration_Dataset_GL_TiesToTheTrialBalance(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	ctx := dsGlobalCtx(context.Background(), t, pool)
	svc := dsService(t, pool)

	march := f.dsFiscalPeriod(1971, 3)
	april := f.dsFiscalPeriod(1971, 4)
	f.dsJournal(march, trentonBranch, "1971-03-05T12:00:00Z", 990_001, []struct {
		accountCode           string
		debitCents, creditCts int64
	}{
		{"1000", 50_000, 0},
		{"1100", 0, 50_000},
	})
	f.dsJournal(march, kingstonBranch, "1971-03-06T12:00:00Z", 990_002, []struct {
		accountCode           string
		debitCents, creditCts int64
	}{
		{"1100", 25_000, 0},
		{"1000", 0, 25_000},
	})
	f.dsJournal(april, trentonBranch, "1971-04-05T12:00:00Z", 990_003, []struct {
		accountCode           string
		debitCents, creditCts int64
	}{
		{"1000", 7_000, 0},
		{"1100", 0, 7_000},
	})
	// A DRAFT journal is not a posted one: the set and the trial balance must both refuse it.
	draft := f.newID()
	f.del(`DELETE FROM gl_journals WHERE id=$1::uuid`, draft)
	f.exec(`INSERT INTO gl_journals (id, fiscal_year, entry_no, period_id, source, memo, status, posted_at, branch_id)
	        VALUES ($1::uuid, 1971, 990004, $2::uuid, 'MANUAL', $3, 'DRAFT', NULL, NULLIF($4,'')::uuid)`,
		draft, march, f.tag+" never posted", trentonBranch)

	res, err := svc.RunDataset(ctx, "gl", DatasetQuery{
		Columns: []string{"account_code", "debit_cents", "credit_cents"},
		Filters: []DatasetFilter{
			{Column: "fiscal_year", Op: "eq", Values: []any{1971}},
			{Column: "fiscal_month", Op: "eq", Values: []any{3}},
		},
		GroupBy: []string{"account_code"},
	})
	if err != nil {
		t.Fatalf("gl read: %v", err)
	}
	// The tie: the set's per account figures are the trial balance's own rows for the period, the read
	// the close and the money desk render.
	tb, err := gl.NewService(gl.NewRepository(pool)).TrialBalance(ctx, 1971, 3, "")
	if err != nil {
		t.Fatalf("the owner's trial balance: %v", err)
	}
	if len(tb.Rows) == 0 {
		t.Fatal("the trial balance answers no rows for a period the fixture just posted into")
	}
	byCode := map[string][]any{}
	for _, row := range res.Rows {
		byCode[row[0].(string)] = row
	}
	for _, row := range tb.Rows {
		set, ok := byCode[row.AccountCode]
		if !ok {
			t.Fatalf("account %s is on the trial balance but not in the set", row.AccountCode)
		}
		if set[1] != row.DebitCents || set[2] != row.CreditCents {
			t.Fatalf("account %s: the set answers %v/%v, the trial balance answers %d/%d",
				row.AccountCode, set[1], set[2], row.DebitCents, row.CreditCents)
		}
	}
	// April's journal stays out of March's read.
	if res.Totals["debit_cents"] != int64(75_000) || res.Totals["credit_cents"] != int64(75_000) {
		t.Fatalf("march ties 75000 a side, got %v", res.Totals)
	}
	// A whole year read carries both months' books.
	yearRes, err := svc.RunDataset(ctx, "gl", DatasetQuery{
		Columns: []string{"debit_cents"},
		Filters: []DatasetFilter{{Column: "fiscal_year", Op: "eq", Values: []any{1971}}},
	})
	if err != nil {
		t.Fatalf("gl year read: %v", err)
	}
	if yearRes.Totals["debit_cents"] != int64(82_000) {
		t.Fatalf("the year read carries april too, got %v", yearRes.Totals)
	}
}

func TestIntegration_Dataset_Sales_TiesToTheInvoices(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	ctx := dsGlobalCtx(context.Background(), t, pool)
	svc := dsService(t, pool)

	cust := f.customer("sales tie")
	prod := f.product("DSSKU", "dataset tie product", "")
	inA := f.dsOpenInvoice(cust, trentonBranch, dsDay1, 30_000, 0)
	f.invoiceLine(inA, prod, 1, 1, 10_000, 10_000)
	f.invoiceLine(inA, prod, 2, 2, 10_000, 20_000)
	inB := f.dsOpenInvoice(cust, kingstonBranch, dsDay9, 4_500, 0)
	f.invoiceLine(inB, prod, 1, 3, 1_500, 4_500)
	// A VOID document is not a sale, and an invoice outside the window is another report's row.
	inVoid := f.invoice(cust, trentonBranch, "", dsDay1, 88_888, 0, "VOID", 88_888)
	f.invoiceLine(inVoid, prod, 1, 1, 88_888, 88_888)
	inLate := f.dsOpenInvoice(cust, trentonBranch, "1971-04-01", 500, 0)
	f.invoiceLine(inLate, prod, 1, 1, 500, 500)

	res, err := svc.RunDataset(ctx, "sales", DatasetQuery{
		Columns: []string{"invoice_id", "line_total_cents"},
		Filters: []DatasetFilter{
			{Column: "invoice_date", Op: "ge", Values: []any{dsDay1}},
			{Column: "invoice_date", Op: "le", Values: []any{dsDay9}},
		},
		GroupBy: []string{"invoice_id"},
	})
	if err != nil {
		t.Fatalf("sales read: %v", err)
	}
	// The tie: each invoice's lines sum to the header the writer posted, and the void and the
	// out-of-window documents are nobody's sale.
	byInvoice := map[string]int64{}
	for _, row := range res.Rows {
		byInvoice[row[0].(string)] = row[1].(int64)
	}
	if len(byInvoice) != 2 {
		t.Fatalf("two live invoices in the window, the set answers %d: %v", len(byInvoice), byInvoice)
	}
	for _, inv := range []struct {
		id       string
		subtotal int64
	}{{
		id:       inA,
		subtotal: 30_000,
	}, {
		id:       inB,
		subtotal: 4_500,
	}} {
		if got := byInvoice[inv.id]; got != inv.subtotal {
			t.Fatalf("invoice %s: its lines answer %d, the header's subtotal is %d", inv.id, got, inv.subtotal)
		}
		var header int64
		if err := pool.QueryRow(context.Background(),
			`SELECT subtotal_cents FROM invoices WHERE id=$1::uuid`, inv.id).Scan(&header); err != nil {
			t.Fatalf("read the header back: %v", err)
		}
		if header != inv.subtotal {
			t.Fatalf("fixture drift: the header says %d, the test wrote %d", header, inv.subtotal)
		}
	}
	if res.Totals["line_total_cents"] != int64(34_500) {
		t.Fatalf("the window's sales total to 34500 cents, got %v", res.Totals)
	}
}

func TestIntegration_Dataset_Stock_TiesToStockValueCents(t *testing.T) {
	pool := intPool(t)
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	f := newFixture(t, pool)
	ctx := dsGlobalCtx(context.Background(), t, pool)
	svc := dsService(t, pool)
	inv := inventory.NewService(inventory.NewRepository(pool))

	before, err := inv.StockValueCents(ctx, "")
	if err != nil {
		t.Fatalf("stock value before: %v", err)
	}
	trentonLoc := f.branchLocation(trentonBranch)
	trentonLoc2 := f.dsBinLocation(trentonBranch, "DSBIN")
	kingstonLoc := f.branchLocation(kingstonBranch)
	prod := f.product("DSSTOCK", "dataset stock product", "")
	f.del(`DELETE FROM product_branch_costs WHERE product_id=$1::uuid`, prod)
	f.exec(`INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
	        VALUES ($1::uuid, $2::uuid, 1000), ($1::uuid, $3::uuid, 1000)`,
		prod, trentonBranch, kingstonBranch)
	// Trenton holds the product on TWO locations of its branch: the set aggregates to product by
	// branch, and the value is the sum of the per balance rounded figures, the same arithmetic the
	// valuation writes. Kingston holds one balance on its own location.
	f.del(`DELETE FROM stock_balances WHERE product_id=$1::uuid`, prod)
	f.exec(`INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base, allocated_base)
	        VALUES ($1::uuid, $2::uuid, $3::uuid, 3.5, 0), ($1::uuid, $4::uuid, $3::uuid, 2.25, 0)`,
		prod, trentonLoc, trentonBranch, trentonLoc2)
	f.exec(`INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base, allocated_base)
	        VALUES ($1::uuid, $2::uuid, $3::uuid, 1, 0)`, prod, kingstonLoc, kingstonBranch)

	after, err := inv.StockValueCents(ctx, "")
	if err != nil {
		t.Fatalf("stock value after: %v", err)
	}
	wantDelta := after - before // 3.5 and 2.25 and 1 at 1000 cents: 3500 + 2250 + 1000

	res, err := svc.RunDataset(ctx, "stock", DatasetQuery{
		Columns: []string{"product_id", "branch_id", "on_hand_base", "unit_cost_cents", "value_cents"},
		Filters: []DatasetFilter{{Column: "product_id", Op: "eq", Values: []any{prod}}},
	})
	if err != nil {
		t.Fatalf("stock read: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("two branches carry the product, the set answers %d rows: %v", len(res.Rows), res.Rows)
	}
	var setValue int64
	for _, row := range res.Rows {
		setValue += row[4].(int64)
	}
	if setValue != wantDelta {
		t.Fatalf("the set values the product at %d cents, the valuation moved %d cents", setValue, wantDelta)
	}
	if res.Totals["on_hand_base"] != 6.75 {
		t.Fatalf("on hand sums across the locations to 6.75, got %v", res.Totals["on_hand_base"])
	}
	for _, row := range res.Rows {
		if row[3] != int64(1_000) {
			t.Fatalf("the unit cost column reads the branch's own cost, got %v", row[3])
		}
	}
}

func TestIntegration_Dataset_AScopedCallerSeesOnlyItsBranches(t *testing.T) {
	pool := intPool(t)
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	f := newFixture(t, pool)
	globalCtx := dsGlobalCtx(context.Background(), t, pool)
	scopedCtx := dsScopedCtx(context.Background(), t, pool, trentonBranch)
	svc := dsService(t, pool)

	// One row of every set in Trenton, one in Kingston, both branches live.
	custN := f.customer("fence north")
	f.dsHomeBranch(custN, trentonBranch)
	custS := f.customer("fence south")
	f.dsHomeBranch(custS, kingstonBranch)
	f.dsOpenInvoice(custN, trentonBranch, dsDay1, 11_000, 0)
	f.dsOpenInvoice(custS, kingstonBranch, dsDay1, 13_000, 0)

	vendorN := f.vendor("fence vendor north")
	vendorS := f.vendor("fence vendor south")
	f.dsPostedBill(vendorN, trentonBranch, dsDay1, 2_100)
	f.dsPostedBill(vendorS, kingstonBranch, dsDay1, 2_300)

	march := f.dsFiscalPeriod(1971, 3)
	f.dsJournal(march, trentonBranch, "1971-03-05T12:00:00Z", 990_011, []struct {
		accountCode           string
		debitCents, creditCts int64
	}{
		{"1000", 4_100, 0},
		{"1100", 0, 4_100},
	})
	f.dsJournal(march, kingstonBranch, "1971-03-05T12:00:00Z", 990_012, []struct {
		accountCode           string
		debitCents, creditCts int64
	}{
		{"1000", 4_300, 0},
		{"1100", 0, 4_300},
	})

	prod := f.product("DSFENCE", "dataset fence product", "")
	f.del(`DELETE FROM product_branch_costs WHERE product_id=$1::uuid`, prod)
	f.exec(`INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
	        VALUES ($1::uuid, $2::uuid, 500), ($1::uuid, $3::uuid, 500)`,
		prod, trentonBranch, kingstonBranch)
	trentonLoc := f.branchLocation(trentonBranch)
	f.del(`DELETE FROM stock_balances WHERE product_id=$1::uuid`, prod)
	f.exec(`INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base, allocated_base)
	        VALUES ($1::uuid, $2::uuid, $3::uuid, 2, 0)`, prod, trentonLoc, trentonBranch)
	kingstonLoc := f.branchLocation(kingstonBranch)
	f.exec(`INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base, allocated_base)
	        VALUES ($1::uuid, $2::uuid, $3::uuid, 4, 0)`, prod, kingstonLoc, kingstonBranch)

	salesIn := f.dsOpenInvoice(custN, trentonBranch, dsDay1, 1_700, 0)
	f.invoiceLine(salesIn, prod, 1, 1, 1_700, 1_700)
	salesOut := f.dsOpenInvoice(custS, kingstonBranch, dsDay1, 1_900, 0)
	f.invoiceLine(salesOut, prod, 1, 1, 1_900, 1_900)

	// Every set read through the scoped caller answers Trenton's rows and nothing else, and its totals
	// are the Trenton slice of the global answer for the same query.
	for _, tc := range []struct {
		set       string
		money     string
		wantTotal any
		query     DatasetQuery
	}{
		{
			// Both Trenton invoices age onto the account: the 11000 document and the 1700 one.
			set: "ar", money: "total_cents", wantTotal: int64(12_700),
			query: DatasetQuery{
				Columns: []string{"branch_id", "total_cents"},
				GroupBy: []string{"branch_id"},
				AsOf:    dsAsOf,
			},
		},
		{
			set: "ap", money: "open_cents", wantTotal: int64(2_100),
			query: DatasetQuery{
				Columns: []string{"branch_id", "open_cents"},
				GroupBy: []string{"branch_id"},
				AsOf:    dsAsOf,
			},
		},
		{
			set: "gl", money: "debit_cents", wantTotal: int64(4_100),
			query: DatasetQuery{
				Columns: []string{"branch_id", "debit_cents"},
				Filters: []DatasetFilter{
					{Column: "fiscal_year", Op: "eq", Values: []any{1971}},
					{Column: "fiscal_month", Op: "eq", Values: []any{3}},
				},
				GroupBy: []string{"branch_id"},
			},
		},
		{
			set: "sales", money: "line_total_cents", wantTotal: int64(1_700),
			query: DatasetQuery{
				Columns: []string{"branch_id", "line_total_cents"},
				Filters: []DatasetFilter{
					{Column: "invoice_date", Op: "ge", Values: []any{dsDay1}},
					{Column: "invoice_date", Op: "le", Values: []any{dsDay9}},
				},
				GroupBy: []string{"branch_id"},
			},
		},
		{
			set: "stock", money: "value_cents", wantTotal: int64(1_000),
			query: DatasetQuery{
				Columns: []string{"branch_id", "value_cents"},
				Filters: []DatasetFilter{{Column: "product_id", Op: "eq", Values: []any{prod}}},
				GroupBy: []string{"branch_id"},
			},
		},
	} {
		scoped, err := svc.RunDataset(scopedCtx, tc.set, tc.query)
		if err != nil {
			t.Fatalf("%s: scoped read: %v", tc.set, err)
		}
		if len(scoped.Rows) != 1 || scoped.Rows[0][0] != trentonBranch {
			t.Fatalf("%s: the scoped caller must see Trenton alone, got %v", tc.set, scoped.Rows)
		}
		if scoped.Totals[tc.money] != tc.wantTotal {
			t.Fatalf("%s: the scoped totals answer %v, Trenton's slice is %v",
				tc.set, scoped.Totals[tc.money], tc.wantTotal)
		}
		global, err := svc.RunDataset(globalCtx, tc.set, tc.query)
		if err != nil {
			t.Fatalf("%s: global read: %v", tc.set, err)
		}
		var globalTrenton any
		for _, row := range global.Rows {
			if row[0] == trentonBranch {
				globalTrenton = row[1]
			}
		}
		if globalTrenton != tc.wantTotal {
			t.Fatalf("%s: the global answer's Trenton slice is %v, the test wrote %v",
				tc.set, globalTrenton, tc.wantTotal)
		}
	}
}
