package payment

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Statuses of a card charge attempt (migration 0361; design section 3.2).
const (
	AttemptCharging     = "CHARGING"
	AttemptCaptured     = "CAPTURED"
	AttemptDeclined     = "DECLINED"
	AttemptUnknown      = "UNKNOWN"
	AttemptBound        = "BOUND"
	AttemptVoiding      = "VOIDING"
	AttemptVoided       = "VOIDED"
	AttemptVoidUnknown  = "VOID_UNKNOWN"
	AttemptVoidDeclined = "VOID_DECLINED"
	AttemptResolved     = "RESOLVED"
)

// Reversal kinds and manager resolutions.
const (
	ReversalVoid   = "VOID"
	ReversalRefund = "REFUND"

	ResolutionNotCharged          = "NOT_CHARGED"
	ResolutionReversedAtProcessor = "REVERSED_AT_PROCESSOR"
)

var (
	// ErrTillUnresolved is the till guard's refusal.
	ErrTillUnresolved = errors.New("payment: the till session holds an unresolved card attempt for this amount")
	// ErrAttemptsUnbuilt marks the stub the failing tests are written against.
	ErrAttemptsUnbuilt = errors.New("payment: attempt store not built")
	// ErrResolveInvalid is a resolve with a missing note, resolver or an unknown resolution.
	ErrResolveInvalid = errors.New("payment: resolve needs a resolution, a note and a resolver")
)

// Attempt is one card_charge_attempts row.
type Attempt struct {
	ID, LegKey, SaleKey                     string
	LegSeq                                  int
	AmountCents                             int64
	BranchID, TillSessionID, CustomerID     string
	CardType, Processor, Status             string
	ProcessorRef, ReversalKind, ReversalRef string
	RespCode, LastError, PaymentID          string
	Resolution, ResolutionNote, ResolvedBy  string
	CreatedBy                               string
	CreatedAt, UpdatedAt                    time.Time
}

// ClaimInput is what a claim records before the processor is called.
type ClaimInput struct {
	LegKey, SaleKey                     string
	LegSeq                              int
	AmountCents                         int64
	BranchID, TillSessionID, CustomerID string
	CardType, Processor, CreatedBy      string
}

// ListQuery selects attempts for a manager.
type ListQuery struct {
	OpenOnly bool
	From, To time.Time
}

func (s *Store) Claim(ctx context.Context, in ClaimInput) (Attempt, bool, error) {
	return Attempt{}, false, ErrAttemptsUnbuilt
}
func (s *Store) GetByLegKey(ctx context.Context, legKey string) (Attempt, bool, error) {
	return Attempt{}, false, ErrAttemptsUnbuilt
}
func (s *Store) StampCaptured(ctx context.Context, id, ref, respCode string) (Attempt, bool, error) {
	return Attempt{}, false, ErrAttemptsUnbuilt
}
func (s *Store) StampDeclined(ctx context.Context, id, respCode, lastErr string) (Attempt, bool, error) {
	return Attempt{}, false, ErrAttemptsUnbuilt
}
func (s *Store) StampUnknown(ctx context.Context, id, lastErr string) (Attempt, bool, error) {
	return Attempt{}, false, ErrAttemptsUnbuilt
}
func (s *Store) BeginVoiding(ctx context.Context, id string) (Attempt, bool, error) {
	return Attempt{}, false, ErrAttemptsUnbuilt
}
func (s *Store) StampVoided(ctx context.Context, id, kind, ref string) (Attempt, bool, error) {
	return Attempt{}, false, ErrAttemptsUnbuilt
}
func (s *Store) StampVoidUnknown(ctx context.Context, id, lastErr string) (Attempt, bool, error) {
	return Attempt{}, false, ErrAttemptsUnbuilt
}
func (s *Store) StampVoidDeclined(ctx context.Context, id, lastErr string) (Attempt, bool, error) {
	return Attempt{}, false, ErrAttemptsUnbuilt
}
func (s *Store) BindTx(ctx context.Context, tx pgx.Tx, id, paymentID string) (Attempt, bool, error) {
	return Attempt{}, false, ErrAttemptsUnbuilt
}
func (s *Store) Resolve(ctx context.Context, id, resolution, note, resolvedBy string) (Attempt, bool, error) {
	return Attempt{}, false, ErrAttemptsUnbuilt
}
func (s *Store) ListOpen(ctx context.Context, q ListQuery) ([]Attempt, error) {
	return nil, ErrAttemptsUnbuilt
}

// SanitizeLastError returns text safe to keep in last_error.
const maxLastErrorRunes = 300

func SanitizeLastError(s string) string { return s }
