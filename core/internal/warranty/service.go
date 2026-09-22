package warranty

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// service.go, the warranty domain orchestration: registry CRUD (FR-1), claim capture with #WTY-n assignment +
// back-dating (FR-2/FR-9), the checklist-gated Kanban phase machine (FR-3) with the Submitted SLA stamp, the
// evidence-reference seam (FR-4), and manual settlement recording with the GL hand-off seam (FR-5). Table access
// is entirely through the warrantyStore seam. The FR-7 reports live in reporting.go.

const (
	actWarrantySettle   = "WARRANTY_SETTLE"
	actWarrantyEvidence = "WARRANTY_EVIDENCE"
)

// - FR-1 vendor registry -

// UpsertVendorProfile creates or updates a vendor's warranty profile (admin-gated at the route). An
// UNDOCUMENTED status is explicitly allowed (the Oaks case), the registry LEARNS as terms are captured.
func (s *Service) UpsertVendorProfile(ctx context.Context, in VendorProfileInput) (VendorProfile, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return VendorProfile{}, err
	}
	if strings.TrimSpace(in.VendorID) == "" {
		return VendorProfile{}, unprocessable("warranty: vendor_id is required")
	}
	if in.Status == "" {
		in.Status = "UNDOCUMENTED"
	}
	if in.Status != "DOCUMENTED" && in.Status != "UNDOCUMENTED" {
		return VendorProfile{}, unprocessable("warranty: status must be DOCUMENTED or UNDOCUMENTED")
	}
	if in.Channel == "" {
		in.Channel = "EMAIL"
	}
	if in.TermMonths != nil && *in.TermMonths < 0 {
		return VendorProfile{}, unprocessable("warranty: term_months must be >= 0")
	}
	return s.store.UpsertVendorProfile(ctx, in, caller.UserID)
}

// GetVendorProfile reads one vendor's profile (with its product overrides).
func (s *Service) GetVendorProfile(ctx context.Context, vendorID string) (VendorProfile, error) {
	p, err := s.store.GetVendorProfileByVendor(ctx, vendorID)
	if err != nil {
		return VendorProfile{}, err
	}
	ov, err := s.store.OverridesForProfile(ctx, p.ID)
	if err != nil {
		return VendorProfile{}, err
	}
	p.Overrides = ov
	return p, nil
}

// ListVendorProfiles lists every vendor warranty profile (the registry admin screen).
func (s *Service) ListVendorProfiles(ctx context.Context) ([]VendorProfile, error) {
	return s.store.ListVendorProfiles(ctx)
}

// AddProductOverride records a per-product override on a vendor profile.
func (s *Service) AddProductOverride(ctx context.Context, in ProductOverrideInput) (ProductOverride, error) {
	if _, err := s.caller(ctx); err != nil {
		return ProductOverride{}, err
	}
	if strings.TrimSpace(in.ProfileID) == "" || strings.TrimSpace(in.ProductID) == "" {
		return ProductOverride{}, unprocessable("warranty: profile_id and product_id are required")
	}
	return s.store.AddProductOverride(ctx, in)
}

// - FR-2 claim capture -

