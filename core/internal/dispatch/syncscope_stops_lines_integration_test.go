//go:build integration

// syncscope_stops_lines_integration_test.go: the driver's stops pull carries
// what comes off the truck at each stop (UX-61). The REST my-runs read has
// always carried it as stop_lines; the sync projection did not, so a synced
// or offline HH Drive showed a stop with no load list. The lines ride the
// stop row's own data under the stop's own fence: the calling driver's stops
// only, products and quantities with units, and never a money member (the
// DriverLine type has none).
package dispatch

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// loadListLines is an OrderLineReader double answering the same two lines
// for every order, carrying a yard Location the driver has no use for.
type loadListLines struct{}

func (loadListLines) OrderPickLines(context.Context, string) ([]PickFeedLine, error) {
	return []PickFeedLine{
		{LineNo: 1, ProductID: "p-1", SKU: "LL-PAV-601", Description: "Brown pavers 60mm", QtyOrdered: "48.000", UOM: "EA", Location: "A-12"},
		{LineNo: 2, ProductID: "p-2", SKU: "LL-AGG-602", Description: "Screened sand", QtyOrdered: "2.500", UOM: "TONNE", Location: "YARD-3"},
	}, nil
}

// pullStopData pulls the stops scope under a filter and returns each
// entity's data by stop id.
func pullStopData(t *testing.T, w *fenceWorld, f sync.ScopeFilter) map[string]map[string]any {
	t.Helper()
	info, ok := sync.Lookup("stops")
	if !ok {
		t.Fatal("stops scope missing from the registry")
	}
	resp, err := w.engine.Pull(context.Background(), info, f, 0, 200)
	if err != nil {
		t.Fatalf("pull stops: %v", err)
	}
	out := map[string]map[string]any{}
	for _, e := range resp.Items {
		out[e.Id] = e.Data
	}
	return out
}

func TestIntegration_StopsPull_DriverCarriesTheStopsLoadList(t *testing.T) {
	w := newFenceWorld(t)
	w.stops.WithOrderLines(loadListLines{})

	data := pullStopData(t, w, w.driverA)[w.stopA]
	if data == nil {
		t.Fatalf("driver A's stops pull is missing their own stop %s", w.stopA)
	}
	raw, ok := data["lines"].([]any)
	if !ok || len(raw) != 2 {
		t.Fatalf("the driver's stop must carry its two load lines under \"lines\", got %v", data["lines"])
	}
	first, _ := raw[0].(map[string]any)
	for k, want := range map[string]any{
		"line_no": float64(1), "sku": "LL-PAV-601", "description": "Brown pavers 60mm", "qty": "48.000", "uom": "EA",
	} {
		if first[k] != want {
			t.Errorf("first line %s = %v, want %v (line %v)", k, first[k], want, first)
		}
	}

	// The money fence: a driver's stop body carries no price, cost, value or
	// amount word, no cents, and not the yard's bin either.
	body, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal stop data: %v", err)
	}
	assertDriverPayloadJSON(t, body)
}

func TestIntegration_StopsPull_LoadListFollowsTheStopFence(t *testing.T) {
	w := newFenceWorld(t)
	w.stops.WithOrderLines(loadListLines{})

	// A driver never receives a colleague's stop, so never its lines.
	if _, ok := pullStopData(t, w, w.driverA)[w.stopB]; ok {
		t.Errorf("driver A's stops pull carries driver B's stop %s", w.stopB)
	}
	// A desk caller's stops are the dispatcher's board rows: no lines.
	for id, data := range pullStopData(t, w, w.desk) {
		if _, has := data["lines"]; has {
			t.Errorf("the desk caller's stop %s carries a load list; only the driver arm does", id)
		}
	}
	// A driver login with no driver record gets no stops at all.
	if got := pullStopData(t, w, w.bare); len(got) != 0 {
		t.Errorf("an unlinked driver login's stops pull answered %d rows", len(got))
	}
}

// A push echoes the row back; if the echo dropped the lines the device would
// lose its load list the moment the driver marked the stop loaded.
func TestIntegration_StopsWrite_EchoKeepsTheLoadList(t *testing.T) {
	w := newFenceWorld(t)
	w.stops.WithOrderLines(loadListLines{})

	state, err := w.stops.Write(context.Background(), w.driverA, w.stopA, syncFreshKey(), map[string]any{
		"id": w.stopA, "status": StopLoaded,
	})
	if err != nil {
		t.Fatalf("driver A marks their stop loaded: %v", err)
	}
	if raw, ok := state.Data["lines"].([]any); !ok || len(raw) != 2 {
		t.Errorf("the write echo must keep the load list, got %v", state.Data["lines"])
	}
	loaded, found, err := w.stops.Load(context.Background(), w.driverA, w.stopA)
	if err != nil || !found {
		t.Fatalf("load own stop: found=%v err=%v", found, err)
	}
	if raw, ok := loaded.Data["lines"].([]any); !ok || len(raw) != 2 {
		t.Errorf("Load must carry the load list too, got %v", loaded.Data["lines"])
	}
}
