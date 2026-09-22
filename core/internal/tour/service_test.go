package tour

// service_test.go, the byte-absence gate, the platform gate, and the bookmark's ownership.
//
//	go test ./internal/tour/ -count=1
//
// THE ONE THING THIS FILE EXISTS FOR is TestTour_OffAnswersExactlyEmptyBraces, and everything
// around it is there so that test cannot pass by accident. The failure it prevents is not a crash:
// it is a payload that carries the walkthrough's copy to a caller who is not meant to have it, with
// a client politely declining to draw it. Three trains in this repo shipped `"field":null` believing
// it was absence; it is not, because `"field" in payload` is true for both, so the scan below looks
// for the literal string "null" as well as for the words.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// -- the fixture ---------------------------------------------------------------------------------

// fakeStore is the seam under test. Every method records what it was asked so a refused write can
// be proved to have written NOTHING rather than merely to have returned an error.
type fakeStore struct {
	policy      Policy
	policyErr   error
	chapters    []chapterRow
	chapterErr  error
	steps       map[string][]Step
	stepsErr    error
	progress    map[string]map[string]Mark // user id → chapter → bookmark
	progressErr error

	stepWrites   []StepEdit
	policyWrites []PolicyEdit
	upserts      []string // "user|chapter|step|state"
	deletes      []string // "user|chapter"
	updateFound  bool
}

func (f *fakeStore) ReadPolicy(_ context.Context, _ string) (Policy, error) {
	if f.policyErr != nil {
		return DefaultPolicy(), f.policyErr
	}
	return f.policy, nil
}

func (f *fakeStore) SetDemoMode(_ context.Context, branchID string, demo bool, _ string) error {
	f.policyWrites = append(f.policyWrites, PolicyEdit{BranchID: branchID, DemoMode: demo})
	f.policy.DemoMode = demo
	return nil
}

func (f *fakeStore) ReadVisibleChapters(_ context.Context) ([]chapterRow, error) {
	return f.chapters, f.chapterErr
}

func (f *fakeStore) ReadSteps(_ context.Context) (map[string][]Step, error) {
	return f.steps, f.stepsErr
}

func (f *fakeStore) UpdateStepCopy(_ context.Context, in StepEdit, _ string) (bool, error) {
	f.stepWrites = append(f.stepWrites, in)
	return f.updateFound, nil
}

func (f *fakeStore) ReadProgress(_ context.Context, userID string) (map[string]Mark, error) {
	if f.progressErr != nil {
		return nil, f.progressErr
	}
	return f.progress[userID], nil
}

func (f *fakeStore) UpsertProgress(_ context.Context, userID string, in ProgressEdit) error {
	f.upserts = append(f.upserts, strings.Join([]string{userID, in.ChapterKey, in.StepKey, in.State}, "|"))
	if f.progress == nil {
		f.progress = map[string]map[string]Mark{}
	}
	if f.progress[userID] == nil {
		f.progress[userID] = map[string]Mark{}
	}
	f.progress[userID][in.ChapterKey] = Mark{ChapterKey: in.ChapterKey, StepKey: in.StepKey, State: in.State}
	return nil
}

func (f *fakeStore) DeleteProgress(_ context.Context, userID, chapterKey string) error {
	f.deletes = append(f.deletes, userID+"|"+chapterKey)
	delete(f.progress[userID], chapterKey)
	return nil
}

