//go:build finworld

package main

// The anchor (W2-1b part 2 round 3 addendum): the plan's year month comes from --anchor, not a
// hardcoded literal, so one source (FINWORLD_ANCHOR through run.sh) sets it for the day walk, the
// tie check and the day list. A different anchor builds a different plan.

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestFinWorldAnchorFlagBuildsDifferentPlans(t *testing.T) {
	getenv := func(string) string { return "" }
	var out bytes.Buffer
	if err := finworldMain([]string{"days", "--anchor", "2026-06"}, &out, io.Discard, getenv); err != nil {
		t.Fatalf("finworld days --anchor 2026-06 refused: %v", err)
	}
	june := out.String()
	out.Reset()
	if err := finworldMain([]string{"days", "--anchor", "2026-09"}, &out, io.Discard, getenv); err != nil {
		t.Fatalf("finworld days --anchor 2026-09 refused: %v", err)
	}
	september := out.String()
	if june == september {
		t.Fatalf("a different anchor built the same day list (%d bytes both)", len(june))
	}
	// The anchor is the world's last month: 2026-06 lays out 2025-07 through 2026-06.
	if !strings.Contains(june, "2025-07") {
		t.Fatalf("the 2026-06 anchor's day list carries no 2025-07 day: %s", finworldAnchorFirstLine(june))
	}
	if !strings.Contains(september, "2025-10") {
		t.Fatalf("the 2026-09 anchor's day list carries no 2025-10 day: %s", finworldAnchorFirstLine(september))
	}

	// The default anchor is the world's own 2026-09.
	out.Reset()
	if err := finworldMain([]string{"days"}, &out, io.Discard, getenv); err != nil {
		t.Fatalf("finworld days refused: %v", err)
	}
	if out.String() != september {
		t.Fatalf("the default anchor is not 2026-09 (default %d bytes, explicit %d bytes)", out.Len(), len(september))
	}

	// A malformed anchor is refused, not silently defaulted.
	if err := finworldMain([]string{"days", "--anchor", "2026-13"}, io.Discard, io.Discard, getenv); err == nil {
		t.Fatalf("anchor 2026-13 must be refused")
	}
	if err := finworldMain([]string{"days", "--anchor", "junk"}, io.Discard, io.Discard, getenv); err == nil {
		t.Fatalf("anchor junk must be refused")
	}
}

func finworldAnchorFirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
