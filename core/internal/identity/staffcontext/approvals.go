package staffcontext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// approvals.go is the rail's service and persistence halves, carried from
// the ERP identity package's service.go and repository.go at the pinned
// commit (the rail methods and the rail's table functions only; the
// session-issuing halves of both files stay behind with the HS256 world).
//
// Fence: permission_approval_requests is owned here, inside the identity
// module, exactly as at the pin; no module outside identity reaches the
// table, and the carried repository is the only writer. The rail reads the
// identity scaffold tables (users, roles, user_roles,
// user_branch_grants) for its candidate pool and caller resolution, which
// the ERP repository owned too; see the dual-source note on
// ApprovalRepository.

// approvalStore is the persistence the rail needs (the rail-narrowed half
// of the ERP staffStore). Real impl: ApprovalRepository (pgx); tests use a fake.
type approvalStore interface {
	CreateApproval(ctx context.Context, r ApprovalRequest) (ApprovalRequest, error)
	// ListApprovals filters by status (empty = any). If branches is nil the caller is
	// global (all branches); otherwise only requests in those branches are returned.
	ListApprovals(ctx context.Context, status string, branches []string) ([]ApprovalRequest, error)
	GetApproval(ctx context.Context, id string) (ApprovalRequest, error)
	// SaveDecision persists an approve/reject via a compare-and-swap (WHERE status='PENDING'). It
	// returns applied=false when zero rows matched, the request was already decided (a concurrent-decide
	// race), so the service returns 409 and fires NO echo/stamp. This is the real no-double-approve
	// guard (0075); the ref stamp is display only.
	SaveDecision(ctx context.Context, id, decidedBy string, status ApprovalStatus, reason string) (applied bool, err error)
	// BeginTx + SaveDecisionTx make the JE decide ATOMIC (0075): the request CAS stamp and the JE-to-GL
	// post run on ONE transaction so the ledger POST and the APPROVED stamp commit or roll back together
	// (closes the audit's post-then-stamp divergence), and the CAS row lock serializes concurrent deciders.
	BeginTx(ctx context.Context) (pgx.Tx, error)
	SaveDecisionTx(ctx context.Context, tx pgx.Tx, id, decidedBy string, status ApprovalStatus, reason string) (applied bool, err error)
	// HasApprovedOverride reports whether an APPROVED override exists for this
	// requester + branch + policy (+ optional subject), closing the loop (H-2).
	HasApprovedOverride(ctx context.Context, requestedBy, branchID, subjectRef string, policy PolicyType) (bool, error)
	// ResolveStaff loads the deciding or listing caller's authority: the rail's
	// own read, keyed by user id exactly as the ERP had it.
	ResolveStaff(ctx context.Context, userID string) (StaffContext, error)
	// ListApproverCandidates returns the ACTIVE staff whose role could grant approval authority: the
	// fan-out candidate pool ResolveApprovers filters via CanApprove (0075).
	ListApproverCandidates(ctx context.Context) ([]StaffContext, error)
	// ApprovalsForReconcile returns PENDING requests plus those decided since `decidedSince`: the
	// bounded window the messaging reconcile sweep backfills cards/stamps from (0075).
	ApprovalsForReconcile(ctx context.Context, decidedSince time.Time) ([]ApprovalRequest, error)
}

// clientError carries a status + a message that is SAFE to return to the caller
// (validation or authorization feedback). Anything that is not a clientError (or a
// known sentinel) is treated as an internal error: generic 500, real error logged.
type clientError struct {
	status int
	msg    string
}

func (e clientError) Error() string { return e.msg }

func clientErr(status int, format string, a ...any) error {
	return clientError{status: status, msg: fmt.Sprintf(format, a...)}
}

