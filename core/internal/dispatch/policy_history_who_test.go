package dispatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// testBranch is the Trenton id every dispatch test uses.
const testBranch = "5d1b3f6e-0000-4000-9000-000000000001"

// fakeTrail is the audit READ seam, holding whatever rows a case wants the history to walk.
type fakeTrail struct{ entries []audit.Entry }

func (f *fakeTrail) ReopenTransaction(context.Context, string, string) ([]audit.Entry, error) {
	return f.entries, nil
}

// trailRow builds one recorded knob-change in the field/before/after shape SavePolicy writes.
func trailRow(actorID, role, field, before, after string) audit.Entry {
	return audit.Entry{
		ActorID:   actorID,
		ActorRole: role,
		CreatedAt: "2026-09-01T10:00:00Z",
		Changes: map[string]any{
			"field": field, "before": before, "after": after, "actor_role": role,
		},
	}
}

// historyHarness is a manager-gated service with a fake trail attached, ready for a history read.
func historyHarness(t *testing.T) (*Service, *fakeTrail) {
	t.Helper()
	at, _ := time.Parse(time.RFC3339, "2026-09-01T12:00:00Z")
	trail := &fakeTrail{}
	svc := NewService(nil).
		withCaller(func(context.Context) (identity.StaffContext, bool) {
			return lbCaller(identity.TierManager, "LOGISTICS_MANAGER"), true
		}).
		WithClock(func() time.Time { return at }).
		WithAuditTrail(trail)
	return svc, trail
}

// policy_history_who_test.go, item 4a, the dispatch half.
//
// THE RULING THIS REVERSES, AND WHAT IT KEEPS. service_loadboard.go chose to record `actor_role` and
// show a role word, on the argument that it answers "who is allowed to have done this", "without
// putting an identifier on a screen". The brief asks for the person's display name with the role in
// parentheses instead.
//
// The intent of the original ruling is kept EXACTLY: the id still never leaves the server. The name
// is resolved here, where `e.ActorID` already was, and only the resolved words go on the wire. What
// changed is that a screen now says "Alex Morgan (General Manager)" instead of "the general manager"
//, a name, not an identifier.
//
// THE LADDER IS THE POINT. This screen worked before the name seam existed and must keep working if
// it is unwired, if it errors, or if it cannot name a particular actor. Every rung below is a real
// answer somebody could read, and none of them is blank.

type fakeNamer struct {
	names map[string]string
	err   error
	calls int
	got   [][]string
}

func (f *fakeNamer) ActorNames(_ context.Context, ids []string) (map[string]string, error) {
	f.calls++
	f.got = append(f.got, append([]string(nil), ids...))
	if f.err != nil {
		return nil, f.err
	}
	return f.names, nil
}

func TestWhoWord_TheLadderNeverBlanks(t *testing.T) {
	for _, tc := range []struct{ name, role, want string }{
		{"Alex Morgan", "GENERAL_MANAGER", "Alex Morgan (General Manager)"},
		{"Alex Morgan", "", "Alex Morgan"},
		{"  Alex Morgan  ", "YARD_TEAM", "Alex Morgan (Yard Team)"},
		// "a dispatch manager", not "a logistics manager", dispatch's actorWord deliberately says
		// what somebody reading a change history would say, and TestActorWord_NeverPrintsARoleCode has
		// pinned that wording since session A. The ladder falls back to THAT, not to a new phrasing.
		{"", "LOGISTICS_MANAGER", "a dispatch manager"},
		{"", "", "somebody"},
		// A role the titles map has never heard of degrades honestly rather than SHOUTING.
		{"Sam Rivera", "FUTURE_ROLE_NOBODY_ADDED", "Sam Rivera (Future role nobody added)"},
	} {
		if got := whoWord(tc.name, tc.role); got != tc.want {
			t.Errorf("whoWord(%q, %q) = %q, want %q", tc.name, tc.role, got, tc.want)
		}
	}
}

// Every role the roster can hold must have a title, or a real person's real role reaches a screen
// as SHOUTING_SNAKE_CASE. Pinned by SIZE too, so a role added to the product without a title here
// fails this test rather than shipping.
func TestRoleTitle_CoversTheRoster(t *testing.T) {
	if len(roleTitles) != 14 {
		t.Fatalf("roleTitles holds %d roles; migrations 0015/0043 seed 14. A role added to the product "+
			"needs a title here or it reaches a settings history as SHOUTING_SNAKE_CASE.", len(roleTitles))
	}
	for code, title := range roleTitles {
		if title == "" {
			t.Errorf("%s has an empty title", code)
		}
		// A title that IS its own code is usually a placeholder somebody forgot, except where the
		// code is already how people say it out loud. HR is the only such role, and spelling it
		// "Hr" to satisfy a rule would be worse than the rule.
		if title == code && code != "HR" {
			t.Errorf("%s is titled as its own code", code)
		}
	}
	// And the default arm is reached, not dead.
	if got := roleTitle("SOMETHING_NEW"); got != "Something new" {
		t.Fatalf("the default arm returned %q, it must de-underscore and sentence-case", got)
	}
	if got := roleTitle(""); got != "" {
		t.Fatalf("an absent role must produce an empty title, got %q", got)
	}
}

