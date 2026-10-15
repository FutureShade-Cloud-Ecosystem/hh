package billing

// register_repository.go, the pgx side of the staff receivables register (DIB-510).
//
// -- THE ONE PROJECTION ------------------------------------------------------------------------
// registerSelect is the ONLY place the register's rows are shaped, and the chip counts are computed
// by counting the SAME predicates over the SAME filtered set in one pass (registerCounts). That is
// deliberate and it is the difference between a chip and a decoration: if the count came from a
// second query with its own WHERE, the first time somebody added a filter to one and not the other
// the screen would say "Overdue 8" above a list of seven.
//
// -- WHAT IS AND IS NOT COMPUTED HERE ----------------------------------------------------------
// Money is read, never derived: subtotal/tax/total/balance/holdback are the stored columns, and the
// only arithmetic in this file is SUM() over payment_allocations (what has actually been applied) and
// the days-past-due integer, both against the SERVICE CLOCK passed in as a parameter so a test can
// pin them and two callers can never disagree about "today".
//
// Aging BUCKETS are not computed here at all. The 30/60/90 buckets belong to agingSelect, which the
// month-end screen and the auto-hold rule both read; a second bucketing here would be the exact
// drift customer_ar_read.go refuses to allow. This file asks only "is it past due, and by how many
// days", a property of one document, not a rollup of an account.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/likeutil"
)

// -- policy -------------------------------------------------------------------------------------

