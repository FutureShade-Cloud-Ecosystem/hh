// imagedoor.go is the load doors' product image door (HH-LOAD-1 LD-5):
// PUT /api/v1/load/products/{legacyId}/image, one put per image, not a
// batch. The bytes land in the product module's own image store through
// the ImageWriter seam, under the module's own rules (the raster
// allowlist, the magic byte sniff and the size cap) carried to the
// boundary by the ImageValidator seam, both taken by constructor. The
// product is resolved by legacy key in the registry before anything
// writes; the image's own name rides the product's legacy key under the
// product_image kind, so the replay decision reads the bytes' hash off
// the registry: an identical replay answers unchanged and writes
// nothing, changed bytes answer updated and replace the primary image.
//
// The door opens one transaction (the registry read, the image write
// and the name's stamp join it, committing or rolling back together),
// carries the same ErrBatchContended answer the batch doors answer when
// Postgres rolls it back whole as a deadlock victim, and writes one
// audit row per put on the same transaction: keys and counts, never a
// byte of the image.
package load

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/jackc/pgx/v5"
)

// imageReadHeadroom is the read bound's headroom over the module's cap:
// a body past the cap must refuse at the boundary naming the field, and
// the headroom is what lets the read finish so the refusal, not a
// transport error, carries the answer.
const imageReadHeadroom = 1 << 10

// ImageValidator is the owning module's own image rules at the door's
// boundary: the declared content type against the raster allowlist, the
// bytes against the declared type, and the size cap. MaxImageBytes
// bounds the request body's read; a violation names the field and
// carries no value. NormaliseContentType is the module's own media type
// normalisation (the parameter strip and the case fold), the one
// spelling the allowlist, the constraint and the replay decision all
// read, so two spellings of one media type are one record.
type ImageValidator interface {
	MaxImageBytes() int
	ValidateImage(contentType string, body []byte) (field, detail string, ok bool)
	NormaliseContentType(contentType string) string
}

// ImageWrite is what the door hands the owning module's writer for one
// image put: the product row the legacy key resolved to, the declared
// type, the alt text if named, the bytes, and the caller of record (the
// machine principal, the store's uploaded_by).
type ImageWrite struct {
	OrgID       string
	LegacyID    string
	ProductID   string
	ContentType string
	AltText     string
	Bytes       []byte
	Caller      string
}

// ImageWriter is the owning module's write path for one product image,
// handed over by constructor. ApplyImage upserts the primary image on
// the door's transaction, opens no connection of its own, commits
// nothing, and wraps its errors with %w so a deadlock stays
// recognisable. A refused write comes back as *Refusal.
type ImageWriter interface {
	ApplyImage(ctx context.Context, tx pgx.Tx, in ImageWrite) error
}

// PutImageRequest is one image put's inputs: the org and source system
// the names live under, the product's legacy key, the caller of record,
// the declared content type, the alt text if named, and the bytes.
type PutImageRequest struct {
	OrgID        string
	SourceSystem string
	LegacyID     string
	Caller       string
	ContentType  string
	AltText      string
	Bytes        []byte
}

// ImageRuleViolation is the boundary's answer to an image the module's
// rules refuse: the handler maps it to 400 with the field named, never
// a value.
type ImageRuleViolation struct {
	Field  string
	Detail string
}

func (v *ImageRuleViolation) Error() string {
	return fmt.Sprintf("load: the image is refused on %s: %s", v.Field, v.Detail)
}

// ImageDoor is the product image door: the frame's store, the module's
// rules and writer, and the audit sink, all by constructor.
type ImageDoor struct {
	store     *Store
	validator ImageValidator
	writer    ImageWriter
	audit     BatchAuditSink
}

// NewImageDoor builds the door. A nil validator, writer or sink is a
// construction the door refuses at the first put: the module's rules
// stand at the boundary, the bytes land through the module's writer,
// and every put is audited, or nothing lands.
func NewImageDoor(store *Store, v ImageValidator, w ImageWriter, a BatchAuditSink) *ImageDoor {
	return &ImageDoor{store: store, validator: v, writer: w, audit: a}
}

