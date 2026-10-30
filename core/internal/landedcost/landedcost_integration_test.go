//go:build integration

// Integration tests run the real pgx Repository against the private W6 DB (migrations 0041), with the
// inventory cost seam + GL poster FAKED (INV-CORE lands its real seam at integration). They exercise the
// end-to-end A16 flows: USD isolation (the document is USD, the persisted *_cad_cents are CAD), Apply
// allocation + capitalization, idempotent re-apply, Void netting the inventory bump to zero, and the retro
// capitalize/sold-through split. Run with:
//
//	DATABASE_URL=... go test -tags integration ./internal/landedcost/... -p 1
package landedcost

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	trentonBranch = "5d1b3f6e-0000-4000-9000-000000000001"
	prodA         = "a7000000-0000-4000-9000-000000000001"
	prodB         = "a7000000-0000-4000-9000-000000000002"
	// landedPO is the PO a landed cost allocates across. The fake capitalizer returns fixed received lines
	// regardless of po_id, but a landed cost must still carry a valid po_id (a DRAFT with an empty po_id is now a
	// clean 4xx, see TestReceivedLines_EmptyPOIDIsCleanClientError), so every Create stamps this.
	landedPO = "b0000000-0000-4000-9000-000000000001"
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

// fakeGLPoster records posted journals, deduped on the idempotency key (mirrors the GL provider). It returns
// an EMPTY journal id so the landed_costs.gl_journal_id FK stays NULL (no real gl_journals row exists here).
type fakeGLPoster struct {
	mu       sync.Mutex
	journals map[string]GLJournal
}

func (f *fakeGLPoster) PostJournal(_ context.Context, in GLJournal) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.journals == nil {
		f.journals = map[string]GLJournal{}
	}
	if _, ok := f.journals[in.IdempotencyKey]; !ok {
		f.journals[in.IdempotencyKey] = in
	}
	return "", nil
}

// PostJournalTx is PostJournal through the caller's transaction; the fake records + dedupes identically (it
// touches no database, so a nil tx rides fine).
func (f *fakeGLPoster) PostJournalTx(_ context.Context, _ pgx.Tx, in GLJournal) (string, error) {
	return f.PostJournal(context.Background(), in)
}

func (f *fakeGLPoster) debit(key, acct string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	var t int64
	for _, l := range f.journals[key].Lines {
		if l.AccountCode == acct {
			t += l.DebitCents
		}
	}
	return t
}

func (f *fakeGLPoster) credit(key, acct string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	var t int64
	for _, l := range f.journals[key].Lines {
		if l.AccountCode == acct {
			t += l.CreditCents
		}
	}
	return t
}

// globalCaller is a Tier-1 GLOBAL staff context, passes every CanAccessBranch check, so the integration
// flows exercise the money paths without threading a real auth guard. The branch-scope enforcement itself is
// covered by the DB-free TestTenantScope_* unit tests.
func globalCaller(context.Context) (identity.StaffContext, bool) {
	return identity.StaffContext{UserID: "it-global", Scope: identity.ScopeGlobal, Tier: identity.TierLeadership}, true
}

func newSvc(pool *pgxpool.Pool, cap Capitalizer, gl *fakeGLPoster) *Service {
	return NewService(NewRepository(pool)).WithCapitalizer(cap).WithGLPoster(gl).withCaller(globalCaller)
}

// seedPO inserts the fixed-id purchase_orders header a landed cost allocates across, so the landed_costs.po_id
// FK is satisfied. Idempotent (ON CONFLICT DO NOTHING) so the suite can run repeatedly. A landed cost now
// requires a valid po_id (an empty one is a clean 4xx, TestReceivedLines_EmptyPOIDIsCleanClientError); the
// received LINES still come through the fake capitalizer, so only the header is needed here.
func seedPO(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	var vendorID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM vendors LIMIT 1`).Scan(&vendorID); err != nil {
		t.Fatalf("seedPO: need at least one vendor: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO purchase_orders (id, po_no, vendor_id, branch_id, status)
		 VALUES ($1, $2, $3, $4, 'RECEIVED') ON CONFLICT (id) DO NOTHING`,
		landedPO, "LC-IT-PO-1", vendorID, trentonBranch); err != nil {
		t.Fatalf("seedPO: %v", err)
	}
}

