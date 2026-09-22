package order

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
)

// edit_test.go, DIB-175 staff order-edit: the ratified edit-state matrix, the authority ladder, the
// money predicate, the quantity invariants, and the shape of the change trail, all DB-free against a
// fake store, mirroring transition_test.go's approach for the DIB-191 transition matrix.
//
// MUTATION CHECK (named for the supervisor): loosening the state guard, adding "READY" to the
// FieldLineQty/FieldLineAdd/FieldLineRemove `from` sets in editRules, must fail
// TestEditMatrix/line_qty/READY, TestEditMatrix/line_add/READY and TestEditMatrix/line_remove/READY.
// The DB-backed counterpart is TestIntegration_EditBlockedAtReady (edit_integration_test.go).

const (
	edOrder  = "77777777-7777-4777-8777-777777777777"
	edCust   = "88888888-8888-4888-8888-888888888888"
	edUser   = "99999999-9999-4999-8999-999999999999"
	edProd   = "aaaa1111-2222-4333-8444-555566667777"
	edBranch = histBranchA
)

// The matrix test walks allStatuses (transition_test.go), the full orders CHECK vocabulary, so a
// status added to the schema without a ruled edit verdict shows up here as an uncovered cell.

// ratifiedMatrix is the edit-state matrix as RATIFIED on 2026-07-29, written out as an independent
// literal.
//
// It deliberately does NOT read editRules. Deriving the expectation from the map under test would make
// every cell assertion a tautology, widening a `from` set would move the expectation with it and the
// test would still pass. Spelling the ruling out here is what turns these subtests into real mutation
// detectors: adding "READY" to the line rules flips TestEditMatrix/line_qty/READY to a failure.
var ratifiedMatrix = map[EditField]map[string]bool{
	FieldLineQty:    {"DRAFT": true, "CONFIRMED": true, "PICKING": true},
	FieldLineAdd:    {"DRAFT": true, "CONFIRMED": true, "PICKING": true},
	FieldLineRemove: {"DRAFT": true, "CONFIRMED": true, "PICKING": true},
	FieldRequestedDate: {"DRAFT": true, "CONFIRMED": true, "PICKING": true,
		"READY": true, "PARTIALLY_FULFILLED": true},
}

// - fakes -

// fakeEditStore layers the DIB-175 edit persistence over the existing fakeStore, so the service's
// `s.store.(editStore)` assertion succeeds while every orderStore behaviour stays as transition_test
// and history_test already exercise it.
//
// The guarded writes here HONOUR allowedFrom exactly as the real repository does under its row lock -
// the matrix must be enforced by the code under test, never by the fake's convenience.
type fakeEditStore struct {
	*fakeStore
	status    string
	taxCents  int64
	line      LineSnapshot
	uom       string
	allocated float64
	fulfilled float64
	date      string
	lineGone  bool
	// captured
	lastWrite lineEditWrite
	writes    int
}

func newFakeEditStore(status string) *fakeEditStore {
	fs := newFakeStore(mkDetail(edOrder, edCust, status))
	fs.branches = map[string]string{edOrder: edBranch}
	return &fakeEditStore{
		fakeStore: fs,
		status:    status,
		uom:       "EA",
		line: LineSnapshot{
			LineNo: 1, ProductID: edProd, QtyEntered: "2.0000", QtyBase: "2.0000",
			UnitPriceCents: 100, LineTotalCents: 200,
		},
		date: "2026-08-01",
	}
}

func (f *fakeEditStore) OrderEditHeader(_ context.Context, orderID string) (string, string, int64, error) {
	if orderID != edOrder {
		return "", "", 0, ErrNotFound
	}
	return f.status, edBranch, f.taxCents, nil
}

func (f *fakeEditStore) OrderPricingContext(_ context.Context, orderID string) (string, string, error) {
	if orderID != edOrder {
		return "", "", ErrNotFound
	}
	return edCust, edBranch, nil
}

func (f *fakeEditStore) OrderLines(_ context.Context, orderID string) ([]OrderViewLine, OrderTotals, int64, error) {
	if orderID != edOrder {
		return nil, OrderTotals{}, 0, ErrNotFound
	}
	if f.lineGone {
		return []OrderViewLine{}, OrderTotals{}, f.taxCents, nil
	}
	return []OrderViewLine{{
		LineNo: f.line.LineNo, ProductID: f.line.ProductID, Description: "seeded",
		QtyEntered: f.line.QtyEntered, UOMEntered: f.uom, QtyBase: f.line.QtyBase,
		AllocatedBase: "0.0000", FulfilledBase: "0.0000",
		UnitPriceCents: f.line.UnitPriceCents, LineTotalCents: f.line.LineTotalCents,
	}}, OrderTotals{SubtotalCents: f.line.LineTotalCents, TotalCents: f.line.LineTotalCents}, f.taxCents, nil
}

