package assist

// handler_test.go, the HTTP surface, exercised through the REAL routes with a REAL identity
// context, and the generator for the walkthrough fixtures.
//
// WHY THESE TESTS EXIST SEPARATELY FROM service_test.go. Everything below this file's seam is
// already covered against fakes; what is covered HERE is the seam itself, the one conversion from
// identity.StaffContext to Caller, the status a refusal is reported with, and the two places the
// handler (not the service) decides something: stripping Usage from a caller who may not see spend,
// and turning a nil slice into `[]`.
//
// THE FIXTURES ARE REAL RESPONSES. Set ASSIST_FIXTURE_DIR and this file writes the exact bytes these
// handlers produced into walkthrough-431/fixtures/, which the screenshot harness then serves. That
// makes every shot in walkthrough-431/ a picture of what the SERVER actually returns rather than of
// hand-written JSON that agrees with the frontend because the same person wrote both. When the
// variable is unset, CI, and every ordinary run, nothing is written and these are just tests.
//
//	ASSIST_FIXTURE_DIR=../../../walkthrough-431/fixtures go test ./internal/assist/ -run Fixture

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// asStaff injects a StaffContext the way the real middleware does, so CallerFrom is exercised rather
// than bypassed.
func asStaff(r *http.Request, sc identity.StaffContext) *http.Request {
	return r.WithContext(identity.ContextWithStaff(r.Context(), sc))
}

func staffCtx(userID, role string, tier identity.Tier, scope identity.Scope, branches ...string) identity.StaffContext {
	sc := identity.StaffContext{UserID: userID, RoleCode: role, Tier: tier, Scope: scope, Status: "ACTIVE"}
	if len(branches) > 0 {
		sc.Branches = map[string]bool{}
		for _, b := range branches {
			sc.Branches[b] = true
		}
	}
	return sc
}

// testHandler wires a handler over fakes, with the guards as pass-throughs, the guards themselves
// are the identity package's own tested code, and stubbing them here keeps these tests about the
// assistant.
// Guards-swap carry note (HH-3.2 fold): the pass-through mux became a
// direct mux over the guarded handler signatures; the do() helper still
// stamps the context the guard's adapter would produce.
func testHandler(t *testing.T, svc *Service) *http.ServeMux {
	t.Helper()
	h := NewHandler(svc, nil)
	direct := func(fn func(http.ResponseWriter, *http.Request, guards.StaffCaller)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { fn(w, r, guards.StaffCaller{}) }
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/assist/commands", direct(h.commands))
	mux.HandleFunc("POST /api/v1/assist/command", direct(h.runCommand))
	mux.HandleFunc("POST /api/v1/assist/chat", direct(h.chat))
	mux.HandleFunc("GET /api/v1/assist/me", direct(h.me))
	mux.HandleFunc("GET /api/v1/assist/admin/spend", direct(h.adminSpend))
	mux.HandleFunc("GET /api/v1/assist/admin/limits", direct(h.adminLimits))
	mux.HandleFunc("PUT /api/v1/assist/admin/limits/role/{role}", direct(h.putRoleLimit))
	mux.HandleFunc("PUT /api/v1/assist/admin/limits/user/{user}", direct(h.putUserLimit))
	mux.HandleFunc("DELETE /api/v1/assist/admin/limits/user/{user}", direct(h.resetUserLimit))
	mux.HandleFunc("GET /api/v1/assist/admin/settings", direct(h.getSettings))
	mux.HandleFunc("PUT /api/v1/assist/admin/settings", direct(h.putSetting))
	return mux
}

func do(t *testing.T, mux *http.ServeMux, method, path string, body string, sc identity.StaffContext) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, asStaff(r, sc))
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

// -- the identity seam --------------------------------------------------------------------------

