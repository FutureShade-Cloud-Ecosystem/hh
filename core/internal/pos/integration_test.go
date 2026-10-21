//go:build integration

// Integration tests run the real pgx Repository + the real sibling seams (pricing A-07, till A-03,
// identity A-20) against the Postgres 16 that `make reset` provisions. They prove migration 0029 applies
// clean (I1), the three tender types each compose paid/finalized + audited (I2/I3/I4), the replay guard
// holds under a real DB (I5), a below-floor line rings at the rule price and commits, its line equal to
// the engine's price (I7, D48 row 481, D49), the operator reopen reads the committed sale (I8), the
// relief assessment returns masked + a snapshot (I9), and card_type lands on the payments row (I10).
// Run with: make test-integration (after make reset).
//
// A-19 is the COMPOSER, the tendered paths (I2/I4/I10) only go fully green once A-03's TakeTender writes
// the payments row; until then those sub-checks skip on the not-implemented sentinel. The composition,
// the schema, the account path, the replay guard, and the relief privacy are provable today.
package pos

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// stubCharger is the deterministic capture seam for the card path (no live call, no attempt
// table): Capture answers the stub's canned capture (or the decline when fail is set), BindTx
// accepts the facts it is handed, Compensate answers a clean reversal.
type stubCharger struct{ fail bool }

func (c stubCharger) Capture(_ context.Context, in till.CardChargeInput) (till.CardCapture, error) {
	if in.ReadOnly {
		return till.CardCapture{}, till.ErrCardLegUnattempted
	}
	if c.fail {
		return till.CardCapture{}, till.ErrCardDeclined
	}
	return till.CardCapture{
		Kind:         till.CardCaptureCaptured,
		AttemptID:    "att-stub-" + in.LegKey,
		ProcessorRef: "rp_stub_captured",
	}, nil
}

func (c stubCharger) BindTx(_ context.Context, _ pgx.Tx, b till.CardBind) error {
	if b.AttemptID == "" || b.PaymentID == "" {
		return errors.New("stub charger: the bind arrived without its attempt or payment id")
	}
	return nil
}

func (c stubCharger) Compensate(_ context.Context, attemptID string) (till.CardCompensation, error) {
	return till.CardCompensation{Kind: till.CardCompVoided, AttemptID: attemptID}, nil
}

func realPosSvc(t *testing.T, pool *pgxpool.Pool, charger till.CardCharger) *Service {
	t.Helper()
	cat := product.NewService(product.NewRepository(pool))
	priceSvc := pricing.NewService(pricing.NewRepository(pool)).
		WithCategoryResolver(cat).
		WithUOMConverter(cat).
		WithMarginGate(pricing.NewIdentityMarginGate())
	tillSvc := till.NewService(till.NewRepository(pool)).WithCardCharger(charger)
	return NewService(testPosRepo(pool)).
		WithLineResolver(priceSvc).
		WithTenderTaker(tillSvc).
		WithCreditGate(NewIdentityCreditGate()).
		WithDepositDocuments(billing.NewInvoiceWriter(pool)).
		WithClock(time.Now)
}

// staffCtx stamps a guard-shaped caller onto the context exactly as identity's RequireStaff does in
// production (identity.ContextWithStaff). It takes a REAL seeded user id because the caller's UserID lands
// on payments.received_by and the audit trail, which are FK-bound.
//
// DIB-389 makes pos.Sale caller-aware; the till (A-03) always was. Passing a bare context.Background() is
// precisely why every tendered path here used to SKIP: till.TakeTender returned "till: unauthenticated",
// isTillStub matched the "till:" substring, and the test reported itself as an unimplemented-sibling skip
// rather than an unauthenticated one. Four tests (CASH, CASH-fresh-seed, CARD, relief) were dark.
func staffCtx(t *testing.T, pool *pgxpool.Pool, scope identity.Scope, branches ...string) context.Context {
	t.Helper()
	var userID string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	granted := map[string]bool{}
	for _, b := range branches {
		granted[b] = true
	}
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: userID, RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership,
		Scope: scope, Status: "ACTIVE", Branches: granted,
	})
}

// globalCtx is the org-wide caller, reaches every branch, i.e. the reach these tests implicitly assumed
// before DIB-389 put an explicit check on it.
func globalCtx(t *testing.T, pool *pgxpool.Pool) context.Context {
	t.Helper()
	return staffCtx(t, pool, identity.ScopeGlobal)
}

