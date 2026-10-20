package assist

// service.go, where halves B and C meet, and where the budget is enforced.
//
// Ask() IS THE WHOLE OF HALF C. Read it top to bottom and the pilot's every claim is visible:
//
//	1. is chat switched on at all               → config, one row
//	2. what may THIS caller ask                 → Registry.List(ctx, caller), their commands, only
//	3. has THIS caller money left               → spend >= budget, refused as a plain sentence
//	4. ask the provider, offering only (2)
//	5. run any tool calls THROUGH THE REGISTRY, as the same caller
//	6. ask the provider again for the sentence
//	7. write the ledger row, always
//
// Step 5 is the one that matters. The provider's tool calls are UNTRUSTED INPUT, a model can name
// a command it was not offered, or one that does not exist, and they are dispatched through
// exactly the same Registry.Run() a button press goes through, with the Caller that arrived on the
// request. There is no branch in this file that constructs a different Caller, and none that calls
// a reader directly. A model that invents "payment.delete" gets ErrUnknownCommand; one that asks
// for a command above the caller's tier gets ErrRefused; and either way the user gets a sentence,
// not a stack trace.
//
// THE LOOP IS BOUNDED AT ONE TOOL ROUND. Not because a second round would be wrong, but because an
// unbounded tool loop is an unbounded bill, and the first thing a pilot with a token budget must
// not do is make its own budget unpredictable. One round answers all five commands. If a future
// question genuinely needs two, that is a decision with a cost attached, and it should be made by
// someone looking at the ledger.

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// Usage is what one exchange cost, reported back for the surfaces that may see it.
type Usage struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	TokensIn  int    `json:"tokens_in"`
	TokensOut int    `json:"tokens_out"`
}

// Answer is one chat reply.
//
// Results carries the STRUCTURED command output alongside the prose, so the panel can render the
// same table the command button would have produced. The prose is what the assistant said; the
// results are what it said it about, and a user who wants the numbers should never have to trust a
// sentence to get them.
//
// LimitReached is NOT an error flag. It is an ordinary state with an ordinary message, and the
// frontend renders it as a quiet notice, never role="alert", never red. Nothing has gone wrong: a
// person reached a number their administrator chose.
type Answer struct {
	Text         string   `json:"text"`
	Results      []Result `json:"results"`
	LimitReached bool     `json:"limit_reached"`
	Usage        *Usage   `json:"usage,omitempty"`

	// -- DIB-478 --------------------------------------------------------------------------------

	// Ran is which commands this answer actually ran, with their arguments. The client echoes it
	// back on the next turn and it is what conversational resolution reads (conversation.go). Only
	// SUCCESSFUL invocations appear: a refused command did not run, and letting it into the history
	// would let a refusal become a carried entity on the next turn.
	Ran []Invocation `json:"ran"`

	// Clarify is present when the honest answer was a QUESTION, genuinely ambiguous input, asked
	// rather than guessed at. It is a first-class answer type, not an error, and the surface renders
	// its options as pickable controls whose choice feeds the next turn.
	Clarify *Clarify `json:"clarify,omitempty"`

	// Carried names, in plain words, what this answer took from earlier turns ("your question about
	// stock", "product Brussels Block"). It is shown quietly beneath the answer, and it exists
	// because the most likely way a carried entity produces a misread number is SILENTLY: a person
	// who asked about Brussels Block and then said "what about Kingston?" must be able to see, at a
	// glance, that the figure in front of them is still Brussels Block's.
	Carried []string `json:"carried,omitempty"`

	// Suggest is DIB-513: command codes the surface should offer as pickable chips beneath a reply
	// that could not answer. `omitempty`, and nil on every answer that DID answer, an answer with
	// numbers in it does not need a menu under it.
	Suggest []string `json:"suggest,omitempty"`
}

// LimitReachedMessage is the exact sentence an over-budget user sees. It is a constant so the test,
// the frontend fixture and the screenshot in walkthrough-431/ are provably the same words.
//
// The second sentence is the point of having built half B first: the commands cost nothing and keep
// working, so the answer to "I'm out of AI" is "here is the thing that still works", not a dead end.
const LimitReachedMessage = "You've used this month's AI allowance, ask your admin if you need more. " +
	"You can still look things up with the buttons below; those don't use any AI."

