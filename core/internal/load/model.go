// Package load is the load frame (HH-LOAD-1 item LD-1): the key
// registry, the load batch record, and the one keyed upsert helper every
// load door uses. The package owns exactly two tables (load_key_registry
// and load_batch, migration 0320); every row of any other table is
// written by the owning module's writer, handed over by constructor as
// the Writer seam, inside a transaction this package opens and the
// writer's statements join (the module boundary rules, AGENTS.md).
//
// The name rule (the plan's section 2): every record that enters HH
// through a load door is named by four parts, the org, the source
// system, the kind, and the legacy id. A name maps to exactly one HH
// row and an HH row carries at most one name per source system and
// kind; both directions are unique in load_key_registry, and the
// registry insert is the lock two racing loads contend on.
//
// No machine gate, no audit row, and no wrong caller test live here:
// those are the machine identity item's (LD-2), in parallel. On this
// round the helper is proven with a test writer; the real doors that
// use it arrive from the reference doors onward.
package load

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// The five outcomes, one per item. The attached word belongs to the map
// doors (the branch map and the user map, HH-LOAD-1 LD-4): they record
// names against rows that already exist and create nothing, so an item
// whose name was not yet recorded and now binds to the pre existing row
// answers attached; a replay of an already recorded name answers
// unchanged or updated as usual. Every other door answers created on the
// create arm.
const (
	OutcomeCreated   = "created"
	OutcomeAttached  = "attached"
	OutcomeUpdated   = "updated"
	OutcomeUnchanged = "unchanged"
	OutcomeRefused   = "refused"
)

// The reason codes this frame refuses with. A writer may carry its own
// codes through Refusal; these are the frame's.
const (
	// ReasonMissingReference: a reference to a name not yet loaded.
	// The refusal names the kind and the key (never a value).
	ReasonMissingReference = "missing_reference"
	// ReasonKeyConflict: a name that would claim a second row, or a
	// row a second name already claims. The refusal names both legacy
	// ids; nothing is merged silently.
	ReasonKeyConflict = "key_conflict"
	// ReasonUpdateTargetMissing: the update arm's row is gone from the
	// owning table; the write refuses rather than creating a second row.
	ReasonUpdateTargetMissing = "update_target_missing"
	// ReasonRecordShape: the record cannot be normalised to a hash.
	ReasonRecordShape = "record_shape"
)

// BatchStateRecorded is the one state this round records: the batch row
// lands when the whole batch has been processed. A door that needs an
// in-flight state adds it in its own migration.
const BatchStateRecorded = "RECORDED"

const (
	// MaxBatchItems is the batch bound: a list over it refuses with 400
	// at the boundary, before any store call. The key resolution's list
	// rides the same bound.
	MaxBatchItems = 500
	// MaxKeyLength is the legacy id's bound, the same bound the 0320
	// check carries.
	MaxKeyLength = 512
)

// ErrBatchBound is the batch bound's sentinel: the handler maps it to
// 400.
var ErrBatchBound = errors.New("load: the item list is empty or over the bound")

// ErrMissingField is a required field's sentinel: the handler maps it to
// 400.
var ErrMissingField = errors.New("load: a required field is empty or over its bound")

// ErrAmbiguousBatch is the batch read's sentinel: the caller's batch id
// is recorded under more than one source system in this org.
var ErrAmbiguousBatch = errors.New("load: the batch id is recorded under more than one source system")

// ErrBatchDoorMismatch is the replay's sentinel: the caller's batch id
// is recorded under a different door's kind, so the submission is not a
// replay but a collision of two doors' batch namespaces. The boundary
// answers it with 409.
var ErrBatchDoorMismatch = errors.New("load: the batch id is recorded under another door")

// ErrBatchActor is the audit actor's guard: the batch names no machine
// principal as its caller, so there is no actor the one audit row per
// batch could carry. The batch refuses loudly before anything runs; an
// audit row with a blank actor is a silent failure the platform cannot
// answer for.
var ErrBatchActor = errors.New("load: the batch names no machine principal as its caller")

// ErrBatchContended is the deadlock's defined answer: the batch shared
// names with another in-flight batch and Postgres rolled this one back
// whole as the victim (SQLSTATE 40P01). Nothing landed, the claim and
// every item rolled back together, and resubmission is the caller's
// answer: the resubmission replays the winner's record or re-runs to
// convergence, the same rules every submission follows.
var ErrBatchContended = errors.New("load: the batch contended with another batch on shared names and rolled back whole")

// Ref is a reference named by legacy key: the kind and the key, resolved
// against the registry before any write.
type Ref struct {
	Kind     string
	LegacyID string
}

// RefMatch is one resolved reference: what the key names.
type RefMatch struct {
	Kind     string
	LegacyID string
	HHID     string
}

