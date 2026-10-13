package till

// secfix_test.go, the two before real data security items on the till's sign in paths, driven through
// the real guards and the real handler over the fake store:
//
//  1. A payment attempt is written under the branch the caller's authority and the request agree on,
//     never under a branch the client merely names. A branch the caller cannot reach is refused with
//     the shared 403 body, byte identical to every other role refusal.
//  2. The org default NSF policy is dealer wide money policy: only tier 1 global finance and general
//     roles (and the platform roles, which stand outside the matrix) write it. A tier 2 manager gets
//     the shared 403; the read stays manager wide.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

type secfixAuthorities struct {
	bySubject map[string]identity.StaffAuthority
	// extraRoles are further roles a subject holds beside the winner (the whole row the role gate walks).
	extraRoles map[string][]string
}

func (s secfixAuthorities) ResolveRoles(_ context.Context, subject, _ string) ([]string, error) {
	a, ok := s.bySubject[subject]
	if !ok {
		return []string{}, nil
	}
	return append([]string{a.RoleID}, s.extraRoles[subject]...), nil
}

func (s secfixAuthorities) ResolveStaff(_ context.Context, subject, _ string) (identity.StaffAuthority, bool, error) {
	a, ok := s.bySubject[subject]
	return a, ok, nil
}

type secfixNoPortal struct{}

func (secfixNoPortal) PortalRole(context.Context, string, string) (string, bool, error) {
	return "", false, nil
}

type secfixKnob struct{}

func (secfixKnob) DriverSessionHours(context.Context, string) (int, error) { return 12, nil }

const (
	secfixBranchA = "00000000-0000-4000-8000-0000000000a1"
	secfixBranchB = "00000000-0000-4000-8000-0000000000b2"
	secfixBranchC = "00000000-0000-4000-8000-0000000000c3"
)

// secfixRole is one decision row per staff_role id. The list is a hand copy of the sixteen rows migration
// 0215 inserts; a role added by a later migration is not seen here until a person adds its row.
type secfixRole struct {
	id    string
	tier  int
	scope string
}

var secfixRoles = []secfixRole{
	{"general_manager", identity.TierLeadership, identity.ScopeGlobal},
	{"financial_controller", identity.TierLeadership, identity.ScopeGlobal},
	{"hr", identity.TierLeadership, identity.ScopeGlobal},
	{"branch_manager", identity.TierManager, identity.ScopeBranch},
	{"sales_manager", identity.TierManager, identity.ScopeBranch},
	{"yard_manager", identity.TierManager, identity.ScopeBranch},
	{"logistics_manager", identity.TierManager, identity.ScopeBranch},
	{"procurement_manager", identity.TierManager, identity.ScopeBranch},
	{"payables_receivables", identity.TierManager, identity.ScopeGlobal},
	{"inside_sales", identity.TierFrontline, identity.ScopeBranch},
	{"outside_sales", identity.TierFrontline, identity.ScopeBranch},
	{"yard_team", identity.TierFrontline, identity.ScopeBranch},
	{"driver", identity.TierFrontline, identity.ScopeBranch},
	{"super_admin", identity.TierLeadership, identity.ScopeGlobal},
	{"dealer_admin", identity.TierLeadership, identity.ScopeGlobal},
	{"fb_operator", identity.TierLeadership, identity.ScopeGlobal},
}

func (r secfixRole) authority(branches ...string) identity.StaffAuthority {
	a := identity.StaffAuthority{RoleID: r.id, Tier: r.tier, Scope: r.scope}
	if r.scope == identity.ScopeBranch {
		a.Branches = branches
	}
	return a
}

type secfixFixture struct {
	mux     *http.ServeMux
	store   *fakeStore
	harness *testkit.Harness
	subject map[string]identity.StaffAuthority
	extra   map[string][]string
}

// newSecfixFixture serves the till routes behind the tier guards only: the tier layer.
func newSecfixFixture(t *testing.T) *secfixFixture { return buildSecfixFixture(t, false) }

// newSecfixGatedFixture serves them as production does: the role gate (default configuration) wraps the
// registration, so a request meets the role gate and then the tier guard.
func newSecfixGatedFixture(t *testing.T) *secfixFixture { return buildSecfixFixture(t, true) }

