package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MachinePrincipal is one machine credential's record (migration
// core/db/migrations/0321_fb_moves_machine_principal.sql, ADR-0024): the
// org it belongs to, its name, whether it is enabled, and the doors (the
// contract operation ids) it may call. It is FB Move's identity for the
// load doors and nothing else: it holds no staff role and appears in no
// roleconfig cell, so no staff role, no roleconfig cell and no dealer
// setting can widen what it reaches. The token subject names the id; the
// record, never the token, carries the authority.
type MachinePrincipal struct {
	ID      string
	OrgID   string
	Name    string
	Enabled bool
	Doors   []string
}

// MachinePrincipalResolver loads a machine principal by id over a pgx
// pool. It reads the one table it owns and nothing else; the authz
// machine gate consumes it through the narrow MachinePrincipalSource
// seam it declares for itself (core/internal/platform/authz/
// machinegate.go), the same shape StaffRolesSource carries.
type MachinePrincipalResolver struct {
	pool *pgxpool.Pool
}

// NewMachinePrincipalResolver validates the collaborator and returns the
// resolver. A nil pool is a construction error, never a first-read
// surprise.
func NewMachinePrincipalResolver(pool *pgxpool.Pool) (*MachinePrincipalResolver, error) {
	if pool == nil {
		return nil, errors.New("identity: machine principal resolver requires a pool")
	}
	return &MachinePrincipalResolver{pool: pool}, nil
}

// MachinePrincipal loads one principal. ok false with a nil error is a
// miss: an id no row carries, including a subject that is not a uuid at
// all, names no principal and the gate answers it with the same body as
// every other machine refusal, so no caller can enumerate the ids that
// exist. The doors array comes back in its stored order.
func (r *MachinePrincipalResolver) MachinePrincipal(ctx context.Context, principalID string) (MachinePrincipal, bool, error) {
	if _, err := uuid.Parse(principalID); err != nil {
		return MachinePrincipal{}, false, nil
	}
	var p MachinePrincipal
	err := r.pool.QueryRow(ctx,
		`SELECT id::text, org_id::text, name, enabled, doors FROM machine_principal WHERE id = $1`,
		principalID).Scan(&p.ID, &p.OrgID, &p.Name, &p.Enabled, &p.Doors)
	if errors.Is(err, pgx.ErrNoRows) {
		return MachinePrincipal{}, false, nil
	}
	if err != nil {
		return MachinePrincipal{}, false, fmt.Errorf("identity: machine principal read failed: %w", err)
	}
	return p, true, nil
}
