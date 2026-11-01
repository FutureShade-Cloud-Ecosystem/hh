package payments

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// fakeStore is an in-memory paymentStore with real tenant scoping, so the service tests
// exercise the vault/charge/allocation flow without pgx (mirrors internal/product +
// internal/portal test style). Invoices are seeded for the pay flow.
type fakeStore struct {
	methods  map[string]PaymentMethod // by method id
	invoices map[string]fakeInvoice   // by invoice id
	payments []Payment
	methodNo int
	payCalls int // how many times Pay actually wrote (idempotency assertion)
	// DIB-415 intent store, indexed both ways (by key for the claim, by id for the stamps).
	intents     map[string]*Intent
	intentsByID map[string]*Intent
}

type fakeInvoice struct {
	customerID    string
	balanceCents  int64
	holdbackCents int64
	status        string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		methods:  map[string]PaymentMethod{},
		invoices: map[string]fakeInvoice{},
	}
}

func (f *fakeStore) seedInvoice(id, customerID string, balance, holdback int64) {
	f.seedInvoiceStatus(id, customerID, balance, holdback, "FINALIZED")
}

func (f *fakeStore) seedInvoiceStatus(id, customerID string, balance, holdback int64, status string) {
	f.invoices[id] = fakeInvoice{customerID: customerID, balanceCents: balance, holdbackCents: holdback, status: status}
}

