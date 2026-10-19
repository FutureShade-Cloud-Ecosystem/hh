//go:build demo

package main

// demoseed_glsworld_tax_test.go, the demo build's seed prices at the dealer's sales tax: the San
// Diego County combined rate, 7.75 percent, on every seeded taxable line. The default build keeps
// the first dealer's rate (the build gate's proof is in demoseed_glsworld_build_test.go).

import "testing"

func TestGLSWorldTax_SeedPricesAtTheSanDiegoCountyRate(t *testing.T) {
	if hstBps != 775 {
		t.Fatalf("the demo seed prices at %d bps, want the San Diego County combined rate, 775", hstBps)
	}
	// Half up once on the header: 100.00 is 7.75, 1.00 is 7.75 cents which rounds to 8, and 6.45
	// is 49.99 cents which rounds to 50.
	for sub, want := range map[int64]int64{10000: 775, 100: 8, 645: 50, 0: 0} {
		if got := taxOn(sub); got != want {
			t.Errorf("taxOn(%d) = %d, want %d", sub, got, want)
		}
	}
}
