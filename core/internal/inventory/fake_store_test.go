package inventory

// fake_store_test.go, a DB-free in-memory `store` for the unit suite (mirrors gl's store_fake_test.go). It
// lets the receiving, decrement, cost-basis, and posting logic run without Postgres. Tx is ignored (there is
// no real transaction to join). Keys are "product|branch" for branch-grain state.

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

type fakeEvent struct {
	Topic   string
	Payload []byte
}

type fakeStore struct {
	hdr          poHeader
	lines        []poLine
	cost         map[string]int64   // product|branch → weighted-avg cents
	onhand       map[string]float64 // product|branch → on-hand
	productCost  map[string]int64   // product → product-level weighted-avg
	products     map[string]bool    // product → is_active (absent = no catalog row)
	moves        []moveParams
	events       []fakeEvent
	poStatus     string
	lineReceived map[string]float64
	receiptSeq   int
	lockedBefore map[string]int // product|branch → count of LockBranchCost calls (asserts lock precedes RMW)
	locks        int            // total LockBranchCost calls across all keys

	// DIB-60 reservation state. reserved is the qty_reserved projection keyed product|branch (the
	// fake is branch-grain throughout, and every reservation this module writes uses location=branch).
	reserved     map[string]float64
	reservations []Reservation
	resSeq       int
	policies     map[string]AllocationPolicy // branch → configured policy (absent = system default)

	// clock is the fake's ONE clock: the fake has no database and no transaction to read one from,
	// so MovementInstant and ExpireDueReservations both read this field and never two different
	// clocks. newFakeStore defaults it to time.Now; a test that wants a pinned instant sets the
	// field to a func of its own.
	clock func() time.Time
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		cost:         map[string]int64{},
		onhand:       map[string]float64{},
		productCost:  map[string]int64{},
		products:     map[string]bool{},
		lineReceived: map[string]float64{},
		reserved:     map[string]float64{},
		policies:     map[string]AllocationPolicy{},
		clock:        time.Now,
	}
}

func key(a, b string) string { return a + "|" + b }

func (f *fakeStore) MovementInstant(_ context.Context, _ pgx.Tx) (time.Time, error) {
	return f.clock(), nil
}

