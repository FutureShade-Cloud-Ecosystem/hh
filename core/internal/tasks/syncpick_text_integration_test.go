//go:build integration

// syncpick_text_integration_test.go: the text legs of a pick record are client text. A control character
// in any of them, or a description cut inside a character, must never reach the database as an
// operational fault: through the outbox the text cannot change, so the op would stall the scope. The
// door refuses the op (rejected, in words) before the order is told anything, and the record body cuts
// a long description on a character boundary.
package tasks

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"
)

func TestPickDoor_ControlCharactersInAnyTextLegAreRejectedBeforeTheOrderMoves(t *testing.T) {
	bad := map[string]string{
		"NUL":                    "dock\x00two",
		"newline":                "dock\ntwo",
		"bell":                   "dock\x07two",
		"DEL":                    "dock\x7ftwo",
		"right to left override": "dock‮two",
		"left to right isolate":  "dock⁦two",
	}
	legs := []string{"product_id", "sku", "description", "qty_ordered", "qty_picked", "uom", "short_reason", "short_note"}
	for _, leg := range legs {
		for name, text := range bad {
			t.Run(leg+" "+name, func(t *testing.T) {
				d := newPickDoor(t)
				id := d.task(d.branchA)
				l := pickLine(1)
				l[leg] = text
				res := d.push(recordOp(uuid.NewString(), id, l))
				if res[0].Status != "rejected" || res[0].Error == nil || *res[0].Error == "" {
					t.Fatalf("want rejected with words, got %+v", res[0])
				}
				if len(d.yard.picks) != 0 || len(d.lines(id)) != 0 {
					t.Fatalf("a refused record moves nothing: order told %d times, %d lines stored", len(d.yard.picks), len(d.lines(id)))
				}
			})
		}
	}
}

func TestPickDoor_LongDescriptionIsCutOnACharacterBoundary(t *testing.T) {
	d := newPickDoor(t)
	id := d.task(d.branchA)
	l := pickLine(1)
	l["description"] = strings.Repeat("a", 399) + "é"
	res := d.push(recordOp(uuid.NewString(), id, l))
	if res[0].Status != "applied" {
		t.Fatalf("a long description is cut, never a fault: %+v", res[0])
	}
	var desc string
	if err := d.pool.QueryRow(context.Background(), `SELECT description FROM task_pick_lines WHERE task_id = $1::uuid AND order_line_no = 1`, id).Scan(&desc); err != nil {
		t.Fatalf("read description: %v", err)
	}
	if !utf8.ValidString(desc) || len(desc) > 400 || len(desc) < 399 {
		t.Fatalf("want 399 valid bytes cut before the split letter, got %d bytes valid=%v", len(desc), utf8.ValidString(desc))
	}
}

func TestRecordPick_DirectDoorCutsOnACharacterBoundaryToo(t *testing.T) {
	d := newPickDoor(t)
	id := d.task(d.branchA)
	in := []PickedLineInput{{OrderLineNo: 1, Description: "a" + strings.Repeat("é", 300), QtyOrdered: "10", QtyPicked: "10", UOM: "EA"}}
	got, err := d.svc.RecordPick(context.Background(), id, "", in)
	if err != nil {
		t.Fatalf("direct door: %v", err)
	}
	if len(got) != 1 || !utf8.ValidString(got[0].Description) || len(got[0].Description) != 399 {
		t.Fatalf("want 399 valid bytes (the letter that straddles byte 400 is dropped), got %d valid=%v", len(got[0].Description), utf8.ValidString(got[0].Description))
	}
}
