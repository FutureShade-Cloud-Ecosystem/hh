package billing

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

const (
	WorklistChipAll          = "all"
	WorklistChipActionNeeded = "action_needed"
	WorklistChipOver60       = "over_60"
	WorklistChipCreditHold   = "credit_hold"
)

const (
	WorklistActionRecordPayment = "record_payment"
	WorklistActionReviewPromise = "review_promise"
	WorklistActionReviewHold    = "review_credit_hold"
	WorklistActionApplyCredit   = "apply_credit"
	WorklistActionOpenAccount   = "open_account"
)

const (
	WorklistUrgencyBrokenPromise = "broken_promise"
	WorklistUrgencyOver60        = "over_60"
	WorklistUrgencyPromiseDue    = "promise_due"
	WorklistUrgencyCreditHold    = "credit_hold"
	WorklistUrgencyCredit        = "credit"
	WorklistUrgencyPastDue       = "past_due"
	WorklistUrgencyOpen          = "open"
)

type WorklistAccount struct {
	CustomerID       string
	CustomerName     string
	AccountNo        string
	BranchID         string
	TermsDays        int
	CreditLimitCents int64
}

type WorklistAccountFacts struct {
	Account              WorklistAccount
	UnappliedCreditCents int64
	BrokenPromises       int
	DuePromises          int
	OpenPromises         int
	PromisedCents        int64
	NextPromiseDate      *time.Time
}

type WorklistRow struct {
	CustomerID           string     `json:"customer_id"`
	CustomerName         string     `json:"customer_name"`
	AccountNo            string     `json:"account_no"`
	BranchID             string     `json:"branch_id"`
	OpenARCents          int64      `json:"open_ar_cents"`
	PastDueCents         int64      `json:"past_due_cents"`
	Over60Cents          int64      `json:"over_60_cents"`
	AvailableCreditCents int64      `json:"available_credit_cents"`
	UnappliedCreditCents int64      `json:"unapplied_credit_cents"`
	OldestBucket         string     `json:"oldest_bucket"`
	OldestBucketKey      string     `json:"oldest_bucket_key"`
	BrokenPromises       int        `json:"broken_promises"`
	DuePromises          int        `json:"due_promises"`
	OpenPromises         int        `json:"open_promises"`
	PromisedCents        int64      `json:"promised_cents"`
	NextPromiseDate      *time.Time `json:"next_promise_date,omitempty"`
	OnHold               bool       `json:"on_hold"`
	CreditHoldCandidate  bool       `json:"credit_hold_candidate"`
	ActionNeeded         bool       `json:"action_needed"`
	Urgency              string     `json:"urgency"`
	RecommendedAction    string     `json:"recommended_action"`
}

type WorklistSummary struct {
	TotalPastDueCents     int64 `json:"total_past_due_cents"`
	Over60Cents           int64 `json:"over_60_cents"`
	BrokenPromises        int   `json:"broken_promises"`
	UnappliedCreditsCents int64 `json:"unapplied_credits_cents"`
}

type WorklistCounts struct {
	AllOpen      int `json:"all_open"`
	ActionNeeded int `json:"action_needed"`
	Over60       int `json:"over_60"`
	CreditHold   int `json:"credit_hold"`
}

type WorklistResponse struct {
	Rows    []WorklistRow   `json:"rows"`
	Summary WorklistSummary `json:"summary"`
	Counts  WorklistCounts  `json:"counts"`
	AsOf    string          `json:"as_of"`
}

type WorklistFilter struct {
	Query    string
	Chip     string
	BranchID string
	AsOf     time.Time
}

type ARWorklistStore interface {
	CreditPolicy(ctx context.Context, branchID string) (CreditPolicy, error)
	AgingRowsIn(ctx context.Context, asOf time.Time, bucketDays int, branchID string, reach BranchReach) ([]AgingRow, error)
	WorklistFacts(ctx context.Context, asOf time.Time, branchID string, reach BranchReach) (map[string]WorklistAccountFacts, error)
}

type ARWorklistService struct {
	store ARWorklistStore
	clock func() time.Time
	zone  houseday.Source
}

func NewARWorklistService(store ARWorklistStore) *ARWorklistService {
	return &ARWorklistService{store: store, clock: time.Now}
}

func (s *ARWorklistService) WithClock(now func() time.Time) *ARWorklistService {
	if now != nil {
		s.clock = now
	}
	return s
}

func (s *ARWorklistService) WithZone(src houseday.Source) *ARWorklistService {
	s.zone = src
	return s
}

