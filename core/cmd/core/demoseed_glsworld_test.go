//go:build demo

package main

// demoseed_glsworld_test.go, the promises of the demo world seam, asserted on the plan without a
// database. The plan is where the first dealer's words enter the demo world, so the proof is
// on the plan: every string value of every row the demo build would write passes through the
// dictionary, and none reads a word from the denylist afterwards.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo"
)

// glsDenylist is the brief's denylist (the first dealer's name, branch codes and cities, the
// Ontario and Canadian postal, phone, currency and tax words, the real brand and supplier names
// of the first dealer's seed, the old system, and the demo marker suffix), plus the placeholder
// tokens the scrub left in migration-planted names.
var glsDenylist = regexp.MustCompile(`(?i)dibbits|quinte|trenton|belleville|kingston|brighton|marmora|cataraqui|hastings|limestone city|ontario|toronto|brampton|northline|northland|bay ridge|\bkgn\b|\btrt\b|\b[abceghjklmnprstvxy]\d[abceghjklmnprstvwxyz][ -]?\d[abceghjklmnprstvwxyz]\d\b|\bcad\b|\bhst\b|613[.-]\d{3}[.-]\d{4}|\(demo|techo|unilock|permacon|oaks|brown's|sika|rymar|alliance gator|in-lite|banas|bistrack|scrubbed party|scrubname|best.?way|brussels|holland|courtstone|granular a`)

// glsPlanStrings returns every string value in the rows, with its table and column.
func glsPlanStrings(rows []row) []string {
	var out []string
	for _, r := range rows {
		for i, v := range r.vals {
			switch s := v.(type) {
			case string:
				out = append(out, r.table+"."+r.cols[i]+" = "+s)
			case []byte:
				out = append(out, r.table+"."+r.cols[i]+" = "+string(s))
			}
		}
	}
	return out
}

func glsWorldInput(t *testing.T) planInput {
	t.Helper()
	in := testInput(t)
	in.Branches = []branch{
		{ID: "5d1b3f6e-0000-4000-9000-000000000002", Code: "JUL", Name: "Gable Landscape Supply, Julian"},
		{ID: "5d1b3f6e-0000-4000-9000-000000000001", Code: "PLM", Name: "Gable Landscape Supply, Point Loma"},
	}
	return in
}

// TestGLSWorld_TenantConfigNamesTheDealer: the config the demo box serves publicly names the demo
// dealer and no word of the first dealer.
func TestGLSWorld_TenantConfigNamesTheDealer(t *testing.T) {
	c := demo.DealerConfig()
	if c.DealerName != "Gable Landscape Supply" {
		t.Errorf("the demo tenant config names %q, want Gable Landscape Supply", c.DealerName)
	}
	if glsDenylist.MatchString(c.Id + c.DealerName + c.SupportEmail + c.SupportPhone) {
		t.Errorf("the demo tenant config reads a first dealer word: %+v", c)
	}
}

// TestGLSWorld_PlanReadsNoFirstDealerWord: the premise first (the plan's own literals do carry
// the denylist, so the proof bites), then the promise (after the dictionary none does).
func TestGLSWorld_PlanReadsNoFirstDealerWord(t *testing.T) {
	rows, _, err := buildPlan(glsWorldInput(t))
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	raw := 0
	for _, s := range glsPlanStrings(rows) {
		if glsDenylist.MatchString(s) {
			raw++
		}
	}
	if raw == 0 {
		t.Fatal("the unrewritten plan carries no denylist word, so the proof below passes vacuously")
	}
	for _, s := range glsPlanStrings(glsRewritePlan(rows)) {
		if glsDenylist.MatchString(s) {
			t.Errorf("after the dictionary the plan still reads a first dealer word: %s", s)
		}
	}
	// Even when the plan is built before glsPrepareWorld converges the branch rows (as in
	// `demoseed -plan` dry-run or testInput), glsRewritePlan and glsRewriteSummary rewrite the
	// branch codes and summary lines onto the demo dealer's words.
	unconvergedRows, unconvergedSum, err := buildPlan(testInput(t))
	if err != nil {
		t.Fatalf("buildPlan(testInput): %v", err)
	}
	for _, s := range glsPlanStrings(glsRewritePlan(unconvergedRows)) {
		if glsDenylist.MatchString(s) {
			t.Errorf("after the dictionary on unconverged branches the plan still reads a first dealer word: %s", s)
		}
	}
	rewrittenSum := glsRewriteSummary(unconvergedSum)
	for _, slice := range [][]string{
		rewrittenSum.RunFill, rewrittenSum.WillCall, rewrittenSum.Quotes,
		rewrittenSum.QuoteLinks, rewrittenSum.ContractorLines,
		rewrittenSum.DepositOrders, rewrittenSum.ProcurementLines,
	} {
		for _, line := range slice {
			if glsDenylist.MatchString(line) {
				t.Errorf("after glsRewriteSummary a summary line still reads a first dealer word: %s", line)
			}
		}
	}
	// Every seeded purchase order names USD explicitly in the demo world, rather than omitting
	// currency and falling back to the schema's CAD default.
	poCount := 0
	for _, r := range glsRewritePlan(rows) {
		if r.table != "purchasing_pos" {
			continue
		}
		poCount++
		f := fieldMap(r)
		if got := f["currency"]; got != "USD" {
			t.Errorf("purchasing_pos %v carries currency %v, want USD", f["po_no"], got)
		}
	}
	if poCount == 0 {
		t.Fatal("the plan carries no purchasing_pos rows")
	}
	// Receiver names sealed into pos_signature before glsRewritePlan runs must already be
	// role labels without person names or (demo) markers.
	for _, st := range demoStops {
		if glsDenylist.MatchString(st.Received) {
			t.Errorf("stop %s receiver %q still carries a denylisted word before sealing", st.Key, st.Received)
		}
	}
	for _, w := range demoWillCall {
		if glsDenylist.MatchString(w.Received) {
			t.Errorf("will-call %s receiver %q still carries a denylisted word before sealing", w.Key, w.Received)
		}
	}
	t.Logf("the plan carried %d denylisted values before the dictionary, none after", raw)
}

