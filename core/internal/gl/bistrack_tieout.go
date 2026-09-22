package gl

// bistrack_tieout.go, the M2-C1 STATIC BisTrack GL tie-out. It reconciles the ERP's own posted books (the
// INCEPTION-TO-DATE closing trial balance as of a fiscal period end, TrialBalanceAsOf) against a static,
// synthetic BisTrack GLExport closing trial balance (testdata/bistrack_gl_snapshot_*.json), account by
// account, and returns a tie-out report. The closing-balance basis matters: a BisTrack closing TB is
// cumulative for balance-sheet accounts, so reconciling against single-period movement would manufacture a
// false discrepancy on every account carrying an opening balance (mirrors the AR tie-out's AccountBalanceAsOf).
//
// WHY STATIC: the live BisTrack GLExport is blocked on client data access (Q-045/046/047, decision_log D-031).
// This is the roadmap M2 gate criterion C1 fallback: "GL reconciled against static BisTrack test data" (due
// 2026-07-31). The fixture is a DROP-IN for the real export, same JSON envelope, same reconciliation grain
// (per-account closing balances for one fiscal period). When the real GLExport lands, only the fixture's rows
// (and, if BisTrack account numbers differ, the per-row erp_account_code crosswalk) change; this code does not.
//
// MONEY: integer cents (int64) end to end. The fixture carries dollars as strings (BisTrack/GP export
// convention); parseDollarsToCents converts EXACTLY via integer arithmetic, never float. Sign is handled on
// the unambiguous net-debit basis (Σdebit − Σcredit), then presented in each account's NATURAL orientation
// (gl_accounts.normal_balance): a debit-normal account shows +net-debit; a credit-normal account shows
// −net-debit (so a positive number always reads as "more of what this account normally holds").

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"
)

// periodEnd returns the last instant of fiscal month (year, month) in UTC, the as-of boundary for an
// inception-to-date closing balance (posted_at <= periodEnd). It is the start of the next month minus one
// nanosecond, so December (month 12) rolls the year correctly via time.Date normalization.
func periodEnd(year, month int) time.Time {
	return time.Date(year, time.Month(month)+1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Nanosecond)
}

// bistrackDefaultSnapshotJSON is the canonical static BisTrack GL snapshot, EMBEDDED so the runtime tie-out
// endpoint resolves it regardless of the process working directory (the same file the tests read from disk).
// It lives under testdata/ because it is first and foremost the M2-C1 test fixture; go:embed matches it by
// explicit path (the testdata exclusion only applies to wildcard walks).
//
//go:embed testdata/bistrack_gl_snapshot_2026-06.json
var bistrackDefaultSnapshotJSON []byte

// BisTrackSnapshot is the parsed static BisTrack GL fixture: a single fiscal period's per-account CLOSING
// TRIAL BALANCE (period header + one row per GL account with a debit or credit magnitude), plus the crosswalk
// to the ERP gl_accounts code. This is balance (summary) grain, NOT the 46-column journal-line
// GeneralLedgerExportLine detail contract; if the real export lands at line grain, an aggregating loader
// adapter is needed to reduce lines to per-account closing balances before this envelope applies.
type BisTrackSnapshot struct {
	Format        string                `json:"format"`
	FormatVersion string                `json:"format_version"`
	IsSynthetic   bool                  `json:"is_synthetic"`
	SourceSystem  string                `json:"source_system"`
	FiscalYear    int                   `json:"fiscal_year"`
	FiscalMonth   int                   `json:"fiscal_month"`
	PeriodLabel   string                `json:"period_label"`
	Currency      string                `json:"currency"`
	Accounts      []BisTrackSnapshotRow `json:"accounts"`
}

// BisTrackSnapshotRow is one GL account's closing balance in the BisTrack export. Debit/Credit are
// non-negative dollar magnitudes as strings (exactly one is non-zero for a normal closing balance, but the
// parser tolerates both being set and nets them). ERPAccountCode is the crosswalk into gl_accounts.
type BisTrackSnapshotRow struct {
	BisTrackAccount string `json:"bistrack_account"`
	BisTrackName    string `json:"bistrack_name"`
	ERPAccountCode  string `json:"erp_account_code"`
	Debit           string `json:"debit"`
	Credit          string `json:"credit"`
}

