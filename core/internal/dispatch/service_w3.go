package dispatch

// service_w3.go, the Wave 3 guards: MTO classification (DIB-63), the audited delivery-date
// reschedule (DIB-382), the pallet/load scan trail (DIB-75) and the optional AR dispatch gate.
//
// THE FENCE IS THE SAME FENCE. Every entry point below resolves the trusted, server-stamped caller
// and asks the one question service.go's header states: can this person reach the BRANCH this row
// belongs to. Nothing here introduces a second authority model; guardStop and guardRun are reused
// verbatim so a Wave 3 route cannot be fenced differently from the Wave 2 route beside it.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// - DIB-63 · MTO classification -

// SetStopMTO classifies a stop's material and records the handling note the driver reads.
//
// PRE-DEPARTURE ONLY. Once a stop is EN_ROUTE the classification is a description of what is on a
// truck that has already left, and editing it would rewrite the manifest that was printed for that
// load. READY and LOADED are the two states where the sheet has not gone out the door yet.
func (s *Service) SetStopMTO(ctx context.Context, stopID string, in MTOInput) (StopDetail, error) {
	stop, err := s.guardStop(ctx, stopID)
	if err != nil {
		return StopDetail{}, err
	}
	class := strings.ToUpper(strings.TrimSpace(in.Class))
	if class == "" {
		class = MTOStandard
	}
	if !validMTOClass(class) {
		return StopDetail{}, httpx.BadRequestf("dispatch: unknown mto_class %q (STANDARD, MTO or MIXED)", in.Class)
	}
	note := strings.TrimSpace(in.Note)
	if len(note) > 400 {
		return StopDetail{}, httpx.BadRequestf("dispatch: the handling note is too long (max 400)")
	}
	// An MTO or MIXED load with no note is allowed but is the case the manifest calls out: the class
	// alone tells a driver the material is not interchangeable, not what to do about it.
	if in.WeightKg != nil && (*in.WeightKg < 0 || *in.WeightKg > 100_000) {
		return StopDetail{}, httpx.BadRequestf("dispatch: weight_kg must be between 0 and 100000")
	}
	ok, err := s.repo.SetStopMTO(ctx, stop.ID, class, note, in.WeightKg)
	if err != nil {
		return StopDetail{}, err
	}
	if !ok {
		return StopDetail{}, conflictf("dispatch: a %s stop's load classification can no longer be changed", stop.Status)
	}
	s.record(ctx, audit.SensitiveAccess{
		Action: "DELIVERY_STOP_MTO_SET", EntityType: "delivery_stop", EntityID: stop.ID,
		Extra: map[string]any{"mto_class": class},
	})
	return s.StopDetail(ctx, stop.ID)
}

// StopMTO reads one stop's compliance trio (fenced).
func (s *Service) StopMTO(ctx context.Context, stopID string) (StopMTO, error) {
	stop, err := s.guardStop(ctx, stopID)
	if err != nil {
		return StopMTO{}, err
	}
	class, note, weight, err := s.repo.StopMTO(ctx, stop.ID)
	if err != nil {
		return StopMTO{}, err
	}
	return StopMTO{Class: class, Note: note, WeightKg: weight}, nil
}

