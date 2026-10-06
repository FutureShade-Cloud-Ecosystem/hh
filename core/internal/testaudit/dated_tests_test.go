// Package testaudit is a TEST-ONLY package (it has no non-test source) that audits the test suite
// itself. It contains no production code and nothing imports it.
//
// -- DIB-427 · the dated-test tripwire ------------------------------------------------------------
//
// THE BUG CLASS. A test that pins a calendar instant, time.Date(2026, 7, 14, …), and then compares
// it against a REAL clock (time.Now() in Go, or now()/CURRENT_DATE/CURRENT_TIMESTAMP inside SQL the
// test executes) is not a test. It is a claim about a date that has not arrived yet. The gap between
// the two sides widens by one day per real day, and on some future morning the suite turns red for
// nobody's commit, or, worse, an age silently clamps to zero and the assertion goes vacuous while
// still reporting PASS.
//
// This is not hypothetical. On 2026-08-06 at 08:00 EDT,
// TestIntegration_ARCadence_QuietHoursHoldTheNotice_AndItAccruesNoAttempts began failing permanently
// (W6-D §5), and on 2026-08-11 the collections fixture was found writing invoice ages against
// CURRENT_DATE while the sweep under test ran at a pinned 2026-08-05. Both were repaired by making
// the fixture and the code-under-test read ONE clock. This file exists so the class cannot re-arm
// quietly: the next person to write the combination gets a failing test on the day they write it
// rather than on an arbitrary day months later.
//
// WHY AN AST WALK AND NOT A GREP. The W6-D audit was grep-based and over-reported: it flagged
// messagingprovider/policy_test.go, whose only "now()" is a fakeClock METHOD, and
// dispatch/dispatch_test.go, whose only "time.Now" is a word in a comment. A parser sees the
// difference for free, time.Now() is matched as a call expression, and the SQL tokens are matched
// only inside string literals, so the tripwire does not cry wolf and nobody learns to ignore it.
//
// WHAT IT CANNOT KNOW, AND THE HONEST CONSEQUENCE. The scan is FILE-scoped. It can prove the two
// ingredients are in one file; it cannot prove they meet in one assertion, because that needs the
// call graph through the code under test. So it is deliberately conservative, it reports the
// combination and requires a human to sign each surviving instance off in the allowlist below, with
// a reason. An allowlist entry is a claim someone made and can be held to; silence is not.
package testaudit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// exemption is one signed-off file: WHICH kinds of real clock are excused there, and why.
//
// The excuses are per-KIND, not per-file, and that is the whole point. A file-level allowlist can be
// re-armed silently: warranty/integration_test.go is excused for a time.Now() cleanup window, and if
// somebody later re-anchors a fixture to CURRENT_DATE in that same file, a file-level entry waves the
// new bomb straight through. This design was not theoretical, it is what the DIB-427 mutation test
// found when it re-armed exactly that file and the guard stayed green. Now an allowlisted file that
// grows a kind of clock its entry does not name is reported like any other offender.
type exemption struct {
	// excuses names the clock kinds this entry covers, spelled exactly as the audit labels them:
	// "time.Now()", "SQL now()", "SQL CURRENT_DATE", "SQL CURRENT_TIMESTAMP".
	excuses []string
	// reason is a one-line justification. It is REQUIRED (TestAllowlistIsHonest fails without it) and
	// it is a claim about the file that somebody checked by reading it, not a shrug.
	reason string
}

