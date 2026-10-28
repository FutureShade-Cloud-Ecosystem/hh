package payments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

var (
	// ErrNotFound - a payment method/invoice not found for this tenant (IDOR-safe 404).
	ErrNotFound = errors.New("payments: not found")
	// ErrDeclined - the gateway declined the charge (maps to HTTP 402).
	ErrDeclined = errors.New("payments: charge declined")
	// ErrChargePending - the gateway returned the transient RETRY verdict (Run "B"): the
	// charge is neither approved nor declined, so nothing is persisted and the caller may
	// safely retry the SAME request (the server-bound idempotency key dedupes it). Distinct
	// from ErrDeclined so a transient state never collapses into a silent decline (sec. 4.2).
	ErrChargePending = errors.New("payments: charge pending, retry")
)

// chargeCurrency is the settlement currency of a dealer with no currency source wired: the Ontario
// dealer (sec. 4.2; CAD settlement on Run is unconfirmed, tracked as an open question - the stub is
// currency-agnostic). A wired source (WithCurrency) answers the dealer's own currency instead.
const chargeCurrency = "CAD"

// CurrencySource is the dealer's own currency, read through a seam the composition root adapts from
// the settings module's dealer configuration. Uncached: a change is live at the next charge.
type CurrencySource interface {
	DealerCurrency(ctx context.Context) (string, error)
}

// paymentStore is the persistence the Service needs. Real impl: Repository (pgx); tests
// use a fake. Every operation is tenant-scoped by customerID.
type paymentStore interface {
	ListMethods(ctx context.Context, customerID string) ([]PaymentMethod, error)
	GetMethod(ctx context.Context, customerID, methodID string) (PaymentMethod, error)
	AddMethod(ctx context.Context, m PaymentMethod, makeDefault bool) (PaymentMethod, error)
	RemoveMethod(ctx context.Context, customerID, methodID string) error
	SetDefaultMethod(ctx context.Context, customerID, methodID string) error
	ListPayments(ctx context.Context, customerID string) ([]Payment, error)
	// FindByGatewayRef looks up a single payment by its gateway ref via one indexed query
	// (the idempotent-replay guard), tenant-scoped. Returns found=false when no row matches.
	FindByGatewayRef(ctx context.Context, customerID, ref string) (Payment, bool, error)
	// PayableInvoices reads the payable balance of each requested invoice, tenant-scoped,
	// and returns ONE Allocation per invoice with AmountCents = the payable amount EXCLUDING
	// the Bill 60 holdback (balance_cents - holdback_cents, floored at 0; sec. 4.2 #7). It is the
	// AR read seam the pay flow needs to derive the summed charge server-side BEFORE the
	// gateway call. If ANY requested invoice is missing or belongs to another tenant it
	// returns ErrNotFound (IDOR-safe), never a partial slice. Reading invoices (billing's
	// table) here is the same cross-table reach the atomic Pay write already takes.
	PayableInvoices(ctx context.Context, customerID string, invoiceIDs []string) ([]Allocation, error)
	// Pay records ONE settled charge across N invoices atomically: insert the payment
	// row (method/gateway_ref/status), N payment_allocations, N customer_transactions,
	// and recompute each invoice's balance/status - all in one transaction.
	Pay(ctx context.Context, customerID, method, gatewayRef string, allocs []Allocation) (Payment, error)
}

