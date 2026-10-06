//go:build integration

// unitlist_integration_test.go proves the staff read of the catalogue
// (HH-LOAD-1 LD-3, productListUnits, GET /api/v1/uoms) at the service
// walk: the whole vocabulary pages by the code, the stable ordering
// key, with no row repeated and none dropped, and a mid list cursor
// resumes without a repeat. The route's registration, gate and census
// legs ride the composition's parity suite in cmd/core.
//
// The fixture is the seeded catalogue of migration 0002 (plus whatever
// the demo seed carried); this test reads only what stands.
//
// Run (the database URL rides the command line, never a default):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5670/ld3units?sslmode=disable' \
//	  go test -tags integration ./internal/product/... -count=1 -run TestUnitList
package product

import (
	"context"
	"sort"
	"testing"
)

func TestUnitListPagesTheCatalogueStableByCode(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	var total int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM uoms`).Scan(&total); err != nil {
		t.Fatalf("catalogue count: %v", err)
	}
	if total == 0 {
		t.Fatalf("the catalogue stands empty: the seeded database is the read's basis")
	}
	svc := NewService(NewRepository(pool)).WithUomReader(NewRepository(pool))

	// Walk the whole vocabulary at a limit the walk must page on.
	seen := map[string]bool{}
	var order []string
	after := ""
	pages := 0
	for {
		rows, next, err := svc.UnitsPage(ctx, 5, after)
		if err != nil {
			t.Fatalf("units page: %v", err)
		}
		if len(rows) == 0 && next != "" {
			t.Fatalf("an empty page carries a next cursor")
		}
		for _, u := range rows {
			if u.Code == "" || u.Name == "" || u.Dimension == "" {
				t.Fatalf("a catalogue row answers incomplete: %+v", u)
			}
			if seen[u.Code] {
				t.Fatalf("the walk answered %s twice", u.Code)
			}
			seen[u.Code] = true
			order = append(order, u.Code)
		}
		pages++
		if next == "" {
			break
		}
		if pages > total+1 {
			t.Fatalf("the walk ran past the catalogue's size: the cursor does not advance")
		}
		after = next
	}
	if len(seen) != total {
		t.Fatalf("the walk answered %d of %d catalogue rows: rows dropped", len(seen), total)
	}
	if !sort.StringsAreSorted(order) {
		t.Fatalf("the walk is not in the code order the cursor mints: %v", order)
	}
	if pages < 2 {
		t.Fatalf("the walk answered in %d page at a limit of five over %d rows: no page happened", pages, total)
	}

	// A mid list cursor resumes without a repeat: the row the cursor
	// names stands at the head of the resume.
	mid := order[2]
	rows, _, err := svc.UnitsPage(ctx, 5, mid)
	if err != nil {
		t.Fatalf("mid resume: %v", err)
	}
	if len(rows) == 0 || rows[0].Code == mid {
		t.Fatalf("the resume answers %v, want the rows after %s", rows, mid)
	}
}
