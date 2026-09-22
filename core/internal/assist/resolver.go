package assist

// resolver.go, DIB-478. What "it" means, what "and last week?" means, and when to admit you
// don't know.
//
// -----------------------------------------------------------------------------------------------
// DETERMINISTIC-FIRST. THIS IS PLAIN CODE OVER STRUCTURED HISTORY.
// -----------------------------------------------------------------------------------------------
// Everything in this file is a pure function of (the window's INVOCATIONS, the sentence just typed,
// the caller's own catalogue/branch reads). No model is consulted to decide what a pronoun refers
// to. With the stub provider, the default, the one that ships enabled, every property below
// holds, and it holds identically on the day a real provider is switched on, because the provider
// is never asked this question.
//
// What a provider MAY be offered, within the existing rails (ledgered, budgeted, one bounded tool
// round), is the PHRASING of the answer. Never the referent. The difference matters: a wrong
// sentence is embarrassing and a wrong referent is a wrong number in front of a customer.
//
// -----------------------------------------------------------------------------------------------
// THE FOUR OUTCOMES, AND THE TWO THAT ARE USUALLY MISSING
// -----------------------------------------------------------------------------------------------
//
//	CALL        the follow-up resolved to one command with carried arguments.
//	COMPARE     it resolved to two, and the answer shows BOTH sides.
//	CLARIFY     it is genuinely ambiguous, so the assistant ASKS, with pickable options.  ← honest
//	NO REFERENT the window holds nothing "it" could mean, so the assistant SAYS SO.        ← honest
//
// The last two are the ones an assistant built to look clever does not have, and they are the two
// that decide whether anybody trusts the first two. A resolver that always resolves is a resolver
// that sometimes resolves WRONGLY and never says which time.
//
// -----------------------------------------------------------------------------------------------
// RESOLUTION CHANGES WHAT IS ASKED. IT NEVER CHANGES WHAT IS ALLOWED.
// -----------------------------------------------------------------------------------------------
// Every call this file produces is handed to Registry.Run with the Caller the request arrived with
//, the same door a button press goes through. The window is client-supplied and therefore
// untrusted: a hand-edited history naming a command the person may not run resolves to a call that
// is then REFUSED, in the app's own words. There is no path in this file that reaches a reader, and
// no path that constructs a Caller.
//
// The catalogue and branch reads this file DOES make (to tell a branch name from a product name)
// are themselves caller-scoped seams, so a scoped user cannot even learn the name of a yard they
// were not granted by asking the assistant to disambiguate it.

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ResolutionKind is which of the four outcomes this turn produced.
type ResolutionKind int

const (
	// ResolveNone: this is not a follow-up. The sentence stands on its own and the ordinary
	// provider path answers it. The resolver's most common and most important answer.
	ResolveNone ResolutionKind = iota
	ResolveCall
	ResolveCompare
	ResolveClarify
	ResolveNoReferent
)

// Resolution is what one turn resolved to.
type Resolution struct {
	Kind    ResolutionKind
	Calls   []ToolCall
	Clarify *Clarify
	// Refusal is the honest sentence for ResolveNoReferent. Written for a person, and it names what
	// was missing rather than saying "I don't understand".
	Refusal string
	// Carried names, in plain words, what was taken from earlier turns. It becomes the answer's
	// Note ("carried over: Brussels Block") so nobody has to guess which product the number is for
	//, the single most likely way a carried entity produces a misread answer is silently.
	Carried []string
}

// Resolver turns a follow-up into an invocation.
//
// It holds only READ seams and a clock. There is no state on it between calls, for the same reason
// StubProvider has none: anything remembered here would be remembered across users.
type Resolver struct {
	catalogue CatalogueReader
	branches  BranchReader
	now       func() time.Time
}

// NewResolver builds the resolver. Both seams may be nil, a deployment that has not wired the
// catalogue still resolves ranges, pronouns and intents, and simply cannot tell a branch name from
// a product name, which it handles by asking rather than by guessing (see classify).
func NewResolver(cat CatalogueReader, br BranchReader) *Resolver {
	return &Resolver{catalogue: cat, branches: br, now: time.Now}
}

// WithClock replaces the clock (tests, and the range arithmetic that depends on "today").
func (r *Resolver) WithClock(f func() time.Time) *Resolver { r.now = f; return r }

// -- the argument vocabulary --------------------------------------------------------------------
//
// These are the argument NAMES the commands use, gathered here because the resolver reasons about
// slots rather than about domains. A command that adds an argument outside this vocabulary is still
// runnable and still carried in the entity memory; it simply cannot be the target of a bare
// "what about X?", which is the correct conservative default.

