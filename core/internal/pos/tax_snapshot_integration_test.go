//go:build integration

package pos

// tax_snapshot_integration_test.go, the ring's relief snapshot money legs at
// Trenton over the real composition on a throwaway local Postgres (the
// repository's integration database, the Trenton legs' world): the real
// handler behind the real guards, the tax assessor wired over the real
// tax.Service the way cmd/core wires it (the rig's hand copy,
// servedTaxAssessor, from taxrate_integration_test.go), so the W4 snapshot row
// under test is the row the real ring writes through the real engine.
//
// The defect these legs pin: the relief snapshot wrote rate_bps as the flat
// 1300 constant no matter what rate the assessor bound one screen earlier,
// wrote the ASSESSED tax into full_hst_cents (a column the tax package's
// recompute reads at the CONSTANT standard rate), and never wrote the ON
// credit at all. The legs: under an edited standard row the row must carry the
// assessed rate, the standard-rate HST, the assessed net, and reconstruct the
// exact subtotal under the recompute's constant-rate convention; a sale with
// no relief must write no row at all; and in the seeded world the row must
// equal today's values exactly (the freeze pin, which passes before AND after
// the fix: it exists to prove the fix changes nothing the seeded world sees).
//
// Run (own throwaway container, removed afterwards, an unused port):
//
//	docker run --name hh-snapshot-pg -e POSTGRES_USER=hardscape \
//	  -e POSTGRES_PASSWORD=hardscape -e POSTGRES_DB=hardscapeos \
//	  -p 5648:5432 -d postgres:16
//	cd core
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5648/hardscapeos?sslmode=disable' \
//	  APP_ENV=dev go run ./cmd/core migrate
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5648/hardscapeos?sslmode=disable' \
//	  APP_ENV=dev go run ./cmd/core seed
//	CI=true DATABASE_URL='postgres://hardscape:hardscape@localhost:5648/hardscapeos?sslmode=disable' \
//	  go test -tags integration ./internal/pos/ -run TestIntegration_Trenton_TaxSnapshot -p 1 -v

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tax"
	"github.com/jackc/pgx/v5/pgxpool"
)

// snapshotKeyFamily separates each leg's key minter, so two legs in one go
// test process never mint the same first key (a fresh minter per test must
// not replay a predecessor's sale).
var snapshotKeyFamily atomic.Int64

// snapshotKeys mints idempotency keys salted per process and per minter, the
// DIB-384 pattern: one go test process must not collide with its
// predecessors on a shared database, and these legs' sales must never replay
// another leg's.
func snapshotKeys() func() string {
	salt := fmt.Sprintf("%02x%06x", snapshotKeyFamily.Add(1), os.Getpid())[:8]
	seq := 0
	return func() string {
		seq++
		return fmt.Sprintf("5d1bf000-0000-4000-9000-%s%04x", salt, seq)
	}
}

// stdRateCapture is one captured tax_rates row, what the restore needs.
type stdRateCapture struct {
	id  string
	bps int
}

