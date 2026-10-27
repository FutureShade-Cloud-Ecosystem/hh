package bankrec

import (
	"sort"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
)

// proposal is one auto-match the engine wants the service to persist: the statement line, the domain target
// it paired to, the rule that fired, the clearing journal to post, and (for card settlements) the payment
// ids to flip to DEPOSITED. It is produced by pure logic (no DB) so the matcher is unit-testable (AC2-4).
type proposal struct {
	Line       StatementLine
	TargetType string
	TargetRef  string
	Rule       MatchRule
	PaymentIDs []string
	FeeCents   int64
	Journal    gl.ExternalJournal
}

// matchLine runs the APPROVED rules (priority order) against one UNMATCHED credit line and returns the first
// proposal that fires. Only positive (credit) lines auto-match to payments/lodgements; debits (Visa spend,
// bank fees) are left for manual handling. Pure: callers inject the candidate slices.
func matchLine(line StatementLine, rules []MatchRule, pmts []CandidatePayment, lodg []Lodgement) (proposal, bool) {
	if line.Status != LineUnmatched {
		return proposal{}, false
	}
	if line.AmountCents <= 0 {
		return proposal{}, false // only deposits/settlements (credits) auto-match at W5
	}
	ordered := append([]MatchRule(nil), rules...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Priority < ordered[j].Priority })
	for _, rule := range ordered {
		if rule.Status != RuleApproved {
			continue
		}
		switch rule.Kind {
		case KindGatewayRefExact:
			if p, ok := matchGatewayExact(line, rule, pmts); ok {
				// The tolerance's remainder is a processor fee: it books to the fee account so the journal
				// balances (FW1-05 item 6); matchGatewayExact guarantees it is never negative.
				return buildPaymentProposal(line, rule, p, p.AmountCents-line.AmountCents), true
			}
		case KindLodgementRefExact:
			if l, ok := matchLodgementExact(line, rule, lodg); ok {
				return buildLodgementProposal(line, rule, l), true
			}
		case KindDescriptorFuzzy:
			if p, fee, ok := matchDescriptorFuzzy(line, rule, pmts); ok {
				return buildPaymentProposal(line, rule, p, fee), true
			}
		}
	}
	return proposal{}, false
}

// matchGatewayExact pairs a card-settlement line to the payment whose gateway_ref equals the line reference,
// with the payment's gross at or above the line and the remainder inside tolerance (0 cents for the seeded
// rule). A deposit larger than the payment is not a settlement of it: a negative remainder cannot book as a
// fee, so it refuses (FW1-05 item 6).
func matchGatewayExact(line StatementLine, rule MatchRule, pmts []CandidatePayment) (CandidatePayment, bool) {
	ref := normalizeRef(line.Reference)
	if ref == "" {
		return CandidatePayment{}, false
	}
	for _, p := range pmts {
		if normalizeRef(p.GatewayRef) != ref {
			continue
		}
		if p.AmountCents >= line.AmountCents && p.AmountCents-line.AmountCents <= rule.ToleranceCents {
			return p, true
		}
	}
	return CandidatePayment{}, false
}

// matchLodgementExact pairs a deposit line to an open lodgement by exact reference + exact amount (zero
// tolerance, AC3: a cent of drift must NOT auto-match a cash/cheque deposit).
func matchLodgementExact(line StatementLine, rule MatchRule, lodg []Lodgement) (Lodgement, bool) {
	ref := normalizeRef(line.Reference)
	for _, l := range lodg {
		if ref != "" && normalizeRef(l.Reference) != ref {
			continue
		}
		if ref == "" {
			continue
		}
		if abs64(l.AmountCents-line.AmountCents) <= rule.ToleranceCents {
			return l, true
		}
	}
	return Lodgement{}, false
}

// matchDescriptorFuzzy pairs a RUN-batch settlement line to a payment by descriptor ILIKE + a ± date window,
// allowing the bank net = payment gross − processor fee. Returns the fee (gross − net) to split to 6300. The
// payment chosen is the one within the window whose gross ≥ net and gives the smallest non-negative fee
// (deterministic), so a re-run picks the same payment (idempotent, AC4).
func matchDescriptorFuzzy(line StatementLine, rule MatchRule, pmts []CandidatePayment) (CandidatePayment, int64, bool) {
	if !descriptorMatches(line.Description, rule.DescriptorILike) {
		return CandidatePayment{}, 0, false
	}
	var best CandidatePayment
	var bestFee int64 = -1
	for _, p := range pmts {
		if !withinDays(line.TxnDate, p.ReceivedAt, rule.DateWindowDays) {
			continue
		}
		fee := p.AmountCents - line.AmountCents // gross − net
		if fee < 0 {
			continue // the deposit exceeds the payment; not a fee split
		}
		if !rule.AllowFeeSplit && fee != 0 {
			continue // this rule does not permit a fee split
		}
		if bestFee == -1 || fee < bestFee {
			best = p
			bestFee = fee
		}
	}
	if bestFee < 0 {
		return CandidatePayment{}, 0, false
	}
	return best, bestFee, true
}

