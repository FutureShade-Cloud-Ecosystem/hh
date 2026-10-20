//go:build integration

// syncpick_pull_fence_integration_test.go: the pick_tasks sync pull held to the caller's branch reach (UX-81,
// FROM-PRINCIPAL item 69). A branch scoped yard device pulls the picks of the branches it reaches and the picks
// that belong to no branch, never another branch's rows (which carry the claimer's id); a GLOBAL caller pulls
// all; a caller with no active authority pulls nothing; and the fence leaves the cursor exact: the pull's
// `seq > since` neither skips nor reorders the rows the caller does reach.
//
// Run with DATABASE_URL set to a throwaway migrated database; the legs skip politely without one.
package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// pullAs makes the pick store's staff authority answer a caller scoped as given, then pulls one page.
func (d *pickDoor) pullAs(sc identity.StaffContext, has bool, since int64, limit int) []string {
	d.t.Helper()
	d.authority = func() (identity.StaffContext, bool, error) { return sc, has, nil }
	items, _, err := d.store.Pull(context.Background(), d.f, since, limit)
	if err != nil {
		d.t.Fatalf("pull: %v", err)
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}

func (d *pickDoor) pullRaw(sc identity.StaffContext) string {
	d.t.Helper()
	d.authority = func() (identity.StaffContext, bool, error) { return sc, true, nil }
	items, _, err := d.store.Pull(context.Background(), d.f, 0, 100)
	if err != nil {
		d.t.Fatalf("pull: %v", err)
	}
	raw, _ := json.Marshal(items)
	return string(raw)
}

func scopedTo(user string, branches ...string) identity.StaffContext {
	b := map[string]bool{}
	for _, id := range branches {
		b[id] = true
	}
	return identity.StaffContext{UserID: user, RoleCode: "YARD_TEAM", Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
		Status: identity.StatusActive, Branches: b}
}

// branchlessTask is a pick with no branch, the shape a row older than the branch copy would have: a sync create
// refuses one now (UX-81 round 2), so the fixture is born on a branch and the branch is taken off in SQL.
func (d *pickDoor) branchlessTask() string {
	d.t.Helper()
	id := d.task(d.branchA)
	if _, err := d.pool.Exec(context.Background(), `UPDATE tasks SET branch_id = NULL WHERE id = $1::uuid`, id); err != nil {
		d.t.Fatalf("take the branch off: %v", err)
	}
	return id
}

func contains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func TestPickPullFence_ABranchScopedDeviceDoesNotPullAnotherBranchsPicks(t *testing.T) {
	d := newPickDoor(t)
	pickA, pickB, pickNone := d.task(d.branchA), d.task(d.branchB), d.branchlessTask()
	// A claims branch A's pick, so its row carries the claimer's id.
	d.authority = func() (identity.StaffContext, bool, error) { return scopedTo(d.user, d.branchA), true, nil }
	if res := d.push(apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "update", Payload: map[string]any{"id": pickA, "status": "ACCEPTED"}}); res[0].Status != "applied" {
		t.Fatalf("A claims: %+v %v", res[0], res[0].Error)
	}
	claimer := d.user

	b := scopedTo(uuid.NewString(), d.branchB)
	ids := d.pullAs(b, true, 0, 100)
	t.Logf("branch B device pulls %d rows: A's pick present %v, B's pick present %v, branchless pick present %v", len(ids),
		contains(ids, pickA), contains(ids, pickB), contains(ids, pickNone))
	if contains(ids, pickA) {
		t.Errorf("a branch B device must not pull branch A's pick")
	}
	if !contains(ids, pickB) {
		t.Errorf("a branch B device pulls its own branch's pick: %v", ids)
	}
	if contains(ids, pickNone) {
		t.Errorf("a branch scoped device does not pull a pick with no branch (UX-81 round 2): %v", ids)
	}
	if raw := d.pullRaw(b); strings.Contains(raw, claimer) || strings.Contains(raw, pickA) {
		t.Errorf("another branch's claimer id and pick id must not ride the pull")
	}

	a := scopedTo(claimer, d.branchA)
	ids = d.pullAs(a, true, 0, 100)
	if !contains(ids, pickA) || contains(ids, pickB) || contains(ids, pickNone) {
		t.Errorf("a branch A device pulls A's pick, not B's and not the branchless pick: %v", ids)
	}

	global := scopedTo(uuid.NewString())
	global.Scope, global.Tier = identity.ScopeGlobal, identity.TierLeadership
	ids = d.pullAs(global, true, 0, 100)
	if !contains(ids, pickA) || !contains(ids, pickB) || !contains(ids, pickNone) {
		t.Errorf("a GLOBAL caller still pulls every branch: %v", ids)
	}

	// A scoped caller who holds no branch grant pulls nothing.
	none := scopedTo(uuid.NewString())
	ids = d.pullAs(none, true, 0, 100)
	if len(ids) != 0 {
		t.Errorf("a scoped caller with no grant pulls nothing: %v", ids)
	}
}