const (
	argProduct  = "product"
	argCustomer = "customer"
	argBranch   = "branch"
	argFrom     = "from"
	argTo       = "to"
	argDate     = "date"
	argOrderNo  = "order_no"
	argQuoteNo  = "quote_no"
)

// entityKind is what a bare noun in a follow-up turned out to be.
type entityKind string

const (
	kindUnknown  entityKind = ""
	kindBranch   entityKind = argBranch
	kindProduct  entityKind = argProduct
	kindCustomer entityKind = argCustomer
)

// -- follow-up detection ------------------------------------------------------------------------

// followUpMode is the SHAPE of the elliptical sentence, decided before anything is looked up.
type followUpMode int

const (
	modeNone    followUpMode = iota
	modeSwap                 // "what about Kingston?", same question, different entity
	modeCompare              // "compare that to Trenton", both sides
	modeRange                // "and last week?", same question, shifted window
	modePronoun              // "when's it arriving?", new question, carried subject
)

// swapOpeners are the ways a person says "same question, different thing". Each is a PREFIX whose
// remainder is the new entity, which is why they are matched in longest-first order below.
var swapOpeners = []string{
	"what about ", "how about ", "and what about ", "what about the ", "and ", "what if ",
	"same for ", "same at ", "how about at ", "and at ", "what about at ", "and for ",
	"ok what about ", "okay what about ",
}

// compareOpeners introduce a two-sided answer.
var compareOpeners = []string{
	"compare that to ", "compare that with ", "compare to ", "compare with ", "compare that against ",
	"how does that compare to ", "how does that compare with ", "versus ", "vs ", "vs. ",
	"against ", "compared to ", "compared with ", "and compare to ",
}

// pronouns are the words that point BACKWARD and carry no content of their own.
var pronouns = []string{
	" it ", " it?", " it.", " it's", " its ", " that ", " that?", " that.", " those ", " those?",
	" them ", " them?", " them.", " they ", " there ", " there?", " this one", " the same",
	" theirs", " that one", " these",
}

// followUp classifies the sentence. It returns the mode and, for the two modes that name something,
// the TAIL, the words after the opener, which is the candidate entity.
//
// ORDER IS THE SPECIFICATION, as it is in the stub's intent table. Compare is checked before swap
// because "and compare to Trenton" contains "and " and means both sides, not a replacement. Range
// is checked before swap because "and last week?" also opens with "and " and names a window, not an
// entity. A sentence carrying a CONCRETE TARGET (an order number, a SKU, a quoted string) is never
// a follow-up: "and SO-4?" is a new lookup that happens to start with a conjunction.
func followUp(q string) (followUpMode, string) {
	raw := strings.TrimSpace(q)
	s := strings.ToLower(raw)
	s = strings.TrimRight(s, " ?!.")
	if s == "" {
		return modeNone, ""
	}

	for _, op := range compareOpeners {
		if tail, ok := afterOpener(raw, s, op); ok && tail != "" {
			return modeCompare, tail
		}
	}
	// A range shift is a sentence that is NOTHING BUT a date phrase. "and last week?", "last week?",
	// "what about last month", strip the phrase and the fillers and there is nothing left to ask.
	//
	// THE ELLIPSIS TEST IS THE WHOLE GUARD, and it was added because the obvious version (a date word
	// in a short sentence) claimed "what came in today?", a complete, self-contained question, and
	// answered it with "I don't have an earlier question to re-run". A follow-up is defined by what
	// it OMITS, so that is what is tested.
	if rw, ok := parseRangeWords(s); ok && rangeIsElliptical(s, rw) && !hasConcreteTarget(raw) {
		return modeRange, s
	}
	for _, op := range swapOpeners {
		if tail, ok := afterOpener(raw, s, op); ok && tail != "" && !hasConcreteTarget(tail) {
			return modeSwap, tail
		}
	}
	// A bare noun phrase: "Kingston?", no verb, no opener, one or two words, and a question mark or
	// nothing at all. Deliberately narrow: two bare words with no punctuation are far more often a
	// new question typed tersely than a follow-up, so the sentence must have ASKED something.
	if strings.HasSuffix(strings.TrimSpace(raw), "?") && len(strings.Fields(s)) <= 2 && !hasConcreteTarget(raw) && !containsVerbish(s) {
		return modeSwap, s
	}
	// A pronoun, or one of the ELLIPTICAL QUANTIFIERS that stand in for a subject the same way one
	// does: "when's MORE arriving?" names no product and is unmistakably about the last one.
	//
	// The length bound is what keeps this conservative. A long sentence containing "more" is a fresh
	// question with a word in it; a five-word one that also matches a pronoun intent is a follow-up.
	// A sentence carrying a concrete target is never either.
	if len(strings.Fields(s)) <= 8 && !hasConcreteTarget(raw) && containsAnyOf(padded(s), ellipticalSubjects) {
		return modePronoun, s
	}
	return modeNone, ""
}

