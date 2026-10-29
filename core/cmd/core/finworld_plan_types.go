//go:build finworld

package main

import "fmt"

// YearMonth names a calendar year and month (1..12) in the house zone.
type YearMonth struct {
	Year  int `json:"year"`
	Month int `json:"month"`
}

// String formats YearMonth as YYYY-MM.
func (ym YearMonth) String() string {
	return fmt.Sprintf("%04d-%02d", ym.Year, ym.Month)
}

// Valid reports whether YearMonth has a sensible year and a month in 1..12.
func (ym YearMonth) Valid() bool {
	return ym.Year >= 1970 && ym.Year <= 9999 && ym.Month >= 1 && ym.Month <= 12
}

// AddMonths returns the YearMonth offset by delta calendar months.
func (ym YearMonth) AddMonths(delta int) YearMonth {
	total := ym.Year*12 + (ym.Month - 1) + delta
	y := total / 12
	m := total%12 + 1
	if m <= 0 {
		m += 12
		y--
	}
	return YearMonth{Year: y, Month: m}
}

// Compare returns -1 if ym < other, 0 if ym == other, and +1 if ym > other.
func (ym YearMonth) Compare(other YearMonth) int {
	if ym.Year != other.Year {
		if ym.Year < other.Year {
			return -1
		}
		return 1
	}
	if ym.Month != other.Month {
		if ym.Month < other.Month {
			return -1
		}
		return 1
	}
	return 0
}

// FinWorldSeasonBand names the seasonal activity tier of a business month.
type FinWorldSeasonBand string

const (
	FinWorldSeasonPeak     FinWorldSeasonBand = "PEAK"
	FinWorldSeasonShoulder FinWorldSeasonBand = "SHOULDER"
	FinWorldSeasonWinter   FinWorldSeasonBand = "WINTER"
)

// Seasonality basis points (10000 = 1.00, 5500 = 0.55, 2000 = 0.20).
const (
	FinWorldSeasonPeakBps     = 10000
	FinWorldSeasonShoulderBps = 5500
	FinWorldSeasonWinterBps   = 2000
)

// Branch volume ratios in basis points relative to Trenton (TRT = 10000, KGN = 5000).
const (
	FinWorldBranchCodeTRT  = "TRT"
	FinWorldBranchCodeKGN  = "KGN"
	FinWorldBranchIDTRT    = "5d1b3f6e-0000-4000-9000-000000000001"
	FinWorldBranchIDKGN    = "5d1b3f6e-0000-4000-9000-000000000002"
	FinWorldBranchRatioTRT = 10000
	FinWorldBranchRatioKGN = 5000
)

// Tax reason codes and basis point rates matching core/internal/tax. The rates are the tax engine's own
// tax_rates rows (core/db/migrations/0042_tax_engine.sql): the engine reads those rows live at every
// assessment, so the plan carries the same numbers.
const (
	FinWorldTaxReasonStandardON = "STD_ON_HST_13"
	FinWorldTaxReasonReliefFN8  = "ON_FN_POS_8"
	// FinWorldTaxRateStandardBps is the tax_rates row for STD_ON_HST_13: the Ontario standard HST.
	FinWorldTaxRateStandardBps = 1300
	// FinWorldTaxRateReliefBps is the tax_rates row for ON_FN_POS_8: the 5% net the engine collects at the
	// point of sale once the relief evidence is attached.
	FinWorldTaxRateReliefBps = 500
	// FinWorldTaxOntarioCreditBps is the 8% Ontario credit the engine tracks alongside the 5% net on the
	// ON_FN_POS_8 path (core/internal/tax/model.go, ontarioCredBps). It is a credit, not a rate row.
	FinWorldTaxOntarioCreditBps = 800
	FinWorldTaxRoundBase        = 10000
)

// FinWorldEventKind identifies the business action represented by a FinWorldEvent.
type FinWorldEventKind string

const (
	FinWorldEventTillOpen              FinWorldEventKind = "TILL_OPEN"
	FinWorldEventTillPaidInOut         FinWorldEventKind = "TILL_PAID_IN_OUT"
	FinWorldEventTillCount             FinWorldEventKind = "TILL_COUNT"
	FinWorldEventTillComplete          FinWorldEventKind = "TILL_COMPLETE"
	FinWorldEventCounterSale           FinWorldEventKind = "COUNTER_SALE"
	FinWorldEventAccountSale           FinWorldEventKind = "ACCOUNT_SALE"
	FinWorldEventDeliveredSale         FinWorldEventKind = "DELIVERED_SALE"
	FinWorldEventCollectLaterOrCODSale FinWorldEventKind = "COLLECT_LATER_OR_COD_SALE"
	FinWorldEventReturn                FinWorldEventKind = "RETURN"
	FinWorldEventCreditMemo            FinWorldEventKind = "CREDIT_MEMO"
	FinWorldEventAccountReceipt        FinWorldEventKind = "ACCOUNT_RECEIPT"
	FinWorldEventNSFBounce             FinWorldEventKind = "NSF_BOUNCE"
	FinWorldEventLodgement             FinWorldEventKind = "LODGEMENT"
	FinWorldEventCardSettlement        FinWorldEventKind = "CARD_SETTLEMENT"
	FinWorldEventPurchaseReceipt       FinWorldEventKind = "PURCHASE_RECEIPT"
	FinWorldEventVendorBill            FinWorldEventKind = "VENDOR_BILL"
	FinWorldEventVendorPayment         FinWorldEventKind = "VENDOR_PAYMENT"
	FinWorldEventLandedCost            FinWorldEventKind = "LANDED_COST"
	FinWorldEventLandedCostSettle      FinWorldEventKind = "LANDED_COST_SETTLE"
	FinWorldEventStockTransfer         FinWorldEventKind = "STOCK_TRANSFER"
	FinWorldEventManualJournal         FinWorldEventKind = "MANUAL_JOURNAL"
	FinWorldEventBankStatement         FinWorldEventKind = "BANK_STATEMENT"
	FinWorldEventPeriodClose           FinWorldEventKind = "PERIOD_CLOSE"
	FinWorldEventYearClose             FinWorldEventKind = "YEAR_CLOSE"
)

