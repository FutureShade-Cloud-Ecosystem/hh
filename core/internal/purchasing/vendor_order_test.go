package purchasing

// vendor_order_test.go, the one-recipient rule and the send record, DB-free.
//
// What is worth proving without a database is exactly what a database cannot prove: that this module
// CANNOT blast a purchase order. The ambiguity refusal is the load-bearing arm and it is asserted
// with its COUNT in the message, because "we found three addresses" is the operator's next question
// and a refusal that does not answer it gets clicked through.
//
// The send ORDER is proved the same way: the fake store records the sequence of calls, so
// "row before transport" is a fact about this code rather than a comment above it.

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// - fakes -

type fakeOrders struct {
	po    PO
	lines []POLine
	err   error
}

func (f *fakeOrders) POHeader(context.Context, string) (PO, error) { return f.po, f.err }
func (f *fakeOrders) POLinesFor(context.Context, string) ([]POLine, error) {
	return f.lines, f.err
}

// fakeSends records the ORDER of what happened to it, which is the property under test. Its insert
// speaks the repository's whole contract (HH-181 round 3): the key index is global, so a key held by
// THIS order is the concurrent-retry replay (the row comes back, nothing is stored), a key held by
// ANOTHER order is the collision, and an unheld key inserts. The mutex is for the concurrent arm;
// every other test here is single-goroutine.
type fakeSends struct {
	contact  VendorContact
	tagged   bool
	rows     []POSend
	priorKey string
	prior    POSend
	findErr  error

	mu       sync.Mutex
	trail    []string
	inserted POSend
	marked   [5]string
	byKey    map[string]POSend
	insertN  int
}

func (f *fakeSends) VendorContact(context.Context, string) (VendorContact, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trail = append(f.trail, "read-contact")
	return f.contact, f.tagged, nil
}

// heldByKey is the row the global key index names, the seeded prior first, then rows this fake
// stored. Caller holds the mutex.
func (f *fakeSends) heldByKey(key string) POSend {
	if f.priorKey != "" && key == f.priorKey {
		return f.prior
	}
	return f.byKey[key]
}

func (f *fakeSends) InsertPOSend(_ context.Context, row POSend, actorID string) (POSend, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if row.IdempotencyKey != "" {
		if held := f.heldByKey(row.IdempotencyKey); held.ID != "" {
			if held.POID == row.POID {
				return held, true, nil
			}
			return POSend{}, false, conflict("purchasing: idempotency key %q already belongs to a send for another order, a new order needs a new key",
				row.IdempotencyKey)
		}
	}
	f.trail = append(f.trail, "insert")
	f.insertN++
	row.ID = "send-" + strconv.Itoa(f.insertN)
	f.inserted = row
	if row.IdempotencyKey != "" {
		if f.byKey == nil {
			f.byKey = map[string]POSend{}
		}
		f.byKey[row.IdempotencyKey] = row
	}
	_ = actorID
	return row, false, nil
}

func (f *fakeSends) MarkPOSendResult(_ context.Context, id, status, provider, ref, detail string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trail = append(f.trail, "mark:"+status)
	f.marked = [5]string{id, status, provider, ref, detail}
	return nil
}

func (f *fakeSends) FindPOSendByKey(_ context.Context, key, poID string) (POSend, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trail = append(f.trail, "find-key")
	// A read the test fails on purpose, the round 2 arm: what the service does with a replay read
	// that errors is a decision this file can prove.
	if f.findErr != nil {
		return POSend{}, false, f.findErr
	}
	// Keyed to the order like the real read: a key is a replay only for the order that minted it.
	if key != "" {
		if held := f.heldByKey(key); held.ID != "" && held.POID == poID {
			return held, true, nil
		}
	}
	return POSend{}, false, nil
}

func (f *fakeSends) ListPOSends(context.Context, string) ([]POSend, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows, nil
}

// insertCount is how many rows this fake actually stored, the concurrent arm's "one row".
func (f *fakeSends) insertCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.insertN
}

