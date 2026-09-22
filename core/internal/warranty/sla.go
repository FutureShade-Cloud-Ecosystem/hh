package warranty

import (
	"context"
	"sort"
	"strings"
	"time"
)

// sla.go, DIB-223 WTY-SLA: per-phase SLA timers, overdue alerts, and the flag-off notification seam.
//
// WHAT P1 MEASURED. One number, 14 days from entering SUBMITTED, and nothing else. That watches the vendor,
// which is the one party whose delay everybody already knows about. The claims that actually rot are the ones
// that sat in TRIAGE for three weeks because no one owned them, and P1 was blind to those by construction.
//
// THE CLOCK IS INJECTED. Every reading here comes from s.clock(). There is no time.Sleep in this feature and
// there must never be one: an SLA test that sleeps is a flake with a countdown attached. The tests move a fake
// clock forward and assert; that is the whole mechanism.
//
// PHASE ENTRY IS DERIVED, NOT DENORMALIZED. "When did this claim enter its current phase" is answered from the
// audit spine (the last PHASE_CHANGE whose to_phase is the current phase), falling back to opened_at for a
// claim still in INTAKE. A phase_since column would be a second source of truth that a back-dated transition
// (FR-9) could put out of step with the events that justify it.
//
// THE NOTIFICATION IS THE OPTIONAL PART. The alert row is written whether or not anything is notified. The
// notification tier is another swarm's fence, so warranty emits through its OWN port (SLANotifier) with a
// flag-off default that records SUPPRESSED. An alert nobody wants to lose must not depend on a downstream
// service being up, that is the correct direction for this dependency, and it is why the in-module surface
// reads the alert table rather than a message queue.
//
// UNION NOTE, the exact topic the notification tier should carry these on: warranty.sla.breached

// - the notify seam (consumer-declared; adapter lands in main.go) -

// SLANotifier is warranty's port onto the notification tier. Exact signature for the union adapter:
//
//	NotifySLABreach(ctx context.Context, in SLABreachEvent) error
//
// Unbound (the shipped default), every breach records notify_status='SUPPRESSED' and the in-module alert list
// still shows it. Binding it is one WithSLANotifier call.
type SLANotifier interface {
	NotifySLABreach(ctx context.Context, in SLABreachEvent) error
}

// SLABreachEvent is the payload warranty emits on a breach. Topic: warranty.sla.breached.
type SLABreachEvent struct {
	Topic          string `json:"topic"` // always "warranty.sla.breached"
	AlertID        string `json:"alert_id"`
	ClaimID        string `json:"claim_id"`
	ClaimNo        string `json:"claim_no"`
	BranchID       string `json:"branch_id"`
	OwnerID        string `json:"owner_id,omitempty"`
	VendorID       string `json:"vendor_id,omitempty"`
	VendorName     string `json:"vendor_name,omitempty"`
	Phase          string `json:"phase"`
	DueAt          string `json:"due_at"`
	OverdueHours   int    `json:"overdue_hours"`
	ExposureCents  int64  `json:"exposure_cents"`
	IdempotencyKey string `json:"idempotency_key"` // "warranty-sla:<claimID>:<phase>", one alert, one message
}

// SLABreachTopic is the event topic, named once so the union adapter and the report cannot disagree.
const SLABreachTopic = "warranty.sla.breached"

// WithSLANotifier binds the notification seam (DIB-223). Unbound = flag-off: alerts are raised and recorded,
// nothing is emitted.
func (s *Service) WithSLANotifier(n SLANotifier) *Service { s.slaNotif = n; return s }

// - DTOs -

// SLATarget is one configured per-phase target. VendorID "" = the default row applied to every vendor.
type SLATarget struct {
	ID          string `json:"id,omitempty"`
	Phase       string `json:"phase"`
	VendorID    string `json:"vendor_id,omitempty"`
	VendorName  string `json:"vendor_name,omitempty"`
	TargetHours int    `json:"target_hours"`
	IsActive    bool   `json:"is_active"`
	Notes       string `json:"notes,omitempty"`
}

// PhaseClock is one open claim's position: which phase it is in and when it entered.
type PhaseClock struct {
	ClaimID       string    `json:"claim_id"`
	ClaimNo       string    `json:"claim_no"`
	BranchID      string    `json:"branch_id"`
	VendorID      string    `json:"vendor_id,omitempty"`
	VendorName    string    `json:"vendor_name,omitempty"`
	OwnerID       string    `json:"owner_id,omitempty"`
	Phase         string    `json:"phase"`
	PhaseSince    time.Time `json:"-"`
	ExpectedCents int64     `json:"expected_cents"`
}

