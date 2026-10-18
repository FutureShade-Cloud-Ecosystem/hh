package warranty

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// repository_policy.go, the warranty_policy read and write over the 0396 table. The shape is the pos
// policy's (internal/pos/repository_w7.go): the branch row overrides the org NULL row, and the org
// row (branch_id IS NULL) updates in place because UNIQUE does not cover NULLs. Touches ONLY the
// warranty-owned warranty_policy table (zero-trust).

func (r *Repository) WarrantyPolicy(ctx context.Context, branchID string) (WarrantyPolicy, error) {
	if r.pool == nil {
		return WarrantyPolicy{}, errNoDB
	}
	// The branch override wins when present; otherwise the org default (branch_id IS NULL, seeded by
	// 0396). A scope with neither row is an error: the migration guarantees the seed.
	const q = `
SELECT COALESCE(branch_id::text,''), claim_threshold_cents, COALESCE(updated_by::text,''), updated_at
  FROM warranty_policy
 WHERE ($1 <> '' AND branch_id::text = $1) OR branch_id IS NULL
 ORDER BY branch_id::text NULLS LAST
 LIMIT 1`
	var p WarrantyPolicy
	var by string
	var updated time.Time
	if err := r.pool.QueryRow(ctx, q, branchID).Scan(&p.BranchID, &p.ClaimThresholdCents, &by, &updated); err != nil {
		return WarrantyPolicy{}, fmt.Errorf("warranty: read policy: %w", err)
	}
	p.UpdatedBy = by
	p.UpdatedAt = updated.Format(time.RFC3339)
	return p, nil
}

func (r *Repository) UpsertWarrantyPolicy(ctx context.Context, in PolicyInput, updatedBy string) (WarrantyPolicy, error) {
	if r.pool == nil {
		return WarrantyPolicy{}, errNoDB
	}
	if in.BranchID == "" {
		// The org-wide default row (branch_id IS NULL, UNIQUE does not cover NULLs, so update in place).
		_, err := r.pool.Exec(ctx,
			`UPDATE warranty_policy
			    SET claim_threshold_cents = $1, updated_by = $2, updated_at = now()
			  WHERE branch_id IS NULL`,
			in.ClaimThresholdCents, nullUUID(updatedBy))
		if err != nil {
			return WarrantyPolicy{}, fmt.Errorf("warranty: update policy: %w", err)
		}
		return r.WarrantyPolicy(ctx, in.BranchID)
	}
	_, err := r.pool.Exec(ctx, `
INSERT INTO warranty_policy (branch_id, claim_threshold_cents, updated_by)
VALUES ($1, $2, $3)
ON CONFLICT (branch_id) DO UPDATE SET
  claim_threshold_cents = EXCLUDED.claim_threshold_cents,
  updated_by = EXCLUDED.updated_by, updated_at = now()`,
		nullUUID(in.BranchID), in.ClaimThresholdCents, nullUUID(updatedBy))
	if err != nil {
		// A branch_id that is not a real branch (a typo'd id, or a non-UUID) trips the FK (23503) or the
		// uuid cast (22P02). A CLIENT error (unknown branch), a 422 in words, the same posture the
		// vendor profile upsert takes for an unknown vendor.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "23503" || pgErr.Code == "22P02") {
			return WarrantyPolicy{}, unprocessable("warranty: unknown branch %q", in.BranchID)
		}
		return WarrantyPolicy{}, fmt.Errorf("warranty: upsert policy: %w", err)
	}
	return r.WarrantyPolicy(ctx, in.BranchID)
}
