//go:build integration

// Integration tests for the DIB-410 counter receipt against the real Postgres: a REAL committed
// counter sale renders a print document carrying the #SO number, every line, the HST line and the
// tender rows, refused across branches, marked REPRINT on the second fetch, and writing a
// print_queue row per fetch (the DIB-411 integration). Shared fixtures live in integration_test.go.
//
// Run with: DATABASE_URL=... go test -tags integration ./internal/printing/
package printing

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// stubCharger is the deterministic card capture seam (no live processor call, no attempt table),
// the pos integration idiom on the capture shape: a canned capture, an accepting bind, a clean
// reversal.
type stubCharger struct{}

func (stubCharger) Capture(_ context.Context, in till.CardChargeInput) (till.CardCapture, error) {
	if in.ReadOnly {
		return till.CardCapture{}, till.ErrCardLegUnattempted
	}
	return till.CardCapture{Kind: till.CardCaptureCaptured, AttemptID: "att-stub-" + in.LegKey, ProcessorRef: "rp_stub_captured"}, nil
}

func (stubCharger) BindTx(_ context.Context, _ pgx.Tx, b till.CardBind) error {
	if b.AttemptID == "" || b.PaymentID == "" {
		return errors.New("stub charger: the bind arrived without its attempt or payment id")
	}
	return nil
}

func (stubCharger) Compensate(_ context.Context, attemptID string) (till.CardCompensation, error) {
	return till.CardCompensation{Kind: till.CardCompVoided, AttemptID: attemptID}, nil
}

// realPosSvc composes the real POS service so these tests ring a REAL sale, a receipt for a sale
// that was fabricated by an INSERT would prove nothing about the document the counter produces.
func realPosSvc(t *testing.T, pool *pgxpool.Pool) *pos.Service {
	t.Helper()
	cat := product.NewService(product.NewRepository(pool))
	priceSvc := pricing.NewService(pricing.NewRepository(pool)).
		WithCategoryResolver(cat).
		WithUOMConverter(cat).
		WithMarginGate(pricing.NewIdentityMarginGate())
	tillSvc := till.NewService(till.NewRepository(pool)).WithCardCharger(stubCharger{})
	return pos.NewService(pos.NewRepository(pool)).
		WithLineResolver(priceSvc).
		WithTenderTaker(tillSvc).
		WithCreditGate(pos.NewIdentityCreditGate()).
		WithClock(time.Now)
}

// otherBranch returns a branch that is NOT branchID, the cross-branch leg needs a real second one.
func otherBranch(t *testing.T, pool *pgxpool.Pool, branchID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM locations WHERE loc_type='BRANCH' AND id <> $1::uuid LIMIT 1`, branchID).Scan(&id); err != nil {
		t.Skipf("no second branch seeded (cross-branch leg needs two): %v", err)
	}
	return id
}

var (
	runSalt    = time.Now().UnixNano()
	stationSeq atomic.Int64
)

func openTill(t *testing.T, pool *pgxpool.Pool, branchID string) string {
	t.Helper()
	station := fmt.Sprintf("print-itest-%d-%d-%d", os.Getpid(), runSalt, stationSeq.Add(1))
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO till_sessions (branch_id, station, status, opened_by, float_cents)
		 VALUES ($1,$2,'OPEN',$3,20000) RETURNING id::text`, branchID, station, seededUser(t, pool)).Scan(&id); err != nil {
		t.Fatalf("open till: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`UPDATE till_sessions SET status='CLOSED' WHERE id=$1 AND status='OPEN'`, id)
	})
	return id
}

func key() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ringCashSale rings a REAL committed CASH sale and returns it.
func ringCashSale(t *testing.T, pool *pgxpool.Pool) (pos.Sale, string) {
	t.Helper()
	productID, branchID := aPaver(t, pool)
	svc := realPosSvc(t, pool)
	sale, err := svc.Sale(globalCtx(t, pool), pos.SaleInput{
		IdempotencyKey: key(), BranchID: branchID, TillSessionID: openTill(t, pool, branchID),
		Tender: pos.TenderCash, SaleType: pos.SaleCollectNow,
		Lines: []pos.LineInput{
			{ProductID: productID, QtyEntered: 10, UOMEntered: "SF"},
			{ProductID: productID, QtyEntered: 4, UOMEntered: "SF"},
		},
	})
	if err != nil {
		t.Fatalf("ring cash sale: %v", err)
	}
	if sale.Status != "COMMITTED" {
		t.Fatalf("sale not committed: %+v", sale)
	}
	return sale, branchID
}

