package billing

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
)

type creditChangeTxKey struct{}

type creditChangeRowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func withCreditChangeTx(ctx context.Context, tx pgx.Tx) context.Context {
	if tx == nil {
		return ctx
	}
	return customer.WithCallerTx(context.WithValue(ctx, creditChangeTxKey{}, tx), tx)
}

func creditChangeTxFrom(ctx context.Context) pgx.Tx {
	if ctx == nil {
		return nil
	}
	tx, _ := ctx.Value(creditChangeTxKey{}).(pgx.Tx)
	return tx
}

func (r *Repository) creditChangeQuerier(ctx context.Context) creditChangeRowQuerier {
	if tx := creditChangeTxFrom(ctx); tx != nil {
		return tx
	}
	if r == nil || r.pool == nil {
		return nil
	}
	return r.pool
}

// InCreditChangeTx opens a single transaction and carries it on ctx so the credit change
// request lock, customer credit limit update, request status update, and audit write commit
// or roll back together.
func (r *Repository) InCreditChangeTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if r.pool == nil {
		return errNoDB
	}
	if tx := creditChangeTxFrom(ctx); tx != nil {
		return fn(ctx)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("billing: begin credit change tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(withCreditChangeTx(ctx, tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("billing: commit credit change tx: %w", err)
	}
	return nil
}

const creditChangeColumns = `
	id::text,
	customer_id::text,
	COALESCE(branch_id::text, ''),
	change_kind,
	previous_limit_cents,
	new_limit_cents,
	delta_cents,
	COALESCE(hold_kind, ''),
	COALESCE(hold_id::text, ''),
	reason,
	status,
	required_approvals,
	first_threshold_cents,
	second_threshold_cents,
	requested_by::text,
	requested_by_role,
	requested_at,
	COALESCE(first_approved_by::text, ''),
	first_approved_by_role,
	first_approved_at,
	first_approval_note,
	COALESCE(second_approved_by::text, ''),
	second_approved_by_role,
	second_approved_at,
	second_approval_note,
	COALESCE(rejected_by::text, ''),
	rejected_by_role,
	rejected_at,
	rejection_reason,
	applied_at,
	created_at,
	updated_at`

func scanCreditChangeRequest(row pgx.Row) (CreditChangeRequest, error) {
	var c CreditChangeRequest
	err := row.Scan(
		&c.ID,
		&c.CustomerID,
		&c.BranchID,
		&c.ChangeKind,
		&c.PreviousLimitCents,
		&c.NewLimitCents,
		&c.DeltaCents,
		&c.HoldKind,
		&c.HoldID,
		&c.Reason,
		&c.Status,
		&c.RequiredApprovals,
		&c.FirstThresholdCents,
		&c.SecondThresholdCents,
		&c.RequestedBy,
		&c.RequestedByRole,
		&c.RequestedAt,
		&c.FirstApprovedBy,
		&c.FirstApprovedByRole,
		&c.FirstApprovedAt,
		&c.FirstApprovalNote,
		&c.SecondApprovedBy,
		&c.SecondApprovedByRole,
		&c.SecondApprovedAt,
		&c.SecondApprovalNote,
		&c.RejectedBy,
		&c.RejectedByRole,
		&c.RejectedAt,
		&c.RejectionReason,
		&c.AppliedAt,
		&c.CreatedAt,
		&c.UpdatedAt,
	)
	if err != nil {
		return CreditChangeRequest{}, err
	}
	return c, nil
}

// CreateCreditChangeRequest inserts a new credit_change_requests row.
func (r *Repository) CreateCreditChangeRequest(ctx context.Context, req CreditChangeRequest) (CreditChangeRequest, error) {
	if r.pool == nil {
		return CreditChangeRequest{}, errNoDB
	}
	var holdKind *string
	if req.HoldKind != "" {
		holdKind = &req.HoldKind
	}
	out, err := scanCreditChangeRequest(r.pool.QueryRow(ctx, `
		INSERT INTO credit_change_requests (
			customer_id,
			branch_id,
			change_kind,
			previous_limit_cents,
			new_limit_cents,
			delta_cents,
			hold_kind,
			hold_id,
			reason,
			status,
			required_approvals,
			first_threshold_cents,
			second_threshold_cents,
			requested_by,
			requested_by_role,
			requested_at,
			first_approval_note,
			applied_at
		) VALUES (
			$1::uuid,
			NULLIF($2, '')::uuid,
			$3,
			$4,
			$5,
			$6,
			$7,
			NULLIF($8, '')::uuid,
			$9,
			$10,
			$11,
			$12,
			$13,
			$14::uuid,
			$15,
			$16,
			$17,
			$18
		)
		RETURNING `+creditChangeColumns,
		req.CustomerID,
		req.BranchID,
		req.ChangeKind,
		req.PreviousLimitCents,
		req.NewLimitCents,
		req.DeltaCents,
		holdKind,
		req.HoldID,
		req.Reason,
		req.Status,
		req.RequiredApprovals,
		req.FirstThresholdCents,
		req.SecondThresholdCents,
		req.RequestedBy,
		req.RequestedByRole,
		req.RequestedAt,
		req.FirstApprovalNote,
		req.AppliedAt,
	))
	if err != nil {
		return CreditChangeRequest{}, fmt.Errorf("billing: create credit change request: %w", err)
	}
	return out, nil
}

