package portal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeProjectStore is an in-memory projectStore that models the tenant fence + a per-project
// spend rollup, so the service/handler tests exercise CP-03 without a DB.
type fakeProjectStore struct {
	// projects keyed by id; each carries its owner customerID and its rolled-up dashboard.
	projects map[string]struct {
		customerID string
		dash       ProjectDashboardDTO
	}
	gotListCustomer string
}

func (f *fakeProjectStore) ListProjects(_ context.Context, customerID string) ([]ProjectSummary, error) {
	f.gotListCustomer = customerID
	var out []ProjectSummary
	for _, p := range f.projects {
		if p.customerID == customerID {
			out = append(out, p.dash.ProjectSummary)
		}
	}
	return out, nil
}

func (f *fakeProjectStore) GetProjectDashboard(_ context.Context, customerID, projectID string) (ProjectDashboardDTO, error) {
	p, ok := f.projects[projectID]
	// The tenant fence: a project belonging to another customer is INDISTINGUISHABLE from a
	// missing one (IDOR-safe), both return ErrProjectNotFound.
	if !ok || p.customerID != customerID {
		return ProjectDashboardDTO{}, ErrProjectNotFound
	}
	return p.dash, nil
}

// newProjectStore seeds a store where tenant A owns "prj-a" with two invoices (spend = their
// sum) and a quote (variance = spend − quoted); tenant B owns "prj-b".
func newProjectStore() *fakeProjectStore {
	return &fakeProjectStore{
		projects: map[string]struct {
			customerID string
			dash       ProjectDashboardDTO
		}{
			"prj-a": {
				customerID: tenantA,
				dash: ProjectDashboardDTO{
					ProjectSummary: ProjectSummary{ID: "prj-a", Name: "Maple Glen Patio", OrderCount: 2, SpendCents: 30000, DeliveryCount: 1},
					QuotedCents:    28000,
					VarianceCents:  2000,
					HasQuote:       true,
					Invoices: []ProjectInvoice{
						{ID: "i1", InvoiceNo: "INV-1", TotalCents: 20000},
						{ID: "i2", InvoiceNo: "INV-2", TotalCents: 10000},
					},
				},
			},
			"prj-b": {
				customerID: tenantB,
				dash:       ProjectDashboardDTO{ProjectSummary: ProjectSummary{ID: "prj-b", Name: "Other Co Job", SpendCents: 99999}},
			},
		},
	}
}

// TestProjects_RollupMath, a project's spend equals the sum of its orders' invoices, and the
// variance = spend − quoted.
func TestProjects_RollupMath(t *testing.T) {
	svc := NewProjectsService(newProjectStore())
	d, err := svc.GetProject(context.Background(), tenantA, "prj-a")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	var invSum int64
	for _, iv := range d.Invoices {
		invSum += iv.TotalCents
	}
	if d.SpendCents != invSum {
		t.Fatalf("spend %d != sum of invoices %d", d.SpendCents, invSum)
	}
	if d.SpendCents != 30000 {
		t.Fatalf("spend = %d, want 30000", d.SpendCents)
	}
	if d.VarianceCents != d.SpendCents-d.QuotedCents {
		t.Fatalf("variance %d != spend-quoted %d", d.VarianceCents, d.SpendCents-d.QuotedCents)
	}
}

// TestProjects_IDOR, tenant A cannot read tenant B's project; it is a 404 (never B's data),
// at BOTH the service and the HTTP handler layer.
func TestProjects_IDOR(t *testing.T) {
	store := newProjectStore()
	svc := NewProjectsService(store)

	// Service layer: A asking for B's project → ErrProjectNotFound.
	if _, err := svc.GetProject(context.Background(), tenantA, "prj-b"); err != ErrProjectNotFound {
		t.Fatalf("cross-tenant GetProject err = %v, want ErrProjectNotFound", err)
	}

	// Handler layer: A's request for B's project id → HTTP 404, no B data leaked.
	h := &Handler{}
	h.projectsSvc = svc
	rec := httptest.NewRecorder()
	req := reqWithCaller(http.MethodGet, "/api/portal/v1/projects/prj-b", "", tenantA)
	req.SetPathValue("id", "prj-b")
	h.getProjectRollup(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant handler status = %d, want 404; body %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); strings.Contains(body, "Other Co Job") || strings.Contains(body, "99999") {
		t.Fatalf("cross-tenant response leaked tenant B data: %s", body)
	}
}

// TestProjects_ListScoped, the list rollup only returns the caller's projects.
func TestProjects_ListScoped(t *testing.T) {
	store := newProjectStore()
	svc := NewProjectsService(store)
	list, err := svc.GetProjectDashboard(context.Background(), tenantA)
	if err != nil {
		t.Fatalf("GetProjectDashboard: %v", err)
	}
	if store.gotListCustomer != tenantA {
		t.Fatalf("list not scoped by caller customer: got %q", store.gotListCustomer)
	}
	for _, p := range list {
		if p.ID == "prj-b" {
			t.Fatal("tenant A's list leaked tenant B's project")
		}
	}
	if len(list) != 1 || list[0].ID != "prj-a" {
		t.Fatalf("list = %+v, want just prj-a", list)
	}
}
