// syncscope_test.go: the DB-free legs of the drive-run sync adapters:
// the payload proofs against the contract's declared fields (the stops
// payload serialises only what the contract's DispatchStop schema
// declares, the two money legs absent by type; the pod projection
// carries no bytes; the runs projection matches DispatchRun), the
// writable-field refusals, the photo byte validation, and the driver
// role's access through the sync surface's scope-member guard, driven
// through the real RequireSync over the real registered routes.
package dispatch

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// contractStopFields is the property list the contract's DispatchStop
// schema declares (core/api/openapi.yaml, the schema the stops scope's
// SyncEntity.data is defined as). The payload proof asserts every
// serialised key is one of these: the sync payload for stops carries
// exactly what the contract declares and nothing else.
var contractStopFields = map[string]bool{
	"id": true, "branch_id": true, "order_id": true, "order_no": true, "lines": true,
	"customer_name": true, "run_id": true, "run_no": true, "stop_no": true,
	"scheduled_date": true, "status": true, "time_window": true,
	"contact_name": true, "contact_phone": true, "address_line": true,
	"city": true, "postal_code": true, "latitude": true, "longitude": true,
	"instructions": true, "gate_code": true, "pod_signature_ref": true, "pod_received_by": true,
	"pod_at": true, "failure_reason": true, "loaded_at": true,
	"en_route_at": true, "delivered_at": true, "handoff_at": true,
	"photo_count": true, "created_at": true, "weight_kg": true,
	"volume_m3": true, "pallet_positions": true, "freight_is_override": true,
	"load_seq": true, "load_count": true, "requested_date": true,
	"commit_state": true, "committed_at": true, "reopened_at": true,
	"reopen_reason": true,
}

// contractRunFields is DispatchRun's declared property list.
var contractRunFields = map[string]bool{
	"id": true, "run_no": true, "branch_id": true, "scheduled_date": true,
	"truck_id": true, "driver_id": true, "truck_code": true,
	"driver_name": true, "status": true, "notes": true, "dispatched_at": true,
	"completed_at": true, "run_departs_at": true, "stop_count": true, "created_at": true,
	"commit_state": true, "reopened_at": true, "reopen_reason": true,
	"commit": true, "gauge": true,
}

// contractPODFields is DispatchPhotoMeta's declared property list: a
// POD record without its bytes.
var contractPODFields = map[string]bool{
	"id": true, "stop_id": true, "content_type": true, "byte_size": true,
	"caption": true, "captured_at": true,
}

// fullStop builds one Stop with every leg set, money pointers included,
// so the payload proof runs against the widest row the module can hold.
// driverRoles is the sync write fence's staff roles seam for the driver
// login this file drives.
type driverRoles struct{}

func (driverRoles) ResolveRoles(context.Context, string, string) ([]string, error) {
	return []string{testkit.RoleDriver}, nil
}

func fullStop() Stop {
	lat, lng := 44.1, -77.0
	weight, volume, pallets := 1200.5, 2.25, 3
	value, freight := int64(45000), int64(1200)
	at := time.Date(2026, 9, 25, 14, 0, 0, 0, time.UTC)
	return Stop{
		ID: "3ef10000-0000-4000-8000-0000000000a1", BranchID: testkit.BranchNorthAID,
		OrderID: testkit.OrderAlphaReadyID, OrderNo: "FXORD-A-READY",
		CustomerName: "Fixture Contractor Alpha", RunID: "3ef10000-0000-4000-8000-0000000000b1",
		RunNo: "#RUN-FXNA-1", StopNo: 2, ScheduledDate: "2026-09-25",
		Status: StopEnRoute, TimeWindow: "AM", ContactName: "Fixture contact",
		ContactPhone: "5550100", AddressLine: "Fixture address line", City: "Fixture City",
		PostalCode: "K0A 0A0", Latitude: &lat, Longitude: &lng,
		Instructions: "Fixture instructions", GateCode: "#4420", PODSignedRef: "FXSIG-1", PODReceivedBy: "Fixture Receiver",
		PODAt: &at, FailureReason: "", LoadedAt: &at, EnRouteAt: &at, DeliveredAt: nil,
		HandoffAt: nil, PhotoCount: 2, CreatedAt: at, WeightKg: &weight, VolumeM3: &volume,
		PalletPositions: &pallets, ValueCents: &value, FreightCents: &freight,
		FreightIsOverride: false, LoadSeq: 1, LoadCount: 2, RequestedDate: "2026-09-24",
		CommitState: CommitCommitted, CommittedAt: &at, ReopenedAt: &at,
		ReopenReason: "Fixture reopen",
	}
}

