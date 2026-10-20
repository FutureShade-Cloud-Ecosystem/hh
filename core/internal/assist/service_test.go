package assist

// service_test.go, half C under test: the chat loop, the ledger row, and the refusal that is not
// an error.
//
// THE TEST THAT MATTERS MOST IS TestAsk_ProviderToolCallsRunThroughTheGateAsTheCaller. A model's
// tool calls are untrusted input, and the claim this pilot rests on is that they are dispatched
// through the same Registry.Run() a button press goes through, carrying the caller who asked. It is
// asserted by looking at what the READ SEAM was handed, not at what the service says it did.
//
// TestAsk_OverBudgetIsAFriendlyAnswerNotAnError is mutation-adjacent to M2 and load-bearing for the
// product: an over-budget user must get a sentence and a working command layer, never a red box.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func at(day int) time.Time { return time.Date(2026, 8, day, 10, 0, 0, 0, time.UTC) }

// chatService wires a service with a scripted provider and empty governance stores.
func chatService(p Provider, led *fakeLedger, lim *fakeLimits, set StaticSettings, rd *fakeReaders) *Service {
	if set == nil {
		set = StaticSettings{}
	}
	reg := NewRegistry(rd.deps(), nil)
	return NewService(reg, p, led, lim, set, nil).
		WithClock(func() time.Time { return at(11) })
}

// -- the loop -----------------------------------------------------------------------------------

