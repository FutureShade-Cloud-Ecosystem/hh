package payment

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// The card capture and compensation entry points (design sections 3.3 to 3.5), over the attempt store.

// Bounds of the detached contexts the dispatch and the compensation run on.
const (
	CaptureDispatchDeadline = 45 * time.Second
	CompensationDeadline    = 90 * time.Second
)

// Result kinds.
const (
	CaptureKindCaptured = "CAPTURED"
	CaptureKindBound    = "BOUND"

	CompensateKindVoided       = "VOIDED"
	CompensateKindCommitted    = "COMMITTED"
	CompensateKindNeedsManager = "NEEDS_MANAGER"
)

var (
	ErrCaptureInFlight         = errors.New("payment: the card charge for this leg is in flight")
	ErrCaptureDeclined         = errors.New("payment: the card was declined")
	ErrCaptureReversed         = errors.New("payment: the card charge was reversed")
	ErrCaptureNeedsManager     = errors.New("payment: the card charge needs a manager")
	ErrCaptureResolved         = errors.New("payment: the card charge was resolved by a manager")
	ErrCaptureNotDispatched    = errors.New("payment: the card charge was not dispatched")
	ErrCaptureAnswerUnrecorded = errors.New("payment: a processor answer could not be recorded")
	ErrCompensateNotCaptured   = errors.New("payment: the attempt holds no captured charge to reverse")
	ErrAttemptNotCaptured      = errors.New("payment: the attempt is no longer captured")
)

// CaptureInput is one card leg. AccountToken is never persisted and never logged.
type CaptureInput struct {
	LegKey, SaleKey                               string
	LegSeq                                        int
	AmountCents                                   int64
	BranchID, TillSessionID, CustomerID, CardType string
	CreatedBy                                     string
	AccountToken                                  string
}

// CaptureResult carries the attempt row the answer was read from or written to.
type CaptureResult struct {
	Kind    string
	Attempt Attempt
}

// CompensateResult carries the attempt row after the compensation.
type CompensateResult struct {
	Kind    string
	Attempt Attempt
}

var errNotBuilt = errors.New("payment: capture not built")

func (s *Service) Capture(context.Context, CaptureInput) (CaptureResult, error) {
	return CaptureResult{}, errNotBuilt
}

func (s *Service) Compensate(context.Context, string) (CompensateResult, error) {
	return CompensateResult{}, errNotBuilt
}

func (s *Service) BindTx(context.Context, pgx.Tx, string, string) error { return errNotBuilt }
