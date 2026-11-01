//go:build integration

// finance_job_door_integration_test.go, wave 3 FC-1c: the Controller's recorded door to enable or
// disable a scheduled finance job. Over the served mux, on the real jobs repository, runner and audit
// service: enable then one tick runs the job once, disable then a tick runs nothing, a refused role
// changes nothing, an unknown name is 404, and the audit row reads back.
//
// Run: go test -tags integration ./internal/billing/ -run W3FC1C
package billing

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	erpidentity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/jobs"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

const (
	fcJobsPath    = "/api/v1/ar/finance-jobs"
	fcApJobName   = "purchasing.ap_revaluation_reversal"
	fcJobAuditAct = "FINANCE_JOB_ENABLED_SET"
)

// fcJobRig is the served mux with the jobs door wired over the real repository and the real audit
// service (behind the seam that can be told to fail), and a runner whose only job is a counter, so a
// tick never presses a real run.
type fcJobRig struct {
	*fcRoutes
	repo   *jobs.Repository
	audit  *fcTxAudit
	runner *jobs.Runner
	ran    atomic.Int64
}

func fcJobRigNew(t *testing.T) *fcJobRig {
	t.Helper()
	r := fcRigNew(t)
	aud := r.realAudit()
	repo := jobs.NewRepository(r.pool)
	fr := NewRepository(r.pool)
	svc := NewFinanceChargeService(r.writer(nil).WithAudit(aud), NewFinanceChargeWaiverService(fr).WithAudit(aud), fr).
		WithJobs(repo, []string{FinanceChargeJobName, fcApJobName}, aud)
	sg := testkit.NewStaffGuards(t)
	mux := http.NewServeMux()
	NewFinanceChargeRouteHandler(svc).WithUserLinker(func(context.Context, string) (string, bool) {
		return fc1Controller, true
	}).RegisterRoutes(mux, sg.Guards)
	j := &fcJobRig{fcRoutes: &fcRoutes{rig: r, sg: sg, mux: mux, audit: &fcAuditStub{}, svc: svc}, repo: repo, audit: aud}
	j.runner = jobs.NewRunner(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, name := range []string{FinanceChargeJobName, fcApJobName} {
		j.runner.Register(name, func(context.Context) (jobs.Result, error) {
			j.ran.Add(1)
			return jobs.Result{}, nil
		})
	}

	// Put the schedule rows back exactly as found, drop the runs the ticks recorded and the audit rows
	// the toggles wrote, every statement failing the test loudly.
	type saved struct {
		name    string
		enabled bool
		next    *time.Time
		last    *time.Time
		status  string
		errText string
	}
	var rows []saved
	for _, name := range []string{FinanceChargeJobName, fcApJobName} {
		var s saved
		s.name = name
		if err := r.pool.QueryRow(r.ctx,
			`SELECT enabled, next_run_at, last_run_at, last_status, last_error FROM job_schedules WHERE name = $1`, name,
		).Scan(&s.enabled, &s.next, &s.last, &s.status, &s.errText); err != nil {
			t.Fatalf("read the %s schedule: %v", name, err)
		}
		rows = append(rows, s)
	}
	var priorRuns []string
	prs, err := r.pool.Query(r.ctx, `SELECT id::text FROM job_runs WHERE job_name = ANY($1::text[])`, []string{FinanceChargeJobName, fcApJobName})
	if err != nil {
		t.Fatalf("read the prior job runs: %v", err)
	}
	for prs.Next() {
		var id string
		if err := prs.Scan(&id); err != nil {
			t.Fatalf("scan a prior job run: %v", err)
		}
		priorRuns = append(priorRuns, id)
	}
	prs.Close()
	t.Cleanup(func() {
		fcDelete(t, r.pool, "job_runs", `DELETE FROM job_runs WHERE job_name = ANY($1::text[]) AND NOT (id::text = ANY($2::text[]))`,
			[]string{FinanceChargeJobName, fcApJobName}, priorRuns)
		for _, s := range rows {
			fcDelete(t, r.pool, "job_schedules", `UPDATE job_schedules SET enabled = $2, next_run_at = $3, last_run_at = $4, last_status = $5, last_error = $6 WHERE name = $1`,
				s.name, s.enabled, s.next, s.last, s.status, s.errText)
		}
		fcDelete(t, r.pool, "audit_log", `DELETE FROM audit_log WHERE action = $1 AND actor_id = $2::uuid`, fcJobAuditAct, fc1Controller)
	})
	return j
}

// schedule reads a job's row through the door's own list.
func (j *fcJobRig) listed(t *testing.T, name string) map[string]any {
	t.Helper()
	rec := j.call(fcController, "GET", fcJobsPath, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("W3-FC1c red: the jobs list = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var out struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("W3-FC1c red: the jobs list is not json: %v", err)
	}
	for _, it := range out.Items {
		if it["name"] == name {
			return it
		}
	}
	t.Fatalf("W3-FC1c red: the jobs list %s has no %s", rec.Body.String(), name)
	return nil
}

func (j *fcJobRig) put(auth erpidentity.StaffAuthority, name, body string) *httptest.ResponseRecorder {
	return j.call(auth, "PUT", fcJobsPath+"/"+name, body)
}

func (j *fcJobRig) setNext(t *testing.T, name, sql string) {
	t.Helper()
	fcDelete(t, j.rig.pool, "job_schedules", `UPDATE job_schedules SET next_run_at = `+sql+` WHERE name = $1`, name)
}

// Enable arms the job's next slot and a running worker's tick runs it, once.
func TestIntegration_W3FC1C_EnableThenOneTickRunsItOnce(t *testing.T) {
	j := fcJobRigNew(t)
	// The seed is off with no next slot.
	if it := j.listed(t, FinanceChargeJobName); it["enabled"] != false {
		t.Fatalf("W3-FC1c red: the finance charge job is seeded %v, want disabled", it["enabled"])
	}
	before := time.Now()
	rec := j.put(fcController, FinanceChargeJobName, `{"enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("W3-FC1c red: enable = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	it := j.listed(t, FinanceChargeJobName)
	next, err := time.Parse(time.RFC3339, it["next_run_at"].(string))
	if it["enabled"] != true || err != nil || !next.After(before) || next.After(before.Add(25*time.Hour)) {
		t.Fatalf("W3-FC1c red: after enable the row reads %v, want enabled with the next slot within a day", it)
	}
	// Nothing is due yet, so a tick runs nothing; once the slot comes, one tick runs the job once.
	if n := j.runner.RunDue(j.rig.ctx); n != 0 || j.ran.Load() != 0 {
		t.Fatalf("W3-FC1c red: a tick before the slot ran %d jobs (%d calls)", n, j.ran.Load())
	}
	j.setNext(t, FinanceChargeJobName, `now() - interval '1 minute'`)
	if n := j.runner.RunDue(j.rig.ctx); n != 1 || j.ran.Load() != 1 {
		t.Fatalf("W3-FC1c red: the tick after enable ran %d jobs (%d calls), want exactly 1", n, j.ran.Load())
	}
	if n := j.runner.RunDue(j.rig.ctx); n != 0 || j.ran.Load() != 1 {
		t.Fatalf("W3-FC1c red: a second tick ran the job again (%d, %d calls)", n, j.ran.Load())
	}
}

// Enable replaces a next slot that is already past, so an old stamp never fires a burst, and keeps a
// future one.
func TestIntegration_W3FC1C_EnableRearmsAPastSlotAndKeepsAFutureOne(t *testing.T) {
	j := fcJobRigNew(t)
	j.setNext(t, FinanceChargeJobName, `now() - interval '3 days'`)
	if rec := j.put(fcController, FinanceChargeJobName, `{"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("W3-FC1c red: enable over a past slot = %d (%s)", rec.Code, rec.Body.String())
	}
	if n := j.runner.RunDue(j.rig.ctx); n != 0 || j.ran.Load() != 0 {
		t.Fatalf("W3-FC1c red: an enabled job with a stale slot fired at once (%d, %d calls)", n, j.ran.Load())
	}
	first := j.listed(t, FinanceChargeJobName)["next_run_at"]
	if rec := j.put(fcController, FinanceChargeJobName, `{"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("W3-FC1c red: a second enable = %d", rec.Code)
	}
	if again := j.listed(t, FinanceChargeJobName)["next_run_at"]; again != first {
		t.Fatalf("W3-FC1c red: a second enable moved the slot %v to %v", first, again)
	}
}

// Disable: the job is never claimed, even with a slot long past.
func TestIntegration_W3FC1C_DisableThenATickRunsNothing(t *testing.T) {
	j := fcJobRigNew(t)
	if rec := j.put(fcController, FinanceChargeJobName, `{"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("W3-FC1c red: enable = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := j.put(fcController, FinanceChargeJobName, `{"enabled":false}`); rec.Code != http.StatusOK {
		t.Fatalf("W3-FC1c red: disable = %d (%s)", rec.Code, rec.Body.String())
	}
	if it := j.listed(t, FinanceChargeJobName); it["enabled"] != false {
		t.Fatalf("W3-FC1c red: after disable the row reads %v", it)
	}
	j.setNext(t, FinanceChargeJobName, `now() - interval '1 hour'`)
	if n := j.runner.RunDue(j.rig.ctx); n != 0 || j.ran.Load() != 0 {
		t.Fatalf("W3-FC1c red: a disabled job ran on a tick (%d jobs, %d calls)", n, j.ran.Load())
	}
}

// The gate: only the Controller roles toggle or list; others are 403 with no change and no
// audit row. The General Manager is a Controller role here (only the waiver is narrower).
func TestIntegration_W3FC1C_TheGateRefusesEveryoneButTheController(t *testing.T) {
	j := fcJobRigNew(t)
	branchMgr := erpidentity.StaffAuthority{RoleID: "BRANCH_MANAGER", Tier: erpidentity.TierManager, Scope: erpidentity.ScopeBranch, Branches: []string{j.rig.branch}}
	clerk := erpidentity.StaffAuthority{RoleID: "COUNTER_SALES", Tier: erpidentity.TierFrontline, Scope: erpidentity.ScopeBranch, Branches: []string{j.rig.branch}}
	for name, who := range map[string]erpidentity.StaffAuthority{"clerk": clerk, "branch manager": branchMgr, "hr": fcHR} {
		for _, c := range []struct{ method, path, body string }{
			{"GET", fcJobsPath, ""},
			{"PUT", fcJobsPath + "/" + FinanceChargeJobName, `{"enabled":true}`},
		} {
			rec := j.call(who, c.method, c.path, c.body)
			// The route gate refuses a non leadership caller bare; HR is leadership and is refused by the
			// handler's Controller narrowing, in words.
			if rec.Code != http.StatusForbidden || (name == "hr" && !strings.Contains(rec.Body.String(), "Controller")) {
				t.Fatalf("W3-FC1c red: %s on %s %s = %d (%s), want 403 in words", name, c.method, c.path, rec.Code, rec.Body.String())
			}
		}
	}
	if it := j.listed(t, FinanceChargeJobName); it["enabled"] != false {
		t.Fatalf("W3-FC1c red: a refused caller changed the job: %v", it)
	}
	if n := len(j.rig.auditRows(fcJobAuditAct, j.scheduleID(t, FinanceChargeJobName))); n != 0 {
		t.Fatalf("W3-FC1c red: refused callers left %d audit rows", n)
	}
	// The General Manager and the platform admin are Controller roles for the door.
	gm := j.put(fcGM, FinanceChargeJobName, `{"enabled":true}`)
	if gm.Code != http.StatusOK {
		t.Fatalf("W3-FC1c red: the General Manager's enable = %d (%s), want 200", gm.Code, gm.Body.String())
	}
}

// An unknown name, a name off the allowlist and a listed name with no schedule row are 404 in words;
// a body without the flag is 400.
func TestIntegration_W3FC1C_UnknownNamesAre404AndABadBodyIs400(t *testing.T) {
	j := fcJobRigNew(t)
	for _, name := range []string{"billing.nope", "notify.dispatch", "BILLING.FINANCE_CHARGE_RUN"} {
		rec := j.put(fcController, name, `{"enabled":true}`)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not a finance job") {
			t.Fatalf("W3-FC1c red: PUT %s = %d (%s), want 404 in words", name, rec.Code, rec.Body.String())
		}
	}
	for _, body := range []string{`{}`, `{"enabled":"yes"}`, `nope`} {
		rec := j.put(fcController, FinanceChargeJobName, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("W3-FC1c red: PUT body %q = %d (%s), want 400", body, rec.Code, rec.Body.String())
		}
	}
	// On the allowlist but with no row in job_schedules.
	fr := NewRepository(j.rig.pool)
	svc := NewFinanceChargeService(j.rig.writer(nil), NewFinanceChargeWaiverService(fr), fr).
		WithJobs(j.repo, []string{"billing.not_seeded"}, nil)
	sg := testkit.NewStaffGuards(t)
	mux := http.NewServeMux()
	NewFinanceChargeRouteHandler(svc).WithUserLinker(func(context.Context, string) (string, bool) { return fc1Controller, true }).RegisterRoutes(mux, sg.Guards)
	other := &fcRoutes{rig: j.rig, sg: sg, mux: mux, svc: svc}
	if rec := other.call(fcController, "PUT", fcJobsPath+"/billing.not_seeded", `{"enabled":true}`); rec.Code != http.StatusNotFound {
		t.Fatalf("W3-FC1c red: a listed name with no schedule row = %d (%s), want 404", rec.Code, rec.Body.String())
	}
}

func (j *fcJobRig) scheduleID(t *testing.T, name string) string {
	t.Helper()
	var id string
	if err := j.rig.pool.QueryRow(j.rig.ctx, `SELECT id::text FROM job_schedules WHERE name = $1`, name).Scan(&id); err != nil {
		t.Fatalf("read the %s schedule id: %v", name, err)
	}
	return id
}

// The audit row reads back from the real audit service: who (the linked staff member), which job, and
// the flag before and after, one row a toggle, in order.
func TestIntegration_W3FC1C_TheToggleIsRecordedOnTheRealAuditService(t *testing.T) {
	j := fcJobRigNew(t)
	id := j.scheduleID(t, FinanceChargeJobName)
	for _, body := range []string{`{"enabled":true}`, `{"enabled":false}`} {
		if rec := j.put(fcController, FinanceChargeJobName, body); rec.Code != http.StatusOK {
			t.Fatalf("W3-FC1c red: PUT %s = %d (%s)", body, rec.Code, rec.Body.String())
		}
	}
	rows := j.rig.auditRows(fcJobAuditAct, id)
	if len(rows) != 2 {
		t.Fatalf("W3-FC1c red: %d audit rows for two toggles, want 2: %v", len(rows), rows)
	}
	want := [][2]bool{{false, true}, {true, false}}
	for i, w := range want {
		row := rows[i]
		if row["job"] != FinanceChargeJobName || row["enabled_from"] != w[0] || row["enabled_to"] != w[1] {
			t.Fatalf("W3-FC1c red: audit row %d = %v, want job %s from %v to %v", i, row, FinanceChargeJobName, w[0], w[1])
		}
	}
	var actor string
	if err := j.rig.pool.QueryRow(j.rig.ctx,
		`SELECT actor_id::text FROM audit_log WHERE action = $1 AND entity_id::text = $2 ORDER BY seq LIMIT 1`, fcJobAuditAct, id).Scan(&actor); err != nil || actor != fc1Controller {
		t.Fatalf("W3-FC1c red: the audit row's actor = %q, %v, want the linked Controller", actor, err)
	}
}

// A record that fails rolls the toggle back: the row is unchanged and the caller sees a failure.
func TestIntegration_W3FC1C_AFailingRecordRollsTheToggleBack(t *testing.T) {
	j := fcJobRigNew(t)
	j.audit.failTx = true
	rec := j.put(fcController, FinanceChargeJobName, `{"enabled":true}`)
	if rec.Code < 500 {
		t.Fatalf("W3-FC1c red: a toggle whose record failed answered %d (%s)", rec.Code, rec.Body.String())
	}
	j.audit.failTx = false
	it := j.listed(t, FinanceChargeJobName)
	if it["enabled"] != false || it["next_run_at"] != nil {
		t.Fatalf("W3-FC1c red: a toggle whose record failed left the row %v", it)
	}
}