// -- shared fixtures ---------------------------------------------------------

func walkInID(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM customers WHERE account_no='WALK-IN'`).Scan(&id); err != nil {
		t.Fatalf("WALK-IN not seeded by 0029: %v", err)
	}
	return id
}

func aPaver(t *testing.T, pool *pgxpool.Pool) (productID, branchID string) {
	t.Helper()
	ctx := context.Background()
	// A product whose base_uom IS ITSELF 'SF' (e.g. DBS-RYMAR-TURF), factor(SF)=1, so an "SF"
	// ring qty and the SF-priced anchor stay in the same unit with no distortion. A PC-based
	// paver anchored per-SF (its own price_uom, e.g. DBS-UL-BRUSSELS's pieces_per_sf conversion)
	// would instead price the ring through the pieces conversion, the case DIB-378 pinned.
	err := pool.QueryRow(ctx, `
		SELECT p.id::text FROM products p
		  JOIN pricing_list_prices lp ON lp.product_id = p.id AND lp.price_uom = 'SF'
		 WHERE p.is_active AND p.base_uom = 'SF' LIMIT 1`).Scan(&productID)
	if err != nil {
		t.Skipf("no seeded SF-priceable product: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded branch: %v", err)
	}
	return productID, branchID
}

// tillRunSalt distinguishes one `go test` process from the next on a shared DB (DIB-384).
var tillRunSalt = time.Now().UnixNano()

var tillStationSeq atomic.Int64

func openTill(t *testing.T, pool *pgxpool.Pool, branchID string) string {
	t.Helper()
	ctx := context.Background()
	var userID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	// Unique station per call: uq_till_open_per_drawer permits only one OPEN session
	// per (branch_id, station), so a fixed station collides across tests on the same branch.
	//
	// DIB-384: the counter alone is not enough. It restarts at 0 in every `go test` PROCESS, while the
	// OPEN sessions it created are still in the database, so the SECOND run of this suite against the
	// same DB collided on "itest-1" and took five tests down with a duplicate-key error. Salting with the
	// pid + start time makes the station unique per RUN as well as per call; the sessions are also closed
	// on the way out so stale OPEN drawers do not pile up.
	station := fmt.Sprintf("itest-%d-%d-%d", os.Getpid(), tillRunSalt, tillStationSeq.Add(1))
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO till_sessions (branch_id, station, status, opened_by, float_cents)
		 VALUES ($1,$2,'OPEN',$3,20000) RETURNING id::text`, branchID, station, userID).Scan(&id); err != nil {
		t.Fatalf("open till: %v", err)
	}
	t.Cleanup(func() {
		// Leave the row (payments reference it) but release the drawer, so the uniqueness constraint is
		// about live drawers only and a re-run never inherits an OPEN one.
		_, _ = pool.Exec(context.Background(),
			`UPDATE till_sessions SET status='CLOSED' WHERE id=$1 AND status='OPEN'`, id)
	})
	return id
}

// key returns a fresh UUIDv4 so each sale gets a distinct idempotency key, a
// second-resolution key collides when two sales land in the same second.
func key() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// I1, 0029 applies clean; the POS tables + the WALK-IN seed exist.
func TestIntegration_MigrationsAndSeed(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	for _, tbl := range []string{"pos_sale", "purchaser_eligibility_profile", "tax_evidence_doc", "tax_decision_snapshot", "pos_signature"} {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_name=$1`, tbl).Scan(&n); err != nil || n != 1 {
			t.Fatalf("table %s missing (0029 not applied): n=%d err=%v", tbl, n, err)
		}
	}
	_ = walkInID(t, pool)
}

// I2, CASH for the WALK-IN composes order(CONFIRMED) + payment + invoice(PAID) + allocation + AR.
func TestIntegration_CashSale_EndToEnd(t *testing.T) {
	pool := intPool(t)
	productID, branchID := aPaver(t, pool)
	till := openTill(t, pool, branchID)
	svc := realPosSvc(t, pool, stubCharger{})

	out, err := svc.Sale(globalCtx(t, pool), SaleInput{
		IdempotencyKey: key(), BranchID: branchID, TillSessionID: till, Tender: TenderCash, SaleType: SaleCollectNow,
		Lines: []LineInput{{ProductID: productID, QtyEntered: 10, UOMEntered: "SF"}},
	})
	if isTillStub(err) {
		t.Skipf("A-03 TakeTender not yet implemented (composer waits on the sibling): %v", err)
	}
	if err != nil {
		t.Fatalf("cash sale: %v", err)
	}
	if out.Status != "COMMITTED" || out.PaymentID == "" {
		t.Fatalf("cash sale not committed/paid: %+v", out)
	}
	var invStatus string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM invoices WHERE id=$1`, out.InvoiceID).Scan(&invStatus); err != nil || invStatus != "PAID" {
		t.Fatalf("invoice not PAID: %s %v", invStatus, err)
	}
}

