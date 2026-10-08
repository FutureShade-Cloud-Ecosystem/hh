package order

// deposit_cancel_seam.go, the cancelled special order's supplier deposit (row
// 1193): the seams the manual cancel path needs beside the money predicate.
// The order module owns these interfaces and reads nothing of billing's
// tables itself (module boundary); the composition root wires
// billing.NewInvoiceWriter and the pos policy read.

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
)

// The disposition vocabulary, the frozen words the pos_policy column's CHECK,
// the settlement and the GL builder share (migration 0375). Restated here so
// the cancel path owns the words it hands the settlement; the settlement
// refuses any other spelling loudly.
const (
	DepositDispositionCreditBack = "CREDIT_BACK"
	DepositDispositionKept       = "KEPT"
)

// DepositCanceller is billing's seam: settle the order's live supplier
// deposit documents for a cancellation under the named disposition, on the
// cancel's own transaction when one is handed over (the guarded write's
// precheck tx), answering how many documents it settled. Satisfied by
// *billing.InvoiceWriter at the composition root.
type DepositCanceller interface {
	CancelOrderDeposits(ctx context.Context, tx pgx.Tx, orderID, disposition, reason string) (int, error)
}

// DepositCancelDefaultReader answers the dealer's default disposition for a
// cancelled order's deposit (pos_policy.supplier_deposit_cancel, branch
// resolved). Satisfied by *pos.Service at the composition root; nil is the
// scaffold posture and answers the ruling's default, CREDIT_BACK.
type DepositCancelDefaultReader interface {
	SupplierDepositCancelDefault(ctx context.Context, branchID string) (string, error)
}

// DepositCancelWired reports whether both deposit seams are present. main.go
// asserts this at boot (fail fast) so a dropped wiring can never reach runtime
// in a deploy, the cancel guards' own posture.
func (s *Service) DepositCancelWired() bool {
	return s.depositCanceller != nil && s.depositDefault != nil
}

// WithDepositCanceller injects the settlement seam. Nil (a scaffold
// composition) leaves the cancel working exactly as before for every order
// with no deposit documents; main.go asserts the wiring at boot so a deploy
// never serves without it.
func (s *Service) WithDepositCanceller(c DepositCanceller) *Service { s.depositCanceller = c; return s }

// WithDepositCancelDefault injects the dealer default read.
func (s *Service) WithDepositCancelDefault(r DepositCancelDefaultReader) *Service {
	s.depositDefault = r
	return s
}

// resolveDepositDisposition answers the disposition a cancel applies to the
// order's deposit documents: a manager's keep override, else the dealer's
// default, else (an unwired seam, the scaffold posture) the ruling's default.
//
// The keep override needs no separate authority check here: the cancel action
// itself is manager-gated (manualRules), and TransitionManual has already
// refused a non manager before this runs; the reason is mandatory for every
// cancel for the same reason. An UNREADABLE default refuses the cancel: the
// disposition is money, and a guess is not the dealer's answer.
func (s *Service) resolveDepositDisposition(ctx context.Context, branchID string, keepDeposit bool) (string, error) {
	if keepDeposit {
		return DepositDispositionKept, nil
	}
	if s.depositDefault == nil {
		return DepositDispositionCreditBack, nil
	}
	d, err := s.depositDefault.SupplierDepositCancelDefault(ctx, branchID)
	if err != nil {
		return "", errDepositDefaultUnreadable{err}
	}
	if d == DepositDispositionKept {
		return DepositDispositionKept, nil
	}
	return DepositDispositionCreditBack, nil
}

// errDepositDefaultUnreadable wraps a failed dealer-default read so the
// handler can answer 503 in words, the cancel guards' own posture for a
// wiring fault rather than a business refusal.
type errDepositDefaultUnreadable struct{ cause error }

func (e errDepositDefaultUnreadable) Error() string {
	return "order: the dealer's deposit rule could not be read, cancel refused"
}

// settleDepositsOnCancel is the cancel precheck's second half: the money
// predicate has proved every other exit, so the deposit documents (the money
// the cancel has an exit for) settle on the same transaction the status flip
// commits in. A settlement that refuses fails the cancel: money must not be
// orphaned on a CANCELLED order the way audit H2 found it.
func (s *Service) settleDepositsOnCancel(ctx context.Context, orderID, disposition, reason string) error {
	if s.depositCanceller == nil {
		// Unreachable in a deploy (main.go asserts the wiring at boot); a
		// scaffold composition without the seam cancels exactly as it did
		// before the deposit exit existed, loudly.
		slog.Default().Warn("order lifecycle: deposit cancel settled nothing, the seam is unwired",
			"order_id", orderID)
		return nil
	}
	_, err := s.depositCanceller.CancelOrderDeposits(ctx, precheckTx(ctx), orderID, disposition, reason)
	return err
}