// rollUpMTO computes the run-level summary from its stops. Stated as a pure function over the stop
// list (plus the truck's advisory capacity) so the rule, one MTO stop makes the RUN mixed, is
// testable without a database.
func rollUpMTO(stops []ManifestStop, capacityKg *float64) MTOSummary {
	sum := MTOSummary{Class: MTOStandard, CapacityKg: capacityKg}
	if len(stops) == 0 {
		return sum
	}
	sawStandard, sawMTO := false, false
	for _, st := range stops {
		switch st.MTO.Class {
		case MTOMade:
			sawMTO = true
			sum.MTOStops++
		case MTOMixed:
			// A mixed stop is BOTH: it carries made-to-order material and standard material, so it
			// contributes to each side of the roll-up rather than to a third bucket.
			sawMTO, sawStandard = true, true
			sum.MTOStops++
		default:
			sawStandard = true
		}
		if st.MTO.WeightKg != nil {
			sum.WeighedStops++
			sum.TotalWeightKg += *st.MTO.WeightKg
		} else {
			sum.UnweighedStops++
		}
	}
	switch {
	case sawMTO && sawStandard:
		sum.Class = MTOMixed
	case sawMTO:
		sum.Class = MTOMade
	default:
		sum.Class = MTOStandard
	}
	// A capacity warning is raised ONLY on a complete total. Half a load's weight compared against a
	// truck's capacity is a number that means nothing, and a warning that means nothing gets ignored
	// on the day it is right.
	if capacityKg != nil && sum.UnweighedStops == 0 && sum.WeighedStops > 0 && sum.TotalWeightKg > *capacityKg {
		sum.OverCapacity = true
	}
	return sum
}

// - DIB-63 · POD photo auto-attach -

// afterPOD is the DIB-63 follow-through, called from CapturePOD once the attestation and the status
// have been written together.
//
// BEST-EFFORT, ON PURPOSE, AND EACH FAILURE IS SEPARATE. The delivery is already evidenced on the
// stop by the time this runs. Rolling a recorded delivery back because a photo-attach UPDATE or a
// scan-trail stamp failed would destroy the fact to preserve its decoration. Both gaps are queryable
// afterwards (a photo with a NULL pod_attached_at, a DELIVERED checkpoint with a blank ref), which is
// the handoff_at doctrine applied to two more tables.
func (s *Service) afterPOD(ctx context.Context, stop Stop, sigRef string, at time.Time) {
	if n, err := s.repo.AttachPODPhotos(ctx, stop.ID, sigRef, at); err != nil {
		s.log.Warn("dispatch: POD photo attach failed", "stop", stop.ID, "err", err)
	} else if n > 0 {
		s.record(ctx, audit.SensitiveAccess{
			Action: "DELIVERY_POD_PHOTOS_ATTACHED", EntityType: "delivery_stop", EntityID: stop.ID,
			MaskedRef: sigRef, Extra: map[string]any{"photos": n},
		})
	}
	if _, err := s.repo.TieScanTrailToPOD(ctx, stop.OrderID, sigRef); err != nil {
		s.log.Warn("dispatch: scan trail POD tie failed", "stop", stop.ID, "order", stop.OrderID, "err", err)
	}
}

// AttachStopPhotos re-runs the attach for a stop that already has a POD, the driver whose phone
// finally got signal and uploaded the pallet photo twenty minutes after signing. Fenced, idempotent,
// and refused on a stop with no attestation (there is nothing to attach the photo TO).
func (s *Service) AttachStopPhotos(ctx context.Context, stopID string) (StopDetail, error) {
	stop, err := s.guardStop(ctx, stopID)
	if err != nil {
		return StopDetail{}, err
	}
	if stop.PODSignedRef == "" {
		return StopDetail{}, conflictf("dispatch: this stop has no proof-of-delivery to attach photos to yet")
	}
	if _, err := s.repo.AttachPODPhotos(ctx, stop.ID, stop.PODSignedRef, s.now().UTC()); err != nil {
		return StopDetail{}, err
	}
	return s.StopDetail(ctx, stop.ID)
}

// - DIB-382 · reschedule -