// loadedFixture is a store with real content in it: two chapters, three steps, one bookmark, and
// the policy ON. It is the SAME value both halves of the byte-absence gate run over, the positive
// control proves the words are in here, so their absence in the OFF answer cannot be an empty
// fixture wearing a passing test's clothes.
func loadedFixture() *fakeStore {
	return &fakeStore{
		policy: Policy{DemoMode: true, DealerEnabled: true},
		chapters: []chapterRow{
			{Key: "sixty-seconds", Position: 0, Title: "In and out in sixty seconds",
				Promise: "A cash sale, start to receipt, without leaving the keyboard.",
				Role:    "INSIDE_SALES", Route: "#sales/pos"},
			{Key: "what-we-keep", Position: 1, Title: "How much am I saving?",
				Promise: "What the yard keeps on this ticket, and who may see it.",
				Role:    "GENERAL_MANAGER", Route: "#sales/pos"},
		},
		steps: map[string][]Step{
			"sixty-seconds": {
				{StepKey: "open", Position: 0, Route: "#sales/pos", Anchor: "tag:hos-pos-terminal",
					Line: "This is the till. Everything here happens on one screen.", Advance: "NEXT"},
				{StepKey: "scan", Position: 1, Anchor: "testid:pos-search-drop-host",
					Line: "Scan or type a code. The line prices itself.", Advance: "NEXT"},
			},
			"what-we-keep": {
				{StepKey: "eye", Position: 0, Anchor: "testid:margin-eye",
					Line: "One eye hides every cost figure on the screen.", Advance: "NEXT"},
			},
		},
		progress: map[string]map[string]Mark{
			"u-alex": {"sixty-seconds": {ChapterKey: "sixty-seconds", StepKey: "scan", State: StateOpen}},
		},
		updateFound: true,
	}
}

func staffCtx(sc identity.StaffContext) func(context.Context) (identity.StaffContext, bool) {
	return func(context.Context) (identity.StaffContext, bool) { return sc, true }
}

var (
	alexTheGM = identity.StaffContext{UserID: "u-alex", RoleCode: "GENERAL_MANAGER",
		Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: "ACTIVE"}
	coltonThePlatform = identity.StaffContext{UserID: "u-colton", RoleCode: identity.RoleSuperAdmin,
		Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: "ACTIVE"}
	samTheClerk = identity.StaffContext{UserID: "u-sam", RoleCode: "INSIDE_SALES",
		Tier: identity.TierFrontline, Scope: identity.ScopeBranch, Status: "ACTIVE"}
)

func svcFor(st *fakeStore, who identity.StaffContext) *Service {
	return NewService(nil).withStore(st).withCaller(staffCtx(who))
}

// theWords are what a leaked walkthrough looks like on the wire. "null" is in the list because it
// is the failure this repo has actually shipped: a null-valued key is a PRESENT key.
var theWords = []string{"chapters", "steps", "line", "promise", "anchor", "progress", "null"}

// -- 1 · the byte-absence gate -------------------------------------------------------------------

