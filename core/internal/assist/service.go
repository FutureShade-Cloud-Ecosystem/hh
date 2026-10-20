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
//	6. ask the provider again, with the results, until it answers in words (bounded)
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
// THE LOOP IS BOUNDED, AT maxToolRounds. A question that needs a second lookup after the first
// one's result gets it; a model that keeps asking is stopped at the bound and the answer is built
// from the results already in hand. An unbounded tool loop is an unbounded bill, and the first
// thing a pilot with a token budget must not do is make its own budget unpredictable.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"strconv"
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

	// Notice is the one quiet note an answer may carry, `{code, class}`. This slice sends one code,
	// model_unavailable: a demo copy answered on the stub because the model could not be used (no
	// key, or a cap reached). The panel words it; the core sends no words of its own.
	Notice *Notice `json:"notice,omitempty"`

	// ConversationID is the opaque, server issued id of the conversation this exchange belongs to.
	// The client sends it back on the next turn instead of its own history. Absent when the bearer
	// has no session id or no store is wired.
	ConversationID string `json:"conversation_id,omitempty"`
}

// Notice is an answer's notice: a code, and a class where the code has classes.
type Notice struct {
	Code  string `json:"code"`
	Class string `json:"class,omitempty"`
}

// NoticeModelUnavailable is the notice of an answer given on the stub because the model could not
// be used (PANEL-API: "fell back to the stub or to commands only").
const NoticeModelUnavailable = "model_unavailable"

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

	// convs is the append only exchange log behind conversation_id. Nil means no server kept
	// conversations: every question is stateless.
	convs ConversationStore

	// router, when attached, chooses the provider PER REQUEST from the settings and the alias table.
	// Without one the service uses its single provider, which is what every test double wants.
	router *Router

	// Slice 2 metering (meter.go): the spend and price tables, the dealer wide cap, the rate
	// limits and the dealer's zone.
	spend     SpendStore
	prices    PriceStore
	dealerCap int64
	rates     *RateLimits
	zone      houseday.Source

	// resolver is DIB-478's conversational layer. It may be nil, a deployment with no resolver
	// answers every question as a fresh one, which is exactly the pre-478 behaviour, so the depth is
	// additive rather than load-bearing.
	resolver *Resolver

	// pinned, when set, is the alias the service always routes to, whatever the settings row says
	// (the demo's tanyai-premium).
	pinned string

	// stubFallback is the demo copy's rule: when the model cannot be used (no key, no dealer cap, a
	// cap reached, no price row) the question is answered on the stub and the answer says so,
	// instead of the plain unavailable or over limit answer other builds give.
	stubFallback bool

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

// WithConversations attaches the exchange log that makes conversation_id work.
func (s *Service) WithConversations(c ConversationStore) *Service { s.convs = c; return s }

// WithRouter attaches the alias router. From then on the provider is resolved on every provider
// call from the current settings, so a changed route or alias applies to the next request with no
// restart.
func (s *Service) WithRouter(r *Router) *Service { s.router = r; return s }

// WithResolver attaches DIB-478's conversational resolver.
//
// SEPARATE FROM NewService, deliberately. The resolver needs caller-scoped catalogue and branch
// reads that a minimal deployment may not have wired, and a constructor that required them would
// make the whole assistant refuse to start over an optional feature. Nil resolver = every question
// answered as a fresh one.
func (s *Service) WithResolver(r *Resolver) *Service { s.resolver = r; return s }

// WithPinnedRoute fixes the route to one alias: every settings read reports the native provider
// through that alias, so the settings table cannot send the service anywhere else. The demo
// composition pins tanyai-premium; the pin is the composition's and no request carries it.
func (s *Service) WithPinnedRoute(alias string) *Service { s.pinned = alias; return s }

// PinnedRoute is the alias the route is pinned to, or empty when it is not.
func (s *Service) PinnedRoute() string { return s.pinned }

// StubFallbackOn reports whether the demo's stub fallback is switched on.
func (s *Service) StubFallbackOn() bool { return s != nil && s.stubFallback }