// buildPaymentProposal builds a card-settlement clearing journal: DR 1000 (net to bank) [+ DR 6300 (fee)]
// / CR 1010 (gross out of card clearing). With no fee this is the plain DR 1000 / CR 1010 (AC2).
func buildPaymentProposal(line StatementLine, rule MatchRule, p CandidatePayment, feeCents int64) proposal {
	net := line.AmountCents
	gross := p.AmountCents
	feeAcct := rule.FeeAccountCode
	if feeAcct == "" {
		feeAcct = "6300"
	}
	lines := []gl.ExternalLine{
		{AccountCode: AcctBank, DebitCents: net, Memo: "bank deposit (card settlement)"},
	}
	if feeCents > 0 {
		lines = append(lines, gl.ExternalLine{AccountCode: feeAcct, DebitCents: feeCents, Memo: "card processing fee"})
	}
	lines = append(lines, gl.ExternalLine{AccountCode: AcctCardClearing, CreditCents: gross, Memo: "clear card settlement"})
	return proposal{
		Line:       line,
		TargetType: TargetPayment,
		TargetRef:  p.PaymentID,
		Rule:       rule,
		PaymentIDs: []string{p.PaymentID},
		FeeCents:   feeCents,
		Journal: gl.ExternalJournal{
			Source:         "ADJUSTMENT",
			SourceRefID:    line.ID,
			IdempotencyKey: journalKey(line.ID),
			Memo:           "bankrec card settlement " + line.Reference,
			BranchID:       p.BranchID,
			DocumentDate:   statementDocDate(line.TxnDate),
			Lines:          lines,
		},
	}
}

// buildLodgementProposal builds a cash/cheque deposit clearing journal: DR 1000 (into bank) / CR 1020 (out
// of undeposited funds) (AC3).
func buildLodgementProposal(line StatementLine, rule MatchRule, l Lodgement) proposal {
	return proposal{
		Line:       line,
		TargetType: TargetLodgement,
		TargetRef:  l.MovementID,
		Rule:       rule,
		Journal: gl.ExternalJournal{
			Source:         "ADJUSTMENT",
			SourceRefID:    line.ID,
			IdempotencyKey: journalKey(line.ID),
			Memo:           "bankrec lodgement deposit " + line.Reference,
			BranchID:       l.BranchID,
			DocumentDate:   statementDocDate(line.TxnDate),
			Lines: []gl.ExternalLine{
				{AccountCode: AcctBank, DebitCents: line.AmountCents, Memo: "bank deposit (lodgement)"},
				{AccountCode: AcctUndeposited, CreditCents: line.AmountCents, Memo: "clear undeposited funds"},
			},
		},
	}
}

// manualJournal builds the clearing journal for a MANUAL pairing by target type and, for a payment, by its
// tender method (FW1-05 item 1): a card settlement clears 1010 (card processor clearing), a cash or cheque
// deposit clears 1020 (undeposited funds); any other tender refuses. A GROUP or JOURNAL target posts no new
// journal (the journal already exists / is a manual grouping header).
func manualJournal(line StatementLine, targetType, method, branchID string) (gl.ExternalJournal, bool) {
	switch targetType {
	case TargetPayment:
		clearing, ok := clearingAccountForMethod(method)
		if !ok {
			return gl.ExternalJournal{}, false
		}
		depositMemo := "bank deposit (card settlement)"
		clearMemo := "clear card settlement"
		if clearing == AcctUndeposited {
			depositMemo = "bank deposit (cash or cheque)"
			clearMemo = "clear undeposited funds"
		}
		return gl.ExternalJournal{
			Source: "ADJUSTMENT", SourceRefID: line.ID, IdempotencyKey: journalKey(line.ID),
			Memo: "bankrec manual deposit " + line.Reference, BranchID: branchID,
			DocumentDate: statementDocDate(line.TxnDate),
			Lines: []gl.ExternalLine{
				{AccountCode: AcctBank, DebitCents: line.AmountCents, Memo: depositMemo},
				{AccountCode: clearing, CreditCents: line.AmountCents, Memo: clearMemo},
			},
		}, true
	case TargetLodgement:
		return gl.ExternalJournal{
			Source: "ADJUSTMENT", SourceRefID: line.ID, IdempotencyKey: journalKey(line.ID),
			Memo: "bankrec manual lodgement deposit " + line.Reference, BranchID: branchID,
			DocumentDate: statementDocDate(line.TxnDate),
			Lines: []gl.ExternalLine{
				{AccountCode: AcctBank, DebitCents: line.AmountCents, Memo: "bank deposit (lodgement)"},
				{AccountCode: AcctUndeposited, CreditCents: line.AmountCents, Memo: "clear undeposited funds"},
			},
		}, true
	default:
		return gl.ExternalJournal{}, false
	}
}

