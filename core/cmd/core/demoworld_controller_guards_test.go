//go:build demo

package main

// demoworld_controller_guards_test.go, the parked journal's one way to post and the approvals page's
// positional cursor, proven through the real registered routes on the controller's world (PR 300 fix
// round 1). The rule at source: a journal parked over the threshold is posted ONLY through the approvals
// rail (an approved request posts it). The GL's own approve route refuses it while ANY request for it
// exists in a state other than approved and spent, rejected and tombstoned ones included.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customerquote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

type cwGuardWorld struct {
	pool       *pgxpool.Pool
	controller *tillClient
	manager    *tillClient
}

// cwNewGuardWorld builds the controller's world the way the main proof does (migrate, first apply, the real
// populate, second apply, the registered surface) and hands back the clients the guard legs need.
func cwNewGuardWorld(t *testing.T) *cwGuardWorld {
	t.Helper()
	dsn := os.Getenv(tillDemoDBEnvVar)
	if dsn == "" {
		t.Skip(tillDemoDBEnvVar + " is not set: the demo world proof needs its own throwaway local Postgres")
	}
	requireLocalDSN(t, dsn)
	ctx := context.Background()
	rawKey := make([]byte, 32)
	if _, err := rand.Read(rawKey); err != nil {
		t.Fatalf("generate vault key: %v", err)
	}
	pool, testDSN := qwFreshDB(t, ctx, dsn, "demoworld_controller_guards")
	t.Setenv("DATABASE_URL", testDSN)
	t.Setenv("CORE_DATABASE_URL", testDSN)
	t.Setenv("PAYMENT_VAULT_KEY", hex.EncodeToString(rawKey))
	t.Setenv("APP_ENV", "dev")
	qwApply(t)
	now := qwClock
	if _, err := demo.RunPopulate(ctx, demo.Options{Getenv: os.Getenv, Now: &now}); err != nil {
		t.Fatalf("demo populate: %v", err)
	}
	qwApply(t)

	appwrite := newAppwriteFake(t)
	for _, sub := range []string{cwControllerUserID, qwSalesUserID, qwManagerUserID} {
		appwrite.setTeam(sub, "org:"+demoOrgSlug)
	}
	keys := testkit.NewKeys(t)
	acceptKey := make([]byte, 32)
	if _, err := rand.Read(acceptKey); err != nil {
		t.Fatalf("generate acceptance key: %v", err)
	}
	env := &compEnv{
		keys: keys, minter: &testkit.Minter{Keys: keys, Clock: testkit.NewClock()},
		keySrv: testkit.NewKeyServer(t, keys), appwrite: appwrite, dsn: testDSN,
	}
	values := map[string]string{
		"CORE_TOKEN_ISSUER": testkit.KitIssuer, "CORE_STAFF_AUDIENCE": testkit.KitStaffAud,
		"CORE_TENANT_AUDIENCE": testkit.KitTenantAud, "CORE_JWKS_URL": env.keySrv.URL(),
		"CORE_DATABASE_URL": testDSN, "CORE_APPWRITE_ENDPOINT": appwrite.server.URL,
		"CORE_APPWRITE_PROJECT": "fx-appwrite-project", "CORE_APPWRITE_API_KEY": "fx-appwrite-key-placeholder",
		customerquote.EnvKeyID: "fx-accept-sign-key", customerquote.EnvKey: hex.EncodeToString(acceptKey),
	}
	env.getenv = func(k string) string { return values[k] }
	rec, _ := registeredSurface(t, env)
	return &cwGuardWorld{
		pool:       pool,
		controller: &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, cwControllerUserID), who: "Financial Controller"},
		manager:    &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, qwManagerUserID), who: "Sales manager"},
	}
}

