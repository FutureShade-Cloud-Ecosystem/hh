//go:build integration

package pos

// fw113c_note_one_instant_integration_test.go, FW1-13c: the across-orders note is ONE instant.
//
// The single return reads the pos clock once and that one instant reaches the REFUNDED payments
// row the till writes beside it, the drawer movement and the return's own events (one clock rules
// the transaction, FW1-13b). The across-orders note path calls that same commit half once per
// child, so before this fix it read the clock once per child: an N-child note carried N instants
// inside one transaction, one per child, and the rule's own wording held per child and not per
// note.
//
// The leg rings one note over two orders with a clock that advances on every read, and proves the
// note's one instant reaches every child's rows and every child's event.

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// TestIntegration_FW113C_TheAcrossOrdersNoteCarriesOneInstant is the red leg. Before the fix the
// note read the clock once per child, so the second child's rows and event carried an instant a
// minute after the first child's.
func TestIntegration_FW113C_TheAcrossOrdersNoteCarriesOneInstant(t *testing.T) {
	s := newTrentonStack(t)
	ctx := context.Background()
	customer := plantCreditCustomer(t, s, "Across orders one instant proof")
	sale1 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", customer, "", 5, TenderCash)
	sale2 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", customer, "", 5, TenderCash)

	// The pos service's clock, answering a minute later on every read. A clock that stands still
	// would hide the defect: the children would share the instant by accident.
	base := time.Date(2026, time.April, 15, 12, 0, 0, 0, time.UTC)
	var reads int
	s.svc.WithClock(func() time.Time {
		reads++
		return base.Add(time.Duration(reads-1) * time.Minute)
	})

	rec := s.postNote(trentonClerkSub, "",
		s.noteBody(key(), customer, RefundCash, []trentonSaleView{sale1, sale2}, []float64{5, 5}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("the two-order note must commit, got %d: %s", rec.Code, rec.Body.String())
	}
	note := s.decodeBody(rec)
	children, _ := note["returns"].([]any)
	if len(children) != 2 {
		t.Fatalf("the note answers its two children: %v", note)
	}
	var returnIDs, returnNos []string
	for _, c := range children {
		cm, _ := c.(map[string]any)
		returnIDs = append(returnIDs, cm["return_id"].(string))
		returnNos = append(returnNos, cm["return_no"].(string))
	}

	// The note's one instant: the first instant the clock answered inside the note.
	wantAt := base

	// Every child's REFUNDED payments row, the row the till writes beside the return.
	rows, err := s.pool.Query(ctx,
		`SELECT r.return_no, p.created_at FROM pos_returns r JOIN payments p ON p.id = r.refund_payment_id
		  WHERE r.id = ANY($1::uuid[]) ORDER BY r.return_no`, returnIDs)
	if err != nil {
		t.Fatalf("read the children's payments rows: %v", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var no string
		var at time.Time
		if err := rows.Scan(&no, &at); err != nil {
			t.Fatalf("scan the child's payments row: %v", err)
		}
		seen++
		if !at.Equal(wantAt) {
			t.Fatalf("the child's %s payments row is stamped %s, want the note's one instant %s", no, at, wantAt)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the children's payments rows: %v", err)
	}
	if seen != 2 {
		t.Fatalf("the note wrote %d refunded payments rows, want one per child", seen)
	}

	// Every child's drawer movement, the row the drawer reads its own day from.
	for _, no := range returnNos {
		var at time.Time
		if err := s.pool.QueryRow(ctx,
			`SELECT created_at FROM till_movements WHERE till_session_id = $1::uuid AND reference = $2`,
			s.trentonTill, no).Scan(&at); err != nil {
			t.Fatalf("read the child's movement row: %v", err)
		}
		if !at.Equal(wantAt) {
			t.Fatalf("the child's %s movement row is stamped %s, want the note's one instant %s", no, at, wantAt)
		}
	}

	// Every child's own event, the instant the GL journal follows.
	for _, id := range returnIDs {
		var text string
		if err := s.pool.QueryRow(ctx,
			`SELECT payload->>'occurred_at' FROM events_outbox
			  WHERE topic = 'pos.return.committed' AND payload->>'return_id' = $1`, id).Scan(&text); err != nil {
			t.Fatalf("read the child's event: %v", err)
		}
		at, perr := time.Parse(time.RFC3339Nano, text)
		if perr != nil {
			t.Fatalf("read the child's event instant %q: %v", text, perr)
		}
		if !at.Equal(wantAt) {
			t.Fatalf("the child's %s event carries %s, want the note's one instant %s", id, at, wantAt)
		}
	}

	// The cause, named: the note read the clock once per child, not once for the whole note.
	if reads != 1 {
		t.Fatalf("the note read the clock %d times, want once for the whole note", reads)
	}

	// The note's own rows leave no residue in the books: its events and any journal a later
	// consumer would post from them are cleaned up by their own keys.
	t.Cleanup(func() {
		c := context.Background()
		for _, id := range returnIDs {
			for _, key := range []string{"pos.return.committed:" + id, "order.returned:" + id, "pos.return.written_off:" + id} {
				_, _ = s.pool.Exec(c, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key = $1)`, key)
				_, _ = s.pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key = $1`, key)
			}
			_, _ = s.pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'return_id' = $1`, id)
		}
		_, _ = s.pool.Exec(c, `DELETE FROM till_movements WHERE till_session_id = $1::uuid AND reference = ANY($2)`, s.trentonTill, returnNos)
	})
}
