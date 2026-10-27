//go:build integration

// Integration tests run the real pgx Repository against the private DB the worker provisions (hos_w6_a15),
// with the REAL gl.Service as the GL posting/rule/balance seam (proving the cross-module reason-coded journal)
// and a FAKE stock/cost seam (INV-CORE lands the real one at integration). They exercise: snapshot freeze
// (I1), the maker-checker four-eyes gate (I2), the idempotent cross-module post + the reason→account matrix
// (I3), the lazy recon run-once (I4), and the negative-stock surface (I5). Run with:
//
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5435/hos_w6_a15?sslmode=disable' \
//	  go test -tags=integration ./internal/invcount/... -count=1 -p 1
package invcount

import (
	"context"
	"os"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seeded fixtures (0003 locations, dibbits_seed products/users).
const (
	branchTRT = "5d1b3f6e-0000-4000-9000-000000000001"
	prodP1    = "a7000000-0000-4000-9000-000000000001"
	prodP2    = "a7000000-0000-4000-9000-000000000002"
	userMaker = "d7000000-0000-4000-9000-000000000001"
	userCheck = "d7000000-0000-4000-9000-000000000002"
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hos_w6_a15?sslmode=disable"
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

// - real gl seam adapters (mirror the main.go adapters over gl.Service) -

type glPosterAdapter struct{ svc *gl.Service }

func (a glPosterAdapter) PostJournal(ctx context.Context, in GLJournal) (string, error) {
	lines := make([]gl.ExternalLine, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = gl.ExternalLine{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo}
	}
	return a.svc.PostExternal(ctx, gl.ExternalJournal{
		Source: in.Source, SourceRefID: in.SourceRefID, IdempotencyKey: in.IdempotencyKey,
		Memo: in.Memo, BranchID: in.BranchID, Lines: lines,
	})
}

type glRuleAdapter struct{ svc *gl.Service }

func (a glRuleAdapter) ResolveRule(ctx context.Context, e, r, b string) (string, error) {
	return a.svc.ResolveRule(ctx, e, r, b)
}

type glBalAdapter struct{ svc *gl.Service }

func (a glBalAdapter) GLBalanceThroughCents(ctx context.Context, code string, y, m int, b string) (int64, error) {
	return a.svc.GLBalanceThroughCents(ctx, code, y, m, b)
}

func (a glBalAdapter) GLBalanceCents(ctx context.Context, code string, y, m int, b string) (int64, error) {
	return a.svc.GLBalanceCents(ctx, code, y, m, b)
}

// - fake stock/cost seam (records the adjustment call; INV-CORE lands the real one) -

type fakeStockSeam struct {
	snaps        []Snapshot
	avg          int64
	moves        int
	createdMoves []string // stock_moves ids this fake inserted, the test cleans them up (shared-DB isolation)
}

func (s *fakeStockSeam) SnapshotOnHand(_ context.Context, _ string) ([]Snapshot, error) {
	return s.snaps, nil
}
func (s *fakeStockSeam) WeightedAvgCostCents(_ context.Context, _, _ string) (int64, error) {
	return s.avg, nil
}

func (s *fakeStockSeam) WeightedAvgCostCentsTx(_ context.Context, _ pgx.Tx, _, _ string) (int64, error) {
	return s.avg, nil
}

// PostCountAdjustmentTx writes a real COUNT stock move inside the caller's tx (as the real inventory seam
// would) so count_lines.move_id links to a genuine append-only ledger row. Signed qtyDelta → to/from location.
func (s *fakeStockSeam) PostCountAdjustmentTx(ctx context.Context, tx pgx.Tx, productID, locationID, branchID string, qtyDelta float64, unitCostCents int64, reason string) (string, error) {
	s.moves++
	abs := qtyDelta
	toLoc, fromLoc := any(locationID), any(nil)
	if qtyDelta < 0 {
		abs = -qtyDelta
		toLoc, fromLoc = any(nil), any(locationID)
	}
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO stock_moves
		(product_id, to_location_id, from_location_id, qty_base, qty_entered, uom_entered, move_type,
		 unit_cost_cents, value_cents, branch_id, reason)
		SELECT $1, $2, $3, $4::numeric, $4::numeric, p.base_uom, 'COUNT', $5::bigint, ($4::numeric * $5::numeric)::bigint, $6, $7
		FROM products p WHERE p.id = $1 RETURNING id::text`,
		productID, toLoc, fromLoc, abs, unitCostCents, branchID, reason).Scan(&id)
	if err == nil {
		s.createdMoves = append(s.createdMoves, id)
	}
	return id, err
}

// cleanupMoves deletes the COUNT stock_moves this fake inserted. The real inventory seam maintains
// stock_balances in lockstep with the ledger; this fake writes only the move (by design), so on the SHARED
// integration DB the leftover moves would drift v_stock_balance_drift. Deleting them keeps the run isolated
// (count_lines.move_id is ON DELETE SET NULL, so this is safe).
func (s *fakeStockSeam) cleanupMoves(t *testing.T, pool *pgxpool.Pool) {
	// DIB-396: the fake writes moves WITHOUT the matching balance, so every test that uses it
	// drifts v_stock_balance_drift until the cleanup below runs. Declare the window (taken here,
	// before the first move, and released after the deletes because t.Cleanup is LIFO) so the
	// whole-DB reconciliation gates in internal/gate + internal/platform/db wait it out.
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	t.Cleanup(func() {
		for _, id := range s.createdMoves {
			_, _ = pool.Exec(context.Background(), `DELETE FROM stock_moves WHERE id=$1`, id)
		}
	})
}

func realSvc(pool *pgxpool.Pool, stock StockCounter) *Service {
	glSvc := gl.NewService(gl.NewRepository(pool))
	return NewService(NewRepository(pool)).
		WithStockCounter(stock).
		WithGLPoster(glPosterAdapter{svc: glSvc}).
		WithRuleResolver(glRuleAdapter{svc: glSvc}).
		WithGLBalanceReader(glBalAdapter{svc: glSvc})
}

// ctxAs stamps the acting staff member the way the identity guards do. DIB-390: a count sheet's every
// step is fenced against the sheet's BRANCH, so the caller carries a real scope + branch grant (a Trenton
// branch manager, the tier the approve/post routes already require) rather than a bare user id.
func ctxAs(uid string) context.Context {
	return ctxAsAt(uid, branchTRT)
}

// ctxAsAt is ctxAs for a caller granted a DIFFERENT branch (the cross-branch tests).
func ctxAsAt(uid, branch string) context.Context {
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: uid, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeBranch,
		Status: identity.StatusActive, Branches: map[string]bool{branch: true},
	})
}

// I1: snapshot freeze captures the on-hand basis onto count_lines and flips the sheet FROZEN.
func TestI1_SnapshotFreeze(t *testing.T) {
	pool := intPool(t)
	stock := &fakeStockSeam{avg: 350, snaps: []Snapshot{
		{ProductID: prodP1, LocationID: branchTRT, OnHandBase: 100, AvgCostCents: 350},
		{ProductID: prodP2, LocationID: branchTRT, OnHandBase: 40, AvgCostCents: 8000},
	}}
	svc := realSvc(pool, stock)
	ctx := ctxAs(userMaker)

	sheet, err := svc.CreateSheet(ctx, CreateSheetRequest{BranchID: branchTRT, Scope: "FULL"})
	if err != nil {
		t.Fatal(err)
	}
	if sheet.Status != StatusOpen {
		t.Fatalf("new sheet status = %q", sheet.Status)
	}
	frozen, err := svc.FreezeSheet(ctx, sheet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.Status != StatusFrozen || frozen.FreezeAt == "" {
		t.Fatalf("freeze: status=%q freeze_at=%q", frozen.Status, frozen.FreezeAt)
	}
	detail, err := svc.SheetDetail(ctx, sheet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Lines) != 2 {
		t.Fatalf("frozen lines = %d, want 2", len(detail.Lines))
	}
	for _, l := range detail.Lines {
		if l.ProductID == prodP1 && l.SnapshotQtyBase != 100 {
			t.Errorf("p1 snapshot = %v, want 100", l.SnapshotQtyBase)
		}
	}
}

// I2: the maker-checker four-eyes gate, the maker cannot approve; a distinct checker can; post needs approval.
func TestI2_MakerCheckerGate(t *testing.T) {
	pool := intPool(t)
	stock := &fakeStockSeam{avg: 350, snaps: []Snapshot{{ProductID: prodP1, LocationID: branchTRT, OnHandBase: 100, AvgCostCents: 350}}}
	stock.cleanupMoves(t, pool)
	svc := realSvc(pool, stock)

	sheet, _ := svc.CreateSheet(ctxAs(userMaker), CreateSheetRequest{BranchID: branchTRT})
	if _, err := svc.FreezeSheet(ctxAs(userMaker), sheet.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitCounts(ctxAs(userMaker), sheet.ID, SubmitCountsRequest{
		Lines: []CountLineInput{{ProductID: prodP1, LocationID: branchTRT, CountedQtyBase: 97, ReasonCode: "DAMAGE"}},
	}); err != nil {
		t.Fatal(err)
	}
	// post before approval → 422
	if err := svc.PostSheet(ctxAs(userCheck), sheet.ID); err == nil {
		t.Fatal("post before approval should fail")
	}
	// maker cannot approve their own count → 422
	if _, err := svc.ApproveSheet(ctxAs(userMaker), sheet.ID); err == nil {
		t.Fatal("maker self-approval should fail")
	}
	// a distinct checker approves
	appr, err := svc.ApproveSheet(ctxAs(userCheck), sheet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if appr.ApprovedBy != userCheck {
		t.Fatalf("approved_by = %q", appr.ApprovedBy)
	}
	// now the post succeeds
	if err := svc.PostSheet(ctxAs(userCheck), sheet.ID); err != nil {
		t.Fatalf("post after approval: %v", err)
	}
}

// I2b (S3 W6 B5): editing counts AFTER approval invalidates that approval. A maker who revises quantities once
// the checker has signed off cannot post under the stale approval, the checker must re-approve the numbers
// that will actually post.
func TestI2b_EditAfterApprovalResetsApproval(t *testing.T) {
	pool := intPool(t)
	stock := &fakeStockSeam{avg: 350, snaps: []Snapshot{{ProductID: prodP1, LocationID: branchTRT, OnHandBase: 100, AvgCostCents: 350}}}
	stock.cleanupMoves(t, pool)
	svc := realSvc(pool, stock)

	sheet, _ := svc.CreateSheet(ctxAs(userMaker), CreateSheetRequest{BranchID: branchTRT})
	if _, err := svc.FreezeSheet(ctxAs(userMaker), sheet.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitCounts(ctxAs(userMaker), sheet.ID, SubmitCountsRequest{
		Lines: []CountLineInput{{ProductID: prodP1, LocationID: branchTRT, CountedQtyBase: 97, ReasonCode: "DAMAGE"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApproveSheet(ctxAs(userCheck), sheet.ID); err != nil {
		t.Fatal(err)
	}

	// Maker revises the count AFTER the checker approved → the approval must be cleared.
	edited, err := svc.SubmitCounts(ctxAs(userMaker), sheet.ID, SubmitCountsRequest{
		Lines: []CountLineInput{{ProductID: prodP1, LocationID: branchTRT, CountedQtyBase: 10, ReasonCode: "DAMAGE"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edited.ApprovedBy != "" {
		t.Fatalf("approval survived an edit: approved_by = %q, want cleared", edited.ApprovedBy)
	}
	// Posting under the now-void approval must fail.
	if err := svc.PostSheet(ctxAs(userCheck), sheet.ID); err == nil {
		t.Fatal("post after a post-approval edit should fail, the approval was reset")
	}
	// Re-approval of the revised numbers restores the ability to post.
	if _, err := svc.ApproveSheet(ctxAs(userCheck), sheet.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.PostSheet(ctxAs(userCheck), sheet.ID); err != nil {
		t.Fatalf("post after re-approval: %v", err)
	}
}

// I3: the idempotent cross-module post writes exactly one balanced reason-coded GL journal (1200 leg via
// ResolveRule, 5200 reason leg), a replay posts no duplicate.
func TestI3_PostIdempotentCrossModule(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	stock := &fakeStockSeam{avg: 350, snaps: []Snapshot{{ProductID: prodP1, LocationID: branchTRT, OnHandBase: 100, AvgCostCents: 350}}}
	stock.cleanupMoves(t, pool)
	svc := realSvc(pool, stock)

	sheet, _ := svc.CreateSheet(ctxAs(userMaker), CreateSheetRequest{BranchID: branchTRT})
	if _, err := svc.FreezeSheet(ctxAs(userMaker), sheet.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitCounts(ctxAs(userMaker), sheet.ID, SubmitCountsRequest{
		Lines: []CountLineInput{{ProductID: prodP1, LocationID: branchTRT, CountedQtyBase: 97, ReasonCode: "DAMAGE"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApproveSheet(ctxAs(userCheck), sheet.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.PostSheet(ctxAs(userCheck), sheet.ID); err != nil {
		t.Fatal(err)
	}
	// replay
	if err := svc.PostSheet(ctxAs(userCheck), sheet.ID); err != nil {
		t.Fatal(err)
	}
	if stock.moves != 1 {
		t.Errorf("stock adjustment posted %d times, want 1 (idempotent)", stock.moves)
	}

	// exactly one GL journal for this sheet, balanced, with the 1200 + 5200 legs.
	var journals int
	key := idempotencyKey(sheet.ID)
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`, key).Scan(&journals); err != nil {
		t.Fatal(err)
	}
	if journals != 1 {
		t.Fatalf("gl journals for sheet = %d, want 1", journals)
	}
	rows, err := pool.Query(ctx, `SELECT a.code, COALESCE(jl.debit_cents,0), COALESCE(jl.credit_cents,0)
		FROM gl_journals j JOIN gl_journal_lines jl ON jl.journal_id = j.id JOIN gl_accounts a ON a.id = jl.account_id
		WHERE j.idempotency_key = $1 ORDER BY a.code`, key)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	legs := map[string][2]int64{}
	var dr, cr int64
	for rows.Next() {
		var code string
		var d, c int64
		if err := rows.Scan(&code, &d, &c); err != nil {
			t.Fatal(err)
		}
		legs[code] = [2]int64{d, c}
		dr += d
		cr += c
	}
	if dr != cr || dr == 0 {
		t.Fatalf("journal not balanced/non-zero: DR %d CR %d", dr, cr)
	}
	// -3 units × 350 = -1050 loss → CR 1200 1050, DR 5200 1050
	if legs["1200"] != [2]int64{0, 1050} {
		t.Errorf("1200 leg = %v, want CR 1050", legs["1200"])
	}
	if legs["5200"] != [2]int64{1050, 0} {
		t.Errorf("5200 leg = %v, want DR 1050", legs["5200"])
	}
}

// I4: the monthly recon runs lazily and once per period+branch (a second call returns the same row).
func TestI4_ReconLazyRunOnce(t *testing.T) {
	pool := intPool(t)
	stock := &fakeStockSeam{snaps: []Snapshot{
		{ProductID: prodP1, LocationID: branchTRT, OnHandBase: 100, AvgCostCents: 350}, // 35000
		{ProductID: prodP2, LocationID: branchTRT, OnHandBase: 10, AvgCostCents: 8000}, // 80000
	}}
	svc := realSvc(pool, stock)
	ctx := ctxAs(userMaker)

	// use a distinct period so re-running the whole suite is stable (delete any prior run first).
	const y, m = 2026, 4
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM inventory_recon_runs WHERE fiscal_year=$1 AND month=$2 AND branch_id=$3::uuid`, y, m, branchTRT); err != nil {
		t.Fatal(err)
	}

	d1, err := svc.ReconcileMonth(ctx, y, m, branchTRT)
	if err != nil {
		t.Fatal(err)
	}
	run1, found, err := svc.ReconRunFor(ctx, y, m, branchTRT)
	if err != nil || !found {
		t.Fatalf("recon run not found after run: %v", err)
	}
	if run1.SubledgerCents != 115000 {
		t.Errorf("subledger = %d, want 115000", run1.SubledgerCents)
	}
	if run1.DeltaCents != d1 {
		t.Errorf("delta mismatch %d vs %d", run1.DeltaCents, d1)
	}
	// second call, run-once: same row id, no new insert.
	if _, err := svc.ReconcileMonth(ctx, y, m, branchTRT); err != nil {
		t.Fatal(err)
	}
	run2, _, _ := svc.ReconRunFor(ctx, y, m, branchTRT)
	if run2.ID != run1.ID {
		t.Errorf("run-once violated: %q vs %q", run2.ID, run1.ID)
	}
	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM inventory_recon_runs WHERE fiscal_year=$1 AND month=$2 AND branch_id=$3::uuid`, y, m, branchTRT).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("recon rows = %d, want 1", count)
	}
}

// I5: the negative-stock surface lists products carrying by-design negative on-hand.
func TestI5_NegativeStockSurface(t *testing.T) {
	pool := intPool(t)
	stock := &fakeStockSeam{snaps: []Snapshot{
		{ProductID: prodP1, LocationID: branchTRT, OnHandBase: 100, AvgCostCents: 350},
		{ProductID: prodP2, LocationID: branchTRT, OnHandBase: -5, AvgCostCents: 8000}, // sold before receipt
	}}
	svc := realSvc(pool, stock)
	rows, err := svc.NegativeStock(context.Background(), branchTRT)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ProductID != prodP2 || rows[0].OnHandBase != -5 {
		t.Fatalf("negative-stock surface = %+v, want just p2 @ -5", rows)
	}
}

func (s *fakeStockSeam) SnapshotOnHandTx(ctx context.Context, _ pgx.Tx, branchID string) ([]Snapshot, error) {
	return s.SnapshotOnHand(ctx, branchID)
}