// park files a fresh journal over the threshold, made by the Sales manager login, with its
// JE_OVER_THRESHOLD request, by copying the seeded J1 (the controller's world holds no second decider that
// could also make one through the route). It returns the journal id and its request id. The copy is the
// newest request in the queue.
func (w *cwGuardWorld) park(t *testing.T) (journalID, requestID string) {
	t.Helper()
	ctx := context.Background()
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	src := demoID("journal:J1")
	if err := tx.QueryRow(ctx, `
		INSERT INTO gl_journals (id, fiscal_year, entry_no, period_id, source, memo, status, branch_id, created_by)
		SELECT gen_random_uuid(), fiscal_year, next_entry_no(fiscal_year), period_id, source, 'guard leg (demo)', 'DRAFT', branch_id, created_by
		FROM gl_journals WHERE id = $1::uuid RETURNING id::text`, src).Scan(&journalID); err != nil {
		t.Fatalf("copy the parked journal: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO gl_journal_lines (id, journal_id, line_no, account_id, branch_id, debit_cents, credit_cents)
		SELECT gen_random_uuid(), $2::uuid, line_no, account_id, branch_id, debit_cents, credit_cents
		FROM gl_journal_lines WHERE journal_id = $1::uuid`, src, journalID); err != nil {
		t.Fatalf("copy the journal lines: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO permission_approval_requests (id, requested_by, branch_id, policy_type, detail, subject_ref, data, status, requested_at)
		SELECT gen_random_uuid(), requested_by, branch_id, policy_type, detail, $2, data, 'PENDING', now()
		FROM permission_approval_requests WHERE id = $1::uuid RETURNING id::text`, demoID("jerequest:J1"), journalID).Scan(&requestID); err != nil {
		t.Fatalf("copy the journal request: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return journalID, requestID
}

func (w *cwGuardWorld) journalStatus(t *testing.T, id string) string {
	t.Helper()
	rsp := w.controller.call("GET", "/api/v1/gl/journals/"+id, nil)
	rsp.ok(t, http.StatusOK)
	var j cwJournal
	rsp.decode(t, &j)
	return j.Status
}

func (w *cwGuardWorld) requestStatus(t *testing.T, id string) string {
	t.Helper()
	var s string
	if err := w.pool.QueryRow(context.Background(), `SELECT status FROM permission_approval_requests WHERE id = $1::uuid`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (w *cwGuardWorld) decide(id string, approve bool) demoResponse {
	return w.controller.call("POST", "/api/v1/staff/approvals/"+id+"/decide", map[string]any{"approve": approve, "reason": "guard leg"})
}

func (w *cwGuardWorld) glApprove(id string) demoResponse {
	return w.controller.call("POST", "/api/v1/gl/journals/"+id+"/approve", nil)
}

// The words each state of the journal's request is refused in (one phrase from each sentence).
const (
	cwWordsPending   = "pending approval request"
	cwWordsRejected  = "was rejected"
	cwWordsDeleted   = "was deleted"
	cwWordsDiffers   = "different amount"
	cwWordsNoRequest = "has no approval request"
)

// refusedAndParked asserts the GL route refused with the 409 in the state's own words, naming the approvals
// queue, and wrote nothing.
func (w *cwGuardWorld) refusedAndParked(t *testing.T, what, journalID, words string, rsp demoResponse) {
	t.Helper()
	rsp.ok(t, http.StatusConflict)
	if !strings.Contains(string(rsp.body), words) {
		t.Errorf("%s: the refusal does not carry its own words %q: %s", what, words, rsp.body)
	}
	if !strings.Contains(string(rsp.body), "approvals queue") {
		t.Errorf("%s: the refusal does not say where it is decided: %s", what, rsp.body)
	}
	if got := w.journalStatus(t, journalID); got != "DRAFT" {
		t.Errorf("%s: the journal reads %s, want DRAFT", what, got)
	}
}

func TestDemoWorld_ControllerGuards(t *testing.T) {
	w := cwNewGuardWorld(t)

	t.Run("upper case id on the GL route is refused while a request is pending", func(t *testing.T) {
		j, req := w.park(t)
		w.refusedAndParked(t, "lower case", j, cwWordsPending, w.glApprove(j))
		w.refusedAndParked(t, "upper case", j, cwWordsPending, w.glApprove(strings.ToUpper(j)))
		if got := w.requestStatus(t, req); got != "PENDING" {
			t.Errorf("the request reads %s after the refused routes, want PENDING", got)
		}
	})

	t.Run("a rejected request keeps the journal off the GL route", func(t *testing.T) {
		j, req := w.park(t)
		w.decide(req, false).ok(t, http.StatusOK)
		if got := w.requestStatus(t, req); got != "REJECTED" {
			t.Fatalf("the request reads %s after the reject, want REJECTED", got)
		}
		w.refusedAndParked(t, "after reject, lower case", j, cwWordsRejected, w.glApprove(j))
		w.refusedAndParked(t, "after reject, upper case", j, cwWordsRejected, w.glApprove(strings.ToUpper(j)))
		// The other route that could post it: deciding the rejected request again as an approval.
		w.decide(req, true).ok(t, http.StatusConflict)
		if got := w.journalStatus(t, j); got != "DRAFT" {
			t.Errorf("a second decision on the rejected request left the journal %s, want DRAFT", got)
		}
	})

	t.Run("a tombstoned request does not free the journal", func(t *testing.T) {
		j, req := w.park(t)
		if _, err := w.pool.Exec(context.Background(), `UPDATE permission_approval_requests SET deleted_at = now() WHERE id = $1::uuid`, req); err != nil {
			t.Fatal(err)
		}
		w.refusedAndParked(t, "after tombstone", j, cwWordsDeleted, w.glApprove(j))
	})

	t.Run("a tombstoned rejected request does not free the journal", func(t *testing.T) {
		j, req := w.park(t)
		w.decide(req, false).ok(t, http.StatusOK)
		if _, err := w.pool.Exec(context.Background(), `UPDATE permission_approval_requests SET deleted_at = now() WHERE id = $1::uuid`, req); err != nil {
			t.Fatal(err)
		}
		w.refusedAndParked(t, "after reject and tombstone", j, cwWordsDeleted, w.glApprove(j))
	})

	t.Run("the rail's own approve posts once and repeats change nothing", func(t *testing.T) {
		j, req := w.park(t)
		w.decide(req, true).ok(t, http.StatusOK)
		if got := w.journalStatus(t, j); got != "POSTED" {
			t.Fatalf("the journal reads %s after the rail approve, want POSTED", got)
		}
		w.decide(req, true).ok(t, http.StatusConflict)
		w.glApprove(j).ok(t, http.StatusConflict)
		w.glApprove(strings.ToUpper(j)).ok(t, http.StatusConflict)
		var posted int
		if err := w.pool.QueryRow(context.Background(), `SELECT count(*) FROM gl_journals WHERE id = $1::uuid AND status = 'POSTED' AND approved_by = $2::uuid`, j, cwControllerUserID).Scan(&posted); err != nil || posted != 1 {
			t.Errorf("the journal is posted %d times by the controller (%v), want once", posted, err)
		}
	})

	t.Run("a journal over the threshold with no request at all is refused", func(t *testing.T) {
		j, req := w.park(t)
		if _, err := w.pool.Exec(context.Background(), `DELETE FROM permission_approval_requests WHERE id = $1::uuid`, req); err != nil {
			t.Fatal(err)
		}
		w.refusedAndParked(t, "no request, lower case", j, cwWordsNoRequest, w.glApprove(j))
		w.refusedAndParked(t, "no request, upper case", j, cwWordsNoRequest, w.glApprove(strings.ToUpper(j)))
	})

	t.Run("an approved request for a different amount does not post the journal", func(t *testing.T) {
		j, req := w.park(t)
		// The rail holds an APPROVED request, but for 700000; the journal carries 750000.
		if _, err := w.pool.Exec(context.Background(),
			`UPDATE permission_approval_requests SET status = 'APPROVED', decided_by = $2::uuid, data = jsonb_set(data, '{amount_cents}', '700000') WHERE id = $1::uuid`,
			req, cwControllerUserID); err != nil {
			t.Fatal(err)
		}
		w.refusedAndParked(t, "approved for another amount", j, cwWordsDiffers, w.glApprove(j))
	})

	t.Run("an approved request for the journal's current amount posts it once", func(t *testing.T) {
		j, req := w.park(t)
		if _, err := w.pool.Exec(context.Background(),
			`UPDATE permission_approval_requests SET status = 'APPROVED', decided_by = $2::uuid WHERE id = $1::uuid`, req, cwControllerUserID); err != nil {
			t.Fatal(err)
		}
		w.glApprove(j).ok(t, http.StatusOK)
		if got := w.journalStatus(t, j); got != "POSTED" {
			t.Errorf("the journal reads %s, want POSTED", got)
		}
		w.glApprove(j).ok(t, http.StatusConflict)
	})
}
