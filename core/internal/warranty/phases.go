package warranty

import "sort"

// phases.go, PURE FR-3 Kanban lifecycle: the CLIENT-CONFIRMED phase sequence (verbatim, 2026-07-07 weekly
// sync), the legal transition map, the checklist-gate rule ("a phase can't advance with required items
// unchecked"), and the win/loss + age classification helpers. Kept DB-free so the phase machine + gating are
// exhaustively unit-testable without a database.

// The CONFIRMED phases (2026-07-07 weekly sync, Ryan B. had nothing to change), verbatim:
//
//	Intake → Triage → Submitted → Waiting on Decision → Decision → Settlement → Closed
//
// plus the Rejected / Abandoned exits.
const (
	PhaseIntake            = "INTAKE"
	PhaseTriage            = "TRIAGE"
	PhaseSubmitted         = "SUBMITTED"
	PhaseWaitingOnDecision = "WAITING_ON_DECISION"
	PhaseDecision          = "DECISION"
	PhaseSettlement        = "SETTLEMENT"
	PhaseClosed            = "CLOSED"
	PhaseRejected          = "REJECTED"
	PhaseAbandoned         = "ABANDONED"
)

// Sources (the FR-2 doorways). WEB is P2 (public dibbits.ca intake), the enum exists so the P2 wire-up needs
// no schema change; P1 accepts INTAKE (office) + COUNTER (from an invoice/order line).
const (
	SourceIntake  = "INTAKE"
	SourceCounter = "COUNTER"
	SourceWeb     = "WEB"
)

// Outcomes (FR-5).
const (
	OutcomeVendorCredit          = "VENDOR_CREDIT"
	OutcomeReplacementStock      = "REPLACEMENT_STOCK"
	OutcomeCustomerAccommodation = "CUSTOMER_ACCOMMODATION"
	OutcomeWriteOff              = "WRITE_OFF"
)

// orderedPhases is the confirmed forward sequence (the Kanban columns, left→right). The board renders in this
// order; reporting funnels count in this order.
var orderedPhases = []string{
	PhaseIntake, PhaseTriage, PhaseSubmitted, PhaseWaitingOnDecision,
	PhaseDecision, PhaseSettlement, PhaseClosed,
}

// KanbanColumns is the full column set the board renders: the confirmed forward sequence plus the two exit
// columns (Rejected / Abandoned).
func KanbanColumns() []string {
	return append(append([]string{}, orderedPhases...), PhaseRejected, PhaseAbandoned)
}

// terminalPhases are the closed states (a claim here is done; entering one stamps closed_at).
var terminalPhases = map[string]bool{
	PhaseClosed: true, PhaseRejected: true, PhaseAbandoned: true,
}

// IsTerminal reports whether a phase is a closed state.
func IsTerminal(phase string) bool { return terminalPhases[phase] }

// allowedTransitions is the legal phase-transition map. The forward path follows the confirmed sequence; every
// working (non-terminal) phase may exit to Rejected or Abandoned (the client's exits); Settlement/Decision may
// close forward. A backward "kickback" to Triage is allowed from the vendor-facing phases (real desks bounce a
// claim back when the vendor asks for more info), a documented, conservative allowance, not a client ruling.
var allowedTransitions = map[string][]string{
	PhaseIntake:            {PhaseTriage, PhaseAbandoned},
	PhaseTriage:            {PhaseSubmitted, PhaseRejected, PhaseAbandoned},
	PhaseSubmitted:         {PhaseWaitingOnDecision, PhaseTriage, PhaseRejected, PhaseAbandoned},
	PhaseWaitingOnDecision: {PhaseDecision, PhaseTriage, PhaseRejected, PhaseAbandoned},
	PhaseDecision:          {PhaseSettlement, PhaseRejected, PhaseAbandoned},
	PhaseSettlement:        {PhaseClosed, PhaseAbandoned},
	// terminal phases have no outbound transitions
	PhaseClosed:    {},
	PhaseRejected:  {},
	PhaseAbandoned: {},
}

