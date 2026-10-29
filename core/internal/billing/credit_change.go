package billing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// Credit change request statuses (0454 CHECK set).
const (
	CreditChangeStatusPending  = "PENDING"
	CreditChangeStatusApproved = "APPROVED"
	CreditChangeStatusApplied  = "APPLIED"
	CreditChangeStatusRejected = "REJECTED"
)

// Credit change kinds (0454 CHECK set).
const (
	CreditChangeKindLimit = "LIMIT_CHANGE"
	CreditChangeKindHold  = "HOLD"
)

// Default per-dealer increase approval thresholds in cents.
const (
	DefaultCreditIncreaseFirstThresholdCents  int64 = 0
	DefaultCreditIncreaseSecondThresholdCents int64 = 2500000 // $25,000.00
)

// Audit action names for credit change requests and decisions.
const (
	ActCreditChangeRequested = "billing.credit_change.requested"
	ActCreditChangeApproved  = "billing.credit_change.approved"
	ActCreditChangeApplied   = "billing.credit_change.applied"
	ActCreditChangeRejected  = "billing.credit_change.rejected"
)

// Staff role codes for credit change approvals.
const (
	RoleBranchManager       = "BRANCH_MANAGER"
	RoleFinancialController = "FINANCIAL_CONTROLLER"
	RoleGeneralManager      = "GENERAL_MANAGER"
)

// Errors returned by CreditChangeService.
var (
	ErrCreditChangeUnauthorized      = errors.New("credit change: authentication required")
	ErrCreditChangeForbidden         = errors.New("credit change: only a manager may request or decide a credit change")
	ErrCreditChangeRoleForbidden     = errors.New("credit change: caller role is not permitted to approve this request")
	ErrCreditChangeSelfApproval      = errors.New("credit change: requester cannot approve their own request")
	ErrCreditChangeDuplicateApprover = errors.New("credit change: the same person cannot approve twice")
	ErrCreditChangeNotFound          = errors.New("credit change request not found")
	ErrCreditChangeCustomerNotFound  = errors.New("customer not found")
	ErrCreditChangeNotPending        = errors.New("credit change: request is not pending")
	ErrCreditChangeReasonRequired    = errors.New("credit change: a reason is required")
	ErrCreditChangeInvalidTarget     = errors.New("credit change: specify either a non-negative new_limit_cents or a hold, not both")
	ErrCreditChangeNotConfigured     = errors.New("credit change: service store not configured")
)

// CustomerCreditFacts holds the customer's home branch and current credit limit.
type CustomerCreditFacts struct {
	CustomerID       string
	HomeBranchID     string
	CreditLimitCents int64
}

// CreditCustomerStore is the narrow seam into the customer module so billing never writes
// customers directly in SQL.
type CreditCustomerStore interface {
	CustomerCreditFacts(ctx context.Context, customerID string) (CustomerCreditFacts, bool, error)
	SetCustomerCreditLimit(ctx context.Context, customerID string, newLimitCents int64) (previousCents int64, err error)
}

// CustomerCreditService is the subset of *customer.Service used by NewCustomerCreditStore.
type CustomerCreditService interface {
	ResolveAccount(ctx context.Context, customerID string) (customer.Account, error)
	UpdateCreditLimit(ctx context.Context, customerID string, newLimitCents int64) (int64, error)
}

type customerCreditAdapter struct {
	svc CustomerCreditService
}

// NewCustomerCreditStore adapts *customer.Service to CreditCustomerStore.
func NewCustomerCreditStore(svc CustomerCreditService) CreditCustomerStore {
	if svc == nil {
		return nil
	}
	return customerCreditAdapter{svc: svc}
}

func (a customerCreditAdapter) CustomerCreditFacts(ctx context.Context, customerID string) (CustomerCreditFacts, bool, error) {
	if _, err := uuid.Parse(strings.TrimSpace(customerID)); err != nil {
		return CustomerCreditFacts{}, false, nil
	}
	acct, err := a.svc.ResolveAccount(ctx, customerID)
	if err != nil {
		if errors.Is(err, customer.ErrNotFound) {
			return CustomerCreditFacts{}, false, nil
		}
		return CustomerCreditFacts{}, false, err
	}
	return CustomerCreditFacts{
		CustomerID:       acct.CustomerID,
		HomeBranchID:     acct.BranchID,
		CreditLimitCents: acct.CreditLimitCents,
	}, true, nil
}

