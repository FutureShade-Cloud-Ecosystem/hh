package warranty

import (
	"context"
	"sort"
	"strings"
	"time"
)

// fakestore_w5b_test.go, the DB-free half of the W5-B store seam, so the money paths, the recovery arithmetic,
// the intake refusals and the SLA timers are all unit-testable without Postgres. The fake mirrors the SQL
// contracts that matter: the (claim_id, money_path) uniqueness of 0129, the outstanding = accrued − received
// definition of the 0130 view, the atomic increment of the 0131 rate limiter, and the (claim_id, phase)
// uniqueness of 0132.

// defaultOutcomePaths mirrors the four rows 0129 seeds.
func defaultOutcomePaths() map[string]MoneyPathConfig {
	return map[string]MoneyPathConfig{
		OutcomeVendorCredit: {Outcome: OutcomeVendorCredit, MoneyPath: PathVendorRecovery, Seam: SeamGL,
			DebitAccount: "1100", CreditAccount: "4900", AccruesRecovery: true},
		OutcomeCustomerAccommodation: {Outcome: OutcomeCustomerAccommodation, MoneyPath: PathCustomerCredit, Seam: SeamBilling,
			DebitAccount: "5900", CreditAccount: "1100"},
		OutcomeReplacementStock: {Outcome: OutcomeReplacementStock, MoneyPath: PathInventoryReplacement, Seam: SeamInventory},
		OutcomeWriteOff: {Outcome: OutcomeWriteOff, MoneyPath: PathExpenseWriteOff, Seam: SeamGL,
			DebitAccount: "5900", CreditAccount: "1100"},
	}
}

// defaultSLATargets mirrors the six defaults 0132 seeds.
func defaultSLATargets() []SLATarget {
	return []SLATarget{
		{Phase: PhaseIntake, TargetHours: 24, IsActive: true},
		{Phase: PhaseTriage, TargetHours: 48, IsActive: true},
		{Phase: PhaseSubmitted, TargetHours: 72, IsActive: true},
		{Phase: PhaseWaitingOnDecision, TargetHours: 336, IsActive: true},
		{Phase: PhaseDecision, TargetHours: 48, IsActive: true},
		{Phase: PhaseSettlement, TargetHours: 120, IsActive: true},
	}
}

// defaultIntakeConfig mirrors the 0131 singleton.
func defaultIntakeConfig() IntakeConfig {
	return IntakeConfig{
		IsEnabled: true, MaxBodyBytes: 8 << 20, MaxPhotoBytes: 4 << 20, MaxPhotos: 6,
		WindowSeconds: 3600, MaxPerWindow: 5, DefaultBranchID: "branch-1",
	}
}

func (f *fakeStore) fail(method string) error { return f.failOn[method] }

// - 0129 money paths -

func (f *fakeStore) OutcomePath(_ context.Context, outcome string) (MoneyPathConfig, error) {
	if err := f.fail("OutcomePath"); err != nil {
		return MoneyPathConfig{}, err
	}
	c, ok := f.paths[outcome]
	if !ok {
		return MoneyPathConfig{}, unprocessable("warranty: no active money path is configured for outcome %s", outcome)
	}
	return c, nil
}

func (f *fakeStore) InsertSettlementPosting(_ context.Context, p SettlementPosting) (string, error) {
	if err := f.fail("InsertSettlementPosting"); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := p.ClaimID + "|" + p.MoneyPath
	if prev, ok := f.postings[key]; ok {
		p.ID = prev.ID // upsert: same row
	} else {
		f.seq++
		p.ID = "post-" + itoa64(int64(f.seq))
	}
	p.PostedAt = time.Now().UTC().Format(time.RFC3339)
	f.postings[key] = p
	return p.ID, nil
}

func (f *fakeStore) PostingsForClaim(_ context.Context, claimID string) ([]SettlementPosting, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []SettlementPosting{}
	for _, p := range f.postings {
		if p.ClaimID == claimID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MoneyPath < out[j].MoneyPath })
	return out, nil
}

