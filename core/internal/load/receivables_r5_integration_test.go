//go:build integration

// receivables_r5_integration_test.go: the round 5 legs of the open
// receivables door (HH-LOAD-1 LD-11, the second review's findings), each
// written red first. Every fixture is invented.
package load_test

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
)

// P1: a replay whose item count differs from what the batch recorded is
// refused with the replay-changed answer before any outcome is built; a
// reordered replay of the same items is still faithful.
func TestReceivablesReplayWithADifferentItemCountRefuses(t *testing.T) {
	e := setupLD11(t)
	ctx := context.Background()
	a := ld11Item("RC-A", "CUST-ONE", "BR-A", "RC-DOC-A", "2020-06-01", "2020-07-01", 1000, "", "")
	b := ld11Item("RC-B", "CUST-ONE", "BR-A", "RC-DOC-B", "2020-06-02", "2020-07-02", 2000, "", "")
	c := ld11Item("RC-C", "CUST-ONE", "BR-A", "RC-DOC-C", "2020-06-03", "2020-07-03", 3000, "", "")
	first, err := e.door.UpsertReceivables(ctx, ld11Batch("rc-1", a, b))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	rows := e.count(t, `SELECT count(*) FROM invoices WHERE org_id = $1::uuid AND source_kind = 'IMPORT'`, ld11OrgID)
	audits := e.audit.len()

	// Subset: A alone of A and B.
	res, err := e.door.UpsertReceivables(ctx, ld11Batch("rc-1", a))
	ref := ld11BatchRefused(t, err)
	if ref.Refusing.Reason != load.ReasonReplayChanged || ref.Refusing.LegacyID != "RC-B" {
		t.Fatalf("the subset replay answered %+v, want %s naming the dropped item RC-B", ref.Refusing, load.ReasonReplayChanged)
	}
	if len(res.Items) != 0 {
		t.Fatalf("the subset replay built %d outcomes before refusing", len(res.Items))
	}
	// Superset: A, B and a third item the batch never recorded.
	_, err = e.door.UpsertReceivables(ctx, ld11Batch("rc-1", a, b, c))
	ref = ld11BatchRefused(t, err)
	if ref.Refusing.Reason != load.ReasonReplayChanged || ref.Refusing.LegacyID != "RC-C" {
		t.Fatalf("the superset replay answered %+v, want %s naming RC-C", ref.Refusing, load.ReasonReplayChanged)
	}
	// Reordered: still faithful, the first outcomes come back.
	again, err := e.door.UpsertReceivables(ctx, ld11Batch("rc-1", b, a))
	if err != nil {
		t.Fatalf("the reordered replay refused: %v", err)
	}
	if len(again.Items) != 2 {
		t.Fatalf("the reordered replay answered %d outcomes, want two", len(again.Items))
	}
	for _, o := range again.Items {
		if o.LegacyID == "" || o.Status == "" {
			t.Fatalf("an empty outcome came back: %+v", o)
		}
	}
	byName := map[string]load.Outcome{}
	for _, o := range first.Items {
		byName[o.LegacyID] = o
	}
	for _, o := range again.Items {
		if byName[o.LegacyID] != o {
			t.Fatalf("outcome %+v is not the first one %+v", o, byName[o.LegacyID])
		}
	}
	if n := e.count(t, `SELECT count(*) FROM invoices WHERE org_id = $1::uuid AND source_kind = 'IMPORT'`, ld11OrgID); n != rows {
		t.Fatalf("the replays wrote rows: %d, want %d", n, rows)
	}
	if e.audit.len() != audits {
		t.Fatalf("the replays wrote audit rows")
	}
}

var _ = time.Now

// P2: an issue date after the load day refuses the batch naming the
// field issue_date and never a value, so the readers that take the
// latest dated ledger row cannot understate a balance after HH's first
// sale. The item dated the load day itself lands.
func TestReceivablesFutureIssueDateRefusesAndTodayLands(t *testing.T) {
	e := setupLD11(t)
	ctx := context.Background()
	future := time.Now().UTC().AddDate(0, 0, 40).Format("2006-01-02")
	today := time.Now().UTC().Format("2006-01-02")
	_, err := e.door.UpsertReceivables(ctx, ld11Batch("fd-1",
		ld11Item("FD-1", "CUST-ONE", "BR-A", "FD-DOC-1", future, future, 3000, "", "")))
	ref := ld11BatchRefused(t, err)
	if ref.Refusing.Field != "issue_date" || ref.Refusing.LegacyID != "FD-1" {
		t.Fatalf("the future dated item answered %+v, want a refusal naming issue_date", ref.Refusing)
	}
	for _, v := range []string{future} {
		if containsStr(ref.Refusing.Detail, v) {
			t.Fatalf("the refusal detail carries the value %q: %s", v, ref.Refusing.Detail)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM invoices WHERE org_id = $1::uuid AND source_kind = 'IMPORT'`, ld11OrgID); n != 0 {
		t.Fatalf("the refused batch landed %d documents", n)
	}

	// The reviewer's probe shape: an item dated the load day, then an HH
	// sale (a FINALIZED invoice and its ledger row at the statement
	// clock, the invoice writer's shape), then the balance readers.
	if _, err := e.door.UpsertReceivables(ctx, ld11Batch("fd-2",
		ld11Item("FD-2", "CUST-ONE", "BR-A", "FD-DOC-2", today, today, 3000, "", ""))); err != nil {
		t.Fatalf("the load day item refused: %v", err)
	}
	var saleID string
	if err := e.pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date, subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, org_id)
		VALUES ('#INV-LD11-SALE', $1::uuid, $2::uuid, 'FINALIZED', (now() AT TIME ZONE 'America/Toronto')::date, (now() AT TIME ZONE 'America/Toronto')::date, 700, 0, 700, 700, now(), $3::uuid) RETURNING id::text`,
		ld11CustOneID, ld11BranchAID, ld11OrgID).Scan(&saleID); err != nil {
		t.Fatalf("the HH sale: %v", err)
	}
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
		VALUES ($1::uuid, 'INVOICE', 700, $2, 'invoice', $3::uuid, $4::uuid, 'sale', clock_timestamp())`,
		ld11CustOneID, e.openTotal(t, ld11CustOneID), saleID, ld11BranchAID); err != nil {
		t.Fatalf("the HH sale ledger row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(ctx, `DELETE FROM customer_transactions WHERE source_ref_id = $1::uuid`, saleID)
		_, _ = e.pool.Exec(ctx, `DELETE FROM invoices WHERE id = $1::uuid`, saleID)
	})
	if got, want := e.shownBalance(t, ld11CustOneID), e.openTotal(t, ld11CustOneID); got != want || want != 3700 {
		t.Fatalf("after the load and one HH sale the readers show %d, open total %d, want 3700", got, want)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
