//go:build integration

// repository_integration_test.go, W6 DIB-211 verify tail, VERIFY:procurement.
//
// WHY THIS ROW WAS STILL OPEN
// ---------------------------
// The W6 verify sweep looked for packages carrying a real database repository and NO integration
// coverage at all. Five packages have zero integration files; three of them are correctly unit-only
// and were already justified in the W2 tail (config is env loading, localization is a pure string
// normaliser, feedback talks to an external Plane over httptest). That left two with a live pool and
// nothing exercising it: internal/notify and this one.
//
// procurement is the more serious of the two. Repository has NINE methods and every one of them is
// TENANT-SCOPED: the customer id is not a filter for convenience, it is the fence between one
// contractor and another contractor's material lists and prices. service_test.go covers the service
// over a fake store, which is the right shape for the service's own logic and is structurally unable
// to say anything about the fence, because the fence is not in Go, it is in the WHERE clause. A fake
// store that returns what it was told to return will agree with any SQL, including SQL that forgot
// `AND customer_id = $1`. Coverage was 32.4%.
//
// So this file asserts the things only the database can answer:
//
//	the fence           every read AND every write refuses a project belonging to another customer,
//	                    across all nine methods, with the SAME error a missing row gives
//	IDOR safety         repository.go claims a malformed-UUID path id must surface as 404 rather than
//	                    a 500 leaking the cast error (SQLSTATE 22P02, "review #9"). That claim is
//	                    about Postgres's behaviour and had never been run against Postgres.
//	the derived columns line_count and subtotal_cents are correlated aggregate subqueries in
//	                    projectSummaryCols. Nothing in Go computes them; a fake cannot get them wrong
//	                    and cannot get them right.
//	cascade             ON DELETE CASCADE on procurement_project_lines is schema behaviour that the
//	                    repository relies on to avoid orphaned lines and never verifies.
//
// FIXTURES. Every row created here is removed in t.Cleanup, including on failure. Nothing outside
// procurement_projects/_lines is written: the customers and products used are read-only lookups of
// existing seed rows, so no lease is required (no stock_moves/stock_balances, no payment_config).
//
// Run: make reset && go test -tags integration ./internal/procurement/...
package procurement

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, integration tests skipped")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return pool
}

// twoCustomers returns two DIFFERENT seeded customer ids. The fence cannot be tested with one.
func twoCustomers(t *testing.T, pool *pgxpool.Pool) (string, string) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT id::text FROM customers ORDER BY id LIMIT 2`)
	if err != nil {
		t.Fatalf("load customers: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan customer: %v", err)
		}
		ids = append(ids, id)
	}
	if len(ids) < 2 {
		t.Skipf("need two seeded customers to test the tenant fence, found %d", len(ids))
	}
	return ids[0], ids[1]
}

func someProduct(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM products ORDER BY id LIMIT 1`).Scan(&id); err != nil {
		t.Skipf("need a seeded product: %v", err)
	}
	return id
}

