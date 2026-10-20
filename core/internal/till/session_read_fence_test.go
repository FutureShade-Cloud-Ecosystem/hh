package till

// session_read_fence_test.go: the drawer reads by session id answer only for a branch the caller reaches. A
// scoped caller who holds another branch's session id is refused with the 403 words every till write gives,
// before any tender, movement or expected figure is read; the caller's own branch reads as before.

import (
	"context"
	"testing"
)

func TestSessionReads_BranchFence(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	own := store.seedSession(TillSession{BranchID: trenton, Station: "MAIN", Status: StatusOpen})
	other := store.seedSession(TillSession{BranchID: kingston, Station: "MAIN", Status: StatusOpen})
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))

	_, err := svc.GetSession(ctx, other)
	wantStatus(t, err, 403)
	_, err = svc.ZReportFor(ctx, other)
	wantStatus(t, err, 403)

	if _, err := svc.GetSession(ctx, own); err != nil {
		t.Fatalf("the caller's own drawer reads: %v", err)
	}
	if _, err := svc.ZReportFor(ctx, own); err != nil {
		t.Fatalf("the caller's own z report reads: %v", err)
	}
}
