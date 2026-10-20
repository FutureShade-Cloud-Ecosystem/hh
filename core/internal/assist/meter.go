package assist

// meter.go, slice 2: no spend without a record of it.
//
// THE RULE IS ONE SENTENCE: a provider call is made only after a reservation row for it is written,
// and every call that was made ends in a settlement row with the usage the provider reported.
// Everything else here is a consequence.
//
//	reserve   estimate (input size plus the call's output ceiling), write the row, refuse the call
//	          if the write fails. The user gets the plain unavailable answer; nothing was spent.
//	call      the provider is reached only past the reservation.
//	settle    actual tokens in, out and cached, cost at the price row in force, that row's id.
//	sweep     a reservation open past the grace may have reached the provider, so a job SETTLES it at
//	          its estimate, marked as the sweep's. Only a call refused before sending is voided.
//
// MONTHLY SPEND READS THIS TABLE. A call counts once, as its settlement when it has one, as nothing
// when it was voided, and as its ESTIMATE while it is still open (FoldCalls). So two requests in
// flight together cannot both pass a budget that only one fits, and a crash between the reservation
// and the settlement never reads as free.
//
// APPEND ONLY. The store has no update or delete verb, and the migration refuses both with the 0154
// trigger pattern. A correction is a row.
//
// NO MARKUP HERE. Cost is the provider's price times the usage. FutureBuild's markup is applied
// in the billing export, never in this ledger (prices.go).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// Audience is who a spend row was incurred for.
type Audience string

const (
	AudienceStaff    Audience = "staff"
	AudiencePro      Audience = "pro"
	AudienceEmbed    Audience = "embed"
	AudienceMessages Audience = "messages"
)

// SpendKind is the row's place in a call's life.
type SpendKind string

const (
	SpendReservation SpendKind = "reservation"
	SpendSettlement  SpendKind = "settlement"
	SpendVoid        SpendKind = "void"
)

// SpendRow is one row of the append only spend table.
//
// An exchange is one question; it makes one or two provider calls, so a call is keyed by the
// exchange id and its number inside the exchange. A settlement or a void carries the At of its
// reservation, so a call never straddles a month.
type SpendRow struct {
	ID         string
	Kind       SpendKind
	ExchangeID string
	CallNo     int
	Audience   Audience
	// Exactly one of the two user ids is set, except for the embed audience, which has neither.
	StaffUserID  string
	PortalUserID string
	RoleCode     string
	Alias        string
	Model        string
	TokensIn     int
	TokensOut    int
	TokensCached int
	// CostMicroCents is integer micro cents (a millionth of a cent). On a reservation it is the
	// estimate; on a settlement the actual cost; on a void zero.
	CostMicroCents int64
	// PriceID names the assist_model_prices row used, empty for a free provider.
	PriceID string
	// SettledBy is empty for a settlement from the provider call itself and SettledBySweep for one the
	// sweep wrote at the reservation's estimate. Only a settlement carries it.
	SettledBy string
	At        time.Time
}

// SettledBySweep marks a settlement the sweep wrote at the estimate.
const SettledBySweep = "sweep"

// Validate is the Go twin of the table's CHECK constraints.
func (r SpendRow) Validate() error {
	switch r.Kind {
	case SpendReservation, SpendSettlement, SpendVoid:
	default:
		return fmt.Errorf("assist: spend row kind %q is not reservation, settlement or void", r.Kind)
	}
	if r.ExchangeID == "" {
		return errors.New("assist: spend row has no exchange id")
	}
	switch r.Audience {
	case AudienceStaff, AudienceMessages:
		if r.StaffUserID == "" || r.PortalUserID != "" {
			return fmt.Errorf("assist: a %s spend row carries exactly the staff user", r.Audience)
		}
	case AudiencePro:
		if r.PortalUserID == "" || r.StaffUserID != "" {
			return errors.New("assist: a pro spend row carries exactly the portal user")
		}
	case AudienceEmbed:
		if r.StaffUserID != "" || r.PortalUserID != "" {
			return errors.New("assist: an embed spend row carries no user")
		}
	default:
		return fmt.Errorf("assist: spend row audience %q is not staff, pro, embed or messages", r.Audience)
	}
	if r.SettledBy != "" && (r.Kind != SpendSettlement || r.SettledBy != SettledBySweep) {
		return errors.New("assist: only a settlement may be marked settled by the sweep")
	}
	if r.TokensIn < 0 || r.TokensOut < 0 || r.TokensCached < 0 || r.CostMicroCents < 0 {
		return errors.New("assist: spend row has a negative amount")
	}
	return nil
}

func callKey(r SpendRow) string { return fmt.Sprintf("%s/%d", r.ExchangeID, r.CallNo) }