// allowlisted records why a test file may hold a fixed calendar date beside a real clock without
// being a time bomb.
//
// The bar for an entry: the fixed date and that clock must never be COMPARED to one another, either
// they live in different tests, or the clock is used for something that is not a date (a unique name,
// a cleanup window, a stopwatch), or the comparison's outcome cannot change with the calendar.
//
// Keys are paths relative to the backend module root, slash-separated.
var allowlisted = map[string]exemption{
	// -- real clock is used for UNIQUENESS, never as a date --------------------------------------
	"internal/quote/qd1_ownership_integration_test.go": {[]string{"time.Now()"},
		"time.Now().UnixNano() only mints unique row tags; every date compared is the test's own fixed `now`, passed explicitly (the edit_integration_test.go pattern)."},
	"internal/quote/edit_integration_test.go": {[]string{"time.Now()"},
		"time.Now().UnixNano() only mints unique row tags; every date compared is the test's own fixed `now`, passed explicitly."},
	"internal/quote/dib491_acceptcontrols_integration_test.go": {[]string{"time.Now()"},
		"time.Now().UnixNano() only mints unique invoice/order tags (the edit_integration_test.go pattern); every date compared is the test's own fixed 2026-08-20 `now`, passed explicitly."},
	"internal/warranty/integration_w6c_test.go": {[]string{"time.Now()"},
		"time.Now().UnixNano() only mints unique claim numbers and idempotency keys; the money/phase math runs at the test's fixed `now`."},

	// -- real clock and fixed date live in DIFFERENT tests in the same file ----------------------
	"internal/reporting/render_test.go": {[]string{"time.Now()"},
		"the fixed RunAt belongs to sampleResult(); the two time.Now() RunAt values are in the XSS-escaping and commission-shape tests, neither of which compares a date."},
	"internal/jobs/runner_test.go": {[]string{"time.Now()"},
		"the fixed dates are nextNightly's pure arithmetic fixtures; the time.Now()-relative values are in separate scheduler-claim tests and are self-relative (now±duration)."},
	"internal/messaging/retention_test.go": {[]string{"time.Now()"},
		"the fixed `now` values are passed explicitly to retentionCutoff/ArchiveOnce; the one ArchiveOnce(ctx, time.Now()) is the policy-load-error test, which asserts 0 archived regardless of clock."},
	"internal/pricing/findings_test.go": {[]string{"time.Now()"},
		"withinValidity(out, time.Now()) runs on a rule whose validity bounds were just CLEARED to nil (always-valid); the fixed 2026-06-29 dates are in a separate bare-date-parsing test."},
	"internal/pricing/service_test.go": {[]string{"time.Now()"},
		"the fake store's CreatedAt: time.Now() is an audit stamp nothing reads; the fixed valid-from date is in a separate rule-validity test."},
	"internal/messaging/rollup_test.go": {[]string{"time.Now()"},
		"the fixed 2026-08-12 instant is rollupDay()'s pure zone-conversion test (fixed in, fixed string out, no clock); the time.Now() uses are in different tests and compare nothing to it, the two-days test is self-relative (now vs now-26h, a gap wider than any Toronto day incl. DST, so it can never collapse to one day) and PostRoutineEvent derives the day ONLY from its `at` argument, and the never-says-today test feeds rollupDay(time.Now()) back in as a string it only asserts containment of."},

	// -- the comparison exists but its outcome cannot change with the calendar -------------------
	"internal/pricing/batch_service_test.go": {[]string{"time.Now()"},
		"ApplyBatch(…, time.Now()) is asserted against an effective date of 2026-01-01, already past and monotonically staying past, so the gate's answer can never flip; every other asOf is passed explicitly."},
	"internal/messagingprovider/recipients_db_integration_test.go": {[]string{"time.Now()"},
		"MaySendAt(…, time.Now()) is asserted on an OPTED-OUT customer, where the answer is 'never' before any clock is read; the fixed 08:30 instant is passed explicitly in a different test."},
	"internal/gl/store_fake_test.go": {[]string{"time.Now()"},
		"the fake store stamps PostedAt with time.Now(), and no test in the package reads PostedAt; the fixed 2026-06-15 is the injected service clock."},

	// -- SENTINEL YEARS: the fixed date is deliberately a year no real row can ever carry, so the
	//    gap to the real clock is decades wide and widens. This is the suite's OTHER correct
	//    pattern (isolate by choosing an impossible year) and these files are its worked examples.
	"internal/billing/repository_integration_test.go": {[]string{"time.Now()"},
		"asOf 1990-06-30 is chosen precisely so it selects ONLY the rows this test back-dates into 1990 and excludes every recently-dated invoice, a far-PAST sentinel that gets safer with time; time.Now() is ns(), a unique namespace suffix."},
	"internal/finstmt/integration_test.go": {[]string{"time.Now()"},
		"the 9999-12-31 asOf is an explicit 'end of time' cutoff that makes the subledger tie exact (the file says why a now()-based cutoff is unreliable here); time.Now() is a perf-smoke stopwatch, not a date."},
	"internal/gl/integration_test.go": {[]string{"time.Now()"},
		"fiscal years 2095-2098 are far-future sentinels so these journals never collide with real-dated periods; every time.Now() is either a UnixNano idempotency key or a service clock whose only output is an OccurredAt audit stamp no assertion reads."},
	"internal/gl/handler_sourcerefid_live_integration_test.go": {[]string{"time.Now()"},
		"fiscal year 2099 sentinel period; time.Now().UnixNano() only mints the idempotency key. The assertions are about the source_ref_id filter gate, not about any date."},
	"internal/gl/list_journals_source_ref_integration_test.go": {[]string{"time.Now()"},
		"fiscal year 2097 sentinel period; time.Now().UnixNano() only mints unique idempotency keys. The assertions are about source_ref_id filtering, not about any date."},
	"internal/order/cursor_test.go": {[]string{"time.Now()"},
		"the 1990-03-15 stamp is a far-PAST sentinel so the same-timestamp order fixtures never collide with a current row; right after, time.Now().UnixNano() only mints the ordcur-... namespace suffix (cursor leg, order + quote sibling sites, rows 804 and 939)."},
	"internal/quote/cursor_test.go": {[]string{"time.Now()"},
		"the 1990-03-15 stamp is a far-PAST sentinel so the same-timestamp quote fixtures never collide with a current row; right after, time.Now().UnixNano() only mints the qtecur-... namespace suffix (cursor leg, order + quote sibling sites, rows 804 and 939)."},

	// -- SQL now() lands only in audit columns that nothing reads back ---------------------------
	"internal/bankrec/integration_test.go": {[]string{"SQL now()"},
		"SQL now() writes only payments.updated_at and gl_journals.posted_at, neither asserted; every matched date (statement TxnDate vs CandidatePayment.ReceivedAt, compared by withinDays) is fixed on BOTH sides."},

	// -- both sides are the real clock, self-consistent, no fixed instant in the comparison -----
	"internal/pricing/w4_integration_test.go": {[]string{"SQL now()", "time.Now()"},
		"the rebate/promo windows are now()±interval and the service under test is UNPINNED (real clock), so both sides move together; the fixed June-2026 promo window is only ever queried with an explicit `at`."},

	// -- the house pattern's own file, and this ticket's conversions -----------------------------
	"internal/messagingprovider/collections_integration_test.go": {[]string{"SQL now()", "time.Now()"},
		"the reference defuse: businessHours() is the single source feeding BOTH the invoice-age fixture and the sweep's WithClock; the SQL now() is only consent/verified_at, never an age, see that file's comment block."},
	"internal/warranty/integration_test.go": {[]string{"time.Now()"},
		"converted (DIB-427): fixture dates are parameterised on intClock(). The one time.Now() is the cleanup window over created_at, a real column that must stay real. NOTE the narrow excuse, a SQL now()/CURRENT_DATE appearing here again is a re-armed bomb and WILL trip this audit."},
}

