package assist

// clarify.go, DIB-478. An honest question is a FIRST-CLASS ANSWER.
//
// THE DEFECT THIS TYPE EXISTS TO MAKE IMPOSSIBLE. Asked "how much Blu have we got?", an assistant
// with two products matching 'Blu' has three options: pick one and say nothing (a confident wrong
// answer, the worst outcome and the one that ends an investigation that should have continued);
// say "I couldn't find that" (false, it found two); or ASK. Only the third is honest, and it is
// only usable if the asking is a structured answer the surface can render as pickable options
// rather than a sentence the person has to retype an answer to.
//
// SO A CLARIFY CARRIES THE PENDING CALL. The command and the arguments already resolved travel with
// the question, so picking an option is not a new sentence to be re-parsed, it is the same
// invocation with one more argument filled in. That is what makes "pick an option, get the answer"
// deterministic: the second turn does not re-derive anything the first turn already decided.
//
// A CLARIFY IS NEVER A GUESS WITH A CONFIRMATION ON TOP. It is raised only when resolution is
// GENUINELY ambiguous, two or more candidates with no exact match among them. One exact match plus
// four fuzzy ones is not ambiguous, and asking anyway would train people to click through a dialog,
// which is how a confirmation stops being read.

import "strings"

// ClarifyOption is one thing the person might have meant.
//
// Label is what the surface shows and what a screen reader says; Value is what fills the argument.
// They differ on purpose: "Techo-Bloc Blu 60 (DBS-TB-BLU60)" is what a person recognises and
// "DBS-TB-BLU60" is what resolves without further ambiguity, and offering the label as the value
// would send the ambiguous words straight back through the same matcher.
type ClarifyOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
	// Hint is the optional second line, why this candidate matched, or what distinguishes it.
	Hint string `json:"hint,omitempty"`
}

// Clarify is the assistant asking, with the answer half-built.
type Clarify struct {
	// Question is the whole sentence, written for a person: "Two products match "Blu", which one?"
	Question string `json:"question"`
	// Options are the candidates, in the order the underlying search ranked them. Never empty: a
	// clarify with nothing to pick is a refusal wearing a question mark.
	Options []ClarifyOption `json:"options"`
	// Command is the invocation waiting on the answer.
	Command string `json:"command"`
	// Args are the arguments ALREADY resolved. The picked option fills Arg.
	Args Args `json:"args"`
	// Arg names the argument the option fills.
	Arg string `json:"arg"`
}

// Complete builds the invocation for a picked option. It is the ONE place a clarify turns back into
// a call, so a picked option can never arrive at the registry as anything other than the pending
// command with one more argument.
func (c *Clarify) Complete(value string) ToolCall {
	args := Args{}
	for k, v := range c.Args {
		args[k] = v
	}
	args[c.Arg] = strings.TrimSpace(value)
	return ToolCall{Command: c.Command, Args: args}
}

// Match resolves what a person typed or clicked back onto one of the options.
//
// FOUR SPELLINGS, because a clarify is answered in four ways and three of them are typing:
//
//	the exact value       , what the button posts
//	the exact label       , what someone copies off the screen
//	a 1-based ordinal     , "1", "the first one", "second"
//	a unique substring    , "blu 60" against "Techo-Bloc Blu 60 (DBS-TB-BLU60)"
//
// The substring arm requires UNIQUENESS. "blu" matches both options in the very example this type
// was written for, and resolving it to the first would be precisely the guess the clarify exists to
// avoid, so an ambiguous reply to a clarifying question leaves the clarify standing.
func (c *Clarify) Match(reply string) (ClarifyOption, bool) {
	s := strings.ToLower(strings.TrimSpace(strings.TrimRight(reply, " ?!.")))
	if s == "" {
		return ClarifyOption{}, false
	}
	for _, o := range c.Options {
		if strings.EqualFold(strings.TrimSpace(o.Value), s) || strings.EqualFold(strings.TrimSpace(o.Label), s) {
			return o, true
		}
	}
	if i, ok := ordinal(s); ok && i >= 1 && i <= len(c.Options) {
		return c.Options[i-1], true
	}
	var hit ClarifyOption
	n := 0
	for _, o := range c.Options {
		if strings.Contains(strings.ToLower(o.Label), s) || strings.Contains(strings.ToLower(o.Value), s) {
			hit, n = o, n+1
		}
	}
	if n == 1 {
		return hit, true
	}
	return ClarifyOption{}, false
}

// ordinalWords are the ways people name a position in a short list out loud.
var ordinalWords = map[string]int{
	"1": 1, "first": 1, "the first": 1, "the first one": 1, "one": 1,
	"2": 2, "second": 2, "the second": 2, "the second one": 2, "two": 2,
	"3": 3, "third": 3, "the third": 3, "the third one": 3, "three": 3,
	"4": 4, "fourth": 4, "the fourth": 4, "the fourth one": 4, "four": 4,
	"5": 5, "fifth": 5, "the fifth": 5, "the fifth one": 5, "five": 5,
}

func ordinal(s string) (int, bool) {
	n, ok := ordinalWords[s]
	return n, ok
}

// maxClarifyOptions bounds the list. A clarify with eleven options is not a question, it is a
// search result, and the honest answer for a term that vague is to say the term is too vague.
const maxClarifyOptions = 5
