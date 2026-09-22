package order

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errNoDB = errors.New("order: database not configured")

// Repository owns reads of orders + order_lines (pgx) and the delivery_instructions
// write (0027). It reads manifests (0012) for delivery status when that surface lands.
type Repository struct {
	pool *pgxpool.Pool // nil in scaffold/tests
}

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// orderColumns is the canonical orders header SELECT list (kept in one place so
// List/Get scan identically). quote_id and requested_date are nullable.
const orderColumns = `id::text, order_no::text, customer_id::text, quote_id::text, status,
	fulfillment, requested_date, subtotal_cents, tax_cents, total_cents, created_at`

// orderLineColumns is the canonical order_lines SELECT list. product_id is nullable and
// the quantity trio is emitted as text so the contractor sees exact DECIMAL values.
const orderLineColumns = `line_no, product_id::text, description, qty_entered::text,
	uom_entered::text, qty_base::text, allocated_base::text, fulfilled_base::text,
	unit_price_cents, line_total_cents`

type rowScanner interface{ Scan(dest ...any) error }

// scanOrder reads the canonical header columns into an Order, mapping the nullable
// quote_id/requested_date to their zero values when SQL NULL.
func scanOrder(row rowScanner) (Order, error) {
	var o Order
	var quoteID *string
	var requestedDate *time.Time
	if err := row.Scan(
		&o.ID, &o.OrderNo, &o.CustomerID, &quoteID, &o.Status,
		&o.Fulfillment, &requestedDate, &o.SubtotalCents, &o.TaxCents, &o.TotalCents, &o.CreatedAt,
	); err != nil {
		return Order{}, err
	}
	if quoteID != nil {
		o.QuoteID = *quoteID
	}
	if requestedDate != nil {
		o.RequestedDate = *requestedDate
	}
	return o, nil
}

// ListOrders returns a tenant-scoped, optionally status-filtered, paginated page plus
// the matching total.
func (r *Repository) ListOrders(ctx context.Context, customerID string, f Filter) ([]Order, int, error) {
	if r.pool == nil {
		return nil, 0, errNoDB
	}
	where := "customer_id = $1"
	args := []any{customerID}
	if f.Status != "" {
		args = append(args, f.Status)
		where += fmt.Sprintf(" AND status = $%d", len(args))
	}

	var total int
	if err := r.pool.QueryRow(ctx, "SELECT COUNT(*) FROM orders WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count orders: %w", err)
	}

	q := fmt.Sprintf("SELECT %s FROM orders WHERE %s ORDER BY created_at DESC, order_no LIMIT $%d OFFSET $%d",
		orderColumns, where, len(args)+1, len(args)+2)
	args = append(args, f.Limit, f.Offset)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// GetOrder loads one order WITH lines + delivery_instructions, scoped to the tenant.
// A missing or cross-tenant id maps to ErrNotFound (never forbidden).
func (r *Repository) GetOrder(ctx context.Context, customerID, orderID string) (Detail, error) {
	if r.pool == nil {
		return Detail{}, errNoDB
	}
	// A malformed (non-UUID) id short-circuits to ErrNotFound before the query, orders.id is a uuid
	// column, so it would otherwise raise SQLSTATE 22P02 → HTTP 500. Mirrors OrderBranch and keeps a
	// garbage id indistinguishable from an absent/cross-tenant one.
	if !looksLikeUUID(orderID) {
		return Detail{}, ErrNotFound
	}

	var d Detail
	var quoteID *string
	var requestedDate *time.Time
	q := fmt.Sprintf("SELECT %s, COALESCE(delivery_instructions, '') FROM orders WHERE id = $1 AND customer_id = $2", orderColumns)
	err := r.pool.QueryRow(ctx, q, orderID, customerID).Scan(
		&d.ID, &d.OrderNo, &d.CustomerID, &quoteID, &d.Status,
		&d.Fulfillment, &requestedDate, &d.SubtotalCents, &d.TaxCents, &d.TotalCents, &d.CreatedAt,
		&d.DeliveryInstructions,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Detail{}, ErrNotFound
		}
		return Detail{}, fmt.Errorf("get order %q: %w", orderID, err)
	}
	if quoteID != nil {
		d.QuoteID = *quoteID
	}
	if requestedDate != nil {
		d.RequestedDate = *requestedDate
	}

	lq := fmt.Sprintf("SELECT %s FROM order_lines WHERE order_id = $1 ORDER BY line_no", orderLineColumns)
	rows, err := r.pool.Query(ctx, lq, orderID)
	if err != nil {
		return Detail{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var l Line
		var productID *string
		if err := rows.Scan(
			&l.LineNo, &productID, &l.Description, &l.QtyEntered, &l.UOMEntered,
			&l.QtyBase, &l.AllocatedBase, &l.FulfilledBase, &l.UnitPriceCents, &l.LineTotalCents,
		); err != nil {
			return Detail{}, err
		}
		if productID != nil {
			l.ProductID = *productID
		}
		d.Lines = append(d.Lines, l)
	}
	if err := rows.Err(); err != nil {
		return Detail{}, err
	}
	return d, nil
}

