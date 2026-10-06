package product

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// Handler exposes the product module's REST surface (A-18 faceted catalogue + edit).
type Handler struct {
	userLink identity.UserIDLinker
	svc      *Service
	log      *slog.Logger
}

func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes self-registers on the shared mux (platform convention). The
// faceted reads are public catalogue data; the attribute WRITE is gated by the
// staff manager guard passed in from cmd/server (Tier≤2), so an anonymous caller
// can no longer mutate product attributes (C-1).
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	if g == nil {
		// Fail closed: a missing guard would silently expose the attribute write to
		// anonymous callers. Refuse to register rather than ship an open mutation.
		panic("product.RegisterRoutes: guards are required (the PUT must be auth-gated)")
	}
	r.Handle("GET /api/v1/products", http.HandlerFunc(h.list))
	r.Handle("GET /api/v1/products/facets", http.HandlerFunc(h.facets))
	r.Handle("GET /api/v1/products/{id}", http.HandlerFunc(h.get))
	r.Handle("PUT /api/v1/products/{id}", g.RequireManager(h.staff(h.update)))
	// The staff read of the unit catalogue (HH-LOAD-1 LD-3,
	// productListUnits): staff tier at read, the contract's x-access
	// annotation naming the dealer-admin surface, and the registrar
	// wraps the registration in the role gate the census carries.
	r.Handle("GET /api/v1/uoms", g.RequireStaff(h.staff(h.listUnits)))
}

// RegisterSearchRoute wires the STAFF fuzzy product search (DIB-56).
//
// The search itself is not new: Service.SearchProducts (pg_trgm similarity on name, plus an escaped SKU
// prefix) has existed since the portal catalogue picker and is route-agnostic. What was missing was a
// staff-authenticated door to it, the contractor portal had one at /api/portal/v1/catalog/search behind
// its own JWT guard, and staff had nothing. The consequence is visible in the DIB-175 order-edit UI,
// which makes a counter operator paste a raw product UUID and type the UOM by hand. The quote builder
// needs a real picker, so this exposes the existing service method rather than adding a second search.
//
// Registered separately from RegisterRoutes so the existing signature (and its callers) are untouched -
// the same three-registration-methods-on-one-handler shape the order module uses.
//
// Route precedence: the literal /products/search binds ahead of the /products/{id} wildcard above under
// Go's ServeMux specificity rules, so "search" is never parsed as a product id.
func (h *Handler) RegisterSearchRoute(r httpx.Registrar, g *guards.Guards) {
	if g == nil {
		panic("product: guards are required")
	}
	r.Handle("GET /api/v1/products/search", g.RequireStaff(h.staff(h.search)))
}

// RegisterCounterRoutes wires the W3 counter surface: the ranked instant search (GABLE:pos-search +
// search-synonyms), the barcode/SKU scan resolve (GABLE:pos-barcode) and the thumbnail routes
// (GABLE:product-images-ui).
//
// THE IMAGE BYTE ROUTE IS DELIBERATELY UNGATED, and that decision needs to be visible rather than
// discovered. Two reasons, both load-bearing:
//
//  1. An <img src> cannot carry an Authorization header. A staff-gated byte route could only be
//     consumed by fetching to a blob URL on every row of every list, which is a lot of machinery to
//     protect data we are already giving away, because
//  2. GET /api/v1/products and GET /api/v1/products/{id} are ALREADY public on this mux (see
//     RegisterRoutes). The catalogue's names, SKUs, collections and attributes are public data. A
//     thumbnail of a paver is not more sensitive than the paver's name and list price.
//
// The WRITE side is gated by writeGuard (manager), exactly like the attribute PUT. If the catalogue
// reads ever become authenticated, this route moves with them, it is registered here, in one place,
// for that reason.
func (h *Handler) RegisterCounterRoutes(r httpx.Registrar, g *guards.Guards) {
	if g == nil {
		panic("product: guards are required")
	}
	// Literal segments bind ahead of the /products/{id} wildcard under Go's ServeMux specificity
	// rules, so "counter-search" and "scan" are never parsed as product ids.
	r.Handle("GET /api/v1/products/counter-search", g.RequireStaff(h.staff(h.counterSearch)))
	r.Handle("GET /api/v1/products/scan", g.RequireStaff(h.staff(h.resolveScan)))
	// DIB-80 / S1 · the material calculator's basis read. Registered HERE, with the counter routes,
	// because it is the same surface and the same audience: a rep at the counter answering "how much
	// material for this job?". Staff-gated like the counter search rather than left public with the
	// catalogue reads, it exposes the yard's packaging and waste rules, which are operational data
	// about how Dibbits sells, not the product facts a customer already sees on a shelf tag.
	r.Handle("GET /api/v1/products/coverage", g.RequireStaff(h.staff(h.coverage)))
	r.Handle("GET /api/v1/products/{id}/image", http.HandlerFunc(h.getImage))
	r.Handle("PUT /api/v1/products/{id}/image", g.RequireManager(h.staff(h.putImage)))
	r.Handle("DELETE /api/v1/products/{id}/image", g.RequireManager(h.staff(h.deleteImage)))
}

