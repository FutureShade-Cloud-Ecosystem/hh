package localization

import "testing"

func TestProvinceCode(t *testing.T) {
	for in, want := range map[string]string{
		"Ontario": "ON", " ont. ": "ON", "ontario": "ON", "on": "ON", "ON": "ON",
		"Quebec": "QC", "Québec": "QC", "British Columbia": "BC", "P.E.I.": "PE", "Newfoundland and Labrador": "NL",
	} {
		if got, ok := ProvinceCode(in); !ok || got != want {
			t.Errorf("ProvinceCode(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "ZA", "Texas", "  "} {
		if got, ok := ProvinceCode(in); ok {
			t.Errorf("ProvinceCode(%q) = %q; want no match", in, got)
		}
	}
}
