package dispatch

// driver_payload_test.go, the DRIVER's payload, pinned where it actually lives.
//
// WHY A NEW FILE RATHER THAN A LINE IN loadboard_test.go. The gate that already exists,
// TestLoadBoardMoneyFields_AllCarryOmitempty, reads exactly two source files, loadboard.go and
// service_loadboard.go, and matches only on a line containing `*int64 \`json:"`. That is the right
// gate for the load board, whose money is a GATED POINTER: the defect it defends against is a missing
// `,omitempty`, and the two files it reads are the two files a gated pointer may be declared in.
//
// The driver's money posture is a DIFFERENT posture in a DIFFERENT file, and it was unpinned.
// Stop.ValueCents and Stop.FreightCents live in model.go and are `json:"-"`, absent by type, for
// everybody, on every read. model.go is not in that gate's file list, so changing either tag from
// `json:"-"` to `json:"value_cents,omitempty"` is a one-word edit that leaves the entire suite,
// unit and integration, green, while putting a load's value onto the day board, the driver's day,
// the manifest feed and every other read of a Stop at once. That is precisely the class of hole
// train 14 found on LoadStop and train 15 found on the board's wiring, one file over.
//
// The two tests below close it from both ends: the tag is asserted where it is written, and the
// bytes are asserted where they are produced. The live end-to-end half, a real seeded stop with real
// cents in the column, read back through Service.MyRuns, is
// driver_moneyscan_integration_test.go.

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestModelGoMoneyFields_OnTheORDINARYPayloadAreAbsentByType is the source-reading half.
//
// It asserts the tag rather than a value on purpose, because a value test cannot see this defect. A
// test written as "the pointer is nil so nothing marshals" passes happily against
// `json:"value_cents,omitempty"`, the pointer really is nil on most rows, and only starts failing
// on the day a driver's phone is showing a real figure. The contract is BYTE-ABSENCE, so the thing
// worth reading is the byte that decides it: the struct tag.
func TestModelGoMoneyFields_OnTheORDINARYPayloadAreAbsentByType(t *testing.T) {
	body, err := os.ReadFile("model.go")
	if err != nil {
		t.Fatalf("read model.go: %v", err)
	}
	// The money names this package uses. Any line declaring a field with one of these names, in the
	// file that holds the ordinary Stop/Run projections, must be marshal-suppressed outright.
	moneyNames := []string{"ValueCents", "FreightCents", "CostCents", "MarginCents", "PriceCents", "TotalCents"}
	found := 0
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.Contains(trimmed, "`json:") {
			continue
		}
		named := ""
		for _, n := range moneyNames {
			if strings.HasPrefix(trimmed, n+" ") || strings.HasPrefix(trimmed, n+"\t") {
				named = n
			}
		}
		if named == "" {
			continue
		}
		found++
		if !strings.Contains(trimmed, "`json:\"-\"`") {
			t.Errorf("model.go declares %s on an ORDINARY payload with a wire tag: %s\n"+
				"Money on the general Stop/Run projection is absent BY TYPE, `json:\"-\"`, not "+
				"omitempty, not zeroed. The general payload is returned by the day board, the driver's "+
				"day, the manifest feed and RunByNo; a field that is on it is a field somebody has to "+
				"remember to blank in every one of them. A gated figure belongs on LoadStop "+
				"(service_loadboard.go), which one policy-asking method produces. If you meant to add "+
				"a NEW money fact, add it there and leave this file's posture alone.", named, trimmed)
		}
	}
	if found < 2 {
		t.Fatalf("expected to find at least the two money fields this gate is about (ValueCents, "+
			"FreightCents) declared in model.go; found %d. If they were renamed, rename them in "+
			"moneyNames above, a gate that matches nothing is a gate that passes for the wrong reason.", found)
	}
}

// TestDriverLine_HasNoMoneyMemberAtAll pins the OTHER half of the driver's money posture: the lines
// added to the driver's day in this train.
//
// It is a source read for the same reason the test above is, but the defect it defends against is one
// step earlier. Stop's safety is one struct tag deep, the repository loads the real cents into
// ValueCents on every driver read and the tag is what stops them reaching the wire. DriverLine's
// safety is that there is nothing to suppress: the struct has no money member, so no tag, no policy
// call and no read path can be got wrong. Order lines carry a unit price in every other module in
// this system, and "add the price, it's already on the line" is a two-word patch away at all times.
func TestDriverLine_HasNoMoneyMemberAtAll(t *testing.T) {
	// The bytes first: a fully-populated line, marshalled, carries nothing money-shaped.
	blob, err := json.Marshal(DriverLine{
		LineNo: 1, SKU: "PAV-BRN-60", Description: "Brown pavers, 60mm", Qty: "48.000", UOM: "EA",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, forbidden := range []string{"cents", "value", "cost", "margin", "price", "total"} {
		if strings.Contains(strings.ToLower(string(blob)), forbidden) {
			t.Fatalf("a driver's line payload carries %q. A driver is told WHAT is on the truck and "+
				"never WHAT IT IS WORTH; that is the whole reason this type is a sibling of "+
				"PickFeedLine rather than a projection of an order line. Got %s", forbidden, blob)
		}
	}
	if !strings.Contains(string(blob), `"qty":"48.000"`) || !strings.Contains(string(blob), `"uom":"EA"`) {
		t.Fatalf("quantity and unit ARE the point of the line and must survive; got %s", blob)
	}

	// And the declaration, so a new member cannot arrive with a name this test's byte scan happens
	// not to catch (an `Amt`, a `Subtotal`, a `Rate`).
	body, err := os.ReadFile("model.go")
	if err != nil {
		t.Fatalf("read model.go: %v", err)
	}
	decl := string(body)
	start := strings.Index(decl, "type DriverLine struct {")
	if start < 0 {
		t.Fatal("DriverLine is gone from model.go, if it moved, move this gate with it")
	}
	end := strings.Index(decl[start:], "\n}")
	if end < 0 {
		t.Fatal("could not find the end of the DriverLine declaration")
	}
	allowed := map[string]bool{"LineNo": true, "SKU": true, "Description": true, "Qty": true, "UOM": true}
	for _, line := range strings.Split(decl[start:start+end], "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.Contains(trimmed, "`json:") {
			continue
		}
		name := strings.Fields(trimmed)[0]
		if !allowed[name] {
			t.Errorf("DriverLine grew a field this gate has not been asked about: %q.\n"+
				"That is not automatically wrong, but the type exists so that 'a driver's lines carry "+
				"no money' is enforced by the COMPILER rather than by a read path. Add the field to "+
				"the allowed set here only after checking it is not a figure, and say why in the "+
				"commit. Line: %s", name, trimmed)
		}
	}
}
