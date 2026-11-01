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
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/jackc/pgx/v5"
)

type glExternalPoster struct {
	svc *gl.Service
	ctx context.Context
}

func (p glExternalPoster) PostJournal(_ context.Context, in gl.ExternalJournal) (string, error) {
	return p.svc.PostExternal(p.ctx, in)
}

// PostJournalTx is the transaction arm (FROM-PRINCIPAL 232): every poster bankrec is served gains it, so
// the match and its clearing journal share the caller's transaction and no leg posts in two steps. The
// caller's context is the leg's staff context, as in PostJournal.
func (p glExternalPoster) PostJournalTx(ctx context.Context, tx pgx.Tx, in gl.ExternalJournal) (string, error) {
	return p.svc.PostExternalTx(p.ctx, tx, in)
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
					_, _ = pool.Exec(c, `UPDATE payments SET till_session_id = NULL WHERE till_session_id = $1::uuid`, sessionID)
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

			// 2. Open a till session at branchID, associate pid with that session, record the cheque lodgement, and match the bank deposit line.
			if err := pool.QueryRow(ctx,
				`INSERT INTO till_sessions (branch_id, station, opened_by) VALUES ($1::uuid, $2, $3::uuid) RETURNING id::text`,
				branchID, "FW106A-"+mode, userID).Scan(&sessionID); err != nil {
				t.Fatalf("insert till_session: %v", err)
			}
			if _, err := pool.Exec(ctx, `UPDATE payments SET till_session_id = $1::uuid WHERE id = $2::uuid`, sessionID, pid); err != nil {
				t.Fatalf("associate payment with till_session: %v", err)
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
				// The window is calendar days (txn_date is a DATE and the upper bound is exclusive), so it
				// steps by days in the dealer's zone: a 24 hour step is 23 hours short of the next day on the
				// night the clocks fall back, and the line dated today then sat on the exclusive bound.
				summary, err := bankrecSvc.AutoMatchReach(cctx, accountID, now.AddDate(0, 0, -1), now.AddDate(0, 0, 1), userID, func(string) bool { return true })
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

// TestIntegration_FW1_06a_BounceOfFeePayingPaymentReopensNSFFee proves Ruling 128 (P1-1 bounce leg):
// when a replacement cheque pays both a reopened invoice ($500.00) and an open NSF fee ($45.00)
// and that replacement cheque subsequently bounces via MarkNSF, MarkNSF restores both the invoice
// and the $45.00 NSF fee in customer_transactions and ReversedCents so GL 1100 and the AR subledger tie.
func TestIntegration_FW1_06a_BounceOfFeePayingPaymentReopensNSFFee(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	cctx := globalCtx(userID)

	tillRepo := NewRepository(pool)
	tillSvc := realSvc(pool)
	payRepo := payments.NewRepository(pool)
	glSvc := gl.NewService(gl.NewRepository(pool)).WithPaymentReader(tillRepo)

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

	suffix := fmt.Sprintf("fw106a-bnc-fee-%d", time.Now().UnixNano()%1_000_000_000)
	var custID, invID, bouncedPayID, nsfID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		VALUES ('FW106A-'||$1, 'FW1-06a Bounce Fee Co', 'CONTRACTOR', 30, 5000000) RETURNING id::text`,
		suffix).Scan(&custID); err != nil {
		t.Fatalf("plant customer: %v", err)
	}
	t.Cleanup(func() {
		// Dependency order; every delete fails the leg loudly, naming its table (FROM-PRINCIPAL 202).
		del := func(table, sql string, args ...any) {
			t.Helper()
			if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
				t.Errorf("cleanup: delete from %s: %v", table, err)
			}
		}
		del("gl_journal_lines", `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE source_ref_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid))`, custID)
		del("gl_journals", `DELETE FROM gl_journals WHERE source_ref_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid)`, custID)
		del("events_outbox", `DELETE FROM events_outbox WHERE payload->>'customer_id' = $1 OR payload->>'payment_id' IN (SELECT id::text FROM payments WHERE customer_id = $1::uuid)`, custID)
		del("payment_allocations", `DELETE FROM payment_allocations WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`, custID)
		del("customer_transactions", `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, custID)
		del("payment_nsf", `DELETE FROM payment_nsf WHERE customer_id = $1::uuid OR payment_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid)`, custID)
		del("payments", `DELETE FROM payments WHERE customer_id = $1::uuid`, custID)
		del("invoices", `DELETE FROM invoices WHERE customer_id = $1::uuid`, custID)
		del("credit_holds", `DELETE FROM credit_holds WHERE customer_id = $1::uuid`, custID)
		del("customers", `DELETE FROM customers WHERE id = $1::uuid`, custID)
	})

	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date, subtotal_cents, tax_cents, total_cents, balance_cents, doc_type)
		VALUES ('INV-'||$1, $2::uuid, $3::uuid, 'FINALIZED', (now() AT TIME ZONE 'America/Toronto')::date - 10, (now() AT TIME ZONE 'America/Toronto')::date + 20, 50000, 0, 50000, 50000, 'INVOICE')
		RETURNING id::text`, suffix, custID, branchID).Scan(&invID); err != nil {
		t.Fatalf("plant invoice: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO payments (payment_no, customer_id, branch_id, method, amount_cents, status, idempotency_key)
		VALUES ('PAY-BNC1-'||$1, $2::uuid, $3::uuid, 'CHEQUE', 50000, 'BOUNCED', 'bnc1-key-'||$1)
		RETURNING id::text`, suffix, custID, branchID).Scan(&bouncedPayID); err != nil {
		t.Fatalf("plant bounced payment: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO payment_nsf (payment_id, customer_id, fee_cents, reason, recorded_by)
		VALUES ($1::uuid, $2::uuid, 4500, 'first bounce', $3::uuid)
		RETURNING id::text`, bouncedPayID, custID, userID).Scan(&nsfID); err != nil {
		t.Fatalf("plant payment_nsf: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO customer_transactions
		   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		 VALUES
		   ($1::uuid, 'INVOICE', 50000, 50000, 'invoice', $2::uuid, $3::uuid, 'reopened invoice'),
		   ($1::uuid, 'ADJUSTMENT', 4500, 54500, 'payment_nsf', $4::uuid, $3::uuid, 'NSF fee')`,
		custID, invID, branchID, nsfID); err != nil {
		t.Fatalf("plant customer_transactions: %v", err)
	}

	applied, err := payRepo.ApplyReceived(ctx, payments.ApplyRequest{
		CustomerID:     custID,
		InvoiceIDs:     []string{invID, nsfID},
		AmountCents:    54500,
		Method:         "CHEQUE",
		Reference:      "REPL-CHQ-BNC",
		IdempotencyKey: suffix + "-repl-chq",
	})
	if err != nil {
		t.Fatalf("ApplyReceived: %v", err)
	}

	loc, _ := time.LoadLocation("America/Toronto")
	now := time.Now().In(loc)
	year, month := now.Year(), int(now.Month())

	glBefore, err := glSvc.GLBalanceCents(cctx, "1100", year, month, "")
	if err != nil {
		t.Fatalf("GLBalanceCents 1100 before bounce: %v", err)
	}
	var subBefore int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions WHERE customer_id = $1::uuid`, custID).Scan(&subBefore); err != nil {
		t.Fatalf("subledger before bounce: %v", err)
	}

	nsfRes, err := tillSvc.MarkNSF(cctx, NSFInput{
		PaymentID: applied.PaymentID,
		Reason:    "replacement cheque bounced NSF",
	})
	if err != nil {
		t.Fatalf("MarkNSF: %v", err)
	}
	var bouncedPayload []byte
	if err := pool.QueryRow(ctx,
		`SELECT payload FROM events_outbox WHERE topic = 'payment.bounced' AND payload->>'payment_id' = $1 ORDER BY created_at DESC LIMIT 1`,
		applied.PaymentID).Scan(&bouncedPayload); err != nil {
		t.Fatalf("read payment.bounced outbox event: %v", err)
	}
	deliver(t, "payment.bounced", bouncedPayload)

	glAfter, err := glSvc.GLBalanceCents(cctx, "1100", year, month, "")
	if err != nil {
		t.Fatalf("GLBalanceCents 1100 after bounce: %v", err)
	}
	var subAfter, feeNetAfter int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions WHERE customer_id = $1::uuid`, custID).Scan(&subAfter); err != nil {
		t.Fatalf("subledger after bounce: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions WHERE customer_id = $1::uuid AND source_type = 'payment_nsf' AND source_ref_id = $2::uuid`,
		custID, nsfID).Scan(&feeNetAfter); err != nil {
		t.Fatalf("nsf group sum after bounce: %v", err)
	}
	glDelta := glAfter - glBefore
	subDelta := subAfter - subBefore
	wantDelta := int64(54500) + nsfRes.FeeCents
	if nsfRes.ReversedCents != 54500 || glDelta != wantDelta || subDelta != wantDelta || feeNetAfter != 4500 {
		t.Fatalf("bounce of fee-paying payment drifted AR: ReversedCents = %d (want 54500), GL 1100 delta = %d, subledger delta = %d (want %d), payment_nsf group sum = %d (want 4500)",
			nsfRes.ReversedCents, glDelta, subDelta, wantDelta, feeNetAfter)
	}
}

