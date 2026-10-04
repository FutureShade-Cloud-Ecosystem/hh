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
		       qa.accepted_total_cents, qa.accepted_at,
		       COALESCE(al.changes->>'before',''), COALESCE(al.changes->>'after','')
		  FROM quote_acceptances qa
		  JOIN audit_log al ON al.seq = qa.audit_seq
		 WHERE qa.quote_id = $1::uuid AND qa.share_link_id = $2::uuid
		   AND al.entity_type = 'quote' AND al.entity_id = $1::uuid AND al.action = 'QUOTE_ACCEPTED'
		   AND al.changes->>'client_id' = $3
		 ORDER BY qa.accepted_at, qa.id
		 LIMIT 1`, in.QuoteID, in.ShareLinkID, in.IdempotencyKey).
		Scan(&out.QuoteNo, &out.TypedName, &out.SubtotalCents, &out.TaxCents, &out.TotalCents,
			&out.AcceptedAt, &before, &after)
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
// WHY FOR SHARE CANNOT DEADLOCK. This transaction holds no lock before it takes the quote row's share
// lock, takes no other lock afterwards (the rest is plain reads, which lock nothing) and waits on
// nothing after that point, so it can never sit on a wait cycle: the only thing it ever waits for is
// the one lock it asks for first, and whoever holds that lock (the accept, the staff edit paths, send,
// convert and the sync write, all of which lock quote rows FOR UPDATE and take no lock this
// transaction holds) is not waiting on it. RevokeShareLink is a plain UPDATE of the link row and takes
// no quote row lock, and the follow-up sweep takes none either, so neither enters the ordering. A share
// lock is compatible with other share locks and with the key share locks foreign keys take, so
// concurrent retries do not queue behind each other. READ COMMITTED gives the lookup that follows the
// lock a fresh snapshot, which is what lets it see the commit it waited for.
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
