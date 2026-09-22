package pos

// dib488_test.go, DIB-488 / D-096 unit tests: the dealer-level workspace preferences on pos_policy
// (line_presentation validation) and the unit-meta enrichment on the stock glance. Same harness and
// posture as service_w7_test.go: every DB touch is the fake store, no clock, no network.

import (
	"context"
	"errors"
	"testing"
)

// -- pos_policy: line_presentation is validated before the write ---------------------------------

func TestUpdatePolicy_LinePresentationRejected_422(t *testing.T) {
	h := newHarness()
	_, err := h.svc.UpdatePolicy(context.Background(), PosPolicy{LinePresentation: "TILES"})
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 422 {
		t.Fatalf("want a 422 for an unknown line_presentation, got %v", err)
	}
	// The refused write must not have landed: the org default row is untouched.
	if got := h.store.policies[""].LinePresentation; got != "" {
		t.Fatalf("a refused policy write reached the store: %q", got)
	}
}

func TestUpdatePolicy_LinePresentationDefaultsToLines(t *testing.T) {
	h := newHarness()
	p, err := h.svc.UpdatePolicy(context.Background(), PosPolicy{CODEnabled: true})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if p.LinePresentation != LinePresentationLines {
		t.Fatalf("an omitted line_presentation must normalize to LINES, got %q", p.LinePresentation)
	}
}

func TestUpdatePolicy_CardsAccepted_AndBranchRowOverrides(t *testing.T) {
	h := newHarness()
	if _, err := h.svc.UpdatePolicy(context.Background(),
		PosPolicy{BranchID: "branch-trt", CODEnabled: true, LinePresentation: LinePresentationCards, QuickChipsEnabled: true}); err != nil {
		t.Fatalf("update: %v", err)
	}
	p, err := h.svc.Policy(context.Background(), "branch-trt")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if p.LinePresentation != LinePresentationCards || !p.QuickChipsEnabled {
		t.Fatalf("branch override lost the DIB-488 fields: %+v", p)
	}
}

// -- stock glance: the DIB-488 unit meta rides the existing availability read --------------------

func TestStockGlance_CarriesUnitMetaAndLowThresholds(t *testing.T) {
	h := newHarness()
	h.store.stock["prod-1"] = map[string]float64{"branch-trt": 250}
	h.store.stockMeta["prod-1"] = fakeStockMeta{
		baseUOM: "PC", sellUOM: "SF", sellFactor: 4.1,
		lowBase: map[string]float64{"branch-trt": 50},
	}
	g, err := h.svc.StockGlance(context.Background(), "prod-1")
	if err != nil {
		t.Fatalf("glance: %v", err)
	}
	if g.BaseUOM != "PC" || g.SellUOM != "SF" || g.SellFactor != 4.1 {
		t.Fatalf("glance lost the unit meta: %+v", g)
	}
	if g.LowBase["branch-trt"] != 50 {
		t.Fatalf("glance lost the low threshold: %+v", g.LowBase)
	}
	if g.OnHand["branch-trt"] != 250 {
		t.Fatalf("the original on-hand read must be unchanged: %+v", g.OnHand)
	}
}

func TestStockGlance_NoThresholds_LowBaseAbsentNotEmpty(t *testing.T) {
	// A product with no authored reorder point has NO low threshold, the field must be absent
	// (nil → omitted from the JSON), never an empty map the client could mistake for "low at 0".
	h := newHarness()
	h.store.stock["prod-2"] = map[string]float64{"branch-trt": 10}
	h.store.stockMeta["prod-2"] = fakeStockMeta{baseUOM: "EA", sellUOM: "EA", sellFactor: 1}
	g, err := h.svc.StockGlance(context.Background(), "prod-2")
	if err != nil {
		t.Fatalf("glance: %v", err)
	}
	if g.LowBase != nil {
		t.Fatalf("no authored thresholds must serialize as absent, got %#v", g.LowBase)
	}
}