func (a customerCreditAdapter) SetCustomerCreditLimit(ctx context.Context, customerID string, newLimitCents int64) (int64, error) {
	prev, err := a.svc.UpdateCreditLimit(ctx, customerID, newLimitCents)
	if err != nil {
		if errors.Is(err, customer.ErrNotFound) {
			return 0, ErrCreditChangeCustomerNotFound
		}
		return 0, err
	}
	return prev, nil
}

// CreditChangeStore is the persistence seam for credit_change_requests, ar_credit_policy, and holds.
type CreditChangeStore interface {
	CreditPolicy(ctx context.Context, branchID string) (CreditPolicy, error)
	PlaceHold(ctx context.Context, customerID, kind, reason, placedBy string) (id string, placed bool, err error)
	CreateCreditChangeRequest(ctx context.Context, req CreditChangeRequest) (CreditChangeRequest, error)
	GetCreditChangeRequest(ctx context.Context, id string) (CreditChangeRequest, error)
	UpdateCreditChangeRequest(ctx context.Context, req CreditChangeRequest) (CreditChangeRequest, error)
	ListCreditChangeRequests(ctx context.Context, filter CreditChangeFilter, reach BranchReach) ([]CreditChangeRequest, error)
}

// CreditChangeRequest is one row in credit_change_requests.
type CreditChangeRequest struct {
	ID                   string     `json:"id"`
	CustomerID           string     `json:"customer_id"`
	BranchID             string     `json:"branch_id,omitempty"`
	ChangeKind           string     `json:"change_kind"`
	PreviousLimitCents   int64      `json:"previous_limit_cents"`
	NewLimitCents        *int64     `json:"new_limit_cents,omitempty"`
	DeltaCents           int64      `json:"delta_cents"`
	HoldKind             string     `json:"hold_kind,omitempty"`
	HoldID               string     `json:"hold_id,omitempty"`
	Reason               string     `json:"reason"`
	Status               string     `json:"status"`
	RequiredApprovals    int        `json:"required_approvals"`
	FirstThresholdCents  int64      `json:"first_threshold_cents"`
	SecondThresholdCents int64      `json:"second_threshold_cents"`
	RequestedBy          string     `json:"requested_by"`
	RequestedByRole      string     `json:"requested_by_role,omitempty"`
	RequestedAt          time.Time  `json:"requested_at"`
	FirstApprovedBy      string     `json:"first_approved_by,omitempty"`
	FirstApprovedByRole  string     `json:"first_approved_by_role,omitempty"`
	FirstApprovedAt      *time.Time `json:"first_approved_at,omitempty"`
	FirstApprovalNote    string     `json:"first_approval_note,omitempty"`
	SecondApprovedBy     string     `json:"second_approved_by,omitempty"`
	SecondApprovedByRole string     `json:"second_approved_by_role,omitempty"`
	SecondApprovedAt     *time.Time `json:"second_approved_at,omitempty"`
	SecondApprovalNote   string     `json:"second_approval_note,omitempty"`
	RejectedBy           string     `json:"rejected_by,omitempty"`
	RejectedByRole       string     `json:"rejected_by_role,omitempty"`
	RejectedAt           *time.Time `json:"rejected_at,omitempty"`
	RejectionReason      string     `json:"rejection_reason,omitempty"`
	AppliedAt            *time.Time `json:"applied_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// CreateCreditChangeInput is the request payload to raise a credit change.
type CreateCreditChangeInput struct {
	CustomerID    string `json:"customer_id"`
	NewLimitCents *int64 `json:"new_limit_cents,omitempty"`
	PlaceHold     bool   `json:"place_hold,omitempty"`
	HoldKind      string `json:"hold_kind,omitempty"`
	Reason        string `json:"reason"`
}

// DecideCreditChangeInput carries an optional note or rejection reason.
type DecideCreditChangeInput struct {
	Note   string `json:"note,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// CreditChangeFilter filters the credit change list.
type CreditChangeFilter struct {
	CustomerID string
	BranchID   string
	Status     string
	Limit      int
}

// CreditChangeService enforces the credit change request and approval rules.
type CreditChangeService struct {
	store      CreditChangeStore
	customers  CreditCustomerStore
	audit      audit.Recorder
	clock      func() time.Time
	callerFrom func(context.Context) (identity.StaffContext, bool)
}

// NewCreditChangeService constructs the credit change service.
func NewCreditChangeService(store CreditChangeStore, customers CreditCustomerStore) *CreditChangeService {
	return &CreditChangeService{
		store:      store,
		customers:  customers,
		clock:      time.Now,
		callerFrom: identity.StaffContextFromContext,
	}
}

// WithAudit attaches the audit recorder.
func (s *CreditChangeService) WithAudit(r audit.Recorder) *CreditChangeService {
	s.audit = r
	return s
}

// WithClock overrides the clock for deterministic tests.
func (s *CreditChangeService) WithClock(fn func() time.Time) *CreditChangeService {
	if fn != nil {
		s.clock = fn
	}
	return s
}

func (s *CreditChangeService) requireManagerCaller(ctx context.Context) (identity.StaffContext, error) {
	if s.store == nil || s.customers == nil {
		return identity.StaffContext{}, ErrCreditChangeNotConfigured
	}
	caller, ok := s.callerFrom(ctx)
	if !ok {
		return identity.StaffContext{}, ErrCreditChangeUnauthorized
	}
	if caller.Status != "" && caller.Status != identity.StatusActive {
		return identity.StaffContext{}, ErrCreditChangeForbidden
	}
	if !caller.IsManager() || caller.RoleCode == "HR" {
		return identity.StaffContext{}, ErrCreditChangeForbidden
	}
	return caller, nil
}

func canFirstApproveCreditIncrease(roleCode string) bool {
	switch roleCode {
	case RoleBranchManager, RoleFinancialController, RoleGeneralManager, identity.RoleSuperAdmin:
		return true
	default:
		return false
	}
}

func canSecondApproveCreditIncrease(roleCode string) bool {
	switch roleCode {
	case RoleFinancialController, RoleGeneralManager, identity.RoleSuperAdmin:
		return true
	default:
		return false
	}
}

func validHoldKind(kind string) bool {
	switch kind {
	case HoldNSF, HoldARAging, HoldCreditLimit, HoldCOD, HoldManual:
		return true
	default:
		return false
	}
}

// Request creates a credit change request. A credit limit decrease or credit hold is applied at
// once by a manager. A credit limit increase requires one approver up to the dealer's second
// threshold (default $25,000) and a second approver above it.
func (s *CreditChangeService) Request(ctx context.Context, in CreateCreditChangeInput) (CreditChangeRequest, error) {
	caller, err := s.requireManagerCaller(ctx)
	if err != nil {
		return CreditChangeRequest{}, err
	}
	customerID := strings.TrimSpace(in.CustomerID)
	if customerID == "" {
		return CreditChangeRequest{}, ErrCreditChangeCustomerNotFound
	}
	cust, found, err := s.customers.CustomerCreditFacts(ctx, customerID)
	if err != nil {
		return CreditChangeRequest{}, err
	}
	if !found || !caller.CanReachAccountHome(cust.HomeBranchID) {
		return CreditChangeRequest{}, ErrCreditChangeCustomerNotFound
	}
	if caller.UserID == "" {
		return CreditChangeRequest{}, ErrCreditChangeUnauthorized
	}

	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return CreditChangeRequest{}, ErrCreditChangeReasonRequired
	}

	var (
		changeKind    string
		newLimitCents *int64
		deltaCents    int64
		holdKind      string
	)
	switch {
	case (in.PlaceHold || strings.TrimSpace(in.HoldKind) != "") && in.NewLimitCents == nil:
		changeKind = CreditChangeKindHold
		holdKind = strings.TrimSpace(in.HoldKind)
		if holdKind == "" {
			holdKind = HoldManual
		}
		if !validHoldKind(holdKind) {
			return CreditChangeRequest{}, ErrCreditChangeInvalidTarget
		}
	case !in.PlaceHold && strings.TrimSpace(in.HoldKind) == "" && in.NewLimitCents != nil && *in.NewLimitCents >= 0:
		changeKind = CreditChangeKindLimit
		limitCopy := *in.NewLimitCents
		newLimitCents = &limitCopy
		deltaCents = limitCopy - cust.CreditLimitCents
	default:
		return CreditChangeRequest{}, ErrCreditChangeInvalidTarget
	}

	pol, err := s.store.CreditPolicy(ctx, cust.HomeBranchID)
	if err != nil {
		return CreditChangeRequest{}, err
	}
	firstThreshold := pol.IncreaseFirstThresholdCents
	if firstThreshold < 0 {
		firstThreshold = DefaultCreditIncreaseFirstThresholdCents
	}
	secondThreshold := pol.IncreaseSecondThresholdCents
	if secondThreshold <= 0 && firstThreshold == 0 {
		secondThreshold = DefaultCreditIncreaseSecondThresholdCents
	}
	if secondThreshold < firstThreshold {
		secondThreshold = firstThreshold
	}

	requiredApprovals := 0
	status := CreditChangeStatusApplied
	if changeKind == CreditChangeKindLimit && deltaCents > firstThreshold {
		status = CreditChangeStatusPending
		if deltaCents <= secondThreshold {
			requiredApprovals = 1
		} else {
			requiredApprovals = 2
		}
	}

	now := s.clock().UTC()
	req := CreditChangeRequest{
		CustomerID:           cust.CustomerID,
		BranchID:             cust.HomeBranchID,
		ChangeKind:           changeKind,
		PreviousLimitCents:   cust.CreditLimitCents,
		NewLimitCents:        newLimitCents,
		DeltaCents:           deltaCents,
		HoldKind:             holdKind,
		Reason:               reason,
		Status:               status,
		RequiredApprovals:    requiredApprovals,
		FirstThresholdCents:  firstThreshold,
		SecondThresholdCents: secondThreshold,
		RequestedBy:          caller.UserID,
		RequestedByRole:      caller.RoleCode,
		RequestedAt:          now,
	}

	if requiredApprovals == 0 {
		if changeKind == CreditChangeKindLimit && newLimitCents != nil {
			if _, err := s.customers.SetCustomerCreditLimit(ctx, cust.CustomerID, *newLimitCents); err != nil {
				return CreditChangeRequest{}, err
			}
		} else if changeKind == CreditChangeKindHold {
			holdID, _, err := s.store.PlaceHold(ctx, cust.CustomerID, holdKind, reason, caller.UserID)
			if err != nil {
				return CreditChangeRequest{}, err
			}
			req.HoldID = holdID
		}
		req.AppliedAt = &now
	}

	saved, err := s.store.CreateCreditChangeRequest(ctx, req)
	if err != nil {
		return CreditChangeRequest{}, err
	}

	action := ActCreditChangeRequested
	if saved.Status == CreditChangeStatusApplied {
		action = ActCreditChangeApplied
	}
	s.recordAudit(ctx, action, caller.UserID, saved)
	return saved, nil
}

