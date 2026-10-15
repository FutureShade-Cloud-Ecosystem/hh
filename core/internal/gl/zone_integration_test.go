//go:build integration

package gl

// A journal is filed to the fiscal period of the dealer's own business day (item 3b slice 3): 05:00 UTC
// on 1 July is July in Toronto and still 30 June in Los Angeles. The zone is read on the posting
// transaction. Fiscal year 2086, outside every band the package's other integration tests use.

import (
	"context"
	"fmt"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/jackc/pgx/v5"
)

type txZone struct {
	zone houseday.Zone
	err  error
	txN  int
}

func (z *txZone) BusinessZoneTx(context.Context, pgx.Tx) (houseday.Zone, error) {
	z.txN++
	return z.zone, z.err
}

func TestIntegration_JournalPeriodFollowsTheDealersZone(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	const year = 2086
	ensurePeriods(t, pool, year)
	at := time.Date(year, time.July, 1, 5, 0, 0, 0, time.UTC)
	caller := identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}
	la, err := houseday.NewZone("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		zone  ZoneTxSource
		month int
	}{
		{"no source, the Ontario dealer", nil, 7},
		{"Los Angeles", &txZone{zone: la}, 6},
		{"Toronto after Los Angeles", &txZone{zone: houseday.DefaultZone()}, 7},
	} {
		svc := NewService(NewRepository(pool).WithZone(tc.zone)).
			withCaller(func(_ context.Context) (identity.StaffContext, bool) { return caller, true }).
			withClock(func() time.Time { return at })
		key := fmt.Sprintf("zone-%s-%d", tc.name, time.Now().UnixNano())
		if _, err := svc.PostExternal(ctx, journalTxExt(key)); err != nil {
			t.Fatalf("%s: post: %v", tc.name, err)
		}
		var y, m int
		if err := pool.QueryRow(ctx, `
			SELECT p.year, p.month FROM gl_journals j JOIN fiscal_periods p ON p.id = j.period_id
			 WHERE j.idempotency_key = $1`, key).Scan(&y, &m); err != nil {
			t.Fatalf("%s: read the period: %v", tc.name, err)
		}
		if y != year || m != tc.month {
			t.Fatalf("%s: filed to %d-%02d, want %d-%02d", tc.name, y, m, year, tc.month)
		}
	}
}

func TestIntegration_JournalRefusedWhenTheZoneCannotBeRead(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	const year = 2086
	ensurePeriods(t, pool, year)
	caller := identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}
	svc := NewService(NewRepository(pool).WithZone(&txZone{err: fmt.Errorf("settings unavailable")})).
		withCaller(func(_ context.Context) (identity.StaffContext, bool) { return caller, true }).
		withClock(func() time.Time { return time.Date(year, time.July, 1, 5, 0, 0, 0, time.UTC) })
	key := fmt.Sprintf("zone-refused-%d", time.Now().UnixNano())
	if _, err := svc.PostExternal(ctx, journalTxExt(key)); err == nil {
		t.Fatal("a journal was posted with the dealer's zone unreadable")
	}
	if n := journalTxCount(t, pool, key); n != 0 {
		t.Fatalf("a refused posting left %d journal(s)", n)
	}
}