// FinWorldPlanT holds the deterministic 12-month business plan for a seed and anchor month.
type FinWorldPlanT struct {
	Seed                  uint64                        `json:"seed"`
	Anchor                YearMonth                     `json:"anchor"`
	StartMonth            YearMonth                     `json:"start_month"`
	FiscalYearEndMonth    YearMonth                     `json:"fiscal_year_end_month"`
	HouseZone             string                        `json:"house_zone"`
	StatutoryHolidaysNote string                        `json:"statutory_holidays_note"`
	TaxRoundingNote       string                        `json:"tax_rounding_note"`
	CatalogueSourceNote   string                        `json:"catalogue_source_note"`
	Dealer                FinWorldDealer                `json:"dealer"`
	Branches              []FinWorldBranch              `json:"branches"`
	Contractors           []FinWorldCustomer            `json:"contractors"`
	Homeowners            []FinWorldCustomer            `json:"homeowners"`
	Vendors               []FinWorldVendor              `json:"vendors"`
	Products              []FinWorldProduct             `json:"products"`
	Months                []FinWorldMonthPlan           `json:"months"`
	IntendedLatePayables  []FinWorldIntendedLatePayable `json:"intended_late_payables"`
	Totals                FinWorldPlanSummary           `json:"totals"`
}

// FinWorldVendor is one vendor the world's books name: a goods supplier of the
// catalogue, or (W2-1d) the freight carrier or customs broker a landed cost
// names as its payee. The display name is what the aging and the PO screens
// show, so it carries no vendor number and no counter.
type FinWorldVendor struct {
	VendorNo         string `json:"vendor_no"`
	Name             string `json:"name"`
	Kind             string `json:"kind"` // "SUPPLIER", "CARRIER" or "BROKER"
	PaymentTermsDays int    `json:"payment_terms_days"`
}

// FinWorldIntendedLatePayable names one carrier or broker payable the plan
// leaves open past its terms on purpose, with the bookkeeper's reason a
// Controller would read on the aging. At most a handful are intended; the
// world census fails on any payable past 90 days late the plan did not name
// here.
type FinWorldIntendedLatePayable struct {
	LandedCostKey string `json:"landed_cost_key"`
	Reason        string `json:"reason"`
}

// FinWorldDealer describes the invented Quinte-area building supply dealer.
type FinWorldDealer struct {
	Key           string `json:"key"`
	Name          string `json:"name"`
	LegalName     string `json:"legal_name"`
	StreetAddress string `json:"street_address"`
	Town          string `json:"town"`
	Province      string `json:"province"`
	PostalCode    string `json:"postal_code"`
	Phone         string `json:"phone"`
	SupportEmail  string `json:"support_email"`
}

// FinWorldBranch describes one of the two seeded branches (Trenton and Kingston).
type FinWorldBranch struct {
	ID             string   `json:"id"`
	Code           string   `json:"code"`
	Name           string   `json:"name"`
	StreetAddress  string   `json:"street_address"`
	Town           string   `json:"town"`
	Province       string   `json:"province"`
	PostalCode     string   `json:"postal_code"`
	VolumeRatioBps int      `json:"volume_ratio_bps"`
	TillKeys       []string `json:"till_keys"`
}

// FinWorldCustomer describes an invented contractor (70) or homeowner (180).
type FinWorldCustomer struct {
	Key               string `json:"key"`
	AccountNo         string `json:"account_no"`
	CustomerType      string `json:"customer_type"`
	IsTopPareto       bool   `json:"is_top_pareto"`
	Name              string `json:"name"`
	ContactName       string `json:"contact_name"`
	Email             string `json:"email"`
	Phone             string `json:"phone"`
	StreetAddress     string `json:"street_address"`
	Town              string `json:"town"`
	Province          string `json:"province"`
	PostalCode        string `json:"postal_code"`
	HomeBranchCode    string `json:"home_branch_code"`
	CreditLimitCents  int64  `json:"credit_limit_cents"`
	PaymentTermsDays  int    `json:"payment_terms_days"`
	TaxReliefEligible bool   `json:"tax_relief_eligible"`
}

// FinWorldProduct describes one of the 30 DBS- catalogue products and its plan weights.
type FinWorldProduct struct {
	ProductID            string `json:"product_id"`
	SKU                  string `json:"sku"`
	Name                 string `json:"name"`
	CategoryCode         string `json:"category_code"`
	BaseUOM              string `json:"base_uom"`
	SellUOM              string `json:"sell_uom"`
	VendorNo             string `json:"vendor_no"`
	ListPriceCents       int64  `json:"list_price_cents"`
	ContractorPriceCents int64  `json:"contractor_price_cents"`
	StandardCostCents    int64  `json:"standard_cost_cents"`
	SelectionWeight      int    `json:"selection_weight"`
	OpeningStockTRT      int64  `json:"opening_stock_trt"`
	OpeningStockKGN      int64  `json:"opening_stock_kgn"`
	ReorderPointTRT      int64  `json:"reorder_point_trt"`
	ReorderPointKGN      int64  `json:"reorder_point_kgn"`
	ReorderQtyTRT        int64  `json:"reorder_qty_trt"`
	ReorderQtyKGN        int64  `json:"reorder_qty_kgn"`
}

