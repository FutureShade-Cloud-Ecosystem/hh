//go:build integration

// w3_integration_test.go, the Wave 3 guards against a real, migrated Postgres.
//
//	DATABASE_URL=... go test -tags integration -count=1 -run W3 ./internal/dispatch/
//
// WHAT ONLY A DATABASE CAN PROVE, AND IS THEREFORE PROVED HERE:
//   - the 0107 CHECKs (an mto_class outside the three, a half-attached photo, a reschedule to the
//     same date) actually refuse, a Go-side constant is not a schema;
//   - the reschedule and its REASON commit together, and a guard that bites leaves NEITHER;
//   - the 0108 UNIQUE key makes a re-scan an update rather than a second row, in the database, which
//     is where two scanners racing actually meet;
//   - POD photo attach is idempotent across a real UPDATE and ties real rows to a real attestation.
//
// FIXTURES RESTORE WHAT THEY TOUCH. Everything created here is deleted on cleanup and nothing seeded
// is written, so the file is order-independent and safe beside another suite. No whole-DB invariant
// is transiently broken (no stock moves, no payment_config), so no DIB-396 advisory lease is taken.

package dispatch

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func w3Harness(t *testing.T) (*harness, context.Context, string) {
	t.Helper()
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	orderID := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	t.Cleanup(func() {
		c := context.Background()
		_, _ = h.pool.Exec(c, `DELETE FROM load_scan_checkpoints WHERE order_id = $1`, orderID)
		_, _ = h.pool.Exec(c, `DELETE FROM dispatch_reschedules WHERE entity_id IN (SELECT id FROM delivery_stops WHERE order_id = $1)`, orderID)
	})
	return h, ctx, orderID
}

func w3Schedule(t *testing.T, h *harness, ctx context.Context, orderID, date string) Stop {
	t.Helper()
	stop, err := h.svc.Schedule(ctx, ScheduleInput{
		OrderID: orderID, ScheduledDate: date, AddressLine: "88 Concession Rd 4",
		City: "Trenton", PostalCode: "K8V 5R5", ContactName: "Jo",
	})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	return stop
}