// TieoutLine is one account's reconciliation: the ERP balance vs the BisTrack balance, both in natural
// orientation (cents), and their delta (erp − bistrack). Delta == 0 means that account ties exactly.
type TieoutLine struct {
	AccountCode   string `json:"account_code"`
	AccountName   string `json:"account_name"`
	NormalBalance string `json:"normal_balance"`         // DEBIT|CREDIT (gl_accounts)
	ERPCents      int64  `json:"erp_cents"`              // natural orientation
	BisTrackCents int64  `json:"bistrack_cents"`         // natural orientation
	DeltaCents    int64  `json:"delta_cents"`            // erp_cents − bistrack_cents
	Source        string `json:"source"`                 // BOTH|ERP_ONLY|BISTRACK_ONLY
	BisTrackRef   string `json:"bistrack_ref,omitempty"` // BisTrack account no. (provenance)
}

// BisTrackTieoutReport is the full per-period reconciliation of the ERP books against the static BisTrack
// snapshot: every reconciled account, the grand-total delta, whether it ties within tolerance, and the subset
// of lines that are out of tie (the discrepancy list).
type BisTrackTieoutReport struct {
	FiscalYear     int          `json:"fiscal_year"`
	Month          int          `json:"month"`
	BranchID       string       `json:"branch_id,omitempty"`
	Source         string       `json:"source"`          // fixture source system (provenance)
	IsSynthetic    bool         `json:"is_synthetic"`    // true while on the static fallback fixture
	ToleranceCents int64        `json:"tolerance_cents"` // per-account materiality tolerance
	Lines          []TieoutLine `json:"lines"`
	Discrepancies  []TieoutLine `json:"discrepancies"` // lines with |delta| > tolerance
	// DELTA-BASIS NOTE: the two totals below are on the NET-DEBIT basis (Σdebit − Σcredit, sign-invariant across
	// account types), whereas each TieoutLine.DeltaCents is on that account's NATURAL orientation. They therefore
	// do NOT cross-foot for credit-normal accounts (a credit-normal line delta is the sign-flip of its net-debit
	// contribution). Use per-line DeltaCents for account-level review and GrandDeltaCents for the balance check;
	// do not sum the line deltas expecting GrandDeltaCents. TiesWithinTol (abs-based) is unaffected by the bases.
	TotalERPCents      int64 `json:"total_erp_cents"`      // Σ erp net-debit (0 when the ERP books balance)
	TotalBisTrackCents int64 `json:"total_bistrack_cents"` // Σ bistrack net-debit (0 when the export balances)
	GrandDeltaCents    int64 `json:"grand_delta_cents"`    // Σ (erp − bistrack) net-debit
	TiesWithinTol      bool  `json:"ties_within_tolerance"`
}

// LoadBisTrackSnapshot parses a static BisTrack GL snapshot fixture. An empty path uses the EMBEDDED canonical
// M2-C1 snapshot (so it resolves at runtime with no dependency on the working directory); a non-empty path is
// read from disk (tests re-anchor the fixture to an isolated year in a temp file).
func LoadBisTrackSnapshot(path string) (BisTrackSnapshot, error) {
	var raw []byte
	if path == "" {
		raw = bistrackDefaultSnapshotJSON
	} else {
		b, err := os.ReadFile(path)
		if err != nil {
			return BisTrackSnapshot{}, fmt.Errorf("gl: read bistrack snapshot %q: %w", path, err)
		}
		raw = b
	}
	var snap BisTrackSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return BisTrackSnapshot{}, fmt.Errorf("gl: parse bistrack snapshot: %w", err)
	}
	if len(snap.Accounts) == 0 {
		return BisTrackSnapshot{}, fmt.Errorf("gl: bistrack snapshot has no account rows")
	}
	return snap, nil
}

// netDebitCents returns the row's net-debit magnitude in cents (debit − credit), parsed exactly from the
// dollar strings. Both fields are optional; a missing/blank field is zero.
func (row BisTrackSnapshotRow) netDebitCents() (int64, error) {
	d, err := parseDollarsToCents(row.Debit)
	if err != nil {
		return 0, fmt.Errorf("gl: bistrack %s debit: %w", row.BisTrackAccount, err)
	}
	c, err := parseDollarsToCents(row.Credit)
	if err != nil {
		return 0, fmt.Errorf("gl: bistrack %s credit: %w", row.BisTrackAccount, err)
	}
	return d - c, nil
}