func (f *fakeEditStore) LineForEdit(_ context.Context, _ string, lineNo int) (LineSnapshot, string, float64, float64, error) {
	if f.lineGone || lineNo != f.line.LineNo {
		return LineSnapshot{}, "", 0, 0, ErrLineNotFound
	}
	return f.line, f.uom, f.allocated, f.fulfilled, nil
}

// guard mirrors the repository's lockOrderForEdit: the status check that decides the write.
func (f *fakeEditStore) guard(allowedFrom []string) error {
	for _, s := range allowedFrom {
		if s == f.status {
			return nil
		}
	}
	return &NotEditableError{Status: f.status}
}

// The guarded writes run the hooks in the SAME order the real repository does (lock/guard → precheck →
// mutate → record → "commit"), so the unit tests exercise the money predicate (B2-2) and the
// transactional audit (B2-1) through the production code path. taxCents is handed to precheck as the
// real lockOrderForEdit hands the locked value; the record hook is called with a nil tx (the fake has no
// real transaction, recordingRecorder ignores the querier). A record failure returns WITHOUT applying
// the mutation, mirroring the rollback the real tx performs.
func (f *fakeEditStore) UpdateLineGuarded(ctx context.Context, w lineEditWrite, hooks lineEditHooks) (LineSnapshot, LineSnapshot, OrderTotals, string, error) {
	if err := f.guard(w.AllowedFrom); err != nil {
		return LineSnapshot{}, LineSnapshot{}, OrderTotals{}, f.status, err
	}
	if hooks.precheck != nil {
		if err := hooks.precheck(ctx, f.taxCents); err != nil {
			return LineSnapshot{}, LineSnapshot{}, OrderTotals{}, f.status, err
		}
	}
	before := f.line
	// Canonicalise exactly as the DECIMAL(19,4) column + RETURNING does in the real repository, the
	// trail must record the STORED value ("5.0000"), never the raw request text ("5").
	after := LineSnapshot{LineNo: w.LineNo, ProductID: before.ProductID, QtyEntered: canonQty(w.QtyEntered),
		QtyBase: w.QtyBase, UnitPriceCents: w.UnitPriceCents, LineTotalCents: w.LineTotalCents}
	totals := OrderTotals{SubtotalCents: after.LineTotalCents, TotalCents: after.LineTotalCents}
	if hooks.record != nil {
		if err := hooks.record(ctx, nil, before, after, totals); err != nil {
			return LineSnapshot{}, LineSnapshot{}, OrderTotals{}, f.status, err // rolled back, no mutation
		}
	}
	f.writes++
	f.lastWrite = w
	f.line = after
	return before, after, totals, f.status, nil
}

func (f *fakeEditStore) InsertLineGuarded(ctx context.Context, w lineEditWrite, hooks lineEditHooks) (LineSnapshot, OrderTotals, string, error) {
	if err := f.guard(w.AllowedFrom); err != nil {
		return LineSnapshot{}, OrderTotals{}, f.status, err
	}
	if hooks.precheck != nil {
		if err := hooks.precheck(ctx, f.taxCents); err != nil {
			return LineSnapshot{}, OrderTotals{}, f.status, err
		}
	}
	after := LineSnapshot{LineNo: 2, ProductID: w.ProductID, QtyEntered: canonQty(w.QtyEntered),
		QtyBase: w.QtyBase, UnitPriceCents: w.UnitPriceCents, LineTotalCents: w.LineTotalCents}
	total := f.line.LineTotalCents + after.LineTotalCents
	totals := OrderTotals{SubtotalCents: total, TotalCents: total}
	if hooks.record != nil {
		if err := hooks.record(ctx, nil, LineSnapshot{}, after, totals); err != nil {
			return LineSnapshot{}, OrderTotals{}, f.status, err
		}
	}
	f.writes++
	f.lastWrite = w
	return after, totals, f.status, nil
}

func (f *fakeEditStore) DeleteLineGuarded(ctx context.Context, _ string, lineNo int, allowedFrom []string, hooks lineEditHooks) (LineSnapshot, OrderTotals, string, error) {
	if err := f.guard(allowedFrom); err != nil {
		return LineSnapshot{}, OrderTotals{}, f.status, err
	}
	if hooks.precheck != nil {
		if err := hooks.precheck(ctx, f.taxCents); err != nil {
			return LineSnapshot{}, OrderTotals{}, f.status, err
		}
	}
	if f.lineGone || lineNo != f.line.LineNo {
		return LineSnapshot{}, OrderTotals{}, f.status, ErrLineNotFound
	}
	before := f.line
	totals := OrderTotals{}
	if hooks.record != nil {
		if err := hooks.record(ctx, nil, before, LineSnapshot{}, totals); err != nil {
			return LineSnapshot{}, OrderTotals{}, f.status, err
		}
	}
	f.writes++
	f.lineGone = true
	return before, totals, f.status, nil
}

