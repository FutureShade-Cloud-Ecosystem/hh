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
	// so the replay decision reads the record's hash off the registry
	// (the bytes, the normalised type and the alt text together, the
	// door's own framing; the whole record replay is what pins it).
	var recorded string
	if err := e.pool.QueryRow(ctx, `
		SELECT content_hash FROM load_key_registry
		WHERE org_id = $1::uuid AND source_system = $2 AND kind = $3 AND legacy_id = $4`,
		fxProdOrgID, fxProdSource, load.KindProductImage, "PR-I1").Scan(&recorded); err != nil {
		t.Fatalf("image name read: %v", err)
	}
	if len(recorded) != 64 {
		t.Fatalf("the recorded hash is %q, want a sha256 hex string", recorded)
	}

	// The identical replay (the whole record: the bytes, the type and
	// the alt text the first put carried) answers unchanged and replaces
	// nothing.
	e.spy.mu.Lock()
	spyBefore := e.spy.rows
	e.spy.mu.Unlock()
	o, err = e.door.PutImage(ctx, load.PutImageRequest{
		OrgID: fxProdOrgID, SourceSystem: fxProdSource, LegacyID: "PR-I1", Caller: fxProdActorID,
		ContentType: "image/png", AltText: "Fixture Alt", Bytes: fxImagePNG,
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

// The record is the whole truth (the review's P2a and P2b, red first):
// a Content-Type in any case is normalised before the allowlist and the
// constraint (never a 500), the normalised type and the alt text ride
// the replay decision beside the bytes (the same bytes with changed alt
// text answers updated and the stored alt text follows), and the
// normalised type makes two spellings of one media type one record.
func TestProductImageLoadReplaysOnTheWholeRecord(t *testing.T) {
	e := imageDoorSetup(t)
	ctx := context.Background()
	e.prSeedUnit(t, "EA", "Each", "count")
	if _, err := e.productLoadEnv.door.UpsertBatch(ctx, load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-img-r0", Caller: fxProdActorID,
		Items: []load.Item{prItem("PR-I1", "FXPR-4001", "Fixture Record Product", nil)}}); err != nil {
		t.Fatalf("product batch: %v", err)
	}
	id, _, _, _, _ := e.prRow(t, "FXPR-4001")
	imageAlt := func(t *testing.T) string {
		t.Helper()
		var alt string
		if err := e.pool.QueryRow(ctx,
			`SELECT coalesce(alt_text, '') FROM product_images WHERE product_id = $1::uuid AND sort_order = 0`, id).Scan(&alt); err != nil {
			t.Fatalf("alt read: %v", err)
		}
		return alt
	}

	// P2a: the declared type in upper case is the same raster type; the
	// put lands and the row carries the normalised spelling, never the
	// constraint's refusal.
	o, err := e.door.PutImage(ctx, load.PutImageRequest{
		OrgID: fxProdOrgID, SourceSystem: fxProdSource, LegacyID: "PR-I1", Caller: fxProdActorID,
		ContentType: "IMAGE/PNG", AltText: "first", Bytes: fxImagePNG,
	})
	if err != nil {
		t.Fatalf("the upper case type put answered %v, want the put landing", err)
	}
	if o.Status != load.OutcomeCreated {
		t.Fatalf("the first put answered %+v, want created", o)
	}
	if ct, _, _ := e.imageRow(t, id); ct != "image/png" {
		t.Fatalf("the row stores content type %q, want the normalised image/png", ct)
	}
	// P2b: the same bytes with changed alt text answer updated, and the
	// stored alt text is the record's new one.
	o, err = e.door.PutImage(ctx, load.PutImageRequest{
		OrgID: fxProdOrgID, SourceSystem: fxProdSource, LegacyID: "PR-I1", Caller: fxProdActorID,
		ContentType: "image/png", AltText: "second", Bytes: fxImagePNG,
	})
	if err != nil {
		t.Fatalf("changed alt put: %v", err)
	}
	if o.Status != load.OutcomeUpdated {
		t.Fatalf("the changed alt answered %+v, want updated", o)
	}
	if got := imageAlt(t); got != "second" {
		t.Fatalf("the stored alt text is %q, want the record's second", got)
	}
	// The replay of the whole record answers unchanged, in either
	// spelling of the type: the normalised type and the alt text ride
	// the recorded hash beside the bytes.
	o, err = e.door.PutImage(ctx, load.PutImageRequest{
		OrgID: fxProdOrgID, SourceSystem: fxProdSource, LegacyID: "PR-I1", Caller: fxProdActorID,
		ContentType: "image/png", AltText: "second", Bytes: fxImagePNG,
	})
	if err != nil {
		t.Fatalf("replay put: %v", err)
	}
	if o.Status != load.OutcomeUnchanged {
		t.Fatalf("the whole record replay answered %+v, want unchanged", o)
	}
	o, err = e.door.PutImage(ctx, load.PutImageRequest{
		OrgID: fxProdOrgID, SourceSystem: fxProdSource, LegacyID: "PR-I1", Caller: fxProdActorID,
		ContentType: "IMAGE/PNG", AltText: "second", Bytes: fxImagePNG,
	})
	if err != nil {
		t.Fatalf("upper case replay put: %v", err)
	}
	if o.Status != load.OutcomeUnchanged {
		t.Fatalf("the upper case replay answered %+v, want unchanged: two spellings of one type are one record", o)
	}
	if got := imageAlt(t); got != "second" {
		t.Fatalf("the replay left alt text %q, want second", got)
	}
}

// A loaded image that replaces a staff upload clears uploaded_by (the
// review's P2c, red first): the row the load lands is the machine
// principal's record, and the staff member the row carried before is
// not the loader of the new bytes.
func TestProductImageLoadReplacesAStaffUploadWhole(t *testing.T) {
	e := imageDoorSetup(t)
	ctx := context.Background()
	e.prSeedUnit(t, "EA", "Each", "count")
	if _, err := e.productLoadEnv.door.UpsertBatch(ctx, load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-img-s0", Caller: fxProdActorID,
		Items: []load.Item{prItem("PR-I1", "FXPR-4001", "Fixture Staff Upload Product", nil)}}); err != nil {
		t.Fatalf("product batch: %v", err)
	}
	id, _, _, _, _ := e.prRow(t, "FXPR-4001")
	const staffUserID = "4642a800-0000-4000-8000-00000000f0b1"
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO users (id, email, display_name, status) VALUES ($1::uuid, 'fixture.image.rep@example.invalid', 'Fixture Image Rep', 'ACTIVE')
		 ON CONFLICT (id) DO NOTHING`, staffUserID); err != nil {
		t.Fatalf("plant staff user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1::uuid`, staffUserID)
	})
	o, err := e.door.PutImage(ctx, load.PutImageRequest{
		OrgID: fxProdOrgID, SourceSystem: fxProdSource, LegacyID: "PR-I1", Caller: fxProdActorID,
		ContentType: "image/png", AltText: "first", Bytes: fxImagePNG,
	})
	if err != nil {
		t.Fatalf("first put: %v", err)
	}
	if o.Status != load.OutcomeCreated {
		t.Fatalf("the first put answered %+v, want created", o)
	}
	// A staff member's upload stands on the row, the shape the staff
	// byte route lands.
	if _, err := e.pool.Exec(ctx,
		`UPDATE product_images SET uploaded_by = $1::uuid WHERE product_id = $2::uuid AND sort_order = 0`,
		staffUserID, id); err != nil {
		t.Fatalf("plant staff upload: %v", err)
	}
	var uploadedBy string
	if err := e.pool.QueryRow(ctx,
		`SELECT coalesce(uploaded_by::text, '') FROM product_images WHERE product_id = $1::uuid AND sort_order = 0`, id).Scan(&uploadedBy); err != nil {
		t.Fatalf("uploaded_by read: %v", err)
	}
	if uploadedBy != staffUserID {
		t.Fatalf("uploaded_by is %q before the load put, want the staff row", uploadedBy)
	}
	// The load put of new bytes replaces the row whole: the staff
	// member's name does not survive it.
	o, err = e.door.PutImage(ctx, load.PutImageRequest{
		OrgID: fxProdOrgID, SourceSystem: fxProdSource, LegacyID: "PR-I1", Caller: fxProdActorID,
		ContentType: "image/png", AltText: "second", Bytes: fxImageOtherPNG,
	})
	if err != nil {
		t.Fatalf("replacing put: %v", err)
	}
	if o.Status != load.OutcomeUpdated {
		t.Fatalf("the replacing put answered %+v, want updated", o)
	}
	if err := e.pool.QueryRow(ctx,
		`SELECT coalesce(uploaded_by::text, '') FROM product_images WHERE product_id = $1::uuid AND sort_order = 0`, id).Scan(&uploadedBy); err != nil {
		t.Fatalf("uploaded_by reread: %v", err)
	}
	if uploadedBy != "" {
		t.Fatalf("uploaded_by is %q after the load put, want it cleared", uploadedBy)
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
