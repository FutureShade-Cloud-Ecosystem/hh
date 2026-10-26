//go:build integration

// weight_answer_fence_integration_test.go, UX-81c round 2: the weight ANSWER doors (REST POST
// /api/v1/yard/picks/{id}/weight, and the weights sync Write and Delete) hold the caller's org and branch reach to the
// task, through the real repository on a real database. The REST door rewrites a priced order line, so the order
// line's quantity is read back after every refusal.
package tasks

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// answerLines is an order line seam that keeps the entered quantity, so a test reads the line back.
type answerLines struct{ qty string }

func (a *answerLines) LineUOM(context.Context, string, int) (string, error) { return "T", nil }
func (a *answerLines) SetLineQty(_ context.Context, _ string, _ int, qty string) error {
	a.qty = qty
	return nil
}

// stampBranch gives a branch a locations row in an org, the row the org held task read joins through.
func stampBranch(t *testing.T, d *pickDoor, branch, org string) {
	t.Helper()
	if _, err := d.pool.Exec(context.Background(),
		`INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name, address, org_id)
		 VALUES ($1::uuid, $1::uuid, NULL, 'BRANCH', 'W2'||substr($1::text,1,6), 'weight answer leg', '{}', $2::uuid)
		 ON CONFLICT (id) DO UPDATE SET org_id = EXCLUDED.org_id`, branch, org); err != nil {
		t.Fatalf("stamp a branch's org: %v", err)
	}
	t.Cleanup(func() { _, _ = d.pool.Exec(context.Background(), `DELETE FROM locations WHERE id = $1::uuid`, branch) })
}

func TestWeightAnswer_RESTHoldsTheTasksOrgAndBranchReach(t *testing.T) {
	d := newPickDoor(t)
	north, south := d.org, d.other
	northB := uuid.NewString() // a second North branch, out of reach for a caller granted branch A
	stampBranch(t, d, d.branchA, north)
	stampBranch(t, d, northB, north)
	stampBranch(t, d, d.branchB, south)
	lines := &answerLines{qty: "5"}
	repo := NewRepository(d.pool)
	svc := NewService(repo).WithOrderLineWriter(lines)
	h := NewHandler(svc, nil).WithBranchOrgReader(branchesOfOrg{d.branchA: {north: true}, northB: {north: true}, d.branchB: {south: true}})
	orgOf := map[string]string{d.branchA: north, northB: north, d.branchB: south}
	mk := func(branch string) string {
		line := 1
		task, err := repo.CreateTask(context.Background(), NewTaskInput{OrderID: uuid.NewString(), OrderLineNo: &line, BranchID: branch,
			TaskType: TypeNeedWeight, Priority: PriorityHigh, Source: SourceManual, OrgID: orgOf[branch]})
		if err != nil {
			t.Fatalf("create weight task: %v", err)
		}
		t.Cleanup(func() {
			_, _ = d.pool.Exec(context.Background(), `DELETE FROM task_events WHERE task_id = $1::uuid`, task.ID)
			_, _ = d.pool.Exec(context.Background(), `DELETE FROM tasks WHERE id = $1::uuid`, task.ID)
		})
		return task.ID
	}
	southTask, northBTask, ownTask := mk(d.branchB), mk(northB), mk(d.branchA)
	scoped := func(branch string) identity.StaffContext {
		return identity.StaffContext{UserID: uuid.NewString(), RoleCode: "YARD_TEAM", Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
			Status: identity.StatusActive, Branches: map[string]bool{branch: true}}
	}
	global := identity.StaffContext{UserID: uuid.NewString(), Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: identity.StatusActive}
	submit := func(id, org string, sc identity.StaffContext) (int, string) {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/yard/picks/"+id+"/weight", bytes.NewBufferString(`{"weight":"2.5","uom":"T"}`))
		r.SetPathValue("id", id)
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(identity.ContextWithStaff(r.Context(), sc))
		w := httptest.NewRecorder()
		h.submitWeight(w, r, guards.StaffCaller{StaffScope: authz.StaffScope{OrgID: org}})
		return w.Code, strings.TrimSpace(w.Body.String())
	}
	status := func(id string) string {
		var s string
		if err := d.pool.QueryRow(context.Background(), `SELECT status FROM tasks WHERE id = $1::uuid`, id).Scan(&s); err != nil {
			t.Fatalf("read task: %v", err)
		}
		return s
	}

	missingCode, missingBody := submit(uuid.NewString(), north, global)
	if missingCode != http.StatusNotFound {
		t.Fatalf("a missing task is the pattern: %d %s", missingCode, missingBody)
	}
	// North's GLOBAL caller on South's task.
	if code, body := submit(southTask, north, global); code != missingCode || body != missingBody || lines.qty != "5" || status(southTask) != StatusQueued {
		t.Errorf("a foreign org's task answers as missing and writes nothing: %d %s, line qty %q, status %s", code, body, lines.qty, status(southTask))
	}
	// A North caller granted branch A on North's other branch.
	if code, body := submit(northBTask, north, scoped(d.branchA)); code != missingCode || body != missingBody || lines.qty != "5" || status(northBTask) != StatusQueued {
		t.Errorf("an out of reach task answers as missing and writes nothing: %d %s, line qty %q, status %s", code, body, lines.qty, status(northBTask))
	}
	// The caller's own task still works and writes the line.
	if code, body := submit(ownTask, north, scoped(d.branchA)); code != http.StatusOK || lines.qty != "2.5000" {
		t.Errorf("the caller's own task still answers: %d %s, line qty %q", code, body, lines.qty)
	}
}

