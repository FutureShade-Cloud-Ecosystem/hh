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
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// - import -

// Accounts lists the 9 bank/card accounts (the console grid).
func (s *Service) Accounts(ctx context.Context) ([]BankAccount, error) {
	return s.repo.ListAccounts(ctx)
}

// RecordCutover records the reconciliation starting point for a bank account (FW1-04 round 2):
// the cut-over date and opening statement balance accepted as reconciled to the book, plus an optional
// confirmed list of ledger entry journal IDs on or before that date that remain outstanding at cut-over.
// It is Financial Controller only, refused once a later month has signed off, and audited (BANKREC_CUTOVER).
func (s *Service) RecordCutover(ctx context.Context, in CutoverInput) (BankAccount, error) {
	caller, ok := identity.StaffContextFromContext(ctx)
	if !ok || caller.UserID == "" || (caller.RoleCode != "FINANCIAL_CONTROLLER" && caller.RoleCode != "financial_controller") {
		return BankAccount{}, forbidden("bankrec: only the Financial Controller may record a bank reconciliation cut-over")
	}
	if strings.TrimSpace(in.BankAccountID) == "" {
		return BankAccount{}, badRequest("bankrec: bank_account_id is required")
	}
	if _, err := time.Parse("2006-01-02", in.CutoverDate); err != nil {
		return BankAccount{}, badRequest("bankrec: cutover_date must be YYYY-MM-DD, got %q", in.CutoverDate)
	}
	for _, jid := range in.OutstandingJournalIDs {
		if _, err := uuid.Parse(jid); err != nil {
			return BankAccount{}, badRequest("bankrec: invalid outstanding journal id %q", jid)
		}
	}
	acct, err := s.repo.GetAccount(ctx, in.BankAccountID)
	if err != nil {
		return BankAccount{}, err
	}
	if completedY, completedM, done, cerr := s.repo.FirstCompletedMonth(ctx, acct.ID); cerr != nil {
		return BankAccount{}, cerr
	} else if done {
		return BankAccount{}, conflict("bankrec: cannot record cut-over at %s because month %d-%02d is already completed",
			in.CutoverDate, completedY, completedM)
	}
	if err := s.checkCutoverReconciles(ctx, acct, in); err != nil {
		return BankAccount{}, err
	}
	recored, err := s.repo.RecordAccountCutover(ctx, in, caller.UserID)
	if err != nil {
		return BankAccount{}, err
	}
	s.recordAudit(ctx, "BANKREC_CUTOVER", "bankrec.account", recored.ID, caller.UserID,
		"record bank reconciliation cut-over",
		fmt.Sprintf("date=%s balance=%d outstanding=%d", in.CutoverDate, in.OpeningStatementBalanceCents, len(in.OutstandingJournalIDs)))
	return recored, nil
}

type asOfBalanceReader interface {
	GLBalanceAsOfCents(ctx context.Context, accountCode string, asOf time.Time, branchID string) (int64, error)
}

func (s *Service) bookBalanceAsOf(ctx context.Context, glAccountCode string, asOf time.Time, entries []gl.BankBookEntry) (int64, error) {
	if ar, ok := s.balances.(asOfBalanceReader); ok {
		return ar.GLBalanceAsOfCents(ctx, glAccountCode, asOf, "")
	}
	if ar, ok := s.journals.(asOfBalanceReader); ok {
		return ar.GLBalanceAsOfCents(ctx, glAccountCode, asOf, "")
	}
	var sum int64
	for _, e := range entries {
		sum += e.NetCents
	}
	return sum, nil
}

