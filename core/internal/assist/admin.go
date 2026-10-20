package assist

// admin.go, the tech-admin surface's server half: who spent what this month, and the limits.
//
// THE ONE JUDGEMENT IN THIS FILE. The spend report lists EVERY active staff member, including the
// ones who have never opened the assistant, with zeros. The ledger cannot produce those rows, a
// person with no exchanges has no rows, so they are filled in from the staff list.
//
// That is deliberate, and it is the difference between a report that answers the client's question
// and one that does not. The question is "is anyone using this", and a list containing only the
// four people who did tells you nothing about the forty who did not. A zero row is a fact.
//
// BUDGETS ARE RESOLVED PER PERSON, NOT LOOKED UP PER PERSON. Two map reads and Resolve(), so the
// admin surface and the enforcement path (service.Ask) get their number from the SAME function.
// Anything else and the screen could promise headroom the enforcer would refuse.

import (
	"context"
	"sort"
	"time"
)

// StaffLister is the seam for "who are the people". Declared as an interface for the usual reason -
// the tests need a spend report without a database, and satisfied by *Repository.
type StaffLister interface {
	Staff(ctx context.Context) ([]StaffRow, error)
}

// WithStaff attaches the staff list used by the admin spend report. Optional: without it the report
// falls back to ledger-only rows, which is degraded (no zero rows) but not wrong.
func (s *Service) WithStaff(l StaffLister) *Service { s.staff = l; return s }

