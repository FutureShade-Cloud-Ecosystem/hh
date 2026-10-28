package finstmt

// export_audit_test.go, the export access trail's unit leg: a failed audit
// must be LOGGED LOUDLY (Error level, the report named), never dropped at
// Warn where it reads as noise (round 4, S2's P1: the export audit was best
// effort AND always failing, since a report name is not a uuid).

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
)

// failingRecorder refuses every record, the stand in for a broken audit path.
type failingRecorder struct{}

func (failingRecorder) Record(_ context.Context, _ audit.SensitiveAccess) error {
	return errors.New("audit: the insert failed")
}

func TestRecordExportFailureIsLoggedAtError(t *testing.T) {
	var buf bytes.Buffer
	svc := NewService(nil).
		WithAudit(failingRecorder{}).
		withLogger(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	svc.recordExport(context.Background(), "INCOME_STATEMENT", "csv", "staff-user", "")

	if !strings.Contains(buf.String(), "level=ERROR") {
		t.Fatalf("the failed export audit was logged at %q, want an ERROR line: %s", levelOf(buf.String()), buf.String())
	}
	if !strings.Contains(buf.String(), "INCOME_STATEMENT") {
		t.Fatalf("the ERROR line does not name the report: %s", buf.String())
	}
}

// levelOf pulls the level= value out of a one line text log, for the failure message.
func levelOf(line string) string {
	i := strings.Index(line, "level=")
	if i < 0 {
		return "(no level)"
	}
	rest := line[i+6:]
	if j := strings.Index(rest, " "); j >= 0 {
		return rest[:j]
	}
	return strings.TrimSpace(rest)
}
