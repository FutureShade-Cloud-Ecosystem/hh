package adapters

// account_position_policy.go, assist.AccountPositionPolicies over the receivables register's own policy read:
// billing's RegisterPolicyFor, which resolves the branch row, else the dealer default (an empty branch is the
// default). The assist handler turns the word into the claim's account position branches exactly as the register's
// accountAllowed does.

import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
)

// RegisterPolicyLookup is what this adapter needs from the billing module.
type RegisterPolicyLookup interface {
	RegisterPolicyFor(ctx context.Context, branchID string) (billing.RegisterPolicy, error)
}

type accountPositionAdapter struct{ repo RegisterPolicyLookup }

var _ assist.AccountPositionPolicies = accountPositionAdapter{}

// NewAccountPositionPolicy builds the seam. A nil repository returns a nil policy: the handler then gives no
// caller any account position.
func NewAccountPositionPolicy(repo RegisterPolicyLookup) assist.AccountPositionPolicies {
	if repo == nil {
		return nil
	}
	return accountPositionAdapter{repo: repo}
}

func (a accountPositionAdapter) AccountPositionVisibility(ctx context.Context, branchID string) (string, error) {
	p, err := a.repo.RegisterPolicyFor(ctx, branchID)
	if err != nil {
		return "", err
	}
	return p.AccountPositionVisibility, nil
}
