package billing

// ar_landing.go, the AR LANDING half: the aging cards that sit over the shipped register
// (DIB-510) and the escalation queue that is the 90+ tail.
//
// -- THE ONE IDEA -------------------------------------------------------------------------------
//
// The register answers "what does the book look like". The landing answers "what do I do first".
// Those are different questions and the second one is the one somebody opens AR to ask at 8am, so
// the tail bucket is not a number here, it is a WORK LIST. A card whose only content is a figure
// tells an operator that ninety-thousand dollars is ninety days late and then makes them go and
// find out who; the queue below names the accounts, says what is wrong with each, and says what
// the next move is.
//
// -- WHERE THE EDGES COME FROM ------------------------------------------------------------------
//
// Not from this file. `0 to 60 to 90, 90 plus` is the DEFAULT, not the rule: the edges are typed,
// CHECK-constrained columns on ar_landing_policy (0183) because a dealer moving the escalation edge
// from 90 days to 75 changes who gets chased and what a customer is told on the phone. Every bucket
// computation below takes the policy as an argument and none of them close over a constant.
//
// -- THE TIER BOUNDARY IS A CAPABILITY, NOT A SECOND SCREEN -------------------------------------
//
// Basic AR ships native; accelerated AR is paid. That boundary is expressed EXACTLY ONCE, here, as
// a capability resolved from ar_landing_policy.ar_tier plus the caller's authority, and it is
// enforced at the MARSHAL layer as byte-absence. A caller who does not hold `ar.advanced` receives
// a payload with no `ar_advanced` key in it at all: not an empty object, not a false flag next to
// the data, not a field the client is trusted to hide. The mutation test in
// ar_landing_capability_test.go asserts the byte strings are missing from the response body,
// because a hidden button is not a gate.
//
// WHAT IS AND IS NOT PAID (the lane's call, recorded in NOTES so it can be reversed cheaply):
//   BASE  the cards, the escalation queue's IDENTITY (who, how much, how old), the register below,
//         AP-contact tagging, and the paper-path flag list.
//   PRO   the ENGAGEMENT signals on each queue entry (last reminder, last open, delivered-never-
//         opened), the computed next action, and the reminder machinery itself.
// The paper-path flag is deliberately NOT paid: it exists because customers who never open anything
// are invisible until they are 90 days late, and a defect fix behind a paywall is still a defect.

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// -- vocabulary ---------------------------------------------------------------------------------

// The four buckets. These strings are on the wire, in the database CHECK and in the frontend
// registry; they are add-only for the same reason curation ids are (a rename orphans saved rows).
const (
	BucketCurrent    = "CURRENT"
	BucketWarn       = "WARN"
	BucketLate       = "LATE"
	BucketEscalation = "ESCALATION"
)

// Card ids. They are the frontend registry's ids (app/src/ar-landing-surface.ts) and are repeated
// here ONLY because the server has to name the card a computed bucket belongs to. The server still
// owns no list of what may render, curation rows carry ids this constant set never sees, and that
// is correct (the 0149/0164 ruling).
const (
	CardCurrent    = "card.current"
	CardWarn       = "card.warn"
	CardLate       = "card.late"
	CardEscalation = "card.escalation"
	CardPaperPath  = "card.paper_path"
)

// ARLandingSurfaceID is the curation substrate's key for this surface.
const ARLandingSurfaceID = "ar.landing"

// The two tiers, and the capability the paid one grants.
const (
	TierBase = "BASE"
	TierPro  = "PRO"

	// CapARAdvanced is the ONE capability name for accelerated AR. One name, one surface.
	CapARAdvanced = "ar.advanced"
)

// The untagged-send postures. There is deliberately no "blast" value, see 0183.
const (
	FallbackConfirm = "CONFIRM"
	FallbackRefuse  = "REFUSE"
)

// -- the typed policy ---------------------------------------------------------------------------

// ARLandingPolicy is ar_landing_policy (0183), the money-gating knobs, typed.
type ARLandingPolicy struct {
	BranchID             string `json:"branch_id,omitempty"`
	AgingWarnDays        int    `json:"aging_warn_days"`
	AgingEscalateDays    int    `json:"aging_escalate_days"`
	ReminderCadenceDays  int    `json:"reminder_cadence_days"`
	ReminderMaxPerCycle  int    `json:"reminder_max_per_cycle"`
	BlackHoleWindowDays  int    `json:"black_hole_window_days"`
	BlackHoleMinSends    int    `json:"black_hole_min_sends"`
	ReminderEmailEnabled bool   `json:"reminder_email_enabled"`
	ReminderSMSEnabled   bool   `json:"reminder_sms_enabled"`
	UntaggedFallback     string `json:"untagged_fallback"`
	Tier                 string `json:"ar_tier"`
}

