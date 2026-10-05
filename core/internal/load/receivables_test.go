package load

// receivables_test.go covers the open receivables door's boundary
// (HH-LOAD-1 LD-11): the record's own rules, money above all. Money is
// integer cents and the door refuses a record whose money field is not
// exact cents, whatever spelling the wire tried; the references are the
// customer and the branch by legacy key, and nothing else; the batch
// names each open item once. The ledger order rule (per customer, by
// issue date) and the outcome remap back to wire order are pinned here
// too, and the handler's error mapping table, so the money door's batch
// level answers (the refused batch, the contended batch) carry the
// statuses the contract promises. Every fixture is invented.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// receivablesRecord builds one receivable record as the wire decoder
// hands it to the door: money as json.Number, the exact-cents shape.
// The amount parameter is any so the refusal cases can hand the door
// every other spelling too.
func receivablesRecord(docNo, issue, due string, amount any, job, address string) map[string]any {
	rec := map[string]any{
		"source_doc_no":     docNo,
		"issue_date":        issue,
		"due_date":          due,
		"open_amount_cents": amount,
	}
	if job != "" {
		rec["source_job_key"] = job
	}
	if address != "" {
		rec["source_address_key"] = address
	}
	return rec
}

// receivablesWireItem is one item as the door receives it, the customer
// and branch named by legacy key.
func receivablesWireItem(legacyID, derivedKey, customerKey, branchKey string, rec map[string]any) Item {
	return Item{
		LegacyID:   legacyID,
		DerivedKey: derivedKey,
		Record:     rec,
		Requires: []Ref{
			{Kind: RefKindCustomer, LegacyID: customerKey},
			{Kind: RefKindBranch, LegacyID: branchKey},
		},
	}
}

// goodReceivablesRecord is one fully valid record, the shape every
// other case starts from.
func goodReceivablesRecord() map[string]any {
	return receivablesRecord("AR-1001", "2021-03-04", "2021-04-03", json.Number("125000"), "JOB-77", "ADDR-91")
}

func TestReceivablesRecordAcceptsExactCentsAndRefusesEveryOtherMoneySpelling(t *testing.T) {
	ok := []json.Number{"125000", "-2500", "1", "9223372036854775807", "-9223372036854775808"}
	for _, amount := range ok {
		rec := receivablesRecord("AR-1001", "2021-03-04", "2021-04-03", amount, "", "")
		if err := ValidateReceivablesItems([]Item{receivablesWireItem("AR-L-1", "", "CUST-1", "BR-1", rec)}); err != nil {
			t.Fatalf("exact cents %s refused: %v", amount, err)
		}
	}
	bad := []struct {
		name   string
		amount any
	}{
		{"a fraction of a cent", json.Number("100.5")},
		{"a fraction spelled small", json.Number("12.50")},
		{"an exponent", json.Number("1e3")},
		{"a float the decoder would make", float64(125000)},
		{"a whole float", float64(100)},
		{"an integer spelled as text", "125000"},
		{"zero", json.Number("0")},
		{"zero with a sign", json.Number("-0")},
		{"a fraction of zero", json.Number("0.00")},
		{"absent", nil},
	}
	for _, tc := range bad {
		rec := receivablesRecord("AR-1001", "2021-03-04", "2021-04-03", tc.amount, "", "")
		err := ValidateReceivablesItems([]Item{receivablesWireItem("AR-L-1", "", "CUST-1", "BR-1", rec)})
		var ie *ItemError
		if err == nil || !errors.As(err, &ie) {
			t.Fatalf("%s answered %v, want an item error", tc.name, err)
		}
		if ie.Code != ReasonRecordShape || ie.Field != "open_amount_cents" {
			t.Fatalf("%s answered %+v, want %s on open_amount_cents", tc.name, ie, ReasonRecordShape)
		}
		if ie.LegacyID != "AR-L-1" {
			t.Fatalf("%s named legacy id %q, want the item's", tc.name, ie.LegacyID)
		}
	}
}