// Item is one item of a load batch: the legacy key, the caller's derived
// key (the name-based uuid the row's own idempotency_key column carries
// where 0213 gave the table one), the record as a decoded JSON object,
// and the references the record names by legacy key.
type Item struct {
	LegacyID   string
	DerivedKey string
	Record     map[string]any
	Requires   []Ref
}

// Batch is one submitted load batch: the org, the source system, the
// caller's batch id, the caller of record (the machine principal id, the
// audit row's actor), and the bounded item list.
type Batch struct {
	OrgID        string
	SourceSystem string
	BatchID      string
	Caller       string
	Items        []Item
}

// Outcome is one item's answer. A refusal names the reason code, the
// field, and a detail that carries keys and fields, never a record
// value.
type Outcome struct {
	LegacyID string `json:"legacy_id"`
	Status   string `json:"status"`
	HHID     string `json:"hh_id,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Field    string `json:"field,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// BatchResult is the batch's answer: the counts by outcome and one
// outcome per item, the shape a replayed batch id returns.
type BatchResult struct {
	BatchID string
	Counts  map[string]int
	Items   []Outcome
}

// BatchView is the batch record as the read serves it.
type BatchView struct {
	BatchID      string
	SourceSystem string
	Kind         string
	Caller       string
	State        string
	Counts       map[string]int
	Items        []Outcome
	CreatedAt    time.Time
}

// Resolution is one key read's answer: the HH row the key names, empty
// where the name is not yet recorded.
type Resolution struct {
	Kind     string
	LegacyID string
	HHID     string
}

// ResolveResult is the key read's answer: one resolution per item and
// the found and missing counts.
type ResolveResult struct {
	Items  []Resolution
	Counts map[string]int
}

// Write is what the frame hands the owning module's writer for one row:
// the org, the legacy key as named (the row's natural key where the
// owning table's upsert is by it, the supplier's vendor number), the
// row to update (empty on the create arm), the derived key for the
// row's idempotency column where the table has one, the record, and the
// resolved references.
type Write struct {
	OrgID      string
	LegacyID   string
	RowID      string
	DerivedKey string
	Record     map[string]any
	Refs       []RefMatch
	// Note lets a writer that landed its row but left one field out say
	// so: the frame carries the note's code, field and detail on the
	// item's created or updated outcome. A note names a field and a
	// reason, never a value. Nil on a call that cannot carry one.
	Note func(Refusal)
}

// Writer is the owning module's write path for one loaded table, handed
// over by constructor. Apply inserts or updates exactly one row inside
// the frame's transaction: the create arm when Write.RowID is empty, the
// update arm naming the row otherwise. The write stamps the org, draws
// the table's sync sequence from its scope where the table has one (the
// 0213 allocator, drawn the way the pricing module's syncscope draws
// it), and stamps the row's idempotency key with the derived key where
// the table has one. A refused write comes back as *Refusal; the frame
// turns it into the item's refused outcome and nothing of the write
// stands. The returned id is the row's HH id for the registry.
type Writer interface {
	Apply(ctx context.Context, tx pgx.Tx, in Write) (rowID string, err error)
}

// Refusal is a write's classified no. The detail names fields and keys,
// never a record value.
type Refusal struct {
	Code   string
	Field  string
	Detail string
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("load: refused %s on %s: %s", r.Code, r.Field, r.Detail)
}

// refusedOutcome folds a refusal into the item's outcome.
func refusedOutcome(item Item, r *Refusal) Outcome {
	if r == nil {
		r = &Refusal{Code: "refused", Detail: "the write refused with no reason named"}
	}
	return Outcome{LegacyID: item.LegacyID, Status: OutcomeRefused, Reason: r.Code, Field: r.Field, Detail: r.Detail}
}

// The audit row's vocabulary, the words the platform's audit write path
// carries for the load frame's one-audit-row-per-batch ruling (ADR-0024):
// the machine principal as actor, the counts by outcome, keys and fields
// only, never a record value.
const (
	AuditAction     = "LOAD_BATCH"
	AuditEntityType = "load.batch"
	AuditPurpose    = "LOAD_BATCH_OUTCOME"
)

// BatchAuditRow is one batch's audit fact: the batch row's own uuid (the
// audit row's entity id), the caller's batch id and the door's kind (keys
// only), the machine principal the batch is attributed to, and the counts
// by outcome.
type BatchAuditRow struct {
	BatchUUID    string
	BatchID      string
	Kind         string
	SourceSystem string
	ActorID      string
	Counts       map[string]int
}

// BatchAuditSink is the platform audit write path's seam: the wiring
// hands the door one implementation over the audit service, and the row
// lands on the batch record's own transaction, committing or rolling
// back with it.
type BatchAuditSink interface {
	RecordBatchTx(ctx context.Context, tx pgx.Tx, row BatchAuditRow) error
}