// CreateClaim captures a warranty claim from the office/counter form OR from an invoice/order line ("start
// claim"). It assigns the next #WTY-n, defaults the owner to the caller when none is given, and records the
// CREATED audit-spine event. OpenedAt supports BACK-DATING (FR-9): the live 6-8 claims enter with their TRUE
// opened date so the reports (time-to-settlement, age) are honest on day one.
func (s *Service) CreateClaim(ctx context.Context, in ClaimInput) (Claim, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Claim{}, err
	}
	if strings.TrimSpace(in.BranchID) == "" {
		return Claim{}, unprocessable("warranty: branch_id is required")
	}
	// Scope gate at the write choke-point (IDOR): a branch-scoped staffer cannot capture a claim into a branch
	// they cannot reach, otherwise they could plant (and thereafter own) a claim in another branch.
	if !caller.CanAccessBranch(in.BranchID) {
		return Claim{}, forbidden("warranty: caller cannot access branch %s", in.BranchID)
	}
	if in.Source == "" {
		in.Source = SourceIntake
	}
	if in.Source != SourceIntake && in.Source != SourceCounter && in.Source != SourceWeb {
		return Claim{}, unprocessable("warranty: source must be INTAKE, COUNTER, or WEB")
	}
	if in.OwnerID == "" {
		in.OwnerID = caller.UserID // every claim gets an owner (FR-2), default to the capturing staff member
	}

	// Back-dating (FR-9): an explicit opened_at is honoured; otherwise the claim opens now.
	openedAt := s.clock()
	if strings.TrimSpace(in.OpenedAt) != "" {
		t, perr := parseFlexTime(in.OpenedAt)
		if perr != nil {
			return Claim{}, unprocessable("warranty: opened_at must be an ISO-8601 date/time")
		}
		if t.After(s.clock().Add(24 * time.Hour)) {
			return Claim{}, unprocessable("warranty: opened_at cannot be in the future")
		}
		openedAt = t
	}

	// claim_no ("" here) is assigned atomically inside InsertClaim under an advisory lock, so concurrent captures
	// get sequential, collision-free #WTY-n numbers rather than one capture 500ing on the UNIQUE constraint.
	claim, err := s.store.InsertClaim(ctx, in, "", openedAt, caller.UserID)
	if err != nil {
		return Claim{}, err
	}

	// CREATED audit-spine event, timestamped at the claim's TRUE open time (back-date carries through).
	if _, err := s.store.InsertEvent(ctx, ClaimEvent{
		ClaimID:    claim.ID,
		EventType:  "CREATED",
		ToPhase:    PhaseIntake,
		ActorID:    caller.UserID,
		Note:       fmt.Sprintf("Claim captured (%s).", in.Source),
		OccurredAt: openedAt.Format(time.RFC3339),
	}); err != nil {
		return Claim{}, err
	}

	claim.Lines, _ = s.store.ClaimLines(ctx, claim.ID)
	return claim, nil
}

// GetClaim reads one claim with its lines.
//
// AUTHZ (the horizontal-access boundary, mirrors order/history.go:113 + till/history.go:211): route
// staff-gating proves the caller is staff, NOT that they may reach THIS claim. A branch-scoped staffer
// must not read a claim in a branch they cannot access by iterating ids (an IDOR gap). So we resolve the
// trusted, server-stamped caller and gate on caller.CanAccessBranch(claim.BranchID): a GLOBAL caller reaches
// any branch; a scoped caller only their grants. A MISSING claim returns not-found (never a leak of existence
// via the authz path); an EXISTING claim in an unreachable branch returns forbidden.
func (s *Service) GetClaim(ctx context.Context, id string) (Claim, error) {
	_, claim, err := s.callerScopedClaim(ctx, id)
	if err != nil {
		return Claim{}, err
	}
	claim.Lines, err = s.store.ClaimLines(ctx, id)
	if err != nil {
		return Claim{}, err
	}
	return claim, nil
}

// callerScopedClaim resolves the trusted caller, loads the claim, and gates branch access BEFORE any
// caller-visible data is returned. It is the single choke-point every per-claim read/mutation funnels through
// so no warranty path crosses a branch boundary by id-iteration (IDOR). A missing claim reports not-found
// (no existence leak); an existing claim the caller cannot reach reports forbidden.
func (s *Service) callerScopedClaim(ctx context.Context, id string) (identity.StaffContext, Claim, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return identity.StaffContext{}, Claim{}, err
	}
	claim, err := s.store.GetClaim(ctx, id)
	if err != nil {
		return identity.StaffContext{}, Claim{}, err // errClaimNotFound for a missing claim, no existence leak
	}
	if !caller.CanAccessBranch(claim.BranchID) {
		return identity.StaffContext{}, Claim{}, forbidden("warranty: caller cannot access branch %s", claim.BranchID)
	}
	return caller, claim, nil
}