func (f *fakeStore) ListMethods(_ context.Context, customerID string) ([]PaymentMethod, error) {
	var out []PaymentMethod
	for _, m := range f.methods {
		if m.CustomerID == customerID && m.Status == "active" {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeStore) GetMethod(_ context.Context, customerID, methodID string) (PaymentMethod, error) {
	m, ok := f.methods[methodID]
	// Mirror the real query (WHERE id+customer_id AND status='active'): a missing,
	// cross-tenant, OR soft-deleted method is not-found, never charged.
	if !ok || m.CustomerID != customerID || m.Status != "active" {
		return PaymentMethod{}, ErrNotFound
	}
	return m, nil
}

func (f *fakeStore) AddMethod(_ context.Context, m PaymentMethod, makeDefault bool) (PaymentMethod, error) {
	if makeDefault {
		for id, existing := range f.methods {
			if existing.CustomerID == m.CustomerID {
				existing.IsDefault = false
				f.methods[id] = existing
			}
		}
	}
	f.methodNo++
	m.ID = fmt.Sprintf("pm-%d", f.methodNo)
	m.IsDefault = makeDefault
	f.methods[m.ID] = m
	return m, nil
}

func (f *fakeStore) RemoveMethod(_ context.Context, customerID, methodID string) error {
	m, ok := f.methods[methodID]
	if !ok || m.CustomerID != customerID {
		return ErrNotFound
	}
	m.Status = "removed"
	m.IsDefault = false
	f.methods[methodID] = m
	return nil
}

func (f *fakeStore) SetDefaultMethod(_ context.Context, customerID, methodID string) error {
	m, ok := f.methods[methodID]
	if !ok || m.CustomerID != customerID || m.Status != "active" {
		return ErrNotFound // missing, removed, or cross-tenant
	}
	for id, existing := range f.methods {
		if existing.CustomerID == customerID && existing.IsDefault {
			existing.IsDefault = false
			f.methods[id] = existing
		}
	}
	m.IsDefault = true
	f.methods[methodID] = m
	return nil
}

func (f *fakeStore) ListPayments(_ context.Context, customerID string) ([]Payment, error) {
	var out []Payment
	for _, p := range f.payments {
		if p.CustomerID == customerID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeStore) FindByGatewayRef(_ context.Context, customerID, ref string) (Payment, bool, error) {
	if ref == "" {
		return Payment{}, false, nil
	}
	for _, p := range f.payments {
		if p.CustomerID == customerID && p.GatewayRef == ref {
			return p, true, nil
		}
	}
	return Payment{}, false, nil
}

func (f *fakeStore) PayableInvoices(_ context.Context, customerID string, invoiceIDs []string) ([]Allocation, error) {
	var out []Allocation
	for _, id := range invoiceIDs {
		inv, ok := f.invoices[id]
		if !ok || inv.customerID != customerID {
			return nil, ErrNotFound // missing or cross-tenant
		}
		payable := inv.balanceCents - inv.holdbackCents
		if payable < 0 {
			payable = 0
		}
		out = append(out, Allocation{InvoiceID: id, AmountCents: payable, Status: inv.status})
	}
	return out, nil
}

func (f *fakeStore) Pay(_ context.Context, customerID, method, gatewayRef string, allocs []Allocation) (Payment, error) {
	// Mirror the real store's per-allocation re-check: a non-positive allocation aborts the
	// whole write (so a zero-payable invoice slipping past pre-validation is still caught).
	for _, a := range allocs {
		if a.AmountCents <= 0 {
			return Payment{}, httpx.BadRequestf("invoice %q balance changed; refresh and retry", a.InvoiceID)
		}
	}
	f.payCalls++
	var sum int64
	for _, a := range allocs {
		sum += a.AmountCents
	}
	p := Payment{
		ID:          fmt.Sprintf("pay-%d", len(f.payments)+1),
		PaymentNo:   fmt.Sprintf("PAY-%d", len(f.payments)+1),
		CustomerID:  customerID,
		Method:      method,
		AmountCents: sum,
		Status:      "RECEIVED",
		GatewayRef:  gatewayRef,
		Allocations: allocs,
	}
	f.payments = append(f.payments, p)
	return p, nil
}

// - DIB-415 intent seam (in-memory, mirroring 0103's semantics) -
// The fake reproduces the ONE thing that matters about ClaimIntent: only the first caller for a key
// wins, and a loser is told the holder's status rather than being allowed to charge. The real
// implementation gets that from a unique index; this one gets it from a map, which is faithful
// because these unit tests are single-goroutine. The concurrent case is proved against the real
// database in the integration test, where it belongs.

func (f *fakeStore) ClaimIntent(_ context.Context, c IntentClaim) (Intent, bool, error) {
	if f.intents == nil {
		f.intents = map[string]*Intent{}
	}
	if existing, ok := f.intents[c.Key]; ok {
		if existing.Status == IntentFailed || existing.Status == IntentAbandoned {
			existing.Status = IntentCharging
			existing.AmountCents = c.AmountCents
			existing.GatewayRef = ""
			existing.LastError = ""
			existing.Attempts++
			return *existing, true, nil
		}
		return *existing, false, nil
	}
	in := &Intent{
		ID: fmt.Sprintf("intent-%d", len(f.intents)+1), Key: c.Key, CustomerID: c.CustomerID,
		AmountCents: c.AmountCents, InvoiceIDs: c.InvoiceIDs, Status: IntentCharging,
		Gateway: c.Gateway, Attempts: 1,
	}
	f.intents[c.Key] = in
	f.intentByID(in.ID, in)
	return *in, true, nil
}

func (f *fakeStore) GetIntentByKey(_ context.Context, key string) (Intent, bool, error) {
	if in, ok := f.intents[key]; ok {
		return *in, true, nil
	}
	return Intent{}, false, nil
}

// intentByID keeps a second index so the ID-keyed calls (stamp/fail/settle) can find a row without
// the caller having to carry the key around.
func (f *fakeStore) intentByID(id string, set *Intent) *Intent {
	if f.intentsByID == nil {
		f.intentsByID = map[string]*Intent{}
	}
	if set != nil {
		f.intentsByID[id] = set
	}
	return f.intentsByID[id]
}

func (f *fakeStore) StampIntentRef(_ context.Context, intentID, gateway, ref string) error {
	if in := f.intentByID(intentID, nil); in != nil {
		in.Gateway, in.GatewayRef = gateway, ref
	}
	return nil
}

func (f *fakeStore) FailIntent(_ context.Context, intentID, reason string) error {
	if in := f.intentByID(intentID, nil); in != nil && in.Status == IntentCharging {
		in.Status, in.LastError = IntentFailed, reason
	}
	return nil
}

func (f *fakeStore) PayWithIntent(ctx context.Context, customerID, method, gatewayRef string, allocs []Allocation, intentID string) (Payment, error) {
	p, err := f.Pay(ctx, customerID, method, gatewayRef, allocs)
	if err != nil {
		return Payment{}, err
	}
	if in := f.intentByID(intentID, nil); in != nil {
		in.Status, in.PaymentID, in.GatewayRef = IntentSettled, p.ID, gatewayRef
	}
	return p, nil
}

func (f *fakeStore) GetPaymentByID(_ context.Context, customerID, paymentID string) (Payment, bool, error) {
	for _, p := range f.payments {
		if p.ID == paymentID && p.CustomerID == customerID {
			return p, true, nil
		}
	}
	return Payment{}, false, nil
}

// spyGateway wraps the deterministic StubGateway to record Vault/Detach calls (so tests can
// assert the detach-before-soft-delete ordering and the nonce-exchange), while preserving
// the stub's deterministic approve/decline Charge behavior.
type spyGateway struct {
	*StubGateway
	detached    []string
	vaultCalls  int
	chargeCalls int
}

func newSpyGateway() *spyGateway { return &spyGateway{StubGateway: NewStubGateway()} }

func (g *spyGateway) Vault(ctx context.Context, req VaultRequest) (VaultedMethod, error) {
	g.vaultCalls++
	return g.StubGateway.Vault(ctx, req)
}

func (g *spyGateway) Charge(ctx context.Context, req ChargeRequest) (ChargeResult, error) {
	g.chargeCalls++
	return g.StubGateway.Charge(ctx, req)
}

func (g *spyGateway) DetachMethod(_ context.Context, token string) error {
	g.detached = append(g.detached, token)
	return nil
}

const tenantA = "cust-A"
const tenantB = "cust-B"

func TestAddMethod_VaultsAndStores(t *testing.T) {
	store := newFakeStore()
	gw := newSpyGateway()
	svc := NewService(store, gw)
	ctx := context.Background()

	m, err := svc.AddMethod(ctx, tenantA, AddMethodRequest{Nonce: "nonce-xyz", MakeDefault: true})
	if err != nil {
		t.Fatalf("add method: %v", err)
	}
	if gw.vaultCalls != 1 {
		t.Fatalf("expected exactly one vault call, got %d", gw.vaultCalls)
	}
	if m.Gateway != "stub" || m.Status != "active" {
		t.Fatalf("method not built from gateway/active: %+v", m)
	}
	if m.Brand != "VISA" || m.Last4 != "4242" {
		t.Fatalf("display metadata not carried from vault: %+v", m)
	}
	if !m.IsDefault {
		t.Fatalf("make_default not honored: %+v", m)
	}
	// The opaque token must be stored but never serialized; verify it round-trips via Get.
	got, err := store.GetMethod(ctx, tenantA, m.ID)
	if err != nil {
		t.Fatalf("get stored method: %v", err)
	}
	if got.Token() == "" {
		t.Fatal("expected a vault token to be persisted")
	}
}

func TestAddMethod_RequiresNonce(t *testing.T) {
	svc := NewService(newFakeStore(), newSpyGateway())
	if _, err := svc.AddMethod(context.Background(), tenantA, AddMethodRequest{}); err == nil {
		t.Fatal("expected a bad-request error when the nonce is empty")
	}
}

func TestRemoveMethod_DetachesThenSoftDeletes(t *testing.T) {
	store := newFakeStore()
	gw := newSpyGateway()
	svc := NewService(store, gw)
	ctx := context.Background()

	m, err := svc.AddMethod(ctx, tenantA, AddMethodRequest{Nonce: "n1"})
	if err != nil {
		t.Fatalf("seed method: %v", err)
	}
	if err := svc.RemoveMethod(ctx, tenantA, m.ID); err != nil {
		t.Fatalf("remove method: %v", err)
	}
	if len(gw.detached) != 1 || gw.detached[0] != m.Token() {
		t.Fatalf("expected DetachMethod called with the vault token, got %+v", gw.detached)
	}
	if got := store.methods[m.ID]; got.Status != "removed" {
		t.Fatalf("method not soft-deleted: %+v", got)
	}
}

func TestRemoveMethod_CrossTenantIsNotFound(t *testing.T) {
	store := newFakeStore()
	gw := newSpyGateway()
	svc := NewService(store, gw)
	ctx := context.Background()

	m, _ := svc.AddMethod(ctx, tenantA, AddMethodRequest{Nonce: "n1"})
	// Tenant B may not remove tenant A's method - it must look not-found, never forbidden.
	if err := svc.RemoveMethod(ctx, tenantB, m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for cross-tenant remove, got %v", err)
	}
	if len(gw.detached) != 0 {
		t.Fatal("cross-tenant remove must not reach the gateway detach")
	}
}

func TestSetDefaultMethod_MovesDefault(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, newSpyGateway())
	ctx := context.Background()

	first, _ := svc.AddMethod(ctx, tenantA, AddMethodRequest{Nonce: "n1", MakeDefault: true})
	second, _ := svc.AddMethod(ctx, tenantA, AddMethodRequest{Nonce: "n2"})

	if err := svc.SetDefaultMethod(ctx, tenantA, second.ID); err != nil {
		t.Fatalf("set default: %v", err)
	}
	if store.methods[first.ID].IsDefault {
		t.Fatal("prior default must be cleared")
	}
	if !store.methods[second.ID].IsDefault {
		t.Fatal("target method must become the default")
	}
}

func TestSetDefaultMethod_CrossTenantIsNotFound(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, newSpyGateway())
	ctx := context.Background()

	m, _ := svc.AddMethod(ctx, tenantA, AddMethodRequest{Nonce: "n1"})
	// Tenant B may not promote tenant A's method - not-found, never forbidden.
	if err := svc.SetDefaultMethod(ctx, tenantB, m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for cross-tenant set-default, got %v", err)
	}
}

func TestSetDefaultMethod_RequiresIDs(t *testing.T) {
	svc := NewService(newFakeStore(), newSpyGateway())
	err := svc.SetDefaultMethod(context.Background(), "", "")
	var bre httpx.BadRequestError
	if !errors.As(err, &bre) {
		t.Fatalf("want httpx.BadRequestError, got %T %v", err, err)
	}
}

func TestPay_ApprovesAndSumsExcludingHoldback(t *testing.T) {
	store := newFakeStore()
	store.seedInvoice("inv-1", tenantA, 5000, 1000) // payable 4000
	store.seedInvoice("inv-2", tenantA, 3000, 0)    // payable 3000
	gw := newSpyGateway()
	svc := NewService(store, gw)
	ctx := context.Background()

	m, _ := svc.AddMethod(ctx, tenantA, AddMethodRequest{Nonce: "n1"})

	p, err := svc.Pay(ctx, tenantA, PayRequest{
		InvoiceIDs:      []string{"inv-1", "inv-2"},
		PaymentMethodID: m.ID,
		ExpectedCents:   7000,
	})
	if err != nil {
		t.Fatalf("pay: %v", err)
	}
	// 4000 (holdback-excluded) + 3000 = 7000.
	if p.AmountCents != 7000 {
		t.Fatalf("charge sum should exclude holdback (want 7000), got %d", p.AmountCents)
	}
	var allocSum int64
	var inv1 int64
	for _, a := range p.Allocations {
		allocSum += a.AmountCents
		if a.InvoiceID == "inv-1" {
			inv1 = a.AmountCents
		}
	}
	if allocSum != 7000 {
		t.Fatalf("allocations must sum to the charge, got %d", allocSum)
	}
	if inv1 != 4000 {
		t.Fatalf("inv-1 allocation must exclude its 1000 holdback (want 4000), got %d", inv1)
	}
	if p.Method != "CARD" {
		t.Fatalf("stub card method should settle as CARD, got %q", p.Method)
	}
	if p.Status != "RECEIVED" || p.GatewayRef == "" {
		t.Fatalf("approved payment must be RECEIVED with a gateway ref: %+v", p)
	}
}

func TestPay_DeclinedMapsToErrDeclined(t *testing.T) {
	store := newFakeStore()
	// Payable 113 -> cents%100==13 triggers the stub deterministic decline.
	store.seedInvoice("inv-1", tenantA, 200, 87) // payable 113
	gw := newSpyGateway()
	svc := NewService(store, gw)
	ctx := context.Background()

	m, _ := svc.AddMethod(ctx, tenantA, AddMethodRequest{Nonce: "n1"})

	_, err := svc.Pay(ctx, tenantA, PayRequest{InvoiceIDs: []string{"inv-1"}, PaymentMethodID: m.ID, ExpectedCents: 113})
	if !errors.Is(err, ErrDeclined) {
		t.Fatalf("expected ErrDeclined, got %v", err)
	}
	if store.payCalls != 0 {
		t.Fatal("a declined charge must persist nothing")
	}
}

func TestPay_IdempotentReplay(t *testing.T) {
	store := newFakeStore()
	store.seedInvoice("inv-1", tenantA, 5000, 0)
	gw := newSpyGateway()
	svc := NewService(store, gw)
	ctx := context.Background()

	m, _ := svc.AddMethod(ctx, tenantA, AddMethodRequest{Nonce: "n1"})
	req := PayRequest{InvoiceIDs: []string{"inv-1"}, PaymentMethodID: m.ID, ExpectedCents: 5000}

	first, err := svc.Pay(ctx, tenantA, req)
	if err != nil {
		t.Fatalf("first pay: %v", err)
	}
	second, err := svc.Pay(ctx, tenantA, req)
	if err != nil {
		t.Fatalf("replay pay: %v", err)
	}
	if first.ID != second.ID || first.GatewayRef != second.GatewayRef {
		t.Fatalf("replay must return the same payment: %+v vs %+v", first, second)
	}
	if store.payCalls != 1 {
		t.Fatalf("replay must not write a second payment, payCalls=%d", store.payCalls)
	}
}

func TestPay_CrossTenantInvoiceIsNotFound(t *testing.T) {
	store := newFakeStore()
	store.seedInvoice("inv-1", tenantB, 5000, 0) // belongs to tenant B
	gw := newSpyGateway()
	svc := NewService(store, gw)
	ctx := context.Background()

	m, _ := svc.AddMethod(ctx, tenantA, AddMethodRequest{Nonce: "n1"})

	// Tenant A pays an invoice it does not own -> not-found, never a cross-tenant charge.
	_, err := svc.Pay(ctx, tenantA, PayRequest{InvoiceIDs: []string{"inv-1"}, PaymentMethodID: m.ID, ExpectedCents: 5000})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for a cross-tenant invoice, got %v", err)
	}
	if store.payCalls != 0 {
		t.Fatal("cross-tenant pay must persist nothing")
	}
}

