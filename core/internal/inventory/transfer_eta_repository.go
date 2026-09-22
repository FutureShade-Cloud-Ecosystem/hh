package inventory

// transfer_eta_repository.go, the pgx half of DIB-472: the branch_transit_days reads and writes
// (0158), plus the eta binding the ship UPDATE takes.
//
// These live on *Repository and are called through s.repo, NOT through the `store` interface. That is
// the rule stock_lookup.go and incoming.go both state and follow: `store` is implemented by fakes in
// several test files here, so widening it is a breaking change dressed up as an addition. Nothing about
// this config read needs a DB-free fake, the arithmetic that does (gradeETA, defaultETA) is a pure
// function in transfer_eta.go and is unit-tested there.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// TransitDaysForPair reads ONE route's configured transit time inside the ship transaction.
//
// found=false is the answer for a pair nobody has configured, and it is an ANSWER, not an error, the
// DIB-443 posture, and the thing that makes "never invent a date" a straight line in the caller
// instead of an error branch that could be mishandled into a default.
//
// It reads through the ship transaction so the number used for the default is the number that was
// stored when the truck was loaded, an admin editing the route in the next second cannot change what
// this shipment promised.
func (r *Repository) TransitDaysForPair(ctx context.Context, tx pgx.Tx, fromBranchID, toBranchID string) (int, bool, error) {
	if r.pool == nil && tx == nil {
		return 0, false, errNoDB
	}
	var days int
	err := r.q(tx).QueryRow(ctx, `
		SELECT transit_days FROM branch_transit_days
		 WHERE from_branch_id = $1::uuid AND to_branch_id = $2::uuid`, fromBranchID, toBranchID).Scan(&days)
	if isNoRows(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("inventory: transit days for pair: %w", err)
	}
	return days, true, nil
}

// transitPairSelect is the admin surface's projection. It is built from a CROSS JOIN of the BRANCHES,
// not from the stored rows, so an unconfigured route is a row in the answer with configured=false -
// see TransitDaysConfig for why the gap has to be visible.
const transitPairSelect = `
	SELECT f.id::text, f.code::text, f.name, t.id::text, t.code::text, t.name,
	       COALESCE(d.transit_days, 0),
	       (d.from_branch_id IS NOT NULL),
	       COALESCE(to_char(d.updated_at, ` + tsFmt + `), '')
	  FROM locations f
	  CROSS JOIN locations t
	  LEFT JOIN branch_transit_days d
	         ON d.from_branch_id = f.id AND d.to_branch_id = t.id
	 WHERE f.loc_type = 'BRANCH' AND t.loc_type = 'BRANCH' AND f.id <> t.id`

func scanTransitPairs(rows pgx.Rows) ([]TransitDaysPair, error) {
	defer rows.Close()
	var out []TransitDaysPair
	for rows.Next() {
		var p TransitDaysPair
		if err := rows.Scan(&p.FromBranchID, &p.FromCode, &p.FromName,
			&p.ToBranchID, &p.ToCode, &p.ToName,
			&p.TransitDays, &p.Configured, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListTransitDays lists every ordered branch pair, ordered by code so the two legs of a route sit
// together and the list is stable rather than dependent on row order.
func (r *Repository) ListTransitDays(ctx context.Context) ([]TransitDaysPair, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, transitPairSelect+` ORDER BY f.code, t.code`)
	if err != nil {
		return nil, fmt.Errorf("inventory: list transit days: %w", err)
	}
	return scanTransitPairs(rows)
}

// TransitDaysPair reads ONE pair's admin projection, so a write can answer with exactly what the list
// would show. A pair with no row comes back configured=false, never errNotFound: the pair EXISTS (two
// branches exist), it just promises nothing, and answering 404 for "we do not commit to arrivals on
// this route" would be a fault dressed over an ordinary answer.
func (r *Repository) TransitDaysPair(ctx context.Context, fromBranchID, toBranchID string) (TransitDaysPair, error) {
	if r.pool == nil {
		return TransitDaysPair{}, errNoDB
	}
	rows, err := r.pool.Query(ctx, transitPairSelect+` AND f.id = $1::uuid AND t.id = $2::uuid`, fromBranchID, toBranchID)
	if err != nil {
		return TransitDaysPair{}, fmt.Errorf("inventory: transit days pair: %w", err)
	}
	list, err := scanTransitPairs(rows)
	if err != nil {
		return TransitDaysPair{}, err
	}
	if len(list) == 0 {
		// Two ids that are not both branches. That IS a caller mistake, and it is the one shape this
		// read cannot answer, so it is the one that gets a refusal.
		return TransitDaysPair{}, badReq("inventory: both ids must be branches, copy them from the branch list")
	}
	return list[0], nil
}

// UpsertTransitDays stores one route's transit time and stamps who reviewed it.
//
// updated_at is set explicitly on the UPDATE branch (the column DEFAULT only fires on INSERT), because
// the surface renders that date as "last reviewed" and an admin who re-confirms an unchanged number
// has reviewed it, the point of showing the date is to make staleness visible, and a re-confirmation
// that left yesterday's date on screen would defeat that.
func (r *Repository) UpsertTransitDays(ctx context.Context, fromBranchID, toBranchID string, days int, actorID string) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO branch_transit_days (from_branch_id, to_branch_id, transit_days, updated_by)
		VALUES ($1::uuid, $2::uuid, $3, $4)
		ON CONFLICT (from_branch_id, to_branch_id) DO UPDATE
		   SET transit_days = EXCLUDED.transit_days,
		       updated_by   = EXCLUDED.updated_by,
		       updated_at   = now()`,
		fromBranchID, toBranchID, days, nullStr(actorID))
	if err != nil {
		return fmt.Errorf("inventory: upsert transit days: %w", err)
	}
	return nil
}

// DeleteTransitDays removes a route's promise. Deleting a row that was never there is a no-op and not
// an error: the caller asked for "this route promises nothing" and that is now true.
func (r *Repository) DeleteTransitDays(ctx context.Context, fromBranchID, toBranchID string) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.pool.Exec(ctx, `
		DELETE FROM branch_transit_days
		 WHERE from_branch_id = $1::uuid AND to_branch_id = $2::uuid`, fromBranchID, toBranchID)
	if err != nil {
		return fmt.Errorf("inventory: clear transit days: %w", err)
	}
	return nil
}
