package tasks

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// yard_source_test.go, guards on the PickSource SEAM's fixture implementation. These matter more
// than they look: StubPickSource is what the yard board is fed by until a real dispatch feed is wired
// at the union, so its honesty (everything it emits is labelled fixture) and its determinism (a
// screenshot taken twice shows the same lines) are contract, not convenience.

func TestStubPickSourceIsDeterministic(t *testing.T) {
	src := NewStubPickSource()
	ctx := context.Background()
	a, err := src.PickTicket(ctx, knownOrder)
	if err != nil {
		t.Fatalf("ticket: %v", err)
	}
	b, err := src.PickTicket(ctx, knownOrder)
	if err != nil {
		t.Fatalf("ticket: %v", err)
	}
	if len(a.Lines) == 0 {
		t.Fatal("derived ticket has no lines")
	}
	if len(a.Lines) != len(b.Lines) {
		t.Fatalf("derived line count is not stable: %d vs %d", len(a.Lines), len(b.Lines))
	}
	for i := range a.Lines {
		if a.Lines[i] != b.Lines[i] {
			t.Fatalf("derived line %d differs between calls: %+v vs %+v", i, a.Lines[i], b.Lines[i])
		}
	}
}

func TestStubPickSourceLabelsEverythingItInvents(t *testing.T) {
	src := NewStubPickSource()
	tk, err := src.PickTicket(context.Background(), knownOrder)
	if err != nil {
		t.Fatalf("ticket: %v", err)
	}
	if tk.Source != "STUB" {
		t.Fatalf("ticket source = %q, want STUB", tk.Source)
	}
	if !strings.HasPrefix(tk.Reference, "FIXTURE-") {
		t.Fatalf("derived reference = %q, want a FIXTURE- prefix", tk.Reference)
	}
	for _, l := range tk.Lines {
		if !strings.HasPrefix(l.SKU, "FIXTURE-") {
			t.Fatalf("derived line SKU = %q, fixture material must be unmistakable on screen", l.SKU)
		}
	}
}

func TestSeededTicketCannotLaunderItsOwnSource(t *testing.T) {
	src := NewStubPickSource().Seed(PickTicket{OrderID: knownOrder, Source: "DISPATCH"})
	tk, err := src.PickTicket(context.Background(), knownOrder)
	if err != nil {
		t.Fatalf("ticket: %v", err)
	}
	if tk.Source != "STUB" {
		t.Fatalf("seeded source = %q, want the stub to force STUB", tk.Source)
	}
}

func TestStrictStubReportsUnknownOrder(t *testing.T) {
	src := NewStrictStubPickSource()
	if _, err := src.PickTicket(context.Background(), knownOrder); !errors.Is(err, ErrUnknownOrder) {
		t.Fatalf("err = %v, want ErrUnknownOrder", err)
	}
}

func TestPendingTicketsNeverReturnsDerivedOrders(t *testing.T) {
	// A queue feed that invented orders would put work on the board for orders nobody placed.
	src := NewStubPickSource() // derive mode ON
	got, _, err := src.PendingTickets(context.Background(), "", 0, 0)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("derive-mode stub returned %d pending tickets from nothing", len(got))
	}
}

func TestPendingTicketsFiltersByBranchAndKeepsSeedOrder(t *testing.T) {
	other := "55555555-5555-4555-8555-555555555555"
	src := NewStubPickSource().
		Seed(PickTicket{OrderID: "o-1", BranchID: branchA}).
		Seed(PickTicket{OrderID: "o-2", BranchID: other}).
		Seed(PickTicket{OrderID: "o-3", BranchID: branchA})

	got, _, err := src.PendingTickets(context.Background(), branchA, 0, 0)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(got) != 2 || got[0].OrderID != "o-1" || got[1].OrderID != "o-3" {
		t.Fatalf("branch filter/order wrong: %+v", got)
	}
}

func TestCounterSlugForYardSlug(t *testing.T) {
	cases := map[string]string{
		"yard-trenton":  "sales-trenton",
		"yard-kingston": "sales-kingston",
		"office":        "", // not a yard slug, the caller must fall back, not invent a channel
		"yard-":         "",
		"":              "",
	}
	for in, want := range cases {
		if got := CounterSlugForYardSlug(in); got != want {
			t.Fatalf("CounterSlugForYardSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestStubPendingTickets_PagesTheSameWayTheRealFeedDoes pins the fixture against the widened seam.
//
// A STUB THAT PAGES DIFFERENTLY FROM THE SHIPPED SOURCE IS WORSE THAN NO STUB. The seam now promises
// a WINDOW and a TOTAL, and a caller developed against a fixture that ignored the window would break
// the first time it met the dispatch feed. So the three properties the real source has are asserted
// here too: the total counts the whole population and not the page; an offset moves the window; and
// an offset past the end is an EMPTY page under an honest total rather than a refusal.
func TestStubPendingTickets_PagesTheSameWayTheRealFeedDoes(t *testing.T) {
	src := NewStubPickSource().
		Seed(PickTicket{OrderID: "o-1", BranchID: branchA}).
		Seed(PickTicket{OrderID: "o-2", BranchID: branchA}).
		Seed(PickTicket{OrderID: "o-3", BranchID: branchA})

	page, total, err := src.PendingTickets(context.Background(), branchA, 2, 0)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if total != 3 {
		t.Fatalf("the total must count the queue, not the page: got %d, want 3", total)
	}
	if len(page) != 2 || page[0].OrderID != "o-1" || page[1].OrderID != "o-2" {
		t.Fatalf("first page wrong: %+v", page)
	}

	page, total, err = src.PendingTickets(context.Background(), branchA, 2, 2)
	if err != nil {
		t.Fatalf("pending page 2: %v", err)
	}
	if total != 3 || len(page) != 1 || page[0].OrderID != "o-3" {
		t.Fatalf("second page wrong: total=%d page=%+v", total, page)
	}

	// PAST THE END IS EMPTY, NOT AN ERROR. Clicking "next" once too often is a thing people do, and
	// it deserves an empty list under a total that explains itself.
	page, total, err = src.PendingTickets(context.Background(), branchA, 2, 99)
	if err != nil {
		t.Fatalf("pending past the end must not be an error: %v", err)
	}
	if len(page) != 0 || total != 3 {
		t.Fatalf("past the end must be an empty page under an honest total: total=%d page=%+v", total, page)
	}
}