func TestPay_NoPayableBalanceRejected(t *testing.T) {
	store := newFakeStore()
	store.seedInvoice("inv-1", tenantA, 1000, 1000) // fully held back -> payable 0
	gw := newSpyGateway()
	svc := NewService(store, gw)
	ctx := context.Background()

	m, _ := svc.AddMethod(ctx, tenantA, AddMethodRequest{Nonce: "n1"})

	_, err := svc.Pay(ctx, tenantA, PayRequest{InvoiceIDs: []string{"inv-1"}, PaymentMethodID: m.ID, ExpectedCents: 5000})
	if err == nil || errors.Is(err, ErrDeclined) {
		t.Fatalf("expected a bad-request (no payable balance), got %v", err)
	}
	if store.payCalls != 0 {
		t.Fatal("a zero-balance selection must not charge or persist")
	}
}

// Re-review #1: a multi-invoice selection containing ONE zero-payable invoice must be
// rejected BEFORE the gateway is charged - never charge-then-rollback.
func TestPay_ZeroPayableInSelectionRejectedBeforeCharge(t *testing.T) {
	store := newFakeStore()
	store.seedInvoice("inv-1", tenantA, 5000, 0)    // payable 5000
	store.seedInvoice("inv-2", tenantA, 1000, 1000) // fully held back -> payable 0
	gw := newSpyGateway()
	svc := NewService(store, gw)
	ctx := context.Background()

	m, _ := svc.AddMethod(ctx, tenantA, AddMethodRequest{Nonce: "n1"})

	_, err := svc.Pay(ctx, tenantA, PayRequest{InvoiceIDs: []string{"inv-1", "inv-2"}, PaymentMethodID: m.ID, ExpectedCents: 5000})
	var bre httpx.BadRequestError
	if !errors.As(err, &bre) {
		t.Fatalf("want httpx.BadRequestError for a zero-payable invoice, got %T %v", err, err)
	}
	if gw.chargeCalls != 0 {
		t.Fatalf("a non-payable selection must not reach the gateway charge, got %d calls", gw.chargeCalls)
	}
	if store.payCalls != 0 {
		t.Fatal("a non-payable selection must persist nothing")
	}
}

