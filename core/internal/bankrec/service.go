package bankrec

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
)

// - import -

// Accounts lists the 9 bank/card accounts (the console grid).
func (s *Service) Accounts(ctx context.Context) ([]BankAccount, error) {
	return s.repo.ListAccounts(ctx)
}

// Import parses a statement export under a profile, dedupes it into bank_statement_lines, and records an
// IMPORTED (or FAILED) audit. Re-importing the same file inserts 0 new lines (AC1). The parser is pluggable:
// an injected StatementSource wins; otherwise the built-in CSV parser is used (AC8).
func (s *Service) Import(ctx context.Context, in ImportInput, importedBy string) (ImportResult, error) {
	if strings.TrimSpace(in.BankAccountID) == "" {
		return ImportResult{}, badRequest("bankrec: bank_account_id is required")
	}
	if strings.TrimSpace(in.Content) == "" {
		return ImportResult{}, badRequest("bankrec: content is required")
	}
	acct, err := s.repo.GetAccount(ctx, in.BankAccountID)
	if err != nil {
		return ImportResult{}, err
	}
	profileCode := in.ProfileCode
	if profileCode == "" {
		if acct.AccountType == AccountCreditCard {
			profileCode = "visa-default"
		} else {
			profileCode = "rbc-default"
		}
	}
	profile, err := s.repo.ProfileByCode(ctx, profileCode)
	if err != nil {
		return ImportResult{}, err
	}

	src := s.statementSource()
	lines, perr := src.Parse(ctx, profile, strings.NewReader(in.Content))
	if perr != nil {
		// A parse failure is recorded as a FAILED import (audit trail) and returned as a 422, not a 500.
		importID, aerr := s.repo.RecordFailedImport(ctx, acct.ID, profile.ID, in.Filename, perr.Error(), importedBy)
		if aerr != nil {
			return ImportResult{}, aerr
		}
		s.recordAudit(ctx, "BANKREC_IMPORT_FAILED", "bankrec.import", importID, importedBy, "statement import failed", perr.Error())
		return ImportResult{ImportID: importID, Status: ImportStatusFailed, Error: perr.Error()},
			unprocessable("bankrec: statement parse failed: %v", perr)
	}

	importID, inserted, err := s.repo.ImportLines(ctx, acct.ID, profile.ID, in.Filename, lines, importedBy)
	if err != nil {
		return ImportResult{}, err
	}
	s.recordAudit(ctx, "BANKREC_IMPORT", "bankrec.import", importID, importedBy, "statement import", "")
	return ImportResult{
		ImportID:   importID,
		Status:     ImportStatusImported,
		Parsed:     len(lines),
		Inserted:   inserted,
		Duplicates: len(lines) - inserted,
	}, nil
}

// Lines lists an account's statement lines (optionally filtered by status).
// Cursor pagination (HH-3.2 B5): the total echo is gone with the offset
// family; the cursor envelope carries no total.
func (s *Service) Lines(ctx context.Context, f LinesFilter) ([]StatementLine, error) {
	if f.AccountID == "" {
		return nil, badRequest("bankrec: bank_account_id is required")
	}
	return s.repo.ListLines(ctx, f)
}

// statementSource returns the injected parser or the built-in CSV impl (pluggable, AC8).
func (s *Service) statementSource() StatementSource {
	if s.source != nil {
		return s.source
	}
	return CSVStatementSource{}
}

// - matching -

// MatchSummary reports the outcome of an auto-match run.
type MatchSummary struct {
	Matched   int     `json:"matched"`
	Unmatched int     `json:"unmatched"`
	Matches   []Match `json:"matches"`
}