// DefaultARLandingPolicy mirrors the 0183 column defaults. It is what a read falls back to when the
// dealer row is missing, so a landing still renders on a half-migrated database rather than 500ing
// at the one moment somebody is trying to find out who owes them money.
func DefaultARLandingPolicy() ARLandingPolicy {
	return ARLandingPolicy{
		AgingWarnDays:        60,
		AgingEscalateDays:    90,
		ReminderCadenceDays:  7,
		ReminderMaxPerCycle:  3,
		BlackHoleWindowDays:  45,
		BlackHoleMinSends:    3,
		ReminderEmailEnabled: true,
		ReminderSMSEnabled:   false,
		UntaggedFallback:     FallbackConfirm,
		Tier:                 TierBase,
	}
}

// ValidateARLandingPolicy refuses a bad policy IN WORDS before any CHECK could 500, the settings
// kit's posture, using the kit's own validators so this module and the quote pilot cannot drift on
// how an out-of-range knob is reported.
//
// The edge ORDER check is separate and comes last because it is the one refusal that is about the
// relationship between two values rather than either value alone, and an operator who typed 95 into
// the warn box needs to be told that, not told that 95 is out of range (it is not).
func ValidateARLandingPolicy(p ARLandingPolicy) (ARLandingPolicy, error) {
	const mod = "ar"
	if p.UntaggedFallback == "" {
		p.UntaggedFallback = FallbackConfirm
	}
	if p.Tier == "" {
		p.Tier = TierBase
	}
	checks := []struct {
		knob   string
		v      int
		lo, hi int
	}{
		{"aging_warn_days", p.AgingWarnDays, 1, 364},
		{"aging_escalate_days", p.AgingEscalateDays, 2, 365},
		{"reminder_cadence_days", p.ReminderCadenceDays, 1, 90},
		{"reminder_max_per_cycle", p.ReminderMaxPerCycle, 1, 20},
		{"black_hole_window_days", p.BlackHoleWindowDays, 7, 365},
		{"black_hole_min_sends", p.BlackHoleMinSends, 1, 20},
	}
	for _, c := range checks {
		if err := settings.ValidateIntRange(mod, c.knob, c.v, c.lo, c.hi); err != nil {
			return ARLandingPolicy{}, err
		}
	}
	switch p.UntaggedFallback {
	case FallbackConfirm, FallbackRefuse:
	default:
		return ARLandingPolicy{}, settings.Errf(http.StatusUnprocessableEntity,
			"ar: untagged_fallback must be CONFIRM or REFUSE, a send with no tagged contact is never a blast")
	}
	switch p.Tier {
	case TierBase, TierPro:
	default:
		return ARLandingPolicy{}, settings.Errf(http.StatusUnprocessableEntity, "ar: ar_tier must be BASE or PRO")
	}
	if p.AgingWarnDays >= p.AgingEscalateDays {
		return ARLandingPolicy{}, settings.Errf(http.StatusUnprocessableEntity,
			"ar: aging_warn_days (%d) must be fewer than aging_escalate_days (%d)", p.AgingWarnDays, p.AgingEscalateDays)
	}
	return p, nil
}

// -- the pure bucket arithmetic -----------------------------------------------------------------

// DaysPastDue counts whole days from the due date to asOf. Zero or negative is CURRENT, an invoice
// due today is not late today, which is the answer a customer will insist on.
func DaysPastDue(due, asOf time.Time) int {
	d := asOf.UTC().Truncate(24 * time.Hour).Sub(due.UTC().Truncate(24 * time.Hour))
	return int(d / (24 * time.Hour))
}

// BucketFor places a days-past-due count in the policy's buckets. Boundaries are INCLUSIVE at the
// top of each bucket: at exactly aging_escalate_days an account IS escalated, because "90 days" in
// a collections conversation has always meant "90 or worse".
func BucketFor(daysPastDue int, p ARLandingPolicy) string {
	switch {
	case daysPastDue <= 0:
		return BucketCurrent
	case daysPastDue < p.AgingWarnDays+1:
		return BucketWarn
	case daysPastDue < p.AgingEscalateDays:
		return BucketLate
	default:
		return BucketEscalation
	}
}