func printingSvcs(pool *pgxpool.Pool) (*ReceiptService, *QueueService) {
	repo := NewRepository(pool)
	q := NewQueueService(repo)
	return NewReceiptService(repo, q), q
}

// -- DIB-410: the counter receipt --------------------------------------------

// The acceptance: a committed sale renders a print document carrying the #SO number, every line, the
// HST line and the tender rows, and every fetch writes its print_queue row (the DIB-411 integration).
func TestIntegration_Receipt_RendersCommittedSale(t *testing.T) {
	pool := intPool(t)
	sale, branchID := ringCashSale(t, pool)
	ctx := globalCtx(t, pool)
	receipts, q := printingSvcs(pool)

	rec, err := receipts.Receipt(ctx, sale.OrderID)
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	html, err := RenderReceiptHTML(rec)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	t.Run("the #SO number is on the document", func(t *testing.T) {
		if !strings.HasPrefix(sale.OrderNo, "#SO-") {
			t.Fatalf("fixture assumption broken: order_no = %q", sale.OrderNo)
		}
		if rec.OrderNo != sale.OrderNo {
			t.Fatalf("receipt order_no = %q, want %q", rec.OrderNo, sale.OrderNo)
		}
		if !strings.Contains(html, sale.OrderNo) {
			t.Fatalf("the #SO number must be on the page: %q", sale.OrderNo)
		}
		if !strings.Contains(html, `class="order-no"`) {
			t.Fatal("the #SO number must be rendered prominently, not buried")
		}
	})

	t.Run("the branch header comes from locations + system_settings", func(t *testing.T) {
		if rec.BranchName == "" || rec.BranchCode == "" {
			t.Fatalf("branch header not resolved: %+v", rec)
		}
		if rec.OrgName == "" {
			t.Fatal("the org name (system_settings.org_name) must letterhead the receipt")
		}
		if !strings.Contains(html, rec.OrgName) || !strings.Contains(html, rec.BranchName) {
			t.Fatal("the header must be on the page")
		}
	})

	t.Run("every sold line is on the document with qty + uom + unit + total", func(t *testing.T) {
		if len(rec.Lines) != 2 {
			t.Fatalf("want the 2 rung lines, got %d", len(rec.Lines))
		}
		for _, l := range rec.Lines {
			if l.Description == "" || l.UOM == "" || l.UnitPriceCents <= 0 || l.LineTotalCents <= 0 {
				t.Fatalf("line %d incomplete: %+v", l.LineNo, l)
			}
			if !strings.Contains(html, Money(l.LineTotalCents)) {
				t.Fatalf("line total %s must be on the page", Money(l.LineTotalCents))
			}
			if !strings.Contains(html, Qty(l.Qty)+" "+l.UOM) {
				t.Fatalf("qty+uom %q must be on the page", Qty(l.Qty)+" "+l.UOM)
			}
		}
	})

	t.Run("the HST line states the tax and the configured rate", func(t *testing.T) {
		if rec.TaxCents <= 0 {
			t.Fatalf("a taxable counter sale must carry tax: %d", rec.TaxCents)
		}
		if !strings.HasPrefix(rec.TaxLabel, "HST") {
			t.Fatalf("tax label = %q, want an HST label", rec.TaxLabel)
		}
		if !strings.Contains(html, rec.TaxLabel) || !strings.Contains(html, Money(rec.TaxCents)) {
			t.Fatal("the HST line must be on the page")
		}
		if !strings.Contains(html, Money(rec.SubtotalCents)) || !strings.Contains(html, Money(rec.TotalCents)) {
			t.Fatal("subtotal + total must be on the page")
		}
	})

	t.Run("the tender rows say how it was paid", func(t *testing.T) {
		if len(rec.Tenders) == 0 {
			t.Fatal("a receipt with no tender row reads like an unpaid mistake")
		}
		var total int64
		for _, tn := range rec.Tenders {
			total += tn.AmountCents
			if !strings.Contains(html, tn.Label) {
				t.Fatalf("tender %q must be on the page", tn.Label)
			}
		}
		if total != rec.TotalCents {
			t.Fatalf("tendered %d != total %d on a fully paid cash sale", total, rec.TotalCents)
		}
		if rec.Tenders[0].Method != "CASH" {
			t.Fatalf("a CASH sale's tender row must say CASH, got %q", rec.Tenders[0].Method)
		}
	})

	t.Run("the timestamp is the sale's, not the print's", func(t *testing.T) {
		if rec.SoldAt.IsZero() {
			t.Fatal("the receipt must carry when the sale happened")
		}
		if !strings.Contains(html, rec.SoldAt.Format("2006-01-02 15:04")) {
			t.Fatal("the sale timestamp must be on the page")
		}
	})

	t.Run("the sale type is plain words, not the raw constant", func(t *testing.T) {
		// The fixture rings COLLECT_NOW; the served document must carry the plain words in the same
		// muted line, and the domain constant must not reach the customer's paper.
		if rec.SaleType != pos.SaleCollectNow {
			t.Fatalf("fixture assumption broken: sale_type = %q", rec.SaleType)
		}
		if !strings.Contains(html, `<div class="muted">Collected at the counter</div>`) {
			t.Fatalf("a COLLECT_NOW sale's receipt must render the plain words, got sale type line over: %q", rec.SaleType)
		}
		if strings.Contains(html, pos.SaleCollectNow) {
			t.Fatal("the raw sale type constant must not print on the served receipt")
		}
	})

	t.Run("DIB-411: the fetch wrote a PRINTED print_queue row for this order", func(t *testing.T) {
		jobs, err := q.ListForRef(ctx, DocReceipt, sale.OrderID)
		if err != nil {
			t.Fatalf("list jobs: %v", err)
		}
		if len(jobs) != 1 {
			t.Fatalf("one fetch → one job, got %d", len(jobs))
		}
		if jobs[0].Status != StatusPrinted || jobs[0].BranchID != branchID {
			t.Fatalf("the receipt job must be PRINTED at the sale's branch: %+v", jobs[0])
		}
		if jobs[0].RenderTarget != TargetBrowser {
			t.Fatalf("a counter receipt renders in the BROWSER, got %q", jobs[0].RenderTarget)
		}
	})

	t.Run("the visible #SO number opens the same receipt", func(t *testing.T) {
		// The cashier holds the number printed on the paper, not a uuid.
		byNo, err := receipts.Receipt(ctx, sale.OrderNo)
		if err != nil {
			t.Fatalf("receipt by order_no %q: %v", sale.OrderNo, err)
		}
		if byNo.OrderID != rec.OrderID {
			t.Fatalf("#SO lookup resolved a different sale: %q vs %q", byNo.OrderID, rec.OrderID)
		}
		sansHash, err := receipts.Receipt(ctx, strings.TrimPrefix(sale.OrderNo, "#"))
		if err != nil || sansHash.OrderID != rec.OrderID {
			t.Fatalf("the number without its '#' must resolve identically: %v", err)
		}
	})
}