// Approvals is the approvals rail service: the rail half of the ERP
// identity Service, on its own type because the session-issuing half of
// that Service stays behind with the superseded HS256 world. Every method
// and seam is the carried one on the carried signature; the constructor
// drops the staff secret the ERP constructor took, because nothing in the
// rail ever signed a token.
type Approvals struct {
	store approvalStore
	log   *slog.Logger
	// Approvals rail seams (0075), all nil-able (kill switch = unwire in the composition root):
	notifier  ApprovalNotifier // fan-out cards + decision echoes to the messaging hub
	jeDecider JEDecider        // bridges a JE_OVER_THRESHOLD decision to the GL maker-checker
	// freightDecider bridges a FREIGHT_ABOVE_QUOTE decision to the parked split dispatch owns (0195).
	freightDecider FreightDecider
}

// NewApprovals returns the rail service over one store, with the default
// logger. The ERP constructor took the staff JWT secret alongside the
// store; the rail never reads it, and the secret no longer exists in the
// target configuration.
func NewApprovals(store approvalStore) *Approvals {
	return &Approvals{store: store, log: slog.Default()}
}

// WithLogger swaps the structured logger (best-effort approvals-rail fan-out logging).
func (s *Approvals) WithLogger(l *slog.Logger) *Approvals {
	if l != nil {
		s.log = l
	}
	return s
}

// CreateApproval files a caller-facing override request (the margin
// override door). The two system-only policies are refused here, on the
// carried rationale: a JE or freight card forged from this path could name
// a subject_ref of its author's choosing, and approving one is what
// applies the money.
func (s *Approvals) CreateApproval(ctx context.Context, requestedBy, branchID string, policy PolicyType, detail, subjectRef string) (ApprovalRequest, error) {
	// JE_OVER_THRESHOLD is a SYSTEM-ONLY policy: it is filed exclusively by the GL park-bridge
	// (FileJEApproval), which binds the REAL parked-journal subject_ref, its debit total as amount_cents,
	// and the journal's maker as requester. Accepting it on this caller-facing path (audit money-1/money-3)
	// would let any authenticated staffer forge a JE card against another user's DRAFT journal with a
	// spoofed $0 amount, which a rail approver could then post. Reject it here; FileJEApproval calls
	// fileApproval directly and is unaffected.
	if policy == PolicyJEThreshold {
		return ApprovalRequest{}, clientErr(http.StatusForbidden, "JE approvals are filed by the ledger, not directly")
	}
	// FREIGHT_ABOVE_QUOTE is SYSTEM-ONLY for the same reason and with sharper teeth (0195). It is
	// filed exclusively by dispatch's freight gate, which binds a real PARKED delivery_freight_raises
	// row as subject_ref after establishing the invariant breach against the order's own frozen
	// baseline. Accepting it would let any authenticated staffer file a freight card naming a
	// subject_ref of their choosing (including a raise nobody proposed, or another branch's), which
	// a rail approver could then APPROVE, and approving is what applies the money.
	if policy == PolicyFreightAboveQuote {
		return ApprovalRequest{}, clientErr(http.StatusForbidden, "freight approvals are filed by dispatch, not directly")
	}
	return s.fileApproval(ctx, ApprovalRequest{
		RequestedBy: requestedBy,
		BranchID:    branchID,
		Policy:      policy,
		Detail:      detail,
		SubjectRef:  subjectRef,
		Status:      ApprovalPending,
	})
}

// fileApproval is the shared create path for every trigger (the margin override from CreateApproval, the
// JE park-bridge from FileJEApproval). It validates, persists, then best-effort fans out APPROVAL cards to
// each eligible approver's channel (log-and-continue: a messaging blip never fails the approval write; a
// miss self-heals on the reconcile sweep).
func (s *Approvals) fileApproval(ctx context.Context, in ApprovalRequest) (ApprovalRequest, error) {
	if in.RequestedBy == "" || in.BranchID == "" {
		return ApprovalRequest{}, clientErr(http.StatusBadRequest, "requested_by and branch_id are required")
	}
	switch in.Policy {
	case PolicyMargin, PolicyCredit, PolicyCOD, PolicyJEThreshold, PolicyFreightAboveQuote:
	default:
		return ApprovalRequest{}, clientErr(http.StatusBadRequest, "unknown policy %q", in.Policy)
	}
	in.Status = ApprovalPending
	req, err := s.store.CreateApproval(ctx, in)
	if err != nil {
		return ApprovalRequest{}, err
	}
	s.notifyApprovalRequested(ctx, req)
	return req, nil
}