// realClockUse is one place a test reads a clock that moves.
type realClockUse struct {
	line int
	what string
}

// fixedDate is one pinned calendar instant.
type fixedDate struct {
	line int
	text string
}

// sqlClockTokens are the Postgres spellings of "right now", matched ONLY inside string literals so a
// Go method named now() and the word now() in a comment are not mistaken for them.
var sqlClockTokens = []string{"CURRENT_DATE", "CURRENT_TIMESTAMP", "current_date", "current_timestamp", "now()", "NOW()"}

// sqlKeywords gate the clock tokens above: a literal must look like SQL before "now()" in it counts
// as the database's clock.
//
// Without this gate the audit fires on FAILURE MESSAGES, warranty/service_test.go says
// `"closed_at must be the back-dated 2026-06-04, not now(); got %s"`, which contains the characters
// "now()" and no SQL whatsoever. That is exactly the kind of false positive that teaches a team to
// add blanket allowlist entries, at which point the guard is decoration. A clock token now has to sit
// in a literal that is actually a query.
var sqlKeywords = []string{"SELECT", "INSERT", "UPDATE ", "DELETE", "WHERE", "VALUES", "FROM ", "SET ", "INTERVAL"}

// looksLikeSQL reports whether a string literal is plausibly a query rather than prose.
func looksLikeSQL(lit string) bool {
	up := strings.ToUpper(lit)
	for _, kw := range sqlKeywords {
		if strings.Contains(up, kw) {
			return true
		}
	}
	return false
}

