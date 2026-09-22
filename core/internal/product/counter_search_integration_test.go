//go:build integration

// counter_search_integration_test.go, GABLE:pos-search, search-synonyms, pos-barcode,
// product-images-ui against real Postgres.
//
// The ranking, the synonym join, the barcode uniqueness index and the image round-trip are all
// properties of SQL and of migrations 0101/0102. A fake store can only prove that a fake agrees with
// itself, so every claim that matters is asserted here.
//
// Run: DATABASE_URL=... go test -tags integration -count=1 ./internal/product/...
package product

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func ciPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
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

// runSalt keeps fixtures from colliding across concurrent packages and repeated runs (the DIB-384
// lesson: a counter that restarts each process is not unique against a database that does not).
var runSalt = fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano()%1_000_000)

// seedProduct inserts a disposable product and removes it (and everything cascading off it) on
// cleanup, so the catalogue is left exactly as found.
func seedProduct(t *testing.T, pool *pgxpool.Pool, sku, name, collection, application string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO products (sku, name, collection, application, base_uom, price_uom, is_active)
		VALUES ($1, $2, $3, $4, 'EA', 'EA', TRUE) RETURNING id::text`,
		sku, name, collection, application).Scan(&id)
	if err != nil {
		t.Fatalf("seed product %s: %v", sku, err)
	}
	t.Cleanup(func() {
		// ON DELETE CASCADE on product_barcodes / product_images / product_search_synonyms takes the
		// children with it.
		_, _ = pool.Exec(context.Background(), `DELETE FROM products WHERE id = $1`, id)
	})
	return id
}

// - GABLE:search-synonyms, the named alias test -

// TestIntegration_CounterSearch_SynonymHit is the named synonym test.
//
// The starter set maps "patio stone" → "Slab" (migration 0101). A catalogue that calls the product a
// Slab and a customer who calls it a patio stone share NO substring, so nothing but the alias table
// can bridge them: this is precisely the query the old client-side `name.includes(q)` filter
// returned empty for.
//
// MUTATION TARGET: the synMatch EXISTS clause in Repository.CounterSearch (counter_search.go), drop
// it from the WHERE, or from the CASE, and "patio stone" stops finding the Slab.
func TestIntegration_CounterSearch_SynonymHit(t *testing.T) {
	ctx := context.Background()
	pool := ciPool(t)
	svc := NewService(NewRepository(pool))

	sku := "SYN-SLAB-" + runSalt
	id := seedProduct(t, pool, sku, "Quarry Edge "+runSalt, "Melville", "Slab")

	// The word the customer says. It appears NOWHERE in the product's name, sku or collection.
	hits, err := svc.CounterSearch(ctx, CounterSearchQuery{Q: "patio stone", Limit: 50})
	if err != nil {
		t.Fatalf("counter search: %v", err)
	}
	var found *CounterHit
	for i := range hits {
		if hits[i].ProductID == id {
			found = &hits[i]
		}
	}
	if found == nil {
		t.Fatalf("the alias 'patio stone' → 'Slab' must find the Slab product; got %d hits and not this one", len(hits))
	}
	if found.MatchReason != matchSynonym {
		t.Fatalf("match_reason = %q, want %q so the cashier can see WHY the row is listed",
			found.MatchReason, matchSynonym)
	}
	// Control: the raw term really does not match by any ordinary arm. If this ever starts passing,
	// the test above has stopped proving anything about synonyms.
	for _, field := range []string{"Quarry Edge " + runSalt, sku, "Melville"} {
		if strings.Contains(strings.ToLower(field), "patio stone") {
			t.Fatalf("fixture is not a real synonym case, %q already contains the term", field)
		}
	}

	t.Run("a deactivated alias stops matching", func(t *testing.T) {
		// The is_active column is the kill switch for a bad alias. Prove it is actually consulted.
		if _, err := pool.Exec(ctx,
			`UPDATE product_search_synonyms SET is_active = FALSE WHERE term = 'patio stone'`); err != nil {
			t.Fatalf("deactivate: %v", err)
		}
		defer func() {
			_, _ = pool.Exec(context.Background(),
				`UPDATE product_search_synonyms SET is_active = TRUE WHERE term = 'patio stone'`)
		}()
		hits, err := svc.CounterSearch(ctx, CounterSearchQuery{Q: "patio stone", Limit: 50})
		if err != nil {
			t.Fatalf("counter search: %v", err)
		}
		for _, h := range hits {
			if h.ProductID == id {
				t.Fatal("a deactivated alias must stop matching")
			}
		}
	})

	t.Run("a product-scoped alias pins one product", func(t *testing.T) {
		term := "thesixty" + runSalt
		if _, err := pool.Exec(ctx,
			`INSERT INTO product_search_synonyms (term, canonical, product_id, source)
			 VALUES ($1, 'irrelevant-canonical', $2, 'STAFF')`, term, id); err != nil {
			t.Fatalf("insert scoped alias: %v", err)
		}
		defer func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM product_search_synonyms WHERE term = $1`, term)
		}()
		hits, err := svc.CounterSearch(ctx, CounterSearchQuery{Q: term, Limit: 50})
		if err != nil {
			t.Fatalf("counter search: %v", err)
		}
		if len(hits) != 1 || hits[0].ProductID != id {
			t.Fatalf("a product-scoped alias must resolve to exactly its product, got %d hits", len(hits))
		}
	})
}

