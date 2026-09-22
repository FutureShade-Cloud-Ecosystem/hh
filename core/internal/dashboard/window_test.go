package dashboard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A fixed instant to reason from: Wednesday 12 August 2026, 09:15 Toronto time.
func at(t *testing.T, s string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(houseZone)
	if err != nil {
		t.Skipf("no tzdata for %s on this machine: %v", houseZone, err)
	}
	parsed, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
	if err != nil {
		t.Fatalf("bad fixture %q: %v", s, err)
	}
	return parsed
}

func day(t *testing.T, w time.Time) string {
	t.Helper()
	loc, _ := time.LoadLocation(houseZone)
	return w.In(loc).Format("2006-01-02 15:04")
}

func TestResolveWindow_TheNamedWindowsAreTheHousesOwn(t *testing.T) {
	now := at(t, "2026-08-12 09:15") // a Wednesday
	for _, tc := range []struct{ named, since, until string }{
		// [midnight, next midnight), the bound does not move with the clock, or the same widget
		// means something different every time it refreshes.
		{RangeToday, "2026-08-12 00:00", "2026-08-13 00:00"},
		// THE WEEK STARTS MONDAY. Wednesday the 12th belongs to the week beginning Monday the 10th.
		{RangeWTD, "2026-08-10 00:00", "2026-08-13 00:00"},
		{RangeMTD, "2026-08-01 00:00", "2026-08-13 00:00"},
		// Q3 is July, so quarter-to-date starts on 1 July, not 1 August.
		{RangeQTD, "2026-07-01 00:00", "2026-08-13 00:00"},
		{RangeYTD, "2026-01-01 00:00", "2026-08-13 00:00"},
		// last_N is N calendar days INCLUDING today: seven days is the 6th to the 12th.
		{RangeLast7, "2026-08-06 00:00", "2026-08-13 00:00"},
		{RangeLast14, "2026-07-30 00:00", "2026-08-13 00:00"},
		{RangeLast30, "2026-07-14 00:00", "2026-08-13 00:00"},
		{RangeLast90, "2026-05-15 00:00", "2026-08-13 00:00"},
	} {
		w := ResolveWindow(tc.named, now)
		if w.IsZero() {
			t.Errorf("%s resolved to no window at all", tc.named)
			continue
		}
		if got := day(t, w.Since); got != tc.since {
			t.Errorf("%s since = %s, want %s", tc.named, got, tc.since)
		}
		if got := day(t, w.Until); got != tc.until {
			t.Errorf("%s until = %s, want %s", tc.named, got, tc.until)
		}
	}
}

func TestResolveWindow_MondayAndSundayAreBothInTheSameWeek(t *testing.T) {
	// The two ends of the Monday-first week, and the one Go's Sunday-first Weekday gets wrong.
	// Sunday the 16th belongs to the week that began Monday the 10th, not to a new one.
	if got := day(t, ResolveWindow(RangeWTD, at(t, "2026-08-10 00:01")).Since); got != "2026-08-10 00:00" {
		t.Errorf("Monday's week-to-date starts %s", got)
	}
	if got := day(t, ResolveWindow(RangeWTD, at(t, "2026-08-16 23:59")).Since); got != "2026-08-10 00:00" {
		t.Errorf("Sunday's week-to-date starts %s, Go counts Sunday as 0, and the yard's week does not", got)
	}
}

func TestResolveWindow_APopulationIsNotAPeriod(t *testing.T) {
	now := at(t, "2026-08-12 09:15")
	// These three name a population rather than a period, so they must produce NO bounds, a
	// provider tests IsZero rather than being handed a window covering all of history, which would
	// silently become a real predicate the first time somebody stored a date before it.
	for _, named := range []string{RangeOpen, RangeAllTime, RangeNow} {
		if w := ResolveWindow(named, now); !w.IsZero() {
			t.Errorf("%s resolved to %v..%v; it names a population, not a period", named, w.Since, w.Until)
		}
	}
	// And so does a name this build has never heard of: the safe arm is the behaviour that existed
	// before this file, not a window somebody guessed at.
	if w := ResolveWindow("last_millennium", now); !w.IsZero() {
		t.Errorf("an unknown window resolved to %v..%v", w.Since, w.Until)
	}
	// A zero clock resolves to nothing rather than to 1 January year 1.
	if w := ResolveWindow(RangeToday, time.Time{}); !w.IsZero() {
		t.Errorf("a zero clock produced %v..%v", w.Since, w.Until)
	}
}

