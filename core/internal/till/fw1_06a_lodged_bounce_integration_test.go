//go:build integration

package till

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/bankrec"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

type glExternalPoster struct {
	svc *gl.Service
	ctx context.Context
}

func (p glExternalPoster) PostJournal(_ context.Context, in gl.ExternalJournal) (string, error) {
	return p.svc.PostExternal(p.ctx, in)
}

type singleEventOutbox struct {
	ev       notify.Event
	lastSett notify.Settlement
}

func (s *singleEventOutbox) ProcessBatch(ctx context.Context, _ int, settle func(context.Context, notify.Event) notify.Settlement) (int, error) {
	s.lastSett = settle(ctx, s.ev)
	return 1, nil
}

// TestIntegration_FW1_06a_LodgedChequeBounceCreditsBank proves that when a cheque payment is
// received (Dr 1020 / Cr 1100), lodged from the till and matched to a bank deposit line
// (Dr 1000 / Cr 1020, auto or manual), and subsequently bounces via MarkNSF, the payment.bounced
// journal credits 1000 (bank) rather than 1020 (undeposited funds), leaving both 1020 and 1000
// at a net 0 delta.
func TestIntegration_FW1_06a_LodgedChequeBounceCreditsBank(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	cctx := globalCtx(userID)

	var accountID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM bank_accounts WHERE gl_account_code = '1000' LIMIT 1`).Scan(&accountID); err != nil {
		t.Fatalf("bank account 1000: %v", err)
	}

	tillRepo := NewRepository(pool)
	tillSvc := realSvc(pool)
	glRepo := gl.NewRepository(pool)
	glSvc := gl.NewService(glRepo).WithPaymentReader(tillRepo)
	bankrecSvc := bankrec.NewService(bankrec.NewRepository(pool)).
		WithPayments(tillRepo).
		WithLodgements(tillRepo).
		WithJournalPoster(glExternalPoster{svc: glSvc, ctx: cctx})

	outbox := &singleEventOutbox{}
	disp := notify.NewDispatcher(outbox, nil)
	glSvc.RegisterPostingHandlers(disp)
	deliver := func(t *testing.T, topic string, payload []byte) {
		t.Helper()
		outbox.ev = notify.Event{Topic: topic, Payload: payload}
		disp.DrainOnce(ctx)
		if outbox.lastSett.LastError != "" {
			t.Fatalf("deliver %s: %s", topic, outbox.lastSett.LastError)
		}
	}

	for _, mode := range []string{"auto", "manual"} {
		t.Run(mode, func(t *testing.T) {
			const chequeCents = int64(50000)
			pid, _, _, cleanupNSF := seedNSFCase(t, pool, branchID, chequeCents)
			t.Cleanup(cleanupNSF)

			var sessionID, lodgementID, lineID, matchID string
			t.Cleanup(func() {
				c := context.Background()
				if lineID != "" {
					_, _ = pool.Exec(c, `DELETE FROM bank_matches WHERE statement_line_id = $1::uuid`, lineID)
				}
				if matchID != "" {
					_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key = $1)`, "bankrec:match:"+matchID)
					_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key = $1`, "bankrec:match:"+matchID)
				}
				if lineID != "" {
					_, _ = pool.Exec(c, `DELETE FROM bank_statement_lines WHERE id = $1::uuid`, lineID)
				}
				_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE source_ref_id = $1)`, pid)
				_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE source_ref_id = $1`, pid)
				if sessionID != "" {
					_, _ = pool.Exec(c, `DELETE FROM till_movements WHERE till_session_id = $1::uuid`, sessionID)
					_, _ = pool.Exec(c, `DELETE FROM till_sessions WHERE id = $1::uuid`, sessionID)
				}
			})

			loc, _ := time.LoadLocation("America/Toronto")
			now := time.Now().In(loc)
			year, month := now.Year(), int(now.Month())

			bal1020Before, err := glSvc.GLBalanceCents(cctx, "1020", year, month, "")
			if err != nil {
				t.Fatalf("GLBalanceCents 1020 before: %v", err)
			}
			bal1000Before, err := glSvc.GLBalanceCents(cctx, "1000", year, month, "")
			if err != nil {
				t.Fatalf("GLBalanceCents 1000 before: %v", err)
			}

			// 1. Post payment.received (Dr 1020 / Cr 1100).
			deliver(t, "payment.received", []byte(fmt.Sprintf(`{"payment_id":%q}`, pid)))

			// 2. Open a till session at branchID, record the cheque lodgement, and match the bank deposit line.
			if err := pool.QueryRow(ctx,
				`INSERT INTO till_sessions (branch_id, station, opened_by) VALUES ($1::uuid, $2, $3::uuid) RETURNING id::text`,
				branchID, "FW106A-"+mode, userID).Scan(&sessionID); err != nil {
				t.Fatalf("insert till_session: %v", err)
			}
			ref := fmt.Sprintf("FW106A-LDG-%s-%d", mode, now.UnixNano())
			if err := pool.QueryRow(ctx,
				`INSERT INTO till_movements (till_session_id, movement_type, method, amount_cents, reason, reference, created_by)
				 VALUES ($1::uuid, 'LODGEMENT', 'CHEQUE', $2, 'cheque deposit', $3, $4::uuid) RETURNING id::text`,
				sessionID, chequeCents, ref, userID).Scan(&lodgementID); err != nil {
				t.Fatalf("insert lodgement: %v", err)
			}
			if err := pool.QueryRow(ctx,
				`INSERT INTO bank_statement_lines (bank_account_id, txn_date, description, reference, amount_cents, dedupe_hash, status)
				 VALUES ($1::uuid, $2, 'CHEQUE DEPOSIT', $3, $4, $5, 'UNMATCHED') RETURNING id::text`,
				accountID, now, ref, chequeCents, "dedupe-"+ref).Scan(&lineID); err != nil {
				t.Fatalf("insert bank_statement_line: %v", err)
			}

			if mode == "auto" {
				summary, err := bankrecSvc.AutoMatchReach(cctx, accountID, now.Add(-24*time.Hour), now.Add(24*time.Hour), userID, func(string) bool { return true })
				if err != nil {
					t.Fatalf("AutoMatchReach: %v", err)
				}
				if summary.Matched != 1 {
					t.Fatalf("AutoMatchReach matched = %d, want 1 (lodgement %s)", summary.Matched, lodgementID)
				}
				matchID = summary.Matches[0].ID
			} else {
				m, err := bankrecSvc.ManualMatch(cctx, bankrec.MatchInput{
					Mode:            "manual",
					StatementLineID: lineID,
					TargetType:      bankrec.TargetLodgement,
					TargetRefID:     lodgementID,
					AmountCents:     chequeCents,
					CanAccessBranch: func(string) bool { return true },
				}, userID)
				if err != nil {
					t.Fatalf("ManualMatch: %v", err)
				}
				matchID = m.ID
			}

			// After the lodgement match, 1020 is cleared (delta 0) and 1000 holds the deposit (+50000).
			bal1020Mid, err := glSvc.GLBalanceCents(cctx, "1020", year, month, "")
			if err != nil {
				t.Fatalf("GLBalanceCents 1020 mid: %v", err)
			}
			bal1000Mid, err := glSvc.GLBalanceCents(cctx, "1000", year, month, "")
			if err != nil {
				t.Fatalf("GLBalanceCents 1000 mid: %v", err)
			}
			if bal1020Mid-bal1020Before != 0 || bal1000Mid-bal1000Before != chequeCents {
				t.Fatalf("after lodgement match: GL 1020 delta = %d (want 0), GL 1000 delta = %d (want %d)",
					bal1020Mid-bal1020Before, bal1000Mid-bal1000Before, chequeCents)
			}

			// 3. Bounce the cheque via MarkNSF and consume payment.bounced into GL.
			if _, err := tillSvc.MarkNSF(cctx, NSFInput{PaymentID: pid, Reason: "cheque returned NSF after bank deposit"}); err != nil {
				t.Fatalf("MarkNSF: %v", err)
			}
			var bouncedPayload []byte
			if err := pool.QueryRow(ctx,
				`SELECT payload FROM events_outbox WHERE topic = 'payment.bounced' AND payload->>'payment_id' = $1 ORDER BY created_at DESC LIMIT 1`,
				pid).Scan(&bouncedPayload); err != nil {
				t.Fatalf("read payment.bounced outbox event: %v", err)
			}
			deliver(t, "payment.bounced", bouncedPayload)

			// 4. Both 1020 and 1000 must return to a net 0 delta (the bounce credited 1000, not 1020).
			bal1020After, err := glSvc.GLBalanceCents(cctx, "1020", year, month, "")
			if err != nil {
				t.Fatalf("GLBalanceCents 1020 after: %v", err)
			}
			bal1000After, err := glSvc.GLBalanceCents(cctx, "1000", year, month, "")
			if err != nil {
				t.Fatalf("GLBalanceCents 1000 after: %v", err)
			}
			delta1020 := bal1020After - bal1020Before
			delta1000 := bal1000After - bal1000Before
			if delta1020 != 0 || delta1000 != 0 {
				t.Fatalf("lodged cheque bounce credited wrong account: GL 1020 delta = %d (want 0), GL 1000 delta = %d (want 0)", delta1020, delta1000)
			}
		})
	}
}
