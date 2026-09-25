// syncscope.go is the purchase_orders sync adapter (HH-3.5 work item
// #168): the purchasing module's implementation of the sync core's
// Store seam over purchasing_pos, the purchase order this module owns
// (the contract's /purchasing/pos surface; the 0011 purchase_orders
// table is the receiving-document shape whose writer rode to HH-5 as
// D46, not this scope's entity). The pattern is the pilot's and the
// drive-run adapters': the sync core owns the protocol and no table,
// this adapter touches only tables this module owns, and the rules it
// enforces are this module's own (the lifecycle matrix in
// lifecycle.go, the vendor gate, the finished-order refusals the
// expected-date and delivery-method routes carry).
//
// The scope's policy is server_wins (the confirmed table): a desk
// transition that lands while the buyer's device was offline is the
// outcome the device is told to show.
//
// THE WRITE WINDOWS, the module's own vocabulary:
//
//	create   a DRAFT order: the vendor (validated through the
//	         module's own VendorDirectory seam, an inactive vendor
//	         refuses exactly as CreatePO refuses), the branch (fenced
//	         to the caller's org), the expected date, the notes. Lines
//	         are the desk's own editor; a header-only DRAFT is the
//	         module's own legal shape.
//	update   the two correctable fields the module exposes on a live
//	         order (expected_at, delivery_method, each with the
//	         finished-status refusal its route carries) and the
//	         operator moves the matrix admits (ORDERED, CANCELLED,
//	         CLOSED through CheckTransition, never the receipt-driven
//	         states).
//	delete   the withdrawal: CANCELLED through the matrix plus the
//	         tombstone.
package purchasing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/readcache"
)

// poSyncScope is the per-scope sequence counter (scope_sequences, the
// 0213 convention) every purchasing_pos write draws from.
const poSyncScope = "purchase_orders"

// poSeqCTE is the data-modifying CTE the module's single-statement
// write paths prepend, the same shape every module's writers use.
const poSeqCTE = `WITH poseq AS (
	INSERT INTO scope_sequences (scope) VALUES ('purchase_orders')
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
	RETURNING next_value
) `

// poRefused wraps one of the module's invariant refusals in the Store
// seam's classified marker.
func poRefused(err error) error {
	return fmt.Errorf("%w: %w", sync.ErrOpRefused, err)
}

