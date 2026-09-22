package dispatch

// freightraise_wiring_test.go, the gate that would have caught the union-15 defect on THIS feature.
//
// THE LESSON THIS FILE EXISTS FOR. Union train 15 measured something worth remembering: the load
// board's money gate was correctly DEFINED, its payload tags were correctly ABSENT, and replacing the
// one line that joined them with `showValue := true` left the entire suite, unit and integration,
// on that tree and on origin/qa, perfectly green while putting a customer's figures on a driver's
// wire. The policy object was right and the struct was right; only the wiring was wrong, and the
// wiring was the one thing nothing read.
//
// A second-truck gate has exactly the same shape and three more places to lose it:
//
//	1. the composition root can forget to hand dispatch the approvals queue, and the gate falls open;
//	2. the composition root can forget to hand identity the return leg, and an approval decides
//	   nothing;
//	3. the quoted baseline can be read through the `<= 0 means unknown` rule its neighbour uses,
//	   and every customer promised free delivery becomes ungated in silence.
//
// None of the three is visible in a service test, because the service is correct in all three worlds.
// So this file reads the SOURCE, the composition root and this package, and asserts the joins. It
// strips comments first, for the reason every scan in this repo does: the code below argues about the
// thing it forbids, and a scan that cannot tell a rule from a sentence about the rule reads the cure
// as the disease.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	goBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	goLineComment  = regexp.MustCompile(`(?m)^\s*//.*$`)
)

// shippedGo is the file as the COMPILER sees it: comments removed, line count preserved so a failure
// can quote a line number the reader will find in their editor.
func shippedGo(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := goBlockComment.ReplaceAllStringFunc(string(raw), func(m string) string {
		return strings.Repeat("\n", strings.Count(m, "\n"))
	})
	return goLineComment.ReplaceAllString(src, "")
}

// compositionRoot reads the wiring file that owns dispatch's joins under
// the carry (HH-3.2 B5): the operations wire function in cmd/core, the
// composition root's successor for these modules.
func compositionRoot(t *testing.T) string {
	t.Helper()
	return shippedGo(t, filepath.Join("..", "..", "cmd", "core", "wire_operations.go"))
}

// The gate is only a gate if the composition root actually hands dispatch a queue to file on and
// hands identity a way back. Either line missing is a feature that looks complete and decides
// nothing, or worse, applies without asking.
func TestFreightGate_IsWiredInTheCompositionRoot(t *testing.T) {
	root := compositionRoot(t)
	for _, join := range []string{
		"WithFreightApprovals(",   // dispatch → the ONE approvals queue
		"WithOrderFreight(",       // an approved raise moves the figure the next split divides
		"WithFreightDecider(",     // identity → dispatch, the return leg
		"ApplyFreightRaiseTx(",    // …and the two halves of it
		"RefuseFreightRaiseTx(",   //
		"FileFreightApproval(",    // the filing itself
		"OrderQuotedFreightCents", // the baseline read
	} {
		if !strings.Contains(root, join) {
			t.Errorf("cmd/core/wire_operations.go does not wire %s, the gate is defined and joined to nothing", join)
		}
	}
	// The gate guards itself: a composition root this test could not read would pass every assertion
	// above vacuously.
	if len(root) < 10000 || !strings.Contains(root, "dispatch.NewService(") {
		t.Fatal("this test did not read the real composition root, so it asserted nothing")
	}
}

// THE BASELINE IS NOT READ THROUGH A `<= 0` RULE, and this is the assertion that costs money if it
// goes. 0194 made orders.quoted_freight_cents NULLable precisely so "never came from a quote" and
// "quoted no delivery charge" are different facts; its neighbour orders.freight_cents is
// `NOT NULL DEFAULT 0` and IS legitimately read that way. Routing the baseline through the same
// helper would turn every promise of free delivery into an order that can be charged freight with
// nobody asked, the exact case the client described.
func TestFreightGate_QuotedBaselineIsNeverReadThroughAZeroRule(t *testing.T) {
	root := compositionRoot(t)
	i := strings.Index(root, "func (a dispatchOrderValueAdapter) OrderQuotedFreightCents")
	if i < 0 {
		t.Fatal("the composition root has no quoted-baseline reader, so this test asserts nothing")
	}
	body := root[i:]
	if j := strings.Index(body[1:], "\nfunc "); j >= 0 {
		body = body[:j+1]
	}
	if strings.Contains(body, "readCents(") {
		t.Error("the quoted baseline is read through readCents, whose `cents <= 0` rule makes a quoted ZERO look like no quote at all")
	}
	if strings.Contains(body, "<= 0") || strings.Contains(body, "< 1") {
		t.Errorf("the quoted baseline reader carries a zero rule:\n%s", body)
	}
	// It must distinguish NULL, which is only expressible through a pointer scan.
	if !strings.Contains(body, "*int64") {
		t.Errorf("the quoted baseline reader cannot tell SQL NULL from zero:\n%s", body)
	}
	// Positive control: the SIBLING reader really does carry the rule this one must not, so the
	// assertions above are about a genuine difference and not about a phrase nobody writes.
	if !strings.Contains(root, "if cents <= 0 {") {
		t.Fatal("positive control failed: the value reader no longer carries the `<= 0` rule this test contrasts with")
	}
}

