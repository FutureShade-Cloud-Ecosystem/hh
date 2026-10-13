package customer

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// Execer is the one statement runner EnsureAccount needs: a pgx pool, a
// connection or a transaction satisfies it.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// PinnedAccount is one customer account written under a caller chosen id,
// for a provisioning program that must name the row elsewhere before it
// exists (the demo populate's contractor account, whose id is the tenant
// claim of the contractor tokens).
type PinnedAccount struct {
	ID               string
	AccountNo        string
	Name             string
	CustomerType     string
	HomeBranchID     string
	OrgID            string
	PaymentTermsDays int
	CreditLimitCents int64
}

// EnsureAccount writes the account under its id, once: a row already
// holding the id is left exactly as it is (created false), so a re-run of
// the provisioning program writes nothing. An account number held by a
// different id is the plan and the database disagreeing about the world,
// and surfaces as the unique violation it is. The row takes its seq from
// the customers scope the module's own pull reads through.
func EnsureAccount(ctx context.Context, db Execer, a PinnedAccount) (created bool, err error) {
	if a.ID == "" || a.AccountNo == "" || a.Name == "" {
		return false, errors.New("customer: a pinned account needs an id, an account number and a name")
	}
	if !validCustomerType(a.CustomerType) {
		return false, fmt.Errorf("customer: invalid customer_type %q", a.CustomerType)
	}
	tag, err := db.Exec(ctx, customerSeqCTE+`
		INSERT INTO customers (id, account_no, name, customer_type, home_branch_id,
		                       payment_terms_days, credit_limit_cents, org_id, seq)
		VALUES ($1::uuid, $2, $3, $4, NULLIF($5, '')::uuid, $6, $7, NULLIF($8, '')::uuid,
		        (SELECT next_value FROM cseq))
		ON CONFLICT (id) DO NOTHING`,
		a.ID, a.AccountNo, a.Name, a.CustomerType, a.HomeBranchID,
		a.PaymentTermsDays, a.CreditLimitCents, a.OrgID)
	if err != nil {
		return false, fmt.Errorf("customer: pinned account write: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
