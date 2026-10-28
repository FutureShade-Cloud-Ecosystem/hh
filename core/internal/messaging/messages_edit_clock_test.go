package messaging

import (
	"context"
	"testing"
	"time"
)

// msgClockFresh is the probe row the clock legs plant, bound to the caller's
// own channel.
const msgClockFresh = "f1000000-0000-4000-9000-0000000000b1"

// UX-109: the edit window is measured on the DATABASE's clock.
//
// The window compares a message's created_at (the database's stamp) with the
// clock the service reads. When that clock is the app's, an app whose clock
// runs ahead of the database's closes the window on a message the database
// says is fresh, and the author cannot edit or delete their own words.
//
// The leg plants the facts' own clock (the database's, at the read) and pins
// the app's clock two hours ahead of it. The window must open on the clock the
// facts carry.
func TestEditMessage_WindowRunsOnTheClocksTheFactsCarry(t *testing.T) {
	f := newFakeStore()
	f.setMember(chGen, uAlice)
	dbNow := time.Now()
	f.editFacts = map[string]messageFacts{
		msgClockFresh: {
			ChannelID: chGen, AuthorID: uAlice, Kind: MsgText,
			CreatedAt: dbNow, Now: dbNow,
		},
	}
	// The app's clock runs two hours ahead of the database's.
	svc := svcWith(f).WithClock(func() time.Time { return dbNow.Add(2 * time.Hour) })

	if _, err := svc.EditMessage(context.Background(), Caller{UserID: uAlice}, chGen, msgClockFresh, "counted again"); err != nil {
		t.Fatalf("a message the database says is fresh must still be editable when the app's clock runs ahead: %v", err)
	}
}

// The same window on the delete: a tombstone is the author's own pen too.
func TestDeleteMessage_WindowRunsOnTheClocksTheFactsCarry(t *testing.T) {
	f := newFakeStore()
	f.setMember(chGen, uAlice)
	dbNow := time.Now()
	f.editFacts = map[string]messageFacts{
		msgClockFresh: {
			ChannelID: chGen, AuthorID: uAlice, Kind: MsgText,
			CreatedAt: dbNow, Now: dbNow,
		},
	}
	svc := svcWith(f).WithClock(func() time.Time { return dbNow.Add(2 * time.Hour) })

	if _, err := svc.DeleteMessage(context.Background(), Caller{UserID: uAlice}, chGen, msgClockFresh); err != nil {
		t.Fatalf("a message the database says is fresh must still be deletable when the app's clock runs ahead: %v", err)
	}
}

// A message the DATABASE says is stale stays outside the window: the fix moves
// the clock the window is measured on, it does not open the window.
func TestEditMessage_WindowStillClosesOnTheDatabasesStaleMessage(t *testing.T) {
	f := newFakeStore()
	f.setMember(chGen, uAlice)
	dbNow := time.Now()
	f.editFacts = map[string]messageFacts{
		msgClockFresh: {
			ChannelID: chGen, AuthorID: uAlice, Kind: MsgText,
			CreatedAt: dbNow.Add(-2 * time.Hour), Now: dbNow,
		},
	}
	// The app's clock is behind the database's here, and still cannot open the window.
	svc := svcWith(f).WithClock(func() time.Time { return dbNow.Add(-2 * time.Hour) })

	if _, err := svc.EditMessage(context.Background(), Caller{UserID: uAlice}, chGen, msgClockFresh, "too late"); err != ErrEditWindowClosed {
		t.Fatalf("a stale message must answer the window wall, got %v", err)
	}
}
