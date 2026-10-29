package purchasing

import (
	"slices"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

func findServedRoute(method, path string) *apigen.Route {
	for i := range apigen.Routes {
		if apigen.Routes[i].Method == method && apigen.Routes[i].Path == path {
			return &apigen.Routes[i]
		}
	}
	return nil
}

func TestAPAgingRoute_IsServedAndManagerGated(t *testing.T) {
	const path = "/api/v1/purchasing/ap-aging"
	found := findServedRoute("GET", path)
	if found == nil {
		t.Fatalf("W2-9a sub5 red: no route GET %s in the served route table (core/internal/platform/apigen)", path)
	}
	if found.OperationID != "purchasingGetAPAging" {
		t.Fatalf("the AP aging route's operationId is %q, want purchasingGetAPAging", found.OperationID)
	}
	if found.Tag != "staff-purchasing" {
		t.Fatalf("the AP aging route's tag is %q, want staff-purchasing", found.Tag)
	}
	if found.XRole != "requireManager" {
		t.Fatalf("the AP aging route is gated %q, want requireManager", found.XRole)
	}
	if found.Bearer != "staff" {
		t.Fatalf("the AP aging route's bearer is %q, want staff", found.Bearer)
	}
	if !found.Addition {
		t.Fatalf("the AP aging route must carry x-addition (no ERP source route)")
	}
	if found.Level != "read" {
		t.Fatalf("the AP aging route's x-access level is %q, want read", found.Level)
	}
	if !slices.Contains(found.Surfaces, "money-desk") {
		t.Fatalf("the AP aging route's x-access surfaces %v do not include money-desk", found.Surfaces)
	}
}
