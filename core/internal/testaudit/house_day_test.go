package testaudit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// house_day_test.go, WHERE PRODUCTION CODE STILL DECIDES THE DAY AT GREENWICH.
//
// -- THE BUG CLASS -------------------------------------------------------------------------------
//
// A business day is a fact about a YARD, not about a prime meridian. Dibbits sells out of Ontario,
// where UTC midnight falls at 8pm, so any code that answers "which day is this?" in UTC is right
// for twenty hours and wrong for four, every single day, and wrong in a way that produces a
// plausible number rather than an error. A sale rung at 21:15 is filed under tomorrow. A quote valid
// "until the 5th" stops being acceptable at 8pm on the 5th, with the customer standing at the
// counter holding it. A "sales today" tile drops the evening it is named after. Nothing fails,
// nothing warns, and the only way to notice is to be looking at 8:30 at night.
//
// The house has ruled on this five times and written the argument down each time -
// messaging/rollup.go:46-49, inventory/transfer_eta.go, messagingprovider/policy.go,
// gl/businessday.go:5-52 and dashboard/window.go:26-32, the last two of which exist ONLY to close
// this class in one module each. What has never existed is anything that asks how many places still
// have it. Every one of those five was found by hand, by somebody who happened to be looking at the
// right file. This file is that question, asked mechanically.
//
// It was written alongside the sixth: pos/sale_lookup.go's past-sale search compared a TIMESTAMPTZ
// column to `$N::date`, which Postgres promotes at the SESSION zone, and platform/db/db.go:14 is a
// bare pgxpool.New that sets none. Both of its bounds were off by four hours in OPPOSITE
// directions, from= swept in last night's closing sales, to= dropped this evening's, which is why
// two readers and a comment that stated the correct behaviour all failed to catch it.
//
// -- WHY THIS PACKAGE AND NOT internal/gate ------------------------------------------------------
//
// internal/gate/ is the obvious-looking home and it is the wrong one: every file in it carries
// `//go:build integration`, and its own doc.go says the suite is "intentionally NOT in CI". A
// tripwire nobody runs is a comment. testaudit is untagged, so .github/workflows/ci.yml's plain
// `go test ./...` runs it on every push, which is the only property that makes a gate a gate.
//
// -- WHY AN AST WALK, AND WHY COMMENTS MUST NOT BE READ ------------------------------------------
//
// This is the same ruling dated_tests_test.go:21-25 made for the audit next door, and it holds twice
// over here, because the files that DISCUSS this defect at length are the files that fixed it.
// messaging/payments_today.go:17-22 explains, in prose, why it does not write
// `WHERE received_at::date = today`. gl/handler.go:88 names the `$N::date` cast it is warning about.
// dashboard/window.go:28 quotes the exact broken expression it replaced. The adapter lives at cmd/core/tops_adapters.go (the fold's re-home of the pin's cmd/server/main.go EOF block)
// discusses the zone. A grep would flag all four, the documentation, not the defect, and the only
// way to make such a gate green is to delete the sentences. cmd/demoseed/source_test.go:19-22 puts
// it plainly: "a scan that could not tell the explanation from the defect would either fail on the
// documentation or force the documentation to stop saying the thing it exists to say."
//
// So the parser runs WITHOUT ParseComments. That is the comment strip, and it is load-bearing: the
// four files above are audited by this test and none of them is reported.
//
// -- THE FOUR SHAPES, AND WHAT EACH ONE IS NOT ---------------------------------------------------
//
//	UTC day truncation      `.Truncate(24 * time.Hour)`, with or without a `.UTC()` in front, the
//	                        method truncates absolute time since the epoch, so it is a Greenwich day
//	                        whether or not anybody wrote UTC. A Truncate to any OTHER duration is a
//	                        rate-limit bucket or a rounding, not a day, and is not reported:
//	                        warranty/intake.go:215 truncates to a configurable window and is right.
//
//	UTC day literal         `.UTC().Format("2006-01-02")`, turning an instant into the day text a
//	                        query or a key will compare. A BARE `.Format("2006-01-02")` is NOT
//	                        reported, deliberately: there are thirty-odd of them and nearly all are
//	                        rendering a value whose day some other line already decided, a filename,
//	                        a due date read back off a DATE column, a label. The `.UTC()` is what
//	                        turns a rendering into a DECISION, and the decision is what this audits.
//
//	Server-zone day         `time.Now().Format("2006-01-02")`, the clock read straight into day text
//	literal                 with no zone chosen: the day is whatever zone the process holds (a UTC
//	                        container, a developer laptop), which is the original shape of the tax
//	                        engine's "as of today" defect. It is reported only when the receiver IS
//	                        the call to time.Now, so a rendering of some value whose day another
//	                        line decided is never reported; a clock first stored in a variable is
//	                        beyond a parser, and `houseday.Today` is the way to write it.
//
//	UTC calendar            `time.Date(…, time.UTC)`. No attempt is made to tell an epoch from a day
//	construction            bound, because that needs to know what the value is FOR, which is a
//	                        human's knowledge and not a parser's. So the ZIP epoch, the Excel 1899
//	                        serial origin and the fiscal sentinels are all reported, and each carries
//	                        a signature below saying why it is not a day. That is the honest trade:
//	                        a short list of signed constants beats a clever rule that guesses.
//
//	SQL day boundary        A `::date` cast or CURRENT_DATE meeting a column this schema declares
//	                        TIMESTAMPTZ. THE COLUMN LIST IS READ FROM backend/migrations AT RUN TIME,
//	                        which is the whole reason this half of the audit is usable: `$1::date`
//	                        against a DATE column (issue_date, due_date, scheduled_date,
//	                        effective_date, period_start, dozens of them) is exact, zone-free and
//	                        perfectly correct, and a scan that reported those would drown the six
//	                        real ones in forty false alarms. The schema already knows which columns
//	                        carry an instant. It is asked rather than guessed.
//
// -- WHAT IT CANNOT KNOW -------------------------------------------------------------------------
//
// It cannot tell a defect from a deliberate choice, and it does not try. `time.Date(1980, 1, 1, …,
// time.UTC)` is a ZIP epoch and `time.Date(u.Year(), u.Month(), u.Day(), …, time.UTC)` is a day
// boundary, and the difference is intent. So this is deliberately conservative in the same way its
// neighbour is: it reports the shape, and a human signs each surviving instance off in
// `dayAllowlisted` with a reason. An allowlist entry is a claim somebody made and can be held to.
// Silence is not.
//
// The SQL half also cannot see across a fmt.Sprintf: a query assembled from fragments is only
// scanned fragment by fragment. Every query in this tree is a single literal today, which is why
// that is a footnote and not a hole.
//
// -- THE ALLOWLIST IS ALSO THE BACKLOG -----------------------------------------------------------
//
// Most of the entries below are not exemptions. They are REAL INSTANCES of the pos defect, verified
// one by one, deferred on purpose, a wave that changed the day boundary in twenty packages at once
// could not be reviewed by anybody, and several of them need a product ruling and not just a zone.
// Each says which four hours it gets wrong and what it does to somebody. When one is fixed its entry
// goes stale and TestHouseDayAllowlistIsHonest fails until it is deleted, which is how the file
// returns to being guarded and how the work gets recorded as done.

