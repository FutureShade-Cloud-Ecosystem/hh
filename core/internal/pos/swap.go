package pos

// swap.go, sub item 10.5: the swap. ONE move that returns goods and rings a new order: the credit note
// across orders and the new order commit in ONE transaction (the single return's and the sale's own
// bodies, returnAcrossInTx and saleInTx), the credit is spent on the new order, what the new order does
// not take goes back by the original tender (Q7, a real refund on a rail), what it takes beyond the
// credit is paid by an ordinary tender, and the two documents are linked both ways in the same
// transaction (pos_swaps). The figures are the server's (Q8): Swap and SwapPreview run ONE function,
// swapFigures, so the preview and the commit can never differ.
//
// Money, in the books: each credit note child books its usual reversal, but the part spent on the new
// order credits the receivable instead of the refund rail (pos.return.committed applied_cents), the
// new order's invoice debits the receivable as always, and the invoice is settled by that credit with
// no tender. The receivable nets to the shortfall alone.

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// swapNoteKey and swapSaleKey derive the two documents' own idempotency keys from the swap's.
// reservedSwapKeyPrefix is the idempotency-key namespace the swap derives its credit note's key in.
const reservedSwapKeyPrefix = "swap:"

func swapNoteKey(key string) string { return reservedSwapKeyPrefix + key }
func swapSaleKey(key string) string {
	// The sale's key is a uuid column everywhere it travels (the ring, the card legs): derive one,
	// deterministically, so a retry of the swap names the same sale.
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos-swap-sale:"+key)).String()
}

// SwapInput is the idempotent POST /api/v1/pos/swaps body: the goods coming back (the credit note's
// orders and lines), the goods going out (the new order's lines), the tender for any shortfall and,
// optionally, the rail the leftover goes back on (it must be the original tender).
type SwapInput struct {
	IdempotencyKey string                   `json:"idempotency_key"`
	CustomerID     string                   `json:"customer_id"`
	Orders         []ReturnAcrossOrderInput `json:"orders"`
	Reason         string                   `json:"reason"`
	LeftoverMethod string                   `json:"leftover_method,omitempty"` // CASH | CARD | ACCOUNT_CREDIT, the original tender
	TillSessionID  string                   `json:"till_session_id"`

	// The new order.
	SaleType      string          `json:"sale_type"` // COLLECT_NOW default; COLLECT_LATER, DELIVERED; never COD
	Tender        string          `json:"tender"`    // the shortfall's tender: CASH (default), CARD, CHEQUE, ACCOUNT
	PaymentToken  string          `json:"payment_token"`
	CardType      string          `json:"card_type,omitempty"`
	SignatureRef  string          `json:"signature_ref"`
	RequestedDate string          `json:"requested_date"`
	Notes         string          `json:"notes"`
	POJobRef      string          `json:"po_job_ref,omitempty"`
	SalespersonID string          `json:"salesperson_id,omitempty"`
	JobAddress    string          `json:"job_address,omitempty"`
	JobReference  string          `json:"job_reference,omitempty"`
	Lines         []LineInput     `json:"lines"`
	Discount      *DiscountInput  `json:"discount,omitempty"`
	Relief        *ReliefDecision `json:"relief,omitempty"`

	// ApproverCredential rides X-Approver-Authorization (the note's refund approval); the handler
	// reads it from the header.
	ApproverCredential string `json:"-"`
}

// SwapNewOrder is the new order's server figures.
type SwapNewOrder struct {
	SubtotalCents int64 `json:"subtotal_cents"`
	TaxCents      int64 `json:"tax_cents"`
	TotalCents    int64 `json:"total_cents"`
}

// SwapPreview is what the screen shows before the swap is rung: the credit, the new order, the part of
// the credit spent on it, the leftover going back, the shortfall to collect. Server figures only.
type SwapPreview struct {
	Credit         ReturnPreview `json:"credit"`
	NewOrder       SwapNewOrder  `json:"new_order"`
	AppliedCents   int64         `json:"applied_cents"`
	LeftoverCents  int64         `json:"leftover_cents"`
	ShortfallCents int64         `json:"shortfall_cents"`
	LeftoverMethod string        `json:"leftover_method"`
}

