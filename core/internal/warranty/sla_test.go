package warranty

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// sla_test.go, DIB-223 acceptance: per-phase SLA timers driven by a FAKE CLOCK.
//
// THERE IS NO time.Sleep IN THIS FILE AND THERE MUST NEVER BE ONE. Every elapsed-time assertion moves an
// injected clock, so a 14-day vendor breach is asserted in microseconds and cannot flake. A sleep-based SLA
// test is a flake with a countdown attached.

var slaT0 = time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)

func clockAt(t time.Time) PhaseClock {
	return PhaseClock{ClaimID: "c1", ClaimNo: "WTY-1", BranchID: testBranch, Phase: PhaseTriage, PhaseSince: t}
}

// - the pure timer -

func TestSLATimer_StatesAcrossTheWindow(t *testing.T) {
	targets := defaultSLATargets() // TRIAGE = 48h
	cases := []struct {
		name      string
		at        time.Time
		want      string
		overdue   int
		remaining int
	}{
		{"fresh", slaT0, SLAStateOK, 0, 48},
		{"halfway", slaT0.Add(24 * time.Hour), SLAStateOK, 0, 24},
		// DUE_SOON begins with a quarter of the window left (12h of 48h).
		{"quarter left", slaT0.Add(36 * time.Hour), SLAStateDueSoon, 0, 12},
		{"one hour left", slaT0.Add(47 * time.Hour), SLAStateDueSoon, 0, 1},
		{"exactly due", slaT0.Add(48 * time.Hour), SLAStateBreached, 0, 0},
		{"one minute over", slaT0.Add(48*time.Hour + time.Minute), SLAStateBreached, 1, -1},
		{"a day over", slaT0.Add(72 * time.Hour), SLAStateBreached, 24, -24},
	}
	for _, c := range cases {
		got := BuildSLATimer(clockAt(slaT0), targets, c.at)
		if got.State != c.want {
			t.Errorf("%s: want %s got %s", c.name, c.want, got.State)
		}
		if got.OverdueHours != c.overdue {
			t.Errorf("%s: overdue want %d got %d", c.name, c.overdue, got.OverdueHours)
		}
		if got.RemainingHours != c.remaining {
			t.Errorf("%s: remaining want %d got %d", c.name, c.remaining, got.RemainingHours)
		}
	}
}

func TestSLATimer_OneMinuteOverIsOneHourOverdueNotZero(t *testing.T) {
	// Rounding overdue hours DOWN would report 0 for a real breach, and a breach reported as zero is a breach
	// that gets ignored on a board sorted by severity.
	got := BuildSLATimer(clockAt(slaT0), defaultSLATargets(), slaT0.Add(48*time.Hour+time.Second))
	if got.State != SLAStateBreached || got.OverdueHours != 1 {
		t.Fatalf("a breach by any margin must read as at least 1h overdue; got %s/%d", got.State, got.OverdueHours)
	}
}

func TestSLATimer_DefaultsAreTheStatedOnesAndPreserveP1(t *testing.T) {
	want := map[string]int{
		PhaseIntake: 24, PhaseTriage: 48, PhaseSubmitted: 72,
		PhaseWaitingOnDecision: 336, PhaseDecision: 48, PhaseSettlement: 120,
	}
	targets := defaultSLATargets()
	for phase, hours := range want {
		got, ok := ResolveSLATarget(targets, phase, "")
		if !ok || got != hours {
			t.Errorf("%s: want %dh got %dh (ok=%v)", phase, hours, got, ok)
		}
	}
	// P1's single number was 14 days on the vendor's clock. WAITING_ON_DECISION preserves it EXACTLY, so this
	// feature adds phases rather than silently re-tuning the one measurement the desk already trusted.
	if h, _ := ResolveSLATarget(targets, PhaseWaitingOnDecision, ""); h != defaultSLADays*24 {
		t.Errorf("WAITING_ON_DECISION must preserve P1's defaultSLADays (%dd); got %dh", defaultSLADays, h)
	}
}