func TestPickPullFence_NoActiveAuthorityPullsNothing(t *testing.T) {
	d := newPickDoor(t)
	d.task(d.branchA)
	d.branchlessTask()
	if ids := d.pullAs(identity.StaffContext{}, false, 0, 100); len(ids) != 0 {
		t.Errorf("an absent authority pulls nothing: %v", ids)
	}
	inactive := scopedTo(uuid.NewString(), d.branchA)
	inactive.Status = "SUSPENDED"
	if ids := d.pullAs(inactive, true, 0, 100); len(ids) != 0 {
		t.Errorf("an inactive authority pulls nothing: %v", ids)
	}
}

// TestPickPullFence_TheCursorStaysExact: picks of two branches interleave in seq order; a branch B device paging
// two rows at a time sees exactly its own rows, in seq order, none skipped, none repeated.
func TestPickPullFence_TheCursorStaysExact(t *testing.T) {
	d := newPickDoor(t)
	var wantB []string
	for i := 0; i < 4; i++ {
		d.task(d.branchA)
		wantB = append(wantB, d.task(d.branchB))
	}
	d.task(d.branchA)
	b := scopedTo(uuid.NewString(), d.branchB)
	d.authority = func() (identity.StaffContext, bool, error) { return b, true, nil }

	var got []string
	var since int64
	pages := 0
	for {
		items, _, err := d.store.Pull(context.Background(), d.f, since, 2)
		if err != nil {
			t.Fatalf("pull: %v", err)
		}
		pages++
		if len(items) == 0 {
			break
		}
		for _, it := range items {
			if it.Seq <= since {
				t.Fatalf("the cursor repeated or reordered a row: seq %d after %d", it.Seq, since)
			}
			since = it.Seq
			got = append(got, it.ID)
		}
		if pages > 20 {
			t.Fatal("the cursor did not advance")
		}
	}
	if strings.Join(got, ",") != strings.Join(wantB, ",") {
		t.Errorf("a branch B device pages exactly its own rows in seq order:\n got  %v\n want %v", got, wantB)
	}
	if pages != 3 {
		t.Errorf("four rows at two a page is two full pages and an empty one, got %d pages", pages)
	}
}

// TestPickPullFence_ASyncCreateWithNoBranchIsRefused: the order brief answers no branch; the sync create refuses
// with a plain refusal and writes no row, so no pick is born branchless through the door (UX-81 round 2).
func TestPickPullFence_ASyncCreateWithNoBranchIsRefused(t *testing.T) {
	d := newPickDoor(t)
	s := NewSyncStore(d.pool, pickDoorOrders{branch: ""}, nil)
	_, err := s.Create(context.Background(), d.f, uuid.NewString(), map[string]any{"order_id": uuid.NewString()})
	if err == nil || !errors.Is(err, hhsync.ErrOpRefused) {
		t.Fatalf("a sync create for an order with no branch must be refused, got %v", err)
	}
	if !strings.Contains(err.Error(), "no branch") {
		t.Errorf("the refusal is plain: %v", err)
	}
	var n int
	if err := d.pool.QueryRow(context.Background(), `SELECT count(*) FROM tasks WHERE org_id = $1::uuid`, d.org).Scan(&n); err != nil || n != 0 {
		t.Errorf("no row is written: %d rows, %v", n, err)
	}
}