// missRead records a replay read that found nothing, for the gated concurrent arm.
func (f *fakeSends) missRead() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trail = append(f.trail, "find-key")
}

type fakeCatalogue struct {
	rows []VendorSKU
	err  error
}

func (f *fakeCatalogue) VendorSKUs(context.Context, string) ([]VendorSKU, error) {
	return f.rows, f.err
}
func (f *fakeCatalogue) UpsertVendorSKU(context.Context, VendorSKU) error      { return nil }
func (f *fakeCatalogue) DeleteVendorSKU(context.Context, string, string) error { return nil }
func (f *fakeCatalogue) UpsertVendorContact(context.Context, VendorContact, string) error {
	return nil
}
func (f *fakeCatalogue) DeleteVendorContact(context.Context, string) error { return nil }

// recordingSender remembers what crossed the seam and answers however the test tells it to.
type recordingSender struct {
	live      bool
	delivered bool
	err       error
	got       POSendMessage
	calls     int
	trail     *[]string
}

func (s *recordingSender) Name() string { return "recording" }
func (s *recordingSender) Live() bool   { return s.live }
func (s *recordingSender) Send(_ context.Context, m POSendMessage) (POSendReceipt, error) {
	s.calls++
	s.got = m
	if s.trail != nil {
		*s.trail = append(*s.trail, "transport")
	}
	if s.err != nil {
		return POSendReceipt{Provider: "recording"}, s.err
	}
	return POSendReceipt{Provider: "recording", Ref: "ref-9", Delivered: s.delivered,
		Detail: "recorded"}, nil
}

// gatedReads holds every replay read at a barrier until the expected number of callers have arrived,
// and answers each as the miss it was AT THAT MOMENT: the miss is decided at arrival, never re-read
// after release, because the winner's insert lands after the barrier and re-reading would answer the
// loser's read as a hit, exactly the interleaving this gate exists to exclude. Embedding keeps the
// rest of the seam on the wrapped fake.
type gatedReads struct {
	*fakeSends
	barrier *sync.WaitGroup
}

func (g *gatedReads) FindPOSendByKey(_ context.Context, _, _ string) (POSend, bool, error) {
	g.barrier.Done()
	g.barrier.Wait()
	g.fakeSends.missRead()
	return POSend{}, false, nil
}

// countingSender counts transport calls across goroutines; the concurrent arm needs only the count.
type countingSender struct{ calls atomic.Int64 }

func (s *countingSender) Name() string { return "counting" }
func (s *countingSender) Live() bool   { return true }
func (s *countingSender) Send(context.Context, POSendMessage) (POSendReceipt, error) {
	s.calls.Add(1)
	return POSendReceipt{Provider: "counting", Ref: "ref-9", Delivered: true, Detail: "recorded"}, nil
}

// countingAudit counts the audit entries the service records, guarded for the concurrent arm.
type countingAudit struct {
	mu      sync.Mutex
	actions []string
}

func (a *countingAudit) Record(_ context.Context, e audit.SensitiveAccess) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.actions = append(a.actions, e.Action)
	return nil
}

func (a *countingAudit) count(action string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, x := range a.actions {
		if x == action {
			n++
		}
	}
	return n
}

func sendCaller() identity.StaffContext {
	return identity.StaffContext{UserID: "u-buyer", Tier: identity.TierManager, Scope: identity.ScopeGlobal}
}

func sendSvc(orders *fakeOrders, sends *fakeSends, sender POSender, p Policy) *Service {
	return sendSvcAs(sendCaller(), orders, sends, sender, p)
}

// sendSvcAs is sendSvc for a caller the test chooses, the arm the branch-fence cases need.
func sendSvcAs(c identity.StaffContext, orders *fakeOrders, sends *fakeSends, sender POSender, p Policy) *Service {
	svc := NewService(nil).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return c, true }).
		withOrderStore(orders).
		withSendStore(sends).
		withCatalogueStore(&fakeCatalogue{}).
		withPolicyStore(&fakePolicy{p: p})
	if sender != nil {
		svc = svc.WithPOSender(sender)
	}
	return svc
}

