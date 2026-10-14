// Package dispatch owns the PHYSICAL leg of the order lifecycle: the delivery a confirmed order
// turns into, the run that carries it, the driver who takes it, the proof it arrived, and the handoff
// back to billing (DIB-64), plus the will-call pickup a customer confirms for themselves (DIB-79).
//
// WHERE IT SITS. Everything upstream of READY already existed: quote → order → picking → READY. What
// came after was a clipboard. This module is the segment between "the material is on the rack" and
// "the customer has it and we can invoice", and every table it owns exists to turn one remembered
// fact into a row: which truck, which driver, what time it left, who signed, what it looked like when
// it was dropped.
//
// WHAT IT DOES NOT OWN. Not stock (inventory decrements at the POS ring / fulfilment, not here). Not
// money (the handoff advances the ORDER through the existing order seam; billing does the rest). Not
// routing (no optimizer, no map service, no PostGIS, plain lat/lng and a haversine estimate, ruled
// 2026-08-04). Not a fleet system (trucks and drivers are a two-column registry, deliberately).
//
// THE FENCE. Every read and every write is gated on identity.StaffContext.CanAccessBranch, checked
// against a branch_id that lives ON the row rather than at the end of a JOIN. A scoped caller with no
// branch named cannot enumerate all branches (403, the landedcost List posture); a document in a
// branch they cannot reach is NOT FOUND rather than forbidden when they only ever held a NUMBER (a
// number lookup that answered 403 would confirm the number exists in a branch they cannot see).
package dispatch

import (
	"errors"
	"time"
)

// - stop lifecycle -
//
// READY → LOADED → EN_ROUTE → DELIVERED is the ruled flow, and it is deliberately FORWARD ONLY. The
// two exits are the honest ones: FAILED (reachable only from EN_ROUTE, a stop that never left cannot
// have failed to arrive) and CANCELLED (reachable only before departure, a truck that has gone is a
// fact, not a plan).
const (
	StopReady     = "READY"
	StopLoaded    = "LOADED"
	StopEnRoute   = "EN_ROUTE"
	StopDelivered = "DELIVERED"
	StopFailed    = "FAILED"
	StopCancelled = "CANCELLED"
)

// - run lifecycle -
const (
	RunPlanned    = "PLANNED"
	RunDispatched = "DISPATCHED"
	RunCompleted  = "COMPLETED"
	RunCancelled  = "CANCELLED"
)

// truckTypes mirrors the 0090 CHECK in Go so an unknown type is a 400 naming the value rather than a
// 500 carrying a constraint name, the printing.docTypes posture.
var truckTypes = map[string]bool{
	"FLATBED": true, "BOOM": true, "DUMP": true, "PICKUP": true, "TRAILER": true,
}

// podContentTypes mirrors the 0091 CHECK. A POD photo is a photo; nothing else is accepted, and the
// refusal happens before any bytes are written.
var podContentTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/webp": true,
}

// MaxPhotoBytes bounds one POD photo (0091's CHECK is the backstop). A phone photo of a pallet on a
// driveway is 1-3 MB; a table holding one row per stop forever must not be able to hold a video.
const MaxPhotoBytes = 4 << 20

// SignaturePurposePOD is the pos_signature.purpose a delivery/pickup attestation is captured under.
// It is one of the four values 0029's CHECK already allows, proof of delivery was designed for from
// the start; nothing had ever written one.
const SignaturePurposePOD = "PROOF_OF_DELIVERY"

// Signature ref_type discriminators, what the sealed attestation is ABOUT.
const (
	RefTypeDeliveryStop = "delivery_stop"
	RefTypeWillCall     = "will_call_pickup"
)

// - rows -

// Truck is one dispatch_trucks row.
type Truck struct {
	ID         string   `json:"id"`
	BranchID   string   `json:"branch_id"`
	Code       string   `json:"code"`
	Name       string   `json:"name"`
	TruckType  string   `json:"truck_type"`
	CapacityKg *float64 `json:"capacity_kg,omitempty"`
	// The other two ways a truck runs out (0184 §2). Both advisory and both NULLable on purpose: a
	// truck nobody has measured draws NO gauge rather than a green one.
	CapacityM3      *float64 `json:"capacity_m3,omitempty"`
	PalletPositions *int     `json:"pallet_positions,omitempty"`
	IsActive        bool     `json:"is_active"`
}