// DIB-410, THE GUARD. The sale's branch must be reachable by the caller (CanAccessBranch, the
// order/edit.go authorizeEdit shape). A receipt names the customer, every line and the tender: a
// cashier scoped to the other branch must not be able to pull it. Removing the check kills this test.
func TestIntegration_Receipt_CrossBranchIsForbidden(t *testing.T) {
	pool := intPool(t)
	sale, branchID := ringCashSale(t, pool)
	receipts, _ := printingSvcs(pool)

	t.Run("a caller scoped to another branch is refused", func(t *testing.T) {
		foreign := staffCtx(t, pool, identity.ScopeBranch, otherBranch(t, pool, branchID))
		if _, err := receipts.Receipt(foreign, sale.OrderID); err == nil {
			t.Fatal("a cross-branch caller must NOT receive the receipt")
		} else if err != ErrForbidden {
			t.Fatalf("want ErrForbidden (403), got %v", err)
		}
	})

	t.Run("a caller scoped to the sale's own branch is served", func(t *testing.T) {
		own := staffCtx(t, pool, identity.ScopeBranch, branchID)
		if _, err := receipts.Receipt(own, sale.OrderID); err != nil {
			t.Fatalf("the sale's own branch must be served: %v", err)
		}
	})

	// The oracle guard: "#SO-4" is sequential, so a cross-branch NUMBER must answer not-found rather
	// than the 403 a cross-branch uuid gets, a 403 would confirm the sale exists to anyone counting.
	t.Run("a cross-branch #SO number is not-found, not 403", func(t *testing.T) {
		foreign := staffCtx(t, pool, identity.ScopeBranch, otherBranch(t, pool, branchID))
		if _, err := receipts.Receipt(foreign, sale.OrderNo); err != ErrNotFound {
			t.Fatalf("want ErrNotFound for a cross-branch #SO number, got %v", err)
		}
	})

	t.Run("the refused fetch wrote no print row", func(t *testing.T) {
		// Exactly one job, from the ALLOWED fetch above. The refusal must not have queued paper.
		_, q := printingSvcs(pool)
		jobs, err := q.ListForRef(globalCtx(t, pool), DocReceipt, sale.OrderID)
		if err != nil {
			t.Fatalf("list jobs: %v", err)
		}
		if len(jobs) != 1 {
			t.Fatalf("only the permitted fetch may record a print, got %d rows", len(jobs))
		}
	})
}

