//go:build integration

// fw1_11_composition_poster_integration_test.go, FW1-11 round 4: the PRODUCTION poster's document
// date carry.
//
// A bill's journal is dated by the bill itself (the document date rule), and the leg that proves it
// in the purchasing package uses that package's own harness adapter, which mirrors w4cGLPoster field
// for field. The composition root's adapter (core/cmd/core/wire_operations.go) is what a real
// deployment posts through, and its carry had no leg of its own: a change that dropped DocumentDate
// in w4cGLPoster would have left every purchasing leg green and filed the bill's journal into the
// posting month, so the tax tie-out would have shown a variance in the bill's month and in the
// posting month.
//
// This leg drives a REAL PostBill through the composition's OWN adapters (w4cGLPoster,
// w4cStockReceiver, w4cVendorDirectory, w4cLandedCoster, glRuleAdapter, item5BranchDirectory)
// over a real migrated and Dibbits-seeded database, and reads the journal's filed period and its
// posted day off the ledger.
//
// EVERY DATE IN THIS FILE IS FIXED and no real clock is read here, so the leg cannot age into a
// dated time bomb (internal/testaudit's tripwire).
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 \
//	     -run 'FW111' ./cmd/core/

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	staffctx "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/landedcost"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
)

// TestIntegration_FW111_PostBillThroughTheCompositionPosterFilesIntoTheBillsMonth drives a real
// bill through the composition root's own poster and reads where the ledger filed it.
func TestIntegration_FW111_PostBillThroughTheCompositionPosterFilesIntoTheBillsMonth(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "fw111poster")
	body, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("reading the embedded seed: %v", err)
	}
	if _, err := pool.Exec(ctx, string(body)); err != nil {
		t.Fatalf("applying the seed: %v", err)
	}
	// The seed runs before any org exists; the branches get one, exactly as the walk's fixture does.
	const (
		orgID   = "88a20000-0000-4000-8000-0000000000e1"
		orgSlug = "fw1-11-poster"
	)
	mustExec(t, ctx, pool, `INSERT INTO org (id, slug, name) VALUES ($1, $2, 'FW1 11 Poster Org') ON CONFLICT (id) DO NOTHING`, orgID, orgSlug)
	mustExec(t, ctx, pool, `UPDATE locations SET org_id = $1 WHERE loc_type = 'BRANCH'`, orgID)

	// The composition's own adapters, the ones wire_operations hands purchasing.
	glSvc := gl.NewService(gl.NewRepository(pool).WithZone(newDealerZone(pool)))
	inventorySvc := inventory.NewService(inventory.NewRepository(pool)).
		WithGLPoster(invGLPoster{svc: glSvc}).
		WithRuleResolver(glRuleAdapter{svc: glSvc})
	landedSvc := landedcost.NewService(landedcost.NewRepository(pool)).
		WithCapitalizer(landedAdapter{svc: inventorySvc}).
		WithGLPoster(landedGLPoster{svc: glSvc})
	purchasingSvc := purchasing.NewService(purchasing.NewRepository(pool)).
		WithZone(newDealerZone(pool)).
		WithStockReceiver(w4cStockReceiver{svc: inventorySvc}).
		WithVendors(w4cVendorDirectory{pool: pool}).
		WithLandedCoster(w4cLandedCoster{svc: landedSvc}).
		WithGLPoster(w4cGLPoster{svc: glSvc}).
		WithRuleResolver(glRuleAdapter{svc: glSvc}).
		WithBranches(item5BranchDirectory{pool: pool}).
		WithVendorAddresses(item5VendorAddressBook{pool: pool}).
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))

	// A caller the branch fence accepts: a global leader on a seeded user.
	var userID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("no seeded user: %v", err)
	}
	staffCtx := staffctx.ContextWithStaff(ctx, staffctx.StaffContext{
		UserID: userID, RoleCode: "GENERAL_MANAGER",
		Tier: staffctx.TierLeadership, Scope: staffctx.ScopeGlobal, Status: staffctx.StatusActive,
	})

	var vendorID, productID, branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM vendors WHERE is_active ORDER BY id LIMIT 1`).Scan(&vendorID); err != nil {
		t.Fatalf("no seeded vendor: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM products WHERE is_active ORDER BY id LIMIT 1`).Scan(&productID); err != nil {
		t.Fatalf("no seeded product: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' ORDER BY id LIMIT 1`).Scan(&branchID); err != nil {
		t.Fatalf("no seeded branch: %v", err)
	}

	// The bill's own day, in a month the books still hold open.
	const (
		billY   = 2026
		billM   = 4
		billDay = "2026-04-15"
		billTax = int64(650)
	)

	po, err := purchasingSvc.CreatePO(staffCtx, purchasing.CreatePORequest{
		VendorID: vendorID, BranchID: branchID, ExpectedAt: "2026-04-01",
		Lines: []purchasing.AddLineParams{{ProductID: productID, QtyOrderedBase: 100, UOM: "EA", UnitCostCents: 2500}},
	})
	if err != nil {
		t.Fatalf("create the purchase order: %v", err)
	}
	ordered, err := purchasingSvc.Transition(staffCtx, po.ID, purchasing.StatusOrdered)
	if err != nil {
		t.Fatalf("place the purchase order: %v", err)
	}
	if _, err := purchasingSvc.Receive(staffCtx, ordered.ID, purchasing.ReceiveRequest{
		Lines: []purchasing.ReceiveLineParams{{POLineID: ordered.Lines[0].ID, QtyBase: 100}},
	}); err != nil {
		t.Fatalf("receive against the purchase order: %v", err)
	}
	bill, err := purchasingSvc.CreateBill(staffCtx, purchasing.CreateBillRequest{
		BillNo: fmt.Sprintf("FW111-POSTER-%s", po.ID), POID: ordered.ID, BillDate: billDay,
		TaxCents: billTax,
		Lines:    []purchasing.BillLineParams{{POLineID: ordered.Lines[0].ID, QtyBilledBase: 100, UnitCostCents: 2500}},
	})
	if err != nil {
		t.Fatalf("create the vendor bill: %v", err)
	}
	posted, err := purchasingSvc.PostBill(staffCtx, bill.ID, purchasing.PostBillRequest{
		AcceptVariance: true, Note: "fw1-11 the composition poster",
	})
	if err != nil {
		t.Fatalf("post the vendor bill: %v", err)
	}

	var journalID string
	if err := pool.QueryRow(ctx,
		`SELECT gl_journal_id::text FROM purchasing_bills WHERE id = $1::uuid`, posted.ID).Scan(&journalID); err != nil {
		t.Fatalf("read the bill's journal: %v", err)
	}
	if journalID == "" {
		t.Fatal("PostBill left no journal on the bill")
	}

	// The ledger side: the journal filed into the BILL'S month, not into the posting month. A
	// dropped DocumentDate in the composition's poster would file it into the month the post
	// happened in, and this line would name the wrong month.
	var jy, jm int
	if err := pool.QueryRow(ctx, `
		SELECT fp.year, fp.month FROM gl_journals j JOIN fiscal_periods fp ON fp.id = j.period_id
		 WHERE j.id = $1::uuid`, journalID).Scan(&jy, &jm); err != nil {
		t.Fatalf("read the journal's fiscal period: %v", err)
	}
	if jy != billY || jm != billM {
		t.Errorf("FW1-11 the composition poster: the bill dated %s filed its journal into %04d-%02d, want the bill's own month %04d-%02d (w4cGLPoster must carry the document date)",
			billDay, jy, jm, billY, billM)
	}

	// The carry itself: the journal's posted day is the bill's own house day, which is the document
	// date the ledger files by. The posting clock would read the day the post happened.
	var postedDay string
	if err := pool.QueryRow(ctx,
		`SELECT to_char(posted_at AT TIME ZONE 'America/Toronto', 'YYYY-MM-DD') FROM gl_journals WHERE id = $1::uuid`,
		journalID).Scan(&postedDay); err != nil {
		t.Fatalf("read the journal's posted day: %v", err)
	}
	if postedDay != billDay {
		t.Errorf("FW1-11 the composition poster: the journal's posted day reads %s, want the bill's own day %s (the document date the production poster carries)",
			postedDay, billDay)
	}
}
