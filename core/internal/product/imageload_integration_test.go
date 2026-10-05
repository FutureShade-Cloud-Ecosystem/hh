//go:build integration

// imageload_integration_test.go proves the load doors' product image
// door (HH-LOAD-1 LD-5) through the frame: the image bytes land in the
// product module's own image store (0102, one primary thumbnail per
// product) on the door's own transaction, the product resolved by
// legacy key (a name not yet loaded refusing as a missing reference
// naming the kind and the key), the replay with identical bytes
// answering unchanged and writing nothing, changed bytes answering
// updated and replacing the row, and one audit row per put. The pool 4
// leg and the deadlock victim leg close the file: the door opens a
// transaction, so it carries the row 786 legs like every batch door.
//
// Fixtures are invented. Run (the database URL rides the command line):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5680/integration_gate?sslmode=disable' \
//	  go test -tags integration ./internal/product/... -count=1 -run TestProductImageLoad
package product

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
)

// fxImagePNG is a minimal well formed PNG byte string (the magic bytes
// plus padding; the store sniffs the type, it does not decode).
var fxImagePNG = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0x00}, 32)...)

// fxImageOtherPNG is a different well formed PNG, so a changed bytes leg
// carries a different content hash.
var fxImageOtherPNG = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0x01}, 32)...)

// imageDoorEnv is one test's fixtures: the product door's env plus the
// image door built over the module's own rules and writer.
type imageDoorEnv struct {
	*productLoadEnv
	door *load.ImageDoor
}

func imageDoorSetup(t *testing.T) *imageDoorEnv {
	t.Helper()
	e := productLoadSetup(t)
	door := load.NewImageDoor(load.NewStore(e.pool), ProductImageLoadRules{}, ProductImageLoadWriter{}, e.spy)
	return &imageDoorEnv{productLoadEnv: e, door: door}
}

// imageRow reads the product's primary image row.
func (e *imageDoorEnv) imageRow(t *testing.T, productID string) (contentType string, size int, checksum string) {
	t.Helper()
	err := e.pool.QueryRow(context.Background(), `
		SELECT content_type, byte_size, checksum FROM product_images
		WHERE product_id = $1::uuid AND sort_order = 0`, productID).
		Scan(&contentType, &size, &checksum)
	if err != nil {
		t.Fatalf("image row read: %v", err)
	}
	return contentType, size, checksum
}

