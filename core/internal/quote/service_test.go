package quote

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
)

// fakeQuoteStore is an in-memory quoteStore that mirrors the repository contract: it is
// tenant-scoped (a foreign or missing id is ErrNotFound, never a leak) and enforces the
// same legal source states for every transition, so the service tests exercise the full
// composition without pgx. State rules intentionally match repository.go:
//
//	Accept : SENT            -> ACCEPTED
//	Reject : SENT|ACCEPTED   -> LOST
//	Convert: ACCEPTED        -> CONVERTED (emits an order)
type fakeQuoteStore struct {
	quotes   map[string]*Quote
	lines    map[string][]Line
	orderSeq int
}

func newFakeQuoteStore() *fakeQuoteStore {
	return &fakeQuoteStore{quotes: map[string]*Quote{}, lines: map[string][]Line{}}
}

func (f *fakeQuoteStore) seed(q Quote, lines ...Line) {
	cp := q
	f.quotes[q.ID] = &cp
	if len(lines) > 0 {
		f.lines[q.ID] = lines
	}
}

// scoped returns the tenant's quote or ErrNotFound (covers missing id AND cross-tenant).
func (f *fakeQuoteStore) scoped(customerID, quoteID string) (*Quote, error) {
	q, ok := f.quotes[quoteID]
	if !ok || q.CustomerID != customerID {
		return nil, ErrNotFound
	}
	return q, nil
}

func (f *fakeQuoteStore) ListQuotes(_ context.Context, customerID string, flt Filter) ([]Quote, int, error) {
	var all []Quote
	for _, q := range f.quotes {
		if q.CustomerID != customerID {
			continue
		}
		if flt.Status != "" && q.Status != flt.Status {
			continue
		}
		all = append(all, *q)
	}
	total := len(all)

	lo := flt.Offset
	if lo > len(all) {
		lo = len(all)
	}
	hi := len(all)
	if flt.Limit > 0 && lo+flt.Limit < hi {
		hi = lo + flt.Limit
	}
	return all[lo:hi], total, nil
}

func (f *fakeQuoteStore) GetQuote(_ context.Context, customerID, quoteID string) (Detail, error) {
	q, err := f.scoped(customerID, quoteID)
	if err != nil {
		return Detail{}, err
	}
	return Detail{Quote: *q, Lines: f.lines[quoteID]}, nil
}

func (f *fakeQuoteStore) Accept(_ context.Context, customerID, quoteID string) (Quote, error) {
	q, err := f.scoped(customerID, quoteID)
	if err != nil {
		return Quote{}, err
	}
	if q.Status != "SENT" {
		return Quote{}, ErrInvalidTransition
	}
	q.Status = "ACCEPTED"
	return *q, nil
}

func (f *fakeQuoteStore) Reject(_ context.Context, customerID, quoteID string) (Quote, error) {
	q, err := f.scoped(customerID, quoteID)
	if err != nil {
		return Quote{}, err
	}
	if q.Status != "SENT" && q.Status != "ACCEPTED" {
		return Quote{}, ErrInvalidTransition
	}
	q.Status = "LOST"
	return *q, nil
}

func (f *fakeQuoteStore) Convert(_ context.Context, customerID, quoteID string, _ ConvertOptions) (ConvertResult, error) {
	q, err := f.scoped(customerID, quoteID)
	if err != nil {
		return ConvertResult{}, err
	}
	if q.Status != "ACCEPTED" {
		return ConvertResult{}, ErrInvalidTransition
	}
	q.Status = "CONVERTED"
	f.orderSeq++
	return ConvertResult{OrderID: "order-" + quoteID, OrderNo: fmt.Sprintf("%d", f.orderSeq)}, nil
}

const (
	tenant = "cust-A"
	other  = "cust-B"
)

type action int

const (
	doAccept action = iota
	doReject
	doConvert
)

func (a action) String() string {
	switch a {
	case doAccept:
		return "accept"
	case doReject:
		return "reject"
	default:
		return "convert"
	}
}

