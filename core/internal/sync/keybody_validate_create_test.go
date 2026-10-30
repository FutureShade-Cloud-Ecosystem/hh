package sync

import (
	"context"
	"fmt"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

// validatingStore is an existsStore that also implements the optional create
// body validation seam (UX-116): Create refuses an invalid body with the
// module's own words before any fence or write, and ValidateCreate answers
// the same refusal for the engine's bound gate. The two must not drift: the
// seam exists so a spent key and a free key meet the same words on an
// invalid body.
type validatingStore struct {
	*existsStore
}

func (v *validatingStore) validateCreate(fields map[string]any) error {
	if fields["v"] == "INVALID" {
		return fmt.Errorf("%w: tasks: field v is invalid", ErrOpRefused)
	}
	return nil
}

func (v *validatingStore) ValidateCreate(_ context.Context, _ ScopeFilter, fields map[string]any) error {
	return v.validateCreate(fields)
}

func (v *validatingStore) Create(ctx context.Context, filter ScopeFilter, key string, fields map[string]any) (RowState, error) {
	if err := v.validateCreate(fields); err != nil {
		return RowState{}, err
	}
	return v.existsStore.Create(ctx, filter, key, fields)
}

// UX-116: the bound gate answers before the store validates a create body, so
// an invalid body on a spent key met the gate's row not found while a free key
// met the validation refusal, and the words told the caller the key was
// spent. With the ValidateCreate seam the engine validates the body BEFORE
// the hidden bound refusal: the two keys meet the same refusal, whole
// (status, words, seq, envelope), for a caller who cannot read the row, on a
// live hidden entity and on a gone row alike. A valid body is unchanged: the
// gate's refusal stands. A store without the seam is untouched.
func TestBoundKeyInvalidCreateBodyAnswersTheValidationRefusal(t *testing.T) {
	inner := &fakeStore{}
	fs := &validatingStore{existsStore: &existsStore{fencedStore: &fencedStore{fakeStore: inner, branch: map[string]string{},
		reachFor: func(f ScopeFilter) []string {
			switch f.Actor {
			case "staff-a":
				return []string{"br-a"}
			case "staff-b":
				return []string{"br-b"}
			}
			return nil
		}}, gone: map[string]bool{}}}
	e := NewEngine()
	if err := e.RegisterScope("pick_tasks", fs); err != nil {
		t.Fatalf("register: %v", err)
	}
	led := newMemLedger()
	e.WithKeyLedger(led)
	info, ok := Lookup("pick_tasks")
	if !ok {
		t.Fatal("pick_tasks missing from the registry")
	}
	push := func(f ScopeFilter, op apigen.SyncPushOperation) apigen.SyncPushResult {
		t.Helper()
		resp, err := e.Push(context.Background(), info, f, []apigen.SyncPushOperation{op})
		if err != nil {
			t.Fatalf("push: %v", err)
		}
		return resp.Results[0]
	}
	assertSameRefusal := func(name string, bound, absent apigen.SyncPushResult) {
		t.Helper()
		if bound.Status != absent.Status || bound.Seq != absent.Seq || bound.Conflict != absent.Conflict {
			t.Errorf("%s: the spent key and the free key answer differently: spent %+v, free %+v", name, bound, absent)
		}
		boundWords, freeWords := "", ""
		if bound.Error != nil {
			boundWords = *bound.Error
		}
		if absent.Error != nil {
			freeWords = *absent.Error
		}
		if boundWords != freeWords {
			t.Errorf("%s: the error words differ: spent %q, free %q", name, boundWords, freeWords)
		}
	}
	fA := ScopeFilter{OrgID: testFilter.OrgID, Actor: "staff-a"}
	fB := ScopeFilter{OrgID: testFilter.OrgID, Actor: "staff-b"}

	op := apigen.SyncPushOperation{ClientId: key(61), Op: "create", Payload: map[string]any{"id": rowID(61), "v": "a"}}
	if first := push(fA, op); first.Status != "applied" || first.Seq == nil {
		t.Fatalf("setup: the create applies: %+v", first)
	}
	invalid := map[string]any{"id": rowID(61), "v": "INVALID"}

	// The LIVE hidden entity: an invalid body meets the validation refusal
	// under the bound key and under a free one, whole.
	boundInvalid := push(fB, apigen.SyncPushOperation{ClientId: key(61), Op: "create", Payload: invalid})
	freeInvalid := push(fB, apigen.SyncPushOperation{ClientId: key(62), Op: "create", Payload: invalid})
	assertSameRefusal("an invalid body on a live hidden entity", boundInvalid, freeInvalid)
	if boundInvalid.Status != "rejected" || boundInvalid.Error == nil || *boundInvalid.Error == ErrRowNotFound.Error() {
		t.Errorf("an invalid body answers the validation refusal, not the gate's: %+v", boundInvalid)
	}
	if len(inner.rows) != 1 {
		t.Errorf("the invalid bodies write nothing: %d rows, want 1", len(inner.rows))
	}

	// The GONE row: the validation refusal still comes first.
	fs.gone[rowID(61)] = true
	kept := inner.rows[:0]
	for _, r := range inner.rows {
		if r.id != rowID(61) {
			kept = append(kept, r)
		}
	}
	inner.rows = kept
	goneBound := push(fB, apigen.SyncPushOperation{ClientId: key(61), Op: "create", Payload: invalid})
	goneFree := push(fB, apigen.SyncPushOperation{ClientId: key(63), Op: "create", Payload: invalid})
	assertSameRefusal("an invalid body on a gone row", goneBound, goneFree)

	// A VALID body is unchanged: the gate's own refusal.
	valid := push(fB, op)
	if valid.Status != "rejected" || valid.Error == nil || *valid.Error != ErrRowNotFound.Error() {
		t.Errorf("a valid body still meets the gate's refusal: %+v", valid)
	}

	// The binder's own key with an invalid body is another body: key_reused,
	// exactly as before.
	binderInvalid := push(fA, apigen.SyncPushOperation{ClientId: key(61), Op: "create", Payload: invalid})
	wantKeyReused(t, binderInvalid)

	// The binder's valid replay keeps the bound answer; nothing writes; the
	// refusals bind nothing.
	again := push(fA, op)
	if again.Status != "applied" || again.Seq != nil {
		t.Errorf("the binder's replay answers applied with no seq: %+v", again)
	}
	if len(inner.rows) != 0 {
		t.Errorf("the replays write nothing: %d rows, want 0", len(inner.rows))
	}
	if led.size() != 1 {
		t.Errorf("the refusals bind nothing new: %d entries, want 1", led.size())
	}
}
