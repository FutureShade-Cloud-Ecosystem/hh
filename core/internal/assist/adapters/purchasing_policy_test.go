package adapters

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
)

type fakePolicy map[string]string

func (f fakePolicy) PolicyFor(_ context.Context, branchID string) purchasing.Policy {
	return purchasing.Policy{BranchID: branchID, CostVisibility: f[branchID]}
}

func TestPurchaseCostPolicyPassesTheModulesWordThrough(t *testing.T) {
	a := NewPurchaseCostPolicy(fakePolicy{"b1": "HIDDEN", "b2": "ALL_STAFF"})
	if got := a.CostVisibility(context.Background(), "b1"); got != "HIDDEN" {
		t.Errorf("b1 = %q", got)
	}
	if got := a.CostVisibility(context.Background(), "b2"); got != "ALL_STAFF" {
		t.Errorf("b2 = %q", got)
	}
	if NewPurchaseCostPolicy(nil) != nil {
		t.Error("a nil service must give a nil policy")
	}
}
