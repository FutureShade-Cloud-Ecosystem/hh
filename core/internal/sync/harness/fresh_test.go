package harness

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// recorder is a fake testing.TB: it records failures instead of failing the
// real test, so a leg that must make Fresh fail can be asserted.
type recorder struct {
	testing.TB
	failures []string
}

func (r *recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

type stopRun struct{}

func (r *recorder) Fatalf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
	panic(stopRun{})
}

// run executes fn, absorbing the stop a Fatalf raises.
func (r *recorder) run(fn func()) {
	defer func() {
		if p := recover(); p != nil {
			if _, ok := p.(stopRun); !ok {
				panic(p)
			}
		}
	}()
	fn()
}

// quietWrite changes a row's field and leaves its seq where it was: the
// defect Fresh exists to catch.
func (f *fakeStore) quietWrite(id string, fields map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.id == id {
			for k, v := range fields {
				r.fields[k] = v
			}
			return nil
		}
	}
	return sync.ErrRowNotFound
}

func freshFixture(t *testing.T) (*fakeStore, FreshCase, string) {
	t.Helper()
	info, ok := sync.Lookup("pick_tasks")
	if !ok {
		t.Fatalf("scope pick_tasks missing")
	}
	store := &fakeStore{}
	e := sync.NewEngine()
	if err := e.RegisterScope(info.Name, store); err != nil {
		t.Fatalf("register: %v", err)
	}
	filter := sync.ScopeFilter{OrgID: "3ef10000-0000-4000-8000-000000000001", Actor: "fresh"}
	row, err := store.Create(context.Background(), filter, "k1", map[string]any{"kind": "fixture", "v": 0})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return store, FreshCase{Engine: e, Scope: info, Filter: filter}, row.ID
}

func wantV(v int) func(map[string]any) error {
	return func(d map[string]any) error {
		if d["v"] != v {
			return fmt.Errorf("v is %v, want %d", d["v"], v)
		}
		return nil
	}
}

func TestFreshPassesWhenTheWriteMovesSeq(t *testing.T) {
	store, c, id := freshFixture(t)
	c.Write = func(context.Context) (string, error) { return id, store.deskEdit(id, map[string]any{"v": 1}) }
	c.Want = wantV(1)
	rec := &recorder{TB: t}
	rec.run(func() { Fresh(rec, c) })
	if len(rec.failures) != 0 {
		t.Fatalf("a write that moves seq failed Fresh: %v", rec.failures)
	}
}

func TestFreshFailsWhenTheWriteDoesNotMoveSeq(t *testing.T) {
	store, c, id := freshFixture(t)
	c.Write = func(context.Context) (string, error) { return id, store.quietWrite(id, map[string]any{"v": 1}) }
	c.Want = wantV(1)
	rec := &recorder{TB: t}
	rec.run(func() { Fresh(rec, c) })
	if len(rec.failures) != 1 {
		t.Fatalf("a write that leaves seq must fail Fresh once, got %v", rec.failures)
	}
	if !strings.Contains(rec.failures[0], "pull from the device's cursor") || !strings.Contains(rec.failures[0], "returns it: false") {
		t.Fatalf("failure words: %q", rec.failures[0])
	}
}

func TestFreshFailsOnAStaleValue(t *testing.T) {
	store, c, id := freshFixture(t)
	c.Write = func(context.Context) (string, error) { return id, store.deskEdit(id, map[string]any{"v": 1}) }
	c.Want = wantV(2)
	rec := &recorder{TB: t}
	rec.run(func() { Fresh(rec, c) })
	if len(rec.failures) != 1 || !strings.Contains(rec.failures[0], "changed value: false") {
		t.Fatalf("a wrong value must fail Fresh: %v", rec.failures)
	}
}

func TestFreshTombstone(t *testing.T) {
	store, c, id := freshFixture(t)
	c.Write = func(context.Context) (string, error) {
		return id, store.deskEdit(id, map[string]any{"deleted": true})
	}
	rec := &recorder{TB: t}
	rec.run(func() { Fresh(rec, c) })
	if len(rec.failures) != 0 {
		t.Fatalf("a tombstoning write failed Fresh: %v", rec.failures)
	}
	// A quiet delete never reaches the feed: Fresh must fail.
	store2, c2, id2 := freshFixture(t)
	c2.Write = func(context.Context) (string, error) {
		return id2, store2.quietWrite(id2, map[string]any{"deleted": true})
	}
	rec2 := &recorder{TB: t}
	rec2.run(func() { Fresh(rec2, c2) })
	if len(rec2.failures) != 1 || !strings.Contains(rec2.failures[0], "tombstone") {
		t.Fatalf("a quiet delete must fail Fresh: %v", rec2.failures)
	}
}

func TestFreshFollowsPages(t *testing.T) {
	store, c, id := freshFixture(t)
	for i := 0; i < 450; i++ {
		if _, err := store.Create(context.Background(), c.Filter, fmt.Sprintf("bulk%d", i), map[string]any{"kind": "bulk", "v": 0}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	c.Write = func(context.Context) (string, error) { return id, store.deskEdit(id, map[string]any{"v": 1}) }
	c.Want = wantV(1)
	Fresh(t, c)
}

func TestFreshForRunsEveryArm(t *testing.T) {
	store, c, id := freshFixture(t)
	c.Write = func(context.Context) (string, error) { return id, store.deskEdit(id, map[string]any{"v": 1}) }
	c.Want = wantV(1)
	staff := sync.ScopeFilter{OrgID: c.Filter.OrgID, Actor: "staff"}
	tenant := sync.ScopeFilter{OrgID: c.Filter.OrgID, Tenant: true, TenantID: "t1", Actor: "tenant"}
	FreshFor(t, c, FreshArm{Name: "staff", Filter: staff}, FreshArm{Name: "tenant", Filter: tenant})
}
