package settings

// box_kind, the finance box's own key in system_settings: shape rules and the
// write seam, unit side. The database round trip lives in
// box_kind_integration_test.go.

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// boxExecRecorder is the least DealerConfigExecer double: it remembers the
// statement and the arguments it was handed.
type boxExecRecorder struct {
	sql  string
	args []any
	err  error
}

func (r *boxExecRecorder) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	r.sql = sql
	r.args = args
	if r.err != nil {
		return pgconn.CommandTag{}, r.err
	}
	return pgconn.CommandTag{}, nil
}

func TestWriteBoxKindTx_RefusesMalformedKinds(t *testing.T) {
	for _, kind := range []string{"", "FINANCE", "a b", "-finance", "finance!"} {
		rec := &boxExecRecorder{}
		if err := WriteBoxKindTx(context.Background(), rec, kind); err == nil {
			t.Errorf("WriteBoxKindTx(%q) must refuse, never write", kind)
		} else if rec.sql != "" {
			t.Errorf("WriteBoxKindTx(%q) refused but still touched the database", kind)
		}
	}
}

func TestWriteBoxKindTx_WritesJsonbThroughTheOwningTable(t *testing.T) {
	rec := &boxExecRecorder{}
	if err := WriteBoxKindTx(context.Background(), rec, "finance"); err != nil {
		t.Fatalf("WriteBoxKindTx: %v", err)
	}
	if !strings.Contains(rec.sql, "system_settings") {
		t.Fatalf("the write must go through the owning table, got: %s", rec.sql)
	}
	if len(rec.args) != 2 || rec.args[0] != KeyBoxKind || rec.args[1] != `"finance"` {
		t.Fatalf("the write must carry the key and the JSON string value, got: %v", rec.args)
	}
}