// - GABLE:pos-search, relevance ordering -

// TestIntegration_CounterSearch_RelevanceOrder proves the rank ladder the counter depends on: an
// exact SKU outranks a prefix, which outranks a substring. A search that puts a description match
// above the SKU the cashier typed in full is a search they will stop trusting.
//
// MUTATION TARGET: the CASE arms in Repository.CounterSearch. Swap any two rank values (e.g. make
// the SKU-exact arm 3 and the name-substring arm 0) and the ordering assertions below fail.
func TestIntegration_CounterSearch_RelevanceOrder(t *testing.T) {
	ctx := context.Background()
	pool := ciPool(t)
	svc := NewService(NewRepository(pool))

	// Three products that all match the term "ZQ" + salt, by three different arms.
	term := "ZQTERM" + strings.ToUpper(strings.ReplaceAll(runSalt, "-", ""))
	exact := seedProduct(t, pool, term, "Alpha Unrelated Name", "C1", "Paver")                  // SKU exact
	prefix := seedProduct(t, pool, term+"-EXTRA", "Beta Unrelated Name", "C2", "Paver")         // SKU prefix
	nameSub := seedProduct(t, pool, "ZZ-OTHER-"+runSalt, "Gamma "+term+" Stone", "C3", "Paver") // name substring

	hits, err := svc.CounterSearch(ctx, CounterSearchQuery{Q: term, Limit: 50})
	if err != nil {
		t.Fatalf("counter search: %v", err)
	}
	pos := map[string]int{}
	for i, h := range hits {
		pos[h.ProductID] = i
	}
	for _, id := range []string{exact, prefix, nameSub} {
		if _, ok := pos[id]; !ok {
			t.Fatalf("every seeded product must be found; missing %s (got %d hits)", id, len(hits))
		}
	}
	if !(pos[exact] < pos[prefix] && pos[prefix] < pos[nameSub]) {
		t.Fatalf("relevance order broken: exact=%d prefix=%d nameSubstring=%d, want exact < prefix < substring",
			pos[exact], pos[prefix], pos[nameSub])
	}
	if hits[pos[exact]].MatchReason != matchExact {
		t.Fatalf("the SKU-exact hit must report %q, got %q", matchExact, hits[pos[exact]].MatchReason)
	}

	t.Run("a one-character term does not scan the catalogue", func(t *testing.T) {
		got, err := svc.CounterSearch(ctx, CounterSearchQuery{Q: "z"})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("a single keystroke must return nothing, got %d hits", len(got))
		}
	})

	t.Run("a LIKE metacharacter is matched literally", func(t *testing.T) {
		// '%' must not turn the prefix lookup into "everything".
		got, err := svc.CounterSearch(ctx, CounterSearchQuery{Q: "%%", Limit: 50})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("'%%%%' must match literally, not wildcard the catalogue, got %d hits", len(got))
		}
	})

	t.Run("under the 150ms counter target on the seeded set", func(t *testing.T) {
		// Not a benchmark, a regression fence: the point of moving the filter server-side was that it
		// stays fast as the catalogue grows. If this ever trips, someone added a per-row subquery.
		start := time.Now()
		for i := 0; i < 5; i++ {
			if _, err := svc.CounterSearch(ctx, CounterSearchQuery{Q: "paver", Limit: 20}); err != nil {
				t.Fatalf("search: %v", err)
			}
		}
		if avg := time.Since(start) / 5; avg > 150*time.Millisecond {
			t.Fatalf("counter search averaged %v, over the 150ms target", avg)
		}
	})
}

// - GABLE:pos-barcode -