// counterSearch serves the debounced instant search. Envelope matches the other catalogue searches.
func (h *Handler) counterSearch(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	q := r.URL.Query()
	limit := 0
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	items, err := h.svc.CounterSearch(r.Context(), CounterSearchQuery{
		Q:        strings.TrimSpace(q.Get("q")),
		BranchID: strings.TrimSpace(q.Get("branch_id")),
		Limit:    limit,
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if items == nil {
		items = []CounterHit{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// coverage backs GET /api/v1/products/coverage, the material calculator's basis rows (DIB-80).
//
// ?ids=<uuid,uuid,…> narrows to named products; no ids means every active product, which is the
// normal case (the calculator ranks the whole catalogue against one job).
//
// The envelope is {"items":[…]}, the same shape as counterSearch and search, so the frontend reads it
// with the house asRows loader and a body it does not recognise becomes an EMPTY calculator rather
// than an error banner (DIB-471). A products table with nothing in it answers 200 and [], never 500:
// "no products carry coverage data" is an answer.
func (h *Handler) coverage(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var ids []string
	if raw := strings.TrimSpace(r.URL.Query().Get("ids")); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			if p := strings.TrimSpace(part); p != "" {
				ids = append(ids, p)
			}
		}
	}
	items, err := h.svc.CoverageBasis(r.Context(), ids)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if items == nil {
		items = []CoverageBasis{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// resolveScan answers the keyboard-wedge scan: one code in, one product out, or 404.
func (h *Handler) resolveScan(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	q := r.URL.Query()
	hit, err := h.svc.ResolveScan(r.Context(), strings.TrimSpace(q.Get("code")), strings.TrimSpace(q.Get("branch_id")))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, hit)
}

// getImage streams the catalogue thumbnail. A missing image is a plain 404 the UI turns into its
// placeholder glyph, never a broken-image state, and never a synthesized "no image" PNG, which
// would defeat the browser's own caching and hide the absence from the caller.
func (h *Handler) getImage(w http.ResponseWriter, r *http.Request) {
	img, err := h.svc.GetPrimaryImage(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	etag := `"` + img.Checksum + `"`
	if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", img.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(img.ByteSize))
	w.Header().Set("ETag", etag)
	// Thumbnails change rarely and are content-addressed by the ETag, so a short cache with
	// revalidation keeps a 40-row grid from re-downloading every image on every keystroke.
	w.Header().Set("Cache-Control", "private, max-age=300, must-revalidate")
	// The bytes are attacker-influenced (an authenticated manager uploaded them). nosniff stops a
	// browser from second-guessing the allowlisted content type we just set.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(img.Bytes)
}

// putImage stores the thumbnail. Raw bytes with a Content-Type header, not base64 in JSON: base64
// inflates the payload by a third for no benefit when the body is a single binary blob.
func (h *Handler) putImage(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	// MaxBytesReader is the real ceiling, it aborts the read rather than buffering an attacker's
	// gigabyte before the size check. maxImageBytes+1 so a body exactly at the cap still succeeds and
	// one byte over is detectable as too-large rather than as a truncated read.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImageBytes+1))
	if err != nil {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "image exceeds the 1 MiB thumbnail cap")
		return
	}
	err = h.svc.PutPrimaryImage(r.Context(), r.PathValue("id"), r.Header.Get("Content-Type"),
		r.URL.Query().Get("alt"), body, identity.StaffUserIDFromContext(r.Context()))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"product_id": r.PathValue("id"), "byte_size": len(body)})
}

func (h *Handler) deleteImage(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	if err := h.svc.DeletePrimaryImage(r.Context(), r.PathValue("id")); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "DELETED"})
}

// search serves the staff catalogue picker. Envelope matches the portal catalogue search ({"items":[…]})
// so both consumers parse the same shape.
func (h *Handler) search(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := 0
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	// Service clamps limit to [1,50] and defaults it; an empty query returns no rows rather than the
	// whole catalogue.
	items, err := h.svc.SearchProducts(r.Context(), q, limit)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if items == nil {
		items = []CatalogResult{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// parseFilter reads the faceted-catalogue filter from the query string. M4 (A-4) adds
// collection, category_id, q (name/sku/description search), and in_stock_only (branch-scoped
// cross-module stock read) on top of A-18's 5 same-module facets.
func parseFilter(r *http.Request) Filter {
	q := r.URL.Query()
	return Filter{
		Finish:       strings.TrimSpace(q.Get("finish")),
		ColorFamily:  strings.TrimSpace(q.Get("color_family")),
		Application:  strings.TrimSpace(q.Get("application")),
		Color:        strings.TrimSpace(q.Get("color")),
		Manufacturer: strings.TrimSpace(q.Get("manufacturer")),
		Collection:   strings.TrimSpace(q.Get("collection")),
		CategoryID:   strings.TrimSpace(q.Get("category_id")),
		Q:            strings.TrimSpace(q.Get("q")),
		ActiveOnly:   q.Get("active") == "true",
		InStockOnly:  q.Get("in_stock_only") == "true",
		BranchID:     strings.TrimSpace(q.Get("branch_id")),
	}
}

// pageParam reads one non-negative row-count query parameter (?limit=, ?offset=), qol2 park P1.
//
// GRADED, not silently defaulted, following inventory.offsetParam: a bad offset read as 0 hands a
// caller page one when it asked for page three and it merges the rows as if they were page three,
// which is data loss dressed as success. Absent stays absent: no parameter means the endpoint's
// existing behaviour (no limit = the whole filtered catalogue, no offset = start at the top), and
// asking for nothing is never an error.
func pageParam(r *http.Request, name string) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, badReq("%s must be a whole number, zero or more", name)
	}
	return n, nil
}

// list backs GET /api/v1/products.
//
// Paging is OPT-IN (qol2 P1): with neither ?limit= nor ?offset= the response is byte-for-byte what
// it has always been, a bare JSON array of every matching product. That shape is load-bearing for
// three existing callers (the POS product grid, the catalogue screen's client-side sort/CSV, and the
// server-side .xlsx export, which must carry the WHOLE catalogue), so it is preserved rather than
// wrapped in an envelope.
//
// When a page IS requested, X-Total-Count carries the pre-limit total. It goes in a header precisely
// so the body shape does not fork between paged and unpaged callers, but a pager still needs its
// denominator, because a page of 200 out of an unknown total is how a user concludes the filter is
// broken when the list is merely long.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	f := parseFilter(r)
	limit, err := pageParam(r, "limit")
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	offset, err := pageParam(r, "offset")
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	f.Limit, f.Offset = limit, offset

	list, err := h.svc.List(r.Context(), f)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if list == nil {
		list = []Product{}
	}
	if limit > 0 || offset > 0 {
		total, terr := h.svc.Count(r.Context(), f)
		if terr != nil {
			// The page itself is good; only its denominator is missing. Log and omit the header
			// rather than 500 a list that was read successfully, a pager without a total still
			// pages, and the alternative is a working screen replaced by an error. A store with no
			// counting capability at all is the expected case here, not a fault, so it is not
			// logged as one.
			if !errors.Is(terr, errNoCount) {
				h.log.Error("product: page total unavailable", "path", r.URL.Path, "err", terr)
			}
		} else {
			w.Header().Set("X-Total-Count", strconv.Itoa(total))
		}
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

// facets backs GET /api/v1/products/facets, per-facet distinct values + counts that reflect
// the currently-applied filter (M4), so the catalogue can render honest counts and drop the
// front-end "harvest distinct on first load" hack.
func (h *Handler) facets(w http.ResponseWriter, r *http.Request) {
	facets, err := h.svc.Facets(r.Context(), parseFilter(r))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if facets == nil {
		facets = []Facet{}
	}
	httpx.WriteJSON(w, http.StatusOK, facets)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var attrs map[string]any
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&attrs); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	p, err := h.svc.Update(r.Context(), r.PathValue("id"), attrs)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

// writeErr maps service errors without leaking internals: client-input errors →
// 400; not-found → 404; everything else → generic 500 with the real error logged.
func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var bre badRequestError
	switch {
	case errors.As(err, &bre):
		httpx.Error(w, http.StatusBadRequest, bre.Error())
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not found")
	case errors.Is(err, ErrImageTooLarge):
		httpx.Error(w, http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, ErrImageType):
		httpx.Error(w, http.StatusUnsupportedMediaType, err.Error())
	case errors.Is(err, errNoImageStore), errors.Is(err, errNoCounterStore):
		httpx.Error(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, errNoDB):
		httpx.Error(w, http.StatusServiceUnavailable, "database unavailable")
	default:
		h.log.Error("product: internal error", "method", r.Method, "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal error")
	}
}

// listUnits answers the staff read of the unit catalogue (HH-LOAD-1
// LD-3, productListUnits): one cursor page of the catalogue, the code
// the stable ordering key. A Service wired without the catalogue read
// answers the unavailable family, never an empty catalogue.
func (h *Handler) listUnits(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	p, err := httpx.ParseCursorPage(r)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "cursor is malformed")
		return
	}
	rows, next, err := h.svc.UnitsPage(r.Context(), p.Limit, p.Cursor)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteCursorPage(w, rows, next, p.Limit)
}
