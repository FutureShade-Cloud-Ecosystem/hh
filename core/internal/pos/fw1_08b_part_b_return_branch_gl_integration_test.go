//go:build integration

package pos

// fw1_08b_part_b_return_branch_gl_integration_test.go is FW1-08b part B's integration leg: a
// real POS return rung at Kingston on a sale rung at Trenton, and the GL journals that follow.
//
// The leg runs the real composition: the real pos service over the real tables, the real outbox
// rows the return wrote, and the real GL posting handlers over them. The claim of the wave is
// the claim under test: every journal line carries a branch, and the return's own journals carry
// the branch the return was rung at (Kingston), even though the sale was rung at Trenton.

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/google/uuid"
)

// fw1PartBOutbox is the leg's own outbox store: it serves the events the leg names, read back
// from the real events_outbox, and nothing else. The real PgOutboxStore would claim every
// pending row on the shared gate database, including the world's other traffic.
type fw1PartBOutbox struct {
	events []notify.Event
	served int
}

func (o *fw1PartBOutbox) ProcessBatch(ctx context.Context, n int, settle func(context.Context, notify.Event) notify.Settlement) (int, error) {
	processed := 0
	for o.served < len(o.events) && processed < n {
		settle(ctx, o.events[o.served])
		o.served++
		processed++
	}
	return processed, nil
}