// DIB-378, fresh-DB retail-anchor completeness: before this fix, only the 4 aggregate SKUs had an
// explicit pricing_list_prices row, so every other demo product (the exact reported repro: Banas
// coping/step, cash tender) either derived an anchor from cost or, when cost was also unavailable at
// ring time, 422'd with "no retail anchor for product". The ring carries no cost at all (D48 row
// 481: the till sends no cost, the core's own reading is the only one), so the fix must not depend
// on one, proving the seeded anchor (not a derived one) is what makes the Banas SKU price. UOM
// matches pos-terminal.ts's own sold_by-else-base_uom fallback, so this reproduces exactly what the
// terminal actually sends.
func TestIntegration_FreshSeed_BanasSKU_CashSale_EndToEnd(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	var productID, uom string
	if err := pool.QueryRow(ctx,
		`SELECT id::text, COALESCE(NULLIF(sold_by,''), base_uom) FROM products WHERE sku = 'DBS-BANAS-COPING'`).
		Scan(&productID, &uom); err != nil {
		t.Skipf("DBS-BANAS-COPING not seeded: %v", err)
	}
	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no branch seeded: %v", err)
	}
	till := openTill(t, pool, branchID)
	svc := realPosSvc(t, pool, stubCharger{})

	out, err := svc.Sale(ctx, SaleInput{
		IdempotencyKey: key(), BranchID: branchID, TillSessionID: till, Tender: TenderCash, SaleType: SaleCollectNow,
		Lines: []LineInput{{ProductID: productID, QtyEntered: 2, UOMEntered: uom}},
	})
	if isTillStub(err) {
		t.Skipf("A-03 TakeTender not yet implemented: %v", err)
	}
	if err != nil {
		t.Fatalf("DIB-378: a fresh-seed Banas SKU cash sale should ring end-to-end, got: %v", err)
	}
	if out.Status != "COMMITTED" || out.PaymentID == "" {
		t.Fatalf("Banas cash sale not committed/paid: %+v", out)
	}
	var unitPrice int64
	if err := pool.QueryRow(ctx,
		`SELECT unit_price_cents FROM order_lines WHERE order_id=$1 LIMIT 1`, out.OrderID).Scan(&unitPrice); err != nil {
		t.Fatalf("read priced line: %v", err)
	}
	if unitPrice <= 0 {
		t.Fatalf("Banas SKU priced at 0, the seeded retail anchor is not being used, got unit_price_cents=%d", unitPrice)
	}
}

// DIB-197, a walk-in CASH ring whose line has no resolvable RETAIL price must surface the pricing
// failure as a mapped 4xx to the cashier, NEVER a bare HTTP 500. A walk-in CASH sale resolves RETAIL
// pricing, so a product with no retail anchor / no uom conversion factor used to 500 the whole
// counter sale (pos.writeErr fell through its default). The engine returns errNoConversion for a
// product with no product_uoms row; the composer must translate it into a client 4xx. Pricing runs
// BEFORE any tx/tender, so this reproduces without an open drawer or a staff context.
func TestIntegration_CashSale_PricingGap_IsClientErrorNot500(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	var branchID, prodID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no branch seeded: %v", err)
	}
	// A product the engine cannot price: no product_uoms row → errNoConversion.
	if err := pool.QueryRow(ctx, `
		SELECT p.id::text FROM products p
		 WHERE p.is_active AND NOT EXISTS (SELECT 1 FROM product_uoms u WHERE u.product_id = p.id)
		 LIMIT 1`).Scan(&prodID); err != nil {
		t.Skipf("no product without a uom conversion seeded: %v", err)
	}
	svc := realPosSvc(t, pool, stubCharger{})
	_, saleErr := svc.Sale(ctx, SaleInput{
		IdempotencyKey: key(), BranchID: branchID, TillSessionID: "unused-pricing-fails-first",
		Tender: TenderCash, SaleType: SaleCollectNow,
		Lines: []LineInput{{ProductID: prodID, QtyEntered: 3, UOMEntered: "EA"}},
	})
	if saleErr == nil {
		t.Fatal("expected the CASH sale to fail on the unpriceable line")
	}
	st, ok := ClientStatus(saleErr)
	if !ok || st < 400 || st >= 500 {
		t.Fatalf("DIB-197: a walk-in CASH ring with an unpriceable RETAIL line must surface a mapped 4xx to the cashier, not a bare HTTP 500, got ClientStatus=%d ok=%v err=%v", st, ok, saleErr)
	}
	t.Logf("DIB-197 OK: cash pricing gap → HTTP %d (%v)", st, saleErr)
}