// WithStubFallback switches on the demo copy's rule: a paid call refused by the meter for a cap,
// a missing cap or a missing price row is answered on the stub with the model_unavailable notice,
// and so is any exchange the stub answered because no key or route was usable. A user's own
// allowance running out is still the plain limit answer. Set on a demo copy only.
func (s *Service) WithStubFallback() *Service { s.stubFallback = true; return s }

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

// Settings resolves the config, defaulting on any failure. A pinned route (the demo's) overrides
// the provider, model and route the table holds.
func (s *Service) Settings(ctx context.Context) Settings {
	set := s.readSettings(ctx)
	if s.pinned != "" {
		set.Provider, set.Model, set.Route = ProviderAnthropic, s.pinned, ""
	}
	return set
}

func (s *Service) readSettings(ctx context.Context) Settings {
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
// estimate, in the dealer's month; the older token ledger is the fallback of a deployment without it.
func (s *Service) MonthSpend(ctx context.Context, userID string) (Spend, error) {
	from, to, err := s.monthWindow(ctx)
	if err != nil {
		return Spend{}, err
	}
	if s.spend != nil {
		return s.spend.UserMonth(ctx, userID, from, to)
	}
	if s.ledger == nil {
		return Spend{}, nil
	}
	return s.ledger.SpendFor(ctx, userID, from, to)
}

// Ask answers one question as the caller. See the file header for the seven steps.
func (s *Service) Ask(ctx context.Context, c Caller, question string, history []Message) (Answer, error) {
	if !c.HasRole() {
		// No assigned role: no commands, so no useful conversation. Refused before any spend.
		return Answer{}, ErrRefused
	}
	// One exchange id for every provider call this question makes (retries and escalations included).
	ctx = withExchange(ctx)
	cfg := s.Settings(ctx)
	if !cfg.ChatEnabled {
		return Answer{Text: ChatDisabledMessage}, nil
	}

	// Slice 2: the rate limit, before the budget and before anything that can cost. A refused
	// request charges nothing and is an ordinary answer.
	if err := s.admit(ctx, c); err != nil {
		return Answer{Text: RateLimitedMessage, Results: []Result{}}, nil
	}

	id := s.identityFor(ctx, cfg)

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
			// answer, and no provider is reached.
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
		s.record(ctx, c, id, 0, 0)
		return Answer{Text: res.Clarify.Question, Clarify: res.Clarify, Results: []Result{}, Ran: nil, Usage: usageOf(id, 0, 0)}, nil
	case ResolveNoReferent:
		// NEVER FABRICATE. The window holds no referent, so the assistant says so plainly instead of
		// picking the most recent plausible thing and sounding certain about it.
		s.record(ctx, c, id, 0, 0)
		return Answer{Text: res.Refusal, Results: []Result{}, Usage: usageOf(id, 0, 0)}, nil
	}

	tag := newExchangeTag()
	msgs := append([]Message{{Role: RoleSystem, Text: systemPromptFor(tag)}}, sanitizeHistory(history)...)
	run := &askRun{tag: tag, msgs: append(msgs, Message{Role: RoleUser, Text: question})}

	if len(res.Calls) > 0 {
		// A RESOLVED FOLLOW-UP SKIPS THE PROVIDER'S FIRST ROUND. The resolver already decided which
		// command and which arguments; asking a model to decide it again would be paying for a second
		// opinion about a question that has a deterministic answer, and taking that opinion would put
		// the referent back in the model's hands. The calls are written into the conversation as an
		// assistant turn with ids the service issued, so the provider sees a legal round trip.
		calls, cl := s.preflight(ctx, c, tools, window, &res, res.Calls, false)
		if cl != nil {
			s.record(ctx, c, id, 0, 0)
			return Answer{Text: cl.Question, Clarify: cl, Results: []Result{}, Usage: usageOf(id, 0, 0)}, nil
		}
		s.exec(ctx, c, run, "", calls)
	}

	// Steps 4 to 6: the model rounds. The provider is offered the caller's tools; whatever it asks
	// for runs through the gate; its next reply sees the results. Bounded, see maxToolRounds.
	out, err := s.converse(ctx, c, cfg, tools, window, &res, run)
	if err != nil && s.stubFallback && paidCallRefused(err) {
		// The demo's rule: the model cannot be used, so the stub answers and the answer says so.
		out, err = s.stubOutcome(ctx, c, tools, window, &res, run)
	}
	if err != nil {
		// A refusal by the meter (budget, dealer cap, unrecordable spend) is an ordinary answer.
		if ans, ok := refusalAnswer(err); ok {
			return ans, nil
		}
		return Answer{}, err
	}
	if out.clarify != nil {
		s.record(ctx, c, run.ident(id), run.in, run.out)
		return Answer{Text: out.clarify.Question, Clarify: out.clarify, Results: []Result{}, Usage: usageOf(run.ident(id), run.in, run.out)}, nil
	}
	if run.executed == 0 {
		// Prose with no lookups: the capability answer, the "I can't answer that one yet", the
		// menu. Nothing ran, so nothing is recorded as having run.
		s.record(ctx, c, run.ident(id), run.in, run.out)
		return Answer{
			Text:    out.text,
			Results: []Result{},
			Suggest: out.suggest,
			Usage:   usageOf(run.ident(id), run.in, run.out),
			Notice:  s.fallbackNotice(run.ident(id)),
		}, nil
	}

	// Step 7: the ledger row, always, including the stub's zeros.
	s.record(ctx, c, run.ident(id), run.in, run.out)

	return Answer{
		Text:    out.text,
		Results: run.results,
		Ran:     run.ran,
		Carried: res.Carried,
		Usage:   usageOf(run.ident(id), run.in, run.out),
		Notice:  s.fallbackNotice(run.ident(id)),
	}, nil
}

