//go:build integration

// syncshortage_create_integration_test.go: the shortage create of the shortages sync scope (Field CP-2)
// against a real database, through the real engine. The create admits the report legs only (the app drops
// the stamped legs from its payload), and it gives the direct door's two effects the sync store lacked:
// the parent task re-appears in the device's pick feed (its shortage counts moved), and the counter is
// told. The settle legs stay the counter's act and refuse.
package tasks

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

type shortDoor struct {
	t      *testing.T
	pool   *pgxpool.Pool
	org    string
	other  string
	notes  *fakeNotifier
	svc    *Service
	store  *ShortageSyncStore
	picks  *SyncStore
	engine *hhsync.Engine
	info   hhsync.ScopeInfo
	f      hhsync.ScopeFilter
}

func newShortDoor(t *testing.T) *shortDoor {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("shortage create leg: DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	d := &shortDoor{t: t, pool: pool, org: uuid.NewString(), other: uuid.NewString(), notes: &fakeNotifier{}}
	for _, o := range []string{d.org, d.other} {
		if _, err := pool.Exec(context.Background(), `INSERT INTO org (id, slug, name) VALUES ($1, $2, 'shortage door leg')`, o, "shortdoor-"+o); err != nil {
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
	d.svc = NewService(NewRepository(pool)).WithChatNotifier(d.notes)
	d.store = NewShortageSyncStore(pool, nil).WithNoticer(d.svc)
	d.picks = NewSyncStore(pool, pickDoorOrders{branch: uuid.NewString()}, nil)
	d.engine = hhsync.NewEngine()
	if err := d.engine.RegisterScope("shortages", d.store); err != nil {
		t.Fatalf("register: %v", err)
	}
	info, ok := hhsync.Lookup("shortages")
	if !ok {
		t.Fatal("shortages missing from the registry")
	}
	d.info = info
	d.f = hhsync.ScopeFilter{OrgID: d.org, Actor: "short-door-actor"}
	return d
}

func (d *shortDoor) task() string {
	d.t.Helper()
	row, err := d.picks.Create(context.Background(), d.f, uuid.NewString(), map[string]any{"order_id": uuid.NewString()})
	if err != nil {
		d.t.Fatalf("create task: %v", err)
	}
	return row.ID
}

func (d *shortDoor) push(ops ...apigen.SyncPushOperation) []apigen.SyncPushResult {
	d.t.Helper()
	resp, err := d.engine.Push(context.Background(), d.info, d.f, ops)
	if err != nil {
		d.t.Fatalf("push: %v", err)
	}
	return resp.Results
}

// appPayload is what the 6a app sends once the stamped legs are dropped.
func appPayload(id, taskID string) map[string]any {
	return map[string]any{
		"id": id, "task_id": taskID, "order_line_no": float64(1), "sku": "",
		"description": "two bundles of pavers", "qty_short": "2", "uom": "EA", "note": "bay 3 is empty",
	}
}

func (d *shortDoor) taskSeq(id string) int64 {
	d.t.Helper()
	var seq int64
	if err := d.pool.QueryRow(context.Background(), `SELECT seq FROM tasks WHERE id = $1::uuid`, id).Scan(&seq); err != nil {
		d.t.Fatalf("task seq: %v", err)
	}
	return seq
}

func (d *shortDoor) shortages(taskID string) []Shortage {
	d.t.Helper()
	out, err := NewRepository(d.pool).ListShortages(context.Background(), taskID, false)
	if err != nil {
		d.t.Fatalf("list shortages: %v", err)
	}
	return out
}

func TestShortageCreate_TheAppsPayloadAppliesAndStampedLegsRefuse(t *testing.T) {
	d := newShortDoor(t)
	id := d.task()
	sid := uuid.NewString()
	res := d.push(apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "create", Payload: appPayload(sid, id)})
	if res[0].Status != "applied" {
		t.Fatalf("the app's payload applies: %+v", res[0])
	}
	rows := d.shortages(id)
	if len(rows) != 1 || rows[0].ID != sid || rows[0].QtyShort != "2.0000" || rows[0].Note != "bay 3 is empty" {
		t.Fatalf("the report lands as sent: %+v", rows)
	}
	for _, leg := range []string{"reported_at", "resolved_at", "resolved_by", "resolution"} {
		p := appPayload(uuid.NewString(), id)
		p[leg] = "stamped by the device"
		r := d.push(apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "create", Payload: p})
		if r[0].Status != "rejected" {
			t.Fatalf("%s is the server's or the counter's to stamp: %+v", leg, r[0])
		}
	}
	if n := len(d.shortages(id)); n != 1 {
		t.Fatalf("the refused creates stored nothing, found %d shortages", n)
	}
}

func TestShortageCreate_EffectEqualsTheDirectDoor(t *testing.T) {
	d := newShortDoor(t)
	direct, viaSync := d.task(), d.task()
	directBefore, syncBefore := d.taskSeq(direct), d.taskSeq(viaSync)
	line := 1
	if _, err := d.svc.FlagShortage(context.Background(), ShortageInput{
		TaskID: direct, OrderLineNo: &line, Description: "two bundles of pavers", QtyShort: "2", UOM: "EA", Note: "bay 3 is empty",
	}); err != nil {
		t.Fatalf("direct door: %v", err)
	}
	sid := uuid.NewString()
	if r := d.push(apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "create", Payload: appPayload(sid, viaSync)}); r[0].Status != "applied" {
		t.Fatalf("sync door: %+v", r[0])
	}
	a, b := d.shortages(direct)[0], d.shortages(viaSync)[0]
	if a.QtyShort != b.QtyShort || a.UOM != b.UOM || a.Description != b.Description || a.Note != b.Note ||
		fmt.Sprint(*a.OrderLineNo) != fmt.Sprint(*b.OrderLineNo) {
		t.Fatalf("the stored report differs:\ndirect %+v\nsync   %+v", a, b)
	}
	if d.taskSeq(direct) <= directBefore || d.taskSeq(viaSync) <= syncBefore {
		t.Fatalf("the parent pick re-appears in the feed on both doors: direct %d>%d sync %d>%d",
			d.taskSeq(direct), directBefore, d.taskSeq(viaSync), syncBefore)
	}
	items, _, err := d.picks.Pull(context.Background(), d.f, 0, 100)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	for _, it := range items {
		if it.ID == viaSync && it.Data["open_shortages"] != float64(1) {
			t.Fatalf("the pulled task carries the moved shortage count: %+v", it.Data)
		}
	}
	if len(d.notes.posts) != 2 {
		t.Fatalf("the counter is told once per door, got %d posts", len(d.notes.posts))
	}
	x, y := d.notes.posts[0], d.notes.posts[1]
	if x.Topic != y.Topic || x.Audience != y.Audience || !strings.Contains(y.Summary, "SHORTAGE") ||
		!strings.Contains(y.Summary, "two bundles of pavers") || y.EventKey != "task:"+viaSync+":shortage:"+sid {
		t.Fatalf("the notice differs:\ndirect %+v\nsync   %+v", x, y)
	}
}

