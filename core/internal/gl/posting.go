package gl

// posting.go, the transactional-outbox posting consumer + the two provider methods bankrec depends on
// (PostExternal / GLBalanceCents) + the boot-time ValidatePostingRules. The consumer owns all PostedTopics
// financial topics (the W5 eight + the W7 POS-return pair); each builder resolves posting_rules (via the
// frozen two-name mapping) and the event's finance facts (through the sibling read seams) into a BALANCED
// journal, posted idempotently on a deterministic key so a redelivered outbox row can never double-post
// (gl_journals.idempotency_key).

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/jackc/pgx/v5"
)

// postingEventType maps an outbox topic to its posting_rules.event_type (the frozen two-name scheme).
func postingEventType(topic string) string {
	if topic == "ar.invoice.finalized" {
		return "invoice.finalized" // the 0007 legacy key
	}
	return topic // every other topic seeds its rules under the literal topic name (0032)
}

// consume is the single outbox handler for all PostedTopics. It builds the journal for the topic, then posts
// it idempotently. A builder that returns post=false is a legitimate no-op (e.g. a zero over/short, a dormant
// COGS leg) and the event is acked. Any error leaves the row PENDING for retry, never a published-without-
// journal financial event.
func (s *Service) consume(ctx context.Context, e notify.Event) error {
	var (
		draft journalDraft
		post  bool
		err   error
	)
	switch e.Topic {
	case "ar.invoice.finalized":
		draft, post, err = s.buildInvoiceFinalized(ctx, e.Payload)
	case "payment.received":
		draft, post, err = s.buildPaymentReceived(ctx, e.Payload)
	case "tax.relief.assessed":
		draft, post, err = s.buildTaxRelief(ctx, e.Payload)
	case "payment.bounced":
		draft, post, err = s.buildPaymentBounced(ctx, e.Payload)
	case "payment.reversed":
		draft, post, err = s.buildPaymentReversed(ctx, e.Payload)
	case "payment.reapplied":
		draft, post, err = s.buildPaymentReapplied(ctx, e.Payload)
	case "payment.paid_out":
		draft, post, err = s.buildPaymentPaidOut(ctx, e.Payload)
	case "order.fulfilled":
		draft, post, err = s.buildOrderFulfilled(ctx, e.Payload)
	case "deposit.released":
		draft, post, err = s.buildDepositReleased(ctx, e.Payload)
	case "deposit.cancelled":
		draft, post, err = s.buildDepositCancelled(ctx, e.Payload)
	case "till.session.completed":
		draft, post, err = s.buildTillSessionCompleted(ctx, e.Payload)
	case "pos.return.committed":
		draft, post, err = s.buildPosReturnCommitted(ctx, e.Payload)
	case "order.returned":
		draft, post, err = s.buildOrderReturned(ctx, e.Payload)
	case "pos.return.written_off":
		draft, post, err = s.buildPosReturnWrittenOff(ctx, e.Payload)
	case "pos.sale.committed", "bank.recon.completed":
		// No direct GL effect: a POS sale's revenue/cash post via its own invoice.finalized / payment.received
		// events; bankrec posts its clearing journals directly through PostExternal. Ack to avoid double-count.
		return nil
	default:
		return fmt.Errorf("gl: no posting builder for topic %q", e.Topic)
	}
	if err != nil {
		return err
	}
	if !post {
		return nil
	}
	if !draft.balanced() {
		return fmt.Errorf("gl: built journal for %q is unbalanced (Σdebit=%d Σcredit=%d)", e.Topic, draft.debitTotal(), draft.creditTotal())
	}
	draft.Status = StatusPosted
	if draft.OccurredAt.IsZero() {
		draft.OccurredAt = s.clock()
	}
	if _, _, err := s.store.InsertJournal(ctx, draft); err != nil {
		return fmt.Errorf("gl: post %q journal: %w", e.Topic, err)
	}
	return nil
}

// pick resolves a required posting-rule role to an account code, erroring loudly when a rule is missing (the
// ValidatePostingRules boot check makes this unreachable in a healthy config, the guard is defence in depth).
func pick(rules map[string]string, event, role string) (string, error) {
	code, ok := rules[role]
	if !ok || code == "" {
		return "", fmt.Errorf("gl: no posting rule for %s/%s", event, role)
	}
	return code, nil
}

func dr(code string, cents int64, memo string) draftLine {
	return draftLine{AccountCode: code, DebitCents: cents, Memo: memo}
}
func cr(code string, cents int64, memo string) draftLine {
	return draftLine{AccountCode: code, CreditCents: cents, Memo: memo}
}

// - builders (one per posting topic) -

type invoiceFinalizedPayload struct {
	InvoiceID string `json:"invoice_id"`
}

func (s *Service) buildInvoiceFinalized(ctx context.Context, payload []byte) (journalDraft, bool, error) {
	var p invoiceFinalizedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return journalDraft{}, false, fmt.Errorf("gl: invoice.finalized payload: %w", err)
	}
	if p.InvoiceID == "" {
		return journalDraft{}, false, fmt.Errorf("gl: invoice.finalized missing invoice_id")
	}
	if s.invoices == nil {
		return journalDraft{}, false, fmt.Errorf("gl: invoice reader not configured")
	}
	a, err := s.invoices.InvoiceForGL(ctx, p.InvoiceID)
	if err != nil {
		return journalDraft{}, false, err
	}
	et := "invoice.finalized"
	rules, err := s.store.PostingAccounts(ctx, et, a.BranchID)
	if err != nil {
		return journalDraft{}, false, err
	}
	arCode, err := pick(rules, et, "ar")
	if err != nil {
		return journalDraft{}, false, err
	}
	// Sub item 9.5: a supplier deposit document is the liability, not revenue.
	// DR the receivable, CR Customer Deposits (posting rule
	// invoice.finalized/customer_deposit, the 0007 account nothing used until
	// now), the whole total, and no revenue or tax legs: the goods the deposit
	// secures have not booked yet, and their invoice posts its own revenue.
	if a.SourceKind == "SUPPLIER_DEPOSIT" {
		depCode, err := pick(rules, et, "customer_deposit")
		if err != nil {
			return journalDraft{}, false, err
		}
		return journalDraft{
			Source:         SourceInvoice,
			SourceRefID:    p.InvoiceID,
			IdempotencyKey: "ar.invoice.finalized:" + p.InvoiceID,
			Memo:           "Supplier deposit " + a.InvoiceNo,
			BranchID:       a.BranchID,
			OccurredAt:     a.FinalizedAt,
			Lines: []draftLine{
				dr(arCode, a.TotalCents, "AR"),
				cr(depCode, a.TotalCents, "customer deposit"),
			},
		}, true, nil
	}
	revCode, err := pick(rules, et, "revenue")
	if err != nil {
		return journalDraft{}, false, err
	}
	taxCode, err := pick(rules, et, "tax")
	if err != nil {
		return journalDraft{}, false, err
	}
	lines := []draftLine{
		dr(arCode, a.TotalCents-a.HoldbackCents, "AR"),
	}
	if a.HoldbackCents > 0 {
		hbCode, err := pick(rules, et, "holdback")
		if err != nil {
			return journalDraft{}, false, err
		}
		lines = append(lines, dr(hbCode, a.HoldbackCents, "holdback receivable"))
	}
	lines = append(lines, cr(revCode, a.SubtotalCents, "revenue"))
	// A zero tax line is not a line: gl_journal_lines' CHECK (0007) demands a
	// positive debit or credit, so an untaxed invoice's tax leg would fail
	// the insert and dead letter the whole accrual. The tax rule is still
	// resolved above (a config missing its tax account is a loud error even
	// when this invoice collects no tax).
	if a.TaxCents > 0 {
		lines = append(lines, cr(taxCode, a.TaxCents, s.taxWord(ctx)))
	}
	return journalDraft{
		Source:         SourceInvoice,
		SourceRefID:    p.InvoiceID,
		IdempotencyKey: "ar.invoice.finalized:" + p.InvoiceID,
		Memo:           "Invoice " + a.InvoiceNo,
		BranchID:       a.BranchID,
		OccurredAt:     a.FinalizedAt,
		Lines:          lines,
	}, true, nil
}