// Driver is one dispatch_drivers row. UserID is empty for a driver with no staff login (casual and
// contract drivers are normal here, not an anomaly).
type Driver struct {
	ID           string `json:"id"`
	BranchID     string `json:"branch_id"`
	UserID       string `json:"user_id,omitempty"`
	Name         string `json:"name"`
	Phone        string `json:"phone,omitempty"`
	LicenceClass string `json:"licence_class,omitempty"`
	IsActive     bool   `json:"is_active"`
}

// Run is one delivery_runs row plus the two display names the board reads (resolved by the
// repository's join, the board renders "TRK-1 · Wade Prentice", not two UUIDs).
type Run struct {
	ID            string     `json:"id"`
	RunNo         string     `json:"run_no"`
	BranchID      string     `json:"branch_id"`
	ScheduledDate string     `json:"scheduled_date"` // YYYY-MM-DD
	TruckID       string     `json:"truck_id,omitempty"`
	DriverID      string     `json:"driver_id,omitempty"`
	TruckCode     string     `json:"truck_code,omitempty"`
	DriverName    string     `json:"driver_name,omitempty"`
	Status        string     `json:"status"`
	Notes         string     `json:"notes,omitempty"`
	DispatchedAt  *time.Time `json:"dispatched_at,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	StopCount     int        `json:"stop_count"`
	CreatedAt     time.Time  `json:"created_at"`

	// - the commitment (0185) -
	// CommitState is the run's BULK LEDGER, AUTO, or REOPENED when a dispatch manager reopened this
	// whole truck. It is NOT the authority on whether a load is frozen: the loads are. Commit is the
	// COMPUTED answer the board renders, folded by the service from the loads on this run.
	CommitState  string      `json:"commit_state,omitempty"`
	ReopenedAt   string      `json:"reopened_at,omitempty"`
	ReopenReason string      `json:"reopen_reason,omitempty"`
	Commit       *LoadCommit `json:"commit,omitempty"`

	// Gauge is this run's load against this truck's capacity, the two bars and (when the policy
	// allows the caller) the dollar value on the load card. Absent on reads that do not build it.
	Gauge *LoadGauge `json:"gauge,omitempty"`
}

// Stop is one delivery_stops row plus the order/customer identifiers the board and the manifest show.
type Stop struct {
	ID            string     `json:"id"`
	BranchID      string     `json:"branch_id"`
	OrderID       string     `json:"order_id"`
	OrderNo       string     `json:"order_no,omitempty"`
	CustomerName  string     `json:"customer_name,omitempty"`
	RunID         string     `json:"run_id,omitempty"`
	RunNo         string     `json:"run_no,omitempty"`
	StopNo        int        `json:"stop_no"`
	ScheduledDate string     `json:"scheduled_date"`
	Status        string     `json:"status"`
	TimeWindow    string     `json:"time_window,omitempty"`
	ContactName   string     `json:"contact_name,omitempty"`
	ContactPhone  string     `json:"contact_phone,omitempty"`
	AddressLine   string     `json:"address_line,omitempty"`
	City          string     `json:"city,omitempty"`
	PostalCode    string     `json:"postal_code,omitempty"`
	Latitude      *float64   `json:"latitude,omitempty"`
	Longitude     *float64   `json:"longitude,omitempty"`
	Instructions  string     `json:"instructions,omitempty"`
	PODSignedRef  string     `json:"pod_signature_ref,omitempty"`
	PODReceivedBy string     `json:"pod_received_by,omitempty"`
	PODAt         *time.Time `json:"pod_at,omitempty"`
	FailureReason string     `json:"failure_reason,omitempty"`
	LoadedAt      *time.Time `json:"loaded_at,omitempty"`
	EnRouteAt     *time.Time `json:"en_route_at,omitempty"`
	DeliveredAt   *time.Time `json:"delivered_at,omitempty"`
	HandoffAt     *time.Time `json:"handoff_at,omitempty"`
	PhotoCount    int        `json:"photo_count"`
	CreatedAt     time.Time  `json:"created_at"`

	// - the load-building facts (0184) -
	//
	// EVERY MEASUREMENT IS A POINTER. nil means NOBODY MEASURED IT, and that is a different fact from
	// zero: a stop with no weight contributes nothing to a run total and is excluded from the
	// "weighed" count, so a gauge can never read complete off a set of blanks (0107's manifest rule).
	WeightKg        *float64 `json:"weight_kg,omitempty"`
	VolumeM3        *float64 `json:"volume_m3,omitempty"`
	PalletPositions *int     `json:"pallet_positions,omitempty"`

	// - the two MONEY facts, and why they are `json:"-"` -
	//
	// ValueCents (what is on this truck) and FreightCents (this load's share of the order's freight)
	// are money, and money is gated. They are therefore NEVER on the general Stop payload at all -
	// not omitempty, not zeroed, ABSENT BY TYPE. The quote pilot's lesson, applied: the gated figures
	// ride on their own payload (QuoteMargin there, LoadStop here) produced by ONE method that has
	// already asked the policy. A field that is on the general payload is a field somebody has to
	// remember to blank in eleven read paths, and the eleventh is the leak.
	ValueCents        *int64 `json:"-"`
	FreightCents      *int64 `json:"-"`
	FreightIsOverride bool   `json:"freight_is_override,omitempty"`

	// The split: which load of how many, on ONE invoice.
	LoadSeq   int `json:"load_seq,omitempty"`
	LoadCount int `json:"load_count,omitempty"`

	// THE STATE MACHINE (0184 §5, unified by 0185). RequestedDate survives the commit deliberately:
	// "we said the 14th and it went out the 16th" is the question this pair exists to answer.
	RequestedDate string     `json:"requested_date,omitempty"`
	CommitState   string     `json:"commit_state,omitempty"`
	CommittedAt   *time.Time `json:"committed_at,omitempty"`
	// The reopen, which is an ordinary act and so is an ordinary field. The stamps are NOT cleared by
	// a later commit, "reopened on the 3rd, committed again on the 4th" is a real history.
	ReopenedAt   *time.Time `json:"reopened_at,omitempty"`
	ReopenReason string     `json:"reopen_reason,omitempty"`
}

// Board is the day view: one date, one branch, the runs on it and every stop (assigned or not).
// Unassigned is the board's first column and the normal birthplace of a stop.
type Board struct {
	BranchID   string `json:"branch_id"`
	Date       string `json:"date"`
	Runs       []Run  `json:"runs"`
	Stops      []Stop `json:"stops"`
	Unassigned []Stop `json:"unassigned"`
}

// PhotoMeta is a POD photo WITHOUT its bytes, the list read. The bytes come back only from the
// per-photo endpoint, so a stop detail never drags megabytes through the board.
type PhotoMeta struct {
	ID          string    `json:"id"`
	StopID      string    `json:"stop_id"`
	ContentType string    `json:"content_type"`
	ByteSize    int       `json:"byte_size"`
	Caption     string    `json:"caption,omitempty"`
	CapturedAt  time.Time `json:"captured_at"`
}

// Photo is PhotoMeta plus the bytes (the single-photo read).
type Photo struct {
	PhotoMeta
	Bytes []byte `json:"-"`
}

// StopDetail is one stop with its POD photo metadata, what the driver's stop screen and the
// dispatcher's drawer both render.
type StopDetail struct {
	Stop
	Photos []PhotoMeta `json:"photos"`
	// W3-C / DIB-63: the made-to-order classification. It is on the DETAIL and not on Stop because
	// Stop is the board/driver projection every Wave 2 consumer already pins, and the classification
	// is a drawer-level fact, the board shows where a delivery is, the drawer shows what is on it.
	MTO StopMTO `json:"mto"`
}

// RunDetail is one run with its stops in stop order, plus the haversine distance estimate when the
// branch has a depot coordinate AND at least one stop is geocoded (absent otherwise, an estimate
// built from a guessed origin is worse than no estimate).
type RunDetail struct {
	Run
	Stops           []Stop   `json:"stops"`
	EstimatedKm     *float64 `json:"estimated_km,omitempty"`
	GeocodedStops   int      `json:"geocoded_stops"`
	UngeocodedStops int      `json:"ungeocoded_stops"`

	// StopLines is WHAT COMES OFF THE TRUCK at each stop, keyed by stop id. See DriverLine below for
	// why it is a separate map rather than a field on Stop.
	//
	// IT IS POPULATED ON EXACTLY ONE READ, Service.MyRuns, the driver's own day. Neither
	// Service.RunDetail (the dispatcher's drawer), nor RunByNo, nor the manifest builder sets it, so
	// every other consumer of this struct marshals byte-for-byte what it marshalled before this field
	// existed. That is deliberate: widening a payload four surfaces already pin is how a fifth surface
	// silently starts carrying something, and the audience that asked for lines is the driver.
	StopLines map[string][]DriverLine `json:"stop_lines,omitempty"`
}

// DriverLine is one line of material coming off the truck at a stop, as the DRIVER meets it.
//
// LOOK AT WHAT IS NOT IN THIS STRUCT. There is no unit price, no extended price, no cost, no margin,
// and no cents field of any kind, not a gated pointer, not an omitempty one, not a `json:"-"` one.
// That is the same ruling Stop's two money facts record (see Stop.ValueCents above) taken one step
// further, and the step matters: Stop CARRIES the cents in memory and suppresses them at the tag, so
// its safety is one struct tag deep. A gated pointer needs a call site that remembers to ask the
// policy, and the driver's read has no policy call in it at all, there is nothing here to ask.
// Declaring the type with no money member makes "a driver's lines carry no money" a fact about the
// TYPE that a compiler enforces, rather than a fact about a read path somebody has to maintain.
//
// It is a sibling of PickFeedLine (picksource.go) rather than a reuse of it, for two reasons. The
// pick feed's line carries Location, the bin the yard pulls from, which is a fact about the yard's
// morning and means nothing to a driver holding a signed ticket in a driveway, and PickFeedLine has
// no JSON tags at all because nothing has ever marshalled it. Bolting wire tags onto an internal feed
// struct would have made every future field on it a wire-compatibility question.
//
// Quantity is a STRING for the reason the pick feed states: it is decimal-as-string in the line's own
// UOM, so the number the driver reads is the number the counter said, never a float that has been
// through a base-unit conversion and back.
type DriverLine struct {
	LineNo      int    `json:"line_no"`
	SKU         string `json:"sku"`
	Description string `json:"description"`
	Qty         string `json:"qty"`
	UOM         string `json:"uom"`
}

// WillCall is one will_call_pickups row as STAFF see it (never the raw token, that is returned once,
// at mint, and never stored).
type WillCall struct {
	ID           string     `json:"id"`
	OrderID      string     `json:"order_id"`
	BranchID     string     `json:"branch_id"`
	Status       string     `json:"status"`
	ExpiresAt    time.Time  `json:"expires_at"`
	SignatureRef string     `json:"signature_ref,omitempty"`
	CollectedBy  string     `json:"collected_by,omitempty"`
	ConfirmedAt  *time.Time `json:"confirmed_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// PublicWillCall is what the TOKEN HOLDER sees. It carries the minimum a person needs to know they
// are confirming the right pickup, the order number, the branch, the customer the order belongs to,
// and how many lines, and NOTHING a staff member would see: no cost, no margin, no prices, no other
// order. Whoever holds the QR is not authenticated; the payload is written on that assumption.
type PublicWillCall struct {
	OrderNo      string `json:"order_no"`
	BranchName   string `json:"branch_name"`
	CustomerName string `json:"customer_name"`
	LineCount    int    `json:"line_count"`
	Status       string `json:"status"`
	CollectedBy  string `json:"collected_by,omitempty"`
}

// - inputs -

// ScheduleInput schedules ONE order for delivery on ONE date. Branch is NOT taken from the caller:
// it is copied from the order through the OrderReader seam, so a stop can never be filed against a
// branch the order does not belong to.
type ScheduleInput struct {
	OrderID       string   `json:"order_id"`
	ScheduledDate string   `json:"scheduled_date"` // YYYY-MM-DD
	TimeWindow    string   `json:"time_window"`
	ContactName   string   `json:"contact_name"`
	ContactPhone  string   `json:"contact_phone"`
	AddressLine   string   `json:"address_line"`
	City          string   `json:"city"`
	PostalCode    string   `json:"postal_code"`
	Latitude      *float64 `json:"latitude"`
	Longitude     *float64 `json:"longitude"`
	Instructions  string   `json:"instructions"`

	// - 0184/0185: how the delivery is being asked for, and what it puts on a truck -
	//
	// CommitState is REQUESTED when a SALESPERSON asks for a date and COMMITTED when a DISPATCHER
	// stands behind one. It normalises to REQUESTED when omitted, the safe direction, because a
	// caller who said nothing has not made a promise. Asking for COMMITTED here is refused for a
	// caller without the commit capability (service.Schedule), so the field is an intent, never an
	// assertion. RequestedDate carries the original ask when the two differ.
	CommitState   string `json:"commit_state"`
	RequestedDate string `json:"requested_date"`
	// DateState is 0184's name for CommitState, accepted on the wire for one release so a client that
	// has not been rebuilt still schedules. CommitState wins when both are present.
	DateState string `json:"date_state,omitempty"`

	// The measurements. Optional and nil-meaning-unmeasured, all the way down.
	WeightKg        *float64 `json:"weight_kg"`
	VolumeM3        *float64 `json:"volume_m3"`
	PalletPositions *int     `json:"pallet_positions"`
	// NOTE THE ABSENCE OF A VALUE FIELD. A load's dollar value is STAMPED FROM THE ORDER through the
	// value seam, never accepted from the request: a caller who could assert what a load is worth
	// could make a $30,000 porcelain load read as a $300 one on the screen that decides whether it
	// travels alone.
}

// CreateRunInput opens a run for a branch/date. Truck and driver are optional at creation (a run is
// often drafted before the morning crew is settled) and REQUIRED at dispatch.
type CreateRunInput struct {
	BranchID      string `json:"branch_id"`
	ScheduledDate string `json:"scheduled_date"`
	TruckID       string `json:"truck_id"`
	DriverID      string `json:"driver_id"`
	Notes         string `json:"notes"`
}

// AssignInput puts a stop on a run at a position. StopNo 0 means "append".
type AssignInput struct {
	RunID  string `json:"run_id"`
	StopNo int    `json:"stop_no"`
}

// PODInput is the delivery attestation: the typed name of the person receiving, and their explicit
// affirmative. Both are REQUIRED and neither is inferred, the DIB-386 refusals apply verbatim.
type PODInput struct {
	ReceivedBy string `json:"received_by"`
	Affirmed   bool   `json:"affirmed"`
	// IdempotencyKey is the sync door's client key. It never travels on the REST body (json "-"): the
	// stop row carries it from the same statement that delivers it, so a replayed attestation is
	// found, not re-run.
	IdempotencyKey string `json:"-"`
}

// TypedAttestation mirrors pos.TypedAttestation BY VALUE so this package never imports pos. The
// composition root adapts one to the other (the landedcost.GLJournal precedent).
type TypedAttestation struct {
	Purpose   string
	RefType   string
	RefID     string
	TypedName string
	Affirmed  bool
}

// PrintJobRequest mirrors printing.EnqueueInput by value, same reason.
type PrintJobRequest struct {
	DocType      string
	RefID        string
	BranchID     string
	RenderTarget string
}

// OrderBrief mirrors order.Brief by value, the four fields this module needs to validate an order
// and copy its branch onto a stop. It never reads the orders table itself.
type OrderBrief struct {
	ID          string
	BranchID    string
	Fulfillment string
	Status      string
}

// - errors -

var (
	// ErrNotFound, no such run/stop/truck/driver, or one the caller may not know exists.
	ErrNotFound = errors.New("dispatch: not found")
	// ErrForbidden, the caller's branch scope does not reach this row's branch.
	ErrForbidden = errors.New("dispatch: forbidden")
	// ErrUnauthorized, no resolved staff caller on the context.
	ErrUnauthorized = errors.New("dispatch: unauthenticated")
	// ErrIllegalTransition, the requested move is not on the lifecycle matrix. 409, never 400: the
	// request was well-formed, the WORLD was in the wrong state.
	ErrIllegalTransition = errors.New("dispatch: illegal status transition")
	// ErrPODRequired, DELIVERED was asked for on a stop with no captured attestation.
	ErrPODRequired = errors.New("dispatch: a delivery must carry a proof-of-delivery attestation")
	// ErrCrewRequired, DISPATCHED was asked for on a run with no truck or no driver.
	ErrCrewRequired = errors.New("dispatch: a run needs both a truck and a driver before it can be dispatched")
	// ErrRunNotDispatched, EN_ROUTE was asked for on a stop whose run has not left.
	ErrRunNotDispatched = errors.New("dispatch: the run has not been dispatched")
	// ErrStopUnassigned, LOADED was asked for on a stop that is not on any run.
	ErrStopUnassigned = errors.New("dispatch: the stop is not assigned to a run")
	// ErrRunHasOpenStops, COMPLETED was asked for on a run with stops still in flight.
	ErrRunHasOpenStops = errors.New("dispatch: the run still has stops that are not finished")
	// ErrTokenGone, a will-call token that exists but is revoked, expired or already used. 410 GONE:
	// the holder should learn the TOKEN died, not be told the order never existed.
	ErrTokenGone = errors.New("dispatch: this pickup code is no longer valid")
	// ErrTokenNotFound, no such token, ever. 404.
	ErrTokenNotFound = errors.New("dispatch: pickup code not found")
	// ErrNoSignatureProvider, the attestation seam is not wired. 501, honestly: the alternative is
	// writing a delivery with no evidence, which is the whole failure this module exists to end.
	ErrNoSignatureProvider = errors.New("dispatch: no signature provider is wired")
)