func (f *fakeEditStore) UpdateRequestedDateGuarded(ctx context.Context, _ string, newDate string, allowedFrom []string, hooks dateEditHooks) (string, string, string, error) {
	if err := f.guard(allowedFrom); err != nil {
		return "", "", f.status, err
	}
	before := f.date
	if hooks.record != nil {
		if err := hooks.record(ctx, nil, before, newDate); err != nil {
			return "", "", f.status, err
		}
	}
	f.writes++
	f.date = newDate
	return before, newDate, f.status, nil
}

// canonQty renders a quantity the way Postgres stores and returns DECIMAL(19,4).
func canonQty(s string) string {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return s
	}
	return formatQty(f)
}

// edPricer resolves a deterministic price: 100¢ per unit at factor 1, so line_total = qty × 100.
type edPricer struct {
	blocked bool
	factor  float64
	err     error
}

func (p *edPricer) ResolvePrice(_ context.Context, in pricing.ResolveInput) (pricing.Breakdown, error) {
	if p.err != nil {
		return pricing.Breakdown{}, p.err
	}
	f := p.factor
	if f == 0 {
		f = 1
	}
	qtyBase := in.QtyEntered * f
	return pricing.Breakdown{
		FinalCents: 100, PriceUOM: "EA", QtyBase: qtyBase,
		LineTotalCents: int64(in.QtyEntered * 100),
		Blocked:        p.blocked, BlockPolicy: "MARGIN_FLOOR",
	}, nil
}

type fakeInv struct{}

func (fakeInv) CheckAvailability(context.Context, string, string, float64) (bool, string, error) {
	return true, "", nil
}
func (fakeInv) CurrentCostCents(context.Context, string, string) (int64, bool, error) {
	return 50, true, nil
}

type fakeCustType struct{ err error }

func (f fakeCustType) CustomerType(context.Context, string) (string, error) {
	return "CONTRACTOR", f.err
}

// edFixture wires a service at `status` with permissive money guards and working pricing seams.
func edFixture(t *testing.T, status string) (*Service, *fakeEditStore, *recordingRecorder) {
	t.Helper()
	store := newFakeEditStore(status)
	rec := &recordingRecorder{}
	svc := NewService(store).WithAudit(rec).withCaller(testCallerFrom).
		WithCancelGuards(&fakeCancelGuards{}, &fakeCancelGuards{}).
		WithReorderPricing(&edPricer{}).WithReorderInventory(fakeInv{}).
		WithCustomerType(fakeCustType{})
	return svc, store, rec
}

// ctxAs threads a StaffContext the way the identity guard stamps it.
func ctxAs(sc identity.StaffContext) context.Context {
	return context.WithValue(context.Background(), ctxKeyOrder{}, sc)
}

// edManager / edStaff are the two authority levels the matrix distinguishes.
func edManager() identity.StaffContext   { return managerAt(edUser, edBranch) }
func edFrontline() identity.StaffContext { return staffAt(edUser, edBranch) }

// applyEdit drives one edit command through the service, so the matrix test can treat all four
// uniformly. A manager caller is used for the money-moving fields and frontline for the date.
func applyEdit(ctx context.Context, svc *Service, field EditField) error {
	switch field {
	case FieldLineQty:
		_, err := svc.EditLineQty(ctx, edOrder, 1, "5")
		return err
	case FieldLineAdd:
		_, err := svc.AddLine(ctx, edOrder, edProd, "3", "EA", "added")
		return err
	case FieldLineRemove:
		_, err := svc.RemoveLine(ctx, edOrder, 1)
		return err
	default:
		_, err := svc.SetRequestedDate(ctx, edOrder, "2026-09-01")
		return err
	}
}

// - the ratified matrix -

