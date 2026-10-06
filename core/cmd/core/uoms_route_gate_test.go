package main

// uoms_route_gate_test.go is the route level leg for the staff read of
// the unit catalogue (HH-LOAD-1 LD-3 round 2): GET /api/v1/uoms carries
// x-role requireStaff with the dealer-admin surface at read, and this
// test proves the gate the caller meets, not the census word: a staff
// caller whose seat holds no dealer-admin read is refused in the RBAC
// 403 family, and a staff caller whose seat does hold it reaches the
// handler.

import (
	"net/http"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// TestUomsReadGatesTheDealerAdminSurface: inside_sales carries no
// dealer-admin cell at all, so its staff bearer is held off the
// catalogue read; the general manager's dealer-admin cell is admin, so
// the read serves. The refused leg is the load bearing one: with the
// gate absent it would answer the handler instead of the RBAC family.
func TestUomsReadGatesTheDealerAdminSurface(t *testing.T) {
	fx := newXRoleFixture(t)
	code, body := rgProbe(t, fx, http.MethodGet, "/api/v1/uoms", rgHeaders(t, fx, testkit.SubInsideSales))
	rgRefused(t, "inside sales is held off the unit catalogue read", code, body)
	code, body = rgProbe(t, fx, http.MethodGet, "/api/v1/uoms", rgHeaders(t, fx, testkit.SubGeneralMgr))
	rgAdmitted(t, "the general manager reads the unit catalogue", code, body)
}
