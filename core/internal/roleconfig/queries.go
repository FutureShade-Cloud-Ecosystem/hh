package roleconfig

import (
	"fmt"
	"time"
)

// Level returns the role's level on the surface. An unknown name is an
// error, never a silent none: a guard must not read its own typo as an
// access level.
func (c *Config) Level(role, surface string) (Level, error) {
	if !IsRole(role) {
		return LevelNone, fmt.Errorf("roleconfig: unknown role %q", role)
	}
	if !IsSurface(surface) {
		return LevelNone, fmt.Errorf("roleconfig: unknown surface %q", surface)
	}
	return c.cells[cellKey{role: role, surface: surface}], nil
}

// Holds reports whether the role's level on the surface reaches want. A
// want outside the five levels is an error, never a silent admit: the
// route's level is read as a string from its annotation, and a mistyped
// one must not open every role through.
func (c *Config) Holds(role, surface string, want Level) (bool, error) {
	if _, ok := levelRank[want]; !ok {
		return false, fmt.Errorf("roleconfig: unknown level %q", string(want))
	}
	level, err := c.Level(role, surface)
	if err != nil {
		return false, err
	}
	return level.AtLeast(want), nil
}

// Cells returns the configuration's cells in document order. The slice is
// a copy; the caller cannot change the configuration.
func (c *Config) Cells() []Cell {
	return append([]Cell(nil), c.cellOrder...)
}

// ExceptionApprovers returns who approves the kind of exception, one
// document per approval (X4). An unknown kind is an error.
func (c *Config) ExceptionApprovers(kind ExceptionKind) ([]string, error) {
	roles, ok := c.lists.ExceptionApprovers[kind]
	if !ok {
		return nil, fmt.Errorf("roleconfig: unknown exception kind %q", string(kind))
	}
	return append([]string(nil), roles...), nil
}

// FloorClearersOwnBranch returns who clears the floor on its own quote
// price inside its own branch (X5, the quote flow only). The list is the
// data; ClearsOwnBranchFloor folds the manager_own_branch_floor_clearing
// setting in and is the rule the guards call.
func (c *Config) FloorClearersOwnBranch() []string {
	return append([]string(nil), c.lists.FloorClearersOwnBranch...)
}

// QuoteFloorBranchApprovers returns who decides a quote floor request
// inside its own branch, beside the floor list's dealer wide approvers
// (CP0-1 answer B: the branch's Sales manager). The list is the data; the
// decide path folds the branch reach in.
func (c *Config) QuoteFloorBranchApprovers() []string {
	return append([]string(nil), c.lists.QuoteFloorBranchApprovers...)
}

// ClearsOwnBranchFloor reports whether the role clears the floor on its
// own quote price inside its own branch, the setting folded in (X5, the
// quote flow only): off, every such price goes to leadership, whatever the
// list holds. The rule the guards call, in one place.
func (c *Config) ClearsOwnBranchFloor(role string) (bool, error) {
	if !IsRole(role) {
		return false, fmt.Errorf("roleconfig: unknown role %q", role)
	}
	if !c.settings.ManagerOwnBranchFloorClearing {
		return false, nil
	}
	for _, r := range c.lists.FloorClearersOwnBranch {
		if r == role {
			return true, nil
		}
	}
	return false, nil
}

// DeliveryDateCommitters returns who commits a delivery date (X11: the
// Yard Team commits none). The list is the data; CommitsDeliveryDate folds
// the general_manager_selling setting in and is the rule the guards call.
func (c *Config) DeliveryDateCommitters() []string {
	return append([]string(nil), c.lists.DeliveryDateCommitters...)
}

// CommitsDeliveryDate reports whether the role commits a delivery date,
// general_manager_selling folded in: the General Manager commits one
// exactly when the setting is on (row 457), the Yard Team never (X11). The
// rule the guards call, in one place.
func (c *Config) CommitsDeliveryDate(role string) (bool, error) {
	if !IsRole(role) {
		return false, fmt.Errorf("roleconfig: unknown role %q", role)
	}
	for _, r := range c.lists.DeliveryDateCommitters {
		if r == role {
			return true, nil
		}
	}
	return role == RoleGeneralManager && c.settings.GeneralManagerSelling, nil
}

// CostVisibility returns who sees cost and margin (X3).
func (c *Config) CostVisibility() []string {
	return append([]string(nil), c.settings.CostVisibility...)
}

// SeesCostAndMargin reports whether the role is on the cost and margin
// list (X3). An unknown role is false with an error.
func (c *Config) SeesCostAndMargin(role string) (bool, error) {
	if !IsRole(role) {
		return false, fmt.Errorf("roleconfig: unknown role %q", role)
	}
	for _, r := range c.settings.CostVisibility {
		if r == role {
			return true, nil
		}
	}
	return false, nil
}

