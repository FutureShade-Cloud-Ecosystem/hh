package invcount

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"

	"github.com/jackc/pgx/v5"
)

// - sheet + line status vocabulary (0040 count_sheets.status CHECK set) -
const (
	StatusOpen      = "OPEN"      // created; no snapshot frozen yet
	StatusFrozen    = "FROZEN"    // snapshot captured; counting in progress
	StatusCounted   = "COUNTED"   // maker submitted counts; awaiting checker approval + post
	StatusPosted    = "POSTED"    // variances posted (stock moves + reason-coded GL journal)
	StatusCancelled = "CANCELLED" // abandoned before post
)

// recon run status (0040 inventory_recon_runs.status CHECK set).
const (
	ReconOpen     = "OPEN"
	ReconTied     = "TIED"       // subledger == GL 1200
	ReconOutOfTie = "OUT_OF_TIE" // subledger != GL 1200 (delta surfaces on the recon screen)
)

// - domain DTOs (JSON-serialized to the count screens) -

// Sheet is one snapshot-frozen cycle count. counted_by is the maker; approved_by is the checker (must differ).
type Sheet struct {
	ID         string `json:"id"`
	SheetNo    string `json:"sheet_no"`
	BranchID   string `json:"branch_id"`
	Scope      string `json:"scope"`
	Status     string `json:"status"`
	FreezeAt   string `json:"freeze_at,omitempty"`
	CountedBy  string `json:"counted_by,omitempty"`
	ApprovedBy string `json:"approved_by,omitempty"`
	ApprovedAt string `json:"approved_at,omitempty"`
	PostedAt   string `json:"posted_at,omitempty"`
	Notes      string `json:"notes"`
	CreatedAt  string `json:"created_at,omitempty"`
}

// Line is a snapshot on-hand vs counted reading, its variance, the weighted-avg valuation, and the reason.
type Line struct {
	ID                 string   `json:"id"`
	SheetID            string   `json:"sheet_id"`
	ProductID          string   `json:"product_id"`
	SKU                string   `json:"sku,omitempty"`
	ProductName        string   `json:"product_name,omitempty"`
	LocationID         string   `json:"location_id"`
	SnapshotQtyBase    float64  `json:"snapshot_qty_base"`
	CountedQtyBase     *float64 `json:"counted_qty_base"`
	VarianceBase       float64  `json:"variance_base"`
	UnitCostCents      int64    `json:"unit_cost_cents,omitempty"`
	VarianceValueCents int64    `json:"variance_value_cents,omitempty"`
	ReasonCode         string   `json:"reason_code,omitempty"`
	MoveID             string   `json:"move_id,omitempty"`
}

// ReasonCode is a per-reason GL account mapping (DAMAGE→5200, FOUND→5200, ...). account_code is the reason leg;
// the 1200 inventory leg is resolved through gl.ResolveRule at post time.
type ReasonCode struct {
	Code        string `json:"code"`
	Description string `json:"description"`
	AccountCode string `json:"account_code"`
	IsActive    bool   `json:"is_active"`
}

