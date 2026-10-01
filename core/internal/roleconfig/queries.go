package roleconfig

import "fmt"

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

// Holds reports whether the role's level on the surface reaches want.
func (c *Config) Holds(role, surface string, want Level) (bool, error) {
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
// price inside its own branch, while ManagerOwnBranchFloorClearing holds
// (X5, the quote flow only).
func (c *Config) FloorClearersOwnBranch() []string {
	return append([]string(nil), c.lists.FloorClearersOwnBranch...)
}

// DeliveryDateCommitters returns who commits a delivery date (X11: the
// Yard Team commits none). GeneralManagerSelling adds the General Manager
// to this list when it is on; the list here is the configured one.
func (c *Config) DeliveryDateCommitters() []string {
	return append([]string(nil), c.lists.DeliveryDateCommitters...)
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
