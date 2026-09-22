package procurement

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
)

const (
	tenant = "cust-A"
	other  = "cust-B"
)

// fakeProduct is the catalog row AddLine captures sku/description from (no DB; mirrors the
// products join the pgx store performs).
type fakeProduct struct {
	sku         string
	description string
}

// fakeStore is an in-memory store that mirrors the Repository contract so the service tests
// exercise the full composition without pgx. Every op is tenant-scoped (a foreign or missing
// id is ErrNotFound, never a leak); status guards match repository.go:
//
//	UpdateProject : status change legal only via canTransition (PLANNING<->QUOTING, Cancel)
//	DeleteProject : PLANNING only (else ErrInvalidTransition)
//	SubmitRFQ     : PLANNING -> QUOTING, persisting indicative prices
type fakeStore struct {
	projects map[string]*Project
	lines    map[string][]Line
	products map[string]fakeProduct
	seq      int
	clock    int64
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		projects: map[string]*Project{},
		lines:    map[string][]Line{},
		products: map[string]fakeProduct{},
	}
}

// tick returns a strictly increasing timestamp so updated_at ordering is deterministic.
func (f *fakeStore) tick() time.Time {
	f.clock++
	return time.Unix(f.clock, 0).UTC()
}

func (f *fakeStore) id(prefix string) string {
	f.seq++
	return fmt.Sprintf("%s-%d", prefix, f.seq)
}

func (f *fakeStore) seedProduct(id, sku, description string) {
	f.products[id] = fakeProduct{sku: sku, description: description}
}

// scoped returns the tenant's project or ErrNotFound (covers missing id AND cross-tenant).
func (f *fakeStore) scoped(customerID, projectID string) (*Project, error) {
	p, ok := f.projects[projectID]
	if !ok || p.CustomerID != customerID {
		return nil, ErrNotFound
	}
	return p, nil
}

// aggregate stamps the derived line_count + subtotal_cents (SUM of indicative line totals).
func (f *fakeStore) aggregate(p Project) Project {
	lines := f.lines[p.ID]
	p.LineCount = len(lines)
	var subtotal int64
	for _, ln := range lines {
		subtotal += ln.IndicativeLineTotalCents
	}
	p.SubtotalCents = subtotal
	return p
}