const (
	kindTruncate = "UTC day truncation"
	kindDayText  = "UTC day literal"
	kindCalendar = "UTC calendar construction"
	kindSQLDay   = "SQL day boundary on a timestamptz column"
	kindBareNow  = "server-zone day literal"
)

type dayExemption struct {
	excuses []string
	reason  string
}

var dayAllowlisted = map[string]dayExemption{
	// -- LEGITIMATE · both sides of the comparison share one basis -------------------------------
	"internal/bankrec/matcher.go": {[]string{kindTruncate},
		"withinDays truncates BOTH instants the same way and compares the DISTANCE between them; a " +
			"shift applied identically to a and b cancels, and the function never asks which day it is."},

	// -- LEGITIMATE · an epoch or a sentinel, not a business day ---------------------------------
	"internal/export/xlsx.go": {[]string{kindCalendar},
		"zipModified is 1980-01-01, the earliest instant the ZIP format can represent, stamped on every " +
			"entry so two exports of the same data are byte-identical. It is a format constant; there is " +
			"no yard and no day in it."},
	"internal/export/workbook.go": {[]string{kindCalendar},
		"excelEpoch is 1899-12-30, Lotus 1-2-3's leap-year bug, which Excel reproduces and a serial " +
			"date must be anchored to. dateSerial's own time.Date reads y/m/d in the value's OWN " +
			"location first (its comment says why: a value stored -04:00 must export as its own date), " +
			"so UTC there is only the arithmetic frame the subtraction happens in."},
	"internal/gl/bistrack_tieout.go": {[]string{kindCalendar},
		"periodEnd is a FISCAL period boundary, not a calendar day: the ledger's month is already " +
			"resolved in the house zone by gl/businessday.go, and this builds the as-of instant for an " +
			"inception-to-date balance from that (year, month) pair. Moving it to a local midnight would " +
			"make the tie-out disagree with the periods the journals were filed into."},
	"internal/gl/close.go": {[]string{kindCalendar},
		"the year-end closing journal is stamped 31 December at NOON UTC on purpose, a sentinel far " +
			"from either midnight so no zone can move it into an adjacent day, and the fiscal period it " +
			"posts to is the year it was asked for, not the day the instant lands on."},
	"internal/bankrec/handler.go": {[]string{kindCalendar},
		"parseWindow defaults `from` to 2000-01-01 as an OPEN LOWER BOUND, a far-past sentinel that " +
			"means 'no from was given', and gets safer with every year. No statement predates it and no " +
			"answer changes if it moves four hours."},

	// -- LEGITIMATE · the UTC day boundary is the object under test ------------------------------
	"internal/platform/houseday/houseday.go": {[]string{kindCalendar},
		"JustAfterUTCDayChange builds half past UTC midnight on a named UTC day on purpose: it is the " +
			"pinned test instant at which the UTC day and the house zone's day DISAGREE, so date code " +
			"under test can be proven to follow the dealer's day rather than Greenwich's. It answers no " +
			"business date itself; every day this package names comes from Location and dayText."},

	// -- LEGITIMATE · the day text is a canonical key, never a day boundary ----------------------
	"internal/bankrec/statement_source.go": {[]string{kindDayText},
		"DedupeHash canonicalises (date, amount, description, reference) into a UNIQUE index key. Both " +
			"the original import and the re-import format the same txnDate the same way, so the hash is " +
			"stable; nothing compares this string to a day, and moving it to the house zone would " +
			"invalidate every hash already stored."},

	// -- DEFERRED · REAL SIBLINGS OF THE POS DEFECT. Not fixed in this wave. ---------------------
	//
	// Each of these decides WHICH DAY IT IS at Greenwich, and each is therefore four hours wrong for
	// the last four hours of every Ontario day. They are listed rather than fixed because a wave that
	// touched twenty packages could not be reviewed, and because several need a product ruling and
	// not just a zone (see gl/repository.go and reporting/catalog.go below). The gate's job is to make
	// sure they cannot be forgotten: delete an entry when the file is fixed and it goes back under
	// guard, and TestHouseDayAllowlistIsHonest fails if the entry outlives the defect.

	// ---- the counter's own twin, in the cockpit adapter ----
	"cmd/core/tops_adapters.go": {[]string{kindSQLDay},
		"DEFERRED · HIGHEST VALUE. w6aSalesToday is HALF fixed and that is the worst state: w6aDay " +
			"(main.go:3570) already resolves the day correctly with dashboard.HouseDay, then hands the " +
			"right day text to `s.created_at >= $2::date`, which UTC-promotes it straight back. The " +
			"'sales today' tile is therefore LABELLED 5 August and COUNTS 4 Aug 20:00 → 5 Aug 20:00: it " +
			"drops the evening it is describing and adds the one before. This is pos/sale_lookup.go's " +
			"defect exactly, on the surface dashboard/window.go was written to fix. Fix it the same way " +
			"- bind an instant, not a date."},

	// ---- SQL day boundaries taken against a timestamptz column ----
	"internal/tax/repository.go": {[]string{kindSQLDay},
		"DEFERRED · ListW4Snapshots is sale_lookup.go's shape line for line, down to the `+ INTERVAL " +
			"'1 day'`: created_at is TIMESTAMPTZ and both bounds promote at the session zone. A tax " +
			"snapshot listing is an audit read, so the wrong four hours is a wrong evidence set."},
	"internal/gl/repository.go": {[]string{kindSQLDay},
		"DEFERRED · ListJournals compares `posted_at::date` to `$N::date` (DIB-204's range predicate, " +
			"which gl/handler.go:88 already discusses in prose). NEEDS A RULING BEFORE A FIX: the " +
			"ledger's own day is the fiscal one gl/businessday.go resolves, and posted_at is a wall " +
			"instant, deciding which of the two a journal FILTER means is a product question, not a " +
			"zone substitution."},
	"internal/reporting/catalog.go": {[]string{kindSQLDay},
		"DEFERRED · nine day-bucketed reports (statement runs, stock moves, receipts, holds, warranty) " +
			"both FILTER on `created_at >= $1::date` and GROUP BY `created_at::date`, so an evening's " +
			"rows are filed under tomorrow in the bucket AND may fall outside the range that was asked " +
			"for. Fixing the filter without the bucket, or either without the other, would make a " +
			"report whose total does not equal its rows, so this file moves as one piece or not at all."},

	// ---- Go-side day boundaries ----
	"internal/dispatch/service.go": {[]string{kindDayText},
		"DEFERRED · two defaults for 'today' (the branch board, and the driver's day in MyRuns). WAVE 9 " +
			"CARES ABOUT THE SECOND ONE: after 8pm a driver opening MyRuns is shown TOMORROW's runs and " +
			"told the evening's remaining stops do not exist."},
	"internal/dispatch/service_loadboard.go": {[]string{kindDayText},
		"DEFERRED · the load board and the readiness badge both default `date` to the UTC day, so the " +
			"board a dispatcher opens at 8:30pm is tomorrow's, empty, with today's outstanding loads " +
			"nowhere on it."},
	"internal/dispatch/service_w3.go": {[]string{kindDayText},
		"DEFERRED · the reschedule guard refuses a date earlier than the UTC 'today', so after 8pm it " +
			"rejects moving a delivery to LATER THE SAME DAY with 'that date has already passed', a " +
			"refusal the dispatcher cannot argue with and cannot work around."},
	"internal/dispatch/syncscope_board.go": {[]string{kindDayText},
		"DEFERRED · the delivery_stops sync adapter mirrors service_w3.go's reschedule guard verbatim " +
			"(the same sentence, the same UTC 'today'), so the device's refusal and the desk's refusal " +
			"disagree with the yard's evening in exactly the same way; when the module's guard takes " +
			"the dealer's day, the adapter takes it with it."},
	"internal/dispatch/handler_loadboard.go": {[]string{kindTruncate},
		"DEFERRED · defaultCalendarRange starts the calendar week on the UTC day, so the week the " +
			"calendar opens on rolls forward at 8pm."},
	"internal/dispatch/loadboard.go": {[]string{kindTruncate},
		"DEFERRED · the schedulable-window check (yesterday .. N weeks out) is anchored on the UTC day, " +
			"so for four hours each evening the window is one day ahead of the yard's."},
	"internal/dispatch/picksource.go": {[]string{kindTruncate},
		"DEFERRED · priorityFor compares a scheduled DATE against the UTC day, so after 8pm every " +
			"pick scheduled for the current day is re-labelled URGENT (past) instead of HIGH (today), " +
			"and tomorrow's becomes HIGH. The yard's priority colours change at 8pm for nobody."},
	"internal/tasks/yard_service.go": {[]string{kindDayText},
		"DEFERRED · the stale-pick nudge is deduped on a UTC day key, so the once-a-day notice fires a " +
			"second time at 8pm when the key rolls while the yard is still open."},
	"internal/inventory/reorder_actions.go": {[]string{kindDayText},
		"DEFERRED · NudgeEventKey is a once-a-day idempotency key on the UTC day; its own comment says " +
			"'tomorrow it can be raised again, which is exactly right', and at 8pm tomorrow arrives " +
			"early and the nudge repeats."},
	"internal/compliance/store.go": {[]string{kindTruncate},
		"DEFERRED · filterUpcoming truncates asOf at Greenwich to decide OVERDUE and the horizon " +
			"cutoff, so a deadline due today is stamped overdue from 8pm the evening before."},
	"internal/assist/commands_domain.go": {[]string{kindTruncate},
		"DEFERRED · the cash-receipts report decides whether its range reaches 'today' against the UTC " +
			"day, so after 8pm it stops printing the note that tells the reader why its figure and the " +
			"payments rollup's differ, on the very evening they differ most. The rollup one module " +
			"over (messaging/rollup.go:50) already files its day in the house zone; this is the reader " +
			"of that rollup disagreeing with it."},
	"internal/pricing/batch_service.go": {[]string{kindCalendar},
		"DEFERRED · dayStart pins an effective_date comparison to UTC midnight, so a price batch " +
			"effective 'today' goes live at 8pm the night before."},
	"internal/quote/edit.go": {[]string{kindCalendar},
		"DEFERRED · quoteExpired ends a quote's validity at the next UTC midnight, so a quote valid " +
			"until 5 August stops being acceptable at 8pm on the 5th, while the counter is open and " +
			"the customer is standing there holding it."},
	"internal/quote/reserve_repository.go": {[]string{kindCalendar},
		"DEFERRED · a hold's end is built as 23:59:59 UTC on the valid-until date, which is BOTH " +
			"defects at once: the wrong zone (19:59:59 in the yard) and the inclusive-end shape " +
			"dashboard/window.go:46-53 ruled against, which loses the final second of the day. It wants " +
			"the half-open next-midnight form."},
	"internal/jobs/jobs.go": {[]string{kindCalendar},
		"DEFERRED · nextNightly schedules on minutes past UTC midnight, and its doc says so, so this is " +
			"a DELIBERATE convention rather than an oversight, but it means the 'nightly' jobs run at " +
			"8pm Ontario, during business. Whether a nightly job should run on the yard's night is a " +
			"product decision; it is recorded here so it is made rather than inherited."},
	"cmd/core/demoseed_build.go": {[]string{kindCalendar},
		"DEFERRED · builder.day() derives demo DATE literals from the injected b.in.Now at UTC " +
			"midnight. It is seed data and no customer sees it, but a seeded 'today' that is not the " +
			"house's makes a demo taken after 8pm show tomorrow's board."},
}