// TestEditMatrix walks EVERY (field × status) cell of the ratified edit-state matrix and asserts the
// verdict, allowed cells write, blocked cells refuse with a 409-mapped NotEditableError that NAMES the
// order's status (the acceptance criterion).
//
// This is the mutation-check anchor: widening any rule's `from` set flips a blocked cell here.
func TestEditMatrix(t *testing.T) {
	for _, field := range editFieldOrder {
		field := field
		t.Run(string(field), func(t *testing.T) {
			for _, status := range allStatuses {
				status := status
				t.Run(status, func(t *testing.T) {
					svc, store, _ := edFixture(t, status)
					// Every field is driven by a MANAGER here so the matrix is isolated from the tier
					// gate; the tier gate has its own test below.
					err := applyEdit(ctxAs(edManager()), svc, field)
					wantAllowed := ratifiedMatrix[field][status]
					if wantAllowed {
						if err != nil {
							t.Fatalf("%s in %s: want the edit to be allowed, got %v", field, status, err)
						}
						if store.writes != 1 {
							t.Fatalf("%s in %s: want exactly 1 write, got %d", field, status, store.writes)
						}
						return
					}
					// Blocked cell.
					if err == nil {
						t.Fatalf("%s in %s: want the edit REFUSED, got success", field, status)
					}
					var ne *NotEditableError
					if !errors.As(err, &ne) {
						t.Fatalf("%s in %s: want NotEditableError, got %T: %v", field, status, err, err)
					}
					if ne.Status != status {
						t.Errorf("want the refusal to name status %q, got %q", status, ne.Status)
					}
					if ne.Field != field {
						t.Errorf("want the refusal to name field %q, got %q", field, ne.Field)
					}
					// The message must NAME the status, a caller has to know why it was refused.
					if !strings.Contains(ne.Error(), status) {
						t.Errorf("want the 409 message to contain %q, got %q", status, ne.Error())
					}
					if store.writes != 0 {
						t.Errorf("a refused edit must not write; got %d writes", store.writes)
					}
				})
			}
		})
	}
}

// TestEditMatrix_RatifiedShape pins the matrix itself, so a silent widening of a `from` set is caught
// even if someone also "fixes" the cell tests to match. Line edits die at READY; dates live to
// PARTIALLY_FULFILLED.
func TestEditMatrix_RatifiedShape(t *testing.T) {
	for _, f := range editFieldOrder {
		for _, s := range allStatuses {
			if got, want := editRules[f].from[s], ratifiedMatrix[f][s]; got != want {
				t.Errorf("editRules[%s][%s] = %v, ratified as %v", f, s, got, want)
			}
		}
	}
	for _, f := range []EditField{FieldLineQty, FieldLineAdd, FieldLineRemove} {
		if !editRules[f].managerOnly {
			t.Errorf("%s: ratified as manager-gated (money-moving)", f)
		}
		if !editRules[f].movesMoney {
			t.Errorf("%s: ratified as money-moving (needs the money predicate)", f)
		}
	}
	d := editRules[FieldRequestedDate]
	if d.managerOnly {
		t.Error("requested_date: ratified as open to active in-branch staff, not manager-gated")
	}
	if d.movesMoney {
		t.Error("requested_date: a date is not money, it must not require the money predicate")
	}
}

// - authority -

// TestEdit_Authority covers the ladder: unauthenticated, inactive, cross-branch, and the tier split
// (a frontline staffer may move the date but NOT a line).
func TestEdit_Authority(t *testing.T) {
	cases := []struct {
		name  string
		field EditField
		ctx   func() context.Context
		want  error
	}{
		{"unauthenticated is 401", FieldLineQty,
			func() context.Context { return context.Background() }, ErrUnauthorized},
		{"inactive caller is 403", FieldLineQty,
			func() context.Context { return ctxAs(inactiveStaff(edUser, edBranch)) }, ErrForbidden},
		{"cross-branch manager is 403", FieldLineQty,
			func() context.Context { return ctxAs(managerAt(edUser, histBranchB)) }, ErrForbidden},
		{"frontline may NOT edit a line", FieldLineQty,
			func() context.Context { return ctxAs(edFrontline()) }, ErrForbidden},
		{"frontline may NOT add a line", FieldLineAdd,
			func() context.Context { return ctxAs(edFrontline()) }, ErrForbidden},
		{"frontline may NOT remove a line", FieldLineRemove,
			func() context.Context { return ctxAs(edFrontline()) }, ErrForbidden},
		{"cross-branch frontline cannot move the date", FieldRequestedDate,
			func() context.Context { return ctxAs(staffAt(edUser, histBranchB)) }, ErrForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, _ := edFixture(t, "CONFIRMED")
			err := applyEdit(tc.ctx(), svc, tc.field)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			if store.writes != 0 {
				t.Errorf("a refused edit must not write; got %d", store.writes)
			}
		})
	}
}

// TestEdit_FrontlineMayMoveTheDate is the positive half of the tier split: the date is NOT money, so
// an active in-branch frontline staffer may move it.
func TestEdit_FrontlineMayMoveTheDate(t *testing.T) {
	svc, store, _ := edFixture(t, "READY")
	if _, err := svc.SetRequestedDate(ctxAs(edFrontline()), edOrder, "2026-09-01"); err != nil {
		t.Fatalf("frontline date edit: %v", err)
	}
	if store.date != "2026-09-01" {
		t.Errorf("want the date written, got %q", store.date)
	}
}