// AutoMatchReach runs the APPROVED rules over the account's UNMATCHED credit lines in [from, to), posting each
// winner's clearing journal (via JournalPoster) and flipping the paired card payments to DEPOSITED. Every
// step is idempotent: a re-run only sees still-UNMATCHED lines, re-posts the same journal keys, and
// re-flips only RECEIVED payments (AC2-4). It is fenced to the caller's branch reach (SF-2g): the proposals are
// built from the till's unreconciled card payments, which a client cannot name, but the set is the dealer's whole
// book, so the candidates are cut to the payments and lodgements of branches the caller reaches before any line is
// matched, and the claim fences again. A nil reach REFUSES (SF-2d round 5); a dealer wide caller passes AllBranches.
func (s *Service) AutoMatchReach(ctx context.Context, accountID string, from, to time.Time, matchedBy string, reach func(string) bool) (MatchSummary, error) {
	if reach == nil {
		return MatchSummary{}, errNoReach
	}
	if accountID == "" {
		return MatchSummary{}, badRequest("bankrec: bank_account_id is required")
	}
	lines, err := s.repo.UnmatchedCreditLines(ctx, accountID, from, to)
	if err != nil {
		return MatchSummary{}, err
	}
	rules, err := s.repo.ApprovedRules(ctx)
	if err != nil {
		return MatchSummary{}, err
	}
	var pmts []CandidatePayment
	if s.payments != nil {
		if pmts, err = s.payments.UnreconciledCardPayments(ctx, from, to); err != nil {
			return MatchSummary{}, err
		}
		kept := pmts[:0:0]
		for _, p := range pmts {
			if reach(p.BranchID) {
				kept = append(kept, p)
			}
		}
		pmts = kept
	}
	var lodg []Lodgement
	if s.lodgements != nil {
		if lodg, err = s.lodgements.OpenLodgements(ctx, from, to); err != nil {
			return MatchSummary{}, err
		}
		kept := lodg[:0:0]
		for _, l := range lodg {
			if reach(l.BranchID) {
				kept = append(kept, l)
			}
		}
		lodg = kept
	}

	summary := MatchSummary{}
	for _, line := range lines {
		prop, ok := matchLine(line, rules, pmts, lodg)
		if !ok {
			summary.Unmatched++
			continue
		}
		m, perr := s.persistProposal(ctx, prop, matchedBy, reach)
		if errors.Is(perr, errMonthCompleted) {
			// A line imported into a signed off month stays unmatched; the run goes on.
			summary.Unmatched++
			continue
		}
		if perr != nil {
			return summary, perr
		}
		summary.Matched++
		summary.Matches = append(summary.Matches, m)
		// Consume the matched target so a later line in the same run cannot re-use it.
		pmts = removePayments(pmts, prop.PaymentIDs)
		if prop.TargetType == TargetLodgement {
			lodg = removeLodgement(lodg, prop.TargetRef)
		}
	}
	return summary, nil
}

// persistProposal claims the match (flip line + claim payment + insert match row atomically), THEN posts the
// clearing journal and writes its id back. Ordering is deliberate: PersistMatch's guards (line still
// UNMATCHED, payment still RECEIVED, no live match on the target) run BEFORE any journal is posted, so a
// duplicate/racing match is rejected with NO second clearing journal. The journal is keyed on the match id so
// a corrective re-match posts fresh legs.
func (s *Service) persistProposal(ctx context.Context, prop proposal, matchedBy string, reach func(string) bool) (Match, error) {
	m := Match{
		StatementLineID: prop.Line.ID,
		TargetType:      prop.TargetType,
		TargetRefID:     prop.TargetRef,
		Method:          MethodAuto,
		RuleID:          prop.Rule.ID,
		AmountCents:     prop.Line.AmountCents,
		MatchedBy:       matchedBy,
	}
	// Where the match is written, a LODGEMENT target is fenced again (the candidates were cut already).
	if prop.TargetType == TargetLodgement {
		if ok, lerr := s.lodgementReachable(ctx, prop.TargetRef, reach); lerr != nil {
			return Match{}, lerr
		} else if !ok {
			return Match{}, errNotFound
		}
	}
	id, err := s.repo.PersistMatchReach(ctx, m, prop.PaymentIDs, reach)
	if err != nil {
		return Match{}, err
	}
	m.ID = id
	if s.poster != nil {
		prop.Journal.IdempotencyKey = clearingKey(id)
		journalID, perr := s.poster.PostJournal(ctx, prop.Journal)
		if perr != nil {
			return Match{}, perr
		}
		m.JournalID = journalID
		if err := s.repo.SetMatchJournal(ctx, id, journalID); err != nil {
			return Match{}, err
		}
	}
	return m, nil
}

