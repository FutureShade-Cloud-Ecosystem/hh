package billing

// credit_repository.go, the pgx side of DIB-65 / DIB-52 (see credit_gate.go for the design).
//
// Module note, restated here because this is the file that actually does it: billing WRITES
// credit_holds, which is internal/till's table (0035). That bend is deliberate, documented in
// migration 0104, and bounded, the alternative was a second holds table duplicating the "one ACTIVE
// hold per customer" invariant, and two copies of an invariant is how you get two of them disagreeing
// at the counter while a customer waits. The POS and convert gates read this table already, so
// writing to it is what makes AR holds effective with no change on their side.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// CreditPolicy resolves the effective policy for a branch: the branch row when one exists, otherwise
// the org default (branch_id IS NULL). A missing org row is a migration failure, not a runtime state,
// so it errors rather than defaulting to something permissive in silence.
func (r *Repository) CreditPolicy(ctx context.Context, branchID string) (CreditPolicy, error) {
	q := r.creditChangeQuerier(ctx)
	if q == nil {
		return CreditPolicy{}, errNoDB
	}
	var p CreditPolicy
	var bid *string
	err := q.QueryRow(ctx,
		`SELECT COALESCE(branch_id::text,''), aging_bucket_days, aging_threshold_cents, aging_hold_enabled,
		        limit_hold_enabled, limit_tolerance_cents, cod_enforced, auto_hold_enabled, write_off_limit_cents,
		        increase_first_threshold_cents, increase_second_threshold_cents
		   FROM ar_credit_policy
		  WHERE branch_id = NULLIF($1, '')::uuid OR branch_id IS NULL
		  ORDER BY branch_id NULLS LAST
		  LIMIT 1`, branchID).
		Scan(&bid, &p.AgingBucketDays, &p.AgingThresholdCents, &p.AgingHoldEnabled,
			&p.LimitHoldEnabled, &p.LimitToleranceCents, &p.CODEnforced, &p.AutoHoldEnabled, &p.WriteOffLimitCents,
			&p.IncreaseFirstThresholdCents, &p.IncreaseSecondThresholdCents)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CreditPolicy{}, errors.New("billing: no ar_credit_policy row, migration 0104 seeds the org default")
		}
		return CreditPolicy{}, fmt.Errorf("billing: read credit policy: %w", err)
	}
	if bid != nil {
		p.BranchID = *bid
	}
	return p, nil
}

// SetAutoHoldEnabled flips ar_credit_policy.auto_hold_enabled, and ONLY that column (DIB-483).
//
// The statement names one column on purpose. The thresholds in this table are the client's, set by
// UPDATE after a conversation, and a writer that could touch them would eventually be handed a whole
// policy struct by a well-meaning refactor. Narrow here is cheap; narrow later is not.
//
// branchID "" targets the ORG DEFAULT row (branch_id IS NULL), the row 0104 seeds. A branch id with
// no row of its own is NOT silently upgraded into one: it updates nothing and reports so, because
// creating a branch policy row is a different act with different consequences (it stops inheriting),
// and a switch that silently forks a branch off the org default is a switch that will surprise
// somebody.
func (r *Repository) SetAutoHoldEnabled(ctx context.Context, branchID string, enabled bool) error {
	if r.pool == nil {
		return errNoDB
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE ar_credit_policy
		    SET auto_hold_enabled = $2, updated_at = now()
		  WHERE ($1 = '' AND branch_id IS NULL) OR branch_id = NULLIF($1, '')::uuid`,
		branchID, enabled)
	if err != nil {
		return fmt.Errorf("billing: set auto_hold_enabled: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if branchID == "" {
			return errors.New("billing: no ar_credit_policy org default row, migration 0104 seeds it")
		}
		return errors.New("billing: this branch has no credit policy row of its own, it inherits the org default, so change the org setting")
	}
	return nil
}

// SetCreditChangeThresholds updates the dealer-configurable increase approval thresholds
// on ar_credit_policy (0461). Like SetAutoHoldEnabled, branchID "" targets the org default
// row (branch_id IS NULL), or an existing branch policy row when branchID is non-empty.
func (r *Repository) SetCreditChangeThresholds(ctx context.Context, branchID string, firstThresholdCents, secondThresholdCents int64) error {
	if r.pool == nil {
		return errNoDB
	}
	if firstThresholdCents < 0 || secondThresholdCents < firstThresholdCents {
		return errors.New("billing: invalid credit change thresholds")
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE ar_credit_policy
		    SET increase_first_threshold_cents = $2,
		        increase_second_threshold_cents = $3,
		        updated_at = now()
		  WHERE ($1 = '' AND branch_id IS NULL) OR branch_id = NULLIF($1, '')::uuid`,
		branchID, firstThresholdCents, secondThresholdCents)
	if err != nil {
		return fmt.Errorf("billing: set credit change thresholds: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if branchID == "" {
			return errors.New("billing: no ar_credit_policy org default row, migration 0104 seeds it")
		}
		return errors.New("billing: this branch has no credit policy row of its own, it inherits the org default, so change the org setting")
	}
	return nil
}

