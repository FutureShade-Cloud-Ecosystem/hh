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
// within the requested branch reach. A payable counts from its posting journal's
// effective instant (the GL journal clock, S2's P3 1): an applied payable from its
// apply journal's, a retro adjusted one from its own journal's, and a voided one on
// the window between its apply journal's and its void journal's effective
// instants, the same late filing rule the GL's account reads keep. With the clock
// unwired the row's own created_at and updated_at keep the inclusion rule the seam
// replaced.
func (s *Service) OpenPayablesAsOf(ctx context.Context, asOf time.Time, branchIDs []string, global bool) ([]OpenPayable, error) {
	if s == nil || s.repo == nil {
		return nil, nil
	}
	rows, err := s.repo.OpenPayableRows(ctx, branchIDs, global)
	if err != nil {
		return nil, err
	}
	if asOf.IsZero() {
		asOf = time.Now().UTC()
	}

	var instants map[string]time.Time
	if s.journalClock != nil {
		ids := make([]string, 0, len(rows))
		for _, r := range rows {
			if r.glJournalID != "" {
				ids = append(ids, r.glJournalID)
			}
		}
		if instants, err = s.journalClock.JournalEffectiveInstants(ctx, ids); err != nil {
			return nil, err
		}
	}

	out := make([]OpenPayable, 0)
	for _, r := range rows {
		if !r.countsAsOf(ctx, asOf, s.journalClock, instants) {
			continue
		}
		p := OpenPayable{
			ID: r.id, POID: r.poID, PORef: r.poRef, BranchID: r.branchID, Currency: r.currency,
			FreightCadCents: r.freight, DutyCadCents: r.duty, HandlingCadCents: r.handling,
			TotalCadCents: r.freight + r.duty + r.handling,
			CreatedAt:     r.createdAt, UpdatedAt: r.updatedAt,
		}
		p.ChargeKind, p.PayeeLabel = classifyLandedPayable(p.PORef, p.FreightCadCents, p.DutyCadCents, p.HandlingCadCents)
		out = append(out, p)
	}
	return out, nil
}

// openPayableRow is one landed_costs row that owes, or owed, a payable: the raw
// material the as-of rule reads. The as-of decision is the caller's.
type openPayableRow struct {
	id          string
	poID        string
	poRef       string
	branchID    string
	currency    string
	freight     int64
	duty        int64
	handling    int64
	status      string
	glJournalID string
	createdAt   time.Time
	updatedAt   time.Time
}

func (r openPayableRow) total() int64 { return r.freight + r.duty + r.handling }

// countsAsOf decides whether the row's payable counts as of asOf, and reports the
// instant it counts from. With the clock wired the posting journals' effective
// instants decide: POSTED and RETRO_ADJUSTED from their own journal (the row's
// gl_journal_id is the current posting), VOIDED on the window between its apply
// journal's and its void journal's effective instants, the apply journal reached by
// its key because the void overwrote the row's pointer. With the clock unwired the
// row's own timestamps keep the rule the seam replaced.
func (r openPayableRow) countsAsOf(ctx context.Context, asOf time.Time, clock JournalClock, instants map[string]time.Time) bool {
	switch r.status {
	case "POSTED", "RETRO_ADJUSTED":
		if clock == nil {
			if r.status == "POSTED" {
				return !r.updatedAt.After(asOf)
			}
			return !r.createdAt.After(asOf)
		}
		eff, ok := time.Time{}, false
		if r.glJournalID != "" {
			eff, ok = instants[r.glJournalID]
		}
		if !ok {
			// no posting journal on the row (the GL was unwired at apply time): the
			// row's own timestamps decide, the legacy rule
			if r.status == "POSTED" {
				return !r.updatedAt.After(asOf)
			}
			return !r.createdAt.After(asOf)
		}
		return !eff.After(asOf)
	case "VOIDED":
		if r.glJournalID == "" {
			return false
		}
		voidEff, voidOK := time.Time{}, false
		if clock != nil {
			voidEff, voidOK = instants[r.glJournalID]
		}
		voidOpen := func() bool {
			if !voidOK {
				return r.updatedAt.After(asOf)
			}
			return voidEff.After(asOf)
		}
		if clock != nil {
			applyEff, found, err := clock.JournalEffectiveInstantByKey(ctx, "landedcost.apply:"+r.id)
			if err == nil && found {
				return !applyEff.After(asOf) && voidOpen()
			}
			// no apply journal under the key: the row's own timestamps decide
		}
		return !r.createdAt.After(asOf) && voidOpen()
	}
	return false
}

// OpenPayableRows reads every landed_costs row in reach that owes, or owed, a
// payable, with no as-of predicate of its own: which rows count as of an instant is
// the posting journals' effective instants, the service's journal clock step.
func (r *Repository) OpenPayableRows(ctx context.Context, branchIDs []string, global bool) ([]openPayableRow, error) {
	if r == nil || r.pool == nil {
		return nil, errNoDB
	}
	if !global && len(branchIDs) == 0 {
		return []openPayableRow{}, nil
	}
	if branchIDs == nil {
		branchIDs = []string{}
	}

	rows, err := r.pool.Query(ctx, `
		SELECT lc.id::text, lc.po_id::text, lc.po_ref, lc.branch_id::text, trim(lc.currency),
		       lc.freight_cad_cents, lc.duty_cad_cents, lc.handling_cad_cents,
		       lc.status, COALESCE(lc.gl_journal_id::text, ''), lc.created_at, lc.updated_at
		  FROM landed_costs lc
		 WHERE lc.status IN ('POSTED', 'RETRO_ADJUSTED', 'VOIDED')
		   AND (lc.freight_cad_cents + lc.duty_cad_cents + lc.handling_cad_cents) > 0
		   AND ($1::boolean OR lc.branch_id = ANY($2::uuid[]))
		 ORDER BY lc.created_at ASC, lc.id ASC`,
		global, branchIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("landedcost: open payable rows: %w", err)
	}
	defer rows.Close()

	out := make([]openPayableRow, 0)
	for rows.Next() {
		var r openPayableRow
		if err := rows.Scan(
			&r.id, &r.poID, &r.poRef, &r.branchID, &r.currency,
			&r.freight, &r.duty, &r.handling,
			&r.status, &r.glJournalID, &r.createdAt, &r.updatedAt,
		); err != nil {
			return nil, fmt.Errorf("landedcost: scan open payable row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("landedcost: open payable rows: %w", err)
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