// SwapResult is the committed swap (answered verbatim on an idempotent replay): both documents, each
// carrying the swap's id, and the figures.
type SwapResult struct {
	SwapID         string     `json:"swap_id"`
	Note           ReturnNote `json:"note"`
	Sale           Sale       `json:"sale"`
	CreditCents    int64      `json:"credit_cents"`
	AppliedCents   int64      `json:"applied_cents"`
	LeftoverCents  int64      `json:"leftover_cents"`
	ShortfallCents int64      `json:"shortfall_cents"`
	LeftoverMethod string     `json:"leftover_method"`
	Replayed       bool       `json:"replayed"`
}

// swapRow is the pos_swaps row.
type swapRow struct {
	ID             string
	IdempotencyKey string
	NoteID         string
	OrderID        string
	CustomerID     string
	BranchID       string
	CreditCents    int64
	AppliedCents   int64
	NewTotalCents  int64
	LeftoverCents  int64
	ShortfallCents int64
	LeftoverMethod string
	CreatedBy      string
}

// swapFig is the swap's priced shape, one computation for the preview and the commit.
type swapFig struct {
	credit    ReturnPreview
	products  []string // the products the swap's two halves move, sorted and unique
	ocs       []returnOrderCtx
	subtotal  int64
	tax       int64
	total     int64
	applied   int64
	leftover  int64
	shortfall int64
	method    string
	tender    string
	saleType  string
}

// originalTenderRail maps an order's original tender onto the refund rail that returns by it.
func originalTenderRail(oc returnOrderCtx) (string, bool) {
	switch oc.SaleTender {
	case TenderCash:
		return RefundCash, true
	case TenderCard:
		return RefundCard, true
	case TenderAccount, "":
		return RefundAccountCredit, true // "" is an order a quote became: its money sits on the account
	}
	return "", false
}

func productIDsOf(lines []LineInput) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.ProductID)
	}
	return out
}

// swapFigures prices a swap: the credit (the commit's own guard, no locks), the new order (the ring's
// own resolver), and the split of the credit between the new order, the shortfall and the leftover.
func (s *Service) swapFigures(ctx context.Context, in SwapInput) (swapFig, error) {
	var fig swapFig
	if len(in.Lines) == 0 {
		return fig, errNoLines
	}
	fig.saleType = in.SaleType
	if fig.saleType == "" {
		fig.saleType = SaleCollectNow
	}
	switch fig.saleType {
	case SaleCollectNow, SaleCollectLater, SaleDelivered:
	default:
		return fig, clientErr(http.StatusUnprocessableEntity, "pos: a swap rings a collect now, collect later or delivered order, not %s", fig.saleType)
	}
	fig.tender = in.Tender
	if fig.tender == "" {
		fig.tender = TenderCash
	}
	switch fig.tender {
	case TenderCash, TenderCard, TenderCheque, TenderAccount:
	default:
		return fig, errBadTender
	}
	credit, ocs, retProducts, err := s.previewNoteProducts(ctx, ReturnPreviewInput{CustomerID: in.CustomerID, Orders: in.Orders}, false)
	if err != nil {
		return fig, err
	}
	fig.credit, fig.ocs = credit, ocs
	seenProduct := map[string]bool{}
	for _, id := range append(retProducts, productIDsOf(in.Lines)...) {
		if id != "" && !seenProduct[id] {
			seenProduct[id] = true
			fig.products = append(fig.products, id)
		}
	}
	sort.Strings(fig.products)
	for _, oc := range ocs {
		// A swap spends credit that is already in hand: a source order whose invoice is still open
		// would have the credit offset what the customer still owes first, not buy anything.
		if oc.InvoiceStatus != "PAID" {
			return fig, clientErr(http.StatusUnprocessableEntity,
				"pos: order %s still has an open invoice, so its return is not credit in hand; return it as a credit note first, then ring the new order", oc.OrderNo)
		}
	}
	cust, err := s.store.ReadCustomer(ctx, in.CustomerID)
	if err != nil {
		return fig, err
	}
	// The new order is priced with the relief the swap carries, exactly as the ring prices it.
	taxReason := TaxStdON13
	if in.Relief != nil && in.Relief.ReasonCode != "" {
		taxReason = in.Relief.ReasonCode
	}
	priced, err := s.priceAndAssess(ctx, saleMoneyInput{
		customerID: in.CustomerID, customerType: cust.CustomerType, branchID: credit.BranchID,
		lines: in.Lines, taxReason: taxReason, discount: in.Discount,
	})
	if err != nil {
		return fig, err
	}
	fig.subtotal, fig.tax, fig.total = priced.subtotal, priced.tax, priced.total
	fig.applied = credit.TotalCents
	if fig.applied > fig.total {
		fig.applied = fig.total
	}
	fig.leftover = credit.TotalCents - fig.applied
	fig.shortfall = fig.total - fig.applied

	// Q7: the leftover goes back by the original tender. Every source order must name the same rail,
	// and a rail the caller names must be it.
	method, uniform := "", true
	for i, oc := range ocs {
		m, ok := originalTenderRail(oc)
		if !ok {
			if fig.leftover > 0 {
				return fig, clientErr(http.StatusUnprocessableEntity,
					"pos: order %s was paid by %s, which has no refund rail; return it as a credit note and ring the new order apart", oc.OrderNo, strings.ToLower(oc.SaleTender))
			}
			m = RefundAccountCredit
		}
		if i == 0 {
			method = m
		} else if m != method {
			uniform = false
		}
	}
	if fig.leftover > 0 {
		if !uniform {
			return fig, clientErr(http.StatusUnprocessableEntity,
				"pos: these orders were paid by different tenders and the leftover goes back by the original tender; swap them one tender at a time")
		}
		if in.LeftoverMethod != "" && in.LeftoverMethod != method {
			return fig, clientErr(http.StatusUnprocessableEntity,
				"pos: the leftover goes back by the original tender, %s, not %s", method, in.LeftoverMethod)
		}
	}
	fig.method = method
	return fig, nil
}