// IsKnownPhase reports whether s is a valid phase.
func IsKnownPhase(s string) bool {
	if terminalPhases[s] {
		return true
	}
	_, ok := allowedTransitions[s]
	return ok
}

// CanTransition reports whether from→to is a legal transition.
func CanTransition(from, to string) bool {
	for _, t := range allowedTransitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// StampsSLA reports whether entering `to` starts the SLA timer (FR-3 automation: entering Submitted stamps the
// vendor + sent-date and starts the SLA clock).
func StampsSLA(to string) bool { return to == PhaseSubmitted }

// defaultSLADays is the SLA window stamped on Submitted (FR-3). DOCUMENTED PLACEHOLDER, vendor-specific SLAs
// (e.g. "Techo answers in 2 weeks") are a residual OPEN item (PRD §9 Q3), so a single default is used until the
// per-vendor SLA table lands (P2). The notification on breach routes through the messaging hub when it ships
// (FR-8), left as a seam here (no scheduler infra in P1).
const defaultSLADays = 14

// GateResult reports whether a phase may advance, and which required checklist items block it.
type GateResult struct {
	OK      bool     `json:"ok"`
	Missing []string `json:"missing"` // item_keys of the unticked REQUIRED items on the FROM phase
	Labels  []string `json:"labels"`  // human labels for the missing items (for the error message)
}

// GateAdvance is the FR-3 checklist gate: a phase can't advance with required items unchecked. Given the FROM
// phase's checklist items and the set of ticked item_keys on the claim, it reports whether every REQUIRED item
// is ticked. Exit transitions (Rejected/Abandoned) are NOT gated, you can always kill/park a claim.
func GateAdvance(from, to string, items []ChecklistItem, ticked map[string]bool) GateResult {
	// Abandoning or rejecting is never gated, a claim can always be killed/parked regardless of checklist.
	if to == PhaseRejected || to == PhaseAbandoned {
		return GateResult{OK: true}
	}
	res := GateResult{OK: true}
	for _, it := range items {
		if it.Phase != from || !it.Required {
			continue
		}
		if !ticked[it.ItemKey] {
			res.OK = false
			res.Missing = append(res.Missing, it.ItemKey)
			res.Labels = append(res.Labels, it.Label)
		}
	}
	return res
}

// - win/loss classification (FR-7) -

// ClaimDisposition classifies a claim's decided state for win/loss reporting.
type ClaimDisposition int

const (
	DispositionOpen ClaimDisposition = iota // still in flight (not a decided outcome)
	DispositionWon                          // settled favourably: a credit/replacement/accommodation was realized
	DispositionLost                         // rejected / abandoned / written off, nothing recovered for us
)

// classify decides won/lost/open from a claim's phase + outcome. A CLOSED claim counts as WON when its outcome
// recovered value (vendor credit / replacement stock / customer accommodation); a WRITE_OFF, REJECTED, or
// ABANDONED claim is LOST. Anything still in a working phase is OPEN.
func classify(phase, outcome string) ClaimDisposition {
	switch phase {
	case PhaseRejected, PhaseAbandoned:
		return DispositionLost
	case PhaseClosed:
		switch outcome {
		case OutcomeVendorCredit, OutcomeReplacementStock, OutcomeCustomerAccommodation:
			return DispositionWon
		case OutcomeWriteOff:
			return DispositionLost
		default:
			// closed with no recovery outcome recorded, count as lost (nothing came back)
			return DispositionLost
		}
	default:
		return DispositionOpen
	}
}

// sortChecklist orders checklist items for stable rendering: by sort_order, then item_key.
func sortChecklist(items []ChecklistItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].SortOrder != items[j].SortOrder {
			return items[i].SortOrder < items[j].SortOrder
		}
		return items[i].ItemKey < items[j].ItemKey
	})
}

// ageBucket classifies an open-claim age (days) into the reporting buckets (mirrors lien's aging buckets).
func ageBucket(days int) string {
	switch {
	case days <= 30:
		return "0-30"
	case days <= 60:
		return "31-60"
	case days <= 90:
		return "61-90"
	default:
		return "90+"
	}
}
