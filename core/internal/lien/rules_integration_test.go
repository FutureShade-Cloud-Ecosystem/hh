//go:build integration

package lien

import (
	"context"
	"testing"
)

// RT-1 item 1: the province rules table (migration 0471). Ontario is seeded at 10 percent, 60 and 90,
// the bounds are the table's own, and the dealer rule row exists from the migration.

func TestIntegration_RT1_OntarioIsSeeded(t *testing.T) {
	pool := intPool(t)
	var pct, preserve, perfect int
	err := pool.QueryRow(context.Background(),
		`SELECT holdback_pct_bps, preserve_days, perfect_days FROM holdback_province_rules WHERE province = 'ON'`,
	).Scan(&pct, &preserve, &perfect)
	if err != nil {
		t.Fatalf("read the Ontario rule: %v", err)
	}
	if pct != 1000 || preserve != 60 || perfect != 90 {
		t.Fatalf("Ontario = %d bps, %d, %d; want 1000, 60, 90", pct, preserve, perfect)
	}
}

func TestIntegration_RT1_ProvinceBoundsAreTheTables(t *testing.T) {
	pool := intPool(t)
	for _, bad := range []struct {
		name    string
		pct     int
		pre, pf int
	}{
		{"over 100 percent", 10001, 60, 90},
		{"negative percent", -1, 60, 90},
		{"negative preserve", 1000, -1, 90},
		{"negative perfect", 1000, 60, -1},
	} {
		_, err := pool.Exec(context.Background(),
			`INSERT INTO holdback_province_rules (province, holdback_pct_bps, preserve_days, perfect_days) VALUES ('ZZ', $1, $2, $3)`,
			bad.pct, bad.pre, bad.pf)
		if err == nil {
			_, _ = pool.Exec(context.Background(), `DELETE FROM holdback_province_rules WHERE province = 'ZZ'`)
			t.Fatalf("%s: the table took it", bad.name)
		}
	}
}

func TestIntegration_RT1_DealerRuleIsOneRowStartingAtOntario(t *testing.T) {
	pool := intPool(t)
	var n int
	var province string
	var pct *int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*), min(province), min(holdback_pct_bps) FROM holdback_dealer_rule`).Scan(&n, &province, &pct); err != nil {
		t.Fatalf("read the dealer rule: %v", err)
	}
	if n != 1 || province != "ON" || pct != nil {
		t.Fatalf("dealer rule = %d rows, province %q, override %v; want one row, ON, no override", n, province, pct)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO holdback_dealer_rule (singleton, province) VALUES (FALSE, 'ON')`); err == nil {
		t.Fatal("a second dealer rule row was taken")
	}
}
