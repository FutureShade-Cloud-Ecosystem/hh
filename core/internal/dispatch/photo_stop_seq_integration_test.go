//go:build integration

// photo_stop_seq_integration_test.go: the photo door moves the stop's own stops
// stream seq, so the device that owns the stop learns that the count moved.
//
// WHY THIS EXISTS. The stops scope's projection carries photo_count, computed
// from the photos table (the module's own stopSelectColumns). A device pulls
// "seq IS NOT NULL AND seq > cursor", so a row whose CONTENT changed is only
// seen again when its seq moved. The photo door writes the photo row and
// draws the POD stream's seq beside it, and leaves the stop row untouched:
// the stop's seq does not move, so no pull ever carries the count that moved.
// The driver's screen then keeps saying no picture was sent, and the proof the
// driver took never reaches the device that holds the run sheet. The same
// rule already stands for the stop's other moves: a stop draws its own
// scope's seq beside the move it makes (the unassignment on a cancelled run,
// the REST path's stamped AssignStop).
//
// The assertion is the wire's, not the table's: the fence world gives the
// test a driver, a run, a stop and the engine the device pulls with, so the
// test asks the pull for the stop since the stop's last seq and expects the
// stop back, carrying its new photo count.
//
//	DATABASE_URL=... go test -tags integration -count=1 -run PhotoDoorMovesTheStopsStreamSeq ./internal/dispatch/

package dispatch

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// photoCountOf reads the photo_count the projection carries, whatever number
// the JSON round trip left behind.
func photoCountOf(data map[string]any) (int, bool) {
	switch v := data["photo_count"].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	default:
		return 0, false
	}
}

// TestPhotoDoorMovesTheStopsStreamSeq proves the door's promise: a picture
// taken on a stop reaches the device that holds the stop.
func TestPhotoDoorMovesTheStopsStreamSeq(t *testing.T) {
	w := newFenceWorld(t)
	ctx := globalCtx(t, w.pool)

	// The stop's seq before the picture lands.
	var before int64
	if err := w.pool.QueryRow(ctx, `SELECT seq FROM delivery_stops WHERE id = $1::uuid`, w.stopA).Scan(&before); err != nil {
		t.Fatalf("read the stop's seq before the photo: %v", err)
	}
	if before == 0 {
		t.Fatalf("the stop carries no seq: the test needs a stop a device can already pull")
	}

	// The picture lands through the REST door the driver app uses.
	repo := NewRepository(w.pool)
	if _, err := repo.InsertPhoto(ctx, w.stopA, testkit.BranchNorthAID, "image/png", "", "", []byte("PHOTO DOOR")); err != nil {
		t.Fatalf("the photo door refused the picture: %v", err)
	}

	// The stop's content changed (its photo count moved), so its own seq must
	// have moved with it.
	var after int64
	if err := w.pool.QueryRow(ctx, `SELECT seq FROM delivery_stops WHERE id = $1::uuid`, w.stopA).Scan(&after); err != nil {
		t.Fatalf("read the stop's seq after the photo: %v", err)
	}
	if after <= before {
		t.Fatalf("the photo door left the stop's stops seq alone: before %d, after %d", before, after)
	}

	// And the wire must carry it: a pull since the old seq answers the stop
	// again, with the count that moved.
	info, ok := sync.Lookup("stops")
	if !ok {
		t.Fatalf("the stops scope is missing from the registry")
	}
	resp, err := w.engine.Pull(ctx, info, w.driverA, before, 200)
	if err != nil {
		t.Fatalf("pull the stops scope: %v", err)
	}
	for _, item := range resp.Items {
		if item.Id != w.stopA {
			continue
		}
		count, ok := photoCountOf(item.Data)
		if !ok || count != 1 {
			t.Fatalf("the pull carried the stop with photo_count %v, want 1", item.Data["photo_count"])
		}
		return
	}
	t.Fatalf("the pull since %d did not carry the stop %s: the device never learns the count moved", before, w.stopA)
}

// TestPhotoDoorTouchesOnlyItsOwnStop holds the fence beside the fix: the
// picture moves its own stop's seq and no other row's, and the count is the
// picture's own.
func TestPhotoDoorTouchesOnlyItsOwnStop(t *testing.T) {
	w := newFenceWorld(t)
	ctx := globalCtx(t, w.pool)

	var otherBefore int64
	if err := w.pool.QueryRow(ctx, `SELECT COALESCE(seq, 0) FROM delivery_stops WHERE id = $1::uuid`, w.stopB).Scan(&otherBefore); err != nil {
		t.Fatalf("read the other stop's seq: %v", err)
	}

	repo := NewRepository(w.pool)
	if _, err := repo.InsertPhoto(ctx, w.stopA, testkit.BranchNorthAID, "image/png", "", "", []byte("PHOTO DOOR")); err != nil {
		t.Fatalf("the photo door refused the picture: %v", err)
	}

	var otherAfter int64
	if err := w.pool.QueryRow(ctx, `SELECT COALESCE(seq, 0) FROM delivery_stops WHERE id = $1::uuid`, w.stopB).Scan(&otherAfter); err != nil {
		t.Fatalf("read the other stop's seq after: %v", err)
	}
	if otherAfter != otherBefore {
		t.Fatalf("the photo door moved another stop's seq: before %d, after %d", otherBefore, otherAfter)
	}

	var count int
	if err := w.pool.QueryRow(ctx, `SELECT count(*) FROM delivery_pod_photos WHERE stop_id = $1::uuid`, w.stopA).Scan(&count); err != nil {
		t.Fatalf("count the pictures on the stop: %v", err)
	}
	if count != 1 {
		t.Fatalf("the picture did not land on its own stop: %d rows", count)
	}
}