// FinWorldMonthPlan holds one business month of trading days and its document summary.
type FinWorldMonthPlan struct {
	MonthIndex      int                  `json:"month_index"`
	YearMonth       YearMonth            `json:"year_month"`
	SeasonBand      FinWorldSeasonBand   `json:"season_band"`
	SeasonFactorBps int                  `json:"season_factor_bps"`
	IsClosed        bool                 `json:"is_closed"`
	IsFiscalYearEnd bool                 `json:"is_fiscal_year_end"`
	FirstTradingDay string               `json:"first_trading_day"`
	LastTradingDay  string               `json:"last_trading_day"`
	TradingDays     []FinWorldTradingDay `json:"trading_days"`
	Summary         FinWorldMonthSummary `json:"summary"`
}

// FinWorldTradingDay holds the ordered list of business events for a single house trading day.
type FinWorldTradingDay struct {
	Date            string          `json:"date"`
	DayOfMonth      int             `json:"day_of_month"`
	Weekday         string          `json:"weekday"`
	TradingDayIndex int             `json:"trading_day_index"`
	IsLastOfMonth   bool            `json:"is_last_of_month"`
	Events          []FinWorldEvent `json:"events"`
}

// FinWorldEvent is a single ordered business event on a trading day.
type FinWorldEvent struct {
	GlobalSeq        int                            `json:"global_seq"`
	DaySeq           int                            `json:"day_seq"`
	EventKey         string                         `json:"event_key"`
	Date             string                         `json:"date"`
	OccurredAt       string                         `json:"occurred_at"`
	BranchCode       string                         `json:"branch_code,omitempty"`
	Kind             FinWorldEventKind              `json:"kind"`
	TillOpen         *FinWorldTillOpenEvent         `json:"till_open,omitempty"`
	TillPaidInOut    *FinWorldTillPaidInOutEvent    `json:"till_paid_in_out,omitempty"`
	TillCount        *FinWorldTillCountEvent        `json:"till_count,omitempty"`
	TillComplete     *FinWorldTillCompleteEvent     `json:"till_complete,omitempty"`
	Sale             *FinWorldSaleEvent             `json:"sale,omitempty"`
	Return           *FinWorldReturnEvent           `json:"return,omitempty"`
	CreditMemo       *FinWorldCreditMemoEvent       `json:"credit_memo,omitempty"`
	AccountReceipt   *FinWorldAccountReceiptEvent   `json:"account_receipt,omitempty"`
	NSFBounce        *FinWorldNSFBounceEvent        `json:"nsf_bounce,omitempty"`
	Lodgement        *FinWorldLodgementEvent        `json:"lodgement,omitempty"`
	CardSettlement   *FinWorldCardSettlementEvent   `json:"card_settlement,omitempty"`
	PurchaseReceipt  *FinWorldPurchaseReceiptEvent  `json:"purchase_receipt,omitempty"`
	VendorBill       *FinWorldVendorBillEvent       `json:"vendor_bill,omitempty"`
	VendorPayment    *FinWorldVendorPaymentEvent    `json:"vendor_payment,omitempty"`
	LandedCost       *FinWorldLandedCostEvent       `json:"landed_cost,omitempty"`
	LandedCostSettle *FinWorldLandedCostSettleEvent `json:"landed_cost_settle,omitempty"`
	StockTransfer    *FinWorldStockTransferEvent    `json:"stock_transfer,omitempty"`
	ManualJournal    *FinWorldManualJournalEvent    `json:"manual_journal,omitempty"`
	BankStatement    *FinWorldBankStatementEvent    `json:"bank_statement,omitempty"`
	PeriodClose      *FinWorldPeriodCloseEvent      `json:"period_close,omitempty"`
	YearClose        *FinWorldYearCloseEvent        `json:"year_close,omitempty"`
}

// FinWorldTillOpenEvent opens a till session at the start of a trading day.
type FinWorldTillOpenEvent struct {
	SessionKey        string `json:"session_key"`
	TillKey           string `json:"till_key"`
	BranchCode        string `json:"branch_code"`
	OpeningFloatCents int64  `json:"opening_float_cents"`
}

// FinWorldTillPaidInOutEvent records a cash paid-in or paid-out movement in an open till session.
type FinWorldTillPaidInOutEvent struct {
	MovementKey string `json:"movement_key"`
	SessionKey  string `json:"session_key"`
	TillKey     string `json:"till_key"`
	BranchCode  string `json:"branch_code"`
	Direction   string `json:"direction"` // "PAID_IN" or "PAID_OUT"
	AmountCents int64  `json:"amount_cents"`
	Reason      string `json:"reason"`
}

// FinWorldTillCountEvent records the end-of-day cash count and any over/short variance.
type FinWorldTillCountEvent struct {
	SessionKey        string `json:"session_key"`
	TillKey           string `json:"till_key"`
	BranchCode        string `json:"branch_code"`
	ExpectedCashCents int64  `json:"expected_cash_cents"`
	CountedCashCents  int64  `json:"counted_cash_cents"`
	VarianceCents     int64  `json:"variance_cents"`
	HasVariance       bool   `json:"has_variance"`
}

// FinWorldTillCompleteEvent completes and locks a counted till session.
type FinWorldTillCompleteEvent struct {
	SessionKey       string `json:"session_key"`
	TillKey          string `json:"till_key"`
	BranchCode       string `json:"branch_code"`
	ClosingCashCents int64  `json:"closing_cash_cents"`
	LodgedCashCents  int64  `json:"lodged_cash_cents"`
}

