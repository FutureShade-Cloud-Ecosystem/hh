//go:build integration

// dib472_transfer_eta_integration_test.go, DIB-472's PREMISE, against real Postgres.
//
// THE PREMISE THIS PINS. The whole ruling rests on one claim about the ship flow:
//
//	a route WITH a configured transit time defaults the eta to ship date + those days
//	a route WITHOUT one stores NULL, nothing is invented, ever
//	an EDITED date is stored exactly as edited, and a CLEARED one stores NULL
//
// Those are database facts (a column value after a transaction), so they cannot be asserted against a
// fake, and they are the facts a plausible refactor breaks silently: a default computed from the wrong
// date, a "sensible" fallback constant added behind the missing row, a clear that re-defaults.
//
// It also pins the read half, inventory.InTransitLine carries the stored eta, and carries '' when
// there is none, which is what keeps DIB-460's fallback sentence honest on the incoming panel.
//
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5432/fix472?sslmode=disable' \
//	  go test -tags=integration ./internal/inventory/ -run DIB472 -count=1

package inventory

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// setTransit forces one route's configured transit days for the duration of a test, restoring whatever
// was there afterwards. The seeded Trenton <-> Kingston rows are shipped configuration (0158) and other
// tests read them, so a test that changes one must put it back.
func setTransit(t *testing.T, pool *pgxpool.Pool, from, to string, days int) {
	t.Helper()
	ctx := context.Background()
	var prev *int
	_ = pool.QueryRow(ctx, `SELECT transit_days FROM branch_transit_days WHERE from_branch_id=$1 AND to_branch_id=$2`, from, to).Scan(&prev)
	if _, err := pool.Exec(ctx, `
		INSERT INTO branch_transit_days (from_branch_id, to_branch_id, transit_days)
		VALUES ($1, $2, $3)
		ON CONFLICT (from_branch_id, to_branch_id) DO UPDATE SET transit_days = EXCLUDED.transit_days, updated_at = now()`,
		from, to, days); err != nil {
		t.Fatalf("set transit days: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		if prev == nil {
			_, _ = pool.Exec(c, `DELETE FROM branch_transit_days WHERE from_branch_id=$1 AND to_branch_id=$2`, from, to)
			return
		}
		_, _ = pool.Exec(c, `
			INSERT INTO branch_transit_days (from_branch_id, to_branch_id, transit_days) VALUES ($1,$2,$3)
			ON CONFLICT (from_branch_id, to_branch_id) DO UPDATE SET transit_days = EXCLUDED.transit_days`,
			from, to, *prev)
	})
}

// clearTransit removes a route's configuration for the duration of a test, restoring it afterwards.
func clearTransit(t *testing.T, pool *pgxpool.Pool, from, to string) {
	t.Helper()
	ctx := context.Background()
	var prev *int
	_ = pool.QueryRow(ctx, `SELECT transit_days FROM branch_transit_days WHERE from_branch_id=$1 AND to_branch_id=$2`, from, to).Scan(&prev)
	if _, err := pool.Exec(ctx, `DELETE FROM branch_transit_days WHERE from_branch_id=$1 AND to_branch_id=$2`, from, to); err != nil {
		t.Fatalf("clear transit days: %v", err)
	}
	t.Cleanup(func() {
		if prev == nil {
			return
		}
		_, _ = pool.Exec(context.Background(), `
			INSERT INTO branch_transit_days (from_branch_id, to_branch_id, transit_days) VALUES ($1,$2,$3)
			ON CONFLICT (from_branch_id, to_branch_id) DO UPDATE SET transit_days = EXCLUDED.transit_days`,
			from, to, *prev)
	})
}

