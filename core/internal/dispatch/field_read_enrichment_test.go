package dispatch

import (
	"testing"
	"time"
)

func TestPickFeedTicket_EnrichesRunDepartsAtAndLoadPosition(t *testing.T) {
	f := NewPickFeed(NewRepository(nil))
	departs := time.Unix(1786004100, 0).UTC()

	tk := f.ticket(pickFeedStop{
		StopID:        "11111111-1111-4111-8111-111111111111",
		OrderID:       "o-1",
		ScheduledDate: "2099-01-01",
		RunID:         "22222222-2222-4222-8222-222222222222",
		RunNo:         "R5",
		RunStatus:     RunPlanned,
		RunDepartsAt:  &departs,
		LoadPosition:  1,
	})
	if tk.RunNo != "R5" {
		t.Fatalf("run_no = %q, want R5", tk.RunNo)
	}
	if tk.RunDepartsAt == nil || !tk.RunDepartsAt.Equal(departs) {
		t.Fatalf("run_departs_at = %v, want %v", tk.RunDepartsAt, departs)
	}
	if tk.LoadPosition != 1 {
		t.Fatalf("load_position = %d, want 1", tk.LoadPosition)
	}

	// An unassigned pickup ticket must leave run_no, run_departs_at, and load_position empty.
	pickup := f.pickupTicket(pickFeedPickup{
		OrderID:      "o-2",
		OrderNo:      "#SO-2",
		Fulfillment:  "WILL_CALL",
		PromisedDate: "2099-01-01",
	})
	if pickup.RunNo != "" || pickup.RunDepartsAt != nil || pickup.LoadPosition != 0 {
		t.Fatalf("unassigned pickup carried truck facts: run_no=%q departs=%v load_pos=%d",
			pickup.RunNo, pickup.RunDepartsAt, pickup.LoadPosition)
	}
}