// suspect is one test file carrying both ingredients.
type suspect struct {
	path   string
	dates  []fixedDate
	clocks []realClockUse
}

// scanSuite walks every *_test.go in the module and returns the files carrying BOTH a pinned calendar
// instant and a real clock, allowlist not yet applied. Both tests below run off this one scan, so
// the tripwire and the allowlist-honesty check can never disagree about what the suite contains.
func scanSuite(t *testing.T, root string) []suspect {
	t.Helper()
	var found []suspect

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "node_modules" || name == "testdata" || name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)

		fset := token.NewFileSet()
		// ParseComments is NOT set: comments must not be able to trip or silence the audit.
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("the audit could not parse %s, a test file that does not parse cannot be audited: %v", rel, perr)
		}

		var dates []fixedDate
		var clocks []realClockUse

		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				sel, ok := node.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "time" {
					return true
				}
				switch sel.Sel.Name {
				case "Now":
					// time.Now(), the Go wall clock. A SELECTOR on a value (c.now(), s.now())
					// never reaches here, which is exactly why this is an AST walk.
					clocks = append(clocks, realClockUse{line: fset.Position(node.Pos()).Line, what: "time.Now()"})
				case "Date":
					// time.Date(YYYY, …) with a literal year is a pinned calendar instant. A
					// computed year (time.Date(base.Year()+1, …)) is derived, not pinned.
					if len(node.Args) > 0 {
						if lit, ok := node.Args[0].(*ast.BasicLit); ok && lit.Kind == token.INT {
							dates = append(dates, fixedDate{line: fset.Position(node.Pos()).Line, text: "time.Date(" + lit.Value + ", …)"})
						}
					}
				}
			case *ast.BasicLit:
				if node.Kind != token.STRING || !looksLikeSQL(node.Value) {
					return true
				}
				for _, tok := range sqlClockTokens {
					if strings.Contains(node.Value, tok) {
						// Canonical label, so "NOW()" and "now()" are one kind in the allowlist.
						clocks = append(clocks, realClockUse{line: fset.Position(node.Pos()).Line, what: "SQL " + canonicalSQLToken(tok)})
					}
				}
			}
			return true
		})

		if len(dates) > 0 && len(clocks) > 0 {
			found = append(found, suspect{path: rel, dates: dates, clocks: clocks})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the test tree: %v", err)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].path < found[j].path })
	return found
}

// TestSuiteHasNoDatedTimeBombs is the tripwire. It fails when a *_test.go file holds BOTH a pinned
// calendar instant and a real clock, unless that file is signed off in allowlisted above.
func TestSuiteHasNoDatedTimeBombs(t *testing.T) {
	var offences []suspect
	for _, s := range scanSuite(t, moduleRoot(t)) {
		ex, ok := allowlisted[s.path]
		if !ok {
			offences = append(offences, s)
			continue
		}
		// The file is signed off, but only for the clock kinds its entry names. Anything else is a
		// bomb that has been dropped into an already-excused file, which is precisely how this class
		// re-arms without anybody noticing.
		var unexcused []realClockUse
		for _, c := range s.clocks {
			if !contains(ex.excuses, c.what) {
				unexcused = append(unexcused, c)
			}
		}
		if len(unexcused) > 0 {
			offences = append(offences, suspect{path: s.path + "  [ALLOWLISTED, but for " +
				strings.Join(ex.excuses, "+") + " only, this is a NEW kind of clock in an excused file]",
				dates: s.dates, clocks: unexcused})
		}
	}

	if len(offences) == 0 {
		return
	}

	var b strings.Builder
	b.WriteString("DATED TIME BOMB, a fixed calendar date can meet a real clock in these test files.\n\n")
	b.WriteString("A test that pins an instant and compares it against time.Now() or SQL now() passes only\n")
	b.WriteString("until a certain date, and then fails for nobody's commit, or silently stops asserting.\n")
	b.WriteString("This is DIB-427 / W6D-7. Two ways out, in order of preference:\n\n")
	b.WriteString("  1. FIX IT. Make the fixture and the code under test read ONE clock: inject the pinned\n")
	b.WriteString("     clock into the fixture, or pass the SQL an explicit as-of parameter instead of now().\n")
	b.WriteString("     The worked example is internal/messagingprovider/collections_integration_test.go.\n")
	b.WriteString("  2. SIGN IT OFF. If the two genuinely never meet, add the file to `allowlisted` in\n")
	b.WriteString("     internal/testaudit/dated_tests_test.go WITH A ONE-LINE REASON that says why.\n\n")
	b.WriteString("Offending files:\n")
	for _, o := range offences {
		b.WriteString("\n  " + o.path + "\n")
		b.WriteString("      fixed dates:  " + describe(o.dates, o.clocks, true) + "\n")
		b.WriteString("      real clocks:  " + describe(o.dates, o.clocks, false) + "\n")
	}
	t.Fatal(b.String())
}

