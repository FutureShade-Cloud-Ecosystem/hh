package dispatch

// loadboard.go, the LOAD BUILDING half of the dispatch surface (Aug-26 call, items A-I).
//
// The board 0090 shipped answers "where is everything today". This file answers the question the
// dispatcher actually opens the screen with: WHAT GOES ON WHICH TRUCK. Four facts decide that -
// weight, volume, pallet positions and DOLLAR VALUE, and two rules protect it: the REQUESTED →
// COMMITTED state machine (0185), and the fact that freight on a split order is arithmetic the
// server does and the client never touches.
//
// 0185, ONE MECHANISM, NOT TWO. 0184 shipped requested/committed as a scheduling state AND a
// 48-hour lock as a separate change freeze. In the client's language those are one act: a load is
// REQUESTED when a salesperson picks a date, COMMITTED when a dispatcher puts it on a truck, and a
// committed load is precisely the one re-optimisation stops shuffling. The 48 hours is WHEN a
// dispatcher typically commits, a PROMPT. Nothing here refuses a mutation because of the clock.
//
// EVERYTHING HERE IS PURE OR NEARLY SO. The gauge bands, the lock predicate and the even split are
// functions over values with an injected clock, so the rules that would be quietly wrong in a
// template are pinned by a table test instead.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// - policy -

// Reopen policies (0185's CHECK, mirrored in Go so an unknown value is a 422 naming it rather than a
// 500 carrying a constraint name, the truckTypes posture).
//
// The words changed with the meaning. MANAGER_UNLOCK described who held a key to a padlock; MANAGER
// describes who may reopen a commitment. OFF meant "no padlock"; ANYONE means a committed load may be
// changed by anyone who could have committed it. HARD meant "no key exists"; NOBODY means a
// commitment is final.
const (
	ReopenAnyone       = "ANYONE"
	ReopenManager      = "MANAGER"
	ReopenNobody       = "NOBODY"
	FreightSplitEven   = "EVEN"
	FreightSplitManual = "MANUAL"
)

// DefaultCommitPromptHours is the client's 48, in its true role: how long before a delivery day a
// still-REQUESTED load is PROMPTED for commitment. It gates nothing.
const DefaultCommitPromptHours = 48

// DefaultWorkingViewDays is how far the dispatcher's daily WORKING VIEW reaches, "almost a couple
// weeks" (one dealer principal), where another principal's floor was "more than 48 hours". It is deliberately NOT
// CommitHorizonWeeks: eight weeks of work in a fortnight's view is the hundred irrelevant items
// the dealer named.
const DefaultWorkingViewDays = 14

// DefaultDriverSessionHours and MaxDriverSessionHours bound the 0209 knob.
//
// TWELVE IS TODAY'S CONSTANT, WRITTEN DOWN. identity/auth.go has said `staffJWTTTL = 12 * time.Hour`
// since C-1, and this default is that same twelve moved somewhere a dealer can reach it. A dealer who
// never opens the settings screen therefore gets exactly the session they have now, this knob
// changes nobody's behaviour until somebody asks it to.
//
// ONE IS THE FLOOR because a session shorter than a delivery expires between arriving and getting a
// signature. 168, one week, IS THE CEILING, and it is the same number identity.MaxDriverSessionTTL
// hard-codes as the outer lifetime it will honour on a token asserting the driver role. The two must
// move together or one of them is decorative; TestDriverSessionBounds_MatchTheIdentityCeiling pins
// them to each other, which is the only reason it is safe to state the number twice.
const (
	DefaultDriverSessionHours = 12
	MaxDriverSessionHours     = 168
)

