package quoteassist

// handler_test.go, the HTTP surface through the REAL routes with a REAL identity context, and the
// generator for the walkthrough's fixtures.
//
// WHAT IS COVERED HERE AND NOT IN THE SERVICE TESTS: the seam itself. The one conversion from
// identity.StaffContext to assist.Caller, the STATUS each refusal is reported with, the arrays that
// must never be null on the wire, and the two places the handler decides something on its own, the
// absent `enabled` field on a PATCH, and the actor stamped onto a rule change.
//
// THE FIXTURES ARE REAL RESPONSES. Set QUOTEASSIST_FIXTURE_DIR and this file writes the exact bytes
// these handlers produced into walkthrough-6667/fixtures/, which the Chromium harness then serves for
// the two routes main.go has not been wired for yet (PARK 1). That is what makes every screenshot in
// walkthrough-6667/ a picture of what the SERVER produces rather than of hand-written JSON that
// agrees with the frontend because the same person wrote both.
//
//	QUOTEASSIST_FIXTURE_DIR=../../../walkthrough-6667/fixtures go test ./internal/quoteassist/ -run Fixture

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// asStaff injects a StaffContext the way the real middleware does, so assist.CallerFrom is exercised
// rather than bypassed.
func asStaff(r *http.Request, sc identity.StaffContext) *http.Request {
	return r.WithContext(identity.ContextWithStaff(r.Context(), sc))
}

func repCtx() identity.StaffContext {
	return identity.StaffContext{
		UserID: "user-rep", RoleCode: "SALES_REP", Tier: identity.TierFrontline,
		Scope: identity.ScopeBranch, Status: "ACTIVE", Branches: map[string]bool{"branch-trenton": true},
	}
}

func adminCtx() identity.StaffContext {
	return identity.StaffContext{
		UserID: "user-admin", RoleCode: "SUPER_ADMIN", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: "ACTIVE",
	}
}

// testMux wires the handler with the guards as pass-throughs. The guards are the identity package's
// own tested code; stubbing them keeps these tests about this surface.
// Guards-swap carry note (HH-3.2 fold): the pass-through mux became a
// direct mux over the guarded handler signatures; asStaff still stamps the
// context the guard's adapter would produce, so these tests stay about
// this surface.
func testMux(t *testing.T, svc *Service) *http.ServeMux {
	t.Helper()
	h := NewHandler(svc)
	direct := func(fn func(http.ResponseWriter, *http.Request, guards.StaffCaller)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { fn(w, r, guards.StaffCaller{}) }
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/quote-assist/extract", direct(h.extract))
	mux.HandleFunc("GET /api/v1/quote-assist/scope-gaps", direct(h.scopeGaps))
	mux.HandleFunc("GET /api/v1/quote-assist/scope-rules", direct(h.listRules))
	mux.HandleFunc("PATCH /api/v1/quote-assist/scope-rules/{code}", direct(h.patchRule))
	return mux
}

func postJSON(t *testing.T, mux *http.ServeMux, path string, body string, sc identity.StaffContext) *httptest.ResponseRecorder {
	t.Helper()
	req := asStaff(httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)), sc)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// -- extract ------------------------------------------------------------------------------------

// TestHandler_ExtractIsAReadThatReturnsProposals.
func TestHandler_ExtractIsAReadThatReturnsProposals(t *testing.T) {
	mux := testMux(t, NewService(seededCatalogue(), nil, nil, nil))
	rec := postJSON(t, mux, "/api/v1/quote-assist/extract",
		`{"text":"480 sq ft of Techo-Bloc Blu 60 for the patio, 6 bags of polymeric sand","branch_id":"branch-trenton"}`, repCtx())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got Extraction
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v, %s", err, rec.Body.String())
	}
	if len(got.Proposals) != 2 {
		t.Fatalf("proposals = %d, want 2: %s", len(got.Proposals), rec.Body.String())
	}
	if got.Source != SourceDeterministic {
		t.Errorf("source = %q", got.Source)
	}
}

// TestHandler_ExtractNeverAnswersNullArrays, a client that renders proposals.map(...) must not have
// to write `?? []` to survive an empty answer (DIB-471, the shape-at-the-edge ruling).
func TestHandler_ExtractNeverAnswersNullArrays(t *testing.T) {
	mux := testMux(t, NewService(seededCatalogue(), nil, nil, nil))
	rec := postJSON(t, mux, "/api/v1/quote-assist/extract", `{"text":""}`, repCtx())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, an empty description is an answer, not a fault", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, `"proposals":null`) || strings.Contains(body, `"unmatched":null`) {
		t.Fatalf("null array on the wire: %s", body)
	}
	if !strings.Contains(body, `"proposals":[]`) {
		t.Errorf("expected an empty array: %s", body)
	}
}