func TestWeightAnswer_SyncWriteAndDeleteHoldTheCallersBranchReach(t *testing.T) {
	d := newPickDoor(t)
	north := d.org
	stampBranch(t, d, d.branchA, north)
	stampBranch(t, d, d.branchB, north)
	staffOf := func(sc identity.StaffContext) identity.SyncStaffResolver {
		return func(context.Context, string, string) (identity.StaffContext, bool, error) { return sc, true, nil }
	}
	branches := branchesOfOrg{d.branchA: {north: true}, d.branchB: {north: true}}
	global := scopedTo(uuid.NewString())
	global.Scope, global.Tier = identity.ScopeGlobal, identity.TierLeadership
	store := func(sc identity.StaffContext) *WeightSyncStore {
		return NewWeightSyncStore(d.pool, weightDoorOrders{owner: map[string]string{}, branch: d.branchA}, nil).
			WithStaffAuthority(staffOf(sc)).WithBranchOrgReader(branches)
	}
	order := uuid.NewString()
	own := NewWeightSyncStore(d.pool, weightDoorOrders{owner: map[string]string{order: north}, branch: d.branchA}, nil).
		WithStaffAuthority(staffOf(global)).WithBranchOrgReader(branches)
	mk := func() string {
		row, err := own.Create(context.Background(), d.f, uuid.NewString(), map[string]any{"order_id": order, "order_line_no": 1})
		if err != nil {
			t.Fatalf("create weight task: %v", err)
		}
		return row.ID
	}
	forB, forA := store(scopedTo(uuid.NewString(), d.branchB)), store(scopedTo(uuid.NewString(), d.branchA))
	write := map[string]any{"weight_entered": "2.5", "weight_uom": "T"}

	id := mk()
	if _, err := forB.Write(context.Background(), d.f, id, uuid.NewString(), write); !errors.Is(err, hhsync.ErrRowNotFound) {
		t.Errorf("a write from another branch is the row not found a missing task gets: %v", err)
	}
	if _, err := forB.Delete(context.Background(), d.f, id, uuid.NewString()); !errors.Is(err, hhsync.ErrRowNotFound) {
		t.Errorf("a delete from another branch is the row not found a missing task gets: %v", err)
	}
	var status string
	var deleted bool
	if err := d.pool.QueryRow(context.Background(), `SELECT status, deleted_at IS NOT NULL FROM tasks WHERE id = $1::uuid`, id).Scan(&status, &deleted); err != nil || status != StatusQueued || deleted {
		t.Errorf("a refused call changes nothing: status %s deleted %v err %v", status, deleted, err)
	}
	if _, err := forA.Write(context.Background(), d.f, id, uuid.NewString(), write); err != nil {
		t.Errorf("the task's own branch writes: %v", err)
	}
	id2 := mk()
	if _, err := forA.Delete(context.Background(), d.f, id2, uuid.NewString()); err != nil {
		t.Errorf("the task's own branch deletes: %v", err)
	}
}

