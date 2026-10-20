package assist

// strict_openai_test.go, a strict fake of an OpenAI compatible chat completions endpoint (port 0).
//
// It enforces: the Authorization and dealer tag headers equal what the test configured (and each
// appears exactly once); a tool message answers a tool_call id of the IMMEDIATELY preceding
// assistant message; every tool call of that message is answered before the next non-tool turn;
// function names match the documented pattern. A violation is answered with a 400 and recorded.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type strictOpenAI struct {
	srv       *httptest.Server
	wantAuth  string
	wantTag   string
	mu        sync.Mutex
	replies   []string
	requests  [][]byte
	headers   []http.Header
	violation []string
}

func newStrictOpenAI(t *testing.T, wantKey, wantTag string, replies ...string) *strictOpenAI {
	t.Helper()
	s := &strictOpenAI{wantAuth: "Bearer " + wantKey, wantTag: wantTag, replies: replies}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *strictOpenAI) URL() string { return s.srv.URL + "/v1" }

func (s *strictOpenAI) Requests() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.requests...)
}

func (s *strictOpenAI) Headers() []http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]http.Header(nil), s.headers...)
}

func (s *strictOpenAI) Violations() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.violation...)
}

func (s *strictOpenAI) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.requests = append(s.requests, body)
	s.headers = append(s.headers, r.Header.Clone())
	n := len(s.requests)
	v := ""
	if r.URL.Path != "/v1/chat/completions" {
		v = "unexpected path " + r.URL.Path
	} else if got := r.Header.Values("Authorization"); len(got) != 1 || got[0] != s.wantAuth {
		v = "authorization header is not the configured key"
	} else if got := r.Header.Values(HeaderDealerTag); len(got) != 1 || got[0] != s.wantTag {
		v = "dealer tag header is not the configured tag"
	} else {
		v = validateOpenAITurns(body)
	}
	if v != "" {
		s.violation = append(s.violation, v)
	}
	reply := s.replies[len(s.replies)-1]
	if n-1 < len(s.replies) {
		reply = s.replies[n-1]
	}
	s.mu.Unlock()
	w.Header().Set("content-type", "application/json")
	if v != "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":{"message":%q}}`, v)
		return
	}
	_, _ = w.Write([]byte(reply))
}

func validateOpenAITurns(body []byte) string {
	var req struct {
		Messages []struct {
			Role       string `json:"role"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID       string `json:"id"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return "unreadable body"
	}
	for i, t := range req.Tools {
		if !anthropicToolName.MatchString(t.Function.Name) {
			return fmt.Sprintf("tools.%d: function name %q does not match the pattern", i, t.Function.Name)
		}
	}
	pending := map[string]bool{}
	for i, m := range req.Messages {
		switch m.Role {
		case "tool":
			if !pending[m.ToolCallID] {
				return fmt.Sprintf("messages.%d: tool_call_id %q does not answer a tool call of the preceding assistant message", i, m.ToolCallID)
			}
			delete(pending, m.ToolCallID)
		default:
			if len(pending) > 0 {
				return fmt.Sprintf("messages.%d: tool calls left unanswered", i)
			}
			if m.Role == "assistant" {
				for _, c := range m.ToolCalls {
					pending[c.ID] = true
				}
			}
		}
	}
	if len(pending) > 0 {
		return "tool calls left unanswered at the end of the conversation"
	}
	return ""
}

func oaiTextReply(text string) string {
	b, _ := json.Marshal(text)
	return `{"choices":[{"message":{"role":"assistant","content":` + string(b) + `},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":4,"prompt_tokens_details":{"cached_tokens":5}}}`
}

func oaiToolReply(calls ...string) string {
	out := `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[`
	for i, c := range calls {
		if i > 0 {
			out += ","
		}
		out += c
	}
	return out + `]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":5}}`
}

func oaiToolCallJSON(id, name, arguments string) string {
	a, _ := json.Marshal(arguments)
	return `{"id":"` + id + `","type":"function","function":{"name":"` + name + `","arguments":` + string(a) + `}}`
}