// TestAsk_ProviderToolCallsRunThroughTheGateAsTheCaller, the central claim of half C.
func TestAsk_ProviderToolCallsRunThroughTheGateAsTheCaller(t *testing.T) {
	rd := seededReaders()
	p := &scriptedProvider{replies: []Reply{
		{ToolCalls: []ToolCall{{Command: "order.lookup", Args: Args{"order_no": "SO-4"}}}, TokensIn: 120, TokensOut: 8},
		{Text: "SO-4 is picking at Kelowna for Northgate Landscaping.", TokensIn: 200, TokensOut: 22},
	}}
	svc := chatService(p, &fakeLedger{}, newFakeLimits(), nil, rd)

	who := manager()
	ans, err := svc.Ask(context.Background(), who, "where is SO-4?", nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	// The seam was handed the ASKING USER, not a service account and not a widened caller.
	if rd.gotCaller.UserID != who.UserID || rd.gotCaller.RoleCode != who.RoleCode || rd.gotCaller.Tier != who.Tier {
		t.Fatalf("the read seam ran as %+v, want the asking user %+v", rd.gotCaller, who)
	}
	if rd.gotCaller.Global {
		t.Fatal("a branch-scoped manager must not reach the seam as a global caller")
	}
	if len(rd.gotCaller.BranchIDs) != 1 || rd.gotCaller.BranchIDs[0] != "b-kel" {
		t.Fatalf("branch grants were not carried through: %v", rd.gotCaller.BranchIDs)
	}
	if rd.gotOrder != "SO-4" {
		t.Fatalf("the seam was asked for %q, want SO-4", rd.gotOrder)
	}

	// The structured result travels alongside the prose, so a user never has to trust a sentence to
	// get the numbers.
	if len(ans.Results) != 1 || ans.Results[0].Command != "order.lookup" {
		t.Fatalf("want one order.lookup result carried back, got %+v", ans.Results)
	}
	if ans.Text != "SO-4 is picking at Kelowna for Northgate Landscaping." {
		t.Fatalf("prose = %q", ans.Text)
	}
}

// TestAsk_OnlyTheCallersOwnCommandsAreOffered: a model is never told about a tool the caller would
// be refused, so a refusal is a thing that essentially cannot happen, belt, before the braces.
func TestAsk_OnlyTheCallersOwnCommandsAreOffered(t *testing.T) {
	rd := seededReaders()
	p := &scriptedProvider{replies: []Reply{{Text: "ok"}}}
	svc := chatService(p, &fakeLedger{}, newFakeLimits(), nil, rd)

	if _, err := svc.Ask(context.Background(), frontline(), "hello", nil); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(p.seen) == 0 {
		t.Fatal("the provider was never called")
	}
	offeredNames := map[string]bool{}
	for _, tool := range p.seen[0].Tools {
		offeredNames[tool.Name] = true
	}
	allowed := map[string]bool{}
	for _, c := range svc.Commands(context.Background(), frontline()) {
		allowed[c.Code] = true
	}
	if len(offeredNames) == 0 {
		t.Fatal("no tools were offered at all, a frontline user has commands")
	}
	for name := range offeredNames {
		if !allowed[name] {
			t.Fatalf("the provider was offered %q, which this caller may not run", name)
		}
	}
	// And the set is exactly the caller's own, not a superset trimmed later.
	if len(offeredNames) != len(allowed) {
		t.Fatalf("offered %d tools, caller may run %d", len(offeredNames), len(allowed))
	}
}

// TestAsk_InventedToolCallIsRefusedAndRelayedNotFatal: a model naming a command that does not exist
// must not take the exchange down with it.
func TestAsk_InventedToolCallIsRefusedAndRelayedNotFatal(t *testing.T) {
	rd := seededReaders()
	p := &scriptedProvider{replies: []Reply{
		{ToolCalls: []ToolCall{{Command: "payment.delete", Args: Args{"id": "1"}}}},
		{Text: "I can't do that, but I can look a payment up."},
	}}
	svc := chatService(p, &fakeLedger{}, newFakeLimits(), nil, rd)

	ans, err := svc.Ask(context.Background(), leadership(), "delete payment 1", nil)
	if err != nil {
		t.Fatalf("an invented tool call must not fail the exchange, got %v", err)
	}
	if len(ans.Results) != 0 {
		t.Fatalf("an invented command produced results: %+v", ans.Results)
	}
	if rd.calls != 0 {
		t.Fatalf("an invented command reached a read seam %d times", rd.calls)
	}
	// Slice 1 (escalation): the invented command is a failed check. It never reaches the registry,
	// and the provider is asked once more with OUR text naming the check.
	if len(p.seen) < 2 {
		t.Fatal("the service did not retry after the failed check")
	}
	retry := p.seen[1].Messages
	if last := retry[len(retry)-1]; last.Role != RoleUser || last.Text != failUnknownCommand {
		t.Fatalf("the retry did not carry the validator's text: %+v", last)
	}
	if ans.Text != "I can't do that, but I can look a payment up." {
		t.Fatalf("text = %q, want the retried reply", ans.Text)
	}
}

// TestAsk_ToolCallAboveTheCallersTierIsRefusedAtTheGate, the braces. Even if a provider names a
// real command the caller may not run, Registry.Run refuses it, and no seam is touched.
func TestAsk_ToolCallAboveTheCallersTierIsRefusedAtTheGate(t *testing.T) {
	rd := seededReaders()
	// customer.balance is above a frontline caller (see the code floors).
	p := &scriptedProvider{replies: []Reply{
		{ToolCalls: []ToolCall{{Command: "customer.balance", Args: Args{"customer": "Northgate"}}}},
		{Text: "That isn't something I can look up for you."},
	}}
	svc := chatService(p, &fakeLedger{}, newFakeLimits(), nil, rd)

	who := frontline()
	if _, err := NewRegistry(rd.deps(), nil).Run(context.Background(), who, "customer.balance", Args{"customer": "x"}); !errors.Is(err, ErrRefused) {
		t.Skipf("customer.balance is not above tier %d in this build; the gate test lives in registry_test.go", who.Tier)
	}

	ans, err := svc.Ask(context.Background(), who, "what does Northgate owe?", nil)
	if err != nil {
		t.Fatalf("a refused tool call must not fail the exchange, got %v", err)
	}
	if len(ans.Results) != 0 {
		t.Fatalf("a refused command produced results: %+v", ans.Results)
	}
	if rd.gotCust != "" {
		t.Fatalf("a refused command reached the customer seam with %q", rd.gotCust)
	}
}

// TestAsk_HistoryIsBoundedSoTheBillDoesNotGrowQuadratically.
func TestAsk_HistoryIsBoundedSoTheBillDoesNotGrowQuadratically(t *testing.T) {
	rd := seededReaders()
	p := &scriptedProvider{replies: []Reply{{Text: "ok"}}}
	svc := chatService(p, &fakeLedger{}, newFakeLimits(), nil, rd)

	var history []Message
	for i := 0; i < 40; i++ {
		history = append(history, Message{Role: RoleUser, Text: "older question"})
	}
	if _, err := svc.Ask(context.Background(), manager(), "the new one", history); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	sent := p.seen[0].Messages
	if len(sent) != maxHistory+1 {
		t.Fatalf("sent %d messages, want %d trimmed turns plus the new question", len(sent), maxHistory)
	}
	if sent[len(sent)-1].Text != "the new one" {
		t.Fatalf("the new question must be last, got %q", sent[len(sent)-1].Text)
	}
}

// TestAsk_ProviderFailureOnTheSummarisingTurnStillAnswers: the tools already ran and the results are
// good. Losing the prose must not lose the answer.
func TestAsk_ProviderFailureOnTheSummarisingTurnStillAnswers(t *testing.T) {
	rd := seededReaders()
	p := &failAfterFirst{first: Reply{
		ToolCalls: []ToolCall{{Command: "order.lookup", Args: Args{"order_no": "SO-4"}}},
		TokensIn:  100, TokensOut: 5,
	}}
	svc := chatService(p, &fakeLedger{}, newFakeLimits(), nil, rd)

	ans, err := svc.Ask(context.Background(), manager(), "where is SO-4?", nil)
	if err != nil {
		t.Fatalf("a failed summarising turn must degrade, not fail: %v", err)
	}
	if len(ans.Results) != 1 {
		t.Fatalf("the command results were lost: %+v", ans.Results)
	}
	if strings.TrimSpace(ans.Text) == "" {
		t.Fatal("the answer has no words at all, the headlines must stand in for the prose")
	}
}

type failAfterFirst struct {
	first Reply
	n     int
}

func (p *failAfterFirst) Name() string  { return "flaky" }
func (p *failAfterFirst) Model() string { return "flaky-1" }
func (p *failAfterFirst) Complete(context.Context, CompletionRequest) (Reply, error) {
	p.n++
	if p.n == 1 {
		return p.first, nil
	}
	return Reply{}, errors.New("provider is having a day")
}

// -- the ledger ---------------------------------------------------------------------------------

// TestAsk_WritesALedgerRowOnEveryExchange, including the stub's zeros (governance 1).
func TestAsk_WritesALedgerRowOnEveryExchange(t *testing.T) {
	rd := seededReaders()
	led := &fakeLedger{}
	// The STUB, not a scripted provider: this is the pilot's default and it costs zero tokens.
	svc := NewService(NewRegistry(rd.deps(), nil), NewStubProvider(), led, newFakeLimits(), StaticSettings{}, nil).
		WithClock(func() time.Time { return at(11) })

	who := manager()
	if _, err := svc.Ask(context.Background(), who, "where is order SO-4?", nil); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(led.entries) != 1 {
		t.Fatalf("got %d ledger rows, want exactly 1, a zero-token exchange is still an exchange", len(led.entries))
	}
	e := led.entries[0]
	if e.UserID != who.UserID || e.RoleCode != who.RoleCode {
		t.Fatalf("row = %+v, want it attributed to the asking user and the role they held", e)
	}
	if e.Provider != "stub" || e.Model != "hos-stub-1" {
		t.Fatalf("row = %+v, want the provider and model actually used", e)
	}
	if e.TokensIn != 0 || e.TokensOut != 0 {
		t.Fatalf("the stub must record honest zeros, got %d/%d", e.TokensIn, e.TokensOut)
	}
	if e.Kind != KindChat {
		t.Fatalf("Kind = %q, want %q", e.Kind, KindChat)
	}
	if !e.At.Equal(at(11)) {
		t.Fatalf("At = %s, want the service clock", e.At)
	}
}

// TestAsk_LedgerSumsBothProviderTurns: an exchange that took two provider turns is billed for both,
// as ONE row. Counting one turn would under-report the bill by roughly half.
func TestAsk_LedgerSumsBothProviderTurns(t *testing.T) {
	led := &fakeLedger{}
	p := &scriptedProvider{replies: []Reply{
		{ToolCalls: []ToolCall{{Command: "order.lookup", Args: Args{"order_no": "SO-4"}}}, TokensIn: 120, TokensOut: 8},
		{Text: "picking.", TokensIn: 200, TokensOut: 22},
	}}
	svc := chatService(p, led, newFakeLimits(), nil, seededReaders())

	ans, err := svc.Ask(context.Background(), manager(), "where is SO-4?", nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(led.entries) != 1 {
		t.Fatalf("got %d rows, want 1 row per exchange regardless of provider turns", len(led.entries))
	}
	if got := led.entries[0]; got.TokensIn != 320 || got.TokensOut != 30 {
		t.Fatalf("ledger row = %d/%d, want 320/30 (both turns summed)", got.TokensIn, got.TokensOut)
	}
	if ans.Usage == nil || ans.Usage.TokensIn != 320 || ans.Usage.TokensOut != 30 {
		t.Fatalf("reported usage = %+v, want it to agree with the ledger row", ans.Usage)
	}
}

// TestAsk_ALedgerWriteFailureDoesNotSwallowTheAnswer: an availability problem must not be made out
// of a bookkeeping one.
func TestAsk_ALedgerWriteFailureDoesNotSwallowTheAnswer(t *testing.T) {
	led := &fakeLedger{err: errors.New("disk is full")}
	p := &scriptedProvider{replies: []Reply{{Text: "here you go", TokensIn: 5, TokensOut: 5}}}
	svc := chatService(p, led, newFakeLimits(), nil, seededReaders())

	ans, err := svc.Ask(context.Background(), manager(), "hello", nil)
	if err != nil {
		t.Fatalf("a ledger failure must not fail the answer, got %v", err)
	}
	if ans.Text != "here you go" {
		t.Fatalf("the answer was lost: %q", ans.Text)
	}
}

// TestRunCommand_WritesNoLedgerRow, half B costs nothing, so it records nothing. This is what keeps
// the command layer working when the pilot is unfunded.
func TestRunCommand_WritesNoLedgerRow(t *testing.T) {
	led := &fakeLedger{}
	svc := chatService(&scriptedProvider{}, led, newFakeLimits(), nil, seededReaders())

	if _, err := svc.RunCommand(context.Background(), manager(), "order.lookup", Args{"order_no": "SO-4"}); err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if len(led.entries) != 0 {
		t.Fatalf("half B wrote %d ledger rows; it must write none", len(led.entries))
	}
}

// -- enforcement --------------------------------------------------------------------------------

// TestAsk_OverBudgetIsAFriendlyAnswerNotAnError, governance 3, in full.
func TestAsk_OverBudgetIsAFriendlyAnswerNotAnError(t *testing.T) {
	rd := seededReaders()
	led := &fakeLedger{entries: []Entry{
		{UserID: "u-sm", RoleCode: "SALES_MANAGER", TokensIn: 900, TokensOut: 100, At: at(3)},
	}}
	lim := newFakeLimits()
	lim.users["u-sm"] = 1000 // spent exactly 1000 of 1000, the boundary

	p := &scriptedProvider{replies: []Reply{{Text: "should never be reached"}}}
	svc := chatService(p, led, lim, nil, rd)

	ans, err := svc.Ask(context.Background(), manager(), "where is SO-4?", nil)
	if err != nil {
		t.Fatalf("over budget must NOT be an error return, got %v", err)
	}
	if !ans.LimitReached {
		t.Fatal("LimitReached must be set so the surface can render a quiet notice")
	}
	if ans.Text != LimitReachedMessage {
		t.Fatalf("message = %q, want the constant so the test, the UI and the screenshot agree", ans.Text)
	}
	// The money was NOT spent: the point of a budget is that the provider is never called.
	if len(p.seen) != 0 {
		t.Fatalf("the provider was called %d times while over budget", len(p.seen))
	}
	// And no row was written: a refusal is not a transaction.
	if len(led.entries) != 1 {
		t.Fatalf("an over-budget refusal wrote a ledger row (%d rows now)", len(led.entries))
	}
	// The message must point at the thing that still works.
	if !strings.Contains(ans.Text, "buttons below") {
		t.Fatalf("the over-budget message must name the working alternative, got %q", ans.Text)
	}
}

// TestRunCommand_KeepsWorkingOverBudget, governance 3's second half, and the whole reason half B was
// built first.
func TestRunCommand_KeepsWorkingOverBudget(t *testing.T) {
	rd := seededReaders()
	led := &fakeLedger{entries: []Entry{{UserID: "u-sm", TokensIn: 99999, At: at(3)}}}
	lim := newFakeLimits()
	lim.users["u-sm"] = 10

	svc := chatService(&scriptedProvider{}, led, lim, nil, rd)

	res, err := svc.RunCommand(context.Background(), manager(), "order.lookup", Args{"order_no": "SO-4"})
	if err != nil {
		t.Fatalf("the command layer must keep working over budget, got %v", err)
	}
	if res.Empty || res.Command != "order.lookup" {
		t.Fatalf("got %+v, want a real order.lookup answer", res)
	}
}

// TestAsk_JustUnderBudgetStillRuns, the other side of the boundary, so a passing over-budget test
// cannot be satisfied by refusing everyone.
func TestAsk_JustUnderBudgetStillRuns(t *testing.T) {
	led := &fakeLedger{entries: []Entry{{UserID: "u-sm", TokensIn: 999, At: at(3)}}}
	lim := newFakeLimits()
	lim.users["u-sm"] = 1000

	p := &scriptedProvider{replies: []Reply{{Text: "one token of headroom is headroom", TokensIn: 1}}}
	svc := chatService(p, led, lim, nil, seededReaders())

	ans, err := svc.Ask(context.Background(), manager(), "hello", nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if ans.LimitReached {
		t.Fatal("a user one token short of their limit must not be refused")
	}
	if len(p.seen) != 1 {
		t.Fatalf("the provider was called %d times, want 1", len(p.seen))
	}
}

// TestAsk_UnassignedRoleIsRefusedBeforeAnySpend: tier 0 is unknown authority, not least privilege.
func TestAsk_UnassignedRoleIsRefusedBeforeAnySpend(t *testing.T) {
	led := &fakeLedger{}
	p := &scriptedProvider{replies: []Reply{{Text: "nope"}}}
	svc := chatService(p, led, newFakeLimits(), nil, seededReaders())

	_, err := svc.Ask(context.Background(), unassigned(), "anything", nil)
	wantErrIs(t, err, ErrRefused)
	if len(p.seen) != 0 {
		t.Fatal("an unassigned account reached the provider")
	}
	if len(led.entries) != 0 {
		t.Fatal("an unassigned account wrote a ledger row")
	}
}

// TestAsk_ChatDisabledIsAnOrdinaryAnswer: an administrator switching chat off is not an outage.
func TestAsk_ChatDisabledIsAnOrdinaryAnswer(t *testing.T) {
	p := &scriptedProvider{replies: []Reply{{Text: "nope"}}}
	svc := chatService(p, &fakeLedger{}, newFakeLimits(), StaticSettings{KeyChatEnabled: "false"}, seededReaders())

	ans, err := svc.Ask(context.Background(), manager(), "hello", nil)
	if err != nil {
		t.Fatalf("chat being off must not be an error, got %v", err)
	}
	if ans.Text != ChatDisabledMessage {
		t.Fatalf("message = %q, want the constant", ans.Text)
	}
	if len(p.seen) != 0 {
		t.Fatal("the provider was called with chat switched off")
	}
}

// -- the visibility toggle (governance 4) -------------------------------------------------------

func TestMySpendVisible_IsATechAdminToggle(t *testing.T) {
	set := StaticSettings{}
	svc := chatService(&scriptedProvider{}, &fakeLedger{}, newFakeLimits(), set, seededReaders())
	ctx := context.Background()

	// Default: hidden from non-admins.
	if svc.MySpendVisible(ctx, manager()) {
		t.Fatal("spend must default to hidden from non-admins")
	}
	// An admin always sees it, they set the toggle, so hiding it from them is theatre.
	if !svc.MySpendVisible(ctx, leadership()) {
		t.Fatal("an admin must always see spend")
	}
	// Flipped on: visible to everyone.
	set[KeySpendVisibleToNonAdmin] = "true"
	if !svc.MySpendVisible(ctx, manager()) {
		t.Fatal("the toggle did not take effect for a non-admin")
	}
	// A typo must fail SAFE, towards hidden, never towards exposed.
	set[KeySpendVisibleToNonAdmin] = "ture"
	if svc.MySpendVisible(ctx, manager()) {
		t.Fatal("an unreadable toggle value exposed spend; it must fail towards hidden")
	}
}

// TestSettings_ATypoDoesNotSwitchThePilotOff, the deliberate asymmetry in SettingsFrom.
func TestSettings_ATypoDoesNotSwitchThePilotOff(t *testing.T) {
	s := SettingsFrom(map[string]string{KeyChatEnabled: "yes-please"})
	if !s.ChatEnabled {
		t.Fatal("an unreadable chat_enabled value switched the pilot off; it must fail towards on")
	}
	if SettingsFrom(map[string]string{KeyChatEnabled: "off"}).ChatEnabled {
		t.Fatal("an explicit 'off' must switch chat off")
	}
}

// TestSettings_AnEmptyTableIsTheSafeConfiguration: a deployment before 0153 boots on the stub.
func TestSettings_AnEmptyTableIsTheSafeConfiguration(t *testing.T) {
	s := SettingsFrom(map[string]string{})
	if s.Provider != ProviderStub {
		t.Fatalf("Provider = %q, want the stub", s.Provider)
	}
	if !s.ChatEnabled {
		t.Fatal("chat must be on by default")
	}
	if s.SpendVisibleToNonAdmin {
		t.Fatal("spend must be hidden by default")
	}
}

// TestSetSetting_RefusesAKeyItDoesNotKnow: a settings table that accepts anything becomes a junk
// drawer, and the first symptom is an admin editing 'chat_enable' for twenty minutes.
func TestSetSetting_RefusesAKeyItDoesNotKnow(t *testing.T) {
	set := StaticSettings{}
	svc := chatService(&scriptedProvider{}, &fakeLedger{}, newFakeLimits(), set, seededReaders())
	ctx := context.Background()

	if err := svc.SetSetting(ctx, "chat_enable", "true", "u-gm"); err == nil {
		t.Fatal("a misspelled key must be refused, not stored")
	}
	if _, ok := set["chat_enable"]; ok {
		t.Fatal("the misspelled key was written to the table anyway")
	}
	if err := svc.SetSetting(ctx, KeyProvider, "openai", "u-gm"); err == nil {
		t.Fatal("an unknown provider must be refused")
	}
	if err := svc.SetSetting(ctx, KeyModel, "", "u-gm"); err == nil {
		t.Fatal("an empty model name must be refused")
	}
	if err := svc.SetSetting(ctx, KeyChatEnabled, "maybe", "u-gm"); err == nil {
		t.Fatal("a non-boolean for a yes/no setting must be refused")
	}
	if err := svc.SetSetting(ctx, KeyProvider, ProviderAnthropic, "u-gm"); err != nil {
		t.Fatalf("a legal provider must be accepted, got %v", err)
	}
}

// -- the provider seam (SOP-1) ------------------------------------------------------------------

// TestProviderSelection_NoKeyMeansTheStubAndTheAppStillBoots.
func TestProviderSelection_NoKeyMeansTheStubAndTheAppStillBoots(t *testing.T) {
	cfg := DefaultSettings()
	cfg.Provider = ProviderAnthropic

	p := NewProviderFromConfig(cfg, "", "", nil)
	if p.Name() != "stub" {
		t.Fatalf("with no key present the provider is %q, want the stub, absent key must not stop the app", p.Name())
	}
	// With a key it selects the real one. The key is passed in and never read from the environment
	// here, which is what makes this testable at all.
	p = NewProviderFromConfig(cfg, "sk-not-a-real-key", "", nil)
	if p.Name() != "anthropic" {
		t.Fatalf("with a key the provider is %q, want anthropic", p.Name())
	}
	// Stub stays stub regardless.
	if got := NewProviderFromConfig(DefaultSettings(), "sk-not-a-real-key", "", nil); got.Name() != "stub" {
		t.Fatalf("the stub configuration selected %q", got.Name())
	}
}

// TestEnvAPIKey_IsANameNotAValue, SOP-1's testable shape. The package may know the NAME of the
// variable; nothing in it may carry a key.
func TestEnvAPIKey_IsANameNotAValue(t *testing.T) {
	if EnvAPIKey != "HOS_ASSIST_ANTHROPIC_API_KEY" {
		t.Fatalf("EnvAPIKey = %q, NOTES-FIX-431.md documents this name for operators", EnvAPIKey)
	}
	if strings.HasPrefix(EnvAPIKey, "sk-") {
		t.Fatal("EnvAPIKey holds something that looks like a credential")
	}
}

// TestKeyPresent_IsABooleanAndNothingMore.
func TestKeyPresent_IsABooleanAndNothingMore(t *testing.T) {
	svc := chatService(&scriptedProvider{}, &fakeLedger{}, newFakeLimits(), nil, seededReaders())
	if svc.KeyPresent() {
		t.Fatal("KeyPresent defaults to true; it must default to false")
	}
	if !svc.WithKeyPresent(true).KeyPresent() {
		t.Fatal("WithKeyPresent(true) did not take")
	}
}

// -- the stub provider (half C with zero keys) --------------------------------------------------

// TestStub_AnswersFromCommandResultsWithNoNetwork walks the whole pilot on the default provider.
func TestStub_AnswersFromCommandResultsWithNoNetwork(t *testing.T) {
	rd := seededReaders()
	led := &fakeLedger{}
	svc := NewService(NewRegistry(rd.deps(), nil), NewStubProvider(), led, newFakeLimits(), StaticSettings{}, nil).
		WithClock(func() time.Time { return at(11) })

	ans, err := svc.Ask(context.Background(), leadership(), "where is order SO-4?", nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(ans.Results) != 1 || ans.Results[0].Command != "order.lookup" {
		t.Fatalf("the stub did not route to order.lookup: %+v", ans.Results)
	}
	if !strings.Contains(ans.Text, "SO-4") {
		t.Fatalf("the answer does not mention the order it looked up: %q", ans.Text)
	}
	if rd.gotOrder != "SO-4" {
		t.Fatalf("the seam was asked for %q", rd.gotOrder)
	}
}

// TestStub_SaysSoWhenItCannotAnswer, an assistant that cannot answer must say so plainly rather
// than inventing something. There is no model here to hallucinate with, and that is the feature.
func TestStub_SaysSoWhenItCannotAnswer(t *testing.T) {
	rd := seededReaders()
	svc := NewService(NewRegistry(rd.deps(), nil), NewStubProvider(), &fakeLedger{}, newFakeLimits(), StaticSettings{}, nil).
		WithClock(func() time.Time { return at(11) })

	ans, err := svc.Ask(context.Background(), leadership(), "what is the meaning of life?", nil)
	if err != nil {
		t.Fatalf("an unanswerable question is not an error, got %v", err)
	}
	if len(ans.Results) != 0 {
		t.Fatalf("an unanswerable question ran commands: %+v", ans.Results)
	}
	if strings.TrimSpace(ans.Text) == "" {
		t.Fatal("the assistant said nothing at all")
	}
	if rd.calls != 0 {
		t.Fatalf("an unanswerable question touched a read seam %d times", rd.calls)
	}
}

// TestStub_RoutesToTheCallersOwnCommandsOnly: a frontline user asking a balance question is told it
// is not available TO THEM, and no seam is touched.
func TestStub_RoutesToTheCallersOwnCommandsOnly(t *testing.T) {
	rd := seededReaders()
	svc := NewService(NewRegistry(rd.deps(), nil), NewStubProvider(), &fakeLedger{}, newFakeLimits(), StaticSettings{}, nil).
		WithClock(func() time.Time { return at(11) })

	who := frontline()
	allowed := map[string]bool{}
	for _, c := range svc.Commands(context.Background(), who) {
		allowed[c.Code] = true
	}
	if allowed["customer.balance"] {
		t.Skip("customer.balance is available to frontline in this build")
	}

	ans, err := svc.Ask(context.Background(), who, "what is the balance for Northgate Landscaping?", nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(ans.Results) != 0 {
		t.Fatalf("a command the caller may not run produced results: %+v", ans.Results)
	}
	if rd.gotCust != "" {
		t.Fatalf("the customer seam was reached with %q by a caller who may not ask", rd.gotCust)
	}
}

// TestSpeak_DoesNotRepeatWhatTheScreenAlreadyShows.
//
// The prose and the structured results travel together (Answer.Text and Answer.Results), and the
// surface renders both. If the spoken sentence also flattens every field of every item, the user
// reads the same record twice, once as a sentence, once as the card underneath it. That is what
// the walkthrough shot caught before this test existed.
//
// renderResult (the MODEL-facing renderer) must keep the full dump: a provider composing a sentence
// cannot see the screen. The two renderers are deliberately different and this pins the difference.
func TestSpeak_DoesNotRepeatWhatTheScreenAlreadyShows(t *testing.T) {
	res := Result{
		Command:  "order.lookup",
		Headline: "SO-4 is picking, Northgate Landscaping, $5,123.00.",
		Items: []Item{{
			Title:  "SO-4",
			Fields: []Field{{Label: "Status", Value: "PICKING"}, {Label: "Branch", Value: "Kelowna"}},
		}},
		Note: "Showing the first 5 of 12.",
	}

	spoken := speak([]Result{res})
	if !strings.Contains(spoken, res.Headline) {
		t.Fatalf("the headline was lost: %q", spoken)
	}
	// The caveat IS carried, it is a statement about the answer, not a repetition of it.
	if !strings.Contains(spoken, "Showing the first 5 of 12.") {
		t.Fatalf("the note was dropped: %q", spoken)
	}
	// The fields are NOT, because the card beneath the sentence is already showing them.
	if strings.Contains(spoken, "Status PICKING") || strings.Contains(spoken, "Kelowna") {
		t.Fatalf("speak() flattened the item fields into the prose, duplicating the result card: %q", spoken)
	}

	// The model-facing renderer keeps everything, a provider cannot see the screen.
	rendered := renderResult(res)
	if !strings.Contains(rendered, "Status PICKING") || !strings.Contains(rendered, "Kelowna") {
		t.Fatalf("renderResult must still carry the fields for a provider to read: %q", rendered)
	}
}