// LowMarginReportReaders returns who reads the low margin report (the audit the managers scan, sorted
// by margin): the roles that clear a floor or decide one, and the roles above them, held to the cost
// and margin list. A role is a reader when it clears the floor on its own price in its own branch
// (ClearsOwnBranchFloor, the setting folded in), approves a quote floor request dealer wide (the
// floor exception approvers) or inside its own branch (the quote floor branch approvers), and is on
// the cost and margin list (X3): a margin beside a price is the cost in another form, so a role that
// may not see cost does not read the report whatever floor list it is on. Derived from the
// configuration's lists on every call, never held as a list of its own, so it cannot drift from them.
// In the role vocabulary's order.
func (c *Config) LowMarginReportReaders() []string {
	var out []string
	for _, role := range roleVocabulary {
		if ok, err := c.ReadsLowMarginReport(role); err == nil && ok {
			out = append(out, role)
		}
	}
	return out
}

// ReadsLowMarginReport reports whether the role reads the low margin report (LowMarginReportReaders
// states the rule). An unknown role is false with an error.
func (c *Config) ReadsLowMarginReport(role string) (bool, error) {
	if !IsRole(role) {
		return false, fmt.Errorf("roleconfig: unknown role %q", role)
	}
	sees, err := c.SeesCostAndMargin(role)
	if err != nil || !sees {
		return false, err
	}
	if clears, err := c.ClearsOwnBranchFloor(role); err == nil && clears {
		return true, nil
	}
	wide, err := c.ExceptionApprovers(ExceptionFloorQuote)
	if err != nil {
		return false, err
	}
	for _, r := range append(wide, c.QuoteFloorBranchApprovers()...) {
		if r == role {
			return true, nil
		}
	}
	return false, nil
}

// CreditAtTill reports whether the counter clerk sees the customer's
// balance, credit limit and headroom at the till (row 353).
func (c *Config) CreditAtTill() bool {
	return c.settings.CreditAtTill
}

// RefundAmountWithoutApprovalCents returns the refund amount without
// approval, in integer cents (250.00 held as 25000).
func (c *Config) RefundAmountWithoutApprovalCents() int64 {
	return c.settings.RefundAmountWithoutApprovalCents
}

// RefundRequiresApproval reports whether the sale's cumulative refunds at
// cumulativeCents ask for an approver under the dealer's configuration:
// false for every amount when the limit is off (the Dibbits ruling: the
// dealer set no refund approval threshold at all, refunds running from
// tens of dollars to thousands, so no refund ever needs an approver), and
// otherwise the amount comparison X7 names, at or under the amount asking
// nobody. The amount is the data; this folds the limit switch in and is
// the rule the guards call, in one place, so a second comparison that
// forgot the switch cannot exist.
func (c *Config) RefundRequiresApproval(cumulativeCents int64) bool {
	if !c.settings.RefundApprovalLimitEnabled {
		return false
	}
	return cumulativeCents > c.settings.RefundAmountWithoutApprovalCents
}

// RefundApprovers returns who approves a refund above the amount, of the
// sale's branch (X7).
func (c *Config) RefundApprovers() []string {
	return append([]string(nil), c.settings.RefundApprovers...)
}

// ApprovesRefundAboveAmount reports whether the role is on the refund
// approvers list. The caller still owes the branch check: the approval
// belongs to the sale's branch (X7). An unknown role is false with an
// error.
func (c *Config) ApprovesRefundAboveAmount(role string) (bool, error) {
	if !IsRole(role) {
		return false, fmt.Errorf("roleconfig: unknown role %q", role)
	}
	for _, r := range c.settings.RefundApprovers {
		if r == role {
			return true, nil
		}
	}
	return false, nil
}

// ManagerOwnBranchFloorClearing reports whether a manager's own below
// floor quote price goes through in its own branch, logged (X5, the quote
// flow only; off, every such price goes to leadership).
func (c *Config) ManagerOwnBranchFloorClearing() bool {
	return c.settings.ManagerOwnBranchFloorClearing
}

// PricingEditors returns who edits prices (X6).
func (c *Config) PricingEditors() []string {
	return append([]string(nil), c.settings.PricingEditors...)
}

// ShortageResolvers returns who resolves a shortage (X11).
func (c *Config) ShortageResolvers() []string {
	return append([]string(nil), c.settings.ShortageResolvers...)
}

// DispatchPolicySetters returns who sets the dispatch policy (X13).
func (c *Config) DispatchPolicySetters() []string {
	return append([]string(nil), c.settings.DispatchPolicySetters...)
}

// GeneralManagerSelling reports whether the General Manager sells and
// commits dates (row 457: off, Read on the till, quotes and dispatch).
func (c *Config) GeneralManagerSelling() bool {
	return c.settings.GeneralManagerSelling
}

// RefundApprovalWindow returns how old the approver's credential may be
// when the return carrying it commits (X7, row 482, plan 3.2: the window
// the configuration sets), typed as a duration from the configured
// seconds.
func (c *Config) RefundApprovalWindow() time.Duration {
	return time.Duration(c.settings.RefundApprovalWindowSeconds) * time.Second
}