// Policy is the effective dispatch_policy row: the branch override when one exists, else the dealer
// NULL row (0043/0173 idiom).
type Policy struct {
	BranchID          string `json:"branch_id,omitempty"`
	CommitPromptHours int    `json:"commit_prompt_hours"`
	ReopenPolicy      string `json:"reopen_policy"`
	// LoadValueVisibility gates MONEY at the marshal layer. Unchanged by 0185.
	LoadValueVisibility string `json:"load_value_visibility"`
	// CommitHorizonWeeks is how far the board PLANS. WorkingViewDays is how far the daily WORKING
	// VIEW reaches. They are two different numbers and 0184 had only the first.
	CommitHorizonWeeks int    `json:"commit_horizon_weeks"`
	WorkingViewDays    int    `json:"working_view_days"`
	FreightSplitMode   string `json:"freight_split_mode"`
	// DriverSessionHours gates PERMISSION, not display: it is how long a DRIVER-role staff session is
	// honoured (0209). It sits beside LoadValueVisibility rather than beside the two view-width knobs
	// above it because that is the company it keeps, both answer "what is this person allowed",
	// which is the D-105 test for a typed column rather than a curation row.
	//
	// IT IS READ FROM THE DEALER-DEFAULT ROW ONLY. A branch override on this column is stored and
	// echoed like any other, and is INERT at token issue, 0209's header and the SessionTTLResolver
	// seam in identity both say why, and the seam carries no branch argument so there is nothing to
	// pass even by accident.
	DriverSessionHours int `json:"driver_session_hours"`
}

// DefaultPolicy is what an unwired or unreadable policy resolves to. It FAILS CLOSED in the two
// directions that matter: a committed load may be reopened only by a dispatch manager, and value is
// manager-only.
func DefaultPolicy() Policy {
	return Policy{
		CommitPromptHours:   DefaultCommitPromptHours,
		ReopenPolicy:        ReopenManager,
		LoadValueVisibility: settings.VisibilityManagerUp,
		CommitHorizonWeeks:  8,
		WorkingViewDays:     DefaultWorkingViewDays,
		FreightSplitMode:    FreightSplitEven,
		// FAILING CLOSED ON A SESSION LENGTH MEANS THE SHORT NUMBER, NOT THE LONG ONE. A policy read
		// that failed must never hand back a week; twelve hours is what this system did before the
		// knob existed, so an unreadable row buys a driver exactly nothing and costs them nothing.
		DriverSessionHours: DefaultDriverSessionHours,
	}
}

// ValidatePolicy refuses a malformed policy IN WORDS before any CHECK could 500 (the settings kit's
// posture). Omitted values normalise to the column defaults rather than to zero, a lock window of
// zero is a real, meaningful setting and must be asked for, never arrived at by omission.
func ValidatePolicy(p Policy) (Policy, error) {
	out := p
	if out.CommitPromptHours == 0 && p.ReopenPolicy == "" && p.LoadValueVisibility == "" {
		out.CommitPromptHours = DefaultCommitPromptHours
	}
	if out.CommitPromptHours < 0 || out.CommitPromptHours > 336 {
		return Policy{}, settings.Errf(http.StatusUnprocessableEntity,
			"dispatch: commit_prompt_hours must be between 0 and 336")
	}
	switch out.ReopenPolicy {
	case "":
		out.ReopenPolicy = ReopenManager
	case ReopenAnyone, ReopenManager, ReopenNobody:
	default:
		return Policy{}, settings.Errf(http.StatusUnprocessableEntity,
			"dispatch: reopen_policy must be ANYONE, MANAGER or NOBODY")
	}
	v, err := settings.ValidateVisibility("dispatch", out.LoadValueVisibility)
	if err != nil {
		return Policy{}, err
	}
	out.LoadValueVisibility = v
	if out.CommitHorizonWeeks == 0 {
		out.CommitHorizonWeeks = 8
	}
	if out.CommitHorizonWeeks < 1 || out.CommitHorizonWeeks > 26 {
		return Policy{}, settings.Errf(http.StatusUnprocessableEntity,
			"dispatch: commit_horizon_weeks must be between 1 and 26")
	}
	if out.WorkingViewDays == 0 {
		out.WorkingViewDays = DefaultWorkingViewDays
	}
	if out.WorkingViewDays < 1 || out.WorkingViewDays > 90 {
		return Policy{}, settings.Errf(http.StatusUnprocessableEntity,
			"dispatch: working_view_days must be between 1 and 90")
	}
	switch out.FreightSplitMode {
	case "":
		out.FreightSplitMode = FreightSplitEven
	case FreightSplitEven, FreightSplitManual:
	default:
		return Policy{}, settings.Errf(http.StatusUnprocessableEntity,
			"dispatch: freight_split_mode must be EVEN or MANUAL")
	}
	// THERE IS NO MEANINGFUL ZERO HERE, unlike commit_prompt_hours a few lines up. A zero-hour session
	// is a person who is signed out at the moment they sign in, so an omitted value normalises to the
	// default (today's twelve) rather than being honoured as a deliberate switch-off. A dealer who
	// wants drivers signing in constantly asks for 1, which is a real setting the CHECK admits.
	if out.DriverSessionHours == 0 {
		out.DriverSessionHours = DefaultDriverSessionHours
	}
	if out.DriverSessionHours < 1 || out.DriverSessionHours > MaxDriverSessionHours {
		return Policy{}, settings.Errf(http.StatusUnprocessableEntity,
			"dispatch: driver_session_hours must be between 1 and %d", MaxDriverSessionHours)
	}
	return out, nil
}

