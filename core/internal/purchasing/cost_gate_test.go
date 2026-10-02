package purchasing

// cost_gate_test.go, the byte scan behind "a clerk role sees no cost bytes".
//
// THREE THINGS ARE PROVED HERE, and the first one is what makes the other two mean anything:
//
//  1. THE POSITIVE CONTROL. The allowed caller's payload is marshalled FIRST and asserted to carry
//     the figures. A byte scan with no positive control passes just as happily over a struct whose
//     money never populated at all, which is the failure mode this test would otherwise hide.
//  2. THE REFUSED CALLER'S PAYLOAD IS `{}`, and the full purchase order carries no money KEY, no
//     money VALUE and, the arm three separate trains in this program have been caught by, no
//     literal `null`. `null` is not absence: it is a field that announces itself and then declines
//     to answer, which is how a screen learns to draw an empty cost column.
//  3. THE GATE HAS EXACTLY ONE PRODUCER, proved by a source scan with comments stripped. A second
//     place that sets a POCosts pointer would be a second gate, and the second gate is always the
//     one nobody remembers to close.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// - fakes -

type fakePolicy struct {
	mu  sync.Mutex
	p   Policy
	err error

	gotBranch string
}

// Policy records the branch it was asked for. The mutex is for the
// concurrent double-click test (HH-181): two goroutines ride this fake at
// once, and an unguarded write here is a data race the race detector
// reports even though the service under test is clean.
func (f *fakePolicy) Policy(_ context.Context, branchID string) (Policy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotBranch = branchID
	return f.p, f.err
}

func gateManager() identity.StaffContext {
	return identity.StaffContext{UserID: "u-mgr", Tier: identity.TierManager, Scope: identity.ScopeGlobal}
}

func gateClerk() identity.StaffContext {
	return identity.StaffContext{UserID: "u-clerk", Tier: identity.TierFrontline, Scope: identity.ScopeGlobal}
}

func gateSvc(p Policy, c identity.StaffContext) *Service {
	return NewService(nil).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return c, true }).
		withPolicyStore(&fakePolicy{p: p})
}

// gateOrder is the fixture both halves marshal: one two-line purchase order with distinctive money
// on every field the gate covers, so a leak is findable by searching for the DIGITS and not only
// for the key name.
func gateOrder() PODetail {
	po := PO{
		ID: "po-1", PONo: "#PO-TRT-4", VendorID: "v-1", BranchID: "b-1",
		Status: StatusOrdered, Currency: "CAD", FXRate: 1,
		SubtotalCents: 778811, SubtotalCadCents: 778811,
		CreatedAt: time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC),
	}
	lines := []POLine{
		{ID: "l-1", LineNo: 1, ProductID: "p-1", SKU: "UNI-BEA-GRA", QtyOrderedBase: 120, UOM: "PC",
			UnitCostCents: 5511, LineTotalCents: 661320, BilledCents: 4433},
		{ID: "l-2", LineNo: 2, ProductID: "p-2", SKU: "TEC-BLU-60", QtyOrderedBase: 40, UOM: "PC",
			UnitCostCents: 2937, LineTotalCents: 117480, BilledCents: 0},
	}
	receipts := []Receipt{{
		ID: "r-1", POID: "po-1", PONo: "#PO-TRT-4", Seq: 1, GRNNo: "#PO-TRT-4/R1",
		ValueCadCents: 220055, ReceivedAt: time.Date(2026, 8, 10, 7, 30, 0, 0, time.UTC),
		Lines: []ReceiptLine{{ID: "rl-1", POLineID: "l-1", ProductID: "p-1", SKU: "UNI-BEA-GRA",
			QtyBase: 40, UnitCostCents: 5511, ValueCents: 220440}},
	}}
	return PODetail{PO: po, Lines: lines, Receipts: receipts}
}

// stamped applies a lens to the whole fixture the way GetPO does, so the test exercises the real
// stamps rather than a hand-built payload.
func stamped(lens costLens) PODetail {
	d := gateOrder()
	lens.stampPO(&d.PO)
	lens.stampPOLines(d.Lines)
	lens.stampReceipts(d.Receipts)
	return d
}

// moneyDigits are the exact figures gateOrder carries. A leak that renamed a key still prints these.
var moneyDigits = []string{"778811", "5511", "661320", "2937", "117480", "4433", "220055", "220440"}

