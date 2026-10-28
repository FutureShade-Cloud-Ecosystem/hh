//go:build integration

package main

// w2_6b_counter_reason_codes_integration_test.go, W2-6b: the counter reason codes.
//
// A till paid in or paid out posted to 9999 Suspense against the drawer with free text reason words,
// and the suspense balance filled with money no one had classified. A branch now keeps a list of the
// codes the drawer names on a paid in or a paid out, and each code points at the GL account that
// movement posts to.
//
// The legs run on a real migrated database seeded with the default build's Ontario Dibbits seed,
// every step through the real services wired exactly as cmd/core wires them (wireMoney and
// wireOperations over one mux; no fakes for money), and the GL consumer's posting loop run
// synchronously after each step, exactly as worker.go registers it, so every outbox event becomes a
// journal before the next assertion.
//
// The legs:
//   - the starter list the seed gives a branch, read through the till's own API as a manager
//   - the reason for the item: a paid out with the postage code posts DR 5100 and CR 1020, with
//     nothing in 9999 Suspense
//   - every journal line of that movement carries a branch (0446)
//   - each refusal: an unknown code, an inactive code, another branch's code, the wrong kind, and a
//     missing code where the branch has codes
//   - a movement with no code on a branch with no codes still posts to 9999 Suspense
//   - the list is a manager's: a front line caller is refused the read
//
//	Run: DATABASE_URL='postgres://colton:fw1@127.0.0.1:57602/postgres?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run W2B6CounterReasonCodes -count=1 -v

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// The legs' fixed facts: the org the walk runs in and the two branches the seed plants.
const (
	w2b6OrgID   = "88a20000-0000-4000-8000-0000000000f6"
	w2b6OrgSlug = "w2b6-codes"
	w2b6TRT     = "5d1b3f6e-0000-4000-9000-000000000001"
	w2b6KGN     = "5d1b3f6e-0000-4000-9000-000000000002"
)

// w2b6Walk is the legs' world: a real database, the production wiring over one mux, and the GL
// consumer's posting loop.
type w2b6Walk struct {
	t     *testing.T
	pool  *pgxpool.Pool
	mux   *http.ServeMux
	sg    *testkit.StaffGuards
	disp  *notify.Dispatcher
	token string
}