// FinWorldSaleLine is one product line on a sale event.
type FinWorldSaleLine struct {
	LineNo                 int    `json:"line_no"`
	LineKey                string `json:"line_key"`
	SKU                    string `json:"sku"`
	Description            string `json:"description"`
	UOM                    string `json:"uom"`
	Quantity               int64  `json:"quantity"`
	UnitPriceCents         int64  `json:"unit_price_cents"`
	UnitCostCents          int64  `json:"unit_cost_cents"`
	LineSubtotalCents      int64  `json:"line_subtotal_cents"`
	TaxReasonCode          string `json:"tax_reason_code"`
	TaxRateBps             int    `json:"tax_rate_bps"`
	TaxCents               int64  `json:"tax_cents"`
	OntarioCreditCents     int64  `json:"ontario_credit_cents"`
	NetTaxAfterCreditCents int64  `json:"net_tax_after_credit_cents"`
	LineTotalCents         int64  `json:"line_total_cents"`
}

// FinWorldSaleEvent represents a counter sale, account sale, delivered sale, or collect-later/COD sale.
type FinWorldSaleEvent struct {
	SaleKey                string             `json:"sale_key"`
	OrderKey               string             `json:"order_key"`
	InvoiceKey             string             `json:"invoice_key,omitempty"`
	PaymentKey             string             `json:"payment_key,omitempty"`
	SessionKey             string             `json:"session_key,omitempty"`
	BranchCode             string             `json:"branch_code"`
	CustomerKey            string             `json:"customer_key"`
	CustomerType           string             `json:"customer_type"`
	IsTopParetoContractor  bool               `json:"is_top_pareto_contractor"`
	SaleMode               string             `json:"sale_mode"`        // "COUNTER", "ACCOUNT", "DELIVERED", "COLLECT_LATER", "COD"
	FulfillmentType        string             `json:"fulfillment_type"` // "COLLECT_NOW", "DELIVERED", "COLLECT_LATER", "COD"
	PaymentMethod          string             `json:"payment_method"`   // "CASH", "CARD", "ACCOUNT", "CHEQUE"
	OnAccount              bool               `json:"on_account"`
	IssueDate              string             `json:"issue_date"`
	DueDate                string             `json:"due_date,omitempty"`
	TaxReasonCode          string             `json:"tax_reason_code"`
	TaxRateBps             int                `json:"tax_rate_bps"`
	Lines                  []FinWorldSaleLine `json:"lines"`
	SubtotalCents          int64              `json:"subtotal_cents"`
	TaxCents               int64              `json:"tax_cents"`
	OntarioCreditCents     int64              `json:"ontario_credit_cents"`
	NetTaxAfterCreditCents int64              `json:"net_tax_after_credit_cents"`
	TotalCents             int64              `json:"total_cents"`
	AmountPaidCents        int64              `json:"amount_paid_cents"`
	InitialBalanceCents    int64              `json:"initial_balance_cents"`
}

// FinWorldReturnLine is one returned product line referencing an earlier sale line.
type FinWorldReturnLine struct {
	LineNo            int    `json:"line_no"`
	OriginalLineNo    int    `json:"original_line_no"`
	OriginalLineKey   string `json:"original_line_key"`
	SKU               string `json:"sku"`
	UOM               string `json:"uom"`
	Quantity          int64  `json:"quantity"`
	UnitPriceCents    int64  `json:"unit_price_cents"`
	UnitCostCents     int64  `json:"unit_cost_cents"`
	LineSubtotalCents int64  `json:"line_subtotal_cents"`
	TaxReasonCode     string `json:"tax_reason_code"`
	TaxRateBps        int    `json:"tax_rate_bps"`
	TaxCents          int64  `json:"tax_cents"`
	LineTotalCents    int64  `json:"line_total_cents"`
}

// FinWorldReturnEvent represents a customer return referencing an earlier sale in the plan.
type FinWorldReturnEvent struct {
	ReturnKey          string               `json:"return_key"`
	OriginalSaleKey    string               `json:"original_sale_key"`
	OriginalInvoiceKey string               `json:"original_invoice_key,omitempty"`
	OriginalSaleDate   string               `json:"original_sale_date"`
	BranchCode         string               `json:"branch_code"`
	CustomerKey        string               `json:"customer_key"`
	SessionKey         string               `json:"session_key,omitempty"`
	RefundMethod       string               `json:"refund_method"` // "CASH", "CARD", "ACCOUNT_CREDIT"
	TaxReasonCode      string               `json:"tax_reason_code"`
	TaxRateBps         int                  `json:"tax_rate_bps"`
	Lines              []FinWorldReturnLine `json:"lines"`
	SubtotalCents      int64                `json:"subtotal_cents"`
	TaxCents           int64                `json:"tax_cents"`
	TotalCents         int64                `json:"total_cents"`
}

// FinWorldCreditMemoEvent represents a standalone credit memo issued against an earlier account invoice.
type FinWorldCreditMemoEvent struct {
	CreditMemoKey      string `json:"credit_memo_key"`
	OriginalSaleKey    string `json:"original_sale_key"`
	OriginalInvoiceKey string `json:"original_invoice_key"`
	BranchCode         string `json:"branch_code"`
	CustomerKey        string `json:"customer_key"`
	ReasonCode         string `json:"reason_code"`
	Description        string `json:"description"`
	TaxReasonCode      string `json:"tax_reason_code"`
	TaxRateBps         int    `json:"tax_rate_bps"`
	SubtotalCents      int64  `json:"subtotal_cents"`
	TaxCents           int64  `json:"tax_cents"`
	TotalCents         int64  `json:"total_cents"`
}

