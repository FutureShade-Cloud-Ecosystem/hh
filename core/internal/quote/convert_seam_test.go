package quote

// convert_seam_test.go, DIB-483 evidence for M3-G7 (staff door), M3-G7B (the refusal keeps its
// reason) and M3-G12 (the fulfilment choice, default byte-identical).
//
// The load-bearing test in this file is TestConvert_BothDoorsReachOneSeam. The gate's finding was not
// "there is no staff convert" alone, it was that building one invites a SECOND credit gate. So the
// test does not check that a staff convert exists; it checks that the staff convert and the
// contractor convert, handed the same store, produce ONE identical call into the persistence seam.
// A copy-paste implementation passes "does it work" and fails this.

import (
	"context"
	"errors"
	auth "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// - a store that satisfies BOTH doors -
// One type implementing quoteStore AND staffStore is what makes the shared-seam assertion possible:
// the two services are handed THE SAME INSTANCE, so a recorded call cannot be attributed to a
// coincidence of two similar fakes.

type recordedConvert struct {
	customerID string
	quoteID    string
	opt        ConvertOptions
}

type seamStore struct {
	*fakeStaffStore
	calls     []recordedConvert
	convertNo string
	convertID string
	err       error
}

func newSeamStore() *seamStore {
	return &seamStore{
		fakeStaffStore: newFakeStaffStore("ACCEPTED"),
		convertID:      "order-1",
		convertNo:      "#SO-TRT-1041",
	}
}

func (s *seamStore) Convert(_ context.Context, customerID, quoteID string, opt ConvertOptions) (ConvertResult, error) {
	s.calls = append(s.calls, recordedConvert{customerID: customerID, quoteID: quoteID, opt: opt})
	if s.err != nil {
		return ConvertResult{}, s.err
	}
	return ConvertResult{OrderID: s.convertID, OrderNo: s.convertNo}, nil
}

// the reads the PORTAL door needs, which the staff fake does not carry.
func (s *seamStore) ListQuotes(context.Context, string, Filter) ([]Quote, error) {
	return nil, nil
}
func (s *seamStore) GetQuote(context.Context, string, string) (Detail, error) { return Detail{}, nil }
func (s *seamStore) Accept(context.Context, string, string) (Quote, error)    { return Quote{}, nil }
func (s *seamStore) Reject(context.Context, string, string) (Quote, error)    { return Quote{}, nil }

// compile-time: one type, both doors. If either interface drifts, this is a build failure, which is
// itself part of the guarantee, because the two doors must keep needing the same seam.
var (
	_ quoteStore = (*seamStore)(nil)
	_ staffStore = (*seamStore)(nil)
)

// sellerCtx builds a staff caller in the quote's branch holding a selling role.
func sellerCtx(role string) identity.StaffContext {
	return identity.StaffContext{
		UserID: "u-seller", RoleCode: role, Tier: identity.TierFrontline,
		Scope: "BRANCH", Status: identity.StatusActive,
		Branches: map[string]bool{qBranch: true},
	}
}

func staffSvcOver(store *seamStore, caller identity.StaffContext) *StaffService {
	return NewStaffService(store).withCaller(
		func(context.Context) (identity.StaffContext, bool) { return caller, true })
}

// - M3-G7: the staff door exists AND reaches the same seam -

// TestConvert_BothDoorsReachOneSeam is the anti-duplication assertion. The contractor door and the
// counter door are driven over ONE store; both must land in store.Convert with the SAME arguments.
//
// This is what dies if someone re-implements the conversion behind the staff door instead of calling
// runConvert: a duplicated implementation either records a second, differently-shaped call, or (if it
// writes its own SQL) records no call at all.
func TestConvert_BothDoorsReachOneSeam(t *testing.T) {
	store := newSeamStore()
	ctx := context.Background()

	// door 1, the contractor (portal): the session IS the customer.
	portalRes, err := NewService(store).Convert(ctx, qCust, qOrg, ConvertOptions{})
	if err != nil {
		t.Fatalf("portal door: %v", err)
	}

	// door 2, the counter (staff): branch reach + selling role, customer read off the quote.
	staffRes, err := staffSvcOver(store, sellerCtx("INSIDE_SALES")).
		ConvertQuote(ctx, qOrg, ConvertOptions{})
	if err != nil {
		t.Fatalf("staff door: %v", err)
	}

	if len(store.calls) != 2 {
		t.Fatalf("the two doors made %d calls into the conversion seam, want exactly 2 (one each)", len(store.calls))
	}
	if store.calls[0] != store.calls[1] {
		t.Fatalf("the doors reached the seam DIFFERENTLY:\n  portal: %+v\n  staff:  %+v\n"+
			"both doors must hand the same (customer, quote, options) to one function", store.calls[0], store.calls[1])
	}
	if store.calls[0].customerID != qCust {
		t.Fatalf("seam called with customer %q, want the quote's own customer %q, the tenant scoping in "+
			"the SQL depends on this", store.calls[0].customerID, qCust)
	}
	if portalRes != staffRes {
		t.Fatalf("doors returned different results: portal %+v vs staff %+v", portalRes, staffRes)
	}
}

// TestConvert_OneSeamCallSiteInSource is the source-scan half of the same guarantee, and it is the
// one that survives a refactor the behavioural test would not notice: a second call onto the
// conversion seam anywhere in this package's non-test source means a second conversion path has been
// born, even if it happens to behave identically today.
//
// UNION TRAIN 9, WHY THIS COUNTS `.Convert(` AND NOT `store.Convert(`. The scan originally matched
// the literal receiver spelling `store.Convert(`. That is one spelling of the seam, and it is not the
// one a developer in THIS package would most naturally reach for: `convert_staff.go` already ships a
// narrowing helper, `s.convertStore()`, precisely so the call site reads as "the one seam". A
// duplicated conversion written as `s.convertStore().Convert(...)`, the idiom sitting in the same
// file, is a second call site that the old pattern did not contain as a substring, so it passed the
// scan, passed TestConvert_BothDoorsReachOneSeam (it behaves identically on the day, which is the
// whole trap), and passed the integration half too. It was caught by re-running the batch's own
// mutation #1 in the union tree, written the natural way instead of the literal way.
//
// Matching the METHOD rather than one receiver name is what makes the count mean what its message
// says. `.Convert(` is unambiguous in this package: `Repository.Convert` is the only method with that
// name anywhere in it, so the pattern cannot collect an unrelated call.
//
// It also pins that runConvert is defined exactly once. Both counts are deliberately exact, not
// "at least", a gate that tolerates growth is not a gate.
func TestConvert_OneSeamCallSiteInSource(t *testing.T) {
	callSites := 0
	definitions := 0
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("scanned no source files, the scan itself is broken, which would make this test vacuous")
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		// Comment lines are skipped: this file's own prose talks ABOUT the call site, and a scan that
		// counted documentation would either be permanently red or force the docs to stay silent
		// about the very invariant they explain.
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			callSites += strings.Count(line, ".Convert(")
			definitions += strings.Count(line, "func runConvert(")
		}
	}
	if callSites != 1 {
		t.Fatalf("found %d `.Convert(` call sites in non-test source, want exactly 1 (runConvert). "+
			"A second call site is a second conversion path, and a second conversion path is a second credit gate. "+
			"Note the receiver spelling does not matter: `s.convertStore().Convert(` is as much a second "+
			"path as `store.Convert(`, and the counter door's own narrowing helper makes it the likelier one.",
			callSites)
	}
	if definitions != 1 {
		t.Fatalf("found %d definitions of runConvert, want exactly 1", definitions)
	}
}