// TestW2B6CounterReasonCodes is the item's exit test.
func TestW2B6CounterReasonCodes(t *testing.T) {
	w := newW2B6Walk(t)

	// 1. The starter list the seed gives the branch. Every branch begins with the same three codes,
	//    each pointing at an expense account of its own, and none of them live yet: a branch turns a
	//    code on through the till's own list API, one branch at a time.
	items := w.listReasonCodes(w2b6TRT)
	if len(items) != 3 {
		t.Fatalf("the seed gave the branch %d reason codes, want 3: %+v", len(items), items)
	}
	want := map[string]struct{ account, kind string }{
		"POSTAGE":     {"6400", "PAID_OUT"},
		"CLEANING":    {"6500", "PAID_OUT"},
		"STAFF_MEALS": {"6600", "PAID_OUT"},
	}
	for _, c := range items {
		w := want[c.Code]
		if w.account == "" {
			t.Errorf("the seed gave the branch a code this leg does not name: %s", c.Code)
			continue
		}
		if c.GLAccountCode != w.account || c.MovementKind != w.kind {
			t.Errorf("the seed's %s points at %s/%s, want %s/%s", c.Code, c.GLAccountCode, c.MovementKind, w.account, w.kind)
		}
		if c.Active {
			t.Errorf("the seed's %s is live; the starter list ships off until a manager turns it on", c.Code)
		}
	}

	// 2. The list is a manager's. A front line caller is refused the read.
	w.sg.SetAuthority(coreid.StaffAuthority{RoleID: "sales", Tier: coreid.TierFrontline, Scope: coreid.ScopeGlobal})
	if code, _ := w.call(http.MethodGet, "/api/v1/till/reason-codes?branch_id="+w2b6TRT, nil); code != http.StatusForbidden {
		t.Errorf("a front line caller read the reason code list with %d, want 403", code)
	}
	w.sg.SetAuthority(coreid.StaffAuthority{RoleID: "financial_controller", Tier: coreid.TierLeadership, Scope: coreid.ScopeGlobal})

	// 3. A manager turns the postage code on. That is the whole adoption: from here the branch
	//    names its paid outs.
	postage := w.codeNamed(t, w2b6TRT, "POSTAGE")
	w.editReasonCode(t, postage.ID, map[string]any{"active": true})
	if !w.codeNamed(t, w2b6TRT, "POSTAGE").Active {
		t.Fatalf("the postage code is still off after the manager turned it on")
	}

	// 4. The reason for the item. A paid out with the postage code posts DR 6400 (office supplies
	//    and postage) and CR 1020 (undeposited), and nothing at all lands in 9999 Suspense.
	session := w.openSession(t, w2b6TRT)
	w.movement(t, session, map[string]any{
		"movement_type": "PAID_OUT",
		"method":        "CASH",
		"amount_cents":  3000,
		"reason":        "the stamp money",
		"reason_code":   "POSTAGE",
	})
	w.drain()
	movement := w.movementID(t, session)
	legs := w.journalLegs(t, movement)
	if legs["6400"].Debit != 3000 {
		t.Errorf("the postage paid out debited 6400 by %d, want 3000: %+v", legs["6400"].Debit, legs)
	}
	if legs["1020"].Credit != 3000 {
		t.Errorf("the postage paid out credited 1020 by %d, want 3000: %+v", legs["1020"].Credit, legs)
	}
	if l, ok := legs["9999"]; ok && (l.Debit != 0 || l.Credit != 0) {
		t.Errorf("the postage paid out left %+v in 9999 Suspense, want nothing", l)
	}

	// 5. Every journal line of that movement carries a branch (0446).
	var unstamped int
	if err := w.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id
		  WHERE j.source_ref_id = $1::uuid AND l.branch_id IS NULL`, movement).Scan(&unstamped); err != nil {
		t.Fatalf("reading the movement's journal lines: %v", err)
	}
	if unstamped != 0 {
		t.Errorf("%d journal lines of the postage paid out carry no branch", unstamped)
	}
	var stamped string
	if err := w.pool.QueryRow(context.Background(),
		`SELECT l.branch_id::text FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id
		  WHERE j.source_ref_id = $1::uuid LIMIT 1`, movement).Scan(&stamped); err != nil {
		t.Fatalf("reading the movement's journal branch: %v", err)
	}
	if stamped != w2b6TRT {
		t.Errorf("the movement's journal lines carry branch %s, want %s", stamped, w2b6TRT)
	}

	// 6. Each refusal, in words.
	//    An unknown code: nobody's code at all.
	w.refused(t, "an unknown code", session, map[string]any{
		"movement_type": "PAID_OUT", "method": "CASH", "amount_cents": 1000,
		"reason": "a code nobody carries", "reason_code": "NOT_A_CODE",
	})
	//    An inactive code: the branch carries it, but the manager turned it off.
	w.refused(t, "an inactive code", session, map[string]any{
		"movement_type": "PAID_OUT", "method": "CASH", "amount_cents": 1000,
		"reason": "a code that is off", "reason_code": "CLEANING",
	})
	//    Another branch's code: the code belongs to the other yard's list.
	other := w.createReasonCode(t, w2b6KGN, map[string]any{
		"code": "SUPPLIES", "label": "Supplies at the other yard",
		"movement_kind": "PAID_OUT", "gl_account_code": "6200", "active": false,
	})
	if other == "" {
		t.Fatalf("the other branch's code was not created")
	}
	w.refused(t, "another branch's code", session, map[string]any{
		"movement_type": "PAID_OUT", "method": "CASH", "amount_cents": 1000,
		"reason": "the other yard's code", "reason_code": "SUPPLIES",
	})
	//    The wrong kind: postage is a paid out, so it may not name a paid in.
	w.refused(t, "the wrong kind", session, map[string]any{
		"movement_type": "PAID_IN", "method": "CASH", "amount_cents": 1000,
		"reason": "postage on a paid in", "reason_code": "POSTAGE",
	})
	//    A missing code where the branch has codes: the branch turned the postage code on, so a
	//    paid out has to name one.
	w.refused(t, "a missing code where the branch has codes", session, map[string]any{
		"movement_type": "PAID_OUT", "method": "CASH", "amount_cents": 1000,
		"reason": "no code at all",
	})

	// 7. A movement with no code on a branch with no codes still posts to 9999 Suspense. The other
	//    branch's starter codes are all off, so its paid out is still today's movement.
	kgnSession := w.openSession(t, w2b6KGN)
	w.movement(t, kgnSession, map[string]any{
		"movement_type": "PAID_OUT",
		"method":        "CASH",
		"amount_cents":  2000,
		"reason":        "no code on a branch with no codes",
	})
	w.drain()
	kgnLegs := w.journalLegs(t, w.movementID(t, kgnSession))
	if kgnLegs["9999"].Debit != 2000 {
		t.Errorf("the code-less paid out debited 9999 by %d, want 2000: %+v", kgnLegs["9999"].Debit, kgnLegs)
	}
	if kgnLegs["1020"].Credit != 2000 {
		t.Errorf("the code-less paid out credited 1020 by %d, want 2000: %+v", kgnLegs["1020"].Credit, kgnLegs)
	}

	// 8. And the same branch with no list at all behaves the same way: the rule counts the codes a
	//    branch has, not the rows a migration happened to plant.
	if _, err := w.pool.Exec(context.Background(),
		`DELETE FROM till_counter_reason_codes WHERE branch_id = $1::uuid`, w2b6KGN); err != nil {
		t.Fatalf("clearing the other branch's list: %v", err)
	}
	emptySession := w.openSession(t, w2b6KGN)
	w.movement(t, emptySession, map[string]any{
		"movement_type": "PAID_OUT",
		"method":        "CASH",
		"amount_cents":  1500,
		"reason":        "no code on a branch with no list",
	})
	w.drain()
	emptyLegs := w.journalLegs(t, w.movementID(t, emptySession))
	if emptyLegs["9999"].Debit != 1500 {
		t.Errorf("the code-less paid out on a branch with no list debited 9999 by %d, want 1500: %+v", emptyLegs["9999"].Debit, emptyLegs)
	}
}

// w2b6Leg is one account's debit and credit on a movement's journal.
type w2b6Leg struct {
	Debit  int64
	Credit int64
}

// newW2B6Walk builds the legs' world: a real migrated database, the seed, and the production
// wiring over one mux.
func newW2B6Walk(t *testing.T) *w2b6Walk {
	t.Helper()
	ctx := context.Background()
	pool := freshMigratedDB(t, "w2b6")
	body, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("reading the embedded seed: %v", err)
	}
	if _, err := pool.Exec(ctx, string(body)); err != nil {
		t.Fatalf("applying the seed: %v", err)
	}
	// The seed runs before any org exists; give the branches one org and link the caller's platform
	// subject to the seeded financial controller login, exactly as the month walk's fixture does.
	mustExec(t, ctx, pool, `INSERT INTO org (id, slug, name) VALUES ($1, $2, 'W2B6 Codes Org') ON CONFLICT (id) DO NOTHING`, w2b6OrgID, w2b6OrgSlug)
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
	wireOperations(mux, pool, sg.Guards, log, money, "w2b6-fx-ticket", belt.outbox)
	// The GL's own read seams, so the seed's invoice events post instead of dead lettering. The
	// counter reason codes ride the till seam wireOperations already made.
	billingRepo := billing.NewRepository(pool)
	posRepo := pos.NewRepository(pool)
	money.glSvc.WithInvoiceReader(billingRepo).WithARSubledger(billingRepo).WithTaxSplitReader(posRepo)

	// The GL consumer and its neighbours, registered exactly as worker.go registers them, then
	// drained synchronously by drain() after every step.
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

// w2b6Writer sends the log lines to the test's own output.
type w2b6Writer struct{ t *testing.T }

func (w w2b6Writer) Write(p []byte) (int, error) {
	w.t.Logf("%s", p)
	return len(p), nil
}

// call is one authenticated staff call over the served mux.
func (w *w2b6Walk) call(method, path string, body any) (int, []byte) {
	w.t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Authorization", "Bearer "+w.token)
	req.Header.Set(authz.HeaderXOrg, w2b6OrgSlug)
	req.Header.Set("Content-Type", "application/json")
	rsp := httptest.NewRecorder()
	w.mux.ServeHTTP(rsp, req)
	return rsp.Code, rsp.Body.Bytes()
}

// ok is a step call that must succeed.
func (w *w2b6Walk) ok(step, method, path string, body any) []byte {
	w.t.Helper()
	code, out := w.call(method, path, body)
	if code >= 300 {
		w.t.Fatalf("%s: %s %s answered %d: %s", step, method, path, code, out)
	}
	return out
}

// drain runs the GL consumer's posting loop until the outbox is empty, exactly as the month walk
// does after every step.
func (w *w2b6Walk) drain() {
	w.t.Helper()
	for i := 0; i < 30; i++ {
		if w.disp.DrainOnce(context.Background()) == 0 {
			return
		}
	}
}

// listReasonCodes reads a branch's list through the till's own API.
func (w *w2b6Walk) listReasonCodes(branchID string) []w2b6Code {
	w.t.Helper()
	out := w.ok("read the reason codes", http.MethodGet, "/api/v1/till/reason-codes?branch_id="+branchID, nil)
	var body struct {
		Items []w2b6Code `json:"items"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		w.t.Fatalf("the reason code list read: %v (%s)", err, out)
	}
	return body.Items
}

