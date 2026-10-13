//go:build demo

// The per-address mint limit lives in internal/platform/ratelimit, shared
// with the real sign in exchange; the demo module keeps its own names for
// it so the mint and its tests read as they always did.
package demo

import (
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
)

// AddressLimit is the shared limit.
type AddressLimit = ratelimit.AddressLimit

// NewAddressLimit returns the shared limit: a non positive allowance or
// window selects the defaults, a nil clock the wall clock.
func NewAddressLimit(allowance int, window time.Duration, now func() time.Time) *AddressLimit {
	return ratelimit.NewAddressLimit(allowance, window, now)
}
