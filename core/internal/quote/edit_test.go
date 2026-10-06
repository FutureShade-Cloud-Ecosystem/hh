package quote

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/jackc/pgx/v5"
)

// - the ratified matrix, written out independently -

// ratifiedQuoteMatrix is the quote edit-state matrix as RATIFIED (R2, 2026-07-30), written out as an
// independent literal.
//
// It deliberately does NOT read quoteEditRules. Deriving the expectation from the map under test would
// make every cell assertion a tautology, widening a `from` set would move the expectation with it and
// the test would still pass. Spelling the ruling out here is what turns these subtests into real
// mutation detectors: adding "ACCEPTED" to the line rules flips
// TestQuoteEditMatrix/line_qty/ACCEPTED to a failure.
//
// (DIB-175 learned this the hard way, its first cut derived the expectation from editRules and every
// cell passed under mutation. This is the corrected idiom, applied from the start.)
var ratifiedQuoteMatrix = map[QuoteEditField]map[string]bool{
	FieldQuoteLineQty:    {"DRAFT": true, "SENT": true},
	FieldQuoteLineAdd:    {"DRAFT": true, "SENT": true},
	FieldQuoteLineRemove: {"DRAFT": true, "SENT": true},
	FieldQuoteValidUntil: {"DRAFT": true, "SENT": true},
}

// allQuoteStatuses is the 0008 CHECK vocabulary, the full status space each field is tested against.
// Note LOST, not REJECTED: R2's "REJECTED" is this schema's LOST (D3).
var allQuoteStatuses = []string{"DRAFT", "SENT", "ACCEPTED", "CONVERTED", "EXPIRED", "LOST"}

// - fixtures -

const (
	qOrg    = "11111111-1111-4111-8111-111111111111" // the quote under test
	qBranch = "22222222-2222-4222-8222-222222222222"
	qOther  = "33333333-3333-4333-8333-333333333333" // a branch the caller cannot reach
	qUser   = "44444444-4444-4444-8444-444444444444"
	qCust   = "55555555-5555-4555-8555-555555555555"
	qProd   = "66666666-6666-4666-8666-666666666666"
)

// fixedNow pins the expiry clock. A date-boundary test that depends on the wall clock passes on most
// days and fails on one, which is worse than no test at all.
var fixedNow = time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

func qNow() time.Time { return fixedNow }

// dateP parses a YYYY-MM-DD into the *time.Time the header seam carries. nil means NEVER EXPIRES.
func dateP(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		panic("bad test date " + s)
	}
	return &t
}

// fakeStaffStore is an in-memory staffStore.
//
// It honours allowedFrom and the expiry window ITSELF, in the same order the real repository does
// (lock → status guard → expiry guard → mutate → recompute → record → "commit"), because the matrix
// must be enforced by the CODE UNDER TEST, never by the fake's convenience. The record hook is invoked
// with a nil pgx.Tx, recordingTxRecorder ignores the querier, and a record failure returns WITHOUT
// applying the mutation, mirroring the rollback the real transaction performs.
type fakeStaffStore struct {
	status     string
	branchID   string
	validUntil *time.Time
	ownerID    string
	line       QuoteLineSnapshot
	uom        string
	lineGone   bool

	// captured
	lastWrite quoteEditWrite
	writes    int
	validSet  string
	// disc and clearing back the floor seam's store read.
	disc     int
	clearing *floorClearingStamp
	// floorReadErr, when set, is the floor state read failing: the read
	// error leg of the quantity reprice (P2-6).
	floorReadErr error
}

func newFakeStaffStore(status string) *fakeStaffStore {
	return &fakeStaffStore{
		status:   status,
		branchID: qBranch,
		uom:      "EA",
		line: QuoteLineSnapshot{
			LineNo: 1, ProductID: qProd,
			QtyEntered: "2.0000", QtyBase: "2.0000",
			UnitPriceCents: 1000, LineTotalCents: 2000,
		},
	}
}

func (f *fakeStaffStore) ResolveQuoteID(_ context.Context, idOrNo string, _ bool, _ []string) (string, error) {
	if idOrNo == qOrg {
		return qOrg, nil
	}
	return "", ErrNotFound
}

func (f *fakeStaffStore) QuoteEditHeader(_ context.Context, quoteID string) (string, string, *time.Time, string, error) {
	if quoteID != qOrg {
		return "", "", nil, "", ErrNotFound
	}
	return f.status, f.branchID, f.validUntil, f.ownerID, nil
}

func (f *fakeStaffStore) CreateQuote(_ context.Context, _ QuoteCreate) (string, string, error) {
	return qOrg, "#Q-1", nil
}

func (f *fakeStaffStore) StaffDetail(_ context.Context, quoteID string) (StaffQuoteView, error) {
	if quoteID != qOrg {
		return StaffQuoteView{}, ErrNotFound
	}
	v := StaffQuoteView{
		ID: qOrg, QuoteNo: "#Q-1", CustomerID: qCust, BranchID: f.branchID,
		Status: f.status, validUntil: f.validUntil, Lines: []QuoteViewLine{}, OwnerID: f.ownerID,
	}
	if f.validUntil != nil {
		v.ValidUntil = f.validUntil.Format(dateLayout)
	}
	return v, nil
}