func (s *ARWorklistService) Worklist(ctx context.Context, f WorklistFilter) (WorklistResponse, error) {
	chip, err := normalizeWorklistChip(f.Chip)
	if err != nil {
		return WorklistResponse{}, err
	}
	zone, err := houseday.Resolve(ctx, s.zone)
	if err != nil {
		return WorklistResponse{}, err
	}
	if f.AsOf.IsZero() {
		f.AsOf = houseClockIn(zone, s.clock())
	}
	reach, err := reachFromContext(ctx).narrow(f.BranchID)
	if err != nil {
		return WorklistResponse{}, err
	}
	policy, err := s.store.CreditPolicy(ctx, f.BranchID)
	if err != nil {
		return WorklistResponse{}, err
	}
	aging, err := s.store.AgingRowsIn(ctx, f.AsOf, policy.AgingBucketDays, f.BranchID, reach)
	if err != nil {
		return WorklistResponse{}, err
	}
	facts, err := s.store.WorklistFacts(ctx, f.AsOf, f.BranchID, reach)
	if err != nil {
		return WorklistResponse{}, err
	}
	all := buildWorklistRows(aging, facts, policy)
	response := WorklistResponse{
		Rows:    filterWorklistRows(all, f.Query, chip),
		Summary: summarizeWorklist(all),
		Counts:  countWorklist(all),
		AsOf:    houseClockIn(zone, f.AsOf).Format("2006-01-02"),
	}
	if response.Rows == nil {
		response.Rows = []WorklistRow{}
	}
	return response, nil
}

func normalizeWorklistChip(chip string) (string, error) {
	chip = strings.ToLower(strings.TrimSpace(chip))
	if chip == "" {
		return WorklistChipAll, nil
	}
	switch chip {
	case WorklistChipAll, WorklistChipActionNeeded, WorklistChipOver60, WorklistChipCreditHold:
		return chip, nil
	default:
		return "", httpx.BadRequestf("a worklist filter must be all, action_needed, over_60, or credit_hold")
	}
}

func buildWorklistRows(aging []AgingRow, facts map[string]WorklistAccountFacts, policy CreditPolicy) []WorklistRow {
	rows := make([]WorklistRow, 0, len(aging)+len(facts))
	seen := make(map[string]bool, len(aging))
	for _, a := range aging {
		seen[a.CustomerID] = true
		fact := facts[a.CustomerID]
		fact.Account = WorklistAccount{
			CustomerID: a.CustomerID, CustomerName: a.CustomerName, AccountNo: a.AccountNo,
			BranchID: a.BranchID, TermsDays: a.TermsDays, CreditLimitCents: a.CreditLimitCents,
		}
		kind, _ := evaluateAccount(a, policy)
		rows = append(rows, makeWorklistRow(a, fact, kind != ""))
	}
	for id, fact := range facts {
		if seen[id] || (fact.UnappliedCreditCents == 0 && fact.BrokenPromises == 0 && fact.DuePromises == 0 && fact.OpenPromises == 0) {
			continue
		}
		rows = append(rows, makeWorklistRow(AgingRow{
			CustomerID: fact.Account.CustomerID, CustomerName: fact.Account.CustomerName,
			AccountNo: fact.Account.AccountNo, BranchID: fact.Account.BranchID,
			TermsDays: fact.Account.TermsDays, CreditLimitCents: fact.Account.CreditLimitCents,
		}, fact, false))
	}
	sort.Slice(rows, func(i, j int) bool {
		left, right := worklistUrgencyRank(rows[i].Urgency), worklistUrgencyRank(rows[j].Urgency)
		if left != right {
			return left < right
		}
		if rows[i].PastDueCents != rows[j].PastDueCents {
			return rows[i].PastDueCents > rows[j].PastDueCents
		}
		if rows[i].Over60Cents != rows[j].Over60Cents {
			return rows[i].Over60Cents > rows[j].Over60Cents
		}
		if rows[i].CustomerName != rows[j].CustomerName {
			return rows[i].CustomerName < rows[j].CustomerName
		}
		return rows[i].CustomerID < rows[j].CustomerID
	})
	return rows
}

