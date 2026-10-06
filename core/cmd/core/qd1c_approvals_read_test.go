package main

// qd1c_approvals_read_test.go is the approvals read doors' margin fence
// (P2-3, round 3): a floor request's detail carries the resulting margin and
// the floor in basis points, which with the discount and the product names
// the cost. The sync pull and the staff list are fenced by org and branch
// alone, so a role off the cost list read the figures there. Driven by
// request through the registered surface, every guard real: a caller sees a
// floor request only when it could decide it or filed it itself.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// qd1cMarginDetail is the detail the quote desk files, in its own words.
const qd1cMarginDetail = "resulting margin 1666 bps against the 2000 bps floor"

// qd1cPlantMargin files one floor request whose detail carries the margin
// figures, as the quote desk does, and returns its id.
func qd1cPlantMargin(t *testing.T, q *qd1bFixture, requester, suffix string) string {
	t.Helper()
	id := qd1bPlantFloor(t, q, requester, testkit.BranchNorthAID, suffix)
	if _, err := q.pool.Exec(t.Context(),
		`UPDATE permission_approval_requests SET detail = $2 WHERE id = $1`, id, qd1cMarginDetail); err != nil {
		t.Fatalf("plant the margin detail: %v", err)
	}
	return id
}

// qd1cSees reports whether the body of a read names the request, and
// whether it carries the margin figures anywhere.
func qd1cSees(t *testing.T, q *qd1bFixture, path, sub, id string) (listed bool, figures bool) {
	t.Helper()
	code, body := qd1bCall(t, q, http.MethodGet, path, sub, nil)
	if code == http.StatusForbidden {
		return false, false
	}
	if code != http.StatusOK {
		t.Fatalf("GET %s as %s: %d %s", path, sub, code, body)
	}
	var probe any
	if err := json.Unmarshal([]byte(body), &probe); err != nil {
		t.Fatalf("decode %s: %v (%s)", path, err, body)
	}
	return strings.Contains(body, id), strings.Contains(body, "bps against the")
}

func TestQD1C_ApprovalsSyncPullHidesFloorRequestsFromRolesThatCannotDecideThem(t *testing.T) {
	q := newQD1BFixture(t)
	clerk := qd1bUserID(t, q.pool, testkit.SubInsideSales)
	id := qd1cPlantMargin(t, q, clerk, "000000000d01")
	const pull = "/api/v1/sync/approvals?since=0&limit=500"

	for _, sub := range []string{xrSubHR, xrSubYardTeam, xrSubBranchManager, xrSubProcurementManager} {
		if listed, figures := qd1cSees(t, q, pull, sub, id); listed || figures {
			t.Errorf("%s must not read the floor request or its figures through the sync pull (listed %v figures %v)", sub, listed, figures)
		}
	}
	for _, sub := range []string{testkit.SubGeneralMgr, xrSubFinancialController, testkit.SubSalesManager, testkit.SubInsideSales} {
		if listed, figures := qd1cSees(t, q, pull, sub, id); !listed || !figures {
			t.Errorf("%s must still read the floor request through the sync pull (listed %v figures %v)", sub, listed, figures)
		}
	}
}

func TestQD1C_ApprovalsListHidesAnotherRequestersFloorRequestFromWorkRoles(t *testing.T) {
	q := newQD1BFixture(t)
	clerk := qd1bUserID(t, q.pool, testkit.SubInsideSales)
	id := qd1cPlantMargin(t, q, clerk, "000000000d02")
	const list = "/api/v1/staff/approvals"

	// The branch manager holds the Quote Desk at Work, so a route that
	// admits it must still not hand it the clerk's request.
	if listed, figures := qd1cSees(t, q, list, xrSubBranchManager, id); listed || figures {
		t.Errorf("a role that cannot decide and did not file must not read the floor request in the list (listed %v figures %v)", listed, figures)
	}
	// Another seller of the same branch: the Sales manager's own request is
	// not the clerk's to read, though the route admits the clerk.
	manager := qd1bUserID(t, q.pool, testkit.SubSalesManager)
	mine := qd1cPlantMargin(t, q, manager, "000000000d03")
	// (The clerk's own card sits in the same body, so only the listing is read.)
	if listed, _ := qd1cSees(t, q, list, testkit.SubInsideSales, mine); listed {
		t.Errorf("a seller must not read another requester's floor request in the list")
	}
	for _, sub := range []string{testkit.SubGeneralMgr, xrSubFinancialController, testkit.SubSalesManager, testkit.SubInsideSales} {
		if listed, figures := qd1cSees(t, q, list, sub, id); !listed || !figures {
			t.Errorf("%s must still read the floor request in the list (listed %v figures %v)", sub, listed, figures)
		}
	}
}