// SpendReport is every active person's current-month usage against their resolved budget, biggest
// spender first.
//
// Sorted by tokens then exchanges then name: the admin opens this to find out who is driving the
// bill, and the answer should be the first row. Name breaks the tie so the order is STABLE, a
// report whose rows shuffle between refreshes is one nobody can compare against yesterday's.
func (s *Service) SpendReport(ctx context.Context) ([]UserSpend, error) {
	from, to, err := s.monthWindow(ctx)
	if err != nil {
		return nil, err
	}

	spend := map[string]Spend{}
	if s.spend != nil {
		var err error
		if spend, err = s.spend.UsersMonth(ctx, from, to); err != nil {
			return nil, err
		}
	} else if s.ledger != nil {
		var err error
		if spend, err = s.ledger.SpendAll(ctx, from, to); err != nil {
			return nil, err
		}
	}

	roleBudgets := map[string]int{}
	userBudgets := map[string]int{}
	if s.limits != nil {
		var err error
		if roleBudgets, err = s.limits.RoleBudgets(ctx); err != nil {
			return nil, err
		}
		if userBudgets, err = s.limits.UserBudgets(ctx); err != nil {
			return nil, err
		}
	}

	var staff []StaffRow
	if s.staff != nil {
		var err error
		if staff, err = s.staff.Staff(ctx); err != nil {
			return nil, err
		}
	} else {
		// No staff list wired: report what the ledger knows and nothing more. Names are unavailable,
		// so the id stands in rather than an empty cell that reads as a bug.
		for id := range spend {
			staff = append(staff, StaffRow{UserID: id, DisplayName: id})
		}
	}

	out := make([]UserSpend, 0, len(staff))
	for _, st := range staff {
		var uo, rd *int
		if v, ok := userBudgets[st.UserID]; ok {
			uo = &v
		}
		if v, ok := roleBudgets[st.RoleCode]; ok {
			rd = &v
		}
		out = append(out, UserSpend{
			UserID:      st.UserID,
			DisplayName: st.DisplayName,
			Email:       st.Email,
			RoleCode:    st.RoleCode,
			Spend:       spend[st.UserID],
			Budget:      Resolve(uo, rd),
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Total() != out[j].Total() {
			return out[i].Total() > out[j].Total()
		}
		if out[i].Exchanges != out[j].Exchanges {
			return out[i].Exchanges > out[j].Exchanges
		}
		return out[i].DisplayName < out[j].DisplayName
	})
	return out, nil
}

// LimitsView is the limits editor's whole payload: the role defaults, the per-user overrides, and
// the code default that applies when neither exists.
type LimitsView struct {
	RoleBudgets   map[string]int `json:"role_budgets"`
	UserOverrides map[string]int `json:"user_overrides"`
	DefaultBudget int            `json:"default_budget"`
}

// Limits reads the whole limits picture for the admin editor.
func (s *Service) Limits(ctx context.Context) (LimitsView, error) {
	v := LimitsView{RoleBudgets: map[string]int{}, UserOverrides: map[string]int{}, DefaultBudget: DefaultBudget}
	if s.limits == nil {
		return v, nil
	}
	rb, err := s.limits.RoleBudgets(ctx)
	if err != nil {
		return LimitsView{}, err
	}
	ub, err := s.limits.UserBudgets(ctx)
	if err != nil {
		return LimitsView{}, err
	}
	v.RoleBudgets, v.UserOverrides = rb, ub
	return v, nil
}

// SetRoleBudget / SetUserBudget / ResetUserBudget are the three writes the admin surface makes.
// Negative budgets are refused here as well as by the CHECK constraint: the surface deserves a
// sentence, not a constraint violation.

func (s *Service) SetRoleBudget(ctx context.Context, roleCode string, tokens int, actorID string) error {
	if s.limits == nil {
		return errUnwired("limit configuration")
	}
	if tokens < 0 {
		return badArg("A budget can't be negative. Use 0 to switch the assistant off for this role.")
	}
	return s.limits.SetRoleBudget(ctx, roleCode, tokens, actorID)
}

func (s *Service) SetUserBudget(ctx context.Context, userID string, tokens int, actorID string) error {
	if s.limits == nil {
		return errUnwired("limit configuration")
	}
	if tokens < 0 {
		return badArg("A budget can't be negative. Use 0 to switch the assistant off for this person.")
	}
	return s.limits.SetUserBudget(ctx, userID, tokens, actorID)
}

// ResetUserBudget is "reset to role", the DELETE, not a copy. See repository.go.
func (s *Service) ResetUserBudget(ctx context.Context, userID string) error {
	if s.limits == nil {
		return errUnwired("limit configuration")
	}
	return s.limits.ClearUserBudget(ctx, userID)
}

// SetSetting writes one config key after checking it is one this package knows.
//
// An unknown key is refused rather than stored. A settings table that accepts anything becomes a
// junk drawer, and the first symptom is an admin editing 'chat_enable' for twenty minutes.
func (s *Service) SetSetting(ctx context.Context, key, value, actorID string) error {
	if s.settings == nil {
		return errUnwired("assistant settings")
	}
	switch key {
	case KeyProvider:
		if value != ProviderStub && value != ProviderAnthropic {
			return badArg("Provider must be %q or %q.", ProviderStub, ProviderAnthropic)
		}
	case KeyModel:
		if value == "" {
			return badArg("The model name can't be empty.")
		}
	case KeyChatEnabled, KeySpendVisibleToNonAdmin:
		if !isTrue(value) && !isFalse(value) {
			return badArg("%s is a yes/no setting, use true or false.", key)
		}
	case KeyRateRequests, KeyRateWindowSeconds:
		if !validRateSetting(key, value) {
			return badArg("%s is out of range.", key)
		}
	default:
		return badArg("%q isn't an assistant setting.", key)
	}
	return s.settings.SetSetting(ctx, key, value, actorID)
}

// MySpendVisible answers "may THIS caller see their own spend?", the DIB-434 house pattern, where
// whether a number is visible to non-admins is itself a tech-admin toggle rather than a decision
// baked into a template.
//
// An admin always sees it: they are the ones who set the toggle, and hiding the number from the
// person who controls it would be theatre.
func (s *Service) MySpendVisible(ctx context.Context, c Caller) bool {
	if c.IsAdmin {
		return true
	}
	return s.Settings(ctx).SpendVisibleToNonAdmin
}

// asOfMonth is a small helper the handler uses to label the report with the month it covers.
func (s *Service) asOfMonth() time.Time { from, _ := MonthWindow(s.now()); return from }
