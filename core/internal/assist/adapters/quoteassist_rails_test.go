package adapters

// quoteassist_rails_test.go, D-075's rails, asserted on the SEQUENCE rather than on the outcome.
//
// WHY THE SEQUENCE IS THE TEST. The budget rule, the boundary and the month window are all assist's
// own functions and are already pinned by assist's own tests (TestBudget_Boundary, TestResolve_*,
// TestMonthWindow_*). What THIS file contributes is the only thing quoteextract.go actually decides:
// the order the rails are applied in. And the order is the whole governance claim, so it is asserted
// by observation of the fakes rather than by reading the code:
//
//	OVER BUDGET   → the provider is never called      (spendable.calls == 0)
//	              → no ledger row is written          (ledger.appended is empty)
//	IN BUDGET     → the provider is called once
//	              → EXACTLY ONE row lands, with the provider's own token counts, the caller's role
//	                code, and kind 'quote_extract'
//	PROVIDER ERROR → the row STILL lands. Something was sent; something was spent.
//
// Mutation M6 in NOTES-FIX-6667.md removes the OverBudget gate; the first test below fails by name.
// Mutation M7 removes the ledger Append; the second fails by name.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quoteassist"
)

// fixedNow keeps the month window stable so a test run at 23:59 on the last of the month behaves
// like one at noon on the first.
var fixedNow = time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

// -- doubles ------------------------------------------------------------------------------------

// spyProvider is an assist.Provider that records whether it was reached at all, which is the whole
// point of a budget: the money is not spent, not spent-and-then-reported.
type spyProvider struct {
	reply assist.Reply
	err   error
	calls int
	got   assist.CompletionRequest
}

func (s *spyProvider) Name() string  { return "spy" }
func (s *spyProvider) Model() string { return "spy-1" }
func (s *spyProvider) Complete(_ context.Context, req assist.CompletionRequest) (assist.Reply, error) {
	s.calls++
	s.got = req
	return s.reply, s.err
}

// spyLedger records every appended row and answers a fixed spend.
type spyLedger struct {
	spend          assist.Spend
	spendErr       error
	appended       []assist.Entry
	appendErr      error
	spendCalls     int
	gotFrom, gotTo time.Time
	gotUser        string
}

func (s *spyLedger) Append(_ context.Context, e assist.Entry) error {
	s.appended = append(s.appended, e)
	return s.appendErr
}

func (s *spyLedger) SpendFor(_ context.Context, userID string, from, to time.Time) (assist.Spend, error) {
	s.spendCalls++
	s.gotUser, s.gotFrom, s.gotTo = userID, from, to
	return s.spend, s.spendErr
}

func (s *spyLedger) SpendAll(context.Context, time.Time, time.Time) (map[string]assist.Spend, error) {
	return nil, nil
}

// spyLimits is an assist.LimitStore over two optional numbers, so the whole-or-absent rule is
// exercised through assist.BudgetFor rather than reimplemented here.
type spyLimits struct {
	user, role *int
	err        error
	gotUser    string
	gotRole    string
}

func (s *spyLimits) RoleBudget(_ context.Context, roleCode string) (*int, error) {
	s.gotRole = roleCode
	return s.role, s.err
}
func (s *spyLimits) UserBudget(_ context.Context, userID string) (*int, error) {
	s.gotUser = userID
	return s.user, s.err
}
func (s *spyLimits) RoleBudgets(context.Context) (map[string]int, error)      { return nil, nil }
func (s *spyLimits) UserBudgets(context.Context) (map[string]int, error)      { return nil, nil }
func (s *spyLimits) SetRoleBudget(context.Context, string, int, string) error { return nil }
func (s *spyLimits) SetUserBudget(context.Context, string, int, string) error { return nil }
func (s *spyLimits) ClearUserBudget(context.Context, string) error            { return nil }

func intp(n int) *int { return &n }

func rep() assist.Caller {
	return assist.Caller{UserID: "user-rep", RoleCode: "SALES_REP", Tier: 3, BranchIDs: []string{"b-trenton"}}
}

func railsWith(p assist.Provider, l assist.LedgerStore, lim assist.LimitStore) *QuoteExtractRails {
	return NewQuoteExtractRails(p, l, lim, nil).WithClock(func() time.Time { return fixedNow })
}

