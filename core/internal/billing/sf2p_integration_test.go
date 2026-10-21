//go:build integration

package billing

// sf2p_integration_test.go (SF-2p, SF-3 folded in): every invoice carries its org, so a credit memo reaches the staff
// feed. seq is drawn by 0422's trg_sync_seq_draw on invoices; the memo adds no explicit draw. org_id was the gap: the
// staff feed fences `i.org_id = $1`, so a memo with no org_id was invisible on Counter Till while the customer's
// books carried it.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/harness"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func invoicesSeq(t *testing.T) int64 {
	t.Helper()
	var n int64
	_ = intPool(t).QueryRow(context.Background(), `SELECT COALESCE((SELECT next_value FROM scope_sequences WHERE scope = 'invoices'), 0)`).Scan(&n)
	return n
}

// A warranty accommodation credit (SourceWarrantyCredit, no ReversesInvoiceID: the shape cmd/core's
// w6cWarrantyCreditIssuer issues) reaches BOTH feeds, the staff arm by org and the tenant arm by customer. Its seq
// comes from the trigger alone: the invoices counter advances by exactly one for the insert (an explicit draw beside
// the trigger would advance it by two), and the stamped org equals the org the staff filter carries.
func TestIntegration_SF2p_AWarrantyCreditReachesBothFeedsWithTheTriggersSeq(t *testing.T) {
	writer, c := freshMemoWorld(t)
	pool := intPool(t)
	if err := c.Engine.RegisterScope("invoices", NewInvoicesSyncStore(pool, writer, nil)); err != nil {
		t.Fatalf("register: %v", err)
	}
	info, ok := sync.Lookup("invoices")
	if !ok {
		t.Fatalf("scope invoices missing")
	}
	c.Scope = info
	var memoID string
	c.Write = func(ctx context.Context) (string, error) {
		before := invoicesSeq(t)
		doc, err := writer.IssueCreditMemo(ctx, CreditMemoInput{
			CustomerID: testkit.TenantAlphaID, BranchID: testkit.BranchNorthAID, AmountCents: 4100, Memo: "warranty accommodation",
			IdempotencyKey: "fresh-memo-" + uuid.NewString(), SourceKind: SourceWarrantyCredit,
		})
		if err != nil {
			return "", err
		}
		memoID = doc.InvoiceID
		if after := invoicesSeq(t); after != before+1 {
			return "", fmt.Errorf("the invoices counter moved %d to %d for one memo, want exactly one draw (the trigger's)", before, after)
		}
		var org *string
		var seq *int64
		if err := pool.QueryRow(ctx, `SELECT org_id::text, seq FROM invoices WHERE id = $1::uuid`, doc.InvoiceID).Scan(&org, &seq); err != nil {
			return "", err
		}
		if org == nil || *org != testkit.OrgNorthID {
			return "", fmt.Errorf("the memo carries org %v, want the staff filter's %s", org, testkit.OrgNorthID)
		}
		if seq == nil || *seq != before+1 {
			return "", fmt.Errorf("the memo carries seq %v, want the one the trigger drew (%d)", seq, before+1)
		}
		return doc.InvoiceID, nil
	}
	c.Want = wantNegativeTotal
	harness.FreshFor(t, c,
		harness.FreshArm{Name: "staff arm, by org", Filter: sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: "fresh"}},
		harness.FreshArm{Name: "tenant arm, by customer", Filter: sync.ScopeFilter{OrgID: testkit.OrgNorthID, TenantID: testkit.TenantAlphaID, Tenant: true, Actor: "fresh"}},
	)
	_ = memoID
}

const migration0432 = "../../db/migrations/0432_invoices_org_backfill_and_check.sql"