func (f *fakeStore) CreateProject(_ context.Context, customerID, createdByUserID, name string) (Project, error) {
	_ = createdByUserID
	now := f.tick()
	p := &Project{
		ID:         f.id("proj"),
		CustomerID: customerID,
		Name:       name,
		Status:     StatusPlanning,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	f.projects[p.ID] = p
	return f.aggregate(*p), nil
}

func (f *fakeStore) ListProjects(_ context.Context, customerID string) ([]Project, error) {
	var out []Project
	for _, p := range f.projects {
		if p.CustomerID != customerID || p.Status == StatusCancelled {
			continue
		}
		out = append(out, f.aggregate(*p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func (f *fakeStore) GetProject(_ context.Context, customerID, projectID string) (ProjectDetail, error) {
	p, err := f.scoped(customerID, projectID)
	if err != nil {
		return ProjectDetail{}, err
	}
	d := ProjectDetail{Project: f.aggregate(*p)}
	lines := append([]Line(nil), f.lines[projectID]...)
	sort.Slice(lines, func(i, j int) bool { return lines[i].LineNo < lines[j].LineNo })
	d.Lines = lines
	return d, nil
}

func (f *fakeStore) UpdateProject(_ context.Context, customerID, projectID string, name, status *string) (Project, error) {
	p, err := f.scoped(customerID, projectID)
	if err != nil {
		return Project{}, err
	}
	if status != nil && *status != p.Status && !canTransition(p.Status, *status) {
		return Project{}, ErrInvalidTransition
	}
	if name != nil {
		p.Name = *name
	}
	if status != nil {
		p.Status = *status
	}
	p.UpdatedAt = f.tick()
	return f.aggregate(*p), nil
}

func (f *fakeStore) DeleteProject(_ context.Context, customerID, projectID string) error {
	p, err := f.scoped(customerID, projectID)
	if err != nil {
		return err
	}
	if p.Status != StatusPlanning {
		return ErrInvalidTransition
	}
	delete(f.projects, projectID)
	delete(f.lines, projectID)
	return nil
}

func (f *fakeStore) AddLine(_ context.Context, customerID, projectID string, in AddLineRequest) (Line, error) {
	p, err := f.scoped(customerID, projectID)
	if err != nil {
		return Line{}, err
	}
	// Mirror repository.go: the PLANNING freeze is enforced atomically under the locked status
	// read; a non-PLANNING project rejects the mutation with ErrInvalidTransition (reviews #2/#3/#6).
	if p.Status != StatusPlanning {
		return Line{}, ErrInvalidTransition
	}
	pr, ok := f.products[in.ProductID]
	if !ok {
		return Line{}, httpx.BadRequestf("product %q not found", in.ProductID)
	}
	var maxNo int
	for _, ln := range f.lines[projectID] {
		if ln.LineNo > maxNo {
			maxNo = ln.LineNo
		}
	}
	ln := Line{
		ID:          f.id("line"),
		LineNo:      maxNo + 1,
		ProductID:   in.ProductID,
		SKU:         pr.sku,
		Description: pr.description,
		QtyEntered:  in.QtyEntered,
		UOM:         in.UOM,
		Notes:       in.Notes,
	}
	f.lines[projectID] = append(f.lines[projectID], ln)
	return ln, nil
}

func (f *fakeStore) UpdateLine(_ context.Context, customerID, projectID, lineID string, in UpdateLineRequest) (Line, error) {
	p, err := f.scoped(customerID, projectID)
	if err != nil {
		return Line{}, err
	}
	// Mirror repository.go: PLANNING freeze enforced under the locked status read (reviews #2/#3/#6).
	if p.Status != StatusPlanning {
		return Line{}, ErrInvalidTransition
	}
	lines := f.lines[projectID]
	for i := range lines {
		if lines[i].ID != lineID {
			continue
		}
		if in.QtyEntered != nil {
			lines[i].QtyEntered = *in.QtyEntered
		}
		if in.UOM != nil {
			lines[i].UOM = *in.UOM
		}
		if in.Notes != nil {
			lines[i].Notes = *in.Notes
		}
		// Mirror repository.go: a qty/uom change invalidates the stored indicative price
		// (cleared, priced=false); a notes-only edit leaves it intact (review #3).
		if in.QtyEntered != nil || in.UOM != nil {
			lines[i].IndicativeUnitPriceCents = 0
			lines[i].IndicativeLineTotalCents = 0
			lines[i].Priced = false
		}
		return lines[i], nil
	}
	return Line{}, ErrNotFound
}

func (f *fakeStore) RemoveLine(_ context.Context, customerID, projectID, lineID string) error {
	p, err := f.scoped(customerID, projectID)
	if err != nil {
		return err
	}
	// Mirror repository.go: PLANNING freeze enforced under the locked status read (reviews #2/#3/#6).
	if p.Status != StatusPlanning {
		return ErrInvalidTransition
	}
	lines := f.lines[projectID]
	for i := range lines {
		if lines[i].ID == lineID {
			f.lines[projectID] = append(lines[:i], lines[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

func (f *fakeStore) SubmitRFQ(ctx context.Context, customerID, projectID string, priced []PricedLine) (ProjectDetail, error) {
	p, err := f.scoped(customerID, projectID)
	if err != nil {
		return ProjectDetail{}, err
	}
	if !canTransition(p.Status, StatusQuoting) {
		return ProjectDetail{}, ErrInvalidTransition
	}
	byID := map[string]PricedLine{}
	for _, pl := range priced {
		byID[pl.LineID] = pl
	}
	lines := f.lines[projectID]
	for i := range lines {
		if pl, ok := byID[lines[i].ID]; ok {
			// Mirror repository.go: an unpriced (best-effort) line stays priced=false with
			// zero cents; only a successfully priced line carries indicative cents (review #1).
			lines[i].IndicativeUnitPriceCents = pl.IndicativeUnitPriceCents
			lines[i].IndicativeLineTotalCents = pl.IndicativeLineTotalCents
			lines[i].Priced = pl.Priced
		}
	}
	p.Status = StatusQuoting
	p.UpdatedAt = f.tick()
	return f.GetProject(ctx, customerID, projectID)
}

// -- pricing/account/catalog seams (in-memory) -----------------------------------

// fakePricing prices every line at a flat unit cents * qty so SubmitRFQ persistence is
// deterministic and verifiable.
type fakePricing struct{ unitCents int64 }

func (p fakePricing) DeriveLinePrice(_ context.Context, in pricing.ResolveInput) (pricing.LinePrice, error) {
	return pricing.LinePrice{
		UnitPriceCents: p.unitCents,
		LineTotalCents: p.unitCents * int64(in.QtyEntered),
	}, nil
}

// failPricing simulates the resolver rejecting EVERY line (e.g. errNoListPrice on a $0 cost
// basis): SubmitRFQ must treat this as best-effort and leave the line unpriced, not 500.
type failPricing struct{}

func (failPricing) DeriveLinePrice(_ context.Context, _ pricing.ResolveInput) (pricing.LinePrice, error) {
	return pricing.LinePrice{}, errors.New("pricing: no retail anchor (list price) for product")
}

type fakeAccounts struct{ acct customer.Account }

func (a fakeAccounts) ResolveAccount(_ context.Context, customerID string) (customer.Account, error) {
	acct := a.acct
	acct.CustomerID = customerID
	return acct, nil
}

type fakeCatalog struct{}

func (fakeCatalog) GetCategory(_ context.Context, productID string) (string, error) {
	return "cat-" + productID, nil
}

// failCatalog simulates GetCategory faulting for every product. SubmitRFQ must treat the
// category as unknown (empty) and CONTINUE best-effort -- a category fault never fails the RFQ
// nor escapes as a non-httpx 500; the line just ends up unpriced (review #2).
type failCatalog struct{}

func (failCatalog) GetCategory(_ context.Context, _ string) (string, error) {
	return "", errors.New("catalog: product category lookup failed")
}

// categoryPricing prices a line only when its category resolved; an empty category (the
// best-effort fallback after a GetCategory fault) leaves the line unanchored -> unpriced.
type categoryPricing struct{ unitCents int64 }

func (p categoryPricing) DeriveLinePrice(_ context.Context, in pricing.ResolveInput) (pricing.LinePrice, error) {
	if in.CategoryID == "" {
		return pricing.LinePrice{}, errors.New("pricing: no category anchor")
	}
	return pricing.LinePrice{
		UnitPriceCents: p.unitCents,
		LineTotalCents: p.unitCents * int64(in.QtyEntered),
	}, nil
}

// fullService wires the store + all three pricing seams (so SubmitRFQ runs end to end).
func fullService(s store) *Service {
	return NewService(s).
		WithPricing(fakePricing{unitCents: 1000}).
		WithAccounts(fakeAccounts{acct: customer.Account{CustomerType: "CONTRACTOR", BranchID: "branch-1"}}).
		WithCatalog(fakeCatalog{})
}

func strptr(s string) *string { return &s }

// -- tests -----------------------------------------------------------------------

func TestCreateProject(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store)
	ctx := context.Background()

	p, err := svc.CreateProject(ctx, tenant, "user-1", "  Patio Phase 1  ")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.ID == "" || p.CustomerID != tenant {
		t.Fatalf("create: bad identity %+v", p)
	}
	if p.Status != StatusPlanning {
		t.Fatalf("create: status = %q, want PLANNING", p.Status)
	}
	if p.Name != "Patio Phase 1" {
		t.Fatalf("create: name = %q, want trimmed", p.Name)
	}
	if p.LineCount != 0 || p.SubtotalCents != 0 {
		t.Fatalf("create: new project not empty: %+v", p)
	}

	// Validation: empty customer / empty name are bad requests (not ErrNotFound).
	if _, err := svc.CreateProject(ctx, "", "u", "x"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("create empty customer: want bad-request, got %v", err)
	}
	if _, err := svc.CreateProject(ctx, tenant, "u", "   "); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("create blank name: want bad-request, got %v", err)
	}
}

func TestListProjects_ExcludesCancelled_AndAggregates(t *testing.T) {
	store := newFakeStore()
	store.seedProduct("prod-1", "SKU-1", "Paver")
	svc := fullService(store)
	ctx := context.Background()

	keep, _ := svc.CreateProject(ctx, tenant, "u", "Keep")
	cancel, _ := svc.CreateProject(ctx, tenant, "u", "Cancel me")
	svc.CreateProject(ctx, other, "u", "Foreign")

	// Cancel the second project -> it must drop out of the board list.
	if _, err := svc.UpdateProject(ctx, tenant, cancel.ID, nil, strptr(StatusCancelled)); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	// Add two priced-by-rfq lines so line_count + subtotal are exercised.
	if _, err := svc.AddLine(ctx, tenant, keep.ID, AddLineRequest{ProductID: "prod-1", QtyEntered: "2", UOM: "EA"}); err != nil {
		t.Fatalf("add line 1: %v", err)
	}
	if _, err := svc.AddLine(ctx, tenant, keep.ID, AddLineRequest{ProductID: "prod-1", QtyEntered: "3", UOM: "EA"}); err != nil {
		t.Fatalf("add line 2: %v", err)
	}
	if _, err := svc.SubmitRFQ(ctx, tenant, keep.ID); err != nil {
		t.Fatalf("submit rfq: %v", err)
	}

	list, err := svc.ListProjects(ctx, tenant)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list: got %d projects, want 1 (cancelled + foreign excluded)", len(list))
	}
	got := list[0]
	if got.ID != keep.ID {
		t.Fatalf("list: returned %q, want %q", got.ID, keep.ID)
	}
	if got.LineCount != 2 {
		t.Fatalf("list: line_count = %d, want 2", got.LineCount)
	}
	if got.SubtotalCents != 5000 { // (2+3) * 1000
		t.Fatalf("list: subtotal_cents = %d, want 5000", got.SubtotalCents)
	}
}

func TestListProjects_OrderedByUpdatedDesc(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store)
	ctx := context.Background()

	a, _ := svc.CreateProject(ctx, tenant, "u", "A")
	b, _ := svc.CreateProject(ctx, tenant, "u", "B")
	// Touch A after B so A becomes the most-recently-updated.
	if _, err := svc.UpdateProject(ctx, tenant, a.ID, strptr("A renamed"), nil); err != nil {
		t.Fatalf("rename: %v", err)
	}

	list, err := svc.ListProjects(ctx, tenant)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].ID != a.ID || list[1].ID != b.ID {
		t.Fatalf("order: got %v, want [%s %s] (updated desc)", []string{list[0].ID, list[1].ID}, a.ID, b.ID)
	}
}

func TestGetProject_NotFoundAndCrossTenant(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store)
	ctx := context.Background()

	p, _ := svc.CreateProject(ctx, tenant, "u", "Mine")

	for _, tc := range []struct {
		name            string
		customerID, pID string
	}{
		{"missing id", tenant, "nope"},
		{"cross-tenant id", other, p.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.GetProject(ctx, tc.customerID, tc.pID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("GetProject: err = %v, want ErrNotFound", err)
			}
		})
	}

	// Owner read still works (cross-tenant probe did not disturb it).
	if _, err := svc.GetProject(ctx, tenant, p.ID); err != nil {
		t.Fatalf("owner get: %v", err)
	}
}

func TestLines_AddUpdateRemove_Sequencing(t *testing.T) {
	store := newFakeStore()
	store.seedProduct("prod-1", "SKU-1", "Paver")
	store.seedProduct("prod-2", "SKU-2", "Sand")
	svc := NewService(store)
	ctx := context.Background()

	p, _ := svc.CreateProject(ctx, tenant, "u", "Build")

	l1, err := svc.AddLine(ctx, tenant, p.ID, AddLineRequest{ProductID: "prod-1", QtyEntered: "4", UOM: "EA"})
	if err != nil {
		t.Fatalf("add l1: %v", err)
	}
	l2, err := svc.AddLine(ctx, tenant, p.ID, AddLineRequest{ProductID: "prod-2", QtyEntered: "10", UOM: "BAG"})
	if err != nil {
		t.Fatalf("add l2: %v", err)
	}
	if l1.LineNo != 1 || l2.LineNo != 2 {
		t.Fatalf("line_no sequencing: got %d,%d want 1,2", l1.LineNo, l2.LineNo)
	}
	if l1.SKU != "SKU-1" || l1.Description != "Paver" {
		t.Fatalf("add: catalog sku/description not captured: %+v", l1)
	}

	// Remove l1, add a third: line_no must continue from MAX (3), not reuse 1.
	if err := svc.RemoveLine(ctx, tenant, p.ID, l1.ID); err != nil {
		t.Fatalf("remove l1: %v", err)
	}
	l3, err := svc.AddLine(ctx, tenant, p.ID, AddLineRequest{ProductID: "prod-1", QtyEntered: "1", UOM: "EA"})
	if err != nil {
		t.Fatalf("add l3: %v", err)
	}
	if l3.LineNo != 3 {
		t.Fatalf("line_no after remove: got %d, want 3 (MAX+1)", l3.LineNo)
	}

	// Partial update: qty only; uom/notes untouched.
	upd, err := svc.UpdateLine(ctx, tenant, p.ID, l2.ID, UpdateLineRequest{QtyEntered: strptr("99")})
	if err != nil {
		t.Fatalf("update l2: %v", err)
	}
	if upd.QtyEntered != "99" || upd.UOM != "BAG" {
		t.Fatalf("partial update: %+v", upd)
	}

	d, err := svc.GetProject(ctx, tenant, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(d.Lines) != 2 || d.Lines[0].LineNo != 2 || d.Lines[1].LineNo != 3 {
		t.Fatalf("ordered lines: %+v", d.Lines)
	}

	// Cross-tenant line ops must be ErrNotFound (project not owned).
	if err := svc.RemoveLine(ctx, other, p.ID, l2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant remove: err = %v, want ErrNotFound", err)
	}
}

func TestDeleteProject_PlanningOnly(t *testing.T) {
	store := newFakeStore()
	store.seedProduct("prod-1", "SKU-1", "Paver")
	svc := NewService(store)
	ctx := context.Background()

	planning, _ := svc.CreateProject(ctx, tenant, "u", "Planning")
	quoting, _ := svc.CreateProject(ctx, tenant, "u", "Quoting")
	// Move the second to QUOTING via submit-rfq (needs at least one line to quote).
	svc2 := fullService(store)
	if _, err := svc2.AddLine(ctx, tenant, quoting.ID, AddLineRequest{ProductID: "prod-1", QtyEntered: "1", UOM: "EA"}); err != nil {
		t.Fatalf("add line: %v", err)
	}
	if _, err := svc2.SubmitRFQ(ctx, tenant, quoting.ID); err != nil {
		t.Fatalf("submit: %v", err)
	}

	if err := svc.DeleteProject(ctx, tenant, quoting.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("delete QUOTING: err = %v, want ErrInvalidTransition", err)
	}
	if err := svc.DeleteProject(ctx, tenant, planning.ID); err != nil {
		t.Fatalf("delete PLANNING: %v", err)
	}
	if _, err := svc.GetProject(ctx, tenant, planning.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: err = %v, want ErrNotFound", err)
	}

	// Cross-tenant / missing delete is ErrNotFound, not a transition error.
	if err := svc.DeleteProject(ctx, other, quoting.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant delete: err = %v, want ErrNotFound", err)
	}
}

func TestSubmitRFQ_PricesPersistedAndStatusMoved(t *testing.T) {
	store := newFakeStore()
	store.seedProduct("prod-1", "SKU-1", "Paver")
	store.seedProduct("prod-2", "SKU-2", "Sand")
	svc := fullService(store)
	ctx := context.Background()

	p, _ := svc.CreateProject(ctx, tenant, "u", "Build")
	svc.AddLine(ctx, tenant, p.ID, AddLineRequest{ProductID: "prod-1", QtyEntered: "2", UOM: "EA"})
	svc.AddLine(ctx, tenant, p.ID, AddLineRequest{ProductID: "prod-2", QtyEntered: "3", UOM: "BAG"})

	d, err := svc.SubmitRFQ(ctx, tenant, p.ID)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if d.Status != StatusQuoting {
		t.Fatalf("status = %q, want QUOTING", d.Status)
	}
	if len(d.Lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(d.Lines))
	}
	for _, ln := range d.Lines {
		if !ln.Priced || ln.IndicativeUnitPriceCents != 1000 {
			t.Fatalf("line not priced indicatively: %+v", ln)
		}
	}
	if d.Lines[0].IndicativeLineTotalCents != 2000 || d.Lines[1].IndicativeLineTotalCents != 3000 {
		t.Fatalf("line totals: %d,%d want 2000,3000", d.Lines[0].IndicativeLineTotalCents, d.Lines[1].IndicativeLineTotalCents)
	}
	if d.SubtotalCents != 5000 {
		t.Fatalf("subtotal = %d, want 5000", d.SubtotalCents)
	}

	// Re-submitting a QUOTING project is an invalid transition (no PLANNING source).
	if _, err := svc.SubmitRFQ(ctx, tenant, p.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("re-submit: err = %v, want ErrInvalidTransition", err)
	}
}

func TestSubmitRFQ_SeamsNotWired(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store) // no With* seams
	ctx := context.Background()
	p, _ := svc.CreateProject(ctx, tenant, "u", "Build")

	if _, err := svc.SubmitRFQ(ctx, tenant, p.ID); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("submit without seams: err = %v, want ErrNotImplemented", err)
	}
}

func TestUpdateProject_InvalidTransitions(t *testing.T) {
	cases := []struct {
		name    string
		start   string // status to drive the project into before the move
		to      string
		wantErr error
	}{
		{"planning->quoting via PUT rejected", StatusPlanning, StatusQuoting, ErrInvalidTransition},
		{"planning->ordered illegal", StatusPlanning, StatusOrdered, ErrInvalidTransition},
		{"planning->fulfilled illegal", StatusPlanning, StatusFulfilled, ErrInvalidTransition},
		{"planning->cancelled ok", StatusPlanning, StatusCancelled, nil},
		{"quoting->planning recall ok", StatusQuoting, StatusPlanning, nil},
		{"quoting->cancelled illegal", StatusQuoting, StatusCancelled, ErrInvalidTransition},
		{"quoting->ordered illegal", StatusQuoting, StatusOrdered, ErrInvalidTransition},
		{"bogus status bad-request", StatusPlanning, "BOGUS", nil}, // handled below as bad-request
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore()
			store.seedProduct("prod-1", "SKU-1", "Paver")
			svc := fullService(store)
			ctx := context.Background()
			p, _ := svc.CreateProject(ctx, tenant, "u", "P")
			if tc.start == StatusQuoting {
				// A submit-rfq needs at least one line; add one before moving to QUOTING.
				if _, err := svc.AddLine(ctx, tenant, p.ID, AddLineRequest{ProductID: "prod-1", QtyEntered: "1", UOM: "EA"}); err != nil {
					t.Fatalf("setup add line: %v", err)
				}
				if _, err := svc.SubmitRFQ(ctx, tenant, p.ID); err != nil {
					t.Fatalf("setup to QUOTING: %v", err)
				}
			}

			_, err := svc.UpdateProject(ctx, tenant, p.ID, nil, strptr(tc.to))

			if tc.to == "BOGUS" {
				if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalidTransition) {
					t.Fatalf("bogus status: want bad-request, got %v", err)
				}
				return
			}
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("%s->%s: err = %v, want %v", tc.start, tc.to, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s->%s: unexpected err %v", tc.start, tc.to, err)
			}
		})
	}
}

