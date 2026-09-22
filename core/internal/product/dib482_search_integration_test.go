//go:build integration

// dib482_search_integration_test.go, M3-G1-PRODSEARCH. The catalog type-ahead could not find a
// product by the one word an operator actually knows: its collection.
//
// These tests are PREMISE-PINNING. They do not merely assert that Search works now; they first
// measure the pg_trgm numbers that made it fail, so that if a later change reverts the operator (or
// somebody "fixes" this by moving the similarity threshold instead), the test says WHICH premise
// broke rather than only that a search went empty.
package product

import (
	"context"
	"strings"
	"testing"
)

// TestIntegration_DIB482_WholeStringSimilarityIsBelowThresholdForACollectionWord is the measurement
// the fix rests on. It asserts the ARITHMETIC, not the behaviour: whole-string similarity between a
// real catalog name and the collection word inside it is 0.25, and pg_trgm's default threshold is
// 0.3. 0.25 < 0.3 is the entire bug, `p.name % 'Antico'` was false for a product named 'Best Way
// Stone Antico Paver'.
//
// If pg_trgm's scoring or default threshold ever changes underneath us, this test fails FIRST and
// explains the rest of the file.
func TestIntegration_DIB482_WholeStringSimilarityIsBelowThresholdForACollectionWord(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	const name = "Best Way Stone Antico Paver"
	const term = "Antico"

	var whole, word float32
	if err := pool.QueryRow(ctx,
		`SELECT similarity($1, $2), word_similarity($2, $1)`, name, term).Scan(&whole, &word); err != nil {
		t.Fatal(err)
	}

	var threshold float32
	if err := pool.QueryRow(ctx, `SELECT show_limit()`).Scan(&threshold); err != nil {
		t.Fatal(err)
	}

	t.Logf("similarity(%q, %q) = %.3f · word_similarity(%q, %q) = %.3f · threshold = %.3f",
		name, term, whole, term, name, word, threshold)

	if whole >= threshold {
		t.Fatalf("PREMISE GONE: similarity(%q, %q) = %.3f is no longer below the %.3f threshold, "+
			"the whole-string operator would now match, and the DIB-482 diagnosis needs re-deriving",
			name, term, whole, threshold)
	}
	if word < threshold {
		t.Fatalf("word_similarity(%q, %q) = %.3f is below the threshold %.3f, the fix's own operator "+
			"would not match either", term, name, word, threshold)
	}
}

// TestIntegration_DIB482_TheOldOperatorMissesAntico_TheNewOneFindsIt runs BOTH operators against the
// live catalog in one test, so the pair is visible together: the shipped predicate returns the
// product and the predicate this ticket removed returns nothing.
//
// This is the mutation anchor for the operator. Put `p.name % $1` back in repository.go and the
// "new operator" half of this test dies.
func TestIntegration_DIB482_TheOldOperatorMissesAntico_TheNewOneFindsIt(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	const term = "Antico"

	// The catalog must actually contain the product, or this test proves nothing at all.
	var seeded int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM products WHERE name ILIKE '%' || $1 || '%' AND is_active`, term).Scan(&seeded); err != nil {
		t.Fatal(err)
	}
	if seeded == 0 {
		t.Fatalf("no active product has %q in its name, reseed before reading anything into this test", term)
	}

	var old int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM products p WHERE p.name % $1 AND p.is_active`, term).Scan(&old); err != nil {
		t.Fatal(err)
	}
	if old != 0 {
		t.Fatalf("the OLD whole-string operator now finds %d row(s) for %q, it found 0 when this "+
			"ticket was written; the premise moved", old, term)
	}

	// And now the seam itself, through the real repository.
	repo := NewRepository(pool)
	got, err := repo.Search(ctx, term, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatalf("Search(%q) returned nothing, the word-similarity predicate is not in force "+
			"(%d such product(s) are seeded and active)", term, seeded)
	}
	var found bool
	for _, r := range got {
		if strings.Contains(strings.ToLower(r.Name), strings.ToLower(term)) {
			found = true
		}
	}
	if !found {
		t.Fatalf("Search(%q) returned %d row(s), none containing the term: %+v", term, len(got), got)
	}
}

