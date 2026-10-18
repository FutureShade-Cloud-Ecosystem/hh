//go:build integration

package dispatch

import (
	"context"
	"testing"
	"time"
)

func TestFieldReadEnrichment_StopGateCodeAndRunDepartsAtRoundTrip(t *testing.T) {
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	date := tomorrow()
	departs := time.Now().UTC().Truncate(time.Second).Add(8*time.Hour + 15*time.Minute)

	order1 := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	order2 := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	order3 := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")

	stop1, err := h.svc.Schedule(ctx, ScheduleInput{
		OrderID:       order1,
		ScheduledDate: date,
		AddressLine:   "101 Harbour Rd",
		City:          "Trenton",
		PostalCode:    "K8V 1A1",
		ContactName:   "First Drop",
		GateCode:      "#4420",
	})
	if err != nil {
		t.Fatalf("schedule stop 1: %v", err)
	}
	if stop1.GateCode != "#4420" {
		t.Fatalf("stop1.GateCode = %q, want #4420", stop1.GateCode)
	}

	readStop, err := h.svc.StopDetail(ctx, stop1.ID)
	if err != nil {
		t.Fatalf("StopDetail: %v", err)
	}
	if readStop.GateCode != "#4420" {
		t.Fatalf("StopDetail GateCode = %q, want #4420", readStop.GateCode)
	}

	stop2, err := h.svc.Schedule(ctx, ScheduleInput{
		OrderID:       order2,
		ScheduledDate: date,
		AddressLine:   "202 Bay St",
		City:          "Trenton",
		PostalCode:    "K8V 2B2",
		ContactName:   "Second Drop",
		GateCode:      "#8812",
	})
	if err != nil {
		t.Fatalf("schedule stop 2: %v", err)
	}

	stop3, err := h.svc.Schedule(ctx, ScheduleInput{
		OrderID:       order3,
		ScheduledDate: date,
		AddressLine:   "303 Ridge Rd",
		City:          "Trenton",
		PostalCode:    "K8V 3C3",
		ContactName:   "Third Drop",
	})
	if err != nil {
		t.Fatalf("schedule stop 3: %v", err)
	}

	run, err := h.svc.CreateRun(ctx, CreateRunInput{
		BranchID:      branchTRT,
		ScheduledDate: date,
		TruckID:       firstTruck(t, h.pool, branchTRT),
		DriverID:      firstDriver(t, h.pool, branchTRT),
		Notes:         "Morning loop",
		RunDepartsAt:  &departs,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = h.pool.Exec(c, `UPDATE delivery_stops SET run_id = NULL WHERE run_id = $1`, run.ID)
		_, _ = h.pool.Exec(c, `DELETE FROM print_queue WHERE ref_id = $1`, run.ID)
		_, _ = h.pool.Exec(c, `DELETE FROM delivery_runs WHERE id = $1`, run.ID)
	})
	if run.RunDepartsAt == nil || !run.RunDepartsAt.Equal(departs) {
		t.Fatalf("CreateRun RunDepartsAt = %v, want %v", run.RunDepartsAt, departs)
	}

	detail, err := h.svc.RunDetail(ctx, run.ID)
	if err != nil {
		t.Fatalf("RunDetail: %v", err)
	}
	if detail.Run.RunDepartsAt == nil || !detail.Run.RunDepartsAt.Equal(departs) {
		t.Fatalf("RunDetail RunDepartsAt = %v, want %v", detail.Run.RunDepartsAt, departs)
	}

	// Assign stops 1, 2, 3 in sequence on the run.
	if _, err := h.svc.AssignStop(ctx, stop1.ID, AssignInput{RunID: run.ID, StopNo: 1}); err != nil {
		t.Fatalf("assign stop 1: %v", err)
	}
	if _, err := h.svc.AssignStop(ctx, stop2.ID, AssignInput{RunID: run.ID, StopNo: 2}); err != nil {
		t.Fatalf("assign stop 2: %v", err)
	}
	if _, err := h.svc.AssignStop(ctx, stop3.ID, AssignInput{RunID: run.ID, StopNo: 3}); err != nil {
		t.Fatalf("assign stop 3: %v", err)
	}

	// Verify LIFO load_position on PickFeed: stop 3 of 3 is loaded 1st (load_position = 1),
	// stop 2 of 3 is loaded 2nd (load_position = 2), stop 1 of 3 is loaded 3rd (load_position = 3).
	feed := NewPickFeed(NewRepository(h.pool))
	tk3, err := feed.TicketForOrder(ctx, order3)
	if err != nil {
		t.Fatalf("TicketForOrder(order3): %v", err)
	}
	if tk3.RunNo != run.RunNo || tk3.RunDepartsAt == nil || !tk3.RunDepartsAt.Equal(departs) || tk3.LoadPosition != 1 {
		t.Fatalf("stop 3 ticket = (run_no=%q, departs=%v, load_pos=%d), want (%q, %v, 1)",
			tk3.RunNo, tk3.RunDepartsAt, tk3.LoadPosition, run.RunNo, departs)
	}

	tk2, err := feed.TicketForOrder(ctx, order2)
	if err != nil {
		t.Fatalf("TicketForOrder(order2): %v", err)
	}
	if tk2.LoadPosition != 2 {
		t.Fatalf("stop 2 load_position = %d, want 2", tk2.LoadPosition)
	}

	tk1, err := feed.TicketForOrder(ctx, order1)
	if err != nil {
		t.Fatalf("TicketForOrder(order1): %v", err)
	}
	if tk1.LoadPosition != 3 {
		t.Fatalf("stop 1 load_position = %d, want 3", tk1.LoadPosition)
	}
}