// GetCreditChangeRequest fetches a single credit_change_requests row by ID.
// When called inside InCreditChangeTx, it locks the row FOR UPDATE on that transaction.
func (r *Repository) GetCreditChangeRequest(ctx context.Context, id string) (CreditChangeRequest, error) {
	q := r.creditChangeQuerier(ctx)
	if q == nil {
		return CreditChangeRequest{}, errNoDB
	}
	lockClause := ""
	if creditChangeTxFrom(ctx) != nil {
		lockClause = " FOR UPDATE"
	}
	out, err := scanCreditChangeRequest(q.QueryRow(ctx, `
		SELECT `+creditChangeColumns+`
		  FROM credit_change_requests
		 WHERE id = $1::uuid`+lockClause, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CreditChangeRequest{}, ErrCreditChangeNotFound
		}
		return CreditChangeRequest{}, fmt.Errorf("billing: get credit change request: %w", err)
	}
	return out, nil
}

// UpdateCreditChangeRequest updates a PENDING credit_change_requests row with an approval or rejection.
func (r *Repository) UpdateCreditChangeRequest(ctx context.Context, req CreditChangeRequest) (CreditChangeRequest, error) {
	q := r.creditChangeQuerier(ctx)
	if q == nil {
		return CreditChangeRequest{}, errNoDB
	}
	out, err := scanCreditChangeRequest(q.QueryRow(ctx, `
		UPDATE credit_change_requests
		   SET status                  = $2,
		       first_approved_by       = NULLIF($3, '')::uuid,
		       first_approved_by_role  = $4,
		       first_approved_at       = $5,
		       first_approval_note     = $6,
		       second_approved_by      = NULLIF($7, '')::uuid,
		       second_approved_by_role = $8,
		       second_approved_at      = $9,
		       second_approval_note    = $10,
		       rejected_by             = NULLIF($11, '')::uuid,
		       rejected_by_role        = $12,
		       rejected_at             = $13,
		       rejection_reason        = $14,
		       applied_at              = $15,
		       previous_limit_cents    = $16,
		       delta_cents             = $17,
		       required_approvals      = $18,
		       first_threshold_cents   = $19,
		       second_threshold_cents  = $20,
		       updated_at              = now()
		 WHERE id = $1::uuid AND status = 'PENDING'
		RETURNING `+creditChangeColumns,
		req.ID,
		req.Status,
		req.FirstApprovedBy,
		req.FirstApprovedByRole,
		req.FirstApprovedAt,
		req.FirstApprovalNote,
		req.SecondApprovedBy,
		req.SecondApprovedByRole,
		req.SecondApprovedAt,
		req.SecondApprovalNote,
		req.RejectedBy,
		req.RejectedByRole,
		req.RejectedAt,
		req.RejectionReason,
		req.AppliedAt,
		req.PreviousLimitCents,
		req.DeltaCents,
		req.RequiredApprovals,
		req.FirstThresholdCents,
		req.SecondThresholdCents,
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CreditChangeRequest{}, ErrCreditChangeNotPending
		}
		return CreditChangeRequest{}, fmt.Errorf("billing: update credit change request: %w", err)
	}
	return out, nil
}

// ListCreditChangeRequests lists credit_change_requests matching the filter within the caller's branch reach.
func (r *Repository) ListCreditChangeRequests(ctx context.Context, f CreditChangeFilter, reach BranchReach) ([]CreditChangeRequest, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+creditChangeColumns+`
		  FROM credit_change_requests
		 WHERE ($1 = '' OR customer_id = $1::uuid)
		   AND ($2 = '' OR branch_id = $2::uuid)
		   AND ($3 = '' OR status = $3)
		   AND ($4::uuid[] IS NULL OR branch_id IS NULL OR branch_id = ANY($4::uuid[]))
		 ORDER BY created_at DESC, id DESC
		 LIMIT $5`,
		f.CustomerID, f.BranchID, f.Status, reach.SQLArg(), limit)
	if err != nil {
		return nil, fmt.Errorf("billing: list credit change requests: %w", err)
	}
	defer rows.Close()
	out := []CreditChangeRequest{}
	for rows.Next() {
		item, err := scanCreditChangeRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("billing: scan credit change request: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
