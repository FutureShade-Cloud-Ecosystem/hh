package portal

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// projects.go, CP-03 Customer-Portal Projects/Jobs rollup (scope reconciliation 2026-07-14,
// A-3). The shipped "Projects" is the procurement/RFQ board (internal/procurement, a legit,
// separate feature that stays). CP-03's FEATURE_BRIEF specced a DIFFERENT view: a contractor's
// activity ROLLED UP BY JOB, per project, its orders + their invoices (spend) + scheduled
// deliveries + a quoted-vs-actual variance. This is that rollup, tenant-scoped by the portal
// JWT's customer_id (IDOR-safe: a contractor sees ONLY their own projects; another customer's
// project id → 404, mirroring the orders.go scoping).
//
// Rollup path (the FEATURE_BRIEF's A3 constraint: invoices/deliveries link to a project only
// THROUGH their order): projects → orders.project_id → invoices/manifests/quotes.
//
// Money rules enforced in the SQL below (do not relax without a spec change):
//   - spendStatusFilter: spend counts ONLY money-bearing invoices. VOID (reversed) and DRAFT
//     (not yet a receivable) invoices are excluded from both the list/dashboard spend rollup
//     AND the invoice drill list, so the drill total always reconciles with spend.
//   - the quoted-vs-actual variance dedups quotes: orders.quote_id has no unique constraint,
//     so a single quote can back multiple orders on a project; a naïve quotes⋈orders JOIN
//     would SUM that quote once per order (fan-out double-count). We sum each DISTINCT quote
//     once via a `q.id IN (SELECT DISTINCT o.quote_id …)` scalar subquery.
//   - the variance is quoted-vs-quoted (LIKE-FOR-LIKE), NOT total-spend-vs-quoted: its spend
//     leg (QuotedSpendCents) sums invoices for ONLY quote-originated orders (o.quote_id IS NOT
//     NULL), so an ad-hoc unquoted order, which has no quote to compare against, inflates
//     the SpendCents headline but can never manufacture a false overrun in VarianceCents.
//     SpendCents stays the all-orders total; QuotedSpendCents is surfaced separately.

// spendStatusFilter is the invoice-status predicate for every money rollup + drill: only
// FINALIZED/PARTIALLY_PAID/PAID invoices bear real receivable money. DRAFT and VOID are excluded.
const spendStatusFilter = `i.status IN ('FINALIZED','PARTIALLY_PAID','PAID')`

// ProjectSummary is one project's list-row rollup.
type ProjectSummary struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	JobAddress    string `json:"job_address"`
	OrderCount    int    `json:"order_count"`
	SpendCents    int64  `json:"spend_cents"`    // Σ invoice.total_cents for the project's orders' invoices
	DeliveryCount int    `json:"delivery_count"` // scheduled/dispatched manifests touching the project's orders
	// CreatedAt is a timestamptz scanned into time.Time so it JSON-marshals as RFC3339 (matching
	// order.Order.CreatedAt and portal User/Invite) rather than Postgres ::text's space-separated form.
	CreatedAt time.Time `json:"created_at"`
}

// ProjectDashboardDTO is the full per-project rollup returned by GetProjectDashboard for one
// project: the summary plus the quoted-vs-actual variance and the order/invoice drill lists.
type ProjectDashboardDTO struct {
	ProjectSummary
	QuotedCents int64 `json:"quoted_cents"` // Σ quote.total_cents for the project's quote-originated orders
	// QuotedSpendCents is the spend leg that is LIKE-FOR-LIKE with QuotedCents: Σ invoice
	// totals for ONLY the invoices whose order was quote-originated (order.quote_id IS NOT
	// NULL). It is ≤ SpendCents; the difference is spend on ad-hoc (unquoted) orders.
	QuotedSpendCents int64            `json:"quoted_spend_cents"`
	VarianceCents    int64            `json:"variance_cents"` // quoted-spend − quoted (only meaningful when QuotedCents > 0)
	HasQuote         bool             `json:"has_quote"`
	Orders           []ProjectOrder   `json:"orders"`
	Invoices         []ProjectInvoice `json:"invoices"`
}

// ProjectOrder is an order drill row within a project.
type ProjectOrder struct {
	ID         string `json:"id"`
	OrderNo    string `json:"order_no"`
	Status     string `json:"status"`
	TotalCents int64  `json:"total_cents"`
}

