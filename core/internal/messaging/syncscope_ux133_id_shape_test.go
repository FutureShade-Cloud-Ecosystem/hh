package messaging

import (
	"context"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// TestUX133IDGatesAnswerAMissBeforeAnyRead: with no repository behind them, a non-canonical id reaches no read at all
// (a read would panic on the nil repository), so Load and BoundEntityExists answer a miss from the shape alone. The
// stores carry a linker that resolves the caller, so Load gets past its link step and only the shape gate answers.
func TestUX133IDGatesAnswerAMissBeforeAnyRead(t *testing.T) {
	f := sync.ScopeFilter{OrgID: "11111111-1111-1111-1111-111111111111", Actor: "sub-sam"}
	ids := []string{
		"",
		"not-a-uuid",
		"urn:uuid:5e1d0a3c-4f2b-4c8e-9a6d-1b2c3d4e5f60",
		"{5e1d0a3c-4f2b-4c8e-9a6d-1b2c3d4e5f60}",
		"5e1d0a3c4f2b4c8e9a6d1b2c3d4e5f60",
	}
	link := func(_ context.Context, sub string) (string, bool) {
		return "11111111-2222-3333-4444-555555555555", sub == "sub-sam"
	}
	stores := map[string]interface {
		sync.Store
		sync.BoundEntityChecker
	}{
		"messages": &messagesSyncStore{link: link},
		"acks":     &acksSyncStore{link: link},
		"prefs":    &notifyPrefsSyncStore{link: link},
	}
	for name, st := range stores {
		for _, id := range ids {
			if _, visible, err := st.Load(context.Background(), f, id); err != nil || visible {
				t.Errorf("%s Load(%q): want a clean miss, got visible=%v err=%v", name, id, visible, err)
			}
			if ok, err := st.BoundEntityExists(context.Background(), f, id); err != nil || ok {
				t.Errorf("%s BoundEntityExists(%q): want false and no fault, got %v err=%v", name, id, ok, err)
			}
		}
	}
}

// TestUX133MessagesValidateCreateTakesCanonicalIDs: both id fields take the canonical form only; an absent or empty
// thread_root_id stays allowed.
func TestUX133MessagesValidateCreateTakesCanonicalIDs(t *testing.T) {
	st := &messagesSyncStore{}
	room := "5e1d0a3c-4f2b-4c8e-9a6d-1b2c3d4e5f60"
	ok := []map[string]any{
		{"channel_id": room, "body": "x"},
		{"channel_id": room, "body": "x", "thread_root_id": ""},
		{"channel_id": room, "body": "x", "thread_root_id": room},
	}
	for _, p := range ok {
		if err := st.ValidateCreate(context.Background(), sync.ScopeFilter{}, p); err != nil {
			t.Errorf("%v: want accepted, got %v", p, err)
		}
	}
	bad := []map[string]any{
		{"channel_id": "urn:uuid:" + room, "body": "x"},
		{"channel_id": "not-a-uuid", "body": "x"},
		{"channel_id": room, "body": "x", "thread_root_id": "urn:uuid:" + room},
		{"channel_id": room, "body": "x", "thread_root_id": "{" + room + "}"},
		{"channel_id": room, "body": "x", "thread_root_id": "not-a-uuid"},
	}
	for _, p := range bad {
		err := st.ValidateCreate(context.Background(), sync.ScopeFilter{}, p)
		if err == nil || !errors.Is(err, sync.ErrOpRefused) {
			t.Errorf("%v: want an op refusal, got %v", p, err)
		}
	}
}