// checkCutoverReconciles is round 4's P1 (FROM-PRINCIPAL 181 and 183): a starting point must reconcile. The
// statement balance on the cut over date must equal the ledger's own book balance at that date less the ticked
// outstanding cheques plus the ticked deposits in transit, that is, the ledger balance less the net of the
// ticked entries. Anything else plants a wrong opening in every later month, so the server refuses it in the
// desk's words, naming the difference.
func (s *Service) checkCutoverReconciles(ctx context.Context, acct BankAccount, in CutoverInput) error {
	br, ok := s.journals.(bankBookEntryReader)
	if s.journals == nil || !ok {
		return unprocessable("bankrec: the ledger's entries cannot be read on this box")
	}
	zone, zerr := houseday.Resolve(ctx, s.zone)
	if zerr != nil {
		zone = houseday.DefaultZone()
	}
	day, err := time.ParseInLocation("2006-01-02", in.CutoverDate, zone.Location())
	if err != nil {
		return badRequest("bankrec: cutover_date must be YYYY-MM-DD, got %q", in.CutoverDate)
	}
	asOf := day.AddDate(0, 0, 1).Add(-time.Nanosecond)
	entries, err := br.BankAccountEntriesAsOf(ctx, acct.GLAccountCode, asOf)
	if err != nil {
		return err
	}
	book, err := s.bookBalanceAsOf(ctx, acct.GLAccountCode, asOf, entries)
	if err != nil {
		return err
	}
	confirmed := make(map[string]bool, len(in.OutstandingJournalIDs))
	for _, id := range in.OutstandingJournalIDs {
		confirmed[id] = true
	}
	var tickedNet int64
	for _, e := range entries {
		if confirmed[e.JournalID] || (e.SourceRefID != "" && confirmed[e.SourceRefID]) {
			tickedNet += e.NetCents
		}
	}
	if diff := in.OpeningStatementBalanceCents - (book - tickedNet); diff != 0 {
		size := diff
		if size < 0 {
			size = -size
		}
		return unprocessable("bankrec: cannot record cut-over at %s: the statement and the book differ by %s; tick the items still outstanding at cut over, or check the date and balance",
			in.CutoverDate, dollars(size))
	}
	return nil
}