// - the gauges -

// Gauge bands. GREEN is room to spare, AMBER is "this is nearly the truck", RED is over.
//
// THE THRESHOLDS ARE NOT ARBITRARY AND THEY ARE NOT SYMMETRIC. Amber starts at 85% because a
// dispatcher needs the warning while there is still time to move a pallet, and red starts at 100%
// because over capacity is over capacity, there is no grace band on a bridge weight.
const (
	BandUnknown = "UNKNOWN"
	BandGreen   = "GREEN"
	BandAmber   = "AMBER"
	BandRed     = "RED"

	amberFrom = 0.85
)

// CapacityBand grades a load against a capacity.
//
// AN UNKNOWN CAPACITY IS UNKNOWN, NEVER GREEN. A truck nobody has measured, or a load nobody has
// weighed, produces BandUnknown and the card draws a dash. Painting the absence of a number green is
// the single most dangerous thing this function could do: it reads as "there is room" to the person
// deciding whether to add a pallet.
func CapacityBand(used, capacity float64) string {
	if capacity <= 0 || math.IsNaN(capacity) || math.IsInf(capacity, 0) {
		return BandUnknown
	}
	if math.IsNaN(used) || math.IsInf(used, 0) || used < 0 {
		return BandUnknown
	}
	switch ratio := used / capacity; {
	case ratio > 1:
		return BandRed
	case ratio >= amberFrom:
		return BandAmber
	default:
		return BandGreen
	}
}

// WorstBand folds several gauges into the one a card shows when it has room for one. RED beats
// AMBER beats GREEN beats UNKNOWN, the pessimistic fold, because a load that is fine on weight and
// over on volume is an over load.
func WorstBand(bands ...string) string {
	worst := BandUnknown
	rank := map[string]int{BandUnknown: 0, BandGreen: 1, BandAmber: 2, BandRed: 3}
	for _, b := range bands {
		if rank[b] > rank[worst] {
			worst = b
		}
	}
	return worst
}