func TestUpdateProject_RenameAndValidation(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store)
	ctx := context.Background()
	p, _ := svc.CreateProject(ctx, tenant, "u", "Old")

	got, err := svc.UpdateProject(ctx, tenant, p.ID, strptr("New name"), nil)
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got.Name != "New name" || got.Status != StatusPlanning {
		t.Fatalf("rename result: %+v", got)
	}

	// Nothing-to-update and blank-name are bad requests.
	if _, err := svc.UpdateProject(ctx, tenant, p.ID, nil, nil); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("empty update: want bad-request, got %v", err)
	}
	if _, err := svc.UpdateProject(ctx, tenant, p.ID, strptr("  "), nil); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("blank rename: want bad-request, got %v", err)
	}

	// Cross-tenant update is ErrNotFound.
	if _, err := svc.UpdateProject(ctx, other, p.ID, strptr("x"), nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant update: err = %v, want ErrNotFound", err)
	}
}

func TestAddLine_Validation(t *testing.T) {
	store := newFakeStore()
	store.seedProduct("prod-1", "SKU-1", "Paver")
	svc := NewService(store)
	ctx := context.Background()
	p, _ := svc.CreateProject(ctx, tenant, "u", "P")

	bad := func(name string, _ Line, err error) {
		if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("%s: want bad-request, got %v", name, err)
		}
	}
	l, err := svc.AddLine(ctx, tenant, p.ID, AddLineRequest{ProductID: "", QtyEntered: "1", UOM: "EA"})
	bad("missing product", l, err)
	l, err = svc.AddLine(ctx, tenant, p.ID, AddLineRequest{ProductID: "prod-1", QtyEntered: "0", UOM: "EA"})
	bad("non-positive qty", l, err)
	l, err = svc.AddLine(ctx, tenant, p.ID, AddLineRequest{ProductID: "prod-1", QtyEntered: "abc", UOM: "EA"})
	bad("non-numeric qty", l, err)
	l, err = svc.AddLine(ctx, tenant, p.ID, AddLineRequest{ProductID: "prod-1", QtyEntered: "1", UOM: "  "})
	bad("blank uom", l, err)
}

