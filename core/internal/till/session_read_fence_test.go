package till

// session_read_fence_test.go: the drawer reads by session id answer only for a branch the caller reaches. A
// scoped caller who holds another branch's session id is refused with the 403 words every till write gives,
// before any tender, movement or expected figure is read; the caller's own branch reads as before. The refusal
// is NOT FOUND, what a missing id answers: a 403 naming the branch would be an existence oracle on random ids.
// The policy read (a branch's variance threshold, close grain and blind close setting) holds the same wall with
// the till's 403 words, since it names a branch rather than a row.

import (
	"context"
	"strings"
	"testing"
)

func TestSessionReads_BranchFence(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	own := store.seedSession(TillSession{BranchID: trenton, Station: "MAIN", Status: StatusOpen})
	other := store.seedSession(TillSession{BranchID: kingston, Station: "MAIN", Status: StatusOpen})
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))

	_, err := svc.GetSession(ctx, other)
	wantStatus(t, err, 404)
	_, err = svc.ZReportFor(ctx, other)
	wantStatus(t, err, 404)
	_, err = svc.GetSession(ctx, "no-such-session")
	wantStatus(t, err, 404)
	if err != nil && strings.Contains(err.Error(), kingston) {
		t.Fatalf("the refusal names the branch: %v", err)
	}

	if _, err := svc.GetSession(ctx, own); err != nil {
		t.Fatalf("the caller's own drawer reads: %v", err)
	}
	if _, err := svc.ZReportFor(ctx, own); err != nil {
		t.Fatalf("the caller's own z report reads: %v", err)
	}
}

func TestPolicyRead_BranchFence(t *testing.T) {
	svc := newTestSvc(newFakeStore(), nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))

	_, err := svc.Policy(ctx, kingston)
	wantStatus(t, err, 403)
	if _, err := svc.Policy(ctx, trenton); err != nil {
		t.Fatalf("the caller's own branch policy reads: %v", err)
	}
	if _, err := svc.Policy(ctx, ""); err != nil {
		t.Fatalf("a read naming no branch answers the default: %v", err)
	}
}
