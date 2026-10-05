package load

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// maxResolveBodyBytes bounds a resolve body: the bound list plus
// framing, with headroom. MaxBytesReader refuses an oversized body
// before it is fully buffered.
const maxResolveBodyBytes = 1 << 20

// Handler serves the frame's two documented operations, loadGetBatch and
// loadResolveKeys, both read semantics. The org arrives from X-Org for
// as long as no machine principal exists to resolve it from; the machine
// identity item (LD-2) re-points that resolution when it lands, and its
// gate wraps these registrations.
type Handler struct {
	store *Store
	log   *slog.Logger
}

// NewHandler wires the handler over the frame's store.
func NewHandler(store *Store, log *slog.Logger) *Handler {
	return &Handler{store: store, log: log}
}

// RegisterRoutes registers the two operations. No guard lives here: the
// routes are machine surface, the machine gate is LD-2's, and the wiring
// registers them only where the deployment's configuration switches the
// load doors on.
func (h *Handler) RegisterRoutes(r httpx.Registrar) {
	r.Handle("GET /api/v1/load/batches/{id}", http.HandlerFunc(h.getBatch))
	r.Handle("POST /api/v1/load/keys/resolve", http.HandlerFunc(h.resolveKeys))
}

// getBatch answers the recorded batch: the counts by outcome and the per
// item outcomes the batch first recorded.
func (h *Handler) getBatch(w http.ResponseWriter, r *http.Request) {
	org := r.Header.Get(authz.HeaderXOrg)
	if org == "" {
		httpx.Error(w, http.StatusBadRequest, "the load surface serves one org per call: name it in X-Org")
		return
	}
	id := r.PathValue("id")
	v, ok, err := h.store.GetBatch(r.Context(), org, id)
	switch {
	case err == nil && ok:
		httpx.WriteJSON(w, http.StatusOK, batchViewWire(v))
	case err == nil:
		httpx.Error(w, http.StatusNotFound, "no batch is recorded under that id")
	case errors.Is(err, ErrAmbiguousBatch):
		httpx.Error(w, http.StatusConflict, "the batch id is recorded under more than one source system")
	default:
		h.log.Error("load: batch read failed", "err", err)
		httpx.Error(w, http.StatusInternalServerError, "the batch read failed")
	}
}

// resolveKeys answers one resolution per named key: the HH row the key
// records, null where the name is not yet recorded, with the counts.
func (h *Handler) resolveKeys(w http.ResponseWriter, r *http.Request) {
	org := r.Header.Get(authz.HeaderXOrg)
	if org == "" {
		httpx.Error(w, http.StatusBadRequest, "the load surface serves one org per call: name it in X-Org")
		return
	}
	var req apigen.LoadKeysResolveRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxResolveBodyBytes))
	if err := dec.Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	refs, err := resolveWire(req)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := h.store.ResolveKeys(r.Context(), org, req.SourceSystem, refs)
	if err != nil {
		if errors.Is(err, ErrBatchBound) || errors.Is(err, ErrMissingField) {
			httpx.Error(w, http.StatusBadRequest, err.Error())
			return
		}
		h.log.Error("load: key read failed", "err", err)
		httpx.Error(w, http.StatusInternalServerError, "the key read failed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resolveResultWire(out))
}

// resolveWire validates the resolve body's shape and bound at the
// boundary: the source system named, the list inside the bound, every
// item named. The store defends the same rules.
func resolveWire(req apigen.LoadKeysResolveRequest) ([]Ref, error) {
	if req.SourceSystem == "" || len(req.SourceSystem) > 64 {
		return nil, ErrMissingField
	}
	if len(req.Items) == 0 || len(req.Items) > MaxBatchItems {
		return nil, ErrBatchBound
	}
	refs := make([]Ref, 0, len(req.Items))
	for _, it := range req.Items {
		if it.Kind == "" || it.LegacyId == "" || len(it.LegacyId) > MaxKeyLength {
			return nil, ErrMissingField
		}
		refs = append(refs, Ref{Kind: it.Kind, LegacyID: it.LegacyId})
	}
	return refs, nil
}

// resolveResultWire maps the read's answer onto the contract shape: an
// unrecorded name answers a null hh id, never an empty string.
func resolveResultWire(out ResolveResult) apigen.LoadKeysResolveResult {
	items := make([]apigen.LoadKeyResolution, 0, len(out.Items))
	for _, it := range out.Items {
		var hh *string
		if it.HHID != "" {
			id := it.HHID
			hh = &id
		}
		items = append(items, apigen.LoadKeyResolution{Kind: it.Kind, LegacyId: it.LegacyID, HhId: hh})
	}
	return apigen.LoadKeysResolveResult{
		Items: items,
		Counts: apigen.LoadKeysResolveCounts{
			Found:   int32(out.Counts["found"]),
			Missing: int32(out.Counts["missing"]),
		},
	}
}

// batchViewWire maps the batch record onto the contract shape, refused
// outcomes carrying their reason, field and detail verbatim: keys and
// fields only, never a record value.
func batchViewWire(v BatchView) apigen.LoadBatchView {
	items := make([]apigen.LoadBatchItemOutcome, 0, len(v.Items))
	for _, o := range v.Items {
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
	return apigen.LoadBatchView{
		BatchId:      v.BatchID,
		SourceSystem: v.SourceSystem,
		Kind:         v.Kind,
		Caller:       v.Caller,
		State:        v.State,
		Counts: apigen.LoadBatchCounts{
			Created:   int32(v.Counts[OutcomeCreated]),
			Updated:   int32(v.Counts[OutcomeUpdated]),
			Unchanged: int32(v.Counts[OutcomeUnchanged]),
			Refused:   int32(v.Counts[OutcomeRefused]),
		},
		Items:     items,
		CreatedAt: v.CreatedAt,
	}
}
