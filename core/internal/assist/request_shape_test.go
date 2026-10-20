package assist

// request_shape_test.go, TanyAI slice 1 sub item 3: what the adapters really put on the wire for a
// full tool round trip, validated against the documented request shape written down as JSON Schema
// in testdata (each schema's $comment names the documentation page it follows).

import (
	"strings"
	"testing"
)

func TestAnthropicRequestShape_FullToolRoundTrip(t *testing.T) {
	srv := newStrictAnthropic(t,
		antToolReply(antToolUse("toolu_01A", "order__lookup", `{"order_no":"SO-4"}`)),
		antTextReply("SO-4 is picking."),
	)
	if _, err := askAgainstAnthropic(t, srv, "where is SO-4?", []Message{
		{Role: RoleUser, Text: "hello"}, {Role: RoleAssistant, Text: "hi, how can I help?"},
	}); err != nil {
		t.Fatal(err)
	}
	schema := loadSchema(t, "testdata/anthropic_messages_request.schema.json")
	reqs := srv.Requests()
	if len(reqs) != 2 {
		t.Fatalf("got %d requests, want the call and the call with results", len(reqs))
	}
	for i, body := range reqs {
		if errs := schema.Check(body); len(errs) != 0 {
			t.Errorf("request %d does not match the documented Messages request shape:\n  %s\nbody: %s", i, strings.Join(errs, "\n  "), body)
		}
	}
	if v := srv.Violations(); len(v) != 0 {
		t.Errorf("turn rules broken: %v", v)
	}
}
