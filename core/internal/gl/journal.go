package gl

// journal.go, the chart-of-accounts CRUD + the journal lifecycle (maker-checker create/approve, void, the
// net-zero reversing JE, and recurring /run). The DB status is DRAFT|POSTED|VOID (0007); the maker-checker
// "awaiting approval" state is DERIVED (a DRAFT above the gl_policy threshold) and surfaced as
// StatusPendingApproval, there is no PENDING_APPROVAL column (see deviations).

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

var validAccountTypes = map[string]bool{"ASSET": true, "LIABILITY": true, "EQUITY": true, "REVENUE": true, "EXPENSE": true}

// - chart of accounts -

func (s *Service) ListAccounts(ctx context.Context) ([]Account, error) {
	return s.store.ListAccounts(ctx)
}

func (s *Service) CreateAccount(ctx context.Context, in CreateAccountInput) (Account, error) {
	in.Code = strings.TrimSpace(in.Code)
	in.Name = strings.TrimSpace(in.Name)
	in.AccountType = strings.ToUpper(strings.TrimSpace(in.AccountType))
	in.NormalBalance = strings.ToUpper(strings.TrimSpace(in.NormalBalance))
	if in.Code == "" || in.Name == "" {
		return Account{}, badReq("gl: account code and name are required")
	}
	if !validAccountTypes[in.AccountType] {
		return Account{}, badReq("gl: account_type must be one of ASSET/LIABILITY/EQUITY/REVENUE/EXPENSE")
	}
	if in.NormalBalance != NormalDebit && in.NormalBalance != NormalCredit {
		return Account{}, badReq("gl: normal_balance must be DEBIT or CREDIT")
	}
	return s.store.InsertAccount(ctx, in)
}

// DeactivateAccount flips an account inactive, but refuses (409) when a DRAFT journal still references it -
// deactivating out from under an unposted entry would strand it. POSTED history is unaffected (the account
// keeps its ledger rows; it just cannot be used on new entries).
func (s *Service) DeactivateAccount(ctx context.Context, code string) (Account, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return Account{}, badReq("gl: account code is required")
	}
	if _, found, err := s.store.AccountByCode(ctx, code); err != nil {
		return Account{}, err
	} else if !found {
		return Account{}, errNotFound
	}
	n, err := s.store.DraftLineCountForAccountCode(ctx, code)
	if err != nil {
		return Account{}, err
	}
	if n > 0 {
		return Account{}, errAccountInUse
	}
	return s.store.SetAccountActive(ctx, code, false)
}

// - journal reads -

// ListJournals returns a page of journals. DIB-392: the branch filter is FENCED with the same rule the
// landed-cost list uses (landedcost/service.go:97-103), a GLOBAL caller lists any or all branches; a
// branch-scoped manager must name a branch they can reach, because an unfiltered list is the whole
// company's ledger and a named foreign branch is a horizontal read. Only the read is fenced; the write
// and maker-checker paths are untouched.
func (s *Service) ListJournals(ctx context.Context, f JournalFilter) ([]Journal, error) {
	if err := s.authorizeBranchRead(ctx, f.BranchID); err != nil {
		return nil, err
	}
	js, err := s.store.ListJournals(ctx, f)
	if err != nil {
		return nil, err
	}
	threshold := s.orgThreshold(ctx)
	for i := range js {
		js[i].DerivedStatus = derivedStatus(js[i].Status, js[i].TotalCents(), threshold)
	}
	return js, nil
}

func (s *Service) GetJournal(ctx context.Context, id string) (Journal, error) {
	j, err := s.store.GetJournal(ctx, id)
	if err != nil {
		return Journal{}, err
	}
	j.DerivedStatus = derivedStatus(j.Status, j.TotalCents(), s.thresholdFor(ctx, j.BranchID))
	return j, nil
}

// - journal writes (maker-checker) -