func (f *fakeStaffStore) ListStaffQuotes(_ context.Context, _ bool, _ []string, _ string, _ Filter) ([]StaffQuoteSummary, error) {
	return []StaffQuoteSummary{}, nil
}

func (f *fakeStaffStore) LineForEdit(_ context.Context, quoteID string, lineNo int) (QuoteLineSnapshot, string, error) {
	if quoteID != qOrg || f.lineGone || lineNo != f.line.LineNo {
		return QuoteLineSnapshot{}, "", ErrLineNotFound
	}
	return f.line, f.uom, nil
}

func (f *fakeStaffStore) QuotePricingContext(_ context.Context, _ string) (string, string, error) {
	return qCust, f.branchID, nil
}

// LineFloorState satisfies the floor seam's store read (floor.go): the
// fake's line is undiscounted unless a test sets disc. The line's own row
// id is a fixed fixture string: no test here relies on row identity.
func (f *fakeStaffStore) LineFloorState(_ context.Context, quoteID string, lineNo int) (string, int, int64, int64, string, *floorClearingStamp, error) {
	if f.floorReadErr != nil {
		return "", 0, 0, 0, "", nil, f.floorReadErr
	}
	if quoteID != qOrg || f.lineGone || lineNo != f.line.LineNo {
		return "", 0, 0, 0, "", nil, ErrLineNotFound
	}
	return "11111111-1111-4111-8111-111111111111", f.disc, 1000, 2000, "EA", f.clearing, nil
}

// Convert satisfies the DIB-483 conversion seam embedded in staffStore. The edit tests never call it;
// the conversion's own behaviour is covered in convert_seam_test.go against a store that records.
func (f *fakeStaffStore) Convert(_ context.Context, _, quoteID string, _ ConvertOptions) (ConvertResult, error) {
	return ConvertResult{OrderID: "order-" + quoteID, OrderNo: "#SO-TRT-1"}, nil
}

// guard mirrors lockQuoteForEdit: the two checks that decide the write, in the same order.
func (f *fakeStaffStore) guard(allowedFrom []string) error {
	for _, s := range allowedFrom {
		if s == f.status {
			if err := assertNotExpired(f.validUntil, fixedNow); err != nil {
				return err
			}
			return nil
		}
	}
	return &NotEditableError{Status: f.status}
}

func (f *fakeStaffStore) totals() QuoteTotals {
	if f.lineGone {
		return QuoteTotals{SubtotalCents: 0, TotalCents: 0}
	}
	return QuoteTotals{SubtotalCents: f.line.LineTotalCents, TotalCents: f.line.LineTotalCents}
}

func (f *fakeStaffStore) UpdateLineGuarded(ctx context.Context, w quoteEditWrite, _ floorWritePlan, hooks quoteEditHooks) (QuoteLineSnapshot, QuoteLineSnapshot, QuoteTotals, string, error) {
	if err := f.guard(w.AllowedFrom); err != nil {
		return QuoteLineSnapshot{}, QuoteLineSnapshot{}, QuoteTotals{}, f.status, err
	}
	before := f.line
	after := QuoteLineSnapshot{
		LineNo: w.LineNo, ProductID: before.ProductID,
		QtyEntered: canonQty(w.QtyEntered), QtyBase: canonQty(w.QtyBase),
		UnitPriceCents: w.UnitPriceCents, LineTotalCents: w.LineTotalCents,
	}
	saved := f.line
	f.line = after
	totals := f.totals()
	if hooks.record != nil {
		if err := hooks.record(ctx, nil, before, after, totals); err != nil {
			f.line = saved // the real tx rolls back; so does the fake
			return before, after, totals, f.status, err
		}
	}
	if err := runPricingLog(ctx, hooks.pricing, nil, after.LineNo, before != after); err != nil {
		f.line = saved
		return before, after, totals, f.status, err
	}
	f.writes++
	f.lastWrite = w
	return before, after, totals, f.status, nil
}

func (f *fakeStaffStore) InsertLineGuarded(ctx context.Context, w quoteEditWrite, hooks quoteEditHooks) (QuoteLineSnapshot, QuoteTotals, string, error) {
	if err := f.guard(w.AllowedFrom); err != nil {
		return QuoteLineSnapshot{}, QuoteTotals{}, f.status, err
	}
	after := QuoteLineSnapshot{
		LineNo: 2, ProductID: w.ProductID,
		QtyEntered: canonQty(w.QtyEntered), QtyBase: canonQty(w.QtyBase),
		UnitPriceCents: w.UnitPriceCents, LineTotalCents: w.LineTotalCents,
	}
	totals := QuoteTotals{
		SubtotalCents: f.line.LineTotalCents + after.LineTotalCents,
		TotalCents:    f.line.LineTotalCents + after.LineTotalCents,
	}
	if hooks.record != nil {
		if err := hooks.record(ctx, nil, QuoteLineSnapshot{}, after, totals); err != nil {
			return after, totals, f.status, err
		}
	}
	if err := runPricingLog(ctx, hooks.pricing, nil, after.LineNo, true); err != nil {
		return after, totals, f.status, err
	}
	f.writes++
	f.lastWrite = w
	return after, totals, f.status, nil
}

