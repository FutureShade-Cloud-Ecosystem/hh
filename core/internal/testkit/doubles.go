package testkit

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// ErrDependencyDown is the double's operational failure. An erroring double
// is a 503 leg, never a 403 and never a 401 (B2 review notes, item 11): an
// operational inability is not a verdict on the caller.
var ErrDependencyDown = errors.New("testkit: dependency double is down")

// MembershipDouble is the D28 Team-membership double (QA finding F4). It
// implements authz.MembershipSource. Answers are set per platform user and
// org slug and can be flipped between calls, proving per-request
// resolution; both methods count calls; both can be made to error, which is
// the 503 leg. It holds membership answers only: no field here could hold
// a credential, by design.
type MembershipDouble struct {
	mu        sync.Mutex
	member    map[string]map[string]bool
	tenants   map[string][]string
	memberErr error
	tenantsEr error
	memberN   int
	tenantsN  int
}

// NewMembershipDouble returns an empty double: nobody is a member, no
// tenant teams exist. Sweeps grant exactly what a leg needs.
func NewMembershipDouble() *MembershipDouble {
	return &MembershipDouble{
		member:  make(map[string]map[string]bool),
		tenants: make(map[string][]string),
	}
}

// SetMember records the OrgMember answer for one platform user and slug.
func (m *MembershipDouble) SetMember(platformUserID, orgSlug string, member bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.member[platformUserID] == nil {
		m.member[platformUserID] = make(map[string]bool)
	}
	m.member[platformUserID][orgSlug] = member
}

// SetTenantOrgs records the TenantOrgs answer for one platform user. More
// than one slug is the fail-closed tenant-shape leg (one org per tenant
// account); zero slugs is the no-tenant-team leg.
func (m *MembershipDouble) SetTenantOrgs(platformUserID string, orgSlugs ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tenants[platformUserID] = orgSlugs
}

// MemberError makes every OrgMember call fail (the 503 leg). A nil error
// restores normal answers.
func (m *MembershipDouble) MemberError(err error) {
	m.mu.Lock()
	m.memberErr = err
	m.mu.Unlock()
}

// TenantOrgsError makes every TenantOrgs call fail. Nil restores.
func (m *MembershipDouble) TenantOrgsError(err error) {
	m.mu.Lock()
	m.tenantsEr = err
	m.mu.Unlock()
}

// Counts reports OrgMember and TenantOrgs call counts. The resolver-side
// statement counting is the pgx tracer's job; these counters stand in for
// the platform-side calls, which touch no core table (ADR-0020).
func (m *MembershipDouble) Counts() (member, tenants int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.memberN, m.tenantsN
}

// OrgMember implements authz.MembershipSource.
func (m *MembershipDouble) OrgMember(_ context.Context, platformUserID, orgSlug string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.memberN++
	if m.memberErr != nil {
		return false, m.memberErr
	}
	return m.member[platformUserID][orgSlug], nil
}

// TenantOrgs implements authz.MembershipSource.
func (m *MembershipDouble) TenantOrgs(_ context.Context, platformUserID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tenantsN++
	if m.tenantsEr != nil {
		return nil, m.tenantsEr
	}
	return append([]string(nil), m.tenants[platformUserID]...), nil
}

// ResolverDouble is the org anchor resolver double. It implements
// authz.OrgResolver, the only core-database seam the middleware holds, so
// its call count is the middleware-leg stand-in for the statement count:
// zero on every refusal, exactly one on the provisioning gap (membership
// passed, anchor row missing, B2 review notes item 12).
type ResolverDouble struct {
	mu    sync.Mutex
	slug  map[string]string
	err   error
	calls int
}

// NewResolverDouble returns a resolver that resolves nothing.
func NewResolverDouble() *ResolverDouble {
	return &ResolverDouble{slug: make(map[string]string)}
}

// Set resolves a slug to an org id.
func (r *ResolverDouble) Set(orgSlug, orgID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.slug[orgSlug] = orgID
}

// Missing marks a slug as membership-passed but anchor-missing: the
// provisioning gap, which must perform exactly one anchor read and refuse
// with the shared body. The call count is what proves the gap's single
// read against the zero of every other refusal.
func (r *ResolverDouble) Missing(orgSlug string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.slug, orgSlug)
}

// Error makes every call fail (the 503 leg). Nil restores.
func (r *ResolverDouble) Error(err error) {
	r.mu.Lock()
	r.err = err
	r.mu.Unlock()
}

