package sync

import (
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

// U+2028 and U+2029 are the two characters Go's encoder always escapes and
// JavaScript's JSON.stringify writes literally; the canonical form writes
// them literally, so the common text matches the client's string. The
// characters are built from their code points, never typed, so no editor or
// tool can turn the test into a no-op.
func TestKeyBodyCanonicalWritesLineAndParagraphSeparatorsLiterally(t *testing.T) {
	ls, ps := string(rune(0x2028)), string(rune(0x2029))
	op := apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"note": "a" + ls + "b" + ps + "c"}}
	got, err := canonicalOp("pick_tasks", op)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	if !strings.Contains(got, "a"+ls+"b"+ps+"c") || strings.Contains(got, "\\u2028") || strings.Contains(got, "\\u2029") {
		t.Fatalf("separators not written literally: %q", got)
	}
	// A payload holding the TEXT backslash u 2 0 2 8 is not a separator: its
	// backslash is escaped by the encoder and must survive as text.
	text := "x" + "\\" + "u2028" + "y"
	op.Payload = map[string]any{"note": text}
	got, err = canonicalOp("pick_tasks", op)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	if strings.Contains(got, ls) || !strings.Contains(got, "x"+"\\\\"+"u2028y") {
		t.Fatalf("literal escape text mistaken for a separator: %q", got)
	}
}
