package messaging

import (
	"context"
	"testing"
	"time"
)

// UX-109: the read cursor is the DATABASE's clock.
//
// The rows the cursor is compared against (msg_messages.created_at) are stamped by the database, so
// the cursor must be stamped by the database too. An app-clock stamp puts a second clock into the
// same comparison, and when the app's clock runs ahead of the database's, a message posted in that
// gap carries a created_at OLDER than the cursor, so the unread math calls the message read and the
// badge stays dark.
//
// The leg pins the application's clock and counts how often the mark-read path reads it. A path that
// asks the app for a time cannot be a path that takes the database's clock.
func TestMarkRead_ReadsNoClockOfItsOwn(t *testing.T) {
	f := newFakeStore()
	f.setMember(chGen, uAlice)
	reads := 0
	svc := svcWith(f).WithClock(func() time.Time { reads++; return time.Now() })

	if err := svc.MarkRead(context.Background(), Caller{UserID: uAlice}, chGen); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	if reads != 0 {
		t.Fatalf("the read cursor must not read the application's clock, the clock was read %d time(s)", reads)
	}
	if len(f.markCalls) != 1 || f.markCalls[0] != chGen+"|"+uAlice {
		t.Fatalf("mark read should have reached the store once, got %v", f.markCalls)
	}
}
