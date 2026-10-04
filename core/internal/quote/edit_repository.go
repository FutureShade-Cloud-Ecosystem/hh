// edit_repository.go, the guarded pgx writes behind the staff quote surface (DIB-56).
//
// Every mutation runs this sequence, and the order of the steps IS the correctness argument:
//
//	BEGIN
//	  SELECT status, valid_until FROM quotes WHERE id = $1 FOR UPDATE   ← lock + re-read the committed row
//	  status ∈ allowedFrom ?                                           ← the matrix guard, INSIDE the lock
//	  valid_until not passed ?                                         ← the expiry guard, INSIDE the lock
//	  <mutate the line / the date>
//	  UPDATE quotes SET subtotal_cents = Σ line_total_cents, total_cents = Σ + tax_cents
//	  record(tx, before, after, totals)                                ← the change trail, on THIS tx (R3)
//	COMMIT   ← so the mutation, the totals and the trail are one atomic unit
//
// Why the guards are re-evaluated here when authorizeQuoteEdit already checked them. That check ran
// without a lock, so between it and this write a concurrent acceptance can commit. Re-reading under
// FOR UPDATE closes that window against every writer that respects the lock: such a writer is
// serialized behind us and cannot land between our read and our mutation.
//
// LOCK CONVENTION, the guarantee and its boundary. This is the same standing constraint the order
// module documents (edit_repository.go, B2-2): the guarantee covers writers that TAKE the row lock. It
// does not physically stop a path that mutates a quote's status without holding it, no in-process
// check can. Every existing quote writer does take it (the 0008 transition helper and Convert both use
// FOR UPDATE), and any future one MUST, or this guarantee silently weakens.
//
// Why there is no `precheck` hook here, where the order side has one. That hook exists to run the money
// predicate under the lock. A quote has no money attached to predicate on (R1), the expiry guard, its
// nearest counterpart, is evaluated inline from the same locked read that yields the status, precisely
// so the two can never disagree about which snapshot they saw.
package quote

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// editLineColumns is the projection every line snapshot is read through. qty_entered/qty_base are cast
// to ::text so what is persisted, returned and recorded on the trail is byte-identical to what the
// DECIMAL(19,4) column stores, a float round-trip would put "5" or "5.00000000001" on the trail where
// the column holds "5.0000".
const editLineColumns = `line_no, COALESCE(product_id::text,''), qty_entered::text, qty_base::text,
	unit_price_cents, line_total_cents`

func scanLineSnapshot(row pgx.Row) (QuoteLineSnapshot, error) {
	var s QuoteLineSnapshot
	err := row.Scan(&s.LineNo, &s.ProductID, &s.QtyEntered, &s.QtyBase, &s.UnitPriceCents, &s.LineTotalCents)
	return s, err
}

// - the shared guard -

// lockQuoteForEdit locks the quote row and re-evaluates BOTH guards inside that lock. It returns the
// quote's true current status, read under the lock.
//
// A status outside allowedFrom yields a *NotEditableError with the status named but the Field left
// empty, the calling service method fills the field in, since only it knows which command was
// attempted (nameQuoteField). An expired quote yields *QuoteExpiredError with the date named.
func lockQuoteForEdit(ctx context.Context, tx pgx.Tx, quoteID string, allowedFrom []string, now time.Time) (status string, err error) {
	var validUntil *time.Time
	err = tx.QueryRow(ctx,
		`SELECT status, valid_until FROM quotes WHERE id = $1 FOR UPDATE`, quoteID).Scan(&status, &validUntil)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("lock quote %q for edit: %w", quoteID, err)
	}
	allowed := false
	for _, s := range allowedFrom {
		if s == status {
			allowed = true
			break
		}
	}
	if !allowed {
		return status, &NotEditableError{Status: status}
	}
	if err := assertNotExpired(validUntil, now); err != nil {
		return status, err
	}
	return status, nil
}