func orderedPO() *fakeOrders {
	return &fakeOrders{
		po: PO{ID: "po-1", PONo: "#PO-TRT-4", VendorID: "v-1", BranchID: "b-1",
			Status: StatusOrdered, Currency: "CAD", FXRate: 1},
		lines: []POLine{{ID: "l-1", LineNo: 1, ProductID: "p-1", SKU: "UNI-BEA-GRA",
			Description: "Beacon Hill Flagstone", QtyOrderedBase: 120, UOM: "PC",
			UnitCostCents: 5511, LineTotalCents: 661320}},
	}
}

func taggedDesk() VendorContact {
	return VendorContact{VendorID: "v-1", Name: "Orders desk", Email: "orders@unilock.example"}
}

// - 1 · the one-recipient rule -

func TestResolveVendorTarget_TheTaggedDeskWinsAndIsRecordedAsSuch(t *testing.T) {
	c := taggedDesk()
	got, err := ResolveVendorTarget(&c, []string{"anything@else.example"}, DefaultPolicy(), false)
	if err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	if got.Recipient != "orders@unilock.example" {
		t.Fatalf("recipient = %q", got.Recipient)
	}
	if got.Kind != RecipientVendorContact {
		t.Fatalf("kind = %q, want %q", got.Kind, RecipientVendorContact)
	}
	if got.FallbackReason != "" {
		t.Fatalf("a tagged send carries a fallback reason: %q", got.FallbackReason)
	}
}

// THE LOAD-BEARING ARM. Two addresses and nobody tagged is refused, and the refusal names HOW MANY -
// which is the operator's next question and the difference between a message that gets read and one
// that gets clicked through.
func TestResolveVendorTarget_AmbiguityIsRefusedWithTheCount(t *testing.T) {
	for _, n := range []int{2, 3, 5} {
		addrs := make([]string, 0, n)
		for i := 0; i < n; i++ {
			addrs = append(addrs, string(rune('a'+i))+"@vendor.example")
		}
		_, err := ResolveVendorTarget(nil, addrs, DefaultPolicy(), true)
		if err == nil {
			t.Fatalf("%d addresses and no tagged contact was NOT refused, that is the blast", n)
		}
		msg := err.Error()
		if !strings.Contains(msg, strings.TrimSpace(itoa(n))) {
			t.Errorf("the refusal does not name the count %d: %q", n, msg)
		}
		if !strings.Contains(msg, "tag") {
			t.Errorf("the refusal does not name the way forward: %q", msg)
		}
	}
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return ""
}

func TestResolveVendorTarget_AnUnconfirmedFallbackIsRefused(t *testing.T) {
	_, err := ResolveVendorTarget(nil, []string{"one@vendor.example"}, DefaultPolicy(), false)
	if err == nil {
		t.Fatal("an untagged send went out without anybody confirming it")
	}
	if !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("the refusal does not name the way forward: %q", err)
	}
}

func TestResolveVendorTarget_AConfirmedSingleFallbackIsRecordedAsFallbackForever(t *testing.T) {
	got, err := ResolveVendorTarget(nil, []string{"one@vendor.example"}, DefaultPolicy(), true)
	if err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	if got.Kind != RecipientFallback {
		t.Fatalf("kind = %q, want FALLBACK, deriving this later would let a contact tagged tomorrow rewrite what happened today", got.Kind)
	}
	if got.FallbackReason == "" {
		t.Fatal("a fallback with no recorded reason is a fallback nobody can audit")
	}
}

func TestResolveVendorTarget_RefusePostureNeverReachesTheCandidates(t *testing.T) {
	p := Policy{UntaggedFallback: FallbackRefuse}
	_, err := ResolveVendorTarget(nil, []string{"one@vendor.example"}, p, true)
	if err == nil {
		t.Fatal("REFUSE let an untagged send through")
	}
	if !strings.Contains(err.Error(), "tag one") {
		t.Fatalf("the refusal does not name the way forward: %q", err)
	}
}

