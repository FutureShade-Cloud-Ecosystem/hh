// yard_notice_fence_test.go, the order rep notice's words fence: a notice
// whose body names money figures rides with its figures only to the rep's
// own DM (N2, the rep's role on the cost and margin list) and to the
// counter channel (#sales-<branch>, whose derived membership is the sales
// floor alone, a cost list audience for OUTSIDE_SALES today); the office
// channel (#office, every active staff member) and the yard channel
// (#yard-<branch>, every role granted the branch) receive the same words
// with the figures left out (ruling 91: a figure never reaches an off list
// role, in cents or in words). Driven through the yardChatMessenger double
// so CI's databaseless ci job carries the routing; the channels' real
// membership is the migrations' seed (0052, 0076) and the derived membership
// rule, stated on orderRepFallbackRoute.
package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messaging"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tasks"
)

// fakeYardChat is the messaging double: a yard slug per branch, a rep answer
// of the caller's choosing, a channel post recorder that can refuse named
// slugs the way a missing channel would, and a per-user role code so the
// rep DM fence's UserRoleCode call has something to read.
type fakeYardChat struct {
	yardSlug string
	repKind  string // messaging.RouteRepDM, RouteNone, or "" with repErr set
	repErr   error
	fail     map[string]error
	posts    []fakeChannelPost
	// The double routes the way messaging.NotifyOrderRep does: an order
	// override rep first, then the notice's salesperson, then the customer's
	// rep, and no rep at all is RouteNone. dmUser and dmBody record where the
	// DM went and the words it carried.
	overrideRep string
	customerRep string
	// roleByUser answers the role lookup in the canonical form the real
	// UserRoleCode returns ("outside_sales", not the ERP "OUTSIDE_SALES");
	// a user not listed has no role (empty, fail closed).
	roleByUser map[string]string
	dmUser     string
	dmBody     string
	dmCalls    int
}

type fakeChannelPost struct {
	slug string
	body string
}

func (f *fakeYardChat) YardSlugForBranch(_ context.Context, _ string) (string, error) {
	return f.yardSlug, nil
}

func (f *fakeYardChat) NotifyOrderRep(_ context.Context, in messaging.OrderRepNotice) (messaging.OrderRepResult, error) {
	if f.repErr != nil {
		return messaging.OrderRepResult{}, f.repErr
	}
	if f.repKind != messaging.RouteRepDM {
		return messaging.OrderRepResult{Kind: f.repKind}, nil
	}
	rep := f.overrideRep
	if rep == "" {
		rep = in.OrderRepUserID
	}
	if rep == "" {
		rep = f.customerRep
	}
	body := in.Body
	if in.BodyNoFigures != "" {
		body = in.BodyNoFigures
		if in.RoleSeesFigures != nil && f.roleByUser[rep] != "" && in.RoleSeesFigures(f.roleByUser[rep]) {
			body = in.Body
		}
	}
	f.dmCalls++
	f.dmUser, f.dmBody = rep, body
	return messaging.OrderRepResult{Kind: messaging.RouteRepDM, RepUserID: rep}, nil
}

func (f *fakeYardChat) PostEventToChannelSlug(_ context.Context, slug, _ string, body string, _ messaging.EventRef, _ bool) (messaging.Message, bool, error) {
	if err, ok := f.fail[slug]; ok {
		return messaging.Message{}, false, err
	}
	f.posts = append(f.posts, fakeChannelPost{slug: slug, body: body})
	return messaging.Message{}, true, nil
}

// defaultFiguresOK reads roleconfig.Default() so the test factory wires a
// real rule (N2 asks it, fail closed on unwired). Existing legs relied on a
// hardcoded `figures: true` for the counter; the rule's default list
// includes OUTSIDE_SALES, so today's behaviour holds.
func defaultFiguresOK(t *testing.T) func(string) (bool, error) {
	t.Helper()
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatal(err)
	}
	return cfg.SeesCostAndMargin
}

// newNotifier wires a yardChatNotifier with the default figuresOK so the
// existing legs continue to pass with the rule rather than the hardcoded
// true; the N2 leg overrides the closure to read a configuration with
// OUTSIDE_SALES off the list.
func newNotifier(t *testing.T, chat *fakeYardChat) yardChatNotifier {
	return yardChatNotifier{svc: chat, figuresOK: defaultFiguresOK(t)}
}

