package assist

// capability.go, DIB-465. "What can you do?" answered from the REGISTRY, not from a guess.
//
// THE COMPLAINT THIS FIXES. Asked what it could do, the assistant fell through parseIntent to
// `cannotAnswer`, "I can't answer that one yet" followed by the tool menu. Technically it listed
// the commands; in practice it opened by refusing the one question every new user asks first. And
// "can you look up a customer's balance?" was worse: 'balance' is a balanceWords hit, so it ran
// customer.balance with no customer and answered "Which customer? A name or account number works."
//, a demand for an argument in reply to a question about whether the thing exists at all.
//
// WHERE THE ANSWER COMES FROM. `req.Tools`, which the service built from Registry.List(ctx, caller)
//, the commands THIS caller may run, already filtered by tier and by the administrator's policy
// rows. That is the whole accuracy claim: the assistant is not describing a feature list, it is
// reading its own gate. A frontline user asking "can you tell me what a customer owes?" is told no,
// because customer.balance (MaxTier 2, money) is not in their tool set, and the same person on a
// manager's login is told yes, from the same code and the same sentence template.
//
// TWO THINGS IT REFUSES TO DO
//
//  1. It never names a command that is not in the offered set. Answering "yes, but you're not
//     allowed" is a way of enumerating what exists to somebody who cannot use it, and it invites
//     the "who can?" conversation the registry deliberately does not have (see notAvailableToYou).
//  2. It never says yes to a WRITE. Read-only is structural in this package (the consumer
//     interfaces in model.go expose no writer), so "can you cancel that order" has exactly one
//     true answer, and matching 'order' to order.lookup and replying "yes, Find an order" would
//     be a confident lie about the one property the whole design exists to hold.

import (
	"fmt"
	"strings"
)

// capabilityOpeners are the ways people ask a yes/no question ABOUT the tool rather than asking it
// to do something. Each one is a prefix, because "can you check the stock of X" needs its subject
// ("check the stock of X") separated from the asking.
var capabilityOpeners = []string{
	"can you ", "can u ", "could you ", "are you able to ", "do you know how to ",
	"are you capable of ", "is it possible for you to ", "do you do ", "can this ",
	"can the assistant ", "can tanya ",
}

// bareCapability are the whole questions, no subject at all. Matched on the normalised sentence,
// by containment, so "hi, what can you do?" and "what can you do for me" both land here.
var bareCapability = []string{
	"what can you do", "what can u do", "what do you do", "what can you help",
	"what are you able", "what can i ask", "what can you tell me", "what can you look up",
	"what are your capabilities", "what can this do", "list your commands", "what commands",
	"how can you help", "what else can you do",
}

// writeVerbs are the acts this package cannot perform, in the words people use for them. The list
// is deliberately generous: a false "no, I only read" for a question that was actually a lookup
// costs the user one rephrase, while a false "yes" about changing data costs the pilot its
// credibility the first time somebody believes it.
var writeVerbs = []string{
	"delete", "remove", "cancel", "void", "refund", "credit ", "create", "add ", "new ",
	"update", "change", "edit", "amend", "adjust", "post ", "approve", "reject", "send",
	"email", "print", "book", "schedule", "reserve", "allocate", "transfer", "receive",
	"write", "set ", "raise", "issue", "ring ", "charge", "pay ", "refile", "close ", "reopen",
}

// CapabilityQuestion is a parsed "what/can you…" question.
type CapabilityQuestion struct {
	// Subject is what was asked about, lower-cased and trimmed. Empty for a bare "what can you do?".
	Subject string
}

// parseCapabilityQuestion recognises a question ABOUT the assistant's abilities.
//
// It is checked BEFORE parseIntent (stub_provider.Complete) but only when the sentence carries no
// concrete target, see hasConcreteTarget. That ordering is the whole subtlety of this feature:
// "can you check stock for TB-BLU60?" is polite phrasing for DO IT, and answering it with a
// description of the stock command would be maddening. "Can you check stock?" is a real question
// and gets a real answer.
func parseCapabilityQuestion(q string) (CapabilityQuestion, bool) {
	s := strings.ToLower(strings.TrimSpace(q))
	s = strings.TrimRight(s, " ?!.")
	if s == "" {
		return CapabilityQuestion{}, false
	}
	for _, b := range bareCapability {
		if strings.Contains(s, b) {
			return CapabilityQuestion{}, true
		}
	}
	if s == "help" || s == "capabilities" || s == "commands" {
		return CapabilityQuestion{}, true
	}
	for _, opener := range capabilityOpeners {
		i := strings.Index(s, opener)
		if i < 0 {
			continue
		}
		subject := strings.TrimSpace(s[i+len(opener):])
		if subject == "" {
			return CapabilityQuestion{}, true
		}
		return CapabilityQuestion{Subject: subject}, true
	}
	return CapabilityQuestion{}, false
}