// w2b6Code is one row of the list as the API shows it.
type w2b6Code struct {
	ID            string `json:"id"`
	BranchID      string `json:"branch_id"`
	Code          string `json:"code"`
	Label         string `json:"label"`
	MovementKind  string `json:"movement_kind"`
	GLAccountCode string `json:"gl_account_code"`
	Active        bool   `json:"active"`
}

// codeNamed reads one code off a branch's list.
func (w *w2b6Walk) codeNamed(t *testing.T, branchID, code string) w2b6Code {
	t.Helper()
	for _, c := range w.listReasonCodes(branchID) {
		if c.Code == code {
			return c
		}
	}
	t.Fatalf("the branch %s carries no code %s", branchID, code)
	return w2b6Code{}
}

// editReasonCode edits one row of the list through the till's own API.
func (w *w2b6Walk) editReasonCode(t *testing.T, id string, edit map[string]any) {
	t.Helper()
	w.ok("edit the reason code", http.MethodPut, "/api/v1/till/reason-codes/"+id, edit)
}

// createReasonCode adds a code to a branch's list through the till's own API.
func (w *w2b6Walk) createReasonCode(t *testing.T, branchID string, in map[string]any) string {
	t.Helper()
	in["branch_id"] = branchID
	out := w.ok("create the reason code", http.MethodPost, "/api/v1/till/reason-codes", in)
	var c w2b6Code
	if err := json.Unmarshal(out, &c); err != nil {
		t.Fatalf("the created reason code read: %v (%s)", err, out)
	}
	return c.ID
}