// TestConvert_StaffDoorIsGatedOnSellingRoles pins the authority model at the counter door. The yard
// team can edit a quote all day (edit_handler.go says so) and still may not commit the customer's
// credit, the divergence is deliberate and this is where it is enforced.
func TestConvert_StaffDoorIsGatedOnSellingRoles(t *testing.T) {
	allowed := []string{"INSIDE_SALES", "OUTSIDE_SALES", "SALES_MANAGER", "BRANCH_MANAGER",
		"GENERAL_MANAGER", "FINANCIAL_CONTROLLER", identity.RoleSuperAdmin}
	refused := []string{"YARD_TEAM", "DRIVERS", "YARD_MANAGER", "HR", "PROCUREMENT_MANAGER",
		"LOGISTICS_MANAGER", "PAYABLES_RECEIVABLES", ""}

	for _, role := range allowed {
		store := newSeamStore()
		if _, err := staffSvcOver(store, sellerCtx(role)).ConvertQuote(context.Background(), qOrg, ConvertOptions{}); err != nil {
			t.Fatalf("role %s must be able to convert at the counter, got %v", role, err)
		}
	}
	for _, role := range refused {
		store := newSeamStore()
		_, err := staffSvcOver(store, sellerCtx(role)).ConvertQuote(context.Background(), qOrg, ConvertOptions{})
		var notSelling *NotSellingRoleError
		if !errors.As(err, &notSelling) {
			t.Fatalf("role %q must be refused at the counter door, got %v", role, err)
		}
		if len(store.calls) != 0 {
			t.Fatalf("role %q was refused but the conversion seam was still called, the gate runs too late", role)
		}
		if role != "" && !strings.Contains(notSelling.Error(), role) {
			t.Fatalf("refusal for %q does not name the role: %q", role, notSelling.Error())
		}
	}
}

