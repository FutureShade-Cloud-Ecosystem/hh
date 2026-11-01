package lien

// rules_repository.go, the pgx access layer of the holdback rule tables (migration 0471):
// holdback_province_rules, holdback_dealer_rule, holdback_job_overrides, and the lien_jobs link column.
//
// EVERY READ AND WRITE HERE RIDES A CALLER'S TRANSACTION when one is carried on the context
// (customer.WithCallerTx): RT-2 asks for the rule inside an invoice's write, and a second pool
// connection asked for there is the pool cycle the till hold read closed.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
)

// querier is what a pool and a transaction both are.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func (r *Repository) q(ctx context.Context) (querier, error) {
	if tx := customer.CallerTx(ctx); tx != nil {
		return tx, nil
	}
	if r.pool == nil {
		return nil, errNoDB
	}
	return r.pool, nil
}

// InTx runs fn on one transaction: the caller's own when one rides the context (customer.WithCallerTx),
// else a new one on the pool that commits when fn returns nil and rolls back on an error. fn's context
// carries the transaction, so every repository call inside rides it.
func (r *Repository) InTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if customer.CallerTx(ctx) != nil {
		return fn(ctx)
	}
	if r.pool == nil {
		return errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("lien: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(customer.WithCallerTx(ctx, tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RuleInputs is everything the rule for one job reads, from ONE statement and so one snapshot, inside
// a caller's transaction or not: the dealer rule, the province rule (the site's province when sitePro
// is set, else the dealer's), and the job's override.
type RuleInputs struct {
	Dealer      DealerRule
	Province    ProvinceRule
	HasProvince bool
	JobOverride *int
}

func (r *Repository) RuleInputs(ctx context.Context, sitePro, customerJobID string) (RuleInputs, error) {
	q, err := r.q(ctx)
	if err != nil {
		return RuleInputs{}, err
	}
	var in RuleInputs
	var ppct, pres, perf *int32
	var prov *string
	if err := q.QueryRow(ctx, `
		SELECT d.id::text, d.province, d.holdback_pct_bps,
		       p.province, p.holdback_pct_bps, p.preserve_days, p.perfect_days,
		       (SELECT o.holdback_pct_bps FROM holdback_job_overrides o WHERE o.customer_job_id = $2)
		FROM holdback_dealer_rule d
		LEFT JOIN holdback_province_rules p ON p.province = COALESCE(NULLIF($1, ''), d.province)
		WHERE d.singleton`, sitePro, customerJobID).
		Scan(&in.Dealer.ID, &in.Dealer.Province, &in.Dealer.HoldbackPctBps, &prov, &ppct, &pres, &perf, &in.JobOverride); err != nil {
		return RuleInputs{}, fmt.Errorf("lien: read the holdback rule inputs: %w", err)
	}
	if prov != nil {
		in.HasProvince = true
		in.Province = ProvinceRule{Province: *prov, HoldbackPctBps: int(*ppct), PreserveDays: int(*pres), PerfectDays: int(*perf)}
	}
	return in, nil
}

// DealerRule is the dealer's own holdback rule: its province, and an optional percent that overrides
// the province's. A nil percent is no override, never zero percent.
type DealerRule struct {
	ID             string `json:"-"` // the row's own uuid, the entity id of its audit record; never on the wire
	Province       string `json:"province"`
	HoldbackPctBps *int   `json:"holdback_pct_bps"`
}

func (r *Repository) DealerRule(ctx context.Context) (DealerRule, error) {
	q, err := r.q(ctx)
	if err != nil {
		return DealerRule{}, err
	}
	var d DealerRule
	if err := q.QueryRow(ctx, `SELECT id::text, province, holdback_pct_bps FROM holdback_dealer_rule WHERE singleton`).
		Scan(&d.ID, &d.Province, &d.HoldbackPctBps); err != nil {
		return DealerRule{}, fmt.Errorf("lien: read the dealer holdback rule: %w", err)
	}
	return d, nil
}

func (r *Repository) SetDealerOverride(ctx context.Context, pct *int, setBy string) (DealerRule, error) {
	q, err := r.q(ctx)
	if err != nil {
		return DealerRule{}, err
	}
	var d DealerRule
	if err := q.QueryRow(ctx,
		`UPDATE holdback_dealer_rule SET holdback_pct_bps = $1, set_by = $2, updated_at = now()
		 WHERE singleton RETURNING id::text, province, holdback_pct_bps`, pct, nullUUID(setBy)).
		Scan(&d.ID, &d.Province, &d.HoldbackPctBps); err != nil {
		return DealerRule{}, fmt.Errorf("lien: set the dealer holdback override: %w", err)
	}
	return d, nil
}

// SetJobOverride stores (pct set) or removes (pct nil) a job's override. branchID is the job's branch.
func (r *Repository) SetJobOverride(ctx context.Context, customerJobID, branchID string, pct *int, setBy string) error {
	q, err := r.q(ctx)
	if err != nil {
		return err
	}
	if pct == nil {
		if _, err := q.Exec(ctx, `DELETE FROM holdback_job_overrides WHERE customer_job_id = $1`, customerJobID); err != nil {
			return fmt.Errorf("lien: clear the job holdback override: %w", err)
		}
		return nil
	}
	if _, err := q.Exec(ctx,
		`INSERT INTO holdback_job_overrides (customer_job_id, branch_id, holdback_pct_bps, set_by)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (customer_job_id) DO UPDATE
		   SET holdback_pct_bps = EXCLUDED.holdback_pct_bps, branch_id = EXCLUDED.branch_id,
		       set_by = EXCLUDED.set_by, set_at = now()`,
		customerJobID, branchID, *pct, nullUUID(setBy)); err != nil {
		return fmt.Errorf("lien: set the job holdback override: %w", err)
	}
	return nil
}

// ProvinceRule is one province's statutory holdback rule.
type ProvinceRule struct {
	Province       string `json:"province"`
	HoldbackPctBps int    `json:"holdback_pct_bps"`
	PreserveDays   int    `json:"preserve_days"`
	PerfectDays    int    `json:"perfect_days"`
}

// ProvinceRule reads one province's rule; ok is false when the province has none.
func (r *Repository) ProvinceRule(ctx context.Context, province string) (rule ProvinceRule, ok bool, err error) {
	q, err := r.q(ctx)
	if err != nil {
		return ProvinceRule{}, false, err
	}
	err = q.QueryRow(ctx,
		`SELECT province, holdback_pct_bps, preserve_days, perfect_days FROM holdback_province_rules WHERE province = $1`, province).
		Scan(&rule.Province, &rule.HoldbackPctBps, &rule.PreserveDays, &rule.PerfectDays)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProvinceRule{}, false, nil
	}
	if err != nil {
		return ProvinceRule{}, false, fmt.Errorf("lien: read the province holdback rule: %w", err)
	}
	return rule, true, nil
}

// JobOverride reads a customer job's override; nil when the job has none.
func (r *Repository) JobOverride(ctx context.Context, customerJobID string) (*int, error) {
	q, err := r.q(ctx)
	if err != nil {
		return nil, err
	}
	var pct int
	err = q.QueryRow(ctx, `SELECT holdback_pct_bps FROM holdback_job_overrides WHERE customer_job_id = $1`, customerJobID).Scan(&pct)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lien: read the job holdback override: %w", err)
	}
	return &pct, nil
}

