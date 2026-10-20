package assist

// stub_provider.go, the DEFAULT provider: intent parsing by rule, answers by template.
//
// This is not a placeholder. It is the provider the pilot ships enabled, the one every test in this
// package runs against, and the one a demo on a plane runs against. It needs no API key, makes no
// network call, and given the same question returns the same answer on every machine forever.
//
// WHAT IT CAN AND CANNOT DO, SAID PLAINLY
// ---------------------------------------
// It recognises the five questions the commands answer, in the words people actually use for them,
// and it pulls the obvious argument out of the sentence. It does not understand English. Ask it
// something outside the five and it says so and lists what it CAN do, which is a better failure
// than a language model's, because it is the same failure every time and a user learns the shape of
// the tool in one try instead of by folklore.
//
// WHY RULES AND NOT A TINY MODEL. The requirement was "works forever with zero external keys". Any
// statistical answer to intent classification is a dependency on weights, a licence, and a
// non-determinism that turns every test in this package into a flake. The rules below are ordered
// most-specific-first and each one is a sentence you can read out loud.
//
// ORDERING IS THE WHOLE PARSER. "How many pavers did Northgate order?" contains both "order" and a
// customer; "what's Northgate's balance" contains a customer and no verb. The matcher below runs
// the discriminating patterns before the ambiguous ones and stops at the first hit, so the ordering
// of the rule table IS the specification, and reordering it is a behaviour change that the tests
// will catch.
//
// TOKENS: ZERO, AND STILL RECORDED. The stub reports 0 in and 0 out because it consumed nothing.
// The service still writes the ledger row (service.go), counting EXCHANGES is the measurement the
// pilot actually needs, and a counter that starts working only when a real provider is switched on
// is a counter nobody can validate before switching it on.

import (
	"context"
	"regexp"
	"strings"
	"time"
)

// StubProvider is the deterministic provider. It holds no state between calls, which is what makes
// it safe to share across users: there is nothing in it that could leak from one conversation into
// another.
type StubProvider struct {
	// now is the clock the DATE-SHAPED intents resolve against, "payments today", "takings last
	// week". Injected rather than read at the call site so the parser stays testable without waiting
	// for tomorrow, and so a test can pin a Tuesday and assert what "last week" became.
	//
	// It is the ONE piece of state on this type and it is not per-conversation, so the "holds nothing
	// between calls, therefore safe to share across users" property is unchanged.
	now func() time.Time
}

// NewStubProvider builds the default provider.
func NewStubProvider() *StubProvider { return &StubProvider{now: time.Now} }

// WithClock replaces the clock (tests, and any caller that wants the house day rather than UTC).
func (p *StubProvider) WithClock(f func() time.Time) *StubProvider { p.now = f; return p }

func (p *StubProvider) clock() time.Time {
	if p.now == nil {
		return time.Now()
	}
	return p.now()
}

func (*StubProvider) Name() string  { return "stub" }
func (*StubProvider) Model() string { return "hos-stub-1" }

// Stream is declared and not implemented.
func (*StubProvider) Stream(context.Context, CompletionRequest) (<-chan StreamEvent, error) {
	return nil, ErrStreamingUnsupported
}

// Complete implements the two-phase protocol: a tool call on the first pass, a sentence on the
// second (once the service has handed back results).
func (p *StubProvider) Complete(_ context.Context, req CompletionRequest) (Reply, error) {
	if results := toolResults(req.Messages); len(results) > 0 {
		return Reply{Text: speak(results)}, nil
	}
	q := lastUserText(req.Messages)
	if strings.TrimSpace(q) == "" {
		return helpReply(req.Tools), nil
	}
	// DIB-465: "what can you do?" / "can you do X?" is a question ABOUT the tool, and it is answered
	// from the tool set this caller was offered, see capability.go.
	//
	// BEFORE parseIntent, because several of those questions contain the keywords the intent table
	// matches on ("can you check a customer's balance" is a balanceWords hit) and running the command
	// answers a question nobody asked, with a demand for an argument. AFTER the concrete-target
	// check, because "can you check stock for TB-BLU60?" is polite phrasing for DO IT, and describing
	// the stock command instead of running it would be the same failure in the other direction.
	// AND (DIB-494) after the PRONOUN-target check, for the same reason one referent to the left:
	// "can you tell me the activity on it?" is a request about the thing the conversation just
	// named, not a question about the tool.
	if cq, ok := parseCapabilityQuestion(q); ok && !hasConcreteTarget(q) && !hasPronounTarget(q) {
		return capabilityReply(cq, req.Tools), nil
	}
	call, ok := parseIntent(q, p.clock())
	if !ok {
		return cannotAnswerReply(q, req.Tools), nil
	}
	// A tool the caller may not run is not offered, and asking for it anyway would earn an
	// ErrRefused from the registry. The stub checks the offered set itself so the user gets the
	// helpful "here's what I can do" instead of a refusal for a command they never named.
	if !offered(req.Tools, call.Command) {
		return notAvailableToYouReply(call.Command, req.Tools), nil
	}
	return Reply{ToolCalls: []ToolCall{call}}, nil
}

