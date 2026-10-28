//go:build integration

package main

// w2_6b_fix_reason_code_account_integration_test.go, W2-6b fix round 1: the account a reason code names
// must be an account the chart holds and keeps live.
//
// Round 1 took any string for a reason code's account. A manager could point a code at 9998, an account
// nobody owns, and the till took the code. The drawer only found out at post time, when the GL refused
// the journal with "unknown account code", and the movement's money stayed unposted. A manager writes
// the list before the drawer needs it, so the refusal belongs where the code is written: the till asks
// the chart about the account and refuses in words when the code names no account, or names one that has
// been turned off.
//
// The legs:
//   - a code pointing at an account that is not in the chart is refused, in words
//   - a code repointed at an account that has been turned off is refused, in words, and the code keeps the
//     account it had
//   - a code pointing at a live account is still accepted, and a movement naming it posts there
//   - a code whose account was turned off after the code was written still refuses at post time, as today
//
// The legs run on a real migrated database seeded with the default build's Ontario Dibbits seed, every
// step through the real services wired exactly as cmd/core wires them, and the GL consumer's posting loop
// run synchronously after each step, exactly as worker.go registers it.
//
//	Run: DATABASE_URL='postgres://postgres@127.0.0.1:57720/hh?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run W2B6FixReasonCodeAccount -count=1 -v

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// TestW2B6FixReasonCodeAccount is the fix's exit test: the account a reason code names is checked when the
// code is written, not when the drawer finally posts.
func TestW2B6FixReasonCodeAccount(t *testing.T) {
	w := newW2B6FixWalk(t)

	// 1. The account is not in the chart. Nobody owns 9998, so the code names nowhere. The till refuses it
	//    and says so in words, so the manager knows what to fix.
	code, out := w.call(http.MethodPost, "/api/v1/till/reason-codes", map[string]any{
		"branch_id": w2b6TRT, "code": "GHOST", "label": "An account nobody owns",
		"movement_kind": "PAID_OUT", "gl_account_code": "9998", "active": false,
	})
	if code != http.StatusUnprocessableEntity {
		t.Errorf("the till answered a code pointing at 9998 with %d, want 422: %s", code, out)
	}
	if !strings.Contains(string(out), "not in the chart") {
		t.Errorf("the refusal of a code pointing at 9998 does not say the account is not in the chart: %s", out)
	}
	if _, held := w.codeMaybe(w2b6TRT, "GHOST"); held {
		t.Errorf("the till kept the code pointing at 9998 after refusing it")
	}

	// 2. The account is in the chart but has been turned off. The manager writes a code that points at a
	//    live account, the account is turned off through the GL's own API, and the manager repoints the
	//    code at it. The till refuses, in words, and the code keeps the account it had: a refusal leaves
	//    nothing behind.
	w.plantAccount(t, "9996", "A counter account about to be retired")
	retired := w.createReasonCode(t, w2b6TRT, map[string]any{
		"code": "RETIRED", "label": "Postage at a retired account",
		"movement_kind": "PAID_OUT", "gl_account_code": "6200", "active": false,
	})
	w.retireAccount(t, "9996")
	code, out = w.call(http.MethodPut, "/api/v1/till/reason-codes/"+retired, map[string]any{
		"gl_account_code": "9996",
	})
	if code != http.StatusUnprocessableEntity {
		t.Errorf("the till answered a code repointed at the retired 9996 with %d, want 422: %s", code, out)
	}
	if !strings.Contains(string(out), "inactive") {
		t.Errorf("the refusal of a code repointed at the retired 9996 does not say the account is inactive: %s", out)
	}
	kept := w.codeNamed(t, w2b6TRT, "RETIRED")
	if kept.GLAccountCode != "6200" {
		t.Errorf("the refused repoint left the code pointing at %s, want the 6200 it had", kept.GLAccountCode)
	}

	// 3. A real active account still works. The code points at 6200, the manager turns it on, and a paid
	//    out naming it posts DR 6200 and CR 1020, with nothing at all in 9999 Suspense.
	w.editReasonCode(t, retired, map[string]any{"active": true})
	session := w.openSession(t, w2b6TRT)
	w.movement(t, session, map[string]any{
		"movement_type": "PAID_OUT",
		"method":        "CASH",
		"amount_cents":  3000,
		"reason":        "the postage money",
		"reason_code":   "RETIRED",
	})
	w.drain()
	legs := w.journalLegs(t, w.movementID(t, session))
	if legs["6200"].Debit != 3000 {
		t.Errorf("the paid out with the code debited 6200 by %d, want 3000: %+v", legs["6200"].Debit, legs)
	}
	if legs["1020"].Credit != 3000 {
		t.Errorf("the paid out with the code credited 1020 by %d, want 3000: %+v", legs["1020"].Credit, legs)
	}
	if l, ok := legs["9999"]; ok && (l.Debit != 0 || l.Credit != 0) {
		t.Errorf("the paid out with the code left %+v in 9999 Suspense, want nothing", l)
	}

	// 4. A code written while its account was live still refuses at post time when the account is turned
	//    off afterwards, as today. The write time check cannot replace the post time check: the account
	//    can be turned off at any moment after the code was written.
	w.plantAccount(t, "9997", "A counter account turned off later")
	later := w.createReasonCode(t, w2b6TRT, map[string]any{
		"code": "LATER", "label": "Postage at an account turned off later",
		"movement_kind": "PAID_OUT", "gl_account_code": "9997", "active": false,
	})
	w.editReasonCode(t, later, map[string]any{"active": true})
	w.retireAccount(t, "9997")
	lateSession := w.openSession(t, w2b6TRT)
	w.movement(t, lateSession, map[string]any{
		"movement_type": "PAID_OUT",
		"method":        "CASH",
		"amount_cents":  2000,
		"reason":        "the postage money, after the account was turned off",
		"reason_code":   "LATER",
	})
	w.drain()
	lateMovement := w.movementID(t, lateSession)
	if lateLegs := w.journalLegs(t, lateMovement); len(lateLegs) != 0 {
		t.Errorf("the movement whose account was turned off posted %+v, want no journal at all", lateLegs)
	}
	var lastErr string
	if err := w.pool.QueryRow(context.Background(),
		`SELECT last_error FROM events_outbox
		  WHERE topic = 'till.movement.posted' AND payload->>'movement_id' = $1
		  ORDER BY occurred_at DESC LIMIT 1`, lateMovement).Scan(&lastErr); err != nil {
		t.Fatalf("reading the outbox row of the movement: %v", err)
	}
	if !strings.Contains(lastErr, "inactive") {
		t.Errorf("the posting of a movement whose account was turned off said %q, want the inactive account named", lastErr)
	}
}

