package till

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5"
)

// - DB-free fakes (the internal/pricing service_test pattern: fake store + fake CardCharger +
//    fake clock + fake StaffContext; no live processor call anywhere) -

var fixedTime = time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

type fakeStore struct {
	sessions      map[string]*TillSession
	openByDrawer  map[string]string // "branch|station" -> sessionID (non-closed)
	payments      map[string]*PaymentRow
	paymentByKey  map[string]string
	allocations   map[string][]int64
	movements     map[string][]MovementRow
	tenderActuals map[string]map[string]int64
	// tenderSnapshots mirrors the stored till_tender_totals.expected_cents column: the close-time
	// capture of the payments sums the count's upsert writes (repository.go UpsertTenderActuals).
	tenderSnapshots map[string]map[string]int64
	denoms          map[string][]DenominationCount
	policies        map[string]TillPolicy
	seq             int
	insertCalls     int

	// - W5 A-05 fake state -
	nsfPolicy    NSFPolicy              // the effective policy returned by NSFPolicyFor
	nsfByPayment map[string]bool        // payments already marked BOUNCED via MarkNSF
	holds        map[string]*CreditHold // by hold id
	activeHold   map[string]string      // customerID -> active hold id (one ACTIVE per customer)
	attempts     []PaymentAttempt       // recorded declined/errored attempts
	emitted      []string               // outbox topics emitted (SetCompleted / MarkNSF)
	lastNSFIn    nsfReverseInput        // the last resolved instruction MarkNSF received (fee assertion)

	lastAllocatedBy string        // Q-044: the allocated_by the last InsertAllocation received
	txMovements     []MovementRow // movements written through InsertMovementTx (the refund path)
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		sessions:        map[string]*TillSession{},
		openByDrawer:    map[string]string{},
		payments:        map[string]*PaymentRow{},
		paymentByKey:    map[string]string{},
		allocations:     map[string][]int64{},
		movements:       map[string][]MovementRow{},
		tenderActuals:   map[string]map[string]int64{},
		tenderSnapshots: map[string]map[string]int64{},
		denoms:          map[string][]DenominationCount{},
		policies:        map[string]TillPolicy{},
		nsfPolicy:       NSFPolicy{FeeCents: 4500, FeeTaxable: false, FeeAccountCode: "4200", AutoHold: true},
		nsfByPayment:    map[string]bool{},
		holds:           map[string]*CreditHold{},
		activeHold:      map[string]string{},
	}
}

func (f *fakeStore) id(prefix string) string { f.seq++; return fmt.Sprintf("%s-%d", prefix, f.seq) }

func (f *fakeStore) seedSession(s TillSession) string {
	if s.ID == "" {
		s.ID = f.id("sess")
	}
	cp := s
	f.sessions[cp.ID] = &cp
	if s.Status != StatusClosed {
		f.openByDrawer[s.BranchID+"|"+s.Station] = cp.ID
	}
	return cp.ID
}

func (f *fakeStore) seedPayment(p PaymentRow) string {
	id := f.id("pay")
	cp := p
	f.payments[id] = &cp
	if p.IdempotencyKey != "" {
		f.paymentByKey[p.IdempotencyKey] = id
	}
	return id
}

func (f *fakeStore) GetSessionTx(_ context.Context, _ pgx.Tx, id string) (TillSession, error) {
	return f.GetSession(context.Background(), id)
}

// FindOpenSession, interface compliance for the DIB-488 rev 5 store method (same class of touch
// as the pos fake's ReadStockUnitMeta in rev 1: the interface must compile).
func (f *fakeStore) FindOpenSession(_ context.Context, branchID, station string) (string, time.Time, bool, error) {
	for id, s := range f.sessions {
		if s.BranchID == branchID && s.Station == station && s.Status == StatusOpen {
			return id, fixedTime, true, nil
		}
	}
	return "", time.Time{}, false, nil
}

func (f *fakeStore) GetSession(_ context.Context, id string) (TillSession, error) {
	s, ok := f.sessions[id]
	if !ok {
		return TillSession{}, errNotFound
	}
	return *s, nil
}

func (f *fakeStore) FindPaymentByKey(_ context.Context, _ pgx.Tx, key string) (string, string, bool, error) {
	if key == "" {
		return "", "", false, nil
	}
	id, ok := f.paymentByKey[key]
	if !ok {
		return "", "", false, nil
	}
	var gw string
	if p, has := f.payments[id]; has {
		gw = p.GatewayRef
	}
	return id, gw, ok, nil
}

func (f *fakeStore) InsertPayment(_ context.Context, _ pgx.Tx, p PaymentRow) (string, string, bool, error) {
	if p.IdempotencyKey != "" {
		if id, ok := f.paymentByKey[p.IdempotencyKey]; ok {
			return id, "", false, nil
		}
	}
	id := f.id("pay")
	cp := p
	f.payments[id] = &cp
	if p.IdempotencyKey != "" {
		f.paymentByKey[p.IdempotencyKey] = id
	}
	f.insertCalls++
	return id, "PMT-" + id, true, nil
}

func (f *fakeStore) InsertAllocation(_ context.Context, _ pgx.Tx, paymentID, _ string, amountCents int64, allocatedBy string) error {
	f.allocations[paymentID] = append(f.allocations[paymentID], amountCents)
	f.lastAllocatedBy = allocatedBy
	return nil
}

func (f *fakeStore) AllocatedTotal(_ context.Context, _ pgx.Tx, paymentID string) (int64, error) {
	var sum int64
	for _, a := range f.allocations[paymentID] {
		sum += a
	}
	return sum, nil
}

func (f *fakeStore) PaymentAmount(_ context.Context, _ pgx.Tx, paymentID string) (int64, error) {
	p, ok := f.payments[paymentID]
	if !ok {
		return 0, errNotFound
	}
	return p.AmountCents, nil
}

func (f *fakeStore) OpenSession(_ context.Context, s TillSession, denoms []DenominationCount) (string, error) {
	key := s.BranchID + "|" + s.Station
	if _, ok := f.openByDrawer[key]; ok {
		return "", errConflict
	}
	id := f.id("sess")
	s.ID = id
	cp := s
	f.sessions[id] = &cp
	f.openByDrawer[key] = id
	if len(denoms) > 0 {
		f.denoms[id] = append(f.denoms[id], denoms...)
	}
	return id, nil
}

func (f *fakeStore) SessionTenders(_ context.Context, sessionID string) ([]TenderTotal, error) {
	var out []TenderTotal
	for method, actual := range f.tenderActuals[sessionID] {
		exp := f.tenderSnapshots[sessionID][method]
		out = append(out, TenderTotal{Method: method, ExpectedCents: exp, ActualCents: actual, VarianceCents: actual - exp})
	}
	return out, nil
}

func (f *fakeStore) SessionMovements(_ context.Context, sessionID string) ([]Movement, error) {
	var out []Movement
	for i, m := range f.movements[sessionID] {
		out = append(out, Movement{
			ID: fmt.Sprintf("mv-%d", i), MovementType: m.MovementType, Method: m.Method,
			AmountCents: m.AmountCents, Reason: m.Reason, Reference: m.Reference,
		})
	}
	return out, nil
}

func (f *fakeStore) InsertMovement(_ context.Context, m MovementRow) error {
	f.movements[m.TillSessionID] = append(f.movements[m.TillSessionID], m)
	return nil
}

func (f *fakeStore) InsertMovementTx(_ context.Context, _ pgx.Tx, m MovementRow) error {
	f.movements[m.TillSessionID] = append(f.movements[m.TillSessionID], m)
	f.txMovements = append(f.txMovements, m)
	return nil
}

func (f *fakeStore) ExpectedTenders(_ context.Context, sessionID string) (map[string]int64, error) {
	out := map[string]int64{}
	for _, p := range f.payments {
		if p.TillSessionID == sessionID && p.Status == PaymentReceived {
			out[p.Method] += p.AmountCents
		}
	}
	return out, nil
}

func (f *fakeStore) TenderActuals(_ context.Context, sessionID string) (map[string]int64, error) {
	out := map[string]int64{}
	for k, v := range f.tenderActuals[sessionID] {
		out[k] = v
	}
	return out, nil
}

func (f *fakeStore) MovementTotalsByMethod(_ context.Context, sessionID string) (map[string]MovementTotals, error) {
	out := map[string]MovementTotals{}
	for _, m := range f.movements[sessionID] {
		mt := out[m.Method]
		switch m.MovementType {
		case MovementPaidIn:
			mt.PaidIn += m.AmountCents
		case MovementPaidOut, MovementDrop:
			mt.PaidOut += m.AmountCents
		case MovementLodgement:
			mt.Lodgement += m.AmountCents
		}
		out[m.Method] = mt
	}
	return out, nil
}

