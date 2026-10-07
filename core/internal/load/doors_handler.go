// doors_handler.go serves the four reference and supplier doors of
// HH-LOAD-1 LD-4: loadMapBranches, loadMapUsers, loadUpsertCategories
// and loadUpsertSuppliers, each a PUT under /api/v1/load/ behind the
// machine gate the registrar wraps them in. One body shape (the plan's
// section 1.1): the source system, the caller's batch id, and a bounded
// list of items, each with its legacy key, the derived key, the record,
// and the references named by legacy key. One answer shape: one outcome
// per item and the batch's counts, the map doors' attach arm included.
//
// The batch level answers: 400 for a body over the bound or out of
// shape, 409 for a batch id recorded under another door, and 503 for
// ErrBatchContended, the contention sentinel the frame answers when
// Postgres rolled the whole batch back as a deadlock victim: nothing of
// it stands, and resubmitting the whole batch is the caller's answer,
// so the status is one a caller may retry the batch on, never a per
// item outcome. The org and the actor come from the machine scope the
// gate stamps: the handler reads no header of its own.
package load

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// maxSubmitBodyBytes bounds a submit body: the bound item list with its
// records (names, terms, an address, a contact), with headroom.
// MaxBytesReader refuses an oversized body before it is fully buffered.
const maxSubmitBodyBytes = 4 << 20

// DoorsHandler serves the four submit doors. The routes' doors arrive
// by constructor, keyed by their registration patterns; the handler
// owns no gate and no org logic: the machine gate wraps every
// registration at the registrar, and the scope it stamps names the org
// and the audit actor.
type DoorsHandler struct {
	routes map[string]*Door
	order  []string
	log    *slog.Logger
}

// NewDoorsHandler builds the submit handler over the doors' routes. The
// route keys must match the contract's paths exactly: the registration
// lands through the census reading registrar, which knows the route by
// its pattern.
func NewDoorsHandler(log *slog.Logger, routes map[string]*Door) *DoorsHandler {
	order := make([]string, 0, len(routes))
	for route := range routes {
		order = append(order, route)
	}
	sort.Strings(order)
	return &DoorsHandler{routes: routes, order: order, log: log}
}

// RegisterRoutes registers the four PUT operations. No guard lives in
// the handler: the routes are machine surface, worded machine in the
// census, and the machine gate wraps them at the registrar.
func (h *DoorsHandler) RegisterRoutes(r httpx.Registrar) {
	for _, route := range h.order {
		door := h.routes[route]
		r.Handle(route, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			h.submit(w, req, door)
		}))
	}
}

// submit answers one door's batch: the scope, the body, the frame, and
// the answer shape.
func (h *DoorsHandler) submit(w http.ResponseWriter, r *http.Request, door *Door) {
	scope, ok := authz.MachineScopeFrom(r.Context())
	if !ok {
		machineRefused(w)
		return
	}
	var body apigen.LoadSubmitRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSubmitBodyBytes))
	if err := dec.Decode(&body); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	batch, err := submitWire(scope.OrgID, scope.PrincipalID, body)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := door.UpsertBatch(r.Context(), batch)
	if err != nil {
		status, message := submitErrorStatus(err)
		if status == http.StatusServiceUnavailable {
			w.Header().Set("Retry-After", "1")
		}
		if status == http.StatusInternalServerError {
			h.log.Error("load: batch submit failed", "err", err)
		}
		httpx.Error(w, status, message)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, submitResultWire(result))
}

// submitWire validates the body's shape and bound at the boundary: the
// identity fields present and bounded, the list inside the bound, every
// item named. The store defends the same rules; the derived key and the
// references ride as named.
func submitWire(orgID, principalID string, req apigen.LoadSubmitRequest) (Batch, error) {
	if req.SourceSystem == "" || len(req.SourceSystem) > 64 {
		return Batch{}, ErrMissingField
	}
	if req.BatchId == "" || len(req.BatchId) > 128 {
		return Batch{}, ErrMissingField
	}
	if len(req.Items) == 0 || len(req.Items) > MaxBatchItems {
		return Batch{}, ErrBatchBound
	}
	items := make([]Item, 0, len(req.Items))
	for _, it := range req.Items {
		if it.LegacyId == "" || len(it.LegacyId) > MaxKeyLength {
			return Batch{}, ErrMissingField
		}
		item := Item{LegacyID: it.LegacyId, Record: it.Record}
		if it.DerivedKey != nil {
			item.DerivedKey = *it.DerivedKey
		}
		if it.Requires != nil {
			for _, r := range *it.Requires {
				item.Requires = append(item.Requires, Ref{Kind: r.Kind, LegacyID: r.LegacyId})
			}
		}
		items = append(items, item)
	}
	return Batch{OrgID: orgID, SourceSystem: req.SourceSystem, BatchID: req.BatchId,
		Caller: principalID, Items: items}, nil
}

// submitErrorStatus maps the frame's batch level errors onto the
// contract's answers. ErrBatchContended is the retryable one: the whole
// batch rolled back, nothing landed, and the caller resubmits.
func submitErrorStatus(err error) (int, string) {
	switch {
	case errors.Is(err, ErrBatchContended):
		return http.StatusServiceUnavailable,
			"the batch contended with another in flight batch and rolled back whole; resubmit the batch"
	case errors.Is(err, ErrBatchDoorMismatch):
		return http.StatusConflict, "the batch id is recorded under another door"
	case errors.Is(err, ErrBatchBound), errors.Is(err, ErrMissingField):
		return http.StatusBadRequest, err.Error()
	default:
		return http.StatusInternalServerError, "the batch failed"
	}
}

// submitResultWire maps the batch result onto the contract shape: the
// batch id, the counts by outcome (the attached word included), and one
// outcome per item, refusals carrying their reason, field and detail
// verbatim: keys and fields only, never a record value.
func submitResultWire(r BatchResult) apigen.LoadSubmitResult {
	items := make([]apigen.LoadBatchItemOutcome, 0, len(r.Items))
	for _, o := range r.Items {
		items = append(items, outcomeWire(o))
	}
	return apigen.LoadSubmitResult{
		BatchId: r.BatchID,
		Counts:  countsWire(r.Counts),
		Items:   items,
	}
}

// outcomeWire maps one item's outcome.
func outcomeWire(o Outcome) apigen.LoadBatchItemOutcome {
	item := apigen.LoadBatchItemOutcome{LegacyId: o.LegacyID, Status: o.Status}
	if o.HHID != "" {
		id := o.HHID
		item.HhId = &id
	}
	if o.Reason != "" {
		reason := o.Reason
		item.Reason = &reason
	}
	if o.Field != "" {
		field := o.Field
		item.Field = &field
	}
	if o.Detail != "" {
		detail := o.Detail
		item.Detail = &detail
	}
	return item
}

// countsWire maps the counts by outcome.
func countsWire(counts map[string]int) apigen.LoadBatchCounts {
	return apigen.LoadBatchCounts{
		Created:   int32(counts[OutcomeCreated]),
		Attached:  int32(counts[OutcomeAttached]),
		Updated:   int32(counts[OutcomeUpdated]),
		Unchanged: int32(counts[OutcomeUnchanged]),
		Refused:   int32(counts[OutcomeRefused]),
	}
}
