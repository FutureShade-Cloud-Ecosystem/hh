//go:build integration

// syncscope_pod_attest_integration_test.go: the typed name attestation door of the pod sync scope (Field
// CP-2) against a real database, through the real engine. The payload is {id?, stop_id, received_by,
// affirmed}; the store runs the one CapturePOD body the REST door runs, under the caller's resolved staff
// context, so "the same effect as the direct door" is proved by running both doors on two stops of one
// driver's run and comparing the stops, the signatures asked for, the order hand off and the audit rows.
//
// The world is the driver fence world (two driver logins, two runs, one stop each) plus a second stop on
// driver A's run. Run with DATABASE_URL set to a migrated and seeded throwaway database.
package dispatch

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	erpidentity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

type attestWorld struct {
	*fenceWorld
	tb     *testing.T
	h      *harness
	stopA2 string
	// authority is what the staff seam answers; nil means identity's own resolver, as production.
	authority func() (identity.StaffContext, bool, error)
}

func newAttestWorld(t *testing.T) *attestWorld {
	t.Helper()
	w := newFenceWorld(t)
	h := newHarness(t)
	h.svc.WithAudit(audit.NewService(audit.NewRepository(h.pool), nil))
	ctx := globalCtx(t, h.pool)
	// A second stop on driver A's run, before the run goes out, then the run goes out and both stops
	// are on the road. Driver B's stop was walked to EN_ROUTE by the world.
	stopA2 := fenceStopOnRun(t, h, ctx, w.runA)
	if _, err := h.svc.DispatchRun(ctx, w.runA.ID); err != nil {
		t.Fatalf("dispatch run A: %v", err)
	}
	for _, id := range []string{w.stopA, stopA2} {
		if _, err := h.svc.AdvanceStop(ctx, id, StopEnRoute, ""); err != nil {
			t.Fatalf("advance %s: %v", id, err)
		}
	}
	a := &attestWorld{fenceWorld: w, tb: t, h: h, stopA2: stopA2}
	// The driver logins hold the branch grants the REST door's guard reads (user_branch_grants), so the
	// branch fence passes for them as it does for a real driver.
	for _, sub := range []string{testkit.SubDriver, w.driverB.Actor} {
		var user string
		if err := h.pool.QueryRow(context.Background(),
			`SELECT id::text FROM users WHERE platform_user_id = $1 AND deleted_at IS NULL`, sub).Scan(&user); err != nil {
			t.Fatalf("driver login %s: %v", sub, err)
		}
		if _, err := h.pool.Exec(context.Background(),
			`INSERT INTO user_branch_grants (user_id, branch_id, org_id) VALUES ($1::uuid, $2::uuid, $3::uuid)
			 ON CONFLICT DO NOTHING`, user, testkit.BranchNorthAID, testkit.OrgNorthID); err != nil {
			t.Fatalf("branch grant for %s: %v", sub, err)
		}
		t.Cleanup(func() {
			_, _ = h.pool.Exec(context.Background(), `DELETE FROM user_branch_grants WHERE user_id = $1::uuid AND branch_id = $2::uuid`,
				user, testkit.BranchNorthAID)
		})
	}
	roles, err := erpidentity.NewStaffAuthorityResolver(h.pool)
	if err != nil {
		t.Fatalf("authority resolver: %v", err)
	}
	link := linkedSyncUser(h.pool)
	w.pod.WithAttestor(h.svc).WithStaffAuthority(func(c context.Context, sub, org string) (identity.StaffContext, bool, error) {
		if a.authority != nil {
			return a.authority()
		}
		au, has, err := roles.ResolveStaff(c, sub, org)
		if err != nil || !has {
			return identity.StaffContext{}, false, err
		}
		sc := identity.FromAuthority(au)
		if uid, ok := link(c, sub); ok {
			sc.UserID = uid
		}
		return sc, true, nil
	})
	return a
}

func (a *attestWorld) push(f sync.ScopeFilter, ops ...apigen.SyncPushOperation) []apigen.SyncPushResult {
	return syncPush(a.tb, &syncFixture{engine: a.engine}, "pod", f, ops...)
}

func attestOp(key, stopID, name string, affirmed any) apigen.SyncPushOperation {
	return syncOp("create", key, map[string]any{"id": syncFreshKey(), "stop_id": stopID, "received_by": name, "affirmed": affirmed}, nil)
}

type stopFacts struct {
	status, receivedBy, sigRef, podBy string
	seq                               int64
}

