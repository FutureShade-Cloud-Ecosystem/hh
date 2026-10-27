package billing

// org_stamp.go, SF-2p2: the billing module's one line seam onto the org stamp rule.
//
// A box provisioned after migrate has invoices with no org (0432 only changes a box that already had an org row), and
// the staff invoices feed fences on org_id. The rule (stamp from the branch's location, refuse rather than guess, add
// the NOT NULL check) is the SQL function org_stamp_and_check, 0432's body parameterised; the identity populate runs
// it through each owning module's seam, so identity never names this table.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// orgDB is the one capability the seam needs: a single row query (identity's DBTX satisfies it).
type orgDB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// StampInvoiceOrgs stamps every invoice with no org from its branch's location and adds invoices_org_id_not_null, by
// the one SQL rule. It answers the rows stamped, and refuses in 0432's words when an invoice has no branch or sits at
// a location with no org. Idempotent; a box with no org row changes nothing.
func StampInvoiceOrgs(ctx context.Context, db orgDB) (int64, error) {
	var n int64
	if err := db.QueryRow(ctx, `SELECT org_stamp_and_check('invoices', 'branch_id', 'invoices_org_id_not_null')`).Scan(&n); err != nil {
		return 0, fmt.Errorf("billing: stamp invoice orgs: %w", err)
	}
	return n, nil
}

// UnstampedInvoiceOrgs counts the invoices still carrying no org (zero after the stamp).
func UnstampedInvoiceOrgs(ctx context.Context, db orgDB) (int64, error) {
	var n int64
	if err := db.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE org_id IS NULL`).Scan(&n); err != nil {
		return 0, fmt.Errorf("billing: count unstamped invoices: %w", err)
	}
	return n, nil
}
