// jobload.go is the portal module's write path for the load doors' job
// items (HH-LOAD-1 LD-6). The projects table (0048) is read by this
// module and had no writer outside the test kit, so the writer lives
// here, the module boundary rule's answer (the plan's reading 10.10),
// and the load frame hands it each item inside its own batch
// transaction, taken by constructor at the wiring.
//
// A job is the record only: the name, the job address and the customer
// by key. Job balances, statements and receivables attribution are
// Tier B and nothing of them is written here. The projects table has no
// org column, so the job's org rides through its customer: the writer
// lands a job only under a customer of the machine principal's org, and
// stamps the tenant (the customer the job belongs to, the 0213 tenant
// convention the test kit's fixtures follow), the sync sequence from the
// projects scope, and the derived key.
//
// The writer's rules, binding: it opens no connection of its own, it
// never commits or rolls back the transaction it is given, and its
// errors wrap the statement's own (%w) so a Postgres deadlock stays
// recognisable at the batch boundary. Refusals come back as
// *load.Refusal and carry codes and fields, never a record value.
package portal

import (
	"context"
	"errors"
	"fmt"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// projectSeqCTE draws the seq atomically in the same statement from the
// projects scope, the table's own name, so a sync scope declared for the
// table later reads rows this writer stamped.
const projectSeqCTE = `WITH pseq AS (
	INSERT INTO scope_sequences (scope) VALUES ('projects')
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
	RETURNING next_value
) `

// CustomerOrgSource is the customer module's read the job writer asks
// whether a customer belongs to the machine principal's org through,
// inside the frame's transaction. The consumer declares the narrow
// interface it needs; the concrete reader is the customer package's
// OrgReader.
type CustomerOrgSource interface {
	CustomerOrgTx(ctx context.Context, tx pgx.Tx, customerID string) (found bool, orgID string, err error)
}

// JobLoadWriter is the load doors' job writer.
type JobLoadWriter struct {
	Customers CustomerOrgSource
}

// Apply answers one job item. The record names the job's name
// (required) and, named, the job address; the one reference of kind
// customer names the customer the job belongs to. A project has no
// natural key of its own, so the registry's name is its identity: the
// create arm inserts, the update arm updates the named row.
func (w JobLoadWriter) Apply(ctx context.Context, tx pgx.Tx, in load.Write) (string, error) {
	name, _ := in.Record["name"].(string)
	if name == "" {
		return "", &load.Refusal{Code: "field_required", Field: "name",
			Detail: "the record names no job name"}
	}
	address, _ := in.Record["job_address"].(string)
	customerID, customerKey := "", ""
	for _, r := range in.Refs {
		if r.Kind != load.KindCustomer || customerID != "" {
			return "", &load.Refusal{Code: load.ReasonWrongReference, Field: r.Kind,
				Detail: "a job names one customer reference and no other"}
		}
		customerID, customerKey = r.HHID, r.LegacyID
	}
	if customerID == "" {
		return "", &load.Refusal{Code: "field_required", Field: load.KindCustomer,
			Detail: "the item names no customer reference; a job lands under its customer"}
	}
	found, org, err := w.Customers.CustomerOrgTx(ctx, tx, customerID)
	if err != nil {
		return "", fmt.Errorf("portal: job customer read: %w", err)
	}
	switch {
	case !found:
		return "", &load.Refusal{Code: load.ReasonMissingReference, Field: load.KindCustomer,
			Detail: fmt.Sprintf("the customer row for legacy key %s is gone", customerKey)}
	case org == "":
		return "", &load.Refusal{Code: load.ReasonMissingReference, Field: load.KindCustomer,
			Detail: fmt.Sprintf("the customer row for legacy key %s carries no org of this load", customerKey)}
	case org != in.OrgID:
		return "", &load.Refusal{Code: "org_mismatch", Field: load.KindCustomer,
			Detail: fmt.Sprintf("customer legacy key %s names a row another org owns", customerKey)}
	}
	var id string
	if in.RowID == "" {
		if err := tx.QueryRow(ctx, projectSeqCTE+`
			INSERT INTO projects (customer_id, name, job_address, tenant_id, seq, idempotency_key)
			VALUES ($1::uuid, $2, $3, $1::uuid, (SELECT next_value FROM pseq), NULLIF($4, '')::uuid)
			RETURNING id::text`,
			customerID, name, address, in.DerivedKey).Scan(&id); err != nil {
			return "", fmt.Errorf("portal: job load write: %w", err)
		}
		return id, nil
	}
	err = tx.QueryRow(ctx, projectSeqCTE+`
		UPDATE projects SET customer_id = $2::uuid, name = $3, job_address = $4, tenant_id = $2::uuid,
		       seq = (SELECT next_value FROM pseq), idempotency_key = NULLIF($5, '')::uuid, updated_at = now()
		WHERE id = $1::uuid
		RETURNING id::text`,
		in.RowID, customerID, name, address, in.DerivedKey).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", &load.Refusal{Code: load.ReasonUpdateTargetMissing, Field: "legacy_id",
			Detail: "the name's row is gone; the update refuses rather than creating a second row"}
	}
	if err != nil {
		return "", fmt.Errorf("portal: job load update: %w", err)
	}
	return id, nil
}

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
