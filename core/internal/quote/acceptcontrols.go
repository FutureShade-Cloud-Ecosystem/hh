// acceptcontrols.go, DIB-491 (D-097, Aug-18 sync): accepting a quote more than once is a DEALER
// CHOICE, and the default is NO.
//
// THE GAP. DIB-393 made the acceptance guards real: a quote that is not SENT cannot be accepted, and
// the expiry re-check runs under the row lock. What those guards could not say is WHY a re-acceptance
// was refused, or WHERE the first acceptance went. A rep looking at "this quote is not awaiting
// acceptance (it is CONVERTED)" has to go hunting for the order that conversion minted, and a dealer
// who legitimately wants a repeat acceptance (a phased job re-ordering the same quoted set) had no
// door at all short of cloning the quote by hand, losing the paper trail in the process.
//
// THE RULING (D-097). Multi-accept is an org-level switch with a RESTRICTIVE default:
//
//	OFF (shipped): a second acceptance is refused with a message that NAMES the order the first
//	     acceptance became and points at both ways forward (open that order / turn the setting on).
//	ON:  a second acceptance is allowed, and EVERY acceptance carries provenance, which door, who,
//	     which numbered acceptance, so the related-documents view can show where each document came
//	     from. Provenance is written on the conversion's own transaction (repository.go), never
//	     best-effort.
//
// THE SWITCH follows the DIB-483 auto-hold convention (billing/autohold.go): admins own enablement
// knobs (the DIB-434 caveat), a read returns the effective value plus whether the write seam is
// wired, a write is audited in the house {field, before, after} shape IN BOTH DIRECTIONS, and
// re-asserting the current value is a no-op rather than an error. The one divergence is WHERE the
// admin gate lives: the auto-hold routes are registered under requireAdmin in main.go, while this
// module's RegisterStaffRoutes signature carries only requireStaff, so the Tier-1 GLOBAL check runs
// in the SERVICE (caller.IsAdmin()), exactly the "route guards are the coarse first line only"
// posture every other guard on this surface already takes. No main.go change, no park.
package quote

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// SettingAllowMultiAccept is the system_settings key (0162). Absent = false: the restrictive default
// holds even on a database that never ran the seed.
const SettingAllowMultiAccept = "allow_quote_multi_accept"

// ActMultiAcceptToggled is the audit action for a flip of the acceptance switch. A constant so a typo
// cannot split one action into two in the trail, the billing.ActAutoHoldToggled rule.
const ActMultiAcceptToggled = "QUOTE_MULTI_ACCEPT_TOGGLED"

// Provenance origins: which DOOR a conversion came through. Package constants, never caller text -
// the same discipline fulfillmentColumns applies to its splice.
const (
	OriginPortal  = "PORTAL"  // the customer's own portal session
	OriginCounter = "COUNTER" // a staff member at the counter (DIB-483's door)
)

// AlreadyAcceptedError is the refusal a repeat acceptance (or repeat conversion) gets while the
// multi-accept switch is OFF. It exists so the refusal can NAME the document the first acceptance
// became and the date it happened, "409" alone sends the person at the counter hunting.
//
// It satisfies errors.Is for BOTH ErrNotAcceptable and ErrInvalidTransition, deliberately:
//
//   - the public accept door maps ErrNotAcceptable to 409 with the error's own words
//     (share_handler.go), so this message reaches the customer unchanged;
//   - the portal BFF maps quote.ErrInvalidTransition to 409 (the ErrCreditBlocked precedent), so the
//     contractor door keeps its 409 rather than degrading to a 500.
//
// Both existing mappings therefore keep working with no caller changes, and the DIB-393 guard tests
// (which assert errors.Is(err, ErrNotAcceptable)) stay true, the refusal got more helpful, not
// different.
type AlreadyAcceptedError struct {
	// Status is the quote's current state (ACCEPTED or CONVERTED). Named in the message: the DIB-393
	// tests pin that a refusal names the state, and a support call starts from this string.
	Status string
	// OrderNo/At: the order the FIRST acceptance became and when it was minted. HasOrder false means
	// the quote is ACCEPTED but not yet converted, At is then the acceptance's own timestamp.
	OrderNo  string
	At       time.Time
	HasOrder bool
}

func (e *AlreadyAcceptedError) Error() string {
	if e.HasOrder {
		return fmt.Sprintf(
			"quote: this quote was already accepted as order %s on %s and is %s, open that order to keep working, "+
				"or have an administrator allow accepting a quote more than once in the acceptance settings",
			e.OrderNo, e.At.Format(dateLayout), e.Status)
	}
	return fmt.Sprintf(
		"quote: this quote was already %s on %s and is waiting to become an order, convert it at the counter, "+
			"or have an administrator allow accepting a quote more than once in the acceptance settings",
		e.Status, e.At.Format(dateLayout))
}