// pushJSON pushes one op as the given caller and returns the whole result as JSON, the way a device reads it.
func (d *pickDoor) pushAs(sc identity.StaffContext, op apigen.SyncPushOperation) (apigen.SyncPushResult, string) {
	d.t.Helper()
	d.authority = func() (identity.StaffContext, bool, error) { return sc, true, nil }
	res := d.push(op)
	raw, _ := json.Marshal(res[0])
	return res[0], string(raw)
}

// TestPickPullFence_AStaleWriteOnAPickOutOfReachGetsNoRowBack: the engine builds a stale write's conflict envelope
// from what SyncStore.Load returns. A branch B device that knows a branch A pick's id (it appears in chat deep
// links) and sends an update with an old base_seq used to get the whole row back (branch, claimer, order). Load is
// held to the pull's reach (UX-81 round 2): such a write is answered "row not found" and no row rides the reply.
func TestPickPullFence_AStaleWriteOnAPickOutOfReachGetsNoRowBack(t *testing.T) {
	d := newPickDoor(t)
	pickA, pickB, pickNone := d.task(d.branchA), d.task(d.branchB), d.branchlessTask()
	aCaller := scopedTo(d.user, d.branchA)
	if res, _ := d.pushAs(aCaller, apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "update", Payload: map[string]any{"id": pickA, "status": "ACCEPTED"}}); res.Status != "applied" {
		t.Fatalf("A claims: %+v", res)
	}
	claimer := d.user
	b := scopedTo(uuid.NewString(), d.branchB)
	stale := int64(0)
	staleUpdate := func(id string) apigen.SyncPushOperation {
		return apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "update", BaseSeq: &stale, Payload: map[string]any{"id": id, "photo_url": "x"}}
	}

	for name, id := range map[string]string{"another branch's pick": pickA, "a branchless pick": pickNone} {
		res, raw := d.pushAs(b, staleUpdate(id))
		t.Logf("branch B device, stale update on %s -> %s", name, raw)
		if res.Status != "rejected" || res.Conflict != nil {
			t.Errorf("a stale update on %s must be rejected with no envelope, got status %q conflict %v", name, res.Status, res.Conflict != nil)
		}
		if res.Error == nil || !strings.Contains(*res.Error, "not found") {
			t.Errorf("a stale update on %s is answered not found, got %v", name, res.Error)
		}
		if strings.Contains(raw, claimer) || strings.Contains(raw, d.branchA) || strings.Contains(raw, id) {
			t.Errorf("no row data (claimer, branch, id) may ride the reply for %s: %s", name, raw)
		}
	}
	// A delete is read the same way.
	res, raw := d.pushAs(b, apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "delete", Payload: map[string]any{"id": pickA}})
	if res.Status != "rejected" || strings.Contains(raw, claimer) {
		t.Errorf("a delete of another branch's pick is rejected with no row: %s", raw)
	}

	// The reach is the pull's: the owning branch and a GLOBAL caller still get the envelope, and B its own pick.
	if res, _ := d.pushAs(aCaller, staleUpdate(pickA)); res.Status == "rejected" {
		t.Errorf("the owning branch's stale write is still judged, not hidden: %+v", res)
	}
	global := scopedTo(uuid.NewString())
	global.Scope, global.Tier = identity.ScopeGlobal, identity.TierLeadership
	for name, id := range map[string]string{"A's pick": pickA, "the branchless pick": pickNone} {
		if res, _ := d.pushAs(global, staleUpdate(id)); res.Status != "conflict" || res.Conflict == nil {
			t.Errorf("a GLOBAL caller's stale write on %s still gets the envelope: %+v", name, res)
		}
	}
	if res, _ := d.pushAs(b, staleUpdate(pickB)); res.Status == "rejected" && res.Error != nil && strings.Contains(*res.Error, "not found") {
		t.Errorf("a branch B device still reaches its own branch's pick")
	}
}

