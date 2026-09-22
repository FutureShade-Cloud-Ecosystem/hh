package messaging

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// - DIB-462 persistence: the rule toggles + the payment facts the matchers read -

// PaymentNotifyFacts resolves ONE payment into the facts the rule matchers need.
//
// - Why this SELECT lives in the messaging repository -
//
// It reads payments / payment_allocations / invoices / orders / locations / customers, tables this
// module does not own. That is the established shape here, not an exception: CustomerFacts reads
// customers, YardSlugForBranch reads locations, and rep_assignments (0136) FKs straight to orders. The
// house rule this module follows is that IDENTITY IS THE SOURCE OF TRUTH and a module resolves the
// facts it routes on rather than trusting a payload to describe them (RouteCustomerMessage states it
// for customers; money deserves it more). A seam through the composition root would have bought
// nothing here except a main.go edit and a second place for the definition of "online" to live.
//
// - The three facts, and how each is decided -
//
//  1. TakenAtCounter, till_session_id IS NOT NULL OR received_by IS NOT NULL. A human took it. Its
//     negation is what ONLINE means (matchesOnline explains why the negative, not the gateway name).
//  2. The ORDER, payment_allocations → invoices → orders. A payment can settle several invoices; a
//     notice names ONE order, so the query takes the LARGEST allocation (ties broken by the oldest
//     invoice), the order the money is mostly about, decided deterministically rather than by
//     whichever row the planner returned first.
//  3. OnStatement, the payment settled at least one invoice at all, i.e. it landed on the account
//     rather than being a bare counter tender. Context for the sentence; no rule turns on it.
//
// A payment id that names no row returns Found=false and a nil error: an event about a payment this
// database cannot see is not a failure of the messaging module, and treating it as one would put the
// outbox row into a retry loop over a fact that will never arrive.
func (r *Repository) PaymentNotifyFacts(ctx context.Context, paymentID string) (PaymentFacts, error) {
	if r.pool == nil {
		return PaymentFacts{}, errNoDB
	}
	const q = `
WITH pick AS (
  -- The order this payment is mostly about: the biggest allocation to an order-backed invoice.
  SELECT i.order_id, sum(pa.amount_cents) AS alloc, min(i.created_at) AS first_at
  FROM payment_allocations pa
  JOIN invoices i ON i.id = pa.invoice_id
  WHERE pa.payment_id = $1::uuid AND i.order_id IS NOT NULL
  GROUP BY i.order_id
  ORDER BY alloc DESC, first_at ASC
  LIMIT 1
)
SELECT p.branch_id::text,
       -- The branch's SHORT name. locations.name is the legal one ("Dibbits Landscape Supply -
       -- Trenton"); a notice that spells that out twice in one sentence buries the two words the
       -- reader actually needs. The city half is the same part of the same string the house slug
       -- convention already derives from (0136: house-<name-after-em-dash>), so this is the shipped
       -- naming rule, not a second one. A name with no em-dash falls back whole.
       COALESCE(NULLIF(btrim(split_part(pl.name, '-', 2)), ''), pl.name, ''),
       (p.till_session_id IS NOT NULL OR p.received_by IS NOT NULL) AS taken_at_counter,
       p.amount_cents,
       COALESCE(p.customer_id::text, ''),
       COALESCE(c.name, ''),
       COALESCE(o.id::text, ''),
       COALESCE(o.order_no::text, ''),
       COALESCE(o.branch_id::text, ''),
       COALESCE(NULLIF(btrim(split_part(ol.name, '-', 2)), ''), ol.name, ''),
       EXISTS (SELECT 1 FROM payment_allocations a WHERE a.payment_id = p.id) AS on_statement
FROM payments p
LEFT JOIN locations pl ON pl.id = p.branch_id
LEFT JOIN customers c  ON c.id = p.customer_id
LEFT JOIN pick        ON TRUE
LEFT JOIN orders o     ON o.id = pick.order_id
LEFT JOIN locations ol ON ol.id = o.branch_id
WHERE p.id = $1::uuid`

	f := PaymentFacts{PaymentID: paymentID}
	err := r.pool.QueryRow(ctx, q, paymentID).Scan(
		&f.TakenBranchID, &f.TakenBranchName, &f.TakenAtCounter, &f.AmountCents,
		&f.CustomerID, &f.CustomerName,
		&f.OrderID, &f.OrderNo, &f.OrderBranchID, &f.OrderBranch,
		&f.OnStatement,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PaymentFacts{PaymentID: paymentID, Found: false}, nil
		}
		return PaymentFacts{}, fmt.Errorf("payment notify facts: %w", err)
	}
	f.Found = true
	return f, nil
}

// PaymentRulesFor returns one user's STORED rule rows. An absent rule is an absent key, the service
// widens it to the full set with false for the missing ones (newPaymentRulePrefs), so "never decided"
// and "decided off" reach the surface identically, which is what they mean.
func (r *Repository) PaymentRulesFor(ctx context.Context, userID string) (map[string]bool, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT rule, enabled FROM msg_payment_notify_rules WHERE user_id = $1::uuid`, userID)
	if err != nil {
		return nil, fmt.Errorf("payment rules for user: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var rule string
		var enabled bool
		if err := rows.Scan(&rule, &enabled); err != nil {
			return nil, fmt.Errorf("scan payment rule: %w", err)
		}
		out[rule] = enabled
	}
	return out, rows.Err()
}

// SetPaymentRule upserts ONE toggle and returns the user's whole stored set, so the caller renders from
// what was actually persisted rather than from what it hoped it wrote.
//
// A turned-OFF rule is stored as enabled=FALSE rather than deleted. Absence and FALSE behave
// identically at read time (both mean "do not notify"), so this is not about behaviour, it is about
// the record: "I looked at this and said no" is a decision, and a surface that quietly forgets it
// cannot tell a returning user whether they have been here before.
func (r *Repository) SetPaymentRule(ctx context.Context, userID, rule string, enabled bool) (map[string]bool, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO msg_payment_notify_rules (user_id, rule, enabled)
		VALUES ($1::uuid, $2, $3)
		ON CONFLICT (user_id, rule) DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = now()`,
		userID, rule, enabled); err != nil {
		return nil, fmt.Errorf("set payment rule: %w", err)
	}
	return r.PaymentRulesFor(ctx, userID)
}

// UsersWithPaymentRule lists the ACTIVE staff who have enabled a rule, the fan-out read, run once per
// matched rule per routine payment.
//
// The `users.status = 'ACTIVE'` join is the guard that matters: a departed staffer's rows survive them
// (the table cascades on user delete, and nobody deletes users), and provisioning a DM channel for a
// deactivated account would write messages into an inbox no one opens. Ordered by id so the fan-out -
// and any test asserting on it, is deterministic.
func (r *Repository) UsersWithPaymentRule(ctx context.Context, rule string) ([]string, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT pr.user_id::text
		FROM msg_payment_notify_rules pr
		JOIN users u ON u.id = pr.user_id
		WHERE pr.rule = $1 AND pr.enabled AND u.status = 'ACTIVE'
		ORDER BY pr.user_id`, rule)
	if err != nil {
		return nil, fmt.Errorf("users with payment rule: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan payment rule user: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
