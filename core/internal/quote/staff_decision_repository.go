package quote

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// DecideQuoteGuarded moves a quote to the status a staff decision names, under the quote row lock, with
// the decision's audit rows on the same transaction.
//
// Order under the lock, and the order is the contract:
//
//  1. a retry under a key already used answers the first outcome and writes nothing, whatever the
//     quote's status is now (the QD-3d discipline: a request that timed out at the client may have
//     committed after the client looked again);
//  2. the status is judged, in words;
//  3. the expiry is judged;
//  4. the move, then the record hook, then commit.
func (r *Repository) DecideQuoteGuarded(ctx context.Context, w decisionWrite, hooks decisionHooks) (decisionOutcome, error) {
	var out decisionOutcome
	if r.pool == nil {
		return out, errNoDB
	}
	if !looksLikeUUID(w.QuoteID) {
		return out, ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return out, fmt.Errorf("begin quote decision: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status, quoteNo string
	var validUntil *time.Time
	var total int64
	if err := tx.QueryRow(ctx, `
		SELECT status, quote_no::text, valid_until, total_cents FROM quotes WHERE id = $1 FOR UPDATE`, w.QuoteID).
		Scan(&status, &quoteNo, &validUntil, &total); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrNotFound
		}
		return out, fmt.Errorf("lock quote for decision: %w", err)
	}
	if w.Key != "" {
		prior, found, ferr := r.findKeyedDecision(ctx, tx, w, quoteNo)
		if ferr != nil {
			return decisionOutcome{}, ferr
		}
		if found {
			return prior, nil
		}
	}
	if err := r.judgeDecision(ctx, tx, w, status, validUntil); err != nil {
		return out, err
	}
	// The precondition, judged under the same lock the move is written under: the total the customer
	// was read must still be the total. Refused here, nothing has been written yet.
	if w.Expected != nil && *w.Expected != total {
		return out, &DecisionRefusedError{Msg: fmt.Sprintf(
			"quote: the quote total changed since you read it to the customer, it was %s and is now %s; "+
				"read the customer the current quote, then accept it again",
			centsText(*w.Expected), centsText(total))}
	}
	var decidedAt time.Time
	if err := tx.QueryRow(ctx, quoteSeqCTE+`
		UPDATE quotes SET status = $2, updated_at = now(), updated_by = NULLIF($3,'')::uuid,
		                  seq = (SELECT next_value FROM qseq)
		 WHERE id = $1 RETURNING updated_at`, w.QuoteID, w.To, w.ActorID).Scan(&decidedAt); err != nil {
		return out, fmt.Errorf("move quote to %s: %w", w.To, err)
	}
	if err := stopFollowUpsTx(ctx, tx, w, decidedAt); err != nil {
		return out, err
	}
	if hooks.release != nil {
		if err := hooks.release(ctx, tx); err != nil {
			return out, fmt.Errorf("release quote holds: %w", err)
		}
	}
	if hooks.record != nil {
		if err := hooks.record(ctx, tx, status, total); err != nil {
			return out, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return out, fmt.Errorf("commit quote decision: %w", err)
	}
	return decisionOutcome{QuoteNo: quoteNo, Prev: status, Status: w.To, Channel: w.Channel,
		DecidedAt: decidedAt, TotalCents: total}, nil
}

// judgeDecision refuses, in words, every state a staff decision cannot happen from. Called under the
// lock, so the state it judges is the state the move writes against.
func (r *Repository) judgeDecision(ctx context.Context, tx pgx.Tx, w decisionWrite, status string, validUntil *time.Time) error {
	switch status {
	case "SENT":
	case "DRAFT":
		if w.To == "ACCEPTED" {
			var lines int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM quote_lines WHERE quote_id = $1`, w.QuoteID).Scan(&lines); err != nil {
				return fmt.Errorf("count quote lines for decision: %w", err)
			}
			if lines == 0 {
				return &DecisionRefusedError{Msg: "quote: this draft has no lines yet, add what the customer agreed to before marking it accepted"}
			}
		}
	case "ACCEPTED", "CONVERTED":
		if w.To == "ACCEPTED" {
			// The customer door's own words, naming the order the first acceptance became.
			return r.alreadyAcceptedErr(ctx, tx, w.QuoteID, status)
		}
		return &DecisionRefusedError{Msg: fmt.Sprintf(
			"quote: this quote is already %s and cannot be marked lost, open the order it became or ask a manager",
			status)}
	case "LOST":
		return &DecisionRefusedError{Msg: "quote: this quote is already marked lost, nothing more to record; make a new quote if the customer comes back"}
	case "EXPIRED":
		return &DecisionRefusedError{Msg: "quote: this quote has expired, request an updated quote"}
	default:
		return &DecisionRefusedError{Msg: fmt.Sprintf("quote: a quote that is %s cannot be accepted or marked lost", status)}
	}
	return assertNotExpired(validUntil, w.Now)
}

// findKeyedDecision returns the first outcome of an earlier staff decision on this quote under this
// key, or found=false. It must run under the quote row lock. The same key used for a different
// decision or a different channel is ErrIdempotencyKeyReused, never a replay.
func (r *Repository) findKeyedDecision(ctx context.Context, tx rowQuerier, w decisionWrite, quoteNo string) (decisionOutcome, bool, error) {
	var action, channel, before, after string
	var amount int64
	var at time.Time
	err := tx.QueryRow(ctx, `
		SELECT al.action, COALESCE(al.changes->>'channel',''), COALESCE(al.changes->>'before',''),
		       COALESCE(al.changes->>'after',''), COALESCE((al.changes->>'amount_cents')::bigint, 0), al.created_at
		  FROM audit_log al
		 WHERE al.entity_type = 'quote' AND al.entity_id = $1::uuid
		   AND al.changes->>'purpose' = $3 AND al.changes->>'client_id' = $2
		 ORDER BY al.seq LIMIT 1`, w.QuoteID, w.Key, decisionPurpose).Scan(&action, &channel, &before, &after, &amount, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return decisionOutcome{}, false, nil
	}
	if err != nil {
		return decisionOutcome{}, false, fmt.Errorf("look up decision by idempotency key: %w", err)
	}
	if action != w.Action || channel != w.Channel {
		return decisionOutcome{}, false, ErrIdempotencyKeyReused
	}
	return decisionOutcome{QuoteNo: quoteNo, Prev: before, Status: after, Channel: channel, DecidedAt: at, TotalCents: amount, Replayed: true}, true, nil
}

// stopFollowUpsTx ends the quote's follow-up cadence on the decision's transaction: the steps not yet
// taken are written CANCELLED with the reason, the plan is turned off, and a step already sent stays as
// it was. A quote with no enabled plan has nothing to stop. The sweep would end the cadence at its next
// run anyway (a terminal status ends it); this makes the quote say so the moment staff record the
// answer, which is when the salesperson looks.
func stopFollowUpsTx(ctx context.Context, tx pgx.Tx, w decisionWrite, now time.Time) error {
	plan, err := scanPlan(tx.QueryRow(ctx, `SELECT `+planCols+`
		FROM quote_followup_plans WHERE quote_id = $1 AND enabled FOR UPDATE`, w.QuoteID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read follow-up plan for decision: %w", err)
	}
	why := "customer accepted, recorded by staff"
	if w.To == "LOST" {
		why = "customer declined, recorded by staff"
	}
	if _, err := (&FollowUpRepository{}).cancelRemainingTx(ctx, tx, plan, why, now); err != nil {
		return fmt.Errorf("stop follow-ups for decision: %w", err)
	}
	return nil
}

// centsText writes cents as dollars for a refusal a person reads.
func centsText(c int64) string {
	sign := ""
	if c < 0 {
		sign, c = "-", -c
	}
	return fmt.Sprintf("%s$%d.%02d", sign, c/100, c%100)
}
