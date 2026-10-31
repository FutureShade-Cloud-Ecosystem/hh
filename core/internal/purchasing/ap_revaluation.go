package purchasing

// ap_revaluation.go, the month end revaluation of open USD AP (W3 FX-2): every open USD
// bill's open document amount (billDocOpen) is taken at the run's month end rate and held
// against the CAD the bill carries in 2100, and ONE journal per run moves 2100 by the
// total difference against the FX account. The entry is temporary bookkeeping: it is
// reversed on the next house day, so the pair nets to zero on every account and a payment
// made after the reversal still posts its realised FX against the bill's own rate,
// exactly as PayBill always has.
//
// THE ACCOUNT. The unrealised leg posts to the FX account the chart already carries, 6310
// Realised Foreign Exchange Gain or Loss, resolved through the rule seam under the
// fx_unrealised role (a rule that does not exist yet falls back to 6310). The chart has no
// unrealised FX account and adding one is a migration (gl_accounts plus a posting rule),
// which this item carries none of; the line memos name the revaluation ("Unrealised FX
// <month>") so the account's ledger reads which lines are the temporary month end entry,
// and the reversal swaps them back out the next house day. The day a chart gains a
// dedicated unrealised account, a posting rule row moves this leg with no code change.
//
// THE RUN STATE LIVES ON ITS JOURNAL. No table is added. The run's idempotency key
// (purchasing.ap.revaluation:<branch>:<month>) names the journal on the GL, so a re-press
// is judged by reading that journal back through the GLJournalReader seam: the same key
// replays the same run, and a press whose rate would post different legs is a conflict in
// words, the same judgement PayBill makes under its own key.

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/jackc/pgx/v5"
)

const (
	// glSourceAdjustment is the journal source a revaluation files under: a bookkeeping
	// adjustment, not a payment (PAYMENT is the bill payments' own source and the aging's
	// payment clock reads it by ref, so a revaluation must never wear it).
	glSourceAdjustment = "ADJUSTMENT"
	// roleFXUnrealised is the posting rule role the revaluation's FX leg resolves through,
	// distinct from the realised leg's fx_gain_loss so a dealer (or a later migration) can
	// point the unrealised entry at its own account without moving the realised one.
	roleFXUnrealised = "fx_unrealised"
	// apRevalKeyPrefix is the GL journal key namespace of the revaluation run, one key per
	// branch and house month. The reversal rides the same key with apRevalReverseSuffix.
	apRevalKeyPrefix     = "purchasing.ap.revaluation:"
	apRevalReverseSuffix = ":reverse"
)

// GLJournalLeg is one leg of a posted journal, by value, so purchasing does not import gl's
// internals.
type GLJournalLeg struct {
	AccountCode string
	DebitCents  int64
	CreditCents int64
	Memo        string
}

// GLPostedJournal is a POSTED journal read back by its idempotency key: its own id, the
// source ref it was filed against, its branch, its memo and its legs in line order.
type GLPostedJournal struct {
	ID          string
	SourceRefID string
	BranchID    string
	Memo        string
	Lines       []GLJournalLeg
}

// GLJournalReader is the read seam over a posted journal the module itself filed, by its
// idempotency key. The revaluation run judges a re-press through it and the reversal reads
// the run's own legs to swap; nil means the run's replay judgement cannot run (the GL's
// key uniqueness still refuses a second journal under one key) and the reversal refuses in
// words, because its legs cannot be read.
type GLJournalReader interface {
	JournalByKey(ctx context.Context, key string) (GLPostedJournal, bool, error)
}

// WithGLJournalReader binds the posted journal read seam.
func (s *Service) WithGLJournalReader(r GLJournalReader) *Service {
	s.journalReader = r
	return s
}

// RevalueAPRequest names the house month being revalued and the month end rate (CAD per
// USD) every open USD bill's document amount is taken at.
type RevalueAPRequest struct {
	BranchID string  `json:"branch_id"`
	Month    string  `json:"month"`
	FXRate   float64 `json:"fx_rate"`
}