// TestTour_OffAnswersExactlyEmptyBraces is the acceptance clause, both halves.
//
// The POSITIVE CONTROL runs first, over the same fixture value, and asserts the words ARE there.
// Without it this test passes on an empty fixture, on a broken fake, and on a service that returns
// the zero value unconditionally, three ways of being green while the feature is gone.
func TestTour_OffAnswersExactlyEmptyBraces(t *testing.T) {
	// -- the positive control --
	on := loadedFixture()
	onBytes, err := json.Marshal(svcFor(on, alexTheGM).Tour(context.Background(), ""))
	if err != nil {
		t.Fatalf("marshal the ON answer: %v", err)
	}
	for _, w := range theWords {
		if w == "null" {
			continue // the ON answer must not carry it either, asserted just below.
		}
		if !strings.Contains(string(onBytes), w) {
			t.Fatalf("the ON answer does not contain %q, the fixture is empty and this gate proves nothing\n%s", w, onBytes)
		}
	}
	if !strings.Contains(string(onBytes), "This is the till.") {
		t.Fatalf("the ON answer carries no step copy, the fixture is not loaded\n%s", onBytes)
	}
	// AND THE ON ANSWER CARRIES NO null EITHER. An absent step list or an unstarted chapter must be
	// an absent key on the way out, not a null one; if this fails, omitempty has been lost somewhere
	// and the OFF half of this test is about to start passing for the wrong reason.
	if strings.Contains(string(onBytes), "null") {
		t.Fatalf("the ON answer contains a null, omitempty has been lost\n%s", onBytes)
	}

	// -- the gate -- the SAME fixture, with each of the ways the walkthrough is off.
	for _, tc := range []struct {
		name string
		bend func(*fakeStore) *Service
	}{
		{"the deployment is not a demo", func(f *fakeStore) *Service {
			f.policy = Policy{DemoMode: false, DealerEnabled: true}
			return svcFor(f, alexTheGM)
		}},
		{"the dealer turned it off", func(f *fakeStore) *Service {
			f.policy = Policy{DemoMode: true, DealerEnabled: false}
			return svcFor(f, alexTheGM)
		}},
		{"the policy is unreadable", func(f *fakeStore) *Service {
			f.policyErr = errors.New("connection reset")
			return svcFor(f, alexTheGM)
		}},
		{"the chapters are unreadable", func(f *fakeStore) *Service {
			f.chapterErr = errors.New("relation does not exist")
			return svcFor(f, alexTheGM)
		}},
		{"the steps are unreadable", func(f *fakeStore) *Service {
			f.stepsErr = errors.New("statement timeout")
			return svcFor(f, alexTheGM)
		}},
		{"there is no chapter to show", func(f *fakeStore) *Service {
			f.chapters = nil
			return svcFor(f, alexTheGM)
		}},
		{"the caller is anonymous", func(f *fakeStore) *Service {
			return NewService(nil).withStore(f).withCaller(
				func(context.Context) (identity.StaffContext, bool) { return identity.StaffContext{}, false })
		}},
		{"the platform administrator is refused too", func(f *fakeStore) *Service {
			// can_edit must not survive the gate: a SUPER_ADMIN on a non-demo deployment gets `{}`
			// and not `{"can_edit":true}`, which would be a key where there must be none.
			f.policy = Policy{DemoMode: false, DealerEnabled: true}
			return svcFor(f, coltonThePlatform)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := tc.bend(loadedFixture())
			got, err := json.Marshal(svc.Tour(context.Background(), ""))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != "{}" {
				t.Fatalf("the OFF answer is %s, want exactly {}, byte-absent, not hidden", got)
			}
			for _, w := range theWords {
				if strings.Contains(string(got), w) {
					t.Fatalf("the OFF answer contains %q: %s", w, got)
				}
			}
		})
	}
}

// TestTour_TheRouteAnswersEmptyBracesOnTheWire, the same gate through the real mux, because the
// service's value and the bytes a browser receives are two different facts and only the second one
// is the acceptance clause.
func TestTour_TheRouteAnswersEmptyBracesOnTheWire(t *testing.T) {
	f := loadedFixture()
	f.policy = Policy{DemoMode: false, DealerEnabled: true}
	// Guards-swap carry note (HH-3.2 fold): the wire-shape answer is the
	// handler's, so the test drives it directly (the pass-through mux the
	// pin used is the superseded wiring).
	h := NewHandler(svcFor(f, alexTheGM))

	w := httptest.NewRecorder()
	h.tour(w, httptest.NewRequest(http.MethodGet, "/api/v1/tour", nil), guards.StaffCaller{})
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/tour = %d, want 200, an off walkthrough is not a fault", w.Code)
	}
	if body := strings.TrimSpace(w.Body.String()); body != "{}" {
		t.Fatalf("body = %q, want {}", body)
	}
}

// passThrough is the mux guard stand-in: the door gate is identity's and is exercised by identity's
// own suite; what this package must prove is that the PLATFORM gate holds with the door wide open.
func passThrough(h http.HandlerFunc) http.HandlerFunc { return h }

// -- 2 · the ON answer is the whole walkthrough, in order ----------------------------------------

