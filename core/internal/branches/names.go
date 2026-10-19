package branches

// names.go, the owning module's name read for the branch ids other modules already hold. The
// stock lookup's out-at line (change 5 slice S4) is the consumer today: a browse row knows the
// branches whose stock record shows nothing available, by id, and a list line needs the name a
// person reads. The locations table stays this module's alone; the consumer holds a
// consumer-declared seam (inventory.BranchNameReader) and the composition root binds this to it.

import (
	"context"
)

// Names resolves the display names of the branch ids the caller holds. An id that names no row of
// this dealer's is simply absent from the reply; what an unnamed branch reads as is the caller's
// decision, the same posture Exists keeps (the owner answers the fact, never the status).
func (r *Repository) Names(ctx context.Context, ids []string) (map[string]string, error) {
	names := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return names, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, name FROM locations WHERE loc_type = 'BRANCH' AND id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		names[id] = name
	}
	return names, rows.Err()
}
