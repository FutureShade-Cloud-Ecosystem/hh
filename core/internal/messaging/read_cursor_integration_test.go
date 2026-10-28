//go:build integration

package messaging

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// UX-109: the read cursor must be the DATABASE's clock.
//
// The application's clock runs an hour ahead of the database's. A message posted in that gap
// carries a created_at (the database's clock) that is OLDER than a cursor taken from the
// application's clock, so the unread math calls the message read and the badge stays dark. The
// cursor must be the database's own clock, taken in the same statement that writes it, so the
// comparison is between two stamps of ONE clock.
func TestIntegration_ReadCursor_FollowsTheDatabaseClock(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	for _, u := range []string{uSamRivera, uAlexMorgan} {
		if err := repo.SyncMembership(ctx, u); err != nil {
			t.Fatal(err)
		}
	}

	// The fixture starts from the database's own clock, and is handed back the database's own clock
	// when the leg is done: the cursor never moves back, so a leg that left it an hour ahead would
	// strand the rows that follow.
	resetCursor := func() {
		if _, err := pool.Exec(ctx,
			`UPDATE msg_members SET last_read_at = now() WHERE channel_id = $1 AND user_id = $2`,
			chOffice, uSamRivera); err != nil {
			t.Errorf("reset the read cursor: %v", err)
		}
	}
	resetCursor()
	t.Cleanup(resetCursor)

	// The database's clock, and the application's clock an hour ahead of it.
	var dbNow time.Time
	if err := pool.QueryRow(ctx, `SELECT now()`).Scan(&dbNow); err != nil {
		t.Fatalf("read the database's clock: %v", err)
	}
	appNow := dbNow.Add(time.Hour)
	svc := NewService(repo).WithClock(func() time.Time { return appNow })

	// Mark the channel read with the application's clock running ahead.
	if err := svc.MarkRead(ctx, Caller{UserID: uSamRivera}, chOffice); err != nil {
		t.Fatalf("mark read: %v", err)
	}

	// The cursor is the database's clock, not the application's.
	var cursor time.Time
	if err := pool.QueryRow(ctx,
		`SELECT last_read_at FROM msg_members WHERE channel_id = $1 AND user_id = $2`,
		chOffice, uSamRivera).Scan(&cursor); err != nil {
		t.Fatalf("read the cursor: %v", err)
	}
	if cursor.After(dbNow.Add(time.Minute)) {
		t.Errorf("the read cursor must be the database's clock, not the application's: cursor %s, the database's clock %s",
			cursor.UTC(), dbNow.UTC())
	}

	// A message posted in the gap lights the badge.
	time.Sleep(5 * time.Millisecond)
	if _, err := repo.InsertMessage(ctx, chOffice, uAlexMorgan, fmt.Sprintf("clock-gap-probe-%d", appNow.UnixNano()), ""); err != nil {
		t.Fatal(err)
	}
	if got := unreadFor(ctx, t, repo, uSamRivera, chOffice); got < 1 {
		t.Errorf("a message posted in the clock gap must light the badge, got %d unread", got)
	}
}