// TestValidateQty_StrictDecimal pins the strict positive-decimal grammar: ParseFloat-only
// inputs that used to slip through (NaN/Inf/hex/exponent/signs) are now rejected (review #4/#5).
func TestValidateQty_StrictDecimal(t *testing.T) {
	store := newFakeStore()
	store.seedProduct("prod-1", "SKU-1", "Paver")
	svc := NewService(store)
	ctx := context.Background()
	p, _ := svc.CreateProject(ctx, tenant, "u", "P")

	// "1." / ".5" / "10." are trailing/leading-dot decimals ParseFloat accepts -> now in accept.
	reject := []string{"NaN", "Inf", "+Inf", "-Inf", "0x1A", "1e3", "1E3", "+1", "-1", ".", "", "  ", "1,5", "abc", "0", "0.0", "00.0", " 0 "}
	for _, q := range reject {
		if _, err := svc.AddLine(ctx, tenant, p.ID, AddLineRequest{ProductID: "prod-1", QtyEntered: q, UOM: "EA"}); err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("qty %q: want bad-request, got %v", q, err)
		}
	}
	accept := []string{"1", "10", "0.5", ".5", "1.", "10.", "2.50", "100"}
	for _, q := range accept {
		if _, err := svc.AddLine(ctx, tenant, p.ID, AddLineRequest{ProductID: "prod-1", QtyEntered: q, UOM: "EA"}); err != nil {
			t.Fatalf("qty %q: want accepted, got %v", q, err)
		}
	}
}