// setStdRate rewrites every ON STD_ON_HST_13 row to the leg's edited value in
// ONE statement (the data-modifying CTE reads the pre-update values, so there
// is no capture/edit window) and restores the captured rows in the cleanup,
// verifying the restore before it returns (fixture-scoped damage, the
// tax_adapter_translation_test.go pattern: the seeded world is never left
// edited).
func setStdRate(t *testing.T, pool *pgxpool.Pool, editedBps int) {
	t.Helper()
	ctx := context.Background()
	rows, err := pool.Query(ctx, `
		WITH before AS (
			SELECT id::text AS id, rate_bps AS bps FROM tax_rates
			 WHERE jurisdiction = 'ON' AND reason_code = 'STD_ON_HST_13'
		), edited AS (
			UPDATE tax_rates SET rate_bps = $1
			 WHERE jurisdiction = 'ON' AND reason_code = 'STD_ON_HST_13'
			RETURNING id
		)
		SELECT before.id, before.bps FROM before JOIN edited ON edited.id::text = before.id`, editedBps)
	if err != nil {
		t.Fatalf("edit the standard rows: %v", err)
	}
	var captured []stdRateCapture
	for rows.Next() {
		var c stdRateCapture
		if err := rows.Scan(&c.id, &c.bps); err != nil {
			rows.Close()
			t.Fatalf("scan the edited standard rows: %v", err)
		}
		captured = append(captured, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("read the edited standard rows: %v", err)
	}
	if len(captured) == 0 {
		t.Fatal("no seeded STD_ON_HST_13 row to edit: the scratch world is not migrated and seeded")
	}
	t.Cleanup(func() {
		for _, c := range captured {
			if _, err := pool.Exec(ctx, `UPDATE tax_rates SET rate_bps = $1 WHERE id = $2::uuid`, c.bps, c.id); err != nil {
				t.Errorf("restore the standard rows: %v", err)
			}
		}
		var back int
		if err := pool.QueryRow(ctx,
			`SELECT rate_bps FROM tax_rates WHERE jurisdiction = 'ON' AND reason_code = 'STD_ON_HST_13' ORDER BY effective_from DESC LIMIT 1`).
			Scan(&back); err != nil || back != captured[0].bps {
			t.Errorf("the standard row must read %d after the restore (err %v), got %d", captured[0].bps, err, back)
		}
	})
}

// seedEvidenceDoc seeds one real tax_evidence_doc row (the snapshot's
// evidence_doc_id column is a UUID foreign key into that table, so the relief
// body must carry a doc that exists). The doc row STAYS: deleting a doc a
// snapshot points at runs the foreign key's ON DELETE SET NULL, an UPDATE the
// snapshot table's insert-only trigger refuses, by design.
func seedEvidenceDoc(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var docID string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO tax_evidence_doc (doc_type) VALUES ('OTHER') RETURNING id::text`).Scan(&docID); err != nil {
		t.Fatalf("seed the evidence doc: %v", err)
	}
	return docID
}

// ringReliefSale rings one relief sale of the rig's exact-cents product
// through the real handler (10 SF at the 10.00 anchor: a 100.00 subtotal,
// cash) carrying the named relief reason and evidence doc id, and returns the
// committed view.
func ringReliefSale(t *testing.T, s *trentonStack, key, reason, evidenceDocID string) trentonSaleView {
	t.Helper()
	body := map[string]any{
		"idempotency_key": key,
		"branch_id":       trentonBranchID,
		"till_session_id": s.trentonTill,
		"sale_type":       SaleCollectNow,
		"tender":          TenderCash,
		"lines":           []map[string]any{{"product_id": l2cProductID, "qty_entered": 10.0, "uom_entered": "SF"}},
		"relief": map[string]any{
			"reason_code":     reason,
			"evidence_doc_id": evidenceDocID,
		},
	}
	rec := s.do(http.MethodPost, "/api/v1/pos/sales", s.bearer(trentonClerkSub), "", body)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("ring the relief sale %s: %d %s", key, rec.Code, rec.Body.String())
	}
	var out trentonSaleView
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode the relief sale: %v", err)
	}
	if out.Status != "COMMITTED" {
		t.Fatalf("relief sale %s not committed: %s", key, rec.Body.String())
	}
	return out
}

// snapshotRow is the W4 snapshot row as the legs read it back.
type snapshotRow struct {
	rateBps      int
	fullHST      int64
	onCredit     int64
	netCollected int64
	automated    bool
	ruleVersion  string
	reason       string
	evidenceDoc  string
}

// snapshotRowForOrder reads the one snapshot row an order's ring wrote.
func snapshotRowForOrder(t *testing.T, pool *pgxpool.Pool, orderID string) snapshotRow {
	t.Helper()
	var r snapshotRow
	err := pool.QueryRow(context.Background(), `
		SELECT rate_bps, full_hst_cents, on_credit_cents, net_collected_cents, automated,
		       rule_version, tax_reason_code, COALESCE(evidence_doc_id::text, '')
		  FROM tax_decision_snapshot WHERE order_id = $1::uuid`, orderID).
		Scan(&r.rateBps, &r.fullHST, &r.onCredit, &r.netCollected, &r.automated,
			&r.ruleVersion, &r.reason, &r.evidenceDoc)
	if err != nil {
		t.Fatalf("read the W4 snapshot row for %s: %v", orderID, err)
	}
	return r
}

// The edited-rate money leg: with the seeded standard row EDITED to a rate
// that is not 1300, a relief sale collects at the assessed rate but the
// snapshot must still tell the audit truth: the assessed rate, the
// STANDARD-rate HST in full_hst_cents (the column the recompute reads at the
// constant rate), the assessed net the ring actually collected, and the exact
// subtotal recoverable from the row. On the defect the row claims 1300 beside
// an assessed-rate full_hst, and the reconstruction lands on a wrong base.
func TestIntegration_Trenton_TaxSnapshot_EditedRate(t *testing.T) {
	s := newTrentonStack(t)
	taxSvc := tax.NewService(tax.NewRepository(s.pool))
	s.svc.WithTaxAssessor(servedTaxAssessor{svc: taxSvc})

	setStdRate(t, s.pool, 1500)

	keys := snapshotKeys()
	docID := seedEvidenceDoc(t, s.pool)
	sale := ringReliefSale(t, s, keys(), TaxONFNPos8, docID)
	row := snapshotRowForOrder(t, s.pool, sale.OrderID)

	const subtotal = int64(10000)              // 10 SF at the 10.00 anchor
	assessed := (subtotal*1500 + 5000) / 10000 // what the ring collected at the edited row: 1500
	wantFull := flatHST13(subtotal)            // the standard-rate HST the column's contract demands: 1300

	if row.reason != TaxONFNPos8 || row.ruleVersion != ruleVersionW4 {
		t.Fatalf("the row must be the relief's W4 snapshot: %+v", row)
	}
	if row.rateBps != 1500 {
		t.Fatalf("rate_bps must read the ASSESSED rate the ring bound (1500), got %d: the snapshot claims the flat constant in an edited-rate world", row.rateBps)
	}
	if row.fullHST != wantFull {
		t.Fatalf("full_hst_cents must be the standard-rate HST on the subtotal (%d), got %d: the assessed tax in this column breaks the recompute's constant-rate read", wantFull, row.fullHST)
	}
	if row.netCollected != assessed {
		t.Fatalf("net_collected_cents must be the assessed tax the ring collected (%d), got %d", assessed, row.netCollected)
	}
	if row.onCredit != 0 {
		t.Fatalf("on_credit_cents must stay 0 through the wired composition (evidence pinned false, the relief did not bind), got %d", row.onCredit)
	}
	// The recompute convention, asserted on the production row:
	// tax.reconstructBase recovers the taxable subtotal by dividing
	// full_hst_cents by the CONSTANT 1300; the row the ring writes must
	// reconstruct the exact subtotal or the W4 recompute and the HST to GL
	// tie-out reason on a wrong base.
	if recon := row.fullHST * 10000 / 1300; recon != subtotal {
		t.Fatalf("the production row misreconstructs under the recompute's constant-rate convention: %d, want %d", recon, subtotal)
	}

	// The companion pin: the same edited row, a sale with NO relief writes NO
	// snapshot row (the insert sits inside the relief arm alone).
	plain := s.ringSale(keys(), trentonBranchID, s.trentonTill, "trenton", []float64{10}, TenderCash)
	var n int
	if err := s.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM tax_decision_snapshot WHERE order_id = $1::uuid`, plain.OrderID).Scan(&n); err != nil {
		t.Fatalf("count the plain sale's snapshot rows: %v", err)
	}
	if n != 0 {
		t.Fatalf("a sale with no relief must write no snapshot row, got %d", n)
	}
}

