package billing

// ar_landing_test.go, the landing's arithmetic and its refusals.
//
// Everything asserted here is PURE: buckets, cards, the queue roll-up, the next-action ladder and
// the policy validator. They are the parts a dealer's configuration moves, so they are the parts
// that must be provable without a database, a clock or a browser.

import (
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// arDay is the AR-landing lane's date helper: noon in the house zone. Union train 13 renamed it
// off the plain `day`, which the invoices register lane declares in dib510_register_test.go at
// 09:30, two different times of day, both load-bearing for their own lane's aging arithmetic, so
// both are kept. Noon in the house zone is the AR lane's load-bearing choice since the aging day
// became the dealer's: an instant there carries its own calendar date whatever reads it, and as a
// clock it names the house zone's day, where this lane's old midnight (UTC) names the evening
// before and ages the book one day short.
func arDay(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 12, 0, 0, 0, houseday.Location())
}

func testPolicy() ARLandingPolicy { return DefaultARLandingPolicy() }

// TestBucketFor_ShippedEdges pins the client's own phrasing, "0 to 60 to 90, 90 plus", including
// the two boundaries people argue about: due today is not late, and 90 days IS escalation.
func TestBucketFor_ShippedEdges(t *testing.T) {
	p := testPolicy()
	cases := []struct {
		days int
		want string
	}{
		{-5, BucketCurrent},
		{0, BucketCurrent}, // due today is not late today
		{1, BucketWarn},
		{60, BucketWarn},
		{61, BucketLate},
		{89, BucketLate},
		{90, BucketEscalation}, // "90 days" has always meant "90 or worse"
		{400, BucketEscalation},
	}
	for _, c := range cases {
		if got := BucketFor(c.days, p); got != c.want {
			t.Errorf("BucketFor(%d) = %s, want %s", c.days, got, c.want)
		}
	}
}

// TestBucketFor_EdgesAreConfiguration is the whole point of the typed policy: a dealer who moves the
// escalation edge to 75 changes who is escalated, and nothing in the code has to be edited.
func TestBucketFor_EdgesAreConfiguration(t *testing.T) {
	p := testPolicy()
	p.AgingWarnDays, p.AgingEscalateDays = 30, 75
	if got := BucketFor(80, p); got != BucketEscalation {
		t.Fatalf("at a 75-day edge, 80 days is %s, the edge is not being read from policy", got)
	}
	if got := BucketFor(60, p); got != BucketLate {
		t.Fatalf("at a 30/75 policy, 60 days is %s, want LATE", got)
	}
	if got := BucketFor(30, p); got != BucketWarn {
		t.Fatalf("at a 30/75 policy, 30 days is %s, want WARN", got)
	}
}

func TestDaysPastDue_CountsWholeDays(t *testing.T) {
	// arDay is noon in the house zone: right as a due date (it carries its own calendar day) and
	// as the clock (it names the house zone's day). A due date off a DATE column arrives from the
	// scan at UTC midnight and must land on the same answer, which the second pair pins.
	if got := DaysPastDue(arDay(2026, 8, 1), arDay(2026, 8, 27)); got != 26 {
		t.Fatalf("DaysPastDue = %d, want 26", got)
	}
	if got := DaysPastDue(arDay(2026, 8, 27), arDay(2026, 8, 27)); got != 0 {
		t.Fatalf("an invoice due today is %d days past due, want 0", got)
	}
	scanned := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	if got := DaysPastDue(scanned, arDay(2026, 8, 27)); got != 0 {
		t.Fatalf("a due date scanned at UTC midnight, read on the house zone's day, is %d days past due, want 0", got)
	}
}

func sampleBook(asOf time.Time) []OpenInvoice {
	return []OpenInvoice{
		{InvoiceID: "i1", CustomerID: "c1", CustomerName: "Rood Landscaping", AccountNo: "A-1",
			BalanceCents: 250_00, DueDate: asOf.AddDate(0, 0, -120)},
		{InvoiceID: "i2", CustomerID: "c1", CustomerName: "Rood Landscaping", AccountNo: "A-1",
			BalanceCents: 750_00, DueDate: asOf.AddDate(0, 0, -95)},
		{InvoiceID: "i3", CustomerID: "c2", CustomerName: "Quinte Hardscapes", AccountNo: "A-2",
			BalanceCents: 400_00, DueDate: asOf.AddDate(0, 0, -70)},
		{InvoiceID: "i4", CustomerID: "c3", CustomerName: "Bayview Builders", AccountNo: "A-3",
			BalanceCents: 100_00, DueDate: asOf.AddDate(0, 0, -10)},
		{InvoiceID: "i5", CustomerID: "c3", CustomerName: "Bayview Builders", AccountNo: "A-3",
			BalanceCents: 900_00, DueDate: asOf.AddDate(0, 0, 15)},
		{InvoiceID: "i6", CustomerID: "c4", CustomerName: "Settled Co", AccountNo: "A-4",
			BalanceCents: 0, DueDate: asOf.AddDate(0, 0, -200)},
	}
}