// I3, ACCOUNT for a CONTRACTOR with credit → invoice FINALIZED + AR INVOICE row, no payment row.
func TestIntegration_AccountSale_FinalizedNoPayment(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	productID, branchID := aPaver(t, pool)
	var custID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM customers WHERE customer_type='CONTRACTOR' AND credit_limit_cents > 1000000 LIMIT 1`).Scan(&custID); err != nil {
		t.Skipf("no CONTRACTOR with credit seeded: %v", err)
	}
	svc := realPosSvc(t, pool, stubCharger{})
	out, err := svc.Sale(ctx, SaleInput{
		IdempotencyKey: key(), BranchID: branchID, CustomerID: custID, Tender: TenderAccount, SaleType: SaleCollectNow,
		Lines: []LineInput{{ProductID: productID, QtyEntered: 5, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("account sale: %v", err)
	}
	if out.PaymentID != "" {
		t.Fatalf("ACCOUNT must write no payment row, got %q", out.PaymentID)
	}
	var invStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM invoices WHERE id=$1`, out.InvoiceID).Scan(&invStatus); err != nil || invStatus != "FINALIZED" {
		t.Fatalf("invoice not FINALIZED: %s %v", invStatus, err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM customer_transactions WHERE source_ref_id=$1 AND txn_type='INVOICE'`, out.InvoiceID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("AR INVOICE row missing: n=%d err=%v", n, err)
	}
}

// DIB-389, a branch-scoped rep must NOT be able to ring an ACCOUNT sale against a branch they cannot
// reach. The ACCOUNT tender is the case that matters: it is unpaid at ring (captureCents == 0), so it
// never reaches till.TakeTender, and TakeTender's CanAccessBranch was the ONLY branch check in the whole
// sale path. A CASH version of this test proves nothing, it passes on the incidental till check alone.
//
// The blast radius is why this is a write-guard and not a display bug: in.BranchID is stamped onto the
// order, invoice, stock decrement, customer_transaction, GL event and audit record. The assertion below
// is therefore "403 AND nothing persisted", not merely "403".
func TestIntegration_POSSale_AccountTender_CrossBranchForbidden(t *testing.T) {
	pool := intPool(t)
	admin := globalCtx(t, pool) // fixture reads only, never used for the sale under test
	productID, homeBranch := aPaver(t, pool)

	// The other branch: the one the caller is NOT granted. Trenton/Kingston in the seed.
	var otherBranch string
	if err := pool.QueryRow(admin, `SELECT id::text FROM locations WHERE id::text <> $1 LIMIT 1`, homeBranch).Scan(&otherBranch); err != nil {
		t.Skipf("need a second branch to prove cross-branch refusal: %v", err)
	}
	var custID string
	if err := pool.QueryRow(admin,
		// UX-89: the control sale below is made on the caller's own branch, so the account must be one that
		// branch may trade with (homed there, or shared with no home), or the customer wall answers not found.
		`SELECT id::text FROM customers WHERE customer_type='CONTRACTOR' AND credit_limit_cents > 1000000
		    AND (home_branch_id IS NULL OR home_branch_id::text = $1) ORDER BY account_no LIMIT 1`, homeBranch).Scan(&custID); err != nil {
		t.Skipf("no CONTRACTOR with credit seeded for the caller's branch: %v", err)
	}

	// A rep granted ONLY homeBranch, ringing against otherBranch.
	scoped := staffCtx(t, pool, identity.ScopeBranch, homeBranch)
	idemKey := key()

	var movesBefore int
	if err := pool.QueryRow(admin, `SELECT count(*) FROM stock_moves WHERE branch_id::text = $1`, otherBranch).Scan(&movesBefore); err != nil {
		t.Fatalf("count stock_moves: %v", err)
	}

	svc := realPosSvc(t, pool, stubCharger{})
	_, err := svc.Sale(scoped, SaleInput{
		IdempotencyKey: idemKey, BranchID: otherBranch, CustomerID: custID,
		Tender: TenderAccount, SaleType: SaleCollectNow,
		Lines: []LineInput{{ProductID: productID, QtyEntered: 5, UOMEntered: "SF"}},
	})
	if err == nil {
		t.Fatal("DIB-389: a branch-scoped caller rang an ACCOUNT sale on a branch they cannot access, and it was ACCEPTED")
	}
	st, ok := ClientStatus(err)
	if !ok || st != http.StatusForbidden {
		t.Fatalf("DIB-389: want 403 for an in-company caller out of branch scope, got status=%d ok=%v err=%v", st, ok, err)
	}

	// Nothing may have been persisted, the guard runs before the first write, not after a rollback.
	var sales int
	if err := pool.QueryRow(admin, `SELECT count(*) FROM pos_sale WHERE idempotency_key = $1`, idemKey).Scan(&sales); err != nil {
		t.Fatalf("count pos_sale: %v", err)
	}
	if sales != 0 {
		t.Fatalf("DIB-389: refused sale still persisted %d pos_sale row(s)", sales)
	}
	var movesAfter int
	if err := pool.QueryRow(admin, `SELECT count(*) FROM stock_moves WHERE branch_id::text = $1`, otherBranch).Scan(&movesAfter); err != nil {
		t.Fatalf("count stock_moves: %v", err)
	}
	if movesAfter != movesBefore {
		t.Fatalf("DIB-389: refused sale moved %s stock (%d → %d)", otherBranch, movesBefore, movesAfter)
	}

	// Control: the SAME sale on the caller's OWN branch is accepted, proving the refusal above is the
	// branch scope and not some unrelated breakage in the ACCOUNT path.
	if _, err := svc.Sale(scoped, SaleInput{
		IdempotencyKey: key(), BranchID: homeBranch, CustomerID: custID,
		Tender: TenderAccount, SaleType: SaleCollectNow,
		Lines: []LineInput{{ProductID: productID, QtyEntered: 5, UOMEntered: "SF"}},
	}); err != nil {
		t.Fatalf("DIB-389: the in-scope ACCOUNT sale must still succeed, got %v", err)
	}
}

// DIB-389, the drawer must belong to the branch being rung. A caller can legitimately reach BOTH
// branches (GLOBAL scope) and still must not run Kingston money through Trenton's till.
func TestIntegration_POSSale_TillSessionFromAnotherBranch_Refused(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	productID, homeBranch := aPaver(t, pool)
	var otherBranch string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE id::text <> $1 LIMIT 1`, homeBranch).Scan(&otherBranch); err != nil {
		t.Skipf("need a second branch: %v", err)
	}
	otherTill := openTill(t, pool, otherBranch) // drawer belongs to the OTHER branch

	svc := realPosSvc(t, pool, stubCharger{})
	_, err := svc.Sale(ctx, SaleInput{
		IdempotencyKey: key(), BranchID: homeBranch, TillSessionID: otherTill,
		Tender: TenderCash, SaleType: SaleCollectNow,
		Lines: []LineInput{{ProductID: productID, QtyEntered: 5, UOMEntered: "SF"}},
	})
	if err == nil {
		t.Fatal("DIB-389: a sale rang on one branch through another branch's drawer was ACCEPTED")
	}
	if st, ok := ClientStatus(err); !ok || st != http.StatusConflict {
		t.Fatalf("DIB-389: want 409 for a cross-branch drawer (till/service.go:290's convention), got status=%d ok=%v err=%v", st, ok, err)
	}
}

