package quote

// dib491_acceptcontrols_test.go, DIB-491 unit cover for the acceptance controls (D-097).
//
// What is provable WITHOUT a database lives here: the refusal's words and its two 409 identities,
// the admin gate on the settings surface, the audited flip in both directions, and the HTTP mapping.
// The enforcement itself, the under-lock switch read inside AcceptQuoteGuarded and Convert, is
// deliberately NOT faked; it is proven against real Postgres in dib491_acceptcontrols_integration_
// test.go, because a fake that reimplements the guard is a test of the fake (the DIB-393 lesson).

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// dib491Store is fakeStaffStore + the DIB-491 seams (multiAcceptReader/Writer) + a stub conversion,
// so it satisfies the full staffStore contract the settings service runs over.
type dib491Store struct {
	*fakeStaffStore
	allow    bool
	allowErr error
	writes   []bool
	writeErr error
}

func (s *dib491Store) AllowMultiAccept(context.Context) (bool, error) { return s.allow, s.allowErr }
func (s *dib491Store) SetAllowMultiAccept(_ context.Context, v bool) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	s.allow = v
	s.writes = append(s.writes, v)
	return nil
}
func (s *dib491Store) Convert(context.Context, string, string, ConvertOptions) (ConvertResult, error) {
	return ConvertResult{}, nil
}

var _ staffStore = (*dib491Store)(nil)
var _ multiAcceptReader = (*dib491Store)(nil)
var _ multiAcceptWriter = (*dib491Store)(nil)

func dib491Svc(store *dib491Store, caller identity.StaffContext) (*StaffService, *recordingTxRecorder) {
	rec := &recordingTxRecorder{}
	svc := NewStaffService(store).
		WithRoleConfig(qTestRoleConfig()).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return caller, true })
	svc.WithAudit(rec)
	return svc, rec
}

func dib491Admin() identity.StaffContext {
	return identity.StaffContext{
		UserID: "u-admin", RoleCode: "GENERAL_MANAGER",
		Tier: identity.TierLeadership, Scope: identity.ScopeGlobal,
		Status: identity.StatusActive,
	}
}

// - the refusal -

// TestDIB491_AlreadyAcceptedError_NamesTheDocumentAndTheWayForward is the message contract: the
// refusal carries the order number, the date, the quote's state (the DIB-393 tests pin that a
// refusal names the state) and the way forward, never a bare "no".
func TestDIB491_AlreadyAcceptedError_NamesTheDocumentAndTheWayForward(t *testing.T) {
	at := time.Date(2026, 8, 12, 9, 30, 0, 0, time.UTC)

	withOrder := (&AlreadyAcceptedError{
		Status: "CONVERTED", OrderNo: "#SO-TRT-1041", At: at, HasOrder: true,
	}).Error()
	for _, want := range []string{"#SO-TRT-1041", "2026-08-12", "CONVERTED", "open that order", "acceptance settings"} {
		if !strings.Contains(withOrder, want) {
			t.Errorf("the with-order refusal must contain %q; got %q", want, withOrder)
		}
	}

	withoutOrder := (&AlreadyAcceptedError{Status: "ACCEPTED", At: at}).Error()
	for _, want := range []string{"ACCEPTED", "2026-08-12", "acceptance settings"} {
		if !strings.Contains(withoutOrder, want) {
			t.Errorf("the no-order refusal must contain %q; got %q", want, withoutOrder)
		}
	}
	if strings.Contains(withoutOrder, "order #") {
		t.Errorf("a quote with no order must not have one invented for the message: %q", withoutOrder)
	}
}

// TestDIB491_AlreadyAcceptedError_WearsBothExistingIdentities pins the compatibility contract that
// lets every shipped mapping keep working: the public accept door matches ErrNotAcceptable (409,
// err.Error() verbatim) and the portal BFF matches ErrInvalidTransition (409). Losing either one
// silently turns a helpful refusal into a 500 at that door.
func TestDIB491_AlreadyAcceptedError_WearsBothExistingIdentities(t *testing.T) {
	// A FIXED instant, not time.Now(): nothing here is a date comparison, and the dated-tests
	// tripwire (testaudit) rightly refuses a real clock in a file that also pins calendar dates.
	err := error(&AlreadyAcceptedError{Status: "CONVERTED", OrderNo: "#SO-1",
		At: time.Date(2026, 8, 12, 9, 30, 0, 0, time.UTC), HasOrder: true})
	if !errors.Is(err, ErrNotAcceptable) {
		t.Error("AlreadyAcceptedError must satisfy errors.Is(ErrNotAcceptable), the public accept door's 409 depends on it")
	}
	if !errors.Is(err, ErrInvalidTransition) {
		t.Error("AlreadyAcceptedError must satisfy errors.Is(ErrInvalidTransition), the portal BFF's 409 depends on it")
	}
	if errors.Is(err, ErrNotFound) {
		t.Error("AlreadyAcceptedError must not leak into unrelated identities")
	}
}

