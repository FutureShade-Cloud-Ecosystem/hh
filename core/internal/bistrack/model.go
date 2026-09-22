package bistrack

import "time"

// model.go, DIB-53 phase 0 the FROZEN data shapes of the BisTrack sync module. Per the GL scaffold
// idiom (internal/gl/module.go): phase 0 freezes types + the connector seam; the W10 worker fills bodies
// and never changes a signature. Nothing here reads a live BisTrack; the module is structurally unable
// to sync (see module.go / the structural-guarantee test).

// EntityType is the set of domains M9 keeps reconciled across the fork. These four are the phase-0
// mapping scope; the string values are the CHECK vocabulary of migration 0079.
type EntityType string

const (
	EntityCustomer  EntityType = "customer"
	EntityProduct   EntityType = "product"
	EntityInventory EntityType = "inventory"
	EntityOrder     EntityType = "order"
)

// AllEntities is the deterministic iteration order used by tests and any future scheduler.
var AllEntities = []EntityType{EntityCustomer, EntityProduct, EntityInventory, EntityOrder}

func (e EntityType) valid() bool {
	switch e {
	case EntityCustomer, EntityProduct, EntityInventory, EntityOrder:
		return true
	default:
		return false
	}
}

// SyncDirection is which way a job moves data. PULL reads BisTrack into HardscapeOS (net-new + the
// initial mirror); PUSH writes native changes back to BisTrack so it stays current during the parallel
// run (the load-bearing half of the "rollback is free until November" guarantee).
type SyncDirection string

const (
	DirectionPull SyncDirection = "pull"
	DirectionPush SyncDirection = "push"
)

// SyncStatus is a job's lifecycle state (0079 CHECK). SKIPPED is a deliberate no-op (e.g. an entity not
// synced in the current migration phase), distinct from FAILED.
type SyncStatus string

const (
	StatusPending   SyncStatus = "PENDING"
	StatusRunning   SyncStatus = "RUNNING"
	StatusSucceeded SyncStatus = "SUCCEEDED"
	StatusFailed    SyncStatus = "FAILED"
	StatusSkipped   SyncStatus = "SKIPPED"
)

// Cursor is a RESUMABLE composite watermark, NOT a bare timestamp (C2). A "max updated_at seen"
// watermark silently skips every row sharing that timestamp tick with the last row consumed, which is
// data loss, not mis-ordering (this program already paid for that class once, DIB-191 audit H1, two
// outbox rows on one tx timestamp with no tiebreaker).
//
// TIE SEMANTICS (frozen now, while there is no data behind the type): a pull fetches rows STRICTLY
// GREATER than the cursor on the LEXICOGRAPHIC pair (UpdatedAt, LastID):
//
//	WHERE (updated_at, bistrack_id) > (Cursor.UpdatedAt, Cursor.LastID)   -- row-value comparison
//	ORDER BY updated_at, bistrack_id
//
// so rows sharing a timestamp are ordered and disambiguated by id and none is dropped. The cursor then
// advances to the (updated_at, bistrack_id) of the LAST row consumed. LastID is the BisTrack id (its
// natural key), stringly-typed because BisTrack keys are ints today but the wire format is not frozen.
// The zero Cursor (zero time, empty id) means "from the beginning", the initial mirror pull.
type Cursor struct {
	UpdatedAt time.Time `json:"updated_at"`
	LastID    string    `json:"last_id"`
}

// IsZero reports the start-from-beginning cursor.
func (c Cursor) IsZero() bool { return c.UpdatedAt.IsZero() && c.LastID == "" }

// Before reports whether c is STRICTLY before other on the composite (UpdatedAt, LastID) order. This is
// the frozen tie semantics (C2), made executable so the W10 worker cannot implement a bare-timestamp
// comparison by accident: a pulled row (updated_at, bistrack_id) is "new" relative to a cursor exactly
// when cursor.Before(rowCursor), and two rows sharing a timestamp are ordered by id, so neither is ever
// skipped. Equal cursors are NOT Before each other (strict), which is why the fetch uses `>` not `>=`.
func (c Cursor) Before(other Cursor) bool {
	if !c.UpdatedAt.Equal(other.UpdatedAt) {
		return c.UpdatedAt.Before(other.UpdatedAt)
	}
	return c.LastID < other.LastID
}

