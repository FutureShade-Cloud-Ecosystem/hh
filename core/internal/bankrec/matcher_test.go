package bankrec

import (
	"testing"
	"time"
)

var refDate = time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)

func approvedRules() []MatchRule {
	return []MatchRule{
		{ID: "r-gw", Code: "gateway-ref-exact", Priority: 10, Status: RuleApproved, Kind: KindGatewayRefExact, Target: TargetPayment, ToleranceCents: 0},
		{ID: "r-lodge", Code: "lodgement-ref-exact", Priority: 20, Status: RuleApproved, Kind: KindLodgementRefExact, Target: TargetLodgement, ToleranceCents: 0},
		// The seeded rule's spec (migration 0442 bounds it): a fuzzy settlement's fee is only a fee within
		// the tolerance (FROM-PRINCIPAL 84).
		{ID: "r-run", Code: "run-settlement-fuzzy", Priority: 30, Status: RuleApproved, Kind: KindDescriptorFuzzy, Target: TargetPayment, DescriptorILike: "%RUN%", DateWindowDays: 3, AllowFeeSplit: true, ToleranceCents: 5000, FeeAccountCode: "6300"},
	}
}

// legAmounts sums the debit/credit for an account code across a journal's lines.
func legAmounts(p proposal, code string) (debit, credit int64) {
	for _, l := range p.Journal.Lines {
		if l.AccountCode == code {
			debit += l.DebitCents
			credit += l.CreditCents
		}
	}
	return
}

func assertBalanced(t *testing.T, p proposal) {
	t.Helper()
	var d, c int64
	for _, l := range p.Journal.Lines {
		d += l.DebitCents
		c += l.CreditCents
	}
	if d != c {
		t.Fatalf("journal not balanced: debits=%d credits=%d", d, c)
	}
}

// AC2, gateway_ref exact match posts DR 1000 / CR 1010 and targets the payment (→ MarkDeposited).
func TestMatch_GatewayRefExact(t *testing.T) {
	line := StatementLine{ID: "L1", Status: LineUnmatched, AmountCents: 100000, Reference: "GTW-1001", TxnDate: refDate}
	pmts := []CandidatePayment{{PaymentID: "P1", GatewayRef: "GTW-1001", AmountCents: 100000, BranchID: "B1", ReceivedAt: refDate}}

	prop, ok := matchLine(line, approvedRules(), pmts, nil)
	if !ok {
		t.Fatal("expected gateway-ref match")
	}
	if prop.Rule.ID != "r-gw" {
		t.Fatalf("wrong rule fired: %s", prop.Rule.ID)
	}
	if prop.TargetType != TargetPayment || prop.TargetRef != "P1" {
		t.Fatalf("wrong target: %s %s", prop.TargetType, prop.TargetRef)
	}
	if len(prop.PaymentIDs) != 1 || prop.PaymentIDs[0] != "P1" {
		t.Fatalf("expected P1 flagged for deposit, got %v", prop.PaymentIDs)
	}
	if prop.FeeCents != 0 {
		t.Fatalf("expected no fee, got %d", prop.FeeCents)
	}
	if d, _ := legAmounts(prop, AcctBank); d != 100000 {
		t.Fatalf("expected DR 1000 = 100000, got %d", d)
	}
	if _, c := legAmounts(prop, AcctCardClearing); c != 100000 {
		t.Fatalf("expected CR 1010 = 100000, got %d", c)
	}
	assertBalanced(t, prop)
}

// AC2, gateway rule (priority 10) wins over the fuzzy RUN rule (priority 30) when both could fire.
func TestMatch_PriorityOrder(t *testing.T) {
	line := StatementLine{ID: "L1", Status: LineUnmatched, AmountCents: 100000, Reference: "GTW-1001", Description: "RUN-PAY CARD SETTLEMENT", TxnDate: refDate}
	pmts := []CandidatePayment{{PaymentID: "P1", GatewayRef: "GTW-1001", AmountCents: 100000, ReceivedAt: refDate}}
	prop, ok := matchLine(line, approvedRules(), pmts, nil)
	if !ok || prop.Rule.ID != "r-gw" {
		t.Fatalf("expected gateway rule to win, got ok=%v rule=%s", ok, prop.Rule.ID)
	}
}