func TestProductImageLoadLandsBytesThroughTheModuleStore(t *testing.T) {
	e := imageDoorSetup(t)
	ctx := context.Background()
	e.prSeedUnit(t, "EA", "Each", "count")
	if _, err := e.productLoadEnv.door.UpsertBatch(ctx, load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-img-1", Caller: fxProdActorID,
		Items: []load.Item{prItem("PR-I1", "FXPR-4001", "Fixture Image Product", nil)}}); err != nil {
		t.Fatalf("product batch: %v", err)
	}
	o, err := e.door.PutImage(ctx, load.PutImageRequest{
		OrgID: fxProdOrgID, SourceSystem: fxProdSource, LegacyID: "PR-I1", Caller: fxProdActorID,
		ContentType: "image/png", AltText: "Fixture Alt", Bytes: fxImagePNG,
	})
	if err != nil {
		t.Fatalf("image put: %v", err)
	}
	if o.Status != load.OutcomeCreated || o.HHID == "" {
		t.Fatalf("the first put answered %+v, want created with the product's id", o)
	}
	id, _, _, _, _ := e.prRow(t, "FXPR-4001")
	if o.HHID != id {
		t.Fatalf("the put names row %s, want the product %s", o.HHID, id)
	}
	ct, size, _ := e.imageRow(t, id)
	if ct != "image/png" || size != len(fxImagePNG) {
		t.Fatalf("the stored image carries %s of %d bytes, want the put's png of %d", ct, size, len(fxImagePNG))
	}
	// The image name rides the product's legacy key under its own kind,
	// so the replay decision reads the bytes' hash off the registry.
	var recorded string
	if err := e.pool.QueryRow(ctx, `
		SELECT content_hash FROM load_key_registry
		WHERE org_id = $1::uuid AND source_system = $2 AND kind = $3 AND legacy_id = $4`,
		fxProdOrgID, fxProdSource, load.KindProductImage, "PR-I1").Scan(&recorded); err != nil {
		t.Fatalf("image name read: %v", err)
	}
	sum := sha256.Sum256(fxImagePNG)
	if recorded != hex.EncodeToString(sum[:]) {
		t.Fatalf("the recorded hash is %q, want the bytes' sha256", recorded)
	}

	// The identical replay answers unchanged and replaces nothing.
	e.spy.mu.Lock()
	spyBefore := e.spy.rows
	e.spy.mu.Unlock()
	o, err = e.door.PutImage(ctx, load.PutImageRequest{
		OrgID: fxProdOrgID, SourceSystem: fxProdSource, LegacyID: "PR-I1", Caller: fxProdActorID,
		ContentType: "image/png", Bytes: fxImagePNG,
	})
	if err != nil {
		t.Fatalf("replay put: %v", err)
	}
	if o.Status != load.OutcomeUnchanged {
		t.Fatalf("the identical replay answered %+v, want unchanged", o)
	}
	_, size, _ = e.imageRow(t, id)
	if size != len(fxImagePNG) {
		t.Fatalf("the replay left %d bytes stored, want the first put's %d", size, len(fxImagePNG))
	}
	e.spy.mu.Lock()
	got := e.spy.rows
	e.spy.mu.Unlock()
	if got != spyBefore+1 {
		t.Fatalf("%d audit rows stand after the replay, want one per put (%d before)", got, spyBefore)
	}

	// Changed bytes answer updated and replace the row.
	o, err = e.door.PutImage(ctx, load.PutImageRequest{
		OrgID: fxProdOrgID, SourceSystem: fxProdSource, LegacyID: "PR-I1", Caller: fxProdActorID,
		ContentType: "image/png", Bytes: fxImageOtherPNG,
	})
	if err != nil {
		t.Fatalf("changed put: %v", err)
	}
	if o.Status != load.OutcomeUpdated {
		t.Fatalf("the changed put answered %+v, want updated", o)
	}
	ct, size, _ = e.imageRow(t, id)
	if ct != "image/png" || size != len(fxImageOtherPNG) {
		t.Fatalf("the replaced image carries %s of %d bytes, want the new png of %d", ct, size, len(fxImageOtherPNG))
	}
}