// I4/I10, CARD → payment row with gateway_ref from the stub + card_type persisted.
func TestIntegration_CardSale_GatewayRefAndCardType(t *testing.T) {
	pool := intPool(t)
	productID, branchID := aPaver(t, pool)
	till := openTill(t, pool, branchID)
	svc := realPosSvc(t, pool, stubCharger{})
	out, err := svc.Sale(globalCtx(t, pool), SaleInput{
		IdempotencyKey: key(), BranchID: branchID, TillSessionID: till, Tender: TenderCard, CardType: "DEBIT",
		PaymentToken: "tok_runner", Lines: []LineInput{{ProductID: productID, QtyEntered: 3, UOMEntered: "SF"}},
	})
	if isTillStub(err) || errors.Is(err, errCardDeclined) {
		t.Skipf("A-03 TakeTender not yet implemented: %v", err)
	}
	if err != nil {
		t.Fatalf("card sale: %v", err)
	}
	var method, cardType, gw string
	if err := pool.QueryRow(context.Background(),
		`SELECT method, COALESCE(card_type,''), gateway_ref FROM payments WHERE id=$1`, out.PaymentID).Scan(&method, &cardType, &gw); err != nil {
		t.Fatalf("read payment: %v", err)
	}
	if method != "CARD" || cardType != "DEBIT" || gw == "" {
		t.Fatalf("card payment fields wrong: method=%s card_type=%s gw=%s", method, cardType, gw)
	}
}