// ChatDisabledMessage is what a user sees when an administrator has switched chat off entirely.
const ChatDisabledMessage = "The assistant's chat is switched off right now. " +
	"You can still look things up with the buttons below."

// Service is the assistant.
type Service struct {
	reg      *Registry
	provider Provider
	ledger   LedgerStore
	limits   LimitStore
	settings SettingStore
	staff    StaffLister
	log      *slog.Logger

	// Slice 2 metering (meter.go): the spend and price tables and the dealer wide cap.
	spend     SpendStore
	prices    PriceStore
	dealerCap int64
	rates     *RateLimits
	zone      houseday.Source

	// resolver is DIB-478's conversational layer. It may be nil, a deployment with no resolver
	// answers every question as a fresh one, which is exactly the pre-478 behaviour, so the depth is
	// additive rather than load-bearing.
	resolver *Resolver

	// keyPresent is set at boot from the environment, see WithKeyPresent. It is never the key.
	keyPresent bool

	// now is injectable so the month-window tests do not have to wait for a month.
	now func() time.Time
}

// NewService wires the assistant. Every dependency is an interface; every one of them may be nil in
// a degraded deployment and the service says something honest rather than panicking.
func NewService(reg *Registry, p Provider, ledger LedgerStore, limits LimitStore, settings SettingStore, log *slog.Logger) *Service {
	if p == nil {
		p = NewStubProvider()
	}
	return &Service{reg: reg, provider: p, ledger: ledger, limits: limits, settings: settings, log: log, now: time.Now}
}

// WithClock replaces the clock (tests only).
func (s *Service) WithClock(f func() time.Time) *Service { s.now = f; return s }

// WithResolver attaches DIB-478's conversational resolver.
//
// SEPARATE FROM NewService, deliberately. The resolver needs caller-scoped catalogue and branch
// reads that a minimal deployment may not have wired, and a constructor that required them would
// make the whole assistant refuse to start over an optional feature. Nil resolver = every question
// answered as a fresh one.
func (s *Service) WithResolver(r *Resolver) *Service { s.resolver = r; return s }

// WithKeyPresent records whether the provider API key was found in the environment at boot.
//
// A BOOLEAN, AND ONLY A BOOLEAN. This is the entire extent to which any layer above the provider
// learns about the credential: enough for the admin screen to distinguish "you never set the key"
// from "the vendor is refusing us", and not enough to leak a character of it (SOP-1). The value
// itself never leaves NewAnthropicProvider.
func (s *Service) WithKeyPresent(v bool) *Service { s.keyPresent = v; return s }

// KeyPresent reports whether a provider credential was configured at boot.
func (s *Service) KeyPresent() bool { return s.keyPresent }

// Commands lists what the caller may run, the command palette's contents.
func (s *Service) Commands(ctx context.Context, c Caller) []Command { return s.reg.List(ctx, c) }

// RunCommand executes one command directly: HALF B, with no AI in the path and no ledger row,
// because nothing was spent.
//
// This is the method that must keep working when everything about half C is broken, off, or
// unfunded. It touches neither the provider nor the budget.
func (s *Service) RunCommand(ctx context.Context, c Caller, code string, a Args) (Result, error) {
	return s.reg.Run(ctx, c, code, a)
}

// Settings resolves the config, defaulting on any failure.
func (s *Service) Settings(ctx context.Context) Settings {
	if s.settings == nil {
		return DefaultSettings()
	}
	raw, err := s.settings.Settings(ctx)
	if err != nil {
		if s.log != nil {
			s.log.Warn("assist: cannot read settings, using defaults", "err", err)
		}
		return DefaultSettings()
	}
	return SettingsFrom(raw)
}

// Budget resolves the caller's monthly allowance.
func (s *Service) Budget(ctx context.Context, c Caller) (Budget, error) {
	return BudgetFor(ctx, s.limits, c)
}

