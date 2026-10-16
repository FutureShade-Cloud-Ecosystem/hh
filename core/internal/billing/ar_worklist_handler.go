package billing

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

type ARWorklistHandler struct {
	userLink identity.UserIDLinker
	service  *ARWorklistService
	log      *slog.Logger
	zone     houseday.Source
}

func NewARWorklistHandler(service *ARWorklistService, log *slog.Logger) *ARWorklistHandler {
	if log == nil {
		log = slog.Default()
	}
	return &ARWorklistHandler{service: service, log: log}
}

func (h *ARWorklistHandler) WithZone(src houseday.Source) *ARWorklistHandler {
	h.zone = src
	return h
}

func (h *ARWorklistHandler) WithUserLinker(link identity.UserIDLinker) *ARWorklistHandler {
	h.userLink = link
	return h
}

func (h *ARWorklistHandler) staff(next guards.StaffHandler) guards.StaffHandler {
	return func(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
		next(w, identity.StampRequest(r, caller, h.userLink), caller)
	}
}

func (h *ARWorklistHandler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/billing/ar/worklist", g.RequireManager(h.staff(h.read)))
}

func (h *ARWorklistHandler) read(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	asOf, err := parseAsOfFor(r.Context(), h.zone, r.URL.Query().Get("as_of"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	query := r.URL.Query()
	response, err := h.service.Worklist(r.Context(), WorklistFilter{
		Query: strings.TrimSpace(query.Get("q")), Chip: query.Get("chip"),
		BranchID: query.Get("branch_id"), AsOf: asOf,
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, response)
}

func (h *ARWorklistHandler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var statusErr *settings.StatusError
	var bad httpx.BadRequestError
	switch {
	case errors.As(err, &statusErr):
		httpx.Error(w, statusErr.Status, statusErr.Msg)
	case errors.Is(err, ErrBranchForbidden):
		httpx.Error(w, http.StatusForbidden, BranchForbiddenWords)
	case errors.As(err, &bad):
		httpx.Error(w, http.StatusBadRequest, bad.Error())
	default:
		h.log.Warn("ar worklist request failed", "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "the receivables worklist is not answering right now")
	}
}