// SLA timer states.
const (
	SLAStateOK       = "OK"
	SLAStateDueSoon  = "DUE_SOON"
	SLAStateBreached = "BREACHED"
	SLAStateNoTarget = "NO_TARGET" // a phase with no configured (or an inactive) target, untimed, and SAID so
)

// dueSoonFraction is how much of the window may remain before a timer reads DUE_SOON. A quarter of the window
// is enough warning to act on a 24h target (6h) without making a 14-day vendor clock shout for three days.
const dueSoonFraction = 0.25

// SLATimer is one claim's live timer for its current phase.
type SLATimer struct {
	ClaimID        string `json:"claim_id"`
	ClaimNo        string `json:"claim_no"`
	BranchID       string `json:"branch_id"`
	VendorID       string `json:"vendor_id,omitempty"`
	VendorName     string `json:"vendor_name,omitempty"`
	OwnerID        string `json:"owner_id,omitempty"`
	Phase          string `json:"phase"`
	TargetHours    int    `json:"target_hours"`
	PhaseSince     string `json:"phase_since"`
	DueAt          string `json:"due_at,omitempty"`
	State          string `json:"state"`
	ElapsedHours   int    `json:"elapsed_hours"`
	RemainingHours int    `json:"remaining_hours"` // negative once breached
	OverdueHours   int    `json:"overdue_hours"`   // 0 unless breached
	ExposureCents  int64  `json:"exposure_cents"`
}

// SLAAlert is a durable overdue record (0132). One per (claim, phase).
type SLAAlert struct {
	ID             string `json:"id,omitempty"`
	ClaimID        string `json:"claim_id"`
	ClaimNo        string `json:"claim_no,omitempty"`
	BranchID       string `json:"branch_id,omitempty"`
	VendorName     string `json:"vendor_name,omitempty"`
	OwnerID        string `json:"owner_id,omitempty"`
	Phase          string `json:"phase"`
	DueAt          string `json:"due_at"`
	BreachedAt     string `json:"breached_at"`
	OverdueHours   int    `json:"overdue_hours"`
	AcknowledgedAt string `json:"acknowledged_at,omitempty"`
	AcknowledgedBy string `json:"acknowledged_by,omitempty"`
	NotifyStatus   string `json:"notify_status"`
	NotifyRef      string `json:"notify_ref,omitempty"`
	NotifyError    string `json:"notify_error,omitempty"`
	ExposureCents  int64  `json:"exposure_cents,omitempty"`
}

// Notification statuses (mirror the 0132 CHECK).
const (
	NotifySuppressed = "SUPPRESSED"
	NotifySent       = "SENT"
	NotifyFailed     = "FAILED"
)

// SLABoardView is the in-module SLA surface: live timers plus the outstanding alerts.
type SLABoardView struct {
	Timers        []SLATimer  `json:"timers"`
	Alerts        []SLAAlert  `json:"alerts"`
	Targets       []SLATarget `json:"targets"`
	BreachedCount int         `json:"breached_count"`
	DueSoonCount  int         `json:"due_soon_count"`
	AsOf          string      `json:"as_of"`
}

// - pure timer math (DB-free; the fake clock is the only input that moves) -

// ResolveSLATarget picks the target hours for a (phase, vendor): a vendor-specific ACTIVE row wins over the
// default ACTIVE row, the same "most specific wins" rule warranty_checklist_items already uses, so a desk that
// understands the checklist override understands this one. Returns ok=false when neither exists (the phase is
// untimed, which the timer reports as NO_TARGET rather than pretending a deadline exists).
func ResolveSLATarget(targets []SLATarget, phase, vendorID string) (int, bool) {
	best, found := 0, false
	for _, t := range targets {
		if !t.IsActive || t.Phase != phase {
			continue
		}
		if t.VendorID != "" && t.VendorID == vendorID {
			return t.TargetHours, true // vendor-specific: most specific, decided
		}
		if t.VendorID == "" {
			best, found = t.TargetHours, true
		}
	}
	return best, found
}

