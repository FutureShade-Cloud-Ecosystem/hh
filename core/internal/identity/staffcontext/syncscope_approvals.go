// syncscope_approvals.go is the approvals scope's Store (server_wins,
// an org scope): the sync core's seam over permission_approval_requests,
// the table this module owns inside the identity fence (approvals.go's
// header). The pattern is the pick_tasks pilot's and the drive-run
// adapters': the sync core owns the protocol and no table, this module
// meets it at the Store interface, and every write runs the rail's own
// rules, never a raw row write around them.
//
// THE APPROVER'S ACT STAYS THE MODULE'S. A pushed update that declares
// a status is a DECISION: the adapter resolves the caller's authority
// through the same ResolveStaff projection the rail reads, runs the
// same ApprovalRequest.Decide validation (branch reachability, the
// CanApprove matrix, four-eyes), and persists through the same
// compare-and-swap discipline (WHERE status = 'PENDING') the rail's
// SaveDecision runs, so no double decide and no silent flip. The two
// system-only money bridges (a JE decision posting the ledger, a
// freight decision applying a parked raise) refuse here: those
// bridges move money inside the decision's own transaction (0075,
// 0195) and are desk acts, the same class of act the till rule keeps
// online; their requests stay decidable on the rail's own route.
//
// THE ORG FENCE IS DERIVED, AND DERIVED TOTALY. permission_approval_requests
// carries no org_id column (0016 predates the org anchor, and 0213's
// additive pass names it for seq, tombstone and idempotency only).
// The one org anchor the frozen schema gives the row is its branch:
// branch_id is NOT NULL and references locations, and locations
// carries the 0213 org stamp, so every row resolves to exactly one
// org (the D47 messaging stop was a PARTIAL derivation, a NULL branch
// on every GLOBAL channel; this one has no NULL leg). The locations
// read is reference data the identity fence already leans on
// (branch_id's own foreign key), the same posture dispatch's
// branchInOrg records.
//
// Idempotency: the row's 0213 idempotency_key column is free on this
// table (no writer predates it), so every sync write stamps the
// client key on the live row, the drive-run design note, and the
// rail's own REST writes stamp their seq only (the desk's decisions
// are what a device's stale replay must meet).
package staffcontext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// approvalsSyncScope names the scope_sequences counter row every
// approval write draws from (the 0213 convention), the row the rail's
// own repository writes draw from too so desk and device share one
// feed.
const approvalsSyncScope = "approvals"

// publishedFloor is the margin floor's decide authority the sync door
// decides with. The composition root wires the rail with the dealer's
// configured set (Approvals.WithFloorApprovers, built from whatever role
// configuration document the root loaded), and it constructs this store
// on a wire the configuration does not pass, so the root PUBLISHES the
// set it gave the rail (PublishFloorApprovers, beside the rail's own
// wiring) and every pushed decision reads it: the desk's door and the
// device's door hold one authority by construction, never a second copy
// of the mapping. The carried default answers until the root runs, the
// same default the rail itself holds before its own wiring.
var publishedFloor = func() *atomic.Pointer[FloorApprovers] {
	p := &atomic.Pointer[FloorApprovers]{}
	d := defaultFloorApprovers()
	p.Store(&d)
	return p
}()

// PublishFloorApprovers publishes the configured margin floor decide
// authority for the sync door: the set the composition root gave the
// rail. A zero set publishes nothing and the carried default stands, the
// same fail posture the rail's own WithFloorApprovers holds.
func PublishFloorApprovers(f FloorApprovers) {
	if len(f.DealerWide) == 0 && len(f.OwnBranch) == 0 {
		return
	}
	publishedFloor.Store(&f)
}

// syncFloorApprovers reads the published authority.
func syncFloorApprovers() FloorApprovers {
	if f := publishedFloor.Load(); f != nil {
		return *f
	}
	return defaultFloorApprovers()
}

// approvalsSeqCTE is the data-modifying CTE the rail's single-statement
// write paths prepend to draw a fresh approvals seq atomically in the
// same statement. Gaps from no-op updates are allowed by the 0213
// convention.
const approvalsSeqCTE = `WITH aseq AS (
	INSERT INTO scope_sequences (scope) VALUES ('approvals')
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
	RETURNING next_value
) `