// TestStopsPayloadSerialisesOnlyTheContractFields: the stops scope's
// sync payload serialises exactly what the contract's DispatchStop
// schema declares. The two money legs are json:"-" on the struct (a
// field that is on the general payload is a field somebody has to
// remember to blank in eleven read paths, the 0184 rule), and the
// proof pins that at the sync projection: no key outside the declared
// list, the money keys never present, the widest row's every declared
// leg present when set.
func TestStopsPayloadSerialisesOnlyTheContractFields(t *testing.T) {
	// Through the adapter's own fold (stopStateOf), so the proof covers
	// everything the adapter adds to the marshal, not the marshal alone.
	state, err := stopStateOf(syncStopRow{stop: fullStop(), seq: 1})
	if err != nil {
		t.Fatalf("projection: %v", err)
	}
	data := state.Data
	for k := range data {
		if !contractStopFields[k] {
			t.Fatalf("stops payload carries %q, which the contract's DispatchStop schema does not declare (payload keys: %v)", k, data)
		}
	}
	for _, k := range []string{"value_cents", "freight_cents"} {
		if _, ok := data[k]; ok {
			t.Fatalf("stops payload carries the gated money leg %q", k)
		}
	}
	// The declared legs that must be present on the widest row: the
	// required set plus the ones this row set.
	for _, k := range []string{"id", "branch_id", "order_id", "run_id", "stop_no", "scheduled_date", "status", "photo_count", "created_at", "weight_kg", "volume_m3", "pallet_positions", "requested_date", "commit_state", "reopened_at", "reopen_reason", "pod_signature_ref", "pod_received_by", "pod_at", "loaded_at", "en_route_at", "latitude", "longitude", "gate_code"} {
		if _, ok := data[k]; !ok {
			t.Fatalf("widest stop row lost declared leg %q (payload keys: %v)", k, data)
		}
	}
	// The D17 contractor stop view is another surface; the contract's
	// staff-side stop shape is what the sync payload carries, proven
	// above by the declared-list compare itself.
}

// TestRunsPayloadMatchesTheContractFields: the runs projection
// serialises only DispatchRun's declared legs.
func TestRunsPayloadMatchesTheContractFields(t *testing.T) {
	// Through the adapter's own fold, like the stops proof.
	at := time.Time{}
	r := Run{
		ID: "3ef10000-0000-4000-8000-0000000000b1", RunNo: "#RUN-FXNA-1",
		BranchID: testkit.BranchNorthAID, ScheduledDate: "2026-09-25",
		TruckID: testkit.TruckID, DriverID: "3ef10000-0000-4000-8000-0000000000c1",
		TruckCode: "FX-TRK-1", DriverName: "Fixture Driver", Status: RunDispatched,
		Notes: "Fixture notes", DispatchedAt: &at, RunDepartsAt: &at, StopCount: 3, CreatedAt: at,
		CommitState: "AUTO", ReopenedAt: "2026-09-20", ReopenReason: "Fixture reopen",
	}
	state, err := runStateOf(syncRunRow{run: r, seq: 1})
	if err != nil {
		t.Fatalf("projection: %v", err)
	}
	data := state.Data
	for k := range data {
		if !contractRunFields[k] {
			t.Fatalf("runs payload carries %q, which the contract's DispatchRun schema does not declare (payload keys: %v)", k, data)
		}
	}
}

// TestPODProjectionCarriesNoBytes: the pod projection serialises
// DispatchPhotoMeta's declared legs only; the bytes come back from the
// per-photo route and never ride a sync payload.
func TestPODProjectionCarriesNoBytes(t *testing.T) {
	m := PhotoMeta{
		ID:          "3ef10000-0000-4000-8000-0000000000d1",
		StopID:      "3ef10000-0000-4000-8000-0000000000a1",
		ContentType: "image/png", ByteSize: 12, Caption: "Fixture caption",
		CapturedAt: time.Time{},
	}
	state, err := podStateOf(syncPodRow{meta: m, seq: 1})
	if err != nil {
		t.Fatalf("projection: %v", err)
	}
	data := state.Data
	for k := range data {
		if !contractPODFields[k] {
			t.Fatalf("pod payload carries %q, which the contract's DispatchPhotoMeta schema does not declare (payload keys: %v)", k, data)
		}
	}
	for _, k := range []string{"image_bytes", "image_bytes_b64", "bytes"} {
		if _, ok := data[k]; ok {
			t.Fatalf("pod payload carries bytes under %q", k)
		}
	}
}

