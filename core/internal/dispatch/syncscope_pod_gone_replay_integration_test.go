//go:build integration

// syncscope_pod_gone_replay_integration_test.go, UX-103 round 3: the pod scope's owner legs and the
// entry 489 answers. The owner's replay of an applied key when the photo row or the attested stop is
// gone for everyone answers applied with no seq and applies nothing twice; a tombstone keeps the
// bound answer with its seq; the attestation's Load answers only a stop that carries a typed name
// attestation; and an update or delete naming an attestation stop id is the row not found refusal it
// always was, never a conflict envelope. Red at 52802de2, where any Load miss dropped the bound
// answer and Load answered any live stop for the attestation arm. Run with DATABASE_URL set to a
// migrated and seeded throwaway database.
package dispatch

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestPodPhotoReplayGone_ARowGoneForEveryoneKeepsTheBoundAnswer: an owner's replay of an applied
// photo key when the photo row is hard gone answers applied with no seq, inserts nothing, and the
// same key with another body is still key_reused.
func TestPodPhotoReplayGone_ARowGoneForEveryoneKeepsTheBoundAnswer(t *testing.T) {
	a := newAttestWorld(t)
	ctx := context.Background()
	photoID, key := uuid.NewString(), syncFreshKey()
	op := syncOp("create", key, map[string]any{
		"id": photoID, "stop_id": a.stopA2, "content_type": "image/png",
		"image_bytes_b64": "RkVOQ0U=", "caption": "gone leg",
	}, nil)
	if first := a.push(a.driverA, op); first[0].Status != "applied" || first[0].Seq == nil {
		t.Fatalf("setup: the photo create applies: %+v (%s)", first[0], errText(first[0]))
	}
	if _, err := a.pool.Exec(ctx, `DELETE FROM delivery_pod_photos WHERE id = $1::uuid`, photoID); err != nil {
		t.Fatalf("drop the photo: %v", err)
	}
	again := a.push(a.driverA, op)
	if again[0].Status != "applied" || again[0].Seq != nil {
		t.Fatalf("the owner's replay of an applied photo key when the row is gone answers applied with no seq: got %+v (%s)", again[0], errText(again[0]))
	}
	var n int
	if err := a.pool.QueryRow(ctx, `SELECT count(*) FROM delivery_pod_photos WHERE id = $1::uuid`, photoID).Scan(&n); err != nil {
		t.Fatalf("count photos: %v", err)
	}
	if n != 0 {
		t.Fatalf("the replay applies nothing twice: %d photo rows, want 0", n)
	}
	other := syncOp("create", key, map[string]any{
		"id": photoID, "stop_id": a.stopA2, "content_type": "image/png",
		"image_bytes_b64": "RkVOQ0U=", "caption": "tampered",
	}, nil)
	if reused := a.push(a.driverA, other); reused[0].Status != "rejected" || !strings.Contains(errText(reused[0]), "key_reused") {
		t.Fatalf("the same key with another body is still key_reused: %+v (%s)", reused[0], errText(reused[0]))
	}
}

// TestPodPhotoReplayGone_ATombstonedRowKeepsTheBoundAnswerWithItsSeq: a tombstoned photo answers
// Load, so the owner's replay keeps the bound answer with the tombstone's seq.
func TestPodPhotoReplayGone_ATombstonedRowKeepsTheBoundAnswerWithItsSeq(t *testing.T) {
	a := newAttestWorld(t)
	ctx := context.Background()
	photoID, key := uuid.NewString(), syncFreshKey()
	op := syncOp("create", key, map[string]any{
		"id": photoID, "stop_id": a.stopA2, "content_type": "image/png",
		"image_bytes_b64": "RkVOQ0U=", "caption": "tombstone leg",
	}, nil)
	if first := a.push(a.driverA, op); first[0].Status != "applied" || first[0].Seq == nil {
		t.Fatalf("setup: the photo create applies: %+v (%s)", first[0], errText(first[0]))
	}
	if _, err := a.pool.Exec(ctx, `UPDATE delivery_pod_photos SET deleted_at = now() WHERE id = $1::uuid`, photoID); err != nil {
		t.Fatalf("tombstone the photo: %v", err)
	}
	var seq int64
	if err := a.pool.QueryRow(ctx, `SELECT seq FROM delivery_pod_photos WHERE id = $1::uuid`, photoID).Scan(&seq); err != nil {
		t.Fatalf("read the tombstone's seq: %v", err)
	}
	again := a.push(a.driverA, op)
	if again[0].Status != "applied" || again[0].Seq == nil || *again[0].Seq != seq {
		t.Fatalf("the owner's replay over a tombstoned photo answers applied with the tombstone's seq %d: got %+v (%s)", seq, again[0], errText(again[0]))
	}
}