// FinWorldReceiptAllocation applies part or all of an account receipt to an earlier invoice key.
type FinWorldReceiptAllocation struct {
	InvoiceKey     string `json:"invoice_key"`
	SaleKey        string `json:"sale_key"`
	InvoiceDate    string `json:"invoice_date"`
	AllocatedCents int64  `json:"allocated_cents"`
}

// FinWorldAccountReceiptEvent records a payment received from an account customer against earlier invoices.
type FinWorldAccountReceiptEvent struct {
	ReceiptKey     string                      `json:"receipt_key"`
	PaymentKey     string                      `json:"payment_key"`
	BranchCode     string                      `json:"branch_code"`
	CustomerKey    string                      `json:"customer_key"`
	PaymentMethod  string                      `json:"payment_method"` // "CHEQUE", "EFT", "CARD"
	AmountCents    int64                       `json:"amount_cents"`
	Allocations    []FinWorldReceiptAllocation `json:"allocations"`
	UnappliedCents int64                       `json:"unapplied_cents"`
}

// FinWorldNSFBounceEvent reverses an earlier cheque account receipt and reopens its invoices.
type FinWorldNSFBounceEvent struct {
	BounceKey           string   `json:"bounce_key"`
	OriginalReceiptKey  string   `json:"original_receipt_key"`
	OriginalPaymentKey  string   `json:"original_payment_key"`
	OriginalReceiptDate string   `json:"original_receipt_date"`
	BranchCode          string   `json:"branch_code"`
	CustomerKey         string   `json:"customer_key"`
	BouncedAmountCents  int64    `json:"bounced_amount_cents"`
	NSFFeeCents         int64    `json:"nsf_fee_cents"`
	ReopenedInvoiceKeys []string `json:"reopened_invoice_keys"`
}

// FinWorldLodgementEvent deposits the day's branch cash take and cheque receipts to the bank.
type FinWorldLodgementEvent struct {
	LodgementKey      string   `json:"lodgement_key"`
	BranchCode        string   `json:"branch_code"`
	SessionKeys       []string `json:"session_keys"`
	ChequeReceiptKeys []string `json:"cheque_receipt_keys,omitempty"`
	CashAmountCents   int64    `json:"cash_amount_cents"`
	ChequeAmountCents int64    `json:"cheque_amount_cents"`
	TotalAmountCents  int64    `json:"total_amount_cents"`
	BankDepositRef    string   `json:"bank_deposit_ref"`
}

// FinWorldCardSettlementEvent settles the day's branch card transactions.
type FinWorldCardSettlementEvent struct {
	SettlementKey     string   `json:"settlement_key"`
	BranchCode        string   `json:"branch_code"`
	SaleKeys          []string `json:"sale_keys,omitempty"`
	ReturnKeys        []string `json:"return_keys,omitempty"`
	ReceiptKeys       []string `json:"receipt_keys,omitempty"`
	GrossCardCents    int64    `json:"gross_card_cents"`
	RefundCardCents   int64    `json:"refund_card_cents"`
	ProcessorFeeCents int64    `json:"processor_fee_cents"`
	NetSettledCents   int64    `json:"net_settled_cents"`
	StatementRef      string   `json:"statement_ref"`
}

// FinWorldPurchaseLine is one product line on a purchase receipt or vendor bill.
type FinWorldPurchaseLine struct {
	LineNo            int    `json:"line_no"`
	SKU               string `json:"sku"`
	UOM               string `json:"uom"`
	Quantity          int64  `json:"quantity"`
	UnitCostCents     int64  `json:"unit_cost_cents"`
	LineSubtotalCents int64  `json:"line_subtotal_cents"`
	TaxRateBps        int    `json:"tax_rate_bps"`
	TaxCents          int64  `json:"tax_cents"`
	LineTotalCents    int64  `json:"line_total_cents"`
	StockBeforeBase   int64  `json:"stock_before_base"`
	ReorderPointBase  int64  `json:"reorder_point_base"`
	TriggeredReorder  bool   `json:"triggered_reorder"`
}

// FinWorldPurchaseReceiptEvent receives stock from a vendor into a branch yard.
type FinWorldPurchaseReceiptEvent struct {
	PurchaseReceiptKey string                 `json:"purchase_receipt_key"`
	PurchaseOrderKey   string                 `json:"purchase_order_key"`
	BranchCode         string                 `json:"branch_code"`
	VendorNo           string                 `json:"vendor_no"`
	TriggeredByReorder bool                   `json:"triggered_by_reorder"`
	Lines              []FinWorldPurchaseLine `json:"lines"`
	SubtotalCents      int64                  `json:"subtotal_cents"`
	TaxCents           int64                  `json:"tax_cents"`
	TotalCents         int64                  `json:"total_cents"`
}

// FinWorldVendorBillEvent posts the supplier invoice matching an earlier purchase receipt.
type FinWorldVendorBillEvent struct {
	VendorBillKey      string `json:"vendor_bill_key"`
	PurchaseReceiptKey string `json:"purchase_receipt_key"`
	PurchaseOrderKey   string `json:"purchase_order_key"`
	BranchCode         string `json:"branch_code"`
	VendorNo           string `json:"vendor_no"`
	VendorInvoiceNo    string `json:"vendor_invoice_no"`
	IssueDate          string `json:"issue_date"`
	DueDate            string `json:"due_date"`
	// PaymentDeferred marks a bill the plan leaves open across a month end (net 30 terms): the
	// driver posts the bill and holds the supplier cheque, and the plan settles it with a
	// VENDOR_PAYMENT event on the next month's first trading day, the day the statement carries
	// the cheque. Left open when there is no next month.
	PaymentDeferred bool                   `json:"payment_deferred,omitempty"`
	Lines           []FinWorldPurchaseLine `json:"lines"`
	SubtotalCents   int64                  `json:"subtotal_cents"`
	TaxCents        int64                  `json:"tax_cents"`
	TotalCents      int64                  `json:"total_cents"`
}

