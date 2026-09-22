package procurement

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
)

var (
	// ErrNotFound -- project/line missing or owned by another account (IDOR-safe 404).
	ErrNotFound = errors.New("procurement: not found")
	// ErrInvalidTransition -- the project is not in a state from which this action is
	// legal (e.g. deleting a non-PLANNING project, recalling a PLANNING one, or a
	// reserved QUOTING->ORDERED / ORDERED->FULFILLED user move). Maps to HTTP 409.
	ErrInvalidTransition = errors.New("procurement: invalid state transition")
	// ErrNotImplemented -- a scaffolded store method with no behavior yet. Maps to HTTP 501
	// so a parallel implementer sees exactly which seams are still stubs (plan honesty rule).
	ErrNotImplemented = errors.New("procurement: not implemented")
)

// provisionalCostCents is the cost basis fed to the pricing waterfall at RFQ time. There is
// no ERP cost source yet, so it is provisional (0) and EVERY derived price is INDICATIVE,
// never authoritative (plan §4.4 review correction #2). The swarm wires the real cost seam.
const provisionalCostCents int64 = 0

// store is the persistence the Service needs. Real impl: Repository (pgx); tests use a fake.
// Every operation is tenant-scoped by customerID (cross-tenant id -> ErrNotFound).
type store interface {
	CreateProject(ctx context.Context, customerID, createdByUserID, name string) (Project, error)
	ListProjects(ctx context.Context, customerID string) ([]Project, error)
	GetProject(ctx context.Context, customerID, projectID string) (ProjectDetail, error)
	// UpdateProject applies a rename and/or a lane move. name/status are nil when omitted;
	// a status change is enforced atomically against the legal source state (canTransition).
	UpdateProject(ctx context.Context, customerID, projectID string, name, status *string) (Project, error)
	// DeleteProject removes a project only when it is still in PLANNING (else ErrInvalidTransition).
	DeleteProject(ctx context.Context, customerID, projectID string) error
	AddLine(ctx context.Context, customerID, projectID string, in AddLineRequest) (Line, error)
	UpdateLine(ctx context.Context, customerID, projectID, lineID string, in UpdateLineRequest) (Line, error)
	RemoveLine(ctx context.Context, customerID, projectID, lineID string) error
	// SubmitRFQ persists the indicative prices the service computed and transitions the
	// project PLANNING -> QUOTING, returning the repriced detail.
	SubmitRFQ(ctx context.Context, customerID, projectID string, priced []PricedLine) (ProjectDetail, error)
}

// priceDeriver is the pricing seam: the one in-process pricing method SubmitRFQ composes
// (pricing.Service.DeriveLinePrice). Injected by main.go so procurement never reaches into
// the pricing module's resolver/store internals.
type priceDeriver interface {
	DeriveLinePrice(ctx context.Context, in pricing.ResolveInput) (pricing.LinePrice, error)
}

// accountResolver is the customers read seam: SubmitRFQ needs the tenant's customer_type +
// home branch to build account-based pricing input (customer.Service.ResolveAccount).
type accountResolver interface {
	ResolveAccount(ctx context.Context, customerID string) (customer.Account, error)
}

// categoryResolver is the catalog read seam: the pricing waterfall keys on the product's
// category, so SubmitRFQ resolves it per line (product.Service.GetCategory).
type categoryResolver interface {
	GetCategory(ctx context.Context, productID string) (string, error)
}

// Service is the Planning-board seam the portal BFF composes in-process. Persistence is the
// store; the pricing/account/catalog seams are injected via the With* builders (nil until
// wired, which makes SubmitRFQ answer 501).
type Service struct {
	store    store
	pricing  priceDeriver
	accounts accountResolver
	catalog  categoryResolver
}

func NewService(s store) *Service { return &Service{store: s} }

// WithPricing injects the pricing seam (pricing.Service.DeriveLinePrice).
func (s *Service) WithPricing(p priceDeriver) *Service { s.pricing = p; return s }

// WithAccounts injects the customers read seam (customer.Service.ResolveAccount).
func (s *Service) WithAccounts(a accountResolver) *Service { s.accounts = a; return s }