// RescheduleStop moves ONE unassigned stop to another date, with a stated reason, audited.
//
// THE THREE REFUSALS, AND WHY EACH ONE.
//
//   - NOT BACKWARD THROUGH THE LIFECYCLE. Only a READY stop may be rescheduled. Once a stop is
//     LOADED it is on a truck, and once it is EN_ROUTE / DELIVERED / FAILED its date is a fact about
//     something that happened. Changing the date of a delivery that already occurred does not move
//     the delivery; it falsifies the record of it.
//   - NOT INTO THE PAST. A delivery cannot be promised for a day that has gone. This is separate from
//     the lifecycle refusal above and catches the other direction: a READY stop being back-dated to
//     tidy a report.
//   - NOT OFF ITS RUN'S DATE. A stop assigned to a run must share the run's date (AssignStop enforces
//     it). Moving the stop alone would create a pairing the board itself refuses to make, so this
//     says so and names the two ways out, unassign the stop, or move the whole run.
func (s *Service) RescheduleStop(ctx context.Context, stopID string, in RescheduleInput) (RescheduleResult, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return RescheduleResult{}, err
	}
	stop, err := s.guardStop(ctx, stopID)
	if err != nil {
		return RescheduleResult{}, err
	}
	to, reason, err := s.validateReschedule(in, stop.ScheduledDate)
	if err != nil {
		return RescheduleResult{}, err
	}
	if stop.Status != StopReady {
		return RescheduleResult{}, ErrBackwardReschedule
	}
	if stop.RunID != "" {
		return RescheduleResult{}, conflictf(
			"dispatch: this stop is on run %s, take it off the run first, or reschedule the whole run", orDash(stop.RunNo))
	}
	trail := Reschedule{
		BranchID: stop.BranchID, EntityType: RescheduleEntityStop, EntityID: stop.ID,
		FromDate: stop.ScheduledDate, ToDate: to, Reason: reason, ActorID: caller.UserID,
	}
	rid, ok, err := s.repo.MoveStopDate(ctx, stop.ID, stop.ScheduledDate, to, trail)
	if err != nil {
		return RescheduleResult{}, err
	}
	if !ok {
		// The row moved under us between the guard and the write, a driver pressed "loaded", or a
		// second dispatcher rescheduled it first. Same answer either way: the world moved on.
		return RescheduleResult{}, ErrBackwardReschedule
	}
	s.record(ctx, audit.SensitiveAccess{
		Action: "DELIVERY_STOP_RESCHEDULED", EntityType: "delivery_stop", EntityID: stop.ID,
		Extra: map[string]any{"from": stop.ScheduledDate, "to": to, "reason": reason},
	})
	moved, err := s.repo.GetStop(ctx, stop.ID)
	if err != nil {
		return RescheduleResult{}, err
	}
	history, _ := s.repo.ListReschedules(ctx, RescheduleEntityStop, stop.ID)
	return RescheduleResult{
		EntityType: RescheduleEntityStop, EntityID: stop.ID, FromDate: stop.ScheduledDate, ToDate: to,
		StopsMoved: 1, RescheduleID: rid, Stop: &moved, History: history,
	}, nil
}

// RescheduleRun moves a PLANNED run and every READY stop on it to another date, with a stated reason.
//
// A DISPATCHED run is refused for the same reason a LOADED stop is: the truck has gone, and the date
// on the row is now a record rather than a plan.
func (s *Service) RescheduleRun(ctx context.Context, runID string, in RescheduleInput) (RescheduleResult, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return RescheduleResult{}, err
	}
	run, err := s.guardRun(ctx, runID)
	if err != nil {
		return RescheduleResult{}, err
	}
	to, reason, err := s.validateReschedule(in, run.ScheduledDate)
	if err != nil {
		return RescheduleResult{}, err
	}
	if run.Status != RunPlanned {
		return RescheduleResult{}, ErrBackwardReschedule
	}
	// 0185, moving a COMMITTED load's DATE is the largest load change there is: it takes the delivery
	// out of the window the customer was told about. The commitment holds it.
	if err := s.assertRunEditable(ctx, run); err != nil {
		return RescheduleResult{}, err
	}
	trail := Reschedule{
		BranchID: run.BranchID, EntityType: RescheduleEntityRun, EntityID: run.ID,
		FromDate: run.ScheduledDate, ToDate: to, Reason: reason, ActorID: caller.UserID,
	}
	rid, moved, ok, err := s.repo.MoveRunDate(ctx, run.ID, run.ScheduledDate, to, trail)
	if err != nil {
		return RescheduleResult{}, err
	}
	if !ok {
		return RescheduleResult{}, ErrBackwardReschedule
	}
	s.record(ctx, audit.SensitiveAccess{
		Action: "DELIVERY_RUN_RESCHEDULED", EntityType: "delivery_run", EntityID: run.ID,
		Extra: map[string]any{"from": run.ScheduledDate, "to": to, "reason": reason, "stops_moved": moved},
	})
	after, err := s.repo.GetRun(ctx, run.ID)
	if err != nil {
		return RescheduleResult{}, err
	}
	history, _ := s.repo.ListReschedules(ctx, RescheduleEntityRun, run.ID)
	return RescheduleResult{
		EntityType: RescheduleEntityRun, EntityID: run.ID, FromDate: run.ScheduledDate, ToDate: to,
		StopsMoved: moved, RescheduleID: rid, Run: &after, History: history,
	}, nil
}