// recomputeTotals folds the quote money back up from its lines. Recomputed from Σ, never delta-adjusted
// , a delta is only correct if every prior write was, and a recompute is correct unconditionally.
//
// tax_cents is READ (to fold into total) but never WRITTEN: M5 owns tax, exactly as on the order side.
// (The DIB-506 terms write is the ONE deliberate exception: it stamps the flat-HST header tax with
// M5's own arithmetic, see builder_repository.go UpdateTermsGuarded.)
//
// DIB-506: delivery_fee_cents (0173) joins the fold-up. Zero on every pre-0173 quote and on every
// quote whose terms never set it, so the sum is byte-identical to the shipped behavior there.
func recomputeTotals(ctx context.Context, tx pgx.Tx, quoteID, actorID string) (QuoteTotals, error) {
	var t QuoteTotals
	// The quotes scope's seq rides the fold-up (0213, syncscope.go):
	// a totals change is a header change, and a device's pull must see
	// it the moment it lands.
	err := tx.QueryRow(ctx, quoteSeqCTE+`
		UPDATE quotes SET
			subtotal_cents = COALESCE((SELECT SUM(line_total_cents) FROM quote_lines WHERE quote_id = $1), 0),
			total_cents    = COALESCE((SELECT SUM(line_total_cents) FROM quote_lines WHERE quote_id = $1), 0)
			                 + tax_cents + delivery_fee_cents,
			updated_by     = NULLIF($2,'')::uuid,
			updated_at     = now(),
			seq            = (SELECT next_value FROM qseq)
		WHERE id = $1
		RETURNING subtotal_cents, total_cents`, quoteID, actorID).Scan(&t.SubtotalCents, &t.TotalCents)
	if err != nil {
		return QuoteTotals{}, fmt.Errorf("recompute quote totals: %w", err)
	}
	return t, nil
}

// runRecord invokes the audit record hook on the tx (R3). A nil hook means no transactional recorder is
// wired (a fake that lacks RecordTx); the edit then commits with no trail. In production the hook is
// always present, main.go asserts the wiring at boot.
func runRecord(ctx context.Context, hooks quoteEditHooks, tx pgx.Tx, before, after QuoteLineSnapshot, totals QuoteTotals) error {
	if hooks.record == nil {
		return nil
	}
	return hooks.record(ctx, tx, before, after, totals)
}

// runHolds invokes the W5 soft-hold sync on the tx, after the record hook and before commit. A nil
// hook means the reservation seam is unwired and the quote simply holds nothing.
func runHolds(ctx context.Context, hooks quoteEditHooks, tx pgx.Tx) error {
	if hooks.holds == nil {
		return nil
	}
	return hooks.holds(ctx, tx)
}

// runHoldsAfterInsert is runHolds at the insert call site. Named separately only so the ORDER of the
// two hooks reads deliberately at each site: the sync must see the new line, and it does either way,
// but a reader should not have to check.
func runHoldsAfterInsert(ctx context.Context, hooks quoteEditHooks, tx pgx.Tx) error {
	return runHolds(ctx, hooks, tx)
}

// runPricingLog invokes the pricing log hook on the tx, the LAST step
// before commit: the pricing log rows a write owes ride the same
// transaction as the mutation, the totals and the trail, so a commit with
// no row cannot happen and a rolled back write leaves no row (plan 4.2).
// changed is the write's own verdict on whether it moved the line, so an
// idempotent rewrite writes no row. A nil hook means this write owes no
// pricing log row.
func runPricingLog(ctx context.Context, hook func(context.Context, pgx.Tx, int, bool) error, tx pgx.Tx, lineNo int, changed bool) error {
	if hook == nil {
		return nil
	}
	return hook(ctx, tx, lineNo, changed)
}

// - guarded writes -

