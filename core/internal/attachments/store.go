// store.go is the attachments module's Store for the sync core
// (HH-3.5 #166): the pull projection with nested links, the write
// primitives the engine drives under last_write_wins_by_field, and the
// desk-facing metadata correction the review surface reads against.
// Every query carries the caller's org predicate (the table's own
// org_id column, NOT NULL by D10); a tenant filter or an empty org
// fails closed and answers nothing.
package attachments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// attachmentsSeqScope names the scope_sequences counter row every
// attachment write draws from (the 0213 convention, one counter row per
// scope).
const attachmentsSeqScope = "attachments"

// attachmentColumns is the sync projection: the Attachment legs plus the
// sync legs (seq, tombstone flag), unqualified so the same constant
// serves the SELECTs and the RETURNING legs alike (the pilot's own
// no-alias discipline).
const attachmentColumns = `id::text, org_id::text, tenant_id::text, kind,
	appwrite_file_id, bucket, COALESCE(mime, ''), size_bytes,
	COALESCE(checksum, ''), COALESCE(created_by, ''), created_at, deleted_at,
	seq, (deleted_at IS NOT NULL) AS deleted`

// attachmentRow is one scanned row: the Attachment legs plus sync legs.
type attachmentRow struct {
	attachment Attachment
	seq        int64
	deleted    bool
}

// rowScanner is the shared scan surface a row or row-set carries (pgx
// cannot split one row across scanners, but Row and Rows share Scan).
type rowScanner interface {
	Scan(dest ...any) error
}

// scanAttachment reads the 14-target row.
func scanAttachment(row rowScanner) (attachmentRow, error) {
	var a Attachment
	var tenantID *string
	var seq int64
	var deleted bool
	if err := row.Scan(
		&a.ID, &a.OrgID, &tenantID, &a.Kind,
		&a.AppwriteFileID, &a.Bucket, &a.Mime, &a.SizeBytes,
		&a.Checksum, &a.CreatedBy, &a.CreatedAt, &a.DeletedAt,
		&seq, &deleted,
	); err != nil {
		return attachmentRow{}, err
	}
	a.TenantID = tenantID
	return attachmentRow{attachment: a, seq: seq, deleted: deleted}, nil
}

// attachmentData projects one Attachment into the wire shape the
// contract pins (a marshal round trip, so omitempty and the nullable
// legs land exactly as the surface writes them).
func attachmentData(a Attachment) (map[string]any, error) {
	raw, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("attachments: sync projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("attachments: sync projection decode: %w", err)
	}
	return data, nil
}

// rowStateOf folds one scanned row into the engine's RowState; a
// tombstone carries its seq and no projection.
func rowStateOf(r attachmentRow) (sync.RowState, error) {
	rs := sync.RowState{ID: r.attachment.ID, Seq: r.seq, Deleted: r.deleted}
	if !r.deleted {
		r.attachment.Links = []Link{}
		data, err := attachmentData(r.attachment)
		if err != nil {
			return sync.RowState{}, err
		}
		rs.Data = data
	}
	return rs, nil
}

// DriverStopFence is the dispatch module's seam the attachments scope
// fences a driver caller through: which of a set of candidate stops sit
// on runs assigned to the driver the platform subject names. An
// attachment's links reach stops polymorphically (owner_kind stop), and
// whether a stop rides the calling driver's run is dispatch's fact over
// dispatch's tables, so this module asks its owner through the narrow
// interface rather than reading past the fence. isDriver false answers
// an actor who is not held to the driver fence: every desk login, whose
// scope stays the org's.
type DriverStopFence interface {
	StopsOnDriverRuns(ctx context.Context, orgID, actor string, candidates []string) (onDriver map[string]bool, isDriver bool, err error)
}

// SyncStore is the attachments Store over the pool.
type SyncStore struct {
	pool  *pgxpool.Pool
	fence DriverStopFence
}

// NewSyncStore wires the adapter.
func NewSyncStore(pool *pgxpool.Pool) *SyncStore {
	return &SyncStore{pool: pool}
}

// WithDriverStopFence wires the driver fence's stop seam (dispatch's
// own adapter over its own tables). Production wires it; the
// composition root's boot fail-fast pins that it did.
func (s *SyncStore) WithDriverStopFence(f DriverStopFence) *SyncStore {
	s.fence = f
	return s
}

// DriverStopFenceWired answers whether the driver fence's seam is
// wired, the leg the composition root's boot fail-fast reads.
func (s *SyncStore) DriverStopFenceWired() bool { return s.fence != nil }

// driverScope resolves the driver fence for one caller: the stop ids a
// driver caller may reach, or ok false for every caller the fence does
// not bind (no seam wired, a tenant filter, an empty actor, or a desk
// login), whose scope stays the org's. An empty candidate set still
// asks: whether the caller is a driver does not depend on which stops a
// page happens to reach, and a driver whose page reaches no stop at all
// (an order photo, a bare row) is held to nothing, never to the org.
func (s *SyncStore) driverScope(ctx context.Context, f sync.ScopeFilter, candidates []string) (map[string]bool, bool, error) {
	if s.fence == nil || f.Tenant || f.Actor == "" {
		return nil, false, nil
	}
	return s.fence.StopsOnDriverRuns(ctx, f.OrgID, f.Actor, candidates)
}