// DIB-410, the reprint marker. The count lives in print_queue, so it survives a refresh, another
// cashier and another terminal, which is the whole reason a duplicate receipt is detectable at all.
func TestIntegration_Receipt_ReprintMarkerOnSecondFetch(t *testing.T) {
	pool := intPool(t)
	sale, _ := ringCashSale(t, pool)
	ctx := globalCtx(t, pool)
	receipts, _ := printingSvcs(pool)

	t.Run("the first fetch is the original", func(t *testing.T) {
		rec, err := receipts.Receipt(ctx, sale.OrderID)
		if err != nil {
			t.Fatalf("first fetch: %v", err)
		}
		if rec.IsReprint() || rec.ReprintCount != 0 {
			t.Fatalf("the customer's first copy must NOT say REPRINT: %d", rec.ReprintCount)
		}
		html, _ := RenderReceiptHTML(rec)
		if strings.Contains(html, "REPRINT") {
			t.Fatal("no REPRINT marker on an original")
		}
	})

	t.Run("the second fetch is REPRINT #1", func(t *testing.T) {
		rec, err := receipts.Receipt(ctx, sale.OrderID)
		if err != nil {
			t.Fatalf("second fetch: %v", err)
		}
		if !rec.IsReprint() || rec.ReprintCount != 1 {
			t.Fatalf("the second copy must be REPRINT #1, got %d", rec.ReprintCount)
		}
		html, err := RenderReceiptHTML(rec)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if !strings.Contains(html, "REPRINT #1") {
			t.Fatal("the REPRINT marker must be on the second copy")
		}
	})

	t.Run("a third fetch counts up, and the queue has one row per fetch", func(t *testing.T) {
		rec, err := receipts.Receipt(ctx, sale.OrderID)
		if err != nil {
			t.Fatalf("third fetch: %v", err)
		}
		if rec.ReprintCount != 2 {
			t.Fatalf("the third copy must be REPRINT #2, got %d", rec.ReprintCount)
		}
		var n int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM print_queue WHERE doc_type='RECEIPT' AND ref_id=$1::uuid AND status='PRINTED'`,
			sale.OrderID).Scan(&n); err != nil {
			t.Fatalf("count rows: %v", err)
		}
		if n != 3 {
			t.Fatalf("three fetches → three PRINTED rows, got %d", n)
		}
	})
}

// An unknown sale is a 404, never an empty document, and never an existence oracle.
func TestIntegration_Receipt_UnknownSaleIsNotFound(t *testing.T) {
	pool := intPool(t)
	receipts, _ := printingSvcs(pool)
	ctx := globalCtx(t, pool)
	if _, err := receipts.Receipt(ctx, newUUID()); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := receipts.Receipt(ctx, "not-a-uuid"); err != ErrNotFound {
		t.Fatalf("a malformed id must be not-found at the boundary, got %v", err)
	}
}

// The scan leg: the simulated flag follows the ROW's own gateway ref, not the rail currently
// wired. A CARD row whose ref carries the till's capture stub's prefix is a simulated capture and
// stamps the flag; a CARD row carrying a foreign ref (a live gateway writes its own shapes) must
// not stamp. The rows here are direct inserts (the bankrec fixture idiom): the flag is the
// repository's stamping decision over the real row, and that decision is what this leg pins. The
// tie leg below covers the case this one cannot: a ref the real stub minted.
func TestIntegration_Receipt_TenderSimulatedFlagFollowsTheRowRef(t *testing.T) {
	pool := intPool(t)
	_, branchID := aPaver(t, pool)
	insertCard := func(t *testing.T, gatewayRef string) string {
		t.Helper()
		id := newUUID()
		if _, err := pool.Exec(context.Background(),
			`INSERT INTO payments (id, payment_no, method, amount_cents, status, branch_id, gateway_ref)
			 VALUES ($1::uuid, $2, 'CARD', 5000, 'RECEIVED', $3::uuid, $4)`,
			id, "PMT-SIM-"+id[:8], branchID, gatewayRef); err != nil {
			t.Fatalf("insert card payment: %v", err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE id = $1::uuid`, id)
		})
		return id
	}
	repo := NewRepository(pool)
	ctx := context.Background()

	t.Run("a stub-prefix ref stamps the flag", func(t *testing.T) {
		id := insertCard(t, payment.StubCaptureRefPrefix+"5f3a91bc")
		tenders, err := repo.ReadReceiptTenders(ctx, id, "")
		if err != nil {
			t.Fatalf("read tenders: %v", err)
		}
		if len(tenders) != 1 {
			t.Fatalf("want the one card row, got %d", len(tenders))
		}
		if !tenders[0].Simulated {
			t.Fatalf("a CARD capture whose gateway ref begins %q must stamp Simulated, the paper must not claim a live charge", payment.StubCaptureRefPrefix)
		}
	})

	t.Run("a foreign ref does not stamp", func(t *testing.T) {
		id := insertCard(t, "8812340077")
		tenders, err := repo.ReadReceiptTenders(ctx, id, "")
		if err != nil {
			t.Fatalf("read tenders: %v", err)
		}
		if len(tenders) != 1 {
			t.Fatalf("want the one card row, got %d", len(tenders))
		}
		if tenders[0].Simulated {
			t.Fatal("a CARD capture carrying a foreign gateway ref must NOT stamp Simulated, a live charge is a live charge")
		}
	})
}