func (f *fakeStaffStore) DeleteLineGuarded(ctx context.Context, quoteID string, lineNo int, allowedFrom []string, actorID string, hooks quoteEditHooks) (QuoteLineSnapshot, QuoteTotals, string, error) {
	if err := f.guard(allowedFrom); err != nil {
		return QuoteLineSnapshot{}, QuoteTotals{}, f.status, err
	}
	if f.lineGone || lineNo != f.line.LineNo {
		return QuoteLineSnapshot{}, QuoteTotals{}, f.status, ErrLineNotFound
	}
	before := f.line
	f.lineGone = true
	totals := f.totals()
	if hooks.record != nil {
		if err := hooks.record(ctx, nil, before, QuoteLineSnapshot{}, totals); err != nil {
			f.lineGone = false
			return before, totals, f.status, err
		}
	}
	f.writes++
	return before, totals, f.status, nil
}

func (f *fakeStaffStore) UpdateValidUntilGuarded(ctx context.Context, quoteID, newDate string, allowedFrom []string, actorID string, hooks quoteDateHooks) (string, string, string, error) {
	if err := f.guard(allowedFrom); err != nil {
		return "", "", f.status, err
	}
	before := ""
	if f.validUntil != nil {
		before = f.validUntil.Format(dateLayout)
	}
	if hooks.record != nil {
		if err := hooks.record(ctx, nil, before, newDate); err != nil {
			return before, newDate, f.status, err
		}
	}
	f.writes++
	f.validSet = newDate
	f.validUntil = dateP(newDate)
	return before, newDate, f.status, nil
}

// canonQty renders a quantity the way Postgres renders DECIMAL(19,4), so trail assertions see "5.0000"
// rather than "5", otherwise the fake and the real DB would disagree about the recorded value and the
// unit tests would pin a shape the integration tests contradict.
func canonQty(s string) string {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return s
	}
	return strconv.FormatFloat(f, 'f', 4, 64)
}

// - audit double -

// recordingTxRecorder is an in-memory audit.Recorder + txRecorder. Implementing RecordTx is what lets
// StaffService bind it as the TRANSACTIONAL recorder, so these unit tests exercise the same emit path
// production uses. txErr forces a recorder failure (the rollback test); the querier is ignored.
type recordingTxRecorder struct {
	records []audit.SensitiveAccess
	err     error
	txErr   error
}

func (r *recordingTxRecorder) Record(_ context.Context, a audit.SensitiveAccess) error {
	if r.err != nil {
		return r.err
	}
	r.records = append(r.records, a)
	return nil
}

func (r *recordingTxRecorder) RecordTx(_ context.Context, _ audit.Execer, a audit.SensitiveAccess) error {
	if r.txErr != nil {
		return r.txErr
	}
	r.records = append(r.records, a)
	return nil
}

// - pricing / seam doubles -

type qPricer struct {
	blocked    bool
	finalCents int64
	marginBps  int64
	floorBps   int64
	factor     float64
	err        error
}

func (p qPricer) ResolvePrice(_ context.Context, in pricing.ResolveInput) (pricing.Breakdown, error) {
	if p.err != nil {
		return pricing.Breakdown{}, p.err
	}
	if p.blocked {
		// The resolve shape a rule price below the floor answers: complete,
		// with Blocked set and the figures riding it (plan 4.1 keeps them).
		final := p.finalCents
		if final == 0 {
			final = 600
		}
		return pricing.Breakdown{
			Blocked: true, BlockPolicy: "FLOOR",
			FinalCents: final, MarginBps: p.marginBps, FloorBps: p.floorBps,
			PriceUOM: "EA", QtyBase: in.QtyEntered, LineTotalCents: int64(in.QtyEntered) * final,
		}, nil
	}
	f := p.factor
	if f == 0 {
		f = 1
	}
	qtyBase := in.QtyEntered * f
	return pricing.Breakdown{
		FinalCents: 1000, PriceUOM: "EA",
		QtyBase: qtyBase, LineTotalCents: int64(qtyBase * 1000),
	}, nil
}

type qInv struct{}

func (qInv) CurrentCostCents(_ context.Context, _, _ string) (int64, bool, error) {
	return 500, true, nil
}

type qCustType struct{}

func (qCustType) CustomerType(_ context.Context, _ string) (string, error) { return "CONTRACTOR", nil }

// - callers -

type qCtxKey struct{}

func qCtxAs(c identity.StaffContext) context.Context {
	return context.WithValue(context.Background(), qCtxKey{}, c)
}