// - the settings surface -

// TestDIB491_Settings_AdminGate: the Tier-1 GLOBAL gate runs in the SERVICE (the route carries only
// requireStaff). A frontline seller and a branch manager are refused BY NAME; an admin passes.
func TestDIB491_Settings_AdminGate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		caller identity.StaffContext
	}{
		{"frontline seller", sellerCtx("INSIDE_SALES")},
		{"branch manager", identity.StaffContext{
			UserID: "u-mgr", RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager,
			Scope: "BRANCH", Status: identity.StatusActive, Branches: map[string]bool{qBranch: true},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := dib491Svc(&dib491Store{fakeStaffStore: newFakeStaffStore("SENT")}, tc.caller)
			var notAdmin *NotSettingsAdminError
			if _, err := svc.AcceptanceSettings(context.Background()); !errors.As(err, &notAdmin) {
				t.Fatalf("read: want NotSettingsAdminError, got %v", err)
			}
			// The refusal names who may act, in plain words, and never the refused caller's own role code.
			const want = "quote: only a sales manager or the general manager changes the quote settings"
			if notAdmin.Error() != want {
				t.Errorf("the read refusal = %q, want %q", notAdmin.Error(), want)
			}
			if strings.Contains(notAdmin.Error(), tc.caller.RoleCode) || strings.Contains(strings.ToLower(notAdmin.Error()), "tech admin") {
				t.Errorf("the refusal must not carry the role code or tech admin words: %q", notAdmin.Error())
			}
			if _, err := svc.SetAllowMultiAccept(context.Background(), true, ""); !errors.As(err, &notAdmin) {
				t.Fatalf("write: want NotSettingsAdminError, got %v", err)
			}
			if notAdmin.Error() != want {
				t.Errorf("the write refusal = %q, want %q", notAdmin.Error(), want)
			}
		})
	}

	svc, _ := dib491Svc(&dib491Store{fakeStaffStore: newFakeStaffStore("SENT")}, dib491Admin())
	got, err := svc.AcceptanceSettings(context.Background())
	if err != nil {
		t.Fatalf("admin read: %v", err)
	}
	if got.AllowMultiAccept {
		t.Error("the default must be OFF (D-097: restrictive default)")
	}
	if !got.SwitchWired {
		t.Error("the store carries the write seam, switch_wired must say so")
	}
}

// TestDIB491_Settings_FlipIsAuditedBothDirections: the trail is written in the house
// {field, before, after} shape for ON and for OFF, the OFF flip is the one somebody will need to
// explain later. This is a mutation anchor: drop the audit call in SetAllowMultiAccept and this dies.
func TestDIB491_Settings_FlipIsAuditedBothDirections(t *testing.T) {
	store := &dib491Store{fakeStaffStore: newFakeStaffStore("SENT")}
	svc, rec := dib491Svc(store, dib491Admin())
	ctx := context.Background()

	on, err := svc.SetAllowMultiAccept(ctx, true, "phased jobs pilot")
	if err != nil {
		t.Fatalf("flip on: %v", err)
	}
	if !on.AllowMultiAccept {
		t.Fatal("the flip must land")
	}
	if _, err := svc.SetAllowMultiAccept(ctx, false, ""); err != nil {
		t.Fatalf("flip off: %v", err)
	}
	if len(store.writes) != 2 || store.writes[0] != true || store.writes[1] != false {
		t.Fatalf("store writes = %v, want [true false]", store.writes)
	}
	if len(rec.records) != 2 {
		t.Fatalf("audit rows = %d, want 2, BOTH directions are audited", len(rec.records))
	}
	for i, wantPair := range [][2]string{{"off", "on"}, {"on", "off"}} {
		a := rec.records[i]
		if a.Action != ActMultiAcceptToggled {
			t.Errorf("row %d action = %q, want %q", i, a.Action, ActMultiAcceptToggled)
		}
		if a.Extra["field"] != SettingAllowMultiAccept ||
			a.Extra["before"] != wantPair[0] || a.Extra["after"] != wantPair[1] {
			t.Errorf("row %d must carry the house {field,before,after} shape with %v; got %v",
				i, wantPair, a.Extra)
		}
		if a.ActorID != "u-admin" {
			t.Errorf("row %d actor = %q, want the flipping admin", i, a.ActorID)
		}
	}
}

// - the offer stays honest -

