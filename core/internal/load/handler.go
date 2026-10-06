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

// maxReceivablesBodyBytes bounds a receivables body: the bound item
// list at the record's full shape, plus framing, with headroom.
const maxReceivablesBodyBytes = 4 << 20

// Handler serves the frame's two documented operations, loadGetBatch and
// loadResolveKeys, both read semantics, and carries the receivables door
// (LD-11) when the wiring binds one: the write door registers only in
// that composition, still behind the machine gate and the deployment's
// switch, and a handler with no door bound answers 404 for its path,
// the same answer an unregistered route carries. The org comes from the
// machine scope the gate stamps on the request (ADR-0024): the gate owns
// X-Org and every refusal for it is the one 403 body, so the handler
// reads no header of its own.
type Handler struct {
	store       *Store
	log         *slog.Logger
	receivables *ReceivablesDoor
}

// NewHandler wires the handler over the frame's store.
func NewHandler(store *Store, log *slog.Logger) *Handler {
	return &Handler{store: store, log: log}
}

// WithReceivables binds the open receivables door (LD-11): the PUT path
// registers only in a composition that carries the door, which is the
// wiring behind the deployment's switch and the machine gate.
func (h *Handler) WithReceivables(d *ReceivablesDoor) *Handler {
	h.receivables = d
	return h
}

// RegisterRoutes registers the operations this composition carries. No
// guard lives in the handler: the routes are machine surface, and the
// machine gate wraps them at the registrar (ADR-0024), which is also
// where the deployment's switch lives; the wiring registers them only
// where the configuration switched the doors on and the machine gate
// rides the registrar.
func (h *Handler) RegisterRoutes(r httpx.Registrar) {
	r.Handle("GET /api/v1/load/batches/{id}", http.HandlerFunc(h.getBatch))
	r.Handle("POST /api/v1/load/keys/resolve", http.HandlerFunc(h.resolveKeys))
	if h.receivables != nil {
		r.Handle("PUT /api/v1/load/receivables", http.HandlerFunc(h.putReceivables))
	}
}

// machineRefused writes the machine leg's one 403 body, byte for byte
// the gate's. A request reaching a handler without the scope the gate
// stamps is a wiring defect; it is answered exactly as the gate answers
// everything else, so the handler adds no answer of its own.
func machineRefused(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write(authz.BodyForbidden)
}

// getBatch answers the recorded batch: the counts by outcome and the per
// item outcomes the batch first recorded. The org is the machine scope's
// own, stamped by the gate, never the raw header: the gate owns X-Org
// and its refusals are the one 403 body, so a malformed or missing value
// never reaches this handler, and nothing here can answer 500 for one.
func (h *Handler) getBatch(w http.ResponseWriter, r *http.Request) {
	scope, ok := authz.MachineScopeFrom(r.Context())
	if !ok {
		machineRefused(w)
		return
	}
	id := r.PathValue("id")
	v, found, err := h.store.GetBatch(r.Context(), scope.OrgID, id)
	switch {
	case err == nil && found:
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
	scope, ok := authz.MachineScopeFrom(r.Context())
	if !ok {
		machineRefused(w)
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
	out, err := h.store.ResolveKeys(r.Context(), scope.OrgID, req.SourceSystem, refs)
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
		if it.Kind == "" || len(it.Kind) > 64 || it.LegacyId == "" || len(it.LegacyId) > MaxKeyLength {
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
		items = append(items, outcomeWire(o))
	}
	return apigen.LoadBatchView{
		BatchId:      v.BatchID,
		SourceSystem: v.SourceSystem,
		Kind:         v.Kind,
		Caller:       v.Caller,
		State:        v.State,
		Counts:       countsWire(v.Counts),
		Items:        items,
		CreatedAt:    v.CreatedAt,
	}
}