// TestHandler_CallerFromCarriesGrantsNotQueryParameters, an assistant that let you ask about
// another branch by editing a URL would be a friendlier version of the thing branch scoping exists
// to prevent.
func TestHandler_CallerFromCarriesGrantsNotQueryParameters(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/assist/commands?branch_id=b-somewhere-else", nil)
	sc := staffCtx("u-sm", "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, "b-kel", "b-ver")
	c := CallerFrom(asStaff(r, sc))

	if c.UserID != "u-sm" || c.RoleCode != "SALES_MANAGER" || c.Tier != int(identity.TierManager) {
		t.Fatalf("caller = %+v, want the middleware's own answer", c)
	}
	if c.Global {
		t.Fatal("a branch-scoped user was converted to a global caller")
	}
	// Sorted, so a downstream SQL plan and any assertion are stable.
	if len(c.BranchIDs) != 2 || c.BranchIDs[0] != "b-kel" || c.BranchIDs[1] != "b-ver" {
		t.Fatalf("BranchIDs = %v, want the sorted grants and nothing from the query string", c.BranchIDs)
	}
	if c.IsAdmin {
		t.Fatal("a tier-2 branch user must not be an admin")
	}
}

func TestHandler_GlobalLeadershipIsTheAdmin(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/assist/me", nil)
	c := CallerFrom(asStaff(r, staffCtx("u-gm", "GENERAL_MANAGER", identity.TierLeadership, identity.ScopeGlobal)))
	if !c.Global || !c.IsAdmin {
		t.Fatalf("caller = %+v, want a global admin", c)
	}
	if len(c.BranchIDs) != 0 {
		t.Fatalf("a global caller carries branch ids %v, they are meaningless and would only mislead a reader", c.BranchIDs)
	}
}

// -- list and run -------------------------------------------------------------------------------

func handlerFixture(t *testing.T) (*http.ServeMux, *Service, *fakeReaders, *fakeLedger, *fakeLimits, StaticSettings) {
	t.Helper()
	rd := seededReaders()
	led := &fakeLedger{}
	lim := newFakeLimits()
	set := StaticSettings{}
	svc := NewService(NewRegistry(rd.deps(), nil), NewStubProvider(), led, lim, set, nil).
		WithStaff(fakeStaff{
			{UserID: "u-gm", DisplayName: "Gail Marchetti", Email: "gail@dibbits.test", RoleCode: "GENERAL_MANAGER"},
			{UserID: "u-sm", DisplayName: "Sam Marsh", Email: "sam@dibbits.test", RoleCode: "SALES_MANAGER"},
			{UserID: "u-is", DisplayName: "Ida Stone", Email: "ida@dibbits.test", RoleCode: "INSIDE_SALES"},
			{UserID: "u-yh", DisplayName: "Yuri Hall", Email: "yuri@dibbits.test", RoleCode: "YARD_TEAM"},
		}).
		WithClock(func() time.Time { return time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC) })
	return testHandler(t, svc), svc, rd, led, lim, set
}