// TestEdit_MissingOrderIsNotFound: a missing order must be 404 even for a caller who could not have
// reached it anyway, the authz path must never leak existence.
func TestEdit_MissingOrderIsNotFound(t *testing.T) {
	svc, _, _ := edFixture(t, "CONFIRMED")
	_, err := svc.EditLineQty(ctxAs(edManager()), "00000000-0000-4000-8000-00000000dead", 1, "5")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// - the money predicate -

// TestEdit_MoneyPredicate: a money-moving edit must PROVE no money is attached. A finalized invoice, a
// committed sale, or assessed tax each refuse it; an unwired predicate fails CLOSED.
func TestEdit_MoneyPredicate(t *testing.T) {
	t.Run("finalized invoice blocks a line edit", func(t *testing.T) {
		svc, store, _ := edFixture(t, "CONFIRMED")
		svc.WithCancelGuards(&fakeCancelGuards{hasInvoice: true}, &fakeCancelGuards{})
		if err := applyEdit(ctxAs(edManager()), svc, FieldLineQty); !errors.Is(err, ErrEditBlocked) {
			t.Fatalf("want ErrEditBlocked, got %v", err)
		}
		if store.writes != 0 {
			t.Error("blocked edit must not write")
		}
	})
	t.Run("committed pos sale blocks a line edit", func(t *testing.T) {
		svc, _, _ := edFixture(t, "CONFIRMED")
		svc.WithCancelGuards(&fakeCancelGuards{}, &fakeCancelGuards{hasSale: true})
		if err := applyEdit(ctxAs(edManager()), svc, FieldLineQty); !errors.Is(err, ErrEditBlocked) {
			t.Fatalf("want ErrEditBlocked, got %v", err)
		}
	})
	t.Run("assessed tax blocks a line edit", func(t *testing.T) {
		svc, store, _ := edFixture(t, "CONFIRMED")
		store.taxCents = 1300
		if err := applyEdit(ctxAs(edManager()), svc, FieldLineQty); !errors.Is(err, ErrEditTaxAssessed) {
			t.Fatalf("want ErrEditTaxAssessed, got %v", err)
		}
	})
	t.Run("unwired predicate fails closed", func(t *testing.T) {
		store := newFakeEditStore("CONFIRMED")
		svc := NewService(store).WithAudit(&recordingRecorder{}).withCaller(testCallerFrom).
			WithReorderPricing(&edPricer{}).WithReorderInventory(fakeInv{}).WithCustomerType(fakeCustType{})
		// NOTE: no WithCancelGuards, the predicate cannot be evaluated.
		if err := applyEdit(ctxAs(edManager()), svc, FieldLineQty); !errors.Is(err, ErrEditUnverifiable) {
			t.Fatalf("want ErrEditUnverifiable (fail closed), got %v", err)
		}
	})
	t.Run("assessed tax does NOT block a date edit", func(t *testing.T) {
		// A date is not money, guard 3 must not leak onto the non-money field.
		svc, store, _ := edFixture(t, "CONFIRMED")
		store.taxCents = 1300
		if _, err := svc.SetRequestedDate(ctxAs(edFrontline()), edOrder, "2026-09-01"); err != nil {
			t.Fatalf("date edit on a taxed order must be allowed, got %v", err)
		}
	})
}

// - B2-1: the audit trail is atomic with the mutation -

// TestEdit_RecorderFailureRollsBack is the B2-1 mutation anchor: when the transactional recorder fails,
// the guarded write must abort, no line change, no partial trail. The fake guarded writes call the
// record hook BEFORE applying the mutation (mirroring the real tx's rollback), and recordingRecorder's
// txErr makes RecordTx fail. This test dies if the record hook is ever made best-effort (swallows its
// error) or moved after commit.
//
// MUTATION CHECK (named in the commit): make runLineRecord / the record hook ignore its error →
// TestEdit_RecorderFailureRollsBack and TestIntegration_EditAudit_RecorderFailureRollsBack both fail.
func TestEdit_RecorderFailureRollsBack(t *testing.T) {
	for _, tc := range []struct {
		name string
		do   func(*Service) error
	}{
		{"qty edit", func(s *Service) error { _, e := s.EditLineQty(ctxAs(edManager()), edOrder, 1, "5"); return e }},
		{"add line", func(s *Service) error {
			_, e := s.AddLine(ctxAs(edManager()), edOrder, edProd, "3", "EA", "x")
			return e
		}},
		{"remove line", func(s *Service) error { _, e := s.RemoveLine(ctxAs(edManager()), edOrder, 1); return e }},
		{"date edit", func(s *Service) error {
			_, e := s.SetRequestedDate(ctxAs(edFrontline()), edOrder, "2026-09-01")
			return e
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeEditStore("CONFIRMED")
			rec := &recordingRecorder{txErr: errors.New("audit write failed")}
			svc := NewService(store).WithAudit(rec).withCaller(testCallerFrom).
				WithCancelGuards(&fakeCancelGuards{}, &fakeCancelGuards{}).
				WithReorderPricing(&edPricer{}).WithReorderInventory(fakeInv{}).WithCustomerType(fakeCustType{})

			if err := tc.do(svc); err == nil {
				t.Fatal("want the edit to fail when the recorder fails")
			}
			if store.writes != 0 {
				t.Errorf("a recorder failure must roll the edit back; got %d writes", store.writes)
			}
			for _, r := range rec.records {
				if r.Action == audit.ActOrderChanged {
					t.Error("a rolled-back edit must leave no ORDER_CHANGED rows")
				}
			}
		})
	}
}

// - quantity invariants -

func TestEdit_QuantityInvariants(t *testing.T) {
	cases := []struct {
		name string
		qty  string
	}{
		{"zero is refused (that is a removal)", "0"},
		{"negative is refused", "-3"},
		{"non-numeric is refused", "abc"},
		{"empty is refused", ""},
		{"above the audit-safe bound is refused", "1000000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, _ := edFixture(t, "CONFIRMED")
			_, err := svc.EditLineQty(ctxAs(edManager()), edOrder, 1, tc.qty)
			if !isBadRequest(err) {
				t.Fatalf("want a 400-mapped BadRequest, got %v", err)
			}
			if store.writes != 0 {
				t.Error("an invalid quantity must not write")
			}
		})
	}
}