// posting is a test helper: the single posting recorded for a claim (fails the caller's expectation loudly by
// returning ok=false when there is none).
func (f *fakeStore) posting(claimID string) (SettlementPosting, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.postings {
		if p.ClaimID == claimID {
			return p, true
		}
	}
	return SettlementPosting{}, false
}

// - 0130 recovery -

// recoveryRow computes ONE claim's balance exactly the way the 0130 view does: accrued from the accruing
// posting, received/promised from the chase log, outstanding as the difference.
func (f *fakeStore) recoveryRow(claimID string) (RecoveryRow, bool) {
	var accrual SettlementPosting
	found := false
	for _, p := range f.postings {
		if p.ClaimID == claimID && p.AccruesRecovery {
			accrual, found = p, true
		}
	}
	if !found {
		return RecoveryRow{}, false
	}
	c := f.claims[claimID]
	row := RecoveryRow{
		ClaimID: claimID, ClaimNo: c.ClaimNo, BranchID: c.BranchID, VendorID: c.VendorID,
		VendorName: c.VendorName, AccruedCents: accrual.AmountCents, AccruedAt: accrual.PostedAt,
	}
	for _, a := range f.recovery[claimID] {
		switch a.Action {
		case ActionReceived:
			row.ReceivedCents += a.AmountCents
		case ActionPromised:
			row.PromisedCents += a.AmountCents
		}
		row.ActionCount++
		if a.OccurredAt > row.LastActionAt {
			row.LastActionAt = a.OccurredAt
		}
	}
	row.OutstandingCents = row.AccruedCents - row.ReceivedCents
	return row, true
}