// Re-review #1: a non-payable STATUS (e.g. already PAID) in the selection is rejected before
// the charge even when it still carries a positive payable amount.
func TestPay_NonPayableStatusRejectedBeforeCharge(t *testing.T) {
	store := newFakeStore()
	store.seedInvoice("inv-1", tenantA, 5000, 0)               // FINALIZED, payable 5000
	store.seedInvoiceStatus("inv-2", tenantA, 2000, 0, "PAID") // PAID -> not payable
	gw := newSpyGateway()
	svc := NewService(store, gw)
	ctx := context.Background()

	m, _ := svc.AddMethod(ctx, tenantA, AddMethodRequest{Nonce: "n1"})

	_, err := svc.Pay(ctx, tenantA, PayRequest{InvoiceIDs: []string{"inv-1", "inv-2"}, PaymentMethodID: m.ID, ExpectedCents: 5000})
	var bre httpx.BadRequestError
	if !errors.As(err, &bre) {
		t.Fatalf("want httpx.BadRequestError for a PAID invoice, got %T %v", err, err)
	}
	if gw.chargeCalls != 0 {
		t.Fatalf("a PAID invoice in the selection must not reach the gateway charge, got %d", gw.chargeCalls)
	}
}

// Re-review #2: a soft-deleted ('removed') method must not be chargeable - GetMethod returns
// ErrNotFound and the gateway is never touched.
func TestPay_RemovedMethodIsNotFound(t *testing.T) {
	store := newFakeStore()
	store.seedInvoice("inv-1", tenantA, 5000, 0)
	gw := newSpyGateway()
	svc := NewService(store, gw)
	ctx := context.Background()

	m, _ := svc.AddMethod(ctx, tenantA, AddMethodRequest{Nonce: "n1"})
	if err := svc.RemoveMethod(ctx, tenantA, m.ID); err != nil {
		t.Fatalf("remove method: %v", err)
	}

	_, err := svc.Pay(ctx, tenantA, PayRequest{InvoiceIDs: []string{"inv-1"}, PaymentMethodID: m.ID, ExpectedCents: 5000})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound charging a removed method, got %v", err)
	}
	if gw.chargeCalls != 0 {
		t.Fatalf("a removed method must not reach the gateway charge, got %d", gw.chargeCalls)
	}
	if store.payCalls != 0 {
		t.Fatal("a removed method must persist nothing")
	}
}

func TestPay_DuplicateInvoiceRejected(t *testing.T) {
	store := newFakeStore()
	store.seedInvoice("inv-1", tenantA, 5000, 0)
	gw := newSpyGateway()
	svc := NewService(store, gw)
	ctx := context.Background()

	m, _ := svc.AddMethod(ctx, tenantA, AddMethodRequest{Nonce: "n1"})

	_, err := svc.Pay(ctx, tenantA, PayRequest{InvoiceIDs: []string{"inv-1", "inv-1"}, PaymentMethodID: m.ID, ExpectedCents: 5000})
	if err == nil {
		t.Fatal("expected a duplicate-invoice rejection")
	}
	if store.payCalls != 0 {
		t.Fatal("duplicate selection must not persist")
	}
}
