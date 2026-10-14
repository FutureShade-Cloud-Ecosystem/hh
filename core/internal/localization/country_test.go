package localization

import (
	"strings"
	"testing"
)

func TestCountryOntarioDealerIsUnchanged(t *testing.T) {
	// A blank country and CA both mean the Canadian rules, exactly as before.
	for _, country := range []string{"", "CA", "ca"} {
		got, err := NormalizeBillingAddressFor(country, map[string]any{"province": "on", "postal": "k8v5p7"})
		if err != nil {
			t.Fatalf("country %q: %v", country, err)
		}
		if got["province"] != "ON" || got["postal"] != "K8V 5P7" || got["country"] != "CA" {
			t.Errorf("country %q: got %v", country, got)
		}
		_, err = NormalizeBillingAddressFor(country, map[string]any{"province": "CA", "postal": "92101"})
		if err == nil || !strings.Contains(err.Error(), "invalid Canadian postal code") && !strings.Contains(err.Error(), "invalid province") {
			t.Errorf("country %q: a US address must be refused in Canadian words, got %v", country, err)
		}
	}
}

func TestCountryUSDealerAcceptsSanDiegoAndRefusesCanadianPostal(t *testing.T) {
	got, err := NormalizeBillingAddressFor("US", map[string]any{"line1": "1 Harbor Dr", "city": "San Diego", "province": "ca", "postal": "92101"})
	if err != nil {
		t.Fatal(err)
	}
	if got["province"] != "CA" || got["postal"] != "92101" || got["country"] != "US" {
		t.Errorf("got %v", got)
	}
	_, err = NormalizeBillingAddressFor("US", map[string]any{"province": "CA", "postal": "K8V 5P7"})
	if err == nil || !strings.Contains(err.Error(), "ZIP code") {
		t.Fatalf("a Canadian postal code must be refused in ZIP words, got %v", err)
	}
	if !isBadRequest(err) {
		t.Errorf("want a bad request, got %T", err)
	}
}

func TestUSZipForms(t *testing.T) {
	ok := map[string]string{"92101": "92101", " 92101 ": "92101", "92101-1234": "92101-1234", "921011234": "92101-1234", "92101 1234": "92101-1234"}
	for in, want := range ok {
		got, err := NormalizeZIP(in)
		if err != nil || got != want {
			t.Errorf("NormalizeZIP(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "9210", "921012", "92101-12", "ABCDE", "K8V 5P7"} {
		if _, err := NormalizeZIP(in); err == nil {
			t.Errorf("NormalizeZIP(%q) must refuse", in)
		}
	}
}

func TestUSStates(t *testing.T) {
	for _, s := range []string{"CA", "ny", "DC", "TX", "AK", "HI"} {
		if _, err := ValidateState(s); err != nil {
			t.Errorf("ValidateState(%q): %v", s, err)
		}
	}
	for _, s := range []string{"", "ON", "XX", "California"} {
		if _, err := ValidateState(s); err == nil {
			t.Errorf("ValidateState(%q) must refuse", s)
		}
	}
	n := 0
	for range usStates {
		n++
	}
	if n != 51 {
		t.Errorf("50 states plus DC, got %d", n)
	}
}

func TestUSDealerRefusesOtherCountryAndUnknownDealerCountry(t *testing.T) {
	if _, err := NormalizeBillingAddressFor("US", map[string]any{"province": "CA", "postal": "92101", "country": "CA"}); err == nil {
		t.Error("a US dealer must refuse country CA")
	}
	if _, err := NormalizeBillingAddressFor("GB", map[string]any{"province": "X", "postal": "Y"}); err == nil {
		t.Error("an unsupported dealer country must refuse in words")
	}
}