// TestSubmitRFQ_EmptyProjectRejected: a project with no lines has nothing to quote and must
// be rejected (bad-request) BEFORE any lane move (review #7).
func TestSubmitRFQ_EmptyProjectRejected(t *testing.T) {
	store := newFakeStore()
	svc := fullService(store)
	ctx := context.Background()
	p, _ := svc.CreateProject(ctx, tenant, "u", "Empty")

	_, err := svc.SubmitRFQ(ctx, tenant, p.ID)
	if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("empty submit: want bad-request, got %v", err)
	}
	// The project must NOT have moved off PLANNING.
	d, gerr := svc.GetProject(ctx, tenant, p.ID)
	if gerr != nil {
		t.Fatalf("get: %v", gerr)
	}
	if d.Status != StatusPlanning {
		t.Fatalf("status = %q, want PLANNING (no transition on empty submit)", d.Status)
	}
}

// TestLineCRUD_BlockedOutsidePlanning: once a project leaves PLANNING (here QUOTING via
// submit-rfq) its material list is frozen -- Add/Update/Remove all return
// ErrInvalidTransition (409), never a silent edit (review #2).
func TestLineCRUD_BlockedOutsidePlanning(t *testing.T) {
	store := newFakeStore()
	store.seedProduct("prod-1", "SKU-1", "Paver")
	svc := fullService(store)
	ctx := context.Background()

	p, _ := svc.CreateProject(ctx, tenant, "u", "Build")
	ln, err := svc.AddLine(ctx, tenant, p.ID, AddLineRequest{ProductID: "prod-1", QtyEntered: "2", UOM: "EA"})
	if err != nil {
		t.Fatalf("seed line: %v", err)
	}
	if _, err := svc.SubmitRFQ(ctx, tenant, p.ID); err != nil {
		t.Fatalf("submit: %v", err)
	}

	if _, err := svc.AddLine(ctx, tenant, p.ID, AddLineRequest{ProductID: "prod-1", QtyEntered: "1", UOM: "EA"}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("add in QUOTING: err = %v, want ErrInvalidTransition", err)
	}
	if _, err := svc.UpdateLine(ctx, tenant, p.ID, ln.ID, UpdateLineRequest{QtyEntered: strptr("9")}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("update in QUOTING: err = %v, want ErrInvalidTransition", err)
	}
	if err := svc.RemoveLine(ctx, tenant, p.ID, ln.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("remove in QUOTING: err = %v, want ErrInvalidTransition", err)
	}
}

