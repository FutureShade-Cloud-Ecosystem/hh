package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// claimer_role_test.go pins UX-75: the pick payload names the claimer by ROLE LABEL ("Yard Manager"),
// never by name, email or id, on the REST detail and list and on the sync pick_tasks rows.

const claimerUser = "99999999-9999-4999-8999-999999999999"

type fakeClaimerRoles struct {
	labels map[string]string
	err    error
	asked  [][]string
}

func (f *fakeClaimerRoles) RoleLabelsForUsers(_ context.Context, ids []string) (map[string]string, error) {
	f.asked = append(f.asked, ids)
	if f.err != nil {
		return nil, f.err
	}
	return f.labels, nil
}

func claimedSvc(t *testing.T, roles ClaimerRoleReader) (*Service, string, string) {
	t.Helper()
	svc, store, _ := newSvc(t)
	svc.WithClaimerRoles(roles)
	claimed, err := svc.CreateFromOrder(context.Background(), CreateInput{OrderID: knownOrder, CreatedBy: "picker"})
	if err != nil {
		t.Fatalf("create claimed: %v", err)
	}
	c := store.tasks[claimed.ID]
	c.ClaimedBy = claimerUser
	store.tasks[claimed.ID] = c
	free, err := svc.CreateFromOrder(context.Background(), CreateInput{OrderID: knownOrder, CreatedBy: "picker"})
	if err != nil {
		t.Fatalf("create unclaimed: %v", err)
	}
	return svc, claimed.ID, free.ID
}

func TestPickDetail_ServesTheClaimersRoleLabel(t *testing.T) {
	roles := &fakeClaimerRoles{labels: map[string]string{claimerUser: "Yard Manager"}}
	svc, claimedID, freeID := claimedSvc(t, roles)

	d, err := svc.PickDetail(context.Background(), claimedID)
	if err != nil {
		t.Fatalf("PickDetail: %v", err)
	}
	if d.Task.ClaimedByRole != "Yard Manager" {
		t.Fatalf("claimed_by_role = %q, want Yard Manager", d.Task.ClaimedByRole)
	}
	raw, _ := json.Marshal(d)
	if !strings.Contains(string(raw), `"claimed_by_role":"Yard Manager"`) {
		t.Fatalf("payload lacks the label: %s", raw)
	}

	free, err := svc.PickDetail(context.Background(), freeID)
	if err != nil {
		t.Fatalf("PickDetail free: %v", err)
	}
	if free.Task.ClaimedByRole != "" {
		t.Fatalf("an unclaimed pick served %q", free.Task.ClaimedByRole)
	}
	rawFree, _ := json.Marshal(free)
	if strings.Contains(string(rawFree), "claimed_by_role") {
		t.Fatalf("an unclaimed pick carries the field: %s", rawFree)
	}
}

func TestPickQueue_ServesTheLabelOnClaimedRowsOnly(t *testing.T) {
	roles := &fakeClaimerRoles{labels: map[string]string{claimerUser: "Yard Manager"}}
	svc, claimedID, _ := claimedSvc(t, roles)

	q, err := svc.PickQueue(context.Background(), TaskFilter{BranchID: branchA})
	if err != nil {
		t.Fatalf("PickQueue: %v", err)
	}
	if len(q) != 2 {
		t.Fatalf("queue len = %d, want 2", len(q))
	}
	for _, e := range q {
		want := ""
		if e.Task.ID == claimedID {
			want = "Yard Manager"
		}
		if e.Task.ClaimedByRole != want {
			t.Fatalf("task %s claimed_by_role = %q, want %q", e.Task.ID, e.Task.ClaimedByRole, want)
		}
	}
	// One read for the whole board, and only the claimer's id goes to it.
	if len(roles.asked) != 1 || len(roles.asked[0]) != 1 || roles.asked[0][0] != claimerUser {
		t.Fatalf("role reads = %v, want one read of the claimer", roles.asked)
	}
}

func TestPickRead_DegradesToNoLabelWhenTheSeamFailsOrIsAbsent(t *testing.T) {
	for name, roles := range map[string]ClaimerRoleReader{
		"failing": &fakeClaimerRoles{err: errors.New("identity down")},
		"unwired": nil,
	} {
		svc, claimedID, _ := claimedSvc(t, roles)
		d, err := svc.PickDetail(context.Background(), claimedID)
		if err != nil {
			t.Fatalf("%s: a role fault must not fail the pick: %v", name, err)
		}
		if d.Task.ClaimedByRole != "" {
			t.Fatalf("%s: label = %q", name, d.Task.ClaimedByRole)
		}
	}
}

func TestSyncDecorate_ServesTheClaimersRoleLabel(t *testing.T) {
	roles := &fakeClaimerRoles{labels: map[string]string{claimerUser: "Yard Manager"}}
	s := NewSyncStore(nil, nil, nil).WithClaimerRoles(roles)

	claimed := &sync.RowState{ID: "t1", Data: map[string]any{"id": "t1", "claimed_by": claimerUser}}
	s.DecorateServerState(context.Background(), sync.ScopeFilter{}, claimed)
	if claimed.Data["claimed_by_role"] != "Yard Manager" {
		t.Fatalf("claimed row data = %v", claimed.Data)
	}
	free := &sync.RowState{ID: "t2", Data: map[string]any{"id": "t2"}}
	s.DecorateServerState(context.Background(), sync.ScopeFilter{}, free)
	if _, has := free.Data["claimed_by_role"]; has {
		t.Fatalf("an unclaimed row carries the field: %v", free.Data)
	}
}

func TestAttachClaimerRoles_OneReadForAPageAndNoNameOrId(t *testing.T) {
	roles := &fakeClaimerRoles{labels: map[string]string{claimerUser: "Yard Manager"}}
	s := NewSyncStore(nil, nil, nil).WithClaimerRoles(roles)
	rows := []map[string]any{
		{"id": "a", "claimed_by": claimerUser},
		{"id": "b", "claimed_by": claimerUser},
		{"id": "c"},
	}
	s.attachClaimerRoles(context.Background(), rows)
	if len(roles.asked) != 1 || len(roles.asked[0]) != 1 {
		t.Fatalf("role reads = %v, want one read of one distinct id", roles.asked)
	}
	for _, r := range rows[:2] {
		if r["claimed_by_role"] != "Yard Manager" {
			t.Fatalf("row %v lacks the label", r)
		}
	}
	if _, has := rows[2]["claimed_by_role"]; has {
		t.Fatalf("unclaimed row carries the field: %v", rows[2])
	}
	// A claimer with no live grant has no label: the field is absent, never the id.
	s2 := NewSyncStore(nil, nil, nil).WithClaimerRoles(&fakeClaimerRoles{labels: map[string]string{}})
	one := []map[string]any{{"id": "d", "claimed_by": claimerUser}}
	s2.attachClaimerRoles(context.Background(), one)
	if v, has := one[0]["claimed_by_role"]; has {
		t.Fatalf("a claimer without a role served %v", v)
	}
}
