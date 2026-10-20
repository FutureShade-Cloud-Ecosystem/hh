//go:build integration

// syncpick_integration_test.go: the pick record door of the pick_tasks sync scope (Field CP-2) against a
// real database, through the real engine. The store under test is the tasks SyncStore wired as the
// composition root wires it (the tasks Service as the record body, a staff authority seam); the order
// module's seam is the recording double the unit suites use, so "the same effect as the direct door" is
// proved by running both doors over the same lines and comparing what the order was told and what the
// task_pick_lines rows hold.
//
// Run with DATABASE_URL set to a throwaway migrated database; the legs skip politely without one.
package tasks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

type pickDoorOrders struct{ branch string }

func (o pickDoorOrders) OrderBrief(_ context.Context, orderID string) (OrderBrief, error) {
	return OrderBrief{ID: orderID, BranchID: o.branch}, nil
}

type pickDoor struct {
	t       *testing.T
	pool    *pgxpool.Pool
	org     string
	other   string
	branchA string
	branchB string
	yard    *fakeYardOrder
	svc     *Service
	store   *SyncStore
	engine  *hhsync.Engine
	info    hhsync.ScopeInfo
	f       hhsync.ScopeFilter
	// authority is what the staff seam answers; tests replace it.
	authority func() (identity.StaffContext, bool, error)
	// hold is the order double the service talks to; it can hold a pooled connection for the length of the
	// call, as the real order module does.
	hold *connOrder
	// user is the linked ERP user the default authority answers.
	user string
	// actors records who the record body was asked to attribute to.
	actors []string
}

type actorSpy struct {
	inner PickRecorder
	spy   *[]string
}

func (a actorSpy) RecordPick(ctx context.Context, taskID, actorID string, lines []PickedLineInput) ([]PickedLine, error) {
	*a.spy = append(*a.spy, actorID)
	return a.inner.RecordPick(ctx, taskID, actorID, lines)
}

func newPickDoor(t *testing.T) *pickDoor { return newPickDoorConns(t, 0) }

// newPickDoorConns is newPickDoor over a pool capped at conns connections (zero keeps the default).
func newPickDoorConns(t *testing.T, conns int32) *pickDoor {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("pick record door leg: DATABASE_URL not set")
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("pool config: %v", err)
	}
	if conns > 0 {
		cfg.MaxConns = conns
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	d := &pickDoor{
		t: t, pool: pool,
		org: uuid.NewString(), other: uuid.NewString(),
		branchA: uuid.NewString(), branchB: uuid.NewString(),
		yard: &fakeYardOrder{},
	}
	for _, o := range []string{d.org, d.other} {
		if _, err := pool.Exec(ctx, `INSERT INTO org (id, slug, name) VALUES ($1, $2, 'pick door leg')`, o, "pickdoor-"+o); err != nil {
			t.Fatalf("seed org: %v", err)
		}
	}
	t.Cleanup(func() {
		bg := context.Background()
		for _, o := range []string{d.org, d.other} {
			_, _ = pool.Exec(bg, `DELETE FROM tasks WHERE org_id = $1`, o)
			_, _ = pool.Exec(bg, `DELETE FROM org WHERE id = $1`, o)
		}
	})
	d.hold = &connOrder{fakeYardOrder: d.yard, pool: pool}
	d.svc = NewService(NewRepository(pool)).WithYardOrderChanges(d.hold)
	// The caller is a linked ERP user, as the direct door requires (taskBranchFence answers 401 without one).
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, status) VALUES ($1, 'pick door leg', 'ACTIVE') RETURNING id::text`,
		"pickdoor-"+d.org+"@example.invalid").Scan(&d.user); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM task_pick_lines WHERE picked_by = $1::uuid`, d.user)
		_, _ = pool.Exec(bg, `DELETE FROM task_events WHERE actor_id = $1::uuid`, d.user)
		_, _ = pool.Exec(bg, `DELETE FROM users WHERE id = $1::uuid`, d.user)
	})
	link := func(_ context.Context, sub string) (string, bool) { return d.user, true }
	d.authority = func() (identity.StaffContext, bool, error) {
		return identity.StaffContext{Scope: identity.ScopeGlobal, Status: identity.StatusActive, UserID: d.user}, true, nil
	}
	d.store = NewSyncStore(pool, pickDoorOrders{branch: d.branchA}, link).
		WithPickRecorder(actorSpy{inner: d.svc, spy: &d.actors}).
		WithStaffAuthority(func(context.Context, string, string) (identity.StaffContext, bool, error) { return d.authority() })
	// The engine runs as the composition root wires it: the key ledger (sync_op_hashes) in front of the
	// door's own idempotency stamp.
	d.engine = hhsync.NewEngine().WithKeyLedger(hhsync.NewPgKeyLedger(pool))
	if err := d.engine.RegisterScope("pick_tasks", d.store); err != nil {
		t.Fatalf("register: %v", err)
	}
	info, ok := hhsync.Lookup("pick_tasks")
	if !ok {
		t.Fatal("pick_tasks missing from the registry")
	}
	d.info = info
	d.f = hhsync.ScopeFilter{OrgID: d.org, Actor: "pick-door-actor"}
	return d
}

