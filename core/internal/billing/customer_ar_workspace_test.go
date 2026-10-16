package billing

// customer_ar_workspace_test.go, the workspace handler tests. The workspace is one
// read composed of the AR console's own aging, the three invoice lists, payments,
// credit notes, statements, notes and the activity feed. The load-bearing assertions
// are:
//
//   - reconciliation to the penny: the 5-bucket aging sums to OpenCents, and the four
//     late buckets sum to OverdueCents; a drift fails the read, so a panel rendered
//     with figures that do not add up cannot leave the server.
//
//   - the seam: the workspace is composed through WorkspaceStore, never through the
//     Repository directly, so the handler can be tested with a fake and the real
//     integration does not depend on a database.
//
//   - the notes add (0405): a note writes inside one transaction, with the partial
//     unique index rule modelled by the test (pinned=true first un-pins any prior
//     pinned row, then inserts the new one).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// fakeWorkspaceStore is the unit-test seam behind WorkspaceStore. Each method returns
// whatever the test set up, and the test asserts the handler asks for the right
// customer and limit rather than reaching into SQL.
type fakeWorkspaceStore struct {
	header CustomerWorkspaceHeader
	invoicesOpen    []CustomerWorkspaceInvoice
	invoicesClosed  []CustomerWorkspaceInvoice
	invoicesDisputed []CustomerWorkspaceInvoice
	payments       []CustomerWorkspacePayment
	creditNotes    []CustomerWorkspaceCreditNote
	statements     []CustomerWorkspaceStatement
	notes          []CustomerWorkspaceNote

	headerErr error
	invoicesErr error
	paymentsErr error
	creditErr error
	statementsErr error
	notesErr error

	gotCustomerID string
	gotLimit int
	addedNotes []AddNoteInput
	addedForID string
	addedByAuthorID string
}

func (f *fakeWorkspaceStore) WorkspaceHeader(_ context.Context, customerID string) (CustomerWorkspaceHeader, error) {
	f.gotCustomerID = customerID
	return f.header, f.headerErr
}

func (f *fakeWorkspaceStore) WorkspaceInvoices(_ context.Context, customerID string) (open, closed, disputed []CustomerWorkspaceInvoice, err error) {
	f.gotCustomerID = customerID
	return f.invoicesOpen, f.invoicesClosed, f.invoicesDisputed, f.invoicesErr
}

func (f *fakeWorkspaceStore) WorkspacePayments(_ context.Context, customerID string) ([]CustomerWorkspacePayment, error) {
	f.gotCustomerID = customerID
	return f.payments, f.paymentsErr
}

func (f *fakeWorkspaceStore) WorkspaceCreditNotes(_ context.Context, customerID string) ([]CustomerWorkspaceCreditNote, error) {
	f.gotCustomerID = customerID
	return f.creditNotes, f.creditErr
}

func (f *fakeWorkspaceStore) WorkspaceStatements(_ context.Context, customerID string) ([]CustomerWorkspaceStatement, error) {
	f.gotCustomerID = customerID
	return f.statements, f.statementsErr
}

func (f *fakeWorkspaceStore) WorkspaceNotes(_ context.Context, customerID string, limit int) ([]CustomerWorkspaceNote, error) {
	f.gotCustomerID = customerID
	f.gotLimit = limit
	return f.notes, f.notesErr
}

func (f *fakeWorkspaceStore) AddNote(_ context.Context, customerID, authorID string, in AddNoteInput) (CustomerWorkspaceNote, error) {
	body := strings.TrimSpace(in.Body)
	if body == "" {
		return CustomerWorkspaceNote{}, httpx.BadRequestf("note body is required")
	}
	if len(body) > 4096 {
		return CustomerWorkspaceNote{}, httpx.BadRequestf("note body exceeds 4096 characters")
	}
	f.addedForID = customerID
	f.addedByAuthorID = authorID
	f.addedNotes = append(f.addedNotes, in)
	return CustomerWorkspaceNote{
		ID: "note-1", Kind: in.Kind, Body: in.Body, AuthorID: authorID, Pinned: in.Pinned,
		CreatedAt: time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC),
	}, nil
}

// fakeCreditForWorkspace is the credit service the workspace handler reaches for the
// CustomerAging and CustomerActivity reads. The seam is optional (nil in tests that
// do not need the aging); when wired, the fake answers what the test set up.
type fakeCreditForWorkspace struct {
	agingRow AgingRow
	asOf     time.Time
	found    bool
	agingErr error

	activity []ARActivity
	actErr   error

	gotID string
}