func TestReceivablesRecordRefusesShapesMoneyNeeds(t *testing.T) {
	cases := []struct {
		name  string
		fixup func(rec map[string]any) map[string]any
		field string
	}{
		{"no source document number", func(r map[string]any) map[string]any {
			delete(r, "source_doc_no")
			return r
		}, "source_doc_no"},
		{"an empty source document number", func(r map[string]any) map[string]any {
			r["source_doc_no"] = ""
			return r
		}, "source_doc_no"},
		{"an overlong source document number", func(r map[string]any) map[string]any {
			r["source_doc_no"] = string(bytes.Repeat([]byte("d"), 129))
			return r
		}, "source_doc_no"},
		{"no issue date", func(r map[string]any) map[string]any {
			delete(r, "issue_date")
			return r
		}, "issue_date"},
		{"an unparseable issue date", func(r map[string]any) map[string]any {
			r["issue_date"] = "not-a-date"
			return r
		}, "issue_date"},
		{"a loosely padded issue date", func(r map[string]any) map[string]any {
			r["issue_date"] = "2021-3-4"
			return r
		}, "issue_date"},
		{"no due date", func(r map[string]any) map[string]any {
			delete(r, "due_date")
			return r
		}, "due_date"},
		{"an impossible due date", func(r map[string]any) map[string]any {
			r["due_date"] = "2021-02-30"
			return r
		}, "due_date"},
		{"a date with a time on it", func(r map[string]any) map[string]any {
			r["due_date"] = "2021-04-03T00:00:00Z"
			return r
		}, "due_date"},
		{"an overlong job key", func(r map[string]any) map[string]any {
			r["source_job_key"] = string(bytes.Repeat([]byte("j"), 513))
			return r
		}, "source_job_key"},
		{"an overlong address key", func(r map[string]any) map[string]any {
			r["source_address_key"] = string(bytes.Repeat([]byte("a"), 513))
			return r
		}, "source_address_key"},
		{"a field the door does not carry", func(r map[string]any) map[string]any {
			r["payment_terms_days"] = json.Number("30")
			return r
		}, "payment_terms_days"},
	}
	for _, tc := range cases {
		rec := tc.fixup(goodReceivablesRecord())
		err := ValidateReceivablesItems([]Item{receivablesWireItem("AR-L-1", "", "CUST-1", "BR-1", rec)})
		var ie *ItemError
		if err == nil || !errors.As(err, &ie) {
			t.Fatalf("%s answered %v, want an item error", tc.name, err)
		}
		if ie.Code != ReasonRecordShape || ie.Field != tc.field {
			t.Fatalf("%s answered %+v, want %s on %s", tc.name, ie, ReasonRecordShape, tc.field)
		}
	}
	// A non object record is the same refusal on the whole record.
	err := ValidateReceivablesItems([]Item{{LegacyID: "AR-L-1", Record: nil,
		Requires: []Ref{{Kind: RefKindCustomer, LegacyID: "CUST-1"}, {Kind: RefKindBranch, LegacyID: "BR-1"}}}})
	var ie *ItemError
	if err == nil || !errors.As(err, &ie) || ie.Code != ReasonRecordShape {
		t.Fatalf("an absent record answered %v, want a record shape item error", err)
	}
}