// FoldCalls reduces rows to one effective row per call: the settlement when there is one, nothing
// when the call was voided, the reservation (its estimate) while the call is open. It is the rule
// monthly spend, the dealer cap and the sweep all share, and the SQL repeats it.
func FoldCalls(rows []SpendRow) []SpendRow {
	best := map[string]SpendRow{}
	rank := func(k SpendKind) int {
		switch k {
		case SpendSettlement:
			return 0
		case SpendVoid:
			return 1
		}
		return 2
	}
	var order []string
	for _, r := range rows {
		k := callKey(r)
		cur, seen := best[k]
		if !seen {
			order = append(order, k)
		}
		if !seen || rank(r.Kind) < rank(cur.Kind) {
			best[k] = r
		}
	}
	var out []SpendRow
	for _, k := range order {
		if best[k].Kind != SpendVoid {
			out = append(out, best[k])
		}
	}
	return out
}

// SpendStore is the append only spend table.
type SpendStore interface {
	Reserve(ctx context.Context, r SpendRow) error
	// ReserveWithin is the check and the reservation as ONE atomic step: under locks that serialise
	// every asker of the same dealer (and the same user), it re-reads the month's folded sums,
	// refuses with ErrOverBudget when the user's budget is reached or ErrDealerCap when the
	// reservation would pass the cap, and otherwise inserts the reservation. Concurrent calls
	// therefore see each other's reservations. Locks are always taken dealer first, then user.
	ReserveWithin(ctx context.Context, r SpendRow, lim ReserveLimits) error
	Settle(ctx context.Context, r SpendRow) error
	Void(ctx context.Context, r SpendRow) error
	// UserMonth is one staff user's spend in [from, to), open reservations counted at their estimate.
	UserMonth(ctx context.Context, staffUserID string, from, to time.Time) (Spend, error)
	// UsersMonth is every staff user's spend in [from, to), for the admin surface.
	UsersMonth(ctx context.Context, from, to time.Time) (map[string]Spend, error)
	// DealerMonthMicroCents is the cost of every audience together in [from, to), open
	// reservations counted at their estimate.
	DealerMonthMicroCents(ctx context.Context, from, to time.Time) (int64, error)
	// Unsettled lists reservations made before the cutoff that have neither a settlement nor a void.
	Unsettled(ctx context.Context, reservedBefore time.Time) ([]SpendRow, error)
	// Settled lists settlement rows in [from, to), for the billing export.
	Settled(ctx context.Context, from, to time.Time) ([]SpendRow, error)
}

// ReserveLimits are the limits ReserveWithin enforces inside its lock.
type ReserveLimits struct {
	// From and To are the month window (the dealer's month, half open).
	From, To time.Time
	// BudgetTokens is the staff user's monthly token budget; the call is refused when the month's
	// spend, open reservations at their estimate, has reached it.
	BudgetTokens int
	// CheckDealer is false for a free provider, which spends nothing against the cap.
	CheckDealer bool
	// DealerCapMicroCents is the cap; the reservation is refused if it would pass it.
	DealerCapMicroCents int64
}

// Errors the metering path returns. Ask turns each into an ordinary answer.
var (
	// ErrMeterUnavailable: the call cannot be recorded, so it is not made.
	ErrMeterUnavailable = errors.New("assist: spend cannot be recorded, the call was not made")
	// ErrDealerCap: the dealer wide monthly cap is reached.
	ErrDealerCap = errors.New("assist: the dealer wide monthly cap is reached")
)

// UnavailableMessage is the plain answer when a call is refused because it could not be recorded.
const UnavailableMessage = "The assistant isn't available right now. " +
	"You can still look things up with the buttons below; those don't use any AI."

// MaxOutputTokens is the output ceiling every provider call is made under, and so what a
// reservation reserves for the reply. The Anthropic adapter's request ceiling is the same number.
const MaxOutputTokens = 1024

// estimateInputTokens is a deliberately generous count of the request's input: three bytes a token
// (JSON heavy, numeric and non English text often runs under four), rounded up, over every message and every tool description. An under-estimate lets a reservation
// pass a budget the call then overruns, so the error is made in the other direction.
func estimateInputTokens(req CompletionRequest) int {
	n := 0
	for _, m := range req.Messages {
		n += len(m.Text) + len(m.ToolName) + 16
	}
	for _, t := range req.Tools {
		n += len(t.Name) + len(t.Description) + len(t.Title) + 32
		for _, a := range t.Args {
			n += len(a.Name) + len(a.Label) + len(a.Example) + 16
		}
	}
	return (n + 2) / 3
}

// exchange identity travels in the context so both calls of one Ask share an exchange id.
type exchangeKey struct{}