func qCallerFrom(ctx context.Context) (identity.StaffContext, bool) {
	c, ok := ctx.Value(qCtxKey{}).(identity.StaffContext)
	return c, ok
}

// qStaff is a frontline, branch-scoped staff member, the LOWEST authority that may edit a
// quote it owns (plan 3.3: an inside sales caller at Work). Using frontline
// (not manager) as the default caller in every matrix test is deliberate: it is
// what proves the ladder's lowest rung rather than merely assuming it.
func qStaff() identity.StaffContext {
	return identity.StaffContext{
		UserID: qUser, RoleCode: "INSIDE_SALES", Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
		Status: identity.StatusActive, Branches: map[string]bool{qBranch: true},
	}
}

func qInactive() identity.StaffContext {
	c := qStaff()
	c.Status = "DISABLED"
	return c
}

// qFixture is the one-liner every test starts from. The store's quote is
// owned by qStaff's user and the service carries the decided role
// configuration, the shape every edit now runs under (plan 3.3).
func qFixture(t *testing.T, status string) (*StaffService, *fakeStaffStore, *recordingTxRecorder) {
	t.Helper()
	store := newFakeStaffStore(status)
	store.ownerID = qUser
	rec := &recordingTxRecorder{}
	svc := NewStaffService(store).
		WithAudit(rec).
		WithPricing(qPricer{}).
		WithCostBasis(qInv{}).
		WithCustomerType(qCustType{}).
		WithRoleConfig(qTestRoleConfig()).
		withCaller(qCallerFrom).
		withNow(qNow)
	return svc, store, rec
}

// qTestRoleConfig loads the decided default once for the unit fixtures.
func qTestRoleConfig() *roleconfig.Config {
	cfg, err := roleconfig.Default()
	if err != nil {
		panic("quote tests: role configuration refused: " + err.Error())
	}
	return cfg
}

// applyQuoteEdit drives one command uniformly, so the matrix test can treat all four identically.
func applyQuoteEdit(svc *StaffService, ctx context.Context, field QuoteEditField) error {
	switch field {
	case FieldQuoteLineQty:
		_, err := svc.EditLineQty(ctx, qOrg, 1, "5")
		return err
	case FieldQuoteLineAdd:
		_, err := svc.AddLine(ctx, qOrg, qProd, "", "3", "EA")
		return err
	case FieldQuoteLineRemove:
		_, err := svc.RemoveLine(ctx, qOrg, 1)
		return err
	case FieldQuoteValidUntil:
		_, err := svc.SetValidUntil(ctx, qOrg, "2026-12-31")
		return err
	}
	return fmt.Errorf("unknown field %q", field)
}

// - the matrix -

func TestQuoteEditMatrix(t *testing.T) {
	for _, field := range quoteFieldOrder {
		field := field
		t.Run(string(field), func(t *testing.T) {
			for _, status := range allQuoteStatuses {
				status := status
				t.Run(status, func(t *testing.T) {
					svc, store, _ := qFixture(t, status)
					err := applyQuoteEdit(svc, qCtxAs(qStaff()), field)
					wantAllowed := ratifiedQuoteMatrix[field][status]

					if wantAllowed {
						if err != nil {
							t.Fatalf("%s in %s: ratified as EDITABLE, got error: %v", field, status, err)
						}
						if store.writes == 0 {
							t.Errorf("%s in %s: an allowed edit must write", field, status)
						}
						return
					}
					if err == nil {
						t.Fatalf("%s in %s: ratified as FROZEN, but the edit succeeded", field, status)
					}
					var ne *NotEditableError
					if !errors.As(err, &ne) {
						t.Fatalf("%s in %s: want *NotEditableError, got %T: %v", field, status, err, err)
					}
					if ne.Status != status {
						t.Errorf("want the refusal to name status %q, got %q", status, ne.Status)
					}
					if ne.Field != field {
						t.Errorf("want the refusal to name field %q, got %q", field, ne.Field)
					}
					// The message must NAME the status, a caller has to know why it was refused.
					if !strings.Contains(ne.Error(), status) {
						t.Errorf("want the 409 message to name the status %q; got %q", status, ne.Error())
					}
					if store.writes != 0 {
						t.Errorf("a refused edit must not write; got %d writes", store.writes)
					}
				})
			}
		})
	}
}

// TestQuoteEditMatrix_RatifiedShape pins the matrix itself, so a silent widening of a `from` set is
// caught even if someone also "fixes" the cell tests to match.
func TestQuoteEditMatrix_RatifiedShape(t *testing.T) {
	for _, f := range quoteFieldOrder {
		for _, s := range allQuoteStatuses {
			if got, want := quoteEditRules[f].from[s], ratifiedQuoteMatrix[f][s]; got != want {
				t.Errorf("quoteEditRules[%s][%s] = %v, ratified as %v", f, s, got, want)
			}
		}
	}
	// Every field is covered by the matrix, a new command must not silently arrive with no ruling.
	if len(quoteEditRules) != len(ratifiedQuoteMatrix) {
		t.Errorf("quoteEditRules has %d fields, the ratified matrix has %d, a field was added or removed "+
			"without a ruling", len(quoteEditRules), len(ratifiedQuoteMatrix))
	}
	if len(quoteFieldOrder) != len(quoteEditRules) {
		t.Errorf("quoteFieldOrder lists %d fields but quoteEditRules has %d, the report order would omit one",
			len(quoteFieldOrder), len(quoteEditRules))
	}
}