// HasApprovedOverride reports whether the requester holds an APPROVED override for
// the given branch + policy (+ optional subject). This is the final hop of the
// request-approve-proceed loop the policy gate consults (H-2).
func (s *Approvals) HasApprovedOverride(ctx context.Context, requestedBy, branchID string, policy PolicyType) (bool, error) {
	if requestedBy == "" || branchID == "" {
		return false, nil
	}
	return s.store.HasApprovedOverride(ctx, requestedBy, branchID, "", policy)
}

// ListApprovals returns the requests the caller is allowed to see:
// a GLOBAL user sees every branch; a scoped user sees only their granted branches.
func (s *Approvals) ListApprovals(ctx context.Context, callerID, status string) ([]ApprovalRequest, error) {
	caller, err := s.store.ResolveStaff(ctx, callerID)
	if err != nil {
		return nil, err
	}
	if caller.Scope == ScopeGlobal {
		return s.store.ListApprovals(ctx, status, nil)
	}
	branches := make([]string, 0, len(caller.Branches))
	for b := range caller.Branches {
		branches = append(branches, b)
	}
	if len(branches) == 0 {
		return []ApprovalRequest{}, nil
	}
	return s.store.ListApprovals(ctx, status, branches)
}

// DecideApproval enforces the authority model, bridges a JE decision to the GL maker-checker (GL first),
// then persists the decision via a compare-and-swap and fires the decision echo.
func (s *Approvals) DecideApproval(ctx context.Context, id, callerID string, approve bool, reason string) error {
	// Reject a malformed (non-UUID) approval id before it reaches the uuid PK cast (22P02 to HTTP 500).
	if _, err := uuid.Parse(id); err != nil {
		return clientErr(http.StatusBadRequest, "invalid approval id")
	}
	caller, err := s.store.ResolveStaff(ctx, callerID)
	if err != nil {
		return err
	}
	req, err := s.store.GetApproval(ctx, id)
	if err != nil {
		return err
	}
	// Validate authority + PENDING-state + four-eyes in memory (sets req.Status). Nothing persists yet, so
	// a rejected decide never touches the ledger or the request row.
	if err := req.Decide(caller, approve); err != nil {
		return err
	}

	// ATOMIC decide (0075). The request CAS stamp and the JE-to-GL post run on ONE transaction so the ledger
	// POST and the APPROVED stamp commit or roll back together. Ordering inside the tx:
	//  1. CAS stamp FIRST: UPDATE with WHERE status='PENDING' takes the request row's lock. applied=false
	//     means a concurrent decide already won (409); a competing decide BLOCKS on this lock until we commit,
	//     then sees the new status and also gets 409, so deciders are serialized and a reject can never
	//     interleave between an approve's post and its stamp (the audit's concurrent-divergence bug).
	//  2. JE-to-GL post SECOND, on the SAME tx. If GL refuses (journal meanwhile voided/posted, 409, or
	//     four-eyes, 403), we return the error and the deferred Rollback UNDOES the stamp: the request
	//     stays PENDING and the ledger is untouched (no "APPROVED without a POST", no "POST without an
	//     APPROVED"). A DECLINE bridges nothing (the journal stays DRAFT; voiding is the maker's action).
	tx, err := s.store.BeginTx(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	applied, err := s.store.SaveDecisionTx(ctx, tx, id, caller.UserID, req.Status, reason)
	if err != nil {
		return err
	}
	if !applied {
		return clientErr(http.StatusConflict, "request already decided")
	}
	if req.Policy == PolicyJEThreshold && approve && s.jeDecider != nil && req.SubjectRef != "" {
		if err := s.jeDecider.ApproveJournalTx(ctx, tx, req.SubjectRef, caller.UserID); err != nil {
			return err // rollback (deferred) reverts the stamp: request stays PENDING, ledger untouched
		}
	}
	// The freight bridge (0195), and note that BOTH outcomes cross it, unlike the JE bridge above,
	// where a decline bridges nothing because voiding a DRAFT journal is the maker's own act.
	//
	// A refused freight raise has somewhere to go: the parked proposal must be marked REFUSED in the
	// same transaction, or it stays PARKED forever, keeps the order's one-parked-raise slot occupied,
	// and tells the dispatcher who looks at it that a decision is still coming when one has already
	// been made. So the reject arm is a WRITE, and it is on this transaction for the same reason the
	// approve arm is: the stamp and the state it mirrors commit or roll back together.
	if req.Policy == PolicyFreightAboveQuote && s.freightDecider != nil && req.SubjectRef != "" {
		var ferr error
		if approve {
			ferr = s.freightDecider.ApplyFreightRaiseTx(ctx, tx, req.SubjectRef, caller.UserID)
		} else {
			ferr = s.freightDecider.RefuseFreightRaiseTx(ctx, tx, req.SubjectRef, caller.UserID)
		}
		if ferr != nil {
			return ferr // deferred rollback reverts the stamp: the request stays PENDING and decidable
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err // stamp + post roll back together
	}
	committed = true

	// Decision committed: best-effort echo, stamp the cards, post the EVENT into approver channels, SSE
	// the requester. Log-and-continue (a messaging blip never fails a committed decision).
	s.notifyApprovalDecided(ctx, req, caller.UserID, reason)
	return nil
}

// errNoDB names the unconfigured-pool state, so a nil-pool repository is a
// loud error rather than a silent zero-value answer.
var errNoDB = errors.New("identity: database not configured")

// ApprovalRepository is the rail's persistence: the rail half of the ERP
// identity Repository, owning permission_approval_requests exactly as at
// the pin (the session-path halves of the ERP Repository stay behind). Its
// SQL is carried byte-identical.
//
// DUAL-SOURCE NOTE (a recorded finding, not a silent choice): the rail's
// candidate pool and caller resolution read the ERP identity scaffold
// tables (users, user_roles, roles, user_branch_grants), while the Phase B
// request path resolves authority from staff_role_grant (D3) and honors
// the tombstone columns those reads predate. On populated data the two
// agree by construction (population seeded the grants from these tables);
// on post-population drift they can diverge. The request-path bridge
// (CallerFrom over the Phase B resolver) is the alternative the wire
// functions choose per call site, and the reconciliation is the wiring
// batches' decision point, recorded with this batch's findings.
type ApprovalRepository struct {
	pool *pgxpool.Pool // nil in scaffold/tests
}

// NewApprovalRepository returns the rail repository over one pool.
func NewApprovalRepository(pool *pgxpool.Pool) *ApprovalRepository {
	return &ApprovalRepository{pool: pool}
}

// CreateApproval inserts a PENDING override request and returns it with its new id.
func (r *ApprovalRepository) CreateApproval(ctx context.Context, req ApprovalRequest) (ApprovalRequest, error) {
	if r.pool == nil {
		return ApprovalRequest{}, errNoDB
	}
	var subject any
	if req.SubjectRef != "" {
		subject = req.SubjectRef
	}
	// AmountCents has no dedicated column (0016): persist it into the existing `data` JSONB so the rail
	// card + the reconcile re-fan can read it back (0075). Zero/absent leaves data as the '{}' default.
	data := []byte(`{}`)
	if req.AmountCents != 0 {
		if b, err := json.Marshal(map[string]int64{"amount_cents": req.AmountCents}); err == nil {
			data = b
		}
	}
	var id string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO permission_approval_requests (requested_by, branch_id, policy_type, detail, subject_ref, data, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'PENDING') RETURNING id::text`,
		req.RequestedBy, req.BranchID, string(req.Policy), req.Detail, subject, data).Scan(&id)
	if err != nil {
		return ApprovalRequest{}, fmt.Errorf("create approval: %w", err)
	}
	req.ID = id
	req.Status = ApprovalPending
	return req, nil
}

// ListApprovals returns requests filtered by status (empty = any) and, when branches
// is non-nil, restricted to those branches.
func (r *ApprovalRepository) ListApprovals(ctx context.Context, status string, branches []string) ([]ApprovalRequest, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	// Select subject_ref + amount so the rail feed (GET /api/v1/staff/approvals) can render a faithful
	// JE card (money magnitude + journal binding), matching ApprovalsForReconcile.
	q := `SELECT id::text, requested_by::text, branch_id::text, policy_type, detail,
	             COALESCE(subject_ref,''), COALESCE((data->>'amount_cents')::bigint, 0),
	             status, COALESCE(decided_by::text,'')
	      FROM permission_approval_requests WHERE ($1 = '' OR status = $1)`
	args := []any{status}
	if branches != nil {
		q += ` AND branch_id = ANY($2::uuid[])`
		args = append(args, branches)
	}
	q += ` ORDER BY requested_at DESC`
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ApprovalRequest
	for rows.Next() {
		var a ApprovalRequest
		var policy, st string
		if err := rows.Scan(&a.ID, &a.RequestedBy, &a.BranchID, &policy, &a.Detail,
			&a.SubjectRef, &a.AmountCents, &st, &a.DecidedBy); err != nil {
			return nil, err
		}
		a.Policy = PolicyType(policy)
		a.Status = ApprovalStatus(st)
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetApproval loads a single request.
func (r *ApprovalRepository) GetApproval(ctx context.Context, id string) (ApprovalRequest, error) {
	if r.pool == nil {
		return ApprovalRequest{}, errNoDB
	}
	var a ApprovalRequest
	var policy, st string
	// Must select subject_ref + amount here: DecideApproval reads req.SubjectRef to bridge a
	// JE_OVER_THRESHOLD approve into gl.ApproveJournal. Omitting it (the shipped bug) left SubjectRef=""
	// so the bridge was skipped: the request stamped APPROVED while the journal stayed DRAFT (0075).
	err := r.pool.QueryRow(ctx, `
		SELECT id::text, requested_by::text, branch_id::text, policy_type, detail,
		       COALESCE(subject_ref,''), COALESCE((data->>'amount_cents')::bigint, 0),
		       status, COALESCE(decided_by::text,'')
		FROM permission_approval_requests WHERE id = $1`, id).Scan(
		&a.ID, &a.RequestedBy, &a.BranchID, &policy, &a.Detail,
		&a.SubjectRef, &a.AmountCents, &st, &a.DecidedBy)
	if err != nil {
		// A well-formed-but-absent id maps to a clean 404 (not a raw 500), matching the module's
		// clientError taxonomy. (Malformed non-UUID ids are rejected upstream in DecideApproval.)
		if errors.Is(err, pgx.ErrNoRows) {
			return ApprovalRequest{}, clientErr(http.StatusNotFound, "approval not found")
		}
		return ApprovalRequest{}, fmt.Errorf("get approval %q: %w", id, err)
	}
	a.Policy = PolicyType(policy)
	a.Status = ApprovalStatus(st)
	return a, nil
}

// SaveDecision persists an approve/reject outcome via a COMPARE-AND-SWAP: the UPDATE is guarded by
// `AND status = 'PENDING'`, so two approvers deciding the same request in the same window no longer
// last-writer-win (the shipped bug: decided_by silently flipped). It returns applied=true iff exactly one
// row matched. applied=false means the request was already decided: the caller returns 409 and fires no
// echo/stamp. This CAS is the real no-double-approve guard (0075); the ref stamp is display only.
func (r *ApprovalRepository) SaveDecision(ctx context.Context, id, decidedBy string, status ApprovalStatus, reason string) (bool, error) {
	if r.pool == nil {
		return false, errNoDB
	}
	ct, err := r.pool.Exec(ctx, `
		UPDATE permission_approval_requests
		SET status = $2, decided_by = $3, decided_at = now(), rejection_reason = $4
		WHERE id = $1 AND status = 'PENDING'`, id, string(status), decidedBy, reason)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() == 1, nil
}

// BeginTx opens a transaction on the pool. The approvals-rail decide (0075) uses it to make the request
// CAS stamp and the JE-to-GL post ATOMIC: both writes run on this one tx so the ledger POST and the
// APPROVED stamp commit or roll back together (closing the post-then-stamp divergence the audit found).
func (r *ApprovalRepository) BeginTx(ctx context.Context) (pgx.Tx, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	return r.pool.Begin(ctx)
}

// SaveDecisionTx is SaveDecision on a caller-supplied transaction (approvals-rail atomic decide, 0075).
// The CAS `WHERE status='PENDING'` takes the request row's lock for the life of the tx, so a concurrent
// decide on the same request blocks until this tx commits/rolls back and then sees the new status,
// serializing deciders and preventing the "one approves + posts while another rejects" divergence.
func (r *ApprovalRepository) SaveDecisionTx(ctx context.Context, tx pgx.Tx, id, decidedBy string, status ApprovalStatus, reason string) (bool, error) {
	ct, err := tx.Exec(ctx, `
		UPDATE permission_approval_requests
		SET status = $2, decided_by = $3, decided_at = now(), rejection_reason = $4
		WHERE id = $1 AND status = 'PENDING'`, id, string(status), decidedBy, reason)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() == 1, nil
}

// HasApprovedOverride reports whether an APPROVED override exists for the
// requester + branch + policy. When subjectRef is non-empty the match is further
// narrowed to that subject; empty matches any (requester/branch-scoped) override.
func (r *ApprovalRepository) HasApprovedOverride(ctx context.Context, requestedBy, branchID, subjectRef string, policy PolicyType) (bool, error) {
	if r.pool == nil {
		return false, errNoDB
	}
	var ok bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM permission_approval_requests
			WHERE requested_by = $1 AND branch_id = $2 AND policy_type = $3 AND status = 'APPROVED'
			  AND ($4 = '' OR subject_ref = $4)
		)`, requestedBy, branchID, string(policy), subjectRef).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check approved override: %w", err)
	}
	return ok, nil
}

