package invcount

import (
	"context"
	"errors"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5"
)

// store resolves the persistence seam the free-function core uses: the real *Repository in production, a
// DB-free fake in unit tests. Service.repo is the concrete pgx layer; it satisfies countStore.
func (s *Service) store() countStore { return s.repo }

// caller resolves the trusted, server-stamped acting staff member (never a spoofable header, C-1).
func (s *Service) caller(ctx context.Context) (string, error) {
	uid := identity.StaffUserIDFromContext(ctx)
	if uid == "" {
		return "", errUnauthorized
	}
	return uid, nil
}

// authorizeBranch is the DIB-390 branch fence. A count sheet writes stock moves and a GL journal FOR ITS
// BRANCH, so the caller must be able to reach that branch, at create time, and again at every transition
// against the branch of the LOADED sheet (order/edit.go:365 authorizeEdit is the same shape: resolve the
// row, then check the caller against the row's branch, never against what the request claimed).
//
// A GLOBAL caller reaches every branch. A branch-scoped MANAGER of that branch may approve and post: the
// route guard already decides the tier (create/freeze/count are staff, approve/post are manager); this
// check decides the BRANCH, and nothing here demands GLOBAL scope.
func (s *Service) authorizeBranch(ctx context.Context, branchID string) error {
	caller, ok := identity.StaffContextFromContext(ctx)
	if !ok || caller.UserID == "" {
		return errUnauthorized
	}
	if branchID == "" || !caller.CanAccessBranch(branchID) {
		return errForbidden
	}
	return nil
}

// authorizeSheet loads a sheet and fences the caller against ITS branch. Every transition runs this, so a
// sheet id from another branch is refused before any state is read into a decision.
func (s *Service) authorizeSheet(ctx context.Context, sheetID string) (Sheet, error) {
	sheet, err := s.store().GetSheet(ctx, sheetID)
	if err != nil {
		return Sheet{}, err // not-found stays not-found, no existence leak
	}
	if err := s.authorizeBranch(ctx, sheet.BranchID); err != nil {
		return Sheet{}, err
	}
	return sheet, nil
}

// - sheet lifecycle (create → freeze → count → approve → post) -

// CreateSheet opens a new OPEN count sheet for a branch.
func (s *Service) CreateSheet(ctx context.Context, in CreateSheetRequest) (Sheet, error) {
	if in.BranchID == "" {
		return Sheet{}, badReq("invcount: branch_id is required")
	}
	// DIB-390: a non-empty branch_id was the ONLY check, any active staffer could open a count sheet
	// against any branch, and the sheet's branch is what the post later moves stock and GL for.
	if err := s.authorizeBranch(ctx, in.BranchID); err != nil {
		return Sheet{}, err
	}
	scope := in.Scope
	if scope == "" {
		scope = "FULL"
	}
	return s.store().CreateSheet(ctx, in.BranchID, scope, in.Notes)
}

// FreezeSheet captures the snapshot on-hand basis (weighted-avg valued) for the whole branch onto the sheet's
// lines and flips it to FROZEN. This is the snapshot-freeze the variance is measured against.
func (s *Service) FreezeSheet(ctx context.Context, sheetID string) (Sheet, error) {
	sheet, err := s.authorizeSheet(ctx, sheetID)
	if err != nil {
		return Sheet{}, err
	}
	if sheet.Status != StatusOpen {
		return Sheet{}, clientErr(409, "invcount: only an OPEN sheet can be frozen (sheet is %s)", sheet.Status)
	}
	if s.stock == nil {
		return Sheet{}, clientErr(503, "invcount: inventory snapshot seam not wired")
	}
	snaps, err := s.stock.SnapshotOnHand(ctx, sheet.BranchID)
	if err != nil {
		return Sheet{}, err
	}
	if err := s.store().FreezeSheet(ctx, sheetID, snaps); err != nil {
		return Sheet{}, err
	}
	return s.store().GetSheet(ctx, sheetID)
}

// SubmitCounts records the maker's counted quantities + reasons against a FROZEN sheet, derives each variance,
// and flips the sheet to COUNTED, stamping the caller as the maker (counted_by).
func (s *Service) SubmitCounts(ctx context.Context, sheetID string, in SubmitCountsRequest) (Sheet, error) {
	maker, err := s.caller(ctx)
	if err != nil {
		return Sheet{}, err
	}
	sheet, err := s.authorizeSheet(ctx, sheetID)
	if err != nil {
		return Sheet{}, err
	}
	if sheet.Status != StatusFrozen && sheet.Status != StatusCounted {
		return Sheet{}, errNotFrozen
	}
	if err := s.store().SaveCounts(ctx, sheetID, maker, in.Lines); err != nil {
		return Sheet{}, err
	}
	return s.store().GetSheet(ctx, sheetID)
}