// TestSyncWritableFieldRefusals: a payload outside a scope's writable
// set refuses the operation whole.
func TestSyncWritableFieldRefusals(t *testing.T) {
	cases := []struct {
		name   string
		check  func(map[string]any) error
		fields map[string]any
	}{
		{"runs create declares status", runCreateFields, map[string]any{"status": RunPlanned}},
		{"runs update declares branch", runWriteFields, map[string]any{"branch_id": "x"}},
		{"stops update declares notes", stopWriteFields, map[string]any{"notes": "x"}},
		{"stops create declares stop_no", stopCreateFields, map[string]any{"stop_no": 1}},
		{"pod update declares content_type", podWriteFields, map[string]any{"content_type": "image/png"}},
		{"pod create declares captured_at", podCreateFields, map[string]any{"captured_at": "x"}},
	}
	for _, c := range cases {
		err := c.check(c.fields)
		if err == nil {
			t.Fatalf("%s: accepted an unwritable field", c.name)
		}
	}
	// The legal sets pass.
	for _, c := range []struct {
		name   string
		check  func(map[string]any) error
		fields map[string]any
	}{
		{"runs create", runCreateFields, map[string]any{"branch_id": "x", "scheduled_date": "2026-09-25", "truck_id": "t", "driver_id": "d", "notes": "n"}},
		{"runs update", runWriteFields, map[string]any{"status": RunCompleted, "notes": "n"}},
		{"stops update", stopWriteFields, map[string]any{"status": StopLoaded, "failure_reason": "r"}},
		{"stops create", stopCreateFields, map[string]any{"order_id": "o", "scheduled_date": "2026-09-25", "run_id": "r", "address_line": "a"}},
		{"pod update", podWriteFields, map[string]any{"caption": "c"}},
		{"pod create", podCreateFields, map[string]any{"stop_id": "s", "content_type": "image/png", "image_bytes_b64": "aGk=", "caption": "c"}},
	} {
		if err := c.check(c.fields); err != nil {
			t.Fatalf("%s: refused a legal payload: %v", c.name, err)
		}
	}
}

// TestDecodePhotoBytes: the create's bytes validate per the module's
// own rules, the content type among 0091's three, the length within
// the module's bound, base64 that decodes.
func TestDecodePhotoBytes(t *testing.T) {
	tiny := base64.StdEncoding.EncodeToString([]byte("fixture-bytes"))
	if _, err := decodePhotoBytes("image/png", tiny); err != nil {
		t.Fatalf("legal photo refused: %v", err)
	}
	if _, err := decodePhotoBytes("image/gif", tiny); err == nil {
		t.Fatalf("a gif accepted")
	}
	if _, err := decodePhotoBytes("image/png", "not base64!!"); err == nil {
		t.Fatalf("malformed base64 accepted")
	}
	if _, err := decodePhotoBytes("image/png", base64.StdEncoding.EncodeToString(nil)); err == nil {
		t.Fatalf("empty bytes accepted")
	}
	big := base64.StdEncoding.EncodeToString(make([]byte, MaxPhotoBytes+1))
	if _, err := decodePhotoBytes("image/png", big); err == nil {
		t.Fatalf("oversize photo accepted")
	}
}

// fakeSyncStore is the DB-free Store the driver-role route leg pushes
// and pulls through: one row, one seq, org-blind (the battery over
// real tables is the integration suite's leg; this one proves the
// guard, not the query).
type fakeSyncStore struct{ pulled int }

