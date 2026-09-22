package main

import (
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dashboard"
)

// w6aBranchScope is the one place the cockpit's branch predicate is encoded, and its middle case is
// the security-relevant one: a caller with NO branch grants must measure NOTHING. Sending nil there
// would make the SQL predicate `NULL IS NULL`, true, and hand that caller every branch's figures
// while looking exactly like a working dashboard.
//
// internal/dashboard cannot test this: it never sees the SQL and never sees this function. So it is
// tested here, next to the adapters, in the same package.
func TestW6ABranchScope(t *testing.T) {
	t.Run("a GLOBAL caller sends NULL, no branch filter", func(t *testing.T) {
		got := w6aBranchScope(dashboard.Scope{AllBranches: true})
		if got != nil {
			t.Fatalf("got %#v, want nil (SQL NULL)", got)
		}
		// Even with a stale branch list present, AllBranches wins, a Tier-1 GLOBAL caller is not
		// narrowed by leftover grants.
		got = w6aBranchScope(dashboard.Scope{AllBranches: true, BranchIDs: []string{"b1"}})
		if got != nil {
			t.Fatalf("got %#v, want nil for a GLOBAL caller carrying grants", got)
		}
	})

	t.Run("a scoped caller sends exactly their granted branches", func(t *testing.T) {
		got, ok := w6aBranchScope(dashboard.Scope{BranchIDs: []string{"b1", "b2"}}).([]string)
		if !ok {
			t.Fatalf("got %T, want []string", got)
		}
		if len(got) != 2 || got[0] != "b1" || got[1] != "b2" {
			t.Fatalf("got %v, want [b1 b2]", got)
		}
	})

	t.Run("a caller with NO grants sends an EMPTY ARRAY, never nil", func(t *testing.T) {
		// This is the whole reason the function exists. ANY('{}') is false for every row, so this
		// caller measures nothing; nil would mean "no filter" and give them everything.
		got := w6aBranchScope(dashboard.Scope{})
		arr, ok := got.([]string)
		if !ok {
			t.Fatalf("got %#v (%T), want an empty []string, nil here leaks every branch", got, got)
		}
		if len(arr) != 0 {
			t.Fatalf("got %v, want an empty slice", arr)
		}
		// THE ASSERTION THAT ACTUALLY BITES, and it took a surviving mutant to find it.
		//
		// `got == nil` is NOT enough: an `any` holding a typed nil slice ([]string(nil)) is not the
		// nil interface, so that check passes for the very bug it was meant to catch. pgx, however,
		// encodes a nil slice as SQL NULL, so a nil []string here silently becomes "no branch
		// filter" and hands a caller with no grants every branch in the company. The slice header
		// itself must be non-nil.
		if arr == nil {
			t.Fatal("a caller with no grants got a NIL slice, pgx encodes that as SQL NULL, which means NO BRANCH FILTER")
		}
	})

	t.Run("an explicitly empty grant list behaves the same as a nil one", func(t *testing.T) {
		got := w6aBranchScope(dashboard.Scope{BranchIDs: []string{}})
		arr, ok := got.([]string)
		if !ok || len(arr) != 0 {
			t.Fatalf("got %#v, want an empty []string", got)
		}
	})
}

// The "today" tiles compare against a date literal. It must be the scope's clock, not the process's,
// so a pinned clock in a test (or a replayed request) measures the day it names.
func TestW6ADayUsesTheScopeClock(t *testing.T) {
	sc := dashboard.Scope{Now: time.Date(2026, time.August, 6, 23, 59, 0, 0, time.UTC)}
	if got := w6aDay(sc); got != "2026-08-06" {
		t.Fatalf("w6aDay = %q, want 2026-08-06", got)
	}
}

func TestW6APluralReadsAsEnglish(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{0, "0 picks waiting"},
		{1, "1 pick waiting"},
		{2, "2 picks waiting"},
	} {
		if got := w6aPlural(tc.n, "pick waiting", "picks waiting"); got != tc.want {
			t.Fatalf("w6aPlural(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