// ListClaims lists claims for the board / list views (filterable by branch/phase/vendor). It is branch-scoped:
// a GLOBAL caller sees every branch; a scoped caller sees only claims in branches they can reach (an explicit
// branch filter they cannot access is forbidden). The scope is enforced in the service so both the SQL list and
// the DB-free fake are guarded identically.
func (s *Service) ListClaims(ctx context.Context, f ClaimFilter) ([]Claim, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	if f.BranchID != "" && !caller.CanAccessBranch(f.BranchID) {
		return nil, forbidden("warranty: caller cannot access branch %s", f.BranchID)
	}
	claims, err := s.store.ListClaims(ctx, f)
	if err != nil {
		return nil, err
	}
	if caller.Scope == identity.ScopeGlobal {
		return claims, nil
	}
	// Scoped caller with no (or an accessible) branch filter: drop any claim outside their grants so a
	// list/board load never leaks a cross-branch claim.
	out := claims[:0]
	for _, c := range claims {
		if caller.CanAccessBranch(c.BranchID) {
			out = append(out, c)
		}
	}
	return out, nil
}

// AssignClaim sets a claim's vendor and/or owner (the FR-2 "give it a vendor or UNKNOWN, give it an owner" +
// the Triage step where a real vendor is identified). An empty vendorID leaves the vendor UNKNOWN.
func (s *Service) AssignClaim(ctx context.Context, claimID, vendorID, ownerID string) (Claim, error) {
	caller, claim, err := s.callerScopedClaim(ctx, claimID)
	if err != nil {
		return Claim{}, err
	}
	// Resolve blanks to the existing values so a partial assign doesn't wipe a field.
	if vendorID == "" {
		vendorID = claim.VendorID
	}
	if ownerID == "" {
		ownerID = claim.OwnerID
	}
	if err := s.store.UpdateClaimVendorOwner(ctx, claimID, vendorID, ownerID); err != nil {
		return Claim{}, err
	}
	if _, err := s.store.InsertEvent(ctx, ClaimEvent{
		ClaimID:    claimID,
		EventType:  "NOTE",
		ActorID:    caller.UserID,
		Note:       "Vendor/owner assignment updated.",
		OccurredAt: s.clock().Format(time.RFC3339),
	}); err != nil {
		return Claim{}, err
	}
	return s.GetClaim(ctx, claimID)
}

// - FR-3 lifecycle -

// TickChecklist toggles a checklist item on a claim (the per-phase gate items). The tick is recorded as an
// audit-spine CHECKLIST_TICK event carrying the item key (history-stable), so "who ticked what when" is
// reconstructable and the gate reads the current state.
func (s *Service) TickChecklist(ctx context.Context, claimID string, in ChecklistTickInput) (ChecklistState, error) {
	caller, claim, err := s.callerScopedClaim(ctx, claimID)
	if err != nil {
		return ChecklistState{}, err
	}
	if strings.TrimSpace(in.ItemKey) == "" {
		return ChecklistState{}, unprocessable("warranty: item_key is required")
	}
	item, err := s.store.ChecklistItemByKey(ctx, claim.Phase, claim.VendorID, in.ItemKey)
	if err != nil {
		return ChecklistState{}, err
	}
	checked := in.Checked
	if _, err := s.store.InsertEvent(ctx, ClaimEvent{
		ClaimID:         claimID,
		EventType:       "CHECKLIST_TICK",
		ChecklistItemID: item.ID,
		ChecklistKey:    item.ItemKey,
		Checked:         &checked,
		ActorID:         caller.UserID,
		Note:            item.Label,
		OccurredAt:      s.clock().Format(time.RFC3339),
	}); err != nil {
		return ChecklistState{}, err
	}
	return ChecklistState{Item: item, Checked: checked}, nil
}