// Calls reports the anchor-read count.
func (r *ResolverDouble) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// ResolveSlug implements authz.OrgResolver.
func (r *ResolverDouble) ResolveSlug(_ context.Context, orgSlug string) (string, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.err != nil {
		return "", false, r.err
	}
	id, ok := r.slug[orgSlug]
	return id, ok, nil
}

// Harness wires the B2 collaborators over kit parts: the verifier against
// the key server (remote JWKS form), the membership and resolver doubles,
// and the authz Gate. Sweeps wrap Recorder handlers in the gate legs and
// drive requests with Do.
type Harness struct {
	Minter     *Minter
	Keys       *Keys
	Server     *KeyServer
	Clock      *Clock
	Verifier   *identity.Verifier
	Membership *MembershipDouble
	Resolver   *ResolverDouble
	Gate       *authz.Gate
}

// NewHarness builds the full wiring. It registers its own cleanup with the
// test. The verifier takes the served JWKS URL, never static key material,
// because the sweeps' claim is about the configured-source form (F3).
func NewHarness(t *testing.T) *Harness {
	t.Helper()
	m := NewMinter(t)
	server := NewKeyServer(t, m.Keys)
	verifier, err := identity.NewVerifier(m.VerifierConfig(server))
	if err != nil {
		t.Fatalf("testkit: NewVerifier: %v", err)
	}
	members := NewMembershipDouble()
	resolver := NewResolverDouble()
	gate, err := authz.NewGate(verifier, members, resolver)
	if err != nil {
		t.Fatalf("testkit: NewGate: %v", err)
	}
	return &Harness{
		Minter:     m,
		Keys:       m.Keys,
		Server:     server,
		Clock:      m.Clock,
		Verifier:   verifier,
		Membership: members,
		Resolver:   resolver,
		Gate:       gate,
	}
}

// WireFixtureOrgs points the doubles at the fixture database's org facts.
// The two-org principal deliberately holds Team membership in the north org
// only while its grants live in both orgs (the database builder plants
// both), so presenting the south org is the wrong-one-of-two leg: refused
// at membership, zero anchor reads, exactly the SW-4 shape. Tenant
// principals hold exactly one tenant team each. The suspended staff
// principal and the inactive portal principal hold normal memberships on
// purpose: their legs are decided at authority and role resolution (the
// account-status refusals), never at the org gate. Sweeps that need other
// wrong-org shapes change the doubles after this call; the fixture grants
// themselves live in the database builder.
func (h *Harness) WireFixtureOrgs(t *testing.T) {
	t.Helper()
	h.Membership.SetMember(SubInsideSales, OrgNorthSlug, true)
	h.Membership.SetMember(SubSalesManager, OrgNorthSlug, true)
	h.Membership.SetMember(SubGeneralMgr, OrgNorthSlug, true)
	h.Membership.SetMember(SubTwoOrg, OrgNorthSlug, true)
	h.Membership.SetMember(SubRevoked, OrgNorthSlug, true)
	h.Membership.SetMember(SubInert, OrgNorthSlug, true)
	h.Membership.SetMember(SubDriver, OrgNorthSlug, true)
	h.Membership.SetMember(SubSuspended, OrgNorthSlug, true)
	h.Membership.SetTenantOrgs(SubPortalAdminAlpha, OrgNorthSlug)
	h.Membership.SetTenantOrgs(SubPortalCrewAlpha, OrgNorthSlug)
	h.Membership.SetTenantOrgs(SubPortalBuyerNoPayAlpha, OrgNorthSlug)
	h.Membership.SetTenantOrgs(SubPortalAdminBeta, OrgNorthSlug)
	h.Membership.SetTenantOrgs(SubPortalCrewBeta, OrgNorthSlug)
	h.Membership.SetTenantOrgs(SubPortalBuyerBeta, OrgNorthSlug)
	h.Membership.SetTenantOrgs(SubPortalAdminGamma, OrgSouthSlug)
	h.Membership.SetTenantOrgs(SubPortalApGamma, OrgSouthSlug)
	h.Membership.SetTenantOrgs(SubPortalInactiveAlpha, OrgNorthSlug)
	h.Resolver.Set(OrgNorthSlug, OrgNorthID)
	h.Resolver.Set(OrgSouthSlug, OrgSouthID)
}
