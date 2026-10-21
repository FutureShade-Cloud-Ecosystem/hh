//go:build integration

package main

// sf2p2_populate_stamp_integration_test.go, SF-2p2: a box provisioned AFTER migrate. 0432 stamps invoices and adds
// invoices_org_id_not_null only on a box that already had an org row, so a box the identity populate provisions later
// kept every NULL org invoice and got no check. The populate now runs each owning module's org stamp seam after it
// stamps the locations (identity.StampOrgTables), the billing seam calling the one SQL rule (org_stamp_and_check).

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

func sf2p2Entries() []identity.OrgStampEntry {
	return []identity.OrgStampEntry{{
		Name:  "invoices",
		Stamp: func(ctx context.Context, db identity.DBTX) (int64, error) { return billing.StampInvoiceOrgs(ctx, db) },
		Unstamped: func(ctx context.Context, db identity.DBTX) (int64, error) {
			return billing.UnstampedInvoiceOrgs(ctx, db)
		},
	}}
}

// sf2p2Invoice plants one invoice (or credit memo) at a branch with no org, the way the migrations and the writers did
// before any org existed.
func sf2p2Invoice(t *testing.T, ctx context.Context, pool *pgxpool.Pool, no, docType, branch string) {
	t.Helper()
	sign := int64(1)
	if docType == "CREDIT_MEMO" {
		sign = -1
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO customers (account_no, name) VALUES ($1, 'SF2P2 customer') ON CONFLICT DO NOTHING`, "SF2P2-"+no); err != nil {
		t.Fatalf("plant a customer: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, due_date, subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		 SELECT $1, c.id, $2::uuid, 'FINALIZED', CURRENT_DATE + 30, $5, 0, $5, $5, now(), $3 FROM customers c WHERE c.account_no = $4`,
		no, branch, docType, "SF2P2-"+no, sign*1000); err != nil {
		t.Fatalf("plant invoice %s: %v", no, err)
	}
}

func sf2p2Counts(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (nullOrg int, hasCheck bool) {
	t.Helper()
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE org_id IS NULL`).Scan(&nullOrg); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'invoices_org_id_not_null' AND conrelid = 'invoices'::regclass AND convalidated)`).Scan(&hasCheck); err != nil {
		t.Fatal(err)
	}
	return nullOrg, hasCheck
}

func TestSF2P2_AnInvoiceOfABoxProvisionedAfterMigrateTakesTheOrgAndGainsTheCheck(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "sf2p2prov")
	base, _ := sf2p2Counts(t, ctx, pool) // the migrations plant a few invoices of their own, before any org existed
	sf2p2Invoice(t, ctx, pool, "SF2P2-INV-1", "INVOICE", prodTrenton)
	sf2p2Invoice(t, ctx, pool, "SF2P2-INV-2", "INVOICE", prodKingston)
	sf2p2Invoice(t, ctx, pool, "SF2P2-CM-1", "CREDIT_MEMO", prodTrenton) // written before any org existed
	if n, has := sf2p2Counts(t, ctx, pool); n != base+3 || has {
		t.Fatalf("before provisioning: %d NULL org invoices and check %v, want %d and no check", n, has, base+3)
	}
	cfg := identity.PopulateConfig{OrgSlug: "sf2p2-dealer", OrgName: "SF2P2 Dealer", OrgTables: sf2p2Entries()}
	report, err := identity.Populate(ctx, pool, cfg)
	if err != nil {
		t.Fatalf("populate: %v", err)
	}
	if report.OrgTables["invoices"] != int64(base+3) {
		t.Errorf("the populate stamped %v, want %d invoices", report.OrgTables, base+3)
	}
	if n, has := sf2p2Counts(t, ctx, pool); n != 0 || !has {
		t.Errorf("after provisioning: %d NULL org invoices and validated check %v, want 0 and the check", n, has)
	}
	// The staff feed fences on org_id: the credit memo written before provisioning is reachable under the org.
	var feed int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE org_id = $1::uuid AND doc_type = 'CREDIT_MEMO'`, report.OrgID).Scan(&feed); err != nil || feed != 1 {
		t.Errorf("the credit memo is reachable under the org %d times (%v), want once", feed, err)
	}
	// A NULL org insert is now refused.
	if _, err := pool.Exec(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, due_date, subtotal_cents, tax_cents, total_cents, balance_cents, org_id)
		 SELECT 'SF2P2-NULL', c.id, $1::uuid, 'DRAFT', CURRENT_DATE, 1, 0, 1, 1, NULL FROM customers c WHERE c.account_no = 'SF2P2-SF2P2-INV-1'`, prodTrenton); err == nil {
		t.Errorf("an invoice with no org was accepted after provisioning")
	}
	left, err := identity.UnstampedOrgTables(ctx, pool, cfg.OrgTables)
	if err != nil || left["invoices"] != 0 {
		t.Errorf("the verification reads %v (%v), want no unstamped invoice", left, err)
	}
	// A second run changes nothing and does not fail.
	again, err := identity.Populate(ctx, pool, cfg)
	if err != nil || again.OrgTables["invoices"] != 0 {
		t.Errorf("a second populate stamped %v (%v), want 0 and no failure", again.OrgTables, err)
	}
}

// An invoice at a location with no org is refused in 0432's words, never given an org by guessing, and nothing is half
// written: the invoices keep their NULL org and the check is not added.
func TestSF2P2_AnInvoiceAtALocationWithNoOrgRefusesWithNothingHalfWritten(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "sf2p2refuse")
	base, _ := sf2p2Counts(t, ctx, pool)
	sf2p2Invoice(t, ctx, pool, "SF2P2-R-1", "INVOICE", prodTrenton)
	if _, err := pool.Exec(ctx, `INSERT INTO org (slug, name) VALUES ('sf2p2-r', 'SF2P2 R')`); err != nil {
		t.Fatalf("plant an org: %v", err)
	}
	// Only Kingston is stamped; the invoice sits at Trenton, which has no org.
	if _, err := pool.Exec(ctx, `UPDATE locations SET org_id = (SELECT id FROM org LIMIT 1) WHERE id = $1::uuid`, prodKingston); err != nil {
		t.Fatalf("stamp one location: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = identity.StampOrgTables(ctx, tx, sf2p2Entries())
	_ = tx.Rollback(ctx)
	if err == nil || !strings.Contains(err.Error(), "sit at a location with no org") || !strings.Contains(err.Error(), "by guessing") {
		t.Fatalf("the stamp answered %v, want 0432's refusal words", err)
	}
	if n, has := sf2p2Counts(t, ctx, pool); n != base+1 || has {
		t.Errorf("after the refusal: %d NULL org invoices and check %v, want %d and no check", n, has, base+1)
	}
}
