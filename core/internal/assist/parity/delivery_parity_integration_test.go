//go:build integration

package parity

// Per record and per column parity for the delivery views (plan r3 section 1, slice 7b), against the dispatch
// module's own scoped reads (Service.RunDetail and Service.StopDetail under the caller's staff context), for
// callers built by the handler's own path. The screens fence by the run's or the stop's branch and gate nothing
// else, so the rows are the module's rows and the cells equal what its payloads carry; the assistant's tier floor
// sits on top (a tier 3 caller reads the screens and none of these views). No view carries a coordinate, an
// instruction, a gate code or a signature reference: the planted values appear nowhere.

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dispatch"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

type staffCallerCase struct {
	name string
	sc   identity.StaffContext
}

func deliveryCallers(branches ...string) []staffCallerCase {
	front := scoped("YARD", branches[0])
	front.Tier = identity.TierFrontline
	return []staffCallerCase{
		{"global manager", identity.StaffContext{UserID: "u-gm", RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: "ACTIVE"}},
		{"manager at a", scoped("BRANCH_MANAGER", branches[0])},
		{"manager at b", scoped("BRANCH_MANAGER", branches[1])},
		{"manager at both", scoped("BRANCH_MANAGER", branches...)},
		{"manager with no grant", scoped("BRANCH_MANAGER")},
		{"frontline at a", front},
	}
}

func claimsOf(t *testing.T, h *assist.Handler, sc identity.StaffContext) (assist.Caller, readtx.Claims) {
	t.Helper()
	req := newReq(testsupport.Ctx(t), sc)
	c, err := h.CallerFor(req, guards.StaffCaller{})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := c.ReadClaims()
	if err != nil {
		t.Fatal(err)
	}
	return c, claims
}

