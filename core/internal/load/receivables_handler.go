package load

// receivables_handler.go serves loadUpsertReceivables (LD-11): the open
// receivables door's PUT. The gate owns admission (the machine scope
// arrives stamped); this boundary owns the body: the decode keeps every
// number literal (UseNumber) so money arrives as its exact cents
// spelling and never as a float, the shape checks refuse at the edge
// before any store call, and the door's own record rules answer 400
// naming the item and the field. The money batch's batch level answers
// map here: a refused batch is 422 (correctable content), a divergent
// replay and a batch id under another door are 409 (the caller's next
// move is a fresh, faithful submission), and a contended batch is the
// retryable 503 with Retry-After, the same answer the other load doors
// give: money batches re-run whole.

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// putReceivables answers one receivables batch. The org and the audit
// actor come from the machine scope the gate stamped, never from the
// body: the batch is attributed to the machine principal that submitted
// it.
func (h *Handler) putReceivables(w http.ResponseWriter, r *http.Request) {
	if h.receivables == nil {
		httpx.Error(w, http.StatusNotFound, "no receivables door is bound in this composition")
		return
	}
	scope, ok := authz.MachineScopeFrom(r.Context())
	if !ok {
		machineRefused(w)
		return
	}
	var req apigen.LoadReceivablesUpsertRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxReceivablesBodyBytes))
	// Numbers stay literal: money must arrive as its exact cents
	// spelling, and a float64 decode is the one way this boundary could
	// lose it (AGENTS.md, money is never a float).
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	items, err := receivablesWire(req)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := h.receivables.UpsertReceivables(r.Context(), Batch{
		OrgID: scope.OrgID, SourceSystem: req.SourceSystem, BatchID: req.BatchId,
		Caller: scope.PrincipalID, Items: items,
	})
	if err != nil {
		h.writeReceivablesError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, receivablesResultWire(out))
}

// receivablesWire validates the body's shape and bound at the boundary:
// the identity fields named and bounded, the list inside the bound,
// every item named, every reference named, the derived key a uuid or
// empty. The record's own rules are the door's (ValidateReceivables
// Items) and answer naming the item and the field; these shape checks
// precede them only because a shapeless item cannot be named.
func receivablesWire(req apigen.LoadReceivablesUpsertRequest) ([]Item, error) {
	if req.SourceSystem == "" || len(req.SourceSystem) > 64 {
		return nil, ErrMissingField
	}
	if req.BatchId == "" || len(req.BatchId) > 128 {
		return nil, ErrMissingField
	}
	if len(req.Items) == 0 || len(req.Items) > MaxBatchItems {
		return nil, ErrBatchBound
	}
	items := make([]Item, 0, len(req.Items))
	for _, it := range req.Items {
		if it.LegacyId == "" || len(it.LegacyId) > MaxKeyLength {
			return nil, ErrMissingField
		}
		derived := ""
		if it.DerivedKey != nil {
			derived = *it.DerivedKey
		}
		if !validDerivedKey(derived) {
			return nil, ErrMissingField
		}
		refs := make([]Ref, 0, len(it.Requires))
		for _, rr := range it.Requires {
			if rr.Kind == "" || len(rr.Kind) > 64 || rr.LegacyId == "" || len(rr.LegacyId) > MaxKeyLength {
				return nil, ErrMissingField
			}
			refs = append(refs, Ref{Kind: rr.Kind, LegacyID: rr.LegacyId})
		}
		items = append(items, Item{LegacyID: it.LegacyId, DerivedKey: derived, Record: it.Record, Requires: refs})
	}
	return items, nil
}

// receivablesErrorAnswer maps the door's errors onto the contract's
// statuses: the boundary's item errors and the frame's field errors are
// 400, the refused batch is 422, the divergent replay and the other
// door's batch id are 409 (the caller's next move a fresh submission),
// and the contended batch is the retryable 503. Everything else is the
// server's own 500.
func receivablesErrorAnswer(err error) (int, string) {
	var item *ItemError
	if errors.As(err, &item) {
		return http.StatusBadRequest, item.Error()
	}
	var ref *BatchRefusal
	if errors.As(err, &ref) {
		if ref.Refusing.Reason == ReasonReplayChanged {
			return http.StatusConflict, "the replayed batch id carries a changed item; submit the correction under a new batch id"
		}
		return http.StatusUnprocessableEntity, "the batch refused whole: " + ref.Refusing.Detail
	}
	switch {
	case errors.Is(err, ErrBatchContended):
		// LD-4's mapping, one place for the retryable answer's words.
		return submitErrorStatus(err)
	case errors.Is(err, ErrBatchDoorMismatch):
		return submitErrorStatus(err)
	case errors.Is(err, ErrMissingField), errors.Is(err, ErrBatchBound), errors.Is(err, ErrBatchActor):
		return http.StatusBadRequest, err.Error()
	default:
		return http.StatusInternalServerError, "the batch failed"
	}
}

// writeReceivablesError answers one error with its status and, where
// the error names an item, the item's key and reason beside the
// message: keys and fields only, never a record value.
func (h *Handler) writeReceivablesError(w http.ResponseWriter, err error) {
	status, msg := receivablesErrorAnswer(err)
	if status == http.StatusServiceUnavailable {
		// The retryable answer, the same the other load doors give: the
		// batch rolled back whole and its id was never recorded.
		w.Header().Set("Retry-After", "1")
	}
	if status == http.StatusInternalServerError {
		h.log.Error("load: receivables batch failed", "err", err)
		httpx.Error(w, status, msg)
		return
	}
	extra := map[string]any{}
	var item *ItemError
	if errors.As(err, &item) {
		extra["legacy_id"] = item.LegacyID
		extra["reason"] = item.Code
		extra["field"] = item.Field
		extra["detail"] = item.Detail
	}
	var ref *BatchRefusal
	if errors.As(err, &ref) {
		extra["legacy_id"] = ref.Refusing.LegacyID
		extra["reason"] = ref.Refusing.Reason
		extra["field"] = ref.Refusing.Field
		extra["detail"] = ref.Refusing.Detail
	}
	httpx.ErrorWith(w, status, msg, extra)
}

// receivablesResultWire maps the batch's answer onto the contract
// shape, the per item outcomes in the wire's own order.
func receivablesResultWire(r BatchResult) apigen.LoadReceivablesResult {
	items := make([]apigen.LoadBatchItemOutcome, 0, len(r.Items))
	for _, o := range r.Items {
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
		items = append(items, item)
	}
	return apigen.LoadReceivablesResult{
		BatchId: r.BatchID,
		Counts: apigen.LoadBatchCounts{
			Created:   int32(r.Counts[OutcomeCreated]),
			Updated:   int32(r.Counts[OutcomeUpdated]),
			Unchanged: int32(r.Counts[OutcomeUnchanged]),
			Refused:   int32(r.Counts[OutcomeRefused]),
		},
		Items: items,
	}
}
