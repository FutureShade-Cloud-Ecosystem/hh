package dashboard

// widget_handler.go, the widget substrate's routes, and the ONE decision this layer makes that
// nothing below it may second-guess: whether this caller is allowed cost.
//
// It reuses vizScopeFrom (viz_handler.go) deliberately rather than repeating the tier test. There is
// one answer in this product to "may this person see cost", caller.IsManager(), the same test quote
// and pos apply, and a second copy of it here would be a second place for it to be wrong.

import (
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"net/http"
)

// RegisterWidgetRoutes wires the widget substrate.
//
//	GET    /api/v1/dashboard/widgets                    staff, MY resolved Home
//	PUT    /api/v1/dashboard/widgets                    staff, save MY whole layout
//	DELETE /api/v1/dashboard/widgets                    staff, RESET to the role default (a DELETE)
//	GET    /api/v1/dashboard/widgets/catalog            staff, measures × splits × shapes × sizes
//	GET    /api/v1/dashboard/widgets/gallery            staff, the ready-made widgets MY role is offered
//	GET    /api/v1/dashboard/widgets/data?key=…&key=…   staff, the NUMBERS behind MY widgets
//	GET    /api/v1/dashboard/needs                      staff, the Needs-you queue
//	GET    /api/v1/dashboard/profiles/{profile}/widgets ADMIN, one role default
//	PUT    /api/v1/dashboard/profiles/{profile}/widgets ADMIN, edit that role default
//	GET    /api/v1/dashboard/profiles/{profile}/gallery ADMIN, that role's gallery as STORED
//	PUT    /api/v1/dashboard/profiles/{profile}/gallery ADMIN, edit that role's gallery
//
// THE GALLERY SPLITS ON THE SAME LINE EVERYTHING ELSE HERE DOES. Reading MY gallery is staff, because
// it is the menu I am offered and it is narrowed to my own catalog before it leaves. Writing a role's
// gallery is admin, because it is curation, one row read by everybody filed under that shape, and
// it is gated against the FULL catalog rather than the writer's own, exactly as a role default is:
// curation hides things and never grants anything, so naming a measure is not seeing it.
//
// THE AUTHORITY SPLIT IS 0140's, unchanged: MY Home is mine, any staff member may arrange their own
// , and a ROLE's default belongs to an admin. A counter clerk rearranging their own widgets is
// housekeeping; a counter clerk rearranging every counter clerk's widgets is not.
//
// THERE IS NO "ADD ONE WIDGET" AND NO "REMOVE ONE WIDGET" ROUTE. A layout is one document and the
// override is whole-or-absent, so the builder places a widget by PUTting the layout it is already
// showing with the new one at position 0. A per-widget POST would need the server to materialise the
// role default first to have something to append to, which is the write this feature exists to not
// do (0191's one-way door).
func (h *Handler) RegisterWidgetRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/dashboard/widgets", g.RequireStaff(h.staff(h.myWidgets)))
	r.Handle("PUT /api/v1/dashboard/widgets", g.RequireStaff(h.staff(h.saveMyWidgets)))
	r.Handle("DELETE /api/v1/dashboard/widgets", g.RequireStaff(h.staff(h.resetMyWidgets)))
	r.Handle("GET /api/v1/dashboard/widgets/catalog", g.RequireStaff(h.staff(h.widgetCatalog)))
	r.Handle("GET /api/v1/dashboard/widgets/gallery", g.RequireStaff(h.staff(h.widgetGallery)))
	r.Handle("GET /api/v1/dashboard/widgets/data", g.RequireStaff(h.staff(h.widgetData)))
	r.Handle("GET /api/v1/dashboard/needs", g.RequireStaff(h.staff(h.needsQueue)))
	r.Handle("GET /api/v1/dashboard/profiles/{profile}/widgets", g.RequireAdmin(h.staff(h.getProfileWidgets)))
	r.Handle("PUT /api/v1/dashboard/profiles/{profile}/widgets", g.RequireAdmin(h.staff(h.putProfileWidgets)))
	r.Handle("GET /api/v1/dashboard/profiles/{profile}/gallery", g.RequireAdmin(h.staff(h.getProfileGallery)))
	r.Handle("PUT /api/v1/dashboard/profiles/{profile}/gallery", g.RequireAdmin(h.staff(h.putProfileGallery)))
}