// CreateJournal validates a balanced manual entry and either posts it directly (below the gl_policy approval
// threshold) or parks it as a DRAFT awaiting a second-person approval (at/above the threshold, maker-checker).
func (s *Service) CreateJournal(ctx context.Context, in CreateJournalInput) (Journal, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Journal{}, err
	}
	if len(in.Lines) < 2 {
		return Journal{}, badReq("gl: a journal needs at least two lines")
	}
	lines := make([]draftLine, 0, len(in.Lines))
	for _, l := range in.Lines {
		if (l.DebitCents > 0) == (l.CreditCents > 0) {
			return Journal{}, badReq("gl: each line must have exactly one of debit_cents / credit_cents > 0")
		}
		if l.AccountCode == "" {
			return Journal{}, badReq("gl: each line needs an account_code")
		}
		lines = append(lines, draftLine{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo})
	}
	source := in.Source
	if source == "" {
		source = SourceManual
	}
	draft := journalDraft{
		Source:     source,
		Memo:       in.Memo,
		BranchID:   in.BranchID,
		CreatedBy:  caller.UserID,
		OccurredAt: s.clock(),
		Lines:      lines,
	}
	if !draft.balanced() {
		return Journal{}, errUnbalanced
	}
	threshold := s.thresholdFor(ctx, in.BranchID)
	amount := draft.debitTotal()
	// Below threshold: post immediately (no second signer required). At/above: park as DRAFT (PENDING_APPROVAL).
	if amount < threshold {
		draft.Status = StatusPosted
	} else {
		draft.Status = StatusDraft
	}
	id, _, err := s.store.InsertJournal(ctx, draft)
	if err != nil {
		return Journal{}, err
	}
	// Approvals rail (0075): a MANUAL journal that PARKED as DRAFT (>= threshold) files a JE_OVER_THRESHOLD
	// approval request so the rail fans out cards to eligible approvers. Best-effort + log-and-continue -
	// the park NEVER depends on it (an identity/messaging blip must not fail a journal write). This is the
	// ONLY GL call site that files: the automated posting paths (posting.go / close.go / PostExternal) do
	// not go through CreateJournal, so they never file a request regardless of amount. Nil seam = shipped
	// behavior. NOTE (commander ruling, DIB-216 #2): `threshold` is the seeded $5,000 gl_policy PLACEHOLDER
	// (0032), BLESSED AS-IS and left a go-live-configurable POLICY ROW, not changed here.
	if s.approvalFiler != nil && draft.Status == StatusDraft && source == SourceManual {
		if err := s.approvalFiler.FileJEApproval(ctx, id, in.BranchID, amount, caller.UserID); err != nil {
			s.log.Warn("gl: approvals-rail JE filing failed (journal still parked as DRAFT)", "journal_id", id, "err", err)
		}
	}
	return s.GetJournal(ctx, id)
}

// ApproveJournal is the checker half of maker-checker: a manager other than the creator (four-eyes) approves a
// parked DRAFT, which posts it (approved_by stamped). The balance invariant is asserted at post time.
func (s *Service) ApproveJournal(ctx context.Context, id string) (Journal, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Journal{}, err
	}
	j, err := s.store.GetJournal(ctx, id)
	if err != nil {
		return Journal{}, err
	}
	if j.Status != StatusDraft {
		return Journal{}, clientErr(409, "gl: journal is %s, not DRAFT, cannot approve", j.Status)
	}
	if j.CreatedBy != "" && j.CreatedBy == caller.UserID {
		return Journal{}, errFourEyes
	}
	// One way to post a parked journal that has a request: an approved request, through the queue. Any other
	// state of that request (pending, rejected, tombstoned) holds the journal, and what happens to it is
	// decided there. The four eyes check above stays first, so a maker is still refused as the maker whichever
	// route they use. Unwired, the guard cannot ask and the route refuses (fail closed).
	if s.pendingChecker == nil {
		return Journal{}, clientErr(503, "gl: the approvals queue cannot be asked; this journal cannot be approved here")
	}
	held, herr := s.pendingChecker.HasBlockingJEApproval(ctx, id)
	if herr != nil {
		return Journal{}, herr
	}
	if held {
		return Journal{}, clientErr(409, "gl: this journal has an approval request; it is decided from the approvals queue, which posts it")
	}
	approved, err := s.store.ApproveJournal(ctx, id, caller.UserID)
	if err != nil {
		return Journal{}, err
	}
	approved.DerivedStatus = derivedStatus(approved.Status, approved.TotalCents(), s.thresholdFor(ctx, approved.BranchID))
	return approved, nil
}