// The tie: the flag must follow a ref the REAL till capture stub minted, not a fixture built from
// the exported constant. A fixture ref and the repository's check can agree while the stub's own
// minting has drifted from the constant (the round 1 review's bite), so this leg lets
// RunPaymentsStub.Charge mint the ref and feeds that ref through ReadReceiptTenders. While the
// stub mints WITH the constant, the mint and the check cannot disagree: change the constant's
// value and both halves move together, which is what makes the round 1 bite pass every suite.
func TestIntegration_Receipt_SimulatedFlagFollowsAStubMintedRef(t *testing.T) {
	pool := intPool(t)
	_, branchID := aPaver(t, pool)

	res, err := payment.RunPaymentsStub{}.Charge(context.Background(),
		map[string]string{"api_key": "test-key"}, "MID-RECEIPT-TIE", 5000, "tok-tie")
	if err != nil {
		t.Fatalf("stub charge: %v", err)
	}
	if !strings.HasPrefix(res.ProcessorRef, payment.StubCaptureRefPrefix) {
		t.Fatalf("the stub minted %q, which does not begin with the exported prefix %q: the stub and the constant have drifted", res.ProcessorRef, payment.StubCaptureRefPrefix)
	}
	id := newUUID()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO payments (id, payment_no, method, amount_cents, status, branch_id, gateway_ref)
		 VALUES ($1::uuid, $2, 'CARD', 5000, 'RECEIVED', $3::uuid, $4)`,
		id, "PMT-SIM-"+id[:8], branchID, res.ProcessorRef); err != nil {
		t.Fatalf("insert card payment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE id = $1::uuid`, id)
	})

	tenders, err := NewRepository(pool).ReadReceiptTenders(context.Background(), id, "")
	if err != nil {
		t.Fatalf("read tenders: %v", err)
	}
	if len(tenders) != 1 {
		t.Fatalf("want the one card row, got %d", len(tenders))
	}
	if !tenders[0].Simulated {
		t.Fatalf("a CARD capture minted by the real stub (ref %q) must stamp Simulated, the mint and the receipt's check have drifted", res.ProcessorRef)
	}
}

// The past-sale reprint path (row 666 item 3): the receipt is fetched from the ORDER (the route
// resolves the order id or the visible "#SO-" number), and the marker follows the order's own print
// count: the original is unmarked, and the fetch by the visible number is REPRINT #1 on the paper.
func TestIntegration_Receipt_ReprintByTheOrderNumber(t *testing.T) {
	pool := intPool(t)
	sale, _ := ringCashSale(t, pool)
	ctx := globalCtx(t, pool)
	receipts, _ := printingSvcs(pool)

	t.Run("the original, fetched by the order id", func(t *testing.T) {
		rec, err := receipts.Receipt(ctx, sale.OrderID)
		if err != nil {
			t.Fatalf("first fetch: %v", err)
		}
		if rec.IsReprint() {
			t.Fatal("the first fetch is the original")
		}
	})

	t.Run("the reprint, fetched by the visible number, is marked with its ordinal", func(t *testing.T) {
		rec, err := receipts.Receipt(ctx, sale.OrderNo)
		if err != nil {
			t.Fatalf("reprint by order_no: %v", err)
		}
		if !rec.IsReprint() || rec.ReprintCount != 1 {
			t.Fatalf("the reprint must be #1, got %d", rec.ReprintCount)
		}
		html, err := RenderReceiptHTML(rec)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if !strings.Contains(html, "REPRINT #1") {
			t.Fatal("the marker must be on the paper")
		}
	})
}

