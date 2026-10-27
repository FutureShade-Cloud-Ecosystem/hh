//go:build integration

package main

// Scope of TestFW1JournalLinesCarryBranch and the non-GL writer call-site census:
//
// 1. What the 27 paths in TestFW1JournalLinesCarryBranch are:
//    The 27 driven paths cover the GL's own writers (the 13 outbox builders in
//    gl/posting.go consume's switch, CreateJournal with and without a branch,
//    ReverseJournal with and without a branch, RunRecurring, CloseYear,
//    bankrec's 5 auto/manual/unmatch paths, and the 0056/0221 migration setup
//    entries) plus two stand-in calls (external_post_external_tx for
//    gl.Service.PostExternalTx and external_credit_memo_poster for
//    w6cCreditMemoPoster.PostCreditMemoJournal).
//
// 2. How the non-GL module writer paths and outbox emitters are pinned here:
//    Every caller outside core/internal/gl that reaches a journal (inventory,
//    transfer, invcount, purchasing, landedcost, lien, warranty, billing credit
//    memo issue and repost, bankrec, the cmd/core wire adapters, every outbox
//    emitter whose payload carries a branch, and every dynamic-topic outbox
//    helper) is listed in fw1NonGLJournalWriterCallSites in
//    core/cmd/core/fw1_branch_census_red.go (the single source of truth for
//    that table) and guarded by the static AST scan
//    TestFW1NonGLJournalWriterCallSitesComplete in
//    core/cmd/core/fw1_branch_census_red_test.go so no new or moved writer can
//    appear unnoticed. FW1-08b drives the real module doors.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/bankrec"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

type fw1CensusCostBasis struct {
	unitCostCents int64
}

func (c fw1CensusCostBasis) UnitCostCents(_ context.Context, _ string, qtyBase float64) (int64, bool, error) {
	return int64(qtyBase) * c.unitCostCents, true, nil
}

type fw1DirectOutbox struct {
	events []notify.Event
}

func (o *fw1DirectOutbox) ProcessBatch(ctx context.Context, _ int, settle func(context.Context, notify.Event) notify.Settlement) (int, error) {
	evs := o.events
	o.events = nil
	for _, ev := range evs {
		s := settle(ctx, ev)
		if s.Status != "PUBLISHED" {
			return 0, fmt.Errorf("topic %s failed: %s", ev.Topic, s.LastError)
		}
	}
	return len(evs), nil
}

