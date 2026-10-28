//go:build integration

package messaging

import (
	"context"
	"testing"
	"time"
)

// UX-109: a mirrored attachment carries the DATABASE's clock.
//
// The room's attachment list is ordered by created_at, and the direct path
// stamps that column with the database's now(). The mirror path stamped it
// with the APPLICATION's clock, so one list carried two clocks. The app's
// clock is pinned two hours ahead of the database's here: the mirrored row's
// stamp must still be the database's.
func TestIntegration_MirroredAttachment_CarriesTheDatabasesClock(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	room, _, _, _ := editWorld(ctx, t, pool)

	// The database's clock, and the application's clock two hours ahead of it.
	var dbNow time.Time
	if err := pool.QueryRow(ctx, `SELECT now()`).Scan(&dbNow); err != nil {
		t.Fatalf("read the database's clock: %v", err)
	}
	svc := NewService(repo).WithClock(func() time.Time { return dbNow.Add(2 * time.Hour) })

	meta := AttachmentMeta{
		ID: attClockMirror, AppwriteFileID: "file-clock-1", Bucket: "storage-files",
		Kind: "document", Mime: "application/pdf",
	}
	if err := svc.MirrorRoomAttachment(ctx, room, meta, uAlexMorgan, "specs.pdf", ""); err != nil {
		t.Fatalf("mirror the attachment: %v", err)
	}

	var createdAt time.Time
	if err := pool.QueryRow(ctx,
		`SELECT created_at FROM msg_attachments WHERE channel_id = $1 AND attachment_id = $2`,
		room, attClockMirror).Scan(&createdAt); err != nil {
		t.Fatalf("read the mirrored row's stamp: %v", err)
	}
	if createdAt.After(dbNow.Add(time.Minute)) {
		t.Errorf("a mirrored attachment must carry the database's clock, not the application's: %s, the database's clock %s",
			createdAt.UTC(), dbNow.UTC())
	}
}
