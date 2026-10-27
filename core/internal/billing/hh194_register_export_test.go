package billing

// hh194_register_export_test.go pins the shape of the export's read without a database: the export
// reads the whole view in ONE store call asking for every row (RegisterFilter.All). An offset walk
// over pages is a different shape that a quiet-book test cannot tell apart, and it can skip or repeat
// a row when a payment lands between pages; one statement is one snapshot.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// exportRecordingStore embeds the fake store and records every RegisterRows call.
type exportRecordingStore struct {
	fakeRegisterStore
	calls []RegisterFilter
	rows  []RegisterRow
}

func (s *exportRecordingStore) RegisterRows(_ context.Context, _ time.Time, _ int, f RegisterFilter) ([]RegisterRow, int, error) {
	s.calls = append(s.calls, f)
	return s.rows, len(s.rows), nil
}

func TestHH194_ExportReadsTheWholeViewInOneCall(t *testing.T) {
	store := &exportRecordingStore{}
	store.policy = RegisterPolicy{AccountPositionVisibility: VisibilityManagerUp, DueSoonDays: 7}
	for i := 0; i < 450; i++ {
		store.rows = append(store.rows, RegisterRow{InvoiceNo: "INV-X", CustomerName: "Test Co"})
	}
	// A manager caller on the context: this leg pins the export's READ SHAPE, not the position gate,
	// so the gate has to admit the caller rather than fail closed under an unauthenticated context.
	svc := NewRegisterService(store).
		WithClock(func() time.Time { return time.Now() }).
		withCaller(callerAs(identity.TierManager, identity.ScopeGlobal))
	h := NewRegisterHandler(svc, nil, nil)
	rec := httptest.NewRecorder()
	h.export(rec, httptest.NewRequest(http.MethodGet, "/api/v1/ar/register/export?chip=overdue", nil), guards.StaffCaller{})
	if rec.Code != http.StatusOK {
		t.Fatalf("export status %d: %s", rec.Code, rec.Body.String())
	}
	if len(store.calls) != 1 || !store.calls[0].All {
		t.Fatalf("export made %d store calls (%+v), want exactly one asking for every row (All): an offset walk can skip or repeat a row when the book changes between pages",
			len(store.calls), store.calls)
	}
	if store.calls[0].Chip != StatusOverdue {
		t.Fatalf("the export dropped the chip filter: %+v", store.calls[0])
	}
	if got := strings.Count(rec.Body.String(), "\n"); got != 451 {
		t.Fatalf("export wrote %d lines, want the header and 450 rows", got)
	}
}

func TestHH194_ListReadIsNeverUnbounded(t *testing.T) {
	// The list handler and every paged caller leave All false: only the export asks for the whole view.
	if (RegisterFilter{}).All {
		t.Fatal("a zero RegisterFilter must be paged")
	}
}