// AC3, lodgement matches on exact reference + exact amount, and posts DR 1000 / CR 1020. A one-cent drift
// must NOT auto-match (zero tolerance).
func TestMatch_LodgementZeroTolerance(t *testing.T) {
	line := StatementLine{ID: "L2", Status: LineUnmatched, AmountCents: 50000, Reference: "LODGE-77", TxnDate: refDate}
	lodg := []Lodgement{{MovementID: "M1", Reference: "LODGE-77", AmountCents: 50000, BranchID: "B1", OccurredAt: refDate}}

	prop, ok := matchLine(line, approvedRules(), nil, lodg)
	if !ok {
		t.Fatal("expected lodgement match")
	}
	if prop.TargetType != TargetLodgement || prop.TargetRef != "M1" {
		t.Fatalf("wrong target: %s %s", prop.TargetType, prop.TargetRef)
	}
	if d, _ := legAmounts(prop, AcctBank); d != 50000 {
		t.Fatalf("expected DR 1000 = 50000, got %d", d)
	}
	if _, c := legAmounts(prop, AcctUndeposited); c != 50000 {
		t.Fatalf("expected CR 1020 = 50000, got %d", c)
	}
	assertBalanced(t, prop)

	// one cent off → no match
	drift := []Lodgement{{MovementID: "M1", Reference: "LODGE-77", AmountCents: 50001, OccurredAt: refDate}}
	if _, ok := matchLine(line, approvedRules(), nil, drift); ok {
		t.Fatal("a one-cent drift must NOT auto-match a lodgement (zero tolerance)")
	}
}

// FW1-13 round 2 (FROM-PRINCIPAL 117 and 120): the bank match of a lodgement that carries its own
// journal posts NOTHING and only confirms the line, because the till posted DR 1000 / CR 1020 at
// lodgement and a second journal would book the deposit twice. An older lodgement (no lodgement
// journal) keeps today's match, which posts DR 1000 / CR 1020.
func TestMatch_LodgementThatCarriesItsJournalPostsNothing(t *testing.T) {
	line := StatementLine{ID: "L6", Status: LineUnmatched, AmountCents: 50000, Reference: "LODGE-88", TxnDate: refDate}

	carries := []Lodgement{{MovementID: "M2", Reference: "LODGE-88", AmountCents: 50000, BranchID: "B1", OccurredAt: refDate, CarriesJournal: true, PaymentIDs: []string{"P-SLIP"}, PaymentsUnresolved: true}}
	prop, ok := matchLine(line, approvedRules(), nil, carries)
	if !ok {
		t.Fatal("expected the lodgement line to match")
	}
	if prop.TargetType != TargetLodgement || prop.TargetRef != "M2" {
		t.Fatalf("wrong target: %s %s", prop.TargetType, prop.TargetRef)
	}
	if !prop.ConfirmsOnly {
		t.Fatal("a lodgement that carries its own journal must only confirm the line")
	}
	if len(prop.Journal.Lines) != 0 {
		t.Fatalf("a lodgement that carries its own journal must post no leg, got %v", prop.Journal.Lines)
	}
	if len(prop.PaymentIDs) != 0 {
		t.Fatalf("a lodgement that carries its own journal must claim no payment, got %v", prop.PaymentIDs)
	}
	if prop.PaymentsUnresolved {
		t.Fatal("a lodgement that carries its own journal must not carry PaymentsUnresolved")
	}

	// An older lodgement (no lodgement journal) keeps today's match: DR 1000 / CR 1020, and carries
	// the exact-set payment ids (or PaymentsUnresolved when unresolved).
	older := []Lodgement{{MovementID: "M3", Reference: "LODGE-88", AmountCents: 50000, BranchID: "B1", OccurredAt: refDate, PaymentIDs: []string{"P-OLD"}, PaymentsUnresolved: false}}
	prop2, ok := matchLine(line, approvedRules(), nil, older)
	if !ok {
		t.Fatal("expected the older lodgement line to match")
	}
	if prop2.ConfirmsOnly {
		t.Fatal("an older lodgement keeps today's match, which posts the clearing journal")
	}
	if len(prop2.PaymentIDs) != 1 || prop2.PaymentIDs[0] != "P-OLD" || prop2.PaymentsUnresolved {
		t.Fatalf("older lodgement proposal PaymentIDs=%v PaymentsUnresolved=%v; want [P-OLD] and false", prop2.PaymentIDs, prop2.PaymentsUnresolved)
	}
	if d, _ := legAmounts(prop2, AcctBank); d != 50000 {
		t.Fatalf("expected DR 1000 = 50000, got %d", d)
	}
	if _, c := legAmounts(prop2, AcctUndeposited); c != 50000 {
		t.Fatalf("expected CR 1020 = 50000, got %d", c)
	}
	assertBalanced(t, prop2)
}

