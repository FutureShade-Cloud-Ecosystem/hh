//go:build integration

// A-08 integration tests: the real pgx batchStore against migrations 0039 + the seeded
// V-* price_import_profiles, exercising the full lifecycle (create → import golden fixture →
// approve with separation of duties → apply on/after effective date → unified retail anchor
// write + append-only change log) plus the DB-level append-only trigger. Run with
// make test-integration against the PRIVATE W6 DB.
package pricing

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	makerID   = "d7000000-0000-4000-9000-000000000002" // Alex Morgan
	checkerID = "d7000000-0000-4000-9000-000000000006" // Jordan Blake
)

// snapshotFixtureAnchors captures the current pricing_list_prices for the fixture SKUs and
// registers a t.Cleanup that restores them EXACTLY, so an A-08 apply that overwrites a seeded
// retail anchor never leaks into sibling integration tests sharing the private DB (test
// isolation; these tests run in one process under -p 1).
func snapshotFixtureAnchors(t *testing.T, ctx context.Context, pool *pgxpool.Pool, skus []string) {
	t.Helper()
	type snap struct {
		productID string
		had       bool
		cents     int64
	}
	var snaps []snap
	for _, sku := range skus {
		var pid string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM products WHERE sku = $1`, sku).Scan(&pid); err != nil {
			continue // unmatched fixture SKU, nothing to restore
		}
		s := snap{productID: pid}
		var cents int64
		if err := pool.QueryRow(ctx, `SELECT list_price_cents FROM pricing_list_prices WHERE product_id = $1`, pid).Scan(&cents); err == nil {
			s.had, s.cents = true, cents
		}
		snaps = append(snaps, s)
	}
	t.Cleanup(func() {
		for _, s := range snaps {
			if s.had {
				pool.Exec(ctx, `UPDATE pricing_list_prices SET list_price_cents = $2 WHERE product_id = $1`, s.productID, s.cents)
			} else {
				pool.Exec(ctx, `DELETE FROM pricing_list_prices WHERE product_id = $1`, s.productID)
			}
		}
	})
}

// cleanupBatch registers a t.Cleanup removing a batch's change-log rows (append-only trigger
// blocks UPDATE, not DELETE), its lines (CASCADE) and the batch itself.
func cleanupBatch(t *testing.T, ctx context.Context, pool *pgxpool.Pool, batchID string) {
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM price_change_log WHERE batch_id = $1`, batchID)
		pool.Exec(ctx, `DELETE FROM price_batches WHERE id = $1`, batchID)
	})
}

// alignFixtureAnchorUnits points the named products' standing anchors at the
// products' own price unit, the unit every desk sheet figure arrives in, and
// restores each anchor's prior unit on cleanup. The round 2 law (LD-3 part B)
// refuses a write that names no unit onto an anchor standing in a unit other
// than the product's price unit, fail closed; the demo seed's provisional
// backfill planted anchors per sold_by while the products carry another
// price_uom, the mixed state the law refuses, so the lifecycle aligns them
// for its apply and leaves the seeded state standing behind it.
func alignFixtureAnchorUnits(t *testing.T, ctx context.Context, pool *pgxpool.Pool, skus []string) {
	t.Helper()
	type alignedUnit struct {
		productID string
		prior     string
	}
	var aligned []alignedUnit
	for _, sku := range skus {
		var pid, prior string
		if err := pool.QueryRow(ctx, `
			SELECT a.product_id::text, a.price_uom::text
			  FROM pricing_list_prices a JOIN products p ON p.id = a.product_id
			 WHERE p.sku = $1 AND a.price_uom <> p.price_uom`, sku).Scan(&pid, &prior); err != nil {
			continue // no anchor, or already standing in the product's price unit
		}
		if _, err := pool.Exec(ctx, `
			UPDATE pricing_list_prices a SET price_uom = p.price_uom
			  FROM products p WHERE p.id = a.product_id AND a.product_id = $1::uuid`, pid); err != nil {
			t.Fatalf("align anchor unit for %s: %v", sku, err)
		}
		aligned = append(aligned, alignedUnit{productID: pid, prior: prior})
	}
	t.Cleanup(func() {
		for _, a := range aligned {
			pool.Exec(ctx, `UPDATE pricing_list_prices SET price_uom = $2::citext WHERE product_id = $1::uuid`, a.productID, a.prior)
		}
	})
}

