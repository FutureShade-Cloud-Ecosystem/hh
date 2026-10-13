package assist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/dealer"
)

// sentSystemPrompt runs one turn against a stub Anthropic endpoint and returns the system prompt
// that went over the wire.
func sentSystemPrompt(t *testing.T) string {
	t.Helper()
	var system string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			System string `json:"system"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		system = body.System
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()
	p := NewAnthropicProvider("test-key", "test-model", srv.URL)
	if _, err := p.Complete(context.Background(), CompletionRequest{Messages: []Message{{Role: "user", Text: "hi"}}}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	return system
}

// The assistant is told which dealer it works for from the dealer's own configuration.
func TestSystemPrompt_ReadsTheConfiguredDealerName(t *testing.T) {
	t.Cleanup(func() { dealer.Set("") })
	dealer.Set("Gable Landscape Supply")
	got := sentSystemPrompt(t)
	if !strings.Contains(got, "managers at Gable Landscape Supply.") {
		t.Errorf("system prompt does not name the configured dealer: %q", got)
	}
	if strings.Contains(strings.ToLower(got), "dibbits") {
		t.Error("system prompt carries a dealer's name from code")
	}
}

// With no dealer name set it says "the dealer", never another dealer's name.
func TestSystemPrompt_NeutralWhenNoDealerName(t *testing.T) {
	dealer.Set("")
	got := sentSystemPrompt(t)
	if strings.Contains(strings.ToLower(got), "dibbits") || !strings.Contains(got, "managers at the dealer.") {
		t.Errorf("system prompt = %q, want the neutral phrase", got)
	}
}