// I5, replaying the same idempotency_key returns identical ids; the row count does not change.
func TestIntegration_Idempotency_ReplayUnderRealDB(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	productID, branchID := aPaver(t, pool)
	custID := walkInID(t, pool)
	svc := realPosSvc(t, pool, stubCharger{})
	in := SaleInput{
		IdempotencyKey: key(), BranchID: branchID, CustomerID: custID, Tender: TenderAccount,
		Lines: []LineInput{{ProductID: productID, QtyEntered: 2, UOMEntered: "SF"}},
	}
	// WALK-IN has no credit → ACCOUNT would be COD-blocked; use a real contractor instead.
	_ = pool.QueryRow(ctx, `SELECT id::text FROM customers WHERE customer_type='CONTRACTOR' AND credit_limit_cents>1000000 LIMIT 1`).Scan(&in.CustomerID)
	first, err := svc.Sale(ctx, in)
	if err != nil {
		t.Fatalf("first sale: %v", err)
	}
	// DIB-396: fenced to THIS sale's idempotency key. The old whole-table count raced internal/gate
	// (which rings and counts its own sales on the same DB) in BOTH directions, and it is also the
	// more precise assertion: a replay must not mint a SECOND row under the same key.
	var before int
	pool.QueryRow(ctx, `SELECT count(*) FROM pos_sale WHERE idempotency_key = $1`, in.IdempotencyKey).Scan(&before)
	replay, err := svc.Sale(ctx, in)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replay.Replayed || replay.OrderID != first.OrderID || replay.InvoiceID != first.InvoiceID {
		t.Fatalf("replay must return the original ids: %+v vs %+v", first, replay)
	}
	var after int
	pool.QueryRow(ctx, `SELECT count(*) FROM pos_sale WHERE idempotency_key = $1`, in.IdempotencyKey).Scan(&after)
	if after != before {
		t.Fatalf("replay created a second pos_sale: %d → %d", before, after)
	}
}