// TestBuildCards_TotalsAndOrder: worst first, accounts distinct from invoices, and a zero-balance
// invoice never counted (it is settled, and a card that counts it is a card that lies).
func TestBuildCards_TotalsAndOrder(t *testing.T) {
	asOf := arDay(2026, 8, 27)
	cards := BuildCards(sampleBook(asOf), testPolicy(), asOf)
	if len(cards) != 4 {
		t.Fatalf("got %d cards, want 4", len(cards))
	}
	wantOrder := []string{CardEscalation, CardLate, CardWarn, CardCurrent}
	for i, w := range wantOrder {
		if cards[i].ID != w {
			t.Fatalf("card %d is %s, want %s, the landing opens at the sick end", i, cards[i].ID, w)
		}
	}
	esc := cards[0]
	if esc.Accounts != 1 || esc.Invoices != 2 || esc.TotalCents != 1000_00 {
		t.Fatalf("escalation card = %d accounts / %d invoices / %d cents, want 1 / 2 / 100000",
			esc.Accounts, esc.Invoices, esc.TotalCents)
	}
	if cards[1].Invoices != 1 || cards[1].TotalCents != 400_00 {
		t.Fatalf("late card = %d invoices / %d cents", cards[1].Invoices, cards[1].TotalCents)
	}
	if cards[3].TotalCents != 900_00 {
		t.Fatalf("current card = %d cents, want 90000", cards[3].TotalCents)
	}
	var total int64
	for _, c := range cards {
		total += c.TotalCents
	}
	if total != 2400_00 {
		t.Fatalf("cards total %d cents, want 240000, every open cent lands in exactly one bucket", total)
	}
}

// TestBuildCards_NetsACreditMemoAsExposure pins what the landing cards do with a credit memo (a
// negative open balance): it is netted into its bucket's total and counted as a document, and a
// credit-only account is still an account on its card. HH-195 changed the escalation QUEUE to skip
// credit memos; the cards are deliberately untouched, so a change here must be a decision.
func TestBuildCards_NetsACreditMemoAsExposure(t *testing.T) {
	asOf := arDay(2026, 8, 27)
	invs := []OpenInvoice{
		{InvoiceID: "i1", CustomerID: "c1", CustomerName: "One", DueDate: asOf.AddDate(0, 0, -5), BalanceCents: 1000_00},
		{InvoiceID: "m1", CustomerID: "c1", CustomerName: "One", DueDate: asOf.AddDate(0, 0, -5), BalanceCents: -300_00},
		{InvoiceID: "m2", CustomerID: "c2", CustomerName: "Two", DueDate: asOf.AddDate(0, 0, -5), BalanceCents: -50_00},
	}
	var late *LandingCard
	cards := BuildCards(invs, testPolicy(), asOf)
	for i := range cards {
		if cards[i].Invoices > 0 {
			late = &cards[i]
		}
	}
	if late == nil || late.Invoices != 3 || late.Accounts != 2 || late.TotalCents != 650_00 {
		t.Fatalf("card with the book = %+v, want 3 documents / 2 accounts / 65000 cents (the memos net into exposure)", late)
	}
}

// TestBuildCards_RangesAreReadable pins the from/to days each card carries, because the card's
// subtitle is derived from them and an off-by-one there is a number a customer will read back.
func TestBuildCards_RangesAreReadable(t *testing.T) {
	asOf := arDay(2026, 8, 27)
	cards := BuildCards(nil, testPolicy(), asOf)
	if cards[0].FromDays != 90 || cards[0].ToDays != 0 {
		t.Fatalf("escalation range = %d..%d, want 90..open", cards[0].FromDays, cards[0].ToDays)
	}
	if cards[1].FromDays != 61 || cards[1].ToDays != 89 {
		t.Fatalf("late range = %d..%d, want 61..89", cards[1].FromDays, cards[1].ToDays)
	}
	if cards[2].FromDays != 1 || cards[2].ToDays != 60 {
		t.Fatalf("warn range = %d..%d, want 1..60", cards[2].FromDays, cards[2].ToDays)
	}
}