var techoFixtureSKUs = []string{"DBS-TB-BLU60", "DBS-TB-ABERDEEN", "DBS-TB-BOREALIS", "DBS-TB-MINICRETA", "DBS-TB-HYDR"}

func TestIntegration_A08_BatchLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := realSvc(pool)
	// Keep the shared private DB pristine for sibling integration tests.
	snapshotFixtureAnchors(t, ctx, pool, techoFixtureSKUs)
	alignFixtureAnchorUnits(t, ctx, pool, techoFixtureSKUs)

	// Locate the seeded Techo profile.
	var profileID string
	if err := pool.QueryRow(ctx,
		`SELECT p.id::text FROM price_import_profiles p JOIN vendors v ON v.id = p.vendor_id
		  WHERE v.vendor_no = 'V-TECHO' LIMIT 1`).Scan(&profileID); err != nil {
		t.Fatalf("seeded Techo profile not found (run make seed): %v", err)
	}

	batchNo := "PB-IT-" + time.Now().Format("150405.000")
	// Immediate effective date (yesterday) so apply is allowed now.
	eff := time.Now().AddDate(0, 0, -1)
	b, err := svc.CreateBatch(ctx, PriceBatch{
		BatchNo: batchNo, ProfileID: profileID, EffectiveDate: &eff, SourceRef: "techo_sample.csv",
	}, makerID)
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	cleanupBatch(t, ctx, pool, b.ID)
	if b.Status != BatchDraft {
		t.Fatalf("new batch status = %s", b.Status)
	}

	data, err := os.ReadFile("testdata/techo_sample.csv")
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.ImportBatch(ctx, b.ID, strings.NewReader(string(data)), "techo_sample.csv")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.MatchedLines != 5 || len(res.Unmatched) != 1 {
		t.Fatalf("import counts: matched=%d unmatched=%d", res.MatchedLines, len(res.Unmatched))
	}
	if res.Batch.Status != BatchStaged {
		t.Fatalf("post-import status = %s", res.Batch.Status)
	}
	// Manufacturer name is available on the staff read path (rule 1, internal only).
	if res.Batch.VendorName == "" {
		t.Logf("note: vendor name empty (batch created without vendor_id); acceptable")
	}

	// Separation of duties: the maker cannot approve.
	if _, err := svc.ApproveBatch(ctx, b.ID, makerID); err == nil {
		t.Fatal("expected separation-of-duties block when maker approves")
	}
	appr, err := svc.ApproveBatch(ctx, b.ID, checkerID)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if appr.Status != BatchApproved || appr.ApprovedBy != checkerID {
		t.Fatalf("approve result: %+v", appr)
	}

	// Apply now (effective date is in the past).
	_, applied, err := svc.ApplyBatch(ctx, b.ID, checkerID, time.Now())
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied != 5 {
		t.Fatalf("applied = %d, want 5 matched lines", applied)
	}

	// Unified retail anchor written: BLU60 cost 10.50 × 1.85 = 1942.5 → 1943.
	var blu60ID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM products WHERE sku = 'DBS-TB-BLU60'`).Scan(&blu60ID); err != nil {
		t.Fatal(err)
	}
	var anchor int64
	if err := pool.QueryRow(ctx, `SELECT list_price_cents FROM pricing_list_prices WHERE product_id = $1`, blu60ID).Scan(&anchor); err != nil {
		t.Fatalf("no anchor written for BLU60: %v", err)
	}
	if anchor != 1943 {
		t.Fatalf("BLU60 anchor = %d, want 1943 (cost×1.85)", anchor)
	}

	// Append-only change log: a retail row exists for BLU60 attributed to the checker.
	var logCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM price_change_log WHERE product_id = $1 AND field = 'retail_cents' AND batch_id = $2`,
		blu60ID, b.ID).Scan(&logCount); err != nil {
		t.Fatal(err)
	}
	if logCount != 1 {
		t.Fatalf("retail change-log rows for BLU60 = %d, want 1", logCount)
	}

	// Idempotent re-apply: no new anchor mutation, no new log rows.
	_, applied2, err := svc.ApplyBatch(ctx, b.ID, checkerID, time.Now())
	if err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if applied2 != 0 {
		t.Fatalf("re-apply applied = %d, want 0 (idempotent)", applied2)
	}
	var logCount2 int
	pool.QueryRow(ctx, `SELECT count(*) FROM price_change_log WHERE batch_id = $1`, b.ID).Scan(&logCount2)
	// 5 retail rows + 5 cost rows = 10 (cost present in the fixture).
	if logCount2 != 10 {
		t.Fatalf("total change-log rows = %d, want 10 (5 retail + 5 cost)", logCount2)
	}
}