// ManualMatch pairs a statement line to a target by hand (method MANUAL), posting the matching clearing
// journal for PAYMENT/LODGEMENT targets. GROUP/JOURNAL targets post no new journal. Kept in the same match
// history as AUTO matches (AC6).
func (s *Service) ManualMatch(ctx context.Context, in MatchInput, matchedBy string) (Match, error) {
	if in.CanAccessBranch == nil {
		return Match{}, errNoReach // a nil reach never means "no fence" (SF-2d round 5); a dealer wide caller passes AllBranches
	}
	if in.StatementLineID == "" {
		return Match{}, badRequest("bankrec: statement_line_id is required")
	}
	if !validTarget(in.TargetType) {
		return Match{}, badRequest("bankrec: target_type must be PAYMENT, LODGEMENT, JOURNAL or GROUP")
	}
	line, err := s.repo.GetLine(ctx, in.StatementLineID)
	if err != nil {
		return Match{}, err
	}
	if line.Status != LineUnmatched {
		return Match{}, conflict("bankrec: statement line %s is %s, not UNMATCHED", line.ID, line.Status)
	}
	amount := in.AmountCents
	if amount == 0 {
		amount = line.AmountCents
	}
	m := Match{
		StatementLineID: line.ID,
		TargetType:      in.TargetType,
		TargetRefID:     in.TargetRefID,
		Method:          MethodManual,
		AmountCents:     amount,
		MatchedBy:       matchedBy,
	}
	// A PAYMENT target is claimed in the same tx (RECEIVED→DEPOSITED); LODGEMENT/JOURNAL/GROUP claim nothing.
	var claim []string
	if in.TargetType == TargetPayment && in.TargetRefID != "" {
		claim = []string{in.TargetRefID}
	}
	switch in.TargetType {
	case TargetGroup:
		// GROUP is a "manual grouping header" (migration 0033, matcher.go) with no member set: nothing resolves
		// what it groups, so a match to it explains no money. Refused until members exist.
		return Match{}, unprocessable("bankrec: a group match is not available yet, match the line to a payment, a lodgement or a journal")
	case TargetLodgement:
		// A LODGEMENT target must exist and be in the caller's reach (SF-2g round 2); otherwise it answers exactly as
		// a missing target, with nothing written. The facts come from the till's seam. The lodgement must also be
		// for the line's own amount: the clearing journal posts the line's amount, and the matcher's own tolerance
		// for a lodgement reference is zero (SF-2j).
		facts, found, lerr := s.lodgementFacts(ctx, in.TargetRefID)
		if lerr != nil {
			return Match{}, lerr
		}
		if !found || !in.CanAccessBranch(facts.BranchID) {
			return Match{}, errNotFound
		}
		if facts.AmountCents != line.AmountCents {
			return Match{}, unprocessable("bankrec: that lodgement is for %d cents and the line is %d cents, so it does not explain the line", facts.AmountCents, line.AmountCents)
		}
	case TargetJournal:
		if jerr := s.checkJournalTarget(ctx, line, in); jerr != nil {
			return Match{}, jerr
		}
	}
	id, err := s.repo.PersistMatchReach(ctx, m, claim, in.CanAccessBranch)
	if err != nil {
		return Match{}, err
	}
	m.ID = id
	// Post the clearing journal AFTER the match is claimed (a rejected duplicate posts none), keyed on match id.
	if journal, post := manualJournal(line, in.TargetType, ""); post && s.poster != nil {
		journal.IdempotencyKey = clearingKey(id)
		journalID, perr := s.poster.PostJournal(ctx, journal)
		if perr != nil {
			return Match{}, perr
		}
		m.JournalID = journalID
		if err := s.repo.SetMatchJournal(ctx, id, journalID); err != nil {
			return Match{}, err
		}
	}
	s.recordAudit(ctx, "BANKREC_MANUAL_MATCH", "bankrec.statement_line", line.ID, matchedBy, "manual match", "")
	return m, nil
}