type exchangeState struct {
	id    string
	calls int
}

func withExchange(ctx context.Context) context.Context {
	if _, ok := ctx.Value(exchangeKey{}).(*exchangeState); ok {
		return ctx
	}
	return context.WithValue(ctx, exchangeKey{}, &exchangeState{id: newUUID()})
}

// nextCall returns the exchange id and the next call number.
func nextCall(ctx context.Context) (string, int) {
	st, ok := ctx.Value(exchangeKey{}).(*exchangeState)
	if !ok {
		return newUUID(), 1
	}
	st.calls++
	return st.id, st.calls
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// WithMetering attaches the spend table and the price table. Without them only a free provider (the
// stub) may be called: a paid provider with nothing to record its spend in is refused.
func (s *Service) WithMetering(spend SpendStore, prices PriceStore) *Service {
	s.spend, s.prices = spend, prices
	return s
}

// EnvDealerCap names the deployment variable that carries the dealer wide monthly cap in micro
// cents. FutureBuild sets it at deploy from its own configuration. It is deliberately not an
// assist setting: a dealer admin writes settings, and the cap is the one number above them.
const EnvDealerCap = "HOS_ASSIST_DEALER_CAP_MICROCENTS"

// ParseDealerCap reads the variable's value. Anything that is not a positive whole number reads as
// zero, which refuses paid calls: a typo must not open the cap.
func ParseDealerCap(raw string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// WithDealerCap sets the dealer wide monthly cap in micro cents. It is deployment configuration set
// by FutureBuild, never an assist setting a dealer admin can write. Zero means not configured, and
// a paid call is refused until it is.
func (s *Service) WithDealerCap(microCents int64) *Service {
	s.dealerCap = microCents
	return s
}

// freeProvider reports whether a provider costs nothing, which is only the stub.
func freeProvider(p Provider) bool { return p.Name() == "stub" }

// Complete is complete for callers outside the package (the quote extraction rails): the same
// budget check, dealer cap, reservation and settlement, as the asking user, in an exchange of its
// own. It exists so no paid provider call is made outside the meter.
func (s *Service) Complete(ctx context.Context, c Caller, req CompletionRequest) (Reply, error) {
	return s.complete(withExchange(ctx), c, req)
}

// complete is the only place this package reaches a provider. Everything the slice adds, the budget
// check, the dealer cap, the reservation and the settlement, happens here, so a path that calls
// the provider cannot skip them.
// meteredComplete is the metered body of Service.complete (service.go), which resolves the provider
// and the alias per call: every attempt, retry and escalation arrives here with its own provider and
// takes its own call number.
func (s *Service) meteredComplete(ctx context.Context, c Caller, p Provider, alias string, req CompletionRequest) (Reply, error) {
	free := freeProvider(p)
	if s.spend == nil {
		if !free {
			s.warn("assist: a paid provider is configured with no spend table wired, the call is refused")
			return Reply{}, ErrMeterUnavailable
		}
		return p.Complete(ctx, req)
	}

	now := s.now()
	from, to, err := s.monthWindow(ctx)
	if err != nil {
		return Reply{}, s.meterFailure("resolve the dealer's month", err)
	}

	// The caller's own allowance: the second call of an exchange and the quote path arrive here
	// without passing Ask's first check. The authoritative check is ReserveWithin's, under lock.
	budget, err := s.Budget(ctx, c)
	if err != nil {
		return Reply{}, s.meterFailure("read budget", err)
	}

	model := p.Model()
	var price ModelPrice
	if !free {
		if s.dealerCap <= 0 {
			s.warn("assist: no dealer wide cap is configured, paid calls are refused until FutureBuild sets one")
			return Reply{}, ErrMeterUnavailable
		}
		var ok bool
		price, ok, err = s.prices.PriceFor(ctx, alias, model)
		if err != nil {
			return Reply{}, s.meterFailure("read price", err)
		}
		if !ok {
			s.warn("assist: no price row for the model in force, the call is refused", "alias", alias, "model", model)
			return Reply{}, ErrMeterUnavailable
		}
	}

	estIn := estimateInputTokens(req)
	estCost := price.Cost(estIn, MaxOutputTokens, 0)

	exchangeID, callNo := nextCall(ctx)
	row := SpendRow{
		ExchangeID: exchangeID, CallNo: callNo, Audience: AudienceStaff,
		StaffUserID: c.UserID, RoleCode: c.RoleCode, Alias: alias, Model: model,
		PriceID: price.ID, At: now,
	}
	res := row
	res.Kind, res.TokensIn, res.TokensOut, res.CostMicroCents = SpendReservation, estIn, MaxOutputTokens, estCost
	err = s.spend.ReserveWithin(ctx, res, ReserveLimits{
		From: from, To: to, BudgetTokens: budget.Tokens, CheckDealer: !free, DealerCapMicroCents: s.dealerCap,
	})
	switch {
	case err == nil:
	case errors.Is(err, ErrOverBudget), errors.Is(err, ErrDealerCap):
		return Reply{}, err
	default:
		return Reply{}, s.meterFailure("write reservation", err)
	}

	// The one thing provably unspent: the context ended before the request was sent. Voided at
	// once; the provider is not reached.
	if cerr := ctx.Err(); cerr != nil {
		v := row
		v.Kind = SpendVoid
		if verr := s.spend.Void(context.WithoutCancel(ctx), v); verr != nil {
			s.warn("assist: void of an unsent call could not be written, the sweep will settle it at its estimate", "err", verr)
		}
		return Reply{}, cerr
	}

	reply, callErr := p.Complete(ctx, req)
	if callErr != nil {
		// The call may have reached the provider, so it is neither settled nor voided here. The
		// reservation stays, counted at its estimate, and the sweep settles it at that estimate.
		return reply, callErr
	}

	set := row
	set.Kind, set.TokensIn, set.TokensOut, set.TokensCached = SpendSettlement, reply.TokensIn, reply.TokensOut, reply.TokensCached
	set.CostMicroCents = price.Cost(reply.TokensIn, reply.TokensOut, reply.TokensCached)
	var werr error
	for try := 0; try < 3; try++ {
		if werr = s.spend.Settle(ctx, set); werr == nil {
			break
		}
	}
	if werr != nil {
		// The money is spent and the answer is good. The open reservation keeps counting at its
		// estimate; the failure is loud so an operator reconciles it.
		s.warn("assist: settlement could not be written, the reservation stands at its estimate",
			"exchange", exchangeID, "call", callNo, "err", werr)
	}
	return reply, nil
}

func (s *Service) meterFailure(what string, err error) error {
	s.warn("assist: metering failed, the call is not made", "step", what, "err", err)
	return ErrMeterUnavailable
}

func (s *Service) warn(msg string, args ...any) {
	if s.log != nil {
		s.log.Warn(msg, args...)
	}
}

// WithZone attaches the dealer's business zone source. The month of every cap, budget and report is
// the dealer's month in that zone; with none wired it is the house zone (America/Toronto), never the
// server clock's location.
func (s *Service) WithZone(src houseday.Source) *Service { s.zone = src; return s }

// monthWindow is the dealer's current month as a half open window.
func (s *Service) monthWindow(ctx context.Context) (time.Time, time.Time, error) {
	zone, err := houseday.Resolve(ctx, s.zone)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	from, to := MonthWindow(s.now().In(zone.Location()))
	return from, to, nil
}

// SweepReservations settles at their estimate the reservations open longer than the grace period,
// and returns how many. Run by the worker's job runner.
func (s *Service) SweepReservations(ctx context.Context, grace time.Duration) (int, error) {
	if s.spend == nil {
		return 0, nil
	}
	return SweepReservations(ctx, s.spend, s.now(), grace)
}

// SpendSweepJobName is the name the sweep is registered and scheduled under in `core jobs`.
const SpendSweepJobName = "assist.spend_reservation_sweep"

// ReservationGrace is how long a reservation may stay open before the sweep voids it: longer than
// any provider call can run, so a live call is never voided under its own settlement.
const ReservationGrace = 15 * time.Minute

// SweepReservations is the sweep over a store, for the worker's job (which holds a store, not a
// service). Each reservation older than the grace with neither a settlement nor a void may have
// reached the provider, so voiding it would drop real spend: it is SETTLED at its estimate (same
// tokens, cost and price row, the reservation's at), marked SettledBySweep. A reservation is voided
// only where it is provable that nothing was sent, and that void is written by complete itself.
func SweepReservations(ctx context.Context, store SpendStore, now time.Time, grace time.Duration) (int, error) {
	open, err := store.Unsettled(ctx, now.Add(-grace))
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range open {
		v := r
		v.Kind, v.SettledBy = SpendSettlement, SettledBySweep
		if err := store.Settle(ctx, v); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// refusalAnswer turns a metering refusal into the ordinary answer the user sees. The provider was
// not reached for any of these, and none is an error to the handler.
func refusalAnswer(err error) (Answer, bool) {
	switch {
	case errors.Is(err, ErrOverBudget), errors.Is(err, ErrDealerCap):
		return Answer{Text: LimitReachedMessage, LimitReached: true, Results: []Result{}}, true
	case errors.Is(err, ErrMeterUnavailable):
		return Answer{Text: UnavailableMessage, Results: []Result{}}, true
	}
	return Answer{}, false
}