// clearingAccountForMethod names the clearing account a deposit of this tender clears through (FW1-05
// item 1): a card settlement clears through 1010, cash and a cheque through 1020. Every other tender
// answers false: a hand match of it is refused in words.
func clearingAccountForMethod(method string) (string, bool) {
	switch method {
	case "CARD":
		return AcctCardClearing, true
	case "CASH", "CHEQUE":
		return AcctUndeposited, true
	}
	return "", false
}

// statementDocDate converts a statement line's calendar date (stored as UTC midnight in bank_statement_lines)
// to 12:00 UTC on that calendar day so the journal lands on the same calendar day in every Canadian time zone
// (FW1-04).
func statementDocDate(txnDate time.Time) time.Time {
	if txnDate.IsZero() {
		return time.Time{}
	}
	return txnDate.UTC().Truncate(24 * time.Hour).Add(12 * time.Hour)
}

// reversalJournal builds the offsetting clearing journal an unmatch posts: it mirrors the ORIGINAL clearing
// journal's legs, swapped, so the net GL effect of the match returns to zero (FW1-05 item 3). A payment
// settlement's original was DR 1000 net [+ DR fee fee] / CR 1010 gross, so the reversal is DR 1010 gross /
// CR 1000 net [+ CR fee fee]; a lodgement's original was DR 1000 / CR 1020, so its reversal is DR 1020 /
// CR 1000. It carries the target's branch, so every line of the reversal is stamped like the original's
// (FW1-05 item 4). Keyed on unmatchKey(matchID) (distinct from the clearing key). GROUP/JOURNAL targets
// posted no clearing journal, so they have nothing to reverse.
func reversalJournal(matchID, targetType, branchID string, amount, feeCents int64, feeAccount string) (gl.ExternalJournal, bool) {
	var lines []gl.ExternalLine
	switch targetType {
	case TargetPayment:
		gross := amount + feeCents
		lines = []gl.ExternalLine{
			{AccountCode: AcctCardClearing, DebitCents: gross, Memo: "reverse card settlement (unmatch)"},
			{AccountCode: AcctBank, CreditCents: amount, Memo: "reverse bank deposit (unmatch)"},
		}
		if feeCents > 0 {
			if feeAccount == "" {
				feeAccount = "6300"
			}
			lines = append(lines, gl.ExternalLine{AccountCode: feeAccount, CreditCents: feeCents, Memo: "reverse card processing fee (unmatch)"})
		}
	case TargetLodgement: // original DR1000/CR1020 → reverse DR1020/CR1000
		lines = []gl.ExternalLine{
			{AccountCode: AcctUndeposited, DebitCents: amount, Memo: "reverse undeposited clear (unmatch)"},
			{AccountCode: AcctBank, CreditCents: amount, Memo: "reverse bank deposit (unmatch)"},
		}
	default:
		return gl.ExternalJournal{}, false
	}
	return gl.ExternalJournal{
		Source:         "ADJUSTMENT",
		IdempotencyKey: unmatchKey(matchID),
		Memo:           "bankrec unmatch reversal",
		BranchID:       branchID,
		Lines:          lines,
	}, true
}

// journalKey ties a PROPOSAL's clearing journal to its statement line so the pure matcher is deterministic and
// unit-testable (a re-run of matchLine over the same line proposes the same key). The SERVICE overrides this
// with clearingKey(matchID) before it actually posts, so every persisted match, including a corrective
// RE-MATCH of the same line after an unmatch, gets its OWN clearing journal (fresh legs), and an unmatch can
// reverse a match by its own id without the stale line key colliding.
func journalKey(lineID string) string { return "bankrec:line:" + lineID }

// clearingKey keys the posted clearing journal to the MATCH (not the line) so a re-match posts fresh legs and
// an unmatch reversal targets exactly this match. unmatchKey keys the reversing journal (a distinct key so it
// is never deduped against the clearing post).
func clearingKey(matchID string) string { return "bankrec:match:" + matchID }
func unmatchKey(matchID string) string  { return "bankrec:unmatch:" + matchID }

func normalizeRef(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

// descriptorMatches implements the subset of SQL ILIKE the rule specs use (leading/trailing % wildcards).
func descriptorMatches(desc, pattern string) bool {
	if pattern == "" {
		return false
	}
	d := strings.ToLower(desc)
	p := strings.ToLower(pattern)
	hasPre := strings.HasPrefix(p, "%")
	hasSuf := strings.HasSuffix(p, "%")
	core := strings.Trim(p, "%")
	switch {
	case hasPre && hasSuf:
		return strings.Contains(d, core)
	case hasSuf:
		return strings.HasPrefix(d, core)
	case hasPre:
		return strings.HasSuffix(d, core)
	default:
		return d == core
	}
}

// withinDays reports whether two instants are within ±days of each other (compared on the calendar-day
// distance so a same-day settlement always matches regardless of the intraday time component).
func withinDays(a, b time.Time, days int) bool {
	da := a.UTC().Truncate(24 * time.Hour)
	db := b.UTC().Truncate(24 * time.Hour)
	diff := da.Sub(db)
	if diff < 0 {
		diff = -diff
	}
	return diff <= time.Duration(days)*24*time.Hour
}