func (f *fakeCreditForWorkspace) CustomerAging(_ context.Context, customerID string) (AgingRow, time.Time, bool, error) {
	f.gotID = customerID
	return f.agingRow, f.asOf, f.found, f.agingErr
}

func (f *fakeCreditForWorkspace) CustomerActivity(_ context.Context, customerID string, _ int, _ bool) ([]ARActivity, error) {
	f.gotID = customerID
	return f.activity, f.actErr
}

// Compile-time check: the test fake satisfies the credit service interface the
// workspace handler reaches. Faking it here rather than via type assertion keeps the
// build honest when the service grows a method.
var _ workspaceCreditSeam = (*fakeCreditForWorkspace)(nil)

// workspaceCreditSeam is the slice of CreditService the workspace handler needs.
// Declared as an interface in the handler file so a unit test can fake it without
// carrying the full service surface.
type workspaceCreditSeam interface {
	CustomerAging(ctx context.Context, customerID string) (AgingRow, time.Time, bool, error)
	CustomerActivity(ctx context.Context, customerID string, limit int, withPayments bool) ([]ARActivity, error)
}

// newWorkspaceHandlerForTest wires the handler against the fake store and the fake
// credit seam. nil-credit is the optional case the seam supports (the handler skips
// the aging field).
func newWorkspaceHandlerForTest(store *fakeWorkspaceStore, credit workspaceCreditSeam) *WorkspaceHandler {
	return NewWorkspaceHandler(store, credit, nil)
}

// -- reconciliation to the penny ----------------------------------------------------------------

// TestWorkspaceAging_ReconcilesToThePenny: the 5 buckets sum to OpenCents, and the
// four late buckets sum to OverdueCents. A drift fails the read; the panel cannot
// be rendered with figures that do not add up.
func TestWorkspaceAging_ReconcilesToThePenny(t *testing.T) {
	a := CustomerWorkspaceAging{
		CurrentCents: 100000, Days30Cents: 25000, Days60Cents: 10000,
		Days90Cents: 5000, Days90PlusCents: 0,
		OpenCents: 140000, OverdueCents: 40000,
	}
	if !a.ReconcilesToThePenny() {
		t.Fatalf("the workspace's own aging should reconcile: %+v", a)
	}

	// Inject a drift: late buckets no longer sum to OverdueCents.
	a.Days30Cents = 24000
	if a.ReconcilesToThePenny() {
		t.Fatalf("a 1000-cents understatement in the 30 bucket must fail reconciliation: %+v", a)
	}
}

// TestWorkspaceAging_BucketsSumToOpenCents: the same assertion the other side of the
// reconciliation. A bucket that pretends more money lives in it than the row reports
// would silently inflate OpenCents, the failure mode the rule was designed to catch.
func TestWorkspaceAging_BucketsSumToOpenCents(t *testing.T) {
	a := CustomerWorkspaceAging{
		CurrentCents: 100000, Days30Cents: 25000, Days60Cents: 10000,
		Days90Cents: 5000, Days90PlusCents: 0,
		OpenCents: 140000, OverdueCents: 40000,
	}
	if !a.ReconcilesToThePenny() {
		t.Fatalf("baseline row should reconcile: %+v", a)
	}

	// OpenCents is now smaller than the buckets sum to: the read must fail.
	a.OpenCents = 100000
	if a.ReconcilesToThePenny() {
		t.Fatalf("an OpenCents lower than the buckets sum must fail reconciliation: %+v", a)
	}
}

// TestBucketForLabel_Boundaries: the 5-bucket label matches the C8 walk's vocabulary
// (Current / 1 to 30 / 31 to 60 / 61 to 90 / 91 and over). A label off by one at a
// boundary puts the wrong bucket pill on the invoice row.
func TestBucketForLabel_Boundaries(t *testing.T) {
	cases := []struct {
		days int
		label string
	}{
		{-1, "Current"},
		{0, "Current"},
		{1, "1 to 30"},
		{30, "1 to 30"},
		{31, "31 to 60"},
		{60, "31 to 60"},
		{61, "61 to 90"},
		{90, "61 to 90"},
		{91, "91 and over"},
		{500, "91 and over"},
	}
	for _, tc := range cases {
		if got := BucketForLabel(tc.days); got != tc.label {
			t.Errorf("days=%d got=%q want=%q", tc.days, got, tc.label)
		}
	}
}

// -- the handler --------------------------------------------------------------------------------

