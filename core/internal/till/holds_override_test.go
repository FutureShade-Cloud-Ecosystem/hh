package till

// holds_override_test.go, JA-3 fix item 5: an approved override waives a limit, and it says WHICH
// one (the job's own or the customer's) in its words and in its data. No door passes an approved
// override today; the leg pins the gate so the first door that does cannot waive a limit silently.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/jackc/pgx/v5"
)

type overrideHolds struct{}

func (overrideHolds) HasActiveHold(context.Context, string) (bool, error) { return false, nil }

type overrideExposure struct {
	custExposure, jobExposure int64
	ownLimit                  *int64
	jobErr                    error
}

func (f overrideExposure) CustomerExposure(context.Context, pgx.Tx, string) (int64, error) {
	return f.custExposure, nil
}

func (f overrideExposure) JobPosition(context.Context, pgx.Tx, string, string) (*int64, int64, int64, error) {
	return f.ownLimit, f.jobExposure, 0, f.jobErr
}

func TestOverriddenLimit_SaysWhichLimitTheOverrideWaives(t *testing.T) {
	own := int64(100000)
	g := NewHoldAwareCreditGate(overrideHolds{}).WithExposure(overrideExposure{custExposure: 500000, jobExposure: 90000, ownLimit: &own})
	jobCtx := WithSaleJob(context.Background(), "job-1")

	// the override lets the sale through ...
	if b := g.Check(jobCtx, "cust", 20000, 0, 1000000, true, "br", true); b != nil {
		t.Fatalf("an approved override still blocked: %v", b)
	}
	// ... and names the job's own limit when that is the one it waives
	n := g.OverriddenLimit(jobCtx, "cust", 20000, 0, 1000000, true, "br")
	if n == nil || n.Data["limit_scope"] != LimitScopeJob || n.Data["overridden"] != "true" {
		t.Fatalf("the override note = %+v, want the job scope and overridden=true", n)
	}
	if !strings.Contains(n.Detail, "approved override waives") || !strings.Contains(n.Detail, "the job's own credit limit") {
		t.Fatalf("the override words = %q, want them to say the job's own limit is waived", n.Detail)
	}

	// the customer's limit, with no job
	n = g.OverriddenLimit(context.Background(), "cust", 600000, 0, 1000000, true, "br")
	if n == nil || n.Data["limit_scope"] != LimitScopeCustomer || n.Data["credit_limit"] != "$10,000.00" {
		t.Fatalf("the override note = %+v, want the customer scope", n)
	}
	if !strings.Contains(n.Detail, "the customer's credit limit") {
		t.Fatalf("the override words = %q, want the customer's limit named", n.Detail)
	}

	// nothing waived when no limit would have fired
	if n := g.OverriddenLimit(jobCtx, "cust", 1000, 0, 1000000, true, "br"); n != nil {
		t.Fatalf("a sale inside both limits reported an override: %+v", n)
	}
}

func TestOverriddenLimit_WithoutTheExposureReaderNamesTheCustomersLimit(t *testing.T) {
	g := NewHoldAwareCreditGate(overrideHolds{})
	n := g.OverriddenLimit(context.Background(), "cust", 60000, 50000, 100000, true, "br")
	if n == nil || n.Data["limit_scope"] != LimitScopeCustomer || n.Data["overridden"] != "true" {
		t.Fatalf("the override note = %+v, want the customer scope", n)
	}
}

// P3: a job that is not the customer's reads as that, never as an outage.
func TestCheck_AJobThatIsNotTheCustomersSaysSo(t *testing.T) {
	g := NewHoldAwareCreditGate(overrideHolds{}).WithExposure(overrideExposure{jobErr: customer.ErrNotFound})
	b := g.Check(WithSaleJob(context.Background(), "job-x"), "cust", 1000, 0, 1000000, true, "br", false)
	if b == nil || !strings.Contains(b.Detail, "not the customer's") || strings.Contains(b.Detail, "unavailable") {
		t.Fatalf("block = %+v, want the job named as not the customer's", b)
	}
	g = NewHoldAwareCreditGate(overrideHolds{}).WithExposure(overrideExposure{jobErr: errors.New("boom")})
	b = g.Check(WithSaleJob(context.Background(), "job-x"), "cust", 1000, 0, 1000000, true, "br", false)
	if b == nil || !strings.Contains(b.Detail, "unavailable") {
		t.Fatalf("block = %+v, want an outage to read as unavailable", b)
	}
}