// UpdateLineGuarded changes one line's quantity + price under the lock. A
// floor plan whose expectedDisc is set (a repriced discounted line, D5)
// re-checks the stored discount under the lock, consumes any approval on
// this transaction, and stamps the clearing the decision granted.
func (r *Repository) UpdateLineGuarded(ctx context.Context, w quoteEditWrite, plan floorWritePlan, hooks quoteEditHooks) (before, after QuoteLineSnapshot, totals QuoteTotals, status string, err error) {
	if r.pool == nil {
		return before, after, totals, "", errNoDB
	}
	if !looksLikeUUID(w.QuoteID) {
		return before, after, totals, "", ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return before, after, totals, "", fmt.Errorf("begin quote line edit: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	status, err = lockQuoteForEdit(ctx, tx, w.QuoteID, w.AllowedFrom, r.now())
	if err != nil {
		return before, after, totals, status, err
	}
	before, err = scanLineSnapshot(tx.QueryRow(ctx, fmt.Sprintf(
		`SELECT %s FROM quote_lines WHERE quote_id = $1 AND line_no = $2 FOR UPDATE`, editLineColumns),
		w.QuoteID, w.LineNo))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return before, after, totals, status, ErrLineNotFound
		}
		return before, after, totals, status, fmt.Errorf("read quote line pre-image: %w", err)
	}
	// The price was read for the product the caller saw on this line; a row
	// holding another product under the lock (a delete and a re-add inside
	// the pricing window) must never take that price, with or without a
	// floor question (P2-1, round 3).
	if before.ProductID != w.ProductID {
		return before, after, totals, status, errFloorStateChanged
	}
	if plan.expectedDisc >= 0 || plan.lineID != "" {
		// The floor decision priced THIS stored discount on THIS row; a
		// different one under the lock means figures nobody decided on (a
		// discount moved between the read and the lock, or the number was
		// reused by another row inside the evaluation window, P2-A), refuse
		// the write rather than re-discount it blind.
		var disc int
		var rowID string
		if err := tx.QueryRow(ctx,
			`SELECT disc_pct, id::text FROM quote_lines WHERE quote_id = $1 AND line_no = $2`,
			w.QuoteID, w.LineNo).Scan(&disc, &rowID); err != nil {
			return before, after, totals, status, fmt.Errorf("read quote line discount: %w", err)
		}
		if plan.expectedDisc >= 0 && disc != plan.expectedDisc {
			return before, after, totals, status, errFloorStateChanged
		}
		if plan.lineID != "" && plan.lineID != rowID {
			return before, after, totals, status, errFloorStateChanged
		}
	}
	if err := runFloorPlan(ctx, tx, &plan); err != nil {
		return before, after, totals, status, err
	}
	// DIB-506: a reprice PRESERVES the line's stored discount, the engine's gross total lands in
	// pre_discount_total_cents and the stored disc_pct re-discounts it (half-up once). disc_pct = 0
	// (every pre-0173 line, and every line never discounted) makes both expressions the shipped
	// values byte-for-byte.
	after, err = scanLineSnapshot(tx.QueryRow(ctx, fmt.Sprintf(`
		UPDATE quote_lines
		    SET qty_entered = $3::decimal, qty_base = $4::decimal,
		        unit_price_cents = $5, price_uom = $6,
		        line_total_cents = CASE WHEN disc_pct > 0
		                                THEN ($7::bigint * (100 - disc_pct) + 50) / 100
		                                ELSE $7 END,
		        pre_discount_total_cents = $7,
		        metadata = CASE WHEN $8::jsonb IS NULL THEN metadata
		                        ELSE metadata || jsonb_build_object('floor_clear', $8::jsonb) END,
		        updated_at = now()
		  WHERE quote_id = $1 AND line_no = $2
		  RETURNING %s`, editLineColumns),
		w.QuoteID, w.LineNo, w.QtyEntered, w.QtyBase, w.UnitPriceCents, w.PriceUOM, w.LineTotalCents, stampClearingJSON(plan.clearing)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return before, after, totals, status, ErrLineNotFound
		}
		return before, after, totals, status, fmt.Errorf("update quote line: %w", err)
	}
	if totals, err = recomputeTotals(ctx, tx, w.QuoteID, w.ActorID); err != nil {
		return before, after, totals, status, err
	}
	if err = runRecord(ctx, hooks, tx, before, after, totals); err != nil {
		return before, after, totals, status, err
	}
	if err = runHolds(ctx, hooks, tx); err != nil {
		return before, after, totals, status, err
	}
	if err = runPricingLog(ctx, hooks.pricing, tx, w.LineNo, before != after); err != nil {
		return before, after, totals, status, err
	}
	if err := tx.Commit(ctx); err != nil {
		return before, after, totals, status, fmt.Errorf("commit quote line edit: %w", err)
	}
	return before, after, totals, status, nil
}