// ellipticalSubjects are the pronouns plus the quantifiers that stand in for one. Kept as one list
// because they play exactly the same grammatical role here: they point backwards and carry nothing.
var ellipticalSubjects = append(append([]string{}, pronouns...),
	" more ", " more?", " any more", " the rest", " the next", " another",
)

// rangeFillers are the words that carry no question of their own, the scaffolding around an
// ellipsis. Everything else is content, and content means the sentence is asking something new.
var rangeFillers = map[string]bool{
	"and": true, "what": true, "about": true, "how": true, "ok": true, "okay": true,
	"so": true, "then": true, "just": true, "the": true, "for": true, "in": true,
	"also": true, "now": true, "that": true, "it": true, "same": true, "again": true,
	"me": true, "us": true, "show": true, "please": true,
}

// rangeIsElliptical reports whether the sentence is a bare date phrase wearing fillers.
func rangeIsElliptical(s string, rw rangeWord) bool {
	rest := s
	for _, w := range rw.words {
		rest = strings.ReplaceAll(rest, w, " ")
	}
	for _, tok := range strings.Fields(rest) {
		if !rangeFillers[strings.Trim(tok, "?!.,'")] {
			return false
		}
	}
	return true
}

// afterOpener finds an opener at the START of the sentence (or immediately after a leading filler
// like "ok, ") and returns the ORIGINAL-CASE remainder, case matters because the remainder is a
// product or customer name that will be echoed back.
func afterOpener(raw, lower, opener string) (string, bool) {
	if !strings.HasPrefix(lower, opener) {
		return "", false
	}
	tail := strings.TrimSpace(raw[len(opener):])
	tail = strings.TrimRight(tail, " ?!.")
	return tail, true
}

func padded(s string) string { return " " + s + " " }

func containsAnyOf(hay string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(hay, n) {
			return true
		}
	}
	return false
}

// verbish words mark a sentence as a fresh instruction rather than a bare noun.
var verbish = []string{"show", "find", "check", "list", "give", "tell", "look", "what", "who", "when", "how", "where", "why"}

func containsVerbish(s string) bool {
	for _, w := range strings.Fields(s) {
		for _, v := range verbish {
			if w == v {
				return true
			}
		}
	}
	return false
}

// -- the intent table for pronoun follow-ups ----------------------------------------------------
//
// "when's IT arriving" is a NEW question about an OLD subject. The verb decides the command; the
// window decides the subject. Each row names the command and the one argument that must be carried
// for the question to mean anything, and if that argument is not in the window, the honest answer
// is that there is nothing to carry.

type pronounIntent struct {
	words    []string
	command  string
	needsArg string
}

var pronounIntents = []pronounIntent{
	// DIB-494, FIRST, because its words are the most specific in this table: "who changed it?" and
	// "can you tell me the activity on it?" are trail questions whatever the window holds, and the
	// row below this one would otherwise claim "activity" for the ACCOUNT question. The general
	// account words ("been up to", "recent", "lately") stay with customer.activity, so "what have
	// they been up to?" is untouched.
	{[]string{"happened", "went wrong", "who changed", "changed", "audit", "last change",
		"activity on it", "activity on that", "history on it", "history on that"},
		"order.activity", argOrderNo},
	{[]string{"arriv", "on the way", "incoming", "coming in", "when's more", "when is more", "restock", "more due", "due in"},
		"stock.incoming", argProduct},
	{[]string{"in stock", "on hand", "how many", "stock", "available", "got any", "any left"},
		"stock.lookup", argProduct},
	{[]string{"cost", "price", "charge", "what do we sell", "sell it for", "selling"},
		"product.price", argProduct},
	{[]string{"owe", "owes", "balance", "outstanding", "arrears"},
		"customer.balance", argCustomer},
	{[]string{"invoice"}, "customer.invoices", argCustomer},
	{[]string{"order"}, "customer.orders", argCustomer},
	{[]string{"quote"}, "customer.quotes", argCustomer},
	{[]string{"hold", "credit hold", "stopped", "on hold"}, "ar.holds", ""},
	{[]string{"activity", "been up to", "recent", "lately"}, "customer.activity", argCustomer},
}

func pronounCommand(s string) (pronounIntent, bool) {
	for _, pi := range pronounIntents {
		for _, w := range pi.words {
			if strings.Contains(s, w) {
				return pi, true
			}
		}
	}
	return pronounIntent{}, false
}

// -- Resolve ------------------------------------------------------------------------------------

