package tasks

import (
	"context"
	"log/slog"
)

// claimer_role.go: the pick payload names who holds a claimed pick by ROLE LABEL ("Yard Manager"),
// never by name, email or id (UX-75). The label is identity's to answer, so this module reaches it
// through a seam and reads no staff table itself. It is read side only: the claim writes, the state
// machine and the sync write fences are untouched.

// ClaimerRoleReader answers the staff role label each given users.id holds. The composition root
// binds identity's reader; unbound, or on a fault, a pick simply carries no label and the yard
// screen says "a teammate". A user with no live grant is absent from the map.
type ClaimerRoleReader interface {
	RoleLabelsForUsers(ctx context.Context, userIDs []string) (map[string]string, error)
}

// WithClaimerRoles injects the claimer role label reader (see ClaimerRoleReader).
func (s *Service) WithClaimerRoles(r ClaimerRoleReader) *Service { s.claimerRoles = r; return s }

// WithClaimerRoles injects the same reader for the pick_tasks sync rows.
func (s *SyncStore) WithClaimerRoles(r ClaimerRoleReader) *SyncStore { s.claimerRoles = r; return s }

// claimerLabels reads the labels of the distinct, non empty claimer ids. It makes no call when no
// pick is claimed, and returns nil on an unwired reader or a fault (logged): a role fault degrades a
// label away, it never fails a board or a pull.
func claimerLabels(ctx context.Context, r ClaimerRoleReader, log *slog.Logger, ids []string) map[string]string {
	if r == nil {
		return nil
	}
	seen := make(map[string]bool, len(ids))
	distinct := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			distinct = append(distinct, id)
		}
	}
	if len(distinct) == 0 {
		return nil
	}
	labels, err := r.RoleLabelsForUsers(ctx, distinct)
	if err != nil {
		if log == nil {
			log = slog.Default()
		}
		log.Warn("tasks: claimer role labels unavailable", "err", err)
		return nil
	}
	return labels
}

// labelClaimers sets ClaimedByRole on each claimed task of the slice, in place, with one read.
func (s *Service) labelClaimers(ctx context.Context, tasks []*Task) {
	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ClaimedBy)
	}
	labels := claimerLabels(ctx, s.claimerRoles, s.log, ids)
	for _, t := range tasks {
		if t.ClaimedBy != "" {
			t.ClaimedByRole = labels[t.ClaimedBy]
		}
	}
}

// attachClaimerRoles sets data["claimed_by_role"] on each sync row that carries a claimer with a
// label, with one read for the lot. A row with no claimer, or a claimer with no role, gets no field.
func (s *SyncStore) attachClaimerRoles(ctx context.Context, rows []map[string]any) {
	ids := make([]string, 0, len(rows))
	for _, d := range rows {
		id, _ := d["claimed_by"].(string)
		ids = append(ids, id)
	}
	labels := claimerLabels(ctx, s.claimerRoles, s.log, ids)
	for _, d := range rows {
		id, _ := d["claimed_by"].(string)
		if label := labels[id]; id != "" && label != "" {
			d["claimed_by_role"] = label
		}
	}
}