// MonthSpend is the caller's usage so far this calendar month.
//
// With the spend table wired (meter.go) it is read from there, so an open reservation counts at its
// estimate; the older token ledger is the fallback of a deployment without it.
func (s *Service) MonthSpend(ctx context.Context, userID string) (Spend, error) {
	if s.spend != nil {
		from, to, err := s.monthWindow(ctx)
		if err != nil {
			return Spend{}, err
		}
		return s.spend.UserMonth(ctx, userID, from, to)
	}
	if s.ledger == nil {
		return Spend{}, nil
	}
	from, to, err := s.monthWindow(ctx)
	if err != nil {
		return Spend{}, err
	}
	return s.ledger.SpendFor(ctx, userID, from, to)
}

// Ask answers one question as the caller. See the file header for the seven steps.
func (s *Service) Ask(ctx context.Context, c Caller, question string, history []Message) (Answer, error) {
	if !c.HasRole() {
		// No assigned role: no commands, so no useful conversation. Refused before any spend.
		return Answer{}, ErrRefused
	}
	cfg := s.Settings(ctx)
	if !cfg.ChatEnabled {
		return Answer{Text: ChatDisabledMessage}, nil
	}

	// Slice 2: the rate limit, before the budget and before anything that can cost. A refused
	// request charges nothing and is an ordinary answer.
	if err := s.admit(ctx, c); err != nil {
		return Answer{Text: RateLimitedMessage, Results: []Result{}}, nil
	}

	// Step 2: the caller's own commands, and nothing else, become the tool set.
	tools := toolSpecsFor(s.reg.List(ctx, c))

	// Step 3: the budget. Checked BEFORE the provider is called, because the point of a budget is
	// that the money is not spent, not that it is spent and then reported.
	budget, err := s.Budget(ctx, c)
	if err != nil {
		return Answer{}, err
	}
	spent, err := s.MonthSpend(ctx, c.UserID)
	if err != nil {
		if s.spend != nil {
			// The spend table cannot be read, so nothing can be reserved: the plain unavailable
			// answer, and the provider is not reached.
			s.warn("assist: cannot read month spend, the question is not sent", "err", err)
			return Answer{Text: UnavailableMessage, Results: []Result{}}, nil
		}
		return Answer{}, err
	}
	if OverBudget(spent, budget) {
		// A plain, friendly answer, NOT an error return. The handler must not be able to turn this
		// into a 4xx by accident, so it never becomes one.
		return Answer{Text: LimitReachedMessage, LimitReached: true}, nil
	}

	// -- Step 3.5 (DIB-478): WHAT IS THIS TURN ACTUALLY ASKING? ---------------------------------
	//
	// Before the provider is consulted at all, the conversation is resolved deterministically over
	// the STRUCTURED history (resolver.go). Three of its four outcomes short-circuit the provider
	// entirely, which is a cost saving and, more importantly, a CORRECTNESS property: "what does
	// 'it' refer to" is answered by code, identically, whether the provider is the stub, a vendor,
	// or absent.
	window := NewWindow(history)
	res := Resolution{Kind: ResolveNone}
	if s.resolver != nil {
		res = s.resolver.Resolve(ctx, c, tools, window, question)
	}

	switch res.Kind {
	case ResolveClarify:
		// AN HONEST QUESTION IS AN ANSWER. No command ran, nothing was spent beyond the exchange
		// itself, and the surface renders pickable options that feed the next turn.
		s.record(ctx, c, 0, 0)
		return Answer{Text: res.Clarify.Question, Clarify: res.Clarify, Results: []Result{}, Ran: nil, Usage: s.usage(0, 0)}, nil
	case ResolveNoReferent:
		// NEVER FABRICATE. The window holds no referent, so the assistant says so plainly instead of
		// picking the most recent plausible thing and sounding certain about it.
		s.record(ctx, c, 0, 0)
		return Answer{Text: res.Refusal, Results: []Result{}, Usage: s.usage(0, 0)}, nil
	}

	msgs := append(trimHistory(history), Message{Role: RoleUser, Text: question})
	var tokensIn, tokensOut int
	var calls []ToolCall

	if len(res.Calls) > 0 {
		// A RESOLVED FOLLOW-UP SKIPS THE PROVIDER'S FIRST ROUND. The resolver already decided which
		// command and which arguments; asking a model to decide it again would be paying for a second
		// opinion about a question that has a deterministic answer, and taking that opinion would put
		// the referent back in the model's hands.
		calls = res.Calls
	} else {
		// Step 4: the ordinary path, a self-contained question, put to the provider.
		reply, err := s.complete(ctx, c, CompletionRequest{Messages: msgs, Tools: tools})
		if err != nil {
			if ans, ok := refusalAnswer(err); ok {
				return ans, nil
			}
			return Answer{}, err
		}
		tokensIn, tokensOut = reply.TokensIn, reply.TokensOut
		if len(reply.ToolCalls) == 0 {
			// Prose with no lookups: the capability answer, the "I can't answer that one yet", the
			// menu. Nothing ran, so nothing is recorded as having run.
			s.record(ctx, c, tokensIn, tokensOut)
			// Suggest rides along UNCHANGED from the provider. The service does not decide what to
			// offer, the provider knows which tools it was handed, and this path is the only one
			// that can produce a menu-shaped reply.
			return Answer{
				Text:    reply.Text,
				Results: []Result{},
				Suggest: reply.Suggest,
				Usage:   s.usage(tokensIn, tokensOut),
			}, nil
		}
		if strings.TrimSpace(reply.Text) != "" {
			msgs = append(msgs, Message{Role: RoleAssistant, Text: reply.Text})
		}
		calls = reply.ToolCalls
	}

	// Step 4.4 (DIB-478): A COMPLETE SENTENCE MAY STILL BE PART OF A CONVERSATION.
	//
	// The provider fills the slots the sentence contains. It cannot fill the ones the sentence left
	// out because the previous turn already established them, "what's coming in at Kingston?" two
	// lines under an answer about TB-BLU60 is still about TB-BLU60. Only REQUIRED slots, only EMPTY
	// ones, and every fill is declared in Carried. See Resolver.FillMissingFromWindow.
	//
	// Skipped entirely on the resolved-follow-up path: res.Calls is already complete BY resolution,
	// and re-filling it would let a stale entity overwrite a freshly resolved one.
	if s.resolver != nil && len(res.Calls) == 0 {
		for i, call := range calls {
			filled, carried := s.resolver.FillMissingFromWindow(tools, window, call)
			calls[i] = filled
			res.Carried = append(res.Carried, carried...)
		}
	}

	// Step 4.5 (DIB-478): AMBIGUITY IS ASKED ABOUT, NOT GUESSED AT, on BOTH paths.
	//
	// "How much Blu have we got?" is a fresh question and is exactly as ambiguous as the same word
	// in a follow-up. Running it would hand back whichever product the catalogue's resolver happened
	// to rank first, with nothing on the screen saying a choice had been made. So the same check
	// runs here, once, for every call about to reach the registry.
	if s.resolver != nil {
		for _, call := range calls {
			if cl := s.resolver.CheckAmbiguousProduct(ctx, c, call); cl != nil {
				s.record(ctx, c, tokensIn, tokensOut)
				return Answer{Text: cl.Question, Clarify: cl, Results: []Result{}, Usage: s.usage(tokensIn, tokensOut)}, nil
			}
		}
	}

	// Step 5: run the calls, AS THE CALLER, THROUGH THE GATE. This is the unchanged security
	// argument: a resolved follow-up and a model's tool call arrive at the same Registry.Run with
	// the same Caller, so resolution can change what is asked and can never change what is allowed.
	var results []Result
	var ran []Invocation
	for _, call := range calls {
		out, runErr := s.reg.Run(ctx, c, call.Command, call.Args)
		if runErr != nil {
			// A failed call is TOLD TO THE PROVIDER as a tool message rather than aborting the
			// exchange, so the assistant can relay it in its own voice ("I need the order number").
			// The user's argument mistakes are the common case here and they are not faults, and a
			// REFUSAL travels this path too, which is how an RBAC refusal survives a conversation as
			// a plain sentence instead of a red box.
			msgs = append(msgs, Message{
				Role:     RoleTool,
				ToolName: call.Command,
				Text:     toolFailureText(runErr),
				Result:   &Result{Command: call.Command, Headline: toolFailureText(runErr), Empty: true},
			})
			continue
		}
		results = append(results, out)
		ran = append(ran, Invocation{Command: call.Command, Args: call.Args})
		msgs = append(msgs, Message{Role: RoleTool, ToolName: call.Command, Text: renderResult(out), Result: &out})
	}

	// Step 6: one round for the sentence. Bounded, see the file header. This is the ONLY place a
	// provider is offered the structured context, and it is offered it for PHRASING: the results are
	// already decided, already authorised, already formatted.
	text := ""
	final, err2 := s.complete(ctx, c, CompletionRequest{Messages: msgs, Tools: tools})
	if err2 != nil {
		// The commands already ran and the results are good. Losing the prose is not worth losing
		// the answer, so the headlines stand in for it, degraded, honest, and still useful.
		if s.log != nil {
			s.log.Warn("assist: provider failed on the summarising turn, answering from command results", "err", err2)
		}
		text = speak(results)
	} else {
		text = final.Text
		tokensIn += final.TokensIn
		tokensOut += final.TokensOut
	}

	// Step 7: the ledger row, always, including the stub's zeros.
	s.record(ctx, c, tokensIn, tokensOut)

	return Answer{
		Text:    text,
		Results: results,
		Ran:     ran,
		Carried: res.Carried,
		Usage:   s.usage(tokensIn, tokensOut),
	}, nil
}

