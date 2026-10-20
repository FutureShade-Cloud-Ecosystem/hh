package assist

// strict_anthropic_test.go, TanyAI slice 1 sub item 2: a strict fake of the Anthropic Messages API.
//
// It binds port 0 and enforces the parts of the real API's contract a tool round trip can break:
//
//   - roles alternate user, assistant, user, ... and the first turn is a user turn (consecutive
//     same role turns are rejected);
//   - every tool_result names a tool_use id that exists in the IMMEDIATELY preceding assistant
//     turn, and every tool_use of that turn is answered in the next user turn;
//   - tool_result blocks come before any text block in their user turn.
//
// A violation is answered with a 400 in the vendor's error shape AND recorded, so a test can see a
// violation even when the service degrades and swallows the failed call.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
)

type strictAnthropic struct {
	t   *testing.T
	srv *httptest.Server

	mu         sync.Mutex
	replies    []string // raw JSON bodies, served in order; the last repeats
	requests   [][]byte
	violations []string
}

func newStrictAnthropic(t *testing.T, replies ...string) *strictAnthropic {
	t.Helper()
	s := &strictAnthropic{t: t, replies: replies}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *strictAnthropic) URL() string { return s.srv.URL }

func (s *strictAnthropic) Requests() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.requests...)
}

func (s *strictAnthropic) Violations() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.violations...)
}

// anthropicToolName is the documented tool name pattern.
var anthropicToolName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

type strictBlock struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	ToolUseID string `json:"tool_use_id"`
}

type strictMsg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func (s *strictAnthropic) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.requests = append(s.requests, body)
	n := len(s.requests)
	violation := validateAnthropicTurns(body)
	if violation != "" {
		s.violations = append(s.violations, violation)
	}
	reply := s.replies[len(s.replies)-1]
	if n-1 < len(s.replies) {
		reply = s.replies[n-1]
	}
	s.mu.Unlock()

	w.Header().Set("content-type", "application/json")
	if violation != "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"type":"error","error":{"type":"invalid_request_error","message":%q}}`, violation)
		return
	}
	_, _ = w.Write([]byte(reply))
}

// validateAnthropicTurns returns "" when the turns obey the contract, else the first violation.
func validateAnthropicTurns(body []byte) string {
	var req struct {
		Messages []strictMsg `json:"messages"`
		Tools    []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return "unreadable body"
	}
	for i, t := range req.Tools {
		if !anthropicToolName.MatchString(t.Name) {
			return fmt.Sprintf("tools.%d.name: %q does not match the pattern ^[a-zA-Z0-9_-]{1,64}$", i, t.Name)
		}
	}
	if len(req.Messages) == 0 {
		return "messages: at least one message is required"
	}
	if req.Messages[0].Role != "user" {
		return "messages: first message must use the user role"
	}
	prevUse := map[string]bool{}
	for i, m := range req.Messages {
		if m.Role != "user" && m.Role != "assistant" {
			return fmt.Sprintf("messages.%d: unknown role %q", i, m.Role)
		}
		if i > 0 && req.Messages[i-1].Role == m.Role {
			return fmt.Sprintf("messages.%d: consecutive %s turns", i, m.Role)
		}
		var blocks []strictBlock
		if err := json.Unmarshal(m.Content, &blocks); err != nil {
			return fmt.Sprintf("messages.%d: content must be a list of blocks", i)
		}
		answered := map[string]bool{}
		seenText := false
		for _, b := range blocks {
			switch b.Type {
			case "text":
				seenText = true
			case "tool_use":
				if m.Role != "assistant" {
					return fmt.Sprintf("messages.%d: tool_use in a %s turn", i, m.Role)
				}
				if !anthropicToolName.MatchString(b.Name) {
					return fmt.Sprintf("messages.%d: tool_use name %q does not match the pattern", i, b.Name)
				}
				if b.ID == "" {
					return fmt.Sprintf("messages.%d: tool_use without an id", i)
				}
			case "tool_result":
				if m.Role != "user" {
					return fmt.Sprintf("messages.%d: tool_result in a %s turn", i, m.Role)
				}
				if seenText {
					return fmt.Sprintf("messages.%d: tool_result must come before text in a user turn", i)
				}
				if !prevUse[b.ToolUseID] {
					return fmt.Sprintf("messages.%d: unexpected tool_use_id %q found in tool_result blocks: no matching tool_use in the previous assistant message", i, b.ToolUseID)
				}
				answered[b.ToolUseID] = true
			default:
				return fmt.Sprintf("messages.%d: unknown block type %q", i, b.Type)
			}
		}
		if m.Role == "user" {
			for id := range prevUse {
				if !answered[id] {
					return fmt.Sprintf("messages.%d: tool_use %q has no tool_result in the next user turn", i, id)
				}
			}
			prevUse = map[string]bool{}
		} else {
			prevUse = map[string]bool{}
			for _, b := range blocks {
				if b.Type == "tool_use" {
					prevUse[b.ID] = true
				}
			}
		}
	}
	return ""
}

// -- canned replies -------------------------------------------------------------------------------

func antTextReply(text string) string {
	b, _ := json.Marshal(text)
	return `{"content":[{"type":"text","text":` + string(b) + `}],"stop_reason":"end_turn","usage":{"input_tokens":11,"output_tokens":3}}`
}

func antToolReply(blocks ...string) string {
	out := `{"content":[`
	for i, b := range blocks {
		if i > 0 {
			out += ","
		}
		out += b
	}
	return out + `],"stop_reason":"tool_use","usage":{"input_tokens":20,"output_tokens":5}}`
}

func antToolUse(id, name, input string) string {
	return `{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":` + input + `}`
}