// LoadGauge is one truck's load against one truck's capacity, the numbers behind the two bars on a
// load card, plus the dollar value the client asked for.
//
// EVERY FIGURE IS A POINTER AND EVERY FIGURE IS omitempty, and that is the marshal-layer gate: a
// caller the policy refuses receives a payload with no `value_cents` key at all. Byte-absence, not
// a zero and not a CSS rule. `Known` counts the stops that contributed a real measurement, so the
// surface can say "3 of 5 weighed" instead of implying the total is complete.
type LoadGauge struct {
	StopCount int `json:"stop_count"`

	WeightKg      *float64 `json:"weight_kg,omitempty"`
	CapacityKg    *float64 `json:"capacity_kg,omitempty"`
	WeightBand    string   `json:"weight_band"`
	WeighedStops  int      `json:"weighed_stops"`
	VolumeM3      *float64 `json:"volume_m3,omitempty"`
	CapacityM3    *float64 `json:"capacity_m3,omitempty"`
	VolumeBand    string   `json:"volume_band"`
	MeasuredStops int      `json:"measured_stops"`

	Pallets         *int   `json:"pallets,omitempty"`
	PalletPositions *int   `json:"pallet_positions,omitempty"`
	PalletBand      string `json:"pallet_band"`

	// ValueCents is MONEY and is gated. Absent from the payload entirely for a caller whose role the
	// dealer's load_value_visibility does not allow. ValuedStops is absent with it, a count of how
	// many loads carried a price is itself a hint about the price.
	ValueCents  *int64 `json:"value_cents,omitempty"`
	ValuedStops *int   `json:"valued_stops,omitempty"`

	// FreightCents is this run's share of freight across every order split onto it. Server-computed,
	// always. It is gated with value: a freight figure is a money figure.
	FreightCents *int64 `json:"freight_cents,omitempty"`

	// Band is the pessimistic fold of weight and volume, the one colour a compact card shows.
	Band string `json:"band"`
}

// LoadCapacity is a truck's three capacities, or as many of them as anybody has measured.
type LoadCapacity struct {
	CapacityKg      *float64
	CapacityM3      *float64
	PalletPositions *int
}

// BuildGauge sums a set of stops against a truck's capacities.
//
// A NIL MEASUREMENT IS NOT A ZERO. A stop with no weight on file contributes nothing to the total
// AND is excluded from WeighedStops, so a run of five stops where two were weighed reports the two
// honestly rather than reporting a total that reads complete. This is 0107's rule for the manifest,
// applied to the gauge: the manifest prints '-' rather than inventing a total, and so does this.
//
// showValue is the POLICY DECISION, taken by the caller (Service.loadValueAllowed). Passing false
// leaves ValueCents nil, which marshals to nothing.
func BuildGauge(stops []Stop, cap LoadCapacity, showValue bool) LoadGauge {
	g := LoadGauge{StopCount: len(stops)}

	var wSum, vSum float64
	var pSum int
	var valSum, frtSum int64
	valued := 0
	pallets := 0
	for _, s := range stops {
		if s.WeightKg != nil {
			wSum += *s.WeightKg
			g.WeighedStops++
		}
		if s.VolumeM3 != nil {
			vSum += *s.VolumeM3
			g.MeasuredStops++
		}
		if s.PalletPositions != nil {
			pSum += *s.PalletPositions
			pallets++
		}
		if s.ValueCents != nil {
			valSum += *s.ValueCents
			valued++
		}
		if s.FreightCents != nil {
			frtSum += *s.FreightCents
		}
	}

	if g.WeighedStops > 0 {
		w := wSum
		g.WeightKg = &w
	}
	if g.MeasuredStops > 0 {
		v := vSum
		g.VolumeM3 = &v
	}
	if pallets > 0 {
		p := pSum
		g.Pallets = &p
	}
	g.CapacityKg, g.CapacityM3, g.PalletPositions = cap.CapacityKg, cap.CapacityM3, cap.PalletPositions

	g.WeightBand = bandFor(g.WeightKg, cap.CapacityKg)
	g.VolumeBand = bandFor(g.VolumeM3, cap.CapacityM3)
	g.PalletBand = bandForInt(g.Pallets, cap.PalletPositions)
	g.Band = WorstBand(g.WeightBand, g.VolumeBand, g.PalletBand)

	if showValue {
		if valued > 0 {
			v := valSum
			g.ValueCents = &v
		}
		n := valued
		g.ValuedStops = &n
		f := frtSum
		g.FreightCents = &f
	}
	return g
}

func bandFor(used, capacity *float64) string {
	if used == nil || capacity == nil {
		return BandUnknown
	}
	return CapacityBand(*used, *capacity)
}

func bandForInt(used, capacity *int) string {
	if used == nil || capacity == nil {
		return BandUnknown
	}
	return CapacityBand(float64(*used), float64(*capacity))
}

