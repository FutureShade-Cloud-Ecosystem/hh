package purchasing

import (
	"context"
	"fmt"
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

	glSourcePayment = "PAYMENT"
	glAccountCash   = "1000"
	glAccountAPCode = "2100"
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

// SupplierPayment is an external supplier payment journal posted to GL 2100.
type SupplierPayment struct {
	JournalID      string
	SourceRefID    string
	IdempotencyKey string
	BranchID       string
	Memo           string
	AmountCents    int64
	PostedAt       time.Time
}

// SupplierPaymentReader reads external supplier payment journals on GL 2100
// that were posted outside purchasing's own bill-instalment payment path.
type SupplierPaymentReader interface {
	ExternalSupplierPaymentsAsOf(ctx context.Context, asOf time.Time, branchIDs []string, global bool) ([]SupplierPayment, error)
}

// WithLandedCostPayables binds the landed-cost payable reader seam.
func (s *Service) WithLandedCostPayables(r LandedCostPayableReader) *Service {
	s.landedPayables = r
	return s
}

// WithSupplierPayments binds the external supplier payment reader seam.
func (s *Service) WithSupplierPayments(r SupplierPaymentReader) *Service {
	s.supplierPayments = r
	return s
}

// PayBillRequest records a part or full supplier payment against a posted bill.
type PayBillRequest struct {
	AmountCents int64     `json:"amount_cents"`
	PaidAt      time.Time `json:"paid_at"`
	Note        string    `json:"note"`
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
	PaidCents        int64
}

// PayBill records a supplier payment against a posted vendor bill and posts
// the AP settlement journal.
func (s *Service) PayBill(ctx context.Context, billID string, in PayBillRequest) (BillDetail, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return BillDetail{}, err
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

	bill, err := s.repo.BillForUpdate(ctx, tx, billID)
	if err != nil {
		return BillDetail{}, err
	}
	if !caller.CanAccessBranch(bill.BranchID) {
		return BillDetail{}, errNotFound
	}
	if bill.Status != BillPosted {
		return BillDetail{}, conflict("purchasing: bill %s is %s; only POSTED bills can be paid", bill.BillNo, bill.Status)
	}

	paidSoFar, err := s.repo.PaidBillCentsTx(ctx, tx, bill.ID)
	if err != nil {
		return BillDetail{}, err
	}
	if paidSoFar+in.AmountCents > bill.TotalCents {
		return BillDetail{}, conflict("purchasing: payment of %d cents exceeds open balance %d cents on bill %s",
			in.AmountCents, bill.TotalCents-paidSoFar, bill.BillNo)
	}

	dueDate := strings.TrimSpace(bill.DueDate)
	if dueDate == "" {
		dueDate = bill.BillDate
	}
	instID, err := s.repo.RecordBillPaymentTx(ctx, tx, bill.ID, dueDate, in.AmountCents, paidAt, strings.TrimSpace(in.Note))
	if err != nil {
		return BillDetail{}, err
	}

	if s.gl != nil {
		apCode := glAccountAPCode
		if s.rules != nil {
			if resolved, rErr := s.rules.ResolveRuleTx(ctx, tx, eventBillPosted, roleAP, bill.BranchID); rErr == nil && strings.TrimSpace(resolved) != "" {
				apCode = resolved
			}
		}
		_, err = s.gl.PostJournalTx(ctx, tx, GLJournal{
			Source:         glSourcePayment,
			SourceRefID:    bill.ID,
			IdempotencyKey: "purchasing.bill.payment:" + instID,
			Memo:           "Supplier payment " + bill.BillNo,
			BranchID:       bill.BranchID,
			DocumentDate:   paidAt,
			Lines: []GLLine{
				{AccountCode: apCode, DebitCents: in.AmountCents, Memo: "AP payment " + bill.BillNo},
				{AccountCode: glAccountCash, CreditCents: in.AmountCents, Memo: "Supplier payment " + bill.BillNo},
			},
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

// PaidBillCentsTx returns the sum of PAID instalments on billID within tx.
func (r *Repository) PaidBillCentsTx(ctx context.Context, tx pgx.Tx, billID string) (int64, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	var sum int64
	if err := r.q(tx).QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_cents), 0)
		  FROM purchasing_bill_instalments
		 WHERE bill_id = $1 AND status = 'PAID'`,
		billID,
	).Scan(&sum); err != nil {
		return 0, fmt.Errorf("purchasing: sum paid instalments: %w", err)
	}
	return sum, nil
}

// RecordBillPaymentTx inserts a PAID instalment row for a supplier payment against billID.
func (r *Repository) RecordBillPaymentTx(ctx context.Context, tx pgx.Tx, billID, dueDate string, amountCents int64, paidAt time.Time, note string) (string, error) {
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
		INSERT INTO purchasing_bill_instalments (bill_id, seq, due_date, amount_cents, status, paid_at, note)
		VALUES ($1, $2, $3::date, $4, 'PAID', $5, $6)
		RETURNING id::text`,
		billID, nextSeq, dueDate, amountCents, paidAt, nullStr(note),
	).Scan(&id); err != nil {
		return "", fmt.Errorf("purchasing: insert paid instalment: %w", err)
	}
	return id, nil
}

// APAging computes the open AP aging as of an instant in the dealer's house zone.
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

	candidates, err := s.repo.OpenBillsAsOf(ctx, effectiveAsOf, zone.Name(), reach)
	if err != nil {
		return APAgingPage{}, err
	}

	if s.supplierPayments != nil && len(candidates) > 0 {
		extPayments, err := s.supplierPayments.ExternalSupplierPaymentsAsOf(ctx, effectiveAsOf, reach.BranchIDs, reach.Global)
		if err != nil {
			return APAgingPage{}, err
		}
		applyExternalSupplierPayments(candidates, extPayments)
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

// OpenBillsAsOf reads posted vendor bills and their paid instalment sums as of asOf.
func (r *Repository) OpenBillsAsOf(ctx context.Context, asOf time.Time, zone string, reach BranchReach) ([]apBillCandidate, error) {
	if r == nil || r.pool == nil {
		return nil, errNoDB
	}
	if !reach.Global && len(reach.BranchIDs) == 0 {
		return []apBillCandidate{}, nil
	}
	if strings.TrimSpace(zone) == "" {
		zone = houseday.DefaultZone().Name()
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
		       COALESCE((
		           SELECT SUM(i.amount_cents)
		             FROM purchasing_bill_instalments i
		            WHERE i.bill_id = b.id
		              AND i.status = 'PAID'
		              AND (i.paid_at IS NULL OR i.paid_at <= $1)
		       ), 0) AS paid_cents
		  FROM purchasing_bills b
		  JOIN vendors v ON v.id = b.vendor_id
		  LEFT JOIN purchasing_pos p ON p.id = b.po_id
		 WHERE b.status = 'POSTED'
		   AND b.bill_date <= ($1 AT TIME ZONE $2)::date
		   AND ($3::boolean OR b.branch_id = ANY($4::uuid[]))
		 ORDER BY v.name ASC, v.id ASC, COALESCE(b.due_date, b.bill_date) ASC, b.bill_date ASC, b.bill_no ASC, b.id ASC`,
		asOf, zone, reach.Global, branchIDs,
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
			&c.TotalCents, &c.PaidCents,
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

func applyExternalSupplierPayments(candidates []apBillCandidate, payments []SupplierPayment) {
	for _, p := range payments {
		rem := p.AmountCents
		if rem <= 0 {
			continue
		}
		// Pass 1: exact match by source_ref_id, finworld:vpay:<bill_no>, or bill_no in memo.
		for i := range candidates {
			if rem <= 0 {
				break
			}
			c := &candidates[i]
			open := c.TotalCents - c.PaidCents
			if open <= 0 {
				continue
			}
			matched := (p.SourceRefID != "" && p.SourceRefID == c.BillID) ||
				(p.IdempotencyKey != "" && p.IdempotencyKey == "finworld:vpay:"+c.BillNo) ||
				(c.BillNo != "" && strings.Contains(p.Memo, c.BillNo))
			if !matched {
				continue
			}
			apply := rem
			if apply > open {
				apply = open
			}
			c.PaidCents += apply
			rem -= apply
		}
		if rem <= 0 {
			continue
		}
		// Pass 2: FIFO across oldest open bills in the same branch (or any branch if payment has no branch).
		bestOrder := make([]int, 0, len(candidates))
		for i := range candidates {
			if p.BranchID != "" && candidates[i].BranchID != p.BranchID {
				continue
			}
			if candidates[i].TotalCents-candidates[i].PaidCents <= 0 {
				continue
			}
			bestOrder = append(bestOrder, i)
		}
		for _, idx := range bestOrder {
			if rem <= 0 {
				break
			}
			c := &candidates[idx]
			open := c.TotalCents - c.PaidCents
			if open <= 0 {
				continue
			}
			apply := rem
			if apply > open {
				apply = open
			}
			c.PaidCents += apply
			rem -= apply
		}
	}
}
