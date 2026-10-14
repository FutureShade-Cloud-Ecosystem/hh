package tasks

// syncpick_test.go: the pick record door of the pick_tasks sync scope (Field CP-2). The payload is
// {id, picked: [line, ...]}; each line carries exactly the legs the direct door's body carries
// (PUT /api/v1/yard/picks/{id}/lines), the op carries nothing else, and the door fails closed
// without its two seams.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

func goodLine() map[string]any {
	return map[string]any{
		"order_line_no": float64(1),
		"product_id":    "",
		"sku":           "SKU-1",
		"description":   "Armour stone",
		"qty_ordered":   "10",
		"qty_picked":    "7",
		"uom":           "EA",
		"short_reason":  ShortReasonNotInYard,
		"short_note":    "",
	}
}

func TestParsePickedLines_AdmitsTheDirectDoorsLegs(t *testing.T) {
	got, err := parsePickedLines([]any{goodLine()})
	if err != nil {
		t.Fatalf("a line with the direct door's nine legs must parse: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want one line, got %d", len(got))
	}
	l := got[0]
	if l.OrderLineNo != 1 || l.SKU != "SKU-1" || l.Description != "Armour stone" || l.QtyOrdered != "10" ||
		l.QtyPicked != "7" || l.UOM != "EA" || l.ShortReason != ShortReasonNotInYard {
		t.Fatalf("legs did not land on the input: %+v", l)
	}
}

func TestParsePickedLines_Refusals(t *testing.T) {
	withLeg := func(k string, v any) map[string]any {
		l := goodLine()
		l[k] = v
		return l
	}
	cases := map[string]any{
		"not an array":         "lines",
		"empty":                []any{},
		"not an object":        []any{"x"},
		"unknown leg":          []any{withLeg("picked_at", "a client stamp")},
		"client row id":        []any{withLeg("id", "00000000-0000-4000-8000-000000000001")},
		"task id":              []any{withLeg("task_id", "00000000-0000-4000-8000-000000000001")},
		"line number string":   []any{withLeg("order_line_no", "1")},
		"line number fraction": []any{withLeg("order_line_no", 1.5)},
		"quantity as number":   []any{withLeg("qty_picked", 7.0)},
		"description as list":  []any{withLeg("description", []any{"x"})},
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parsePickedLines(raw)
			if err == nil {
				t.Fatalf("%s must refuse", name)
			}
			if !errors.Is(err, sync.ErrOpRefused) {
				t.Fatalf("%s must be an op refusal, got %v", name, err)
			}
		})
	}
	big := make([]any, 501)
	for i := range big {
		l := goodLine()
		l["order_line_no"] = float64(i + 1)
		big[i] = l
	}
	if _, err := parsePickedLines(big); err == nil || !errors.Is(err, sync.ErrOpRefused) {
		t.Fatalf("more than 500 lines must refuse as the direct door does, got %v", err)
	}
}

func TestCheckPickedOnly_TheOpCarriesNothingElse(t *testing.T) {
	if err := checkPickedOnly(map[string]any{"id": "x", "picked": []any{}}); err != nil {
		t.Fatalf("id and picked is the whole payload: %v", err)
	}
	for _, extra := range []string{"status", "photo_url", "stage_location", "stage_ref", "order_id"} {
		err := checkPickedOnly(map[string]any{"id": "x", "picked": []any{}, extra: "v"})
		if err == nil || !errors.Is(err, sync.ErrOpRefused) {
			t.Fatalf("picked beside %q must refuse the op whole, got %v", extra, err)
		}
	}
}

func TestSyncWritableSetNowAdmitsPicked(t *testing.T) {
	if err := checkWritable(map[string]any{"id": "x", "picked": []any{}}); err != nil {
		t.Fatalf("picked is in the writable set: %v", err)
	}
	if err := checkWritable(map[string]any{"id": "x", "skid_count": float64(2)}); err == nil {
		t.Fatal("the skid count stays a direct door, it must still refuse")
	}
}

func TestPickDoorFailsClosedWithoutItsSeams(t *testing.T) {
	f := sync.ScopeFilter{OrgID: "00000000-0000-4000-8000-0000000000aa", Actor: "sub"}
	fields := map[string]any{"id": "00000000-0000-4000-8000-0000000000bb", "picked": []any{goodLine()}}
	for name, s := range map[string]*SyncStore{
		"neither seam": NewSyncStore(nil, nil, nil),
		"no authority": NewSyncStore(nil, nil, nil).WithPickRecorder(&noopRecorder{}),
	} {
		_, err := s.Write(context.Background(), f, "00000000-0000-4000-8000-0000000000bb", "00000000-0000-4000-8000-0000000000cc", fields)
		if err == nil || !errors.Is(err, sync.ErrOpRefused) {
			t.Fatalf("%s: the door must refuse, never record unfenced: %v", name, err)
		}
		if !strings.Contains(err.Error(), "pick record") {
			t.Fatalf("%s: the refusal must say which door: %v", name, err)
		}
	}
}

type noopRecorder struct{ calls int }

func (n *noopRecorder) RecordPick(context.Context, string, string, []PickedLineInput) ([]PickedLine, error) {
	n.calls++
	return nil, nil
}