// THE WHOLE POINT OF THE ZONE: the answer must not depend on how the server is configured. This is
// the same assertion gl/businessday_test.go makes about the ledger's fiscal period, for the same
// defect, one module over.
func TestResolveWindow_TheProcessTimezoneCannotChangeTheAnswer(t *testing.T) {
	// 11pm Toronto on 12 August is already the 13th in UTC. A window computed in the process's zone
	// would put this instant in the wrong day; a window computed in the house zone does not.
	evening := at(t, "2026-08-12 23:30")
	want := ResolveWindow(RangeToday, evening)

	for _, zone := range []string{"UTC", "Asia/Tokyo", "America/Los_Angeles"} {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Skipf("no tzdata for %s: %v", zone, err)
		}
		got := ResolveWindow(RangeToday, evening.In(loc))
		if !got.Since.Equal(want.Since) || !got.Until.Equal(want.Until) {
			t.Errorf("the same instant read in %s gave %v..%v, want %v..%v, the day boundary is "+
				"the yard's, not the server's", zone, got.Since, got.Until, want.Since, want.Until)
		}
	}
	// And the day literal too, which is what the two `today` measures actually compare against.
	if got := HouseDay(evening); got != "2026-08-12" {
		t.Errorf("HouseDay(11:30pm Toronto) = %q, want 2026-08-12, in UTC that instant is the 13th", got)
	}
	if HouseDay(time.Time{}) != "" {
		t.Error("a zero instant must produce no day literal at all")
	}
}

// AddDate walks the CALENDAR rather than adding 24h, so a window spanning a DST transition is still
// N days. 8 March 2026 is the spring-forward Sunday; the seven days ending Wednesday the 11th
// contain it.
func TestResolveWindow_ASpringForwardDoesNotShortenAWindow(t *testing.T) {
	w := ResolveWindow(RangeLast7, at(t, "2026-03-11 09:00"))
	if got := day(t, w.Since); got != "2026-03-05 00:00" {
		t.Fatalf("last_7 across the spring-forward starts %s, want 2026-03-05 00:00", got)
	}
	// The wall-clock span is seven days; the ELAPSED span is one hour less, and that is correct.
	if h := w.Until.Sub(w.Since).Hours(); h != 7*24-1 {
		t.Errorf("the window spans %g hours; seven calendar days across a spring-forward is %g", h, 7.0*24-1)
	}
	// The autumn transition, the other way.
	fall := ResolveWindow(RangeLast7, at(t, "2026-11-04 09:00"))
	if h := fall.Until.Sub(fall.Since).Hours(); h != 7*24+1 {
		t.Errorf("across the autumn transition the window spans %g hours, want %g", h, 7.0*24+1)
	}
}