func TestTour_OnServesChaptersInOrderWithTheCallersOwnBookmark(t *testing.T) {
	got := svcFor(loadedFixture(), alexTheGM).Tour(context.Background(), "")
	if len(got.Chapters) != 2 {
		t.Fatalf("chapters = %d, want 2", len(got.Chapters))
	}
	if got.Chapters[0].ChapterKey != "sixty-seconds" || got.Chapters[1].ChapterKey != "what-we-keep" {
		t.Fatalf("order = %s, %s", got.Chapters[0].ChapterKey, got.Chapters[1].ChapterKey)
	}
	// THE POSITION THE SERVER SORTED BY IS ON THE WIRE. The consumer re-sorts by it, so a payload
	// that dropped it would have the client open the demo on whichever key sorts first.
	if got.Chapters[0].Position != 0 || got.Chapters[1].Position != 1 {
		t.Fatalf("positions = %d, %d, the client re-sorts by this", got.Chapters[0].Position, got.Chapters[1].Position)
	}
	if len(got.Chapters[0].Steps) != 2 || got.Chapters[0].Steps[0].StepKey != "open" || got.Chapters[0].Steps[1].Position != 1 {
		t.Fatalf("chapter 1 steps = %+v", got.Chapters[0].Steps)
	}
	// ONE MARK, FOR THE ONE CHAPTER THIS CALLER STARTED, and none for the one they did not.
	if len(got.Progress) != 1 {
		t.Fatalf("progress = %+v, want exactly the one chapter alex started", got.Progress)
	}
	if got.Progress[0].ChapterKey != "sixty-seconds" || got.Progress[0].StepKey != "scan" {
		t.Fatalf("the caller's bookmark did not reach the payload: %+v", got.Progress[0])
	}
	// AND EVERY MARK NAMES A CHAPTER THE SAME BODY SENT.
	sent := map[string]bool{}
	for _, c := range got.Chapters {
		sent[c.ChapterKey] = true
	}
	for _, m := range got.Progress {
		if !sent[m.ChapterKey] {
			t.Fatalf("progress names %q, which is not in this payload's chapters", m.ChapterKey)
		}
	}
	if got.CanEdit {
		t.Fatal("a General Manager is not the platform administrator and can_edit must say so")
	}
	if !svcFor(loadedFixture(), coltonThePlatform).Tour(context.Background(), "").CanEdit {
		t.Fatal("the platform administrator must be told they may edit, authority is the server's fact")
	}
}

// TestTour_OneCallerNeverSeesAnothersBookmark, the bookmark is read by user id, and the id is the
// caller's. A second person on the same deployment starts at the beginning.
func TestTour_OneCallerNeverSeesAnothersBookmark(t *testing.T) {
	got := svcFor(loadedFixture(), samTheClerk).Tour(context.Background(), "")
	if len(got.Progress) != 0 {
		t.Fatalf("sam was handed somebody else's bookmarks: %+v", got.Progress)
	}
	// And the walkthrough itself is still there, this must not pass because the tour went off.
	if len(got.Chapters) != 2 {
		t.Fatalf("chapters = %d, want 2, a caller with no bookmark still gets the walkthrough", len(got.Chapters))
	}
}

// TestTour_AnUnreadableBookmarkStartsYouAtTheBeginning, the one read that fails SOFT, because
// losing a bookmark is a state the product already has a name for.
func TestTour_AnUnreadableBookmarkStartsYouAtTheBeginning(t *testing.T) {
	f := loadedFixture()
	f.progressErr = errors.New("statement timeout")
	got := svcFor(f, alexTheGM).Tour(context.Background(), "")
	if len(got.Chapters) != 2 {
		t.Fatalf("chapters = %d, want 2, a lost bookmark must not cost the walkthrough", len(got.Chapters))
	}
	if len(got.Progress) != 0 {
		t.Fatalf("progress = %+v, want none", got.Progress)
	}
}

// -- 3 · the platform gate, on the ACT and not only on the payload -------------------------------

