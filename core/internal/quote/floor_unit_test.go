package quote

// floor_unit_test.go, the DB-free floor doubles the unit fixtures use: an
// arbiter that answers fixed figures and a rail that records filings, so a
// unit assertion about the ladder is never about the pricing fixtures'
// rates of the day (the integration harness carries its own copy).
import (
	"context"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/jackc/pgx/v5"
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

func (a *unitFloorArbiter) RecordFloorDecisionTx(_ context.Context, _ pgx.Tx, rec pricing.FloorRecord) error {
	a.recorded = append(a.recorded, rec)
	return nil
}

// A store that cannot answer the floor question must refuse the reprice
// rather than skip it (fail closed): the floor state read is how the write
// knows the line carries a discount at all, so without it a discounted line
// would reprice with no floor question asked. The production Repository
// carries the read; this is the degraded-store posture.
func TestEditLineQty_FailsClosedWithoutFloorStore(t *testing.T) {
	store := newFakeStaffStore("DRAFT")
	store.ownerID = qUser
	// The wrapper hides everything the staffStore interface does not
	// carry, LineFloorState included, the shape of a store that predates
	// the floor surface.
	hidden := struct{ staffStore }{store}
	svc := NewStaffService(hidden).
		WithAudit(&recordingTxRecorder{}).
		WithPricing(qPricer{}).
		WithCostBasis(qInv{}).
		WithCustomerType(qCustType{}).
		WithRoleConfig(qTestRoleConfig()).
		WithFloorArbiter(&unitFloorArbiter{}).
		withCaller(qCallerFrom).
		withNow(qNow)

	_, err := svc.EditLineQty(qCtxAs(qStaff()), qOrg, 1, "5")
	if !errors.Is(err, ErrEditNotConfigured) {
		t.Fatalf("a reprice with no floor state read must refuse fail closed, got %v", err)
	}
	if store.writes != 0 {
		t.Fatal("the refused reprice must not reach the store")
	}
}
