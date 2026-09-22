package pos

// workspace.go, DIB-488 rev 2: the POS two-panel workspace's persistence, resolved server-side.
//
// THE PERSISTENCE RULING (stated once, here, and mirrored in 0166 and the client):
//
//   DEALER level (pos_policy; 0166 columns)   the DEFAULTS, default split, default arrangement -
//                                             per the client's standing "configure once at dealer
//                                             level" ruling. Written through the existing
//                                             admin-gated PUT /api/v1/pos/policy only.
//   PER USER  (pos_user_workspace; 0166)      one counter person's ERGONOMICS, where they parked
//                                             the divider, what they collapsed, whether they
//                                             swapped sides, so each person keeps their own
//                                             workspace across logins and tills. Staff-gated,
//                                             self-service, keyed by the AUTHENTICATED caller
//                                             (never a user id off the wire).
//   NOT persisted                             temporary maximize. A gesture, not a preference.
//
// The user override is WHOLE OR ABSENT (the dashboard_user_tiles/_user_tabs ruling, 0140/0149):
// a stored row IS the workspace and the dealer defaults are not consulted; reset is a DELETE.

import (
	"context"
	"net/http"
)

// validateWorkspaceGeometry normalizes/refuses the shared split+arrangement pair. An omitted
// arrangement normalizes to the default (the 0166 column default posture); an omitted split (0)
// normalizes to defaultSplit. Out-of-range/unknown values are a 422 with words, never a
// CHECK-constraint 500.
func validateWorkspaceGeometry(splitPct int, arrangement string, defaultSplit int) (int, string, error) {
	if splitPct == 0 {
		splitPct = defaultSplit
	}
	if splitPct < WorkspaceSplitMin || splitPct > WorkspaceSplitMax {
		return 0, "", clientErr(http.StatusUnprocessableEntity,
			"pos: workspace split_pct must be between 20 and 80")
	}
	switch arrangement {
	case "":
		arrangement = ArrangementLookupLeft
	case ArrangementLookupLeft, ArrangementSaleLeft:
		// authored explicitly, fine
	default:
		return 0, "", clientErr(http.StatusUnprocessableEntity,
			"pos: workspace arrangement must be LOOKUP_LEFT or SALE_LEFT")
	}
	return splitPct, arrangement, nil
}

// validateLookupPresentation normalizes/refuses the DIB-488 rev-3 lookup-results presentation
// (0167). An omitted value ("") normalizes to CARDS, the column-default posture, exactly the
// line_presentation precedent; an unknown value is a 422 in words before the CHECK could 500.
func validateLookupPresentation(v string) (string, error) {
	switch v {
	case "":
		return LookupPresentationCards, nil
	case LookupPresentationCards, LookupPresentationRows:
		return v, nil
	default:
		return "", clientErr(http.StatusUnprocessableEntity,
			"pos: lookup_presentation must be CARDS or ROWS")
	}
}

// Workspace resolves the calling user's POS workspace: their stored row when present (source
// "user"), else the branch-effective pos_policy dealer defaults (source "dealer"). Collapse flags
// default open on the dealer path, a dealer configures the split and the arrangement; nobody
// configures "everyone starts collapsed".
func (s *Service) Workspace(ctx context.Context, branchID string) (WorkspacePrefs, error) {
	userID := actorFrom(ctx)
	if userID != "" {
		row, ok, err := s.store.ReadUserWorkspace(ctx, userID)
		if err != nil {
			return WorkspacePrefs{}, err
		}
		if ok {
			// rev 3: a pre-0167 fake (or partial read) may hold "", resolve it to the column
			// default (CARDS) rather than hand the client an empty presentation.
			pres, err := validateLookupPresentation(row.LookupPresentation)
			if err != nil {
				return WorkspacePrefs{}, err
			}
			return WorkspacePrefs{
				SplitPct: row.SplitPct, Arrangement: row.Arrangement,
				LookupCollapsed: row.LookupCollapsed, SaleCollapsed: row.SaleCollapsed,
				LookupPresentation: pres,
				MarginHidden:       row.MarginHidden, // rev 8 (0170): rides the same row
				Source:             "user",
			}, nil
		}
	}
	p, err := s.store.ReadPosPolicy(ctx, branchID)
	if err != nil {
		return WorkspacePrefs{}, err
	}
	// Old rows from before 0166 cannot exist (column defaults backfill), but a zero from a fake or
	// a partial read still resolves to the shipped default rather than an unusable 0% panel.
	split, arr, err := validateWorkspaceGeometry(p.WorkspaceSplitPct, p.WorkspaceArrangement, WorkspaceSplitDefault)
	if err != nil {
		return WorkspacePrefs{}, err
	}
	pres, err := validateLookupPresentation(p.LookupPresentation)
	if err != nil {
		return WorkspacePrefs{}, err
	}
	return WorkspacePrefs{SplitPct: split, Arrangement: arr, LookupPresentation: pres, Source: "dealer"}, nil
}

