//go:build integration

// Integration tests run the real pgx Repository + Service against the worker's PRIVATE DB (hos_w6_tax), against
// the 0029 + 0042 schema. Run with:
//
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5435/hos_w6_tax?sslmode=disable' \
//	  go test -tags=integration ./internal/tax/... -count=1 -p 1
//
// Covers M5: AssessSale over the seeded effective-dated tax_rates + a hot rate change (effective-dating);
// masked-only evidence persistence (no raw column exists) + A-21 audit; the tax_config HST registration number;
// the W4-snapshot recompute (insert-only + idempotent + parity) against a real tax_decision_snapshot; the
// insert-only trigger; and the HST → GL reconciliation collected-side tie-out.
package tax

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/jackc/pgx/v5/pgxpool"
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hos_w6_tax?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("tax integration: connect: %v", err)
		return nil
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("tax integration: ping: %v", err)
		return nil
	}
	return pool
}

func intSvc(t *testing.T, pool *pgxpool.Pool) *Service {
	return &Service{
		repo: NewRepository(pool),
		log:  slog.Default(),
		now:  func() time.Time { return time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC) },
	}
}

func seededOrderID(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	// DIB-419: ORDER BY id makes the anchor DETERMINISTIC. A bare `LIMIT 1` returns
	// whatever the heap hands back first, which changes the moment any package on the
	// shared DB updates an `orders` row (dispatch, pos, order, e2e all do). Two calls in
	// the same run could anchor two different orders, and two runs on the same DB could
	// anchor different orders again, so a fixture's own teardown could miss the rows it
	// wrote. The seeded set is stable, so ORDER BY id pins one order for every reader.
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM orders ORDER BY id LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("no seeded order to anchor a snapshot: %v", err)
	}
	return id
}

// dib419IsolatedYear is a recompute window no other package writes into. The recompute
// scans tax_decision_snapshot by created_at across the WHOLE table, so a window anchored
// on "now" sweeps in every W4-flat-13 row internal/pos commits in parallel. Pinning the
// fixture's own row into a far-future year turns the whole-DB assertion into a SCOPED one
// without weakening it. Sibling TestIntegration_Reconcile_CollectedTiesToGL already uses
// 2091 for the same reason; this uses a different year so the two never collide.
const (
	dib419IsolatedYear = "2089"
	dib419WindowFrom   = dib419IsolatedYear + "-01-01"
	dib419WindowTo     = dib419IsolatedYear + "-12-31"
	dib419StampedAt    = dib419IsolatedYear + "-05-01 12:00:00+00"
)

// -- AssessSale over the seeded rate table + effective-dated hot change -------------