type paymentReceivedPayload struct {
	PaymentID string `json:"payment_id"`
	Method    string `json:"method"`
}

func (s *Service) buildPaymentReceived(ctx context.Context, payload []byte) (journalDraft, bool, error) {
	var p paymentReceivedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return journalDraft{}, false, fmt.Errorf("gl: payment.received payload: %w", err)
	}
	if p.PaymentID == "" {
		return journalDraft{}, false, fmt.Errorf("gl: payment.received missing payment_id")
	}
	if s.payments == nil {
		return journalDraft{}, false, fmt.Errorf("gl: payment reader not configured")
	}
	// The payment.received payload carries no branch/customer, the PaymentReader seam is the source of truth
	// and picks the 1010 (card) / 1020 (cash/cheque undeposited) debit leg (1020 undeposited-funds doctrine).
	f, err := s.payments.PaymentForGL(ctx, p.PaymentID)
	if err != nil {
		return journalDraft{}, false, err
	}
	et := "payment.received"
	rules, err := s.store.PostingAccounts(ctx, et, f.BranchID)
	if err != nil {
		return journalDraft{}, false, err
	}
	arCode, err := pick(rules, et, "ar")
	if err != nil {
		return journalDraft{}, false, err
	}
	debitRole := "cash" // → 1020 undeposited funds
	if f.Method == "CARD" {
		debitRole = "card_clearing" // → 1010 card settlement clearing
	}
	debitCode, err := pick(rules, et, debitRole)
	if err != nil {
		return journalDraft{}, false, err
	}
	lines := []draftLine{
		dr(debitCode, f.AmountCents, "receipt ("+f.Method+")"),
		cr(arCode, f.AmountCents, "AR settled"),
	}
	return journalDraft{
		Source:         SourcePayment,
		SourceRefID:    p.PaymentID,
		IdempotencyKey: "payment.received:" + p.PaymentID,
		Memo:           "Payment " + f.Method,
		BranchID:       f.BranchID,
		OccurredAt:     s.clock(),
		Lines:          lines,
	}, true, nil
}

type taxReliefPayload struct {
	OrderID string `json:"order_id"`
}

func (s *Service) buildTaxRelief(ctx context.Context, payload []byte) (journalDraft, bool, error) {
	var p taxReliefPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return journalDraft{}, false, fmt.Errorf("gl: tax.relief.assessed payload: %w", err)
	}
	if p.OrderID == "" {
		return journalDraft{}, false, fmt.Errorf("gl: tax.relief.assessed missing order_id")
	}
	if s.taxSplits == nil {
		return journalDraft{}, false, fmt.Errorf("gl: tax split reader not configured")
	}
	// FN 13/8/5 split: full = 13% HST, onCredit = 8% ON provincial portion given point-of-sale relief,
	// net = 5% federal collected. invoice.finalized already CREDITED 2200 the full 13%; the relief REDUCES that
	// liability to the 5% net federal actually collected by DEBITING 2200 for the 8% ON portion, offsetting the
	// FN relief recoverable (1165): DR 2200 8% / CR 1165 8%. After both entries 2200 nets to +5% of subtotal.
	// found=false (standard-rate sale) → no relief legs.
	full, onCredit, net, found, err := s.taxSplits.TaxSplit(ctx, p.OrderID)
	if err != nil {
		return journalDraft{}, false, err
	}
	if !found || onCredit == 0 {
		return journalDraft{}, false, nil
	}
	et := "tax.relief.assessed"
	rules, err := s.store.PostingAccounts(ctx, et, "")
	if err != nil {
		return journalDraft{}, false, err
	}
	hstCode, err := pick(rules, et, "hst_full")
	if err != nil {
		return journalDraft{}, false, err
	}
	reliefCode, err := pick(rules, et, "fn_relief")
	if err != nil {
		return journalDraft{}, false, err
	}
	memo := fmt.Sprintf("FN POS relief 8%% (full=%d net=%d)", full, net)
	lines := []draftLine{
		dr(hstCode, onCredit, "reduce HST payable 8% to net 5%"),
		cr(reliefCode, onCredit, "offset FN relief recoverable"),
	}
	return journalDraft{
		Source:         SourceAdjustment,
		SourceRefID:    p.OrderID,
		IdempotencyKey: "tax.relief.assessed:" + p.OrderID,
		Memo:           memo,
		OccurredAt:     s.clock(),
		Lines:          lines,
	}, true, nil
}

type paymentBouncedPayload struct {
	PaymentID   string `json:"payment_id"`
	BranchID    string `json:"branch_id"`
	Method      string `json:"method"` // CASH/CARD/CHEQUE/EFT/OTHER, picks the reversal leg with Cleared (A-05 emits it)
	AmountCents int64  `json:"amount_cents"`
	FeeCents    int64  `json:"fee_cents"`
	FeeTaxable  bool   `json:"fee_taxable"` // true = the gross NSF fee splits into 4200 income + 2200 HST
	Cleared     bool   `json:"cleared"`     // true = had settled to bank 1000; false = still undeposited/clearing
}

// ontarioHSTRateBps is the 13% Ontario HST rate an unwired service backs out of a tax-inclusive (gross) NSF
// fee: the rate every dealer had before the standard rate came through StandardRateReader.
const ontarioHSTRateBps = 1300

// nsfBackoutRateBps is the standard rate the tax embedded in a tax-inclusive NSF fee is backed out at: the
// dealer's own through the seam, the Ontario 13 percent when none is wired. A failed read is an error, never
// a guess: the posting is retried rather than booked at a rate the dealer does not charge.
func (s *Service) nsfBackoutRateBps(ctx context.Context) (int64, error) {
	if s.standardRate == nil {
		return ontarioHSTRateBps, nil
	}
	bps, err := s.standardRate.StandardRateBps(ctx, "")
	if err != nil {
		return 0, fmt.Errorf("gl: the dealer's standard rate for the NSF fee backout: %w", err)
	}
	return int64(bps), nil
}