// cardForBucket maps a bucket onto the card that renders it.
func cardForBucket(b string) string {
	switch b {
	case BucketCurrent:
		return CardCurrent
	case BucketWarn:
		return CardWarn
	case BucketLate:
		return CardLate
	default:
		return CardEscalation
	}
}

// -- the wire shapes ----------------------------------------------------------------------------

// OpenInvoice is one unpaid invoice as the landing needs it. A projection, not the invoice: the
// landing never edits an invoice and must never be a second way to read one.
type OpenInvoice struct {
	InvoiceID    string
	InvoiceNo    string
	CustomerID   string
	CustomerName string
	AccountNo    string
	BranchID     string
	BalanceCents int64
	DueDate      time.Time
}

// LandingCard is one aging card. Accounts and Invoices are both carried because they answer
// different questions, eleven invoices across two accounts is two phone calls, not eleven.
type LandingCard struct {
	ID         string `json:"id"`
	Bucket     string `json:"bucket"`
	FromDays   int    `json:"from_days"`
	ToDays     int    `json:"to_days,omitempty"` // absent = open-ended (the tail)
	Accounts   int    `json:"accounts"`
	Invoices   int    `json:"invoices"`
	TotalCents int64  `json:"total_cents"`

	// THE ACCOUNTS THIS CARD COUNTED, so the register it filters shows exactly them.
	//
	// It is here because the alternative is worse in a way the walkthrough caught: the shipped
	// register carries FIXED 30/60/90 bucket totals, so a client-side filter can only ask "does this
	// account hold money past 60 days", and under a 90-day escalation card that let a 74-day
	// account through. The card said two accounts and the register underneath it showed three. A
	// landing whose figure disagrees with the rows it opens is worse than no filter at all, so the
	// server sends the set it actually counted and the register filters by identity.
	AccountIDs []string `json:"account_ids"`
}

// EscalationEntry is one ACCOUNT in the 90+ work list. Identity and exposure only, everything that
// makes it actionable lives in ARAdvanced, behind the capability.
type EscalationEntry struct {
	CustomerID   string `json:"customer_id"`
	CustomerName string `json:"customer_name"`
	AccountNo    string `json:"account_no"`
	OldestDays   int    `json:"oldest_days_past_due"`
	Invoices     int    `json:"invoices"`
	BalanceCents int64  `json:"balance_cents"`
	OnHold       bool   `json:"on_hold"`
}

// EngagementEntry is the PAID half of one queue row: whether anything we sent this account has ever
// been opened, and what to do about it. Every field here is byte-absent for a BASE caller because
// the whole struct is.
type EngagementEntry struct {
	CustomerID           string     `json:"customer_id"`
	APContactTagged      bool       `json:"ap_contact_tagged"`
	LastReminderAt       *time.Time `json:"last_reminder_at,omitempty"`
	LastOpenAt           *time.Time `json:"last_open_at,omitempty"`
	DeliveredNeverOpened int        `json:"delivered_never_opened"`
	PaperPathFlagged     bool       `json:"paper_path_flagged"`
	NextAction           string     `json:"next_action"`
}

// ARAdvanced is the accelerated-AR payload. It is a POINTER on ARLanding so that a caller without
// the capability gets no key at all.
type ARAdvanced struct {
	Engagement       []EngagementEntry `json:"engagement"`
	RemindersEnabled bool              `json:"reminders_enabled"`
	CadenceDays      int               `json:"cadence_days"`
	EmailEnabled     bool              `json:"email_enabled"`
	SMSEnabled       bool              `json:"sms_enabled"`
}

// PaperPathEntry is one flagged account, the black hole, made visible.
type PaperPathEntry struct {
	FlagID         string    `json:"flag_id"`
	CustomerID     string    `json:"customer_id"`
	CustomerName   string    `json:"customer_name"`
	AccountNo      string    `json:"account_no"`
	DeliveredSends int       `json:"delivered_sends"`
	WindowDays     int       `json:"window_days"`
	RaisedAt       time.Time `json:"raised_at"`
}

// ARLanding is the one read the landing opens on.
type ARLanding struct {
	AsOf         string            `json:"as_of"`
	Policy       ARLandingPolicy   `json:"policy"`
	Cards        []LandingCard     `json:"cards"`
	Escalation   []EscalationEntry `json:"escalation_queue"`
	PaperPath    []PaperPathEntry  `json:"paper_path"`
	Capabilities map[string]bool   `json:"capabilities"`

	// THE GATE. Absent for a caller without ar.advanced, no key, no empty object, no false flag.
	Advanced *ARAdvanced `json:"ar_advanced,omitempty"`
}