func makeWorklistRow(a AgingRow, fact WorklistAccountFacts, creditCandidate bool) WorklistRow {
	oldestLabel, oldestKey := worklistOldestBucket(a)
	available := a.CreditLimitCents - a.TotalCents
	if available < 0 {
		available = 0
	}
	row := WorklistRow{
		CustomerID: a.CustomerID, CustomerName: a.CustomerName, AccountNo: a.AccountNo, BranchID: a.BranchID,
		OpenARCents: a.TotalCents, PastDueCents: a.OverdueCents(),
		Over60Cents: a.Days60To90Cents + a.Days90PlusCents, AvailableCreditCents: available,
		UnappliedCreditCents: fact.UnappliedCreditCents,
		OldestBucket:         oldestLabel, OldestBucketKey: oldestKey,
		BrokenPromises: fact.BrokenPromises, DuePromises: fact.DuePromises, OpenPromises: fact.OpenPromises,
		PromisedCents: fact.PromisedCents, NextPromiseDate: fact.NextPromiseDate,
		OnHold: a.OnHold, CreditHoldCandidate: a.OnHold || creditCandidate,
	}
	row.Urgency = worklistUrgency(row)
	row.ActionNeeded = row.Urgency != WorklistUrgencyOpen
	row.RecommendedAction = worklistRecommendedAction(row.Urgency)
	return row
}

func worklistOldestBucket(a AgingRow) (string, string) {
	switch {
	case a.Days90PlusCents != 0:
		return BucketLabelFor(91)
	case a.Days60To90Cents != 0:
		return BucketLabelFor(61)
	case a.Days60Cents != 0:
		return BucketLabelFor(31)
	case a.Days30Cents != 0:
		return BucketLabelFor(1)
	default:
		return BucketLabelFor(0)
	}
}

func worklistUrgency(row WorklistRow) string {
	switch {
	case row.BrokenPromises > 0:
		return WorklistUrgencyBrokenPromise
	case row.Over60Cents > 0:
		return WorklistUrgencyOver60
	case row.DuePromises > 0:
		return WorklistUrgencyPromiseDue
	case row.CreditHoldCandidate:
		return WorklistUrgencyCreditHold
	case row.UnappliedCreditCents > 0:
		return WorklistUrgencyCredit
	case row.PastDueCents > 0:
		return WorklistUrgencyPastDue
	default:
		return WorklistUrgencyOpen
	}
}

func worklistUrgencyRank(urgency string) int {
	switch urgency {
	case WorklistUrgencyBrokenPromise:
		return 0
	case WorklistUrgencyOver60:
		return 1
	case WorklistUrgencyPromiseDue:
		return 2
	case WorklistUrgencyCreditHold:
		return 3
	case WorklistUrgencyCredit:
		return 4
	case WorklistUrgencyPastDue:
		return 5
	default:
		return 6
	}
}

func worklistRecommendedAction(urgency string) string {
	switch urgency {
	case WorklistUrgencyBrokenPromise, WorklistUrgencyPromiseDue:
		return WorklistActionReviewPromise
	case WorklistUrgencyCreditHold:
		return WorklistActionReviewHold
	case WorklistUrgencyCredit:
		return WorklistActionApplyCredit
	case WorklistUrgencyOver60, WorklistUrgencyPastDue:
		return WorklistActionRecordPayment
	default:
		return WorklistActionOpenAccount
	}
}

func filterWorklistRows(rows []WorklistRow, query, chip string) []WorklistRow {
	query = strings.ToLower(strings.TrimSpace(query))
	out := make([]WorklistRow, 0, len(rows))
	for _, row := range rows {
		if query != "" {
			haystack := strings.ToLower(row.CustomerName + " " + row.AccountNo + " " + row.CustomerID)
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		switch chip {
		case WorklistChipActionNeeded:
			if !row.ActionNeeded {
				continue
			}
		case WorklistChipOver60:
			if row.Over60Cents <= 0 {
				continue
			}
		case WorklistChipCreditHold:
			if !row.CreditHoldCandidate {
				continue
			}
		}
		out = append(out, row)
	}
	return out
}

func summarizeWorklist(rows []WorklistRow) WorklistSummary {
	var out WorklistSummary
	for _, row := range rows {
		out.TotalPastDueCents += row.PastDueCents
		out.Over60Cents += row.Over60Cents
		out.BrokenPromises += row.BrokenPromises
		out.UnappliedCreditsCents += row.UnappliedCreditCents
	}
	return out
}

func countWorklist(rows []WorklistRow) WorklistCounts {
	out := WorklistCounts{AllOpen: len(rows)}
	for _, row := range rows {
		if row.ActionNeeded {
			out.ActionNeeded++
		}
		if row.Over60Cents > 0 {
			out.Over60++
		}
		if row.CreditHoldCandidate {
			out.CreditHold++
		}
	}
	return out
}