// Is makes the refusal wear both existing 409 identities (see the type comment).
func (e *AlreadyAcceptedError) Is(target error) bool {
	return target == ErrNotAcceptable || target == ErrInvalidTransition
}

// NotSettingsAdminError is the refusal a non-admin gets at the acceptance-settings surface. Typed so
// the message can name WHY (the NotSellingRoleError convention): a manager told only "403" assumes
// the screen is broken and tries again.
type NotSettingsAdminError struct{ RoleCode string }

func (e *NotSettingsAdminError) Error() string {
	if e.RoleCode == "" {
		return "quote: changing the acceptance settings is limited to administrators"
	}
	return fmt.Sprintf(
		"quote: the acceptance settings decide whether a quote can be accepted more than once, and changing them "+
			"is limited to administrators, %s cannot do it; ask a tech admin", e.RoleCode)
}

// ErrAcceptSettingsNotConfigured, the settings write seam is not wired (a store that predates it).
// A deliberate 503-shaped refusal, not a 500: an operator should learn this is a deploy problem.
var ErrAcceptSettingsNotConfigured = errors.New(
	"quote: the acceptance settings are not configured on this server")

// multiAcceptReader is the read seam: does this dealer allow accepting a quote more than once?
// *Repository satisfies it; a fake that omits it leaves the feature at its restrictive default.
type multiAcceptReader interface {
	AllowMultiAccept(ctx context.Context) (bool, error)
}

// multiAcceptWriter is the write seam, separate from the reader so "who may write this setting"
// stays a one-line answer (the billing autoHoldStore split).
type multiAcceptWriter interface {
	SetAllowMultiAccept(ctx context.Context, allow bool) error
}

// AcceptanceSettings is what the admin surface renders: the one boolean, plus whether the write seam
// is wired so the client can render read-only instead of offering a control that will fail.
type AcceptanceSettings struct {
	AllowMultiAccept bool `json:"allow_multi_accept"`
	SwitchWired      bool `json:"switch_wired"`
}

// multiAcceptOn answers the effective switch for the READ paths (public Acceptable flag, the mint
// guard, the CanConvert offer). FAIL RESTRICTIVE: a store that cannot answer, or predates the seam -
// leaves multi-accept OFF, which is the shipped behavior. The ENFORCEMENT paths do not use this
// helper: AcceptQuoteGuarded and Convert re-read the setting inside their own transactions
// (acceptcontrols_repository.go), for the same reason the expiry guard re-reads under the lock.
func (s *StaffService) multiAcceptOn(ctx context.Context) bool {
	r, ok := s.store.(multiAcceptReader)
	if !ok {
		if r, ok = s.shares.(multiAcceptReader); !ok {
			return false
		}
	}
	on, err := r.AllowMultiAccept(ctx)
	if err != nil {
		return false
	}
	return on
}

// settingsAdmin resolves the caller and applies the settings' role rule of
// CP0-5 answer A: the Sales manager (whoever holds Manage on the Quote
// Desk) and the General Manager. Reading the acceptance settings opens to
// both; saving follows the same rule through settingsWriter.
func (s *StaffService) settingsAdmin(ctx context.Context) (identity.StaffContext, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return identity.StaffContext{}, err
	}
	if caller.Status != identity.StatusActive {
		return identity.StaffContext{}, ErrForbidden
	}
	if !s.holdsQuoteSettingsAuthority(caller) {
		return identity.StaffContext{}, &NotSettingsAdminError{RoleCode: caller.RoleCode}
	}
	return caller, nil
}

// settingsWriter applies the write half of the settings' role rule (CP0-5
// answer A): a branch row is written by a caller holding Manage on the
// Quote Desk, in a branch it reaches; the NULL dealer default row is the
// General Manager's alone.
func (s *StaffService) settingsWriter(ctx context.Context, branchID string) (identity.StaffContext, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return identity.StaffContext{}, err
	}
	if caller.Status != identity.StatusActive {
		return identity.StaffContext{}, ErrForbidden
	}
	if !s.holdsQuoteSettingsAuthority(caller) {
		return identity.StaffContext{}, &NotSettingsAdminError{RoleCode: caller.RoleCode}
	}
	if branchID != "" {
		// A branch row: Manage on the Quote Desk (the Sales manager under
		// the decided default), in a branch the caller reaches. The General
		// Manager holds Read and writes the dealer default alone.
		if s.quoteDeskLevel(caller) != roleconfig.LevelManage || !caller.CanAccessBranch(branchID) {
			return identity.StaffContext{}, &NotSettingsAdminError{RoleCode: caller.RoleCode}
		}
		return caller, nil
	}
	// The dealer default row: the General Manager alone under the decided
	// default (a Manage caller keeps its branch rows).
	if canonicalRole(caller.RoleCode) != roleconfig.RoleGeneralManager {
		return identity.StaffContext{}, &NotSettingsAdminError{RoleCode: caller.RoleCode}
	}
	return caller, nil
}

