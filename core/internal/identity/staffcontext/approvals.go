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
	// floorApprovers is the margin floor's decide authority from the dealer's
	// role configuration (D11): the floor list's dealer wide approvers plus
	// the branch scoped quote floor approvers (CP0-1 answer B). The zero
	// value falls back to the carried default, which equals r3's ruling.
	floorApprovers FloorApprovers
}

// NewApprovals returns the rail service over one store, with the default
// logger. The ERP constructor took the staff JWT secret alongside the
// store; the rail never reads it, and the secret no longer exists in the
// target configuration.
func NewApprovals(store approvalStore) *Approvals {
	return &Approvals{store: store, log: slog.Default(), floorApprovers: defaultFloorApprovers()}
}

// WithLogger swaps the structured logger (best-effort approvals-rail fan-out logging).
func (s *Approvals) WithLogger(l *slog.Logger) *Approvals {
	if l != nil {
		s.log = l
	}
	return s
}

// WithFloorApprovers wires the margin floor's decide authority from the
// dealer's role configuration (D11): the composition root builds it from
// the configuration's exception_approvers.floor list plus its branch scoped
// quote floor approvers list, mapped onto the ERP role codes the carried
// vocabulary speaks. An empty set is the carried default, never a silent
// widening: the default equals r3's ruling.
func (s *Approvals) WithFloorApprovers(f FloorApprovers) *Approvals {
	if len(f.DealerWide) == 0 && len(f.OwnBranch) == 0 {
		f = defaultFloorApprovers()
	}
	s.floorApprovers = f
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
	// FREIGHT_ABOVE_QUOTE is SYSTEM-ONLY for the same reason and with sharper teeth (0195). It
	// is filed exclusively by dispatch's freight gate, which binds a real PARKED delivery_freight_raises
	// row as subject_ref after establishing the invariant breach against the order's own frozen
	// baseline. Accepting it would let any authenticated staffer file a freight card naming a
	// subject_ref of their choosing (including a raise nobody proposed, or another branch's), which
	// a rail approver could then APPROVE, and approving is what applies the money.
	if policy == PolicyFreightAboveQuote {
		return ApprovalRequest{}, clientErr(http.StatusForbidden, "freight approvals are filed by dispatch, not directly")
	}
	// MARGIN_BELOW_MINIMUM is QUOTE-MODULE-ONLY (D7): the margin request is
	// filed by the quote module alone, through FileFloorApproval, which
	// binds the real quote, line and discount from its own state and builds
	// the detail the approver reads from its own figures. Accepting it here
	// would let any caller file a margin card with a subject and a detail
	// of its own choosing, and the approver would be shown words the server
	// never checked.
	if policy == PolicyMargin {
		return ApprovalRequest{}, clientErr(http.StatusForbidden,
			"margin approvals are filed by the quote desk for a quote line, not directly")
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

// FloorApprovalFiling is the quote module's floor request (D7, plan 1.1):
// every binding figure arrives from the quote module's own state, never
// from a caller's body. SubjectRef is the quote id; LineNo and DiscPct bind
// the request to one line at exactly the discount that was asked, and
// LineID and ProductID bind it to the line's OWN row: a line number is
// reused by the next line added after a delete, the row id never is.
type FloorApprovalFiling struct {
	RequestedBy string
	BranchID    string
	SubjectRef  string
	Detail      string
	LineNo      int
	DiscPct     int
	LineID      string
	ProductID   string
	Note        string
}

// FloorRequestRecord is a filed floor request's state as the quote module
// answers it: the request's id, status, and its line and discount binding.
type FloorRequestRecord struct {
	ID      string
	Status  ApprovalStatus
	LineNo  int
	DiscPct int
}

// FileFloorApproval files a quote floor request (the quote module's door,
// D7). It is the ONLY path a MARGIN_BELOW_MINIMUM request is filed on: the
// subject, the detail and the binding figures are the quote module's own,
// so a caller can never file a margin card with a subject and a detail of
// its own choosing.
func (s *Approvals) FileFloorApproval(ctx context.Context, in FloorApprovalFiling) (FloorRequestRecord, error) {
	if in.LineNo <= 0 {
		return FloorRequestRecord{}, clientErr(http.StatusBadRequest, "line_no is required")
	}
	if in.DiscPct < 0 || in.DiscPct > 100 {
		return FloorRequestRecord{}, clientErr(http.StatusBadRequest, "disc_pct must be between 0 and 100")
	}
	// The row binding is the request's identity: without it the request
	// matches a line NUMBER, which a later line can reuse, and an approval
	// could clear a line that did not exist when it was filed. A filing
	// without the row id and the product is refused, never stored unbound.
	if in.LineID == "" || in.ProductID == "" {
		return FloorRequestRecord{}, clientErr(http.StatusBadRequest,
			"line_id and product_id are required: a floor request is bound to the line's own row")
	}
	req, err := s.fileApproval(ctx, ApprovalRequest{
		RequestedBy: in.RequestedBy,
		BranchID:    in.BranchID,
		Policy:      PolicyMargin,
		Detail:      in.Detail,
		SubjectRef:  in.SubjectRef,
		LineNo:      in.LineNo,
		DiscPct:     in.DiscPct,
		LineID:      in.LineID,
		ProductID:   in.ProductID,
		Note:        in.Note,
		Status:      ApprovalPending,
	})
	if err != nil {
		return FloorRequestRecord{}, err
	}
	return FloorRequestRecord{ID: req.ID, Status: req.Status, LineNo: req.LineNo, DiscPct: req.DiscPct}, nil
}

// FloorRequestFor answers the caller's open request for one quote line: a
// PENDING or APPROVED and unused request bound to that subject and that
// line's own row, so the typed refusal can carry it (plan 1.1's refusal
// body) and the request door can answer an existing request's state instead
// of filing a twin. lineID and productID are the line's OWN identity: a
// line number alone is not, the next line added after a delete reuses it.
func (s *Approvals) FloorRequestFor(ctx context.Context, requestedBy, branchID, subjectRef string, lineNo int, lineID, productID string) (FloorRequestRecord, bool, error) {
	if requestedBy == "" || branchID == "" || subjectRef == "" || lineNo <= 0 || lineID == "" || productID == "" {
		return FloorRequestRecord{}, false, nil
	}
	r, ok := s.store.(floorRequestReader)
	if !ok {
		return FloorRequestRecord{}, false, nil
	}
	return r.FloorRequestFor(ctx, requestedBy, branchID, subjectRef, lineNo, lineID, productID)
}

// floorRequestReader is the store's subject narrowed floor request lookup
// (D3: the store already narrows on a subject when given one).
type floorRequestReader interface {
	FloorRequestFor(ctx context.Context, requestedBy, branchID, subjectRef string, lineNo int, lineID, productID string) (FloorRequestRecord, bool, error)
}

// floorTxConsumer is the store's transactional consumption of an approved
// floor request: the quote module's own transaction carries it, so the used
// up stamp commits or rolls back with the write that used it.
type floorTxConsumer interface {
	ConsumeApprovedFloorTx(ctx context.Context, tx pgx.Tx, requestedBy, branchID, subjectRef string, lineNo, discPct int, lineID, productID string) (FloorRequestRecord, FloorConsumption, error)
}

// FloorConsumption names what a consumed approval was: the request and who
// decided it, for the clearing stamp on the line.
type FloorConsumption struct {
	ApprovalID string
	DecidedBy  string
}

// ConsumeApprovedFloorTx consumes the caller's APPROVED and unused floor
// request for exactly that quote, that line's own row, at no deeper a
// discount than was asked, ON the transaction the quote write holds (D3):
// the first write that uses the request uses it up, in the same commit as
// the line it cleared. ok is false when no such request stands.
func (s *Approvals) ConsumeApprovedFloorTx(ctx context.Context, tx pgx.Tx, requestedBy, branchID, subjectRef string, lineNo, discPct int, lineID, productID string) (FloorRequestRecord, FloorConsumption, error) {
	c, ok := s.store.(floorTxConsumer)
	if !ok {
		return FloorRequestRecord{}, FloorConsumption{}, clientErr(http.StatusServiceUnavailable,
			"the approvals store cannot consume a floor request on this transaction")
	}
	return c.ConsumeApprovedFloorTx(ctx, tx, requestedBy, branchID, subjectRef, lineNo, discPct, lineID, productID)
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
//
// D3: the subject is a PARAMETER, never a hidden empty string at the lookup
// site. The resolver's adapter passes "" explicitly (a generic resolve
// carries no document, and no till or generic route sets OverrideRequested,
// so that arm is unreachable in practice); every quote floor path goes
// through the subject narrowed FloorRequestFor and ConsumeApprovedFloorTx
// instead, so one approved card can no longer clear every below floor
// price for that person at that branch.
func (s *Approvals) HasApprovedOverride(ctx context.Context, requestedBy, branchID, subjectRef string, policy PolicyType) (bool, error) {
	if requestedBy == "" || branchID == "" {
		return false, nil
	}
	return s.store.HasApprovedOverride(ctx, requestedBy, branchID, subjectRef, policy)
}

// ListApprovals returns the requests the caller is allowed to see:
// a GLOBAL user sees every branch; a scoped user sees only their granted branches.
//
// CARRY NOTE (the A3 decide-path re-point, HH-3.2 B5): the ERP signature
// took the caller's user id and re-resolved authority from the identity
// scaffold tables inside this method. Under the carry the caller arrives
// as the bridged StaffContext the guard already resolved from the Phase B
// source (the request path's only authority read, D3); the store's
// scaffold read is gone from the decide path and nothing on it derives
// authority from the scaffold tables.
func (s *Approvals) ListApprovals(ctx context.Context, caller StaffContext, status string) ([]ApprovalRequest, error) {
	var (
		list []ApprovalRequest
		err  error
	)
	if caller.Scope == ScopeGlobal {
		list, err = s.store.ListApprovals(ctx, status, nil)
	} else {
		branches := make([]string, 0, len(caller.Branches))
		for b := range caller.Branches {
			branches = append(branches, b)
		}
		if len(branches) == 0 {
			return []ApprovalRequest{}, nil
		}
		list, err = s.store.ListApprovals(ctx, status, branches)
	}
	if err != nil {
		return nil, err
	}
	// A floor request's margin figures are read by whoever could decide it
	// or filed it, never by the rest of the branch (P2-3, round 3).
	kept := make([]ApprovalRequest, 0, len(list))
	for _, r := range list {
		if s.MayReadRequest(caller, r) {
			kept = append(kept, r)
		}
	}
	return kept, nil
}

// approvalKinds is the closed policy vocabulary a list can be narrowed to (the CHECK constraint's
// set). A kind outside it is a bad request, never an empty answer.
var approvalKinds = []PolicyType{PolicyMargin, PolicyCredit, PolicyCOD, PolicyJEThreshold, PolicyFreightAboveQuote}

// DecidesKind answers whether the caller holds decide authority over at least one request of this
// kind in a branch it reaches, under the configured floor set (exception_approvers). It probes the
// same CanApproveFloor rule the decide path runs: a global caller is probed once, a scoped one at
// each granted branch, and the margin arm at each salesperson role a branch scoped approver may
// decide for (row 743).
func (s *Approvals) DecidesKind(caller StaffContext, kind PolicyType) bool {
	branches := []string{""}
	if caller.Scope != ScopeGlobal {
		branches = branches[:0]
		for b := range caller.Branches {
			branches = append(branches, b)
		}
	}
	roles := []string{""}
	if kind == PolicyMargin {
		roles = []string{"INSIDE_SALES", "OUTSIDE_SALES"}
	}
	for _, b := range branches {
		for _, role := range roles {
			if CanApproveFloor(caller, kind, b, role, s.floorApprovers) {
				return true
			}
		}
	}
	return false
}

// ListApprovalsByKind is ListApprovals narrowed to one policy kind, for a kind the caller decides.
// An empty kind is the unchanged list. An unknown kind is a 400; a known kind the caller holds no
// decide authority over is a 403, so a surface drawing a tab per kind has one honest answer for
// which tabs exist. The narrowing runs after ListApprovals, so the branch fence and the floor
// request read rule apply exactly as they do to the whole list.
func (s *Approvals) ListApprovalsByKind(ctx context.Context, caller StaffContext, status string, kind PolicyType) ([]ApprovalRequest, error) {
	if kind == "" {
		return s.ListApprovals(ctx, caller, status)
	}
	known := false
	for _, k := range approvalKinds {
		known = known || k == kind
	}
	if !known {
		return nil, clientErr(http.StatusBadRequest, "unknown approval kind")
	}
	if !s.DecidesKind(caller, kind) {
		return nil, clientErr(http.StatusForbidden, "you do not decide %s approvals", kind)
	}
	list, err := s.ListApprovals(ctx, caller, status)
	if err != nil {
		return nil, err
	}
	out := make([]ApprovalRequest, 0, len(list))
	for _, r := range list {
		if r.Policy == kind {
			out = append(out, r)
		}
	}
	return out, nil
}

// DecideApproval enforces the authority model, bridges a JE decision to the GL maker-checker (GL first),
// then persists the decision via a compare-and-swap and fires the decision echo.
//
// CARRY NOTE (the A3 decide-path re-point, HH-3.2 B5): the ERP signature
// took the caller's user id and re-resolved authority from the identity
// scaffold tables inside this method, which fails open on scaffold drift
// (a revoked grant could still decide money). Under the carry the caller
// arrives as the bridged StaffContext the guard already resolved from the
// Phase B source (staff_role_grant per request, D3), with the ERP users.id
// the linkage supplied on the stamp: decide-time authority never reads the
// scaffold tables on any path.
func (s *Approvals) DecideApproval(ctx context.Context, id string, caller StaffContext, approve bool, reason string) error {
	// Reject a malformed (non-UUID) approval id before it reaches the uuid PK cast (22P02 to HTTP 500).
	if _, err := uuid.Parse(id); err != nil {
		return clientErr(http.StatusBadRequest, "invalid approval id")
	}
	req, err := s.store.GetApproval(ctx, id)
	if err != nil {
		return err
	}
	// Validate authority + PENDING-state + four-eyes in memory (sets req.Status). Nothing persists yet, so
	// a rejected decide never touches the ledger or the request row. The margin arm's authority is the
	// dealer's configured set (D11, CP0-1 answer B), carried on the service.
	if err := req.DecideWithFloor(caller, approve, s.floorApprovers); err != nil {
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
// the pin (the session-path halves of the ERP Repository stay behind).
// Its SQL is carried byte-identical except the three write statements,
// which now draw a per-scope feed seq from the approvals counter
// (scope_sequences, the 0213 convention) beside the write: a desk filing
// or decision is the row change a device's stale replay must meet, so
// every write path the rail owns stamps the seq the sync feed orders on
// (the tasks and dispatch modules' own recorded posture).
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
	// The quote floor binding (D3, D7) rides the same column: line_no, disc_pct, the line's own row id
	// and product, and the requester's note.
	data := map[string]any{}
	if req.AmountCents != 0 {
		data["amount_cents"] = req.AmountCents
	}
	if req.LineNo > 0 || req.DiscPct != 0 || req.Note != "" {
		data["line_no"] = req.LineNo
		data["disc_pct"] = req.DiscPct
		if req.LineID != "" {
			data["line_id"] = req.LineID
		}
		if req.ProductID != "" {
			data["product_id"] = req.ProductID
		}
		if req.Note != "" {
			data["note"] = req.Note
		}
	}
	raw := []byte(`{}`)
	if len(data) > 0 {
		if b, err := json.Marshal(data); err == nil {
			raw = b
		}
	}
	var id string
	err := r.pool.QueryRow(ctx, approvalsSeqCTE+`
		INSERT INTO permission_approval_requests (requested_by, branch_id, policy_type, detail, subject_ref, data, status, seq)
		VALUES ($1, $2, $3, $4, $5, $6, 'PENDING', (SELECT next_value FROM aseq))
		RETURNING id::text, `+requesterRoleSQL("requested_by"),
		req.RequestedBy, req.BranchID, string(req.Policy), req.Detail, subject, raw).Scan(&id, &req.RequesterRole)
	if err != nil {
		return ApprovalRequest{}, fmt.Errorf("create approval: %w", err)
	}
	req.ID = id
	req.Status = ApprovalPending
	return req, nil
}

// requesterRoleSQL is the SQL that reads the ERP role code of the user a
// request's requested_by names (row 743): the user's platform link, the
// live staff_role_grant rows of that platform user, the role's origin_code
// (the ERP name the carried predicates key on). The user must hold exactly
// ONE role across its grants and that role must carry an ERP name, else the
// answer is the empty string, which the floor arm reads as no salesperson:
// the request reaches leadership only. col is the requester's user id column
// or parameter (a uuid); the expression is a scalar subquery.
func requesterRoleSQL(col string) string {
	return requesterRoleHead + col + requesterRoleTail
}

// requesterRoleHead and requesterRoleTail are the two halves of the
// expression around the requester column, constants so a const projection
// (the sync door's column list) can carry the same read.
const (
	requesterRoleHead = `COALESCE((SELECT CASE WHEN COUNT(DISTINCT g.role_id) = 1 THEN MIN(sr.origin_code) END
		FROM users ru
		JOIN staff_role_grant g ON g.platform_user_id = ru.platform_user_id AND g.deleted_at IS NULL
		JOIN staff_role sr ON sr.id = g.role_id
		WHERE ru.id = `
	requesterRoleTail = `), '')`
)

// ListApprovals returns requests filtered by status (empty = any) and, when branches
// is non-nil, restricted to those branches.
func (r *ApprovalRepository) ListApprovals(ctx context.Context, status string, branches []string) ([]ApprovalRequest, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	// Select subject_ref + amount so the rail feed (GET /api/v1/staff/approvals) can render a faithful
	// JE card (money magnitude + journal binding), matching ApprovalsForReconcile. line_id rides along
	// so the queue's count can hold the decide path's branch arm rule (P2-C).
	q := `SELECT id::text, requested_by::text, branch_id::text, policy_type, detail,
	             COALESCE(subject_ref,''), COALESCE((data->>'amount_cents')::bigint, 0),
	             COALESCE(data->>'line_id',''),
	             status, COALESCE(decided_by::text,''), ` + requesterRoleSQL("requested_by") + `, requested_at
	      FROM permission_approval_requests WHERE deleted_at IS NULL AND ($1 = '' OR status = $1)`
	args := []any{status}
	if branches != nil {
		q += ` AND branch_id = ANY($2::uuid[])`
		args = append(args, branches)
	}
	q += ` ORDER BY requested_at DESC, id`
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
			&a.SubjectRef, &a.AmountCents, &a.LineID, &st, &a.DecidedBy, &a.RequesterRole, &a.RequestedAt); err != nil {
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
	// line_id rides along for the decide path's branch arm rule (P2-C): a margin request with
	// no line binding is decidable by the dealer wide floor set alone.
	err := r.pool.QueryRow(ctx, `
		SELECT id::text, requested_by::text, branch_id::text, policy_type, detail,
		       COALESCE(subject_ref,''), COALESCE((data->>'amount_cents')::bigint, 0),
		       COALESCE(data->>'line_id',''),
		       status, COALESCE(decided_by::text,''), `+requesterRoleSQL("requested_by")+`
		FROM permission_approval_requests WHERE id = $1 AND deleted_at IS NULL`, id).Scan(
		&a.ID, &a.RequestedBy, &a.BranchID, &policy, &a.Detail,
		&a.SubjectRef, &a.AmountCents, &a.LineID, &st, &a.DecidedBy, &a.RequesterRole)
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
	ct, err := r.pool.Exec(ctx, approvalsSeqCTE+`
		UPDATE permission_approval_requests
		SET status = $2, decided_by = $3, decided_at = now(), rejection_reason = $4,
		    seq = (SELECT next_value FROM aseq)
		WHERE id = $1 AND status = 'PENDING' AND deleted_at IS NULL`, id, string(status), decidedBy, reason)
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
	ct, err := tx.Exec(ctx, approvalsSeqCTE+`
		UPDATE permission_approval_requests
		SET status = $2, decided_by = $3, decided_at = now(), rejection_reason = $4,
		    seq = (SELECT next_value FROM aseq)
		WHERE id = $1 AND status = 'PENDING' AND deleted_at IS NULL`, id, string(status), decidedBy, reason)
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
			  AND deleted_at IS NULL
			  AND ($4 = '' OR subject_ref = $4)
		)`, requestedBy, branchID, string(policy), subjectRef).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check approved override: %w", err)
	}
	return ok, nil
}

// HasBlockingJEApproval reports whether a JE_OVER_THRESHOLD request for this journal holds it: any request
// in a state other than approved, a rejected one and a tombstoned one included (an approved request has
// already posted the journal). The subject_ref is TEXT and the id is compared as a uuid, so the letter case
// of the id cannot hide a request. Tombstones are counted on purpose: deleting the request must not free
// the journal for the GL's own route.
func (r *ApprovalRepository) HasBlockingJEApproval(ctx context.Context, journalID string) (bool, error) {
	if r.pool == nil {
		return false, errNoDB
	}
	var ok bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM permission_approval_requests
			WHERE policy_type = 'JE_OVER_THRESHOLD'
			  AND lower(subject_ref) = $1::uuid::text
			  AND NOT (status = 'APPROVED' AND deleted_at IS NULL))`, journalID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check held journal request: %w", err)
	}
	return ok, nil
}

// FloorRequestFor answers the caller's open quote floor request for one
// subject quote and line: a PENDING request, or an APPROVED one no write
// has used up (the used state lives in the data JSON column, D3: no schema
// change, no new status word). D3's narrowing is here: the subject and the
// line are always given, one approved card clears one line of one quote.
// The line match is the line's OWN row id and product, never the number
// alone: the next line added after a delete reuses the number.
func (r *ApprovalRepository) FloorRequestFor(ctx context.Context, requestedBy, branchID, subjectRef string, lineNo int, lineID, productID string) (FloorRequestRecord, bool, error) {
	if r.pool == nil {
		return FloorRequestRecord{}, false, errNoDB
	}
	var rec FloorRequestRecord
	var status string
	err := r.pool.QueryRow(ctx, `
		SELECT id::text, status, COALESCE((data->>'line_no')::int, 0), COALESCE((data->>'disc_pct')::int, 0)
		FROM permission_approval_requests
		WHERE requested_by = $1 AND branch_id = $2 AND policy_type = 'MARGIN_BELOW_MINIMUM'
		  AND deleted_at IS NULL
		  AND subject_ref = $3 AND COALESCE((data->>'line_no')::int, 0) = $4
		  AND COALESCE(data->>'line_id', '') = $5
		  AND COALESCE(data->>'product_id', '') = $6
		  AND (status = 'PENDING' OR (status = 'APPROVED' AND (data->>'used') IS NULL))
		  -- An APPROVED and unused request outranks a newer PENDING twin:
		  -- the newest row alone would hide a request already decided
		  -- behind one nobody has decided yet.
		ORDER BY (status = 'APPROVED' AND (data->>'used') IS NULL) DESC, requested_at DESC LIMIT 1`,
		requestedBy, branchID, subjectRef, lineNo, lineID, productID).Scan(&rec.ID, &status, &rec.LineNo, &rec.DiscPct)
	if errors.Is(err, pgx.ErrNoRows) {
		return FloorRequestRecord{}, false, nil
	}
	if err != nil {
		return FloorRequestRecord{}, false, fmt.Errorf("read floor request: %w", err)
	}
	rec.Status = ApprovalStatus(status)
	return rec, true, nil
}

// ConsumeApprovedFloorTx consumes the caller's APPROVED and unused floor
// request for exactly that subject and line's own row, at no deeper a
// discount than the request was filed at (a shallower write is covered, a
// deeper one is not), ON the caller's transaction: the used up stamp (the
// data JSON column, D3) commits or rolls back with the quote write that
// used it. The compare-and-swap on the unused state is what makes the
// request usable once: two racing writes, one stamp.
func (r *ApprovalRepository) ConsumeApprovedFloorTx(ctx context.Context, tx pgx.Tx, requestedBy, branchID, subjectRef string, lineNo, discPct int, lineID, productID string) (FloorRequestRecord, FloorConsumption, error) {
	var rec FloorRequestRecord
	var status, decidedBy string
	err := tx.QueryRow(ctx, approvalsSeqCTE+`
		UPDATE permission_approval_requests
		SET data = data || jsonb_build_object('used', true, 'used_by_disc_pct', $6::text),
		    seq = (SELECT next_value FROM aseq)
		WHERE id = (
			SELECT id FROM permission_approval_requests
			WHERE requested_by = $1 AND branch_id = $2 AND policy_type = 'MARGIN_BELOW_MINIMUM'
			  AND deleted_at IS NULL
			  AND subject_ref = $3 AND status = 'APPROVED' AND (data->>'used') IS NULL
			  AND COALESCE((data->>'line_no')::int, 0) = $4
			  AND COALESCE((data->>'disc_pct')::int, 0) >= $5
			  AND COALESCE(data->>'line_id', '') = $7
			  AND COALESCE(data->>'product_id', '') = $8
			ORDER BY requested_at DESC LIMIT 1
			FOR UPDATE
		)
		RETURNING id::text, status, COALESCE((data->>'line_no')::int, 0), COALESCE((data->>'disc_pct')::int, 0),
		          COALESCE(decided_by::text, '')`,
		requestedBy, branchID, subjectRef, lineNo, discPct, fmt.Sprintf("%d", discPct), lineID, productID).
		Scan(&rec.ID, &status, &rec.LineNo, &rec.DiscPct, &decidedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return FloorRequestRecord{}, FloorConsumption{}, nil
	}
	if err != nil {
		return FloorRequestRecord{}, FloorConsumption{}, fmt.Errorf("consume approved floor request: %w", err)
	}
	rec.Status = ApprovalStatus(status)
	return rec, FloorConsumption{ApprovalID: rec.ID, DecidedBy: decidedBy}, nil
}

// ResolveStaff loads one user's CURRENT authority (status, tier, scope,
// granted branches) keyed by users.id.
//
// CARRY NOTE (the A3 decide-path re-point, HH-3.2 B5): the carried ERP
// query read the identity scaffold tables (users, user_roles, roles,
// user_branch_grants) without the tombstone columns those reads predate.
// This read is RE-POINTED at the canonical source: the role, tier and
// scope come from staff_role through the user's linked grants
// (users.platform_user_id, the same linkage column the wire's linker
// reads; migration 0215), tombstones honored on every leg, the winning
// grant chosen by the Phase B resolver's rule (lowest tier, then role id)
// so this projection and the guard can never disagree on the vocabulary,
// and the ERP role code from staff_role.origin_code (the bridge's mapping
// in data form). It is a PROJECTION read for document surfaces that name a
// person by their ERP id (lien's co-signer); decide-time authority never
// runs through it (DecideApproval takes the guard-resolved caller), and
// until HH-5 adds the org dimension the grant read crosses no org
// predicate because the frozen data holds one org.
func (r *ApprovalRepository) ResolveStaff(ctx context.Context, userID string) (StaffContext, error) {
	if r.pool == nil {
		return StaffContext{}, errNoDB
	}
	var status, platformID string
	err := r.pool.QueryRow(ctx,
		`SELECT u.status, COALESCE(u.platform_user_id,'') FROM users u WHERE u.id = $1 AND u.deleted_at IS NULL`,
		userID).Scan(&status, &platformID)
	if err != nil {
		return StaffContext{}, fmt.Errorf("resolve staff %q: %w", userID, err)
	}
	out := StaffContext{UserID: userID, Status: status, Branches: map[string]bool{}}
	if platformID == "" {
		// No platform link: no canonical grants exist, and the projection
		// resolves an unlinked person to no authority rather than to the
		// scaffold's stale copy (fail closed, same as the guard chain).
		return out, nil
	}
	var roleCode, scope string
	var tier int
	err = r.pool.QueryRow(ctx, `
		SELECT COALESCE(sr.origin_code,''), COALESCE(sr.tier,0), COALESCE(sr.scope,'branch')
		FROM staff_role_grant g
		JOIN staff_role sr ON sr.id = g.role_id
		WHERE g.platform_user_id = $1 AND g.deleted_at IS NULL AND sr.deleted_at IS NULL
		ORDER BY sr.tier, sr.id
		LIMIT 1`, platformID).Scan(&roleCode, &tier, &scope)
	if err != nil {
		return StaffContext{}, fmt.Errorf("resolve staff %q: %w", userID, err)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT ubg.branch_id::text
		FROM user_branch_grants ubg
		WHERE ubg.user_id = $1 AND ubg.deleted_at IS NULL
		ORDER BY ubg.branch_id`, userID)
	if err != nil {
		return StaffContext{}, fmt.Errorf("resolve staff %q: %w", userID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return StaffContext{}, fmt.Errorf("resolve staff %q: %w", userID, err)
		}
		out.Branches[b] = true
	}
	if err := rows.Err(); err != nil {
		return StaffContext{}, fmt.Errorf("resolve staff %q: %w", userID, err)
	}
	out.RoleCode = roleCode
	out.Tier = Tier(tier)
	switch scope {
	case "global":
		out.Scope = ScopeGlobal
	default:
		out.Scope = ScopeBranch
	}
	return out, nil
}

// ResolveStaffInOrg is ResolveStaff with the winning-grant read scoped
// to ONE org: the per-org authority resolution the request path's
// guard carries (D3), which the rail's sync door needs because its
// caller arrives as the middleware-stamped org filter rather than a
// guard-resolved caller. A user whose grants all sit in another org
// resolves to NO authority here (the empty context), exactly as the
// guard answers the RBAC-403 for no active grant in the org; the
// caller keeps the branch grants read unscoped, which only widens the
// branch-reachability facts CanApprove re-checks.
func (r *ApprovalRepository) ResolveStaffInOrg(ctx context.Context, userID, orgID string) (StaffContext, error) {
	if r.pool == nil {
		return StaffContext{}, errNoDB
	}
	var status, platformID string
	err := r.pool.QueryRow(ctx,
		`SELECT u.status, COALESCE(u.platform_user_id,'') FROM users u WHERE u.id = $1 AND u.deleted_at IS NULL`,
		userID).Scan(&status, &platformID)
	if err != nil {
		return StaffContext{}, fmt.Errorf("resolve staff %q: %w", userID, err)
	}
	out := StaffContext{UserID: userID, Status: status, Branches: map[string]bool{}}
	if platformID == "" {
		// No platform link: no canonical grants exist, and the
		// projection resolves an unlinked person to no authority
		// rather than to the scaffold's stale copy (fail closed, the
		// same rule ResolveStaff carries).
		return out, nil
	}
	var roleCode, scope string
	var tier int
	err = r.pool.QueryRow(ctx, `
		SELECT COALESCE(sr.origin_code,''), COALESCE(sr.tier,0), COALESCE(sr.scope,'branch')
		FROM staff_role_grant g
		JOIN staff_role sr ON sr.id = g.role_id
		WHERE g.platform_user_id = $1 AND g.org_id = $2::uuid
		  AND g.deleted_at IS NULL AND sr.deleted_at IS NULL
		ORDER BY sr.tier, sr.id
		LIMIT 1`, platformID, orgID).Scan(&roleCode, &tier, &scope)
	if errors.Is(err, pgx.ErrNoRows) {
		// No active grant in this org: no authority, fail closed (the
		// empty context CanApprove refuses everything).
		return out, nil
	}
	if err != nil {
		return StaffContext{}, fmt.Errorf("resolve staff in org %s: %w", orgID, err)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT ubg.branch_id::text
		FROM user_branch_grants ubg
		WHERE ubg.user_id = $1 AND ubg.deleted_at IS NULL
		ORDER BY ubg.branch_id`, userID)
	if err != nil {
		return StaffContext{}, fmt.Errorf("resolve staff %q: %w", userID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return StaffContext{}, fmt.Errorf("resolve staff %q: %w", userID, err)
		}
		out.Branches[b] = true
	}
	if err := rows.Err(); err != nil {
		return StaffContext{}, fmt.Errorf("resolve staff %q: %w", userID, err)
	}
	out.RoleCode = roleCode
	out.Tier = Tier(tier)
	switch scope {
	case "global":
		out.Scope = ScopeGlobal
	default:
		out.Scope = ScopeBranch
	}
	return out, nil
}

// ManagerInOrg answers whether one platform subject holds an active
// manager-or-leadership grant (tier 1 or 2, the guard's IsManager) in
// one org: the billing sync door's narrow authority check, declared by
// the consuming module and answered by this one because the identity
// scaffold reads belong here.
func (r *ApprovalRepository) ManagerInOrg(ctx context.Context, platformUserID, orgID string) (bool, error) {
	if r.pool == nil {
		return false, errNoDB
	}
	var ok bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM staff_role_grant g
			JOIN staff_role sr ON sr.id = g.role_id
			WHERE g.platform_user_id = $1 AND g.org_id = $2::uuid
			  AND g.deleted_at IS NULL AND sr.deleted_at IS NULL
			  AND sr.tier <= 2
		)`, platformUserID, orgID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check manager in org: %w", err)
	}
	return ok, nil
}

// ListApproverCandidates returns every ACTIVE staff member WITH an assigned role (an unassigned user can
// approve nothing) projected as a StaffContext: the fan-out candidate pool ResolveApprovers filters via
// CanApprove (0075). Role/branch is read straight from the DB, so a promotion/transfer is reflected on the
// next fan-out or reconcile pass.
//
// CARRY NOTE (the A3 candidate-pool tombstones, HH-3.2 B5): the carried
// query predates the tombstone columns migration 0213 added, so a
// tombstoned user, role assignment or branch grant kept a person in the
// card pool after their access was revoked. Every leg now honors
// deleted_at. The pool is FAN-OUT ONLY (best-effort card targeting):
// decide-time authority comes from the caller the guard resolved, never
// from this read, so the scaffold role vocabulary it still reads cannot
// grant anyone the power to decide money.
func (r *ApprovalRepository) ListApproverCandidates(ctx context.Context) ([]StaffContext, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT u.id::text, COALESCE(ro.code::text,''), COALESCE(ro.tier,0), COALESCE(ro.scope,'BRANCH'), u.status,
		       COALESCE(array_remove(array_agg(DISTINCT ubg.branch_id::text), NULL), ARRAY[]::text[])
		FROM users u
		JOIN user_roles ur ON ur.user_id = u.id AND ur.deleted_at IS NULL
		JOIN roles ro ON ro.id = ur.role_id AND ro.deleted_at IS NULL
		LEFT JOIN user_branch_grants ubg ON ubg.user_id = u.id AND ubg.deleted_at IS NULL
		WHERE u.status = 'ACTIVE' AND u.deleted_at IS NULL
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
		       status, COALESCE(decided_by::text,''), `+requesterRoleSQL("requested_by")+`
		FROM permission_approval_requests
		WHERE deleted_at IS NULL AND (status = 'PENDING' OR (decided_at IS NOT NULL AND decided_at >= $1))
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
			&a.SubjectRef, &a.AmountCents, &st, &a.DecidedBy, &a.RequesterRole); err != nil {
			return nil, fmt.Errorf("scan reconcile row: %w", err)
		}
		a.Policy = PolicyType(policy)
		a.Status = ApprovalStatus(st)
		out = append(out, a)
	}
	return out, rows.Err()
}