// AC2 + AC1 + AC5 + AC6: USD doc converts to CAD, Apply allocates+capitalizes, re-apply is idempotent, Void
// nets the inventory bump to zero.
func TestIntegration_USDApplyIdempotentVoid(t *testing.T) {
	pool := intPool(t)
	seedPO(t, pool)
	ctx := context.Background()
	cap := &fakeCapitalizer{lines: []ReceivedLine{
		{ProductID: prodA, QtyBase: 4, ValueCents: 30000, WeightKg: 2},
		{ProductID: prodB, QtyBase: 6, ValueCents: 70000, WeightKg: 3},
	}}
	gl := &fakeGLPoster{}
	svc := newSvc(pool, cap, gl)

	// USD document: 10000 USD-cents freight × fx 1.35 → 13500 CAD-cents. GL/valuation only ever see CAD.
	id, err := svc.Create(ctx, CreateInput{
		POID: landedPO, BranchID: trentonBranch, Currency: "usd", FXRate: 1.35,
		FreightCents: 10000, AllocationBasis: "value",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := svc.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Currency != "USD" || got.FreightCents != 10000 || got.FreightCadCents != 13500 {
		t.Fatalf("USD isolation: currency=%s freight=%d cad=%d, want USD/10000/13500", got.Currency, got.FreightCents, got.FreightCadCents)
	}
	if got.FXRate != 1.35 {
		t.Fatalf("fx not persisted: %v", got.FXRate)
	}

	// Apply: value basis over 30000/70000 → 4050/9450 of 13500.
	if err := svc.Apply(ctx, id); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, _ = svc.Get(ctx, id)
	if got.Status != StatusPosted {
		t.Fatalf("status = %s, want POSTED", got.Status)
	}
	if cap.caps[prodA] != 4050 || cap.caps[prodB] != 9450 {
		t.Fatalf("capitalized A=%d B=%d, want 4050/9450", cap.caps[prodA], cap.caps[prodB])
	}
	var allocSum int64
	for _, a := range got.Allocations {
		allocSum += a.AllocatedCadCents
	}
	if allocSum != 13500 {
		t.Fatalf("Σ allocations = %d, want 13500", allocSum)
	}
	// GL: DR 1200 13500 / CR 2100 13500 (CAD, never USD).
	applyKey := "landedcost.apply:" + id
	if gl.debit(applyKey, acctInventory) != 13500 || gl.credit(applyKey, acctFreightPayable) != 13500 {
		t.Fatalf("apply journal DR1200=%d CR2100=%d, want 13500/13500", gl.debit(applyKey, acctInventory), gl.credit(applyKey, acctFreightPayable))
	}

	// Idempotent re-apply: no additional capitalization, still POSTED.
	if err := svc.Apply(ctx, id); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if cap.caps[prodA] != 4050 || cap.caps[prodB] != 9450 {
		t.Fatalf("re-apply double-capitalized: A=%d B=%d", cap.caps[prodA], cap.caps[prodB])
	}

	// Void: the inventory bump nets to zero per product.
	if err := svc.Void(ctx, id); err != nil {
		t.Fatalf("void: %v", err)
	}
	got, _ = svc.Get(ctx, id)
	if got.Status != StatusVoided {
		t.Fatalf("status = %s, want VOIDED", got.Status)
	}
	if cap.caps[prodA] != 0 || cap.caps[prodB] != 0 {
		t.Fatalf("void did not net to zero: A=%d B=%d", cap.caps[prodA], cap.caps[prodB])
	}
	voidKey := "landedcost.void:" + id
	if gl.debit(voidKey, acctFreightPayable) != 13500 || gl.credit(voidKey, acctInventory) != 13500 {
		t.Fatalf("void journal DR2100=%d CR1200=%d, want 13500/13500", gl.debit(voidKey, acctFreightPayable), gl.credit(voidKey, acctInventory))
	}
	// Idempotent void.
	if err := svc.Void(ctx, id); err != nil {
		t.Fatalf("re-void: %v", err)
	}
	if cap.caps[prodA] != 0 || cap.caps[prodB] != 0 {
		t.Fatalf("re-void changed the net: A=%d B=%d", cap.caps[prodA], cap.caps[prodB])
	}
}

// S3 W6 B1/B2: a share whose product has no on-hand to capitalize into must be EXPENSED to COGS, never
// stranded as a phantom 1200 debit. The GL inventory debit tracks what the subledger actually absorbed; the
// remainder flows to COGS so DR1200 + DR5000 == CR2100 and GL 1200 stays tied to the perpetual value.
func TestIntegration_Apply_NoOnHandExpensesToCOGS(t *testing.T) {
	pool := intPool(t)
	seedPO(t, pool)
	ctx := context.Background()
	cap := &fakeCapitalizer{
		lines: []ReceivedLine{
			{ProductID: prodA, QtyBase: 4, ValueCents: 30000, WeightKg: 2},
			{ProductID: prodB, QtyBase: 6, ValueCents: 70000, WeightKg: 3},
		},
		onHandZero: map[string]bool{prodB: true}, // prodB already sold / never stocked → absorbs nothing
	}
	gl := &fakeGLPoster{}
	svc := newSvc(pool, cap, gl)

	id, err := svc.Create(ctx, CreateInput{
		POID: landedPO, BranchID: trentonBranch, Currency: "cad", FXRate: 1,
		FreightCents: 13500, AllocationBasis: "value",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Apply(ctx, id); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// value basis over 30000/70000 → A=4050 capitalized (on-hand), B=9450 expensed (no on-hand).
	if cap.caps[prodA] != 4050 {
		t.Fatalf("A capitalized=%d, want 4050", cap.caps[prodA])
	}
	if cap.caps[prodB] != 0 {
		t.Fatalf("B must NOT capitalize with no on-hand, got %d", cap.caps[prodB])
	}
	applyKey := "landedcost.apply:" + id
	if d := gl.debit(applyKey, acctInventory); d != 4050 {
		t.Fatalf("DR1200=%d, want 4050 (on-hand only)", d)
	}
	if d := gl.debit(applyKey, acctCOGS); d != 9450 {
		t.Fatalf("DR5000=%d, want 9450 (no-on-hand share to COGS)", d)
	}
	if c := gl.credit(applyKey, acctFreightPayable); c != 13500 {
		t.Fatalf("CR2100=%d, want 13500 (balanced)", c)
	}
	// The stored split records the sold-through portion so a later Void reverses both legs symmetrically.
	got, _ := svc.Get(ctx, id)
	var cap1200, cogs int64
	for _, a := range got.Allocations {
		cap1200 += a.AllocatedCadCents
		cogs += a.SoldThroughCadCents
	}
	if cap1200 != 4050 || cogs != 9450 {
		t.Fatalf("stored split cap=%d cogs=%d, want 4050/9450", cap1200, cogs)
	}
}

// AC4: late-freight retro-adjustment splits each share into the on-hand portion (capitalized) and the
// sold-through portion (expensed to COGS).
func TestIntegration_RetroCapitalizeSoldThroughSplit(t *testing.T) {
	pool := intPool(t)
	seedPO(t, pool)
	ctx := context.Background()
	cap := &fakeCapitalizer{lines: []ReceivedLine{
		{ProductID: prodA, QtyBase: 4, ValueCents: 30000},
		{ProductID: prodB, QtyBase: 6, ValueCents: 70000},
	}}
	gl := &fakeGLPoster{}
	svc := newSvc(pool, cap, gl)

	// The original landed cost the late freight retro-adjusts (satisfies the retro_of_id FK).
	origID, err := svc.Create(ctx, CreateInput{POID: landedPO, BranchID: trentonBranch, Currency: "CAD", FreightCents: 5000, AllocationBasis: "value"})
	if err != nil {
		t.Fatalf("create original: %v", err)
	}
	if err := svc.Apply(ctx, origID); err != nil {
		t.Fatalf("apply original: %v", err)
	}

	// Retro doc: 1000 CAD-cents late freight. prodA 2 of 4 still on hand (half sold), prodB all 6 on hand.
	id, err := svc.Create(ctx, CreateInput{
		POID: landedPO, BranchID: trentonBranch, Currency: "CAD", FreightCents: 1000, AllocationBasis: "value",
		RetroOfID: origID,
		OnHand:    []OnHandLine{{ProductID: prodA, OnHandBase: 2}, {ProductID: prodB, OnHandBase: 6}},
	})
	if err != nil {
		t.Fatalf("create retro: %v", err)
	}
	cap.caps = nil // isolate the retro capitalization from the original apply
	if err := svc.RetroAdjust(ctx, id); err != nil {
		t.Fatalf("retro: %v", err)
	}
	got, _ := svc.Get(ctx, id)
	if got.Status != StatusRetroAdjusted {
		t.Fatalf("status = %s, want RETRO_ADJUSTED", got.Status)
	}
	// Allocation: prodA 300 (half sold → 150 cap / 150 sold), prodB 700 (all on hand → 700 cap / 0 sold).
	if cap.caps[prodA] != 150 || cap.caps[prodB] != 700 {
		t.Fatalf("capitalized on-hand A=%d B=%d, want 150/700", cap.caps[prodA], cap.caps[prodB])
	}
	var onHand, sold int64
	for _, a := range got.Allocations {
		onHand += a.AllocatedCadCents
		sold += a.SoldThroughCadCents
	}
	if onHand != 850 || sold != 150 {
		t.Fatalf("split totals on-hand=%d sold=%d, want 850/150", onHand, sold)
	}
	// GL: DR 1200 850 (capitalize) + DR 5000 150 (COGS) / CR 2100 1000.
	key := "landedcost.retro:" + id
	if gl.debit(key, acctInventory) != 850 || gl.debit(key, acctCOGS) != 150 || gl.credit(key, acctFreightPayable) != 1000 {
		t.Fatalf("retro journal DR1200=%d DR5000=%d CR2100=%d, want 850/150/1000",
			gl.debit(key, acctInventory), gl.debit(key, acctCOGS), gl.credit(key, acctFreightPayable))
	}
}

// The apply flow persists allocation rows tied to the landed cost; Preview computes a live per-basis split.
func TestIntegration_PreviewPerBasis(t *testing.T) {
	pool := intPool(t)
	seedPO(t, pool)
	ctx := context.Background()
	cap := &fakeCapitalizer{lines: []ReceivedLine{
		{ProductID: prodA, QtyBase: 1, ValueCents: 300, WeightKg: 4},
		{ProductID: prodB, QtyBase: 3, ValueCents: 700, WeightKg: 1},
	}}
	svc := newSvc(pool, cap, &fakeGLPoster{})
	id, err := svc.Create(ctx, CreateInput{POID: landedPO, BranchID: trentonBranch, FreightCents: 1000, AllocationBasis: "value"})
	if err != nil {
		t.Fatal(err)
	}
	// Flip the basis at preview time without persisting.
	weight, err := svc.Preview(ctx, id, "WEIGHT")
	if err != nil {
		t.Fatal(err)
	}
	if weight.Basis != BasisWeight || len(weight.Lines) != 2 {
		t.Fatalf("preview basis=%s lines=%d", weight.Basis, len(weight.Lines))
	}
	var sum int64
	for _, l := range weight.Lines {
		sum += l.AllocatedCadCents
	}
	if sum != 1000 {
		t.Fatalf("weight-basis preview Σ = %d, want 1000", sum)
	}
	// prodA has 4/5 of the weight → 800, prodB → 200.
	if weight.Lines[0].AllocatedCadCents != 800 || weight.Lines[1].AllocatedCadCents != 200 {
		t.Fatalf("weight preview = %d/%d, want 800/200", weight.Lines[0].AllocatedCadCents, weight.Lines[1].AllocatedCadCents)
	}
}

// wacCapitalizer is a stateful stand-in for the REAL inventory cost seam: it holds a per-product on-hand +
// weighted-average cost and capitalizes exactly as inventory.capitalizeLanded does, re-derive the integer
// per-unit WAC, clamp its realized value delta into the allocation interval, and FLOOR the WAC at 0, returning
// the value the subledger actually absorbed. A sale between Apply and Void shrinks on-hand, so the reversal can
// only pull value out of the units still on hand. This lets the DB-backed Void path prove the L8 money fixes.
type wacCapitalizer struct {
	lines  []ReceivedLine
	onHand map[string]float64
	wac    map[string]int64
}

func (c *wacCapitalizer) ReceivedLinesForPO(context.Context, string) ([]ReceivedLine, error) {
	return c.lines, nil
}

func itRound(f float64) int64 {
	if f < 0 {
		return int64(f - 0.5)
	}
	return int64(f + 0.5)
}

func (c *wacCapitalizer) CapitalizeLandedCostTx(_ context.Context, _ pgx.Tx, product, _ string, alloc int64, _ time.Time, _ string) (int64, error) {
	if alloc == 0 {
		return 0, nil
	}
	oh := c.onHand[product]
	if oh <= 0 {
		return 0, nil
	}
	oldAvg := c.wac[product]
	oldValue := itRound(float64(oldAvg) * oh)
	newAvg := itRound(float64(oldValue+alloc) / oh)
	rd := func(a int64) int64 { return itRound(float64(a)*oh) - oldValue }
	if alloc >= 0 {
		for rd(newAvg) > alloc && newAvg > oldAvg {
			newAvg--
		}
		for rd(newAvg) < 0 && newAvg < oldAvg {
			newAvg++
		}
	} else {
		for rd(newAvg) < alloc && newAvg < oldAvg {
			newAvg++
		}
		for rd(newAvg) > 0 && newAvg > oldAvg {
			newAvg--
		}
	}
	if newAvg < 0 {
		newAvg = 0
	}
	absorbed := rd(newAvg)
	c.wac[product] = newAvg
	return absorbed, nil
}

// CapitalizeLandedCostSnapTx mirrors CapitalizeLandedCostTx but also returns the exact pre-Apply average (snapAvg)
// it capitalized over and whether a WAC row existed, the snapshot the service persists for a cent-exact Void.
func (c *wacCapitalizer) CapitalizeLandedCostSnapTx(ctx context.Context, tx pgx.Tx, product, branch string, alloc int64, at time.Time, sourceRefID string) (int64, int64, bool, error) {
	oldAvg, hadCost := c.wac[product]
	absorbed, err := c.CapitalizeLandedCostTx(ctx, tx, product, branch, alloc, at, sourceRefID)
	return absorbed, oldAvg, hadCost, err
}

// RestoreLandedCostTx mirrors the real inventory seam: it removes EXACTLY `absorbed` (never a delta over current
// on-hand) and returns the realized subledger delta (negative on a void), the general cent-exact inverse the service's
// Void path books as CR 1200. On-hand unchanged since Apply → restore snapAvg verbatim (drift-0); otherwise drain the
// exact capitalized value, clamping the post-reversal value and WAC at zero so no negative leg is ever produced.
func (c *wacCapitalizer) RestoreLandedCostTx(_ context.Context, _ pgx.Tx, product, _ string, snapAvg, absorbed int64, _ time.Time, _ string) (int64, error) {
	if snapAvg < 0 {
		snapAvg = 0
	}
	oh := c.onHand[product]
	cur := c.wac[product]
	if oh <= 0 {
		c.wac[product] = snapAvg
		return 0, nil
	}
	curValue := itRound(float64(cur) * oh)
	if curValue-absorbed == itRound(float64(snapAvg)*oh) {
		c.wac[product] = snapAvg
		return itRound(float64(snapAvg)*oh) - curValue, nil
	}
	newValue := curValue - absorbed
	if newValue < 0 {
		newValue = 0
	}
	newAvg := itRound(float64(newValue) / oh)
	if newAvg < 0 {
		newAvg = 0
	}
	c.wac[product] = newAvg
	return itRound(float64(newAvg)*oh) - curValue, nil
}

// TestIntegration_VoidAfterPartialSale_TiesGL1200AndWACNonNegative is the L8 BLOCKING + MAJOR regression on the
// DB-backed Void path. A landed cost is applied over a large on-hand, THEN most stock sells (on-hand shrinks),
// THEN the doc is voided. The reversing capitalize can only pull value out of the units STILL on hand, so it
// reverses LESS than the capTotal stored at apply. This asserts: (1) the upserted WAC never goes negative
// (BLOCKING), and (2) the void journal books CR 1200 == the ACTUAL subledger value reversed (not the stale
// capTotal), the difference rolls to CR 5000, and the journal balances CR(1200)+CR(5000)==DR(2100) (MAJOR).
// Against the pre-fix code the void CR 1200 was the stale capTotal (larger than what inventory actually gave
// back), ledger diverges from subledger, and the WAC could be driven negative.
func TestIntegration_VoidAfterPartialSale_TiesGL1200AndWACNonNegative(t *testing.T) {
	pool := intPool(t)
	seedPO(t, pool)
	ctx := context.Background()

	// One product, 200 on hand @ WAC 130, freight 2700 CAD applied over all 200 units.
	cap := &wacCapitalizer{
		lines:  []ReceivedLine{{ProductID: prodA, QtyBase: 200, ValueCents: 100000, WeightKg: 1}},
		onHand: map[string]float64{prodA: 200},
		wac:    map[string]int64{prodA: 130},
	}
	gl := &fakeGLPoster{}
	svc := newSvc(pool, cap, gl)

	id, err := svc.Create(ctx, CreateInput{POID: landedPO, BranchID: trentonBranch, Currency: "CAD", FreightCents: 2700, AllocationBasis: "value"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Apply(ctx, id); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, _ := svc.Get(ctx, id)
	var capTotal int64
	for _, a := range got.Allocations {
		capTotal += a.AllocatedCadCents
	}

	// A sale drains on-hand from 200 to 1 between Apply and Void.
	cap.onHand[prodA] = 1
	wacBeforeVoid := cap.wac[prodA]

	if err := svc.Void(ctx, id); err != nil {
		t.Fatalf("void: %v", err)
	}
	if s := (func() string { g, _ := svc.Get(ctx, id); return g.Status })(); s != StatusVoided {
		t.Fatalf("status = %s, want VOIDED", s)
	}

	// (1) BLOCKING: the WAC upserted by the reversal is never negative.
	if cap.wac[prodA] < 0 {
		t.Errorf("void-after-sale drove WAC negative: %d", cap.wac[prodA])
	}

	// The actual value the subledger reversed at void time.
	actualReversed := itRound(float64(cap.wac[prodA])*1) - itRound(float64(wacBeforeVoid)*1)
	subledgerDrop := -actualReversed // magnitude reversed out of inventory

	voidKey := "landedcost.void:" + id
	crInv := gl.credit(voidKey, acctInventory)
	crCogs := gl.credit(voidKey, acctCOGS)
	drPay := gl.debit(voidKey, acctFreightPayable)

	// (2) MAJOR: CR 1200 == the ACTUAL subledger delta (not the stale capTotal, which is far larger here).
	if crInv != subledgerDrop {
		t.Errorf("void CR1200=%d != actual subledger drop=%d (booked from stale capTotal=%d?)", crInv, subledgerDrop, capTotal)
	}
	// The reversing journal balances: CR(1200)+CR(5000) == DR(2100) == the full payable reversed.
	if crInv+crCogs != drPay {
		t.Errorf("void journal unbalanced: CR1200(%d)+CR5000(%d)=%d != DR2100(%d)", crInv, crCogs, crInv+crCogs, drPay)
	}
	// And since almost nothing could be un-capitalized, CR 1200 must be far below capTotal (the divergence the
	// stale-capTotal void would have hidden).
	if crInv >= capTotal {
		t.Errorf("expected CR1200 (%d) << capTotal (%d) after selling down to 1 unit", crInv, capTotal)
	}
}

// TestIntegration_ApplyVoidSnapshotRestoresWAC_SubUnit is the L8 MAJOR regression through the REAL service +
// repository: on a sub-unit on-hand where round(oldAvg × on-hand) quantizes oldAvg away, Apply must persist the
// exact pre-Apply average on the allocation row (0058 pre_apply_avg_unit_cost) and Void must restore it verbatim,
// so the WAC round-trips to the cent (drift 0). Against the legacy reverse-via-negate Void this shape drifted
// (WAC 1 → 10800 → 0). This asserts the persisted snapshot is present and the WAC returns exactly to its pre-Apply
// value. Uses wacCapitalizer (mirrors the real inventory seam) so the snapshot round-trips through the DB column.
func TestIntegration_ApplyVoidSnapshotRestoresWAC_SubUnit(t *testing.T) {
	pool := intPool(t)
	seedPO(t, pool)
	ctx := context.Background()

	// One product, 0.25 units on hand @ WAC 1, the spec's drifting shape. Freight 2700 CAD.
	cap := &wacCapitalizer{
		lines:  []ReceivedLine{{ProductID: prodA, QtyBase: 1, ValueCents: 100000, WeightKg: 1}},
		onHand: map[string]float64{prodA: 0.25},
		wac:    map[string]int64{prodA: 1},
	}
	gl := &fakeGLPoster{}
	svc := newSvc(pool, cap, gl)

	id, err := svc.Create(ctx, CreateInput{POID: landedPO, BranchID: trentonBranch, Currency: "CAD", FreightCents: 2700, AllocationBasis: "value"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Apply(ctx, id); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// The persisted allocation carries the exact pre-Apply snapshot (1), written to pre_apply_avg_unit_cost.
	got, _ := svc.Get(ctx, id)
	var snapped bool
	for _, a := range got.Allocations {
		if a.ProductID == prodA {
			if !a.HasPreApplyAvg || a.PreApplyAvgUnitCost != 1 {
				t.Fatalf("allocation snapshot = %d (has=%v), want 1/true", a.PreApplyAvgUnitCost, a.HasPreApplyAvg)
			}
			snapped = true
		}
	}
	if !snapped {
		t.Fatalf("no allocation row for %s", prodA)
	}
	// Read the column straight from the DB to prove it persisted (not just an in-memory projection).
	var dbSnap *int64
	if err := pool.QueryRow(ctx,
		`SELECT pre_apply_avg_unit_cost FROM landed_cost_allocations WHERE landed_cost_id=$1 AND product_id=$2`,
		id, prodA).Scan(&dbSnap); err != nil {
		t.Fatalf("read snapshot column: %v", err)
	}
	if dbSnap == nil || *dbSnap != 1 {
		t.Fatalf("DB pre_apply_avg_unit_cost = %v, want 1", dbSnap)
	}

	// Void with NO sale between → restore the snapshot exactly. The WAC returns to 1 (drift 0), where the legacy
	// negate path left it at 0.
	if err := svc.Void(ctx, id); err != nil {
		t.Fatalf("void: %v", err)
	}
	if cap.wac[prodA] != 1 {
		t.Errorf("WAC after apply+void = %d, want 1 (round-trip drift %d)", cap.wac[prodA], cap.wac[prodA]-1)
	}
}

// TestIntegration_VoidAfterReceipt_NetsToZeroNoNegativeLeg is the L8 MAJOR 3rd-void-edge regression through the REAL
// service + repository + GL journal: on-hand GROWS between Apply and Void (an intervening RECEIPT). This is the exact
// case the old restore mishandled, it computed CR 1200 as round(snapAvg×onHand) − round(curAvg×onHand) over the
// ENLARGED on-hand, folding the receipt's own value into the reversal and over-reversing by up to N× the capitalized
// amount → CR 1200 > capTotal → cogsCredit = soldTotal + (capTotal − reversedTotal) goes NEGATIVE → the void journal
// carries a negative CR 5000 leg → gl_journal_lines_check rejects it → the doc is PERMANENTLY UN-VOIDABLE.
//
// The fix removes EXACTLY the capitalized value. This asserts: (1) the void succeeds and the doc flips VOIDED; (2) NO
// journal leg is negative; (3) CR 1200 == the actual subledger drop and equals the capitalized total (nothing sold,
// so the full capitalization is reversible), NOT the over-reversed enlarged-on-hand delta; (4) the journal balances
// CR(1200)+CR(5000)==DR(2100); (5) the post-void WAC is sensible and non-negative (the receipt value is preserved).
// Against the pre-fix restore this FAILS (over-reversed CR 1200 → negative COGS leg → the void errors out).
func TestIntegration_VoidAfterReceipt_NetsToZeroNoNegativeLeg(t *testing.T) {
	pool := intPool(t)
	seedPO(t, pool)
	ctx := context.Background()

	// Canonical repro: 10 on hand @ WAC 500 (a real prior cost so snap≠0), freight 5000 CAD over all 10 units.
	cap := &wacCapitalizer{
		lines:  []ReceivedLine{{ProductID: prodA, QtyBase: 10, ValueCents: 100000, WeightKg: 1}},
		onHand: map[string]float64{prodA: 10},
		wac:    map[string]int64{prodA: 500},
	}
	gl := &fakeGLPoster{}
	svc := newSvc(pool, cap, gl)

	id, err := svc.Create(ctx, CreateInput{POID: landedPO, BranchID: trentonBranch, Currency: "CAD", FreightCents: 5000, AllocationBasis: "value"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Apply(ctx, id); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, _ := svc.Get(ctx, id)
	var capTotal int64
	for _, a := range got.Allocations {
		capTotal += a.AllocatedCadCents
	}
	wacAfterApply := cap.wac[prodA]

	// Intervening RECEIPT: 90 more units land at the post-Apply avg → on-hand 10 → 100 (value grows, WAC unchanged).
	cap.onHand[prodA] = 100

	if err := svc.Void(ctx, id); err != nil {
		t.Fatalf("void errored (pre-fix over-reversal → negative COGS leg → unpostable): %v", err)
	}
	if s := (func() string { g, _ := svc.Get(ctx, id); return g.Status })(); s != StatusVoided {
		t.Fatalf("status = %s, want VOIDED", s)
	}

	voidKey := "landedcost.void:" + id
	crInv := gl.credit(voidKey, acctInventory)
	crCogs := gl.credit(voidKey, acctCOGS)
	drPay := gl.debit(voidKey, acctFreightPayable)

	// (2) NO negative leg, the exact failure the old over-reversal produced.
	if crInv < 0 || crCogs < 0 || drPay < 0 {
		t.Errorf("negative leg in void journal: CR1200=%d CR5000=%d DR2100=%d", crInv, crCogs, drPay)
	}
	// (3) CR 1200 == the capitalized total (nothing sold → fully reversible), NOT the over-reversed enlarged delta.
	if crInv != capTotal {
		t.Errorf("void CR1200=%d != capTotal=%d (over-reversal folded receipt value into the reversal)", crInv, capTotal)
	}
	// (4) The journal balances.
	if crInv+crCogs != drPay {
		t.Errorf("void journal unbalanced: CR1200(%d)+CR5000(%d)=%d != DR2100(%d)", crInv, crCogs, crInv+crCogs, drPay)
	}
	// (5) Post-void WAC sensible + non-negative: value 100×wacAfterApply − capTotal, spread over 100 units.
	if cap.wac[prodA] < 0 {
		t.Errorf("post-void WAC negative: %d", cap.wac[prodA])
	}
	wantWAC := itRound(float64(itRound(float64(wacAfterApply)*100)-capTotal) / 100)
	if cap.wac[prodA] != wantWAC {
		t.Errorf("post-void WAC=%d, want %d (exact-absorbed removal, receipt value preserved)", cap.wac[prodA], wantWAC)
	}
}

// - crash-durability regression (L8 REGRESSION: void must be money-atomic) -

// failOnceGL wraps the deduped fake GL poster and makes the FIRST post carrying failKey return a transient error
// (the realistic mid-flight DB/timeout on gl.PostExternal). Every subsequent post, including the retry of the
// same idempotency key, goes through the real fake, which records + dedupes it. This is the failOnceGL seam the
// verify re-checks: it fails the void's GL post exactly once so the test can prove the void is crash-atomic.
type failOnceGL struct {
	inner   *fakeGLPoster
	failKey string
	failed  bool
}

var errTransientGL = errors.New("gl: transient post failure (injected once)")

func (f *failOnceGL) PostJournal(ctx context.Context, in GLJournal) (string, error) {
	if !f.failed && in.IdempotencyKey == f.failKey {
		f.failed = true
		return "", errTransientGL // the post never lands: no gl_journals row, and the caller's tx must roll back
	}
	return f.inner.PostJournal(ctx, in)
}

// PostJournalTx carries the same fail-once semantics through the tx-aware seam variant the fixed Void posts
// the reversing journal with.
func (f *failOnceGL) PostJournalTx(ctx context.Context, tx pgx.Tx, in GLJournal) (string, error) {
	if !f.failed && in.IdempotencyKey == f.failKey {
		f.failed = true
		return "", errTransientGL // the post never lands: no gl_journals row, and the caller's tx must roll back
	}
	return f.inner.PostJournalTx(ctx, tx, in)
}

// txDurableWAC is a subledger stand-in whose weighted-average cost lives IN THE DATABASE, written through the
// SAME pgx.Tx the void runs in (a scratch _lc_test_wac row). This is what makes the crash-durability assertion
// faithful: when the void's tx rolls back after the injected GL failure, the reversal write to the WAC row rolls
// back WITH it, exactly as the real inventory.capitalizeLanded (which upserts branch_costs through the tx) would.
// An in-memory fake would leave its WAC mutated after a rollback and hide the very stranding we are testing for.
// The capitalize math mirrors inventory.capitalizeLanded / wacCapitalizer: re-derive the integer per-unit WAC,
// clamp the realized delta into the allocation interval, floor the WAC at 0, and return the value the subledger
// actually absorbed.
type txDurableWAC struct {
	lines  []ReceivedLine
	onHand map[string]float64
}

func (c *txDurableWAC) ReceivedLinesForPO(context.Context, string) ([]ReceivedLine, error) {
	return c.lines, nil
}

// ensureWACTable creates the scratch WAC table + seeds one product's opening average, all outside any test tx.
func (c *txDurableWAC) ensureWACTable(t *testing.T, pool *pgxpool.Pool, product string, openingAvg int64) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS _lc_test_wac (product text PRIMARY KEY, avg_cents bigint NOT NULL)`); err != nil {
		t.Fatalf("create scratch wac table: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO _lc_test_wac (product, avg_cents) VALUES ($1,$2)
		 ON CONFLICT (product) DO UPDATE SET avg_cents = EXCLUDED.avg_cents`, product, openingAvg); err != nil {
		t.Fatalf("seed scratch wac: %v", err)
	}
}

// wacNow reads the committed WAC (autocommit, no test tx), used to prove a rollback left it untouched.
func (c *txDurableWAC) wacNow(t *testing.T, pool *pgxpool.Pool, product string) int64 {
	t.Helper()
	var avg int64
	if err := pool.QueryRow(context.Background(), `SELECT avg_cents FROM _lc_test_wac WHERE product = $1`, product).Scan(&avg); err != nil {
		t.Fatalf("read scratch wac: %v", err)
	}
	return avg
}

func (c *txDurableWAC) CapitalizeLandedCostTx(ctx context.Context, tx pgx.Tx, product, _ string, alloc int64, _ time.Time, _ string) (int64, error) {
	if alloc == 0 {
		return 0, nil
	}
	oh := c.onHand[product]
	if oh <= 0 {
		return 0, nil
	}
	var oldAvg int64
	if err := tx.QueryRow(ctx, `SELECT avg_cents FROM _lc_test_wac WHERE product = $1 FOR UPDATE`, product).Scan(&oldAvg); err != nil {
		return 0, err
	}
	oldValue := itRound(float64(oldAvg) * oh)
	newAvg := itRound(float64(oldValue+alloc) / oh)
	rd := func(a int64) int64 { return itRound(float64(a)*oh) - oldValue }
	if alloc >= 0 {
		for rd(newAvg) > alloc && newAvg > oldAvg {
			newAvg--
		}
		for rd(newAvg) < 0 && newAvg < oldAvg {
			newAvg++
		}
	} else {
		for rd(newAvg) < alloc && newAvg < oldAvg {
			newAvg++
		}
		for rd(newAvg) > 0 && newAvg > oldAvg {
			newAvg--
		}
	}
	if newAvg < 0 {
		newAvg = 0
	}
	absorbed := rd(newAvg)
	// Write the new WAC THROUGH the tx, a rollback of the void's tx reverts this exactly as the real seam would.
	if _, err := tx.Exec(ctx, `UPDATE _lc_test_wac SET avg_cents = $2 WHERE product = $1`, product, newAvg); err != nil {
		return 0, err
	}
	return absorbed, nil
}

// CapitalizeLandedCostSnapTx mirrors CapitalizeLandedCostTx and additionally returns the exact pre-Apply average
// (snapAvg) it capitalized over. hadCost is true whenever a WAC row exists (ensureWACTable seeds one).
func (c *txDurableWAC) CapitalizeLandedCostSnapTx(ctx context.Context, tx pgx.Tx, product, branch string, alloc int64, at time.Time, sourceRefID string) (int64, int64, bool, error) {
	oh := c.onHand[product]
	var oldAvg int64
	hadCost := true
	if err := tx.QueryRow(ctx, `SELECT avg_cents FROM _lc_test_wac WHERE product = $1`, product).Scan(&oldAvg); err != nil {
		hadCost = false // no row yet → nothing to snapshot
	}
	if oh <= 0 {
		return 0, oldAvg, hadCost, nil
	}
	absorbed, err := c.CapitalizeLandedCostTx(ctx, tx, product, branch, alloc, at, sourceRefID)
	return absorbed, oldAvg, hadCost, err
}

// RestoreLandedCostTx removes EXACTLY `absorbed` THROUGH the tx (so a rollback reverts it, preserving void crash-
// atomicity) and returns the realized subledger delta (negative on a void). Mirrors the real seam: on-hand unchanged
// since Apply → restore snapAvg verbatim (drift-0); otherwise drain the exact capitalized value with value/WAC floored
// at zero so no negative leg is produced.
func (c *txDurableWAC) RestoreLandedCostTx(ctx context.Context, tx pgx.Tx, product, _ string, snapAvg, absorbed int64, _ time.Time, _ string) (int64, error) {
	if snapAvg < 0 {
		snapAvg = 0
	}
	oh := c.onHand[product]
	var cur int64
	if err := tx.QueryRow(ctx, `SELECT avg_cents FROM _lc_test_wac WHERE product = $1 FOR UPDATE`, product).Scan(&cur); err != nil {
		return 0, err
	}
	newAvg := snapAvg
	delta := int64(0)
	if oh > 0 {
		curValue := itRound(float64(cur) * oh)
		if curValue-absorbed == itRound(float64(snapAvg)*oh) {
			delta = itRound(float64(snapAvg)*oh) - curValue
		} else {
			newValue := curValue - absorbed
			if newValue < 0 {
				newValue = 0
			}
			newAvg = itRound(float64(newValue) / oh)
			if newAvg < 0 {
				newAvg = 0
			}
			delta = itRound(float64(newAvg)*oh) - curValue
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE _lc_test_wac SET avg_cents = $2 WHERE product = $1`, product, newAvg); err != nil {
		return 0, err
	}
	return delta, nil
}

// TestIntegration_VoidGLPostFailure_IsCrashAtomicAndRetryable is the L8 crash-durability REGRESSION test on the
// DB-backed Void path. The void's GL post is failed ONCE (failOnceGL). The fix makes the reversing capitalize
// (subledger reversal) + the reversing GL journal + the status flip MONEY-ATOMIC in one tx: a GL post failure
// rolls the whole thing back, so the doc stays POSTED (voidable) and the subledger reversal is UNDONE, never a
// stranded reversal without its journal. A retry then completes the void with CR 1200 booked exactly once and the
// subledger tied.
//
// Against 18cd902 (capitalize + flip → VOIDED COMMITTED first, THEN post) this FAILS: after the injected post
// failure the doc is already VOIDED and the subledger reversed, but no journal exists; the retry hits the
// alreadyVoided guard and returns nil WITHOUT re-posting → CR 1200 is never booked → permanent GL/subledger
// divergence. After the fix it PASSES.
func TestIntegration_VoidGLPostFailure_IsCrashAtomicAndRetryable(t *testing.T) {
	pool := intPool(t)
	seedPO(t, pool)
	ctx := context.Background()

	// One product, 200 on hand @ WAC 130, freight 2700 CAD applied across all 200 units.
	cap := &txDurableWAC{
		lines:  []ReceivedLine{{ProductID: prodA, QtyBase: 200, ValueCents: 100000, WeightKg: 1}},
		onHand: map[string]float64{prodA: 200},
	}
	cap.ensureWACTable(t, pool, prodA, 130)
	inner := &fakeGLPoster{}
	gl := &failOnceGL{inner: inner}
	// Wire the service directly: newSvc takes the concrete *fakeGLPoster, but this test needs the failOnceGL
	// wrapper on the GLPoster seam.
	svc := NewService(NewRepository(pool)).WithCapitalizer(cap).WithGLPoster(gl).withCaller(globalCaller)

	id, err := svc.Create(ctx, CreateInput{POID: landedPO, BranchID: trentonBranch, Currency: "CAD", FreightCents: 2700, AllocationBasis: "value"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Apply(ctx, id); err != nil {
		t.Fatalf("apply: %v", err)
	}
	gl.failKey = "landedcost.void:" + id // fail ONLY the upcoming void post (apply already posted cleanly)

	// A partial sale drains on-hand 200 → 1 between Apply and Void, so the reversal only pulls value out of the
	// one unit still on hand (reversedTotal is small, the L8-corrected amount, not the stale capTotal).
	cap.onHand[prodA] = 1
	wacBeforeVoid := cap.wacNow(t, pool, prodA)

	// First void: the GL post fails once. The whole void tx must roll back → the error propagates.
	if err := svc.Void(ctx, id); err == nil {
		t.Fatalf("void: expected the injected GL post failure to propagate, got nil")
	} else if !errors.Is(err, errTransientGL) {
		t.Fatalf("void: expected errTransientGL, got %v", err)
	}

	// No stranding (this is the regression): the doc is STILL POSTED (not VOIDED) and the subledger reversal was
	// rolled back with the tx (WAC unchanged). Against 18cd902 the doc is already VOIDED here with the WAC reversed
	// but no journal, the stranded state the retry can no longer heal.
	got, _ := svc.Get(ctx, id)
	if got.Status != StatusPosted {
		t.Fatalf("after failed void: status=%s, want POSTED (void must roll back, not strand a VOIDED doc)", got.Status)
	}
	if w := cap.wacNow(t, pool, prodA); w != wacBeforeVoid {
		t.Fatalf("after failed void: subledger WAC=%d, want unchanged %d (reversal must roll back, not strand)", w, wacBeforeVoid)
	}
	voidKey := "landedcost.void:" + id
	if _, posted := inner.journals[voidKey]; posted {
		t.Fatalf("after failed void: a void journal was recorded despite the post failing, not atomic")
	}

	// Retry: the seam now lets the post through. The void completes exactly once.
	if err := svc.Void(ctx, id); err != nil {
		t.Fatalf("void retry: %v", err)
	}
	got, _ = svc.Get(ctx, id)
	if got.Status != StatusVoided {
		t.Fatalf("after retry: status=%s, want VOIDED", got.Status)
	}

	// The value the subledger ACTUALLY reversed at void time (WAC floored to 0 over the 1 unit still on hand).
	wacAfterVoid := cap.wacNow(t, pool, prodA)
	subledgerDrop := itRound(float64(wacBeforeVoid)*1) - itRound(float64(wacAfterVoid)*1)

	crInv := inner.credit(voidKey, acctInventory)
	crCogs := inner.credit(voidKey, acctCOGS)
	drPay := inner.debit(voidKey, acctFreightPayable)

	// CR 1200 == the ACTUAL subledger delta the void reversed (the L8-corrected amount), booked exactly once.
	if crInv != subledgerDrop {
		t.Errorf("after retry: void CR1200=%d != actual subledger drop=%d", crInv, subledgerDrop)
	}
	// The journal balances and ties: CR(1200)+CR(5000) == DR(2100) == the full payable reversed.
	if crInv+crCogs != drPay {
		t.Errorf("after retry: void journal unbalanced: CR1200(%d)+CR5000(%d)=%d != DR2100(%d)", crInv, crCogs, crInv+crCogs, drPay)
	}

	// Booked EXACTLY ONCE: a third void call (already VOIDED) must not re-post or re-reverse.
	wacTiedAt := cap.wacNow(t, pool, prodA)
	if err := svc.Void(ctx, id); err != nil {
		t.Fatalf("idempotent re-void: %v", err)
	}
	if w := cap.wacNow(t, pool, prodA); w != wacTiedAt {
		t.Errorf("re-void moved the subledger: WAC=%d, want unchanged %d (double reversal)", w, wacTiedAt)
	}
	if c := inner.credit(voidKey, acctInventory); c != crInv {
		t.Errorf("re-void changed CR1200: %d, want unchanged %d (double journal)", c, crInv)
	}
}
