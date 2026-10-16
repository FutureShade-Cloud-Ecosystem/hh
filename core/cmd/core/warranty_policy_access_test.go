package main

// The warranty policy routes' access decision, pinned against the generated census (routes.gen.go,
// regenerated from core/api/openapi.yaml). Walk change 6: the threshold is READ by manager-home (the
// Warranty board's tiles), counter-till (the return line's sentence) and dealer-admin (the Warranty
// page's own field), and WRITTEN by dealer-admin alone. The read rides the staff bearer, the write
// the admin gate; the guard equality itself is the xrole suite's assertion, this file pins WHICH
// surfaces the matrix names, so an annotation dropped or widened in a regeneration fails here.

import (
	"slices"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

func TestWarrantyPolicyRoutesCarryTheAccessMatrixSurfaces(t *testing.T) {
	want := map[string]struct {
		xrole    string
		surfaces []string
		level    string
	}{
		"warrantyGetPolicy": {xrole: "requireStaff", surfaces: []string{"manager-home", "counter-till", "dealer-admin"}, level: "read"},
		"warrantyPutPolicy": {xrole: "requireAdmin", surfaces: []string{"dealer-admin"}, level: "admin"},
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
			t.Fatalf("%s carries surfaces %v, want %v: the read is named by manager-home, counter-till and dealer-admin, the write by dealer-admin alone", op, route.Surfaces, w.surfaces)
		}
		if route.Level != w.level {
			t.Fatalf("%s carries level %q, want %q", op, route.Level, w.level)
		}
	}
}