// -- 1 · the budget gate ------------------------------------------------------------------------

// TestQuoteExtractRails_OverBudgetNeverReachesTheProvider is the load-bearing governance test.
//
// The role's budget is 1,000 tokens and the ledger already holds 1,000 spent. assist.OverBudget's
// boundary is `spent >= budget`, so this caller is done, and "done" has to mean nothing is sent,
// because a budget that spends the money and then reports it is not a budget.
func TestQuoteExtractRails_OverBudgetNeverReachesTheProvider(t *testing.T) {
	provider := &spyProvider{reply: assist.Reply{Text: `[{"text":"480 sq ft Blu 60"}]`, TokensIn: 400, TokensOut: 40}}
	ledger := &spyLedger{spend: assist.Spend{Exchanges: 3, TokensIn: 900, TokensOut: 100}}
	limits := &spyLimits{role: intp(1000)}
	rails := railsWith(provider, ledger, limits)

	_, err := rails.SegmentText(context.Background(), rep(), "480 sq ft Blu 60")
	if !errors.Is(err, assist.ErrOverBudget) {
		t.Fatalf("err = %v, want assist.ErrOverBudget", err)
	}
	if provider.calls != 0 {
		t.Fatalf("the provider was called %d times over budget, the money was spent and then reported", provider.calls)
	}
	if len(ledger.appended) != 0 {
		t.Fatalf("a ledger row was written for a refusal: %+v, an over-budget refusal is not a transaction", ledger.appended)
	}
	// The refusal was reached through the assist rails, not around them: the budget was resolved for
	// THIS caller and the spend was read for THIS user, over the calendar month.
	if limits.gotUser != "user-rep" || limits.gotRole != "SALES_REP" {
		t.Errorf("budget resolved for %q/%q, want the caller's own", limits.gotUser, limits.gotRole)
	}
	wantFrom, wantTo := assist.MonthWindow(fixedNow)
	if !ledger.gotFrom.Equal(wantFrom) || !ledger.gotTo.Equal(wantTo) {
		t.Errorf("spend window = [%s, %s), want assist.MonthWindow's [%s, %s)",
			ledger.gotFrom, ledger.gotTo, wantFrom, wantTo)
	}
}

// TestQuoteExtractRails_AtExactlyTheBoundaryIsRefused, `spent >= budget`, which is assist's own
// operator. Called rather than copied, so flipping it breaks both surfaces' tests at once.
func TestQuoteExtractRails_AtExactlyTheBoundaryIsRefused(t *testing.T) {
	provider := &spyProvider{}
	ledger := &spyLedger{spend: assist.Spend{TokensIn: 500, TokensOut: 500}}
	rails := railsWith(provider, ledger, &spyLimits{role: intp(1000)})
	if _, err := rails.SegmentText(context.Background(), rep(), "x"); !errors.Is(err, assist.ErrOverBudget) {
		t.Fatalf("err = %v at exactly the limit, want a refusal", err)
	}
	if provider.calls != 0 {
		t.Fatal("the provider ran at exactly the limit")
	}
}