// TestDIB491_CanConvert_ReappearsOnlyWithTheSwitch: the CONVERTED offer is the conjunction the write
// path enforces, switch ON and a selling role, so a button that appears is a button that works.
func TestDIB491_CanConvert_ReappearsOnlyWithTheSwitch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status string
		allow  bool
		caller identity.StaffContext
		want   bool
	}{
		{"converted, switch off, seller", "CONVERTED", false, sellerCtx("INSIDE_SALES"), false},
		{"converted, switch on, seller", "CONVERTED", true, sellerCtx("INSIDE_SALES"), true},
		{"converted, switch on, yard", "CONVERTED", true, sellerCtx("YARD_DRIVER"), false},
		{"accepted, switch off, seller", "ACCEPTED", false, sellerCtx("INSIDE_SALES"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &dib491Store{fakeStaffStore: newFakeStaffStore(tc.status), allow: tc.allow}
			svc, _ := dib491Svc(store, tc.caller)
			view, err := svc.StaffQuoteRead(context.Background(), qOrg)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if view.CanConvert != tc.want {
				t.Errorf("CanConvert = %v, want %v", view.CanConvert, tc.want)
			}
		})
	}
}

// - the HTTP mapping -

// TestDIB491_WriteErr_KeepsTheDocumentInTheAnswer: the typed case sits ABOVE the generic
// ErrInvalidTransition case, same ordering rule, same reason, as the credit refusal (M3-G7B). Move
// it below and the order number is thrown away while everything still compiles.
func TestDIB491_WriteErr_KeepsTheDocumentInTheAnswer(t *testing.T) {
	h := NewStaffHandler(nil)

	w := httptest.NewRecorder()
	h.writeErr(w, &AlreadyAcceptedError{
		Status: "CONVERTED", OrderNo: "#SO-TRT-1041",
		At: time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC), HasOrder: true,
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if !strings.Contains(body.Error, "#SO-TRT-1041") {
		t.Errorf("the 409 must keep the order number; got %q", body.Error)
	}

	w = httptest.NewRecorder()
	h.writeErr(w, &NotSettingsAdminError{RoleCode: "INSIDE_SALES"})
	if w.Code != http.StatusForbidden {
		t.Errorf("NotSettingsAdminError = %d, want 403", w.Code)
	}

	w = httptest.NewRecorder()
	h.writeErr(w, ErrLineageNotConfigured)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("ErrLineageNotConfigured = %d, want 503", w.Code)
	}
}

// TestDIB491_SettingsWrite_RefusesAnAbsentBoolean: the *bool contract, a body that omits the field
// is a 400, never "false by accident" (the setAutoHold rule; this flag mints duplicate orders).
func TestDIB491_SettingsWrite_RefusesAnAbsentBoolean(t *testing.T) {
	store := &dib491Store{fakeStaffStore: newFakeStaffStore("SENT")}
	svc, _ := dib491Svc(store, dib491Admin())
	h := NewStaffHandler(svc)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/quotes/settings/acceptance",
		strings.NewReader(`{"reason":"just curious"}`))
	w := httptest.NewRecorder()
	h.setAcceptanceSettings(w, req, guards.StaffCaller{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("an absent allow_multi_accept must be a 400; got %d (%s)", w.Code, w.Body.String())
	}
	if len(store.writes) != 0 {
		t.Fatalf("a refused body must write nothing; wrote %v", store.writes)
	}
}

// - round 14 (a): the settings grant -