// TestConvert_StaffDoorRefusesOutOfBranch: the counter door still proves branch reach. A selling role
// in the wrong branch is a 403, and the conversion seam is never touched.
func TestConvert_StaffDoorRefusesOutOfBranch(t *testing.T) {
	store := newSeamStore()
	caller := sellerCtx("INSIDE_SALES")
	caller.Branches = map[string]bool{"some-other-branch": true}
	_, err := staffSvcOver(store, caller).ConvertQuote(context.Background(), qOrg, ConvertOptions{})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("out-of-branch seller: got %v, want ErrForbidden", err)
	}
	if len(store.calls) != 0 {
		t.Fatal("an out-of-branch caller reached the conversion seam")
	}
}

// - M3-G7B: the refusal carries its reason through BOTH doors -

// blockedFor builds the credit refusal the walk reproduces: $112,200.00 of stone against a $50,000.00
// limit. The annotations are the ones quote.checkCredit attaches.
func blockedFor() ErrCreditBlocked {
	return ErrCreditBlocked{Blocked: &identity.ActionBlocked{
		Policy: identity.PolicyCredit,
		Detail: "order would put the balance 6220000 cents over the 5000000 cents limit",
		Data: map[string]string{
			"order_total":  "$112,200.00",
			"credit_limit": "$50,000.00",
			"open_balance": "$0.00",
		},
	}}
}

// TestCreditRefusal_CarriesReasonThroughStaffDoor is the counter half of M3-G7B. Before DIB-483 the
// same block answered the literal string "invalid state transition", because ErrCreditBlocked unwraps
// to ErrInvalidTransition and the generic case matched first.
//
// The assertions are on WORDS, not on a code: a 409 with the wrong sentence is the bug.
func TestCreditRefusal_CarriesReasonThroughStaffDoor(t *testing.T) {
	store := newSeamStore()
	store.err = blockedFor()
	h := NewStaffHandler(staffSvcOver(store, sellerCtx("INSIDE_SALES")))

	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(auth.StaffAuthority{RoleID: "inside_sales", Tier: 3, Scope: "branch", Branches: []string{qBranch}})
	mux := http.NewServeMux()
	h.RegisterStaffRoutes(mux, sg.Guards)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/quotes/"+qOrg+"/convert", nil)
	r.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
	r.Header.Set("X-Org", testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (a credit block is a business refusal, not a fault)", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "invalid state transition") {
		t.Fatalf("the refusal regressed to the generic transition message, the errors.As case has been "+
			"reordered below the generic errors.Is case. body: %s", body)
	}
	for _, want := range []string{"$112,200.00", "$50,000.00", "credit limit"} {
		if !strings.Contains(body, want) {
			t.Fatalf("refusal does not carry %q, the reason was dropped. body: %s", want, body)
		}
	}
	if !strings.Contains(body, "no order was created") {
		t.Fatalf("refusal does not say what happened to the order. body: %s", body)
	}
}

