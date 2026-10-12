package staffcontext

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// rosterDouble answers per org and records the org it was asked for, so a test proves the handler
// hands the request org (never a body or query value) to the reader.
type rosterDouble struct {
	byOrg map[string][]identity.RosterEntry
	asked []string
	err   error
}

func (d *rosterDouble) Roster(_ context.Context, orgID string) ([]identity.RosterEntry, error) {
	d.asked = append(d.asked, orgID)
	return d.byOrg[orgID], d.err
}

// The roster route (identityListStaff) over the real Guards: a manager reads the members of the
// request org in the contract's shape, a non manager is refused, no bearer is refused, and another
// org's members never appear.
func TestStaffRoster_Route(t *testing.T) {
	sg := testkit.NewStaffGuards(t)
	bearer := sg.Bearer(t)

	// The reader answers in the list order: tier, display name, id.
	d := &rosterDouble{byOrg: map[string][]identity.RosterEntry{
		testkit.OrgNorthID: {
			{UserID: "a", Email: "gm@example.test", DisplayName: "General Manager One", RoleID: "general_manager", RoleLabel: "General Manager", Tier: 1, Scope: "global"},
			{UserID: "c", Email: "fb@example.test", DisplayName: "Operator One", RoleID: "fb_operator", RoleLabel: "FutureBuild Operator", Tier: 1, Scope: "global"},
			{UserID: "b", Email: "is@example.test", DisplayName: "Inside Sales One", RoleID: "inside_sales", RoleLabel: "Inside Sales", Tier: 3, Scope: "branch", BranchIDs: []string{trt}},
		},
		testkit.OrgSouthID: {{UserID: "z", Email: "south@example.test", DisplayName: "South One", RoleID: "general_manager", RoleLabel: "General Manager", Tier: 1, Scope: "global"}},
	}}
	mux := http.NewServeMux()
	NewStaffRosterHandler(d).RegisterRoutes(mux, sg.Guards)

	get := func(path string, withBearer bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if withBearer {
			r.Header.Set("Authorization", "Bearer "+bearer)
			r.Header.Set("X-Org", testkit.OrgNorthSlug)
		}
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, r)
		return rr
	}
	type member struct {
		UserID      string   `json:"user_id"`
		Email       string   `json:"email"`
		DisplayName string   `json:"display_name"`
		RoleCode    string   `json:"role_code"`
		RoleName    string   `json:"role_name"`
		Tier        int      `json:"tier"`
		Scope       string   `json:"scope"`
		BranchIDs   []string `json:"branch_ids"`
	}
	type page struct {
		Items      []member `json:"items"`
		NextCursor *string  `json:"next_cursor"`
		Limit      int      `json:"limit"`
	}

	// A front-line caller is refused (manager tier), and the reader is never consulted.
	sg.SetAuthority(identity.StaffAuthority{RoleID: "inside_sales", Tier: 3, Scope: "branch", Branches: []string{trt}})
	if rr := get("/api/v1/staff", true); rr.Code != http.StatusForbidden {
		t.Fatalf("front-line roster = %d, want 403 (body %s)", rr.Code, rr.Body.String())
	}
	if len(d.asked) != 0 {
		t.Fatalf("the reader was asked %v for a refused caller", d.asked)
	}

	// No bearer is refused.
	if rr := get("/api/v1/staff", false); rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
		t.Fatalf("anonymous roster = %d, want the gate's refusal", rr.Code)
	}

	// A manager reads the request org's members, contract shaped, none from the other org.
	sg.SetAuthority(identity.StaffAuthority{RoleID: "branch_manager", Tier: 2, Scope: "branch", Branches: []string{trt}})
	rr := get("/api/v1/staff", true)
	if rr.Code != http.StatusOK {
		t.Fatalf("manager roster = %d, want 200 (body %s)", rr.Code, rr.Body.String())
	}
	var p page
	if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil {
		t.Fatalf("roster body is not a page: %v (%s)", err, rr.Body.String())
	}
	if len(p.Items) != 3 || p.NextCursor != nil {
		t.Fatalf("roster = %d items, cursor %v, want the 3 north members and a null cursor (%s)", len(p.Items), p.NextCursor, rr.Body.String())
	}
	for _, m := range p.Items {
		if m.UserID == "z" || m.Email == "south@example.test" {
			t.Fatalf("the other org's member leaked: %+v", m)
		}
	}
	gm, is := p.Items[0], p.Items[2]
	if gm.RoleCode != "GENERAL_MANAGER" || gm.RoleName != "General Manager" || gm.Tier != 1 || gm.Scope != "GLOBAL" || gm.BranchIDs == nil || len(gm.BranchIDs) != 0 {
		t.Fatalf("global member = %+v, want the ERP code, GLOBAL scope and an empty branch_ids array", gm)
	}
	if is.RoleCode != "INSIDE_SALES" || is.Scope != "BRANCH" || len(is.BranchIDs) != 1 || is.BranchIDs[0] != trt {
		t.Fatalf("branch member = %+v, want INSIDE_SALES, BRANCH and the granted branch", is)
	}
	// A role with no ERP code carries the canonical id so the row is never blank.
	if op := p.Items[1]; op.RoleCode != "fb_operator" {
		t.Fatalf("role without an ERP code = %q, want the canonical id", op.RoleCode)
	}
	if len(d.asked) != 1 || d.asked[0] != testkit.OrgNorthID {
		t.Fatalf("reader asked for %v, want exactly the request org", d.asked)
	}

	// Paging: limit 2 yields a cursor, the cursor yields the rest, and the walk repeats nothing.
	rr = get("/api/v1/staff?limit=2", true)
	var first page
	if err := json.Unmarshal(rr.Body.Bytes(), &first); err != nil || len(first.Items) != 2 || first.NextCursor == nil || first.Limit != 2 {
		t.Fatalf("first page = %s (err %v), want 2 items, a cursor and limit 2", rr.Body.String(), err)
	}
	rr = get("/api/v1/staff?limit=2&cursor="+*first.NextCursor, true)
	var second page
	if err := json.Unmarshal(rr.Body.Bytes(), &second); err != nil || len(second.Items) != 1 || second.NextCursor != nil || second.Items[0].UserID != p.Items[2].UserID {
		t.Fatalf("second page = %s (err %v), want the third member and a null cursor", rr.Body.String(), err)
	}
	if rr = get("/api/v1/staff?cursor=bm90LWEtY3Vyc29y", true); rr.Code != http.StatusBadRequest {
		t.Fatalf("malformed cursor = %d, want 400", rr.Code)
	}

	// An empty org answers an empty items array, never null.
	d.byOrg[testkit.OrgNorthID] = nil
	rr = get("/api/v1/staff", true)
	if rr.Code != http.StatusOK || !jsonHasEmptyItems(rr.Body.Bytes()) {
		t.Fatalf("empty roster = %d %s, want 200 with items []", rr.Code, rr.Body.String())
	}

	// A reader failure is a 500 that leaks nothing.
	d.err = context.DeadlineExceeded
	if rr = get("/api/v1/staff", true); rr.Code != http.StatusInternalServerError {
		t.Fatalf("reader failure = %d, want 500", rr.Code)
	}
}

func jsonHasEmptyItems(b []byte) bool {
	var v struct {
		Items []json.RawMessage `json:"items"`
	}
	return json.Unmarshal(b, &v) == nil && v.Items != nil && len(v.Items) == 0
}