// TestEdit_QtyBaseBoundIsEnforced: the bound applies to the DERIVED base quantity too, a UOM factor
// can push qty_base past the audit-safe threshold even when the entered qty is well under it. Without
// this the before/after would land in the trail as "[redacted]".
func TestEdit_QtyBaseBoundIsEnforced(t *testing.T) {
	svc, store, _ := edFixture(t, "CONFIRMED")
	svc.WithReorderPricing(&edPricer{factor: 1000}) // 2000 entered × 1000 = 2,000,000 base
	_, err := svc.EditLineQty(ctxAs(edManager()), edOrder, 1, "2000")
	if !isBadRequest(err) {
		t.Fatalf("want the derived base qty refused as a 400, got %v", err)
	}
	if store.writes != 0 {
		t.Error("must not write")
	}
}

// TestEdit_CannotCutBelowFulfilled: the 0008 CHECK (fulfilled_base <= qty_base) surfaces as a NAMED
// refusal rather than an opaque constraint violation.
func TestEdit_CannotCutBelowFulfilled(t *testing.T) {
	svc, store, _ := edFixture(t, "CONFIRMED")
	store.fulfilled = 10
	_, err := svc.EditLineQty(ctxAs(edManager()), edOrder, 1, "5")
	if !errors.Is(err, ErrLineInUse) {
		t.Fatalf("want ErrLineInUse, got %v", err)
	}
	if store.writes != 0 {
		t.Error("must not write")
	}
}

// TestEdit_CannotRemoveCommittedLine: a line the yard has allocated or fulfilled against is not
// removable.
func TestEdit_CannotRemoveCommittedLine(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		allocated, fulfilled float64
	}{
		{"allocated", 4, 0},
		{"fulfilled", 0, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, _ := edFixture(t, "PICKING")
			store.allocated, store.fulfilled = tc.allocated, tc.fulfilled
			_, err := svc.RemoveLine(ctxAs(edManager()), edOrder, 1)
			if !errors.Is(err, ErrLineInUse) {
				t.Fatalf("want ErrLineInUse, got %v", err)
			}
			if store.writes != 0 {
				t.Error("must not write")
			}
		})
	}
}

// TestEdit_BelowFloorPriceIsRefused: repricing that trips the A-20 margin gate refuses the edit rather
// than committing a below-floor line.
func TestEdit_BelowFloorPriceIsRefused(t *testing.T) {
	svc, store, _ := edFixture(t, "CONFIRMED")
	svc.WithReorderPricing(&edPricer{blocked: true})
	_, err := svc.EditLineQty(ctxAs(edManager()), edOrder, 1, "5")
	var pb *PriceBlockedError
	if !errors.As(err, &pb) {
		t.Fatalf("want PriceBlockedError, got %v", err)
	}
	if !strings.Contains(pb.Error(), "MARGIN_FLOOR") {
		t.Errorf("want the block policy named, got %q", pb.Error())
	}
	if store.writes != 0 {
		t.Error("a below-floor edit must not write")
	}
}