// parseDollarsToCents converts a signed decimal dollar string ("10000.00", "-25.5", "1234", "") to int64
// cents using integer arithmetic ONLY, no float, so there is no rounding drift. It accepts 0, 1, or 2
// fractional digits and thousands separators are NOT allowed (the export is machine-generated). "" == 0.
func parseDollarsToCents(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	neg := false
	switch s[0] {
	case '-':
		neg, s = true, s[1:]
	case '+':
		s = s[1:]
	}
	if s == "" {
		return 0, fmt.Errorf("gl: empty dollar amount")
	}
	whole, frac := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		whole, frac = s[:i], s[i+1:]
	}
	if whole == "" {
		whole = "0"
	}
	if len(frac) > 2 {
		return 0, fmt.Errorf("gl: too many decimal places in %q", s)
	}
	for len(frac) < 2 { // pad "5" → "50", "" → "00"
		frac += "0"
	}
	var cents int64
	for _, part := range []string{whole, frac} {
		for i := 0; i < len(part); i++ {
			ch := part[i]
			if ch < '0' || ch > '9' {
				return 0, fmt.Errorf("gl: non-numeric dollar amount %q", s)
			}
			d := int64(ch - '0')
			// Overflow guard: cents*10 + d must stay within int64 (else it silently wraps and can flip sign).
			// A GL magnitude never approaches int64-max cents ($9.2e16), so tripping this means a malformed or
			// corrupt export value, fail loudly rather than reconcile a wrong number.
			if cents > (math.MaxInt64-d)/10 {
				return 0, fmt.Errorf("gl: dollar amount %q overflows int64 cents", s)
			}
			cents = cents*10 + d
		}
	}
	if neg {
		cents = -cents
	}
	return cents, nil
}

// naturalOrient converts a net-debit amount to the account's natural orientation using its normal balance:
// debit-normal keeps the sign, credit-normal flips it (so a positive result always means "more of what this
// account normally carries"). It mirrors glBalanceCents / TieOut's sign handling.
func naturalOrient(netDebit int64, normalBalance string) int64 {
	if normalBalance == NormalCredit {
		return -netDebit
	}
	return netDebit
}