// Resolve decides what this turn is asking, given what the earlier turns asked.
//
// tools is the caller's OWN offered set (Registry.List for this caller). It is consulted so a
// follow-up never resolves to a command the person cannot run, they get the same "that isn't
// something I can look up for you" they would get for asking directly, rather than a refusal for a
// command they never named.
func (r *Resolver) Resolve(ctx context.Context, c Caller, tools []ToolSpec, w Window, question string) Resolution {
	// -- 1. a live clarifying question, answered ------------------------------------------------
	if cl, ok := w.PendingClarify(); ok {
		if opt, matched := cl.Match(question); matched {
			return Resolution{
				Kind:    ResolveCall,
				Calls:   []ToolCall{cl.Complete(opt.Value)},
				Carried: []string{"you picked " + opt.Label},
			}
		}
		// Not an answer to the clarify. The person changed the subject, which is allowed, and the
		// stale question is dropped rather than being forced onto the new sentence.
	}

	mode, tail := followUp(question)
	if mode == modeNone {
		return Resolution{Kind: ResolveNone}
	}

	prior, hasPrior := w.LastInvocation()
	ents := w.Entities()

	switch mode {
	case modeRange:
		return r.resolveRange(question, prior, hasPrior, tools)
	case modePronoun:
		return r.resolvePronoun(ctx, c, tools, question, ents)
	case modeSwap, modeCompare:
		return r.resolveSwap(ctx, c, tools, mode, tail, prior, hasPrior, ents)
	}
	return Resolution{Kind: ResolveNone}
}

// -- range follow-ups ---------------------------------------------------------------------------

// resolveRange re-runs the previous question over a different window.
//
// THE COMMAND MAY CHANGE, AND THAT IS THE POINT. "Payments today" is answered by the ROLLUP, which
// only knows about a day; "and last week?" is a range and is answered by the cash-receipts report.
// Carrying the intent means carrying the QUESTION ("how much money came in"), not the command code
// , so rangeSibling maps the day command onto its range counterpart, and the answer says which
// source it used rather than pretending they are one command.
func (r *Resolver) resolveRange(question string, prior Invocation, hasPrior bool, tools []ToolSpec) Resolution {
	if !hasPrior {
		return Resolution{
			Kind: ResolveNoReferent,
			Refusal: "I don't have an earlier question to re-run over " + strings.TrimSpace(strings.TrimRight(question, "?")) +
				". Ask me something first, for example \"what came in today?\", and then I can shift the dates.",
		}
	}
	rng, ok := parseRange(question, r.now())
	if !ok {
		return Resolution{Kind: ResolveNone}
	}
	cmd := prior.Command
	if sib, has := rangeSibling[cmd]; has {
		cmd = sib
	}
	if !offered(tools, cmd) {
		return Resolution{Kind: ResolveNone}
	}
	args := Args{}
	for k, v := range prior.Args {
		// The old window is replaced, not merged: "and last week?" must not leave last month's
		// end-date behind, which would silently widen the range nobody asked to widen.
		if k == argFrom || k == argTo || k == argDate {
			continue
		}
		args[k] = v
	}
	args[argFrom], args[argTo] = rng.From, rng.To
	return Resolution{
		Kind:    ResolveCall,
		Calls:   []ToolCall{{Command: cmd, Args: args}},
		Carried: []string{carriedPhrase(prior, cmd), rng.Label},
	}
}

// rangeSibling maps a day-shaped command onto the range-shaped command that answers the same
// question over more than a day.
var rangeSibling = map[string]string{
	"payments.today": "payments.range",
}

// -- pronoun follow-ups -------------------------------------------------------------------------

// resolvePronoun answers a NEW question about an OLD subject, and refuses honestly when there is no
// old subject to be about.
func (r *Resolver) resolvePronoun(ctx context.Context, c Caller, tools []ToolSpec, question string, ents map[string]string) Resolution {
	s := strings.ToLower(question)
	pi, ok := pronounCommand(s)
	if !ok {
		// A pronoun with no recognisable question in it ("what about it?") is not resolvable and is
		// not worth guessing at. Falling through to the ordinary path lets the provider say "I can't
		// answer that one yet", which is true.
		return Resolution{Kind: ResolveNone}
	}
	if !offered(tools, pi.command) {
		return Resolution{Kind: ResolveNone}
	}
	args := Args{}
	var carried []string
	if pi.needsArg != "" {
		v := ents[pi.needsArg]
		if strings.TrimSpace(v) == "" {
			return Resolution{Kind: ResolveNoReferent, Refusal: noReferentSentence(pi.needsArg)}
		}
		args[pi.needsArg] = v
		carried = append(carried, humanArg(pi.needsArg)+" "+v)
	}
	// A branch already in play narrows the new question too, "Brussels Block at Kingston" then
	// "when's it arriving?" means Kingston. Only when the command has somewhere to put it.
	if b := ents[argBranch]; b != "" && commandTakesArg(tools, pi.command, argBranch) {
		args[argBranch] = b
		carried = append(carried, "branch "+b)
	}
	return Resolution{Kind: ResolveCall, Calls: []ToolCall{{Command: pi.command, Args: args}}, Carried: carried}
}

