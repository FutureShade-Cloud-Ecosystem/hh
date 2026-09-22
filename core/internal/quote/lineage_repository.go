// lineage_repository.go, pgx persistence for the DIB-491 related-documents read (lineage.go).
//
// Every family is read on ONE read-only REPEATABLE READ transaction (the PublicQuote idiom), so the
// quote, its orders and their invoices always come from a single snapshot, a family must never show
// an invoice whose order it simultaneously claims not to have.
//
// ON READING ACROSS TABLES. This module already writes orders/order_lines inside Convert (the
// conversion IS the order write, 0008's design: "conversion is a link, not a mutation"), and the
// lineage read walks exactly the links that conversion and its downstream writers left behind:
// orders.quote_id, invoices.order_id, quote_accept_provenance. It reads document HEADERS only -
// no lines, no costs, no margin, the same narrow projection the numbers on a list row already show.
package quote

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ResolveOrderForLineage resolves an order id or its visible number ("#SO-…" / "SO-…"), scoped to
// the caller's branches, the mirror of ResolveQuoteID, with order.Repository.ResolveOrderID's
// number normalization (a bare number gets its '#' back, because the paperwork prints one).
// A missing or out-of-reach order is ErrNotFound, never an existence oracle.
func (r *Repository) ResolveOrderForLineage(ctx context.Context, idOrNo string, global bool, branchIDs []string) (orderID, branchID, quoteID string, err error) {
	if r.pool == nil {
		return "", "", "", errNoDB
	}
	// A scoped caller with NO branch grants reaches nothing, short-circuit rather than issue a
	// `= ANY('{}')` that can never match.
	if !global && len(branchIDs) == 0 {
		return "", "", "", ErrNotFound
	}
	const cols = `SELECT id::text, branch_id::text, COALESCE(quote_id::text,'') FROM orders`
	var row pgx.Row
	if looksLikeUUID(idOrNo) {
		row = r.pool.QueryRow(ctx,
			cols+` WHERE id = $1::uuid AND ($2 OR branch_id = ANY($3::uuid[]))`,
			idOrNo, global, branchIDs)
	} else {
		no := idOrNo
		if !strings.HasPrefix(no, "#") {
			no = "#" + no
		}
		row = r.pool.QueryRow(ctx,
			cols+` WHERE order_no = $1 AND ($2 OR branch_id = ANY($3::uuid[]))`,
			no, global, branchIDs)
	}
	if err := row.Scan(&orderID, &branchID, &quoteID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", "", ErrNotFound
		}
		return "", "", "", fmt.Errorf("resolve order %q for lineage: %w", idOrNo, err)
	}
	return orderID, branchID, quoteID, nil
}

// QuoteFamily walks quote → orders → invoices and returns the flat, parent-annotated rows.
func (r *Repository) QuoteFamily(ctx context.Context, quoteID, selfID string) ([]RelatedDocument, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if !looksLikeUUID(quoteID) {
		return nil, ErrNotFound
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin family read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	out := []RelatedDocument{}

	// The root quote.
	var root RelatedDocument
	root.Kind = DocKindQuote
	if err := tx.QueryRow(ctx, `
		SELECT id::text, quote_no::text, status, created_at, total_cents
		  FROM quotes WHERE id = $1::uuid`, quoteID).
		Scan(&root.ID, &root.Number, &root.Status, &root.Date, &root.TotalCents); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("family quote read: %w", err)
	}
	out = append(out, root)

	// The orders the quote's acceptances became, oldest first, accept_seq order where provenance
	// exists, creation order where it predates 0162. LEFT JOIN: an order minted before the
	// provenance table simply carries no origin, which is honest.
	orderRows, err := tx.Query(ctx, `
		SELECT o.id::text, o.order_no::text, o.status, o.created_at, o.total_cents,
		       COALESCE(p.origin,''), COALESCE(p.accept_seq,0)
		  FROM orders o
		  LEFT JOIN quote_accept_provenance p ON p.order_id = o.id
		 WHERE o.quote_id = $1::uuid
		 ORDER BY o.created_at, o.order_no`, quoteID)
	if err != nil {
		return nil, fmt.Errorf("family orders read: %w", err)
	}
	defer orderRows.Close()
	for orderRows.Next() {
		d := RelatedDocument{Kind: DocKindOrder, ParentID: quoteID}
		if err := orderRows.Scan(&d.ID, &d.Number, &d.Status, &d.Date, &d.TotalCents,
			&d.Origin, &d.AcceptSeq); err != nil {
			return nil, fmt.Errorf("family order scan: %w", err)
		}
		out = append(out, d)
	}
	if err := orderRows.Err(); err != nil {
		return nil, err
	}
	orderRows.Close()

	// The invoices those orders became.
	inv, err := r.invoicesForTx(ctx, tx, `
		SELECT i.id::text, i.invoice_no::text, i.status, i.created_at, i.total_cents, i.order_id::text
		  FROM invoices i
		 WHERE i.order_id IN (SELECT id FROM orders WHERE quote_id = $1::uuid)
		 ORDER BY i.created_at, i.invoice_no`, quoteID)
	if err != nil {
		return nil, err
	}
	out = append(out, inv...)

	markSelf(out, selfID)
	return out, nil
}

// OrderOnlyFamily is the family of an order with no quote behind it: the order + its invoices.
func (r *Repository) OrderOnlyFamily(ctx context.Context, orderID string) ([]RelatedDocument, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if !looksLikeUUID(orderID) {
		return nil, ErrNotFound
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin family read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	out := []RelatedDocument{}
	var root RelatedDocument
	root.Kind = DocKindOrder
	if err := tx.QueryRow(ctx, `
		SELECT id::text, order_no::text, status, created_at, total_cents
		  FROM orders WHERE id = $1::uuid`, orderID).
		Scan(&root.ID, &root.Number, &root.Status, &root.Date, &root.TotalCents); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("family order read: %w", err)
	}
	out = append(out, root)

	inv, err := r.invoicesForTx(ctx, tx, `
		SELECT i.id::text, i.invoice_no::text, i.status, i.created_at, i.total_cents, i.order_id::text
		  FROM invoices i WHERE i.order_id = $1::uuid
		 ORDER BY i.created_at, i.invoice_no`, orderID)
	if err != nil {
		return nil, err
	}
	out = append(out, inv...)

	markSelf(out, orderID)
	return out, nil
}

// invoicesForTx runs one of the two invoice SELECTs above and shapes the rows. The statements are
// package constants passed whole, never caller text.
func (r *Repository) invoicesForTx(ctx context.Context, tx pgx.Tx, sql, arg string) ([]RelatedDocument, error) {
	rows, err := tx.Query(ctx, sql, arg)
	if err != nil {
		return nil, fmt.Errorf("family invoices read: %w", err)
	}
	defer rows.Close()
	out := []RelatedDocument{}
	for rows.Next() {
		d := RelatedDocument{Kind: DocKindInvoice}
		if err := rows.Scan(&d.ID, &d.Number, &d.Status, &d.Date, &d.TotalCents, &d.ParentID); err != nil {
			return nil, fmt.Errorf("family invoice scan: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// markSelf flags the row the caller asked from.
func markSelf(rows []RelatedDocument, selfID string) {
	for i := range rows {
		if rows[i].ID == selfID {
			rows[i].Self = true
		}
	}
}