func (f swapFig) preview() SwapPreview {
	return SwapPreview{
		Credit: f.credit, NewOrder: SwapNewOrder{SubtotalCents: f.subtotal, TaxCents: f.tax, TotalCents: f.total},
		AppliedCents: f.applied, LeftoverCents: f.leftover, ShortfallCents: f.shortfall, LeftoverMethod: f.method,
	}
}

// PreviewSwap answers the swap's server figures with nothing written.
func (s *Service) PreviewSwap(ctx context.Context, in SwapInput) (SwapPreview, error) {
	fig, err := s.swapFigures(ctx, in)
	if err != nil {
		return SwapPreview{}, err
	}
	return fig.preview(), nil
}

// errSwapChanged is the swap whose priced figures moved under it.
var errSwapChanged = clientErr(http.StatusConflict, "pos: the swap's figures changed while it was being rung; review the swap and ring it again")

// Swap rings the swap: pre-transaction checks and the card capture of any shortfall, then ONE
// transaction for the credit note, the new order and their link. Idempotent on the swap's key.
func (s *Service) Swap(ctx context.Context, in SwapInput) (SwapResult, error) {
	if in.IdempotencyKey == "" {
		return SwapResult{}, errMissingKey
	}
	if strings.HasPrefix(in.IdempotencyKey, reservedRequestKeyPrefix) || strings.HasPrefix(in.IdempotencyKey, reservedNoteKeyPrefix) {
		return SwapResult{}, errReservedKey
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return SwapResult{}, err
	}
	if res, found, rerr := s.replaySwap(ctx, in.IdempotencyKey, caller); rerr != nil || found {
		return res, rerr
	}

	fig, err := s.swapFigures(ctx, in)
	if err != nil {
		return SwapResult{}, err
	}
	branchID := fig.credit.BranchID

	noteIn := ReturnAcrossInput{
		IdempotencyKey: swapNoteKey(in.IdempotencyKey), CustomerID: in.CustomerID, Orders: in.Orders,
		RefundMethod: fig.method, Reason: in.Reason, Restock: true, TillSessionID: in.TillSessionID,
		ApproverCredential: in.ApproverCredential,
		swap:               true, swapCredit: fig.credit.TotalCents, swapApplied: fig.applied, railOptional: fig.leftover == 0,
	}
	notePlan, err := s.prepareReturnAcross(ctx, noteIn)
	if err != nil {
		return SwapResult{}, err
	}
	if notePlan.preReplayed {
		return SwapResult{}, clientErr(http.StatusConflict, "pos: this swap's credit note exists without its swap; ask a manager")
	}

	// The credit side's refusals that can be known now are answered BEFORE any card is charged: the
	// approval gate, judged on the figures the swap was priced with (the transaction judges it again,
	// exact). A swap with a card shortfall must never charge a card and then void it for a refusal
	// the clerk could have been told first.
	priors := make([]int64, len(notePlan.children))
	shares := make([]int64, len(notePlan.children))
	for i, child := range notePlan.children {
		prior, perr := s.store.SumCommittedReturnTotals(ctx, child.oc.OrderID)
		if perr != nil {
			return SwapResult{}, perr
		}
		priors[i], shares[i] = prior, fig.credit.Orders[i].TotalCents
	}
	if _, gerr := s.judgeNoteGate(fig.credit.TotalCents, priors, shares, caller, notePlan.children[0]); gerr != nil {
		var refusal ErrRefundApprovalRequired
		if errors.As(gerr, &refusal) {
			s.record(ctx, audit.SensitiveAccess{
				Action: auditActionRefundApprovalRefused, EntityType: "pos.return_note", EntityID: in.CustomerID,
				ActorID: notePlan.actor, BranchID: branchID, Purpose: "POS_RETURN",
				Reason: refundApprovalRequiredError,
				Extra:  map[string]any{"amount_cents": refusal.RefundCents},
			})
		}
		return SwapResult{}, gerr
	}

	saleIn := SaleInput{
		IdempotencyKey: swapSaleKey(in.IdempotencyKey), BranchID: branchID, TillSessionID: in.TillSessionID,
		CustomerID: in.CustomerID, SaleType: fig.saleType, Tender: fig.tender, PaymentToken: in.PaymentToken,
		CardType: in.CardType, SignatureRef: in.SignatureRef, Relief: in.Relief, RequestedDate: in.RequestedDate,
		Notes: in.Notes, POJobRef: in.POJobRef, SalespersonID: in.SalespersonID, JobAddress: in.JobAddress,
		JobReference: in.JobReference, Lines: in.Lines, Discount: in.Discount,
		swapCreditCents: fig.credit.TotalCents,
	}
	salePlan, early, err := s.prepareSale(ctx, saleIn)
	if early != nil {
		if err == nil {
			err = clientErr(http.StatusConflict, "pos: this swap's new order exists without its swap; ask a manager")
		}
		return SwapResult{}, err
	}
	if err != nil {
		return SwapResult{}, err
	}
	if salePlan.total != fig.total || salePlan.applied != fig.applied {
		if len(salePlan.ring.captured) > 0 {
			salePlan.ring.rank = cardRankReversed
			_, cerr := s.settleFailedCardRing(ctx, salePlan.ring)
			return SwapResult{}, cerr
		}
		return SwapResult{}, errSwapChanged
	}

	var out Sale
	var tenderResults []till.TenderResult
	var note ReturnNote
	var approvedHere, replayedHere bool
	var swapID string
	actor := identity.StaffUserIDFromContext(ctx)
	txErr := s.store.RunInTx(ctx, func(tx pgx.Tx) error {
		// Both halves move stock: take the balance rows of every product either touches, sorted,
		// before the first move, so two crossed swaps queue instead of deadlocking.
		if s.stockLocks != nil {
			if lerr := s.stockLocks.LockStockTx(ctx, tx, branchID, fig.products); lerr != nil {
				return lerr
			}
		}
		var nerr error
		note, replayedHere, approvedHere, nerr = s.returnAcrossInTx(ctx, tx, notePlan)
		if nerr != nil {
			return nerr
		}
		if replayedHere {
			return errSwapTwinWon
		}
		if serr := s.saleInTx(ctx, tx, salePlan, &out, &tenderResults); serr != nil {
			return serr
		}
		var ierr error
		swapID, ierr = s.store.InsertSwap(ctx, tx, swapRow{
			IdempotencyKey: in.IdempotencyKey, NoteID: note.NoteID, OrderID: out.OrderID, CustomerID: in.CustomerID,
			BranchID: branchID, CreditCents: fig.credit.TotalCents, AppliedCents: fig.applied, NewTotalCents: fig.total,
			LeftoverCents: fig.leftover, ShortfallCents: fig.shortfall, LeftoverMethod: fig.method, CreatedBy: actor,
		})
		return ierr
	})
	if txErr != nil {
		s.settleCardRefunds(ctx, notePlan.children, txErr)
		var refusal ErrRefundApprovalRequired
		if errors.As(txErr, &refusal) {
			s.record(ctx, audit.SensitiveAccess{
				Action: auditActionRefundApprovalRefused, EntityType: "pos.return_note", EntityID: in.CustomerID,
				ActorID: notePlan.actor, BranchID: branchID, Purpose: "POS_RETURN",
				Reason: refundApprovalRequiredError,
				Extra:  map[string]any{"amount_cents": refusal.RefundCents},
			})
		}
		if len(salePlan.ring.captured) > 0 {
			salePlan.ring.rank = cardRankReversed
			if _, cerr := s.settleFailedCardRing(ctx, salePlan.ring); cerr != nil && !errors.Is(cerr, ErrSaleFailedCardReversed) {
				// The compensation itself did not end clean (a manager item, an unknown): that answer.
				return SwapResult{}, cerr
			}
			// The card was reversed cleanly: the clerk is told the credit side's own reason, not the
			// reversal (a refusal in words, not a 503).
			if isClientRefusal(txErr) {
				return SwapResult{}, txErr
			}
			if !isUniqueViolationErr(txErr) && !errors.Is(txErr, errSwapTwinWon) {
				return SwapResult{}, ErrSaleFailedCardReversed
			}
		}
		// A twin of the same key won the race: answer its swap.
		if errors.Is(txErr, errSwapTwinWon) || isUniqueViolationErr(txErr) {
			if res, found, rerr := s.replaySwap(ctx, in.IdempotencyKey, caller); rerr == nil && found {
				return res, nil
			}
		}
		return SwapResult{}, txErr
	}
	s.settleCardRefunds(ctx, notePlan.children, nil)
	s.recordNoteCompleted(ctx, notePlan, note, approvedHere)
	s.finishSale(ctx, salePlan, out, tenderResults)
	note.SwapID, out.SwapID = swapID, swapID
	return SwapResult{
		SwapID: swapID, Note: note, Sale: out, CreditCents: fig.credit.TotalCents, AppliedCents: fig.applied,
		LeftoverCents: fig.leftover, ShortfallCents: fig.shortfall, LeftoverMethod: fig.method,
	}, nil
}