// etaOf reads the stored column directly, as a nullable string, so NULL and ” are distinguishable -
// the distinction the whole ticket turns on.
func etaOf(t *testing.T, pool *pgxpool.Pool, transferID string) *string {
	t.Helper()
	var eta *string
	if err := pool.QueryRow(context.Background(),
		`SELECT to_char(eta, 'YYYY-MM-DD') FROM stock_transfers WHERE id = $1`, transferID).Scan(&eta); err != nil {
		t.Fatalf("read eta: %v", err)
	}
	return eta
}

// draftTransfer opens a DRAFT with stock behind it, ready to ship.
func draftTransfer(t *testing.T, svc *Service, pool *pgxpool.Pool, sku string, from, to string) (Transfer, string) {
	t.Helper()
	product := mkProduct(t, pool, sku)
	seedBalance(t, pool, product, from, 40, 1500)
	tr, err := svc.CreateTransfer(context.Background(), CreateTransferInput{
		FromBranchID: from, ToBranchID: to, Notes: "DIB-472 fixture",
		Lines: []CreateTransferLine{{ProductID: product, QtyBase: 5}},
	})
	if err != nil {
		t.Fatalf("create transfer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = svc.repo.pool.Exec(c, `DELETE FROM stock_transfer_lines WHERE transfer_id = $1`, tr.ID)
		_, _ = svc.repo.pool.Exec(c, `DELETE FROM stock_transfers WHERE id = $1`, tr.ID)
	})
	return tr, product
}

