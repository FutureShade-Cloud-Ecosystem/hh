// loadread.go is the customer module's read seam for the load doors that
// hang their rows under a customer (HH-LOAD-1 LD-6): the job writer in the
// portal module asks it whether a customer belongs to the machine
// principal's org, inside the frame's transaction, instead of reading the
// customers table itself. The read opens no connection and ends no
// transaction; its error wraps the statement's own (%w).
package customer

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// OrgReader answers customer membership questions on a caller's
// transaction.
type OrgReader struct{}

// CustomerOrgTx reads the customer row's org on the caller's
// transaction: found is false when the row is gone, orgID is empty when
// the row carries no org. The caller says the true thing in its refusal.
func (OrgReader) CustomerOrgTx(ctx context.Context, tx pgx.Tx, customerID string) (found bool, orgID string, err error) {
	var org *string
	err = tx.QueryRow(ctx, `SELECT org_id::text FROM customers WHERE id = $1::uuid`, customerID).Scan(&org)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return false, "", fmt.Errorf("customer: org read: %w", err)
	}
	if org != nil {
		orgID = *org
	}
	return true, orgID, nil
}