// allocateApprovalsSeqTx draws the next approvals seq inside the
// caller's transaction, the same upsert every adapter write runs.
func allocateApprovalsSeqTx(ctx context.Context, tx pgx.Tx) (int64, error) {
	var next int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO scope_sequences (scope) VALUES ($1)
		 ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
		 RETURNING next_value`, approvalsSyncScope).Scan(&next); err != nil {
		return 0, fmt.Errorf("identity: seq allocate for scope %s: %w", approvalsSyncScope, err)
	}
	return next, nil
}

// approvalSyncColumns is the rail's own card projection (the columns
// ListApprovals selects, amount out of the data JSONB exactly as the
// rail reads it) plus the two sync legs. line_id rides along so a pushed
// decision holds the decide path's branch arm rule (P2-C): the device
// decides exactly what the desk decides.
const approvalSyncColumns = `a.id::text, a.requested_by::text, a.branch_id::text, a.policy_type, a.detail,
	COALESCE(a.subject_ref,''), COALESCE((a.data->>'amount_cents')::bigint, 0),
	COALESCE(a.data->>'line_id',''),
	a.status, COALESCE(a.decided_by::text,''),
	` + requesterRoleHead + `a.requested_by` + requesterRoleTail + `,
	a.seq, (a.deleted_at IS NOT NULL) AS deleted`

// approvalSyncRow is one scanned row plus its sync legs.
type approvalSyncRow struct {
	req     ApprovalRequest
	seq     int64
	deleted bool
}

// scanApprovalSync reads the 12-target row.
func scanApprovalSync(row interface{ Scan(dest ...any) error }) (approvalSyncRow, error) {
	var r approvalSyncRow
	var policy, status string
	if err := row.Scan(&r.req.ID, &r.req.RequestedBy, &r.req.BranchID, &policy, &r.req.Detail,
		&r.req.SubjectRef, &r.req.AmountCents, &r.req.LineID, &status, &r.req.DecidedBy, &r.req.RequesterRole,
		&r.seq, &r.deleted); err != nil {
		return approvalSyncRow{}, err
	}
	r.req.Policy = PolicyType(policy)
	r.req.Status = ApprovalStatus(status)
	return r, nil
}

// approvalData projects one row through the module's own json tags,
// the wire shape the rail's card carries.
func approvalData(r ApprovalRequest) (map[string]any, error) {
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("identity: approvals sync projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("identity: approvals sync projection decode: %w", err)
	}
	return data, nil
}

// approvalStateOf folds one scanned row into the engine's RowState; a
// tombstone carries its seq and no projection.
func approvalStateOf(r approvalSyncRow) (sync.RowState, error) {
	rs := sync.RowState{ID: r.req.ID, Seq: r.seq, Deleted: r.deleted}
	if r.deleted {
		return rs, nil
	}
	data, err := approvalData(r.req)
	if err != nil {
		return sync.RowState{}, err
	}
	rs.Data = data
	return rs, nil
}

// approvalScopePredicate is the WHERE predicate every approvals
// adapter query runs under: the org off the row's branch (the total
// derivation the file header states), never absent, and a tenant
// filter fails closed (an org scope; the route's 403 is the middleware
// half, this is the store's).
func approvalScopePredicate(f sync.ScopeFilter) (string, []any, bool) {
	if f.Tenant || f.OrgID == "" {
		return "", nil, false
	}
	return `EXISTS (SELECT 1 FROM locations l WHERE l.id = a.branch_id AND l.org_id = $1::uuid)`,
		[]any{f.OrgID}, true
}

// ApprovalsSyncStore serves the approvals scope over
// permission_approval_requests.
type ApprovalsSyncStore struct {
	pool *pgxpool.Pool
	link UserIDLinker
}

// NewApprovalsSyncStore wires the adapter; a nil linker leaves actor
// attribution refusing (every write that needs a requester or a
// decider fails closed rather than stamping nobody).
func NewApprovalsSyncStore(pool *pgxpool.Pool, link UserIDLinker) *ApprovalsSyncStore {
	return &ApprovalsSyncStore{pool: pool, link: link}
}

// approvalSyncQuery is the projection over the module's own table.
const approvalSyncQuery = `SELECT ` + approvalSyncColumns + `
	FROM permission_approval_requests a`

// Pull returns the page of approval changes past since, live rows and
// tombstones in seq order, at most limit rows total, under the org
// fence. A floor request is a live row only for a caller that could
// decide it or filed it (P2-3, round 3): its detail carries the margin
// figures and this door has no money gate. Rows the caller may not read
// are skipped and the page keeps filling, so a hidden row never shortens
// a page that has a follower. A tombstone names an id alone and stays.
func (s *ApprovalsSyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	where, args, ok := approvalScopePredicate(f)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	reader := s.readerFor(ctx, f)
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	for {
		rows, err := s.pool.Query(ctx, approvalSyncQuery+`
			WHERE `+where+` AND a.seq IS NOT NULL AND a.seq > $2
			ORDER BY a.seq ASC
			LIMIT $3`, append(append([]any{}, args...), since, limit)...)
		if err != nil {
			return nil, nil, fmt.Errorf("identity: approvals sync pull: %w", err)
		}
		scanned := 0
		for rows.Next() {
			r, err := scanApprovalSync(rows)
			if err != nil {
				rows.Close()
				return nil, nil, fmt.Errorf("identity: approvals sync pull scan: %w", err)
			}
			scanned++
			since = r.seq
			if r.deleted {
				tombstones = append(tombstones, sync.Tombstone{ID: r.req.ID, Seq: r.seq})
			} else {
				if !reader(r.req) {
					continue
				}
				data, err := approvalData(r.req)
				if err != nil {
					rows.Close()
					return nil, nil, err
				}
				items = append(items, sync.Entity{ID: r.req.ID, Seq: r.seq, Data: data})
			}
			if len(items)+len(tombstones) >= limit {
				break
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, nil, fmt.Errorf("identity: approvals sync pull: %w", err)
		}
		if len(items)+len(tombstones) >= limit || scanned < limit {
			return items, tombstones, nil
		}
	}
}

// readerFor resolves the caller's authority once and answers, per request,
// whether the caller may read its card (mayReadRequestWith under the
// published floor set). A caller with no linked user, or whose authority
// cannot be read, reads no floor request: the door fails closed.
func (s *ApprovalsSyncStore) readerFor(ctx context.Context, f sync.ScopeFilter) func(ApprovalRequest) bool {
	fa := syncFloorApprovers()
	var caller StaffContext
	if s.link != nil && f.Actor != "" {
		if uid, ok := s.link(ctx, f.Actor); ok && uid != "" {
			if c, err := s.resolve(ctx, uid, f.OrgID); err == nil {
				caller = c
			}
		}
	}
	return func(r ApprovalRequest) bool { return mayReadRequestWith(fa, caller, r) }
}

// FindApplied resolves one idempotency key to the row it applied,
// under the org fence.
func (s *ApprovalsSyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	where, args, ok := approvalScopePredicate(f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	r, err := scanApprovalSync(s.pool.QueryRow(ctx, approvalSyncQuery+`
		WHERE `+where+` AND a.idempotency_key = $2::uuid
		LIMIT 1`, append(args, key)...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("identity: approvals sync find applied: %w", err)
	}
	rs, err := approvalStateOf(r)
	return rs, true, err
}

// Load reads one request by id under the org fence; a row outside the
// caller's org is the same as an absent one (existence never leaks).
func (s *ApprovalsSyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	where, args, ok := approvalScopePredicate(f)
	if !ok || !approvalLooksLikeUUID(id) {
		return sync.RowState{}, false, nil
	}
	r, err := scanApprovalSync(s.pool.QueryRow(ctx, approvalSyncQuery+`
		WHERE a.id = $2::uuid AND `+where, append(args, id)...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("identity: approvals sync load: %w", err)
	}
	rs, err := approvalStateOf(r)
	if err == nil && !r.deleted && !s.readerFor(ctx, f)(r.req) {
		// The row exists and the engine still needs its id and seq (the
		// stale check, the write's own refusal words), but its projection
		// travels in a conflict envelope: a card the caller may not read
		// carries no data out of this door (P2-3, round 3).
		rs.Data = nil
	}
	return rs, true, err
}

// approvalCreateFields is the field set a pushed create may declare:
// the branch the request is filed at, the caller-facing policy, the
// detail note and the optional subject. The requester is the CALLER's
// own linked user, never a payload field: a create naming another
// requester would let the filer step around four-eyes by filing in
// someone else's name and deciding the request themselves.
func approvalCreateFields(fields map[string]any) error {
	for k := range fields {
		switch k {
		case "id", "branch_id", "policy_type", "detail", "subject_ref":
		default:
			return fmt.Errorf("identity: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	return nil
}

// approvalCallerPolicy pins the caller-facing policy set: the two
// system-only policies (a JE card, a freight card) are filed by the
// ledger and the dispatch gate, and the margin floor is the quote
// module's own (D7, filed through FileFloorApproval, which binds the
// quote, the line and the discount from its own state); the module's own
// CreateApproval refuses all three with these words and the sync door
// refuses identically, so no door files a card whose subject and detail
// are the filer's own choosing.
func approvalCallerPolicy(p PolicyType) error {
	switch p {
	case PolicyCredit, PolicyCOD:
		return nil
	case PolicyMargin:
		return fmt.Errorf("identity: margin approvals are filed by the quote desk for a quote line, not directly: %w", sync.ErrOpRefused)
	case PolicyJEThreshold:
		return fmt.Errorf("identity: JE approvals are filed by the ledger, not directly: %w", sync.ErrOpRefused)
	case PolicyFreightAboveQuote:
		return fmt.Errorf("identity: freight approvals are filed by dispatch, not directly: %w", sync.ErrOpRefused)
	}
	return fmt.Errorf("identity: unknown policy %q: %w", p, sync.ErrOpRefused)
}

// approvalBranchInOrg answers whether the branch location sits in the
// caller's org, the create fence (the filter is the org authority, the
// branch row ties it to a yard). The locations read is reference
// data, the posture the file header states.
func approvalBranchInOrg(ctx context.Context, s *ApprovalsSyncStore, branchID, orgID string) bool {
	var ok bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM locations WHERE id = $1::uuid AND org_id = $2::uuid)`,
		branchID, orgID).Scan(&ok); err != nil {
		return false
	}
	return ok
}