// TestGLSWorld_NoPersonNameInPlan: the people the plan names are role labels, never a person.
func TestGLSWorld_NoPersonNameInPlan(t *testing.T) {
	rows, _, err := buildPlan(glsWorldInput(t))
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	people := []string{"Priya", "Nair", "Ted Nurse", "Ravi Raman", "Owen Sharpe", "Vance", "Okonkwo", "Beaupre", "Villeneuve", "Okafor", "Lachance", "Marchand"}
	for _, s := range glsPlanStrings(glsRewritePlan(rows)) {
		for _, p := range people {
			if strings.Contains(s, p) {
				t.Errorf("the demo plan still names a person (%s): %s", p, s)
			}
		}
	}
}

// TestGLSWorld_DictionaryIsIdempotent: carrying a value through the dictionary twice reads the
// same as once, for every key and every replacement the tables hold; a second apply on a built
// world therefore renames nothing.
func TestGLSWorld_DictionaryIsIdempotent(t *testing.T) {
	var values []string
	for k, v := range glsExact {
		values = append(values, k, v)
	}
	for _, tok := range glsTokens {
		values = append(values, tok[0], tok[1])
	}
	for _, v := range values {
		once := glsApply(v)
		if twice := glsApply(once); twice != once {
			t.Errorf("the dictionary is not idempotent on %q: once %q, twice %q", v, once, twice)
		}
	}
	for k, v := range glsExact {
		if glsDenylist.MatchString(v) {
			t.Errorf("the exact replacement for %q reads a denylisted word: %q", k, v)
		}
	}
	for _, tok := range glsTokens {
		if glsDenylist.MatchString(tok[1]) {
			t.Errorf("the token replacement for %q reads a denylisted word: %q", tok[0], tok[1])
		}
	}
}

// TestGLSWorld_SitesAreInSanDiegoCounty: every delivery site the demo build plans carries a
// San Diego County postal code, a phone in the reserved 555 0100 to 555 0199 block and
// coordinates inside the county's box.
func TestGLSWorld_SitesAreInSanDiegoCounty(t *testing.T) {
	check := func(label, postal, phone string, lat, lng float64) {
		if !strings.HasPrefix(postal, "91") && !strings.HasPrefix(postal, "92") {
			t.Errorf("%s: postal code %q is not a San Diego County code", label, postal)
		}
		if !regexp.MustCompile(`^\d{3}-555-01\d\d$`).MatchString(phone) {
			t.Errorf("%s: phone %q is not in the reserved 555 0100 to 555 0199 block", label, phone)
		}
		if lat < 32.5 || lat > 33.5 || lng < -117.6 || lng > -116.0 {
			t.Errorf("%s: coordinates %.4f, %.4f are outside San Diego County", label, lat, lng)
		}
	}
	for _, s := range demoStops {
		check("stop "+s.Key, s.Postal, s.Phone, s.Lat, s.Lng)
	}
	for _, s := range demoTodayStops {
		if s.Tender == "" {
			check("today stop "+s.Key, s.Postal, s.Phone, s.Lat, s.Lng)
		}
	}
	for _, s := range demoDriverDayStops {
		check("driver day stop "+s.Key, s.Postal, s.Phone, s.Lat, s.Lng)
	}
}