func (s *Service) buildPaymentBounced(ctx context.Context, payload []byte) (journalDraft, bool, error) {
	var p paymentBouncedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return journalDraft{}, false, fmt.Errorf("gl: payment.bounced payload: %w", err)
	}
	if p.PaymentID == "" {
		return journalDraft{}, false, fmt.Errorf("gl: payment.bounced missing payment_id")
	}
	branch := p.BranchID
	amount := p.AmountCents
	method := p.Method
	// Fall back to the PaymentReader facts for amount/branch/method when the emitter omitted them.
	if (amount == 0 || branch == "" || method == "") && s.payments != nil {
		if f, err := s.payments.PaymentForGL(ctx, p.PaymentID); err == nil {
			if amount == 0 {
				amount = f.AmountCents
			}
			if branch == "" {
				branch = f.BranchID
			}
			if method == "" {
				method = f.Method
			}
		}
	}
	if amount == 0 {
		return journalDraft{}, false, fmt.Errorf("gl: payment.bounced has no amount")
	}
	et := "payment.bounced"
	rules, err := s.store.PostingAccounts(ctx, et, branch)
	if err != nil {
		return journalDraft{}, false, err
	}
	arCode, err := pick(rules, et, "ar")
	if err != nil {
		return journalDraft{}, false, err
	}
	// The reversal-credit leg mirrors the account the ORIGINAL receipt debited, keyed off method AND status:
	//   DEPOSITED (Cleared) → 1000 bank (the receipt had settled) ; else CARD → 1010 card clearing (where a card
	//   receipt lands first) ; else cash/cheque/EFT → 1020 undeposited funds. A card receipt that bounced before
	//   settlement must reverse 1010, NOT 1020 (that was the A-05 defect).
	bankRole := "undeposited" // reverse the still-undeposited cash/cheque receipt (1020)
	switch {
	case p.Cleared:
		bankRole = "bank" // reverse the settled bank credit (1000)
	case method == "CARD":
		bankRole = "card_clearing" // reverse the still-clearing card receipt (1010)
	}
	bankCode, err := pick(rules, et, bankRole)
	if err != nil {
		return journalDraft{}, false, err
	}
	// Reinstate AR for the reversed receipt + the NSF fee charged to the customer; credit the bank leg for the
	// reversal. The fee credit goes to NSF fee income (4200); when the fee is taxable the GROSS fee (which AR
	// already carries in full) splits into fee income + the embedded HST (2200) so the tax is not booked at $0.
	lines := []draftLine{
		dr(arCode, amount+p.FeeCents, "NSF reversal + fee to AR"),
		cr(bankCode, amount, "reverse receipt"),
	}
	if p.FeeCents > 0 {
		feeCode, err := pick(rules, et, "fee_income")
		if err != nil {
			return journalDraft{}, false, err
		}
		if p.FeeTaxable {
			// Back the HST out of the tax-inclusive gross fee: net = gross·10000/(10000+rate); HST = gross − net.
			rate, err := s.nsfBackoutRateBps(ctx)
			if err != nil {
				return journalDraft{}, false, err
			}
			net := p.FeeCents * 10000 / (10000 + rate)
			hst := p.FeeCents - net
			hstCode, err := pick(rules, et, "hst")
			if err != nil {
				return journalDraft{}, false, err
			}
			lines = append(lines, cr(feeCode, net, "NSF fee income (net)"), cr(hstCode, hst, s.taxWord(ctx)+" on NSF fee"))
		} else {
			lines = append(lines, cr(feeCode, p.FeeCents, "NSF fee income"))
		}
	}
	return journalDraft{
		Source:         SourceAdjustment,
		SourceRefID:    p.PaymentID,
		IdempotencyKey: "payment.bounced:" + p.PaymentID,
		Memo:           "NSF reversal",
		BranchID:       branch,
		OccurredAt:     s.clock(),
		Lines:          lines,
	}, true, nil
}

// paymentReversedPayload is what payments/reversal.go emits in the reversal's own transaction: the reversing row's
// id, the original's id, the full amount, the original's method, and (round 2) the original's status facts and
// branch read under the payment row lock, kept for the audit.
type paymentReversedPayload struct {
	PaymentID         string `json:"payment_id"`          // the reversing (VOID) payments row
	ReversesPaymentID string `json:"reverses_payment_id"` // the original receipt
	AmountCents       int64  `json:"amount_cents"`
	Method            string `json:"method"`
	BranchID          string `json:"branch_id"`
}

// buildPaymentReversed books the reversal of a received payment: Dr AR / Cr 2310 refund_payable, the money held for
// the customer, for EVERY reversal, whatever the tender, session, register or deposit status (SF-2d round 4, ruled).
//
// Why never the receipt's own account: a reversal leaves the original payment RECEIVED, and the till's expected
// tenders count every RECEIVED payment of the session. So the money is either still in the drawer, where it is
// lodged and the bank match clears 1020, or gone unrecorded, where the count shows the shortage and the close posts
// Dr 6350 / Cr 1020. Crediting 1020 at the reversal credits it twice in either case. 2310 holds the amount until the
// act that moves the money clears it (re-application, pay out or write off). The account therefore depends on no
// status, no deposit, no payload fact: there is no fallback by status, and an old payload posts the same way.
// payment.bounced is its own rule (that money never arrived) and is untouched.
//
// The reversal is the FULL amount of the original against AR, as the subledger's contra rows are (reversal.go gives
// back every allocation of the payment): a partly allocated or multi invoice original reverses the same one amount.
// The branch (a fact of the payment, not of its status) comes from the payload, and for a row written before the
// payload carried it, from the payment reader; the amount and method likewise.
func (s *Service) buildPaymentReversed(ctx context.Context, payload []byte) (journalDraft, bool, error) {
	var p paymentReversedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return journalDraft{}, false, fmt.Errorf("gl: payment.reversed payload: %w", err)
	}
	if p.PaymentID == "" || p.ReversesPaymentID == "" {
		return journalDraft{}, false, fmt.Errorf("gl: payment.reversed missing payment_id or reverses_payment_id")
	}
	amount, method, branch := p.AmountCents, p.Method, p.BranchID
	if (amount == 0 || branch == "" || method == "") && s.payments != nil {
		f, err := s.payments.PaymentForGL(ctx, p.ReversesPaymentID)
		if err != nil {
			return journalDraft{}, false, err
		}
		if amount == 0 {
			amount = f.AmountCents
		}
		if branch == "" {
			branch = f.BranchID
		}
		if method == "" {
			method = f.Method
		}
	}
	if amount == 0 {
		return journalDraft{}, false, fmt.Errorf("gl: payment.reversed has no amount")
	}
	et := "payment.reversed"
	rules, err := s.store.PostingAccounts(ctx, et, branch)
	if err != nil {
		return journalDraft{}, false, err
	}
	arCode, err := pick(rules, et, "ar")
	if err != nil {
		return journalDraft{}, false, err
	}
	refundCode, err := pick(rules, et, "refund_payable")
	if err != nil {
		return journalDraft{}, false, err
	}
	return journalDraft{
		Source:         SourcePayment,
		SourceRefID:    p.PaymentID,
		IdempotencyKey: "payment.reversed:" + p.PaymentID,
		Memo:           "Payment reversal " + method,
		BranchID:       branch,
		OccurredAt:     s.clock(),
		Lines: []draftLine{
			dr(arCode, amount, "AR reinstated (payment reversed)"),
			cr(refundCode, amount, "held for the customer (payment reversed)"),
		},
	}, true, nil
}

type paymentReappliedPayload struct {
	BatchID     string `json:"batch_id"`   // the request: one journal per batch
	PaymentID   string `json:"payment_id"` // the reversed original whose held money is applied
	AmountCents int64  `json:"amount_cents"`
	Method      string `json:"method"`
	BranchID    string `json:"branch_id"`
}