// agingSelect is the AR aging projection, bucketed by DUE DATE.
//
// Due date, not issue date, and that is the choice worth flagging. "60 days old" as a credit trigger
// means "60 days LATE", not "60 days since we sent it", on net-30 terms those differ by a month, and
// a policy that confuses them holds accounts that are perfectly current. The aging screen and the
// auto-hold rule read this same query, so the number an operator argues about is the number the
// machine used.
//
// It unions open invoice balances (FINALIZED / PARTIALLY_PAID, plus PAID only for what it still
// owed at a past as-of date) with non-invoice AR movements in customer_transactions (NSF fees,
// adjustments, standalone credits and write-offs, grouped by source_ref_id so a paid NSF fee nets
// to zero on its original date), so the aging grand total ties to ARBalanceCents and GL 1100
// inception to date.
//
// The five C8 bucket columns (Current, Days30, Days60, Days60To90, Days90Plus) are mutually
// exclusive at every day boundary, by construction: the upper bound of one band is exactly one
// less than the lower bound of the next. The same projection feeds both the auto-hold rule and the
// operator's aging summary, so a band total of 30 in the screen matches 30 in the rule and 30 in
// the register's per-row bucket_label derivation, all from one expression.
//
// The balance each document carries is the one it carried AT the as-of instant ($6, the instant the
// handler resolved), not today's. At an as of on or after the CURRENT house day ($7) the figure IS
// the balance: balance_cents is the inception to date figure, every open document on the books is
// read (the finalize cut is a past date's cut; a document issued ahead of the house day by the
// zone's own reading of the seed is on the books at now), so today's aging, today's sweep and
// today's statement run read it directly and no writer can move a balance without today's figures
// moving with it. Only a PAST date rebuilds from the dated acts that moved the balance, and only
// there is a document on the books from its finalize instant ($6, matching the GL journal's
// posted_at so the document and its journal join the books at the same instant): every real
// allocation dated by the instant is a payment received by then, a reversing payment's contra
// allocation gives its share back, a re-application dated by the instant is money applied by then,
// and the dated ledger rows tagged to the document itself (a credit offset, a deposit close, a
// write off) move it by their signed amounts. An invoice paid after the as-of date therefore still
// ages here at its full total, and an invoice finalized after it is not on the books at it. What was
// settled by the instant nets to zero, so only a document open at the date survives the HAVING,
// whatever its status is today. A bounce and a void keep their allocation rows (0450): the application
// stays where it was and the undo is written beside it as a reversal row dated at the undo, so a cheque
// that was good at the as-of date reads as paid there and as owed from the bounce's own instant on, and
// the payment's per-invoice split survives the bounce.
//
// What this costs (S6's P3, measured with EXPLAIN on a migrated database): the three rebuild arms
// are index-backed lookups per document (idx_cust_txn_source on the ledger rows, the
// payment_allocations index on invoice_id, idx_payment_reapplications_invoice), so the cost driver
// is the DOCUMENT COUNT: the join reads every invoice of every customer, the PAID ones included,
// because a document paid after the as-of date must still age at its full total, and the HAVING
// drops the settled ones only after their balance has been rebuilt. A dealer with years of history
// pays three index lookups for each of those documents. The narrowings that would pay it down (a
// settled-after column on the document, or a created_at index on the allocation so a settled
// document can be skipped without a rebuild) are schema changes, and the migration numbers are a
// shared space, so they are a lead's call, not this query's.
const agingSelect = `
	WITH open_ar AS (
		SELECT r.customer_id, r.branch_id,
		       r.raw - LEAST(r.holdback_cents, GREATEST(r.raw, 0)) AS bal,
		       r.due_date
		  FROM (
		SELECT i.customer_id, i.branch_id,
		       GREATEST(i.holdback_cents, 0)
		       + CASE WHEN $7::bool THEN 0 ELSE COALESCE((SELECT -SUM(m.holdback_cents) FROM invoices m
		                   WHERE m.reverses_invoice_id = i.id AND m.doc_type = 'CREDIT_MEMO'
		                     AND m.status IN ('FINALIZED','PARTIALLY_PAID','PAID')
		                     AND COALESCE(m.finalized_at, m.created_at) > $6::timestamptz), 0) END AS holdback_cents,
		       CASE WHEN $7::bool THEN i.balance_cents
		            ELSE i.total_cents
		                 + COALESCE((SELECT SUM(t.amount_cents) FROM customer_transactions t
		                     WHERE t.source_type = 'invoice' AND t.source_ref_id = i.id
		                       AND t.txn_type <> i.doc_type AND t.created_at <= $6::timestamptz), 0)
		                 - COALESCE((SELECT SUM(CASE WHEN p.reverses_payment_id IS NULL THEN a.amount_cents ELSE -a.amount_cents END)
		                     FROM payment_allocations a JOIN payments p ON p.id = a.payment_id
		                     WHERE a.invoice_id = i.id AND a.created_at <= $6::timestamptz), 0)
		                 - COALESCE((SELECT SUM(ra.amount_cents) FROM payment_reapplications ra
		                     WHERE ra.invoice_id = i.id AND ra.created_at <= $6::timestamptz), 0)
		       END AS raw,
		       i.due_date
		  FROM invoices i
		 WHERE i.status IN ('FINALIZED','PARTIALLY_PAID','PAID')
		   AND ($7::bool OR COALESCE(i.finalized_at, i.created_at) <= $6::timestamptz)
		  ) r
		UNION ALL
		SELECT ct.customer_id, ct.branch_id,
		       SUM(ct.amount_cents) AS bal,
		       MIN((ct.occurred_at AT TIME ZONE $5::text)::date) AS due_date
		  FROM customer_transactions ct
		 WHERE ct.txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO','WRITE_OFF')
		   AND ct.source_type <> 'invoice'
		   AND ($7::bool OR ct.created_at <= $6::timestamptz)
		 GROUP BY ct.customer_id, ct.branch_id, ct.source_type, COALESCE(ct.source_ref_id, ct.id)
		HAVING SUM(ct.amount_cents) <> 0
	)
	SELECT c.id::text, c.name, c.account_no::text, COALESCE(c.home_branch_id::text,''),
	       c.payment_terms_days, c.credit_limit_cents,
	       COALESCE(SUM(i.bal) FILTER (WHERE $1::date <= i.due_date), 0),
	       COALESCE(SUM(i.bal) FILTER (WHERE $1::date >  i.due_date AND $1::date <= i.due_date + 30), 0),
	       COALESCE(SUM(i.bal) FILTER (WHERE $1::date >  i.due_date + 30 AND $1::date <= i.due_date + 60), 0),
	       COALESCE(SUM(i.bal) FILTER (WHERE $1::date >  i.due_date + 60 AND $1::date <= i.due_date + 90), 0),
	       COALESCE(SUM(i.bal) FILTER (WHERE $1::date >  i.due_date + 90), 0),
	       COALESCE(SUM(i.bal), 0),
	       COALESCE(SUM(i.bal) FILTER (WHERE $1::date > i.due_date + $2::int), 0),
	       EXISTS (SELECT 1 FROM credit_holds h WHERE h.customer_id = c.id AND h.status = 'ACTIVE'),
	       COALESCE((SELECT h.hold_kind FROM credit_holds h WHERE h.customer_id = c.id AND h.status = 'ACTIVE' LIMIT 1), '')
	  FROM customers c
	  JOIN open_ar i ON i.customer_id = c.id
	 WHERE i.bal <> 0
	   AND ($3 = '' OR i.branch_id = $3::uuid)
	   AND ($4::uuid[] IS NULL OR i.branch_id = ANY($4::uuid[]))
	 GROUP BY c.id, c.name, c.account_no, c.home_branch_id, c.payment_terms_days, c.credit_limit_cents
	HAVING COALESCE(SUM(i.bal), 0) <> 0
	 ORDER BY COALESCE(SUM(i.bal), 0) DESC`