// -- the pure assembler -------------------------------------------------------------------------

// BuildCards totals the open book into the policy's four cards, worst first.
//
// WORST FIRST IS THE ARGUMENT, not a sort preference. The aging report reads healthy-to-sick because
// it is a report; a landing is where somebody starts work and the work is at the sick end. The order
// a dealer actually SEES is still theirs, curation rows own that (0183), but the server's order is
// the one that is right when nobody has curated anything.
func BuildCards(invs []OpenInvoice, p ARLandingPolicy, asOf time.Time) []LandingCard {
	type agg struct {
		invoices int
		cents    int64
		accounts map[string]bool
	}
	buckets := map[string]*agg{}
	for _, b := range []string{BucketEscalation, BucketLate, BucketWarn, BucketCurrent} {
		buckets[b] = &agg{accounts: map[string]bool{}}
	}
	for _, iv := range invs {
		if iv.BalanceCents == 0 {
			continue
		}
		a := buckets[BucketFor(DaysPastDue(iv.DueDate, asOf), p)]
		a.invoices++
		a.cents += iv.BalanceCents
		a.accounts[iv.CustomerID] = true
	}
	mk := func(bucket string, from, to int) LandingCard {
		a := buckets[bucket]
		ids := make([]string, 0, len(a.accounts))
		for id := range a.accounts {
			ids = append(ids, id)
		}
		// Sorted, so the payload is stable between reads and a diff of two landings is readable.
		sort.Strings(ids)
		return LandingCard{
			ID: cardForBucket(bucket), Bucket: bucket, FromDays: from, ToDays: to,
			Accounts: len(a.accounts), Invoices: a.invoices, TotalCents: a.cents,
			AccountIDs: ids,
		}
	}
	return []LandingCard{
		mk(BucketEscalation, p.AgingEscalateDays, 0),
		mk(BucketLate, p.AgingWarnDays+1, p.AgingEscalateDays-1),
		mk(BucketWarn, 1, p.AgingWarnDays),
		mk(BucketCurrent, 0, 0),
	}
}

// BuildEscalation rolls the tail bucket up per ACCOUNT, worst first by balance.
//
// Per account, not per invoice, because the unit of work is a phone call. A contractor with eleven
// invoices at 94 days is one conversation; a queue that lists them eleven times is a queue nobody
// finishes.
func BuildEscalation(invs []OpenInvoice, p ARLandingPolicy, asOf time.Time, onHold map[string]bool) []EscalationEntry {
	byCustomer := map[string]*EscalationEntry{}
	for _, iv := range invs {
		if iv.BalanceCents == 0 {
			continue
		}
		d := DaysPastDue(iv.DueDate, asOf)
		if BucketFor(d, p) != BucketEscalation {
			continue
		}
		e, ok := byCustomer[iv.CustomerID]
		if !ok {
			e = &EscalationEntry{
				CustomerID: iv.CustomerID, CustomerName: iv.CustomerName,
				AccountNo: iv.AccountNo, OnHold: onHold[iv.CustomerID],
			}
			byCustomer[iv.CustomerID] = e
		}
		e.Invoices++
		e.BalanceCents += iv.BalanceCents
		if d > e.OldestDays {
			e.OldestDays = d
		}
	}
	out := make([]EscalationEntry, 0, len(byCustomer))
	for _, e := range byCustomer {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].BalanceCents != out[j].BalanceCents {
			return out[i].BalanceCents > out[j].BalanceCents
		}
		return out[i].CustomerID < out[j].CustomerID
	})
	return out
}

// EngagementInput is what the engagement half is computed from, one row per queued account.
type EngagementInput struct {
	CustomerID           string
	APContactTagged      bool
	LastReminderAt       *time.Time
	LastOpenAt           *time.Time
	DeliveredNeverOpened int
	PaperPathFlagged     bool
	OnHold               bool
}

