//go:build integration

package pos

// The RETURN_COMPLETED completion row must carry the return's own number and
// total through the audit chokepoint into the stored audit_log row: returns.go
// sets return_no and total_cents in the record's Extra, and a key the
// chokepoint's deny-by-default allowlist lacks is dropped, leaving a
// nameless, amountless completion row. This leg completes one cash return
// over the real composition and reads the row back.
//
// Run (a throwaway local Postgres, this lane's container, removed after;
// angle brackets are placeholders, never a pinned port or database; migrate
// and seed first with the tree's entrypoints):
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:<port>/<database>?sslmode=disable' \
//	  CI=true go test -tags integration ./internal/pos/ -run TestIntegration_ReturnCompletedAuditCarriesReturnNoAndTotal -p 1 -v

import (
	"net/http"
	"testing"
)

func TestIntegration_ReturnCompletedAuditCarriesReturnNoAndTotal(t *testing.T) {
	s := newTrentonStack(t)
	sale := s.ringSale(key(), trentonBranchID, s.trentonTill, "trenton", []float64{2}, TenderCash)
	in := s.returnBody(key(), sale, 0, RefundCash, 2)
	rec := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", in)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the cash return must commit, got %d: %s", rec.Code, rec.Body.String())
	}
	out := s.decodeBody(rec)
	returnID := out["return_id"].(string)
	wantNo, ok := out["return_no"].(string)
	if !ok || wantNo == "" {
		t.Fatalf("the committed return must answer its own number: %v", out)
	}
	wantTotal, ok := out["total_cents"].(float64)
	if !ok {
		t.Fatalf("the committed return must answer its total: %v", out)
	}
	rows := s.auditRows("RETURN_COMPLETED", returnID)
	if len(rows) != 1 {
		t.Fatalf("want exactly one completion row under the return, got %d", len(rows))
	}
	if rows[0]["return_no"] != wantNo {
		t.Fatalf("the stored completion row must carry the return number %q: %v", wantNo, rows[0])
	}
	if rows[0]["total_cents"] != wantTotal {
		t.Fatalf("the stored completion row must carry the committed total %v: %v", wantTotal, rows[0])
	}
}
