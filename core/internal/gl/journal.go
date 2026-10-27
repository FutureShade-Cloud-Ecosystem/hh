package gl

// journal.go, the chart-of-accounts CRUD + the journal lifecycle (maker-checker create/approve, void, the
// net-zero reversing JE, and recurring /run). The DB status is DRAFT|POSTED|VOID (0007); the maker-checker
// "awaiting approval" state is DERIVED (a DRAFT above the gl_policy threshold) and surfaced as
// StatusPendingApproval, there is no PENDING_APPROVAL column (see deviations).

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
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
// keeps its ledger rows; it just cannot be used on new entries). It also refuses (FW1-09a) when an active
// posting rule points at the account: every automated posting the rule carries would be refused as
// "inactive" and dead letter, so the refusal names the rules and their topics instead.
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
	rules, err := s.store.ListActiveRules(ctx)
	if err != nil {
		return Account{}, err
	}
	var need []string
	for _, rr := range rules {
		if strings.EqualFold(rr.AccountCode, code) {
			need = append(need, fmt.Sprintf("%s (%s)", rr.EventType, rr.LineRole))
		}
	}
	if len(need) > 0 {
		return Account{}, clientErr(409, "gl: account %s cannot be deactivated: the posting rules %s need it; deactivating it would stop those postings", code, strings.Join(need, ", "))
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

// GetJournal is fenced to the caller's branch reach (FW1-10), the same rule ListJournals carries
// (DIB-392): a journal posted at a branch the caller cannot reach answers as MISSING, never 403,
// because an id is an existence oracle and the list already hides these rows. A journal with no
// branch is the consolidated books (until FW1-08 stamps every line, branch money sits in such
// journals), so it reads by id only by a dealer wide caller, the trial balance rule.
// journalAnswered reads a just-written journal back through the STORE, never through the fenced read,
// so an answer never contradicts a committed write (615 round 2, P2-2): the caller just wrote this
// row, and a refused-looking answer for a landed write is the contradiction S2 named. The write
// fences themselves (SECURITY-SOD gap 19) are a named item's, FW1-09b.
func (s *Service) journalAnswered(ctx context.Context, id string) (Journal, error) {
	j, err := s.store.GetJournal(ctx, id)
	if err != nil {
		return Journal{}, err
	}
	j.DerivedStatus = derivedStatus(j.Status, j.TotalCents(), s.thresholdFor(ctx, j.BranchID))
	return j, nil
}

// GetJournal is the fenced READ route. Writers answer through journalAnswered.
func (s *Service) GetJournal(ctx context.Context, id string) (Journal, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Journal{}, err
	}
	j, err := s.store.GetJournal(ctx, id)
	if err != nil {
		return Journal{}, err
	}
	if !caller.CanAccessBranch(j.BranchID) {
		return Journal{}, errNotFound
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
	// Approvals rail (0075, ruling 108): a MANUAL journal that PARKS as DRAFT (>= threshold) files its
	// JE_OVER_THRESHOLD request IN THE SAME TRANSACTION as the park, so the rail can post it. If the request
	// cannot be filed, or the journal has no branch to file it under, the create fails with words and nothing
	// is saved: a parked journal that can never post is never left behind. The card fan-out runs after the
	// commit and stays best effort (a messaging blip never fails the write; the reconcile sweep heals it).
	// This is the ONLY GL call site that files: the automated posting paths (posting.go / close.go /
	// PostExternal) never park. Nil seam = shipped behavior (a bare DRAFT). NOTE (commander ruling, DIB-216
	// #2): `threshold` is the seeded $5,000 gl_policy PLACEHOLDER (0032), BLESSED AS-IS and left a
	// go-live-configurable POLICY ROW, not changed here.
	// The create route is the only caller of CreateJournal, and no non manual source files its own request
	// (the automated posting paths never come through here and never park), so a non manual journal at or
	// over the threshold would park with no request and could never post (ruling 115). Refuse it, nothing
	// saved. Under the threshold any source posts straight through, as before.
	if draft.Status == StatusDraft && source != SourceManual {
		return Journal{}, badReq("gl: only a manual journal can be parked for approval at or over the approval threshold; a %q journal at this amount cannot be created here", source)
	}
	if s.approvalFiler != nil && draft.Status == StatusDraft && source == SourceManual {
		if in.BranchID == "" {
			return Journal{}, badReq("gl: a journal at or over the approval threshold needs a branch so its approval request can be filed; choose a branch")
		}
		var notify func(context.Context)
		var filingErr error
		id, err := s.store.InsertParkedJournal(ctx, draft, func(ctx context.Context, tx pgx.Tx, journalID string) error {
			n, ferr := s.approvalFiler.FileJEApprovalTx(ctx, tx, journalID, in.BranchID, amount, caller.UserID)
			if ferr != nil {
				filingErr = ferr
				return ferr
			}
			notify = n
			return nil
		})
		if err != nil {
			if filingErr != nil {
				s.log.Warn("gl: approval request could not be filed with the park; the journal was not saved", "err", filingErr)
				return Journal{}, clientErr(503, "gl: this journal is over the approval threshold and its approval request could not be filed, so nothing was saved; try again")
			}
			return Journal{}, err
		}
		if notify != nil {
			notify(ctx)
		}
		return s.journalAnswered(ctx, id)
	}
	id, _, err := s.store.InsertJournal(ctx, draft)
	if err != nil {
		return Journal{}, err
	}
	return s.journalAnswered(ctx, id)
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
	// A journal over the threshold is posted by this route only when the rail holds an APPROVED request for
	// its current amount. Every other state refuses in its own words (what becomes of the request is decided
	// in the approvals queue). A journal under the threshold with no request at all needs none. The four eyes
	// check above stays first, so a maker is still refused as the maker whichever route they use. Unwired,
	// the guard cannot ask and the route refuses (fail closed).
	if s.approvalChecker == nil {
		return Journal{}, clientErr(503, "gl: the approvals queue cannot be asked; this journal cannot be approved here")
	}
	amount := j.TotalCents()
	state, serr := s.approvalChecker.JournalApprovalState(ctx, id, amount)
	if serr != nil {
		return Journal{}, serr
	}
	switch state {
	case ApprovalApproved:
	case ApprovalNone:
		if threshold := s.thresholdFor(ctx, j.BranchID); threshold > 0 && amount < threshold {
			break
		}
		if j.Source == SourceManual && j.SourceRefID != "" {
			// A recurring run: it carries no branch, so no request was filed for it. Name the template so
			// nobody hunts for a missing request; recurring runs file one after the template gains a branch.
			name := j.SourceRefID
			if tpl, terr := s.store.GetRecurring(ctx, j.SourceRefID); terr == nil && tpl.Name != "" {
				name = tpl.Name
			}
			return Journal{}, clientErr(409, "gl: this journal came from the recurring template %q and is over the approval threshold; a recurring run cannot yet file an approval request, so it cannot be posted from here", name)
		}
		return Journal{}, clientErr(409, "gl: this journal is over the approval threshold and has no approval request in the approvals queue; it cannot be posted from here")
	case ApprovalPending:
		return Journal{}, clientErr(409, "gl: this journal has a pending approval request; decide it from the approvals queue, which posts it")
	case ApprovalRejected:
		return Journal{}, clientErr(409, "gl: this journal's approval request was rejected in the approvals queue; it cannot be posted from here")
	case ApprovalDeleted:
		return Journal{}, clientErr(409, "gl: this journal's approval request was deleted from the approvals queue; it cannot be posted from here")
	case ApprovalAmountDiffers:
		return Journal{}, clientErr(409, "gl: the approval for this journal in the approvals queue is for a different amount than the journal now carries; it cannot be posted from here")
	default:
		return Journal{}, clientErr(503, "gl: the approvals queue gave no usable answer; this journal cannot be approved here")
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
// reverses_journal_id; the original stays POSTED (audit trail intact). Two guards (FW1-09a): a journal is
// reversed once, and a journal a module posted is not reversed from the ledger (its document's own
// correction is the path, so the subledgers stay tied to the GL).
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
	if orig.Source != SourceManual {
		if orig.ReversesJournalID != "" {
			return Journal{}, clientErr(409, "gl: entry #%d is itself a reversal; a reversal cannot be reversed", orig.EntryNo)
		}
		if owner := systemJournalOwner(orig); owner != "" {
			return Journal{}, clientErr(409, "gl: entry #%d was posted by %s, not by a person in the ledger; it cannot be reversed from here", orig.EntryNo, owner)
		}
	}
	reversed, err := s.store.JournalIsReversed(ctx, id)
	if err != nil {
		return Journal{}, err
	}
	if reversed {
		return Journal{}, alreadyReversedErr(orig.EntryNo)
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
	// InsertJournal locks the original gl_journals row FOR UPDATE inside its transaction before re-checking
	// reverses_journal_id and drawing next_entry_no (PR 621 round 2), and maps a unique violation on 0440's
	// partial index to the same refusal, so two concurrent reversals serialise and only one lands.
	newID, _, err := s.store.InsertJournal(ctx, draft)
	if err != nil {
		if isUniqueViolation(err) {
			return Journal{}, alreadyReversedErr(orig.EntryNo)
		}
		return Journal{}, err
	}
	return s.journalAnswered(ctx, newID)
}

func alreadyReversedErr(entryNo int) error {
	return clientErr(409, "gl: entry #%d was already reversed; a journal can be reversed once", entryNo)
}

// systemJournalOwner names the module behind a journal the ledger did not post, for the reverse route's
// refusal. It is only called for non MANUAL sources. ADJUSTMENT covers several writers, so the
// idempotency key's prefix names the writer when the source alone cannot (bankrec's clearing journals,
// the landed cost paths and the holdback release all file under ADJUSTMENT); a key the map does not
// know still refuses, as "another module". FW1-09a.
func systemJournalOwner(j Journal) string {
	switch j.Source {
	case SourceInvoice:
		return "the invoicing module"
	case SourcePayment:
		return "the payments module"
	case SourceFulfillment:
		return "the fulfilment module"
	case SourcePOReceipt:
		return "the purchasing module"
	case SourceClosing:
		return "the year end close"
	}
	for prefix, owner := range map[string]string{
		"bankrec:":                "the bank reconciliation module",
		"landedcost.":             "the landed cost module",
		"holdback-release:":       "the holdback module",
		"till.session.completed:": "the till module",
	} {
		if strings.HasPrefix(j.IdempotencyKey, prefix) {
			return owner
		}
	}
	return "another module"
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
	// SourceRefID carries the template's id: the only MANUAL journals with a source ref are recurring runs, and
	// the approve route reads it to name the template when it refuses an over-threshold run (ruling 108).
	draft := journalDraft{
		Source:      SourceManual,
		SourceRefID: tpl.ID,
		Memo:        memo,
		Status:      StatusDraft,
		CreatedBy:   caller.UserID,
		OccurredAt:  s.clock(),
		Lines:       lines,
	}
	id, _, err = s.store.InsertJournal(ctx, draft)
	if err != nil {
		return Journal{}, err
	}
	return s.journalAnswered(ctx, id)
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

// BankJournalFacts is what a bank reconciliation needs to know about one journal to let a statement line be explained
// by it (SF-2j): whether it is POSTED, its branch (empty when it has none: a dealer wide journal), and its net effect
// on one GL account (debits minus credits on that account's lines).
type BankJournalFacts struct {
	Posted    bool
	BranchID  string
	NetCents  int64 // debits minus credits on the named account's lines
	TouchesGL bool  // at least one line is on that account
	// Reversed: another journal names this one in reverses_journal_id. The GL keeps the original POSTED, so Posted
	// alone cannot say a journal was cancelled.
	Reversed bool
	// ReversesAnother: this journal is itself a reversal of another.
	ReversesAnother bool
	// IdempotencyKey is the key the journal was posted under; a caller that posts its own journals recognises them
	// by its key prefix.
	IdempotencyKey string
}

// JournalForBank answers BankJournalFacts for one journal and one GL account code, found false when the id is no
// journal (or no uuid). bankrec reads a journal through this seam and never from the table.
func (s *Service) JournalForBank(ctx context.Context, journalID, glAccountCode string) (BankJournalFacts, bool, error) {
	if _, err := uuid.Parse(journalID); err != nil {
		return BankJournalFacts{}, false, nil
	}
	j, err := s.store.GetJournal(ctx, journalID)
	if errors.Is(err, errNotFound) {
		return BankJournalFacts{}, false, nil
	}
	if err != nil {
		return BankJournalFacts{}, false, err
	}
	reversed, err := s.store.JournalIsReversed(ctx, journalID)
	if err != nil {
		return BankJournalFacts{}, false, err
	}
	f := BankJournalFacts{Posted: j.Status == StatusPosted, BranchID: j.BranchID, Reversed: reversed,
		ReversesAnother: j.ReversesJournalID != "", IdempotencyKey: j.IdempotencyKey}
	for _, l := range j.Lines {
		if l.AccountCode == glAccountCode {
			f.TouchesGL = true
			f.NetCents += l.DebitCents - l.CreditCents
		}
	}
	return f, true, nil
}