// validateReschedule applies the shape rules shared by both moves: a well-formed target date, a date
// that is actually different, a date that is not in the past, and a stated reason.
func (s *Service) validateReschedule(in RescheduleInput, fromDate string) (to, reason string, err error) {
	to = strings.TrimSpace(in.ToDate)
	if !validDate(to) {
		return "", "", httpx.BadRequestf("dispatch: to_date must be YYYY-MM-DD")
	}
	if to == fromDate {
		return "", "", httpx.BadRequestf("dispatch: that is already the scheduled date")
	}
	today := s.now().UTC().Format("2006-01-02")
	if to < today {
		return "", "", httpx.BadRequestf("dispatch: %s has already passed, a delivery cannot be promised for a past date", to)
	}
	reason = strings.TrimSpace(in.Reason)
	if reason == "" {
		return "", "", httpx.BadRequestf("dispatch: say why the delivery is moving, the customer will ask")
	}
	if len(reason) > 400 {
		return "", "", httpx.BadRequestf("dispatch: the reason is too long (max 400)")
	}
	return to, reason, nil
}

// RescheduleHistory reads one entity's move trail (fenced through the entity itself).
func (s *Service) RescheduleHistory(ctx context.Context, entityType, entityID string) ([]Reschedule, error) {
	switch entityType {
	case RescheduleEntityStop:
		if _, err := s.guardStop(ctx, entityID); err != nil {
			return nil, err
		}
	case RescheduleEntityRun:
		if _, err := s.guardRun(ctx, entityID); err != nil {
			return nil, err
		}
	default:
		return nil, httpx.BadRequestf("dispatch: unknown entity_type %q", entityType)
	}
	return s.repo.ListReschedules(ctx, entityType, entityID)
}

// - DIB-75 · the scan trail -