// moneyKeys are every JSON key the gate is responsible for.
var moneyKeys = []string{
	"subtotal_cents", "subtotal_cad_cents", "unit_cost_cents", "line_total_cents",
	"billed_cents", "value_cents", "value_cad_cents", "value_open_cents",
}

// - 1 · the positive control, then the absence -

func TestCostGate_AllowedCallerSeesEveryFigure_ThePositiveControl(t *testing.T) {
	raw, err := json.Marshal(stamped(costLens{allow: true}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	for _, k := range moneyKeys {
		if k == "value_open_cents" {
			continue // the worklist's field; it is not on a PODetail and is proved below
		}
		if !strings.Contains(body, `"`+k+`"`) {
			t.Errorf("the ALLOWED caller is missing %s, the gate is closed on everybody, which would make every absence test below vacuous:\n%s", k, body)
		}
	}
	for _, d := range moneyDigits {
		if !strings.Contains(body, d) {
			t.Errorf("the ALLOWED caller is missing the figure %s", d)
		}
	}
}

func TestCostGate_RefusedCallerGetsNoCostBytes(t *testing.T) {
	raw, err := json.Marshal(stamped(costLens{}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	for _, k := range moneyKeys {
		if strings.Contains(body, k) {
			t.Errorf("the refused caller received the key %q, money must be ABSENT, not present and empty:\n%s", k, body)
		}
	}
	for _, d := range moneyDigits {
		if strings.Contains(body, d) {
			t.Errorf("the refused caller received the figure %s, a renamed key is still a leak:\n%s", d, body)
		}
	}
	// `null` IS NOT ABSENCE, and this program has been caught by that three times. A null cost is a
	// field that announces itself and declines to answer, and a screen that sees one draws the
	// column.
	if strings.Contains(body, "null") {
		t.Errorf("the refused caller received a literal null, null is a field that failed to populate, not a field that is not there:\n%s", body)
	}
	// The order itself still arrived. A gate that hid the purchase order would be a route gate, and
	// the argument for putting this one at the marshal layer is that a clerk opens the same order.
	for _, want := range []string{`"#PO-TRT-4"`, `"UNI-BEA-GRA"`, `"qty_ordered_base":120`} {
		if !strings.Contains(body, want) {
			t.Errorf("the refused caller lost %s, the gate must remove the money and nothing else:\n%s", want, body)
		}
	}
}

func TestCostGate_TheStandalonePayloadMarshalsAsEmptyObject(t *testing.T) {
	// THE HOUSE SHAPE, asserted literally: the zero value is `{}`.
	raw, err := json.Marshal(costLens{}.costs(func(c *POCosts) {
		v := int64(999)
		c.Subtotal, c.UnitCost = &v, &v
	}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(raw) != "{}" {
		t.Fatalf("refused POCosts = %s, want {}", raw)
	}
	// The positive control on the same producer, so "{}" cannot be the answer to everything.
	raw, err = json.Marshal(costLens{allow: true}.costs(func(c *POCosts) {
		v := int64(999)
		c.Subtotal, c.UnitCost = &v, &v
	}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"subtotal_cents":999`) {
		t.Fatalf("allowed POCosts = %s, want the figure", raw)
	}
}

func TestCostGate_TheWorklistFigureIsGatedToo(t *testing.T) {
	items := []WorklistItem{{POID: "po-1", PONo: "#PO-TRT-4", BranchID: "b-1", OpenLines: 2,
		QtyOutstanding: 80, ValueOpenCents: 441199}}
	costLens{allow: true}.stampWorklist(items)
	allowed, _ := json.Marshal(items)
	if !strings.Contains(string(allowed), `"value_open_cents":441199`) {
		t.Fatalf("allowed worklist lost its figure: %s", allowed)
	}
	items[0].POCosts = POCosts{}
	costLens{}.stampWorklist(items)
	refused, _ := json.Marshal(items)
	if strings.Contains(string(refused), "441199") || strings.Contains(string(refused), "value_open") {
		t.Fatalf("refused worklist still carries the open value: %s", refused)
	}
}

func TestCostGate_TheIncomingReadIsGatedToo(t *testing.T) {
	rows := []IncomingPOLine{{POID: "po-1", PONo: "#PO-TRT-4", BranchID: "b-1",
		QtyOutstanding: 80, UOM: "PC", UnitCostCents: 5511}}
	costLens{allow: true}.stampIncoming(rows)
	allowed, _ := json.Marshal(rows)
	if !strings.Contains(string(allowed), `"unit_cost_cents":5511`) {
		t.Fatalf("allowed incoming lost its figure: %s", allowed)
	}
	rows[0].POCosts = POCosts{}
	costLens{}.stampIncoming(rows)
	refused, _ := json.Marshal(rows)
	if strings.Contains(string(refused), "5511") || strings.Contains(string(refused), "unit_cost") {
		t.Fatalf("refused incoming still carries the unit cost: %s", refused)
	}
}

// - 2 · who the lens lets through -

func TestCostLens_ResolvesTheThreeVisibilityWords(t *testing.T) {
	cases := []struct {
		name    string
		vis     string
		caller  identity.StaffContext
		wantSee bool
	}{
		{"manager-up admits a manager", settings.VisibilityManagerUp, gateManager(), true},
		{"manager-up refuses a clerk", settings.VisibilityManagerUp, gateClerk(), false},
		{"all-staff admits a clerk", settings.VisibilityAllStaff, gateClerk(), true},
		{"hidden refuses a manager", settings.VisibilityHidden, gateManager(), false},
		{"an unknown word falls back to manager-up", "EVERYONE_AND_THE_DOG", gateClerk(), false},
		{"an unknown word still admits a manager", "EVERYONE_AND_THE_DOG", gateManager(), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := gateSvc(Policy{CostVisibility: tc.vis, UntaggedFallback: FallbackConfirm}, tc.caller)
			if got := svc.costLensFor(context.Background(), "b-1").allow; got != tc.wantSee {
				t.Fatalf("allow = %v, want %v", got, tc.wantSee)
			}
		})
	}
}

func TestCostLens_AnAnonymousCallerNeverSeesCost(t *testing.T) {
	svc := NewService(nil).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return identity.StaffContext{}, false }).
		withPolicyStore(&fakePolicy{p: Policy{CostVisibility: settings.VisibilityAllStaff}})
	if svc.costLensFor(context.Background(), "b-1").allow {
		t.Fatal("an unauthenticated caller was admitted by ALL_STAFF, the gate must fail closed before it reads a policy")
	}
}

func TestPolicyFor_AnUnreadablePolicyFailsClosedAndNeverErrors(t *testing.T) {
	svc := NewService(nil).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return gateClerk(), true }).
		withPolicyStore(&fakePolicy{err: context.DeadlineExceeded})
	p := svc.PolicyFor(context.Background(), "b-1")
	if p.CostVisibility != settings.VisibilityManagerUp {
		t.Fatalf("visibility = %q, want the restrictive default", p.CostVisibility)
	}
	if p.UntaggedFallback != FallbackConfirm {
		t.Fatalf("fallback = %q, want CONFIRM, REFUSE on a scan error would stop the yard ordering", p.UntaggedFallback)
	}
	if svc.costLensFor(context.Background(), "b-1").allow {
		t.Fatal("a clerk saw cost through a failed policy read")
	}
}

func TestPolicyFor_NoRepositoryStillAnswers(t *testing.T) {
	// The read path degrades downward: no store at all is a value, never a 500.
	svc := NewService(nil).withCaller(func(context.Context) (identity.StaffContext, bool) { return gateManager(), true })
	if got := svc.PolicyFor(context.Background(), ""); got.CostVisibility != settings.VisibilityManagerUp {
		t.Fatalf("policy = %+v, want the seeded default", got)
	}
}

// - 3 · the gate has exactly one producer (source scan) -

// shippedSource reads a package file with its comments REMOVED, so a comment that quotes the thing
// being forbidden is not read as the thing itself. The app-side gates
// (_counter-vocabulary.test.mjs) take exactly this lens, and for exactly this reason.
func shippedSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	src := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(b), "")
	src = regexp.MustCompile(`(?m)^\s*//.*$`).ReplaceAllString(src, "")
	return regexp.MustCompile(`\s//[^"\n]*$`).ReplaceAllString(src, "")
}

// poCostsWrite matches an assignment into one of POCosts' gated pointers.
var poCostsWrite = regexp.MustCompile(`\.(Subtotal|SubtotalCad|UnitCost|LineTotal|Billed|Value|ValueCad|ValueOpen)\s*(,[^=\n]*)?=\s*[^=]`)

func TestCostGate_ExactlyOneFileProducesACost(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	// GUARD THE GUARD, first half: the sweep must actually reach this package.
	if len(files) < 10 {
		t.Fatalf("only %d source files found, the scan has drifted", len(files))
	}
	// GUARD THE GUARD, second half: the regex still recognises a write when it is shown one.
	if !poCostsWrite.MatchString("c.UnitCost = &unit") || !poCostsWrite.MatchString("c.Subtotal, c.SubtotalCad = &sub, &cad") {
		t.Fatal("the scan no longer recognises a POCosts write, every zero below would be vacuous")
	}
	if poCostsWrite.MatchString("if c.UnitCost == nil {") {
		t.Fatal("the scan counts a comparison as a write")
	}

	writers := map[string]int{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		for _, line := range strings.Split(shippedSource(t, f), "\n") {
			if poCostsWrite.MatchString(line) {
				writers[f]++
			}
		}
	}
	if len(writers) != 1 || writers["cost_gate.go"] == 0 {
		t.Fatalf("purchase cost is produced in %v, there must be exactly one producer (cost_gate.go), because the second gate is the one nobody remembers to close", writers)
	}
	if writers["cost_gate.go"] < 6 {
		t.Fatalf("cost_gate.go contains only %d cost writes, the stamps have gone somewhere else", writers["cost_gate.go"])
	}
}

func TestCostGate_EveryGatedReadShapeKeepsItsMoneyOffTheWire(t *testing.T) {
	// The other half of the same rule, from the model's side: on every PURCHASE-ORDER read shape,
	// each money field must be `json:"-"` and the shape must embed POCosts. A field that kept its
	// own tag would reach a client without ever passing the lens.
	//
	// THE SCOPE IS THE PO SURFACE, AND THAT IS ARGUED RATHER THAN OVERLOOKED. Two families of money
	// are deliberately outside it:
	//   · AP BILL shapes (Bill, BillLine, Instalment). A PO unit cost is what we BUY AT, a
	//     negotiated price a competitor would like. A vendor bill is what we OWE, and the people who
	//     read it are the people whose job is to pay it. Hiding a payable behind a purchase-cost
	//     switch takes the invoice away from AP. That gate is AP's, with AP's own audience.
	//   · REQUEST bodies (AddLineParams, BillLineParams). Those are money coming IN, typed by the
	//     buyer who is allowed to type it; `json:"-"` on an inbound field would silently discard it.
	src := shippedSource(t, "model.go") + "\n" + shippedSource(t, "incoming.go")
	shapes := []string{"PO", "POLine", "Receipt", "ReceiptLine", "WorklistItem", "IncomingPOLine"}
	money := regexp.MustCompile(`(?m)^\s*(SubtotalCents|SubtotalCadCents|UnitCostCents|LineTotalCents|BilledCents|ValueCadCents|ValueCents|ValueOpenCents)\s+int64\s+` + "`" + `json:"([^"]*)"`)

	found, leaked, ungated := 0, []string{}, []string{}
	for _, name := range shapes {
		body := regexp.MustCompile(`(?s)\ntype ` + name + ` struct \{(.*?)\n\}`).FindStringSubmatch(src)
		if body == nil {
			t.Fatalf("the shape %s is no longer declared where this gate looks, the scan is asleep", name)
		}
		found++
		for _, m := range money.FindAllStringSubmatch(body[1], -1) {
			if m[2] != "-" {
				leaked = append(leaked, name+"."+m[1]+" → "+m[2])
			}
		}
		if !regexp.MustCompile(`(?m)^\s*POCosts\s*$`).MatchString(body[1]) {
			ungated = append(ungated, name)
		}
	}
	// GUARD THE GUARD: every shape was located, and the scan still recognises a leak when shown one.
	if found != len(shapes) {
		t.Fatalf("located %d of %d shapes", found, len(shapes))
	}
	probe := "\ntype Probe struct {\n\tUnitCostCents int64 `json:\"unit_cost_cents\"`\n}"
	if len(money.FindAllStringSubmatch(probe, -1)) != 1 {
		t.Fatal("the scan no longer recognises an ungated money field")
	}
	if len(leaked) > 0 {
		t.Fatalf("these money fields marshal without passing the gate: %v", leaked)
	}
	if len(ungated) > 0 {
		t.Fatalf("these read shapes carry money but embed no gate: %v", ungated)
	}
}