// fenceRef is one rep notice carrying both bodies: the rep's copy names the
// figures, the second body keeps the words and leaves them out.
func fenceRef() tasks.TaskEventRef {
	return tasks.TaskEventRef{
		EventKey: "yard.skids:o-1:c-1", BranchID: "b-1", Topic: "yard.skid_change",
		Entity: "order:o-1", Summary: "Skid count on order #SO-1 changed from 3 to 2 skids. The skid deposit is now $100.00 (was $150.00).",
		SummaryNoFigures: "Skid count on order #SO-1 changed from 3 to 2 skids. The skid deposit changed.",
		Audience:         tasks.AudienceOrderRep, OrderID: "o-1",
	}
}

func TestOrderRepNotice_RepDMCarriesTheFiguresAndNothingIsPostedToAChannel(t *testing.T) {
	chat := &fakeYardChat{yardSlug: "yard-trenton", repKind: messaging.RouteRepDM, roleByUser: map[string]string{"u-rep-1": "outside_sales"}}
	n := newNotifier(t, chat)
	if err := n.PostTaskEvent(context.Background(), fenceRef()); err != nil {
		t.Fatalf("the rep route delivers: %v", err)
	}
	if len(chat.posts) != 0 {
		t.Fatalf("a delivered rep notice posts to no channel: %+v", chat.posts)
	}
}

func TestOrderRepNotice_NoRepKeepsTheFiguresForTheCounterChannelOnly(t *testing.T) {
	chat := &fakeYardChat{yardSlug: "yard-trenton", repKind: messaging.RouteNone}
	n := newNotifier(t, chat)
	if err := n.PostTaskEvent(context.Background(), fenceRef()); err != nil {
		t.Fatalf("the fallback delivers: %v", err)
	}
	if len(chat.posts) != 1 || chat.posts[0].slug != "sales-trenton" {
		t.Fatalf("the counter channel is the first fallback and takes the notice: %+v", chat.posts)
	}
	body := chat.posts[0].body
	if !strings.Contains(body, orderRepFallbackNote) || !strings.Contains(body, "$100.00") || !strings.Contains(body, "$150.00") {
		t.Fatalf("the counter channel seats the sales floor, a cost list audience, and keeps the figures:\n%s", body)
	}
}

func TestOrderRepNotice_TheOfficeAndYardChannelsGetTheWordsWithoutTheFigures(t *testing.T) {
	// No rep, and the counter channel is unreachable: the notice walks to
	// #office, whose reader is every active staff member.
	chat := &fakeYardChat{yardSlug: "yard-trenton", repKind: messaging.RouteNone,
		fail: map[string]error{"sales-trenton": errors.New("no such channel")}}
	n := newNotifier(t, chat)
	if err := n.PostTaskEvent(context.Background(), fenceRef()); err != nil {
		t.Fatalf("the fallback delivers: %v", err)
	}
	if len(chat.posts) != 1 || chat.posts[0].slug != "office" {
		t.Fatalf("#office is the second fallback: %+v", chat.posts)
	}
	body := chat.posts[0].body
	if strings.Contains(body, "$") || strings.Contains(body, "100.00") || strings.Contains(body, "150.00") {
		t.Fatalf("#office seats every active staff member and must not carry a figure:\n%s", body)
	}
	for _, want := range []string{orderRepFallbackNote, "changed from 3 to 2 skids", "The skid deposit changed."} {
		if !strings.Contains(body, want) {
			t.Errorf("the figureless copy keeps the words (%q):\n%s", want, body)
		}
	}

	// The yard channel is the last fallback, and it seats every role granted
	// the branch: figureless there too.
	chat = &fakeYardChat{yardSlug: "yard-trenton", repKind: messaging.RouteNone,
		fail: map[string]error{"sales-trenton": errors.New("no such channel"), "office": errors.New("no such channel")}}
	n = newNotifier(t, chat)
	if err := n.PostTaskEvent(context.Background(), fenceRef()); err != nil {
		t.Fatalf("the fallback delivers: %v", err)
	}
	if len(chat.posts) != 1 || chat.posts[0].slug != "yard-trenton" {
		t.Fatalf("the yard channel is the last fallback: %+v", chat.posts)
	}
	if body := chat.posts[0].body; strings.Contains(body, "$") {
		t.Fatalf("the yard channel seats every role granted the branch and must not carry a figure:\n%s", body)
	}
}

func TestOrderRepNotice_AFailedRepRouteSaysSoAndFencesTheSameWay(t *testing.T) {
	chat := &fakeYardChat{yardSlug: "yard-trenton", repErr: errors.New("messaging is down"),
		fail: map[string]error{"sales-trenton": errors.New("no such channel")}}
	n := newNotifier(t, chat)
	if err := n.PostTaskEvent(context.Background(), fenceRef()); err != nil {
		t.Fatalf("the fallback delivers: %v", err)
	}
	if len(chat.posts) != 1 || chat.posts[0].slug != "office" {
		t.Fatalf("a failed rep route walks the same chain: %+v", chat.posts)
	}
	body := chat.posts[0].body
	if !strings.Contains(body, unreachableRepNote) || strings.Contains(body, "$") {
		t.Fatalf("a failed rep route says so and still fences the figures:\n%s", body)
	}
}