// PutImage answers one put: the boundary's rules first, then one
// transaction over the registry read, the module's image write and the
// name's stamp. The answer is one outcome in the batch doors'
// vocabulary (created, updated, unchanged, refused) on the product's
// legacy key.
func (d *ImageDoor) PutImage(ctx context.Context, in PutImageRequest) (Outcome, error) {
	if in.OrgID == "" || in.SourceSystem == "" || in.LegacyID == "" || len(in.LegacyID) > MaxKeyLength {
		return Outcome{}, ErrMissingField
	}
	if !canonicalUUID(in.Caller) {
		return Outcome{}, ErrBatchActor
	}
	if d.validator == nil || d.writer == nil {
		return Outcome{}, errors.New("load: the image door carries no validator or writer")
	}
	if d.audit == nil {
		return Outcome{}, errors.New("load: the image door carries no audit sink; every put is audited")
	}
	// The module's rules stand before any store call: a refused image
	// never reaches the pool.
	if field, detail, ok := d.validator.ValidateImage(in.ContentType, in.Bytes); !ok {
		return Outcome{}, &ImageRuleViolation{Field: field, Detail: detail}
	}
	if d.store == nil || d.store.pool == nil {
		return Outcome{}, errors.New("load: no database")
	}
	// The record is the whole truth: the bytes, the normalised type and
	// the alt text hash together, so a put whose bytes are identical but
	// whose alt text differs answers updated and the stored alt text
	// follows, and two spellings of one media type are one record.
	hash := imageRecordHash(d.validator.NormaliseContentType(in.ContentType),
		strings.TrimSpace(in.AltText), in.Bytes)

	tx, err := d.store.pool.Begin(ctx)
	if err != nil {
		return Outcome{}, fmt.Errorf("load: image begin: %w", err)
	}
	o, err := d.tryImage(ctx, tx, in, hash)
	if err != nil {
		_ = tx.Rollback(ctx)
		if isDeadlock(err) {
			return Outcome{}, fmt.Errorf("%w: %v", ErrBatchContended, err)
		}
		return Outcome{}, err
	}
	return o, nil
}

