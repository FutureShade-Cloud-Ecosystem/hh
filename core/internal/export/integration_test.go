//go:build integration

// integration_test.go, the catalogue export against real Postgres and the REAL read services.
//
// Run with:
//
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5432/<db>?sslmode=disable' \
//	  go test -tags=integration ./internal/export/... -count=1 -v
//
// The unit tests prove the writer and the rules with fakes. This one proves the SEAM: that
// *product.Service and *inventory.Service, the exact services the catalogue screen reads, wired the
// way cmd/server wires them, satisfy the interfaces this package declares, and that the file coming
// out the other end holds the same numbers the services hold. The adapters are bound here directly
// because the mount itself lives in cmd/server/main.go, which this lane does not touch (see
// NOTES-FIX-441X.md, PARK 1).
package export

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...) // a configured DB that fails must fail, not skip
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// inventoryService / productService build the two reads the way cmd/server builds them: the product
// service carrying its DIB-404 branch-stock seam and its DIB-437 list-price seam, over the inventory
// and pricing services. Shared with the walkthrough harness in evidence_test.go so the screenshots
// and the assertions are looking at the same wiring.
func inventoryService(pool *pgxpool.Pool) *inventory.Service {
	return inventory.NewService(inventory.NewRepository(pool))
}

func productService(pool *pgxpool.Pool, inv *inventory.Service) *product.Service {
	return product.NewService(product.NewRepository(pool)).
		WithBranchStock(inv).
		WithListPrices(pricing.NewService(pricing.NewRepository(pool)))
}

// liveHandler is the export handler over those real services.
func liveHandler(t *testing.T, pool *pgxpool.Pool) *Handler {
	t.Helper()
	inv := inventoryService(pool)
	return NewHandler(NewCatalogueService(productService(pool, inv), inv, quietLog()), quietLog())
}

