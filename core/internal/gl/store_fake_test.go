package gl

// store_fake_test.go, the DB-free fake glStore + reader fakes the unit tests inject (mirrors the internal/
// till fakeStore pattern). It captures inserted journal drafts so a test can assert the built legs, models
// idempotency by key, and lets a test seed accounts / journals / closed periods.

import (
	"context"
	"fmt"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5"
)

func defaultRules() map[string]map[string]string {
	return map[string]map[string]string{
		"invoice.finalized":      {"ar": "1100", "revenue": "4000", "tax": "2200", "holdback": "1150", "customer_deposit": "2300"},
		"payment.received":       {"cash": "1020", "ar": "1100", "card_clearing": "1010"},
		"tax.relief.assessed":    {"hst_full": "2200", "fn_relief": "1165"},
		"payment.bounced":        {"ar": "1100", "bank": "1000", "undeposited": "1020", "card_clearing": "1010", "fee_income": "4200", "hst": "2200"},
		"payment.reversed":       {"ar": "1100", "refund_payable": "2310"},
		"payment.reapplied":      {"ar": "1100", "refund_payable": "2310"},
		"payment.paid_out":       {"refund_payable": "2310", "drawer": "1020", "bank": "1000", "card_clearing": "1010", "write_off": "6350"},
		"till.session.completed": {"over_short": "6350", "undeposited": "1020"},
		"order.fulfilled":        {"cogs": "5000", "inventory": "1200"},
		"deposit.released":       {"customer_deposit": "2300", "revenue": "4000"},
		"order.returned":         {"inventory": "1200", "cogs": "5000"},
		"pos.return.committed":   {"revenue": "4000", "hst": "2200", "undeposited": "1020", "card_clearing": "1010", "ar": "1100", "fee_revenue": "4210"},
		// Item 10.3: goods returned damaged are written off to inventory adjustments (Q4).
		"pos.return.written_off": {"expense": "5200", "cogs": "5000"},
	}
}

type fakeStore struct {
	policy        Policy
	rules         map[string]map[string]string
	activeRules   []RuleRef
	accounts      map[string]Account
	draftRefs     map[string]int
	journals      map[string]*Journal
	recurring     *RecurringTemplate // the template GetRecurring answers; nil = not found
	byKey         map[string]string
	inserted      []journalDraft
	closedPeriods map[string]bool
	asOfSeen      time.Time         // AccountBalanceAsOf records the instant the GL arm of the tie-out cut at
	asOfNetDebit  int64             // AccountBalanceAsOf return (the inception-to-date GL side of the tie-out)
	tbRows        []TrialBalanceRow // TrialBalance / TrialBalanceAsOf return (the ERP side of the BisTrack static tie-out)
	asOfRows      []TrialBalanceRow // TrialBalanceAsOf override when the test must differ from period-movement tbRows
	plBranchRows  []PLBranchRow
	headOffice    string
	seq           int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		policy:        Policy{JEApprovalThresholdCents: 500000, FiscalYearStartMonth: 1, CostBasisStrategy: "WEIGHTED_AVG"},
		rules:         defaultRules(),
		accounts:      map[string]Account{},
		draftRefs:     map[string]int{},
		journals:      map[string]*Journal{},
		byKey:         map[string]string{},
		closedPeriods: map[string]bool{},
		headOffice:    "head-office",
	}
}

func periodKey(t time.Time) string { return fmt.Sprintf("%d-%02d", t.Year(), int(t.Month())) }

func (f *fakeStore) Policy(_ context.Context, _ string) (Policy, error) { return f.policy, nil }

func (f *fakeStore) PostingAccounts(_ context.Context, eventType, _ string) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range f.rules[eventType] {
		out[k] = v
	}
	return out, nil
}

// PostingAccountsTx has no transaction to ride in a DB-free fake; it answers as PostingAccounts does.
func (f *fakeStore) PostingAccountsTx(ctx context.Context, _ pgx.Tx, eventType, branchID string) (map[string]string, error) {
	return f.PostingAccounts(ctx, eventType, branchID)
}

func (f *fakeStore) ListActiveRules(_ context.Context) ([]RuleRef, error) { return f.activeRules, nil }