// ProjectInvoice is an invoice drill row within a project.
type ProjectInvoice struct {
	ID           string `json:"id"`
	InvoiceNo    string `json:"invoice_no"`
	Status       string `json:"status"`
	TotalCents   int64  `json:"total_cents"`
	BalanceCents int64  `json:"balance_cents"`
}

// projectStore is the persistence the ProjectsService needs. Real impl: projectRepository
// (pgx); tests use a fake. EVERY method takes customerID and scopes on it, the tenant fence
// lives in the SQL, not in the handler, so a bug can never widen the scope.
type projectStore interface {
	ListProjects(ctx context.Context, customerID string) ([]ProjectSummary, error)
	GetProjectDashboard(ctx context.Context, customerID, projectID string) (ProjectDashboardDTO, error)
}

// ProjectsService is the CP-03 rollup seam the portal BFF composes. It owns no state; it just
// resolves the tenant-scoped rollups.
type ProjectsService struct {
	store projectStore
}

// NewProjectsService wires the store.
func NewProjectsService(store projectStore) *ProjectsService {
	return &ProjectsService{store: store}
}

// GetProjectDashboard returns every project of a customer with its rollup summary (the list
// view). The name mirrors the FEATURE_BRIEF's specced service method.
func (s *ProjectsService) GetProjectDashboard(ctx context.Context, customerID string) ([]ProjectSummary, error) {
	if customerID == "" {
		return nil, httpx.BadRequestf("customer id is required")
	}
	return s.store.ListProjects(ctx, customerID)
}

// GetProject returns one project's full dashboard (summary + variance + drill lists), scoped
// to the tenant. A missing or cross-tenant project id → ErrProjectNotFound (mapped to 404).
func (s *ProjectsService) GetProject(ctx context.Context, customerID, projectID string) (ProjectDashboardDTO, error) {
	if customerID == "" {
		return ProjectDashboardDTO{}, httpx.BadRequestf("customer id is required")
	}
	if projectID == "" {
		return ProjectDashboardDTO{}, httpx.BadRequestf("project id is required")
	}
	return s.store.GetProjectDashboard(ctx, customerID, projectID)
}

// ErrProjectNotFound is returned when a project does not exist OR belongs to another customer
// (never distinguished, IDOR-safe). The handler maps it to 404.
var ErrProjectNotFound = errors.New("portal: project not found")

// -- pgx store ----------------------------------------------------------------

// projectRepository is the pgx projectStore. It reads projects + rolls up through
// orders.project_id → invoices / manifests / quotes. Every query carries customer_id in the
// WHERE so the tenant fence is enforced in SQL.
type projectRepository struct {
	pool *pgxpool.Pool
}

// NewProjectRepository wires the pool.
func NewProjectRepository(pool *pgxpool.Pool) *projectRepository {
	return &projectRepository{pool: pool}
}