// errSwapTwinWon marks the transaction a same-key twin beat to the note.
var errSwapTwinWon = errors.New("pos: a twin of this swap committed first")

// replaySwap answers a committed swap from its key, to a caller the wall admits to its branch.
func (s *Service) replaySwap(ctx context.Context, key string, caller identity.StaffContext) (SwapResult, bool, error) {
	row, found, err := s.store.FindSwapByKey(ctx, key)
	if err != nil || !found {
		return SwapResult{}, false, err
	}
	if err := requireBranchReach(caller, row.BranchID); err != nil {
		return SwapResult{}, false, err
	}
	note, nfound, err := s.store.FindReturnNoteByKey(ctx, swapNoteKey(key))
	if err != nil {
		return SwapResult{}, false, err
	}
	sale, sfound, err := s.findSaleByKey(ctx, swapSaleKey(key))
	if err != nil {
		return SwapResult{}, false, err
	}
	if !nfound || !sfound {
		return SwapResult{}, false, clientErr(http.StatusConflict, "pos: swap %s lost one of its documents; ask a manager", row.ID)
	}
	note.Replayed, sale.Replayed = true, true
	note.SwapID, sale.SwapID = row.ID, row.ID
	return SwapResult{
		SwapID: row.ID, Note: note, Sale: sale, CreditCents: row.CreditCents, AppliedCents: row.AppliedCents,
		LeftoverCents: row.LeftoverCents, ShortfallCents: row.ShortfallCents, LeftoverMethod: row.LeftoverMethod, Replayed: true,
	}, true, nil
}

// isClientRefusal is an error the clerk can act on, in its own words (a refusal or a 4xx), as opposed
// to a fault: after a card was reversed cleanly, the clerk is told this, not the reversal.
func isClientRefusal(err error) bool {
	var ce clientError
	var ap ErrRefundApprovalRequired
	return errors.As(err, &ce) || errors.As(err, &ap)
}
