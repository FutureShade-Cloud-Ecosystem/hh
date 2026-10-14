// tops_adapters.go is the fold's share of the pin's composition-root
// adapter belt (cmd/server/main.go at 6eaea3ec, the EOF sections the tops
// modules need): pure type translations and read projections, no policy,
// carried verbatim where the dependency landed (the same rule the money
// and operations wire files carry). The identity.Service adapters re-point
// at the bridge's Approvals, the sanctioned class.
package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/taxwords"
	"time"

	"log/slog"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/branches"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dashboard"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dispatch"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messaging"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messagingprovider"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tasks"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tax"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/warranty"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The composition root hands pos.Repository to orderSvc.WithCancelGuards as the cancel predicate's
// sale reader. The compile-time assertion below pins that it keeps answering the order module's
// TRANSACTION-JOINING reader (order.CommittedSaleTxReader, OrderHasCommittedPOSSaleTx): the order
// cancel and guarded line edit prechecks run INSIDE their order transaction, and a repository that
// stopped satisfying the tx reader would silently fall back to the pool read there, a second
// acquisition while the transaction holds its connection, the starvation the tx reader exists to
// delete, with every leg that mocks the seam still green. A signature change on the pos method now
// breaks the build here instead of reverting the connection behaviour quietly.
var _ order.CommittedSaleTxReader = (*pos.Repository)(nil)

// The same pin for the invoice half of the cancel predicate: orderSvc.WithCancelGuards receives
// billingRepo, and assertCancellable falls back to the pool read when it does not answer the
// transaction-joining reader (order.FinalizedInvoiceTxReader, OrderHasFinalizedInvoiceTx). A signature
// change on the billing method breaks the build here instead of quietly taking a second connection.
var _ order.FinalizedInvoiceTxReader = (*billing.Repository)(nil)

// cardChargerAdapter satisfies till.CardCharger over payment.Service's Capture, Compensate and
// BindTx, the creds-free card capture rail: the till passes the leg's facts and the token and gets
// the attempt id and the capture's own processor ref back; creds stay inside RUN-PAY. The seam's
// transaction discipline is the adapter's whole shape (the card charge design 3.3, decision log
// row 786): Capture and Compensate go through the SERVICE's pool-shaped entry points with no
// transaction anywhere in the call stack (pos calls them before the sale transaction opens and
// after it returns), and BindTx rides the SALE's transaction for every statement it makes: the
// FOR UPDATE verify below and payment's guarded bind both go through the tx the till handed in,
// never the pool.
//
// This adapter is also the ONE place the payment module's typed capture answers become the till's
// (the posTaxAdapter pattern): pos reads the till's sentinels, a payment word never crosses that
// boundary. The store beside the service answers the seam's read-only arm (the retry pass that
// must not claim), over the attempt store's own read.
type cardChargerAdapter struct {
	svc   *payment.Service
	store *payment.Store
}

// Capture charges one leg at most once through payment.Service.Capture, translating the typed
// answer. The read-only arm answers an existing row through the attempt store without claiming.
func (a cardChargerAdapter) Capture(ctx context.Context, in till.CardChargeInput) (till.CardCapture, error) {
	if in.ReadOnly {
		return a.readAttempt(ctx, in.LegKey)
	}
	res, err := a.svc.Capture(ctx, payment.CaptureInput{
		LegKey:        in.LegKey,
		SaleKey:       in.SaleKey,
		LegSeq:        in.LegSeq,
		AmountCents:   in.AmountCents,
		BranchID:      in.BranchID,
		TillSessionID: in.TillSessionID,
		CustomerID:    in.CustomerID,
		CardType:      in.CardType,
		CreatedBy:     in.CreatedBy,
		AccountToken:  in.AccountToken,
	})
	out := till.CardCapture{
		AttemptID:    res.Attempt.ID,
		ProcessorRef: res.Attempt.ProcessorRef,
		PaymentID:    res.Attempt.PaymentID,
	}
	if err != nil {
		return out, translateCaptureError(err)
	}
	switch res.Kind {
	case payment.CaptureKindCaptured:
		out.Kind = till.CardCaptureCaptured
	case payment.CaptureKindBound:
		out.Kind = till.CardCaptureBound
	}
	return out, nil
}

// translateCaptureError is the one table that turns the payment module's typed capture answers
// into the till's (both packages' sentinels say the same things; only the words differ).
func translateCaptureError(err error) error {
	switch {
	case errors.Is(err, payment.ErrCaptureInFlight):
		return till.ErrCardInFlight
	case errors.Is(err, payment.ErrCaptureDeclined):
		return till.ErrCardDeclined
	case errors.Is(err, payment.ErrCaptureReversed):
		return till.ErrCardReversed
	case errors.Is(err, payment.ErrCaptureResolved):
		return till.ErrCardResolved
	case errors.Is(err, payment.ErrTillUnresolved):
		return till.ErrCardUnresolvedOnTill
	case errors.Is(err, payment.ErrCaptureNotDispatched):
		return till.ErrCardNotDispatched
	case errors.Is(err, payment.ErrCaptureBadCharge):
		// A malformed leg (no account token, a non positive amount) is refused before the
		// claim: nothing was claimed, nothing was dispatched and no money moved, the same
		// truth the seam's ErrCardNotDispatched carries across the boundary. The
		// invalid-charge word rides the text so the cashier's typed answer names the leg's
		// own fault instead of a bare 500.
		return fmt.Errorf("%w: card_charge_invalid_token (%s)", till.ErrCardNotDispatched, err)
	case errors.Is(err, payment.ErrCaptureNeedsManager), errors.Is(err, payment.ErrCaptureAnswerUnrecorded):
		return till.ErrCardNeedsManager
	}
	return err
}

// readAttempt is the seam's read-only arm: the attempt row's own answer, no claim, no call.
func (a cardChargerAdapter) readAttempt(ctx context.Context, legKey string) (till.CardCapture, error) {
	att, found, err := a.store.GetByLegKey(ctx, legKey)
	if err != nil {
		return till.CardCapture{}, err
	}
	if !found {
		return till.CardCapture{}, till.ErrCardLegUnattempted
	}
	out := till.CardCapture{AttemptID: att.ID, ProcessorRef: att.ProcessorRef, PaymentID: att.PaymentID}
	switch att.Status {
	case payment.AttemptBound:
		out.Kind = till.CardCaptureBound
		return out, nil
	case payment.AttemptCharging, payment.AttemptCaptured:
		return out, till.ErrCardInFlight
	case payment.AttemptDeclined:
		return out, till.ErrCardDeclined
	case payment.AttemptVoided:
		return out, till.ErrCardReversed
	case payment.AttemptUnknown, payment.AttemptVoiding, payment.AttemptVoidUnknown, payment.AttemptVoidDeclined:
		return out, till.ErrCardNeedsManager
	case payment.AttemptResolved:
		return out, till.ErrCardResolved
	}
	return out, fmt.Errorf("payment: attempt %s has the unexpected status %q", att.ID, att.Status)
}

// BindTx binds a captured attempt to the payments row the sale's transaction just wrote. The
// verify read takes the row lock FOR UPDATE ON THE SALE'S TRANSACTION and checks the four facts
// the design names (the status, the amount, the till session, the leg key) against the tender;
// payment's own guarded bind then moves the row, on the same transaction. Every statement rides
// tx; nothing here can ask the pool for a second connection while the sale holds one (row 786).
func (a cardChargerAdapter) BindTx(ctx context.Context, tx pgx.Tx, b till.CardBind) error {
	var status string
	var amount int64
	var session, legKey string
	err := tx.QueryRow(ctx, `
		SELECT status, amount_cents, till_session_id::text, leg_key FROM card_charge_attempts
		 WHERE id = $1::uuid FOR UPDATE`, b.AttemptID).Scan(&status, &amount, &session, &legKey)
	if err != nil {
		return fmt.Errorf("card bind: read the attempt on the sale's transaction: %w", err)
	}
	if status != payment.AttemptCaptured || amount != b.AmountCents || session != b.TillSessionID || legKey != b.LegKey {
		return fmt.Errorf("%w: the attempt is %s (amount %d, session %s, leg %s) against the tender's (amount %d, session %s, leg %s)",
			payment.ErrAttemptNotCaptured, status, amount, session, legKey, b.AmountCents, b.TillSessionID, b.LegKey)
	}
	return a.svc.BindTx(ctx, tx, b.AttemptID, b.PaymentID)
}