func (f *fakeStore) RecoveryRows(_ context.Context, branchID, vendorID string, openOnly bool) ([]RecoveryRow, error) {
	if err := f.fail("RecoveryRows"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := map[string]bool{}
	out := []RecoveryRow{}
	for _, p := range f.postings {
		if !p.AccruesRecovery || seen[p.ClaimID] {
			continue
		}
		seen[p.ClaimID] = true
		row, ok := f.recoveryRow(p.ClaimID)
		if !ok {
			continue
		}
		if branchID != "" && row.BranchID != branchID {
			continue
		}
		if vendorID != "" && row.VendorID != vendorID {
			continue
		}
		if openOnly && row.OutstandingCents <= 0 {
			continue
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ClaimNo < out[j].ClaimNo })
	return out, nil
}

func (f *fakeStore) RecoveryBalanceForClaim(_ context.Context, claimID string) (RecoveryRow, error) {
	if err := f.fail("RecoveryBalanceForClaim"); err != nil {
		return RecoveryRow{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.recoveryRow(claimID)
	if !ok {
		return RecoveryRow{}, nil
	}
	return row, nil
}

func (f *fakeStore) InsertRecoveryAction(_ context.Context, a RecoveryAction) (string, error) {
	if err := f.fail("InsertRecoveryAction"); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	a.ID = "rec-" + itoa64(int64(f.seq))
	f.recovery[a.ClaimID] = append(f.recovery[a.ClaimID], a)
	return a.ID, nil
}

func (f *fakeStore) RecoveryActionsForClaim(_ context.Context, claimID string) ([]RecoveryAction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]RecoveryAction{}, f.recovery[claimID]...)
	sort.Slice(out, func(i, j int) bool { return out[i].OccurredAt < out[j].OccurredAt })
	return out, nil
}

// - 0131 intake -

func (f *fakeStore) IntakeConfig(_ context.Context) (IntakeConfig, error) {
	if err := f.fail("IntakeConfig"); err != nil {
		return IntakeConfig{}, err
	}
	return f.intakeCfg, nil
}

func (f *fakeStore) IntakeRateLimitHit(_ context.Context, bucketKey string, windowStart time.Time) (int, error) {
	if err := f.fail("IntakeRateLimitHit"); err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := bucketKey + "|" + windowStart.UTC().Format(time.RFC3339)
	f.rateHits[key]++
	return f.rateHits[key], nil
}

func (f *fakeStore) InsertPublicClaim(_ context.Context, in PublicIntakeInput, claimNo, publicRef, branchID string, photos []IntakePhoto, openedAt time.Time) (Claim, error) {
	if err := f.fail("InsertPublicClaim"); err != nil {
		return Claim{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	id := "claim-web-" + itoa64(int64(f.seq))
	if claimNo == "" {
		claimNo = "WTY-" + itoa64(int64(f.seq))
	}
	c := Claim{
		ID: id, ClaimNo: claimNo, BranchID: branchID, Phase: PhaseIntake, Source: SourceWeb,
		Description: in.Description, JobAddress: in.JobAddress, OpenedAt: openedAt.Format(time.RFC3339),
	}
	f.claims[id] = c
	for _, l := range in.Lines {
		f.lines[id] = append(f.lines[id], ClaimLine{ClaimID: id, ProductText: l.ProductText, Qty: l.Qty, Notes: l.Notes})
	}
	for i, p := range photos {
		p.ID = "photo-" + itoa64(int64(i+1))
		p.ClaimID = id
		f.photos[id] = append(f.photos[id], p)
	}
	f.events = append(f.events, ClaimEvent{ClaimID: id, EventType: "CREATED", ToPhase: PhaseIntake,
		Note: "Claim submitted through the public warranty form.", OccurredAt: openedAt.Format(time.RFC3339)})
	// publicRef is not surfaced on the Claim DTO; the tests read it off the result.
	_ = publicRef
	return c, nil
}

func (f *fakeStore) IntakePhotosForClaim(_ context.Context, claimID string) ([]IntakePhoto, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]IntakePhoto{}, f.photos[claimID]...), nil
}

func (f *fakeStore) IntakePhoto(_ context.Context, claimID, photoID string) (IntakePhoto, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.photos[claimID] {
		if p.ID == photoID {
			return p, nil
		}
	}
	return IntakePhoto{}, notFound("warranty: photo not found")
}

// - 0132 SLA -

func (f *fakeStore) SLATargets(_ context.Context) ([]SLATarget, error) {
	if err := f.fail("SLATargets"); err != nil {
		return nil, err
	}
	return append([]SLATarget{}, f.slaTargets...), nil
}

// OpenClaimPhaseClocks derives phase entry from the event spine exactly as the SQL does: the latest
// PHASE_CHANGE into the current phase, falling back to opened_at.
func (f *fakeStore) OpenClaimPhaseClocks(_ context.Context, branchID string) ([]PhaseClock, error) {
	if err := f.fail("OpenClaimPhaseClocks"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []PhaseClock{}
	for _, c := range f.claims {
		if IsTerminal(c.Phase) {
			continue
		}
		if branchID != "" && c.BranchID != branchID {
			continue
		}
		since := c.OpenedAt
		for _, e := range f.events {
			if e.ClaimID == c.ID && e.EventType == "PHASE_CHANGE" && e.ToPhase == c.Phase && e.OccurredAt > since {
				since = e.OccurredAt
			}
		}
		t, err := parseFlexTime(since)
		if err != nil {
			t = time.Time{}
		}
		out = append(out, PhaseClock{
			ClaimID: c.ID, ClaimNo: c.ClaimNo, BranchID: c.BranchID, VendorID: c.VendorID,
			VendorName: c.VendorName, OwnerID: c.OwnerID, Phase: c.Phase, PhaseSince: t,
			ExpectedCents: c.ExpectedCents,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ClaimNo < out[j].ClaimNo })
	return out, nil
}

func (f *fakeStore) UpsertSLAAlert(_ context.Context, a SLAAlert) (string, error) {
	if err := f.fail("UpsertSLAAlert"); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := a.ClaimID + "|" + a.Phase
	if prev, ok := f.slaAlerts[key]; ok {
		a.ID = prev.ID
		a.AcknowledgedAt = prev.AcknowledgedAt // an update never clears an acknowledgement
		a.AcknowledgedBy = prev.AcknowledgedBy
	} else {
		f.seq++
		a.ID = "alert-" + itoa64(int64(f.seq))
	}
	if a.NotifyStatus == "" {
		a.NotifyStatus = NotifySuppressed
	}
	f.slaAlerts[key] = a
	return a.ID, nil
}

func (f *fakeStore) SLAAlerts(_ context.Context, branchID string, includeAcknowledged bool) ([]SLAAlert, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []SLAAlert{}
	for _, a := range f.slaAlerts {
		if branchID != "" && a.BranchID != branchID {
			continue
		}
		if !includeAcknowledged && a.AcknowledgedAt != "" {
			continue
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OverdueHours > out[j].OverdueHours })
	return out, nil
}

func (f *fakeStore) AcknowledgeSLAAlert(_ context.Context, alertID, userID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, a := range f.slaAlerts {
		if a.ID == alertID {
			if a.AcknowledgedAt == "" {
				a.AcknowledgedAt = time.Now().UTC().Format(time.RFC3339)
				a.AcknowledgedBy = userID
				f.slaAlerts[k] = a
			}
			return nil
		}
	}
	return notFound("warranty: alert not found")
}

func (f *fakeStore) MarkSLANotified(_ context.Context, alertID, status, ref, errText string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, a := range f.slaAlerts {
		if a.ID == alertID {
			a.NotifyStatus, a.NotifyRef, a.NotifyError = status, ref, errText
			f.slaAlerts[k] = a
			return nil
		}
	}
	return nil
}

// alert is a test helper: the alert recorded for a (claim, phase).
func (f *fakeStore) alert(claimID, phase string) (SLAAlert, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.slaAlerts[claimID+"|"+phase]
	return a, ok
}

// - test doubles for the three W5-B seams -

// legs renders a journal's account/amount pairs as a stable string for assertions.
func legs(j GLJournal) string {
	var b strings.Builder
	for i, l := range j.Lines {
		if i > 0 {
			b.WriteString(" ")
		}
		if l.DebitCents > 0 {
			b.WriteString("DR" + l.AccountCode + ":" + itoa64(l.DebitCents))
		} else {
			b.WriteString("CR" + l.AccountCode + ":" + itoa64(l.CreditCents))
		}
	}
	return b.String()
}

// fakeCredit is the BILLING seam double.
type fakeCredit struct {
	reqs []CustomerCreditRequest
	res  CustomerCreditResult
	err  error
}

func (c *fakeCredit) IssueCustomerCredit(_ context.Context, in CustomerCreditRequest) (CustomerCreditResult, error) {
	c.reqs = append(c.reqs, in)
	if c.err != nil {
		return CustomerCreditResult{}, c.err
	}
	return c.res, nil
}

// fakeInvCost is the INVENTORY seam double.
type fakeInvCost struct {
	reqs []ReplacementCostRequest
	res  ReplacementCostResult
	err  error
}

func (m *fakeInvCost) MoveReplacementCost(_ context.Context, in ReplacementCostRequest) (ReplacementCostResult, error) {
	m.reqs = append(m.reqs, in)
	if m.err != nil {
		return ReplacementCostResult{}, m.err
	}
	return m.res, nil
}

// fakeNotifier is the SLA notification seam double (flag-ON, for the wired-path test).
type fakeNotifier struct {
	events []SLABreachEvent
	err    error
}

func (n *fakeNotifier) NotifySLABreach(_ context.Context, in SLABreachEvent) error {
	n.events = append(n.events, in)
	return n.err
}