// buildPaymentReapplied books the re-application of money a reversal holds: Dr 2310 refund_payable / Cr AR for the
// total applied (SF-2l, ruled 49.3 and 52). It is the mirror of buildPaymentReversed: the reversal put the money in
// 2310 and reopened the invoices' AR, and applying it to invoices clears the liability and takes the AR back out, the
// invoice balances and the AR subledger having moved in the same transaction (payments.applySliceTx). One journal per
// request, keyed on its batch, so a retry of the outbox row posts once. The branch is the payment's, which every
// invoice the request names shares (the write refuses any other).
func (s *Service) buildPaymentReapplied(ctx context.Context, payload []byte) (journalDraft, bool, error) {
	var p paymentReappliedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return journalDraft{}, false, fmt.Errorf("gl: payment.reapplied payload: %w", err)
	}
	if p.BatchID == "" || p.PaymentID == "" {
		return journalDraft{}, false, fmt.Errorf("gl: payment.reapplied missing batch_id or payment_id")
	}
	if p.AmountCents <= 0 {
		return journalDraft{}, false, fmt.Errorf("gl: payment.reapplied has no amount")
	}
	et := "payment.reapplied"
	rules, err := s.store.PostingAccounts(ctx, et, p.BranchID)
	if err != nil {
		return journalDraft{}, false, err
	}
	arCode, err := pick(rules, et, "ar")
	if err != nil {
		return journalDraft{}, false, err
	}
	refundCode, err := pick(rules, et, "refund_payable")
	if err != nil {
		return journalDraft{}, false, err
	}
	return journalDraft{
		Source:         SourcePayment,
		SourceRefID:    p.PaymentID,
		IdempotencyKey: "payment.reapplied:" + p.BatchID,
		Memo:           "Held payment re-applied to invoices " + p.Method,
		BranchID:       p.BranchID,
		OccurredAt:     s.clock(),
		Lines: []draftLine{
			dr(refundCode, p.AmountCents, "held money applied (payment re-applied)"),
			cr(arCode, p.AmountCents, "AR cleared (payment re-applied)"),
		},
	}, true, nil
}

type paymentPaidOutPayload struct {
	PayoutID    string `json:"payout_id"`
	PaymentID   string `json:"payment_id"`
	AmountCents int64  `json:"amount_cents"`
	Kind        string `json:"kind"`
	CreditRole  string `json:"credit_role"`
	Method      string `json:"method"`
	BranchID    string `json:"branch_id"`
}

// paidOutCreditRoles is the closed set of roles a pay out may credit against 2310: the drawer (1020), the bank (1000),
// the card clearing account (1010) and the shortage expense (6350). A payload naming any other is refused, never posted.
var paidOutCreditRoles = map[string]bool{"drawer": true, "bank": true, "card_clearing": true, "write_off": true}

// buildPaymentPaidOut books money a reversal holds leaving 2310 by the act that moves it (SF-2m, SF-2n; ruling 52.3):
// Dr 2310 refund_payable / Cr the account the act moved, named by the payload's credit role: the drawer for cash handed
// back from the till, the bank for a cheque or transfer, the card clearing account for the processor, and the shortage
// expense for a write off after the count. One journal per act, keyed on its payout row. The branch is the payment's.
func (s *Service) buildPaymentPaidOut(ctx context.Context, payload []byte) (journalDraft, bool, error) {
	var p paymentPaidOutPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return journalDraft{}, false, fmt.Errorf("gl: payment.paid_out payload: %w", err)
	}
	if p.PayoutID == "" || p.PaymentID == "" {
		return journalDraft{}, false, fmt.Errorf("gl: payment.paid_out missing payout_id or payment_id")
	}
	if p.AmountCents <= 0 {
		return journalDraft{}, false, fmt.Errorf("gl: payment.paid_out has no amount")
	}
	if !paidOutCreditRoles[p.CreditRole] {
		return journalDraft{}, false, fmt.Errorf("gl: payment.paid_out names no credit account (role %q)", p.CreditRole)
	}
	et := "payment.paid_out"
	rules, err := s.store.PostingAccounts(ctx, et, p.BranchID)
	if err != nil {
		return journalDraft{}, false, err
	}
	refundCode, err := pick(rules, et, "refund_payable")
	if err != nil {
		return journalDraft{}, false, err
	}
	creditCode, err := pick(rules, et, p.CreditRole)
	if err != nil {
		return journalDraft{}, false, err
	}
	return journalDraft{
		Source:         SourcePayment,
		SourceRefID:    p.PaymentID,
		IdempotencyKey: "payment.paid_out:" + p.PayoutID,
		Memo:           "Held payment paid out " + p.Kind,
		BranchID:       p.BranchID,
		OccurredAt:     s.clock(),
		Lines: []draftLine{
			dr(refundCode, p.AmountCents, "held money paid out ("+p.Kind+")"),
			cr(creditCode, p.AmountCents, "money left ("+p.Kind+")"),
		},
	}, true, nil
}

type orderFulfilledPayload struct {
	OrderID  string `json:"order_id"`
	BranchID string `json:"branch_id"`
	GroupID  string `json:"group_id"`
	Lines    []struct {
		ProductID  string  `json:"product_id"`
		QtyBase    float64 `json:"qty_base"`
		ValueCents *int64  `json:"value_cents"`
	} `json:"lines"`
}

func (s *Service) resolveLineCost(ctx context.Context, productID, branchID string, qtyBase float64) (int64, bool, error) {
	if s.costBasis == nil {
		return 0, false, nil
	}
	if branchID != "" {
		if bcb, ok := s.costBasis.(BranchCostBasis); ok {
			return bcb.BranchUnitCostCents(ctx, productID, branchID, qtyBase)
		}
	}
	return s.costBasis.UnitCostCents(ctx, productID, qtyBase)
}