// -- phase 2: speaking the results --------------------------------------------------------------

// toolResults collects the results appended since the last user turn.
func toolResults(msgs []Message) []Result {
	var out []Result
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role == RoleUser {
			break
		}
		if m.Role == RoleTool && m.Result != nil {
			out = append([]Result{*m.Result}, out...)
		}
	}
	return out
}

// speak renders results as the assistant's reply TO A PERSON.
//
// The headline is used VERBATIM. The command layer already wrote a plain-English sentence with the
// numbers formatted once (commands.go, format.go); re-phrasing it here would be a second voice for
// the same fact and the two would eventually disagree. The stub's job is to join, not to author.
//
// HEADLINES AND NOTES ONLY, NOT renderResult(). The two renderers look interchangeable and are not,
// and using the wrong one is a defect you can only see in a screenshot:
//
//	renderResult  is for a MODEL. It dumps every field of every item, because a provider composing
//	              a sentence needs the data and cannot see the screen.
//	speak         is for a USER, whose screen ALREADY shows those items as a table, Answer.Results
//	              travels beside the prose precisely so the numbers are never trapped in a sentence.
//
// Calling renderResult here put the same record on the screen twice: once flattened into the
// assistant's speech ("- SO-4: Status PICKING, Customer ..., Total ...") and again in the result
// card directly beneath it. A note IS carried, because it is a caveat about the answer rather than a
// repetition of it, and dropping it would be the one thing worse than repeating it.
func speak(rs []Result) string {
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		s := r.Headline
		if r.Note != "" {
			s += "\n(" + r.Note + ")"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "\n\n")
}

// -- phase 1: intent ----------------------------------------------------------------------------

var (
	// An order number as people write it: "#SO-4", "SO-4", "so 4". The '#' is optional because half
	// the app prints it and half does not.
	reOrderNo = regexp.MustCompile(`(?i)#?\b((?:so|ord)[- ]?\d+)\b`)
	// A payment reference: PMT-1042, REF-88, or a bare gateway-looking token with a hyphen.
	rePaymentRef = regexp.MustCompile(`(?i)#?\b((?:pmt|pay|ref|rcpt)[- ]?\d+)\b`)
	// Money: "$1,240.00", "1240.00", "1,240". Requires either a $ or a decimal part so a bare "60"
	// in "Blu 60" is never read as an amount.
	reAmount = regexp.MustCompile(`\$\s?[\d,]+(?:\.\d{1,2})?|\b\d[\d,]*\.\d{2}\b`)
	// A SKU-ish token: letters and digits with AT LEAST ONE hyphen, and as many more as the SKU
	// has, e.g. TB-BLU60, PAV-1000, DBS-TB-BLU60.
	//
	// The repetition is not cosmetic. Every SKU in the Dibbits catalogue is three segments
	// (DBS-TB-BLU60, DBS-AGG-34CLEAR); the single-segment pattern this replaced matched only the
	// first two characters' worth, "DBS-TB", so the assistant answered "I can't find a product
	// matching \"DBS-TB\"" for every real SKU a person could copy off a pallet label. Caught by the
	// DIB-465 walkthrough asking a live catalogue a live question, not by a unit test with a
	// convenient two-segment fixture.
	reSKU = regexp.MustCompile(`\b[A-Za-z]{2,}(?:-[A-Za-z0-9]{2,})+\b`)
	// Anything the user put in quotes wins over every heuristic below, it is the escape hatch for
	// a customer called "Order" or a product with a space in its name.
	reQuoted = regexp.MustCompile(`"([^"]{1,80})"`)
	// DIB-478, a quote number: "Q-1042", "#Q1042", "quo 1042". The same shape as an order number
	// and kept as its own pattern rather than widened into reOrderNo, because the two resolve to
	// different commands and a shared pattern would make the discriminator a substring test.
	reQuoteNo = regexp.MustCompile(`(?i)#?\b((?:q|quo|qte)[- ]?\d+)\b`)
)