func (f *fakeStore) UpsertTenderActuals(_ context.Context, sessionID string, actuals []TenderActual) error {
	if f.tenderActuals[sessionID] == nil {
		f.tenderActuals[sessionID] = map[string]int64{}
	}
	if f.tenderSnapshots[sessionID] == nil {
		f.tenderSnapshots[sessionID] = map[string]int64{}
	}
	for _, a := range actuals {
		f.tenderActuals[sessionID][a.Method] = a.ActualCents
		// The stored snapshot is the close-time capture of the payments sums (the real upsert's
		// SELECT): Σ RECEIVED payments of this method as of the count.
		var sum int64
		for _, p := range f.payments {
			if p.TillSessionID == sessionID && p.Method == a.Method && p.Status == PaymentReceived {
				sum += p.AmountCents
			}
		}
		f.tenderSnapshots[sessionID][a.Method] = sum
	}
	return nil
}

func (f *fakeStore) InsertDenominations(_ context.Context, sessionID, phase string, denoms []DenominationCount) error {
	for _, d := range denoms {
		d.Phase = phase
		f.denoms[sessionID] = append(f.denoms[sessionID], d)
	}
	return nil
}

func (f *fakeStore) SetCounted(_ context.Context, sessionID, countedBy string, _ time.Time, closingFloatCents int64) error {
	s, ok := f.sessions[sessionID]
	if !ok || s.Status != StatusOpen {
		return errConflict
	}
	s.Status = StatusCounted
	s.ClosingFloatCents = closingFloatCents
	return nil
}

func (f *fakeStore) SetCompleted(_ context.Context, sessionID, branchID, completedBy string, _ time.Time, overShortCents, countedCashCents int64, overrideNotes string) error {
	s, ok := f.sessions[sessionID]
	if !ok || s.Status != StatusCounted {
		return errConflict
	}
	s.Status = StatusClosed
	s.OverrideNotes = overrideNotes
	delete(f.openByDrawer, s.BranchID+"|"+s.Station)
	f.emitted = append(f.emitted, "till.session.completed") // same-tx emission (AC9)
	return nil
}

// - W5 A-05 fake store methods -

func (f *fakeStore) PaymentNSFContext(_ context.Context, paymentID string) (string, string, string, string, int64, bool, error) {
	p, ok := f.payments[paymentID]
	if !ok {
		return "", "", "", "", 0, false, nil
	}
	status := p.Status
	if f.nsfByPayment[paymentID] {
		status = "BOUNCED"
	}
	return p.CustomerID, p.BranchID, status, p.Method, p.AmountCents, true, nil
}

func (f *fakeStore) NSFPolicyFor(_ context.Context, _ string) (NSFPolicy, error) {
	return f.nsfPolicy, nil
}

func (f *fakeStore) MarkNSF(_ context.Context, in nsfReverseInput) (nsfReverseResult, error) {
	p, ok := f.payments[in.PaymentID]
	if !ok {
		return nsfReverseResult{}, errNotFound
	}
	if f.nsfByPayment[in.PaymentID] {
		return nsfReverseResult{}, errAlreadyBounced
	}
	f.lastNSFIn = in
	f.nsfByPayment[in.PaymentID] = true
	p.Status = "BOUNCED"
	var reversed int64
	for _, a := range f.allocations[in.PaymentID] {
		reversed += a
	}
	res := nsfReverseResult{
		CustomerID: p.CustomerID, BranchID: p.BranchID, Method: p.Method,
		AmountCents: p.AmountCents, ReversedCents: reversed, ReopenedInvoices: len(f.allocations[in.PaymentID]),
	}
	delete(f.allocations, in.PaymentID)
	if in.AutoHold && p.CustomerID != "" {
		if _, held := f.activeHold[p.CustomerID]; !held {
			id := f.id("hold")
			f.holds[id] = &CreditHold{ID: id, CustomerID: p.CustomerID, SourcePaymentID: in.PaymentID, Status: HoldActive, Reason: in.Reason}
			f.activeHold[p.CustomerID] = id
			res.HoldPlaced = true
		}
	}
	f.emitted = append(f.emitted, "payment.bounced") // same-tx emission (AC3)
	return res, nil
}

func (f *fakeStore) HasActiveHold(_ context.Context, customerID string) (bool, error) {
	if customerID == "" {
		return false, nil
	}
	_, held := f.activeHold[customerID]
	return held, nil
}

func (f *fakeStore) ReleaseHold(_ context.Context, holdID, releasedBy, reason string) (string, error) {
	h, ok := f.holds[holdID]
	if !ok || h.Status != HoldActive {
		return "", clientErr(404, "till: no ACTIVE credit hold for id %s", holdID)
	}
	h.Status = HoldReleased
	h.ReleaseReason = reason
	h.ReleasedBy = releasedBy
	delete(f.activeHold, h.CustomerID)
	return h.CustomerID, nil
}

func (f *fakeStore) ListCreditHolds(_ context.Context, status string) ([]CreditHold, error) {
	var out []CreditHold
	for _, h := range f.holds {
		if status == "" || h.Status == status {
			out = append(out, *h)
		}
	}
	return out, nil
}

func (f *fakeStore) ListBouncedPayments(_ context.Context, gatewayRef string) ([]BouncedPayment, error) {
	var out []BouncedPayment
	for id := range f.nsfByPayment {
		p := f.payments[id]
		if p == nil {
			continue
		}
		if gatewayRef != "" && p.GatewayRef != gatewayRef {
			continue
		}
		out = append(out, BouncedPayment{PaymentID: id, CustomerID: p.CustomerID, BranchID: p.BranchID, Method: p.Method, AmountCents: p.AmountCents, GatewayRef: p.GatewayRef, FeeCents: f.nsfPolicy.FeeCents})
	}
	return out, nil
}

func (f *fakeStore) UpdateNSFPolicy(_ context.Context, in NSFPolicy) error {
	f.nsfPolicy = in
	return nil
}

func (f *fakeStore) RecordPaymentAttempt(_ context.Context, in PaymentAttempt) error {
	for _, a := range f.attempts {
		if in.IdempotencyKey != "" && a.IdempotencyKey == in.IdempotencyKey {
			return nil // idempotent
		}
	}
	f.attempts = append(f.attempts, in)
	return nil
}

func (f *fakeStore) Policy(_ context.Context, branchID string) (TillPolicy, error) {
	if p, ok := f.policies[branchID]; ok {
		return p, nil
	}
	return TillPolicy{BranchID: branchID, VarianceThresholdCents: 0, CloseGrain: "SESSION", RequireBlindClose: true}, nil
}

// - Q-044 history reads (fake projections over the fake payment map) -

func (f *fakeStore) PaymentHistory(_ context.Context, paymentID string) (PaymentHistory, bool, error) {
	p, ok := f.payments[paymentID]
	if !ok {
		return PaymentHistory{}, false, nil
	}
	return PaymentHistory{
		PaymentID: paymentID, PaymentNo: "PMT-" + paymentID, BranchID: p.BranchID,
		Method: p.Method, Status: p.Status, AmountCents: p.AmountCents,
		ReceivedBy: p.ReceivedBy, Allocations: []AllocationRecord{},
	}, true, nil
}

func (f *fakeStore) InvoicePayments(_ context.Context, invoiceID string) (InvoicePayments, bool, error) {
	return InvoicePayments{InvoiceID: invoiceID, Items: []InvoicePaymentRecord{}}, true, nil
}

// - fake CardCharger (deterministic; NO live call, the gable payment_test mock pattern) -

type fakeCharger struct {
	result     ChargeResult
	err        error
	calls      int
	lastAmount int64
	lastToken  string
	lastRef    string
}

func (c *fakeCharger) Charge(_ context.Context, _ pgx.Tx, amountCents int64, token, ref string) (ChargeResult, error) {
	c.calls++
	c.lastAmount = amountCents
	c.lastToken = token
	c.lastRef = ref
	return c.result, c.err
}

// - fake StaffContext threading -

type ctxKeyT struct{}

func ctxWith(ctx context.Context, sc identity.StaffContext) context.Context {
	return context.WithValue(ctx, ctxKeyT{}, sc)
}

func testCallerFrom(ctx context.Context) (identity.StaffContext, bool) {
	sc, ok := ctx.Value(ctxKeyT{}).(identity.StaffContext)
	return sc, ok
}

func staffAt(uid, branch string) identity.StaffContext {
	return identity.StaffContext{UserID: uid, RoleCode: "INSIDE_SALES", Tier: identity.TierFrontline,
		Scope: identity.ScopeBranch, Status: "ACTIVE", Branches: map[string]bool{branch: true}}
}

func managerAt(uid, branch string) identity.StaffContext {
	return identity.StaffContext{UserID: uid, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Status: "ACTIVE", Branches: map[string]bool{branch: true}}
}

func newTestSvc(store tillStore, charger CardCharger) *Service {
	s := NewService(store).WithClock(func() time.Time { return fixedTime })
	if charger != nil {
		s.WithCardCharger(charger)
	}
	return s.withCaller(testCallerFrom)
}

const (
	trenton  = "br-trenton"
	kingston = "br-kingston"
)

// recordingTx is a no-op pgx.Tx that records whether the till manages the tx lifecycle (it must NOT -
// the caller owns commit/rollback). Embeds pgx.Tx so the unused methods exist; only Commit/Rollback
// are overridden (and never called by TakeTender).
type recordingTx struct {
	pgx.Tx
	committed, rolledBack bool
}

func (t *recordingTx) Commit(context.Context) error   { t.committed = true; return nil }
func (t *recordingTx) Rollback(context.Context) error { t.rolledBack = true; return nil }