// noReferentSentence is the honest refusal, per missing slot.
//
// IT NAMES WHAT IS MISSING AND WHAT WOULD FIX IT. "I don't know what you mean" is true and useless;
// "I don't know which product 'it' is, name one and I'll check" is the same admission with the next
// step attached, and it is the difference between an assistant that feels broken and one that feels
// careful. NOTHING IS INVENTED HERE: there is deliberately no fallback that picks the most recent
// product "probably", because a probably-right referent is the failure mode this whole file exists
// to prevent.
func noReferentSentence(arg string) string {
	switch arg {
	case argProduct:
		return "I'm not sure which product you mean, nothing earlier in this conversation names one. Tell me the product or SKU and I'll check."
	case argCustomer:
		return "I'm not sure which customer you mean, nothing earlier in this conversation names one. Give me a name or account number and I'll look."
	case argBranch:
		return "I'm not sure which branch you mean, nothing earlier in this conversation names one."
	case argOrderNo:
		return "I'm not sure which order you mean, nothing earlier in this conversation names one. Give me the number, like SO-4, and I'll pull up what's happened on it."
	default:
		return "I'm not sure what that refers to, nothing earlier in this conversation names it."
	}
}

func humanArg(arg string) string {
	switch arg {
	case argProduct:
		return "product"
	case argCustomer:
		return "customer"
	case argBranch:
		return "branch"
	case argOrderNo:
		return "order"
	default:
		return arg
	}
}

// -- entity swaps and comparisons ---------------------------------------------------------------

// resolveSwap re-runs the previous question with ONE entity replaced (modeSwap) or with both the old
// and the new one answered side by side (modeCompare).
func (r *Resolver) resolveSwap(ctx context.Context, c Caller, tools []ToolSpec, mode followUpMode, tail string, prior Invocation, hasPrior bool, ents map[string]string) Resolution {
	if !hasPrior {
		return Resolution{
			Kind:    ResolveNoReferent,
			Refusal: "I don't have an earlier question to apply " + quoteWord(tail) + " to. Ask me something first and then I can carry it across.",
		}
	}
	if !offered(tools, prior.Command) {
		return Resolution{Kind: ResolveNone}
	}
	slots := slotsFor(tools, prior.Command)
	if len(slots) == 0 {
		return Resolution{Kind: ResolveNone}
	}

	kind, value, cl := r.classify(ctx, c, tail, slots, prior)
	if cl != nil {
		return Resolution{Kind: ResolveClarify, Clarify: cl}
	}
	if kind == kindUnknown {
		return Resolution{
			Kind: ResolveNoReferent,
			Refusal: "I couldn't work out what " + quoteWord(tail) + " is, I checked the branches you can see and the catalogue and found nothing by that name. " +
				"If it's a customer, try the account name or number.",
		}
	}

	base := Args{}
	for k, v := range prior.Args {
		base[k] = v
	}
	swapped := Args{}
	for k, v := range base {
		swapped[k] = v
	}
	swapped[string(kind)] = value

	carried := []string{carriedPhrase(prior, prior.Command)}
	for _, s := range slots {
		if s == string(kind) {
			continue
		}
		if v := base[s]; v != "" {
			carried = append(carried, humanArg(s)+" "+v)
		}
	}
	_ = ents

	if mode == modeCompare {
		// BOTH SIDES, in the order the person said them: what they were looking at, then what they
		// asked to compare it with. A comparison that showed only the new side would be a swap with
		// a misleading name.
		return Resolution{
			Kind:    ResolveCompare,
			Calls:   []ToolCall{{Command: prior.Command, Args: base}, {Command: prior.Command, Args: swapped}},
			Carried: carried,
		}
	}
	return Resolution{Kind: ResolveCall, Calls: []ToolCall{{Command: prior.Command, Args: swapped}}, Carried: carried}
}

// slotsFor is which of the resolver's vocabulary arguments a command actually accepts, taken from
// the caller's OWN offered spec rather than from a table here, so a command that gains an argument
// becomes swappable with no change in this file, and one that loses an argument stops being.
func slotsFor(tools []ToolSpec, command string) []string {
	var out []string
	for _, t := range tools {
		if t.Name != command {
			continue
		}
		for _, a := range t.Args {
			switch a.Name {
			case argProduct, argCustomer, argBranch:
				out = append(out, a.Name)
			}
		}
	}
	sort.Strings(out)
	return out
}