// run dispatches one transition through the service, returning the resulting quote status
// (looked up fresh for convert, which returns a ConvertResult rather than a Quote).
func run(t *testing.T, svc *Service, a action, customerID, quoteID string) (string, error) {
	t.Helper()
	ctx := context.Background()
	switch a {
	case doAccept:
		q, err := svc.Accept(ctx, customerID, quoteID)
		return q.Status, err
	case doReject:
		q, err := svc.Reject(ctx, customerID, quoteID)
		return q.Status, err
	default:
		if _, err := svc.Convert(ctx, customerID, quoteID, ConvertOptions{}); err != nil {
			return "", err
		}
		d, gerr := svc.GetQuote(ctx, customerID, quoteID)
		if gerr != nil {
			t.Fatalf("convert: re-read quote: %v", gerr)
		}
		return d.Status, nil
	}
}

func TestQuote_Transitions(t *testing.T) {
	cases := []struct {
		name    string
		from    action
		start   string
		want    string // expected resulting status (empty when wantErr is set)
		wantErr error
	}{
		{"accept SENT->ACCEPTED", doAccept, "SENT", "ACCEPTED", nil},
		{"accept DRAFT invalid", doAccept, "DRAFT", "", ErrInvalidTransition},
		{"accept ACCEPTED invalid", doAccept, "ACCEPTED", "", ErrInvalidTransition},
		{"accept EXPIRED invalid", doAccept, "EXPIRED", "", ErrInvalidTransition},
		{"accept CONVERTED invalid", doAccept, "CONVERTED", "", ErrInvalidTransition},
		{"accept LOST invalid", doAccept, "LOST", "", ErrInvalidTransition},

		{"reject SENT->LOST", doReject, "SENT", "LOST", nil},
		{"reject ACCEPTED->LOST", doReject, "ACCEPTED", "LOST", nil},
		{"reject DRAFT invalid", doReject, "DRAFT", "", ErrInvalidTransition},
		{"reject EXPIRED invalid", doReject, "EXPIRED", "", ErrInvalidTransition},
		{"reject CONVERTED invalid", doReject, "CONVERTED", "", ErrInvalidTransition},
		{"reject LOST invalid", doReject, "LOST", "", ErrInvalidTransition},

		{"convert ACCEPTED->CONVERTED", doConvert, "ACCEPTED", "CONVERTED", nil},
		{"convert SENT invalid", doConvert, "SENT", "", ErrInvalidTransition},
		{"convert DRAFT invalid", doConvert, "DRAFT", "", ErrInvalidTransition},
		{"convert CONVERTED invalid", doConvert, "CONVERTED", "", ErrInvalidTransition},
		{"convert LOST invalid", doConvert, "LOST", "", ErrInvalidTransition},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeQuoteStore()
			store.seed(Quote{ID: "q1", QuoteNo: "Q-1", CustomerID: tenant, Status: tc.start})
			svc := NewService(store)

			got, err := run(t, svc, tc.from, tenant, "q1")
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("%s from %s: err = %v, want %v", tc.from, tc.start, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s from %s: unexpected err %v", tc.from, tc.start, err)
			}
			if got != tc.want {
				t.Fatalf("%s from %s: status = %q, want %q", tc.from, tc.start, got, tc.want)
			}
		})
	}
}

func TestQuote_NotFound(t *testing.T) {
	store := newFakeQuoteStore()
	store.seed(Quote{ID: "q1", QuoteNo: "Q-1", CustomerID: tenant, Status: "SENT"})
	svc := NewService(store)
	ctx := context.Background()

	// Missing id and cross-tenant id must both be ErrNotFound (404), never a 403/leak.
	for _, tc := range []struct {
		name            string
		customerID, qID string
	}{
		{"missing id", tenant, "nope"},
		{"cross-tenant id", other, "q1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.GetQuote(ctx, tc.customerID, tc.qID); !errors.Is(err, ErrNotFound) {
				t.Errorf("GetQuote: err = %v, want ErrNotFound", err)
			}
			if _, err := svc.Accept(ctx, tc.customerID, tc.qID); !errors.Is(err, ErrNotFound) {
				t.Errorf("Accept: err = %v, want ErrNotFound", err)
			}
			if _, err := svc.Reject(ctx, tc.customerID, tc.qID); !errors.Is(err, ErrNotFound) {
				t.Errorf("Reject: err = %v, want ErrNotFound", err)
			}
			if _, err := svc.Convert(ctx, tc.customerID, tc.qID, ConvertOptions{}); !errors.Is(err, ErrNotFound) {
				t.Errorf("Convert: err = %v, want ErrNotFound", err)
			}
		})
	}

	// The cross-tenant Accept must NOT have mutated the real owner's quote.
	d, err := svc.GetQuote(ctx, tenant, "q1")
	if err != nil {
		t.Fatalf("owner re-read: %v", err)
	}
	if d.Status != "SENT" {
		t.Fatalf("cross-tenant access mutated owner quote: status = %q, want SENT", d.Status)
	}
}