// TestIntegration_DIB472_CapturePremise is the premise-pinning test: one table, four ship flows, the
// stored column read back from Postgres each time.
func TestIntegration_DIB472_CapturePremise(t *testing.T) {
	pool := intPool(t)
	// A shipped transfer leaves the source down and the destination not yet up, the legitimate drift
	// window DIB-396's shared lease exists for (the rule stock_truth_integration_test.go states).
	dib396LeaseShared(t, pool, 3960001)
	svc := newIntSvc(pool)
	ctx := context.Background()

	// The expected default, computed the way the yard reads a calendar: TODAY in the house zone plus the
	// route's days. Deliberately NOT recomputed with the same helper the code under test uses, it is
	// spelled out here so a mutation of defaultETA fails this test instead of moving with it.
	loc := etaLocation()
	today := time.Now().In(loc)
	dayPlus := func(n int) string {
		return time.Date(today.Year(), today.Month(), today.Day(), 12, 0, 0, 0, loc).AddDate(0, 0, n).Format("2006-01-02")
	}

	t.Run("pair present: the eta is defaulted to ship date + transit days", func(t *testing.T) {
		setTransit(t, pool, branch2, branch1, 2)
		tr, _ := draftTransfer(t, svc, pool, "DIB472-DEFAULT", branch2, branch1)

		shipped, err := svc.ShipTransfer(ctx, tr.ID, "")
		if err != nil {
			t.Fatalf("ship: %v", err)
		}
		want := dayPlus(2)
		if shipped.ETA != want {
			t.Fatalf("returned eta %q, want %q (ship day + 2)", shipped.ETA, want)
		}
		stored := etaOf(t, pool, tr.ID)
		if stored == nil || *stored != want {
			t.Fatalf("stored eta %v, want %q, the column is the fact the surface reads", stored, want)
		}
	})

	t.Run("a same-day route promises TODAY, which is a real answer and not 'unset'", func(t *testing.T) {
		setTransit(t, pool, branch2, branch1, 0)
		tr, _ := draftTransfer(t, svc, pool, "DIB472-SAMEDAY", branch2, branch1)

		shipped, err := svc.ShipTransfer(ctx, tr.ID, "")
		if err != nil {
			t.Fatalf("ship: %v", err)
		}
		if want := dayPlus(0); shipped.ETA != want {
			t.Fatalf("same-day route eta %q, want %q", shipped.ETA, want)
		}
	})

	t.Run("pair absent: the eta is NULL, nothing is invented", func(t *testing.T) {
		clearTransit(t, pool, branch2, branch1)
		tr, _ := draftTransfer(t, svc, pool, "DIB472-NOPAIR", branch2, branch1)

		shipped, err := svc.ShipTransfer(ctx, tr.ID, "")
		if err != nil {
			t.Fatalf("ship: %v", err)
		}
		if shipped.ETA != "" {
			t.Fatalf("an unconfigured route produced eta %q, a date nothing in the database supports", shipped.ETA)
		}
		if stored := etaOf(t, pool, tr.ID); stored != nil {
			t.Fatalf("stored eta %q for an unconfigured route, want NULL", *stored)
		}
		// And the transfer still shipped: the missing configuration costs the PROMISE, never the truck.
		if shipped.Status != TransferInTransit {
			t.Fatalf("status %q, want IN_TRANSIT", shipped.Status)
		}
	})

	t.Run("edited: the date the clerk confirmed is stored verbatim", func(t *testing.T) {
		setTransit(t, pool, branch2, branch1, 1)
		tr, _ := draftTransfer(t, svc, pool, "DIB472-EDITED", branch2, branch1)

		// A date that is NOT the default, so a "the default won anyway" bug cannot pass.
		//
		// DERIVED, NOT PINNED, and that is the whole point of this line. It used to read
		// `const edited = "2026-09-04"`, chosen because it was not the default ON THE DAY IT WAS
		// WRITTEN, the route's transit is 1 day, so the default was tomorrow, and tomorrow was
		// 2026-09-03. The calendar then walked into it: at midnight Toronto on 2026-09-03 the
		// default BECAME 2026-09-04, the literal collided with it, and the guard below fired on a
		// perfectly correct result. The test failed for nobody's commit, in the evening, which is
		// the exact DIB-427 shape (a pinned calendar instant compared against a real clock, the gap
		// closing by one day per real day).
		// dayPlus(5) can never be dayPlus(1), whatever day it is.
		edited := dayPlus(5)
		shipped, err := svc.ShipTransferWithETA(ctx, tr.ID, "", ShipETA{Supplied: true, Value: edited})
		if err != nil {
			t.Fatalf("ship: %v", err)
		}
		if shipped.ETA != edited {
			t.Fatalf("returned eta %q, want the edited %q", shipped.ETA, edited)
		}
		stored := etaOf(t, pool, tr.ID)
		if stored == nil || *stored != edited {
			t.Fatalf("stored eta %v, want %q", stored, edited)
		}
		if *stored == dayPlus(1) {
			t.Fatalf("the edit was overwritten by the route default")
		}
	})

	t.Run("cleared: an empty supplied eta stores NULL and is NOT re-defaulted", func(t *testing.T) {
		setTransit(t, pool, branch2, branch1, 1)
		tr, _ := draftTransfer(t, svc, pool, "DIB472-CLEARED", branch2, branch1)

		shipped, err := svc.ShipTransferWithETA(ctx, tr.ID, "", ShipETA{Supplied: true, Value: ""})
		if err != nil {
			t.Fatalf("ship: %v", err)
		}
		if shipped.ETA != "" {
			t.Fatalf("a cleared field produced eta %q, the server re-defaulted over a deliberate blank", shipped.ETA)
		}
		if stored := etaOf(t, pool, tr.ID); stored != nil {
			t.Fatalf("stored eta %q, want NULL", *stored)
		}
	})

	t.Run("a mistyped date refuses the whole shipment, nothing moved", func(t *testing.T) {
		setTransit(t, pool, branch2, branch1, 1)
		tr, product := draftTransfer(t, svc, pool, "DIB472-BADDATE", branch2, branch1)

		before := onHandOf(t, pool, product, branch2)
		if _, err := svc.ShipTransferWithETA(ctx, tr.ID, "", ShipETA{Supplied: true, Value: "aug 20"}); err == nil {
			t.Fatal("a hand-typed date was accepted")
		}
		// The transaction rolled back: still a DRAFT, and the source branch still holds the stock. A ship
		// that half-happened because the date was wrong would be the worst possible outcome here.
		after, err := svc.GetTransfer(ctx, tr.ID)
		if err != nil {
			t.Fatalf("re-read: %v", err)
		}
		if after.Status != TransferDraft {
			t.Fatalf("status %q after a refused ship, want DRAFT", after.Status)
		}
		if got := onHandOf(t, pool, product, branch2); got != before {
			t.Fatalf("source on-hand moved from %v to %v on a refused ship", before, got)
		}
	})
}