func tomorrow() string  { return time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02") }
func nextWeek() string  { return time.Now().UTC().AddDate(0, 0, 7).Format("2006-01-02") }
func yesterday() string { return time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02") }

// - DIB-382 · reschedule -

func TestW3Reschedule_MovesTheStopAndRecordsWhy(t *testing.T) {
	h, ctx, orderID := w3Harness(t)
	stop := w3Schedule(t, h, ctx, orderID, tomorrow())

	res, err := h.svc.RescheduleStop(ctx, stop.ID, RescheduleInput{
		ToDate: nextWeek(), Reason: "customer is away until Monday",
	})
	if err != nil {
		t.Fatalf("reschedule: %v", err)
	}
	if res.ToDate != nextWeek() || res.StopsMoved != 1 {
		t.Fatalf("result = %+v", res)
	}
	after, err := h.svc.StopDetail(ctx, stop.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if after.ScheduledDate != nextWeek() {
		t.Fatalf("scheduled_date = %s, want %s", after.ScheduledDate, nextWeek())
	}
	// The reason is the point of the ticket. A date that changed with no recorded reason is the hole
	// DIB-382 exists to close.
	trail, err := h.svc.RescheduleHistory(ctx, RescheduleEntityStop, stop.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(trail) != 1 {
		t.Fatalf("trail rows = %d, want 1", len(trail))
	}
	if trail[0].Reason != "customer is away until Monday" {
		t.Fatalf("reason = %q", trail[0].Reason)
	}
	if trail[0].FromDate != tomorrow() || trail[0].ToDate != nextWeek() {
		t.Fatalf("trail dates = %s → %s", trail[0].FromDate, trail[0].ToDate)
	}
	if trail[0].ActorID == "" {
		t.Fatal("the trail must name who moved it")
	}
}

func TestW3Reschedule_RefusesABackwardMoveThroughTheLifecycle(t *testing.T) {
	// THE GUARD THE WALKTHROUGH DEMONSTRATES. A stop that is on a truck cannot have its promise date
	// changed: the date is a record of something that is happening, not a plan.
	h, ctx, orderID := w3Harness(t)
	stop := w3Schedule(t, h, ctx, orderID, tomorrow())
	run := seedRun(t, h, ctx, branchTRT, tomorrow(), true)
	if _, err := h.svc.AssignStop(ctx, stop.ID, AssignInput{RunID: run.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := h.svc.DispatchRun(ctx, run.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	// The stop is now LOADED.
	_, err := h.svc.RescheduleStop(ctx, stop.ID, RescheduleInput{ToDate: nextWeek(), Reason: "try it on"})
	if !errors.Is(err, ErrBackwardReschedule) {
		t.Fatalf("err = %v, want ErrBackwardReschedule", err)
	}
	// And NOTHING was recorded, a refused move must not leave a trail row claiming it happened.
	trail, _ := h.svc.RescheduleHistory(ctx, RescheduleEntityStop, stop.ID)
	if len(trail) != 0 {
		t.Fatalf("a refused move wrote %d trail rows", len(trail))
	}
	after, _ := h.svc.StopDetail(ctx, stop.ID)
	if after.ScheduledDate != tomorrow() {
		t.Fatalf("the date moved anyway: %s", after.ScheduledDate)
	}
}

func TestW3Reschedule_RefusesAStopThatIsOnARunAndSaysWhatToDo(t *testing.T) {
	h, ctx, orderID := w3Harness(t)
	stop := w3Schedule(t, h, ctx, orderID, tomorrow())
	run := seedRun(t, h, ctx, branchTRT, tomorrow(), false)
	if _, err := h.svc.AssignStop(ctx, stop.ID, AssignInput{RunID: run.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	_, err := h.svc.RescheduleStop(ctx, stop.ID, RescheduleInput{ToDate: nextWeek(), Reason: "customer"})
	if err == nil {
		t.Fatal("a stop on a run cannot be moved off its run's date")
	}
	if !strings.Contains(err.Error(), "run") {
		t.Fatalf("the refusal must name the two ways out, got %q", err)
	}
}

func TestW3Reschedule_RefusesAPastDate(t *testing.T) {
	h, ctx, orderID := w3Harness(t)
	stop := w3Schedule(t, h, ctx, orderID, tomorrow())
	if _, err := h.svc.RescheduleStop(ctx, stop.ID, RescheduleInput{
		ToDate: yesterday(), Reason: "tidy the report",
	}); err == nil {
		t.Fatal("a delivery cannot be promised for a day that has gone")
	}
}

func TestW3Reschedule_RefusesAMoveWithNoReason(t *testing.T) {
	h, ctx, orderID := w3Harness(t)
	stop := w3Schedule(t, h, ctx, orderID, tomorrow())
	if _, err := h.svc.RescheduleStop(ctx, stop.ID, RescheduleInput{ToDate: nextWeek()}); err == nil {
		t.Fatal("the customer will ask why, the system must be able to answer")
	}
	trail, _ := h.svc.RescheduleHistory(ctx, RescheduleEntityStop, stop.ID)
	if len(trail) != 0 {
		t.Fatal("a refused move must write no trail")
	}
}

func TestW3Reschedule_ARunCarriesItsStopsWithIt(t *testing.T) {
	h, ctx, orderID := w3Harness(t)
	stop := w3Schedule(t, h, ctx, orderID, tomorrow())
	run := seedRun(t, h, ctx, branchTRT, tomorrow(), false)
	if _, err := h.svc.AssignStop(ctx, stop.ID, AssignInput{RunID: run.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	res, err := h.svc.RescheduleRun(ctx, run.ID, RescheduleInput{ToDate: nextWeek(), Reason: "truck in the shop"})
	if err != nil {
		t.Fatalf("reschedule run: %v", err)
	}
	if res.StopsMoved != 1 {
		t.Fatalf("stops moved = %d, want 1, the run and its stops must not drift apart", res.StopsMoved)
	}
	after, _ := h.svc.StopDetail(ctx, stop.ID)
	if after.ScheduledDate != nextWeek() {
		t.Fatalf("the stop did not move with its run: %s", after.ScheduledDate)
	}
	moved, _ := h.svc.RunDetail(ctx, run.ID)
	if moved.ScheduledDate != nextWeek() {
		t.Fatalf("the run did not move: %s", moved.ScheduledDate)
	}
}

func TestW3Reschedule_ADispatchedRunIsRefused(t *testing.T) {
	h, ctx, orderID := w3Harness(t)
	stop := w3Schedule(t, h, ctx, orderID, tomorrow())
	run := seedRun(t, h, ctx, branchTRT, tomorrow(), true)
	if _, err := h.svc.AssignStop(ctx, stop.ID, AssignInput{RunID: run.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := h.svc.DispatchRun(ctx, run.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := h.svc.RescheduleRun(ctx, run.ID, RescheduleInput{
		ToDate: nextWeek(), Reason: "the truck has gone",
	}); !errors.Is(err, ErrBackwardReschedule) {
		t.Fatalf("err = %v, want ErrBackwardReschedule", err)
	}
}

// - DIB-63 · MTO + POD attach -

func TestW3MTO_ClassificationReachesTheManifestAndRollsUp(t *testing.T) {
	h, ctx, orderID := w3Harness(t)
	stop := w3Schedule(t, h, ctx, orderID, tomorrow())
	run := seedRun(t, h, ctx, branchTRT, tomorrow(), true)
	if _, err := h.svc.AssignStop(ctx, stop.ID, AssignInput{RunID: run.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	w := 820.0
	if _, err := h.svc.SetStopMTO(ctx, stop.ID, MTOInput{
		Class: MTOMade, Note: "cut to 1150mm, do not tip", WeightKg: &w,
	}); err != nil {
		t.Fatalf("set mto: %v", err)
	}
	m, err := h.svc.Manifest(ctx, run.ID)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if m.MTO.Class != MTOMade || m.MTO.MTOStops != 1 {
		t.Fatalf("roll-up = %+v, want a made-to-order run with 1 MTO stop", m.MTO)
	}
	if len(m.MTOStops) != 1 || m.MTOStops[0].MTO.Note != "cut to 1150mm, do not tip" {
		t.Fatalf("the handling note did not reach the sheet: %+v", m.MTOStops)
	}
	html, err := RenderManifestHTML(m)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "MADE TO ORDER") || !strings.Contains(html, "cut to 1150mm") {
		t.Fatal("the printed sheet must carry the class and the handling note")
	}
}

func TestW3MTO_TheSchemaRefusesAClassOutsideTheThree(t *testing.T) {
	// The service refuses first with a sentence; this proves the 0107 CHECK is the backstop behind it
	// for any other writer (a script, a hand-run UPDATE).
	h, ctx, orderID := w3Harness(t)
	stop := w3Schedule(t, h, ctx, orderID, tomorrow())
	if _, err := h.svc.SetStopMTO(ctx, stop.ID, MTOInput{Class: "CUSTOM"}); err == nil {
		t.Fatal("the service must refuse an unknown class")
	}
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE delivery_stops SET mto_class = 'CUSTOM' WHERE id = $1`, stop.ID); err == nil {
		t.Fatal("0107's CHECK must refuse a class outside the three even for a direct UPDATE")
	}
}

func TestW3MTO_AnEnRouteStopCanNoLongerBeReclassified(t *testing.T) {
	h, ctx, orderID := w3Harness(t)
	stop := w3Schedule(t, h, ctx, orderID, tomorrow())
	run := seedRun(t, h, ctx, branchTRT, tomorrow(), true)
	if _, err := h.svc.AssignStop(ctx, stop.ID, AssignInput{RunID: run.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := h.svc.DispatchRun(ctx, run.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := h.svc.AdvanceStop(ctx, stop.ID, StopEnRoute, ""); err != nil {
		t.Fatalf("en route: %v", err)
	}
	if _, err := h.svc.SetStopMTO(ctx, stop.ID, MTOInput{Class: MTOMade}); err == nil {
		t.Fatal("reclassifying a load that has left would rewrite the manifest that went out with it")
	}
}

func TestW3POD_PhotosAreAttachedToTheAttestationOnCapture(t *testing.T) {
	h, ctx, orderID := w3Harness(t)
	stop := w3Schedule(t, h, ctx, orderID, tomorrow())
	run := seedRun(t, h, ctx, branchTRT, tomorrow(), true)
	if _, err := h.svc.AssignStop(ctx, stop.ID, AssignInput{RunID: run.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := h.svc.DispatchRun(ctx, run.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := h.svc.AdvanceStop(ctx, stop.ID, StopEnRoute, ""); err != nil {
		t.Fatalf("en route: %v", err)
	}
	png := []byte("\x89PNG\r\n\x1a\n-fake-bytes-for-a-pallet-on-a-driveway")
	if _, err := h.svc.AddPhoto(ctx, stop.ID, "image/png", "pallet on the pad", png); err != nil {
		t.Fatalf("photo: %v", err)
	}

	// Before the POD: the photo exists and is NOT part of any sealed record.
	var attached int
	if err := h.pool.QueryRow(context.Background(),
		`SELECT count(*)::int FROM delivery_pod_photos WHERE stop_id = $1 AND pod_attached_at IS NOT NULL`,
		stop.ID).Scan(&attached); err != nil {
		t.Fatalf("count: %v", err)
	}
	if attached != 0 {
		t.Fatal("a photo taken before the signature is not yet evidence of the delivery")
	}

	detail, err := h.svc.CapturePOD(ctx, stop.ID, PODInput{ReceivedBy: "Dana Whitfield", Affirmed: true})
	if err != nil {
		t.Fatalf("pod: %v", err)
	}
	var ref string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT pod_signature_ref FROM delivery_pod_photos WHERE stop_id = $1`, stop.ID).Scan(&ref); err != nil {
		t.Fatalf("read photo: %v", err)
	}
	if ref == "" || ref != detail.PODSignedRef {
		t.Fatalf("photo attestation ref = %q, want the stop's %q", ref, detail.PODSignedRef)
	}

	// A LATE photo, the dead-signal retry the module was built to tolerate, attaches on the re-run,
	// and the already-attached one is not re-stamped.
	if _, err := h.svc.AddPhoto(ctx, stop.ID, "image/png", "gate closed behind us", png); err != nil {
		t.Fatalf("late photo: %v", err)
	}
	if _, err := h.svc.AttachStopPhotos(ctx, stop.ID); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := h.pool.QueryRow(context.Background(),
		`SELECT count(*)::int FROM delivery_pod_photos WHERE stop_id = $1 AND pod_signature_ref = $2`,
		stop.ID, detail.PODSignedRef).Scan(&attached); err != nil {
		t.Fatalf("count: %v", err)
	}
	if attached != 2 {
		t.Fatalf("attached photos = %d, want 2 (the late one folded in on the retry)", attached)
	}
}

func TestW3POD_TheSchemaRefusesAHalfAttachedPhoto(t *testing.T) {
	h, ctx, orderID := w3Harness(t)
	stop := w3Schedule(t, h, ctx, orderID, tomorrow())
	if _, err := h.pool.Exec(context.Background(), `
INSERT INTO delivery_pod_photos (stop_id, branch_id, content_type, byte_size, image_bytes, pod_attached_at)
VALUES ($1, $2, 'image/png', 4, '\xdeadbeef'::bytea, now())`, stop.ID, branchTRT); err == nil {
		t.Fatal("0107's CHECK must refuse a timestamp with no attestation ref, a half-attach is not a fact")
	}
}

// - DIB-75 · the scan trail -

func TestW3Scan_ForwardOnlyAndIdempotentAtTheSameCheckpoint(t *testing.T) {
	h, ctx, orderID := w3Harness(t)
	stop := w3Schedule(t, h, ctx, orderID, tomorrow())

	for _, cp := range []string{ScanReceived, ScanYard, ScanPicked} {
		if _, err := h.svc.RecordScan(ctx, ScanInput{
			StopID: stop.ID, LoadRef: "PAL-77", Checkpoint: cp,
		}); err != nil {
			t.Fatalf("scan %s: %v", cp, err)
		}
	}
	// The gun beeped twice. That is not a second event.
	if _, err := h.svc.RecordScan(ctx, ScanInput{
		StopID: stop.ID, LoadRef: "PAL-77", Checkpoint: ScanPicked, Note: "re-scan",
	}); err != nil {
		t.Fatalf("re-scan must be accepted: %v", err)
	}
	trail, err := h.svc.ScanTrailForOrder(ctx, orderID)
	if err != nil {
		t.Fatalf("trail: %v", err)
	}
	if len(trail.Checkpoints) != 3 {
		t.Fatalf("checkpoints = %d, want 3, a re-scan updates the row, it does not append one", len(trail.Checkpoints))
	}
	// A pallet does not un-leave the yard.
	if _, err := h.svc.RecordScan(ctx, ScanInput{
		StopID: stop.ID, LoadRef: "PAL-77", Checkpoint: ScanYard,
	}); !errors.Is(err, ErrBackwardScan) {
		t.Fatalf("err = %v, want ErrBackwardScan", err)
	}
	// Case does not matter to a scan gun.
	if _, err := h.svc.RecordScan(ctx, ScanInput{
		StopID: stop.ID, LoadRef: "pal-77", Checkpoint: ScanReceived,
	}); !errors.Is(err, ErrBackwardScan) {
		t.Fatalf("a lower-case re-read of the same label must be the SAME load, got %v", err)
	}
}

func TestW3Scan_ARefusedCheckpointNameIsRefused(t *testing.T) {
	h, ctx, orderID := w3Harness(t)
	stop := w3Schedule(t, h, ctx, orderID, tomorrow())
	if _, err := h.svc.RecordScan(ctx, ScanInput{
		StopID: stop.ID, LoadRef: "PAL-1", Checkpoint: "SOMEWHERE",
	}); err == nil {
		t.Fatal("an unknown checkpoint must be refused with the five names")
	}
	if _, err := h.svc.RecordScan(ctx, ScanInput{
		StopID: stop.ID, Checkpoint: ScanYard,
	}); err == nil {
		t.Fatal("a scan with no load reference records nothing anybody can find")
	}
}

func TestW3Scan_TheTrailBeginsBeforeAnyStopExists(t *testing.T) {
	// RECEIVED and YARD happen before a delivery is scheduled. These are the two checkpoints nobody
	// has today, and refusing them until a stop exists would have kept it that way.
	h, ctx, orderID := w3Harness(t)
	if _, err := h.svc.RecordScan(ctx, ScanInput{
		OrderID: orderID, LoadRef: "PAL-9", Checkpoint: ScanReceived,
	}); err != nil {
		t.Fatalf("scan against an order with no stop: %v", err)
	}
	trail, err := h.svc.ScanTrailForOrder(ctx, orderID)
	if err != nil {
		t.Fatalf("trail: %v", err)
	}
	if len(trail.Loads) != 1 || trail.Loads[0].Furthest != ScanReceived {
		t.Fatalf("trail = %+v", trail.Loads)
	}
}

func TestW3Scan_PODTiesTheFinalCheckpoint(t *testing.T) {
	// The DIB-75 tie: the trail's end is not "somebody pressed delivered", it is "this signature says
	// it arrived".
	h, ctx, orderID := w3Harness(t)
	stop := w3Schedule(t, h, ctx, orderID, tomorrow())
	run := seedRun(t, h, ctx, branchTRT, tomorrow(), true)
	if _, err := h.svc.AssignStop(ctx, stop.ID, AssignInput{RunID: run.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := h.svc.DispatchRun(ctx, run.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := h.svc.AdvanceStop(ctx, stop.ID, StopEnRoute, ""); err != nil {
		t.Fatalf("en route: %v", err)
	}
	for _, cp := range []string{ScanReceived, ScanYard, ScanPicked, ScanInTransit, ScanDelivered} {
		if _, err := h.svc.RecordScan(ctx, ScanInput{
			StopID: stop.ID, LoadRef: "PAL-42", Checkpoint: cp,
		}); err != nil {
			t.Fatalf("scan %s: %v", cp, err)
		}
	}
	detail, err := h.svc.CapturePOD(ctx, stop.ID, PODInput{ReceivedBy: "Dana Whitfield", Affirmed: true})
	if err != nil {
		t.Fatalf("pod: %v", err)
	}
	trail, err := h.svc.ScanTrailForOrder(ctx, orderID)
	if err != nil {
		t.Fatalf("trail: %v", err)
	}
	if !trail.Complete {
		t.Fatal("every load delivered means the trail is complete")
	}
	if trail.Loads[0].PODSignatureRef != detail.PODSignedRef {
		t.Fatalf("the final checkpoint is not tied to the attestation: %q vs %q",
			trail.Loads[0].PODSignatureRef, detail.PODSignedRef)
	}
}

func TestW3Scan_TheSchemaRefusesAPODRefOnANonFinalCheckpoint(t *testing.T) {
	h, ctx, orderID := w3Harness(t)
	stop := w3Schedule(t, h, ctx, orderID, tomorrow())
	if _, err := h.pool.Exec(context.Background(), `
INSERT INTO load_scan_checkpoints (branch_id, order_id, stop_id, load_ref, checkpoint, pod_signature_ref)
VALUES ($1, $2, $3, 'PAL-BAD', 'YARD', 'sig_x')`, branchTRT, orderID, stop.ID); err == nil {
		t.Fatal("0108's CHECK must keep the attestation on the FINAL checkpoint, a mid-trail POD is ambiguous")
	}
}

// - the AR/COD gate against the real dispatch path -

type denyGate struct{ orders map[string]bool }

func (g denyGate) AllowDispatch(_ context.Context, orderID string) (DispatchGateDecision, error) {
	if g.orders[orderID] {
		return DispatchGateDecision{
			Allow: false, Code: "COD_UNPAID", AmountDueCents: 124000,
			Reason: "COD balance of $1,240.00 is outstanding on this order.",
		}, nil
	}
	return DispatchGateDecision{Allow: true}, nil
}

func TestW3Gate_AHeldOrderStopsTheRunAndFeedsTheDriverBanner(t *testing.T) {
	h, ctx, orderID := w3Harness(t)
	stop := w3Schedule(t, h, ctx, orderID, tomorrow())
	run := seedRun(t, h, ctx, branchTRT, tomorrow(), true)
	if _, err := h.svc.AssignStop(ctx, stop.ID, AssignInput{RunID: run.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	h.svc.WithARGate(denyGate{orders: map[string]bool{orderID: true}})

	if _, err := h.svc.DispatchRun(ctx, run.ID); err == nil {
		t.Fatal("a run carrying a held order must not leave the yard")
	} else if !strings.Contains(err.Error(), "COD balance") {
		t.Fatalf("the refusal must carry the gate's own sentence, got %q", err)
	}

	hold, err := h.svc.PaymentHoldForStop(ctx, stop.ID)
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if !hold.Held || hold.Code != "COD_UNPAID" || hold.AmountDueCents != 124000 {
		t.Fatalf("driver banner payload = %+v", hold)
	}

	// Clear the hold and the same run dispatches, the gate is a predicate, not a status.
	h.svc.WithARGate(denyGate{orders: map[string]bool{}})
	if _, err := h.svc.DispatchRun(ctx, run.ID); err != nil {
		t.Fatalf("dispatch after the hold clears: %v", err)
	}
}

func TestW3Gate_UnwiredIsIndistinguishableFromWave2(t *testing.T) {
	h, ctx, orderID := w3Harness(t)
	stop := w3Schedule(t, h, ctx, orderID, tomorrow())
	run := seedRun(t, h, ctx, branchTRT, tomorrow(), true)
	if _, err := h.svc.AssignStop(ctx, stop.ID, AssignInput{RunID: run.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	// No WithARGate call at all, the shipped default.
	if _, err := h.svc.DispatchRun(ctx, run.ID); err != nil {
		t.Fatalf("an unwired gate must change nothing: %v", err)
	}
	hold, err := h.svc.PaymentHoldForStop(ctx, stop.ID)
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if hold.Held {
		t.Fatal("an unwired gate must never hold a stop")
	}
}