func readRows(t *testing.T, env *testsupport.Env, claims readtx.Claims, sql string, each func(rs pgx.Rows) error) {
	t.Helper()
	if err := env.Readers.Staff.Run(testsupport.Ctx(t), claims, sql, nil, func(rs pgx.Rows) error {
		for rs.Next() {
			if err := each(rs); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func TestParity_EveryDeliveryRunAndStopFollowsTheDispatchModulesOwnReads(t *testing.T) {
	env := testsupport.Open(t)
	w := plantDeliveryWorld(t, env)
	ctx := testsupport.Ctx(t)
	pool, err := pgxpool.New(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	svc := dispatch.NewService(dispatch.NewRepository(pool))
	h := assist.NewHandler(nil, nil)
	type idrow struct{ id, no string }
	var allRuns, allStops []idrow
	for _, q := range []struct {
		sql string
		dst *[]idrow
	}{{`SELECT id::text, run_no FROM delivery_runs`, &allRuns}, {`SELECT id::text, id::text FROM delivery_stops`, &allStops}} {
		rows, err := env.Admin.Query(ctx, q.sql)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var x idrow
			if err := rows.Scan(&x.id, &x.no); err != nil {
				t.Fatal(err)
			}
			*q.dst = append(*q.dst, x)
		}
		rows.Close()
	}
	if len(allRuns) < 3 || len(allStops) < 6 {
		t.Fatalf("the delivery world holds %d runs and %d stops", len(allRuns), len(allStops))
	}
	compared := 0
	for _, c := range deliveryCallers(w.branches()...) {
		_, claims := claimsOf(t, h, c.sc)
		floor := c.sc.Tier == identity.TierLeadership || c.sc.Tier == identity.TierManager
		type runCells struct {
			no, truck, driver, status, branch string
			date                              string
			stops                             int
		}
		gotRuns := map[string]runCells{}
		readRows(t, env, claims, `SELECT id::text, run_no, truck_code, driver_label, status, branch_label, scheduled_date::text, stop_count FROM ai.delivery_runs`, func(rs pgx.Rows) error {
			var id string
			var x runCells
			if err := rs.Scan(&id, &x.no, &x.truck, &x.driver, &x.status, &x.branch, &x.date, &x.stops); err != nil {
				return err
			}
			gotRuns[id] = x
			return nil
		})
		octx := identity.ContextWithStaff(ctx, c.sc)
		for _, r := range allRuns {
			d, derr := svc.RunDetail(octx, r.id)
			want := derr == nil && floor
			x, in := gotRuns[r.id]
			if in != want {
				t.Errorf("%s: run %s is in the view = %v, want %v (module read ok=%v)", c.name, r.no, in, want, derr == nil)
				continue
			}
			if !in {
				continue
			}
			compared++
			if x.no != d.RunNo || x.truck != d.TruckCode || x.driver != d.DriverName || x.status != d.Status || x.date != d.ScheduledDate || x.stops != d.StopCount {
				t.Errorf("%s: run %s differs: view %+v, module %+v", c.name, r.no, x, d.Run)
			}
		}
		type stopCells struct {
			runNo, orderNo, customer, status, window, contact, phone, address, city, postal, failure string
			stopNo                                                                                   int
			delivered                                                                                bool
			date                                                                                     string
		}
		gotStops := map[string]stopCells{}
		readRows(t, env, claims, `SELECT id::text, run_no, order_no, customer_name, status, time_window, contact_name, contact_phone, address_line, city, postal_code, failure_reason, stop_no, delivered_at IS NOT NULL, scheduled_date::text FROM ai.delivery_stops`, func(rs pgx.Rows) error {
			var id string
			var x stopCells
			if err := rs.Scan(&id, &x.runNo, &x.orderNo, &x.customer, &x.status, &x.window, &x.contact, &x.phone, &x.address, &x.city, &x.postal, &x.failure, &x.stopNo, &x.delivered, &x.date); err != nil {
				return err
			}
			gotStops[id] = x
			return nil
		})
		for _, s := range allStops {
			d, derr := svc.StopDetail(octx, s.id)
			want := derr == nil && floor
			x, in := gotStops[s.id]
			if in != want {
				t.Errorf("%s: stop %s is in the view = %v, want %v (module read ok=%v)", c.name, s.id, in, want, derr == nil)
				continue
			}
			if !in {
				continue
			}
			compared++
			if x.runNo != d.RunNo || x.orderNo != d.OrderNo || x.customer != d.CustomerName || x.status != d.Status || x.window != d.TimeWindow || x.contact != d.ContactName ||
				x.phone != d.ContactPhone || x.address != d.AddressLine || x.city != d.City || x.postal != d.PostalCode || x.failure != d.FailureReason ||
				x.stopNo != d.StopNo || x.delivered != (d.DeliveredAt != nil) || x.date != d.ScheduledDate {
				t.Errorf("%s: stop %s differs: view %+v, module %+v", c.name, s.id, x, d.Stop)
			}
		}
	}
	if compared < 24 {
		t.Fatalf("too little was compared: %d records", compared)
	}
}

// Coordinates, delivery instructions, the gate code and the signature reference reach no view, no catalogue and
// no log. A planted stop carries a gate code inside its instructions and in its own column; no cell of either
// view contains it, no catalogue column names it, and the query log of a query over the stops never holds it.
func TestDelivery_NoCoordinateInstructionGateCodeOrSignatureReachesAViewACatalogueOrALog(t *testing.T) {
	env := testsupport.Open(t)
	w := plantDeliveryWorld(t, env)
	ctx := testsupport.Ctx(t)
	h := assist.NewHandler(nil, nil)
	gm := deliveryCallers(w.branches()...)[0]
	caller, claims := claimsOf(t, h, gm.sc)
	secrets := []string{gateCodeSecret, signatureSecret, "Back lane", "dog loose", "44.1234", "-77.5678", "run note secret"}
	for _, view := range []string{"ai.delivery_runs", "ai.delivery_stops"} {
		readRows(t, env, claims, `SELECT to_jsonb(v)::text FROM `+view+` v`, func(rs pgx.Rows) error {
			var row string
			if err := rs.Scan(&row); err != nil {
				return err
			}
			for _, s := range secrets {
				if strings.Contains(row, s) {
					t.Errorf("%s carries %q: %s", view, s, row)
				}
			}
			return nil
		})
		var cols []string
		readRows(t, env, claims, `SELECT column_name FROM information_schema.columns WHERE table_schema = 'ai' AND table_name = '`+strings.TrimPrefix(view, "ai.")+`'`, func(rs pgx.Rows) error {
			var c string
			if err := rs.Scan(&c); err != nil {
				return err
			}
			cols = append(cols, c)
			return nil
		})
		for _, c := range cols {
			for _, bad := range []string{"latitude", "longitude", "instruction", "gate_code", "signature", "pod_", "notes"} {
				if strings.Contains(c, bad) {
					t.Errorf("%s has a column named %s", view, c)
				}
			}
		}
	}
	// No catalogue names them, and a spec naming them is unknown, never answered.
	for _, v := range assist.CatalogueFor(caller) {
		for _, c := range v.Columns {
			for _, bad := range []string{"latitude", "longitude", "instruction", "gate_code", "signature", "pod_signature_ref"} {
				if strings.Contains(c.Name, bad) {
					t.Errorf("the catalogue names %s.%s", v.Name, c.Name)
				}
			}
		}
	}
	log := &memLog{}
	q := &assist.ReadQuerier{Reader: env.Readers.Staff, Log: log}
	// A spec naming a column that is in no view is unknown, never answered.
	for _, spec := range []string{
		`{"view":"ai.delivery_stops","select":["order_no","latitude"]}`,
		`{"view":"ai.delivery_stops","select":["order_no","instructions"]}`,
		`{"view":"ai.delivery_stops","select":["gate_code"]}`,
		`{"view":"ai.delivery_stops","select":["order_no"],"where":[{"col":"pod_signature_ref","op":"is_null","value":false}]}`,
	} {
		res, err := q.Query(ctx, caller, spec)
		if err == nil && len(res.Items) != 0 {
			t.Errorf("a spec naming a column that is in no view answered: %s -> %+v", spec, res)
		}
	}
	res, err := q.Query(ctx, caller, `{"view":"ai.delivery_stops","where":[{"col":"order_no","op":"contains","value":"S7B-`+w.tag+`"}],"limit":100}`)
	if err != nil || len(res.Items) != 6 {
		t.Fatalf("a plain read of the planted stops: %v, %d rows", err, len(res.Items))
	}
	for _, it := range res.Items {
		for _, f := range it.Fields {
			for _, s := range secrets {
				if strings.Contains(f.Value, s) {
					t.Errorf("a result field %q carries %q", f.Label, s)
				}
			}
		}
	}
	// The query log keeps the typed spec and counts, never a result value: none of the planted values is in it.
	if len(log.rows) < 5 {
		t.Fatalf("the log holds %d rows, want a row per query", len(log.rows))
	}
	for _, e := range log.rows {
		b := string(e.Spec) + e.View + e.Refusal + strings.Join(e.CitedIDs, ",")
		for _, s := range secrets {
			if strings.Contains(b, s) {
				t.Errorf("a log row carries %q: %s", s, b)
			}
		}
	}
}
