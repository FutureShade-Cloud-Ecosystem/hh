package payments

// intent.go, DIB-415: the durable pre-charge intent that makes the replay guard real.
//
// READ THIS BEFORE CHANGING ANYTHING HERE. The shipped guard checked, AFTER the charge, whether a
// payment already existed for the gateway's returned ref. Two things were wrong with that, and they
// compound:
//
//  1. It is keyed on a fact we only learn once the money has already moved. At best it prevents a
//     duplicate ROW; it cannot prevent a duplicate CHARGE.
//  2. It only ever fires when the gateway hands back the SAME ref for a repeated idempotency key.
//     StubGateway does exactly that (its ref is a hash of the key), which is why every test went
//     green for two waves. Run exposes no idempotency header on /charge, so a retried live charge
//     comes back with a brand-new trans_id and the guard matches nothing.
//
// The window is the ordinary one: capture succeeds at the processor, the process dies before the
// settle commits, the invoices are still payable, the customer taps Pay again, and the card is
// charged twice. Nothing in the old code could see it, because the only gateway anyone ever ran it
// against could not produce it.
//
// The fix inverts the ordering. We write down what we are ABOUT to do, keyed on something WE own -
// the server-bound idempotency key, and commit that BEFORE touching the gateway. The unique index
// on payment_intents.idempotency_key is the actual guard: the second request cannot insert, so it
// never reaches the processor. It reads the row instead and answers from its status.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"

	"github.com/jackc/pgx/v5"
)

// Intent statuses (0103 CHECK).
const (
	IntentPending   = "PENDING"
	IntentCharging  = "CHARGING"
	IntentSettled   = "SETTLED"
	IntentFailed    = "FAILED"
	IntentAbandoned = "ABANDONED"
)

// ErrAmountChanged: the amount the customer was shown is not the amount the core quotes now. Nothing
// was claimed and no money moved; the caller re-reads the quote and asks again.
var ErrAmountChanged = errors.New("payments: the amount changed since it was shown")

// CodeQuoteChanged is the machine code a changed-amount refusal carries on the wire, beside its words.
// Clients match on it, never on the prose.
const CodeQuoteChanged = "quote_changed"

// RequireShownAmount is the rule every door that takes a customer's payment holds: the request names
// the amount the Pay button showed, in cents, above zero. Without it the core could only charge the
// re-quote, and a customer would pay an amount they never saw.
func RequireShownAmount(expectedCents int64) error {
	if expectedCents <= 0 {
		return httpx.BadRequestf("expected_cents is required: send the amount the Pay button showed, in cents, above zero")
	}
	return nil
}

// ErrPaymentInFlight, a charge for this exact request is already at the processor and we do not
// know its outcome. Maps to HTTP 409.
//
// This is the honest answer to the crash window, and it is deliberately NOT a retry-friendly error.
// From our side, "the processor never received it" and "the processor took the money and the ack was
// lost" are indistinguishable. Charging again would resolve the ambiguity in the customer's
// disfavour, at their expense, silently. So we refuse, name the intent, and leave a human to
// reconcile against the trans_id we recorded before the call.
var ErrPaymentInFlight = errors.New("payments: a charge for this request is already in flight")