// Checklist returns the resolved checklist for a claim's CURRENT phase (items + their ticked state), the card
// renders this and uses it to know whether the phase can advance.
func (s *Service) Checklist(ctx context.Context, claimID string) ([]ChecklistState, error) {
	_, claim, err := s.callerScopedClaim(ctx, claimID)
	if err != nil {
		return nil, err
	}
	items, err := s.store.ChecklistForPhase(ctx, claim.Phase, claim.VendorID)
	if err != nil {
		return nil, err
	}
	ticked, err := s.store.TickedKeys(ctx, claimID)
	if err != nil {
		return nil, err
	}
	out := make([]ChecklistState, 0, len(items))
	for _, it := range items {
		out = append(out, ChecklistState{Item: it, Checked: ticked[it.ItemKey]})
	}
	return out, nil
}

// AdvancePhase moves a claim to a new phase (FR-3). It enforces the confirmed transition map AND the checklist
// gate ("a phase can't advance with required items unchecked"). Entering Submitted stamps the sent-date + starts
// the SLA timer (the FR-3 automation; the breach notification is a messaging-hub seam, FR-8, not built in P1).
// Entering a terminal phase stamps closed_at. Every advance writes a PHASE_CHANGE audit-spine event.
func (s *Service) AdvancePhase(ctx context.Context, claimID string, in PhaseTransitionInput) (Claim, error) {
	caller, claim, err := s.callerScopedClaim(ctx, claimID)
	if err != nil {
		return Claim{}, err
	}
	to := strings.TrimSpace(in.ToPhase)
	if !IsKnownPhase(to) {
		return Claim{}, unprocessable("warranty: unknown phase %q", to)
	}
	if IsTerminal(claim.Phase) {
		return Claim{}, unprocessable("warranty: claim %s is already closed (%s)", claim.ClaimNo, claim.Phase)
	}
	if !CanTransition(claim.Phase, to) {
		return Claim{}, unprocessable("warranty: cannot move %s → %s", claim.Phase, to)
	}
	// A SETTLED claim (an outcome has been recorded, with its realized $ + possibly a posted GL journal) may
	// only be CLOSED forward, never diverted to ABANDONED/REJECTED. Without this guard a claim settled with a
	// real recovery (VENDOR_CREDIT, journal posted) could be pushed to a "LOST" exit, whereupon classify()
	// reports it lost (won=0) while its realized recovery still feeds exposure/realized reporting, a corrupt
	// FR-7 (a recovery counted as a loss). The settlement is the decision of record: a settled claim closes.
	if claim.Outcome != "" && (to == PhaseRejected || to == PhaseAbandoned) {
		return Claim{}, unprocessable(
			"warranty: claim %s is settled (%s), a settled claim can only be CLOSED, not %s",
			claim.ClaimNo, claim.Outcome, to)
	}

	// Checklist gate: every REQUIRED item on the CURRENT phase must be ticked before advancing forward. Exit
	// transitions (Rejected/Abandoned) are never gated.
	items, err := s.store.ChecklistForPhase(ctx, claim.Phase, claim.VendorID)
	if err != nil {
		return Claim{}, err
	}
	ticked, err := s.store.TickedKeys(ctx, claimID)
	if err != nil {
		return Claim{}, err
	}
	if gate := GateAdvance(claim.Phase, to, items, ticked); !gate.OK {
		return Claim{}, unprocessable("warranty: cannot advance, required checklist items incomplete: %s",
			strings.Join(gate.Labels, ", "))
	}

	// Back-dating (FR-9, symmetric with capture's opened_at): an explicit occurred_at is honoured so a live claim
	// resolved before go-live enters with a TRUE closed_at/submitted_at, otherwise time-to-settlement and age are
	// inflated to today. Otherwise the transition happens now.
	now := s.clock()
	if strings.TrimSpace(in.OccurredAt) != "" {
		t, perr := parseFlexTime(in.OccurredAt)
		if perr != nil {
			return Claim{}, unprocessable("warranty: occurred_at must be an ISO-8601 date/time")
		}
		if t.After(s.clock().Add(24 * time.Hour)) {
			return Claim{}, unprocessable("warranty: occurred_at cannot be in the future")
		}
		if opened, oerr := parseFlexTime(claim.OpenedAt); oerr == nil && t.Before(opened) {
			return Claim{}, unprocessable("warranty: occurred_at cannot be before the claim was opened")
		}
		now = t
	}

	var submittedAt, slaDue, closedAt *time.Time
	if StampsSLA(to) {
		t := now
		due := now.AddDate(0, 0, defaultSLADays)
		submittedAt = &t
		slaDue = &due
	}
	if IsTerminal(to) {
		t := now
		closedAt = &t
	}

	note := in.Note
	if note == "" {
		note = fmt.Sprintf("Advanced %s → %s", claim.Phase, to)
	}
	// SERIALIZATION POINT (TOCTOU FIX): the phase write + PHASE_CHANGE event happen inside a tx that first
	// SELECT … FOR UPDATE-locks the claim row and RE-CHECKS the mutual-exclusion guard under that lock. The
	// Go-side guard at line ~322 (claim.Outcome != "" && to is a LOST exit) is a fast pre-check off a stale read;
	// it does NOT protect against a settle that commits between that read and this write. guardExit tells the repo
	// this advance is a terminal-LOST exit (ABANDONED/REJECTED), the repo refuses (422) if the claim was settled
	// under the lock, and a concurrent settle symmetrically refuses once THIS terminal phase is committed. So a
	// DECISION claim can never end phase=ABANDONED(LOST) WITH outcome=VENDOR_CREDIT + a posted recovery journal.
	guardExit := to == PhaseRejected || to == PhaseAbandoned
	if err := s.store.AdvanceClaimPhaseGuarded(ctx, claimID, to, guardExit, submittedAt, slaDue, closedAt, ClaimEvent{
		ClaimID:    claimID,
		EventType:  "PHASE_CHANGE",
		FromPhase:  claim.Phase,
		ToPhase:    to,
		ActorID:    caller.UserID,
		Note:       note,
		OccurredAt: now.Format(time.RFC3339),
	}); err != nil {
		return Claim{}, err
	}
	return s.GetClaim(ctx, claimID)
}