// usage is the per-exchange cost report, built in one place so every early return in Ask carries the
// same shape. The handler decides who may READ it (the spend-visibility toggle); the service always
// computes it, because the ledger needs it either way.
func (s *Service) usage(in, out int) *Usage {
	return &Usage{Provider: s.provider.Name(), Model: s.provider.Model(), TokensIn: in, TokensOut: out}
}

// record appends the ledger row. A failure is logged and swallowed, see ledger.go for why the
// answer is not held hostage to the accounting.
func (s *Service) record(ctx context.Context, c Caller, in, out int) {
	if s.ledger == nil {
		return
	}
	err := s.ledger.Append(ctx, Entry{
		UserID:    c.UserID,
		RoleCode:  c.RoleCode,
		Provider:  s.provider.Name(),
		Model:     s.provider.Model(),
		TokensIn:  in,
		TokensOut: out,
		Kind:      KindChat,
		At:        s.now(),
	})
	if err != nil && s.log != nil {
		s.log.Error("assist: token ledger write failed, this exchange is not counted", "err", err, "user_id", c.UserID)
	}
}

// toolFailureText turns a command error into something the assistant can say. The named errors get
// their own sentence; a bad argument keeps the command's own wording, which was written for a human
// in the first place.
func toolFailureText(err error) string {
	switch {
	case errors.Is(err, ErrUnknownCommand):
		return "That isn't something I can look up."
	case errors.Is(err, ErrRefused), errors.Is(err, ErrDisabled):
		return "That isn't something I can look up for you."
	}
	var unavailable UnavailableError
	if errors.As(err, &unavailable) {
		return "That lookup isn't switched on in this system yet."
	}
	return err.Error()
}

// maxHistory bounds the conversation sent to a provider.
//
// Every message in the history is re-billed on every turn, so an unbounded history is a bill that
// grows quadratically with the length of a conversation, the single most common way a chat pilot
// produces a surprising invoice. Six turns is enough for "and what about the other branch?" and
// short enough that the cost of a long session stays flat.
const maxHistory = 6

func trimHistory(h []Message) []Message {
	if len(h) <= maxHistory {
		return append([]Message(nil), h...)
	}
	return append([]Message(nil), h[len(h)-maxHistory:]...)
}

// refusalAnswer turns a metering refusal into the ordinary answer the user sees. The provider was
// not reached for any of these, and none is an error to the handler.
func refusalAnswer(err error) (Answer, bool) {
	switch {
	case errors.Is(err, ErrOverBudget), errors.Is(err, ErrDealerCap):
		return Answer{Text: LimitReachedMessage, LimitReached: true, Results: []Result{}}, true
	case errors.Is(err, ErrMeterUnavailable):
		return Answer{Text: UnavailableMessage, Results: []Result{}}, true
	}
	return Answer{}, false
}