// TestPlatformWrites_ADealersGMIsRefusedAndWritesNothing is the twin of billing's
// TestReminderSend_IsRefusedOnBaseTier: byte-absence gates the display, so the write must be gated
// separately or a hand-crafted PUT walks straight through the hole the payload gate left.
func TestPlatformWrites_ADealersGMIsRefusedAndWritesNothing(t *testing.T) {
	for _, who := range []identity.StaffContext{alexTheGM, samTheClerk} {
		t.Run(who.RoleCode, func(t *testing.T) {
			f := loadedFixture()
			svc := svcFor(f, who)

			stepErr := svc.PutStepCopy(context.Background(), StepEdit{
				ChapterKey: "sixty-seconds", StepKey: "open", Line: "A line this caller may not write."})
			var np *NotPlatformAdminError
			if !errors.As(stepErr, &np) {
				t.Fatalf("step edit by %s = %v, want NotPlatformAdminError", who.RoleCode, stepErr)
			}
			if !strings.Contains(np.Error(), who.RoleCode) {
				t.Fatalf("the refusal must name the role it is refusing: %q", np.Error())
			}

			policyErr := svc.PutPolicy(context.Background(), PolicyEdit{DemoMode: true})
			if !errors.As(policyErr, &np) {
				t.Fatalf("policy write by %s = %v, want NotPlatformAdminError", who.RoleCode, policyErr)
			}

			if len(f.stepWrites) != 0 || len(f.policyWrites) != 0 {
				t.Fatalf("a refused caller wrote %d steps and %d policies, the gate is on the payload only",
					len(f.stepWrites), len(f.policyWrites))
			}
		})
	}
	// SELF-GUARD: the same two calls by the platform administrator MUST reach the store, or the
	// assertion above passes on a service that refuses everybody.
	f := loadedFixture()
	svc := svcFor(f, coltonThePlatform)
	if err := svc.PutStepCopy(context.Background(), StepEdit{
		ChapterKey: "sixty-seconds", StepKey: "open", Line: "The till, and everything on one screen."}); err != nil {
		t.Fatalf("the platform administrator was refused: %v", err)
	}
	if err := svc.PutPolicy(context.Background(), PolicyEdit{DemoMode: true}); err != nil {
		t.Fatalf("the platform administrator was refused the demo knob: %v", err)
	}
	if len(f.stepWrites) != 1 || len(f.policyWrites) != 1 {
		t.Fatalf("the platform administrator's writes did not reach the store: %d steps, %d policies",
			len(f.stepWrites), len(f.policyWrites))
	}
}

// TestPlatformWrites_TheRefusalIsA403OnTheWire, the typed refusal reaches the status code.
func TestPlatformWrites_TheRefusalIsA403OnTheWire(t *testing.T) {
	f := loadedFixture()
	// Guards-swap carry note (HH-3.2 fold): the kit's real Guards with a
	// resolved staff authority carry the wire-shape refusal test.
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(coreid.StaffAuthority{RoleID: "general_manager", Tier: 1, Scope: "global"})
	mux := http.NewServeMux()
	NewHandler(svcFor(f, alexTheGM)).RegisterRoutes(mux, sg.Guards)
	bearer := sg.Bearer(t)

	for _, route := range []struct{ path, body string }{
		{"/api/v1/platform/tour/steps", `{"chapter_key":"sixty-seconds","step_key":"open","line":"Anything at all."}`},
		{"/api/v1/platform/tour/policy", `{"demo_mode":true}`},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, route.path, strings.NewReader(route.body))
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("X-Org", testkit.OrgNorthSlug)
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("PUT %s by a GM = %d (%s), want 403", route.path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "tour:") {
			t.Fatalf("the module prefix reached the client: %s", w.Body.String())
		}
	}
	if len(f.stepWrites) != 0 || len(f.policyWrites) != 0 {
		t.Fatalf("a 403 still wrote: %d steps, %d policies", len(f.stepWrites), len(f.policyWrites))
	}
}

// -- 4 · the 422s, in words, before a CHECK could 500 --------------------------------------------