// driverArm is the fence's answer for one caller, resolved BEFORE a
// transaction opens and carried into it. The seam reads over a pool, so
// asking it from inside a transaction is a second pool acquisition
// while the first connection is held (connection starvation when
// pool-size writers do it together). bound false is every caller the
// fence does not bind. A stop the arm was not resolved for is not in
// allowed, so a link set that changed between the pre-read and the row
// lock fails closed.
type driverArm struct {
	bound   bool
	allowed map[string]bool
}

// sees answers whether one row's link set is visible to the caller.
func (a driverArm) sees(links []Link) bool {
	return !a.bound || linksReachAStop(links, a.allowed)
}

// holdsTarget answers whether a DECLARED link set may be written by the
// caller: the target half of the write fence (sees is the source half).
// A held driver's attachments hang off stops on their own runs alone, so
// every declared stop link must be one of theirs and the set must reach
// at least one (a set that reaches none would hide the row from the
// driver and, for a colleague's stop, write it onto their day).
func (a driverArm) holdsTarget(links []Link) bool {
	if !a.bound {
		return true
	}
	for _, l := range links {
		if l.OwnerKind == OwnerStop && !a.allowed[l.OwnerID] {
			return false
		}
	}
	return linksReachAStop(links, a.allowed)
}

// declaredLinks reads the link set a pushed write declares, nil when it
// declares none or the set is malformed (applyWrite refuses a malformed
// set itself, before any write).
func declaredLinks(fields map[string]any) []Link {
	raw, ok := fields["links"]
	if !ok {
		return nil
	}
	links, err := linksOf(raw)
	if err != nil {
		return nil
	}
	return links
}

// resolveArm resolves the driver arm for the stop candidates a write is
// about to touch. Call it on the pool, never with a transaction open.
func (s *SyncStore) resolveArm(ctx context.Context, f sync.ScopeFilter, candidates []string) (driverArm, error) {
	allowed, isDriver, err := s.driverScope(ctx, f, candidates)
	if err != nil {
		return driverArm{}, err
	}
	return driverArm{bound: isDriver, allowed: allowed}, nil
}

// armForRow resolves the arm for a write addressed at one row: the
// candidates are the stops the row's standing links reach (read on the
// pool, before the transaction), plus any the caller declares.
func (s *SyncStore) armForRow(ctx context.Context, f sync.ScopeFilter, id string, declared []Link) (driverArm, error) {
	if s.fence == nil || f.Tenant || f.Actor == "" {
		return driverArm{}, nil
	}
	var standing []Link
	if looksLikeUUID(id) {
		links, err := s.loadLinks(ctx, []string{id})
		if err != nil {
			return driverArm{}, err
		}
		standing = links[id]
	}
	return s.resolveArm(ctx, f, stopCandidates(standing, declared))
}

// linksReachAStop answers whether one link set carries a link to one of
// the allowed stops. A driver's attachments are the ones hanging off
// their own runs' stops (the photos the drive-run app syncs); every
// other link shape (order, plan, receiving, quote) is the desk's
// business and not the driver's day.
func linksReachAStop(links []Link, allowed map[string]bool) bool {
	for _, l := range links {
		if l.OwnerKind == OwnerStop && allowed[l.OwnerID] {
			return true
		}
	}
	return false
}

// stopCandidates collects the distinct stop owner ids a set of link
// sets reaches, the candidates one seam call resolves.
func stopCandidates(linkSets ...[]Link) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, links := range linkSets {
		for _, l := range links {
			if l.OwnerKind == OwnerStop && !seen[l.OwnerID] {
				seen[l.OwnerID] = true
				out = append(out, l.OwnerID)
			}
		}
	}
	return out
}

// holdToDriver answers whether one row's link set is visible to the
// calling driver: false is the same answer an org-foreign row already
// gets (the callers answer not-found), so the row's existence never
// leaks. Every caller the fence does not bind passes untouched.
func (s *SyncStore) holdToDriver(ctx context.Context, f sync.ScopeFilter, links []Link) (bool, error) {
	allowed, isDriver, err := s.driverScope(ctx, f, stopCandidates(links))
	if err != nil {
		return false, err
	}
	if isDriver && !linksReachAStop(links, allowed) {
		return false, nil
	}
	return true, nil
}

// scopePredicate is the mandatory org predicate; ok false fails closed.
func scopePredicate(f sync.ScopeFilter) (string, []any, bool) {
	if f.Tenant || f.OrgID == "" {
		return "", nil, false
	}
	return "org_id = $1::uuid", []any{f.OrgID}, true
}

// loadLinks reads the link set of the given attachments, in insert order,
// grouped by attachment id. This is the module's own table; no join
// crosses the fence.
func (s *SyncStore) loadLinks(ctx context.Context, ids []string) (map[string][]Link, error) {
	return s.loadLinksOn(ctx, s.pool, ids)
}

