package messaging

// role_vocabulary_test.go: the canonical role UserRoleCode answers is identity's own mapping, so a
// role identity learns is never a role this module reads back as a raw ERP code the cost rule
// refuses (PR 302 round 3, P3-3).

import (
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

func TestCanonicalRole_AgreesWithIdentityForEveryRoleTheRulesNamePlusTheCostList(t *testing.T) {
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range cfg.Cells() {
		seen[c.Role] = true
	}
	for _, r := range cfg.CostVisibility() {
		seen[r] = true
	}
	checked := 0
	for role := range seen {
		erp, ok := identity.ERPCodeFor(role)
		if !ok {
			continue
		}
		checked++
		if got := canonicalRole(erp); got != role {
			t.Errorf("canonicalRole(%q) = %q, identity maps it to %q", erp, got, role)
		}
	}
	if checked < 12 {
		t.Fatalf("only %d roles were checked, the rule vocabulary should name the staff roles", checked)
	}
	if got := canonicalRole("NO_SUCH_ROLE"); got != "NO_SUCH_ROLE" {
		t.Errorf("an unknown code reads back as it was read: %q", got)
	}
}