// ClaimEvents returns a claim's full audit-spine timeline (phase transitions + checklist ticks + settlement +
// notes), oldest first, the claim-detail timeline reads this. Branch-scoped: a scoped caller cannot read the
// timeline of a claim in a branch they cannot reach (IDOR).
func (s *Service) ClaimEvents(ctx context.Context, claimID string) ([]ClaimEvent, error) {
	if _, _, err := s.callerScopedClaim(ctx, claimID); err != nil {
		return nil, err
	}
	return s.store.EventsForClaim(ctx, claimID)
}

// - FR-4 evidence (seam) -

// AttachEvidence records a MASKED evidence reference on a claim (FR-4). P1 stores the reference shape only via
// the audit-spine EVIDENCE event and mirrors the A-21 sensitive-access record, the real encrypted-at-rest file
// store + upload infra is P2 (documented seam; this never handles bytes). evidence_ref is expected to already be
// a masked store ref (never cleartext).
func (s *Service) AttachEvidence(ctx context.Context, claimID string, in EvidenceInput) (ClaimEvent, error) {
	caller, claim, err := s.callerScopedClaim(ctx, claimID)
	if err != nil {
		return ClaimEvent{}, err
	}
	if strings.TrimSpace(in.EvidenceRef) == "" {
		return ClaimEvent{}, unprocessable("warranty: evidence_ref is required")
	}
	id, err := s.store.InsertEvent(ctx, ClaimEvent{
		ClaimID:     claimID,
		EventType:   "EVIDENCE",
		EvidenceRef: in.EvidenceRef,
		ActorID:     caller.UserID,
		Note:        in.Note,
		OccurredAt:  s.clock().Format(time.RFC3339),
	})
	if err != nil {
		return ClaimEvent{}, err
	}
	// Mirror the A-21 sensitive-access record (the evidence-storage pattern POS/audit use). Best-effort:
	// evidence recording is never blocked by an audit failure in P1 (the event row is the source of truth).
	if s.audit != nil {
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action:     audit.ActEvidenceChange,
			EntityType: "warranty.claim",
			EntityID:   claimID,
			ActorID:    caller.UserID,
			BranchID:   claim.BranchID,
			MaskedRef:  in.EvidenceRef,
			Reason:     "warranty evidence attached",
		})
	}
	return ClaimEvent{ID: id, ClaimID: claimID, EventType: "EVIDENCE", EvidenceRef: in.EvidenceRef, Note: in.Note}, nil
}