type dayHit struct {
	line int
	kind string
	text string
}

type dayOffender struct {
	path string
	hits []dayHit
}

// -- the shapes -----------------------------------------------------------------------------------

// isTimeUTC reports whether an expression is the identifier `time.UTC`.
func isTimeUTC(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "UTC" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "time"
}

// isUTCCall reports whether an expression is a call to `.UTC()` on something.
func isUTCCall(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "UTC"
}

// isTimeNowCall reports whether an expression is the call `time.Now()`.
func isTimeNowCall(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Now" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "time"
}

// isOneDay reports whether an expression is the duration `24 * time.Hour`, written either way round.
func isOneDay(e ast.Expr) bool {
	bin, ok := e.(*ast.BinaryExpr)
	if !ok || bin.Op != token.MUL {
		return false
	}
	twentyFour := func(x ast.Expr) bool {
		lit, ok := x.(*ast.BasicLit)
		return ok && lit.Kind == token.INT && lit.Value == "24"
	}
	hour := func(x ast.Expr) bool {
		sel, ok := x.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Hour" {
			return false
		}
		pkg, ok := sel.X.(*ast.Ident)
		return ok && pkg.Name == "time"
	}
	return (twentyFour(bin.X) && hour(bin.Y)) || (hour(bin.X) && twentyFour(bin.Y))
}