// LinkCustomerJob sets (customerJobID set) or clears ("") the customer job a lien job links to.
func (r *Repository) LinkCustomerJob(ctx context.Context, lienJobID, customerJobID string) error {
	q, err := r.q(ctx)
	if err != nil {
		return err
	}
	tag, err := q.Exec(ctx, `UPDATE lien_jobs SET customer_job_id = $2, updated_at = now() WHERE id = $1`, lienJobID, nullStr(customerJobID))
	if err != nil {
		return fmt.Errorf("lien: link the customer job: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errJobNotFound
	}
	return nil
}

// LockJobLink takes a lien job's row FOR UPDATE and answers the customer job it is linked to ("" when none).
// It is the first lock of a link change; a withhold takes the same row FOR SHARE first (LinkedLienJobLocked).
func (r *Repository) LockJobLink(ctx context.Context, lienJobID string) (string, error) {
	q, err := r.q(ctx)
	if err != nil {
		return "", err
	}
	var id *string
	err = q.QueryRow(ctx, `SELECT customer_job_id::text FROM lien_jobs WHERE id = $1 FOR UPDATE`, lienJobID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errJobNotFound
	}
	if err != nil {
		return "", fmt.Errorf("lien: lock the lien job's link: %w", err)
	}
	if id == nil {
		return "", nil
	}
	return *id, nil
}

// JobHoldsInvoiceRows answers whether a lien job's ledger holds any row that names an invoice.
func (r *Repository) JobHoldsInvoiceRows(ctx context.Context, lienJobID string) (bool, error) {
	q, err := r.q(ctx)
	if err != nil {
		return false, err
	}
	var held bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM holdback_ledger WHERE job_id = $1 AND invoice_id IS NOT NULL)`, lienJobID).Scan(&held); err != nil {
		return false, fmt.Errorf("lien: read the job's invoice level rows: %w", err)
	}
	return held, nil
}

// ProvinceRules lists every province's rule, by province code.
func (r *Repository) ProvinceRules(ctx context.Context) ([]ProvinceRule, error) {
	if tx := customer.CallerTx(ctx); tx == nil && r.pool == nil {
		return nil, errNoDB
	}
	var rows pgx.Rows
	var err error
	const stmt = `SELECT province, holdback_pct_bps, preserve_days, perfect_days FROM holdback_province_rules ORDER BY province`
	if tx := customer.CallerTx(ctx); tx != nil {
		rows, err = tx.Query(ctx, stmt)
	} else {
		rows, err = r.pool.Query(ctx, stmt)
	}
	if err != nil {
		return nil, fmt.Errorf("lien: list the province holdback rules: %w", err)
	}
	defer rows.Close()
	var out []ProvinceRule
	for rows.Next() {
		var p ProvinceRule
		if err := rows.Scan(&p.Province, &p.HoldbackPctBps, &p.PreserveDays, &p.PerfectDays); err != nil {
			return nil, fmt.Errorf("lien: scan a province holdback rule: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
