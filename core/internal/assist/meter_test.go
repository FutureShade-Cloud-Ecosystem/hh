package assist

// meter_test.go, slice 2 sub item 1: reservation before the provider call, settlement after, and
// the refusal when the reservation cannot be written.
//
// NO DATABASE. memSpend is the in-memory SpendStore; it applies the same fold the SQL does
// (FoldCalls), so a test that passes here is a claim about the rule, and the Postgres side is
// held to the same rule by the integration leg that needs the migration.

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// memSpend is an append-only SpendStore.
type memSpend struct {
	mu           sync.Mutex
	rows         []SpendRow
	failReserve  error
	failSettle   error
	settleTries  int
	onReserve    func(SpendRow)
	reserveCalls int
}

func (m *memSpend) Reserve(_ context.Context, r SpendRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reserveCalls++
	if m.failReserve != nil {
		return m.failReserve
	}
	if err := r.Validate(); err != nil {
		return err
	}
	m.rows = append(m.rows, r)
	if m.onReserve != nil {
		m.onReserve(r)
	}
	return nil
}

func (m *memSpend) ReserveWithin(_ context.Context, r SpendRow, lim ReserveLimits) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reserveCalls++
	if m.failReserve != nil {
		return m.failReserve
	}
	var tok, cost int64
	for _, f := range FoldCalls(m.inWindow(lim.From, lim.To)) {
		cost += f.CostMicroCents
		if f.StaffUserID == r.StaffUserID {
			tok += int64(f.TokensIn + f.TokensOut)
		}
	}
	if r.StaffUserID != "" && tok >= int64(lim.BudgetTokens) {
		return ErrOverBudget
	}
	if lim.CheckDealer && (cost >= lim.DealerCapMicroCents || cost+r.CostMicroCents > lim.DealerCapMicroCents) {
		return ErrDealerCap
	}
	if err := r.Validate(); err != nil {
		return err
	}
	m.rows = append(m.rows, r)
	if m.onReserve != nil {
		m.onReserve(r)
	}
	return nil
}

func (m *memSpend) Settle(_ context.Context, r SpendRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settleTries++
	if m.failSettle != nil {
		return m.failSettle
	}
	if err := r.Validate(); err != nil {
		return err
	}
	m.rows = append(m.rows, r)
	return nil
}

func (m *memSpend) Void(_ context.Context, r SpendRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := r.Validate(); err != nil {
		return err
	}
	m.rows = append(m.rows, r)
	return nil
}

func (m *memSpend) inWindow(from, to time.Time) []SpendRow {
	var in []SpendRow
	for _, r := range m.rows {
		if !r.At.Before(from) && r.At.Before(to) {
			in = append(in, r)
		}
	}
	return in
}

func (m *memSpend) UserMonth(_ context.Context, userID string, from, to time.Time) (Spend, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var s Spend
	seen := map[string]bool{}
	for _, r := range FoldCalls(m.inWindow(from, to)) {
		if r.StaffUserID != userID {
			continue
		}
		if !seen[r.ExchangeID] {
			seen[r.ExchangeID] = true
			s.Exchanges++
		}
		s.TokensIn += r.TokensIn
		s.TokensOut += r.TokensOut
	}
	return s, nil
}

func (m *memSpend) UsersMonth(ctx context.Context, from, to time.Time) (map[string]Spend, error) {
	out := map[string]Spend{}
	m.mu.Lock()
	users := map[string]bool{}
	for _, r := range m.rows {
		users[r.StaffUserID] = true
	}
	m.mu.Unlock()
	for u := range users {
		if u == "" {
			continue
		}
		if s, _ := m.UserMonth(ctx, u, from, to); s.Exchanges > 0 {
			out[u] = s
		}
	}
	return out, nil
}

func (m *memSpend) DealerMonthMicroCents(_ context.Context, from, to time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var total int64
	for _, r := range FoldCalls(m.inWindow(from, to)) {
		total += r.CostMicroCents
	}
	return total, nil
}

