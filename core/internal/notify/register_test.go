package notify

import (
	"context"
	"testing"
)

// A duplicate handler registration for the same topic must PANIC, the boot-safe single-handler-per-topic
// invariant W6 relies on (gl + inventory register disjoint topic sets; a collision must fail loudly at wire
// time, never silently clobber a topic's routing).
func TestRegister_PanicsOnDuplicateTopic(t *testing.T) {
	d := NewDispatcher(&fakeStore{}, nil)
	noop := func(context.Context, Event) error { return nil }

	d.Register("w6.topic", noop) // first registration is fine

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("a second Register for the same topic must panic")
		}
	}()
	d.Register("w6.topic", noop) // duplicate → panic
}

// A duplicate across Register + RegisterSecret is equally a collision and must panic.
func TestRegister_PanicsAcrossSecret(t *testing.T) {
	d := NewDispatcher(&fakeStore{}, nil)
	noop := func(context.Context, Event) error { return nil }

	d.Register("w6.mixed", noop)

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("RegisterSecret on an already-registered topic must panic")
		}
	}()
	d.RegisterSecret("w6.mixed", noop)
}

// Distinct topics register cleanly (the invariant only fires on a genuine collision).
func TestRegister_DistinctTopicsOK(t *testing.T) {
	d := NewDispatcher(&fakeStore{}, nil)
	noop := func(context.Context, Event) error { return nil }
	d.Register("w6.a", noop)
	d.Register("w6.b", noop)
	d.RegisterSecret("w6.c", noop)
}