// TestCreditRefusal_MessageIsOneFunction pins that both doors render the refusal through the same
// function, so they cannot drift into two different sentences for one event.
func TestCreditRefusal_MessageIsOneFunction(t *testing.T) {
	msg := CreditRefusalMessage(blockedFor())
	if !strings.Contains(msg, "$112,200.00 against a $50,000.00 credit limit") {
		t.Fatalf("plain-words lead missing from %q", msg)
	}
	// the policy detail the type already carries must survive, verbatim.
	if !strings.Contains(msg, "6220000 cents over the 5000000 cents limit") {
		t.Fatalf("the carried policy detail was dropped from %q", msg)
	}

	// the COD leg reads as a different sentence, because it is a different problem.
	cod := CreditRefusalMessage(ErrCreditBlocked{Blocked: &identity.ActionBlocked{
		Policy: identity.PolicyCOD,
		Detail: "customer is on a credit hold; account tender denied (cash or card only)",
	}})
	if !strings.Contains(cod, "cannot buy on account") || !strings.Contains(cod, "credit hold") {
		t.Fatalf("COD refusal does not explain itself: %q", cod)
	}

	// and a verdict-less block still says something useful rather than panicking or going blank.
	if bare := CreditRefusalMessage(ErrCreditBlocked{}); !strings.Contains(bare, "accounts receivable") {
		t.Fatalf("empty verdict produced an unhelpful message: %q", bare)
	}
}

// TestCreditRefusal_StillUnwrapsToInvalidTransition guards the property the fix must NOT break. The
// 409 that every other consumer relies on comes from that unwrap; the fix reorders the mapping, it
// does not change the error's relationship to the sentinel.
func TestCreditRefusal_StillUnwrapsToInvalidTransition(t *testing.T) {
	if !errors.Is(blockedFor(), ErrInvalidTransition) {
		t.Fatal("ErrCreditBlocked no longer unwraps to ErrInvalidTransition, the 409 mapping every " +
			"other consumer depends on has been broken")
	}
}

// - M3-G12: the fulfilment choice -

// TestConvert_FulfillmentDefaultIsByteIdentical is the zero-delta assertion. `want` below is a frozen
// copy of the INSERT. If the fulfilment splice ever starts naming the column when nothing was chosen,
// every existing conversion silently changes shape, and this test is the only thing that would say
// so.
//
// - THE BASELINE MOVED ONCE, DELIBERATELY (0194, session B) -
//
// `want` was frozen against the pre-DIB-483 statement and held byte-for-byte until 0194 added the
// freight pair, `freight_cents` (the opening actual) and `quoted_freight_cents` (the frozen
// baseline the second-truck approval gate measures against). Both were added in the SAME commit as
// this string, which is the only honest way to move a frozen copy: the test did its job, it refused
// the change until somebody came here and said why, and weakening or deleting it instead would have
// thrown away the guard for every future edit.
//
// It moved a second time for UX-95: org_id (the branch's org, a subselect on locations) joined the pair, spliced
// before the fulfilment points like the freight pair, in the same commit as the statement.
//
// What it still pins, unchanged: the fulfilment splice contributes NOTHING when no fulfilment is
// chosen, and the column list and the SELECT list pair POSITIONALLY, with the fulfilment splice last
// in both. The two new columns sit before the splice points for exactly that reason.
func TestConvert_FulfillmentDefaultIsByteIdentical(t *testing.T) {
	const want = `INSERT INTO orders (order_no, customer_id, branch_id, quote_id, subtotal_cents, tax_cents, total_cents, freight_cents, quoted_freight_cents, org_id)
		 SELECT next_order_no(branch_id), customer_id, branch_id, id, subtotal_cents, tax_cents, total_cents, delivery_fee_cents, delivery_fee_cents,
		        (SELECT l.org_id FROM locations l WHERE l.id = quotes.branch_id)
		 FROM quotes WHERE id=$2 AND customer_id=$1
		 RETURNING id::text, order_no::text`

	cols, vals, err := fulfillmentColumns("")
	if err != nil {
		t.Fatalf("unspecified fulfilment errored: %v", err)
	}
	if cols != "" || vals != "" {
		t.Fatalf("unspecified fulfilment produced SQL fragments %q / %q, it must produce none", cols, vals)
	}
	if got := convertInsertSQL(cols, vals); got != want {
		t.Fatalf("the conversion INSERT is no longer byte-identical when no fulfilment is chosen.\n"+
			"got:\n%s\nwant:\n%s", got, want)
	}
}

