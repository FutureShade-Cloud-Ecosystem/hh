package gl

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// fakeCostBasis reports a cost basis only for the product ids in `have`; all others report found=false
// (the production reality for a product never received against a PO). costCents is the per-line cost.
type fakeCostBasis struct {
	have      map[string]bool
	costCents int64
}

func (c fakeCostBasis) UnitCostCents(_ context.Context, productID string, _ float64) (int64, bool, error) {
	if c.have[productID] {
		return c.costCents, true, nil
	}
	return 0, false, nil
}

// TestOrderFulfilled_MissingCostBasis_LogsWARN is the L8 regression for the SILENT COGS no-op: a sold line
// whose product has no cost basis (never received against a PO) books revenue+cash but no COGS, overstating
// gross margin. The fix makes that OBSERVABLE, a WARN carrying the product id + order id. Without the WARN
// this test fails (the log buffer is empty), and the COGS leg for the product that DOES have a basis must
// still post so a partially-costed order is not swallowed whole.
func TestOrderFulfilled_MissingCostBasis_LogsWARN(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()

	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	svc := newTestService(f).
		WithCostBasis(fakeCostBasis{have: map[string]bool{"prod-costed": true}, costCents: 4200}).
		withLog(slog.New(handler))

	e := ev("order.fulfilled", map[string]any{
		"order_id":  "ord-1",
		"branch_id": "b1",
		"lines": []map[string]any{
			{"product_id": "prod-costed", "qty_base": 1.0}, // has a cost basis → COGS posts
			{"product_id": "prod-nocost", "qty_base": 3.0}, // NO cost basis → silent no-op pre-fix
		},
	})
	if err := svc.consume(ctx, e); err != nil {
		t.Fatalf("consume order.fulfilled: %v", err)
	}

	// The costed line must still post its COGS leg (DR 5000 / CR 1200 for 4200).
	if len(f.inserted) != 1 {
		t.Fatalf("want exactly 1 COGS journal for the costed line, got %d", len(f.inserted))
	}
	wantLeg(t, &f.inserted[0], "5000", 4200, 0)
	wantLeg(t, &f.inserted[0], "1200", 0, 4200)

	// The missing-cost-basis line must be OBSERVABLE: a WARN carrying product id + order id.
	out := buf.String()
	if out == "" {
		t.Fatalf("missing cost basis was a SILENT no-op, expected a WARN log, got none")
	}
	var rec map[string]any
	// Take the last WARN line.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &rec); err != nil {
		t.Fatalf("parse log line %q: %v", lines[len(lines)-1], err)
	}
	if rec["level"] != "WARN" {
		t.Fatalf("cost-basis skip must log at WARN, got level=%v", rec["level"])
	}
	if rec["product_id"] != "prod-nocost" {
		t.Fatalf("WARN must name the uncosted product, got product_id=%v", rec["product_id"])
	}
	if rec["order_id"] != "ord-1" {
		t.Fatalf("WARN must name the order, got order_id=%v", rec["order_id"])
	}
	// The costed product must NOT be warned about.
	if strings.Contains(out, "prod-costed") {
		t.Fatalf("a product WITH a cost basis must not trigger a missing-cost-basis WARN:\n%s", out)
	}
}