// TestUpdateLine_ClearsIndicativePricing: editing qty/uom on a recalled (PLANNING) line drops
// its stale indicative price (priced=false, zero cents); a notes-only edit keeps it (review #3).
func TestUpdateLine_ClearsIndicativePricing(t *testing.T) {
	store := newFakeStore()
	store.seedProduct("prod-1", "SKU-1", "Paver")
	svc := fullService(store)
	ctx := context.Background()

	p, _ := svc.CreateProject(ctx, tenant, "u", "Build")
	ln, _ := svc.AddLine(ctx, tenant, p.ID, AddLineRequest{ProductID: "prod-1", QtyEntered: "2", UOM: "EA"})
	if _, err := svc.SubmitRFQ(ctx, tenant, p.ID); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// Recall to PLANNING so the line set is editable again.
	if _, err := svc.UpdateProject(ctx, tenant, p.ID, nil, strptr(StatusPlanning)); err != nil {
		t.Fatalf("recall: %v", err)
	}

	// A notes-only edit must PRESERVE the indicative price.
	kept, err := svc.UpdateLine(ctx, tenant, p.ID, ln.ID, UpdateLineRequest{Notes: strptr("leave price")})
	if err != nil {
		t.Fatalf("notes edit: %v", err)
	}
	if !kept.Priced || kept.IndicativeUnitPriceCents != 1000 {
		t.Fatalf("notes-only edit cleared pricing: %+v", kept)
	}

	// A qty change must CLEAR the indicative price.
	upd, err := svc.UpdateLine(ctx, tenant, p.ID, ln.ID, UpdateLineRequest{QtyEntered: strptr("5")})
	if err != nil {
		t.Fatalf("qty edit: %v", err)
	}
	if upd.Priced || upd.IndicativeUnitPriceCents != 0 || upd.IndicativeLineTotalCents != 0 {
		t.Fatalf("qty change did not clear pricing: %+v", upd)
	}
}