// - the state machine (0185) -
//
// ONE ACT, TWO STATES, AND A REOPEN THAT IS ORDINARY.
//
//	REQUESTED   a salesperson picked a date and it went on the board. Nobody has promised it, the
//	            re-optimisation may shuffle it freely, and anybody who can edit the board may change it.
//	COMMITTED   a dispatcher put it on a truck and stood behind it. The re-optimisation leaves it
//	            alone; changing it is gated; a customer has been told.
//
//	COMMITTED → REQUESTED is a REOPEN. A principal's objection IS the requirement, "what if an order
//	comes in that's a better fit for that load?", so it is a first-class, one-action, visible path for
//	a dispatch manager, with a reason recorded. It is a normal Thursday, not an error.
const (
	CommitRequested = "REQUESTED"
	CommitCommitted = "COMMITTED"

	// RunAuto / RunReopened are the RUN's bulk ledger, not a second mechanism: they record that a
	// manager reopened a whole truck. The authority on whether a load is frozen is the LOAD's state.
	RunAuto     = "AUTO"
	RunReopened = "REOPENED"
)

// Legacy aliases kept for one release so a caller that still says DateRequested compiles. The stored
// values never changed, only the names around them did.
const (
	DateRequested = CommitRequested
	DateCommitted = CommitCommitted
)

// ValidateCommitState refuses an unknown state in words. "" normalises to REQUESTED, the SAFE
// direction: a caller who says nothing has not made a promise.
func ValidateCommitState(s string) (string, error) {
	switch s {
	case "":
		return CommitRequested, nil
	case CommitRequested, CommitCommitted:
		return s, nil
	default:
		return "", settings.Errf(http.StatusUnprocessableEntity,
			"dispatch: commit_state must be REQUESTED or COMMITTED")
	}
}

// ValidateDateState is the previous name for the same rule. Kept so no caller breaks on the rename.
func ValidateDateState(s string) (string, error) { return ValidateCommitState(s) }

// WithinHorizon answers whether a date is inside the dealer's PLANNING horizon (commit_horizon_weeks,
// 8 by default). A date outside it is refused at the door with the horizon named, rather than
// accepted onto a board nobody looks at.
//
// THIS IS NOT THE WORKING VIEW. WorkingWindow below is how far the daily workflow reaches; this is
// how far the board plans. Conflating them was the second half of R5's finding.
func WithinHorizon(date string, today time.Time, weeks int) bool {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return false
	}
	if weeks <= 0 {
		weeks = 8
	}
	start := today.UTC().Truncate(24 * time.Hour)
	return !d.Before(start.AddDate(0, 0, -1)) && !d.After(start.AddDate(0, 0, weeks*7))
}

// WorkingWindow is the dispatcher's DAILY VIEW, as dates: from the board's date, forward
// working_view_days. The dealer asked "t-minus 24, 28 or 48 hours?" and was told it needs to be more
// than 48 hours (one principal) and "almost a couple weeks" (another). Fourteen days is the default
// and it is its own knob.
func WorkingWindow(fromDate string, days int) (string, string) {
	if days <= 0 {
		days = DefaultWorkingViewDays
	}
	d, err := time.Parse("2006-01-02", fromDate)
	if err != nil {
		return fromDate, fromDate
	}
	return fromDate, d.AddDate(0, 0, days-1).Format("2006-01-02")
}

// - the commitment, and who may move it -

// ErrLoadCommitted, the requested change touches a COMMITTED load and this caller may not pass.
// 409, never 403: the caller has the authority to edit loads in general, the LOAD is in a state that
// refuses this one, and reopening it is the way forward.
var ErrLoadCommitted = errors.New("dispatch: this load is committed")

// ErrCommitNotPermitted, a salesperson tried to commit. A dealer principal: "I don't think that the
// salesperson has any business even trying to attempt because our dispatchers definitely do not want
// our people trying to overpromise or overcommit to something that they know nothing about."
var ErrCommitNotPermitted = errors.New("dispatch: a salesperson can request a delivery date, only dispatch can commit one")

