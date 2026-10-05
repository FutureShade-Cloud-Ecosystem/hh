//go:build integration

// receivables_r3_integration_test.go: the round 3 legs of the open
// receivables door (HH-LOAD-1 LD-11, the review's findings), each
// written red first. Every fixture is invented.
package load_test

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
)

// ld11RecordingPoster records every journal the repost job asks for.
type ld11RecordingPoster struct{ posted []billing.CreditMemoJournal }

func (p *ld11RecordingPoster) PostCreditMemoJournal(_ context.Context, in billing.CreditMemoJournal) (string, error) {
	p.posted = append(p.posted, in)
	return "journal-fixture", nil
}

// P1-1: the nightly credit memo repost job never journals a loaded
// credit memo, and still journals one HH raised itself.
func TestReceivablesNightlyRepostNeverSweepsALoadedCreditMemo(t *testing.T) {
	e := setupLD11(t)
	ctx := context.Background()
	if _, err := e.door.UpsertReceivables(ctx, ld11Batch("b-r3-repost",
		ld11Item("RP-1", "CUST-ONE", "BR-A", "RP-DOC-1", "2022-06-01", "2022-07-01", -4321, "", ""))); err != nil {
		t.Fatalf("load: %v", err)
	}
	poster := &ld11RecordingPoster{}
	res, err := billing.NewInvoiceWriter(e.pool).WithCreditMemoPoster(poster).RepostFailedCreditMemoJournals(ctx, 0)
	if err != nil {
		t.Fatalf("repost: %v", err)
	}
	for _, m := range poster.posted {
		if m.ARCents == 4321 {
			t.Fatalf("the nightly repost job asked the GL to post a journal for the loaded credit memo (result %+v)", res)
		}
	}
}

// P1-2: a replay is graded against what that batch recorded, not
// against the registry's current stamp.
func TestReceivablesReplayIsGradedAgainstWhatThatBatchRecorded(t *testing.T) {
	e := setupLD11(t)
	ctx := context.Background()
	a := ld11Item("RV-A", "CUST-ONE", "BR-A", "RV-DOC-A", "2022-06-01", "2022-07-01", 1000, "", "")
	b := ld11Item("RV-B", "CUST-ONE", "BR-A", "RV-DOC-B", "2022-06-02", "2022-07-02", 2000, "", "")
	first, err := e.door.UpsertReceivables(ctx, ld11Batch("rv-r1", a))
	if err != nil {
		t.Fatalf("rv-r1: %v", err)
	}
	if _, err := e.door.UpsertReceivables(ctx, ld11Batch("rv-r2", b)); err != nil {
		t.Fatalf("rv-r2: %v", err)
	}
	// Shape one: the replay carries an item another batch recorded,
	// with its amount changed.
	bChanged := ld11Item("RV-B", "CUST-ONE", "BR-A", "RV-DOC-B", "2022-06-02", "2022-07-02", 2500, "", "")
	_, err = e.door.UpsertReceivables(ctx, ld11Batch("rv-r1", a, bChanged))
	ref := ld11BatchRefused(t, err)
	if ref.Refusing.Reason != load.ReasonReplayChanged || ref.Refusing.LegacyID != "RV-B" {
		t.Fatalf("shape one answered %+v, want %s naming RV-B", ref.Refusing, load.ReasonReplayChanged)
	}
	// Shape two: another batch restamps A unchanged, then the replay of
	// rv-r1 carries A with a changed amount.
	if _, err := e.door.UpsertReceivables(ctx, ld11Batch("rv-r3", a)); err != nil {
		t.Fatalf("rv-r3: %v", err)
	}
	aChanged := ld11Item("RV-A", "CUST-ONE", "BR-A", "RV-DOC-A", "2022-06-01", "2022-07-01", 1500, "", "")
	_, err = e.door.UpsertReceivables(ctx, ld11Batch("rv-r1", aChanged))
	ref = ld11BatchRefused(t, err)
	if ref.Refusing.Reason != load.ReasonReplayChanged || ref.Refusing.LegacyID != "RV-A" {
		t.Fatalf("shape two answered %+v, want %s naming RV-A", ref.Refusing, load.ReasonReplayChanged)
	}
	// The faithful replay still answers the first outcomes and writes nothing.
	rowsBefore := e.count(t, `SELECT count(*) FROM invoices WHERE org_id = $1::uuid AND source_kind = 'IMPORT'`, ld11OrgID)
	again, err := e.door.UpsertReceivables(ctx, ld11Batch("rv-r1", a))
	if err != nil {
		t.Fatalf("faithful replay: %v", err)
	}
	if len(again.Items) != 1 || again.Items[0] != first.Items[0] {
		t.Fatalf("the faithful replay answered %+v, want the first outcomes %+v", again.Items, first.Items)
	}
	if n := e.count(t, `SELECT count(*) FROM invoices WHERE org_id = $1::uuid AND source_kind = 'IMPORT'`, ld11OrgID); n != rowsBefore {
		t.Fatalf("the faithful replay wrote rows: %d, want %d", n, rowsBefore)
	}
	if inv := e.invoices(t); inv["RV-DOC-A"].Balance != 1000 || inv["RV-DOC-B"].Balance != 2000 {
		t.Fatalf("the refused replays moved money: %+v", inv)
	}
}