func commandTakesArg(tools []ToolSpec, command, arg string) bool {
	for _, s := range slotsFor(tools, command) {
		if s == arg {
			return true
		}
	}
	return false
}

// classify decides WHICH KIND of thing a bare noun is, using the caller's own reads.
//
// THE ORDER IS BRANCH, THEN PRODUCT, THEN CUSTOMER, and it is not arbitrary. Branch names are a
// short closed set the caller is explicitly granted, so a match there is certain. Products are a
// ranked search that can match loosely. Customers are the loosest of the three (partial name
// matching), so they are the last resort, otherwise "Kingston" would resolve to a customer with
// "Kingston" in its trading name in preference to the yard of the same name, which is exactly
// backwards for a person standing in that yard.
//
// AMBIGUITY IS RETURNED, NOT RESOLVED. Two products, or a branch AND a product, produce a Clarify.
// The one case that does not is an EXACT hit: a term that is a product's exact SKU or exact name is
// that product, however many other things it also prefix-matches.
func (r *Resolver) classify(ctx context.Context, c Caller, term string, slots []string, prior Invocation) (entityKind, string, *Clarify) {
	term = strings.TrimSpace(term)
	if term == "" {
		return kindUnknown, "", nil
	}
	allows := func(k entityKind) bool {
		for _, s := range slots {
			if s == string(k) {
				return true
			}
		}
		return false
	}

	// A yard the caller can reach, named EXACTLY, is that yard and there is nothing to ask about.
	// A person standing in Kingston who says "Kingston" means Kingston, whatever else in the
	// catalogue happens to trigram-match those eight letters, and asking them to confirm it is the
	// dialog people learn to click through.
	var branchHit string
	var branchExact bool
	if allows(kindBranch) && r.branches != nil {
		if rows, err := r.branches.Branches(ctx, c); err == nil {
			for _, b := range rows {
				if strings.EqualFold(b.Name, term) || strings.EqualFold(b.Code, term) {
					branchHit, branchExact = b.Name, true
					break
				}
			}
			if branchHit == "" {
				for _, b := range rows {
					if containsFold(b.Name, term) {
						if branchHit != "" {
							branchHit = "" // two branches match loosely: not certain enough to use.
							break
						}
						branchHit = b.Name
					}
				}
			}
		}
	}
	if branchExact {
		return kindBranch, branchHit, nil
	}

	var hits []CatalogueHit
	if allows(kindProduct) && r.catalogue != nil {
		if rows, err := r.catalogue.FindProducts(ctx, c, term, maxClarifyOptions+1); err == nil {
			hits = rows
		}
	}
	// An exact SKU or exact name ends the question.
	for _, h := range hits {
		if strings.EqualFold(h.SKU, term) || strings.EqualFold(h.Name, term) {
			return kindProduct, h.SKU, nil
		}
	}

	switch {
	case branchHit != "" && len(hits) == 0:
		return kindBranch, branchHit, nil
	case branchHit == "" && len(hits) == 1:
		return kindProduct, skuOrName(hits[0]), nil
	case branchHit != "" && len(hits) >= 1:
		// A word that is both a yard and a product is genuinely ambiguous, and it is the one case a
		// human standing at the counter would also ask about.
		return kindUnknown, "", ambiguousBranchOrProduct(term, branchHit, hits, prior)
	case len(hits) > 1:
		return kindUnknown, "", ambiguousProduct(term, hits, prior)
	}

	if allows(kindCustomer) {
		// The customer arm is deliberately UNVERIFIED here: customer resolution is the command's own
		// first step (every account command resolves through the caller-fenced lookup and says "I
		// can't find a customer matching …" when it fails), so re-resolving it here would be a second
		// opinion about the same fence. The resolver's job is to decide WHICH SLOT the word goes in,
		// and having ruled out branch and product, customer is what is left.
		return kindCustomer, term, nil
	}
	return kindUnknown, "", nil
}

// ambiguousProduct builds the clarify for "two products match 'Blu'".
func ambiguousProduct(term string, hits []CatalogueHit, prior Invocation) *Clarify {
	n := min(len(hits), maxClarifyOptions)
	cl := &Clarify{
		Question: fmt.Sprintf("%d products match %q, which one?", len(hits), term),
		Command:  prior.Command,
		Args:     copyArgs(prior.Args),
		Arg:      argProduct,
	}
	for _, h := range hits[:n] {
		cl.Options = append(cl.Options, ClarifyOption{Label: labelFor(h), Value: skuOrName(h), Hint: matchReasonInWords(h.MatchReason, term)})
	}
	if len(hits) > n {
		cl.Question = fmt.Sprintf("Several products match %q, did you mean one of these?", term)
	}
	return cl
}