// describe renders up to five sites of one kind as "line:what", so the failure names the exact lines
// a reader has to look at rather than making them re-run a grep.
func describe(dates []fixedDate, clocks []realClockUse, wantDates bool) string {
	var parts []string
	if wantDates {
		for _, d := range dates {
			parts = append(parts, itoa(d.line)+": "+d.text)
		}
	} else {
		for _, c := range clocks {
			parts = append(parts, itoa(c.line)+": "+c.what)
		}
	}
	total := len(parts)
	if total > 5 {
		parts = parts[:5]
	}
	out := strings.Join(parts, ", ")
	if total > 5 {
		out += ", … (" + itoa(total-5) + " more)"
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestAllowlistIsHonest keeps the allowlist from rotting into a junk drawer. An exemption list that
// nobody prunes is how a guard stops guarding: entries outlive the code they excused, and the next
// person to put a time bomb in one of those files is waved straight through. So every entry must
//
//   - carry a justification (an unexplained exemption is indistinguishable from a bug);
//   - name a file that still exists; and
//   - name a file that STILL carries both ingredients, once a file is cleaned up, its exemption is
//     stale and must be deleted so the file goes back under guard.
func TestAllowlistIsHonest(t *testing.T) {
	root := moduleRoot(t)

	stillSuspect := map[string]bool{}
	for _, s := range scanSuite(t, root) {
		stillSuspect[s.path] = true
	}

	var stale []string
	for rel, ex := range allowlisted {
		if len(ex.excuses) == 0 {
			t.Errorf("allowlist entry %q excuses no clock kind, it exempts nothing and should be deleted", rel)
			continue
		}
		if strings.TrimSpace(ex.reason) == "" {
			t.Errorf("allowlist entry %q has no justification, an unexplained exemption is indistinguishable from a bug", rel)
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("allowlist entry %q names a file that no longer exists, delete the entry: %v", rel, err)
			continue
		}
		if !stillSuspect[rel] {
			stale = append(stale, rel)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("these allowlist entries are STALE, the files no longer hold a fixed date beside a real\n"+
			"clock, so the exemption excuses nothing and only hides the next one. Delete them from\n"+
			"`allowlisted` in internal/testaudit/dated_tests_test.go:\n  %s", strings.Join(stale, "\n  "))
	}
}

// moduleRoot walks up from the test's working directory to the backend module root (the directory
// holding go.mod), so the audit covers the WHOLE module however it is invoked.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s, the audit cannot find the module root", dir)
		}
		dir = parent
	}
}

// canonicalSQLToken folds the case variants of a Postgres clock spelling into one label, so an
// allowlist entry does not have to enumerate now() and NOW() separately.
func canonicalSQLToken(tok string) string {
	switch strings.ToLower(tok) {
	case "now()":
		return "now()"
	case "current_date":
		return "CURRENT_DATE"
	case "current_timestamp":
		return "CURRENT_TIMESTAMP"
	}
	return tok
}

// contains reports whether xs holds s.
func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