// lodgementReachable says whether a lodgement exists and sits in a branch the caller reaches, through the till's
// seam. An unwired seam cannot say, so with a reach to enforce it answers not reachable (fail closed).
func (s *Service) lodgementReachable(ctx context.Context, ref string, reach func(string) bool) (bool, error) {
	if s.lodgements == nil || ref == "" {
		return false, nil
	}
	f, found, err := s.lodgements.LodgementFacts(ctx, ref)
	if err != nil {
		return false, err
	}
	return found && reach(f.BranchID), nil
}

// lodgementFacts reads a lodgement's branch and amount through the till's seam. An unwired seam or an empty id is a
// missing lodgement (fail closed).
func (s *Service) lodgementFacts(ctx context.Context, ref string) (LodgementFact, bool, error) {
	if s.lodgements == nil || ref == "" {
		return LodgementFact{}, false, nil
	}
	return s.lodgements.LodgementFacts(ctx, ref)
}

// checkJournalTarget refuses a JOURNAL target that does not explain the line: it must be a POSTED journal (read
// through the GL seam) that touches the bank account's GL code for exactly the line's signed amount. A journal with
// a branch outside the caller's reach answers as missing; a journal with no branch is dealer wide, like the bank
// line itself. That no other live match holds it is checked in the write's transaction (PersistMatchReach).
func (s *Service) checkJournalTarget(ctx context.Context, line StatementLine, in MatchInput) error {
	if s.journals == nil || in.TargetRefID == "" {
		return errNotFound
	}
	acct, err := s.repo.GetAccount(ctx, line.BankAccountID)
	if err != nil {
		return err
	}
	f, found, err := s.journals.JournalForBank(ctx, in.TargetRefID, acct.GLAccountCode)
	if err != nil {
		return err
	}
	if !found || (f.BranchID != "" && !in.CanAccessBranch(f.BranchID)) {
		return errNotFound
	}
	return journalExplains(f, line.AmountCents)
}

// journalExplains says whether the facts of a journal let it explain a line of the given signed amount: POSTED, not
// reversed, not itself a reversal, not one of bankrec's own postings (a clearing or an unmatch's reversal), touching
// the bank account, for exactly the line's amount. The same rule runs at match time and again when a month is
// completed.
func journalExplains(f gl.BankJournalFacts, lineCents int64) error {
	if !f.Posted {
		return unprocessable("bankrec: that journal is not posted, so it cannot explain a bank line")
	}
	if f.Reversed {
		return unprocessable("bankrec: that journal has been reversed, so it no longer explains a bank line")
	}
	if f.ReversesAnother {
		return unprocessable("bankrec: that journal is itself a reversal, so it cannot explain a bank line")
	}
	if strings.HasPrefix(f.IdempotencyKey, "bankrec:") {
		return unprocessable("bankrec: that journal was posted by the bank reconciliation itself, so it cannot explain a bank line")
	}
	if !f.TouchesGL {
		return unprocessable("bankrec: that journal does not touch this bank account")
	}
	if f.NetCents != lineCents {
		return unprocessable("bankrec: that journal moves this bank account by %d cents and the line is %d cents", f.NetCents, lineCents)
	}
	return nil
}

// dollars writes cents as a dealer reads them, "$1,234.50" ("-$45.00" below zero).
func dollars(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	whole := strconv.FormatInt(cents/100, 10)
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	return fmt.Sprintf("%s$%s.%02d", sign, whole, cents%100)
}