func TestResolveVendorTarget_NoAddressAtAllIsRefusedInWords(t *testing.T) {
	_, err := ResolveVendorTarget(nil, nil, DefaultPolicy(), true)
	if err == nil {
		t.Fatal("a send with no recipient at all was allowed")
	}
	if !strings.Contains(err.Error(), "tag an ordering contact") {
		t.Fatalf("the refusal does not name the way forward: %q", err)
	}
}

// A tagged desk that is a phone number is NAMED, not silently skipped into the fallback. Somebody
// did the work of tagging; the honest answer is that this build cannot mail a phone number.
func TestResolveVendorTarget_APhoneOnlyDeskIsNamedRatherThanSkipped(t *testing.T) {
	c := VendorContact{VendorID: "v-1", Name: "Ordering", Phone: "613-555-0134"}
	_, err := ResolveVendorTarget(&c, []string{"one@vendor.example"}, DefaultPolicy(), true)
	if err == nil {
		t.Fatal("a phone-only contact silently fell through to the vendor record address")
	}
	if !strings.Contains(err.Error(), "phone number") {
		t.Fatalf("the refusal does not say what is wrong: %q", err)
	}
}

func TestResolveVendorTarget_DuplicateAddressesAreOneAddress(t *testing.T) {
	// Two spellings of the same desk is not ambiguity, and refusing it would teach the operator that
	// the refusal is noise.
	got, err := ResolveVendorTarget(nil,
		[]string{"Orders@Vendor.example", " orders@vendor.example "}, DefaultPolicy(), true)
	if err != nil {
		t.Fatalf("the same address twice was treated as two: %v", err)
	}
	if got.Recipient != "Orders@Vendor.example" {
		t.Fatalf("recipient = %q, want the record's own first spelling", got.Recipient)
	}
}

// - 2 · the module cannot express a blast -

func TestVendorSend_NothingInThisPackageReturnsAListOfRecipients(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) < 10 {
		t.Fatalf("only %d files scanned, the sweep has drifted", len(files))
	}
	plural := regexp.MustCompile(`\)\s*\(?\s*\[\]SendTarget`)
	// GUARD THE GUARD: the scan recognises the thing it forbids when shown it.
	if !plural.MatchString("func f(x int) ([]SendTarget, error)") {
		t.Fatal("the scan no longer recognises a list-of-recipients return")
	}
	var offenders []string
	seenSingular := false
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src := shippedSource(t, f)
		if strings.Contains(src, "(SendTarget, error)") {
			seenSingular = true
		}
		if plural.MatchString(src) {
			offenders = append(offenders, f)
		}
	}
	if !seenSingular {
		t.Fatal("no function returns a single SendTarget, the resolver has been renamed and this gate is asleep")
	}
	if len(offenders) > 0 {
		t.Fatalf("these files can return more than one recipient: %v, a blast must not be expressible", offenders)
	}
}

// - 3 · the send record -

func TestSendPO_TheRowIsWrittenBeforeTheTransportIsCalled(t *testing.T) {
	sends := &fakeSends{contact: taggedDesk(), tagged: true}
	sender := &recordingSender{live: true, delivered: true, trail: &sends.trail}
	if _, err := sendSvc(orderedPO(), sends, sender, DefaultPolicy()).
		SendPO(context.Background(), "po-1", SendPORequest{}); err != nil {
		t.Fatalf("send: %v", err)
	}
	insert, transport := indexOf(sends.trail, "insert"), indexOf(sends.trail, "transport")
	if insert < 0 || transport < 0 {
		t.Fatalf("trail = %v, the send did not both record and attempt", sends.trail)
	}
	if insert > transport {
		t.Fatalf("trail = %v, the transport was called before the row existed; an order that leaves with no row behind it is a commitment nobody can find", sends.trail)
	}
	if sends.marked[1] != SendDelivered {
		t.Fatalf("marked %q, want DELIVERED from a live transport that delivered", sends.marked[1])
	}
}

