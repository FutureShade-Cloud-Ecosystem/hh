//go:build integration

// receivables_ledger_integration_test.go: the ledger and guard legs of
// the open receivables door (HH-LOAD-1 LD-11): running balances stay
// true in date order when a later batch carries older items or a
// restated item moves, an item worked in HH since it loaded is never
// overwritten by a reload, and a branch of another org never lands a
// document. Every fixture is invented.
package load_test

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
)

func TestReceivablesOlderBatchAfterNewerKeepsRunningBalancesInDateOrder(t *testing.T) {
	e := setupLD11(t)
	ctx := context.Background()
	if _, err := e.door.UpsertReceivables(ctx, ld11Batch("b-ord-1",
		ld11Item("OR-1", "CUST-ONE", "BR-A", "OR-DOC-1", "2032-06-10", "2032-07-10", 10000, "", ""),
		ld11Item("OR-2", "CUST-ONE", "BR-A", "OR-DOC-2", "2032-06-20", "2032-07-20", 20000, "", ""))); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	if _, err := e.door.UpsertReceivables(ctx, ld11Batch("b-ord-2",
		ld11Item("OR-3", "CUST-ONE", "BR-A", "OR-DOC-3", "2032-06-05", "2032-07-05", 3000, "", ""),
		ld11Item("OR-4", "CUST-ONE", "BR-A", "OR-DOC-4", "2032-06-15", "2032-07-15", 4000, "", ""))); err != nil {
		t.Fatalf("second batch: %v", err)
	}
	ledger := e.ledger(t, ld11CustOneID)
	if len(ledger) != 4 {
		t.Fatalf("%d ledger rows, want four", len(ledger))
	}
	for i, want := range []string{"OR-DOC-3", "OR-DOC-1", "OR-DOC-4", "OR-DOC-2"} {
		if ledger[i].SourceDoc != want {
			t.Fatalf("ledger position %d is %s, want %s in date order", i, ledger[i].SourceDoc, want)
		}
	}
	ld11AssertRunningBalances(t, ledger)
	if last := ledger[3].BalanceAfter; last != 37000 {
		t.Fatalf("the last running balance is %d, want the customer's total 37000", last)
	}
}

func TestReceivablesUpdateMovesDateAndKeepsRunningBalances(t *testing.T) {
	e := setupLD11(t)
	ctx := context.Background()
	items := []load.Item{
		ld11Item("UD-1", "CUST-ONE", "BR-A", "UD-DOC-1", "2032-06-01", "2032-07-01", 10000, "", ""),
		ld11Item("UD-2", "CUST-ONE", "BR-A", "UD-DOC-2", "2032-06-10", "2032-07-10", 20000, "", ""),
		ld11Item("UD-3", "CUST-ONE", "BR-A", "UD-DOC-3", "2032-06-20", "2032-07-20", 30000, "", ""),
	}
	if _, err := e.door.UpsertReceivables(ctx, ld11Batch("b-ud-1", items...)); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	// The middle item is restated: a new amount and a date past the last.
	moved := ld11Item("UD-2", "CUST-ONE", "BR-A", "UD-DOC-2", "2032-06-25", "2032-07-25", 15000, "", "")
	r, err := e.door.UpsertReceivables(ctx, ld11Batch("b-ud-2", items[0], moved, items[2]))
	if err != nil {
		t.Fatalf("second batch: %v", err)
	}
	if r.Items[1].Status != load.OutcomeUpdated {
		t.Fatalf("the restated item answered %+v, want updated", r.Items[1])
	}
	ledger := e.ledger(t, ld11CustOneID)
	for i, want := range []string{"UD-DOC-1", "UD-DOC-3", "UD-DOC-2"} {
		if ledger[i].SourceDoc != want {
			t.Fatalf("ledger position %d is %s, want %s after the move", i, ledger[i].SourceDoc, want)
		}
	}
	ld11AssertRunningBalances(t, ledger)
	if last := ledger[2].BalanceAfter; last != 55000 {
		t.Fatalf("the last running balance is %d, want 55000", last)
	}
	if ledger[2].Memo == "" || ledger[2].Memo[0] != '#' {
		t.Fatalf("the moved row's memo %q lost HH's own document number", ledger[2].Memo)
	}
}