// TestEdit_UnwiredPricingFailsClosed: without a pricing seam an edited line cannot be priced, so the
// edit is refused rather than persisted at a stale or zero price.
func TestEdit_UnwiredPricingFailsClosed(t *testing.T) {
	store := newFakeEditStore("CONFIRMED")
	svc := NewService(store).WithAudit(&recordingRecorder{}).withCaller(testCallerFrom).
		WithCancelGuards(&fakeCancelGuards{}, &fakeCancelGuards{})
	// No pricing/inventory/customer-type seams.
	if _, err := svc.EditLineQty(ctxAs(edManager()), edOrder, 1, "5"); !errors.Is(err, ErrEditNotConfigured) {
		t.Fatalf("want ErrEditNotConfigured (fail closed), got %v", err)
	}
	if store.writes != 0 {
		t.Error("must not write")
	}
}

// - the change trail -

// changeRows returns the ORDER_CHANGED rows the recorder captured, keyed by field.
func changeRows(rec *recordingRecorder) map[string]audit.SensitiveAccess {
	out := map[string]audit.SensitiveAccess{}
	for _, r := range rec.records {
		if r.Action != audit.ActOrderChanged {
			continue
		}
		if f, ok := r.Extra["field"].(string); ok {
			out[f] = r
		}
	}
	return out
}

// TestEdit_QtyWritesReconstructableTrail: a qty edit records every column that moved, with a
// before→after pair, attributed to the caller, the acceptance criterion that an edit is
// reconstructable from the audit rows alone.
func TestEdit_QtyWritesReconstructableTrail(t *testing.T) {
	svc, _, rec := edFixture(t, "CONFIRMED")
	if _, err := svc.EditLineQty(ctxAs(edManager()), edOrder, 1, "5"); err != nil {
		t.Fatalf("edit: %v", err)
	}
	rows := changeRows(rec)
	for _, want := range []string{"qty_entered", "qty_base", "line_total_cents", "subtotal_cents", "total_cents"} {
		if _, ok := rows[want]; !ok {
			t.Errorf("want an ORDER_CHANGED row for %q; got fields %v", want, keysOf(rows))
		}
	}
	qty := rows["qty_entered"]
	if got := fmt.Sprint(qty.Extra["before"]); got != "2.0000" {
		t.Errorf("qty_entered before: want 2.0000, got %v", got)
	}
	if got := fmt.Sprint(qty.Extra["after"]); got != "5.0000" {
		t.Errorf("qty_entered after: want the canonical stored value 5.0000, got %v", got)
	}
	if qty.ActorID != edUser {
		t.Errorf("want the edit attributed to %s, got %s", edUser, qty.ActorID)
	}
	if qty.EntityType != "order" || qty.EntityID != edOrder {
		t.Errorf("want entity order/%s, got %s/%s", edOrder, qty.EntityType, qty.EntityID)
	}
	// Money must ride as an INTEGER, never a string, a string would be exposed to the audit
	// boundary's cleartext redaction (7+ consecutive digits → "[redacted]").
	lt := rows["line_total_cents"]
	if _, ok := lt.Extra["after"].(int64); !ok {
		t.Errorf("line_total_cents after must be int64 (redaction-safe), got %T", lt.Extra["after"])
	}
	// unit_price_cents did not move (the fake prices at a flat 100¢), so it must emit NO row, the
	// trail records transitions, not attempts.
	if _, ok := rows["unit_price_cents"]; ok {
		t.Error("unit_price_cents did not change; it must not emit a row")
	}
}

// TestEdit_RemoveSnapshotsThePreImage is condition C1: the removal row must capture WHAT was removed,
// because the line no longer exists to be read afterwards.
func TestEdit_RemoveSnapshotsThePreImage(t *testing.T) {
	svc, _, rec := edFixture(t, "PICKING")
	if _, err := svc.RemoveLine(ctxAs(edManager()), edOrder, 1); err != nil {
		t.Fatalf("remove: %v", err)
	}
	rows := changeRows(rec)
	removed, ok := rows["line_removed"]
	if !ok {
		t.Fatalf("want a line_removed row; got %v", keysOf(rows))
	}
	if got := fmt.Sprint(removed.Extra["before"]); got != edProd {
		t.Errorf("want the removed product id %s snapshotted, got %v", edProd, got)
	}
	if got := fmt.Sprint(rows["qty_base"].Extra["before"]); got != "2.0000" {
		t.Errorf("want the removed qty_base snapshotted, got %v", got)
	}
	if _, ok := rows["line_removed"].Extra["after"]; ok {
		t.Error("a removal has no post-image; `after` must be absent")
	}
}