// TestSubmitRFQ_UnpriceableLinesGraceful: when the resolver rejects every line, SubmitRFQ
// still succeeds and moves PLANNING -> QUOTING, leaving each line UNPRICED (priced=false,
// zero cents). A pricing fault never fails the RFQ nor escapes as a 500 (review #1 + #6).
func TestSubmitRFQ_UnpriceableLinesGraceful(t *testing.T) {
	store := newFakeStore()
	store.seedProduct("prod-1", "SKU-1", "Paver")
	svc := NewService(store).
		WithPricing(failPricing{}).
		WithAccounts(fakeAccounts{acct: customer.Account{CustomerType: "CONTRACTOR", BranchID: "branch-1"}}).
		WithCatalog(fakeCatalog{})
	ctx := context.Background()

	p, _ := svc.CreateProject(ctx, tenant, "u", "Build")
	svc.AddLine(ctx, tenant, p.ID, AddLineRequest{ProductID: "prod-1", QtyEntered: "2", UOM: "EA"})
	svc.AddLine(ctx, tenant, p.ID, AddLineRequest{ProductID: "prod-1", QtyEntered: "3", UOM: "EA"})

	d, err := svc.SubmitRFQ(ctx, tenant, p.ID)
	if err != nil {
		t.Fatalf("submit with unpriceable lines: %v", err)
	}
	if d.Status != StatusQuoting {
		t.Fatalf("status = %q, want QUOTING (RFQ still moves on pricing failure)", d.Status)
	}
	for _, ln := range d.Lines {
		if ln.Priced || ln.IndicativeUnitPriceCents != 0 || ln.IndicativeLineTotalCents != 0 {
			t.Fatalf("unpriceable line not left unpriced: %+v", ln)
		}
	}
	if d.SubtotalCents != 0 {
		t.Fatalf("subtotal = %d, want 0 (no priced lines)", d.SubtotalCents)
	}
}

// TestSubmitRFQ_CategoryFaultGraceful: a GetCategory fault for a product must NOT fail the
// whole RFQ (review #2). Category resolution is best-effort like pricing: on error the category
// is treated as unknown and the line ends up unpriced, but the project still moves to QUOTING.
func TestSubmitRFQ_CategoryFaultGraceful(t *testing.T) {
	store := newFakeStore()
	store.seedProduct("prod-1", "SKU-1", "Paver")
	svc := NewService(store).
		WithPricing(categoryPricing{unitCents: 1000}).
		WithAccounts(fakeAccounts{acct: customer.Account{CustomerType: "CONTRACTOR", BranchID: "branch-1"}}).
		WithCatalog(failCatalog{})
	ctx := context.Background()

	p, _ := svc.CreateProject(ctx, tenant, "u", "Build")
	svc.AddLine(ctx, tenant, p.ID, AddLineRequest{ProductID: "prod-1", QtyEntered: "2", UOM: "EA"})

	d, err := svc.SubmitRFQ(ctx, tenant, p.ID)
	if err != nil {
		t.Fatalf("submit with category fault: %v", err)
	}
	if d.Status != StatusQuoting {
		t.Fatalf("status = %q, want QUOTING (category fault must not fail the RFQ)", d.Status)
	}
	for _, ln := range d.Lines {
		if ln.Priced || ln.IndicativeUnitPriceCents != 0 || ln.IndicativeLineTotalCents != 0 {
			t.Fatalf("category-fault line not left unpriced: %+v", ln)
		}
	}
}