func TestQuote_Convert_HappyPath(t *testing.T) {
	store := newFakeQuoteStore()
	store.seed(
		Quote{ID: "q1", QuoteNo: "Q-1", CustomerID: tenant, Status: "ACCEPTED"},
		Line{LineNo: 1, ProductID: "p1", Description: "Paver", QtyEntered: "10.0000", UOMEntered: "EA", UnitPriceCents: 500, LineTotalCents: 5000},
	)
	svc := NewService(store)
	ctx := context.Background()

	res, err := svc.Convert(ctx, tenant, "q1", ConvertOptions{})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if res.OrderID == "" || res.OrderNo == "" {
		t.Fatalf("convert returned empty order ref: %+v", res)
	}

	d, err := svc.GetQuote(ctx, tenant, "q1")
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if d.Status != "CONVERTED" {
		t.Fatalf("quote status = %q, want CONVERTED", d.Status)
	}

	// A second convert is now an invalid transition (idempotency guard).
	if _, err := svc.Convert(ctx, tenant, "q1", ConvertOptions{}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("double convert: err = %v, want ErrInvalidTransition", err)
	}
}

func TestQuote_List_TenantScopedAndFiltered(t *testing.T) {
	store := newFakeQuoteStore()
	store.seed(Quote{ID: "q1", QuoteNo: "Q-1", CustomerID: tenant, Status: "SENT"})
	store.seed(Quote{ID: "q2", QuoteNo: "Q-2", CustomerID: tenant, Status: "ACCEPTED"})
	store.seed(Quote{ID: "q3", QuoteNo: "Q-3", CustomerID: tenant, Status: "SENT"})
	store.seed(Quote{ID: "q4", QuoteNo: "Q-4", CustomerID: other, Status: "SENT"})
	svc := NewService(store)
	ctx := context.Background()

	all, total, err := svc.ListQuotes(ctx, tenant, Filter{Limit: 50})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if total != 3 || len(all) != 3 {
		t.Fatalf("tenant scope: got %d items / total %d, want 3/3 (cust-B excluded)", len(all), total)
	}

	sent, total, err := svc.ListQuotes(ctx, tenant, Filter{Status: "SENT", Limit: 50})
	if err != nil {
		t.Fatalf("list SENT: %v", err)
	}
	if total != 2 || len(sent) != 2 {
		t.Fatalf("status filter: got %d / total %d, want 2/2", len(sent), total)
	}

	// Pagination: total reflects the full filtered set, the page is clamped to Limit.
	page, total, err := svc.ListQuotes(ctx, tenant, Filter{Limit: 1})
	if err != nil {
		t.Fatalf("list page: %v", err)
	}
	if total != 3 || len(page) != 1 {
		t.Fatalf("pagination: got %d items / total %d, want 1/3", len(page), total)
	}
}

func TestQuote_GetQuote_ReturnsLines(t *testing.T) {
	store := newFakeQuoteStore()
	store.seed(
		Quote{ID: "q1", QuoteNo: "Q-1", CustomerID: tenant, Status: "SENT", TotalCents: 5000},
		Line{LineNo: 1, ProductID: "p1", Description: "Paver", QtyEntered: "10.0000", UOMEntered: "EA", UnitPriceCents: 500, LineTotalCents: 5000},
	)
	svc := NewService(store)

	d, err := svc.GetQuote(context.Background(), tenant, "q1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(d.Lines) != 1 || d.Lines[0].LineTotalCents != 5000 {
		t.Fatalf("lines not returned: %+v", d.Lines)
	}
	if d.TotalCents != 5000 {
		t.Fatalf("header total = %d, want 5000", d.TotalCents)
	}
}

