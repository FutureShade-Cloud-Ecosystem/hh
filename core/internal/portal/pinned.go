package portal

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ProjectDB is the statement runner EnsureProject needs: a transaction or
// a pool satisfies it.
type ProjectDB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// PinnedProject is one job written under a caller chosen id, for a
// provisioning program (the demo populate's three Summit Ridge Builders
// jobs). The tenant is the customer the job belongs to.
type PinnedProject struct {
	ID         string
	CustomerID string
	Name       string
	JobAddress string
}

// EnsureProject writes the job under its id, once: a row already holding
// the id is left as it is (created false) and draws no sync sequence, so a
// re-run writes nothing. The row is stamped with the tenant (its customer,
// the 0213 convention) and the sync sequence from the projects scope.
func EnsureProject(ctx context.Context, db ProjectDB, p PinnedProject) (created bool, err error) {
	if p.ID == "" || p.CustomerID == "" || p.Name == "" {
		return false, errors.New("portal: a pinned project needs an id, a customer and a name")
	}
	var one int
	switch err := db.QueryRow(ctx, `SELECT 1 FROM projects WHERE id = $1::uuid`, p.ID).Scan(&one); {
	case err == nil:
		return false, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return false, fmt.Errorf("portal: pinned project read: %w", err)
	}
	tag, err := db.Exec(ctx, projectSeqCTE+`
		INSERT INTO projects (id, customer_id, name, job_address, tenant_id, seq)
		VALUES ($1::uuid, $2::uuid, $3, $4, $2::uuid, (SELECT next_value FROM pseq))
		ON CONFLICT (id) DO NOTHING`,
		p.ID, p.CustomerID, p.Name, p.JobAddress)
	if err != nil {
		return false, fmt.Errorf("portal: pinned project write: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