// allocatePOSeqTx draws the next purchase_orders seq inside the
// caller's transaction.
func allocatePOSeqTx(ctx context.Context, tx pgx.Tx) (int64, error) {
	var next int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO scope_sequences (scope) VALUES ($1)
		 ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
		 RETURNING next_value`, poSyncScope).Scan(&next); err != nil {
		return 0, fmt.Errorf("purchasing: seq allocate: %w", err)
	}
	return next, nil
}

// syncPOColumns is the pull projection: the module's poCols plus the
// line count the list view carries (a count of zero for a PO with
// lines would be a confident wrong number) plus the sync legs. Every
// leg carries the p. alias: the pull reads FROM purchasing_pos p and
// the INSERT/UPDATE arms alias their target AS p, so one list serves
// both (Postgres accepts an alias on the target of INSERT and UPDATE
// exactly for RETURNING).
const syncPOColumns = poCols + `,
	(SELECT count(*) FROM purchasing_po_lines l WHERE l.po_id = p.id)::int,
		p.seq, (p.deleted_at IS NOT NULL) AS deleted`

// syncPORow is one scanned row of the sync projection.
type syncPORow struct {
	po      PO
	seq     int64
	deleted bool
}

// scanSyncPO reads the sixteen targets: scanPO's thirteen, the line
// count, then the two sync legs.
func scanSyncPO(row pgx.Row) (syncPORow, error) {
	var p PO
	var seq int64
	var deleted bool
	if err := row.Scan(&p.ID, &p.PONo, &p.VendorID, &p.BranchID, &p.Status,
		&p.ExpectedAt, &p.OrderedAt, &p.Notes, &p.CreatedAt, &p.UpdatedAt, &p.Currency, &p.FXRate,
		&p.DeliveryMethod, &p.LineCount, &seq, &deleted); err != nil {
		return syncPORow{}, err
	}
	return syncPORow{po: p, seq: seq, deleted: deleted}, nil
}

// poData projects one row through its json tags into the wire map the
// contract's SyncEntity.data carries. The money legs are json:"-"
// behind the module's cost gate, so a synced PO carries no money at
// all: the gate holds on the sync surface exactly as it holds on the
// REST surface, fail closed for everybody rather than open for a
// device.
func poData(p PO) (map[string]any, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("purchasing: sync projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("purchasing: sync projection decode: %w", err)
	}
	return data, nil
}

// poStateOf folds one scanned row into the engine's RowState; a
// tombstone carries its seq and no projection.
func poStateOf(r syncPORow) (sync.RowState, error) {
	rs := sync.RowState{ID: r.po.ID, Seq: r.seq, Deleted: r.deleted}
	if !r.deleted {
		data, err := poData(r.po)
		if err != nil {
			return sync.RowState{}, err
		}
		rs.Data = data
	}
	return rs, nil
}

// poScopePredicate is the WHERE predicate every adapter query runs
// under; purchase_orders is an org scope, so a tenant filter fails
// closed. purchasing_pos carries no org column (0213 stamped none on
// it), so the fence is the branch's, the same read every module fence
// of this shape makes.
func poScopePredicate(f sync.ScopeFilter) (string, []any, bool) {
	if f.Tenant || f.OrgID == "" {
		return "", nil, false
	}
	return `p.branch_id IN (SELECT id FROM locations WHERE org_id = $1::uuid)`, []any{f.OrgID}, true
}

// SyncStore is the purchase_orders Store.
type SyncStore struct {
	pool   *pgxpool.Pool
	vendor VendorDirectory
	link   identity.UserIDLinker
}

// NewSyncStore wires the adapter: the vendor seam is the module's own
// (the create gate), the linker resolves the audit actor.
func NewSyncStore(pool *pgxpool.Pool, vendor VendorDirectory, link identity.UserIDLinker) *SyncStore {
	return &SyncStore{pool: pool, vendor: vendor, link: link}
}

// Pull returns the page of PO changes past since, live rows and
// tombstones in seq order, at most limit rows total, under the org
// fence.
func (s *SyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	where, args, ok := poScopePredicate(f)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM purchasing_pos p
		WHERE %s AND p.seq IS NOT NULL AND p.seq > $2
		ORDER BY p.seq ASC
		LIMIT $3`, syncPOColumns, where)
	args = append(args, since, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("purchasing: sync pull: %w", err)
	}
	defer rows.Close()
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	for rows.Next() {
		r, err := scanSyncPO(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("purchasing: sync pull scan: %w", err)
		}
		if r.deleted {
			tombstones = append(tombstones, sync.Tombstone{ID: r.po.ID, Seq: r.seq})
			continue
		}
		data, err := poData(r.po)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, sync.Entity{ID: r.po.ID, Seq: r.seq, Data: data})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("purchasing: sync pull: %w", err)
	}
	return items, tombstones, nil
}

// FindApplied resolves one idempotency key to the row it applied: the
// D30 column's latest key, kept through a tombstone.
func (s *SyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	where, args, ok := poScopePredicate(f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM purchasing_pos p
		WHERE %s AND p.idempotency_key = $2::uuid
		LIMIT 1`, syncPOColumns, where)
	args = append(args, key)
	r, err := scanSyncPO(s.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("purchasing: sync find applied: %w", err)
	}
	rs, err := poStateOf(r)
	return rs, true, err
}

// Load reads one PO by id under the org fence; a PO outside the
// caller's org is the same as an absent one.
func (s *SyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	where, args, ok := poScopePredicate(f)
	if !ok || !looksLikeUUID(id) {
		return sync.RowState{}, false, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM purchasing_pos p WHERE p.id = $2::uuid AND %s`,
		syncPOColumns, where)
	args = append(args, id)
	r, err := scanSyncPO(s.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("purchasing: sync load: %w", err)
	}
	rs, err := poStateOf(r)
	return rs, true, err
}

