package dispatch

// model_w3.go, the Wave 3 rows, inputs and seams: MTO classification (DIB-63), the reschedule trail
// (DIB-382), the pallet/load scan trail (DIB-75) and the AR dispatch gate (Swarm B's seam).
//
// Everything here is ADDITIVE. No Wave 2 type is edited in place: Stop, Run, Manifest and the four
// module.go seams keep the shapes their own tests and the frontend already pin. The manifest gains
// its MTO section by carrying a NEW sibling struct rather than by growing new fields on Stop, so a
// consumer that only knows the Wave 2 Stop still compiles and still renders.

import (
	"context"
	"errors"
	"time"
)

// - DIB-63 · made-to-order classification -

// MTO classes (0107 CHECK), mirrored in Go so an unknown value is a 400 naming it rather than a 500
// carrying a constraint name, the truckTypes posture.
const (
	MTOStandard = "STANDARD" // stock material: interchangeable, returnable
	MTOMade     = "MTO"      // made/cut to order for this customer: not substitutable, not re-stockable
	MTOMixed    = "MIXED"    // both on one load, the handling rule differs per pallet
)

var mtoClasses = map[string]bool{MTOStandard: true, MTOMade: true, MTOMixed: true}

// StopMTO is one stop's compliance trio. WeightKg is a POINTER because NULL and zero are different
// facts: nobody weighed it, versus it weighs nothing. The manifest prints "-" for the first and
// refuses to add it into a total.
type StopMTO struct {
	Class    string   `json:"mto_class"`
	Note     string   `json:"mto_note,omitempty"`
	WeightKg *float64 `json:"weight_kg,omitempty"`
}

// MTOInput is the classification write.
type MTOInput struct {
	Class    string   `json:"mto_class"`
	Note     string   `json:"mto_note"`
	WeightKg *float64 `json:"weight_kg"`
}

// ManifestStop is one manifest row: the Wave 2 Stop plus its compliance trio. Embedding rather than
// widening Stop keeps the board, the driver surface and every existing test on the frozen shape.
type ManifestStop struct {
	Stop
	MTO StopMTO `json:"mto"`
}

// MTOSummary is the run-level roll-up the manifest header carries. It is COMPUTED from the stops on
// every render and stored nowhere: two copies of a classification is one copy that goes stale
// (0107's header states the rule).
type MTOSummary struct {
	// Class is the run's class: STANDARD when every stop is, MTO when every stop is, MIXED otherwise
	// (including a run that carries one MTO stop among standard ones, which is the case a driver most
	// needs told, and the case a "majority" rule would hide).
	Class string `json:"mto_class"`
	// MTOStops is how many stops on the run carry made-to-order material.
	MTOStops int `json:"mto_stops"`
	// WeighedStops / TotalWeightKg describe the weight total HONESTLY: the total is the sum of the
	// stops that were actually weighed, and the count says how many that was. A total presented
	// without that count would read as the weight of the load.
	WeighedStops  int     `json:"weighed_stops"`
	TotalWeightKg float64 `json:"total_weight_kg"`
	// UnweighedStops is the rest. Printed next to the total so nobody compares a partial figure to a
	// truck's capacity and concludes there is room.
	UnweighedStops int `json:"unweighed_stops"`
	// CapacityKg is the assigned truck's advisory capacity (0090: advisory, never a refusal), present
	// only when a truck is assigned AND it has one recorded.
	CapacityKg *float64 `json:"capacity_kg,omitempty"`
	// OverCapacity is true only when a COMPLETE weight total exceeds a known capacity. A partial total
	// can never raise it: a warning built on half the load is a warning people learn to ignore.
	OverCapacity bool `json:"over_capacity"`
}

// - DIB-382 · the reschedule trail -