// dispatch NEVER READS THE BASELINE OFF THE ORDERS TABLE. The name appears in this package twice and
// both are its own: a column on delivery_freight_raises, which dispatch owns, and a JSON key on the
// payload it produces. What it must never do is put that column and that table in one statement -
// that is a read across a module boundary, and the whole reason OrderValueReader exists is that this
// package asks for what it needs instead of reaching for it.
func TestFreightGate_DispatchReadsTheBaselineOnlyThroughItsSeam(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var offenders []string
	own := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		for _, line := range strings.Split(shippedGo(t, f), "\n") {
			if !strings.Contains(line, "quoted_freight_cents") {
				continue
			}
			own++
			if strings.Contains(line, "orders") {
				offenders = append(offenders, f+": "+strings.TrimSpace(line))
			}
		}
	}
	if len(offenders) > 0 {
		t.Errorf("dispatch reads another module's column off its table instead of asking through the seam: %v", offenders)
	}
	// The scan guards itself twice over: it must have read real source, and it must have FOUND the
	// column somewhere, a scan that never sees the name proves nothing about how it is used.
	if len(files) < 10 || own == 0 {
		t.Fatalf("this scan asserted nothing: %d files, %d mentions of the column", len(files), own)
	}
}

// THERE IS NO SECOND DOOR. Freight reaches delivery_stops through exactly one statement, and that
// statement is reached from exactly two places: the ordinary split (which has already asked the gate)
// and the apply (which can only be reached from an approval decision).
func TestFreightGate_OneStatementWritesALoadsFreight(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	writers, callers := 0, map[string]int{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src := shippedGo(t, f)
		writers += strings.Count(src, "UPDATE delivery_stops SET freight_cents")
		if n := strings.Count(src, "writeFreightSplitOn("); n > 0 {
			callers[f] = n
		}
	}
	if writers != 1 {
		t.Errorf("delivery_stops.freight_cents is written by %d statements, want exactly 1", writers)
	}
	// Two pool-shaped doors onto the one body, WriteFreightSplit and WriteFreightSplitTx, plus the
	// definition itself. Anything more is a third path that has not been graded by the gate.
	total := 0
	for _, n := range callers {
		total += n
	}
	if total != 3 {
		t.Errorf("the one freight-writing statement is reached %d ways, want 3 (its definition + the pool door + the tx door): %v", total, callers)
	}
	// The gate guards itself.
	if len(callers) == 0 {
		t.Fatal("this scan found no freight writer at all, so it asserted nothing")
	}
}

// The gate stands between the arithmetic and the write, in that order. A gate that fires after the
// loads have been minted has already applied part of the split it is refusing, which is exactly the
// defect this suite caught the first time it ran, when the fourth truck appeared on the board before
// anybody had agreed to pay for it.
func TestFreightGate_FiresBeforeAnythingIsMinted(t *testing.T) {
	src := shippedGo(t, "service_loadboard.go")
	gate := strings.Index(src, "if GatesFreightRaise(")
	mint := strings.Index(src, "s.repo.InsertSplitLoad(")
	write := strings.Index(src, "s.applySplit(")
	if gate < 0 || mint < 0 || write < 0 {
		t.Fatal("SplitFreight no longer has a gate, a mint and a write to order, this test asserts nothing")
	}
	if gate > mint {
		t.Error("a load is minted BEFORE the gate decides: a parked raise would put a truck on the board nobody approved")
	}
	if gate > write {
		t.Error("freight is written BEFORE the gate decides")
	}
}