// - FR-5 settlement -

// RecordSettlement records a settlement outcome + expected/realized amounts on a claim (FR-5, MANUAL recording
// for P1). It updates the claim's outcome + realized_cents (and expected_cents when supplied), writes a
// SETTLEMENT audit-spine event, and records the A-21 access.
//
// GL WIRING (the documented P1 seam): when a GLPoster is wired, this ATTEMPTS to post the warranty.settled
// journal (recovery credit / expense debit through gl.PostExternal) and stamps the returned journal id on the
// claim. When no poster is wired, OR the post fails, the settlement is still RECORDED with amounts (the claim
// carries expected vs realized either way, the ~$40k backlog becomes measurable) and GLPosted=false with a
// note. Full, reconciled billing-credit-note / inventory-receipt wiring per outcome is P2 (PRD §7), P1
// deliberately RECORDS the settlement and leaves the per-outcome money-path wiring as a clearly-marked seam so
// the default GL accounts (PRD §9 Q5) can be ratified before real journals post at scale.
func (s *Service) RecordSettlement(ctx context.Context, claimID string, in SettlementInput) (SettlementResult, error) {
	caller, claim, err := s.callerScopedClaim(ctx, claimID)
	if err != nil {
		return SettlementResult{}, err
	}
	if !validOutcome(in.Outcome) {
		return SettlementResult{}, unprocessable("warranty: outcome must be one of VENDOR_CREDIT, REPLACEMENT_STOCK, CUSTOMER_ACCOMMODATION, WRITE_OFF")
	}
	if in.RealizedCents < 0 || (in.ExpectedCents != nil && *in.ExpectedCents < 0) {
		return SettlementResult{}, unprocessable("warranty: amounts must be >= 0")
	}
	if IsTerminal(claim.Phase) {
		return SettlementResult{}, unprocessable("warranty: claim %s is already closed", claim.ClaimNo)
	}
	// A settlement is the DECISION-then-SETTLEMENT step of the confirmed lifecycle: the vendor's verdict is in
	// and the money is being booked. Recording one on a claim still in an EARLY phase (INTAKE/TRIAGE/SUBMITTED/
	// WAITING_ON_DECISION) is out of sequence, the outcome would be set before the vendor has ruled, then the
	// claim would carry realized $ while the board still shows it awaiting a decision. Restrict settlement to the
	// DECISION and SETTLEMENT phases so the money only books once the workflow has reached the decision point.
	if claim.Phase != PhaseDecision && claim.Phase != PhaseSettlement {
		return SettlementResult{}, unprocessable(
			"warranty: claim %s cannot be settled from %s, advance to DECISION or SETTLEMENT first",
			claim.ClaimNo, claim.Phase)
	}
	// A settlement records the outcome + money and posts (once) to GL, but does NOT advance the claim to a
	// terminal phase (settle ≠ close is the confirmed two-step design). So IsTerminal(phase) never fires as a
	// re-settle guard. The Go-side outcome read below is only a fast pre-check for the SEQUENTIAL path, it does
	// NOT protect against a concurrent double-submit (two goroutines both read outcome="" before either writes).
	// The AUTHORITATIVE guard is the atomic conditional write in ClaimSettlementWinner below: exactly one
	// concurrent/repeat settle wins there, the rest fall through to 409 having posted nothing.
	if claim.Outcome != "" {
		return SettlementResult{}, conflict("warranty: claim %s is already settled (%s), re-settlement is not permitted in P1", claim.ClaimNo, claim.Outcome)
	}

	// ExpectedCents omitted (nil) keeps the captured exposure; an explicit value (incl. 0) is honoured.
	expected := claim.ExpectedCents
	if in.ExpectedCents != nil {
		expected = *in.ExpectedCents
	}

	res := SettlementResult{
		ClaimID:       claimID,
		Outcome:       in.Outcome,
		ExpectedCents: expected,
		RealizedCents: in.RealizedCents,
		GLNote:        "recorded (P1: GL journal not wired, see PRD §7 P2 / §9 Q5)",
	}

	// SERIALIZATION POINT (fixes the concurrent-double-submit divergence): claim the settlement via a single
	// conditional write BEFORE any GL post. Under any concurrency exactly ONE goroutine wins here (UPDATE …
	// WHERE outcome IS NULL RETURNING); every other one gets won=false and returns 409 having posted NOTHING.
	// The SETTLEMENT audit-spine event is written in the SAME transaction, so a settled claim always has its
	// event. Because the winner is unambiguous BEFORE the GL post, the journal it posts (with ITS amounts)
	// always matches the claim row + the FR-7 reporting money, they can never diverge.
	won, err := s.store.ClaimSettlementWinner(ctx, claimID, in.Outcome, expected, in.RealizedCents, ClaimEvent{
		ClaimID:   claimID,
		EventType: "SETTLEMENT",
		FromPhase: claim.Phase,
		ToPhase:   claim.Phase,
		ActorID:   caller.UserID,
		Note:      in.Note,
		Detail: map[string]any{
			"outcome":        in.Outcome,
			"expected_cents": expected,
			"realized_cents": in.RealizedCents,
		},
		OccurredAt: s.clock().Format(time.RFC3339),
	})
	if err != nil {
		return SettlementResult{}, err
	}
	if !won {
		// Lost the race (or a repeat settle): the claim was already settled by another goroutine, 409, post nothing.
		return SettlementResult{}, conflict("warranty: claim %s is already settled, re-settlement is not permitted in P1", claim.ClaimNo)
	}

	// This goroutine is the unambiguous winner and now OWNS the claim row.
	//
	// ERP:warranty-p2, THE PER-OUTCOME MONEY PATH. P1 asked one question here ("does this outcome+amount move
	// cash?") and posted one generic journal if the answer was yes. That collapsed four different money
	// consequences into two. executeMoneyPath instead runs the ONE path configured for this outcome
	// (warranty_outcome_paths, 0129) and returns a posting row describing exactly what happened, including the
	// three outcomes P1 could not express: a seam that is not bound here, a fallback journal posted in a bound
	// seam's absence, and a bound seam that refused. See moneypath.go.
	//
	// It is non-fatal by design, unchanged from P1: the settlement + amounts are already committed, so a money
	// path that cannot complete downgrades to a STATUS on the posting row rather than an error the manager reads
	// as "your settlement did not save". The claim and the ledger can disagree about paperwork; they can never
	// disagree about what was settled.
	lines, lerr := s.store.ClaimLines(ctx, claimID)
	if lerr != nil {
		// Lines feed only the replacement seam's request payload; losing them must not sink the settlement.
		s.log.Warn("warranty: reading claim lines for the money path failed", "claim", claim.ClaimNo, "err", lerr)
	}
	posting, perr := s.executeMoneyPath(ctx, claim, lines, in.Outcome, in.RealizedCents, in.ManualOverride, in.OverrideReason)
	if perr != nil {
		// A CONFIGURATION failure (no warranty_outcome_paths row for this outcome). Refusing to invent a path is
		// the whole point; the settlement stays recorded and the note says why nothing moved.
		s.log.Error("warranty: no money path configured for outcome", "claim", claim.ClaimNo, "outcome", in.Outcome, "err", perr)
		res.GLNote = "recorded; no money path is configured for outcome " + in.Outcome + ", nothing was posted"
		return res, nil
	}

	if id, ierr := s.store.InsertSettlementPosting(ctx, posting); ierr != nil {
		// The path ran; recording it failed. Log loudly, a lost accrual understates the recovery board, but do
		// not fail the call: the money movement itself already happened.
		s.log.Error("warranty: recording the settlement posting failed", "claim", claim.ClaimNo, "path", posting.MoneyPath, "err", ierr)
	} else {
		posting.ID = id
	}

	res.MoneyPath = posting.MoneyPath
	res.PathSeam = posting.Seam
	res.PathStatus = posting.Status
	res.ExternalRef = posting.ExternalRef
	res.GLJournalID = posting.GLJournalID
	res.GLPosted = posting.Status == PathPosted || posting.Status == PathFallbackGL
	res.GLNote = moneyPathNote(posting)
	if posting.AccruesRecovery {
		res.AccruedCents = posting.AmountCents
	}
	// Stamp the journal id on the claim when this path produced one, so the P1 claim-level gl_journal_id keeps
	// meaning what it always meant.
	if posting.GLJournalID != "" {
		if serr := s.store.StampSettlementJournal(ctx, claimID, posting.GLJournalID); serr != nil {
			s.log.Warn("warranty: stamping settlement journal id failed (settlement + journal both recorded)", "claim", claim.ClaimNo, "err", serr)
		}
	}

	// A-21 access record. Best-effort (log-and-continue), consistent with the evidence path: the settlement is
	// already durably recorded (claim + SETTLEMENT event committed), so an audit-sink hiccup must not report the
	// call as failed after the money is booked.
	if s.audit != nil {
		if aerr := s.audit.Record(ctx, audit.SensitiveAccess{
			Action:     actWarrantySettle,
			EntityType: "warranty.claim",
			EntityID:   claimID,
			ActorID:    caller.UserID,
			BranchID:   claim.BranchID,
			Reason:     in.Outcome,
			Extra: map[string]any{
				"expected_cents": expected,
				"realized_cents": in.RealizedCents,
				"gl_journal_id":  res.GLJournalID,
			},
		}); aerr != nil {
			s.log.Warn("warranty: settlement audit record failed (settlement recorded)", "claim", claim.ClaimNo, "err", aerr)
		}
	}
	return res, nil
}

