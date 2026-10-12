package sync

import (
	"context"
	"errors"
	"strings"
	gosync "sync"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

// memLedger is the in-memory KeyLedger the engine's unit legs run against:
// first write wins per (org, tenant, scope, key), exactly the contract the
// Postgres ledger keeps, with injectable faults on each half.
type memLedger struct {
	mu      gosync.Mutex
	hashes  map[string]string
	readErr error
	saveErr error
}

func newMemLedger() *memLedger { return &memLedger{hashes: map[string]string{}} }

func ledgerSlot(f ScopeFilter, scope, key string) string {
	return f.OrgID + "|" + f.TenantID + "|" + scope + "|" + key
}

func (l *memLedger) BodyHash(_ context.Context, f ScopeFilter, scope, key string) (string, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.readErr != nil {
		return "", false, l.readErr
	}
	h, ok := l.hashes[ledgerSlot(f, scope, key)]
	return h, ok, nil
}

func (l *memLedger) Remember(_ context.Context, f ScopeFilter, scope, key, hash string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.saveErr != nil {
		return l.saveErr
	}
	slot := ledgerSlot(f, scope, key)
	if _, ok := l.hashes[slot]; !ok {
		l.hashes[slot] = hash
	}
	return nil
}

func (l *memLedger) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.hashes)
}

func ledgerFixture(t *testing.T, policy Policy) (*Engine, *fakeStore, *memLedger, *auditRecorderDouble, ScopeInfo) {
	t.Helper()
	e, store, info, _ := newEngineFixture(t, policy)
	led := newMemLedger()
	marker := &auditRecorderDouble{}
	e.WithKeyLedger(led).WithAudit(marker, nil)
	return e, store, led, marker, info
}

func wantKeyReused(t *testing.T, r apigen.SyncPushResult) {
	t.Helper()
	if r.Status != "rejected" {
		t.Fatalf("status %q, want rejected: %+v", r.Status, r)
	}
	if r.Error == nil || !strings.HasPrefix(*r.Error, "key_reused") {
		t.Fatalf("error %v, want the key_reused reason word first", r.Error)
	}
	if r.Seq != nil || r.Conflict != nil {
		t.Fatalf("a refused reuse carries no seq and no card of the first: %+v", r)
	}
}

// The fourth guarantee: the same key with a different body is refused for
// that operation, never applied, and the first application stands.
func TestKeyBodyDifferentBodyIsRefusedAndNeverApplied(t *testing.T) {
	e, store, led, marker, info := ledgerFixture(t, PolicyServerWins)
	first := push(t, e, info,
		apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "v": "a"}})
	if first[0].Status != "applied" {
		t.Fatalf("setup: %+v", first[0])
	}
	if led.size() != 1 {
		t.Fatalf("the first application stored %d hashes, want 1", led.size())
	}
	before, _, _ := store.Load(context.Background(), testFilter, rowID(1))

	// Same key, a different payload, then a different op kind on the same
	// key, then a different base_seq: each is a different body.
	for name, op := range map[string]apigen.SyncPushOperation{
		"payload": {ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "v": "TAMPERED"}},
		"op":      {ClientId: key(1), Op: "delete", Payload: map[string]any{"id": rowID(1)}},
		"base":    {ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "v": "a"}, BaseSeq: i64(5)},
		"entity":  {ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "v": "a"}, Entity: str("other")},
	} {
		got := push(t, e, info, op)
		t.Run(name, func(t *testing.T) { wantKeyReused(t, got[0]) })
	}

	after, _, _ := store.Load(context.Background(), testFilter, rowID(1))
	if after.Seq != before.Seq || after.Data["v"] != "a" || after.Deleted {
		t.Fatalf("a refused reuse moved the row: before %+v after %+v", before, after)
	}
	if got := marker.count("SYNC_KEY_REUSED"); got != 4 {
		t.Fatalf("%d SYNC_KEY_REUSED audit rows, want 4", got)
	}
	for _, r := range marker.snapshot() {
		if r.Action == "SYNC_KEY_REUSED" && (r.Outcome != "rejected" || r.ClientID != key(1) || r.Scope != info.Name) {
			t.Errorf("reuse audit row malformed: %+v", r)
		}
	}
}

