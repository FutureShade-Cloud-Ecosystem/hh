package dashboard

import "context"

// MetricProvider supplies ONE tile's number.
//
// CONSUMER-DECLARED. This package is the consumer, it wants numbers and does not care who has
// them, so the interface lives here and every implementation lives in cmd/server/main.go as an
// adapter over the module that actually owns the data. internal/dashboard therefore imports
// nothing from pos, order, dispatch, tasks, billing, inventory, warranty or messaging, and a
// module can be deleted, renamed or rewritten without this package noticing.
//
// A provider MUST honour Scope.BranchIDs / Scope.AllBranches. The dashboard cannot enforce branch
// scoping on a number it did not compute, an adapter that ignores the scope leaks another
// branch's figures onto this branch's home screen, and no test in this package can catch it. That
// obligation is the price of the seam and it is stated here so the adapter author reads it.
//
// A provider MAY be slow and MAY fail. Service.Resolve gives each one its own timeout, runs them
// concurrently, and turns a failure into StateError rather than a 500: one module having a bad
// afternoon must not blank the whole cockpit.
type MetricProvider interface {
	Measure(ctx context.Context, scope Scope) (Value, error)
}

// MetricFunc adapts a plain function to MetricProvider, so an adapter in the composition root can
// be one closure rather than one type per tile.
type MetricFunc func(ctx context.Context, scope Scope) (Value, error)

func (f MetricFunc) Measure(ctx context.Context, scope Scope) (Value, error) { return f(ctx, scope) }
