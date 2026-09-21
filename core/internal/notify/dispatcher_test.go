package notify

import (
	"context"
	"errors"
	"testing"
)

// fakeStore mimics PgOutboxStore over an in-memory queue: it claims rows in order, calls
// settle, and requeues PENDING rows (attempts+1) so the retry→dead-letter path is exercised
// with no database.
type fakeStore struct {
	queue   []Event
	settled []Settlement
}

func (f *fakeStore) ProcessBatch(ctx context.Context, n int, settle func(context.Context, Event) Settlement) (int, error) {
	c := 0
	for c < n && len(f.queue) > 0 {
		e := f.queue[0]
		f.queue = f.queue[1:]
		st := settle(ctx, e)
		f.settled = append(f.settled, st)
		if st.Status == statusPending {
			e.Attempts++
			f.queue = append(f.queue, e)
		}
		c++
	}
	return c, nil
}

func (f *fakeStore) last() Settlement { return f.settled[len(f.settled)-1] }

func TestDispatcher_PublishesOnSuccess(t *testing.T) {
	fs := &fakeStore{queue: []Event{{EventID: "1", Topic: "t.ok"}}}
	d := NewDispatcher(fs, nil)
	called := false
	d.Register("t.ok", func(context.Context, Event) error { called = true; return nil })

	if got := d.DrainOnce(context.Background()); got != 1 {
		t.Fatalf("DrainOnce = %d, want 1", got)
	}
	if !called {
		t.Error("handler was not invoked")
	}
	if fs.last().Status != statusPublished {
		t.Errorf("status = %q, want PUBLISHED", fs.last().Status)
	}
	if fs.last().Redact {
		t.Error("non-secret topic should not be redacted")
	}
}

// An observer runs AFTER the owner succeeds and is a non-owning side-effect: the row still PUBLISHES.
func TestDispatcher_ObserverRunsAfterOwnerAndDoesNotOwnSettlement(t *testing.T) {
	fs := &fakeStore{queue: []Event{{EventID: "1", Topic: "t.raw"}}}
	d := NewDispatcher(fs, nil)
	var order []string
	d.Register("t.raw", func(context.Context, Event) error { order = append(order, "owner"); return nil })
	d.RegisterObserver("t.raw", func(context.Context, Event) error { order = append(order, "observer"); return nil })

	if got := d.DrainOnce(context.Background()); got != 1 {
		t.Fatalf("DrainOnce = %d, want 1", got)
	}
	if len(order) != 2 || order[0] != "owner" || order[1] != "observer" {
		t.Fatalf("observer must run after owner, got %v", order)
	}
	if fs.last().Status != statusPublished {
		t.Errorf("row should publish, got %q", fs.last().Status)
	}
}

// An observer error is swallowed: it never fails, retries, or dead-letters the owner's row.
func TestDispatcher_ObserverErrorDoesNotFailRow(t *testing.T) {
	fs := &fakeStore{queue: []Event{{EventID: "1", Topic: "t.raw"}}}
	d := NewDispatcher(fs, nil)
	d.Register("t.raw", func(context.Context, Event) error { return nil })
	d.RegisterObserver("t.raw", func(context.Context, Event) error { return errors.New("messaging blip") })

	d.DrainOnce(context.Background())
	if fs.last().Status != statusPublished {
		t.Errorf("an observer error must not change the row's disposition, got %q", fs.last().Status)
	}
}

// If the owner FAILS, its observers must NOT run (the event was not delivered).
func TestDispatcher_ObserverSkippedWhenOwnerFails(t *testing.T) {
	fs := &fakeStore{queue: []Event{{EventID: "1", Topic: "t.raw"}}}
	d := NewDispatcher(fs, nil)
	d.Register("t.raw", func(context.Context, Event) error { return errors.New("owner down") })
	observed := false
	d.RegisterObserver("t.raw", func(context.Context, Event) error { observed = true; return nil })

	d.DrainOnce(context.Background())
	if observed {
		t.Error("observer must not run when the owner fails")
	}
}

// An observer on a topic with no primary owner is a wiring bug, Run panics fast.
func TestDispatcher_ObserverWithoutOwnerPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for observer with no primary handler")
		}
	}()
	d := NewDispatcher(&fakeStore{}, nil)
	d.RegisterObserver("t.orphan", func(context.Context, Event) error { return nil })
	d.validateObservers()
}