// UX-81c round 3: the weights sync Pull and Load hold the caller's branch reach as the pick scope's pull does. A device
// scoped to branch B pulls and loads nothing of branch A's weight tasks, a deleted one's tombstone included, and its
// own branch's task still pulls and loads.
func TestWeightSyncPullAndLoadHoldTheCallersBranchReach(t *testing.T) {
	d := newPickDoor(t)
	north := d.org
	stampBranch(t, d, d.branchA, north)
	stampBranch(t, d, d.branchB, north)
	staffOf := func(sc identity.StaffContext) identity.SyncStaffResolver {
		return func(context.Context, string, string) (identity.StaffContext, bool, error) { return sc, true, nil }
	}
	branches := branchesOfOrg{d.branchA: {north: true}, d.branchB: {north: true}}
	global := scopedTo(uuid.NewString())
	global.Scope, global.Tier = identity.ScopeGlobal, identity.TierLeadership
	orderA, orderB := uuid.NewString(), uuid.NewString()
	owner := map[string]string{orderA: north, orderB: north}
	create := func(order, branch string) string {
		s := NewWeightSyncStore(d.pool, weightDoorOrders{owner: owner, branch: branch}, nil).
			WithStaffAuthority(staffOf(global)).WithBranchOrgReader(branches)
		row, err := s.Create(context.Background(), d.f, uuid.NewString(), map[string]any{"order_id": order, "order_line_no": 1})
		if err != nil {
			t.Fatalf("create weight task: %v", err)
		}
		return row.ID
	}
	onA, onB, goneOnA := create(orderA, d.branchA), create(orderB, d.branchB), create(orderA, d.branchA)
	byGlobal := NewWeightSyncStore(d.pool, weightDoorOrders{owner: owner, branch: d.branchA}, nil).
		WithStaffAuthority(staffOf(global)).WithBranchOrgReader(branches)
	if _, err := byGlobal.Delete(context.Background(), d.f, goneOnA, uuid.NewString()); err != nil {
		t.Fatalf("delete: %v", err)
	}
	deviceB := NewWeightSyncStore(d.pool, weightDoorOrders{owner: owner, branch: d.branchB}, nil).
		WithStaffAuthority(staffOf(scopedTo(uuid.NewString(), d.branchB))).WithBranchOrgReader(branches)

	items, tombs, err := deviceB.Pull(context.Background(), d.f, 0, 100)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	for _, it := range items {
		if it.ID == onA {
			t.Errorf("a branch B device pulled branch A's weight task")
		}
	}
	for _, tb := range tombs {
		if tb.ID == goneOnA {
			t.Errorf("a branch B device pulled the tombstone of branch A's deleted weight task")
		}
	}
	if len(items) != 1 || items[0].ID != onB {
		t.Errorf("the device pulls its own branch's task and only that: %d items", len(items))
	}
	if _, found, err := deviceB.Load(context.Background(), d.f, onA); err != nil || found {
		t.Errorf("loading branch A's weight task answers a missing row: found %v err %v", found, err)
	}
	if _, found, err := deviceB.Load(context.Background(), d.f, onB); err != nil || !found {
		t.Errorf("loading its own branch's weight task works: found %v err %v", found, err)
	}
	// A GLOBAL caller of the org still pulls both.
	if all, _, err := byGlobal.Pull(context.Background(), d.f, 0, 100); err != nil || len(all) != 2 {
		t.Errorf("a GLOBAL caller still pulls every branch of its org: %d items, %v", len(all), err)
	}
}