// BuildSLATimer computes one claim's timer against the resolved target. Pure: given the same clock reading it
// always produces the same answer, which is what makes the fake-clock tests exact rather than approximate.
func BuildSLATimer(c PhaseClock, targets []SLATarget, now time.Time) SLATimer {
	t := SLATimer{
		ClaimID: c.ClaimID, ClaimNo: c.ClaimNo, BranchID: c.BranchID,
		VendorID: c.VendorID, VendorName: c.VendorName, OwnerID: c.OwnerID,
		Phase: c.Phase, PhaseSince: c.PhaseSince.UTC().Format(time.RFC3339),
		ExposureCents: c.ExpectedCents,
	}
	hours, ok := ResolveSLATarget(targets, c.Phase, c.VendorID)
	if !ok || hours <= 0 {
		t.State = SLAStateNoTarget
		return t
	}
	due := c.PhaseSince.Add(time.Duration(hours) * time.Hour)
	elapsed := now.Sub(c.PhaseSince)
	remaining := due.Sub(now)

	t.TargetHours = hours
	t.DueAt = due.UTC().Format(time.RFC3339)
	t.ElapsedHours = int(elapsed.Hours())
	t.RemainingHours = int(remaining.Hours())
	switch {
	case !now.Before(due):
		t.State = SLAStateBreached
		// Overdue hours round UP: a claim one minute past its deadline is overdue by an hour on the board, not
		// by zero. Reporting 0 for a real breach is how a breach gets ignored.
		t.OverdueHours = ceilHours(now.Sub(due))
		t.RemainingHours = -t.OverdueHours
	case remaining.Hours() <= float64(hours)*dueSoonFraction:
		t.State = SLAStateDueSoon
	default:
		t.State = SLAStateOK
	}
	return t
}

// ceilHours rounds a duration UP to whole hours, with a floor of 1 for any positive duration.
func ceilHours(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	h := int(d / time.Hour)
	if d%time.Hour != 0 {
		h++
	}
	if h < 1 {
		h = 1
	}
	return h
}

// BuildSLATimers computes every timer and sorts them worst-first (breached, then due-soon, then by exposure).
func BuildSLATimers(clocks []PhaseClock, targets []SLATarget, now time.Time) []SLATimer {
	out := make([]SLATimer, 0, len(clocks))
	for _, c := range clocks {
		out = append(out, BuildSLATimer(c, targets, now))
	}
	rank := map[string]int{SLAStateBreached: 0, SLAStateDueSoon: 1, SLAStateOK: 2, SLAStateNoTarget: 3}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if rank[a.State] != rank[b.State] {
			return rank[a.State] < rank[b.State]
		}
		if a.OverdueHours != b.OverdueHours {
			return a.OverdueHours > b.OverdueHours
		}
		if a.ExposureCents != b.ExposureCents {
			return a.ExposureCents > b.ExposureCents
		}
		return a.ClaimNo < b.ClaimNo
	})
	return out
}

// - service -

// SLABoard returns the live timers + outstanding alerts for the caller's reachable branches.
func (s *Service) SLABoard(ctx context.Context, branchID string, includeAcknowledged bool) (SLABoardView, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return SLABoardView{}, err
	}
	if branchID != "" && !caller.CanAccessBranch(branchID) {
		return SLABoardView{}, forbidden("warranty: caller cannot access branch %s", branchID)
	}
	targets, err := s.store.SLATargets(ctx)
	if err != nil {
		return SLABoardView{}, err
	}
	clocks, err := s.store.OpenClaimPhaseClocks(ctx, branchID)
	if err != nil {
		return SLABoardView{}, err
	}
	if branchID == "" {
		kept := clocks[:0]
		for _, c := range clocks {
			if caller.CanAccessBranch(c.BranchID) {
				kept = append(kept, c)
			}
		}
		clocks = kept
	}
	alerts, err := s.store.SLAAlerts(ctx, branchID, includeAcknowledged)
	if err != nil {
		return SLABoardView{}, err
	}
	if branchID == "" {
		keptA := alerts[:0]
		for _, a := range alerts {
			if caller.CanAccessBranch(a.BranchID) {
				keptA = append(keptA, a)
			}
		}
		alerts = keptA
	}
	view := SLABoardView{
		Timers:  BuildSLATimers(clocks, targets, s.clock()),
		Alerts:  alerts,
		Targets: targets,
		AsOf:    s.clock().UTC().Format(time.RFC3339),
	}
	for _, t := range view.Timers {
		switch t.State {
		case SLAStateBreached:
			view.BreachedCount++
		case SLAStateDueSoon:
			view.DueSoonCount++
		}
	}
	return view, nil
}

