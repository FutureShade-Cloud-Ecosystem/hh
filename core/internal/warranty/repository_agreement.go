package warranty

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// repository_agreement.go, the vendor agreement read and write over the 0404 table. The read LEFT
// JOINs the vendor master and answers the default kind for a vendor with no row, so the default
// lives in this one COALESCE and nowhere else; the write upserts on UNIQUE (vendor_id). Touches ONLY
// the warranty-owned warranty_vendor_agreements table plus the vendors master's three display
// columns (zero-trust: a read of another module's master by its primary key, the same reach the
// vendor profile read takes over 0053).

func (r *Repository) VendorAgreements(ctx context.Context) ([]VendorAgreement, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	// Every ACTIVE vendor, in name order, its effective agreement beside it. A vendor the dealer
	// never set answers claim_by_claim from the COALESCE, with the period and allowance cleared.
	const q = `
SELECT v.id::text, v.vendor_no, v.name,
       COALESCE(a.agreement_kind, $1),
       COALESCE(a.period, ''),
       COALESCE(a.allowance_bp, 0),
       COALESCE(a.updated_by::text, ''),
       a.updated_at
  FROM vendors v
  LEFT JOIN warranty_vendor_agreements a ON a.vendor_id = v.id
 WHERE v.is_active
 ORDER BY v.name`
	rows, err := r.pool.Query(ctx, q, AgreementKindClaimByClaim)
	if err != nil {
		return nil, fmt.Errorf("warranty: read vendor agreements: %w", err)
	}
	defer rows.Close()
	var out []VendorAgreement
	for rows.Next() {
		var a VendorAgreement
		var updated *time.Time
		if err := rows.Scan(&a.VendorID, &a.VendorNo, &a.VendorName, &a.AgreementKind, &a.Period, &a.AllowanceBP, &a.UpdatedBy, &updated); err != nil {
			return nil, fmt.Errorf("warranty: scan vendor agreement: %w", err)
		}
		if updated != nil {
			a.UpdatedAt = updated.Format(time.RFC3339)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *Repository) VendorAgreement(ctx context.Context, vendorID string) (VendorAgreement, error) {
	if r.pool == nil {
		return VendorAgreement{}, errNoDB
	}
	const q = `
SELECT v.id::text, v.vendor_no, v.name,
       COALESCE(a.agreement_kind, $2),
       COALESCE(a.period, ''),
       COALESCE(a.allowance_bp, 0),
       COALESCE(a.updated_by::text, ''),
       a.updated_at
  FROM vendors v
  LEFT JOIN warranty_vendor_agreements a ON a.vendor_id = v.id
 WHERE v.id = $1`
	var a VendorAgreement
	var updated *time.Time
	if err := r.pool.QueryRow(ctx, q, vendorID, AgreementKindClaimByClaim).
		Scan(&a.VendorID, &a.VendorNo, &a.VendorName, &a.AgreementKind, &a.Period, &a.AllowanceBP, &a.UpdatedBy, &updated); err != nil {
		return VendorAgreement{}, fmt.Errorf("warranty: read vendor agreement: %w", err)
	}
	if updated != nil {
		a.UpdatedAt = updated.Format(time.RFC3339)
	}
	return a, nil
}

func (r *Repository) UpsertVendorAgreement(ctx context.Context, vendorID string, in AgreementInput, updatedBy string) (VendorAgreement, error) {
	if r.pool == nil {
		return VendorAgreement{}, errNoDB
	}
	_, err := r.pool.Exec(ctx, `
INSERT INTO warranty_vendor_agreements (vendor_id, agreement_kind, period, allowance_bp, updated_by)
VALUES ($1, $2, NULLIF($3, ''), $4, $5)
ON CONFLICT (vendor_id) DO UPDATE SET
  agreement_kind = EXCLUDED.agreement_kind, period = EXCLUDED.period,
  allowance_bp = EXCLUDED.allowance_bp, updated_by = EXCLUDED.updated_by, updated_at = now()`,
		vendorID, in.AgreementKind, in.Period, in.AllowanceBP, nullUUID(updatedBy))
	if err != nil {
		// A vendor_id that is not a real vendor trips the FK (23503) or the uuid cast (22P02). A
		// CLIENT error (unknown vendor), a 422 in words, the same posture the policy write takes for
		// an unknown branch.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "23503" || pgErr.Code == "22P02") {
			return VendorAgreement{}, unprocessable("warranty: unknown vendor %q", vendorID)
		}
		return VendorAgreement{}, fmt.Errorf("warranty: upsert vendor agreement: %w", err)
	}
	return r.VendorAgreement(ctx, vendorID)
}