// TestQuoteEdit_IsStaffGatedNotManagerGated pins R1, the deliberate divergence from DIB-175. A
// frontline (Tier 3) caller must be able to perform EVERY quote edit. If someone "helpfully" adds a
// manager gate to match the order side, this fails.
func TestQuoteEdit_IsStaffGatedNotManagerGated(t *testing.T) {
	for _, field := range quoteFieldOrder {
		field := field
		t.Run(string(field), func(t *testing.T) {
			svc, store, _ := qFixture(t, "DRAFT")
			frontline := qStaff()
			if frontline.IsManager() {
				t.Fatal("fixture bug: the default test caller must be frontline, not a manager")
			}
			if err := applyQuoteEdit(svc, qCtxAs(frontline), field); err != nil {
				t.Fatalf("R1: a frontline in-branch staff member must be able to edit %s, got: %v", field, err)
			}
			if store.writes == 0 {
				t.Error("the edit must have written")
			}
		})
	}
}

// - authority -

func TestQuoteEdit_Authority(t *testing.T) {
	t.Run("no caller is 401", func(t *testing.T) {
		svc, _, _ := qFixture(t, "DRAFT")
		err := applyQuoteEdit(svc, context.Background(), FieldQuoteLineQty)
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("want ErrUnauthorized, got %v", err)
		}
	})
	t.Run("inactive caller is 403", func(t *testing.T) {
		svc, store, _ := qFixture(t, "DRAFT")
		err := applyQuoteEdit(svc, qCtxAs(qInactive()), FieldQuoteLineQty)
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("want ErrForbidden, got %v", err)
		}
		if store.writes != 0 {
			t.Error("an inactive caller must not write")
		}
	})
	t.Run("cross-branch caller is 403", func(t *testing.T) {
		svc, store, _ := qFixture(t, "DRAFT")
		store.branchID = qOther // the quote lives somewhere the caller cannot reach
		err := applyQuoteEdit(svc, qCtxAs(qStaff()), FieldQuoteLineQty)
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("want ErrForbidden for a cross-branch quote, got %v", err)
		}
		if store.writes != 0 {
			t.Error("a cross-branch caller must not write")
		}
	})
	t.Run("unknown quote is 404 before any authz answer", func(t *testing.T) {
		svc, _, _ := qFixture(t, "DRAFT")
		_, err := svc.EditLineQty(qCtxAs(qStaff()), "99999999-9999-4999-8999-999999999999", 1, "5")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})
}

// - expiry (D2) -

// TestQuoteExpiry_NullNeverExpires is the D2a mutation anchor and the single most important expiry
// test. A NULL valid_until means the quote never expires; the naive implementation
// (`validUntil.Before(now)` on a zero time.Time) would freeze every quote ever created without an
// expiry date, which is most of them, since nothing has ever populated the column outside seeds.
func TestQuoteExpiry_NullNeverExpires(t *testing.T) {
	if quoteExpired(nil, fixedNow) {
		t.Fatal("D2a: a NULL valid_until means NEVER EXPIRES, not expired")
	}
	if err := assertNotExpired(nil, fixedNow); err != nil {
		t.Fatalf("D2a: a quote with no expiry date must be editable, got: %v", err)
	}
	// …and end to end: every command must succeed on a quote with no expiry.
	for _, field := range quoteFieldOrder {
		svc, store, _ := qFixture(t, "DRAFT")
		store.validUntil = nil
		if err := applyQuoteEdit(svc, qCtxAs(qStaff()), field); err != nil {
			t.Errorf("D2a: %s must be allowed on a quote with no expiry, got: %v", field, err)
		}
	}
}

func TestQuoteExpiry_Boundary(t *testing.T) {
	cases := []struct {
		name       string
		validUntil string
		wantExpiry bool
	}{
		// A quote valid until the 30th is good for the WHOLE of the 30th, expiry begins the day after.
		{"expires today is still valid", "2026-07-30", false},
		{"expires tomorrow is valid", "2026-07-31", false},
		{"expired yesterday", "2026-07-29", true},
		{"expired long ago", "2026-01-01", true},
		{"far future", "2030-01-01", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := quoteExpired(dateP(tc.validUntil), fixedNow); got != tc.wantExpiry {
				t.Errorf("quoteExpired(%s, %s) = %v, want %v",
					tc.validUntil, fixedNow.Format(dateLayout), got, tc.wantExpiry)
			}
		})
	}
}

