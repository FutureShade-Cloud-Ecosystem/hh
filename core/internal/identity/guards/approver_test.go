package guards

// approver_test.go, the manager's credential resolved through the same
// chain any caller passes: verification and org at the gate, authority
// from the staff source, the role's session ceiling. The refusals come
// back as the gate's sentinel families so the calling rule maps the same
// statuses the middleware writes, and no HTTP is written here.

import (
	"context"
	"crypto"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// approverFixture points the doubles at the approver's shape: a
// branch-scoped manager granted one branch.
func approverFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.staff.auth = identity.StaffAuthority{
		RoleID: "sales_manager", Tier: identity.TierManager, Scope: identity.ScopeBranch,
		Branches: []string{fxBranchA},
	}
	return f
}

// bearerHeader is the header value shape: the same Bearer scheme prefix as
// Authorization.
func bearerHeader(token string) string { return "Bearer " + token }

func TestResolveApprover_OK(t *testing.T) {
	f := approverFixture(t)
	credential := bearerHeader(f.staffToken(t, 10*time.Minute))

	approver, err := f.g.ResolveApprover(context.Background(), credential, fxOrgSlug)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if approver.RoleID != "sales_manager" || approver.PlatformUserID != fxSubject {
		t.Fatalf("wrong approver resolved: %+v", approver.StaffAuthority)
	}
	if approver.OrgID != fxOrgID || approver.OrgSlug != fxOrgSlug {
		t.Fatalf("the approver must resolve in the request org: %+v", approver.StaffScope)
	}
	if approver.IssuedAt.IsZero() {
		t.Fatal("the resolved scope must carry the credential's issue time, the window reads it")
	}
	if !approver.CanAccessBranch(fxBranchA) {
		t.Fatal("the branch grants must ride the resolved authority")
	}
}

func TestResolveApprover_NotABearerCredential(t *testing.T) {
	f := approverFixture(t)
	_, err := f.g.ResolveApprover(context.Background(), f.staffToken(t, 10*time.Minute), fxOrgSlug)
	if !errors.Is(err, authz.ErrSecondCredentialRefused) {
		t.Fatalf("a bare token without the scheme is refused: %v", err)
	}
}

func TestResolveApprover_TenantBearerRefused(t *testing.T) {
	f := approverFixture(t)
	_, err := f.g.ResolveApprover(context.Background(), bearerHeader(f.tenantToken(t)), fxOrgSlug)
	if !errors.Is(err, authz.ErrSecondCredentialRefused) {
		t.Fatalf("a tenant bearer is not a staff approver: %v", err)
	}
}

func TestResolveApprover_ExpiredRefused(t *testing.T) {
	f := approverFixture(t)
	now := time.Now()
	expired := f.mint(t, map[string]any{
		"iss": fxIssuer, "sub": fxSubject, "aud": fxStaffAud,
		"iat": now.Add(-2 * time.Hour).Unix(),
		"exp": now.Add(-time.Hour).Unix(),
	})
	_, err := f.g.ResolveApprover(context.Background(), bearerHeader(expired), fxOrgSlug)
	if !errors.Is(err, authz.ErrSecondCredentialRefused) {
		t.Fatalf("an expired credential is refused on the credential family: %v", err)
	}
}

func TestResolveApprover_NoMembershipOfTheRequestOrg(t *testing.T) {
	f := approverFixture(t)
	members := &fakeMembers{member: func(slug string) bool { return slug != fxOrgSlug }}
	verifier, err := identity.NewVerifier(identity.Config{
		KeySource: identity.NewStaticKeySource(map[string]crypto.PublicKey{
			fxKeyID: &f.keys.PublicKey,
		}),
		Issuer:         fxIssuer,
		StaffAudience:  fxStaffAud,
		TenantAudience: fxTenantAud,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	gate, err := authz.NewGate(verifier, members, &fakeOrgs{anchors: map[string]string{fxOrgSlug: fxOrgID}})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	g, err := NewGuards(gate, f.staff, f.portal, nil)
	if err != nil {
		t.Fatalf("NewGuards: %v", err)
	}
	_, err = g.ResolveApprover(context.Background(), bearerHeader(f.staffToken(t, 10*time.Minute)), fxOrgSlug)
	if !errors.Is(err, authz.ErrSecondCredentialForeign) {
		t.Fatalf("no membership of the request org is the foreign family: %v", err)
	}
}

func TestResolveApprover_NoGrantInTheOrg(t *testing.T) {
	f := approverFixture(t)
	f.staff.ok = false
	_, err := f.g.ResolveApprover(context.Background(), bearerHeader(f.staffToken(t, 10*time.Minute)), fxOrgSlug)
	if !errors.Is(err, authz.ErrSecondCredentialForeign) {
		t.Fatalf("no grant in the org is the foreign family: %v", err)
	}
}

func TestResolveApprover_InactiveAccountIsTheCredentialFamily(t *testing.T) {
	f := approverFixture(t)
	f.staff.err = identity.ErrStaffUserInactive
	_, err := f.g.ResolveApprover(context.Background(), bearerHeader(f.staffToken(t, 10*time.Minute)), fxOrgSlug)
	if !errors.Is(err, authz.ErrSecondCredentialRefused) {
		t.Fatalf("an inactive account is a credential-lifetime fact: %v", err)
	}
}

func TestResolveApprover_OverTheRoleCeiling(t *testing.T) {
	f := approverFixture(t)
	// A credential whose self-declared window exceeds the standard staff
	// ceiling (exp minus iat past the backstop every non-driver role gets)
	// is refused, the same ceiling any staff caller is held to.
	now := time.Now()
	overCeiling := f.mint(t, map[string]any{
		"iss": fxIssuer, "sub": fxSubject, "aud": fxStaffAud,
		"iat": now.Add(-13 * time.Hour).Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
	})
	_, err := f.g.ResolveApprover(context.Background(), bearerHeader(overCeiling), fxOrgSlug)
	if !errors.Is(err, authz.ErrSecondCredentialRefused) {
		t.Fatalf("a credential past the role's ceiling is refused: %v", err)
	}
}

func TestResolveApprover_EmptyCredentialRefused(t *testing.T) {
	f := approverFixture(t)
	_, err := f.g.ResolveApprover(context.Background(), "", fxOrgSlug)
	if !errors.Is(err, authz.ErrSecondCredentialRefused) {
		t.Fatalf("an absent credential is refused, never guessed: %v", err)
	}
	if _, err := f.g.ResolveApprover(context.Background(), bearerHeader(mintGarbage(t)), fxOrgSlug); !errors.Is(err, authz.ErrSecondCredentialRefused) {
		t.Fatalf("garbage is refused: %v", err)
	}
}

// mintGarbage is a structurally broken credential (not a JWS).
func mintGarbage(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return string(raw)
}
