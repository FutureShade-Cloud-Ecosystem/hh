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
	// AddressCeilingVar is the optional address ceiling, read once at boot
	// and passed to NewAddressLimit for the address bucket only. It exists
	// for a room of testers behind one office address, who share the bucket.
	AddressCeilingVar = "HH_DEMO_MINT_ADDRESS_CEILING"
	// MaxAddressCeiling caps the address ceiling. The bucket's whole job is
	// to bound cookie farming from one address: each fresh visitor id buys
	// DefaultBurstAllowance mints, so the ceiling is the number of mints a
	// script can spend per address per window whatever ids it forges. 200
	// is five times the default, room for a large office, while a farm
	// stays bounded at a few hundred session mints per window.
	MaxAddressCeiling = 200
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

// AddressCeiling parses the raw HH_DEMO_MINT_ADDRESS_CEILING value, with the
// per-visitor allowance already in effect. An empty, unparseable, or
// non-positive value keeps the default ceiling; a positive value is capped
// at MaxAddressCeiling. The result is never below the visitor allowance: a
// ceiling under it would refuse a single visitor's own flow, which the
// visitor bucket is there to meter. Staging and public deployments leave
// the variable unset.
func AddressCeiling(raw string, visitorAllowance int) int {
	n := DefaultAddressCeiling
	if parsed, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && parsed > 0 {
		n = parsed
	}
	if n > MaxAddressCeiling {
		n = MaxAddressCeiling
	}
	if n < visitorAllowance {
		n = visitorAllowance
	}
	return n
}