// -- THE PRICE OF SIX COPIES, AND WHY THIS GATE STOPPED PAYING IT --------------------------------
//
// gl/businessday.go:42-47 argues for duplicating the zone rather than importing it, "a shared
// constant would couple the ledger to the message board over a fact they merely agree about. Three
// copies that agree is the house posture", and the price of that argument is a test that reads
// every copy. This is that test. It has carried its own name since there were three copies.
//
// IT HAD ALREADY GONE BLIND. Until this rewrite it enumerated the copies from a hardcoded
// three-entry map: messaging/rollup.go, inventory/transfer_eta.go, gl/businessday.go. By then there
// were FIVE, dashboard/window.go had declared its own houseZone on line 32 (the very constant this
// file's other tests are written against, sitting one file away and unchecked) and
// messagingprovider/policy.go:40 had exported DefaultZone. Two live copies could have drifted to
// another zone and this test would have passed, reporting "read 3 of 3 copies" with total
// confidence. Its guard-the-guard clause was guarding the map, not the tree.
//
// THIS IS THE LESSON OF 3844587, TWICE IN ONE SESSION. That commit found the session gate globbing
// `019[2-9]_*.sql` for the migrations a branch adds, written when 0199 was the highest number, so
// it "silently stopped checking the moment the branch reached 0200. Six of the thirteen migrations
// were unverified by that clause and the run still printed PASS. A gate whose coverage shrinks
// without failing is the exact failure mode every gate in this session exists to prevent." A
// hardcoded peer map is that same glob. Adding pos to the map would have been that same glob with
// one more entry in it.
//
// SO THE SCAN ASKS THE TREE. It walks backend/internal for every occurrence of the zone string in
// production Go and rules on what it finds:
//
//  1. EVERY occurrence must be a named constant. An inline "America/Toronto" passed straight to
//     time.LoadLocation is a copy no reader can find and no future gate can enumerate.
//  2. Every constant whose name ends in Zone and whose value looks like an IANA name must name THIS
//     zone. That is the original assertion, kept, but sourced from the tree instead of a map.
//  3. The count may go up and may not go down. A floor is not a hardcoded enumeration: it cannot
//     stop checking, it can only refuse to check less than it did yesterday.
//
// The zone appears in PROSE in nine more places, businessday.go's own argument quotes all three of
// the copies it was written beside. Those are invisible here because the parser is run without
// ParseComments, which is the same ruling cmd/demoseed/source_test.go:19-22 makes for the same
// reason: a scan that cannot tell the explanation from the defect forces the documentation to stop
// saying the thing it exists to say.
func TestHouseZone_EveryCopyInTheBackendNamesTheSameZone(t *testing.T) {
	root := filepath.Join(backendRoot(t), "internal")

	// zoneFloor is the number of copies that existed when this gate was made self-enumerating:
	// messaging/rollup.go, inventory/transfer_eta.go, messagingprovider/policy.go, gl/businessday.go,
	// dashboard/window.go and pos/sale_lookup.go. Raise it when a seventh module takes its own copy.
	// NEVER lower it to make a failure go away, a copy that disappeared either moved (say so here)
	// or was deleted, and a deleted copy means a module went back to reading the server's clock.
	const zoneFloor = 6

	type site struct {
		file, konst string
		line        int
	}
	var named []site      // occurrences that ARE a named constant
	var bare []site       // occurrences that are not
	var divergent []site  // a *Zone constant naming some OTHER zone
	var unparsed []string // files the parser could not read but whose TEXT holds the zone

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if n := d.Name(); n == "node_modules" || n == "testdata" || n == ".git" {
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
		// ParseComments is NOT set: the zone is DISCUSSED in prose in this file, in businessday.go and
		// in six others, and a gate that counted those would make the argument unwritable.
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			// A file mid-edit must not make this gate go quiet. It cannot be walked, so it is checked
			// as TEXT instead, if the zone is in there at all, the reader is told to look.
			if b, rerr := os.ReadFile(path); rerr == nil && strings.Contains(string(b), houseZone) {
				unparsed = append(unparsed, rel)
			}
			return nil
		}

		// Pass one: the literals that are the VALUE of a named constant, by position.
		constAt := map[token.Pos]string{}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, val := range vs.Values {
					lit, ok := val.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING || i >= len(vs.Names) {
						continue
					}
					unquoted, uerr := strconv.Unquote(lit.Value)
					if uerr != nil {
						continue
					}
					cname := vs.Names[i].Name
					if unquoted == houseZone {
						constAt[lit.Pos()] = cname
						named = append(named, site{rel, cname, fset.Position(lit.Pos()).Line})
						continue
					}
					// A constant that NAMES a zone but holds a different one. This is the assertion the
					// old map made ("four copies that disagree is worse than one import"), now sourced
					// from the tree: an IANA name is the only thing that can be a zone, so the shape is
					// what qualifies a constant for the comparison, not a filename somebody listed.
					if strings.HasSuffix(strings.ToLower(cname), "zone") && strings.Contains(unquoted, "/") {
						divergent = append(divergent, site{rel, cname + " = " + strconv.Quote(unquoted),
							fset.Position(lit.Pos()).Line})
					}
				}
			}
		}

		// Pass two: EVERY occurrence of the string. Anything pass one did not claim is a bare copy.
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if unquoted, uerr := strconv.Unquote(lit.Value); uerr == nil && unquoted == houseZone {
				if _, isConst := constAt[lit.Pos()]; !isConst {
					bare = append(bare, site{rel, "", fset.Position(lit.Pos()).Line})
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s, this gate cannot report on a tree it could not read: %v", root, err)
	}

	sort.Slice(named, func(i, j int) bool { return named[i].file < named[j].file })

	for _, s := range divergent {
		t.Errorf("%s:%d declares %s. This package names %q, and so does every other copy. Six "+
			"constants that disagree is worse than one import, pick one zone and change them "+
			"together, with a migration behind it if the dealer really has moved.",
			s.file, s.line, s.konst, houseZone)
	}
	for _, s := range bare {
		t.Errorf("%s:%d spells %q inline rather than declaring a named constant for it. Every other "+
			"module names its copy (rollupZone, etaZone, DefaultZone, houseZone…) so that a reader "+
			"and this gate can both find it; an inline literal is a copy that drifts in the dark.",
			s.file, s.line, houseZone)
	}
	for _, f := range unparsed {
		t.Errorf("%s holds the house zone but does not parse, so this gate could not rule on it. Fix "+
			"the file: a copy of the zone in a file nothing can walk is a copy nothing is checking.", f)
	}

	// GUARD THE GUARD, the clause the old map got wrong. A rename, a moved file or a walk that
	// quietly found nothing must FAIL rather than pass with a smaller scan.
	if len(named) < zoneFloor {
		var got []string
		for _, s := range named {
			got = append(got, s.file+":"+strconv.Itoa(s.line)+" "+s.konst)
		}
		t.Fatalf("found %d named copies of the house zone under %s, and this gate has been checking at "+
			"least %d since it was made self-enumerating. A gate whose coverage shrinks without "+
			"failing is the failure mode it exists to prevent (3844587). Copies found:\n  %s",
			len(named), root, zoneFloor, strings.Join(got, "\n  "))
	}
	if !strings.Contains(houseZone, "/") {
		t.Fatalf("houseZone is %q, which is not an IANA name", houseZone)
	}
}

// backendRoot walks up from the test's working directory to the backend module root (the directory
// holding go.mod), so the scan covers the whole tree however the test is invoked rather than
// depending on a relative path that only works from one place. testaudit/dated_tests_test.go:388
// makes the same move for the same reason.
func backendRoot(t *testing.T) string {
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
			t.Fatalf("no go.mod above %s, the gate cannot find the module root", dir)
		}
		dir = parent
	}
}