// ResolveStaff loads a user's CURRENT authority (status, tier, scope, granted
// branches) straight from the DB: the rail's caller read on the carried ERP
// query, keyed by users.id. See the dual-source note on ApprovalRepository:
// the request path resolves through the Phase B resolver instead, and the
// bridge maps that resolution onto this same vocabulary.
func (r *ApprovalRepository) ResolveStaff(ctx context.Context, userID string) (StaffContext, error) {
	if r.pool == nil {
		return StaffContext{}, errNoDB
	}
	var roleCode, scope, status string
	var tier int
	var branchIDs []string
	err := r.pool.QueryRow(ctx, `
		SELECT u.status, COALESCE(ro.code::text,''), COALESCE(ro.tier,0), COALESCE(ro.scope,'BRANCH'),
		       COALESCE(array_remove(array_agg(DISTINCT ubg.branch_id::text), NULL), ARRAY[]::text[])
		FROM users u
		LEFT JOIN user_roles ur ON ur.user_id = u.id
		LEFT JOIN roles ro ON ro.id = ur.role_id
		LEFT JOIN user_branch_grants ubg ON ubg.user_id = u.id
		WHERE u.id = $1
		GROUP BY u.id, u.status, ro.code, ro.tier, ro.scope`, userID).Scan(&status, &roleCode, &tier, &scope, &branchIDs)
	if err != nil {
		return StaffContext{}, fmt.Errorf("resolve staff %q: %w", userID, err)
	}
	branches := make(map[string]bool, len(branchIDs))
	for _, b := range branchIDs {
		branches[b] = true
	}
	return StaffContext{UserID: userID, RoleCode: roleCode, Tier: Tier(tier), Scope: Scope(scope), Status: status, Branches: branches}, nil
}