// -- the SQL shape --------------------------------------------------------------------------------

// timestamptzColumns reads the migrations and returns every column this schema declares TIMESTAMPTZ.
func timestamptzColumns(t *testing.T, root string) []string {
	t.Helper()
	dir := filepath.Join(root, "db", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s, this audit cannot know which columns carry an instant without the "+
			"migrations that declare them: %v", dir, err)
	}
	decl := regexp.MustCompile(`(?im)^\s*([a-z_][a-z0-9_]*)\s+timestamptz\b`)
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		b, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			t.Fatalf("reading migration %s: %v", e.Name(), rerr)
		}
		for _, m := range decl.FindAllStringSubmatch(string(b), -1) {
			seen[strings.ToLower(m[1])] = true
		}
	}
	if len(seen) < 50 {
		t.Fatalf("found only %d TIMESTAMPTZ columns in %s. This audit's SQL half is only as wide as "+
			"that list, and a list that suddenly shrank means the migrations moved or the declaration "+
			"spelling changed, not that the schema stopped storing instants.", len(seen), dir)
	}
	cols := make([]string, 0, len(seen))
	for c := range seen {
		cols = append(cols, c)
	}
	// Longest first, so `created_at` is never matched as the column `at`.
	sort.Slice(cols, func(i, j int) bool {
		if len(cols[i]) != len(cols[j]) {
			return len(cols[i]) > len(cols[j])
		}
		return cols[i] < cols[j]
	})
	return cols
}

