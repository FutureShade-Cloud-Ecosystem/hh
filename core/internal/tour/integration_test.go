//go:build integration

// integration_test.go, the walkthrough against a real migrated Postgres.
//
//	TZ=UTC DATABASE_URL=postgres://…/hos_tour go test -tags=integration -count=1 ./internal/tour/
//
// WHAT ONLY A DATABASE CAN PROVE, and is therefore proved here rather than against a fake:
//
//   - 0192 REALLY SEEDED THE NINE CHAPTERS AND THE 26 STEPS, in the order the migration wrote them.
//     A Go fixture asserting nine chapters proves that a Go fixture has nine chapters; this proves
//     the migration and the reader cannot drift apart without a red test.
//   - THE POLICY REALLY GATES THE PAYLOAD, through the real store, the real service and the real
//     mux, `{}` on the wire and not merely a zero value in a struct.
//   - A COPY EDIT IS LIVE WITH NO DEPLOY. The acceptance clause is a sequence, not a state: PUT the
//     new line, GET again, read the new line. Nothing short of two round trips over one database
//     can show that.
//   - THE PLATFORM REFUSAL WRITES NOTHING. The row is re-read after the 403 and proved byte-for-byte
//     unchanged, billing's TestReminderSend_IsRefusedOnBaseTier is the precedent, and the reason is
//     that byte-absence gates the DISPLAY and a hand-crafted PUT is not a display.
//   - RESTART IS A DELETE. Only SQL can tell "the row is gone" from "the row says step 0", and those
//     are the two readings 0193 spent a header separating.
//
// FIXTURES RESTORE WHAT THEY TOUCH. Every test that writes snapshots the row first and puts it back
// on cleanup, so the seeded content other suites (and the running demo) read is unaltered and this
// file is safe beside them.