// keyword sets, in the words the counter actually uses.
var (
	stockWords    = []string{"stock", "on hand", "on-hand", "in stock", "inventory", "how many", "how much", "available", "got any", "do we have", "have we got"}
	paymentWords  = []string{"payment", "paid", "cheque", "check ", "receipt", "remittance", "e-transfer", "eft"}
	balanceWords  = []string{"balance", "owe", "owes", "owing", "outstanding", "account balance", "arrears"}
	activityWords = []string{"activity", "recent", "lately", "last few", "history", "been up to", "what's happened", "whats happened"}
	orderWords    = []string{"order", "so-", "sales order", "delivery for", "shipment"}

	// -- DIB-478: the words for the rest of the yard --------------------------------------------
	//
	// Each set is the vocabulary of ONE question, and the sets are deliberately narrow. A generous
	// keyword list makes the parser answer more questions and answer more of them wrongly, and a
	// wrong answer here is silent: the person gets a plausible table for a question they did not ask.

	// Money COMING IN, in the words a counter uses for it. Paired with a date phrase (never on its
	// own) so "did the cash land?" stays a payment lookup rather than becoming a day report.
	moneyInWords = []string{
		"came in", "come in", "coming in", "taken", " took ", " take ", "takings",
		"collected", "receipts", "banked", "payment", "payments", "paid",
	}
	// The AR desk's two questions.
	holdWords  = []string{"credit hold", "on hold", "held account", "stopped account", "who is stopped", "blocked account"}
	agingWords = []string{"aging", "ageing", "past due", "overdue account", "who owes", "owes the most", "ar worklist", "debtor"}
	// The documents.
	invoiceWords = []string{"invoice", "invoices", "invoiced", "bill for", "billing for"}
	quoteWords   = []string{"quote", "quotes", "quotation", "quoted"}
	// The yard.
	//
	// "coming in" / "come in" ALSO APPEAR IN moneyInWords, and that overlap is the point rather than
	// an oversight: the same two words carry both questions in this trade ("what came in today?" is
	// cash; "what's coming in for Blu 60?" is a pallet). They are disambiguated by the two rules in
	// route(), the money branch needs a DATE PHRASE, and an EXPLICIT product token beats a date
	// phrase, not by pretending the language is unambiguous.
	//
	// They were missing here until a live walkthrough asked the ordinary question and got the
	// capability menu: with the phrase only in the money list and the money branch demanding a date,
	// "what's coming in for TB-BLU60?" matched no branch at all.
	incomingWords = []string{
		"arriving", "on the way", "incoming", "on order", "in transit", "coming to", "coming in",
		"come in", "comes in", "came in", "restock", "due in", "when's more", "when is more",
	}
	// The catalogue.
	priceWords     = []string{"price", "pricing", "cost", "costs", "charge", "how much do we sell", "sell it for", "what do we charge"}
	catalogueWords = []string{"catalogue", "catalog", "find a product", "find the product", "look up a product", "search for a product", "what is the sku", "which product"}

	// -- DIB-494: the order's trail -------------------------------------------------------------
	//
	// Three sets, because the evidence comes in three strengths and each buys a different rule:
	//
	// orderTrailWords are the general words for "the recorded changes", real evidence only NEXT TO
	// an order (a number, or an order-referent phrase), because several of them are also the words
	// of the customer-activity question ("history", "activity") and stealing those would answer the
	// account question with a document trail.
	orderTrailWords = []string{
		"activity", "history", "happened", "changed", "changes", "change log", "audit",
		"went wrong", "last change", "who did",
	}
	// orderTrailStrong are the phrasings that are about a change-trail whatever else the sentence
	// says, nobody asks "who changed" or "what's the audit" about an account's recent orders. These
	// fire without an order word, and an order number then fills from the conversation or is asked
	// for by the command's own message.
	orderTrailStrong = []string{"who changed", "last change", "went wrong", "audit"}
	// orderRefPhrases are how a sentence points at ONE order without numbering it. Bare "order" is
	// deliberately NOT here: "order history for Northgate" is the account question and stays with it.
	orderRefPhrases = []string{"this order", "that order", "the order", "my order", "an order"}
)

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// paymentIsMeant guards the ONE ambiguous word in the payment set.
//
// "check " is in paymentWords because a paper cheque is a payment people ask about by that name
// ("did the check from Northgate clear?"). It is also the ordinary English verb for looking
// something up, "can you check stock for TB-BLU60", and because the payment branch runs before
// the stock branch, that sentence was being read as a payment lookup for a customer called
// "TB-BLU60". Found by DIB-465's capability tests, which ask exactly that question.
//
// The rule: if the only payment evidence is the word "check" AND the sentence is about stock, it is
// the verb. Any other payment word ("cheque", "payment", "receipt", "eft"…) still wins outright, so
// "check the payment from Bay Stone" is unaffected, as is "did that cheque for the pavers clear".
func paymentIsMeant(s string) bool {
	if !containsAny(s, paymentWords) {
		return false
	}
	for _, w := range paymentWords {
		if w == "check " {
			continue
		}
		if strings.Contains(s, w) {
			return true
		}
	}
	return !containsAny(s, stockWords)
}

