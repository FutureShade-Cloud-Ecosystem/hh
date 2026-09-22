package inventory

// stock_lookup_test.go, the branch fence and the two empties, without a database.
//
// The SQL itself is proved in the integration file. What is proved HERE is the thing that decides
// what the SQL is allowed to see: the parameter BranchReach renders. That parameter is one value
// with three meanings, and getting the third one wrong inverts the whole fence while leaving a
// working-looking screen behind.

import (
	"context"
	"errors"
	"testing"
)

// TestBranchReach_TheThreeCases, the fence, at the level that decides the outcome.
func TestBranchReach_TheThreeCases(t *testing.T) {
	t.Run("global reaches everything", func(t *testing.T) {
		if got := (BranchReach{Global: true}).branchFilter(); got != nil {
			t.Fatalf("filter = %#v, want nil (SQL NULL → no branch predicate)", got)
		}
	})
	t.Run("grants reach exactly those branches", func(t *testing.T) {
		got, ok := (BranchReach{BranchIDs: []string{"b-1", "b-2"}}).branchFilter().([]string)
		if !ok || len(got) != 2 || got[0] != "b-1" || got[1] != "b-2" {
			t.Fatalf("filter = %#v, want [b-1 b-2]", got)
		}
	})
	// THE ONE THAT INVERTS THE FENCE. nil here would render SQL NULL, the predicate would
	// short-circuit true, and a user with no branch grants would be handed every yard in the company
	//, while the screen looked entirely normal.
	t.Run("no grants reach nothing", func(t *testing.T) {
		got, ok := (BranchReach{}).branchFilter().([]string)
		if !ok {
			t.Fatalf("filter = %#v, want an EMPTY []string, nil would hand the whole company to a caller with no grants", got)
		}
		if len(got) != 0 {
			t.Fatalf("filter = %#v, want an empty list", got)
		}
	})
}

func TestBranchReach_ReachesNothingShortCircuits(t *testing.T) {
	if (BranchReach{Global: true}).reachesNothing() {
		t.Error("a global reach was treated as reaching nothing")
	}
	if (BranchReach{BranchIDs: []string{"b-1"}}).reachesNothing() {
		t.Error("a granted reach was treated as reaching nothing")
	}
	if !(BranchReach{}).reachesNothing() {
		t.Error("a caller with no grants must short-circuit rather than issue a query that cannot match")
	}
}

// - the service, over a fake seam -

type fakeStockLookup struct {
	*fakeStore
	product ProductLookup
	rows    []BranchStock
	found   bool
	err     error

	gotRef   string
	gotReach BranchReach
	queried  bool
}

func (f *fakeStockLookup) ResolveProductRef(_ context.Context, ref string) (ProductLookup, bool, error) {
	f.gotRef = ref
	return f.product, f.found, f.err
}

func (f *fakeStockLookup) ProductStockByBranch(_ context.Context, _ string, reach BranchReach) ([]BranchStock, error) {
	f.gotReach, f.queried = reach, true
	return f.rows, f.err
}

func newStockLookupSvc(f *fakeStockLookup) *Service {
	f.fakeStore = newFakeStore()
	return NewService(nil).withStore(f)
}

func TestLookupStock_NoSuchProductIsFoundFalseNotAnError(t *testing.T) {
	f := &fakeStockLookup{found: false}
	svc := newStockLookupSvc(f)

	p, rows, found, err := svc.LookupStock(context.Background(), BranchReach{Global: true}, "nope")
	if err != nil {
		t.Fatalf("an unknown product is an ANSWER: %v", err)
	}
	if found || len(rows) != 0 || p.ProductID != "" {
		t.Fatalf("found=%v rows=%d p=%+v", found, len(rows), p)
	}
	if f.queried {
		t.Error("the balance query ran for a product that did not resolve, a wasted round trip on every typo")
	}
}

// TestLookupStock_AResolvedProductWithNoBalancesKeepsItsIdentity is the DIB-443 distinction at the
// service level: the answer must still be able to NAME the product it found nothing for.
func TestLookupStock_AResolvedProductWithNoBalancesKeepsItsIdentity(t *testing.T) {
	f := &fakeStockLookup{
		product: ProductLookup{ProductID: "p-1", SKU: "TB-BLU60", Name: "Techo-Bloc Blu 60", UOM: "EA"},
		found:   true,
		rows:    nil,
	}
	svc := newStockLookupSvc(f)

	p, rows, found, err := svc.LookupStock(context.Background(), BranchReach{Global: true}, "TB-BLU60")
	if err != nil {
		t.Fatalf("a product with no stock is an ANSWER: %v", err)
	}
	if !found {
		t.Fatal("found=false for a product that resolved, the two empties have been collapsed into one")
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %d", len(rows))
	}
	if p.SKU != "TB-BLU60" || p.UOM != "EA" {
		t.Fatalf("product = %+v", p)
	}
}

func TestLookupStock_TheReachTravelsIntoTheQuery(t *testing.T) {
	f := &fakeStockLookup{product: ProductLookup{ProductID: "p-1"}, found: true}
	svc := newStockLookupSvc(f)

	if _, _, _, err := svc.LookupStock(context.Background(), BranchReach{BranchIDs: []string{"b-1"}}, "TB-BLU60"); err != nil {
		t.Fatalf("LookupStock: %v", err)
	}
	if f.gotReach.Global || len(f.gotReach.BranchIDs) != 1 || f.gotReach.BranchIDs[0] != "b-1" {
		t.Fatalf("the balance query was scoped %+v, want the caller's single branch", f.gotReach)
	}
}

// TestLookupStock_NoGrantsSeeNoYards: the product still resolves (the catalogue is not branch
// scoped, and pretending it is would tell a user their SKU is wrong when it is not), but no
// position comes back and no query is issued.
func TestLookupStock_NoGrantsSeeNoYards(t *testing.T) {
	f := &fakeStockLookup{product: ProductLookup{ProductID: "p-1", SKU: "TB-BLU60"}, found: true}
	svc := newStockLookupSvc(f)

	p, rows, found, err := svc.LookupStock(context.Background(), BranchReach{}, "TB-BLU60")
	if err != nil {
		t.Fatalf("LookupStock: %v", err)
	}
	if !found || p.SKU != "TB-BLU60" {
		t.Fatalf("the product should still resolve: found=%v p=%+v", found, p)
	}
	if len(rows) != 0 {
		t.Fatalf("a caller with no branch grants saw %d stock rows", len(rows))
	}
	if f.queried {
		t.Error("a query was issued for a caller who can reach no branch")
	}
}

func TestLookupStock_AnEmptyReferenceIsA400NotAnEmptyAnswer(t *testing.T) {
	svc := newStockLookupSvc(&fakeStockLookup{})
	_, _, _, err := svc.LookupStock(context.Background(), BranchReach{Global: true}, "   ")
	var br badRequestError
	if !errors.As(err, &br) {
		t.Fatalf("an empty reference = %v, want a bad-request, it is a client bug, not 'no such product'", err)
	}
}