package tour

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping tour integration tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// twoRealUsers returns two user ids off the deployment. They are READ, not invented: tour_policy
// .updated_by and tour_user_progress.user_id are both foreign keys into users, so a made-up uuid
// would fail the write for a reason that has nothing to do with what is being tested.
func twoRealUsers(t *testing.T, pool *pgxpool.Pool) (string, string) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT id::text FROM users ORDER BY id LIMIT 2`)
	if err != nil {
		t.Fatalf("read users: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan user: %v", err)
		}
		ids = append(ids, id)
	}
	if len(ids) < 2 {
		t.Skipf("this database has %d users; the ownership test needs two", len(ids))
	}
	return ids[0], ids[1]
}

// snapshotPolicy restores the dealer NULL row's demo_mode on cleanup.
func snapshotPolicy(t *testing.T, repo *Repository) {
	t.Helper()
	before, err := repo.ReadPolicy(context.Background(), "")
	if err != nil {
		t.Fatalf("snapshot policy: %v", err)
	}
	t.Cleanup(func() {
		if err := repo.SetDemoMode(context.Background(), "", before.DemoMode, ""); err != nil {
			t.Fatalf("restore policy: %v", err)
		}
	})
}

// snapshotStep restores one step's three editable fields on cleanup.
func snapshotStep(t *testing.T, pool *pgxpool.Pool, chapter, step string) StepEdit {
	t.Helper()
	before := StepEdit{ChapterKey: chapter, StepKey: step}
	err := pool.QueryRow(context.Background(),
		`SELECT line, route, anchor FROM platform_tour_steps WHERE chapter_key = $1 AND step_key = $2`,
		chapter, step).Scan(&before.Line, &before.Route, &before.Anchor)
	if err != nil {
		t.Fatalf("snapshot %s/%s: %v", chapter, step, err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`UPDATE platform_tour_steps SET line = $3, route = $4, anchor = $5, updated_by = NULL
			  WHERE chapter_key = $1 AND step_key = $2`,
			chapter, step, before.Line, before.Route, before.Anchor); err != nil {
			t.Fatalf("restore %s/%s: %v", chapter, step, err)
		}
	})
	return before
}

// clearProgressFor removes a person's bookmarks before and after a test, so a rerun starts clean
// and the demo deployment is left as it was found.
func clearProgressFor(t *testing.T, pool *pgxpool.Pool, userIDs ...string) {
	t.Helper()
	wipe := func() {
		for _, id := range userIDs {
			if _, err := pool.Exec(context.Background(),
				`DELETE FROM tour_user_progress WHERE user_id = $1::uuid`, id); err != nil {
				t.Fatalf("clear progress for %s: %v", id, err)
			}
		}
	}
	wipe()
	t.Cleanup(wipe)
}

// intMux builds the real handler over the real store, with the door guard passed through, the
// platform gate under test lives in the service, and identity's own suite owns the door.
// Guards-swap carry note (HH-3.2 fold): the pass-through door stub became
// the kit's real Guards behind a wrapper that injects the kit bearer on
// every request; the platform gate under test lives in the service, and
// identity's own suite owns the door.
//
// The wrapper drives BOTH halves of the production request path, because production reads
// neither the token nor any context a test may have pre-stamped for authority. The guard
// resolves whatever the kit's authority stub last resolved (identity.SessionCeiling refuses an
// authority with no resolved role, so a stub left at its zero value is a 503, fail closed), so
// call() sets it from the request's `who` through SetAuthority. And the handler's staff stamp
// (caller_stamp.go) rebuilds the bridged StaffContext from the guard's resolved caller plus the
// handler's user linker, exactly as the composition root wires it (wire_tops.go registers the
// tour handler WithUserLinker over users.platform_user_id), so intMux supplies a linker too.
// A context stamped by the test itself is overwritten by that stamp, which is why none is
// stamped here: everything the service reads about the caller arrived through the guard.
type tourMux struct {
	mux    *http.ServeMux
	bearer string
	sg     *testkit.StaffGuards
	// who is the caller of the request in flight. call() sets it before the request runs, and
	// both halves of the production path read it: the guard through SetAuthority, the linker as
	// the users.id it answers with.
	who identity.StaffContext
}

func (m *tourMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Header.Set("Authorization", "Bearer "+m.bearer)
	r.Header.Set("X-Org", testkit.OrgNorthSlug)
	m.mux.ServeHTTP(w, r)
}

func intMux(t *testing.T, pool *pgxpool.Pool) *tourMux {
	sg := testkit.NewStaffGuards(t)
	m := &tourMux{sg: sg}
	// The linker stands in for the composition root's users.platform_user_id read. The kit
	// bearer admits one subject, so the platform user id is the same on every request and the
	// caller of THIS request is the one call() planted last; ok false (no planted user) leaves
	// the stamp authority-only and every id-consuming read fails closed, the same posture
	// production's nil linker has.
	link := identity.UserIDLinker(func(_ context.Context, _ string) (string, bool) {
		id := m.who.UserID
		return id, id != ""
	})
	mux := http.NewServeMux()
	NewHandler(NewService(NewRepository(pool))).WithUserLinker(link).RegisterRoutes(mux, sg.Guards)
	m.mux = mux
	m.bearer = sg.Bearer(t)
	return m
}

func as(userID, role string) identity.StaffContext {
	return identity.StaffContext{UserID: userID, RoleCode: role,
		Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: "ACTIVE"}
}

func call(t *testing.T, mux *tourMux, method, path, body string, who identity.StaffContext) *httptest.ResponseRecorder {
	t.Helper()
	// THE GUARD RESOLVES THE CALLER; THE TEST DOES NOT STAMP ONE. The authority the stub resolves
	// is the StaffAuthority shape the real resolver reads off staff_role: the canonical role id
	// behind the role code (FromAuthority walks this same mapping table forward to bridge, so
	// resolving its lookup half here is the exact round trip), the tier, and the canonical scope.
	canonical, ok := identity.CanonicalForERPCode(who.RoleCode)
	if !ok {
		t.Fatalf("call: %q is not a role code the identity bridge maps", who.RoleCode)
	}
	branches := make([]string, 0, len(who.Branches))
	for b := range who.Branches {
		branches = append(branches, b)
	}
	sort.Strings(branches)
	mux.sg.SetAuthority(coreid.StaffAuthority{
		RoleID:   canonical,
		Tier:     int(who.Tier),
		Scope:    identity.CanonicalScope(who.Scope),
		Branches: branches,
	})
	mux.who = who
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

// -- 1 · off is `{}`, on is the whole seeded walkthrough in order --------------------------------

func TestIntegration_Tour_OffIsEmptyAndOnIsTheNineChapters(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	snapshotPolicy(t, repo)
	platform, _ := twoRealUsers(t, pool)
	mux := intMux(t, pool)
	ctx := context.Background()

	// -- OFF: the seeded default. demo_mode is FALSE out of 0192, so a fresh deployment is silent.
	if err := repo.SetDemoMode(ctx, "", false, platform); err != nil {
		t.Fatalf("set demo off: %v", err)
	}
	off := call(t, mux, http.MethodGet, "/api/v1/tour", "", as(platform, identity.RoleSuperAdmin))
	if off.Code != http.StatusOK {
		t.Fatalf("GET while off = %d, want 200", off.Code)
	}
	if got := strings.TrimSpace(off.Body.String()); got != "{}" {
		t.Fatalf("GET while off = %q, want {}, byte-absent from the payload, not hidden in the client", got)
	}
	for _, w := range []string{"chapters", "steps", "line", "null"} {
		if strings.Contains(off.Body.String(), w) {
			t.Fatalf("the off payload carries %q: %s", w, off.Body.String())
		}
	}

	// -- ON: through the real platform route, which is also the proof that the knob is reachable.
	if w := call(t, mux, http.MethodPut, "/api/v1/platform/tour/policy",
		`{"demo_mode":true}`, as(platform, identity.RoleSuperAdmin)); w.Code != http.StatusOK {
		t.Fatalf("PUT policy = %d (%s), want 200", w.Code, w.Body.String())
	}

	on := call(t, mux, http.MethodGet, "/api/v1/tour", "", as(platform, identity.RoleSuperAdmin))
	var got Tour
	if err := json.Unmarshal(on.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode the on payload: %v, %s", err, on.Body.String())
	}

	// 0192 §4 SEEDS NINE AND 0211 ADDS THE TENTH, at the end. `receivables-rules` is the chapter the
	// demo's fourth card is about: 0210 had appended its five steps to `your-rules` (a SUPER_ADMIN
	// chapter about keyboard shortcuts) because the brief it worked from misidentified that chapter,
	// and 0211 moves them onto a FINANCIAL_CONTROLLER chapter of their own. Position 9 rather than a
	// slot beside chase-invoice, so that nothing already seeded is renumbered, see 0211's header.
	wantChapters := []string{"sixty-seconds", "what-we-keep", "bulk-board", "requested",
		"pick-to-proof", "will-call", "chase-invoice", "own-tile", "your-rules", "receivables-rules"}
	if len(got.Chapters) != len(wantChapters) {
		t.Fatalf("chapters = %d, want %d (0192 §4 seeds nine, 0211 adds the tenth)", len(got.Chapters), len(wantChapters))
	}
	for i, want := range wantChapters {
		if got.Chapters[i].ChapterKey != want {
			t.Fatalf("chapter %d = %q, want %q, the read must come back in position order",
				i, got.Chapters[i].ChapterKey, want)
		}
		// THE POSITION 0192 STORED IS ON THE WIRE, and it is the seeded one. The consumer re-sorts
		// by it, so a payload that dropped it would reorder the demo alphabetically.
		if got.Chapters[i].Position != i {
			t.Fatalf("chapter %s carries position %d, want %d", want, got.Chapters[i].Position, i)
		}
		if got.Chapters[i].Title == "" || got.Chapters[i].Role == "" || got.Chapters[i].Route == "" {
			t.Fatalf("chapter %s is missing its title/role/route: %+v", want, got.Chapters[i])
		}
	}

	total := 0
	for _, c := range got.Chapters {
		total += len(c.Steps)
		for _, s := range c.Steps {
			if s.Line == "" {
				t.Fatalf("%s/%s has no line, a step with nothing to say reached the wire", c.ChapterKey, s.StepKey)
			}
			if n := len([]rune(s.Line)); n > MaxLine {
				t.Fatalf("%s/%s is %d characters, over the %d budget", c.ChapterKey, s.StepKey, n, MaxLine)
			}
		}
	}
	// 0192 §4 seeded twenty-six steps across the nine chapters. 0210 ("four flows that finish") added
	// nineteen more, rows only, no new chapter, so that the four demo flows run through to their
	// WRITE instead of stopping one screen short. 26 + 19 = 45, and the number is pinned rather than
	// derived because the point of this assertion has never been the total: it is that a migration
	// which touches the tour has to come here and say so.
	//
	// 0211 CHANGED NO TOTAL AND THAT IS THE POINT OF IT. It MOVED five of 0210's steps from
	// `your-rules` to the new `receivables-rules`, same keys, same lines, renumbered from 0, so the
	// chain has added a chapter and not a single step. A total that had gone UP here would have meant
	// the correction copied rather than moved, and left the receivables flow living in two places.
	if total != 45 {
		t.Fatalf("steps = %d, want 45 (0192 §4 seeds 26, 0210 adds 19, 0211 moves 5 without adding any)", total)
	}
	// The move itself, both ends of it: the platform-key chapter is three steps again, and the five
	// that left are all on the new chapter.
	byKey := map[string][]Step{}
	for _, c := range got.Chapters {
		byKey[c.ChapterKey] = c.Steps
	}
	if n := len(byKey["your-rules"]); n != 3 {
		t.Fatalf("your-rules has %d steps, want 3, it is the SUPER_ADMIN platform-key chapter and nothing else (0211)", n)
	}
	wantAR := []string{"limits", "rules", "edge", "save", "history"}
	if n := len(byKey["receivables-rules"]); n != len(wantAR) {
		t.Fatalf("receivables-rules has %d steps, want %d", n, len(wantAR))
	}
	for i, want := range wantAR {
		s := byKey["receivables-rules"][i]
		if s.StepKey != want {
			t.Fatalf("receivables-rules step %d = %q, want %q", i, s.StepKey, want)
		}
		if s.Position != i {
			t.Fatalf("receivables-rules step %s carries position %d, want %d, 0211 renumbers the move from 0",
				want, s.Position, i)
		}
	}

	// STEP ORDER, on the chapter the demo opens with.
	//
	// 0210 KEPT ALL FIVE SEEDED KEYS AND RE-ORDERED TWO OF THEM. `customer` moved ahead of `scan`
	// because the counter picks who is buying before it rings anything up, and `pay` slid to the end
	// of a tender sequence that now finishes the sale. The keys below are still the seeded five; what
	// changed is where they sit, which is exactly the kind of change the position check exists to
	// notice. Positions are asserted against the slice index, so this stays a claim that the wire
	// comes back in stored order and not merely that the keys are present.
	wantSteps := []string{"open", "customer", "scan", "browse", "lines", "payment", "tender", "pay", "ring", "sold"}
	for i, want := range wantSteps {
		if got.Chapters[0].Steps[i].StepKey != want {
			t.Fatalf("sixty-seconds step %d = %q, want %q", i, got.Chapters[0].Steps[i].StepKey, want)
		}
		if got.Chapters[0].Steps[i].Position != i {
			t.Fatalf("sixty-seconds step %s carries position %d, want %d",
				want, got.Chapters[0].Steps[i].Position, i)
		}
	}

	// A DEALER WHO TURNS IT OFF GETS `{}` WITH demo_mode STILL TRUE, the two knobs are a product,
	// and this is the half no platform route can write.
	if _, err := pool.Exec(ctx, `UPDATE tour_policy SET dealer_enabled = false WHERE branch_id IS NULL`); err != nil {
		t.Fatalf("turn the dealer knob off: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `UPDATE tour_policy SET dealer_enabled = true WHERE branch_id IS NULL`)
	})
	dealerOff := call(t, mux, http.MethodGet, "/api/v1/tour", "", as(platform, identity.RoleSuperAdmin))
	if body := strings.TrimSpace(dealerOff.Body.String()); body != "{}" {
		t.Fatalf("a dealer who turned it off still gets %q", body)
	}
}

// -- 2 · a copy edit is live on the next GET -----------------------------------------------------

func TestIntegration_PlatformStepEdit_IsLiveOnTheNextRead(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	snapshotPolicy(t, repo)
	before := snapshotStep(t, pool, "sixty-seconds", "open")
	platform, _ := twoRealUsers(t, pool)
	mux := intMux(t, pool)

	if err := repo.SetDemoMode(context.Background(), "", true, platform); err != nil {
		t.Fatalf("set demo on: %v", err)
	}

	const edited = "This is the till. One screen, start to receipt."
	body := `{"chapter_key":"sixty-seconds","step_key":"open","line":"` + edited +
		`","route":"#sales/pos","anchor":"tag:hos-pos-terminal"}`
	if w := call(t, mux, http.MethodPut, "/api/v1/platform/tour/steps", body,
		as(platform, identity.RoleSuperAdmin)); w.Code != http.StatusOK {
		t.Fatalf("PUT step = %d (%s), want 200", w.Code, w.Body.String())
	}

	// THE VERY NEXT GET. No restart, no cache bust, no deploy.
	w := call(t, mux, http.MethodGet, "/api/v1/tour", "", as(platform, identity.RoleSuperAdmin))
	var got Tour
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v, %s", err, w.Body.String())
	}
	if got.Chapters[0].Steps[0].Line != edited {
		t.Fatalf("the next read served %q, want the edited copy %q",
			got.Chapters[0].Steps[0].Line, edited)
	}
	if before.Line == edited {
		t.Fatal("the fixture's line already equalled the edit, this test proves nothing")
	}

	// AND THE VALIDATOR ANSWERS IN WORDS BEFORE THE CHECK COULD 500.
	long := `{"chapter_key":"sixty-seconds","step_key":"open","line":"` + strings.Repeat("a", MaxLine+1) + `"}`
	refusal := call(t, mux, http.MethodPut, "/api/v1/platform/tour/steps", long,
		as(platform, identity.RoleSuperAdmin))
	if refusal.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a %d-character line = %d (%s), want 422", MaxLine+1, refusal.Code, refusal.Body.String())
	}
	if !strings.Contains(refusal.Body.String(), "line") || !strings.Contains(refusal.Body.String(), "72") {
		t.Fatalf("the refusal must name the field and the limit: %s", refusal.Body.String())
	}
	// The refused edit changed nothing.
	var still string
	if err := pool.QueryRow(context.Background(),
		`SELECT line FROM platform_tour_steps WHERE chapter_key = 'sixty-seconds' AND step_key = 'open'`).
		Scan(&still); err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if still != edited {
		t.Fatalf("a 422 wrote: the row now says %q", still)
	}
}

