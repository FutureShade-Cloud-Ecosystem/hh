package messaging

import (
	"context"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// TestUX130AckIDShapeRefusesEveryNonCanonicalForm: the acks body check refuses every message_id form Postgres would
// reject, so a bad id is one refused operation and never a failed push (PR 786 Opus read, P3-1). uuid.Validate alone
// accepts the urn:uuid: and braced forms; the canonical 36 character check the prefs store holds does not.
func TestUX130AckIDShapeRefusesEveryNonCanonicalForm(t *testing.T) {
	s := &acksSyncStore{}
	refused := []string{
		"not-a-uuid",
		"urn:uuid:5e1d0a3c-4f2b-4c8e-9a6d-1b2c3d4e5f60",
		"{5e1d0a3c-4f2b-4c8e-9a6d-1b2c3d4e5f60}",
		"5e1d0a3c4f2b4c8e9a6d1b2c3d4e5f60",
	}
	for _, id := range refused {
		err := s.ValidateCreate(context.Background(), sync.ScopeFilter{}, map[string]any{"message_id": id, "method": "TAP"})
		if err == nil || !errors.Is(err, sync.ErrOpRefused) {
			t.Errorf("message_id %q: want a refused operation, got %v", id, err)
		}
	}
	for _, id := range []string{"5e1d0a3c-4f2b-4c8e-9a6d-1b2c3d4e5f60", "5E1D0A3C-4F2B-4C8E-9A6D-1B2C3D4E5F60"} {
		if err := s.ValidateCreate(context.Background(), sync.ScopeFilter{}, map[string]any{"message_id": id, "method": "TAP"}); err != nil {
			t.Errorf("canonical message_id %q: want no refusal, got %v", id, err)
		}
	}
}