func (f *fakeStore) POHeaderForReceipt(_ context.Context, _ pgx.Tx, _ string) (poHeader, error) {
	return f.hdr, nil
}
func (f *fakeStore) POLinesForReceipt(_ context.Context, _ pgx.Tx, _ string) ([]poLine, error) {
	return f.lines, nil
}
func (f *fakeStore) InsertReceipt(_ context.Context, _ pgx.Tx, _, _ string) (string, error) {
	f.receiptSeq++
	return "rcpt-" + itoa(f.receiptSeq), nil
}
func (f *fakeStore) UpdatePOLineReceived(_ context.Context, _ pgx.Tx, lineID string, received float64) error {
	f.lineReceived[lineID] = received
	return nil
}
func (f *fakeStore) UpdatePOStatus(_ context.Context, _ pgx.Tx, _, status string) error {
	f.poStatus = status
	return nil
}
func (f *fakeStore) EmitEvent(_ context.Context, _ pgx.Tx, topic string, payload any) error {
	b, _ := json.Marshal(payload)
	f.events = append(f.events, fakeEvent{Topic: topic, Payload: b})
	return nil
}
func (f *fakeStore) InsertMove(_ context.Context, _ pgx.Tx, m moveParams) (string, error) {
	f.moves = append(f.moves, m)
	return "move-" + itoa(len(f.moves)), nil
}
func (f *fakeStore) HasShipMovesForOrderTx(_ context.Context, _ pgx.Tx, orderID string) (bool, error) {
	for _, m := range f.moves {
		if m.MoveType == MoveShip && m.SourceRefID == orderID {
			return true, nil
		}
	}
	return false, nil
}
func (f *fakeStore) OnHandQtyBranch(_ context.Context, _ pgx.Tx, productID, branchID string) (float64, error) {
	return f.onhand[key(productID, branchID)], nil
}
func (f *fakeStore) BalanceRows(_ context.Context, _ pgx.Tx, productID, branchID string) ([]BalanceRow, error) {
	return []BalanceRow{{LocationID: branchID, OnHandBase: f.onhand[key(productID, branchID)]}}, nil
}
func (f *fakeStore) UpsertBalanceDelta(_ context.Context, _ pgx.Tx, productID, _, branchID string, delta float64) error {
	f.onhand[key(productID, branchID)] += delta
	return nil
}
func (f *fakeStore) LockBranchCost(_ context.Context, _ pgx.Tx, productID, branchID string) error {
	if f.lockedBefore == nil {
		f.lockedBefore = map[string]int{}
	}
	f.lockedBefore[key(productID, branchID)]++
	f.locks++
	return nil
}
func (f *fakeStore) GetBranchCost(_ context.Context, _ pgx.Tx, productID, branchID string) (int64, bool, error) {
	c, ok := f.cost[key(productID, branchID)]
	return c, ok, nil
}
func (f *fakeStore) UpsertBranchCost(_ context.Context, _ pgx.Tx, productID, branchID string, avgCents int64) error {
	f.cost[key(productID, branchID)] = avgCents
	return nil
}
func (f *fakeStore) FallbackCost(_ context.Context, _ pgx.Tx, productID, _ string) (int64, string, bool, error) {
	if c, ok := f.productCost[productID]; ok && c > 0 {
		return c, "LAST_KNOWN_COST", true, nil
	}
	return 0, "", false, nil
}
func (f *fakeStore) UntruedProvisionalShipMoves(_ context.Context, _ pgx.Tx, _, _ string) ([]PreReceiptException, error) {
	return nil, nil
}
func (f *fakeStore) PreReceiptExceptions(_ context.Context, _ string) ([]PreReceiptException, error) {
	return nil, nil
}
func (f *fakeStore) AvailabilityFacts(_ context.Context, productID, branchID string) (AvailabilityFacts, error) {
	active, found := f.products[productID]
	sumFor := func(m map[string]float64) float64 {
		if branchID != "" {
			return m[key(productID, branchID)]
		}
		total := 0.0
		for k, v := range m {
			if len(k) > len(productID) && k[:len(productID)+1] == productID+"|" {
				total += v
			}
		}
		return total
	}
	return AvailabilityFacts{
		Found: found, IsActive: active,
		OnHandBase:   sumFor(f.onhand),
		ReservedBase: sumFor(f.reserved),
	}, nil
}

// - DIB-60 reservation seam -

func (f *fakeStore) InsertReservation(_ context.Context, _ pgx.Tx, in ReserveInput, holdMode string, expires *time.Time) (Reservation, error) {
	f.resSeq++
	res := Reservation{
		ID: "res-" + itoa(f.resSeq), ProductID: in.ProductID, BranchID: in.BranchID,
		LocationID: in.LocationID, ResType: in.ResType, HoldMode: holdMode, QtyBase: in.QtyBase,
		Status: ResActive, SourceType: in.SourceType, SourceRefID: in.SourceRefID, Reason: in.Reason,
	}
	if expires != nil {
		res.ExpiresAt = expires.Format(time.RFC3339)
	}
	f.reservations = append(f.reservations, res)
	return res, nil
}

func (f *fakeStore) AdjustReserved(_ context.Context, _ pgx.Tx, productID, _, branchID string, delta float64) error {
	k := key(productID, branchID)
	f.reserved[k] += delta
	if f.reserved[k] < 0 {
		f.reserved[k] = 0 // mirrors the GREATEST(...,0) clamp in the real repository
	}
	return nil
}

func (f *fakeStore) BalanceFactsTx(_ context.Context, _ pgx.Tx, productID, branchID string) (float64, float64, error) {
	return f.onhand[key(productID, branchID)], f.reserved[key(productID, branchID)], nil
}

// closeMatching is the fake's shared close: flip the matched ACTIVE rows and unwind the HARD ones,
// exactly as the real repository's single CTE statement does.
func (f *fakeStore) closeMatching(status string, match func(Reservation) bool) (int, error) {
	n := 0
	for i := range f.reservations {
		r := &f.reservations[i]
		if r.Status != ResActive || !match(*r) {
			continue
		}
		r.Status = status
		if HoldsStock(r.HoldMode) {
			_ = f.AdjustReserved(context.Background(), nil, r.ProductID, r.LocationID, r.BranchID, -r.QtyBase)
		}
		n++
	}
	return n, nil
}

