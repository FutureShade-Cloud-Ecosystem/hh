// activity_repository.go, the pgx side of DIB-492/493: the 0161 order_notes writes/reads (this
// module is the table's only writer) and the immediate-documents projection the activity stream
// merges in.
//
// ON THE DOCUMENT READ AND MODULE BOUNDARIES. OrderDocumentEvents reads invoices, payments and
// payment_allocations, tables billing and the till own. This is a READ-ONLY presentation
// projection, never a money decision, and it follows two shipped precedents: the till's Q-044
// attributed history reads (till/history.go joins invoices + users the same way), and this very
// repository's shipment-plan read (shipgroup_repository.go LEFT JOINs invoices for the group's
// invoice_no). Anything that DECIDES on money keeps going through the declared seams
// (cancel_seam.go, invoice_source.go), this file only puts words on rows that already exist.
package order

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// noteCols is the shared order_notes projection: the row plus both display names (author, editor),
// resolved at read time, never denormalized (0161).
const noteCols = `n.id::text, n.order_id::text, COALESCE(n.author_id::text,''), COALESCE(ua.display_name,''),
	n.channel, n.body, COALESCE(n.edited_by::text,''), COALESCE(ue.display_name,''), n.edited_at, n.created_at`

const noteFrom = ` FROM order_notes n
	LEFT JOIN users ua ON ua.id = n.author_id
	LEFT JOIN users ue ON ue.id = n.edited_by`

func scanNote(row pgx.Row) (OrderNote, error) {
	var n OrderNote
	var editedAt *time.Time
	if err := row.Scan(&n.ID, &n.OrderID, &n.AuthorID, &n.AuthorName,
		&n.Channel, &n.Body, &n.EditedBy, &n.EditedByName, &editedAt, &n.CreatedAt); err != nil {
		return OrderNote{}, err
	}
	n.EditedAt = editedAt
	return n, nil
}