func TestReceivablesItemsNameTheCustomerAndTheBranchAndNothingElse(t *testing.T) {
	base := func(reqs ...Ref) Item {
		return Item{LegacyID: "AR-L-1", Record: goodReceivablesRecord(), Requires: reqs}
	}
	cases := []struct {
		name string
		item Item
	}{
		{"no references at all", base()},
		{"a customer alone", base(Ref{Kind: RefKindCustomer, LegacyID: "CUST-1"})},
		{"a branch alone", base(Ref{Kind: RefKindBranch, LegacyID: "BR-1"})},
		{"a job reference, which rides on the record instead", base(
			Ref{Kind: RefKindCustomer, LegacyID: "CUST-1"},
			Ref{Kind: RefKindBranch, LegacyID: "BR-1"},
			Ref{Kind: "job", LegacyID: "JOB-77"})},
		{"a second customer", base(
			Ref{Kind: RefKindCustomer, LegacyID: "CUST-1"},
			Ref{Kind: RefKindCustomer, LegacyID: "CUST-2"},
			Ref{Kind: RefKindBranch, LegacyID: "BR-1"})},
		{"an unnamed reference kind", base(
			Ref{Kind: "", LegacyID: "CUST-1"},
			Ref{Kind: RefKindBranch, LegacyID: "BR-1"})},
	}
	for _, tc := range cases {
		err := ValidateReceivablesItems([]Item{tc.item})
		var ie *ItemError
		if err == nil || !errors.As(err, &ie) {
			t.Fatalf("%s answered %v, want an item error", tc.name, err)
		}
		if ie.Code != ReasonRecordShape || ie.Field != "requires" {
			t.Fatalf("%s answered %+v, want %s on requires", tc.name, ie, ReasonRecordShape)
		}
	}
	if err := ValidateReceivablesItems([]Item{base(
		Ref{Kind: RefKindCustomer, LegacyID: "CUST-1"},
		Ref{Kind: RefKindBranch, LegacyID: "BR-1"})}); err != nil {
		t.Fatalf("the customer and the branch alone refused: %v", err)
	}
}

func TestReceivablesItemsRefuseADuplicateLegacyID(t *testing.T) {
	items := []Item{
		receivablesWireItem("AR-L-1", "", "CUST-1", "BR-1", goodReceivablesRecord()),
		receivablesWireItem("AR-L-2", "", "CUST-2", "BR-1", goodReceivablesRecord()),
		receivablesWireItem("AR-L-1", "", "CUST-1", "BR-1", receivablesRecord("AR-1009", "2021-03-09", "2021-04-08", json.Number("500"), "", "")),
	}
	err := ValidateReceivablesItems(items)
	var ie *ItemError
	if err == nil || !errors.As(err, &ie) {
		t.Fatalf("a duplicated legacy id answered %v, want an item error", err)
	}
	if ie.Code != ReasonRecordShape || ie.Field != "legacy_id" || ie.LegacyID != "AR-L-1" {
		t.Fatalf("the duplicate answered %+v, want %s on legacy_id naming AR-L-1", ie, ReasonRecordShape)
	}
}

// TestReceivablesItemsOrderForTheLedger pins the ledger's date order
// rule: the door writes each customer's items oldest first, whatever
// order the wire carried them in, so the running balances read in date
// order; the outcomes still answer in the wire's order.
func TestReceivablesItemsOrderForTheLedger(t *testing.T) {
	rec := func(docNo, issue string) map[string]any {
		return receivablesRecord(docNo, issue, "2021-05-01", json.Number("1000"), "", "")
	}
	items := []Item{
		receivablesWireItem("AR-L-B2", "", "CUST-B", "BR-1", rec("AR-B2", "2021-03-10")),
		receivablesWireItem("AR-L-A2", "", "CUST-A", "BR-1", rec("AR-A2", "2021-03-08")),
		receivablesWireItem("AR-L-B1", "", "CUST-B", "BR-1", rec("AR-B1", "2021-03-02")),
		receivablesWireItem("AR-L-A1", "", "CUST-A", "BR-1", rec("AR-A1", "2021-03-05")),
	}
	ordered, wire := orderReceivablesItems(items)
	want := []string{"AR-L-A1", "AR-L-A2", "AR-L-B1", "AR-L-B2"}
	if len(ordered) != len(want) {
		t.Fatalf("the ordered list holds %d items, want %d", len(ordered), len(want))
	}
	for i, id := range want {
		if ordered[i].LegacyID != id {
			t.Fatalf("ordered position %d is %s, want %s (the ledger writes each customer oldest first)", i, ordered[i].LegacyID, id)
		}
	}
	if wire["AR-L-B2"] != 0 || wire["AR-L-A2"] != 1 || wire["AR-L-B1"] != 2 || wire["AR-L-A1"] != 3 {
		t.Fatalf("the wire index map lost the wire order: %v", wire)
	}
}

