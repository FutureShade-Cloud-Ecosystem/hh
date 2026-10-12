package sync

import (
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

// U+2028 and U+2029 are the two characters Go's encoder always escapes and
// JavaScript's JSON.stringify writes literally; the canonical form writes
// them literally, so the common text matches the client's string.
func TestKeyBodyCanonicalWritesLineAndParagraphSeparatorsLiterally(t *testing.T) {
	op := apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"note": "a b c"}}
	got, err := canonicalOp("pick_tasks", op)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	if !strings.Contains(got, "a b c") || strings.Contains(got, ` `) || strings.Contains(got, ` `) {
		t.Fatalf("separators not written literally: %q", got)
	}
}