// RevalueAPRun is the run's answer: what was revalued, at what rate, and the journal that
// carries it. Posted is false when the books needed no entry (the open document amount at
// the month end rate already equals the CAD carried in 2100), which is a complete run, not
// a refusal. The run's journal is two lines and the FIRST is always the 2100 leg, the
// order the reversal's answer reads the difference back with.
type RevalueAPRun struct {
	BranchID         string  `json:"branch_id"`
	Month            string  `json:"month"`
	MonthEnd         string  `json:"month_end"`
	FXRate           float64 `json:"fx_rate"`
	Bills            int     `json:"bills"`
	DocOpenCents     int64   `json:"doc_open_cents"`
	CarriedCadCents  int64   `json:"carried_cad_cents"`
	RevaluedCadCents int64   `json:"revalued_cad_cents"`
	DifferenceCents  int64   `json:"difference_cents"`
	Posted           bool    `json:"posted"`
	JournalID        string  `json:"journal_id,omitempty"`
}

// apRevalKey is the run's one key per branch and house month; the reversal rides it with
// the reverse suffix.
func apRevalKey(branchID, month string) string { return apRevalKeyPrefix + branchID + ":" + month }

// parseAPMonth reads a strict YYYY-MM house month and answers its year and month.
func parseAPMonth(month string) (int, int, error) {
	month = strings.TrimSpace(month)
	if len(month) != 7 || month[4] != '-' {
		return 0, 0, badReq("purchasing: month must be a YYYY-MM house month")
	}
	y, err := strconv.Atoi(month[:4])
	if err != nil || y < 1 {
		return 0, 0, badReq("purchasing: month must be a YYYY-MM house month")
	}
	m, err := strconv.Atoi(month[5:])
	if err != nil || m < 1 || m > 12 {
		return 0, 0, badReq("purchasing: month must be a YYYY-MM house month")
	}
	return y, m, nil
}

// apMonthBounds answers the month's first instant (its first house midnight) and its last
// instant (the last nanosecond of its last house day, the instant a month end read counts
// as of), both in the dealer's own zone. A month end computed at Greenwich midnight would
// sit inside the dealer's previous evening, filing a month end journal into the month
// before it.
func apMonthBounds(year, month int, loc *time.Location) (start, end time.Time) {
	if loc == nil {
		loc = time.UTC
	}
	start = time.Date(year, time.Month(month), 1, 0, 0, 0, 0, loc)
	firstOfNext := start.AddDate(0, 1, 0)
	lastDay := firstOfNext.AddDate(0, 0, -1)
	end = time.Date(lastDay.Year(), lastDay.Month(), lastDay.Day(), 23, 59, 59, 999999999, loc)
	return start, end
}