// Create files one request as the CALLER (the rail's own
// caller-facing door): the caller-facing policies only, the branch in
// the caller's org, the row stamped PENDING with a freshly allocated
// seq and the idempotency key. The id is the payload's when the
// client generated one; a taken id refuses the create whole (inside
// the caller's org as the caller's own fact, outside it as not-found
// so existence never leaks).
func (s *ApprovalsSyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := approvalScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := approvalCreateFields(fields); err != nil {
		return sync.RowState{}, err
	}
	branchID, _ := fields["branch_id"].(string)
	if !approvalLooksLikeUUID(branchID) {
		return sync.RowState{}, fmt.Errorf("identity: branch_id must be a uuid: %w", sync.ErrOpRefused)
	}
	policy := PolicyType(strings.TrimSpace(fmt.Sprint(fields["policy_type"])))
	if err := approvalCallerPolicy(policy); err != nil {
		return sync.RowState{}, err
	}
	if !approvalBranchInOrg(ctx, s, branchID, f.OrgID) {
		return sync.RowState{}, fmt.Errorf("identity: the branch is not in this organisation: %w", sync.ErrOpRefused)
	}
	detail, _ := fields["detail"].(string)
	subject, _ := fields["subject_ref"].(string)
	if strings.TrimSpace(subject) != "" && len(strings.TrimSpace(subject)) > 256 {
		return sync.RowState{}, fmt.Errorf("identity: subject_ref is too long: %w", sync.ErrOpRefused)
	}
	id, hasID := fields["id"].(string)
	if hasID && !approvalLooksLikeUUID(id) {
		return sync.RowState{}, fmt.Errorf("identity: payload id must be a uuid: %w", sync.ErrOpRefused)
	}
	requester := ""
	if s.link != nil && f.Actor != "" {
		if uid, ok := s.link(ctx, f.Actor); ok {
			requester = uid
		}
	}
	if requester == "" {
		return sync.RowState{}, fmt.Errorf("identity: a pushed filing needs the caller's linked staff user: %w", sync.ErrOpRefused)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("identity: begin approvals sync create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := allocateApprovalsSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	var rowID string
	err = tx.QueryRow(ctx, `
		INSERT INTO permission_approval_requests
			(id, requested_by, branch_id, policy_type, detail, subject_ref, data, status, seq, idempotency_key)
		VALUES (COALESCE(NULLIF($1,'')::uuid, gen_random_uuid()), $2::uuid, $3::uuid, $4, $5,
		        NULLIF($6,''), '{}'::jsonb, 'PENDING', $7, $8::uuid)
		ON CONFLICT (id) DO NOTHING
		RETURNING id::text`,
		id, requester, branchID, string(policy), detail, subject, seq, key).Scan(&rowID)
	if errors.Is(err, pgx.ErrNoRows) {
		// The client-chosen id is taken: inside the caller's org the
		// collision is the caller's own fact, outside it the answer is
		// not-found so existence never leaks across the fence.
		if _, own, _ := s.Load(ctx, f, id); own {
			return sync.RowState{}, sync.ErrRowExists
		}
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("identity: approvals sync create insert: %w", err)
	}
	r, err := scanApprovalSync(tx.QueryRow(ctx, approvalSyncQuery+` WHERE a.id = $1::uuid`, rowID))
	if err != nil {
		return sync.RowState{}, fmt.Errorf("identity: approvals sync create read back: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("identity: commit approvals sync create: %w", err)
	}
	return approvalStateOf(r)
}

// approvalDecideFields is the decision's field set; the status and the
// optional rejection reason, the rail's own decide payload.
func approvalDecideFields(fields map[string]any) bool {
	for k := range fields {
		switch k {
		case "id", "status", "rejection_reason":
		default:
			return false
		}
	}
	return true
}

// approvalNoteFields is the requester's note field set: the detail of
// the caller's own PENDING request.
func approvalNoteFields(fields map[string]any) bool {
	for k := range fields {
		switch k {
		case "id", "detail":
		default:
			return false
		}
	}
	return true
}

// Write applies exactly the declared fields under the rail's own
// rules. Two surfaces exist, and nothing else:
//
//   - the DECISION (status APPROVED or REJECTED, optional rejection
//     reason): the caller's authority resolves through ResolveStaff,
//     the module's own Decide validates it (four-eyes, the CanApprove
//     matrix, the PENDING state), and the stamp is the rail's own
//     compare-and-swap, WHERE status = 'PENDING', drawing a fresh seq
//     and the idempotency key. A decision the row already carries
//     under another key refuses with the rail's own words rather than
//     flipping decided_by.
//
//   - the NOTE (detail): the requester's edit of their own PENDING
//     request, repeatable, with the value-equal arm answering applied
//     without stamping so a superseded replay cannot move the feed.
func (s *ApprovalsSyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := approvalScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	decide := approvalDecideFields(fields)
	note := !decide && approvalNoteFields(fields)
	if !decide && !note {
		return sync.RowState{}, fmt.Errorf("identity: an approvals update declares a decision (status) or a note (detail), nothing else: %w", sync.ErrOpRefused)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("identity: begin approvals sync write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if decide {
		return s.writeDecision(ctx, tx, f, id, key, fields)
	}
	return s.writeNote(ctx, tx, f, id, key, fields)
}

// writeDecision is the decide arm: the module's own rules, then the
// rail's own CAS stamp.
func (s *ApprovalsSyncStore) writeDecision(ctx context.Context, tx pgx.Tx, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	status, _ := fields["status"].(string)
	approve := status == string(ApprovalApproved)
	if !approve && status != string(ApprovalRejected) {
		return sync.RowState{}, fmt.Errorf("identity: a decision is APPROVED or REJECTED, not %q: %w", status, sync.ErrOpRefused)
	}
	reason, _ := fields["rejection_reason"].(string)

	// The caller's authority, resolved the way the rail resolves a
	// projection caller: the linked ERP user, then ResolveStaff.
	decider := ""
	if s.link != nil && f.Actor != "" {
		if uid, ok := s.link(ctx, f.Actor); ok {
			decider = uid
		}
	}
	if decider == "" {
		return sync.RowState{}, fmt.Errorf("identity: a pushed decision needs the caller's linked staff user: %w", sync.ErrOpRefused)
	}

	r, err := scanApprovalSync(tx.QueryRow(ctx, approvalSyncQuery+`
		WHERE a.id = $1::uuid AND deleted_at IS NULL
		  AND EXISTS (SELECT 1 FROM locations l WHERE l.id = a.branch_id AND l.org_id = $2::uuid)
		FOR UPDATE OF a`, id, f.OrgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("identity: lock approval %q for sync decide: %w", id, err)
	}
	// The money bridges are desk transactions (the file header); a
	// pushed decision on their cards refuses rather than stamping an
	// approval whose money never moved.
	if r.req.Policy == PolicyJEThreshold || r.req.Policy == PolicyFreightAboveQuote {
		return sync.RowState{}, fmt.Errorf("identity: a %s decision moves money at the desk, not through a replayed outbox: %w",
			r.req.Policy, sync.ErrOpRefused)
	}
	caller, err := s.resolve(ctx, decider, f.OrgID)
	if err != nil {
		return sync.RowState{}, err
	}
	// The module's own validation: authority, four-eyes, PENDING. The
	// margin arm's authority is the CONFIGURED floor set the rail holds
	// (D11, published by the composition root), so a pushed decision and
	// a desk decision hold one authority: the branch's configured Sales
	// manager decides from the device exactly what it decides at the desk.
	if err := r.req.DecideWithFloor(caller, approve, syncFloorApprovers()); err != nil {
		return sync.RowState{}, fmt.Errorf("identity: %w: %w", sync.ErrOpRefused, err)
	}
	seq, err := allocateApprovalsSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	var updated bool
	err = tx.QueryRow(ctx, `
		UPDATE permission_approval_requests a SET
			status          = $3,
			decided_by      = $4::uuid,
			decided_at      = now(),
			rejection_reason = $5,
			seq             = $6,
			idempotency_key = $7::uuid
		WHERE a.id = $1::uuid AND a.status = 'PENDING'
		  AND EXISTS (SELECT 1 FROM locations l WHERE l.id = a.branch_id AND l.org_id = $2::uuid)
		RETURNING (a.deleted_at IS NOT NULL)`,
		id, f.OrgID, status, decider, reason, seq, key).Scan(&updated)
	if errors.Is(err, pgx.ErrNoRows) {
		// The CAS missed: the row was decided between the lock read and
		// the stamp is impossible here (the lock holds), so the miss is
		// the not-decided-yet-at-read race the rail answers 409.
		return sync.RowState{}, fmt.Errorf("identity: request already decided: %w", sync.ErrOpRefused)
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("identity: approvals sync decide %q: %w", id, err)
	}
	out, err := scanApprovalSync(tx.QueryRow(ctx, approvalSyncQuery+` WHERE a.id = $1::uuid`, id))
	if err != nil {
		return sync.RowState{}, fmt.Errorf("identity: approvals sync decide read back: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("identity: commit approvals sync decide: %w", err)
	}
	return approvalStateOf(out)
}

// writeNote is the requester's-note arm: PENDING-only, requester-only,
// value-equal no-op.
func (s *ApprovalsSyncStore) writeNote(ctx context.Context, tx pgx.Tx, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	detail, _ := fields["detail"].(string)
	detail = strings.TrimSpace(detail)

	writer := ""
	if s.link != nil && f.Actor != "" {
		if uid, ok := s.link(ctx, f.Actor); ok {
			writer = uid
		}
	}
	if writer == "" {
		return sync.RowState{}, fmt.Errorf("identity: a pushed note needs the caller's linked staff user: %w", sync.ErrOpRefused)
	}

	var fromDetail, fromStatus, fromRequester, fromPolicy string
	err := tx.QueryRow(ctx, `
		SELECT a.detail, a.status, a.requested_by::text, a.policy_type FROM permission_approval_requests a
		 WHERE a.id = $1::uuid AND a.deleted_at IS NULL
		   AND EXISTS (SELECT 1 FROM locations l WHERE l.id = a.branch_id AND l.org_id = $2::uuid)
		 FOR UPDATE OF a`, id, f.OrgID).Scan(&fromDetail, &fromStatus, &fromRequester, &fromPolicy)
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("identity: lock approval %q for sync note: %w", id, err)
	}
	if fromRequester != writer {
		return sync.RowState{}, fmt.Errorf("identity: only the requester may edit a pending request's note: %w", sync.ErrOpRefused)
	}
	if fromStatus != string(ApprovalPending) {
		return sync.RowState{}, fmt.Errorf("identity: a decided request is not editable: %w", sync.ErrOpRefused)
	}
	// A floor request's detail (and the subject it is bound to) is the
	// quote desk's own record, built from the server's figures at filing
	// (D7): the approver reads words the server checked, so the requester
	// cannot replace them through this door, whatever note rights a plain
	// filing carries.
	if PolicyType(fromPolicy) == PolicyMargin {
		return sync.RowState{}, fmt.Errorf("identity: the floor request's detail is the quote desk's own record, not an editable note: %w", sync.ErrOpRefused)
	}
	if detail == fromDetail {
		// The declared note is already the request's note (a superseded
		// replay of one constant note meets this arm): answer applied
		// with the row as it stands, stamping nothing.
		r, err := scanApprovalSync(tx.QueryRow(ctx, approvalSyncQuery+` WHERE a.id = $1::uuid`, id))
		if err != nil {
			return sync.RowState{}, fmt.Errorf("identity: approvals sync note read back: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return sync.RowState{}, fmt.Errorf("identity: commit approvals sync note: %w", err)
		}
		return approvalStateOf(r)
	}
	seq, err := allocateApprovalsSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanApprovalSync(tx.QueryRow(ctx, `
		UPDATE permission_approval_requests a SET
			detail          = $3,
			seq             = $4,
			idempotency_key = $5::uuid
		WHERE a.id = $1::uuid AND a.deleted_at IS NULL
		  AND EXISTS (SELECT 1 FROM locations l WHERE l.id = a.branch_id AND l.org_id = $2::uuid)
		RETURNING `+approvalSyncColumns,
		id, f.OrgID, detail, seq, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("identity: approvals sync note %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("identity: commit approvals sync note: %w", err)
	}
	return approvalStateOf(r)
}

// Delete tombstones one request: deleted_at set, the row retained (the
// decision history the override lookup reads stays), a fresh seq. The
// idempotency key is deliberately NOT stamped over the row's, the
// drive-run design note: the last live key still dedups after the
// delete, and the delete itself is idempotent through the engine's
// already-tombstoned arm.
func (s *ApprovalsSyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	if _, _, ok := approvalScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	_ = key
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("identity: begin approvals sync delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := allocateApprovalsSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	var deleted bool
	err = tx.QueryRow(ctx, `
		UPDATE permission_approval_requests a SET
			deleted_at = now(),
			seq        = $3
		WHERE a.id = $1::uuid AND a.deleted_at IS NULL
		  AND EXISTS (SELECT 1 FROM locations l WHERE l.id = a.branch_id AND l.org_id = $2::uuid)
		RETURNING (a.deleted_at IS NOT NULL)`, id, f.OrgID, seq).Scan(&deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("identity: approvals sync delete %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("identity: commit approvals sync delete: %w", err)
	}
	return sync.RowState{ID: id, Seq: seq, Deleted: true}, nil
}

// resolve loads one linked user's current authority through the
// repository's own ORG-SCOPED projection (ResolveStaffInOrg): the
// caller arrived as the middleware-stamped org filter, and the
// winning-grant read must scope to that org exactly as the request
// path's guard resolves authority per org (D3), so a grant held only
// in another org decides nothing here. An unreadable projection
// refuses the decision rather than deciding on no authority.
func (s *ApprovalsSyncStore) resolve(ctx context.Context, userID, orgID string) (StaffContext, error) {
	caller, err := NewApprovalRepository(s.pool).ResolveStaffInOrg(ctx, userID, orgID)
	if err != nil {
		return StaffContext{}, fmt.Errorf("identity: resolve the decider's authority: %w: %w", sync.ErrOpRefused, err)
	}
	return caller, nil
}

// approvalLooksLikeUUID reports whether s is a canonical 36-char uuid,
// the boundary guard every pushed id passes before it reaches a
// Postgres uuid column.
func approvalLooksLikeUUID(s string) bool {
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
