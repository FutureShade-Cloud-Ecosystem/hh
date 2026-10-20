package dashboard

import (
	"strings"
	"testing"
)

// HH-777 round 2: the viz and widget paths measure composed METRICS and the Needs-you sources, never a
// tile provider (the only caller of a tile's provider is Resolve, which asks the tile's access rule).
// This pins that none of them carries a payment figure today. The day one does, this fails and the
// author must give it the payment trail rule (Scope.PaymentTrail style) rather than slip it past the
// tile gate.
func TestVizAndWidgetCatalogsCarryNoPaymentFigure(t *testing.T) {
	for _, m := range MetricCatalog {
		if strings.Contains(strings.ToLower(m.ID+" "+m.Label+" "+m.Module), "payment") {
			t.Errorf("composed metric %q carries a payment figure with no access rule", m.ID)
		}
	}
	for _, n := range NeedsSourceCatalog {
		if strings.Contains(strings.ToLower(n.Key+" "+n.What), "payment") {
			t.Errorf("needs source %q carries a payment figure with no access rule", n.Key)
		}
	}
}
