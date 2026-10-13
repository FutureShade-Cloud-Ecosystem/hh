//go:build integration

// syncpick_text_integration_test.go: the text legs of a pick record are client text. A control character
// in any of them, or a description cut inside a character, must never reach the database as an
// operational fault: through the outbox the text cannot change, so the op would stall the scope. The
// door refuses the op (rejected, in words) before the order is told anything, and the record body cuts
// a long description on a character boundary.
package tasks

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// Text a picker can type on several lines (the Yard note is a textarea; the description is copied from the
// ticket line) keeps tab, LF and CR; every other C0 control, DEL and the bidi controls are refused in every
// text leg, and the single line legs refuse tab, LF and CR too.
var pickAlwaysBad = map[string]string{
	"NUL":                     "dock\x00two",
	"bell":                    "dock\x07two",
	"escape":                  "dock\x1btwo",
	"unit separator":          "dock\x1ftwo",
	"DEL":                     "dock\x7ftwo",
	"right to left override":  "dock\u202etwo",
	"left to right isolate":   "dock\u2066two",
	"pop directional isolate": "dock\u2069two",
}

var pickLayout = map[string]string{"newline": "dock\ntwo", "carriage return": "dock\r\ntwo", "tab": "dock\ttwo"}

func TestPickDoor_ControlCharactersInAnyTextLegAreRejectedBeforeTheOrderMoves(t *testing.T) {
	multiline := map[string]bool{"description": true, "short_note": true}
	legs := []string{"product_id", "sku", "description", "qty_ordered", "qty_picked", "uom", "short_reason", "short_note"}
	for _, leg := range legs {
		cases := map[string]string{}
		for k, v := range pickAlwaysBad {
			cases[k] = v
		}
		if !multiline[leg] {
			for k, v := range pickLayout {
				cases[k] = v
			}
		}
		for name, text := range cases {
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

func TestPickDoor_NoteAndDescriptionKeepTabNewlineAndCarriageReturn(t *testing.T) {
	for name, text := range pickLayout {
		t.Run(name, func(t *testing.T) {
			d := newPickDoor(t)
			id := d.task(d.branchA)
			l := pickLine(1)
			l["short_note"], l["description"] = "first\n"+text, "ticket "+text
			res := d.push(recordOp(uuid.NewString(), id, l))
			if res[0].Status != "applied" {
				t.Fatalf("an ordinary multi line note is a record, not a refusal: %+v (%v)", res[0], res[0].Error)
			}
			var note, desc string
			if err := d.pool.QueryRow(context.Background(),
				`SELECT short_note, description FROM task_pick_lines WHERE task_id = $1::uuid AND order_line_no = 1`, id).Scan(&note, &desc); err != nil {
				t.Fatalf("read: %v", err)
			}
			if note != "first\n"+text || !strings.HasPrefix(desc, "ticket") {
				t.Fatalf("stored verbatim: %q %q", note, desc)
			}
		})
	}
}

// The direct door shares the rule through RecordPick: a refusal is a 400 BadRequest before the order is
// told, where a NUL used to be a 500 after ApplyPicks had moved the order.
func TestRecordPick_DirectDoorRefusesTheSameTextBeforeTheOrderMoves(t *testing.T) {
	for name, text := range pickAlwaysBad {
		for _, leg := range []string{"description", "short_note", "sku", "uom"} {
			t.Run(leg+" "+name, func(t *testing.T) {
				d := newPickDoor(t)
				id := d.task(d.branchA)
				in := PickedLineInput{OrderLineNo: 1, QtyOrdered: "10", QtyPicked: "7", UOM: "EA", ShortReason: ShortReasonNotInYard}
				switch leg {
				case "description":
					in.Description = text
				case "short_note":
					in.ShortNote = text
				case "sku":
					in.SKU = text
				case "uom":
					in.UOM = text
				}
				_, err := d.svc.RecordPick(context.Background(), id, d.user, []PickedLineInput{in})
				var bad httpx.BadRequestError
				if !errors.As(err, &bad) {
					t.Fatalf("want the module's BadRequest, got %v", err)
				}
				if len(d.yard.picks) != 0 || len(d.lines(id)) != 0 {
					t.Fatalf("a refused record moves nothing: order told %d times", len(d.yard.picks))
				}
			})
		}
	}
	d := newPickDoor(t)
	id := d.task(d.branchA)
	in := PickedLineInput{OrderLineNo: 1, QtyOrdered: "10", QtyPicked: "7", UOM: "EA", ShortReason: ShortReasonNotInYard, ShortNote: "a\nb\r\nc\td"}
	if _, err := d.svc.RecordPick(context.Background(), id, d.user, []PickedLineInput{in}); err != nil {
		t.Fatalf("the direct door keeps a multi line note: %v", err)
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
