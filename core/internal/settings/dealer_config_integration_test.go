//go:build integration

package settings

// The dealer configuration against a real migrated database: the seeded dealer reads as Ontario,
// a write is live at the next read, and a refused write changes nothing. Every test restores the
// four rows it touches, per this package's convention.

import (
	"context"
	"testing"
)

func restoreDealerConfig(t *testing.T, r *DealerConfigReader) {
	t.Helper()
	ctx := context.Background()
	type row struct {
		key string
		val []byte
		has bool
	}
	var before []row
	for _, k := range []string{KeyCurrency, KeyTaxRegion, KeyBusinessZone, KeyHSTRateBps, KeyCountry} {
		var v []byte
		err := r.pool.QueryRow(ctx, `SELECT value FROM system_settings WHERE key = $1`, k).Scan(&v)
		before = append(before, row{key: k, val: v, has: err == nil})
	}
	t.Cleanup(func() {
		for _, b := range before {
			if b.has {
				_, _ = r.pool.Exec(ctx, `UPDATE system_settings SET value = $2::jsonb WHERE key = $1`, b.key, string(b.val))
			} else {
				_, _ = r.pool.Exec(ctx, `DELETE FROM system_settings WHERE key = $1`, b.key)
			}
		}
	})
}

func TestIntegration_DealerConfig_SeededDealerIsOntarioAndAWriteIsLive(t *testing.T) {
	r := NewDealerConfigReader(intPool(t))
	restoreDealerConfig(t, r)
	ctx := context.Background()

	// Remove the one key the migration never planted: the seeded dealer is Ontario.
	if _, err := r.pool.Exec(ctx, `DELETE FROM system_settings WHERE key = 'business_zone'`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE system_settings SET value = '"CAD"' WHERE key = 'currency'`); err != nil {
		t.Fatal(err)
	}
	got, err := r.DealerConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.TaxRegion != "ON" || got.Currency != "CAD" || got.BusinessZone != "America/Toronto" || got.HSTRateBps != 1300 || got.Country != "CA" {
		t.Fatalf("seeded dealer: %+v", got)
	}

	want := DealerConfig{Currency: "USD", TaxRegion: "CA", BusinessZone: "America/Los_Angeles", HSTRateBps: 775, Country: "US"}
	if err := r.WriteDealerConfig(ctx, want); err != nil {
		t.Fatal(err)
	}
	if got, _ = r.DealerConfig(ctx); got != want {
		t.Fatalf("after write: %+v want %+v", got, want)
	}

	// A refused write changes nothing.
	bad := want
	bad.BusinessZone = "Nowhere/Land"
	if err := r.WriteDealerConfig(ctx, bad); err == nil {
		t.Fatal("a bad zone was written")
	}
	if got, _ = r.DealerConfig(ctx); got != want {
		t.Fatalf("a refused write moved the config: %+v", got)
	}
}

// The currency read rides the caller's transaction: the card rail's sale transaction holds a
// connection while it charges, and a pool read there can deadlock a full pool.
func TestIntegration_DealerCurrencyTx_ReadsOnTheCallersTransaction(t *testing.T) {
	r := NewDealerConfigReader(intPool(t))
	restoreDealerConfig(t, r)
	ctx := context.Background()
	want := DealerConfig{Currency: "USD", TaxRegion: "CA", BusinessZone: "America/Los_Angeles", HSTRateBps: 775, Country: "US"}
	if err := r.WriteDealerConfig(ctx, want); err != nil {
		t.Fatal(err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// An uncommitted change on this transaction is what its own read sees (the pool would not).
	if _, err := tx.Exec(ctx, `UPDATE system_settings SET value = '"EUR"' WHERE key = 'currency'`); err != nil {
		t.Fatal(err)
	}
	got, err := r.DealerConfigTx(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Currency != "EUR" {
		t.Fatalf("tx read currency = %q, want the transaction's own EUR", got.Currency)
	}
	if pooled, _ := r.DealerConfig(ctx); pooled.Currency != "USD" {
		t.Fatalf("pool read currency = %q, want the committed USD", pooled.Currency)
	}
}

// A damaged tax_region row refuses at the reader (so at the engine); it never reads as Ontario.
func TestIntegration_DealerConfig_MalformedTaxRegionRefuses(t *testing.T) {
	r := NewDealerConfigReader(intPool(t))
	restoreDealerConfig(t, r)
	ctx := context.Background()
	if _, err := r.pool.Exec(ctx, `UPDATE system_settings SET value = '"ca"' WHERE key = 'tax_region'`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.DealerConfig(ctx); err == nil {
		t.Fatal("a malformed tax region was read as a configuration")
	}
}

func TestIntegration_DealerConfig_BusinessZoneIsServedAndALiveWrite(t *testing.T) {
	r := NewDealerConfigReader(intPool(t))
	restoreDealerConfig(t, r)
	ctx := context.Background()
	if _, err := r.pool.Exec(ctx, `DELETE FROM system_settings WHERE key = 'business_zone'`); err != nil {
		t.Fatal(err)
	}
	if z, err := r.BusinessZone(ctx); err != nil || z != "America/Toronto" {
		t.Fatalf("an absent zone is the Ontario dealer's: %q %v", z, err)
	}
	pacific := DefaultDealerConfig()
	pacific.BusinessZone = "America/Los_Angeles"
	if err := r.WriteDealerConfig(ctx, pacific); err != nil {
		t.Fatal(err)
	}
	if z, err := r.BusinessZone(ctx); err != nil || z != "America/Los_Angeles" {
		t.Fatalf("a written zone is live at the next read: %q %v", z, err)
	}
}
