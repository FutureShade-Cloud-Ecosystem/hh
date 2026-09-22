package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
)

// fakeCuration is an in-memory MetricCurationStore.
type fakeCuration struct {
	rows map[string][]CuratedMetric
	err  error
}

func (f *fakeCuration) CuratedMetrics(_ context.Context, profile string) ([]CuratedMetric, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rows[profile], nil
}

func metricIDs(ms []MetricMeta) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

func hasID(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// -- the composed id -----------------------------------------------------------------------------

func TestComposeAndParseRoundTrip(t *testing.T) {
	id := ComposeID(MetricARBalance, DimBucket, ShapeSplit)
	if id != "viz.ar.aging-bucket-split" {
		t.Fatalf("composed id = %q", id)
	}
	c, ok := ParseComposition(id)
	if !ok {
		t.Fatal("a composed id must parse back")
	}
	if c.MetricID != MetricARBalance || c.Dimension != DimBucket || c.Shape != ShapeSplit {
		t.Fatalf("round trip lost a choice: %+v", c)
	}
}

func TestParseCompositionRefusesAnythingOutsideTheClosedSets(t *testing.T) {
	for _, id := range []string{
		"",                                // not a composition at all
		"ar.aging-bucket-split",           // no viz. prefix: somebody else's item id
		"viz.ar.aging",                    // no dimension, no shape
		"viz.ar.aging-bucket",             // no shape
		"viz.nope.metric-branch-bar",      // a metric this build does not have
		"viz.ar.aging-customer-split",     // a dimension the metric does not offer
		"viz.ar.aging-bucket-scatter",     // a shape nobody vetted
		"viz.orders.open-bucket-bar",      // a real dimension, wrong metric
		"viz.margin.gross_bps-status-bar", // a real metric, dimension it does not offer
	} {
		if _, ok := ParseComposition(id); ok {
			t.Fatalf("ParseComposition(%q) must refuse: a stale saved tile must not become a query", id)
		}
	}
}

// -- the gate ------------------------------------------------------------------------------------

func TestMetricsForDropsEveryCostMetricWhenCostIsNotAllowed(t *testing.T) {
	got := metricIDs(MetricsFor(nil, false))
	for _, m := range MetricCatalog {
		if m.Class != ClassCost {
			continue
		}
		if hasID(got, m.ID) {
			t.Fatalf("cost-class metric %q reached a caller without cost visibility", m.ID)
		}
	}
	if len(got) == 0 {
		t.Fatal("a caller without cost visibility still gets the operational catalog")
	}
}

func TestMetricsForKeepsCostMetricsForAnAllowedCaller(t *testing.T) {
	got := metricIDs(MetricsFor(nil, true))
	if !hasID(got, MetricGrossMargin) || !hasID(got, MetricInventoryValue) {
		t.Fatalf("an allowed caller must be offered the cost metrics: %v", got)
	}
}

// THE RULING, AS A TEST. Curation decides the MENU; it never grants anything. An admin who marks a
// cost metric visible for a shape whose people are not manager-up still sends them a payload with
// no cost metric in it.
func TestCurationCannotGrantACostMetric(t *testing.T) {
	curated := []CuratedMetric{
		{MetricID: MetricGrossMargin, Position: 0, Visible: true},
		{MetricID: MetricInventoryValue, Position: 1, Visible: true},
		{MetricID: MetricOrdersOpen, Position: 2, Visible: true},
	}
	got := metricIDs(MetricsFor(curated, false))
	if hasID(got, MetricGrossMargin) || hasID(got, MetricInventoryValue) {
		t.Fatalf("curation granted a cost metric: %v", got)
	}
	if !hasID(got, MetricOrdersOpen) {
		t.Fatalf("the curated operational metric must survive: %v", got)
	}
}

func TestCurationHidesAndOrders(t *testing.T) {
	curated := []CuratedMetric{
		{MetricID: MetricWarrantyOpen, Position: 0, Visible: true},
		{MetricID: MetricOrdersOpen, Position: 1, Visible: true},
		{MetricID: MetricPickQueue, Position: 2, Visible: false},
	}
	got := metricIDs(MetricsFor(curated, false))
	if len(got) != 2 || got[0] != MetricWarrantyOpen || got[1] != MetricOrdersOpen {
		t.Fatalf("curated order not honoured: %v", got)
	}
	if hasID(got, MetricPickQueue) {
		t.Fatal("a metric curated invisible must not be offered")
	}
}

func TestNoCurationOffersTheShippedCatalog(t *testing.T) {
	if len(MetricsFor(nil, true)) != len(MetricCatalog) {
		t.Fatal("an uncurated dealer gets a working composer, not an empty one")
	}
}

func TestCurationIgnoresAnIdThisBuildDoesNotHave(t *testing.T) {
	curated := []CuratedMetric{
		{MetricID: "gone.metric", Position: 0, Visible: true},
		{MetricID: MetricOrdersOpen, Position: 1, Visible: true},
	}
	got := metricIDs(MetricsFor(curated, false))
	if len(got) != 1 || got[0] != MetricOrdersOpen {
		t.Fatalf("an unknown curated id renders nothing, silently: %v", got)
	}
}

// -- resolving composed tiles --------------------------------------------------------------------

func vizService(t *testing.T) *Service {
	t.Helper()
	s := NewService(newFakeStore(), nil)
	s.WithMetricStore(&fakeCuration{rows: map[string][]CuratedMetric{}})
	return s
}

func TestResolveVizMeasuresAnAllowedTile(t *testing.T) {
	s := vizService(t)
	if err := s.RegisterSeries(MetricOrdersOpen, SeriesFunc(
		func(context.Context, Scope, string) ([]Point, error) {
			return []Point{{Label: "CONFIRMED", Number: 2}, {Label: "INVOICED", Number: 3}}, nil
		})); err != nil {
		t.Fatal(err)
	}
	id := ComposeID(MetricOrdersOpen, DimStatus, ShapeBar)
	got := s.ResolveViz(context.Background(), Scope{Profile: ProfileCounter}, []string{id})
	if len(got) != 1 || got[0].State != StateOK {
		t.Fatalf("expected one measured tile, got %+v", got)
	}
	if got[0].Total == nil || *got[0].Total != 5 {
		t.Fatalf("the SERVER totals a series, not the client: %+v", got[0].Total)
	}
}

func TestResolveVizLeavesAnUnwiredMetricPresentAndSaysSo(t *testing.T) {
	s := vizService(t)
	id := ComposeID(MetricOrdersOpen, DimStatus, ShapeBar)
	got := s.ResolveViz(context.Background(), Scope{Profile: ProfileCounter}, []string{id})
	if len(got) != 1 {
		t.Fatalf("an unwired metric keeps its tile, a saved chart must not look deleted: %+v", got)
	}
	if got[0].State != StateUnwired || got[0].Note == "" {
		t.Fatalf("an unwired composed tile says which module it waits on: %+v", got[0])
	}
	if len(got[0].Points) != 0 {
		t.Fatal("an unwired tile invents no points")
	}
}

func TestResolveVizOmitsACostTileForACallerWithoutCostVisibility(t *testing.T) {
	s := vizService(t)
	s.RegisterSeries(MetricGrossMargin, SeriesFunc(
		func(context.Context, Scope, string) ([]Point, error) {
			return []Point{{Label: "TRT", Number: 3400}}, nil
		}))
	id := ComposeID(MetricGrossMargin, DimBranch, ShapeBar)
	got := s.ResolveViz(context.Background(), Scope{Profile: ProfileManager, CostAllowed: false}, []string{id})
	if len(got) != 0 {
		t.Fatalf("a cost tile must be ABSENT, not empty and not refused in words: %+v", got)
	}
}

// THE MUTATION TEST THE LANE RULES REQUIRE: byte-absence, asserted on the serialized payload, for
// every cost-bearing metric and for a composed tile naming one.
func TestCostBearingMetricsAreByteAbsentForACallerWhoMayNotSeeThem(t *testing.T) {
	s := vizService(t)
	for _, m := range []string{MetricGrossMargin, MetricInventoryValue} {
		s.RegisterSeries(m, SeriesFunc(func(context.Context, Scope, string) ([]Point, error) {
			return []Point{{Label: "TRT", Number: 999999}}, nil
		}))
	}
	scope := Scope{Profile: ProfileCounter, RoleCode: "INSIDE_SALES", CostAllowed: false}

	catalog, err := json.Marshal(map[string]any{
		"metrics": s.MetricCatalogFor(context.Background(), scope),
	})
	if err != nil {
		t.Fatal(err)
	}
	tiles, err := json.Marshal(map[string]any{
		"tiles": s.ResolveViz(context.Background(), scope, []string{
			ComposeID(MetricGrossMargin, DimBranch, ShapeBar),
			ComposeID(MetricInventoryValue, DimBranch, ShapeSplit),
			ComposeID(MetricOrdersOpen, DimStatus, ShapeBar),
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{string(catalog), string(tiles)} {
		for _, forbidden := range []string{
			MetricGrossMargin, MetricInventoryValue,
			"margin", "Gross margin", "Inventory value", "cost", "999999",
		} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("byte %q is present for a caller who may not see cost: %s", forbidden, body)
			}
		}
	}
	// And the same payload still carries the operational metric, so the assertion above is
	// measuring absence rather than an empty response.
	if !strings.Contains(string(tiles), MetricOrdersOpen) {
		t.Fatalf("the allowed tile must still be there: %s", tiles)
	}
}

func TestResolveVizRefusesAnIdOutsideTheClosedSets(t *testing.T) {
	s := vizService(t)
	got := s.ResolveViz(context.Background(), Scope{Profile: ProfileManager, CostAllowed: true},
		[]string{"viz.ar.aging-customer-split", "not-a-viz-item", "viz.gone.metric-branch-bar"})
	if len(got) != 0 {
		t.Fatalf("nothing outside the closed sets may be measured: %+v", got)
	}
}

func TestResolveVizDedupesAndNumbersPositions(t *testing.T) {
	s := vizService(t)
	a := ComposeID(MetricOrdersOpen, DimStatus, ShapeBar)
	b := ComposeID(MetricWarrantyOpen, DimStatus, ShapeList)
	got := s.ResolveViz(context.Background(), Scope{Profile: ProfileCounter}, []string{a, b, a})
	if len(got) != 2 || got[0].Position != 0 || got[1].Position != 1 {
		t.Fatalf("composed tiles are deduped and positioned in ask order: %+v", got)
	}
}

func TestSeriesProviderFailureDegradesRatherThanFails(t *testing.T) {
	s := vizService(t)
	s.RegisterSeries(MetricOrdersOpen, SeriesFunc(
		func(context.Context, Scope, string) ([]Point, error) {
			return nil, errors.New("boom")
		}))
	got := s.ResolveViz(context.Background(), Scope{Profile: ProfileCounter},
		[]string{ComposeID(MetricOrdersOpen, DimStatus, ShapeBar)})
	if len(got) != 1 || got[0].State != StateError || got[0].Note == "" {
		t.Fatalf("a failing series degrades to a stated error, never a 500: %+v", got)
	}
}

func TestSeriesProviderPanicDegradesToo(t *testing.T) {
	s := vizService(t)
	s.RegisterSeries(MetricOrdersOpen, SeriesFunc(
		func(context.Context, Scope, string) ([]Point, error) {
			panic("a nil map in an adapter")
		}))
	got := s.ResolveViz(context.Background(), Scope{Profile: ProfileCounter},
		[]string{ComposeID(MetricOrdersOpen, DimStatus, ShapeBar)})
	if len(got) != 1 || got[0].State != StateError {
		t.Fatalf("a panicking series degrades like a failing one: %+v", got)
	}
}

func TestRegisterSeriesRefusesAnUnknownMetric(t *testing.T) {
	s := vizService(t)
	if err := s.RegisterSeries("nope.metric", SeriesFunc(
		func(context.Context, Scope, string) ([]Point, error) { return nil, nil })); err == nil {
		t.Fatal("a typo'd metric id must be refused at boot, not silently dead")
	}
	if err := s.RegisterSeries(MetricOrdersOpen, nil); err == nil {
		t.Fatal("a nil provider must be refused")
	}
}

func TestUnreadableCurationOffersTheShippedCatalogRatherThanNothing(t *testing.T) {
	s := NewService(newFakeStore(), nil)
	s.WithMetricStore(&fakeCuration{err: errors.New("table missing")})
	got := s.MetricCatalogFor(context.Background(), Scope{Profile: ProfileCounter})
	if len(got) == 0 {
		t.Fatal("an unreadable curation table must not take away yesterday's composer")
	}
	for _, m := range got {
		if MetricByID[m.ID].Class == ClassCost {
			t.Fatalf("...and it must not hand out a cost metric on the way: %v", m.ID)
		}
	}
}

// Every catalog entry must be internally coherent: at least one dimension, at least one shape, and
// every one of them from the closed sets. This is the drift gate for the catalog itself.
func TestEveryCatalogMetricIsWellFormed(t *testing.T) {
	dims := map[string]bool{DimBranch: true, DimStatus: true, DimBucket: true}
	shapes := map[string]bool{ShapeBar: true, ShapeSplit: true, ShapeList: true}
	classes := map[string]bool{ClassOperational: true, ClassRevenue: true, ClassCost: true}
	for _, m := range MetricCatalog {
		if m.ID == "" || m.Label == "" || m.Module == "" || m.Lead == "" {
			t.Fatalf("catalog entry is missing a required field: %+v", m)
		}
		if !classes[m.Class] {
			t.Fatalf("metric %q has an unknown class %q", m.ID, m.Class)
		}
		if len(m.Dimensions) == 0 || len(m.Shapes) == 0 {
			t.Fatalf("metric %q offers no dimension or no shape", m.ID)
		}
		for _, d := range m.Dimensions {
			if !dims[d] {
				t.Fatalf("metric %q offers unknown dimension %q", m.ID, d)
			}
		}
		for _, sh := range m.Shapes {
			if !shapes[sh] {
				t.Fatalf("metric %q offers unknown shape %q", m.ID, sh)
			}
		}
		// Every composition this metric offers must survive a round trip through the stored id,
		// and must fit the 0172 item-id alphabet and its 64-character bound.
		for _, d := range m.Dimensions {
			for _, sh := range m.Shapes {
				id := ComposeID(m.ID, d, sh)
				if len(id) > 64 {
					t.Fatalf("composed id %q exceeds the substrate's 64-character item id", id)
				}
				for _, r := range id {
					ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_'
					if !ok {
						t.Fatalf("composed id %q leaves the substrate's alphabet at %q", id, r)
					}
				}
				if _, parsed := ParseComposition(id); !parsed {
					t.Fatalf("composed id %q does not parse back", id)
				}
			}
		}
	}
}

// The metric ids the migration seeds must all exist in this build's catalog, the 0149 drift gate,
// applied to the metric menu.
func TestSeededCurationNamesOnlyRealMetrics(t *testing.T) {
	for _, id := range seededMetricIDs(t) {
		if _, ok := MetricByID[id]; !ok {
			t.Fatalf("0182 seeds metric %q which this build does not have", id)
		}
	}
}

// seededMetricIDs reads the ids 0182 seeds. Reading the MIGRATION rather than restating its list is
// the 0149 drift gate's own idiom (app/src/dashboard-nav.test.mjs does the same to the tab seed):
// a test that carries its own copy of the data proves only that the copy is consistent.
func seededMetricIDs(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile("../../db/migrations/0182_home_metric_catalog.sql")
	if err != nil {
		t.Fatalf("0182 must be readable from the package: %v", err)
	}
	re := regexp.MustCompile(`\(\s*'([a-z0-9._]+)'\s*,\s*\d+\s*,\s*(?:true|false)\s*\)`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		t.Fatal("0182 seeds no metric ids, the drift gate would pass vacuously")
	}
	return out
}