// TestIntegration_Barcode_UniqueAcrossProducts is the named barcode-uniqueness test.
//
// MUTATION TARGET: `CREATE UNIQUE INDEX uq_product_barcodes_code ON product_barcodes (code)` in
// migration 0101. Drop the UNIQUE (or scope it to (product_id, code), the tempting wrong version)
// and the second insert below succeeds, at which point a scan is ambiguous and the counter has to
// show a picker, which is slower than typing the SKU and defeats the whole feature.
func TestIntegration_Barcode_UniqueAcrossProducts(t *testing.T) {
	ctx := context.Background()
	pool := ciPool(t)

	a := seedProduct(t, pool, "BC-A-"+runSalt, "Barcode Fixture A", "C", "Paver")
	b := seedProduct(t, pool, "BC-B-"+runSalt, "Barcode Fixture B", "C", "Paver")
	code := "0629" + strings.ReplaceAll(runSalt, "-", "")[:8]

	if _, err := pool.Exec(ctx,
		`INSERT INTO product_barcodes (product_id, code, symbology) VALUES ($1,$2,'UPC_A')`, a, code); err != nil {
		t.Fatalf("first barcode must insert: %v", err)
	}
	// THE assertion: the same physical label cannot belong to two products.
	_, err := pool.Exec(ctx,
		`INSERT INTO product_barcodes (product_id, code, symbology) VALUES ($1,$2,'UPC_A')`, b, code)
	if err == nil {
		t.Fatal("the same barcode on a second product must be refused, a scan that resolves to two " +
			"products cannot resolve to a line")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "uq_product_barcodes_code") {
		t.Fatalf("want the catalogue-wide uniqueness index to fire, got %v", err)
	}

	t.Run("case-insensitive: CITEXT means one code, not two", func(t *testing.T) {
		alpha := "AB" + strings.ReplaceAll(runSalt, "-", "")[:6]
		if _, err := pool.Exec(ctx,
			`INSERT INTO product_barcodes (product_id, code) VALUES ($1,$2)`, a, alpha); err != nil {
			t.Fatalf("seed alpha code: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO product_barcodes (product_id, code) VALUES ($1,$2)`, b, strings.ToLower(alpha)); err == nil {
			t.Fatal("a hand-typed code differing only in case must not become a second row")
		}
	})

	t.Run("at most one yard-owned primary label per product", func(t *testing.T) {
		p1 := "P1" + strings.ReplaceAll(runSalt, "-", "")[:6]
		p2 := "P2" + strings.ReplaceAll(runSalt, "-", "")[:6]
		if _, err := pool.Exec(ctx,
			`INSERT INTO product_barcodes (product_id, code, is_primary) VALUES ($1,$2,TRUE)`, a, p1); err != nil {
			t.Fatalf("first primary: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO product_barcodes (product_id, code, is_primary) VALUES ($1,$2,TRUE)`, a, p2); err == nil {
			t.Fatal("a second primary label on one product must be refused")
		}
	})
}

// TestIntegration_ResolveScan proves the keyboard-wedge rail: barcode wins over SKU, an unknown code
// is not-found (never a silent empty result), and the scan is exact rather than fuzzy.
func TestIntegration_ResolveScan(t *testing.T) {
	ctx := context.Background()
	pool := ciPool(t)
	svc := NewService(NewRepository(pool))

	scanned := seedProduct(t, pool, "SCAN-TARGET-"+runSalt, "Scan Target", "C", "Paver")
	decoy := seedProduct(t, pool, "SCAN-DECOY-"+runSalt, "Scan Decoy", "C", "Paver")
	code := "0621" + strings.ReplaceAll(runSalt, "-", "")[:8]
	if _, err := pool.Exec(ctx,
		`INSERT INTO product_barcodes (product_id, code, symbology) VALUES ($1,$2,'UPC_A')`, scanned, code); err != nil {
		t.Fatalf("seed barcode: %v", err)
	}

	t.Run("a scanned barcode resolves to its product", func(t *testing.T) {
		hit, err := svc.ResolveScan(ctx, code, "")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if hit.ProductID != scanned {
			t.Fatalf("resolved to %s, want the barcode's product %s", hit.ProductID, scanned)
		}
	})

	t.Run("a typed SKU resolves too, the same box serves both", func(t *testing.T) {
		hit, err := svc.ResolveScan(ctx, "SCAN-DECOY-"+runSalt, "")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if hit.ProductID != decoy {
			t.Fatalf("resolved to %s, want %s", hit.ProductID, decoy)
		}
	})

	t.Run("SKU lookup is case-insensitive", func(t *testing.T) {
		hit, err := svc.ResolveScan(ctx, strings.ToLower("SCAN-DECOY-"+runSalt), "")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if hit.ProductID != decoy {
			t.Fatalf("a lower-cased SKU must still resolve, got %s", hit.ProductID)
		}
	})

	t.Run("an unknown code is not-found, never an empty success", func(t *testing.T) {
		_, err := svc.ResolveScan(ctx, "0000000000000", "")
		if err == nil {
			t.Fatal("an unknown code must not resolve, a scan that silently finds nothing is how the " +
				"wrong item ends up on a ticket")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Fatalf("want a not-found error, got %v", err)
		}
	})

	t.Run("a partial code does not resolve", func(t *testing.T) {
		// BOTH arms, because they are separate SQL branches and a mutation that loosens one is
		// invisible to a test that only exercises the other. (Discovered by mutation M14: loosening
		// the SKU comparison to a prefix survived a test that truncated only the barcode, because no
		// seeded SKU starts with digits.)
		if _, err := svc.ResolveScan(ctx, code[:len(code)-2], ""); err == nil {
			t.Fatal("a truncated BARCODE must not resolve, a prefix match would ring the wrong product")
		}
		sku := "SCAN-DECOY-" + runSalt
		if _, err := svc.ResolveScan(ctx, sku[:len(sku)-3], ""); err == nil {
			t.Fatal("a truncated SKU must not resolve either, the scan box takes typed SKUs, and a " +
				"cashier who stops typing halfway must get nothing rather than a neighbouring product")
		}
	})

	t.Run("an inactive product is not scannable", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `UPDATE products SET is_active = FALSE WHERE id = $1`, scanned); err != nil {
			t.Fatalf("deactivate: %v", err)
		}
		defer func() {
			_, _ = pool.Exec(context.Background(), `UPDATE products SET is_active = TRUE WHERE id = $1`, scanned)
		}()
		if _, err := svc.ResolveScan(ctx, code, ""); err == nil {
			t.Fatal("a discontinued product must not be sellable by scan")
		}
	})
}

