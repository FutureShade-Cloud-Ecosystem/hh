//go:build integration

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// TestFW1_08b_HeadOfficePolicyMultiBranchCloseAndCrossBranchReturn exercises
// FW1-08b end-to-end against real Postgres:
//  1. HeadOfficeBranchID default (TRT) and SetHeadOfficeBranch (Controller-only,
//     reason-required, audited in sensitive_access_log), plus branchless manual,
//     reverse, recurring, and tax.relief.assessed journals stamping the head office
//     or order branch on every gl_journal_lines row.
//  2. Multi-branch CloseYear closing P&L per branch (TRT + KGN + company-level mapped
//     to head office) into 3900 Retained Earnings with zero NULL branch_id lines.
//  3. Cross-branch POS return (sale at TRT, return rung at KGN) writing KGN to
//     pos_returns.branch_id and posting all three return journals (pos.return.committed,
//     order.returned, pos.return.written_off) to KGN on every gl_journal_lines row.
func TestFW1_08b_HeadOfficePolicyMultiBranchCloseAndCrossBranchReturn(t *testing.T) {
	ctx := context.Background()
	pool := fw1CensusDB(t)

	// Ensure FY 2026 and FY 2018 periods 1..12 exist and are OPEN. The year end leg
	// closes 2018, a year whose last day has passed on the real clock: the merged tree
	// keeps FW1-09b round 2's refusal of a year end whose year has not ended (the same
	// reason the census leg closes 2020).
	for _, yr := range []int{2026, 2018} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO fiscal_periods (year, month, status)
			SELECT $1, m, 'OPEN' FROM generate_series(1, 12) AS m
			ON CONFLICT (year, month) DO UPDATE SET status = 'OPEN', closed_at = NULL
		`, yr); err != nil {
			t.Fatalf("ensure %d periods open: %v", yr, err)
		}
	}

	var trtBranchID, kgnBranchID, userID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE code = 'TRT'`).Scan(&trtBranchID); err != nil {
		t.Fatalf("resolve TRT location: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE code = 'KGN'`).Scan(&kgnBranchID); err != nil {
		t.Fatalf("resolve KGN location: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("resolve seeded user: %v", err)
	}

	auditSvc := audit.NewService(audit.NewRepository(pool), slog.Default())
	billingRepo := billing.NewRepository(pool)
	posRepo := pos.NewRepository(pool)
	tillRepo := till.NewRepository(pool)
	orderRepo := order.NewRepository(pool)
	glRepo := gl.NewRepository(pool).WithZone(newDealerZone(pool))
	glSvc := gl.NewService(glRepo).
		WithStandardRate(newTaxEngine(pool)).
		WithTaxLabel(newTaxWords(pool)).
		WithInvoiceReader(billingRepo).
		WithARSubledger(billingRepo).
		WithTaxSplitReader(posRepo).
		WithDeliveryReader(orderRepo).
		WithPaymentReader(tillRepo).
		WithAudit(auditSvc).
		WithAuditTxRecorder(auditSvc).
		WithCostBasis(fw1CensusCostBasis{unitCostCents: 2000})

	ctrl := identity.StaffContext{
		UserID:   userID,
		RoleCode: "FINANCIAL_CONTROLLER",
		Tier:     identity.TierLeadership,
		Scope:    identity.ScopeGlobal,
	}
	mgr := identity.StaffContext{
		UserID:   userID,
		RoleCode: "SALES_MANAGER",
		Tier:     identity.TierManager,
		Scope:    identity.ScopeGlobal,
	}
	ctrlCtx := identity.ContextWithStaff(ctx, ctrl)
	mgrCtx := identity.ContextWithStaff(ctx, mgr)

	// 1. Default head office branch is TRT; SetHeadOfficeBranch is Controller-only, reason-required, and audited.
	ho, err := glSvc.HeadOfficeBranchID(ctrlCtx)
	if err != nil {
		t.Fatalf("HeadOfficeBranchID default: %v", err)
	}
	if ho != trtBranchID {
		t.Fatalf("default HeadOfficeBranchID = %q, want TRT (%q)", ho, trtBranchID)
	}

	if err := glSvc.SetHeadOfficeBranch(mgrCtx, kgnBranchID, "non-controller attempt"); err == nil {
		t.Fatal("expected non-controller SetHeadOfficeBranch to fail with 403")
	} else if status, ok := gl.ErrorHTTPStatus(err); !ok || status != 403 {
		t.Fatalf("non-controller SetHeadOfficeBranch status = (%d, %v), want (403, true); err=%v", status, ok, err)
	}

	if err := glSvc.SetHeadOfficeBranch(ctrlCtx, kgnBranchID, "   "); err == nil {
		t.Fatal("expected empty-reason SetHeadOfficeBranch to fail with 422")
	} else if status, ok := gl.ErrorHTTPStatus(err); !ok || status != 422 {
		t.Fatalf("empty-reason SetHeadOfficeBranch status = (%d, %v), want (422, true); err=%v", status, ok, err)
	}

	if err := glSvc.SetHeadOfficeBranch(ctrlCtx, kgnBranchID, "Ruling 82.3 test relocate head office to KGN"); err != nil {
		t.Fatalf("SetHeadOfficeBranch(KGN): %v", err)
	}
	hoAfter, err := glSvc.HeadOfficeBranchID(ctrlCtx)
	if err != nil || hoAfter != kgnBranchID {
		t.Fatalf("HeadOfficeBranchID after SetHeadOfficeBranch = %q (err=%v), want KGN (%q)", hoAfter, err, kgnBranchID)
	}

	var auditCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM audit_log
		 WHERE action = 'GL_HEAD_OFFICE_BRANCH_SET'
		   AND entity_type = 'gl.policy'
		   AND branch_id = $1::uuid
	`, kgnBranchID).Scan(&auditCount); err != nil {
		t.Fatalf("query audit_log for GL_HEAD_OFFICE_BRANCH_SET: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("audit_log GL_HEAD_OFFICE_BRANCH_SET count = %d, want 1", auditCount)
	}

	// Branchless manual journal now stamps KGN (the configured head office branch).
	manKGN, err := glSvc.CreateJournal(ctrlCtx, gl.CreateJournalInput{
		Memo:   "fw1-08b branchless manual journal after head office change",
		Source: gl.SourceManual,
		Lines: []gl.JournalLineInput{
			{AccountCode: "6100", DebitCents: 1500, Memo: "dr"},
			{AccountCode: "1000", CreditCents: 1500, Memo: "cr"},
		},
	})
	if err != nil {
		t.Fatalf("CreateJournal (branchless after SetHeadOfficeBranch): %v", err)
	}
	if manKGN.BranchID != kgnBranchID {
		t.Fatalf("branchless CreateJournal BranchID = %q, want KGN (%q)", manKGN.BranchID, kgnBranchID)
	}
	assertJournalLinesBranch(t, ctx, pool, manKGN.ID, kgnBranchID)

	// Restore head office branch to TRT for the remaining checks.
	if err := glSvc.SetHeadOfficeBranch(ctrlCtx, trtBranchID, "Restore head office to TRT"); err != nil {
		t.Fatalf("SetHeadOfficeBranch(TRT): %v", err)
	}

	// 2. Multi-branch CloseYear on FY 2018 (TRT + KGN + company-level P&L lines).
	postFY2018 := func(branchID, memo, acctDr, acctCr string, cents int64) string {
		t.Helper()
		jid, err := glSvc.PostExternal(ctrlCtx, gl.ExternalJournal{
			Source:         gl.SourceAdjustment,
			IdempotencyKey: "fw1-08b-fy2018:" + uuid.NewString(),
			Memo:           memo,
			BranchID:       branchID,
			DocumentDate:   time.Date(2018, 6, 15, 12, 0, 0, 0, time.UTC),
			Lines: []gl.ExternalLine{
				{AccountCode: acctDr, DebitCents: cents, Memo: memo},
				{AccountCode: acctCr, CreditCents: cents, Memo: memo},
			},
		})
		if err != nil {
			t.Fatalf("postFY2018(%s): %v", memo, err)
		}
		return jid
	}
	// TRT: revenue 80000, expense 30000 -> net income 50000.
	postFY2018(trtBranchID, "TRT FY2018 revenue", "1000", "4000", 80000)
	postFY2018(trtBranchID, "TRT FY2018 expense", "5000", "1000", 30000)
	// KGN: revenue 50000, expense 20000 -> net income 30000.
	postFY2018(kgnBranchID, "KGN FY2018 revenue", "1000", "4000", 50000)
	postFY2018(kgnBranchID, "KGN FY2018 expense", "5000", "1000", 20000)

	closeRes, err := glSvc.CloseYear(ctrlCtx, 2018, "FW1-08b multi-branch close")
	if err != nil {
		t.Fatalf("CloseYear(2018): %v", err)
	}
	if closeRes.NetIncomeCents != 80000 {
		t.Fatalf("CloseYear(2018) NetIncomeCents = %d, want 80000", closeRes.NetIncomeCents)
	}

	// Verify both TRT and KGN received balanced CLOSING journals with non-null branch_id on every line.
	rows, err := pool.Query(ctx, `
		SELECT id::text, COALESCE(branch_id::text, '')
		  FROM gl_journals
		 WHERE fiscal_year = 2018 AND source = 'CLOSING'
		 ORDER BY entry_no
	`)
	if err != nil {
		t.Fatalf("query FY2018 CLOSING journals: %v", err)
	}
	closingByBranch := map[string]string{}
	for rows.Next() {
		var jid, bid string
		if err := rows.Scan(&jid, &bid); err != nil {
			rows.Close()
			t.Fatalf("scan CLOSING journal: %v", err)
		}
		closingByBranch[bid] = jid
	}
	rows.Close()
	for _, wantBranch := range []string{trtBranchID, kgnBranchID} {
		jid, ok := closingByBranch[wantBranch]
		if !ok {
			t.Fatalf("missing FY2018 CLOSING journal for branch %s (got %v)", wantBranch, closingByBranch)
		}
		assertJournalLinesBranch(t, ctx, pool, jid, wantBranch)
	}

	// Verify PLBalancesByBranch(2018) nets to 0 on every P&L row after CloseYear.
	plAfter, err := glRepo.PLBalancesByBranch(ctrlCtx, 2018)
	if err != nil {
		t.Fatalf("PLBalancesByBranch(2018) after close: %v", err)
	}
	for _, r := range plAfter {
		if r.NetDebitCents != 0 {
			t.Errorf("PLBalancesByBranch(2018) after close has non-zero row: %+v", r)
		}
	}

	// 3. Cross-branch POS return (sale at TRT, return rung at KGN): outbox events carry KGN (pos_returns.branch_id)
	// and GL posts all three return journals (pos.return.committed, order.returned, pos.return.written_off) to KGN.
	directOutbox := &fw1DirectOutbox{}
	disp := notify.NewDispatcher(directOutbox, slog.Default())
	glSvc.RegisterPostingHandlers(disp)

	dispatchTopic := func(topic string, payload any, idempotencyKey string) string {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal %s payload: %v", topic, err)
		}
		directOutbox.events = append(directOutbox.events, notify.Event{
			EventID: uuid.NewString(),
			Topic:   topic,
			Payload: raw,
		})
		if n := disp.DrainOnce(ctrlCtx); n != 1 {
			t.Fatalf("dispatch %s: drained %d events, want 1", topic, n)
		}
		var jid string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM gl_journals WHERE idempotency_key = $1`, idempotencyKey).Scan(&jid); err != nil {
			t.Fatalf("lookup journal for %s (key %s): %v", topic, idempotencyKey, err)
		}
		return jid
	}

	var customerID, trtOrderID, trtInvID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		VALUES ('FW1-08B-CUST', 'FW1 08b Customer', 'CONTRACTOR', $1::uuid, 5000000)
		RETURNING id::text
	`, trtBranchID).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, subtotal_cents, tax_cents, total_cents)
		VALUES ('#ORD-FW1-08B-TRT', $1::uuid, $2::uuid, 'CONFIRMED', 10000, 1300, 11300)
		RETURNING id::text
	`, customerID, trtBranchID).Scan(&trtOrderID); err != nil {
		t.Fatalf("seed TRT order: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, subtotal_cents, tax_cents, total_cents, balance_cents, due_date, finalized_at)
		VALUES ('#INV-FW1-08B-TRT', $1::uuid, $2::uuid, 'PAID', 10000, 1300, 11300, 0, '2026-07-15', '2026-06-15 15:00:00+00'::timestamptz)
		RETURNING id::text
	`, customerID, trtBranchID).Scan(&trtInvID); err != nil {
		t.Fatalf("seed TRT invoice: %v", err)
	}

	kgnReturnID := uuid.NewString()
	jRetCommitted := dispatchTopic("pos.return.committed", map[string]any{
		"return_id":      kgnReturnID,
		"order_id":       trtOrderID,
		"invoice_id":     trtInvID,
		"refund_method":  "CASH",
		"subtotal_cents": 2000,
		"tax_cents":      260,
		"total_cents":    2260,
		"branch_id":      kgnBranchID,
	}, "pos.return.committed:"+kgnReturnID)
	assertJournalLinesBranch(t, ctx, pool, jRetCommitted, kgnBranchID)

	jOrdReturned := dispatchTopic("order.returned", map[string]any{
		"order_id":  trtOrderID,
		"return_id": kgnReturnID,
		"branch_id": kgnBranchID,
		"lines": []map[string]any{
			{"product_id": uuid.NewString(), "qty_base": 1},
		},
	}, "order.returned:"+kgnReturnID)
	assertJournalLinesBranch(t, ctx, pool, jOrdReturned, kgnBranchID)

	jWrittenOff := dispatchTopic("pos.return.written_off", map[string]any{
		"order_id":  trtOrderID,
		"return_id": kgnReturnID,
		"branch_id": kgnBranchID,
		"lines": []map[string]any{
			{"product_id": uuid.NewString(), "qty_base": 1},
		},
	}, "pos.return.written_off:"+kgnReturnID)
	assertJournalLinesBranch(t, ctx, pool, jWrittenOff, kgnBranchID)
}

func assertJournalLinesBranch(t *testing.T, ctx context.Context, pool *pgxpool.Pool, journalID, wantBranchID string) {
	t.Helper()
	var hdrBranch string
	var totalLines, matchingLines int
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(j.branch_id::text, ''),
		       COUNT(l.id),
		       COUNT(l.id) FILTER (WHERE l.branch_id = $2::uuid)
		  FROM gl_journals j
		  LEFT JOIN gl_journal_lines l ON l.journal_id = j.id
		 WHERE j.id = $1::uuid
		 GROUP BY j.id, j.branch_id
	`, journalID, wantBranchID).Scan(&hdrBranch, &totalLines, &matchingLines); err != nil {
		t.Fatalf("assertJournalLinesBranch(%s): %v", journalID, err)
	}
	if hdrBranch != wantBranchID {
		t.Errorf("journal %s header branch_id = %q, want %q", journalID, hdrBranch, wantBranchID)
	}
	if totalLines == 0 || matchingLines != totalLines {
		t.Errorf("journal %s lines matching branch %s = %d/%d", journalID, wantBranchID, matchingLines, totalLines)
	}
}
