package finstmt

// -- report row / detail types (beyond the frozen StatementRow / AgingRow) ----

// GLDetailRow is one POSTED journal line from v_gl_detail, the drill-down target. A statement figure
// ties EXACTLY to Σ(natural-orientation debit-credit) of the detail rows behind its account.
type GLDetailRow struct {
	LineID      string `json:"line_id"`
	JournalID   string `json:"journal_id"`
	EntryNo     int    `json:"entry_no"`
	Source      string `json:"source"`
	PeriodYear  int    `json:"period_year"`
	PeriodMonth int    `json:"period_month"`
	AccountCode string `json:"account_code"`
	AccountName string `json:"account_name"`
	AccountType string `json:"account_type"`
	BranchID    string `json:"branch_id"`
	DebitCents  int64  `json:"debit_cents"`
	CreditCents int64  `json:"credit_cents"`
	Memo        string `json:"memo"`
	PostedAt    string `json:"posted_at"`
}

// GLDetailFilter narrows the drill-down. Zero year/month = all; empty account/branch = all.
type GLDetailFilter struct {
	Year        int
	Month       int
	AccountCode string
	BranchID    string
}

// TrialBalanceRow carries the full debit/credit columns (the frozen TrialBalance projects the signed
// balance into StatementRow; this richer shape backs the report + export).
type TrialBalanceRow struct {
	AccountCode      string `json:"account_code"`
	AccountName      string `json:"account_name"`
	AccountType      string `json:"account_type"`
	NormalBalance    string `json:"normal_balance"`
	TotalDebitCents  int64  `json:"total_debit_cents"`
	TotalCreditCents int64  `json:"total_credit_cents"`
	BalanceCents     int64  `json:"balance_cents"` // Σdebit − Σcredit (positive = debit-heavy)
}

// -- composed reports (totals + invariants the handlers/tests assert) ---------

// IncomeStatementReport is the P&L with revenue/expense subtotals + net income.
type IncomeStatementReport struct {
	Year           int            `json:"year"`
	Month          int            `json:"month"` // 0 = full year
	BranchID       string         `json:"branch_id"`
	Revenue        []StatementRow `json:"revenue"`
	Expenses       []StatementRow `json:"expenses"`
	RevenueCents   int64          `json:"revenue_cents"`
	ExpenseCents   int64          `json:"expense_cents"`
	NetIncomeCents int64          `json:"net_income_cents"`
}

// BalanceSheetReport groups the three sections + asserts the balance invariant
// (assets == liabilities + equity + net income).
type BalanceSheetReport struct {
	Year                  int            `json:"year"`
	BranchID              string         `json:"branch_id"`
	Assets                []StatementRow `json:"assets"`
	Liabilities           []StatementRow `json:"liabilities"`
	Equity                []StatementRow `json:"equity"`
	TotalAssetsCents      int64          `json:"total_assets_cents"`
	TotalLiabilitiesCents int64          `json:"total_liabilities_cents"`
	TotalEquityCents      int64          `json:"total_equity_cents"`
	NetIncomeCents        int64          `json:"net_income_cents"`
	Balanced              bool           `json:"balanced"`
}

// TrialBalanceReport carries the rows plus the double-entry tie (Σdebit == Σcredit → InBalance).
type TrialBalanceReport struct {
	Year             int               `json:"year"`
	Rows             []TrialBalanceRow `json:"rows"`
	TotalDebitCents  int64             `json:"total_debit_cents"`
	TotalCreditCents int64             `json:"total_credit_cents"`
	InBalance        bool              `json:"in_balance"`
}

// -- comparatives -------------------------------------------------------------

// ComparativeRow pairs a current-period amount with the prior-period amount for the same account
// (prior == 0 when history is empty, the empty-comparative case must not drop the row).
type ComparativeRow struct {
	AccountCode  string `json:"account_code"`
	AccountName  string `json:"account_name"`
	AccountType  string `json:"account_type"`
	CurrentCents int64  `json:"current_cents"`
	PriorCents   int64  `json:"prior_cents"`
	DeltaCents   int64  `json:"delta_cents"`
}

// -- AR aging summary ---------------------------------------------------------

// AgingBucket is one age band's total.
type AgingBucket struct {
	Bucket string `json:"bucket"`
	Cents  int64  `json:"cents"`
}

// AgingSummary is the bucketed roll-up whose GrandTotalCents ties to billing's ARBalanceCents.
type AgingSummary struct {
	BranchID        string        `json:"branch_id"`
	Buckets         []AgingBucket `json:"buckets"`
	Rows            []AgingRow    `json:"rows"`
	GrandTotalCents int64         `json:"grand_total_cents"`
}

// agingBucketOrder is the canonical display order of the age bands (mirrors the 0034 v_ar_aging CASE).
var agingBucketOrder = []string{"CURRENT", "1_30", "31_60", "61_90", "90_PLUS"}

// -- schedules (finstmt_schedules registry CRUD) ------------------------------

// ScheduleInput is the create/update payload for a scheduled-report registry row.
type ScheduleInput struct {
	Report     string `json:"report"`
	Recipients string `json:"recipients"`
	Cadence    string `json:"cadence"`
	BranchID   string `json:"branch_id"`
	IsActive   bool   `json:"is_active"`
}

// validReports / validCadences mirror the 0034 finstmt_schedules CHECK constraints (validated in-app so
// a bad payload is a 400, not a 23514 from Postgres).
var validReports = map[string]bool{
	"INCOME_STATEMENT": true, "BALANCE_SHEET": true, "TRIAL_BALANCE": true,
	"GL_DETAIL": true, "AR_AGING": true,
}
var validCadences = map[string]bool{"DAILY": true, "WEEKLY": true, "MONTHLY": true}
