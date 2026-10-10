package inventory

// draft_po_seam.go: where the worklist row learns of a draft purchase order a buyer has started for
// it. A draft never covers a shortfall (the principal's ruling, row 1303): the on order leg counts
// placed orders only (on_order_seam.go), so a row stays on the worklist while its draft sits unplaced.
// What the row gains is a pointer, draft_po_id and draft_po_no, so a screen can say "drafted, not
// placed" with the draft's link instead of inviting a second draft. Placing the draft is what takes
// the row off the worklist, through the on order leg.
//
// CONSUMER-DECLARED, like OnOrderReader beside it: inventory states the question, purchasing
// answers from its own tables, and a main.go adapter binds the two, primitive-only types so neither
// module imports the other's.

import "context"

// DraftPO is the newest unplaced draft purchase order with an outstanding line for one product at
// one branch.
type DraftPO struct {
	ProductID string
	BranchID  string
	POID      string
	PONo      string
}

// DraftPOReader is the CONSUMER-DECLARED SEAM for the draft pointer. branchID "" means every branch;
// productIDs empty means every product with a draft.
type DraftPOReader interface {
	OpenDraftsFor(ctx context.Context, branchID string, productIDs []string) ([]DraftPO, error)
}

// WithDraftPOReader injects the purchasing seam.
func (s *Service) WithDraftPOReader(r DraftPOReader) *Service { s.draftPOs = r; return s }

// attachDrafts stamps each row with the draft the seam names for its (product, branch). An unbound
// seam leaves the rows as they are: the pointer is a convenience on top of a worklist that is
// already right without it. A bound seam that FAILS is an error, not a silent gap: a row with no
// pointer reads "nothing drafted", and the buyer would draft a second order for it.
func (s *Service) attachDrafts(ctx context.Context, reach BranchReach, branchID string, rows []ShortfallRow) error {
	if s.draftPOs == nil || len(rows) == 0 {
		return nil
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ProductID)
	}
	drafts, err := s.draftPOs.OpenDraftsFor(ctx, branchID, ids)
	if err != nil {
		return err
	}
	type key struct{ product, branch string }
	byRow := make(map[key]DraftPO, len(drafts))
	for _, d := range drafts {
		// The pointer read carries the caller's reach too: a draft at a branch outside it is never
		// matched, whatever the seam answered for an all-branches question.
		if !reach.covers(d.BranchID) {
			continue
		}
		byRow[key{d.ProductID, d.BranchID}] = d
	}
	for i := range rows {
		if d, ok := byRow[key{rows[i].ProductID, rows[i].BranchID}]; ok {
			rows[i].DraftPOID, rows[i].DraftPONo = d.POID, d.PONo
		}
	}
	return nil
}