func (s *Service) buildOrderFulfilled(ctx context.Context, payload []byte) (journalDraft, bool, error) {
	var p orderFulfilledPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return journalDraft{}, false, fmt.Errorf("gl: order.fulfilled payload: %w", err)
	}
	if p.OrderID == "" {
		return journalDraft{}, false, fmt.Errorf("gl: order.fulfilled missing order_id")
	}
	// CostBasis is dormant at W5 (NoopCostBasis, found=false) → no COGS leg → no-op ack. The real inventory
	// cost basis arrives with W6; the builder is fully wired for it (DR 5000 COGS / CR 1200 Inventory).
	// PRODUCTION cost basis comes from PO receipts (move_type=RECEIPT / source_type='po_receipt', per
	// migration 0011 + the WAC weighted-average carried on the stock ledger): a product only has a cost
	// basis once it has been received against a purchase order. A sold product with NO cost basis therefore
	// posts revenue+cash but NO COGS leg, silently overstating gross margin. Do NOT swallow that, every
	// missing-cost-basis line is logged WARN below (product id + order id) so it is diagnosable, and the
	// whole draft is skipped only when NO line has a cost basis (total == 0).
	if s.costBasis == nil {
		return journalDraft{}, false, nil
	}
	var total int64
	for _, ln := range p.Lines {
		// Round 2 (S3's P1): the event's own move value is the journal's figure;
		// the average only answers an old row with no value.
		cost, found := int64(0), false
		if ln.ValueCents != nil {
			cost, found = *ln.ValueCents, true
		} else {
			var rerr error
			cost, found, rerr = s.resolveLineCost(ctx, ln.ProductID, p.BranchID, ln.QtyBase)
			if rerr != nil {
				return journalDraft{}, false, rerr
			}
		}
		if found {
			total += cost
			continue
		}
		// OBSERVABLE no-op: this sold line books no COGS because the product has no cost basis (never
		// received against a PO). Left silent this overstates gross margin with no signal, surface it.
		s.log.Warn("gl: COGS skipped, product has no cost basis (no PO receipt); gross margin overstated for this line",
			"product_id", ln.ProductID, "order_id", p.OrderID, "qty_base", ln.QtyBase)
	}
	if total == 0 {
		return journalDraft{}, false, nil
	}
	et := "order.fulfilled"
	rules, err := s.store.PostingAccounts(ctx, et, p.BranchID)
	if err != nil {
		return journalDraft{}, false, err
	}
	cogsCode, err := pick(rules, et, "cogs")
	if err != nil {
		return journalDraft{}, false, err
	}
	invCode, err := pick(rules, et, "inventory")
	if err != nil {
		return journalDraft{}, false, err
	}
	// Round 2c (S3's P1-3): the group's relief emits the group's id on the payload, so the
	// journal's idempotency key is the GROUP's own. Keyed by the order alone, a second group
	// of the same order lands on the first group's key and its COGS is dropped as a
	// duplicate: the second truck's cost never reaches the books. An event with no group (an
	// old row, or its replay) keeps the order wide key, so it stays idempotent with the
	// journal it already created.
	key := "order.fulfilled:" + p.OrderID
	if p.GroupID != "" {
		key = key + ":" + p.GroupID
	}
	return journalDraft{
		Source:         SourceFulfillment,
		SourceRefID:    p.OrderID,
		IdempotencyKey: key,
		Memo:           "COGS on fulfillment",
		BranchID:       p.BranchID,
		OccurredAt:     s.clock(),
		Lines:          []draftLine{dr(cogsCode, total, "COGS"), cr(invCode, total, "inventory relief")},
	}, true, nil
}

// depositReleasedPayload is the release event's payload: the deposit document
// whose liability releases (the order id rides along for queryability, the
// builder reads every fact from the document itself).
type depositReleasedPayload struct {
	InvoiceID string `json:"invoice_id"`
	OrderID   string `json:"order_id"`
}

// buildDepositReleased posts the release of a supplier deposit when its special
// order is fulfilled: DR 2300 Customer Deposits / CR revenue for the deposit's
// whole total, no AR and no tax legs. The deposit document billed AR in full
// at its own finalization (the 0373 rule: DR 1100 / CR 2300) and the sale's
// own invoice booked the sale's whole tax on the rest, so the release is
// exactly the deposit amount moving from the liability to revenue: after it,
// 2300 for the order nets to zero and the revenue recognised for the sale
// equals its pre-tax total. Idempotent per deposit document, so a redelivered
// release posts nothing.
func (s *Service) buildDepositReleased(ctx context.Context, payload []byte) (journalDraft, bool, error) {
	var p depositReleasedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return journalDraft{}, false, fmt.Errorf("gl: deposit.released payload: %w", err)
	}
	if p.InvoiceID == "" {
		return journalDraft{}, false, fmt.Errorf("gl: deposit.released missing invoice_id")
	}
	if s.invoices == nil {
		return journalDraft{}, false, fmt.Errorf("gl: invoice reader not configured")
	}
	a, err := s.invoices.InvoiceForGL(ctx, p.InvoiceID)
	if err != nil {
		return journalDraft{}, false, err
	}
	// Only a deposit document may release: the event names the liability
	// clearing, and an ordinary invoice here is malformed money, refused
	// loudly (the row retries into visibility) never silently posted.
	if a.SourceKind != "SUPPLIER_DEPOSIT" {
		return journalDraft{}, false, fmt.Errorf("gl: deposit.released names invoice %s whose source_kind %q is not SUPPLIER_DEPOSIT",
			p.InvoiceID, a.SourceKind)
	}
	if a.TotalCents == 0 {
		return journalDraft{}, false, nil // a nothing deposit is no liability, the zero-leg no-op posture
	}
	et := "deposit.released"
	rules, err := s.store.PostingAccounts(ctx, et, a.BranchID)
	if err != nil {
		return journalDraft{}, false, err
	}
	depCode, err := pick(rules, et, "customer_deposit")
	if err != nil {
		return journalDraft{}, false, err
	}
	revCode, err := pick(rules, et, "revenue")
	if err != nil {
		return journalDraft{}, false, err
	}
	// Round 2 (S2 P1-2): the release's document is the DELIVERY, not the deposit taken months
	// earlier, so the journal is dated by the delivery fact MarkDelivered records (or the clock when
	// the stamp is absent or the seam unwired). Dated by the deposit's finalization it moved revenue
	// into an already reported month with nothing showing it moved. When the delivery's month is
	// closed the store still files the journal into the earliest open month at or after it, late
	// posted, never refusing.
	at := s.clock()
	if p.OrderID != "" && s.delivery != nil {
		deliveredAt, found, derr := s.delivery.DeliveredAt(ctx, p.OrderID)
		if derr != nil {
			return journalDraft{}, false, fmt.Errorf("gl: read delivery date for order %s: %w", p.OrderID, derr)
		}
		if found && !deliveredAt.IsZero() {
			at = deliveredAt
		}
	}
	return journalDraft{
		Source:         SourceInvoice,
		SourceRefID:    p.InvoiceID,
		IdempotencyKey: "deposit.released:" + p.InvoiceID,
		Memo:           "Deposit released " + a.InvoiceNo,
		BranchID:       a.BranchID,
		OccurredAt:     at,
		Lines: []draftLine{
			dr(depCode, a.TotalCents, "customer deposit released"),
			cr(revCode, a.TotalCents, "revenue"),
		},
	}, true, nil
}

// depositCancelledPayload is the cancel event's payload: the deposit
// document whose liability leaves the books, the order it secured, and the
// disposition the cancel applied (the settlement's own words, billing's
// CREDIT_BACK / KEPT).
type depositCancelledPayload struct {
	InvoiceID   string `json:"invoice_id"`
	OrderID     string `json:"order_id"`
	Disposition string `json:"disposition"`
	// The split the settlement made of the document (row 1228): what the
	// customer had PAID and the unpaid rest it CLOSED; they add up to the
	// document's total. KEPT books the paid part to other income and the
	// closed rest against the receivable.
	PaidCents   int64 `json:"paid_cents"`
	ClosedCents int64 `json:"closed_cents"`
}

