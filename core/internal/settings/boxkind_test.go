package settings

// The box kind mark's parse and write-shape rules, without a database.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
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

// boxKindWriter is a non nil write handle that records the calls it took:
// the shape rule is proved against a handle that is there, so the refusal
// cannot be the nil handle's refusal wearing the shape's name. It remembers
// the statement and the arguments too, so the write's own shape (the owning
// table, the key, the JSON string value) is proved as well.
type boxKindWriter struct {
	calls int
	sql   string
	args  []any
}

func (w *boxKindWriter) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	w.calls++
	w.sql = sql
	w.args = args
	return pgconn.CommandTag{}, nil
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
}

// The write path's two refusals, each named by the fault it is: a missing
// handle and a malformed kind. The shape is checked before the handle, so a
// malformed kind refuses as malformed on any handle, and a well shaped kind
// on no handle refuses as the missing handle it is.
func TestBoxKindWriteRefusals(t *testing.T) {
	if err := WriteBoxKindTx(context.Background(), nil, "finance"); err == nil {
		t.Fatal("a write on a nil transaction must refuse")
	} else if !strings.Contains(err.Error(), "database handle") {
		t.Fatalf("a write on a nil transaction must refuse as the missing handle, got %v", err)
	}
	if err := WriteBoxKindTx(context.Background(), nil, "Finance Box"); err == nil {
		t.Fatal("a malformed kind must refuse")
	} else if !strings.Contains(err.Error(), "short lowercase word") {
		t.Fatalf("a malformed kind must refuse as malformed, got %v", err)
	}
}

// The same two refusals against a handle that is there: the shape rule
// refuses before the write, and a well shaped kind reaches the write.
func TestBoxKindWriteShapeOnANonNilHandle(t *testing.T) {
	w := &boxKindWriter{}
	if err := WriteBoxKindTx(context.Background(), w, "Finance Box"); err == nil {
		t.Fatal("a malformed kind must refuse")
	} else if !strings.Contains(err.Error(), "short lowercase word") {
		t.Fatalf("a malformed kind must refuse as malformed, got %v", err)
	}
	if w.calls != 0 {
		t.Fatalf("a malformed kind wrote %d times, want 0", w.calls)
	}
	if err := WriteBoxKindTx(context.Background(), w, "finance"); err != nil {
		t.Fatalf("a well shaped kind must write: %v", err)
	}
	if w.calls != 1 {
		t.Fatalf("a well shaped kind wrote %d times, want 1", w.calls)
	}
}

// The whole malformed vocabulary, not one sample of it: every kind that is
// not a short lowercase word refuses, and a refusal never touches the
// database. Carried over from the other branch's copy of this seam, whose
// callers needed the wider set proved.
func TestBoxKindWriteRefusesEveryMalformedKind(t *testing.T) {
	for _, kind := range []string{"", "FINANCE", "a b", "-finance", "finance!"} {
		w := &boxKindWriter{}
		if err := WriteBoxKindTx(context.Background(), w, kind); err == nil {
			t.Errorf("WriteBoxKindTx(%q) must refuse, never write", kind)
		} else if w.calls != 0 {
			t.Errorf("WriteBoxKindTx(%q) refused but still touched the database", kind)
		}
	}
}

// The write's own shape: it goes through the owning table and carries the
// key and the JSON string value, so the row reads back as the word. Carried
// over from the other branch's copy of this seam.
func TestBoxKindWriteGoesThroughTheOwningTable(t *testing.T) {
	w := &boxKindWriter{}
	if err := WriteBoxKindTx(context.Background(), w, "finance"); err != nil {
		t.Fatalf("WriteBoxKindTx: %v", err)
	}
	if !strings.Contains(w.sql, "system_settings") {
		t.Fatalf("the write must go through the owning table, got: %s", w.sql)
	}
	if len(w.args) != 2 || w.args[0] != KeyBoxKind || w.args[1] != `"finance"` {
		t.Fatalf("the write must carry the key and the JSON string value, got: %v", w.args)
	}
}
