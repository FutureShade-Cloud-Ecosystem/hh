//go:build demo

package demo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// fakeMinter records the subjects it is asked to sign and answers a fixed
// token, or the error it is told to.
type fakeMinter struct {
	subjects []string
	token    string
	err      error
}

func (f *fakeMinter) Mint(subject string) (string, error) {
	f.subjects = append(f.subjects, subject)
	if f.err != nil {
		return "", f.err
	}
	return f.token, nil
}

// fakeOrgs answers a fixed org id, presence and error.
type fakeOrgs struct {
	id    string
	ok    bool
	err   error
	calls []string
}

func (f *fakeOrgs) ResolveSlug(_ context.Context, slug string) (string, bool, error) {
	f.calls = append(f.calls, slug)
	return f.id, f.ok, f.err
}

// fakeStaff answers a fixed authority, presence and error, and records
// every question's platform user id and org id.
type fakeStaff struct {
	auth  identity.StaffAuthority
	ok    bool
	err   error
	calls [][2]string
}

func (f *fakeStaff) ResolveStaff(_ context.Context, platformUserID, orgID string) (identity.StaffAuthority, bool, error) {
	f.calls = append(f.calls, [2]string{platformUserID, orgID})
	return f.auth, f.ok, f.err
}

// salesAuthority is the live answer the demo world's database gives for
// the sales account: inside_sales, front line, branch scope.
func salesAuthority() identity.StaffAuthority {
	return identity.StaffAuthority{
		RoleID: "inside_sales", Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
		Branches: []string{"5d1b3f6e-0000-4000-9000-000000000001"},
	}
}

// newTestMint builds the module over fakes, with the sales authority as
// the staff answer and the demo org resolving.
func newTestMint(t *testing.T, minter *fakeMinter, staff *fakeStaff) (*Mint, *fakeOrgs) {
	t.Helper()
	if staff == nil {
		staff = &fakeStaff{auth: salesAuthority(), ok: true}
	}
	orgs := &fakeOrgs{id: "org-demo-1", ok: true}
	m, err := NewMint(MintDeps{Minter: minter, Orgs: orgs, Staff: staff})
	if err != nil {
		t.Fatalf("NewMint: %v", err)
	}
	return m, orgs
}

// postSession drives the session route the way the entry page calls it.
func postSession(t *testing.T, m *Mint, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/demo/session", strings.NewReader(body))
	rsp := httptest.NewRecorder()
	m.Session(rsp, req)
	return rsp
}

// The flow listing answers the closed table, in the table's order, with
// the contract's three fields per row and nothing else: no account name
// and no address on a pre-auth listing.
func TestFlowsAnswersTheClosedTable(t *testing.T) {
	m, _ := newTestMint(t, &fakeMinter{token: "t"}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/demo/flows", nil)
	rsp := httptest.NewRecorder()
	m.Flows(rsp, req)

	if rsp.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rsp.Code)
	}
	var got flowList
	if err := json.Unmarshal(rsp.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not a flow list: %v", err)
	}
	all := flows.All()
	if len(got.Flows) != len(all) {
		t.Fatalf("the listing carries %d flows, want the table's %d", len(got.Flows), len(all))
	}
	for i, row := range got.Flows {
		if row.Flow != all[i].Flow || row.RoleCode != all[i].Role || row.Label != all[i].Label {
			t.Errorf("row %d is %+v, want the table's flow %q, role %q, label %q",
				i, row, all[i].Flow, all[i].Role, all[i].Label)
		}
	}
	// The labels are the flow table's, never the roles table's: the roles
	// catalog spells the pair differently, and the testers' page must read
	// what the table says.
	if got.Flows[0].Label != "Sales" || got.Flows[1].Label != "Sales manager" {
		t.Errorf("labels %q and %q are not the flow table's", got.Flows[0].Label, got.Flows[1].Label)
	}
}

