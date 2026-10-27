//go:build integration

// syncweights_fence_integration_test.go, UX-81c: the NEED_WEIGHT sync create holds the pick door's fences, through the
// real database. The caller's org holds the order read, the caller's branch reach holds the branch, and a refusal
// carries exactly the words an unknown order gets.
package tasks

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// weightDoorOrders answers an order only inside the org that owns it, on one branch: the shape of the real adapter.
type weightDoorOrders struct {
	owner  map[string]string // order id -> owning org
	branch string
}

func (o weightDoorOrders) OrderBrief(_ context.Context, id string) (OrderBrief, error) {
	return OrderBrief{ID: id, BranchID: o.branch}, nil // the unscoped read, which the fenced store never takes
}

func (o weightDoorOrders) OrderBriefInOrg(_ context.Context, orgID, id string) (OrderBrief, error) {
	if o.owner[id] != orgID {
		return OrderBrief{}, ErrUnknownOrder
	}
	return OrderBrief{ID: id, BranchID: o.branch}, nil
}

func TestWeightSyncCreate_HoldsTheCallersOrgAndBranchReach(t *testing.T) {
	d := newPickDoor(t)
	north, south := d.org, d.other
	order := uuid.NewString()
	staffOf := func(sc identity.StaffContext) identity.SyncStaffResolver {
		return func(context.Context, string, string) (identity.StaffContext, bool, error) { return sc, true, nil }
	}
	branches := branchesOfOrg{d.branchA: {south: true, north: true}, d.branchB: {south: true, north: true}}
	store := func(orders OrderReader, sc identity.StaffContext) *WeightSyncStore {
		return NewWeightSyncStore(d.pool, orders, nil).WithStaffAuthority(staffOf(sc)).WithBranchOrgReader(branches)
	}
	southOrder := weightDoorOrders{owner: map[string]string{order: south}, branch: d.branchA}
	global := scopedTo(uuid.NewString())
	global.Scope, global.Tier = identity.ScopeGlobal, identity.TierLeadership
	create := func(s *WeightSyncStore, f hhsync.ScopeFilter, orderID string) error {
		_, err := s.Create(context.Background(), f, uuid.NewString(), map[string]any{"order_id": orderID, "order_line_no": 1})
		return err
	}
	southF := hhsync.ScopeFilter{OrgID: south, Actor: "weight-door-actor"}
	count := func(org string) int {
		var n int
		if err := d.pool.QueryRow(context.Background(), `SELECT count(*) FROM tasks WHERE org_id = $1::uuid AND task_type = 'NEED_WEIGHT'`, org).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	wantErr := create(store(unknownOrders{}, global), southF, uuid.NewString())
	if wantErr == nil || !errors.Is(wantErr, hhsync.ErrOpRefused) {
		t.Fatalf("the unknown order refusal is the pattern: %v", wantErr)
	}
	// North's GLOBAL caller naming south's order.
	if err := create(store(southOrder, global), d.f, order); err == nil || err.Error() != wantErr.Error() || !errors.Is(err, hhsync.ErrOpRefused) {
		t.Errorf("another org's order must be refused with exactly the unknown order words:\n got  %v\n want %v", err, wantErr)
	}
	// South's caller scoped to a branch the order is not on.
	if err := create(store(southOrder, scopedTo(uuid.NewString(), d.branchB)), southF, order); err == nil || err.Error() != wantErr.Error() {
		t.Errorf("an out of reach branch must be refused with exactly the unknown order words: %v", err)
	}
	if n := count(north) + count(south); n != 0 {
		t.Errorf("a refused create writes no row: %d rows", n)
	}
	// The owning org on its own branch creates, and the row carries the caller's org.
	if err := create(store(southOrder, scopedTo(uuid.NewString(), d.branchA)), southF, order); err != nil {
		t.Fatalf("the owning org on its own branch creates: %v", err)
	}
	if count(south) != 1 || count(north) != 0 {
		t.Errorf("the created task carries the caller's org: south %d north %d", count(south), count(north))
	}
	if !store(southOrder, global).OrgScoped() || NewWeightSyncStore(d.pool, southOrder, nil).OrgScoped() {
		t.Errorf("OrgScoped reports the fences wired, and an unfenced store as not")
	}
}