// TestBuildEscalation_RollsUpPerAccount: eleven invoices on one account is ONE phone call.
func TestBuildEscalation_RollsUpPerAccount(t *testing.T) {
	asOf := arDay(2026, 8, 27)
	q := BuildEscalation(sampleBook(asOf), testPolicy(), asOf, map[string]bool{"c1": true})
	if len(q) != 1 {
		t.Fatalf("queue has %d entries, want 1 account", len(q))
	}
	e := q[0]
	if e.CustomerID != "c1" || e.Invoices != 2 || e.BalanceCents != 1000_00 {
		t.Fatalf("entry = %+v", e)
	}
	if e.OldestDays != 120 {
		t.Fatalf("oldest = %d days, want 120, the queue leads with the worst invoice on the account", e.OldestDays)
	}
	if !e.OnHold {
		t.Fatal("the queue must carry the hold, or somebody phones an account the counter has already stopped")
	}
}

// TestBuildEscalation_SkipsCreditMemos (HH-195): a credit memo (a negative open balance) is money owed
// TO the customer, never a call to make. A credit-only account is absent from the queue, and a memo
// never lends an account its age, its count or its balance. Unit level on purpose: the integration
// suite that also covers it is on the CI job's red list.
func TestBuildEscalation_SkipsCreditMemos(t *testing.T) {
	asOf := arDay(2026, 8, 27)
	invs := []OpenInvoice{
		{InvoiceID: "m1", CustomerID: "c2", CustomerName: "CreditOnly", DueDate: asOf.AddDate(0, 0, -150), BalanceCents: -50_00},
		{InvoiceID: "m2", CustomerID: "c3", CustomerName: "Mixed", DueDate: asOf.AddDate(0, 0, -200), BalanceCents: -30_00},
		{InvoiceID: "i3", CustomerID: "c3", CustomerName: "Mixed", DueDate: asOf.AddDate(0, 0, -120), BalanceCents: 80_00},
	}
	q := BuildEscalation(invs, testPolicy(), asOf, nil)
	if len(q) != 1 || q[0].CustomerID != "c3" {
		t.Fatalf("queue = %+v, want only the mixed account (a credit-only account is not a call to make)", q)
	}
	if q[0].Invoices != 1 || q[0].BalanceCents != 80_00 || q[0].OldestDays != 120 {
		t.Fatalf("mixed entry = %+v, want 1 invoice / 8000 cents / oldest 120 (the memo lends nothing)", q[0])
	}
}

func TestBuildEscalation_EmptyBookIsEmptyQueueNotNil(t *testing.T) {
	asOf := arDay(2026, 8, 27)
	if q := BuildEscalation(nil, testPolicy(), asOf, nil); len(q) != 0 {
		t.Fatalf("an empty book produced %d queue entries", len(q))
	}
}

// TestNextAction_Ladder pins the ORDER of the ladder, which is the judgement in the function.
func TestNextAction_Ladder(t *testing.T) {
	p := testPolicy()
	now := arDay(2026, 8, 27)
	old := now.AddDate(0, 0, -30)
	recent := now.AddDate(0, 0, -1)

	if got := NextAction(EngagementInput{APContactTagged: false}, p, now); got != "Tag AP contact" {
		t.Fatalf("untagged account → %q; unreachable beats unchased", got)
	}
	if got := NextAction(EngagementInput{APContactTagged: true, PaperPathFlagged: true, LastReminderAt: &recent}, p, now); got != "Move to paper" {
		t.Fatalf("flagged account → %q", got)
	}
	if got := NextAction(EngagementInput{APContactTagged: true}, p, now); got != "Send reminder" {
		t.Fatalf("never-reminded account → %q", got)
	}
	if got := NextAction(EngagementInput{APContactTagged: true, LastReminderAt: &old}, p, now); got != "Send reminder" {
		t.Fatalf("cadence elapsed → %q", got)
	}
	if got := NextAction(EngagementInput{APContactTagged: true, LastReminderAt: &recent}, p, now); got != "Call" {
		t.Fatalf("chased yesterday → %q; the honest last answer is a person", got)
	}
	if got := NextAction(EngagementInput{APContactTagged: true, LastReminderAt: &recent, DeliveredNeverOpened: 3}, p, now); got != "Move to paper" {
		t.Fatalf("three delivered and never opened → %q", got)
	}
}

// TestNextActionIsChrome holds the doctrine budget: this string renders in a work-list cell, so it
// is a verb phrase of at most three words and never a sentence.
func TestNextActionIsChrome(t *testing.T) {
	p := testPolicy()
	now := arDay(2026, 8, 27)
	recent := now.AddDate(0, 0, -1)
	for _, in := range []EngagementInput{
		{}, {APContactTagged: true}, {APContactTagged: true, LastReminderAt: &recent},
		{APContactTagged: true, PaperPathFlagged: true, LastReminderAt: &recent},
	} {
		got := NextAction(in, p, now)
		if got == "" {
			t.Fatal("every queue row needs a next action, a blank cell is a row nobody works")
		}
		if n := len(splitWords(got)); n > 3 {
			t.Fatalf("next action %q is %d words; chrome is 1-3", got, n)
		}
		if got[len(got)-1] == '.' {
			t.Fatalf("next action %q is a sentence", got)
		}
	}
}

