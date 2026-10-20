package assist

// spend_repository.go, the Postgres side of the spend and price tables.
//
// The tables are created by the slice's migration (assist_spend, assist_model_prices). Every method
// here applies the fold of FoldCalls in SQL: per call (exchange id, call number) the settlement
// wins, then a void, then the open reservation, and a void contributes nothing.

import (
	"context"
	"fmt"
	"time"
)

var (
	_ SpendStore = (*Repository)(nil)
	_ PriceStore = (*Repository)(nil)
)

const spendColumns = `kind, exchange_id::text, call_no, audience,
	COALESCE(staff_user_id::text, ''), COALESCE(portal_user_id::text, ''), role_code::text,
	alias, model, tokens_in, tokens_out, tokens_cached, cost_micro_cents, COALESCE(price_id::text, ''), settled_by, at`

// foldedCalls selects one effective row per call inside the window. The window is on the row's at,
// and a settlement or void carries its reservation's at, so a call never straddles a month.
const foldedCalls = `(SELECT DISTINCT ON (exchange_id, call_no) *
	  FROM assist_spend
	 WHERE at >= $1 AND at < $2
	 ORDER BY exchange_id, call_no,
	          CASE kind WHEN 'settlement' THEN 0 WHEN 'void' THEN 1 ELSE 2 END) f`

const insertSpendSQL = `
	INSERT INTO assist_spend (kind, exchange_id, call_no, audience, staff_user_id, portal_user_id, role_code,
	                          alias, model, tokens_in, tokens_out, tokens_cached, cost_micro_cents, price_id, settled_by, at)
	VALUES ($1, $2::uuid, $3, $4, NULLIF($5,'')::uuid, NULLIF($6,'')::uuid, $7,
	        $8, $9, $10, $11, $12, $13, NULLIF($14,'')::uuid, $15, $16)`

func (r *Repository) appendSpend(ctx context.Context, row SpendRow) error {
	if err := row.Validate(); err != nil {
		return err
	}
	at := row.At
	if at.IsZero() {
		at = time.Now()
	}
	_, err := r.pool.Exec(ctx, insertSpendSQL,
		string(row.Kind), row.ExchangeID, row.CallNo, string(row.Audience), row.StaffUserID, row.PortalUserID, row.RoleCode,
		row.Alias, row.Model, row.TokensIn, row.TokensOut, row.TokensCached, row.CostMicroCents, row.PriceID, row.SettledBy, at)
	if err != nil {
		return fmt.Errorf("assist: append spend %s: %w", row.Kind, err)
	}
	return nil
}

// Reserve, Settle and Void are INSERTs. The table's unique key on (exchange, call, kind) is what
// stops a call being settled twice.
func (r *Repository) Reserve(ctx context.Context, row SpendRow) error { return r.appendSpend(ctx, row) }
func (r *Repository) Settle(ctx context.Context, row SpendRow) error  { return r.appendSpend(ctx, row) }
func (r *Repository) Void(ctx context.Context, row SpendRow) error    { return r.appendSpend(ctx, row) }

func (r *Repository) UserMonth(ctx context.Context, staffUserID string, from, to time.Time) (Spend, error) {
	var s Spend
	err := r.pool.QueryRow(ctx, `
		SELECT count(DISTINCT exchange_id), COALESCE(sum(tokens_in), 0), COALESCE(sum(tokens_out), 0)
		  FROM `+foldedCalls+`
		 WHERE f.kind <> 'void' AND f.staff_user_id = $3::uuid`, from, to, staffUserID).
		Scan(&s.Exchanges, &s.TokensIn, &s.TokensOut)
	if err != nil {
		return Spend{}, fmt.Errorf("assist: read user month spend: %w", err)
	}
	return s, nil
}

func (r *Repository) UsersMonth(ctx context.Context, from, to time.Time) (map[string]Spend, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT f.staff_user_id::text, count(DISTINCT f.exchange_id), COALESCE(sum(f.tokens_in), 0), COALESCE(sum(f.tokens_out), 0)
		  FROM `+foldedCalls+`
		 WHERE f.kind <> 'void' AND f.staff_user_id IS NOT NULL
		 GROUP BY f.staff_user_id`, from, to)
	if err != nil {
		return nil, fmt.Errorf("assist: read users month spend: %w", err)
	}
	defer rows.Close()
	out := map[string]Spend{}
	for rows.Next() {
		var id string
		var s Spend
		if err := rows.Scan(&id, &s.Exchanges, &s.TokensIn, &s.TokensOut); err != nil {
			return nil, fmt.Errorf("assist: scan users month spend: %w", err)
		}
		out[id] = s
	}
	return out, rows.Err()
}

func (r *Repository) DealerMonthMicroCents(ctx context.Context, from, to time.Time) (int64, error) {
	var total int64
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(sum(f.cost_micro_cents), 0) FROM `+foldedCalls+` WHERE f.kind <> 'void'`, from, to).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("assist: read dealer month spend: %w", err)
	}
	return total, nil
}

// dealerLockKey and userLockKey name the advisory locks ReserveWithin takes. Dealer first, then
// user, in every call, so two calls cannot wait on each other in opposite orders.
const dealerLockKey = "assist.spend.dealer"

