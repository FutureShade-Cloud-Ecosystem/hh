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
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// fakeMinter records the subjects it is asked to sign and answers a fixed
// token, or the error it is told to. When it shares an event log, it
// appends there, so a test can hold an order between collaborators.
type fakeMinter struct {
	subjects []string
	token    string
	err      error
	events   *[]string
}

func (f *fakeMinter) Mint(subject string) (string, error) {
	f.subjects = append(f.subjects, subject)
	if f.events != nil {
		*f.events = append(*f.events, "mint")
	}
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

// fakeAudit records every row it is asked to write, and can be told to
// fail and to share an event log.
type fakeAudit struct {
	rows   []audit.SensitiveAccess
	err    error
	events *[]string
}

func (f *fakeAudit) Record(_ context.Context, a audit.SensitiveAccess) error {
	f.rows = append(f.rows, a)
	if f.events != nil {
		*f.events = append(*f.events, "audit")
	}
	return f.err
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
// the staff answer, the demo org resolving, and a recording audit
// recorder.
func newTestMint(t *testing.T, minter *fakeMinter, staff *fakeStaff) (*Mint, *fakeOrgs, *fakeAudit) {
	t.Helper()
	if staff == nil {
		staff = &fakeStaff{auth: salesAuthority(), ok: true}
	}
	orgs := &fakeOrgs{id: "org-demo-1", ok: true}
	auditFake := &fakeAudit{}
	m, err := NewMint(MintDeps{Minter: minter, Orgs: orgs, Staff: staff, Audit: auditFake})
	if err != nil {
		t.Fatalf("NewMint: %v", err)
	}
	return m, orgs, auditFake
}

// postSession drives the session route the way the entry page calls it.
func postSession(t *testing.T, m *Mint, body string) *httptest.ResponseRecorder {
	t.Helper()
	return postSessionFrom(t, m, body, "192.0.2.1:1234", nil)
}

// postSessionFrom drives the session route from a named peer address with
// extra headers, the way the limit's key question is exercised.
func postSessionFrom(t *testing.T, m *Mint, body, remoteAddr string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/demo/session", strings.NewReader(body))
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rsp := httptest.NewRecorder()
	m.Session(rsp, req)
	return rsp
}

// The flow listing answers the closed table, in the table's order, with
// the contract's three fields per row and nothing else: no account name
// and no address on a pre-auth listing.
func TestFlowsAnswersTheClosedTable(t *testing.T) {
	m, _, _ := newTestMint(t, &fakeMinter{token: "t"}, nil)
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
	m, orgs, _ := newTestMint(t, minter, staff)
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
	m, _, _ := newTestMint(t, minter, &fakeStaff{
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

// The persona flows mint the roles roadmap's first increments' logins: the
// Yard Team and the Driver personas migration 0200 plants, each answered by
// the resolver's live role (tier 3, branch scope), with the table's labels.
func TestSessionMintsThePersonaFlows(t *testing.T) {
	for _, tc := range []struct {
		flow  string
		role  string
		label string
		id    string
	}{
		{"yard-team", "yard_team", "Yard Team", "d0200000-0000-4000-9000-000000000002"},
		{"driver", "driver", "Driver", "d0200000-0000-4000-9000-000000000003"},
	} {
		t.Run(tc.flow, func(t *testing.T) {
			minter := &fakeMinter{token: "demo-token-" + tc.flow}
			m, _, _ := newTestMint(t, minter, &fakeStaff{
				auth: identity.StaffAuthority{
					RoleID: tc.role, Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
				},
				ok: true,
			})
			rsp := postSession(t, m, `{"flow":"`+tc.flow+`"}`)
			if rsp.Code != http.StatusOK {
				t.Fatalf("status %d, want 200 (body: %s)", rsp.Code, rsp.Body.String())
			}
			if len(minter.subjects) != 1 || minter.subjects[0] != tc.id {
				t.Fatalf("the minter was asked to sign %v, want the persona's platform user id", minter.subjects)
			}
			var got sessionResponse
			if err := json.Unmarshal(rsp.Body.Bytes(), &got); err != nil {
				t.Fatalf("body does not decode: %v", err)
			}
			if got.Flow != tc.flow {
				t.Errorf("the answer echoes flow %q, want %q", got.Flow, tc.flow)
			}
			if got.User.UserID != tc.id || got.User.RoleCode != tc.role || got.User.RoleName != tc.label {
				t.Errorf("the persona credential is %+v, want the flow table's %s account", got.User, tc.flow)
			}
			if got.User.Tier != identity.TierFrontline || got.User.Scope != "BRANCH" {
				t.Errorf("tier %d scope %q, want the resolver's answer", got.User.Tier, got.User.Scope)
			}
		})
	}
}

// The request carries a flow name and nothing else: a malformed body, a
// missing flow and a name outside the closed table are all 400, and none
// of them reaches the issuer.
func TestSessionRefusesBadRequests(t *testing.T) {
	minter := &fakeMinter{token: "t"}
	m, _, _ := newTestMint(t, minter, nil)
	cases := []struct {
		name string
		body string
	}{
		{"not JSON", `flow=sales`},
		{"empty object", `{}`},
		{"empty flow", `{"flow":""}`},
		{"unknown flow", `{"flow":"receivables"}`},
		{"an unknown field", `{"flow":"sales","email":"x","role":"y"}`},
		{"trailing data", `{"flow":"sales"} {"flow":"sales-manager"}`},
		{"trailing garbage", `{"flow":"sales"} not json`},
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
			m, _, _ := newTestMint(t, minter, c.staff)
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
			m, err := NewMint(MintDeps{Minter: minter, Orgs: c.orgs, Staff: c.staff, Audit: &fakeAudit{}})
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
		{"no issuer", MintDeps{Orgs: &fakeOrgs{ok: true}, Staff: &fakeStaff{ok: true}, Audit: &fakeAudit{}}},
		{"no org resolver", MintDeps{Minter: &fakeMinter{}, Staff: &fakeStaff{ok: true}, Audit: &fakeAudit{}}},
		{"no staff resolver", MintDeps{Minter: &fakeMinter{}, Orgs: &fakeOrgs{ok: true}, Audit: &fakeAudit{}}},
		{"no audit recorder", MintDeps{Minter: &fakeMinter{}, Orgs: &fakeOrgs{ok: true}, Staff: &fakeStaff{ok: true}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NewMint(c.deps); err == nil {
				t.Fatal("NewMint accepted an unwired collaborator")
			}
		})
	}
}

// The order the plan states: the audit row is written before the token.
// Both collaborators share one event log, and the log must read audit
// then mint; the row itself names the action, the route's entity type and
// the account the session is minted for.
func TestSessionWritesTheAuditRowBeforeTheToken(t *testing.T) {
	events := []string{}
	minter := &fakeMinter{token: "t", events: &events}
	auditFake := &fakeAudit{events: &events}
	m, err := NewMint(MintDeps{
		Minter: minter,
		Orgs:   &fakeOrgs{id: "org-demo-1", ok: true},
		Staff:  &fakeStaff{auth: salesAuthority(), ok: true},
		Audit:  auditFake,
	})
	if err != nil {
		t.Fatalf("NewMint: %v", err)
	}

	rsp := postSession(t, m, `{"flow":"sales"}`)
	if rsp.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 (body: %s)", rsp.Code, rsp.Body.String())
	}
	if len(events) != 2 || events[0] != "audit" || events[1] != "mint" {
		t.Fatalf("the event log reads %v, want audit then mint", events)
	}
	if len(auditFake.rows) != 1 {
		t.Fatalf("%d audit rows written, want one per mint", len(auditFake.rows))
	}
	row := auditFake.rows[0]
	if row.Action != "DEMO_SESSION_MINTED" || row.EntityType != "demo.session" ||
		row.EntityID != "de110000-0000-4000-9000-000000000001" ||
		row.ActorID != "de110000-0000-4000-9000-000000000001" || row.Purpose != "DEMO_SIGN_IN" {
		t.Errorf("the mint's trail row is %+v, want the demo session mint row naming the sales account", row)
	}
}

// A failure to trail is a failure to mint: the audit row is written
// before the token, so when the write fails there is no token, and the
// issuer is never reached.
func TestSessionRefusesWhenTheAuditRowFails(t *testing.T) {
	minter := &fakeMinter{token: "t"}
	m, _, _ := newTestMint(t, minter, nil)
	m.audit = &fakeAudit{err: errors.New("audit write refused")}

	rsp := postSession(t, m, `{"flow":"sales"}`)
	if rsp.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500 (body: %s)", rsp.Code, rsp.Body.String())
	}
	if len(minter.subjects) != 0 {
		t.Errorf("the minter was called for %v, an untrailed mint must not sign", minter.subjects)
	}
}

// A burst from one address is refused: the module's own limit, at the
// default allowance, answers the first admissions and refuses the rest,
// and a refused mint signs nothing and trails nothing. A request the
// closed table refuses never counts against the allowance.
func TestSessionRefusesABurst(t *testing.T) {
	minter := &fakeMinter{token: "t"}
	m, _, auditFake := newTestMint(t, minter, nil)
	m.limit = NewAddressLimit(2, time.Second, nil)

	for i := 0; i < 2; i++ {
		rsp := postSession(t, m, `{"flow":"sales"}`)
		if rsp.Code != http.StatusOK {
			t.Fatalf("admission %d: status %d, want 200", i+1, rsp.Code)
		}
	}
	refused := postSession(t, m, `{"flow":"sales"}`)
	if refused.Code != http.StatusTooManyRequests {
		t.Fatalf("the burst answer is %d, want 429 (body: %s)", refused.Code, refused.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(refused.Body.Bytes(), &got); err != nil {
		t.Fatalf("the refusal is not the error envelope: %v", err)
	}
	if !strings.Contains(got["error"], "too many sign-in attempts") {
		t.Errorf("the refusal does not name the limit: %q", got["error"])
	}
	if len(minter.subjects) != 2 || len(auditFake.rows) != 2 {
		t.Errorf("%d mints and %d trail rows, want the allowance's two and no more: a refused burst signs nothing and trails nothing",
			len(minter.subjects), len(auditFake.rows))
	}
	// An unknown flow is refused by the table before the limit is asked,
	// so a burst of garbage cannot spend the allowance.
	if rsp := postSession(t, m, `{"flow":"nope"}`); rsp.Code != http.StatusBadRequest {
		t.Errorf("an unknown flow answered %d, want the table's 400", rsp.Code)
	}
	if len(minter.subjects) != 2 {
		t.Errorf("the minter saw %d subjects after an unknown flow, want no change", len(minter.subjects))
	}
}

// The issuer failing after the audit row: the answer is the 500 that says
// the session could not be minted, and the trail row the mint wrote stays,
// the record of an attempt that passed every check.
func TestSessionRefusesWhenTheIssuerFails(t *testing.T) {
	minter := &fakeMinter{err: errors.New("key gone")}
	m, _, auditFake := newTestMint(t, minter, nil)

	rsp := postSession(t, m, `{"flow":"sales"}`)
	if rsp.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500 (body: %s)", rsp.Code, rsp.Body.String())
	}
	if len(auditFake.rows) != 1 {
		t.Errorf("%d trail rows after a failed mint, want the one written before the token", len(auditFake.rows))
	}
}

// Role refusals spend the allowance, deliberately: the limit is asked
// before the role answer, so a broken world cannot be probed for free.
// Stated here so the semantics are a pin, not an accident.
func TestRoleRefusalsSpendTheAllowance(t *testing.T) {
	minter := &fakeMinter{token: "t"}
	m, _, _ := newTestMint(t, minter, nil)
	m.limit = NewAddressLimit(1, time.Second, nil)
	m.staff = &fakeStaff{ok: false}

	refused := postSession(t, m, `{"flow":"sales"}`)
	if refused.Code != http.StatusForbidden {
		t.Fatalf("status %d, want the role refusal's 403", refused.Code)
	}
	spent := postSession(t, m, `{"flow":"sales"}`)
	if spent.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d, want the limit's 429: the role refusal spent the one admission", spent.Code)
	}
	if len(minter.subjects) != 0 {
		t.Errorf("the minter saw %v, neither refusal reaches the issuer", minter.subjects)
	}
}

// Behind the box's own proxy (the demo image's Caddy on loopback) the
// limit keys on the first address of the forwarded chain, so two testers
// tapping cards together hold separate allowances and the box is not one
// bucket.
func TestLimitKeysTheForwardedChainBehindALoopbackPeer(t *testing.T) {
	minter := &fakeMinter{token: "t"}
	m, _, _ := newTestMint(t, minter, nil)
	m.limit = NewAddressLimit(2, time.Second, nil)

	for i := 0; i < 2; i++ {
		rsp := postSessionFrom(t, m, `{"flow":"sales"}`, "127.0.0.1:4444",
			map[string]string{"X-Forwarded-For": "198.51.100.1"})
		if rsp.Code != http.StatusOK {
			t.Fatalf("admission %d from the first tester: status %d, want 200", i+1, rsp.Code)
		}
	}
	if rsp := postSessionFrom(t, m, `{"flow":"sales"}`, "127.0.0.1:4444",
		map[string]string{"X-Forwarded-For": "198.51.100.1"}); rsp.Code != http.StatusTooManyRequests {
		t.Fatalf("the first tester's burst answered %d, want 429", rsp.Code)
	}
	if rsp := postSessionFrom(t, m, `{"flow":"sales"}`, "127.0.0.1:4444",
		map[string]string{"X-Forwarded-For": "198.51.100.2"}); rsp.Code != http.StatusOK {
		t.Fatalf("the second tester answered %d, want 200 from their own bucket", rsp.Code)
	}
	// A loopback peer with no forwarded chain still counts against
	// something: the proxy's own address, one bucket.
	direct := postSessionFrom(t, m, `{"flow":"sales"}`, "127.0.0.1:4445", nil)
	if direct.Code != http.StatusOK && direct.Code != http.StatusTooManyRequests {
		t.Fatalf("a direct loopback mint answered %d, want an answer from a real bucket", direct.Code)
	}
}

// From a peer that is not the box's proxy the forwarded header is
// untrusted and ignored: the connection's own address is the key, so one
// direct client cannot rotate buckets by rewriting it.
func TestLimitIgnoresTheForwardedHeaderFromANonLoopbackPeer(t *testing.T) {
	minter := &fakeMinter{token: "t"}
	m, _, _ := newTestMint(t, minter, nil)
	m.limit = NewAddressLimit(2, time.Second, nil)

	for i, fwd := range []string{"1.2.3.4", "5.6.7.8"} {
		if rsp := postSessionFrom(t, m, `{"flow":"sales"}`, "198.51.100.9:7777",
			map[string]string{"X-Forwarded-For": fwd}); rsp.Code != http.StatusOK {
			t.Fatalf("admission %d: status %d, want 200 inside the peer's allowance", i+1, rsp.Code)
		}
	}
	if rsp := postSessionFrom(t, m, `{"flow":"sales"}`, "198.51.100.9:7777",
		map[string]string{"X-Forwarded-For": "9.9.9.9"}); rsp.Code != http.StatusTooManyRequests {
		t.Fatalf("the third mint answered %d, want 429: the spoofed header bought no new bucket", rsp.Code)
	}
}