// errFloorStateChanged is the refusal a guarded write answers when the row
// under the lock is not the row the floor decision priced: the stored
// discount moved between the read and the lock, or the line number was
// reused by another row inside the evaluation window. The decision priced
// figures that are no longer the line's.
var errFloorStateChanged = errors.New("quote: the line changed while its price was being decided, try again")

// InsertLineGuarded appends a line under the lock.
//
// line_no is derived INSIDE the lock (MAX+1) rather than passed in, so two concurrent adds cannot
// collide on the (quote_id, line_no) unique index, they serialize behind the quote row lock and each
// sees the other's committed line.
func (r *Repository) InsertLineGuarded(ctx context.Context, w quoteEditWrite, hooks quoteEditHooks) (after QuoteLineSnapshot, totals QuoteTotals, status string, err error) {
	if r.pool == nil {
		return after, totals, "", errNoDB
	}
	if !looksLikeUUID(w.QuoteID) {
		return after, totals, "", ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return after, totals, "", fmt.Errorf("begin quote line add: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	status, err = lockQuoteForEdit(ctx, tx, w.QuoteID, w.AllowedFrom, r.now())
	if err != nil {
		return after, totals, status, err
	}
	after, err = scanLineSnapshot(tx.QueryRow(ctx, fmt.Sprintf(
		`INSERT INTO quote_lines
		   (quote_id, line_no, product_id, description, qty_entered, uom_entered, qty_base,
		    unit_price_cents, price_uom, line_total_cents, pre_discount_total_cents)
		 VALUES ($1,
		         COALESCE((SELECT MAX(line_no) FROM quote_lines WHERE quote_id = $1), 0) + 1,
		         -- DIB-397: an empty description defaults to the product's name rather than storing
		         -- blank. Defaulting beats rejecting (friendlier, and matches how the staff UI already
		         -- behaves), and doing it in SQL avoids a second round trip, priceLine returns no name.
		         $2::uuid,
		         COALESCE(NULLIF($3,''), (SELECT name FROM products WHERE id = $2::uuid), ''),
		         -- DIB-506: a fresh line's gross equals its net (disc_pct defaults 0).
		         $4::decimal, $5, $6::decimal, $7, $8, $9, $9)
		 RETURNING %s`, editLineColumns),
		w.QuoteID, w.ProductID, w.Description, w.QtyEntered, w.UOMEntered, w.QtyBase,
		w.UnitPriceCents, w.PriceUOM, w.LineTotalCents))
	if err != nil {
		return after, totals, status, fmt.Errorf("insert quote line: %w", err)
	}
	if totals, err = recomputeTotals(ctx, tx, w.QuoteID, w.ActorID); err != nil {
		return after, totals, status, err
	}
	if err = runHoldsAfterInsert(ctx, hooks, tx); err != nil {
		return after, totals, status, err
	}
	if err = runRecord(ctx, hooks, tx, QuoteLineSnapshot{}, after, totals); err != nil {
		return after, totals, status, err
	}
	// The pricing log row (a below floor rule price, CP0-3 answer A) is
	// named by the line the insert derived under the lock; a new line is a
	// change by definition.
	if err = runPricingLog(ctx, hooks.pricing, tx, after.LineNo, true); err != nil {
		return after, totals, status, err
	}
	if err := tx.Commit(ctx); err != nil {
		return after, totals, status, fmt.Errorf("commit quote line add: %w", err)
	}
	return after, totals, status, nil
}

// DeleteLineGuarded removes a line under the lock.
//
// The RETURNING pre-image is the ONLY surviving record of what was removed, once the row is gone the
// change trail is the sole account of it, which is why the record hook runs before the commit rather
// than after it.
func (r *Repository) DeleteLineGuarded(ctx context.Context, quoteID string, lineNo int, allowedFrom []string, actorID string, hooks quoteEditHooks) (before QuoteLineSnapshot, totals QuoteTotals, status string, err error) {
	if r.pool == nil {
		return before, totals, "", errNoDB
	}
	if !looksLikeUUID(quoteID) {
		return before, totals, "", ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return before, totals, "", fmt.Errorf("begin quote line remove: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	status, err = lockQuoteForEdit(ctx, tx, quoteID, allowedFrom, r.now())
	if err != nil {
		return before, totals, status, err
	}
	before, err = scanLineSnapshot(tx.QueryRow(ctx, fmt.Sprintf(
		`DELETE FROM quote_lines WHERE quote_id = $1 AND line_no = $2 RETURNING %s`, editLineColumns),
		quoteID, lineNo))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return before, totals, status, ErrLineNotFound
		}
		return before, totals, status, fmt.Errorf("delete quote line: %w", err)
	}
	if totals, err = recomputeTotals(ctx, tx, quoteID, actorID); err != nil {
		return before, totals, status, err
	}
	if err = runRecord(ctx, hooks, tx, before, QuoteLineSnapshot{}, totals); err != nil {
		return before, totals, status, err
	}
	if err = runHolds(ctx, hooks, tx); err != nil {
		return before, totals, status, err
	}
	if err := tx.Commit(ctx); err != nil {
		return before, totals, status, fmt.Errorf("commit quote line remove: %w", err)
	}
	return before, totals, status, nil
}

// UpdateValidUntilGuarded moves the quote's expiry date under the lock.
//
// The pre-image is read EXPLICITLY under the lock rather than lifted out of a RETURNING subquery: the
// subquery form works, but resting an audit trail on the evaluation order of a correlated subquery
// inside an UPDATE is far too subtle to be worth the saved round trip.
func (r *Repository) UpdateValidUntilGuarded(ctx context.Context, quoteID, newDate string, allowedFrom []string, actorID string, hooks quoteDateHooks) (before, after, status string, err error) {
	if r.pool == nil {
		return "", "", "", errNoDB
	}
	if !looksLikeUUID(quoteID) {
		return "", "", "", ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", "", "", fmt.Errorf("begin quote expiry edit: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	status, err = lockQuoteForEdit(ctx, tx, quoteID, allowedFrom, r.now())
	if err != nil {
		return "", "", status, err
	}
	if err = tx.QueryRow(ctx,
		`SELECT COALESCE(valid_until::text,'') FROM quotes WHERE id = $1`, quoteID).Scan(&before); err != nil {
		return "", "", status, fmt.Errorf("read quote expiry pre-image: %w", err)
	}
	if err = tx.QueryRow(ctx, quoteSeqCTE+`
		UPDATE quotes SET valid_until = NULLIF($2,'')::date, updated_by = NULLIF($3,'')::uuid, updated_at = now(),
		                   seq = (SELECT next_value FROM qseq)
		 WHERE id = $1
		 RETURNING COALESCE(valid_until::text,'')`, quoteID, newDate, actorID).Scan(&after); err != nil {
		return before, "", status, fmt.Errorf("update quote expiry: %w", err)
	}
	if hooks.record != nil {
		if err = hooks.record(ctx, tx, before, after); err != nil {
			return before, after, status, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return before, after, status, fmt.Errorf("commit quote expiry edit: %w", err)
	}
	return before, after, status, nil
}

// - creation -

// CreateQuote inserts a DRAFT quote, minting its number from quote_no_seq as '#Q-<n>'. The
// salesperson is the caller (plan 3.3: creating a quote sets the salesperson to the caller), so
// the quote is born owned by the person who opened it.
//
// The sync legs (0213, syncscope.go) stamp beside the row: the org
// read off the branch (the fence the drive-run adapters stamp the same
// way) and the seq from the quotes scope's allocator, both inside the
// one statement, so a quote the desk opens is visible to a device's
// pull the moment it lands.
func (r *Repository) CreateQuote(ctx context.Context, in QuoteCreate) (id, quoteNo string, err error) {
	if r.pool == nil {
		return "", "", errNoDB
	}
	err = r.pool.QueryRow(ctx, quoteSeqCTE+`
		INSERT INTO quotes (quote_no, customer_id, branch_id, status, valid_until, created_by, salesperson_id, org_id, seq)
		SELECT '#Q-' || nextval('quote_no_seq'), $1::uuid, $2::uuid, 'DRAFT', NULLIF($3,'')::date, NULLIF($4,'')::uuid,
		       NULLIF($4,'')::uuid,
		       (SELECT org_id FROM locations WHERE id = $2::uuid), (SELECT next_value FROM qseq)
		RETURNING id::text, quote_no::text`,
		in.CustomerID, in.BranchID, in.ValidUntil, in.CreatedBy).Scan(&id, &quoteNo)
	if err != nil {
		return "", "", fmt.Errorf("create quote: %w", err)
	}
	return id, quoteNo, nil
}

// - staff reads -

// ResolveQuoteID accepts either a real quote id (a UUID) or the visible quote_no ("#Q-4", or "Q-4" -
// someone reading it off a printout or dictating it may drop the leading '#') and returns the real id.
//
// A UUID passes through UNRESOLVED (no round trip): a cross-branch UUID deep-link stays a 403 downstream
// via the branch check on the loaded quote, preserving that flow's semantics for an in-company link.
//
// A quote_no is looked up BRANCH-SCOPED. Sequential '#Q-' numbers plus a global lookup plus a
// cross-branch 403 would let a branch-A caller confirm branch-B's quotes exist (and count them) by
// iterating numbers. So a scoped caller resolves only quote_nos in a branch they can reach, and a
// cross-branch or absent number maps to the SAME ErrNotFound a missing id does, no oracle. This is the
// B2-4 lesson from DIB-175, applied here from the start rather than as a correction round.
func (r *Repository) ResolveQuoteID(ctx context.Context, idOrNo string, global bool, branchIDs []string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	if looksLikeUUID(idOrNo) {
		return idOrNo, nil
	}
	no := strings.TrimSpace(idOrNo)
	if no == "" {
		return "", ErrNotFound
	}
	if !strings.HasPrefix(no, "#") {
		no = "#" + no
	}
	// A scoped caller with NO branch grants reaches nothing by number, short-circuit rather than issue
	// a `= ANY('{}')` that can never match.
	if !global && len(branchIDs) == 0 {
		return "", ErrNotFound
	}
	var id string
	err := r.pool.QueryRow(ctx,
		`SELECT id::text FROM quotes WHERE quote_no = $1 AND ($2 OR branch_id = ANY($3::uuid[]))`,
		no, global, branchIDs).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("resolve quote_no %q: %w", idOrNo, err)
	}
	return id, nil
}

// QuoteEditHeader reads the four facts every edit guard needs in one query:
// status, owning branch, the validity window, and the quote's owner (the
// salesperson when one is set, else the creator, plan 3.3; "" when neither
// is stamped).
func (r *Repository) QuoteEditHeader(ctx context.Context, quoteID string) (status, branchID string, validUntil *time.Time, ownerID string, err error) {
	if r.pool == nil {
		return "", "", nil, "", errNoDB
	}
	if !looksLikeUUID(quoteID) {
		return "", "", nil, "", ErrNotFound
	}
	err = r.pool.QueryRow(ctx,
		`SELECT status, branch_id::text, valid_until,
		        COALESCE(salesperson_id::text, COALESCE(created_by::text, ''))
		  FROM quotes WHERE id = $1`, quoteID).
		Scan(&status, &branchID, &validUntil, &ownerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", nil, "", ErrNotFound
		}
		return "", "", nil, "", fmt.Errorf("read quote edit header: %w", err)
	}
	return status, branchID, validUntil, ownerID, nil
}

// QuotePricingContext resolves the customer + branch a quote line must be priced for.
func (r *Repository) QuotePricingContext(ctx context.Context, quoteID string) (customerID, branchID string, err error) {
	if r.pool == nil {
		return "", "", errNoDB
	}
	if !looksLikeUUID(quoteID) {
		return "", "", ErrNotFound
	}
	err = r.pool.QueryRow(ctx,
		`SELECT customer_id::text, branch_id::text FROM quotes WHERE id = $1`, quoteID).Scan(&customerID, &branchID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", ErrNotFound
		}
		return "", "", fmt.Errorf("read quote pricing context: %w", err)
	}
	return customerID, branchID, nil
}

// LineForEdit reads one line's current state plus the UOM it was entered in.
func (r *Repository) LineForEdit(ctx context.Context, quoteID string, lineNo int) (QuoteLineSnapshot, string, error) {
	if r.pool == nil {
		return QuoteLineSnapshot{}, "", errNoDB
	}
	if !looksLikeUUID(quoteID) {
		return QuoteLineSnapshot{}, "", ErrNotFound
	}
	var snap QuoteLineSnapshot
	var uom string
	err := r.pool.QueryRow(ctx, fmt.Sprintf(
		`SELECT %s, uom_entered::text FROM quote_lines WHERE quote_id = $1 AND line_no = $2`, editLineColumns),
		quoteID, lineNo).Scan(&snap.LineNo, &snap.ProductID, &snap.QtyEntered, &snap.QtyBase,
		&snap.UnitPriceCents, &snap.LineTotalCents, &uom)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return QuoteLineSnapshot{}, "", ErrLineNotFound
		}
		return QuoteLineSnapshot{}, "", fmt.Errorf("read quote line for edit: %w", err)
	}
	return snap, uom, nil
}

