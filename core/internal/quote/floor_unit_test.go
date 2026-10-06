package quote

// floor_unit_test.go, the DB-free floor doubles the unit fixtures use: an
// arbiter that answers fixed figures and a rail that records filings, so a
// unit assertion about the ladder is never about the pricing fixtures'
// rates of the day (the integration harness carries its own copy).
import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
)

// unitFloorArbiter answers fixed figures; the zero value answers above the
// floor (the accepted below-rule case).
type unitFloorArbiter struct {
	marginBps int64
	floorBps  int64
	recorded  []pricing.FloorRecord
}

func (a *unitFloorArbiter) EvaluateLineFloor(_ context.Context, in pricing.FloorQuestion) (pricing.FloorAnswer, error) {
	floor := a.floorBps
	if floor == 0 {
		floor = 1200
	}
	margin := a.marginBps
	if margin == 0 {
		margin = 2000
	}
	return pricing.FloorAnswer{
		MarginBps: margin, FloorBps: floor,
		BelowRule:  in.CandidateUnitCents < in.RuleUnitCents,
		BelowFloor: margin < floor,
	}, nil
}

func (a *unitFloorArbiter) RecordFloorDecision(_ context.Context, rec pricing.FloorRecord) {
	a.recorded = append(a.recorded, rec)
}
