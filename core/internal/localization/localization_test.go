package localization

import (
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

func isBadRequest(err error) bool {
	var bre httpx.BadRequestError
	return errors.As(err, &bre)
}

func TestNormalizePostal_Valid(t *testing.T) {
	cases := map[string]string{
		"K8V 5P7":  "K8V 5P7", // already canonical
		"k8v5p7":   "K8V 5P7", // lowercase, no space
		"K8V5P7":   "K8V 5P7", // uppercase, no space
		"k8v 5p7":  "K8V 5P7", // lowercase, spaced
		" K8V5P7 ": "K8V 5P7", // padded
		"k7p2y7":   "K7P 2Y7", // Kingston branch postal
	}
	for in, want := range cases {
		got, err := NormalizePostal(in)
		if err != nil {
			t.Errorf("NormalizePostal(%q) errored: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("NormalizePostal(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizePostal_Invalid(t *testing.T) {
	bad := []string{
		"12345",      // US ZIP
		"12345-6789", // US ZIP+4
		"",           // empty
		"K8V 5P",     // too short
		"K8V 5P77",   // too long
		"D8V 5P7",    // leading D is disallowed
		"K8V 5PZ",    // trailing letter where a digit is required
		"garbage",    // not a postal at all
		"K8-5P7",     // wrong shape
	}
	for _, in := range bad {
		_, err := NormalizePostal(in)
		if err == nil {
			t.Errorf("NormalizePostal(%q) should have errored", in)
			continue
		}
		if !isBadRequest(err) {
			t.Errorf("NormalizePostal(%q) error is not a BadRequestError: %v", in, err)
		}
	}
}

func TestValidateProvince(t *testing.T) {
	for _, ok := range []string{"ON", "on", " qc ", "BC", "NL", "NU"} {
		if _, err := ValidateProvince(ok); err != nil {
			t.Errorf("ValidateProvince(%q) should pass: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "XX", "CA", "NY", "ONT", "Ontario"} {
		_, err := ValidateProvince(bad)
		if err == nil {
			t.Errorf("ValidateProvince(%q) should fail", bad)
			continue
		}
		if !isBadRequest(err) {
			t.Errorf("ValidateProvince(%q) error is not a BadRequestError: %v", bad, err)
		}
	}
	// canonical uppercase returned
	got, _ := ValidateProvince("on")
	if got != "ON" {
		t.Errorf("ValidateProvince(\"on\") = %q, want ON", got)
	}
}

func TestNormalizeAddress(t *testing.T) {
	got, err := NormalizeAddress(Address{
		Line1: "132 County Road 40", City: "Trenton", Province: "on", Postal: "k8v5p7",
	})
	if err != nil {
		t.Fatalf("NormalizeAddress: %v", err)
	}
	if got.Province != "ON" || got.Postal != "K8V 5P7" || got.Country != "CA" {
		t.Fatalf("normalized = %+v, want ON / K8V 5P7 / CA", got)
	}

	// A US-shaped address (bad postal) is rejected.
	if _, err := NormalizeAddress(Address{Province: "ON", Postal: "90210"}); err == nil {
		t.Fatal("US ZIP should be rejected")
	}
	// A non-CA country is rejected.
	if _, err := NormalizeAddress(Address{Province: "ON", Postal: "K8V 5P7", Country: "US"}); err == nil {
		t.Fatal("non-CA country should be rejected")
	}
}

func TestNormalizeBillingAddress(t *testing.T) {
	// Empty address passes through untouched (billing_address is optional).
	if out, err := NormalizeBillingAddress(map[string]any{}); err != nil || len(out) != 0 {
		t.Fatalf("empty billing address should pass through: out=%v err=%v", out, err)
	}
	// A free-form address with neither province nor postal is not asserted on.
	free := map[string]any{"line1": "123 Main"}
	if out, err := NormalizeBillingAddress(free); err != nil || out["line1"] != "123 Main" {
		t.Fatalf("free-form address should pass through: out=%v err=%v", out, err)
	}
	// A supplied province+postal is normalized.
	out, err := NormalizeBillingAddress(map[string]any{"province": "on", "postal": "k8v5p7", "city": "Trenton"})
	if err != nil {
		t.Fatalf("valid billing address: %v", err)
	}
	if out["province"] != "ON" || out["postal"] != "K8V 5P7" {
		t.Fatalf("normalized billing = %+v", out)
	}
	// A supplied-but-invalid province is a 400.
	if _, err := NormalizeBillingAddress(map[string]any{"province": "XX", "postal": "K8V 5P7"}); err == nil {
		t.Fatal("invalid province in billing address should be rejected")
	}
	// A postal present without a province still validates the postal (and requires a valid province).
	if _, err := NormalizeBillingAddress(map[string]any{"postal": "12345"}); err == nil {
		t.Fatal("US ZIP in billing address should be rejected")
	}
}