// THE SHIPPED POSTURE. Nothing in this build delivers email, so the default sender must record the
// row and then say so, never report a delivery that did not happen.
func TestSendPO_TheUnwiredTransportRecordsAndRefusesRatherThanClaiming(t *testing.T) {
	sends := &fakeSends{contact: taggedDesk(), tagged: true}
	got, err := sendSvc(orderedPO(), sends, nil, DefaultPolicy()).
		SendPO(context.Background(), "po-1", SendPORequest{})
	if err != nil {
		t.Fatalf("the send errored instead of recording an honest failure: %v", err)
	}
	if got.Status != SendFailed {
		t.Fatalf("status = %q, want FAILED, a buyer who believes an order went out waits three weeks for a truck nobody ordered", got.Status)
	}
	if !strings.Contains(got.Detail, "no transport is wired") {
		t.Fatalf("detail = %q, want the honest words", got.Detail)
	}
	if indexOf(sends.trail, "insert") < 0 {
		t.Fatalf("trail = %v, the attempt left no record", sends.trail)
	}
	if got.RecipientKind != RecipientVendorContact || got.Recipient != "orders@unilock.example" {
		t.Fatalf("the failed row lost who it was aimed at: %+v", got)
	}
}

func TestSendPO_ATransportErrorIsAFailedRowAndNotAThrownRequest(t *testing.T) {
	sends := &fakeSends{contact: taggedDesk(), tagged: true}
	sender := &recordingSender{live: true, err: errors.New("smtp: connection refused")}
	got, err := sendSvc(orderedPO(), sends, sender, DefaultPolicy()).
		SendPO(context.Background(), "po-1", SendPORequest{})
	if err != nil {
		t.Fatalf("a transport failure became a request failure: %v", err)
	}
	if got.Status != SendFailed || !strings.Contains(sends.marked[4], "connection refused") {
		t.Fatalf("marked = %v, want FAILED carrying the transport's own words", sends.marked)
	}
}

func TestSendPO_TheDocumentCrossesTheSeamWithTheMessage(t *testing.T) {
	sends := &fakeSends{contact: taggedDesk(), tagged: true}
	sender := &recordingSender{live: true, delivered: true}
	got, err := sendSvc(orderedPO(), sends, sender, DefaultPolicy()).
		SendPO(context.Background(), "po-1", SendPORequest{})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if sender.got.DocumentName != "PO-TRT-4.csv" {
		t.Fatalf("attachment name = %q", sender.got.DocumentName)
	}
	if !strings.Contains(string(sender.got.DocumentCSV), "UNI-BEA-GRA") {
		t.Fatalf("the attachment does not carry the order:\n%s", sender.got.DocumentCSV)
	}
	// A PURCHASE ORDER LEAVING THE BUILDING ALWAYS CARRIES ITS PRICES. The cost gate governs who may
	// READ the order on a screen; it does not redact the contract.
	if !strings.Contains(string(sender.got.DocumentCSV), "55.11") {
		t.Fatalf("the sent document carries no prices:\n%s", sender.got.DocumentCSV)
	}
	if got.DocumentBytes != len(sender.got.DocumentCSV) {
		t.Fatalf("recorded %d bytes, attached %d", got.DocumentBytes, len(sender.got.DocumentCSV))
	}
	if !strings.Contains(sender.got.Body, "#PO-TRT-4") || !strings.Contains(sender.got.Subject, "#PO-TRT-4") {
		t.Fatalf("the message does not quote the number the vendor echoes back: %+v", sender.got)
	}
}

