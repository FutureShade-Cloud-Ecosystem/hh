package pos

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	iauth "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5"
)

// -- DB-free fakes (the pricing/service_test.go pattern) ---------------------

type fakeStore struct {
	tr *[]string

	// policyErr, when set, fails every policy read (the standing read's unreadable-policy leg).
	policyErr error

	walkInID  string
	customers map[string]customerRec
	cached    map[string]Sale
	profiles  map[string]eligibilityMasked
	stock     map[string]map[string]float64
	views     map[string]SaleView

	orders      []orderRow
	orderLines  []orderLineRow
	invoices    []invoiceRow
	custTxns    []customerTxnRow
	allocations []allocRec
	posSales    []posSaleRow
	saleTenders []saleTenderRow // DIB-165 split-tender legs
	lookups     []SaleSummary   // DIB-165 canned past-sale lookup result
	lookupQuery SaleLookupQuery // the query the service passed down (fence assertions)
	snapshots   []taxSnapshotRow
	events      []string
	eventLoads  []any // payload parallel to events[i] (so a test can inspect an emitted event body)
	signatures  []signatureRow

	// -- W7 counter-v2 state --
	policies        map[string]PosPolicy        // branch_id ("" = org default) → row
	userWS          map[string]userWorkspaceRow // DIB-488 rev2: user_id → workspace ergonomics row
	userWSWrites    int                         // upsert count, the replay tests assert "no second write"
	cachedReturns   map[string]Return           // idempotency key → committed return
	retCtx          map[string]returnOrderCtx   // order_id → the original-sale context
	retLines        map[string]returnableLine   // order_line_id → the sold line (prior sums derive from returnLineRows)
	returnRows      []returnRow
	returnLineRows  []returnLineRow
	priorSums       map[string]int64  // order_id → the sale's earlier committed return total (the per-sale sum the refund gate reads)
	returnLocks     []string          // order ids the per-sale return advisory lock was taken for, in order
	refundPayments  map[string]string // return id → refund payment id
	creditApplies   []creditApply
	invoiceBalance  map[string]int64 // invoice id → open balance (drives ApplyCreditToInvoice)
	parked          map[string]ParkedTicket
	lastResumeActor string // the actor the last resume stamped (the parked fence's who-resumed)
	pricingDocs     map[string]PricingOrder
	products        map[string][2]string     // product id → {sku, name}
	stockMeta       map[string]fakeStockMeta // DIB-488: canned glance unit meta per product
	staff           map[string]string        // DIB-488 rev 6: user id → display name (ACTIVE staff)
	discountStaff   []discountStaffRow       // the counter discount's candidate logins (discount_test.go)
	discountReads   []SaleDiscount           // canned ReadSaleDiscounts answer
	// -- DIB-488 rev 8 canned state (interface compliance, the rev-1/2/5/6 fake-extension class) --
	weights       map[string]float64  // product id → kg per base unit (absent = none on file)
	branchCosts   map[string]int64    // product id → WAC cents at the (single) test branch
	costAsks      int                 // how many times a cost read was made (the gate-runs-first proof)
	addonPairings map[string][]string // source application → suggested applications
	addonCands    []addonCandidate    // canned ListAddonCandidates answer
	addonAsked    [][]string          // the application lists the service asked with
	productApps   map[string]string   // product id → application
	topApps       []string            // canned TopSellingApplications answer
	pickups       map[string]ReturnPickup
	// A-06 portal return requests (methods live in return_requests_test.go)
	returnRequests map[string]ReturnRequest

	seq int
}

type allocRec struct {
	paymentID, invoiceID string
	amount               int64
	allocatedBy          string
}

// fakeStockMeta is one product's canned DIB-488 glance unit meta.
type fakeStockMeta struct {
	baseUOM, sellUOM string
	sellFactor       float64
	lowBase          map[string]float64
}

type creditApply struct {
	invoiceID string
	amount    int64
	applied   int64
}

func newFakeStore(tr *[]string) *fakeStore {
	return &fakeStore{
		tr:        tr,
		walkInID:  "walkin-house",
		customers: map[string]customerRec{},
		cached:    map[string]Sale{},
		profiles:  map[string]eligibilityMasked{},
		stock:     map[string]map[string]float64{},
		views:     map[string]SaleView{},

		policies:       map[string]PosPolicy{"": {CODEnabled: true}},
		userWS:         map[string]userWorkspaceRow{},
		cachedReturns:  map[string]Return{},
		retCtx:         map[string]returnOrderCtx{},
		retLines:       map[string]returnableLine{},
		priorSums:      map[string]int64{},
		refundPayments: map[string]string{},
		invoiceBalance: map[string]int64{},
		parked:         map[string]ParkedTicket{},
		pricingDocs:    map[string]PricingOrder{},
		products:       map[string][2]string{},
		stockMeta:      map[string]fakeStockMeta{},
		staff:          map[string]string{},
		pickups:        map[string]ReturnPickup{},
		returnRequests: map[string]ReturnRequest{},
	}
}

func (f *fakeStore) log(s string) {
	if f.tr != nil {
		*f.tr = append(*f.tr, s)
	}
}

type txSnap struct{ o, ol, inv, ct, al, ps, st, sn, ev, rr, rl, ca int }

func (f *fakeStore) snap() txSnap {
	return txSnap{len(f.orders), len(f.orderLines), len(f.invoices), len(f.custTxns),
		len(f.allocations), len(f.posSales), len(f.saleTenders), len(f.snapshots), len(f.events),
		len(f.returnRows), len(f.returnLineRows), len(f.creditApplies)}
}

func (f *fakeStore) restore(s txSnap) {
	f.orders = f.orders[:s.o]
	f.orderLines = f.orderLines[:s.ol]
	f.invoices = f.invoices[:s.inv]
	f.custTxns = f.custTxns[:s.ct]
	f.allocations = f.allocations[:s.al]
	f.posSales = f.posSales[:s.ps]
	f.saleTenders = f.saleTenders[:s.st] // DIB-165: a rolled-back split leaves no legs
	f.snapshots = f.snapshots[:s.sn]
	f.events = f.events[:s.ev]
	f.eventLoads = f.eventLoads[:s.ev]
	f.returnRows = f.returnRows[:s.rr]
	f.returnLineRows = f.returnLineRows[:s.rl]
	f.creditApplies = f.creditApplies[:s.ca]
}

// lastEvent returns the payload of the most recent emitted event with the given topic.
func (f *fakeStore) lastEvent(topic string) (map[string]any, bool) {
	for i := len(f.events) - 1; i >= 0; i-- {
		if f.events[i] != topic {
			continue
		}
		m, _ := f.eventLoads[i].(map[string]any)
		return m, true
	}
	return nil, false
}

func (f *fakeStore) RunInTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	before := f.snap()
	if err := fn(nil); err != nil {
		f.restore(before) // emulate ROLLBACK, nothing staged in this tx persists
		return err
	}
	return nil
}

func (f *fakeStore) FindSaleByKey(_ context.Context, key string) (Sale, bool, error) {
	s, ok := f.cached[key]
	return s, ok, nil
}

func (f *fakeStore) ReadCustomer(_ context.Context, id string) (customerRec, error) {
	if c, ok := f.customers[id]; ok {
		return c, nil
	}
	return customerRec{ID: id, CustomerType: "RETAIL"}, nil
}

// The tx twin answers from the same map: the fake has one world, the tx
// shape exists so the service's in-transaction customer read stays tx-bound.
func (f *fakeStore) ReadCustomerTx(_ context.Context, _ pgx.Tx, id string) (customerRec, error) {
	return f.ReadCustomer(context.Background(), id)
}

func (f *fakeStore) ResolveWalkIn(_ context.Context) (string, error) { return f.walkInID, nil }

func (f *fakeStore) ReadStockOnHand(_ context.Context, productID string) (map[string]float64, error) {
	return f.stock[productID], nil
}

// DIB-488: canned unit meta for the glance. Zero-valued when a test never sets it, which is the
// honest "no conversion on file / no thresholds authored" shape.
func (f *fakeStore) ReadStockUnitMeta(_ context.Context, productID string) (string, string, float64, map[string]float64, error) {
	m := f.stockMeta[productID]
	return m.baseUOM, m.sellUOM, m.sellFactor, m.lowBase, nil
}