// -- 3 · the platform refusal writes nothing -----------------------------------------------------

// TestIntegration_PlatformWrites_ADealersGMIs403AndWritesNothing is the twin test: the 403 is
// asserted AND the rows are re-read and proved unchanged. Byte-absence gates the display, not the
// act, so a gate that only shaped a payload would leave this PUT working.
func TestIntegration_PlatformWrites_ADealersGMIs403AndWritesNothing(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	snapshotPolicy(t, repo)
	before := snapshotStep(t, pool, "what-we-keep", "eye")
	platform, gm := twoRealUsers(t, pool)
	mux := intMux(t, pool)
	ctx := context.Background()

	// A known starting state for both rows, written by somebody who is allowed to.
	if err := repo.SetDemoMode(ctx, "", true, platform); err != nil {
		t.Fatalf("set demo on: %v", err)
	}

	for _, who := range []identity.StaffContext{
		as(gm, "GENERAL_MANAGER"),
		as(gm, "FINANCIAL_CONTROLLER"),
		{UserID: gm, RoleCode: "INSIDE_SALES", Tier: identity.TierFrontline, Scope: identity.ScopeBranch, Status: "ACTIVE"},
	} {
		step := call(t, mux, http.MethodPut, "/api/v1/platform/tour/steps",
			`{"chapter_key":"what-we-keep","step_key":"eye","line":"A line a dealer may not write."}`, who)
		if step.Code != http.StatusForbidden {
			t.Fatalf("PUT step as %s = %d (%s), want 403", who.RoleCode, step.Code, step.Body.String())
		}
		policy := call(t, mux, http.MethodPut, "/api/v1/platform/tour/policy", `{"demo_mode":false}`, who)
		if policy.Code != http.StatusForbidden {
			t.Fatalf("PUT policy as %s = %d (%s), want 403", who.RoleCode, policy.Code, policy.Body.String())
		}
	}

	// RE-READ AND PROVE UNCHANGED, the half that makes this a twin test rather than a status check.
	var line, route, anchor string
	if err := pool.QueryRow(ctx,
		`SELECT line, route, anchor FROM platform_tour_steps WHERE chapter_key = 'what-we-keep' AND step_key = 'eye'`).
		Scan(&line, &route, &anchor); err != nil {
		t.Fatalf("re-read the step: %v", err)
	}
	if line != before.Line || route != before.Route || anchor != before.Anchor {
		t.Fatalf("a refused caller changed the step: line %q→%q, route %q→%q, anchor %q→%q",
			before.Line, line, before.Route, route, before.Anchor, anchor)
	}
	after, err := repo.ReadPolicy(ctx, "")
	if err != nil {
		t.Fatalf("re-read the policy: %v", err)
	}
	if !after.DemoMode {
		t.Fatal("a refused caller turned the demo off")
	}

	// SELF-GUARD: the platform administrator's identical PUTs DO land, or the assertions above pass
	// on a route that refuses everybody.
	ok := call(t, mux, http.MethodPut, "/api/v1/platform/tour/steps",
		`{"chapter_key":"what-we-keep","step_key":"eye","line":"Margin lives on the ticket."}`,
		as(platform, identity.RoleSuperAdmin))
	if ok.Code != http.StatusOK {
		t.Fatalf("the platform administrator was refused: %d (%s)", ok.Code, ok.Body.String())
	}
	if err := pool.QueryRow(ctx,
		`SELECT line FROM platform_tour_steps WHERE chapter_key = 'what-we-keep' AND step_key = 'eye'`).
		Scan(&line); err != nil {
		t.Fatalf("re-read after the allowed write: %v", err)
	}
	if line != "Margin lives on the ticket." {
		t.Fatalf("the allowed write did not land: %q", line)
	}
}

