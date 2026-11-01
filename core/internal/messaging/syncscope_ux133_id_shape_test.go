package messaging

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// TestUX133IDGatesAnswerAMissBeforeAnyRead: with no repository behind them, a non-canonical id reaches no read at all
// (a read would panic on the nil repository), so Load and BoundEntityExists answer a miss from the shape alone.
func TestUX133IDGatesAnswerAMissBeforeAnyRead(t *testing.T) {
	f := sync.ScopeFilter{OrgID: "11111111-1111-1111-1111-111111111111", Actor: "sub-sam"}
	ids := []string{
		"",
		"not-a-uuid",
		"urn:uuid:5e1d0a3c-4f2b-4c8e-9a6d-1b2c3d4e5f60",
		"{5e1d0a3c-4f2b-4c8e-9a6d-1b2c3d4e5f60}",
		"5e1d0a3c4f2b4c8e9a6d1b2c3d4e5f60",
	}
	stores := map[string]interface {
		sync.Store
		sync.BoundEntityChecker
	}{
		"messages": &messagesSyncStore{},
		"acks":     &acksSyncStore{},
		"prefs":    &notifyPrefsSyncStore{},
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
