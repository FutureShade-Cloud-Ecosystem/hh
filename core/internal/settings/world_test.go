package settings

// The finance world sha256 row's parse and write-shape rules, without a
// database.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// worldSHA256Sample is a well formed digest for the shape legs: sixty four
// hex characters, lowercase.
const worldSHA256Sample = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestParseFinanceWorldSHA256(t *testing.T) {
	cases := []struct {
		name string
		raw  json.RawMessage
		want string
		bad  bool
	}{
		{name: "absent", raw: nil, want: ""},
		{name: "json null", raw: json.RawMessage(`null`), want: ""},
		{name: "a digest", raw: json.RawMessage(`"` + worldSHA256Sample + `"`), want: worldSHA256Sample},
		{name: "blank", raw: json.RawMessage(`"  "`), want: ""},
		{name: "not a string", raw: json.RawMessage(`7`), want: "", bad: true},
		{name: "short", raw: json.RawMessage(`"abcd"`), want: "", bad: true},
		{name: "not hex", raw: json.RawMessage(`"` + strings.Repeat("g", 64) + `"`), want: "", bad: true},
		{name: "a phrase", raw: json.RawMessage(`"a world"`), want: "", bad: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			badCalls := 0
			got := parseFinanceWorldSHA256(c.raw, func() { badCalls++ })
			if got != c.want {
				t.Fatalf("parseFinanceWorldSHA256(%s) = %q, want %q", c.raw, got, c.want)
			}
			if (badCalls > 0) != c.bad {
				t.Fatalf("parseFinanceWorldSHA256(%s) bad calls = %d, want bad %v", c.raw, badCalls, c.bad)
			}
		})
	}
}

// worldSHAWriter is a non nil write handle that records the calls it took:
// the shape rule is proved against a handle that is there, so the refusal
// cannot be the nil handle's refusal wearing the shape's name.
type worldSHAWriter struct {
	calls int
	sql   string
	args  []any
}

func (w *worldSHAWriter) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	w.calls++
	w.sql = sql
	w.args = args
	return pgconn.CommandTag{}, nil
}

func TestFinanceWorldSHA256NilReaderAnswersUnrecorded(t *testing.T) {
	var r *DealerConfigReader
	got, err := r.FinanceWorldSHA256(context.Background())
	if err != nil {
		t.Fatalf("nil reader: %v", err)
	}
	if got != "" {
		t.Fatalf("nil reader answered %q, want unrecorded", got)
	}
}

// The write path's two refusals, each named by the fault it is: a missing
// handle and a malformed digest. The shape is checked before the handle, so
// a malformed digest refuses as malformed on any handle, and a well formed
// digest on no handle refuses as the missing handle it is.
func TestFinanceWorldSHA256WriteRefusals(t *testing.T) {
	if err := WriteFinanceWorldSHA256Tx(context.Background(), nil, worldSHA256Sample); err == nil {
		t.Fatal("a write on a nil transaction must refuse")
	} else if !strings.Contains(err.Error(), "database handle") {
		t.Fatalf("a write on a nil transaction must refuse as the missing handle, got %v", err)
	}
	if err := WriteFinanceWorldSHA256Tx(context.Background(), nil, "not-a-digest"); err == nil {
		t.Fatal("a malformed digest must refuse")
	} else if !strings.Contains(err.Error(), "64 hex characters") {
		t.Fatalf("a malformed digest must refuse as malformed, got %v", err)
	}
}

// The same two refusals against a handle that is there: the shape rule
// refuses before the write, and a well formed digest reaches the write.
func TestFinanceWorldSHA256WriteShapeOnANonNilHandle(t *testing.T) {
	w := &worldSHAWriter{}
	if err := WriteFinanceWorldSHA256Tx(context.Background(), w, "not-a-digest"); err == nil {
		t.Fatal("a malformed digest must refuse")
	} else if !strings.Contains(err.Error(), "64 hex characters") {
		t.Fatalf("a malformed digest must refuse as malformed, got %v", err)
	}
	if w.calls != 0 {
		t.Fatalf("a malformed digest wrote %d times, want 0", w.calls)
	}
	if err := WriteFinanceWorldSHA256Tx(context.Background(), w, worldSHA256Sample); err != nil {
		t.Fatalf("a well formed digest must write: %v", err)
	}
	if w.calls != 1 {
		t.Fatalf("a well formed digest wrote %d times, want 1", w.calls)
	}
}

// The whole malformed vocabulary, not one sample of it: every digest that is
// not sixty four hex characters refuses, and a refusal never touches the
// database.
func TestFinanceWorldSHA256WriteRefusesEveryMalformedDigest(t *testing.T) {
	for _, sha := range []string{"", "ABC", strings.Repeat("0", 63), strings.Repeat("0", 65), strings.Repeat("z", 64), "a b"} {
		w := &worldSHAWriter{}
		if err := WriteFinanceWorldSHA256Tx(context.Background(), w, sha); err == nil {
			t.Errorf("WriteFinanceWorldSHA256Tx(%q) must refuse, never write", sha)
		} else if w.calls != 0 {
			t.Errorf("WriteFinanceWorldSHA256Tx(%q) refused but still touched the database", sha)
		}
	}
}

// The write's own shape: it goes through the owning table and carries the
// key and the JSON string value, so the row reads back as the digest. The
// stored word is lowercase however the caller spelled the digest, so the
// recorded world is one spelling.
func TestFinanceWorldSHA256WriteGoesThroughTheOwningTable(t *testing.T) {
	w := &worldSHAWriter{}
	if err := WriteFinanceWorldSHA256Tx(context.Background(), w, strings.ToUpper(worldSHA256Sample)); err != nil {
		t.Fatalf("WriteFinanceWorldSHA256Tx: %v", err)
	}
	if !strings.Contains(w.sql, "system_settings") {
		t.Fatalf("the write must go through the owning table, got: %s", w.sql)
	}
	if len(w.args) != 2 || w.args[0] != KeyFinanceWorldSHA256 || w.args[1] != `"`+worldSHA256Sample+`"` {
		t.Fatalf("the write must carry the key and the lowercase JSON string value, got: %v", w.args)
	}
}
