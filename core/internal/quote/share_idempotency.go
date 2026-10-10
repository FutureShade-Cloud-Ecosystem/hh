// share_idempotency.go, QD-3d: the optional Idempotency-Key on the public share link accept.
//
// WHY. A re-read showing SENT after a failed accept does not prove the acceptance was not recorded: a
// request that timed out at the client can still commit at the server after the re-read (decision row
// 744). So a retry carries the SAME key as the first attempt, and the server answers a repeat with the
// FIRST outcome and writes nothing, with multi-accept on or off.
//
// WHERE THE KEY LIVES (no migration; the schema freeze is shut). quote_acceptances has no key column
// and no free text or JSON column. The key rides the QUOTE_ACCEPTED audit row the acceptance already
// writes on its own transaction, under the allowlisted client_id key (a uuid, the same key shape the
// sync rows use for their idempotency key). quote_acceptances.audit_seq anchors the two rows to each
// other, so the lookup joins an acceptance to its audit row through the anchor, then matches the key.
//
// WHY THAT IS EXACT, CHEAP AND RACE FREE. The match is an equality on a key, scoped to this quote and
// this share link by the acceptance row's own columns. The audit side is read by (entity_type,
// entity_id), idx_audit_log_entity_seq, a quote's handful of acceptances. The lookup runs on the
// transaction that holds the quote row lock, BEFORE the status checks, so two requests with one key
// serialize on the lock and the second always sees the first's committed row.
package quote

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/jackc/pgx/v5"
)

// ErrIdempotencyKeyReused: the key was already used for a different acceptance (another typed name).
// Mapped to 409. It never carries the first acceptance's name.
var ErrIdempotencyKeyReused = errors.New("quote: this idempotency key was used for a different acceptance")

// normalizeIdempotencyKey validates the optional Idempotency-Key header value. Empty means none was
// sent. A key is a UUID, case-insensitive; anything else is a 400, and the stored form is lower case so
// the same key in either case is the same key.
func normalizeIdempotencyKey(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if !looksLikeUUID(raw) {
		return "", httpx.BadRequestf("Idempotency-Key must be a UUID")
	}
	return strings.ToLower(raw), nil
}

// findKeyedAcceptance returns the earlier acceptance of this quote through this share link that
// carried the key, rebuilt as the confirmation it first produced, or found=false. It must run under
// the quote row lock. A found acceptance with a different typed name is ErrIdempotencyKeyReused.
func (r *Repository) findKeyedAcceptance(ctx context.Context, tx rowQuerier, in acceptWrite) (out AcceptanceResult, found bool, err error) {
	var before, after string
	err = tx.QueryRow(ctx, `
		SELECT qa.quote_no, qa.typed_name, qa.accepted_subtotal_cents, qa.accepted_tax_cents,
		       qa.accepted_total_cents, qa.accepted_at, qa.quantities_confirmed,
		       COALESCE(al.changes->>'before',''), COALESCE(al.changes->>'after','')
		  FROM quote_acceptances qa
		  JOIN audit_log al ON al.seq = qa.audit_seq
		 WHERE qa.quote_id = $1::uuid AND qa.share_link_id = $2::uuid
		   AND al.entity_type = 'quote' AND al.entity_id = $1::uuid AND al.action = 'QUOTE_ACCEPTED'
		   AND al.changes->>'client_id' = $3
		 ORDER BY qa.accepted_at, qa.id
		 LIMIT 1`, in.QuoteID, in.ShareLinkID, in.IdempotencyKey).
		Scan(&out.QuoteNo, &out.TypedName, &out.SubtotalCents, &out.TaxCents, &out.TotalCents,
			&out.AcceptedAt, &out.QuantitiesConfirmed, &before, &after)
	if errors.Is(err, pgx.ErrNoRows) {
		return AcceptanceResult{}, false, nil
	}
	if err != nil {
		return AcceptanceResult{}, false, fmt.Errorf("look up acceptance by idempotency key: %w", err)
	}
	if out.TypedName != in.TypedName {
		return AcceptanceResult{}, false, ErrIdempotencyKeyReused
	}
	// A repeat acceptance is the one whose audit row moved nothing (before equals after), the same
	// rule recordAcceptance writes. The first outcome had the repeat flag only if this one did.
	out.Repeat = before != "" && before == after
	out.priorStatus = before
	out.replayed = true
	return out, true, nil
}

