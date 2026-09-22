package quote

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// followup_repository.go, persistence for the cadence (0110) plus the one read of the quotes table
// the sweep needs.

// FollowUpRepository owns quote_followup_plans and quote_followup_events. A nil pool makes every read
// empty and every write an error, the scaffold posture the rest of this codebase uses.
type FollowUpRepository struct{ pool *pgxpool.Pool }

// NewFollowUpRepository builds the store.
func NewFollowUpRepository(pool *pgxpool.Pool) *FollowUpRepository {
	return &FollowUpRepository{pool: pool}
}

// QuoteCadenceHeader is everything the sweep needs to decide and render one quote's step, read in one
// query so status and money cannot be torn against each other.
type QuoteCadenceHeader struct {
	QuoteID    string
	QuoteNo    string
	CustomerID string
	Status     string
	TotalCents int64
	ValidUntil *time.Time
}

// Header reads one quote's cadence-relevant facts.
func (r *FollowUpRepository) Header(ctx context.Context, quoteID string) (QuoteCadenceHeader, error) {
	var h QuoteCadenceHeader
	if r.pool == nil {
		return h, ErrFollowUpNotWired
	}
	err := r.pool.QueryRow(ctx, `
		SELECT id::text, quote_no::text, customer_id::text, status, total_cents, valid_until
		FROM quotes WHERE id = $1`, quoteID).
		Scan(&h.QuoteID, &h.QuoteNo, &h.CustomerID, &h.Status, &h.TotalCents, &h.ValidUntil)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return h, ErrNotFound
		}
		return h, fmt.Errorf("quote: cadence header: %w", err)
	}
	return h, nil
}