// SyncJob is one attempt to reconcile one entity in one direction (a sync_jobs row).
type SyncJob struct {
	ID            string        `json:"id"`
	EntityType    EntityType    `json:"entity_type"`
	Direction     SyncDirection `json:"direction"`
	Status        SyncStatus    `json:"status"`
	Cursor        Cursor        `json:"cursor"`
	RowsRead      int64         `json:"rows_read"`
	RowsWritten   int64         `json:"rows_written"`
	Discrepancies int64         `json:"discrepancies"`
	Error         string        `json:"error,omitempty"`
	ScheduledAt   *time.Time    `json:"scheduled_at,omitempty"`
	StartedAt     *time.Time    `json:"started_at,omitempty"`
	FinishedAt    *time.Time    `json:"finished_at,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
}

// DiscrepancyKind is what reconciliation could not line up. These are the point of the sync, surfaced,
// never silently dropped, and are the 0079 CHECK vocabulary.
type DiscrepancyKind string

const (
	// KindMissingInNative: a BisTrack row with no native counterpart (a pull would create it).
	KindMissingInNative DiscrepancyKind = "MISSING_IN_NATIVE"
	// KindMissingInBisTrack: a native row with no BisTrack counterpart (a push would create it).
	KindMissingInBisTrack DiscrepancyKind = "MISSING_IN_BISTRACK"
	// KindFieldMismatch: both sides exist but a mapped field disagrees.
	KindFieldMismatch DiscrepancyKind = "FIELD_MISMATCH"
	// KindUnmapped: a BisTrack row the mapping layer cannot resolve to a native shape (no id-map entry,
	// or an excluded/uncharacterized source table).
	KindUnmapped DiscrepancyKind = "UNMAPPED"
	// KindMappingError: a mapping rule was violated (a required field absent, a bad coercion, or a
	// many-to-one crosswalk conflict, the reason bistrack_id_map is unique in both directions).
	KindMappingError DiscrepancyKind = "MAPPING_ERROR"
)

// SyncDiscrepancy is one unreconciled fact (a sync_discrepancies row). Detail carries the field-level
// specifics as an allowlisted map (never raw PII); native_id is empty when there is no native side yet.
type SyncDiscrepancy struct {
	ID         string          `json:"id,omitempty"`
	SyncJobID  string          `json:"sync_job_id,omitempty"`
	EntityType EntityType      `json:"entity_type"`
	BisTrackID string          `json:"bistrack_id,omitempty"`
	NativeID   string          `json:"native_id,omitempty"`
	Kind       DiscrepancyKind `json:"kind"`
	Detail     map[string]any  `json:"detail,omitempty"`
	DetectedAt time.Time       `json:"detected_at"`
	ResolvedAt *time.Time      `json:"resolved_at,omitempty"`
}

// IDMapping is one bistrack_id_map row, the BisTrack id <-> native uuid crosswalk. It is unique in BOTH
// directions (0079): (entity, bistrack_id) makes pull idempotent, (entity, native_id) makes push safe -
// a native entity mapping to two BisTrack ids conflicts rather than silently lying (surfaced as a
// KindMappingError).
type IDMapping struct {
	EntityType EntityType `json:"entity_type"`
	BisTrackID string     `json:"bistrack_id"`
	NativeID   string     `json:"native_id"`
}

// Status is the read-only observability DTO returned by GET /api/v1/bistrack/status (and mirrored into
// healthz). Phase 0 with no credentials: {configured:false, last_sync:null, discrepancies:0}.
type Status struct {
	Configured    bool       `json:"configured"`
	LastSync      *time.Time `json:"last_sync"`
	Discrepancies int64      `json:"discrepancies"`
}
