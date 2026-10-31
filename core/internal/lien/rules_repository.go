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

// DealerRule is the dealer's own holdback rule: its province, and an optional percent that overrides
// the province's. A nil percent is no override, never zero percent.
type DealerRule struct {
	Province       string `json:"province"`
	HoldbackPctBps *int   `json:"holdback_pct_bps"`
}

func (r *Repository) DealerRule(ctx context.Context) (DealerRule, error) {
	q, err := r.q(ctx)
	if err != nil {
		return DealerRule{}, err
	}
	var d DealerRule
	if err := q.QueryRow(ctx, `SELECT province, holdback_pct_bps FROM holdback_dealer_rule WHERE singleton`).
		Scan(&d.Province, &d.HoldbackPctBps); err != nil {
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
		 WHERE singleton RETURNING province, holdback_pct_bps`, pct, nullUUID(setBy)).
		Scan(&d.Province, &d.HoldbackPctBps); err != nil {
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
