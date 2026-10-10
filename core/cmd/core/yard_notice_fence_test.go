// yard_notice_fence_test.go, the order rep notice's words fence: a notice
// whose body names money figures rides with its figures only to the rep's
// own DM and to the counter channel (#sales-<branch>, whose derived
// membership is the sales floor alone, a cost list audience); the office
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
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tasks"
)

// fakeYardChat is the messaging double: a yard slug per branch, a rep answer
// of the caller's choosing, and a channel post recorder that can refuse
// named slugs the way a missing channel would.
type fakeYardChat struct {
	yardSlug string
	repKind  string // messaging.RouteRepDM, RouteNone, or "" with repErr set
	repErr   error
	fail     map[string]error
	posts    []fakeChannelPost
}

type fakeChannelPost struct {
	slug string
	body string
}

func (f *fakeYardChat) YardSlugForBranch(_ context.Context, _ string) (string, error) {
	return f.yardSlug, nil
}

func (f *fakeYardChat) NotifyOrderRep(_ context.Context, _ messaging.OrderRepNotice) (messaging.OrderRepResult, error) {
	if f.repErr != nil {
		return messaging.OrderRepResult{}, f.repErr
	}
	return messaging.OrderRepResult{Kind: f.repKind}, nil
}

func (f *fakeYardChat) PostEventToChannelSlug(_ context.Context, slug, _ string, body string, _ messaging.EventRef, _ bool) (messaging.Message, bool, error) {
	if err, ok := f.fail[slug]; ok {
		return messaging.Message{}, false, err
	}
	f.posts = append(f.posts, fakeChannelPost{slug: slug, body: body})
	return messaging.Message{}, true, nil
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
	chat := &fakeYardChat{yardSlug: "yard-trenton", repKind: messaging.RouteRepDM}
	n := yardChatNotifier{svc: chat}
	if err := n.PostTaskEvent(context.Background(), fenceRef()); err != nil {
		t.Fatalf("the rep route delivers: %v", err)
	}
	if len(chat.posts) != 0 {
		t.Fatalf("a delivered rep notice posts to no channel: %+v", chat.posts)
	}
}

func TestOrderRepNotice_NoRepKeepsTheFiguresForTheCounterChannelOnly(t *testing.T) {
	chat := &fakeYardChat{yardSlug: "yard-trenton", repKind: messaging.RouteNone}
	n := yardChatNotifier{svc: chat}
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
	n := yardChatNotifier{svc: chat}
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
	n = yardChatNotifier{svc: chat}
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
	n := yardChatNotifier{svc: chat}
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
	n := yardChatNotifier{svc: chat}
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