// fakeCustErr fails the customer exposure read.
type custErrExposure struct{ overrideExposure }

func (custErrExposure) CustomerExposure(context.Context, pgx.Tx, string) (int64, error) {
	return 0, errors.New("boom")
}

// JA-3 fix round 2, P2: the override changes no decision but the limit it waives. Main passed a
// customer with no credit under an override, and passed when the job read failed.
func TestCheck_OverridePassesANoCreditCustomerAsOnMain(t *testing.T) {
	ctx := context.Background()
	if b := NewHoldAwareCreditGate(overrideHolds{}).Check(ctx, "cust", 1000, 0, 0, true, "br", true); b != nil {
		t.Fatalf("no reader, no credit, override: %+v, want a pass as on main", b)
	}
	g := NewHoldAwareCreditGate(overrideHolds{}).WithExposure(overrideExposure{})
	if b := g.Check(ctx, "cust", 1000, 0, 0, true, "br", true); b != nil {
		t.Fatalf("reader, no credit, override: %+v, want a pass as on main", b)
	}
}

func TestCheck_OverridePassesWhenTheJobReadFailsAsOnMain(t *testing.T) {
	g := NewHoldAwareCreditGate(overrideHolds{}).WithExposure(overrideExposure{jobErr: errors.New("boom")})
	if b := g.Check(WithSaleJob(context.Background(), "job-1"), "cust", 1000, 0, 1000000, true, "br", true); b != nil {
		t.Fatalf("failed job read under an override: %+v, want a pass as on main", b)
	}
	// the customer's own exposure read is not taken under an override (main read none), so its failure
	// cannot refuse the sale
	g = NewHoldAwareCreditGate(overrideHolds{}).WithExposure(custErrExposure{})
	if b := g.Check(context.Background(), "cust", 1000, 0, 1000000, true, "br", true); b != nil {
		t.Fatalf("failed customer read under an override: %+v, want a pass as on main", b)
	}
}

func TestCheck_ANoCreditCustomerIsTreatedTheSameInsideAndOverTheJobLimit(t *testing.T) {
	own := int64(100)
	g := NewHoldAwareCreditGate(overrideHolds{}).WithExposure(overrideExposure{ownLimit: &own})
	jobCtx := WithSaleJob(context.Background(), "job-1")
	for _, sale := range []int64{50, 1000} {
		// no override: both read COD, never the job's limit for the larger one
		b := g.Check(jobCtx, "cust", sale, 0, 0, true, "br", false)
		if b == nil || b.Policy != "COD_CONSTRAINT" || !strings.Contains(b.Detail, "no credit") {
			t.Fatalf("sale %d, no override: %+v, want the no credit COD block", sale, b)
		}
		// override: both pass, as on main
		if b := g.Check(jobCtx, "cust", sale, 0, 0, true, "br", true); b != nil {
			t.Fatalf("sale %d, override: %+v, want a pass", sale, b)
		}
		// the waiver is recorded the same for both sizes: the customer's no credit, never the job's limit
		if n := g.OverriddenLimit(jobCtx, "cust", sale, 0, 0, true, "br"); n == nil || n.Data["limit_scope"] != LimitScopeCustomer || !strings.Contains(n.Detail, "no credit limit") {
			t.Fatalf("sale %d: want the customer's no credit recorded as waived, got %+v", sale, n)
		}
	}
}

// P3-c: the fallback path attaches no limit data to its refusals, as on main.
func TestCheck_TheFallbackPathAttachesNoLimitData(t *testing.T) {
	b := NewHoldAwareCreditGate(overrideHolds{}).Check(context.Background(), "cust", 60000, 50000, 100000, true, "br", false)
	if b == nil || len(b.Data) != 0 {
		t.Fatalf("fallback refusal = %+v, want a refusal with no Data", b)
	}
}

func TestOverriddenLimit_ANoCreditCustomerRecordsTheWaiver(t *testing.T) {
	g := NewHoldAwareCreditGate(overrideHolds{}).WithExposure(overrideExposure{})
	b := g.OverriddenLimit(context.Background(), "cust", 1000, 0, 0, true, "br")
	if b == nil || b.Data["overridden"] != "true" || b.Data["limit_scope"] != LimitScopeCustomer || !strings.Contains(b.Detail, "no credit limit") {
		t.Fatalf("no credit customer under an override: %+v, want the waiver recorded", b)
	}
}
