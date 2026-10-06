// audit_resolvers.go adapts the owning modules' branch reads to the audit reopen fence's narrow
// resolver seam (HH-180). The adapters live at the composition root; the audit module knows the
// interface, the owners know the reads, and neither knows the other. Not-found maps to found=false
// so the reopen read answers an empty trail, never an error and never an existence signal, exactly
// like an unknown id.
package main

import (
	"context"
	"errors"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
)

// auditOrderBranchResolver resolves an order's owning branch for the reopen fence.
type auditOrderBranchResolver struct {
	repo *order.Repository
}

// BranchOf implements audit.EntityBranchResolver over order.Repository.OrderBranch, the same read
// the order history surfaces branch-scope on.
func (a auditOrderBranchResolver) BranchOf(ctx context.Context, entityID string) (string, bool, error) {
	branchID, err := a.repo.OrderBranch(ctx, entityID)
	if errors.Is(err, order.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return branchID, true, nil
}

// auditQuoteBranchResolver resolves a quote's owning branch for the reopen fence.
type auditQuoteBranchResolver struct {
	repo *quote.Repository
}

// BranchOf implements audit.EntityBranchResolver over quote.Repository.QuoteEditHeader, the same
// read the quote edit history branch-scopes on.
func (a auditQuoteBranchResolver) BranchOf(ctx context.Context, entityID string) (string, bool, error) {
	_, branchID, _, _, err := a.repo.QuoteEditHeader(ctx, entityID)
	if errors.Is(err, quote.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return branchID, true, nil
}