// - helpers -

func wantStatus(t *testing.T, err error, code int) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error with status %d, got nil", code)
	}
	var ce clientError
	if !errors.As(err, &ce) {
		t.Fatalf("want clientError(%d), got %T: %v", code, err, err)
	}
	if ce.status != code {
		t.Fatalf("want status %d, got %d: %v", code, ce.status, err)
	}
}

func openSession(t *testing.T, store *fakeStore, branch string) string {
	t.Helper()
	return store.seedSession(TillSession{BranchID: branch, Station: "MAIN", Status: StatusOpen, OpenedBy: "u-opener", FloatCents: 20000})
}

// - U1-U4: OpenTill -

func TestOpenTill_CreatesOpenSession(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))

	id, err := svc.OpenTill(ctx, OpenTillInput{BranchID: trenton, Station: "MAIN", FloatCents: 20000})
	if err != nil {
		t.Fatalf("OpenTill: %v", err)
	}
	s := store.sessions[id]
	if s.Status != StatusOpen || s.OpenedBy != "u-sarah" || s.FloatCents != 20000 {
		t.Fatalf("unexpected session: %+v", s)
	}
}

func TestOpenTill_DoubleOpen_Conflicts(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))

	if _, err := svc.OpenTill(ctx, OpenTillInput{BranchID: trenton, Station: "MAIN", FloatCents: 20000}); err != nil {
		t.Fatalf("first open: %v", err)
	}
	_, err := svc.OpenTill(ctx, OpenTillInput{BranchID: trenton, Station: "MAIN", FloatCents: 20000})
	wantStatus(t, err, 409)
}

func TestOpenTill_OpeningDenoms_MustEqualFloat(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))

	// 10×$10 + 20×$5 = 20000; declare a mismatched float of 19000 → 422.
	_, err := svc.OpenTill(ctx, OpenTillInput{BranchID: trenton, Station: "MAIN", FloatCents: 19000,
		OpeningDenominations: []DenominationCount{{DenominationCents: 1000, Quantity: 10}, {DenominationCents: 500, Quantity: 20}}})
	wantStatus(t, err, 422)
}

func TestOpenTill_BranchScope_Forbidden(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	// Sarah can reach Trenton only; she opens Kingston → 403.
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	_, err := svc.OpenTill(ctx, OpenTillInput{BranchID: kingston, Station: "MAIN", FloatCents: 20000})
	wantStatus(t, err, 403)
}

// - U5-U16, U29, U31, U32: TakeTender + Allocate -

func TestTakeTender_Cash_WritesPaymentRow(t *testing.T) {
	store := newFakeStore()
	charger := &fakeCharger{}
	svc := newTestSvc(store, charger)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)

	res, err := svc.TakeTender(ctx, nil, TenderInput{TillSessionID: sid, Method: MethodCash, AmountCents: 4500, IdempotencyKey: "k1"})
	if err != nil {
		t.Fatalf("TakeTender: %v", err)
	}
	p := store.payments[res.PaymentID]
	if p.Method != MethodCash || p.Status != PaymentReceived || p.AmountCents != 4500 || p.ReceivedBy != "u-sarah" || p.BranchID != trenton {
		t.Fatalf("unexpected payment row: %+v", p)
	}
	if res.GatewayRef != "" {
		t.Fatalf("cash tender must carry no gateway_ref, got %q", res.GatewayRef)
	}
	if charger.calls != 0 {
		t.Fatalf("cash must not call the card rail; calls=%d", charger.calls)
	}
}

func TestTakeTender_Cash_OfflineFirstClass(t *testing.T) {
	store := newFakeStore()
	// A charger that would error if called, proving cash never touches it.
	charger := &fakeCharger{err: errors.New("gateway unreachable")}
	svc := newTestSvc(store, charger)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)

	res, err := svc.TakeTender(ctx, nil, TenderInput{TillSessionID: sid, Method: MethodCash, AmountCents: 4500, Offline: true, IdempotencyKey: "k1"})
	if err != nil {
		t.Fatalf("offline cash should still write: %v", err)
	}
	if !store.payments[res.PaymentID].OfflineCaptured {
		t.Fatal("offline_captured must be true")
	}
	if charger.calls != 0 {
		t.Fatal("offline cash must not call the card rail")
	}
}

func TestTakeTender_IdempotentReplay_NoDoubleCount(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)

	first, err := svc.TakeTender(ctx, nil, TenderInput{TillSessionID: sid, Method: MethodCash, AmountCents: 4500, IdempotencyKey: "K"})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := svc.TakeTender(ctx, nil, TenderInput{TillSessionID: sid, Method: MethodCash, AmountCents: 4500, IdempotencyKey: "K"})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if first.PaymentID != second.PaymentID {
		t.Fatalf("replay returned a different id: %s != %s", first.PaymentID, second.PaymentID)
	}
	if store.insertCalls != 1 {
		t.Fatalf("replay double-counted: insertCalls=%d", store.insertCalls)
	}
	exp, _ := store.ExpectedTenders(ctx, sid)
	if exp[MethodCash] != 4500 {
		t.Fatalf("expected cash changed on replay: %d", exp[MethodCash])
	}
}

func TestTakeTender_AmountNonPositive_Rejected(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)

	_, err := svc.TakeTender(ctx, nil, TenderInput{TillSessionID: sid, Method: MethodCash, AmountCents: 0, IdempotencyKey: "k"})
	wantStatus(t, err, 422)
}

func TestTakeTender_Card_CapturesAndPersistsGatewayRef(t *testing.T) {
	store := newFakeStore()
	charger := &fakeCharger{result: ChargeResult{ProcessorRef: "rp_sim_abc", Status: ChargeCaptured}}
	svc := newTestSvc(store, charger)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)

	res, err := svc.TakeTender(ctx, nil, TenderInput{TillSessionID: sid, Method: MethodCard, AmountCents: 12000,
		PaymentToken: "tok_xyz", CardType: CardCredit, IdempotencyKey: "k"})
	if err != nil {
		t.Fatalf("card capture: %v", err)
	}
	if charger.calls != 1 {
		t.Fatalf("charger must be called once; calls=%d", charger.calls)
	}
	p := store.payments[res.PaymentID]
	if p.GatewayRef != "rp_sim_abc" || p.Status != PaymentReceived || p.CardType != CardCredit {
		t.Fatalf("unexpected card payment: %+v", p)
	}
	// The result surfaces the SAME gateway_ref persisted to the row (the single source of truth A-19
	// threads into the Sale response + the payment.received event).
	if res.GatewayRef != "rp_sim_abc" {
		t.Fatalf("TakeTender result must carry the gateway_ref, got %q", res.GatewayRef)
	}
}

func TestTakeTender_Card_Declined_NoReceivedRow(t *testing.T) {
	store := newFakeStore()
	charger := &fakeCharger{result: ChargeResult{Status: ChargeFailed}}
	svc := newTestSvc(store, charger)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)

	_, err := svc.TakeTender(ctx, nil, TenderInput{TillSessionID: sid, Method: MethodCard, AmountCents: 12000,
		PaymentToken: "tok", CardType: CardCredit, IdempotencyKey: "k"})
	wantStatus(t, err, 402)
	if store.insertCalls != 0 {
		t.Fatalf("declined card must not write a RECEIVED row; insertCalls=%d", store.insertCalls)
	}
}

func TestTakeTender_Card_CaptureBeforeReturn(t *testing.T) {
	store := newFakeStore()
	charger := &fakeCharger{result: ChargeResult{ProcessorRef: "rp_1", Status: ChargeCaptured}}
	svc := newTestSvc(store, charger)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)

	res, err := svc.TakeTender(ctx, nil, TenderInput{TillSessionID: sid, Method: MethodCard, AmountCents: 100,
		PaymentToken: "tok", CardType: CardCredit, IdempotencyKey: "k"})
	if err != nil {
		t.Fatalf("card: %v", err)
	}
	// On a successful return, the capture has already happened and its ref is on the persisted row AND the
	// returned result (the single source of truth for the gateway_ref).
	if charger.calls != 1 || store.payments[res.PaymentID].GatewayRef != "rp_1" || res.GatewayRef != "rp_1" {
		t.Fatalf("capture must complete before success: calls=%d row.ref=%q result.ref=%q", charger.calls, store.payments[res.PaymentID].GatewayRef, res.GatewayRef)
	}
}

func TestTakeTender_Card_Offline_Blocked(t *testing.T) {
	store := newFakeStore()
	charger := &fakeCharger{result: ChargeResult{Status: ChargeCaptured}}
	svc := newTestSvc(store, charger)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)

	_, err := svc.TakeTender(ctx, nil, TenderInput{TillSessionID: sid, Method: MethodCard, AmountCents: 100,
		PaymentToken: "tok", Offline: true, IdempotencyKey: "k"})
	if !errors.Is(err, errOffline) {
		t.Fatalf("card offline must be blocked (errOffline); got %v", err)
	}
	if charger.calls != 0 {
		t.Fatal("blocked offline card must not call the rail")
	}
}

