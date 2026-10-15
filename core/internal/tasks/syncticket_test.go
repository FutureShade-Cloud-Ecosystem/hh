package tasks

import (
	"context"
	"errors"
	"testing"
)

type failingPickSource struct{}

func (failingPickSource) PickTicket(context.Context, string) (PickTicket, error) {
	return PickTicket{}, errors.New("source down")
}
func (failingPickSource) PendingTickets(context.Context, string, int, int) ([]PickTicket, int, error) {
	return nil, 0, nil
}

func TestAttachTicketData_BuildsTheRESTTicketWithoutTheFigure(t *testing.T) {
	src := NewStubPickSource().Seed(PickTicket{
		OrderID: "o1", Source: "DISPATCH", HeldFromPicking: true, HoldWords: "Balance due", StillOwedCents: 500,
		Lines: []PickLine{{LineNo: 1, Description: "Paver", QtyOrdered: "3", UOM: "EA"}},
	})
	s := NewSyncStore(nil, nil, nil).WithPickSource(src)
	data := map[string]any{"id": "t1"}
	s.attachTicketData(context.Background(), "o1", data)
	tk, ok := data["ticket"].(map[string]any)
	if !ok {
		t.Fatalf("no ticket: %v", data)
	}
	if lines, _ := tk["lines"].([]any); len(lines) != 1 {
		t.Fatalf("lines = %v", tk["lines"])
	}
	if _, has := tk["still_owed_cents"]; has {
		t.Fatalf("still owed leaked: %v", tk)
	}
	if tk["hold_words"] != "Balance due" || tk["source"] != "STUB" {
		t.Fatalf("ticket words lost: %v", tk)
	}
}

func TestAttachTicketData_DegradesWithoutASourceOrAnOrder(t *testing.T) {
	for name, s := range map[string]*SyncStore{
		"unwired": NewSyncStore(nil, nil, nil),
		"failing": NewSyncStore(nil, nil, nil).WithPickSource(failingPickSource{}),
	} {
		data := map[string]any{}
		s.attachTicketData(context.Background(), "o1", data)
		if _, has := data["ticket"]; has {
			t.Fatalf("%s: a ticket appeared", name)
		}
	}
	s := NewSyncStore(nil, nil, nil).WithPickSource(NewStubPickSource())
	data := map[string]any{}
	s.attachTicketData(context.Background(), "", data)
	if _, has := data["ticket"]; has {
		t.Fatal("no order id must attach nothing")
	}
}