// SaveWorkspace upserts the CALLER's own pos_user_workspace row. Requires the settings-write
// idempotency key; a replay of the last accepted key returns the stored row without writing again.
func (s *Service) SaveWorkspace(ctx context.Context, in WorkspaceSaveInput) (WorkspacePrefs, error) {
	userID := actorFrom(ctx)
	if userID == "" {
		return WorkspacePrefs{}, clientErr(http.StatusUnauthorized, "pos: no authenticated staff caller")
	}
	if in.IdempotencyKey == "" {
		return WorkspacePrefs{}, clientErr(http.StatusUnprocessableEntity,
			"pos: idempotency_key is required on a workspace write")
	}
	split, arr, err := validateWorkspaceGeometry(in.SplitPct, in.Arrangement, WorkspaceSplitDefault)
	if err != nil {
		return WorkspacePrefs{}, err
	}
	// rev 3 (0167): the lookup presentation rides the same whole-or-absent save.
	pres, err := validateLookupPresentation(in.LookupPresentation)
	if err != nil {
		return WorkspacePrefs{}, err
	}
	if prev, ok, err := s.store.ReadUserWorkspace(ctx, userID); err != nil {
		return WorkspacePrefs{}, err
	} else if ok && prev.WriteKey == in.IdempotencyKey {
		// The retry of an already-accepted save: answer from the store, write nothing.
		return WorkspacePrefs{
			SplitPct: prev.SplitPct, Arrangement: prev.Arrangement,
			LookupCollapsed: prev.LookupCollapsed, SaleCollapsed: prev.SaleCollapsed,
			LookupPresentation: prev.LookupPresentation,
			MarginHidden:       prev.MarginHidden, // rev 8: the STORED row answers, this field included
			Source:             "user", Replayed: true,
		}, nil
	}
	row := userWorkspaceRow{
		UserID: userID, SplitPct: split, Arrangement: arr,
		LookupCollapsed: in.LookupCollapsed, SaleCollapsed: in.SaleCollapsed,
		LookupPresentation: pres,
		MarginHidden:       in.MarginHidden, // rev 8 (0170): the quick-hide toggle rides the row
		WriteKey:           in.IdempotencyKey,
	}
	if err := s.store.UpsertUserWorkspace(ctx, row); err != nil {
		return WorkspacePrefs{}, err
	}
	return WorkspacePrefs{
		SplitPct: split, Arrangement: arr,
		LookupCollapsed: in.LookupCollapsed, SaleCollapsed: in.SaleCollapsed,
		LookupPresentation: pres,
		MarginHidden:       in.MarginHidden,
		Source:             "user",
	}, nil
}

// ResetWorkspace deletes the caller's override, reset IS a delete (the dashboard ruling), after
// which the next read resolves to the dealer defaults again.
func (s *Service) ResetWorkspace(ctx context.Context) error {
	userID := actorFrom(ctx)
	if userID == "" {
		return clientErr(http.StatusUnauthorized, "pos: no authenticated staff caller")
	}
	return s.store.DeleteUserWorkspace(ctx, userID)
}
