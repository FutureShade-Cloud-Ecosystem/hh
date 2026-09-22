package order

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// fakeStore is an in-memory orderStore that enforces tenant scope exactly like the pgx
// Repository would (a row owned by another customer reads as not-found), so the service
// tests exercise validation + pass-through + IDOR safety without a database.
type fakeStore struct {
	details map[string]Detail // orderID -> order detail (CustomerID via embedded Order)
	// branches maps orderID -> owning branch_id, so OrderBranch (the history read's branch-scope
	// source, mirroring the pgx read of orders.branch_id) can be exercised without a database.
	branches map[string]string

	// recorded inputs for assertions
	lastFilter   Filter
	lastSetText  string
	setCalls     int
	statusCalls  int
	branchCalls  int
	listForceErr error
}

func newFakeStore(ds ...Detail) *fakeStore {
	m := make(map[string]Detail, len(ds))
	for _, d := range ds {
		m[d.ID] = d
	}
	return &fakeStore{details: m}
}

func (f *fakeStore) ListOrders(_ context.Context, customerID string, flt Filter) ([]Order, int, error) {
	f.lastFilter = flt
	if f.listForceErr != nil {
		return nil, 0, f.listForceErr
	}
	var matched []Order
	for _, d := range f.details {
		if d.CustomerID != customerID {
			continue
		}
		if flt.Status != "" && d.Status != flt.Status {
			continue
		}
		matched = append(matched, d.Order)
	}
	total := len(matched)
	// honor offset/limit so pagination pass-through is observable
	if flt.Offset >= len(matched) {
		return nil, total, nil
	}
	matched = matched[flt.Offset:]
	if flt.Limit > 0 && flt.Limit < len(matched) {
		matched = matched[:flt.Limit]
	}
	return matched, total, nil
}

func (f *fakeStore) GetOrder(_ context.Context, customerID, orderID string) (Detail, error) {
	d, ok := f.details[orderID]
	if !ok || d.CustomerID != customerID {
		return Detail{}, ErrNotFound
	}
	return d, nil
}

func (f *fakeStore) SetDeliveryInstructions(_ context.Context, customerID, orderID, text string) error {
	f.setCalls++
	f.lastSetText = text
	d, ok := f.details[orderID]
	if !ok || d.CustomerID != customerID {
		return ErrNotFound
	}
	d.DeliveryInstructions = text
	f.details[orderID] = d
	return nil
}

// OrderBranch mirrors the pgx OrderBranch: it returns the order's owning branch_id, or ErrNotFound
// when the id is unknown (the not-found-vs-forbidden split the history read relies on). The branch is
// read from the branches map when set, else falls back to a per-detail default so existing tests that
// only seed details still resolve a (single) branch.
func (f *fakeStore) OrderBranch(_ context.Context, orderID string) (string, error) {
	f.branchCalls++
	if b, ok := f.branches[orderID]; ok {
		return b, nil
	}
	if _, ok := f.details[orderID]; ok {
		return "", nil // an order exists but no branch was seeded, treat as the empty/default branch
	}
	return "", ErrNotFound
}

// OrderHeader mirrors the pgx OrderHeader: current status + owning branch in one read, ErrNotFound for
// an unknown id (so the staff read's not-found-vs-forbidden split is exercised without a database).
func (f *fakeStore) OrderHeader(_ context.Context, orderID string) (string, string, error) {
	d, ok := f.details[orderID]
	if !ok {
		return "", "", ErrNotFound
	}
	return d.Status, f.branches[orderID], nil
}

// ResolveOrderID mirrors the pgx ResolveOrderID passthrough: unit tests always key by real id (never
// an order_no), so it's an identity function here, the real order_no lookup is DB-backed and covered
// by the order package's integration tests instead.
// ResolveOrderID mirrors the pgx resolver for the tests: a UUID passes through; anything else is a
// no-op pass-through too (the fake keys orders by whatever id the fixture used). B2-4 branch scoping is
// exercised against the REAL repository in edit_integration_test.go, not here.
func (f *fakeStore) ResolveOrderID(_ context.Context, idOrNo string, _ bool, _ []string) (string, error) {
	return idOrNo, nil
}

