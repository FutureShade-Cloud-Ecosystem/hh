package gl

// rules_seam_tx.go, the transaction variant of rules_seam.go. A producer that already holds a transaction
// (purchasing PostBill holds the bill's from Begin to Commit) resolves its posting rules through that
// transaction, so it never asks the shared pool for a second connection while it holds the first. The pool
// variant in rules_seam.go is unchanged and stays the seam for callers with no transaction.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ResolveRuleTx is ResolveRule read through tx. Same resolution, same loud refusal when no active rule
// resolves the role; only the connection differs.
func (s *Service) ResolveRuleTx(ctx context.Context, tx pgx.Tx, eventType, lineRole, branchID string) (accountCode string, err error) {
	rules, err := s.store.PostingAccountsTx(ctx, tx, eventType, branchID)
	if err != nil {
		return "", err
	}
	code, ok := rules[lineRole]
	if !ok || code == "" {
		return "", fmt.Errorf("gl: no posting rule for %s/%s", eventType, lineRole)
	}
	return code, nil
}
