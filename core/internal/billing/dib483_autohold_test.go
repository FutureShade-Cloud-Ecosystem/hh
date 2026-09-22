package billing

// dib483_autohold_test.go, M3-G7B-NO-HOLD-SWITCH.
//
// 0104 shipped ar_credit_policy.auto_hold_enabled FALSE and said so out loud: "merging this migration
// changes nobody's ability to transact until an operator turns it on." The gate's finding is that no
// operator could. The column had two READERS and no WRITER, the safety flag was a one-way door in the
// wrong direction, off with no handle on the inside.
//
// What is pinned here is as much about the SHAPE of the surface as its existence:
//
//   - the switch writes exactly one column, and the thresholds come back read-only;
//   - the flip is audited in BOTH directions, with the direction recorded;
//   - an absent `enabled` in the body is refused, never read as false;
//   - the setting an admin reads is the same policy the nightly sweep obeys.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// -- a store that can also WRITE the switch -------------------------------------------------------

type autoHoldFakeStore struct {
	fakeCreditStore
	writes   []autoHoldWrite
	writeErr error
}

type autoHoldWrite struct {
	branchID string
	enabled  bool
}

func (f *autoHoldFakeStore) SetAutoHoldEnabled(_ context.Context, branchID string, enabled bool) error {
	f.writes = append(f.writes, autoHoldWrite{branchID: branchID, enabled: enabled})
	if f.writeErr != nil {
		return f.writeErr
	}
	f.policy.AutoHoldEnabled = enabled
	return nil
}

// recordingAudit captures the trail so a flip's provenance can be asserted rather than assumed.
type recordingAudit struct{ entries []audit.SensitiveAccess }

func (r *recordingAudit) Record(_ context.Context, a audit.SensitiveAccess) error {
	r.entries = append(r.entries, a)
	return nil
}

func shippedPolicy() CreditPolicy {
	// exactly what 0104's org-default INSERT writes.
	return CreditPolicy{
		AgingBucketDays: 60, AgingThresholdCents: 0, AgingHoldEnabled: true,
		LimitHoldEnabled: true, LimitToleranceCents: 0, CODEnforced: true,
		AutoHoldEnabled: false,
	}
}

// -- the switch exists and is wired by construction -----------------------------------------------

// TestAutoHold_SwitchIsWiredByConstruction: a service built over a store that CAN write the column
// gets the switch, with no second wiring line at the call site. This is what makes the control live in
// the shipped composition root without editing it.
func TestAutoHold_SwitchIsWiredByConstruction(t *testing.T) {
	writable := &autoHoldFakeStore{fakeCreditStore: fakeCreditStore{policy: shippedPolicy()}}
	if !NewCreditService(writable).AutoHoldSwitchWired() {
		t.Fatal("a store that satisfies the write seam must bind it, otherwise the switch is dead in " +
			"production unless somebody remembers a wiring line")
	}

	// a read-only store leaves the switch unwired, and the surface degrades rather than failing.
	readOnly := &fakeCreditStore{policy: shippedPolicy()}
	svc := NewCreditService(readOnly)
	if svc.AutoHoldSwitchWired() {
		t.Fatal("a read-only store must not report a writable switch")
	}
	if _, err := svc.SetAutoHold(context.Background(), "", true, "u1", ""); !errors.Is(err, ErrAutoHoldNotConfigured) {
		t.Fatalf("unwired write = %v, want ErrAutoHoldNotConfigured (a DEPLOY fault, named)", err)
	}
}

// TestAutoHold_ReadsTheShippedStateAndItsThresholds is the "before" the walk shows: the flag is off,
// as 0104 shipped it, and the thresholds it WOULD apply are visible and marked not-editable.
func TestAutoHold_ReadsTheShippedStateAndItsThresholds(t *testing.T) {
	store := &autoHoldFakeStore{fakeCreditStore: fakeCreditStore{policy: shippedPolicy()}}
	got, err := NewCreditService(store).AutoHold(context.Background(), "")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Enabled {
		t.Fatal("the shipped state is OFF, this read disagrees with migration 0104")
	}
	if got.Scope != "ORG" {
		t.Fatalf("scope = %q, want ORG (no branch row answered)", got.Scope)
	}
	if got.AgingBucketDays != 60 || !got.AgingHoldEnabled || !got.LimitHoldEnabled || !got.CODEnforced {
		t.Fatalf("the thresholds an admin needs to SEE before flipping are wrong: %+v", got)
	}
	if got.ThresholdsEditable {
		t.Fatal("thresholds must be reported NOT editable, those values are client-set, and a screen " +
			"that lets an admin retype them is a screen that will eventually hold the whole book")
	}
}