// ReplayKeyedAcceptance is the lookup behind a retry through a link that has since died (revoked or
// expired): the caller holds the token that existed and the key, so the first outcome is what they were
// owed. It writes nothing. It reads under a SHARE lock on the quote row, the row AcceptQuoteGuarded
// takes FOR UPDATE, so a retry that arrives while the first attempt is still in flight (resolved while
// the link was live, then the link died) waits for that attempt to commit or abort and then sees the
// truth. Without the lock it could answer "nothing accepted" a moment before the first attempt commits.
//
// WHY FOR SHARE ADDS NO DEADLOCK. This transaction holds no lock before it takes the quote row's share
// lock and takes no other lock afterwards (the rest is plain reads, which lock nothing), so it is
// never the holder of an edge in a wait cycle. It can still be a victim: a queued row lock waiter
// holds the row's tuple lock while it waits, so it can sit inside a cycle that two OTHER transactions
// form on the quote and link rows, and be the one Postgres aborts (SQLSTATE 40P01). That answers 500,
// an honest unknown outcome, never the 410. No pair of product paths forms such a cycle on quote or
// link rows: the accept takes the quote FOR UPDATE and its foreign key inserts then take KEY SHARE on
// the link and the quote; RevokeShareLink updates only the link row; mint, the share events and the
// follow-up writes take only KEY SHARE through foreign keys, which is compatible with FOR SHARE; and
// the other quote writers (send, convert and the sync write lock FOR UPDATE, while transition,
// which serves reject and the other status moves, and the builder repricing use plain UPDATEs) take
// a row lock that waits on this lookup and never the reverse. A share lock is compatible with other
// share locks and with the key share locks foreign keys take, so concurrent retries do not queue
// behind each other. READ COMMITTED gives the lookup that follows the lock a fresh snapshot, which is
// what lets it see the commit it waited for.
func (r *Repository) ReplayKeyedAcceptance(ctx context.Context, in acceptWrite) (AcceptanceResult, bool, error) {
	if r.pool == nil {
		return AcceptanceResult{}, false, errNoDB
	}
	if !looksLikeUUID(in.QuoteID) {
		return AcceptanceResult{}, false, nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return AcceptanceResult{}, false, fmt.Errorf("begin keyed replay: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var one int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM quotes WHERE id = $1 FOR SHARE`, in.QuoteID).Scan(&one); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AcceptanceResult{}, false, nil
		}
		return AcceptanceResult{}, false, fmt.Errorf("lock quote for keyed replay: %w", err)
	}
	return r.findKeyedAcceptance(ctx, tx, in)
}

// keyedConversionOrderID reads the order id the conversion row of this quote under the key recorded
// (convert_idempotency.go), or found=false. It must run under the quote row lock.
func keyedConversionOrderID(ctx context.Context, tx rowQuerier, quoteID, key string) (string, bool, error) {
	var orderID string
	err := tx.QueryRow(ctx, `
		SELECT al.changes->>'source_order_id'
		  FROM audit_log al
		 WHERE al.entity_type = 'quote' AND al.entity_id = $1::uuid AND al.action = $2
		   AND al.changes->>'client_id' = $3 AND al.changes->>'source_order_id' IS NOT NULL
		 ORDER BY al.seq
		 LIMIT 1`, quoteID, audit.ActQuoteChanged, key).Scan(&orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("look up conversion by idempotency key: %w", err)
	}
	return orderID, true, nil
}
