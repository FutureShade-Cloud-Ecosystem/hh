package main

// The vendor warranty credit agreement routes' access decision, pinned against the generated census
// (routes.gen.go, regenerated from core/api/openapi.yaml), the threshold pin's twin. Walk change 6:
// the agreements are READ by manager-home (the Vendors and credits board) and dealer-admin (the
// Warranty page's own card), and WRITTEN by dealer-admin alone. The read rides the staff bearer, the
// write the admin gate; the guard equality itself is the xrole suite's assertion, this file pins
// WHICH surfaces the matrix names, so an annotation dropped or widened in a regeneration fails here.

import (
	"slices"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

func TestWarrantyVendorAgreementRoutesCarryTheAccessMatrixSurfaces(t *testing.T) {
	want := map[string]struct {
		xrole    string
		surfaces []string
		level    string
	}{
		"warrantyListVendorAgreements":  {xrole: "requireStaff", surfaces: []string{"manager-home", "dealer-admin"}, level: "read"},
		"warrantyUpsertVendorAgreement": {xrole: "requireAdmin", surfaces: []string{"dealer-admin"}, level: "admin"},
	}
	found := map[string]apigen.Route{}
	for _, route := range apigen.Routes {
		if _, ok := want[route.OperationID]; ok {
			found[route.OperationID] = route
		}
	}
	for op, w := range want {
		route, ok := found[op]
		if !ok {
			t.Fatalf("%s is not in the census; the route or its annotation is gone", op)
		}
		if route.XRole != w.xrole {
			t.Fatalf("%s carries x-role %q, want %q", op, route.XRole, w.xrole)
		}
		got := append([]string(nil), route.Surfaces...)
		slices.Sort(got)
		wantSurfaces := append([]string(nil), w.surfaces...)
		slices.Sort(wantSurfaces)
		if !slices.Equal(got, wantSurfaces) {
			t.Fatalf("%s carries surfaces %v, want %v: the read is named by manager-home and dealer-admin, the write by dealer-admin alone", op, route.Surfaces, w.surfaces)
		}
		if route.Level != w.level {
			t.Fatalf("%s carries level %q, want %q", op, route.Level, w.level)
		}
	}
}