func TestTakeTender_NeverLogsToken(t *testing.T) {
	store := newFakeStore()
	charger := &fakeCharger{result: ChargeResult{ProcessorRef: "rp_ref", Status: ChargeCaptured}}
	svc := newTestSvc(store, charger)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)

	const token = "tok_secret_PAN"
	res, err := svc.TakeTender(ctx, nil, TenderInput{TillSessionID: sid, Method: MethodCard, AmountCents: 100,
		PaymentToken: token, CardType: CardCredit, IdempotencyKey: "k"})
	if err != nil {
		t.Fatalf("card: %v", err)
	}
	p := store.payments[res.PaymentID]
	// The token never lands on the row, only the opaque gateway_ref is stored (PCI SAQ-A). The
	// PaymentRow type has NO token field by construction, and the stored values never equal the token.
	if p.GatewayRef == token || p.SignatureRef == token || p.CardType == token {
		t.Fatalf("token leaked onto the payment row: %+v", p)
	}
}

func TestTakeTender_Split_TwoPaymentsOneSession(t *testing.T) {
	store := newFakeStore()
	charger := &fakeCharger{result: ChargeResult{ProcessorRef: "rp", Status: ChargeCaptured}}
	svc := newTestSvc(store, charger)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)

	cash, err := svc.TakeTender(ctx, nil, TenderInput{TillSessionID: sid, Method: MethodCash, AmountCents: 5000, IdempotencyKey: "k1"})
	if err != nil {
		t.Fatalf("cash: %v", err)
	}
	card, err := svc.TakeTender(ctx, nil, TenderInput{TillSessionID: sid, Method: MethodCard, AmountCents: 10000,
		PaymentToken: "tok", CardType: CardCredit, IdempotencyKey: "k2"})
	if err != nil {
		t.Fatalf("card: %v", err)
	}
	if cash.PaymentID == card.PaymentID || store.insertCalls != 2 {
		t.Fatalf("expected two distinct payment rows; insertCalls=%d", store.insertCalls)
	}
	if store.payments[cash.PaymentID].TillSessionID != sid || store.payments[card.PaymentID].TillSessionID != sid {
		t.Fatal("both tenders must attach to the same session")
	}
}

func TestAllocate_LinksPaymentToInvoice_CapSum(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	// Allocate now stamps allocated_by (Q-044) so it needs an authenticated caller like TakeTender.
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	pid := store.seedPayment(PaymentRow{Method: MethodCash, AmountCents: 15000, Status: PaymentReceived})

	if err := svc.Allocate(ctx, nil, pid, "inv-1", 15000); err != nil {
		t.Fatalf("allocate: %v", err)
	}
	// Over-cap: a further allocation would exceed amount_cents.
	err := svc.Allocate(ctx, nil, pid, "inv-2", 1)
	wantStatus(t, err, 422)
}

func TestTakeTender_InCallerTx_RollsBackWithSale(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)

	tx := &recordingTx{}
	if _, err := svc.TakeTender(ctx, tx, TenderInput{TillSessionID: sid, Method: MethodCash, AmountCents: 4500, IdempotencyKey: "k"}); err != nil {
		t.Fatalf("TakeTender in tx: %v", err)
	}
	// The till writes THROUGH the caller's tx but never commits or rolls it back itself, so a failing
	// later sale step can roll the whole tx back (no payment persists). Lifecycle stays with A-19.
	if tx.committed || tx.rolledBack {
		t.Fatalf("till must not manage the tx lifecycle: committed=%v rolledBack=%v", tx.committed, tx.rolledBack)
	}
}

func TestTakeTender_SignatureRef_Persisted(t *testing.T) {
	store := newFakeStore()
	charger := &fakeCharger{result: ChargeResult{ProcessorRef: "rp", Status: ChargeCaptured}}
	svc := newTestSvc(store, charger)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)

	res, err := svc.TakeTender(ctx, nil, TenderInput{TillSessionID: sid, Method: MethodCard, AmountCents: 100,
		PaymentToken: "tok", CardType: CardCredit, SignatureRef: "sig-ref-9", IdempotencyKey: "k"})
	if err != nil {
		t.Fatalf("card: %v", err)
	}
	if store.payments[res.PaymentID].SignatureRef != "sig-ref-9" {
		t.Fatalf("signature_ref not persisted: %+v", store.payments[res.PaymentID])
	}
}

func TestTakeTender_Card_PersistsCardType(t *testing.T) {
	for _, ct := range []string{CardCredit, CardDebit, CardPrepaid, CardUnknown} {
		store := newFakeStore()
		charger := &fakeCharger{result: ChargeResult{ProcessorRef: "rp", Status: ChargeCaptured}}
		svc := newTestSvc(store, charger)
		ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
		sid := openSession(t, store, trenton)

		res, err := svc.TakeTender(ctx, nil, TenderInput{TillSessionID: sid, Method: MethodCard, AmountCents: 100,
			PaymentToken: "tok", CardType: ct, IdempotencyKey: "k-" + ct})
		if err != nil {
			t.Fatalf("card %s: %v", ct, err)
		}
		p := store.payments[res.PaymentID]
		if p.CardType != ct {
			t.Fatalf("card_type not persisted: want %s got %s", ct, p.CardType)
		}
		if p.Method != MethodCard {
			t.Fatalf("method must stay single CARD (D-A03-4), got %s", p.Method)
		}
	}
}

func TestTakeTender_PendingStatus_ValidButUnused(t *testing.T) {
	store := newFakeStore()
	charger := &fakeCharger{result: ChargeResult{ProcessorRef: "rp", Status: ChargeCaptured}}
	svc := newTestSvc(store, charger)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)

	// Online card + cash both land RECEIVED; no W4 path ever writes the RESERVED 'PENDING' status.
	if _, err := svc.TakeTender(ctx, nil, TenderInput{TillSessionID: sid, Method: MethodCash, AmountCents: 100, IdempotencyKey: "k1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.TakeTender(ctx, nil, TenderInput{TillSessionID: sid, Method: MethodCard, AmountCents: 100, PaymentToken: "t", CardType: CardCredit, IdempotencyKey: "k2"}); err != nil {
		t.Fatal(err)
	}
	for id, p := range store.payments {
		if p.Status == PaymentPending {
			t.Fatalf("payment %s wrote the RESERVED PENDING status at the gate", id)
		}
	}
	if PaymentPending != "PENDING" {
		t.Fatal("PENDING must remain a defined, reserved status value")
	}
}

// - U17-U28, U30: lifecycle + over/short + the conditional gate -

func TestPaidOut_ReducesExpectedCash_StartFloatInMath(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton) // float 20000
	store.seedPayment(PaymentRow{Method: MethodCash, AmountCents: 60000, Status: PaymentReceived, TillSessionID: sid})

	if err := svc.PaidOut(ctx, MovementInput{TillSessionID: sid, MovementType: MovementPaidOut, Method: MethodCash, AmountCents: 50000, Reason: "midday drop"}); err != nil {
		t.Fatalf("paid out: %v", err)
	}
	z, err := svc.ZReportFor(ctx, sid)
	if err != nil {
		t.Fatalf("zreport: %v", err)
	}
	if z.Cash.ExpectedCents != 30000 { // 20000 + 60000 - 50000
		t.Fatalf("expected cash with start float in the math = 30000, got %d", z.Cash.ExpectedCents)
	}
}

func TestPaidIn_IncreasesExpectedCash(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton) // float 20000

	if err := svc.PaidIn(ctx, MovementInput{TillSessionID: sid, MovementType: MovementPaidIn, Method: MethodCash, AmountCents: 10000, Reason: "top-up"}); err != nil {
		t.Fatalf("paid in: %v", err)
	}
	z, _ := svc.ZReportFor(ctx, sid)
	if z.Cash.ExpectedCents != 30000 { // 20000 + 10000
		t.Fatalf("expected cash should rise to 30000, got %d", z.Cash.ExpectedCents)
	}
}

func TestLodgement_RecordedAgainstSession(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)

	if err := svc.Lodge(ctx, MovementInput{TillSessionID: sid, MovementType: MovementLodgement, Method: MethodCash, AmountCents: 30000, Reference: "bag-77"}); err != nil {
		t.Fatalf("lodge: %v", err)
	}
	mv := store.movements[sid]
	if len(mv) != 1 || mv[0].MovementType != MovementLodgement || mv[0].Reference != "bag-77" || mv[0].CreatedBy != "u-sarah" {
		t.Fatalf("unexpected lodgement movement: %+v", mv)
	}
}

func TestCountTill_Blind_NoExpectedRevealed(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)

	// CountTill returns ONLY an error (the handler writes {status:COUNTED} with no expected), there is
	// no expected figure in the count path's surface at all.
	if err := svc.CountTill(ctx, CountInput{TillSessionID: sid, Tenders: []TenderActual{{Method: MethodCash, ActualCents: 29950}}}); err != nil {
		t.Fatalf("count: %v", err)
	}
	if store.sessions[sid].Status != StatusCounted {
		t.Fatalf("status should be COUNTED, got %s", store.sessions[sid].Status)
	}
}

