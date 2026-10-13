//go:build demo

package issuer

import (
	"context"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

const (
	testTenantAud = "hh-tenant"
	testAccount   = "de2c0000-0000-4000-9000-000000000001"
	testPortalSub = "de2c0000-0000-4000-9000-0000000000a1"
)

func tenantVerifier(t *testing.T, iss *Issuer) *identity.Verifier {
	t.Helper()
	v, err := identity.NewVerifier(identity.Config{
		KeySource:      iss.KeySource(),
		Issuer:         ClaimIssuer,
		StaffAudience:  "hh-staff",
		TenantAudience: testTenantAud,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

// A tenant token passes the unchanged verifier as a tenant bearer that
// carries the contractor account in the tenant claim, and the staff mint
// of the same issuer still answers a staff bearer with no tenant claim.
func TestMintTenantClassifiesAsTenantWithTheAccount(t *testing.T) {
	iss, err := New("hh-staff", 0, WithTenantAudience(testTenantAud))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	v := tenantVerifier(t, iss)
	tok, err := iss.MintTenant(testPortalSub, testAccount)
	if err != nil {
		t.Fatalf("MintTenant: %v", err)
	}
	got, err := v.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("the real verifier refused a tenant token: %v", err)
	}
	if got.Class != identity.ClassTenant || got.Subject != testPortalSub || got.TenantID != testAccount {
		t.Fatalf("token = %+v, want tenant class, subject %s, tenant %s", got, testPortalSub, testAccount)
	}
	staffTok, err := iss.Mint("de110000-0000-4000-9000-000000000001")
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	st, err := v.Verify(context.Background(), staffTok)
	if err != nil {
		t.Fatalf("Verify staff: %v", err)
	}
	if st.Class != identity.ClassStaff || st.TenantID != "" {
		t.Fatalf("staff token = %+v, want staff class and no tenant", st)
	}
	claims := decodePart(t, strings.Split(staffTok, ".")[1])
	if strings.Contains(claims, `"tenant"`) {
		t.Fatalf("a staff token carries a tenant claim: %s", claims)
	}
}

// The tenant mint refuses every shape that would not be a tenant bearer.
func TestMintTenantRefusals(t *testing.T) {
	plain, err := New("hh-staff", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := plain.MintTenant(testPortalSub, testAccount); err == nil {
		t.Fatal("an issuer with no tenant audience minted a tenant token")
	}
	iss, err := New("hh-staff", 0, WithTenantAudience(testTenantAud))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := iss.MintTenant("", testAccount); err == nil {
		t.Fatal("minted with no subject")
	}
	if _, err := iss.MintTenant(testPortalSub, ""); err == nil {
		t.Fatal("minted with no contractor account")
	}
}

// The two audiences must differ: one token can never claim both classes.
func TestTenantAudienceMustDifferFromStaff(t *testing.T) {
	if _, err := New("hh-staff", 0, WithTenantAudience("hh-staff")); err == nil {
		t.Fatal("New accepted the staff audience as the tenant audience")
	}
	if _, err := New("hh-staff", 0, WithTenantAudience("")); err == nil {
		t.Fatal("New accepted an empty tenant audience")
	}
}