// matchReasonInWords turns the catalogue's internal match-reason code into the sentence a person
// picking between five near-identical slabs actually needs.
//
// THE CODES ARE THE COUNTER SEARCH'S OWN VOCABULARY (internal/product/counter_search.go): EXACT,
// SKU_PREFIX, NAME_PREFIX, NAME, DESCRIPTION, ALIAS, FUZZY. They exist to explain a RANKING to the
// code that does the ranking. Passing one straight through to a chat window put the literal string
// "NAME_PREFIX" under every option in walkthrough-478/06-clarify-keyboard-focus.png, under a
// question whose entire purpose is to help somebody choose.
//
// The hint is the only thing distinguishing five options whose labels all begin "Techo-Bloc", so it
// has to say something true and useful. An UNKNOWN code returns "" rather than inventing a
// description: no second line is better than a wrong one, and the label still identifies the product.
func matchReasonInWords(code, term string) string {
	q := strings.TrimSpace(term)
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "EXACT":
		return "exact match"
	case "SKU_PREFIX":
		return "SKU starts with " + strconv.Quote(q)
	case "NAME_PREFIX":
		return "name starts with " + strconv.Quote(q)
	case "NAME":
		return "name contains " + strconv.Quote(q)
	case "DESCRIPTION":
		return "described as " + strconv.Quote(q)
	case "ALIAS":
		return "a known alias for " + strconv.Quote(q)
	case "FUZZY":
		return "close spelling"
	default:
		return ""
	}
}

// ambiguousBranchOrProduct builds the clarify for a word that is both a yard and a product.
//
// It asks about the PRODUCT slot, because that is the slot whose wrong answer produces a wrong
// number: choosing the branch reading when the product was meant returns the previous product's
// figures at a new yard, which looks entirely plausible and is entirely wrong. The branch reading is
// offered as the first option and carries the branch name as its value with a marker the completer
// understands.
func ambiguousBranchOrProduct(term, branch string, hits []CatalogueHit, prior Invocation) *Clarify {
	cl := &Clarify{
		Question: fmt.Sprintf("%q is both a branch and a product here, which did you mean?", term),
		Command:  prior.Command,
		Args:     copyArgs(prior.Args),
		Arg:      argProduct,
	}
	// The branch reading keeps the product where it was and moves the branch; expressed as an option
	// that fills the product argument with the value it already had, plus the branch pinned into the
	// pending args. One argument, one question.
	cl.Args[argBranch] = branch
	cl.Options = append(cl.Options, ClarifyOption{
		Label: "The " + branch + " yard, same product, different branch",
		Value: prior.Args[argProduct],
		Hint:  "branch " + branch,
	})
	for _, h := range hits[:min(len(hits), maxClarifyOptions-1)] {
		cl.Options = append(cl.Options, ClarifyOption{Label: labelFor(h) + ", the product", Value: skuOrName(h), Hint: h.SKU})
	}
	return cl
}

// CheckAmbiguousProduct is the SAME clarify, raised for a FRESH question rather than a follow-up.
//
// "How much Blu have we got?" is not elliptical and resolves to stock.lookup{product:"Blu"} through
// the ordinary path, and 'Blu' is exactly as ambiguous there as it is in a follow-up. Running the
// command would hand back whichever product the catalogue's resolver happened to pick first, with no
// sign that a choice was made. So both paths ask the same question through this one function.
//
// It returns nil for everything that is NOT genuinely ambiguous: an exact SKU, a single hit, no
// hits at all (the command's own "I can't find a product matching …" is the better answer and it is
// already written), and any command with no product argument.
func (r *Resolver) CheckAmbiguousProduct(ctx context.Context, c Caller, call ToolCall) *Clarify {
	if r == nil || r.catalogue == nil {
		return nil
	}
	term := strings.TrimSpace(call.Args[argProduct])
	if term == "" || len(term) < 2 {
		return nil
	}
	// A SKU-shaped token is already unambiguous by construction, it is the catalogue's own key.
	if reSKU.MatchString(term) {
		return nil
	}
	hits, err := r.catalogue.FindProducts(ctx, c, term, maxClarifyOptions+1)
	if err != nil || len(hits) < 2 {
		return nil
	}
	for _, h := range hits {
		if strings.EqualFold(h.SKU, term) || strings.EqualFold(h.Name, term) {
			return nil
		}
	}
	return ambiguousProduct(term, hits, Invocation{Command: call.Command, Args: call.Args})
}