// ErrReopenNotPermitted, somebody who may commit, but is not a dispatch manager, asked to reopen.
var ErrReopenNotPermitted = errors.New("dispatch: only a dispatch manager can reopen a committed load")

// ErrReopenDisabled, the dealer runs NOBODY: a commitment is final and nobody has a key. It is a
// SEPARATE error from the one above because the two refusals need different sentences, telling a
// general manager to "ask a dispatch manager" when the dealer has switched reopening off entirely
// sends them looking for a person who cannot help either.
var ErrReopenDisabled = errors.New("dispatch: this dealer does not allow a committed load to be reopened")

// LoadCommit is the computed answer for one truck's lane, and the shape the board renders.
//
// FOUR BOOLEANS AND THEY ARE FOUR DIFFERENT FACTS. Committed is about the LOAD; Reopenable is about
// the DEALER'S POLICY; CanReopen and CanCommit are about THIS CALLER. A board that drew its Reopen
// control off the policy alone would offer a driver a button that answers 403, the reading-to-be-safe
// defect, found in the live build during 0184's walkthrough and not reintroduced here.
type LoadCommit struct {
	Committed  bool `json:"committed"`
	Reopenable bool `json:"reopenable"`
	CanReopen  bool `json:"can_reopen"`
	// CanCommit is the salesperson gate on the surface: a control they can never use is never drawn.
	CanCommit bool   `json:"can_commit"`
	Reason    string `json:"reason,omitempty"`
	// DueToCommit is the 48 hours in its only remaining role: this truck still carries asks and the
	// delivery day is close. It is a PROMPT. It refuses nothing.
	DueToCommit  bool   `json:"due_to_commit"`
	PromptHours  int    `json:"prompt_hours"`
	Reopened     bool   `json:"reopened"`
	ReopenedAt   string `json:"reopened_at,omitempty"`
	ReopenReason string `json:"reopen_reason,omitempty"`
}

// DueToCommit answers the prompt: is a still-REQUESTED load close enough to its delivery day that a
// dispatcher should be looking at it?
//
// A DATE THAT WILL NOT PARSE PROMPTS. That is the opposite of 0184's fail-closed on the same
// arithmetic, and deliberately: this is no longer a gate. A prompt that fires on a broken row puts it
// in front of a person; a prompt that stays silent hides it.
func DueToCommit(deliveryDate string, now time.Time, promptHours int) bool {
	if promptHours <= 0 {
		return false
	}
	d, err := time.Parse("2006-01-02", deliveryDate)
	if err != nil {
		return true
	}
	return !now.Before(d.Add(-time.Duration(promptHours) * time.Hour))
}