// holdsQuoteSettingsAuthority answers whether the caller is one of the two
// roles CP0-5 answer A names: a caller holding Manage on the Quote Desk
// (the Sales manager) or the General Manager.
func (s *StaffService) holdsQuoteSettingsAuthority(caller identity.StaffContext) bool {
	if s.roles == nil {
		return false
	}
	if s.quoteDeskLevel(caller).AtLeast(roleconfig.LevelManage) {
		return true
	}
	return canonicalRole(caller.RoleCode) == roleconfig.RoleGeneralManager
}

// AcceptanceSettings reads the effective switch. Admin-gated like the write: the setting is an
// enablement knob, and the surface that renders it is the surface that flips it.
func (s *StaffService) AcceptanceSettings(ctx context.Context) (AcceptanceSettings, error) {
	if _, err := s.settingsAdmin(ctx); err != nil {
		return AcceptanceSettings{}, err
	}
	r, ok := s.store.(multiAcceptReader)
	if !ok {
		return AcceptanceSettings{}, ErrAcceptSettingsNotConfigured
	}
	on, err := r.AllowMultiAccept(ctx)
	if err != nil {
		return AcceptanceSettings{}, err
	}
	_, wired := s.store.(multiAcceptWriter)
	return AcceptanceSettings{AllowMultiAccept: on, SwitchWired: wired}, nil
}

// SetAllowMultiAccept flips the switch and returns the setting as it now stands.
//
// Every flip is audited, IN BOTH DIRECTIONS, in the house {field, before, after} shape (the union
// train 9 lesson: those keys are already allowlisted, and the OFF flip is the one somebody will need
// to explain later, when a duplicate order exists that the policy should have refused). EntityID is
// empty on purpose, this is an org-level setting, audit_log.entity_id is a uuid column, and a row
// that EXISTS with a NULL subject beats a searchable subject on a row that does not (autohold.go's
// policyEntityID lesson). Re-asserting the current value is not an error: a UI switch re-posts its
// state, and refusing the no-op turns a double-click into a red banner.
//
// The write's rule: the switch is DEALER WIDE (one row, no branch axis), so
// CP0-5 answer A gives it to the General Manager alone. settingsWriter's
// empty branch arm holds that, never settingsAdmin, which would let a Sales
// manager flip what every branch reads.
func (s *StaffService) SetAllowMultiAccept(ctx context.Context, allow bool, reason string) (AcceptanceSettings, error) {
	caller, err := s.settingsWriter(ctx, "")
	if err != nil {
		return AcceptanceSettings{}, err
	}
	r, ok := s.store.(multiAcceptReader)
	if !ok {
		return AcceptanceSettings{}, ErrAcceptSettingsNotConfigured
	}
	w, ok := s.store.(multiAcceptWriter)
	if !ok {
		return AcceptanceSettings{}, ErrAcceptSettingsNotConfigured
	}
	before, err := r.AllowMultiAccept(ctx)
	if err != nil {
		return AcceptanceSettings{}, err
	}
	if err := w.SetAllowMultiAccept(ctx, allow); err != nil {
		return AcceptanceSettings{}, err
	}
	after, err := r.AllowMultiAccept(ctx)
	if err != nil {
		return AcceptanceSettings{}, err
	}
	if reason == "" {
		reason = "acceptance settings"
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action: ActMultiAcceptToggled, EntityType: "quote_settings", EntityID: "",
			ActorID: caller.UserID, Purpose: "QUOTE_SETTINGS_CHANGE", Reason: reason,
			Extra: map[string]any{
				"field":  SettingAllowMultiAccept,
				"before": multiAcceptWord(before),
				"after":  multiAcceptWord(after),
			},
		})
	}
	return AcceptanceSettings{AllowMultiAccept: after, SwitchWired: true}, nil
}

// multiAcceptWord renders the switch state for the trail, words, not booleans, because the
// {field, before, after} shape carries strings everywhere else and "before: false" makes a reader
// work out what "false" was the state OF (the autoHoldWord rule).
func multiAcceptWord(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