// TestHandler_ExtractRefusesAMalformedBodyAsA400, a body that did not parse is a 400, never a
// silent empty extraction. "text": missing arriving as a successful no-op would be the worst failure
// mode here (the quote module's decodeBody argument).
func TestHandler_ExtractRefusesAMalformedBodyAsA400(t *testing.T) {
	mux := testMux(t, NewService(seededCatalogue(), nil, nil, nil))
	rec := postJSON(t, mux, "/api/v1/quote-assist/extract", `{"text":`, repCtx())
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestHandler_ExtractOnAnUnwiredCatalogueIs503, nothing failed; a seam is not connected, and the
// honest answer names it rather than reporting a 500.
func TestHandler_ExtractOnAnUnwiredCatalogueIs503(t *testing.T) {
	mux := testMux(t, NewService(nil, nil, nil, nil))
	rec := postJSON(t, mux, "/api/v1/quote-assist/extract", `{"text":"480 sq ft Blu 60"}`, repCtx())
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
}

// TestHandler_ExtractCallerIsTheGuardStampedOne, the request body cannot name a caller, and the
// caller that reaches the AI rails is the one identity stamped. A caller a request could name would
// be a budget a request could pick.
func TestHandler_ExtractCallerIsTheGuardStampedOne(t *testing.T) {
	ai := &recordingAI{}
	mux := testMux(t, NewService(seededCatalogue(), nil, nil, nil).WithAI(ai))
	rec := postJSON(t, mux, "/api/v1/quote-assist/extract",
		`{"text":"480 sq ft Blu 60","use_ai":true,"user_id":"user-somebody-else","role_code":"SUPER_ADMIN"}`, repCtx())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if ai.gotCaller.UserID != "user-rep" {
		t.Fatalf("the caller reaching the rails was %q, a request body renamed the spender", ai.gotCaller.UserID)
	}
	if ai.gotCaller.RoleCode != "SALES_REP" {
		t.Errorf("role reaching the rails was %q, budgets are per role", ai.gotCaller.RoleCode)
	}
	if ai.gotCaller.Global {
		t.Error("the caller reached the rails as global, the branch scope was rewritten by a body field")
	}
}

// -- scope gaps ---------------------------------------------------------------------------------

func TestHandler_ScopeGapsNeedsAQuoteID(t *testing.T) {
	mux := testMux(t, NewService(nil, &fakeLines{found: true}, &fakeRules{rules: seededRules()}, nil))
	req := asStaff(httptest.NewRequest(http.MethodGet, "/api/v1/quote-assist/scope-gaps", nil), repCtx())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandler_ScopeGapsAnswersFindingsForAPaverQuote(t *testing.T) {
	mux := testMux(t, NewService(nil, &fakeLines{lines: paverOnlyQuote(), found: true}, &fakeRules{rules: seededRules()}, nil))
	req := asStaff(httptest.NewRequest(http.MethodGet, "/api/v1/quote-assist/scope-gaps?quote_id=q-1", nil), repCtx())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var got ScopeGaps
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Findings) != 4 {
		t.Fatalf("findings = %v, want 4", findingCodes(got.Findings))
	}
}

// TestHandler_ScopeGapsOnAnUnreachableQuoteIs404WithNoDetail, "no such quote" and "not yours" are
// one answer, because a distinguishable refusal is an existence oracle for another branch.
func TestHandler_ScopeGapsOnAnUnreachableQuoteIs404WithNoDetail(t *testing.T) {
	mux := testMux(t, NewService(nil, &fakeLines{found: false}, &fakeRules{rules: seededRules()}, nil))
	req := asStaff(httptest.NewRequest(http.MethodGet, "/api/v1/quote-assist/scope-gaps?quote_id=q-elsewhere", nil), repCtx())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if strings.Contains(strings.ToLower(rec.Body.String()), "branch") {
		t.Errorf("the refusal leaks why: %s", rec.Body.String())
	}
}

// TestHandler_ScopeGapsEmptyQuoteIsAnEmptyAnswer.
func TestHandler_ScopeGapsEmptyQuoteIsAnEmptyAnswer(t *testing.T) {
	mux := testMux(t, NewService(nil, &fakeLines{lines: []ScopeLine{}, found: true}, &fakeRules{rules: seededRules()}, nil))
	req := asStaff(httptest.NewRequest(http.MethodGet, "/api/v1/quote-assist/scope-gaps?quote_id=q-1", nil), repCtx())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), `"findings":null`) {
		t.Fatalf("null findings on the wire: %s", rec.Body.String())
	}
}

// -- the admin rule surface ---------------------------------------------------------------------

func TestHandler_RuleListingShowsDisabledRulesToo(t *testing.T) {
	rules := seededRules()
	rules[2].Enabled = false
	mux := testMux(t, NewService(nil, nil, &fakeRules{rules: rules}, nil))
	req := asStaff(httptest.NewRequest(http.MethodGet, "/api/v1/quote-assist/scope-rules", nil), adminCtx())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct{ Rules []ScopeRule }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Rules) != 7 {
		t.Fatalf("rules = %d, want all 7", len(body.Rules))
	}
}