// TestAutoHold_FlipWritesOneColumnAndIsAudited, the flip itself, and its record.
func TestAutoHold_FlipWritesOneColumnAndIsAudited(t *testing.T) {
	store := &autoHoldFakeStore{fakeCreditStore: fakeCreditStore{policy: shippedPolicy()}}
	trail := &recordingAudit{}
	svc := NewCreditService(store).WithAudit(trail).
		WithClock(func() time.Time { return time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC) })

	after, err := svc.SetAutoHold(context.Background(), "", true, "u-admin", "go-live: AR sign-off")
	if err != nil {
		t.Fatalf("flip on: %v", err)
	}
	if !after.Enabled {
		t.Fatal("the switch did not come back on")
	}
	if len(store.writes) != 1 || store.writes[0].enabled != true || store.writes[0].branchID != "" {
		t.Fatalf("writes = %+v, want exactly one org-scoped write of true", store.writes)
	}
	if len(trail.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(trail.entries))
	}
	e := trail.entries[0]
	if e.Action != ActAutoHoldToggled {
		t.Fatalf("audit action = %q, want %q", e.Action, ActAutoHoldToggled)
	}
	// UNION TRAIN 9: this used to demand EntityID == "ORG" on the reasoning that a subject of "" is a
	// subject nobody can search for. The reasoning was right and the value was wrong: audit_log's
	// entity_id is a uuid column (`NULLIF($3,'')::uuid`), so "ORG" is not an unsearchable subject, it
	// is a FAILED INSERT, and CreditService.record swallows the error, so the org-scoped flip landed
	// no row at all. The org scope now leaves the subject empty (NULL in the column) and names itself
	// in changes.status, which is allowlisted and therefore actually persists. A BRANCH flip still
	// carries its uuid, and that half is asserted below.
	if e.EntityType != "ar_credit_policy" || e.EntityID != "" {
		t.Fatalf("audit subject = %s/%q, want ar_credit_policy with an EMPTY subject at org scope, "+
			"entity_id is a uuid column and a non-uuid there is a silently refused INSERT",
			e.EntityType, e.EntityID)
	}
	if e.Extra["status"] == nil || e.Extra["status"] == "" {
		t.Fatalf("the row does not name the scope it flipped: %+v", e.Extra)
	}
	if e.ActorID != "u-admin" {
		t.Fatalf("audit actor = %q", e.ActorID)
	}
	if e.Reason != "go-live: AR sign-off" {
		t.Fatalf("audit reason = %q", e.Reason)
	}
	// UNION TRAIN 9: from/to are not allowlisted and never persisted. See the ON/OFF pair below and
	// TestAutoHold_TrailKeysSurviveAuditsAllowlist.
	if e.Extra["field"] != "auto_hold_enabled" || e.Extra["before"] != "off" || e.Extra["after"] != "on" {
		t.Fatalf("the audit row does not record the DIRECTION: %+v", e.Extra)
	}
}

// TestAutoHold_TurningItOffIsAuditedToo. The off-flip is the one somebody will have to explain later,
// when an account that should have been held wasn't. A trail that only records the scary direction has
// a hole in exactly the shape of the question it will be asked.
func TestAutoHold_TurningItOffIsAuditedToo(t *testing.T) {
	pol := shippedPolicy()
	pol.AutoHoldEnabled = true
	store := &autoHoldFakeStore{fakeCreditStore: fakeCreditStore{policy: pol}}
	trail := &recordingAudit{}

	if _, err := NewCreditService(store).WithAudit(trail).
		SetAutoHold(context.Background(), "", false, "u-admin", "pausing during the cutover"); err != nil {
		t.Fatalf("flip off: %v", err)
	}
	if len(trail.entries) != 1 {
		t.Fatalf("turning the machine OFF recorded %d entries, want 1", len(trail.entries))
	}
	// UNION TRAIN 9: this used to read Extra["from"]/["to"]. Those keys are not on audit's
	// deny-by-default allowlist (allowedExtraKeys, audit/service.go:303), so they were dropped before
	// persistence, this test passed against the fake below while the real trail carried no direction
	// at all. The row now uses the house {field, before, after} shape, which IS allowlisted, and this
	// assertion is written in the vocabulary that survives to the database.
	if e := trail.entries[0]; e.Extra["before"] != "on" || e.Extra["after"] != "off" {
		t.Fatalf("the off-flip's direction was not recorded: %+v", e.Extra)
	}
}

