package landedcost

// ap_payable.go, the module's own read of the landed-cost payable for the month walk's AP tie-out.
// The landed-cost documents post their freight/duty/handling payable to GL 2100, the same account
// the vendor bills ride, so the tie reads this balance through the module's own seam rather than
// naming the module's table from the outside.

import (
	"context"
	"fmt"
	"time"
)

// OutstandingPayableCentsAsOf returns the landed-cost payable still owed at asOf: the CAD total of
// every document applied on or before asOf and not yet voided at asOf.
//
// The landed_costs header carries no applied_at/voided_at columns, so the read rides the status
// stamps SetStatusTx writes: a POSTED (or retro-adjusted) header's updated_at is its apply, a
// VOIDED header's updated_at is its void, and created_at always precedes the apply. A document
// created after asOf is never counted, so a document that does not exist at asOf cannot enter the
// balance from either side. The one shape the read cannot separate is a document created before
// asOf but applied after it and voided later still: created_at admits it while the GL at asOf has
// nothing of it. Apply follows Create within one posting in this product's every path, so the
// window is the apply call's own length.
func (s *Service) OutstandingPayableCentsAsOf(ctx context.Context, asOf time.Time) (int64, error) {
	return s.repo.OutstandingPayableCentsAsOf(ctx, asOf)
}

// OutstandingPayableCentsAsOf is the repository read behind the service seam.
func (r *Repository) OutstandingPayableCentsAsOf(ctx context.Context, asOf time.Time) (int64, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	var total int64
	if err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(freight_cad_cents + duty_cad_cents + handling_cad_cents), 0)
		  FROM landed_costs
		 WHERE created_at <= $1
		   AND (
		         (status IN ('POSTED', 'RETRO_ADJUSTED') AND updated_at <= $1)
		      OR (status = 'VOIDED' AND updated_at > $1)
		       )`, asOf).Scan(&total); err != nil {
		return 0, fmt.Errorf("landedcost: outstanding payable as of %s: %w", asOf.Format("2006-01-02 15:04:05"), err)
	}
	return total, nil
}

// OpenPayable is an open landed-cost payable owed to a freight carrier or
// customs broker as of an instant, exposed across the landedcost seam so the
// purchasing AP aging rule never queries landed_costs directly.
type OpenPayable struct {
	ID               string    `json:"id"`
	POID             string    `json:"po_id"`
	PORef            string    `json:"po_ref"`
	BranchID         string    `json:"branch_id"`
	Currency         string    `json:"currency"`
	FreightCadCents  int64     `json:"freight_cad_cents"`
	DutyCadCents     int64     `json:"duty_cad_cents"`
	HandlingCadCents int64     `json:"handling_cad_cents"`
	TotalCadCents    int64     `json:"total_cad_cents"`
	ChargeKind       string    `json:"charge_kind"`
	PayeeLabel       string    `json:"payee_label"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// OpenPayablesAsOf returns all landed-cost payables that were open as of asOf
// within the requested branch reach.
func (s *Service) OpenPayablesAsOf(ctx context.Context, asOf time.Time, branchIDs []string, global bool) ([]OpenPayable, error) {
	if s == nil || s.repo == nil {
		return nil, nil
	}
	return s.repo.OpenPayablesAsOf(ctx, asOf, branchIDs, global)
}

// OpenPayablesAsOf reads open landed-cost payables from landed_costs as of asOf.
func (r *Repository) OpenPayablesAsOf(ctx context.Context, asOf time.Time, branchIDs []string, global bool) ([]OpenPayable, error) {
	if r == nil || r.pool == nil {
		return nil, errNoDB
	}
	if !global && len(branchIDs) == 0 {
		return []OpenPayable{}, nil
	}
	if branchIDs == nil {
		branchIDs = []string{}
	}
	if asOf.IsZero() {
		asOf = time.Now().UTC()
	}

	rows, err := r.pool.Query(ctx, `
		SELECT lc.id::text, lc.po_id::text, lc.po_ref, lc.branch_id::text, trim(lc.currency),
		       lc.freight_cad_cents, lc.duty_cad_cents, lc.handling_cad_cents,
		       (lc.freight_cad_cents + lc.duty_cad_cents + lc.handling_cad_cents) AS total_cad_cents,
		       lc.created_at, lc.updated_at
		  FROM landed_costs lc
		 WHERE (
		           (lc.status = 'POSTED' AND lc.updated_at <= $1)
		        OR (lc.status = 'RETRO_ADJUSTED' AND lc.created_at <= $1)
		        OR (lc.status = 'VOIDED' AND lc.gl_journal_id IS NOT NULL AND lc.created_at <= $1 AND lc.updated_at > $1)
		       )
		   AND (lc.freight_cad_cents + lc.duty_cad_cents + lc.handling_cad_cents) > 0
		   AND ($2::boolean OR lc.branch_id = ANY($3::uuid[]))
		 ORDER BY lc.created_at ASC, lc.id ASC`,
		asOf, global, branchIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("landedcost: open payables as of: %w", err)
	}
	defer rows.Close()

	out := make([]OpenPayable, 0)
	for rows.Next() {
		var p OpenPayable
		if err := rows.Scan(
			&p.ID, &p.POID, &p.PORef, &p.BranchID, &p.Currency,
			&p.FreightCadCents, &p.DutyCadCents, &p.HandlingCadCents,
			&p.TotalCadCents, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("landedcost: scan open payable: %w", err)
		}
		p.ChargeKind, p.PayeeLabel = classifyLandedPayable(p.PORef, p.FreightCadCents, p.DutyCadCents, p.HandlingCadCents)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("landedcost: open payables rows: %w", err)
	}
	return out, nil
}

func classifyLandedPayable(poRef string, freightCad, dutyCad, handlingCad int64) (string, string) {
	switch {
	case dutyCad > 0 && freightCad == 0 && handlingCad == 0:
		return "DUTY", fmt.Sprintf("Customs Broker (%s)", poRef)
	case dutyCad > 0 && freightCad > 0:
		return "FREIGHT_AND_DUTY", fmt.Sprintf("Carrier / Broker (%s)", poRef)
	default:
		return "FREIGHT", fmt.Sprintf("Freight Carrier (%s)", poRef)
	}
}
