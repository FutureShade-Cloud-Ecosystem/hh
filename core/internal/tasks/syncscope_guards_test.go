package tasks

import (
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// The untagged legs for the two yard-pick adapters' pure guards: the
// writable field sets (the mutation checks target these), the report
// validation the module's own FlagShortage bounds shape, and the loose
// JSON number leg. The database-backed batteries live in the sync
// package's integration suite.

func TestShortageWritableSets(t *testing.T) {
	// A create declares the report itself.
	if err := shortageWritableCreate(map[string]any{
		"task_id": "x", "description": "d", "qty_short": "1", "uom": "EA",
		"order_line_no": 1, "product_id": "p", "sku": "s", "note": "n", "id": "i",
	}); err != nil {
		t.Fatalf("a full valid create set: %v", err)
	}
	for _, field := range []string{"resolved_at", "resolved_by", "resolution", "reported_by", "reported_at", "status"} {
		err := shortageWritableCreate(map[string]any{field: "x"})
		if !errors.Is(err, sync.ErrOpRefused) {
			t.Fatalf("create field %q: %v, want the classified refusal", field, err)
		}
	}
	// An update declares the correctable report legs only: the
	// resolution legs are the counter's act and refuse.
	if err := shortageCheckWritable(map[string]any{"id": "i", "qty_short": "2", "uom": "EA", "sku": "s", "description": "d", "note": "n"}); err != nil {
		t.Fatalf("a full valid update set: %v", err)
	}
	for _, field := range []string{"resolution", "resolved_at", "resolved_by", "task_id", "product_id", "order_line_no", "reported_by"} {
		err := shortageCheckWritable(map[string]any{field: "x"})
		if !errors.Is(err, sync.ErrOpRefused) {
			t.Fatalf("update field %q: %v, want the classified refusal", field, err)
		}
	}
}

func TestShortageValidateReport(t *testing.T) {
	desc, qty, uom, _, _, _, err := shortageValidateReport(map[string]any{
		"description": "short two bundles", "qty_short": "2.5",
	})
	if err != nil {
		t.Fatalf("the minimal valid report: %v", err)
	}
	if desc != "short two bundles" || qty != "2.5000" || uom != "EA" {
		t.Fatalf("defaults: desc %q qty %q uom %q", desc, qty, uom)
	}
	// Description required, exactly as FlagShortage rules.
	if _, _, _, _, _, _, err := shortageValidateReport(map[string]any{"qty_short": "1"}); !errors.Is(err, sync.ErrOpRefused) {
		t.Fatalf("missing description: %v", err)
	}
	// A non-positive or non-decimal quantity refuses.
	for _, bad := range []string{"0", "-1", "abc", ""} {
		fields := map[string]any{"description": "d", "qty_short": bad}
		if _, _, _, _, _, _, err := shortageValidateReport(fields); !errors.Is(err, sync.ErrOpRefused) {
			t.Fatalf("qty_short %q: %v, want the classified refusal", bad, err)
		}
	}
	// The bounded legs.
	if _, _, _, _, _, _, err := shortageValidateReport(map[string]any{
		"description": strings.Repeat("x", 401), "qty_short": "1",
	}); !errors.Is(err, sync.ErrOpRefused) {
		t.Fatal("an over-long description must refuse")
	}
	if _, _, _, _, _, _, err := shortageValidateReport(map[string]any{
		"description": "d", "qty_short": "1", "order_line_no": 0,
	}); !errors.Is(err, sync.ErrOpRefused) {
		t.Fatal("a non-positive order_line_no must refuse")
	}
	if _, _, _, _, _, _, err := shortageValidateReport(map[string]any{
		"description": "d", "qty_short": "1", "product_id": "not-a-uuid",
	}); !errors.Is(err, sync.ErrOpRefused) {
		t.Fatal("a non-uuid product_id must refuse")
	}
}

func TestWeightWritableSets(t *testing.T) {
	if err := weightWritableCreate(map[string]any{
		"order_id": "o", "order_line_no": 1, "weight_uom": "TONNE", "id": "i",
	}); err != nil {
		t.Fatalf("a full valid create set: %v", err)
	}
	// A create cannot declare the weight or the status: the birth row is
	// QUEUED, exactly as the pick adapter's rule holds. Priority refuses
	// too: RequestWeight rules the birth row HIGH, not negotiable.
	for _, field := range []string{"weight_entered", "status", "photo_url", "branch_id", "source", "priority"} {
		err := weightWritableCreate(map[string]any{field: "x"})
		if !errors.Is(err, sync.ErrOpRefused) {
			t.Fatalf("create field %q: %v, want the classified refusal", field, err)
		}
	}
	for _, field := range []string{"status", "weight_entered", "weight_uom", "photo_url", "id"} {
		if !weightWritable[field] {
			t.Fatalf("update field %q must be writable", field)
		}
	}
	for _, field := range []string{"priority", "order_id", "order_line_no", "stage_location", "claimed_by"} {
		if weightWritable[field] {
			t.Fatalf("update field %q must refuse", field)
		}
	}
}

func TestNumericLeg(t *testing.T) {
	for _, c := range []struct {
		raw  any
		want int
		ok   bool
	}{
		{3, 3, true}, {int64(3), 3, true}, {float64(3), 3, true}, {"3", 3, true},
		{2.5, 0, false}, {"x", 0, false}, {nil, 0, false},
	} {
		got, ok := numericLeg(c.raw)
		if ok != c.ok || got != c.want {
			t.Fatalf("numericLeg(%v) = %d,%v want %d,%v", c.raw, got, ok, c.want, c.ok)
		}
	}
}

// TestSeqScopeNames: the two counters the tasks table draws from are the
// manifest's own scope names, the 0213 convention's one-counter-row-per-
// scope rule; the adapters' and the repository's draws name these
// constants and nothing else.
func TestSeqScopeNames(t *testing.T) {
	if pickTasksSeqScope != "pick_tasks" {
		t.Fatalf("pick counter %q", pickTasksSeqScope)
	}
	if weightsSeqScope != "weights" {
		t.Fatalf("weights counter %q", weightsSeqScope)
	}
	if shortagesSeqScope != "shortages" {
		t.Fatalf("shortages counter %q", shortagesSeqScope)
	}
}
