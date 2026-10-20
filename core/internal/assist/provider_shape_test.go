package assist

// provider_shape_test.go, TanyAI slice 1 sub item 1: the provider interface is modelled on the
// OpenAI compatible chat API. The shape is proven here with no network: roles, tool calls with a
// provider id, a tool message that names the id it answers, structured output as an option, usage
// with a cached count, and a streaming method that is declared and refuses by name.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestProviderShape_RolesCallsAndUsage(t *testing.T) {
	req := CompletionRequest{
		Messages: []Message{
			{Role: RoleSystem, Text: "rules"},
			{Role: RoleUser, Text: "where is SO-4?"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_1", Command: "order.lookup", Args: Args{"order_no": "SO-4"}}}},
			{Role: RoleTool, ToolCallID: "call_1", ToolName: "order.lookup", Text: "data"},
		},
		Schema: &OutputSchema{Name: "spec", Schema: json.RawMessage(`{"type":"object"}`)},
	}
	if req.Messages[2].ToolCalls[0].ID != "call_1" || req.Messages[3].ToolCallID != "call_1" {
		t.Fatal("a tool call carries its provider id and the tool message names the id it answers")
	}
	r := Reply{TokensIn: 10, TokensOut: 2, TokensCached: 7}
	if r.TokensCached != 7 {
		t.Fatal("usage carries cached tokens")
	}
}

// The stub moves to the new shape unchanged in behaviour: a system message in front of the
// conversation changes nothing it answers.
func TestProviderShape_StubIgnoresASystemMessage(t *testing.T) {
	p := NewStubProvider()
	tools := toolSpecsFor(NewRegistry(seededReaders().deps(), nil).List(context.Background(), manager()))
	with, err := p.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: RoleSystem, Text: "rules"}, {Role: RoleUser, Text: "where is order SO-4?"}},
		Tools:    tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	without, _ := p.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: RoleUser, Text: "where is order SO-4?"}},
		Tools:    tools,
	})
	if len(with.ToolCalls) != 1 || with.ToolCalls[0].Command != without.ToolCalls[0].Command {
		t.Fatalf("stub answers differently with a system message: %+v vs %+v", with, without)
	}
}

// Streaming is declared and not implemented: a named error, never a silent empty stream.
func TestProviderShape_StreamingIsDeclaredAndRefusesByName(t *testing.T) {
	for _, p := range []Provider{NewStubProvider(), NewAnthropicProvider("k", "m", "http://127.0.0.1:1")} {
		sp, ok := p.(StreamingProvider)
		if !ok {
			t.Fatalf("%T does not declare Stream", p)
		}
		if _, err := sp.Stream(context.Background(), CompletionRequest{}); !errors.Is(err, ErrStreamingUnsupported) {
			t.Fatalf("%T Stream err = %v, want ErrStreamingUnsupported", p, err)
		}
	}
}

// Tool calls and their ids are never read from a client: a history message from the wire cannot
// carry them, whatever the JSON says.
func TestProviderShape_ClientHistoryCannotCarryToolCalls(t *testing.T) {
	var m Message
	raw := `{"role":"assistant","text":"hi","tool_calls":[{"id":"x","command":"order.lookup"}],"tool_call_id":"x"}`
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if len(m.ToolCalls) != 0 || m.ToolCallID != "" {
		t.Fatalf("a client smuggled provider fields into a message: %+v", m)
	}
}
