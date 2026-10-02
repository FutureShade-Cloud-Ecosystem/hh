//go:build integration

package pos

// tax_silentzero_integration_test.go, the money-path proof at Trenton over
// the real composition on a throwaway local Postgres: the seeded
// STD_ON_HST_13 row is removed (fixture-scoped: the captured rows go back in
// the leg's cleanup, so a shared scratch database never loses them), then a
// sale is rung through the wired engine and the counter's tax-rate read is
// driven on the same damaged table. On the defect both assessSale fallback
// reads discard the found flag, so the ring commits UNTAXED with a 2xx and
// the read answers rate_bps 0 with a 200; the leg demands the 503 refusal
// (the counter-readable shape: the rate table is not configured, the sale
// cannot be ringed) and that no pos_sale row was written. The legs are
// written against main's tree, so the first demand is the refusal itself; the
// exact sentinel wording lands with the fix.
//
// Run (own throwaway container, removed afterwards, an unused port):
//
//	docker run --name hh-silentzero-pg -e POSTGRES_USER=hardscape \
//	  -e POSTGRES_PASSWORD=hardscape -e POSTGRES_DB=hardscapeos \
//	  -p 5688:5432 -d postgres:16
//	cd core
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5688/hardscapeos?sslmode=disable' \
//	  APP_ENV=dev go run ./cmd/core migrate
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5688/hardscapeos?sslmode=disable' \
//	  APP_ENV=dev go run ./cmd/core seed
//	CI=true DATABASE_URL='postgres://hardscape:hardscape@localhost:5688/hardscapeos?sslmode=disable' \
//	  go test -tags integration ./internal/pos/ -run TestIntegration_Trenton_StandardRateRowAbsent -p 1 -v

import (
	"context"
	"net/http"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tax"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ringTaxAssessor mirrors the cmd/core posTaxAdapter over *tax.Service (the
// M5 engine), which a test cannot import: the pos-declared input mapped onto
// tax.SaleAssessment with evidence pinned false, so the ring and the read
// assess through the real engine exactly as the wired composition does. The
// production adapter is this adapter's twin; when the fix lands, both gain
// the same translation.
type ringTaxAssessor struct{ svc *tax.Service }

func (a ringTaxAssessor) AssessSale(ctx context.Context, in TaxQuoteInput) (QuoteResult, error) {
	q, err := a.svc.AssessSale(ctx, tax.SaleAssessment{
		BranchID:         in.BranchID,
		SubtotalCents:    in.SubtotalCents,
		ReasonCode:       in.ReasonCode,
		EvidenceAttached: false,
	})
	if err != nil {
		return QuoteResult{}, err
	}
	return QuoteResult{TaxCents: q.TaxCents, RateBps: q.RateBps}, nil
}

// dropStandardRows removes every ON STD_ON_HST_13 row (the defect's world:
// deleted, never seeded, or every row expired) and restores the captured rows
// in the leg's cleanup, so the damage never outlives the leg and a shared
// scratch database never loses another suite's rows.
func dropStandardRows(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	rows, err := pool.Query(ctx, `
		SELECT id::text, rate_bps, effective_from::text, COALESCE(effective_to::text, ''), notes
		  FROM tax_rates WHERE jurisdiction = 'ON' AND reason_code = 'STD_ON_HST_13'`)
	if err != nil {
		t.Fatalf("capture the standard rows: %v", err)
	}
	type stdRow struct {
		id    string
		bps   int
		fromD string
		toD   string
		notes string
	}
	var captured []stdRow
	for rows.Next() {
		var r stdRow
		if err := rows.Scan(&r.id, &r.bps, &r.fromD, &r.toD, &r.notes); err != nil {
			rows.Close()
			t.Fatalf("scan the standard rows: %v", err)
		}
		captured = append(captured, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("read the standard rows: %v", err)
	}
	if len(captured) == 0 {
		t.Fatal("no seeded STD_ON_HST_13 row to remove: the scratch world is not migrated and seeded")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM tax_rates WHERE jurisdiction = 'ON' AND reason_code = 'STD_ON_HST_13'`); err != nil {
		t.Fatalf("remove the standard rows: %v", err)
	}
	t.Cleanup(func() {
		for _, r := range captured {
			if _, err := pool.Exec(ctx, `
				INSERT INTO tax_rates (id, jurisdiction, reason_code, rate_bps, effective_from, effective_to, notes)
				VALUES ($1::uuid, 'ON', 'STD_ON_HST_13', $2, $3::date, NULLIF($4, '')::date, $5)
				ON CONFLICT (id) DO NOTHING`, r.id, r.bps, r.fromD, r.toD, r.notes); err != nil {
				t.Errorf("restore the standard rows: %v", err)
			}
		}
	})
}

// The ring and the read on one damaged table: with the standard row absent,
// a CASH ring through the wired engine must refuse with the 503 the counter
// can act on and write nothing, and the counter's tax-rate read must refuse
// the same way, never answer rate 0 with a 200.
func TestIntegration_Trenton_StandardRateRowAbsent_SaleAndReadRefuse(t *testing.T) {
	s := newTrentonStack(t)
	taxSvc := tax.NewService(tax.NewRepository(s.pool))
	s.svc.WithTaxAssessor(ringTaxAssessor{svc: taxSvc})
	dropStandardRows(t, s.pool)

	// The ring: on the defect the sale commits untaxed with a 2xx; the leg
	// demands the 503 refusal and that no order row was written.
	idem := key()
	body := map[string]any{
		"idempotency_key": idem,
		"branch_id":       trentonBranchID,
		"till_session_id": s.trentonTill,
		"sale_type":       SaleCollectNow,
		"tender":          TenderCash,
		"lines":           []map[string]any{{"product_id": l2cProductID, "qty_entered": 10, "uom_entered": "SF"}},
	}
	rec := s.do(http.MethodPost, "/api/v1/pos/sales", s.bearer(trentonClerkSub), "", body)
	if rec.Code == http.StatusCreated || rec.Code == http.StatusOK {
		t.Fatalf("no standard rate row in effect: the ring must refuse, not commit untaxed: got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("the ring's refusal must be the 503 the counter can act on, got %d: %s", rec.Code, rec.Body.String())
	}
	var n int
	if err := s.pool.QueryRow(context.Background(), `SELECT count(*) FROM pos_sale WHERE idempotency_key = $1`, idem).Scan(&n); err != nil {
		t.Fatalf("count pos_sale: %v", err)
	}
	if n != 0 {
		t.Fatalf("a refused ring must write no order row, found %d pos_sale row(s)", n)
	}

	// The counter's tax-rate read on the same damaged table: on the defect it
	// answers rate_bps 0 with a 200; the leg demands the same 503 refusal.
	rec = s.do(http.MethodGet, "/api/v1/pos/tax-rate", s.bearer(trentonClerkSub), "", nil)
	if rec.Code == http.StatusOK {
		t.Fatalf("no standard rate row in effect: the tax-rate read must refuse, not answer rate 0 with a 200: %s", rec.Body.String())
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("the read's refusal must be the same 503, got %d: %s", rec.Code, rec.Body.String())
	}
}