// FinWorldVendorPaymentEvent settles a bill the plan left open across a month end: the supplier
// cheque for the named bill clears on the day the event sits on, the same day the bank statement
// carries its clearing line and the open AP drops by the amount.
type FinWorldVendorPaymentEvent struct {
	VendorPaymentKey string `json:"vendor_payment_key"`
	VendorBillKey    string `json:"vendor_bill_key"`
	VendorInvoiceNo  string `json:"vendor_invoice_no"`
	BranchCode       string `json:"branch_code"`
	AmountCents      int64  `json:"amount_cents"`
}

// FinWorldLandedCostEvent mirrors the landed-cost module's own shape (core/internal/landedcost):
// the purchase receipt it attaches to, the charge (freight or duty) in integer cents, the document
// currency and the allocation basis, so the driver can post it through the module's API without
// translation. A POSTED event is a landed cost that was created and applied; a VOIDED event
// reverses the landed cost named by VoidOfLandedCostKey, the way the module's Void reverses an
// applied document. The module's statuses and bases are spelled exactly as the module spells them.
type FinWorldLandedCostEvent struct {
	LandedCostKey      string `json:"landed_cost_key"`
	PurchaseReceiptKey string `json:"purchase_receipt_key"`
	PurchaseOrderKey   string `json:"purchase_order_key"`
	BranchCode         string `json:"branch_code"`
	VendorNo           string `json:"vendor_no"`
	// PayeeVendorNo names the vendor the charge is owed to: a freight carrier
	// for a FREIGHT charge, a customs broker for a DUTY one. The driver posts
	// the create route with that vendor's id, so the AP aging labels the
	// payable with the carrier's or broker's own name.
	PayeeVendorNo      string `json:"payee_vendor_no,omitempty"`
	Currency           string `json:"currency"`
	FXRateBps          int    `json:"fx_rate_bps"`
	ChargeKind         string `json:"charge_kind"` // "FREIGHT" or "DUTY"
	ChargeCents        int64  `json:"charge_cents"`
	ChargeCadCents     int64  `json:"charge_cad_cents"`
	FreightCents       int64  `json:"freight_cents"`
	DutyCents          int64  `json:"duty_cents"`
	HandlingCents      int64  `json:"handling_cents"`
	AllocationBasis    string `json:"allocation_basis"`
	ExcludeSpecialBuys bool   `json:"exclude_special_buys"`
	Description        string `json:"description"`
	AppliedDate        string `json:"applied_date"`
	Status             string `json:"status"`
	// SettledOnDate is the trading day the plan settles the payable with its
	// payee vendor, on the vendor's terms. Empty leaves the payable open; a
	// payable still open more than 90 days past its due date must be named in
	// the plan's IntendedLatePayables, or the world census fails.
	SettledOnDate       string `json:"settled_on_date,omitempty"`
	VoidOfLandedCostKey string `json:"void_of_landed_cost_key,omitempty"`
	VoidedDate          string `json:"voided_date,omitempty"`
	VoidReason          string `json:"void_reason,omitempty"`
}

// FinWorldLandedCostSettleEvent settles a landed cost's payable with its payee
// vendor on the vendor's terms: the cheque clears on the day the event sits
// on, the same day the bank statement carries it and the payable leaves the
// AP aging.
type FinWorldLandedCostSettleEvent struct {
	LandedCostKey string `json:"landed_cost_key"`
	PayeeVendorNo string `json:"payee_vendor_no"`
	BranchCode    string `json:"branch_code"`
	// PayableCadCents is the whole payable the settle route relieves: the
	// freight, the duty and the handling the document carries, the same figure
	// the module's settle journal credits the bank by.
	PayableCadCents int64  `json:"payable_cad_cents"`
	SettledDate     string `json:"settled_date"`
}

// FinWorldTransferLine is one product line on an inter-branch stock transfer.
type FinWorldTransferLine struct {
	LineNo         int    `json:"line_no"`
	SKU            string `json:"sku"`
	UOM            string `json:"uom"`
	Quantity       int64  `json:"quantity"`
	UnitCostCents  int64  `json:"unit_cost_cents"`
	LineTotalCents int64  `json:"line_total_cents"`
}

// FinWorldStockTransferEvent moves stock between Trenton and Kingston.
type FinWorldStockTransferEvent struct {
	TransferKey    string                 `json:"transfer_key"`
	FromBranchCode string                 `json:"from_branch_code"`
	ToBranchCode   string                 `json:"to_branch_code"`
	Lines          []FinWorldTransferLine `json:"lines"`
	TotalCostCents int64                  `json:"total_cost_cents"`
}

// FinWorldJournalLine is one balanced debit or credit line on a manual journal.
type FinWorldJournalLine struct {
	LineNo      int    `json:"line_no"`
	AccountCode string `json:"account_code"`
	AccountName string `json:"account_name"`
	DebitCents  int64  `json:"debit_cents"`
	CreditCents int64  `json:"credit_cents"`
	Memo        string `json:"memo"`
}

