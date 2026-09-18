package identity

import (
	"context"
	"errors"
	"testing"
	"time"
)

// knobFake is the injectable knob source: tests flip its answer.
type knobFake struct {
	hours int
	err   error
	calls int
}

func (k *knobFake) DriverSessionHours(context.Context, string) (int, error) {
	k.calls++
	return k.hours, k.err
}

// Per-role selection: a non-driver role keeps the standard staff session
// without ever reading the knob, even when no source is wired.
func TestSessionCeilingNonDriverNeverReadsKnob(t *testing.T) {
	knob := &knobFake{}
	ceiling, err := SessionCeiling(context.Background(), "inside_sales", "org-1", knob)
	if err != nil || ceiling != StaffSessionTTL {
		t.Fatalf("non-driver ceiling = %v err=%v", ceiling, err)
	}
	ceiling, err = SessionCeiling(context.Background(), "general_manager", "org-1", nil)
	if err != nil || ceiling != StaffSessionTTL {
		t.Fatalf("non-driver with no source: %v err=%v", ceiling, err)
	}
	if knob.calls != 0 {
		t.Fatalf("non-driver arm read the knob %d times", knob.calls)
	}
}

// The driver arm: the knob is the ceiling inside its contract, and every
// arm that cannot compute a policy refuses (never guesses, never falls
// back).
func TestSessionCeilingDriverArms(t *testing.T) {
	ctx := context.Background()
	if _, err := SessionCeiling(ctx, RoleDriver, "org-1", nil); err == nil {
		t.Fatalf("unwired source computed a ceiling")
	}
	if _, err := SessionCeiling(ctx, RoleDriver, "org-1", &knobFake{err: errors.New("unreadable")}); err == nil {
		t.Fatalf("unreadable knob computed a ceiling")
	}
	for _, hours := range []int{0, -1, 169, 1000} {
		if _, err := SessionCeiling(ctx, RoleDriver, "org-1", &knobFake{hours: hours}); err == nil {
			t.Fatalf("out-of-contract knob %d computed a ceiling", hours)
		}
	}
	for _, hours := range []int{MinDriverSessionHours, 12, MaxDriverSessionHours} {
		ceiling, err := SessionCeiling(ctx, RoleDriver, "org-1", &knobFake{hours: hours})
		if err != nil {
			t.Fatalf("in-contract knob %d: %v", hours, err)
		}
		if ceiling != time.Duration(hours)*time.Hour {
			t.Fatalf("knob %d hours produced ceiling %v", hours, ceiling)
		}
	}
	if _, err := SessionCeiling(ctx, "", "org-1", nil); err == nil {
		t.Fatalf("empty role computed a ceiling")
	}
}

// The window comparison, both directions at both knob edges, with the
// boundary itself exact in both directions.
func TestSessionWithinCeilingBothDirections(t *testing.T) {
	iat := time.Unix(1_700_000_000, 0).UTC()
	exp := iat.Add(2 * time.Hour)
	if !SessionWithinCeiling(iat, exp, 2*time.Hour) {
		t.Fatalf("window exactly at the ceiling refused")
	}
	if SessionWithinCeiling(iat, exp.Add(time.Second), 2*time.Hour) {
		t.Fatalf("window one second over the ceiling admitted")
	}
	if !SessionWithinCeiling(iat, exp.Add(-time.Second), 2*time.Hour) {
		t.Fatalf("window under the ceiling refused")
	}
	if !SessionWithinCeiling(iat, iat.Add(time.Hour), time.Hour) {
		t.Fatalf("edge 1: a one-hour window against a one-hour ceiling refused")
	}
	if SessionWithinCeiling(iat, iat.Add(time.Hour+time.Second), time.Hour) {
		t.Fatalf("edge 1: over admitted")
	}
	if !SessionWithinCeiling(iat, iat.Add(168*time.Hour), 168*time.Hour) {
		t.Fatalf("edge 168: a week-long window against the backstop refused")
	}
	if SessionWithinCeiling(iat, iat.Add(168*time.Hour+time.Second), 168*time.Hour) {
		t.Fatalf("edge 168: over admitted")
	}
}
