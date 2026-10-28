//go:build integration

package messaging

import (
	"context"
	"testing"
	"time"
)

// UX-109: the edit window runs on the DATABASE's clock.
//
// The message is posted here, so the database stamped its created_at. The
// application's clock is then pinned two hours ahead of the database's, and the
// author edits their own message: the window must still be open, because the
// database says the message is fresh. A window measured on the app's clock
// would close it, and the author would lose the pen on their own words.
func TestIntegration_EditMessage_WindowRunsOnTheDatabasesClock(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	room, msg, author, _ := editWorld(ctx, t, pool)

	// The database's clock, and the application's clock two hours ahead of it.
	var dbNow time.Time
	if err := pool.QueryRow(ctx, `SELECT now()`).Scan(&dbNow); err != nil {
		t.Fatalf("read the database's clock: %v", err)
	}
	svc := NewService(repo).WithClock(func() time.Time { return dbNow.Add(2 * time.Hour) })

	if _, err := svc.EditMessage(ctx, Caller{UserID: author}, room, msg, "Counted again, 13 on the pallet"); err != nil {
		t.Fatalf("a message the database says is fresh must still be editable when the app's clock runs ahead: %v", err)
	}
}