// parseIntent maps a question onto one command invocation.
//
// THE ORDER OF THESE BRANCHES IS THE SPECIFICATION, see the file header. Each branch is guarded by
// the most distinctive evidence available for that command, so a sentence that could be read two
// ways is resolved by the stronger signal rather than by which branch happens to be first.
func parseIntent(q string, now time.Time) (ToolCall, bool) {
	s := strings.ToLower(strings.TrimSpace(q))
	quoted := firstGroup(reQuoted, q) // case preserved: it is a name or a SKU

	// 1. An explicit order number is the strongest signal in the language. "Where's #SO-4" needs no
	//    other evidence, and (DIB-494) a trail word beside it decides WHICH order question it is:
	//    "what happened on SO-4?" wants the change history, "where's SO-4?" wants the status line.
	//    Two commands now take an order number, and the trail words are the discriminator.
	if m := reOrderNo.FindStringSubmatch(q); m != nil {
		no := normaliseOrderNo(m[1])
		if containsAny(s, orderTrailWords) || containsAny(s, orderTrailStrong) {
			return ToolCall{Command: "order.activity", Args: Args{"order_no": no}}, true
		}
		return ToolCall{Command: "order.lookup", Args: Args{"order_no": no}}, true
	}

	// -- DIB-478 branches, inserted where the ordering argument puts them -----------------------

	// 1b. A QUOTE NUMBER plus a hold question. Before everything else for the same reason an order
	//     number is: a document number is the strongest signal in the language, and only one command
	//     takes a quote number.
	if m := reQuoteNo.FindStringSubmatch(q); m != nil && containsAny(s, []string{"hold", "holding", "reserv", "allocat"}) {
		return ToolCall{Command: "quote.holds", Args: Args{"quote_no": normaliseDocNo(m[1])}}, true
	}

	// 1c. MONEY OVER A PERIOD. Checked before the payment LOOKUP because "what came in today?" and
	//     "did PMT-1042 land?" are different questions and only the second is a lookup, and because
	//     paymentWords would otherwise swallow the first and demand a reference for it.
	//
	//     THE DATE PHRASE IS REQUIRED. Without it the sentence is a lookup, and a report answered in
	//     place of a lookup is the more annoying of the two mistakes: the person is holding a cheque.
	//     AND AN EXPLICIT PRODUCT OUTRANKS THE DATE PHRASE. "what came in today for TB-BLU60?" carries
	//     both signals, and it is a question about a delivery: nobody names a SKU to ask about cash.
	//     The rule is deliberately narrow, a SKU or a quoted name, both of which the user typed on
	//     purpose, so that a bare "what came in today?" stays the money question it has always been.
	//     A loose rule here (any noun that might be a product) would quietly steal the cash question,
	//     which is the more common one.
	namesAProduct := quoted != "" || reSKU.FindString(q) != ""
	if rng, ok := parseRange(s, now); ok && containsAny(s, moneyInWords) && !namesAProduct {
		today := now.Format(dayFmt)
		if rng.From == today && rng.To == today {
			// TODAY IS THE ROLLUP'S. One source for the day, matching the payments channel and the
			// dashboard tile, see commands_domain.go.
			return ToolCall{Command: "payments.today", Args: Args{}}, true
		}
		return ToolCall{Command: "payments.range", Args: Args{"from": rng.From, "to": rng.To}}, true
	}

	// 1d. CREDIT HOLDS before AGING, because "who is on hold" is the more specific question and the
	//     aging worklist mentions holds in passing.
	if containsAny(s, holdWords) {
		return ToolCall{Command: "ar.holds", Args: Args{}}, true
	}
	if containsAny(s, agingWords) {
		return ToolCall{Command: "ar.aging", Args: Args{}}, true
	}

	// 1e. INCOMING before STOCK. "How many are arriving?" contains "how many", which is a stock word,
	//     and answering it with the on-hand position would be a true number to a question about the
	//     future, the single most misleading shape an answer here can take.
	if containsAny(s, incomingWords) {
		p := quoted
		if p == "" {
			p = reSKU.FindString(q)
		}
		if p == "" {
			p = productAfter(q)
		}
		return ToolCall{Command: "stock.incoming", Args: Args{"product": p}}, true
	}

	// 2. A payment reference, likewise, but only when the sentence is about a payment, because
	//    "REF-12" is a plausible thing to call a product too.
	if paymentIsMeant(s) {
		a := Args{}
		if m := rePaymentRef.FindStringSubmatch(q); m != nil {
			a["reference"] = strings.ToUpper(strings.ReplaceAll(m[1], " ", "-"))
		}
		if amt := reAmount.FindString(q); amt != "" {
			a["amount"] = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(amt), "$"))
		}
		if quoted != "" {
			a["customer"] = quoted
		} else if who := customerAfter(q); who != "" {
			a["customer"] = who
		}
		if len(a) > 0 {
			return ToolCall{Command: "payment.lookup", Args: a}, true
		}
		// "did we get a payment" with nothing else in it. The command's own argument check will
		// produce the "tell me a reference, amount or customer" message, which is the right one.
		return ToolCall{Command: "payment.lookup", Args: Args{}}, true
	}

	// 3. Stock. Checked before the account questions because "how many pavers for Northgate" is a
	//    stock question with a customer in it, not an account question with a product in it.
	if containsAny(s, stockWords) {
		p := quoted
		if p == "" {
			p = reSKU.FindString(q)
		}
		if p == "" {
			p = productAfter(q)
		}
		if p != "" {
			return ToolCall{Command: "stock.lookup", Args: Args{"product": p}}, true
		}
		return ToolCall{Command: "stock.lookup", Args: Args{}}, true
	}

	// 3b. DIB-494: THE ORDER'S TRAIL, WITHOUT A NUMBER. Before the account questions because
	//     "what's happened on this order?" contains an activity word, and answering it with an
	//     ACCOUNT's recent orders would be a plausible table for a question nobody asked. Two ways
	//     in: a strong trail phrase ("who changed", "went wrong", "audit", "last change") stands on
	//     its own; a general trail word needs an order-referent phrase ("this order", "that order")
	//     beside it. Either way the number is deliberately left empty, the conversation's entity
	//     memory fills it when one is in play (FillMissingFromWindow), and the command's own
	//     "Which order?" message asks when nothing is.
	if containsAny(s, orderTrailStrong) || (containsAny(s, orderRefPhrases) && containsAny(s, orderTrailWords)) {
		return ToolCall{Command: "order.activity", Args: Args{}}, true
	}

	// 4. Balance before activity: "what's their balance lately" is a balance question, and
	//    'balance' is the more specific word.
	if containsAny(s, balanceWords) {
		who := quoted
		if who == "" {
			who = customerAfter(q)
		}
		return ToolCall{Command: "customer.balance", Args: Args{"customer": who}}, true
	}

	// 5. Activity.
	if containsAny(s, activityWords) {
		who := quoted
		if who == "" {
			who = customerAfter(q)
		}
		return ToolCall{Command: "customer.activity", Args: Args{"customer": who}}, true
	}

	// -- DIB-478: the document questions, and the catalogue -------------------------------------
	//
	// All of these sit AFTER balance and activity, because those two are asked far more often and
	// their words are more specific. They sit BEFORE the bare-order fallback, because "show me their
	// invoices" ends in a word the order branch does not claim and would otherwise fall through to
	// "I can't answer that one yet".

	// 5b. PRICE FOR A CUSTOMER. A price question with no customer still resolves here: the command's
	//     own required-argument message ("Which customer?") is the right reply, and it is a better
	//     one than the generic menu.
	if containsAny(s, priceWords) {
		a := Args{}
		if p := firstNonEmpty(quoted, reSKU.FindString(q), productAfter(q)); p != "" {
			a["product"] = p
		}
		if who := customerAfter(q); who != "" && who != a["product"] {
			a["customer"] = who
		}
		return ToolCall{Command: "product.price", Args: a}, true
	}

	// 5c. INVOICES.
	if containsAny(s, invoiceWords) {
		return ToolCall{Command: "customer.invoices", Args: Args{"customer": firstNonEmpty(quoted, customerAfter(q))}}, true
	}

	// 5d. QUOTES. After invoices because 'quote' is also a verb people use about pricing ("what did
	//     we quote them?"), and the price branch above has already taken the ones that meant a price.
	if containsAny(s, quoteWords) {
		return ToolCall{Command: "customer.quotes", Args: Args{"customer": firstNonEmpty(quoted, customerAfter(q))}}, true
	}

	// 5e. THE CATALOGUE.
	if containsAny(s, catalogueWords) {
		return ToolCall{Command: "product.find", Args: Args{"product": firstNonEmpty(quoted, reSKU.FindString(q), productAfter(q))}}, true
	}

	// 6. A bare mention of an order.
	//
	//    TWO COMMANDS SHARE THIS WORD and the discriminator is whether a CUSTOMER was named. "Where's
	//    SO-4" was taken by branch 1; "what orders has Northgate got?" is a list; anything else is a
	//    lookup whose required-argument message ("give me the number, like SO-4") is the right reply.
	if containsAny(s, orderWords) {
		if who := firstNonEmpty(quoted, customerAfter(q)); who != "" {
			return ToolCall{Command: "customer.orders", Args: Args{"customer": who}}, true
		}
		return ToolCall{Command: "order.lookup", Args: Args{"order_no": quoted}}, true
	}

	return ToolCall{}, false
}