// ApproveSheet records the checker's approval (approved_by/approved_at). The four-eyes rule (checker != maker)
// is enforced here AND re-checked at post time. This is the maker-checker gate.
func (s *Service) ApproveSheet(ctx context.Context, sheetID string) (Sheet, error) {
	checker, err := s.caller(ctx)
	if err != nil {
		return Sheet{}, err
	}
	sheet, err := s.authorizeSheet(ctx, sheetID)
	if err != nil {
		return Sheet{}, err
	}
	if sheet.Status != StatusCounted {
		return Sheet{}, errSheetNotCounted
	}
	if sheet.CountedBy != "" && sheet.CountedBy == checker {
		return Sheet{}, clientErr(422, "invcount: maker-checker violation, the maker cannot approve their own count")
	}
	if err := s.store().ApproveSheet(ctx, sheetID, checker); err != nil {
		return Sheet{}, err
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action: audit.ActHTTPPost, EntityType: "invcount.sheet", EntityID: sheetID,
			ActorID: checker, BranchID: sheet.BranchID, Purpose: "COUNT_APPROVE",
		})
	}
	return s.store().GetSheet(ctx, sheetID)
}

// CancelSheet abandons a sheet before post.
func (s *Service) CancelSheet(ctx context.Context, sheetID string) error {
	sheet, err := s.authorizeSheet(ctx, sheetID)
	if err != nil {
		return err
	}
	if sheet.Status == StatusPosted {
		return clientErr(409, "invcount: a POSTED sheet cannot be cancelled")
	}
	return s.store().CancelSheet(ctx, sheetID)
}

// - reads -

// ListSheets returns count sheets, optionally filtered by branch + status.
func (s *Service) ListSheets(ctx context.Context, branchID, status string) ([]Sheet, error) {
	return s.store().ListSheets(ctx, branchID, status)
}

// SheetDetail returns a sheet, its lines, and the variance summary (the count-variance screen payload).
func (s *Service) SheetDetail(ctx context.Context, sheetID string) (SheetDetail, error) {
	sheet, err := s.store().GetSheet(ctx, sheetID)
	if err != nil {
		return SheetDetail{}, err
	}
	lines, err := s.store().ListLines(ctx, sheetID)
	if err != nil {
		return SheetDetail{}, err
	}
	return SheetDetail{Sheet: sheet, Lines: lines, Summary: summarize(lines)}, nil
}

// ReasonCodes returns the per-reason GL account mapping (the reason picker).
func (s *Service) ReasonCodes(ctx context.Context) ([]ReasonCode, error) {
	return s.store().ReasonCodes(ctx)
}

// NegativeStock returns products carrying by-design negative on-hand at a branch (sell-before-receipt).
func (s *Service) NegativeStock(ctx context.Context, branchID string) ([]NegativeStockRow, error) {
	if s.stock == nil {
		return nil, clientErr(503, "invcount: inventory snapshot seam not wired")
	}
	snaps, err := s.stock.SnapshotOnHand(ctx, branchID)
	if err != nil {
		return nil, err
	}
	out := []NegativeStockRow{}
	for _, sn := range snaps {
		if sn.OnHandBase < 0 {
			out = append(out, NegativeStockRow{
				ProductID: sn.ProductID, LocationID: sn.LocationID,
				OnHandBase: sn.OnHandBase, AvgCostCents: sn.AvgCostCents,
			})
		}
	}
	return out, nil
}

// ListReconRuns returns the recon history for a branch (the recon screen table).
func (s *Service) ListReconRuns(ctx context.Context, branchID string) ([]ReconRun, error) {
	return s.store().ListReconRuns(ctx, branchID)
}

// ReconRunFor returns the recon row for a period (after a lazy run).
func (s *Service) ReconRunFor(ctx context.Context, year, month int, branchID string) (ReconRun, bool, error) {
	return s.store().GetReconRun(ctx, year, month, branchID)
}

// - post + recon core (free functions so unit tests inject fakes for the store + seams) -

// errAlreadyPosted is an internal sentinel: the in-tx lock found the sheet already POSTED (a concurrent/replayed
// post won). The caller treats it as success and still (idempotently) re-posts the GL journal.
var errAlreadyPosted = errors.New("invcount: sheet already posted")

