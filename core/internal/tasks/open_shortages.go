package tasks

// open_shortages.go, the open-shortage count behind the yard manager's Needs-you row (0380).
//
// The count lives here because task_shortages is this module's table: the dashboard's adapter asks
// through this seam and never reads the table itself. The seam is optional, discovered by type
// assertion off the store (the shape purchasing's incoming read uses), so the stores that predate it
// need no new method.

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ShortageReach is a caller's branch authority projected onto this read. tasks' own copy, by the
// codebase's zero-trust convention: a module must not accept another module's authority object by
// accident.
//
// Global true means every branch and BranchIDs is ignored. Global false with an EMPTY BranchIDs means
// NO branches, a real state (a scoped account with no grants) that resolves to no rows.
type ShortageReach struct {
	Global    bool
	BranchIDs []string
}

// OpenShortages is the answer: how many shortages the yard raised and nobody has resolved, and the
// report time of the oldest.
type OpenShortages struct {
	Count  int
	Oldest time.Time
}

type openShortageStore interface {
	OpenShortageSummary(ctx context.Context, reach ShortageReach) (OpenShortages, error)
}

// OpenShortageSummary counts the unresolved shortages inside the reach. A caller with no grants is
// answered without a round trip; the branch predicate in the store's SQL is the fence that survives
// a caller that skips this method.
func (s *Service) OpenShortageSummary(ctx context.Context, reach ShortageReach) (OpenShortages, error) {
	st, ok := s.store.(openShortageStore)
	if !ok {
		return OpenShortages{}, errors.New("tasks: this store cannot summarise open shortages")
	}
	if !reach.Global && len(reach.BranchIDs) == 0 {
		return OpenShortages{}, nil
	}
	return st.OpenShortageSummary(ctx, reach)
}

// OpenShortageSummary is the pgx half. The branch predicate is always present: nil means global, and
// an empty array matches nothing.
func (r *Repository) OpenShortageSummary(ctx context.Context, reach ShortageReach) (OpenShortages, error) {
	if r.pool == nil {
		return OpenShortages{}, errNoDB
	}
	var branches any
	if !reach.Global {
		ids := reach.BranchIDs
		if ids == nil {
			ids = []string{}
		}
		branches = ids
	}
	var out OpenShortages
	var oldest *time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT count(*)::int, min(s.reported_at)
		  FROM task_shortages s
		  JOIN tasks t ON t.id = s.task_id
		 WHERE s.resolved_at IS NULL
		   AND s.deleted_at IS NULL
		   AND ($1::uuid[] IS NULL OR t.branch_id = ANY($1::uuid[]))`, branches).
		Scan(&out.Count, &oldest)
	if err != nil {
		return OpenShortages{}, fmt.Errorf("tasks: open shortage summary: %w", err)
	}
	if oldest != nil {
		out.Oldest = *oldest
	}
	return out, nil
}