// sqlDayRegexps builds the two patterns that catch a day boundary taken against an instant column.
func sqlDayRegexps(cols []string) (compare, cast *regexp.Regexp) {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = regexp.QuoteMeta(c)
	}
	// No trailing boundary is needed and RE2 has no lookahead to write one with: everything that may
	// follow the column in these patterns is whitespace, a cast colon or a comparison operator, none
	// of which can match a letter, so `at` cannot match the head of `attempts`. The leading class
	// handles the other direction, which is why `at` never matches inside `created_at`.
	col := `(?:[a-z_][a-z0-9_]*\.)?(?:` + strings.Join(quoted, "|") + `)`
	notCol := `(?:^|[^a-z0-9_.])`
	// `created_at >= $1::date`, `posted_at <= $2::date`, `occurred_at < ($3::date + 1)`.
	compare = regexp.MustCompile(`(?is)` + notCol + col + `\s*(?:::\s*date\b)?\s*(?:>=|<=|<>|!=|>|<|=)([^;]{0,60}?)(?:::\s*date\b|\bcurrent_date\b)`)
	// `created_at::date`, the column itself flattened to a civil day, for a bucket or a group-by.
	cast = regexp.MustCompile(`(?is)` + notCol + col + `\s*::\s*date\b`)
	return compare, cast
}