// The deposit and the change due, read from the committed rows (row 666 item 1). The deposit is the
// ring's own capture: the payments allocated to the sale's invoice whose received_at equals the
// invoice's created_at, both now() of the ring's ONE transaction, so a later settlement (a
// different, later transaction) joins the tender rows and moves the balance, and never rides the
// deposit figure. The change due is the order's own cash-tendered fact, the ring's derivation.
func TestIntegration_Receipt_DepositAndChangeDueFromTheCommittedRows(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	productID, branchID := aPaver(t, pool)
	var custID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM customers WHERE customer_type='CONTRACTOR' AND credit_limit_cents > 1000000 LIMIT 1`).Scan(&custID); err != nil {
		t.Skipf("no CONTRACTOR with credit seeded: %v", err)
	}
	svc := realPosSvc(t, pool)

	t.Run("a deposit sale states the deposit and the balance that remains", func(t *testing.T) {
		sale, err := svc.Sale(ctx, pos.SaleInput{
			IdempotencyKey: key(), BranchID: branchID, CustomerID: custID,
			TillSessionID: openTill(t, pool, branchID),
			Tender:        pos.TenderAccount, SaleType: pos.SaleCollectNow,
			DepositCents: 4000, DepositMethod: pos.TenderCash,
			Lines: []pos.LineInput{{ProductID: productID, QtyEntered: 10, UOMEntered: "SF"}},
		})
		if err != nil {
			t.Fatalf("deposit ring: %v", err)
		}
		receipts, _ := printingSvcs(pool)
		rec, err := receipts.Receipt(ctx, sale.OrderID)
		if err != nil {
			t.Fatalf("receipt: %v", err)
		}
		if rec.DepositCents != 4000 {
			t.Fatalf("the deposit must read 4000 from the ring's own capture, got %d", rec.DepositCents)
		}
		if rec.BalanceCents != sale.TotalCents-4000 {
			t.Fatalf("the balance must be the invoice's total less the deposit, got %d want %d", rec.BalanceCents, sale.TotalCents-4000)
		}
		html, err := RenderReceiptHTML(rec)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if !strings.Contains(html, "Deposit") || !strings.Contains(html, Money(4000)) {
			t.Fatal("the Deposit row must be on the paper")
		}
		if !strings.Contains(html, "Balance due") || !strings.Contains(html, Money(sale.TotalCents-4000)) {
			t.Fatal("the Balance due row must be on the paper")
		}

		// A LATER settlement against the same invoice: a second payment, a
		// different transaction, allocated by the AR side. The tender rows grow
		// and the balance moves; the deposit figure does not.
		settlement := newUUID()
		if _, err := pool.Exec(ctx,
			`INSERT INTO payments (id, payment_no, method, amount_cents, status, branch_id, customer_id, received_at)
			 VALUES ($1::uuid, $2, 'CASH', 2000, 'RECEIVED', $3::uuid, $4::uuid, now() + interval '1 hour')`,
			settlement, "PMT-ITEST-"+settlement[:8], branchID, custID); err != nil {
			t.Fatalf("insert settlement: %v", err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE id = $1::uuid`, settlement) })
		if _, err := pool.Exec(ctx,
			`INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents) VALUES ($1::uuid, $2::uuid, 2000)`,
			settlement, sale.InvoiceID); err != nil {
			t.Fatalf("allocate settlement: %v", err)
		}
		// The settlement's third write, the one the AR side's own code performs:
		// the invoice's balance moves down by the settled amount.
		if _, err := pool.Exec(ctx,
			`UPDATE invoices SET balance_cents = balance_cents - 2000 WHERE id = $1::uuid`,
			sale.InvoiceID); err != nil {
			t.Fatalf("settle invoice balance: %v", err)
		}
		after, err := receipts.Receipt(ctx, sale.OrderID)
		if err != nil {
			t.Fatalf("receipt after settlement: %v", err)
		}
		if after.DepositCents != 4000 {
			t.Fatalf("a later settlement must never ride the deposit figure, got %d", after.DepositCents)
		}
		if after.BalanceCents != sale.TotalCents-6000 {
			t.Fatalf("the balance must move with the settlement, got %d want %d", after.BalanceCents, sale.TotalCents-6000)
		}
		if len(after.Tenders) != 2 {
			t.Fatalf("the tender rows must carry the deposit and the settlement, got %d", len(after.Tenders))
		}
	})

	t.Run("an over-tendered cash sale states the change due", func(t *testing.T) {
		sale, err := svc.Sale(ctx, pos.SaleInput{
			IdempotencyKey: key(), BranchID: branchID,
			TillSessionID: openTill(t, pool, branchID),
			Tender:        pos.TenderCash, SaleType: pos.SaleCollectNow,
			CashTenderedCents: 15000,
			Lines:             []pos.LineInput{{ProductID: productID, QtyEntered: 1, UOMEntered: "SF"}},
		})
		if err != nil {
			t.Fatalf("cash ring: %v", err)
		}
		if sale.ChangeDueCents != 15000-sale.TotalCents {
			t.Fatalf("fixture assumption: the ring's own change due %d, want %d", sale.ChangeDueCents, 15000-sale.TotalCents)
		}
		receipts, _ := printingSvcs(pool)
		rec, err := receipts.Receipt(ctx, sale.OrderID)
		if err != nil {
			t.Fatalf("receipt: %v", err)
		}
		if rec.ChangeDueCents != 15000-rec.TotalCents {
			t.Fatalf("the change due must read %d from the committed sale, got %d", 15000-rec.TotalCents, rec.ChangeDueCents)
		}
		html, err := RenderReceiptHTML(rec)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if !strings.Contains(html, "Change due") || !strings.Contains(html, Money(rec.ChangeDueCents)) {
			t.Fatal("the Change due row must be on the paper")
		}
	})
}