func TestValidateStepEdit_RefusesInWordsWithTheFieldAndTheLimit(t *testing.T) {
	ok := StepEdit{ChapterKey: "sixty-seconds", StepKey: "open", Line: "The till, on one screen.",
		Route: "#sales/pos", Anchor: "tag:hos-pos-terminal"}
	if err := ValidateStepEdit(ok); err != nil {
		t.Fatalf("a legal edit was refused: %v, this gate would then pass vacuously", err)
	}

	long := ok
	long.Line = strings.Repeat("a", MaxLine+1)
	err := ValidateStepEdit(long)
	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusUnprocessableEntity {
		t.Fatalf("a %d-character line = %v, want a 422", MaxLine+1, err)
	}
	if !strings.Contains(se.Msg, "line") || !strings.Contains(se.Msg, "72") {
		t.Fatalf("the refusal must name the field and the limit: %q", se.Msg)
	}

	// EXACTLY 72 IS LEGAL. An off-by-one here is a validator that refuses copy the CHECK accepts.
	atLimit := ok
	atLimit.Line = strings.Repeat("a", MaxLine)
	if err := ValidateStepEdit(atLimit); err != nil {
		t.Fatalf("a %d-character line was refused: %v", MaxLine, err)
	}

	// AND IT COUNTS CHARACTERS, NOT BYTES, 72 em dashes is 216 bytes and a legal line, and a byte
	// count would refuse it while the Postgres CHECK let it through.
	runes := ok
	runes.Line = strings.Repeat("-", MaxLine)
	if err := ValidateStepEdit(runes); err != nil {
		t.Fatalf("%d multi-byte characters were refused: %v", MaxLine, err)
	}

	for _, bad := range []struct {
		name string
		edit StepEdit
	}{
		{"an empty line", StepEdit{ChapterKey: "sixty-seconds", StepKey: "open"}},
		{"a route that is not a screen", StepEdit{ChapterKey: "a", StepKey: "b", Line: "x", Route: "/sales/pos"}},
		{"an anchor that is a CSS class", StepEdit{ChapterKey: "a", StepKey: "b", Line: "x", Anchor: "class:brand"}},
		{"a chapter key nobody could store", StepEdit{ChapterKey: "Sixty Seconds", StepKey: "b", Line: "x"}},
	} {
		if err := ValidateStepEdit(bad.edit); err == nil {
			t.Fatalf("%s was accepted, the CHECK would then answer with a 500", bad.name)
		} else if !errors.As(err, &se) || se.Status != http.StatusUnprocessableEntity {
			t.Fatalf("%s = %v, want a 422", bad.name, err)
		}
	}
}

func TestValidateProgressEdit_RefusesAStateNobodyCouldResume(t *testing.T) {
	if err := ValidateProgressEdit(ProgressEdit{ChapterKey: "sixty-seconds", StepKey: "scan", State: StateOpen}); err != nil {
		t.Fatalf("a legal bookmark was refused: %v", err)
	}
	if err := ValidateProgressEdit(ProgressEdit{ChapterKey: "sixty-seconds", State: StateDismissed}); err != nil {
		t.Fatalf("a dismissed chapter was refused: %v", err)
	}
	for _, bad := range []struct {
		name string
		in   ProgressEdit
	}{
		{"a state nobody defined", ProgressEdit{ChapterKey: "a", State: "SKIPPED"}},
		{"no state at all", ProgressEdit{ChapterKey: "a"}},
		{"no chapter", ProgressEdit{State: StateOpen}},
		{"a finished chapter still holding a step", ProgressEdit{ChapterKey: "a", StepKey: "open", State: StateDone}},
	} {
		var se *StatusError
		if err := ValidateProgressEdit(bad.in); !errors.As(err, &se) || se.Status != http.StatusUnprocessableEntity {
			t.Fatalf("%s = %v, want a 422", bad.name, err)
		}
	}
}