// splitsClause reports whether the text between a comparison and its date token crosses into another
// predicate, which would mean the two halves are not part of the same comparison at all.
var splitsClause = regexp.MustCompile(`(?is)\b(and|or)\b|,`)

// NO "does this literal look like SQL?" PRE-FILTER, AND THAT IS A CORRECTION, NOT AN OVERSIGHT.
//
// dated_tests_test.go:146-154 gates its clock tokens on a SQL-keyword check, because "now()" appears
// in ordinary English, in a failure message that says `closed_at must be the back-dated 2026-06-04,
// not now()`, and without the gate that audit cried wolf. The same gate was written here first, and
// a mutation test caught it letting THE DEFECT THIS FILE WAS WRITTEN FOR walk straight past:
// pos/sale_lookup.go composed its predicate as `add("ps.created_at >= $%d::date", q.From)`, a
// fragment with no SELECT, no WHERE and no FROM in it, joined into a query three statements later.
// A gate that misses the one instance that motivated it is decoration.
//
// It is safe to drop here because this audit's evidence is far stronger than a single token. To be
// reported, a literal must contain a `::date` cast or CURRENT_DATE, AND a comparison, AND a column
// name this schema actually declares TIMESTAMPTZ. English does not accidentally contain
// `ps.created_at >= $1::date`, and comments, where the prose lives, are never parsed at all.

