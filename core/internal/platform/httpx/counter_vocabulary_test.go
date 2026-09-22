package httpx

// counter_vocabulary_test.go, item 5e (session A): the gate that keeps the module prefix off a screen.
//
// THE RULE. Client-facing copy carries zero internal engineering vocabulary. "dispatch: branch_id is
// required" tells a counter clerk which of OUR directories was unhappy; it is a fact about our
// package layout wearing a refusal's clothes.
//
// THE RULE IS ENFORCED AT ONE DOOR, WHICH IS THE ONLY REASON IT IS ENFORCEABLE. There are 2,000-odd
// message literals in this monolith carrying a module prefix, and the prefix EARNS its place in Go
// source: it makes an error greppable to its module, it survives wrapping, and handlers log the raw
// error separately from what they answer. Editing them all out would be a 2,000-site change that the
// next lane re-introduces on its first new error. So the prefix stays in the source and comes off in
// Error / ErrorWith, and this file guards that those two are the only ways an envelope is written.
//
//	go test ./internal/platform/httpx/ -count=1

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// envelopeOf runs a write through the real handler and returns the decoded body.
func envelopeOf(t *testing.T, write func(w *httptest.ResponseRecorder)) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	write(rec)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("envelope is not JSON: %v, %s", err, rec.Body.String())
	}
	return body
}

// TestError_TakesTheModulePrefixOffOnTheWayOut is the treatment itself, over real messages lifted
// verbatim from the tree.
func TestError_TakesTheModulePrefixOffOnTheWayOut(t *testing.T) {
	cases := []struct{ in, want string }{
		{"dispatch: branch_id is required", "branch_id is required"},
		{"invcount: a POSTED sheet cannot be cancelled", "a POSTED sheet cannot be cancelled"},
		{"gl: the period is closed", "the period is closed"},
		{"ar: that AP email address is not an address", "that AP email address is not an address"},
		{"landedcost: nothing to allocate", "nothing to allocate"},
		{"bankrec: this statement is already reconciled", "this statement is already reconciled"},
		{"settings: the item set is empty, to reset, use DELETE", "the item set is empty, to reset, use DELETE"},

		// LEFT ALONE, and each for its own reason. The strip is anchored and takes a single
		// all-lowercase token: it must not reach into a sentence and remove a clause.
		{"Dispatch: branch_id is required", "Dispatch: branch_id is required"}, // capitalised, not our prefix shape
		{"this order is already closed", "this order is already closed"},       // no prefix at all
		{"some selected lines cannot be reordered", "some selected lines cannot be reordered"},
		{"credit hold: this account is over its limit", "credit hold: this account is over its limit"}, // two words, a real clause
		{"stock_truth: not a module", "stock_truth: not a module"},                                     // underscore, not our prefix shape
		{"12:30 is not a delivery window", "12:30 is not a delivery window"},                           // digits first
		{"dispatch:no space", "dispatch:no space"},                                                     // no space, not our prefix shape
	}
	for _, c := range cases {
		body := envelopeOf(t, func(w *httptest.ResponseRecorder) { Error(w, 400, c.in) })
		if got := body["error"]; got != c.want {
			t.Errorf("Error(%q)\n  got  %q\n  want %q", c.in, got, c.want)
		}
	}
}

// TestErrorWith_GetsTheSameTreatmentAndCannotBeShadowed pins the payload-carrying form. An envelope
// that happens to hand a draft back is still an envelope a person reads.
func TestErrorWith_GetsTheSameTreatmentAndCannotBeShadowed(t *testing.T) {
	body := envelopeOf(t, func(w *httptest.ResponseRecorder) {
		ErrorWith(w, 503, "inventory: database not configured", map[string]any{"draft": "d-1", "error": "shadowed"})
	})
	if got := body["error"]; got != "database not configured" {
		t.Fatalf("ErrorWith must strip the prefix and must not let extra shadow the message: %q", got)
	}
	if got := body["draft"]; got != "d-1" {
		t.Fatalf("ErrorWith must carry its extra fields: %v", body)
	}
}

// -- the door -------------------------------------------------------------------------------------