type creditChangeTxRunner interface {
	InCreditChangeTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type creditChangeAuditTxRecorder interface {
	RecordTx(ctx context.Context, q audit.Execer, a audit.SensitiveAccess) error
}

func (s *CreditChangeService) inTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if tr, ok := s.store.(creditChangeTxRunner); ok && tr != nil {
		return tr.InCreditChangeTx(ctx, fn)
	}
	return fn(ctx)
}

func alreadyDecidedError(status string) error {
	return fmt.Errorf("%w: credit change request has already been %s", ErrCreditChangeNotPending, strings.ToLower(status))
}

// Approve records an approver's approval on a PENDING credit change request.
// It takes the request row FOR UPDATE in one transaction, judges its status there,
// applies the limit when the approval threshold is satisfied, marks the request,
// and writes the audit row in that same transaction.
func (s *CreditChangeService) Approve(ctx context.Context, id string, in DecideCreditChangeInput) (CreditChangeRequest, error) {
	caller, err := s.requireManagerCaller(ctx)
	if err != nil {
		return CreditChangeRequest{}, err
	}
	id = strings.TrimSpace(id)
	if _, err := uuid.Parse(id); err != nil {
		return CreditChangeRequest{}, ErrCreditChangeNotFound
	}
	if caller.UserID == "" {
		return CreditChangeRequest{}, ErrCreditChangeUnauthorized
	}

	now := s.clock().UTC()
	note := strings.TrimSpace(in.Note)
	if note == "" {
		note = strings.TrimSpace(in.Reason)
	}

	var updated CreditChangeRequest
	err = s.inTx(ctx, func(txCtx context.Context) error {
		req, err := s.store.GetCreditChangeRequest(txCtx, id)
		if err != nil {
			return err
		}
		if !caller.CanReachAccountHome(req.BranchID) {
			return ErrCreditChangeNotFound
		}
		if req.Status != CreditChangeStatusPending {
			return alreadyDecidedError(req.Status)
		}
		if caller.UserID == req.RequestedBy {
			return ErrCreditChangeSelfApproval
		}
		if req.FirstApprovedBy != "" && caller.UserID == req.FirstApprovedBy {
			return ErrCreditChangeDuplicateApprover
		}

		if req.FirstApprovedBy == "" {
			if !canFirstApproveCreditIncrease(caller.RoleCode) {
				return ErrCreditChangeRoleForbidden
			}
			req.FirstApprovedBy = caller.UserID
			req.FirstApprovedByRole = caller.RoleCode
			req.FirstApprovedAt = &now
			req.FirstApprovalNote = note
			if req.RequiredApprovals <= 1 {
				if err := s.applyRequest(txCtx, &req, caller.UserID, now); err != nil {
					return err
				}
			}
		} else {
			if !canSecondApproveCreditIncrease(caller.RoleCode) {
				return ErrCreditChangeRoleForbidden
			}
			req.SecondApprovedBy = caller.UserID
			req.SecondApprovedByRole = caller.RoleCode
			req.SecondApprovedAt = &now
			req.SecondApprovalNote = note
			if err := s.applyRequest(txCtx, &req, caller.UserID, now); err != nil {
				return err
			}
		}

		updated, err = s.store.UpdateCreditChangeRequest(txCtx, req)
		if err != nil {
			if errors.Is(err, ErrCreditChangeNotPending) {
				return alreadyDecidedError("decided")
			}
			return err
		}
		action := ActCreditChangeApproved
		if updated.Status == CreditChangeStatusApplied {
			action = ActCreditChangeApplied
		}
		return s.recordAuditTx(txCtx, action, caller.UserID, updated)
	})
	if err != nil {
		return CreditChangeRequest{}, err
	}
	return updated, nil
}