func splitWords(s string) []string {
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == ' ' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// TestValidateARLandingPolicy_RefusesInWords: every refusal names the knob, and the crossed-edges
// case names BOTH numbers rather than telling somebody a legal value is out of range.
func TestValidateARLandingPolicy_RefusesInWords(t *testing.T) {
	base := testPolicy()

	if _, err := ValidateARLandingPolicy(base); err != nil {
		t.Fatalf("the shipped defaults must validate: %v", err)
	}

	bad := base
	bad.AgingWarnDays, bad.AgingEscalateDays = 95, 90
	_, err := ValidateARLandingPolicy(bad)
	if err == nil {
		t.Fatal("crossed edges must be refused")
	}
	if !arContains(err.Error(), "95") || !arContains(err.Error(), "90") {
		t.Fatalf("the refusal %q must name both numbers the operator typed", err)
	}

	bad = base
	bad.BlackHoleWindowDays = 2
	if _, err := ValidateARLandingPolicy(bad); err == nil || !arContains(err.Error(), "black_hole_window_days") {
		t.Fatalf("an out-of-range window must be refused by name, got %v", err)
	}

	bad = base
	bad.UntaggedFallback = "BLAST"
	if _, err := ValidateARLandingPolicy(bad); err == nil {
		t.Fatal("BLAST must not be spellable, it is the behaviour the column exists to prevent")
	}

	bad = base
	bad.Tier = "ENTERPRISE"
	if _, err := ValidateARLandingPolicy(bad); err == nil {
		t.Fatal("an unknown tier must be refused rather than silently granting or withholding")
	}
}

// TestValidateARLandingPolicy_NormalisesOmissions: a PUT that omits the two enum knobs gets the
// restrictive default, never a zero value that would fail a CHECK downstream.
func TestValidateARLandingPolicy_NormalisesOmissions(t *testing.T) {
	p := testPolicy()
	p.UntaggedFallback, p.Tier = "", ""
	clean, err := ValidateARLandingPolicy(p)
	if err != nil {
		t.Fatalf("omitted enums must normalise, got %v", err)
	}
	if clean.UntaggedFallback != FallbackConfirm || clean.Tier != TierBase {
		t.Fatalf("normalised to %s / %s, want CONFIRM / BASE", clean.UntaggedFallback, clean.Tier)
	}
}

// arContains is a local substring helper (the package already has a `contains` next door in
// credit_memo_repost_test.go; a second one with the same name would collide).
func arContains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestBuildCards_CarryTheAccountsTheyCounted, the fix for a defect the Chromium walkthrough found:
// the card said two accounts and the filtered register below it showed three, because the shipped
// register's fixed 30/60/90 columns cannot tell a 74-day account from a 91-day one. The card now
// carries the set it counted, and the register filters by identity.
func TestBuildCards_CarryTheAccountsTheyCounted(t *testing.T) {
	asOf := arDay(2026, 8, 27)
	cards := BuildCards(sampleBook(asOf), testPolicy(), asOf)
	esc, late := cards[0], cards[1]
	if len(esc.AccountIDs) != esc.Accounts {
		t.Fatalf("escalation card counts %d accounts but names %d", esc.Accounts, len(esc.AccountIDs))
	}
	if esc.AccountIDs[0] != "c1" {
		t.Fatalf("escalation ids = %v, want [c1]", esc.AccountIDs)
	}
	for _, id := range late.AccountIDs {
		if id == "c1" {
			t.Fatal("an account is counted in exactly one bucket per invoice set; c1 leaked into LATE")
		}
	}
	// Every id set is sorted, so two reads of the same book produce the same bytes and a diff of
	// two landings is readable.
	for _, c := range BuildCards(sampleBook(asOf), testPolicy(), asOf) {
		for i := 1; i < len(c.AccountIDs); i++ {
			if c.AccountIDs[i-1] > c.AccountIDs[i] {
				t.Fatalf("%s ids are unsorted: %v", c.ID, c.AccountIDs)
			}
		}
	}
	// And every account carrying an open balance is named by at least one card.
	named := map[string]bool{}
	for _, c := range BuildCards(sampleBook(asOf), testPolicy(), asOf) {
		for _, id := range c.AccountIDs {
			named[id] = true
		}
	}
	for _, want := range []string{"c1", "c2", "c3"} {
		if !named[want] {
			t.Fatalf("%s carries an open balance and no card names it", want)
		}
	}
	if named["c4"] {
		t.Fatal("a settled account must not be named by any card")
	}
}
