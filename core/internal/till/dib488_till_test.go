package till

// DIB-488 rev 5, the POS mount-time till rediscovery read (OpenSessionLookup).
//
// The terminal held tillSessionId in client memory only: a mid-shift reload (or a duplicated
// surface) showed "Open Session" and every tendered ring 409'd while the drawer WAS open. These
// tests pin the service read that fixes it: the OPEN drawer is answered with its opened_at (the
// elapsed timer resumes honestly), "no open drawer" is an ordinary found=false answer, never an
// error, and the branch fence is the same one OpenTill enforces.

import (
	"context"
	"testing"
)

func TestOpenSessionLookup_FindsTheOpenDrawer(t *testing.T) {
	store := newFakeStore()
	id := store.seedSession(TillSession{BranchID: trenton, Station: "MAIN", Status: StatusOpen, OpenedBy: "u-opener", FloatCents: 30000})
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-cashier", trenton))

	got, openedAt, found, err := svc.OpenSessionLookup(ctx, trenton, "MAIN")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !found || got != id {
		t.Fatalf("want the seeded OPEN session %s, got found=%v id=%q", id, found, got)
	}
	if openedAt.IsZero() {
		t.Fatalf("opened_at must ride the answer, the terminal's elapsed timer resumes from it")
	}
}

func TestOpenSessionLookup_NoOpenDrawerIsAnOrdinaryAnswer(t *testing.T) {
	store := newFakeStore()
	// A CLOSED session at the drawer is not an open one.
	store.seedSession(TillSession{BranchID: trenton, Station: "MAIN", Status: StatusClosed, OpenedBy: "u-x"})
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-cashier", trenton))

	id, _, found, err := svc.OpenSessionLookup(ctx, trenton, "MAIN")
	if err != nil {
		t.Fatalf("a closed till is a STATE, not a failure: %v", err)
	}
	if found || id != "" {
		t.Fatalf("want found=false with no id, got found=%v id=%q", found, id)
	}
}

func TestOpenSessionLookup_BranchFenced(t *testing.T) {
	store := newFakeStore()
	store.seedSession(TillSession{BranchID: kingston, Station: "MAIN", Status: StatusOpen, OpenedBy: "u-opener"})
	svc := newTestSvc(store, nil)
	// A Trenton-scoped cashier may not read Kingston's drawer state.
	ctx := ctxWith(context.Background(), staffAt("u-cashier", trenton))

	if _, _, _, err := svc.OpenSessionLookup(ctx, kingston, "MAIN"); err == nil {
		t.Fatalf("want the branch fence (forbidden), got nil")
	}
}

func TestOpenSessionLookup_EmptyStationDefaultsToMain(t *testing.T) {
	store := newFakeStore()
	id := store.seedSession(TillSession{BranchID: trenton, Station: "MAIN", Status: StatusOpen, OpenedBy: "u-opener"})
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-cashier", trenton))

	got, _, found, err := svc.OpenSessionLookup(ctx, trenton, "")
	if err != nil || !found || got != id {
		t.Fatalf("empty station must mean MAIN (the OpenTill default): err=%v found=%v id=%q", err, found, got)
	}
}