func (s *CreditChangeService) applyRequest(ctx context.Context, req *CreditChangeRequest, actorID string, now time.Time) error {
	if req.ChangeKind == CreditChangeKindLimit && req.NewLimitCents != nil {
		if _, err := s.customers.SetCustomerCreditLimit(ctx, req.CustomerID, *req.NewLimitCents); err != nil {
			return err
		}
	} else if req.ChangeKind == CreditChangeKindHold {
		holdID, _, err := s.store.PlaceHold(ctx, req.CustomerID, req.HoldKind, req.Reason, actorID)
		if err != nil {
			return err
		}
		req.HoldID = holdID
	}
	req.Status = CreditChangeStatusApplied
	req.AppliedAt = &now
	return nil
}

// Reject rejects a PENDING credit change request under one FOR UPDATE transaction lock.
func (s *CreditChangeService) Reject(ctx context.Context, id string, in DecideCreditChangeInput) (CreditChangeRequest, error) {
	caller, err := s.requireManagerCaller(ctx)
	if err != nil {
		return CreditChangeRequest{}, err
	}
	id = strings.TrimSpace(id)
	if _, err := uuid.Parse(id); err != nil {
		return CreditChangeRequest{}, ErrCreditChangeNotFound
	}
	if caller.UserID == "" {
		return CreditChangeRequest{}, ErrCreditChangeUnauthorized
	}

	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = strings.TrimSpace(in.Note)
	}

	var updated CreditChangeRequest
	err = s.inTx(ctx, func(txCtx context.Context) error {
		req, err := s.store.GetCreditChangeRequest(txCtx, id)
		if err != nil {
			return err
		}
		if !caller.CanReachAccountHome(req.BranchID) {
			return ErrCreditChangeNotFound
		}
		if req.Status != CreditChangeStatusPending {
			return alreadyDecidedError(req.Status)
		}
		if !canFirstApproveCreditIncrease(caller.RoleCode) {
			return ErrCreditChangeRoleForbidden
		}
		if caller.UserID == req.RequestedBy {
			return ErrCreditChangeSelfApproval
		}
		if reason == "" {
			return ErrCreditChangeReasonRequired
		}

		now := s.clock().UTC()
		req.Status = CreditChangeStatusRejected
		req.RejectedBy = caller.UserID
		req.RejectedByRole = caller.RoleCode
		req.RejectedAt = &now
		req.RejectionReason = reason

		updated, err = s.store.UpdateCreditChangeRequest(txCtx, req)
		if err != nil {
			if errors.Is(err, ErrCreditChangeNotPending) {
				return alreadyDecidedError("decided")
			}
			return err
		}
		return s.recordAuditTx(txCtx, ActCreditChangeRejected, caller.UserID, updated)
	})
	if err != nil {
		return CreditChangeRequest{}, err
	}
	return updated, nil
}

