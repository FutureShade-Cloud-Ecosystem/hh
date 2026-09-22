package settings

// openref_test.go, DIB-506 simplification pass (0174): the surface-neutral "open object"
// reference on the workspace row. Additive proofs: the save persists it, the write_key replay
// answers the STORED ref (never the input), a malformed ref is a 422 in words, and clearing it
// ('', nothing open) is an ordinary save.

import (
	"context"
	"testing"
)

func TestSaveWorkspace_OpenRefPersistsAndReplays(t *testing.T) {
	store := &fakeKitStore{}
	svc := kitSvc(store, kitFrontline())

	v, err := svc.SaveWorkspace(context.Background(), "quotes.builder", WorkspaceSaveInput{
		SplitPct: 60, Arrangement: ArrangementPrimaryLeft,
		OpenRef: "q-123", IdempotencyKey: "k-1",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if v.OpenRef != "q-123" || store.wsRow.OpenRef != "q-123" {
		t.Fatalf("open_ref must persist: view %q, stored %q", v.OpenRef, store.wsRow.OpenRef)
	}

	// The replay of the SAME key answers the STORED row's ref, never the (different) input.
	r, err := svc.SaveWorkspace(context.Background(), "quotes.builder", WorkspaceSaveInput{
		SplitPct: 60, Arrangement: ArrangementPrimaryLeft,
		OpenRef: "q-DIFFERENT", IdempotencyKey: "k-1",
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !r.Replayed || r.OpenRef != "q-123" {
		t.Fatalf("the replay must answer the STORED open_ref: replayed=%v ref=%q", r.Replayed, r.OpenRef)
	}
	if store.wsWrites != 1 {
		t.Fatalf("the replay must not write a second time; writes=%d", store.wsWrites)
	}

	// A NEW key with '' clears it, "nothing open" is an ordinary save, not a special case.
	c, err := svc.SaveWorkspace(context.Background(), "quotes.builder", WorkspaceSaveInput{
		SplitPct: 60, Arrangement: ArrangementPrimaryLeft,
		OpenRef: "", IdempotencyKey: "k-2",
	})
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if c.OpenRef != "" || store.wsRow.OpenRef != "" {
		t.Fatalf("'' must clear the stored ref: view %q, stored %q", c.OpenRef, store.wsRow.OpenRef)
	}
}

func TestSaveWorkspace_OpenRefValidated_422InWords(t *testing.T) {
	store := &fakeKitStore{}
	svc := kitSvc(store, kitFrontline())
	_, err := svc.SaveWorkspace(context.Background(), "quotes.builder", WorkspaceSaveInput{
		SplitPct: 60, Arrangement: ArrangementPrimaryLeft,
		OpenRef: "has a space", IdempotencyKey: "k-1",
	})
	want422(t, err, "open_ref")
	if store.wsWrites != 0 {
		t.Fatalf("a refused save must not write; writes=%d", store.wsWrites)
	}
	// Too long is refused too (the CHECK must never be the first refusal).
	long := make([]byte, 65)
	for i := range long {
		long[i] = 'a'
	}
	_, err = svc.SaveWorkspace(context.Background(), "quotes.builder", WorkspaceSaveInput{
		SplitPct: 60, Arrangement: ArrangementPrimaryLeft,
		OpenRef: string(long), IdempotencyKey: "k-1",
	})
	want422(t, err, "open_ref")
}

func TestResolveWorkspace_OpenRefRidesTheUserRow(t *testing.T) {
	store := &fakeKitStore{
		hasWSRow: true,
		wsRow: WorkspaceRow{
			UserID: "u-counter", SurfaceID: "quotes.builder",
			SplitPct: 58, Arrangement: ArrangementPrimaryLeft, OpenRef: "q-99", WriteKey: "k-0",
		},
	}
	svc := kitSvc(store, kitFrontline())
	v, err := svc.Workspace(context.Background(), "quotes.builder", "")
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	if v.OpenRef != "q-99" || v.Source != "user" {
		t.Fatalf("the stored open_ref must ride the user row: %+v", v)
	}
	// The dealer-default path carries no ref, nothing was open.
	store.hasWSRow = false
	d, err := svc.Workspace(context.Background(), "quotes.builder", "")
	if err != nil {
		t.Fatalf("dealer workspace: %v", err)
	}
	if d.OpenRef != "" {
		t.Fatalf("the dealer default has no open_ref; got %q", d.OpenRef)
	}
}