// RecordScan writes one checkpoint for one load.
//
// FORWARD ONLY, AND IDEMPOTENT AT THE SAME CHECKPOINT. A load that has reached IN_TRANSIT cannot be
// scanned back to YARD: a pallet does not un-leave the yard, and a trail that admits backward moves
// answers "where has this been" with a shrug. Re-scanning the checkpoint a load is ALREADY at is
// allowed and updates the row (0108's UNIQUE key), the gun beeped twice, the driver pressed it again
// on a dead signal, and neither of those is a second event.
//
// THE BRANCH COMES FROM THE ROW, NEVER THE CALLER. A scan against a stop takes the stop's branch; a
// scan against an order with no stop yet takes the order's. Same rule as InsertStop.
func (s *Service) RecordScan(ctx context.Context, in ScanInput) (ScanCheckpoint, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return ScanCheckpoint{}, err
	}
	checkpoint := strings.ToUpper(strings.TrimSpace(in.Checkpoint))
	if scanOrder[checkpoint] == 0 {
		return ScanCheckpoint{}, httpx.BadRequestf(
			"dispatch: unknown checkpoint %q (RECEIVED, YARD, PICKED, IN_TRANSIT or DELIVERED)", in.Checkpoint)
	}
	loadRef := strings.TrimSpace(in.LoadRef)
	if loadRef == "" {
		return ScanCheckpoint{}, httpx.BadRequestf("dispatch: a scan needs the load reference on the pallet")
	}
	if len(loadRef) > 80 {
		return ScanCheckpoint{}, httpx.BadRequestf("dispatch: the load reference is too long (max 80)")
	}
	if len(strings.TrimSpace(in.Note)) > 400 {
		return ScanCheckpoint{}, httpx.BadRequestf("dispatch: the scan note is too long (max 400)")
	}

	branchID, orderID, stopID := "", strings.TrimSpace(in.OrderID), strings.TrimSpace(in.StopID)
	if stopID != "" {
		stop, err := s.guardStop(ctx, stopID)
		if err != nil {
			return ScanCheckpoint{}, err
		}
		branchID, orderID = stop.BranchID, stop.OrderID
	} else {
		if orderID == "" {
			return ScanCheckpoint{}, httpx.BadRequestf("dispatch: a scan needs an order_id or a stop_id")
		}
		b, err := s.repo.OrderBranchForScan(ctx, orderID)
		if err != nil {
			return ScanCheckpoint{}, ErrNotFound
		}
		if !caller.CanAccessBranch(b) {
			return ScanCheckpoint{}, ErrForbidden
		}
		branchID = b
	}

	// The forward-only guard, read from the trail this load already has.
	trail, err := s.repo.ListScanTrail(ctx, orderID)
	if err != nil {
		return ScanCheckpoint{}, err
	}
	furthest := 0
	for _, c := range trail {
		if strings.EqualFold(c.LoadRef, loadRef) && scanOrder[c.Checkpoint] > furthest {
			furthest = scanOrder[c.Checkpoint]
		}
	}
	if scanOrder[checkpoint] < furthest {
		return ScanCheckpoint{}, ErrBackwardScan
	}

	saved, err := s.repo.UpsertScanCheckpoint(ctx, ScanCheckpoint{
		BranchID: branchID, OrderID: orderID, StopID: stopID, LoadRef: loadRef,
		Checkpoint: checkpoint, ScannedBy: caller.UserID, ScannedAt: s.now().UTC(),
		Note: strings.TrimSpace(in.Note),
	})
	if err != nil {
		return ScanCheckpoint{}, err
	}
	// A DELIVERED scan on a stop that is ALREADY signed for inherits the attestation immediately -
	// the driver who scans the last pallet after signing should not be left with an untied trail.
	if checkpoint == ScanDelivered && stopID != "" {
		if stop, err := s.repo.GetStop(ctx, stopID); err == nil && stop.PODSignedRef != "" {
			if _, err := s.repo.TieScanTrailToPOD(ctx, orderID, stop.PODSignedRef); err == nil {
				saved.PODSignatureRef = stop.PODSignedRef
			}
		}
	}
	s.record(ctx, audit.SensitiveAccess{
		Action: "LOAD_SCANNED_" + checkpoint, EntityType: "load_scan_checkpoint", EntityID: saved.ID,
		Extra: map[string]any{"order_id": orderID, "load_ref": loadRef},
	})
	return saved, nil
}

// ScanTrailForOrder reads one order's trail, fenced on the ORDER's branch (the trail exists before
// any stop does, so the stop cannot be the thing being fenced).
func (s *Service) ScanTrailForOrder(ctx context.Context, orderID string) (ScanTrail, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return ScanTrail{}, err
	}
	branchID, err := s.repo.OrderBranchForScan(ctx, orderID)
	if err != nil {
		return ScanTrail{}, ErrNotFound
	}
	if !caller.CanAccessBranch(branchID) {
		return ScanTrail{}, ErrForbidden
	}
	rows, err := s.repo.ListScanTrail(ctx, orderID)
	if err != nil {
		return ScanTrail{}, err
	}
	return BuildScanTrail(orderID, rows), nil
}

