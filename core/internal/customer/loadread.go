// loadread.go is the customer module's read seam for the load doors that
// hang their rows under a customer (HH-LOAD-1 LD-6): the job writer in the
// portal module asks it whether a customer belongs to the machine
// principal's org, inside the frame's transaction, instead of reading the
// customers table itself. The read opens no connection and ends no
// transaction; its error wraps the statement's own (%w).
package customer

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// OrgReader answers customer membership questions on a caller's
// transaction.
type OrgReader struct{}

// CustomerInOrgTx reports whether the customer row exists and carries the
// org the load stamped on it.
func (OrgReader) CustomerInOrgTx(ctx context.Context, tx pgx.Tx, customerID, orgID string) (bool, error) {
	var in bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM customers WHERE id = $1::uuid AND org_id = $2::uuid)`,
		customerID, orgID).Scan(&in); err != nil {
		return false, fmt.Errorf("customer: org membership read: %w", err)
	}
	return in, nil
}