// -- the scan -------------------------------------------------------------------------------------

func scanHouseDays(t *testing.T, root string) []dayOffender {
	t.Helper()
	compare, cast := sqlDayRegexps(timestamptzColumns(t, root))
	var found []dayOffender

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if n := d.Name(); n == "node_modules" || n == "testdata" || n == ".git" || n == "migrations" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)

		fset := token.NewFileSet()
		// ParseComments is NOT set. See the header: this is the comment strip, and it is load-bearing.
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Errorf("the audit could not parse %s, a production file that does not parse cannot be "+
				"audited, and this gate will not report clean on a tree it could not read: %v", rel, perr)
			return nil
		}

		var hits []dayHit
		at := func(p token.Pos) int { return fset.Position(p).Line }

		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				sel, ok := node.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "Truncate":
					if len(node.Args) == 1 && isOneDay(node.Args[0]) {
						hits = append(hits, dayHit{at(node.Pos()), kindTruncate, ".Truncate(24 * time.Hour)"})
					}
				case "Format":
					if len(node.Args) == 1 && isTimeNowCall(sel.X) {
						if lit, ok := node.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING && lit.Value == `"2006-01-02"` {
							hits = append(hits, dayHit{at(node.Pos()), kindBareNow, `time.Now().Format("2006-01-02")`})
						}
						return true
					}
					if len(node.Args) != 1 || !isUTCCall(sel.X) {
						return true
					}
					if lit, ok := node.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING && lit.Value == `"2006-01-02"` {
						hits = append(hits, dayHit{at(node.Pos()), kindDayText, `.UTC().Format("2006-01-02")`})
					}
				case "Date":
					pkg, ok := sel.X.(*ast.Ident)
					if !ok || pkg.Name != "time" || len(node.Args) == 0 {
						return true
					}
					if isTimeUTC(node.Args[len(node.Args)-1]) {
						hits = append(hits, dayHit{at(node.Pos()), kindCalendar, "time.Date(…, time.UTC)"})
					}
				}
			case *ast.BasicLit:
				if node.Kind != token.STRING {
					return true
				}
				lit, uerr := strconv.Unquote(node.Value)
				if uerr != nil {
					return true
				}
				low := strings.ToLower(lit)
				flagged := ""
				if m := cast.FindString(low); m != "" {
					flagged = strings.TrimSpace(m)
				} else {
					for _, m := range compare.FindAllStringSubmatch(low, -1) {
						if splitsClause.MatchString(m[1]) {
							continue
						}
						flagged = strings.Join(strings.Fields(m[0]), " ")
						break
					}
				}
				if flagged != "" {
					hits = append(hits, dayHit{at(node.Pos()), kindSQLDay, flagged})
				}
			}
			return true
		})

		if len(hits) > 0 {
			sort.Slice(hits, func(i, j int) bool { return hits[i].line < hits[j].line })
			found = append(found, dayOffender{path: rel, hits: hits})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the production tree: %v", err)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].path < found[j].path })
	return found
}

