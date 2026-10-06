// StaffEmailReader is the load doors' read seam over existing staff
// rows (HH-LOAD-1 LD-4, the user map). Read only: no user is ever
// created by a door (D38, the plan's reading 13), the map records names
// against staff rows the provisioning program made and refuses an email
// that answers none. The users table stays this package's to read.
package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// StaffEmailReader answers through any pgx transaction the caller
// holds, so the read lands inside the caller's own transaction; the
// load frame hands its batch transaction and the read joins it.
type StaffEmailReader struct{}

// StaffRowByEmailTx reads one staff row's id by its verified login
// email inside the caller's transaction. Any live staff row answers,
// whatever its invitation state: the map records names against people
// that exist, and the invitation state is a login fact, not an identity
// fact. ok false with a nil error is a clean miss; the read itself
// never writes and never creates.
func (StaffEmailReader) StaffRowByEmailTx(ctx context.Context, tx pgx.Tx, email string) (userID string, ok bool, err error) {
	var id string
	err = tx.QueryRow(ctx,
		`SELECT id::text FROM users WHERE email = $1 AND deleted_at IS NULL`, email).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("identity: staff read by email: %w", err)
	}
	return id, true, nil
}