// The review's P2-1 shape, pinned: a split of cash and account settles the sale itself, pos
// refuses a deposit on it, so the cash leg captured in the ring's transaction is a TENDER and the
// account remainder is the BALANCE, and the paper names no deposit. Run first against the
// ungated read it failed with the reviewer's own figures (the cash leg reading as a deposit).
func TestIntegration_Receipt_NoDepositOnASplitWithAnAccountLeg(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	productID, branchID := aPaver(t, pool)
	var custID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM customers WHERE customer_type='CONTRACTOR' AND credit_limit_cents > 1000000 LIMIT 1`).Scan(&custID); err != nil {
		t.Skipf("no CONTRACTOR with credit seeded: %v", err)
	}
	svc := realPosSvc(t, pool)
	// The split's legs must sum exactly to the total, and the total is the engine's answer, so a
	// plain account probe rings the same line first to learn the figure the legs split.
	probe, err := svc.Sale(ctx, pos.SaleInput{
		IdempotencyKey: key(), BranchID: branchID, CustomerID: custID,
		Tender: pos.TenderAccount, SaleType: pos.SaleCollectNow,
		Lines: []pos.LineInput{{ProductID: productID, QtyEntered: 1, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("probe ring: %v", err)
	}
	cashLeg := int64(3000)
	if cashLeg >= probe.TotalCents {
		cashLeg = probe.TotalCents / 2
	}
	sale, err := svc.Sale(ctx, pos.SaleInput{
		IdempotencyKey: key(), BranchID: branchID, CustomerID: custID,
		TillSessionID: openTill(t, pool, branchID),
		SaleType:      pos.SaleCollectNow,
		Tenders: []pos.TenderLegInput{
			{Method: pos.TenderCash, AmountCents: cashLeg},
			{Method: pos.TenderAccount, AmountCents: probe.TotalCents - cashLeg},
		},
		Lines: []pos.LineInput{{ProductID: productID, QtyEntered: 1, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("split ring: %v", err)
	}
	if sale.SplitTender != true {
		t.Fatal("fixture assumption broken: the ring is not a split")
	}

	receipts, _ := printingSvcs(pool)
	rec, err := receipts.Receipt(ctx, sale.OrderID)
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if rec.DepositCents != 0 {
		t.Fatalf("a split's captured leg must never read as a deposit, got %d (the ring answered deposit_cents %d)", rec.DepositCents, sale.DepositCents)
	}
	if rec.BalanceCents != probe.TotalCents-cashLeg {
		t.Fatalf("the account remainder is the balance, got %d want %d", rec.BalanceCents, probe.TotalCents-cashLeg)
	}
	if len(rec.Tenders) != 1 || rec.Tenders[0].Method != "CASH" || rec.Tenders[0].AmountCents != cashLeg {
		t.Fatalf("the tender rows carry the captured leg alone, got %+v", rec.Tenders)
	}
	html, err := RenderReceiptHTML(rec)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(html, "Deposit") {
		t.Fatal("the paper must carry no Deposit row for a split with an account leg")
	}
	if !strings.Contains(html, "Balance due") || !strings.Contains(html, Money(probe.TotalCents-cashLeg)) {
		t.Fatal("the balance due row must carry the account remainder")
	}
}

// A sale that carries manual discounts prints the role label of the manager who approved each
// one, read from the committed rows (order_lines and pos_sale, migration 0365), on both papers.
// The discounts are planted on the rung sale's rows directly: the approver rule itself is the pos
// module's own integration leg, this one holds the receipt's reads and its words.
func TestIntegration_Receipt_PrintsWhoApprovedTheDiscounts(t *testing.T) {
	pool := intPool(t)
	sale, _ := ringCashSale(t, pool)
	ctx := globalCtx(t, pool)
	var approver string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&approver); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE order_lines SET discount_cents = 700, discount_percent_bps = 500,
		discount_approved_by = $2::uuid, discount_approved_role = 'sales_manager'
		WHERE order_id = $1::uuid AND line_no = 1`, sale.OrderID, approver); err != nil {
		t.Fatalf("plant the line discount: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE pos_sale SET discount_cents = 300,
		discount_approved_by = $2::uuid, discount_approved_role = 'branch_manager'
		WHERE order_id = $1::uuid`, sale.OrderID, approver); err != nil {
		t.Fatalf("plant the ticket discount: %v", err)
	}
	receipts, _ := printingSvcs(pool)
	rec, err := receipts.Receipt(ctx, sale.OrderID)
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	for name, render := range map[string]func(Receipt) (string, error){"roll": RenderReceiptHTML, "page": RenderReceiptPageHTML} {
		html, err := render(rec)
		if err != nil {
			t.Fatalf("%s render: %v", name, err)
		}
		for _, want := range []string{"Discount approved by Sales Manager", "$7.00 off", "Discount approved by Branch Manager", "-$3.00"} {
			if !strings.Contains(html, want) {
				t.Errorf("%s receipt lacks %q", name, want)
			}
		}
	}
}

// The receipt of an order carries the dealer's quantity notice: the dealer default row's wording, and a
// branch row over it, read through the receipt's own branch header.
func TestIntegration_Receipt_CarriesTheQuantityNotice(t *testing.T) {
	pool := intPool(t)
	sale, branchID := ringCashSale(t, pool)
	ctx := globalCtx(t, pool)
	receipts, _ := printingSvcs(pool)
	bg := context.Background()
	if _, err := pool.Exec(bg, `INSERT INTO quote_policy (branch_id, send_sms_enabled)
		SELECT NULL, TRUE WHERE NOT EXISTS (SELECT 1 FROM quote_policy WHERE branch_id IS NULL)`); err != nil {
		t.Fatalf("ensure dealer row: %v", err)
	}
	if _, err := pool.Exec(bg, `UPDATE quote_policy SET quantity_notice = DEFAULT WHERE branch_id IS NULL`); err != nil {
		t.Fatalf("reset dealer notice: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(bg, `UPDATE quote_policy SET quantity_notice = DEFAULT WHERE branch_id IS NULL`)
		_, _ = pool.Exec(bg, `DELETE FROM quote_policy WHERE branch_id = $1`, branchID)
	})

	rec, err := receipts.Receipt(ctx, sale.OrderID)
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if rec.QuantityNotice != defaultQuantityNotice {
		t.Errorf("notice = %q, want the default", rec.QuantityNotice)
	}
	html, _ := RenderReceiptHTML(rec)
	if !strings.Contains(html, defaultQuantityNotice) {
		t.Error("the default notice must be printed on the roll")
	}

	if _, err := pool.Exec(bg, `UPDATE quote_policy SET quantity_notice = 'Dealer words on quantities.' WHERE branch_id IS NULL`); err != nil {
		t.Fatalf("dealer notice: %v", err)
	}
	if rec, err = receipts.Receipt(ctx, sale.OrderID); err != nil || rec.QuantityNotice != "Dealer words on quantities." {
		t.Fatalf("dealer wording = %q, %v", rec.QuantityNotice, err)
	}
	if _, err := pool.Exec(bg, `INSERT INTO quote_policy (branch_id, quantity_notice) VALUES ($1, 'Branch words on quantities.')
		ON CONFLICT (branch_id) DO UPDATE SET quantity_notice = EXCLUDED.quantity_notice`, branchID); err != nil {
		t.Fatalf("branch notice: %v", err)
	}
	if rec, err = receipts.Receipt(ctx, sale.OrderID); err != nil || rec.QuantityNotice != "Branch words on quantities." {
		t.Fatalf("branch must win: %q, %v", rec.QuantityNotice, err)
	}
}