// CutoverPreview answers the starting point form's read (FW1-04 round 3 and round 4): the ledger's entries on
// the account's GL code on or before the cut over date (the same read the reconciliation's outstanding lists
// draw from), with the ledger's own book balance at that date. When the read carries the form's typed statement
// balance and its ticked entries (round 4), the answer also carries the difference between them and whether it
// reconciles, so the desk shows the server's difference and never computes one of its own.
func (s *Service) CutoverPreview(ctx context.Context, accountID, date string, statementCents *int64, ticked []string) (CutoverPreview, error) {
	acct, err := s.repo.GetAccount(ctx, accountID)
	if err != nil {
		return CutoverPreview{}, err
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return CutoverPreview{}, badRequest("bankrec: cutover_date must be YYYY-MM-DD, got %q", date)
	}
	br, ok := s.journals.(bankBookEntryReader)
	if s.journals == nil || !ok {
		return CutoverPreview{}, unprocessable("bankrec: the ledger's entries cannot be read on this box")
	}
	zone, zerr := houseday.Resolve(ctx, s.zone)
	if zerr != nil {
		zone = houseday.DefaultZone()
	}
	day, err := time.ParseInLocation("2006-01-02", date, zone.Location())
	if err != nil {
		return CutoverPreview{}, badRequest("bankrec: cutover_date must be YYYY-MM-DD, got %q", date)
	}
	asOf := day.AddDate(0, 0, 1).Add(-time.Nanosecond)
	entries, err := br.BankAccountEntriesAsOf(ctx, acct.GLAccountCode, asOf)
	if err != nil {
		return CutoverPreview{}, err
	}
	book, err := s.bookBalanceAsOf(ctx, acct.GLAccountCode, asOf, entries)
	if err != nil {
		return CutoverPreview{}, err
	}
	confirmed := make(map[string]bool, len(ticked))
	for _, id := range ticked {
		confirmed[id] = true
	}
	out := CutoverPreview{BookBalanceCents: book}
	var tickedNet int64
	for _, e := range entries {
		out.Entries = append(out.Entries, CutoverPreviewEntry{
			JournalID:   e.JournalID,
			PostedAt:    e.PostedAt,
			Memo:        e.Memo,
			AmountCents: e.NetCents,
		})
		if confirmed[e.JournalID] || (e.SourceRefID != "" && confirmed[e.SourceRefID]) {
			tickedNet += e.NetCents
		}
	}
	if statementCents != nil {
		diff := *statementCents - (out.BookBalanceCents - tickedNet)
		out.DifferenceCents = &diff
		reconciles := diff == 0
		out.Reconciles = &reconciles
	}
	return out, nil
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
	// A lodgement whose own journal has posted is only confirmed by its match (FROM-PRINCIPAL 117 and
	// 120), so the candidates carry the fact before the matcher pairs them.
	if err := s.markLodgementsWithJournals(ctx, lodg); err != nil {
		return MatchSummary{}, err
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

// lodgementJournalKey is the idempotency key the till's movement journal carries, so the GL's keyed
// existence read can name the lodgement's own journal (core/internal/gl/posting.go, the builder's key).
const lodgementJournalKey = "till.movement.posted:"

// carriesOwnJournal says whether a lodgement carries its OWN journal, the fact the match needs
// (FROM-PRINCIPAL 117 and 120): a lodgement that journaled at lodgement is only CONFIRMED by its match,
// which posts nothing, or the deposit is booked twice.
//
// The till's seam answers what the till knows (the slip's link rows, or the emission that carries the
// journal). The GL's seam answers whether the journal itself has posted. The rule does NOT rest on the
// outbox alone: an outbox row can be archived, and the journal that names the lodgement cannot.
func (s *Service) carriesOwnJournal(ctx context.Context, movementID string, tillSays bool) (bool, error) {
	if tillSays || movementID == "" || s.journalKeys == nil {
		return tillSays, nil
	}
	found, err := s.journalKeys.JournalKeysExist(ctx, []string{lodgementJournalKey + movementID})
	if err != nil {
		return false, err
	}
	return len(found) > 0, nil
}

// markLodgementsWithJournals marks the candidates whose own journal has posted, so the matcher confirms
// them instead of posting a second journal for the same deposit.
func (s *Service) markLodgementsWithJournals(ctx context.Context, lodg []Lodgement) error {
	if s.journalKeys == nil || len(lodg) == 0 {
		return nil
	}
	keys := make([]string, 0, len(lodg))
	for _, l := range lodg {
		if !l.CarriesJournal {
			keys = append(keys, lodgementJournalKey+l.MovementID)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	found, err := s.journalKeys.JournalKeysExist(ctx, keys)
	if err != nil {
		return err
	}
	posted := make(map[string]bool, len(found))
	for _, k := range found {
		posted[k] = true
	}
	for i := range lodg {
		if posted[lodgementJournalKey+lodg[i].MovementID] {
			lodg[i].CarriesJournal = true
			lodg[i].PaymentIDs = nil
			lodg[i].PaymentsUnresolved = false
		}
	}
	return nil
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
		// A lodgement whose covered payments could not be resolved says so on the answer (R2-P2-1).
		PaymentsUnresolved: prop.PaymentsUnresolved,
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
	// A lodgement that carries its OWN journal is only confirmed: the match posts no clearing journal
	// (FROM-PRINCIPAL 117 and 120), so the deposit is booked once, at lodgement.
	if s.poster != nil && !prop.ConfirmsOnly {
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
// journal for PAYMENT and older LODGEMENT targets. GROUP/JOURNAL targets (and new LODGEMENT targets that
// already posted DR 1000 / CR 1020 at lodgement time, FROM-PRINCIPAL 117) post no new journal. Kept in the
// same match history as AUTO matches (AC6).
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
	// A PAYMENT or LODGEMENT target claims its RECEIVED payment(s) in the same tx (RECEIVED to DEPOSITED).
	var claim []string
	var journalBranchID string
	if in.TargetType == TargetPayment && in.TargetRefID != "" {
		claim = []string{in.TargetRefID}
	}
	// carriesJournal says the lodgement target posted its own journal at lodgement, so the match only confirms
	// the line and posts nothing (FROM-PRINCIPAL 117 and 120).
	carriesJournal := false
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
		journalBranchID = facts.BranchID
		if carriesJournal, err = s.carriesOwnJournal(ctx, in.TargetRefID, facts.CarriesJournal); err != nil {
			return Match{}, err
		}
		if !carriesJournal {
			claim = facts.PaymentIDs
			// The lodgement's own answer-time fact rides the answer (R2-P2-1).
			m.PaymentsUnresolved = facts.PaymentsUnresolved
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
	if journal, post := manualJournal(line, in.TargetType, journalBranchID, carriesJournal); post && s.poster != nil {
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

// clock returns the service clock (defaults to time.Now).
func (s *Service) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

type lodgementJournalReader interface {
	LodgementJournalForBank(ctx context.Context, movementID, glAccountCode string) (string, bool, error)
}

// reconFigures holds the output of the single shared reconciliation computation (FW1-04) used by
// OpenReconciliation, RefreshReconciliation, CompleteReconciliation, and Reconciliations.
type reconFigures struct {
	OpeningBalanceCents      *int64
	StatementBalanceCents    int64
	StatementBalanceDerived  bool
	DepositsInTransitCents   int64
	OutstandingChequesCents  int64
	AdjustedBankBalanceCents int64
	GLBalanceCents           int64
	VarianceCents            int64
	MatchedLinesCount        int
	DepositsInTransit        []OutstandingItem
	OutstandingCheques       []OutstandingItem
}

func (rec *Reconciliation) applyFigures(f reconFigures) {
	rec.OpeningBalanceCents = f.OpeningBalanceCents
	rec.StatementBalanceCents = f.StatementBalanceCents
	rec.StatementBalanceDerived = f.StatementBalanceDerived
	rec.DepositsInTransitCents = f.DepositsInTransitCents
	rec.OutstandingChequesCents = f.OutstandingChequesCents
	rec.AdjustedBankBalanceCents = f.AdjustedBankBalanceCents
	rec.GLBalanceCents = f.GLBalanceCents
	rec.VarianceCents = f.VarianceCents
	rec.MatchedLinesCount = f.MatchedLinesCount
	rec.DepositsInTransit = f.DepositsInTransit
	rec.OutstandingCheques = f.OutstandingCheques
}

type closingBalanceReader interface {
	GLClosingBalanceCents(ctx context.Context, accountCode string, year, month int, branchID string) (int64, error)
}

type bankBookEntryReader interface {
	BankAccountEntriesAsOf(ctx context.Context, glAccountCode string, asOf time.Time) ([]gl.BankBookEntry, error)
}

func (s *Service) monthEndCut(ctx context.Context, year, month int) time.Time {
	zone, err := houseday.Resolve(ctx, s.zone)
	if err != nil {
		zone = houseday.DefaultZone()
	}
	return time.Date(year, time.Month(month)+1, 1, 0, 0, 0, 0, zone.Location()).Add(-time.Nanosecond)
}

// computeReconFigures is the ONE shared calculation for statement balance, GL closing balance at month end,
// deposits in transit, outstanding cheques, adjusted bank balance, and variance across OpenReconciliation,
// RefreshReconciliation, CompleteReconciliation, and Reconciliations (FW1-04).
func (s *Service) computeReconFigures(
	ctx context.Context,
	acct BankAccount,
	year, month int,
	statedBalanceCents int64,
	derived bool,
	stats monthLineStats,
	requireBalances bool,
) (reconFigures, error) {
	if requireBalances && s.balances == nil {
		return reconFigures{}, unprocessable("bankrec: cannot complete, the ledger balance cannot be read")
	}
	stmtBal := statedBalanceCents
	if derived {
		stmtBal = stats.LineSumCents
		if stats.PriorCompletedClosing != nil {
			stmtBal += *stats.PriorCompletedClosing
		}
	}
	var glBal int64
	var usesClosingBalance bool
	if s.balances != nil {
		if cr, ok := s.balances.(closingBalanceReader); ok {
			usesClosingBalance = true
			var err error
			if glBal, err = cr.GLClosingBalanceCents(ctx, acct.GLAccountCode, year, month, ""); err != nil {
				return reconFigures{}, err
			}
		} else {
			var err error
			if glBal, err = s.balances.GLBalanceCents(ctx, acct.GLAccountCode, year, month, ""); err != nil {
				return reconFigures{}, err
			}
		}
	}

	var dit []OutstandingItem
	var oc []OutstandingItem
	var ditCents, ocCents int64
	if usesClosingBalance && s.journals != nil {
		if br, ok := s.journals.(bankBookEntryReader); ok {
			entries, err := br.BankAccountEntriesAsOf(ctx, acct.GLAccountCode, s.monthEndCut(ctx, year, month))
			if err != nil {
				return reconFigures{}, err
			}
			zone, zerr := houseday.Resolve(ctx, s.zone)
			if zerr != nil {
				zone = houseday.DefaultZone()
			}
			confirmedCutover := map[string]bool{}
			for _, jid := range acct.CutoverOutstandingJournalIDs {
				confirmedCutover[jid] = true
			}
			for _, e := range entries {
				if stats.ClearedJournalIDs[e.JournalID] || (e.SourceRefID != "" && stats.ClearedJournalIDs[e.SourceRefID]) {
					continue
				}
				if acct.CutoverDate != nil && *acct.CutoverDate != "" {
					entryDay := e.PostedAt.In(zone.Location()).Format("2006-01-02")
					if entryDay <= *acct.CutoverDate && !confirmedCutover[e.JournalID] && !(e.SourceRefID != "" && confirmedCutover[e.SourceRefID]) {
						continue
					}
				}
				if e.NetCents > 0 {
					dit = append(dit, OutstandingItem{
						JournalID:   e.JournalID,
						PostedAt:    e.PostedAt,
						Memo:        e.Memo,
						AmountCents: e.NetCents,
					})
					ditCents += e.NetCents
				} else if e.NetCents < 0 {
					amt := -e.NetCents
					oc = append(oc, OutstandingItem{
						JournalID:   e.JournalID,
						PostedAt:    e.PostedAt,
						Memo:        e.Memo,
						AmountCents: amt,
					})
					ocCents += amt
				}
			}
		}
	}

	adjBank := stmtBal + ditCents - ocCents
	variance := adjBank - glBal
	return reconFigures{
		OpeningBalanceCents:      stats.PriorCompletedClosing,
		StatementBalanceCents:    stmtBal,
		StatementBalanceDerived:  derived,
		DepositsInTransitCents:   ditCents,
		OutstandingChequesCents:  ocCents,
		AdjustedBankBalanceCents: adjBank,
		GLBalanceCents:           glBal,
		VarianceCents:            variance,
		MatchedLinesCount:        stats.MatchedLines,
		DepositsInTransit:        dit,
		OutstandingCheques:       oc,
	}, nil
}

// recheckJournalMatches is CompleteReconciliation's recheck (SF-2j round 2 + FW1-04): every live JOURNAL match in
// the month is read again through the GL seam and must still explain its line, and every clearing journal posted on
// a PAYMENT/LODGEMENT match is checked so a clearing journal reversed in the ledger refuses the sign off.
func (s *Service) recheckJournalMatches(glCode string) func(ctx context.Context, matches []JournalMatchFact) error {
	return func(ctx context.Context, matches []JournalMatchFact) error {
		if len(matches) == 0 {
			return nil
		}
		for _, m := range matches {
			if m.TargetType == TargetJournal {
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
			if m.ClearingJournalID != "" && s.journals != nil {
				f, found, err := s.journals.JournalForBank(ctx, m.ClearingJournalID, glCode)
				if err != nil {
					return err
				}
				if !found || !f.Posted || f.Reversed {
					return unprocessable("bankrec: cannot complete, the clearing journal for %q has been reversed in the ledger", m.LineDescription)
				}
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
	peekType, peekRef, live, perr := s.repo.PeekMatchTarget(ctx, matchID)
	if perr != nil {
		return perr
	}
	if live && peekType == TargetLodgement {
		if ok, lerr := s.lodgementReachable(ctx, peekRef, reach); lerr != nil {
			return lerr
		} else if !ok {
			return conflict("bankrec: match %s is not live (already unmatched)", matchID)
		}
	}
	lineDate, hasLineDate, _ := s.repo.MatchLineDate(ctx, matchID)
	targetType, targetRef, amount, err := s.repo.UnmatchReach(ctx, matchID, userID, reach)
	if err != nil {
		return err
	}
	// A lodgement that carries its OWN journal posted no clearing journal when it was matched
	// (FROM-PRINCIPAL 117 and 120), so its undo reverses nothing either.
	carriesJournal := false
	if targetType == TargetLodgement {
		facts, found, ferr := s.lodgementFacts(ctx, targetRef)
		if ferr != nil {
			return ferr
		}
		if found {
			if carriesJournal, ferr = s.carriesOwnJournal(ctx, targetRef, facts.CarriesJournal); ferr != nil {
				return ferr
			}
		}
	}
	if s.poster != nil && amount > 0 {
		if rev, ok := reversalJournal(matchID, targetType, amount, carriesJournal); ok {
			if hasLineDate {
				rev.DocumentDate = statementDocDate(lineDate)
			}
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

// OpenReconciliation prepares (or refreshes when in.Refresh is true) the monthly reconciliation: it reads the GL
// closing balance through the GLBalanceReader seam (never the 0034 views directly), computes deposits in transit and
// outstanding cheques at month end, and computes the variance against the adjusted bank balance through the single
// shared computeReconFigures function (FW1-04). The derived flag comes from the act (an open with zero sets it TRUE,
// an open with a typed figure sets it FALSE, and a refresh keeps the stored flag and stated figure).
func (s *Service) OpenReconciliation(ctx context.Context, in ReconInput) (Reconciliation, error) {
	if in.Refresh {
		return s.RefreshReconciliation(ctx, in.BankAccountID, in.Year, in.Month)
	}
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
	derived := in.StatementBalanceCents == 0
	stats, err := s.repo.MonthLineStats(ctx, acct.ID, in.Year, in.Month)
	if err != nil {
		return Reconciliation{}, err
	}
	figs, err := s.computeReconFigures(ctx, acct, in.Year, in.Month, in.StatementBalanceCents, derived, stats, false)
	if err != nil {
		return Reconciliation{}, err
	}
	rec, err := s.repo.UpsertReconciliation(ctx, Reconciliation{
		BankAccountID:           acct.ID,
		Year:                    in.Year,
		Month:                   in.Month,
		StatementBalanceCents:   figs.StatementBalanceCents,
		StatementBalanceDerived: figs.StatementBalanceDerived,
		GLBalanceCents:          figs.GLBalanceCents,
		VarianceCents:           figs.VarianceCents,
	})
	if err != nil {
		return Reconciliation{}, err
	}
	rec.applyFigures(figs)
	return rec, nil
}

// RefreshReconciliation re-reads the ledger, outstanding items, and (when derived is true) the month's lines in place
// while keeping the stored derived flag and any stated closing balance, using the shared computeReconFigures function.
func (s *Service) RefreshReconciliation(ctx context.Context, bankAccountID string, year, month int) (Reconciliation, error) {
	if bankAccountID == "" {
		return Reconciliation{}, badRequest("bankrec: bank_account_id is required")
	}
	if month < 1 || month > 12 || year < 2000 {
		return Reconciliation{}, badRequest("bankrec: valid year and month (1-12) are required")
	}
	acct, err := s.repo.GetAccount(ctx, bankAccountID)
	if err != nil {
		return Reconciliation{}, err
	}
	existing, err := s.repo.GetOpenReconciliationByPeriod(ctx, acct.ID, year, month)
	if err != nil {
		return Reconciliation{}, err
	}
	stats, err := s.repo.MonthLineStats(ctx, acct.ID, year, month)
	if err != nil {
		return Reconciliation{}, err
	}
	figs, err := s.computeReconFigures(ctx, acct, year, month, existing.StatementBalanceCents, existing.StatementBalanceDerived, stats, false)
	if err != nil {
		return Reconciliation{}, err
	}
	rec, err := s.repo.UpdateOpenReconciliationFigures(ctx, acct.ID, year, month, figs.StatementBalanceCents, figs.GLBalanceCents, figs.VarianceCents)
	if err != nil {
		return Reconciliation{}, err
	}
	rec.applyFigures(figs)
	return rec, nil
}

// Reconciliations lists reconciliations (optionally scoped to an account) with their month-end report breakdown.
func (s *Service) Reconciliations(ctx context.Context, accountID string) ([]Reconciliation, error) {
	rows, err := s.repo.ListReconciliations(ctx, accountID)
	if err != nil {
		return nil, err
	}
	acctCache := map[string]BankAccount{}
	for i := range rows {
		r := &rows[i]
		acct, ok := acctCache[r.BankAccountID]
		if !ok {
			if a, aerr := s.repo.GetAccount(ctx, r.BankAccountID); aerr == nil {
				acct = a
				acctCache[r.BankAccountID] = a
			}
		}
		if acct.ID == "" {
			r.AdjustedBankBalanceCents = r.StatementBalanceCents
			continue
		}
		stats, serr := s.repo.MonthLineStats(ctx, r.BankAccountID, r.Year, r.Month)
		if serr != nil {
			r.AdjustedBankBalanceCents = r.StatementBalanceCents
			continue
		}
		figs, ferr := s.computeReconFigures(ctx, acct, r.Year, r.Month, r.StatementBalanceCents, r.StatementBalanceDerived && r.Status == ReconOpen, stats, false)
		if ferr != nil {
			r.OpeningBalanceCents = stats.PriorCompletedClosing
			r.MatchedLinesCount = stats.MatchedLines
			r.AdjustedBankBalanceCents = r.StatementBalanceCents
			continue
		}
		r.OpeningBalanceCents = figs.OpeningBalanceCents
		r.DepositsInTransitCents = figs.DepositsInTransitCents
		r.OutstandingChequesCents = figs.OutstandingChequesCents
		r.AdjustedBankBalanceCents = r.StatementBalanceCents + figs.DepositsInTransitCents - figs.OutstandingChequesCents
		r.MatchedLinesCount = figs.MatchedLinesCount
		r.DepositsInTransit = figs.DepositsInTransit
		r.OutstandingCheques = figs.OutstandingCheques
	}
	return rows, nil
}

// CompleteReconciliation signs off a monthly reconciliation. Guards (AC7 + FW1-04):
//   - the month must have reached month end in the dealer's house zone,
//   - a bank account must have at least one imported statement line in the month,
//   - the account must have ZERO UNMATCHED lines in the month,
//   - no earlier month on the account may still be OPEN, and no later month may already be COMPLETED,
//   - every live match's journal or clearing journal must still be POSTED and unreversed in the GL,
//   - the opening statement balance must equal the prior completed month's closing statement balance,
//   - the recomputed variance (adjusted bank balance minus GL closing balance) must be zero.
//
// On success it flips COMPLETED and emits bank.recon.completed in the same tx, returning the full
// reconciliation report.
func (s *Service) CompleteReconciliation(ctx context.Context, id, completedBy string) (Reconciliation, error) {
	if id == "" {
		return Reconciliation{}, badRequest("bankrec: reconciliation id is required")
	}
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
	zone, _ := houseday.Resolve(ctx, s.zone)
	today := zone.Today(s.clock)
	monthEndDay := fmt.Sprintf("%04d-%02d-%02d", rec.Year, rec.Month, daysInMonth(rec.Year, rec.Month))
	nextY, nextM := rec.Year, rec.Month+1
	if rec.Month == 12 {
		nextY, nextM = rec.Year+1, 1
	}
	firstSignOffDay := fmt.Sprintf("%04d-%02d-01", nextY, nextM)
	if today <= monthEndDay {
		return Reconciliation{}, unprocessable("bankrec: cannot complete, %d-%02d has not reached month end yet (month end is %s; first day to sign off is %s)", rec.Year, rec.Month, monthEndDay, firstSignOffDay)
	}
	unmatched, err := s.repo.CountUnmatchedInMonth(ctx, rec.BankAccountID, rec.Year, rec.Month)
	if err != nil {
		return Reconciliation{}, err
	}
	if unmatched > 0 {
		return Reconciliation{}, unprocessable("bankrec: cannot complete, %d UNMATCHED line(s) remain for %d-%02d", unmatched, rec.Year, rec.Month)
	}
	acct, err := s.repo.GetAccount(ctx, rec.BankAccountID)
	if err != nil {
		return Reconciliation{}, err
	}
	compute := func(ctx context.Context, year, month int, storedStmt int64, derived bool, stats monthLineStats) (reconFigures, error) {
		return s.computeReconFigures(ctx, acct, year, month, storedStmt, derived, stats, true)
	}
	out, err := s.repo.completeReconciliation(ctx, id, acct, completedBy, s.recheckJournalMatches(acct.GLAccountCode), compute)
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

func daysInMonth(year, month int) int {
	switch month {
	case 4, 6, 9, 11:
		return 30
	case 2:
		if (year%4 == 0 && year%100 != 0) || year%400 == 0 {
			return 29
		}
		return 28
	default:
		return 31
	}
}
