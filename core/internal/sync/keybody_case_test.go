package sync

import (
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

// A uuid is one key whatever its letter case: the second spelling is the
// same key (refused with another body, a replay with the same), and every
// result answers with the spelling its own operation sent.
func TestKeyBodyKeyCaseIsOneKeyAndResultsEchoTheSpelling(t *testing.T) {
	e, _, led, _, info := ledgerFixture(t, PolicyServerWins)
	lower := "abcdef01-0000-4000-8000-000000000001"
	upper := strings.ToUpper(lower)
	first := push(t, e, info, apigen.SyncPushOperation{ClientId: upper, Op: "create", Payload: map[string]any{"id": rowID(1), "v": "a"}})
	if first[0].Status != "applied" || first[0].ClientId != upper {
		t.Fatalf("first: %+v, want applied echoing %s", first[0], upper)
	}
	same := push(t, e, info, apigen.SyncPushOperation{ClientId: lower, Op: "create", Payload: map[string]any{"id": rowID(1), "v": "a"}})
	if same[0].Status != "applied" || same[0].ClientId != lower {
		t.Fatalf("same body other case: %+v, want applied echoing %s", same[0], lower)
	}
	other := push(t, e, info, apigen.SyncPushOperation{ClientId: lower, Op: "create", Payload: map[string]any{"id": rowID(1), "v": "TAMPERED"}})
	wantKeyReused(t, other[0])
	if other[0].ClientId != lower {
		t.Fatalf("refusal echoes %q, want %q", other[0].ClientId, lower)
	}
	if led.size() != 1 {
		t.Fatalf("%d ledger entries, want one slot for both spellings", led.size())
	}
}