// intentStore is the DIB-415 durable pre-charge intent seam (0103). It is DELIBERATELY separate from
// paymentStore and resolved by a type assertion in NewService rather than being folded into the
// required interface.
//
// The reason is a fence, and it is worth stating rather than leaving as an oddity: internal/portal
// composes payments.NewService with its own test fake, and portal is another swarm's territory this
// branch must not edit. Widening the REQUIRED interface would break their build at the union, a
// compile error in somebody else's package, caused by a change they cannot see the reason for.
//
// The optional shape has one real risk: a store that does not implement it silently loses the guard.
// That is closed two ways. There is a compile-time assertion below that the production *Repository
// satisfies it, so it cannot be dropped by accident; and NewService records what it resolved, so a
// service running without the guard is a fact somebody can see rather than infer.
type intentStore interface {
	// ClaimIntent atomically claims the right to charge for a server-bound idempotency key.
	// claimed=false means somebody else holds it; the returned Intent says who and in what state.
	ClaimIntent(ctx context.Context, c IntentClaim) (Intent, bool, error)
	// GetIntentByKey reads an intent by its server-bound key (the replay fast path).
	GetIntentByKey(ctx context.Context, key string) (Intent, bool, error)
	// StampIntentRef records the processor ref the instant it is known (before the settle).
	StampIntentRef(ctx context.Context, intentID, gateway, ref string) error
	// FailIntent marks a KNOWN-no-money outcome so a genuine retry can re-claim the key.
	FailIntent(ctx context.Context, intentID, reason string) error
	// PayWithIntent is Pay, closing the intent out SETTLED in the same transaction.
	PayWithIntent(ctx context.Context, customerID, method, gatewayRef string, allocs []Allocation, intentID string) (Payment, error)
	// GetPaymentByID reads a settled payment so a SETTLED intent can replay the original.
	GetPaymentByID(ctx context.Context, customerID, paymentID string) (Payment, bool, error)
}

// The production store MUST carry the guard. This assertion is what stops the optional seam above
// from quietly becoming an unimplemented one.
var _ intentStore = (*Repository)(nil)

// Service composes the PaymentGateway port with the store: it sums the selected
// invoices, runs ONE charge, then atomically splits it across allocations. The gateway
// is selected by config (Stub default; Run live) and never hardcoded.
type Service struct {
	store   paymentStore
	gateway PaymentGateway
	// intents is nil only when the store does not implement the DIB-415 seam, in practice, a test
	// fake. Pay refuses to charge in that state rather than falling back to the pre-0103 behaviour:
	// a payment path with no double-charge guard is not a degraded mode, it is the bug DIB-415 exists
	// to close, and it must not be reachable by forgetting to implement an interface.
	intents intentStore
	// currency is nil for a service built without a dealer currency (the scaffold and the test
	// fakes); a charge is then CAD, the Ontario dealer.
	currency CurrencySource
}

// WithCurrency wires the dealer's currency source. The charge currency is read once per charge,
// after the invoices validate and before the intent is claimed, so a failed read refuses with no
// intent held and no money moved.
func (s *Service) WithCurrency(src CurrencySource) *Service {
	s.currency = src
	return s
}

// chargeCurrencyFor answers the currency a charge is made in: the dealer's, or CAD with no source.
func (s *Service) chargeCurrencyFor(ctx context.Context) (string, error) {
	if s.currency == nil {
		return chargeCurrency, nil
	}
	c, err := s.currency.DealerCurrency(ctx)
	if err != nil {
		return "", fmt.Errorf("payments: the dealer's currency could not be read, nothing was charged: %w", err)
	}
	if c == "" {
		return "", errors.New("payments: the dealer's currency is blank, nothing was charged")
	}
	return c, nil
}

func NewService(store paymentStore, gateway PaymentGateway) *Service {
	s := &Service{store: store, gateway: gateway}
	s.intents, _ = store.(intentStore)
	return s
}

// GuardedByIntent reports whether the DIB-415 pre-charge intent guard is active on this service.
// Exposed so a boot path or a test can assert it rather than assume it.
func (s *Service) GuardedByIntent() bool { return s.intents != nil }

// Gateway exposes the active processor so the boot path can fail closed in prod when it
// is not a live gateway (sec. 4.2 #3).
func (s *Service) Gateway() PaymentGateway { return s.gateway }

// ListMethods returns the tenant's active stored methods (tokens never serialized).
func (s *Service) ListMethods(ctx context.Context, customerID string) ([]PaymentMethod, error) {
	if customerID == "" {
		return nil, httpx.BadRequestf("customer id is required")
	}
	return s.store.ListMethods(ctx, customerID)
}