// BuildScanTrail folds checkpoint rows into the per-load progress a surface renders. Exported and
// pure so the "is this order fully delivered" rule is testable with no database, it is the rule the
// driver's completion strip and the yard's trail column BOTH draw from, and two implementations of it
// would eventually disagree on screen.
func BuildScanTrail(orderID string, rows []ScanCheckpoint) ScanTrail {
	trail := ScanTrail{OrderID: orderID, Checkpoints: rows, Loads: []ScanLoad{}}
	if len(rows) == 0 {
		return trail
	}
	idx := map[string]int{}
	for _, c := range rows {
		key := strings.ToUpper(c.LoadRef)
		i, seen := idx[key]
		if !seen {
			trail.Loads = append(trail.Loads, ScanLoad{LoadRef: c.LoadRef})
			i = len(trail.Loads) - 1
			idx[key] = i
		}
		l := &trail.Loads[i]
		l.Reached = append(l.Reached, c.Checkpoint)
		if scanOrder[c.Checkpoint] >= scanOrder[l.Furthest] {
			l.Furthest = c.Checkpoint
		}
		if c.PODSignatureRef != "" {
			l.PODSignatureRef = c.PODSignatureRef
		}
		if c.ScannedAt.After(l.LastScannedAt) {
			l.LastScannedAt = c.ScannedAt
		}
	}
	// Reached is re-ordered into checkpoint sequence, not scan sequence: the strip draws stages, and a
	// clock skew between two scanners must not draw them out of order.
	complete := true
	for i := range trail.Loads {
		l := &trail.Loads[i]
		ordered := make([]string, 0, len(l.Reached))
		for _, stage := range ScanCheckpointSequence {
			for _, got := range l.Reached {
				if got == stage {
					ordered = append(ordered, stage)
					break
				}
			}
		}
		l.Reached = ordered
		if l.Furthest != ScanDelivered {
			complete = false
		}
	}
	trail.Complete = complete
	return trail
}

// - the optional AR / COD dispatch gate -

// WithARGate injects Swarm B's dispatch predicate. Never required: a nil gate allows everything, which
// is exactly what Wave 2 did (model_w3.go states why that default and not the safe-looking opposite).
func (s *Service) WithARGate(g ARDispatchGate) *Service { s.gate = g; return s }

// gateFor asks the gate about one order. A nil gate, a blank order or a gate ERROR all ALLOW.
//
// AN ERROR ALLOWS, DELIBERATELY. The gate is a read against another module; if it is briefly down, the
// choice is between "no deliveries leave the yard today" and "deliveries leave as they did before this
// feature existed". The second is the honest failure mode for an advisory credit check, and the error
// is logged loudly so the outage is visible rather than inferred from a quiet yard.
func (s *Service) gateFor(ctx context.Context, orderID string) DispatchGateDecision {
	if s.gate == nil || orderID == "" {
		return DispatchGateDecision{Allow: true}
	}
	d, err := s.gate.AllowDispatch(ctx, orderID)
	if err != nil {
		s.log.Warn("dispatch: AR gate unavailable, allowing dispatch", "order", orderID, "err", err)
		return DispatchGateDecision{Allow: true}
	}
	if !d.Allow && strings.TrimSpace(d.Reason) == "" {
		// A refusal with no sentence is unusable at a customer's gate. Substituting a generic one is
		// better than a blank banner, and it names the gate so the vagueness is attributable.
		d.Reason = "Payment is due on this order before it can be delivered."
	}
	return d
}

// PaymentHoldForStop is the driver banner's read: what does the gate say about this stop's order.
// Fenced through the stop. Always answers, a nil gate answers "not held".
func (s *Service) PaymentHoldForStop(ctx context.Context, stopID string) (StopPaymentHold, error) {
	stop, err := s.guardStop(ctx, stopID)
	if err != nil {
		return StopPaymentHold{}, err
	}
	d := s.gateFor(ctx, stop.OrderID)
	return StopPaymentHold{
		StopID: stop.ID, OrderID: stop.OrderID, Held: !d.Allow,
		Reason: d.Reason, Code: d.Code, AmountDueCents: d.AmountDueCents,
	}, nil
}

