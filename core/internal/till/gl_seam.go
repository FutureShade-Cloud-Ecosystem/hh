package till

// gl_seam.go, the till's side of the GL's counter reason code seams (W2-6b).
//
// The till owns the list of counter reason codes (migration 0451) and the GL owns the journal. A till
// movement that names a code posts to the account that code pointed at when the movement was recorded,
// and the account travels with the movement (W2-6b fix round 3), so a repoint of the code never moves
// a movement that has not posted yet. The GL asks the till for the account of a movement recorded
// before that, and for the live codes that name an account, so no GL statement names a till table and
// the till's list stays the till's own.
//
// The reads are deliberately narrow: one row of the till's own list, by its id, on its own branch, and
// the live codes that name one account.

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
//
// It answers for a movement recorded before the account travelled with the movement (W2-6b fix round
// 3). A movement recorded since carries its own account, and the GL posts that one.
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

// ActiveCounterCodesForAccount answers the LIVE counter reason codes that name the account, with the
// branch each sits on. The chart's guard asks this before retiring an account (W2-6b fix round 3): a
// movement naming a live code posts to the account that code points at, so retiring the account under
// a live code would leave the drawer's money unposted.
//
// The read is narrow and it is the till's own table: the codes that are on and point at the account.
// A code that is off names no account the drawer can use, so it holds nothing back.
func (r *Repository) ActiveCounterCodesForAccount(ctx context.Context, accountCode string) ([]gl.CounterAccountUse, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	return activeCounterCodesForAccountOn(ctx, r.pool, accountCode)
}

// ActiveCounterCodesForAccountTx reads the LIVE counter reason codes that name the account on the
// caller's transaction (W2-6b fix round 4, P3-1), under the account row's lock.
func (r *Repository) ActiveCounterCodesForAccountTx(ctx context.Context, tx pgx.Tx, accountCode string) ([]gl.CounterAccountUse, error) {
	if tx == nil {
		return r.ActiveCounterCodesForAccount(ctx, accountCode)
	}
	return activeCounterCodesForAccountOn(ctx, tx, accountCode)
}

type counterCodeQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func activeCounterCodesForAccountOn(ctx context.Context, q counterCodeQuerier, accountCode string) ([]gl.CounterAccountUse, error) {
	accountCode = strings.TrimSpace(accountCode)
	if accountCode == "" {
		return nil, nil
	}
	rows, err := q.Query(ctx,
		`SELECT code, branch_id::text
		   FROM till_counter_reason_codes
		  WHERE active AND gl_account_code = $1
		  ORDER BY code, branch_id`, accountCode)
	if err != nil {
		return nil, fmt.Errorf("till: active counter codes for account: %w", err)
	}
	defer rows.Close()
	var out []gl.CounterAccountUse
	for rows.Next() {
		var u gl.CounterAccountUse
		if err := rows.Scan(&u.Code, &u.BranchID); err != nil {
			return nil, fmt.Errorf("till: scan the active counter code: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("till: active counter codes for account: %w", err)
	}
	return out, nil
}

// The till's repository is the GL's counter account use reader.
var _ gl.CounterAccountUseReader = (*Repository)(nil)