func TestReceivablesReloadNeverOverwritesADocumentWorkedInHH(t *testing.T) {
	e := setupLD11(t)
	ctx := context.Background()
	item := ld11Item("WK-1", "CUST-ONE", "BR-A", "WK-DOC-1", "2032-06-01", "2032-07-01", 10000, "", "")
	if _, err := e.door.UpsertReceivables(ctx, ld11Batch("b-wk-1", item)); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE invoices SET status = 'PARTIALLY_PAID', balance_cents = 6000
		WHERE org_id = $1::uuid AND source_doc_no = 'WK-DOC-1'`, ld11OrgID); err != nil {
		t.Fatalf("simulate a payment: %v", err)
	}
	restated := ld11Item("WK-1", "CUST-ONE", "BR-A", "WK-DOC-1", "2032-06-01", "2032-07-01", 9000, "", "")
	_, err := e.door.UpsertReceivables(ctx, ld11Batch("b-wk-2", restated))
	ref := ld11BatchRefused(t, err)
	if ref.Refusing.Reason != load.ReasonUpdateTargetMissing || ref.Refusing.LegacyID != "WK-1" {
		t.Fatalf("the reload answered %+v, want %s naming WK-1", ref.Refusing, load.ReasonUpdateTargetMissing)
	}
	inv := e.invoices(t)["WK-DOC-1"]
	if inv.Balance != 6000 || inv.Total != 10000 || inv.Status != "PARTIALLY_PAID" {
		t.Fatalf("the worked document moved to %+v, want it untouched", inv)
	}
	if n := e.count(t, `SELECT count(*) FROM load_batch WHERE org_id = $1::uuid AND batch_id = 'b-wk-2'`, ld11OrgID); n != 0 {
		t.Fatalf("the refused reload recorded its batch id")
	}
}

func TestReceivablesBranchOfAnotherOrgRefusesTheBatch(t *testing.T) {
	e := setupLD11(t)
	ctx := context.Background()
	const otherOrg = "6a110000-0000-4000-8000-0000000000a2"
	const otherBranch = "6a110000-0000-4000-8000-0000000000b9"
	for _, stmt := range []string{
		`INSERT INTO org (id, slug, name) VALUES ('` + otherOrg + `', 'ld11-other', 'LD11 Other Yard') ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name, address, org_id) VALUES
			('` + otherBranch + `', '` + otherBranch + `', NULL, 'BRANCH', 'LD11-X', 'LD11 Other Branch', '{}', '` + otherOrg + `') ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO load_key_registry (org_id, source_system, kind, legacy_id, hh_id, content_hash, last_batch_id) VALUES
			('` + ld11OrgID + `', '` + ld11Source + `', 'branch', 'BR-X', '` + otherBranch + `', 'aa110000000000000000000000000000000000000000000000000000000000b9', 'fixture')`,
	} {
		if _, err := e.pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(ctx, `DELETE FROM load_key_registry WHERE org_id = '`+ld11OrgID+`'::uuid AND legacy_id = 'BR-X'`)
		_, _ = e.pool.Exec(ctx, `DELETE FROM locations WHERE id = '`+otherBranch+`'`)
		_, _ = e.pool.Exec(ctx, `DELETE FROM org WHERE id = '`+otherOrg+`'::uuid`)
	})
	_, err := e.door.UpsertReceivables(ctx, ld11Batch("b-xorg-1",
		ld11Item("XO-1", "CUST-ONE", "BR-X", "XO-DOC-1", "2032-06-01", "2032-07-01", 1000, "", "")))
	ref := ld11BatchRefused(t, err)
	if ref.Refusing.Reason != load.ReasonMissingReference || ref.Refusing.Field != load.RefKindBranch {
		t.Fatalf("the cross org branch answered %+v, want a missing reference on the branch", ref.Refusing)
	}
	if n := e.count(t, `SELECT count(*) FROM invoices WHERE org_id = $1::uuid AND source_kind = 'IMPORT'`, ld11OrgID); n != 0 {
		t.Fatalf("%d documents landed, want none", n)
	}
}