func TestShortageCreate_ReplayAppliesOnceAndNoticesOnce(t *testing.T) {
	d := newShortDoor(t)
	id := d.task()
	key := uuid.NewString()
	op := apigen.SyncPushOperation{ClientId: key, Op: "create", Payload: appPayload(uuid.NewString(), id)}
	if r := d.push(op); r[0].Status != "applied" {
		t.Fatalf("first: %+v", r[0])
	}
	seq := d.taskSeq(id)
	if r := d.push(op); r[0].Status != "applied" {
		t.Fatalf("a replay answers applied: %+v", r[0])
	}
	if len(d.shortages(id)) != 1 || len(d.notes.posts) != 1 || d.taskSeq(id) != seq {
		t.Fatalf("nothing runs twice: shortages %d, notices %d, seq %d then %d", len(d.shortages(id)), len(d.notes.posts), seq, d.taskSeq(id))
	}
}

func TestShortageSettleLegsStayTheCountersAct(t *testing.T) {
	d := newShortDoor(t)
	id := d.task()
	sid := uuid.NewString()
	d.push(apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "create", Payload: appPayload(sid, id)})
	for _, leg := range []string{"resolution", "resolved_at", "resolved_by"} {
		r := d.push(apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "update", Payload: map[string]any{"id": sid, leg: "settled on the truck"}})
		if r[0].Status != "rejected" {
			t.Fatalf("%s must refuse through sync: %+v", leg, r[0])
		}
	}
	if got := d.shortages(id)[0]; got.Resolution != "" || got.ResolvedAt != nil {
		t.Fatalf("the shortage stays open: %+v", got)
	}
}

func TestShortageCreate_ManualConflictAndIsolation(t *testing.T) {
	d := newShortDoor(t)
	id := d.task()
	sid := uuid.NewString()
	d.push(apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "create", Payload: appPayload(sid, id)})
	stale := int64(0)
	r := d.push(apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "update", BaseSeq: &stale,
		Payload: map[string]any{"id": sid, "qty_short": "5"}})
	if r[0].Status != "conflict" || r[0].Conflict == nil || r[0].Conflict.Policy != "manual" ||
		r[0].Conflict.Applied == nil || *r[0].Conflict.Applied {
		t.Fatalf("a stale correction waits for review, nothing applied: %+v", r[0])
	}
	if got := d.shortages(id)[0].QtyShort; got != "2.0000" {
		t.Fatalf("the report stands: %s", got)
	}
	// Another org's task is absent: the create is not found and stores nothing.
	d.f = hhsync.ScopeFilter{OrgID: d.other, Actor: "short-door-actor"}
	o := d.push(apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "create", Payload: appPayload(uuid.NewString(), id)})
	if o[0].Status != "rejected" || len(d.shortages(id)) != 1 {
		t.Fatalf("another org's task is the same as absent: %+v", o[0])
	}
}

func TestShortageCreate_PartialRefusalOrdering(t *testing.T) {
	d := newShortDoor(t)
	id := d.task()
	bad := appPayload(uuid.NewString(), id)
	bad["qty_short"] = "0"
	res := d.push(
		apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "create", Payload: appPayload(uuid.NewString(), id)},
		apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "create", Payload: bad},
		apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "create", Payload: appPayload(uuid.NewString(), id)},
	)
	got := []string{res[0].Status, res[1].Status, res[2].Status}
	if fmt.Sprint(got) != fmt.Sprint([]string{"applied", "rejected", "applied"}) {
		t.Fatalf("results in push order: %v", got)
	}
	if len(d.shortages(id)) != 2 || len(d.notes.posts) != 2 {
		t.Fatalf("only the accepted reports are stored and told: %d, %d", len(d.shortages(id)), len(d.notes.posts))
	}
}