// StaffDetail reads one quote plus its lines for the staff surface.
//
// Both reads run in ONE read-only REPEATABLE READ transaction so the header money and Σ line_total_cents
// come from the same MVCC snapshot. Without it a concurrent edit landing between the two queries would
// serve a header whose subtotal does not equal the lines shown beneath it, a torn read that looks like
// a money bug and is impossible to reproduce.
func (r *Repository) StaffDetail(ctx context.Context, quoteID string) (StaffQuoteView, error) {
	var v StaffQuoteView
	if r.pool == nil {
		return v, errNoDB
	}
	if !looksLikeUUID(quoteID) {
		return v, ErrNotFound
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return v, fmt.Errorf("begin quote detail read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var validUntil *time.Time
	err = tx.QueryRow(ctx, `
		SELECT q.id::text, q.quote_no::text, q.customer_id::text, COALESCE(c.name,''), q.branch_id::text,
		       q.status, q.valid_until, q.subtotal_cents, q.tax_cents, q.total_cents, q.updated_at,
		       COALESCE(q.salesperson_id::text, COALESCE(q.created_by::text, ''))
		  FROM quotes q LEFT JOIN customers c ON c.id = q.customer_id
		 WHERE q.id = $1`, quoteID).
		Scan(&v.ID, &v.QuoteNo, &v.CustomerID, &v.CustomerName, &v.BranchID,
			&v.Status, &validUntil, &v.SubtotalCents, &v.TaxCents, &v.TotalCents, &v.UpdatedAt, &v.OwnerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return v, ErrNotFound
		}
		return v, fmt.Errorf("read quote detail: %w", err)
	}
	v.validUntil = validUntil
	if validUntil != nil {
		v.ValidUntil = validUntil.Format(dateLayout)
	}

	// DIB-397: same blank-description fallback as the public read, staff had the identical gap.
	rows, err := tx.Query(ctx, `
		SELECT ql.line_no, COALESCE(ql.product_id::text,''),
		       COALESCE(NULLIF(ql.description,''), p.name, ''),
		       ql.qty_entered::text, ql.uom_entered::text,
		       ql.qty_base::text, ql.unit_price_cents, ql.price_uom::text, ql.line_total_cents
		  FROM quote_lines ql
		  LEFT JOIN products p ON p.id = ql.product_id
		 WHERE ql.quote_id = $1 ORDER BY ql.line_no`, quoteID)
	if err != nil {
		return v, fmt.Errorf("read quote lines: %w", err)
	}
	defer rows.Close()
	v.Lines = []QuoteViewLine{}
	for rows.Next() {
		var l QuoteViewLine
		if err := rows.Scan(&l.LineNo, &l.ProductID, &l.Description, &l.QtyEntered, &l.UOMEntered,
			&l.QtyBase, &l.UnitPriceCents, &l.PriceUOM, &l.LineTotalCents); err != nil {
			return v, fmt.Errorf("scan quote line: %w", err)
		}
		v.Lines = append(v.Lines, l)
	}
	if err := rows.Err(); err != nil {
		return v, fmt.Errorf("iterate quote lines: %w", err)
	}
	return v, nil
}

// ListStaffQuotes lists quotes the caller can reach, newest first. A
// non-empty ownerID narrows the list to that owner's quotes (a Work
// caller's default, plan 3.3): the owner is the salesperson when one is
// set, else the creator.
func (r *Repository) ListStaffQuotes(ctx context.Context, global bool, branchIDs []string, ownerID string, f Filter) ([]StaffQuoteSummary, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if !global && len(branchIDs) == 0 {
		return []StaffQuoteSummary{}, nil
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	args := []any{global, branchIDs}
	where := "(q.branch_id = ANY($2::uuid[]) OR $1)"
	if ownerID != "" {
		args = append(args, ownerID)
		where += fmt.Sprintf(" AND COALESCE(q.salesperson_id::text, COALESCE(q.created_by::text, '')) = $%d", len(args))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		where += fmt.Sprintf(" AND q.status = $%d", len(args))
	}
	// Cursor pagination (HH-3.2 B4): the resume key is the created_at /
	// quote_no pair (the list's stable ordering; quote_no is unique), NULL
	// on the first page and guarded by the IS NULL leg so the row
	// comparison only evaluates with a key present. No count query: the
	// cursor envelope carries no total.
	var cCreated, cQuoteNo any
	if f.AfterCursor {
		cCreated, cQuoteNo = f.CursorCreated, f.CursorQuoteNo
	}
	args = append(args, cCreated, cQuoteNo, limit)
	rows, err := r.pool.Query(ctx, fmt.Sprintf(`
		SELECT q.id::text, q.quote_no::text, q.customer_id::text, COALESCE(c.name,''), q.branch_id::text,
		       q.status, COALESCE(q.valid_until::text,''), q.subtotal_cents, q.tax_cents, q.total_cents,
		       q.updated_at, q.created_at::text,
		       COALESCE(q.salesperson_id::text, COALESCE(q.created_by::text, ''))
		  FROM quotes q LEFT JOIN customers c ON c.id = q.customer_id
		 WHERE %s AND ($%d::timestamptz IS NULL OR (q.created_at, q.quote_no) < ($%d::timestamptz, $%d::text))
		 ORDER BY q.created_at DESC, q.quote_no LIMIT $%d`,
		where, len(args)-2, len(args)-2, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("list staff quotes: %w", err)
	}
	defer rows.Close()
	out := []StaffQuoteSummary{}
	for rows.Next() {
		var s StaffQuoteSummary
		var createdAt string
		if err := rows.Scan(&s.ID, &s.QuoteNo, &s.CustomerID, &s.CustomerName, &s.BranchID,
			&s.Status, &s.ValidUntil, &s.SubtotalCents, &s.TaxCents, &s.TotalCents, &s.UpdatedAt,
			&createdAt, &s.OwnerID); err != nil {
			return nil, fmt.Errorf("scan staff quote: %w", err)
		}
		s.cursorKey = createdAt + "/" + s.QuoteNo
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate staff quotes: %w", err)
	}
	return out, nil
}

// ActorNames resolves staff display names for a set of audit actor ids, so a change row reads
// "a person with a role" rather than a UUID. A non-staff actor (no users row) is simply absent from the map and
// the caller renders the raw id. Mirrors order.Repository.ActorNames.
func (r *Repository) ActorNames(ctx context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if r.pool == nil || len(ids) == 0 {
		return out, nil
	}
	// Dedup so the ANY($1) array stays tight; skip empties (a system actor).
	seen := map[string]bool{}
	uniq := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		uniq = append(uniq, id)
	}
	if len(uniq) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, display_name FROM users WHERE id = ANY($1::uuid[])`, uniq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// now is the repository's clock for the under-lock expiry guard. Overridable in tests via withNow so a
// date-boundary integration test can pin "today" instead of depending on the wall clock.
func (r *Repository) now() time.Time {
	if r.nowFn == nil {
		return time.Now()
	}
	return r.nowFn()
}

// looksLikeUUID guards every repository entry point: a malformed id would otherwise reach Postgres and
// come back as SQLSTATE 22P02 → an opaque 500, where the honest answer is 404.
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
			isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}