// changeStr closes the "<nil>" trap. audit's put() drops empty values, so a knob whose previous value
// was the empty string writes a row with NO `before` key, and fmt.Sprint of a missing map entry is
// the four characters "<nil>". Dispatch was safe by accident; this makes it safe on purpose.
func TestChangeStr_AMissingValueIsEmptyNotNil(t *testing.T) {
	var missing any // exactly what a map lookup of an absent key yields
	if got := changeStr(missing); got != "" {
		t.Fatalf("a missing value rendered as %q, a history that says a setting used to be "+
			"\"<nil>\" is a history reporting a bug in itself", got)
	}
	for _, tc := range []struct {
		in   any
		want string
	}{
		{"MANAGER_UP", "MANAGER_UP"},
		{float64(1250), "1250"}, // the jsonb round-trip, never "1.25e+03"
		{true, "true"},
		{7, "7"},
		{int64(9), "9"},
	} {
		if got := changeStr(tc.in); got != tc.want {
			t.Errorf("changeStr(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// THE SEAM IS A BATCH, and a history page must be ONE name read rather than one per row. A per-row
// lookup is the difference between a page and N queries, and it is the reason the interface takes a
// slice at all.
func TestPolicyHistory_ResolvesEveryNameInOneCall(t *testing.T) {
	svc, trail := historyHarness(t)
	namer := &fakeNamer{names: map[string]string{
		"user-a": "Alex Morgan",
		"user-b": "Sam Rivera",
	}}
	svc.WithActorNames(namer)
	trail.entries = []audit.Entry{
		trailRow("user-a", "GENERAL_MANAGER", "working_view_days", "7", "14"),
		trailRow("user-b", "SALES_MANAGER", "commit_horizon_weeks", "2", "3"),
		trailRow("user-a", "GENERAL_MANAGER", "load_value_visibility", "ALL_STAFF", "MANAGER_UP"),
	}

	rows, err := svc.PolicyHistory(context.Background(), testBranch, 0)
	if err != nil {
		t.Fatalf("PolicyHistory: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if namer.calls != 1 {
		t.Fatalf("the namer was called %d times for a 3-row page, it must be ONE batch read", namer.calls)
	}
	if len(namer.got[0]) != 2 {
		t.Fatalf("asked for %d ids across 3 rows by 2 people, duplicates must be collapsed: %v",
			len(namer.got[0]), namer.got[0])
	}
	// Newest first, and every row names a person.
	if rows[0].Who != "Alex Morgan (General Manager)" {
		t.Fatalf("row 0 who = %q, want a name with the role", rows[0].Who)
	}
	if rows[1].Who != "Sam Rivera (Sales Manager)" {
		t.Fatalf("row 1 who = %q", rows[1].Who)
	}
	// AND THE ID IS NOWHERE. The original ruling's intent, kept.
	for _, r := range rows {
		if r.Who == "user-a" || r.Who == "user-b" {
			t.Fatalf("an actor id reached the wire: %q", r.Who)
		}
	}
}

// The degradation that matters most: the seam is there and FAILS. Every row must still carry the
// role word this screen showed before item 4a, not a blank, not an error, not a uuid.
func TestPolicyHistory_ANameFailureFallsBackToTheRoleWord(t *testing.T) {
	svc, trail := historyHarness(t)
	svc.WithActorNames(&fakeNamer{err: errors.New("directory unavailable")})
	trail.entries = []audit.Entry{
		trailRow("user-a", "GENERAL_MANAGER", "load_value_visibility", "ALL_STAFF", "MANAGER_UP"),
	}

	rows, err := svc.PolicyHistory(context.Background(), testBranch, 0)
	if err != nil {
		t.Fatalf("a name failure must not fail the history: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Who != "the general manager" {
		t.Fatalf("who = %q, want the role word this screen showed before the name seam existed", rows[0].Who)
	}
}

// And with NO seam wired at all, the state every deployment is in until main.go injects one.
func TestPolicyHistory_AnUnwiredNamerIsTheOldBehaviour(t *testing.T) {
	svc, trail := historyHarness(t)
	trail.entries = []audit.Entry{
		trailRow("user-a", "YARD_MANAGER", "working_view_days", "7", "14"),
	}
	rows, err := svc.PolicyHistory(context.Background(), testBranch, 0)
	if err != nil {
		t.Fatalf("PolicyHistory: %v", err)
	}
	if len(rows) != 1 || rows[0].Who != "a yard manager" {
		t.Fatalf("unwired namer produced %+v, it must be exactly the pre-item-4a behaviour", rows)
	}
}