// recordingRecorder is an in-memory audit.Recorder capturing every emitted SensitiveAccess.
type recordingRecorder struct{ records []audit.SensitiveAccess }

func (r *recordingRecorder) Record(_ context.Context, a audit.SensitiveAccess) error {
	r.records = append(r.records, a)
	return nil
}

// DIB-159 REGRESSION (MAJOR-2): quote→order conversion is a creation writer, the order is INSERTed with
// no status column, so it defaults to DRAFT (migration 0008). The per-order status timeline must carry
// the ""→DRAFT creation transition. Before this fix Convert emitted no audit record at all, so a
// quote-converted order's history was silently empty. Assert exactly one ORDER_STATUS_CHANGED with
// ""→DRAFT, keyed to the new order.
func TestQuote_Convert_EmitsCreationStatusTransition(t *testing.T) {
	store := newFakeQuoteStore()
	store.seed(Quote{ID: "q1", QuoteNo: "Q-1", CustomerID: tenant, Status: "ACCEPTED"})
	rec := &recordingRecorder{}
	svc := NewService(store).WithAudit(rec)

	res, err := svc.Convert(context.Background(), tenant, "q1", ConvertOptions{})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	var statusRows []audit.SensitiveAccess
	for _, a := range rec.records {
		if a.Action == audit.ActOrderStatusChanged {
			statusRows = append(statusRows, a)
		}
	}
	if len(statusRows) != 1 {
		t.Fatalf("convert emitted %d ORDER_STATUS_CHANGED rows, want exactly 1 (creation ->DRAFT): %+v", len(statusRows), rec.records)
	}
	sc := statusRows[0]
	if sc.EntityType != "order" || sc.EntityID != res.OrderID {
		t.Errorf("status row entity = %s/%s, want order/%s", sc.EntityType, sc.EntityID, res.OrderID)
	}
	if sc.ActorID != tenant {
		t.Errorf("actor = %s, want %s", sc.ActorID, tenant)
	}
	if sc.Extra["old_status"] != "" || sc.Extra["new_status"] != "DRAFT" {
		t.Errorf("from→to = %v→%v, want →DRAFT", sc.Extra["old_status"], sc.Extra["new_status"])
	}
}

// A failed conversion (invalid source state) records NO status transition.
func TestQuote_Convert_NoEmitOnFailure(t *testing.T) {
	store := newFakeQuoteStore()
	store.seed(Quote{ID: "q1", QuoteNo: "Q-1", CustomerID: tenant, Status: "SENT"}) // not ACCEPTED → invalid
	rec := &recordingRecorder{}
	svc := NewService(store).WithAudit(rec)

	if _, err := svc.Convert(context.Background(), tenant, "q1", ConvertOptions{}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("convert from SENT: err = %v, want ErrInvalidTransition", err)
	}
	for _, a := range rec.records {
		if a.Action == audit.ActOrderStatusChanged {
			t.Fatalf("a failed conversion must emit no status transition, got %+v", a)
		}
	}
}

func TestQuote_ServiceValidation(t *testing.T) {
	svc := NewService(newFakeQuoteStore())
	ctx := context.Background()

	// Empty ids are a bad request (400), distinct from ErrNotFound / ErrInvalidTransition.
	mustBadReq := func(name string, err error) {
		if err == nil {
			t.Fatalf("%s: want error, got nil", name)
		}
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("%s: want bad-request, got %v", name, err)
		}
	}

	_, _, err := svc.ListQuotes(ctx, "", Filter{})
	mustBadReq("ListQuotes empty customer", err)

	_, _, err = svc.ListQuotes(ctx, tenant, Filter{Status: "BOGUS"})
	mustBadReq("ListQuotes bad status", err)

	_, err = svc.GetQuote(ctx, tenant, "")
	mustBadReq("GetQuote empty id", err)

	_, err = svc.Accept(ctx, "", "q1")
	mustBadReq("Accept empty customer", err)

	_, err = svc.Convert(ctx, tenant, "", ConvertOptions{})
	mustBadReq("Convert empty id", err)
}
