package tax

// service_silentzero_test.go, the silent-zero legs: when no STD_ON_HST_13 row
// is in effect for the date, the engine must REFUSE the assessment, never
// answer a Quote{RateBps: 0} with a nil error. On the defect both fallback
// reads in assessSale discard the found flag, so a deleted, never-seeded or
// fully expired standard row binds a silent zero rate on a sale, a quote, a
// relief assessment and the counter's tax-rate read: every ticket rings
// untaxed while the ledger believes a rate is configured. The legs are
// written against main's tree, so the demand here is the refusal itself (a
// non-nil error where main answers nil); the typed sentinel demand lands with
// the fix. The regression pins beside them hold the two behaviors the refusal
// must not disturb: the nil-repo offline service keeps answering the standard
// rate, and a healthy table keeps answering exactly the seeded shape (the
// values the existing matrix test pins).

import (
	"context"
	"log/slog"
	"testing"
)

// dropStandardRow models every way the standard row goes missing on a live
// table: deleted, never seeded, or every row expired. The fake's ResolveRate
// answers found=false for the reason afterwards.
func dropStandardRow(f *fakeStore) { delete(f.rates, "ON|"+ReasonStdON13) }

// (a) The no-row-for-reason fallback read: the reason's own row is absent and
// the standard row is absent too. On the defect the fallback read discards
// found, rateBps stays 0, and the engine answers Quote{RateBps: 0} with a NIL
// error. The leg demands the refusal.
func TestAssessSale_StandardRowAbsent_RefusesNotSilentZero(t *testing.T) {
	f := newFakeStore()
	dropStandardRow(f)
	svc, _ := newSvc(f)
	q, err := svc.AssessSale(context.Background(), SaleAssessment{SubtotalCents: 100_000})
	if err == nil {
		t.Fatalf("no standard rate row in effect: the engine must refuse, not bind a silent zero: got Quote{RateBps: %d, TaxCents: %d} with a nil error", q.RateBps, q.TaxCents)
	}
	if q.RateBps != 0 || q.TaxCents != 0 {
		t.Errorf("a refused assessment must carry no quote values, got %+v", q)
	}
}

// (b) The evidence gate's standard fallback read: an evidence-gated reason
// with no evidence attached and the standard row absent. On the defect the
// gate's read discards found and binds 0 as "the standard", still with a nil
// error. The leg demands the same refusal.
func TestAssessSale_EvidenceGateStandardRowAbsent_RefusesNotSilentZero(t *testing.T) {
	f := newFakeStore()
	dropStandardRow(f)
	svc, _ := newSvc(f)
	q, err := svc.AssessSale(context.Background(), SaleAssessment{
		SubtotalCents: 100_000, ReasonCode: ReasonONFNPos8, EvidenceAttached: false,
	})
	if err == nil {
		t.Fatalf("no standard rate row in effect under the relief gate: the engine must refuse, not bind 0 as the standard: got Quote{RateBps: %d, TaxCents: %d} with a nil error", q.RateBps, q.TaxCents)
	}
	if q.RateBps != 0 || q.TaxCents != 0 {
		t.Errorf("a refused assessment must carry no quote values, got %+v", q)
	}
}

// (c1) REGRESSION PIN: the nil-repo offline path is UNCHANGED. A scaffold
// service with no repository answers the standard rate offline (stdRateBps)
// for the standard and manual reasons, with no error, so scaffold and
// database-free tests keep working.
func TestAssessSale_OfflineNilRepo_StillAnswersStandard(t *testing.T) {
	svc := &Service{repo: nil, log: slog.Default()}
	for _, reason := range []string{"", ReasonStdON13, ReasonManual} {
		q, err := svc.AssessSale(context.Background(), SaleAssessment{
			SubtotalCents: 100_000, ReasonCode: reason,
		})
		if err != nil {
			t.Fatalf("offline assess %q: %v", reason, err)
		}
		if q.RateBps != stdRateBps || q.TaxCents != 13_000 {
			t.Errorf("offline assess %q: got rate=%d tax=%d, want %d/13000", reason, q.RateBps, q.TaxCents, stdRateBps)
		}
	}
}

// (c2) REGRESSION PIN: a healthy table answers exactly the seeded shape, the
// same values the existing matrix test pins, so the refusal changes nothing
// for a configured yard.
func TestAssessSale_HealthyTable_SameValuesAsBefore(t *testing.T) {
	f := newFakeStore()
	svc, _ := newSvc(f)
	const sub = 100_000
	tests := []struct {
		name       string
		reason     string
		evidence   bool
		wantRate   int
		wantTax    int64
		wantCredit int64
	}{
		{"standard", ReasonStdON13, false, 1300, 13_000, 0},
		{"ontario POS with evidence", ReasonONFNPos8, true, 500, 5_000, 8_000},
		{"ontario POS without evidence", ReasonONFNPos8, false, 1300, 13_000, 0},
		{"fngst with evidence", ReasonFNGST5, true, 500, 5_000, 0},
		{"fngst without evidence", ReasonFNGST5, false, 1300, 13_000, 0},
		{"vendor delivery with evidence", ReasonFedVendDel, true, 0, 0, 0},
		{"vendor delivery without evidence", ReasonFedVendDel, false, 1300, 13_000, 0},
		{"manual review", ReasonManual, false, 1300, 13_000, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q, err := svc.AssessSale(context.Background(), SaleAssessment{
				SubtotalCents: sub, ReasonCode: tc.reason, EvidenceAttached: tc.evidence,
			})
			if err != nil {
				t.Fatalf("healthy table assess %s: %v", tc.name, err)
			}
			if q.RateBps != tc.wantRate || q.TaxCents != tc.wantTax || q.OnCreditCents != tc.wantCredit {
				t.Errorf("%s: got rate=%d tax=%d credit=%d, want %d/%d/%d",
					tc.name, q.RateBps, q.TaxCents, q.OnCreditCents, tc.wantRate, tc.wantTax, tc.wantCredit)
			}
		})
	}
}