// A notice that names no figure has no second body, and every channel reads
// its own words: the fence must not blank such a notice.
func TestOrderRepNotice_AMoneylessNoticeReadsTheSameEverywhere(t *testing.T) {
	ref := fenceRef()
	ref.Summary = "Order #SO-1 is STAGED at bay 3, ready for the customer."
	ref.SummaryNoFigures = ""
	chat := &fakeYardChat{yardSlug: "yard-trenton", repKind: messaging.RouteNone,
		fail: map[string]error{"sales-trenton": errors.New("no such channel")}}
	n := newNotifier(t, chat)
	if err := n.PostTaskEvent(context.Background(), ref); err != nil {
		t.Fatalf("the fallback delivers: %v", err)
	}
	if len(chat.posts) != 1 || chat.posts[0].slug != "office" {
		t.Fatalf("#office takes the notice: %+v", chat.posts)
	}
	if body := chat.posts[0].body; !strings.Contains(body, "STAGED at bay 3") {
		t.Fatalf("a moneyless notice keeps its own words on every channel:\n%s", body)
	}
}

// fenceRefWithRep returns the same notice with a RepUserID stamped so the
// rep DM path runs (and the figuresOK closure has a role to ask about).
func fenceRefWithRep() tasks.TaskEventRef {
	r := fenceRef()
	r.RepUserID = "u-rep-1"
	return r
}

// counterAudienceRoleOut figuresOK: every role except the counter audience
// (the canonical "outside_sales") answers yes. Mirrors a dealer
// configuration that takes the counter audience off the cost and margin
// list while keeping every other role. The brief names OUTSIDE_SALES as
// the seated role; the rule's vocabulary is the canonical lowercase, and
// UserRoleCode normalizes its answer to the same canonical, so the gate
// here uses the canonical id.
func counterAudienceRoleOut() func(string) (bool, error) {
	off := map[string]bool{counterAudienceRole: true}
	return func(role string) (bool, error) {
		if off[role] {
			return false, nil
		}
		// Empty role (a not-found user): refuse the figures (fail closed).
		if role == "" {
			return false, nil
		}
		return true, nil
	}
}

// N2 (P3): a dealer configuration that takes OUTSIDE_SALES off the cost
// and margin list flips the counter copy to figureless: the rule the prior
// round hardcoded as `figures: true` is now asked (N2), and the answer is
// no, so the chain walks to #office carrying the figureless twin. The
// CRITICAL leg for N2 because it is the fail open direction the brief
// names: every other channel in the chain is figureless already, so the
// behavior the deck would notice is the counter going figureless too.
func TestOrderRepNotice_CounterAudienceRoleOffList_CounterCopiesGoFigureless(t *testing.T) {
	chat := &fakeYardChat{yardSlug: "yard-trenton", repKind: messaging.RouteNone}
	n := yardChatNotifier{svc: chat, figuresOK: counterAudienceRoleOut()}
	if err := n.PostTaskEvent(context.Background(), fenceRef()); err != nil {
		t.Fatalf("the fallback delivers: %v", err)
	}
	if len(chat.posts) != 1 || chat.posts[0].slug != "sales-trenton" {
		t.Fatalf("the counter channel is the first fallback: %+v", chat.posts)
	}
	body := chat.posts[0].body
	for _, leak := range []string{"$", "100.00", "150.00"} {
		if strings.Contains(body, leak) {
			t.Errorf("OUTSIDE_SALES off the list: counter copy must be figureless (%q in %q)", leak, body)
		}
	}
	for _, want := range []string{orderRepFallbackNote, "The skid deposit changed."} {
		if !strings.Contains(body, want) {
			t.Errorf("figureless twin keeps the words (%q in %q)", want, body)
		}
	}
}