// task births one PICK task in the org on the given branch through the real sync create.
func (d *pickDoor) task(branch string) string {
	d.t.Helper()
	s := NewSyncStore(d.pool, pickDoorOrders{branch: branch}, nil)
	row, err := s.Create(context.Background(), d.f, uuid.NewString(), map[string]any{"order_id": uuid.NewString()})
	if err != nil {
		d.t.Fatalf("create task: %v", err)
	}
	return row.ID
}

func (d *pickDoor) push(ops ...apigen.SyncPushOperation) []apigen.SyncPushResult {
	d.t.Helper()
	resp, err := d.engine.Push(context.Background(), d.info, d.f, ops)
	if err != nil {
		d.t.Fatalf("push: %v", err)
	}
	return resp.Results
}

func recordOp(key, taskID string, lines ...map[string]any) apigen.SyncPushOperation {
	arr := make([]any, len(lines))
	for i, l := range lines {
		arr[i] = l
	}
	return apigen.SyncPushOperation{ClientId: key, Op: "update", Payload: map[string]any{"id": taskID, "picked": arr}}
}

func pickLine(no int) map[string]any {
	l := goodLine()
	l["order_line_no"] = float64(no)
	return l
}

type lineRow struct {
	no                                int
	ordered, picked, reason, note, un string
}