// TestIntegration_DIB482_TechoFindsTheWholeCollection is the second half of the reported gap. 'Techo'
// found 0 of the seeded Techo-Bloc products, because every one of those names is long enough to
// dilute a whole-string score to near zero. A collection search must return the COLLECTION, not a
// lucky subset of it.
func TestIntegration_DIB482_TechoFindsTheWholeCollection(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	const term = "Techo"

	var seeded int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM products WHERE name ILIKE $1 || '%' AND is_active`, term).Scan(&seeded); err != nil {
		t.Fatal(err)
	}
	if seeded == 0 {
		t.Skipf("no active product name starts with %q on this database", term)
	}

	repo := NewRepository(pool)
	// limit is deliberately above the seeded count: the assertion is "all of them", and a limit that
	// happened to equal the count would pass while hiding a predicate that only found some.
	got, err := repo.Search(ctx, term, seeded+5)
	if err != nil {
		t.Fatal(err)
	}
	var hits int
	for _, r := range got {
		if strings.HasPrefix(strings.ToLower(r.Name), strings.ToLower(term)) {
			hits++
		}
	}
	if hits != seeded {
		t.Fatalf("Search(%q) found %d of the %d seeded %s products, a collection search that returns "+
			"a subset is worse than one that returns nothing, because the operator believes it",
			term, hits, seeded, term)
	}
}

// TestIntegration_DIB482_SkuPrefixBranchSurvivesTheOperatorSwap guards the half of the WHERE clause
// this ticket did NOT change. Swapping the name operator must not cost an operator the ability to
// type a code, that is the fastest path in the picker and the one a counter hand uses all day.
func TestIntegration_DIB482_SkuPrefixBranchSurvivesTheOperatorSwap(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	var sku string
	if err := pool.QueryRow(ctx,
		`SELECT sku::text FROM products WHERE is_active ORDER BY sku LIMIT 1`).Scan(&sku); err != nil {
		t.Fatal(err)
	}

	repo := NewRepository(pool)
	got, err := repo.Search(ctx, sku, 10)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range got {
		if r.SKU == sku {
			found = true
		}
	}
	if !found {
		t.Fatalf("Search(%q) did not return the product with that exact SKU, the ILIKE prefix branch "+
			"was lost in the operator swap (got %d row(s))", sku, len(got))
	}

	// A LIKE metacharacter in the term must still be matched literally, not as a wildcard. If the
	// escaping regressed, '%' alone would drag back the whole catalog.
	wild, err := repo.Search(ctx, "%", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range wild {
		if strings.HasPrefix(r.SKU, "%") {
			continue
		}
		if !strings.Contains(strings.ToLower(r.Name), "%") {
			t.Fatalf("Search(%q) returned %q/%q, which contains no literal '%%', the LIKE escape "+
				"regressed and '%%' is acting as a wildcard again", "%", r.SKU, r.Name)
		}
	}
}

// TestIntegration_DIB482_RankingPutsTheCloserNameFirst pins the ORDER BY. Both rows below match the
// term on word similarity (1.0 each), so the SECOND ranking level decides, and it must put the
// product whose whole name is the term above the long descriptive one. That second level is the
// ranking this seam always had; the ticket kept it deliberately rather than replacing it.
func TestIntegration_DIB482_RankingPutsTheCloserNameFirst(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	// Two throwaway rows in one transaction, rolled back, this test must not leave catalog behind
	// for the next test in the package to trip over.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const exact = "Dib482 Rankcheck"
	const long = "Dib482 Rankcheck Premium Tumbled Paver Charcoal"
	for i, name := range []string{long, exact} { // inserted long-first so insertion order cannot flatter us
		if _, err := tx.Exec(ctx, `
			INSERT INTO products (sku, name, base_uom, price_uom, is_active)
			VALUES ($1, $2, 'EA', 'EA', TRUE)`, "DIB482-RANK-"+string(rune('A'+i)), name); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := tx.Query(ctx, `
		SELECT p.name
		FROM products p
		WHERE ($1 <% p.name OR p.sku::text ILIKE $2 ESCAPE '\') AND p.is_active
		ORDER BY word_similarity($1, p.name) DESC, similarity(p.name, $1) DESC, p.name
		LIMIT 10`, "Rankcheck", "Rankcheck%")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(names) < 2 {
		t.Fatalf("expected both seeded rows back, got %v", names)
	}
	if names[0] != exact {
		t.Fatalf("ranking put %q first; %q is the closer name and must lead (order: %v)", names[0], exact, names)
	}
}