// AddMethod exchanges a one-time nonce for a durable vault token via the gateway, then
// persists ONLY the token + display metadata. The nonce/PAN never lands in our DB or logs.
func (s *Service) AddMethod(ctx context.Context, customerID string, req AddMethodRequest) (PaymentMethod, error) {
	if customerID == "" {
		return PaymentMethod{}, httpx.BadRequestf("customer id is required")
	}
	if req.Nonce == "" {
		return PaymentMethod{}, httpx.BadRequestf("a tokenization nonce is required")
	}

	vault, err := s.gateway.Vault(ctx, VaultRequest{CustomerID: customerID, Nonce: req.Nonce})
	if err != nil {
		return PaymentMethod{}, fmt.Errorf("vault method: %w", err)
	}

	m := PaymentMethod{
		CustomerID: customerID,
		Gateway:    s.gateway.Name(),
		MethodType: vault.MethodType,
		Brand:      vault.Brand,
		Last4:      vault.Last4,
		ExpMonth:   vault.ExpMonth,
		ExpYear:    vault.ExpYear,
		IsDefault:  req.MakeDefault,
		Status:     "active",
	}.WithToken(vault.Token)

	return s.store.AddMethod(ctx, m, req.MakeDefault)
}

// RemoveMethod detaches the method at the processor, then soft-deletes it locally. The
// detach runs FIRST so a removed method can never be charged again even if the local
// soft-delete is retried.
func (s *Service) RemoveMethod(ctx context.Context, customerID, methodID string) error {
	if customerID == "" || methodID == "" {
		return httpx.BadRequestf("customer id and method id are required")
	}

	// GetMethod is tenant-scoped: a cross-tenant id surfaces as ErrNotFound, not 403.
	m, err := s.store.GetMethod(ctx, customerID, methodID)
	if err != nil {
		return err
	}
	if err := s.gateway.DetachMethod(ctx, m.Token()); err != nil {
		return fmt.Errorf("detach method: %w", err)
	}
	return s.store.RemoveMethod(ctx, customerID, methodID)
}

// SetDefaultMethod marks one stored method as the tenant's default, clearing any prior
// default in the same transaction. Tenant-scoped: a missing or cross-tenant method id
// surfaces as ErrNotFound (IDOR-safe), never a 403.
func (s *Service) SetDefaultMethod(ctx context.Context, customerID, methodID string) error {
	if customerID == "" || methodID == "" {
		return httpx.BadRequestf("customer id and method id are required")
	}
	return s.store.SetDefaultMethod(ctx, customerID, methodID)
}

// ListPayments returns the tenant's payment history with allocations.
func (s *Service) ListPayments(ctx context.Context, customerID string) ([]Payment, error) {
	if customerID == "" {
		return nil, httpx.BadRequestf("customer id is required")
	}
	return s.store.ListPayments(ctx, customerID)
}

// Pay runs the multi-invoice pay flow: validate the selection, derive the summed payable
// amount server-side (EXCLUDING holdback), run ONE gateway charge under a server-bound
// idempotency key, then atomically split the settled amount across allocations.
//
// Ordering (sec. 4.2): the amount and Run customer are derived server-side and never trusted
// from the client; ONE Charge runs for the summed balance; on OutcomeApproved the payment
// row + N allocations + N customer_transactions are written in one store.Pay transaction
// (which re-validates balances FOR NO KEY UPDATE); OutcomeDeclined maps to ErrDeclined; the
// transient OutcomeRetry maps to ErrChargePending and persists nothing.
//
// Idempotency (DIB-415, and read the ORDER carefully, it is the whole guarantee): the key is
// RE-BOUND server-side to {customer_id, sorted invoice selection, funding source}; the client's
// IdempotencyKey is advisory and deliberately ignored, so a client can neither force nor collide a
// key across accounts. A durable payment_intents row is claimed and COMMITTED under that key BEFORE
// the gateway is touched, and closed out SETTLED in the same transaction as the settle.
//
// That ordering is what stops a live double charge. The old guard ran AFTER the charge and matched on
// the gateway's own ref, which only de-duplicates when the processor returns a stable ref for a
// repeated key, true of StubGateway by construction, false of Run, which exposes no idempotency
// header on /charge. So the hole was structurally invisible to every test that could be written
// against the stub. The intent moves the guard to a key we own and a moment before the money moves.
//
// What a replay now gets, by intent status:
//
//	SETTLED  → the ORIGINAL payment, returned as-is. One charge, one settlement set.
//	CHARGING → with a stamped processor ref (approved, settle outstanding): the settle is retried
//	           against that ref, never a second charge (resume). Without a ref: ErrPaymentInFlight
//	           (409). NOT a second charge. We cannot distinguish "the processor never saw it" from "the
//	           processor took the money and the ack was lost", and resolving that ambiguity by charging
//	           again spends the customer's money to save us a phone call.
//	FAILED   → re-claimable; a decline left no money anywhere, so a genuine retry is allowed.
func (s *Service) Pay(ctx context.Context, customerID string, req PayRequest) (Payment, error) {
	return s.pay(ctx, customerID, req, settleStamps{})
}