// AC4, RUN settlement fee-split: bank net + fee to 6300 clears the gross card settlement. The journal key is
// line-derived so a re-run posts the SAME key (GL dedupes → idempotent, no second journal).
func TestMatch_RunSettlementFeeSplit(t *testing.T) {
	line := StatementLine{ID: "L3", Status: LineUnmatched, AmountCents: 98000, Description: "RUN BATCH SETTLEMENT", TxnDate: refDate}
	pmts := []CandidatePayment{{PaymentID: "P9", AmountCents: 100000, ReceivedAt: refDate}}

	prop, ok := matchLine(line, approvedRules(), pmts, nil)
	if !ok {
		t.Fatal("expected fuzzy RUN match")
	}
	if prop.Rule.ID != "r-run" {
		t.Fatalf("wrong rule: %s", prop.Rule.ID)
	}
	if prop.FeeCents != 2000 {
		t.Fatalf("expected fee 2000, got %d", prop.FeeCents)
	}
	if d, _ := legAmounts(prop, AcctBank); d != 98000 {
		t.Fatalf("expected DR 1000 net = 98000, got %d", d)
	}
	if d, _ := legAmounts(prop, "6300"); d != 2000 {
		t.Fatalf("expected DR 6300 fee = 2000, got %d", d)
	}
	if _, c := legAmounts(prop, AcctCardClearing); c != 100000 {
		t.Fatalf("expected CR 1010 gross = 100000, got %d", c)
	}
	assertBalanced(t, prop)

	if prop.Journal.IdempotencyKey != "bankrec:line:L3" {
		t.Fatalf("journal key not line-derived: %s", prop.Journal.IdempotencyKey)
	}
	// Re-running over the same line yields the identical idempotency key (idempotent post).
	again, _ := matchLine(line, approvedRules(), pmts, nil)
	if again.Journal.IdempotencyKey != prop.Journal.IdempotencyKey {
		t.Fatal("re-match produced a different journal key (not idempotent)")
	}
}

// A RUN line outside the date window does not match; a debit line never auto-matches; a shortfall beyond
// the rule's tolerance never books as a fee (FROM-PRINCIPAL 84).
func TestMatch_WindowAndSign(t *testing.T) {
	line := StatementLine{ID: "L4", Status: LineUnmatched, AmountCents: 98000, Description: "RUN BATCH", TxnDate: refDate}
	far := []CandidatePayment{{PaymentID: "P", AmountCents: 100000, ReceivedAt: refDate.AddDate(0, 0, 10)}}
	if _, ok := matchLine(line, approvedRules(), far, nil); ok {
		t.Fatal("payment outside the ±3-day window must not match")
	}
	debit := StatementLine{ID: "L5", Status: LineUnmatched, AmountCents: -4500, Description: "SERVICE FEE", TxnDate: refDate}
	if _, ok := matchLine(debit, approvedRules(), nil, nil); ok {
		t.Fatal("a debit line must not auto-match")
	}

	shortLine := StatementLine{ID: "L6", Status: LineUnmatched, AmountCents: 489000, Description: "RUN BATCH SETTLEMENT", TxnDate: refDate}
	short := []CandidatePayment{{PaymentID: "P10", AmountCents: 600000, ReceivedAt: refDate}}
	if _, ok := matchLine(shortLine, approvedRules(), short, nil); ok {
		t.Fatal("a $1,110.00 shortfall on a $6,000.00 payment must not auto match as a fee (tolerance 5000)")
	}
}

func TestDescriptorMatches(t *testing.T) {
	cases := []struct {
		desc, pat string
		want      bool
	}{
		{"RUN BATCH SETTLEMENT", "%RUN%", true},
		{"run-pay deposit", "%RUN%", true},
		{"CASH DEPOSIT", "%RUN%", false},
		{"RUNNER", "RUN%", true},
		{"PRERUN", "%RUN", true},
	}
	for _, c := range cases {
		if got := descriptorMatches(c.desc, c.pat); got != c.want {
			t.Errorf("descriptorMatches(%q,%q)=%v want %v", c.desc, c.pat, got, c.want)
		}
	}
}