func buildSecfixFixture(t *testing.T, gated bool) *secfixFixture {
	t.Helper()
	store := newFakeStore()
	subjects := map[string]identity.StaffAuthority{}
	extra := map[string][]string{}
	auths := secfixAuthorities{bySubject: subjects, extraRoles: extra}
	harness := testkit.NewHarness(t)
	g, err := guards.NewGuards(harness.Gate, auths, secfixNoPortal{}, secfixKnob{})
	if err != nil {
		t.Fatalf("NewGuards: %v", err)
	}
	harness.Resolver.Set(testkit.OrgNorthSlug, testkit.OrgNorthID)
	svc := NewService(store)
	h := NewHandler(svc, nil).WithUserLinker(func(_ context.Context, sub string) (string, bool) { return "user-" + sub, true })
	mux := http.NewServeMux()
	var reg httpx.Registrar = mux
	if gated {
		cfg, err := roleconfig.Default()
		if err != nil {
			t.Fatalf("roleconfig.Default: %v", err)
		}
		rg, err := authz.NewRoleGate(harness.Gate, auths, cfg)
		if err != nil {
			t.Fatalf("NewRoleGate: %v", err)
		}
		wrapped, err := authz.NewRoleGateRegistrar(mux, rg)
		if err != nil {
			t.Fatalf("NewRoleGateRegistrar: %v", err)
		}
		reg = wrapped
	}
	h.RegisterRoutes(reg, g)
	return &secfixFixture{mux: mux, store: store, harness: harness, subject: subjects, extra: extra}
}

func (f *secfixFixture) person(sub string, a identity.StaffAuthority) {
	f.subject[sub] = a
	f.harness.Membership.SetMember(sub, testkit.OrgNorthSlug, true)
}