// PaySynced is the sync door's Pay (HH-3.5): the SAME choreography,
// one body, with the offline replay's stamps carried onto the settled
// row (the client-chosen payment id and the client idempotency key
// the engine's dedup resolves). The stamps change NOTHING about the
// money: the charge still runs under the server-bound intent key, the
// amount is still derived server-side, and the settle still re-valid
// balances under lock. A settle store that cannot carry the stamps
// refuses rather than falling back to an unstamped row (the
// intentStore posture: an optional seam that silently drops its guard
// is the bug the optionality was supposed to prevent).
func (s *Service) PaySynced(ctx context.Context, customerID string, req PayRequest, rowID, clientKey string) (Payment, error) {
	return s.pay(ctx, customerID, req, settleStamps{RowID: rowID, ClientKey: clientKey})
}

// stampedSettler is the optional settle seam the sync stamps ride on:
// the production *Repository carries it (the compile-time assertion
// below), a test fake may not, and PaySynced over a store that does
// not refuses the charge rather than settling a row the replay cannot
// dedup, the intentStore doctrine applied to the second optional
// interface this package declares.
type stampedSettler interface {
	PayWithStamps(ctx context.Context, customerID, method, gatewayRef string, allocs []Allocation, intentID string, stamps settleStamps) (Payment, error)
}

// The production store MUST carry the stamped settle.
var _ stampedSettler = (*Repository)(nil)