func TestSendPO_AnIdenticalRetryReusesItsRow(t *testing.T) {
	sends := &fakeSends{contact: taggedDesk(), tagged: true,
		priorKey: "k-1", prior: POSend{ID: "send-0", POID: "po-1", Status: SendFailed, Recipient: "orders@unilock.example"}}
	sender := &recordingSender{live: true, delivered: true}
	got, err := sendSvc(orderedPO(), sends, sender, DefaultPolicy()).
		SendPO(context.Background(), "po-1", SendPORequest{IdempotencyKey: "k-1"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if got.ID != "send-0" {
		t.Fatalf("the retry minted a new row %q, a double-clicked button must be one order", got.ID)
	}
	if sender.calls != 0 {
		t.Fatalf("the retry called the transport %d times", sender.calls)
	}
}

// HH-181 (b): the key index is global in the database, so a client that reuses one key across two orders
// must be told it collided, in the 409 shape this route already speaks, and must never be handed the FIRST
// order's record as though the second order went out. A caller told "sent" for an order that was never
// sent is left waiting for a truck nobody ordered.
func TestSendPO_AKeyHeldByAnotherOrderIsAKeyCollisionNotAReplay(t *testing.T) {
	sends := &fakeSends{contact: taggedDesk(), tagged: true,
		priorKey: "k-1", prior: POSend{ID: "send-0", POID: "po-2", Status: SendFailed,
			Recipient: "orders@unilock.example"}}
	sender := &recordingSender{live: true, delivered: true}
	got, err := sendSvc(orderedPO(), sends, sender, DefaultPolicy()).
		SendPO(context.Background(), "po-1", SendPORequest{IdempotencyKey: "k-1"})
	if err == nil {
		t.Fatalf("the second order was answered with the first order's record %+v, a key collision is not a replay", got)
	}
	var ce clientError
	if !errors.As(err, &ce) || ce.status != http.StatusConflict {
		t.Fatalf("the collision = %v, want the 409 conflict classification", err)
	}
	if got.ID == "send-0" || got.POID == "po-2" {
		t.Fatalf("the first order's record leaked through the collision: %+v", got)
	}
	if sender.calls != 0 {
		t.Fatalf("the second order was sent to the vendor %d times behind a colliding key", sender.calls)
	}
}

// HH-181 (c): the replay answer must arrive AFTER the branch fence, never before it. A manager scoped to
// another branch who quotes a real key must meet the fence, not the record: a replay answered first is a
// read of another branch's send history through the one route that forgot to ask.
func TestSendPO_AReplayKeyDoesNotAnswerForACallerOutsideTheBranch(t *testing.T) {
	outsider := identity.StaffContext{UserID: "u-other", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Branches: map[string]bool{"b-2": true}}
	sends := &fakeSends{contact: taggedDesk(), tagged: true,
		priorKey: "k-1", prior: POSend{ID: "send-0", POID: "po-1", Status: SendFailed}}
	sender := &recordingSender{live: true, delivered: true}
	got, err := sendSvcAs(outsider, orderedPO(), sends, sender, DefaultPolicy()).
		SendPO(context.Background(), "po-1", SendPORequest{IdempotencyKey: "k-1"})
	if !errors.Is(err, errForbidden) {
		t.Fatalf("err = %v, a caller outside the branch was answered through the replay instead of the fence", err)
	}
	if got.ID == "send-0" {
		t.Fatalf("the replay handed another branch's send record to a caller the fence refuses: %+v", got)
	}
	if indexOf(sends.trail, "find-key") >= 0 {
		t.Fatalf("the replay lookup ran before the branch fence: %v", sends.trail)
	}
	if sender.calls != 0 {
		t.Fatalf("the transport was called %d times for a caller the fence refuses", sender.calls)
	}
}

// HH-181 round 2 (P2): a replay read that FAILS must be returned, never fallen past. Falling through
// answers a genuine same-order retry as a key collision once the insert trips the index, which is a
// claim about a row the code just failed to read.
func TestSendPO_AFailedReplayReadIsReturnedNeverFallenPast(t *testing.T) {
	sends := &fakeSends{contact: taggedDesk(), tagged: true,
		findErr: errors.New("purchasing: find po send: connection reset")}
	sender := &recordingSender{live: true, delivered: true}
	got, err := sendSvc(orderedPO(), sends, sender, DefaultPolicy()).
		SendPO(context.Background(), "po-1", SendPORequest{IdempotencyKey: "k-1"})
	if err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("err = %v, the failed replay read was swallowed and the send proceeded to %+v", err, got)
	}
	if indexOf(sends.trail, "insert") >= 0 {
		t.Fatalf("a send whose replay read failed still wrote a row: %v", sends.trail)
	}
	if sender.calls != 0 {
		t.Fatalf("the transport was called %d times behind a failed read", sender.calls)
	}
}

// HH-181 round 3 (P1): the loser of a same-order double click must be answered exactly as the serial
// replay is. Both replay reads are held at a barrier until BOTH have missed, the interleaving the
// route exists for, by construction rather than by luck of the scheduler; the fake's insert then
// speaks the repository's whole contract, so the loser's insert is handed the winner's row as a
// replay, and everything after the insert must skip: no second transport call (a duplicate purchase
// order dispatched to the vendor once a real transport is wired), no mark over the winner's result,
// no second purchasing.po.sent.
func TestSendPO_TheLoserOfAConcurrentDoubleClickIsTheReplayNotASecondDispatch(t *testing.T) {
	sends := &fakeSends{contact: taggedDesk(), tagged: true}
	var reads sync.WaitGroup
	reads.Add(2)
	gated := &gatedReads{fakeSends: sends, barrier: &reads}
	sender := &countingSender{}
	audits := &countingAudit{}
	svc := NewService(nil).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return sendCaller(), true }).
		withOrderStore(orderedPO()).
		withSendStore(gated).
		withCatalogueStore(&fakeCatalogue{}).
		withPolicyStore(&fakePolicy{p: DefaultPolicy()}).
		WithPOSender(sender).
		WithAudit(audits)

	const callers = 2
	var done sync.WaitGroup
	rows := make([]POSend, callers)
	errs := make([]error, callers)
	done.Add(callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			defer done.Done()
			rows[i], errs[i] = svc.SendPO(context.Background(), "po-1", SendPORequest{IdempotencyKey: "k-1"})
		}(i)
	}
	done.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d of the double click was refused: %v", i, err)
		}
	}
	if rows[0].ID == "" || rows[0].ID != rows[1].ID {
		t.Fatalf("the double click was answered as two orders: %+v and %+v", rows[0], rows[1])
	}
	if n := sender.calls.Load(); n != 1 {
		t.Fatalf("the transport was called %d times for one order and one key, a duplicate dispatch", n)
	}
	if n := sends.insertCount(); n != 1 {
		t.Fatalf("%d send rows for one order and one key", n)
	}
	if n := audits.count("purchasing.po.sent"); n != 1 {
		t.Fatalf("%d purchasing.po.sent entries for one send", n)
	}
}