// NextAction is the work-list verb: what this account needs next, in at most three words.
//
// ORDER IS THE JUDGEMENT, and it is the reason this is a function and not a ternary at the render
// site. The account nobody can reach is worse than the account nobody has chased, which is worse
// than the account we chased yesterday, so the checks run reachability, then paper, then cadence,
// then the human step. "Call" is the honest last answer: when the machine has done everything it
// can and the money is still not here, the next action is a person, and pretending otherwise is how
// an automated dunning ladder ends up sending a fifth text to somebody who needs a phone call.
func NextAction(e EngagementInput, p ARLandingPolicy, now time.Time) string {
	if !e.APContactTagged {
		return "Tag AP contact"
	}
	if e.PaperPathFlagged {
		return "Move to paper"
	}
	if e.LastReminderAt == nil {
		return "Send reminder"
	}
	if e.DeliveredNeverOpened >= p.BlackHoleMinSends && e.LastOpenAt == nil {
		return "Move to paper"
	}
	if now.Sub(*e.LastReminderAt) >= time.Duration(p.ReminderCadenceDays)*24*time.Hour {
		return "Send reminder"
	}
	return "Call"
}

// BuildEngagement computes the paid half for the queue, in the queue's own order.
func BuildEngagement(queue []EscalationEntry, in map[string]EngagementInput, p ARLandingPolicy, now time.Time) []EngagementEntry {
	out := make([]EngagementEntry, 0, len(queue))
	for _, q := range queue {
		e := in[q.CustomerID]
		e.CustomerID = q.CustomerID
		out = append(out, EngagementEntry{
			CustomerID:           q.CustomerID,
			APContactTagged:      e.APContactTagged,
			LastReminderAt:       e.LastReminderAt,
			LastOpenAt:           e.LastOpenAt,
			DeliveredNeverOpened: e.DeliveredNeverOpened,
			PaperPathFlagged:     e.PaperPathFlagged,
			NextAction:           NextAction(e, p, now),
		})
	}
	return out
}

// -- the service --------------------------------------------------------------------------------

// ARLandingStore is the landing's persistence seam. Every method is a READ except the policy write;
// the reminder and flag writes live on ARReminderStore next door, so a deployment can wire the
// landing without wiring the sender.
type ARLandingStore interface {
	ReadARLandingPolicy(ctx context.Context, branchID string) (ARLandingPolicy, error)
	UpsertARLandingPolicy(ctx context.Context, p ARLandingPolicy) error
	OpenInvoices(ctx context.Context, asOf time.Time, branchID string) ([]OpenInvoice, error)
	ActiveHoldCustomers(ctx context.Context) (map[string]bool, error)
	EngagementFor(ctx context.Context, customerIDs []string, windowDays int, now time.Time) (map[string]EngagementInput, error)
	OpenPaperPathFlags(ctx context.Context) ([]PaperPathEntry, error)
}

// ARLandingService serves the landing. The clock is injected, nothing in this package calls
// time.Now() at a decision point, because "is this invoice 90 days late" is a decision.
type ARLandingService struct {
	store ARLandingStore
	now   func() time.Time
	// The three settings-history seams (ar_landing_history.go). This service shipped with NONE of
	// them: unlike dispatch, which already recorded its policy changes and only needed a way to read
	// them back, SetPolicy here wrote the row and recorded nothing at all. All three are nil-tolerant
	//, no recorder records nothing, no trail is an empty history, no resolver is a role word.
	audit audit.Recorder
	trail ARPolicyTrailReader
	names ARActorNameResolver
}

// NewARLandingService wires the store with the real clock.
func NewARLandingService(store ARLandingStore) *ARLandingService {
	return &ARLandingService{store: store, now: time.Now}
}

// WithClock injects the clock (tests, and any deployment that backdates a run).
func (s *ARLandingService) WithClock(now func() time.Time) *ARLandingService {
	if now != nil {
		s.now = now
	}
	return s
}

// Policy reads the effective policy, falling back to the shipped defaults rather than failing the
// whole landing when the dealer row is unreadable.
func (s *ARLandingService) Policy(ctx context.Context, branchID string) ARLandingPolicy {
	p, err := s.store.ReadARLandingPolicy(ctx, branchID)
	if err != nil {
		return DefaultARLandingPolicy()
	}
	if p.AgingEscalateDays == 0 {
		return DefaultARLandingPolicy()
	}
	return p
}