// pay is the one pay choreography both doors run; the stamps alter
// only the settled row's sync legs.
func (s *Service) pay(ctx context.Context, customerID string, req PayRequest, stamps settleStamps) (Payment, error) {
	if customerID == "" {
		return Payment{}, httpx.BadRequestf("customer id is required")
	}
	if len(req.InvoiceIDs) == 0 {
		return Payment{}, httpx.BadRequestf("at least one invoice is required")
	}
	if req.PaymentMethodID == "" && req.Nonce == "" {
		return Payment{}, httpx.BadRequestf("a stored method or a tokenization nonce is required")
	}
	if hasDuplicates(req.InvoiceIDs) {
		return Payment{}, httpx.BadRequestf("an invoice may be selected only once")
	}

	// - DIB-415 replay fast path, BEFORE any re-validation of the world -
	// A replay is by definition the same request, and the same request must get the same answer.
	// Re-deriving payability first would tell a customer who double-tapped Pay that their invoice
	// "is not payable", technically true (they just paid it), and completely useless. Worse, it
	// would refuse to hand back a receipt for money we already took.
	//
	// This read is a FAST PATH, not the guard. The authoritative decision is still ClaimIntent's
	// single atomic statement below, so two genuinely simultaneous requests are resolved by the
	// unique index and not by whichever of them read first.
	if s.intents == nil {
		// See the intentStore doc. Refusing beats charging without the guard.
		return Payment{}, errors.New("payments: this service was built over a store with no durable " +
			"payment-intent support, refusing to charge without the DIB-415 double-charge guard")
	}
	idemKey := idempotencyKey(customerID, req)
	if prior, ok, err := s.intents.GetIntentByKey(ctx, idemKey); err != nil {
		return Payment{}, err
	} else if ok && prior.Status != IntentFailed && prior.Status != IntentAbandoned {
		return s.replay(ctx, customerID, req, stamps, prior)
	}

	// Resolve the funding source. A stored method is loaded tenant-scoped (cross-tenant ->
	// ErrNotFound); an add-at-checkout nonce charges directly without a stored row.
	var methodToken, nonce, method string
	if req.PaymentMethodID != "" {
		m, err := s.store.GetMethod(ctx, customerID, req.PaymentMethodID)
		if err != nil {
			return Payment{}, err
		}
		methodToken = m.Token()
		method = paymentMethodFor(m.MethodType)
	} else {
		nonce = req.Nonce
		method = "CARD"
	}

	// Derive the summed payable amount server-side, EXCLUDING holdback. A cross-tenant or
	// missing invoice surfaces as ErrNotFound (never a partial charge).
	allocs, err := s.store.PayableInvoices(ctx, customerID, req.InvoiceIDs)
	if err != nil {
		return Payment{}, err
	}
	// Validate EVERY selected invoice BEFORE charging the gateway: a single zero-payable or
	// non-payable (PAID/VOID/fully-held-back) invoice in a multi-invoice selection would
	// otherwise charge the gateway and only then roll back in store.Pay's per-allocation
	// re-check. Reject the whole request up front so the gateway is never touched (sec. 4.2 #1).
	var sum int64
	for _, a := range allocs {
		if a.AmountCents <= 0 || !isPayableStatus(a.Status) {
			return Payment{}, httpx.BadRequestf("invoice %q is not payable; refresh and retry", a.InvoiceID)
		}
		sum += a.AmountCents
	}

	// The dealer's currency, read before the intent is claimed: a failed read holds no intent and moves
	// no money. A replay never reaches here (it answered above, from the stored intent).
	currency, err := s.chargeCurrencyFor(ctx)
	if err != nil {
		return Payment{}, err
	}

	// - DIB-415: claim the durable intent BEFORE the irreversible act -
	// This row is committed before a single byte reaches the processor. Everything after it is
	// recoverable; nothing before it has spent anyone's money.
	intent, claimed, err := s.intents.ClaimIntent(ctx, IntentClaim{
		Key:         idemKey,
		CustomerID:  customerID,
		AmountCents: sum,
		InvoiceIDs:  canonicalInvoiceIDs(req.InvoiceIDs),
		Gateway:     s.gateway.Name(),
	})
	if err != nil {
		return Payment{}, err
	}
	if !claimed {
		return s.replay(ctx, customerID, req, stamps, intent)
	}

	// ONE charge for the summed balance, under the server-bound idempotency key.
	res, err := s.gateway.Charge(ctx, ChargeRequest{
		CustomerID:     customerID,
		AmountCents:    sum,
		Currency:       currency,
		MethodToken:    methodToken,
		Nonce:          nonce,
		IdempotencyKey: idemKey,
		Description:    fmt.Sprintf("portal payment: %d invoice(s)", len(allocs)),
	})
	if err != nil {
		// The transport failed. We do NOT know whether the processor saw the request, so the intent
		// stays CHARGING and this key is burned until a human reconciles it. Marking it FAILED here
		// would be a guess that money did not move, and the cost of guessing wrong is a double charge.
		return Payment{}, fmt.Errorf("charge: %w", err)
	}
	switch res.Outcome {
	case OutcomeApproved:
		// Stamp the ref immediately. If the settle below dies, this is what reconciliation reads.
		if err := s.intents.StampIntentRef(ctx, intent.ID, s.gateway.Name(), res.GatewayRef); err != nil {
			return Payment{}, err
		}
	case OutcomeDeclined:
		// A decline is a KNOWN no-money outcome, so the key is released for a genuine retry.
		_ = s.intents.FailIntent(ctx, intent.ID, "declined by gateway")
		return Payment{}, ErrDeclined
	case OutcomeRetry:
		// Run "B": transient, explicitly nothing captured. Also a known no-money outcome.
		_ = s.intents.FailIntent(ctx, intent.ID, "gateway returned RETRY")
		return Payment{}, ErrChargePending
	default:
		_ = s.intents.FailIntent(ctx, intent.ID, "unexpected gateway outcome")
		return Payment{}, fmt.Errorf("charge: unexpected outcome %q", res.Outcome)
	}

	// Belt-and-braces for the pre-0103 shape: if a payment already carries this ref, hand it back
	// rather than writing a second one. With the intent in place this is unreachable on a healthy
	// path; it is kept because it costs one indexed lookup and it is the correct answer if it ever
	// DOES fire (a processor that replays a ref we have already settled).
	if existing, ok, err := s.findByGatewayRef(ctx, customerID, res.GatewayRef); err != nil {
		return Payment{}, err
	} else if ok {
		return existing, nil
	}

	// Atomic settle: payment row + N allocations + N customer_transactions + balance recompute + the
	// intent's SETTLED stamp, all in ONE transaction (invoices re-validated FOR NO KEY UPDATE). The sync
	// stamps ride the same settle when the door carries them.
	return s.settle(ctx, customerID, method, res.GatewayRef, allocs, intent.ID, stamps)
}

