package quote

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errNoDB = errors.New("quote: database not configured")

// quoteColumns is the canonical quote header SELECT list (kept in one place so
// List/Get/transition scan identically). valid_until is a nullable DATE, read as *time.Time.
const quoteColumns = `id::text, quote_no::text, customer_id::text, status, valid_until,
	subtotal_cents, tax_cents, total_cents, created_at`

// Repository owns reads of quotes + quote_lines (pgx) and the in-place status
// transitions. Convert coordinates the order write (a separate, gated path).
type Repository struct {
	pool *pgxpool.Pool // nil in scaffold/tests
	// nowFn is the clock the under-lock expiry guard reads (DIB-56 / D2). Injectable so a
	// date-boundary test can pin "today" rather than depending on the wall clock, a test that only
	// passes on some days is worse than no test. nil = time.Now.
	nowFn func() time.Time
	// credit is the DIB-387 credit/hold gate Convert consults before it writes an order (credit.go).
	// nil = the identity-only default; main.go injects till.HoldAwareCreditGate.
	credit CreditGate
	// stock is the W5 seam-3 reservation port. Convert is where SOFT becomes HARD: the quote's soft
	// holds are CONSUMED and the new order's hard holds are raised, inside THIS transaction, because
	// the order id the hard hold points at does not exist until the INSERT a few lines below.
	// See stock_seam.go for why the boundary is Convert and not Accept. nil = the seam is unwired and
	// conversion behaves exactly as it did before this wave.
	stock StockReserver
}

// WithStockReserver injects the reservation port into the conversion path (main.go wires the
// *inventory.Service adapter).
func (r *Repository) WithStockReserver(s StockReserver) *Repository { r.stock = s; return r }

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// withNow pins the repository's expiry clock (tests only).
func (r *Repository) withNow(f func() time.Time) *Repository { r.nowFn = f; return r }

func scanQuote(row pgx.Row) (Quote, error) {
	var q Quote
	var validUntil *time.Time // nullable column
	if err := row.Scan(
		&q.ID, &q.QuoteNo, &q.CustomerID, &q.Status, &validUntil,
		&q.SubtotalCents, &q.TaxCents, &q.TotalCents, &q.CreatedAt,
	); err != nil {
		return Quote{}, err
	}
	if validUntil != nil {
		q.ValidUntil = *validUntil
	}
	return q, nil
}