func TestIntegration_AssessSale_SeededRatesAndEffectiveDating(t *testing.T) {
	pool := intPool(t)
	if pool == nil {
		return
	}
	defer pool.Close()
	svc := intSvc(t, pool)
	ctx := context.Background()

	// Seeded standard rate is 13%.
	q, err := svc.AssessSale(ctx, SaleAssessment{SubtotalCents: 100_000})
	if err != nil {
		t.Fatal(err)
	}
	if q.TaxCents != 13_000 || q.RateBps != 1300 {
		t.Fatalf("seeded standard: tax=%d rate=%d, want 13000/1300", q.TaxCents, q.RateBps)
	}

	// Vendor-delivery 0% is evidence-gated: without evidence it binds the standard rate (hard gate preserved).
	blocked, _ := svc.AssessSale(ctx, SaleAssessment{SubtotalCents: 100_000, ReasonCode: ReasonFedVendDel})
	if blocked.RateBps != 1300 || !blocked.RequiresEvidence {
		t.Fatalf("hard gate: rate=%d requiresEvidence=%v", blocked.RateBps, blocked.RequiresEvidence)
	}
	withEv, _ := svc.AssessSale(ctx, SaleAssessment{SubtotalCents: 100_000, ReasonCode: ReasonFedVendDel, EvidenceAttached: true})
	if withEv.TaxCents != 0 {
		t.Fatalf("vendor delivery with evidence must be 0%%, got %d", withEv.TaxCents)
	}

	// Effective-dating hot change: a NEW row at 14% effective 2026-08-01 governs sales on/after that date only.
	//
	// DIB-384: this row MUST be torn down. It was written when 2026-08-01 was in the future, so it was
	// inert; once that date passed it became the governing ON-HST rate for every later reader on the same
	// DB, and internal/e2e's W6 scenario started failing with "standard ON-HST rate = 1400 bps, want 1300".
	// A time-bombed fixture: harmless the day it was written, cross-package residue the day the clock
	// caught up with it.
	const hotChangeNotes = "int-test hot change"
	if _, err := svc.CreateRate(ctx, Rate{ReasonCode: ReasonStdON13, RateBps: 1400, EffectiveFrom: "2026-08-01", Notes: hotChangeNotes}); err != nil {
		t.Fatal(err)
	}
	// NOT t.Cleanup: this test does `defer pool.Close()` at the top, and t.Cleanup functions run AFTER the
	// body's defers, the delete would silently no-op against a closed pool. A defer registered here runs
	// LIFO, i.e. before that close, so it actually reaches the database.
	defer func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM tax_rates WHERE reason_code=$1 AND rate_bps=1400 AND notes=$2`,
			string(ReasonStdON13), hotChangeNotes); err != nil {
			t.Errorf("DIB-384: failed to tear down the hot-change rate, it will govern later readers: %v", err)
		}
	}()
	before, _ := svc.AssessSale(ctx, SaleAssessment{SubtotalCents: 100_000, AsOf: "2026-07-31"})
	after, _ := svc.AssessSale(ctx, SaleAssessment{SubtotalCents: 100_000, AsOf: "2026-08-01"})
	if before.RateBps != 1300 {
		t.Errorf("pre-effective date should still be 1300, got %d", before.RateBps)
	}
	if after.RateBps != 1400 || after.TaxCents != 14_000 {
		t.Errorf("post-effective date should be 1400/14000, got %d/%d", after.RateBps, after.TaxCents)
	}
}

// -- masked-only evidence + insert path ---------------------------------------------

func TestIntegration_Evidence_MaskedOnlyPersistence(t *testing.T) {
	pool := intPool(t)
	if pool == nil {
		return
	}
	defer pool.Close()
	svc := intSvc(t, pool)
	ctx := context.Background()

	ev, err := svc.CaptureEvidence(ctx, EvidenceCapture{
		CardProfileCode: "ON_RESERVE_DELIVERY", PhotoRef: "vault://img/int", RawNumber: "600055512340481", Attestation: true, TCRDExpiry: "2027-12-31",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ev.NumberMask != "•••• 0481" {
		t.Fatalf("mask=%q want '•••• 0481'", ev.NumberMask)
	}
	// The tax_evidence table has NO raw-number column at all, prove the schema itself cannot store cleartext.
	var col int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_name='tax_evidence' AND column_name IN ('card_number','raw_number','proof_identifier')`).Scan(&col); err != nil {
		t.Fatal(err)
	}
	if col != 0 {
		t.Fatalf("tax_evidence has a raw-number column (%d), masked-only contract violated", col)
	}
	// Round-trips masked.
	got, ok, err := svc.ViewEvidence(ctx, ev.ID)
	if err != nil || !ok {
		t.Fatalf("view: ok=%v err=%v", ok, err)
	}
	if got.NumberMask != "•••• 0481" {
		t.Errorf("view mask=%q", got.NumberMask)
	}
}

// -- config: HST registration number the invoice carries ----------------------------