// paidCallRefused reports a metering refusal that stops a paid call and is not the asker's own
// allowance: the dealer cap, or a call that could not be priced or recorded.
func paidCallRefused(err error) bool {
	return errors.Is(err, ErrDealerCap) || errors.Is(err, ErrMeterUnavailable)
}

// fallbackNotice is the model_unavailable notice for an exchange the stub answered on a demo copy.
func (s *Service) fallbackNotice(who ident) *Notice {
	if s.stubFallback && who.provider == "stub" {
		return &Notice{Code: NoticeModelUnavailable}
	}
	return nil
}

// newExchangeTag is the random tag of one exchange's data envelope: 64 bits from the system's
// random source, so a record written before the exchange cannot contain the closing marker.
func newExchangeTag() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// The system source failing is not survivable for a boundary that depends on it.
		panic("assist: no randomness for the data envelope tag: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// envelope wraps a tool result for the model: a labelled data block with the exchange's tag. See
// systemPromptFor for the rule the model is given about what is inside.
func envelope(tag, command, body string) string {
	return "[[DATA " + tag + " " + command + "]]\n" + body + "\n[[END DATA " + tag + "]]"
}

// askRun is the state of one Ask across its rounds: the conversation as it will be sent, what the
// commands returned, and what the provider calls cost.
type askRun struct {
	msgs     []Message
	results  []Result
	ran      []Invocation
	in, out  int
	cached   int
	calls    int // provider calls made so far
	executed int // tool calls dispatched through the registry, failed ones included
	nextID   int
	filled   bool
	// alias is the route entry the next provider call is for; note is OUR text naming the check the
	// last reply failed, sent once with the retry.
	// tag is this exchange's random envelope tag.
	tag       string
	alias     string
	note      string
	answerNow bool
	// provider and model are who answered last, for the ledger row and the usage report.
	provider, model string
}

// ident is the provider identity an exchange is recorded under.
type ident struct{ provider, model string }

func (r *askRun) ident(fallback ident) ident {
	if r.provider == "" {
		return fallback
	}
	return ident{provider: r.provider, model: r.model}
}

// outcome is what one pass over the rounds produced.
type outcome struct {
	text    string
	suggest []string
	clarify *Clarify
	// fail is set when the pass stopped on a failed check; it holds OUR text naming the check.
	fail string
}

// issueID makes an id for a call the service decided itself.
func (r *askRun) issueID() string {
	r.nextID++
	return "call_" + strconv.Itoa(r.nextID)
}

// preflight applies the two checks that sit between a set of calls and the registry: slots the
// sentence left empty are filled from the window (model calls only), and an ambiguous product is
// asked about rather than guessed at.
func (s *Service) preflight(ctx context.Context, c Caller, tools []ToolSpec, window Window, res *Resolution, calls []ToolCall, fill bool) ([]ToolCall, *Clarify) {
	calls = append([]ToolCall(nil), calls...)
	if s.resolver == nil {
		return calls, nil
	}
	if fill {
		// Only REQUIRED slots, only EMPTY ones, and every fill is declared in Carried. See
		// Resolver.FillMissingFromWindow.
		for i, call := range calls {
			filled, carried := s.resolver.FillMissingFromWindow(tools, window, call)
			calls[i] = filled
			res.Carried = append(res.Carried, carried...)
		}
	}
	// "How much Blu have we got?" is a fresh question and is exactly as ambiguous as the same word in
	// a follow-up. Running it would hand back whichever product the catalogue's resolver happened to
	// rank first, with nothing on the screen saying a choice had been made.
	for _, call := range calls {
		if cl := s.resolver.CheckAmbiguousProduct(ctx, c, call); cl != nil {
			return calls, cl
		}
	}
	return calls, nil
}

// exec runs calls AS THE CALLER, THROUGH THE GATE, and writes the assistant turn and the tool turns
// into the conversation with matching ids. This is the unchanged security argument: a resolved
// follow-up and a model's tool call arrive at the same Registry.Run with the same Caller, so
// resolution can change what is asked and can never change what is allowed.
func (s *Service) exec(ctx context.Context, c Caller, run *askRun, text string, calls []ToolCall) {
	issued := make([]ToolCall, len(calls))
	for i, call := range calls {
		if call.ID == "" {
			call.ID = run.issueID()
		}
		issued[i] = call
	}
	run.msgs = append(run.msgs, Message{Role: RoleAssistant, Text: text, ToolCalls: issued})
	for _, call := range issued {
		run.executed++
		out, runErr := s.reg.Run(ctx, c, call.Command, call.Args)
		if runErr != nil {
			// A failed call is TOLD TO THE PROVIDER as a tool message rather than aborting the
			// exchange, so the assistant can relay it in its own voice ("I need the order number").
			// The user's argument mistakes are the common case here and they are not faults, and a
			// REFUSAL travels this path too, which is how an RBAC refusal survives a conversation as
			// a plain sentence instead of a red box.
			msg := toolFailureText(runErr)
			run.msgs = append(run.msgs, Message{
				Role:       RoleTool,
				ToolCallID: call.ID,
				ToolName:   call.Command,
				Text:       envelope(run.tag, call.Command, msg),
				Result:     &Result{Command: call.Command, Headline: msg, Empty: true},
			})
			continue
		}
		run.results = append(run.results, out)
		run.ran = append(run.ran, Invocation{Command: call.Command, Args: call.Args})
		run.msgs = append(run.msgs, Message{Role: RoleTool, ToolCallID: call.ID, ToolName: call.Command, Text: envelope(run.tag, call.Command, renderResult(out)), Result: &out})
	}
}

// drive is the bounded multi round loop: ask, run what was asked, ask again with the results.
func (s *Service) drive(ctx context.Context, c Caller, tools []ToolSpec, window Window, res *Resolution, run *askRun) (outcome, error) {
	limit := maxToolRounds
	if run.answerNow {
		// The bound was already reached once and the results are in hand: an attempt after that is
		// one call to answer in words, not another loop.
		limit = 1
	}
	for round := 0; round < limit; round++ {
		reply, err := s.complete(ctx, c, CompletionRequest{Alias: run.alias, Messages: run.request(), Tools: tools})
		run.note = ""
		run.calls++
		run.in += reply.TokensIn
		run.out += reply.TokensOut
		run.cached += reply.TokensCached
		if reply.Provider != "" {
			run.provider, run.model = reply.Provider, reply.Model
		}
		if err != nil {
			if run.executed == 0 {
				return outcome{}, err
			}
			// The commands already ran and the results are good. Losing the prose is not worth
			// losing the answer, so the headlines stand in for it.
			if s.log != nil {
				s.log.Warn("assist: provider failed on a later turn, answering from command results", "err", err)
			}
			return outcome{text: speak(run.results)}, nil
		}
		if len(reply.ToolCalls) == 0 {
			o := outcome{text: reply.Text}
			if run.executed == 0 {
				// Suggest rides along UNCHANGED from the provider. The service does not decide what
				// to offer, the provider knows which tools it was handed, and this path is the only
				// one that can produce a menu-shaped reply.
				o.suggest = reply.Suggest
			}
			return o, nil
		}
		if fail := checkCalls(tools, reply.ToolCalls); fail != "" {
			return outcome{fail: fail}, nil
		}
		calls, cl := s.preflight(ctx, c, tools, window, res, reply.ToolCalls, !run.filled && len(res.Calls) == 0)
		run.filled = true
		if cl != nil {
			return outcome{clarify: cl}, nil
		}
		s.exec(ctx, c, run, reply.Text, calls)
	}
	return outcome{fail: failBound}, nil
}

// The validator's texts. They are OURS: fixed sentences that name the check, never a model's words
// and never a record's, because they go back to a model and anything of a record's in them would be
// a way to carry an instruction.
const (
	failUnknownCommand = "Your last reply was rejected: it called a command that is not in your tool list. Call only the tools you were given, or answer in words."
	failBadArguments   = "Your last reply was rejected: a tool call's arguments were not one valid JSON object. Send the arguments as a single JSON object."
	failBound          = "The lookup loop reached its limit. Do not call more tools: answer now in words from the results you already have."
)

// checkCalls is the check available in this slice: every call names a command in the asker's own
// list, and every call's arguments were valid JSON. "" means the calls stand.
func checkCalls(tools []ToolSpec, calls []ToolCall) string {
	for _, call := range calls {
		if !offered(tools, call.Command) {
			return failUnknownCommand
		}
		if call.ArgsErr != "" {
			return failBadArguments
		}
	}
	return ""
}

// request is the conversation as the next provider call sees it: the messages, plus the validator's
// note when the last reply failed a check.
func (r *askRun) request() []Message {
	if r.note == "" {
		return r.msgs
	}
	return append(append([]Message(nil), r.msgs...), Message{Role: RoleUser, Text: r.note})
}

// maxProviderCalls bounds the provider calls of ONE Ask across every retry and every alias, so a
// chain of failed checks is a bounded bill.
const maxProviderCalls = 12

// aliasStubFallback is the pseudo alias of the last step of escalation: the deterministic stub.
const aliasStubFallback = "stub-fallback"

// converse runs the rounds with escalation on a failed check: one retry on the same alias with the
// validator's text, then the next alias of the route that is really configured, then the stub's
// plain results. Every attempt is a Service.complete call, so it is metered like any other, and the
// Caller is the asker's on every one of them: escalation changes who is asked, never who is asking.
func (s *Service) converse(ctx context.Context, c Caller, cfg Settings, tools []ToolSpec, window Window, res *Resolution, run *askRun) (outcome, error) {
	for _, alias := range s.escalationRoute(ctx, cfg) {
		for attempt := 0; attempt < 2; attempt++ {
			if run.calls >= maxProviderCalls {
				break
			}
			run.alias = alias
			out, err := s.drive(ctx, c, tools, window, res, run)
			if err != nil || out.fail == "" {
				return out, err
			}
			run.note = out.fail
			run.answerNow = run.answerNow || out.fail == failBound
			if s.log != nil {
				s.log.Warn("assist: a reply failed a check, escalating", "alias", alias, "attempt", attempt+1)
			}
		}
	}
	// Every alias failed: the stub's plain results.
	return s.stubOutcome(ctx, c, tools, window, res, run)
}

// stubOutcome is the last step of every route: the deterministic stub answers. The commands that
// already ran are good, so they stand; the stub answers the question itself when nothing ran yet.
func (s *Service) stubOutcome(ctx context.Context, c Caller, tools []ToolSpec, window Window, res *Resolution, run *askRun) (outcome, error) {
	run.alias, run.note = aliasStubFallback, ""
	out, err := s.drive(ctx, c, tools, window, res, run)
	if err != nil || out.fail != "" {
		if len(run.results) == 0 && err != nil {
			return outcome{}, err
		}
		return outcome{text: speak(run.results)}, nil
	}
	return out, nil
}

// escalationRoute is the aliases to try, in order: those of the current route that resolve to a
// real provider. With no router, or nothing configured, it is the single default entry (the base
// provider, or the stub), which still gets its one retry.
func (s *Service) escalationRoute(ctx context.Context, cfg Settings) []string {
	if s.router == nil {
		return []string{""}
	}
	var out []string
	for _, a := range cfg.RouteAliases() {
		if _, ok := s.router.Resolve(ctx, a); ok {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// complete is the ONE place a provider is called. Every attempt, retry and escalation goes through
// it, so whatever wraps it (metering, caps) sees every provider call and nothing bypasses it. The
// provider is resolved here, per call, from the current settings and alias table.
func (s *Service) complete(ctx context.Context, c Caller, req CompletionRequest) (Reply, error) {
	p := s.providerFor(ctx, req.Alias)
	reply, err := s.meteredComplete(ctx, c, p, s.priceAlias(ctx, req.Alias, p), req)
	reply.Provider, reply.Model = p.Name(), p.Model()
	return reply, err
}

// priceAlias is the alias row a call is priced and recorded under: the alias named by the request,
// else the route's first entry; with no router, the single provider's own name. The stub is its own.
func (s *Service) priceAlias(ctx context.Context, alias string, p Provider) string {
	if p.Name() == "stub" {
		return "stub"
	}
	if s.router == nil {
		return p.Name()
	}
	if alias == "" {
		if route := s.Settings(ctx).RouteAliases(); len(route) > 0 {
			alias = route[0]
		}
	}
	return alias
}

// providerFor resolves the provider for an alias; an empty alias means the route's first entry.
func (s *Service) providerFor(ctx context.Context, alias string) Provider {
	if alias == aliasStubFallback {
		if s.router != nil {
			p, _ := s.router.Resolve(ctx, aliasStubFallback)
			return p
		}
		return NewStubProvider()
	}
	if s.router == nil {
		return s.provider
	}
	if alias == "" {
		if route := s.Settings(ctx).RouteAliases(); len(route) > 0 {
			alias = route[0]
		}
	}
	p, _ := s.router.Resolve(ctx, alias)
	return p
}

// identityFor is the provider identity an exchange that never reaches a provider is recorded under.
func (s *Service) identityFor(ctx context.Context, cfg Settings) ident {
	if s.router == nil {
		return ident{s.provider.Name(), s.provider.Model()}
	}
	alias := ""
	if route := cfg.RouteAliases(); len(route) > 0 {
		alias = route[0]
	}
	p, _ := s.router.Resolve(ctx, alias)
	return ident{p.Name(), p.Model()}
}

// usage is the per-exchange cost report, built in one place so every early return in Ask carries the
// same shape. The handler decides who may READ it (the spend-visibility toggle); the service always
// computes it, because the ledger needs it either way.
func usageOf(id ident, in, out int) *Usage {
	return &Usage{Provider: id.provider, Model: id.model, TokensIn: in, TokensOut: out}
}

// record appends the ledger row. A failure is logged and swallowed, see ledger.go for why the
// answer is not held hostage to the accounting.
func (s *Service) record(ctx context.Context, c Caller, id ident, in, out int) {
	if s.ledger == nil {
		return
	}
	err := s.ledger.Append(ctx, Entry{
		UserID:    c.UserID,
		RoleCode:  c.RoleCode,
		Provider:  id.provider,
		Model:     id.model,
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

// sanitizeHistory is what of a history reaches a provider: the last maxHistory turns, user and
// assistant text only. System and tool turns are the service's to write, never a client's, and a
// turn with no words says nothing a provider could use.
func sanitizeHistory(h []Message) []Message {
	var out []Message
	for _, m := range trimHistory(h) {
		if (m.Role != RoleUser && m.Role != RoleAssistant) || strings.TrimSpace(m.Text) == "" {
			continue
		}
		out = append(out, Message{Role: m.Role, Text: m.Text})
	}
	return out
}

// maxToolRounds bounds the provider round trips of one attempt: a call that asks for tools is one
// round. Over the bound the loop stops and the answer is built from the results already in hand.
const maxToolRounds = 4