// Reschedule is one dispatch_reschedules row (0107).
type Reschedule struct {
	ID         string    `json:"id"`
	BranchID   string    `json:"branch_id"`
	EntityType string    `json:"entity_type"` // delivery_stop | delivery_run
	EntityID   string    `json:"entity_id"`
	FromDate   string    `json:"from_date"` // YYYY-MM-DD
	ToDate     string    `json:"to_date"`
	Reason     string    `json:"reason"`
	StopsMoved int       `json:"stops_moved"`
	ActorID    string    `json:"actor_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// Reschedule entity discriminators (0107 CHECK).
const (
	RescheduleEntityStop = "delivery_stop"
	RescheduleEntityRun  = "delivery_run"
)

// RescheduleInput moves a stop or a run to another date. Reason is REQUIRED, and the service says why.
type RescheduleInput struct {
	ToDate string `json:"to_date"`
	Reason string `json:"reason"`
}

// RescheduleResult is what a successful move reports back: where it went, what came with it, and the
// trail row that recorded it.
type RescheduleResult struct {
	EntityType   string       `json:"entity_type"`
	EntityID     string       `json:"entity_id"`
	FromDate     string       `json:"from_date"`
	ToDate       string       `json:"to_date"`
	StopsMoved   int          `json:"stops_moved"`
	RescheduleID string       `json:"reschedule_id"`
	Stop         *Stop        `json:"stop,omitempty"`
	Run          *Run         `json:"run,omitempty"`
	History      []Reschedule `json:"history,omitempty"`
}

// - DIB-75 · the pallet/load scan trail -

// The five physical checkpoints (0108 CHECK). They are PLACES a load has been, not document states.
const (
	ScanReceived  = "RECEIVED"   // off a supplier truck, into our yard
	ScanYard      = "YARD"       // racked/binned, addressable
	ScanPicked    = "PICKED"     // pulled for an order
	ScanInTransit = "IN_TRANSIT" // on our truck
	ScanDelivered = "DELIVERED"  // handed over
)

// scanOrder is the forward-only ordinal. It is the single source of truth for legality (used by the
// service AND by ScanTrailComplete), so "which way is forward" is stated once.
var scanOrder = map[string]int{
	ScanReceived: 1, ScanYard: 2, ScanPicked: 3, ScanInTransit: 4, ScanDelivered: 5,
}

// ScanCheckpointSequence is the trail in order, what the driver and yard surfaces render as the
// spine of the progress strip, including the checkpoints that have NOT happened yet.
var ScanCheckpointSequence = []string{ScanReceived, ScanYard, ScanPicked, ScanInTransit, ScanDelivered}

// ScanCheckpoint is one load_scan_checkpoints row (0108).
type ScanCheckpoint struct {
	ID              string    `json:"id"`
	BranchID        string    `json:"branch_id"`
	OrderID         string    `json:"order_id"`
	StopID          string    `json:"stop_id,omitempty"`
	LoadRef         string    `json:"load_ref"`
	Checkpoint      string    `json:"checkpoint"`
	ScannedBy       string    `json:"scanned_by,omitempty"`
	ScannedAt       time.Time `json:"scanned_at"`
	Note            string    `json:"note,omitempty"`
	PODSignatureRef string    `json:"pod_signature_ref,omitempty"`
}

// ScanInput is one scan. StopID is optional: RECEIVED and YARD happen before any delivery is
// scheduled, and refusing them until a stop exists would drop the two checkpoints nobody has today.
type ScanInput struct {
	OrderID    string `json:"order_id"`
	StopID     string `json:"stop_id"`
	LoadRef    string `json:"load_ref"`
	Checkpoint string `json:"checkpoint"`
	Note       string `json:"note"`
}

// ScanTrail is the trail as a surface renders it: every checkpoint that happened, grouped by load,
// plus the per-load progress the strip draws.
type ScanTrail struct {
	OrderID     string           `json:"order_id"`
	Checkpoints []ScanCheckpoint `json:"checkpoints"`
	Loads       []ScanLoad       `json:"loads"`
	// Complete is true only when EVERY load on the order has reached DELIVERED. An order with three
	// pallets and two delivered is not a delivered order, and this is the field that says so.
	Complete bool `json:"complete"`
}

// ScanLoad is one physical load's progress through the five checkpoints.
type ScanLoad struct {
	LoadRef string `json:"load_ref"`
	// Reached lists the checkpoints this load has hit, in sequence order.
	Reached []string `json:"reached"`
	// Furthest is the last checkpoint reached ("" for a load with no scans, which cannot occur).
	Furthest string `json:"furthest"`
	// PODSignatureRef is set once the DELIVERED checkpoint has been tied to the attestation, the
	// "POD ties the final checkpoint" half of DIB-75.
	PODSignatureRef string    `json:"pod_signature_ref,omitempty"`
	LastScannedAt   time.Time `json:"last_scanned_at"`
}

// - the AR / COD dispatch gate (Swarm B's seam, consumed OPTIONALLY) -

// DispatchGateDecision is one order's answer to "may this go on a truck today". Reason is written for
// a DRIVER to read on a phone at a customer's gate, not for a log line: it becomes the PAYMENT DUE
// banner verbatim. This is the GATE's own answer, in process only: the door money fence
// (cost_gate.go) decides which caller may receive the figure and this sentence, whose words may name
// the figure.
type DispatchGateDecision struct {
	Allow  bool   `json:"allow"`
	Reason string `json:"reason,omitempty"`
	// Code is the machine handle for the refusal (e.g. "COD_UNPAID", "CREDIT_HOLD"). Surfaces let the
	// client style the banner without parsing prose.
	Code string `json:"code,omitempty"`
	// AmountDueCents is what the order still owes before release. Zero when the gate does not say. It
	// reaches a caller only through the payment hold reads, and only for a role on the cost and
	// margin list; every other caller is told the hold in words, never a figure.
	AmountDueCents int64 `json:"amount_due_cents,omitempty"`
}

// ARDispatchGate is the CONSUMER-DECLARED predicate over Swarm B's accounts-receivable rules: may
// this order's material leave the yard, and if not, what does the driver need to be told.
//
// NIL IS ALLOW, DELIBERATELY. This module shipped in Wave 2 with no gate at all, and a seam that
// turned every unwired deployment into a yard that cannot dispatch would be a feature that takes the
// business down when its provider is absent. An unwired gate therefore permits exactly what Wave 2
// permitted, nothing regresses, and turning the gate on is one adapter in the composition root.
//
// SIGNATURE RECONCILIATION. Swarm B exports the predicate; if their concrete signature differs from
// this interface at the union, the ADAPTER in main.go changes and nothing in this package does. That
// is the whole point of declaring it here rather than importing theirs (REPORT-W3-C.md, union notes,
// states the shape this side expects).
type ARDispatchGate interface {
	AllowDispatch(ctx context.Context, orderID string) (DispatchGateDecision, error)
}

// StopPaymentHold is the driver-facing banner payload: what is owed on this stop and why it is held.
// Computed per read from the gate, never stored, a payment state cached on a delivery row is a
// payment state that is wrong the moment the customer pays. The hold itself reaches every caller,
// its mark, its code and its words; the figure and the gate's own sentence (whose words may name the
// figure) reach only a role on the cost and margin list (cost_gate.go), and every other caller is
// told the hold in words, never a figure.
type StopPaymentHold struct {
	StopID  string `json:"stop_id"`
	OrderID string `json:"order_id"`
	Held    bool   `json:"held"`
	Reason  string `json:"reason,omitempty"`
	Code    string `json:"code,omitempty"`
	// AmountDueCents rides only to a role on the cost and margin list; absent, not zero, for every
	// other role.
	AmountDueCents int64 `json:"amount_due_cents,omitempty"`
}

// - Wave 3 errors -

var (
	// ErrBackwardReschedule, the move was refused because the stop/run has already moved past the
	// point where a date is a plan. 409, never 400: the request was fine, the WORLD had moved on.
	ErrBackwardReschedule = errors.New("dispatch: this delivery has already left the planning stage and its date can no longer be changed")
	// ErrBackwardScan, a checkpoint behind one this load has already reached. The trail is
	// forward-only for the same reason the stop lifecycle is: a pallet does not un-leave the yard.
	ErrBackwardScan = errors.New("dispatch: this load has already passed that checkpoint")
	// ErrPaymentHold, the AR gate refused this order's dispatch. Carries the gate's own sentence.
	ErrPaymentHold = errors.New("dispatch: this order is held for payment")
)

// validMTOClass reports whether a class is one of 0107's three.
func validMTOClass(c string) bool { return mtoClasses[c] }