// buildDepositCancelled posts the exit of a supplier deposit whose special
// order was cancelled (row 1193): the liability the deposit document booked
// in 2300 Customer Deposits leaves the books, one entry, both dispositions
// under the one journal key deposit.cancelled:<invoice id> so the document
// cancels exactly once whichever disposition the first event carried.
//
//	CREDIT_BACK: DR 2300 / CR the customer's receivable. The settlement
//	  cleared the open document (or credited the account for the paid part),
//	  so the AR leg is the credit on the account the ruling names.
//	KEPT: DR 2300 / CR other income for what was PAID, CR the receivable for
//	  the unpaid rest the settlement closed (row 1228, keep only what was
//	  paid). One entry; no tax leg: the deposit document booked none.
func (s *Service) buildDepositCancelled(ctx context.Context, payload []byte) (journalDraft, bool, error) {
	var p depositCancelledPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return journalDraft{}, false, fmt.Errorf("gl: deposit.cancelled payload: %w", err)
	}
	if p.InvoiceID == "" {
		return journalDraft{}, false, fmt.Errorf("gl: deposit.cancelled missing invoice_id")
	}
	const creditBack, kept = "CREDIT_BACK", "KEPT"
	if p.Disposition != creditBack && p.Disposition != kept {
		return journalDraft{}, false, fmt.Errorf("gl: deposit.cancelled disposition %q is neither %s nor %s",
			p.Disposition, creditBack, kept)
	}
	if s.invoices == nil {
		return journalDraft{}, false, fmt.Errorf("gl: invoice reader not configured")
	}
	a, err := s.invoices.InvoiceForGL(ctx, p.InvoiceID)
	if err != nil {
		return journalDraft{}, false, err
	}
	// Only a deposit document may cancel out: the event names the liability
	// leaving, an ordinary invoice here is malformed money, refused loudly
	// (the row retries into visibility) never silently posted.
	if a.SourceKind != "SUPPLIER_DEPOSIT" {
		return journalDraft{}, false, fmt.Errorf("gl: deposit.cancelled names invoice %s whose source_kind %q is not SUPPLIER_DEPOSIT",
			p.InvoiceID, a.SourceKind)
	}
	if a.TotalCents == 0 {
		return journalDraft{}, false, nil // a nothing deposit is no liability, the zero-leg no-op posture
	}
	et := "deposit.cancelled"
	rules, err := s.store.PostingAccounts(ctx, et, a.BranchID)
	if err != nil {
		return journalDraft{}, false, err
	}
	depCode, err := pick(rules, et, "customer_deposit")
	if err != nil {
		return journalDraft{}, false, err
	}
	var lines []draftLine
	var memo string
	switch p.Disposition {
	case creditBack:
		arCode, err := pick(rules, et, "ar")
		if err != nil {
			return journalDraft{}, false, err
		}
		lines = []draftLine{
			dr(depCode, a.TotalCents, "customer deposit credited back"),
			cr(arCode, a.TotalCents, "AR credited"),
		}
		memo = "Deposit credited back " + a.InvoiceNo
	case kept:
		// Colton's A (row 1228): only what was paid is the dealer's. The
		// unpaid rest was closed by the settlement, so it clears the
		// receivable exactly as the credit back does. The amounts must add up
		// to the document: anything else is malformed money, refused loudly.
		if p.PaidCents < 0 || p.ClosedCents < 0 || p.PaidCents+p.ClosedCents != a.TotalCents {
			return journalDraft{}, false, fmt.Errorf("gl: deposit.cancelled KEPT amounts %d paid + %d closed do not add up to the document total %d",
				p.PaidCents, p.ClosedCents, a.TotalCents)
		}
		lines = []draftLine{dr(depCode, a.TotalCents, "customer deposit kept")}
		if p.PaidCents > 0 {
			oiCode, err := pick(rules, et, "other_income")
			if err != nil {
				return journalDraft{}, false, err
			}
			lines = append(lines, cr(oiCode, p.PaidCents, "other income, the paid part kept"))
		}
		if p.ClosedCents > 0 {
			arCode, err := pick(rules, et, "ar")
			if err != nil {
				return journalDraft{}, false, err
			}
			lines = append(lines, cr(arCode, p.ClosedCents, "AR closed, the unpaid rest"))
		}
		memo = "Deposit kept " + a.InvoiceNo
	}
	return journalDraft{
		Source:         SourceInvoice,
		SourceRefID:    p.InvoiceID,
		IdempotencyKey: "deposit.cancelled:" + p.InvoiceID,
		Memo:           memo,
		BranchID:       a.BranchID,
		OccurredAt:     s.clock(),
		Lines:          lines,
	}, true, nil
}

type posReturnCommittedPayload struct {
	ReturnID      string `json:"return_id"`
	OrderID       string `json:"order_id"`
	InvoiceID     string `json:"invoice_id"`
	RefundMethod  string `json:"refund_method"` // CASH | CARD | ACCOUNT_CREDIT, picks the credit rail
	SubtotalCents int64  `json:"subtotal_cents"`
	TaxCents      int64  `json:"tax_cents"`
	TotalCents    int64  `json:"total_cents"`
	FeeCents      int64  `json:"fee_cents"` // A-06 restocking fee, reduces the rail credit; books to fee_revenue
	// AppliedCents is the part of a swap child's total spent on the swap's new order (10.5): it
	// credits the receivable (the new invoice it settles debits it) instead of the refund rail.
	AppliedCents int64 `json:"applied_cents"`
	// InvoiceOffsetCents is the part of a CASH or CARD return of an order not fully paid that came off
	// the order's open invoice instead of going out on the rail (the principal's split): it credits
	// the receivable, as the applied credit does.
	InvoiceOffsetCents int64  `json:"invoice_offset_cents"`
	BranchID           string `json:"branch_id"`
}