// TestDIB491_SettingsGrant_AnswersPerRole: the three booleans use the same predicates the
// refusing routes use, so the answer cannot drift from the refusal. INSIDE_SALES gets nothing
// (frontline, not LevelManage on the quote-desk surface). SALES_MANAGER gets acceptance read
// and policy edit (LevelManage on quote-desk) but not scope rules (not Tier-1 GLOBAL).
// GENERAL_MANAGER and the platform SUPER_ADMIN get all three (Tier-1 GLOBAL).
func TestDIB491_SettingsGrant_AnswersPerRole(t *testing.T) {
	for _, tc := range []struct {
		name           string
		caller         identity.StaffContext
		wantAcceptance bool
		wantScopeRules bool
		wantPolicyEdit bool
	}{
		{
			name: "Inside Sales: no grant",
			caller: identity.StaffContext{
				UserID: "u-sales", RoleCode: "INSIDE_SALES",
				Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
				Status: identity.StatusActive, Branches: map[string]bool{"b-1": true},
			},
			wantAcceptance: false, wantScopeRules: false, wantPolicyEdit: false,
		},
		{
			name: "Sales Manager: acceptance + policy edit, no scope rules",
			caller: identity.StaffContext{
				UserID: "u-sm", RoleCode: "SALES_MANAGER",
				Tier: identity.TierManager, Scope: identity.ScopeBranch,
				Status: identity.StatusActive, Branches: map[string]bool{"b-1": true},
			},
			wantAcceptance: true, wantScopeRules: false, wantPolicyEdit: true,
		},
		{
			name: "General Manager: all three",
			caller: identity.StaffContext{
				UserID: "u-gm", RoleCode: "GENERAL_MANAGER",
				Tier: identity.TierLeadership, Scope: identity.ScopeGlobal,
				Status: identity.StatusActive,
			},
			wantAcceptance: true, wantScopeRules: true, wantPolicyEdit: true,
		},
		{
			// Financial Controller is Tier-1 GLOBAL (IsAdmin: scope rules) but the
			// acceptance and policy grants come from holdsQuoteSettingsAuthority which
			// requires LevelManage on Quote Desk or canonical General Manager -- FC is neither.
			name: "Financial Controller: scope rules only (IsAdmin, not QuoteSettingsAuthority)",
			caller: identity.StaffContext{
				UserID: "u-fc", RoleCode: "FINANCIAL_CONTROLLER",
				Tier: identity.TierLeadership, Scope: identity.ScopeGlobal,
				Status: identity.StatusActive,
			},
			wantAcceptance: false, wantScopeRules: true, wantPolicyEdit: false,
		},
		{
			// SUPER_ADMIN is Tier-1 GLOBAL so it passes IsAdmin (scope rules read) but
			// holdsQuoteSettingsAuthority checks for LevelManage on Quote Desk or
			// canonicalRole General Manager -- neither applies to the platform role --
			// so acceptance read and policy edit are false.
			name: "platform SUPER_ADMIN: scope rules only (IsAdmin, not QuoteSettingsAuthority)",
			caller: identity.StaffContext{
				UserID: "u-super", RoleCode: "SUPER_ADMIN",
				Tier: identity.TierLeadership, Scope: identity.ScopeGlobal,
				Status: identity.StatusActive,
			},
			wantAcceptance: false, wantScopeRules: true, wantPolicyEdit: false,
		},
		{
			name: "Branch Manager: no grant (TierManager but not LevelManage on quote-desk surface)",
			caller: identity.StaffContext{
				UserID: "u-bm", RoleCode: "BRANCH_MANAGER",
				Tier: identity.TierManager, Scope: identity.ScopeBranch,
				Status: identity.StatusActive, Branches: map[string]bool{"b-1": true},
			},
			wantAcceptance: false, wantScopeRules: false, wantPolicyEdit: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := dib491Svc(&dib491Store{fakeStaffStore: newFakeStaffStore("SENT")}, tc.caller)
			got, err := svc.SettingsGrant(context.Background())
			if err != nil {
				t.Fatalf("SettingsGrant: %v", err)
			}
			if got.CanReadAcceptance != tc.wantAcceptance {
				t.Errorf("CanReadAcceptance = %v, want %v", got.CanReadAcceptance, tc.wantAcceptance)
			}
			if got.CanReadScopeRules != tc.wantScopeRules {
				t.Errorf("CanReadScopeRules = %v, want %v", got.CanReadScopeRules, tc.wantScopeRules)
			}
			if got.CanEditPolicy != tc.wantPolicyEdit {
				t.Errorf("CanEditPolicy = %v, want %v", got.CanEditPolicy, tc.wantPolicyEdit)
			}
			// Each answer is also asserted against the refusing service route's own answer for the same caller.
			_, readErr := svc.AcceptanceSettings(context.Background())
			var notAdmin *NotSettingsAdminError
			if refused := errors.As(readErr, &notAdmin); refused == got.CanReadAcceptance {
				t.Errorf("CanReadAcceptance = %v but the acceptance read answered %v", got.CanReadAcceptance, readErr)
			}
			if !got.CanReadAcceptance && readErr != nil && !errors.As(readErr, &notAdmin) {
				t.Errorf("a refused acceptance read must be the typed refusal, got %v", readErr)
			}
			_, writeErr := svc.settingsWriter(context.Background(), "")
			if allowed := writeErr == nil; allowed && !got.CanEditPolicy {
				t.Errorf("the policy write gate admitted a caller the grant refuses: %v", tc.name)
			}
			if got.CanEditPolicy && errors.As(writeErr, &notAdmin) && !notAdmin.DealerDefault {
				t.Errorf("a caller the grant admits to the policy write was refused wrongly: %v", writeErr)
			}
		})
	}
}

// TestDIB491_SettingsGrant_RefusesInactiveCaller: the grant returns ErrForbidden for an inactive caller.
func TestDIB491_SettingsGrant_RefusesInactiveCaller(t *testing.T) {
	inactive := identity.StaffContext{
		UserID: "u-inactive", RoleCode: "GENERAL_MANAGER",
		Tier: identity.TierLeadership, Scope: identity.ScopeGlobal,
		Status: "DISABLED", // not StatusActive
	}
	svc, _ := dib491Svc(&dib491Store{fakeStaffStore: newFakeStaffStore("SENT")}, inactive)
	_, err := svc.SettingsGrant(context.Background())
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("SettingsGrant for inactive: want ErrForbidden, got %v", err)
	}
}