// firstNonEmpty is the "try these in order" the argument extractors are written for: a quoted string
// beats a pattern match beats a preposition heuristic, and an honest "" beats a confident guess.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// normaliseDocNo turns "q 1042" / "Q-1042" / "q1042" into "Q-1042", normaliseOrderNo's rule, applied
// to the other document number this parser now recognises.
func normaliseDocNo(raw string) string { return normaliseOrderNo(raw) }

// normaliseOrderNo turns "so 4" / "SO-4" / "so-4" into "SO-4". The order service resolves
// case-insensitively anyway; normalising here is so the ECHO in the answer reads like the app's own
// printing of the number rather than like the user's typing.
func normaliseOrderNo(raw string) string {
	s := strings.ToUpper(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, " ", "-")
	if !strings.Contains(s, "-") {
		// "SO4" → "SO-4"
		for i := 0; i < len(s); i++ {
			if s[i] >= '0' && s[i] <= '9' {
				return s[:i] + "-" + s[i:]
			}
		}
	}
	return s
}

// customerAfter pulls the account name out of "... for Northgate Landscaping?" / "... from Bay
// Stone" / "does Northgate owe us anything".
//
// It is a heuristic and it is allowed to fail: when it returns "", the command answers with its own
// "which customer?" message, which is a fine outcome. What it must never do is return a WRONG name
// confidently, so it only reads text after an explicit preposition, stops at punctuation, and
// (DIB-494) refuses a bare pronoun outright: "the activity on it" is pointing backwards, and "it"
// offered as an account name is precisely the confident wrong answer this function promises never
// to give.
func customerAfter(q string) string {
	lower := strings.ToLower(q)
	for _, prep := range []string{" for ", " from ", " on ", " of ", " does ", " is "} {
		i := strings.Index(lower, prep)
		if i < 0 {
			continue
		}
		rest := strings.TrimSpace(q[i+len(prep):])
		rest = cutAtAny(rest, "?", ",", ".", " owe", " owes", " owing", " have", " got", " account", " balance")
		rest = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "'s"))
		if isBarePronoun(rest) {
			continue
		}
		if len(rest) >= 2 && len(rest) <= 60 {
			return rest
		}
	}
	// "Northgate's balance", a possessive at the start of the sentence.
	if i := strings.Index(q, "'s "); i > 0 {
		name := strings.TrimSpace(q[:i])
		if len(name) >= 2 && len(name) <= 60 && !strings.Contains(strings.ToLower(name), "what") {
			return name
		}
	}
	return ""
}

