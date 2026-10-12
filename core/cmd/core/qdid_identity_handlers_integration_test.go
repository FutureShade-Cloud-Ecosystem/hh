//go:build integration

package main

// qdid_identity_handlers_integration_test.go drives the two staff identity routes Quote Desk calls
// (identityCheckPolicy and identityListStaff) through the registered surface over a migrated
// database, every guard real: the policy check against the margin floor and a real approved
// override row, the roster against real users and grants in two orgs.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestQDID_PolicyCheck_AgainstTheDatabase(t *testing.T) {
	q := newQD1BFixture(t)
	const path = "/api/v1/staff/policy/check"
	ask := func(sub string, price, cost int64, branch string) (int, string) {
		return qd1bCall(t, q, http.MethodPost, path, sub, map[string]any{"price_cents": price, "cost_cents": cost, "branch_id": branch})
	}

	// A clerk of branch A above the floor: allowed, no override.
	code, body := ask(testkit.SubInsideSales, 10000, 8000, testkit.BranchNorthAID)
	if code != http.StatusOK || !strings.Contains(body, `"allowed":true`) || !strings.Contains(body, `"margin_bps":2000`) || !strings.Contains(body, `"override":false`) {
		t.Fatalf("above floor = %d %s, want 200 allowed at 2000 bps with no override", code, body)
	}

	// Below the floor, no approved override: 403 with the block under the block key.
	code, body = ask(testkit.SubInsideSales, 10000, 9000, testkit.BranchNorthAID)
	var blocked struct {
		Error string `json:"error"`
		Block struct {
			PolicyType string            `json:"policy_type"`
			Detail     string            `json:"detail"`
			Data       map[string]string `json:"data"`
		} `json:"block"`
	}
	if code != http.StatusForbidden || json.Unmarshal([]byte(body), &blocked) != nil ||
		blocked.Block.PolicyType != "MARGIN_BELOW_MINIMUM" || blocked.Block.Data["margin_bps"] != "1000" || blocked.Error != blocked.Block.Detail {
		t.Fatalf("below floor = %d %s, want 403 with the margin block", code, body)
	}

	// A branch outside the clerk's grants is refused, above the floor or not.
	if code, body = ask(testkit.SubInsideSales, 10000, 8000, testkit.BranchNorthBID); code != http.StatusForbidden || strings.Contains(body, `"block"`) {
		t.Fatalf("foreign branch = %d %s, want a plain 403", code, body)
	}
	// (A global role reaching any branch is the unit leg's; the dealer's role gate admits only the sellers here.)

	// An APPROVED margin override for this clerk and branch clears the block; another caller's does not.
	clerk := qd1bUserID(t, q.pool, testkit.SubInsideSales)
	id := qd1bPlantFloor(t, q, clerk, testkit.BranchNorthAID, "000000000e01")
	if _, err := q.pool.Exec(context.Background(), `UPDATE permission_approval_requests SET status = 'APPROVED' WHERE id = $1`, id); err != nil {
		t.Fatalf("approve the override: %v", err)
	}
	code, body = ask(testkit.SubInsideSales, 10000, 9000, testkit.BranchNorthAID)
	if code != http.StatusOK || !strings.Contains(body, `"override":true`) || !strings.Contains(body, `"margin_bps":1000`) {
		t.Fatalf("with an approved override = %d %s, want 200 allowed with override true", code, body)
	}
	if code, body = ask(testkit.SubSalesManager, 10000, 9000, testkit.BranchNorthAID); code != http.StatusForbidden {
		t.Fatalf("another caller on the clerk's override = %d %s, want 403", code, body)
	}

	// Once the line write has used the approval up it is no standing override: the check answers 403 again.
	if _, err := q.pool.Exec(context.Background(), `UPDATE permission_approval_requests SET data = data || '{"used": true}'::jsonb WHERE id = $1`, id); err != nil {
		t.Fatalf("use the override up: %v", err)
	}
	if code, body = ask(testkit.SubInsideSales, 10000, 9000, testkit.BranchNorthAID); code != http.StatusForbidden || !strings.Contains(body, `"block"`) {
		t.Fatalf("with a used up override = %d %s, want the 403 block", code, body)
	}

	// A bad body is a 400.
	if code, body = qd1bCall(t, q, http.MethodPost, path, testkit.SubInsideSales, map[string]any{"price_cents": 1}); code != http.StatusBadRequest {
		t.Fatalf("missing fields = %d %s, want 400", code, body)
	}

	// No bearer is refused by the gate.
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"price_cents":1,"cost_cents":1,"branch_id":"`+testkit.BranchNorthAID+`"}`))
	rec := httptest.NewRecorder()
	q.fx.reg.inner.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d, want 401", rec.Code)
	}
}

func TestQDID_Roster_AgainstTheDatabase(t *testing.T) {
	q := newQD1BFixture(t)
	ctx := context.Background()

	// A person whose only grant is in the south org: never in the north roster.
	const southSub, southUser, southGrant = "qdid-south-only", "3ef10000-0000-4000-8000-0000000d1001", "3ef10000-0000-4000-8000-0000000d1002"
	if _, err := q.pool.Exec(ctx, `INSERT INTO users (id, email, display_name, status, platform_user_id, org_id)
		VALUES ($1, 'qdid.south.only@fixture.test', 'Fixture South Only', 'ACTIVE', $2, $3)`, southUser, southSub, testkit.OrgSouthID); err != nil {
		t.Fatalf("plant the south user: %v", err)
	}
	if _, err := q.pool.Exec(ctx, `INSERT INTO staff_role_grant (id, platform_user_id, role_id, org_id, branch_id)
		VALUES ($1, $2, 'general_manager', $3, NULL)`, southGrant, southSub, testkit.OrgSouthID); err != nil {
		t.Fatalf("plant the south grant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = q.pool.Exec(ctx, `DELETE FROM staff_role_grant WHERE id = $1`, southGrant)
		_, _ = q.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, southUser)
	})

	type member struct {
		UserID    string   `json:"user_id"`
		Email     string   `json:"email"`
		RoleCode  string   `json:"role_code"`
		RoleName  string   `json:"role_name"`
		Tier      int      `json:"tier"`
		Scope     string   `json:"scope"`
		BranchIDs []string `json:"branch_ids"`
	}
	read := func(sub, query string) (int, struct {
		Items      []member `json:"items"`
		NextCursor *string  `json:"next_cursor"`
	}) {
		var page struct {
			Items      []member `json:"items"`
			NextCursor *string  `json:"next_cursor"`
		}
		code, body := qd1bCall(t, q, http.MethodGet, "/api/v1/staff"+query, sub, nil)
		if code == http.StatusOK {
			if err := json.Unmarshal([]byte(body), &page); err != nil {
				t.Fatalf("roster body is not a page: %v (%s)", err, body)
			}
		}
		return code, page
	}

	// A front-line clerk is refused.
	if code, _ := read(testkit.SubInsideSales, ""); code != http.StatusForbidden {
		t.Fatalf("clerk roster = %d, want 403", code)
	}

	// A manager reads the north org: the planted principals with their roles, the south person absent.
	code, page := read(testkit.SubSalesManager, "")
	if code != http.StatusOK {
		t.Fatalf("manager roster = %d, want 200", code)
	}
	byUser := map[string]member{}
	for _, m := range page.Items {
		if _, dup := byUser[m.UserID]; dup {
			t.Fatalf("user %s listed twice: one row per person", m.UserID)
		}
		byUser[m.UserID] = m
	}
	if _, leaked := byUser[southUser]; leaked {
		t.Fatalf("the south org's person is in the north roster")
	}
	gm := byUser[qd1bUserID(t, q.pool, testkit.SubGeneralMgr)]
	if gm.RoleCode != "GENERAL_MANAGER" || gm.Tier != 1 || gm.Scope != "GLOBAL" || gm.BranchIDs == nil || len(gm.BranchIDs) != 0 {
		t.Fatalf("general manager row = %+v, want GENERAL_MANAGER, tier 1, GLOBAL, empty branch_ids", gm)
	}
	clerk := byUser[qd1bUserID(t, q.pool, testkit.SubInsideSales)]
	if clerk.RoleCode != "INSIDE_SALES" || clerk.Tier != 3 || clerk.Scope != "BRANCH" || len(clerk.BranchIDs) != 1 || clerk.BranchIDs[0] != testkit.BranchNorthAID {
		t.Fatalf("clerk row = %+v, want INSIDE_SALES, tier 3, BRANCH, branch A", clerk)
	}
	if two := byUser[qd1bUserID(t, q.pool, testkit.SubTwoOrg)]; two.UserID == "" {
		t.Fatalf("the two org person has a north grant and must be listed in the north roster")
	}
	for i := 1; i < len(page.Items); i++ {
		if page.Items[i].Tier < page.Items[i-1].Tier {
			t.Fatalf("roster is not ordered by tier at %d", i)
		}
	}

	// Paging walks the whole list once, with no repeat and no gap.
	var walked []string
	cursor := ""
	for pages := 0; pages < len(page.Items)+2; pages++ {
		query := "?limit=2"
		if cursor != "" {
			query += "&cursor=" + cursor
		}
		c, p := read(testkit.SubGeneralMgr, query)
		if c != http.StatusOK {
			t.Fatalf("page %d = %d", pages, c)
		}
		for _, m := range p.Items {
			walked = append(walked, m.UserID)
		}
		if p.NextCursor == nil {
			break
		}
		cursor = *p.NextCursor
	}
	if len(walked) != len(page.Items) {
		t.Fatalf("the walk read %d members, the single read %d", len(walked), len(page.Items))
	}
	for i, id := range walked {
		if id != page.Items[i].UserID {
			t.Fatalf("walk position %d = %s, single read %s", i, id, page.Items[i].UserID)
		}
	}

	// No bearer is refused.
	rec := httptest.NewRecorder()
	q.fx.reg.inner.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/staff", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous roster = %d, want 401", rec.Code)
	}
}