// ld11ShownBalance is the customer module's own query shape: the
// balance_after of the row with the latest occurred_at, no tie break
// (core/internal/customer/repository.go, account_lookup.go; the till's
// and the quote desk's credit reads read the same way).
func (e *ld11Env) shownBalance(t *testing.T, customerID string) int64 {
	t.Helper()
	var shown int64
	if err := e.pool.QueryRow(context.Background(), `
		SELECT COALESCE((SELECT t.balance_after FROM customer_transactions t
		                  WHERE t.customer_id = c.id ORDER BY t.occurred_at DESC LIMIT 1), 0)
		  FROM customers c WHERE c.id = $1::uuid`, customerID).Scan(&shown); err != nil {
		t.Fatalf("shown balance read: %v", err)
	}
	return shown
}

func (e *ld11Env) openTotal(t *testing.T, customerID string) int64 {
	t.Helper()
	var open int64
	if err := e.pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(balance_cents),0) FROM invoices WHERE customer_id = $1::uuid AND status IN ('FINALIZED','PARTIALLY_PAID')`,
		customerID).Scan(&open); err != nil {
		t.Fatalf("open total read: %v", err)
	}
	return open
}

// P1-3: same day items keep the recorded day and the write order, and
// the customer module's own query shape shows the open total.
func TestReceivablesSameDayItemsShowTheOpenTotalToTheCustomerReaders(t *testing.T) {
	e := setupLD11(t)
	ctx := context.Background()
	if _, err := e.door.UpsertReceivables(ctx, ld11Batch("b-sd-1",
		ld11Item("SD-1", "CUST-ONE", "BR-A", "SD-DOC-1", "2022-06-01", "2022-07-01", 5000, "", ""),
		ld11Item("SD-2", "CUST-ONE", "BR-A", "SD-DOC-2", "2022-06-01", "2022-07-01", 7000, "", ""),
		ld11Item("SD-3", "CUST-ONE", "BR-A", "SD-DOC-3", "2022-06-01", "2022-07-01", 9000, "", ""))); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	if got, want := e.shownBalance(t, ld11CustOneID), e.openTotal(t, ld11CustOneID); got != want || want != 21000 {
		t.Fatalf("after three same day items the reader shows %d, open total %d, want 21000", got, want)
	}
	ledger := e.ledger(t, ld11CustOneID)
	for i, r := range ledger {
		if r.OccurredAt.UTC().Format("2006-01-02") != "2022-06-01" {
			t.Fatalf("row %d (%s) left its recorded issue day: %s", i, r.SourceDoc, r.OccurredAt)
		}
		if i > 0 && !r.OccurredAt.After(ledger[i-1].OccurredAt) {
			t.Fatalf("rows %d and %d tie on occurred_at: %s", i-1, i, r.OccurredAt)
		}
	}
	for i, want := range []string{"SD-DOC-1", "SD-DOC-2", "SD-DOC-3"} {
		if ledger[i].SourceDoc != want {
			t.Fatalf("ledger position %d is %s, want %s in write order", i, ledger[i].SourceDoc, want)
		}
	}
	ld11AssertRunningBalances(t, ledger)

	// The restating batch shape: a later batch adds an older item and
	// moves two items onto the tied day.
	if _, err := e.door.UpsertReceivables(ctx, ld11Batch("b-sd-2",
		ld11Item("SD-4", "CUST-ONE", "BR-A", "SD-DOC-4", "2022-05-20", "2022-06-20", 1000, "", ""),
		ld11Item("SD-5", "CUST-ONE", "BR-A", "SD-DOC-5", "2022-06-15", "2022-07-15", 2000, "", ""),
		ld11Item("SD-6", "CUST-ONE", "BR-A", "SD-DOC-6", "2022-06-15", "2022-07-15", 3000, "", ""))); err != nil {
		t.Fatalf("second batch: %v", err)
	}
	moved := ld11Item("SD-5", "CUST-ONE", "BR-A", "SD-DOC-5", "2022-06-01", "2022-07-01", 2500, "", "")
	moved2 := ld11Item("SD-6", "CUST-ONE", "BR-A", "SD-DOC-6", "2022-06-01", "2022-07-01", 3500, "", "")
	if _, err := e.door.UpsertReceivables(ctx, ld11Batch("b-sd-3", moved, moved2)); err != nil {
		t.Fatalf("restating batch: %v", err)
	}
	if got, want := e.shownBalance(t, ld11CustOneID), e.openTotal(t, ld11CustOneID); got != want {
		t.Fatalf("after the restating batch the reader shows %d, open total %d", got, want)
	}
	ledger = e.ledger(t, ld11CustOneID)
	ld11AssertRunningBalances(t, ledger)
	for i := 1; i < len(ledger); i++ {
		if !ledger[i].OccurredAt.After(ledger[i-1].OccurredAt) {
			t.Fatalf("rows %d and %d tie on occurred_at after the restating batch", i-1, i)
		}
	}
}

// P2-1: a restated amount that changes sign on an update is refused: a
// source item that changed sign is a different document.
func TestReceivablesSignChangeOnUpdateRefusesWithItsOwnReason(t *testing.T) {
	e := setupLD11(t)
	ctx := context.Background()
	if _, err := e.door.UpsertReceivables(ctx, ld11Batch("b-sg-1",
		ld11Item("SG-1", "CUST-ONE", "BR-A", "SG-DOC-1", "2022-06-01", "2022-07-01", 5000, "", ""))); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	_, err := e.door.UpsertReceivables(ctx, ld11Batch("b-sg-2",
		ld11Item("SG-1", "CUST-ONE", "BR-A", "SG-DOC-1", "2022-06-01", "2022-07-01", -2500, "", "")))
	ref := ld11BatchRefused(t, err)
	if ref.Refusing.Reason != load.ReasonSignChanged || ref.Refusing.Field != "open_amount_cents" || ref.Refusing.LegacyID != "SG-1" {
		t.Fatalf("the sign change answered %+v, want %s naming open_amount_cents", ref.Refusing, load.ReasonSignChanged)
	}
	if inv := e.invoices(t)["SG-DOC-1"]; inv.DocType != "INVOICE" || inv.Total != 5000 || len(inv.InvoiceNo) < 5 || inv.InvoiceNo[:5] != "#INV-" {
		t.Fatalf("the refused sign change moved the document: %+v", inv)
	}
}

// P3-1: update_target_missing stays for a registry name whose document
// is truly gone.
func TestReceivablesGoneTargetKeepsUpdateTargetMissing(t *testing.T) {
	e := setupLD11(t)
	ctx := context.Background()
	if _, err := e.door.UpsertReceivables(ctx, ld11Batch("b-gn-1",
		ld11Item("GN-1", "CUST-ONE", "BR-A", "GN-DOC-1", "2022-06-01", "2022-07-01", 5000, "", ""))); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	if _, err := e.pool.Exec(ctx, `DELETE FROM customer_transactions WHERE source_ref_id IN (SELECT id FROM invoices WHERE source_doc_no = 'GN-DOC-1' AND org_id = $1::uuid)`, ld11OrgID); err != nil {
		t.Fatalf("remove the ledger row: %v", err)
	}
	if _, err := e.pool.Exec(ctx, `DELETE FROM invoices WHERE source_doc_no = 'GN-DOC-1' AND org_id = $1::uuid`, ld11OrgID); err != nil {
		t.Fatalf("remove the document: %v", err)
	}
	_, err := e.door.UpsertReceivables(ctx, ld11Batch("b-gn-2",
		ld11Item("GN-1", "CUST-ONE", "BR-A", "GN-DOC-1", "2022-06-01", "2022-07-01", 6000, "", "")))
	ref := ld11BatchRefused(t, err)
	if ref.Refusing.Reason != load.ReasonUpdateTargetMissing {
		t.Fatalf("a gone target answered %+v, want %s", ref.Refusing, load.ReasonUpdateTargetMissing)
	}
}

// P3-3: the writer's open balance read carries the org predicate: a
// FINALIZED document of another org that happens to name the customer
// never enters the loading org's running balance.
func TestReceivablesOpenBalanceReadCarriesTheOrgPredicate(t *testing.T) {
	e := setupLD11(t)
	ctx := context.Background()
	const otherOrg = "6a110000-0000-4000-8000-0000000000a3"
	for _, stmt := range []string{
		`INSERT INTO org (id, slug, name) VALUES ('` + otherOrg + `', 'ld11-other-bal', 'LD11 Other Balance Yard') ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date, subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, org_id)
		 VALUES ('#INV-LD11-X1', '` + ld11CustOneID + `', '` + ld11BranchAID + `', 'FINALIZED', '2022-01-01', '2022-02-01', 900, 0, 900, 900, now(), '` + otherOrg + `')`,
	} {
		if _, err := e.pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(ctx, `DELETE FROM invoices WHERE invoice_no = '#INV-LD11-X1'`)
		_, _ = e.pool.Exec(ctx, `DELETE FROM org WHERE id = '`+otherOrg+`'::uuid`)
	})
	if _, err := e.door.UpsertReceivables(ctx, ld11Batch("b-og-1",
		ld11Item("OG-1", "CUST-ONE", "BR-A", "OG-DOC-1", "2022-06-01", "2022-07-01", 5000, "", ""))); err != nil {
		t.Fatalf("load: %v", err)
	}
	ledger := e.ledger(t, ld11CustOneID)
	if len(ledger) != 1 || ledger[0].BalanceAfter != 5000 {
		t.Fatalf("the loaded row's running balance is %+v, want 5000: another org's document leaked in", ledger)
	}
}