// get runs a request through a mux with the route registered behind a pass-through guard, the
// staff middleware is cmd/server's to supply, and its behaviour is asserted in the unit tests.
func get(t *testing.T, h *Handler, url string) *httptest.ResponseRecorder {
	t.Helper()
	// Guards-swap carry note (HH-3.2 fold): the pass-through stub became the
	// kit's real Guards with a resolved staff authority.
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(coreid.StaffAuthority{RoleID: "inside_sales", Tier: 3, Scope: "global"})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
	req.Header.Set("X-Org", testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestIntegration_CatalogueExportIsAReadableWorkbookOfTheSeededCatalogue(t *testing.T) {
	pool := intPool(t)
	rec := get(t, liveHandler(t, pool), "/api/v1/exports/catalogue.xlsx")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != ContentType {
		t.Errorf("Content-Type = %q", ct)
	}
	got := parts(t, rec.Body.Bytes())
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels", "xl/styles.xml", "xl/worksheets/sheet1.xml"} {
		if _, ok := got[name]; !ok {
			t.Fatalf("the export is missing %s", name)
		}
	}
	sheet := parseSheet(t, got["xl/worksheets/sheet1.xml"])

	// Title, subtitle, header, then one row per active product in the seed.
	var seeded int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM products WHERE is_active`).Scan(&seeded); err != nil {
		t.Fatal(err)
	}
	if seeded == 0 {
		t.Skip("no active products seeded")
	}
	if want := seeded + 3; len(sheet.Rows) != want {
		t.Errorf("rows = %d, want %d (title + subtitle + header + %d products)", len(sheet.Rows), want, seeded)
	}
	if c, _ := cellAt(sheet, "A3"); c.Inline.Text != "SKU" {
		t.Errorf("header row = %+v", c)
	}
}

// The point of the whole exercise: the numbers in the file are the numbers the SERVICES hold, cell
// for cell, for every row, not a second query that will drift from the screen.
func TestIntegration_EveryCellMatchesWhatTheCatalogueServicesReport(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()

	inv := inventoryService(pool)
	products := productService(pool, inv)
	stampSeededBranches(t, pool)

	rows, err := products.List(ctx, product.Filter{ActiveOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Skip("no active products seeded")
	}
	browse, err := inv.Browse(ctx, inventory.BrowseFilter{Limit: stockReadLimit, OrgID: testkit.OrgNorthID})
	if err != nil {
		t.Fatal(err)
	}
	buckets := map[string]inventory.BrowseRow{}
	for _, r := range browse.Rows {
		buckets[r.ProductID] = r
	}

	rec := get(t, liveHandler(t, pool), "/api/v1/exports/catalogue.xlsx")
	sheet := parseSheet(t, parts(t, rec.Body.Bytes())["xl/worksheets/sheet1.xml"])

	var checkedPrices, checkedStock, checkedBlanks int
	for i, p := range rows {
		row := 4 + i // title, subtitle, header, then the data
		if c, _ := cellAt(sheet, cellRef(0, row)); c.Inline.Text != p.SKU {
			t.Fatalf("row %d SKU = %q, want %q (the export is not in the service's order)", row, c.Inline.Text, p.SKU)
		}
		if c, _ := cellAt(sheet, cellRef(1, row)); c.Inline.Text != p.Name {
			t.Errorf("row %d name = %q, want %q", row, c.Inline.Text, p.Name)
		}
		if c, _ := cellAt(sheet, cellRef(7, row)); c.Inline.Text != p.BaseUOM {
			t.Errorf("row %d qty UOM = %q, want %q, a quantity without its unit is a number nobody can act on", row, c.Inline.Text, p.BaseUOM)
		}

		// List price: the money cell holds the anchor in cents, and the DIB-437 unit sits beside it.
		priceCell, hasPrice := cellAt(sheet, cellRef(8, row))
		if p.ListPriceCents == nil {
			if hasPrice {
				t.Errorf("row %d has a price cell (%+v) for a product with no anchor", row, priceCell)
			}
			checkedBlanks++
		} else {
			if !hasPrice {
				t.Fatalf("row %d is missing its list price", row)
			}
			if want := formatCents(*p.ListPriceCents); priceCell.Value != want {
				t.Errorf("row %d price = %q, want %q", row, priceCell.Value, want)
			}
			if priceCell.Type != "" {
				t.Errorf("row %d price is typed %q, money must be a number", row, priceCell.Type)
			}
			if c, _ := cellAt(sheet, cellRef(9, row)); c.Inline.Text != p.ListPriceUOM {
				t.Errorf("row %d price UOM = %q, want %q (DIB-437: the ANCHOR's unit)", row, c.Inline.Text, p.ListPriceUOM)
			}
			checkedPrices++
		}

		// On hand: the sum of the per-branch map, blank when the ledger has no row at all.
		onHand, hasOnHand := cellAt(sheet, cellRef(5, row))
		if total := onHandTotal(p); total == nil {
			if hasOnHand {
				t.Errorf("row %d on hand = %+v for a product with no ledger row, that must be blank, not 0", row, onHand)
			}
			checkedBlanks++
		} else {
			got, err := strconv.ParseFloat(onHand.Value, 64)
			if err != nil || got != *total {
				t.Errorf("row %d on hand = %q, want %v", row, onHand.Value, *total)
			}
			checkedStock++
		}

		// Available: straight from the batched browse read, blank when it returned no position.
		avail, hasAvail := cellAt(sheet, cellRef(6, row))
		if b, ok := buckets[p.ID]; ok {
			got, err := strconv.ParseFloat(avail.Value, 64)
			if err != nil || got != b.AvailableBase {
				t.Errorf("row %d available = %q, want %v from the browse read", row, avail.Value, b.AvailableBase)
			}
		} else if hasAvail {
			t.Errorf("row %d available = %+v with no browse row", row, avail)
		}
	}
	if checkedPrices == 0 {
		t.Error("the seed exercised no priced product, this test proves nothing about money")
	}
	if checkedStock == 0 {
		t.Error("the seed exercised no stocked product, this test proves nothing about quantities")
	}
	t.Logf("checked %d rows: %d priced, %d stocked, %d blank cells asserted", len(rows), checkedPrices, checkedStock, checkedBlanks)
}

func TestIntegration_TheFilterAndSortReachTheDatabase(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	var manufacturer string
	if err := pool.QueryRow(ctx, `
		SELECT manufacturer FROM products
		WHERE is_active AND manufacturer <> ''
		GROUP BY manufacturer ORDER BY count(*) DESC LIMIT 1`).Scan(&manufacturer); err != nil {
		t.Skipf("no manufacturer in the seed: %v", err)
	}
	h := liveHandler(t, pool)

	all := parseSheet(t, parts(t, get(t, h, "/api/v1/exports/catalogue.xlsx").Body.Bytes())["xl/worksheets/sheet1.xml"])
	filteredURL := "/api/v1/exports/catalogue.xlsx?manufacturer=" + url.QueryEscape(manufacturer) + "&sort=list_price&dir=desc"
	filtered := parseSheet(t, parts(t, get(t, h, filteredURL).Body.Bytes())["xl/worksheets/sheet1.xml"])

	if len(filtered.Rows) >= len(all.Rows) {
		t.Errorf("the filter changed nothing: %d rows filtered vs %d unfiltered", len(filtered.Rows), len(all.Rows))
	}
	if len(filtered.Rows) <= 3 {
		t.Fatalf("the filter removed everything (%d rows), %q should match products", len(filtered.Rows), manufacturer)
	}
	// Every row is that manufacturer, and the prices descend with the blanks at the bottom.
	prev := ""
	seenBlank := false
	for i := 4; i <= len(filtered.Rows)+1; i++ {
		c, ok := cellAt(filtered, cellRef(2, i))
		if !ok {
			continue
		}
		if c.Inline.Text != manufacturer {
			t.Errorf("row %d is %q, not the filtered manufacturer %q", i, c.Inline.Text, manufacturer)
		}
		price, hasPrice := cellAt(filtered, cellRef(8, i))
		if !hasPrice {
			seenBlank = true
			continue
		}
		if seenBlank {
			t.Errorf("row %d has a price after an unpriced row, unknown must sort LAST, not first", i)
		}
		if prev != "" {
			a, _ := strconv.ParseFloat(prev, 64)
			b, _ := strconv.ParseFloat(price.Value, 64)
			if b > a {
				t.Errorf("row %d price %v is above the previous %v in a descending sort", i, b, a)
			}
		}
		prev = price.Value
	}
	// The provenance line carries the filter that produced the file.
	if c, _ := cellAt(filtered, "A2"); c.Inline.Text == "" {
		t.Error("the filtered export carries no provenance line")
	}
}

func TestIntegration_AFilterThatMatchesNothingStillDownloadsASheet(t *testing.T) {
	pool := intPool(t)
	rec := get(t, liveHandler(t, pool), "/api/v1/exports/catalogue.xlsx?q=zzz-no-such-product-zzz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want a real file", rec.Code)
	}
	sheet := parseSheet(t, parts(t, rec.Body.Bytes())["xl/worksheets/sheet1.xml"])
	if len(sheet.Rows) != 3 {
		t.Errorf("rows = %d, want title + subtitle + header only", len(sheet.Rows))
	}
	if c, _ := cellAt(sheet, "A3"); c.Inline.Text != "SKU" {
		t.Error("an empty result must still carry its headers, a button that does nothing reads as broken")
	}
}

// stampSeededBranches puts the seeded branches (which carry no org) in the fixture north org for the
// leg, so the stock reads, which hold to the caller's org (UX-88), have branches to answer for. The
// request the handler leg makes resolves to the same org.
func stampSeededBranches(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE locations SET org_id = $1 WHERE loc_type = 'BRANCH' AND org_id IS NULL`, testkit.OrgNorthID); err != nil {
		t.Fatalf("stamp the seeded branches: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `UPDATE locations SET org_id = NULL WHERE code IN ('TRT','KGN')`)
	})
}
