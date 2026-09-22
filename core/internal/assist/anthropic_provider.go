package assist

// anthropic_provider.go, the optional real provider, selectable by config.
//
// SOP-1, STATED ONCE AND HELD EVERYWHERE IN THIS FILE:
//
//	The API key is read from an ENVIRONMENT VARIABLE at boot. It is never written to a config row,
//	never committed, never logged, never returned by an endpoint, and never included in an error.
//
// The variable is named here and in NOTES-FIX-431.md and nowhere else:
//
//	HOS_ASSIST_ANTHROPIC_API_KEY
//
// The only thing any other layer may learn about it is the BOOLEAN "is it set" (KeyPresent), which
// is what the admin surface renders so an operator can tell "the key is missing" from "the vendor
// is down" without ever seeing a character of it.
//
// IF THE KEY IS ABSENT THE APP STILL BOOTS. NewProviderFromConfig falls back to the stub and says so
// once in the log. An assistant is not load-bearing; refusing to start the ERP because an optional
// assistant has no credential would be a self-inflicted outage, and it is exactly the kind of thing
// that happens at 6am on the morning of a demo.
//
// WHAT IS DELIBERATELY NOT HERE: retries, streaming, prompt caching, a system-prompt library.
// This is a pilot whose question is "is the answer any good and what does it cost". Every one of
// those features changes the token accounting, and an accounting change during a measurement is how
// a pilot produces a number nobody can defend.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// EnvAPIKey is the environment variable the Anthropic-style provider reads its credential from.
// Exported as a NAME so main.go, the admin surface and the documentation can all refer to the same
// string without any of them handling the value.
const EnvAPIKey = "HOS_ASSIST_ANTHROPIC_API_KEY"

// defaultAnthropicModel is used when assist_settings has no model row. Named here rather than
// inlined so the ledger's `model` column and this constant are obviously the same fact.
const defaultAnthropicModel = "claude-sonnet-4-5"

const anthropicVersion = "2023-06-01"

// AnthropicProvider talks to an Anthropic-style Messages API.
type AnthropicProvider struct {
	key     string // never logged, never rendered, never serialised
	model   string
	baseURL string
	http    *http.Client
}

// NewAnthropicProvider builds the provider. An empty key is a programming error at this level -
// the DECISION about a missing key belongs to NewProviderFromConfig, which is the one place that
// knows falling back to the stub is acceptable.
func NewAnthropicProvider(key, model, baseURL string) *AnthropicProvider {
	if model == "" {
		model = defaultAnthropicModel
	}
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	return &AnthropicProvider{
		key:     key,
		model:   model,
		baseURL: strings.TrimRight(baseURL, "/"),
		// A bounded timeout, because an assistant that hangs holds a request goroutine and a user's
		// attention. 30s is long enough for a tool-using turn and short enough to fail visibly.
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *AnthropicProvider) Name() string  { return "anthropic" }
func (p *AnthropicProvider) Model() string { return p.model }

// The wire shapes, kept minimal and local. Only the fields this pilot sends or reads.
type antReq struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system,omitempty"`
	Messages  []antMsg  `json:"messages"`
	Tools     []antTool `json:"tools,omitempty"`
}

type antMsg struct {
	Role    string     `json:"role"`
	Content []antBlock `json:"content"`
}

type antBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type antTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

