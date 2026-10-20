package assist

// openai_provider.go, the OpenAI compatible adapter: chat completions with tools, for a gateway or
// an open weight host (plan section 6.5).
//
// WHO IS TALKING, AND HOW THE GATEWAY KNOWS. The adapter authenticates with ONE key and names ONE
// dealer tag, both handed in at construction from deployment configuration (the operator's
// environment, see assist_config.go) and set on every request from there. Nothing on a request,
// not a question, a record, a tool description, a context value, a history turn, can reach either:
// CompletionRequest has no field that could carry one (a test pins the field list), and the two
// header values are written from the struct, never from the request. A compromised dealer box
// therefore cannot spend as, or appear as, another dealer by anything it sends this process
// (review r2, P2-3, core side). The gateway side (no response caching, no body logging) is slice 13.
//
// A redirect is refused: a custom header such as the dealer tag would be forwarded to the redirect
// target, and a gateway has no reason to redirect a POST.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// OpenAICompatConfig is deployment configuration. None of it ever comes from a request.
type OpenAICompatConfig struct {
	BaseURL   string // including the version prefix, for example http://gateway:4000/v1
	Key       Secret // the dealer's key; never logged, never rendered
	DealerTag string // the dealer's tag for the gateway's metering and reconciliation
	Model     string // the gateway's model name for this alias
	HTTP      *http.Client
}

// String and GoString keep the key out of any dump of the config.
func (c OpenAICompatConfig) String() string {
	return "OpenAICompatConfig{key:" + c.Key.String() + " model:" + c.Model + "}"
}
func (c OpenAICompatConfig) GoString() string { return c.String() }

// HeaderDealerTag is the header the dealer tag travels in.
const HeaderDealerTag = "X-Dealer-Tag"

// OpenAICompatProvider talks to an OpenAI compatible chat completions endpoint.
type OpenAICompatProvider struct {
	baseURL string
	key     Secret // never logged, never rendered, never serialised
	tag     string
	model   string
	http    *http.Client
}