// FillMissingFromWindow completes a PROVIDER-ROUTED call from the conversation's entity memory.
//
// THE HOLE THIS CLOSES. Resolve() handles the ELLIPTICAL follow-up, "what about Kingston?", "and
// last week?", "when's it arriving?", sentences that are fragments and mean nothing on their own.
// A sentence like "what's coming in at Kingston?" is not a fragment: it is a complete, well-formed
// question, so it goes to the provider, which routes it correctly and fills the slots it can find.
// It cannot find a product, because the person did not repeat one, they had just asked about it.
//
// Before this, the command then refused for a missing required argument, two lines under an answer
// about that very product. Saying "it" worked and naming the yard did not, which is not a
// distinction anyone can be expected to learn.
//
// FOUR RULES, and each is the reason a looser version would be wrong:
//
//  1. REQUIRED SLOTS ONLY. An optional slot left empty is a deliberate breadth, "who owes us
//     money?" across every branch must not silently narrow to the branch mentioned four turns ago.
//  2. EMPTY SLOTS ONLY. A sentence that names its own subject has changed the subject, and memory
//     never overwrites what the person actually typed.
//  3. ONLY SLOTS THE COMMAND DECLARES. The vocabulary comes from the caller's OWN tool specs, so a
//     command cannot be handed an argument it never asked for.
//  4. IT MUST SAY SO. Every fill is returned as a Carried line, because a value the person did not
//     type, in an answer they will act on, is only safe if the screen names it.
//
// It grants nothing. The completed call goes to the same Registry.Run as every other, as the same
// caller, filling a slot can change what is ASKED and can never change what is ALLOWED.
func (r *Resolver) FillMissingFromWindow(tools []ToolSpec, w Window, call ToolCall) (ToolCall, []string) {
	if r == nil {
		return call, nil
	}
	spec, ok := specFor(tools, call.Command)
	if !ok {
		return call, nil
	}
	ents := w.Entities()
	if len(ents) == 0 {
		return call, nil
	}
	var carried []string
	for _, a := range spec.Args {
		if !a.Required {
			continue // rule 1
		}
		if strings.TrimSpace(call.Args[a.Name]) != "" {
			continue // rule 2
		}
		v, have := ents[a.Name]
		if !have || strings.TrimSpace(v) == "" {
			continue
		}
		if call.Args == nil {
			call.Args = Args{}
		}
		call.Args[a.Name] = v
		carried = append(carried, v) // rule 4
	}
	return call, carried
}

// specFor finds the caller's spec for a command. A command absent from the caller's own tool list is
// one they may not run, and this returns nothing for it rather than reasoning about it.
func specFor(tools []ToolSpec, command string) (ToolSpec, bool) {
	for _, t := range tools {
		if t.Name == command {
			return t, true
		}
	}
	return ToolSpec{}, false
}

// -- small helpers ------------------------------------------------------------------------------

func labelFor(h CatalogueHit) string {
	switch {
	case h.Name != "" && h.SKU != "":
		return h.Name + " (" + h.SKU + ")"
	case h.Name != "":
		return h.Name
	default:
		return h.SKU
	}
}

func skuOrName(h CatalogueHit) string {
	if strings.TrimSpace(h.SKU) != "" {
		return h.SKU
	}
	return h.Name
}

func copyArgs(a Args) Args {
	out := Args{}
	for k, v := range a {
		out[k] = v
	}
	return out
}

func containsFold(hay, needle string) bool {
	return strings.Contains(strings.ToLower(hay), strings.ToLower(needle))
}

func quoteWord(s string) string { return "\"" + strings.TrimSpace(s) + "\"" }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// carriedPhrase names the earlier question in the words of the command that answered it, so the
// note under a follow-up answer reads "same question as before" rather than a command code.
func carriedPhrase(prior Invocation, now string) string {
	if now != prior.Command {
		return "your earlier question about " + subjectOf(prior.Command)
	}
	return "your question about " + subjectOf(prior.Command)
}

func subjectOf(code string) string {
	switch {
	case strings.HasPrefix(code, "payment"):
		return "payments"
	case strings.HasPrefix(code, "stock"):
		return "stock"
	case strings.HasPrefix(code, "ar."):
		return "the AR ledger"
	case strings.HasPrefix(code, "quote"), strings.Contains(code, "quotes"):
		return "quotes"
	case strings.Contains(code, "order"):
		return "orders"
	case strings.Contains(code, "invoice"):
		return "invoices"
	case strings.HasPrefix(code, "product"):
		return "the catalogue"
	case strings.HasPrefix(code, "customer"):
		return "the account"
	default:
		return "that"
	}
}