func TestIntegration_FW1_08b_PartB_ARealReturnRungAtKingstonOnATrentonSaleStampsEveryJournalLine(t *testing.T) {
	s := newTrentonStack(t)
	ctx := context.Background()
	customer := plantCreditCustomer(t, s, "FW1 08b part b return")

	// The sale is rung at Trenton, by the Trenton clerk.
	sale := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", customer, "", 5, TenderCash)

	// The Kingston clerk holds a grant on Trenton too, so the Trenton sale is inside their reach
	// and the return may be rung on it. The branch the return is RUNG at is the one the body
	// names: Kingston, where the refund leaves the drawer and the stock comes back to.
	//
	// The grant is this leg's own row and it goes away with the leg: the shared gate database
	// outlives the test, and a clerk who can reach Trenton would read Trenton's approvers (a
	// wall another leg proves).
	var orgID string
	if err := s.pool.QueryRow(ctx, `SELECT id::text FROM org WHERE slug = $1`, demoOrgSlug).Scan(&orgID); err != nil {
		t.Fatalf("the org anchor: %v", err)
	}
	var hadTrenton bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM user_branch_grants WHERE user_id = $1::uuid AND branch_id = $2::uuid)`,
		kClerkUserID, trentonBranchID).Scan(&hadTrenton); err != nil {
		t.Fatalf("read the clerk's branch grants: %v", err)
	}
	if !hadTrenton {
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO user_branch_grants (user_id, branch_id, org_id)
			VALUES ($1::uuid, $2::uuid, $3::uuid)`,
			kClerkUserID, trentonBranchID, orgID); err != nil {
			t.Fatalf("grant the Kingston clerk the Trenton reach: %v", err)
		}
		t.Cleanup(func() {
			_, _ = s.pool.Exec(context.Background(), `
				DELETE FROM user_branch_grants WHERE user_id = $1::uuid AND branch_id = $2::uuid`,
				kClerkUserID, trentonBranchID)
		})
	}

	var lineID string
	if err := s.pool.QueryRow(ctx,
		`SELECT id::text FROM order_lines WHERE order_id = $1::uuid ORDER BY line_no LIMIT 1`, sale.OrderID,
	).Scan(&lineID); err != nil {
		t.Fatalf("the sale's line: %v", err)
	}
	body := map[string]any{
		"idempotency_key": key(),
		"order_id":        sale.OrderID,
		"branch_id":       kingstonBranchID,
		"refund_method":   RefundCash,
		"till_session_id": s.kingstonTill,
		"restock":         false,
		"lines":           []map[string]any{{"order_line_id": lineID, "qty_base": 1}},
	}
	rec := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(kingstonClerkSub), "", body)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("ring the return at Kingston: %d %s", rec.Code, rec.Body.String())
	}

	// The return's own row names the branch it was rung at.
	var returnID, returnBranch string
	if err := s.pool.QueryRow(ctx, `
		SELECT id::text, branch_id::text FROM pos_returns
		 WHERE order_id = $1::uuid ORDER BY created_at DESC LIMIT 1`, sale.OrderID,
	).Scan(&returnID, &returnBranch); err != nil {
		t.Fatalf("the return row: %v", err)
	}
	if returnBranch != kingstonBranchID {
		t.Fatalf("pos_returns.branch_id = %q, want the rung branch %q", returnBranch, kingstonBranchID)
	}

	// The real outbox rows the return wrote, read back off the table.
	rows, err := s.pool.Query(ctx, `
		SELECT topic, payload::text FROM events_outbox
		 WHERE topic IN ('pos.return.committed', 'order.returned', 'pos.return.written_off')
		   AND payload->>'return_id' = $1
		 ORDER BY occurred_at`, returnID)
	if err != nil {
		t.Fatalf("read the return's outbox rows: %v", err)
	}
	defer rows.Close()
	out := &fw1PartBOutbox{}
	for rows.Next() {
		var topic, payload string
		if err := rows.Scan(&topic, &payload); err != nil {
			t.Fatalf("scan an outbox row: %v", err)
		}
		out.events = append(out.events, notify.Event{EventID: uuid.NewString(), Topic: topic, Payload: []byte(payload)})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("the return's outbox rows: %v", err)
	}
	if len(out.events) == 0 {
		t.Fatalf("the return wrote no outbox row the GL could post")
	}

	// The GL takes them off the outbox through its real posting handlers.
	invSvc := inventory.NewService(inventory.NewRepository(s.pool))
	disp := notify.NewDispatcher(out, slog.Default())
	gl.NewService(gl.NewRepository(s.pool)).WithCostBasis(invSvc).RegisterPostingHandlers(disp)
	want := len(out.events)
	drained := 0
	for pass := 0; pass < 8; pass++ {
		n := disp.DrainOnce(ctx)
		drained += n
		if n == 0 {
			break
		}
	}
	if drained != want {
		t.Fatalf("the GL drained %d of the return's %d events", drained, want)
	}

	// Every journal the return wrote carries Kingston on every line, and no journal line in the
	// box is left with no branch.
	journals := 0
	for _, topic := range []string{"pos.return.committed", "order.returned", "pos.return.written_off"} {
		var journalID string
		err := s.pool.QueryRow(ctx, `SELECT id::text FROM gl_journals WHERE idempotency_key = $1`,
			topic+":"+returnID).Scan(&journalID)
		if err != nil {
			continue // this return's event booked nothing, which is its own answer
		}
		journals++
		var headerBranch string
		var total, matching int
		if err := s.pool.QueryRow(ctx, `
			SELECT COALESCE(j.branch_id::text, ''), COUNT(l.id),
			       COUNT(l.id) FILTER (WHERE l.branch_id = $2::uuid)
			  FROM gl_journals j LEFT JOIN gl_journal_lines l ON l.journal_id = j.id
			 WHERE j.id = $1::uuid
			 GROUP BY j.id, j.branch_id`, journalID, kingstonBranchID,
		).Scan(&headerBranch, &total, &matching); err != nil {
			t.Fatalf("read the journal %s: %v", journalID, err)
		}
		if headerBranch != kingstonBranchID {
			t.Errorf("journal %s (%s) header branch = %q, want the rung branch %q", journalID, topic, headerBranch, kingstonBranchID)
		}
		if total == 0 || matching != total {
			t.Errorf("journal %s (%s) lines carrying %s = %d of %d", journalID, topic, kingstonBranchID, matching, total)
		}
	}
	if journals == 0 {
		t.Fatalf("the return rung at Kingston booked no journal")
	}

	var nullLines int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM gl_journal_lines WHERE branch_id IS NULL`).Scan(&nullLines); err != nil {
		t.Fatalf("count the branchless journal lines: %v", err)
	}
	if nullLines != 0 {
		t.Errorf("the box carries %d journal lines with no branch", nullLines)
	}
}