func TestSendPO_ADraftIsRefusedAndACancelledOrderIsRefused(t *testing.T) {
	for _, status := range []string{StatusDraft, StatusCancelled} {
		orders := orderedPO()
		orders.po.Status = status
		sends := &fakeSends{contact: taggedDesk(), tagged: true}
		_, err := sendSvc(orders, sends, nil, DefaultPolicy()).
			SendPO(context.Background(), "po-1", SendPORequest{})
		if err == nil {
			t.Fatalf("a %s order was sent to a vendor", status)
		}
		if indexOf(sends.trail, "insert") >= 0 {
			t.Fatalf("a %s order left a send row behind: %v", status, sends.trail)
		}
	}
}

func TestSendPO_AnUntaggedVendorIsRefusedAndWritesNothing(t *testing.T) {
	sends := &fakeSends{}
	_, err := sendSvc(orderedPO(), sends, nil, DefaultPolicy()).
		SendPO(context.Background(), "po-1", SendPORequest{})
	if err == nil {
		t.Fatal("an order with no recipient was sent")
	}
	if indexOf(sends.trail, "insert") >= 0 {
		t.Fatalf("a refused send left a row behind: %v", sends.trail)
	}
}

func indexOf(xs []string, want string) int {
	for i, x := range xs {
		if x == want {
			return i
		}
	}
	return -1
}

// - 4 · validation, in words before a CHECK could 500 -