// directEnvelope finds a hand-rolled `"error":` map literal, an envelope written without going
// through Error or ErrorWith, and therefore without the treatment.
var directEnvelope = regexp.MustCompile(`"error":\s*([^,\n}]+)`)

// modulePrefixedLiteral is the prefix shape inside a Go string literal.
var modulePrefixedLiteral = regexp.MustCompile(`^"[a-z][a-z0-9]*: `)

// bypassBaseline is every file that still writes an error envelope by hand, with the argument for
// each. THIS LIST MAY ONLY SHRINK. A new entry means a new way for a module name, or worse, to
// reach a screen without passing the one place that takes it off.
//
// The ERP baseline this test arrived with carried six files (identity, pos, order x2, portal,
// billing); none of them has been carried into this tree, so the list lands here empty of them and
// they re-enter the baseline only as their modules carry, if their hand-rolled sites survive the
// carry. The one entry that exists in this tree today:
//
//	platform/authz/middleware.go  the pinned refusal bodies of ADR-0020's addendum: one constant
//	                              401 body, one 403 body, one 503 body and four X-Org 400 bodies,
//	                              written as []byte so the pinned bytes cannot drift. They are
//	                              machine-shaped constants, never module-prefixed prose, and they
//	                              must not be re-routed through Error without a recorded decision,
//	                              because the addendum pins the exact bytes.
var bypassBaseline = map[string]bool{
	"platform/authz/middleware.go": true,
}

// TestError_IsTheOnlyDoorAnEnvelopeGoesThrough sweeps the monolith for hand-rolled envelopes.
//
// Two assertions, and they do different work. The first is HARD and has no baseline: no hand-rolled
// envelope may carry a module-prefixed literal, ever, because such a site bypasses the treatment by
// construction. The second is a RATCHET on the remaining hand-rolled sites, which are machine codes
// and prefix-free sentences, they are fine today, and the list may only shrink.
func TestError_IsTheOnlyDoorAnEnvelopeGoesThrough(t *testing.T) {
	root := filepath.Join("..", "..")
	var prefixed, bypass []string
	scanned := 0

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		if strings.HasSuffix(path, "_test.go") || strings.Contains(filepath.ToSlash(path), "platform/httpx/") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		rel := strings.TrimPrefix(filepath.ToSlash(strings.TrimPrefix(filepath.ToSlash(path), filepath.ToSlash(root))), "/")
		for _, m := range directEnvelope.FindAllStringSubmatch(string(src), -1) {
			val := strings.TrimSpace(m[1])
			// A struct tag (`json:"error"`) is not an envelope; the capture is empty or a tag fragment.
			if val == "" || strings.HasPrefix(val, "`") {
				continue
			}
			if modulePrefixedLiteral.MatchString(val) {
				prefixed = append(prefixed, rel+": "+val)
			}
			bypass = append(bypass, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	// Guard the guard: a scan that reads nothing passes every assertion below vacuously.
	// The floor is the exact non-test file count of this tree at the operations
	// carry (the walk covers internal/ minus this package), and every carry batch that
	// adds files raises it in the same change: a floor left behind while the tree grows
	// is the vacuous pass this check exists to prevent, and so is a floor above a tree
	// that shrank. It is never lowered without a recorded reason.
	if scanned < 342 {
		t.Fatalf("the sweep read only %d Go files, it is measuring nothing", scanned)
	}

	if len(prefixed) > 0 {
		sort.Strings(prefixed)
		t.Errorf("a hand-rolled envelope carries a module prefix, so it never reaches the strip in Error:\n  %s",
			strings.Join(prefixed, "\n  "))
	}

	seen := map[string]bool{}
	for _, f := range bypass {
		seen[f] = true
	}
	var added []string
	for f := range seen {
		if !bypassBaseline[f] {
			added = append(added, f)
		}
	}
	if len(added) > 0 {
		sort.Strings(added)
		t.Errorf(`%d file(s) now write an error envelope by hand rather than through httpx.Error/ErrorWith:
  %s
Use httpx.Error, or httpx.ErrorWith when the envelope must carry extra fields. If the value really is
a machine code the client switches on rather than prose a person reads, say so in bypassBaseline and
add it there, but the list is meant to shrink.`, len(added), strings.Join(added, "\n  "))
	}
}