// TestAutoHold_ReassertingIsNotAnError, a UI re-posts its switch state; a double-click must not be a
// red banner. The no-op is still recorded, and is still legible as one.
//
// UNION TRAIN 9: it used to be flagged by a dedicated Extra["no_op"] boolean, which audit's allowlist
// dropped, so the flag existed only in this test's fake. A no-op is now read off the row the same way
// a reader would read it, before and after are the same word, which is a DERIVED fact rather than an
// asserted one, and therefore cannot disagree with the direction recorded beside it.
func TestAutoHold_ReassertingIsNotAnError(t *testing.T) {
	store := &autoHoldFakeStore{fakeCreditStore: fakeCreditStore{policy: shippedPolicy()}}
	trail := &recordingAudit{}
	svc := NewCreditService(store).WithAudit(trail)

	if _, err := svc.SetAutoHold(context.Background(), "", false, "u-admin", ""); err != nil {
		t.Fatalf("re-asserting the current value errored: %v", err)
	}
	if len(trail.entries) != 1 {
		t.Fatalf("a re-assertion recorded %d entries, want 1, a no-op is still a decision somebody made",
			len(trail.entries))
	}
	e := trail.entries[0]
	if e.Extra["before"] != "off" || e.Extra["after"] != "off" {
		t.Fatalf("a no-op flip is not legible as one: %+v", e.Extra)
	}
}

// TestAutoHold_TrailKeysSurviveAuditsAllowlist is the test whose absence let the whole class through.
//
// audit.Service.Record is deny-by-default: an Extra key that is not in allowedExtraKeys is DROPPED,
// with a log line and no error. A recorder fake does not apply that rule, so a unit test asserting on
// a fake can be green about a field that never reaches audit_log, which is exactly what happened to
// this row's direction. Holding the keys against the shipped allowlist here means a future author who
// invents a new one is told at build time rather than by an empty column during an audit.
func TestAutoHold_TrailKeysSurviveAuditsAllowlist(t *testing.T) {
	store := &autoHoldFakeStore{fakeCreditStore: fakeCreditStore{policy: shippedPolicy()}}
	trail := &recordingAudit{}
	if _, err := NewCreditService(store).WithAudit(trail).
		SetAutoHold(context.Background(), "", true, "u-admin", "gate"); err != nil {
		t.Fatalf("flip: %v", err)
	}
	// The house shape, and the only keys this row is allowed to use. Keep this list in step with
	// audit/service.go's allowedExtraKeys, it is asserted, not assumed, by the integration half in
	// train9_autohold_audit_integration_test.go, which reads the row back out of Postgres.
	allowed := map[string]bool{"field": true, "before": true, "after": true, "status": true}
	for k := range trail.entries[0].Extra {
		if !allowed[k] {
			t.Fatalf("the auto-hold trail carries Extra key %q, which audit's deny-by-default allowlist "+
				"drops before persistence, the key would exist in this test and nowhere else", k)
		}
	}
}

// TestAutoHold_ThresholdsAreNotWritable is the negative space. The service exposes NO way to change a
// threshold; those values are the client's. If a Set* for one ever appears, this test is where the
// decision has to be made deliberately.
func TestAutoHold_ThresholdsAreNotWritable(t *testing.T) {
	store := &autoHoldFakeStore{fakeCreditStore: fakeCreditStore{policy: shippedPolicy()}}
	svc := NewCreditService(store)

	if _, err := svc.SetAutoHold(context.Background(), "", true, "u-admin", ""); err != nil {
		t.Fatalf("flip: %v", err)
	}
	// the ONLY thing the write seam was asked to change is the boolean; every threshold is as it was.
	after, err := svc.AutoHold(context.Background(), "")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	before := shippedPolicy()
	if after.AgingBucketDays != before.AgingBucketDays ||
		after.AgingThresholdCents != before.AgingThresholdCents ||
		after.LimitToleranceCents != before.LimitToleranceCents ||
		after.AgingHoldEnabled != before.AgingHoldEnabled ||
		after.LimitHoldEnabled != before.LimitHoldEnabled ||
		after.CODEnforced != before.CODEnforced {
		t.Fatalf("flipping the switch changed a threshold:\n before %+v\n after  %+v", before, after)
	}
}

// TestAutoHold_SwitchAndSweepReadOneSource: the switch an admin looks at must be the flag the machine
// obeys. They are the same CreditPolicy read, so turning it on makes the very next sweep a real run.
func TestAutoHold_SwitchAndSweepReadOneSource(t *testing.T) {
	store := &autoHoldFakeStore{fakeCreditStore: fakeCreditStore{
		policy: shippedPolicy(),
		rows: []AgingRow{
			{CustomerID: "over-limit", TermsDays: 30, CreditLimitCents: 1000, TotalCents: 90000},
		},
	}}
	svc := NewCreditService(store)
	ctx := context.Background()

	dry, err := svc.Evaluate(ctx, "")
	if err != nil {
		t.Fatalf("dry sweep: %v", err)
	}
	if !dry.DryRun || dry.WouldHold != 1 || dry.Placed != 0 {
		t.Fatalf("with the switch off the sweep must be a dry run reporting the blast radius: %+v", dry)
	}

	if _, err := svc.SetAutoHold(ctx, "", true, "u-admin", "go-live"); err != nil {
		t.Fatalf("flip: %v", err)
	}

	live, err := svc.Evaluate(ctx, "")
	if err != nil {
		t.Fatalf("live sweep: %v", err)
	}
	if live.DryRun {
		t.Fatal("the sweep is still a dry run after the switch was turned on, the admin surface and " +
			"the nightly job are reading different sources of truth, which is the failure this whole " +
			"design exists to prevent")
	}
	if live.Placed != 1 {
		t.Fatalf("live sweep placed %d, want 1", live.Placed)
	}
}