// InsertOrderNote writes one note, idempotent on (order_id, idempotency_key): the 0161 unique index
// makes a retried POST converge on the note the first attempt wrote (returned with replayed=true)
// rather than duplicating it. ON CONFLICT DO NOTHING + a keyed re-read keeps the replay race-free
// without an advisory lock, whichever attempt wins the index, both read back the same row.
func (r *Repository) InsertOrderNote(ctx context.Context, d NoteDraft) (OrderNote, bool, error) {
	if r.pool == nil {
		return OrderNote{}, false, errNoDB
	}
	var id string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO order_notes (order_id, author_id, channel, body, idempotency_key)
		VALUES ($1::uuid, NULLIF($2,'')::uuid, $3, $4, $5)
		ON CONFLICT (order_id, idempotency_key) DO NOTHING
		RETURNING id::text`,
		d.OrderID, d.AuthorID, d.Channel, d.Body, d.IdempotencyKey).Scan(&id)
	if err == nil {
		n, rerr := r.orderNoteByID(ctx, d.OrderID, id)
		return n, false, rerr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return OrderNote{}, false, fmt.Errorf("order: insert note: %w", err)
	}
	// The key already wrote a note on this order, the idempotent replay.
	n, rerr := scanNote(r.pool.QueryRow(ctx,
		`SELECT `+noteCols+noteFrom+` WHERE n.order_id = $1::uuid AND n.idempotency_key = $2`,
		d.OrderID, d.IdempotencyKey))
	if rerr != nil {
		return OrderNote{}, false, fmt.Errorf("order: replay note read: %w", rerr)
	}
	return n, true, nil
}

// orderNoteByID re-reads one note with its display names (the post-insert read).
func (r *Repository) orderNoteByID(ctx context.Context, orderID, noteID string) (OrderNote, error) {
	n, err := scanNote(r.pool.QueryRow(ctx,
		`SELECT `+noteCols+noteFrom+` WHERE n.id = $1::uuid AND n.order_id = $2::uuid`, noteID, orderID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OrderNote{}, ErrNoteNotFound
		}
		return OrderNote{}, fmt.Errorf("order: note read: %w", err)
	}
	return n, nil
}

// UpdateOrderNoteBody corrects a note's text (and channel when non-empty), stamping the edit
// attribution server-side: edited_by is the resolved caller, edited_at is the database clock.
// The WHERE is keyed to BOTH ids, so a note id from another order answers ErrNoteNotFound rather
// than crossing orders. The caller has already validated noteID's shape (no ::uuid blowups).
func (r *Repository) UpdateOrderNoteBody(ctx context.Context, orderID, noteID, body, channel, editorID string) (OrderNote, error) {
	if r.pool == nil {
		return OrderNote{}, errNoDB
	}
	var id string
	err := r.pool.QueryRow(ctx, `
		UPDATE order_notes
		   SET body = $3,
		       channel = COALESCE(NULLIF($4,''), channel),
		       edited_by = NULLIF($5,'')::uuid,
		       edited_at = now()
		 WHERE id = $1::uuid AND order_id = $2::uuid
		 RETURNING id::text`,
		noteID, orderID, body, channel, editorID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OrderNote{}, ErrNoteNotFound
		}
		return OrderNote{}, fmt.Errorf("order: edit note: %w", err)
	}
	return r.orderNoteByID(ctx, orderID, id)
}

// ListOrderNotes returns one order's notes, newest first (the 0161 index order), with both display
// names resolved.
func (r *Repository) ListOrderNotes(ctx context.Context, orderID string) ([]OrderNote, error) {
	if r.pool == nil {
		return []OrderNote{}, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+noteCols+noteFrom+` WHERE n.order_id = $1::uuid ORDER BY n.created_at DESC, n.id DESC`, orderID)
	if err != nil {
		return nil, fmt.Errorf("order: list notes: %w", err)
	}
	defer rows.Close()
	out := []OrderNote{}
	for rows.Next() {
		n, serr := scanNote(rows)
		if serr != nil {
			return nil, fmt.Errorf("order: scan note: %w", serr)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// OrderDocumentEvents shapes the order's immediate documents as display rows: each invoice raised
// against the order (issued / still a draft / voided) and each payment applied to those invoices,
// with the acting staff display names resolved in the same read. Read-only presentation, see the
// file header for the boundary ruling.
func (r *Repository) OrderDocumentEvents(ctx context.Context, orderID string) ([]DocEvent, error) {
	if r.pool == nil {
		return []DocEvent{}, nil
	}
	out := []DocEvent{}

	// Invoices. One row per document fact: issued (finalized_at), still-a-draft (created_at), and -
	// additionally, for a document that was later unwound, voided (updated_at is the closest
	// honest timestamp the schema holds for the void).
	invRows, err := r.pool.Query(ctx, `
		SELECT i.id::text, i.invoice_no::text, i.status, i.total_cents,
		       i.created_at, i.finalized_at, i.updated_at,
		       COALESCE(i.created_by::text,''), COALESCE(u.display_name,'')
		  FROM invoices i
		  LEFT JOIN users u ON u.id = i.created_by
		 WHERE i.order_id = $1::uuid
		 ORDER BY i.created_at`, orderID)
	if err != nil {
		return nil, fmt.Errorf("order: activity invoices: %w", err)
	}
	defer invRows.Close()
	for invRows.Next() {
		var (
			id, no, status, actorID, actorName string
			totalCents                         int64
			createdAt, updatedAt               time.Time
			finalizedAt                        *time.Time
		)
		if err := invRows.Scan(&id, &no, &status, &totalCents,
			&createdAt, &finalizedAt, &updatedAt, &actorID, &actorName); err != nil {
			return nil, fmt.Errorf("order: scan activity invoice: %w", err)
		}
		switch {
		case finalizedAt != nil:
			out = append(out, DocEvent{
				ID: "invoice:" + id + ":issued", OccurredAt: *finalizedAt,
				ActorID: actorID, ActorName: actorName,
				Summary: fmt.Sprintf("issued invoice %s for %s", no, moneyStr(totalCents)),
			})
		case status == "DRAFT":
			out = append(out, DocEvent{
				ID: "invoice:" + id + ":draft", OccurredAt: createdAt,
				ActorID: actorID, ActorName: actorName,
				Summary: fmt.Sprintf("started invoice %s (draft)", no),
			})
		}
		if status == "VOID" {
			out = append(out, DocEvent{
				ID: "invoice:" + id + ":void", OccurredAt: updatedAt,
				Summary: fmt.Sprintf("voided invoice %s", no),
			})
		}
	}
	if err := invRows.Err(); err != nil {
		return nil, fmt.Errorf("order: activity invoices: %w", err)
	}

	// Payments applied to the order's invoices. The actor is WHO MATCHED the money to the invoice
	// (allocated_by), falling back to WHO TOOK it (received_by) for an auto-applied tender, the
	// Q-044 attribution pair.
	payRows, err := r.pool.Query(ctx, `
		SELECT p.id::text, i.invoice_no::text, p.method, pa.amount_cents, pa.created_at,
		       COALESCE(pa.allocated_by::text,''), COALESCE(ua.display_name,''),
		       COALESCE(p.received_by::text,''), COALESCE(ur.display_name,'')
		  FROM payment_allocations pa
		  JOIN invoices i ON i.id = pa.invoice_id
		  JOIN payments p ON p.id = pa.payment_id
		  LEFT JOIN users ua ON ua.id = pa.allocated_by
		  LEFT JOIN users ur ON ur.id = p.received_by
		 WHERE i.order_id = $1::uuid
		 ORDER BY pa.created_at`, orderID)
	if err != nil {
		return nil, fmt.Errorf("order: activity payments: %w", err)
	}
	defer payRows.Close()
	for payRows.Next() {
		var (
			pid, invNo, method                 string
			allocBy, allocName, rcvBy, rcvName string
			amountCents                        int64
			at                                 time.Time
		)
		if err := payRows.Scan(&pid, &invNo, &method, &amountCents, &at,
			&allocBy, &allocName, &rcvBy, &rcvName); err != nil {
			return nil, fmt.Errorf("order: scan activity payment: %w", err)
		}
		ev := DocEvent{
			ID: "payment:" + pid + ":" + invNo, OccurredAt: at,
			ActorID: allocBy, ActorName: allocName,
			Summary: fmt.Sprintf("applied a %s payment (%s) to invoice %s",
				moneyStr(amountCents), humanCode(method), invNo),
		}
		if ev.ActorID == "" {
			ev.ActorID, ev.ActorName = rcvBy, rcvName
		}
		out = append(out, ev)
	}
	if err := payRows.Err(); err != nil {
		return nil, fmt.Errorf("order: activity payments: %w", err)
	}
	return out, nil
}