type widgetsBody struct {
	Widgets []Widget `json:"widgets"`
}

type galleryBody struct {
	Entries []GalleryEntry `json:"entries"`
}

func (h *Handler) myWidgets(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	httpx.WriteJSON(w, http.StatusOK, h.svc.WidgetsFor(r.Context(), h.vizScopeFrom(r)))
}

func (h *Handler) saveMyWidgets(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var body widgetsBody
	if !decode(w, r, &body) {
		return
	}
	rows, err := h.svc.SaveMyWidgets(r.Context(), h.vizScopeFrom(r), body.Widgets)
	if err != nil {
		h.failWidget(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, widgetsBody{Widgets: rows})
}

// resetMyWidgets answers with the layout the reset LANDED ON, not just a count, the same choice
// resetMyLayout and resetMyTabs make, and for the same reason: the caller's whole purpose is "put me
// back on the role default", and making them re-fetch to find out what that is invites a flash of
// the old bento in between.
func (h *Handler) resetMyWidgets(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	caller, _ := identity.StaffContextFromContext(r.Context())
	n, err := h.svc.ResetMyWidgets(r.Context(), caller.UserID)
	if err != nil {
		h.failWidget(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"cleared": n,
		"layout":  h.svc.WidgetsFor(r.Context(), h.vizScopeFrom(r)),
	})
}

// widgetCatalog is the builder's one read. The measures come from the caller's own catalog, so a
// measure they may not see is not in the payload at all, there is nothing for the builder to grey
// out, because nothing arrived.
func (h *Handler) widgetCatalog(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	scope := h.vizScopeFrom(r)
	measures := h.svc.WidgetCatalogFor(r.Context(), scope)
	if measures == nil {
		measures = []MetricMeta{} // an empty catalog is an empty ARRAY, never null
	}
	// THE GRID GOES ON THIS PAYLOAD TOO, and it is not decoration. The grip resizes freely and has to
	// know which lines are real BEFORE the drag ends, a client that guessed would either snap to a
	// column the CHECK refuses (a 422 at the end of a gesture, naming a number nobody typed) or carry
	// its own copy of the snap set, which is the mirror this wire exists to make unnecessary. `spans`
	// is SpanOf as data for the same reason: the arithmetic that decides whether a bento fits is the
	// server's, and a browser recomputing it is a second place for it to be wrong.
	spans := make(map[string][]int, len(WidgetSizeSet))
	for _, s := range WidgetSizeSet {
		c, rows := SpanOf(s)
		spans[s] = []int{c, rows}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"measures":  measures,
		"shapes":    WidgetShapeSet,
		"sizes":     WidgetSizeSet,
		"spans":     spans,
		"span_cols": WidgetColSet,
		"max_rows":  WidgetMaxRows,
		"splits":    SplitSet,
		"ranges":    RangeSet,
		"profile":   h.svc.ProfileFor(r.Context(), scope.RoleCode),
	})
}

// widgetGallery is the "Add widget" panel's first read: the ready-made widgets this caller's role is
// offered, already narrowed to this caller's own catalog.
//
// AN ENTRY THEY MAY NOT SEE IS NOT IN THE PAYLOAD AT ALL, there is nothing for the panel to grey
// out, because nothing arrived. Same sentence as widgetCatalog above it, and the same gate.
func (h *Handler) widgetGallery(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	httpx.WriteJSON(w, http.StatusOK, h.svc.GalleryFor(r.Context(), h.vizScopeFrom(r)))
}

func (h *Handler) needsQueue(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	httpx.WriteJSON(w, http.StatusOK, h.svc.NeedsFor(r.Context(), h.vizScopeFrom(r)))
}

func (h *Handler) getProfileWidgets(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	profile := r.PathValue("profile")
	rows, err := h.svc.ProfileWidgetsFor(r.Context(), profile)
	if err != nil {
		h.failWidget(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"profile": profile, "widgets": rows})
}