// ListApproverCandidates returns every ACTIVE staff member WITH an assigned role (an unassigned user can
// approve nothing) projected as a StaffContext: the fan-out candidate pool ResolveApprovers filters via
// CanApprove (0075). Role/branch is read straight from the DB, so a promotion/transfer is reflected on the
// next fan-out or reconcile pass.
func (r *ApprovalRepository) ListApproverCandidates(ctx context.Context) ([]StaffContext, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT u.id::text, COALESCE(ro.code::text,''), COALESCE(ro.tier,0), COALESCE(ro.scope,'BRANCH'), u.status,
		       COALESCE(array_remove(array_agg(DISTINCT ubg.branch_id::text), NULL), ARRAY[]::text[])
		FROM users u
		JOIN user_roles ur ON ur.user_id = u.id
		JOIN roles ro ON ro.id = ur.role_id
		LEFT JOIN user_branch_grants ubg ON ubg.user_id = u.id
		WHERE u.status = 'ACTIVE'
		GROUP BY u.id, ro.code, ro.tier, ro.scope, u.status`)
	if err != nil {
		return nil, fmt.Errorf("list approver candidates: %w", err)
	}
	defer rows.Close()
	var out []StaffContext
	for rows.Next() {
		var c StaffContext
		var roleCode, scope, status string
		var tier int
		var branchIDs []string
		if err := rows.Scan(&c.UserID, &roleCode, &tier, &scope, &status, &branchIDs); err != nil {
			return nil, fmt.Errorf("scan approver candidate: %w", err)
		}
		c.RoleCode = roleCode
		c.Tier = Tier(tier)
		c.Scope = Scope(scope)
		c.Status = status
		c.Branches = make(map[string]bool, len(branchIDs))
		for _, b := range branchIDs {
			c.Branches[b] = true
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ApprovalsForReconcile returns the bounded window the messaging reconcile sweep needs: every PENDING
// request plus every request decided at/after decidedSince (backfill of missed cards/stamps). It selects
// subject_ref + the amount from `data` so the sweep can rebuild a faithful card (0075).
func (r *ApprovalRepository) ApprovalsForReconcile(ctx context.Context, decidedSince time.Time) ([]ApprovalRequest, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, requested_by::text, branch_id::text, policy_type, detail,
		       COALESCE(subject_ref,''), COALESCE((data->>'amount_cents')::bigint, 0),
		       status, COALESCE(decided_by::text,'')
		FROM permission_approval_requests
		WHERE status = 'PENDING' OR (decided_at IS NOT NULL AND decided_at >= $1)
		ORDER BY requested_at DESC`, decidedSince)
	if err != nil {
		return nil, fmt.Errorf("approvals for reconcile: %w", err)
	}
	defer rows.Close()
	var out []ApprovalRequest
	for rows.Next() {
		var a ApprovalRequest
		var policy, st string
		if err := rows.Scan(&a.ID, &a.RequestedBy, &a.BranchID, &policy, &a.Detail,
			&a.SubjectRef, &a.AmountCents, &st, &a.DecidedBy); err != nil {
			return nil, fmt.Errorf("scan reconcile row: %w", err)
		}
		a.Policy = PolicyType(policy)
		a.Status = ApprovalStatus(st)
		out = append(out, a)
	}
	return out, rows.Err()
}