func (a *attestWorld) stop(id string) stopFacts {
	a.tb.Helper()
	var f stopFacts
	err := a.pool.QueryRow(context.Background(),
		`SELECT status, COALESCE(pod_received_by,''), COALESCE(pod_signature_ref,''), COALESCE(pod_by::text,''), seq
		   FROM delivery_stops WHERE id = $1::uuid`, id).Scan(&f.status, &f.receivedBy, &f.sigRef, &f.podBy, &f.seq)
	if err != nil {
		a.tb.Fatalf("read stop %s: %v", id, err)
	}
	return f
}

func (a *attestWorld) auditCount(stopID string) int {
	a.tb.Helper()
	var n int
	if err := a.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = 'DELIVERY_POD_CAPTURED' AND entity_id = $1`, stopID).Scan(&n); err != nil {
		a.tb.Fatalf("audit count: %v", err)
	}
	return n
}

func errText(r apigen.SyncPushResult) string {
	if r.Error == nil {
		return ""
	}
	return *r.Error
}

func TestPodAttest_EffectEqualsTheDirectDoor(t *testing.T) {
	a := newAttestWorld(t)
	directCtx := identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: testkit.UserDriverID, RoleCode: identity.RoleDrivers, Tier: identity.TierFrontline,
		Scope: identity.ScopeBranch, Status: identity.StatusActive, Branches: map[string]bool{testkit.BranchNorthAID: true},
	})
	before := a.stop(a.stopA2).seq
	if _, err := a.h.svc.CapturePOD(directCtx, a.stopA, PODInput{ReceivedBy: "Site Foreman", Affirmed: true}); err != nil {
		t.Fatalf("direct door: %v", err)
	}
	res := a.push(a.driverA, attestOp(syncFreshKey(), a.stopA2, "Site Foreman", true))
	if res[0].Status != "applied" || res[0].Seq == nil {
		t.Fatalf("the sync door must apply: %+v (%s)", res[0], errText(res[0]))
	}

	d, s := a.stop(a.stopA), a.stop(a.stopA2)
	if d.status != StopDelivered || s.status != StopDelivered {
		t.Fatalf("both doors deliver: direct %q sync %q", d.status, s.status)
	}
	if d.receivedBy != s.receivedBy || s.receivedBy != "Site Foreman" {
		t.Fatalf("the typed name lands on the stop: %q vs %q", d.receivedBy, s.receivedBy)
	}
	if d.sigRef == "" || s.sigRef == "" {
		t.Fatalf("the attestation is sealed first, both stops carry a signature ref: %q %q", d.sigRef, s.sigRef)
	}
	if d.podBy == "" || d.podBy != s.podBy {
		t.Fatalf("the recorded actor is the calling driver's user: %q vs %q", d.podBy, s.podBy)
	}
	if s.seq <= before || *res[0].Seq != s.seq {
		t.Fatalf("the stop draws a fresh seq and the result reports it: before %d, after %d, result %d", before, s.seq, *res[0].Seq)
	}
	calls := a.h.signer.calls
	if len(calls) != 2 {
		t.Fatalf("the signature provider is asked once per door, got %d", len(calls))
	}
	x, y := calls[0], calls[1]
	if x.Purpose != y.Purpose || x.RefType != y.RefType || x.TypedName != y.TypedName || x.Affirmed != y.Affirmed {
		t.Fatalf("the attestation differs between doors:\ndirect %+v\nsync   %+v", x, y)
	}
	if y.RefID != a.stopA2 {
		t.Fatalf("the sync attestation signs the stop it names: %q", y.RefID)
	}
	if len(a.h.fulfil.orders) != 2 {
		t.Fatalf("the order hand off runs once per door, got %v", a.h.fulfil.orders)
	}
	if a.auditCount(a.stopA) != 1 || a.auditCount(a.stopA2) != 1 {
		t.Fatalf("the capture audit row lands on both doors: %d %d", a.auditCount(a.stopA), a.auditCount(a.stopA2))
	}
}

func TestPodAttest_ReplayAppliesOnceAndAnswersApplied(t *testing.T) {
	a := newAttestWorld(t)
	key := syncFreshKey()
	first := a.push(a.driverA, attestOp(key, a.stopA2, "Site Foreman", true))
	if first[0].Status != "applied" {
		t.Fatalf("first: %+v %s", first[0], errText(first[0]))
	}
	seq := a.stop(a.stopA2).seq
	second := a.push(a.driverA, attestOp(key, a.stopA2, "Site Foreman", true))
	if second[0].Status != "applied" || second[0].Seq == nil || *second[0].Seq != seq {
		t.Fatalf("a replay answers applied with the stop's seq %d: %+v (%s)", seq, second[0], errText(second[0]))
	}
	if len(a.h.signer.calls) != 1 || len(a.h.fulfil.orders) != 1 || a.auditCount(a.stopA2) != 1 {
		t.Fatalf("nothing runs twice: signatures %d, hand offs %d, audit rows %d",
			len(a.h.signer.calls), len(a.h.fulfil.orders), a.auditCount(a.stopA2))
	}
	if a.stop(a.stopA2).seq != seq {
		t.Fatal("a replay must not move the stop")
	}
	// A different key on the delivered stop is a second attestation and refuses.
	third := a.push(a.driverA, attestOp(syncFreshKey(), a.stopA2, "Another Name", true))
	if third[0].Status != "rejected" {
		t.Fatalf("a second attestation on a delivered stop refuses: %+v", third[0])
	}
	if a.stop(a.stopA2).receivedBy != "Site Foreman" || len(a.h.signer.calls) != 1 {
		t.Fatal("the first attestation stands")
	}
}

func TestPodAttest_RefusalsAttestNothing(t *testing.T) {
	a := newAttestWorld(t)
	photo := func(extra map[string]any) apigen.SyncPushOperation {
		p := map[string]any{"stop_id": a.stopA2, "received_by": "Site Foreman", "affirmed": true}
		for k, v := range extra {
			p[k] = v
		}
		return syncOp("create", syncFreshKey(), p, nil)
	}
	cases := map[string]apigen.SyncPushOperation{
		"blank name":              attestOp(syncFreshKey(), a.stopA2, "   ", true),
		"no name":                 syncOp("create", syncFreshKey(), map[string]any{"stop_id": a.stopA2, "affirmed": true}, nil),
		"not affirmed":            attestOp(syncFreshKey(), a.stopA2, "Site Foreman", false),
		"affirmed as a string":    attestOp(syncFreshKey(), a.stopA2, "Site Foreman", "true"),
		"affirmed missing":        syncOp("create", syncFreshKey(), map[string]any{"stop_id": a.stopA2, "received_by": "Site Foreman"}, nil),
		"stamped time on wire":    photo(map[string]any{"created_at": "a client stamp"}),
		"mixed with photo bytes":  photo(map[string]any{"content_type": "image/png", "image_bytes_b64": "RkVOQ0U="}),
		"unknown leg":             photo(map[string]any{"pod_at": "a client stamp"}),
		"stop id not a uuid":      attestOp(syncFreshKey(), "not-a-uuid", "Site Foreman", true),
		"name that is not text":   syncOp("create", syncFreshKey(), map[string]any{"stop_id": a.stopA2, "received_by": 12, "affirmed": true}, nil),
		"affirmed but name empty": attestOp(syncFreshKey(), a.stopA2, "", true),
	}
	for name, op := range cases {
		res := a.push(a.driverA, op)
		if res[0].Status != "rejected" || errText(res[0]) == "" {
			t.Fatalf("%s: want rejected with words, got %+v", name, res[0])
		}
	}
	if len(a.h.signer.calls) != 0 || a.stop(a.stopA2).status != StopEnRoute {
		t.Fatalf("refusals attest nothing: signatures %d, stop %q", len(a.h.signer.calls), a.stop(a.stopA2).status)
	}
	// The direct door's words travel: the name and the confirmation refusals.
	if got := errText(a.push(a.driverA, attestOp(syncFreshKey(), a.stopA2, "", true))[0]); !strings.Contains(got, "full name") {
		t.Fatalf("the blank name refusal keeps the direct door's words: %q", got)
	}
	if got := errText(a.push(a.driverA, attestOp(syncFreshKey(), a.stopA2, "Site Foreman", false))[0]); !strings.Contains(got, "confirm") {
		t.Fatalf("the affirmation refusal keeps the direct door's words: %q", got)
	}
}

func TestPodAttest_DriverFenceAndOrgIsolation(t *testing.T) {
	a := newAttestWorld(t)
	// Driver A cannot attest driver B's stop; the stop is the same as absent to them.
	res := a.push(a.driverA, attestOp(syncFreshKey(), a.stopB, "Site Foreman", true))
	if res[0].Status != "rejected" {
		t.Fatalf("a colleague's stop is off the driver's runs: %+v", res[0])
	}
	if a.stop(a.stopB).status != StopEnRoute || len(a.h.signer.calls) != 0 {
		t.Fatal("the fenced attestation must change nothing")
	}
	// Driver B attests their own.
	if res := a.push(a.driverB, attestOp(syncFreshKey(), a.stopB, "Site Foreman", true)); res[0].Status != "applied" {
		t.Fatalf("driver B on their own stop: %+v (%s)", res[0], errText(res[0]))
	}
	// An unlinked driver login reads nothing and writes nothing.
	if res := a.push(a.bare, attestOp(syncFreshKey(), a.stopA2, "Site Foreman", true)); res[0].Status != "rejected" {
		t.Fatalf("an unlinked driver login is held to nothing: %+v", res[0])
	}
	// Another org's caller never finds the stop.
	south := sync.ScopeFilter{OrgID: testkit.OrgSouthID, Actor: testkit.SubInsideSales}
	if res := a.push(south, attestOp(syncFreshKey(), a.stopA2, "Site Foreman", true)); res[0].Status != "rejected" {
		t.Fatalf("another org's stop is absent: %+v", res[0])
	}
	if a.stop(a.stopA2).status != StopEnRoute {
		t.Fatal("the stop is untouched by the fenced callers")
	}
}

func TestPodAttest_BranchFenceIsTheDirectDoors(t *testing.T) {
	a := newAttestWorld(t)
	a.authority = func() (identity.StaffContext, bool, error) {
		return identity.StaffContext{UserID: testkit.UserDriverID, RoleCode: identity.RoleDrivers, Scope: identity.ScopeBranch,
			Status: identity.StatusActive, Branches: map[string]bool{testkit.BranchSouthAID: true}}, true, nil
	}
	if res := a.push(a.driverA, attestOp(syncFreshKey(), a.stopA2, "Site Foreman", true)); res[0].Status != "rejected" {
		t.Fatalf("a caller whose branches miss the stop's branch is refused: %+v", res[0])
	}
	a.authority = func() (identity.StaffContext, bool, error) { return identity.StaffContext{}, false, nil }
	if res := a.push(a.driverA, attestOp(syncFreshKey(), a.stopA2, "Site Foreman", true)); res[0].Status != "rejected" {
		t.Fatalf("no staff authority is a refusal: %+v", res[0])
	}
	a.authority = func() (identity.StaffContext, bool, error) {
		return identity.StaffContext{RoleCode: identity.RoleDrivers, Scope: identity.ScopeBranch, Status: identity.StatusActive,
			Branches: map[string]bool{testkit.BranchNorthAID: true}}, true, nil
	}
	if res := a.push(a.driverA, attestOp(syncFreshKey(), a.stopA2, "Site Foreman", true)); res[0].Status != "rejected" {
		t.Fatalf("a caller with no linked user cannot be an attributed actor: %+v", res[0])
	}
	if a.stop(a.stopA2).status != StopEnRoute || len(a.h.signer.calls) != 0 {
		t.Fatal("every fenced attestation leaves the stop alone")
	}
	// The positive control: the driver's own authority, as identity resolves it, attests the stop.
	a.authority = nil
	if res := a.push(a.driverA, attestOp(syncFreshKey(), a.stopA2, "Site Foreman", true)); res[0].Status != "applied" {
		t.Fatalf("the branch the driver is granted attests: %+v (%s)", res[0], errText(res[0]))
	}
}

func TestPodAttest_UnwiredDoorRefuses(t *testing.T) {
	w := newFenceWorld(t)
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	stopA2 := fenceStopOnRun(t, h, ctx, w.runA)
	if _, err := h.svc.DispatchRun(ctx, w.runA.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := h.svc.AdvanceStop(ctx, stopA2, StopEnRoute, ""); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if w.pod.AttestDoorWired() {
		t.Fatal("a store with no seams is not wired")
	}
	res := syncPush(t, &syncFixture{engine: w.engine}, "pod", w.driverA, attestOp(syncFreshKey(), stopA2, "Site Foreman", true))
	if res[0].Status != "rejected" || !strings.Contains(errText(res[0]), "attestation") {
		t.Fatalf("an unwired door refuses and says which door: %+v", res[0])
	}
}

func TestPodAttest_PartialRefusalOrderingAndNoUpdateDoor(t *testing.T) {
	a := newAttestWorld(t)
	res := a.push(a.driverA,
		attestOp(syncFreshKey(), a.stopA, "Site Foreman", true),
		attestOp(syncFreshKey(), a.stopA2, "", true),
		attestOp(syncFreshKey(), a.stopA2, "Site Foreman", true),
	)
	got := []string{res[0].Status, res[1].Status, res[2].Status}
	if fmt.Sprint(got) != fmt.Sprint([]string{"applied", "rejected", "applied"}) {
		t.Fatalf("results in push order: %v", got)
	}
	// An attestation is a fact, not a field: there is no update door onto it (the stop id is not a pod
	// photo id, so the manual scope answers not found).
	up := a.push(a.driverA, syncOp("update", syncFreshKey(), map[string]any{"id": a.stopA, "received_by": "Someone Else"}, nil))
	if up[0].Status != "rejected" {
		t.Fatalf("an attestation cannot be edited through sync: %+v", up[0])
	}
	if a.stop(a.stopA).receivedBy != "Site Foreman" {
		t.Fatal("the attestation stands")
	}
}