// ReconRun is one monthly inventory→GL reconciliation (kept per period and branch, re-runnable).
type ReconRun struct {
	ID             string `json:"id"`
	BranchID       string `json:"branch_id,omitempty"`
	FiscalYear     int    `json:"fiscal_year"`
	Month          int    `json:"month"`
	SubledgerCents int64  `json:"subledger_cents"`
	GL1200Cents    int64  `json:"gl_1200_cents"`
	DeltaCents     int64  `json:"delta_cents"`
	Status         string `json:"status"`
	RunBy          string `json:"run_by,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
}

// SheetDetail is a sheet plus its lines and a variance summary (the count-variance screen payload).
type SheetDetail struct {
	Sheet   Sheet           `json:"sheet"`
	Lines   []Line          `json:"lines"`
	Summary VarianceSummary `json:"summary"`
}

// VarianceSummary rolls up a sheet's counted variances for the variance-summary surface.
type VarianceSummary struct {
	LineCount             int   `json:"line_count"`
	CountedLines          int   `json:"counted_lines"`
	VarianceLines         int   `json:"variance_lines"`
	NetVarianceValueCents int64 `json:"net_variance_value_cents,omitempty"`
	AbsVarianceValueCents int64 `json:"abs_variance_value_cents,omitempty"`
}

// NegativeStockRow is one product carrying by-design negative on-hand (sell-before-receipt detection).
type NegativeStockRow struct {
	ProductID    string  `json:"product_id"`
	LocationID   string  `json:"location_id"`
	OnHandBase   float64 `json:"on_hand_base"`
	AvgCostCents int64   `json:"avg_cost_cents,omitempty"`
}

// - request payloads -

// CreateSheetRequest opens a new count sheet for a branch (scope FULL/CATEGORY:<id>/AD_HOC).
type CreateSheetRequest struct {
	BranchID string `json:"branch_id"`
	Scope    string `json:"scope"`
	Notes    string `json:"notes"`
}

// CountLineInput is one counted line the maker submits (counted qty + reason for the variance).
type CountLineInput struct {
	ProductID      string  `json:"product_id"`
	LocationID     string  `json:"location_id"`
	CountedQtyBase float64 `json:"counted_qty_base"`
	ReasonCode     string  `json:"reason_code"`
}

// SubmitCountsRequest is the maker's blind/open count submission for a frozen sheet.
type SubmitCountsRequest struct {
	Lines []CountLineInput `json:"lines"`
}

// - GLBalanceReader, OPTIONAL recon seam (the GL 1200 side of the monthly tie-out) -
// The scaffold main.go wiring injects StockCounter+GLPoster+RuleResolver but NOT a GL-balance reader, so the
// recon's GL side is only live once the integrator adds `.WithGLBalanceReader(glBalanceAdapter{svc: glSvc})`
// (glBalanceAdapter already exists in main.go and satisfies this interface). Until then the recon runs with the
// subledger side fully computed and the GL side 0 (OUT_OF_TIE), the closest honest behavior. Recorded as a
// deviation for the integrator. Additive; no frozen signature changes.
type GLBalanceReader interface {
	GLBalanceCents(ctx context.Context, accountCode string, year, month int, branchID string) (int64, error)
	// GLBalanceThroughCents is the cumulative through-period balance the tie-out's GL side reads
	// (FW1-11): stock as of the period end against the ledger as of the period end, not one month's
	// movement. Satisfied by the gl provider through the composition root's adapter.
	GLBalanceThroughCents(ctx context.Context, accountCode string, year, month int, branchID string) (int64, error)
}

// WithGLBalanceReader injects the GL account-balance seam (the recon 1200 tie-out side). Optional.
func (s *Service) WithGLBalanceReader(r GLBalanceReader) *Service { s.glBal = r; return s }

// - persistence seam (the service depends on this; *Repository satisfies it, unit tests inject a fake) -
type countStore interface {
	// non-tx reads
	GetSheet(ctx context.Context, sheetID string) (Sheet, error)
	ListSheets(ctx context.Context, branchID, status string) ([]Sheet, error)
	ListLines(ctx context.Context, sheetID string) ([]Line, error)
	ReasonCodes(ctx context.Context) ([]ReasonCode, error)
	// sheet lifecycle (own tx)
	CreateSheet(ctx context.Context, branchID, scope, notes string) (Sheet, error)
	FreezeSheet(ctx context.Context, sheetID string, snaps []Snapshot) error
	SaveCounts(ctx context.Context, sheetID, countedBy string, lines []CountLineInput) error
	ApproveSheet(ctx context.Context, sheetID, approvedBy string) error
	CancelSheet(ctx context.Context, sheetID string) error
	// post (in the caller's tx)
	InTx(ctx context.Context, fn func(tx pgx.Tx) error) error
	LockSheetTx(ctx context.Context, tx pgx.Tx, sheetID string) (Sheet, error)
	LinesForPostTx(ctx context.Context, tx pgx.Tx, sheetID string) ([]Line, error)
	UpdateLinePostedTx(ctx context.Context, tx pgx.Tx, lineID string, unitCostCents, varianceValueCents int64, moveID string) error
	MarkSheetPostedTx(ctx context.Context, tx pgx.Tx, sheetID string) error
	// recon
	GetReconRun(ctx context.Context, year, month int, branchID string) (ReconRun, bool, error)
	InsertReconRun(ctx context.Context, r ReconRun) (ReconRun, error)
	ListReconRuns(ctx context.Context, branchID string) ([]ReconRun, error)
}

// - error taxonomy (mirrors the till/pricing house pattern: an errors.As-able status carrier) -
type clientError struct {
	status int
	msg    string
}

func (e clientError) Error() string { return e.msg }
func clientErr(status int, format string, a ...any) error {
	return clientError{status: status, msg: fmt.Sprintf(format, a...)}
}

var (
	errNotFound        = clientError{status: http.StatusNotFound, msg: "invcount: not found"}
	errUnauthorized    = clientError{status: http.StatusUnauthorized, msg: "invcount: unauthenticated"}
	errSheetNotCounted = clientError{status: http.StatusConflict, msg: "invcount: sheet is not in COUNTED state"}
	errNotFrozen       = clientError{status: http.StatusConflict, msg: "invcount: sheet must be FROZEN before counts are entered"}
	errNoApprover      = clientError{status: http.StatusUnprocessableEntity, msg: "invcount: post requires a checker approval (approved_by) distinct from the maker"}
	// errForbidden is DIB-390's horizontal boundary: a caller acting on a sheet in a branch their scope
	// does not reach. A count sheet moves STOCK and posts a GL journal for its branch, so every step of
	// the lifecycle is checked against that branch, not just the one that created it.
	errForbidden = clientError{status: http.StatusForbidden, msg: "invcount: caller cannot access this branch"}
)

func badReq(format string, a ...any) error {
	return clientError{status: http.StatusBadRequest, msg: fmt.Sprintf(format, a...)}
}

// - pure domain helpers (unit-tested with no DB) -

// valueVariance values a signed base-unit variance at the weighted-average unit cost, rounded to the cent.
// Positive = found stock (inventory up); negative = loss/shrink (inventory down).
func valueVariance(varianceBase float64, avgCostCents int64) int64 {
	return int64(math.Round(varianceBase * float64(avgCostCents)))
}

// validateMakerChecker enforces the four-eyes gate: a sheet may only post from COUNTED, with a checker
// (approved_by) set who is NOT the maker (counted_by). Returns a typed clientError so the handler maps it.
func validateMakerChecker(s Sheet) error {
	if s.Status == StatusPosted {
		return nil // idempotent replay, already approved + posted
	}
	if s.Status != StatusCounted {
		return errSheetNotCounted
	}
	if s.ApprovedBy == "" {
		return errNoApprover
	}
	if s.CountedBy != "" && s.ApprovedBy == s.CountedBy {
		return clientErr(http.StatusUnprocessableEntity,
			"invcount: maker-checker violation, the checker (%s) must differ from the maker", s.ApprovedBy)
	}
	return nil
}

// buildAdjustmentJournal builds the balanced reason-coded variance journal from a sheet's posted lines: the
// reason leg (per-reason GL account) against the 1200 inventory leg (resolved via gl.ResolveRule). A positive
// variance (found) debits inventory / credits the reason account; a negative variance (loss) does the reverse.
// Legs that share an account are netted; the result balances by construction (Σ signed = 0). reasonAccounts
// maps a reason code to its GL account. Lines with zero value or no reason are skipped.
func buildAdjustmentJournal(sheetID, sheetNo, branchID, invAccount string, reasonAccounts map[string]string, lines []Line) (GLJournal, error) {
	// net signed debit per account (positive = net debit, negative = net credit).
	net := map[string]int64{}
	any := false
	for _, ln := range lines {
		if ln.VarianceValueCents == 0 {
			continue
		}
		reasonAcct, ok := reasonAccounts[ln.ReasonCode]
		if !ok || reasonAcct == "" {
			return GLJournal{}, badReq("invcount: line %s has no GL account for reason %q", ln.ProductID, ln.ReasonCode)
		}
		v := ln.VarianceValueCents
		net[invAccount] += v // found (+v) → DR inventory; loss (−v) → CR inventory
		net[reasonAcct] -= v // found (+v) → CR reason;    loss (−v) → DR reason
		any = true
	}
	if !any {
		return GLJournal{}, badReq("invcount: sheet %s has no valued variances to post", sheetID)
	}
	accts := make([]string, 0, len(net))
	for a := range net {
		accts = append(accts, a)
	}
	// deterministic ordering: inventory leg first, then reason accounts sorted.
	sort.Slice(accts, func(i, j int) bool {
		if accts[i] == invAccount {
			return true
		}
		if accts[j] == invAccount {
			return false
		}
		return accts[i] < accts[j]
	})
	lines2 := make([]GLLine, 0, len(accts))
	for _, a := range accts {
		n := net[a]
		if n == 0 {
			continue
		}
		if n > 0 {
			lines2 = append(lines2, GLLine{AccountCode: a, DebitCents: n, Memo: "count adjustment " + sheetNo})
		} else {
			lines2 = append(lines2, GLLine{AccountCode: a, CreditCents: -n, Memo: "count adjustment " + sheetNo})
		}
	}
	return GLJournal{
		Source:         "ADJUSTMENT",
		SourceRefID:    sheetID,
		IdempotencyKey: idempotencyKey(sheetID),
		Memo:           "inventory count adjustment " + sheetNo,
		BranchID:       branchID,
		Lines:          lines2,
	}, nil
}

// idempotencyKey is the stable per-sheet GL idempotency key (gl.PostExternal dedupes on it, so re-posting a
// POSTED sheet is a no-op, the cross-module idempotency guarantee).
func idempotencyKey(sheetID string) string { return "invcount.count:" + sheetID }

// summarize rolls a sheet's lines into the variance-summary surface.
func summarize(lines []Line) VarianceSummary {
	sum := VarianceSummary{LineCount: len(lines)}
	for _, ln := range lines {
		if ln.CountedQtyBase != nil {
			sum.CountedLines++
		}
		if ln.VarianceBase != 0 {
			sum.VarianceLines++
		}
		sum.NetVarianceValueCents += ln.VarianceValueCents
		if ln.VarianceValueCents < 0 {
			sum.AbsVarianceValueCents -= ln.VarianceValueCents
		} else {
			sum.AbsVarianceValueCents += ln.VarianceValueCents
		}
	}
	return sum
}