// onHandOf reads one (product, branch) balance, used to prove a refused ship moved nothing.
func onHandOf(t *testing.T, pool *pgxpool.Pool, productID, branchID string) float64 {
	t.Helper()
	var qty float64
	if err := pool.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(on_hand_base), 0)::float8 FROM stock_balances
		 WHERE product_id = $1 AND branch_id = $2`, productID, branchID).Scan(&qty); err != nil {
		t.Fatalf("read on hand: %v", err)
	}
	return qty
}

// TestIntegration_DIB472_InTransitLineCarriesTheStoredETA is the READ half: the incoming panel's line
// gets the promise when there is one and ” when there is not, so the two sentences in
// transferSentence are both reachable from real data.
func TestIntegration_DIB472_InTransitLineCarriesTheStoredETA(t *testing.T) {
	pool := intPool(t)
	dib396LeaseShared(t, pool, 3960001)
	svc := newIntSvc(pool)
	ctx := context.Background()

	t.Run("with a promise", func(t *testing.T) {
		setTransit(t, pool, branch2, branch1, 3)
		tr, product := draftTransfer(t, svc, pool, "DIB472-LINE-ETA", branch2, branch1)
		shipped, err := svc.ShipTransfer(ctx, tr.ID, "")
		if err != nil {
			t.Fatalf("ship: %v", err)
		}
		lines, err := svc.InTransitForProduct(ctx, BranchReach{Global: true}, product)
		if err != nil {
			t.Fatalf("in-transit read: %v", err)
		}
		if len(lines) != 1 {
			t.Fatalf("got %d in-transit lines, want 1", len(lines))
		}
		if lines[0].ETA != shipped.ETA || lines[0].ETA == "" {
			t.Fatalf("line eta %q, want the transfer's %q", lines[0].ETA, shipped.ETA)
		}
		// The shipped date is STILL on the line. The fallback sentence needs it, and a promise does not
		// erase the fact that the truck left.
		if lines[0].ShippedAt == "" {
			t.Fatal("the line lost its shipped date")
		}
	})

	t.Run("without one", func(t *testing.T) {
		clearTransit(t, pool, branch2, branch1)
		tr, product := draftTransfer(t, svc, pool, "DIB472-LINE-NOETA", branch2, branch1)
		if _, err := svc.ShipTransfer(ctx, tr.ID, ""); err != nil {
			t.Fatalf("ship: %v", err)
		}
		lines, err := svc.InTransitForProduct(ctx, BranchReach{Global: true}, product)
		if err != nil {
			t.Fatalf("in-transit read: %v", err)
		}
		if len(lines) != 1 {
			t.Fatalf("got %d in-transit lines, want 1", len(lines))
		}
		if lines[0].ETA != "" {
			t.Fatalf("line eta %q for an unconfigured route, want empty, the panel would claim an arrival", lines[0].ETA)
		}
		if lines[0].ShippedAt == "" {
			t.Fatal("an eta-less line must still carry the shipped date, it is the honest sentence")
		}
	})
}

// TestIntegration_DIB472_TransitDaysConfigIsMaintainable pins the admin surface's contract: every
// ordered pair is listed (the unconfigured ones are the point), a save round-trips, a clear removes the
// promise, and the reviewed timestamp moves when somebody looks at it.
func TestIntegration_DIB472_TransitDaysConfigIsMaintainable(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	ctx := context.Background()

	pairs, err := svc.TransitDaysConfig(ctx)
	if err != nil {
		t.Fatalf("config read: %v", err)
	}
	// Two branches → two ordered routes. Both directions are separate rows, because the two legs of a
	// route are not the same trip.
	if len(pairs) < 2 {
		t.Fatalf("got %d routes, want at least 2 (both directions)", len(pairs))
	}
	find := func(from, to string) TransitDaysPair {
		t.Helper()
		for _, p := range pairs {
			if p.FromBranchID == from && p.ToBranchID == to {
				return p
			}
		}
		t.Fatalf("route %s→%s missing from the config list", from, to)
		return TransitDaysPair{}
	}
	// The 0158 seed: Trenton <-> Kingston, both directions, one day.
	for _, dir := range [][2]string{{branch1, branch2}, {branch2, branch1}} {
		p := find(dir[0], dir[1])
		if !p.Configured || p.TransitDays != 1 {
			t.Fatalf("seeded route %s→%s is configured=%v days=%d, want true/1", p.FromCode, p.ToCode, p.Configured, p.TransitDays)
		}
		if p.UpdatedAt == "" {
			t.Fatalf("route %s→%s carries no reviewed date, staleness would be invisible", p.FromCode, p.ToCode)
		}
		if p.FromCode == "" || p.ToCode == "" || p.FromName == "" {
			t.Fatalf("route %s→%s has no human labels, a table of UUIDs cannot be maintained", p.FromBranchID, p.ToBranchID)
		}
	}

	// An admin edit round-trips, and the reviewed date moves.
	setTransit(t, pool, branch1, branch2, 1)
	before := find(branch1, branch2).UpdatedAt
	time.Sleep(5 * time.Millisecond)
	saved, err := svc.SetTransitDays(ctx, SetTransitDaysInput{FromBranchID: branch1, ToBranchID: branch2, TransitDays: 4})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !saved.Configured || saved.TransitDays != 4 {
		t.Fatalf("saved as configured=%v days=%d, want true/4", saved.Configured, saved.TransitDays)
	}
	if saved.UpdatedAt == before {
		t.Fatalf("the reviewed date did not move on a save (%q), the staleness signal is dead", saved.UpdatedAt)
	}

	// A clear removes the promise, and reads back as "not set" rather than as zero days.
	cleared, err := svc.SetTransitDays(ctx, SetTransitDaysInput{FromBranchID: branch1, ToBranchID: branch2, Clear: true})
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if cleared.Configured {
		t.Fatal("a cleared route still reads as configured")
	}
	if days, ok, err := svc.repo.TransitDaysForPair(ctx, nil, branch1, branch2); err != nil || ok {
		t.Fatalf("cleared route still resolves (days=%d ok=%v err=%v), ships would keep promising", days, ok, err)
	}

	// Clearing twice is not an error: the caller asked for "this route promises nothing", and it does.
	if _, err := svc.SetTransitDays(ctx, SetTransitDaysInput{FromBranchID: branch1, ToBranchID: branch2, Clear: true}); err != nil {
		t.Fatalf("clearing an already-clear route: %v", err)
	}
}

// TestIntegration_DIB472_ETACannotExistWithoutAShip pins the 0158 CHECK, the schema's copy of "capture
// at ship time". A later change that stamped an eta on a DRAFT has to change the migration too, which
// is the point: the capture point stops being an implementation detail one refactor can move.
func TestIntegration_DIB472_ETACannotExistWithoutAShip(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO stock_transfers (transfer_no, from_branch_id, to_branch_id, eta)
		VALUES ('#TR-DIB472-CHECK-' || substr(md5(random()::text),1,8), $1, $2, CURRENT_DATE)
		RETURNING id::text`, branch1, branch2).Scan(&id)
	if err == nil {
		_, _ = pool.Exec(ctx, `DELETE FROM stock_transfers WHERE id = $1`, id)
		t.Fatal("a DRAFT transfer accepted an eta, the promise is supposed to require a ship event")
	}
}