// The FREEZE pin: in the seeded world (no edits) the row the ring writes
// equals today's values exactly. This leg passes BEFORE the fix and AFTER it
// (at the standard rate the assessed quote IS the standard-rate HST, and the
// wired composition carries a zero credit), it exists to prove the fix
// changes nothing the seeded world can see.
func TestIntegration_Trenton_TaxSnapshot_SeededWorldByteIdentical(t *testing.T) {
	s := newTrentonStack(t)
	taxSvc := tax.NewService(tax.NewRepository(s.pool))
	s.svc.WithTaxAssessor(servedTaxAssessor{svc: taxSvc})

	keys := snapshotKeys()
	docID := seedEvidenceDoc(t, s.pool)
	sale := ringReliefSale(t, s, keys(), TaxONFNPos8, docID)
	row := snapshotRowForOrder(t, s.pool, sale.OrderID)

	const subtotal = int64(10000)
	assessed := flatHST13(subtotal) // 1300: at the standard row the assessed tax IS the standard-rate HST
	if row.rateBps != 1300 {
		t.Fatalf("seeded world rate_bps must read 1300, got %d", row.rateBps)
	}
	if row.fullHST != assessed || row.netCollected != assessed {
		t.Fatalf("seeded world full_hst and net must both read the assessed tax %d, got %d and %d", assessed, row.fullHST, row.netCollected)
	}
	if row.onCredit != 0 {
		t.Fatalf("seeded world on_credit_cents must read 0 (the wired composition carries no bound credit), got %d", row.onCredit)
	}
	if row.automated {
		t.Fatal("the ring's snapshot is a manual decision, automated must be false")
	}
	if row.reason != TaxONFNPos8 || row.ruleVersion != ruleVersionW4 {
		t.Fatalf("seeded world reason/rule wrong: %+v", row)
	}
	if row.evidenceDoc != docID {
		t.Fatalf("the passed evidence doc id must be recorded verbatim, got %q want %q", row.evidenceDoc, docID)
	}
}