// buildPosReturnCommitted posts the money reversal of a POS return: DR 4000 revenue (subtotal) + DR 2200
// HST (tax) / CR the refund rail, 1020 undeposited for CASH (the drawer paid out), 1010 card clearing
// for CARD (the processor reversal clears there), 1100 AR for ACCOUNT_CREDIT (the customer's account
// absorbed it). The rules are seeded in 0043; the payload carries the full split so no sibling read is
// needed. Idempotent per return.
func (s *Service) buildPosReturnCommitted(ctx context.Context, payload []byte) (journalDraft, bool, error) {
	var p posReturnCommittedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return journalDraft{}, false, fmt.Errorf("gl: pos.return.committed payload: %w", err)
	}
	if p.ReturnID == "" {
		return journalDraft{}, false, fmt.Errorf("gl: pos.return.committed missing return_id")
	}
	if p.TotalCents == 0 {
		return journalDraft{}, false, nil // a zero-value return books nothing
	}
	if p.SubtotalCents+p.TaxCents != p.TotalCents {
		return journalDraft{}, false, fmt.Errorf("gl: pos.return.committed split does not add up (%d+%d != %d)",
			p.SubtotalCents, p.TaxCents, p.TotalCents)
	}
	// A-06 restocking fee: the rail is credited total − fee and the fee credits its own revenue line, so
	// the journal stays balanced (DR subtotal + DR tax = CR total−fee + CR fee). A fee outside [0,total]
	// is a malformed event.
	if p.FeeCents < 0 || p.FeeCents > p.TotalCents {
		return journalDraft{}, false, fmt.Errorf("gl: pos.return.committed fee_cents %d out of range (total %d)",
			p.FeeCents, p.TotalCents)
	}
	if p.AppliedCents < 0 || p.InvoiceOffsetCents < 0 || p.AppliedCents+p.InvoiceOffsetCents+p.FeeCents > p.TotalCents {
		return journalDraft{}, false, fmt.Errorf("gl: pos.return.committed applied_cents %d and invoice_offset_cents %d out of range (total %d, fee %d)",
			p.AppliedCents, p.InvoiceOffsetCents, p.TotalCents, p.FeeCents)
	}
	et := "pos.return.committed"
	rules, err := s.store.PostingAccounts(ctx, et, p.BranchID)
	if err != nil {
		return journalDraft{}, false, err
	}
	var railRole string
	switch p.RefundMethod {
	case "CASH":
		railRole = "undeposited"
	case "CARD":
		railRole = "card_clearing"
	case "ACCOUNT_CREDIT":
		railRole = "ar"
	default:
		return journalDraft{}, false, fmt.Errorf("gl: pos.return.committed unknown refund_method %q", p.RefundMethod)
	}
	railCode, err := pick(rules, et, railRole)
	if err != nil {
		return journalDraft{}, false, err
	}
	var lines []draftLine
	if p.SubtotalCents > 0 {
		revCode, err := pick(rules, et, "revenue")
		if err != nil {
			return journalDraft{}, false, err
		}
		lines = append(lines, dr(revCode, p.SubtotalCents, "revenue reversed"))
	}
	if p.TaxCents > 0 {
		hstCode, err := pick(rules, et, "hst")
		if err != nil {
			return journalDraft{}, false, err
		}
		lines = append(lines, dr(hstCode, p.TaxCents, s.taxWord(ctx)+" reversed"))
	}
	if net := p.TotalCents - p.FeeCents - p.AppliedCents - p.InvoiceOffsetCents; net > 0 {
		lines = append(lines, cr(railCode, net, "refund ("+p.RefundMethod+")"))
	}
	if p.AppliedCents > 0 {
		arCode, err := pick(rules, et, "ar")
		if err != nil {
			return journalDraft{}, false, err
		}
		lines = append(lines, cr(arCode, p.AppliedCents, "credit applied to the swap's new order"))
	}
	if p.InvoiceOffsetCents > 0 {
		arCode, err := pick(rules, et, "ar")
		if err != nil {
			return journalDraft{}, false, err
		}
		lines = append(lines, cr(arCode, p.InvoiceOffsetCents, "taken off the order's open invoice"))
	}
	if p.FeeCents > 0 {
		feeCode, err := pick(rules, et, "fee_revenue")
		if err != nil {
			return journalDraft{}, false, err
		}
		lines = append(lines, cr(feeCode, p.FeeCents, "restocking fee"))
	}
	return journalDraft{
		Source:         SourceAdjustment,
		SourceRefID:    p.ReturnID,
		IdempotencyKey: "pos.return.committed:" + p.ReturnID,
		Memo:           "POS return refund",
		BranchID:       p.BranchID,
		OccurredAt:     s.clock(),
		Lines:          lines,
	}, true, nil
}

type orderReturnedPayload struct {
	OrderID  string `json:"order_id"`
	ReturnID string `json:"return_id"`
	BranchID string `json:"branch_id"`
	Lines    []struct {
		ProductID  string  `json:"product_id"`
		QtyBase    float64 `json:"qty_base"`
		ValueCents *int64  `json:"value_cents"`
	} `json:"lines"`
}

// buildOrderReturned posts the stock side of a restocked POS return, buildOrderFulfilled inverted:
// DR 1200 inventory / CR 5000 COGS at the CostBasis weighted average. found=false (no cost row) skips
// quietly, exactly like the fulfilment COGS leg. Keyed per RETURN (an order can be returned in parts).
func (s *Service) buildOrderReturned(ctx context.Context, payload []byte) (journalDraft, bool, error) {
	var p orderReturnedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return journalDraft{}, false, fmt.Errorf("gl: order.returned payload: %w", err)
	}
	if p.ReturnID == "" {
		return journalDraft{}, false, fmt.Errorf("gl: order.returned missing return_id")
	}
	if s.costBasis == nil {
		return journalDraft{}, false, nil
	}
	var total int64
	for _, ln := range p.Lines {
		// Round 2 (S3's P1): the event's own move value is the journal's figure;
		// the average only answers an old row with no value.
		cost, found := int64(0), false
		if ln.ValueCents != nil {
			cost, found = *ln.ValueCents, true
		} else {
			var rerr error
			cost, found, rerr = s.resolveLineCost(ctx, ln.ProductID, p.BranchID, ln.QtyBase)
			if rerr != nil {
				return journalDraft{}, false, rerr
			}
		}
		if found {
			total += cost
		}
	}
	if total == 0 {
		return journalDraft{}, false, nil
	}
	et := "order.returned"
	rules, err := s.store.PostingAccounts(ctx, et, p.BranchID)
	if err != nil {
		return journalDraft{}, false, err
	}
	invCode, err := pick(rules, et, "inventory")
	if err != nil {
		return journalDraft{}, false, err
	}
	cogsCode, err := pick(rules, et, "cogs")
	if err != nil {
		return journalDraft{}, false, err
	}
	return journalDraft{
		Source:         SourceAdjustment,
		SourceRefID:    p.ReturnID,
		IdempotencyKey: "order.returned:" + p.ReturnID,
		Memo:           "COGS reversed on return",
		BranchID:       p.BranchID,
		OccurredAt:     s.clock(),
		Lines:          []draftLine{dr(invCode, total, "inventory restocked"), cr(cogsCode, total, "COGS reversal")},
	}, true, nil
}

// buildPosReturnWrittenOff posts the write off of goods that came back damaged (sub item 10.3, the Q4
// ruling): DR 5200 inventory adjustments / CR 5000 COGS at the CostBasis weighted average, the cost the
// sale booked to COGS moving to the damage expense so the loss reads as damage, not cost of sales.
// found=false (no cost row) skips quietly like the COGS reversal. Keyed per RETURN.
func (s *Service) buildPosReturnWrittenOff(ctx context.Context, payload []byte) (journalDraft, bool, error) {
	var p orderReturnedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return journalDraft{}, false, fmt.Errorf("gl: pos.return.written_off payload: %w", err)
	}
	if p.ReturnID == "" {
		return journalDraft{}, false, fmt.Errorf("gl: pos.return.written_off missing return_id")
	}
	if s.costBasis == nil {
		return journalDraft{}, false, nil
	}
	var total int64
	for _, ln := range p.Lines {
		// Round 2 (S3's P1): the event's own move value is the journal's figure;
		// the average only answers an old row with no value.
		cost, found := int64(0), false
		if ln.ValueCents != nil {
			cost, found = *ln.ValueCents, true
		} else {
			var rerr error
			cost, found, rerr = s.resolveLineCost(ctx, ln.ProductID, p.BranchID, ln.QtyBase)
			if rerr != nil {
				return journalDraft{}, false, rerr
			}
		}
		if found {
			total += cost
		}
	}
	if total == 0 {
		return journalDraft{}, false, nil
	}
	et := "pos.return.written_off"
	rules, err := s.store.PostingAccounts(ctx, et, p.BranchID)
	if err != nil {
		return journalDraft{}, false, err
	}
	expenseCode, err := pick(rules, et, "expense")
	if err != nil {
		return journalDraft{}, false, err
	}
	cogsCode, err := pick(rules, et, "cogs")
	if err != nil {
		return journalDraft{}, false, err
	}
	return journalDraft{
		Source:         SourceAdjustment,
		SourceRefID:    p.ReturnID,
		IdempotencyKey: "pos.return.written_off:" + p.ReturnID,
		Memo:           "Damaged goods written off on return",
		BranchID:       p.BranchID,
		OccurredAt:     s.clock(),
		Lines:          []draftLine{dr(expenseCode, total, "damaged goods written off"), cr(cogsCode, total, "COGS moved to write off")},
	}, true, nil
}