func TestSLATimer_VendorOverrideBeatsTheDefault(t *testing.T) {
	// "Techo answers in two weeks and Oaks answers when it feels like it", different clocks, and always were.
	targets := append(defaultSLATargets(),
		SLATarget{Phase: PhaseWaitingOnDecision, VendorID: "v-techo", TargetHours: 336, IsActive: true},
		SLATarget{Phase: PhaseWaitingOnDecision, VendorID: "v-oaks", TargetHours: 1008, IsActive: true},
	)
	if h, _ := ResolveSLATarget(targets, PhaseWaitingOnDecision, "v-oaks"); h != 1008 {
		t.Errorf("a vendor row must beat the default; got %dh", h)
	}
	if h, _ := ResolveSLATarget(targets, PhaseWaitingOnDecision, "v-unknown"); h != 336 {
		t.Errorf("an unlisted vendor falls back to the default; got %dh", h)
	}
	// An INACTIVE vendor row is not an override, it is a row someone turned off.
	targets = append(targets, SLATarget{Phase: PhaseTriage, VendorID: "v-x", TargetHours: 9999, IsActive: false})
	if h, _ := ResolveSLATarget(targets, PhaseTriage, "v-x"); h != 48 {
		t.Errorf("an inactive vendor row must not override; got %dh", h)
	}
}

func TestSLATimer_UnconfiguredPhaseIsNoTargetNotAFakeDeadline(t *testing.T) {
	got := BuildSLATimer(clockAt(slaT0), []SLATarget{}, slaT0.Add(1000*time.Hour))
	if got.State != SLAStateNoTarget {
		t.Fatalf("an untimed phase must say NO_TARGET, never invent a deadline; got %s", got.State)
	}
	if got.DueAt != "" {
		t.Errorf("an untimed phase must have no due date; got %q", got.DueAt)
	}
}

func TestSLATimers_SortWorstFirst(t *testing.T) {
	targets := defaultSLATargets()
	now := slaT0.Add(100 * time.Hour)
	clocks := []PhaseClock{
		{ClaimID: "ok", ClaimNo: "WTY-3", Phase: PhaseWaitingOnDecision, PhaseSince: now.Add(-1 * time.Hour)},
		{ClaimID: "worst", ClaimNo: "WTY-1", Phase: PhaseTriage, PhaseSince: slaT0, ExpectedCents: 100},
		{ClaimID: "soon", ClaimNo: "WTY-2", Phase: PhaseTriage, PhaseSince: now.Add(-40 * time.Hour)},
	}
	got := BuildSLATimers(clocks, targets, now)
	if got[0].ClaimID != "worst" || got[0].State != SLAStateBreached {
		t.Fatalf("breached claims must sort first; got %+v", got[0])
	}
	if got[1].State != SLAStateDueSoon {
		t.Errorf("due-soon must follow breached; got %s", got[1].State)
	}
}

// - the sweep, the alert, and the flag-off notification -