// FinWorldManualJournalEvent posts a manual accrual or adjustment journal flagged for review.
type FinWorldManualJournalEvent struct {
	JournalKey       string                `json:"journal_key"`
	BranchCode       string                `json:"branch_code"`
	Description      string                `json:"description"`
	RequiresReview   bool                  `json:"requires_review"`
	ReviewStatus     string                `json:"review_status"`
	Lines            []FinWorldJournalLine `json:"lines"`
	TotalDebitCents  int64                 `json:"total_debit_cents"`
	TotalCreditCents int64                 `json:"total_credit_cents"`
}

// FinWorldBankStatementLine is one line on a monthly bank statement import.
type FinWorldBankStatementLine struct {
	LineNo         int    `json:"line_no"`
	LineKey        string `json:"line_key"`
	ValueDate      string `json:"value_date"`
	Kind           string `json:"kind"` // "LODGEMENT", "CARD_SETTLEMENT", "EFT_RECEIPT", "VENDOR_PAYMENT", "BANK_FEE", "NSF_RETURN"
	SourceEventKey string `json:"source_event_key,omitempty"`
	Reference      string `json:"reference"`
	Description    string `json:"description"`
	AmountCents    int64  `json:"amount_cents"` // positive for inflows, negative for outflows
}

// FinWorldBankStatementEvent imports a month's bank statement on the month's last trading day.
type FinWorldBankStatementEvent struct {
	StatementKey          string                      `json:"statement_key"`
	YearMonth             YearMonth                   `json:"year_month"`
	StatementDate         string                      `json:"statement_date"`
	OpeningBalanceCents   int64                       `json:"opening_balance_cents"`
	ClosingBalanceCents   int64                       `json:"closing_balance_cents"`
	BankFeeCents          int64                       `json:"bank_fee_cents"`
	Lines                 []FinWorldBankStatementLine `json:"lines"`
	DepositsInTransitKeys []string                    `json:"deposits_in_transit_keys"`
	OutstandingChequeKeys []string                    `json:"outstanding_cheque_keys"`
}

// FinWorldPeriodCloseEvent closes a fiscal month after its last trading day.
type FinWorldPeriodCloseEvent struct {
	CloseKey       string    `json:"close_key"`
	YearMonth      YearMonth `json:"year_month"`
	FiscalYear     int       `json:"fiscal_year"`
	FiscalPeriod   int       `json:"fiscal_period"`
	LastTradingDay string    `json:"last_trading_day"`
}

// FinWorldYearCloseEvent closes the fiscal year after the period close of month 12.
type FinWorldYearCloseEvent struct {
	YearCloseKey   string    `json:"year_close_key"`
	FiscalYear     int       `json:"fiscal_year"`
	YearEndMonth   YearMonth `json:"year_end_month"`
	PeriodCloseKey string    `json:"period_close_key"`
	LastTradingDay string    `json:"last_trading_day"`
}

// FinWorldBranchSummary aggregates document counts and sales cents for one branch in a month.
type FinWorldBranchSummary struct {
	BranchCode               string `json:"branch_code"`
	CounterSales             int    `json:"counter_sales"`
	AccountSales             int    `json:"account_sales"`
	DeliveredSales           int    `json:"delivered_sales"`
	CollectLaterOrCODSales   int    `json:"collect_later_or_cod_sales"`
	TotalSalesDocs           int    `json:"total_sales_docs"`
	TotalSalesLines          int    `json:"total_sales_lines"`
	ReturnDocs               int    `json:"return_docs"`
	ReturnLines              int    `json:"return_lines"`
	CreditMemos              int    `json:"credit_memos"`
	AccountReceipts          int    `json:"account_receipts"`
	NSFBounces               int    `json:"nsf_bounces"`
	TillOpens                int    `json:"till_opens"`
	TillPaidInOuts           int    `json:"till_paid_in_outs"`
	TillCounts               int    `json:"till_counts"`
	TillCompletes            int    `json:"till_completes"`
	TillOverShortCount       int    `json:"till_over_short_count"`
	Lodgements               int    `json:"lodgements"`
	CardSettlements          int    `json:"card_settlements"`
	PurchaseReceipts         int    `json:"purchase_receipts"`
	ReorderTriggeredReceipts int    `json:"reorder_triggered_receipts"`
	VendorBills              int    `json:"vendor_bills"`
	LandedCosts              int    `json:"landed_costs"`
	LandedCostVoids          int    `json:"landed_cost_voids"`
	StockTransfers           int    `json:"stock_transfers"`
	ManualJournals           int    `json:"manual_journals"`
	SalesSubtotalCents       int64  `json:"sales_subtotal_cents"`
	SalesTaxCents            int64  `json:"sales_tax_cents"`
	SalesTotalCents          int64  `json:"sales_total_cents"`
	ReturnsTotalCents        int64  `json:"returns_total_cents"`
	CreditMemosTotalCents    int64  `json:"credit_memos_total_cents"`
	ReceiptsTotalCents       int64  `json:"receipts_total_cents"`
	LandedCostChargesCents   int64  `json:"landed_cost_charges_cents"`
	LandedCostVoidCents      int64  `json:"landed_cost_void_cents"`
}