func (f *fakeStore) ListAccounts(_ context.Context) ([]Account, error) {
	var out []Account
	for _, a := range f.accounts {
		out = append(out, a)
	}
	return out, nil
}

func (f *fakeStore) AccountByCode(_ context.Context, code string) (Account, bool, error) {
	a, ok := f.accounts[code]
	return a, ok, nil
}

func (f *fakeStore) InsertAccount(_ context.Context, in CreateAccountInput) (Account, error) {
	if _, ok := f.accounts[in.Code]; ok {
		return Account{}, clientErr(409, "gl: account code %q already exists", in.Code)
	}
	f.seq++
	a := Account{ID: fmt.Sprintf("acct-%d", f.seq), Code: in.Code, Name: in.Name, AccountType: in.AccountType, NormalBalance: in.NormalBalance, ParentID: in.ParentID, IsActive: true}
	f.accounts[in.Code] = a
	return a, nil
}

func (f *fakeStore) DraftLineCountForAccountCode(_ context.Context, code string) (int, error) {
	return f.draftRefs[code], nil
}

func (f *fakeStore) SetAccountActive(_ context.Context, code string, active bool) (Account, error) {
	a, ok := f.accounts[code]
	if !ok {
		return Account{}, errNotFound
	}
	a.IsActive = active
	f.accounts[code] = a
	return a, nil
}

// InsertJournalTx has no transaction to ride in a DB-free fake; it posts as InsertJournal does.
func (f *fakeStore) InsertJournalTx(ctx context.Context, _ pgx.Tx, d journalDraft) (string, bool, error) {
	return f.InsertJournal(ctx, d)
}

func (f *fakeStore) InsertJournal(_ context.Context, d journalDraft) (string, bool, error) {
	if d.IdempotencyKey != "" {
		if id, ok := f.byKey[d.IdempotencyKey]; ok {
			return id, false, nil
		}
	}
	if f.closedPeriods[periodKey(d.OccurredAt)] {
		return "", false, errClosedPeriod
	}
	f.seq++
	id := fmt.Sprintf("jrn-%d", f.seq)
	j := &Journal{
		ID:                id,
		FiscalYear:        d.OccurredAt.Year(),
		EntryNo:           f.seq,
		Source:            d.Source,
		SourceRefID:       d.SourceRefID,
		Memo:              d.Memo,
		Status:            d.Status,
		BranchID:          d.BranchID,
		ApprovedBy:        d.ApprovedBy,
		ReversesJournalID: d.ReversesJournalID,
		IdempotencyKey:    d.IdempotencyKey,
		CreatedBy:         d.CreatedBy,
		CreatedAt:         d.OccurredAt,
	}
	if d.Status == StatusPosted {
		t := d.OccurredAt
		j.PostedAt = &t
	}
	for i, ln := range d.Lines {
		name := ""
		if a, ok := f.accounts[ln.AccountCode]; ok {
			name = a.Name
		}
		j.Lines = append(j.Lines, JournalLine{LineNo: i + 1, AccountCode: ln.AccountCode, AccountName: name, DebitCents: ln.DebitCents, CreditCents: ln.CreditCents, Memo: ln.Memo})
	}
	f.journals[id] = j
	if d.IdempotencyKey != "" {
		f.byKey[d.IdempotencyKey] = id
	}
	f.inserted = append(f.inserted, d)
	return id, true, nil
}

func (f *fakeStore) InsertParkedJournal(ctx context.Context, d journalDraft, file func(ctx context.Context, tx pgx.Tx, journalID string) error) (string, error) {
	id, _, err := f.InsertJournal(ctx, d)
	if err != nil {
		return "", err
	}
	if err := file(ctx, nil, id); err != nil {
		delete(f.journals, id)
		return "", err
	}
	return id, nil
}

func (f *fakeStore) GetJournal(_ context.Context, id string) (Journal, error) {
	j, ok := f.journals[id]
	if !ok {
		return Journal{}, errNotFound
	}
	return *j, nil
}

