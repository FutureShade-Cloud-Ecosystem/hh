package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dispatch"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tasks"
)

// The pick ticket carries what the order line was ordered for, so a yard that finds the rest of a
// short line sends the true number and not the reduced one the line now shows.
func TestOrderedWithBackorder(t *testing.T) {
	for _, c := range []struct{ qty, back, wantOrdered, wantBack string }{
		{"3.0000", "2.0000", "5.0000", "2.0000"},
		{"0.3333", "6.6667", "7.0000", "6.6667"},
		{"5.0000", "0.0000", "5.0000", ""},
		{"5", "", "5", ""},
	} {
		o, b := orderedWithBackorder(c.qty, c.back)
		if o != c.wantOrdered || b != c.wantBack {
			t.Errorf("(%q + %q) = %q, %q; want %q, %q", c.qty, c.back, o, b, c.wantOrdered, c.wantBack)
		}
	}
}

func TestBackorderClearedTextNamesTheLineWhatWasClearedAndTheNewQuantity(t *testing.T) {
	got := backorderClearedText(order.BackorderClearedNotice{
		Order: order.YardOrderFacts{OrderNo: "#SO-1"}, LineNo: 2, Description: "Armour stone",
		UOM: "EA", ClearedQty: "2.0000", NewQty: "10.0000"})
	for _, want := range []string{"#SO-1", "line 2", "Armour stone", "10.0000 EA", "2.0000 EA", "cleared", "new order quantity"} {
		if !strings.Contains(got, want) {
			t.Errorf("the notice must say %q: %s", want, got)
		}
	}
}

// A short pick the order cannot take (a shipped plan, a line already out) reaches the yard as a 409
// refusal, never a 500.
func TestYardOrderRefusalMapsToTheYardsRefusals(t *testing.T) {
	if err := yardOrderRefusal(fmt.Errorf("%w: line 1 has 2.0000 on groups already out", order.ErrLineInUse)); !errors.Is(err, tasks.ErrOrderRefused) {
		t.Fatalf("a line already out is a refusal: %v", err)
	}
	if err := yardOrderRefusal(fmt.Errorf("line 1: %w", order.ErrAboveOrdered)); !errors.Is(err, tasks.ErrAboveOrdered) {
		t.Fatalf("above ordered is its own refusal: %v", err)
	}
}

func TestPickTicketFromFeed_CarriesTruckRunAndLoadPosition(t *testing.T) {
	departs := time.Date(2026, 8, 6, 12, 15, 0, 0, time.UTC)
	got := pickTicketFromFeed(dispatch.PickFeedTicket{
		OrderID:      "o-1",
		OrderNo:      "#SO-TRT-1041",
		BranchID:     "b-1",
		CustomerName: "Bostock Landscaping",
		Fulfillment:  "DELIVERY",
		Priority:     "HIGH",
		Reference:    "s-1",
		RunNo:        "#DEMO-RUN-PLM-R5",
		RunDepartsAt: &departs,
		LoadPosition: 1,
		Lines:        []dispatch.PickFeedLine{{LineNo: 1, Description: "Holland Paver", QtyOrdered: "480", UOM: "PC"}},
	})
	if got.RunNo != "#DEMO-RUN-PLM-R5" {
		t.Fatalf("RunNo = %q, want #DEMO-RUN-PLM-R5", got.RunNo)
	}
	if got.RunDepartsAt == nil || !got.RunDepartsAt.Equal(departs) {
		t.Fatalf("RunDepartsAt = %v, want %v", got.RunDepartsAt, departs)
	}
	if got.LoadPosition != 1 {
		t.Fatalf("LoadPosition = %d, want 1", got.LoadPosition)
	}
}