// TestPickPullFence_AReplayNeverAnswersForARowOutOfReach: FindApplied resolves a client key to the row it applied.
// A device that presents a key another branch's operation applied is told nothing: the row is out of its reach, so
// it is not "applied" for this caller, and the owning branch's own replay is still answered.
func TestPickPullFence_AReplayNeverAnswersForARowOutOfReach(t *testing.T) {
	d := newPickDoor(t)
	pickA := d.task(d.branchA)
	aCaller := scopedTo(d.user, d.branchA)
	key := uuid.NewString()
	claim := apigen.SyncPushOperation{ClientId: key, Op: "update", Payload: map[string]any{"id": pickA, "status": "ACCEPTED"}}
	first, _ := d.pushAs(aCaller, claim)
	if first.Status != "applied" {
		t.Fatalf("A claims: %+v", first)
	}
	again, _ := d.pushAs(aCaller, claim)
	if again.Status != "applied" || again.Seq == nil {
		t.Errorf("the owning branch's replay is still answered applied: %+v", again)
	}
	d.authority = func() (identity.StaffContext, bool, error) { return scopedTo(uuid.NewString(), d.branchB), true, nil }
	row, found, err := d.store.FindApplied(context.Background(), d.f, key)
	if err != nil || found || row.ID != "" || row.Seq != 0 {
		t.Errorf("another branch's device is told nothing of a key it did not apply: found %v seq %d id %q err %v", found, row.Seq, row.ID, err)
	}
	d.authority = func() (identity.StaffContext, bool, error) { return scopedTo(d.user, d.branchA), true, nil }
	if _, found, err := d.store.FindApplied(context.Background(), d.f, key); err != nil || !found {
		t.Errorf("the owning branch's device still resolves its key: found %v err %v", found, err)
	}
}

// unknownOrders is an order seam that knows no order, the answer a sync create meets for an id that is not an order.
type unknownOrders struct{}

func (unknownOrders) OrderBrief(context.Context, string) (OrderBrief, error) {
	return OrderBrief{}, ErrUnknownOrder
}