// The same key with the same body answers as it does today, from any
// number of replays, and the row never moves.
func TestKeyBodySameBodyReplaysAsApplied(t *testing.T) {
	e, store, led, _, info := ledgerFixture(t, PolicyServerWins)
	ops := []apigen.SyncPushOperation{
		{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "v": "a", "n": float64(3), "nested": map[string]any{"z": 1, "a": []any{"x", nil}}}},
		{ClientId: key(2), Op: "update", Payload: map[string]any{"id": rowID(1), "v": "b"}},
		{ClientId: key(3), Op: "delete", Payload: map[string]any{"id": rowID(1)}},
	}
	push(t, e, info, ops...)
	if led.size() != 3 {
		t.Fatalf("%d hashes stored, want one per applied operation (3)", led.size())
	}
	row, _, _ := store.Load(context.Background(), testFilter, rowID(1))
	for i := 0; i < 2; i++ {
		for j, r := range push(t, e, info, ops...) {
			if r.Status != "applied" || r.Seq == nil || r.Error != nil {
				t.Fatalf("replay %d op %d answered %+v, want applied with a seq", i, j, r)
			}
		}
	}
	after, _, _ := store.Load(context.Background(), testFilter, rowID(1))
	if after.Seq != row.Seq {
		t.Fatalf("replay re-applied: seq %d to %d", row.Seq, after.Seq)
	}
}

// A refusal sinks only its own operation: the operations behind it in the
// same push still land, and their keys are remembered.
func TestKeyBodyRefusalDoesNotSinkTheOnesBehindIt(t *testing.T) {
	e, _, led, _, info := ledgerFixture(t, PolicyServerWins)
	push(t, e, info, apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "v": "a"}})
	res := push(t, e, info,
		apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "v": "other"}},
		apigen.SyncPushOperation{ClientId: key(2), Op: "create", Payload: map[string]any{"id": rowID(2), "v": "b"}},
	)
	wantKeyReused(t, res[0])
	if res[1].Status != "applied" {
		t.Fatalf("the op behind the refusal answered %+v, want applied", res[1])
	}
	if led.size() != 2 {
		t.Fatalf("%d hashes, want 2", led.size())
	}
}

// A key that never applied is not claimed: a rejected operation (the
// module refused it) leaves the key free, so the client's corrected retry
// under the same key lands. Only an APPLIED key is bound to its body.
func TestKeyBodyRejectedOperationLeavesTheKeyFree(t *testing.T) {
	e, _, led, _, info := ledgerFixture(t, PolicyServerWins)
	res := push(t, e, info, apigen.SyncPushOperation{ClientId: key(1), Op: "update", Payload: map[string]any{"id": rowID(9), "v": "x"}})
	if res[0].Status != "rejected" {
		t.Fatalf("setup: %+v", res[0])
	}
	if led.size() != 0 {
		t.Fatalf("a rejected operation stored %d hashes, want none", led.size())
	}
	res = push(t, e, info, apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(9), "v": "x"}})
	if res[0].Status != "applied" {
		t.Fatalf("the corrected retry under the free key answered %+v, want applied", res[0])
	}
}

// A conflict that applies nothing stores nothing; a last write wins merge
// that writes is an application and is bound to its body.
func TestKeyBodyConflictLegs(t *testing.T) {
	t.Run("server wins conflict stores nothing", func(t *testing.T) {
		e, _, led, _, info := ledgerFixture(t, PolicyServerWins)
		push(t, e, info,
			apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "v": "a"}},
			apigen.SyncPushOperation{ClientId: key(2), Op: "update", Payload: map[string]any{"id": rowID(1), "v": "b"}})
		before := led.size()
		res := push(t, e, info, apigen.SyncPushOperation{ClientId: key(3), Op: "update", Payload: map[string]any{"id": rowID(1), "v": "c"}, BaseSeq: i64(1)})
		if res[0].Status != "conflict" {
			t.Fatalf("setup: %+v", res[0])
		}
		if led.size() != before {
			t.Fatalf("a conflict that applied nothing stored a hash")
		}
	})
	t.Run("lww merge that writes is bound", func(t *testing.T) {
		e, _, led, _, info := ledgerFixture(t, PolicyLWWByField)
		push(t, e, info,
			apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "a": "1"}},
			apigen.SyncPushOperation{ClientId: key(2), Op: "update", Payload: map[string]any{"id": rowID(1), "a": "2"}})
		merged := apigen.SyncPushOperation{ClientId: key(3), Op: "update", Payload: map[string]any{"id": rowID(1), "b": "x"}, BaseSeq: i64(1)}
		res := push(t, e, info, merged)
		if res[0].Status != "applied" || res[0].Conflict == nil {
			t.Fatalf("setup, want an applied merge: %+v", res[0])
		}
		if _, ok, _ := led.BodyHash(context.Background(), testFilter, info.Name, key(3)); !ok {
			t.Fatalf("the merge that wrote stored no hash")
		}
		again := merged
		again.Payload = map[string]any{"id": rowID(1), "b": "different"}
		wantKeyReused(t, push(t, e, info, again)[0])
	})
}