// EnsurePlan creates a plan for a quote if it has none, and returns the plan either way.
//
// IT DOES NOT RESTART AN EXISTING CADENCE, and that is the whole reason it is an upsert-that-does-
// nothing rather than an insert-or-update. Re-sharing a quote on day 6 must not reset the clock to
// day 0, the customer's day-3 message already went out, and a plan that restarts on every share is
// how a customer receives the same three messages twice.
func (r *FollowUpRepository) EnsurePlan(ctx context.Context, quoteID, channel string, offsets []int, startedAt time.Time) (FollowUpPlan, error) {
	if r.pool == nil {
		return FollowUpPlan{}, ErrFollowUpNotWired
	}
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO quote_followup_plans (quote_id, channel, offsets_days, started_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (quote_id) DO NOTHING`,
		quoteID, channel, offsets, startedAt.UTC()); err != nil {
		return FollowUpPlan{}, fmt.Errorf("quote: ensure follow-up plan: %w", err)
	}
	return r.Plan(ctx, quoteID)
}

const planCols = `quote_id::text, enabled, channel, offsets_days, started_at, disabled_at, disabled_why`

func scanPlan(row pgx.Row) (FollowUpPlan, error) {
	var p FollowUpPlan
	err := row.Scan(&p.QuoteID, &p.Enabled, &p.Channel, &p.OffsetsDays, &p.StartedAt, &p.DisabledAt, &p.DisabledWhy)
	return p, err
}

// Plan reads one quote's plan. A missing plan is ErrNotFound, "this quote has no cadence" is a
// legitimate answer the caller renders, not an error condition.
func (r *FollowUpRepository) Plan(ctx context.Context, quoteID string) (FollowUpPlan, error) {
	if r.pool == nil {
		return FollowUpPlan{}, ErrFollowUpNotWired
	}
	p, err := scanPlan(r.pool.QueryRow(ctx, `SELECT `+planCols+` FROM quote_followup_plans WHERE quote_id = $1`, quoteID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return FollowUpPlan{}, ErrNotFound
		}
		return FollowUpPlan{}, fmt.Errorf("quote: read follow-up plan: %w", err)
	}
	return p, nil
}

// EnabledPlans returns the plans the sweep should consider, oldest first.
func (r *FollowUpRepository) EnabledPlans(ctx context.Context, limit int) ([]FollowUpPlan, error) {
	if r.pool == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 500
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+planCols+` FROM quote_followup_plans WHERE enabled ORDER BY started_at LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("quote: list follow-up plans: %w", err)
	}
	defer rows.Close()
	var out []FollowUpPlan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, fmt.Errorf("quote: scan follow-up plan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetEnabled is the one-click switch. Disabling records who and why; re-enabling clears both, because
// a stale "disabled by Alex on Tuesday" beside an enabled plan reads as a bug.
//
// It does NOT delete history. Turning the cadence off stops future steps and leaves every message
// already sent visible on the quote, the salesperson's next phone call depends on knowing what the
// customer has already received.
func (r *FollowUpRepository) SetEnabled(ctx context.Context, quoteID string, enabled bool, actorID, why string) error {
	if r.pool == nil {
		return ErrFollowUpNotWired
	}
	var at, by any
	if !enabled {
		at = time.Now().UTC()
		if actorID != "" {
			by = actorID
		}
	} else {
		why = ""
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE quote_followup_plans
		SET enabled = $2, disabled_at = $3, disabled_by = $4, disabled_why = $5, updated_at = now()
		WHERE quote_id = $1`, quoteID, enabled, at, by, why)
	if err != nil {
		return fmt.Errorf("quote: set follow-up enabled: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Events reads one quote's recorded steps, oldest first.
func (r *FollowUpRepository) Events(ctx context.Context, quoteID string) ([]FollowUpEvent, error) {
	if r.pool == nil {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT step_day, channel, status, reason, COALESCE(event_id::text, ''), scheduled_for, occurred_at
		FROM quote_followup_events WHERE quote_id = $1
		ORDER BY scheduled_for, channel`, quoteID)
	if err != nil {
		return nil, fmt.Errorf("quote: read follow-up events: %w", err)
	}
	defer rows.Close()
	var out []FollowUpEvent
	for rows.Next() {
		var e FollowUpEvent
		if err := rows.Scan(&e.StepDay, &e.Channel, &e.Status, &e.Reason, &e.EventID, &e.ScheduledFor, &e.OccurredAt); err != nil {
			return nil, fmt.Errorf("quote: scan follow-up event: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ClaimStep inserts the ledger row for one (quote, step, channel) and reports whether THIS call created
// it. An empty result means somebody already decided this step, the idempotent path, guaranteed by
// the UNIQUE constraint in 0110 rather than by the shape of the caller's loop.
//
// It runs on a caller-supplied transaction so the claim and the enqueue commit together: there is no
// interval in which a step is recorded as sent but not queued, or queued but not recorded.
func (r *FollowUpRepository) ClaimStep(ctx context.Context, tx pgx.Tx, quoteID string, stepDay int, channel string, scheduledFor time.Time) (bool, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO quote_followup_events (quote_id, step_day, channel, status, scheduled_for)
		VALUES ($1, $2, $3, 'SENT', $4)
		ON CONFLICT (quote_id, step_day, channel) DO NOTHING
		RETURNING id::text`, quoteID, stepDay, channel, scheduledFor.UTC()).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("quote: claim follow-up step: %w", err)
	}
	return true, nil
}

// SettleStep stamps a claimed step with its outcome.
func (r *FollowUpRepository) SettleStep(ctx context.Context, tx pgx.Tx, quoteID string, stepDay int, channel, status, reason, eventID string) error {
	var ev any
	if eventID != "" {
		ev = eventID
	}
	if _, err := tx.Exec(ctx, `
		UPDATE quote_followup_events SET status = $4, reason = $5, event_id = $6
		WHERE quote_id = $1 AND step_day = $2 AND channel = $3`,
		quoteID, stepDay, channel, status, reason, ev); err != nil {
		return fmt.Errorf("quote: settle follow-up step: %w", err)
	}
	return nil
}

// CancelRemaining records every not-yet-taken step of a plan as CANCELLED and turns the plan off, in
// one transaction.
//
// The cancelled rows are written rather than merely implied by the disabled plan, because the quote
// surface must be able to say "the day-14 message was cancelled when the customer accepted" months
// later, when nobody remembers what the cadence used to be.
func (r *FollowUpRepository) CancelRemaining(ctx context.Context, plan FollowUpPlan, why string, now time.Time) (int, error) {
	if r.pool == nil {
		return 0, ErrFollowUpNotWired
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cancelled := 0
	for _, d := range plan.OffsetsDays {
		due := plan.StartedAt.AddDate(0, 0, d)
		for _, ch := range ChannelsOf(plan.Channel) {
			claimed, err := r.ClaimStep(ctx, tx, plan.QuoteID, d, ch, due)
			if err != nil {
				return cancelled, err
			}
			if !claimed {
				continue // already sent or already decided, history is not rewritten
			}
			if err := r.SettleStep(ctx, tx, plan.QuoteID, d, ch, StepCancelled, why, ""); err != nil {
				return cancelled, err
			}
			cancelled++
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE quote_followup_plans
		SET enabled = FALSE, disabled_at = $2, disabled_why = $3, updated_at = now()
		WHERE quote_id = $1`, plan.QuoteID, now.UTC(), why); err != nil {
		return cancelled, fmt.Errorf("quote: disable plan: %w", err)
	}
	return cancelled, tx.Commit(ctx)
}

// Begin exposes a transaction to the service (the claim + enqueue pair needs one).
func (r *FollowUpRepository) Begin(ctx context.Context) (pgx.Tx, error) {
	if r.pool == nil {
		return nil, ErrFollowUpNotWired
	}
	return r.pool.Begin(ctx)
}