// repDMBodyLeg drives one rep DM through the double and returns the body the
// DM carried. The double answers the role in the canonical form the real
// UserRoleCode returns ("outside_sales", never the ERP "OUTSIDE_SALES"), and
// the decision runs in the double exactly as it does in messaging, over the
// notice's own two bodies and role predicate.
func repDMBodyLeg(t *testing.T, role string, figuresOK func(string) (bool, error)) (string, *fakeYardChat) {
	t.Helper()
	users := map[string]string{}
	if role != "" {
		users["u-rep-1"] = role
	}
	chat := &fakeYardChat{yardSlug: "yard-trenton", repKind: messaging.RouteRepDM, roleByUser: users}
	n := yardChatNotifier{svc: chat, figuresOK: figuresOK}
	if err := n.PostTaskEvent(context.Background(), fenceRefWithRep()); err != nil {
		t.Fatalf("the rep route delivers: %v", err)
	}
	if len(chat.posts) != 0 {
		t.Fatalf("a delivered rep notice posts to no channel: %+v", chat.posts)
	}
	if chat.dmCalls != 1 || chat.dmUser != "u-rep-1" {
		t.Fatalf("one DM to the rep: calls %d, to %q", chat.dmCalls, chat.dmUser)
	}
	return chat.dmBody, chat
}

func wantRepDMFigureless(t *testing.T, body string) {
	t.Helper()
	for _, leak := range []string{"$", "100.00", "150.00"} {
		if strings.Contains(body, leak) {
			t.Errorf("the rep DM must be figureless (%q in %q)", leak, body)
		}
	}
	if !strings.Contains(body, "The skid deposit changed.") {
		t.Errorf("the figureless rep DM keeps the words: %q", body)
	}
}

// N2 (P3): a rep whose role is off the cost and margin list gets the
// figureless twin as the DM body, not only "no channel post".
func TestOrderRepNotice_RepDMRoleOffListFigureless(t *testing.T) {
	body, _ := repDMBodyLeg(t, "driver", defaultFiguresOK(t))
	wantRepDMFigureless(t, body)
}

// A rep whose role reads back empty (no active role) is judged off the list.
func TestOrderRepNotice_RepDMRoleUnknownFigureless(t *testing.T) {
	body, _ := repDMBodyLeg(t, "", defaultFiguresOK(t))
	wantRepDMFigureless(t, body)
}

// Ruling 91's other half: a rep on the list keeps the figures in the DM. A
// fence that made every rep figureless would pass every leg above and fail here.
func TestOrderRepNotice_RepDMListedRepKeepsTheFigures(t *testing.T) {
	body, _ := repDMBodyLeg(t, "outside_sales", defaultFiguresOK(t))
	for _, want := range []string{"$100.00", "$150.00"} {
		if !strings.Contains(body, want) {
			t.Errorf("a listed rep keeps the figures (%q missing from %q)", want, body)
		}
	}
}

// N2 (P3): the fence fails closed when SeesCostAndMargin errors. The
// figureless twin rides the rep DM, never a figured sentence to a reader whose
// clearance the rule could not answer, even for a role the list would admit.
func TestOrderRepNotice_FigurelessWhenRuleErrors(t *testing.T) {
	broken := func(string) (bool, error) { return true, errors.New("config unavailable") }
	body, _ := repDMBodyLeg(t, "outside_sales", broken)
	wantRepDMFigureless(t, body)
}

// N2 (P3): with every role refused and no rep to DM, the whole fallback chain
// is figureless.
func TestOrderRepNotice_RepDMFigurelessBodyWhenRuleDisallows(t *testing.T) {
	// Every role refused: with no rep to DM the chain runs and every copy
	// is the figureless twin.
	refused := func(string) (bool, error) { return false, nil }
	chat := &fakeYardChat{yardSlug: "yard-trenton", repKind: messaging.RouteNone, roleByUser: map[string]string{"u-rep-1": "outside_sales"}}
	n := yardChatNotifier{svc: chat, figuresOK: refused}
	if err := n.PostTaskEvent(context.Background(), fenceRefWithRep()); err != nil {
		t.Fatalf("the fallback delivers: %v", err)
	}
	// The rep route answered RouteNone, so the fallback chain runs.
	if len(chat.posts) != 1 || chat.posts[0].slug != "sales-trenton" {
		t.Fatalf("the chain ran with the rule refused: %+v", chat.posts)
	}
	for _, leak := range []string{"$", "100.00", "150.00"} {
		if strings.Contains(chat.posts[0].body, leak) {
			t.Errorf("the rule refused: counter is figureless (%q in %q)", leak, chat.posts[0].body)
		}
	}
}

// N2 (P3): a dealer that takes the rep's own role off the list sends that rep
// the figureless twin, though the role is the one the default list admits.
func TestOrderRepNotice_RepDMReceivesFigurelessBodyWhenRuleOffList(t *testing.T) {
	body, _ := repDMBodyLeg(t, counterAudienceRole, counterAudienceRoleOut())
	wantRepDMFigureless(t, body)
}