func fw1CensusDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		base = os.Getenv("HH_CORE_TEST_DATABASE_URL")
		if base != "" {
			t.Setenv("DATABASE_URL", base)
		}
	}
	if base == "" {
		t.Skip("DATABASE_URL is unset; TestFW1JournalLinesCarryBranch requires a real Postgres database")
	}

	ctx := context.Background()
	adminDSN := swapDatabase(base, "postgres")
	admin, err := pgxpool.New(ctx, adminDSN)
	if err == nil {
		var hasTemplate bool
		_ = admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'fw1census_template')`).Scan(&hasTemplate)
		if hasTemplate {
			name := "fw1_branch_census_it"
			testDSN := swapDatabase(base, name)
			if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
				admin.Close()
				t.Fatalf("dropping %s: %v", name, err)
			}
			if _, err := admin.Exec(ctx, "CREATE DATABASE "+name+" TEMPLATE fw1census_template"); err != nil {
				admin.Close()
				t.Fatalf("cloning %s from fw1census_template: %v", name, err)
			}
			admin.Close()
			t.Cleanup(func() {
				c, err := pgxpool.New(context.Background(), adminDSN)
				if err == nil {
					_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
					c.Close()
				}
			})
			pool, err := pgxpool.New(ctx, testDSN)
			if err != nil {
				t.Fatalf("connecting to %s: %v", name, err)
			}
			t.Cleanup(pool.Close)
			return pool
		}
		admin.Close()
	}
	return freshMigratedDB(t, "fw1_branch_census")
}

type fw1WriterResult struct {
	name       string
	sourceSite string
	journalIDs []string
}

func TestFW1JournalLinesCarryBranch(t *testing.T) {
	if os.Getenv("FW1_WAVE_FINAL") == "1" && len(fw1BranchCensusRed) > 0 {
		t.Fatalf("FW1_WAVE_FINAL=1 requires fw1BranchCensusRed to be empty, still has %d entries: %v", len(fw1BranchCensusRed), fw1BranchCensusRed)
	}

	ctx := context.Background()
	pool := fw1CensusDB(t)

	// Ensure FY 2026 periods 1..12 exist and are OPEN.
	if _, err := pool.Exec(ctx, `
		INSERT INTO fiscal_periods (year, month, status)
		SELECT 2026, m, 'OPEN' FROM generate_series(1, 12) AS m
		ON CONFLICT (year, month) DO UPDATE SET status = 'OPEN', closed_at = NULL
	`); err != nil {
		t.Fatalf("ensure 2026 periods open: %v", err)
	}

	var trtBranchID, userID, rbcAccountID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE code = 'TRT'`).Scan(&trtBranchID); err != nil {
		t.Fatalf("resolve TRT location: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("resolve seeded user: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM bank_accounts WHERE gl_account_code = '1000'`).Scan(&rbcAccountID); err != nil {
		t.Fatalf("resolve RBC bank account: %v", err)
	}

	// Pin clock to 2026-06-15 15:00:00 UTC inside the seeded 2026 calendar.
	pinnedNow := time.Date(2026, 6, 15, 15, 0, 0, 0, time.UTC)
	ctrl := identity.StaffContext{
		UserID:   userID,
		RoleCode: "FINANCIAL_CONTROLLER",
		Tier:     identity.TierLeadership,
		Scope:    identity.ScopeGlobal,
	}
	ctx = identity.ContextWithStaff(ctx, ctrl)

	billingRepo := billing.NewRepository(pool)
	posRepo := pos.NewRepository(pool)
	tillRepo := till.NewRepository(pool)
	glRepo := gl.NewRepository(pool).WithZone(newDealerZone(pool))
	glSvc := gl.NewService(glRepo).
		WithStandardRate(newTaxEngine(pool)).
		WithTaxLabel(newTaxWords(pool)).
		WithInvoiceReader(billingRepo).
		WithARSubledger(billingRepo).
		WithTaxSplitReader(posRepo).
		WithPaymentReader(tillRepo).
		WithCostBasis(fw1CensusCostBasis{unitCostCents: 2000})

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
		if n := disp.DrainOnce(ctx); n != 1 {
			t.Fatalf("dispatch %s: drained %d events, want 1", topic, n)
		}
		var jid string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM gl_journals WHERE idempotency_key = $1`, idempotencyKey).Scan(&jid); err != nil {
			t.Fatalf("lookup journal for %s (key %s): %v", topic, idempotencyKey, err)
		}
		return jid
	}

	bankrecSvc := bankrec.NewService(bankrec.NewRepository(pool)).
		WithPayments(tillRepo).
		WithLodgements(tillRepo).
		WithJournalPoster(glPosterAdapter{svc: glSvc}).
		WithGLBalances(glBalanceAdapter{svc: glSvc}).
		WithJournals(glSvc)

	var results []fw1WriterResult
	record := func(name, sourceSite string, journalIDs ...string) {
		t.Helper()
		if len(journalIDs) == 0 {
			t.Fatalf("writer %s produced no journal IDs", name)
		}
		for _, id := range journalIDs {
			if id == "" {
				t.Fatalf("writer %s returned an empty journal ID", name)
			}
		}
		results = append(results, fw1WriterResult{
			name:       name,
			sourceSite: sourceSite,
			journalIDs: journalIDs,
		})
	}

	// 1. Migration-seeded setup entries (0056 + 0221).
	rows, err := pool.Query(ctx, `
		SELECT id::text FROM gl_journals
		WHERE idempotency_key LIKE 'm2c1-demo-%' OR idempotency_key LIKE 'm2c1-retire-%'
		ORDER BY idempotency_key
	`)
	if err != nil {
		t.Fatalf("query migration setup journals: %v", err)
	}
	var migIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatalf("scan migration setup journal: %v", err)
		}
		migIDs = append(migIDs, id)
	}
	rows.Close()
	record("migration_0056_0221_setup_entries", "core/db/migrations/0056_bistrack_tieout_demo.sql:83-96,0221_the_setup_entries_behind_no_customer_document_come_back_out.sql:108-121", migIDs...)

	// 2. Manual journal WITH branch (gl/journal.go:101-190).
	manWithBranch, err := glSvc.CreateJournal(ctx, gl.CreateJournalInput{
		BranchID: trtBranchID,
		Memo:     "fw1 census manual with branch",
		Lines: []gl.JournalLineInput{
			{AccountCode: "1000", DebitCents: 10000, Memo: "dr"},
			{AccountCode: "4000", CreditCents: 10000, Memo: "cr"},
		},
	})
	if err != nil {
		t.Fatalf("manual_journal_with_branch: %v", err)
	}
	record("manual_journal_with_branch", "core/internal/gl/journal.go:123-130 (BranchID set)", manWithBranch.ID)

	// 3. Manual journal WITHOUT branch below threshold (gl/journal.go:101-190).
	manNoBranch, err := glSvc.CreateJournal(ctx, gl.CreateJournalInput{
		BranchID: "",
		Memo:     "fw1 census manual without branch",
		Lines: []gl.JournalLineInput{
			{AccountCode: "6100", DebitCents: 4000, Memo: "dr"},
			{AccountCode: "1000", CreditCents: 4000, Memo: "cr"},
		},
	})
	if err != nil {
		t.Fatalf("manual_journal_without_branch: %v", err)
	}
	record("manual_journal_without_branch", "core/internal/gl/journal.go:123-130 (BranchID empty)", manNoBranch.ID)

	// 4. Reverse journal WITH branch (gl/journal.go:281-317).
	revWithBranch, err := glSvc.ReverseJournal(ctx, manWithBranch.ID)
	if err != nil {
		t.Fatalf("reverse_journal_with_branch: %v", err)
	}
	record("reverse_journal_with_branch", "core/internal/gl/journal.go:302-311 (orig.BranchID set)", revWithBranch.ID)

	// 5. Reverse journal WITHOUT branch (gl/journal.go:281-317).
	revNoBranch, err := glSvc.ReverseJournal(ctx, manNoBranch.ID)
	if err != nil {
		t.Fatalf("reverse_journal_without_branch: %v", err)
	}
	record("reverse_journal_without_branch", "core/internal/gl/journal.go:302-311 (orig.BranchID empty)", revNoBranch.ID)

	// 6. Recurring journal run (gl/journal.go:327-363).
	var tplID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO gl_recurring_templates (name, memo, lines, schedule)
		VALUES ('FW1 Census Rent', 'monthly rent', '[{"account_code":"6100","debit_cents":12000},{"account_code":"2100","credit_cents":12000}]'::jsonb, 'MONTHLY')
		RETURNING id::text
	`).Scan(&tplID); err != nil {
		t.Fatalf("seed recurring template: %v", err)
	}
	recJournal, err := glSvc.RunRecurring(ctx, tplID)
	if err != nil {
		t.Fatalf("recurring_journal_run: %v", err)
	}
	record("recurring_journal_run", "core/internal/gl/journal.go:349-357 (RunRecurring)", recJournal.ID)

	// Seed a customer for invoice/order/payment fixtures on TRT.
	var customerID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		VALUES ('FW1-CENSUS-CUST', 'FW1 Census Customer', 'CONTRACTOR', $1::uuid, 5000000)
		RETURNING id::text
	`, trtBranchID).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}

	// 7. Outbox ar.invoice.finalized (gl/posting.go:113-188).
	var invID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, subtotal_cents, tax_cents, total_cents, balance_cents, due_date, finalized_at)
		VALUES ('#INV-FW1-CENSUS-1', $1::uuid, $2::uuid, 'FINALIZED', 10000, 1300, 11300, 11300, '2026-07-15', $3)
		RETURNING id::text
	`, customerID, trtBranchID, pinnedNow).Scan(&invID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	jid := dispatchTopic("ar.invoice.finalized", map[string]any{"invoice_id": invID}, "ar.invoice.finalized:"+invID)
	record("outbox_invoice_finalized", "core/internal/gl/posting.go:113-188 (buildInvoiceFinalized)", jid)

	// 8. Outbox payment.received (gl/posting.go:195-242).
	var pmtID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id, received_at)
		VALUES ('#PMT-FW1-CENSUS-1', $1::uuid, 'CASH', 11300, 'RECEIVED', $2::uuid, $3)
		RETURNING id::text
	`, customerID, trtBranchID, pinnedNow).Scan(&pmtID); err != nil {
		t.Fatalf("seed payment: %v", err)
	}
	jid = dispatchTopic("payment.received", map[string]any{"payment_id": pmtID, "method": "CASH"}, "payment.received:"+pmtID)
	record("outbox_payment_received", "core/internal/gl/posting.go:195-242 (buildPaymentReceived)", jid)

	// 9. Outbox tax.relief.assessed (gl/posting.go:248-297).
	var orderID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, subtotal_cents, tax_cents, total_cents)
		VALUES ('#ORD-FW1-CENSUS-1', $1::uuid, $2::uuid, 'CONFIRMED', 10000, 500, 10500)
		RETURNING id::text
	`, customerID, trtBranchID).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO tax_decision_snapshot (order_id, tax_reason_code, full_hst_cents, on_credit_cents, net_collected_cents)
		VALUES ($1::uuid, 'ON_FN_POS_8', 1300, 800, 500)
	`, orderID); err != nil {
		t.Fatalf("seed tax_decision_snapshot: %v", err)
	}
	jid = dispatchTopic("tax.relief.assessed", map[string]any{"order_id": orderID}, "tax.relief.assessed:"+orderID)
	record("outbox_tax_relief_assessed", "core/internal/gl/posting.go:248-297 (buildTaxRelief)", jid)

	// 10. Outbox payment.bounced (gl/posting.go:327-417).
	var bouncedPmtID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id, received_at)
		VALUES ('#PMT-FW1-CENSUS-BNC', $1::uuid, 'CHEQUE', 8000, 'BOUNCED', $2::uuid, $3)
		RETURNING id::text
	`, customerID, trtBranchID, pinnedNow).Scan(&bouncedPmtID); err != nil {
		t.Fatalf("seed bounced payment: %v", err)
	}
	jid = dispatchTopic("payment.bounced", map[string]any{
		"payment_id":   bouncedPmtID,
		"branch_id":    trtBranchID,
		"method":       "CHEQUE",
		"amount_cents": 8000,
		"fee_cents":    4500,
		"fee_taxable":  false,
		"cleared":      false,
	}, "payment.bounced:"+bouncedPmtID)
	record("outbox_payment_bounced", "core/internal/gl/posting.go:327-417 (buildPaymentBounced)", jid)

	// 11. Outbox payment.reversed (gl/posting.go:445-497).
	var revPmtID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id, received_at)
		VALUES ('#PMT-FW1-CENSUS-REV', $1::uuid, 'CASH', 5000, 'VOID', $2::uuid, $3)
		RETURNING id::text
	`, customerID, trtBranchID, pinnedNow).Scan(&revPmtID); err != nil {
		t.Fatalf("seed reversal payment: %v", err)
	}
	jid = dispatchTopic("payment.reversed", map[string]any{
		"payment_id":          revPmtID,
		"reverses_payment_id": pmtID,
		"amount_cents":        5000,
		"method":              "CASH",
		"branch_id":           trtBranchID,
	}, "payment.reversed:"+revPmtID)
	record("outbox_payment_reversed", "core/internal/gl/posting.go:445-497 (buildPaymentReversed)", jid)

	// 12. Outbox payment.reapplied (gl/posting.go:513-549).
	batchID := uuid.NewString()
	jid = dispatchTopic("payment.reapplied", map[string]any{
		"batch_id":     batchID,
		"payment_id":   pmtID,
		"amount_cents": 5000,
		"method":       "CASH",
		"branch_id":    trtBranchID,
	}, "payment.reapplied:"+batchID)
	record("outbox_payment_reapplied", "core/internal/gl/posting.go:513-549 (buildPaymentReapplied)", jid)

	// 13. Outbox order.fulfilled (gl/posting.go:560-619).
	jid = dispatchTopic("order.fulfilled", map[string]any{
		"order_id":  orderID,
		"branch_id": trtBranchID,
		"lines": []map[string]any{
			{"product_id": uuid.NewString(), "qty_base": 2},
		},
	}, "order.fulfilled:"+orderID)
	record("outbox_order_fulfilled", "core/internal/gl/posting.go:560-619 (buildOrderFulfilled)", jid)

	// 14. Outbox deposit.released (gl/posting.go:638-688).
	var depInvID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, source_kind, subtotal_cents, tax_cents, total_cents, balance_cents, due_date, finalized_at)
		VALUES ('#INV-FW1-CENSUS-DEP1', $1::uuid, $2::uuid, 'FINALIZED', 'SUPPLIER_DEPOSIT', 6000, 0, 6000, 6000, '2026-07-15', $3)
		RETURNING id::text
	`, customerID, trtBranchID, pinnedNow).Scan(&depInvID); err != nil {
		t.Fatalf("seed deposit invoice 1: %v", err)
	}
	jid = dispatchTopic("deposit.released", map[string]any{
		"invoice_id": depInvID,
		"order_id":   orderID,
	}, "deposit.released:"+depInvID)
	record("outbox_deposit_released", "core/internal/gl/posting.go:638-688 (buildDepositReleased)", jid)

	// 15. Outbox deposit.cancelled (gl/posting.go:718-805).
	var depCancelInvID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, source_kind, subtotal_cents, tax_cents, total_cents, balance_cents, due_date, finalized_at)
		VALUES ('#INV-FW1-CENSUS-DEP2', $1::uuid, $2::uuid, 'FINALIZED', 'SUPPLIER_DEPOSIT', 7000, 0, 7000, 7000, '2026-07-15', $3)
		RETURNING id::text
	`, customerID, trtBranchID, pinnedNow).Scan(&depCancelInvID); err != nil {
		t.Fatalf("seed deposit invoice 2: %v", err)
	}
	jid = dispatchTopic("deposit.cancelled", map[string]any{
		"invoice_id":   depCancelInvID,
		"order_id":     orderID,
		"disposition":  "CREDIT_BACK",
		"paid_cents":   0,
		"closed_cents": 7000,
	}, "deposit.cancelled:"+depCancelInvID)
	record("outbox_deposit_cancelled", "core/internal/gl/posting.go:718-805 (buildDepositCancelled)", jid)

	// 16. Outbox pos.return.committed (gl/posting.go:831-925).
	retID := uuid.NewString()
	jid = dispatchTopic("pos.return.committed", map[string]any{
		"return_id":      retID,
		"order_id":       orderID,
		"invoice_id":     invID,
		"refund_method":  "CASH",
		"subtotal_cents": 2000,
		"tax_cents":      260,
		"total_cents":    2260,
		"branch_id":      trtBranchID,
	}, "pos.return.committed:"+retID)
	record("outbox_pos_return_committed", "core/internal/gl/posting.go:831-925 (buildPosReturnCommitted)", jid)

	// 17. Outbox order.returned (gl/posting.go:940-986).
	jid = dispatchTopic("order.returned", map[string]any{
		"order_id":  orderID,
		"return_id": retID,
		"branch_id": trtBranchID,
		"lines": []map[string]any{
			{"product_id": uuid.NewString(), "qty_base": 1},
		},
	}, "order.returned:"+retID)
	record("outbox_order_returned", "core/internal/gl/posting.go:940-986 (buildOrderReturned)", jid)

	// 18. Outbox pos.return.written_off (gl/posting.go:992-1038).
	woRetID := uuid.NewString()
	jid = dispatchTopic("pos.return.written_off", map[string]any{
		"order_id":  orderID,
		"return_id": woRetID,
		"branch_id": trtBranchID,
		"lines": []map[string]any{
			{"product_id": uuid.NewString(), "qty_base": 1},
		},
	}, "pos.return.written_off:"+woRetID)
	record("outbox_pos_return_written_off", "core/internal/gl/posting.go:992-1038 (buildPosReturnWrittenOff)", jid)

	// 19. Outbox till.session.completed (gl/posting.go:1046-1085).
	sessID := uuid.NewString()
	jid = dispatchTopic("till.session.completed", map[string]any{
		"session_id":       sessID,
		"branch_id":        trtBranchID,
		"over_short_cents": -500,
	}, "till.session.completed:"+sessID)
	record("outbox_till_session_completed", "core/internal/gl/posting.go:1046-1085 (buildTillSessionCompleted)", jid)

	// 20. Bankrec auto-match payment (bankrec/service.go:209 -> matcher.go:128-158).
	var autoPmtID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id, gateway_ref, received_at)
		VALUES ('#PMT-FW1-AUTO-1', $1::uuid, 'CARD', 19500, 'RECEIVED', $2::uuid, 'GTW-FW1-AUTO-1', $3)
		RETURNING id::text
	`, customerID, trtBranchID, pinnedNow).Scan(&autoPmtID); err != nil {
		t.Fatalf("seed auto card payment: %v", err)
	}
	var autoPmtLineID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO bank_statement_lines (bank_account_id, txn_date, description, reference, amount_cents, dedupe_hash, status)
		VALUES ($1::uuid, '2026-06-15', 'MONERIS SETTLEMENT', 'GTW-FW1-AUTO-1', 19500, 'hash-fw1-auto-pmt', 'UNMATCHED')
		RETURNING id::text
	`, rbcAccountID).Scan(&autoPmtLineID); err != nil {
		t.Fatalf("seed auto payment statement line: %v", err)
	}
	sumPmt, err := bankrecSvc.AutoMatchReach(ctx, rbcAccountID, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), userID, bankrec.AllBranches)
	if err != nil || len(sumPmt.Matches) != 1 {
		t.Fatalf("bankrec_auto_match_payment: matches=%d err=%v", len(sumPmt.Matches), err)
	}
	record("bankrec_auto_match_payment", "core/internal/bankrec/matcher.go:128-158 (buildPaymentProposal)", sumPmt.Matches[0].JournalID)

	// 21. Bankrec auto-match lodgement (bankrec/service.go:209 -> matcher.go:162-180).
	var tillSessID, autoLodgID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO till_sessions (branch_id, opened_by, status)
		VALUES ($1::uuid, $2::uuid, 'OPEN')
		RETURNING id::text
	`, trtBranchID, userID).Scan(&tillSessID); err != nil {
		t.Fatalf("seed till session: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO till_movements (till_session_id, movement_type, amount_cents, reference, created_at)
		VALUES ($1::uuid, 'LODGEMENT', 24000, 'SLIP-FW1-AUTO-1', $2)
		RETURNING id::text
	`, tillSessID, pinnedNow).Scan(&autoLodgID); err != nil {
		t.Fatalf("seed auto lodgement: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO bank_statement_lines (bank_account_id, txn_date, description, reference, amount_cents, dedupe_hash, status)
		VALUES ($1::uuid, '2026-06-15', 'BRANCH DEPOSIT', 'SLIP-FW1-AUTO-1', 24000, 'hash-fw1-auto-lodg', 'UNMATCHED')
	`, rbcAccountID); err != nil {
		t.Fatalf("seed auto lodgement statement line: %v", err)
	}
	sumLodg, err := bankrecSvc.AutoMatchReach(ctx, rbcAccountID, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), userID, bankrec.AllBranches)
	if err != nil || len(sumLodg.Matches) != 1 {
		t.Fatalf("bankrec_auto_match_lodgement: matches=%d err=%v", len(sumLodg.Matches), err)
	}
	record("bankrec_auto_match_lodgement", "core/internal/bankrec/matcher.go:162-180 (buildLodgementProposal)", sumLodg.Matches[0].JournalID)

	// 22. Bankrec manual match payment (bankrec/service.go:289 -> matcher.go:184-194).
	var manPmtID, manPmtLineID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id, gateway_ref, received_at)
		VALUES ('#PMT-FW1-MAN-1', $1::uuid, 'CARD', 31000, 'RECEIVED', $2::uuid, 'GTW-FW1-MAN-1', $3)
		RETURNING id::text
	`, customerID, trtBranchID, pinnedNow).Scan(&manPmtID); err != nil {
		t.Fatalf("seed manual card payment: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO bank_statement_lines (bank_account_id, txn_date, description, reference, amount_cents, dedupe_hash, status)
		VALUES ($1::uuid, '2026-06-16', 'MANUAL CARD SETTLEMENT', 'REF-MAN-PMT', 31000, 'hash-fw1-man-pmt', 'UNMATCHED')
		RETURNING id::text
	`, rbcAccountID).Scan(&manPmtLineID); err != nil {
		t.Fatalf("seed manual payment statement line: %v", err)
	}
	manPmtMatch, err := bankrecSvc.ManualMatch(ctx, bankrec.MatchInput{
		StatementLineID: manPmtLineID,
		TargetType:      bankrec.TargetPayment,
		TargetRefID:     manPmtID,
		CanAccessBranch: bankrec.AllBranches,
	}, userID)
	if err != nil {
		t.Fatalf("bankrec_manual_match_payment: %v", err)
	}
	record("bankrec_manual_match_payment", "core/internal/bankrec/service.go:289,matcher.go:184-194 (manualJournal PAYMENT)", manPmtMatch.JournalID)

	// 23. Bankrec manual match lodgement (bankrec/service.go:289 -> matcher.go:195-204).
	var manLodgID, manLodgLineID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO till_movements (till_session_id, movement_type, amount_cents, reference, created_at)
		VALUES ($1::uuid, 'LODGEMENT', 40000, 'SLIP-FW1-MAN-1', $2)
		RETURNING id::text
	`, tillSessID, pinnedNow).Scan(&manLodgID); err != nil {
		t.Fatalf("seed manual lodgement: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO bank_statement_lines (bank_account_id, txn_date, description, reference, amount_cents, dedupe_hash, status)
		VALUES ($1::uuid, '2026-06-16', 'MANUAL DEPOSIT', 'REF-MAN-LODG', 40000, 'hash-fw1-man-lodg', 'UNMATCHED')
		RETURNING id::text
	`, rbcAccountID).Scan(&manLodgLineID); err != nil {
		t.Fatalf("seed manual lodgement statement line: %v", err)
	}
	manLodgMatch, err := bankrecSvc.ManualMatch(ctx, bankrec.MatchInput{
		StatementLineID: manLodgLineID,
		TargetType:      bankrec.TargetLodgement,
		TargetRefID:     manLodgID,
		CanAccessBranch: bankrec.AllBranches,
	}, userID)
	if err != nil {
		t.Fatalf("bankrec_manual_match_lodgement: %v", err)
	}
	record("bankrec_manual_match_lodgement", "core/internal/bankrec/service.go:289,matcher.go:195-204 (manualJournal LODGEMENT)", manLodgMatch.JournalID)

	// 24. Bankrec unmatch reversal (bankrec/service.go:437 -> matcher.go:212-234).
	if err := bankrecSvc.UnmatchReach(ctx, sumPmt.Matches[0].ID, userID, bankrec.AllBranches); err != nil {
		t.Fatalf("bankrec_unmatch_reversal: %v", err)
	}
	var unmatchJournalID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM gl_journals WHERE idempotency_key = $1`, "bankrec:unmatch:"+sumPmt.Matches[0].ID).Scan(&unmatchJournalID); err != nil {
		t.Fatalf("lookup unmatch journal: %v", err)
	}
	record("bankrec_unmatch_reversal", "core/internal/bankrec/service.go:437,matcher.go:212-234 (reversalJournal)", unmatchJournalID)

	// 25. External credit memo poster (cmd/core/tops_adapters.go:1220-1255).
	cmPoster := w6cCreditMemoPoster{
		svc:   glSvc,
		rules: glRepo,
		words: newTaxWords(pool),
	}
	cmID := uuid.NewString()
	cmJournalID, err := cmPoster.PostCreditMemoJournal(ctx, billing.CreditMemoJournal{
		CreditMemoID:   cmID,
		CreditMemoNo:   "#CM-FW1-CENSUS-1",
		BranchID:       trtBranchID,
		IdempotencyKey: "ar.credit_memo.issued:" + cmID,
		Memo:           "fw1 census credit memo",
		ARCents:        3390,
		RevenueCents:   3000,
		TaxCents:       390,
	})
	if err != nil {
		t.Fatalf("external_credit_memo_poster: %v", err)
	}
	record("external_credit_memo_poster", "core/cmd/core/tops_adapters.go:1220-1255 (w6cCreditMemoPoster.PostCreditMemoJournal)", cmJournalID)

	// 26. External PostExternalTx (gl/module.go:189-215, shared by inventory/invcount/purchasing/landedcost/lien/warranty).
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx for PostExternalTx: %v", err)
	}
	extTxJID, err := glSvc.PostExternalTx(ctx, tx, gl.ExternalJournal{
		Source:         gl.SourceAdjustment,
		SourceRefID:    uuid.NewString(),
		IdempotencyKey: "fw1-census:post-external-tx",
		Memo:           "fw1 census PostExternalTx with branch",
		BranchID:       trtBranchID,
		Lines: []gl.ExternalLine{
			{AccountCode: "1200", DebitCents: 15000, Memo: "inventory"},
			{AccountCode: "2100", CreditCents: 15000, Memo: "ap"},
		},
	})
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("external_post_external_tx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit PostExternalTx: %v", err)
	}
	record("external_post_external_tx", "core/internal/gl/module.go:372-374,posting.go:1099-1146 (PostExternalTx)", extTxJID)

	// 27. Year-end close (gl/close.go:159-212). The census closes a year of
	// its own, not the seeded 2026: FW1-09b round 2 refuses a year end whose
	// year has not ended in the dealer's zone, and the seeded year stands mid
	// year on the real clock. The leg plants 2020's twelve periods, gives its
	// Trenton books a P&L through PostExternal (dated by the document, the
	// ruling 82.2 seam), closes THAT year, and reads its closing journal back.
	// The census database is the test's own and dropped afterwards.
	const censusFiscalYear = 2020
	if _, err := pool.Exec(ctx, `
		INSERT INTO fiscal_periods (year, month)
		SELECT $1, m FROM generate_series(1, 12) AS m
		ON CONFLICT (year, month) DO NOTHING
	`, censusFiscalYear); err != nil {
		t.Fatalf("ensure %d periods: %v", censusFiscalYear, err)
	}
	if _, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		Source:         gl.SourceAdjustment,
		IdempotencyKey: "fw1-census:year-end-pnl",
		Memo:           "fw1 census year end P&L",
		BranchID:       trtBranchID,
		DocumentDate:   time.Date(censusFiscalYear, 6, 15, 12, 0, 0, 0, time.UTC),
		Lines: []gl.ExternalLine{
			{AccountCode: "1000", DebitCents: 5000, Memo: "bank"},
			{AccountCode: "4000", CreditCents: 5000, Memo: "revenue"},
		},
	}); err != nil {
		t.Fatalf("year end P&L: %v", err)
	}
	closeRes, err := glSvc.CloseYear(ctx, censusFiscalYear, "fw1 census year-end close")
	if err != nil {
		t.Fatalf("year_end_close: %v", err)
	}
	record("year_end_close", "core/internal/gl/close.go:159-212 (CloseYear)", closeRes.ClosingJournalID)

	// Verify that every writer in fw1BranchCensusWriters was exercised.
	exercised := make(map[string]bool, len(results))
	for _, r := range results {
		exercised[r.name] = true
	}
	for _, w := range fw1BranchCensusWriters {
		if !exercised[w] {
			t.Fatalf("writer %q in fw1BranchCensusWriters was not exercised by TestFW1JournalLinesCarryBranch", w)
		}
	}

	skipNone := os.Getenv("FW1_SKIP_NONE") == "1"
	accountedNullJournals := make(map[string]string)

	for _, r := range results {
		var totalLines, nullLines int
		if err := pool.QueryRow(ctx, `
			SELECT COUNT(*), COUNT(*) FILTER (WHERE branch_id IS NULL)
			FROM gl_journal_lines
			WHERE journal_id = ANY($1::uuid[])
		`, r.journalIDs).Scan(&totalLines, &nullLines); err != nil {
			t.Fatalf("check lines for writer %s: %v", r.name, err)
		}
		if totalLines < 2 {
			t.Fatalf("writer %s (%s) wrote %d gl_journal_lines rows, want >= 2", r.name, r.sourceSite, totalLines)
		}

		owner, isKnownRed := fw1BranchCensusRed[r.name]
		if !skipNone && isKnownRed {
			if nullLines == 0 {
				t.Errorf("STALE SKIP [%s] (owner %s): %s now stamps branch_id on all %d gl_journal_lines rows; remove %q from fw1BranchCensusRed",
					r.name, owner, r.sourceSite, totalLines, r.name)
			} else {
				for _, jid := range r.journalIDs {
					accountedNullJournals[jid] = r.name
				}
				t.Logf("KNOWN RED [%s] (owner %s): %s left %d/%d gl_journal_lines rows with branch_id IS NULL",
					r.name, owner, r.sourceSite, nullLines, totalLines)
			}
			continue
		}

		if nullLines > 0 {
			t.Errorf("RED [%s] (%s): left %d/%d gl_journal_lines rows with branch_id IS NULL (journal_ids=%v)",
				r.name, r.sourceSite, nullLines, totalLines, r.journalIDs)
		}
	}

	// Database-wide sweep: assert no unaccounted gl_journal_lines row has branch_id IS NULL.
	sweepRows, err := pool.Query(ctx, `
		SELECT j.id::text, j.source, COALESCE(j.idempotency_key, ''), COALESCE(j.memo, ''), COUNT(*)
		FROM gl_journal_lines jl
		JOIN gl_journals j ON j.id = jl.journal_id
		WHERE jl.branch_id IS NULL
		GROUP BY j.id, j.source, j.idempotency_key, j.memo
		ORDER BY j.id
	`)
	if err != nil {
		t.Fatalf("database-wide NULL branch_id sweep: %v", err)
	}
	defer sweepRows.Close()
	for sweepRows.Next() {
		var jid, source, key, memo string
		var cnt int
		if err := sweepRows.Scan(&jid, &source, &key, &memo, &cnt); err != nil {
			t.Fatalf("scan NULL branch_id sweep row: %v", err)
		}
		if _, ok := accountedNullJournals[jid]; !ok {
			t.Errorf("DB SWEEP RED: journal %s (source=%s key=%q memo=%q) has %d gl_journal_lines rows with branch_id IS NULL",
				jid, source, key, memo, cnt)
		}
	}
}