// -- the route ------------------------------------------------------------------------------------

func autoHoldMux(svc *CreditService) *http.ServeMux {
	// Guards-swap carry note (HH-3.2 fold): the pass-through guard stub
	// became a direct adapter over the guarded handler signature (the
	// caller arrives via adminReq's stamp, as at the pin).
	h := NewMonthEndHandler(svc, nil, slog.Default())
	mux := http.NewServeMux()
	direct := func(fn func(http.ResponseWriter, *http.Request, guards.StaffCaller)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { fn(w, r, guards.StaffCaller{}) }
	}
	mux.HandleFunc("GET /api/v1/ar/auto-hold", direct(h.autoHold))
	mux.HandleFunc("PUT /api/v1/ar/auto-hold", direct(h.setAutoHold))
	mux.HandleFunc("POST /api/v1/ar/credit-sweep", direct(h.sweep))
	return mux
}

func adminReq(method, target, body string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	return r.WithContext(identity.ContextWithStaff(r.Context(), identity.StaffContext{
		UserID: "u-admin", RoleCode: identity.RoleSuperAdmin, Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	}))
}

// TestAutoHoldRoute_ReadThenFlip drives the surface the admin actually touches.
func TestAutoHoldRoute_ReadThenFlip(t *testing.T) {
	store := &autoHoldFakeStore{fakeCreditStore: fakeCreditStore{policy: shippedPolicy()}}
	mux := autoHoldMux(NewCreditService(store))

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, adminReq(http.MethodGet, "/api/v1/ar/auto-hold", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d (%s)", w.Code, w.Body.String())
	}
	var read map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &read); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if read["auto_hold_enabled"] != false {
		t.Fatalf("GET reports %v, want the shipped false", read["auto_hold_enabled"])
	}
	if read["switch_wired"] != true {
		t.Fatal("GET must tell the client whether the control will work, so it can render read-only " +
			"instead of offering a button that fails")
	}
	if read["thresholds_editable"] != false {
		t.Fatal("GET must mark the thresholds read-only on the wire")
	}

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, adminReq(http.MethodPut, "/api/v1/ar/auto-hold", `{"enabled":true,"reason":"go-live"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT = %d (%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"auto_hold_enabled":true`) {
		t.Fatalf("PUT did not return the new state: %s", w.Body.String())
	}
}

// TestAutoHoldRoute_MissingEnabledIsRefused is the safety property of the request shape. `enabled` is
// a *bool precisely so that a body which omits it cannot be read as "false", a malformed request
// silently turning the safety flag off is the worst available failure here, and it is the DEFAULT one
// for a plain bool.
func TestAutoHoldRoute_MissingEnabledIsRefused(t *testing.T) {
	pol := shippedPolicy()
	pol.AutoHoldEnabled = true
	store := &autoHoldFakeStore{fakeCreditStore: fakeCreditStore{policy: pol}}
	mux := autoHoldMux(NewCreditService(store))

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, adminReq(http.MethodPut, "/api/v1/ar/auto-hold", `{"reason":"oops"}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a body with no `enabled` = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "true or false") {
		t.Fatalf("the refusal does not say what is missing: %s", w.Body.String())
	}
	if len(store.writes) != 0 {
		t.Fatal("a malformed request reached the write seam, it could have turned the switch off")
	}
}

// TestAutoHoldRoute_UnwiredSwitchIs503, a deploy fault reads as a deploy fault, not as a policy one.
func TestAutoHoldRoute_UnwiredSwitchIs503(t *testing.T) {
	mux := autoHoldMux(NewCreditService(&fakeCreditStore{policy: shippedPolicy()}))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, adminReq(http.MethodPut, "/api/v1/ar/auto-hold", `{"enabled":true}`))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired switch = %d, want 503", w.Code)
	}
	if !strings.Contains(w.Body.String(), "not configured") {
		t.Fatalf("the 503 does not name the problem: %s", w.Body.String())
	}
}