// TestConvert_FulfillmentChoiceReachesTheSQL: the other half, a chosen fulfilment DOES name the
// column, with the constant and not with caller text.
func TestConvert_FulfillmentChoiceReachesTheSQL(t *testing.T) {
	for _, tc := range []struct{ choice, wantVal string }{
		{FulfillPickup, "'PICKUP'"},
		{FulfillWillCall, "'WILL_CALL'"},
	} {
		cols, vals, err := fulfillmentColumns(tc.choice)
		if err != nil {
			t.Fatalf("%s: %v", tc.choice, err)
		}
		if cols != ", fulfillment" {
			t.Fatalf("%s: column fragment = %q", tc.choice, cols)
		}
		if vals != ", "+tc.wantVal {
			t.Fatalf("%s: value fragment = %q, want %q", tc.choice, vals, ", "+tc.wantVal)
		}
		// THE CLAIM IS "THE SPLICE LANDS LAST IN BOTH LISTS", not "it lands after total_cents".
		// This used to assert the literal `total_cents, fulfillment)`, which was the same claim only
		// for as long as total_cents happened to be the last column, and 0194 put the freight pair
		// after it. Asserting the POSITION rather than its neighbour keeps the guard true through the
		// next column too, and it is the property fulfillmentColumns actually depends on: the column
		// list and the SELECT list are paired positionally, so a splice that landed anywhere but the
		// end of both would put a chosen fulfilment into the wrong column.
		sql := convertInsertSQL(cols, vals)
		if !strings.Contains(sql, ", fulfillment)") {
			t.Fatalf("%s: the fulfilment column is not last in the column list:\n%s", tc.choice, sql)
		}
		if !strings.Contains(sql, ", "+tc.wantVal+"\n") {
			t.Fatalf("%s: the fulfilment value is not last in the SELECT list:\n%s", tc.choice, sql)
		}
		// And the two lists must still have the SAME number of members, which is the invariant a
		// positional pairing lives or dies by.
		head := sql[strings.Index(sql, "(")+1 : strings.Index(sql, ")")]
		sel := sql[strings.Index(sql, "SELECT ")+len("SELECT ") : strings.Index(sql, "\n\t\t FROM quotes")]
		if got, want := strings.Count(sel, ",")+1, strings.Count(head, ",")+1; got != want {
			t.Fatalf("%s: %d columns but %d values, a positional pairing that does not pair:\n%s",
				tc.choice, want, got, sql)
		}
	}
}

// TestConvert_FulfillmentNormalization covers what a human may type into the control and what the
// system must refuse. The DELIVERY refusal is the interesting one: it is not "invalid", it points at
// the surface that actually books a delivery.
func TestConvert_FulfillmentNormalization(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"PICKUP", FulfillPickup},
		{"pickup", FulfillPickup},
		{" Will_Call ", FulfillWillCall},
		{"will call", FulfillWillCall},
		{"will-call", FulfillWillCall},
	} {
		got, err := normalizeFulfillment(tc.in)
		if err != nil {
			t.Fatalf("%q: unexpected error %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("%q normalized to %q, want %q", tc.in, got, tc.want)
		}
	}
	if _, err := normalizeFulfillment("DELIVERY"); err == nil ||
		!strings.Contains(err.Error(), "scheduling a stop") {
		t.Fatalf("DELIVERY must be refused with a pointer to dispatch, got %v", err)
	}
	if _, err := normalizeFulfillment("banana"); err == nil ||
		!strings.Contains(err.Error(), "WILL_CALL") {
		t.Fatalf("a nonsense fulfilment must name the legal answers, got %v", err)
	}
}

// TestConvert_FulfillmentFlowsThroughBothDoors: the choice is not a portal-only field. The counter
// carries the same control, and both deliver it to the same seam unchanged.
func TestConvert_FulfillmentFlowsThroughBothDoors(t *testing.T) {
	ctx := context.Background()

	store := newSeamStore()
	if _, err := NewService(store).Convert(ctx, qCust, qOrg, ConvertOptions{Fulfillment: "will call"}); err != nil {
		t.Fatalf("portal door: %v", err)
	}
	if _, err := staffSvcOver(store, sellerCtx("INSIDE_SALES")).
		ConvertQuote(ctx, qOrg, ConvertOptions{Fulfillment: "Will-Call"}); err != nil {
		t.Fatalf("staff door: %v", err)
	}
	for i, c := range store.calls {
		if c.opt.Fulfillment != FulfillWillCall {
			t.Fatalf("call %d reached the seam with fulfillment %q, want %q (normalized once, in runConvert)",
				i, c.opt.Fulfillment, FulfillWillCall)
		}
	}

	// and an unspecified conversion stays unspecified all the way down, this is what makes the
	// zero-delta property reachable from the doors, not just from the SQL builder.
	blank := newSeamStore()
	if _, err := NewService(blank).Convert(ctx, qCust, qOrg, ConvertOptions{}); err != nil {
		t.Fatalf("unspecified portal convert: %v", err)
	}
	if blank.calls[0].opt.Fulfillment != "" {
		t.Fatalf("an unspecified conversion arrived at the seam as %q, the default leaked in",
			blank.calls[0].opt.Fulfillment)
	}
}