// receivablesBody builds one PUT body around the item JSON given.
func receivablesBody(batchID, itemsJSON string) []byte {
	return []byte(`{"source_system":"testsuite","batch_id":"` + batchID + `","items":[` + itemsJSON + `]}`)
}

// oneItemJSON is one valid item's JSON with the open amount slot left
// open, so the money spellings vary.
func oneItemJSON(amount string) string {
	return `{"legacy_id":"AR-L-1","derived_key":"","requires":[{"kind":"customer","legacy_id":"CUST-1"},{"kind":"branch","legacy_id":"BR-1"}],` +
		`"record":{"source_doc_no":"AR-1001","issue_date":"2021-03-04","due_date":"2021-04-03","open_amount_cents":` + amount + `}}`
}

// putReceivablesBody drives the handler's PUT with a machine scope
// stamped, the shape a request carries past the gate.
func putReceivablesBody(t *testing.T, h *Handler, scoped bool, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/load/receivables", bytes.NewReader(body))
	if scoped {
		req = req.WithContext(authz.WithMachineScope(req.Context(), authz.MachineScope{
			PrincipalID: "5e1f0000-0000-4000-8000-1f00000000e2",
			OrgID:       testScopeOrg,
			OrgSlug:     "fixture-load",
		}))
	}
	rec := httptest.NewRecorder()
	h.putReceivables(rec, req)
	return rec
}

// nilPoolReceivablesHandler is the boundary test's handler: the store
// over a nil pool refuses any batch that passes validation, so a 400
// proves validation refused and a 500 proves validation passed.
func nilPoolReceivablesHandler(t *testing.T) *Handler {
	t.Helper()
	store := NewStore(nil)
	return NewHandler(store, testLogger(t)).WithReceivables(
		NewReceivablesDoor(store, refusingWriter{}, noopAuditSink{}))
}

