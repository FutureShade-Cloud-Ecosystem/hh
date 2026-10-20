package assist

import (
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
)

const (
	b1 = "11111111-1111-1111-1111-111111111111"
	b2 = "22222222-2222-2222-2222-222222222222"
)

func TestReadClaimsCarriesOnlyWhatTheResolvedCallerHolds(t *testing.T) {
	c := Caller{UserID: "u-1", RoleCode: "manager", Tier: 2, BranchIDs: []string{b1, b2}, PaymentTrail: true}
	got, err := c.ReadClaims()
	if err != nil {
		t.Fatal(err)
	}
	if got.Audience != readtx.AudienceStaff || got.AskerID != "u-1" || got.Global || got.CustomerID != "" {
		t.Errorf("identity fields wrong: %+v", got)
	}
	if strings.Join(got.BranchIDs, ",") != b1+","+b2 || !got.PaymentTrail {
		t.Errorf("scope wrong: %+v", got)
	}
	// Cost, margin, PII and channels come with the classification registry and
	// the messaging slice; until then they are empty, never guessed from a tier.
	if got.Margin || len(got.CostBranchIDs) != 0 || len(got.PIIClasses) != 0 || len(got.ChannelIDs) != 0 || len(got.Capabilities) != 0 {
		t.Errorf("a gated field was set: %+v", got)
	}
}

func TestReadClaimsCopiesTheBranchList(t *testing.T) {
	c := Caller{UserID: "u", Tier: 3, BranchIDs: []string{b1}}
	got, _ := c.ReadClaims()
	got.BranchIDs[0] = b2
	if c.BranchIDs[0] != b1 {
		t.Fatal("the claim aliases the caller's slice")
	}
}

func TestReadClaimsGlobalCallerKeepsItsFlag(t *testing.T) {
	got, err := (Caller{UserID: "u", Tier: 1, Global: true}).ReadClaims()
	if err != nil || !got.Global {
		t.Fatalf("global caller: %+v %v", got, err)
	}
}

func TestReadClaimsRefusesWhatCannotBeSigned(t *testing.T) {
	for name, c := range map[string]Caller{
		"no role":       {UserID: "u", Tier: 0},
		"no user":       {Tier: 2},
		"bad branch id": {UserID: "u", Tier: 2, BranchIDs: []string{"b1' OR '1'='1"}},
	} {
		if _, err := c.ReadClaims(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