// W2-1d item 5: every clearing journal a match or an unmatch posts carries a
// memo in a bookkeeper's words, "Deposit, <bank line description>", never an
// engine word and never the line's reference or a key.
func TestMatch_MemosReadLikeABookkeeperWroteThem(t *testing.T) {
	line := StatementLine{ID: "L9", Status: LineUnmatched, AmountCents: 50000, Reference: "pay-s1-010531", Description: "Customer EFT receipt", TxnDate: refDate}
	pmts := []CandidatePayment{{PaymentID: "P9", GatewayRef: "pay-s1-010531", AmountCents: 50000, BranchID: "B1", ReceivedAt: refDate}}

	prop, ok := matchLine(line, approvedRules(), pmts, nil)
	if !ok {
		t.Fatal("expected the gateway-ref match to fire")
	}
	if prop.Journal.Memo != "Deposit, Customer EFT receipt" {
		t.Fatalf("card settlement memo %q, want the deposit and the bank line's own description", prop.Journal.Memo)
	}

	lodg := []Lodgement{{MovementID: "M1", Reference: "DEP-1", AmountCents: 25000, BranchID: "B1"}}
	line2 := StatementLine{ID: "L10", Status: LineUnmatched, AmountCents: 25000, Reference: "DEP-1", Description: "Daily branch deposit", TxnDate: refDate}
	prop2, ok := matchLine(line2, approvedRules(), nil, lodg)
	if !ok {
		t.Fatal("expected the lodgement match to fire")
	}
	if prop2.Journal.Memo != "Deposit, Daily branch deposit" {
		t.Fatalf("lodgement memo %q, want the deposit and the bank line's own description", prop2.Journal.Memo)
	}

	rev, ok := reversalJournal("M1", TargetLodgement, "B1", "", 25000, 0, "", false)
	if !ok {
		t.Fatal("expected the reversal journal to build")
	}
	if rev.Memo != "Reversed deposit match" {
		t.Fatalf("unmatch reversal memo %q, want bookkeeper words", rev.Memo)
	}

	manual, ok := manualJournal(line, TargetPayment, "CHEQUE", "B1", false)
	if !ok {
		t.Fatal("expected the manual deposit journal to build")
	}
	if manual.Memo != "Deposit, Customer EFT receipt" {
		t.Fatalf("manual deposit memo %q, want the deposit and the bank line's own description", manual.Memo)
	}
}

// W3 P3-3: a bank line with no description posts "Deposit", never a trailing
// bare comma ("Deposit, ").
func TestMatch_EmptyDescriptionMemoHasNoBareComma(t *testing.T) {
	for _, blank := range []string{"", "   "} {
		line := StatementLine{ID: "L-BLANK-1", Status: LineUnmatched, AmountCents: 50000, Reference: "pay-s1-010531", Description: blank, TxnDate: refDate}
		pmts := []CandidatePayment{{PaymentID: "P9", GatewayRef: "pay-s1-010531", AmountCents: 50000, BranchID: "B1", ReceivedAt: refDate}}
		prop, ok := matchLine(line, approvedRules(), pmts, nil)
		if !ok {
			t.Fatal("expected the gateway-ref match to fire")
		}
		if prop.Journal.Memo != "Deposit" {
			t.Errorf("card settlement memo with description %q = %q, want %q", blank, prop.Journal.Memo, "Deposit")
		}

		lodg := []Lodgement{{MovementID: "M1", Reference: "DEP-1", AmountCents: 25000, BranchID: "B1"}}
		line2 := StatementLine{ID: "L-BLANK-2", Status: LineUnmatched, AmountCents: 25000, Reference: "DEP-1", Description: blank, TxnDate: refDate}
		prop2, ok := matchLine(line2, approvedRules(), nil, lodg)
		if !ok {
			t.Fatal("expected the lodgement match to fire")
		}
		if prop2.Journal.Memo != "Deposit" {
			t.Errorf("lodgement memo with description %q = %q, want %q", blank, prop2.Journal.Memo, "Deposit")
		}

		manualPmt, ok := manualJournal(line, TargetPayment, "CHEQUE", "B1", false)
		if !ok {
			t.Fatal("expected the manual payment deposit journal to build")
		}
		if manualPmt.Memo != "Deposit" {
			t.Errorf("manual payment memo with description %q = %q, want %q", blank, manualPmt.Memo, "Deposit")
		}

		manualLodg, ok := manualJournal(line2, TargetLodgement, "", "B1", false)
		if !ok {
			t.Fatal("expected the manual lodgement deposit journal to build")
		}
		if manualLodg.Memo != "Deposit" {
			t.Errorf("manual lodgement memo with description %q = %q, want %q", blank, manualLodg.Memo, "Deposit")
		}
	}
}
