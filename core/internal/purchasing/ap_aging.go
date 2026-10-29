package purchasing

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/jackc/pgx/v5"
)

const (
	APBucketCurrent  = "CURRENT"
	APBucketDays30   = "DAYS_30"
	APBucketDays60   = "DAYS_60"
	APBucketDays6090 = "DAYS_60_TO_90"
	APBucketDays90P  = "DAYS_90_PLUS"

	APRowKindVendor     = "VENDOR"
	APRowKindLandedCost = "LANDED_COST"

	APItemKindBill       = "BILL"
	APItemKindLandedCost = "LANDED_COST"

	glSourcePayment     = "PAYMENT"
	glAccountCash       = "1000"
	glAccountAPCode     = "2100"
	glAccountFXGainLoss = "6310"
	roleFXGainLoss      = "fx_gain_loss"

	// glPaymentKeyPrefix is the GL journal key namespace of PayBill: the caller's
	// idempotency key rides the journal (purchasing.bill.payment:<key>), because the
	// instalment table carries no key column and no migration may be added without
	// the lead's word. A re-press is judged by looking the instalment up through
	// that journal.
	glPaymentKeyPrefix = "purchasing.bill.payment:"
)

// LandedCostPayable is the by-value projection of an open landed-cost payable
// owed to a freight carrier or customs broker, read across the landedcost seam.
type LandedCostPayable struct {
	ID               string
	POID             string
	PORef            string
	BranchID         string
	Currency         string
	FreightCadCents  int64
	DutyCadCents     int64
	HandlingCadCents int64
	TotalCadCents    int64
	ChargeKind       string
	PayeeLabel       string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// LandedCostPayableReader is purchasing's read seam over landedcost's open
// payables as of an instant.
type LandedCostPayableReader interface {
	OpenPayablesAsOf(ctx context.Context, asOf time.Time, branchIDs []string, global bool) ([]LandedCostPayable, error)
}

// WithLandedCostPayables binds the landed-cost payable reader seam.
func (s *Service) WithLandedCostPayables(r LandedCostPayableReader) *Service {
	s.landedPayables = r
	return s
}

// PayBillRequest records a part or full supplier payment against a posted bill.
type PayBillRequest struct {
	AmountCents    int64     `json:"amount_cents"`
	FXRate         float64   `json:"fx_rate,omitempty"`
	PaidAt         time.Time `json:"paid_at"`
	Note           string    `json:"note"`
	IdempotencyKey string    `json:"idempotency_key"`
}

// APAgingItem is one open AP obligation (either an open vendor bill or an open
// landed-cost payable) as of the requested instant.
type APAgingItem struct {
	ID               string `json:"id"`
	Kind             string `json:"kind"`
	IsLandedCost     bool   `json:"is_landed_cost"`
	RefNo            string `json:"ref_no"`
	BillNo           string `json:"bill_no,omitempty"`
	POID             string `json:"po_id,omitempty"`
	PONo             string `json:"po_no,omitempty"`
	PORef            string `json:"po_ref,omitempty"`
	BranchID         string `json:"branch_id"`
	VendorID         string `json:"vendor_id,omitempty"`
	VendorName       string `json:"vendor_name,omitempty"`
	ChargeKind       string `json:"charge_kind,omitempty"`
	BillDate         string `json:"bill_date"`
	DueDate          string `json:"due_date"`
	TotalCents       int64  `json:"total_cents"`
	PaidCents        int64  `json:"paid_cents"`
	OpenCents        int64  `json:"open_cents"`
	FreightCadCents  int64  `json:"freight_cad_cents,omitempty"`
	DutyCadCents     int64  `json:"duty_cad_cents,omitempty"`
	HandlingCadCents int64  `json:"handling_cad_cents,omitempty"`
	Bucket           string `json:"bucket"`
}

// APAgingRow is one row of the AP aging table: either a vendor with its open
// bills or an open landed-cost payable owed to its carrier or broker.
type APAgingRow struct {
	RowKind          string        `json:"row_kind"`
	IsLandedCost     bool          `json:"is_landed_cost"`
	VendorID         string        `json:"vendor_id,omitempty"`
	VendorNo         string        `json:"vendor_no,omitempty"`
	VendorName       string        `json:"vendor_name"`
	LandedCostID     string        `json:"landed_cost_id,omitempty"`
	POID             string        `json:"po_id,omitempty"`
	PORef            string        `json:"po_ref,omitempty"`
	BranchID         string        `json:"branch_id,omitempty"`
	ChargeKind       string        `json:"charge_kind,omitempty"`
	DueDate          string        `json:"due_date,omitempty"`
	PaymentTermsDays int           `json:"payment_terms_days"`
	FreightCadCents  int64         `json:"freight_cad_cents,omitempty"`
	DutyCadCents     int64         `json:"duty_cad_cents,omitempty"`
	HandlingCadCents int64         `json:"handling_cad_cents,omitempty"`
	CurrentCents     int64         `json:"current_cents"`
	Days30Cents      int64         `json:"days_30_cents"`
	Days60Cents      int64         `json:"days_60_cents"`
	Days60To90Cents  int64         `json:"days_60_to_90_cents"`
	Days90PlusCents  int64         `json:"days_90_plus_cents"`
	TotalCents       int64         `json:"total_cents"`
	OpenItems        []APAgingItem `json:"open_items"`
}

// APAgingPage is the AP aging response as of an instant in the dealer's house zone.
type APAgingPage struct {
	AsOf            string       `json:"as_of"`
	CurrentCents    int64        `json:"current_cents"`
	Days30Cents     int64        `json:"days_30_cents"`
	Days60Cents     int64        `json:"days_60_cents"`
	Days60To90Cents int64        `json:"days_60_to_90_cents"`
	Days90PlusCents int64        `json:"days_90_plus_cents"`
	TotalCents      int64        `json:"total_cents"`
	Items           []APAgingRow `json:"items"`
	NextCursor      *string      `json:"next_cursor"`
}

type apBillCandidate struct {
	BillID           string
	BillNo           string
	VendorID         string
	VendorNo         string
	VendorName       string
	PaymentTermsDays int
	BranchID         string
	POID             string
	PONo             string
	BillDate         string
	DueDate          string
	TotalCents       int64
	GLJournalID      string
	// PaidAts and PaidAmounts are the bill's PAID instalments, aligned by index. The
	// as-of sum is the caller's to draw: which instalments count is the payment
	// journals' effective instants, not the rows' own paid_at.
	PaidAts     []time.Time
	PaidAmounts []int64
	PaidCents   int64
}

// PayBill records a supplier payment against a posted vendor bill and posts
// the AP settlement journal.
func (s *Service) PayBill(ctx context.Context, billID string, in PayBillRequest) (BillDetail, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return BillDetail{}, err
	}
	key := strings.TrimSpace(in.IdempotencyKey)
	if key == "" {
		return BillDetail{}, badReq("purchasing: idempotency_key is required on a bill payment (one key per press)")
	}
	if in.AmountCents <= 0 {
		return BillDetail{}, badReq("purchasing: payment amount_cents must be positive")
	}
	zone, err := houseday.Resolve(ctx, s.zone)
	if err != nil {
		return BillDetail{}, err
	}
	paidAt := in.PaidAt
	if paidAt.IsZero() {
		if s.now != nil {
			paidAt = s.now().In(zone.Location())
		} else {
			paidAt = time.Now().In(zone.Location())
		}
	}

	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return BillDetail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// One key is one payment across every bill: two presses under the same key
	// serialize here, so the second sees the first's journal and is judged a
	// replay or a key spent elsewhere, never a second payment (S2 round 2 P2).
	if err := s.repo.LockPaymentKeyTx(ctx, tx, glPaymentKeyPrefix+key); err != nil {
		return BillDetail{}, err
	}

	bill, err := s.repo.BillForUpdate(ctx, tx, billID)
	if err != nil {
		return BillDetail{}, err
	}
	if !caller.CanAccessBranch(bill.BranchID) {
		return BillDetail{}, errNotFound
	}

	// The replay judgement runs before any money moves: a re-press under the first
	// press's key returns the first payment and writes nothing. The key lives on the
	// journal, so a key spent on another bill is a conflict, not a replay. The read
	// goes through the GL journal clock seam, never a GL table. With the clock
	// unwired the judgement cannot run and the payment proceeds, the behavior the
	// seam replaced.
	if s.journalClock != nil {
		journalRef, found, err := s.journalClock.JournalSourceRefByKey(ctx, glPaymentKeyPrefix+key)
		if err != nil {
			return BillDetail{}, err
		}
		if found {
			if journalRef != bill.ID {
				return BillDetail{}, conflict("purchasing: idempotency key already used for another bill's payment")
			}
			return s.GetBill(ctx, billID)
		}
	}

	if bill.Status != BillPosted {
		return BillDetail{}, conflict("purchasing: bill %s is %s; only POSTED bills can be paid", bill.BillNo, bill.Status)
	}

	isUSD := strings.EqualFold(strings.TrimSpace(bill.Currency), "USD")
	payFX := 1.0
	if isUSD {
		if math.IsNaN(in.FXRate) || math.IsInf(in.FXRate, 0) || in.FXRate <= 0 {
			return BillDetail{}, badReq("purchasing: fx_rate is required and must be positive when paying a USD bill")
		}
		payFX = in.FXRate
	} else if math.IsNaN(in.FXRate) || math.IsInf(in.FXRate, 0) || in.FXRate < 0 || (in.FXRate != 0 && in.FXRate != 1) {
		return BillDetail{}, badReq("purchasing: CAD bill payment must not carry a non-unit fx_rate (got %v)", in.FXRate)
	}

	clearedCadSoFar, paidDocSoFar, err := s.repo.PaidBillTotalsTx(ctx, tx, bill.ID)
	if err != nil {
		return BillDetail{}, err
	}

	billDocTotal := bill.TotalCents
	if isUSD {
		if bill.DocTotalCents > 0 {
			billDocTotal = bill.DocTotalCents
		} else if bill.FXRate > 0 {
			billRateMicros := int64(math.Round(bill.FXRate * 1_000_000))
			billDocTotal = proRataHalfToEven(bill.TotalCents, 1_000_000, billRateMicros)
		}
	}

	paidSoFar := clearedCadSoFar
	if isUSD {
		paidSoFar = paidDocSoFar
	}
	if paidSoFar+in.AmountCents > billDocTotal {
		return BillDetail{}, conflict("purchasing: payment of %d cents exceeds open balance %d cents on bill %s",
			in.AmountCents, billDocTotal-paidSoFar, bill.BillNo)
	}

	apCadCents := in.AmountCents
	cashCadCents := in.AmountCents
	if isUSD {
		if paidDocSoFar+in.AmountCents == billDocTotal {
			apCadCents = bill.TotalCents - clearedCadSoFar
		} else {
			apCadCents = proRataHalfToEven(bill.TotalCents, in.AmountCents, billDocTotal)
		}
		payRateMicros := int64(math.Round(payFX * 1_000_000))
		cashCadCents = proRataHalfToEven(in.AmountCents, payRateMicros, 1_000_000)
	}
	fxDiffCents := cashCadCents - apCadCents

	dueDate := strings.TrimSpace(bill.DueDate)
	if dueDate == "" {
		dueDate = bill.BillDate
	}
	if _, err := s.repo.RecordBillPaymentTx(ctx, tx, bill.ID, dueDate, apCadCents, in.AmountCents, payFX, paidAt, strings.TrimSpace(in.Note)); err != nil {
		return BillDetail{}, err
	}

	if s.gl != nil {
		apCode := glAccountAPCode
		if s.rules != nil {
			if resolved, rErr := s.rules.ResolveRuleTx(ctx, tx, eventBillPosted, roleAP, bill.BranchID); rErr == nil && strings.TrimSpace(resolved) != "" {
				apCode = resolved
			}
		}
		lines := []GLLine{
			{AccountCode: apCode, DebitCents: apCadCents, Memo: "AP payment " + bill.BillNo},
			{AccountCode: glAccountCash, CreditCents: cashCadCents, Memo: "Supplier payment " + bill.BillNo},
		}
		if fxDiffCents != 0 {
			fxCode := glAccountFXGainLoss
			if s.rules != nil {
				if resolved, rErr := s.rules.ResolveRuleTx(ctx, tx, eventBillPosted, roleFXGainLoss, bill.BranchID); rErr == nil && strings.TrimSpace(resolved) != "" {
					fxCode = resolved
				}
			}
			if fxDiffCents > 0 {
				lines = append(lines, GLLine{AccountCode: fxCode, DebitCents: fxDiffCents, Memo: "Realised FX loss " + bill.BillNo})
			} else {
				lines = append(lines, GLLine{AccountCode: fxCode, CreditCents: -fxDiffCents, Memo: "Realised FX gain " + bill.BillNo})
			}
		}
		_, err = s.gl.PostJournalTx(ctx, tx, GLJournal{
			Source:         glSourcePayment,
			SourceRefID:    bill.ID,
			IdempotencyKey: glPaymentKeyPrefix + key,
			Memo:           "Supplier payment " + bill.BillNo,
			BranchID:       bill.BranchID,
			DocumentDate:   paidAt,
			Lines:          lines,
		})
		if err != nil {
			return BillDetail{}, fmt.Errorf("purchasing: post supplier payment journal: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return BillDetail{}, fmt.Errorf("purchasing: commit supplier payment: %w", err)
	}
	return s.GetBill(ctx, billID)
}

// proRataHalfToEven computes (totalCents * partCents) / wholeCents rounded half
// to even in cents using exact integer arithmetic.
func proRataHalfToEven(totalCents, partCents, wholeCents int64) int64 {
	if wholeCents <= 0 || totalCents == 0 || partCents == 0 {
		return 0
	}
	num := new(big.Int).Mul(big.NewInt(totalCents), big.NewInt(partCents))
	den := big.NewInt(wholeCents)
	quo, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	twoRem := new(big.Int).Lsh(rem, 1)
	cmp := twoRem.Cmp(den)
	q := quo.Int64()
	if cmp > 0 || (cmp == 0 && q%2 != 0) {
		q++
	}
	return q
}

// LockPaymentKeyTx holds a transaction scoped advisory lock on a payment's
// idempotency key, so concurrent presses under one key run one at a time. It
// touches no table.
func (r *Repository) LockPaymentKeyTx(ctx context.Context, tx pgx.Tx, key string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
		return fmt.Errorf("purchasing: lock the payment key: %w", err)
	}
	return nil
}

// PaidBillTotalsTx returns the sum of PAID instalments on billID within tx:
// clearedCadCents is the sum of amount_cents (the 2100 CAD cleared so far) and
// paidDocCents is the sum of doc_amount_cents (the paid amount in the bill's currency).
func (r *Repository) PaidBillTotalsTx(ctx context.Context, tx pgx.Tx, billID string) (clearedCadCents, paidDocCents int64, err error) {
	if r.pool == nil {
		return 0, 0, errNoDB
	}
	if err := r.q(tx).QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_cents), 0),
		       COALESCE(SUM(COALESCE(NULLIF(doc_amount_cents, 0), amount_cents)), 0)
		  FROM purchasing_bill_instalments
		 WHERE bill_id = $1 AND status = 'PAID'`,
		billID,
	).Scan(&clearedCadCents, &paidDocCents); err != nil {
		return 0, 0, fmt.Errorf("purchasing: sum paid instalments: %w", err)
	}
	return clearedCadCents, paidDocCents, nil
}

// PaidBillCentsTx returns the sum of PAID instalments on billID within tx.
func (r *Repository) PaidBillCentsTx(ctx context.Context, tx pgx.Tx, billID string) (int64, error) {
	cleared, _, err := r.PaidBillTotalsTx(ctx, tx, billID)
	return cleared, err
}

// RecordBillPaymentTx inserts a PAID instalment row for a supplier payment against billID.
func (r *Repository) RecordBillPaymentTx(ctx context.Context, tx pgx.Tx, billID, dueDate string, amountCadCents, docAmountCents int64, fxRate float64, paidAt time.Time, note string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var nextSeq int
	if err := r.q(tx).QueryRow(ctx,
		`SELECT COALESCE(MAX(seq), 0) + 1 FROM purchasing_bill_instalments WHERE bill_id = $1`,
		billID,
	).Scan(&nextSeq); err != nil {
		return "", fmt.Errorf("purchasing: next instalment seq: %w", err)
	}
	var id string
	if err := r.q(tx).QueryRow(ctx, `
		INSERT INTO purchasing_bill_instalments (bill_id, seq, due_date, amount_cents, doc_amount_cents, fx_rate, status, paid_at, note)
		VALUES ($1, $2, $3::date, $4, $5, $6, 'PAID', $7, $8)
		RETURNING id::text`,
		billID, nextSeq, dueDate, amountCadCents, docAmountCents, fxRate, paidAt, note,
	).Scan(&id); err != nil {
		return "", fmt.Errorf("purchasing: insert paid instalment: %w", err)
	}
	return id, nil
}

// APAging computes the open AP aging as of an instant in the dealer's house zone.
// The aging IS the subledger: it grows only by posted vendor bills and open
// landed-cost payables, so a 2100 credit that is neither (a MANUAL or ADJUSTMENT
// accrual, another module's posting) shows as a tie difference against GL 2100's
// net credit balance at the same instant, never as an aging row (FROM-PRINCIPAL
// 122, 124, 125). A document counts from its posting journal's effective instant,
// the GL's own late filing rule, and still ages by its document date.
func (s *Service) APAging(ctx context.Context, asOf time.Time, reach BranchReach) (APAgingPage, error) {
	zone, err := houseday.Resolve(ctx, s.zone)
	if err != nil {
		return APAgingPage{}, err
	}
	loc := zone.Location()
	if asOf.IsZero() {
		if s.now != nil {
			asOf = s.now()
		} else {
			asOf = time.Now()
		}
	}
	effectiveAsOf := apAsOfEffectiveInstant(asOf, loc)
	asOfDay := effectiveAsOf.In(loc).Format("2006-01-02")

	page := APAgingPage{
		AsOf:  asOfDay,
		Items: []APAgingRow{},
	}
	if !reach.Global && len(reach.BranchIDs) == 0 {
		return page, nil
	}

	candidates, err := s.repo.OpenBillsAsOf(ctx, reach)
	if err != nil {
		return APAgingPage{}, err
	}
	candidates, err = s.applyJournalClock(ctx, candidates, effectiveAsOf, loc)
	if err != nil {
		return APAgingPage{}, err
	}

	vendorIndex := make(map[string]int)
	for _, c := range candidates {
		openCents := c.TotalCents - c.PaidCents
		if openCents <= 0 {
			continue
		}
		bucket := bucketForDueDate(asOfDay, c.DueDate)
		item := APAgingItem{
			ID:           c.BillID,
			Kind:         APItemKindBill,
			IsLandedCost: false,
			RefNo:        c.BillNo,
			BillNo:       c.BillNo,
			POID:         c.POID,
			PONo:         c.PONo,
			BranchID:     c.BranchID,
			VendorID:     c.VendorID,
			VendorName:   c.VendorName,
			BillDate:     c.BillDate,
			DueDate:      c.DueDate,
			TotalCents:   c.TotalCents,
			PaidCents:    c.PaidCents,
			OpenCents:    openCents,
			Bucket:       bucket,
		}

		idx, exists := vendorIndex[c.VendorID]
		if !exists {
			idx = len(page.Items)
			vendorIndex[c.VendorID] = idx
			page.Items = append(page.Items, APAgingRow{
				RowKind:          APRowKindVendor,
				IsLandedCost:     false,
				VendorID:         c.VendorID,
				VendorNo:         c.VendorNo,
				VendorName:       c.VendorName,
				BranchID:         c.BranchID,
				DueDate:          c.DueDate,
				PaymentTermsDays: c.PaymentTermsDays,
				OpenItems:        []APAgingItem{},
			})
		}
		row := &page.Items[idx]
		if row.DueDate == "" || c.DueDate < row.DueDate {
			row.DueDate = c.DueDate
		}
		row.OpenItems = append(row.OpenItems, item)
		addBucketCents(row, &page, bucket, openCents)
	}

	if s.landedPayables != nil {
		payables, err := s.landedPayables.OpenPayablesAsOf(ctx, effectiveAsOf, reach.BranchIDs, reach.Global)
		if err != nil {
			return APAgingPage{}, err
		}
		for _, lc := range payables {
			if lc.TotalCadCents <= 0 {
				continue
			}
			lcDay := lc.CreatedAt.In(loc).Format("2006-01-02")
			bucket := bucketForDueDate(asOfDay, lcDay)
			label := strings.TrimSpace(lc.PayeeLabel)
			if label == "" {
				label = fmt.Sprintf("Freight Carrier (%s)", lc.PORef)
			}
			chargeKind := strings.TrimSpace(lc.ChargeKind)
			if chargeKind == "" {
				chargeKind = "FREIGHT"
			}
			item := APAgingItem{
				ID:               lc.ID,
				Kind:             APItemKindLandedCost,
				IsLandedCost:     true,
				RefNo:            lc.PORef,
				POID:             lc.POID,
				PORef:            lc.PORef,
				BranchID:         lc.BranchID,
				VendorName:       label,
				ChargeKind:       chargeKind,
				BillDate:         lcDay,
				DueDate:          lcDay,
				TotalCents:       lc.TotalCadCents,
				PaidCents:        0,
				OpenCents:        lc.TotalCadCents,
				FreightCadCents:  lc.FreightCadCents,
				DutyCadCents:     lc.DutyCadCents,
				HandlingCadCents: lc.HandlingCadCents,
				Bucket:           bucket,
			}
			row := APAgingRow{
				RowKind:          APRowKindLandedCost,
				IsLandedCost:     true,
				VendorID:         lc.ID,
				VendorName:       label,
				LandedCostID:     lc.ID,
				POID:             lc.POID,
				PORef:            lc.PORef,
				BranchID:         lc.BranchID,
				ChargeKind:       chargeKind,
				DueDate:          lcDay,
				FreightCadCents:  lc.FreightCadCents,
				DutyCadCents:     lc.DutyCadCents,
				HandlingCadCents: lc.HandlingCadCents,
				OpenItems:        []APAgingItem{item},
			}
			addBucketCents(&row, &page, bucket, lc.TotalCadCents)
			page.Items = append(page.Items, row)
		}
	}

	return page, nil
}

// APBalanceAsOf returns the total open AP in cents as of asOf from the single
// APAging rule, for global reach when branchID is empty or for the named branch.
func (s *Service) APBalanceAsOf(ctx context.Context, asOf time.Time, branchID string) (int64, error) {
	var reach BranchReach
	if strings.TrimSpace(branchID) == "" {
		reach = BranchReach{Global: true}
	} else {
		reach = BranchReach{BranchIDs: []string{strings.TrimSpace(branchID)}}
	}
	page, err := s.APAging(ctx, asOf, reach)
	if err != nil {
		return 0, err
	}
	return page.TotalCents, nil
}

// APAgingForCaller evaluates the open AP aging for the authenticated caller,
// enforcing branch reach and parsing rawAsOf (empty -> now; RFC3339 instant;
// or YYYY-MM-DD in the dealer's house zone).
func (s *Service) APAgingForCaller(ctx context.Context, rawAsOf, branchID string) (APAgingPage, error) {
	reach, err := s.reachFor(ctx, branchID)
	if err != nil {
		return APAgingPage{}, err
	}
	zone, err := houseday.Resolve(ctx, s.zone)
	if err != nil {
		return APAgingPage{}, err
	}
	asOf, err := parseAPAsOfParam(rawAsOf, zone.Location())
	if err != nil {
		return APAgingPage{}, err
	}
	return s.APAging(ctx, asOf, reach)
}

func parseAPAsOfParam(raw string, loc *time.Location) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	if loc == nil {
		loc = time.UTC
	}
	if d, err := time.ParseInLocation("2006-01-02", raw, loc); err == nil && d.Format("2006-01-02") == raw {
		return time.Date(d.Year(), d.Month(), d.Day(), 23, 59, 59, int(time.Second-time.Nanosecond), loc), nil
	}
	return time.Time{}, badReq("as_of must be an RFC3339 instant or YYYY-MM-DD")
}

// OpenBillsAsOf reads posted vendor bills in reach with their PAID instalments. The
// query carries no as-of predicate of its own: which bills and which instalments
// count as of an instant is the posting journals' effective instants, the caller's
// GLJournalClock step.
func (r *Repository) OpenBillsAsOf(ctx context.Context, reach BranchReach) ([]apBillCandidate, error) {
	if r == nil || r.pool == nil {
		return nil, errNoDB
	}
	if !reach.Global && len(reach.BranchIDs) == 0 {
		return []apBillCandidate{}, nil
	}
	branchIDs := reach.BranchIDs
	if branchIDs == nil {
		branchIDs = []string{}
	}

	rows, err := r.pool.Query(ctx, `
		SELECT b.id::text, b.bill_no, b.vendor_id::text,
		       v.vendor_no::text, v.name, v.payment_terms_days,
		       b.branch_id::text, COALESCE(b.po_id::text, ''), COALESCE(p.po_no::text, ''),
		       to_char(b.bill_date, 'YYYY-MM-DD'),
		       to_char(COALESCE(b.due_date, b.bill_date), 'YYYY-MM-DD'),
		       b.total_cents,
		       COALESCE(b.gl_journal_id::text, ''),
		       COALESCE((SELECT array_agg(i.paid_at ORDER BY i.paid_at, i.id)
		           FROM purchasing_bill_instalments i
		          WHERE i.bill_id = b.id AND i.status = 'PAID'), '{}'),
		       COALESCE((SELECT array_agg(i.amount_cents ORDER BY i.paid_at, i.id)
		           FROM purchasing_bill_instalments i
		          WHERE i.bill_id = b.id AND i.status = 'PAID'), '{}')
		  FROM purchasing_bills b
		  JOIN vendors v ON v.id = b.vendor_id
		  LEFT JOIN purchasing_pos p ON p.id = b.po_id
		 WHERE b.status = 'POSTED'
		   AND ($1::boolean OR b.branch_id = ANY($2::uuid[]))
		 ORDER BY v.name ASC, v.id ASC, COALESCE(b.due_date, b.bill_date) ASC, b.bill_date ASC, b.bill_no ASC, b.id ASC`,
		reach.Global, branchIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("purchasing: open bills as of: %w", err)
	}
	defer rows.Close()

	out := make([]apBillCandidate, 0)
	for rows.Next() {
		var c apBillCandidate
		if err := rows.Scan(
			&c.BillID, &c.BillNo, &c.VendorID,
			&c.VendorNo, &c.VendorName, &c.PaymentTermsDays,
			&c.BranchID, &c.POID, &c.PONo,
			&c.BillDate, &c.DueDate,
			&c.TotalCents, &c.GLJournalID, &c.PaidAts, &c.PaidAmounts,
		); err != nil {
			return nil, fmt.Errorf("purchasing: scan open bill as of: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("purchasing: open bills rows: %w", err)
	}
	return out, nil
}

// applyJournalClock narrows the candidates to what counts as of the instant under the
// GL's own filing rule (S2's P2 1, FROM-PRINCIPAL 124): a bill counts from its posting
// journal's effective instant and a payment from its payment journal's, the same late
// filing rule the GL's account reads keep, so a bill filed late into a later open month
// breaks the tie in neither book until that month ends. The bill still AGES by its due
// date; only the inclusion instant moved. Each instalment pairs with its payment
// journal by the instant (PayBill stamps the journal's document date and the
// instalment's paid_at with the same instant); a PAID instalment with no journal on
// the books counts at its own paid_at, the legacy rule. With the clock unwired the
// document's own dates keep the rule the seam replaced.
func (s *Service) applyJournalClock(ctx context.Context, candidates []apBillCandidate, asOf time.Time, loc *time.Location) ([]apBillCandidate, error) {
	if s.journalClock == nil {
		day := asOf.In(loc).Format("2006-01-02")
		out := make([]apBillCandidate, 0, len(candidates))
		for _, c := range candidates {
			if c.BillDate > day {
				continue
			}
			var paid int64
			for i, at := range c.PaidAts {
				if !at.After(asOf) {
					paid += c.PaidAmounts[i]
				}
			}
			c.PaidCents = paid
			out = append(out, c)
		}
		return out, nil
	}

	journalIDs := make([]string, 0, len(candidates))
	billIDs := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if c.GLJournalID != "" {
			journalIDs = append(journalIDs, c.GLJournalID)
		}
		billIDs = append(billIDs, c.BillID)
	}
	billEff, err := s.journalClock.JournalEffectiveInstants(ctx, journalIDs)
	if err != nil {
		return nil, err
	}
	entries, err := s.journalClock.RefJournalInstants(ctx, glSourcePayment, billIDs)
	if err != nil {
		return nil, err
	}
	payByRef := make(map[string][]GLRefJournalInstant, len(entries))
	for _, e := range entries {
		payByRef[e.RefID] = append(payByRef[e.RefID], e)
	}

	out := make([]apBillCandidate, 0, len(candidates))
	for _, c := range candidates {
		if c.GLJournalID != "" {
			eff, ok := billEff[c.GLJournalID]
			if !ok || eff.After(asOf) {
				continue
			}
		} else if c.BillDate > asOf.In(loc).Format("2006-01-02") {
			// no posting journal on the row (the GL was unwired at post time): the
			// document's own date decides, the legacy rule
			continue
		}
		payments := payByRef[c.BillID]
		used := make([]bool, len(payments))
		var paid int64
		for i, at := range c.PaidAts {
			instant := at
			for j := range payments {
				if used[j] {
					continue
				}
				if payments[j].PostedAt.Truncate(time.Microsecond).Equal(at.Truncate(time.Microsecond)) {
					used[j] = true
					instant = payments[j].EffectiveAt
					break
				}
			}
			if !instant.After(asOf) {
				paid += c.PaidAmounts[i]
			}
		}
		c.PaidCents = paid
		out = append(out, c)
	}
	return out, nil
}

func apAsOfEffectiveInstant(asOf time.Time, loc *time.Location) time.Time {
	if asOf.IsZero() {
		return asOf
	}
	if loc == nil {
		loc = time.UTC
	}
	local := asOf.In(loc)
	if local.Hour() == 0 && local.Minute() == 0 && local.Second() == 0 && local.Nanosecond() == 0 {
		return time.Date(local.Year(), local.Month(), local.Day(), 23, 59, 59, int(time.Second-time.Nanosecond), loc)
	}
	return asOf
}

func bucketForDueDate(asOfDay, dueDate string) string {
	asOfT, err1 := time.Parse("2006-01-02", asOfDay)
	dueT, err2 := time.Parse("2006-01-02", dueDate)
	if err1 != nil || err2 != nil {
		return APBucketCurrent
	}
	switch {
	case !asOfT.After(dueT):
		return APBucketCurrent
	case !asOfT.After(dueT.AddDate(0, 0, 30)):
		return APBucketDays30
	case !asOfT.After(dueT.AddDate(0, 0, 60)):
		return APBucketDays60
	case !asOfT.After(dueT.AddDate(0, 0, 90)):
		return APBucketDays6090
	default:
		return APBucketDays90P
	}
}

func addBucketCents(row *APAgingRow, page *APAgingPage, bucket string, cents int64) {
	switch bucket {
	case APBucketCurrent:
		row.CurrentCents += cents
		page.CurrentCents += cents
	case APBucketDays30:
		row.Days30Cents += cents
		page.Days30Cents += cents
	case APBucketDays60:
		row.Days60Cents += cents
		page.Days60Cents += cents
	case APBucketDays6090:
		row.Days60To90Cents += cents
		page.Days60To90Cents += cents
	case APBucketDays90P:
		row.Days90PlusCents += cents
		page.Days90PlusCents += cents
	}
	row.TotalCents += cents
	page.TotalCents += cents
}
