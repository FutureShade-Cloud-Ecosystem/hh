package landedcost

import (
	"context"
	"fmt"
	"time"
)

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