// isBarePronoun reports whether a candidate "name" is really a backward-pointing pronoun.
func isBarePronoun(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "it", "that", "this", "them", "those", "these", "him", "her", "us", "me":
		return true
	}
	return false
}

// productAfter pulls a product name out of "how many Blu 60 do we have" / "stock of Blu 60".
// Same contract as customerAfter: an honest "" beats a confident guess.
func productAfter(q string) string {
	lower := strings.ToLower(q)
	// "how much " joins the list because DIB-478 added it to stockWords: "how much Blu have we got?"
	// is the same sentence as "how many Blu have we got?" and it was resolving to a stock lookup with
	// no product at all.
	for _, prep := range []string{" of ", " for ", "how many ", "how much ", " have we got ", " stock on ", " left of "} {
		i := strings.Index(lower, prep)
		if i < 0 {
			continue
		}
		rest := strings.TrimSpace(q[i+len(prep):])
		rest = cutAtAny(rest, "?", ",", ".", " do we", " have we", " are there", " left", " in stock", " at ")
		if len(rest) >= 2 && len(rest) <= 60 {
			return rest
		}
	}
	return ""
}

// cutAtAny truncates s at the earliest of the given markers (case-insensitive).
func cutAtAny(s string, markers ...string) string {
	lower := strings.ToLower(s)
	cut := len(s)
	for _, m := range markers {
		if i := strings.Index(lower, strings.ToLower(m)); i >= 0 && i < cut {
			cut = i
		}
	}
	return strings.TrimSpace(s[:cut])
}