type antResp struct {
	Content []antBlock `json:"content"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// systemPrompt is the whole of what the model is told about its job.
//
// It is short because the fence is not made of words. The model cannot reach any data except
// through the tools it is given, and those tools were already filtered to what the asking user may
// run (service.go). A longer prompt would make the fence look stronger and would not make it
// stronger; the sentences here are about TONE and about not inventing numbers, which are the two
// things a prompt can actually influence.
const systemPrompt = `You are TanyaAI, the assistant inside HH Yard, a landscape-supply ERP used by counter staff, yard crews and managers at Dibbits.

Answer only from the results of the tools you are given. Never guess a number, a status, a price or a date; if a tool did not return it, say you do not have it. If no tool fits the question, say so plainly and mention what you can look up.

Keep answers to a couple of short sentences. These are people at a counter with a customer waiting.`

// Complete sends one turn.
func (p *AnthropicProvider) Complete(ctx context.Context, req CompletionRequest) (Reply, error) {
	body := antReq{
		Model:     p.model,
		MaxTokens: 1024,
		System:    systemPrompt,
		Messages:  toAnthropicMessages(req.Messages),
		Tools:     toAnthropicTools(req.Tools),
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return Reply{}, fmt.Errorf("assist: encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(buf))
	if err != nil {
		return Reply{}, fmt.Errorf("assist: build request: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	// The one place the key is used. It goes onto a header of a request being sent, and into
	// nothing else, not a struct that gets logged, not an error, not a retry record.
	httpReq.Header.Set("x-api-key", p.key)

	resp, err := p.http.Do(httpReq)
	if err != nil {
		// %w on a *url.Error prints the METHOD and URL, never headers. Verified shape: this cannot
		// carry the key.
		return Reply{}, fmt.Errorf("assist: provider unreachable: %w", err)
	}
	defer resp.Body.Close()

	var out antResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Reply{}, fmt.Errorf("assist: provider returned unreadable body (status %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		// The vendor's own message is surfaced but the STATUS is what the operator needs: 401 is a
		// key problem, 429 is a rate limit, 5xx is theirs. None of the three is the user's fault and
		// none of them should ever print a credential.
		msg := "provider refused the request"
		if out.Error != nil && out.Error.Message != "" {
			msg = out.Error.Message
		}
		return Reply{}, fmt.Errorf("assist: provider error (status %d): %s", resp.StatusCode, msg)
	}

	reply := Reply{TokensIn: out.Usage.InputTokens, TokensOut: out.Usage.OutputTokens}
	var texts []string
	for _, b := range out.Content {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				texts = append(texts, b.Text)
			}
		case "tool_use":
			args := Args{}
			// Tool inputs arrive as arbitrary JSON. Every command argument is a STRING, so numbers
			// and booleans are rendered rather than rejected, a model that answers `{"amount":
			// 1240}` has asked a perfectly sensible question, and refusing it on a type technicality
			// would be a fault the user cannot see or fix.
			var raw map[string]any
			if len(b.Input) > 0 {
				_ = json.Unmarshal(b.Input, &raw)
			}
			for k, v := range raw {
				args[k] = stringifyArg(v)
			}
			reply.ToolCalls = append(reply.ToolCalls, ToolCall{Command: b.Name, Args: args})
		}
	}
	reply.Text = strings.Join(texts, "\n\n")
	return reply, nil
}

func stringifyArg(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		// %v on a float64 gives "1240" for whole numbers rather than "1240.000000".
		return strings.TrimSuffix(fmt.Sprintf("%v", t), ".0")
	case bool:
		return fmt.Sprintf("%t", t)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}

// toAnthropicMessages translates the package's three roles into the vendor's two-role + block
// format. Tool results become user-role tool_result blocks, which is the vendor's shape.
//
// The tool_use_id is synthesised from the command name because this pilot issues at most one tool
// call per turn (service.go bounds the loop). If that ever changes, the ids have to be carried on
// the Message, which is why the synthesis is in ONE function that would have to be edited.
func toAnthropicMessages(msgs []Message) []antMsg {
	out := make([]antMsg, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case RoleUser:
			out = append(out, antMsg{Role: "user", Content: []antBlock{{Type: "text", Text: m.Text}}})
		case RoleAssistant:
			if strings.TrimSpace(m.Text) == "" {
				continue
			}
			out = append(out, antMsg{Role: "assistant", Content: []antBlock{{Type: "text", Text: m.Text}}})
		case RoleTool:
			out = append(out, antMsg{Role: "user", Content: []antBlock{{
				Type:      "tool_result",
				ToolUseID: "call_" + m.ToolName,
				Content:   m.Text,
			}}})
		}
	}
	return out
}

// toAnthropicTools renders the caller's permitted commands as JSON-schema tools. Every argument is
// a string for the reason given in Complete: the command layer parses its own arguments (amounts,
// order numbers) and having the schema also parse them would put two parsers on one value.
func toAnthropicTools(tools []ToolSpec) []antTool {
	out := make([]antTool, 0, len(tools))
	for _, t := range tools {
		props := map[string]any{}
		var required []string
		for _, a := range t.Args {
			props[a.Name] = map[string]any{
				"type":        "string",
				"description": a.Label + ", e.g. " + a.Example,
			}
			if a.Required {
				required = append(required, a.Name)
			}
		}
		schema := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			schema["required"] = required
		}
		out = append(out, antTool{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	return out
}
