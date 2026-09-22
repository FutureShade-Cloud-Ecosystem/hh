// acceptcontrols_repository.go, pgx persistence for DIB-491's acceptance controls.
//
// Two families of read, on purpose:
//
//   - AllowMultiAccept (pool) answers the DISPLAY questions, should the public page offer the accept
//     form again, should the mint guard let a link be issued for an accepted quote, should the
//     CanConvert offer appear. A display read may be a heartbeat stale; the worst case is a button
//     the server then refuses, which is the ordinary fate of every optimistic offer in this app.
//
//   - allowMultiAcceptTx (tx) answers the ENFORCEMENT question, inside the same transaction that
//     holds the quote's FOR UPDATE lock. The DIB-393 expiry guard re-reads under the lock for exactly
//     this reason: only the transaction that writes can know the fact it is writing against. An admin
//     flipping the switch OFF races a customer's accept; whichever commits first wins, and there is
//     no window in which both a refusal and an acceptance can be true.
package quote

import (
	"context"
	"errors"
	"fmt"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5"
)

// allowMultiAcceptSQL treats an ABSENT key as false, the restrictive default survives a database
// that never ran 0162's seed. The JSONB comparison is against the jsonb literal 'true', so a
// mistyped value ('"yes"', '1') also reads as OFF rather than ON: this switch widens behavior, and a
// value we do not recognise must not widen anything.
const allowMultiAcceptSQL = `SELECT COALESCE(
	(SELECT value = 'true'::jsonb FROM system_settings WHERE key = $1), false)`

// AllowMultiAccept reads the effective switch (display reads; see the file header).
func (r *Repository) AllowMultiAccept(ctx context.Context) (bool, error) {
	if r.pool == nil {
		return false, errNoDB
	}
	var on bool
	if err := r.pool.QueryRow(ctx, allowMultiAcceptSQL, SettingAllowMultiAccept).Scan(&on); err != nil {
		return false, fmt.Errorf("read %s: %w", SettingAllowMultiAccept, err)
	}
	return on, nil
}

// SetAllowMultiAccept writes the switch. Upsert, so the first write on a database without the seed
// row creates it rather than failing, the same posture the branches default write takes.
func (r *Repository) SetAllowMultiAccept(ctx context.Context, allow bool) error {
	if r.pool == nil {
		return errNoDB
	}
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO system_settings (key, value) VALUES ($1, to_jsonb($2::boolean))
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		SettingAllowMultiAccept, allow); err != nil {
		return fmt.Errorf("write %s: %w", SettingAllowMultiAccept, err)
	}
	return nil
}

// allowMultiAcceptTx is the ENFORCEMENT read (see the file header). Same statement, the caller's tx.
func (r *Repository) allowMultiAcceptTx(ctx context.Context, tx pgx.Tx) (bool, error) {
	var on bool
	if err := tx.QueryRow(ctx, allowMultiAcceptSQL, SettingAllowMultiAccept).Scan(&on); err != nil {
		return false, fmt.Errorf("read %s under lock: %w", SettingAllowMultiAccept, err)
	}
	return on, nil
}

// rowQuerier is the one read both the pool and a transaction can answer, it lets the refusal
// builder below serve the under-lock paths (Convert, AcceptQuoteGuarded) and the pool-level portal
// Accept with one set of statements.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// alreadyAcceptedErr builds the refusal for a repeat acceptance while the switch is OFF, reading its
// facts on the caller's querier (under-lock callers pass their tx, so the facts cannot move between
// the decision and the message).
//
// The order it names is the EARLIEST order minted from this quote, "already accepted as order #X"
// means the original acceptance, not whichever duplicate happened last. A quote that is ACCEPTED but
// not yet converted has no order to name; the message then carries the acceptance's own date
// (quote_acceptances), falling back to the quote's updated_at for the legacy portal path that
// records no acceptance row.
func (r *Repository) alreadyAcceptedErr(ctx context.Context, tx rowQuerier, quoteID, status string) error {
	e := &AlreadyAcceptedError{Status: status}
	err := tx.QueryRow(ctx, `
		SELECT order_no::text, created_at FROM orders
		 WHERE quote_id = $1::uuid ORDER BY created_at ASC LIMIT 1`, quoteID).
		Scan(&e.OrderNo, &e.At)
	switch {
	case err == nil:
		e.HasOrder = true
		return e
	case errors.Is(err, pgx.ErrNoRows):
		// No order yet, name the acceptance instead.
	default:
		return fmt.Errorf("read accepted-order facts: %w", err)
	}
	err = tx.QueryRow(ctx, `
		SELECT accepted_at FROM quote_acceptances
		 WHERE quote_id = $1::uuid ORDER BY accepted_at ASC LIMIT 1`, quoteID).Scan(&e.At)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read acceptance facts: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if uerr := tx.QueryRow(ctx,
			`SELECT updated_at FROM quotes WHERE id = $1::uuid`, quoteID).Scan(&e.At); uerr != nil {
			return fmt.Errorf("read quote facts: %w", uerr)
		}
	}
	return e
}

// recordAcceptProvenanceTx writes the provenance row for an order minted inside Convert's own
// transaction, NEVER best-effort, per D-097: an order whose birth cannot be explained is the defect
// this lane exists to close, so a provenance failure rolls the conversion back with it.
//
// accept_seq is MAX+1 over the quote's existing rows, computed under the quote's FOR UPDATE lock, so
// concurrent conversions serialize on the lock and the (quote_id, accept_seq) UNIQUE never fires in
// honest operation.
func (r *Repository) recordAcceptProvenanceTx(ctx context.Context, tx pgx.Tx,
	quoteID, quoteNo, orderID, orderNo, origin, actorID string) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO quote_accept_provenance (quote_id, order_id, quote_no, order_no, accept_seq, origin, actor_id)
		VALUES ($1::uuid, $2::uuid, $3, $4,
		        (SELECT COALESCE(MAX(accept_seq), 0) + 1 FROM quote_accept_provenance WHERE quote_id = $1::uuid),
		        $5, NULLIF($6,'')::uuid)`,
		quoteID, orderID, quoteNo, orderNo, origin, actorID); err != nil {
		return fmt.Errorf("record accept provenance for quote %q: %w", quoteID, err)
	}
	return nil
}

// convertAttribution names the door and the actor for a conversion's provenance row WITHOUT touching
// the conversion seam's signature. The seam test (convert_seam_test.go) pins that both doors hand the
// SAME (customer, quote, options) to one store call, the actor is the one fact that legitimately
// differs per door, so it rides where actor facts already ride: the request context. A staff caller
// stamped by the identity guard means the counter door; no staff caller means the portal, whose
// session IS the customer (convert.go's own words).
func convertAttribution(ctx context.Context, customerID string) (origin, actorID string) {
	if sc, ok := identity.StaffContextFromContext(ctx); ok && sc.UserID != "" {
		return OriginCounter, sc.UserID
	}
	return OriginPortal, customerID
}