// TestPodAttestReplayGone_ATombstonedStopKeepsTheBoundAnswer: the stop the attestation create
// answered is tombstoned, so Load misses for the owner too; the replay still answers applied with no
// seq and attests nothing twice. Red at 52802de2: the dropped bound answer sent the replay down the
// plain path and the create refused.
func TestPodAttestReplayGone_ATombstonedStopKeepsTheBoundAnswer(t *testing.T) {
	a := newAttestWorld(t)
	ctx := context.Background()
	op := attestOp(syncFreshKey(), a.stopA2, "Site Foreman", true)
	if first := a.push(a.driverA, op); first[0].Status != "applied" {
		t.Fatalf("setup: the attestation applies: %+v (%s)", first[0], errText(first[0]))
	}
	if _, err := a.pool.Exec(ctx, `UPDATE delivery_stops SET deleted_at = now() WHERE id = $1::uuid`, a.stopA2); err != nil {
		t.Fatalf("tombstone the stop: %v", err)
	}
	again := a.push(a.driverA, op)
	if again[0].Status != "applied" || again[0].Seq != nil {
		t.Fatalf("the owner's replay of an applied key when the stop is tombstoned answers applied with no seq: got %+v (%s)", again[0], errText(again[0]))
	}
	if len(a.h.signer.calls) != 1 {
		t.Fatalf("nothing attests twice: signatures %d", len(a.h.signer.calls))
	}
}

// TestPodAttestReplayGone_AMissingStopKeepsTheBoundAnswer: the stop the attestation create answered
// is gone entirely; the same owner guarantee.
func TestPodAttestReplayGone_AMissingStopKeepsTheBoundAnswer(t *testing.T) {
	a := newAttestWorld(t)
	ctx := context.Background()
	op := attestOp(syncFreshKey(), a.stopA2, "Site Foreman", true)
	if first := a.push(a.driverA, op); first[0].Status != "applied" {
		t.Fatalf("setup: the attestation applies: %+v (%s)", first[0], errText(first[0]))
	}
	if _, err := a.pool.Exec(ctx, `DELETE FROM delivery_stops WHERE id = $1::uuid`, a.stopA2); err != nil {
		t.Fatalf("drop the stop: %v", err)
	}
	again := a.push(a.driverA, op)
	if again[0].Status != "applied" || again[0].Seq != nil {
		t.Fatalf("the owner's replay of an applied key when the stop is gone answers applied with no seq: got %+v (%s)", again[0], errText(again[0]))
	}
	if len(a.h.signer.calls) != 1 {
		t.Fatalf("nothing attests twice: signatures %d", len(a.h.signer.calls))
	}
}

// TestPodAttest_LoadAnswersOnlyAStopThatCarriesAnAttestation: the attestation arm of Load reads the
// typed name proof, not any delivery. A stop delivered with a photo (a signature ref, no received
// name) stays not found; an attested stop answers, as the replay needs.
func TestPodAttest_LoadAnswersOnlyAStopThatCarriesAnAttestation(t *testing.T) {
	a := newAttestWorld(t)
	ctx := globalCtx(t, a.pool)
	if first := a.push(a.driverA, attestOp(syncFreshKey(), a.stopA2, "Site Foreman", true)); first[0].Status != "applied" {
		t.Fatalf("setup: the attestation applies: %+v (%s)", first[0], errText(first[0]))
	}
	if _, found, err := a.pod.Load(ctx, a.driverA, a.stopA2); err != nil || !found {
		t.Fatalf("the control: an attested stop answers Load: found %v (%v)", found, err)
	}
	// A photo delivered stop: the photo door's own proof shape, no typed name ever taken. The
	// world's second stop sits on the colleague driver's run, so the colleague is its owner here.
	photoStop := a.stopB
	if _, err := a.pool.Exec(ctx,
		`UPDATE delivery_stops SET status = 'DELIVERED', pod_signature_ref = $2, pod_at = now() WHERE id = $1::uuid`,
		photoStop, "photo-ref-"+uuid.NewString()); err != nil {
		t.Fatalf("deliver by photo: %v", err)
	}
	if _, found, err := a.pod.Load(ctx, a.driverB, photoStop); err != nil || found {
		t.Fatalf("a photo delivered stop stays not found: found %v (%v)", found, err)
	}
}

// TestPodAttest_AnUpdateOrDeleteNamingAnAttestationStopIsRefusedNotFound: the stop is the replay's
// row, not a writable one. An update naming the stop id, stale base included, is the row not found
// refusal with no conflict envelope, and so is a delete.
func TestPodAttest_AnUpdateOrDeleteNamingAnAttestationStopIsRefusedNotFound(t *testing.T) {
	a := newAttestWorld(t)
	if first := a.push(a.driverA, attestOp(syncFreshKey(), a.stopA2, "Site Foreman", true)); first[0].Status != "applied" {
		t.Fatalf("setup: the attestation applies: %+v (%s)", first[0], errText(first[0]))
	}
	before := a.stop(a.stopA2).seq
	old := before - 1
	upd := syncOp("update", syncFreshKey(), map[string]any{"id": a.stopA2, "caption": "late edit"}, &old)
	if res := a.push(a.driverA, upd); res[0].Status != "rejected" || !strings.Contains(errText(res[0]), "row not found") || res[0].Conflict != nil {
		t.Fatalf("an update naming an attestation stop id is refused row not found with no conflict envelope: got %+v (%s)", res[0], errText(res[0]))
	}
	del := syncOp("delete", syncFreshKey(), map[string]any{"id": a.stopA2}, nil)
	if res := a.push(a.driverA, del); res[0].Status != "rejected" || !strings.Contains(errText(res[0]), "row not found") || res[0].Conflict != nil {
		t.Fatalf("a delete naming an attestation stop id is refused row not found with no conflict envelope: got %+v (%s)", res[0], errText(res[0]))
	}
	if len(a.h.signer.calls) != 1 {
		t.Fatalf("nothing runs twice: signatures %d", len(a.h.signer.calls))
	}
}