func TestValidateVendorContact_RefusesTheNameInABox(t *testing.T) {
	_, err := ValidateVendorContact(VendorContact{Name: "Somebody"})
	if err == nil {
		t.Fatal("a contact with no address and no number was accepted")
	}
	if !strings.Contains(err.Error(), "email or a phone") {
		t.Fatalf("the refusal does not say what is missing: %q", err)
	}
}

func TestValidateVendorSKU_RefusesAHalfFilledUnit(t *testing.T) {
	_, err := ValidateVendorSKU(VendorSKU{ProductID: "p-1", VendorUOM: "SKID"})
	if err == nil {
		t.Fatal("a vendor unit with no conversion factor was accepted, nothing can be counted in it")
	}
	if _, err := ValidateVendorSKU(VendorSKU{ProductID: "p-1", QtyPerVendorUOM: 60}); err == nil {
		t.Fatal("a conversion factor with no unit was accepted, it is a number with no dimension")
	}
	if _, err := ValidateVendorSKU(VendorSKU{ProductID: "p-1", VendorSKU: "UNI-9911"}); err != nil {
		t.Fatalf("a code-only mapping is legitimate and was refused: %v", err)
	}
	if _, err := ValidateVendorSKU(VendorSKU{ProductID: "p-1"}); err == nil {
		t.Fatal("an entirely empty mapping was accepted")
	}
}

// - 5 · the migration says what this file says -

func TestMigration0197_KeepsTheOneRowPerVendorShapeAndForbidsABlastValue(t *testing.T) {
	// CARRY NOTE (HH-3.2 B5): the pin read the ERP's migrations directory;
	// under the carry the frozen set lives under core/db/migrations.
	b, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "0197_purchasing_vendor_order.sql"))
	if err != nil {
		t.Fatalf("read 0197: %v", err)
	}
	// COMMENTS ARE STRIPPED BEFORE THE SCAN, the house rule for every gate here. This migration's own
	// header ARGUES against a BLAST value and NAMES what reverting would cost, a scan that read
	// prose as schema would fail on the very sentences that make the schema defensible.
	stripped := regexp.MustCompile(`(?m)^\s*--.*$`).ReplaceAllString(string(b), "")

	// GUARD THE GUARD: stripping must remove the prose and keep the DDL.
	if strings.Contains(stripped, "Modelling this as a list") {
		t.Fatal("the comment stripper is not stripping")
	}
	if !strings.Contains(stripped, "CREATE TABLE IF NOT EXISTS purchasing_vendor_contacts") {
		t.Fatal("the comment stripper ate the schema, every assertion below would be vacuous")
	}

	for _, want := range []string{
		"vendor_id    UUID PRIMARY KEY REFERENCES vendors(id)", // one row per vendor, by key
		"recipient_kind  TEXT NOT NULL CHECK (recipient_kind IN ('VENDOR_CONTACT', 'FALLBACK'))",
		"untagged_fallback TEXT NOT NULL DEFAULT 'CONFIRM'",
		"cost_visibility  TEXT NOT NULL DEFAULT 'MANAGER_UP'",
		"CONSTRAINT ck_purchasing_vendor_uom_pair CHECK ((vendor_uom IS NULL) = (qty_per_vendor_uom IS NULL))",
	} {
		if !strings.Contains(stripped, want) {
			t.Errorf("0197 no longer contains %q, the schema and this module now disagree", want)
		}
	}
	if strings.Contains(stripped, "'BLAST'") {
		t.Error("0197 has grown a BLAST fallback value, the thing the column exists to prevent must not be spellable")
	}
	// ADDITIVE ONLY. Staging is never wiped, so a DROP or an ALTER of a shipped purchasing table in
	// this file would be a change to data somebody is standing on.
	for _, forbidden := range []string{"DROP TABLE", "DROP COLUMN", "ALTER TABLE", "BEGIN;", "COMMIT;"} {
		if strings.Contains(strings.ToUpper(stripped), forbidden) {
			t.Errorf("0197 contains %q, the runner wraps each file in one transaction and staging is never wiped", forbidden)
		}
	}
}