// Compensate reverses one captured leg through payment.Service.Compensate, translating the kind.
func (a cardChargerAdapter) Compensate(ctx context.Context, attemptID string) (till.CardCompensation, error) {
	res, err := a.svc.Compensate(ctx, attemptID)
	out := till.CardCompensation{AttemptID: res.Attempt.ID}
	if err != nil {
		return out, err
	}
	switch res.Kind {
	case payment.CompensateKindVoided:
		out.Kind = till.CardCompVoided
	case payment.CompensateKindCommitted:
		out.Kind = till.CardCompCommitted
	case payment.CompensateKindNeedsManager:
		out.Kind = till.CardCompNeedsManager
	}
	return out, nil
}

// cardAttemptAlerter is the sweep's escalation post over messaging's escalations channel, the
// unionSLANotifier pattern (the card charge design section 4): the attempt row is the system of
// record, the post is visibility. The event key carries the attempt and the status it landed on,
// so a state posts once. Packet C's sweep seam consumes this adapter; it lands with the wiring
// now so the handoff it will close is visible at the composition root.
type cardAttemptAlerter struct{ svc *messaging.Service }

// PostCardAttemptAlert posts one escalation naming the attempt's money facts. The body never
// carries a token, a credential or a customer name: the amount, the branch, the till session, the
// sale key and the charge ref are what a person reconciling a settlement report needs.
func (a cardAttemptAlerter) PostCardAttemptAlert(ctx context.Context, attemptID, status string, amountCents int64, branchID, tillSessionID, saleKey, processorRef string) error {
	body := fmt.Sprintf("Card charge attempt %s is %s: amount %d cents, branch %s, till session %s, sale key %s",
		shortRef(attemptID), status, amountCents, shortRef(branchID), shortRef(tillSessionID), shortRef(saleKey))
	if processorRef != "" {
		body += ", charge ref " + processorRef
	}
	_, _, err := a.svc.PostEventToChannelSlug(ctx, "escalations", "card-attempt:"+attemptID+":"+status, body,
		messaging.EventRef{Topic: "payment.card_attempt", Entity: "card_charge_attempt:" + attemptID, Summary: body}, false)
	return err
}