func (f *fakeStore) ReadEligibilityProfileMasked(_ context.Context, id string) (eligibilityMasked, error) {
	return f.profiles[id], nil
}

func (f *fakeStore) GetSaleView(_ context.Context, id string) (SaleView, bool, error) {
	v, ok := f.views[id]
	return v, ok, nil
}

func (f *fakeStore) InsertOrder(_ context.Context, _ pgx.Tx, o orderRow) (string, string, error) {
	f.log("order")
	f.seq++
	f.orders = append(f.orders, o)
	return "order-" + itoa(f.seq), "#SO-" + itoa(f.seq), nil
}

func (f *fakeStore) InsertOrderLines(_ context.Context, _ pgx.Tx, orderID string, lines []orderLineRow) ([]string, error) {
	ids := make([]string, len(lines))
	for i, l := range lines {
		f.orderLines = append(f.orderLines, l)
		ids[i] = "line-" + itoa(i+1)
	}
	return ids, nil
}

func (f *fakeStore) InsertInvoice(_ context.Context, _ pgx.Tx, inv invoiceRow) (string, string, error) {
	f.log("invoice")
	f.seq++
	f.invoices = append(f.invoices, inv)
	return "inv-" + itoa(f.seq), "#INV-" + itoa(f.seq), nil
}

func (f *fakeStore) InsertInvoiceLines(_ context.Context, _ pgx.Tx, _ string, _ []string, _ []orderLineRow) error {
	return nil
}

func (f *fakeStore) InsertPaymentAllocation(_ context.Context, _ pgx.Tx, paymentID, invoiceID string, amt int64, allocatedBy string) error {
	f.log("alloc")
	f.allocations = append(f.allocations, allocRec{paymentID, invoiceID, amt, allocatedBy})
	return nil
}

func (f *fakeStore) InsertCustomerTxn(_ context.Context, _ pgx.Tx, t customerTxnRow) error {
	f.custTxns = append(f.custTxns, t)
	return nil
}

func (f *fakeStore) InsertPosSale(_ context.Context, _ pgx.Tx, s posSaleRow) (string, error) {
	f.posSales = append(f.posSales, s)
	return "possale-1", nil
}

func (f *fakeStore) InsertSaleTenders(_ context.Context, _ pgx.Tx, _ string, legs []saleTenderRow) error {
	f.log("split-tenders")
	f.saleTenders = append(f.saleTenders, legs...)
	return nil
}

func (f *fakeStore) LookupSales(_ context.Context, q SaleLookupQuery) ([]SaleSummary, error) {
	f.lookupQuery = q
	return f.lookups, nil
}

func (f *fakeStore) InsertTaxSnapshot(_ context.Context, _ pgx.Tx, snap taxSnapshotRow) (string, error) {
	f.snapshots = append(f.snapshots, snap)
	return "snap-1", nil
}

func (f *fakeStore) EmitEvent(_ context.Context, _ pgx.Tx, topic string, payload any) error {
	f.events = append(f.events, topic)
	f.eventLoads = append(f.eventLoads, payload)
	return nil
}

func (f *fakeStore) InsertSignature(_ context.Context, sig signatureRow) (string, error) {
	f.signatures = append(f.signatures, sig)
	return "sig-row-1", nil
}

func (f *fakeStore) InsertSignatureTx(_ context.Context, _ pgx.Tx, sig signatureRow) (string, error) {
	f.signatures = append(f.signatures, sig)
	return "sig-row-tx", nil
}

// -- W7 counter-v2 fakes -----------------------------------------------------

func (f *fakeStore) ReadPosPolicy(_ context.Context, branchID string) (PosPolicy, error) {
	if f.policyErr != nil {
		return PosPolicy{}, f.policyErr
	}
	if p, ok := f.policies[branchID]; ok {
		return p, nil
	}
	return f.policies[""], nil
}

func (f *fakeStore) UpsertPosPolicy(_ context.Context, p PosPolicy) error {
	f.policies[p.BranchID] = p
	return nil
}

// -- DIB-488 rev 2: pos_user_workspace fakes (interface compliance; behavior in dib488_ws_test.go) --

func (f *fakeStore) ReadUserWorkspace(_ context.Context, userID string) (userWorkspaceRow, bool, error) {
	row, ok := f.userWS[userID]
	return row, ok, nil
}

func (f *fakeStore) UpsertUserWorkspace(_ context.Context, row userWorkspaceRow) error {
	f.userWS[row.UserID] = row
	f.userWSWrites++
	return nil
}

func (f *fakeStore) DeleteUserWorkspace(_ context.Context, userID string) error {
	delete(f.userWS, userID)
	return nil
}

func (f *fakeStore) FindReturnByKey(_ context.Context, key string) (Return, bool, error) {
	r, ok := f.cachedReturns[key]
	return r, ok, nil
}

// The tx twins answer from the same maps: the fake has one world, the tx
// shape exists so the service's in-transaction re-check stays tx-bound.
func (f *fakeStore) FindReturnByKeyTx(_ context.Context, _ pgx.Tx, key string) (Return, bool, error) {
	return f.FindReturnByKey(context.Background(), key)
}

func (f *fakeStore) ReturnOrderContextTx(_ context.Context, _ pgx.Tx, orderID, orderNo string) (returnOrderCtx, bool, error) {
	return f.ReturnOrderContext(context.Background(), orderID, orderNo)
}

func (f *fakeStore) ReturnOrderContextFor(ctx context.Context, orderID, orderNo string, _ []string) (returnOrderCtx, bool, error) {
	return f.ReturnOrderContext(ctx, orderID, orderNo)
}

func (f *fakeStore) ReturnOrderContext(_ context.Context, orderID, orderNo string) (returnOrderCtx, bool, error) {
	if oc, ok := f.retCtx[orderID]; ok {
		return oc, true, nil
	}
	for _, oc := range f.retCtx {
		// The real query compares the number without its hash and in upper case (normalizeSaleRef
		// has already stripped the typed one), so the fake does the same with the stored number.
		if orderNo != "" && strings.TrimPrefix(strings.ToUpper(oc.OrderNo), "#") == orderNo {
			return oc, true, nil
		}
	}
	return returnOrderCtx{}, false, nil
}