// BisTrackTieOut reconciles the ERP CLOSING trial balance as of the end of (year, month), inception-to-date,
// via TrialBalanceAsOf, against a static BisTrack GL snapshot for that same period end (branchID "" is
// consolidated). An empty fixturePath uses the default M2-C1 fixture. toleranceCents is the per-account
// materiality band (default 0 for exact tie-out; callers pass a small band when rounding on a real export is
// expected). It computes on the fly (no persisted report table).
//
// BASIS: the ERP side is inception-to-date (posted_at <= period end), NOT single-period movement, because a
// BisTrack closing trial balance is cumulative for balance-sheet accounts. Reconciling movement against a
// cumulative closing balance would flag a false discrepancy on every balance-sheet account that carries an
// opening balance. This mirrors the AR subledger tie-out (close.go TieOut → AccountBalanceAsOf).
//
// The report contains one line per account seen on EITHER side (full-outer join on erp_account_code), so an
// account present in one system but not the other surfaces as a discrepancy (ERP_ONLY / BISTRACK_ONLY) rather
// than being silently dropped. Grand-total deltas use the net-debit basis: Σ erp and Σ bistrack are each 0 when
// that side's books balance (the double-entry invariant), so a non-zero grand delta is a real out-of-tie.
func (s *Service) BisTrackTieOut(ctx context.Context, year, month int, branchID, fixturePath string, toleranceCents int64) (BisTrackTieoutReport, error) {
	if year <= 0 {
		return BisTrackTieoutReport{}, badReq("gl: bistrack tie-out requires a fiscal_year")
	}
	if toleranceCents < 0 {
		toleranceCents = 0
	}
	snap, err := LoadBisTrackSnapshot(fixturePath)
	if err != nil {
		return BisTrackTieoutReport{}, err
	}
	if snap.FiscalYear != 0 && snap.FiscalYear != year {
		return BisTrackTieoutReport{}, badReq("gl: bistrack snapshot is FY%d but tie-out requested FY%d", snap.FiscalYear, year)
	}
	if month <= 0 || month > 12 {
		return BisTrackTieoutReport{}, badReq("gl: bistrack tie-out requires a fiscal month 1-12 (a closing balance is reconciled at ONE period end, not whole-year)")
	}
	if snap.FiscalMonth != 0 && snap.FiscalMonth != month {
		return BisTrackTieoutReport{}, badReq("gl: bistrack snapshot is month %d but tie-out requested month %d", snap.FiscalMonth, month)
	}

	// The BisTrack fixture is a CLOSING trial balance: cumulative inception-to-date for balance-sheet accounts
	// (and year-to-date for P&L, since prior years are swept to retained earnings at year-end close). So the ERP
	// side MUST be reconciled on the same inception-to-date basis as of period end, NOT the single-period
	// MOVEMENT that TrialBalance(year, month) returns (that view groups by fiscal month, so it understates every
	// balance-sheet account by its opening balance and manufactures a false discrepancy on the first real export
	// with any carry-forward). This mirrors the AR subledger TieOut, which reconciles GL 1100 via
	// AccountBalanceAsOf for exactly this reason. asOf is the last instant of the requested fiscal month.
	asOf := periodEnd(year, month)
	rows, err := s.store.TrialBalanceAsOf(ctx, asOf, branchID)
	if err != nil {
		return BisTrackTieoutReport{}, err
	}

	// - join both sides on account code -
	type side struct {
		name    string
		normal  string
		erpND   int64 // net-debit (Σdebit − Σcredit)
		btND    int64
		btRef   string
		haveERP bool
		haveBT  bool
	}
	acc := map[string]*side{}
	get := func(code string) *side {
		if acc[code] == nil {
			acc[code] = &side{}
		}
		return acc[code]
	}

	// ERP side: the inception-to-date closing-balance rows (BalanceCents is net-debit).
	for _, r := range rows {
		e := get(r.AccountCode)
		e.name = r.AccountName
		e.normal = r.NormalBalance
		e.erpND = r.BalanceCents
		e.haveERP = true
	}

	// BisTrack side: the static snapshot rows, summed by ERP account code (multiple BisTrack sub-accounts may
	// crosswalk to one ERP account, the export can be finer-grained than the ERP chart).
	for _, row := range snap.Accounts {
		code := strings.TrimSpace(row.ERPAccountCode)
		if code == "" {
			return BisTrackTieoutReport{}, badReq("gl: bistrack snapshot row %q has no erp_account_code crosswalk", row.BisTrackAccount)
		}
		nd, err := row.netDebitCents()
		if err != nil {
			return BisTrackTieoutReport{}, err
		}
		b := get(code)
		b.btND += nd
		b.haveBT = true
		if b.btRef == "" {
			b.btRef = row.BisTrackAccount
		} else {
			b.btRef += "," + row.BisTrackAccount
		}
		if b.name == "" {
			b.name = row.BisTrackName
		}
	}

	// Resolve normal_balance for any BisTrack-only account (absent from the ERP TB rows) so its natural
	// orientation is correct in the report.
	for code, sd := range acc {
		if sd.normal == "" {
			if a, ok, aerr := s.store.AccountByCode(ctx, code); aerr != nil {
				return BisTrackTieoutReport{}, aerr
			} else if ok {
				sd.normal = a.NormalBalance
				if sd.name == "" {
					sd.name = a.Name
				}
			} else {
				sd.normal = NormalDebit // unknown account: treat as debit-normal (net-debit shown as-is)
			}
		}
	}

	codes := make([]string, 0, len(acc))
	for code := range acc {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	report := BisTrackTieoutReport{
		FiscalYear:     year,
		Month:          month,
		BranchID:       branchID,
		Source:         snap.SourceSystem,
		IsSynthetic:    snap.IsSynthetic,
		ToleranceCents: toleranceCents,
	}
	for _, code := range codes {
		sd := acc[code]
		line := TieoutLine{
			AccountCode:   code,
			AccountName:   sd.name,
			NormalBalance: sd.normal,
			ERPCents:      naturalOrient(sd.erpND, sd.normal),
			BisTrackCents: naturalOrient(sd.btND, sd.normal),
			BisTrackRef:   sd.btRef,
		}
		line.DeltaCents = line.ERPCents - line.BisTrackCents
		switch {
		case sd.haveERP && sd.haveBT:
			line.Source = "BOTH"
		case sd.haveERP:
			line.Source = "ERP_ONLY"
		default:
			line.Source = "BISTRACK_ONLY"
		}
		report.Lines = append(report.Lines, line)
		report.TotalERPCents += sd.erpND
		report.TotalBisTrackCents += sd.btND
		if abs64(line.DeltaCents) > toleranceCents {
			report.Discrepancies = append(report.Discrepancies, line)
		}
	}
	report.GrandDeltaCents = report.TotalERPCents - report.TotalBisTrackCents
	report.TiesWithinTol = len(report.Discrepancies) == 0
	return report, nil
}