// AgingRows returns one row per account carrying an open balance, across every branch: the sweep's
// and the statement run's read. A request's read goes through AgingRowsIn with the caller's reach.
func (r *Repository) AgingRows(ctx context.Context, asOf time.Time, bucketDays int, branchID string) ([]AgingRow, error) {
	return r.AgingRowsIn(ctx, asOf, bucketDays, branchID, AllBranches)
}

// AgingRowsIn is AgingRows inside a branch reach: only the invoices of the reach are summed, so an
// account that lives partly elsewhere ages for the reach alone.
func (r *Repository) AgingRowsIn(ctx context.Context, asOf time.Time, bucketDays int, branchID string, reach BranchReach) ([]AgingRow, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if bucketDays <= 0 {
		bucketDays = 60
	}
	zone, err := r.zoneFor(ctx)
	if err != nil {
		return nil, err
	}
	day := houseClockIn(zone, asOf)
	today := houseClockIn(zone, time.Now())
	rows, err := r.pool.Query(ctx, agingSelect, day, bucketDays, branchID, reach.SQLArg(), zone.Name(), asOf, onOrAfterHouseDay(day, today))
	if err != nil {
		return nil, fmt.Errorf("billing: aging rows: %w", err)
	}
	defer rows.Close()
	var out []AgingRow
	for rows.Next() {
		var a AgingRow
		if err := rows.Scan(&a.CustomerID, &a.CustomerName, &a.AccountNo, &a.BranchID,
			&a.TermsDays, &a.CreditLimitCents,
			&a.CurrentCents, &a.Days30Cents, &a.Days60Cents, &a.Days60To90Cents, &a.Days90PlusCents,
			&a.TotalCents, &a.PastBucketCents, &a.OnHold, &a.HoldKind); err != nil {
			return nil, fmt.Errorf("billing: scan aging row: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CustomerExists is the tight existence check the C8 S4 manual hold handler and the C8 S3
// paylink handler use to refuse a hold against an absent account or a paylink on nobody.
// The check is one query against customers.id, the same row every other billing read goes
// through. False on no-rows; anything else is a real error.
//
// The customers table is allowlisted for credit_repository.go already (see allowlist), so
// the read lives here rather than in the handler. The handler does no SQL of its own.
func (r *Repository) CustomerExists(ctx context.Context, customerID string) (bool, error) {
	if r.pool == nil {
		return false, errNoDB
	}
	var one int
	err := r.pool.QueryRow(ctx,
		`SELECT 1 FROM customers WHERE id = $1::uuid`, customerID).Scan(&one)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("billing: lookup customer: %w", err)
	}
	return true, nil
}

// PlaceHold inserts an ACTIVE credit hold, returning placed=false when the account already has one.
//
// The no-op-on-existing behaviour is what makes the nightly sweep idempotent, and it is enforced by
// the database (ux_credit_holds_one_active), not by a read-then-write we would have to get right
// under concurrency. The WHERE NOT EXISTS is the fast path; the unique index is the guarantee.
func (r *Repository) PlaceHold(ctx context.Context, customerID, kind, reason, placedBy string) (string, bool, error) {
	q := r.creditChangeQuerier(ctx)
	if q == nil {
		return "", false, errNoDB
	}
	var actor *string
	if placedBy != "" {
		actor = &placedBy
	}
	var id string
	err := q.QueryRow(ctx,
		`INSERT INTO credit_holds (customer_id, status, hold_kind, auto_placed, reason, placed_by)
		 SELECT $1::uuid, 'ACTIVE', $2, $4, $3, $5::uuid
		  WHERE NOT EXISTS (SELECT 1 FROM credit_holds WHERE customer_id = $1::uuid AND status = 'ACTIVE')
		 RETURNING id::text`, customerID, kind, reason, placedBy == "", actor).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil // already held, nothing to do, and that is a success
		}
		return "", false, fmt.Errorf("billing: place hold: %w", err)
	}
	return id, true, nil
}

// holdColumns is the canonical hold SELECT list.
const holdColumns = `h.id::text, h.customer_id::text, COALESCE(c.name,''), COALESCE(c.home_branch_id::text,''), h.status, h.hold_kind,
	h.auto_placed, h.reason, h.override_until, h.override_reason, h.created_at`

func scanHold(row pgx.Row) (CreditHold, error) {
	var h CreditHold
	var until *time.Time
	if err := row.Scan(&h.ID, &h.CustomerID, &h.CustomerName, &h.HomeBranchID, &h.Status, &h.Kind,
		&h.AutoPlaced, &h.Reason, &until, &h.OverrideReason, &h.CreatedAt); err != nil {
		return CreditHold{}, err
	}
	h.OverrideUntil = until
	return h, nil
}

// ActiveHold returns the customer's ACTIVE hold, if any.
func (r *Repository) ActiveHold(ctx context.Context, customerID string) (CreditHold, bool, error) {
	q := r.creditChangeQuerier(ctx)
	if q == nil {
		return CreditHold{}, false, errNoDB
	}
	h, err := scanHold(q.QueryRow(ctx,
		`SELECT `+holdColumns+` FROM credit_holds h
		   LEFT JOIN customers c ON c.id = h.customer_id
		  WHERE h.customer_id = $1::uuid AND h.status = 'ACTIVE'`, customerID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CreditHold{}, false, nil
		}
		return CreditHold{}, false, fmt.Errorf("billing: active hold: %w", err)
	}
	return h, true, nil
}

// ListHolds returns holds filtered by status ("" = all), newest first, across every branch.
func (r *Repository) ListHolds(ctx context.Context, status string) ([]CreditHold, error) {
	return r.ListHoldsIn(ctx, status, AllBranches)
}

// ListHoldsIn is ListHolds inside a branch reach. A hold is on an account, and the book is cut by
// invoice, so a hold lists for a reach when the account has an open invoice in it: the same cut the
// aging makes, so the two screens never disagree about whose account this is.
func (r *Repository) ListHoldsIn(ctx context.Context, status string, reach BranchReach) ([]CreditHold, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+holdColumns+` FROM credit_holds h
		   LEFT JOIN customers c ON c.id = h.customer_id
		  WHERE ($1 = '' OR h.status = $1)
		    AND ($2::uuid[] IS NULL OR EXISTS (
		          SELECT 1 FROM invoices i
		           WHERE i.customer_id = h.customer_id
		             AND i.status IN ('FINALIZED','PARTIALLY_PAID')
		             AND i.branch_id = ANY($2::uuid[])))
		  ORDER BY h.created_at DESC LIMIT 500`, status, reach.SQLArg())
	if err != nil {
		return nil, fmt.Errorf("billing: list holds: %w", err)
	}
	defer rows.Close()
	var out []CreditHold
	for rows.Next() {
		h, err := scanHold(rows)
		if err != nil {
			return nil, fmt.Errorf("billing: scan hold: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// OverrideHold stamps the override window. The guarded WHERE means only an ACTIVE hold can be
// overridden, overriding a released hold would write a permission that refers to nothing.
func (r *Repository) OverrideHold(ctx context.Context, holdID, reason string, until time.Time, actorID string) error {
	if r.pool == nil {
		return errNoDB
	}
	var actor *string
	if actorID != "" {
		actor = &actorID
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE credit_holds
		    SET override_until = $2, override_reason = $3, overridden_by = $4::uuid, updated_at = now()
		  WHERE id = $1::uuid AND status = 'ACTIVE'`, holdID, until, reason, actor)
	if err != nil {
		return fmt.Errorf("billing: override hold: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("billing: hold %s is not ACTIVE, nothing to override", holdID)
	}
	return nil
}

// ReleaseHold releases an AR-placed hold and returns the kind it released.
//
// It REFUSES NSF holds by SQL predicate rather than by a Go check, so the refusal cannot be bypassed
// by a future second caller that forgets it. NSF holds carry a Tier-1 authorization requirement that
// this module does not enforce, so this module does not get to lift them.
func (r *Repository) ReleaseHold(ctx context.Context, holdID, reason, actorID string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var actor *string
	if actorID != "" {
		actor = &actorID
	}
	var kind string
	err := r.pool.QueryRow(ctx,
		`UPDATE credit_holds
		    SET status = 'RELEASED', release_reason = $2, released_by = $3::uuid, released_at = now(), updated_at = now()
		  WHERE id = $1::uuid AND status = 'ACTIVE' AND hold_kind <> 'NSF'
		 RETURNING hold_kind`, holdID, reason, actor).Scan(&kind)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("billing: hold %s is not an ACTIVE non-NSF hold, an NSF hold is released "+
				"through the till's Tier-1 path, which enforces an authorization this module does not", holdID)
		}
		return "", fmt.Errorf("billing: release hold: %w", err)
	}
	return kind, nil
}

// OrderDispatchFacts gathers everything CanDispatch needs in ONE round trip. The gate is on the
// fulfilment hot path, a driver waiting at a gate is not the place to discover we issue four queries.
//
// The override window is evaluated IN SQL against now(), so a stale application clock cannot extend
// somebody's override past its expiry.
func (r *Repository) OrderDispatchFacts(ctx context.Context, orderID string) (DispatchFacts, error) {
	if r.pool == nil {
		return DispatchFacts{}, errNoDB
	}
	var f DispatchFacts
	err := r.pool.QueryRow(ctx,
		`SELECT o.id::text, o.customer_id::text, o.branch_id::text, c.payment_terms_days, COALESCE(c.name,''),
		        COALESCE((SELECT SUM(i.balance_cents) FROM invoices i
		                   WHERE i.order_id = o.id AND i.status IN ('FINALIZED','PARTIALLY_PAID')), 0),
		        EXISTS (SELECT 1 FROM credit_holds h WHERE h.customer_id = o.customer_id AND h.status = 'ACTIVE'),
		        COALESCE((SELECT h.hold_kind FROM credit_holds h
		                   WHERE h.customer_id = o.customer_id AND h.status = 'ACTIVE' LIMIT 1), ''),
		        COALESCE((SELECT h.override_until > now() FROM credit_holds h
		                   WHERE h.customer_id = o.customer_id AND h.status = 'ACTIVE' LIMIT 1), FALSE),
		        EXISTS (SELECT 1 FROM invoices i WHERE i.order_id = o.id AND i.status <> 'DRAFT')
		   FROM orders o
		   JOIN customers c ON c.id = o.customer_id
		  WHERE o.id = $1::uuid`, orderID).
		Scan(&f.OrderID, &f.CustomerID, &f.BranchID, &f.TermsDays, &f.CustomerName,
			&f.UnpaidCents, &f.HoldActive, &f.HoldKind, &f.OverrideInForce, &f.InvoicesFinalized)
	if err != nil {
		return DispatchFacts{}, err
	}
	return f, nil
}

// WorklistFacts gathers unapplied credit memo balances and open/broken promise counts by customer
// for the AR collections worklist.
func (r *Repository) WorklistFacts(ctx context.Context, asOf time.Time, branchID string, reach BranchReach) (map[string]WorklistAccountFacts, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	day, err := r.clockAt(ctx, asOf)
	if err != nil {
		return nil, err
	}
	facts := map[string]WorklistAccountFacts{}

	creditRows, err := r.pool.Query(ctx, `
		SELECT c.id::text, c.name, c.account_no::text, COALESCE(c.home_branch_id::text, ''),
		       c.payment_terms_days, c.credit_limit_cents, COALESCE(SUM(-i.balance_cents), 0)
		  FROM customers c
		  JOIN invoices i ON i.customer_id = c.id
		 WHERE i.status IN ('FINALIZED', 'PARTIALLY_PAID')
		   AND i.doc_type = 'CREDIT_MEMO'
		   AND i.balance_cents < 0
		   AND ($1 = '' OR i.branch_id = $1::uuid)
		   AND ($2::uuid[] IS NULL OR i.branch_id = ANY($2::uuid[]))
		 GROUP BY c.id, c.name, c.account_no, c.home_branch_id, c.payment_terms_days, c.credit_limit_cents`, branchID, reach.SQLArg())
	if err != nil {
		return nil, fmt.Errorf("billing: worklist credits: %w", err)
	}
	defer creditRows.Close()
	for creditRows.Next() {
		var account WorklistAccount
		var cents int64
		if err := creditRows.Scan(&account.CustomerID, &account.CustomerName, &account.AccountNo, &account.BranchID,
			&account.TermsDays, &account.CreditLimitCents, &cents); err != nil {
			return nil, fmt.Errorf("billing: scan worklist credit: %w", err)
		}
		fact := facts[account.CustomerID]
		fact.Account = account
		fact.UnappliedCreditCents = cents
		facts[account.CustomerID] = fact
	}
	if err := creditRows.Err(); err != nil {
		return nil, err
	}

	promiseRows, err := r.pool.Query(ctx, `
		SELECT c.id::text, c.name, c.account_no::text, COALESCE(c.home_branch_id::text, ''),
		       c.payment_terms_days, c.credit_limit_cents,
		       COUNT(*) FILTER (WHERE p.status = 'BROKEN' OR (p.status = 'OPEN' AND p.promised_date < $1::date)),
		       COUNT(*) FILTER (WHERE p.status = 'OPEN' AND p.promised_date = $1::date),
		       COUNT(*) FILTER (WHERE p.status = 'OPEN'),
		       COALESCE(SUM(p.promised_cents) FILTER (WHERE p.status = 'OPEN'), 0),
		       MIN(p.promised_date) FILTER (WHERE p.status = 'OPEN')
		  FROM customers c
		  JOIN ar_promises_to_pay p ON p.customer_id = c.id
		  JOIN invoices i ON i.id = p.invoice_id
		 WHERE p.status IN ('OPEN', 'BROKEN')
		   AND i.status IN ('FINALIZED', 'PARTIALLY_PAID')
		   AND i.balance_cents > 0
		   AND ($2 = '' OR i.branch_id = $2::uuid)
		   AND ($3::uuid[] IS NULL OR i.branch_id = ANY($3::uuid[]))
		 GROUP BY c.id, c.name, c.account_no, c.home_branch_id, c.payment_terms_days, c.credit_limit_cents`, day, branchID, reach.SQLArg())
	if err != nil {
		return nil, fmt.Errorf("billing: worklist promises: %w", err)
	}
	defer promiseRows.Close()
	for promiseRows.Next() {
		var account WorklistAccount
		var broken, due, open int
		var promised int64
		var next *time.Time
		if err := promiseRows.Scan(&account.CustomerID, &account.CustomerName, &account.AccountNo, &account.BranchID,
			&account.TermsDays, &account.CreditLimitCents, &broken, &due, &open, &promised, &next); err != nil {
			return nil, fmt.Errorf("billing: scan worklist promise: %w", err)
		}
		fact := facts[account.CustomerID]
		fact.Account = account
		fact.BrokenPromises = broken
		fact.DuePromises = due
		fact.OpenPromises = open
		fact.PromisedCents = promised
		fact.NextPromiseDate = next
		facts[account.CustomerID] = fact
	}
	if err := promiseRows.Err(); err != nil {
		return nil, err
	}
	return facts, nil
}