func (m *memSpend) Unsettled(_ context.Context, reservedBefore time.Time) ([]SpendRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	closed := map[string]bool{}
	for _, r := range m.rows {
		if r.Kind != SpendReservation {
			closed[callKey(r)] = true
		}
	}
	var out []SpendRow
	for _, r := range m.rows {
		if r.Kind == SpendReservation && !closed[callKey(r)] && r.At.Before(reservedBefore) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

func (m *memSpend) Settled(_ context.Context, from, to time.Time) ([]SpendRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []SpendRow
	for _, r := range m.inWindow(from, to) {
		if r.Kind == SpendSettlement {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memSpend) kinds() []SpendKind {
	m.mu.Lock()
	defer m.mu.Unlock()
	var k []SpendKind
	for _, r := range m.rows {
		k = append(k, r.Kind)
	}
	return k
}

// memPrices is a PriceStore over a fixed table.
type memPrices map[string]ModelPrice

func (p memPrices) PriceFor(_ context.Context, alias, model string) (ModelPrice, bool, error) {
	pr, ok := p[alias+"/"+model]
	return pr, ok, nil
}

// pricedProvider is a paid-looking provider that counts the requests it receives, so "refused before
// the provider is reached" is a number, not an opinion.
type pricedProvider struct {
	reply Reply
	err   error
	calls int
	// during runs inside Complete, to look at the world while the call is in flight.
	during func()
}

func (p *pricedProvider) Name() string  { return "anthropic" }
func (p *pricedProvider) Model() string { return "claude-test-1" }
func (p *pricedProvider) Complete(_ context.Context, _ CompletionRequest) (Reply, error) {
	p.calls++
	if p.during != nil {
		p.during()
	}
	return p.reply, p.err
}

var testPrice = ModelPrice{
	ID: "price-1", Alias: "anthropic", Model: "claude-test-1",
	InMicroPerM: 3_000_000_00, OutMicroPerM: 15_000_000_00, CachedMicroPerM: 300_000_00, Seq: 1,
}

func testPrices() memPrices { return memPrices{"anthropic/claude-test-1": testPrice} }

// meteredService is a service over a paid-looking provider with metering wired and a dealer cap
// large enough not to be the thing under test.
func meteredService(p Provider, sp *memSpend) *Service {
	return NewService(NewRegistry(seededReaders().deps(), nil), p, &fakeLedger{}, newFakeLimits(), StaticSettings{}, nil).
		WithClock(func() time.Time { return at(11) }).
		WithMetering(sp, testPrices()).
		WithDealerCap(1 << 60)
}

func TestComplete_ReservesBeforeTheProviderAndSettlesAfter(t *testing.T) {
	sp := &memSpend{}
	var during []SpendKind
	p := &pricedProvider{reply: Reply{Text: "ok", TokensIn: 1000, TokensOut: 200, TokensCached: 50}}
	p.during = func() { during = sp.kinds() }
	svc := meteredService(p, sp)

	if _, err := svc.complete(context.Background(), manager(), CompletionRequest{Messages: []Message{{Role: RoleUser, Text: "hi"}}}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if len(during) != 1 || during[0] != SpendReservation {
		t.Fatalf("while the provider ran the table held %v, want exactly one reservation", during)
	}
	got := sp.kinds()
	if len(got) != 2 || got[1] != SpendSettlement {
		t.Fatalf("after the call the table holds %v, want reservation then settlement", got)
	}
	set := sp.rows[1]
	if set.TokensIn != 1000 || set.TokensOut != 200 || set.TokensCached != 50 {
		t.Fatalf("settlement carries %d/%d/%d, want the provider's reported 1000/200/50", set.TokensIn, set.TokensOut, set.TokensCached)
	}
	if set.PriceID != "price-1" || set.Alias != "anthropic" || set.Model != "claude-test-1" {
		t.Fatalf("settlement names price %q alias %q model %q", set.PriceID, set.Alias, set.Model)
	}
	if set.StaffUserID != "u-sm" || set.RoleCode != "SALES_MANAGER" || set.Audience != AudienceStaff {
		t.Fatalf("settlement is not stamped with the asker: %+v", set)
	}
	if sp.rows[0].ExchangeID == "" || sp.rows[0].ExchangeID != set.ExchangeID || sp.rows[0].CallNo != set.CallNo {
		t.Fatalf("reservation and settlement do not share an exchange and call: %+v %+v", sp.rows[0], set)
	}
}

func TestComplete_ReservationWriteFailureRefusesTheCall(t *testing.T) {
	sp := &memSpend{failReserve: errors.New("disk full")}
	p := &pricedProvider{reply: Reply{Text: "ok"}}
	svc := meteredService(p, sp)

	_, err := svc.complete(context.Background(), manager(), CompletionRequest{})
	wantErrIs(t, err, ErrMeterUnavailable)
	if p.calls != 0 {
		t.Fatalf("the provider received %d requests after a failed reservation write, want 0", p.calls)
	}
}

func TestAsk_ReservationWriteFailureIsThePlainUnavailableAnswer(t *testing.T) {
	sp := &memSpend{failReserve: errors.New("disk full")}
	p := &pricedProvider{reply: Reply{Text: "ok"}}
	svc := meteredService(p, sp)

	ans, err := svc.Ask(context.Background(), manager(), "where is SO-4?", nil)
	if err != nil {
		t.Fatalf("a metering failure must be an answer, not an error: %v", err)
	}
	if ans.Text != UnavailableMessage {
		t.Fatalf("answer = %q, want the plain unavailable sentence", ans.Text)
	}
	if p.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", p.calls)
	}
}

func TestComplete_AFailedProviderCallLeavesTheReservationForTheSweep(t *testing.T) {
	sp := &memSpend{}
	p := &pricedProvider{err: errors.New("upstream 529")}
	svc := meteredService(p, sp)

	if _, err := svc.complete(context.Background(), manager(), CompletionRequest{}); err == nil {
		t.Fatal("the provider's error must reach the caller")
	}
	if k := sp.kinds(); len(k) != 1 || k[0] != SpendReservation {
		t.Fatalf("table = %v, want the lone reservation (nothing is settled that cannot be proven)", k)
	}
}

func TestComplete_ASettlementThatCannotBeWrittenStillReturnsTheReply(t *testing.T) {
	sp := &memSpend{failSettle: errors.New("write refused")}
	p := &pricedProvider{reply: Reply{Text: "ok", TokensIn: 10, TokensOut: 5}}
	svc := meteredService(p, sp)

	r, err := svc.complete(context.Background(), manager(), CompletionRequest{})
	if err != nil || r.Text != "ok" {
		t.Fatalf("the money is already spent, the answer must stand: %v %+v", err, r)
	}
	if sp.settleTries < 2 {
		t.Fatalf("settlement tried %d times, want a retry", sp.settleTries)
	}
}

func TestMonthSpend_CountsAnUnsettledReservationAtItsEstimate(t *testing.T) {
	sp := &memSpend{}
	p := &pricedProvider{}
	p.during = func() {
		got, err := meteredServiceSpend(p, sp, "u-sm")
		if err != nil {
			t.Errorf("MonthSpend: %v", err)
			return
		}
		if got.Total() == 0 {
			t.Errorf("an in flight reservation counted as %+v, want its estimate", got)
		}
	}
	svc := meteredService(p, sp)
	if _, err := svc.complete(context.Background(), manager(), CompletionRequest{Messages: []Message{{Role: RoleUser, Text: strings.Repeat("a", 400)}}}); err != nil {
		t.Fatal(err)
	}
	// Settled at zero tokens: the estimate is replaced, not added to.
	got, _ := svc.MonthSpend(context.Background(), "u-sm")
	if got.Total() != 0 {
		t.Fatalf("after settlement at zero the month reads %+v, want zero", got)
	}
}

// meteredServiceSpend reads the month through a fresh service over the same store.
func meteredServiceSpend(p Provider, sp *memSpend, user string) (Spend, error) {
	return meteredService(p, sp).MonthSpend(context.Background(), user)
}

func TestFoldCalls(t *testing.T) {
	res := SpendRow{Kind: SpendReservation, ExchangeID: "e1", CallNo: 1, TokensIn: 500, TokensOut: 1024, CostMicroCents: 900}
	set := SpendRow{Kind: SpendSettlement, ExchangeID: "e1", CallNo: 1, TokensIn: 100, TokensOut: 20, CostMicroCents: 40}
	voi := SpendRow{Kind: SpendVoid, ExchangeID: "e2", CallNo: 1}
	res2 := SpendRow{Kind: SpendReservation, ExchangeID: "e2", CallNo: 1, TokensIn: 7, CostMicroCents: 7}
	open := SpendRow{Kind: SpendReservation, ExchangeID: "e3", CallNo: 2, TokensIn: 11, CostMicroCents: 11}

	got := FoldCalls([]SpendRow{res, set, res2, voi, open})
	var cost int64
	for _, r := range got {
		cost += r.CostMicroCents
	}
	if len(got) != 2 || cost != 51 {
		t.Fatalf("fold = %+v (cost %d), want the settlement and the open reservation only (cost 51)", got, cost)
	}
}

func TestSweepSettlesAtTheEstimateWhatMayHaveReachedTheProvider(t *testing.T) {
	sp := &memSpend{}
	svc := meteredService(&pricedProvider{}, sp)
	old := at(1)
	sp.rows = []SpendRow{
		{Kind: SpendReservation, ExchangeID: "e-settled", CallNo: 1, Audience: AudienceStaff, StaffUserID: "u-sm", Alias: "a", Model: "m", At: old},
		{Kind: SpendSettlement, ExchangeID: "e-settled", CallNo: 1, Audience: AudienceStaff, StaffUserID: "u-sm", Alias: "a", Model: "m", At: old},
		{Kind: SpendReservation, ExchangeID: "e-ended", CallNo: 1, Audience: AudienceStaff, StaffUserID: "u-sm", Alias: "a", Model: "m", TokensIn: 500, TokensOut: 1024, CostMicroCents: 900, PriceID: "p", At: old},
		{Kind: SpendReservation, ExchangeID: "e-live", CallNo: 1, Audience: AudienceStaff, StaffUserID: "u-sm", Alias: "a", Model: "m", At: at(11)},
	}
	n, err := svc.SweepReservations(context.Background(), 15*time.Minute)
	if err != nil || n != 1 {
		t.Fatalf("swept %d (%v), want exactly the one ended reservation", n, err)
	}
	last := sp.rows[len(sp.rows)-1]
	if last.Kind != SpendSettlement || last.ExchangeID != "e-ended" || last.SettledBy != SettledBySweep ||
		last.TokensIn != 500 || last.TokensOut != 1024 || last.CostMicroCents != 900 || last.PriceID != "p" || !last.At.Equal(old) {
		t.Fatalf("last row = %+v, want a sweep settlement at the reservation's estimate", last)
	}
	if n, _ := svc.SweepReservations(context.Background(), 15*time.Minute); n != 0 {
		t.Fatalf("second sweep touched %d, want 0", n)
	}
}

// The provider answered, the settlement write failed, the sweep ran: the spend is kept.
func TestAnAnsweredCallWhoseSettlementFailedKeepsItsSpendThroughTheSweep(t *testing.T) {
	sp := &memSpend{failSettle: errors.New("write refused")}
	p := &pricedProvider{reply: Reply{Text: "ok", TokensIn: 10, TokensOut: 5}}
	clock := at(11)
	svc := meteredService(p, sp).WithClock(func() time.Time { return clock })
	if _, err := svc.complete(context.Background(), manager(), CompletionRequest{Messages: []Message{{Role: RoleUser, Text: "hi"}}}); err != nil {
		t.Fatal(err)
	}
	from, to := MonthWindow(clock)
	before, _ := sp.DealerMonthMicroCents(context.Background(), from, to)
	if before == 0 {
		t.Fatal("the open reservation must count at its estimate")
	}
	clock = clock.Add(time.Hour)
	sp.failSettle = nil
	if _, err := svc.SweepReservations(context.Background(), 15*time.Minute); err != nil {
		t.Fatal(err)
	}
	after, _ := sp.DealerMonthMicroCents(context.Background(), from, to)
	if after != before {
		t.Fatalf("dealer spend %d after the sweep, was %d: the sweep dropped spend", after, before)
	}
	for _, k := range sp.kinds() {
		if k == SpendVoid {
			t.Fatal("the sweep voided a call that reached the provider")
		}
	}
}

// A call refused before sending is the one thing provably unspent: voided at once.
func TestACallCancelledBeforeSendingIsVoidedAndNeverReachesTheProvider(t *testing.T) {
	sp := &memSpend{}
	p := &pricedProvider{reply: Reply{Text: "ok"}}
	svc := meteredService(p, sp)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.complete(ctx, manager(), CompletionRequest{}); err == nil {
		t.Fatal("a cancelled context must refuse the call")
	}
	if p.calls != 0 {
		t.Fatal("the provider was reached")
	}
	if k := sp.kinds(); len(k) != 2 || k[1] != SpendVoid {
		t.Fatalf("table = %v, want reservation then void", k)
	}
}

func TestSpendRowValidate(t *testing.T) {
	base := SpendRow{Kind: SpendReservation, ExchangeID: "e", CallNo: 1, Audience: AudienceStaff, StaffUserID: "u", Alias: "a", Model: "m"}
	cases := []struct {
		name string
		mut  func(*SpendRow)
		ok   bool
	}{
		{"staff with a staff user", func(*SpendRow) {}, true},
		{"staff with no user", func(r *SpendRow) { r.StaffUserID = "" }, false},
		{"staff with both users", func(r *SpendRow) { r.PortalUserID = "p" }, false},
		{"pro with a portal user", func(r *SpendRow) { r.Audience, r.StaffUserID, r.PortalUserID = AudiencePro, "", "p" }, true},
		{"pro with a staff user", func(r *SpendRow) { r.Audience = AudiencePro }, false},
		{"embed with neither", func(r *SpendRow) { r.Audience, r.StaffUserID = AudienceEmbed, "" }, true},
		{"embed with a user", func(r *SpendRow) { r.Audience = AudienceEmbed }, false},
		{"messages with a staff user", func(r *SpendRow) { r.Audience = AudienceMessages }, true},
		{"unknown audience", func(r *SpendRow) { r.Audience = "web" }, false},
		{"settled by something else", func(r *SpendRow) { r.Kind, r.SettledBy = SpendSettlement, "operator" }, false},
		{"sweep mark on a reservation", func(r *SpendRow) { r.SettledBy = SettledBySweep }, false},
		{"unknown kind", func(r *SpendRow) { r.Kind = "refund" }, false},
		{"negative tokens", func(r *SpendRow) { r.TokensIn = -1 }, false},
		{"negative cost", func(r *SpendRow) { r.CostMicroCents = -1 }, false},
		{"no exchange", func(r *SpendRow) { r.ExchangeID = "" }, false},
	}
	for _, c := range cases {
		r := base
		c.mut(&r)
		if err := r.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: Validate = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

// withTestMeter wires a metered path for tests whose provider is not the stub: a zero priced row for
// it and a cap that is never the thing under test. Rows already in the old token ledger are carried
// over as settlements, so a test that seeds a user's spend through the ledger still means it.
func (s *Service) withTestMeter() *memSpend {
	sp := &memSpend{}
	key := s.provider.Name() + "/" + s.provider.Model()
	s.WithMetering(sp, memPrices{key: {ID: "p0", Alias: s.provider.Name(), Model: s.provider.Model(), Seq: 1}}).
		WithDealerCap(1 << 60)
	if led, ok := s.ledger.(*fakeLedger); ok {
		for i, e := range led.entries {
			sp.rows = append(sp.rows, SpendRow{
				Kind: SpendSettlement, ExchangeID: "seed-" + string(rune('a'+i)), CallNo: 1, Audience: AudienceStaff,
				StaffUserID: e.UserID, RoleCode: e.RoleCode, Alias: "seed", Model: "seed",
				TokensIn: e.TokensIn, TokensOut: e.TokensOut, At: e.At,
			})
		}
	}
	return sp
}

// -- the dealer's month (review P3-1), and the estimate divisor (P3-2) ---------------------------

func TestTheMonthOfBudgetsAndCapsIsTheDealersMonthNotTheServers(t *testing.T) {
	sp := &memSpend{}
	mk := func(ex string, when time.Time) SpendRow {
		return SpendRow{Kind: SpendSettlement, ExchangeID: ex, CallNo: 1, Audience: AudienceStaff, StaffUserID: "u-sm",
			Alias: "a", Model: "m", TokensIn: 10, CostMicroCents: 7, At: when}
	}
	// 19:00 and 23:00 on the dealer's last evening of August, which are September in UTC for the second.
	sp.rows = []SpendRow{
		mk("e1", time.Date(2026, 8, 31, 23, 0, 0, 0, time.UTC)),
		mk("e2", time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC)),
		withTokens(mk("e3", time.Date(2026, 9, 1, 5, 0, 0, 0, time.UTC)), 1000), // 01:00 September 1 in Toronto
	}
	clock := time.Date(2026, 9, 1, 3, 30, 0, 0, time.UTC) // UTC says September, Toronto says August 31
	svc := meteredService(&pricedProvider{}, sp).WithClock(func() time.Time { return clock })

	got, err := svc.MonthSpend(context.Background(), "u-sm")
	if err != nil {
		t.Fatal(err)
	}
	if got.TokensIn != 20 {
		t.Fatalf("month spend = %+v, want August's two rows (20 tokens): the dealer's month, not UTC's", got)
	}
	from, to, _ := svc.monthWindow(context.Background())
	if from.Location().String() != "America/Toronto" || from.Day() != 1 || from.Month() != time.August || to.Month() != time.September {
		t.Fatalf("window = %v to %v", from, to)
	}
}

func withTokens(r SpendRow, n int) SpendRow { r.TokensIn = n; return r }

func TestTheDealersOwnZoneIsHonoured(t *testing.T) {
	svc := meteredService(&pricedProvider{}, &memSpend{}).
		WithClock(func() time.Time { return time.Date(2026, 8, 31, 20, 0, 0, 0, time.UTC) }).
		WithZone(fixedZone("Pacific/Auckland")) // already September 1 there
	from, _, err := svc.monthWindow(context.Background())
	if err != nil || from.Month() != time.September {
		t.Fatalf("window from %v (%v), want September in the dealer's own zone", from, err)
	}
}

type fixedZone string

func (z fixedZone) BusinessZone(context.Context) (houseday.Zone, error) {
	return houseday.NewZone(string(z))
}

func TestTheInputEstimateIsBytesOverThree(t *testing.T) {
	req := CompletionRequest{Messages: []Message{{Role: RoleUser, Text: strings.Repeat("a", 300)}}}
	// 300 text bytes + 16 framing = 316 bytes; three bytes a token, rounded up.
	if got := estimateInputTokens(req); got != 106 {
		t.Fatalf("estimate = %d, want 106 (316 bytes over 3 rounded up)", got)
	}
}