// WithCatalog injects the catalog category seam (product.Service.GetCategory).
func (s *Service) WithCatalog(c categoryResolver) *Service { s.catalog = c; return s }

// -- projects ------------------------------------------------------------------

// CreateProject opens a new PLANNING project for the tenant, attributed to the caller.
func (s *Service) CreateProject(ctx context.Context, customerID, createdByUserID, name string) (Project, error) {
	if customerID == "" {
		return Project{}, httpx.BadRequestf("customer id is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Project{}, httpx.BadRequestf("project name is required")
	}
	return s.store.CreateProject(ctx, customerID, createdByUserID, name)
}

// ListProjects returns the tenant's non-terminal projects (the board groups by status).
func (s *Service) ListProjects(ctx context.Context, customerID string) ([]Project, error) {
	if customerID == "" {
		return nil, httpx.BadRequestf("customer id is required")
	}
	return s.store.ListProjects(ctx, customerID)
}

// GetProject returns one project with its lines, scoped to the tenant.
func (s *Service) GetProject(ctx context.Context, customerID, projectID string) (ProjectDetail, error) {
	if customerID == "" || projectID == "" {
		return ProjectDetail{}, httpx.BadRequestf("customer id and project id are required")
	}
	return s.store.GetProject(ctx, customerID, projectID)
}

// UpdateProject renames and/or moves a project's lane. A move INTO QUOTING is rejected here:
// it must go through SubmitRFQ (which prices the lines). Other moves are validated against the
// legal source state atomically by the store (canTransition).
func (s *Service) UpdateProject(ctx context.Context, customerID, projectID string, name, status *string) (Project, error) {
	if customerID == "" || projectID == "" {
		return Project{}, httpx.BadRequestf("customer id and project id are required")
	}
	if name == nil && status == nil {
		return Project{}, httpx.BadRequestf("nothing to update")
	}
	if name != nil {
		// Trim the rename so persistence matches CreateProject (which trims); an
		// empty-after-trim name is a bad request, same as create (review #14).
		trimmed := strings.TrimSpace(*name)
		if trimmed == "" {
			return Project{}, httpx.BadRequestf("project name cannot be empty")
		}
		name = &trimmed
	}
	if status != nil {
		if !isValidStatus(*status) {
			return Project{}, httpx.BadRequestf("unknown project status %q", *status)
		}
		if *status == StatusQuoting {
			// PLANNING -> QUOTING prices the lines, so it is the submit-rfq path, not a bare
			// lane move. Reject a direct PUT into QUOTING (409).
			return Project{}, ErrInvalidTransition
		}
	}
	return s.store.UpdateProject(ctx, customerID, projectID, name, status)
}

// DeleteProject removes a project, allowed only while it is in PLANNING (the store enforces
// the source state and returns ErrInvalidTransition for any other lane).
func (s *Service) DeleteProject(ctx context.Context, customerID, projectID string) error {
	if customerID == "" || projectID == "" {
		return httpx.BadRequestf("customer id and project id are required")
	}
	return s.store.DeleteProject(ctx, customerID, projectID)
}

// -- lines ---------------------------------------------------------------------

// AddLine appends a catalog line to a project. The product/qty/uom are required.
func (s *Service) AddLine(ctx context.Context, customerID, projectID string, in AddLineRequest) (Line, error) {
	if customerID == "" || projectID == "" {
		return Line{}, httpx.BadRequestf("customer id and project id are required")
	}
	if in.ProductID == "" {
		return Line{}, httpx.BadRequestf("product_id is required")
	}
	if strings.TrimSpace(in.UOM) == "" {
		return Line{}, httpx.BadRequestf("uom is required")
	}
	if err := validateQty(in.QtyEntered); err != nil {
		return Line{}, err
	}
	// The PLANNING freeze is enforced ATOMICALLY in the store under a row lock (no
	// check-then-act GetProject round-trip): a racing SubmitRFQ cannot slip this line onto a
	// QUOTING project. The store returns ErrNotFound (404) / ErrInvalidTransition (409)
	// (reviews #3 + #6).
	return s.store.AddLine(ctx, customerID, projectID, in)
}

// UpdateLine edits qty/uom/notes on an existing line (partial update).
func (s *Service) UpdateLine(ctx context.Context, customerID, projectID, lineID string, in UpdateLineRequest) (Line, error) {
	if customerID == "" || projectID == "" || lineID == "" {
		return Line{}, httpx.BadRequestf("customer id, project id and line id are required")
	}
	if in.QtyEntered == nil && in.UOM == nil && in.Notes == nil {
		return Line{}, httpx.BadRequestf("nothing to update")
	}
	if in.QtyEntered != nil {
		if err := validateQty(*in.QtyEntered); err != nil {
			return Line{}, err
		}
	}
	if in.UOM != nil && strings.TrimSpace(*in.UOM) == "" {
		return Line{}, httpx.BadRequestf("uom cannot be empty")
	}
	// PLANNING freeze enforced atomically in the store under a row lock (reviews #3 + #6).
	return s.store.UpdateLine(ctx, customerID, projectID, lineID, in)
}

// RemoveLine deletes one line from a project.
func (s *Service) RemoveLine(ctx context.Context, customerID, projectID, lineID string) error {
	if customerID == "" || projectID == "" || lineID == "" {
		return httpx.BadRequestf("customer id, project id and line id are required")
	}
	// PLANNING freeze enforced atomically in the store under a row lock (reviews #3 + #6).
	return s.store.RemoveLine(ctx, customerID, projectID, lineID)
}

// SubmitRFQ transitions a PLANNING project to QUOTING, pricing every line INDICATIVELY via the
// pricing seam under the tenant's account context. Cost is provisional, so prices are flagged
// indicative (never authoritative). The store persists the prices + the lane move atomically.
func (s *Service) SubmitRFQ(ctx context.Context, customerID, projectID string) (ProjectDetail, error) {
	if customerID == "" || projectID == "" {
		return ProjectDetail{}, httpx.BadRequestf("customer id and project id are required")
	}
	if s.pricing == nil || s.accounts == nil || s.catalog == nil {
		// Pricing seams not injected yet (scaffold): the RFQ price loop cannot run.
		return ProjectDetail{}, ErrNotImplemented
	}

	detail, err := s.store.GetProject(ctx, customerID, projectID)
	if err != nil {
		return ProjectDetail{}, err
	}
	if !canTransition(detail.Status, StatusQuoting) {
		return ProjectDetail{}, ErrInvalidTransition
	}
	if len(detail.Lines) == 0 {
		// An empty material list has nothing to quote: reject before any lane move (review #7).
		return ProjectDetail{}, httpx.BadRequestf("project has no lines to quote")
	}
	acct, err := s.accounts.ResolveAccount(ctx, customerID)
	if err != nil {
		return ProjectDetail{}, err
	}

	// Resolve every line's category in ONE pass over the distinct product_ids before the
	// pricing loop -- no per-line GetCategory N+1 (review #15). Category resolution is
	// BEST-EFFORT, exactly like the pricing loop below: a GetCategory fault for one product
	// must NEVER fail the whole RFQ nor escape as a non-httpx 500 (review #2). On error the
	// category is treated as empty/unknown and that line simply ends up unpriced via the
	// graceful pricing path (an unanchored line the dealer prices back).
	categories := make(map[string]string, len(detail.Lines))
	for _, ln := range detail.Lines {
		if _, done := categories[ln.ProductID]; done {
			continue
		}
		categoryID, err := s.catalog.GetCategory(ctx, ln.ProductID)
		if err != nil {
			categoryID = "" // unknown category: continue best-effort, leave the line unpriced
		}
		categories[ln.ProductID] = categoryID
	}

	priced := make([]PricedLine, 0, len(detail.Lines))
	for _, ln := range detail.Lines {
		// Pricing is BEST-EFFORT/indicative: the cost basis is provisional (0), so a line
		// the waterfall cannot anchor (e.g. no list price) is left UNPRICED rather than
		// failing the whole RFQ. The dealer prices it back. A pricing fault must never
		// escape as a non-httpx 500 (reviews #1 + #6); the project still moves to QUOTING.
		pl := PricedLine{LineID: ln.ID} // unpriced default: 0 cents, priced=false
		if qty, qerr := strconv.ParseFloat(ln.QtyEntered, 64); qerr == nil {
			lp, derr := s.pricing.DeriveLinePrice(ctx, pricing.ResolveInput{
				CustomerID:   customerID,
				CustomerType: acct.CustomerType,
				ProductID:    ln.ProductID,
				CategoryID:   categories[ln.ProductID],
				BranchID:     acct.BranchID,
				QtyEntered:   qty,
				UOMEntered:   ln.UOM,
				CostCents:    provisionalCostCents,
			})
			if derr == nil {
				// Use the extended line total the resolver returns directly -- never
				// recompute money by multiplying unit cents by a binary float qty (review #4/#5).
				pl.IndicativeUnitPriceCents = lp.UnitPriceCents
				pl.IndicativeLineTotalCents = lp.LineTotalCents
				pl.Priced = true
			}
		}
		priced = append(priced, pl)
	}
	return s.store.SubmitRFQ(ctx, customerID, projectID, priced)
}

// -- state machine ---------------------------------------------------------------

// allowedTransitions is the user-driven state machine for THIS increment. PLANNING<->QUOTING
// (QUOTING via submit-rfq, PLANNING via recall) + Cancel. QUOTING->ORDERED and
// ORDERED->FULFILLED are reserved for the future dealer/order-conversion side: the enum
// defines them but they are NOT user-legal here, so canTransition rejects them (409).
var allowedTransitions = map[string][]string{
	StatusPlanning: {StatusQuoting, StatusCancelled},
	StatusQuoting:  {StatusPlanning},
}

// canTransition reports whether a user may move a project from -> to in this increment.
func canTransition(from, to string) bool {
	for _, t := range allowedTransitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// isValidStatus reports whether status is one of the five procurement_projects CHECK values.
func isValidStatus(status string) bool {
	switch status {
	case StatusPlanning, StatusQuoting, StatusOrdered, StatusFulfilled, StatusCancelled:
		return true
	default:
		return false
	}
}

// maxQtyLen bounds the entered-quantity string so an absurdly long magnitude (a megabyte of
// digits) can never reach strconv.ParseFloat / the numeric column. A real quantity is short.
const maxQtyLen = 64

// qtyPattern is a STRICT positive-decimal grammar: bare digits with an OPTIONAL trailing dot
// ("10", "10.") or a fractional form with an optional integer part ("0.5", ".5"). It accepts
// the trailing-dot ("10.") and bare-leading-dot (".5") decimals strconv.ParseFloat also
// accepts (review #5), while deliberately rejecting signs, exponents, hex, and the IEEE words
// NaN/Inf -- qty_entered is money-adjacent and must be an ordinary decimal (review #4/#5).
// A lone "." matches neither alternative, so it is rejected.
var qtyPattern = regexp.MustCompile(`^(?:\d+\.?|\d*\.\d+)$`)

// validateQty rejects a missing/non-decimal/non-positive/out-of-range entered quantity before
// it reaches the store (qty_entered is a DECIMAL-as-string on the wire). It accepts ONLY a
// plain decimal string with a finite value strictly greater than zero.
func validateQty(qty string) error {
	s := strings.TrimSpace(qty)
	if s == "" {
		return httpx.BadRequestf("qty_entered is required")
	}
	if len(s) > maxQtyLen {
		// Absurd magnitude (would overflow when parsed): out-of-range, not a >0 fault (review #7).
		return httpx.BadRequestf("qty_entered is out of range")
	}
	if !qtyPattern.MatchString(s) {
		return httpx.BadRequestf("qty_entered %q must be a positive decimal", qty)
	}
	q, err := strconv.ParseFloat(s, 64)
	if err != nil {
		// The pattern guarantees a finite decimal, so the only ParseFloat failure here is a
		// range/overflow error: report out-of-range rather than the misleading >0 message,
		// which is reserved for an actual non-positive value (review #7).
		if errors.Is(err, strconv.ErrRange) {
			return httpx.BadRequestf("qty_entered is out of range")
		}
		return httpx.BadRequestf("qty_entered %q must be a positive decimal", qty)
	}
	if q <= 0 {
		// An actual non-positive magnitude (0, 0.0, ...).
		return httpx.BadRequestf("qty_entered must be greater than zero")
	}
	return nil
}