// slaSvc builds a service whose clock the test controls, with one claim parked in TRIAGE.
func slaSvc(t *testing.T) (*Service, *fakeStore, *time.Time, string) {
	t.Helper()
	svc, fs, _ := newSvc()
	now := slaT0
	svc.withClock(func() time.Time { return now })
	ctx := ctxWith(staff("u1"))
	c, err := svc.CreateClaim(ctx, ClaimInput{BranchID: testBranch, Description: "spalling"})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"contact_captured", "product_identified", "description_captured"} {
		if _, err := svc.TickChecklist(ctx, c.ID, ChecklistTickInput{ItemKey: k, Checked: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.AdvancePhase(ctx, c.ID, PhaseTransitionInput{ToPhase: PhaseTriage}); err != nil {
		t.Fatal(err)
	}
	return svc, fs, &now, c.ID
}

func TestSLASweep_RaisesNothingBeforeTheDeadlineAndAnAlertAfter(t *testing.T) {
	svc, fs, now, id := slaSvc(t)
	ctx := ctxWith(staff("u1"))

	// 47 hours into a 48-hour TRIAGE window: nothing is overdue.
	*now = slaT0.Add(47 * time.Hour)
	raised, err := svc.SweepSLA(ctx, testBranch)
	if err != nil {
		t.Fatal(err)
	}
	if len(raised) != 0 {
		t.Fatalf("nothing is overdue at 47h of a 48h target; raised %d", len(raised))
	}

	// One hour later, the clock MOVES, nothing sleeps, the breach is real and durable.
	*now = slaT0.Add(49 * time.Hour)
	raised, err = svc.SweepSLA(ctx, testBranch)
	if err != nil {
		t.Fatal(err)
	}
	if len(raised) != 1 || raised[0].Phase != PhaseTriage {
		t.Fatalf("a breached TRIAGE claim must raise one alert; got %+v", raised)
	}
	if raised[0].OverdueHours != 1 {
		t.Errorf("overdue hours wrong: %d", raised[0].OverdueHours)
	}
	a, ok := fs.alert(id, PhaseTriage)
	if !ok || a.NotifyStatus != NotifySuppressed {
		t.Fatalf("the alert must be durable and, unbound, SUPPRESSED; got %+v", a)
	}
}

func TestSLASweep_IsIdempotentPerClaimAndPhase(t *testing.T) {
	svc, fs, now, id := slaSvc(t)
	ctx := ctxWith(staff("u1"))
	*now = slaT0.Add(50 * time.Hour)

	first, _ := svc.SweepSLA(ctx, testBranch)
	second, _ := svc.SweepSLA(ctx, testBranch)
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("each sweep reports the breach; got %d then %d", len(first), len(second))
	}
	if first[0].ID != second[0].ID {
		t.Error("a repeated sweep must UPDATE the alert, not accumulate duplicates a desk dismisses twice")
	}
	// And the overdue count advances with the clock.
	*now = slaT0.Add(60 * time.Hour)
	third, _ := svc.SweepSLA(ctx, testBranch)
	if third[0].OverdueHours != 12 {
		t.Errorf("overdue hours must track the clock; got %d", third[0].OverdueHours)
	}
	if a, _ := fs.alert(id, PhaseTriage); a.OverdueHours != 12 {
		t.Errorf("the stored alert must track too; got %d", a.OverdueHours)
	}
}

func TestSLASweep_AcknowledgementSurvivesARepeatSweep(t *testing.T) {
	svc, fs, now, id := slaSvc(t)
	ctx := ctxWith(staff("u1"))
	*now = slaT0.Add(50 * time.Hour)
	raised, _ := svc.SweepSLA(ctx, testBranch)

	if err := svc.AcknowledgeSLAAlert(ctx, id, raised[0].ID); err != nil {
		t.Fatal(err)
	}
	*now = slaT0.Add(70 * time.Hour)
	if _, err := svc.SweepSLA(ctx, testBranch); err != nil {
		t.Fatal(err)
	}
	// Re-raising an acknowledged-but-still-overdue alert would train people to dismiss the list.
	a, _ := fs.alert(id, PhaseTriage)
	if a.AcknowledgedAt == "" {
		t.Fatal("a repeat sweep must not clear an acknowledgement")
	}
	// It is still visible to anyone who asks for the acknowledged ones, the alert is not deleted, because a
	// disappeared alert cannot be asked about at the next stand-up.
	view, _ := svc.SLABoard(ctx, testBranch, true)
	if len(view.Alerts) != 1 {
		t.Errorf("the acknowledged alert must remain readable; got %d", len(view.Alerts))
	}
	open, _ := svc.SLABoard(ctx, testBranch, false)
	if len(open.Alerts) != 0 {
		t.Errorf("an acknowledged alert must drop off the OPEN list; got %d", len(open.Alerts))
	}
}

func TestSLASweep_NotifierUnbound_AlertStillRaisedAndSuppressed(t *testing.T) {
	svc, fs, now, id := slaSvc(t)
	ctx := ctxWith(staff("u1"))
	*now = slaT0.Add(50 * time.Hour)
	if _, err := svc.SweepSLA(ctx, testBranch); err != nil {
		t.Fatal(err)
	}
	// The shipped default: the notification tier is another swarm's fence, so nothing is emitted and the alert
	// is raised anyway. An alert nobody wants to lose must not depend on a downstream service being up.
	a, ok := fs.alert(id, PhaseTriage)
	if !ok || a.NotifyStatus != NotifySuppressed || a.NotifyRef != "" {
		t.Fatalf("unbound notifier must record SUPPRESSED with no ref; got %+v", a)
	}
}

func TestSLASweep_NotifierBound_EmitsTheNamedTopicOncePerAlert(t *testing.T) {
	svc, fs, now, id := slaSvc(t)
	n := &fakeNotifier{}
	svc.WithSLANotifier(n)
	ctx := ctxWith(staff("u1"))
	*now = slaT0.Add(50 * time.Hour)
	if _, err := svc.SweepSLA(ctx, testBranch); err != nil {
		t.Fatal(err)
	}
	if len(n.events) != 1 {
		t.Fatalf("want one emission; got %d", len(n.events))
	}
	ev := n.events[0]
	// The literal is spelled out on purpose: the union adapter and REPORT-W5-B.md both name this string, and a
	// constant compared only against itself would let a rename slip through both.
	if ev.Topic != "warranty.sla.breached" || SLABreachTopic != "warranty.sla.breached" {
		t.Errorf("the union topic must be warranty.sla.breached; got %q (const %q)", ev.Topic, SLABreachTopic)
	}
	if ev.IdempotencyKey != "warranty-sla:"+id+":"+PhaseTriage {
		t.Errorf("one alert, one message, key wrong: %q", ev.IdempotencyKey)
	}
	if a, _ := fs.alert(id, PhaseTriage); a.NotifyStatus != NotifySent {
		t.Errorf("a delivered emission must record SENT; got %q", a.NotifyStatus)
	}
}

func TestSLASweep_NotifierFailure_NeverUnRaisesTheAlert(t *testing.T) {
	svc, fs, now, id := slaSvc(t)
	svc.WithSLANotifier(&fakeNotifier{err: errors.New("notify: queue down")})
	ctx := ctxWith(staff("u1"))
	*now = slaT0.Add(50 * time.Hour)
	raised, err := svc.SweepSLA(ctx, testBranch)
	if err != nil {
		t.Fatal("a notification failure must not fail the sweep")
	}
	if len(raised) != 1 {
		t.Fatalf("the alert must still be raised; got %d", len(raised))
	}
	a, _ := fs.alert(id, PhaseTriage)
	if a.NotifyStatus != NotifyFailed || !strings.Contains(a.NotifyError, "queue down") {
		t.Errorf("the failure must be recorded on the alert, not swallowed; got %+v", a)
	}
}

func TestSLABoard_CountsAndBranchScope(t *testing.T) {
	svc, _, now, _ := slaSvc(t)
	ctx := ctxWith(staff("u1"))
	*now = slaT0.Add(50 * time.Hour)

	view, err := svc.SLABoard(ctx, testBranch, false)
	if err != nil {
		t.Fatal(err)
	}
	if view.BreachedCount != 1 {
		t.Errorf("want one breached timer; got %d", view.BreachedCount)
	}
	if len(view.Targets) == 0 {
		t.Error("the board must publish the configured targets so \"why is this red\" is answerable without reading a migration")
	}
	// A staffer in another branch sees nothing, and asking explicitly is refused.
	outsider := ctxWith(staffAt("u2", otherBranch))
	other, err := svc.SLABoard(outsider, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(other.Timers) != 0 {
		t.Errorf("SLA timers must obey the branch fence; got %d", len(other.Timers))
	}
	if _, err := svc.SLABoard(outsider, testBranch, false); err == nil {
		t.Fatal("an explicit cross-branch SLA read must be forbidden")
	}
}

func TestSLASweep_TerminalClaimsAreNeverOverdue(t *testing.T) {
	svc, fs, now, id := slaSvc(t)
	ctx := ctxWith(staff("u1"))
	// Abandon it, then wind the clock far past every target.
	if _, err := svc.AdvancePhase(ctx, id, PhaseTransitionInput{ToPhase: PhaseAbandoned}); err != nil {
		t.Fatal(err)
	}
	*now = slaT0.Add(5000 * time.Hour)
	raised, err := svc.SweepSLA(ctx, testBranch)
	if err != nil {
		t.Fatal(err)
	}
	if len(raised) != 0 {
		t.Fatalf("nothing is overdue about a finished claim; raised %d", len(raised))
	}
	if _, ok := fs.alert(id, PhaseTriage); ok {
		t.Error("no alert should exist for a claim that was closed before it breached")
	}
}

func TestSLA_PhaseEntryIsDerivedFromTheAuditSpine(t *testing.T) {
	// The clock restarts when the claim ENTERS a phase, not when the claim was opened, otherwise a claim that
	// spent six days in INTAKE arrives in TRIAGE already breached through no fault of the person who just
	// picked it up.
	svc, _, now, _ := slaSvc(t) // opened AND advanced to TRIAGE at slaT0
	ctx := ctxWith(staff("u1"))
	*now = slaT0.Add(47 * time.Hour)
	view, err := svc.SLABoard(ctx, testBranch, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Timers) != 1 {
		t.Fatalf("want one timer; got %d", len(view.Timers))
	}
	if view.Timers[0].Phase != PhaseTriage || view.Timers[0].State == SLAStateBreached {
		t.Errorf("the TRIAGE clock must start at the TRIAGE transition; got %+v", view.Timers[0])
	}
	if view.Timers[0].PhaseSince != slaT0.Format(time.RFC3339) {
		t.Errorf("phase_since must come from the PHASE_CHANGE event; got %s", view.Timers[0].PhaseSince)
	}
}

// The sweep is a domain method, so a job registration is a one-line binding at the union rather than a rewrite.
var _ func(context.Context, string) ([]SLAAlert, error) = (*Service)(nil).SweepSLA