// TestQuoteEdit_ExpiredIsRefusedWithDateAndWayForward pins D2b: the refusal must not be a bare 409.
func TestQuoteEdit_ExpiredIsRefusedWithDateAndWayForward(t *testing.T) {
	for _, field := range quoteFieldOrder {
		field := field
		t.Run(string(field), func(t *testing.T) {
			svc, store, _ := qFixture(t, "SENT")
			store.validUntil = dateP("2026-06-01")
			err := applyQuoteEdit(svc, qCtxAs(qStaff()), field)
			var qe *QuoteExpiredError
			if !errors.As(err, &qe) {
				t.Fatalf("want *QuoteExpiredError, got %T: %v", err, err)
			}
			if !strings.Contains(qe.Error(), "2026-06-01") {
				t.Errorf("D2b: the refusal must NAME the expiry date; got %q", qe.Error())
			}
			if !strings.Contains(strings.ToLower(qe.Error()), "request an updated quote") {
				t.Errorf("D2b: the refusal must name the way forward; got %q", qe.Error())
			}
			if store.writes != 0 {
				t.Error("an expired quote must not be written")
			}
		})
	}
}

// TestQuoteEdit_ExpiredOffersNoEditableFields pins the read-side half of D2: the client must never be
// offered a control the server would refuse.
func TestQuoteEdit_ExpiredOffersNoEditableFields(t *testing.T) {
	svc, store, _ := qFixture(t, "SENT")
	store.validUntil = dateP("2026-06-01")
	view, err := svc.StaffQuoteRead(qCtxAs(qStaff()), qOrg)
	if err != nil {
		t.Fatalf("reading an expired quote must still succeed: %v", err)
	}
	if !view.Expired {
		t.Error("want Expired=true on a lapsed quote")
	}
	if len(view.EditableFields) != 0 {
		t.Errorf("an expired quote must offer nothing; got %v", view.EditableFields)
	}
	if view.EditableFields == nil {
		t.Error("editable_fields must marshal as [] not null, an absent list must fail closed client-side")
	}
}

// - editable_fields -

func TestQuoteEditableFields_TracksTheMatrix(t *testing.T) {
	for _, status := range allQuoteStatuses {
		status := status
		t.Run(status, func(t *testing.T) {
			svc, _, _ := qFixture(t, status)
			view, err := svc.StaffQuoteRead(qCtxAs(qStaff()), qOrg)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			got := map[string]bool{}
			for _, f := range view.EditableFields {
				got[f] = true
			}
			for _, f := range quoteFieldOrder {
				want := ratifiedQuoteMatrix[f][status]
				if got[string(f)] != want {
					t.Errorf("editable_fields in %s: %s offered=%v, ratified=%v",
						status, f, got[string(f)], want)
				}
			}
		})
	}
}

// - the change trail -

// changeRows indexes the captured audit rows by the column they report.
func changeRows(rec *recordingTxRecorder) map[string]audit.SensitiveAccess {
	out := map[string]audit.SensitiveAccess{}
	for _, r := range rec.records {
		if r.Action != audit.ActQuoteChanged {
			continue
		}
		if f, ok := r.Extra["field"].(string); ok {
			out[f] = r
		}
	}
	return out
}

func keysOf(m map[string]audit.SensitiveAccess) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestQuoteEdit_QtyWritesReconstructableTrail(t *testing.T) {
	svc, _, rec := qFixture(t, "DRAFT")
	if _, err := svc.EditLineQty(qCtxAs(qStaff()), qOrg, 1, "5"); err != nil {
		t.Fatalf("edit: %v", err)
	}
	rows := changeRows(rec)
	for _, want := range []string{"qty_entered", "qty_base", "line_total_cents", "subtotal_cents", "total_cents"} {
		if _, ok := rows[want]; !ok {
			t.Errorf("want a QUOTE_CHANGED row for %q; got fields %v", want, keysOf(rows))
		}
	}
	qty := rows["qty_entered"]
	if got := fmt.Sprint(qty.Extra["before"]); got != "2.0000" {
		t.Errorf("qty_entered before = %q, want the canonical stored value 2.0000", got)
	}
	if got := fmt.Sprint(qty.Extra["after"]); got != "5.0000" {
		t.Errorf("qty_entered after = %q, want the canonical stored value 5.0000", got)
	}
	if qty.ActorID != qUser {
		t.Errorf("actor = %q, want %q", qty.ActorID, qUser)
	}
	if qty.EntityType != "quote" || qty.EntityID != qOrg {
		t.Errorf("entity = %s/%s, want quote/%s", qty.EntityType, qty.EntityID, qOrg)
	}
	// Money must ride as an INTEGER, never a string, a string would be exposed to the audit boundary's
	// cleartext redaction (7+ consecutive digits → "[redacted]"), which would silently destroy the
	// before→after reconstruction this whole feature exists to provide.
	lt := rows["line_total_cents"]
	if _, ok := lt.Extra["after"].(int64); !ok {
		t.Errorf("line_total_cents after must be int64 (redaction-safe), got %T", lt.Extra["after"])
	}
	// unit_price_cents did not move (the fake prices at a flat 1000), so it must emit NO row. The trail
	// records transitions, not attempts.
	if _, ok := rows["unit_price_cents"]; ok {
		t.Error("unit_price_cents did not change, it must not emit a row")
	}
}