func TestCountTill_ClosingDenoms_MustEqualCountedCash(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)

	// Counted cash 29950 but denominations total 30000 → 422.
	err := svc.CountTill(ctx, CountInput{TillSessionID: sid,
		Tenders:              []TenderActual{{Method: MethodCash, ActualCents: 29950}},
		ClosingDenominations: []DenominationCount{{DenominationCents: 10000, Quantity: 3}}})
	wantStatus(t, err, 422)
}

func TestZReport_OverShort_StartFloatInMath(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton) // float 20000
	store.seedPayment(PaymentRow{Method: MethodCash, AmountCents: 60000, Status: PaymentReceived, TillSessionID: sid})
	_ = svc.PaidOut(ctx, MovementInput{TillSessionID: sid, MovementType: MovementPaidOut, Method: MethodCash, AmountCents: 50000})
	_ = svc.CountTill(ctx, CountInput{TillSessionID: sid, Tenders: []TenderActual{{Method: MethodCash, ActualCents: 29950}}})

	z, err := svc.ZReportFor(ctx, sid)
	if err != nil {
		t.Fatalf("zreport: %v", err)
	}
	if z.Cash.ExpectedCents != 30000 || z.Cash.CountedCents != 29950 || z.Cash.OverShortCents != -50 {
		t.Fatalf("over/short derived wrong: %+v", z.Cash)
	}
}

func TestZReport_PerTenderVariance_Derived(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)
	store.seedPayment(PaymentRow{Method: MethodCash, AmountCents: 6000, Status: PaymentReceived, TillSessionID: sid})
	store.seedPayment(PaymentRow{Method: MethodCheque, AmountCents: 8000, Status: PaymentReceived, TillSessionID: sid})
	store.seedPayment(PaymentRow{Method: MethodCard, AmountCents: 12000, Status: PaymentReceived, TillSessionID: sid})
	store.seedPayment(PaymentRow{Method: MethodEFT, AmountCents: 7000, Status: PaymentReceived, TillSessionID: sid})
	store.seedPayment(PaymentRow{Method: MethodOther, AmountCents: 2500, Status: PaymentReceived, TillSessionID: sid})
	// The blind count covers the whole drawer (start float + cash taken): the cashier hand-counts it
	// short by 50 (25950 against an expected 20000 + 6000); cheque exact; the card/EFT/OTHER
	// hand-count is IGNORED (D-A03-9).
	_ = svc.CountTill(ctx, CountInput{TillSessionID: sid, Tenders: []TenderActual{
		{Method: MethodCash, ActualCents: 25950}, {Method: MethodCheque, ActualCents: 8000}, {Method: MethodCard, ActualCents: 0},
	}})

	z, _ := svc.ZReportFor(ctx, sid)
	got := map[string]TenderTotal{}
	for _, tt := range z.PerTender {
		got[tt.Method] = tt
	}
	if got[MethodCash].VarianceCents != -50 {
		t.Fatalf("cash variance want -50, got %d", got[MethodCash].VarianceCents)
	}
	if got[MethodCash].ExpectedCents != 26000 {
		t.Fatalf("per-tender CASH expected carries the float like the summary (26000), got %d", got[MethodCash].ExpectedCents)
	}
	if got[MethodCheque].VarianceCents != 0 {
		t.Fatalf("cheque variance want 0, got %d", got[MethodCheque].VarianceCents)
	}
	// Card actual = processor captures = expected, so variance is 0 regardless of any hand-count.
	if got[MethodCard].ActualCents != 12000 || got[MethodCard].VarianceCents != 0 {
		t.Fatalf("card actual must equal captures (12000, variance 0), got %+v", got[MethodCard])
	}
	// EFT/OTHER are processor-confirmed like card: actual = expected, variance 0.
	if got[MethodEFT].ActualCents != 7000 || got[MethodEFT].VarianceCents != 0 {
		t.Fatalf("eft actual must equal captures (7000, variance 0), got %+v", got[MethodEFT])
	}
	if got[MethodOther].ActualCents != 2500 || got[MethodOther].VarianceCents != 0 {
		t.Fatalf("other actual must equal captures (2500, variance 0), got %+v", got[MethodOther])
	}
}

// The Z report must not answer two different numbers both named expected_cents for cash. A cash
// refund books a REFUNDED payments row (excluded by the RECEIVED payments sum) PLUS a cash PAID_OUT
// movement (the documented RefundTender shape), and the float sits in the drawer from open, so the
// per-tender CASH line carries the SAME expected the cash summary derives (float + movements in).
// A perfectly counted drawer then reads variance 0 on BOTH lines, equal to the summary over/short.
func TestZReport_PerTenderCashExpected_AgreesWithCashSummary(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton) // float 20000
	store.seedPayment(PaymentRow{Method: MethodCash, AmountCents: 60000, Status: PaymentReceived, TillSessionID: sid})
	// The movement every cash refund books (RefundTender's documented shape).
	if err := svc.PaidOut(ctx, MovementInput{TillSessionID: sid, MovementType: MovementPaidOut, Method: MethodCash, AmountCents: 5000, Reason: "RETURN ret-1"}); err != nil {
		t.Fatalf("paid out: %v", err)
	}

	perTenderCash := func(z ZReport) TenderTotal {
		for _, tt := range z.PerTender {
			if tt.Method == MethodCash {
				return tt
			}
		}
		t.Fatalf("no per-tender CASH line in %+v", z.PerTender)
		return TenderTotal{}
	}

	z, err := svc.ZReportFor(ctx, sid)
	if err != nil {
		t.Fatalf("zreport: %v", err)
	}
	if z.Cash.ExpectedCents != 75000 { // 20000 + 60000 - 5000
		t.Fatalf("summary expected cash = 75000, got %d", z.Cash.ExpectedCents)
	}
	if cash := perTenderCash(z); cash.ExpectedCents != z.Cash.ExpectedCents {
		t.Fatalf("per-tender CASH expected %d disagrees with the summary's %d: one answer, one expected cash", cash.ExpectedCents, z.Cash.ExpectedCents)
	}

	// A drawer counted exactly right: variance 0 on BOTH cash lines.
	if err := svc.CountTill(ctx, CountInput{TillSessionID: sid, Tenders: []TenderActual{{Method: MethodCash, ActualCents: 75000}}}); err != nil {
		t.Fatalf("count: %v", err)
	}
	z, err = svc.ZReportFor(ctx, sid)
	if err != nil {
		t.Fatalf("zreport after count: %v", err)
	}
	if z.Cash.OverShortCents != 0 {
		t.Fatalf("summary over/short = 0, got %d", z.Cash.OverShortCents)
	}
	if cash := perTenderCash(z); cash.VarianceCents != 0 || cash.ActualCents != z.Cash.CountedCents {
		t.Fatalf("a perfectly counted drawer shows per-tender CASH variance %d (actual %d), want variance 0 on the counted %d", cash.VarianceCents, cash.ActualCents, z.Cash.CountedCents)
	}
}

// Cheques CAN carry drawer movements: the 0028 movement table's own vocabulary is movement_type
// PAID_IN/PAID_OUT/DROP/LODGEMENT crossed with method CASH/CHEQUE/CARD/EFT/OTHER, the contract
// accepts a CHEQUE movement, and the movement writer passes the method through. So the CHEQUE
// line's expected is the drawer's expected cheques: RECEIVED cheque payments + cheque paid in -
// cheque paid out and dropped - cheque lodgements, with no float (the float is cash in the drawer
// by definition).

// The round 2 review's probe: a cheque lodgement books against ITS OWN method, so a drawer whose
// cheque went to the bank counts CHEQUE variance 0, never -lodgement against a payments-only sum.
func TestZReport_ChequeLodgement_InChequeExpected(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton) // float 20000, cash only
	store.seedPayment(PaymentRow{Method: MethodCheque, AmountCents: 8000, Status: PaymentReceived, TillSessionID: sid})
	if err := svc.Lodge(ctx, MovementInput{TillSessionID: sid, MovementType: MovementLodgement, Method: MethodCheque, AmountCents: 8000, Reference: "bag-9"}); err != nil {
		t.Fatalf("cheque lodge: %v", err)
	}
	// The drawer counted exactly right: the cash float is there, the cheque is at the bank.
	if err := svc.CountTill(ctx, CountInput{TillSessionID: sid, Tenders: []TenderActual{
		{Method: MethodCash, ActualCents: 20000}, {Method: MethodCheque, ActualCents: 0},
	}}); err != nil {
		t.Fatalf("count: %v", err)
	}
	z, err := svc.ZReportFor(ctx, sid)
	if err != nil {
		t.Fatalf("zreport: %v", err)
	}
	got := map[string]TenderTotal{}
	for _, tt := range z.PerTender {
		got[tt.Method] = tt
	}
	if got[MethodCheque].ExpectedCents != 0 { // 8000 received - 8000 lodged
		t.Fatalf("cheque expected with the lodgement out = 0, got %d", got[MethodCheque].ExpectedCents)
	}
	if got[MethodCheque].VarianceCents != 0 {
		t.Fatalf("a correctly counted drawer shows cheque variance %d, want 0", got[MethodCheque].VarianceCents)
	}
	// The movement books against its own method: the cash summary never sees it.
	if z.Cash.ExpectedCents != 20000 || z.Cash.OverShortCents != 0 {
		t.Fatalf("cash summary must stay free of cheque movements: %+v", z.Cash)
	}
}