// hasConcreteTarget reports whether the sentence names an actual record, an order number, a
// payment reference, an amount, a SKU, or anything quoted. It reuses the intent parser's own
// patterns rather than a second set, so the two can never disagree about what "concrete" means.
func hasConcreteTarget(q string) bool {
	return reOrderNo.MatchString(q) ||
		rePaymentRef.MatchString(q) ||
		reAmount.MatchString(q) ||
		reSKU.MatchString(q) ||
		reQuoted.MatchString(q)
}

// hasPronounTarget (DIB-494) is hasConcreteTarget's conversational sibling: a sentence whose
// subject is a backward-pointing pronoun HAS a target, it is carried by the conversation rather
// than typed. "Something went wrong with this order, can you tell me the activity on it?" opens
// with a capability opener and is not a question about the tool: it is polite phrasing for DO IT,
// about the thing the person just named. Answering it with the capability menu is the same failure
// the concrete-target guard exists to prevent, one referent to the left.
func hasPronounTarget(q string) bool {
	s := strings.TrimRight(strings.ToLower(strings.TrimSpace(q)), " ?!.")
	if s == "" {
		return false
	}
	return containsAnyOf(padded(s+"?"), pronouns)
}

// wantsToWrite reports whether the subject describes changing something.
func wantsToWrite(subject string) bool {
	s := " " + strings.ToLower(strings.TrimSpace(subject)) + " "
	for _, v := range writeVerbs {
		if strings.Contains(s, " "+strings.TrimSpace(v)) {
			return true
		}
	}
	return false
}

// ReadOnlyAnswer is what the assistant says to any request to CHANGE something. A constant so the
// test, the walkthrough shot and the frontend fixture are provably the same words.
const ReadOnlyAnswer = "No, I can only look things up. I can't create, change or cancel anything in HH Yard; " +
	"the screens do that, with your name against the change."

// capabilityReply composes the reply, from the caller's OWN tool set and nothing else.
//
// -- DIB-513 · THE MENU IS CHIPS ----------------------------------------------------------------
//
// Three of the four branches below used to end in capabilityMenu(): every offered tool as
// "- Title: full Summary (e.g. EXAMPLE)". Asking "help" on a live stack produced 136 words, and the
// same question on a manager's palette produced more. That list is the command palette four inches
// up the same screen, retyped as prose.
//
// So the branches keep their SENTENCE, which is the part that carries the yes, the no, and the
// read-only refusal, and hand the menu back as Suggest, the codes the surface renders as pickable
// chips. The tier fence is unchanged and is now exact rather than textual: Suggest can only ever
// contain the codes in `tools`, which is the caller's own filtered set.
//
// The one branch that does NOT offer chips is the direct hit: "Yes, Find an order. <what it does>
// (e.g. SO-1042)" already IS the answer, and a menu under it would be a menu under an answer.
func capabilityReply(q CapabilityQuestion, tools []ToolSpec) Reply {
	if len(tools) == 0 {
		// Not "I can do nothing", a person with no commands has been narrowed by an administrator,
		// and saying so is the difference between a broken tool and a configured one.
		return Reply{Text: "There's nothing I can look up for you right now, an administrator sets what I can reach."}
	}
	if q.Subject == "" {
		return Reply{
			Text:    "Here's what I can look up. I only ever look things up, I never change anything.",
			Suggest: toolCodes(tools),
		}
	}
	if wantsToWrite(q.Subject) {
		return Reply{Text: ReadOnlyAnswer, Suggest: toolCodes(tools)}
	}
	if t, ok := matchTool(q.Subject, tools); ok {
		// The command is named EXACTLY as the palette names it, capitalisation included, so the
		// sentence and the button a person then goes looking for are the same words.
		return Reply{Text: fmt.Sprintf("Yes, %s. %s\n%s", toolTitle(t), t.Description, exampleLine(t))}
	}
	return Reply{Text: "No, that's not something I can look up.", Suggest: toolCodes(tools)}
}

func exampleLine(t ToolSpec) string {
	if ex := exampleArg(t); ex != "" {
		return "Just ask, e.g. " + ex + "."
	}
	return "Just ask."
}

// exampleArg is the first required argument's example, or the first example of any argument. It is
// what makes the answer actionable in one read.
func exampleArg(t ToolSpec) string {
	for _, a := range t.Args {
		if a.Required && a.Example != "" {
			return a.Example
		}
	}
	for _, a := range t.Args {
		if a.Example != "" {
			return a.Example
		}
	}
	return ""
}

// toolTitle is the command's human name, falling back to its code for a provider-built spec that
// carries none. Never blank: an empty bullet in a capability list is worse than a code.
func toolTitle(t ToolSpec) string {
	if strings.TrimSpace(t.Title) != "" {
		return t.Title
	}
	return t.Name
}