func TestQuoteEdit_AddRecordsWhatWasAdded(t *testing.T) {
	svc, _, rec := qFixture(t, "DRAFT")
	if _, err := svc.AddLine(qCtxAs(qStaff()), qOrg, qProd, "Paver", "3", "EA"); err != nil {
		t.Fatalf("add: %v", err)
	}
	rows := changeRows(rec)
	added, ok := rows["line_added"]
	if !ok {
		t.Fatalf("want a line_added row; got %v", keysOf(rows))
	}
	if _, has := added.Extra["before"]; has {
		t.Error("a creation has no pre-image, `before` must be absent")
	}
	if got := fmt.Sprint(added.Extra["after"]); got != qProd {
		t.Errorf("line_added after = %q, want the product id %q", got, qProd)
	}
}

func TestQuoteEdit_RemoveSnapshotsThePreImage(t *testing.T) {
	svc, _, rec := qFixture(t, "DRAFT")
	if _, err := svc.RemoveLine(qCtxAs(qStaff()), qOrg, 1); err != nil {
		t.Fatalf("remove: %v", err)
	}
	rows := changeRows(rec)
	removed, ok := rows["line_removed"]
	if !ok {
		t.Fatalf("want a line_removed row; got %v", keysOf(rows))
	}
	// The pre-image is the ONLY surviving record of what was removed, the row no longer exists.
	if got := fmt.Sprint(removed.Extra["before"]); got != qProd {
		t.Errorf("line_removed before = %q, want the removed product id %q", got, qProd)
	}
	if _, has := removed.Extra["after"]; has {
		t.Error("a removal has no post-image, `after` must be absent")
	}
}

func TestQuoteEdit_DateChangeIsRecorded(t *testing.T) {
	svc, store, rec := qFixture(t, "DRAFT")
	store.validUntil = dateP("2026-09-01")
	if _, err := svc.SetValidUntil(qCtxAs(qStaff()), qOrg, "2026-12-31"); err != nil {
		t.Fatalf("set expiry: %v", err)
	}
	rows := changeRows(rec)
	vu, ok := rows["valid_until"]
	if !ok {
		t.Fatalf("want a valid_until row; got %v", keysOf(rows))
	}
	if got := fmt.Sprint(vu.Extra["before"]); got != "2026-09-01" {
		t.Errorf("valid_until before = %q, want 2026-09-01", got)
	}
	if got := fmt.Sprint(vu.Extra["after"]); got != "2026-12-31" {
		t.Errorf("valid_until after = %q, want 2026-12-31", got)
	}
}

func TestQuoteEdit_NoOpEmitsNoRow(t *testing.T) {
	svc, _, rec := qFixture(t, "DRAFT")
	// Re-set the SAME quantity the line already carries.
	if _, err := svc.EditLineQty(qCtxAs(qStaff()), qOrg, 1, "2"); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if _, ok := changeRows(rec)["qty_entered"]; ok {
		t.Error("a no-op qty edit must emit no qty_entered row, the trail records transitions, not attempts")
	}
}

// TestQuoteEdit_RecorderFailureRollsBack is the R3 mutation anchor: when the transactional recorder
// fails, the guarded write must abort, no mutation, no partial trail.
//
// MUTATION CHECK: make runRecord (edit_repository.go) or the fake's record branch ignore its error →
// this test and TestIntegration_QuoteAudit_RecorderFailureRollsBack both fail.
func TestQuoteEdit_RecorderFailureRollsBack(t *testing.T) {
	for _, field := range quoteFieldOrder {
		field := field
		t.Run(string(field), func(t *testing.T) {
			store := newFakeStaffStore("DRAFT")
			rec := &recordingTxRecorder{txErr: errors.New("audit write failed")}
			svc := NewStaffService(store).
				WithAudit(rec).
				WithPricing(qPricer{}).WithCostBasis(qInv{}).WithCustomerType(qCustType{}).
				withCaller(qCallerFrom).withNow(qNow)

			if err := applyQuoteEdit(svc, qCtxAs(qStaff()), field); err == nil {
				t.Fatal("want the edit to fail when the recorder fails")
			}
			if store.writes != 0 {
				t.Errorf("a recorder failure must roll the edit back; got %d writes", store.writes)
			}
			for _, r := range rec.records {
				if r.Action == audit.ActQuoteChanged {
					t.Error("a rolled-back edit must leave no QUOTE_CHANGED rows")
				}
			}
		})
	}
}

// - bounds -