func (f *secfixFixture) do(t *testing.T, sub, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Authorization", "Bearer "+f.harness.Minter.StaffToken(t, sub))
	req.Header.Set("X-Org", testkit.OrgNorthSlug)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func secfixForbiddenBody(t *testing.T) string {
	t.Helper()
	rec := httptest.NewRecorder()
	authz.RefuseForbidden(rec)
	return rec.Body.String()
}

func secfixAttempt(key, branch string) PaymentAttempt {
	return PaymentAttempt{IdempotencyKey: key, AmountCents: 1500, Method: MethodCard, Processor: "p", Result: "DECLINED", BranchID: branch}
}

const secfixAttemptPath = "/api/v1/ar/payment-attempts"

func TestSecfix_AttemptBranchIsNeverTheClientsChoice(t *testing.T) {
	// Served as production serves it: role gate (default configuration), then the tier guard. The global
	// person is a platform role because the default matrix gives general_manager only read on counter-till
	// and financial_controller none, so the gate would refuse them before the branch rule is reached.
	f := newSecfixGatedFixture(t)
	forbidden := secfixForbiddenBody(t)
	branchRole := secfixRoles[3] // branch_manager
	f.person("scoped-a", branchRole.authority(secfixBranchA))
	f.person("clerk-a", secfixRoles[9].authority(secfixBranchA)) // inside_sales
	f.person("global", secfixRoles[13].authority())              // super_admin
	f.person("two-grants", branchRole.authority(secfixBranchA, secfixBranchB))

	// A branch scoped role in a second branch: refused with the shared 403, nothing written.
	for _, sub := range []string{"scoped-a", "clerk-a"} {
		rec := f.do(t, sub, http.MethodPost, secfixAttemptPath, secfixAttempt("k-"+sub+"-other", secfixBranchB))
		if rec.Code != http.StatusForbidden || rec.Body.String() != forbidden {
			t.Errorf("%s naming a branch it cannot reach: got %d %q, want the shared 403 %q", sub, rec.Code, rec.Body.String(), forbidden)
		}
	}
	if len(f.store.attempts) != 0 {
		t.Fatalf("a refused attempt must write nothing, got %d rows", len(f.store.attempts))
	}

	// Its own branch is written as named.
	if rec := f.do(t, "scoped-a", http.MethodPost, secfixAttemptPath, secfixAttempt("k-own", secfixBranchA)); rec.Code != http.StatusCreated {
		t.Fatalf("own branch: got %d %s", rec.Code, rec.Body.String())
	}
	// A branch scoped caller with one grant who names none is written under the grant, never branchless.
	if rec := f.do(t, "scoped-a", http.MethodPost, secfixAttemptPath, secfixAttempt("k-empty", "")); rec.Code != http.StatusCreated {
		t.Fatalf("one grant, no branch named: got %d %s", rec.Code, rec.Body.String())
	}
	// A global role may name any branch, or none (the attempt is then org wide: a NULL branch).
	if rec := f.do(t, "global", http.MethodPost, secfixAttemptPath, secfixAttempt("k-global", secfixBranchC)); rec.Code != http.StatusCreated {
		t.Fatalf("global naming a branch: got %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(t, "global", http.MethodPost, secfixAttemptPath, secfixAttempt("k-global-none", "")); rec.Code != http.StatusCreated {
		t.Fatalf("global naming no branch: got %d %s", rec.Code, rec.Body.String())
	}
	// The same person with two grants: both reachable, the third refused, none named is ambiguous (422).
	if rec := f.do(t, "two-grants", http.MethodPost, secfixAttemptPath, secfixAttempt("k-two-b", secfixBranchB)); rec.Code != http.StatusCreated {
		t.Fatalf("two grants, second branch: got %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(t, "two-grants", http.MethodPost, secfixAttemptPath, secfixAttempt("k-two-c", secfixBranchC)); rec.Code != http.StatusForbidden || rec.Body.String() != forbidden {
		t.Fatalf("two grants, third branch: got %d %q, want the shared 403", rec.Code, rec.Body.String())
	}
	if rec := f.do(t, "two-grants", http.MethodPost, secfixAttemptPath, secfixAttempt("k-two-none", "")); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("two grants, no branch named: got %d %s, want 422", rec.Code, rec.Body.String())
	}

	want := map[string]string{"k-own": secfixBranchA, "k-empty": secfixBranchA, "k-global": secfixBranchC, "k-global-none": "", "k-two-b": secfixBranchB}
	if len(f.store.attempts) != len(want) {
		t.Fatalf("rows written: got %d want %d", len(f.store.attempts), len(want))
	}
	for _, a := range f.store.attempts {
		if want[a.IdempotencyKey] != a.BranchID {
			t.Errorf("attempt %s written under branch %q, want %q", a.IdempotencyKey, a.BranchID, want[a.IdempotencyKey])
		}
	}
}

// nsfWritersServed is who writes the org default NSF policy as production serves it: the route is
// requireAdmin (tier 1 global) with x-access money-desk manage, and the default matrix gives manage or
// above to general_manager (manage) and financial_controller (admin); the platform roles pass the role
// gate's level check. Every tier 2 role and hr is refused with the shared 403.
var nsfWritersServed = map[string]bool{
	"general_manager": true, "financial_controller": true,
	"super_admin": true, "dealer_admin": true, "fb_operator": true,
}

// nsfReadersServed is who reads it as served: tier 1 or 2 with a money-desk cell of read or above.
var nsfReadersServed = map[string]bool{
	"general_manager": true, "financial_controller": true, "payables_receivables": true,
	"branch_manager": true, "sales_manager": true, "yard_manager": true, "procurement_manager": true,
	"super_admin": true, "dealer_admin": true, "fb_operator": true,
}

const secfixNSFPath = "/api/v1/ar/nsf-policy"

func TestSecfix_NSFPolicyWriteSweepServed(t *testing.T) {
	f := newSecfixGatedFixture(t)
	forbidden := secfixForbiddenBody(t)
	body := NSFPolicy{FeeCents: 9900, FeeAccountCode: "4200"}
	pairs := 0
	for _, r := range secfixRoles {
		sub := "role-" + r.id
		f.person(sub, r.authority(secfixBranchA))
		put := f.do(t, sub, http.MethodPut, secfixNSFPath, body)
		if nsfWritersServed[r.id] {
			if put.Code != http.StatusOK {
				t.Errorf("%s writes the NSF policy: got %d %s", r.id, put.Code, put.Body.String())
			}
		} else if put.Code != http.StatusForbidden || put.Body.String() != forbidden {
			t.Errorf("%s must get the shared 403 on the NSF policy write: got %d %q", r.id, put.Code, put.Body.String())
		}
		get := f.do(t, sub, http.MethodGet, secfixNSFPath, nil)
		if nsfReadersServed[r.id] != (get.Code == http.StatusOK) {
			t.Errorf("%s NSF policy read as served: got %d, admitted want %v", r.id, get.Code, nsfReadersServed[r.id])
		}
		if !nsfReadersServed[r.id] && (get.Code != http.StatusForbidden || get.Body.String() != forbidden) {
			t.Errorf("%s read refusal must be the shared 403: got %d %q", r.id, get.Code, get.Body.String())
		}
		pairs += 2
	}
	t.Logf("served NSF sweep: %d roles, %d role by route pairs", len(secfixRoles), pairs)
}

// The tier layer alone (no role gate): requireAdmin admits every tier 1 global role, so the matrix is what
// narrows the write in production; no tier 2 role and no tier 3 role passes it whatever a dealer configures.
func TestSecfix_NSFPolicyWriteTierLayer(t *testing.T) {
	f := newSecfixFixture(t)
	forbidden := secfixForbiddenBody(t)
	for _, r := range secfixRoles {
		sub := "role-" + r.id
		f.person(sub, r.authority(secfixBranchA))
		put := f.do(t, sub, http.MethodPut, secfixNSFPath, NSFPolicy{FeeCents: 9900, FeeAccountCode: "4200"})
		tier1Global := r.tier == identity.TierLeadership && r.scope == identity.ScopeGlobal
		if tier1Global && put.Code != http.StatusOK {
			t.Errorf("%s (tier 1 global) passes the tier guard: got %d %s", r.id, put.Code, put.Body.String())
		}
		if !tier1Global && (put.Code != http.StatusForbidden || put.Body.String() != forbidden) {
			t.Errorf("%s must get the shared 403 at the tier layer: got %d %q", r.id, put.Code, put.Body.String())
		}
		// The read at the tier layer: every tier 1 and tier 2 role passes the manager guard.
		get := f.do(t, sub, http.MethodGet, secfixNSFPath, nil)
		if (r.tier <= identity.TierManager) != (get.Code == http.StatusOK) {
			t.Errorf("%s tier layer read: got %d", r.id, get.Code)
		}
	}
}

// A person holding hr and super_admin resolves to hr as the winner (tier, then id), but the role gate walks
// the whole row (D48 X10) and admits on the platform role; the tier guard admits tier 1 global.
func TestSecfix_NSFPolicyWriteHoldsHrAndPlatformRole(t *testing.T) {
	f := newSecfixGatedFixture(t)
	f.person("hr-and-platform", secfixRoles[2].authority())
	f.extra["hr-and-platform"] = []string{"super_admin"}
	if rec := f.do(t, "hr-and-platform", http.MethodPut, secfixNSFPath, NSFPolicy{FeeCents: 100, FeeAccountCode: "4200"}); rec.Code != http.StatusOK {
		t.Fatalf("hr beside super_admin: got %d %s, want 200", rec.Code, rec.Body.String())
	}
	f.person("hr-only", secfixRoles[2].authority())
	if rec := f.do(t, "hr-only", http.MethodPut, secfixNSFPath, NSFPolicy{FeeCents: 100, FeeAccountCode: "4200"}); rec.Code != http.StatusForbidden {
		t.Fatalf("hr alone: got %d, want 403", rec.Code)
	}
}

// A global caller reaches every branch, so the reach check cannot catch a branch that is no location: the
// service answers 422 for a malformed id instead of letting the uuid cast fail as a 500.
func TestSecfix_AttemptBranchMustBeAUUID(t *testing.T) {
	f := newSecfixGatedFixture(t)
	f.person("global", secfixRoles[13].authority()) // super_admin
	for _, bad := range []string{"not-a-uuid", "00000000-0000-4000-8000-00000000000g", secfixBranchA + "0"} {
		rec := f.do(t, "global", http.MethodPost, secfixAttemptPath, secfixAttempt("k-bad", bad))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("branch %q: got %d %s, want 422", bad, rec.Code, rec.Body.String())
		}
	}
	if len(f.store.attempts) != 0 {
		t.Fatalf("a refused attempt must write nothing, got %d rows", len(f.store.attempts))
	}
}