// TestQuoteExtractRails_UserOverrideReplacesTheRoleBudgetWholly, assist.Resolve's whole-or-absent
// rule, reached through BudgetFor. A user override of 5,000 beats a role default of 100.
func TestQuoteExtractRails_UserOverrideReplacesTheRoleBudgetWholly(t *testing.T) {
	provider := &spyProvider{reply: assist.Reply{Text: `[{"text":"480 sq ft Blu 60","qty":"480","unit":"sq ft"}]`}}
	ledger := &spyLedger{spend: assist.Spend{TokensIn: 200}}
	rails := railsWith(provider, ledger, &spyLimits{user: intp(5000), role: intp(100)})

	if _, err := rails.SegmentText(context.Background(), rep(), "480 sq ft Blu 60"); err != nil {
		t.Fatalf("SegmentText: %v, the user override should have allowed this", err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
}

// TestQuoteExtractRails_RolelessCallerIsRefusedBeforeAnySpend, tier 0 is an account whose authority
// is unknown, and unknown authority is not frontline authority (assist's own rule).
func TestQuoteExtractRails_RolelessCallerIsRefusedBeforeAnySpend(t *testing.T) {
	provider := &spyProvider{}
	ledger := &spyLedger{}
	rails := railsWith(provider, ledger, &spyLimits{})
	_, err := rails.SegmentText(context.Background(), assist.Caller{UserID: "u"}, "x")
	if !errors.Is(err, assist.ErrRefused) {
		t.Fatalf("err = %v, want assist.ErrRefused", err)
	}
	if provider.calls != 0 || len(ledger.appended) != 0 || ledger.spendCalls != 0 {
		t.Fatal("a role-less caller reached the rails")
	}
}

// -- 2 · the ledger row -------------------------------------------------------------------------

// TestQuoteExtractRails_WritesExactlyOneLedgerRowWithTheProvidersOwnNumbers.
func TestQuoteExtractRails_WritesExactlyOneLedgerRowWithTheProvidersOwnNumbers(t *testing.T) {
	provider := &spyProvider{reply: assist.Reply{
		Text:     `[{"text":"480 sq ft of Techo-Bloc Blu 60","qty":"480","unit":"sq ft"}]`,
		TokensIn: 412, TokensOut: 96,
	}}
	ledger := &spyLedger{}
	rails := railsWith(provider, ledger, &spyLimits{role: intp(60000)})

	seg, err := rails.SegmentText(context.Background(), rep(), "480 sq ft of Techo-Bloc Blu 60")
	if err != nil {
		t.Fatalf("SegmentText: %v", err)
	}
	if len(ledger.appended) != 1 {
		t.Fatalf("ledger rows = %d, want exactly 1", len(ledger.appended))
	}
	row := ledger.appended[0]
	if row.TokensIn != 412 || row.TokensOut != 96 {
		t.Errorf("row tokens = %d/%d, want the provider's own 412/96, never estimated", row.TokensIn, row.TokensOut)
	}
	if row.Kind != KindQuoteExtract {
		t.Errorf("row kind = %q, want %q so chat and extraction sum to one number", row.Kind, KindQuoteExtract)
	}
	if row.RoleCode != "SALES_REP" {
		t.Errorf("row role = %q, budgets are per role, and August's spend must stay in August's role", row.RoleCode)
	}
	if row.UserID != "user-rep" {
		t.Errorf("row user = %q", row.UserID)
	}
	if row.Provider != "spy" || row.Model != "spy-1" {
		t.Errorf("row names %q/%q, the ledger records what it ACTUALLY was", row.Provider, row.Model)
	}
	if !row.At.Equal(fixedNow) {
		t.Errorf("row at = %s, want the injected clock", row.At)
	}
	// And the answer came back with the same numbers.
	if seg.TokensIn != 412 || seg.TokensOut != 96 {
		t.Errorf("segmentation reports %d/%d", seg.TokensIn, seg.TokensOut)
	}
	if seg.Provider != "spy" || seg.Model != "spy-1" {
		t.Errorf("segmentation names %q/%q", seg.Provider, seg.Model)
	}
}

// TestQuoteExtractRails_TheStubsZerosStillWriteARow, the pilot's default provider costs nothing, so a
// ledger that only recorded non-zero rows would read "nobody used it" for the whole pilot. Counting
// EXCHANGES is the measurement the client actually asked for.
func TestQuoteExtractRails_TheStubsZerosStillWriteARow(t *testing.T) {
	provider := &spyProvider{reply: assist.Reply{Text: `[{"text":"6 bags polymeric sand"}]`}} // zero tokens
	ledger := &spyLedger{}
	rails := railsWith(provider, ledger, &spyLimits{})

	if _, err := rails.SegmentText(context.Background(), rep(), "6 bags polymeric sand"); err != nil {
		t.Fatalf("SegmentText: %v", err)
	}
	if len(ledger.appended) != 1 {
		t.Fatalf("ledger rows = %d, want 1 even at zero tokens", len(ledger.appended))
	}
	if ledger.appended[0].TokensIn != 0 || ledger.appended[0].TokensOut != 0 {
		t.Errorf("row = %+v, want honest zeros", ledger.appended[0])
	}
}

// TestQuoteExtractRails_AProviderErrorStillWritesTheRow, something was sent, so something was
// spent, and the row is how that stays true in the accounting.
func TestQuoteExtractRails_AProviderErrorStillWritesTheRow(t *testing.T) {
	provider := &spyProvider{err: errors.New("upstream 500"), reply: assist.Reply{TokensIn: 300}}
	ledger := &spyLedger{}
	rails := railsWith(provider, ledger, &spyLimits{})

	if _, err := rails.SegmentText(context.Background(), rep(), "x"); err == nil {
		t.Fatal("a provider error must reach the caller so the surface can degrade")
	}
	if len(ledger.appended) != 1 {
		t.Fatalf("ledger rows = %d, want 1, the exchange happened", len(ledger.appended))
	}
	if ledger.appended[0].TokensIn != 300 {
		t.Errorf("row tokens = %d, want the 300 the provider reported before failing", ledger.appended[0].TokensIn)
	}
}

// TestQuoteExtractRails_ALedgerWriteFailureDoesNotSwallowTheAnswer, assist/ledger.go's ruling: a
// missing row is recoverable from the provider's own billing, a broken feature is not.
func TestQuoteExtractRails_ALedgerWriteFailureDoesNotSwallowTheAnswer(t *testing.T) {
	provider := &spyProvider{reply: assist.Reply{Text: `[{"text":"480 sq ft Blu 60","qty":"480","unit":"sq ft"}]`}}
	ledger := &spyLedger{appendErr: errors.New("disk full")}
	rails := railsWith(provider, ledger, &spyLimits{})

	seg, err := rails.SegmentText(context.Background(), rep(), "480 sq ft Blu 60")
	if err != nil {
		t.Fatalf("SegmentText: %v, the accounting must not hold the answer hostage", err)
	}
	if len(seg.Phrases) != 1 {
		t.Fatalf("phrases = %d, want 1", len(seg.Phrases))
	}
}

// TestQuoteExtractRails_NoLedgerWiredStillAnswers, a partially wired deployment is a normal state
// during a rollout.
func TestQuoteExtractRails_NoLedgerWiredStillAnswers(t *testing.T) {
	provider := &spyProvider{reply: assist.Reply{Text: `[{"text":"480 sq ft Blu 60"}]`}}
	rails := railsWith(provider, nil, nil)
	if _, err := rails.SegmentText(context.Background(), rep(), "480 sq ft Blu 60"); err != nil {
		t.Fatalf("SegmentText: %v", err)
	}
}

// TestQuoteExtractRails_NilProviderIsANilSeam, the shipped default. quoteassist reads a nil
// AIExtractor as "AI is off", not as a fault, and returning nil is what makes the absence legible at
// the wiring site.
func TestQuoteExtractRails_NilProviderIsANilSeam(t *testing.T) {
	if got := NewQuoteExtractRails(nil, &spyLedger{}, &spyLimits{}, nil); got != nil {
		t.Fatalf("NewQuoteExtractRails(nil, …) = %+v, want nil", got)
	}
}

// -- 3 · the model is only asked to do English --------------------------------------------------

// TestQuoteExtractRails_NoToolsAreOfferedToTheProvider.
//
// The chat surface offers the provider the caller's permitted commands. This one offers NOTHING: it
// is a text transformation, and handing a model the registry's tools for a job that is pure English
// would give it a reason to go looking at data it has no business in.
func TestQuoteExtractRails_NoToolsAreOfferedToTheProvider(t *testing.T) {
	provider := &spyProvider{reply: assist.Reply{Text: `[{"text":"480 sq ft Blu 60"}]`}}
	rails := railsWith(provider, &spyLedger{}, &spyLimits{})
	if _, err := rails.SegmentText(context.Background(), rep(), "480 sq ft Blu 60"); err != nil {
		t.Fatalf("SegmentText: %v", err)
	}
	if len(provider.got.Tools) != 0 {
		t.Fatalf("%d tools were offered to the provider, this exchange must not be able to reach a command", len(provider.got.Tools))
	}
	if len(provider.got.Messages) != 1 {
		t.Fatalf("messages = %d, want exactly the one prompt", len(provider.got.Messages))
	}
	// The prompt tells the model not to name products. It is not a guarantee, the guarantee is that
	// Segmentation has nowhere to put one, but a prompt that asked for SKUs would be inviting the
	// thing the type system then throws away.
	if !strings.Contains(strings.ToLower(provider.got.Messages[0].Text), "do not name products") {
		t.Errorf("prompt does not ask the model to stay out of the catalogue:\n%s", provider.got.Messages[0].Text)
	}
	if !strings.Contains(provider.got.Messages[0].Text, "480 sq ft Blu 60") {
		t.Error("the caller's own text did not reach the prompt")
	}
}

// TestQuoteExtractRails_ParsesTheRequestedShapeAndTwoFallbacks, a provider's output format is not
// something this app controls, so all three shapes must produce phrases.
func TestQuoteExtractRails_ParsesTheRequestedShapeAndTwoFallbacks(t *testing.T) {
	cases := []struct {
		name     string
		reply    string
		wantText []string
	}{
		{
			"the requested JSON objects",
			`[{"text":"480 sq ft of Blu 60","qty":"480","unit":"sq ft"},{"text":"6 bags of sand","qty":"6","unit":"bags"}]`,
			[]string{"480 sq ft of Blu 60", "6 bags of sand"},
		},
		{
			"a fenced JSON array of strings",
			"```json\n[\"480 sq ft of Blu 60\", \"6 bags of sand\"]\n```",
			[]string{"480 sq ft of Blu 60", "6 bags of sand"},
		},
		{
			"prose, one item per line",
			"480 sq ft of Blu 60\n6 bags of sand",
			[]string{"480 sq ft of Blu 60", "6 bags of sand"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := &spyProvider{reply: assist.Reply{Text: tc.reply}}
			rails := railsWith(provider, &spyLedger{}, &spyLimits{})
			seg, err := rails.SegmentText(context.Background(), rep(), "anything")
			if err != nil {
				t.Fatalf("SegmentText: %v", err)
			}
			if len(seg.Phrases) != len(tc.wantText) {
				t.Fatalf("phrases = %+v, want %v", seg.Phrases, tc.wantText)
			}
			for i, want := range tc.wantText {
				if seg.Phrases[i].Text != want {
					t.Errorf("phrase %d = %q, want %q", i, seg.Phrases[i].Text, want)
				}
			}
		})
	}
}

// TestQuoteExtractRails_TheDeterministicParserFillsWhatTheModelLeftBlank, and only what it left
// blank. A model that drops a quantity costs a keystroke; a model that INVENTS one costs money, so the
// parser gets the last word on a blank and never overrides a stated value.
func TestQuoteExtractRails_TheDeterministicParserFillsWhatTheModelLeftBlank(t *testing.T) {
	provider := &spyProvider{reply: assist.Reply{Text: `[{"text":"12 tonnes of 3/4 clear stone","qty":"","unit":""}]`}}
	rails := railsWith(provider, &spyLedger{}, &spyLimits{})
	seg, err := rails.SegmentText(context.Background(), rep(), "anything")
	if err != nil {
		t.Fatalf("SegmentText: %v", err)
	}
	if len(seg.Phrases) != 1 {
		t.Fatalf("phrases = %+v", seg.Phrases)
	}
	if seg.Phrases[0].Qty != "12" || seg.Phrases[0].Unit != "tonnes" {
		t.Errorf("phrase = %+v, want the deterministic parser to have filled 12/tonnes", seg.Phrases[0])
	}

	// The other direction: a STATED quantity is kept, even one the parser would have read differently.
	provider2 := &spyProvider{reply: assist.Reply{Text: `[{"text":"a dozen tonnes of 3/4 clear stone","qty":"12","unit":"tonnes"}]`}}
	rails2 := railsWith(provider2, &spyLedger{}, &spyLimits{})
	seg2, err := rails2.SegmentText(context.Background(), rep(), "anything")
	if err != nil {
		t.Fatalf("SegmentText: %v", err)
	}
	if seg2.Phrases[0].Qty != "12" {
		t.Errorf("phrase = %+v, the model read the sentence and this code did not", seg2.Phrases[0])
	}
}

// TestQuoteExtractRails_SatisfiesTheSeamItClaimsTo, the compile-time assertion, restated as a test
// so a reader looking for the contract finds it here too.
func TestQuoteExtractRails_SatisfiesTheSeamItClaimsTo(t *testing.T) {
	var _ quoteassist.AIExtractor = railsWith(&spyProvider{}, &spyLedger{}, &spyLimits{})
}
