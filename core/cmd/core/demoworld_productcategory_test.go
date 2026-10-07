//go:build demo

package main

// demoworld_productcategory_test.go: proof that every demo product carries a category the
// catalogue knows, including on a box an older seed filled.
//
// The visual check that produced this proof: the till showed "PRODUCT" as the category of
// "Holland Paver, Charcoal (demo)" and other demo products, so they got no category tile and the
// quick picks had no Pavers tile. products.application was EMPTY on the live box: an older seed
// left it empty, and the applier inserts with DO NOTHING, so a fresh plan's value never reached
// it. The demoseed_repair pair's guarded UPDATE fills it from demoProducts, the table the fresh
// seed writes from.

import (
	"context"
	"os"
	"testing"
	"time"
)

// knownApplications are the application words the till's product tiles answer to (the design
// system's product-tiles WORDS table, lower case); a demo product whose application is outside
// the set has no tile and renders under the "PRODUCT" fallback.
var knownApplications = map[string]bool{
	"wall": true, "paver": true, "step": true, "mulch": true, "soil": true,
	"sand": true, "base": true, "edging": true,
}

func TestDemoWorld_ProductCategories(t *testing.T) {
	dsn := os.Getenv(tillDemoDBEnvVar)
	if dsn == "" {
		t.Skip(tillDemoDBEnvVar + " is not set: the demo world proof needs its own throwaway local Postgres")
	}
	requireLocalDSN(t, dsn)
	ctx := context.Background()

	pool, testDSN := qwFreshDB(t, ctx, dsn, "demoworld_prodcat")
	t.Setenv("DATABASE_URL", testDSN)
	t.Setenv("CORE_DATABASE_URL", testDSN)
	t.Setenv("APP_ENV", "dev")
	if _, created := qwApply(t); created == 0 {
		t.Fatal("the first apply created nothing on a fresh database")
	}

	appOf := func(key string) (app string, updated time.Time) {
		t.Helper()
		var a *string
		if err := pool.QueryRow(ctx, `SELECT application, updated_at FROM products WHERE id = $1::uuid`,
			demoID("product:"+key)).Scan(&a, &updated); err != nil {
			t.Fatalf("reading %s: %v", key, err)
		}
		if a != nil {
			app = *a
		}
		return app, updated
	}
	assertAllKnown := func(when string) {
		t.Helper()
		for _, p := range demoProducts {
			got, _ := appOf(p.Key)
			if !knownApplications[lower(got)] {
				t.Errorf("%s: %s carries application %q, a category the catalogue does not know", when, p.SKU, got)
			}
			if got != p.Application {
				t.Errorf("%s: %s carries application %q, the plan writes %q", when, p.SKU, got, p.Application)
			}
		}
	}
	assertAllKnown("a fresh seed")

	// A box an older seed filled: three products empty (the column is NOT NULL, so an older seed's gap reads as an empty string), one a hand
	// re-categorised, plus a product that is not the demo world's with an empty application.
	if _, err := pool.Exec(ctx, `UPDATE products SET application = '' WHERE id = ANY($1::uuid[])`,
		[]string{demoID("product:P01"), demoID("product:P02"), demoID("product:P03")}); err != nil {
		t.Fatalf("planting empty applications: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE products SET application = 'Hand chosen' WHERE id = $1::uuid`, demoID("product:P04")); err != nil {
		t.Fatalf("planting a hand-chosen application: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO products (id, sku, name, base_uom, price_uom, is_active, application)
		VALUES ($1::uuid, 'REAL-EMPTY-APP', 'A real product', 'PC', 'PC', true, '')`, demoID("product:not-demo")); err != nil {
		t.Fatalf("planting a non-demo product: %v", err)
	}
	if app, _ := appOf("P01"); app != "" {
		t.Fatalf("the repair leg's premise is wrong: P01 still reads %q", app)
	}

	qwApply(t)
	for _, k := range []string{"P01", "P02", "P03"} {
		want := ""
		for _, p := range demoProducts {
			if p.Key == k {
				want = p.Application
			}
		}
		if got, _ := appOf(k); got != want {
			t.Errorf("after the repair apply %s reads %q, want %q", k, got, want)
		}
	}
	if got, _ := appOf("P04"); got != "Hand chosen" {
		t.Errorf("the repair overwrote a set application: P04 reads %q", got)
	}
	var other string
	if err := pool.QueryRow(ctx, `SELECT application FROM products WHERE sku = 'REAL-EMPTY-APP'`).Scan(&other); err != nil || other != "" {
		t.Errorf("the repair touched a product that is not the demo world's (%q, err %v)", other, err)
	}

	// A second apply repairs nothing: no row matches, so no updated_at moves.
	_, before := appOf("P01")
	qwApply(t)
	if _, after := appOf("P01"); !after.Equal(before) {
		t.Errorf("the second apply touched a repaired product (updated_at %s to %s)", before, after)
	}

	// Restore P04 to the plan's value so the whole-world check below reads the repaired world.
	if _, err := pool.Exec(ctx, `UPDATE products SET application = 'Wall' WHERE id = $1::uuid`, demoID("product:P04")); err != nil {
		t.Fatalf("restoring P04: %v", err)
	}
	assertAllKnown("the repaired box")
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