// UpdateStatusFromChecked mirrors UpdateStatusFrom but runs the precheck (the cancel money predicate)
// BEFORE the move once the transition is otherwise legal, the same order the real locked write uses
// (B2-2). The predicate's error propagates so a fixture with money attached is refused.
func (f *fakeStore) UpdateStatusFromChecked(ctx context.Context, orderID, newStatus string, allowedFrom []string, precheck func(context.Context) error) (string, error) {
	d, ok := f.details[orderID]
	if !ok {
		return "", ErrNotFound
	}
	old := d.Status
	if old == newStatus {
		return old, ErrNoTransition
	}
	allowed := false
	for _, s := range allowedFrom {
		if s == old {
			allowed = true
			break
		}
	}
	if !allowed {
		return old, ErrIllegalTransition
	}
	if precheck != nil {
		if err := precheck(ctx); err != nil {
			return old, err
		}
	}
	f.statusCalls++
	d.Status = newStatus
	f.details[orderID] = d
	return old, nil
}

// UpdateStatus mirrors the pgx UpdateStatus semantics for the DIB-159 choke-point tests: it returns
// the prior status atomically, ErrNoTransition on a no-op (to == from), and ErrNotFound on a missing
// or (when tenant != "") cross-tenant id.
func (f *fakeStore) UpdateStatus(_ context.Context, tenant, orderID, newStatus string) (string, error) {
	f.statusCalls++
	d, ok := f.details[orderID]
	if !ok || (tenant != "" && d.CustomerID != tenant) {
		return "", ErrNotFound
	}
	old := d.Status
	if old == newStatus {
		return old, ErrNoTransition
	}
	d.Status = newStatus
	f.details[orderID] = d
	return old, nil
}

// UpdateStatusFrom mirrors the pgx guarded write for the DIB-191 manual-transition tests: it moves the
// order only when its current status is in allowedFrom, returning the prior status atomically. Missing
// id → ErrNotFound; current == newStatus → ErrNoTransition; current ∉ allowedFrom → ErrIllegalTransition.
func (f *fakeStore) UpdateStatusFrom(_ context.Context, orderID, newStatus string, allowedFrom []string) (string, error) {
	f.statusCalls++
	d, ok := f.details[orderID]
	if !ok {
		return "", ErrNotFound
	}
	old := d.Status
	if old == newStatus {
		return old, ErrNoTransition
	}
	allowed := false
	for _, s := range allowedFrom {
		if s == old {
			allowed = true
			break
		}
	}
	if !allowed {
		return old, ErrIllegalTransition
	}
	d.Status = newStatus
	f.details[orderID] = d
	return old, nil
}

// fakeCancelGuards is the B2/R2 cancel-predicate stand-in. One type satisfies BOTH seams
// (FinalizedInvoiceReader + CommittedSaleReader) so a fixture can wire a single instance.
//
// The zero value is PERMISSIVE, no invoice, no sale, i.e. "no money attached, cancel is allowed" -
// which is what the pre-R2 tests implicitly assumed. Set hasInvoice/hasSale to simulate a blocked
// order, or set err to simulate an unreadable predicate. Leaving the service's guards nil (i.e. not
// calling WithCancelGuards at all) is how the fail-closed path is tested.
type fakeCancelGuards struct {
	hasInvoice bool
	hasSale    bool
	err        error
}

func (f *fakeCancelGuards) OrderHasFinalizedInvoice(context.Context, string) (bool, error) {
	return f.hasInvoice, f.err
}

func (f *fakeCancelGuards) OrderHasCommittedPOSSale(context.Context, string) (bool, error) {
	return f.hasSale, f.err
}

func mkDetail(id, customerID, status string) Detail {
	return Detail{Order: Order{ID: id, OrderNo: "SO-" + id, CustomerID: customerID, Status: status}}
}

func isBadRequest(err error) bool {
	var bre httpx.BadRequestError
	return errors.As(err, &bre)
}