func userLockKey(id string) string { return "assist.spend.user:" + id }

// ReserveWithin is the atomic check and reservation (see SpendStore). One transaction: the dealer
// lock (paid providers only), then the user lock, then the folded sums are read INSIDE the locks
// (READ COMMITTED takes a fresh snapshot per statement, so a reservation committed by the lock's
// previous holder is seen), then the reservation is inserted only if within both limits. The locks
// are transaction scoped and drop at commit or rollback.
func (r *Repository) ReserveWithin(ctx context.Context, row SpendRow, lim ReserveLimits) error {
	if err := row.Validate(); err != nil {
		return err
	}
	at := row.At
	if at.IsZero() {
		at = time.Now()
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("assist: begin reservation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if lim.CheckDealer {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, dealerLockKey); err != nil {
			return fmt.Errorf("assist: dealer lock: %w", err)
		}
	}
	if row.StaffUserID != "" {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, userLockKey(row.StaffUserID)); err != nil {
			return fmt.Errorf("assist: user lock: %w", err)
		}
		var spent int64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(sum(f.tokens_in + f.tokens_out), 0)
			  FROM `+foldedCalls+`
			 WHERE f.kind <> 'void' AND f.staff_user_id = $3::uuid`, lim.From, lim.To, row.StaffUserID).Scan(&spent); err != nil {
			return fmt.Errorf("assist: read user month inside the lock: %w", err)
		}
		if spent >= int64(lim.BudgetTokens) {
			return ErrOverBudget
		}
	}
	if lim.CheckDealer {
		var used int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(f.cost_micro_cents), 0) FROM `+foldedCalls+` WHERE f.kind <> 'void'`, lim.From, lim.To).Scan(&used); err != nil {
			return fmt.Errorf("assist: read dealer month inside the lock: %w", err)
		}
		if used >= lim.DealerCapMicroCents || used+row.CostMicroCents > lim.DealerCapMicroCents {
			return ErrDealerCap
		}
	}
	if _, err := tx.Exec(ctx, insertSpendSQL,
		string(row.Kind), row.ExchangeID, row.CallNo, string(row.Audience), row.StaffUserID, row.PortalUserID, row.RoleCode,
		row.Alias, row.Model, row.TokensIn, row.TokensOut, row.TokensCached, row.CostMicroCents, row.PriceID, row.SettledBy, at); err != nil {
		return fmt.Errorf("assist: append reservation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("assist: commit reservation: %w", err)
	}
	return nil
}

func scanSpend(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]SpendRow, error) {
	var out []SpendRow
	for rows.Next() {
		var s SpendRow
		var kind, aud string
		if err := rows.Scan(&kind, &s.ExchangeID, &s.CallNo, &aud, &s.StaffUserID, &s.PortalUserID, &s.RoleCode,
			&s.Alias, &s.Model, &s.TokensIn, &s.TokensOut, &s.TokensCached, &s.CostMicroCents, &s.PriceID, &s.SettledBy, &s.At); err != nil {
			return nil, fmt.Errorf("assist: scan spend: %w", err)
		}
		s.Kind, s.Audience = SpendKind(kind), Audience(aud)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repository) Unsettled(ctx context.Context, reservedBefore time.Time) ([]SpendRow, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+spendColumns+`
		  FROM assist_spend r
		 WHERE r.kind = 'reservation' AND r.at < $1
		   AND NOT EXISTS (SELECT 1 FROM assist_spend o
		                    WHERE o.exchange_id = r.exchange_id AND o.call_no = r.call_no AND o.kind <> 'reservation')
		 ORDER BY r.at`, reservedBefore)
	if err != nil {
		return nil, fmt.Errorf("assist: read unsettled reservations: %w", err)
	}
	defer rows.Close()
	return scanSpend(rows)
}

func (r *Repository) Settled(ctx context.Context, from, to time.Time) ([]SpendRow, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+spendColumns+` FROM assist_spend
		 WHERE kind = 'settlement' AND at >= $1 AND at < $2 ORDER BY at, exchange_id, call_no`, from, to)
	if err != nil {
		return nil, fmt.Errorf("assist: read settlements: %w", err)
	}
	defer rows.Close()
	return scanSpend(rows)
}

// PriceFor returns the row in force: the highest effective seq for the alias and model.
func (r *Repository) PriceFor(ctx context.Context, alias, model string) (ModelPrice, bool, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, alias, model, price_in_micro_per_m, price_out_micro_per_m, price_cached_micro_per_m, effective_seq
		  FROM assist_model_prices WHERE alias = $1 AND model = $2
		 ORDER BY effective_seq DESC LIMIT 1`, alias, model)
	if err != nil {
		return ModelPrice{}, false, fmt.Errorf("assist: read price: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return ModelPrice{}, false, rows.Err()
	}
	var p ModelPrice
	if err := rows.Scan(&p.ID, &p.Alias, &p.Model, &p.InMicroPerM, &p.OutMicroPerM, &p.CachedMicroPerM, &p.Seq); err != nil {
		return ModelPrice{}, false, fmt.Errorf("assist: scan price: %w", err)
	}
	return p, true, nil
}
