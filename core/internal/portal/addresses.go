package portal

// addresses.go, GET /api/portal/v1/addresses[/{id}] (DIB-479, spec §2.2 gap 4).
//
// WHAT A "DELIVERY SITE" IS IN THIS ERP. There is no customer_addresses table, and this ticket does
// not invent one: a Stage-1 read-only gap is the wrong moment to add a master-data table that
// nobody writes to yet. The account's sites are ALREADY in the database, in three places, each
// answering a different question:
//
//	billing      customers.billing_address  , the address on the account itself.
//	job_site     projects.job_address       , the CP-03 jobs board's sites (planned work).
//	delivery     delivery_stops.address_*   , sites this account's orders were ACTUALLY delivered
//	                                           to (the ground truth: somebody drove there).
//
// Unioning the three is honest, a contractor picking "where does this go?" wants the site they
// used last month AND the job they set up this morning, and `kind` keeps the provenance visible
// rather than pretending one flat list of addresses was curated somewhere. The alternative (invent
// a table, backfill it, keep it in sync with three writers) is a migration and a sync problem in
// exchange for no new information.
//
// EVERY QUERY CARRIES customer_id. Not "the handler checks first", the tenant id is a WHERE clause
// parameter in the SQL itself (the projects.go convention), so a cross-tenant id cannot be answered
// even by a caller who guesses one: it matches no row and returns 404, never 403 (spec §2.4 rule 2 -
// a 403 confirms the row exists, which is the whole thing IDOR-safety is trying not to say).
//
// NOTHING PRICED, NOTHING INTERNAL. An address row carries no money, no branch assignment, no
// dispatch state, no driver, no other customer's geography, the fields below are the ones a person
// needs to recognise a place they have been.

import (
	"context"
	"errors"
	"net/http"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// Address kinds. Stable strings, HH Pro keys its site picker's grouping off them.
const (
	AddressKindBilling  = "billing"
	AddressKindJobSite  = "job_site"
	AddressKindDelivery = "delivery"
)

// Address is one delivery site of the account, projected for the contractor.
//
// ID is SYNTHETIC and namespaced by kind ("project:<uuid>", "billing:<uuid>", "site:<uuid>"),
// because these rows come from three tables whose ids could otherwise collide in one list. It is a
// stable handle: the same site yields the same id across calls, so a client may store it against a
// draft order and resolve it later through GET /addresses/{id}.
type Address struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Line1 string `json:"line1"`
	Line2 string `json:"line2"`
	City  string `json:"city"`
	// Province/Postal are the Canadian spellings the rest of this ERP uses (internal/localization).
	// The HH Pro adapter maps them onto its own Address.state/zip, a naming translation that
	// belongs in the client, not a second vocabulary in the server.
	Province string `json:"province"`
	Postal   string `json:"postal"`
	// LastUsedAt is the most recent delivery to this site (delivery kind only); nil elsewhere. It
	// is what lets a picker put "the one you used on Tuesday" at the top instead of alphabetising.
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// ErrAddressNotFound is returned when an address id does not exist OR belongs to another customer -
// never distinguished. The handler maps it to 404.
var ErrAddressNotFound = errors.New("portal: address not found")

// errAddressStoreNotConfigured means the wired projects store predates this read (a unit-test fake
// implementing only projectStore). A wiring state, not a user error, so the route can answer "not
// available here" rather than pretending the account has no sites.
var errAddressStoreNotConfigured = errors.New("portal: address store not configured")

// addressStore is the persistence the addresses read needs.
//
// A SEPARATE optional interface asserted onto the projects store rather than a widened projectStore,
// following the same convention inventory's stockLookupStore documents: projectStore is implemented
// by fakes in this package's tests, and widening a seam other people implement is a breaking change
// dressed up as an addition. *projectRepository implements both, so every real deployment takes the
// real path with no composition-root change.
type addressStore interface {
	ListAddresses(ctx context.Context, customerID string) ([]Address, error)
	GetAddress(ctx context.Context, customerID, addressID string) (Address, error)
}

// addresses resolves the optional store off whatever ProjectsService is holding.
func (s *ProjectsService) addresses() (addressStore, error) {
	as, ok := s.store.(addressStore)
	if !ok {
		return nil, errAddressStoreNotConfigured
	}
	return as, nil
}

// ListAddresses returns the account's delivery sites. Tenant id comes from the caller's JWT and is
// pushed into the SQL; there is no request-supplied customer parameter anywhere on this path.
func (s *ProjectsService) ListAddresses(ctx context.Context, customerID string) ([]Address, error) {
	if customerID == "" {
		return nil, httpx.BadRequestf("customer id is required")
	}
	as, err := s.addresses()
	if err != nil {
		return nil, err
	}
	return as.ListAddresses(ctx, customerID)
}

// GetAddress resolves ONE site of the account. A cross-tenant or unknown id is the same answer -
// ErrAddressNotFound → 404, because "that exists but is not yours" is a sentence a server should
// never say.
func (s *ProjectsService) GetAddress(ctx context.Context, customerID, addressID string) (Address, error) {
	if customerID == "" {
		return Address{}, httpx.BadRequestf("customer id is required")
	}
	if addressID == "" {
		return Address{}, httpx.BadRequestf("address id is required")
	}
	as, err := s.addresses()
	if err != nil {
		return Address{}, err
	}
	return as.GetAddress(ctx, customerID, addressID)
}

// -- routes -------------------------------------------------------------------

// registerAddressRoutes wires the delivery-site reads.
//
// GATE: ViewOrdersDeliveries. A site list is delivery information, not financial information, so
// field_crew, the people who actually stand at the site, can read it, while the gate still exists
// and is explicit. (h.authn would have been laxer and h.billing would have locked the crew out of
// the one thing they need.)
func (h *Handler) registerAddressRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/portal/v1/addresses", g.RequireCapability(identity.CapViewOrdersDeliveries, h.tenant(h.listAddresses)))
	r.Handle("GET /api/portal/v1/addresses/{id}", g.RequireCapability(identity.CapViewOrdersDeliveries, h.tenant(h.getAddress)))
}