// SetDeliveryInstructions updates the note ONLY when the order belongs to the tenant.
// RowsAffected == 0 (missing or cross-tenant) maps to ErrNotFound.
func (r *Repository) SetDeliveryInstructions(ctx context.Context, customerID, orderID, text string) error {
	if r.pool == nil {
		return errNoDB
	}
	if !looksLikeUUID(orderID) {
		return ErrNotFound
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE orders SET delivery_instructions = $3, updated_at = now() WHERE id = $2 AND customer_id = $1`,
		customerID, orderID, text)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// OrderBranch resolves the owning branch_id of one order (migration 0008) so the staff-gated
// history read can branch-scope the caller (mirroring the till payment-history reads, which scope
// on the payment/invoice branch_id). This read is NOT tenant-scoped, it is the staff (counter)
// surface, and the branch check is the horizontal-access boundary. A missing id maps to ErrNotFound
// so a caller can never distinguish "no such order" from "an order I may not reach" via this read.
//
// A malformed (non-UUID) id short-circuits to ErrNotFound BEFORE the query: orders.id is a
// Postgres uuid column, so passing a non-UUID string to `WHERE id = $1` would raise
// "invalid input syntax for type uuid" (SQLSTATE 22P02) and surface as an HTTP 500. Validating
// here keeps the not-found-vs-forbidden contract intact, a garbage id is indistinguishable from
// a nonexistent one, and never leaks a database error to the caller.
func (r *Repository) OrderBranch(ctx context.Context, orderID string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	if !looksLikeUUID(orderID) {
		return "", ErrNotFound
	}
	var branchID string
	err := r.pool.QueryRow(ctx,
		`SELECT branch_id::text FROM orders WHERE id = $1`, orderID).Scan(&branchID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("order branch %q: %w", orderID, err)
	}
	return branchID, nil
}

// Brief is the narrow, staff-side order projection a cross-module consumer reads through an interface
// (e.g. the tasks module's OrderReader) so it never JOINs orders directly, just id/branch/fulfilment/
// status. Not tenant-scoped: the consumer is staff-gated at its own route (mirrors OrderBranch).
type Brief struct {
	ID          string `json:"id"`
	BranchID    string `json:"branch_id"`
	Fulfillment string `json:"fulfillment"`
	Status      string `json:"status"`
}

// OrderBrief resolves one order's id/branch/fulfilment/status for a cross-module consumer (the tasks
// module validates an order exists + copies its branch onto a task through this narrow seam, NEVER a
// JOIN). Mirrors OrderBranch: not tenant-scoped, and a malformed/absent id maps to ErrNotFound (so a
// consumer can never distinguish "no such order" from a garbage id, and no existence leaks).
func (r *Repository) OrderBrief(ctx context.Context, orderID string) (Brief, error) {
	if r.pool == nil {
		return Brief{}, errNoDB
	}
	if !looksLikeUUID(orderID) {
		return Brief{}, ErrNotFound
	}
	var b Brief
	err := r.pool.QueryRow(ctx,
		`SELECT id::text, branch_id::text, fulfillment, status FROM orders WHERE id = $1`, orderID).
		Scan(&b.ID, &b.BranchID, &b.Fulfillment, &b.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Brief{}, ErrNotFound
		}
		return Brief{}, fmt.Errorf("order brief %q: %w", orderID, err)
	}
	return b, nil
}

// CustomerForOrder resolves one order's owning customer id. It closes the W3-D declared gap: the yard's
// fulfilment notice (sms.dispatch.notice) had nobody to resolve because every order→customer path this
// module exposed was ALREADY customer-scoped, GetOrder takes the customer id it would have to return,
// so it can confirm a guess but cannot answer the question. This is the answer, and nothing else.
//
// IT IS NOT TENANT-SCOPED, deliberately, and mirrors OrderBranch/OrderBrief exactly: the consumer is a
// server-side delivery path with no tenant of its own, and a scoped signature would just push the same
// unanswered question one layer out. A malformed or absent id is ErrNotFound, never a distinguishable
// "no such order", so the seam cannot be used as an order-existence oracle.
//
// THE CONSUMER GETS AN ID, NOT A CUSTOMER. messagingprovider.ContactSource maps a reference to a
// customer id and reads the number from customer_sms_prefs; handing back anything richer here would put
// a second, unconsented contact path within reach of the delivery code (see recipients_db.go's header).
func (r *Repository) CustomerForOrder(ctx context.Context, orderID string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	if !looksLikeUUID(orderID) {
		return "", ErrNotFound
	}
	var customerID string
	err := r.pool.QueryRow(ctx,
		`SELECT customer_id::text FROM orders WHERE id = $1`, orderID).Scan(&customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("order customer %q: %w", orderID, err)
	}
	return customerID, nil
}

// looksLikeUUID reports whether s is a canonical 36-char 8-4-4-4-12 hex UUID (the shape of an
// orders.id). It mirrors the audit package's validator so a malformed id can be rejected before it
// reaches a Postgres uuid column (which would otherwise raise SQLSTATE 22P02 → HTTP 500).
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

// ResolveOrderID accepts either a real order id (a UUID) or the visible order_no ("#SO-4" as the
// terminal renders it, or "SO-4", a cashier reading it off a receipt or dictating it may drop the
// leading '#') and returns the order's real id (DIB-379/F3).
//
// A UUID passes through UNRESOLVED (no round trip): a cross-branch UUID deep-link stays a 403 downstream
// (the branch check on the loaded order), preserving that flow's semantics.
//
// An order_no is looked up by number, CITEXT, so case-insensitive, after normalizing a missing leading
// '#'. B2-4: the lookup is BRANCH-SCOPED. Sequential #SO- numbers plus a global lookup plus a cross-branch
// 403 would let a branch-A caller confirm branch-B's orders exist (and count them) by iterating numbers.
// So a non-global caller resolves only order_nos in a branch they can reach; a cross-branch (or absent)
// number maps to the SAME ErrNotFound a missing id does, indistinguishable from "no such order", no
// oracle. global=true (a GLOBAL-scoped caller) reaches every branch, as it does everywhere else.
//
// DIB-388: BOTH number forms resolve, the legacy global "#SO-4" and the branch-coded "#SO-TRT-1041"
// (migration 0087), with or without the leading '#'. That falls out of the design on purpose: the
// normalize-then-look-up-as-is rule makes no assumption about what follows "#SO-", so the mint's format
// can evolve without a second code path here. What it CANNOT tolerate is a stored number with no '#'
// (the pre-0087 quote-conversion damage), normalization would look for "#4" and never find "4", which
// is exactly what 0087's backfill repairs. The branch scoping above is unchanged and still does the
// enumeration work: a branch-coded number is, if anything, a weaker oracle than a global one.
func (r *Repository) ResolveOrderID(ctx context.Context, idOrNo string, global bool, branchIDs []string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	if looksLikeUUID(idOrNo) {
		return idOrNo, nil
	}
	// DIB-513, BOTH SPELLINGS RESOLVE.
	//
	// This used to force a leading '#' onto whatever it was given and match that one string. The
	// orders table does not agree with it: the seeded demo alone holds '#SO-DPA3-CRIT' AND
	// 'SO-2026-0001', because the number is minted by several writers and only some of them prefix
	// it. So an order whose stored order_no has no '#' was unreachable by number, proved against a
	// live stack, as the caller who owns the number:
	//
	//   "SO-2026-0001"   -> "I can't find an order numbered SO-2026-0001."
	//   "#SO-2026-0001"  -> "I can't find an order numbered #SO-2026-0001."
	//
	// Both spellings, on an order that exists and that this caller may see. A person reading a
	// number off a printed ticket has no way to know which of the two the database happens to hold,
	// and "it does not exist" is the one answer that is never true.
	//
	// So the query accepts EITHER form of what was typed. The branch fence is untouched and still
	// does the deciding: a cross-branch number answers not-found exactly as before (B2-4).
	bare := strings.TrimPrefix(idOrNo, "#")
	hashed := "#" + bare
	// A scoped caller with NO branch grants reaches nothing by number, short-circuit to not-found rather
	// than issue a `= ANY('{}')` that can never match (and would still be a query round trip).
	if !global && len(branchIDs) == 0 {
		return "", ErrNotFound
	}
	var id string
	// ORDER BY puts the '#' spelling first so a database that somehow held both forms of one number
	// resolves deterministically rather than by whichever the planner reached first.
	err := r.pool.QueryRow(ctx,
		`SELECT id::text FROM orders
		  WHERE order_no IN ($1, $4) AND ($2 OR branch_id = ANY($3::uuid[]))
		  ORDER BY (order_no = $1) DESC
		  LIMIT 1`,
		hashed, global, branchIDs, bare).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("resolve order_no %q: %w", idOrNo, err)
	}
	return id, nil
}

// UpdateStatus is the DIB-159 status-transition write: it moves ONE order from its
// current status to newStatus and RETURNS the prior status, all in a single statement
// so the from→to pair the audit record needs is captured atomically with the write (no
// separate read that could race). The `status IS DISTINCT FROM` guard makes a no-op
// transition (to == from) return ErrNoTransition so the caller emits no spurious history
// row. A missing id (or, when tenant is non-empty, a cross-tenant id) returns ErrNotFound.
//
// tenant is the customer_id scope for a portal-originated transition; pass "" for a
// staff/ERP transition that is not tenant-scoped (e.g. a fulfilment status move). The
// RETURNING clause reads the pre-image inside the same UPDATE, so the choke-point never
// loses history to a concurrent writer.
func (r *Repository) UpdateStatus(ctx context.Context, tenant, orderID, newStatus string) (oldStatus string, err error) {
	if r.pool == nil {
		return "", errNoDB
	}
	// One statement, two behaviours distinguished by whether ANY row matched the id:
	//   - id absent (or cross-tenant)         → no row → pgx.ErrNoRows → ErrNotFound
	//   - id present but status == newStatus  → row exists but the DISTINCT-FROM guard
	//                                            skips the write → we detect via a CTE that
	//                                            always returns the current row.
	//
	// CONCURRENCY (the from_status-must-be-the-true-pre-image invariant): `cur` takes a
	// `FOR UPDATE` ROW LOCK on the target order. Under READ COMMITTED a `SELECT … FOR UPDATE`
	// waits for any in-flight writer to commit and then re-reads the LATEST committed row, so
	// the status `cur` sees is the true immediately-prior value, never a stale pre-image from
	// the statement-start snapshot. `upd` then joins `cur` (same locked row) and writes only on
	// a real change, so the from→to pair the audit record captures is exactly the transition
	// that landed. Without the lock, a concurrent committed writer could slip between `cur`'s
	// snapshot read and `upd`'s re-read, and we would record a from_status that never actually
	// preceded the write (a false history edge). The lock serialises the choke-point per order.
	q := `
WITH cur AS (
    SELECT id, status FROM orders
    WHERE id = $1 AND ($2 = '' OR customer_id = $2::uuid)
    FOR UPDATE
),
upd AS (
    UPDATE orders SET status = $3, updated_at = now()
    FROM cur
    WHERE orders.id = cur.id AND orders.status IS DISTINCT FROM $3
    RETURNING orders.id
)
SELECT cur.status, (SELECT count(*) FROM upd) AS changed FROM cur`
	var changed int
	err = r.pool.QueryRow(ctx, q, orderID, tenant, newStatus).Scan(&oldStatus, &changed)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("update order %q status: %w", orderID, err)
	}
	if changed == 0 {
		// The order exists but is already in newStatus, a no-op, not history.
		return oldStatus, ErrNoTransition
	}
	return oldStatus, nil
}

// OrderHeader reads the order's CURRENT status and owning branch in a single query, backing the staff
// order read (B2/R6). Status comes from orders.status, the source of truth, never from the audit
// timeline, whose writes are best-effort. A malformed (non-UUID) id short-circuits to ErrNotFound before
// the query, since orders.id is a uuid column and a garbage id would otherwise raise SQLSTATE 22P02 →
// HTTP 500 (mirrors OrderBranch / UpdateStatusFrom).
func (r *Repository) OrderHeader(ctx context.Context, orderID string) (status, branchID string, err error) {
	if r.pool == nil {
		return "", "", errNoDB
	}
	if !looksLikeUUID(orderID) {
		return "", "", ErrNotFound
	}
	err = r.pool.QueryRow(ctx,
		`SELECT status, COALESCE(branch_id::text, '') FROM orders WHERE id = $1`, orderID).Scan(&status, &branchID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", ErrNotFound
		}
		return "", "", fmt.Errorf("read order header %q: %w", orderID, err)
	}
	return status, branchID, nil
}

// UpdateStatusFrom is the guarded status-transition write for the DIB-191 MANUAL forward-only path: it
// moves the order to newStatus ONLY when its current status is one of allowedFrom, RETURNING the prior
// status atomically. This closes the check-then-write race the manual matrix would otherwise have, the
// legality (current ∈ allowedFrom) is evaluated INSIDE the same locked UPDATE as the write, so a
// concurrent transition can never slip an illegal move through (the same FOR UPDATE row-lock discipline
// UpdateStatus uses for its pre-image). It is NOT tenant-scoped: the manual path is staff/ERP and branch
// scope is enforced above it (Service.TransitionManual on the resolved caller).
//
// Outcomes, distinguished by whether a row matched and whether it changed:
//   - id absent                        → pgx.ErrNoRows → ErrNotFound
//   - current status ∉ allowedFrom     → row exists, guard skips the write → ErrIllegalTransition
//   - current status == newStatus      → row exists, DISTINCT-FROM skips the write → ErrNoTransition
//   - otherwise                        → the write landed; returns the true pre-image
func (r *Repository) UpdateStatusFrom(ctx context.Context, orderID, newStatus string, allowedFrom []string) (oldStatus string, err error) {
	if r.pool == nil {
		return "", errNoDB
	}
	// A malformed (non-UUID) id short-circuits to ErrNotFound before the query, orders.id is a uuid
	// column, so a garbage id would otherwise raise SQLSTATE 22P02 → HTTP 500 (mirrors OrderBranch).
	if !looksLikeUUID(orderID) {
		return "", ErrNotFound
	}
	// `cur` takes a FOR UPDATE row lock and re-reads the LATEST committed status (so the guard is
	// evaluated against the true immediately-prior value, never a stale snapshot); `upd` joins the same
	// locked row and writes only when current ∈ allowedFrom AND the status actually changes.
	q := `
WITH cur AS (
    SELECT id, status FROM orders
    WHERE id = $1
    FOR UPDATE
),
upd AS (
    UPDATE orders SET status = $2, updated_at = now()
    FROM cur
    WHERE orders.id = cur.id
      AND cur.status = ANY($3::text[])
      AND orders.status IS DISTINCT FROM $2
    RETURNING orders.id
)
SELECT cur.status, (SELECT count(*) FROM upd) AS changed FROM cur`
	var changed int
	err = r.pool.QueryRow(ctx, q, orderID, newStatus, allowedFrom).Scan(&oldStatus, &changed)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("guarded update order %q status: %w", orderID, err)
	}
	if changed == 0 {
		if oldStatus == newStatus {
			// Already in the requested state, a benign no-op (e.g. a double-clicked "Mark picking").
			return oldStatus, ErrNoTransition
		}
		// The order exists but its current status is not a legal source for this move (forward-only).
		return oldStatus, ErrIllegalTransition
	}
	return oldStatus, nil
}