// Intent is one payment_intents row (0103).
type Intent struct {
	ID          string    `json:"id"`
	Key         string    `json:"idempotency_key"`
	CustomerID  string    `json:"customer_id"`
	AmountCents int64     `json:"amount_cents"`
	InvoiceIDs  string    `json:"invoice_ids"`
	Status      string    `json:"status"`
	Gateway     string    `json:"gateway"`
	GatewayRef  string    `json:"gateway_ref"`
	PaymentID   string    `json:"payment_id,omitempty"`
	Attempts    int       `json:"attempts"`
	LastError   string    `json:"last_error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// IntentClaim is the request to claim the right to charge.
type IntentClaim struct {
	Key         string
	CustomerID  string
	AmountCents int64
	InvoiceIDs  string // canonical sorted, comma-joined
	Gateway     string
}

const intentColumns = `id::text, idempotency_key, customer_id::text, amount_cents, invoice_ids,
	status, gateway, gateway_ref, COALESCE(payment_id::text,''), attempts, last_error, created_at, updated_at`

func scanIntent(row pgx.Row) (Intent, error) {
	var in Intent
	err := row.Scan(&in.ID, &in.Key, &in.CustomerID, &in.AmountCents, &in.InvoiceIDs,
		&in.Status, &in.Gateway, &in.GatewayRef, &in.PaymentID, &in.Attempts, &in.LastError,
		&in.CreatedAt, &in.UpdatedAt)
	return in, err
}

// ClaimIntent is the guard, expressed as ONE statement so there is no window between "is there an
// intent?" and "make one". It returns claimed=true when THIS caller now owns the right to charge.
//
// The INSERT ... ON CONFLICT DO UPDATE ... WHERE shape does the whole job:
//   - No row exists → insert it CHARGING. We own it.
//   - A row exists in FAILED or ABANDONED → a genuine retry after a known-no-money outcome. The
//     conflict update re-claims it, bumps attempts, and refreshes the amount (balances may have
//     moved since the failure). We own it.
//   - A row exists in PENDING, CHARGING or SETTLED → the WHERE fails, no row comes back, and we own
//     nothing. The caller reads the row and answers from its status.
//
// Two concurrent identical requests therefore produce exactly one winner at the database level, not
// at the application level, which is the only place a race like this can actually be settled.
func (r *Repository) ClaimIntent(ctx context.Context, c IntentClaim) (Intent, bool, error) {
	if r.pool == nil {
		return Intent{}, false, errNoDB
	}
	if c.Key == "" {
		return Intent{}, false, errors.New("payments: an intent needs an idempotency key")
	}
	in, err := scanIntent(r.pool.QueryRow(ctx,
		`INSERT INTO payment_intents (idempotency_key, customer_id, amount_cents, invoice_ids, status, gateway, attempts)
		 VALUES ($1, $2::uuid, $3, $4, 'CHARGING', $5, 1)
		 ON CONFLICT (idempotency_key) DO UPDATE
		    SET status = 'CHARGING',
		        amount_cents = EXCLUDED.amount_cents,
		        gateway = EXCLUDED.gateway,
		        gateway_ref = '',
		        last_error = '',
		        attempts = payment_intents.attempts + 1,
		        updated_at = now()
		  WHERE payment_intents.status IN ('FAILED','ABANDONED')
		 RETURNING `+intentColumns,
		c.Key, c.CustomerID, c.AmountCents, c.InvoiceIDs, c.Gateway))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Lost the claim. Read who holds it so the caller can answer precisely.
			existing, ok, gerr := r.GetIntentByKey(ctx, c.Key)
			if gerr != nil {
				return Intent{}, false, gerr
			}
			if !ok {
				// Vanishingly rare: the holder was deleted between the conflict and this read. Treat
				// it as in-flight rather than charging, an unexplained state is not a licence to
				// touch a payment processor.
				return Intent{}, false, ErrPaymentInFlight
			}
			return existing, false, nil
		}
		return Intent{}, false, fmt.Errorf("payments: claim intent: %w", err)
	}
	return in, true, nil
}

// GetIntentByKey reads one intent by its server-bound key.
func (r *Repository) GetIntentByKey(ctx context.Context, key string) (Intent, bool, error) {
	if r.pool == nil {
		return Intent{}, false, errNoDB
	}
	in, err := scanIntent(r.pool.QueryRow(ctx,
		`SELECT `+intentColumns+` FROM payment_intents WHERE idempotency_key = $1`, key))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Intent{}, false, nil
		}
		return Intent{}, false, fmt.Errorf("payments: get intent: %w", err)
	}
	return in, true, nil
}

// StampIntentRef records the processor's ref the INSTANT it is known, before the settle is attempted.
//
// This is the single most valuable row-write in the whole flow and it is easy to under-rate. If the
// settle then dies, the trans_id is already on disk, so the stuck CHARGING row an operator finds in
// the morning names the exact transaction to look up at the processor. Without it, reconciliation
// means grepping application logs that may have rotated.
func (r *Repository) StampIntentRef(ctx context.Context, intentID, gateway, ref string) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE payment_intents SET gateway = $2, gateway_ref = $3, updated_at = now()
		  WHERE id = $1::uuid`, intentID, gateway, ref)
	if err != nil {
		return fmt.Errorf("payments: stamp intent ref: %w", err)
	}
	return nil
}

// FailIntent records a KNOWN-no-money outcome (a decline, or a rejection before dispatch) so the
// next genuine attempt can re-claim the key. It must NEVER be called for an unknown outcome, that
// is what leaving the row CHARGING is for.
func (r *Repository) FailIntent(ctx context.Context, intentID, reason string) error {
	if r.pool == nil {
		return errNoDB
	}
	if len(reason) > 500 {
		reason = reason[:500]
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE payment_intents SET status = 'FAILED', last_error = $2, updated_at = now()
		  WHERE id = $1::uuid AND status = 'CHARGING'`, intentID, reason)
	if err != nil {
		return fmt.Errorf("payments: fail intent: %w", err)
	}
	return nil
}

// GetPaymentByID reads one settled payment (with allocations), tenant-scoped. It is how a SETTLED
// intent hands the ORIGINAL payment back to a replayed request.
func (r *Repository) GetPaymentByID(ctx context.Context, customerID, paymentID string) (Payment, bool, error) {
	if r.pool == nil {
		return Payment{}, false, errNoDB
	}
	var p Payment
	var pCustomerID, pGatewayRef *string
	err := r.pool.QueryRow(ctx,
		`SELECT id::text, payment_no::text, customer_id::text, method, amount_cents, status, gateway_ref, received_at
		   FROM payments WHERE id = $2::uuid AND customer_id = $1::uuid`, customerID, paymentID).
		Scan(&p.ID, &p.PaymentNo, &pCustomerID, &p.Method, &p.AmountCents, &p.Status, &pGatewayRef, &p.ReceivedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Payment{}, false, nil
		}
		return Payment{}, false, fmt.Errorf("payments: get payment: %w", err)
	}
	if pCustomerID != nil {
		p.CustomerID = *pCustomerID
	}
	if pGatewayRef != nil {
		p.GatewayRef = *pGatewayRef
	}
	byPayment, err := r.allocationsByPayment(ctx, []string{p.ID})
	if err != nil {
		return Payment{}, false, err
	}
	p.Allocations = byPayment[p.ID]
	return p, true, nil
}