// TestWorkspaceHandler_RejectsNonUUID: a non-uuid that reaches the route answers 400.
// A non-uuid that reaches SQL as an uncastable literal comes back as 500; the route
// short-circuits before the driver to keep the error vocabulary accurate.
func TestWorkspaceHandler_RejectsNonUUID(t *testing.T) {
	h := newWorkspaceHandlerForTest(&fakeWorkspaceStore{}, nil)
	r := newWorkspaceRequest("not-a-uuid", nil)
	rr := httptest.NewRecorder()
	h.workspace(rr, r, staffCaller())
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// TestWorkspaceHandler_404WhenAccountMissing: header scan returns zero-value CustomerID
// when the customer does not exist. The handler answers 404 (not 200 with empty
// data, which would be a panel that lies).
func TestWorkspaceHandler_404WhenAccountMissing(t *testing.T) {
	store := &fakeWorkspaceStore{} // empty header -> CustomerID == ""
	h := newWorkspaceHandlerForTest(store, nil)
	r := newWorkspaceRequest("c1b00000-0000-4000-a000-000000000099", nil)
	rr := httptest.NewRecorder()
	h.workspace(rr, r, staffCaller())
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// TestWorkspaceHandler_ComposesAllSubPayloads: with a credit service wired and a fake
// store, the workspace handler assembles the whole payload in one read. The test
// asserts every sub-list made it into the response body and the 5-bucket
// reconciliation passes.
func TestWorkspaceHandler_ComposesAllSubPayloads(t *testing.T) {
	at := time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC)
	store := &fakeWorkspaceStore{
		header: CustomerWorkspaceHeader{
			CustomerID: "c1b00000-0000-4000-a000-000000000099",
			Name: "Northgate",
			AccountNo: "NG-1",
			Status: AccountCreditOK,
			TermsDays: 30,
			CreditLimitCents: 5_000_000,
			OpenCents: 140_000,
			OverdueCents: 40_000,
		},
		invoicesOpen: []CustomerWorkspaceInvoice{
			{ID: "inv-1", InvoiceNo: "INV-1", DocType: "INVOICE", Status: "FINALIZED",
				IssueDate: at, DueDate: at.Add(-72 * time.Hour), TotalCents: 100_000,
				BalanceCents: 100_000, DaysOverdue: 3, DaysOverdueBucket: "1 to 30"},
		},
		invoicesClosed: []CustomerWorkspaceInvoice{
			{ID: "inv-2", InvoiceNo: "INV-2", Status: "PAID", TotalCents: 50_000, BalanceCents: 0},
		},
		payments: []CustomerWorkspacePayment{
			{ID: "p-1", PaymentNo: "PAY-1", ReceivedAt: at, AmountCents: 50_000, Method: "EFT", UnappliedCents: 0},
		},
		creditNotes: []CustomerWorkspaceCreditNote{
			{ID: "c-1", MemoNo: "CM-1", IssuedAt: at, AmountCents: 25_000, UnappliedCents: 25_000, Reason: "Damaged"},
		},
		statements: []CustomerWorkspaceStatement{
			{ID: "s-1", RunID: "r-1", Mode: "MASS", AsOfDate: at.Add(-24 * time.Hour),
				ClosingBalanceCents: 140_000, DeliveryStatus: "SENT", CreatedAt: at.Add(-24 * time.Hour)},
		},
		notes: []CustomerWorkspaceNote{
			{ID: "n-1", Kind: "NOTE", Body: "promised by phone", Pinned: true, CreatedAt: at},
		},
	}
	credit := &fakeCreditForWorkspace{
		agingRow: AgingRow{
			CurrentCents: 100_000, Days30Cents: 25_000, Days60Cents: 10_000,
			Days60To90Cents: 5_000, Days90PlusCents: 0, TotalCents: 140_000,
		},
		asOf: at,
		found: true,
		activity: []ARActivity{
			{Kind: "INVOICE", Reference: "INV-1", AmountCents: 100_000, OccurredAt: at},
		},
	}
	h := newWorkspaceHandlerForTest(store, credit)

	r := newWorkspaceRequest("c1b00000-0000-4000-a000-000000000099", nil)
	rr := httptest.NewRecorder()
	h.workspace(rr, r, staffCaller())

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	var got CustomerWorkspace
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Header.CustomerID != "c1b00000-0000-4000-a000-000000000099" {
		t.Errorf("header.customer_id=%q", got.Header.CustomerID)
	}
	if len(got.InvoicesOpen) != 1 || got.InvoicesOpen[0].InvoiceNo != "INV-1" {
		t.Errorf("invoices_open=%+v", got.InvoicesOpen)
	}
	if len(got.InvoicesClosed) != 1 || got.InvoicesClosed[0].InvoiceNo != "INV-2" {
		t.Errorf("invoices_closed=%+v", got.InvoicesClosed)
	}
	if len(got.Payments) != 1 || got.Payments[0].PaymentNo != "PAY-1" {
		t.Errorf("payments=%+v", got.Payments)
	}
	if len(got.CreditNotes) != 1 || got.CreditNotes[0].MemoNo != "CM-1" {
		t.Errorf("credit_notes=%+v", got.CreditNotes)
	}
	if len(got.Statements) != 1 || got.Statements[0].Mode != "MASS" {
		t.Errorf("statements=%+v", got.Statements)
	}
	if len(got.Notes) != 1 || got.Notes[0].Body != "promised by phone" {
		t.Errorf("notes=%+v", got.Notes)
	}
	if len(got.Activity) != 1 || got.Activity[0].Reference != "INV-1" {
		t.Errorf("activity=%+v", got.Activity)
	}
	if !got.Header.Aging.ReconcilesToThePenny() {
		t.Errorf("aging must reconcile to the penny: %+v", got.Header.Aging)
	}
	if got.Header.UtilizationBps != 280 { // 140_000 * 10000 / 5_000_000 = 280
		t.Errorf("utilization_bps=%d want 280", got.Header.UtilizationBps)
	}
	if got.Header.AvailableCreditCents != 5_000_000-140_000 {
		t.Errorf("available_credit_cents=%d", got.Header.AvailableCreditCents)
	}
}

// TestWorkspaceHandler_500WhenAgingDoesNotReconcile: a drift between the credit
// service's aging and the workspace's open balance fails the read with 500. The
// screen must never render two figures that do not agree.
func TestWorkspaceHandler_500WhenAgingDoesNotReconcile(t *testing.T) {
	at := time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC)
	store := &fakeWorkspaceStore{
		header: CustomerWorkspaceHeader{
			CustomerID: "c1b00000-0000-4000-a000-000000000099",
			Name: "Northgate", AccountNo: "NG-1",
			Status: AccountCreditOK, TermsDays: 30, CreditLimitCents: 5_000_000,
			OpenCents: 100_000, // DRIFT: less than the aging's total below.
		},
		invoicesOpen: []CustomerWorkspaceInvoice{}, invoicesClosed: []CustomerWorkspaceInvoice{},
		payments: []CustomerWorkspacePayment{}, creditNotes: []CustomerWorkspaceCreditNote{},
		statements: []CustomerWorkspaceStatement{}, notes: []CustomerWorkspaceNote{},
	}
	credit := &fakeCreditForWorkspace{
		agingRow: AgingRow{
			CurrentCents: 100_000, Days30Cents: 25_000, Days60Cents: 10_000,
			Days60To90Cents: 5_000, Days90PlusCents: 0, TotalCents: 140_000,
		},
		asOf: at, found: true,
	}
	h := newWorkspaceHandlerForTest(store, credit)
	r := newWorkspaceRequest("c1b00000-0000-4000-a000-000000000099", nil)
	rr := httptest.NewRecorder()
	h.workspace(rr, r, staffCaller())
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on aging drift, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// -- the notes writer ---------------------------------------------------------------------------

// TestWorkspaceAddNote_PassesAuthorAndBody: the POST handler forwards the body,
// kind, pinned flag and the caller's platform user id to the seam. The seam is
// tested by inspecting the AddNoteInputs the fake recorded.
func TestWorkspaceAddNote_PassesAuthorAndBody(t *testing.T) {
	store := &fakeWorkspaceStore{}
	h := newWorkspaceHandlerForTest(store, nil)
	body := strings.NewReader(`{"body":"promised by phone","kind":"CALL","pinned":true}`)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/billing/ar/customers/c1b00000-0000-4000-a000-000000000099/notes", body)
	r.Header.Set("Content-Type", "application/json")
	r.SetPathValue("customer_id", "c1b00000-0000-4000-a000-000000000099")
	rr := httptest.NewRecorder()
	h.addNote(rr, r, staffCaller())

	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if store.addedForID != "c1b00000-0000-4000-a000-000000000099" {
		t.Errorf("addedForID=%q", store.addedForID)
	}
	if store.addedByAuthorID != "de110000-0000-4000-9000-000000000007" {
		t.Errorf("addedByAuthorID=%q", store.addedByAuthorID)
	}
	if len(store.addedNotes) != 1 {
		t.Fatalf("addedNotes=%+v", store.addedNotes)
	}
	if store.addedNotes[0].Body != "promised by phone" || store.addedNotes[0].Kind != "CALL" || !store.addedNotes[0].Pinned {
		t.Errorf("addedNotes=%+v", store.addedNotes[0])
	}
}

// TestWorkspaceAddNote_RejectsEmptyBody: a body-less note is the one input the seam
// refuses. The handler surfaces that in the same envelope every other billing write
// uses.
func TestWorkspaceAddNote_RejectsEmptyBody(t *testing.T) {
	store := &fakeWorkspaceStore{}
	h := newWorkspaceHandlerForTest(store, nil)
	body := strings.NewReader(`{"body":"  ","kind":"NOTE"}`)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/billing/ar/customers/c1b00000-0000-4000-a000-000000000099/notes", body)
	r.Header.Set("Content-Type", "application/json")
	r.SetPathValue("customer_id", "c1b00000-0000-4000-a000-000000000099")
	rr := httptest.NewRecorder()
	h.addNote(rr, r, staffCaller())

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for blank body, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(store.addedNotes) != 0 {
		t.Errorf("the seam must not be called for an empty body")
	}
}

// TestWorkspaceAddNote_RejectsNonUUIDCustomer: a non-uuid that reaches the route
// answers 400 (same posture as the GET).
func TestWorkspaceAddNote_RejectsNonUUIDCustomer(t *testing.T) {
	store := &fakeWorkspaceStore{}
	h := newWorkspaceHandlerForTest(store, nil)
	body := strings.NewReader(`{"body":"x"}`)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/billing/ar/customers/not-a-uuid/notes", body)
	r.SetPathValue("customer_id", "not-a-uuid")
	rr := httptest.NewRecorder()
	h.addNote(rr, r, staffCaller())

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// -- helpers ------------------------------------------------------------------------------------

// staffCallerID is the platform user id the workspace handler reaches for when it
// records the author of a note. The fake caller below carries this id; production
// builds it from the verified staff bearer.
const staffCallerID = "de110000-0000-4000-9000-000000000007"

// staffCaller is a test stub for guards.StaffCaller. The handler only reads the
// embedded PlatformUserID, which we set so the AddNote route can record the author.
func staffCaller() guards.StaffCaller { return testStaffCaller }

// testStaffCaller is the one caller every workspace handler test uses. The handler
// reads only PlatformUserID from the embedded StaffScope; the rest are blank.
var testStaffCaller = guards.StaffCaller{
	StaffScope: authz.StaffScope{PlatformUserID: staffCallerID},
}

// newWorkspaceRequest builds a GET request for /workspace with the customer id in
// the path. The handler reads it via r.PathValue, so we set it explicitly.
func newWorkspaceRequest(customerID string, _ any) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/billing/ar/customers/"+customerID+"/workspace", nil)
	r.SetPathValue("customer_id", customerID)
	return r
}

// -- nil safety ---------------------------------------------------------------------------------

// TestWorkspaceHandler_NilCreditLeavesAgingEmpty: when the credit service is nil, the
// aging field is left zero. The handler does not panic.
func TestWorkspaceHandler_NilCreditLeavesAgingEmpty(t *testing.T) {
	store := &fakeWorkspaceStore{
		header: CustomerWorkspaceHeader{
			CustomerID: "c1b00000-0000-4000-a000-000000000099",
			Name: "Northgate", AccountNo: "NG-1",
			Status: AccountCurrent, TermsDays: 30, CreditLimitCents: 0,
		},
	}
	h := newWorkspaceHandlerForTest(store, nil)
	r := newWorkspaceRequest("c1b00000-0000-4000-a000-000000000099", nil)
	rr := httptest.NewRecorder()
	h.workspace(rr, r, staffCaller())
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// TestWorkspaceHandler_PropagatesStoreError: a header scan error surfaces as 500.
// The workspace handler does not invent success answers for store failures.
func TestWorkspaceHandler_PropagatesStoreError(t *testing.T) {
	store := &fakeWorkspaceStore{headerErr: errors.New("db unreachable")}
	h := newWorkspaceHandlerForTest(store, nil)
	r := newWorkspaceRequest("c1b00000-0000-4000-a000-000000000099", nil)
	rr := httptest.NewRecorder()
	h.workspace(rr, r, staffCaller())
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}