// A panicking observer must NOT crash the dispatcher: the owner has already succeeded, so the row
// still PUBLISHES, a subsequent event still processes (the drain loop survives), and a second
// non-panicking observer registered alongside the panicking one still runs.
func TestDispatcher_ObserverPanicDoesNotCrashDispatcher(t *testing.T) {
	fs := &fakeStore{queue: []Event{
		{EventID: "1", Topic: "t.raw"},
		{EventID: "2", Topic: "t.raw"},
	}}
	d := NewDispatcher(fs, nil)

	ownerCalls := 0
	d.Register("t.raw", func(context.Context, Event) error { ownerCalls++; return nil })
	// A panicking observer registered BEFORE a well-behaved one, to prove per-observer recovery
	// (the panic must not stop the sibling observer that follows it).
	d.RegisterObserver("t.raw", func(context.Context, Event) error { panic("messaging exploded") })
	siblingCalls := 0
	d.RegisterObserver("t.raw", func(context.Context, Event) error { siblingCalls++; return nil })

	// (b) The drain loop survives the panic and processes BOTH events; no re-panic escapes.
	if got := d.DrainOnce(context.Background()); got != 2 {
		t.Fatalf("DrainOnce = %d, want 2 (dispatcher must survive the observer panic)", got)
	}
	if ownerCalls != 2 {
		t.Fatalf("owner ran %d times, want 2 (subsequent event must still process)", ownerCalls)
	}

	// (a) The panic must not change the row's disposition, both rows still PUBLISH, none fails/dead-letters.
	if len(fs.settled) != 2 {
		t.Fatalf("settlements = %d, want 2", len(fs.settled))
	}
	for i, st := range fs.settled {
		if st.Status != statusPublished {
			t.Errorf("settlement[%d] = %q, want PUBLISHED (an observer panic must not fail the financial row)", i, st.Status)
		}
	}

	// (c) The sibling non-panicking observer still ran for each event.
	if siblingCalls != 2 {
		t.Errorf("sibling observer ran %d times, want 2 (one bad observer must not stop the others)", siblingCalls)
	}
}

func TestDispatcher_RedactsSecretTopicOnPublish(t *testing.T) {
	fs := &fakeStore{queue: []Event{{EventID: "1", Topic: "t.secret"}}}
	d := NewDispatcher(fs, nil)
	d.RegisterSecret("t.secret", func(context.Context, Event) error { return nil })

	d.DrainOnce(context.Background())
	if fs.last().Status != statusPublished {
		t.Fatalf("status = %q, want PUBLISHED", fs.last().Status)
	}
	if !fs.last().Redact {
		t.Error("secret topic must be redacted on publish")
	}
}

func TestDispatcher_RetriesThenDeadLetters(t *testing.T) {
	fs := &fakeStore{queue: []Event{{EventID: "1", Topic: "t.fail"}}}
	d := NewDispatcher(fs, nil)
	d.Register("t.fail", func(context.Context, Event) error { return errors.New("boom") })

	d.DrainOnce(context.Background())

	// maxAttempts=5 → 4 transient PENDINGs then a terminal DEAD.
	if len(fs.settled) != defaultMaxAttempts {
		t.Fatalf("settlements = %d, want %d", len(fs.settled), defaultMaxAttempts)
	}
	for i := 0; i < defaultMaxAttempts-1; i++ {
		if fs.settled[i].Status != statusPending {
			t.Errorf("settlement[%d] = %q, want PENDING", i, fs.settled[i].Status)
		}
	}
	if fs.last().Status != statusDead {
		t.Errorf("final status = %q, want DEAD", fs.last().Status)
	}
	if fs.last().LastError == "" {
		t.Error("dead-lettered row should carry a last_error")
	}
}

func TestDispatcher_UnknownTopicDeadLetters(t *testing.T) {
	fs := &fakeStore{queue: []Event{{EventID: "1", Topic: "t.nohandler"}}}
	d := NewDispatcher(fs, nil)

	d.DrainOnce(context.Background())
	if fs.last().Status != statusDead {
		t.Errorf("unknown topic final status = %q, want DEAD", fs.last().Status)
	}
}