// shortRef names an id the way a person reads it: the head of the uuid.
func shortRef(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// customerTypeAdapter satisfies order.CustomerTypeResolver over customer.Service.ResolveAccount, so
// the DIB-175 order-edit path can reprice an edited line at the account's own tier (CONTRACTOR /
// RETAIL / …) without the order module importing the customer package. It is the same fact the portal
// reorder path passes in as order.ReorderContext.CustomerType (portal/reorder.go), reached here
// through a narrow seam because a staff edit has no portal handler to resolve it first.
type customerTypeAdapter struct{ svc *customer.Service }

func (a customerTypeAdapter) CustomerType(ctx context.Context, customerID string) (string, error) {
	acct, err := a.svc.ResolveAccount(ctx, customerID)
	if err != nil {
		return "", err
	}
	return acct.CustomerType, nil
}

// quoteCustomerTypeAdapter is the same fact for the DIB-56 staff quote builder. It is a distinct type
// from customerTypeAdapter above only because each module declares its OWN narrow seam interface rather
// than sharing one, that is what keeps quote from importing order (or either from importing customer)
// just to name a pricing tier. Identical body, deliberately not shared.
type quoteCustomerTypeAdapter struct{ svc *customer.Service }

func (a quoteCustomerTypeAdapter) CustomerType(ctx context.Context, customerID string) (string, error) {
	acct, err := a.svc.ResolveAccount(ctx, customerID)
	if err != nil {
		return "", err
	}
	return acct.CustomerType, nil
}

// productStockReader satisfies product.StockReader over *inventory.Service (M4 / A-4): the catalogue
// in_stock_only filter resolves the branch-scoped in-stock product ids through inventory (the sole owner
// of stock_balances), so the product module never SELECTs the stock ledger itself.
type productStockReader struct{ svc *inventory.Service }

func (a productStockReader) InStockProductIDs(ctx context.Context, branchID string) ([]string, error) {
	return a.svc.InStockProductIDs(ctx, branchID)
}

// posTaxAdapter satisfies pos.TaxAssessor over *tax.Service (the M5 engine). It maps the pos-declared
// TaxQuoteInput → tax.SaleAssessment and the tax.Quote → pos.QuoteResult. EvidenceAttached is pinned false so
// the pos seam PRESERVES the 0% evidence hard gate, an evidence-gated reason binds the standard rate here,
// bit-identical to W4/W5 (the real evidence path runs in the tax-admin recompute, not at the counter). The
// engine's no-standard-rate refusal translates into pos.ErrTaxRailUnavailable, the sentinel pos's writeErr maps
// to the counter-readable 503: pos never imports the tax package, this adapter is the boundary where the tax
// rail's "not configured" becomes the counter's "tax cannot be assessed", and the ONLY place that translation
// lives (pinned by tax_adapter_translation_test.go against the real engine). The FlatHSTAssessor default never
// produces the refusal, so unwired scaffold compositions are unchanged.
type posTaxAdapter struct{ svc *tax.Service }

func (a posTaxAdapter) AssessSale(ctx context.Context, in pos.TaxQuoteInput) (pos.QuoteResult, error) {
	q, err := a.svc.AssessSale(ctx, tax.SaleAssessment{
		BranchID:         in.BranchID,
		SubtotalCents:    in.SubtotalCents,
		ReasonCode:       in.ReasonCode,
		EvidenceAttached: false,
	})
	if err != nil {
		// The engine's reason-universe refusal is a client-correctable refusal at the counter: translate
		// it into the pos sentinel the handler maps to 400 (the engine's words ride behind the
		// sentinel's). The translation keys on the TYPED refusal, never on the 400 shape, so no later
		// 400-shaped engine refusal can reach the counter worded as an unknown tax reason. Any other
		// refusal, the misconfigured-table shape, stays a server fault: the admin edge is what keeps
		// that shape off the ring, CreateRate refusing to plant the row. Sits beside the sibling
		// refusal translation out of the same engine.
		var unk tax.UnknownReasonError
		if errors.As(err, &unk) {
			return pos.QuoteResult{}, fmt.Errorf("%w: %s", pos.ErrUnknownTaxReason, unk.Error())
		}
		if errors.Is(err, tax.ErrNoStandardRate) {
			return pos.QuoteResult{}, pos.ErrTaxRailUnavailable
		}
		return pos.QuoteResult{}, err
	}
	return pos.QuoteResult{
		TaxCents: q.TaxCents, RateBps: q.RateBps, OnCreditCents: q.OnCreditCents,
		ReasonCode: q.ReasonCode, Jurisdiction: q.Jurisdiction, StandardRateBps: q.StandardRateBps,
	}, nil
}

// dealerRegionAdapter binds the tax engine's DealerRegionSource to the settings module's dealer
// configuration: the one seam the dealer's tax jurisdiction is read through. It reads at every
// assessment, uncached, so a configuration change is live at the next ring.
type dealerRegionAdapter struct{ r *settings.DealerConfigReader }

func (a dealerRegionAdapter) TaxRegion(ctx context.Context) (string, error) {
	c, err := a.r.DealerConfig(ctx)
	return c.TaxRegion, err
}

// dealerCountryAdapter binds the customer module's CountrySource to the same dealer configuration:
// the country the address rules follow, read live at each customer write and each loaded item.
type dealerCountryAdapter struct{ r *settings.DealerConfigReader }

func (a dealerCountryAdapter) DealerCountry(ctx context.Context) (string, error) {
	c, err := a.r.DealerConfig(ctx)
	return c.Country, err
}

// dealerTaxWordsAdapter binds platform/taxwords.Source to the same dealer configuration: the name of
// the dealer's tax ("HST" for an Ontario dealer, "Sales tax" for every other jurisdiction), read live at
// each use so a configuration change shows at the next receipt, quote, journal or printed invoice.
type dealerTaxWordsAdapter struct{ r *settings.DealerConfigReader }

func (a dealerTaxWordsAdapter) TaxLabel(ctx context.Context) (string, error) {
	c, err := a.r.DealerConfig(ctx)
	if err != nil {
		return "", err
	}
	return taxwords.For(c.TaxRegion), nil
}

// newTaxWords builds the dealer's tax word source over the pool.
func newTaxWords(pool *pgxpool.Pool) dealerTaxWordsAdapter {
	return dealerTaxWordsAdapter{r: settings.NewDealerConfigReader(pool)}
}

// newTaxEngine builds a tax engine over the pool bound to the dealer's own jurisdiction. The engine
// is stateless beyond its repository and its region source, so every composition that must assess a
// tax builds its own, and two dealers in one process never share a jurisdiction.
func newTaxEngine(pool *pgxpool.Pool) *tax.Service {
	return tax.NewService(tax.NewRepository(pool)).
		WithDealerRegion(dealerRegionAdapter{r: settings.NewDealerConfigReader(pool)})
}

// posTaxRuleAdapter satisfies pos.TaxRuleSource over *tax.Service: the round rule word the counter's
// tax-rate read answers, the org default config row's round_mode (empty branch). The read's rate side
// needs no adapter: the wired posTaxAdapter above IS the rate source, the same assessor the ring binds
// with, so at the moment of the read the counter's word and the ring's math agree for every ticket the
// counter can ring today: this adapter pins EvidenceAttached false, so an evidence-gated relief reason
// falls back to the standard rate at the ring exactly as this read answers (the fragment's description
// carries the full claim, MANUAL_TAX_REVIEW's ungated row included).
type posTaxRuleAdapter struct{ svc *tax.Service }

func (a posTaxRuleAdapter) RoundMode(ctx context.Context) (string, error) {
	c, err := a.svc.GetConfig(ctx, "")
	if err != nil {
		return "", err
	}
	return c.RoundMode, nil
}

// posStockAdapter satisfies pos.StockDecrementer over *inventory.Service. It maps the pos-declared
// StockDecrementInput → inventory.DecrementInput (distinct named types, same shape) so a COLLECT_NOW sale
// decrements real weighted-avg stock + posts COGS; DELIVERED/COLLECT_LATER records zero moves in-module.
type posStockAdapter struct{ svc *inventory.Service }

func (a posStockAdapter) DecrementForSaleTx(ctx context.Context, tx pgx.Tx, in pos.StockDecrementInput) error {
	lines := make([]inventory.SaleLine, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = inventory.SaleLine{ProductID: l.ProductID, QtyBase: l.QtyBase}
	}
	return a.svc.DecrementForSaleTx(ctx, tx, inventory.DecrementInput{
		OrderID:  in.OrderID,
		BranchID: in.BranchID,
		SaleType: in.SaleType,
		Lines:    lines,
	})
}

// LockStockTx satisfies pos.StockLocker: the swap takes the branch's balance rows of every product its
// two halves move, sorted, ahead of both.
func (a posStockAdapter) LockStockTx(ctx context.Context, tx pgx.Tx, branchID string, productIDs []string) error {
	return a.svc.LockStockTx(ctx, tx, branchID, productIDs)
}

// posRestockAdapter satisfies pos.StockRestocker over *inventory.Service (the posStockAdapter mirror for
// the W7 return restock): one RETURN move per line at the current weighted average, inside the return tx.
type posRestockAdapter struct{ svc *inventory.Service }

func (a posRestockAdapter) RestockForReturnTx(ctx context.Context, tx pgx.Tx, in pos.RestockInput) error {
	lines := make([]inventory.SaleLine, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = inventory.SaleLine{ProductID: l.ProductID, QtyBase: l.QtyBase}
	}
	return a.svc.RestockForReturnTx(ctx, tx, inventory.RestockInput{
		ReturnID: in.ReturnID,
		OrderID:  in.OrderID,
		BranchID: in.BranchID,
		ReturnNo: in.ReturnNo,
		Lines:    lines,
	})
}

// posCardRefunderAdapter satisfies pos.CardRefunder over payment.Service.RefundTx, the creds-free
// reversal rail (the cardChargerAdapter mirror): pos passes the ORIGINAL capture's gateway_ref and gets
// the reversal's own processor ref back; creds stay inside RUN-PAY. The pos Return transaction passes
// STRAIGHT THROUGH: the config read and the rotation write-back ride the caller's transaction, the only
// shape inside a transaction that holds a connection and the per-sale lock, where a pool read would
// deadlock a full pool of Return waiters needing the holder's lock.
type posCardRefunderAdapter struct{ svc *payment.Service }

func (a posCardRefunderAdapter) RefundCard(ctx context.Context, tx pgx.Tx, gatewayRef string, amountCents int64, reference, idempotencyKey string) (string, error) {
	rcpt, err := a.svc.RefundTx(ctx, tx, payment.RefundInput{
		AmountCents:    amountCents,
		ProcessorRef:   gatewayRef,
		Reference:      reference,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return "", err
	}
	return rcpt.ProcessorRef, nil
}

// dispatchSignatureAdapter satisfies dispatch.SignatureCapturer over pos.Service.CaptureTypedSignature
// (DIB-386). The whole attestation crosses the seam, name AND affirmative, because the two refusals
// that make it evidence can only be enforced by the provider if they reach it.
type dispatchSignatureAdapter struct{ svc *pos.Service }

func (a dispatchSignatureAdapter) CaptureTyped(ctx context.Context, in dispatch.TypedAttestation) (string, error) {
	return a.svc.CaptureTypedSignature(ctx, pos.TypedAttestation{
		Purpose:   in.Purpose,
		RefType:   in.RefType,
		RefID:     in.RefID,
		TypedName: in.TypedName,
		Affirmed:  in.Affirmed,
	})
}

// CaptureTypedTx rides the CALLER'S transaction through pos.Service.CaptureTypedSignatureTx: the
// pickups sync confirm captures inside its own transaction, so the signature row and its audit row
// commit or roll back with the confirm and the capture never takes a second pool connection.
func (a dispatchSignatureAdapter) CaptureTypedTx(ctx context.Context, tx pgx.Tx, in dispatch.TypedAttestation) (string, error) {
	return a.svc.CaptureTypedSignatureTx(ctx, tx, pos.TypedAttestation{
		Purpose:   in.Purpose,
		RefType:   in.RefType,
		RefID:     in.RefID,
		TypedName: in.TypedName,
		Affirmed:  in.Affirmed,
	})
}

// stalePickWindow is how long a PICK task may sit QUEUED before the nightly sweep speaks up. Twelve
// hours means "still there the next morning" for anything queued during a working day, without
// nagging about a ticket raised at five o'clock.
const stalePickWindow = 12 * time.Hour

// stageNoticeAdapter satisfies tasks.StageOutbox over the SMS notification queue: when a pick is
// STAGED, a fulfilment notice is enqueued INSIDE the stage transaction, so the customer is never told
// their order is ready by a transaction that rolled back.
//
// The notice carries no phone number. The consumer resolves the recipient at delivery time through
// messagingprovider's RecipientResolver seam, whose default resolves nobody, so with no resolver
// wired this queues a notice and sends nothing, which is the correct behaviour for a system that does
// not yet know any customer's mobile number.
type stageNoticeAdapter struct{ outbox *messagingprovider.Outbox }

func (a stageNoticeAdapter) EnqueueStagedTx(ctx context.Context, tx pgx.Tx, n tasks.StagedNotice) error {
	return a.outbox.EnqueueTx(ctx, tx, messagingprovider.TopicDispatchNotice, messagingprovider.DispatchNotice{
		TaskID:        n.TaskID,
		OrderID:       n.OrderID,
		BranchID:      n.BranchID,
		StageLocation: n.StageLocation,
		StageRef:      n.StageRef,
	})
}

// w3dContactSource satisfies messagingprovider.ContactSource: the NAME of a customer, and the mapping
// from a business reference to the customer it belongs to.
//
// IT CANNOT SUPPLY A PHONE NUMBER, BY CONSTRUCTION. The interface has no method that returns one, and
// that is the entire point: customer_contacts.phone is how we CALL somebody. Whether we may TEXT them,
// and at which number, comes from customer_sms_prefs (0109) and nowhere else.
type w3dContactSource struct {
	customers *customer.Service
	invoices  *billing.Repository
	orders    *order.Repository // UNION-W5: the W3-D declared gap closes, C exposed CustomerForOrder
	log       *slog.Logger
}

func (a w3dContactSource) ContactForCustomer(ctx context.Context, customerID string) (messagingprovider.Contact, error) {
	c, err := a.customers.GetPrimaryContact(ctx, customerID)
	if err != nil {
		// No primary contact is not a reason to withhold a message the customer consented to, the
		// resolver greets them with "Hi" instead of by name.
		return messagingprovider.Contact{}, messagingprovider.ErrNoRecipient
	}
	return messagingprovider.Contact{CustomerID: customerID, Name: c.Name}, nil
}

func (a w3dContactSource) CustomerForInvoice(ctx context.Context, invoiceID string) (string, error) {
	// InvoiceForGL is billing's existing narrow projection of one invoice's header facts (it is what
	// the GL posting path reads). Reusing it here means no new query against another module's table.
	amt, err := a.invoices.InvoiceForGL(ctx, invoiceID)
	if err != nil {
		return "", messagingprovider.ErrNoRecipient
	}
	return amt.CustomerID, nil
}

// CustomerForOrder, the W3-D declared gap, CLOSED at the W5 union: order.Repository.CustomerForOrder
// (swarm W5-C) is the unscoped order→customer read this adapter waited for. ErrNoRecipient on any
// error is the seam contract, a deleted customer is a normal, uninteresting state, not an error.
func (a w3dContactSource) CustomerForOrder(ctx context.Context, orderID string) (string, error) {
	id, err := a.orders.CustomerForOrder(ctx, orderID)
	if err != nil {
		return "", messagingprovider.ErrNoRecipient
	}
	return id, nil
}

// w3dNoticeQueue satisfies quote.NoticeQueue over the SMS outbox and the consent resolver. Nothing it
// does sends anything: every method queues a row, and delivery is the dispatcher's job.
type w3dNoticeQueue struct {
	outbox   *messagingprovider.Outbox
	resolver *messagingprovider.ConsentResolver
}

func (a w3dNoticeQueue) MaySendSMS(ctx context.Context, customerID string, at time.Time) (bool, time.Time, error) {
	return a.resolver.MaySendAt(ctx, customerID, at)
}

func (a w3dNoticeQueue) EnqueueQuoteShare(ctx context.Context, n quote.ShareNotice, notBefore time.Time) (string, error) {
	return a.outbox.EnqueueAt(ctx, messagingprovider.TopicQuoteShare, messagingprovider.QuoteShareNotice{
		QuoteID:    n.QuoteID,
		QuoteNo:    n.QuoteNo,
		CustomerID: n.CustomerID,
		TotalCents: n.TotalCents,
		ValidUntil: n.ValidUntil,
		ShareURL:   n.ShareURL,
		// To is deliberately left empty: this topic's handler holds the REAL resolver, so consent is
		// re-checked at delivery time.
	}, notBefore)
}

func (a w3dNoticeQueue) EnqueueFollowUpSMS(ctx context.Context, n quote.FollowUpNotice, notBefore time.Time) (string, error) {
	return a.outbox.EnqueueAt(ctx, messagingprovider.TopicQuoteFollowupSMS, w3dFollowUpPayload(n), notBefore)
}

func (a w3dNoticeQueue) EnqueueFollowUpEmail(ctx context.Context, n quote.FollowUpNotice) (string, error) {
	// No not_before on the email path: quiet hours are a courtesy owed to a phone that buzzes on a
	// nightstand, not to an inbox.
	return a.outbox.EnqueueAt(ctx, messagingprovider.TopicQuoteFollowupEmail, w3dFollowUpPayload(n), time.Time{})
}

func w3dFollowUpPayload(n quote.FollowUpNotice) messagingprovider.QuoteFollowupNotice {
	return messagingprovider.QuoteFollowupNotice{
		QuoteID:    n.QuoteID,
		QuoteNo:    n.QuoteNo,
		CustomerID: n.CustomerID,
		TotalCents: n.TotalCents,
		ValidUntil: n.ValidUntil,
		StepDay:    n.StepDay,
		ShareURL:   n.ShareURL,
		Email:      n.Email,
	}
}

// w3dContactReader satisfies quote.ContactReader, the email address a follow-up's EMAIL step goes to.
type w3dContactReader struct{ svc *customer.Service }

func (a w3dContactReader) PrimaryContact(ctx context.Context, customerID string) (string, string, error) {
	c, err := a.svc.GetPrimaryContact(ctx, customerID)
	if err != nil {
		// An account with no primary contact has no email step. The cadence records it as SKIPPED with
		// that reason rather than failing the run.
		return "", "", nil
	}
	return c.Name, c.Email, nil
}

type unionARGate struct{ gate billing.ARDispatchGate }

func (u unionARGate) AllowDispatch(ctx context.Context, orderID string) (dispatch.DispatchGateDecision, error) {
	allowed, reason := u.gate.CanDispatch(ctx, orderID)
	return dispatch.DispatchGateDecision{Allow: allowed, Reason: reason, Code: "AR"}, nil
}

// w0bBranchExistence satisfies dashboard.BranchResolver over internal/branches.
//
// It is the same move the two item-5 adapters above make, for the same stated reason: the module
// declares the question it needs answered and the composition root answers it, so no table gains a
// second owner. The difference is that this one does not touch SQL at all, internal/branches is
// already the reader of `locations WHERE loc_type='BRANCH'`, so the adapter is pure type
// translation between two packages that must not import each other, and the query stays in
// branches/repository.go where the rest of that table's reads live.
//
// A NIL SERVICE IS AN ERROR, NOT A FALSE, and the distinction is the whole of the degrade posture.
// dashboard's refuseUnknownBranches treats an ERROR as "I could not check" and lets the save
// proceed on the grant gate alone; it treats FALSE as "that branch is not real" and refuses. An
// unwired directory answering false would refuse every branch-narrowed Home in the building.
type w0bBranchExistence struct{ svc *branches.Service }

func (a w0bBranchExistence) Exists(ctx context.Context, branchID string) (bool, error) {
	if a.svc == nil {
		return false, fmt.Errorf("branch existence: no branch service")
	}
	return a.svc.Exists(ctx, branchID)
}

// w5aStockReserver satisfies order.StockReserver over *inventory.Service. The pgx.Tx passes STRAIGHT
// THROUGH: the order's confirm transaction is the transaction the hold is written on, which is what
// makes "an order that fails to commit leaves no hold" a guarantee rather than a hope.
type w5aStockReserver struct{ svc *inventory.Service }

func (a w5aStockReserver) ReserveTx(ctx context.Context, tx pgx.Tx, in order.StockReserveInput) (order.StockReserveResult, error) {
	res, err := a.svc.ReserveTx(ctx, tx, inventory.ReserveInput{
		ProductID:        in.ProductID,
		BranchID:         in.BranchID,
		LocationID:       in.LocationID,
		ResType:          in.ResType,
		QtyBase:          in.QtyBase,
		SourceType:       in.SourceType,
		SourceRefID:      in.SourceRefID,
		Reason:           in.Reason,
		CreatedBy:        in.CreatedBy,
		EnforceAvailable: in.EnforceAvailable,
	})
	if err != nil {
		return order.StockReserveResult{}, err
	}
	return order.StockReserveResult{
		ReservationID: res.Reservation.ID,
		HoldMode:      res.Reservation.HoldMode,
		Applied:       res.Applied,
	}, nil
}

func (a w5aStockReserver) ReleaseBySourceTx(ctx context.Context, tx pgx.Tx, sourceType, sourceRefID, status string) (int, error) {
	return a.svc.ReleaseBySourceTx(ctx, tx, sourceType, sourceRefID, status)
}

func (a w5aStockReserver) LockHardHoldBalancesTx(ctx context.Context, tx pgx.Tx, sourceType, sourceRefID string) error {
	return a.svc.LockHardHoldBalancesTx(ctx, tx, sourceType, sourceRefID)
}

// w5aPickState satisfies order.PickStateReader over the W2/W3 tasks substrate.
//
// IT READS THE TABLE, NOT tasks.Service, and that is a considered choice with a stated reason: the
// question is "does a PICK task for this order exist at ACCEPTED or beyond", and tasks.Service's
// ListTasks filter is built around branch/status paging for a queue screen, not around a
// single-order existence test, using it would mean paging a queue to answer a boolean. This adapter
// is the same shape as w4cVendorDirectory (a read-only projection satisfied in the composition root),
// and internal/order still contains no tasks SQL and no tasks import. When tasks grows a
// PickStartedForOrder read, this adapter is repointed at it and order does not change.
//
// STAGED and DONE count as started: the load is staged or gone, which is emphatically post-pick.
// QUEUED and PUSHED do NOT: nobody has picked anything up yet, so an ordinary line edit is correct.
type w5aPickState struct{ pool *pgxpool.Pool }

func (a w5aPickState) PickStarted(ctx context.Context, orderID string) (bool, string, error) {
	if a.pool == nil {
		return false, "", nil
	}
	var taskID string
	err := a.pool.QueryRow(ctx, `
		SELECT id::text FROM tasks
		 WHERE order_id = $1::uuid
		   AND task_type = 'PICK'
		   AND status IN ('ACCEPTED','IN_PROGRESS','STAGED','DONE')
		 ORDER BY created_at DESC LIMIT 1`, orderID).Scan(&taskID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, "", nil
		}
		return false, "", err
	}
	return true, taskID, nil
}

// w5aFinanceApprover satisfies order.FinanceApprover over *gl.Service.CheckApprover, the DIB-360/361
// approvals substrate. order passes its OWN policy type through, so the named-approver configuration
// for change orders is separate from the one for journal entries while sharing the same machinery,
// the same fail-open-when-unconfigured rule, and the same admin surface.
type w5aFinanceApprover struct{ svc *gl.Service }

func (a w5aFinanceApprover) CheckApprover(ctx context.Context, policyType, branchID, userID string) error {
	return a.svc.CheckApprover(ctx, policyType, branchID, userID)
}

// w5cStatementQueue satisfies billing.StatementNoticeQueue over the SMS outbox. One method, one line:
// billing states "put this payload on this topic inside my transaction" and the composition root says
// which durable queue that is. internal/billing names no messaging type anywhere.
type w5cStatementQueue struct{ outbox *messagingprovider.Outbox }

func (a w5cStatementQueue) EnqueueTx(ctx context.Context, tx pgx.Tx, topic string, payload any) error {
	return a.outbox.EnqueueTx(ctx, tx, topic, payload)
}

// w6aBranchScope encodes the caller's branch grants for the `($1::uuid[] IS NULL OR branch_id =
// ANY($1::uuid[]))` predicate every query below carries.
//
// THE THREE CASES ARE DELIBERATELY DIFFERENT, AND THE MIDDLE ONE IS THE POINT:
//
//	GLOBAL scope        → nil → SQL NULL → the predicate short-circuits true → no branch filter.
//	branch grants       → the granted ids → ANY(...) → only those branches.
//	NO grants at all    → an EMPTY ARRAY, not nil. ANY('{}') is false for every row, so a caller
//	                      with no branch access measures nothing. Sending nil here would hand them
//	                      the whole company, which is the exact inversion branch scoping exists to
//	                      prevent, and it would look like a working dashboard while doing it.
func w6aBranchScope(sc dashboard.Scope) any {
	if sc.AllBranches {
		return nil
	}
	if sc.BranchIDs == nil {
		return []string{}
	}
	return sc.BranchIDs
}

// w6aDay is the calendar day a "today" tile means, as a date literal every query compares against.
//
// IT IS THE YARD'S DAY, NOT THE SERVER'S. This read `sc.Now.Format(...)`, which takes the calendar
// of whatever timezone the PROCESS happens to carry, so a dealer in Trenton on a server running UTC
// watched "sales today" roll over at eight in the evening, and nobody had chosen that. It is the
// identical defect gl/businessday.go closed for the ledger's fiscal period, and it reached eight
// cockpit measurements: five tile providers and three series providers.
//
// dashboard.HouseDay owns the conversion, so the day boundary and the widget windows resolved beside
// it cannot disagree.
func w6aDay(sc dashboard.Scope) string { return dashboard.HouseDay(sc.Now) }

// w6aCount builds a provider over a query that takes ONLY the branch scope and returns one bigint.
func w6aCount(pool *pgxpool.Pool, sql string, hint func(int64) string) dashboard.MetricFunc {
	return func(ctx context.Context, sc dashboard.Scope) (dashboard.Value, error) {
		var n int64
		if err := pool.QueryRow(ctx, sql, w6aBranchScope(sc)).Scan(&n); err != nil {
			return dashboard.Value{}, err
		}
		v := dashboard.Value{Number: n}
		if hint != nil {
			v.Hint = hint(n)
		}
		return v, nil
	}
}

// w6aCountDay is w6aCount for a query that also takes the calendar day as $2.
func w6aCountDay(pool *pgxpool.Pool, sql string, hint func(int64) string) dashboard.MetricFunc {
	return func(ctx context.Context, sc dashboard.Scope) (dashboard.Value, error) {
		var n int64
		if err := pool.QueryRow(ctx, sql, w6aBranchScope(sc), w6aDay(sc)).Scan(&n); err != nil {
			return dashboard.Value{}, err
		}
		v := dashboard.Value{Number: n}
		if hint != nil {
			v.Hint = hint(n)
		}
		return v, nil
	}
}

func w6aPlural(n int64, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// -- pos · today's sales ------------------------------------------------------------------------
// The invoice total is preferred and the order total is the fallback, because a committed counter
// sale reaches its invoice a moment after it reaches pos_sale: taking only the invoice would make
// the tile lag the till by however long that takes, and taking only the order would ignore the
// invoice's own adjustments. COALESCE in that order is "the best number that exists yet".
func w6aSalesToday(pool *pgxpool.Pool) dashboard.MetricFunc {
	const q = `
		SELECT COALESCE(SUM(COALESCE(i.total_cents, o.total_cents, 0)), 0)::bigint AS cents,
		       COUNT(*)::bigint AS sales
		  FROM pos_sale s
		  LEFT JOIN invoices i ON i.id = s.invoice_id
		  LEFT JOIN orders   o ON o.id = s.order_id
		 WHERE s.status = 'COMMITTED'
		   AND s.created_at >= $2::date
		   AND s.created_at <  $2::date + INTERVAL '1 day'
		   AND ($1::uuid[] IS NULL OR s.branch_id = ANY($1::uuid[]))`
	return func(ctx context.Context, sc dashboard.Scope) (dashboard.Value, error) {
		var cents, sales int64
		if err := pool.QueryRow(ctx, q, w6aBranchScope(sc), w6aDay(sc)).Scan(&cents, &sales); err != nil {
			return dashboard.Value{}, err
		}
		return dashboard.Value{Number: cents, Hint: w6aPlural(sales, "sale", "sales")}, nil
	}
}

// -- order · open orders ------------------------------------------------------------------------
// "Open" is defined by exclusion, and that is on purpose: CLOSED and CANCELLED are the two terminal
// states, and a status added in a later wave is far more likely to be another kind of in-flight than
// another kind of finished. An inclusion list would silently under-count the day a new status ships.
func w6aOrdersOpen(pool *pgxpool.Pool) dashboard.MetricFunc {
	return w6aCount(pool, `
		SELECT COUNT(*)::bigint FROM orders
		 WHERE status NOT IN ('DRAFT', 'CLOSED', 'CANCELLED')
		   AND ($1::uuid[] IS NULL OR branch_id = ANY($1::uuid[]))`,
		func(n int64) string {
			if n == 0 {
				return "Nothing outstanding"
			}
			return "Confirmed and not yet closed"
		})
}

// -- dispatch · deliveries today ----------------------------------------------------------------
// Stops still owed today: scheduled for the day and not yet resolved one way or the other. A
// DELIVERED stop is not a delivery the yard still has to make, and neither is a cancelled one.
func w6aDeliveriesToday(pool *pgxpool.Pool) dashboard.MetricFunc {
	return w6aCountDay(pool, `
		SELECT COUNT(*)::bigint FROM delivery_stops
		 WHERE scheduled_date = $2::date
		   AND status NOT IN ('DELIVERED', 'CANCELLED')
		   AND ($1::uuid[] IS NULL OR branch_id = ANY($1::uuid[]))`,
		func(n int64) string {
			if n == 0 {
				return "Nothing left on today's board"
			}
			return "Scheduled and not yet delivered"
		})
}

// -- tasks · pick queue depth (DIB-305) ---------------------------------------------------------
// Everything a picker can still be handed or is still holding. STAGED is excluded: the load is on
// the ground and the queue is no longer where the work is.
func w6aPickQueue(pool *pgxpool.Pool) dashboard.MetricFunc {
	return w6aCount(pool, `
		SELECT COUNT(*)::bigint FROM tasks
		 WHERE task_type = 'PICK'
		   AND status IN ('QUEUED', 'PUSHED', 'ACCEPTED', 'IN_PROGRESS')
		   AND ($1::uuid[] IS NULL OR branch_id = ANY($1::uuid[]))`,
		func(n int64) string { return w6aPlural(n, "pick waiting", "picks waiting") })
}

// -- tasks · stale picks (DIB-305) --------------------------------------------------------------
// A pick that was pushed to somebody and sat past its own accept deadline. Not "old": tasks carry
// the deadline the push set (accept_deadline_at), so this measures a MISSED COMMITMENT rather than
// an arbitrary age threshold invented on this screen. A task with no deadline cannot be late.
func w6aStalePicks(pool *pgxpool.Pool) dashboard.MetricFunc {
	return w6aCount(pool, `
		SELECT COUNT(*)::bigint FROM tasks
		 WHERE task_type = 'PICK'
		   AND status IN ('QUEUED', 'PUSHED')
		   AND accept_deadline_at IS NOT NULL
		   AND accept_deadline_at < now()
		   AND ($1::uuid[] IS NULL OR branch_id = ANY($1::uuid[]))`,
		func(n int64) string {
			if n == 0 {
				return "Nothing past its accept deadline"
			}
			return "Past the accept deadline"
		})
}

// -- tasks · NEED_WEIGHT pending (DIB-305) ------------------------------------------------------
// A weight the counter asked for and the yard has not sent back. weight_entered IS NULL is the
// question, not the status: a task can be IN_PROGRESS with the number already captured.
func w6aWeightPending(pool *pgxpool.Pool) dashboard.MetricFunc {
	return w6aCount(pool, `
		SELECT COUNT(*)::bigint FROM tasks
		 WHERE task_type = 'NEED_WEIGHT'
		   AND status NOT IN ('DONE', 'CANCELLED')
		   AND weight_entered IS NULL
		   AND ($1::uuid[] IS NULL OR branch_id = ANY($1::uuid[]))`,
		func(n int64) string { return w6aPlural(n, "weight outstanding", "weights outstanding") })
}

// -- billing · AR aging -------------------------------------------------------------------------
// The headline is the total outstanding balance; the parts are the aging buckets the collections
// conversation actually happens in. Buckets are measured from DUE DATE, not issue date, "60 days
// old" and "60 days late" are different amounts of trouble and only one of them is a problem.
func w6aARAging(pool *pgxpool.Pool) dashboard.MetricFunc {
	const q = `
		SELECT COALESCE(SUM(balance_cents), 0)::bigint,
		       COALESCE(SUM(balance_cents) FILTER (WHERE due_date >= $2::date), 0)::bigint,
		       COALESCE(SUM(balance_cents) FILTER (WHERE due_date <  $2::date AND due_date >= $2::date - 30), 0)::bigint,
		       COALESCE(SUM(balance_cents) FILTER (WHERE due_date <  $2::date - 30 AND due_date >= $2::date - 60), 0)::bigint,
		       COALESCE(SUM(balance_cents) FILTER (WHERE due_date <  $2::date - 60), 0)::bigint
		  FROM invoices
		 WHERE status IN ('FINALIZED', 'PARTIALLY_PAID')
		   AND balance_cents > 0
		   AND ($1::uuid[] IS NULL OR branch_id = ANY($1::uuid[]))`
	return func(ctx context.Context, sc dashboard.Scope) (dashboard.Value, error) {
		var total, current, d30, d60, d60plus int64
		if err := pool.QueryRow(ctx, q, w6aBranchScope(sc), w6aDay(sc)).
			Scan(&total, &current, &d30, &d60, &d60plus); err != nil {
			return dashboard.Value{}, err
		}
		hint := "Nothing outstanding"
		if total > 0 {
			hint = "Outstanding receivables"
		}
		return dashboard.Value{
			Number: total,
			Hint:   hint,
			Parts: []dashboard.Part{
				{Label: "Current", Number: current, Kind: dashboard.KindMoneyCents},
				{Label: "1-30", Number: d30, Kind: dashboard.KindMoneyCents},
				{Label: "31-60", Number: d60, Kind: dashboard.KindMoneyCents},
				{Label: "60+", Number: d60plus, Kind: dashboard.KindMoneyCents},
			},
		}, nil
	}
}

// -- inventory · low stock ----------------------------------------------------------------------
// Products at or below the reorder point somebody set for that branch. The comparison is against
// ON HAND across the branch's locations, deliberately not against available-after-reservations:
// this tile answers "what should we be buying", and a reservation does not change what is in the
// yard. The availability question already has its own screen.
func w6aLowStock(pool *pgxpool.Pool) dashboard.MetricFunc {
	return w6aCount(pool, `
		SELECT COUNT(*)::bigint
		  FROM product_reorder_points rp
		 WHERE rp.is_active
		   AND ($1::uuid[] IS NULL OR rp.branch_id = ANY($1::uuid[]))
		   AND COALESCE((SELECT SUM(sb.on_hand_base) FROM stock_balances sb
		                  WHERE sb.product_id = rp.product_id
		                    AND sb.branch_id  = rp.branch_id), 0) <= rp.reorder_point_base`,
		func(n int64) string {
			if n == 0 {
				return "Everything above its reorder point"
			}
			return w6aPlural(n, "product to reorder", "products to reorder")
		})
}

// -- warranty · open claims ---------------------------------------------------------------------
// Open = not in one of the three terminal phases. Same exclusion reasoning as open orders.
func w6aWarrantyOpen(pool *pgxpool.Pool) dashboard.MetricFunc {
	return w6aCount(pool, `
		SELECT COUNT(*)::bigint FROM warranty_claims
		 WHERE phase NOT IN ('CLOSED', 'REJECTED', 'ABANDONED')
		   AND ($1::uuid[] IS NULL OR branch_id = ANY($1::uuid[]))`,
		func(n int64) string { return w6aPlural(n, "claim in flight", "claims in flight") })
}

// -- messaging · unread + escalations -----------------------------------------------------------
// PER-USER, not per-branch: your unread count is yours. The branch scope is deliberately absent
// here and that is not an oversight, channel membership is already the access control, and adding
// a branch predicate on top of it would hide messages from someone who was invited to the channel.
//
// The escalations part rides along as a Part rather than a second tile because "3 unread, 1 of them
// an escalation" is one glance and two tiles are two.
func w6aMessagingUnread(pool *pgxpool.Pool) dashboard.MetricFunc {
	const q = `
		SELECT COUNT(*)::bigint,
		       COUNT(*) FILTER (WHERE c.slug = 'escalations')::bigint
		  FROM msg_members m
		  JOIN msg_channels c  ON c.id = m.channel_id AND NOT c.is_archived
		  JOIN msg_messages ms ON ms.channel_id = m.channel_id
		 WHERE m.user_id = $1::uuid
		   AND ms.deleted_at IS NULL
		   AND ms.author_id IS DISTINCT FROM m.user_id
		   AND (m.last_read_at IS NULL OR ms.created_at > m.last_read_at)`
	return func(ctx context.Context, sc dashboard.Scope) (dashboard.Value, error) {
		if sc.UserID == "" {
			return dashboard.Value{}, errors.New("dashboard: unread needs a caller")
		}
		var unread, esc int64
		if err := pool.QueryRow(ctx, q, sc.UserID).Scan(&unread, &esc); err != nil {
			return dashboard.Value{}, err
		}
		hint := "You are all caught up"
		if unread > 0 {
			hint = "Across your channels"
		}
		v := dashboard.Value{Number: unread, Hint: hint}
		if esc > 0 {
			v.Parts = []dashboard.Part{{Label: "Escalations", Number: esc, Kind: dashboard.KindCount}}
		}
		return v, nil
	}
}

// w6aPaymentsToday is DIB-463's adapter. It measures nothing itself: PaymentsTodayFor folds the
// rollup ROOTS the #payments channels are already showing, so the tile is a READ of the day line
// rather than a second opinion about the same day. Scope.BranchIDs / AllBranches are passed straight
// through, the MetricProvider contract puts that obligation on the adapter, and this one meets it by
// handing the caller's grants to a query that applies them.
func w6aPaymentsToday(svc *messaging.Service) dashboard.MetricFunc {
	return func(ctx context.Context, sc dashboard.Scope) (dashboard.Value, error) {
		today, err := svc.PaymentsTodayFor(ctx, sc.BranchIDs, sc.AllBranches, sc.Now)
		if err != nil {
			return dashboard.Value{}, err
		}
		return dashboard.Value{
			Number: today.Cents,
			AsOf:   today.Day,
			Hint:   w6aPlural(int64(today.Count), "payment", "payments"),
			Parts: []dashboard.Part{
				{Label: "Payments", Number: int64(today.Count), Kind: dashboard.KindCount},
			},
		}, nil
	}
}

// w6bRepAssignmentReader satisfies reporting.RepAssignmentReader over the W5-D rep-assignment
// substrate. It is the same shape as w3dContactSource, w4cStockReceiver and w5cOverrideAuthority
// above: an interface DECLARED BY ITS CONSUMER over a provider the consumer must not import.
//
// WHY THIS ADAPTER IS ONE LINE AND STILL WORTH ITS COMMENT. internal/reporting attributes sales to reps
// in bulk SQL, implementing 0136's ruling (per-order override, then customer-level default, then the
// house queue) as a COALESCE join. internal/messaging OWNS that ruling. Binding this makes the report
// able to ASK the owner, per invoice, whether it still agrees, which is what
// /api/v1/reporting/commission/attribution-check does.
//
// The failure it exists to catch is not dramatic and that is the point: someone changes the resolution
// order in messaging, the report's join keeps doing what it always did, and split-book commission goes
// to the wrong rep for a quarter with nothing on any screen to say so.
//
// messaging.ResolveRep returns (RepAssignment, level, error). An EMPTY rep with level "NONE" is a
// SUCCESSFUL answer, the house queue, and is passed through as such rather than being turned into an
// error, because "nobody owns this account" is a fact the commission report has to be able to report.
type w6bRepAssignmentReader struct{ svc *messaging.Service }

func (a w6bRepAssignmentReader) ResolveRep(ctx context.Context, customerID, orderID string) (string, string, error) {
	assignment, level, err := a.svc.ResolveRep(ctx, customerID, orderID)
	if err != nil {
		return "", "", err
	}
	return assignment.RepUserID, level, nil
}

type w6cInvoiceTaxAdapter struct{ svc *tax.Service }

func (a w6cInvoiceTaxAdapter) AssessSale(ctx context.Context, in billing.TaxQuoteInput) (billing.TaxQuoteResult, error) {
	q, err := a.svc.AssessSale(ctx, tax.SaleAssessment{
		BranchID:         in.BranchID,
		SubtotalCents:    in.SubtotalCents,
		ReasonCode:       in.ReasonCode,
		EvidenceAttached: false,
	})
	if err != nil {
		return billing.TaxQuoteResult{}, err
	}
	return billing.TaxQuoteResult{TaxCents: q.TaxCents, RateBps: q.RateBps}, nil
}

// w6cOrderInvoiceSource binds billing's OrderInvoiceSource to *order.Service. The two structs are
// field-for-field identical and are STILL copied by hand: that is the price of neither module
// importing the other, and it is cheap. The copy is also where a future field mismatch surfaces as a
// compile error in one visible place rather than as a silent coupling.
type w6cOrderInvoiceSource struct{ svc *order.Service }

func (a w6cOrderInvoiceSource) InvoiceableOrder(ctx context.Context, orderID string, groupIDs []string) (billing.OrderInvoiceFacts, error) {
	f, err := a.svc.InvoiceableOrder(ctx, orderID, groupIDs)
	if err != nil {
		return billing.OrderInvoiceFacts{}, err
	}
	out := billing.OrderInvoiceFacts{
		OrderID: f.OrderID, OrderNo: f.OrderNo, CustomerID: f.CustomerID, BranchID: f.BranchID,
		TermsDays: f.TermsDays, TaxReasonCode: f.TaxReasonCode, ClosesOrder: f.ClosesOrder,
		GroupIDs: f.GroupIDs,
	}
	out.Lines = make([]billing.OrderInvoiceLine, 0, len(f.Lines))
	for _, l := range f.Lines {
		out.Lines = append(out.Lines, billing.OrderInvoiceLine{
			LineNo: l.LineNo, OrderLineID: l.OrderLineID, ProductID: l.ProductID,
			Description: l.Description, QtyEntered: l.QtyEntered, UOMEntered: l.UOMEntered,
			QtyBase: l.QtyBase, UnitPriceCents: l.UnitPriceCents, PriceUOM: l.PriceUOM,
			LineTotalCents: l.LineTotalCents,
		})
	}
	return out, nil
}

func (a w6cOrderInvoiceSource) MarkGroupsInvoiced(ctx context.Context, groupIDs []string, invoiceID string, at time.Time) error {
	return a.svc.MarkGroupsInvoiced(ctx, groupIDs, invoiceID, at)
}

// w6cMultiShipInvoicer binds order's MultiShipInvoicer to the writer, W5-A's park, closed.
type w6cMultiShipInvoicer struct{ w *billing.InvoiceWriter }

func (a w6cMultiShipInvoicer) InvoiceGroups(ctx context.Context, orderID string, groupIDs []string, policy string) (string, string, error) {
	doc, err := a.w.InvoiceOrderGroups(ctx, orderID, groupIDs, policy)
	if err != nil {
		return "", "", err
	}
	return doc.InvoiceID, doc.InvoiceNo, nil
}

// w6cWarrantyCreditIssuer binds warranty's CustomerCreditIssuer to the writer, W5-B's park, closed.
//
// SourceKind is stamped WARRANTY_CREDIT so the 0146 producer column tells the truth about who raised
// the document. The idempotency key is warranty's own ("warranty-credit:<claimID>"), passed through
// unchanged: warranty knows what a repeat settlement of one claim means, and billing's UNIQUE index
// is what makes that knowledge binding.
type w6cWarrantyCreditIssuer struct{ w *billing.InvoiceWriter }

func (a w6cWarrantyCreditIssuer) IssueCustomerCredit(ctx context.Context, in warranty.CustomerCreditRequest) (warranty.CustomerCreditResult, error) {
	doc, err := a.w.IssueCreditMemo(ctx, billing.CreditMemoInput{
		CustomerID:     in.CustomerID,
		BranchID:       in.BranchID,
		AmountCents:    in.AmountCents,
		Memo:           in.Memo,
		IdempotencyKey: in.IdempotencyKey,
		SourceKind:     billing.SourceWarrantyCredit,
	})
	if err != nil {
		return warranty.CustomerCreditResult{}, err
	}
	return warranty.CustomerCreditResult{
		CreditMemoID: doc.InvoiceID,
		CreditMemoNo: doc.InvoiceNo,
		GLJournalID:  doc.GLJournalID,
	}, nil
}

// w6cCreditMemoPoster is THE REVERSING ENTRY, and the only adapter here that does arithmetic.
//
// It resolves the SAME three posting roles buildInvoiceFinalized uses, ar / revenue / tax, read from
// gl's own posting_rules for the memo's branch, and emits them with the SIDES EXCHANGED:
//
//	an invoice   DR ar total   CR revenue subtotal   CR tax
//	its reversal DR revenue subtotal   DR tax   CR ar total
//
// Swapping rather than negating is not a style choice. gl_journal_lines carries
// CHECK (debit_cents IS NOT NULL AND ... debit_cents > 0 OR ... credit_cents > 0): a leg of −$500 is
// not representable, deliberately, because a ledger that admits negative amounts has two spellings
// for every entry and reconciles to neither. Reading the codes rather than typing them means a branch
// that overrides its revenue account gets a reversal into the account it actually used.
type w6cCreditMemoPoster struct {
	svc   *gl.Service
	rules *gl.Repository
	words taxwords.Source // the dealer's tax word for the tax leg's memo; nil is "HST"
}

func (a w6cCreditMemoPoster) PostCreditMemoJournal(ctx context.Context, in billing.CreditMemoJournal) (string, error) {
	rules, err := a.rules.PostingAccounts(ctx, "invoice.finalized", in.BranchID)
	if err != nil {
		return "", fmt.Errorf("w6c: resolve credit-memo posting accounts: %w", err)
	}
	// The SIDE SWAP itself is billing.CreditMemoJournal.ReversingLegs, owned and unit-tested by the
	// module whose rule it is. This adapter does one thing: role → account code. (It used to do the
	// swap too, and a mutation survived here because nothing can test a composition-root adapter.)
	var lines []gl.ExternalLine
	word, _ := taxwords.Resolve(ctx, a.words) // a failed read memos the neutral "Tax"
	for _, leg := range in.ReversingLegsFor(word) {
		code := rules[leg.Role]
		if code == "" {
			// Refused, not guessed. A journal posted to an account nobody chose is worse than no
			// journal: the books look complete and are wrong, and IssueCreditMemo's caller is built to
			// report a missing journal honestly.
			return "", fmt.Errorf("w6c: credit memo %s has no %q posting rule for its branch",
				in.CreditMemoNo, leg.Role)
		}
		lines = append(lines, gl.ExternalLine{
			AccountCode: code, DebitCents: leg.DebitCents, CreditCents: leg.CreditCents, Memo: leg.Memo,
		})
	}
	return a.svc.PostExternal(ctx, gl.ExternalJournal{
		Source:         gl.SourceInvoice,
		SourceRefID:    in.CreditMemoID,
		IdempotencyKey: in.IdempotencyKey,
		Memo:           in.Memo,
		BranchID:       in.BranchID,
		// No OccurredAt: gl.ExternalJournal has no such field and stamps the journal with its own
		// clock. Correct here, a credit memo is posted the moment it is issued, so the service clock
		// and the memo's issue date are the same instant. CreditMemoJournal.OccurredAt is carried
		// anyway so a future backdated memo has somewhere to put the date without a seam change.
		Lines: lines,
	})
}

// posAgingAdapter satisfies pos.AgingReader over the AR console's own single-account read.
//
// It contains no arithmetic on purpose. billing/customer_ar_read.go argues at length that one
// definition of "what this account owes and how late it is" must exist in this system, and that a
// second smaller query, however equivalent on the day it is written, is the mistake that ends
// with two screens quoting a customer two different numbers. So this adapter calls that read and
// forwards its own accessors, and the only thing it decides is that a customer who is not in the
// worklist owes nothing (found=false is the good answer, not a missing one, the aging select drops
// accounts whose balances net to zero).
type posAgingAdapter struct{ svc *billing.CreditService }

func (a posAgingAdapter) CustomerAging(ctx context.Context, customerID string) (pos.PastDue, error) {
	row, _, found, err := a.svc.CustomerAging(ctx, customerID)
	if err != nil || !found {
		return pos.PastDue{}, err
	}
	return pos.PastDue{
		PastDueCents: row.OverdueCents(),
		OldestBucket: row.OldestOverdueBucket(),
		OnHold:       row.OnHold,
	}, nil
}

// posAccountAdapter answers the counter's standing read whether an account exists FOR THIS CALLER,
// through the customer module's by-id read, so the standing read stands behind the same reach fence
// (the caller's branches, unhomed accounts visible, a global caller unfenced). An account the caller
// cannot reach answers false exactly as an id nobody has, so the read's 404 carries no existence
// signal and the hold and past-due figures behind it are never read for it. The caller is the
// guard-stamped staff context on the request, never a request field.
type posAccountAdapter struct{ svc *customer.Service }

func (a posAccountAdapter) AccountExists(ctx context.Context, customerID string) (bool, error) {
	_, err := a.svc.GetAccount(ctx, customerID)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, customer.ErrNotFound):
		return false, nil
	default:
		return false, err
	}
}
