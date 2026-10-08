//go:build integration

// qd1f_floor_approvers_integration_test.go, the approvers a below floor
// refusal names (P2-B, round 4). The refusal named the branch's Sales manager
// beside the dealer wide approvers for every caller, but row 743 sends a
// request filed by a Branch manager or a Sales manager (the Yard manager
// once, too, while it held the desk; decision log row 1156 removed its Quote
// Desk, so it files nothing now and is refused outright) to leadership
// alone, so for those filers the Sales manager was named and refused. The
// named set must be exactly who the decide rule would let decide that
// caller's request, so the test asks the decide rule itself
// (identity.CanApproveFloor over the configured set) for every role and
// compares, under the setting on and off.
package quote

import (
	"errors"
	"sort"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// qd1fDecideSet is the set of business roles the decide rule lets decide a
// margin request filed by filerRole in branch, over the configured floor set
// built as the composition root builds it.
func qd1fDecideSet(t *testing.T, cfg *roleconfig.Config, filerRole, branch string) []string {
	t.Helper()
	set := identity.FloorApprovers{}
	wide, err := cfg.ExceptionApprovers(roleconfig.ExceptionFloorQuote)
	if err != nil {
		t.Fatalf("floor approvers: %v", err)
	}
	for _, r := range wide {
		if erp, ok := identity.ERPCodeFor(r); ok {
			set.DealerWide = append(set.DealerWide, erp)
		}
	}
	for _, r := range cfg.QuoteFloorBranchApprovers() {
		if erp, ok := identity.ERPCodeFor(r); ok {
			set.OwnBranch = append(set.OwnBranch, erp)
		}
	}
	var out []string
	for _, def := range identity.Roles() {
		c := identity.StaffContext{
			UserID: "decider", Status: identity.StatusActive, RoleCode: def.Code,
			Tier: def.Tier, Scope: def.Scope, Branches: map[string]bool{branch: true},
		}
		if identity.CanApproveFloor(c, identity.PolicyMargin, branch, filerRole, set) {
			out = append(out, def.Code)
		}
	}
	sort.Strings(out)
	return out
}

func TestQD1F_RefusalNamesExactlyWhoCouldDecideTheFilersRequest(t *testing.T) {
	pool := intPool(t)
	now := time.Now().UTC()
	configs := []struct {
		name string
		cfg  *roleconfig.Config
	}{{"on", qd1Cfg}, {"off", qd1CfgNoManagerClearing(t)}}
	for _, c := range configs {
		for _, role := range []string{"INSIDE_SALES", "OUTSIDE_SALES", "BRANCH_MANAGER", "SALES_MANAGER"} {
			filer := qd1User(t, pool, "f743-"+c.name+"-"+role)
			_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", filer, true)
			svc := qd1SvcFloor(t, pool, now, qd1Arbiter{marginBps: 500, floorBps: 1200}, c.cfg)
			ctx := qd1WithRole(qCtxAs(intStaff(intTrenton)), role, intTrenton, filer)

			_, err := svc.SetLineDiscount(ctx, quoteID, 1, 25)
			var ref *FloorRefusalError
			if !errors.As(err, &ref) {
				// A manager clearing its own price (setting on) is not refused.
				clears := c.name == "on" && (role == "BRANCH_MANAGER" || role == "SALES_MANAGER")
				if !clears {
					t.Errorf("clearing %s, %s: want the typed refusal, got %v", c.name, role, err)
				}
				continue
			}
			got := append([]string(nil), ref.Approvers...)
			sort.Strings(got)
			want := qd1fDecideSet(t, c.cfg, role, intTrenton)
			if len(got) != len(want) {
				t.Errorf("clearing %s, %s: the refusal names %v, the decide rule lets %v decide", c.name, role, got, want)
				continue
			}
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("clearing %s, %s: the refusal names %v, the decide rule lets %v decide", c.name, role, got, want)
					break
				}
			}
			// Leadership-bound filers name no branch approver.
			if !identity.FloorBranchFiler(role) {
				for _, n := range got {
					if n == "SALES_MANAGER" {
						t.Errorf("clearing %s, %s: a leadership-bound request names the Sales manager", c.name, role)
					}
				}
			}
		}
	}

	// The Yard manager held the desk once and filed from it; row 1156 removed
	// its Quote Desk, so it is refused outright, before the floor logic can
	// name any decider, and its line is untouched.
	ym := qd1User(t, pool, "ym")
	_, ymQuote, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", ym, true)
	ymSvc := qd1SvcFloor(t, pool, now, qd1Arbiter{marginBps: 500, floorBps: 1200}, qd1Cfg)
	ymCtx := qd1WithRole(qCtxAs(intStaff(intTrenton)), "YARD_MANAGER", intTrenton, ym)
	if _, yerr := ymSvc.SetLineDiscount(ymCtx, ymQuote, 1, 25); !errors.Is(yerr, ErrForbidden) {
		t.Errorf("the Yard manager must be refused outright at the desk, got %T: %v", yerr, yerr)
	}
	if got := liveLine(t, pool, ymQuote, 1); got.LineTotalCents != 2000 {
		t.Errorf("the Yard manager's refused write changed the line, total now %d", got.LineTotalCents)
	}
}
