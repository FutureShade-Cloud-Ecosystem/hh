package bankrec

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
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

// AutoMatch runs the APPROVED rules over the account's UNMATCHED credit lines in [from, to), posting each
// winner's clearing journal (via JournalPoster) and flipping the paired card payments to DEPOSITED. Every
// step is idempotent: a re-run only sees still-UNMATCHED lines, re-posts the same journal keys, and
// re-flips only RECEIVED payments (AC2-4).
func (s *Service) AutoMatch(ctx context.Context, accountID string, from, to time.Time, matchedBy string) (MatchSummary, error) {
	return s.AutoMatchReach(ctx, accountID, from, to, matchedBy, nil)
}

// AutoMatchReach is AutoMatch fenced to the caller's branch reach (SF-2g): the proposals are built from the till's
// unreconciled card payments, which a client cannot name, but the set is the dealer's whole book, so the candidates
// are cut to the payments of branches the caller reaches before any line is matched, and the claim fences again.
// A nil reach fences nothing.
func (s *Service) AutoMatchReach(ctx context.Context, accountID string, from, to time.Time, matchedBy string, reach func(string) bool) (MatchSummary, error) {
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
		if reach != nil {
			kept := pmts[:0:0]
			for _, p := range pmts {
				if reach(p.BranchID) {
					kept = append(kept, p)
				}
			}
			pmts = kept
		}
	}
	var lodg []Lodgement
	if s.lodgements != nil {
		if lodg, err = s.lodgements.OpenLodgements(ctx, from, to); err != nil {
			return MatchSummary{}, err
		}
		if reach != nil {
			kept := lodg[:0:0]
			for _, l := range lodg {
				if reach(l.BranchID) {
					kept = append(kept, l)
				}
			}
			lodg = kept
		}
	}

	summary := MatchSummary{}
	for _, line := range lines {
		prop, ok := matchLine(line, rules, pmts, lodg)
		if !ok {
			summary.Unmatched++
			continue
		}
		m, perr := s.persistProposal(ctx, prop, matchedBy, reach)
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
	if prop.TargetType == TargetLodgement && reach != nil {
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
	// A LODGEMENT target must exist and be in the caller's reach (SF-2g round 2); otherwise it answers exactly as a
	// missing target, with nothing written. The fact comes from the till's seam.
	if in.TargetType == TargetLodgement && in.CanAccessBranch != nil {
		if ok, lerr := s.lodgementReachable(ctx, in.TargetRefID, in.CanAccessBranch); lerr != nil {
			return Match{}, lerr
		} else if !ok {
			return Match{}, errNotFound
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

// Unmatch reverses a live match: it stamps the match unmatched (history kept), reverts its line to UNMATCHED,
// releases any claimed payment back to RECEIVED (all in one tx), and posts a REVERSING clearing journal under
// a NEW idempotency key (unmatchKey) that cancels the original legs, so the GL no longer carries a phantom
// clearing entry and a corrective re-match books its own fresh journal (never reusing the stale line key).
// NOTE: the reversal mirrors the target-type clearing legs by amount (PAYMENT: DR1010/CR1000; LODGEMENT:
// DR1020/CR1000), which is exact for gateway/lodgement matches; a fuzzy fee-split match leaves its 6300 fee
// residue for a controller adjustment (documented, fee is not carried on the match row).
func (s *Service) Unmatch(ctx context.Context, matchID, userID string) error {
	return s.UnmatchReach(ctx, matchID, userID, nil)
}

// lodgementReachable says whether a lodgement exists and sits in a branch the caller reaches, through the till's
// seam. An unwired seam cannot say, so with a reach to enforce it answers not reachable (fail closed).
func (s *Service) lodgementReachable(ctx context.Context, ref string, reach func(string) bool) (bool, error) {
	if s.lodgements == nil || ref == "" {
		return false, nil
	}
	branch, found, err := s.lodgements.LodgementBranch(ctx, ref)
	if err != nil {
		return false, err
	}
	return found && reach(branch), nil
}

// UnmatchReach is Unmatch fenced to the caller's branch reach (SF-2g round 2). A match whose target is a payment or a
// lodgement outside the reach answers exactly as a match that is not live, with nothing written. A nil reach fences
// nothing. For a PAYMENT target the check runs inside the unmatch's transaction under the payment row lock; for a
// LODGEMENT target it is read through the till's seam first (the lodgement's branch is immutable). JOURNAL and GROUP
// targets carry no branch bankrec can read: they move no money and post no journal, and are listed in the report.
func (s *Service) UnmatchReach(ctx context.Context, matchID, userID string, reach func(string) bool) error {
	if matchID == "" {
		return badRequest("bankrec: match_id is required")
	}
	if reach != nil {
		if tt, ref, live, perr := s.repo.PeekMatchTarget(ctx, matchID); perr != nil {
			return perr
		} else if live && tt == TargetLodgement {
			if ok, lerr := s.lodgementReachable(ctx, ref, reach); lerr != nil {
				return lerr
			} else if !ok {
				return conflict("bankrec: match %s is not live (already unmatched)", matchID)
			}
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
	if stmtBal == 0 {
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
		BankAccountID:         acct.ID,
		Year:                  in.Year,
		Month:                 in.Month,
		StatementBalanceCents: stmtBal,
		GLBalanceCents:        glBal,
		VarianceCents:         stmtBal - glBal,
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
	out, err := s.repo.CompleteReconciliation(ctx, id, acct.GLAccountCode, completedBy)
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
