package assist

// ratelimit.go, slice 2 sub item 4: request rate limits, in front of the budget and the provider.
//
// THREE LAYERS, ALL REFUSED BEFORE THE PROVIDER IS REACHED
//
//	per asker   every build. Keyed by the staff user and the session's jti where it has one, with
//	            the allowance and window read live from assist_settings (rate_limit_requests,
//	            rate_limit_window_seconds), so an operator changes it without a restart.
//	demo        demo copies only. Demo personas are shared users, so a user keyed limit alone lets
//	            one visitor drain a flow for every other. The jti is one per minted demo session, the
//	            address is the demo mint's own (clientAddress below), and one wide ceiling covers
//	            the box. Wired only when the build is the demo.
//
// A request is admitted only if every applicable bucket has room; then every bucket is charged. A
// refused request charges nothing, so a visitor over the address limit does not also burn their
// own allowance.
//
// The limiter is the platform's own (internal/platform/ratelimit), the one the demo mint uses. It
// lives in memory and forgets with the window, like the mint's: nothing is written to the database.

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/ratelimit"
)

// ErrRateLimited: too many requests from this asker, session or address.
var ErrRateLimited = errorString("assist: rate limited")

type errorString string

func (e errorString) Error() string { return string(e) }

// RateLimitedMessage is the ordinary answer to a request over a rate limit. Nothing is wrong and
// nothing was spent: the person is asking faster than the allowance.
const RateLimitedMessage = "You're asking quickly. Give it a moment and try again. " +
	"You can still look things up with the buttons below; those don't use any AI."

// The two settings keys and their bounds. Bounded so no setting can switch the limit off.
const (
	KeyRateRequests      = "rate_limit_requests"
	KeyRateWindowSeconds = "rate_limit_window_seconds"

	DefaultRateRequests      = 20
	DefaultRateWindowSeconds = 60
	maxRateRequests          = 1000
	minRateWindowSeconds     = 10
	maxRateWindowSeconds     = 3600
)

// RateSettings is the per asker allowance.
type RateSettings struct {
	Requests int
	Window   time.Duration
}

// RateSettingsFrom folds the raw settings onto the defaults. A value out of range reads as the
// default, never as unlimited.
func RateSettingsFrom(raw map[string]string) RateSettings {
	rs := RateSettings{Requests: DefaultRateRequests, Window: DefaultRateWindowSeconds * time.Second}
	if n, err := strconv.Atoi(strings.TrimSpace(raw[KeyRateRequests])); err == nil && n >= 1 && n <= maxRateRequests {
		rs.Requests = n
	}
	if n, err := strconv.Atoi(strings.TrimSpace(raw[KeyRateWindowSeconds])); err == nil && n >= minRateWindowSeconds && n <= maxRateWindowSeconds {
		rs.Window = time.Duration(n) * time.Second
	}
	return rs
}

// validRateSetting is SetSetting's check for the two keys.
func validRateSetting(key, value string) bool {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	if key == KeyRateRequests {
		return n >= 1 && n <= maxRateRequests
	}
	return n >= minRateWindowSeconds && n <= maxRateWindowSeconds
}

// The demo's defaults. A visitor is one browser's minted session; the address is one tester's
// connection; the wide ceiling is the box.
const (
	DemoVisitorAllowance = 12
	DemoAddressAllowance = 30
	DemoWideAllowance    = 300
	DemoBurstWindow      = time.Minute
	DemoWideWindow       = time.Hour
)

// DemoLimits are the demo copy's extra buckets.
type DemoLimits struct {
	Visitor *ratelimit.AddressLimit
	Address *ratelimit.AddressLimit
	Wide    *ratelimit.AddressLimit
}

// NewDemoLimits builds the demo buckets with the defaults above. A nil clock is the wall clock.
func NewDemoLimits(now func() time.Time) *DemoLimits {
	return &DemoLimits{
		Visitor: ratelimit.NewAddressLimit(DemoVisitorAllowance, DemoBurstWindow, now),
		Address: ratelimit.NewAddressLimit(DemoAddressAllowance, DemoBurstWindow, now),
		Wide:    ratelimit.NewAddressLimit(DemoWideAllowance, DemoWideWindow, now),
	}
}

// RateLimits is the service's limiter state.
type RateLimits struct {
	mu    sync.Mutex
	now   func() time.Time
	asker *ratelimit.AddressLimit
	cur   RateSettings
	demo  *DemoLimits
}

// NewRateLimits returns the per asker limiter; demo is nil outside demo copies.
func NewRateLimits(demo *DemoLimits, now func() time.Time) *RateLimits {
	return &RateLimits{demo: demo, now: now}
}

// Admit charges one request for the caller, or reports ErrRateLimited and charges nothing.
func (l *RateLimits) Admit(c Caller, rs RateSettings) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.asker == nil || l.cur != rs {
		l.asker = ratelimit.NewAddressLimit(rs.Requests, rs.Window, l.now)
		l.cur = rs
	}
	type bucket struct {
		lim *ratelimit.AddressLimit
		key string
	}
	buckets := []bucket{{l.asker, "u:" + c.UserID + "|s:" + c.SessionID}}
	if l.demo != nil {
		visitor := c.SessionID
		if visitor == "" {
			visitor = "addr:" + c.Address
		}
		addr := c.Address
		if addr == "" {
			addr = "unknown"
		}
		buckets = append(buckets,
			bucket{l.demo.Visitor, "v:" + visitor},
			bucket{l.demo.Address, "a:" + addr},
			bucket{l.demo.Wide, "wide"})
	}
	for _, b := range buckets {
		if !b.lim.Peek(b.key) {
			return ErrRateLimited
		}
	}
	for _, b := range buckets {
		b.lim.Record(b.key)
	}
	return nil
}

// WithRateLimits attaches the limiter. Without it nothing is rate limited, which is the test and
// degraded default; the composition root always attaches one.
func (s *Service) WithRateLimits(l *RateLimits) *Service {
	s.rates = l
	return s
}

// admit is Ask's rate check. The settings read is live, so a changed allowance applies at once.
func (s *Service) admit(ctx context.Context, c Caller) error {
	if s.rates == nil {
		return nil
	}
	var raw map[string]string
	if s.settings != nil {
		if m, err := s.settings.Settings(ctx); err == nil {
			raw = m
		}
	}
	return s.rates.Admit(c, RateSettingsFrom(raw))
}

// clientAddress is the demo mint's trusted address rule, mirrored exactly
// (core/internal/demo/session.go clientAddress is unexported and in a build tagged package, so it
// cannot be imported here): the connection's own address stands, except behind the box's own proxy
// on loopback, where the first address of the forwarded chain names the tester.
func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !isLoopbackHost(host) {
		return host
	}
	if chain := r.Header.Get("X-Forwarded-For"); chain != "" {
		first := strings.TrimSpace(strings.Split(chain, ",")[0])
		if first != "" {
			return first
		}
	}
	return host
}

func isLoopbackHost(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