// - helpers -

func validOutcome(o string) bool {
	switch o {
	case OutcomeVendorCredit, OutcomeReplacementStock, OutcomeCustomerAccommodation, OutcomeWriteOff:
		return true
	}
	return false
}

// moneyPathNote renders the human sentence the settle response carries in gl_note. It is derived from the
// posting row's STATUS so the note, the status and gl_posted can never disagree, the P1 lesson (a $0 write-off
// reporting "GL journal not wired" when the seam was wired and there was simply nothing to post).
func moneyPathNote(p SettlementPosting) string {
	switch p.Status {
	case PathPosted:
		if p.ExternalRef != "" {
			return "recorded; " + p.MoneyPath + " posted, " + p.ExternalRef
		}
		return "recorded; " + p.MoneyPath + " posted to GL"
	case PathFallbackGL:
		return "recorded; " + p.MoneyPath + ", " + p.Note
	case PathNoEffect:
		return "recorded; no money moved, " + p.Note
	case PathSeamUnwired:
		return "recorded; " + p.MoneyPath + " seam (" + p.Seam + ") is not wired here, the request is preserved for replay"
	case PathFailed:
		return "recorded; " + p.MoneyPath + " failed, " + p.ErrorText
	case PathManual:
		return "recorded; manual settlement override, the automatic " + p.MoneyPath + " path was not run"
	default:
		return "recorded"
	}
}

// parseFlexTime accepts an RFC3339 timestamp OR a bare YYYY-MM-DD date (the back-date form supplies a date).
func parseFlexTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", s)
}
