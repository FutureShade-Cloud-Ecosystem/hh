//go:build integration

package pos

// The SALE_COMPLETED record carries the order facts as IDS through the audit
// chokepoint into the stored audit_log row: salesperson_id survives, while
// po_job_ref and job_reference stay dropped. Those two are free text a staff
// member types at the counter, so a value can hold a customer's name or a site
// address, and the reference is read from the sale record when shown.
// salesperson_name and job_address (a person's name and an address) stay
// dropped too. The tax relief assessment record carries declared_use and
// fulfilment. A key the chokepoint's deny-by-default allowlist lacks is
// dropped, so free text a caller sends cannot reach the stored row.
//
// Run (a throwaway local Postgres, this lane's container, removed after;
// angle brackets are placeholders, never a pinned port or database; migrate
// and seed first with the tree's entrypoints):
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:<port>/<database>?sslmode=disable' \
//	  CI=true go test -tags integration ./internal/pos/ -run 'TestIntegration_(SaleCompletedAudit|ReliefAssessAudit)' -p 1 -v

import (
	"context"
	"net/http"
	"testing"
)

func TestIntegration_SaleCompletedAuditCarriesIdsNotFreeTextNamesOrAddresses(t *testing.T) {
	s := newTrentonStack(t)
	var staffID string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT id::text FROM users WHERE status = 'ACTIVE' ORDER BY id LIMIT 1`).Scan(&staffID); err != nil {
		t.Fatalf("read an active staff id: %v", err)
	}
	body := map[string]any{
		"idempotency_key": key(),
		"branch_id":       trentonBranchID,
		"till_session_id": s.trentonTill,
		"sale_type":       SaleCollectNow,
		"tender":          TenderCash,
		"lines":           []map[string]any{{"product_id": l2cProductID, "qty_entered": 2, "uom_entered": "SF"}},
		"po_job_ref":      "PO 4471 / JOB 12",
		"salesperson_id":  staffID,
		"job_reference":   "JOB-12",
		"job_address":     "128 Front St, Trenton ON",
	}
	rec := s.do(http.MethodPost, "/api/v1/pos/sales", s.bearer(trentonClerkSub), "", body)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("ring sale: %d %s", rec.Code, rec.Body.String())
	}
	orderID, _ := s.decodeBody(rec)["order_id"].(string)
	if orderID == "" {
		t.Fatalf("the sale must answer its order id: %s", rec.Body.String())
	}
	rows := s.auditRows("SALE_COMPLETED", orderID)
	if len(rows) != 1 {
		t.Fatalf("want exactly one SALE_COMPLETED row under the order, got %d", len(rows))
	}
	row := rows[0]
	if row["salesperson_id"] != staffID {
		t.Fatalf("the stored sale row must carry salesperson_id %q: %v", staffID, row)
	}
	for _, k := range []string{"po_job_ref", "job_reference", "salesperson_name", "job_address"} {
		if _, ok := row[k]; ok {
			t.Fatalf("the stored sale row must not carry %s (free text, a name or an address): %v", k, row)
		}
	}
}

func TestIntegration_ReliefAssessAuditCarriesDeclaredUseAndFulfilment(t *testing.T) {
	s := newTrentonStack(t)
	var before int64
	if err := s.pool.QueryRow(context.Background(),
		`SELECT COALESCE(max(seq), 0) FROM audit_log`).Scan(&before); err != nil {
		t.Fatalf("read the audit high-water mark: %v", err)
	}
	rec := s.do(http.MethodPost, "/api/v1/pos/assess-relief", s.bearer(trentonClerkSub), "", map[string]any{
		"purchaser_type": "individual",
		"declared_use":   "personal",
		"fulfilment":     "pickup",
		"subtotal_cents": 10000,
		"purpose_ack":    true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("assess relief: %d %s", rec.Code, rec.Body.String())
	}
	var use, fulfilment *string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT changes->>'declared_use', changes->>'fulfilment'
		   FROM audit_log WHERE action = 'TAX_RELIEF_ASSESS' AND seq > $1
		  ORDER BY seq DESC LIMIT 1`, before).Scan(&use, &fulfilment); err != nil {
		t.Fatalf("read the assessment row: %v", err)
	}
	if use == nil || *use != "personal" || fulfilment == nil || *fulfilment != "pickup" {
		t.Fatalf("the stored assessment row must carry declared_use and fulfilment: %v %v", use, fulfilment)
	}
}
