package settings

// The box kind mark's parse and write-shape rules, without a database.

import (
	"context"
	"encoding/json"
	"testing"
)

func TestParseBoxKind(t *testing.T) {
	cases := []struct {
		name string
		raw  json.RawMessage
		want string
		bad  bool
	}{
		{name: "absent", raw: nil, want: ""},
		{name: "json null", raw: json.RawMessage(`null`), want: ""},
		{name: "a kind", raw: json.RawMessage(`"finance"`), want: "finance"},
		{name: "blank", raw: json.RawMessage(`"  "`), want: ""},
		{name: "not a string", raw: json.RawMessage(`7`), want: "", bad: true},
		{name: "uppercase", raw: json.RawMessage(`"Finance"`), want: "", bad: true},
		{name: "a phrase", raw: json.RawMessage(`"finance box"`), want: "", bad: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			badCalls := 0
			got := parseBoxKind(c.raw, func() { badCalls++ })
			if got != c.want {
				t.Fatalf("parseBoxKind(%s) = %q, want %q", c.raw, got, c.want)
			}
			if (badCalls > 0) != c.bad {
				t.Fatalf("parseBoxKind(%s) bad calls = %d, want bad %v", c.raw, badCalls, c.bad)
			}
		})
	}
}

func TestBoxKindNilReaderAnswersUnmarked(t *testing.T) {
	var r *DealerConfigReader
	got, err := r.BoxKind(context.Background())
	if err != nil {
		t.Fatalf("nil reader: %v", err)
	}
	if got != "" {
		t.Fatalf("nil reader answered %q, want unmarked", got)
	}
	if err := WriteBoxKindTx(context.Background(), nil, "finance"); err == nil {
		t.Fatal("a write on a nil transaction must refuse")
	}
	if err := WriteBoxKindTx(context.Background(), nil, "Finance Box"); err == nil {
		t.Fatal("a malformed kind must refuse")
	}
}