// TestLineMutation_NonPlanningAtomic: the PLANNING freeze is enforced by the store's locked
// status read, not a service pre-check. Simulating the locked read returning QUOTING (the
// race a concurrent SubmitRFQ would create), every line mutation is ErrInvalidTransition --
// none can slip onto a non-PLANNING project (reviews #2 + #3 + #6).
func TestLineMutation_NonPlanningAtomic(t *testing.T) {
	store := newFakeStore()
	store.seedProduct("prod-1", "SKU-1", "Paver")
	svc := fullService(store)
	ctx := context.Background()

	p, _ := svc.CreateProject(ctx, tenant, "u", "Build")
	ln, err := svc.AddLine(ctx, tenant, p.ID, AddLineRequest{ProductID: "prod-1", QtyEntered: "2", UOM: "EA"})
	if err != nil {
		t.Fatalf("seed line: %v", err)
	}
	// Simulate the locked status read seeing QUOTING (as if a racing SubmitRFQ committed first).
	store.projects[p.ID].Status = StatusQuoting

	if _, err := svc.AddLine(ctx, tenant, p.ID, AddLineRequest{ProductID: "prod-1", QtyEntered: "1", UOM: "EA"}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("add onto QUOTING: err = %v, want ErrInvalidTransition", err)
	}
	if _, err := svc.UpdateLine(ctx, tenant, p.ID, ln.ID, UpdateLineRequest{QtyEntered: strptr("9")}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("update onto QUOTING: err = %v, want ErrInvalidTransition", err)
	}
	if err := svc.RemoveLine(ctx, tenant, p.ID, ln.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("remove onto QUOTING: err = %v, want ErrInvalidTransition", err)
	}
}

// TestValidateQty_TrailingDotAndOverflow: trailing/leading-dot decimals ParseFloat accepts are
// now accepted (review #5); an overflowing magnitude reports out-of-range, NOT the misleading
// "greater than zero" message reserved for an actual non-positive value (review #7).
func TestValidateQty_TrailingDotAndOverflow(t *testing.T) {
	store := newFakeStore()
	store.seedProduct("prod-1", "SKU-1", "Paver")
	svc := NewService(store)
	ctx := context.Background()
	p, _ := svc.CreateProject(ctx, tenant, "u", "P")

	for _, q := range []string{"10.", ".5", "1."} {
		if _, err := svc.AddLine(ctx, tenant, p.ID, AddLineRequest{ProductID: "prod-1", QtyEntered: q, UOM: "EA"}); err != nil {
			t.Fatalf("qty %q: want accepted, got %v", q, err)
		}
	}

	// An overflowing magnitude must be out-of-range, never the >0 message.
	overflow := "1" + strings.Repeat("0", 400)
	_, err := svc.AddLine(ctx, tenant, p.ID, AddLineRequest{ProductID: "prod-1", QtyEntered: overflow, UOM: "EA"})
	if err == nil {
		t.Fatalf("overflow qty: want bad-request, got nil")
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("overflow qty: want bad-request, got %v", err)
	}
	if strings.Contains(err.Error(), "greater than zero") {
		t.Fatalf("overflow qty: misleading >0 message, want out-of-range: %v", err)
	}
}

// TestNotFoundMapping_InvalidUUID pins the repository error mapping: a malformed-UUID path id
// (Postgres 22P02) and a no-rows miss both fold into ErrNotFound, so an id probe gets a 404
// rather than a 500 leaking the cast error (review #9).
func TestNotFoundMapping_InvalidUUID(t *testing.T) {
	invalidUUID := &pgconn.PgError{Code: "22P02", Message: `invalid input syntax for type uuid: "not-a-uuid"`}
	if !isInvalidUUID(invalidUUID) {
		t.Fatalf("isInvalidUUID(22P02) = false, want true")
	}
	if !notFound(invalidUUID) {
		t.Fatalf("notFound(22P02) = false, want true")
	}
	if !notFound(pgx.ErrNoRows) {
		t.Fatalf("notFound(ErrNoRows) = false, want true")
	}
	// An unrelated error must NOT be swallowed as not-found (else real 500s vanish).
	other := &pgconn.PgError{Code: "23505", Message: "unique_violation"}
	if isInvalidUUID(other) || notFound(other) {
		t.Fatalf("notFound(23505) = true, want false")
	}
	if notFound(errors.New("boom")) {
		t.Fatalf("notFound(plain error) = true, want false")
	}
}