// SetPolicy validates in words and writes. Admin gating is the route's; the validation is here so
// every caller of the service gets it, including a future job.
//
// -- THE PRE-IMAGE IS READ BEFORE THE WRITE ------------------------------------------------------
//
// which is the whole reason before→after can be truthful, and it is read through s.store, NEVER
// through s.Policy(). Policy() substitutes DefaultARLandingPolicy() when the read errors AND when
// AgingEscalateDays == 0, a deliberate kindness that keeps the landing rendering on a half-migrated
// database. Diffing against it would compare the dealer's real stored row with a set of shipped
// defaults and FABRICATE up to ten history rows on the first save of any dealer whose row is
// incomplete: a trail claiming somebody changed nine settings they never touched. The trail must
// diff what is in the columns against what is going into them, and nothing else.
//
// A FAILED PRE-READ DOES NOT BLOCK THE SAVE. The dealer's intent is the important thing; losing the
// before-image costs a history row, and refusing a legitimate rule change because a second read
// failed would cost them the change itself.
func (s *ARLandingService) SetPolicy(ctx context.Context, p ARLandingPolicy) (ARLandingPolicy, error) {
	clean, err := ValidateARLandingPolicy(p)
	if err != nil {
		return ARLandingPolicy{}, err
	}
	before, beforeErr := s.store.ReadARLandingPolicy(ctx, p.BranchID)
	if err := s.store.UpsertARLandingPolicy(ctx, clean); err != nil {
		return ARLandingPolicy{}, err
	}
	if beforeErr == nil {
		// ONE ROW PER CHANGED KNOB, in the field/before/after shape the audit allowlist already
		// admits. The alternative, a new allowlisted key per knob per module, scales to a key per
		// setting in the system, and that allowlist is deny-by-default precisely so it stays small
		// enough for a person to read.
		s.recordPolicyChanges(ctx, p.BranchID, arPolicyChanges(before, clean))
	}
	return clean, nil
}

// AdvancedAllowed resolves the ar.advanced capability for THIS caller. Fail closed: an anonymous
// caller, an unreadable policy or a BASE dealer all resolve to false, and the difference between
// those three is deliberately invisible on the wire, a payload that told a base-tier dealer which
// of the three reasons applied would be a licence-probing oracle.
func (s *ARLandingService) AdvancedAllowed(ctx context.Context, branchID string) bool {
	caller, ok := identity.StaffContextFromContext(ctx)
	if !ok {
		return false
	}
	p, err := s.store.ReadARLandingPolicy(ctx, branchID)
	if err != nil {
		return false
	}
	if p.Tier != TierPro {
		return false
	}
	return caller.IsManager()
}

// Landing assembles the one read. The advanced payload is attached only when the capability holds,
// and the engagement query is not even ISSUED otherwise, a gate that fetches the data and then
// declines to serialize it is one refactor away from leaking it.
func (s *ARLandingService) Landing(ctx context.Context, branchID string, asOf time.Time) (ARLanding, error) {
	if asOf.IsZero() {
		asOf = s.now()
	}
	p := s.Policy(ctx, branchID)
	invs, err := s.store.OpenInvoices(ctx, asOf, branchID)
	if err != nil {
		return ARLanding{}, err
	}
	holds, err := s.store.ActiveHoldCustomers(ctx)
	if err != nil {
		// A hold read that fails must not blank the landing: the queue is still true without the
		// pill, and an operator who cannot see who owes money is worse off than one who cannot see
		// which of them is stopped at the counter.
		holds = map[string]bool{}
	}
	out := ARLanding{
		AsOf:       asOf.Format("2006-01-02"),
		Policy:     p,
		Cards:      BuildCards(invs, p, asOf),
		Escalation: BuildEscalation(invs, p, asOf, holds),
	}
	if out.Escalation == nil {
		out.Escalation = []EscalationEntry{}
	}
	flags, err := s.store.OpenPaperPathFlags(ctx)
	if err != nil || flags == nil {
		flags = []PaperPathEntry{}
	}
	out.PaperPath = flags

	allowed := s.AdvancedAllowed(ctx, branchID)
	out.Capabilities = map[string]bool{CapARAdvanced: allowed}
	if !allowed {
		return out, nil
	}
	ids := make([]string, 0, len(out.Escalation))
	for _, e := range out.Escalation {
		ids = append(ids, e.CustomerID)
	}
	eng, err := s.store.EngagementFor(ctx, ids, p.BlackHoleWindowDays, asOf)
	if err != nil {
		eng = map[string]EngagementInput{}
	}
	out.Advanced = &ARAdvanced{
		Engagement:       BuildEngagement(out.Escalation, eng, p, asOf),
		RemindersEnabled: p.ReminderEmailEnabled || p.ReminderSMSEnabled,
		CadenceDays:      p.ReminderCadenceDays,
		EmailEnabled:     p.ReminderEmailEnabled,
		SMSEnabled:       p.ReminderSMSEnabled,
	}
	return out, nil
}