// AddressListResponse is the list envelope ({"addresses": [...]}), matching the shape the other
// portal list routes use so the client has one thing to unwrap.
type AddressListResponse struct {
	Addresses []Address `json:"addresses"`
}

func (h *Handler) listAddresses(w http.ResponseWriter, r *http.Request) {
	if h.projectsSvc == nil {
		httpx.NotImplemented(w, "addresses not implemented")
		return
	}
	items, err := h.projectsSvc.ListAddresses(r.Context(), callerFrom(r.Context()).CustomerID)
	if err != nil {
		h.writeAddressErr(w, r, err)
		return
	}
	if items == nil {
		items = []Address{}
	}
	httpx.WriteJSON(w, http.StatusOK, AddressListResponse{Addresses: items})
}

func (h *Handler) getAddress(w http.ResponseWriter, r *http.Request) {
	if h.projectsSvc == nil {
		httpx.NotImplemented(w, "addresses not implemented")
		return
	}
	a, err := h.projectsSvc.GetAddress(r.Context(), callerFrom(r.Context()).CustomerID, r.PathValue("id"))
	if err != nil {
		h.writeAddressErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, a)
}

// writeAddressErr maps the addresses errors: not-found (INCLUDING cross-tenant) → 404; a malformed
// id → 404 as well (an id that cannot name a row is a miss, not a server complaint); client-input →
// 400; everything else → a generic 500 with the real error logged.
func (h *Handler) writeAddressErr(w http.ResponseWriter, r *http.Request, err error) {
	var bre httpx.BadRequestError
	switch {
	case errors.Is(err, ErrAddressNotFound):
		httpx.Error(w, http.StatusNotFound, "not found")
	case errors.Is(err, errAddressStoreNotConfigured):
		httpx.NotImplemented(w, "addresses not implemented")
	case errors.As(err, &bre):
		httpx.Error(w, http.StatusBadRequest, bre.Error())
	default:
		h.log.Error("portal: addresses error", "method", r.Method, "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal error")
	}
}