// A cheque paid out leaves the drawer like a cash one: the CHEQUE expected drops by it.
func TestZReport_ChequePaidOut_ReducesChequeExpected(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)
	store.seedPayment(PaymentRow{Method: MethodCheque, AmountCents: 8000, Status: PaymentReceived, TillSessionID: sid})
	if err := svc.PaidOut(ctx, MovementInput{TillSessionID: sid, MovementType: MovementPaidOut, Method: MethodCheque, AmountCents: 3000, Reason: "supplier paid by cheque"}); err != nil {
		t.Fatalf("cheque paid out: %v", err)
	}
	if err := svc.CountTill(ctx, CountInput{TillSessionID: sid, Tenders: []TenderActual{
		{Method: MethodCash, ActualCents: 20000}, {Method: MethodCheque, ActualCents: 5000},
	}}); err != nil {
		t.Fatalf("count: %v", err)
	}
	z, err := svc.ZReportFor(ctx, sid)
	if err != nil {
		t.Fatalf("zreport: %v", err)
	}
	for _, tt := range z.PerTender {
		if tt.Method != MethodCheque {
			continue
		}
		if tt.ExpectedCents != 5000 { // 8000 received - 3000 paid out
			t.Fatalf("cheque expected after the paid out = 5000, got %d", tt.ExpectedCents)
		}
		if tt.VarianceCents != 0 {
			t.Fatalf("a correctly counted drawer shows cheque variance %d, want 0", tt.VarianceCents)
		}
	}
}

// The CASH line now means the drawer, so it is present whenever the summary states an expected,
// even with no RECEIVED cash payment and no count yet (the round 2 review's probe: float plus a
// paid in answered per_tender null beside cash.expected 21000).
func TestZReport_CashLinePresent_FloatPlusPaidIn(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton) // float 20000
	if err := svc.PaidIn(ctx, MovementInput{TillSessionID: sid, MovementType: MovementPaidIn, Method: MethodCash, AmountCents: 1000, Reason: "change top-up"}); err != nil {
		t.Fatalf("paid in: %v", err)
	}
	z, err := svc.ZReportFor(ctx, sid)
	if err != nil {
		t.Fatalf("zreport: %v", err)
	}
	if z.Cash.ExpectedCents != 21000 {
		t.Fatalf("summary expected = 21000, got %d", z.Cash.ExpectedCents)
	}
	var cash *TenderTotal
	for i := range z.PerTender {
		if z.PerTender[i].Method == MethodCash {
			cash = &z.PerTender[i]
		}
	}
	if cash == nil {
		t.Fatalf("no per-tender CASH line beside a summary expected of %d", z.Cash.ExpectedCents)
	}
	if cash.ExpectedCents != z.Cash.ExpectedCents || cash.VarianceCents != -21000 {
		t.Fatalf("per-tender CASH line wrong: %+v (summary expected %d)", *cash, z.Cash.ExpectedCents)
	}
}

// One truth per counted method across BOTH reads: the session detail's tenders row states the same
// derived expected the Z report states, not the stored payments-sum snapshot (the round 2 review's
// probe: detail read cash expected 60000, actual 75000, variance +15000 while the Z report gave
// 75000 and 0 on the same counted drawer).
func TestSessionDetail_CountedMethods_AgreeWithZReport(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton) // float 20000
	store.seedPayment(PaymentRow{Method: MethodCash, AmountCents: 60000, Status: PaymentReceived, TillSessionID: sid})
	store.seedPayment(PaymentRow{Method: MethodCheque, AmountCents: 8000, Status: PaymentReceived, TillSessionID: sid})
	if err := svc.PaidOut(ctx, MovementInput{TillSessionID: sid, MovementType: MovementPaidOut, Method: MethodCash, AmountCents: 5000, Reason: "drop"}); err != nil {
		t.Fatalf("paid out: %v", err)
	}
	if err := svc.Lodge(ctx, MovementInput{TillSessionID: sid, MovementType: MovementLodgement, Method: MethodCheque, AmountCents: 8000, Reference: "bag-5"}); err != nil {
		t.Fatalf("cheque lodge: %v", err)
	}
	if err := svc.CountTill(ctx, CountInput{TillSessionID: sid, Tenders: []TenderActual{
		{Method: MethodCash, ActualCents: 75000}, {Method: MethodCheque, ActualCents: 0},
	}}); err != nil {
		t.Fatalf("count: %v", err)
	}

	z, err := svc.ZReportFor(ctx, sid)
	if err != nil {
		t.Fatalf("zreport: %v", err)
	}
	zr := map[string]TenderTotal{}
	for _, tt := range z.PerTender {
		zr[tt.Method] = tt
	}
	detail, err := svc.GetSession(ctx, sid)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	dt := map[string]TenderTotal{}
	for _, tt := range detail.Tenders {
		dt[tt.Method] = tt
	}
	if dt[MethodCash].ExpectedCents != zr[MethodCash].ExpectedCents {
		t.Fatalf("detail read cash expected %d disagrees with the Z report's %d", dt[MethodCash].ExpectedCents, zr[MethodCash].ExpectedCents)
	}
	if dt[MethodCash].VarianceCents != 0 {
		t.Fatalf("detail read cash variance %d, want 0 on a correctly counted drawer", dt[MethodCash].VarianceCents)
	}
	if dt[MethodCheque].ExpectedCents != zr[MethodCheque].ExpectedCents {
		t.Fatalf("detail read cheque expected %d disagrees with the Z report's %d", dt[MethodCheque].ExpectedCents, zr[MethodCheque].ExpectedCents)
	}
	if dt[MethodCheque].VarianceCents != 0 {
		t.Fatalf("detail read cheque variance %d, want 0 on a correctly counted drawer", dt[MethodCheque].VarianceCents)
	}
}

func TestCompleteTill_CleanClose_NoManager(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton) // float 20000
	store.seedPayment(PaymentRow{Method: MethodCash, AmountCents: 5000, Status: PaymentReceived, TillSessionID: sid})
	// Exact count: counted = 20000 + 5000 = 25000 → variance 0 → within any threshold.
	_ = svc.CountTill(ctx, CountInput{TillSessionID: sid, Tenders: []TenderActual{{Method: MethodCash, ActualCents: 25000}}})

	if err := svc.CompleteTill(ctx, CompleteInput{TillSessionID: sid}); err != nil {
		t.Fatalf("clean close by a non-manager should succeed: %v", err)
	}
	if store.sessions[sid].Status != StatusClosed {
		t.Fatalf("status should be CLOSED, got %s", store.sessions[sid].Status)
	}
}

func TestCompleteTill_OverThreshold_NonManager_Blocked(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil) // default policy threshold 0 → any nonzero variance is over
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)
	store.seedPayment(PaymentRow{Method: MethodCash, AmountCents: 5000, Status: PaymentReceived, TillSessionID: sid})
	// Counted 24950 vs expected 25000 → -50 variance, over the 0 threshold.
	_ = svc.CountTill(ctx, CountInput{TillSessionID: sid, Tenders: []TenderActual{{Method: MethodCash, ActualCents: 24950}}})

	err := svc.CompleteTill(ctx, CompleteInput{TillSessionID: sid})
	wantStatus(t, err, 403)
	if store.sessions[sid].Status != StatusCounted {
		t.Fatalf("a blocked close must stay COUNTED, got %s", store.sessions[sid].Status)
	}
}

func TestCompleteTill_OverThreshold_Manager_RequiresOverrideNote(t *testing.T) {
	store := newFakeStore()
	rec := &fakeRecorder{}
	svc := newTestSvc(store, nil).WithAudit(rec)
	mctx := ctxWith(context.Background(), managerAt("u-ryan", trenton))
	sid := openSession(t, store, trenton)
	store.seedPayment(PaymentRow{Method: MethodCash, AmountCents: 5000, Status: PaymentReceived, TillSessionID: sid})
	_ = svc.CountTill(mctx, CountInput{TillSessionID: sid, Tenders: []TenderActual{{Method: MethodCash, ActualCents: 24950}}})

	// Manager, over threshold, but NO override note → 422.
	err := svc.CompleteTill(mctx, CompleteInput{TillSessionID: sid})
	wantStatus(t, err, 422)

	// With a note → CLOSED + audited.
	if err := svc.CompleteTill(mctx, CompleteInput{TillSessionID: sid, OverrideNotes: "recount confirmed"}); err != nil {
		t.Fatalf("manager close with note: %v", err)
	}
	if store.sessions[sid].Status != StatusClosed || store.sessions[sid].OverrideNotes != "recount confirmed" {
		t.Fatalf("override note not persisted: %+v", store.sessions[sid])
	}
	if rec.calls != 1 || rec.last.Action != "TILL_OVERRIDE" {
		t.Fatalf("over-threshold sign-off must be audited: %+v", rec)
	}
}

