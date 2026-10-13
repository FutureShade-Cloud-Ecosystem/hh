package main

// The sign in exchange's composition (state note section 6): configuration,
// the collaborators and the handler, shared by the default build and the demo
// build's console mode. The exchange reads membership from the UNCACHED
// FB Console source, never from the cache the per request gate sits behind.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/exchange"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/ratelimit"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	envSigninEmailDomains   = "CORE_SIGNIN_EMAIL_DOMAINS"
	envSigninEmailAddresses = "CORE_SIGNIN_EMAIL_ADDRESSES"
	envTrustedProxies       = "CORE_TRUSTED_PROXIES"
	envExchangeJWTBound     = "CORE_EXCHANGE_JWT_BOUND"
)

// defaultExchangeJWTBound is the bound on a Console JWT's exp when none is
// configured: a little over the probed JWT life, so a small clock
// difference with Console never refuses a live JWT.
const defaultExchangeJWTBound = 20 * time.Minute

// exchangeConfig is the exchange's environment, read without touching a
// file or the network. Empty allow lists are valid: the route then refuses
// every sign in.
type exchangeConfig struct {
	domains   []string
	addresses []string
	trusted   ratelimit.TrustedProxies
	jwtBound  time.Duration
}

func loadExchangeConfig(getenv func(string) string) (exchangeConfig, error) {
	cfg := exchangeConfig{
		domains:   exchange.ParseAllowList(getenv(envSigninEmailDomains)),
		addresses: exchange.ParseAllowList(getenv(envSigninEmailAddresses)),
		jwtBound:  defaultExchangeJWTBound,
	}
	trusted, err := ratelimit.ParseTrustedProxies(getenv(envTrustedProxies))
	if err != nil {
		return exchangeConfig{}, fmt.Errorf("%s must be a comma list of addresses", envTrustedProxies)
	}
	cfg.trusted = trusted
	if raw := strings.TrimSpace(getenv(envExchangeJWTBound)); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return exchangeConfig{}, fmt.Errorf("%s must be a positive duration, got %q", envExchangeJWTBound, raw)
		}
		if err := identity.CheckJWTClaimBound(d); err != nil {
			return exchangeConfig{}, fmt.Errorf("%s is below the console JWT life of %s", envExchangeJWTBound, identity.MinJWTClaimBound)
		}
		cfg.jwtBound = d
	}
	return cfg, nil
}

// defaultSignInMethods is the named seam of gate 4b until item 6c lands the
// dealer's allowed sign in methods: it admits the house sign in (the
// Console email and password provider) and nothing else, so a session of
// any other provider is refused rather than passed.
type defaultSignInMethods struct{}

// SignInMethodAllowed implements exchange.SignInMethods.
func (defaultSignInMethods) SignInMethodAllowed(_ context.Context, _ string, provider string) (bool, error) {
	return provider == "email", nil
}

// exchangeDeps is what the two builds hand the exchange composition.
type exchangeDeps struct {
	getenv   func(string) string
	log      *slog.Logger
	pool     *pgxpool.Pool
	session  *sessionComposition
	appwrite appwriteNames
	orgs     authz.OrgResolver
	staff    *identity.StaffAuthorityResolver
	knob     identity.DriverSessionHours
	audit    *audit.Service
}

type appwriteNames struct{ endpoint, project, apiKey string }

// buildExchange constructs the handler; a configuration refusal stops the
// boot.
func buildExchange(d exchangeDeps) (*exchange.Handler, error) {
	if d.session == nil {
		return nil, errors.New("exchange: the persistent session composition is required")
	}
	cfg, err := loadExchangeConfig(d.getenv)
	if err != nil {
		return nil, err
	}
	raw, err := authz.NewAppwriteMembershipSource(d.appwrite.endpoint, d.appwrite.project, d.appwrite.apiKey, nil)
	if err != nil {
		return nil, err
	}
	console, err := authz.NewConsoleClient(d.appwrite.endpoint, d.appwrite.project, d.appwrite.apiKey, nil)
	if err != nil {
		return nil, err
	}
	claims, err := identity.NewJWTClaims(d.pool, cfg.jwtBound)
	if err != nil {
		return nil, err
	}
	return exchange.New(exchange.Config{
		Console: console, Members: raw, Orgs: d.orgs, Claims: claims, Staff: d.staff,
		Knob: d.knob, Audit: d.audit, Minter: d.session.minter, Methods: defaultSignInMethods{},
		AllowedDomains: cfg.domains, AllowedAddresses: cfg.addresses,
		TokenTTL: d.session.exchangeTTL, Trusted: cfg.trusted, Log: d.log,
	})
}
