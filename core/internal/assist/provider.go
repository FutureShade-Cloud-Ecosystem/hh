package assist

// provider.go, HALF C's seam. The one interface the chat surface knows about.
//
// WHY AN INTERFACE AT ALL, IN A PILOT. Because the two decisions this feature has to survive are
// "which vendor" and "whether any vendor", and both of them are decisions the client has not made
// yet. A pilot wired directly to one HTTP API answers neither question and has to be rewritten to
// ask them. Behind this interface sit two implementations today, a deterministic stub that needs
// no key and no network, and an Anthropic-style HTTP provider, and the choice between them is a
// config row plus an environment variable.
//
// THE STUB IS NOT A MOCK. It ships, it is the DEFAULT, and every test in this package runs against
// it. That is a deliberate property: the assistant must be demonstrable on a laptop with no
// credentials, in CI with no network, and on a customer site whose IT has not yet approved an
// outbound API. What the stub gives up is language flexibility, not correctness, it invokes the
// same commands through the same registry and renders the same results.
//
// THE PROTOCOL IS TWO-PHASE, ON PURPOSE. Complete() may answer with TEXT or with TOOL CALLS. The
// service runs the tool calls (through the registry, as the asking user), appends the results as
// messages, and calls Complete() again for the sentence. It is the shape every tool-using model API
// already has, so the real provider is a translation rather than an adaptation, and it is the
// shape that keeps the model on the OUTSIDE of the data: it never receives a connection, only the
// results of commands the caller was allowed to run.

import (
	"context"
	"strings"
)

// Message roles. Strings rather than an enum so the wire format, the stub, and any provider
// translation all speak the same three words with no mapping table.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message is one turn. A RoleTool message carries the RESULT of a command the service already ran;
// ToolName says which command produced it.
//
// Text on a tool message is the result rendered as plain prose. Both are carried because a real
// model reads the prose and a stub reads the structure, and generating one from the other at the
// provider boundary would put a second formatter behind the interface.
type Message struct {
	Role     string  `json:"role"`
	Text     string  `json:"text"`
	ToolName string  `json:"tool_name,omitempty"`
	Result   *Result `json:"result,omitempty"`

	// -- DIB-478: the STRUCTURED half of a turn -------------------------------------------------
	//
	// Ran is which commands this assistant turn actually ran, with the arguments they ran with. It
	// is what conversational resolution reads (conversation.go), never the prose, because prose is
	// written by a provider that may be swapped out and "what does 'it' refer to" must not change
	// when the vendor does.
	//
	// It is echoed back by the client on the next turn. That makes it CLIENT-SUPPLIED and therefore
	// untrusted, which is safe for exactly one reason: nothing in a history grants authority. A
	// forged Invocation naming a command the caller may not run resolves to a call that Registry.Run
	// then refuses, in the app's own words.
	Ran []Invocation `json:"ran,omitempty"`

	// Clarify is the question this assistant turn ASKED, when it asked one. Carried so that the next
	// turn can be recognised as an ANSWER to it rather than re-parsed as a new question, picking
	// "Techo-Bloc Blu 60" out of a list is not a sentence, it is the completion of an invocation.
	Clarify *Clarify `json:"clarify,omitempty"`
}

// ToolSpec is one command offered to the provider for this exchange.
//
// CRITICALLY: the service builds this list from Registry.List(ctx, caller), the commands THIS
// caller may run. A model is never told about a tool it would be refused, which means a refusal is
// a thing that essentially cannot happen rather than a thing that has to be handled gracefully. The
// registry still checks on the way back in; belt and braces, because the model's output is
// untrusted input.
type ToolSpec struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Args        []ArgSpec `json:"args"`
	// Title is the command's human name ("Find an order"), what the capability answer calls it
	// (DIB-465). Carried on the spec rather than looked up from the registry at answer time,
	// because the spec IS the caller's filtered view of the registry: an answer composed from
	// anything else could name a command the person may not run.
	Title string `json:"title,omitempty"`
}

// ToolCall is the provider asking for a command to be run. Command is matched against the registry;
// anything it invents is refused by name.
type ToolCall struct {
	Command string `json:"command"`
	Args    Args   `json:"args"`
}

// CompletionRequest is what a provider is given: the conversation so far and the tools allowed.
type CompletionRequest struct {
	Messages []Message
	Tools    []ToolSpec
}

// Reply is what a provider returns. TokensIn/TokensOut are recorded in the ledger EXACTLY as
// reported, never estimated, never rounded up "to be safe". A budget enforced against a guess is a
// budget that cannot be explained to the person it refuses.
type Reply struct {
	Text      string
	ToolCalls []ToolCall
	TokensIn  int
	TokensOut int
	// TokensCached is input served from the provider's cache, billed at the cached price. Zero when
	// the provider does not report it.
	TokensCached int

	// Suggest is DIB-513: the command CODES to offer beside a reply that could not answer.
	//
	// It exists so a refusal can be a sentence and a row of chips instead of a sentence and a
	// bulleted transcript of the whole palette. Measured before it: the "I can't answer that one
	// yet" reply was 279 words for a manager and 155 for a counter tier, because it spelled out
	// every tool's full Summary as prose. The words are the same information the palette above the
	// chat already shows as buttons, and a list you can click beats a list you have to read.
	//
	// Optional for a Provider. A real model writes its own prose and leaves this nil, which renders
	// exactly as it did before.
	Suggest []string
}

// Provider is the pluggable completion seam.
//
// Name and Model are read for the LEDGER, per exchange, rather than captured once at boot: a
// provider that fails over to a different model mid-flight must be recorded as what it actually
// was, not as what it was configured to be.
type Provider interface {
	Name() string
	Model() string
	Complete(ctx context.Context, req CompletionRequest) (Reply, error)
}

// toolSpecsFor renders a caller's permitted commands as tool specs.
func toolSpecsFor(cmds []Command) []ToolSpec {
	out := make([]ToolSpec, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, ToolSpec{Name: c.Code, Title: c.Title, Description: c.Summary, Args: c.Args})
	}
	return out
}

// renderResult turns a command result into the prose a provider reads back. It is the ONE place a
// Result becomes text for a model, so a caveat in Note can never be dropped on the way to the
// sentence the user finally sees.
func renderResult(r Result) string {
	var b strings.Builder
	b.WriteString(r.Headline)
	for _, it := range r.Items {
		b.WriteString("\n- ")
		b.WriteString(it.Title)
		for i, f := range it.Fields {
			if i == 0 {
				b.WriteString(": ")
			} else {
				b.WriteString(", ")
			}
			b.WriteString(f.Label)
			b.WriteString(" ")
			b.WriteString(f.Value)
		}
	}
	if r.Note != "" {
		b.WriteString("\n(")
		b.WriteString(r.Note)
		b.WriteString(")")
	}
	return b.String()
}