func TestTillPolicy_ThresholdIsConfigurableRow(t *testing.T) {
	store := newFakeStore()
	store.policies[trenton] = TillPolicy{BranchID: trenton, VarianceThresholdCents: 100, CloseGrain: "SESSION"}
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))

	// variance -50, within the configured 100 threshold → a non-manager may close.
	sid := openSession(t, store, trenton)
	store.seedPayment(PaymentRow{Method: MethodCash, AmountCents: 5000, Status: PaymentReceived, TillSessionID: sid})
	_ = svc.CountTill(ctx, CountInput{TillSessionID: sid, Tenders: []TenderActual{{Method: MethodCash, ActualCents: 24950}}})
	if err := svc.CompleteTill(ctx, CompleteInput{TillSessionID: sid}); err != nil {
		t.Fatalf("within configured threshold a non-manager should close: %v", err)
	}

	// variance -150, over the same configured threshold → blocked for a non-manager.
	store2 := newFakeStore()
	store2.policies[trenton] = TillPolicy{BranchID: trenton, VarianceThresholdCents: 100}
	svc2 := newTestSvc(store2, nil)
	sid2 := openSession(t, store2, trenton)
	store2.seedPayment(PaymentRow{Method: MethodCash, AmountCents: 5000, Status: PaymentReceived, TillSessionID: sid2})
	_ = svc2.CountTill(ctx, CountInput{TillSessionID: sid2, Tenders: []TenderActual{{Method: MethodCash, ActualCents: 24850}}})
	wantStatus(t, svc2.CompleteTill(ctx, CompleteInput{TillSessionID: sid2}), 403)
}

func TestClosingFloat_CarriesForward(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)

	if err := svc.CountTill(ctx, CountInput{TillSessionID: sid, Tenders: []TenderActual{{Method: MethodCash, ActualCents: 20000}}, ClosingFloatCents: 15000}); err != nil {
		t.Fatalf("count: %v", err)
	}
	z, _ := svc.ZReportFor(ctx, sid)
	if z.ClosingFloatCents != 15000 {
		t.Fatalf("closing float should carry forward as 15000, got %d", z.ClosingFloatCents)
	}
}

func TestCompleteTill_LifecycleGuard(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))

	// Completing an OPEN (un-counted) session → 409.
	openS := openSession(t, store, trenton)
	wantStatus(t, svc.CompleteTill(ctx, CompleteInput{TillSessionID: openS}), 409)

	// Re-completing a CLOSED session → 409.
	closedS := store.seedSession(TillSession{BranchID: trenton, Station: "ALT", Status: StatusClosed, OpenedBy: "u-x"})
	wantStatus(t, svc.CompleteTill(ctx, CompleteInput{TillSessionID: closedS}), 409)
}

// - fake audit Recorder (implements audit.Recorder) -

type fakeRecorder struct {
	calls int
	last  audit.SensitiveAccess
	all   []audit.SensitiveAccess // every record, in order (Q-044 assertions)
}

func (r *fakeRecorder) Record(_ context.Context, a audit.SensitiveAccess) error {
	r.calls++
	r.last = a
	r.all = append(r.all, a)
	return nil
}

// byAction returns the recorder's rows for one action verb.
func (r *fakeRecorder) byAction(action string) []audit.SensitiveAccess {
	var out []audit.SensitiveAccess
	for _, a := range r.all {
		if a.Action == action {
			out = append(out, a)
		}
	}
	return out
}

// - W7: RefundTender (the POS-return refund rail, sole payments/movements writer) -

func TestRefundTender_Cash_WritesRefundedRowAndPaidOutMovement(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)

	res, err := svc.RefundTender(ctx, nil, RefundInput{
		TillSessionID: sid, Method: MethodCash, AmountCents: 2500, BranchID: trenton,
		Reference: "#RET-9", IdempotencyKey: "ret-k1",
	})
	if err != nil {
		t.Fatalf("cash refund: %v", err)
	}
	p := store.payments[res.PaymentID]
	if p == nil || p.Status != PaymentRefunded || p.Method != MethodCash || p.AmountCents != 2500 {
		t.Fatalf("REFUNDED cash row wrong: %+v", p)
	}
	if p.TillSessionID != sid || p.BranchID != trenton {
		t.Fatalf("refund row not attached to the drawer: %+v", p)
	}
	if len(store.txMovements) != 1 {
		t.Fatalf("want 1 in-tx PAID_OUT movement, got %d", len(store.txMovements))
	}
	mv := store.txMovements[0]
	if mv.MovementType != MovementPaidOut || mv.Method != MethodCash || mv.AmountCents != 2500 ||
		mv.Reason != "RETURN #RET-9" || mv.Reference != "#RET-9" {
		t.Fatalf("PAID_OUT movement wrong: %+v", mv)
	}
	// The REFUNDED row must NOT raise expected tenders (RECEIVED-filtered).
	exp, _ := store.ExpectedTenders(context.Background(), sid)
	if exp[MethodCash] != 0 {
		t.Fatalf("REFUNDED row leaked into expected tenders: %+v", exp)
	}
}

func TestRefundTender_Cash_RequiresOpenSession(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := store.seedSession(TillSession{BranchID: trenton, Station: "MAIN", Status: StatusClosed})

	_, err := svc.RefundTender(ctx, nil, RefundInput{TillSessionID: sid, Method: MethodCash, AmountCents: 100})
	wantStatus(t, err, 409)
	if len(store.txMovements) != 0 {
		t.Fatal("no movement may be written for a closed drawer")
	}
}

func TestRefundTender_Cash_BranchMismatch_Conflicts(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)

	_, err := svc.RefundTender(ctx, nil, RefundInput{TillSessionID: sid, Method: MethodCash, AmountCents: 100, BranchID: kingston})
	wantStatus(t, err, 409)
}

func TestRefundTender_Card_WritesRefundedRowNoDrawerEffect(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))

	res, err := svc.RefundTender(ctx, nil, RefundInput{
		Method: MethodCard, AmountCents: 9900, BranchID: trenton, GatewayRef: "rp_ref_abc",
		Reference: "#RET-10", IdempotencyKey: "ret-k2",
	})
	if err != nil {
		t.Fatalf("card refund: %v", err)
	}
	p := store.payments[res.PaymentID]
	if p == nil || p.Status != PaymentRefunded || p.Method != MethodCard || p.GatewayRef != "rp_ref_abc" {
		t.Fatalf("REFUNDED card row wrong: %+v", p)
	}
	if len(store.txMovements) != 0 {
		t.Fatal("a card refund must not touch the drawer")
	}
}

func TestRefundTender_IdempotentReplay_NoSecondRowOrMovement(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)

	in := RefundInput{TillSessionID: sid, Method: MethodCash, AmountCents: 500, Reference: "#RET-11", IdempotencyKey: "ret-k3"}
	first, err := svc.RefundTender(ctx, nil, in)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	replay, err := svc.RefundTender(ctx, nil, in)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.PaymentID != first.PaymentID || store.insertCalls != 1 || len(store.txMovements) != 1 {
		t.Fatalf("replay must reuse the row and write no second movement: ids %q/%q inserts=%d movements=%d",
			first.PaymentID, replay.PaymentID, store.insertCalls, len(store.txMovements))
	}
}

func TestRefundTender_RejectsBadMethodAndAmount(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))

	if _, err := svc.RefundTender(ctx, nil, RefundInput{Method: "CHEQUE", AmountCents: 100, BranchID: trenton}); err == nil {
		t.Fatal("cheque refunds are not a till rail")
	}
	if _, err := svc.RefundTender(ctx, nil, RefundInput{Method: MethodCash, AmountCents: 0, TillSessionID: "x"}); err == nil {
		t.Fatal("zero amount must be rejected")
	}
}

func TestAllocate_StampsAllocatedBy(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	pid := store.seedPayment(PaymentRow{Method: MethodCash, AmountCents: 1000, Status: PaymentReceived})

	if err := svc.Allocate(ctx, nil, pid, "inv-1", 1000); err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if store.lastAllocatedBy != "u-sarah" {
		t.Fatalf("allocated_by not stamped from the caller: %q", store.lastAllocatedBy)
	}
}

// - Q-044: explicit business-semantic payment audit records -