// - GABLE:product-images-ui -

// pngBytes is the smallest real PNG: signature + IHDR/IDAT/IEND for a 1x1 image. Real magic bytes,
// so the sniff check is genuinely exercised rather than bypassed.
var pngBytes = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00,
	0x1f, 0x15, 0xc4, 0x89,
	0x00, 0x00, 0x00, 0x0a, 'I', 'D', 'A', 'T',
	0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05, 0x00, 0x01,
	0x0d, 0x0a, 0x2d, 0xb4,
	0x00, 0x00, 0x00, 0x00, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
}

func TestIntegration_ProductImages(t *testing.T) {
	ctx := context.Background()
	pool := ciPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo).WithImages(repo)

	id := seedProduct(t, pool, "IMG-"+runSalt, "Image Fixture", "C", "Paver")

	t.Run("absent image is not-found, so the UI can show its placeholder", func(t *testing.T) {
		if _, err := svc.GetPrimaryImage(ctx, id); err == nil {
			t.Fatal("a product with no thumbnail must report not-found rather than empty bytes")
		}
	})

	t.Run("round-trip", func(t *testing.T) {
		if err := svc.PutPrimaryImage(ctx, id, "image/png", "a paver", pngBytes, ""); err != nil {
			t.Fatalf("put: %v", err)
		}
		img, err := svc.GetPrimaryImage(ctx, id)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if !bytes.Equal(img.Bytes, pngBytes) {
			t.Fatal("the bytes read back must be the bytes written")
		}
		if img.ContentType != "image/png" || img.ByteSize != len(pngBytes) || img.AltText != "a paver" {
			t.Fatalf("metadata round-trip broken: %+v", img)
		}
		if img.Checksum == "" {
			t.Fatal("a checksum must be stored, it is the ETag the byte route revalidates against")
		}
	})

	t.Run("re-upload replaces rather than duplicates", func(t *testing.T) {
		if err := svc.PutPrimaryImage(ctx, id, "image/png", "replaced", pngBytes, ""); err != nil {
			t.Fatalf("re-put: %v", err)
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM product_images WHERE product_id = $1`, id).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 1 {
			t.Fatalf("want exactly one primary thumbnail per product, got %d rows", n)
		}
	})

	t.Run("counter search reports has_image", func(t *testing.T) {
		hits, err := svc.CounterSearch(ctx, CounterSearchQuery{Q: "Image Fixture", Limit: 50})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		for _, h := range hits {
			if h.ProductID == id && !h.HasImage {
				t.Fatal("has_image must be true once a thumbnail exists, so the UI skips a request that would 404")
			}
		}
	})

	t.Run("content type outside the allowlist is refused", func(t *testing.T) {
		for _, ct := range []string{"image/svg+xml", "text/html", "application/pdf", ""} {
			if err := svc.PutPrimaryImage(ctx, id, ct, "", pngBytes, ""); err == nil {
				t.Fatalf("content type %q must be refused, these bytes are served back to a browser", ct)
			}
		}
	})

	t.Run("bytes that disagree with the declared type are refused", func(t *testing.T) {
		html := []byte("<html><script>alert(1)</script></html>")
		if err := svc.PutPrimaryImage(ctx, id, "image/png", "", html, ""); err == nil {
			t.Fatal("HTML declared as image/png must be refused, a declared type is a claim by the uploader")
		}
	})

	t.Run("oversized upload is refused at the door", func(t *testing.T) {
		big := make([]byte, maxImageBytes+1)
		copy(big, pngBytes)
		if err := svc.PutPrimaryImage(ctx, id, "image/png", "", big, ""); err == nil {
			t.Fatal("an upload over the 1 MiB thumbnail cap must be refused")
		}
	})

	t.Run("the DB cap is the backstop, independent of the service", func(t *testing.T) {
		// The service refuses oversize at the door; 0102's CHECK is what binds a writer that does not
		// go through the service. MUTATION TARGET: the byte_size CHECK in migration 0102.
		big := make([]byte, maxImageBytes+1)
		copy(big, pngBytes)
		_, err := pool.Exec(ctx, `
			INSERT INTO product_images (product_id, content_type, byte_size, image_bytes, sort_order)
			VALUES ($1, 'image/png', $2, $3, 1)`, id, len(big), big)
		if err == nil {
			t.Fatal("the byte_size CHECK must refuse an oversized row written directly")
		}
	})

	t.Run("delete is idempotent and leaves the product", func(t *testing.T) {
		if err := svc.DeletePrimaryImage(ctx, id); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if err := svc.DeletePrimaryImage(ctx, id); err != nil {
			t.Fatalf("deleting an absent image must be a no-op, got %v", err)
		}
		if _, err := svc.Get(ctx, id); err != nil {
			t.Fatalf("the product must survive its thumbnail: %v", err)
		}
	})
}

// TestIntegration_CounterSearch_BranchStockDisplay proves the branch-aware on-hand the counter reads
// off each hit, through the same zero-trust seam the catalogue list uses.
func TestIntegration_CounterSearch_BranchStockDisplay(t *testing.T) {
	ctx := context.Background()
	pool := ciPool(t)

	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations LIMIT 1`).Scan(&branchID); err != nil {
		t.Fatalf("no seeded branch: %v", err)
	}
	id := seedProduct(t, pool, "STK-"+runSalt, "Stock Display Fixture", "C", "Paver")

	svc := NewService(NewRepository(pool)).WithBranchStock(fixedBranchStock{
		byProduct: map[string]map[string]float64{id: {branchID: 42.5}},
	})
	hits, err := svc.CounterSearch(ctx, CounterSearchQuery{Q: "Stock Display Fixture", BranchID: branchID, Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("fixture product must be findable")
	}
	h := hits[0]
	if h.OnHandBranch == nil || *h.OnHandBranch != 42.5 {
		t.Fatalf("on_hand_branch = %v, want 42.5 for the requested branch", h.OnHandBranch)
	}

	// Without a branch the hit still carries the full per-branch map, but no single-branch scalar -
	// "no branch asked" and "zero at this branch" must not look the same.
	unscoped, err := svc.CounterSearch(ctx, CounterSearchQuery{Q: "Stock Display Fixture", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if unscoped[0].OnHandBranch != nil {
		t.Fatal("with no branch requested there is no single-branch on-hand to report")
	}
	if len(unscoped[0].OnHandByBranch) == 0 {
		t.Fatal("the per-branch map must still be present")
	}

	// A failing stock read degrades the row rather than the search: a cashier can ring a product whose
	// on-hand lookup timed out.
	degraded := NewService(NewRepository(pool)).WithBranchStock(fixedBranchStock{err: fmt.Errorf("stock down")})
	got, err := degraded.CounterSearch(ctx, CounterSearchQuery{Q: "Stock Display Fixture", BranchID: branchID, Limit: 10})
	if err != nil {
		t.Fatalf("a stock outage must not fail the search: %v", err)
	}
	if len(got) == 0 || got[0].OnHandBranch != nil {
		t.Fatalf("want the row without stock detail, got %+v", got)
	}
}

type fixedBranchStock struct {
	byProduct map[string]map[string]float64
	err       error
}

func (f fixedBranchStock) OnHandByBranch(_ context.Context, _ []string) (map[string]map[string]float64, error) {
	return f.byProduct, f.err
}