// RegisterPolicyFor resolves the ar_invoice_policy row for a branch: the branch row when one exists,
// otherwise the org default (branch_id IS NULL). A missing org row is a migration failure, not a
// runtime state, it errors rather than defaulting to something permissive in silence (the
// CreditPolicy posture, verbatim).
func (r *Repository) RegisterPolicyFor(ctx context.Context, branchID string) (RegisterPolicy, error) {
	if r.pool == nil {
		return RegisterPolicy{}, errNoDB
	}
	var p RegisterPolicy
	var bid *string
	var tones string
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(branch_id::text,''), account_position_visibility, due_soon_days,
		        reminder_tones, reminder_cooldown_hours, show_credits_default, default_branch_scope,
		        workspace_split_pct, workspace_arrangement
		   FROM ar_invoice_policy
		  WHERE ($1 <> '' AND branch_id = $1::uuid) OR branch_id IS NULL
		  ORDER BY branch_id NULLS LAST
		  LIMIT 1`, branchID).
		Scan(&bid, &p.AccountPositionVisibility, &p.DueSoonDays, &tones, &p.ReminderCooldownHours,
			&p.ShowCreditsDefault, &p.DefaultBranchScope, &p.WorkspaceSplitPct, &p.WorkspaceArrangement)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RegisterPolicy{}, errors.New("billing: no ar_invoice_policy row, migration 0178 seeds the org default")
		}
		return RegisterPolicy{}, fmt.Errorf("billing: read register policy: %w", err)
	}
	if bid != nil {
		p.BranchID = *bid
	}
	p.ReminderTones = splitTones(tones)
	return p, nil
}

// splitTones parses the stored comma list. An empty or all-blank setting means the dealer offers no
// tone at all, which is a real choice (a dealer whose reminders are always the same words) and is
// rendered as a send with no selector rather than as a broken control.
func splitTones(s string) []string {
	out := []string{}
	for _, part := range strings.Split(s, ",") {
		if t := strings.ToUpper(strings.TrimSpace(part)); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// SetRegisterPolicy writes the dealer-configurable knobs of ar_invoice_policy for the org default row
// (branchID "") or an existing branch row. It names its columns: a writer that could be handed a
// whole struct by a well-meaning refactor is a writer that will eventually overwrite a column nobody
// meant to touch (the SetAutoHoldEnabled reasoning, re-applied).
//
// A branch id with no row of its own is NOT silently upgraded into one, it updates nothing and
// reports so, because creating a branch policy row stops that branch inheriting the default, which is
// a different act with different consequences.
func (r *Repository) SetRegisterPolicy(ctx context.Context, p RegisterPolicy) (bool, error) {
	if r.pool == nil {
		return false, errNoDB
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE ar_invoice_policy
		    SET account_position_visibility = $2,
		        due_soon_days               = $3,
		        reminder_tones              = $4,
		        reminder_cooldown_hours     = $5,
		        show_credits_default        = $6,
		        default_branch_scope        = $7,
		        workspace_split_pct         = $8,
		        workspace_arrangement       = $9,
		        updated_at                  = now()
		  WHERE ($1 = '' AND branch_id IS NULL) OR ($1 <> '' AND branch_id = $1::uuid)`,
		p.BranchID, p.AccountPositionVisibility, p.DueSoonDays, strings.Join(p.ReminderTones, ","),
		p.ReminderCooldownHours, p.ShowCreditsDefault, p.DefaultBranchScope,
		p.WorkspaceSplitPct, p.WorkspaceArrangement)
	if err != nil {
		return false, fmt.Errorf("billing: write register policy: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// -- the register projection --------------------------------------------------------------------

// registerSelect projects one register row. $1 is the as-of DATE (the service clock) and $2 the
// due-soon window in days; the caller appends its filter arguments from $3.
//
// The four LATERAL/EXISTS legs are the facts the concept's row carries that live outside `invoices`:
// the open dispute (0177), the live promise (0176), the active credit hold (till's 0035 table, which
// billing already writes), and the last thing we sent about this document plus whether it was opened
// (0175). Each is bounded to one row; none of them can multiply the result set.
const registerSelect = `
	SELECT i.id::text, i.invoice_no::text, i.doc_type,
	       c.id::text, c.name, c.account_no::text, i.branch_id::text,
	       COALESCE(o.id::text,''), COALESCE(o.order_no::text,''), COALESCE(o.po_job_ref,''),
	       COALESCE(sp.display_name,''),
	       i.issue_date, i.due_date,
	       i.total_cents, i.balance_cents, i.holdback_cents, i.status,
	       EXISTS (SELECT 1 FROM ar_invoice_disputes d WHERE d.invoice_id = i.id AND d.status = 'OPEN'),
	       EXISTS (SELECT 1 FROM credit_holds h WHERE h.customer_id = c.id AND h.status = 'ACTIVE'),
	       EXISTS (SELECT 1 FROM ar_invoice_disputes d2 WHERE d2.customer_id = c.id AND d2.status = 'OPEN'),
	       pr.promised_date, COALESCE(pr.promised_cents, 0),
	       snd.sent_at, snd.opened_at, (snd.id IS NOT NULL)
	  FROM invoices i
	  JOIN customers c ON c.id = i.customer_id
	  LEFT JOIN orders o ON o.id = i.order_id
	  LEFT JOIN users sp ON sp.id = o.salesperson_id
	  LEFT JOIN LATERAL (
	        SELECT p.promised_date, p.promised_cents
	          FROM ar_promises_to_pay p
	         WHERE p.invoice_id = i.id AND p.status = 'OPEN'
	         LIMIT 1
	  ) pr ON TRUE
	  LEFT JOIN LATERAL (
	        SELECT s.id, s.sent_at, s.opened_at
	          FROM ar_invoice_deliveries s
	         WHERE s.invoice_id = i.id
	         ORDER BY s.sent_at DESC
	         LIMIT 1
	  ) snd ON TRUE`

// registerAnchor, see registerWhere.
const registerAnchor = "$1::date IS NOT NULL AND $2::int IS NOT NULL"

// registerOrder puts the work first: unpaid documents ahead of settled ones, oldest due date first
// inside each group. A register sorted by issue date buries the ninety-day problem under this
// morning's paperwork.
const registerOrder = `
	 ORDER BY (i.status IN ('FINALIZED','PARTIALLY_PAID')) DESC, i.due_date ASC, i.invoice_no ASC`

// registerWhere builds the filter clause and its arguments, starting at $3.
//
// SEARCH IS ONE FIELD ACROSS FIVE COLUMNS because that is how the question is actually asked at a
// counter: somebody reads out a number and it is an invoice number, or their PO, or the order it came
// from, or their account, or they just say the company name. Making the operator choose which kind of
// number they are holding before they can type it is the sort of thing that gets a screen replaced by
// a spreadsheet.
func registerWhere(f RegisterFilter, args []any) (string, []any) {
	var clauses []string
	if !f.ShowCredits {
		clauses = append(clauses, "i.doc_type = 'INVOICE'")
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		args = append(args, "%"+likeutil.EscapeLikePattern(s)+"%")
		n := len(args)
		clauses = append(clauses, fmt.Sprintf(
			`(i.invoice_no::text ILIKE $%d ESCAPE '\' OR c.name ILIKE $%d ESCAPE '\'
			  OR c.account_no::text ILIKE $%d ESCAPE '\'
			  OR COALESCE(o.order_no::text,'') ILIKE $%d ESCAPE '\'
			  OR COALESCE(o.po_job_ref,'') ILIKE $%d ESCAPE '\')`,
			n, n, n, n, n))
	}
	if f.BranchID != "" {
		args = append(args, f.BranchID)
		clauses = append(clauses, fmt.Sprintf("i.branch_id = $%d::uuid", len(args)))
	}
	// The caller's branch wall, always applied: a named branch only narrows inside it.
	args = append(args, f.Reach.SQLArg())
	clauses = append(clauses, fmt.Sprintf("($%d::uuid[] IS NULL OR i.branch_id = ANY($%d::uuid[]))", len(args), len(args)))
	if f.CustomerID != "" {
		args = append(args, f.CustomerID)
		clauses = append(clauses, fmt.Sprintf("i.customer_id = $%d::uuid", len(args)))
	}
	if f.Salesperson != "" {
		args = append(args, f.Salesperson)
		clauses = append(clauses, fmt.Sprintf("o.salesperson_id = $%d::uuid", len(args)))
	}
	if f.IssuedFrom != nil {
		args = append(args, *f.IssuedFrom)
		clauses = append(clauses, fmt.Sprintf("i.issue_date >= $%d::date", len(args)))
	}
	if f.IssuedTo != nil {
		args = append(args, *f.IssuedTo)
		clauses = append(clauses, fmt.Sprintf("i.issue_date <= $%d::date", len(args)))
	}
	// registerAnchor names $1 and $2 in a tautology. The as-of date and the due-soon window are the
	// first two arguments of EVERY query in this file, the projection, the count and the chip
	// predicates all share one argument list, and Postgres rejects a statement that is handed a
	// parameter it never references. The alternative is renumbering the placeholders per query, which
	// is exactly how a filter ends up applied to the list and not to the count that sits above it.
	clauses = append([]string{registerAnchor}, clauses...)
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// chipPredicates are the derived-status filters, as SQL over $1 (as-of date) and $2 (due-soon days).
//
// OPEN IS THE WHOLE WORKING SET, every unpaid document, current or late, and Due soon / Overdue are
// SUBSETS of it. The alternative reading (open = "not yet due") would make the chips a partition and
// leave the operator with no single click for "everything I still have to collect", which is the one
// thing this screen exists to hand them.
var chipPredicates = map[string]string{
	StatusOpen:          `i.status IN ('FINALIZED','PARTIALLY_PAID')`,
	StatusDueSoon:       `i.status IN ('FINALIZED','PARTIALLY_PAID') AND i.due_date >= $1::date AND i.due_date <= $1::date + $2::int`,
	StatusOverdue:       `i.status IN ('FINALIZED','PARTIALLY_PAID') AND i.due_date < $1::date`,
	StatusDisputed:      `EXISTS (SELECT 1 FROM ar_invoice_disputes cd WHERE cd.invoice_id = i.id AND cd.status = 'OPEN')`,
	StatusPaid:          `i.status = 'PAID'`,
	StatusCredit:        `i.doc_type = 'CREDIT_MEMO'`,
	StatusPartiallyPaid: `i.status = 'PARTIALLY_PAID'`,
}

// RegisterRows reads one page of the register. asOf is the service clock; dueSoonDays comes from
// policy. The returned rows carry NO AccountPosition, the gate is the service's, applied after this
// read, so a repository can never be the thing that leaks it.
func (r *Repository) RegisterRows(ctx context.Context, asOf time.Time, dueSoonDays int, f RegisterFilter) ([]RegisterRow, int, error) {
	if r.pool == nil {
		return nil, 0, errNoDB
	}
	zone, err := r.zoneFor(ctx)
	if err != nil {
		return nil, 0, err
	}
	args := []any{houseClockIn(zone, asOf), dueSoonDays}
	where, args := registerWhere(f, args)
	if pred, ok := chipPredicates[f.Chip]; ok {
		where += " AND (" + pred + ")"
	}

	var total int
	countQ := `SELECT COUNT(*) FROM invoices i JOIN customers c ON c.id = i.customer_id
	           LEFT JOIN orders o ON o.id = i.order_id` + where
	if err := r.pool.QueryRow(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("billing: count register: %w", err)
	}

	q := registerSelect + where + registerOrder
	if !f.All {
		limit := f.Limit
		if limit <= 0 || limit > 200 {
			limit = 50
		}
		args = append(args, limit, max0(f.Offset))
		q += fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	}

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("billing: register rows: %w", err)
	}
	defer rows.Close()

	out := []RegisterRow{}
	for rows.Next() {
		var v RegisterRow
		var promisedDate, sentAt, openedAt *time.Time
		var everSent, onHold, accountDisputed bool
		if err := rows.Scan(&v.ID, &v.InvoiceNo, &v.DocType,
			&v.CustomerID, &v.CustomerName, &v.AccountNo, &v.BranchID,
			&v.OrderID, &v.OrderNo, &v.CustomerPO, &v.Salesperson,
			&v.IssueDate, &v.DueDate,
			&v.TotalCents, &v.BalanceCents, &v.HoldbackCents, &v.StoredStatus,
			&v.Disputed, &onHold, &accountDisputed,
			&promisedDate, &v.PromisedCents,
			&sentAt, &openedAt, &everSent); err != nil {
			return nil, 0, fmt.Errorf("billing: scan register row: %w", err)
		}
		v.PromisedDate = promisedDate
		if promisedDate == nil {
			v.PromisedCents = 0
		}
		v.LastSentAt = sentAt
		// Opened stays ABSENT when we never sent anything: "not opened" is an accusation and we have
		// not earned it if we never put the document in front of them.
		if everSent {
			opened := openedAt != nil
			v.Opened = &opened
		}
		v.AccountStatus = accountStatusFor(onHold, accountDisputed, v.BalanceCents)
		v.Status, v.DaysOverdue = registerStatusIn(zone, v, asOf, dueSoonDays)
		out = append(out, v)
	}
	return out, total, rows.Err()
}

// RegisterCountsFor counts every chip's predicate over the SAME filter set, minus the chip itself.
// One query, one pass, seven FILTER aggregates, so the chips and the list can never disagree.
func (r *Repository) RegisterCountsFor(ctx context.Context, asOf time.Time, dueSoonDays int, f RegisterFilter) (RegisterCounts, error) {
	if r.pool == nil {
		return RegisterCounts{}, errNoDB
	}
	// The chip is deliberately dropped: a chip row whose counts changed when you clicked one of them
	// would make the operator's own click look like the book had moved under them. Credits are always
	// counted (the toggle governs the LIST, not the count that tells you the toggle is worth using).
	f.Chip = ""
	f.ShowCredits = true
	day, err := r.clockAt(ctx, asOf)
	if err != nil {
		return RegisterCounts{}, err
	}
	args := []any{day, dueSoonDays}
	where, args := registerWhere(f, args)

	var c RegisterCounts
	q := fmt.Sprintf(`
		SELECT COUNT(*) FILTER (WHERE %s AND i.doc_type = 'INVOICE'),
		       COUNT(*) FILTER (WHERE %s AND i.doc_type = 'INVOICE'),
		       COUNT(*) FILTER (WHERE %s AND i.doc_type = 'INVOICE'),
		       COUNT(*) FILTER (WHERE %s),
		       COUNT(*) FILTER (WHERE %s AND i.doc_type = 'INVOICE'),
		       COUNT(*) FILTER (WHERE %s)
		  FROM invoices i
		  JOIN customers c ON c.id = i.customer_id
		  LEFT JOIN orders o ON o.id = i.order_id%s`,
		chipPredicates[StatusOpen], chipPredicates[StatusDueSoon], chipPredicates[StatusOverdue],
		chipPredicates[StatusDisputed], chipPredicates[StatusPaid], chipPredicates[StatusCredit], where)
	if err := r.pool.QueryRow(ctx, q, args...).Scan(
		&c.Open, &c.DueSoon, &c.Overdue, &c.Disputed, &c.Paid, &c.Credits); err != nil {
		return RegisterCounts{}, fmt.Errorf("billing: register counts: %w", err)
	}
	return c, nil
}

// SelectionTotals sums the OUTSTANDING balance of an explicit selection, server-side.
//
// The bulk bar's "3 selected · $X" figure is read from here rather than added up in the browser for
// one reason: the operator is about to act on that number, and the action will read the balances
// again from this table. A figure summed on a stale row set would let somebody create a statement for
// an amount nobody can reproduce.
func (r *Repository) SelectionTotals(ctx context.Context, invoiceIDs []string, reach BranchReach) (int64, int, error) {
	if r.pool == nil {
		return 0, 0, errNoDB
	}
	if len(invoiceIDs) == 0 {
		return 0, 0, nil
	}
	var sum int64
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(balance_cents),0), COUNT(*) FROM invoices
		  WHERE id = ANY($1::uuid[]) AND ($2::uuid[] IS NULL OR branch_id = ANY($2::uuid[]))`,
		invoiceIDs, reach.SQLArg()).Scan(&sum, &n); err != nil {
		return 0, 0, fmt.Errorf("billing: selection totals: %w", err)
	}
	return sum, n, nil
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