func (f *fakeStore) CloseReservationsBySource(_ context.Context, _ pgx.Tx, sourceType, sourceRefID, status string) (int, error) {
	return f.closeMatching(status, func(r Reservation) bool {
		return (sourceType == "" || r.SourceType == sourceType) && r.SourceRefID == sourceRefID
	})
}

// LockHardHoldBalancesTx is a lock pass: nothing to lock in memory, the rows are already the
// fake's own.
func (f *fakeStore) LockHardHoldBalancesTx(_ context.Context, _ pgx.Tx, _, _ string) error {
	return nil
}

func (f *fakeStore) CloseReservationByID(_ context.Context, _ pgx.Tx, reservationID, status string) (int, error) {
	return f.closeMatching(status, func(r Reservation) bool { return r.ID == reservationID })
}

func (f *fakeStore) ExpireDueReservations(_ context.Context, _ pgx.Tx) (int, error) {
	now := f.clock()
	return f.closeMatching(ResExpired, func(r Reservation) bool {
		if r.ExpiresAt == "" {
			return false
		}
		t, err := time.Parse(time.RFC3339, r.ExpiresAt)
		return err == nil && !t.After(now)
	})
}
func (f *fakeStore) ProductCostCents(_ context.Context, productID string) (int64, bool, error) {
	c, ok := f.productCost[productID]
	return c, ok, nil
}
func (f *fakeStore) BranchCostCents(_ context.Context, productID, branchID string) (int64, bool, error) {
	c, ok := f.cost[key(productID, branchID)]
	return c, ok, nil
}
func (f *fakeStore) OnHandFor(context.Context, string) ([]OnHand, error) { return nil, nil }
func (f *fakeStore) RecentMoves(context.Context, string, string, int) ([]StockMove, error) {
	return nil, nil
}
func (f *fakeStore) PendingFulfilments(context.Context, string) ([]PendingFulfilment, error) {
	return nil, nil
}
func (f *fakeStore) SnapshotOnHand(context.Context, pgx.Tx, string) ([]CountSnapshot, error) {
	return nil, nil
}
func (f *fakeStore) StockValueCents(context.Context, pgx.Tx, string) (int64, error) {
	return 0, nil
}
func (f *fakeStore) StockValueAsOfCents(context.Context, pgx.Tx, time.Time, string) (int64, error) {
	return 0, nil
}
func (f *fakeStore) ReceivedLinesForPO(context.Context, string) ([]ReceivedLine, error) {
	return nil, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// - GL + rule fakes for the posting-consumer tests -

type fakeGLPoster struct {
	journals []GLJournal
	err      error
}

func (p *fakeGLPoster) PostJournal(_ context.Context, in GLJournal) (string, error) {
	if p.err != nil {
		return "", p.err
	}
	p.journals = append(p.journals, in)
	return "jr-" + itoa(len(p.journals)), nil
}

// fakeRules resolves posting-rule roles from an in-memory map keyed "event|role".
type fakeRules struct{ m map[string]string }

func (r fakeRules) ResolveRule(_ context.Context, eventType, lineRole, _ string) (string, error) {
	if code, ok := r.m[eventType+"|"+lineRole]; ok {
		return code, nil
	}
	return "", errNotFound
}

func defaultRules() fakeRules {
	return fakeRules{m: map[string]string{
		"po.received|inventory":        "1200",
		"po.received|ap_accrual":       "2150",
		"inventory.adjusted|inventory": "1200",
		"inventory.adjusted|expense":   "5200",
	}}
}

// AllocationPolicy, the fake defaults every branch to the SYSTEM default (found=false) unless a
// test seeds policies, so a unit test only has to think about policy when policy is what it is testing.
func (f *fakeStore) AllocationPolicy(_ context.Context, _ pgx.Tx, branchID string) (AllocationPolicy, bool, error) {
	p, ok := f.policies[branchID]
	return p, ok, nil
}

func (f *fakeStore) LockBalances(_ context.Context, _ pgx.Tx, _ string, _ []string) error { return nil }
