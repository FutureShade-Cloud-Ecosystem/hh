package dispatch

// handler.go, the dispatch HTTP surface. Thin by policy: it decodes, delegates, and maps the error
// taxonomy. Every authority decision is in service.go, so there is exactly one place to read to know
// who may do what.

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// maxBodyBytes bounds a JSON body. The photo upload has its own, larger bound.
const maxBodyBytes = 1 << 20

// Handler exposes the dispatch surface.
type Handler struct {
	userLink identity.UserIDLinker
	svc      *Service
	log      *slog.Logger
}

// NewHandler wires the service.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log}
}

// - fleet -

func (h *Handler) listTrucks(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	items, err := h.svc.Trucks(r.Context(), q.Get("branch_id"), q.Get("include_inactive") == "true")
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) createTruck(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in Truck
	if !decodeJSON(w, r, &in) {
		return
	}
	id, err := h.svc.CreateTruck(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (h *Handler) listDrivers(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	items, err := h.svc.Drivers(r.Context(), q.Get("branch_id"), q.Get("include_inactive") == "true")
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) createDriver(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in Driver
	if !decodeJSON(w, r, &in) {
		return
	}
	id, err := h.svc.CreateDriver(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]string{"id": id})
}

// - board + stops -

func (h *Handler) board(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	b, err := h.svc.Board(r.Context(), q.Get("branch_id"), q.Get("date"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) schedule(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in ScheduleInput
	if !decodeJSON(w, r, &in) {
		return
	}
	stop, err := h.svc.Schedule(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, stop)
}

func (h *Handler) stop(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	d, err := h.svc.StopDetail(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

func (h *Handler) assignStop(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in AssignInput
	if !decodeJSON(w, r, &in) {
		return
	}
	stop, err := h.svc.AssignStop(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, stop)
}

func (h *Handler) advanceStop(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	stop, err := h.svc.AdvanceStop(r.Context(), r.PathValue("id"), in.Status, in.Reason)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, stop)
}

func (h *Handler) capturePOD(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in PODInput
	if !decodeJSON(w, r, &in) {
		return
	}
	d, err := h.svc.CapturePOD(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

// uploadPhoto takes the raw image as the request BODY with a real Content-Type, not multipart.
//
// The driver surface has one file and no other fields, so multipart would add a parser, a boundary
// and a second failure mode to carry nothing. The bytes are bounded by MaxBytesReader BEFORE they are
// read into memory, a phone on a bad connection retrying a 4 MB upload must not be able to make the
// server hold an unbounded buffer.
func (h *Handler) uploadPhoto(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	ct := r.Header.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxPhotoBytes+1))
	if err != nil {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "dispatch: the photo is too large")
		return
	}
	meta, err := h.svc.AddPhoto(r.Context(), r.PathValue("id"), ct, r.URL.Query().Get("caption"), body)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, meta)
}

func (h *Handler) photo(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	p, err := h.svc.Photo(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.Header().Set("Content-Type", p.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(p.Bytes)))
	// Evidence is immutable once written, so it caches, but PRIVATELY. A shared cache holding a
	// customer's driveway is not a trade this endpoint makes.
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(p.Bytes)
}

// - runs -

func (h *Handler) listRuns(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	items, err := h.svc.Runs(r.Context(), q.Get("branch_id"), q.Get("date"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) createRun(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in CreateRunInput
	if !decodeJSON(w, r, &in) {
		return
	}
	run, err := h.svc.CreateRun(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, run)
}

func (h *Handler) run(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	d, err := h.svc.RunDetail(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

func (h *Handler) runByNo(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	d, err := h.svc.RunByNo(r.Context(), r.PathValue("runNo"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

func (h *Handler) assignCrew(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in struct {
		TruckID  string `json:"truck_id"`
		DriverID string `json:"driver_id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	run, err := h.svc.AssignCrew(r.Context(), r.PathValue("id"), in.TruckID, in.DriverID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, run)
}

func (h *Handler) dispatchRun(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	d, err := h.svc.DispatchRun(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

func (h *Handler) completeRun(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	run, err := h.svc.CompleteRun(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, run)
}

func (h *Handler) cancelRun(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	run, err := h.svc.CancelRun(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, run)
}

// manifest serves the printable HTML (the driver's working copy).
func (h *Handler) manifest(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	m, err := h.svc.Manifest(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	html, err := RenderManifestHTML(m)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// A manifest is a point-in-time document: a cached copy would show stops that have since moved,
	// which is the one thing on the page a driver must not be handed.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(html))
}

func (h *Handler) printManifest(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	jobID, err := h.svc.PrintManifest(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]string{"job_id": jobID, "doc_type": "DELIVERY_DOC"})
}

func (h *Handler) myRuns(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.svc.MyRuns(r.Context(), r.URL.Query().Get("date"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// - will-call -

func (h *Handler) mintWillCall(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in struct {
		OrderID  string `json:"order_id"`
		TTLHours int    `json:"ttl_hours"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	minted, err := h.svc.MintWillCall(r.Context(), in.OrderID, time.Duration(in.TTLHours)*time.Hour)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	// The RAW token is in this response and NOWHERE else, ever. no-store so it does not land in a
	// proxy or a browser cache on a shared counter machine.
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusCreated, minted)
}

func (h *Handler) listWillCall(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.svc.ListWillCall(r.Context(), r.URL.Query().Get("order_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) revokeWillCall(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if err := h.svc.RevokeWillCall(r.Context(), r.PathValue("id")); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "REVOKED"})
}

func (h *Handler) publicWillCall(w http.ResponseWriter, r *http.Request) {
	v, err := h.svc.PublicWillCall(r.Context(), r.PathValue("token"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) publicConfirmWillCall(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TypedName string `json:"typed_name"`
		Affirmed  bool   `json:"affirmed"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	v, err := h.svc.ConfirmWillCall(r.Context(), r.PathValue("token"), in.TypedName, in.Affirmed)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, v)
}

// - plumbing -

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(dst); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

// writeErr maps the dispatch error taxonomy to HTTP.
//
// THE 409 FAMILY IS NOT 400. ErrIllegalTransition, ErrCrewRequired, ErrPODRequired and the rest all
// describe a well-formed request against a world in the wrong state. A client that gets 400 will
// "fix" the request and retry; a client that gets 409 with a sentence will fix the WORLD, which is
// the only thing that can actually work.
func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	if status, msg, ok := ErrorHTTPStatus(err); ok {
		httpx.Error(w, status, msg)
		return
	}
	h.log.Error("dispatch: internal error", "method", r.Method, "path", r.URL.Path, "err", err)
	httpx.Error(w, http.StatusInternalServerError, "internal error")
}

// ErrorHTTPStatus grades one dispatch error into the status and the sentence the surface shows.
// ok=false means the error is not in this module's taxonomy and belongs in the 500 arm, logged.
//
// IT IS EXPORTED, AND IT IS THE SAME MAPPING writeErr HAS ALWAYS RUN, the body moved here, nothing
// about it changed. The 0195 approval bridge is what needed a second reader: dispatch's apply runs
// inside identity's decide transaction, so its refusals ("this order is now on 3 loads and the raise
// was asked for 4") reach an approver through identity's handler and not through this one. Without a
// shared grader those refusals would arrive as generic 500s, which is exactly the failure
// gl.ErrorHTTPStatus exists to prevent on the journal bridge beside it.
//
// A SECOND COPY WAS THE ALTERNATIVE AND IT IS THE WRONG ONE, for the reason this function's own
// header used to give: two error mappers on one surface is how a 403 becomes a 500 in the path
// nobody re-read.
func ErrorHTTPStatus(err error) (int, string, bool) {
	var br httpx.BadRequestError
	var ce conflictError
	// 0184 folds the load board's two refusals and the settings kit's 422-in-words into the SAME
	// mapping rather than a second one beside it.
	if status, msg, ok := loadboardStatus(err); ok {
		return status, msg, true
	}
	switch {
	case errors.Is(err, ErrUnauthorized):
		return http.StatusUnauthorized, "unauthenticated", true
	case errors.Is(err, ErrForbidden):
		return http.StatusForbidden, "forbidden", true
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrTokenNotFound):
		return http.StatusNotFound, "not found", true
	case errors.Is(err, ErrTokenGone):
		// 410, not 404: the holder learns the CODE died rather than being told the order never
		// existed (0092's header; quote share links answer the same way for the same reason).
		return http.StatusGone, err.Error(), true
	case errors.Is(err, ErrNoSignatureProvider):
		return http.StatusNotImplemented, err.Error(), true
	case errors.Is(err, ErrIllegalTransition), errors.Is(err, ErrPODRequired),
		errors.Is(err, ErrCrewRequired), errors.Is(err, ErrRunNotDispatched),
		errors.Is(err, ErrStopUnassigned), errors.Is(err, ErrRunHasOpenStops):
		return http.StatusConflict, err.Error(), true
	case errors.As(err, &ce):
		return http.StatusConflict, ce.Error(), true
	case errors.As(err, &br):
		return http.StatusBadRequest, br.Msg, true
	}
	return 0, "", false
}