func (f *fakeSyncStore) Pull(_ context.Context, _ sync.ScopeFilter, since int64, _ int) ([]sync.Entity, []sync.Tombstone, error) {
	f.pulled++
	if since > 0 {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	return []sync.Entity{{
		ID: "3ef10000-0000-4000-8000-0000000000e1", Seq: 1,
		Data: map[string]any{"id": "3ef10000-0000-4000-8000-0000000000e1", "status": RunDispatched},
	}}, nil, nil
}

func (f *fakeSyncStore) FindApplied(_ context.Context, _ sync.ScopeFilter, _ string) (sync.RowState, bool, error) {
	return sync.RowState{}, false, nil
}

func (f *fakeSyncStore) Load(_ context.Context, _ sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	return sync.RowState{ID: id, Seq: 1, Data: map[string]any{"id": id, "status": RunDispatched}}, true, nil
}

func (f *fakeSyncStore) Create(_ context.Context, _ sync.ScopeFilter, _ string, fields map[string]any) (sync.RowState, error) {
	return sync.RowState{ID: fmt.Sprint(fields["id"]), Seq: 1}, nil
}

func (f *fakeSyncStore) Write(_ context.Context, _ sync.ScopeFilter, id, _ string, _ map[string]any) (sync.RowState, error) {
	return sync.RowState{ID: id, Seq: 2}, nil
}

func (f *fakeSyncStore) Delete(_ context.Context, _ sync.ScopeFilter, id, _ string) (sync.RowState, error) {
	return sync.RowState{ID: id, Seq: 3, Deleted: true}, nil
}

// fixedAuthority is the settable staff authority source for the
// driver route leg (the kit's own stub stays unexported; this one
// lives where the test that needs it is).
type fixedAuthority struct {
	a   identity.StaffAuthority
	has bool
}

func (f *fixedAuthority) ResolveStaff(context.Context, string, string) (identity.StaffAuthority, bool, error) {
	return f.a, f.has, nil
}

// fixedPortal answers no portal row; the staff leg never consults it.
type fixedPortal struct{}

func (fixedPortal) PortalRole(context.Context, string, string) (string, bool, error) {
	return "", false, nil
}

// fixedKnob answers the dealer's driver session knob, in contract.
type fixedKnob struct{}

func (fixedKnob) DriverSessionHours(context.Context, string) (int, error) { return 12, nil }

// TestDriverRoleReachesSyncScopeMember: the driver role's access to
// the drive-run scopes rides the sync surface's scope-member gate, and
// the real RequireSync admits a driver-role staff caller to the runs
// scope (the pull answers through the store), while the same caller
// without a grant is refused the shared 403. The drive-run catalog
// entry is the driver's, and the guard is where that access is proven
// against, not a per-route role list.
func TestDriverRoleReachesSyncScopeMember(t *testing.T) {
	// The kit's StaffGuards wires a nil driver-session knob, and the
	// driver ceiling leg refuses fail closed without one (the 0209
	// rule), so this leg builds the real Guards over the kit's gate
	// and minter with a fixed knob and a settable authority, exactly
	// the StaffGuards shape with the one collaborator a driver needs.
	sg := testkit.NewStaffGuards(t)
	driverGuards, err := guards.NewGuards(sg.Harness.Gate, &fixedAuthority{
		a: identity.StaffAuthority{
			RoleID: testkit.RoleDriver, Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
			Branches: []string{testkit.BranchNorthAID},
		}, has: true,
	}, fixedPortal{}, fixedKnob{})
	if err != nil {
		t.Fatalf("NewGuards: %v", err)
	}
	engine := sync.NewEngine()
	store := &fakeSyncStore{}
	for _, scope := range []string{"runs", "stops", "pod"} {
		if err := engine.RegisterScope(scope, store); err != nil {
			t.Fatalf("register %s: %v", scope, err)
		}
	}
	h, err := sync.NewHandler(engine, driverRoles{}, fixedPortal{}, nil)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, driverGuards)

	do := func(method, target string, body string) *httptest.ResponseRecorder {
		t.Helper()
		var reader *strings.Reader
		if body == "" {
			reader = strings.NewReader("")
		} else {
			reader = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, target, reader)
		req.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
		req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	for _, scope := range []string{"runs", "stops", "pod"} {
		rec := do(http.MethodGet, "/api/v1/sync/"+scope+"?since=0&limit=10", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("driver pull of %s: %d %s", scope, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"scope":"`+scope+`"`) {
			t.Fatalf("driver pull of %s did not answer the scope's feed: %s", scope, rec.Body.String())
		}
	}
	// A driver pushing a runs outbox through the same guard: the write
	// path reaches the store (the applied result), the guard admits.
	rec := do(http.MethodPost, "/api/v1/sync/runs", `{"ops":[{"client_id":"30000000-0000-4000-8000-000000000001","op":"create","payload":{"branch_id":"`+testkit.BranchNorthAID+`","scheduled_date":"2026-09-25"}}]}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"applied"`) {
		t.Fatalf("driver push of runs: %d %s", rec.Code, rec.Body.String())
	}
	if store.pulled < 3 {
		t.Fatalf("the pulls never reached the store (pulled %d)", store.pulled)
	}

	// The same caller with no grant: authenticated, the x-role gate
	// refused, the shared 403 body.
	driverGuards, err = guards.NewGuards(sg.Harness.Gate, &fixedAuthority{has: false}, fixedPortal{}, fixedKnob{})
	if err != nil {
		t.Fatalf("NewGuards (no grant): %v", err)
	}
	mux = http.NewServeMux()
	h.RegisterRoutes(mux, driverGuards)
	rec = do(http.MethodGet, "/api/v1/sync/runs", "")
	if rec.Code != http.StatusForbidden || rec.Body.String() != string(authz.BodyForbidden) {
		t.Fatalf("no-grant pull: %d %s", rec.Code, rec.Body.String())
	}
}

// silence the unused warning for apigen when the build tags move: the
// route leg above exercises the JSON shapes through the handler, and
// apigen's SyncPullResponse is the response's own type.
var _ apigen.SyncPullResponse