// TestHandler_PatchRuleRefusesAnAbsentEnabledField.
//
// A POINTER on the decode target, so `{}` is refused rather than read as "switch this rule off". A
// silent, destructive default on a config surface is indistinguishable from a client bug and would
// turn every rep's suggestion panel off with nobody having decided that.
func TestHandler_PatchRuleRefusesAnAbsentEnabledField(t *testing.T) {
	rules := &fakeRules{rules: seededRules()}
	mux := testMux(t, NewService(nil, nil, rules, nil))
	req := asStaff(httptest.NewRequest(http.MethodPatch, "/api/v1/quote-assist/scope-rules/SURFACE_NEEDS_JOINTING",
		strings.NewReader(`{}`)), adminCtx())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if rules.setCalls != 0 {
		t.Fatal("an absent enabled field reached the store")
	}
}

// TestHandler_PatchRuleStampsTheActorFromTheContext, a config change is somebody's decision, and the
// somebody comes from the guard, not from the body.
func TestHandler_PatchRuleStampsTheActorFromTheContext(t *testing.T) {
	rules := &fakeRules{rules: seededRules()}
	mux := testMux(t, NewService(nil, nil, rules, nil))
	req := asStaff(httptest.NewRequest(http.MethodPatch, "/api/v1/quote-assist/scope-rules/SURFACE_NEEDS_JOINTING",
		strings.NewReader(`{"enabled":false,"updated_by":"somebody-else"}`)), adminCtx())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if rules.setActor != "user-admin" {
		t.Fatalf("actor = %q, want the guard-stamped user-admin", rules.setActor)
	}
	if rules.setEnabled {
		t.Error("the rule was not switched off")
	}
}

func TestHandler_PatchUnknownRuleIs404(t *testing.T) {
	mux := testMux(t, NewService(nil, nil, &fakeRules{rules: seededRules()}, nil))
	req := asStaff(httptest.NewRequest(http.MethodPatch, "/api/v1/quote-assist/scope-rules/NOT_A_RULE",
		strings.NewReader(`{"enabled":true}`)), adminCtx())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// -- the walkthrough fixtures -------------------------------------------------------------------

// TestHandler_AllThreeSurfacesAnswer is the shape check the walkthrough's fixtures depend on.
//
// IT DOES NOT WRITE THE FIXTURES. The bytes the Chromium harness replays come from
// TestFixture_WriteRealCatalogueBodies in integration_test.go, against the REAL seeded catalogue -
// because a screenshot of a fake catalogue's answers would show a rep six products that agree with
// the frontend only because the same person wrote both. What this test does is prove that the three
// HTTP surfaces still answer in the shape those fixtures are in, with no database.
func TestHandler_AllThreeSurfacesAnswer(t *testing.T) {
	extractSvc := NewService(seededCatalogue(), nil, nil, nil)
	gapsSvc := NewService(nil, &fakeLines{lines: paverOnlyQuote(), found: true}, &fakeRules{rules: seededRules()}, nil)
	rulesSvc := NewService(nil, nil, &fakeRules{rules: seededRules()}, nil)

	extract := postJSON(t, testMux(t, extractSvc), "/api/v1/quote-assist/extract",
		`{"text":`+jsonString(realJobDescription)+`,"branch_id":"branch-trenton"}`, repCtx())
	if extract.Code != http.StatusOK {
		t.Fatalf("extract fixture: status %d, %s", extract.Code, extract.Body.String())
	}

	gapsMux := testMux(t, gapsSvc)
	gapsReq := asStaff(httptest.NewRequest(http.MethodGet, "/api/v1/quote-assist/scope-gaps?quote_id=FIXTURE", nil), repCtx())
	gaps := httptest.NewRecorder()
	gapsMux.ServeHTTP(gaps, gapsReq)
	if gaps.Code != http.StatusOK {
		t.Fatalf("gaps fixture: status %d", gaps.Code)
	}

	rulesMux := testMux(t, rulesSvc)
	rulesReq := asStaff(httptest.NewRequest(http.MethodGet, "/api/v1/quote-assist/scope-rules", nil), adminCtx())
	rules := httptest.NewRecorder()
	rulesMux.ServeHTTP(rules, rulesReq)
	if rules.Code != http.StatusOK {
		t.Fatalf("rules fixture: status %d", rules.Code)
	}

	// Every one of the three carries the arrays a client renders straight into a .map().
	for name, body := range map[string]string{
		"extract":     extract.Body.String(),
		"scope-gaps":  gaps.Body.String(),
		"scope-rules": rules.Body.String(),
	} {
		if strings.Contains(body, ":null") {
			t.Errorf("%s answers a null where a client expects an array: %s", name, body)
		}
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// recordingAI records the Caller the rails were handed, the only way to assert, from outside, that
// the guard-stamped caller is the one that would have been billed.
type recordingAI struct {
	gotCaller callerRecord
	calls     int
}

// callerRecord is the two fields the assertion needs, copied so the test does not depend on the
// assist package's struct layout.
type callerRecord struct {
	UserID   string
	RoleCode string
	Global   bool
}

func (r *recordingAI) SegmentText(_ context.Context, c assist.Caller, _ string) (Segmentation, error) {
	r.calls++
	r.gotCaller = callerRecord{UserID: c.UserID, RoleCode: c.RoleCode, Global: c.Global}
	// Returns nothing usable on purpose: this test is about WHO was handed to the rails, and the
	// extraction must still answer from the deterministic path.
	return Segmentation{}, nil
}
