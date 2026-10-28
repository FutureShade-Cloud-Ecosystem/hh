package messaging

import (
	"context"
	"testing"
	"time"
)

// attClockMirror is the attachment id the clock legs mirror.
const attClockMirror = "00000000-0000-4000-8000-0000000000c1"

// UX-109: a mirrored attachment's stamp is the DATABASE's clock.
//
// The room's attachment list is ordered by created_at, and the index over it is
// (channel_id, created_at DESC, id DESC). The direct path stamps that column
// with the database's now(); the mirror path stamped it with the APPLICATION's
// clock, so one list carried two clocks and its order was the order of two
// machines. With the app's clock ahead of the database's, a mirrored file
// sorts ahead of the files the database stamped.
//
// The leg pins the app's clock and says the mirror hands the store no clock of
// its own: the database's clock stamps the row.
func TestMirrorRoomAttachment_StampsNoClockOfItsOwn(t *testing.T) {
	f := newFakeStore()
	reads := 0
	svc := svcWith(f).WithClock(func() time.Time { reads++; return time.Now() })

	meta := AttachmentMeta{
		ID: attClockMirror, AppwriteFileID: "file-clock-1", Bucket: "storage-files",
		Kind: "document", Mime: "application/pdf",
	}
	if err := svc.MirrorRoomAttachment(context.Background(), chGen, meta, uAlice, "specs.pdf", ""); err != nil {
		t.Fatalf("mirror the attachment: %v", err)
	}
	if reads != 0 {
		t.Fatalf("a mirrored attachment's stamp is the database's, the app's clock was read %d time(s)", reads)
	}
}