// SweepSLA evaluates every open claim's timer and RAISES an alert for each breach: one durable row per
// (claim, phase), upserted so a claim kicked back to a phase it already breached updates rather than
// duplicates. Each newly-raised alert emits through the SLANotifier port; unbound, it records SUPPRESSED.
//
// Returns the alerts it raised or refreshed. Idempotent by construction: running it twice in the same clock
// minute produces the same rows with the same overdue hours.
//
// This is a HAND-CRANKED sweep with an operator route today. Registering it on the shared jobs runner is a PARK
// (the runner is already Run()ning by the time warranty is constructed in main, and registering after start is
// a data race), the exact JobFunc is named in REPORT-W5-B.md.
func (s *Service) SweepSLA(ctx context.Context, branchID string) ([]SLAAlert, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	if branchID != "" && !caller.CanAccessBranch(branchID) {
		return nil, forbidden("warranty: caller cannot access branch %s", branchID)
	}
	targets, err := s.store.SLATargets(ctx)
	if err != nil {
		return nil, err
	}
	clocks, err := s.store.OpenClaimPhaseClocks(ctx, branchID)
	if err != nil {
		return nil, err
	}
	now := s.clock()
	raised := []SLAAlert{}
	for _, c := range clocks {
		if branchID == "" && !caller.CanAccessBranch(c.BranchID) {
			continue
		}
		t := BuildSLATimer(c, targets, now)
		if t.State != SLAStateBreached {
			continue
		}
		alert := SLAAlert{
			ClaimID:       c.ClaimID,
			ClaimNo:       c.ClaimNo,
			BranchID:      c.BranchID,
			VendorName:    c.VendorName,
			OwnerID:       c.OwnerID,
			Phase:         c.Phase,
			DueAt:         t.DueAt,
			BreachedAt:    now.UTC().Format(time.RFC3339),
			OverdueHours:  t.OverdueHours,
			NotifyStatus:  NotifySuppressed,
			ExposureCents: c.ExpectedCents,
		}
		id, uerr := s.store.UpsertSLAAlert(ctx, alert)
		if uerr != nil {
			s.log.Warn("warranty: raising the SLA alert failed", "claim", c.ClaimNo, "phase", c.Phase, "err", uerr)
			continue
		}
		alert.ID = id

		// The flag-off notification seam. A notify failure NEVER un-raises the alert: the row is the alert, the
		// message is a courtesy.
		if s.slaNotif != nil {
			ev := SLABreachEvent{
				Topic: SLABreachTopic, AlertID: id, ClaimID: c.ClaimID, ClaimNo: c.ClaimNo,
				BranchID: c.BranchID, OwnerID: c.OwnerID, VendorID: c.VendorID, VendorName: c.VendorName,
				Phase: c.Phase, DueAt: t.DueAt, OverdueHours: t.OverdueHours, ExposureCents: c.ExpectedCents,
				IdempotencyKey: "warranty-sla:" + c.ClaimID + ":" + c.Phase,
			}
			if nerr := s.slaNotif.NotifySLABreach(ctx, ev); nerr != nil {
				alert.NotifyStatus = NotifyFailed
				alert.NotifyError = nerr.Error()
				_ = s.store.MarkSLANotified(ctx, id, NotifyFailed, "", nerr.Error())
			} else {
				alert.NotifyStatus = NotifySent
				alert.NotifyRef = ev.IdempotencyKey
				_ = s.store.MarkSLANotified(ctx, id, NotifySent, ev.IdempotencyKey, "")
			}
		}
		raised = append(raised, alert)
	}
	return raised, nil
}

// AcknowledgeSLAAlert marks an alert as seen by the acting staff member. The alert is not deleted: a
// disappeared alert cannot be asked about at the next stand-up.
func (s *Service) AcknowledgeSLAAlert(ctx context.Context, claimID, alertID string) error {
	caller, _, err := s.callerScopedClaim(ctx, claimID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(alertID) == "" {
		return unprocessable("warranty: alert id is required")
	}
	return s.store.AcknowledgeSLAAlert(ctx, alertID, caller.UserID)
}

// SLATargetList exposes the configured targets (the defaults are stated in 0132 and shown on the surface, so
// "why is this claim red" is answerable without reading a migration).
func (s *Service) SLATargetList(ctx context.Context) ([]SLATarget, error) {
	if _, err := s.caller(ctx); err != nil {
		return nil, err
	}
	return s.store.SLATargets(ctx)
}