// newProject creates a project for owner and guarantees its removal (lines cascade).
func newProject(t *testing.T, pool *pgxpool.Pool, owner, name string) Project {
	t.Helper()
	repo := &Repository{pool: pool}
	p, err := repo.CreateProject(context.Background(), owner, "", name)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM procurement_projects WHERE id = $1`, p.ID)
	})
	return p
}

// -- the fence --------------------------------------------------------------------------------------

func TestRepository_TenantFence_EveryMethodRefusesAnotherCustomersProject(t *testing.T) {
	pool := testPool(t)
	owner, intruder := twoCustomers(t, pool)
	product := someProduct(t, pool)
	repo := &Repository{pool: pool}
	ctx := context.Background()

	project := newProject(t, pool, owner, "W6 fence, owner's project")

	// A real line, so the line-scoped methods have something they would damage if the fence leaked.
	line, err := repo.AddLine(ctx, owner, project.ID, AddLineRequest{ProductID: product, QtyEntered: "3", UOM: "EA"})
	if err != nil {
		t.Fatalf("AddLine as owner: %v", err)
	}

	qty := "999"
	// Every method that takes a customer id, called with the WRONG one. The expectation is uniform:
	// ErrNotFound. Not ErrForbidden, a 403 would confirm the row exists, which is the IDOR leak the
	// 404 exists to prevent.
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"GetProject", func() error { _, err := repo.GetProject(ctx, intruder, project.ID); return err }},
		{"UpdateProject", func() error {
			n := "renamed by intruder"
			_, err := repo.UpdateProject(ctx, intruder, project.ID, &n, nil)
			return err
		}},
		{"DeleteProject", func() error { return repo.DeleteProject(ctx, intruder, project.ID) }},
		{"AddLine", func() error {
			_, err := repo.AddLine(ctx, intruder, project.ID, AddLineRequest{ProductID: product, QtyEntered: "1"})
			return err
		}},
		{"UpdateLine", func() error {
			_, err := repo.UpdateLine(ctx, intruder, project.ID, line.ID, UpdateLineRequest{QtyEntered: &qty})
			return err
		}},
		{"RemoveLine", func() error { return repo.RemoveLine(ctx, intruder, project.ID, line.ID) }},
		{"SubmitRFQ", func() error {
			_, err := repo.SubmitRFQ(ctx, intruder, project.ID, []PricedLine{{
				LineID: line.ID, IndicativeUnitPriceCents: 1, IndicativeLineTotalCents: 1, Priced: true,
			}})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("%s with another customer's id returned %v, want ErrNotFound, the tenant fence is open", tc.name, err)
			}
		})
	}

	// And the fence held in BOTH directions: nothing above mutated the owner's project.
	after, err := repo.GetProject(ctx, owner, project.ID)
	if err != nil {
		t.Fatalf("GetProject as owner after the intrusion attempts: %v", err)
	}
	if after.Name != "W6 fence, owner's project" {
		t.Fatalf("the intruder's UpdateProject changed the name to %q despite returning ErrNotFound", after.Name)
	}
	if len(after.Lines) != 1 {
		t.Fatalf("owner's project has %d lines after the intrusion attempts, want 1", len(after.Lines))
	}
	if after.Lines[0].QtyEntered == qty {
		t.Fatal("the intruder's UpdateLine wrote through despite returning ErrNotFound")
	}
	if after.Status != "PLANNING" {
		t.Fatalf("the intruder's SubmitRFQ moved the lane to %q despite returning ErrNotFound", after.Status)
	}
}

func TestRepository_ListProjects_ShowsOnlyTheCallersProjects(t *testing.T) {
	pool := testPool(t)
	owner, other := twoCustomers(t, pool)
	repo := &Repository{pool: pool}

	mine := newProject(t, pool, owner, "W6 list, mine")
	theirs := newProject(t, pool, other, "W6 list, theirs")

	got, err := repo.ListProjects(context.Background(), owner)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	var sawMine bool
	for _, p := range got {
		if p.ID == theirs.ID {
			t.Fatal("ListProjects returned another customer's project, the list query is missing its tenant predicate")
		}
		if p.ID == mine.ID {
			sawMine = true
		}
	}
	if !sawMine {
		t.Fatal("ListProjects did not return the caller's own project")
	}
}

// -- IDOR safety: a malformed uuid must 404, not 500 ------------------------------------------------

func TestRepository_MalformedUUID_IsNotFoundNotAServerError(t *testing.T) {
	// repository.go maps SQLSTATE 22P02 to ErrNotFound so an id probe cannot tell "does not exist"
	// from "was not even a uuid", and, more practically, so a fuzzed path segment does not surface
	// as a 500 with the Postgres cast error in it. That is a claim about Postgres, so it is worth
	// exactly nothing until it has been run against Postgres.
	pool := testPool(t)
	owner, _ := twoCustomers(t, pool)
	repo := &Repository{pool: pool}
	ctx := context.Background()

	for _, bad := range []string{"not-a-uuid", "../../etc/passwd", "1 OR 1=1", ""} {
		t.Run("id="+bad, func(t *testing.T) {
			if _, err := repo.GetProject(ctx, owner, bad); !errors.Is(err, ErrNotFound) {
				t.Fatalf("GetProject(%q) returned %v, want ErrNotFound", bad, err)
			}
			if err := repo.DeleteProject(ctx, owner, bad); !errors.Is(err, ErrNotFound) {
				t.Fatalf("DeleteProject(%q) returned %v, want ErrNotFound", bad, err)
			}
		})
	}

	// A malformed CUSTOMER id must be just as safe, it is the other half of every WHERE clause.
	if _, err := repo.ListProjects(ctx, "not-a-uuid"); err == nil {
		t.Fatal("ListProjects with a malformed customer id returned no error; a cast failure must not pass silently")
	}
}

// -- the derived columns nothing in Go computes -----------------------------------------------------

func TestRepository_LineCountAndSubtotal_AreComputedBySQL(t *testing.T) {
	pool := testPool(t)
	owner, _ := twoCustomers(t, pool)
	product := someProduct(t, pool)
	repo := &Repository{pool: pool}
	ctx := context.Background()

	project := newProject(t, pool, owner, "W6 aggregates")

	// A fresh project: the COALESCE arms of both correlated subqueries. A SUM over no rows is NULL,
	// and a NULL scanned into int64 panics, this is the case the COALESCE exists for and the one a
	// fake store can never reach.
	got, err := repo.GetProject(ctx, owner, project.ID)
	if err != nil {
		t.Fatalf("GetProject (empty): %v", err)
	}
	if got.LineCount != 0 || got.SubtotalCents != 0 {
		t.Fatalf("empty project reports line_count=%d subtotal=%d, want 0/0", got.LineCount, got.SubtotalCents)
	}

	l1, err := repo.AddLine(ctx, owner, project.ID, AddLineRequest{ProductID: product, QtyEntered: "2", UOM: "EA"})
	if err != nil {
		t.Fatalf("AddLine 1: %v", err)
	}
	l2, err := repo.AddLine(ctx, owner, project.ID, AddLineRequest{ProductID: product, QtyEntered: "5", UOM: "EA"})
	if err != nil {
		t.Fatalf("AddLine 2: %v", err)
	}

	// Unpriced lines: the count moves, the subtotal does not (indicative_line_total_cents is NULL
	// until SubmitRFQ). A subtotal that moved here would mean unpriced lines were being counted as 0
	// in a SUM that should have skipped them, harmless, or that the column was being written early.
	got, err = repo.GetProject(ctx, owner, project.ID)
	if err != nil {
		t.Fatalf("GetProject (2 unpriced lines): %v", err)
	}
	if got.LineCount != 2 {
		t.Fatalf("line_count=%d after two AddLine calls, want 2", got.LineCount)
	}
	if got.SubtotalCents != 0 {
		t.Fatalf("subtotal=%d with no priced lines, want 0", got.SubtotalCents)
	}

	// Removing a line moves the count back down: the aggregate is a LIVE subquery, not a stored
	// column somebody has to remember to update.
	//
	// This happens here, before SubmitRFQ, and not after, RemoveLine is PLANNING-only and refuses
	// with "invalid state transition" once the project has moved to QUOTING. That is correct (a
	// contractor must not silently delete a line out of a list that has already been sent out for
	// pricing) and the first draft of this test got the order wrong and was told so by the database.
	if err := repo.RemoveLine(ctx, owner, project.ID, l1.ID); err != nil {
		t.Fatalf("RemoveLine while PLANNING: %v", err)
	}
	got, err = repo.GetProject(ctx, owner, project.ID)
	if err != nil {
		t.Fatalf("GetProject (after remove): %v", err)
	}
	if got.LineCount != 1 {
		t.Fatalf("line_count=%d after removing one of two lines, want 1", got.LineCount)
	}

	l3, err := repo.AddLine(ctx, owner, project.ID, AddLineRequest{ProductID: product, QtyEntered: "1", UOM: "EA"})
	if err != nil {
		t.Fatalf("AddLine 3: %v", err)
	}

	// Price them, and the SQL aggregate must add up.
	detail, err := repo.SubmitRFQ(ctx, owner, project.ID, []PricedLine{
		{LineID: l2.ID, IndicativeUnitPriceCents: 1000, IndicativeLineTotalCents: 5000, Priced: true},
		{LineID: l3.ID, IndicativeUnitPriceCents: 1500, IndicativeLineTotalCents: 3000, Priced: true},
	})
	if err != nil {
		t.Fatalf("SubmitRFQ: %v", err)
	}
	if detail.SubtotalCents != 8000 {
		t.Fatalf("subtotal=%d after pricing 5000 + 3000, want 8000", detail.SubtotalCents)
	}
	if detail.LineCount != 2 {
		t.Fatalf("line_count=%d after pricing, want 2", detail.LineCount)
	}
	if detail.Status != "QUOTING" {
		t.Fatalf("SubmitRFQ left the project in %q, want QUOTING", detail.Status)
	}

	// And the same numbers come back on a fresh read, so they are the database's answer rather than
	// something SubmitRFQ happened to return from memory.
	reread, err := repo.GetProject(ctx, owner, project.ID)
	if err != nil {
		t.Fatalf("GetProject (after RFQ): %v", err)
	}
	if reread.SubtotalCents != 8000 || reread.LineCount != 2 {
		t.Fatalf("re-read reports line_count=%d subtotal=%d, want 2/8000", reread.LineCount, reread.SubtotalCents)
	}
}

// -- the cascade the repository leans on ------------------------------------------------------------

func TestRepository_DeleteProject_CascadesItsLines(t *testing.T) {
	// DeleteProject issues one DELETE against procurement_projects. The lines go because the schema
	// says ON DELETE CASCADE, not because any Go code removes them, so if that clause were ever
	// dropped in a migration, every deleted project would leave orphaned lines behind and nothing in
	// the Go suite would notice.
	pool := testPool(t)
	owner, _ := twoCustomers(t, pool)
	product := someProduct(t, pool)
	repo := &Repository{pool: pool}
	ctx := context.Background()

	project := newProject(t, pool, owner, "W6 cascade")
	if _, err := repo.AddLine(ctx, owner, project.ID, AddLineRequest{ProductID: product, QtyEntered: "1"}); err != nil {
		t.Fatalf("AddLine: %v", err)
	}

	var before int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM procurement_project_lines WHERE project_id = $1`, project.ID).Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}
	if before != 1 {
		t.Fatalf("expected 1 line before delete, got %d", before)
	}

	if err := repo.DeleteProject(ctx, owner, project.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	var after int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM procurement_project_lines WHERE project_id = $1`, project.ID).Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != 0 {
		t.Fatalf("%d line(s) survived the project delete, ON DELETE CASCADE is not doing what the repository assumes", after)
	}

	// And the project is gone for its owner too, with the same 404 an intruder gets.
	if _, err := repo.GetProject(ctx, owner, project.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProject after delete returned %v, want ErrNotFound", err)
	}
}
