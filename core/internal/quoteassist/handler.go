package quoteassist

// handler.go, the HTTP surface. Four routes, three of them READ, and the fourth flips a config
// switch.
//
//	POST  /api/v1/quote-assist/extract              staff   read a job description → PROPOSALS
//	GET   /api/v1/quote-assist/scope-gaps?quote_id= staff   evaluate one quote against the rules
//	GET   /api/v1/quote-assist/scope-rules          admin   the rule listing, enabled and disabled
//	PATCH /api/v1/quote-assist/scope-rules/{code}   admin   switch one rule on or off
//
// WHY EXTRACT IS A POST AND STILL A READ. The job description is a paragraph, a request BODY, not a
// query string, but nothing is written by it: no quote line, no draft, no saved paste, not even a
// ledger row unless the caller explicitly asked for the AI path and the rails let it through. It is
// the same shape POST /api/v1/quotes/{id}/lines/{n}/compare already uses for the same reason
// (quote/edit_handler.go), so the surface is not inventing a convention.
//
// WHY THE EXTRACT ROUTE IS NOT UNDER /quotes/{id}. Because extraction does not need a quote and
// pretending it does would be a lie about the dependency: a rep can read a description before they
// have created the quote, and the parsed rows are the same rows whichever quote they land on. What
// DOES need a quote is the confirmation, and that goes through the quote module's own route, with the
// quote module's own guards, its pricing, its stock holds and its audit row.
//
// THE ADMIN ROUTES ARE RequireAdmin, NOT RequireManager. The rule table decides what every rep in
// the business is advised about; the audit viewer's own gate is the right rung for it.

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"net/http"
	"strings"
)

// maxExtractBytes bounds a pasted description.
//
// 32 KiB is roughly ten pages of prose, far more than any job description and small enough that a
// pasted spreadsheet is refused at the edge rather than parsed into a hundred rows nobody will read.
// The phrase cap (maxPhrases) is the second, friendlier limit; this one is the one that protects the
// process.
const maxExtractBytes = 32 << 10

// Handler serves the quote-assist surface.
type Handler struct {
	svc      *Service
	userLink identity.UserIDLinker
}

// NewHandler builds the handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// RegisterRoutes wires the surface. The guards are injected as decorators rather than imported, so
// this module never depends on the identity package's middleware, the house pattern
// (quote.RegisterStaffRoutes, assist.RegisterRoutes).
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("POST /api/v1/quote-assist/extract", g.RequireStaff(h.staff(h.extract)))
	r.Handle("GET /api/v1/quote-assist/scope-gaps", g.RequireStaff(h.staff(h.scopeGaps)))
	r.Handle("GET /api/v1/quote-assist/scope-rules", g.RequireAdmin(h.staff(h.listRules)))
	r.Handle("PATCH /api/v1/quote-assist/scope-rules/{code}", g.RequireAdmin(h.staff(h.patchRule)))
}

func (h *Handler) extract(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	r.Body = http.MaxBytesReader(w, r.Body, maxExtractBytes)
	var body ExtractRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.Error(w, http.StatusBadRequest, "That description didn't arrive in a form I could read, try pasting it again.")
		return
	}
	// The caller is the guard-stamped one, converted ONCE through the assist package's own
	// conversion. Not re-derived, not read from the body: the AI rails downstream resolve a budget
	// against this value, and a caller a request could name would be a budget a request could pick.
	c := assist.CallerFrom(r)
	out, err := h.svc.Extract(r.Context(), c, body)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	// Arrays are never null on the wire. A client that renders `proposals.map(...)` must not have to
	// write `?? []` to survive an empty answer (DIB-471).
	if out.Proposals == nil {
		out.Proposals = []Proposal{}
	}
	if out.Unmatched == nil {
		out.Unmatched = []Unmatched{}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) scopeGaps(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	quoteID := strings.TrimSpace(r.URL.Query().Get("quote_id"))
	if quoteID == "" {
		httpx.Error(w, http.StatusBadRequest, "Which quote? Pass quote_id.")
		return
	}
	out, found, err := h.svc.ScopeGaps(r.Context(), quoteID)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	if !found {
		// 404 with no detail. "No such quote" and "not yours" are one answer here for the same
		// reason they are one answer in the quote module: a distinguishable refusal is an existence
		// oracle for a branch the caller cannot reach.
		httpx.Error(w, http.StatusNotFound, "No quote found.")
		return
	}
	if out.Findings == nil {
		out.Findings = []Finding{}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) listRules(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	rules, err := h.svc.Rules(r.Context())
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

func (h *Handler) patchRule(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	code := strings.TrimSpace(r.PathValue("code"))
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.Error(w, http.StatusBadRequest, "malformed request body")
		return
	}
	// A POINTER, so an absent field is refused rather than read as false. `{}` arriving as "switch
	// this rule off" is the worst possible default for a config surface, silent, destructive, and
	// indistinguishable from a client bug.
	if body.Enabled == nil {
		httpx.Error(w, http.StatusBadRequest, "Say whether the rule should be enabled: {\"enabled\": true|false}.")
		return
	}
	actor := ""
	if sc, ok := identity.StaffContextFromContext(r.Context()); ok {
		actor = sc.UserID
	}
	rule, err := h.svc.SetRuleEnabled(r.Context(), code, *body.Enabled, actor)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rule)
}

// writeErr maps this package's errors onto status codes.
//
// The three unwired-seam errors are 503, not 500: nothing failed, a seam is not connected in this
// deployment, and the honest answer names it (the assist UnavailableError doctrine). A rule code
// that does not exist is a 404. Everything else is a 500, and there is deliberately no branch here
// that turns an over-budget AI path into an error status, because Extract never returns one.
func (h *Handler) writeErr(w http.ResponseWriter, err error) {
	var bad httpx.BadRequestError
	switch {
	case errors.As(err, &bad):
		httpx.Error(w, http.StatusBadRequest, bad.Msg)
	case errors.Is(err, ErrCatalogueUnwired):
		httpx.Error(w, http.StatusServiceUnavailable, "Reading a description isn't switched on in this system yet, the catalogue search seam isn't wired.")
	case errors.Is(err, ErrQuoteLinesUnwired), errors.Is(err, ErrRulesUnwired):
		httpx.Error(w, http.StatusServiceUnavailable, "Scope checking isn't switched on in this system yet.")
	case errors.Is(err, ErrRuleNotFound):
		httpx.Error(w, http.StatusNotFound, "No scope rule with that code.")
	default:
		httpx.Error(w, http.StatusInternalServerError, "Something went wrong reading that.")
	}
}
