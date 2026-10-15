package finstmt

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

type zwWords struct{}

func (zwWords) TaxLabel(context.Context) (string, error) { return "Sales tax", nil }

type zwZone struct{ z houseday.Zone }

func (s zwZone) BusinessZone(context.Context) (houseday.Zone, error) { return s.z, nil }

// The register's zone and its printed invoice's tax word are two options on one handler: binding the
// second must not drop the first, in either order.
func TestHandler_RegisterZoneAndTaxWordHoldTogether(t *testing.T) {
	la, err := houseday.NewZone("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	for name, h := range map[string]*Handler{
		"zone then word": NewHandler(nil, nil).WithZone(zwZone{z: la}).WithRegisterTaxLabel(zwWords{}),
		"word then zone": NewHandler(nil, nil).WithRegisterTaxLabel(zwWords{}).WithZone(zwZone{z: la}),
	} {
		if h.zone == nil || h.registerTaxWords == nil {
			t.Fatalf("%s: zone bound %v, words bound %v", name, h.zone != nil, h.registerTaxWords != nil)
		}
		if got, _ := h.registerTaxWords.TaxLabel(context.Background()); got != "Sales tax" {
			t.Fatalf("%s: word %q", name, got)
		}
		if z, _ := h.zone.BusinessZone(context.Background()); z.Name() != "America/Los_Angeles" {
			t.Fatalf("%s: zone %q", name, z.Name())
		}
	}
}
