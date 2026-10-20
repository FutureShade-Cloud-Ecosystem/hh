package main

import (
	"context"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messaging"
)

// slugRecorder is the messaging post, recording the room each alert lands in.
type slugRecorder struct{ slugs, bodies []string }

func (r *slugRecorder) PostEventToChannelSlug(_ context.Context, slug, _ string, body string, _ messaging.EventRef, _ bool) (messaging.Message, bool, error) {
	r.slugs = append(r.slugs, slug)
	r.bodies = append(r.bodies, body)
	return messaging.Message{}, true, nil
}

// HH-424 round 1: the card attempt sweep's alert carries the attempt's amount and sale key. It must land
// in a room fenced by the payment trail (#finance carries a member_roles list), never in #escalations,
// which is a GLOBAL room every active staffer holds.
func TestCardAttemptAlert_LandsInAFencedMoneyRoom(t *testing.T) {
	rec := &slugRecorder{}
	body := "card charge attempt 1a2b3c4d is UNKNOWN: amount 124000 cents, branch 5d1b3f6e, till session 9f8e7d6c, sale key 0a1b2c3d"
	if err := (cardAttemptEscalations{svc: rec}).post(context.Background(), "card-attempt:x:UNKNOWN", body); err != nil {
		t.Fatalf("post: %v", err)
	}
	if len(rec.slugs) != 1 {
		t.Fatalf("one post expected, got %v", rec.slugs)
	}
	if rec.slugs[0] != "finance" {
		t.Fatalf("the alert names an amount and a sale key and must land in #finance, got #%s", rec.slugs[0])
	}
	if !strings.Contains(rec.bodies[0], "124000") {
		t.Fatalf("the money desk still gets the facts: %q", rec.bodies[0])
	}
}