func (d *pickDoor) lines(taskID string) []lineRow {
	d.t.Helper()
	rows, err := d.pool.Query(context.Background(),
		`SELECT order_line_no, qty_ordered::text, qty_picked::text, short_reason, short_note, uom
		   FROM task_pick_lines WHERE task_id = $1::uuid ORDER BY order_line_no`, taskID)
	if err != nil {
		d.t.Fatalf("read lines: %v", err)
	}
	defer rows.Close()
	var out []lineRow
	for rows.Next() {
		var r lineRow
		if err := rows.Scan(&r.no, &r.ordered, &r.picked, &r.reason, &r.note, &r.un); err != nil {
			d.t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func (d *pickDoor) taskSeq(taskID string) int64 {
	d.t.Helper()
	var seq int64
	if err := d.pool.QueryRow(context.Background(), `SELECT seq FROM tasks WHERE id = $1::uuid`, taskID).Scan(&seq); err != nil {
		d.t.Fatalf("read seq: %v", err)
	}
	return seq
}

func TestPickDoor_EffectEqualsTheDirectDoor(t *testing.T) {
	d := newPickDoor(t)
	direct, viaSync := d.task(d.branchA), d.task(d.branchA)
	in := []PickedLineInput{
		{OrderLineNo: 1, SKU: "SKU-1", Description: "Armour stone", QtyOrdered: "10", QtyPicked: "7", UOM: "EA", ShortReason: ShortReasonNotInYard},
		{OrderLineNo: 2, SKU: "SKU-2", Description: "Sand", QtyOrdered: "4", QtyPicked: "4", UOM: "EA"},
	}
	if _, err := d.svc.RecordPick(context.Background(), direct, "", in); err != nil {
		t.Fatalf("direct door: %v", err)
	}
	l2 := pickLine(2)
	l2["sku"], l2["description"], l2["qty_ordered"], l2["qty_picked"], l2["short_reason"] = "SKU-2", "Sand", "4", "4", ""
	res := d.push(recordOp(uuid.NewString(), viaSync, pickLine(1), l2))
	if res[0].Status != "applied" {
		t.Fatalf("the sync door must apply: %+v (error %v)", res[0], res[0].Error)
	}

	if len(d.yard.picks) != 2 {
		t.Fatalf("the order must be told once per door, got %d", len(d.yard.picks))
	}
	a, b := d.yard.picks[0], d.yard.picks[1]
	if fmt.Sprintf("%+v", a.Lines) != fmt.Sprintf("%+v", b.Lines) {
		t.Fatalf("the order was told different things:\ndirect %+v\nsync   %+v", a.Lines, b.Lines)
	}
	if b.OrderID == "" || a.OrderID == b.OrderID {
		t.Fatalf("each door is told its own task's order: %q %q", a.OrderID, b.OrderID)
	}
	if fmt.Sprintf("%+v", d.lines(direct)) != fmt.Sprintf("%+v", d.lines(viaSync)) {
		t.Fatalf("the recorded lines differ:\ndirect %+v\nsync   %+v", d.lines(direct), d.lines(viaSync))
	}
	// The row re-appears in the device's pull: a fresh seq, and the pull carries the lines.
	items, _, err := d.store.Pull(context.Background(), d.f, 0, 100)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	found := false
	for _, it := range items {
		if it.ID != viaSync {
			continue
		}
		found = true
		picked, _ := it.Data["picked"].([]any)
		if len(picked) != 2 {
			t.Fatalf("the pulled row must carry the recorded lines, got %+v", it.Data["picked"])
		}
		if it.Seq != d.taskSeq(viaSync) {
			t.Fatalf("pulled seq %d, row seq %d", it.Seq, d.taskSeq(viaSync))
		}
	}
	if !found {
		t.Fatal("the recorded task is missing from the pull")
	}
}

func TestPickDoor_ReplaySameKeyAppliesOnceAndAnswersApplied(t *testing.T) {
	d := newPickDoor(t)
	id := d.task(d.branchA)
	key := uuid.NewString()
	first := d.push(recordOp(key, id, pickLine(1)))
	if first[0].Status != "applied" {
		t.Fatalf("first: %+v %v", first[0], first[0].Error)
	}
	seq := d.taskSeq(id)
	second := d.push(recordOp(key, id, pickLine(1)))
	if second[0].Status != "applied" {
		t.Fatalf("a replay answers applied, got %+v %v", second[0], second[0].Error)
	}
	if len(d.yard.picks) != 1 {
		t.Fatalf("the record must run once, ran %d times", len(d.yard.picks))
	}
	if d.taskSeq(id) != seq {
		t.Fatalf("a replay must not move the row: seq %d then %d", seq, d.taskSeq(id))
	}
	// A later write on the same row does not lose the earlier key.
	later := d.push(apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "update", Payload: map[string]any{"id": id, "status": "ACCEPTED"}})
	if later[0].Status != "applied" {
		t.Fatalf("later write: %+v %v", later[0], later[0].Error)
	}
	third := d.push(recordOp(key, id, pickLine(1)))
	if third[0].Status != "applied" || len(d.yard.picks) != 1 {
		t.Fatalf("the key still resolves after a later write: %+v, record ran %d times", third[0], len(d.yard.picks))
	}
}

func TestPickDoor_RefusalsRecordNothing(t *testing.T) {
	d := newPickDoor(t)
	id := d.task(d.branchA)
	unknownLeg := pickLine(1)
	unknownLeg["picked_at"] = "a client stamp"
	above := pickLine(1)
	above["qty_picked"] = "11"
	noReason := pickLine(1)
	noReason["short_reason"] = ""
	cases := map[string]apigen.SyncPushOperation{
		"unknown leg":          recordOp(uuid.NewString(), id, unknownLeg),
		"above ordered":        recordOp(uuid.NewString(), id, above),
		"short with no reason": recordOp(uuid.NewString(), id, noReason),
		"duplicate line":       recordOp(uuid.NewString(), id, pickLine(1), pickLine(1)),
		"mixed with status": {ClientId: uuid.NewString(), Op: "update",
			Payload: map[string]any{"id": id, "status": "ACCEPTED", "picked": []any{pickLine(1)}}},
		"skid count stays direct": {ClientId: uuid.NewString(), Op: "update",
			Payload: map[string]any{"id": id, "skid_count": float64(3)}},
	}
	for name, op := range cases {
		res := d.push(op)
		if res[0].Status != "rejected" {
			t.Fatalf("%s: want rejected, got %+v", name, res[0])
		}
		if res[0].Error == nil || *res[0].Error == "" {
			t.Fatalf("%s: a refusal carries the module's words", name)
		}
		if n := len(d.lines(id)); n != 0 {
			t.Fatalf("%s: a refused op records nothing, found %d lines", name, n)
		}
	}
	if !strings.Contains(*d.push(cases["above ordered"])[0].Error, "ordered") {
		t.Fatal("the above ordered refusal keeps the order module's words")
	}
}

func TestPickDoor_BranchFenceIsTheDirectDoors(t *testing.T) {
	d := newPickDoor(t)
	own := d.task(d.branchA)
	foreign := d.task(d.branchB)

	d.authority = func() (identity.StaffContext, bool, error) {
		return identity.StaffContext{Scope: identity.ScopeBranch, Status: identity.StatusActive, UserID: d.user, Branches: map[string]bool{d.branchA: true}}, true, nil
	}
	if res := d.push(recordOp(uuid.NewString(), foreign, pickLine(1))); res[0].Status != "rejected" {
		t.Fatalf("a branch scoped caller must not record another branch's pick: %+v", res[0])
	}
	if len(d.yard.picks) != 0 || len(d.lines(foreign)) != 0 {
		t.Fatal("the fenced pick must record nothing and tell the order nothing")
	}
	if res := d.push(recordOp(uuid.NewString(), own, pickLine(1))); res[0].Status != "applied" {
		t.Fatalf("a branch scoped caller records their own branch: %+v %v", res[0], res[0].Error)
	}
	d.authority = func() (identity.StaffContext, bool, error) {
		return identity.StaffContext{Scope: identity.ScopeGlobal, Status: identity.StatusActive, UserID: d.user}, true, nil
	}
	if res := d.push(recordOp(uuid.NewString(), foreign, pickLine(1))); res[0].Status != "applied" {
		t.Fatalf("a global caller reaches every branch: %+v %v", res[0], res[0].Error)
	}
	d.authority = func() (identity.StaffContext, bool, error) { return identity.StaffContext{}, false, nil }
	if res := d.push(recordOp(uuid.NewString(), own, pickLine(2))); res[0].Status != "rejected" {
		t.Fatalf("no staff authority in the org is a refusal: %+v", res[0])
	}
	d.authority = func() (identity.StaffContext, bool, error) {
		return identity.StaffContext{Scope: identity.ScopeGlobal, Status: "SUSPENDED", UserID: d.user}, true, nil
	}
	if res := d.push(recordOp(uuid.NewString(), own, pickLine(2))); res[0].Status != "rejected" {
		t.Fatalf("an inactive staff member is a refusal: %+v", res[0])
	}
}

func TestPickDoor_OrgIsolation(t *testing.T) {
	d := newPickDoor(t)
	id := d.task(d.branchA)
	other := d.f
	d.f = hhsync.ScopeFilter{OrgID: d.other, Actor: "pick-door-actor"}
	res := d.push(recordOp(uuid.NewString(), id, pickLine(1)))
	if res[0].Status != "rejected" || len(d.lines(id)) != 0 || len(d.yard.picks) != 0 {
		t.Fatalf("another org's pick is the same as absent: %+v", res[0])
	}
	d.f = other
	if res := d.push(recordOp(uuid.NewString(), id, pickLine(1))); res[0].Status != "applied" {
		t.Fatalf("the owning org records the same pick: %+v", res[0])
	}
}

func TestPickDoor_ServerWinsOnAStaleBase(t *testing.T) {
	d := newPickDoor(t)
	id := d.task(d.branchA)
	stale := int64(0)
	op := recordOp(uuid.NewString(), id, pickLine(1))
	op.BaseSeq = &stale
	res := d.push(op)
	if res[0].Status != "conflict" || res[0].Conflict == nil || res[0].Conflict.Policy != "server_wins" {
		t.Fatalf("a stale base answers the server wins envelope: %+v", res[0])
	}
	if res[0].Conflict.Applied == nil || *res[0].Conflict.Applied {
		t.Fatalf("server wins applies nothing: %+v", res[0].Conflict)
	}
	if len(d.yard.picks) != 0 || len(d.lines(id)) != 0 {
		t.Fatal("a conflicted record must not run")
	}
	fresh := d.taskSeq(id)
	op2 := recordOp(uuid.NewString(), id, pickLine(1))
	op2.BaseSeq = &fresh
	if res := d.push(op2); res[0].Status != "applied" {
		t.Fatalf("a current base applies: %+v %v", res[0], res[0].Error)
	}
}

func TestPickDoor_PartialRefusalOrdering(t *testing.T) {
	d := newPickDoor(t)
	id := d.task(d.branchA)
	above := pickLine(1)
	above["qty_picked"] = "11"
	res := d.push(
		recordOp(uuid.NewString(), id, pickLine(1)),
		recordOp(uuid.NewString(), id, above),
		apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "update", Payload: map[string]any{"id": id, "status": "ACCEPTED"}},
	)
	got := []string{res[0].Status, res[1].Status, res[2].Status}
	want := []string{"applied", "rejected", "applied"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("results in push order: got %v want %v", got, want)
	}
	if rows := d.lines(id); len(rows) != 1 || rows[0].picked != "7.0000" {
		t.Fatalf("the refused record in the middle left the first intact: %+v", rows)
	}
}

func TestPickDoor_ActorIsTheLinkedUser(t *testing.T) {
	d := newPickDoor(t)
	id := d.task(d.branchA)
	if res := d.push(recordOp(uuid.NewString(), id, pickLine(1))); res[0].Status != "applied" {
		t.Fatalf("a linked caller records: %+v %v", res[0], res[0].Error)
	}
	if len(d.actors) != 1 || d.actors[0] != d.user {
		t.Fatalf("the record body is attributed to the linked ERP user, got %v", d.actors)
	}
	var by string
	if err := d.pool.QueryRow(context.Background(),
		`SELECT picked_by::text FROM task_pick_lines WHERE task_id = $1::uuid AND order_line_no = 1`, id).Scan(&by); err != nil || by != d.user {
		t.Fatalf("picked_by names the linked user: %q %v", by, err)
	}
}

// The direct door answers 401 "yard: unauthenticated" to a staff login with no linked ERP user
// (taskBranchFence); the sync door must not record, with no actor, for the same caller.
func TestPickDoor_UnlinkedLoginIsRefusedLikeTheDirectDoor(t *testing.T) {
	d := newPickDoor(t)
	id := d.task(d.branchA)
	d.authority = func() (identity.StaffContext, bool, error) {
		return identity.StaffContext{Scope: identity.ScopeGlobal, Status: identity.StatusActive, UserID: ""}, true, nil
	}
	res := d.push(recordOp(uuid.NewString(), id, pickLine(1)))
	if res[0].Status != "rejected" || res[0].Error == nil || !strings.Contains(*res[0].Error, "unauthenticated") {
		t.Fatalf("want rejected with the direct door's words, got %+v", res[0])
	}
	if len(d.actors) != 0 || len(d.yard.picks) != 0 || len(d.lines(id)) != 0 {
		t.Fatalf("an unattributed caller moves nothing: actors %q, order told %d times, %d lines", d.actors, len(d.yard.picks), len(d.lines(id)))
	}
}

// A record through the direct door must move the task's sync seq as every other write path does, or a
// device's pulled row keeps a stale picked projection and a queued sync record based on the older seq
// applies blind over the newer record (server wins must hold across both doors).
func TestPickDoor_DirectDoorRecordMovesTheSeqSoServerWinsHoldsAcrossDoors(t *testing.T) {
	d := newPickDoor(t)
	id := d.task(d.branchA)
	before := d.taskSeq(id)
	if _, err := d.svc.RecordPick(context.Background(), id, d.user, []PickedLineInput{{OrderLineNo: 1, QtyOrdered: "10", QtyPicked: "10", UOM: "EA"}}); err != nil {
		t.Fatal(err)
	}
	if after := d.taskSeq(id); after <= before {
		t.Fatalf("a direct door record must move the task's seq: before %d after %d", before, after)
	}
	told := len(d.yard.picks)
	op := recordOp(uuid.NewString(), id, pickLine(1))
	op.BaseSeq = &before
	res := d.push(op)
	if res[0].Status != "conflict" || res[0].Conflict == nil || res[0].Conflict.Policy != "server_wins" {
		t.Fatalf("a device record based on the older seq meets server wins: %+v", res[0])
	}
	if len(d.yard.picks) != told {
		t.Fatalf("a conflicted record must not run: order told %d times, was %d", len(d.yard.picks), told)
	}
	if rows := d.lines(id); len(rows) != 1 || rows[0].picked != "10.0000" {
		t.Fatalf("the newer record stands: %+v", rows)
	}
}

// The pull carries the ticket (the REST door's builder, so one shape) and the recorded lines, holds the
// still owed figure out, and stays inside the org.
func TestPickDoor_PullCarriesTheTicketAndThePickedLines(t *testing.T) {
	d := newPickDoor(t)
	orderID := uuid.NewString()
	row, err := d.store.Create(context.Background(), d.f, uuid.NewString(), map[string]any{"order_id": orderID})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	src := NewStubPickSource().Seed(PickTicket{
		OrderID: orderID, OrderNo: "SO-1", Source: "DISPATCH", HeldFromPicking: true, HoldWords: "Balance due", StillOwedCents: 12345,
		Lines: []PickLine{{LineNo: 1, Description: "Paver", QtyOrdered: "10", UOM: "EA"}, {LineNo: 2, Description: "Sand", QtyOrdered: "4", UOM: "EA"}},
	})
	d.store.WithPickSource(src)
	if res := d.push(recordOp(uuid.NewString(), row.ID, pickLine(1))); res[0].Status != "applied" {
		t.Fatalf("record: %+v %v", res[0], res[0].Error)
	}

	items, _, err := d.store.Pull(context.Background(), d.f, 0, 50)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	var data map[string]any
	for _, it := range items {
		if it.ID == row.ID {
			data = it.Data
		}
	}
	if data == nil {
		t.Fatal("the task did not come through the pull")
	}
	ticket, ok := data["ticket"].(map[string]any)
	if !ok {
		t.Fatalf("pulled row carries no ticket: %v", data)
	}
	if lines, _ := ticket["lines"].([]any); len(lines) != 2 {
		t.Fatalf("ticket lines = %v, want 2", ticket["lines"])
	}
	if ticket["held_from_picking"] != true || ticket["hold_words"] != "Balance due" {
		t.Fatalf("the hold mark must stay: %v", ticket)
	}
	if _, has := ticket["still_owed_cents"]; has {
		t.Fatalf("the figure must be held out of the pull: %v", ticket)
	}
	if picked, _ := data["picked"].([]any); len(picked) != 1 {
		t.Fatalf("picked = %v, want the one recorded line", data["picked"])
	}
	// A plain Load (the update and delete paths, the replay) pays for no ticket; the conflict envelope's
	// decoration carries it.
	rs, found, err := d.store.Load(context.Background(), d.f, row.ID)
	if err != nil || !found || rs.Data["ticket"] != nil {
		t.Fatalf("Load must carry no ticket: %v %v %v", rs.Data, found, err)
	}
	d.store.DecorateServerState(context.Background(), d.f, &rs)
	if rs.Data["ticket"] == nil {
		t.Fatalf("the conflict envelope's row carries no ticket: %v", rs.Data)
	}
	// Another org sees nothing, so no ticket leaves with it.
	foreign := hhsync.ScopeFilter{OrgID: d.other, Actor: "pick-door-actor"}
	if items, _, err := d.store.Pull(context.Background(), foreign, 0, 50); err != nil || len(items) != 0 {
		t.Fatalf("another org's pull must be empty: %v %v", items, err)
	}
}

// The ticket leaves only to a caller whose staff authority reaches the row's branch: a branch narrowed
// actor pulls the other branch's row with its task fields and no ticket, GLOBAL reaches both, and an
// authority that cannot be resolved attaches nothing (fail closed). Load and FindApplied follow the
// same rule.
func TestPickDoor_TicketFollowsTheCallersBranchReach(t *testing.T) {
	d := newPickDoor(t)
	ctx := context.Background()
	src := NewStubPickSource()
	d.store.WithPickSource(src)
	mk := func(branch string) (taskID, orderID string) {
		orderID = uuid.NewString()
		src.Seed(PickTicket{OrderID: orderID, OrderNo: "SO-" + branch[:4], CustomerName: "Walk In",
			Lines: []PickLine{{LineNo: 1, Description: "Paver", QtyOrdered: "2", UOM: "EA"}}})
		s := NewSyncStore(d.pool, pickDoorOrders{branch: branch}, nil)
		row, err := s.Create(ctx, d.f, uuid.NewString(), map[string]any{"order_id": orderID})
		if err != nil {
			t.Fatalf("create task: %v", err)
		}
		return row.ID, orderID
	}
	inA, _ := mk(d.branchA)
	inB, _ := mk(d.branchB)

	has := func(items []hhsync.Entity, id string) (present, ticket bool) {
		for _, it := range items {
			if it.ID == id {
				_, ticket = it.Data["ticket"]
				return true, ticket
			}
		}
		return false, false
	}
	narrowed := func() (identity.StaffContext, bool, error) {
		return identity.StaffContext{Scope: identity.ScopeBranch, Status: identity.StatusActive, UserID: d.user,
			Branches: map[string]bool{d.branchA: true}}, true, nil
	}

	d.authority = narrowed
	items, _, err := d.store.Pull(ctx, d.f, 0, 50)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if p, tk := has(items, inA); !p || !tk {
		t.Fatalf("own branch row: present=%v ticket=%v, want both", p, tk)
	}
	if p, _ := has(items, inB); p {
		t.Fatalf("other branch row: present=%v, a branch scoped caller no longer pulls another branch's pick (UX-81)", p)
	}
	decorated := func(id string) map[string]any {
		rs, ok, err := d.store.Load(ctx, d.f, id)
		if err != nil || !ok {
			t.Fatalf("load %s: %v %v", id, ok, err)
		}
		d.store.DecorateServerState(ctx, d.f, &rs)
		return rs.Data
	}
	// UX-81 round 2: Load is held to the pull's reach, so there is no conflict envelope row at all for another
	// branch's pick (it used to be loaded and shown without its ticket).
	if _, ok, err := d.store.Load(ctx, d.f, inB); err != nil || ok {
		t.Fatalf("the other branch's row must not load for a branch scoped caller: ok=%v err=%v", ok, err)
	}
	if decorated(inA)["ticket"] == nil {
		t.Fatal("the conflict envelope of the own branch's row lost its ticket")
	}

	// GLOBAL reaches both.
	d.authority = func() (identity.StaffContext, bool, error) {
		return identity.StaffContext{Scope: identity.ScopeGlobal, Status: identity.StatusActive, UserID: d.user}, true, nil
	}
	items, _, _ = d.store.Pull(ctx, d.f, 0, 50)
	if _, tk := has(items, inB); !tk {
		t.Fatal("a GLOBAL caller must receive the other branch's ticket")
	}

	// Unresolvable authority (UX-81): nothing comes, fail closed; the pull does not fail.
	for name, auth := range map[string]func() (identity.StaffContext, bool, error){
		"error":   func() (identity.StaffContext, bool, error) { return identity.StaffContext{}, false, errors.New("down") },
		"unknown": func() (identity.StaffContext, bool, error) { return identity.StaffContext{}, false, nil },
		"inactive": func() (identity.StaffContext, bool, error) {
			return identity.StaffContext{Scope: identity.ScopeGlobal, Status: "SUSPENDED", UserID: d.user}, true, nil
		},
	} {
		d.authority = auth
		items, _, err := d.store.Pull(ctx, d.f, 0, 50)
		if err != nil {
			t.Fatalf("%s: pull must not fail: %v", name, err)
		}
		for _, id := range []string{inA, inB} {
			if p, _ := has(items, id); p {
				t.Fatalf("%s: row %s present=%v, an authority that cannot be used pulls nothing (UX-81)", name, id, p)
			}
		}
	}
}

// errPickSource answers every ticket read with one fixed error.
type errPickSource struct{ err error }

func (e errPickSource) PickTicket(context.Context, string) (PickTicket, error) {
	return PickTicket{}, e.err
}
func (e errPickSource) PendingTickets(context.Context, string, int, int) ([]PickTicket, int, error) {
	return nil, 0, e.err
}

// A transient source fault fails the pull page (the engine answers 503 and the device retries the same
// cursor) rather than shipping a ticket-less row the device would keep until the task next moves; an
// order the source does not know, and a task with no order id, are the quiet skips.
func TestPickDoor_PullFailsThePageOnATransientTicketFault(t *testing.T) {
	d := newPickDoor(t)
	ctx := context.Background()
	row, err := d.store.Create(ctx, d.f, uuid.NewString(), map[string]any{"order_id": uuid.NewString()})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	d.store.WithPickSource(errPickSource{err: errors.New("hold read: connection reset")})
	if items, _, err := d.store.Pull(ctx, d.f, 0, 50); err == nil {
		t.Fatalf("a transient source fault must fail the page, got %d items", len(items))
	}

	d.store.WithPickSource(errPickSource{err: fmt.Errorf("read: %w", ErrUnknownOrder)})
	items, _, err := d.store.Pull(ctx, d.f, 0, 50)
	if err != nil {
		t.Fatalf("an unknown order is a quiet skip: %v", err)
	}
	var seen bool
	for _, it := range items {
		if it.ID == row.ID {
			seen = true
			if _, has := it.Data["ticket"]; has {
				t.Fatal("an unknown order carries no ticket")
			}
		}
	}
	if !seen {
		t.Fatal("the row must still come through")
	}
}
