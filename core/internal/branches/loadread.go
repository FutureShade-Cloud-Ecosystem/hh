// BranchCodeReader is the load doors' read seam over the branch rows
// (HH-LOAD-1 LD-4, the branch map). It is read only, the same posture
// the whole package carries: the map records names against BRANCH rows
// that already exist and creates nothing, and the branches module stays
// the one place the locations table is read for them.
package branches

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// BranchCodeReader answers through any pgx transaction the caller
// holds, so the read lands inside the caller's own transaction; the
// load frame hands its batch transaction and the read joins it.
type BranchCodeReader struct{}

// BranchRowByCodeTx reads one live BRANCH row's id by its yard code
// inside the caller's transaction. ok false with a nil error is a clean
// miss: the code names no live branch row, which the caller refuses;
// the read itself never writes and never creates.
func (BranchCodeReader) BranchRowByCodeTx(ctx context.Context, tx pgx.Tx, code string) (branchID string, ok bool, err error) {
	var id string
	err = tx.QueryRow(ctx,
		`SELECT id::text FROM locations
		 WHERE code = $1 AND loc_type = 'BRANCH' AND deleted_at IS NULL`, code).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("branches: branch read by code: %w", err)
	}
	return id, true, nil
}
