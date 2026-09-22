package gl

// rules_seam.go, SCAFFOLD-owned, same-package GL read seam exposing posting-rule resolution to the W6
// cross-module producers (A-15 inventory counts resolve the 1200 inventory leg through here; A-16 landed cost
// and M6 holdback resolve their legs the same way). It is a THIN wrapper over the existing store.PostingAccounts
// so W6 modules never touch posting_rules directly. FROZEN signature, no existing gl file is edited.

import (
	"context"
	"fmt"
)

// ResolveRule returns the account_code a posting rule maps (eventType, lineRole) to for a branch. A branch
// override wins over the org-default (branchID "" = default only). It errors loudly when no active rule
// resolves the role, a producer must never post to a guessed account. Mirrors the pick() discipline the
// posting consumer already uses internally.
func (s *Service) ResolveRule(ctx context.Context, eventType, lineRole, branchID string) (accountCode string, err error) {
	rules, err := s.store.PostingAccounts(ctx, eventType, branchID)
	if err != nil {
		return "", err
	}
	code, ok := rules[lineRole]
	if !ok || code == "" {
		return "", fmt.Errorf("gl: no posting rule for %s/%s", eventType, lineRole)
	}
	return code, nil
}