// TestConvert_BadFulfillmentRefusedBeforeTheWrite: a bad choice is a 400 and NOTHING is written. At
// both doors, with the same words.
func TestConvert_BadFulfillmentRefusedBeforeTheWrite(t *testing.T) {
	ctx := context.Background()
	portalStore := newSeamStore()
	_, perr := NewService(portalStore).Convert(ctx, qCust, qOrg, ConvertOptions{Fulfillment: "TELEPORT"})
	staffStoreV := newSeamStore()
	_, serr := staffSvcOver(staffStoreV, sellerCtx("INSIDE_SALES")).
		ConvertQuote(ctx, qOrg, ConvertOptions{Fulfillment: "TELEPORT"})

	if perr == nil || serr == nil {
		t.Fatalf("a nonsense fulfilment was accepted (portal=%v staff=%v)", perr, serr)
	}
	if perr.Error() != serr.Error() {
		t.Fatalf("the two doors refuse differently:\n  portal: %v\n  staff:  %v", perr, serr)
	}
	if len(portalStore.calls) != 0 || len(staffStoreV.calls) != 0 {
		t.Fatal("a refused fulfilment still reached the conversion seam")
	}
}

// TestConvert_OfferMatchesTheWritePath is the client-affordance guarantee: the surface must never
// offer a Convert button the server would refuse, and must never hide one it would honour.
//
// The failure this prevents is the ordinary one, an authority model mirrored into a browser, which
// then has two versions of itself. StaffQuoteView.CanConvert is computed from the SAME predicate
// ConvertQuote enforces, and this test drives BOTH over the same callers and asserts they agree on
// every cell.
func TestConvert_OfferMatchesTheWritePath(t *testing.T) {
	roles := []string{"INSIDE_SALES", "OUTSIDE_SALES", "SALES_MANAGER", "BRANCH_MANAGER",
		"GENERAL_MANAGER", "FINANCIAL_CONTROLLER", identity.RoleSuperAdmin,
		"YARD_TEAM", "DRIVERS", "YARD_MANAGER", "HR", "PROCUREMENT_MANAGER", ""}

	for _, role := range roles {
		store := newSeamStore()
		svc := staffSvcOver(store, sellerCtx(role))

		view, err := svc.StaffQuoteRead(context.Background(), qOrg)
		if err != nil {
			t.Fatalf("role %q: read: %v", role, err)
		}
		_, writeErr := svc.ConvertQuote(context.Background(), qOrg, ConvertOptions{})
		writeAllowed := writeErr == nil

		if view.CanConvert != writeAllowed {
			t.Fatalf("role %q: the surface offers convert=%v but the write path says %v (%v). "+
				"An offer the server refuses is a button that lies; a hidden offer the server would "+
				"honour is a feature nobody finds.", role, view.CanConvert, writeAllowed, writeErr)
		}
	}
}

// TestConvert_OfferIsFalseOnAnUnconvertibleQuote: the offer tracks STATUS too, not just the role.
func TestConvert_OfferIsFalseOnAnUnconvertibleQuote(t *testing.T) {
	for _, status := range []string{"DRAFT", "SENT", "CONVERTED", "LOST"} {
		store := newSeamStore()
		store.fakeStaffStore = newFakeStaffStore(status)
		view, err := staffSvcOver(store, sellerCtx("INSIDE_SALES")).
			StaffQuoteRead(context.Background(), qOrg)
		if err != nil {
			t.Fatalf("%s: read: %v", status, err)
		}
		if view.CanConvert {
			t.Fatalf("a %s quote must not offer conversion, only an ACCEPTED one converts", status)
		}
	}
}