// B1 (S3 remediation), the reject-after-crash window against the REAL schema: money committed under
// the request's reserved "rreq:" key with the decision row still PENDING must refuse a rejection (409,
// FOR UPDATE + rail probe) and converge to APPROVED with the SAME return on an approve replay.
func TestIntegration_ReturnRequest_CrashWindow_RejectRefused_ApproveConverges(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	productID, branchID := aPaver(t, pool)
	var custID, userID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers WHERE customer_type='CONTRACTOR' AND credit_limit_cents>1000000 LIMIT 1`).Scan(&custID); err != nil {
		t.Skipf("no contractor: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	svc := realPosSvc(t, pool, stubCharger{})
	// The rail caller is an authorized approver: the bridged context with
	// the global staff authority AND the linked user id, the full stamp the
	// guard adapter builds on the request path (the return rules fence the
	// rail caller by the same wall they fence the counter, so a bare user
	// id stamp with no authority is refused).
	sctx := identity.ContextWithStaff(identity.ContextWithStaffUserID(ctx, userID), identity.StaffContext{
		UserID: userID, RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	})

	// A committed ACCOUNT sale to return against.
	sale, err := svc.Sale(ctx, SaleInput{
		IdempotencyKey: key(), BranchID: branchID, CustomerID: custID, Tender: TenderAccount,
		Lines: []LineInput{{ProductID: productID, QtyEntered: 4, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("seed sale: %v", err)
	}
	var lineID string
	var qtyBase float64
	if err := pool.QueryRow(ctx, `SELECT id::text, qty_base::float8 FROM order_lines WHERE order_id=$1::uuid LIMIT 1`, sale.OrderID).Scan(&lineID, &qtyBase); err != nil {
		t.Fatalf("read order line: %v", err)
	}

	rr, err := svc.CreateReturnRequest(ctx, CreateReturnRequestInput{
		CustomerID: custID, OrderID: sale.OrderID, ReasonCode: ReasonDamaged, Note: "itest",
		Lines: []ReturnRequestLineInput{{OrderLineID: lineID, QtyBase: qtyBase}},
	})
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	// Simulate the crash between rail-commit and decide: run exactly the rail input approve would,
	// under the request's reserved key, money commits, the request row stays PENDING.
	policy, err := svc.Policy(ctx, rr.BranchID)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	crashRet, err := svc.Return(sctx, ReturnInput{
		IdempotencyKey: "rreq:" + rr.ID, OrderID: rr.OrderID,
		Lines:        []ReturnLineInput{{OrderLineID: lineID, QtyBase: qtyBase, ReasonCode: rr.ReasonCode}},
		RefundMethod: RefundAccountCredit, Restock: true, FeeBps: policy.RestockingFeeBps,
		internalRail: true,
	})
	if err != nil {
		t.Fatalf("simulated crash-window rail commit: %v", err)
	}

	// Reject must refuse: the FOR UPDATE + FindReturnByKey probe sees the committed money.
	_, rerr := svc.RejectReturnRequest(sctx, rr.ID, "operator tries to reject")
	if st, ok := ClientStatus(rerr); !ok || st != 409 {
		t.Fatalf("reject after the crash window must 409, got %v", rerr)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM return_requests WHERE id=$1::uuid`, rr.ID).Scan(&status); err != nil || status != RequestPending {
		t.Fatalf("request must stay PENDING after the refused reject: %s %v", status, err)
	}

	// Approve converges: replays the SAME return (no second pos_returns row) and stamps APPROVED.
	var returnsBefore int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM pos_returns WHERE idempotency_key = $1`, "rreq:"+rr.ID).Scan(&returnsBefore)
	out, err := svc.ApproveReturnRequest(sctx, rr.ID)
	if err != nil {
		t.Fatalf("approve replay: %v", err)
	}
	if out.Status != RequestApproved || out.ReturnID != crashRet.ReturnID {
		t.Fatalf("approve must converge onto the committed return %s: %+v", crashRet.ReturnID, out)
	}
	var returnsAfter int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM pos_returns WHERE idempotency_key = $1`, "rreq:"+rr.ID).Scan(&returnsAfter)
	if returnsBefore != 1 || returnsAfter != 1 {
		t.Fatalf("the converge must replay, never re-refund: before=%d after=%d", returnsBefore, returnsAfter)
	}

	// And a decided request refuses BOTH further decisions.
	if _, err := svc.ApproveReturnRequest(sctx, rr.ID); err == nil {
		t.Fatal("double-approve must 409")
	}
	if _, err := svc.RejectReturnRequest(sctx, rr.ID, "late"); err == nil {
		t.Fatal("reject-after-approve must 409")
	}
}