func TestTakeTender_RecordsPaymentReceivedAudit(t *testing.T) {
	store := newFakeStore()
	rec := &fakeRecorder{}
	svc := newTestSvc(store, nil).WithAudit(rec)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)

	res, err := svc.TakeTender(ctx, nil, TenderInput{TillSessionID: sid, Method: MethodCash, AmountCents: 4500, IdempotencyKey: "K"})
	if err != nil {
		t.Fatalf("TakeTender: %v", err)
	}
	// M4: NOTHING is recorded mid-tx, the record rides the explicit post-commit hook only, so a
	// caller rollback can never orphan a receipt row.
	if got := len(rec.byAction(audit.ActPaymentReceived)); got != 0 {
		t.Fatalf("TakeTender must not record PAYMENT_RECEIVED mid-tx; rows=%d", got)
	}
	if !res.Inserted || res.PaymentNo == "" || res.Method != MethodCash || res.AmountCents != 4500 || res.BranchID != trenton {
		t.Fatalf("TenderResult must carry the post-commit audit facts: %+v", res)
	}

	// The caller commits, then invokes the hook, exactly one attributed record.
	svc.RecordPaymentReceived(ctx, res)
	rows := rec.byAction(audit.ActPaymentReceived)
	if len(rows) != 1 {
		t.Fatalf("want 1 PAYMENT_RECEIVED audit row, got %d", len(rows))
	}
	a := rows[0]
	if a.EntityType != "payment" || a.EntityID != res.PaymentID || a.ActorID != "u-sarah" || a.BranchID != trenton {
		t.Fatalf("wrong attribution on PAYMENT_RECEIVED: %+v", a)
	}
	if a.Extra["method"] != MethodCash || a.Extra["amount_cents"] != int64(4500) {
		t.Fatalf("PAYMENT_RECEIVED must carry method + amount_cents: %+v", a.Extra)
	}
	if no, _ := a.Extra["payment_no"].(string); no == "" {
		t.Fatalf("PAYMENT_RECEIVED must carry the payment_no: %+v", a.Extra)
	}

	// An idempotent replay returns the same payment with Inserted=false, the hook must no-op, so
	// replay + hook can never write a second receipt record.
	replay, err := svc.TakeTender(ctx, nil, TenderInput{TillSessionID: sid, Method: MethodCash, AmountCents: 4500, IdempotencyKey: "K"})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.Inserted {
		t.Fatalf("a replayed tender must carry Inserted=false: %+v", replay)
	}
	svc.RecordPaymentReceived(ctx, replay)
	if got := len(rec.byAction(audit.ActPaymentReceived)); got != 1 {
		t.Fatalf("replay must not re-record PAYMENT_RECEIVED; rows=%d", got)
	}
}

// M4, rollback-then-retry: a tender whose enclosing tx rolls back leaves NO receipt record (the hook
// is never reached), and the retry's fresh insert + post-commit hook records exactly once, no orphan,
// no duplicate.
func TestTakeTender_RollbackRetry_NoOrphanNoDuplicateReceipt(t *testing.T) {
	store := newFakeStore()
	rec := &fakeRecorder{}
	svc := newTestSvc(store, nil).WithAudit(rec)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	sid := openSession(t, store, trenton)

	in := TenderInput{TillSessionID: sid, Method: MethodCash, AmountCents: 4500, IdempotencyKey: "K-retry"}
	first, err := svc.TakeTender(ctx, nil, in)
	if err != nil {
		t.Fatalf("first TakeTender: %v", err)
	}
	// The caller's tx ROLLS BACK: the hook is never invoked, and the DB would discard the payments
	// row, emulate that on the fake store so the retry is a true fresh insert.
	if got := len(rec.byAction(audit.ActPaymentReceived)); got != 0 {
		t.Fatalf("a rolled-back tender must leave no receipt record; rows=%d", got)
	}
	delete(store.payments, first.PaymentID)
	delete(store.paymentByKey, in.IdempotencyKey)

	// Retry: a fresh insert (Inserted=true), commit succeeds this time, the hook records ONCE.
	retry, err := svc.TakeTender(ctx, nil, in)
	if err != nil {
		t.Fatalf("retry TakeTender: %v", err)
	}
	if !retry.Inserted {
		t.Fatalf("the retry must be a fresh insert: %+v", retry)
	}
	svc.RecordPaymentReceived(ctx, retry)
	rows := rec.byAction(audit.ActPaymentReceived)
	if len(rows) != 1 {
		t.Fatalf("rollback-retry must record exactly once, got %d", len(rows))
	}
	if rows[0].EntityID != retry.PaymentID {
		t.Fatalf("receipt record must reference the committed payment: %+v", rows[0])
	}
}

// M5, a BOUNCED payment's history reconstructs its NSF-reversed applications from the audit trail:
// the header is flagged reversed and each historical allocation carries reversed_by_nsf + the audit
// facts (invoice_id, amount, actor). Non-allocation trail rows (PAYMENT_RECEIVED) are ignored.
func TestPaymentHistory_Bounced_ReconstructsReversedAllocations(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil).WithAuditTrail(fakeTrail{entries: []audit.Entry{
		{Action: audit.ActPaymentReceived, EntityType: "payment", ActorID: "u-sarah",
			Changes: map[string]any{"method": "CHEQUE", "amount_cents": int64(7700)}},
		{Action: audit.ActPaymentAllocated, EntityType: "payment", ActorID: "u-sarah",
			// float64 mirrors the jsonb round-trip of the real repo read.
			Changes:   map[string]any{"invoice_id": "inv-9", "amount_cents": float64(7700)},
			CreatedAt: "2026-07-13T12:00:00.000000Z"},
	}})
	pid := store.seedPayment(PaymentRow{Method: MethodCheque, AmountCents: 7700, Status: "BOUNCED", BranchID: trenton})

	h, err := svc.PaymentHistory(ctxWith(context.Background(), staffAt("u-sarah", trenton)), pid)
	if err != nil {
		t.Fatalf("PaymentHistory: %v", err)
	}
	if !h.Reversed {
		t.Fatal("a BOUNCED payment must be marked reversed")
	}
	if len(h.Allocations) != 1 {
		t.Fatalf("want 1 reconstructed allocation, got %d: %+v", len(h.Allocations), h.Allocations)
	}
	al := h.Allocations[0]
	if !al.ReversedByNSF || al.InvoiceID != "inv-9" || al.AmountCents != 7700 || al.AllocatedBy != "u-sarah" {
		t.Fatalf("reconstructed allocation wrong: %+v", al)
	}
	if al.AllocatedAt.IsZero() {
		t.Fatalf("reconstructed allocation must carry the audit timestamp: %+v", al)
	}

	// A RECEIVED payment stays untouched: no reversed flag, no trail read mixing in.
	pid2 := store.seedPayment(PaymentRow{Method: MethodCash, AmountCents: 100, Status: PaymentReceived, BranchID: trenton})
	h2, err := svc.PaymentHistory(ctxWith(context.Background(), staffAt("u-sarah", trenton)), pid2)
	if err != nil {
		t.Fatalf("PaymentHistory (received): %v", err)
	}
	if h2.Reversed || len(h2.Allocations) != 0 {
		t.Fatalf("a live payment must not carry reversed history: %+v", h2)
	}
}

// fakeTrail satisfies AuditTrailReader for the M5 unit test.
type fakeTrail struct{ entries []audit.Entry }

func (f fakeTrail) ReopenTransaction(context.Context, string, string) ([]audit.Entry, error) {
	return f.entries, nil
}

func TestAllocate_RecordsPaymentAllocatedAudit(t *testing.T) {
	store := newFakeStore()
	rec := &fakeRecorder{}
	svc := newTestSvc(store, nil).WithAudit(rec)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	pid := store.seedPayment(PaymentRow{Method: MethodCash, AmountCents: 1000, Status: PaymentReceived})

	if err := svc.Allocate(ctx, nil, pid, "inv-1", 1000); err != nil {
		t.Fatalf("allocate: %v", err)
	}
	rows := rec.byAction(audit.ActPaymentAllocated)
	if len(rows) != 1 {
		t.Fatalf("want 1 PAYMENT_ALLOCATED audit row, got %d", len(rows))
	}
	a := rows[0]
	if a.EntityType != "payment" || a.EntityID != pid || a.ActorID != "u-sarah" {
		t.Fatalf("wrong attribution on PAYMENT_ALLOCATED: %+v", a)
	}
	if a.Extra["invoice_id"] != "inv-1" || a.Extra["amount_cents"] != int64(1000) {
		t.Fatalf("PAYMENT_ALLOCATED must carry invoice_id + amount_cents: %+v", a.Extra)
	}
}

func TestAllocate_RejectedOverage_WritesNoAuditRow(t *testing.T) {
	store := newFakeStore()
	rec := &fakeRecorder{}
	svc := newTestSvc(store, nil).WithAudit(rec)
	ctx := ctxWith(context.Background(), staffAt("u-sarah", trenton))
	pid := store.seedPayment(PaymentRow{Method: MethodCash, AmountCents: 1000, Status: PaymentReceived})

	wantStatus(t, svc.Allocate(ctx, nil, pid, "inv-1", 1500), 422)
	if got := len(rec.byAction(audit.ActPaymentAllocated)); got != 0 {
		t.Fatalf("a rejected allocation must not be audited as applied; rows=%d", got)
	}
}

// - Q-044: history read guards (branch scope + not-found) -

func TestPaymentHistory_BranchScope(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	pid := store.seedPayment(PaymentRow{Method: MethodCash, AmountCents: 1000, Status: PaymentReceived, BranchID: kingston})

	// A Kingston payment is invisible to Trenton-scoped staff (403), visible to Kingston staff.
	_, err := svc.PaymentHistory(ctxWith(context.Background(), staffAt("u-sarah", trenton)), pid)
	wantStatus(t, err, 403)

	h, err := svc.PaymentHistory(ctxWith(context.Background(), staffAt("u-kim", kingston)), pid)
	if err != nil {
		t.Fatalf("PaymentHistory: %v", err)
	}
	if h.PaymentID != pid || h.AmountCents != 1000 {
		t.Fatalf("unexpected history: %+v", h)
	}

	_, err = svc.PaymentHistory(ctxWith(context.Background(), staffAt("u-kim", kingston)), "missing")
	wantStatus(t, err, 404)
}