func TestListOrders(t *testing.T) {
	store := newFakeStore(
		mkDetail("1", "cust-a", "CONFIRMED"),
		mkDetail("2", "cust-a", "FULFILLED"),
		mkDetail("3", "cust-b", "CONFIRMED"), // other tenant
	)
	svc := NewService(store)
	ctx := context.Background()

	t.Run("tenant-scoped page + total", func(t *testing.T) {
		got, total, err := svc.ListOrders(ctx, "cust-a", Filter{})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if total != 2 || len(got) != 2 {
			t.Fatalf("want 2 orders for cust-a, got len=%d total=%d", len(got), total)
		}
	})

	t.Run("status filter passed through", func(t *testing.T) {
		got, total, err := svc.ListOrders(ctx, "cust-a", Filter{Status: "CONFIRMED"})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if total != 1 || len(got) != 1 || got[0].ID != "1" {
			t.Fatalf("want only order 1, got len=%d total=%d", len(got), total)
		}
		if store.lastFilter.Status != "CONFIRMED" {
			t.Fatalf("filter not forwarded: %+v", store.lastFilter)
		}
	})

	t.Run("pagination forwarded", func(t *testing.T) {
		_, total, err := svc.ListOrders(ctx, "cust-a", Filter{Limit: 1, Offset: 1})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if total != 2 {
			t.Fatalf("total should be unfiltered by paging, got %d", total)
		}
		if store.lastFilter.Limit != 1 || store.lastFilter.Offset != 1 {
			t.Fatalf("paging not forwarded: %+v", store.lastFilter)
		}
	})

	t.Run("missing customer id", func(t *testing.T) {
		if _, _, err := svc.ListOrders(ctx, "", Filter{}); !isBadRequest(err) {
			t.Fatalf("want bad request, got %v", err)
		}
	})

	t.Run("unknown status rejected before store", func(t *testing.T) {
		if _, _, err := svc.ListOrders(ctx, "cust-a", Filter{Status: "BOGUS"}); !isBadRequest(err) {
			t.Fatalf("want bad request, got %v", err)
		}
	})
}

func TestGetOrder(t *testing.T) {
	store := newFakeStore(
		mkDetail("1", "cust-a", "CONFIRMED"),
		mkDetail("2", "cust-b", "CONFIRMED"),
	)
	svc := NewService(store)
	ctx := context.Background()

	t.Run("found", func(t *testing.T) {
		d, err := svc.GetOrder(ctx, "cust-a", "1")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if d.ID != "1" {
			t.Fatalf("want order 1, got %q", d.ID)
		}
	})

	t.Run("missing id -> not found", func(t *testing.T) {
		if _, err := svc.GetOrder(ctx, "cust-a", "999"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})

	t.Run("cross-tenant -> not found (never forbidden)", func(t *testing.T) {
		if _, err := svc.GetOrder(ctx, "cust-a", "2"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})

	t.Run("missing args", func(t *testing.T) {
		if _, err := svc.GetOrder(ctx, "", "1"); !isBadRequest(err) {
			t.Fatalf("want bad request, got %v", err)
		}
		if _, err := svc.GetOrder(ctx, "cust-a", ""); !isBadRequest(err) {
			t.Fatalf("want bad request, got %v", err)
		}
	})
}

func TestSetDeliveryInstructions(t *testing.T) {
	ctx := context.Background()

	t.Run("ok", func(t *testing.T) {
		store := newFakeStore(mkDetail("1", "cust-a", "CONFIRMED"))
		svc := NewService(store)
		if err := svc.SetDeliveryInstructions(ctx, "cust-a", "1", "leave at gate B"); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if store.setCalls != 1 || store.lastSetText != "leave at gate B" {
			t.Fatalf("store write not as expected: calls=%d text=%q", store.setCalls, store.lastSetText)
		}
	})

	t.Run("at the length bound is allowed", func(t *testing.T) {
		store := newFakeStore(mkDetail("1", "cust-a", "CONFIRMED"))
		svc := NewService(store)
		if err := svc.SetDeliveryInstructions(ctx, "cust-a", "1", strings.Repeat("x", maxInstructionsLen)); err != nil {
			t.Fatalf("unexpected err at bound: %v", err)
		}
	})

	t.Run("over the length bound rejected before store", func(t *testing.T) {
		store := newFakeStore(mkDetail("1", "cust-a", "CONFIRMED"))
		svc := NewService(store)
		err := svc.SetDeliveryInstructions(ctx, "cust-a", "1", strings.Repeat("x", maxInstructionsLen+1))
		if !isBadRequest(err) {
			t.Fatalf("want bad request, got %v", err)
		}
		if store.setCalls != 0 {
			t.Fatalf("store should not be called when text too long, calls=%d", store.setCalls)
		}
	})

	t.Run("cross-tenant -> not found", func(t *testing.T) {
		store := newFakeStore(mkDetail("1", "cust-a", "CONFIRMED"))
		svc := NewService(store)
		if err := svc.SetDeliveryInstructions(ctx, "cust-b", "1", "x"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})

	t.Run("missing args", func(t *testing.T) {
		store := newFakeStore()
		svc := NewService(store)
		if err := svc.SetDeliveryInstructions(ctx, "", "1", "x"); !isBadRequest(err) {
			t.Fatalf("want bad request, got %v", err)
		}
		if err := svc.SetDeliveryInstructions(ctx, "cust-a", "", "x"); !isBadRequest(err) {
			t.Fatalf("want bad request, got %v", err)
		}
	})
}