// ledgerBalance reads the bank account's GL balance for a month through the GLBalanceReader seam (no GL table read
// here), for CompleteReconciliation to take inside its transaction after the month's lock. An unwired reader refuses
// completion in plain words.
func (s *Service) ledgerBalance(glCode string) func(ctx context.Context, year, month int) (int64, error) {
	return func(ctx context.Context, year, month int) (int64, error) {
		if s.balances == nil {
			return 0, unprocessable("bankrec: cannot complete, the ledger balance cannot be read")
		}
		return s.balances.GLBalanceCents(ctx, glCode, year, month, "")
	}
}

// recheckJournalMatches is CompleteReconciliation's recheck (SF-2j round 2): every live JOURNAL match in the month is
// read again through the GL seam and must still explain its line. A journal reversed since the match, or one that no
// longer qualifies, refuses the sign off and names the line. It runs inside the completion's transaction, after the
// month's lines are locked, so the set of matches cannot change under it. A month with journal matches cannot be
// rechecked without the seam, and is refused.
func (s *Service) recheckJournalMatches(glCode string) func(ctx context.Context, matches []JournalMatchFact) error {
	return func(ctx context.Context, matches []JournalMatchFact) error {
		if len(matches) == 0 {
			return nil
		}
		for _, m := range matches {
			if s.journals == nil {
				return unprocessable("bankrec: cannot complete, the journal matched to %q cannot be checked", m.LineDescription)
			}
			f, found, err := s.journals.JournalForBank(ctx, m.JournalID, glCode)
			if err != nil {
				return err
			}
			if !found {
				return unprocessable("bankrec: cannot complete, the journal matched to the statement line %q no longer exists", m.LineDescription)
			}
			if jerr := journalExplains(f, m.LineAmountCents); jerr != nil {
				return unprocessable("bankrec: cannot complete, the journal matched to the statement line %q no longer explains it (%s)", m.LineDescription, strings.TrimPrefix(jerr.Error(), "bankrec: that journal "))
			}
		}
		return nil
	}
}

// UnmatchReach reverses a live match: it stamps the match unmatched (history kept), reverts its line to UNMATCHED,
// releases any claimed payment back to RECEIVED (all in one tx), and posts a REVERSING clearing journal under
// a NEW idempotency key (unmatchKey) that cancels the original legs, so the GL no longer carries a phantom
// clearing entry and a corrective re-match books its own fresh journal (never reusing the stale line key).
// NOTE: the reversal mirrors the target-type clearing legs by amount (PAYMENT: DR1010/CR1000; LODGEMENT:
// DR1020/CR1000), which is exact for gateway/lodgement matches; a fuzzy fee-split match leaves its 6300 fee
// residue for a controller adjustment (documented, fee is not carried on the match row).
//
// It is fenced to the caller's branch reach (SF-2g round 2). A match whose target is a payment or a
// lodgement outside the reach answers exactly as a match that is not live, with nothing written. A nil reach REFUSES
// (SF-2d round 5). For a PAYMENT target the check runs inside the unmatch's transaction under the payment row lock; for a
// LODGEMENT target it is read through the till's seam first (the lodgement's branch is immutable). JOURNAL and GROUP
// targets carry no branch bankrec can read: they move no money and post no journal, and are listed in the report.
func (s *Service) UnmatchReach(ctx context.Context, matchID, userID string, reach func(string) bool) error {
	if reach == nil {
		return errNoReach
	}
	if matchID == "" {
		return badRequest("bankrec: match_id is required")
	}
	if tt, ref, live, perr := s.repo.PeekMatchTarget(ctx, matchID); perr != nil {
		return perr
	} else if live && tt == TargetLodgement {
		if ok, lerr := s.lodgementReachable(ctx, ref, reach); lerr != nil {
			return lerr
		} else if !ok {
			return conflict("bankrec: match %s is not live (already unmatched)", matchID)
		}
	}
	targetType, _, amount, err := s.repo.UnmatchReach(ctx, matchID, userID, reach)
	if err != nil {
		return err
	}
	if s.poster != nil && amount > 0 {
		if rev, ok := reversalJournal(matchID, targetType, amount); ok {
			if _, perr := s.poster.PostJournal(ctx, rev); perr != nil {
				return perr
			}
		}
	}
	s.recordAudit(ctx, "BANKREC_UNMATCH", "bankrec.match", matchID, userID, "manual unmatch", "")
	return nil
}