// Get fetches one credit change request by ID within the caller's branch reach.
func (s *CreditChangeService) Get(ctx context.Context, id string) (CreditChangeRequest, error) {
	caller, err := s.requireManagerCaller(ctx)
	if err != nil {
		return CreditChangeRequest{}, err
	}
	id = strings.TrimSpace(id)
	if _, err := uuid.Parse(id); err != nil {
		return CreditChangeRequest{}, ErrCreditChangeNotFound
	}
	req, err := s.store.GetCreditChangeRequest(ctx, id)
	if err != nil {
		return CreditChangeRequest{}, err
	}
	if !caller.CanReachAccountHome(req.BranchID) {
		return CreditChangeRequest{}, ErrCreditChangeNotFound
	}
	return req, nil
}

// List returns credit change requests matching the filter within the caller's branch reach.
func (s *CreditChangeService) List(ctx context.Context, f CreditChangeFilter) ([]CreditChangeRequest, error) {
	caller, err := s.requireManagerCaller(ctx)
	if err != nil {
		return nil, err
	}
	f.CustomerID = strings.TrimSpace(f.CustomerID)
	f.BranchID = strings.TrimSpace(f.BranchID)
	f.Status = strings.TrimSpace(f.Status)
	if f.CustomerID != "" {
		cust, found, err := s.customers.CustomerCreditFacts(ctx, f.CustomerID)
		if err != nil {
			return nil, err
		}
		if !found || !caller.CanReachAccountHome(cust.HomeBranchID) {
			return nil, ErrCreditChangeCustomerNotFound
		}
	}
	if f.BranchID != "" && !caller.CanAccessBranch(f.BranchID) {
		return []CreditChangeRequest{}, nil
	}
	return s.store.ListCreditChangeRequests(ctx, f, reachOfCaller(caller))
}

