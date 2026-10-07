// share_repository.go, pgx persistence for DIB-57 share links + acceptance.
package quote

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const shareLinkCols = `id::text, quote_id::text, expires_at, revoked_at,
	COALESCE(created_by::text,''), created_at`

func scanShareLink(row pgx.Row) (ShareLink, error) {
	var l ShareLink
	err := row.Scan(&l.ID, &l.QuoteID, &l.ExpiresAt, &l.RevokedAt, &l.CreatedBy, &l.CreatedAt)
	return l, err
}

// CreateShareLink stores a link by TOKEN HASH. The raw token never reaches this layer.
func (r *Repository) CreateShareLink(ctx context.Context, quoteID, tokenHash string, expiresAt time.Time, createdBy string) (ShareLink, error) {
	if r.pool == nil {
		return ShareLink{}, errNoDB
	}
	if !looksLikeUUID(quoteID) {
		return ShareLink{}, ErrNotFound
	}
	l, err := scanShareLink(r.pool.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO quote_share_links (quote_id, token_hash, expires_at, created_by)
		VALUES ($1::uuid, $2, $3, NULLIF($4,'')::uuid)
		RETURNING %s`, shareLinkCols), quoteID, tokenHash, expiresAt, createdBy))
	if err != nil {
		return ShareLink{}, fmt.Errorf("create share link: %w", err)
	}
	return l, nil
}

func (r *Repository) ListShareLinks(ctx context.Context, quoteID string) ([]ShareLink, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if !looksLikeUUID(quoteID) {
		return nil, ErrNotFound
	}
	rows, err := r.pool.Query(ctx, fmt.Sprintf(
		`SELECT %s FROM quote_share_links WHERE quote_id = $1 ORDER BY created_at DESC`, shareLinkCols),
		quoteID)
	if err != nil {
		return nil, fmt.Errorf("list share links: %w", err)
	}
	defer rows.Close()
	out := []ShareLink{}
	for rows.Next() {
		l, err := scanShareLink(rows)
		if err != nil {
			return nil, fmt.Errorf("scan share link: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// RevokeShareLink marks a link dead. Already-revoked links keep their original revoked_at, revocation
// is not an event you can restamp.
func (r *Repository) RevokeShareLink(ctx context.Context, quoteID, linkID string, at time.Time) error {
	if r.pool == nil {
		return errNoDB
	}
	if !looksLikeUUID(quoteID) || !looksLikeUUID(linkID) {
		return ErrLinkNotFound
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE quote_share_links SET revoked_at = COALESCE(revoked_at, $3)
		  WHERE id = $2::uuid AND quote_id = $1::uuid`, quoteID, linkID, at)
	if err != nil {
		return fmt.Errorf("revoke share link: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLinkNotFound
	}
	return nil
}

// ResolveShareToken looks a token hash up WITHOUT judging liveness, the caller needs to see a dead row
// to answer 410 rather than 404.
func (r *Repository) ResolveShareToken(ctx context.Context, tokenHash string) (ShareLink, string, error) {
	if r.pool == nil {
		return ShareLink{}, "", errNoDB
	}
	l, err := scanShareLink(r.pool.QueryRow(ctx, fmt.Sprintf(
		`SELECT %s FROM quote_share_links WHERE token_hash = $1`, shareLinkCols), tokenHash))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ShareLink{}, "", ErrLinkNotFound
		}
		return ShareLink{}, "", fmt.Errorf("resolve share token: %w", err)
	}
	return l, l.QuoteID, nil
}