func TestIntegration_Config_HSTRegistrationNo(t *testing.T) {
	pool := intPool(t)
	if pool == nil {
		return
	}
	defer pool.Close()
	svc := intSvc(t, pool)
	ctx := context.Background()

	// DIB-419: tax_config's org-default row is a single whole-DB row every package reads.
	// This test used to write it and walk away, leaving a test-authored registration
	// number as the DB's truth for every later reader, the sibling Dormant test only
	// papers over that by restoring it itself. Capture and restore here, at the source.
	orig, err := svc.GetConfig(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, rerr := svc.UpdateConfig(context.Background(), Config{HSTRegistrationNo: orig.HSTRegistrationNo, RoundMode: orig.RoundMode}); rerr != nil {
			t.Errorf("DIB-419: restore tax_config: %v", rerr)
		}
	}()

	if _, err := svc.UpdateConfig(ctx, Config{HSTRegistrationNo: "899988877RT0001", RoundMode: "INVOICE_TOTAL"}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetConfig(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.HSTRegistrationNo != "899988877RT0001" {
		t.Fatalf("hst reg no = %q", got.HSTRegistrationNo)
	}
}

// -- recompute: insert-only + idempotent + parity + the DB trigger ------------------

func TestIntegration_Recompute_InsertOnlyIdempotentParity(t *testing.T) {
	pool := intPool(t)
	if pool == nil {
		return
	}
	defer pool.Close()
	svc := intSvc(t, pool)
	ctx := context.Background()
	orderID := seededOrderID(t, pool)
	// Isolate: clear any snapshots left by a prior run of this test (DELETE is allowed; only UPDATE is blocked).
	if _, err := pool.Exec(ctx, `DELETE FROM tax_decision_snapshot WHERE order_id=$1::uuid`, orderID); err != nil {
		t.Fatal(err)
	}
	// DIB-419: restore what this fixture touched. Without it every run leaves a W4 row +
	// its M5 row behind, and the residue is what later runs then have to reason around.
	//
	// NOT t.Cleanup, same trap the DIB-384 note above documents: this test does
	// `defer pool.Close()` at the top and t.Cleanup runs AFTER the body's defers, so the
	// delete would fire against a closed pool. A defer registered here runs LIFO, i.e.
	// before that close. (Written as t.Cleanup first; it failed with "closed pool".)
	defer func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM tax_decision_snapshot WHERE order_id=$1::uuid`, orderID); err != nil {
			t.Errorf("DIB-419: teardown of the recompute fixture failed: %v", err)
		}
	}()

	// Insert a settled W4 flat-13 snapshot (the audit truth the recompute re-derives),
	// STAMPED into the isolated year so the recompute window below sees this row and
	// nothing else.
	if _, err := pool.Exec(ctx, `
		INSERT INTO tax_decision_snapshot (order_id, tax_reason_code, rule_version, rate_bps, full_hst_cents, net_collected_cents, automated, created_at)
		VALUES ($1::uuid,'STD_ON_HST_13','W4-flat-13',1300,1300,1300,false, TIMESTAMPTZ '`+dib419StampedAt+`')`, orderID); err != nil {
		t.Fatal(err)
	}

	res, err := svc.RecomputeReport(ctx, dib419WindowFrom, dib419WindowTo)
	if err != nil {
		t.Fatal(err)
	}
	// DIB-419: the window contains exactly this fixture's row, so the count is EXACT
	// (`>= 1` was the loose form the whole-DB window forced). Scanned is pinned too, if
	// another package ever starts writing into the isolated year, this says so out loud
	// instead of silently becoming order-dependent again.
	if res.Scanned != 1 || res.Inserted != 1 {
		t.Fatalf("recompute over the isolated %s window: scanned=%d inserted=%d, want 1/1, a foreign W4 row is in the window",
			dib419IsolatedYear, res.Scanned, res.Inserted)
	}
	// The M5 recompute row exists for our order and is byte-parity with the live engine on the reconstructed base.
	exists, err := svc.repo.SnapshotExists(ctx, orderID, "", ruleVersionM5)
	if err != nil || !exists {
		t.Fatalf("M5 recompute row missing for order: exists=%v err=%v", exists, err)
	}
	var net int64
	var rate int
	// DIB-419: deterministic anchor here too, ORDER BY created_at DESC, id DESC pins the
	// row this run produced even if an earlier row for the same order survived teardown.
	if err := pool.QueryRow(ctx, `
		SELECT net_collected_cents, rate_bps FROM tax_decision_snapshot
		WHERE order_id=$1::uuid AND rule_version='M5-recompute'
		ORDER BY created_at DESC, id DESC LIMIT 1`, orderID).Scan(&net, &rate); err != nil {
		t.Fatal(err)
	}
	live, _ := svc.AssessSale(ctx, SaleAssessment{SubtotalCents: 10_000, ReasonCode: ReasonStdON13})
	if net != live.NetCollectedCents || rate != live.RateBps {
		t.Fatalf("parity: recompute net=%d rate=%d vs live net=%d rate=%d", net, rate, live.NetCollectedCents, live.RateBps)
	}

	// Idempotent: a second run over the same window inserts nothing new. DIB-419: the
	// window is the isolated year, so this reads ONLY this fixture's row, a W4 snapshot
	// committed by a parallel internal/pos sale can no longer be counted as "not
	// idempotent". SkippedExists is asserted alongside, so the zero has to come from the
	// row being recognised as already-recomputed rather than from an empty scan.
	res2, err := svc.RecomputeReport(ctx, dib419WindowFrom, dib419WindowTo)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Inserted != 0 || res2.SkippedExists != 1 || res2.Scanned != 1 {
		t.Fatalf("second recompute: scanned=%d inserted=%d skipped=%d, want 1/0/1 (idempotent)",
			res2.Scanned, res2.Inserted, res2.SkippedExists)
	}

	// Insert-only DB trigger: a snapshot can never be UPDATE-d (a reversing snapshot is inserted instead).
	_, uerr := pool.Exec(ctx, `UPDATE tax_decision_snapshot SET net_collected_cents = 0 WHERE order_id=$1::uuid`, orderID)
	if uerr == nil || !strings.Contains(uerr.Error(), "insert-only") {
		t.Fatalf("expected insert-only trigger to block UPDATE, got %v", uerr)
	}
}

// -- HST → GL reconciliation collected-side tie-out ---------------------------------

type stubGL struct{ bal int64 }

func (s stubGL) GLBalanceCents(context.Context, string, int, int, string) (int64, error) {
	return s.bal, nil
}

func TestIntegration_Reconcile_CollectedTiesToGL(t *testing.T) {
	pool := intPool(t)
	if pool == nil {
		return
	}
	defer pool.Close()
	svc := intSvc(t, pool)
	ctx := context.Background()
	orderID := seededOrderID(t, pool)
	// Isolate the 2091 window so a re-run against the same DB does not accumulate.
	if _, err := pool.Exec(ctx, `DELETE FROM tax_decision_snapshot WHERE order_id=$1::uuid AND EXTRACT(YEAR FROM created_at)=2091`, orderID); err != nil {
		t.Fatal(err)
	}
	// DIB-419: and restore it on the way out too (defer, not t.Cleanup, see the note in
	// the recompute test), so the 2091 row is not left as residue for the next run.
	defer func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM tax_decision_snapshot WHERE order_id=$1::uuid AND EXTRACT(YEAR FROM created_at)=2091`, orderID); err != nil {
			t.Errorf("DIB-419: teardown of the 2091 reconcile fixture failed: %v", err)
		}
	}()

	// A collected snapshot in a fresh, isolated period (year 2091) so it never collides with other rows.
	if _, err := pool.Exec(ctx, `
		INSERT INTO tax_decision_snapshot (order_id, tax_reason_code, rule_version, rate_bps, full_hst_cents, net_collected_cents, automated, created_at)
		VALUES ($1::uuid,'STD_ON_HST_13','W4-flat-13',1300,13000,13000,false, TIMESTAMPTZ '2091-03-15 12:00:00+00')`, orderID); err != nil {
		t.Fatal(err)
	}
	collected, err := svc.repo.CollectedHSTCents(ctx, 2091, 3)
	if err != nil {
		t.Fatal(err)
	}
	if collected != 13_000 {
		t.Fatalf("collected for 2091-03 = %d, want 13000", collected)
	}

	// With the GL 2200 balance matching the collected total, the recon ties out.
	svc.glBalance = stubGL{bal: collected}
	rep, err := svc.ReconcileReport(ctx, 2091, 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.TiedOut || rep.VarianceCents != 0 {
		t.Fatalf("expected tie-out, got variance=%d tied=%v", rep.VarianceCents, rep.TiedOut)
	}
}

// -- ON_FN_POS_8: 5% net collected + 8% Ontario POS credit, and the evidence hard-gate on the credit ----------
//
// DIB-41 finance verification: the Ontario First-Nations point-of-sale relief resolves through the REAL
// Repository.ResolveRate against the seeded effective-dated tax_rates table. Ground truth is the seeded rows -
// ON_FN_POS_8=500bps (5% net) and FNGST_5=500bps, the STD_ON_HST_13=1300bps fallback row, and the code
// constant ontarioCredBps (800 = 8% of the $1000.00 base). None of these expectations is read back from the
// engine output: each is derived independently from the seeded rate table and the documented spec constant.
func TestIntegration_AssessSale_ONFNPos8CreditAndGate(t *testing.T) {
	pool := intPool(t)
	if pool == nil {
		return
	}
	defer pool.Close()
	svc := intSvc(t, pool)
	ctx := context.Background()

	// Independently confirm the seeded rows the engine will resolve (not the engine's own output). The clock is
	// pinned to 2026-07-07 by intSvc, so this is the effective row on the sale date the assessments use.
	onNet, onFound, err := svc.repo.ResolveRate(ctx, jurisdictionON, ReasonONFNPos8, "2026-07-07")
	if err != nil {
		t.Fatal(err)
	}
	if !onFound || onNet != 500 {
		t.Fatalf("seeded ON_FN_POS_8 rate = %d found=%v, want 500/true (seed precondition)", onNet, onFound)
	}
	fnNet, fnFound, err := svc.repo.ResolveRate(ctx, jurisdictionON, ReasonFNGST5, "2026-07-07")
	if err != nil {
		t.Fatal(err)
	}
	if !fnFound || fnNet != 500 {
		t.Fatalf("seeded FNGST_5 rate = %d found=%v, want 500/true (seed precondition)", fnNet, fnFound)
	}

	const base = int64(100_000) // $1000.00
	// Expectations derived from the seed + spec constant, NOT from the quote:
	//   net collected = 5% of $1000.00 = $50.00; ON POS credit = 8% (ontarioCredBps) of $1000.00 = $80.00.
	wantNet := hstOn(base, onNet)             // 5000
	wantCredit := hstOn(base, ontarioCredBps) // 8000
	if wantNet != 5_000 || wantCredit != 8_000 {
		t.Fatalf("derivation sanity: net=%d credit=%d, want 5000/8000", wantNet, wantCredit)
	}

	// (1) ON_FN_POS_8 with evidence: relief binds → 5% collected + 8% Ontario credit tracked once.
	withEv, err := svc.AssessSale(ctx, SaleAssessment{SubtotalCents: base, ReasonCode: ReasonONFNPos8, EvidenceAttached: true})
	if err != nil {
		t.Fatal(err)
	}
	if withEv.RateBps != 500 || withEv.TaxCents != wantNet || withEv.NetCollectedCents != wantNet {
		t.Fatalf("ON_FN_POS_8 w/ evidence: rate=%d tax=%d net=%d, want 500/%d/%d", withEv.RateBps, withEv.TaxCents, withEv.NetCollectedCents, wantNet, wantNet)
	}
	if withEv.OnCreditCents != wantCredit {
		t.Fatalf("ON_FN_POS_8 w/ evidence: on_credit=%d, want %d (8%% ontario credit)", withEv.OnCreditCents, wantCredit)
	}
	if !withEv.RequiresEvidence {
		t.Fatalf("ON_FN_POS_8 is evidence-gated; RequiresEvidence must be true even when evidence attached")
	}

	// (2) ON_FN_POS_8 WITHOUT evidence: hard gate → binding rate reverts to the seeded standard 13%, NO credit,
	// but the reason still flags RequiresEvidence (relief pending).
	noEv, err := svc.AssessSale(ctx, SaleAssessment{SubtotalCents: base, ReasonCode: ReasonONFNPos8})
	if err != nil {
		t.Fatal(err)
	}
	if noEv.RateBps != 1300 || noEv.TaxCents != 13_000 {
		t.Fatalf("ON_FN_POS_8 w/o evidence: rate=%d tax=%d, want 1300/13000 (standard fallback)", noEv.RateBps, noEv.TaxCents)
	}
	if noEv.OnCreditCents != 0 {
		t.Fatalf("ON_FN_POS_8 w/o evidence: on_credit=%d, want 0 (ungated credit withheld)", noEv.OnCreditCents)
	}
	if !noEv.RequiresEvidence {
		t.Fatalf("ON_FN_POS_8 w/o evidence: RequiresEvidence must be true")
	}

	// (3) FNGST_5 with evidence: 5% collected, but NO Ontario POS credit (that split is ON_FN_POS_8 only).
	fn, err := svc.AssessSale(ctx, SaleAssessment{SubtotalCents: base, ReasonCode: ReasonFNGST5, EvidenceAttached: true})
	if err != nil {
		t.Fatal(err)
	}
	if fn.RateBps != 500 || fn.TaxCents != wantNet {
		t.Fatalf("FNGST_5 w/ evidence: rate=%d tax=%d, want 500/%d", fn.RateBps, fn.TaxCents, wantNet)
	}
	if fn.OnCreditCents != 0 {
		t.Fatalf("FNGST_5 w/ evidence: on_credit=%d, want 0 (no ontario POS credit on this reason)", fn.OnCreditCents)
	}
}

// -- config: the dormant Avalara/postal toggles are REFUSED and never persist (D-A19-5) -----------------------
//
// DIB-41 title names "Avalara fallback, postal-code routing"; at W6 those are deliberately dormant stubs. The
// only real production behavior is UpdateConfig refusing to enable them. This ties that refusal invariant to the
// real seeded tax_config org-default row (avalara_enabled=f, postal_routing_enabled=f) and proves neither
// refused call mutated the row. The hst_registration_no is captured up-front and restored to avoid cross-test
// drift (a sibling test in this package writes it).
func TestIntegration_Config_DormantAvalaraPostalRefusedAndPersistFalse(t *testing.T) {
	pool := intPool(t)
	if pool == nil {
		return
	}
	defer pool.Close()
	svc := intSvc(t, pool)
	ctx := context.Background()

	// Capture the org-default row so we can restore it verbatim afterward.
	orig, err := svc.GetConfig(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if orig.AvalaraEnabled || orig.PostalRoutingEnabled {
		t.Fatalf("seed precondition: org-default tax_config must have avalara=f/postal=f, got avalara=%v postal=%v", orig.AvalaraEnabled, orig.PostalRoutingEnabled)
	}
	defer func() {
		if _, rerr := svc.UpdateConfig(ctx, Config{HSTRegistrationNo: orig.HSTRegistrationNo, RoundMode: orig.RoundMode}); rerr != nil {
			t.Errorf("restore config: %v", rerr)
		}
	}()

	// Enabling Avalara is refused with a BadRequest (dormant at W6), even with an otherwise-valid round mode.
	if _, err := svc.UpdateConfig(ctx, Config{AvalaraEnabled: true, RoundMode: "INVOICE_TOTAL"}); err == nil {
		t.Fatal("UpdateConfig{AvalaraEnabled:true} must be refused, got nil error")
	} else if !errors.As(err, &httpx.BadRequestError{}) {
		t.Fatalf("UpdateConfig{AvalaraEnabled:true}: want BadRequestError, got %T: %v", err, err)
	}

	// Enabling postal routing is likewise refused.
	if _, err := svc.UpdateConfig(ctx, Config{PostalRoutingEnabled: true, RoundMode: "INVOICE_TOTAL"}); err == nil {
		t.Fatal("UpdateConfig{PostalRoutingEnabled:true} must be refused, got nil error")
	} else if !errors.As(err, &httpx.BadRequestError{}) {
		t.Fatalf("UpdateConfig{PostalRoutingEnabled:true}: want BadRequestError, got %T: %v", err, err)
	}

	// The refused writes never touched the real row: it still reports both toggles false.
	got, err := svc.GetConfig(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.AvalaraEnabled || got.PostalRoutingEnabled {
		t.Fatalf("tax_config drifted after refused writes: avalara=%v postal=%v, want both false", got.AvalaraEnabled, got.PostalRoutingEnabled)
	}
}

// TestIntegration_CreateRateIntervalGuard drives the interval guard through the REAL repository's SQL
// (the round 3 review's P2 and P3 1: the NULL scan faulted the admin edge on every no-overlap shape
// and no committed leg had driven the SQL at all). Four shapes on the seeded world, all
// fixture-scoped (the planted rows go in the leg's cleanup, keyed by their notes marker):
//
//   - a backdated open-ended ungated row beside a planted later raised standard era: refused, naming
//     the raise;
//   - the same row closed the day before the raise: accepted (it never governs beside it);
//   - an ungated row before the seed's standard begins (a no-overlap shape): accepted, no fault (the
//     NULL scan's own case);
//   - a manual row planted HIGH first, then an ungated row above the seeded standard: accepted, which
//     bites the SQL's reason scoping (with the reason predicate dropped, the manual row's rate would
//     refuse it).
func TestIntegration_CreateRateIntervalGuard(t *testing.T) {
	pool := intPool(t)
	svc := intSvc(t, pool)
	ctx := context.Background()

	const marker = "interval guard leg marker"
	cleanup := func() {
		if _, err := pool.Exec(ctx, `DELETE FROM tax_rates WHERE notes = $1`, marker); err != nil {
			t.Errorf("clean the leg's marked rows: %v", err)
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	// A raised standard era thirty days ahead of the fixture clock (2026-07-07).
	if _, err := pool.Exec(ctx, `
		INSERT INTO tax_rates (jurisdiction, reason_code, rate_bps, effective_from, notes)
		VALUES ('ON', 'STD_ON_HST_13', 1500, DATE '2026-08-06', $1)`, marker); err != nil {
		t.Fatalf("plant the raised era: %v", err)
	}

	// The backdated open-ended row governs beside the 1500 era: refused, naming it.
	_, err := svc.CreateRate(ctx, Rate{ReasonCode: "FNT_PRODUCT", RateBps: 1300, EffectiveFrom: "2026-06-07", Notes: marker})
	if err == nil {
		t.Fatal("the backdated open-ended ungated row must be refused against the 1500 era it governs beside")
	}
	var bad httpx.BadRequestError
	if !errors.As(err, &bad) {
		t.Fatalf("the refusal must be the 400-shaped BadRequestError, got %T: %v", err, err)
	}
	if !strings.Contains(bad.Msg, "1500") {
		t.Fatalf("the refusal must name the 1500 era, got %q", bad.Msg)
	}

	// The same row closed the day before the raise never governs beside it: accepted.
	if _, err := svc.CreateRate(ctx, Rate{ReasonCode: "FNT_PRODUCT", RateBps: 1300, EffectiveFrom: "2026-06-07", EffectiveTo: "2026-08-05", Notes: marker}); err != nil {
		t.Fatalf("the row closed before the raise must be accepted: %v", err)
	}

	// A no-overlap shape: an ungated row entirely before the seeded standard begins. The guard must
	// refuse NOTHING here (found=false, the NULL scan's own case), no fault.
	if _, err := svc.CreateRate(ctx, Rate{ReasonCode: "FNT_PRODUCT", RateBps: 0, EffectiveFrom: "1999-01-01", EffectiveTo: "1999-12-31", Notes: marker}); err != nil {
		t.Fatalf("a row overlapping no standard era must pass the guard cleanly (the round 3 NULL scan fault): %v", err)
	}

	// The reason scoping: a manual row planted high (the manual freedom), then an ungated row ABOVE
	// the seeded standard and closed before the raise (so no standard era it governs beside reads
	// above it). Accepted: the guard measures standard rows only, never the manual row's rate
	// (dropping the SQL's reason predicate would refuse this against the manual 2000).
	if _, err := svc.CreateRate(ctx, Rate{ReasonCode: "MANUAL_TAX_REVIEW", RateBps: 2000, EffectiveFrom: "2026-07-07", Notes: marker}); err != nil {
		t.Fatalf("the manual row's freedom must be accepted: %v", err)
	}
	if _, err := svc.CreateRate(ctx, Rate{ReasonCode: "FNT_PRODUCT", RateBps: 1400, EffectiveFrom: "2026-07-07", EffectiveTo: "2026-08-05", Notes: marker}); err != nil {
		t.Fatalf("an ungated row above every standard era it governs beside must be accepted whatever other reasons' rows read: %v", err)
	}
}