// ListQuotes returns a tenant-scoped, optionally status-filtered page. Lines are
// not joined here. Cursor pagination (HH-3.2 fold), the tenant half: stable-ordering
// basis created_at DESC, quote_no (unique), the same basis the staff list carries;
// the cursor is the created_at/quote_no pair of the last kept row.
func (r *Repository) ListQuotes(ctx context.Context, customerID string, f Filter) ([]Quote, error) {
	if r.pool == nil {
		return nil, errNoDB
	}

	where := "customer_id = $1"
	args := []any{customerID}
	if f.Status != "" {
		args = append(args, f.Status)
		where += fmt.Sprintf(" AND status = $%d", len(args))
	}
	var cCreated, cQuoteNo any
	if f.AfterCursor {
		cCreated, cQuoteNo = f.CursorCreated, f.CursorQuoteNo
	}
	args = append(args, cCreated, cQuoteNo, f.Limit)
	q := fmt.Sprintf(
		"SELECT %s FROM quotes WHERE %s AND ($%d::timestamptz IS NULL OR created_at < $%d::timestamptz OR (created_at = $%d::timestamptz AND quote_no > $%d::text)) ORDER BY created_at DESC, quote_no LIMIT $%d",
		quoteColumns, where, len(args)-2, len(args)-2, len(args)-2, len(args)-1, len(args),
	)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list quotes: %w", err)
	}
	defer rows.Close()

	var out []Quote
	for rows.Next() {
		qt, err := scanQuote(rows)
		if err != nil {
			return nil, fmt.Errorf("scan quote: %w", err)
		}
		out = append(out, qt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// GetQuote loads one quote WITH its lines, scoped to the tenant. A missing or
// cross-tenant id maps pgx.ErrNoRows to ErrNotFound (404, never 403).
func (r *Repository) GetQuote(ctx context.Context, customerID, quoteID string) (Detail, error) {
	if r.pool == nil {
		return Detail{}, errNoDB
	}

	q := fmt.Sprintf("SELECT %s FROM quotes WHERE customer_id = $1 AND id = $2", quoteColumns)
	qt, err := scanQuote(r.pool.QueryRow(ctx, q, customerID, quoteID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Detail{}, ErrNotFound
		}
		return Detail{}, fmt.Errorf("get quote %q: %w", quoteID, err)
	}
	d := Detail{Quote: qt}

	// DIB-397: blank-description fallback, as on the public and staff-edit reads.
	rows, err := r.pool.Query(ctx,
		`SELECT ql.line_no, ql.product_id::text,
			COALESCE(NULLIF(ql.description,''), p.name, ''),
			ql.qty_entered::text, ql.uom_entered::text,
			ql.unit_price_cents, ql.line_total_cents
		 FROM quote_lines ql
		 LEFT JOIN products p ON p.id = ql.product_id
		 WHERE ql.quote_id = $1 ORDER BY ql.line_no`, quoteID)
	if err != nil {
		return Detail{}, fmt.Errorf("get quote lines %q: %w", quoteID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var ln Line
		var productID *string // nullable column
		if err := rows.Scan(
			&ln.LineNo, &productID, &ln.Description, &ln.QtyEntered, &ln.UOMEntered,
			&ln.UnitPriceCents, &ln.LineTotalCents,
		); err != nil {
			return Detail{}, fmt.Errorf("scan quote line: %w", err)
		}
		if productID != nil {
			ln.ProductID = *productID
		}
		d.Lines = append(d.Lines, ln)
	}
	if err := rows.Err(); err != nil {
		return Detail{}, err
	}
	return d, nil
}

// Accept transitions SENT -> ACCEPTED for the tenant's quote, atomically.
//
// DIB-491: an ALREADY-accepted quote no longer answers a bare "invalid state transition". With
// multi-accept OFF (the default) the refusal names the order the first acceptance became and the way
// forward; with it ON the re-accept is an idempotent success, the portal button re-asserts a fact
// that is already true, and refusing a no-op would turn a double-click into a red banner (the
// SetAutoHold rule). Every other wrong state keeps the original refusal verbatim.
func (r *Repository) Accept(ctx context.Context, customerID, quoteID string) (Quote, error) {
	if r.pool == nil {
		return Quote{}, errNoDB
	}
	q, err := r.transition(ctx, customerID, quoteID, "ACCEPTED", "status = 'SENT'")
	if err == nil || !errors.Is(err, ErrInvalidTransition) {
		return q, err
	}
	// The transition refused on state (transition() already disambiguated not-found). Re-read the
	// tenant's row to see whether "wrong state" means "already accepted".
	cur, gerr := scanQuote(r.pool.QueryRow(ctx,
		fmt.Sprintf("SELECT %s FROM quotes WHERE customer_id = $1 AND id = $2", quoteColumns),
		customerID, quoteID))
	if gerr != nil {
		return Quote{}, ErrInvalidTransition
	}
	if cur.Status != "ACCEPTED" && cur.Status != "CONVERTED" {
		return Quote{}, ErrInvalidTransition
	}
	allow, aerr := r.AllowMultiAccept(ctx)
	if aerr != nil {
		return Quote{}, ErrInvalidTransition // an unreadable switch keeps the restrictive refusal
	}
	if allow {
		return cur, nil
	}
	return Quote{}, r.alreadyAcceptedErr(ctx, r.pool, quoteID, cur.Status)
}

// Reject transitions SENT/ACCEPTED -> LOST for the tenant's quote.
func (r *Repository) Reject(ctx context.Context, customerID, quoteID string) (Quote, error) {
	if r.pool == nil {
		return Quote{}, errNoDB
	}
	return r.transition(ctx, customerID, quoteID, "LOST", "status IN ('SENT','ACCEPTED')")
}

// transition runs a conditional, tenant-scoped status UPDATE that only fires from a
// legal source state (fromPredicate is a fixed SQL fragment, never caller input). A zero
// RETURNING means either the row does not exist for this tenant (ErrNotFound) or it does
// but is in the wrong source state (ErrInvalidTransition) - a follow-up existence check
// disambiguates so a wrong-state quote never leaks as a 404 nor a foreign quote as a 409.
func (r *Repository) transition(ctx context.Context, customerID, quoteID, newStatus, fromPredicate string) (Quote, error) {
	q := fmt.Sprintf(
		`UPDATE quotes SET status=$3, updated_at=now()
		 WHERE id=$2 AND customer_id=$1 AND %s
		 RETURNING %s`, fromPredicate, quoteColumns)
	qt, err := scanQuote(r.pool.QueryRow(ctx, q, customerID, quoteID, newStatus))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			ok, eerr := r.quoteExists(ctx, customerID, quoteID)
			if eerr != nil {
				return Quote{}, fmt.Errorf("transition quote %q: existence check: %w", quoteID, eerr)
			}
			if !ok {
				return Quote{}, ErrNotFound
			}
			return Quote{}, ErrInvalidTransition
		}
		return Quote{}, fmt.Errorf("transition quote %q: %w", quoteID, err)
	}
	return qt, nil
}

// quoteExists reports whether the tenant owns a quote with this id.
func (r *Repository) quoteExists(ctx context.Context, customerID, quoteID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM quotes WHERE id=$2 AND customer_id=$1)`,
		customerID, quoteID).Scan(&exists)
	return exists, err
}

// Convert creates an order from an ACCEPTED quote and marks the quote CONVERTED, in one
// transaction. The row is locked FOR UPDATE so concurrent converts cannot double-issue an
// order.
//
// DIB-387: the credit/hold gate runs HERE, inside that transaction, between the lock and the order
// INSERT, the conversion is the order write, so this is the only place the gate cannot be skipped by a
// caller. The facts are read under the same lock as the INSERT, so there is no check→write window, and a
// refusal returns ErrCreditBlocked with NO orders row written (the tx is rolled back by the defer).
// DIB-483: opt carries the FULFILMENT choice made at conversion (convert.go). An empty
// opt.Fulfillment leaves the column out of the INSERT entirely, so 0008's DEFAULT 'PICKUP' applies and
// the statement is the one that shipped, the zero-delta property convert_default_test.go pins.
func (r *Repository) Convert(ctx context.Context, customerID, quoteID string, opt ConvertOptions) (ConvertResult, error) {
	if r.pool == nil {
		return ConvertResult{}, errNoDB
	}
	fulCols, fulVals, err := fulfillmentColumns(opt.Fulfillment)
	if err != nil {
		return ConvertResult{}, err
	}
	// JA-2: the job the order is opened on, resolved and fenced by runConvert before this
	// transaction began. No job named splices nothing, and the INSERT is the one that shipped.
	jobCols, jobVals := jobColumns(opt.JobID != "")

	// QD-5: when the payment door carries its transaction, the conversion runs on it and commits with
	// it (or not at all); every other caller gets the transaction of its own, exactly as before.
	tx, carried := txFrom(ctx)
	if !carried {
		var err error
		tx, err = r.pool.Begin(ctx)
		if err != nil {
			return ConvertResult{}, fmt.Errorf("convert quote %q: begin: %w", quoteID, err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
	}

	// Lock the quote row and verify it is convertible. quote_no rides along for the provenance row
	// (DIB-491), read under the same lock so the snapshot cannot tear against the INSERT below.
	var status, quoteNo string
	err = tx.QueryRow(ctx,
		`SELECT status, quote_no::text FROM quotes WHERE id=$2 AND customer_id=$1 FOR UPDATE`,
		customerID, quoteID).Scan(&status, &quoteNo)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ConvertResult{}, ErrNotFound
		}
		return ConvertResult{}, fmt.Errorf("convert quote %q: load: %w", quoteID, err)
	}
	// FROM-PRINCIPAL 121: a keyed repeat answers the FIRST order and writes nothing, whatever the quote's
	// status is now and whatever the multi accept switch says. It runs under the lock just taken and
	// before every status check, because a request that timed out at the client may have committed
	// after the client looked again (convert_idempotency.go).
	if opt.IdempotencyKey != "" {
		prior, found, ferr := r.findKeyedConversion(ctx, tx, quoteID, opt.IdempotencyKey, opt.Fulfillment)
		if ferr != nil {
			return ConvertResult{}, ferr
		}
		if found {
			return prior, nil
		}
	}
	if status != "ACCEPTED" {
		// DIB-491 (D-097): a CONVERTED quote may be converted AGAIN, minting a duplicate order -
		// but only when the dealer has switched multi-accept ON. The switch is read on THIS
		// transaction, under the lock just taken, for the same reason DIB-393 re-reads expiry under
		// the lock: only the transaction that writes can know the fact it writes against. OFF (the
		// shipped default) refuses with the order the first acceptance became, named, not a bare
		// "invalid state transition" that sends the counter hunting.
		if status != "CONVERTED" {
			return ConvertResult{}, ErrInvalidTransition
		}
		allow, aerr := r.allowMultiAcceptTx(ctx, tx)
		if aerr != nil {
			return ConvertResult{}, fmt.Errorf("convert quote %q: %w", quoteID, aerr)
		}
		if !allow {
			return ConvertResult{}, r.alreadyAcceptedErr(ctx, tx, quoteID, status)
		}
	}

	// DIB-387 credit/hold gate. Read the account posture under the quote lock and consult the same gate
	// the POS ring consults (till.HoldAwareCreditGate, wired in main.go). FAIL CLOSED: a customer row we
	// cannot read is a customer we cannot clear, so the conversion is refused rather than let through.
	facts, err := r.convertCreditFacts(ctx, tx, customerID, quoteID)
	if err != nil {
		return ConvertResult{}, err
	}
	if opt.paidAtCounter && facts.TotalCents != opt.paidTotalCents {
		// The payment was validated against a total the lock no longer sees: refuse, write nothing.
		return ConvertResult{}, &OrderPaymentConflictError{Msg: "the quote's total changed while the payment was being taken; read it again"}
	}
	if err := r.checkCredit(ctx, tx, facts, !opt.paidAtCounter); err != nil {
		return ConvertResult{}, err
	}

	// Create the order header from the quote, drawing a fresh BRANCH-CODED order_no (DIB-388). This
	// call site used to draw a bare nextval('order_no_seq')::text, a number with no '#SO-' prefix,
	// which order.Repository.ResolveOrderID (which normalizes by prepending '#') could never find, so a
	// converted order was unreachable by the only identifier the customer was ever shown. next_order_no
	// (migration 0087) is now the single mint for all three order writers.
	//
	// DIB-483: the fulfilment splice. With no choice made, fulCols/fulVals are empty and this is the
	// statement that shipped, byte for byte (convert.go: convertInsertSQL). JA-2's job splice rides
	// the same rule: with no job named, jobCols/jobVals are empty too.
	var res ConvertResult
	convertArgs := []any{customerID, quoteID}
	if opt.JobID != "" {
		convertArgs = append(convertArgs, opt.JobID)
	}
	err = tx.QueryRow(ctx, convertInsertSQL(fulCols, fulVals, jobCols, jobVals),
		convertArgs...).Scan(&res.OrderID, &res.OrderNo)
	if err != nil {
		return ConvertResult{}, fmt.Errorf("convert quote %q: insert order: %w", quoteID, err)
	}

	// Copy the quote lines into order lines verbatim (the entered/base qty trio is preserved).
	if _, err := tx.Exec(ctx,
		`INSERT INTO order_lines
			(order_id, line_no, product_id, description, qty_entered, uom_entered, qty_base,
			 unit_price_cents, price_uom, line_total_cents, metadata)
		 SELECT $1, line_no, product_id,
			 CASE WHEN disc_pct > 0 THEN description || ' (' || disc_pct::text || '% off)' ELSE description END,
			 qty_entered, uom_entered, qty_base,
			 unit_price_cents, price_uom, line_total_cents,
			 CASE WHEN disc_pct > 0
			      THEN jsonb_build_object('disc_pct', disc_pct, 'pre_discount_total_cents', pre_discount_total_cents)
			      ELSE '{}'::jsonb END
		 FROM quote_lines WHERE quote_id=$2 ORDER BY line_no`,
		res.OrderID, quoteID); err != nil {
		return ConvertResult{}, fmt.Errorf("convert quote %q: copy lines: %w", quoteID, err)
	}

	// Mark the quote converted. On a multi-accept re-convert the status is already CONVERTED and this
	// re-asserts it, the updated_at bump is the honest part (the quote's family just grew).
	if _, err := tx.Exec(ctx, quoteSeqCTE+`
		UPDATE quotes SET status='CONVERTED', updated_at=now(), seq = (SELECT next_value FROM qseq)
		 WHERE id=$2 AND customer_id=$1`,
		customerID, quoteID); err != nil {
		return ConvertResult{}, fmt.Errorf("convert quote %q: mark converted: %w", quoteID, err)
	}

	// DIB-491 (D-097): provenance, on THIS transaction, never best-effort. Every order minted from a
	// quote records which numbered acceptance it was, which door it came through, and who, the facts
	// the related-documents view renders. The door/actor pair is read off the request context
	// (convertAttribution) so the conversion seam's signature stays exactly what the one-seam test
	// pins: identical arguments from both doors.
	origin, actorID := convertAttribution(ctx, customerID)
	if err := r.recordAcceptProvenanceTx(ctx, tx, quoteID, quoteNo, res.OrderID, res.OrderNo, origin, actorID); err != nil {
		return ConvertResult{}, err
	}
	if opt.IdempotencyKey != "" {
		if err := recordKeyedConversion(ctx, tx, opt, quoteID, status, res); err != nil {
			return ConvertResult{}, err
		}
	}

	// W5 seam 3, SOFT becomes HARD, in this transaction.
	//
	// The soft holds close as CONSUMED, not RELEASED: the interest they represented did not evaporate,
	// it turned into a commitment. A week later "why did that hold end" has two different answers and
	// the terminal status is the only place they are distinguishable.
	//
	// The hard holds are then raised against the ORDER lines just inserted, keyed by the new order id.
	// Both halves share this transaction and therefore the order INSERT's fate: a conversion that
	// rolls back leaves the soft holds exactly as they were.
	if err := r.convertHoldsTx(ctx, tx, quoteID, res.OrderID); err != nil {
		return ConvertResult{}, err
	}

	if !carried {
		if err := tx.Commit(ctx); err != nil {
			return ConvertResult{}, fmt.Errorf("convert quote %q: commit: %w", quoteID, err)
		}
	}
	return res, nil
}

// convertHoldsTx performs the soft→hard conversion inside Convert's transaction. A nil seam is a
// no-op: the feature is off and conversion behaves as it did before W5.
func (r *Repository) convertHoldsTx(ctx context.Context, tx pgx.Tx, quoteID, orderID string) error {
	if r.stock == nil {
		return nil
	}
	if _, err := r.stock.ReleaseBySourceTx(ctx, tx, ResSourceQuote, quoteID, ResConsumed); err != nil {
		return fmt.Errorf("convert quote %q: release soft holds: %w", quoteID, err)
	}
	var branchID string
	if err := tx.QueryRow(ctx, `SELECT branch_id::text FROM orders WHERE id = $1`, orderID).
		Scan(&branchID); err != nil {
		return fmt.Errorf("convert quote %q: order branch: %w", quoteID, err)
	}
	rows, err := tx.Query(ctx, `
		SELECT COALESCE(product_id::text,''), qty_base
		  FROM order_lines WHERE order_id = $1 ORDER BY line_no`, orderID)
	if err != nil {
		return fmt.Errorf("convert quote %q: read order lines: %w", quoteID, err)
	}
	type ln struct {
		productID string
		qtyBase   float64
	}
	var lines []ln
	for rows.Next() {
		var l ln
		if err := rows.Scan(&l.productID, &l.qtyBase); err != nil {
			rows.Close()
			return fmt.Errorf("convert quote %q: scan order line: %w", quoteID, err)
		}
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, l := range lines {
		// Free-text and zero-quantity lines have no stock to hold, the same skip the order module
		// makes on confirm, made here so a converted order and a manually confirmed one hold the same
		// set of things.
		if l.productID == "" || l.qtyBase <= 0 {
			continue
		}
		if _, err := r.stock.ReserveTx(ctx, tx, StockReserveInput{
			ProductID:   l.productID,
			BranchID:    branchID,
			ResType:     ResTypeOrder,
			QtyBase:     l.qtyBase,
			SourceType:  ResSourceOrder,
			SourceRefID: orderID,
			Reason:      "quote converted to order",
			// EnforceAvailable false: a conversion must record the commitment even when it overdraws.
			// Refusing here would lose an order the customer has already accepted.
			EnforceAvailable: false,
		}); err != nil {
			return fmt.Errorf("convert quote %q: hard-reserve order line: %w", quoteID, err)
		}
	}
	return nil
}

// replayedOrder reads the number and fulfilment of the order a keyed conversion made, for the replay
// (convert_idempotency.go). It runs on the conversion's transaction, under the quote row lock.
func replayedOrder(ctx context.Context, tx pgx.Tx, orderID string) (ConvertResult, string, error) {
	out := ConvertResult{OrderID: orderID}
	var fulfillment string
	if err := tx.QueryRow(ctx, `SELECT order_no::text, fulfillment FROM orders WHERE id = $1::uuid`, orderID).
		Scan(&out.OrderNo, &fulfillment); err != nil {
		return ConvertResult{}, "", fmt.Errorf("read the keyed conversion's order: %w", err)
	}
	return out, fulfillment, nil
}
