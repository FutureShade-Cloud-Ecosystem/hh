package inventory

import (
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/dealer"
)

// The featured-product toggle reads the dealer's own configured name.
func TestFeaturedFacetLabel_ReadsTheConfiguredDealerName(t *testing.T) {
	t.Cleanup(func() { dealer.Set("") })
	dealer.Set("Gable Landscape Supply")
	if got := featuredFacetLabel(); got != "Gable Landscape Supply favourite" {
		t.Fatalf("label = %q, want the configured dealer name and favourite", got)
	}
}

// With no dealer name set it is a neutral word, never another dealer's name.
func TestFeaturedFacetLabel_NeutralWhenNoDealerName(t *testing.T) {
	dealer.Set("")
	got := featuredFacetLabel()
	if strings.Contains(strings.ToLower(got), "dibbits") || got != "Dealer favourite" {
		t.Fatalf("label = %q, want the neutral label", got)
	}
}