func (h *Handler) putProfileWidgets(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body widgetsBody
	if !decode(w, r, &body) {
		return
	}
	caller, _ := identity.StaffContextFromContext(r.Context())
	rows, err := h.svc.SaveProfileWidgets(r.Context(), r.PathValue("profile"), body.Widgets, caller.UserID)
	if err != nil {
		h.failWidget(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, widgetsBody{Widgets: rows})
}

// getProfileGallery answers a role's gallery AS STORED, retired entries included, an admin about to
// edit a gallery has to be able to see what they switched off in order to switch it back on. The
// staff read is where the caller's own catalog narrows it.
func (h *Handler) getProfileGallery(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	profile := r.PathValue("profile")
	rows, err := h.svc.ProfileGalleryFor(r.Context(), profile)
	if err != nil {
		h.failWidget(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"profile": profile, "entries": rows})
}

func (h *Handler) putProfileGallery(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body galleryBody
	if !decode(w, r, &body) {
		return
	}
	caller, _ := identity.StaffContextFromContext(r.Context())
	rows, err := h.svc.SaveProfileGallery(r.Context(), r.PathValue("profile"), body.Entries, caller.UserID)
	if err != nil {
		h.failWidget(w, err)
		return
	}
	if rows == nil {
		rows = []GalleryEntry{} // an empty gallery is an empty ARRAY, never null
	}
	httpx.WriteJSON(w, http.StatusOK, galleryBody{Entries: rows})
}

// failWidget maps the widget layer's sentinels.
//
// 422, NOT 403, FOR A FORBIDDEN MEASURE. The submission is well-formed and the caller is entitled to
// be here; what is wrong is the CONTENT of one field, which is what 422 means. A 403 would also be
// defensible, and the reason it is not used is that this endpoint returns the identical status and
// wording for "no such measure", so a 403 would answer "that measure exists and is not yours" for
// half the cases, which is exactly the enumeration the one-message rule exists to prevent.
func (h *Handler) failWidget(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrMeasureNotOffered), errors.Is(err, ErrShapeNotOffered),
		errors.Is(err, ErrSplitNotOffered), errors.Is(err, ErrRangeNotOffered),
		errors.Is(err, ErrBranchNotOffered), errors.Is(err, ErrSpanNotOffered):
		httpx.Error(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, ErrEmptyWidgets), errors.Is(err, ErrNoCaller):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrUnknownProfile):
		httpx.Error(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrNoGalleryStore):
		// The gallery substrate is not migrated. A 503 for ErrNoWidgetStore's reason exactly: it is a
		// deployment state that fixes itself when 0208 runs, not a bug in the request.
		httpx.Error(w, http.StatusServiceUnavailable, "the widget gallery is not available on this deployment yet")
	case errors.Is(err, ErrNoWidgetStore):
		// The substrate is not migrated. A 503 rather than a 500: it is a deployment state that
		// fixes itself when the migration runs, not a bug in the request or a broken server.
		httpx.Error(w, http.StatusServiceUnavailable, "widgets are not available on this deployment yet")
	default:
		h.fail(w, err)
	}
}

// maxWidgetData bounds one measurement read, by TRUNCATION rather than refusal, the same idiom and
// the same posture as maxVizTiles. A role landing holds 6-9 widgets by the doctrine and a person may
// add more; twice the largest seeded layout is room to spare, and a URL built to make the server
// measure a hundred series gets the first thirty-two rather than an error nobody can act on.
const maxWidgetData = 32

// widgetData answers the numbers behind the caller's own widgets.
//
// THE KEYS SELECT; THEY DO NOT DESCRIBE. Everything measured, the measure, the split, the branch,
// the window, comes from the caller's stored row, resolved through the same layering and the same
// catalog gate as their Home. There is no parameter here that can widen what gets measured, which is
// the same discipline scopeFrom applies to branches: a dashboard that let you ask for another
// branch's numbers by editing a URL would be a nicer-looking version of the thing branch scoping
// exists to prevent.
//
// It reuses vizScopeFrom for the cost decision rather than repeating the tier test, exactly as the
// rest of this file does.
func (h *Handler) widgetData(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	keys := r.URL.Query()["key"]
	if len(keys) > maxWidgetData {
		keys = keys[:maxWidgetData]
	}
	data := h.svc.WidgetDataFor(r.Context(), h.vizScopeFrom(r), keys)
	if data == nil {
		data = []WidgetDatum{} // an empty measurement is an empty ARRAY, never null.
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": data})
}
