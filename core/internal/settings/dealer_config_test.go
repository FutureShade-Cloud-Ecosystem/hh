package settings

import (
	"context"
	"encoding/json"
	"testing"
)

func raw(m map[string]string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range m {
		out[k] = json.RawMessage(v)
	}
	return out
}

// A dealer with no configuration rows is the Ontario dealer, exactly as before the seam existed.
func TestDealerConfig_NoRowsIsOntario(t *testing.T) {
	got := parseDealerConfig(nil, nil)
	want := DealerConfig{Currency: "CAD", TaxRegion: "ON", BusinessZone: "America/Toronto", HSTRateBps: 1300, Country: "CA"}
	if got != want {
		t.Fatalf("no rows: got %+v want %+v", got, want)
	}
	if got != DefaultDealerConfig() {
		t.Fatalf("the parse default and DefaultDealerConfig disagree")
	}
}

func TestDealerConfig_ReadsEveryKey(t *testing.T) {
	got := parseDealerConfig(raw(map[string]string{
		"currency":      `"USD"`,
		"tax_region":    `"CA"`,
		"business_zone": `"America/Los_Angeles"`,
		"hst_rate_bps":  `775`,
		"country":       `"US"`,
	}), nil)
	want := DealerConfig{Currency: "USD", TaxRegion: "CA", BusinessZone: "America/Los_Angeles", HSTRateBps: 775, Country: "US"}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

// A half configured dealer: each absent key answers its own Ontario default, a present key stands.
func TestDealerConfig_HalfConfiguredKeepsPerKeyDefaults(t *testing.T) {
	got := parseDealerConfig(raw(map[string]string{"tax_region": `"CA"`}), nil)
	if got.TaxRegion != "CA" || got.Currency != "CAD" || got.BusinessZone != "America/Toronto" || got.HSTRateBps != 1300 {
		t.Fatalf("half configured: %+v", got)
	}
}

// A malformed stored value never reaches a consumer: the key's default stands and the reader says so.
func TestDealerConfig_MalformedValuesFallBackAndSay(t *testing.T) {
	var said []string
	got := parseDealerConfig(raw(map[string]string{
		"currency":      `"dollars"`,
		"tax_region":    `"not a region"`,
		"business_zone": `"Mars/Olympus"`,
		"hst_rate_bps":  `"abc"`,
		"country":       `"Canada"`,
	}), func(key string) { said = append(said, key) })
	if got != DefaultDealerConfig() {
		t.Fatalf("malformed values must fall back to the defaults, got %+v", got)
	}
	if len(said) != 5 {
		t.Fatalf("each malformed key must be reported, got %v", said)
	}
}

func TestDealerConfig_HSTRateAcceptsTheBareAndTheQuotedForm(t *testing.T) {
	for _, v := range []string{`775`, `"775"`} {
		got := parseDealerConfig(raw(map[string]string{"hst_rate_bps": v}), nil)
		if got.HSTRateBps != 775 {
			t.Fatalf("%s: got %d", v, got.HSTRateBps)
		}
	}
	// Out of range is malformed, never clamped.
	if got := parseDealerConfig(raw(map[string]string{"hst_rate_bps": `20000`}), nil); got.HSTRateBps != 1300 {
		t.Fatalf("out of range must fall back, got %d", got.HSTRateBps)
	}
}

func TestDealerConfig_ValidateRefusesInWords(t *testing.T) {
	ok := DealerConfig{Currency: "USD", TaxRegion: "CA", BusinessZone: "America/Los_Angeles", HSTRateBps: 775, Country: "US"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid config refused: %v", err)
	}
	for name, mut := range map[string]func(*DealerConfig){
		"currency": func(c *DealerConfig) { c.Currency = "usd" },
		"region":   func(c *DealerConfig) { c.TaxRegion = "" },
		"zone":     func(c *DealerConfig) { c.BusinessZone = "Nowhere/Land" },
		"rate":     func(c *DealerConfig) { c.HSTRateBps = -1 },
	} {
		c := ok
		mut(&c)
		if err := c.Validate(); err == nil {
			t.Fatalf("%s: a bad value was accepted", name)
		}
	}
}

// Two readers in one process answer their own databases: nothing is held at package level.
func TestDealerConfig_ReaderWithNoPoolAnswersTheDefault(t *testing.T) {
	r := NewDealerConfigReader(nil)
	got, err := r.DealerConfig(context.Background())
	if err != nil || got != DefaultDealerConfig() {
		t.Fatalf("nil pool: %+v %v", got, err)
	}
}

// The country defaults to Canada exactly, and a write of a malformed country is refused.
func TestDealerConfig_CountryDefaultsToCanadaAndValidates(t *testing.T) {
	if got := parseDealerConfig(raw(map[string]string{"tax_region": `"CA"`}), nil); got.Country != "CA" {
		t.Fatalf("an absent country must be CA, got %q", got.Country)
	}
	bad := DefaultDealerConfig()
	bad.Country = "USA"
	if bad.Validate() == nil {
		t.Fatal("a three letter country must be refused")
	}
	bad.Country = ""
	if bad.Validate() == nil {
		t.Fatal("a blank country must be refused on write")
	}
}