// newW2B6FixWalk is the fix legs' world: a real migrated database, the seed, and the production wiring
// over one mux. It is newW2B6Walk's world on its own database, so the fix's legs and round 1's legs can
// run in one package run without fighting over one database.
func newW2B6FixWalk(t *testing.T) *w2b6Walk {
	t.Helper()
	ctx := context.Background()
	pool := freshMigratedDB(t, "w2b6fix")
	body, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("reading the embedded seed: %v", err)
	}
	if _, err := pool.Exec(ctx, string(body)); err != nil {
		t.Fatalf("applying the seed: %v", err)
	}
	mustExec(t, ctx, pool, `INSERT INTO org (id, slug, name) VALUES ($1, $2, 'W2B6 Fix Org') ON CONFLICT (id) DO NOTHING`, w2b6OrgID, w2b6OrgSlug)
	mustExec(t, ctx, pool, `UPDATE locations SET org_id = $1 WHERE loc_type = 'BRANCH'`, w2b6OrgID)
	mustExec(t, ctx, pool, `UPDATE users SET platform_user_id = $1 WHERE id = 'd7000000-0000-4000-9000-000000000006'`, testkit.SubSalesManager)

	sg := testkit.NewStaffGuards(t)
	sg.Harness.Membership.SetMember(testkit.SubSalesManager, w2b6OrgSlug, true)
	sg.Harness.Resolver.Set(w2b6OrgSlug, w2b6OrgID)
	sg.SetAuthority(coreid.StaffAuthority{RoleID: "financial_controller", Tier: coreid.TierLeadership, Scope: coreid.ScopeGlobal})

	log := slog.New(slog.NewTextHandler(w2b6Writer{t}, nil))
	mux := http.NewServeMux()
	belt := buildSmsBelt(pool, log)
	money := wireMoney(mux, pool, sg.Guards, log, "", mustRoleConfigForWire())
	wireOperations(mux, pool, sg.Guards, log, money, "w2b6fix-fx-ticket", belt.outbox)
	// The GL's own read seams, so the seed's invoice events post instead of dead lettering, exactly as
	// the round 1 legs' world does.
	billingRepo := billing.NewRepository(pool)
	posRepo := pos.NewRepository(pool)
	money.glSvc.WithInvoiceReader(billingRepo).WithARSubledger(billingRepo).WithTaxSplitReader(posRepo)

	d := notify.NewDispatcher(money.outboxStore, log)
	money.glSvc.RegisterPostingHandlers(d)

	return &w2b6Walk{
		t:     t,
		pool:  pool,
		mux:   mux,
		sg:    sg,
		disp:  d,
		token: sg.Harness.Minter.StaffToken(t, testkit.SubSalesManager),
	}
}

// codeMaybe reads one code off a branch's list, if the list holds it.
func (w *w2b6Walk) codeMaybe(branchID, code string) (w2b6Code, bool) {
	w.t.Helper()
	for _, c := range w.listReasonCodes(branchID) {
		if c.Code == code {
			return c, true
		}
	}
	return w2b6Code{}, false
}

// plantAccount adds an account to the chart through the GL's own API.
func (w *w2b6Walk) plantAccount(t *testing.T, code, name string) {
	t.Helper()
	w.ok("plant the account", http.MethodPost, "/api/v1/gl/accounts", map[string]any{
		"code": code, "name": name, "account_type": "EXPENSE", "normal_balance": "DEBIT",
	})
}

// retireAccount turns an account off through the GL's own API.
func (w *w2b6Walk) retireAccount(t *testing.T, code string) {
	t.Helper()
	w.ok("turn the account off", http.MethodPost, "/api/v1/gl/accounts/"+code+"/deactivate", nil)
}