type tillCompletedPayload struct {
	SessionID      string `json:"session_id"`
	BranchID       string `json:"branch_id"`
	OverShortCents int64  `json:"over_short_cents"`
}

func (s *Service) buildTillSessionCompleted(ctx context.Context, payload []byte) (journalDraft, bool, error) {
	var p tillCompletedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return journalDraft{}, false, fmt.Errorf("gl: till.session.completed payload: %w", err)
	}
	if p.OverShortCents == 0 {
		return journalDraft{}, false, nil // a balanced drawer books nothing
	}
	et := "till.session.completed"
	rules, err := s.store.PostingAccounts(ctx, et, p.BranchID)
	if err != nil {
		return journalDraft{}, false, err
	}
	osCode, err := pick(rules, et, "over_short")
	if err != nil {
		return journalDraft{}, false, err
	}
	undCode, err := pick(rules, et, "undeposited")
	if err != nil {
		return journalDraft{}, false, err
	}
	amt := abs64(p.OverShortCents)
	var lines []draftLine
	if p.OverShortCents < 0 {
		// Short: cash missing → expense. DR 6350 Cash Over/Short, CR 1020 Undeposited Funds.
		lines = []draftLine{dr(osCode, amt, "cash short"), cr(undCode, amt, "undeposited reduced")}
	} else {
		// Over: extra cash → gain. DR 1020 Undeposited Funds, CR 6350 Cash Over/Short.
		lines = []draftLine{dr(undCode, amt, "undeposited increased"), cr(osCode, amt, "cash over")}
	}
	return journalDraft{
		Source:         SourceAdjustment,
		SourceRefID:    p.SessionID,
		IdempotencyKey: "till.session.completed:" + p.SessionID,
		Memo:           "Till over/short",
		BranchID:       p.BranchID,
		OccurredAt:     s.clock(),
		Lines:          lines,
	}, true, nil
}

// - provider surface (bankrec + finstmt + boot) -

func (s *Service) postExternal(ctx context.Context, in ExternalJournal) (string, error) {
	draft, err := s.externalDraft(in)
	if err != nil {
		return "", err
	}
	id, _, err := s.store.InsertJournal(ctx, draft)
	return id, err
}

// postExternalTx is postExternal posted through the caller's transaction.
func (s *Service) postExternalTx(ctx context.Context, tx pgx.Tx, in ExternalJournal) (string, error) {
	draft, err := s.externalDraft(in)
	if err != nil {
		return "", err
	}
	id, _, err := s.store.InsertJournalTx(ctx, tx, draft)
	return id, err
}

// externalDraft validates a caller-built external journal and turns it into the draft both the pool and the
// transaction post share, so the two variants refuse exactly the same inputs.
func (s *Service) externalDraft(in ExternalJournal) (journalDraft, error) {
	if len(in.Lines) == 0 {
		return journalDraft{}, badReq("gl: external journal has no lines")
	}
	source := in.Source
	if source == "" {
		source = SourceAdjustment
	}
	lines := make([]draftLine, 0, len(in.Lines))
	for _, l := range in.Lines {
		lines = append(lines, draftLine{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo})
	}
	at := s.clock()
	// FW1-01: a caller-built journal takes its document's date when the caller knows one (bankrec
	// dates a clearing journal by its statement line); the clock is only the fallback.
	if !in.DocumentDate.IsZero() {
		at = in.DocumentDate
	}
	draft := journalDraft{
		Source:         source,
		SourceRefID:    in.SourceRefID,
		IdempotencyKey: in.IdempotencyKey,
		Memo:           in.Memo,
		BranchID:       in.BranchID,
		Status:         StatusPosted,
		OccurredAt:     at,
		Lines:          lines,
	}
	// Reject any leg carrying a negative debit or credit BEFORE the DB does. A negative debit leg (e.g. a
	// landed-cost over-absorption booking COGS as −9, DIB-189) can still net Σdebit==Σcredit and slip past
	// balanced(), only to be rejected by gl_journal_lines_check (debit_cents > 0) as an opaque constraint error.
	// Failing loudly here as errUnbalanced makes such a malformed journal a caller-visible Go error, not a DB
	// surprise. All legitimate legs are non-negative, so this changes no correct behavior.
	for _, l := range draft.Lines {
		if l.DebitCents < 0 || l.CreditCents < 0 {
			return journalDraft{}, errUnbalanced
		}
	}
	if !draft.balanced() {
		return journalDraft{}, errUnbalanced
	}
	return draft, nil
}

func (s *Service) glBalanceCents(ctx context.Context, accountCode string, year, month int, branchID string) (int64, error) {
	netDebit, normal, err := s.store.AccountBalance(ctx, accountCode, year, month, branchID)
	if err != nil {
		return 0, err
	}
	// Natural orientation: a credit-normal account's balance is the credit surplus (−net_debit).
	if normal == NormalCredit {
		return -netDebit, nil
	}
	return netDebit, nil
}

// ValidatePostingRules is the boot-time parity check (also exposed for the AC8 test): every ACTIVE posting
// rule must resolve to an existing + active account, AND every role the eight posting builders require must
// exist for its event_type. A failure here means an event would post a wrong or zero-leg journal, so it is a
// loud config error surfaced at boot (RegisterPostingHandlers), never a silent bad post.
func (s *Service) ValidatePostingRules(ctx context.Context) error {
	rules, err := s.store.ListActiveRules(ctx)
	if err != nil {
		return err
	}
	var problems []string
	present := map[string]map[string]bool{}
	for _, r := range rules {
		if !r.AccountOK {
			problems = append(problems, fmt.Sprintf("%s/%s→%s (missing/inactive account)", r.EventType, r.LineRole, r.AccountCode))
		}
		if present[r.EventType] == nil {
			present[r.EventType] = map[string]bool{}
		}
		present[r.EventType][r.LineRole] = true
	}
	required := map[string][]string{
		"invoice.finalized":      {"ar", "revenue", "tax"},
		"payment.received":       {"cash", "ar", "card_clearing"},
		"tax.relief.assessed":    {"hst_full", "fn_relief"},
		"payment.bounced":        {"ar", "bank", "undeposited", "card_clearing", "fee_income", "hst"},
		"payment.reversed":       {"ar", "refund_payable"},
		"payment.reapplied":      {"ar", "refund_payable"},
		"payment.paid_out":       {"refund_payable", "drawer", "bank", "card_clearing", "write_off"},
		"till.session.completed": {"over_short", "undeposited"},
		"order.fulfilled":        {"cogs", "inventory"},
		"pos.return.committed":   {"revenue", "hst", "undeposited", "card_clearing", "ar", "fee_revenue"},
		"order.returned":         {"inventory", "cogs"},
		"deposit.released":       {"customer_deposit", "revenue"},
		"deposit.cancelled":      {"customer_deposit", "ar", "other_income"},
		"pos.return.written_off": {"expense", "cogs"},
	}
	for et, roles := range required {
		for _, role := range roles {
			if !present[et][role] {
				problems = append(problems, fmt.Sprintf("missing rule %s/%s", et, role))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("gl: %d posting-rule problem(s): %v", len(problems), problems)
	}
	return nil
}
