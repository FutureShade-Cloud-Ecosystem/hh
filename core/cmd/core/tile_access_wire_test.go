package main

import (
	"context"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dashboard"
)

type trailDouble map[string]bool

func (t trailDouble) HoldsPaymentTrail(_ context.Context, platformUserID, _ string) (bool, error) {
	return t[platformUserID], nil
}

// saveOnlyStore is a dashboard.Store that supports the one write SaveMyLayout makes; any other method
// would panic through the nil embedded interface, which this test never reaches.
type saveOnlyStore struct{ dashboard.Store }

func (saveOnlyStore) SaveUserTiles(context.Context, string, []dashboard.LayoutRow) error { return nil }

// HH-777: the composition root binds the money tile's rule to the payment trail reader, fails closed
// unbound, and needs the caller's platform identity and org.
func TestBindTileAccess_PaymentsTodayFollowsThePaymentTrail(t *testing.T) {
	refused := func(trail billing.PaymentTrailReader, sc dashboard.Scope) bool {
		svc := dashboard.NewService(saveOnlyStore{}, nil)
		bindTileAccess(svc, trail)
		_, err := svc.SaveMyLayout(context.Background(), sc, []dashboard.LayoutRow{{TileID: dashboard.TilePaymentsToday, Visible: true}})
		return errors.Is(err, dashboard.ErrTileForbidden)
	}
	holder := dashboard.Scope{UserID: "u1", PlatformUserID: "p1", OrgID: "o1"}
	if refused(trailDouble{"p1": true}, holder) {
		t.Fatal("a trail holder must pass the rule")
	}
	if !refused(trailDouble{}, holder) {
		t.Fatal("a staffer without the trail must be refused")
	}
	if !refused(trailDouble{"p1": true}, dashboard.Scope{UserID: "u1"}) {
		t.Fatal("a scope with no platform identity or org must be refused")
	}
	if !refused(&billing.LatePaymentTrail{}, holder) {
		t.Fatal("an unbound late reader answers no, so the tile stays closed")
	}
}
