package inventory

// branch_name_seam.go, where the browse row learns the NAMES of the branches it is out of (change
// 5, slice S4, ruling R1's Sales stock lookup). The row is a roll-up over every branch, so a list
// line that says "Out at <branch>" needs the names on the row itself; the per branch read is one
// call per product and a lookup list is one line per product.
//
// CONSUMER-DECLARED, like ShortOrderReader beside it: inventory states the question, the branches
// module (the owner of the branch rows) answers it from its own table, and a composition-root
// adapter binds the two. The browse query aggregates the branch IDS of the balances that show
// nothing available, from stock_balances, this module's own table; the ids cross the seam and come
// back as names. Primitive types only, so neither module imports the other's.
//
// The field is absent, never empty: no seam bound, or the reader named none of the row's branches,
// and out_at_branches does not ride. An unbound seam costs the row nothing; the row still says how
// much is available, and the caller's own words say out.

import "context"

// BranchNameReader is the CONSUMER-DECLARED SEAM for the out-at names. The branches module
// implements it over its own table; a composition-root adapter binds it. The reply maps branch id
// to display name; an id that names no branch of this dealer's is absent from the reply.
type BranchNameReader interface {
	BranchNames(ctx context.Context, branchIDs []string) (map[string]string, error)
}

// WithBranchNameReader injects the branches module's seam.
func (s *Service) WithBranchNameReader(r BranchNameReader) *Service { s.branchNames = r; return s }

// attachOutAtBranches trades the rows' out-at branch ids for names through the seam. An unbound
// seam leaves the rows without the field, the shortfall numbers' posture. A bound seam that FAILS
// is an error, not a silent gap: a list whose rows carry no names reads "out of stock" and a
// salesperson would send a customer to a yard the product is not at.
func (s *Service) attachOutAtBranches(ctx context.Context, rows []BrowseRow) error {
	ids := make([]string, 0, len(rows))
	for i := range rows {
		if len(rows[i].outAtBranchIDs) > 0 {
			ids = append(ids, rows[i].outAtBranchIDs...)
		}
	}
	if s.branchNames == nil || len(ids) == 0 {
		for i := range rows {
			rows[i].outAtBranchIDs = nil
		}
		return nil
	}
	names, err := s.branchNames.BranchNames(ctx, ids)
	if err != nil {
		return err
	}
	for i := range rows {
		row := &rows[i]
		named := make([]string, 0, len(row.outAtBranchIDs))
		for _, id := range row.outAtBranchIDs {
			if name := names[id]; name != "" {
				named = append(named, name)
			}
		}
		row.outAtBranchIDs = nil
		if len(named) > 0 {
			row.OutAtBranches = named
		}
	}
	return nil
}