// looksLikeUUID reports whether s is a canonical 36-char uuid (the
// boundary guard every module shares, restated local to this package).
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

// poCreateFields is the field set a pushed create may declare.
func poCreateFields(fields map[string]any) error {
	for k := range fields {
		switch k {
		case "id", "vendor_id", "branch_id", "expected_at", "notes":
		default:
			return fmt.Errorf("purchasing: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	return nil
}

// poActor resolves the ERP user a push attributes its write to.
func (s *SyncStore) poActor(ctx context.Context, f sync.ScopeFilter) string {
	if s.link == nil || f.Actor == "" {
		return ""
	}
	if uid, ok := s.link(ctx, f.Actor); ok {
		return uid
	}
	return ""
}

// Create inserts one DRAFT purchase order from a pushed create: the
// branch fences to the caller's org, the vendor validates through the
// module's own seam (an inactive vendor refuses with CreatePO's own
// sentence), the number mints from the module's own next_po_no, and
// the row stamps a fresh seq and the idempotency key.
func (s *SyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := poScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := poCreateFields(fields); err != nil {
		return sync.RowState{}, err
	}
	vendorID, _ := fields["vendor_id"].(string)
	if !looksLikeUUID(vendorID) {
		return sync.RowState{}, fmt.Errorf("purchasing: vendor_id must be a uuid: %w", sync.ErrOpRefused)
	}
	branchID, _ := fields["branch_id"].(string)
	if !looksLikeUUID(branchID) {
		return sync.RowState{}, fmt.Errorf("purchasing: branch_id must be a uuid: %w", sync.ErrOpRefused)
	}
	var inOrg bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM locations WHERE id = $1::uuid AND org_id = $2::uuid)`,
		branchID, f.OrgID).Scan(&inOrg); err != nil {
		return sync.RowState{}, fmt.Errorf("purchasing: sync create branch read: %w", err)
	}
	if !inOrg {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if s.vendor == nil {
		return sync.RowState{}, fmt.Errorf("purchasing: vendor seam not configured: %w", sync.ErrOpRefused)
	}
	v, err := s.vendor.Vendor(ctx, vendorID)
	if err != nil {
		// A missing vendor and a foreign one answer the same
		// not-found, the posture every fence keeps.
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if !v.IsActive {
		return sync.RowState{}, poRefused(fmt.Errorf("vendor %s is not active", v.VendorNo))
	}
	expectedAt, _ := fields["expected_at"].(string)
	if expectedAt != "" {
		if _, err := time.Parse("2006-01-02", expectedAt); err != nil {
			return sync.RowState{}, fmt.Errorf("purchasing: expected_at must be YYYY-MM-DD: %w", sync.ErrOpRefused)
		}
	}
	notes, _ := fields["notes"].(string)
	if len(notes) > 2000 {
		return sync.RowState{}, fmt.Errorf("purchasing: keep the notes under 2000 characters: %w", sync.ErrOpRefused)
	}
	id, hasID := fields["id"].(string)
	if hasID && id != "" && !looksLikeUUID(id) {
		return sync.RowState{}, fmt.Errorf("purchasing: payload id must be a uuid: %w", sync.ErrOpRefused)
	}
	actor := s.poActor(ctx, f)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("purchasing: begin sync create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := allocatePOSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	var poNo string
	if err := tx.QueryRow(ctx, `SELECT next_po_no($1)`, branchID).Scan(&poNo); err != nil {
		return sync.RowState{}, fmt.Errorf("purchasing: sync create mint po_no: %w", err)
	}
	r, err := scanSyncPO(tx.QueryRow(ctx, `
		INSERT INTO purchasing_pos AS p (id, po_no, vendor_id, branch_id, status, expected_at, notes, created_by, seq, idempotency_key)
		VALUES (COALESCE(NULLIF($1,'')::uuid, gen_random_uuid()), $2, $3::uuid, $4::uuid, 'DRAFT',
		        NULLIF($5,'')::date, $6, NULLIF($7,'')::uuid, $8, $9::uuid)
		ON CONFLICT (id) DO NOTHING
		RETURNING `+syncPOColumns,
		id, poNo, vendorID, branchID, expectedAt, notes, actor, seq, key))
	if errors.Is(err, pgx.ErrNoRows) {
		if _, own, _ := s.Load(ctx, f, id); own {
			return sync.RowState{}, sync.ErrRowExists
		}
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("purchasing: sync create insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("purchasing: commit sync create: %w", err)
	}
	return poStateOf(r)
}

// poWriteFields validates a pushed update's declared legs and returns
// them: the two correctable facts and the operator move.
func poWriteFields(fields map[string]any) (expectedAt, deliveryMethod, status *string, err error) {
	for k, v := range fields {
		str, isStr := v.(string)
		switch k {
		case "id":
			continue
		case "expected_at", "delivery_method", "status":
			if !isStr {
				return nil, nil, nil, fmt.Errorf("purchasing: %s must be a string: %w", k, sync.ErrOpRefused)
			}
		default:
			return nil, nil, nil, fmt.Errorf("purchasing: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
		switch k {
		case "expected_at":
			expectedAt = &str
		case "delivery_method":
			deliveryMethod = &str
		case "status":
			status = &str
		}
	}
	if expectedAt == nil && deliveryMethod == nil && status == nil {
		return nil, nil, nil, poRefused(fmt.Errorf("an update that moves nothing refuses"))
	}
	return expectedAt, deliveryMethod, status, nil
}

// poFinished reports the statuses the module's own correction routes
// refuse (the delivery-method route's own set).
func poFinished(status string) bool {
	switch status {
	case StatusReceived, StatusClosed, StatusCancelled:
		return true
	}
	return false
}

// Write applies exactly the declared fields to one PO under the
// module's own rules, on a FOR UPDATE-locked pre-image: the correctable
// facts refuse on a finished order exactly as their routes refuse, and
// the operator move runs the module's own CheckTransition on the
// row's live receipt state (PARTIAL and RECEIVED are reached by
// receiving goods, never by a status change).
func (s *SyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := poScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	expectedAt, deliveryMethod, status, err := poWriteFields(fields)
	if err != nil {
		return sync.RowState{}, err
	}
	if expectedAt != nil && strings.TrimSpace(*expectedAt) != "" {
		if _, err := time.Parse("2006-01-02", strings.TrimSpace(*expectedAt)); err != nil {
			return sync.RowState{}, fmt.Errorf("purchasing: expected_at must be YYYY-MM-DD: %w", sync.ErrOpRefused)
		}
	}
	if deliveryMethod != nil {
		switch strings.ToUpper(*deliveryMethod) {
		case DeliveryPickup, DeliveryDelivered, DeliveryUnknown:
		default:
			return sync.RowState{}, fmt.Errorf("purchasing: delivery_method must be PICKUP, DELIVERY or UNKNOWN: %w", sync.ErrOpRefused)
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("purchasing: begin sync write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var from string
	if err := tx.QueryRow(ctx,
		`SELECT status FROM purchasing_pos
		 WHERE id = $1::uuid AND deleted_at IS NULL
		   AND branch_id IN (SELECT id FROM locations WHERE org_id = $2::uuid)
		 FOR UPDATE`,
		id, f.OrgID).Scan(&from); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		return sync.RowState{}, fmt.Errorf("purchasing: lock %q for sync write: %w", id, err)
	}
	if (expectedAt != nil || deliveryMethod != nil) && poFinished(from) {
		return sync.RowState{}, poRefused(fmt.Errorf("a %s purchase order carries no corrections, its facts are the record", from))
	}
	// The value-equal arm, the attachments lane's replay posture for a
	// free-field scope the frozen schema gives no applied-key history:
	// a superseded replay of one constant correction (the same expected
	// date, the same arrival) answers applied with the standing row and
	// stamps nothing, so a replay cannot churn the feed. A status move
	// never reaches this arm: the matrix refuses from == to first.
	if status == nil {
		var standingDate, standingMethod string
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(to_char(expected_at,'YYYY-MM-DD'),''), delivery_method::text
			 FROM purchasing_pos WHERE id = $1::uuid`, id).Scan(&standingDate, &standingMethod); err != nil {
			return sync.RowState{}, fmt.Errorf("purchasing: sync write standing read: %w", err)
		}
		dateEqual := expectedAt == nil || strings.TrimSpace(*expectedAt) == standingDate
		methodEqual := deliveryMethod == nil ||
			strings.EqualFold(strings.TrimSpace(*deliveryMethod), standingMethod)
		if dateEqual && methodEqual {
			r, err := scanSyncPO(tx.QueryRow(ctx,
				`SELECT `+syncPOColumns+` FROM purchasing_pos p WHERE p.id = $1::uuid`, id))
			if err != nil {
				return sync.RowState{}, fmt.Errorf("purchasing: sync write read back: %w", err)
			}
			if err := tx.Commit(ctx); err != nil {
				return sync.RowState{}, fmt.Errorf("purchasing: commit sync write: %w", err)
			}
			return poStateOf(r)
		}
	}
	if status != nil {
		to := strings.ToUpper(strings.TrimSpace(*status))
		if !KnownStatus(to) {
			return sync.RowState{}, fmt.Errorf("purchasing: %q is not a purchase-order status: %w", to, sync.ErrOpRefused)
		}
		status = &to
		var anyReceived, allReceived = false, false
		var lineCount int
		if err := tx.QueryRow(ctx, `
			SELECT count(*),
			       COALESCE(BOOL_OR(qty_received_base > 0), false),
			       COALESCE(BOOL_AND(qty_received_base >= qty_ordered_base), true)
			  FROM purchasing_po_lines WHERE po_id = $1::uuid`, id).Scan(&lineCount, &anyReceived, &allReceived); err != nil {
			return sync.RowState{}, fmt.Errorf("purchasing: sync write lines read: %w", err)
		}
		if err := CheckTransition(Transition{
			From: from, To: to, AnyReceived: anyReceived, HasLines: lineCount > 0, AllReceived: allReceived,
		}); err != nil {
			return sync.RowState{}, poRefused(err)
		}
	}
	seq, err := allocatePOSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanSyncPO(tx.QueryRow(ctx, `
		UPDATE purchasing_pos AS p SET
			expected_at     = CASE WHEN $2::text IS NULL THEN expected_at ELSE NULLIF($2,'')::date END,
			delivery_method = COALESCE($3, delivery_method),
			status          = COALESCE($4, status),
			ordered_at      = CASE WHEN $4 = 'ORDERED' THEN now() ELSE ordered_at END,
			closed_at       = CASE WHEN $4 IN ('CLOSED','CANCELLED') THEN now() ELSE closed_at END,
			updated_at      = now(),
			seq             = $5,
			idempotency_key = $6::uuid
		WHERE p.id = $1::uuid AND p.deleted_at IS NULL AND p.status = $7
		RETURNING `+syncPOColumns,
		id, expectedAt, deliveryMethod, status, seq, key, from))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("purchasing: sync write %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("purchasing: commit sync write: %w", err)
	}
	return poStateOf(r)
}