// ListProjects returns each of the customer's projects with its rolled-up order count, spend
// (Σ invoice totals via order), and delivery count (manifests touching the project's orders).
func (r *projectRepository) ListProjects(ctx context.Context, customerID string) ([]ProjectSummary, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	// Subqueries roll up through orders.project_id. spend sums invoice totals for invoices
	// whose order belongs to the project; delivery_count counts DISTINCT manifests touching
	// those orders that represent a REAL delivery (scheduled/dispatched, see below). All
	// scoped by p.customer_id = $1.
	const q = `
		SELECT p.id::text, p.name, p.job_address, p.created_at,
		       COALESCE((SELECT COUNT(*) FROM orders o WHERE o.project_id = p.id), 0),
		       COALESCE((SELECT SUM(i.total_cents) FROM invoices i
		                 JOIN orders o ON o.id = i.order_id
		                 WHERE o.project_id = p.id AND ` + spendStatusFilter + `), 0),
		       -- DeliveryCount: DISTINCT manifests touching the project's orders whose status means a
		       -- real delivery, scheduled (FINAL) or dispatched/delivered (DISPATCHED/COMPLETED).
		       -- DRAFT (not yet a delivery) and CANCELLED (never happened) are excluded, per the DTO
		       -- contract ('scheduled/dispatched manifests').
		       COALESCE((SELECT COUNT(DISTINCT ml.manifest_id) FROM manifest_lines ml
		                 JOIN orders o ON o.id = ml.order_id
		                 JOIN manifests m ON m.id = ml.manifest_id
		                 WHERE o.project_id = p.id
		                   AND m.status IN ('FINAL','DISPATCHED','COMPLETED')), 0)
		FROM projects p
		WHERE p.customer_id = $1
		ORDER BY p.created_at DESC, p.name`
	rows, err := r.pool.Query(ctx, q, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectSummary
	for rows.Next() {
		var s ProjectSummary
		if err := rows.Scan(&s.ID, &s.Name, &s.JobAddress, &s.CreatedAt,
			&s.OrderCount, &s.SpendCents, &s.DeliveryCount); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetProjectDashboard loads one project (tenant-scoped) with its summary rollup, the
// quoted-vs-actual variance, and the order + invoice drill lists. The project header read
// carries customer_id in the WHERE, so a cross-tenant id returns ErrProjectNotFound (404) -
// never another customer's data.
func (r *projectRepository) GetProjectDashboard(ctx context.Context, customerID, projectID string) (ProjectDashboardDTO, error) {
	if r.pool == nil {
		return ProjectDashboardDTO{}, errNoDB
	}
	var d ProjectDashboardDTO
	err := r.pool.QueryRow(ctx, `
		-- proj_shares: for every quote that backs an order on THIS project ($1), enumerate the
		-- per-project order-total shares of that quote across ALL projects it touches, with a
		-- deterministic largest-remainder rank (rnk) used to distribute the residual cents. The
		-- QuotedCents column below reads this CTE; nothing else depends on it.
		WITH quote_project_orders AS (
		    -- (quote, project) → Σ that project's order totals on the quote, for every quote that
		    -- touches $1 and every project that quote spans.
		    SELECT o.quote_id, o.project_id, SUM(o.total_cents) AS proj_orders
		    FROM orders o
		    WHERE o.quote_id IS NOT NULL AND o.project_id IS NOT NULL
		      AND o.quote_id IN (SELECT DISTINCT o2.quote_id FROM orders o2
		                         WHERE o2.project_id = $1 AND o2.quote_id IS NOT NULL)
		    GROUP BY o.quote_id, o.project_id
		),
		quote_shares AS (
		    -- add each quote's total + the quote-wide Σ of order totals (all_orders) as plain
		    -- columns, so the remainder rank below can be computed without nesting a window
		    -- function inside another window's ORDER BY (Postgres forbids that).
		    SELECT qpo.quote_id, qpo.project_id, qpo.proj_orders,
		           qt.total_cents AS quote_total,
		           SUM(qpo.proj_orders) OVER (PARTITION BY qpo.quote_id) AS all_orders
		    FROM quote_project_orders qpo
		    JOIN quotes qt ON qt.id = qpo.quote_id
		),
		proj_shares AS (
		    SELECT qs.quote_id, qs.project_id, qs.proj_orders, qs.all_orders,
		           -- rank by fractional remainder of the proportional share (largest first),
		           -- tie-broken by project_id so allocation is fully deterministic.
		           ROW_NUMBER() OVER (
		               PARTITION BY qs.quote_id
		               ORDER BY (qs.quote_total::numeric * qs.proj_orders / NULLIF(qs.all_orders, 0))
		                        - FLOOR(qs.quote_total::numeric * qs.proj_orders / NULLIF(qs.all_orders, 0)) DESC,
		                        qs.project_id
		           ) AS rnk
		    FROM quote_shares qs
		)
		SELECT p.id::text, p.name, p.job_address, p.created_at,
		       COALESCE((SELECT COUNT(*) FROM orders o WHERE o.project_id = p.id), 0),
		       -- SpendCents: total spend headline, Σ money-bearing invoices for ALL the
		       -- project's orders (quoted AND ad-hoc). This is the spend number shown to the
		       -- contractor and it must NOT be quote-filtered.
		       COALESCE((SELECT SUM(i.total_cents) FROM invoices i
		                 JOIN orders o ON o.id = i.order_id
		                 WHERE o.project_id = p.id AND `+spendStatusFilter+`), 0),
		       -- DeliveryCount: DISTINCT manifests touching the project's orders that represent a
		       -- REAL delivery, scheduled (FINAL) or dispatched/delivered (DISPATCHED/COMPLETED).
		       -- DRAFT (not yet a delivery) and CANCELLED (never happened) are excluded, per the
		       -- DTO contract ('scheduled/dispatched manifests').
		       COALESCE((SELECT COUNT(DISTINCT ml.manifest_id) FROM manifest_lines ml
		                 JOIN orders o ON o.id = ml.order_id
		                 JOIN manifests m ON m.id = ml.manifest_id
		                 WHERE o.project_id = p.id
		                   AND m.status IN ('FINAL','DISPATCHED','COMPLETED')), 0),
		       -- QuotedCents: Σ quote.total_cents for the project's quote-originated orders, dedup'd so
		       -- a quote backing MULTIPLE orders on this project counts ONCE (orders.quote_id has no
		       -- UNIQUE), AND scoped so a quote whose orders span TWO projects (orders.project_id is
		       -- nullable / freely set) is never double-attributed: each project gets only the SHARE of
		       -- the quote total proportional to that project's orders' totals within the quote. For a
		       -- single-project quote this is the whole total (this project holds all the quote's
		       -- orders); summed across the projects a quote touches it reconstructs the quote total
		       -- exactly once, mirroring how QuotedSpendCents only ever sums THIS project's invoice legs.
		       --
		       -- Residual reconciliation (largest-remainder): a naïve ROUND() of each project's
		       -- proportional share drifts off the quote total when the split is non-divisible (e.g.
		       -- 100c split 1:1:1 → three ROUND(33.33)=33 shares summing to 99, losing 1c). We instead
		       -- FLOOR each project's share and distribute the leftover cents (quote.total − Σ floors)
		       -- one-per-project to the projects with the largest fractional remainders (deterministic
		       -- tie-break on project id). Summed across the projects a quote touches, the per-project
		       -- shares then sum EXACTLY to the quote total.
		       COALESCE((SELECT SUM(share.alloc) FROM (
		           SELECT
		             -- floor of THIS project's proportional share …
		             FLOOR(q.total_cents::numeric * pp.proj_orders / NULLIF(pp.all_orders, 0))
		             -- … plus 1c iff this project is among the top-(leftover) projects of the quote by
		             -- fractional remainder. rnk is 1-based rank of THIS project among the quote's
		             -- projects (largest remainder first, tie-break on project id); leftover is the
		             -- number of cents still to distribute after flooring every project.
		             + CASE WHEN pp.rnk <= (
		                   q.total_cents
		                     - (SELECT COALESCE(SUM(FLOOR(q.total_cents::numeric * a.proj_orders
		                                                    / NULLIF(a.all_orders, 0))), 0)
		                        FROM proj_shares a WHERE a.quote_id = q.id)
		               ) THEN 1 ELSE 0 END AS alloc
		           FROM quotes q
		           JOIN proj_shares pp ON pp.quote_id = q.id AND pp.project_id = p.id
		       ) share), 0),
		       -- QuotedSpendCents: the LIKE-FOR-LIKE spend leg for the variance, Σ money-bearing
		       -- invoices for ONLY the quote-originated orders (o.quote_id IS NOT NULL). Comparing
		       -- THIS against QuotedCents (not the all-orders SpendCents) is what keeps an ad-hoc
		       -- unquoted order from inflating the variance into a false overrun.
		       COALESCE((SELECT SUM(i.total_cents) FROM invoices i
		                 JOIN orders o ON o.id = i.order_id
		                 WHERE o.project_id = p.id AND o.quote_id IS NOT NULL
		                   AND `+spendStatusFilter+`), 0)
		FROM projects p
		WHERE p.id = $1 AND p.customer_id = $2`, projectID, customerID).
		Scan(&d.ID, &d.Name, &d.JobAddress, &d.CreatedAt,
			&d.OrderCount, &d.SpendCents, &d.DeliveryCount, &d.QuotedCents, &d.QuotedSpendCents)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProjectDashboardDTO{}, ErrProjectNotFound
		}
		return ProjectDashboardDTO{}, err
	}
	d.HasQuote = d.QuotedCents > 0
	if d.HasQuote {
		// Variance is quoted-vs-quoted: like-for-like spend on the quoted work minus the
		// quoted amount. Ad-hoc (unquoted) order spend lives in SpendCents only and never
		// touches the variance.
		d.VarianceCents = d.QuotedSpendCents - d.QuotedCents
	}

	// Order drill list (scoped to the project; the project header was already tenant-checked).
	oRows, err := r.pool.Query(ctx, `
		SELECT o.id::text, o.order_no::text, o.status, o.total_cents
		FROM orders o WHERE o.project_id = $1 ORDER BY o.created_at DESC, o.order_no`, projectID)
	if err != nil {
		return ProjectDashboardDTO{}, err
	}
	defer oRows.Close()
	d.Orders = []ProjectOrder{}
	for oRows.Next() {
		var o ProjectOrder
		if err := oRows.Scan(&o.ID, &o.OrderNo, &o.Status, &o.TotalCents); err != nil {
			return ProjectDashboardDTO{}, err
		}
		d.Orders = append(d.Orders, o)
	}
	if err := oRows.Err(); err != nil {
		return ProjectDashboardDTO{}, err
	}

	// Invoice drill list (via order → project). Restricted to the SAME money-bearing statuses
	// as the spend rollup so the drill total always reconciles with SpendCents (DRAFT/VOID
	// excluded).
	iRows, err := r.pool.Query(ctx, `
		SELECT i.id::text, i.invoice_no::text, i.status, i.total_cents, i.balance_cents
		FROM invoices i JOIN orders o ON o.id = i.order_id
		WHERE o.project_id = $1 AND `+spendStatusFilter+`
		ORDER BY i.issue_date DESC, i.invoice_no`, projectID)
	if err != nil {
		return ProjectDashboardDTO{}, err
	}
	defer iRows.Close()
	d.Invoices = []ProjectInvoice{}
	for iRows.Next() {
		var iv ProjectInvoice
		if err := iRows.Scan(&iv.ID, &iv.InvoiceNo, &iv.Status, &iv.TotalCents, &iv.BalanceCents); err != nil {
			return ProjectDashboardDTO{}, err
		}
		d.Invoices = append(d.Invoices, iv)
	}
	return d, iRows.Err()
}

// -- handler routes (BFF) -------------------------------------------------------

// registerProjectRoutes wires the CP-03 rollup reads. Both require ViewBilling (per-project
// SPEND is financial and follows the dashboard's role gate, FEATURE_BRIEF A5).
func (h *Handler) registerProjectRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/portal/v1/projects", g.RequireCapability(identity.CapViewBilling, h.tenant(h.listProjectRollup)))
	r.Handle("GET /api/portal/v1/projects/{id}", g.RequireCapability(identity.CapViewBilling, h.tenant(h.getProjectRollup)))
}