// stopWords are the words that carry no evidence about WHICH command is meant. Matching on them
// would make every question match everything.
var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "me": true, "my": true, "our": true, "us": true,
	"for": true, "of": true, "on": true, "in": true, "to": true, "up": true, "about": true,
	"and": true, "or": true, "with": true, "any": true, "some": true, "is": true, "are": true,
	"do": true, "does": true, "what": true, "how": true, "much": true, "many": true, "look": true,
	"tell": true, "show": true, "give": true, "find": true, "get": true, "know": true, "see": true,
	"check": true, "please": true, "thing": true, "things": true, "something": true, "s": true,
	"it": true, "that": true, "this": true, "there": true, "we": true, "you": true, "i": true,
}

// matchTool picks the offered command a subject is asking about, or reports that none is.
//
// Scoring is deliberately crude and deliberately EVIDENCE-BASED: a subject word must appear in the
// command's NAME or in its summary. No stemming beyond one plural step, no synonyms table, the
// words in the summaries are the words the commands were named in ("payment", "order", "stock",
// "balance", "activity"), and a cleverer matcher would only make the "no" cases less predictable.
//
// THE NAME COUNTS FOR MORE THAN THE SUMMARY, and the threshold is why. Every summary in this
// registry mentions a customer, because every one of these lookups is about somebody's business:
// "Look up an order by its number and see its status, customer and total". Scoring all words alike
// made "can you tell me what a customer owes?" match ORDER LOOKUP on the word 'customer', a
// confident yes to the wrong command, and (for a frontline caller who is not offered the balance
// command) a yes where the honest answer is no. A single summary word is now not enough on its own:
// it takes the command's own name, or two independent summary hits.
//
// A tie goes to the first command in menu order, which is the registry's own ordering.
const (
	scoreTitleHit   = 3
	scoreSummaryHit = 1
	// scoreThreshold is "the command's name, or two summary words". Below it, the answer is "no".
	scoreThreshold = 2
)

// universalNouns name WHOSE data a command is about, never WHICH question it answers.
//
// DIB-465 established the rule for summaries: every summary in this registry mentions a customer,
// because every one of these lookups is about somebody's business, so a summary hit on 'customer'
// is not evidence. DIB-478 made the same thing true of TITLES, "Customer invoices", "Customer
// orders", "Customer balance", and the moment it did, a frontline caller asking "can you tell me
// what a customer owes?" started being told YES and pointed at Customer orders: a confident answer
// to a question about money, given to somebody who cannot see money, on the strength of the one
// word every command in the menu shares.
//
// So these words are scored at SUMMARY weight wherever they appear, title included. The list is
// deliberately tiny and deliberately not "every entity noun": 'stock', 'order', 'invoice', 'quote'
// and 'balance' all name a QUESTION and must keep their title weight, or "can you check stock?"
// stops being answerable. What is here is only the words that name the party, not the subject.
var universalNouns = map[string]bool{
	"customer": true, "customers": true, "client": true, "clients": true,
	"account": true, "accounts": true,
}

func matchTool(subject string, tools []ToolSpec) (ToolSpec, bool) {
	words := strings.FieldsFunc(strings.ToLower(subject), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
	best := ToolSpec{}
	bestScore := 0
	for _, t := range tools {
		// The TITLE is the strong signal; the CODE is not. Codes namespace by entity
		// ("customer.activity"), so scoring the code as a title hit made "what a customer owes"
		// match Recent activity on the word 'customer', the namespace, not the meaning. The code
		// is still evidence, at summary weight.
		name := strings.ToLower(toolTitle(t))
		summary := strings.ToLower(t.Description + " " + t.Name)
		score := 0
		for _, w := range words {
			if len(w) < 3 || stopWords[w] {
				continue
			}
			switch {
			case universalNouns[w]:
				// Namespace, not meaning. Counted once at summary weight whether it is in the title,
				// the summary or the code, see universalNouns.
				if containsWordish(name, w) || containsWordish(summary, w) {
					score += scoreSummaryHit
				}
			case containsWordish(name, w):
				score += scoreTitleHit
			case containsWordish(summary, w):
				score += scoreSummaryHit
			}
		}
		if score > bestScore {
			best, bestScore = t, score
		}
	}
	return best, bestScore >= scoreThreshold
}

// containsWordish is substring matching with ONE plural/verb-ending step: "owes" finds "owe",
// "stocks" finds "stock". 'e' and 's' endings are where English costs a match, and going further
// (a stemmer) would start producing matches nobody can predict from reading the summary.
func containsWordish(hay, w string) bool {
	if strings.Contains(hay, w) {
		return true
	}
	if len(w) > 4 {
		trimmed := strings.TrimSuffix(strings.TrimSuffix(w, "s"), "e")
		if len(trimmed) >= 3 && strings.Contains(hay, trimmed) {
			return true
		}
	}
	return false
}