// LogShareEvent records a VIEW or ACCEPT against a token (R4).
func (r *Repository) LogShareEvent(ctx context.Context, shareLinkID, event string) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO quote_share_link_events (share_link_id, event) VALUES ($1::uuid, $2)`,
		shareLinkID, event)
	return err
}

// PublicQuote reads the customer-visible projection.
//
// The SELECT list is the allowlist in SQL form: it names exactly the columns a customer may see, so a
// column added to `quotes` later cannot leak through this path by default. Note what is absent -
// branch_id, created_by, updated_by, metadata, product_id, qty_base, price_uom.
//
// One read-only REPEATABLE READ transaction, so the header money and the lines a customer is asked to
// agree to always come from a single snapshot.
func (r *Repository) PublicQuote(ctx context.Context, quoteID string) (PublicQuoteView, error) {
	var v PublicQuoteView
	if r.pool == nil {
		return v, errNoDB
	}
	if !looksLikeUUID(quoteID) {
		return v, ErrNotFound
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return v, fmt.Errorf("begin public quote read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	err = tx.QueryRow(ctx, `
		SELECT q.quote_no::text, COALESCE(c.name,''), q.status, COALESCE(q.valid_until::text,''),
		       q.subtotal_cents, q.tax_cents, q.total_cents, q.updated_at,
		       q.customer_notes, q.deposit_required, q.deposit_pct, q.include_images,
		       q.fulfillment, q.delivery_address, COALESCE(q.requested_date::text,''), q.delivery_fee_cents,
		       COALESCE((SELECT qp.quantity_notice FROM quote_policy qp WHERE qp.branch_id = q.branch_id),
		                (SELECT qp.quantity_notice FROM quote_policy qp WHERE qp.branch_id IS NULL), $2)
		  FROM quotes q LEFT JOIN customers c ON c.id = q.customer_id
		 WHERE q.id = $1`, quoteID, DefaultQuantityNotice).
		Scan(&v.QuoteNo, &v.CustomerName, &v.Status, &v.ValidUntil,
			&v.SubtotalCents, &v.TaxCents, &v.TotalCents, &v.AsOf,
			&v.CustomerNotes, &v.DepositRequired, &v.DepositPct, &v.IncludeImages,
			&v.Fulfillment, &v.DeliveryAddress, &v.RequestedDate, &v.DeliveryFeeCents, &v.QuantityNotice)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return v, ErrNotFound
		}
		return v, fmt.Errorf("read public quote: %w", err)
	}
	// DIB-397: fall back to the product name when the stored description is blank. Doing this in the
	// READ repairs every row that is already blank, with no backfill migration; the write-side default
	// in InsertLineGuarded stops new ones being created. Both are needed, the read alone would leave
	// blanks in the table to propagate through Convert into order_lines.
	// DIB-506: the line read also carries the discount facts and, ONLY when the dealer's
	// include-images choice is on, a thumbnail reference for products that actually have a primary
	// image (share.go PublicQuoteLine.ImageRef states the rule).
	rows, err := tx.Query(ctx, `
		SELECT ql.line_no, COALESCE(NULLIF(ql.description,''), p.name, ''),
		       ql.qty_entered::text, ql.uom_entered::text,
		       ql.unit_price_cents, ql.line_total_cents,
		       ql.disc_pct, ql.pre_discount_total_cents,
		       CASE WHEN $2 AND EXISTS (SELECT 1 FROM product_images pi
		                                 WHERE pi.product_id = ql.product_id AND pi.sort_order = 0)
		            THEN COALESCE(ql.product_id::text,'') ELSE '' END
		  FROM quote_lines ql
		  LEFT JOIN products p ON p.id = ql.product_id
		 WHERE ql.quote_id = $1 ORDER BY ql.line_no`, quoteID, v.IncludeImages)
	if err != nil {
		return v, fmt.Errorf("read public quote lines: %w", err)
	}
	defer rows.Close()
	v.Lines = []PublicQuoteLine{}
	for rows.Next() {
		var l PublicQuoteLine
		if err := rows.Scan(&l.LineNo, &l.Description, &l.QtyEntered, &l.UOMEntered,
			&l.UnitPriceCents, &l.LineTotalCents, &l.DiscPct, &l.PreDiscountTotalCents, &l.ImageRef); err != nil {
			return v, fmt.Errorf("scan public quote line: %w", err)
		}
		v.Lines = append(v.Lines, l)
	}
	if v.DepositRequired && v.DepositPct > 0 {
		v.DepositCents = (v.TotalCents*int64(v.DepositPct) + 50) / 100
	}
	return v, rows.Err()
}

// MarkSent moves DRAFT -> SENT under the quote row lock, recording the transition on the same tx.
// Idempotent: an already-SENT quote is a no-op, not an error, because minting a second link for a
// quote already out with a customer is a legitimate act.
func (r *Repository) MarkSent(ctx context.Context, quoteID, actorID string, hooks quoteDateHooks) error {
	if r.pool == nil {
		return errNoDB
	}
	if !looksLikeUUID(quoteID) {
		return ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin mark sent: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	if err := tx.QueryRow(ctx,
		`SELECT status FROM quotes WHERE id = $1 FOR UPDATE`, quoteID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lock quote for send: %w", err)
	}
	if status == "SENT" {
		return nil // already out with the customer
	}
	if status != "DRAFT" {
		return &NotEditableError{Field: "share_link", Status: status}
	}
	// DIB-395: refuse the FIRST mint of an empty quote. Without this, an empty DRAFT is flipped to SENT,
	// the public view reports acceptable:true, and an anonymous caller can sign a $0 attestation for a
	// quote with nothing on it, which is then convertible to an order.
	//
	// This counts LINES, never money. A fully-discounted or warranty-replacement quote legitimately
	// totals $0 and must still be sendable; guarding on total would refuse a real business case.
	//
	// Counted inside the same transaction as the status flip and under the row lock taken above, so a
	// line cannot be removed between the check and the transition.
	var lineCount int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM quote_lines WHERE quote_id = $1`, quoteID).Scan(&lineCount); err != nil {
		return fmt.Errorf("count quote lines for send: %w", err)
	}
	if lineCount == 0 {
		return ErrEmptyQuote
	}
	if _, err := tx.Exec(ctx, quoteSeqCTE+`
		UPDATE quotes SET status='SENT', updated_by = NULLIF($2,'')::uuid, updated_at = now(),
		                   seq = (SELECT next_value FROM qseq)
		  WHERE id = $1`, quoteID, actorID); err != nil {
		return fmt.Errorf("mark quote sent: %w", err)
	}
	if hooks.record != nil {
		if err := hooks.record(ctx, tx, "DRAFT", "SENT"); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// AcceptQuoteGuarded flips SENT -> ACCEPTED under the quote row lock and writes the acceptance record
// plus the audit row on the SAME transaction.
//
// The expiry guard runs under the lock here too: a quote that lapsed between the customer loading the
// page and pressing accept must not be acceptable, and only a re-read under the lock can know that.
func (r *Repository) AcceptQuoteGuarded(ctx context.Context, in acceptWrite, hooks acceptHooks) (AcceptanceResult, error) {
	var out AcceptanceResult
	if r.pool == nil {
		return out, errNoDB
	}
	if !looksLikeUUID(in.QuoteID) {
		return out, ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return out, fmt.Errorf("begin quote acceptance: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status, quoteNo string
	var validUntil *time.Time
	var subtotal, tax, total int64
	if err := tx.QueryRow(ctx, `
		SELECT status, quote_no::text, valid_until, subtotal_cents, tax_cents, total_cents
		  FROM quotes WHERE id = $1 FOR UPDATE`, in.QuoteID).
		Scan(&status, &quoteNo, &validUntil, &subtotal, &tax, &total); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrNotFound
		}
		return out, fmt.Errorf("lock quote for acceptance: %w", err)
	}
	// QD-3d: a retry under the key of an earlier acceptance through this link answers that first
	// outcome and writes nothing, whatever the quote's status is now. It runs here, under the lock and
	// before every status check, because a request that timed out at the client may have committed
	// after the client looked again (share_idempotency.go). The deferred rollback ends this tx.
	if in.IdempotencyKey != "" {
		if hooks.record == nil {
			return out, errors.New("quote acceptance: an idempotency key needs the audit hook")
		}
		prior, found, ferr := r.findKeyedAcceptance(ctx, tx, in)
		if ferr != nil {
			return AcceptanceResult{}, ferr
		}
		if found {
			return prior, nil
		}
		// QD-3d review 3: the link was live when the request resolved it, and may have died while
		// this request waited for a connection or for this lock. A keyed retry that arrived in that
		// window was answered 410 (its own lookup found nothing), so this attempt must not commit
		// behind it: the link is read again here, under the lock, against the database clock read
		// now (clock_timestamp, never in.Now and not this transaction's start), so whichever request
		// takes the quote row lock first, a keyed 410 is honest. Keyed attempts only: an unkeyed
		// accept is unchanged. A link row that is gone counts as dead.
		var live bool
		if lerr := tx.QueryRow(ctx, `
			SELECT revoked_at IS NULL AND expires_at > clock_timestamp()
			  FROM quote_share_links WHERE id = $1::uuid`, in.ShareLinkID).Scan(&live); lerr != nil {
			if errors.Is(lerr, pgx.ErrNoRows) {
				return out, ErrLinkGone
			}
			return out, fmt.Errorf("re-read share link for acceptance: %w", lerr)
		}
		if !live {
			return out, ErrLinkGone
		}
	}
	repeat := false
	if status != "SENT" {
		// DIB-491 (D-097): a quote that was ALREADY accepted may be accepted again, but only when
		// the dealer has switched multi-accept ON, and the switch is read on THIS transaction under
		// the lock just taken (the DIB-393 discipline: only the transaction that writes can know the
		// fact it writes against). OFF, the shipped, restrictive default, refuses with the order
		// the first acceptance became, named, so the person reading the message knows where the
		// document went. Every other state keeps the DIB-393 refusal verbatim.
		switch status {
		case "ACCEPTED", "CONVERTED":
			allow, aerr := r.allowMultiAcceptTx(ctx, tx)
			if aerr != nil {
				return out, aerr
			}
			if !allow {
				return out, r.alreadyAcceptedErr(ctx, tx, in.QuoteID, status)
			}
			repeat = true
		default:
			// Not a NotEditableError: the customer is not editing. Naming the state still matters, so
			// the message says what happened rather than just refusing.
			return out, fmt.Errorf("%w (it is %s)", ErrNotAcceptable, status)
		}
	}
	if err := assertNotExpired(validUntil, in.Now); err != nil {
		return out, err
	}
	// A repeat acceptance does NOT touch the status: ACCEPTED stays ACCEPTED, and CONVERTED must not
	// be dragged backwards to ACCEPTED, the quote's family of orders is the record of what it became,
	// and this transaction's quote_acceptances row is the record of the repeat attestation.
	if !repeat {
		if _, err := tx.Exec(ctx, quoteSeqCTE+`
			UPDATE quotes SET status='ACCEPTED', updated_at = now(), seq = (SELECT next_value FROM qseq)
			 WHERE id = $1`, in.QuoteID); err != nil {
			return out, fmt.Errorf("accept quote: %w", err)
		}
	}
	out = AcceptanceResult{
		QuoteNo: quoteNo, TypedName: in.TypedName, AcceptedAt: in.Now, QuantitiesConfirmed: in.QuantitiesConfirmed,
		SubtotalCents: subtotal, TaxCents: tax, TotalCents: total,
		Repeat: repeat, priorStatus: status, idemKey: in.IdempotencyKey,
	}
	var seq int64
	if hooks.record != nil {
		if seq, err = hooks.record(ctx, tx, out); err != nil {
			return AcceptanceResult{}, err
		}
	}
	// The acceptance record SNAPSHOTS the money and the document number (C1): it must stay meaningful
	// even if the quote row is later gone, so it never reads back through quote_id at display time.
	// The record also keeps what the customer was shown (0368): the lines as they stand under this lock
	// and the notice wording in force for the quote's branch, read in the same statement.
	if err := tx.QueryRow(ctx, `
		INSERT INTO quote_acceptances
		  (quote_id, share_link_id, quote_no, typed_name,
		   accepted_subtotal_cents, accepted_tax_cents, accepted_total_cents, audit_seq, accepted_at,
		   quantities_confirmed, quantity_notice, accepted_lines)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, NULLIF($8,0), $9, $10,
		  COALESCE((SELECT qp.quantity_notice FROM quote_policy qp
		             WHERE qp.branch_id = (SELECT branch_id FROM quotes WHERE id = $1::uuid)),
		           (SELECT qp.quantity_notice FROM quote_policy qp WHERE qp.branch_id IS NULL), $11),
		  COALESCE((SELECT jsonb_agg(jsonb_build_object(
		              'line_no', ql.line_no,
		              'description', COALESCE(NULLIF(ql.description,''), p.name, ''),
		              'qty_entered', ql.qty_entered::text, 'uom_entered', ql.uom_entered::text,
		              'unit_price_cents', ql.unit_price_cents, 'line_total_cents', ql.line_total_cents)
		              ORDER BY ql.line_no)
		            FROM quote_lines ql LEFT JOIN products p ON p.id = ql.product_id
		           WHERE ql.quote_id = $1::uuid), '[]'::jsonb))
		RETURNING accepted_at`,
		in.QuoteID, in.ShareLinkID, quoteNo, in.TypedName, subtotal, tax, total, seq, in.Now,
		in.QuantitiesConfirmed, DefaultQuantityNotice).
		Scan(&out.AcceptedAt); err != nil {
		return AcceptanceResult{}, fmt.Errorf("record quote acceptance: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AcceptanceResult{}, fmt.Errorf("commit quote acceptance: %w", err)
	}
	return out, nil
}