// A session mint for a table flow answers the contract's response: the
// token from the issuer, the flow echoed, and the staff login user block
// field for field, with the role label taken from the flow table.
func TestSessionAnswersTheContractShape(t *testing.T) {
	minter := &fakeMinter{token: "demo-token-1"}
	staff := &fakeStaff{auth: salesAuthority(), ok: true}
	m, orgs := newTestMint(t, minter, staff)
	rsp := postSession(t, m, `{"flow":"sales"}`)

	if rsp.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 (body: %s)", rsp.Code, rsp.Body.String())
	}
	if len(minter.subjects) != 1 || minter.subjects[0] != "de110000-0000-4000-9000-000000000001" {
		t.Fatalf("the minter was asked to sign %v, want exactly the sales account's platform user id", minter.subjects)
	}
	// The authority question was asked about the demo org's resolved id,
	// not the slug and not whatever the request carried.
	if len(orgs.calls) != 1 || orgs.calls[0] != flows.OrgSlug {
		t.Fatalf("the org resolver was asked %v, want exactly the demo org's slug", orgs.calls)
	}
	if len(staff.calls) != 1 || staff.calls[0] != [2]string{"de110000-0000-4000-9000-000000000001", "org-demo-1"} {
		t.Fatalf("the staff resolver was asked %v, want the sales account against the resolved org id", staff.calls)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rsp.Body.Bytes(), &raw); err != nil {
		t.Fatalf("body is not a JSON object: %v", err)
	}
	if string(raw["token"]) != `"demo-token-1"` {
		t.Errorf("token %s, want the issuer's", raw["token"])
	}
	if string(raw["flow"]) != `"sales"` {
		t.Errorf("flow %s, want the request's flow echoed", raw["flow"])
	}
	if _, ok := raw["chapter_key"]; ok {
		t.Errorf("the answer carries a chapter_key: the closed table names no walkthrough, the field stays absent")
	}
	var user struct {
		UserID      string `json:"user_id"`
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		RoleCode    string `json:"role_code"`
		RoleName    string `json:"role_name"`
		Tier        int    `json:"tier"`
		Scope       string `json:"scope"`
	}
	if err := json.Unmarshal(raw["user"], &user); err != nil {
		t.Fatalf("the user block does not decode: %v", err)
	}
	account, _ := flows.Get("sales")
	if user.UserID != account.PlatformUserID || user.Email != account.Email ||
		user.DisplayName != account.DisplayName || user.RoleCode != account.Role {
		t.Errorf("user block %+v, want the flow table's account values", user)
	}
	if user.RoleName != "Sales" {
		t.Errorf("role name %q, want the flow table's label, never the roles table's", user.RoleName)
	}
	if user.Tier != identity.TierFrontline || user.Scope != "BRANCH" {
		t.Errorf("tier %d scope %q, want the resolver's answer with the scope spelled the contract's enum", user.Tier, user.Scope)
	}
}

// The manager flow mints the manager's credential: the shape W8's
// approval dialog sends on the one call that needs it.
func TestSessionMintsTheManagerFlow(t *testing.T) {
	minter := &fakeMinter{token: "demo-token-2"}
	m, _ := newTestMint(t, minter, &fakeStaff{
		auth: identity.StaffAuthority{
			RoleID: "sales_manager", Tier: identity.TierManager, Scope: identity.ScopeBranch,
		},
		ok: true,
	})
	rsp := postSession(t, m, `{"flow":"sales-manager"}`)
	if rsp.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 (body: %s)", rsp.Code, rsp.Body.String())
	}
	if len(minter.subjects) != 1 || minter.subjects[0] != "de110000-0000-4000-9000-000000000002" {
		t.Fatalf("the minter was asked to sign %v, want exactly the manager account's platform user id", minter.subjects)
	}
	var got sessionResponse
	if err := json.Unmarshal(rsp.Body.Bytes(), &got); err != nil {
		t.Fatalf("body does not decode: %v", err)
	}
	if got.User.RoleCode != "sales_manager" || got.User.RoleName != "Sales manager" {
		t.Errorf("manager block carries role %q named %q, want the flow table's pair", got.User.RoleCode, got.User.RoleName)
	}
	if got.User.Tier != identity.TierManager || got.User.Scope != "BRANCH" {
		t.Errorf("manager tier %d scope %q, want the resolver's answer", got.User.Tier, got.User.Scope)
	}
}