// EvaluateCommit grades ONE RUN from the loads on it.
//
// THE RUN HAS NO STATE OF ITS OWN. `committed` and `requested` are counts of the run's live loads,
// and the verdict is read off them: a truck carrying a committed load is a committed truck. That is
// what makes this one mechanism rather than two, there is no stored run boolean to go stale at
// midnight, and no clock in the verdict at all.
func EvaluateCommit(runDate, runState string, committed, requested int, policy Policy, now time.Time) LoadCommit {
	st := LoadCommit{
		PromptHours: policy.CommitPromptHours,
		Reopened:    runState == RunReopened,
		Reopenable:  policy.ReopenPolicy != ReopenNobody,
		Committed:   committed > 0,
		DueToCommit: requested > 0 && DueToCommit(runDate, now, policy.CommitPromptHours),
	}
	if st.Committed {
		st.Reason = fmt.Sprintf("%s committed, reopen it to move what is on it.", plural(committed, "load"))
	}
	return st
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// - freight split -

// EvenSplit divides a freight total across n loads with NO CENT LOST AND NONE INVENTED.
//
// The remainder rides on the EARLIEST loads, one cent each, rather than on the last: the first truck
// out is the one whose paperwork gets checked first, and a rounding cent that appears on the final
// load looks like a correction somebody made. 100 cents over 3 loads is 34/33/33, and the three
// always sum to 100.
//
// This function is the ONLY producer of a default split, and it runs on the server. The client can
// ask for "split into three"; it can never send the three numbers and have them believed.
func EvenSplit(totalCents int64, n int) []int64 {
	if n <= 0 {
		return nil
	}
	out := make([]int64, n)
	if totalCents == 0 {
		return out
	}
	sign := int64(1)
	if totalCents < 0 {
		sign, totalCents = -1, -totalCents
	}
	base := totalCents / int64(n)
	rem := totalCents % int64(n)
	for i := range out {
		out[i] = base * sign
		if int64(i) < rem {
			out[i] += sign
		}
	}
	return out
}

// ValidateOverride refuses a manual split that does not add up. THE SUM IS THE WHOLE POINT: an
// override that is a dollar light is an invoice that is a dollar light, and the client sent it, so
// the server is the only thing standing between a typo and the ledger.
func ValidateOverride(amounts []int64, totalCents int64) error {
	if len(amounts) == 0 {
		return settings.Errf(http.StatusUnprocessableEntity, "dispatch: an override needs an amount for every load")
	}
	var sum int64
	for _, a := range amounts {
		if a < 0 {
			return settings.Errf(http.StatusUnprocessableEntity, "dispatch: a load's freight cannot be negative")
		}
		sum += a
	}
	if sum != totalCents {
		return settings.Errf(http.StatusUnprocessableEntity,
			"dispatch: the split comes to %s and the order's freight is %s, they have to match",
			money(sum), money(totalCents))
	}
	return nil
}

// money renders cents for a REFUSAL SENTENCE only. It is not a formatter for a payload: figures on
// the wire are integer cents and the surface formats them.
func money(cents int64) string {
	neg := ""
	if cents < 0 {
		neg, cents = "-", -cents
	}
	return fmt.Sprintf("%s$%d.%02d", neg, cents/100, cents%100)
}

// - procurement gaps -

// ProcurementGap is one line on a load the yard does not have enough of. It exists so a shortage is
// discovered on the board at 6:45am, not at the forklift at 9:20 with a truck waiting.
type ProcurementGap struct {
	StopID      string  `json:"stop_id"`
	OrderNo     string  `json:"order_no,omitempty"`
	SKU         string  `json:"sku"`
	Description string  `json:"description,omitempty"`
	QtyNeeded   float64 `json:"qty_needed"`
	QtyOnHand   float64 `json:"qty_on_hand"`
	QtyShort    float64 `json:"qty_short"`
	UOM         string  `json:"uom,omitempty"`
}

// StockAvailabilityReader is the consumer-declared seam for "how much of this is actually here".
// Satisfied in the composition root by an adapter over inventory. It is deliberately NOT part of
// OrderLineReader: what is ON an order and what is IN the yard are different questions with
// different failure modes, and a board that cannot reach inventory must still draw.
//
// CONTRACT: an unknown product answers 0 with a nil error, an item nobody stocks is short by
// definition, which is the honest reading, not an outage.
type StockAvailabilityReader interface {
	OnHandBase(ctx context.Context, productID, branchID string) (float64, error)
}

// GapsFor folds lines and on-hand into the shortages. A line whose need is met produces nothing -
// the card shows gaps or shows nothing, never a wall of green rows.
func GapsFor(stopID, orderNo string, lines []PickFeedLine, onHand map[string]float64) []ProcurementGap {
	out := []ProcurementGap{}
	for _, l := range lines {
		need := parseQty(l.QtyOrdered)
		if need <= 0 {
			continue
		}
		have := onHand[l.ProductID]
		if have >= need {
			continue
		}
		out = append(out, ProcurementGap{
			StopID: stopID, OrderNo: orderNo, SKU: l.SKU, Description: l.Description,
			QtyNeeded: need, QtyOnHand: have, QtyShort: need - have, UOM: l.UOM,
		})
	}
	return out
}

func parseQty(s string) float64 {
	var f float64
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%g", &f); err != nil {
		return 0
	}
	return f
}