func TestProductionCodeHasNoUnsignedUTCDayBoundary(t *testing.T) {
	var offences []dayOffender
	for _, o := range scanHouseDays(t, moduleRoot(t)) {
		ex, ok := dayAllowlisted[o.path]
		if !ok {
			offences = append(offences, o)
			continue
		}
		var unexcused []dayHit
		for _, h := range o.hits {
			if !contains(ex.excuses, h.kind) {
				unexcused = append(unexcused, h)
			}
		}
		if len(unexcused) > 0 {
			offences = append(offences, dayOffender{
				path: o.path + "  [SIGNED OFF for " + strings.Join(ex.excuses, " + ") +
					" only, this is a NEW kind of day boundary in an already-excused file]",
				hits: unexcused})
		}
	}
	if len(offences) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("UTC DAY BOUNDARY, production code decides which day it is at Greenwich.\n\n")
	for _, o := range offences {
		b.WriteString("\n  " + o.path + "\n")
		for _, h := range o.hits {
			b.WriteString("      " + itoa(h.line) + ": " + h.kind + ", " + h.text + "\n")
		}
	}
	t.Fatal(b.String())
}

// TestHouseDayAllowlistIsHonest keeps the signatures above from rotting into a junk drawer, on
// exactly the terms dated_tests_test.go:346-353 sets for its own list: an exemption nobody prunes is
// how a guard stops guarding, because entries outlive the code they excused and the next defect
// dropped into one of those files is waved straight through.
//
// The staleness clause is the one that matters most HERE, and it is different in kind from the
// dated-test audit's. There, a stale entry means a test got tidied. Here, a stale entry means A
// DEFERRED DEFECT WAS FIXED, so the failure this produces is a good-news failure, and its message
// says so. Deleting the entry is how the file goes back under guard, and it is the only record that
// the work was finished.
func TestHouseDayAllowlistIsHonest(t *testing.T) {
	root := moduleRoot(t)

	stillHit := map[string]bool{}
	for _, o := range scanHouseDays(t, root) {
		stillHit[o.path] = true
	}

	knownKinds := []string{kindTruncate, kindDayText, kindCalendar, kindSQLDay, kindBareNow}
	var stale []string
	for rel, ex := range dayAllowlisted {
		if len(ex.excuses) == 0 {
			t.Errorf("allowlist entry %q excuses no kind of day boundary, it exempts nothing and "+
				"should be deleted", rel)
			continue
		}
		for _, k := range ex.excuses {
			if !contains(knownKinds, k) {
				t.Errorf("allowlist entry %q excuses %q, which is not a kind this audit reports. A "+
					"misspelled excuse silently excuses NOTHING, which is the one outcome worse than "+
					"no entry at all. The kinds are: %s", rel, k, strings.Join(knownKinds, ", "))
			}
		}
		if strings.TrimSpace(ex.reason) == "" {
			t.Errorf("allowlist entry %q has no justification. An unexplained exemption is "+
				"indistinguishable from a bug, and this list is the only place the deferred work is "+
				"written down, an entry with no reason has forgotten the thing it exists to remember",
				rel)
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("allowlist entry %q names a file that no longer exists, delete the entry: %v", rel, err)
			continue
		}
		if !stillHit[rel] {
			stale = append(stale, rel)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("GOOD NEWS, AND A CHORE. These files no longer take a day boundary at Greenwich, so "+
			"their entries in `dayAllowlisted` excuse nothing and now only serve to hide the NEXT one "+
			"that lands in the same file. Delete them from internal/testaudit/house_day_test.go:\n  %s",
			strings.Join(stale, "\n  "))
	}
}