// TestHandler_CommandsIsAlwaysAnArrayNeverNull, `{"commands":null}` and `{"commands":[]}` are
// different claims to a client that does not write `?? []`, and the difference shows up as a blank
// panel with no explanation. (The DIB-443 lesson, one layer up.)
func TestHandler_CommandsIsAlwaysAnArrayNeverNull(t *testing.T) {
	mux, _, _, _, _, _ := handlerFixture(t)

	// An unassigned account may run nothing at all, the case that produces the nil slice.
	w, _ := do(t, mux, "GET", "/api/v1/assist/commands", "", staffCtx("u-new", "", 0, identity.ScopeBranch))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"commands":[]`) {
		t.Fatalf("body = %s, want an empty ARRAY", w.Body.String())
	}
}

func TestHandler_CommandListIsFilteredByTier(t *testing.T) {
	mux, _, _, _, _, _ := handlerFixture(t)

	_, gm := do(t, mux, "GET", "/api/v1/assist/commands", "", staffCtx("u-gm", "GENERAL_MANAGER", identity.TierLeadership, identity.ScopeGlobal))
	_, is := do(t, mux, "GET", "/api/v1/assist/commands", "", staffCtx("u-is", "INSIDE_SALES", identity.TierFrontline, identity.ScopeBranch, "b-kel"))

	gmCount := len(gm["commands"].([]any))
	isCount := len(is["commands"].([]any))
	if gmCount == 0 || isCount == 0 {
		t.Fatalf("leadership sees %d commands, frontline sees %d, both should see some", gmCount, isCount)
	}
	if isCount >= gmCount {
		t.Fatalf("frontline sees %d commands and leadership sees %d, the list must narrow with authority", isCount, gmCount)
	}
}

func TestHandler_RunCommandAsTheAskingUser(t *testing.T) {
	mux, _, rd, _, _, _ := handlerFixture(t)
	sc := staffCtx("u-sm", "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, "b-kel")

	w, body := do(t, mux, "POST", "/api/v1/assist/command", `{"command":"order.lookup","args":{"order_no":"SO-4"}}`, sc)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if body["command"] != "order.lookup" {
		t.Fatalf("body = %v", body)
	}
	if rd.gotCaller.UserID != "u-sm" {
		t.Fatalf("the read seam ran as %q, want u-sm", rd.gotCaller.UserID)
	}
}

// TestHandler_FourSituationsGetFourStatuses, the four remedies are different and DIB-443 is the
// ticket that proves a user reads the difference.
func TestHandler_FourSituationsGetFourStatuses(t *testing.T) {
	mux, _, _, _, _, _ := handlerFixture(t)
	frontlineSC := staffCtx("u-is", "INSIDE_SALES", identity.TierFrontline, identity.ScopeBranch, "b-kel")

	cases := []struct {
		name string
		body string
		sc   identity.StaffContext
		want int
	}{
		{"a command that does not exist", `{"command":"payment.delete","args":{}}`, frontlineSC, http.StatusNotFound},
		{"a command above the caller's tier", `{"command":"customer.balance","args":{"customer":"Northgate"}}`, frontlineSC, http.StatusForbidden},
		{"a missing required argument", `{"command":"order.lookup","args":{}}`, frontlineSC, http.StatusBadRequest},
		{"a body that is not JSON", `not json`, frontlineSC, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, body := do(t, mux, "POST", "/api/v1/assist/command", tc.body, tc.sc)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.want, w.Body.String())
			}
			msg, _ := body["error"].(string)
			if strings.TrimSpace(msg) == "" {
				t.Fatal("an error response with no words in it")
			}
			// Not a stack trace, not a Go error string with a package prefix.
			if strings.Contains(msg, "assist:") {
				t.Fatalf("the user is shown an internal error string: %q", msg)
			}
		})
	}
}

// TestHandler_UnknownAndRefusedDoNotConfirmEachOther: telling a user that a command exists but is
// switched off for them is a slightly more informative way of confirming it exists.
func TestHandler_UnknownAndRefusedDoNotConfirmEachOther(t *testing.T) {
	mux, _, _, _, _, _ := handlerFixture(t)
	sc := staffCtx("u-is", "INSIDE_SALES", identity.TierFrontline, identity.ScopeBranch, "b-kel")

	_, refused := do(t, mux, "POST", "/api/v1/assist/command", `{"command":"customer.balance","args":{"customer":"x"}}`, sc)
	msg, _ := refused["error"].(string)
	if strings.Contains(strings.ToLower(msg), "tier") || strings.Contains(strings.ToLower(msg), "permission") {
		t.Fatalf("the refusal explains the policy: %q", msg)
	}
}

// -- chat ---------------------------------------------------------------------------------------

// TestHandler_OverBudgetIsA200NotA4xx. The service returns it as an ordinary answer specifically so
// the handler cannot turn it into an error by accident; this asserts it did not.
func TestHandler_OverBudgetIsA200NotA4xx(t *testing.T) {
	mux, _, _, led, lim, _ := handlerFixture(t)
	lim.users["u-sm"] = 1000
	led.entries = append(led.entries, Entry{UserID: "u-sm", TokensIn: 1000, At: time.Date(2026, 8, 2, 9, 0, 0, 0, time.UTC)})

	sc := staffCtx("u-sm", "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, "b-kel")
	w, body := do(t, mux, "POST", "/api/v1/assist/chat", `{"question":"where is SO-4?"}`, sc)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, a reached limit is not a failure", w.Code)
	}
	if body["limit_reached"] != true {
		t.Fatalf("limit_reached = %v, want true", body["limit_reached"])
	}
	if body["text"] != LimitReachedMessage {
		t.Fatalf("text = %q, want the constant", body["text"])
	}
	// The array, not null, so the frontend's empty-state code does not have to guess.
	if _, ok := body["results"].([]any); !ok {
		t.Fatalf("results = %v, want []", body["results"])
	}
}

func TestHandler_AnEmptyQuestionIsA400WithWords(t *testing.T) {
	mux, _, _, _, _, _ := handlerFixture(t)
	sc := staffCtx("u-sm", "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, "b-kel")

	w, body := do(t, mux, "POST", "/api/v1/assist/chat", `{"question":"   "}`, sc)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if msg, _ := body["error"].(string); msg == "" {
		t.Fatal("no message on the 400")
	}
}

// TestHandler_UsageIsStrippedFromACallerWhoMayNotSeeSpend, governance 4, enforced at the handler so
// the toggle lives in one place instead of threading a flag through the service.
func TestHandler_UsageIsStrippedFromACallerWhoMayNotSeeSpend(t *testing.T) {
	mux, _, _, _, _, set := handlerFixture(t)
	sc := staffCtx("u-sm", "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, "b-kel")

	// Default: hidden.
	_, body := do(t, mux, "POST", "/api/v1/assist/chat", `{"question":"where is order SO-4?"}`, sc)
	if _, present := body["usage"]; present {
		t.Fatalf("usage was returned to a caller who may not see spend: %v", body["usage"])
	}

	// Toggled on: present.
	set[KeySpendVisibleToNonAdmin] = "true"
	_, body = do(t, mux, "POST", "/api/v1/assist/chat", `{"question":"where is order SO-4?"}`, sc)
	if _, present := body["usage"]; !present {
		t.Fatal("usage was withheld with the toggle on")
	}

	// An admin sees it regardless.
	set[KeySpendVisibleToNonAdmin] = "false"
	_, body = do(t, mux, "POST", "/api/v1/assist/chat", `{"question":"where is order SO-4?"}`,
		staffCtx("u-gm", "GENERAL_MANAGER", identity.TierLeadership, identity.ScopeGlobal))
	if _, present := body["usage"]; !present {
		t.Fatal("an admin was denied their own usage")
	}
}

// TestHandler_MeAlwaysShowsTheBudgetEvenWhenSpendIsHidden, a person refused for hitting a limit has
// to be able to see what the limit was; hiding it turns a policy into a mystery.
func TestHandler_MeAlwaysShowsTheBudgetEvenWhenSpendIsHidden(t *testing.T) {
	mux, _, _, _, _, set := handlerFixture(t)
	sc := staffCtx("u-sm", "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, "b-kel")

	w, body := do(t, mux, "GET", "/api/v1/assist/me", "", sc)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if body["budget"] == nil {
		t.Fatal("the budget was hidden along with the spend")
	}
	if _, present := body["spend"]; present {
		t.Fatal("spend was returned with the toggle off")
	}
	if body["spend_visible"] != false {
		t.Fatalf("spend_visible = %v, want false", body["spend_visible"])
	}

	set[KeySpendVisibleToNonAdmin] = "true"
	_, body = do(t, mux, "GET", "/api/v1/assist/me", "", sc)
	if _, present := body["spend"]; !present {
		t.Fatal("spend was withheld with the toggle on")
	}
}

// -- the admin surface --------------------------------------------------------------------------

func TestHandler_AdminSettingsNeverCarryACredential(t *testing.T) {
	mux, svc, _, _, _, _ := handlerFixture(t)
	svc.WithKeyPresent(true)

	w, body := do(t, mux, "GET", "/api/v1/assist/admin/settings", "",
		staffCtx("u-gm", "GENERAL_MANAGER", identity.TierLeadership, identity.ScopeGlobal))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if body["key_env_var"] != EnvAPIKey {
		t.Fatalf("key_env_var = %v, want the NAME %q", body["key_env_var"], EnvAPIKey)
	}
	if body["key_present"] != true {
		t.Fatalf("key_present = %v", body["key_present"])
	}
	// SOP-1: there is no field here that could carry a key, and the raw bytes prove it.
	raw := w.Body.String()
	for _, forbidden := range []string{"sk-", "api_key", "apiKey", "secret"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("the settings response contains %q: %s", forbidden, raw)
		}
	}
}

func TestHandler_ResetToRoleIsAnIdempotentDelete(t *testing.T) {
	mux, _, _, _, lim, _ := handlerFixture(t)
	admin := staffCtx("u-gm", "GENERAL_MANAGER", identity.TierLeadership, identity.ScopeGlobal)

	// Resetting a person who has NO override succeeds, a 404 would make an admin wonder whether it
	// worked, and the answer to "is there an override" is "no" either way.
	w, _ := do(t, mux, "DELETE", "/api/v1/assist/admin/limits/user/u-is", "", admin)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for an idempotent reset", w.Code)
	}

	lim.users["u-is"] = 5000
	w, body := do(t, mux, "DELETE", "/api/v1/assist/admin/limits/user/u-is", "", admin)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if body["source"] != SourceRole {
		t.Fatalf("source = %v, want %q", body["source"], SourceRole)
	}
	if _, still := lim.users["u-is"]; still {
		t.Fatal("the override survived reset-to-role")
	}
}

func TestHandler_NegativeBudgetIsA400(t *testing.T) {
	mux, _, _, _, _, _ := handlerFixture(t)
	admin := staffCtx("u-gm", "GENERAL_MANAGER", identity.TierLeadership, identity.ScopeGlobal)

	w, _ := do(t, mux, "PUT", "/api/v1/assist/admin/limits/user/u-is", `{"tokens":-5}`, admin)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	// Zero is legal, "switch the assistant off for this person".
	w, _ = do(t, mux, "PUT", "/api/v1/assist/admin/limits/user/u-is", `{"tokens":0}`, admin)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d for a zero budget, want 200", w.Code)
	}
}

func TestHandler_AdminSpendListsEveryoneWithAMonthLabel(t *testing.T) {
	mux, _, _, led, _, _ := handlerFixture(t)
	led.entries = append(led.entries,
		Entry{UserID: "u-sm", RoleCode: "SALES_MANAGER", TokensIn: 4200, TokensOut: 900, At: time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)},
		Entry{UserID: "u-is", RoleCode: "INSIDE_SALES", TokensIn: 800, TokensOut: 210, At: time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)},
	)
	w, body := do(t, mux, "GET", "/api/v1/assist/admin/spend", "",
		staffCtx("u-gm", "GENERAL_MANAGER", identity.TierLeadership, identity.ScopeGlobal))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if body["month"] != "2026-08" {
		t.Fatalf("month = %v, want 2026-08", body["month"])
	}
	users, _ := body["users"].([]any)
	if len(users) != 4 {
		t.Fatalf("%d rows, want all 4 active staff including the two with zeros", len(users))
	}
}

// -- the walkthrough fixtures -------------------------------------------------------------------

// TestFixtures_ForTheWalkthrough writes the exact response bytes the harness serves.
//
// Skipped unless ASSIST_FIXTURE_DIR is set, so an ordinary `go test ./...` writes nothing.
func TestFixtures_ForTheWalkthrough(t *testing.T) {
	dir := os.Getenv("ASSIST_FIXTURE_DIR")
	if dir == "" {
		t.Skip("ASSIST_FIXTURE_DIR not set, not regenerating walkthrough fixtures")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	write := func(name string, w *httptest.ResponseRecorder) {
		t.Helper()
		var pretty any
		if err := json.Unmarshal(w.Body.Bytes(), &pretty); err != nil {
			t.Fatalf("%s is not JSON: %v", name, err)
		}
		b, _ := json.MarshalIndent(pretty, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, name), append(b, '\n'), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	manager := staffCtx("u-sm", "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, "b-kel")
	admin := staffCtx("u-gm", "GENERAL_MANAGER", identity.TierLeadership, identity.ScopeGlobal)

	// -- the ordinary panel: commands, budget, and a payment lookup ------------------------------
	{
		mux, _, _, led, lim, set := handlerFixture(t)
		set[KeySpendVisibleToNonAdmin] = "true"
		lim.users["u-sm"] = 300000
		led.entries = append(led.entries,
			Entry{UserID: "u-sm", RoleCode: "SALES_MANAGER", Provider: "anthropic", Model: "claude-3-5-haiku",
				TokensIn: 41200, TokensOut: 6100, Kind: KindChat, At: time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)},
		)
		w, _ := do(t, mux, "GET", "/api/v1/assist/commands", "", manager)
		write("commands.json", w)
		w, _ = do(t, mux, "GET", "/api/v1/assist/me", "", manager)
		write("me.json", w)
		w, _ = do(t, mux, "POST", "/api/v1/assist/command",
			`{"command":"payment.lookup","args":{"reference":"gw_88x"}}`, manager)
		if w.Code != http.StatusOK {
			t.Fatalf("payment lookup fixture failed: %s", w.Body.String())
		}
		write("command-answer.json", w)

		// A chat answer, through the REAL stub provider, intent parse, command, template sentence.
		w, _ = do(t, mux, "POST", "/api/v1/assist/chat", `{"question":"where is order SO-4?"}`, manager)
		if w.Code != http.StatusOK {
			t.Fatalf("chat fixture failed: %s", w.Body.String())
		}
		write("chat-answer.json", w)
	}

	// -- the limit-reached state -----------------------------------------------------------------
	{
		mux, _, _, led, lim, set := handlerFixture(t)
		set[KeySpendVisibleToNonAdmin] = "true"
		lim.users["u-sm"] = 150000
		led.entries = append(led.entries,
			Entry{UserID: "u-sm", RoleCode: "SALES_MANAGER", Provider: "anthropic", Model: "claude-3-5-haiku",
				TokensIn: 132400, TokensOut: 17600, Kind: KindChat, At: time.Date(2026, 8, 7, 9, 0, 0, 0, time.UTC)},
		)
		w, _ := do(t, mux, "GET", "/api/v1/assist/me", "", manager)
		write("me-over-budget.json", w)
		w, body := do(t, mux, "POST", "/api/v1/assist/chat", `{"question":"what does Northgate owe?"}`, manager)
		if w.Code != http.StatusOK || body["limit_reached"] != true {
			t.Fatalf("the over-budget fixture is not over budget: %d %s", w.Code, w.Body.String())
		}
		write("chat-limit-reached.json", w)
	}

	// -- the admin surface -----------------------------------------------------------------------
	{
		mux, svc, _, led, lim, _ := handlerFixture(t)
		svc.WithKeyPresent(false)
		lim.roles["GENERAL_MANAGER"] = 600000
		lim.roles["SALES_MANAGER"] = 300000
		lim.roles["INSIDE_SALES"] = 150000
		lim.roles["YARD_TEAM"] = 60000
		lim.users["u-is"] = 250000 // an override, so the surface shows "their own"
		led.entries = append(led.entries,
			Entry{UserID: "u-sm", RoleCode: "SALES_MANAGER", Provider: "anthropic", Model: "claude-3-5-haiku", TokensIn: 41200, TokensOut: 6100, At: time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)},
			Entry{UserID: "u-sm", RoleCode: "SALES_MANAGER", Provider: "anthropic", Model: "claude-3-5-haiku", TokensIn: 9800, TokensOut: 1400, At: time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)},
			Entry{UserID: "u-is", RoleCode: "INSIDE_SALES", Provider: "anthropic", Model: "claude-3-5-haiku", TokensIn: 214000, TokensOut: 38000, At: time.Date(2026, 8, 7, 9, 0, 0, 0, time.UTC)},
			Entry{UserID: "u-gm", RoleCode: "GENERAL_MANAGER", Provider: "stub", Model: "hos-stub-1", TokensIn: 0, TokensOut: 0, At: time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)},
			Entry{UserID: "u-gm", RoleCode: "GENERAL_MANAGER", Provider: "stub", Model: "hos-stub-1", TokensIn: 0, TokensOut: 0, At: time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)},
		)
		w, _ := do(t, mux, "GET", "/api/v1/assist/admin/spend", "", admin)
		write("admin-spend.json", w)
		w, _ = do(t, mux, "GET", "/api/v1/assist/admin/limits", "", admin)
		write("admin-limits.json", w)
		w, _ = do(t, mux, "GET", "/api/v1/assist/admin/settings", "", admin)
		write("admin-settings.json", w)
	}

	t.Logf("walkthrough fixtures written to %s", dir)
}

// ensure the fixture helper compiles even when unused by the skipped path.
var _ = context.Background