func firstGroup(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func lastUserText(msgs []Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == RoleUser {
			return msgs[i].Text
		}
	}
	return ""
}

func offered(tools []ToolSpec, name string) bool {
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

// -- the three things the stub says when it cannot answer ---------------------------------------
//
// All three are ORDINARY ANSWERS, not errors. Nothing has gone wrong when an assistant is asked
// something outside its remit, and the surface must not paint a red box for any of them.

// -- DIB-513 · THE MENU IS A ROW OF CHIPS, NOT A PARAGRAPH --------------------------------------
//
// All three of these used to end in toolMenu(): the word "Right now I can:" followed by every
// offered tool's full Summary as a prose bullet. Measured on the shipped build, one unanswerable
// question produced 279 words for a manager and 155 for a counter tier, a wall of text where the
// honest content is "not that; try one of these", and where the list of "these" is ALREADY on the
// screen as buttons, four inches up.
//
// So the sentence stays a sentence and the menu becomes Suggest: the codes the surface renders as
// pickable chips. The person clicks the lookup instead of reading about it, which is also the only
// version of this that ends in an answer rather than in more typing.
//
// The refusals themselves are unchanged in voice, softened, naming the way forward, never echoing
// the question back, because that part was never the problem.

func helpReply(tools []ToolSpec) Reply {
	return Reply{
		Text:    "Ask me about a payment, an order, stock, or a customer's balance.",
		Suggest: toolCodes(tools),
	}
}

func cannotAnswerReply(q string, tools []ToolSpec) Reply {
	_ = q // the question is deliberately NOT echoed: repeating a misunderstood sentence back reads
	// as mockery, and it is the one thing every bad chatbot does.
	return Reply{
		Text:    noReachText("I can't answer that one yet, I only look things up in HH Yard.", tools),
		Suggest: toolCodes(tools),
	}
}

func notAvailableToYouReply(cmd string, tools []ToolSpec) Reply {
	// Names no command and hints at no permission: "you can't see payments" is an invitation to go
	// and find out who can. It offers what the person CAN do instead, which is the useful half.
	_ = cmd
	return Reply{
		Text:    noReachText("That's not something I can look up for you.", tools),
		Suggest: toolCodes(tools),
	}
}

// noReachText keeps the ONE case where prose is still the whole answer: a caller offered no tools at
// all has no chips to be given, so the sentence has to carry what would change that.
func noReachText(lead string, tools []ToolSpec) string {
	if len(tools) == 0 {
		return lead + " There's nothing I can reach for you right now, an administrator sets that."
	}
	return lead + " Try one of the lookups."
}

// toolCodes is the offered set, in the order the caller was given it. Nil for an empty set, so a
// caller with no tools gets no empty chip row.
func toolCodes(tools []ToolSpec) []string {
	if len(tools) == 0 {
		return nil
	}
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}