// -- 4 · the bookmark: write, read back, restart, and whose row it is ----------------------------

func TestIntegration_Progress_WriteReadRestartAndOwnership(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	snapshotPolicy(t, repo)
	alex, sam := twoRealUsers(t, pool)
	clearProgressFor(t, pool, alex, sam)
	mux := intMux(t, pool)
	ctx := context.Background()

	if err := repo.SetDemoMode(ctx, "", true, alex); err != nil {
		t.Fatalf("set demo on: %v", err)
	}

	// -- write, as two different people, with the SAME body. The body names no user; the caller does.
	const body = `{"chapter_key":"sixty-seconds","step_key":"scan","state":"OPEN"}`
	if w := call(t, mux, http.MethodPut, "/api/v1/tour/progress", body, as(alex, "GENERAL_MANAGER")); w.Code != http.StatusOK {
		t.Fatalf("alex's write = %d (%s)", w.Code, w.Body.String())
	}
	const samsBody = `{"chapter_key":"bulk-board","step_key":"curate","state":"OPEN"}`
	if w := call(t, mux, http.MethodPut, "/api/v1/tour/progress", samsBody, as(sam, "INSIDE_SALES")); w.Code != http.StatusOK {
		t.Fatalf("sam's write = %d (%s)", w.Code, w.Body.String())
	}

	// -- read back: each sees their own and only their own.
	alexTour := readTour(t, mux, as(alex, "GENERAL_MANAGER"))
	if p := bookmark(alexTour, "sixty-seconds"); p == nil || p.StepKey != "scan" || p.State != StateOpen {
		t.Fatalf("alex cannot read back alex's bookmark: %+v", p)
	}
	if p := bookmark(alexTour, "bulk-board"); p != nil {
		t.Fatalf("alex read sam's bookmark: %+v", p)
	}
	samTour := readTour(t, mux, as(sam, "INSIDE_SALES"))
	if p := bookmark(samTour, "bulk-board"); p == nil || p.StepKey != "curate" {
		t.Fatalf("sam cannot read back sam's bookmark: %+v", p)
	}
	if p := bookmark(samTour, "sixty-seconds"); p != nil {
		t.Fatalf("sam read alex's bookmark: %+v", p)
	}

	// -- the row really is per person, in SQL.
	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM tour_user_progress WHERE chapter_key IN ('sixty-seconds','bulk-board')
		   AND user_id IN ($1::uuid, $2::uuid)`, alex, sam).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 2 {
		t.Fatalf("rows = %d, want 2, one per person", rows)
	}

	// -- DONE clears the step, because there is nothing to come back to.
	if w := call(t, mux, http.MethodPut, "/api/v1/tour/progress",
		`{"chapter_key":"sixty-seconds","state":"DONE"}`, as(alex, "GENERAL_MANAGER")); w.Code != http.StatusOK {
		t.Fatalf("alex's DONE = %d (%s)", w.Code, w.Body.String())
	}
	if p := bookmark(readTour(t, mux, as(alex, "GENERAL_MANAGER")), "sixty-seconds"); p == nil || p.State != StateDone || p.StepKey != "" {
		t.Fatalf("a DONE chapter reads back as %+v", p)
	}

	// -- RESTART IS A DELETE, and only SQL can tell that from a write of step 0.
	if w := call(t, mux, http.MethodDelete, "/api/v1/tour/progress/sixty-seconds", "", as(alex, "GENERAL_MANAGER")); w.Code != http.StatusOK {
		t.Fatalf("alex's restart = %d (%s)", w.Code, w.Body.String())
	}
	var left int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM tour_user_progress WHERE user_id = $1::uuid AND chapter_key = 'sixty-seconds'`,
		alex).Scan(&left); err != nil {
		t.Fatalf("count after restart: %v", err)
	}
	if left != 0 {
		t.Fatalf("restart left %d row(s), 0193 says restart DELETES, so 'never started' and 'back to the beginning' stay one state", left)
	}
	if p := bookmark(readTour(t, mux, as(alex, "GENERAL_MANAGER")), "sixty-seconds"); p != nil {
		t.Fatalf("after a restart the chapter still carries %+v", p)
	}

	// -- AND SAM'S ROW SURVIVED ALEX'S RESTART. A DELETE scoped to the caller is the whole point.
	if p := bookmark(readTour(t, mux, as(sam, "INSIDE_SALES")), "bulk-board"); p == nil {
		t.Fatal("alex's restart deleted sam's bookmark")
	}

	// -- a state nobody defined is a 422 in words, not a CHECK's 500.
	bad := call(t, mux, http.MethodPut, "/api/v1/tour/progress",
		`{"chapter_key":"sixty-seconds","step_key":"scan","state":"SKIPPED"}`, as(alex, "GENERAL_MANAGER"))
	if bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown state = %d (%s), want 422", bad.Code, bad.Body.String())
	}
}

func readTour(t *testing.T, mux *tourMux, who identity.StaffContext) Tour {
	t.Helper()
	w := call(t, mux, http.MethodGet, "/api/v1/tour", "", who)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/tour = %d (%s)", w.Code, w.Body.String())
	}
	var out Tour
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v, %s", err, w.Body.String())
	}
	if len(out.Chapters) == 0 {
		t.Fatalf("the walkthrough came back empty for %s, the bookmark assertions would then pass vacuously", who.RoleCode)
	}
	return out
}

// bookmark is the payload's own join: `progress` is a sibling collection keyed by chapter_key, so a
// reader looks a mark up rather than reaching into a chapter for it.
func bookmark(tr Tour, chapterKey string) *Mark {
	for i := range tr.Progress {
		if tr.Progress[i].ChapterKey == chapterKey {
			return &tr.Progress[i]
		}
	}
	return nil
}
