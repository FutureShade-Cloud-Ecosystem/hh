package inventory

// stock_value_seam.go, FW1-09d: the CONSOLIDATED stock value the GL's close pre flight reads. The pre
// flight ties the company's 1200 balance against the company's stock, so it asks for one figure: every
// branch's on hand at that branch's weighted average cost, read in ONE statement. The count sheet's own
// snapshot (count_seam.go) stays branch scoped; this one answers for the company as a whole.

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// SnapshotOnHandAll is the consolidated freeze basis: the same on-hand and weighted average cost rows
// the branch-scoped snapshot reads, for EVERY branch, in one read. A caller that holds a transaction
// rides it through SnapshotOnHandAllTx; a caller that does not reads through the pool.
func (s *Service) SnapshotOnHandAll(ctx context.Context) ([]CountSnapshot, error) {
	return s.db().SnapshotOnHandAll(ctx, nil)
}

// SnapshotOnHandAllTx is SnapshotOnHandAll inside the caller's transaction, so a caller that already
// holds a connection reads through it rather than asking the shared pool for a second one (nil tx reads
// the pool).
func (s *Service) SnapshotOnHandAllTx(ctx context.Context, tx pgx.Tx) ([]CountSnapshot, error) {
	return s.db().SnapshotOnHandAll(ctx, tx)
}