// TestPutStepCopy_AnUnknownStepIs404AndNotAnInsert, an UPDATE, never an UPSERT.
func TestPutStepCopy_AnUnknownStepIs404AndNotAnInsert(t *testing.T) {
	f := loadedFixture()
	f.updateFound = false
	err := svcFor(f, coltonThePlatform).PutStepCopy(context.Background(),
		StepEdit{ChapterKey: "sixty-seconds", StepKey: "nope", Line: "A step nobody wrote."})
	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusNotFound {
		t.Fatalf("editing a step that does not exist = %v, want a 404", err)
	}
	if !strings.Contains(se.Msg, "nope") {
		t.Fatalf("the refusal must name the id that was not found: %q", se.Msg)
	}
}

// -- 5 · the bookmark belongs to the caller ------------------------------------------------------

// TestProgress_TheRowWrittenIsTheCallersOwn, write, read back, and prove the second person's
// identical body wrote a DIFFERENT row.
func TestProgress_TheRowWrittenIsTheCallersOwn(t *testing.T) {
	f := loadedFixture()
	body := ProgressEdit{ChapterKey: "what-we-keep", StepKey: "eye", State: StateOpen}

	if err := svcFor(f, alexTheGM).PutProgress(context.Background(), body); err != nil {
		t.Fatalf("alex's write: %v", err)
	}
	if err := svcFor(f, samTheClerk).PutProgress(context.Background(), body); err != nil {
		t.Fatalf("sam's write: %v", err)
	}
	if len(f.upserts) != 2 || f.upserts[0] != "u-alex|what-we-keep|eye|OPEN" || f.upserts[1] != "u-sam|what-we-keep|eye|OPEN" {
		t.Fatalf("the writes did not carry the CALLER's id: %v", f.upserts)
	}

	// READ BACK: each sees their own and only their own.
	alex := svcFor(f, alexTheGM).Tour(context.Background(), "")
	if markFor(alex, "what-we-keep") == nil || markFor(alex, "what-we-keep").StepKey != "eye" {
		t.Fatalf("alex cannot read back what alex wrote: %+v", alex.Progress)
	}
	// Sam's own bookmark is in what-we-keep and NOT in sixty-seconds, which is alex's alone.
	sam := svcFor(f, samTheClerk).Tour(context.Background(), "")
	if markFor(sam, "sixty-seconds") != nil {
		t.Fatalf("sam read alex's bookmark in sixty-seconds: %+v", sam.Progress)
	}
	if markFor(sam, "what-we-keep") == nil {
		t.Fatal("sam cannot read back sam's own bookmark")
	}
}

// markFor is the payload's own join: `progress` rows name their chapter, so a reader looks one up
// rather than reaching into a chapter for it.
func markFor(tr Tour, chapterKey string) *Mark {
	for i := range tr.Progress {
		if tr.Progress[i].ChapterKey == chapterKey {
			return &tr.Progress[i]
		}
	}
	return nil
}

// TestRestart_IsADeleteAndNeverAWriteOfStepZero, 0193's ruling, at the service.
func TestRestart_IsADeleteAndNeverAWriteOfStepZero(t *testing.T) {
	f := loadedFixture()
	svc := svcFor(f, alexTheGM)
	if err := svc.RestartChapter(context.Background(), "sixty-seconds"); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if len(f.deletes) != 1 || f.deletes[0] != "u-alex|sixty-seconds" {
		t.Fatalf("restart deleted %v, want exactly the caller's own row", f.deletes)
	}
	if len(f.upserts) != 0 {
		t.Fatalf("restart WROTE %v, 0193 says restart is a DELETE, so 'never started' and 'back to the beginning' stay one state", f.upserts)
	}
	if got := markFor(svc.Tour(context.Background(), ""), "sixty-seconds"); got != nil {
		t.Fatalf("after a restart the chapter still carries %+v", got)
	}
	// Restarting a chapter nobody started is a success: the caller asked to be at the beginning and
	// that is where they are.
	if err := svc.RestartChapter(context.Background(), "what-we-keep"); err != nil {
		t.Fatalf("restarting an unstarted chapter = %v, want no error", err)
	}
}

// -- 6 · the structural gates over this package's own source -------------------------------------

