package load

// doors_handler_test.go covers the submit boundary's answers that need no
// database: the machine scope's absence is the one 403 body, a malformed
// body and a list over the bound are 400 before any store call, the
// batch level error mapping names the retryable contention answer, and
// the wire shape carries the attached outcome word the map doors answer.
// The doors are built over a nil pool store on purpose: if any of these
// paths reached the pool the test would answer a 500 and fail, which is
// the assertion's teeth (the same idiom handler_test.go carries).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/jackc/pgx/v5"
)

// noopAudit keeps the test doors constructable: the boundary answers
// under test stand or fall before any audit sink is reached.
type noopAudit struct{}

func (noopAudit) RecordBatchTx(_ context.Context, _ pgx.Tx, _ BatchAuditRow) error { return nil }

// testDoorsHandler builds the handler over one branch map door whose
// store holds no pool.
func testDoorsHandler(t *testing.T) *DoorsHandler {
	t.Helper()
	return NewDoorsHandler(testLogger(t), map[string]*Door{
		"PUT /api/v1/load/branches": NewDoor(NewStore(nil), KindBranch, &BranchMapWriter{}, noopAudit{}),
	})
}

func postSubmit(t *testing.T, h *DoorsHandler, scoped bool, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/load/branches", bytes.NewReader(body))
	if scoped {
		req = withScope(req)
	}
	rec := httptest.NewRecorder()
	h.submit(rec, req, h.routes["PUT /api/v1/load/branches"])
	return rec
}

func TestSubmitRefusesAMissingScope(t *testing.T) {
	h := testDoorsHandler(t)
	rec := postSubmit(t, h, false, []byte(`{"source_system":"testsuite","batch_id":"b-1","items":[{"legacy_id":"B-1","record":{"code":"FXB1"}}]}`))
	if rec.Code != http.StatusForbidden || rec.Body.String() != string(authz.BodyForbidden) {
		t.Fatalf("a submit with no machine scope answered %d %q, want the one 403 body", rec.Code, rec.Body.String())
	}
}

func TestSubmitRefusesAMalformedBodyAndAnOverboundList(t *testing.T) {
	h := testDoorsHandler(t)
	if rec := postSubmit(t, h, true, []byte(`{not json`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("a malformed body answered %d, want 400", rec.Code)
	}
	var b bytes.Buffer
	b.WriteString(`{"source_system":"testsuite","batch_id":"b-1","items":[`)
	for i := 0; i < MaxBatchItems+1; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(fmt.Sprintf(`{"legacy_id":"B-%d","record":{"code":"C"}}`, i))
	}
	b.WriteString(`]}`)
	if rec := postSubmit(t, h, true, b.Bytes()); rec.Code != http.StatusBadRequest {
		t.Fatalf("a list over the bound answered %d, want 400", rec.Code)
	}
	// An item with no legacy id is the boundary's 400 too.
	if rec := postSubmit(t, h, true, []byte(`{"source_system":"testsuite","batch_id":"b-1","items":[{"record":{"code":"C"}}]}`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("an item with no legacy id answered %d, want 400", rec.Code)
	}
}

func TestSubmitErrorMapsTheContentionSentinelToARetryableAnswer(t *testing.T) {
	if status, _ := submitErrorStatus(fmt.Errorf("wrapped: %w", ErrBatchContended)); status != http.StatusServiceUnavailable {
		t.Fatalf("a contended batch mapped to %d, want the retryable 503", status)
	}
	if status, _ := submitErrorStatus(ErrBatchDoorMismatch); status != http.StatusConflict {
		t.Fatalf("a cross door replay mapped to %d, want 409", status)
	}
	if status, _ := submitErrorStatus(ErrBatchBound); status != http.StatusBadRequest {
		t.Fatalf("an overbound list mapped to %d, want 400", status)
	}
	if status, _ := submitErrorStatus(ErrMissingField); status != http.StatusBadRequest {
		t.Fatalf("a shape refusal mapped to %d, want 400", status)
	}
	if status, _ := submitErrorStatus(errors.New("infrastructure")); status != http.StatusInternalServerError {
		t.Fatalf("an infrastructure failure mapped to %d, want 500", status)
	}
}

func TestSubmitResultWireCarriesTheAttachedOutcome(t *testing.T) {
	r := BatchResult{BatchID: "b-wire-1", Counts: map[string]int{
		OutcomeCreated: 0, OutcomeAttached: 2, OutcomeUpdated: 0, OutcomeUnchanged: 0, OutcomeRefused: 0,
	}, Items: []Outcome{
		{LegacyID: "B-1", Status: OutcomeAttached, HHID: "4442a800-0000-4000-8000-000000000b01"},
		{LegacyID: "B-2", Status: OutcomeAttached, HHID: "4442a800-0000-4000-8000-000000000b02"},
	}}
	raw, err := json.Marshal(submitResultWire(r))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"attached":2`, `"status":"attached"`, `"batch_id":"b-wire-1"`} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Fatalf("the submit answer %s does not carry %s", raw, want)
		}
	}
}