func TestProductImageLoadRefusesAProductNotYetLoaded(t *testing.T) {
	e := imageDoorSetup(t)
	o, err := e.door.PutImage(context.Background(), load.PutImageRequest{
		OrgID: fxProdOrgID, SourceSystem: fxProdSource, LegacyID: "PR-MISSING", Caller: fxProdActorID,
		ContentType: "image/png", Bytes: fxImagePNG,
	})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if o.Status != load.OutcomeRefused || o.Reason != load.ReasonMissingReference ||
		!strings.Contains(o.Detail, "product") || !strings.Contains(o.Detail, "PR-MISSING") {
		t.Fatalf("the unloaded product answered %+v, want refused as missing_reference naming the kind and the key", o)
	}
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM product_images WHERE product_id IN (SELECT id FROM products WHERE sku LIKE 'FXPR-%')`).Scan(&n); err != nil {
		t.Fatalf("image count: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d image rows stand after the refused put, want none", n)
	}
}

func TestProductImageLoadPoolFourThreeContenders(t *testing.T) {
	e := imageDoorSetup(t)
	ctx := context.Background()
	e.prSeedUnit(t, "EA", "Each", "count")
	if _, err := e.productLoadEnv.door.UpsertBatch(ctx, load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-img-p0", Caller: fxProdActorID,
		Items: []load.Item{prItem("PR-I2", "FXPR-4002", "Fixture Pool Image", nil)}}); err != nil {
		t.Fatalf("product batch: %v", err)
	}
	pool := poolFour(t)
	held, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("hold one connection: %v", err)
	}
	defer held.Release()
	door := load.NewImageDoor(load.NewStore(pool), ProductImageLoadRules{}, ProductImageLoadWriter{}, &loadAuditSpy{})
	type answer struct {
		o   load.Outcome
		err error
	}
	answers := make([]answer, 3)
	var wg sync.WaitGroup
	for k := 0; k < 3; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, poolFourBatchTimeout)
			defer cancel()
			answers[k].o, answers[k].err = door.PutImage(cctx, load.PutImageRequest{
				OrgID: fxProdOrgID, SourceSystem: fxProdSource, LegacyID: "PR-I2", Caller: fxProdActorID,
				ContentType: "image/png", Bytes: fxImagePNG,
			})
		}(k)
	}
	wg.Wait()
	created := 0
	for k, a := range answers {
		if a.err != nil {
			t.Fatalf("contender %d: %v (the image door needed a connection beyond its transaction)", k, a.err)
		}
		if a.o.Status == load.OutcomeCreated {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("%d creates across the three contenders, want exactly one", created)
	}
	var rows int
	if err := e.pool.QueryRow(ctx,
		`SELECT count(*) FROM product_images WHERE product_id = (SELECT id FROM products WHERE sku = 'FXPR-4002')`).Scan(&rows); err != nil {
		t.Fatalf("image count: %v", err)
	}
	if rows != 1 {
		t.Fatalf("%d image rows stand, want the one row three contenders raced on", rows)
	}
}

func TestProductImageLoadDeadlockAnswersTheContentionSentinel(t *testing.T) {
	e := imageDoorSetup(t)
	ctx := context.Background()
	e.prSeedUnit(t, "EA", "Each", "count")
	if _, err := e.productLoadEnv.door.UpsertBatch(ctx, load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-img-d0", Caller: fxProdActorID,
		Items: []load.Item{prItem("PR-I3", "FXPR-4003", "Fixture Deadlock Image", nil)}}); err != nil {
		t.Fatalf("product batch: %v", err)
	}
	// One put stands, so the name row and the image row both exist and
	// the next put takes the write arm (the recorded hash corrupted to
	// one no put carries).
	if _, err := e.door.PutImage(ctx, load.PutImageRequest{
		OrgID: fxProdOrgID, SourceSystem: fxProdSource, LegacyID: "PR-I3", Caller: fxProdActorID,
		ContentType: "image/png", Bytes: fxImagePNG,
	}); err != nil {
		t.Fatalf("first put: %v", err)
	}
	if _, err := e.pool.Exec(ctx,
		`UPDATE load_key_registry SET content_hash = repeat('a', 64)
		 WHERE org_id = $1::uuid AND source_system = $2 AND kind = $3 AND legacy_id = 'PR-I3'`,
		fxProdOrgID, fxProdSource, load.KindProductImage); err != nil {
		t.Fatalf("hash plant: %v", err)
	}
	outside, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("outside begin: %v", err)
	}
	defer func() { _ = outside.Rollback(ctx) }()
	// A long deadlock timeout on the outside session, so the put's own
	// detection (the default, a second) fires first and the put is the
	// victim.
	if _, err := outside.Exec(ctx, `SET LOCAL deadlock_timeout = '30s'`); err != nil {
		t.Fatalf("outside timeout: %v", err)
	}
	// The outside transaction holds the image row; the put holds the
	// name row (its read carried FOR UPDATE) and wants the image row;
	// the outside then wants the name row, the cycle closes, and
	// Postgres kills the put.
	if _, err := outside.Exec(ctx, `
		UPDATE product_images SET alt_text = alt_text
		WHERE product_id = (SELECT id FROM products WHERE sku = 'FXPR-4003')`); err != nil {
		t.Fatalf("outside image lock: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		_, err := e.door.PutImage(cctx, load.PutImageRequest{
			OrgID: fxProdOrgID, SourceSystem: fxProdSource, LegacyID: "PR-I3", Caller: fxProdActorID,
			ContentType: "image/png", Bytes: fxImagePNG,
		})
		done <- err
	}()
	waitForLockWait(t, e.pool)
	_, _ = outside.Exec(ctx, `
		UPDATE load_key_registry SET content_hash = content_hash
		WHERE org_id = $1::uuid AND source_system = $2 AND kind = $3 AND legacy_id = 'PR-I3'`,
		fxProdOrgID, fxProdSource, load.KindProductImage)
	select {
	case err := <-done:
		if !errors.Is(err, load.ErrBatchContended) {
			t.Fatalf("the deadlocked put answered %v, want the ErrBatchContended sentinel", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("the deadlock never resolved")
	}
	_ = outside.Rollback(ctx)
	// The resubmission converges.
	o, err := e.door.PutImage(ctx, load.PutImageRequest{
		OrgID: fxProdOrgID, SourceSystem: fxProdSource, LegacyID: "PR-I3", Caller: fxProdActorID,
		ContentType: "image/png", Bytes: fxImagePNG,
	})
	if err != nil {
		t.Fatalf("resubmission: %v", err)
	}
	if o.Status != load.OutcomeUpdated && o.Status != load.OutcomeCreated {
		t.Fatalf("the resubmission answered %+v, want the put landing", o)
	}
}