// listProjectRollup returns the tenant's projects with their spend rollup.
func (h *Handler) listProjectRollup(w http.ResponseWriter, r *http.Request) {
	if h.projectsSvc == nil {
		httpx.NotImplemented(w, "projects rollup not implemented")
		return
	}
	caller := callerFrom(r.Context())
	items, err := h.projectsSvc.GetProjectDashboard(r.Context(), caller.CustomerID)
	if err != nil {
		h.writeProjectErr(w, r, err)
		return
	}
	if items == nil {
		items = []ProjectSummary{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"projects": items})
}

// getProjectRollup returns one project's full dashboard, tenant-scoped (cross-tenant → 404).
func (h *Handler) getProjectRollup(w http.ResponseWriter, r *http.Request) {
	if h.projectsSvc == nil {
		httpx.NotImplemented(w, "projects rollup not implemented")
		return
	}
	caller := callerFrom(r.Context())
	d, err := h.projectsSvc.GetProject(r.Context(), caller.CustomerID, r.PathValue("id"))
	if err != nil {
		h.writeProjectErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

// writeProjectErr maps the rollup errors: not-found (incl. cross-tenant) → 404; client-input
// → 400; else a generic 500 logged. Kept local so it doesn't have to be threaded into the big
// shared writeErr switch.
func (h *Handler) writeProjectErr(w http.ResponseWriter, r *http.Request, err error) {
	var bre httpx.BadRequestError
	switch {
	case errors.Is(err, ErrProjectNotFound):
		httpx.Error(w, http.StatusNotFound, "not found")
	case errors.As(err, &bre):
		httpx.Error(w, http.StatusBadRequest, bre.Error())
	default:
		h.log.Error("portal: projects rollup error", "method", r.Method, "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal error")
	}
}