// Delete tombstones one PO and cancels it: the withdrawal through the
// module's own matrix (a PO that has received goods closes, it never
// cancels), deleted_at set, the row retained, a fresh seq.
func (s *SyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	if _, _, ok := poScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	_ = key
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("purchasing: begin sync delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var from string
	var anyReceived bool
	if err := tx.QueryRow(ctx, `
		SELECT p.status, COALESCE((SELECT BOOL_OR(l.qty_received_base > 0)
		                             FROM purchasing_po_lines l WHERE l.po_id = p.id), false)
		  FROM purchasing_pos p
		 WHERE p.id = $1::uuid AND p.deleted_at IS NULL
		   AND p.branch_id IN (SELECT id FROM locations WHERE org_id = $2::uuid)
		 FOR UPDATE OF p`, id, f.OrgID).Scan(&from, &anyReceived); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		return sync.RowState{}, fmt.Errorf("purchasing: lock %q for sync delete: %w", id, err)
	}
	if err := CheckTransition(Transition{
		From: from, To: StatusCancelled, AnyReceived: anyReceived, HasLines: true,
	}); err != nil {
		return sync.RowState{}, poRefused(err)
	}
	seq, err := allocatePOSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	var deleted bool
	err = tx.QueryRow(ctx, `
		UPDATE purchasing_pos AS p SET
			status     = 'CANCELLED',
			closed_at  = now(),
			deleted_at = now(),
			updated_at = now(),
			seq        = $2
		WHERE p.id = $1::uuid AND p.deleted_at IS NULL AND p.status = $3
		RETURNING (p.deleted_at IS NOT NULL)`, id, seq, from).Scan(&deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("purchasing: sync delete %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("purchasing: commit sync delete: %w", err)
	}
	return sync.RowState{ID: id, Seq: seq, Deleted: true}, nil
}

// ----------------------------------------------------------------

// vendorsColumns is the sync projection: the identification legs plus
// the sync legs.
const vendorsColumns = `id::text, vendor_no::text, name, payment_terms_days, is_edi,
	is_active, created_at, updated_at,
	seq, (deleted_at IS NOT NULL) AS deleted`

// vendorRow is the wire shape of one cached vendor row.
type vendorRow struct {
	ID               string    `json:"id"`
	VendorNo         string    `json:"vendor_no"`
	Name             string    `json:"name"`
	PaymentTermsDays int       `json:"payment_terms_days"`
	IsEDI            bool      `json:"is_edi"`
	IsActive         bool      `json:"is_active"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// scanVendorRow reads the 9-target row and projects it.
func scanVendorRow(row readcache.Scanner) (string, int64, bool, map[string]any, error) {
	var r vendorRow
	var seq int64
	var deleted bool
	if err := row.Scan(&r.ID, &r.VendorNo, &r.Name, &r.PaymentTermsDays, &r.IsEDI,
		&r.IsActive, &r.CreatedAt, &r.UpdatedAt, &seq, &deleted); err != nil {
		return "", 0, false, nil, err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return "", 0, false, nil, fmt.Errorf("purchasing: sync projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", 0, false, nil, fmt.Errorf("purchasing: sync projection decode: %w", err)
	}
	return r.ID, seq, deleted, data, nil
}

// vendorsPredicate is the org predicate over the module's own table.
func vendorsPredicate(f sync.ScopeFilter) ([]any, bool) {
	if f.Tenant || f.OrgID == "" {
		return nil, false
	}
	return []any{f.OrgID}, true
}

// NewVendorsStore builds the vendors Store.
func NewVendorsStore(pool *pgxpool.Pool) *readcache.Store {
	return readcache.New(pool, readcache.Query{
		Page: `SELECT ` + vendorsColumns + ` FROM vendors
		 WHERE org_id = $1::uuid AND seq IS NOT NULL AND seq > $2
		 ORDER BY seq ASC LIMIT $3`,
		ByID: `SELECT ` + vendorsColumns + ` FROM vendors
		 WHERE org_id = $1::uuid AND id = $2::uuid`,
		Args: vendorsPredicate,
		Scan: scanVendorRow,
	})
}