// TestEdit_AddRecordsWhatWasAdded: a creation has no pre-image, so the trail must name the product and
// the quantity that entered the order.
func TestEdit_AddRecordsWhatWasAdded(t *testing.T) {
	svc, _, rec := edFixture(t, "DRAFT")
	if _, err := svc.AddLine(ctxAs(edManager()), edOrder, edProd, "3", "EA", "new line"); err != nil {
		t.Fatalf("add: %v", err)
	}
	rows := changeRows(rec)
	added, ok := rows["line_added"]
	if !ok {
		t.Fatalf("want a line_added row; got %v", keysOf(rows))
	}
	if got := fmt.Sprint(added.Extra["after"]); got != edProd {
		t.Errorf("want the added product id, got %v", got)
	}
	if _, ok := added.Extra["before"]; ok {
		t.Error("a creation has no pre-image; `before` must be absent")
	}
}

// TestEdit_DateChangeIsRecorded covers the non-money field's trail.
func TestEdit_DateChangeIsRecorded(t *testing.T) {
	svc, _, rec := edFixture(t, "READY")
	if _, err := svc.SetRequestedDate(ctxAs(edFrontline()), edOrder, "2026-09-01"); err != nil {
		t.Fatalf("date edit: %v", err)
	}
	row, ok := changeRows(rec)["requested_date"]
	if !ok {
		t.Fatal("want a requested_date row")
	}
	if fmt.Sprint(row.Extra["before"]) != "2026-08-01" || fmt.Sprint(row.Extra["after"]) != "2026-09-01" {
		t.Errorf("want 2026-08-01 → 2026-09-01, got %v → %v", row.Extra["before"], row.Extra["after"])
	}
}

// TestEdit_NoOpEmitsNoRow: re-submitting the same date is not a change, so it must leave no trail row.
func TestEdit_NoOpEmitsNoRow(t *testing.T) {
	svc, store, rec := edFixture(t, "READY")
	if _, err := svc.SetRequestedDate(ctxAs(edFrontline()), edOrder, store.date); err != nil {
		t.Fatalf("date edit: %v", err)
	}
	if _, ok := changeRows(rec)["requested_date"]; ok {
		t.Error("an unchanged value must not emit a change row")
	}
}

// - the read-side matrix projection -

// TestEditableFields_MatchTheMatrix: editable_fields on the order read is computed from the SAME rules
// the write path enforces, so the client can never render a control the server would refuse.
func TestEditableFields_MatchTheMatrix(t *testing.T) {
	for _, status := range allStatuses {
		t.Run(status, func(t *testing.T) {
			svc, _, _ := edFixture(t, status)
			view, err := svc.OrderRead(ctxAs(edManager()), edOrder)
			if err != nil {
				t.Fatalf("order read: %v", err)
			}
			got := map[string]bool{}
			for _, f := range view.EditableFields {
				got[f] = true
			}
			for _, f := range editFieldOrder {
				want := ratifiedMatrix[f][status]
				if got[string(f)] != want {
					t.Errorf("%s in %s: editable_fields says %v, matrix says %v", f, status, got[string(f)], want)
				}
			}
		})
	}
}

// TestEditableFields_RespectTier: a frontline caller is offered the date but never a line edit.
func TestEditableFields_RespectTier(t *testing.T) {
	svc, _, _ := edFixture(t, "CONFIRMED")
	view, err := svc.OrderRead(ctxAs(edFrontline()), edOrder)
	if err != nil {
		t.Fatalf("order read: %v", err)
	}
	for _, f := range view.EditableFields {
		if f != string(FieldRequestedDate) {
			t.Errorf("frontline must only be offered the date, got %q", f)
		}
	}
	if len(view.EditableFields) != 1 {
		t.Errorf("want exactly the date offered, got %v", view.EditableFields)
	}
}

// TestEditableFields_MoneyGuardOmitsRatherThanFails: a blocked money predicate must OMIT the line
// fields from the offer, never fail the order read (the softer offer-side rule, mirroring
// canOfferCancel).
func TestEditableFields_MoneyGuardOmitsRatherThanFails(t *testing.T) {
	svc, store, _ := edFixture(t, "CONFIRMED")
	store.taxCents = 1300
	view, err := svc.OrderRead(ctxAs(edManager()), edOrder)
	if err != nil {
		t.Fatalf("the order read must still succeed, got %v", err)
	}
	for _, f := range view.EditableFields {
		if f != string(FieldRequestedDate) {
			t.Errorf("a taxed order must offer no line edits, got %q", f)
		}
	}
}

func keysOf(m map[string]audit.SensitiveAccess) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