func TestPutReceivablesRefusesMoneyThatIsNotExactCentsAtTheBoundary(t *testing.T) {
	h := nilPoolReceivablesHandler(t)
	for _, tc := range []struct {
		name   string
		amount string
	}{
		{"a fraction of a cent", "1250.5"},
		{"a fraction spelled small", "12.50"},
		{"an exponent", "1e3"},
		{"zero", "0"},
		{"a quoted integer", `"125000"`},
	} {
		rec := putReceivablesBody(t, h, true, receivablesBody("b-money-1", oneItemJSON(tc.amount)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s answered %d, want 400: %s", tc.name, rec.Code, rec.Body.String())
		}
		var out struct {
			Error    string `json:"error"`
			LegacyID string `json:"legacy_id"`
			Field    string `json:"field"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: the 400 body does not decode: %v", tc.name, err)
		}
		if out.LegacyID != "AR-L-1" || out.Field != "open_amount_cents" {
			t.Fatalf("%s: the 400 body names %q on %q, want the item on open_amount_cents", tc.name, out.LegacyID, out.Field)
		}
	}
}

// TestPutReceivablesDecodesMoneyAsNumbersNotFloats proves the decoder
// keeps the wire's exact spelling: the int64 bound is beyond a float's
// exact range, and the boundary still accepts it (the nil pool guard's
// 500 is the proof it passed validation and reached the store).
func TestPutReceivablesDecodesMoneyAsNumbersNotFloats(t *testing.T) {
	h := nilPoolReceivablesHandler(t)
	rec := putReceivablesBody(t, h, true, receivablesBody("b-money-2", oneItemJSON("9223372036854775807")))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("the int64 bound answered %d at the boundary, want it past validation (500 from the nil pool guard): %s", rec.Code, rec.Body.String())
	}
}

func TestPutReceivablesRefusesAMissingScope(t *testing.T) {
	h := nilPoolReceivablesHandler(t)
	rec := putReceivablesBody(t, h, false, receivablesBody("b-1", oneItemJSON("100")))
	if rec.Code != http.StatusForbidden || rec.Body.String() != string(authz.BodyForbidden) {
		t.Fatalf("no machine scope answered %d %q, want the one 403 body", rec.Code, rec.Body.String())
	}
}

func TestReceivablesErrorAnswerTable(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
	}{
		{"a refused batch, one item refused it all", &BatchRefusal{Refusing: Outcome{
			LegacyID: "AR-L-1", Status: OutcomeRefused, Reason: ReasonMissingReference,
			Field: "customer", Detail: "reference not loaded: kind customer legacy key CUST-9",
		}}, http.StatusUnprocessableEntity},
		{"a contended batch, retryable", fmt.Errorf("%w: deadlock detected", ErrBatchContended), http.StatusServiceUnavailable},
		{"another door's batch id", ErrBatchDoorMismatch, http.StatusConflict},
		{"a divergent replay", &BatchRefusal{Refusing: Outcome{
			LegacyID: "AR-L-1", Status: OutcomeRefused, Reason: ReasonReplayChanged,
			Field: "legacy_id", Detail: "the replayed batch carries a changed item",
		}}, http.StatusConflict},
		{"a missing field", ErrMissingField, http.StatusBadRequest},
		{"the batch bound", ErrBatchBound, http.StatusBadRequest},
		{"the audit actor guard", ErrBatchActor, http.StatusBadRequest},
		{"an item error from the boundary", &ItemError{LegacyID: "AR-L-1", Code: ReasonRecordShape, Field: "issue_date", Detail: "bad"}, http.StatusBadRequest},
		{"anything else is the server's", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		status, _ := receivablesErrorAnswer(tc.err)
		if status != tc.status {
			t.Fatalf("%s answered %d, want %d", tc.name, status, tc.status)
		}
	}
}

// refusingWriter refuses any write: the boundary tests must never reach
// a writer, and a reached writer would fail the test through it.
type refusingWriter struct{}

func (refusingWriter) Apply(_ context.Context, _ pgx.Tx, _ Write) (string, error) {
	return "", errors.New("the refusing writer was reached")
}

// noopAuditSink hands the boundary tests an audit sink; the nil pool
// guard refuses the batch before any audit row.
type noopAuditSink struct{}

func (noopAuditSink) RecordBatchTx(_ context.Context, _ pgx.Tx, _ BatchAuditRow) error { return nil }

// The date rule's own shapes, pinned: a valid day parses, every other
// spelling the cases above carry does not.
func TestReceivablesDateRuleParsesStrictDayShapes(t *testing.T) {
	for _, ok := range []string{"2021-01-01", "2021-12-31", "2024-02-29"} {
		if _, err := time.Parse("2006-01-02", ok); err != nil {
			t.Fatalf("a valid date refused: %v", err)
		}
	}
	for _, bad := range []string{"2021-02-29", "2021-00-01", "2021-01-00", "2021-1-01", "31-01-01", ""} {
		if _, err := time.Parse("2006-01-02", bad); err == nil {
			t.Fatalf("an invalid date accepted: %s", bad)
		}
	}
}

// A contended batch answers the retryable status the other load doors
// answer (503 with Retry-After): the batch rolled back whole and its id
// was never recorded, so the caller resubmits it whole.
func TestReceivablesContendedBatchAnswersRetryable503WithRetryAfter(t *testing.T) {
	h := &Handler{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	rec := httptest.NewRecorder()
	h.writeReceivablesError(rec, fmt.Errorf("%w: deadlock detected", ErrBatchContended))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a contended batch answered %d, want 503", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After is %q, want 1", got)
	}
	if !strings.Contains(rec.Body.String(), "resubmit") {
		t.Fatalf("the body %q does not tell the caller to resubmit", rec.Body.String())
	}
}
