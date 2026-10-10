package messaging

import (
	"context"
	"fmt"
)

// - DIB-463 persistence: the rollup roots of a day, folded -

// RollupTotalsForDay sums the day's rollup ROOTS across the payments channels a caller may see.
//
// - What this query deliberately does NOT touch -
//
// The `payments` table. Not once. The tile's number comes from msg_messages, the same rows the
// #payments channel renders, so the card and the channel cannot disagree. See payments_today.go for
// why that is the entire point of the ticket rather than an implementation detail.
//
// - The predicates, each doing one job -
//
//	event_key = $1              the DAY's identity (rollup.go rollupKey), the same string the writer
//	                            uses. One row per channel per day is guaranteed by the SHIPPED
//	                            uq_msg_messages_event_key, so this cannot double-count a day.
//	thread_root_id IS NULL      roots only. A reply is one payment inside the day; adding replies to
//	                            roots would count every payment twice.
//	deleted_at IS NULL          a removed day line is not a day.
//	slug = payments / payments-%  the DIB-449 channels (0152). Scoping by slug rather than by "every
//	                            channel that happens to hold a rollup" means a future rollup class in
//	                            another channel cannot silently inflate a tile that says "payments".
//	                            The payments family is reserved against caller founding (model.go),
//	                            so only system seeded rows can ever hold these slugs: a caller
//	                            founded room in the family is refused before the insert runs.
//	branch predicate            the caller's grants, plus the GLOBAL channel (branch_id IS NULL).
//
// COALESCE on the ref fields, not a cast that can fail: a root written before its first recompute
// carries count 0, and a malformed ref contributes nothing rather than erroring the whole tile.
func (r *Repository) RollupTotalsForDay(ctx context.Context, eventKey string, branchIDs []string, allBranches bool) (int, int64, int, error) {
	if r.pool == nil {
		return 0, 0, 0, errNoDB
	}
	if branchIDs == nil {
		branchIDs = []string{}
	}
	const q = `
SELECT COALESCE(sum(COALESCE((m.ref->>'count')::int, 0)), 0)         AS payments,
       COALESCE(sum(COALESCE((m.ref->>'amount_cents')::bigint, 0)), 0) AS cents,
       count(*)                                                       AS channels
FROM msg_messages m
JOIN msg_channels ch ON ch.id = m.channel_id
WHERE m.event_key = $1
  AND m.kind = 'EVENT'
  AND m.thread_root_id IS NULL
  AND m.deleted_at IS NULL
  AND (ch.slug = 'payments' OR ch.slug LIKE 'payments-%')
  AND ($2::boolean OR ch.branch_id IS NULL OR ch.branch_id = ANY($3::uuid[]))`

	var count, channels int
	var cents int64
	if err := r.pool.QueryRow(ctx, q, eventKey, allBranches, branchIDs).Scan(&count, &cents, &channels); err != nil {
		return 0, 0, 0, fmt.Errorf("rollup totals for day: %w", err)
	}
	return count, cents, channels, nil
}