func (s *CreditChangeService) buildAuditEntry(action, actorID string, req CreditChangeRequest) audit.SensitiveAccess {
	extra := map[string]any{
		"status":       req.Status,
		"requested_by": req.RequestedBy,
	}
	if req.ChangeKind == CreditChangeKindLimit {
		extra["field"] = "credit_limit_cents"
		extra["before"] = req.PreviousLimitCents
		if req.NewLimitCents != nil {
			extra["after"] = *req.NewLimitCents
		}
		extra["amount_cents"] = req.DeltaCents
	} else {
		extra["field"] = "credit_hold"
		extra["before"] = "none"
		extra["after"] = req.HoldKind
	}
	if req.FirstApprovedBy != "" {
		extra["first_approved_by"] = req.FirstApprovedBy
	}
	if req.SecondApprovedBy != "" {
		extra["second_approved_by"] = req.SecondApprovedBy
	}
	if req.RejectedBy != "" {
		extra["rejected_by"] = req.RejectedBy
	}
	return audit.SensitiveAccess{
		Action:     action,
		EntityType: "customer",
		EntityID:   req.CustomerID,
		ActorID:    actorID,
		BranchID:   req.BranchID,
		Reason:     req.Reason,
		Extra:      extra,
	}
}

func (s *CreditChangeService) recordAudit(ctx context.Context, action, actorID string, req CreditChangeRequest) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Record(ctx, s.buildAuditEntry(action, actorID, req))
}

func (s *CreditChangeService) recordAuditTx(ctx context.Context, action, actorID string, req CreditChangeRequest) error {
	if s.audit == nil {
		return nil
	}
	entry := s.buildAuditEntry(action, actorID, req)
	if tx := creditChangeTxFrom(ctx); tx != nil {
		if rx, ok := s.audit.(creditChangeAuditTxRecorder); ok {
			return rx.RecordTx(ctx, tx, entry)
		}
	}
	return s.audit.Record(ctx, entry)
}