// postCountedSheet posts a COUNTED sheet's variances: it writes the signed COUNT stock-move per line (via the
// inventory seam) and the reason-coded GL journal (reason leg + 1200 leg via ResolveRule), all idempotently.
// Idempotency: the stock-move + line-valuation + status flip commit in one tx guarded by a FOR UPDATE lock on
// the sheet; the GL journal is posted on a stable per-sheet idempotency key that gl.PostExternal dedupes. A
// replay (sheet already POSTED) skips the stock write but still re-issues the GL post (a dedup no-op), so a
// crash between the tx commit and the GL post converges on retry.
func postCountedSheet(ctx context.Context, st countStore, sc StockCounter, gp GLPoster, rr RuleResolver, sheetID string) error {
	sheet, err := st.GetSheet(ctx, sheetID)
	if err != nil {
		return err
	}
	if err := validateMakerChecker(sheet); err != nil {
		return err
	}
	if sc == nil || gp == nil || rr == nil {
		return clientErr(503, "invcount: posting seams (stock/gl/rule) not wired")
	}

	// Write the stock moves + value the lines + flip POSTED atomically (skip if a replay already posted).
	if sheet.Status == StatusCounted {
		err := st.InTx(ctx, func(tx pgx.Tx) error {
			locked, lerr := st.LockSheetTx(ctx, tx, sheetID)
			if lerr != nil {
				return lerr
			}
			if locked.Status == StatusPosted {
				return errAlreadyPosted
			}
			if locked.Status != StatusCounted {
				return errSheetNotCounted
			}
			lines, lerr := st.LinesForPostTx(ctx, tx, sheetID)
			if lerr != nil {
				return lerr
			}
			for _, ln := range lines {
				if ln.VarianceBase == 0 {
					continue
				}
				avg, cerr := sc.WeightedAvgCostCentsTx(ctx, tx, ln.ProductID, sheet.BranchID)
				if cerr != nil {
					return cerr
				}
				val := valueVariance(ln.VarianceBase, avg)
				moveID, merr := sc.PostCountAdjustmentTx(ctx, tx, ln.ProductID, ln.LocationID, sheet.BranchID, ln.VarianceBase, avg, ln.ReasonCode)
				if merr != nil {
					return merr
				}
				if uerr := st.UpdateLinePostedTx(ctx, tx, ln.ID, avg, val, moveID); uerr != nil {
					return uerr
				}
			}
			return st.MarkSheetPostedTx(ctx, tx, sheetID)
		})
		if err != nil && !errors.Is(err, errAlreadyPosted) {
			return err
		}
	}

	// Rebuild the journal from the now-valued lines and post it idempotently (dedup no-op on replay).
	return postSheetGL(ctx, st, rr, sheet, sheetID, gp)
}

// postSheetGL resolves the 1200 leg and the reason accounts, builds the balanced reason-coded journal from the
// persisted (valued) lines, and posts it idempotently.
func postSheetGL(ctx context.Context, st countStore, rr RuleResolver, sheet Sheet, sheetID string, gp GLPoster) error {
	invAcct, err := rr.ResolveRule(ctx, "inventory.adjusted", "inventory", sheet.BranchID)
	if err != nil {
		return err
	}
	rcs, err := st.ReasonCodes(ctx)
	if err != nil {
		return err
	}
	reasonAccounts := make(map[string]string, len(rcs))
	for _, rc := range rcs {
		reasonAccounts[rc.Code] = rc.AccountCode
	}
	lines, err := st.ListLines(ctx, sheetID)
	if err != nil {
		return err
	}
	journal, err := buildAdjustmentJournal(sheetID, sheet.SheetNo, sheet.BranchID, invAcct, reasonAccounts, lines)
	if err != nil {
		return err
	}
	_, err = gp.PostJournal(ctx, journal)
	return err
}

// reconcileMonth runs (or returns) the lazy inventory→GL reconciliation for a period+branch. Run-once: an
// existing run for the period is returned unchanged; otherwise the subledger (Σ weighted-avg on-hand) and the
// GL 1200 balance are computed, the delta + tie status derived, and the row inserted ON CONFLICT DO NOTHING so
// concurrent screen loads converge on one row.
func reconcileMonth(ctx context.Context, st countStore, sc StockCounter, glBal GLBalanceReader, runBy string, year, month int, branchID string) (ReconRun, error) {
	if existing, found, err := st.GetReconRun(ctx, year, month, branchID); err != nil {
		return ReconRun{}, err
	} else if found {
		return existing, nil
	}
	if sc == nil {
		return ReconRun{}, clientErr(503, "invcount: inventory snapshot seam not wired")
	}
	snaps, err := sc.SnapshotOnHand(ctx, branchID)
	if err != nil {
		return ReconRun{}, err
	}
	var subledger int64
	for _, sn := range snaps {
		subledger += valueVariance(sn.OnHandBase, sn.AvgCostCents)
	}
	var gl1200 int64
	if glBal != nil {
		gl1200, err = glBal.GLBalanceCents(ctx, "1200", year, month, branchID)
		if err != nil {
			return ReconRun{}, err
		}
	}
	delta := subledger - gl1200
	status := ReconOutOfTie
	if delta == 0 {
		status = ReconTied
	}
	run := ReconRun{
		BranchID: branchID, FiscalYear: year, Month: month,
		SubledgerCents: subledger, GL1200Cents: gl1200, DeltaCents: delta,
		Status: status, RunBy: runBy,
	}
	return st.InsertReconRun(ctx, run)
}

// currentYearMonth is the recon default period (no scheduler, the screen load drives the current-month run).
func currentYearMonth(now time.Time) (int, int) {
	return now.Year(), int(now.Month())
}