// RevalueAP runs the month end revaluation of one branch's open USD bills: their open
// document amount (billDocOpen, the aging's own remaining amount) at the run's rate
// against the CAD each carries in 2100, one journal for the total difference. Idempotent
// per branch and month by its key: a re-press replays the posted run, and a press whose
// rate would post different legs is refused in words.
func (s *Service) RevalueAP(ctx context.Context, in RevalueAPRequest) (RevalueAPRun, error) {
	_, branchID, err := s.branchFence(ctx, in.BranchID)
	if err != nil {
		return RevalueAPRun{}, err
	}
	if branchID == "" {
		return RevalueAPRun{}, badReq("purchasing: branch_id is required, a revaluation is one branch's books")
	}
	year, month, err := parseAPMonth(in.Month)
	if err != nil {
		return RevalueAPRun{}, err
	}
	monthWords := fmt.Sprintf("%04d-%02d", year, month)
	// The rate's shape is judged before anything is read, in words, like a payment rate's.
	if math.IsNaN(in.FXRate) || math.IsInf(in.FXRate, 0) || in.FXRate <= 0 {
		return RevalueAPRun{}, badReq("purchasing: fx_rate is required and must be positive on a revaluation run")
	}
	if dec := rateDecimals(in.FXRate); dec > maxRateDecimals {
		return RevalueAPRun{}, badReq("purchasing: fx_rate has %d decimals; at most %d are kept, so enter it with %d or fewer", dec, maxRateDecimals, maxRateDecimals)
	}
	// The dealer's day, decided BEFORE the transaction opens (a zone read must not hold a
	// connection beside an open transaction).
	zone, err := houseday.Resolve(ctx, s.zone)
	if err != nil {
		return RevalueAPRun{}, err
	}
	loc := zone.Location()
	monthStart, monthEnd := apMonthBounds(year, month, loc)
	if s.clock().In(loc).Before(monthStart) {
		return RevalueAPRun{}, unprocessable("purchasing: month %s has not begun yet on the dealer's day %s; a month is revalued at its own end",
			monthWords, zone.Today(s.now))
	}

	key := apRevalKey(branchID, monthWords)

	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return RevalueAPRun{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// One key is one run: two presses under the same key serialize here, so the second is
	// judged against the first's journal, never a second journal (the GL's key index is the
	// belt under this lock).
	if err := s.repo.LockPaymentKeyTx(ctx, tx, key); err != nil {
		return RevalueAPRun{}, err
	}

	// The replay judgement runs before any refusal that reads the books, so a re-press is
	// still answered as a replay. The journal's legs decide: the same rate on the same
	// books recomputes the same legs (through the same rule resolution the posting used),
	// and different legs mean a different rate (or books a late filed document moved under
	// the closed month), either way not this key's run.
	if s.journalReader != nil {
		posted, found, err := s.journalReader.JournalByKey(ctx, key)
		if err != nil {
			return RevalueAPRun{}, err
		}
		if found {
			if posted.SourceRefID != branchID {
				return RevalueAPRun{}, conflict("purchasing: idempotency key %s already names another run's journal", key)
			}
			run, legs, cerr := s.revalueAPCompute(ctx, branchID, monthEnd, loc, monthWords, in.FXRate)
			if cerr == nil {
				s.resolveRevaluationLegsTx(ctx, tx, branchID, legs)
			}
			if cerr != nil || !sameJournalLegs(legs, posted.Lines) {
				return RevalueAPRun{}, conflict("purchasing: month %s in branch %s is already revalued under this key (the posted journal moved %d cents); the pressed fx_rate %v would not retrace it; one key is one run",
					monthWords, branchID, postedLegsDifference(posted.Lines), in.FXRate)
			}
			run.JournalID = posted.ID
			run.Posted = true
			return run, nil
		}
	}

	run, legs, err := s.revalueAPCompute(ctx, branchID, monthEnd, loc, monthWords, in.FXRate)
	if err != nil {
		return RevalueAPRun{}, err
	}
	s.resolveRevaluationLegsTx(ctx, tx, branchID, legs)

	if run.DifferenceCents == 0 {
		// Nothing to book: the open document amount at the month end rate already equals
		// the CAD carried in 2100. The run is complete and posts no journal (a zero leg is
		// not a leg the GL can carry).
		if err := tx.Commit(ctx); err != nil {
			return RevalueAPRun{}, fmt.Errorf("purchasing: commit revaluation run: %w", err)
		}
		s.log.Info("purchasing: AP revaluation posted nothing", "branch_id", branchID, "month", monthWords)
		s.record(ctx, "purchasing.ap_revaluation.run", branchID, map[string]any{
			"month": monthWords, "bills": run.Bills, "difference_cents": int64(0), "posted": false,
		})
		return run, nil
	}

	journalID, err := s.postRevaluationJournalTx(ctx, tx, branchID, monthWords, monthEnd, in.FXRate, legs)
	if err != nil {
		return RevalueAPRun{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RevalueAPRun{}, fmt.Errorf("purchasing: commit revaluation run: %w", err)
	}
	run.Posted = true
	run.JournalID = journalID
	s.log.Info("purchasing: AP revaluation posted", "branch_id", branchID, "month", monthWords,
		"bills", run.Bills, "difference_cents", run.DifferenceCents, "journal_id", journalID)
	s.record(ctx, "purchasing.ap_revaluation.run", branchID, map[string]any{
		"month": monthWords, "bills": run.Bills, "difference_cents": run.DifferenceCents, "posted": true,
	})
	return run, nil
}

// revalueAPCompute reads the branch's open bills as of the month end instant and answers
// the run's figures for its USD bills, plus the journal legs the difference posts. The
// read is the aging's own (OpenBillsAsOf, the journal clock, the credits' clock and
// billDocOpen), so the run's per bill figures and the aging's agree by construction.
func (s *Service) revalueAPCompute(ctx context.Context, branchID string, monthEnd time.Time, loc *time.Location, monthWords string, rate float64) (RevalueAPRun, []GLJournalLeg, error) {
	run := RevalueAPRun{
		BranchID: branchID,
		Month:    monthWords,
		MonthEnd: monthEnd.In(loc).Format("2006-01-02"),
		FXRate:   rate,
	}
	reach := BranchReach{BranchIDs: []string{branchID}}

	candidates, err := s.repo.OpenBillsAsOf(ctx, reach)
	if err != nil {
		return RevalueAPRun{}, nil, err
	}
	candidates, err = s.applyJournalClock(ctx, candidates, monthEnd, loc)
	if err != nil {
		return RevalueAPRun{}, nil, err
	}
	countedBills := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		countedBills[c.BillID] = true
	}
	creditCandidates, err := s.repo.OpenVendorCreditsAsOf(ctx, reach)
	if err != nil {
		return RevalueAPRun{}, nil, err
	}
	_, appliedByBill, err := s.applyCreditClock(ctx, creditCandidates, monthEnd, loc, countedBills)
	if err != nil {
		return RevalueAPRun{}, nil, err
	}

	rateMicros := int64(math.Round(rate * 1_000_000))
	for _, c := range candidates {
		if !strings.EqualFold(strings.TrimSpace(c.Currency), "USD") {
			continue
		}
		openCad := c.TotalCents - c.PaidCents - appliedByBill[c.BillID]
		if openCad <= 0 {
			continue
		}
		// A payment rate is bounded against its bill's own rate so a typed 138 for 1.38 is
		// refused rather than paid out a hundredfold; the run's rate is bounded against
		// every bill it revalues, the same band, refusing the whole run and naming the
		// bill so the odd rate reads as the bill's, not the run's.
		if c.FXRate > 0 && (rate > c.FXRate*maxPayRateFactor || rate < c.FXRate/maxPayRateFactor) {
			return RevalueAPRun{}, nil, badReq("purchasing: fx_rate %v is more than %vx away from bill %s's own rate %v; check it was typed right",
				rate, maxPayRateFactor, c.BillNo, c.FXRate)
		}
		docOpen := billDocOpen(c.TotalCents, c.DocTotalCents, c.PaidDocCents, appliedByBill[c.BillID], openCad)
		revalued := proRataHalfToEven(docOpen, rateMicros, 1_000_000)
		run.Bills++
		run.DocOpenCents += docOpen
		run.CarriedCadCents += openCad
		run.RevaluedCadCents += revalued
		run.DifferenceCents += revalued - openCad
	}
	if run.Bills == 0 {
		return RevalueAPRun{}, nil, unprocessable("purchasing: no open USD bills in branch %s at %s month end, there is nothing to revalue",
			branchID, run.MonthEnd)
	}
	return run, revaluationLegs(run.DifferenceCents, monthWords), nil
}

// revaluationLegs builds the run's two journal legs: the 2100 move first, the FX leg
// against it. A positive difference is a liability increase (the month end rate exceeds
// what the bills carry), so 2100 is credited and the unrealised loss debited; a negative
// one debits 2100 and credits the unrealised gain.
func revaluationLegs(differenceCents int64, monthWords string) []GLJournalLeg {
	apLeg := GLJournalLeg{AccountCode: glAccountAPCode, CreditCents: differenceCents, Memo: "AP revaluation " + monthWords}
	fxLeg := GLJournalLeg{AccountCode: glAccountFXGainLoss, DebitCents: differenceCents, Memo: "Unrealised FX " + monthWords}
	if differenceCents < 0 {
		apLeg = GLJournalLeg{AccountCode: glAccountAPCode, DebitCents: -differenceCents, Memo: "AP revaluation " + monthWords}
		fxLeg = GLJournalLeg{AccountCode: glAccountFXGainLoss, CreditCents: -differenceCents, Memo: "Unrealised FX " + monthWords}
	}
	return []GLJournalLeg{apLeg, fxLeg}
}

// resolveRevaluationLegsTx points the run's legs at the posting rules' accounts exactly as
// PayBill resolves its own legs (a dealer's override moves with the rule, the chart's own
// code the fallback). The resolution happens ONCE, before the posting and before the
// replay's legs comparison, so the legs a re-press recomputes are the legs the posted
// journal carries whatever the rules say.
func (s *Service) resolveRevaluationLegsTx(ctx context.Context, tx pgx.Tx, branchID string, legs []GLJournalLeg) {
	if s.rules == nil || len(legs) < 2 {
		return
	}
	if resolved, rErr := s.rules.ResolveRuleTx(ctx, tx, eventBillPosted, roleAP, branchID); rErr == nil && strings.TrimSpace(resolved) != "" {
		legs[0].AccountCode = resolved
	}
	if resolved, rErr := s.rules.ResolveRuleTx(ctx, tx, eventBillPosted, roleFXUnrealised, branchID); rErr == nil && strings.TrimSpace(resolved) != "" {
		legs[1].AccountCode = resolved
	}
}

// postRevaluationJournalTx posts the run's one journal through the GL seam inside the
// run's transaction. The journal is dated the month end instant in the dealer's zone, so
// it files in the month it revalues, or the earliest open month when that month is closed,
// late posted, never refused.
func (s *Service) postRevaluationJournalTx(ctx context.Context, tx pgx.Tx, branchID, monthWords string, monthEnd time.Time, rate float64, legs []GLJournalLeg) (string, error) {
	if s.gl == nil {
		return "", ErrGLSeamUnwired
	}
	glLines := make([]GLLine, len(legs))
	for i, l := range legs {
		glLines[i] = GLLine{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo}
	}
	journalID, err := s.gl.PostJournalTx(ctx, tx, GLJournal{
		Source:         glSourceAdjustment,
		SourceRefID:    branchID,
		IdempotencyKey: apRevalKey(branchID, monthWords),
		Memo:           "AP revaluation " + monthWords + " at " + strconv.FormatFloat(rate, 'f', -1, 64),
		BranchID:       branchID,
		DocumentDate:   monthEnd,
		Lines:          glLines,
	})
	if err != nil {
		return "", fmt.Errorf("purchasing: post AP revaluation journal: %w", err)
	}
	return journalID, nil
}

// sameJournalLegs answers whether the legs the books would post now are the legs the
// posted journal carries, in line order (the run's first line is always the 2100 leg).
func sameJournalLegs(want, posted []GLJournalLeg) bool {
	if len(want) != len(posted) {
		return false
	}
	for i := range want {
		if want[i].AccountCode != posted[i].AccountCode ||
			want[i].DebitCents != posted[i].DebitCents ||
			want[i].CreditCents != posted[i].CreditCents {
			return false
		}
	}
	return true
}

// postedLegsDifference answers the run journal's 2100 move from its legs: the first leg's
// credit less its debit (a two line journal, so the FX leg carries the same figure the
// other way). It is only used for the conflict words.
func postedLegsDifference(legs []GLJournalLeg) int64 {
	if len(legs) == 0 {
		return 0
	}
	return legs[0].CreditCents - legs[0].DebitCents
}