// loadLinksOn is loadLinks over a querier (the pool, or a transaction
// the caller already holds).
func (s *SyncStore) loadLinksOn(ctx context.Context, q rowQuerier, ids []string) (map[string][]Link, error) {
	out := map[string][]Link{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `
		SELECT attachment_id::text, owner_kind, owner_id::text, COALESCE(role, '')
		FROM attachment_link
		WHERE attachment_id = ANY($1::uuid[])
		ORDER BY created_at, id`, ids)
	if err != nil {
		return nil, fmt.Errorf("attachments: load links: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var attachmentID, ownerKind, ownerID, role string
		if err := rows.Scan(&attachmentID, &ownerKind, &ownerID, &role); err != nil {
			return nil, fmt.Errorf("attachments: load links scan: %w", err)
		}
		out[attachmentID] = append(out[attachmentID], Link{
			AttachmentID: attachmentID, OwnerKind: ownerKind, OwnerID: ownerID, Role: role,
		})
	}
	return out, rows.Err()
}

// withLinks reads the link set of one row and folds it into the
// projection.
func (s *SyncStore) withLinks(ctx context.Context, a Attachment) (Attachment, error) {
	links, err := s.loadLinks(ctx, []string{a.ID})
	if err != nil {
		return Attachment{}, err
	}
	if len(links[a.ID]) == 0 {
		a.Links = []Link{}
		return a, nil
	}
	a.Links = links[a.ID]
	return a, nil
}

// Pull returns the page of attachment changes past since, live rows and
// tombstones in seq order, at most limit rows total, under the org
// predicate. The driver fence filters AFTER a page is cut (whether a
// row hangs off the driver's stops is a link fact resolved through the
// seam), so a page made only of colleagues' rows would answer empty
// with no cursor and the driver's own later rows would never arrive.
// Pull therefore keeps cutting pages from the last scanned seq until
// limit visible rows are gathered or the feed ends; a caller the fence
// does not bind fills the first page, so its path is one query.
func (s *SyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	where, args, ok := scopePredicate(f)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	if limit <= 0 {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM attachment
		WHERE %s AND seq IS NOT NULL AND seq > $2
		ORDER BY seq ASC
		LIMIT $3`, attachmentColumns, where)
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	cursor := since
	for {
		scanned, err := s.pullPage(ctx, q, append(append([]any{}, args...), cursor, limit))
		if err != nil {
			return nil, nil, err
		}
		if len(scanned) == 0 {
			return items, tombstones, nil
		}
		// The driver fence reads the links of every row on the page, live
		// and tombstoned alike (a tombstone still names the row), resolves
		// the stops the calling driver may reach in one seam call, and
		// drops every row that does not hang off one of them: a colleague's
		// photo is the same as absent.
		ids := make([]string, 0, len(scanned))
		for _, r := range scanned {
			ids = append(ids, r.attachment.ID)
		}
		links, err := s.loadLinks(ctx, ids)
		if err != nil {
			return nil, nil, err
		}
		linkSets := make([][]Link, 0, len(scanned))
		for _, ls := range links {
			linkSets = append(linkSets, ls)
		}
		allowed, isDriver, err := s.driverScope(ctx, f, stopCandidates(linkSets...))
		if err != nil {
			return nil, nil, err
		}
		for _, r := range scanned {
			cursor = r.seq
			if isDriver && !linksReachAStop(links[r.attachment.ID], allowed) {
				continue
			}
			if r.deleted {
				tombstones = append(tombstones, sync.Tombstone{ID: r.attachment.ID, Seq: r.seq})
			} else {
				r.attachment.Links = links[r.attachment.ID]
				if r.attachment.Links == nil {
					r.attachment.Links = []Link{}
				}
				data, err := attachmentData(r.attachment)
				if err != nil {
					return nil, nil, err
				}
				items = append(items, sync.Entity{ID: r.attachment.ID, Seq: r.seq, Data: data})
			}
			// The page is full: stop mid page, the next pull resumes from
			// the last row answered.
			if len(items)+len(tombstones) >= limit {
				return items, tombstones, nil
			}
		}
		// A short page is the end of the feed.
		if len(scanned) < limit {
			return items, tombstones, nil
		}
	}
}

// pullPage reads and scans one page of attachment rows.
func (s *SyncStore) pullPage(ctx context.Context, q string, args []any) ([]attachmentRow, error) {
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("attachments: sync pull: %w", err)
	}
	defer rows.Close()
	scanned := []attachmentRow{}
	for rows.Next() {
		r, err := scanAttachment(rows)
		if err != nil {
			return nil, fmt.Errorf("attachments: sync pull scan: %w", err)
		}
		scanned = append(scanned, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("attachments: sync pull: %w", err)
	}
	return scanned, nil
}

// FindApplied resolves one idempotency key to the row it applied under
// the org predicate. The row's column records the latest applied WRITE or
// create key; a replayed delete resolves through the engine's tombstone
// arm (Load answers Deleted and the result reports the retained seq),
// which is why Delete below does not restamp the column: restamping
// would hide the write key a replay needs. See the package doc for the
// one replay edge this leaves.
func (s *SyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	where, args, ok := scopePredicate(f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM attachment WHERE %s AND idempotency_key = $2::uuid LIMIT 1`,
		attachmentColumns, where)
	args = append(args, key)
	r, err := scanAttachment(s.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("attachments: sync find applied: %w", err)
	}
	links, err := s.loadLinks(ctx, []string{r.attachment.ID})
	if err != nil {
		return sync.RowState{}, false, err
	}
	if visible, err := s.holdToDriver(ctx, f, links[r.attachment.ID]); err != nil {
		return sync.RowState{}, false, err
	} else if !visible {
		return sync.RowState{}, false, nil
	}
	rs, err := rowStateOf(r)
	return rs, true, err
}

// Load reads one row by id under the org predicate; a row outside the
// caller's org is the same as an absent one (existence never leaks).
// Live rows carry their links.
func (s *SyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	return s.load(ctx, s.pool, f, id, nil)
}

// BoundEntityExists answers the bound key gate's existence half (UX-103):
// whether an attachment row the scope's keys can name still exists, for
// every caller of the org at once. The org predicate alone, tombstone
// aware, never the driver hold on the row's links the caller's Load
// speaks.
func (s *SyncStore) BoundEntityExists(ctx context.Context, f sync.ScopeFilter, id string) (bool, error) {
	if f.Tenant || f.OrgID == "" || !looksLikeUUID(id) {
		return false, nil
	}
	var present bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM attachment WHERE id = $1::uuid AND org_id = $2::uuid AND deleted_at IS NULL)`,
		id, f.OrgID).Scan(&present); err != nil {
		return false, fmt.Errorf("attachments: sync bound entity exists: %w", err)
	}
	return present, nil
}

// rowQuerier is the read surface a pool and a transaction share, so one
// loader body serves the public Load (the pool) and the callers that
// already hold a transaction (their own connection).
type rowQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// load is Load over a querier. A caller inside a transaction passes the
// transaction, so the read rides the connection it already holds instead
// of asking the shared pool for a second one (connection starvation by
// second acquisition when pool-size callers do this together). The
// driver fence follows the same rule: a caller holding a transaction
// passes the arm it resolved before the transaction opened, and nil
// (the public Load, no transaction held) resolves it here on the pool.
func (s *SyncStore) load(ctx context.Context, q rowQuerier, f sync.ScopeFilter, id string, arm *driverArm) (sync.RowState, bool, error) {
	where, args, ok := scopePredicate(f)
	if !ok || !looksLikeUUID(id) {
		return sync.RowState{}, false, nil
	}
	query := fmt.Sprintf(`SELECT %s FROM attachment WHERE id = $2::uuid AND %s`,
		attachmentColumns, where)
	args = append(args, id)
	r, err := scanAttachment(q.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("attachments: sync load: %w", err)
	}
	links, err := s.loadLinksOn(ctx, q, []string{r.attachment.ID})
	if err != nil {
		return sync.RowState{}, false, err
	}
	if arm != nil {
		if !arm.sees(links[r.attachment.ID]) {
			return sync.RowState{}, false, nil
		}
	} else if visible, err := s.holdToDriver(ctx, f, links[r.attachment.ID]); err != nil {
		return sync.RowState{}, false, err
	} else if !visible {
		return sync.RowState{}, false, nil
	}
	if r.deleted {
		rs, err := rowStateOf(r)
		return rs, true, err
	}
	a := r.attachment
	a.Links = []Link{}
	if len(links[a.ID]) > 0 {
		a.Links = links[a.ID]
	}
	data, err := attachmentData(a)
	if err != nil {
		return sync.RowState{}, false, err
	}
	return sync.RowState{ID: a.ID, Seq: r.seq, Data: data}, true, nil
}

// writableCreate is the field set a pushed create may declare: the
// registration legs. Anything shaped like content (bytes, data, base64,
// a URL the client cooked) is outside the set and refuses whole: the
// bytes are already in Appwrite Storage, and the payload that pretends
// otherwise is a client defect, not a sync input.
var writableCreate = map[string]bool{
	"id": true, "kind": true, "appwrite_file_id": true, "bucket": true,
	"mime": true, "size_bytes": true, "checksum": true, "tenant_id": true, "links": true,
}

// writable is the field set a pushed update may declare under
// last_write_wins_by_field: the row address (the engine's addressing
// leg), the correctable metadata and the link set. The identity legs
// (kind, appwrite_file_id, bucket) are the bytes' address in Storage and
// refuse: a merge that moved them would point the record at different
// bytes than the ones in Storage.
var writable = map[string]bool{
	"id": true, "mime": true, "checksum": true, "size_bytes": true, "links": true,
}

// attachmentCreateShape is the body-only half of the pushed create's
// checks: the writable field set, the kind vocabulary, the file address
// legs and their bounds, the size and tenant shape, the link set and the
// payload id shape. It reads nothing and honours no fence (the driver arm
// the links resolve through is Create's own read), so the bound key gate
// can run it before the hidden refusal (UX-116): an invalid body then
// meets these refusals under a spent key and a free one. The refusals are
// exactly the ones Create answers.
func attachmentCreateShape(fields map[string]any) (kind, fileID, bucket, mime, checksum string, sizeBytes, tenantID any, links []Link, id string, err error) {
	for k := range fields {
		if !writableCreate[k] {
			return "", "", "", "", "", nil, nil, nil, "", fmt.Errorf("attachments: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	kind, _ = fields["kind"].(string)
	if !validKind(kind) {
		return "", "", "", "", "", nil, nil, nil, "", fmt.Errorf("attachments: unknown kind: %w", sync.ErrOpRefused)
	}
	fileID, _ = fields["appwrite_file_id"].(string)
	if fileID == "" {
		return "", "", "", "", "", nil, nil, nil, "", fmt.Errorf("attachments: appwrite_file_id is required: %w", sync.ErrOpRefused)
	}
	if len(fileID) > 200 {
		return "", "", "", "", "", nil, nil, nil, "", fmt.Errorf("attachments: appwrite_file_id is too long (max 200): %w", sync.ErrOpRefused)
	}
	bucket, _ = fields["bucket"].(string)
	if bucket == "" {
		return "", "", "", "", "", nil, nil, nil, "", fmt.Errorf("attachments: bucket is required: %w", sync.ErrOpRefused)
	}
	if len(bucket) > 200 {
		return "", "", "", "", "", nil, nil, nil, "", fmt.Errorf("attachments: bucket is too long (max 200): %w", sync.ErrOpRefused)
	}
	// mime is optional in the contract's AttachmentCreate and NOT NULL in
	// the table: an unstated mime lands as the empty string, the explicit
	// placeholder for an unknown value, never a guessed one.
	mime, _ = fields["mime"].(string)
	if len(mime) > 200 {
		return "", "", "", "", "", nil, nil, nil, "", fmt.Errorf("attachments: mime is too long (max 200): %w", sync.ErrOpRefused)
	}
	checksum, _ = fields["checksum"].(string)
	if len(checksum) > 200 {
		return "", "", "", "", "", nil, nil, nil, "", fmt.Errorf("attachments: checksum is too long (max 200): %w", sync.ErrOpRefused)
	}
	sizeBytes, err = sizeLeg(fields, "size_bytes")
	if err != nil {
		return "", "", "", "", "", nil, nil, nil, "", err
	}
	if raw, ok := fields["tenant_id"].(string); ok && raw != "" {
		if !looksLikeUUID(raw) {
			return "", "", "", "", "", nil, nil, nil, "", fmt.Errorf("attachments: tenant_id must be a uuid: %w", sync.ErrOpRefused)
		}
		tenantID = raw
	}
	links, err = linksOf(fields["links"])
	if err != nil {
		return "", "", "", "", "", nil, nil, nil, "", fmt.Errorf("%v: %w", err, sync.ErrOpRefused)
	}
	id, hasID := fields["id"].(string)
	if hasID && !looksLikeUUID(id) {
		return "", "", "", "", "", nil, nil, nil, "", fmt.Errorf("attachments: payload id must be a uuid: %w", sync.ErrOpRefused)
	}
	return kind, fileID, bucket, mime, checksum, sizeBytes, tenantID, links, id, nil
}

// ValidateCreate answers the pushed create's body shape without the driver
// arm, the collision read or a write: the engine's bound key gate calls it
// before the hidden refusal (UX-116). The refusals are exactly Create's.
func (s *SyncStore) ValidateCreate(_ context.Context, _ sync.ScopeFilter, fields map[string]any) error {
	_, _, _, _, _, _, _, _, _, err := attachmentCreateShape(fields)
	return err
}

var _ sync.CreateBodyValidator = (*SyncStore)(nil)

// Create registers one attachment from a pushed create: the org stamps
// from the filter, the file's Appwrite address validates as D10's own
// required legs, the links land beside the row, and the row draws a
// fresh attachments seq and the idempotency key, one transaction.
func (s *SyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := scopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	kind, fileID, bucket, mime, checksum, sizeBytes, tenantID, links, id, err := attachmentCreateShape(fields)
	if err != nil {
		return sync.RowState{}, err
	}
	// A driver caller registers attachments against their own stops
	// alone: the drive-run app's photos. A declared stop link off their
	// own runs, or a create that reaches no stop of theirs at all, is
	// refused whole.
	// The arm is resolved here, on the pool, before the transaction
	// opens: the declared stops, plus the stops a taken payload id's
	// standing row reaches (the conflict arm below reads that row under
	// the same fence and must not ask the pool again).
	arm, err := s.armForRow(ctx, f, id, links)
	if err != nil {
		return sync.RowState{}, err
	}
	if !arm.holdsTarget(links) {
		return sync.RowState{}, fmt.Errorf("attachments: a driver registers attachments against their own stops: %w", sync.ErrOpRefused)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("attachments: begin sync create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := allocateSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanAttachment(tx.QueryRow(ctx, `
		INSERT INTO attachment
			(id, org_id, tenant_id, kind, appwrite_file_id, bucket, mime, size_bytes, checksum,
			 created_by, seq, idempotency_key)
		VALUES (COALESCE(NULLIF($1,'')::uuid, gen_random_uuid()), $2::uuid, $3::uuid, $4, $5, $6,
		        $7::text, $8::bigint, NULLIF($9,''), $10, $11, $12::uuid)
		ON CONFLICT (id) DO NOTHING
		RETURNING `+attachmentColumns,
		id, f.OrgID, tenantID, kind, fileID, bucket, mime, sizeBytes, checksum, f.Actor, seq, key))
	if errors.Is(err, pgx.ErrNoRows) {
		// The client-chosen id is taken; WHERE decides the refusal so
		// existence never leaks across the scope boundary.
		if _, own, _ := s.load(ctx, tx, f, id, &arm); own {
			return sync.RowState{}, sync.ErrRowExists
		}
		return sync.RowState{}, sync.ErrRowNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		// The unique idempotency index fired: another row holds this key.
		// The engine's dedup runs first, so reaching the index means the
		// key was spent on a DIFFERENT row; that is the caller's own
		// defect and answers as a per-operation rejection.
		return sync.RowState{}, sync.ErrRowExists
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("attachments: sync create insert: %w", err)
	}
	if err := insertLinksTx(ctx, tx, r.attachment.ID, links); err != nil {
		return sync.RowState{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("attachments: commit sync create: %w", err)
	}
	a, err := s.withLinks(ctx, r.attachment)
	if err != nil {
		return sync.RowState{}, err
	}
	data, err := attachmentData(a)
	if err != nil {
		return sync.RowState{}, err
	}
	return sync.RowState{ID: a.ID, Seq: r.seq, Data: data}, nil
}

// insertLinksTx writes one link set beside its attachment row, the
// module's own table, inside the caller's transaction.
func insertLinksTx(ctx context.Context, tx pgx.Tx, attachmentID string, links []Link) error {
	for _, l := range links {
		if _, err := tx.Exec(ctx, `
			INSERT INTO attachment_link (attachment_id, owner_kind, owner_id, role)
			VALUES ($1::uuid, $2, $3::uuid, NULLIF($4,''))
			ON CONFLICT (attachment_id, owner_kind, owner_id, role) DO NOTHING`,
			attachmentID, l.OwnerKind, l.OwnerID, l.Role); err != nil {
			return fmt.Errorf("attachments: insert link: %w", err)
		}
	}
	return nil
}

// sizeLeg reads the declared size_bytes leg as a validated int64.
func sizeLeg(fields map[string]any, name string) (any, error) {
	raw, ok := fields[name]
	if !ok || raw == nil {
		return nil, nil
	}
	n, ok := raw.(float64)
	if !ok || n < 0 || n != float64(int64(n)) {
		return nil, fmt.Errorf("attachments: %s must be a non-negative integer: %w", name, sync.ErrOpRefused)
	}
	return int64(n), nil
}

// applyWrite validates and applies one declared field set to a locked
// row, drawing a fresh seq. Shared by the sync Write and the desk's
// UpdateMetadata so the two paths cannot drift: the desk IS a writer
// with the same bounds, exactly as the contract's registered routes will
// drive it. stampKey is the sync Write's idempotency key, or "" for the
// desk path (the row's existing key stands). arm is the driver fence
// resolved before the transaction opened (armForRow): this body runs
// inside the transaction and never asks the pool for another connection.
func (s *SyncStore) applyWrite(ctx context.Context, tx pgx.Tx, f sync.ScopeFilter, id string, fields map[string]any, stampKey string, arm driverArm) (attachmentRow, error) {
	mime, hasMime := fields["mime"].(string)
	if raw, ok := fields["mime"]; ok && raw != nil && !hasMime {
		return attachmentRow{}, fmt.Errorf("attachments: mime must be a string: %w", sync.ErrOpRefused)
	}
	if hasMime && len(mime) > 200 {
		return attachmentRow{}, fmt.Errorf("attachments: mime is too long (max 200): %w", sync.ErrOpRefused)
	}
	checksum, hasChecksum := fields["checksum"].(string)
	if raw, ok := fields["checksum"]; ok && raw != nil && !hasChecksum {
		return attachmentRow{}, fmt.Errorf("attachments: checksum must be a string: %w", sync.ErrOpRefused)
	}
	if hasChecksum && len(checksum) > 200 {
		return attachmentRow{}, fmt.Errorf("attachments: checksum is too long (max 200): %w", sync.ErrOpRefused)
	}
	sizeBytes, err := sizeLeg(fields, "size_bytes")
	if err != nil {
		return attachmentRow{}, err
	}
	var links []Link
	if raw, ok := fields["links"]; ok {
		links, err = linksOf(raw)
		if err != nil {
			return attachmentRow{}, fmt.Errorf("%v: %w", err, sync.ErrOpRefused)
		}
	}

	// Lock the row under the org predicate, reading the full row: a
	// tombstoned row refuses, and an identical write is a no-op (below).
	current, err := scanAttachment(tx.QueryRow(ctx,
		fmt.Sprintf(`SELECT %s FROM attachment WHERE id = $1::uuid AND org_id = $2::uuid FOR UPDATE`,
			attachmentColumns),
		id, f.OrgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return attachmentRow{}, sync.ErrRowNotFound
	}
	if err != nil {
		return attachmentRow{}, fmt.Errorf("attachments: lock %q for sync write: %w", id, err)
	}
	if current.deleted {
		return attachmentRow{}, fmt.Errorf("attachments: a deleted attachment cannot be edited: %w", sync.ErrOpRefused)
	}

	// The identical-write no-op. The frozen schema gives this module no
	// append-only applied-key history (the tasks module's task_events
	// pattern), so this module's replay idempotency rests on the row's
	// single key column PLUS this rule: a write whose every declared leg
	// already equals the row's current state changes nothing, draws no
	// seq and restamps nothing, so a replayed operation that already
	// landed (under a key the column no longer holds) converges on the
	// unchanged row instead of churning the feed. See the package doc
	// for the residual this leaves.
	currentLinks, err := s.loadLinksTx(ctx, tx, id)
	if err != nil {
		return attachmentRow{}, err
	}
	// The driver fence holds the row-addressed paths to the calling
	// driver's own stops: a colleague's attachment is the same as
	// absent, before any field is compared or written.
	if !arm.sees(currentLinks) {
		return attachmentRow{}, sync.ErrRowNotFound
	}
	// The target half: a held driver's relink stays on their own stops.
	if _, declares := fields["links"]; declares && !arm.holdsTarget(links) {
		return attachmentRow{}, fmt.Errorf("attachments: a driver keeps attachments on their own stops: %w", sync.ErrOpRefused)
	}
	if declaredIsNoOp(fields, mime, checksum, sizeBytes, links, current, currentLinks) {
		return current, nil
	}

	seq, err := allocateSeqTx(ctx, tx)
	if err != nil {
		return attachmentRow{}, err
	}
	var keyArg any
	if stampKey != "" {
		keyArg = stampKey
	}
	r, err := scanAttachment(tx.QueryRow(ctx, `
		UPDATE attachment SET
			mime            = CASE WHEN $3::text IS NOT NULL THEN $3 ELSE mime END,
			checksum        = CASE WHEN $4::text IS NOT NULL THEN $4 ELSE checksum END,
			size_bytes      = COALESCE($5::bigint, size_bytes),
			updated_at      = now(),
			seq             = $6,
			idempotency_key = COALESCE($7::uuid, idempotency_key)
		WHERE id = $1::uuid AND org_id = $2::uuid AND deleted_at IS NULL
		RETURNING `+attachmentColumns,
		id, f.OrgID,
		legOrNil(fields, "mime", mime), legOrNil(fields, "checksum", checksum), sizeBytes,
		seq, keyArg))
	if errors.Is(err, pgx.ErrNoRows) {
		return attachmentRow{}, sync.ErrRowNotFound
	}
	if err != nil {
		return attachmentRow{}, fmt.Errorf("attachments: sync write %q: %w", id, err)
	}
	if _, ok := fields["links"]; ok {
		// The link set is one field under last_write_wins_by_field: the
		// declared set replaces the row's set, wholly and in one
		// transaction with the metadata legs above.
		if _, err := tx.Exec(ctx, `DELETE FROM attachment_link WHERE attachment_id = $1::uuid`, id); err != nil {
			return attachmentRow{}, fmt.Errorf("attachments: replace links: %w", err)
		}
		if err := insertLinksTx(ctx, tx, id, links); err != nil {
			return attachmentRow{}, err
		}
	}
	return r, nil
}

// loadLinksTx reads one row's link set inside the caller's transaction
// (the no-op compare needs it under the same lock).
func (s *SyncStore) loadLinksTx(ctx context.Context, tx pgx.Tx, id string) ([]Link, error) {
	rows, err := tx.Query(ctx, `
		SELECT owner_kind, owner_id::text, COALESCE(role, '')
		FROM attachment_link WHERE attachment_id = $1::uuid
		ORDER BY created_at, id`, id)
	if err != nil {
		return nil, fmt.Errorf("attachments: load links tx: %w", err)
	}
	defer rows.Close()
	out := []Link{}
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.OwnerKind, &l.OwnerID, &l.Role); err != nil {
			return nil, fmt.Errorf("attachments: load links tx scan: %w", err)
		}
		l.AttachmentID = id
		out = append(out, l)
	}
	return out, rows.Err()
}

// declaredIsNoOp reports whether every declared leg already equals the
// row's current state: an undeclared leg never differs (the write keeps
// the row's value), a declared leg compares against the row.
func declaredIsNoOp(fields map[string]any, mime, checksum string, sizeBytes any, links []Link, current attachmentRow, currentLinks []Link) bool {
	if v, ok := fields["mime"]; ok && v != nil {
		if mime != current.attachment.Mime {
			return false
		}
	}
	if v, ok := fields["checksum"]; ok && v != nil {
		if checksum != current.attachment.Checksum {
			return false
		}
	}
	if _, ok := fields["size_bytes"]; ok {
		declared, derr := int64Leg(sizeBytes)
		if derr != nil {
			return false
		}
		if current.attachment.SizeBytes == nil {
			if declared != nil {
				return false
			}
		} else if declared == nil || *declared != *current.attachment.SizeBytes {
			return false
		}
	}
	if _, ok := fields["links"]; ok {
		if len(links) != len(currentLinks) {
			return false
		}
		have := map[string]bool{}
		for _, l := range currentLinks {
			have[l.OwnerKind+"|"+l.OwnerID+"|"+l.Role] = true
		}
		for _, l := range links {
			if !have[l.OwnerKind+"|"+l.OwnerID+"|"+l.Role] {
				return false
			}
		}
	}
	return true
}

// int64Leg reads the validated size leg as an int64 pointer.
func int64Leg(v any) (*int64, error) {
	if v == nil {
		return nil, nil
	}
	n, ok := v.(int64)
	if !ok {
		return nil, fmt.Errorf("attachments: size leg is not an int64")
	}
	return &n, nil
}

// legOrNil reads one optional declared string leg: nil when the payload
// does not declare it (the SQL CASE keeps the row's value).
func legOrNil(fields map[string]any, name string, value string) any {
	if _, ok := fields[name]; !ok {
		return nil
	}
	return value
}

// Write applies exactly the declared fields to one row: the correctable
// metadata and the link set, a fresh seq, the idempotency key stamped
// (the replay dedup's column). The engine calls this for a fresh write
// and for a last_write_wins_by_field merge alike; both apply exactly the
// declared fields, the server row authoritative on the rest.
func (s *SyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := scopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	for k := range fields {
		if !writable[k] {
			return sync.RowState{}, fmt.Errorf("attachments: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	if !looksLikeUUID(id) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	arm, err := s.armForRow(ctx, f, id, declaredLinks(fields))
	if err != nil {
		return sync.RowState{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("attachments: begin sync write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	r, err := s.applyWrite(ctx, tx, f, id, fields, key, arm)
	if err != nil {
		return sync.RowState{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("attachments: commit sync write: %w", err)
	}
	a, err := s.withLinks(ctx, r.attachment)
	if err != nil {
		return sync.RowState{}, err
	}
	data, err := attachmentData(a)
	if err != nil {
		return sync.RowState{}, err
	}
	return sync.RowState{ID: a.ID, Seq: r.seq, Data: data}, nil
}

// Delete tombstones one row: deleted_at set, the row retained, a fresh
// seq, updated_at bumped. The idempotency key is deliberately NOT
// restamped: the column holds the last applied create-or-write key a
// replay dedups on, and the engine's own tombstone arm answers a
// replayed delete from the retained row without a store call.
func (s *SyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, _ string) (sync.RowState, error) {
	// The key parameter is deliberately unused: the delete leg does not
	// restamp the row's key (see the method doc above).
	if _, _, ok := scopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	// The arm resolves on the pool before the transaction opens.
	arm, err := s.armForRow(ctx, f, id, nil)
	if err != nil {
		return sync.RowState{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("attachments: begin sync delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The driver fence holds the tombstone to the calling driver's own
	// attachments, read off the row's standing links before the seq is
	// drawn: a colleague's row is the same as absent.
	delLinks, err := s.loadLinksTx(ctx, tx, id)
	if err != nil {
		return sync.RowState{}, err
	}
	if !arm.sees(delLinks) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	// The tombstone relies on the sync seq trigger (0422): the row lock first, then the counter. An explicit
	// draw ahead of the UPDATE would take the counter before the row and cross a writer that relies on the
	// trigger (PR 556 round 3c); RETURNING answers the seq the trigger drew.
	r, err := scanAttachment(tx.QueryRow(ctx, `
		UPDATE attachment SET
			deleted_at = now(),
			updated_at = now()
		WHERE id = $1::uuid AND org_id = $2::uuid AND deleted_at IS NULL
		RETURNING `+attachmentColumns,
		id, f.OrgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("attachments: sync delete %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("attachments: commit sync delete: %w", err)
	}
	rs, err := rowStateOf(r)
	if err != nil {
		return sync.RowState{}, err
	}
	rs.Deleted = true
	return rs, nil
}

// UpdateMetadata is the desk's correction path: the same declared-field
// bounds and the same link replacement the sync Write applies, drawn on
// the same seq counter, without an idempotency key (the desk is not an
// outbox). The review surface and the contract's registered routes read
// against exactly what this writes.
func (s *SyncStore) UpdateMetadata(ctx context.Context, f sync.ScopeFilter, id string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := scopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	for k := range fields {
		if !writable[k] {
			return sync.RowState{}, fmt.Errorf("attachments: field %q is not writable: %w", k, sync.ErrOpRefused)
		}
	}
	if !looksLikeUUID(id) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	arm, err := s.armForRow(ctx, f, id, declaredLinks(fields))
	if err != nil {
		return sync.RowState{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("attachments: begin desk update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	r, err := s.applyWrite(ctx, tx, f, id, fields, "", arm)
	if err != nil {
		return sync.RowState{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("attachments: commit desk update: %w", err)
	}
	a, err := s.withLinks(ctx, r.attachment)
	if err != nil {
		return sync.RowState{}, err
	}
	data, err := attachmentData(a)
	if err != nil {
		return sync.RowState{}, err
	}
	return sync.RowState{ID: a.ID, Seq: r.seq, Data: data}, nil
}

// allocateSeqTx draws the next attachments seq on the caller's
// transaction, the 0213 convention's scope_sequences upsert.
func allocateSeqTx(ctx context.Context, tx pgx.Tx) (int64, error) {
	var next int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO scope_sequences (scope) VALUES ($1)
		 ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
		 RETURNING next_value`, attachmentsSeqScope).Scan(&next); err != nil {
		return 0, fmt.Errorf("attachments: seq allocate for scope %s: %w", attachmentsSeqScope, err)
	}
	return next, nil
}