// The backfill makes a NULL org invoice visible on the staff arm: it takes its branch's org and the trigger re-serves
// the row with a new seq. Run inside a transaction that is rolled back, so the shared database keeps no new
// constraint. Every location takes an org first so the migration has no unresolved row to refuse.
func TestIntegration_SF2p_TheBackfillStampsANullOrgInvoiceAndReServesIt(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	if err := testkit.Build(ctx, testkit.PgxPool(pool)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	t.Cleanup(func() { _ = testkit.Cleanup(context.Background(), testkit.PgxPool(pool)) })
	sqlText, err := os.ReadFile(migration0432)
	if err != nil {
		t.Fatalf("read the migration: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE locations SET org_id = $1::uuid WHERE org_id IS NULL`, testkit.OrgNorthID); err != nil {
		t.Fatalf("stamp the locations: %v", err)
	}
	var id string
	if err := tx.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date, subtotal_cents, tax_cents, total_cents, balance_cents)
		 VALUES ('#INV-SF2P-NULL', $1::uuid, $2::uuid, 'FINALIZED', current_date, current_date, 1000, 0, 1000, 1000) RETURNING id::text`,
		testkit.TenantAlphaID, testkit.BranchNorthAID).Scan(&id); err != nil {
		t.Fatalf("plant the NULL org invoice: %v", err)
	}
	var orgBefore *string
	var seqBefore *int64
	_ = tx.QueryRow(ctx, `SELECT org_id::text, seq FROM invoices WHERE id = $1::uuid`, id).Scan(&orgBefore, &seqBefore)
	if orgBefore != nil {
		t.Fatalf("the planted invoice already carries org %s", *orgBefore)
	}
	if _, err := tx.Exec(ctx, string(sqlText)); err != nil {
		t.Fatalf("run 0432: %v", err)
	}
	var org *string
	var seq *int64
	var visible bool
	if err := tx.QueryRow(ctx, `SELECT org_id::text, seq, (org_id = $2::uuid AND seq IS NOT NULL) FROM invoices WHERE id = $1::uuid`, id, testkit.OrgNorthID).Scan(&org, &seq, &visible); err != nil {
		t.Fatalf("read back: %v", err)
	}
	t.Logf("after 0432: org %v seq %v (before: seq %v)", org, seq, seqBefore)
	if !visible || (seqBefore != nil && *seq <= *seqBefore) {
		t.Errorf("the backfilled invoice is not served on the staff arm: org %v, seq %v (before %v)", org, seq, seqBefore)
	}
	var nulls int
	_ = tx.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE org_id IS NULL`).Scan(&nulls)
	if nulls != 0 {
		t.Errorf("%d invoice(s) still carry no org after 0432", nulls)
	}
	// The CHECK now refuses a NULL org on a box that has an org.
	if _, err := tx.Exec(ctx, `SAVEPOINT s`); err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date, subtotal_cents, tax_cents, total_cents, balance_cents)
		 VALUES ('#INV-SF2P-NULL2', $1::uuid, $2::uuid, 'FINALIZED', current_date, current_date, 1000, 0, 1000, 1000)`,
		testkit.TenantAlphaID, testkit.BranchNorthAID); err == nil {
		t.Errorf("an invoice with no org was accepted after 0432")
	}
	_, _ = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT s`)
}

// The RAISE: a box that has an org, with an invoice whose location has none, fails 0432 with the stated message
// instead of guessing.
func TestIntegration_SF2p_TheMigrationRaisesForAnInvoiceAtALocationWithNoOrg(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	if err := testkit.Build(ctx, testkit.PgxPool(pool)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	t.Cleanup(func() { _ = testkit.Cleanup(context.Background(), testkit.PgxPool(pool)) })
	sqlText, err := os.ReadFile(migration0432)
	if err != nil {
		t.Fatalf("read the migration: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE locations SET org_id = $1::uuid WHERE org_id IS NULL AND id <> $2::uuid`, testkit.OrgNorthID, testkit.BranchNorthBID); err != nil {
		t.Fatalf("stamp the other locations: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE locations SET org_id = NULL WHERE id = $1::uuid`, testkit.BranchNorthBID); err != nil {
		t.Fatalf("unstamp one location: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date, subtotal_cents, tax_cents, total_cents, balance_cents)
		 VALUES ('#INV-SF2P-RAISE', $1::uuid, $2::uuid, 'FINALIZED', current_date, current_date, 1000, 0, 1000, 1000)`,
		testkit.TenantAlphaID, testkit.BranchNorthBID); err != nil {
		t.Fatalf("plant the invoice: %v", err)
	}
	_, err = tx.Exec(ctx, string(sqlText))
	t.Logf("0432 with an unresolvable invoice: %v", err)
	if err == nil {
		t.Fatalf("0432 ran with an invoice at a location that has no org")
	}
	if !strings.Contains(err.Error(), "0432") || !strings.Contains(err.Error(), "no org") {
		t.Errorf("the failure %q does not carry the stated message", err.Error())
	}
}