// settle writes the approved charge. It runs only AFTER the processor approved, so a refusal here
// is never a client error: the money has moved and the intent stays CHARGING with the ref stamped.
// A balance that moved under the settle (a 400 from lockAndTotal) or a row that vanished (a 404)
// is therefore answered as ErrPaymentInFlight, the unknown class the clients already treat as
// "may have gone through", and the same request resumes it (see replay).
func (s *Service) settle(ctx context.Context, customerID, method, gatewayRef string, allocs []Allocation, intentID string, stamps settleStamps) (Payment, error) {
	var p Payment
	var err error
	if stamps.RowID != "" || stamps.ClientKey != "" {
		settler, ok := s.store.(stampedSettler)
		if !ok {
			return Payment{}, errors.New("payments: this service was built over a store with no stamped settle, refusing to charge a replay it cannot dedup")
		}
		p, err = settler.PayWithStamps(ctx, customerID, method, gatewayRef, allocs, intentID, stamps)
	} else {
		p, err = s.intents.PayWithIntent(ctx, customerID, method, gatewayRef, allocs, intentID)
	}
	return p, afterApprovedCharge(err)
}

// afterApprovedCharge reclassifies a settle refusal that is a client-error class (400, 404) as
// ErrPaymentInFlight. The cause is kept in the text for the log and not wrapped, so no layer above
// can read it back as a bad request.
func afterApprovedCharge(err error) error {
	if err == nil {
		return nil
	}
	var bre httpx.BadRequestError
	if errors.As(err, &bre) || errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: settle refused after an approved charge: %s", ErrPaymentInFlight, err.Error())
	}
	return err
}

// resume settles a CHARGING intent whose processor ref is stamped, which is the state a refused
// settle (or a crash between the stamp and the settle) leaves. It never calls the gateway: the money
// is already taken, so the only open question is whether the invoices can now take it. They can only
// if every one is still payable and the total is exactly what was charged; otherwise the intent
// stays CHARGING for a human to reconcile against the stamped ref, and the answer stays in flight.
// The settle's guarded UPDATE (status = CHARGING) makes two racing resumes write one payment.
func (s *Service) resume(ctx context.Context, customerID string, req PayRequest, stamps settleStamps, intent Intent) (Payment, error) {
	if existing, ok, err := s.findByGatewayRef(ctx, customerID, intent.GatewayRef); err != nil {
		return Payment{}, err
	} else if ok {
		return existing, nil
	}
	method := "CARD"
	if req.PaymentMethodID != "" {
		m, err := s.store.GetMethod(ctx, customerID, req.PaymentMethodID)
		if err != nil {
			return Payment{}, afterApprovedCharge(err)
		}
		method = paymentMethodFor(m.MethodType)
	}
	allocs, err := s.store.PayableInvoices(ctx, customerID, req.InvoiceIDs)
	if err != nil {
		return Payment{}, afterApprovedCharge(err)
	}
	var sum int64
	for _, a := range allocs {
		if a.AmountCents <= 0 || !isPayableStatus(a.Status) {
			return Payment{}, ErrPaymentInFlight
		}
		sum += a.AmountCents
	}
	if sum != intent.AmountCents {
		return Payment{}, ErrPaymentInFlight
	}
	return s.settle(ctx, customerID, method, intent.GatewayRef, allocs, intent.ID, stamps)
}