// TestPickPullFence_ASyncCreateOnAnotherBranchsOrderIsRefusedAsUnknown (UX-81 round 3): a branch B device that
// names an order on branch A used to create a pick on branch A's board. The create is held to the caller's reach
// (callerReaches, the rule of Load and the pull) and an order out of reach is refused with exactly the words and
// status an unknown order gets, so the refusal tells nothing.
func TestPickPullFence_ASyncCreateOnAnotherBranchsOrderIsRefusedAsUnknown(t *testing.T) {
	d := newPickDoor(t)
	staffOf := func(sc identity.StaffContext) func(context.Context, string, string) (identity.StaffContext, bool, error) {
		return func(context.Context, string, string) (identity.StaffContext, bool, error) { return sc, true, nil }
	}
	b := scopedTo(uuid.NewString(), d.branchB)
	onA := NewSyncStore(d.pool, pickDoorOrders{branch: d.branchA}, nil).WithStaffAuthority(staffOf(b))
	unknown := NewSyncStore(d.pool, unknownOrders{}, nil).WithStaffAuthority(staffOf(b))
	create := func(s *SyncStore) error {
		_, err := s.Create(context.Background(), d.f, uuid.NewString(), map[string]any{"order_id": uuid.NewString()})
		return err
	}
	wantErr := create(unknown)
	if wantErr == nil || !errors.Is(wantErr, hhsync.ErrOpRefused) {
		t.Fatalf("the unknown order refusal is the pattern: %v", wantErr)
	}
	gotErr := create(onA)
	if gotErr == nil || gotErr.Error() != wantErr.Error() || !errors.Is(gotErr, hhsync.ErrOpRefused) {
		t.Errorf("an order on another branch must be refused with exactly the unknown order words:\n got  %v\n want %v", gotErr, wantErr)
	}
	var n int
	if err := d.pool.QueryRow(context.Background(), `SELECT count(*) FROM tasks WHERE org_id = $1::uuid`, d.org).Scan(&n); err != nil || n != 0 {
		t.Errorf("a refused create writes no row: %d rows, %v", n, err)
	}
	// The order's own branch and a GLOBAL caller still create.
	own := NewSyncStore(d.pool, pickDoorOrders{branch: d.branchA}, nil).WithStaffAuthority(staffOf(scopedTo(uuid.NewString(), d.branchA)))
	if err := create(own); err != nil {
		t.Errorf("the order's own branch still creates: %v", err)
	}
	global := scopedTo(uuid.NewString())
	global.Scope, global.Tier = identity.ScopeGlobal, identity.TierLeadership
	if err := create(NewSyncStore(d.pool, pickDoorOrders{branch: d.branchA}, nil).WithStaffAuthority(staffOf(global))); err != nil {
		t.Errorf("a GLOBAL caller still creates: %v", err)
	}
	// A scoped caller on a branchless order meets the same unknown order words (it must not learn the order exists).
	none := NewSyncStore(d.pool, pickDoorOrders{branch: ""}, nil).WithStaffAuthority(staffOf(b))
	if err := create(none); err == nil || err.Error() != wantErr.Error() {
		t.Errorf("a scoped caller on a branchless order meets the unknown order words too: %v", err)
	}
}

// TestPickPullFence_AFailedAuthorityReadIsRetryableNotARefusal (UX-81 round 4, S2's P3): when the staff authority
// cannot be read, a legitimate create used to be answered "unknown order" (a rejected op, which the device drops for
// good). An unreadable authority is no verdict on the operation: every reach read now returns a plain error, which
// the sync engine does not classify as the operation's rejection (engine.go classified: only ErrRowNotFound,
// ErrRowExists and ErrOpRefused are), so the whole push fails and the HTTP layer answers 503 "sync unavailable"
// (sync/handler.go), the answer a device retries with the operation still in its outbox. The retry then applies.
func TestPickPullFence_AFailedAuthorityReadIsRetryableNotARefusal(t *testing.T) {
	d := newPickDoor(t)
	create := apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "create", Payload: map[string]any{"order_id": uuid.NewString()}}
	d.authority = func() (identity.StaffContext, bool, error) {
		return identity.StaffContext{}, false, errors.New("authority read failed")
	}
	resp, err := d.engine.Push(context.Background(), d.info, d.f, []apigen.SyncPushOperation{create})
	if err == nil {
		t.Fatalf("a failed authority read must fail the push (retryable), got results %+v", resp.Results)
	}
	if errors.Is(err, hhsync.ErrOpRefused) || strings.Contains(err.Error(), "unknown order") {
		t.Errorf("the failure is no refusal and no unknown order answer: %v", err)
	}
	var n int
	if e := d.pool.QueryRow(context.Background(), `SELECT count(*) FROM tasks WHERE org_id = $1::uuid`, d.org).Scan(&n); e != nil || n != 0 {
		t.Errorf("a failed read writes nothing: %d rows, %v", n, e)
	}
	// The same read failing on a pull is the same retryable error, never an empty page that hides it.
	if _, _, perr := d.store.Pull(context.Background(), d.f, 0, 10); perr == nil {
		t.Errorf("a pull whose authority cannot be read must fail, not answer an empty page")
	}
	// The authority back: the very same operation applies.
	d.authority = func() (identity.StaffContext, bool, error) { return scopedTo(d.user, d.branchA), true, nil }
	if res := d.push(create); res[0].Status != "applied" {
		t.Errorf("the retried create applies once the authority answers: %+v %v", res[0], res[0].Error)
	}
}