// FinWorldMonthSummary aggregates document counts and cents across both branches for one month.
type FinWorldMonthSummary struct {
	YearMonth              YearMonth                        `json:"year_month"`
	SeasonBand             FinWorldSeasonBand               `json:"season_band"`
	SeasonFactorBps        int                              `json:"season_factor_bps"`
	TradingDayCount        int                              `json:"trading_day_count"`
	CounterSales           int                              `json:"counter_sales"`
	AccountSales           int                              `json:"account_sales"`
	DeliveredSales         int                              `json:"delivered_sales"`
	CollectLaterOrCODSales int                              `json:"collect_later_or_cod_sales"`
	TotalSalesDocs         int                              `json:"total_sales_docs"`
	TotalSalesLines        int                              `json:"total_sales_lines"`
	ReturnDocs             int                              `json:"return_docs"`
	ReturnLines            int                              `json:"return_lines"`
	CreditMemos            int                              `json:"credit_memos"`
	AccountReceipts        int                              `json:"account_receipts"`
	NSFBounces             int                              `json:"nsf_bounces"`
	TillOpens              int                              `json:"till_opens"`
	TillPaidInOuts         int                              `json:"till_paid_in_outs"`
	TillCounts             int                              `json:"till_counts"`
	TillCompletes          int                              `json:"till_completes"`
	TillOverShortCount     int                              `json:"till_over_short_count"`
	Lodgements             int                              `json:"lodgements"`
	CardSettlements        int                              `json:"card_settlements"`
	PurchaseReceipts       int                              `json:"purchase_receipts"`
	VendorBills            int                              `json:"vendor_bills"`
	LandedCosts            int                              `json:"landed_costs"`
	LandedCostVoids        int                              `json:"landed_cost_voids"`
	StockTransfers         int                              `json:"stock_transfers"`
	ManualJournals         int                              `json:"manual_journals"`
	BankStatements         int                              `json:"bank_statements"`
	PeriodCloses           int                              `json:"period_closes"`
	YearCloses             int                              `json:"year_closes"`
	SalesSubtotalCents     int64                            `json:"sales_subtotal_cents"`
	SalesTaxCents          int64                            `json:"sales_tax_cents"`
	SalesTotalCents        int64                            `json:"sales_total_cents"`
	ReturnsTotalCents      int64                            `json:"returns_total_cents"`
	CreditMemosTotalCents  int64                            `json:"credit_memos_total_cents"`
	ReceiptsTotalCents     int64                            `json:"receipts_total_cents"`
	LandedCostChargesCents int64                            `json:"landed_cost_charges_cents"`
	LandedCostVoidCents    int64                            `json:"landed_cost_void_cents"`
	ByBranch               map[string]FinWorldBranchSummary `json:"by_branch"`
}

// FinWorldPlanSummary holds 12-month grand totals and aging/Pareto metrics for the plan.
type FinWorldPlanSummary struct {
	TotalTradingDays            int   `json:"total_trading_days"`
	TotalEvents                 int   `json:"total_events"`
	CounterSales                int   `json:"counter_sales"`
	AccountSales                int   `json:"account_sales"`
	DeliveredSales              int   `json:"delivered_sales"`
	CollectLaterOrCODSales      int   `json:"collect_later_or_cod_sales"`
	TotalSalesDocs              int   `json:"total_sales_docs"`
	TotalSalesLines             int   `json:"total_sales_lines"`
	TaxReliefSales              int   `json:"tax_relief_sales"`
	ReturnDocs                  int   `json:"return_docs"`
	ReturnLines                 int   `json:"return_lines"`
	CreditMemos                 int   `json:"credit_memos"`
	AccountReceipts             int   `json:"account_receipts"`
	NSFBounces                  int   `json:"nsf_bounces"`
	TillOpens                   int   `json:"till_opens"`
	TillPaidInOuts              int   `json:"till_paid_in_outs"`
	TillCounts                  int   `json:"till_counts"`
	TillCompletes               int   `json:"till_completes"`
	TillOverShortCount          int   `json:"till_over_short_count"`
	Lodgements                  int   `json:"lodgements"`
	CardSettlements             int   `json:"card_settlements"`
	PurchaseReceipts            int   `json:"purchase_receipts"`
	ReorderTriggeredReceipts    int   `json:"reorder_triggered_receipts"`
	VendorBills                 int   `json:"vendor_bills"`
	LandedCosts                 int   `json:"landed_costs"`
	LandedCostVoids             int   `json:"landed_cost_voids"`
	StockTransfers              int   `json:"stock_transfers"`
	ManualJournals              int   `json:"manual_journals"`
	BankStatements              int   `json:"bank_statements"`
	PeriodCloses                int   `json:"period_closes"`
	YearCloses                  int   `json:"year_closes"`
	SalesSubtotalCents          int64 `json:"sales_subtotal_cents"`
	SalesTaxCents               int64 `json:"sales_tax_cents"`
	SalesTotalCents             int64 `json:"sales_total_cents"`
	AccountSalesTotalCents      int64 `json:"account_sales_total_cents"`
	Top15ContractorAccountSales int   `json:"top_15_contractor_account_sales"`
	TotalAccountInvoiceDocs     int   `json:"total_account_invoice_docs"`
	Top15ContractorAccountCents int64 `json:"top_15_contractor_account_cents"`
	ReturnsTotalCents           int64 `json:"returns_total_cents"`
	CreditMemosTotalCents       int64 `json:"credit_memos_total_cents"`
	ReceiptsTotalCents          int64 `json:"receipts_total_cents"`
	LandedCostChargesCents      int64 `json:"landed_cost_charges_cents"`
	LandedCostVoidCents         int64 `json:"landed_cost_void_cents"`
	OpenARCurrent0To30Cents     int64 `json:"open_ar_current_0_to_30_cents"`
	OpenAR31To90Cents           int64 `json:"open_ar_31_to_90_cents"`
	OpenAROver90Cents           int64 `json:"open_ar_over_90_cents"`
	OpenAR31To90Count           int   `json:"open_ar_31_to_90_count"`
	OpenAROver90Count           int   `json:"open_ar_over_90_count"`
}
