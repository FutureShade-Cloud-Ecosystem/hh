package inventory

// stock_value_seam.go, FW1-09d: the CONSOLIDATED stock value the GL's close pre flight reads. The pre
// flight ties the company's 1200 balance against the company's stock, so it asks for one figure: every
// branch's on hand at that branch's weighted average cost, read in ONE statement. The count sheet's own
// snapshot (count_seam.go) stays branch scoped; this one answers for the company as a whole.

import (
	"context"
)

// SnapshotOnHandAll is the consolidated freeze basis: the same on-hand and weighted average cost rows
// the branch-scoped snapshot reads, for EVERY branch, in one read through the pool. The month close
// holds no transaction, and the year end's pre flight reads stock on its own connection by design.
func (s *Service) SnapshotOnHandAll(ctx context.Context) ([]CountSnapshot, error) {
	return s.db().SnapshotOnHandAll(ctx, nil)
}
