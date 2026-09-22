package warranty

import (
	"context"
	"sort"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// reporting.go, the FR-7 HEADLINE reports ("we have a real gut feel but no metrics", 2026-07-07 sync). The
// aggregation math is PURE (buildReport takes []ReportClaim + asOf) so it is exhaustively unit-testable without
// a database; Reports() is the thin service entry that loads the projection and calls it.

// Reports computes the whole FR-7 metrics bundle for a branch ("" = all branches the caller can reach).
//
// AUTHZ: the FR-7 dashboard aggregates money (exposure, realized recovery, win/loss), a branch-scoped caller
// must not see another branch's figures. A GLOBAL caller sees everything; a scoped caller either gets the one
// branch they asked for (gated) or, with no branch filter, only their own branches' claims. The scope is
// applied to the projection BEFORE the pure math runs, so the reported totals never include an unreachable
// branch's claims (mirrors ListClaims + the order/till horizontal-access boundary).
func (s *Service) Reports(ctx context.Context, branchID string) (ReportBundle, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return ReportBundle{}, err
	}
	if branchID != "" && !caller.CanAccessBranch(branchID) {
		return ReportBundle{}, forbidden("warranty: caller cannot access branch %s", branchID)
	}
	claims, err := s.store.AllClaimsForReport(ctx, branchID)
	if err != nil {
		return ReportBundle{}, err
	}
	if caller.Scope != identity.ScopeGlobal {
		kept := claims[:0]
		for _, c := range claims {
			if caller.CanAccessBranch(c.BranchID) {
				kept = append(kept, c)
			}
		}
		claims = kept
	}
	return buildReport(claims, s.clock()), nil
}

// buildReport is the pure FR-7 aggregation: by-manufacturer frequency + win/loss, funnel counts per phase,
// time-to-settlement, $ exposure by vendor, and open claims by phase/vendor/age. Kept DB-free + deterministic.
func buildReport(claims []ReportClaim, asOf time.Time) ReportBundle {
	bundle := ReportBundle{
		Funnel:          buildFunnel(claims),
		ByManufacturer:  buildByManufacturer(claims),
		Exposure:        buildExposure(claims),
		OpenClaimsByAge: buildOpenAges(claims, asOf),
	}
	bundle.TimeToSettlement = buildTimeToSettlement(claims)
	for _, oc := range bundle.OpenClaimsByAge {
		bundle.TotalExposureCents += oc.ExpectedCents
	}
	bundle.TotalOpen = len(bundle.OpenClaimsByAge)
	return bundle
}

// buildFunnel counts claims currently in each phase (FR-7 funnel counts per phase). Every column in the
// confirmed board is present (0 when empty) so the funnel renders stably.
func buildFunnel(claims []ReportClaim) []FunnelCount {
	counts := map[string]int{}
	for _, c := range claims {
		counts[c.Phase]++
	}
	cols := KanbanColumns()
	out := make([]FunnelCount, 0, len(cols))
	for _, ph := range cols {
		out = append(out, FunnelCount{Phase: ph, Count: counts[ph]})
	}
	return out
}

// buildByManufacturer rolls claims up per vendor: total/open counts, win/loss (FR-7 win/loss ratio "by
// manufacturer, how often"), and expected/realized dollars. A claim with no vendor (UNKNOWN pending triage) is
// bucketed under a synthetic "UNKNOWN" vendor so it is still counted.
func buildByManufacturer(claims []ReportClaim) []ManufacturerFrequency {
	type acc struct {
		name                   string
		total, open, won, lost int
		expected, realized     int64
	}
	byV := map[string]*acc{}
	for _, c := range claims {
		key := c.VendorID
		name := c.VendorName
		if key == "" {
			key = "UNKNOWN"
			name = "UNKNOWN (pending triage)"
		}
		a := byV[key]
		if a == nil {
			a = &acc{name: name}
			byV[key] = a
		}
		a.total++
		a.expected += c.ExpectedCents
		// MONEY-CRITICAL: realized recovery must be consistent with the win/loss classification.
		// classify() is the single source of truth for whether a claim recovered value; a claim in a
		// terminal-LOST phase (REJECTED/ABANDONED), or CLOSED with a non-recovery/absent outcome, is NOT a
		// win, so any realized_cents it still carries is NOT recovered money and must not inflate the vendor's
		// realized total (else recovery gets attributed to vendors with ~zero wins). Only DispositionWon claims
		// contribute realized recovery, exactly mirroring buildTimeToSettlement's "count a WIN" rule.
		switch classify(c.Phase, c.Outcome) {
		case DispositionOpen:
			a.open++
		case DispositionWon:
			a.won++
			a.realized += c.RealizedCents
		case DispositionLost:
			a.lost++
		}
	}
	out := make([]ManufacturerFrequency, 0, len(byV))
	for id, a := range byV {
		var winRate float64
		if decided := a.won + a.lost; decided > 0 {
			winRate = float64(a.won) / float64(decided)
		}
		out = append(out, ManufacturerFrequency{
			VendorID: id, VendorName: a.name,
			TotalClaims: a.total, OpenClaims: a.open, WonClaims: a.won, LostClaims: a.lost,
			WinRate: winRate, ExpectedCents: a.expected, RealizedCents: a.realized,
		})
	}
	// Most-claimed first (the "which manufacturers more than others" question), then by name for stability.
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalClaims != out[j].TotalClaims {
			return out[i].TotalClaims > out[j].TotalClaims
		}
		return out[i].VendorName < out[j].VendorName
	})
	return out
}