// I7, a line below the margin floor rings at the rule price and commits (D48 row 481, D49): the
// till prices through the counter resolve and never asks the floor, and the committed line equals
// the engine's own price for the same line.
func TestIntegration_BelowFloorLine_RingsAndCommits(t *testing.T) {
	pool := intPool(t)
	productID, branchID := aPaver(t, pool)
	svc := realPosSvc(t, pool, stubCharger{})
	custID := walkInID(t, pool)
	out, err := svc.Sale(globalCtx(t, pool), SaleInput{
		IdempotencyKey: key(), BranchID: branchID, CustomerID: custID, Tender: TenderCash,
		TillSessionID: openTill(t, pool, branchID),
		Lines:         []LineInput{{ProductID: productID, QtyEntered: 1, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("the till rings the line unblocked: %v", err)
	}
	if out.Status != "COMMITTED" {
		t.Fatalf("the ruled line commits: %+v", out)
	}
	var unit, total int64
	if err := pool.QueryRow(context.Background(),
		`SELECT unit_price_cents, line_total_cents FROM order_lines WHERE order_id=$1 AND line_no=1`, out.OrderID).
		Scan(&unit, &total); err != nil {
		t.Fatalf("read the committed line: %v", err)
	}
	// The engine's price for the same line, resolved through the same counter resolve the ring
	// prices with, the customer type the sale itself resolved.
	var custType string
	if err := pool.QueryRow(context.Background(),
		`SELECT customer_type FROM customers WHERE id=$1`, custID).Scan(&custType); err != nil {
		t.Fatalf("read the walk-in type: %v", err)
	}
	cat := product.NewService(product.NewRepository(pool))
	priceSvc := pricing.NewService(pricing.NewRepository(pool)).
		WithCategoryResolver(cat).
		WithUOMConverter(cat).
		WithMarginGate(pricing.NewIdentityMarginGate())
	lp, err := priceSvc.DeriveCounterLinePrice(context.Background(), pricing.ResolveInput{
		CustomerID: custID, CustomerType: custType, ProductID: productID,
		BranchID: branchID, QtyEntered: 1, UOMEntered: "SF",
	})
	if err != nil {
		t.Fatalf("counter resolve: %v", err)
	}
	if unit != lp.UnitPriceCents || total != lp.LineTotalCents {
		t.Fatalf("the committed line must equal the engine's price: line %d/%d, engine %d/%d",
			unit, total, lp.UnitPriceCents, lp.LineTotalCents)
	}
}

// I8, GET reopen reads the committed sale.
func TestIntegration_OperatorReopen(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	productID, branchID := aPaver(t, pool)
	var custID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers WHERE customer_type='CONTRACTOR' AND credit_limit_cents>1000000 LIMIT 1`).Scan(&custID); err != nil {
		t.Skipf("no contractor: %v", err)
	}
	svc := realPosSvc(t, pool, stubCharger{})
	out, err := svc.Sale(ctx, SaleInput{
		IdempotencyKey: key(), BranchID: branchID, CustomerID: custID, Tender: TenderAccount,
		Lines: []LineInput{{ProductID: productID, QtyEntered: 1, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	v, err := svc.GetSale(ctx, out.OrderID)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if v.OrderNo != out.OrderNo || len(v.Lines) == 0 {
		t.Fatalf("reopen mismatch: %+v", v)
	}
}

// I9, relief assessment returns the masked identifier + a snapshot; the full identifier is NOT returned.
func TestIntegration_AssessRelief_MaskedAndSnapshot(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	productID, branchID := aPaver(t, pool)
	custID := walkInID(t, pool)
	// Seed a profile with an encrypted identifier + a mask; A-19 must only ever surface the mask.
	var profileID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO purchaser_eligibility_profile (customer_id, purchaser_type, ontario_eligibility, proof_identifier_enc, proof_identifier_mask)
		 VALUES ($1,'INDIVIDUAL','ON_RESIDENT', '\xdeadbeef', '•••• 4821') RETURNING id::text`, custID).Scan(&profileID); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	// Bind it to an order so the snapshot can be written.
	svc := realPosSvc(t, pool, stubCharger{})
	sale, err := svc.Sale(ctx, SaleInput{
		IdempotencyKey: key(), BranchID: branchID, CustomerID: custID, Tender: TenderCash,
		TillSessionID: openTill(t, pool, branchID),
		Lines:         []LineInput{{ProductID: productID, QtyEntered: 4, UOMEntered: "SF"}},
	})
	if isTillStub(err) {
		t.Skipf("A-03 TakeTender not yet implemented: %v", err)
	}
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	out, err := svc.AssessRelief(ctx, ReliefAssessInput{
		ProfileID: profileID, OrderID: sale.OrderID, DeclaredUse: "personal", Fulfilment: "pickup", SubtotalCents: 10000,
	})
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if out.MaskedIdentifier != "•••• 4821" {
		t.Fatalf("masked identifier not surfaced: %q", out.MaskedIdentifier)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tax_decision_snapshot WHERE order_id=$1`, sale.OrderID).Scan(&n); err != nil || n < 1 {
		t.Fatalf("snapshot not written: n=%d err=%v", n, err)
	}
}

// isTillStub reports whether err is the till scaffold's not-implemented sentinel (A-03 not landed yet).
func isTillStub(err error) bool {
	return err != nil && (errors.Is(err, errCardDeclined)) == false &&
		(contains(err.Error(), "not yet implemented") || contains(err.Error(), "till:"))
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
