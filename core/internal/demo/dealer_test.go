//go:build demo

package demo

import (
	"strings"
	"testing"
)

// The demo's dealer is Gable Landscape Supply (GLS-TENANT-r3 and the brand
// kit): the name, the navy of the chrome, a support line at the reserved
// invalid domain and a number in the 555 0100 block, and no other dealer's
// name in any field.
func TestDealerConfigIsGableLandscapeSupply(t *testing.T) {
	c := DealerConfig()
	if c.DealerName != "Gable Landscape Supply" || c.Id != "gable" {
		t.Fatalf("dealer = %q / %q", c.Id, c.DealerName)
	}
	if c.PrimaryColor != "#0B2338" {
		t.Fatalf("primary colour = %q, want the kit's navy", c.PrimaryColor)
	}
	if !strings.HasSuffix(c.SupportEmail, "@hh-demo.invalid") {
		t.Fatalf("support email = %q, want the reserved demo domain", c.SupportEmail)
	}
	if !strings.Contains(c.SupportPhone, "555-01") {
		t.Fatalf("support phone = %q, want the 555 0100 block", c.SupportPhone)
	}
	for _, v := range []string{c.Id, c.DealerName, c.LogoUrl, c.PrimaryColor, c.SupportEmail, c.SupportPhone} {
		if strings.Contains(strings.ToLower(v), "dibbits") {
			t.Fatalf("the demo dealer names another dealer: %+v", c)
		}
		if strings.ContainsAny(v, "!") || strings.ContainsRune(v, '\u2014') || strings.ContainsRune(v, '\u2013') {
			t.Fatalf("field %q breaks the kit's voice or the house rules", v)
		}
	}
}
