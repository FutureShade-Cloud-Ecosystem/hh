package adapters

// purchasing_policy.go, assist.PurchaseCostPolicies over the purchasing module's per branch policy: the same
// PolicyFor the purchasing screens' cost lens asks, which resolves the branch row, else the dealer default, and
// fails closed to MANAGER_UP. The assist handler turns the word into the claim's purchase cost branches.

import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
)

// PolicyLookup is what this adapter needs from the purchasing module.
type PolicyLookup interface {
	PolicyFor(ctx context.Context, branchID string) purchasing.Policy
}

type purchaseCostAdapter struct{ svc PolicyLookup }

var _ assist.PurchaseCostPolicies = purchaseCostAdapter{}

// NewPurchaseCostPolicy builds the seam. A nil service returns a nil policy: the handler then gives no caller
// any purchase cost.
func NewPurchaseCostPolicy(svc PolicyLookup) assist.PurchaseCostPolicies {
	if svc == nil {
		return nil
	}
	return purchaseCostAdapter{svc: svc}
}

func (a purchaseCostAdapter) CostVisibility(ctx context.Context, branchID string) string {
	return a.svc.PolicyFor(ctx, branchID).CostVisibility
}
