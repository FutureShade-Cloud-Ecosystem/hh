//go:build integration

// syncscope_lock_integration_test.go, the unit set door's window
// (HH-LOAD-1 LD-3 part B, round 2): the unit check inside the one list
// price write path takes the product row FOR NO KEY UPDATE, which
// conflicts with the FOR SHARE the unit set door (LD-3 part A, branches
// load/ld3-unit-catalogue and load/ld5-product-door, not in this base)
// holds on the same row while its removal check reads the price rows
// and drops units only after it. A price landing in a unit and a set
// replacement dropping it therefore serialise in either order, and the
// final state never leaves a price in a unit outside the set. Part A
// is not in this base, so the door stands in as a raw transaction that
// does exactly what the door owes: take the products row FOR SHARE,
// read the price rows as the removal check does, hold, then remove.
// The lock must ride the caller's transaction (row 786), and the
// allocator lock is drawn first (the pool file's convergence leg
// proves the order cannot deadlock).
//
// Run with (the URL is the command's own throwaway database):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5714/ld3bcore?sslmode=disable' \
//	  go test -tags=integration ./internal/pricing/ -run TestMatrixUnitSetDoor -count=1 -v -p 1
package pricing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// holdDoor opens the stand in for the unit set door: the products row
// FOR SHARE, then the removal check's own read of the price rows that
// name the unit, and the transaction left open, the share held.
func holdDoor(t *testing.T, f *unitFixture, productID, unit string) (pgx.Tx, int) {
	t.Helper()
	door, err := f.pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin the door: %v", err)
	}
	if _, err := door.Exec(context.Background(),
		`SELECT 1 FROM products WHERE id = $1::uuid FOR SHARE`, productID); err != nil {
		t.Fatalf("the door's FOR SHARE: %v", err)
	}
	var naming int
	if err := door.QueryRow(context.Background(),
		`SELECT count(*) FROM pricing_list_prices WHERE product_id = $1::uuid AND price_uom = $2`,
		productID, unit).Scan(&naming); err != nil {
		t.Fatalf("the door's removal check read: %v", err)
	}
	return door, naming
}

// TestMatrixUnitSetDoorWindowClosesUnderTheProductsRowLock: the door
// holds its FOR SHARE, its removal check has read no price naming BOX,
// and the write naming BOX starts inside that window. With the
// products row lock the write waits behind the share; the door drops
// the unit and commits; the write then proceeds, reads the set without
// BOX and refuses, and nothing stands in a dropped unit. Without the
// lock the write slips through the window and the door's drop leaves
// an anchor in a unit outside the set, which is the red the bite
// reproduces.
func TestMatrixUnitSetDoorWindowClosesUnderTheProductsRowLock(t *testing.T) {
	f := newUnitFixture(t)
	id := f.product("PC", "BOX", 100)
	door, naming := holdDoor(t, f, id, "BOX")
	if naming != 0 {
		t.Fatalf("the door's removal check read %d price rows naming BOX, want none yet", naming)
	}

	landed := make(chan error, 1)
	go func() {
		landed <- f.write(matrixPriceWrite{ProductID: id, Cents: 3500, PriceUOM: "BOX"})
	}()

	// The window: give the write a fair slice to land inside it. Under
	// the lock it cannot; the bite shows it landing here.
	var slipped bool
	var writeErr error
	time.Sleep(700 * time.Millisecond)
	select {
	case writeErr = <-landed:
		slipped = true
		t.Logf("the write landed inside the door's window (err %v), the window did not hold the write", writeErr)
	default:
	}

	// The door drops the unit its removal check found no price in, and
	// commits.
	if _, err := door.Exec(context.Background(),
		`DELETE FROM product_uoms WHERE product_id = $1::uuid AND uom_code = $2`, id, "BOX"); err != nil {
		t.Fatalf("the door's unit drop: %v", err)
	}
	if err := door.Commit(context.Background()); err != nil {
		t.Fatalf("the door's commit: %v", err)
	}

	if !slipped {
		select {
		case writeErr = <-landed:
		case <-time.After(20 * time.Second):
			t.Fatalf("the write never came back behind the door's commit")
		}
	}
	var prices, units int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pricing_list_prices WHERE product_id = $1::uuid AND price_uom = 'BOX'`,
		id).Scan(&prices); err != nil {
		t.Fatalf("price read: %v", err)
	}
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM product_uoms WHERE product_id = $1::uuid AND uom_code = 'BOX'`,
		id).Scan(&units); err != nil {
		t.Fatalf("unit read: %v", err)
	}
	if prices > 0 && units == 0 {
		t.Fatalf("the window leaked: %d price row(s) stand in BOX and the set door dropped the unit", prices)
	}
	if prices == 0 {
		if !errors.Is(writeErr, errPriceUOMNotInSet) {
			t.Fatalf("the write behind the committed door answered %v, want the typed price_uom refusal", writeErr)
		}
		t.Logf("serialised, door first: the drop landed, the write refused behind it")
	}
}

// TestMatrixUnitSetDoorSeesAPriceLandedBeforeItsCheck: the other order,
// the write first. The door opens after the price landed, and its
// removal check, reading the price rows as the real door does, sees the
// row naming BOX and refuses its drop, so the unit stays with its
// price. The read the door owes is the same one the window leg above
// exercised; this pin says the landed price is visible to it.
func TestMatrixUnitSetDoorSeesAPriceLandedBeforeItsCheck(t *testing.T) {
	f := newUnitFixture(t)
	id := f.product("PC", "BOX", 100)
	if err := f.write(matrixPriceWrite{ProductID: id, Cents: 3500, PriceUOM: "BOX"}); err != nil {
		t.Fatalf("anchor load per the larger unit: %v", err)
	}
	door, naming := holdDoor(t, f, id, "BOX")
	if naming != 1 {
		t.Fatalf("the door's removal check read %d price rows naming BOX, want the landed one (a real door refuses its drop)", naming)
	}
	if err := door.Rollback(context.Background()); err != nil {
		t.Fatalf("the door's rollback: %v", err)
	}
	var units int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM product_uoms WHERE product_id = $1::uuid AND uom_code = 'BOX'`,
		id).Scan(&units); err != nil {
		t.Fatalf("unit read: %v", err)
	}
	if units != 1 {
		t.Fatalf("%d BOX unit rows stand behind the door's check, want the one it saw", units)
	}
}