// buildTimeToSettlement summarizes the open→close cycle for SETTLED (won) claims (FR-7 time-to-settlement). A
// claim counts when it reached a CLOSED phase with a recovery outcome and carries both opened_at + closed_at -
// so rejected/abandoned losses don't skew "how long a WIN takes". Days are whole calendar days.
func buildTimeToSettlement(claims []ReportClaim) TimeToSettlement {
	var days []int
	for _, c := range claims {
		if classify(c.Phase, c.Outcome) != DispositionWon || c.ClosedAt == nil {
			continue
		}
		d := wholeDays(c.OpenedAt, *c.ClosedAt)
		days = append(days, d)
	}
	res := TimeToSettlement{SettledCount: len(days)}
	if len(days) == 0 {
		return res
	}
	sort.Ints(days)
	res.MinDays = days[0]
	res.MaxDays = days[len(days)-1]
	var sum int
	for _, d := range days {
		sum += d
	}
	res.AvgDays = float64(sum) / float64(len(days))
	res.MedianDays = median(days)
	return res
}

// buildExposure aggregates open-recovery exposure per vendor (FR-7 $ exposure by vendor = Σ expected_cents on
// OPEN claims). Only claims still in flight contribute (a closed claim is no longer exposure).
func buildExposure(claims []ReportClaim) []ExposureByVendor {
	type acc struct {
		name  string
		open  int
		cents int64
	}
	byV := map[string]*acc{}
	for _, c := range claims {
		if classify(c.Phase, c.Outcome) != DispositionOpen {
			continue
		}
		key := c.VendorID
		name := c.VendorName
		if key == "" {
			key = "UNKNOWN"
			name = "UNKNOWN (pending triage)"
		}
		a := byV[key]
		if a == nil {
			a = &acc{name: name}
			byV[key] = a
		}
		a.open++
		a.cents += c.ExpectedCents
	}
	out := make([]ExposureByVendor, 0, len(byV))
	for id, a := range byV {
		out = append(out, ExposureByVendor{VendorID: id, VendorName: a.name, OpenClaims: a.open, ExposureCents: a.cents})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ExposureCents != out[j].ExposureCents {
			return out[i].ExposureCents > out[j].ExposureCents // biggest exposure first
		}
		return out[i].VendorName < out[j].VendorName
	})
	return out
}

// buildOpenAges lists every OPEN claim aged from opened_at (FR-7 open claims by phase/vendor/age). Oldest first.
func buildOpenAges(claims []ReportClaim, asOf time.Time) []OpenClaimAge {
	var out []OpenClaimAge
	for _, c := range claims {
		if classify(c.Phase, c.Outcome) != DispositionOpen {
			continue
		}
		age := wholeDays(c.OpenedAt, asOf)
		out = append(out, OpenClaimAge{
			ClaimID: c.ID, ClaimNo: c.ClaimNo, Phase: c.Phase,
			VendorID: c.VendorID, VendorName: c.VendorName,
			AgeDays: age, Bucket: ageBucket(age), ExpectedCents: c.ExpectedCents,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AgeDays != out[j].AgeDays {
			return out[i].AgeDays > out[j].AgeDays
		}
		return out[i].ClaimNo < out[j].ClaimNo
	})
	return out
}

// - numeric helpers -

// wholeDays returns whole calendar days from t to asOf (never negative).
func wholeDays(t, asOf time.Time) int {
	d := int(asOf.Sub(t).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}

// median of a SORTED int slice (average of the two middle values when even).
func median(sorted []int) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return float64(sorted[n/2])
	}
	return float64(sorted[n/2-1]+sorted[n/2]) / 2
}