// NewOpenAICompatProvider builds the adapter.
func NewOpenAICompatProvider(cfg OpenAICompatConfig) *OpenAICompatProvider {
	c := cfg.HTTP
	if c == nil {
		c = &http.Client{Timeout: 60 * time.Second}
	} else {
		cp := *c
		c = &cp
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &OpenAICompatProvider{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		key:     cfg.Key,
		tag:     cfg.DealerTag,
		model:   cfg.Model,
		http:    c,
	}
}

func (p *OpenAICompatProvider) Name() string  { return "openai_compatible" }
func (p *OpenAICompatProvider) Model() string { return p.model }

// String and GoString keep the key out of any log line or error that formats the provider.
func (p OpenAICompatProvider) String() string   { return "openai_compatible:" + p.model }
func (p OpenAICompatProvider) GoString() string { return p.String() }

// Stream is declared and not implemented.
func (*OpenAICompatProvider) Stream(context.Context, CompletionRequest) (<-chan StreamEvent, error) {
	return nil, ErrStreamingUnsupported
}

type oaiReq struct {
	Model          string       `json:"model"`
	MaxTokens      int          `json:"max_tokens"`
	Messages       []oaiMsg     `json:"messages"`
	Tools          []oaiTool    `json:"tools,omitempty"`
	ResponseFormat *oaiRespForm `json:"response_format,omitempty"`
}

type oaiMsg struct {
	Role       string        `json:"role"`
	Content    *string       `json:"content"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type oaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaiTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Parameters  any    `json:"parameters"`
	} `json:"function"`
}

type oaiRespForm struct {
	Type       string `json:"type"`
	JSONSchema struct {
		Name   string          `json:"name"`
		Schema json.RawMessage `json:"schema"`
		Strict bool            `json:"strict"`
	} `json:"json_schema"`
}

type oaiResp struct {
	Choices []struct {
		Message struct {
			Content   *string       `json:"content"`
			ToolCalls []oaiToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func strp(s string) *string { return &s }

// toOpenAIMessages translates the package's turns. Tool calls keep their ids, and a tool turn
// carries the id it answers.
func toOpenAIMessages(msgs []Message) []oaiMsg {
	out := make([]oaiMsg, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case RoleSystem, RoleUser:
			if strings.TrimSpace(m.Text) != "" {
				out = append(out, oaiMsg{Role: m.Role, Content: strp(m.Text)})
			}
		case RoleAssistant:
			om := oaiMsg{Role: RoleAssistant}
			if strings.TrimSpace(m.Text) != "" {
				om.Content = strp(m.Text)
			}
			for _, c := range m.ToolCalls {
				args, err := json.Marshal(map[string]string(c.Args))
				if err != nil || string(args) == "null" {
					args = []byte("{}")
				}
				tc := oaiToolCall{ID: c.ID, Type: "function"}
				tc.Function.Name = wireName(c.Command)
				tc.Function.Arguments = string(args)
				om.ToolCalls = append(om.ToolCalls, tc)
			}
			if om.Content == nil && len(om.ToolCalls) == 0 {
				continue
			}
			out = append(out, om)
		case RoleTool:
			out = append(out, oaiMsg{Role: RoleTool, ToolCallID: m.ToolCallID, Content: strp(m.Text)})
		}
	}
	return out
}

func toOpenAITools(tools []ToolSpec) []oaiTool {
	out := make([]oaiTool, 0, len(tools))
	for _, t := range tools {
		props := map[string]any{}
		var required []string
		for _, a := range t.Args {
			props[a.Name] = map[string]any{"type": "string", "description": a.Label + ", e.g. " + a.Example}
			if a.Required {
				required = append(required, a.Name)
			}
		}
		schema := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			schema["required"] = required
		}
		ot := oaiTool{Type: "function"}
		ot.Function.Name = wireName(t.Name)
		ot.Function.Description = t.Description
		ot.Function.Parameters = schema
		out = append(out, ot)
	}
	return out
}

// Complete sends one turn.
func (p *OpenAICompatProvider) Complete(ctx context.Context, req CompletionRequest) (Reply, error) {
	body := oaiReq{
		Model:     p.model,
		MaxTokens: 1024,
		Messages:  toOpenAIMessages(req.Messages),
		Tools:     toOpenAITools(req.Tools),
	}
	if req.Schema != nil {
		rf := &oaiRespForm{Type: "json_schema"}
		rf.JSONSchema.Name = req.Schema.Name
		rf.JSONSchema.Schema = req.Schema.Schema
		rf.JSONSchema.Strict = true
		body.ResponseFormat = rf
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return Reply{}, fmt.Errorf("assist: encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return Reply{}, fmt.Errorf("assist: build request: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	// The only place the key and the dealer tag are used. Both come from the struct, which was built
	// from deployment configuration, and from nowhere on the request.
	if p.key.Set() {
		httpReq.Header.Set("Authorization", "Bearer "+p.key.Reveal())
	}
	if p.tag != "" {
		httpReq.Header.Set(HeaderDealerTag, p.tag)
	}

	resp, err := p.http.Do(httpReq)
	if err != nil {
		return Reply{}, fmt.Errorf("assist: provider unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return Reply{}, errors.New("assist: provider answered with a redirect, which is refused")
	}
	var out oaiResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Reply{}, fmt.Errorf("assist: provider returned unreadable body (status %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		msg := "provider refused the request"
		if out.Error != nil && out.Error.Message != "" {
			msg = out.Error.Message
		}
		return Reply{}, fmt.Errorf("assist: provider error (status %d): %s", resp.StatusCode, msg)
	}
	if len(out.Choices) == 0 {
		return Reply{}, errors.New("assist: provider returned no choices")
	}

	reply := Reply{
		TokensIn:     out.Usage.PromptTokens,
		TokensOut:    out.Usage.CompletionTokens,
		TokensCached: out.Usage.PromptTokensDetails.CachedTokens,
	}
	msg := out.Choices[0].Message
	if msg.Content != nil {
		reply.Text = *msg.Content
	}
	idx := wireNameIndex(req.Tools)
	for _, tc := range msg.ToolCalls {
		call := ToolCall{ID: tc.ID, Command: codeForWire(idx, tc.Function.Name), Args: Args{}}
		// arguments is a JSON STRING and a model can send anything in it. Empty is no arguments;
		// anything that is not a JSON object is flagged for the service's escalation check.
		if strings.TrimSpace(tc.Function.Arguments) != "" {
			var raw map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &raw); err != nil {
				call.ArgsErr = "the tool arguments were not a valid JSON object"
			}
			for k, v := range raw {
				call.Args[k] = stringifyArg(v)
			}
		}
		reply.ToolCalls = append(reply.ToolCalls, call)
	}
	return reply, nil
}