// MatchHistory returns the full match history (live + unmatched) for a statement line.
func (s *Service) MatchHistory(ctx context.Context, lineID string) ([]Match, error) {
	if lineID == "" {
		return nil, badRequest("bankrec: statement_line_id is required")
	}
	return s.repo.ListMatches(ctx, lineID)
}

// - rules -

// Rules lists match rules (all versions by default, or only APPROVED when live=true).
func (s *Service) Rules(ctx context.Context, liveOnly bool) ([]MatchRule, error) {
	if liveOnly {
		return s.repo.ApprovedRules(ctx)
	}
	return s.repo.AllRules(ctx)
}

// ApproveRule promotes a new rule version to APPROVED, superseding (and retiring) the current APPROVED for
// the code, the supersedes chain (AC5).
func (s *Service) ApproveRule(ctx context.Context, in RuleInput, approvedBy string) (MatchRule, error) {
	if strings.TrimSpace(in.Code) == "" {
		return MatchRule{}, badRequest("bankrec: rule code is required")
	}
	if len(in.Spec) == 0 {
		return MatchRule{}, badRequest("bankrec: rule spec is required")
	}
	if kind := specStr(in.Spec, "kind"); kind != KindGatewayRefExact && kind != KindLodgementRefExact && kind != KindDescriptorFuzzy {
		return MatchRule{}, unprocessable("bankrec: spec.kind must be one of gateway_ref_exact, lodgement_ref_exact, descriptor_fuzzy")
	}
	rule, err := s.repo.ApproveNewRuleVersion(ctx, in, approvedBy)
	if err != nil {
		return MatchRule{}, err
	}
	s.recordAudit(ctx, "BANKREC_RULE_APPROVE", "bankrec.rule", rule.ID, approvedBy,
		"approve match rule "+rule.Code, "")
	return rule, nil
}

// - reconciliation -

// OpenReconciliation prepares (or refreshes) the monthly reconciliation: it reads the GL balance through the
// GLBalanceReader seam (never the 0034 views directly) and computes the variance against the statement
// balance (the caller's real closing balance, or the month's line net as a placeholder default).
func (s *Service) OpenReconciliation(ctx context.Context, in ReconInput) (Reconciliation, error) {
	if in.BankAccountID == "" {
		return Reconciliation{}, badRequest("bankrec: bank_account_id is required")
	}
	if in.Month < 1 || in.Month > 12 || in.Year < 2000 {
		return Reconciliation{}, badRequest("bankrec: valid year and month (1-12) are required")
	}
	acct, err := s.repo.GetAccount(ctx, in.BankAccountID)
	if err != nil {
		return Reconciliation{}, err
	}
	stmtBal := in.StatementBalanceCents
	derived := stmtBal == 0
	if derived {
		if stmtBal, err = s.repo.MonthLineSum(ctx, acct.ID, in.Year, in.Month); err != nil {
			return Reconciliation{}, err
		}
	}
	var glBal int64
	if s.balances != nil {
		if glBal, err = s.balances.GLBalanceCents(ctx, acct.GLAccountCode, in.Year, in.Month, ""); err != nil {
			return Reconciliation{}, err
		}
	}
	return s.repo.UpsertReconciliation(ctx, Reconciliation{
		BankAccountID:           acct.ID,
		Year:                    in.Year,
		Month:                   in.Month,
		StatementBalanceCents:   stmtBal,
		StatementBalanceDerived: derived,
		GLBalanceCents:          glBal,
		VarianceCents:           stmtBal - glBal,
	})
}