// Keys applied before the ledger existed have no stored hash: they answer
// as a plain replay whatever the body, are documented as such, and the
// replay does not invent a hash for them.
func TestKeyBodyKeyWithNoStoredHashIsAPlainReplay(t *testing.T) {
	e, store, info, _ := newEngineFixture(t, PolicyServerWins)
	// Applied through an engine with no ledger wired: the pre-migration world.
	push(t, e, info, apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "v": "a"}})
	led := newMemLedger()
	e.WithKeyLedger(led)
	row, _, _ := store.Load(context.Background(), testFilter, rowID(1))
	res := push(t, e, info, apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "v": "different"}})
	if res[0].Status != "applied" || res[0].Seq == nil || *res[0].Seq != row.Seq {
		t.Fatalf("legacy key answered %+v, want a plain applied replay at seq %d", res[0], row.Seq)
	}
	after, _, _ := store.Load(context.Background(), testFilter, rowID(1))
	if after.Seq != row.Seq || after.Data["v"] != "a" {
		t.Fatalf("legacy replay applied: %+v", after)
	}
	if led.size() != 0 {
		t.Fatalf("a legacy replay stored a hash it cannot know is the first body")
	}
}

// No ledger wired means no body check (as no audit means no audit rows):
// the engine keeps today's three guarantees.
func TestKeyBodyUnwiredLedgerKeepsTodaysBehavior(t *testing.T) {
	e, _, info, _ := newEngineFixture(t, PolicyServerWins)
	push(t, e, info, apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "v": "a"}})
	res := push(t, e, info, apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "v": "b"}})
	if res[0].Status != "applied" {
		t.Fatalf("unwired ledger answered %+v", res[0])
	}
}

// A ledger fault is an operational fault, never a verdict: the whole push
// fails and the client retries it whole, where applied keys dedup.
func TestKeyBodyLedgerFaultsFailTheWholePush(t *testing.T) {
	boom := errors.New("ledger down")
	t.Run("read on replay", func(t *testing.T) {
		e, _, led, _, info := ledgerFixture(t, PolicyServerWins)
		op := apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1)}}
		push(t, e, info, op)
		led.readErr = boom
		if _, err := e.Push(context.Background(), info, testFilter, []apigen.SyncPushOperation{op}); !errors.Is(err, boom) {
			t.Fatalf("err %v, want the ledger fault", err)
		}
	})
	t.Run("write after apply", func(t *testing.T) {
		e, _, led, _, info := ledgerFixture(t, PolicyServerWins)
		led.saveErr = boom
		op := apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1)}}
		if _, err := e.Push(context.Background(), info, testFilter, []apigen.SyncPushOperation{op}); !errors.Is(err, boom) {
			t.Fatalf("err %v, want the ledger fault", err)
		}
		// The retry whole: the row applied, the key has no hash, so the
		// retry is a plain replay and the push now succeeds.
		led.saveErr = nil
		if res := push(t, e, info, op); res[0].Status != "applied" {
			t.Fatalf("retry answered %+v", res[0])
		}
	})
}

// The canonical form is fixed and documented; these are its exact bytes
// and digest, so a change to either is a deliberate, visible act.
func TestKeyBodyCanonicalForm(t *testing.T) {
	op := apigen.SyncPushOperation{
		ClientId: key(1), Op: "update", Entity: str("pick_task"),
		Payload: map[string]any{"z": 1.0, "id": rowID(1), "m": map[string]any{"b": true, "a": []any{nil, "<&>", 2.5}}},
		BaseSeq: i64(7),
	}
	want := `{"baseSeq":7,"entity":"pick_task","op":"update","payload":{"id":"` + rowID(1) +
		`","m":{"a":[null,"<&>",2.5],"b":true},"z":1},"scope":"pick_tasks"}`
	got, err := canonicalOp("pick_tasks", op)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	if got != want {
		t.Fatalf("canonical form\n got %s\nwant %s", got, want)
	}
	// Absent entity and base_seq are explicit nulls, as the client writes them.
	got, _ = canonicalOp("pick_tasks", apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{}})
	if want := `{"baseSeq":null,"entity":null,"op":"create","payload":{},"scope":"pick_tasks"}`; got != want {
		t.Fatalf("canonical form\n got %s\nwant %s", got, want)
	}
	// The key is not part of the body; the scope is.
	a, _ := opHash("pick_tasks", op)
	op.ClientId = key(2)
	b, _ := opHash("pick_tasks", op)
	c, _ := opHash("quotes", op)
	if a != b || a == c || len(a) != 64 {
		t.Fatalf("hash key independence or scope binding broken: %s %s %s", a, b, c)
	}
}

func i64(n int64) *int64   { return &n }
func str(s string) *string { return &s }
