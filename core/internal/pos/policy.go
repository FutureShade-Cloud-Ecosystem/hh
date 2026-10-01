package pos

// policy.go, the W7 pos_policy surface (branch row overrides the org NULL row; till_policy precedent).
// The COD gate itself runs inside Sale; these are the read/admin endpoints behind requireStaff /
// requireAdmin.

import (
	"context"
	"net/http"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// actorFrom returns the authenticated staff actor for attribution stamps (parked_by/resumed_by/
// created_by), the same identity guard read the Sale/Return use.
func actorFrom(ctx context.Context) string { return identity.StaffUserIDFromContext(ctx) }

// Policy returns the EFFECTIVE policy for a branch (the branch row when present, else the org default).
//
// DIB-488 rev 8 (item 4): an un-curated quick_chip_set (” in the column) is DERIVED here from the
// top-selling categories of committed sales (catalogue categories when no sales exist yet), the
// commander's "seed the curated set from top-selling categories until the client curates" ruling,
// answered data-driven at read time so it tracks reality without a job. Best-effort: a failed
// derivation leaves the set absent and the client falls back to the full chip row (never an error).
func (s *Service) Policy(ctx context.Context, branchID string) (PosPolicy, error) {
	p, err := s.store.ReadPosPolicy(ctx, branchID)
	if err != nil {
		return PosPolicy{}, err
	}
	if len(p.QuickChipSet) == 0 {
		if derived, derr := s.store.TopSellingApplications(ctx, 6); derr == nil {
			p.QuickChipSet = derived
		}
	}
	return p, nil
}

// UpdatePolicy upserts a policy row: branch_id "" targets the org-wide default (seeded by 0043), any
// other value a per-branch override. Admin-gated at the route.
//
// DIB-488: line_presentation is validated HERE, before the row is written, so a bad value is a 422
// with words rather than a CHECK-constraint 500. An omitted value ("") normalizes to the D-096
// default (LINES), the same posture the 0165 column default takes.
func (s *Service) UpdatePolicy(ctx context.Context, p PosPolicy) (PosPolicy, error) {
	switch p.LinePresentation {
	case "":
		p.LinePresentation = LinePresentationLines
	case LinePresentationLines, LinePresentationCards:
		// authored explicitly, fine
	default:
		return PosPolicy{}, clientErr(http.StatusUnprocessableEntity,
			"pos: line_presentation must be LINES or CARDS")
	}
	// DIB-488 rev 2 (0166): the workspace DEFAULTS ride the same admin write. Same posture as
	// line_presentation, omitted values normalize to the column defaults, bad values are a 422
	// in words before the CHECK constraint could make them a 500.
	split, arr, err := validateWorkspaceGeometry(p.WorkspaceSplitPct, p.WorkspaceArrangement, WorkspaceSplitDefault)
	if err != nil {
		return PosPolicy{}, err
	}
	p.WorkspaceSplitPct, p.WorkspaceArrangement = split, arr
	// DIB-488 rev 3 (0167): the lookup-results presentation default rides the same admin write -
	// omitted normalizes to CARDS (the column default), bad values are a 422 in words.
	pres, err := validateLookupPresentation(p.LookupPresentation)
	if err != nil {
		return PosPolicy{}, err
	}
	p.LookupPresentation = pres
	// DIB-488 rev 8 (0170): margin visibility + the curated chip set ride the same admin write -
	// omitted normalizes to the column default, bad values are a 422 in words.
	mv, err := validateMarginVisibility(p.MarginVisibility)
	if err != nil {
		return PosPolicy{}, err
	}
	p.MarginVisibility = mv
	// 0181: the past-due balance gate rides the same admin write, same posture.
	bv, err := validateBalanceVisibility(p.BalanceVisibility)
	if err != nil {
		return PosPolicy{}, err
	}
	p.BalanceVisibility = bv
	chips, err := validateQuickChipSet(p.QuickChipSet)
	if err != nil {
		return PosPolicy{}, err
	}
	p.QuickChipSet = chips
	// THE PRE-IMAGE IS READ BEFORE THE WRITE, which is the whole reason before→after can be truthful.
	//
	// IT IS s.store.ReadPosPolicy AND NOT s.Policy. Policy() DERIVES an un-curated quick_chip_set from
	// the top-selling categories (see its header), so diffing against it would compare a stored empty
	// set with a computed one and write a quick_chip_set change row on the FIRST save of every dealer
	// who has never curated a chip, a history entry for something nobody did. The trail must diff
	// what is in the column against what is going into it, and nothing else.
	//
	// A FAILED PRE-READ DOES NOT BLOCK THE SAVE. The dealer's intent is the important thing; losing the
	// before-image costs a history row, and refusing a legitimate settings change because a second read
	// failed would cost them the change itself.
	before, beforeErr := s.store.ReadPosPolicy(ctx, p.BranchID)
	if err := s.store.UpsertPosPolicy(ctx, p); err != nil {
		return PosPolicy{}, err
	}
	saved, err := s.store.ReadPosPolicy(ctx, p.BranchID)
	if err != nil {
		return PosPolicy{}, err
	}
	if beforeErr == nil {
		// ONE ROW PER CHANGED KNOB, in the field/before/after shape DIB-175 proved and the audit
		// allowlist already admits. The alternative, a new allowlisted key per knob per module -
		// scales to a key per setting in the system, and the allowlist is deny-by-default precisely so
		// that it stays small enough to read.
		//
		// The caller is resolved rather than assumed: the route is admin-gated, so a caller() that
		// fails here means the guard changed underneath this code, and an unattributed row is better
		// than a wrong one, actorFrom/RoleCode simply come back empty and the history says "somebody".
		caller, _ := s.caller(ctx)
		s.recordPolicyChanges(ctx, p.BranchID, caller.UserID, caller.RoleCode, posPolicyChanges(before, saved))
	}
	return saved, nil
}

// Park saves a ticket for later (no order, no stock effect, no AR, prices re-derive on resume).
func (s *Service) Park(ctx context.Context, in ParkInput) (ParkedTicket, error) {
	if in.BranchID == "" {
		return ParkedTicket{}, clientErr(http.StatusBadRequest, "pos: branch_id is required")
	}
	if len(in.Lines) == 0 {
		return ParkedTicket{}, errNoLines
	}
	for _, ln := range in.Lines {
		if ln.ProductID == "" || ln.QtyEntered <= 0 {
			return ParkedTicket{}, clientErr(http.StatusUnprocessableEntity, "pos: every parked line needs a product_id and a positive qty_entered")
		}
	}
	saleType := in.SaleType
	if saleType == "" {
		saleType = SaleCollectNow
	}
	return s.store.InsertParkedTicket(ctx, ParkedTicket{
		BranchID: in.BranchID, CustomerID: in.CustomerID, SaleType: saleType,
		Notes: in.Notes, Lines: in.Lines, Status: "PARKED",
	}, actorFrom(ctx))
}

// ListParked returns the PARKED tickets for a branch (newest first).
//
// THE PARKED FENCE (D49 row 500): a branch is a hard wall, so a caller is held to its own
// branches on every parked read, the same 403 the sale's branch check answers.
func (s *Service) ListParked(ctx context.Context, branchID string) ([]ParkedTicket, error) {
	if branchID == "" {
		return nil, clientErr(http.StatusBadRequest, "pos: branch_id is required")
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	if !caller.CanAccessBranch(branchID) {
		return nil, clientErr(http.StatusForbidden, "pos: caller cannot access branch %s", branchID)
	}
	return s.store.ListParkedTickets(ctx, branchID)
}

// ResumeParked marks a PARKED ticket RESUMED and returns its payload, the client re-adds the lines
// through the normal pricing path so nothing rings at a stale price. Resuming twice is a 409.
//
// THE PARKED FENCE (D49 row 500): anyone at the counter picks up any parked sale at THEIR branch,
// no owner check, who parked and who resumed stays recorded (0043's columns). The wall is the
// branch alone, and a ticket behind it is refused before any write.
func (s *Service) ResumeParked(ctx context.Context, id string) (ParkedTicket, error) {
	if id == "" {
		return ParkedTicket{}, errBadRequest
	}
	if err := s.parkedBranchAccess(ctx, id); err != nil {
		return ParkedTicket{}, err
	}
	return s.store.MarkParkedResumed(ctx, id, actorFrom(ctx))
}

// DiscardParked marks a PARKED ticket DISCARDED. Discarding a non-PARKED ticket is a 409.
// The same parked fence holds: the caller's branches, checked before any write.
func (s *Service) DiscardParked(ctx context.Context, id string) error {
	if id == "" {
		return errBadRequest
	}
	if err := s.parkedBranchAccess(ctx, id); err != nil {
		return err
	}
	return s.store.MarkParkedDiscarded(ctx, id)
}

// parkedBranchAccess is the by-id half of the parked fence: the ticket must exist (404) and sit
// in a branch the caller reaches (403), both read before any write, so a foreign branch's ticket
// is refused on its branch and not on its status.
func (s *Service) parkedBranchAccess(ctx context.Context, id string) error {
	t, found, err := s.store.GetParkedTicket(ctx, id)
	if err != nil {
		return err
	}
	if !found {
		return errParkedNotFound
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	if !caller.CanAccessBranch(t.BranchID) {
		return clientErr(http.StatusForbidden, "pos: caller cannot access branch %s", t.BranchID)
	}
	return nil
}