// openSession opens a drawer at a branch.
func (w *w2b6Walk) openSession(t *testing.T, branchID string) string {
	t.Helper()
	out := w.ok("open the till", http.MethodPost, "/api/v1/till/sessions", map[string]any{
		"branch_id": branchID, "station": "MAIN", "float_cents": 20000,
	})
	var body struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &body); err != nil || body.ID == "" {
		t.Fatalf("the till session read: %v (%s)", err, out)
	}
	return body.ID
}

// movement records a drawer movement through the till's own API.
func (w *w2b6Walk) movement(t *testing.T, sessionID string, in map[string]any) {
	t.Helper()
	w.ok("record the movement", http.MethodPost, "/api/v1/till/sessions/"+sessionID+"/movements", in)
}

// refused records a movement the till must refuse, and says the words it refused with.
func (w *w2b6Walk) refused(t *testing.T, what, sessionID string, in map[string]any) {
	t.Helper()
	code, out := w.call(http.MethodPost, "/api/v1/till/sessions/"+sessionID+"/movements", in)
	if code < 400 {
		t.Errorf("the till accepted %s with %d, want a refusal: %s", what, code, out)
		return
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(out, &body); err != nil || strings.TrimSpace(body.Error) == "" {
		t.Errorf("the refusal of %s carries no words: %s", what, out)
	}
}

// movementID reads the session's own movement id, the one the journal points at.
func (w *w2b6Walk) movementID(t *testing.T, sessionID string) string {
	t.Helper()
	var id string
	if err := w.pool.QueryRow(context.Background(),
		`SELECT id::text FROM till_movements WHERE till_session_id = $1::uuid
		  ORDER BY created_at DESC, id DESC LIMIT 1`, sessionID).Scan(&id); err != nil {
		t.Fatalf("reading the movement id: %v", err)
	}
	return id
}

// journalLegs reads the account legs of one movement's journal.
func (w *w2b6Walk) journalLegs(t *testing.T, movementID string) map[string]w2b6Leg {
	t.Helper()
	rows, err := w.pool.Query(context.Background(),
		`SELECT a.code, COALESCE(l.debit_cents, 0), COALESCE(l.credit_cents, 0)
		   FROM gl_journal_lines l
		   JOIN gl_journals j ON j.id = l.journal_id
		   JOIN gl_accounts a ON a.id = l.account_id
		  WHERE j.source_ref_id = $1::uuid AND j.status = 'POSTED'`, movementID)
	if err != nil {
		t.Fatalf("reading the movement's journal legs: %v", err)
	}
	defer rows.Close()
	out := map[string]w2b6Leg{}
	for rows.Next() {
		var code string
		var l w2b6Leg
		if err := rows.Scan(&code, &l.Debit, &l.Credit); err != nil {
			t.Fatalf("scanning the journal leg: %v", err)
		}
		out[code] = l
	}
	return out
}
