package assist

// anthropic_roundtrip_test.go, TanyAI slice 1 sub item 2: the tool round trip against a STRICT fake
// of the Messages API (strict_anthropic_test.go). On main the adapter sent a tool_result whose id
// was invented from the command name, with no assistant tool_use turn before it; the strict server
// rejects exactly that.

import (
	"context"
	"strings"
	"testing"
)

func askAgainstAnthropic(t *testing.T, srv *strictAnthropic, question string, history []Message) (Answer, error) {
	t.Helper()
	p := NewAnthropicProvider("test-key", "claude-sonnet-5-5", srv.URL())
	svc := chatService(p, &fakeLedger{}, newFakeLimits(), nil, seededReaders())
	return svc.Ask(context.Background(), manager(), question, history)
}

func TestAnthropicRoundTrip_RealIdsAreCarried(t *testing.T) {
	srv := newStrictAnthropic(t,
		antToolReply(antToolUse("toolu_01A", "order.lookup", `{"order_no":"SO-4"}`)),
		antTextReply("SO-4 is picking at Kelowna."),
	)
	ans, err := askAgainstAnthropic(t, srv, "where is SO-4?", nil)
	if err != nil {
		t.Fatal(err)
	}
	if v := srv.Violations(); len(v) != 0 {
		t.Fatalf("the adapter broke the Messages contract: %v", v)
	}
	if ans.Text != "SO-4 is picking at Kelowna." {
		t.Fatalf("text = %q, want the model's sentence from the second call", ans.Text)
	}
	reqs := srv.Requests()
	if len(reqs) != 2 {
		t.Fatalf("got %d provider calls, want 2", len(reqs))
	}
	if !strings.Contains(string(reqs[1]), `"tool_use_id":"toolu_01A"`) || !strings.Contains(string(reqs[1]), `"id":"toolu_01A"`) {
		t.Fatalf("the second request does not carry the real id on both turns: %s", reqs[1])
	}
	for _, r := range reqs {
		if strings.Contains(string(r), "call_order.lookup") {
			t.Fatalf("an invented call_<command> id is still sent: %s", r)
		}
	}
}

func TestAnthropicRoundTrip_TwoCallsInOneTurnAreBothAnswered(t *testing.T) {
	srv := newStrictAnthropic(t,
		antToolReply(
			antToolUse("toolu_A", "order.lookup", `{"order_no":"SO-4"}`),
			antToolUse("toolu_B", "order.lookup", `{"order_no":"SO-5"}`),
		),
		antTextReply("Both found."),
	)
	ans, err := askAgainstAnthropic(t, srv, "where are SO-4 and SO-5?", nil)
	if err != nil {
		t.Fatal(err)
	}
	if v := srv.Violations(); len(v) != 0 {
		t.Fatalf("contract broken: %v", v)
	}
	if ans.Text != "Both found." || len(ans.Results) != 2 {
		t.Fatalf("answer = %q with %d results", ans.Text, len(ans.Results))
	}
}

func TestAnthropicRoundTrip_MoreThanOneToolRound(t *testing.T) {
	srv := newStrictAnthropic(t,
		antToolReply(antToolUse("toolu_1", "order.lookup", `{"order_no":"SO-4"}`)),
		antToolReply(antToolUse("toolu_2", "order.lookup", `{"order_no":"SO-5"}`)),
		antTextReply("Checked both."),
	)
	ans, err := askAgainstAnthropic(t, srv, "check SO-4 then SO-5", nil)
	if err != nil {
		t.Fatal(err)
	}
	if v := srv.Violations(); len(v) != 0 {
		t.Fatalf("contract broken: %v", v)
	}
	if ans.Text != "Checked both." || len(ans.Results) != 2 || len(ans.Ran) != 2 {
		t.Fatalf("answer = %q results %d ran %d, want two rounds of results and the model's sentence", ans.Text, len(ans.Results), len(ans.Ran))
	}
}

// A model that never stops asking for tools is stopped at the bound and the answer is built from
// the results already in hand.
func TestAnthropicRoundTrip_LoopIsBounded(t *testing.T) {
	srv := newStrictAnthropic(t, antToolReply(antToolUse("toolu_x", "order.lookup", `{"order_no":"SO-4"}`)))
	ans, err := askAgainstAnthropic(t, srv, "where is SO-4?", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(srv.Requests()); got != maxToolRounds {
		t.Fatalf("provider called %d times, want exactly the bound %d", got, maxToolRounds)
	}
	if len(ans.Results) == 0 || strings.TrimSpace(ans.Text) == "" {
		t.Fatalf("over the bound the answer must come from the results, got %+v", ans)
	}
}

// A history whose assistant turn was empty must not leave two user turns side by side.
func TestAnthropicRoundTrip_NoConsecutiveSameRoleTurns(t *testing.T) {
	srv := newStrictAnthropic(t, antTextReply("ok"))
	hist := []Message{
		{Role: RoleUser, Text: "hi"},
		{Role: RoleAssistant, Text: ""},
		{Role: RoleAssistant, Text: "hello"},
		{Role: RoleAssistant, Text: "again"},
	}
	if _, err := askAgainstAnthropic(t, srv, "and now?", hist); err != nil {
		t.Fatal(err)
	}
	if v := srv.Violations(); len(v) != 0 {
		t.Fatalf("contract broken: %v", v)
	}
}

// A client history that opens with an assistant turn is dropped to a legal start.
func TestAnthropicRoundTrip_HistoryMustStartWithAUserTurn(t *testing.T) {
	srv := newStrictAnthropic(t, antTextReply("ok"))
	hist := []Message{{Role: RoleAssistant, Text: "earlier answer"}}
	if _, err := askAgainstAnthropic(t, srv, "and now?", hist); err != nil {
		t.Fatal(err)
	}
	if v := srv.Violations(); len(v) != 0 {
		t.Fatalf("contract broken: %v", v)
	}
}

func TestAnthropicDefaultModelIsSonnet55(t *testing.T) {
	if got := NewAnthropicProvider("k", "", "").Model(); got != "claude-sonnet-5-5" {
		t.Fatalf("default model = %q, want claude-sonnet-5-5", got)
	}
}
