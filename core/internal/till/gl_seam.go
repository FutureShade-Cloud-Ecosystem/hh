package till

// gl_seam.go, the till's side of the GL's counter reason code seam (W2-6b).
//
// The till owns the list of counter reason codes (migration 0451) and the GL owns the journal. A till
// movement that names a code posts to the account that code points at, and the account resolves at post
// time from the movement's own code. The GL asks the till for it through this seam, so no GL statement
// names a till table and the till's list stays the till's own.
//
// The read is deliberately narrow: one row of the till's own list, by its id, on its own branch.

import (
	"context"
	"errors"
	"fmt"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/jackc/pgx/v5"
)

// CounterAccountCode answers the GL account the named counter reason code points at, for that branch.
//
// found=false means the code is not on that branch (it was never there, or it belongs to another
// branch), so the movement names no account and posts to suspense as it did before.
//
// The read does not ask whether the code is still live: a movement that named the code is a fact, and
// turning the code off afterwards must not move the money back to suspense.
func (r *Repository) CounterAccountCode(ctx context.Context, branchID, reasonCodeID string) (string, bool, error) {
	if r.pool == nil {
		return "", false, errNoDB
	}
	var account string
	err := r.pool.QueryRow(ctx,
		`SELECT gl_account_code
		   FROM till_counter_reason_codes
		  WHERE id = $1::uuid AND branch_id = $2::uuid`, reasonCodeID, branchID).Scan(&account)
	if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("till: counter account code: %w", err)
	}
	return account, true, nil
}

// The till's repository is the GL's counter account reader.
var _ gl.CounterAccountReader = (*Repository)(nil)