// replay answers a request whose intent is held by an earlier attempt. It is the entire behavioural
// surface of DIB-415, so each branch says what it costs to get wrong.
func (s *Service) replay(ctx context.Context, customerID string, req PayRequest, stamps settleStamps, intent Intent) (Payment, error) {
	switch intent.Status {
	case IntentSettled:
		// The happy replay: exactly the payment the first attempt produced.
		if intent.PaymentID == "" {
			return Payment{}, fmt.Errorf("payments: intent %s is SETTLED with no payment id", intent.ID)
		}
		p, ok, err := s.intents.GetPaymentByID(ctx, customerID, intent.PaymentID)
		if err != nil {
			return Payment{}, err
		}
		if !ok {
			// A SETTLED intent whose payment is not visible to this tenant. Refusing beats charging.
			return Payment{}, ErrPaymentInFlight
		}
		return p, nil
	case IntentCharging:
		// A stamped ref means the processor approved and only the settle is outstanding: settle it
		// against that ref, never a second charge. No ref means the processor's answer was lost.
		if intent.GatewayRef != "" {
			return s.resume(ctx, customerID, req, stamps, intent)
		}
		return Payment{}, ErrPaymentInFlight
	case IntentPending:
		// The crash window. Refuse, see ErrPaymentInFlight's doc comment for why this is not a retry.
		return Payment{}, ErrPaymentInFlight
	default:
		// FAILED/ABANDONED are re-claimable, so ClaimIntent would have taken them and we would not be
		// here. Reaching this line means the row changed between the claim and the read; the only safe
		// answer to a state we cannot explain is not to charge.
		return Payment{}, ErrPaymentInFlight
	}
}

// canonicalInvoiceIDs renders the selection the same way idempotencyKey hashes it, so the recorded
// intent and the key it is stored under describe the same request.
func canonicalInvoiceIDs(ids []string) string {
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	return strings.Join(sorted, ",")
}

// findByGatewayRef looks for an already-persisted payment with the given gateway ref
// (idempotent-replay guard). Empty ref never matches. It delegates to a single indexed
// store lookup rather than scanning the tenant's entire payment history (sec. 4.2 #7).
func (s *Service) findByGatewayRef(ctx context.Context, customerID, ref string) (Payment, bool, error) {
	if ref == "" {
		return Payment{}, false, nil
	}
	return s.store.FindByGatewayRef(ctx, customerID, ref)
}

// isPayableStatus reports whether an invoice in this status may receive a portal payment.
// Only an open balance (FINALIZED) or a partly-settled balance (PARTIALLY_PAID) is payable;
// PAID/VOID/DRAFT and anything else are rejected before the gateway is charged.
func isPayableStatus(status string) bool {
	return status == "FINALIZED" || status == "PARTIALLY_PAID"
}

// paymentMethodFor maps a stored-method type (CARD|BANK) to a payments.method CHECK value
// (0010): a bank/ACH method settles as EFT, everything else as CARD.
func paymentMethodFor(methodType string) string {
	if methodType == "BANK" {
		return "EFT"
	}
	return "CARD"
}

// idempotencyKey RE-BINDS idempotency to {customer_id, request hash}: a stable sha256 over
// the tenant, the sorted invoice selection, and the funding source. The client-supplied
// IdempotencyKey is deliberately excluded so a client cannot force or collide a key across
// accounts (sec. 4.2). Two genuinely identical retries hash identically; any change to the
// selection or funding source is a distinct charge.
func idempotencyKey(customerID string, req PayRequest) string {
	ids := append([]string(nil), req.InvoiceIDs...)
	sort.Strings(ids)
	canonical := strings.Join([]string{
		customerID,
		strings.Join(ids, ","),
		req.PaymentMethodID,
		req.Nonce,
	}, "|")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// hasDuplicates reports whether ids contains a repeated value (a duplicate would collide on
// the payment_allocations primary key and double-count the balance).
func hasDuplicates(ids []string) bool {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			return true
		}
		seen[id] = struct{}{}
	}
	return false
}