func (f *fakeStore) ListReturnableLines(_ context.Context, orderID string) ([]ReturnableLineView, error) {
	out := []ReturnableLineView{}
	for id, rl := range f.retLines {
		if f.retCtx[orderID].OrderID != orderID {
			continue
		}
		v := ReturnableLineView{
			OrderLineID: id, ProductID: rl.ProductID, QtySold: rl.QtySold,
			UnitPriceCents: rl.UnitPriceCents, LineTotalCents: rl.LineTotalCents, TaxCents: rl.TaxCents,
		}
		for _, prev := range f.returnLineRows {
			if prev.OrderLineID == id {
				v.QtyReturned += prev.QtyBase
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func (f *fakeStore) LockReturnableLine(_ context.Context, _ pgx.Tx, orderID, orderLineID string) (returnableLine, bool, error) {
	rl, ok := f.retLines[orderLineID]
	if !ok || f.retCtx[orderID].OrderID != orderID {
		return returnableLine{}, false, nil
	}
	// Prior sums derive from the COMMITTED return lines (the real repo sums pos_return_lines).
	for _, prev := range f.returnLineRows {
		if prev.OrderLineID == orderLineID {
			rl.PriorQty += prev.QtyBase
			rl.PriorMoney += prev.LineTotalCents
			rl.PriorTax += prev.TaxCents
		}
	}
	return rl, true, nil
}

func (f *fakeStore) InsertReturn(_ context.Context, _ pgx.Tx, r returnRow) (string, string, error) {
	f.log("return")
	f.returnRows = append(f.returnRows, r)
	f.seq++
	id := "ret-" + itoa(f.seq)
	no := "#RET-" + itoa(f.seq)
	// The real store replays by the idempotency key (ux_pos_returns_idem
	// then FindReturnByKey); the fake mirrors that, so the replay paths are
	// exercisable against the harness.
	if r.IdempotencyKey != "" {
		f.cachedReturns[r.IdempotencyKey] = Return{
			ReturnID: id, ReturnNo: no, OrderID: r.OrderID, InvoiceID: r.InvoiceID,
			RefundMethod: r.RefundMethod, SubtotalCents: r.SubtotalCents, TaxCents: r.TaxCents,
			TotalCents: r.TotalCents, FeeCents: r.FeeCents, Status: "COMMITTED",
		}
	}
	return id, no, nil
}

func (f *fakeStore) InsertReturnLines(_ context.Context, _ pgx.Tx, _ string, lines []returnLineRow) error {
	f.returnLineRows = append(f.returnLineRows, lines...)
	return nil
}

// The per-sale return sums and lock: the fake answers the map a test set
// directly (the real store sums pos_returns; see the LockSaleForReturn and
// SumCommittedReturnTotals implementations in repository_w7.go).
func (f *fakeStore) SumCommittedReturnTotals(_ context.Context, orderID string) (int64, error) {
	return f.priorSums[orderID], nil
}

func (f *fakeStore) SumCommittedReturnTotalsTx(_ context.Context, _ pgx.Tx, orderID string) (int64, error) {
	return f.priorSums[orderID], nil
}

func (f *fakeStore) LockSaleForReturn(_ context.Context, _ pgx.Tx, orderID string) error {
	f.returnLocks = append(f.returnLocks, orderID)
	return nil
}

func (f *fakeStore) SetReturnRefundPayment(_ context.Context, _ pgx.Tx, returnID, paymentID string) error {
	f.refundPayments[returnID] = paymentID
	return nil
}

func (f *fakeStore) ApplyCreditToInvoice(_ context.Context, _ pgx.Tx, invoiceID string, amountCents int64) (int64, error) {
	bal := f.invoiceBalance[invoiceID]
	applied := amountCents
	if applied > bal {
		applied = bal
	}
	f.invoiceBalance[invoiceID] = bal - applied
	f.creditApplies = append(f.creditApplies, creditApply{invoiceID, amountCents, applied})
	return applied, nil
}

func (f *fakeStore) ListReturnPickups(_ context.Context, branchID string) ([]ReturnPickup, error) {
	out := []ReturnPickup{}
	for _, p := range f.pickups {
		if p.BranchID == branchID && (p.PickupStatus == PickupRequested || p.PickupStatus == PickupScheduled) {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeStore) SetPickupStatus(_ context.Context, returnID, from, to string) error {
	p, ok := f.pickups[returnID]
	if !ok || p.PickupStatus != from {
		return clientErr(409, "pos: pickup already moved past %s", from)
	}
	p.PickupStatus = to
	f.pickups[returnID] = p
	return nil
}

func (f *fakeStore) GetReturnPickup(_ context.Context, returnID string) (ReturnPickup, bool, error) {
	p, ok := f.pickups[returnID]
	return p, ok, nil
}

func (f *fakeStore) InsertParkedTicket(_ context.Context, t ParkedTicket, actor string) (ParkedTicket, error) {
	f.seq++
	t.ID = "park-" + itoa(f.seq)
	t.TicketNo = "#PARK-" + itoa(f.seq)
	t.Status = "PARKED"
	_ = actor
	f.parked[t.ID] = t
	return t, nil
}

func (f *fakeStore) ListParkedTickets(_ context.Context, branchID string) ([]ParkedTicket, error) {
	out := []ParkedTicket{}
	for _, t := range f.parked {
		if t.BranchID == branchID && t.Status == "PARKED" {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *fakeStore) GetParkedTicket(_ context.Context, id string) (ParkedTicket, bool, error) {
	t, ok := f.parked[id]
	return t, ok, nil
}

func (f *fakeStore) MarkParkedResumed(_ context.Context, id, actor string) (ParkedTicket, error) {
	t, ok := f.parked[id]
	if !ok {
		return ParkedTicket{}, errParkedNotFound
	}
	if t.Status != "PARKED" {
		return ParkedTicket{}, clientErr(409, "pos: cannot resume a %s ticket", t.Status)
	}
	t.Status = "RESUMED"
	f.parked[id] = t
	f.lastResumeActor = actor
	return t, nil
}

func (f *fakeStore) MarkParkedDiscarded(_ context.Context, id string) error {
	t, ok := f.parked[id]
	if !ok {
		return errParkedNotFound
	}
	if t.Status != "PARKED" {
		return clientErr(409, "pos: cannot discard a %s ticket", t.Status)
	}
	t.Status = "DISCARDED"
	f.parked[id] = t
	return nil
}

func (f *fakeStore) InsertPricingOrder(_ context.Context, po PricingOrder, _ string) (PricingOrder, error) {
	f.seq++
	po.ID = "price-" + itoa(f.seq)
	po.DocNo = "#PRICE-" + itoa(f.seq)
	f.pricingDocs[po.ID] = po
	return po, nil
}

func (f *fakeStore) GetPricingOrder(_ context.Context, id string) (PricingOrder, bool, error) {
	po, ok := f.pricingDocs[id]
	return po, ok, nil
}

func (f *fakeStore) ListPricingOrders(_ context.Context, branchID string, _ int) ([]PricingOrder, error) {
	out := []PricingOrder{}
	for _, po := range f.pricingDocs {
		if po.BranchID == branchID {
			out = append(out, po)
		}
	}
	return out, nil
}

func (f *fakeStore) ReadProductSnapshot(_ context.Context, productID string) (string, string, error) {
	if p, ok := f.products[productID]; ok {
		return p[0], p[1], nil
	}
	return "SKU-" + productID, "Product " + productID, nil
}

// DIB-488 rev 6 (interface compliance, the same class of touch as ReadStockUnitMeta): the
// salesperson reads, backed by the canned staff map.
func (f *fakeStore) ReadStaffName(_ context.Context, userID string) (string, bool, error) {
	name, ok := f.staff[userID]
	return name, ok, nil
}

func (f *fakeStore) ListDiscountStaff(_ context.Context, _, _, userID string) ([]discountStaffRow, error) {
	var out []discountStaffRow
	for _, r := range f.discountStaff {
		if userID == "" || r.UserID == userID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeStore) ReadSaleDiscounts(_ context.Context, _ string) ([]SaleDiscount, error) {
	return f.discountReads, nil
}

func (f *fakeStore) ListSalespeople(_ context.Context) ([]Salesperson, error) {
	var out []Salesperson
	for id, name := range f.staff {
		out = append(out, Salesperson{ID: id, Name: name})
	}
	return out, nil
}

// -- DIB-488 rev 8: interface compliance (the same fake-extension class as ReadStockUnitMeta,
// the rev-2 workspace methods, rev-5 FindOpenSession and the rev-6 salesperson reads) --

func (f *fakeStore) ReadProductWeightKg(_ context.Context, productID string) (float64, bool, error) {
	kg, ok := f.weights[productID]
	return kg, ok && kg > 0, nil
}

func (f *fakeStore) ReadBranchCosts(_ context.Context, productIDs []string, _ string) (map[string]int64, error) {
	f.costAsks++
	out := map[string]int64{}
	for _, id := range productIDs {
		if c, ok := f.branchCosts[id]; ok && c > 0 {
			out[id] = c
		}
	}
	return out, nil
}

func (f *fakeStore) ReadAddonPairings(_ context.Context) (map[string][]string, error) {
	if f.addonPairings == nil {
		return map[string][]string{}, nil
	}
	return f.addonPairings, nil
}

func (f *fakeStore) ListAddonCandidates(_ context.Context, applications, exclude []string, limit int) ([]addonCandidate, error) {
	f.addonAsked = append(f.addonAsked, applications)
	skip := map[string]bool{}
	for _, id := range exclude {
		skip[id] = true
	}
	var out []addonCandidate
	for _, c := range f.addonCands {
		if skip[c.ProductID] || len(out) >= limit {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func (f *fakeStore) ReadProductApplications(_ context.Context, productIDs []string) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range productIDs {
		if a, ok := f.productApps[id]; ok && a != "" {
			out[id] = a
		}
	}
	return out, nil
}

func (f *fakeStore) TopSellingApplications(_ context.Context, limit int) ([]string, error) {
	if len(f.topApps) > limit {
		return f.topApps[:limit], nil
	}
	return f.topApps, nil
}

// fakeResolver is the pricing seam. blocked/policy play the GATED resolve's verdict on the same
// line (what the quote flow's floor would say); the till prices through the counter resolve and
// ignores that verdict (D48 row 481, D49).
type fakeResolver struct {
	discountLogs []pricing.CounterDiscountRecord // the pricing log rows the ring wrote for manual discounts
	tr           *[]string
	price        pricing.LinePrice
	blocked      bool
	policy       string
	err          error
}

func (r *fakeResolver) DeriveCounterLinePrice(_ context.Context, _ pricing.ResolveInput) (pricing.LinePrice, error) {
	if r.tr != nil {
		*r.tr = append(*r.tr, "price")
	}
	if r.err != nil {
		return pricing.LinePrice{}, r.err
	}
	lp := r.price
	lp.Breakdown.Blocked = r.blocked
	lp.Breakdown.BlockPolicy = r.policy
	return lp, nil
}

// RecordCounterDiscountTx makes the fake resolver the pricing log's counter-discount door.
func (r *fakeResolver) RecordCounterDiscountTx(_ context.Context, _ pgx.Tx, rec pricing.CounterDiscountRecord) error {
	r.discountLogs = append(r.discountLogs, rec)
	return nil
}

// fakeTender is the till seam.
type fakeTender struct {
	tr         *[]string
	paymentID  string
	gatewayRef string
	err        error
	called     bool
	last       till.TenderInput

	// The capture seam's double: a canned capture per card leg (the id derives from the leg key
	// so a split's legs bind distinctly), a canned decline when captureErr is set, and the
	// compensations the failure paths run, recorded.
	captureErr   error
	captureCalls []till.CardChargeInput
	compCalls    []string

	refundPaymentID string
	refundErr       error
	refundCalled    bool
	lastRefund      till.RefundInput

	// DIB-165: a split takes several tenders, so the fake records EVERY call and mints a distinct
	// payment id per leg. The first call still returns paymentID verbatim, so every pre-existing
	// single-tender assertion ("pay-1") is untouched.
	takes []till.TenderInput

	// receiptRecords collects the post-commit RecordPaymentReceived hook calls (M4).
	receiptRecords []till.TenderResult

	// sessionBranch is the branch GetSession reports for the drawer (DIB-389). Defaults to
	// baseInput()'s branch so the equality guard is satisfied unless a test deliberately diverges.
	sessionBranch string
	sessionErr    error
}

func (t *fakeTender) GetSession(_ context.Context, id string) (till.SessionDetail, error) {
	if t.sessionErr != nil {
		return till.SessionDetail{}, t.sessionErr
	}
	return till.SessionDetail{TillSession: till.TillSession{ID: id, BranchID: t.sessionBranch, Status: "OPEN"}}, nil
}

// CaptureCard is the pre-transaction capture double: one canned capture per leg, keyed by the leg
// key so a split binds distinctly, unless the test set a decline or another typed refusal.
func (t *fakeTender) CaptureCard(_ context.Context, in till.CardChargeInput) (till.CardCapture, error) {
	if in.ReadOnly {
		return till.CardCapture{}, till.ErrCardLegUnattempted
	}
	t.captureCalls = append(t.captureCalls, in)
	if t.captureErr != nil {
		return till.CardCapture{AttemptID: "att-fake-" + in.LegKey}, t.captureErr
	}
	return till.CardCapture{
		Kind:         till.CardCaptureCaptured,
		AttemptID:    "att-fake-" + in.LegKey,
		ProcessorRef: t.gatewayRef,
	}, nil
}

// CompensateCard is the compensation double: every captured leg reverses cleanly.
func (t *fakeTender) CompensateCard(_ context.Context, attemptID string) (till.CardCompensation, error) {
	t.compCalls = append(t.compCalls, attemptID)
	return till.CardCompensation{Kind: till.CardCompVoided, AttemptID: attemptID}, nil
}

func (t *fakeTender) TakeTender(_ context.Context, _ pgx.Tx, in till.TenderInput) (till.TenderResult, error) {
	if t.tr != nil {
		*t.tr = append(*t.tr, "tender")
	}
	t.called = true
	t.last = in
	t.takes = append(t.takes, in)
	if t.err != nil {
		return till.TenderResult{}, t.err
	}
	paymentID := t.paymentID
	if n := len(t.takes); n > 1 {
		paymentID = fmt.Sprintf("%s-leg%d", t.paymentID, n)
	}
	return till.TenderResult{
		PaymentID: paymentID, GatewayRef: t.gatewayRef,
		PaymentNo: "PMT-" + paymentID, Method: in.Method, AmountCents: in.AmountCents, Inserted: true,
	}, nil
}

func (t *fakeTender) RecordPaymentReceived(_ context.Context, res till.TenderResult) {
	if t.tr != nil {
		*t.tr = append(*t.tr, "record-received")
	}
	t.receiptRecords = append(t.receiptRecords, res)
}

func (t *fakeTender) RefundTender(_ context.Context, _ pgx.Tx, in till.RefundInput) (till.RefundResult, error) {
	if t.tr != nil {
		*t.tr = append(*t.tr, "refund-tender")
	}
	t.refundCalled = true
	t.lastRefund = in
	if t.refundErr != nil {
		return till.RefundResult{}, t.refundErr
	}
	id := t.refundPaymentID
	if id == "" {
		id = "refund-pay-1"
	}
	return till.RefundResult{PaymentID: id}, nil
}

// fakeRestock is the pos-declared StockRestocker seam.
type fakeRestock struct {
	calls []RestockInput
	err   error
}

func (r *fakeRestock) RestockForReturnTx(_ context.Context, _ pgx.Tx, in RestockInput) error {
	r.calls = append(r.calls, in)
	return r.err
}

// fakeCardRefunder is RUN-PAY's reversal rail seam. The tx parameter is the
// CardRefunder seam's discipline: the rail rides the caller's Return
// transaction, this fake records the call and moves on.
type fakeCardRefunder struct {
	refundRef string
	err       error
	called    bool
	lastRef   string
	lastCents int64
}

func (c *fakeCardRefunder) RefundCard(_ context.Context, _ pgx.Tx, gatewayRef string, amountCents int64, _, _ string) (string, error) {
	c.called = true
	c.lastRef = gatewayRef
	c.lastCents = amountCents
	if c.err != nil {
		return "", c.err
	}
	return c.refundRef, nil
}

// fakeCredit is the identity credit gate.
type fakeCredit struct {
	tr         *[]string
	block      *identity.ActionBlocked
	lastAmount int64 // the gated exposure (deposit shrinks it to total − deposit)
}

func (c *fakeCredit) Check(_ context.Context, _ string, orderTotalCents, _, _ int64, _ bool, _ string, _ bool) *identity.ActionBlocked {
	if c.tr != nil {
		*c.tr = append(*c.tr, "credit")
	}
	c.lastAmount = orderTotalCents
	return c.block
}

// fakeRecorder is the audit seam.
type fakeRecorder struct{ rows []audit.SensitiveAccess }

func (r *fakeRecorder) Record(_ context.Context, a audit.SensitiveAccess) error {
	r.rows = append(r.rows, a)
	return nil
}

func (r *fakeRecorder) has(action string) bool {
	for _, x := range r.rows {
		if x.Action == action {
			return true
		}
	}
	return false
}

// fakeVault is the AES-GCM Sealer (seals deterministically for CI).
type fakeVault struct{}

func (fakeVault) Encrypt(p []byte) ([]byte, int, error) {
	out := make([]byte, len(p))
	for i, b := range p {
		out[i] = b ^ 0x5a
	}
	return out, 1, nil
}
func (fakeVault) Decrypt(c []byte, _ int) ([]byte, error) {
	out := make([]byte, len(c))
	for i, b := range c {
		out[i] = b ^ 0x5a
	}
	return out, nil
}

// fakeSig is a stand-in SignatureProvider (proves the seam, U22).
type fakeSig struct{ ref string }

func (s fakeSig) Capture(_ context.Context, _, _, _ string) (string, error) { return s.ref, nil }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// -- harness -------------------------------------------------------------

type harness struct {
	svc      *Service
	store    *fakeStore
	resolver *fakeResolver
	tender   *fakeTender
	credit   *fakeCredit
	restock  *fakeRestock
	refunder *fakeCardRefunder
	rec      *fakeRecorder
	trace    []string
	caller   identity.StaffContext
}

func newHarness() *harness {
	h := &harness{}
	h.store = newFakeStore(&h.trace)
	h.resolver = &fakeResolver{tr: &h.trace, price: pricing.LinePrice{
		UnitPriceCents: 39934, PriceUOM: "SF", LineTotalCents: 10000, QtyBase: 100,
		Breakdown: pricing.Breakdown{FinalCents: 39934, PriceUOM: "SF", LineTotalCents: 10000},
	}}
	h.tender = &fakeTender{tr: &h.trace, paymentID: "pay-1", sessionBranch: "branch-trt"}
	h.credit = &fakeCredit{tr: &h.trace}
	h.restock = &fakeRestock{}
	h.refunder = &fakeCardRefunder{refundRef: "rp_rev_1"}
	h.rec = &fakeRecorder{}
	// DIB-389: Sale now resolves a guard-stamped caller. The harness default is a GLOBAL-scope staff
	// context, which reaches every branch, so every pre-existing test behaves exactly as before. Tests
	// that exercise branch scoping override h.caller with a branch-scoped context.
	h.caller = identity.StaffContext{UserID: "u-harness", RoleCode: "CSR", Tier: identity.TierManager, Scope: identity.ScopeGlobal}
	h.svc = &Service{
		store: h.store, pricing: h.resolver, tender: h.tender, credit: h.credit,
		restock: h.restock, cardRefund: h.refunder,
		audit: h.rec, now: func() time.Time { return time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC) },
		callerFrom: func(context.Context) (identity.StaffContext, bool) { return h.caller, true },
		// The gated paths fail closed without a configuration (the lead's
		// P1-1 ruling), so the harness wires the decided default through
		// the test config builder; a test that wants another configuration
		// or none calls WithRoleConfig after the harness.
		roles: testRoleConfig(),
	}
	return h
}

// testRoleConfig is the unit tests' config builder: the decided default,
// loaded once. A refused default is a build failure the roleconfig
// package's own tests would have caught first.
func testRoleConfig() *roleconfig.Config {
	cfg, err := roleconfig.Default()
	if err != nil {
		panic("test harness: the decided default does not load: " + err.Error())
	}
	return cfg
}

func baseInput() SaleInput {
	return SaleInput{
		IdempotencyKey: "11111111-1111-4111-8111-111111111111",
		BranchID:       "branch-trt",
		TillSessionID:  "till-1",
		CustomerID:     "cust-abc",
		SaleType:       SaleCollectNow,
		Tender:         TenderCash,
		Lines:          []LineInput{{ProductID: "prod-blu", QtyEntered: 100, UOMEntered: "SF"}},
	}
}

// -- U1, cash composes paid + invoiced + recorded --------------------------

func TestSale_Cash_ComposesPaidInvoiced(t *testing.T) {
	h := newHarness()
	out, err := h.svc.Sale(context.Background(), baseInput())
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	if out.Tender != TenderCash || out.Status != "COMMITTED" {
		t.Fatalf("want CASH/COMMITTED, got %s/%s", out.Tender, out.Status)
	}
	if len(h.store.orders) != 1 || h.store.orders[0].Status != "CONFIRMED" {
		t.Fatalf("order not CONFIRMED: %+v", h.store.orders)
	}
	if !h.tender.called || h.tender.last.Method != TenderCash {
		t.Fatalf("TakeTender(CASH) not called: %+v", h.tender)
	}
	if len(h.store.invoices) != 1 || h.store.invoices[0].Status != "PAID" {
		t.Fatalf("invoice not PAID: %+v", h.store.invoices)
	}
	if len(h.store.allocations) != 1 {
		t.Fatalf("want 1 allocation, got %d", len(h.store.allocations))
	}
	if len(h.store.custTxns) != 1 || h.store.custTxns[0].TxnType != "PAYMENT" {
		t.Fatalf("want PAYMENT txn: %+v", h.store.custTxns)
	}
	if out.PaymentID != "pay-1" {
		t.Fatalf("payment id not surfaced: %q", out.PaymentID)
	}
}

// -- DIB-197, a pricing failure on a CASH ring surfaces as a mapped 4xx, never a bare 500 --
//
// A walk-in CASH sale resolves RETAIL pricing; when a line's retail pricing is misconfigured (no
// retail anchor / no uom conversion factor) the engine returns its typed client error. Before the
// fix the POS composer passed that raw pricing sentinel straight up, so handler.writeErr fell
// through to its default HTTP 500 "internal error" and the whole counter sale failed. The composer
// must translate pricing's taxonomy into a pos clientError so the ring reports the SAME friendly
// 4xx the /pricing endpoints already return. This test rings the CASH path and would have caught
// the 500: before the fix ClientStatus(err) was ok=false here (unmapped → 500).
func TestSale_Cash_PricingFailure_IsClientErrorNot500(t *testing.T) {
	// A genuine pricing client error obtained through pricing's public surface, DB-free: a zero-qty
	// resolve is a 400 badRequestError, which pricing.ClientStatus classifies as a 4xx.
	_, pricingErr := pricing.NewService(nil).DeriveLinePrice(context.Background(), pricing.ResolveInput{ProductID: "p", UOMEntered: "EA"})
	if st, _, ok := pricing.ClientStatus(pricingErr); !ok || st < 400 || st >= 500 {
		t.Fatalf("precondition: want a pricing 4xx client error, got status=%d ok=%v err=%v", st, ok, pricingErr)
	}

	h := newHarness()
	h.resolver.err = pricingErr // every priced line now fails with the pricing client error

	_, err := h.svc.Sale(context.Background(), baseInput()) // baseInput() rings CASH
	if err == nil {
		t.Fatal("a failed line price must abort the CASH sale")
	}
	// The regression: handler.writeErr maps a pos clientError to its status; an UNMAPPED error is the
	// default HTTP 500. Before the fix ClientStatus(err) was ok=false here → 500 "internal error".
	st, ok := ClientStatus(err)
	if !ok {
		t.Fatalf("CASH pricing failure must surface as a mapped client error (was HTTP 500), got unmapped: %v", err)
	}
	if st < 400 || st >= 500 {
		t.Fatalf("CASH pricing failure must map to a 4xx, got %d (%v)", st, err)
	}
	// The line-scoped message reaches the cashier (not a bare "internal error").
	if !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("want a line-scoped message, got %q", err.Error())
	}
	// It aborts BEFORE any tx opens, nothing persisted, no tender taken.
	if len(h.store.orders) != 0 || h.tender.called {
		t.Fatalf("a below-pricing abort must open no tx: orders=%d tenderCalled=%v", len(h.store.orders), h.tender.called)
	}
}

// -- M4, PAYMENT_RECEIVED records post-commit through the till hook, exactly once --

func TestSale_RecordsPaymentReceived_PostCommit_OncePerCommittedTender(t *testing.T) {
	h := newHarness()
	if _, err := h.svc.Sale(context.Background(), baseInput()); err != nil {
		t.Fatalf("sale: %v", err)
	}
	if len(h.tender.receiptRecords) != 1 || h.tender.receiptRecords[0].PaymentID != "pay-1" {
		t.Fatalf("a committed tendered sale must invoke the post-commit receipt hook once: %+v", h.tender.receiptRecords)
	}
	// The hook fires AFTER the money tx (trace: tender inside the tx, record-received after commit).
	assertOrder(t, h.trace, "tender", "invoice", "alloc", "record-received")

	// A replayed sale never re-records.
	h2 := newHarness()
	in := baseInput()
	h2.store.cached[in.IdempotencyKey] = Sale{OrderID: "order-orig", Status: "COMMITTED"}
	if _, err := h2.svc.Sale(context.Background(), in); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(h2.tender.receiptRecords) != 0 {
		t.Fatal("a replayed sale must not invoke the receipt hook")
	}

	// A rolled-back sale (declined card) never records, no orphan receipt.
	h3 := newHarness()
	h3.tender.err = fmt.Errorf("%w: gateway refused", till.ErrCardDeclined)
	bad := baseInput()
	bad.Tender = TenderCard
	if _, err := h3.svc.Sale(context.Background(), bad); err == nil {
		t.Fatal("want the decline")
	}
	if len(h3.tender.receiptRecords) != 0 {
		t.Fatal("a rolled-back sale must not invoke the receipt hook")
	}

	// An ACCOUNT sale (no tender) records nothing.
	h4 := newHarness()
	h4.store.customers["cust-abc"] = customerRec{ID: "cust-abc", CustomerType: "CONTRACTOR", CreditLimitCents: 5000000}
	acct := baseInput()
	acct.Tender = TenderAccount
	if _, err := h4.svc.Sale(context.Background(), acct); err != nil {
		t.Fatalf("account sale: %v", err)
	}
	if len(h4.tender.receiptRecords) != 0 {
		t.Fatal("an untendered sale must not invoke the receipt hook")
	}
}

// -- U2, account finalizes AR, no payment row ------------------------------

func TestSale_Account_FinalizedNoPayment(t *testing.T) {
	h := newHarness()
	h.store.customers["cust-abc"] = customerRec{ID: "cust-abc", CustomerType: "CONTRACTOR", PaymentTermsDays: 30, CreditLimitCents: 5000000}
	in := baseInput()
	in.Tender = TenderAccount
	out, err := h.svc.Sale(context.Background(), in)
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	if h.tender.called {
		t.Fatalf("TakeTender must NOT be called for ACCOUNT")
	}
	if len(h.store.invoices) != 1 || h.store.invoices[0].Status != "FINALIZED" {
		t.Fatalf("invoice not FINALIZED: %+v", h.store.invoices)
	}
	if len(h.store.allocations) != 0 {
		t.Fatalf("no allocation for ACCOUNT, got %d", len(h.store.allocations))
	}
	if len(h.store.custTxns) != 1 || h.store.custTxns[0].TxnType != "INVOICE" {
		t.Fatalf("want INVOICE txn: %+v", h.store.custTxns)
	}
	if out.PaymentID != "" {
		t.Fatalf("ACCOUNT must have no payment id, got %q", out.PaymentID)
	}
	if h.store.custTxns[0].BalanceAfter != out.TotalCents {
		t.Fatalf("AR balance not incremented: %d vs %d", h.store.custTxns[0].BalanceAfter, out.TotalCents)
	}
}

// -- U3, account over limit blocked, no commit -----------------------------

func TestSale_Account_OverLimit_BlockedNoCommit(t *testing.T) {
	h := newHarness()
	h.credit.block = &identity.ActionBlocked{Policy: identity.PolicyCredit}
	in := baseInput()
	in.Tender = TenderAccount
	_, err := h.svc.Sale(context.Background(), in)
	var cb ErrCreditBlocked
	if !errors.As(err, &cb) || cb.Policy != string(identity.PolicyCredit) {
		t.Fatalf("want ErrCreditBlocked(CREDIT_LIMIT_EXCEEDED), got %v", err)
	}
	if len(h.store.orders) != 0 || len(h.store.invoices) != 0 {
		t.Fatalf("nothing must commit on a blocked credit: %+v %+v", h.store.orders, h.store.invoices)
	}
}

// -- U4, no credit → COD constraint ----------------------------------------

func TestSale_Account_NoCredit_CODConstraint(t *testing.T) {
	h := newHarness()
	h.credit.block = &identity.ActionBlocked{Policy: identity.PolicyCOD}
	in := baseInput()
	in.Tender = TenderAccount
	_, err := h.svc.Sale(context.Background(), in)
	var cb ErrCreditBlocked
	if !errors.As(err, &cb) || cb.Policy != string(identity.PolicyCOD) {
		t.Fatalf("want ErrCreditBlocked(COD_CONSTRAINT), got %v", err)
	}
}

// -- U5, card captures, payment surfaced, invoice PAID ---------------------

func TestSale_Card_StubCaptures_GatewayRefPersisted(t *testing.T) {
	h := newHarness()
	h.tender.gatewayRef = "rp_sim_card_42" // the till returns the processor ref for a CARD capture
	in := baseInput()
	in.Tender = TenderCard
	in.PaymentToken = "tok_runner"
	out, err := h.svc.Sale(context.Background(), in)
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	if !h.tender.called || h.tender.last.Method != TenderCard || h.tender.last.PaymentToken != "tok_runner" {
		t.Fatalf("card tender not threaded: %+v", h.tender.last)
	}
	if out.PaymentID != "pay-1" {
		t.Fatalf("card payment not surfaced: %q", out.PaymentID)
	}
	// The gateway_ref from the till result MUST surface on the Sale response (the bug was that it never
	// did, gatewayRef was declared but never assigned, so the response + event carried "" for card).
	if out.GatewayRef != "rp_sim_card_42" {
		t.Fatalf("card gateway_ref not surfaced to the Sale response: got %q want %q", out.GatewayRef, "rp_sim_card_42")
	}
	if h.store.invoices[0].Status != "PAID" {
		t.Fatalf("card invoice not PAID")
	}
	// The payment.received event must carry the SAME gateway_ref (not empty).
	ev, ok := h.store.lastEvent("payment.received")
	if !ok {
		t.Fatalf("no payment.received event emitted")
	}
	if ev["gateway_ref"] != "rp_sim_card_42" {
		t.Fatalf("payment.received event gateway_ref = %v, want %q", ev["gateway_ref"], "rp_sim_card_42")
	}
}

// -- U6, declined card rolls EVERYTHING back -------------------------------

func TestSale_Card_Declined_RollsBackEverything(t *testing.T) {
	h := newHarness()
	// The decline arrives at the CAPTURE, before the transaction opens; the ring answers the
	// decline word and persists nothing (no order, no invoice, no pos_sale, no tender taken).
	h.tender.captureErr = fmt.Errorf("%w: gateway refused", till.ErrCardDeclined)
	in := baseInput()
	in.Tender = TenderCard
	_, err := h.svc.Sale(context.Background(), in)
	if !errors.Is(err, ErrCardChargeCardDeclined) {
		t.Fatalf("want ErrCardChargeCardDeclined, got %v", err)
	}
	if len(h.store.orders) != 0 || len(h.store.invoices) != 0 || len(h.store.posSales) != 0 {
		t.Fatalf("declined card must persist NOTHING: o=%d i=%d p=%d",
			len(h.store.orders), len(h.store.invoices), len(h.store.posSales))
	}
	if len(h.tender.takes) != 0 {
		t.Fatalf("a declined capture must take no tender at all; takes=%d", len(h.tender.takes))
	}
}

// A NON-decline till failure (session not open, branch access, conflict) must NOT read as "card
// declined", that mislabel sent cashiers chasing the card when the drawer was the problem.
func TestSale_Card_TillFailure_IsNotADecline(t *testing.T) {
	h := newHarness()
	h.tender.err = errors.New("till: session sess-1 is not OPEN")
	in := baseInput()
	in.Tender = TenderCard
	_, err := h.svc.Sale(context.Background(), in)
	if err == nil {
		t.Fatal("want an error")
	}
	if errors.Is(err, errCardDeclined) {
		t.Fatalf("a till failure must not surface as a decline, got %v", err)
	}
	if len(h.store.orders) != 0 || len(h.store.invoices) != 0 || len(h.store.posSales) != 0 {
		t.Fatalf("failed tender must persist NOTHING: o=%d i=%d p=%d",
			len(h.store.orders), len(h.store.invoices), len(h.store.posSales))
	}
}

// -- U7, line price comes from the resolver, never the body ----------------

func TestSale_LinePriced_FromResolver_NotBody(t *testing.T) {
	h := newHarness()
	_, err := h.svc.Sale(context.Background(), baseInput())
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	if h.store.orderLines[0].UnitPriceCents != 39934 || h.store.orderLines[0].LineTotalCents != 10000 {
		t.Fatalf("line price not from resolver: %+v", h.store.orderLines[0])
	}
}

// -- U8, a line below the floor rings at the rule price, no block (D48 row 481, D49) --------

func TestSale_BelowFloorLine_RingsAtRulePrice_NoBlock(t *testing.T) {
	h := newHarness()
	// blocked/policy are the GATED resolve's verdict on the same line, the floor the quote flow
	// meets. The till prices through the counter resolve and never asks that question.
	h.resolver.blocked = true
	h.resolver.policy = string(identity.PolicyMargin)
	out, err := h.svc.Sale(context.Background(), baseInput())
	if err != nil {
		t.Fatalf("the till rings a below-floor line unblocked, got %v", err)
	}
	if out.Status != "COMMITTED" || len(h.store.orders) != 1 {
		t.Fatalf("the ruled line commits: %+v orders=%d", out, len(h.store.orders))
	}
	if h.store.orderLines[0].UnitPriceCents != 39934 {
		t.Fatalf("the line rings at the resolver's rule price, got %+v", h.store.orderLines[0])
	}
}

// A ring body still carrying a stray cost_cents key is TAKEN: the till's line has no such field,
// the lenient decoder drops the key, and the sale rings through the counter resolve at the rule
// price, with or without the key (D48 row 481, D49). Driven through the real handler decode path,
// not through a struct literal, so the wire shape is what is under test.
func TestRing_BodyCarryingStrayCostCents_IsTaken(t *testing.T) {
	h := newHarness()
	body := `{"idempotency_key":"11111111-1111-4111-8111-111111111111","branch_id":"branch-trt",` +
		`"till_session_id":"till-1","customer_id":"cust-abc","sale_type":"COLLECT_NOW","tender":"CASH",` +
		`"cost_cents":999999,` +
		`"lines":[{"product_id":"prod-blu","qty_entered":100,"uom_entered":"SF","cost_cents":20000}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pos/sales", strings.NewReader(body))
	rec := httptest.NewRecorder()
	caller := guards.StaffCaller{
		StaffScope: authz.StaffScope{PlatformUserID: "u-harness", OrgID: "org-harness"},
		StaffAuthority: iauth.StaffAuthority{
			RoleID: "inside_sales", Tier: iauth.TierFrontline, Scope: iauth.ScopeBranch,
			Branches: []string{"branch-trt"},
		},
	}
	hd := NewHandler(h.svc, nil)
	hd.staff(hd.createSale)(rec, req, caller)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a ring body carrying a stray cost_cents key is taken: %d %s", rec.Code, rec.Body.String())
	}
	if len(h.store.orderLines) != 1 || h.store.orderLines[0].UnitPriceCents != 39934 {
		t.Fatalf("the stray key must price nothing: the line rings at the rule price, got %+v", h.store.orderLines)
	}
}

// -- U9/U10/U11, idempotency -----------------------------------------------

func TestSale_Idempotency_ReplayReturnsOriginal(t *testing.T) {
	h := newHarness()
	in := baseInput()
	h.store.cached[in.IdempotencyKey] = Sale{OrderID: "order-orig", OrderNo: "#SO-1042", Status: "COMMITTED", Tender: TenderCash}
	out, err := h.svc.Sale(context.Background(), in)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !out.Replayed || out.OrderID != "order-orig" {
		t.Fatalf("want cached replay, got %+v", out)
	}
	if len(h.store.orders) != 0 {
		t.Fatalf("replay must not create a second order")
	}
}

func TestSale_Idempotency_MissingKey_400(t *testing.T) {
	h := newHarness()
	in := baseInput()
	in.IdempotencyKey = ""
	_, err := h.svc.Sale(context.Background(), in)
	if !errors.Is(err, errMissingKey) {
		t.Fatalf("want errMissingKey, got %v", err)
	}
}

func TestSale_Idempotency_NewKey_NewSale(t *testing.T) {
	h := newHarness()
	in := baseInput()
	in.IdempotencyKey = "22222222-2222-4222-8222-222222222222"
	out, err := h.svc.Sale(context.Background(), in)
	if err != nil || out.Status != "COMMITTED" || len(h.store.orders) != 1 {
		t.Fatalf("fresh key must create a sale: %v %+v", err, out)
	}
}

// -- U12, empty customer resolves the house WALK-IN ------------------------

func TestSale_CustomerEmpty_ResolvesWalkIn(t *testing.T) {
	h := newHarness()
	in := baseInput()
	in.CustomerID = ""
	_, err := h.svc.Sale(context.Background(), in)
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	if h.store.orders[0].CustomerID != "walkin-house" {
		t.Fatalf("walk-in not resolved: %q", h.store.orders[0].CustomerID)
	}
}

// -- U13, provisional flat-13% tax -----------------------------------------

func TestSale_TaxFlat13_Provisional(t *testing.T) {
	h := newHarness()
	out, err := h.svc.Sale(context.Background(), baseInput())
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	if out.TaxCents != 1300 { // 13% of the 10000 subtotal
		t.Fatalf("want flat-13%% = 1300, got %d", out.TaxCents)
	}
	if out.TaxReasonCode != TaxStdON13 || out.TotalCents != 11300 {
		t.Fatalf("tax reason/total wrong: %s / %d", out.TaxReasonCode, out.TotalCents)
	}
}

// -- U14, capture-before-fulfil ordering -----------------------------------

func TestSale_CaptureBeforeFulfil_Ordering(t *testing.T) {
	h := newHarness()
	in := baseInput()
	in.Tender = TenderCard
	if _, err := h.svc.Sale(context.Background(), in); err != nil {
		t.Fatalf("sale: %v", err)
	}
	assertOrder(t, h.trace, "price", "order", "tender", "invoice", "alloc")
}

func assertOrder(t *testing.T, trace []string, want ...string) {
	t.Helper()
	idx := 0
	for _, ev := range trace {
		if idx < len(want) && ev == want[idx] {
			idx++
		}
	}
	if idx != len(want) {
		t.Fatalf("call order %v does not contain subsequence %v", trace, want)
	}
}

// -- U15, cash is first-class offline --------------------------------------

func TestSale_Cash_OfflineFirstClass(t *testing.T) {
	h := newHarness()
	in := baseInput()
	in.Offline = true
	out, err := h.svc.Sale(context.Background(), in)
	if err != nil {
		t.Fatalf("offline cash must commit: %v", err)
	}
	if !h.tender.last.Offline {
		t.Fatalf("offline flag not threaded to TakeTender")
	}
	if out.GatewayRef != "" {
		t.Fatalf("offline cash must have no gateway_ref")
	}
}

// -- U16, card offline blocked by default ----------------------------------

func TestSale_Card_OfflineBlockedByDefault(t *testing.T) {
	h := newHarness()
	in := baseInput()
	in.Tender = TenderCard
	in.Offline = true
	_, err := h.svc.Sale(context.Background(), in)
	if !errors.Is(err, errCardOffline) {
		t.Fatalf("want errCardOffline, got %v", err)
	}
	if len(h.store.orders) != 0 || h.tender.called {
		t.Fatalf("offline card must not open the sale")
	}
}

// -- U17, assess-relief pre-populates masked-only --------------------------

func TestAssessRelief_PrePopulatesMaskedOnly(t *testing.T) {
	h := newHarness()
	h.store.profiles["prof-1"] = eligibilityMasked{
		ProfileID: "prof-1", PurchaserType: "INDIVIDUAL", MaskedIdentifier: "•••• 4821", OntarioEligibility: "ON_RESIDENT",
	}
	out, err := h.svc.AssessRelief(context.Background(), ReliefAssessInput{
		ProfileID: "prof-1", DeclaredUse: "personal", Fulfilment: "pickup", SubtotalCents: 10000,
	})
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if out.MaskedIdentifier != "•••• 4821" || out.PurchaserType != "INDIVIDUAL" {
		t.Fatalf("masked pre-fill wrong: %+v", out)
	}
	// the outcome type carries NO cleartext identifier field (compile-time guarantee + reflect check)
	rt := reflect.TypeOf(out)
	for i := 0; i < rt.NumField(); i++ {
		n := strings.ToLower(rt.Field(i).Name)
		if strings.Contains(n, "identifier") && !strings.Contains(n, "masked") {
			t.Fatalf("ReliefOutcome leaks a non-masked identifier field: %s", rt.Field(i).Name)
		}
	}
}

// -- U18, pickup never yields 0% -------------------------------------------

func TestAssessRelief_PickupNeverZero(t *testing.T) {
	h := newHarness()
	h.store.profiles["prof-1"] = eligibilityMasked{ProfileID: "prof-1", OntarioEligibility: "ON_RESIDENT", MaskedIdentifier: "•••• 1"}
	out, _ := h.svc.AssessRelief(context.Background(), ReliefAssessInput{
		ProfileID: "prof-1", DeclaredUse: "personal", Fulfilment: "pickup", SubtotalCents: 10000,
	})
	if strings.HasPrefix(out.Outcome, "0%") {
		t.Fatalf("pickup must never offer 0%%, got %s", out.Outcome)
	}
	if out.Outcome != "5%" || out.ReasonCode != TaxONFNPos8 {
		t.Fatalf("eligible personal pickup → 5%% ON_FN_POS_8, got %s/%s", out.Outcome, out.ReasonCode)
	}
}

// -- U19, 0% needs delivery evidence (HARD GATE) ---------------------------

func TestAssessRelief_ZeroRequiresDeliveryEvidence_HardGate(t *testing.T) {
	h := newHarness()
	h.store.profiles["prof-1"] = eligibilityMasked{ProfileID: "prof-1", OntarioEligibility: "ON_RESIDENT", MaskedIdentifier: "•••• 1"}
	pending, _ := h.svc.AssessRelief(context.Background(), ReliefAssessInput{
		ProfileID: "prof-1", Fulfilment: "dibbits_truck", SubtotalCents: 10000, EvidenceAttached: false,
	})
	if !pending.RequiresEvidence || pending.RateBps != flatHSTRateBps || pending.NetCollectedCents != 1300 {
		t.Fatalf("no-evidence 0%% must fall back to binding 13%%: %+v", pending)
	}
	attached, _ := h.svc.AssessRelief(context.Background(), ReliefAssessInput{
		ProfileID: "prof-1", Fulfilment: "dibbits_truck", SubtotalCents: 10000, EvidenceAttached: true,
	})
	if attached.Outcome != "0%" || attached.RateBps != 0 || !attached.EvidenceSatisfied {
		t.Fatalf("with POD/waybill → binding 0%%: %+v", attached)
	}
}

// -- U20, records reason + snapshot ----------------------------------------

func TestAssessRelief_RecordsReasonAndSnapshot(t *testing.T) {
	h := newHarness()
	h.store.profiles["prof-1"] = eligibilityMasked{ProfileID: "prof-1", OntarioEligibility: "ON_RESIDENT", MaskedIdentifier: "•••• 1"}
	_, err := h.svc.AssessRelief(context.Background(), ReliefAssessInput{
		ProfileID: "prof-1", OrderID: "order-1", DeclaredUse: "personal", Fulfilment: "pickup", SubtotalCents: 10000,
	})
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if len(h.store.snapshots) != 1 {
		t.Fatalf("want 1 tax_decision_snapshot, got %d", len(h.store.snapshots))
	}
	s := h.store.snapshots[0]
	if s.Automated || s.TaxReasonCode != TaxONFNPos8 || s.OnCreditCents != 800 {
		t.Fatalf("snapshot fields wrong: %+v", s)
	}
}

// -- U21, simulated signature returns an encrypted, audited ref -------------

func TestSignature_Simulated_ReturnsEncryptedRef(t *testing.T) {
	store := newFakeStore(nil)
	rec := &fakeRecorder{}
	p := &SimulatedSignatureProvider{vault: fakeVault{}, store: store, audit: rec, now: time.Now}
	ref, err := p.Capture(context.Background(), "TAX_ATTESTATION", "order", "order-1")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if !strings.HasPrefix(ref, "sig_") {
		t.Fatalf("want a sig_ ref, got %q", ref)
	}
	if len(store.signatures) != 1 || len(store.signatures[0].SignatureEnc) == 0 || store.signatures[0].Provider != "SIMULATED" {
		t.Fatalf("signature not persisted encrypted: %+v", store.signatures)
	}
	if string(store.signatures[0].SignatureEnc) == "SIMULATED:TAX_ATTESTATION:"+ref {
		t.Fatalf("signature payload stored in cleartext")
	}
	if !rec.has(audit.ActSignatureCapture) {
		t.Fatalf("signature capture not audited")
	}
}

// -- U22, the provider is a seam -------------------------------------------

func TestSignature_ProviderIsSeam(t *testing.T) {
	h := newHarness()
	h.svc.sig = fakeSig{ref: "sig_substituted"}
	ref, err := h.svc.CaptureSignature(context.Background(), "ORDER_SIGNOFF", "order", "order-1")
	if err != nil || ref != "sig_substituted" {
		t.Fatalf("a substituted provider must work with no caller change: %v %q", err, ref)
	}
}

// -- U23, stock glance, both branches, read-only ---------------------------

func TestStockGlance_BothBranches_ReadOnly(t *testing.T) {
	h := newHarness()
	h.store.stock["prod-blu"] = map[string]float64{"branch-trt": 240, "branch-kgn": 80}
	g, err := h.svc.StockGlance(context.Background(), "prod-blu")
	if err != nil {
		t.Fatalf("glance: %v", err)
	}
	if g.OnHand["branch-trt"] != 240 || g.OnHand["branch-kgn"] != 80 {
		t.Fatalf("both branches not glanceable: %+v", g.OnHand)
	}
	if len(h.store.orders) != 0 || len(h.store.posSales) != 0 {
		t.Fatalf("glance must not write/decrement anything")
	}
}

// -- U24, operator reopen, no evidence leak --------------------------------

func TestGetSale_OperatorReopen_NoEvidenceLeak(t *testing.T) {
	h := newHarness()
	const saleID = "00000000-0000-4000-8000-000000000001" // real sale ids are uuids (GetSale now guards non-uuid)
	h.store.views[saleID] = SaleView{
		OrderID: saleID, OrderNo: "#SO-1", Tender: TenderCash, InvoiceNo: "#INV-1",
		Lines: []SaleViewLine{{LineNo: 1, ProductID: "prod-blu", UnitPriceCents: 39934}},
	}
	v, err := h.svc.GetSale(context.Background(), saleID)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if v.OrderNo != "#SO-1" || v.InvoiceNo != "#INV-1" || len(v.Lines) != 1 {
		t.Fatalf("reopen missing order/lines/invoice: %+v", v)
	}
	rt := reflect.TypeOf(v)
	for i := 0; i < rt.NumField(); i++ {
		n := strings.ToLower(rt.Field(i).Name)
		if strings.Contains(n, "identifier") || strings.Contains(n, "evidence") || strings.Contains(n, "enc") {
			t.Fatalf("SaleView leaks FN evidence field: %s", rt.Field(i).Name)
		}
	}
}

// GetSale must treat a malformed (non-uuid) id as not-found at the boundary, never reach the DB
// (no 22P02 → 500) and never leak existence (sweep #46 GetSaleView hardening).
func TestGetSale_NonUUID_NotFound(t *testing.T) {
	h := newHarness()
	if _, err := h.svc.GetSale(context.Background(), "not-a-uuid"); !errors.Is(err, errSaleNotFound) {
		t.Fatalf("non-uuid id: want errSaleNotFound, got %v", err)
	}
}

// -- U25, every FN-evidence access writes an audit row ---------------------

func TestRelief_EvidenceAccess_WritesAudit(t *testing.T) {
	h := newHarness()
	h.store.profiles["prof-1"] = eligibilityMasked{ProfileID: "prof-1", MaskedIdentifier: "•••• 9", OntarioEligibility: "ON_RESIDENT"}
	if _, err := h.svc.AssessRelief(context.Background(), ReliefAssessInput{
		ProfileID: "prof-1", DeclaredUse: "personal", Fulfilment: "pickup", SubtotalCents: 5000,
	}); err != nil {
		t.Fatalf("assess: %v", err)
	}
	if !h.rec.has(audit.ActTaxReliefAssess) {
		t.Fatalf("FN-evidence access not audited")
	}
}

// -- U26, no card-image upload field in the standard in-person flow ---------

func TestRelief_NoCardImageField(t *testing.T) {
	rt := reflect.TypeOf(ReliefAssessInput{})
	for i := 0; i < rt.NumField(); i++ {
		n := strings.ToLower(rt.Field(i).Name)
		if strings.Contains(n, "image") || strings.Contains(n, "scan") || strings.Contains(n, "upload") {
			t.Fatalf("assess-relief input must not carry a card-image field: %s", rt.Field(i).Name)
		}
	}
}

// -- U27, card_type captured + threaded (no surcharge at gate) --------------

func TestSale_Card_RecordsCardType(t *testing.T) {
	h := newHarness()
	in := baseInput()
	in.Tender = TenderCard
	in.CardType = "DEBIT"
	out, err := h.svc.Sale(context.Background(), in)
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	if h.tender.last.CardType != "DEBIT" {
		t.Fatalf("card_type not threaded to TakeTender: %q", h.tender.last.CardType)
	}
	// no surcharge math at gate, the total is subtotal + flat-13% only.
	if out.TotalCents != 11300 {
		t.Fatalf("a surcharge leaked into the total: %d", out.TotalCents)
	}
}

// -- extra, bad tender + no lines guards -----------------------------------

func TestSale_BadTender_Rejected(t *testing.T) {
	h := newHarness()
	in := baseInput()
	in.Tender = "BITCOIN"
	if _, err := h.svc.Sale(context.Background(), in); !errors.Is(err, errBadTender) {
		t.Fatalf("want errBadTender, got %v", err)
	}
}
