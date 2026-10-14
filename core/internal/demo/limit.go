//go:build demo

// The per-address mint limit lives in internal/platform/ratelimit, shared
// with the real sign in exchange; the demo module keeps its own names for
// it so the mint and its tests read as they always did.
package demo

import (
	"strconv"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/ratelimit"
)

// The allowance a serve wiring gets when it names none.
const (
	DefaultBurstAllowance = ratelimit.DefaultBurstAllowance
	DefaultBurstWindow    = ratelimit.DefaultBurstWindow
	// DefaultAddressCeiling is the allowance of the second bucket every
	// mint also spends, keyed by the address alone.
	DefaultAddressCeiling = ratelimit.DefaultAddressCeiling
	// VisitorAllowanceVar is the optional per-visitor allowance, read once
	// at boot and passed to NewAddressLimit for the visitor bucket only.
	// It exists for the testers' copy: staging and public deployments leave
	// it unset and keep the default burst allowance.
	VisitorAllowanceVar = "HH_DEMO_MINT_VISITOR_ALLOWANCE"
	// MaxVisitorAllowance caps the per-visitor allowance so an operator
	// cannot raise it without bound.
	MaxVisitorAllowance = 20
)

// AddressLimit is the shared limit.
type AddressLimit = ratelimit.AddressLimit

// NewAddressLimit returns the shared limit: a non positive allowance or
// window selects the defaults, a nil clock the wall clock.
func NewAddressLimit(allowance int, window time.Duration, now func() time.Time) *AddressLimit {
	return ratelimit.NewAddressLimit(allowance, window, now)
}

// VisitorAllowance parses the raw HH_DEMO_MINT_VISITOR_ALLOWANCE value.
// An empty, unparseable, or non-positive value keeps the default burst
// allowance; a positive value is capped at MaxVisitorAllowance. Staging
// and public deployments leave the variable unset.
func VisitorAllowance(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultBurstAllowance
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return DefaultBurstAllowance
	}
	if n > MaxVisitorAllowance {
		return MaxVisitorAllowance
	}
	return n
}