// TestIntegration_A08_ChangeLogAppendOnly proves the 0039 trigger blocks UPDATE of a recorded
// price change (append-only audit, a correction must be a new row, never an in-place edit).
func TestIntegration_A08_ChangeLogAppendOnly(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	var prodID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM products WHERE sku = 'DBS-TB-ABERDEEN'`).Scan(&prodID); err != nil {
		t.Fatal(err)
	}
	var logID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO price_change_log (product_id, field, before_cents, after_cents, changed_by)
		 VALUES ($1, 'retail_cents', 100, 200, $2) RETURNING id::text`, prodID, checkerID).Scan(&logID); err != nil {
		t.Fatalf("insert change-log row: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM price_change_log WHERE id = $1`, logID) })
	if _, err := pool.Exec(ctx, `UPDATE price_change_log SET after_cents = 999 WHERE id = $1`, logID); err == nil {
		t.Fatal("expected the append-only trigger to block an UPDATE of price_change_log")
	}
}

// TestIntegration_A08_EffectiveDateGate proves a future-effective approved batch cannot be
// applied early, and the apply-due sweep only picks up arrived batches.
func TestIntegration_A08_EffectiveDateGate(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := realSvc(pool)

	var profileID string
	if err := pool.QueryRow(ctx,
		`SELECT p.id::text FROM price_import_profiles p JOIN vendors v ON v.id = p.vendor_id
		  WHERE v.vendor_no = 'V-UNILOCK' LIMIT 1`).Scan(&profileID); err != nil {
		t.Fatalf("seeded Unilock profile not found: %v", err)
	}

	future := time.Now().AddDate(0, 0, 30)
	b, err := svc.CreateBatch(ctx, PriceBatch{
		BatchNo: "PB-IT-FUT-" + time.Now().Format("150405.000"), ProfileID: profileID, EffectiveDate: &future,
	}, makerID)
	if err != nil {
		t.Fatal(err)
	}
	cleanupBatch(t, ctx, pool, b.ID)
	data, _ := os.ReadFile("testdata/unilock_sample.csv")
	if _, err := svc.ImportBatch(ctx, b.ID, strings.NewReader(string(data)), "unilock_sample.csv"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApproveBatch(ctx, b.ID, checkerID); err != nil {
		t.Fatal(err)
	}
	// Early apply blocked.
	if _, _, err := svc.ApplyBatch(ctx, b.ID, checkerID, time.Now()); err == nil {
		t.Fatal("expected future-effective batch to block apply")
	}
	// apply-due as-of now must NOT include the future batch.
	applied, err := svc.ApplyDueBatches(ctx, checkerID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Confirm the future batch is still APPROVED (not swept).
	after, _ := svc.GetBatch(ctx, b.ID)
	if after.Status != BatchApproved {
		t.Fatalf("future batch status = %s, want APPROVED (not applied); applied=%d", after.Status, applied)
	}
}