// imageRecordHash frames the record's three fields with their lengths,
// each field length prefixed and separated, so no spelling of one field
// can masquerade as another, then hashes: the door's replay decision
// reads this off the registry, the record the writer stores.
func imageRecordHash(contentType, altText string, body []byte) string {
	h := sha256.New()
	for _, field := range []string{contentType, altText} {
		fmt.Fprintf(h, "%d:", len(field))
		h.Write([]byte(field))
		h.Write([]byte{0})
	}
	fmt.Fprintf(h, "%d:", len(body))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// tryImage is one attempt on the door's transaction: the product
// resolved by legacy key, the image name's replay decision, the
// module's write, the name's stamp, the audit row, the commit.
func (d *ImageDoor) tryImage(ctx context.Context, tx pgx.Tx, in PutImageRequest, hash string) (Outcome, error) {
	defer func() { _ = tx.Rollback(ctx) }()

	// The product resolves by legacy key before anything writes: a name
	// not yet loaded refuses as a missing reference naming the kind and
	// the key (the plan's section 2 rule 4; the product door loads
	// first).
	productKey := nameKey{OrgID: in.OrgID, SourceSystem: in.SourceSystem, Kind: KindProduct, LegacyID: in.LegacyID}
	productID, _, found, err := readName(ctx, tx, productKey)
	if err != nil {
		return Outcome{}, err
	}
	if !found {
		o := refusedOutcome(Item{LegacyID: in.LegacyID}, &Refusal{Code: ReasonMissingReference, Field: KindProduct,
			Detail: fmt.Sprintf("reference not loaded: kind %s legacy key %s", KindProduct, in.LegacyID)})
		return o, d.auditPut(ctx, tx, in, o, "")
	}
	key := nameKey{OrgID: in.OrgID, SourceSystem: in.SourceSystem, Kind: KindProductImage, LegacyID: in.LegacyID}
	_, curHash, named, err := readName(ctx, tx, key)
	if err != nil {
		return Outcome{}, err
	}
	status := OutcomeCreated
	if named {
		if curHash == hash {
			// The identical replay: the bytes stand, nothing writes.
			o := Outcome{LegacyID: in.LegacyID, Status: OutcomeUnchanged, HHID: productID}
			return o, d.auditPut(ctx, tx, in, o, productID)
		}
		status = OutcomeUpdated
	}
	if err := d.writer.ApplyImage(ctx, tx, ImageWrite{
		OrgID: in.OrgID, LegacyID: in.LegacyID, ProductID: productID,
		ContentType: in.ContentType, AltText: in.AltText, Bytes: in.Bytes, Caller: in.Caller,
	}); err != nil {
		var refused *Refusal
		if errors.As(err, &refused) {
			o := refusedOutcome(Item{LegacyID: in.LegacyID}, refused)
			return o, d.auditPut(ctx, tx, in, o, productID)
		}
		return Outcome{}, err
	}
	if named {
		if err := stampName(ctx, tx, key, hash, ""); err != nil {
			return Outcome{}, err
		}
	} else {
		if err := insertName(ctx, tx, key, productID, hash, ""); err != nil {
			if !isUniqueViolation(err) {
				return Outcome{}, fmt.Errorf("load: image name insert: %w", err)
			}
			// Two puts racing on one product's image: the registry
			// insert is the lock, and the winner committed between this
			// put's read and its insert. Re-read the committed name and
			// converge on it: the same hash answers unchanged (the
			// speculative write's upsert landed the same bytes), a
			// different hash answers updated off this put's write.
			_, curHash, named, err := readName(ctx, tx, key)
			if err != nil {
				return Outcome{}, err
			}
			if !named {
				return Outcome{}, fmt.Errorf("load: image name insert: the name stayed absent past its unique index: %w", err)
			}
			if curHash == hash {
				o := Outcome{LegacyID: in.LegacyID, Status: OutcomeUnchanged, HHID: productID}
				return o, d.auditPut(ctx, tx, in, o, productID)
			}
			if err := stampName(ctx, tx, key, hash, ""); err != nil {
				return Outcome{}, err
			}
			status = OutcomeUpdated
		}
	}
	o := Outcome{LegacyID: in.LegacyID, Status: status, HHID: productID}
	return o, d.auditPut(ctx, tx, in, o, productID)
}

// auditPut writes the put's one audit row and commits the transaction:
// the machine principal as actor, the outcome's count, keys only. A
// refused or unchanged put is audited like a landing one.
func (d *ImageDoor) auditPut(ctx context.Context, tx pgx.Tx, in PutImageRequest, o Outcome, batchUUID string) error {
	counts := map[string]int{OutcomeCreated: 0, OutcomeAttached: 0, OutcomeUpdated: 0, OutcomeUnchanged: 0, OutcomeRefused: 0}
	counts[o.Status]++
	if err := d.audit.RecordBatchTx(ctx, tx, BatchAuditRow{
		BatchUUID:    batchUUID,
		BatchID:      in.LegacyID,
		Kind:         KindProductImage,
		SourceSystem: in.SourceSystem,
		ActorID:      in.Caller,
		Counts:       counts,
	}); err != nil {
		return fmt.Errorf("load: image audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("load: image commit: %w", err)
	}
	return nil
}

// ImageHandler serves the image put. The door arrives by constructor;
// the handler owns no gate: the route is machine surface, worded
// machine in the census, and the machine gate wraps it at the registrar.
type ImageHandler struct {
	door *ImageDoor
	log  *slog.Logger
}

// NewImageHandler wires the handler over the door.
func NewImageHandler(door *ImageDoor, log *slog.Logger) *ImageHandler {
	return &ImageHandler{door: door, log: log}
}

// RegisterRoutes registers the one put. No guard lives in the handler:
// the machine gate wraps the registration at the registrar.
func (h *ImageHandler) RegisterRoutes(r httpx.Registrar) {
	r.Handle("PUT /api/v1/load/products/{legacyId}/image", http.HandlerFunc(h.putImage))
}

// putImage answers one put: the scope, the bounded body read, the
// boundary's rules, and the outcome shape. The source system rides the
// source_system query parameter (there is no JSON body to carry it),
// the alt text the alt query parameter, the bytes the body under its
// declared Content-Type.
func (h *ImageHandler) putImage(w http.ResponseWriter, r *http.Request) {
	scope, ok := authz.MachineScopeFrom(r.Context())
	if !ok {
		machineRefused(w)
		return
	}
	bound := h.door.validator.MaxImageBytes()
	if bound <= 0 {
		h.log.Error("load: the image door carries no body bound")
		httpx.Error(w, http.StatusInternalServerError, "the image put failed")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, int64(bound+imageReadHeadroom)))
	if err != nil || len(body) > bound {
		// The refusal names the field, never a value: no byte of the
		// body appears in the answer.
		httpx.Error(w, http.StatusBadRequest,
			"load: the image is refused on image: the body is over the size cap the module carries")
		return
	}
	altText := r.URL.Query().Get("alt")
	o, err := h.door.PutImage(r.Context(), PutImageRequest{
		OrgID:        scope.OrgID,
		SourceSystem: r.URL.Query().Get("source_system"),
		LegacyID:     r.PathValue("legacyId"),
		Caller:       scope.PrincipalID,
		ContentType:  r.Header.Get("Content-Type"),
		AltText:      altText,
		Bytes:        body,
	})
	if err != nil {
		status, message := imageErrorStatus(err)
		if status == http.StatusServiceUnavailable {
			w.Header().Set("Retry-After", "1")
		}
		if status == http.StatusInternalServerError {
			h.log.Error("load: image put failed", "err", err)
		}
		httpx.Error(w, status, message)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, outcomeWire(o))
}

// imageErrorStatus maps the image door's errors onto the contract's
// answers, the batch doors' mapping: a rule violation is 400 (the field
// named, never a value), the contention sentinel is the retryable 503,
// a missing or malformed field is 400.
func imageErrorStatus(err error) (int, string) {
	var violation *ImageRuleViolation
	switch {
	case errors.As(err, &violation):
		return http.StatusBadRequest, violation.Error()
	case errors.Is(err, ErrBatchContended):
		return http.StatusServiceUnavailable,
			"the put contended with another in flight load and rolled back whole; resubmit it"
	case errors.Is(err, ErrBatchBound), errors.Is(err, ErrMissingField), errors.Is(err, ErrBatchActor):
		return http.StatusBadRequest, err.Error()
	default:
		return http.StatusInternalServerError, "the image put failed"
	}
}