// TestIntegration_FW1_06a_LodgementPaymentIDsNeverGuesses pins Ruling 128 (P1-2):
// lodgementPaymentIDs claims only unreconciled RECEIVED payments from the lodgement's own
// till_session_id and method, and only when exactly one subset of those candidates sums to
// the lodgement amount; otherwise it claims nothing and marks PaymentsUnresolved = true.
func TestIntegration_FW1_06a_LodgementPaymentIDsNeverGuesses(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	repo := NewRepository(pool)

	suffix := fmt.Sprintf("fw106a-ldg-guess-%d", time.Now().UnixNano()%1_000_000_000)
	var custID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		VALUES ('FW106A-LG-'||$1, 'FW1-06a Lodgement Guess Co', 'CONTRACTOR', 30, 5000000) RETURNING id::text`,
		suffix).Scan(&custID); err != nil {
		t.Fatalf("plant customer: %v", err)
	}
	var sessions []string
	var paymentsPlanted []string
	t.Cleanup(func() {
		c := context.Background()
		for _, pid := range paymentsPlanted {
			_, _ = pool.Exec(c, `DELETE FROM payments WHERE id = $1::uuid`, pid)
		}
		for _, sid := range sessions {
			_, _ = pool.Exec(c, `DELETE FROM till_movements WHERE till_session_id = $1::uuid`, sid)
			_, _ = pool.Exec(c, `DELETE FROM till_sessions WHERE id = $1::uuid`, sid)
		}
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, custID)
	})

	newSession := func(t *testing.T, label string) string {
		t.Helper()
		var sid string
		if err := pool.QueryRow(ctx,
			`INSERT INTO till_sessions (branch_id, station, opened_by) VALUES ($1::uuid, $2, $3::uuid) RETURNING id::text`,
			branchID, label, userID).Scan(&sid); err != nil {
			t.Fatalf("insert till_session %s: %v", label, err)
		}
		sessions = append(sessions, sid)
		return sid
	}
	newPayment := func(t *testing.T, sid, method string, amt int64, tag string) string {
		t.Helper()
		var pid string
		if err := pool.QueryRow(ctx,
			`INSERT INTO payments (payment_no, customer_id, branch_id, till_session_id, method, amount_cents, status)
			 VALUES ($1, $2::uuid, $3::uuid, $4::uuid, $5, $6, 'RECEIVED') RETURNING id::text`,
			fmt.Sprintf("PMT-%s-%d", tag, time.Now().UnixNano()%1_000_000), custID, branchID, sid, method, amt).Scan(&pid); err != nil {
			t.Fatalf("insert payment %s: %v", tag, err)
		}
		paymentsPlanted = append(paymentsPlanted, pid)
		return pid
	}
	newLodgement := func(t *testing.T, sid, method string, amt int64, ref string) string {
		t.Helper()
		var lid string
		if err := pool.QueryRow(ctx,
			`INSERT INTO till_movements (till_session_id, movement_type, method, amount_cents, reason, reference, created_by)
			 VALUES ($1::uuid, 'LODGEMENT', $2, $3, 'test lodgement', $4, $5::uuid) RETURNING id::text`,
			sid, method, amt, ref, userID).Scan(&lid); err != nil {
			t.Fatalf("insert lodgement %s: %v", ref, err)
		}
		return lid
	}

	// Leg 1: ambiguous subset in the same session and method ([$250, $250] vs [$300, $200] both sum to $500).
	t.Run("ambiguous_subset_in_same_session_and_method", func(t *testing.T) {
		sid := newSession(t, "FW106A-AMBIG")
		_ = newPayment(t, sid, "CHEQUE", 25000, "ambig-250a")
		_ = newPayment(t, sid, "CHEQUE", 25000, "ambig-250b")
		_ = newPayment(t, sid, "CHEQUE", 30000, "ambig-300")
		_ = newPayment(t, sid, "CHEQUE", 20000, "ambig-200")
		lid := newLodgement(t, sid, "CHEQUE", 50000, "LDG-AMBIG-500")

		fact, found, err := repo.LodgementFacts(ctx, lid)
		if err != nil || !found {
			t.Fatalf("LodgementFacts: found=%v err=%v", found, err)
		}
		if len(fact.PaymentIDs) != 0 || !fact.PaymentsUnresolved {
			t.Fatalf("ambiguous same-session/method subset claimed PaymentIDs=%v PaymentsUnresolved=%v; want nil PaymentIDs and PaymentsUnresolved=true",
				fact.PaymentIDs, fact.PaymentsUnresolved)
		}
	})

	// Leg 2: a payment from another session must never be claimed by this session's lodgement.
	t.Run("payment_from_another_session", func(t *testing.T) {
		sidA := newSession(t, "FW106A-SESS-A")
		sidB := newSession(t, "FW106A-SESS-B")
		_ = newPayment(t, sidB, "CHEQUE", 50000, "other-sess-500")
		lidA := newLodgement(t, sidA, "CHEQUE", 50000, "LDG-SESS-A-500")

		fact, found, err := repo.LodgementFacts(ctx, lidA)
		if err != nil || !found {
			t.Fatalf("LodgementFacts: found=%v err=%v", found, err)
		}
		if len(fact.PaymentIDs) != 0 || !fact.PaymentsUnresolved {
			t.Fatalf("lodgement in session A claimed PaymentIDs=%v (PaymentsUnresolved=%v) from session B; want nil PaymentIDs and PaymentsUnresolved=true",
				fact.PaymentIDs, fact.PaymentsUnresolved)
		}
	})

	// Leg 3: cash under a cheque lodgement (or partial cheque set) must never be claimed.
	t.Run("cash_under_cheque_lodgement", func(t *testing.T) {
		sid := newSession(t, "FW106A-CASH-CHQ")
		_ = newPayment(t, sid, "CHEQUE", 30000, "chq-300")
		_ = newPayment(t, sid, "CASH", 20000, "cash-200")
		lid := newLodgement(t, sid, "CHEQUE", 50000, "LDG-CHQ-500")

		fact, found, err := repo.LodgementFacts(ctx, lid)
		if err != nil || !found {
			t.Fatalf("LodgementFacts: found=%v err=%v", found, err)
		}
		if len(fact.PaymentIDs) != 0 || !fact.PaymentsUnresolved {
			t.Fatalf("cheque lodgement claimed PaymentIDs=%v (PaymentsUnresolved=%v) using cash/partial set; want nil PaymentIDs and PaymentsUnresolved=true",
				fact.PaymentIDs, fact.PaymentsUnresolved)
		}
	})

	// Leg 4 (R2-P3): the one unique exact set is claimed. Cheques of $250, $300 and $200 under a
	// $500 cheque lodgement admit exactly one subset ($300 + $200), so the lodgement claims exactly
	// those two payments and is not unresolved (S6's round 1 probe, pinned).
	t.Run("unique_exact_set_is_claimed", func(t *testing.T) {
		sid := newSession(t, "FW106A-UNIQ")
		_ = newPayment(t, sid, "CHEQUE", 25000, "uniq-250")
		p300 := newPayment(t, sid, "CHEQUE", 30000, "uniq-300")
		p200 := newPayment(t, sid, "CHEQUE", 20000, "uniq-200")
		lid := newLodgement(t, sid, "CHEQUE", 50000, "LDG-UNIQ-500")

		fact, found, err := repo.LodgementFacts(ctx, lid)
		if err != nil || !found {
			t.Fatalf("LodgementFacts: found=%v err=%v", found, err)
		}
		want := map[string]bool{p300: true, p200: true}
		if fact.PaymentsUnresolved || len(fact.PaymentIDs) != 2 || !want[fact.PaymentIDs[0]] || !want[fact.PaymentIDs[1]] {
			t.Fatalf("unique exact set: PaymentIDs=%v PaymentsUnresolved=%v; want exactly the $300 and $200 cheques and PaymentsUnresolved=false",
				fact.PaymentIDs, fact.PaymentsUnresolved)
		}
	})

	// Leg 5 (R2-P1-2): a busy cheque session, 60 same-session payments of distinct even amounts
	// under a lodgement no subset reaches. The answer must come back inside the bound with nothing
	// claimed and PaymentsUnresolved (the ruling caps the candidates at 20); the subset walk this
	// replaced was exponential (57 s at 36 payments) and never answered.
	t.Run("sixty_payments_answer_inside_the_bound", func(t *testing.T) {
		sid := newSession(t, "FW106A-SIXTY")
		for i := 0; i < 60; i++ {
			_ = newPayment(t, sid, "CHEQUE", int64(i+1)*100, fmt.Sprintf("sixty-%02d", i))
		}
		lid := newLodgement(t, sid, "CHEQUE", 99999, "LDG-SIXTY")

		type answer struct {
			fact  bankrec.LodgementFact
			found bool
			err   error
		}
		done := make(chan answer, 1)
		start := time.Now()
		go func() {
			fact, found, err := repo.LodgementFacts(ctx, lid)
			done <- answer{fact, found, err}
		}()
		var got answer
		select {
		case got = <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("LodgementFacts over 60 same-session cheque payments did not answer within 10s: the subset search is exponential (R2-P1-2)")
		}
		if got.err != nil || !got.found {
			t.Fatalf("LodgementFacts: found=%v err=%v", got.found, got.err)
		}
		if elapsed := time.Since(start); elapsed > 10*time.Second {
			t.Fatalf("LodgementFacts over 60 payments took %s, over the 10s bound", elapsed)
		}
		if len(got.fact.PaymentIDs) != 0 || !got.fact.PaymentsUnresolved {
			t.Fatalf("capped lodgement claimed PaymentIDs=%v PaymentsUnresolved=%v; want nil PaymentIDs and PaymentsUnresolved=true",
				got.fact.PaymentIDs, got.fact.PaymentsUnresolved)
		}
	})
}