// PaymentHoldsForRun is the same read for a whole run, the driver's day loads one banner set, not
// one request per stop.
func (s *Service) PaymentHoldsForRun(ctx context.Context, runID string) ([]StopPaymentHold, error) {
	run, err := s.guardRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	stops, err := s.repo.ListStopsForRun(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	out := make([]StopPaymentHold, 0, len(stops))
	for _, st := range stops {
		d := s.gateFor(ctx, st.OrderID)
		out = append(out, StopPaymentHold{
			StopID: st.ID, OrderID: st.OrderID, Held: !d.Allow,
			Reason: d.Reason, Code: d.Code, AmountDueCents: d.AmountDueCents,
		})
	}
	return out, nil
}

// assertGateAllowsRun is the DISPATCH-SIDE CALL SITE: no truck leaves carrying an order the gate has
// refused.
//
// IT REFUSES THE WHOLE RUN, NAMING THE ORDERS. The alternative, quietly leaving the held stops behind
// and dispatching the rest, produces a truck whose manifest does not match its load, which is worse
// than a refusal an operator can act on. The two ways out are both one click away: take the stop off
// the run, or clear the payment.
func (s *Service) assertGateAllowsRun(ctx context.Context, stops []Stop) error {
	if s.gate == nil {
		return nil
	}
	held := []string{}
	reason := ""
	for _, st := range stops {
		if st.Status == StopCancelled || st.Status == StopFailed {
			continue
		}
		d := s.gateFor(ctx, st.OrderID)
		if d.Allow {
			continue
		}
		held = append(held, orDash(st.OrderNo))
		if reason == "" {
			reason = d.Reason
		}
	}
	if len(held) == 0 {
		return nil
	}
	return conflictf("dispatch: %s held for payment, %s. Take the stop off the run or clear the payment, then dispatch.",
		strings.Join(held, ", "), strings.TrimSuffix(reason, "."))
}

// - manifest MTO assembly -

// manifestMTO decorates a run's stops with their compliance trio and rolls the run's summary up.
// One query for the classifications (ManifestStopMTO), never one per stop, a print button that
// issues N+1 reads is a print button that times out on the day the run is full.
func (s *Service) manifestMTO(ctx context.Context, run Run, stops []Stop) ([]ManifestStop, MTOSummary, error) {
	byStop, err := s.repo.ManifestStopMTO(ctx, run.ID)
	if err != nil {
		return nil, MTOSummary{}, err
	}
	out := make([]ManifestStop, 0, len(stops))
	for _, st := range stops {
		m, ok := byStop[st.ID]
		if !ok {
			m = StopMTO{Class: MTOStandard}
		}
		out = append(out, ManifestStop{Stop: st, MTO: m})
	}
	var capacity *float64
	if run.TruckID != "" {
		if truck, err := s.repo.GetTruck(ctx, run.TruckID); err == nil {
			capacity = truck.CapacityKg
		} else {
			s.log.Warn("dispatch: manifest could not read the truck capacity", "run", run.ID, "err", err)
		}
	}
	return out, rollUpMTO(out, capacity), nil
}

// mtoClassLabel renders a class for the printed sheet. Kept next to the roll-up so the words on the
// paper and the rule that chose them live together.
func mtoClassLabel(class string) string {
	switch class {
	case MTOMade:
		return "MADE TO ORDER"
	case MTOMixed:
		// No "+" in the label. html/template escapes it to &#43;, which prints as literal noise on the
		// sheet and reads as a bug to whoever is holding it. The word is clearer anyway.
		return "MIXED, MTO AND STOCK"
	default:
		return "STOCK"
	}
}

// weightLabel formats a weight for print. An absent weight is a dash, never a zero: printing 0 kg
// next to a truck's capacity is a lie with a unit on it.
func weightLabel(w *float64) string {
	if w == nil {
		return "-"
	}
	return fmt.Sprintf("%.0f kg", *w)
}