// The request carries a flow name and nothing else: a malformed body, a
// missing flow and a name outside the closed table are all 400, and none
// of them reaches the issuer.
func TestSessionRefusesBadRequests(t *testing.T) {
	minter := &fakeMinter{token: "t"}
	m, _ := newTestMint(t, minter, nil)
	cases := []struct {
		name string
		body string
	}{
		{"not JSON", `flow=sales`},
		{"empty object", `{}`},
		{"empty flow", `{"flow":""}`},
		{"unknown flow", `{"flow":"receivables"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rsp := postSession(t, m, c.body)
			if rsp.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400 (body: %s)", rsp.Code, rsp.Body.String())
			}
			var got map[string]string
			if err := json.Unmarshal(rsp.Body.Bytes(), &got); err != nil {
				t.Fatalf("the refusal is not the error envelope: %v", err)
			}
			if got["error"] == "" {
				t.Fatal("the refusal carries no message")
			}
		})
	}
	if len(minter.subjects) != 0 {
		t.Fatalf("the minter was called for %v, a refused request must reach no issuer", minter.subjects)
	}
}

// The role refusal: each flow names the role its account must hold, and
// the staff resolver's live answer decides. No authority, another role,
// an inactive account: every one refuses the mint with the 403 that names
// its own check, and none of them reaches the issuer.
func TestSessionRefusesWhenTheRoleDoesNotAnswer(t *testing.T) {
	cases := []struct {
		name    string
		staff   *fakeStaff
		wantExt string
	}{
		{
			name:    "no staff grant at all",
			staff:   &fakeStaff{ok: false},
			wantExt: "holds no staff grant",
		},
		{
			name: "another role where the flow's should be",
			staff: &fakeStaff{
				auth: identity.StaffAuthority{RoleID: "yard_team", Tier: identity.TierFrontline, Scope: identity.ScopeBranch},
				ok:   true,
			},
			wantExt: "the flow requires inside_sales",
		},
		{
			name:    "the account is not active",
			staff:   &fakeStaff{err: identity.ErrStaffUserInactive},
			wantExt: "not active",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			minter := &fakeMinter{token: "t"}
			m, _ := newTestMint(t, minter, c.staff)
			rsp := postSession(t, m, `{"flow":"sales"}`)
			if rsp.Code != http.StatusForbidden {
				t.Fatalf("status %d, want 403 (body: %s)", rsp.Code, rsp.Body.String())
			}
			var got map[string]string
			if err := json.Unmarshal(rsp.Body.Bytes(), &got); err != nil {
				t.Fatalf("the refusal is not the error envelope: %v", err)
			}
			if !strings.Contains(got["error"], c.wantExt) {
				t.Errorf("refusal %q does not name its own check (%q)", got["error"], c.wantExt)
			}
			if len(minter.subjects) != 0 {
				t.Errorf("the minter was called for %v, a role refusal must reach no issuer", minter.subjects)
			}
		})
	}
}

// The operational failures are 500, not 403: a database error and a
// missing demo org are defects of the box, not refusals of the flow, and
// neither mints.
func TestSessionAnswersOperationalFailureAs500(t *testing.T) {
	cases := []struct {
		name  string
		orgs  *fakeOrgs
		staff *fakeStaff
	}{
		{
			name: "the org read fails",
			orgs: &fakeOrgs{err: errors.New("connection refused")},
		},
		{
			name: "the demo org is missing",
			orgs: &fakeOrgs{ok: false},
		},
		{
			name:  "the authority read fails",
			staff: &fakeStaff{err: errors.New("connection refused")},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			minter := &fakeMinter{token: "t"}
			if c.orgs == nil {
				c.orgs = &fakeOrgs{id: "org-demo-1", ok: true}
			}
			if c.staff == nil {
				c.staff = &fakeStaff{auth: salesAuthority(), ok: true}
			}
			m, err := NewMint(MintDeps{Minter: minter, Orgs: c.orgs, Staff: c.staff})
			if err != nil {
				t.Fatalf("NewMint: %v", err)
			}
			rsp := postSession(t, m, `{"flow":"sales"}`)
			if rsp.Code != http.StatusInternalServerError {
				t.Fatalf("status %d, want 500 (body: %s)", rsp.Code, rsp.Body.String())
			}
			if len(minter.subjects) != 0 {
				t.Errorf("the minter was called for %v, an operational failure mints nothing", minter.subjects)
			}
		})
	}
}

// NewMint refuses to build without every collaborator: the contract's own
// condition that the module refuse to register unless its dependencies
// are wired.
func TestNewMintRequiresItsCollaborators(t *testing.T) {
	cases := []struct {
		name string
		deps MintDeps
	}{
		{"no issuer", MintDeps{Orgs: &fakeOrgs{ok: true}, Staff: &fakeStaff{ok: true}}},
		{"no org resolver", MintDeps{Minter: &fakeMinter{}, Staff: &fakeStaff{ok: true}}},
		{"no staff resolver", MintDeps{Minter: &fakeMinter{}, Orgs: &fakeOrgs{ok: true}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NewMint(c.deps); err == nil {
				t.Fatal("NewMint accepted an unwired collaborator")
			}
		})
	}
}