func (f *fakeStore) JournalIsReversed(_ context.Context, id string) (bool, error) {
	for _, j := range f.journals {
		if j.ReversesJournalID == id {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) ListJournals(_ context.Context, _ JournalFilter) ([]Journal, error) {
	var out []Journal
	for _, j := range f.journals {
		out = append(out, *j)
	}
	return out, nil
}

func (f *fakeStore) ApproveJournal(_ context.Context, id, approverID string) (Journal, error) {
	j, ok := f.journals[id]
	if !ok {
		return Journal{}, errNotFound
	}
	if j.Status != StatusDraft {
		return Journal{}, clientErr(409, "gl: not DRAFT")
	}
	j.Status = StatusPosted
	j.ApprovedBy = approverID
	t := time.Now()
	j.PostedAt = &t
	return *j, nil
}

// ApproveJournalTx mirrors ApproveJournal for the atomic approvals-rail path (0075); the fake ignores the
// tx and enforces the same DRAFT-state + four-eyes guards.
// VoidJournalTx mirrors VoidJournal for the rail's reject arm; the fake flips a DRAFT to VOID and
// leaves anything else standing, as the repository does.
func (f *fakeStore) VoidJournalTx(_ context.Context, _ pgx.Tx, id string) error {
	if j, ok := f.journals[id]; ok && j.Status == StatusDraft {
		j.Status = StatusVoid
	}
	return nil
}

func (f *fakeStore) ApproveJournalTx(_ context.Context, _ pgx.Tx, id, approverID string) error {
	j, ok := f.journals[id]
	if !ok {
		return errNotFound
	}
	if j.Status != StatusDraft {
		return clientErr(409, "gl: not DRAFT")
	}
	if j.CreatedBy != "" && j.CreatedBy == approverID {
		return errFourEyes
	}
	j.Status = StatusPosted
	j.ApprovedBy = approverID
	t := time.Now()
	j.PostedAt = &t
	return nil
}

func (f *fakeStore) VoidJournal(_ context.Context, id string) (Journal, error) {
	j, ok := f.journals[id]
	if !ok {
		return Journal{}, errNotFound
	}
	if j.Status != StatusDraft {
		return Journal{}, clientErr(409, "gl: only DRAFT voidable")
	}
	j.Status = StatusVoid
	return *j, nil
}

func (f *fakeStore) EnsureNextFiscalYear(_ context.Context, _ time.Time) (int, error) {
	return 2027, nil
}

func (f *fakeStore) OpenNextFiscalYear(_ context.Context, _ time.Time, _ func(ctx context.Context, tx pgx.Tx, year int) error) (int, error) {
	return 2027, nil
}

func (f *fakeStore) ListPeriods(_ context.Context) ([]Period, error) { return nil, nil }
func (f *fakeStore) GetPeriod(_ context.Context, _ string) (Period, error) {
	return Period{}, errNotFound
}
func (f *fakeStore) ClosePeriod(_ context.Context, id, _, _ string) (Period, error) {
	return Period{ID: id, Status: "CLOSED"}, nil
}
func (f *fakeStore) ReopenPeriod(_ context.Context, id, _, _ string) (Period, error) {
	return Period{ID: id, Status: "OPEN"}, nil
}
func (f *fakeStore) ClosePeriodsForYear(_ context.Context, _ int, _, _ string) error { return nil }

func (f *fakeStore) AccountBalance(_ context.Context, _ string, _, _ int, _ string) (int64, string, error) {
	return 0, NormalDebit, nil
}
func (f *fakeStore) AccountBalanceAsOf(_ context.Context, _ string, at time.Time, _ string) (int64, string, error) {
	f.asOfSeen = at
	return f.asOfNetDebit, NormalDebit, nil
}
func (f *fakeStore) TrialBalance(_ context.Context, _, _ int, _ string) ([]TrialBalanceRow, error) {
	return f.tbRows, nil
}

// TrialBalanceAsOf returns the fake's closing balances (tbRows model the ERP inception-to-date closing
// net-debit balances the BisTrack tie-out reconciles against). asOfRows, when set, overrides tbRows so a test
// can distinguish period-movement from inception-to-date (the opening-balance regression case).
func (f *fakeStore) TrialBalanceAsOf(_ context.Context, _ time.Time, _ string) ([]TrialBalanceRow, error) {
	if f.asOfRows != nil {
		return f.asOfRows, nil
	}
	return f.tbRows, nil
}
func (f *fakeStore) PLBalances(_ context.Context, _ int) ([]TrialBalanceRow, error) { return nil, nil }

func (f *fakeStore) HeadOfficeBranchID(_ context.Context) (string, error) {
	if f.headOffice != "" {
		return f.headOffice, nil
	}
	return "head-office", nil
}

func (f *fakeStore) SetHeadOfficeBranch(ctx context.Context, branchID string, record func(ctx context.Context, tx pgx.Tx) error) error {
	if record != nil {
		if err := record(ctx, nil); err != nil {
			return err
		}
	}
	f.headOffice = branchID
	return nil
}

func (f *fakeStore) PLBalancesByBranch(_ context.Context, _ int) ([]PLBranchRow, error) {
	return f.plBranchRows, nil
}

func (f *fakeStore) UpsertTieout(_ context.Context, sub, code, branch string, subC, glC int64, at time.Time) (TieoutState, error) {
	return TieoutState{Subledger: sub, AccountCode: code, BranchID: branch, SubledgerCents: subC, GLCents: glC, DeltaCents: subC - glC, InTie: subC == glC, LastCheckedAt: at}, nil
}

func (f *fakeStore) ListRecurring(_ context.Context) ([]RecurringTemplate, error) { return nil, nil }
func (f *fakeStore) GetRecurring(_ context.Context, _ string) (RecurringTemplate, error) {
	if f.recurring != nil {
		return *f.recurring, nil
	}
	return RecurringTemplate{}, errNotFound
}

func (f *fakeStore) InsertExportBatch(_ context.Context, _, _ int, _, _ string, _ int) (string, error) {
	f.seq++
	return fmt.Sprintf("batch-%d", f.seq), nil
}
func (f *fakeStore) ExportRows(_ context.Context, _, _ int, _ string) ([]ExportRow, error) {
	return nil, nil
}

// lastInserted returns the most recently inserted draft (nil if none).
func (f *fakeStore) lastInserted() *journalDraft {
	if len(f.inserted) == 0 {
		return nil
	}
	return &f.inserted[len(f.inserted)-1]
}

// - reader fakes -

type fakeInvoiceReader struct{ amt InvoiceAmounts }

func (f fakeInvoiceReader) InvoiceForGL(_ context.Context, _ string) (InvoiceAmounts, error) {
	return f.amt, nil
}

type fakePaymentReader struct{ facts PaymentFacts }

func (f fakePaymentReader) PaymentForGL(_ context.Context, _ string) (PaymentFacts, error) {
	return f.facts, nil
}

type fakeTaxSplit struct {
	full, onCredit, net int64
	found               bool
}

func (f fakeTaxSplit) TaxSplit(_ context.Context, _ string) (int64, int64, int64, bool, error) {
	return f.full, f.onCredit, f.net, f.found, nil
}

// - test service wiring -

var testCaller = identity.StaffContext{UserID: "u-maker", RoleCode: "SALES_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeBranch}

// fakeHold is the approvals queue seen from the approve route: state is the answer (the zero value means an
// approved request for the journal's amount), err makes the read fail.
type fakeHold struct {
	state ApprovalState
	err   error
}

func (h fakeHold) JournalApprovalState(context.Context, string, int64) (ApprovalState, error) {
	if h.state == "" {
		return ApprovalApproved, h.err
	}
	return h.state, h.err
}

func newTestService(f *fakeStore) *Service {
	holder := testCaller
	return NewService(nil).
		WithApprovalStateChecker(fakeHold{}).
		withStore(f).
		withClock(func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }).
		withCaller(func(_ context.Context) (identity.StaffContext, bool) { return holder, holder.UserID != "" })
}

// newTestServiceAs wires a service whose caller is the given StaffContext (four-eyes / controller tests).
func newTestServiceAs(f *fakeStore, caller identity.StaffContext) *Service {
	return NewService(nil).
		WithApprovalStateChecker(fakeHold{}).
		withStore(f).
		withClock(func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }).
		withCaller(func(_ context.Context) (identity.StaffContext, bool) { return caller, caller.UserID != "" })
}
