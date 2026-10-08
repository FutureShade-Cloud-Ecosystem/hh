package customer

// pay_balance_seam.go, the customer reads the POS Pay balance door (item 9.4)
// and billing's pay before release hold compose with the money modules'
// reads: the customers and customer_contacts tables are this module's, so
// those callers ask here and never read the tables themselves. Every method
// takes the caller's transaction.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// PayBalanceCustomer is the customer's name and terms as the Pay balance door
// shows and judges them.
type PayBalanceCustomer struct {
	Name      string
	TermsDays int
}

// MatchPayBalanceCustomers answers the ids of the customers a Pay balance
// find term reaches: the name matches the LIKE pattern, or an active contact's
// phone does. At most limit ids, so a one letter term cannot walk the table.
func (r *Repository) MatchPayBalanceCustomers(ctx context.Context, tx pgx.Tx, like string, limit int) ([]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT c.id::text FROM customers c
		 WHERE c.name ILIKE $1
		    OR EXISTS (SELECT 1 FROM customer_contacts cc
		                WHERE cc.customer_id = c.id AND cc.is_active AND cc.phone LIKE $1)
		 ORDER BY c.id
		 LIMIT $2`, like, limit)
	if err != nil {
		return nil, fmt.Errorf("customer: match pay balance customers: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("customer: scan pay balance customer: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// PayBalanceCustomers answers the name and terms of each customer id, keyed
// by id; an id with no customer is simply absent.
func (r *Repository) PayBalanceCustomers(ctx context.Context, tx pgx.Tx, ids []string) (map[string]PayBalanceCustomer, error) {
	rows, err := tx.Query(ctx,
		`SELECT id::text, COALESCE(name,''), payment_terms_days FROM customers WHERE id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, fmt.Errorf("customer: pay balance customers: %w", err)
	}
	defer rows.Close()
	out := make(map[string]PayBalanceCustomer, len(ids))
	for rows.Next() {
		var id string
		var c PayBalanceCustomer
		if err := rows.Scan(&id, &c.Name, &c.TermsDays); err != nil {
			return nil, fmt.Errorf("customer: scan pay balance customer: %w", err)
		}
		out[id] = c
	}
	return out, rows.Err()
}

// PickHoldTermsDaysTx is the customer half of the pay before release hold
// (billing declares the seam it reads this through): the account's payment
// terms in days, on the caller's transaction. found is false for a missing
// customer.
func (r *Repository) PickHoldTermsDaysTx(ctx context.Context, tx pgx.Tx, customerID string) (days int, found bool, err error) {
	err = tx.QueryRow(ctx,
		`SELECT payment_terms_days FROM customers WHERE id = $1::uuid`, customerID).Scan(&days)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("customer: pick hold terms: %w", err)
	}
	return days, true, nil
}