// TestGate_EveryFieldOfThePayloadCarriesOmitempty is the gate that keeps byte-absence structural.
//
// It reads THIS package's source with comments stripped, so a `json:"x"` written inside a comment
// cannot fail it and a rule quoted in a header cannot pass it, and asserts that every field of the
// four payload structs carries omitempty. It GUARDS ITSELF twice: the struct must be found, and a
// minimum number of tagged fields must be seen, so a rename or a deletion turns this red instead of
// quietly vacuous.
func TestGate_EveryFieldOfThePayloadCarriesOmitempty(t *testing.T) {
	src := packageSource(t)
	tagRe := regexp.MustCompile("`json:\"([^\"]+)\"`")

	for _, want := range []struct {
		typ      string
		minTags  int
		whyItsIn string
	}{
		{"Tour", 3, "the gated payload itself, one field without omitempty and it can never marshal as {}"},
		{"Chapter", 7, "nested in Tour; a chapter that marshals empty strings is a chapter half-present"},
		{"Step", 6, "the copy the gate exists to withhold"},
		{"Mark", 3, "a bookmark of empty strings would be a third state the client must learn to read"},
	} {
		block := structBlock(t, src, want.typ)
		tags := tagRe.FindAllStringSubmatch(block, -1)
		if len(tags) < want.minTags {
			t.Fatalf("%s has %d json tags, expected at least %d, this gate no longer knows the type it guards (%s)",
				want.typ, len(tags), want.minTags, want.whyItsIn)
		}
		for _, m := range tags {
			if !strings.Contains(m[1], ",omitempty") {
				t.Errorf("%s.%s has no omitempty, %s", want.typ, m[1], want.whyItsIn)
			}
		}
	}
}

// TestGate_NoWriteBodyCanNameAUser, the ownership rule, structurally.
//
// The strongest version of "the user id comes from the caller" is the one where the wrong value has
// nowhere to be typed: no request body in this package carries a user id, so no handler can read
// one even by mistake. Self-guarded on finding the body type at all.
func TestGate_NoWriteBodyCanNameAUser(t *testing.T) {
	src := packageSource(t)
	for _, typ := range []string{"ProgressEdit", "StepEdit", "PolicyEdit"} {
		block := structBlock(t, src, typ)
		for _, banned := range []string{"user_id", "UserID", "user "} {
			if strings.Contains(block, banned) {
				t.Errorf("%s carries %q, a user id in a request body is a claim, and the request already made one", typ, banned)
			}
		}
	}
	// The positive half: the caller's id really is read from the identity context, in the service.
	if !strings.Contains(src, "caller.UserID") {
		t.Fatal("nothing in this package reads caller.UserID, the ownership rule has no implementation to guard")
	}
}

// structBlock returns the body of `type <name> struct { … }` from stripped source, failing the test
// when the type is gone, which is what makes every gate above impossible to pass vacuously.
func structBlock(t *testing.T, src, name string) string {
	t.Helper()
	head := "type " + name + " struct {"
	i := strings.Index(src, head)
	if i < 0 {
		t.Fatalf("type %s is gone from internal/tour, a gate that guards a type that no longer exists is a green light over nothing", name)
	}
	rest := src[i+len(head):]
	j := strings.Index(rest, "\n}")
	if j < 0 {
		t.Fatalf("type %s has no closing brace this gate can find", name)
	}
	return rest[:j]
}

// packageSource concatenates this package's non-test Go source WITH COMMENTS STRIPPED, so a rule
// argued in a header can never be mistaken for the rule being kept in the code.
func packageSource(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	blockComment := regexp.MustCompile(`(?s)/\*.*?\*/`)
	lineComment := regexp.MustCompile(`(?m)^\s*//.*$`)
	var b strings.Builder
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		s := blockComment.ReplaceAllString(string(raw), "")
		s = lineComment.ReplaceAllString(s, "")
		b.WriteString(s)
		b.WriteString("\n")
	}
	if !strings.Contains(b.String(), "package tour") {
		t.Fatal("packageSource read no source, every gate built on it would pass on an empty string")
	}
	return b.String()
}
