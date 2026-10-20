//go:build integration

// seqtrigger_drawbeforelock_integration_test.go: the draw before lock leg for attachment (PR 556, round 3c). The
// sync tombstone used to allocate the counter and then lock the row; a writer that relies on the sync seq trigger takes the
// row first. It now relies on the trigger too. The trigger relying writer (a plain touch) is parked between its
// row lock and its draw with a BEFORE INSERT trigger on scope_sequences (testkit.DrawPark); the real writer runs
// on the same row. Red at 099f2e27: the writer draws the counter, waits for the row, the park releases, the
// touch wants the counter, 40P01. The writer still answers the seq the trigger drew.
package attachments

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

const (
	dblOrgID = "a0e41400-0000-4000-8000-000000000001"
	dblRowID = "a0e41400-0000-4000-8000-000000000002"
)

func TestIntegration_DrawBeforeLock_AttachmentTombstoneBesideAWrite(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("attachments draw before lock leg: DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	for _, q := range []string{
		`INSERT INTO org (id, slug, name) VALUES ('` + dblOrgID + `', 'attachments-draw-before-lock', 'draw before lock')`,
		`INSERT INTO attachment (id, org_id, kind, appwrite_file_id, bucket, mime, seq) VALUES ('` + dblRowID + `', '` + dblOrgID + `', 'photo', 'dbl', 'hh', '', 1)`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM attachment WHERE org_id = $1`, dblOrgID)
		_, _ = pool.Exec(c, `DELETE FROM org WHERE id = $1`, dblOrgID)
	})

	store := NewSyncStore(pool)
	filter := hhsync.ScopeFilter{OrgID: dblOrgID, Actor: "draw-before-lock-leg"}
	engine := hhsync.NewEngine()
	if err := engine.RegisterScope("attachments", store); err != nil {
		t.Fatalf("register: %v", err)
	}
	info, _ := hhsync.Lookup("attachments")
	// The device's cursor, held before the write.
	before, err := engine.Pull(ctx, info, filter, 0, 100)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}

	park := testkit.NewDrawPark(t, pool, "attachments", 55010011)
	touch := park.TouchRow("attachment", "updated_at = now()", dblRowID)
	park.WaitParked("the touch stands between its row lock and its draw")

	del := make(chan error, 1)
	go func() {
		_, err := store.Delete(ctx, filter, dblRowID, "")
		del <- err
	}()
	park.WaitBlocked("the tombstone waits")
	park.Answers(map[string]chan error{"touch": touch, "tombstone": del})

	// The device pulls from the cursor it held and receives the tombstone with a seq above it.
	pulled, err := engine.Pull(ctx, info, filter, before.LastSeq, 100)
	if err != nil {
		t.Fatalf("pull after: %v", err)
	}
	for _, tb := range pulled.Tombstones {
		if tb.Id == dblRowID && tb.Seq > before.LastSeq {
			return
		}
	}
	t.Fatalf("the device did not receive the tombstone from cursor %d: %+v", before.LastSeq, pulled.Tombstones)
}