// ApproveJournalTx is the transaction-scoped checker half used by the approvals rail (0075): it posts the
// parked DRAFT within the caller's transaction so the ledger POST and the approval-request stamp commit
// atomically. deciderID is the rail approver (already resolved + authorized by the identity module); the
// four-eyes (approver ≠ maker) and DRAFT-state guards are enforced in the store's single guarded UPDATE.
func (s *Service) ApproveJournalTx(ctx context.Context, tx pgx.Tx, id, deciderID string) error {
	return s.store.ApproveJournalTx(ctx, tx, id, deciderID)
}

// VoidJournal voids a DRAFT (a POSTED journal is corrected with a reversing JE, never voided).
func (s *Service) VoidJournal(ctx context.Context, id string) (Journal, error) {
	if _, err := s.caller(ctx); err != nil {
		return Journal{}, err
	}
	j, err := s.store.VoidJournal(ctx, id)
	if err != nil {
		return Journal{}, err
	}
	j.DerivedStatus = StatusVoid
	return j, nil
}

// ReverseJournal posts a one-click net-zero reversing JE for a POSTED journal: every leg's debit/credit is
// swapped, so the original + reversal sum to zero on every account. The reversal links back via
// reverses_journal_id; the original stays POSTED (audit trail intact).
func (s *Service) ReverseJournal(ctx context.Context, id string) (Journal, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Journal{}, err
	}
	orig, err := s.store.GetJournal(ctx, id)
	if err != nil {
		return Journal{}, err
	}
	if orig.Status != StatusPosted {
		return Journal{}, clientErr(409, "gl: only a POSTED journal can be reversed (status=%s)", orig.Status)
	}
	lines := make([]draftLine, 0, len(orig.Lines))
	for _, l := range orig.Lines {
		lines = append(lines, draftLine{
			AccountCode: l.AccountCode,
			DebitCents:  l.CreditCents, // swap
			CreditCents: l.DebitCents,  // swap
			Memo:        "reversal",
		})
	}
	draft := journalDraft{
		Source:            SourceAdjustment,
		Memo:              fmt.Sprintf("Reversal of entry #%d", orig.EntryNo),
		BranchID:          orig.BranchID,
		Status:            StatusPosted,
		ReversesJournalID: orig.ID,
		CreatedBy:         caller.UserID,
		OccurredAt:        s.clock(),
		Lines:             lines,
	}
	newID, _, err := s.store.InsertJournal(ctx, draft)
	if err != nil {
		return Journal{}, err
	}
	return s.GetJournal(ctx, newID)
}

// - recurring -

func (s *Service) ListRecurring(ctx context.Context) ([]RecurringTemplate, error) {
	return s.store.ListRecurring(ctx)
}

// RunRecurring materializes a DRAFT journal from a template's JSONB lines (for review + approval). It always
// lands as a DRAFT, a machine-generated accrual is reviewed before it posts.
func (s *Service) RunRecurring(ctx context.Context, id string) (Journal, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Journal{}, err
	}
	tpl, err := s.store.GetRecurring(ctx, id)
	if err != nil {
		return Journal{}, err
	}
	if len(tpl.Lines) < 2 {
		return Journal{}, badReq("gl: recurring template has fewer than two lines")
	}
	lines := make([]draftLine, 0, len(tpl.Lines))
	for _, l := range tpl.Lines {
		lines = append(lines, draftLine{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo})
	}
	memo := tpl.Memo
	if memo == "" {
		memo = "Recurring: " + tpl.Name
	}
	draft := journalDraft{
		Source:     SourceManual,
		Memo:       memo,
		Status:     StatusDraft,
		CreatedBy:  caller.UserID,
		OccurredAt: s.clock(),
		Lines:      lines,
	}
	id, _, err = s.store.InsertJournal(ctx, draft)
	if err != nil {
		return Journal{}, err
	}
	return s.GetJournal(ctx, id)
}

// - derived maker-checker status -

func derivedStatus(status string, total, threshold int64) string {
	if status == StatusDraft && total >= threshold && threshold > 0 {
		return StatusPendingApproval
	}
	return status
}

func (s *Service) thresholdFor(ctx context.Context, branchID string) int64 {
	p, err := s.store.Policy(ctx, branchID)
	if err != nil {
		return 0 // conservative: everything routes to approval on a policy read error
	}
	return p.JEApprovalThresholdCents
}

func (s *Service) orgThreshold(ctx context.Context) int64 { return s.thresholdFor(ctx, "") }