// TestQuoteQty_BoundedBelowRedactionThreshold pins why maxQuoteQty exists: a 7-digit quantity would trip
// the audit boundary's cleartext redaction and land on the trail as "[redacted]".
func TestQuoteQty_BoundedBelowRedactionThreshold(t *testing.T) {
	if maxQuoteQty >= 1000000 {
		t.Fatalf("maxQuoteQty = %d, it must stay below the 7-consecutive-digit redaction threshold, "+
			"or a legitimate quantity lands on the change trail as [redacted]", maxQuoteQty)
	}
	if _, err := parseQuoteQty("1000000"); err == nil {
		t.Error("a 7-digit quantity must be refused")
	}
	// The DERIVED base qty is bounded too, a UOM factor multiplies it, so an in-bounds entered qty can
	// still produce an out-of-bounds base qty.
	svc, _, _ := qFixture(t, "DRAFT")
	svc.WithPricing(qPricer{factor: 100})
	if _, err := svc.EditLineQty(qCtxAs(qStaff()), qOrg, 1, "99999"); err == nil {
		t.Error("a base qty over the bound must be refused even when the entered qty is under it")
	}
}

func TestQuoteQty_RejectsNonPositive(t *testing.T) {
	for _, in := range []string{"", "0", "-1", "abc"} {
		if _, err := parseQuoteQty(in); err == nil {
			t.Errorf("parseQuoteQty(%q) must be refused", in)
		}
	}
}

// TestQuoteEdit_RulePriceBelowFloorIsAccepted pins plan 4.1 (defect D1,
// CP0-3 answer A): a line whose rule price resolves below the margin floor
// is priced and written like any other, as at the till, the resolver's
// figures riding the breakdown for the write's own logging. The floor
// question belongs to the two writes that put a line below its rule price,
// not to the resolve.
func TestQuoteEdit_RulePriceBelowFloorIsAccepted(t *testing.T) {
	svc, store, _ := qFixture(t, "DRAFT")
	// The arbiter is wired: the rule price row cannot be logged without one,
	// and the write then fails closed (qd2_floor_source_integration_test.go).
	svc.WithPricing(qPricer{blocked: true, finalCents: 600, marginBps: 500, floorBps: 1200}).
		WithFloorArbiter(&unitFloorArbiter{})
	if err := applyQuoteEdit(svc, qCtxAs(qStaff()), FieldQuoteLineQty); err != nil {
		t.Fatalf("a line at its rule price must be accepted wherever the floor sits: %v", err)
	}
	if store.writes != 1 {
		t.Errorf("the reprice must be written, got %d writes", store.writes)
	}
}

// TestQuoteEdit_UnwiredPricingFailsClosed pins that a missing seam refuses rather than quoting at zero.
func TestQuoteEdit_UnwiredPricingFailsClosed(t *testing.T) {
	store := newFakeStaffStore("DRAFT")
	store.ownerID = qUser
	svc := NewStaffService(store).WithAudit(&recordingTxRecorder{}).
		WithRoleConfig(qTestRoleConfig()).
		withCaller(qCallerFrom).withNow(qNow) // no pricing/cost/customer-type seams
	if svc.QuoteEditSeamsWired() {
		t.Fatal("fixture bug: seams must be unwired for this test")
	}
	_, err := svc.EditLineQty(qCtxAs(qStaff()), qOrg, 1, "5")
	if !errors.Is(err, ErrEditNotConfigured) {
		t.Fatalf("want ErrEditNotConfigured (fail closed), got %v", err)
	}
	if store.writes != 0 {
		t.Error("an unpriceable line must not be written")
	}
}

// - misc guards -

func TestParseValidUntil(t *testing.T) {
	if got, err := parseValidUntil(""); err != nil || got != "" {
		t.Errorf("empty must clear the date (never expires), got %q %v", got, err)
	}
	if _, err := parseValidUntil("31-12-2026"); err == nil {
		t.Error("a non-ISO date must be refused")
	}
	if got, err := parseValidUntil("2026-12-31"); err != nil || got != "2026-12-31" {
		t.Errorf("valid date round-trip failed: %q %v", got, err)
	}
}

func TestLooksLikeUUID(t *testing.T) {
	if !looksLikeUUID(qOrg) {
		t.Error("a real uuid must pass")
	}
	for _, bad := range []string{"", "#Q-4", "not-a-uuid", strings.Repeat("z", 36)} {
		if looksLikeUUID(bad) {
			t.Errorf("%q must not pass as a uuid", bad)
		}
	}
}

// compile-time proof the fake satisfies the real seam.
var _ staffStore = (*fakeStaffStore)(nil)
var _ pgx.Tx = pgx.Tx(nil)

// TestQuoteQty_FloorStateReadFailureRefuses (P2-6): a quantity reprice
// whose floor state read FAILS is refused, never waved through the floor
// decision as though the line carried no discount.
func TestQuoteQty_FloorStateReadFailureRefuses(t *testing.T) {
	svc, store, _ := qFixture(t, "DRAFT")
	store.floorReadErr = fmt.Errorf("floor state read failed")
	if _, err := svc.EditLineQty(qCtxAs(qStaff()), qOrg, 1, "5"); err == nil {
		t.Fatal("a failed floor state read must refuse the quantity change, not skip the floor question")
	}
	if store.writes != 0 {
		t.Errorf("a refused reprice must not write, wrote %d", store.writes)
	}
}