// Reconciliations lists reconciliations (optionally scoped to an account).
func (s *Service) Reconciliations(ctx context.Context, accountID string) ([]Reconciliation, error) {
	return s.repo.ListReconciliations(ctx, accountID)
}

// CompleteReconciliation signs off a monthly reconciliation. Guards (AC7): the account must have ZERO
// UNMATCHED lines in the month AND zero variance. On success it flips COMPLETED and emits
// bank.recon.completed in the same tx.
func (s *Service) CompleteReconciliation(ctx context.Context, id, completedBy string) (Reconciliation, error) {
	if id == "" {
		return Reconciliation{}, badRequest("bankrec: reconciliation id is required")
	}
	// A malformed (non-UUID) id must not reach the uuid PK cast (SQLSTATE 22P02 → HTTP 500). Guard
	// here rather than in the repo: under pgx's default cache-statement mode the bad-uuid failure can
	// surface as a client-side encode error, not a server PgError, which a code=="22P02" check would
	// miss. errNotFound (404) keeps a malformed id indistinguishable from an absent one.
	if _, err := uuid.Parse(id); err != nil {
		return Reconciliation{}, errNotFound
	}
	rec, err := s.repo.GetReconciliation(ctx, id)
	if err != nil {
		return Reconciliation{}, err
	}
	if rec.Status == ReconCompleted {
		return Reconciliation{}, conflict("bankrec: reconciliation %s is already COMPLETED", id)
	}
	unmatched, err := s.repo.CountUnmatchedInMonth(ctx, rec.BankAccountID, rec.Year, rec.Month)
	if err != nil {
		return Reconciliation{}, err
	}
	if unmatched > 0 {
		return Reconciliation{}, unprocessable("bankrec: cannot complete, %d UNMATCHED line(s) remain for %d-%02d", unmatched, rec.Year, rec.Month)
	}
	if rec.VarianceCents != 0 {
		return Reconciliation{}, unprocessable("bankrec: cannot complete, variance is %d cents (must be zero)", rec.VarianceCents)
	}
	acct, err := s.repo.GetAccount(ctx, rec.BankAccountID)
	if err != nil {
		return Reconciliation{}, err
	}
	out, err := s.repo.CompleteReconciliation(ctx, id, acct.GLAccountCode, completedBy, s.recheckJournalMatches(acct.GLAccountCode), s.ledgerBalance(acct.GLAccountCode))
	if err != nil {
		return Reconciliation{}, err
	}
	s.recordAudit(ctx, "BANKREC_RECON_COMPLETE", "bankrec.reconciliation", id, completedBy,
		"month reconciliation completed", "")
	return out, nil
}

// - helpers -

func (s *Service) recordAudit(ctx context.Context, action, entityType, entityID, actorID, purpose, reason string) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Record(ctx, audit.SensitiveAccess{
		Action:     action,
		EntityType: entityType,
		EntityID:   entityID,
		ActorID:    actorID,
		Purpose:    purpose,
		Reason:     reason,
	})
}

func validTarget(t string) bool {
	switch t {
	case TargetPayment, TargetLodgement, TargetJournal, TargetGroup:
		return true
	}
	return false
}

func removePayments(pmts []CandidatePayment, ids []string) []CandidatePayment {
	if len(ids) == 0 {
		return pmts
	}
	drop := map[string]bool{}
	for _, id := range ids {
		drop[id] = true
	}
	out := pmts[:0:0]
	for _, p := range pmts {
		if !drop[p.PaymentID] {
			out = append(out, p)
		}
	}
	return out
}

func removeLodgement(lodg []Lodgement, movementID string) []Lodgement {
	out := lodg[:0:0]
	for _, l := range lodg {
		if l.MovementID != movementID {
			out = append(out, l)
		}
	}
	return out
}
