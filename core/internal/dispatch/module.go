package dispatch

// module.go, the CONSUMER-DECLARED seams and the route registration.
//
// This package imports NEITHER order, NOR pos, NOR printing. Each of the four things it needs from a
// sibling module is declared here as the narrowest interface that expresses the need, and the
// composition root (cmd/server/main.go) writes the four-line adapter that satisfies it. That is the
// landedcost.Capitalizer / tasks.OrderReader pattern, and it buys three specific things:
//
//   - dispatch's unit tests run with no order service, no signature vault and no print queue;
//   - a change to pos.TypedAttestation's shape is a compile error in ONE adapter, not a rewrite here;
//   - the dependency direction is stated in the type system instead of in a convention nobody reads.

import (
	"context"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// OrderReader is the ORDER AUTHORITY seam: it answers "does this order exist, which branch owns it,
// and what state is it in". It is the source of the branch stamped on every stop, the caller never
// supplies one. Satisfied by an adapter over order.Repository.OrderBrief.
//
// DIB-483 (M3-G8 / M3-G12-WILLCALL-UUID) widened it by ONE method, ResolveOrderID.
//
// The gate's finding was that the Schedule form and the Will-call form identify an order by raw UUID
// text box, while the same yard's POS lookup has accepted "#SO-TRT-1041" since DIB-379. Nobody at a
// counter reads a UUID off anything; the number is what is printed on the paperwork.
//
// The fix deliberately does NOT add order-number parsing to this package. There is exactly one
// order-number resolver in the system, order.Repository.ResolveOrderID, and it already carries the
// two properties that are easy to get wrong and expensive to get wrong twice: a UUID passes through
// with no round trip (so a cross-branch deep link keeps its 403 semantics), and a NUMBER is resolved
// BRANCH-SCOPED (so sequential #SO- numbers are not an existence oracle for another branch's book).
// Re-deriving either of those here would be a second parser and, eventually, a second answer.
//
// So the seam widens and the composition root's existing adapter grows one delegating method. dispatch
// still imports no sibling module, and dispatch still owns no opinion about what an order number
// looks like.
type OrderReader interface {
	Brief(ctx context.Context, orderID string) (OrderBrief, error)
	// ResolveOrderID turns a caller-supplied reference, a real order id, or the visible order_no with
	// or without its leading '#', into the real id, branch-scoped. Satisfied by an adapter over
	// order.Repository.ResolveOrderID. A number the caller cannot reach resolves to the SAME
	// not-found a missing one does.
	ResolveOrderID(ctx context.Context, idOrNo string, global bool, branchIDs []string) (string, error)
}

// OrderFulfiller is the INVOICE HANDOFF seam. Delivering the last stop for an order advances that
// order toward invoicing; this is the only way dispatch touches order state.
//
// It is deliberately NOT "set status to X". The order module grades every system-driven move against
// its own forward-only ladder (order.Service.AdvanceFromEvent), so an order a POS ring already
// carried past FULFILLED declines the advance instead of being walked backwards. Naming the method
// for the FACT (this was fulfilled) rather than the WRITE (set FULFILLED) keeps that judgement where
// it belongs, with the module that owns the ladder.
type OrderFulfiller interface {
	MarkFulfilled(ctx context.Context, orderID, reason string) error
}

// SignatureCapturer is the DIB-386 typed-name attestation seam. Satisfied by an adapter over
// pos.Service.CaptureTypedSignature, which seals the real payload (name + affirmative + actor +
// subject + timestamp) through the shared AES-256-GCM vault and stamps TYPED_NAME_V1.
//
// The seam takes the whole attestation, not (purpose, refType, refID): the two refusals that make it
// evidence, a blank name is not a signature, and the affirmative is a SEPARATE ACT, are only
// expressible if the name and the affirmative reach the provider. A narrower seam would have forced
// this module to fabricate them, which is the exact stub behaviour DIB-386 deleted.
type SignatureCapturer interface {
	CaptureTyped(ctx context.Context, in TypedAttestation) (signatureRef string, err error)

	// CaptureTypedTx is the same capture riding the CALLER'S transaction. A confirm that already
	// holds a transaction (the pickups sync Write) must capture through it, a pool capture there
	// would be a second acquisition while the transaction holds its connection (connection
	// starvation at pool size, and rows that commit outside the confirm they belong to). It is on
	// this interface rather than a second one because it is the same question: a capturer that
	// cannot ride the caller's transaction says so here, at compile time of its wiring, instead of
	// a write path discovering it mid-transaction.
	CaptureTypedTx(ctx context.Context, tx pgx.Tx, in TypedAttestation) (signatureRef string, err error)
}

// PickCompletionReader is the RACK-TAG seam, and it exists to reverse a dependency that was pointing
// the wrong way.
//
// THE DEPENDENCY IT REVERSES. 0092 shipped will-call on the sentence "staff mint a pickup token when
// the order goes on the rack". That presupposes somebody already pulled the material, OFF-SYSTEM,
// with nothing recording that they had. The tag existed first and the pull was assumed; a code could
// be printed and hung on a rack holding nothing. After this the arrow runs the other way: the tag
// exists BECAUSE a recorded pick was staged. The rack tag describes a pick that happened rather than
// instructing one that might.
//
// IT IS A SEAM AND NOT AN IMPORT for the reason every seam in this file is one: dispatch does not
// import tasks and tasks does not import dispatch. The composition root satisfies it over the yard
// module's own read, and dispatch keeps no opinion about what a pick task looks like.
//
// NIL IS ALLOW. Un-wired, MintWillCall behaves exactly as it shipped, which is what makes this
// additive rather than a flag day, and what keeps every existing suite honest: a deployment that has
// not wired the yard has not lost the ability to print a tag.
type PickCompletionReader interface {
	OrderPickState(ctx context.Context, orderID string) (PickCompletion, error)
	// OrderPickStates is the same question about many orders in one round trip.
	//
	// IT IS ON THIS INTERFACE RATHER THAN A SECOND ONE because it is the same question, and two
	// seams asking one question is how the two answers eventually disagree. The D-110 pickups card
	// needs the whole open-pickup population graded at once; grading it one order at a time would be
	// N round trips to paint one number, and grading it in SQL would mean dispatch reading the tasks
	// module's own table, the dependency this seam exists to reverse.
	//
	// An order with no live pick is ABSENT from the map, never present-and-zero: "nobody has started"
	// and "the read failed" deserve different sentences, and a zero value that means both is how they
	// get confused.
	OrderPickStates(ctx context.Context, orderIDs []string) (map[string]PickCompletion, error)
}

// PickCompletion is what the yard can say about one order's pull. Three fields because the refusal
// needs to tell the difference between "nobody has started" and "somebody is halfway through", a
// counter person who is told only "no" walks to the yard to find out which.
type PickCompletion struct {
	// Exists is whether a pick for this order is on the yard's board at all.
	Exists bool
	// Staged is whether the material has actually been pulled and parked. It is the ONLY field that
	// gates the mint: a pick that is claimed or in progress is a promise, not a pallet.
	Staged bool
	// Status is the yard's own word for where the pick is, used verbatim in the refusal so the
	// sentence says something the person can act on. Never parsed here.
	Status string
}

// FreightApprovalFiler is the APPROVALS QUEUE seam (0195), "park this raise on the one queue this
// system has and tell me which row decides it".
//
// IT IS A SEAM AND NOT A CALL TO identity.Service for the reason every seam in this file is one, but
// there is a second reason here worth stating: this module already imports identity, for
// StaffContext and the two capability predicates. That import is to a package of PURE FUNCTIONS AND
// A CONTEXT KEY, no service, no store, no state. Reaching from here into identity's *Service to
// file a row would turn a value dependency into a runtime one, and dispatch's unit tests would need
// an identity service, a store, and a database to exercise a freight split.
//
// THE SEAM CARRIES NO MONEY, WHICH IS THE POINT. It takes a subject, a branch, a requester and a
// sentence. The quoted-versus-proposed figures never cross it, so identity never holds a copy of a
// customer's delivery charge in order to route a decision about it, and the approvals list, which
// every authenticated staff member can read, and which has no money gate of its own, has nothing to
// leak. The figures stay in delivery_freight_raises behind this module's own visibility policy.
//
// NIL IS REFUSE, NOT ALLOW. Every other optional seam in this file degrades toward doing less; this
// one degrades toward doing NOTHING, because the thing it would skip is the approval. An unwired
// queue means a raise above the quoted freight cannot be filed, so it is refused in words, never
// applied on the grounds that nobody was available to object.
type FreightApprovalFiler interface {
	FileFreightApproval(ctx context.Context, raiseID, branchID, requestedBy, detail string) (approvalID string, err error)
}

// OrderFreightWriter is the RAISED-CHARGE seam: the order's own freight figure, moved.
//
// WHY THIS EXISTS AT ALL. `orders.freight_cents` is the number a split divides (0184:251, and the
// only writer before this was quote/convert.go, which stamps it equal to the quoted baseline). If an
// approved raise wrote only the per-load shares, the order would still say it carries the OLD
// freight, and the next split, dividing that stale figure, would silently undo a rise a general
// manager had signed off. A raise that does not move the number the next arithmetic reads is not a
// raise; it is a rounding error waiting for a second dispatcher.
//
// IT TAKES A TRANSACTION because both callers have one and neither may half-apply. The approve path
// runs inside identity's decide transaction (the stamp and the money commit together); the direct,
// un-gated path opens dispatch's own, so writing the shares and moving the header cannot diverge.
//
// IT IS A SET AND NOT AN ADD. An additive seam ("add 4000 cents") is not idempotent under a retry
// and cannot be reasoned about from the row afterwards; the absolute figure is the one the approver
// actually agreed to, and it is what the proposal stored.
type OrderFreightWriter interface {
	SetOrderFreightTx(ctx context.Context, tx pgx.Tx, orderID string, cents int64) error
}

// PrintEnqueuer is the W1a print_queue seam (DIB-411). Satisfied by an adapter over
// printing.QueueService.Enqueue.
//
// The manifest rides the EXISTING DELIVERY_DOC document type rather than adding an enum value. 0088
// wrote that type into the CHECK for exactly this document, "the driver's delivery paperwork", in
// its own words, and it has never had a producer. Widening another module's enum (a migration
// against a table this bundle does not own, plus an edit to printing.docTypes, which is outside this
// fence) to add a second name for the same concept would have been ceremony, not extension.
type PrintEnqueuer interface {
	Enqueue(ctx context.Context, in PrintJobRequest) (jobID string, err error)
}

// RegisterRoutes self-registers the dispatch surface behind the identity guards.
//
// THE GATING RULE. Reads and the operational lifecycle are RequireStaff, because the people who
// actually run a delivery day, yard team, drivers, inside sales taking the call, are frontline
// roles, and a dispatch board only managers can touch is a dispatch board nobody uses. The BRANCH
// fence in service.go is what makes that safe: staff-gated does not mean unfenced.
//
// RequireManager holds the two writes with consequences outside the day: registering fleet (a truck
// row outlives every run on it) and revoking a live will-call token.
//
// The public will-call routes take NO guard, by design, the token IS the credential (0092's header
// states the model). They are registered last and separately so that fact is visible at the call
// site rather than buried in a path prefix.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	// fleet registry
	r.Handle("GET /api/v1/dispatch/trucks", g.RequireStaff(h.staff(h.listTrucks)))
	r.Handle("POST /api/v1/dispatch/trucks", g.RequireManager(h.staff(h.createTruck)))
	r.Handle("GET /api/v1/dispatch/drivers", g.RequireStaff(h.staff(h.listDrivers)))
	r.Handle("POST /api/v1/dispatch/drivers", g.RequireManager(h.staff(h.createDriver)))

	// the board + scheduling
	r.Handle("GET /api/v1/dispatch/board", g.RequireStaff(h.staff(h.board)))
	r.Handle("POST /api/v1/dispatch/stops", g.RequireStaff(h.staff(h.schedule)))
	r.Handle("GET /api/v1/dispatch/stops/{id}", g.RequireStaff(h.staff(h.stop)))
	r.Handle("POST /api/v1/dispatch/stops/{id}/assign", g.RequireStaff(h.staff(h.assignStop)))
	r.Handle("POST /api/v1/dispatch/stops/{id}/status", g.RequireStaff(h.staff(h.advanceStop)))
	r.Handle("POST /api/v1/dispatch/stops/{id}/pod", g.RequireStaff(h.staff(h.capturePOD)))
	r.Handle("POST /api/v1/dispatch/stops/{id}/photos", g.RequireStaff(h.staff(h.uploadPhoto)))
	r.Handle("GET /api/v1/dispatch/photos/{id}", g.RequireStaff(h.staff(h.photo)))

	// runs
	r.Handle("GET /api/v1/dispatch/runs", g.RequireStaff(h.staff(h.listRuns)))
	r.Handle("POST /api/v1/dispatch/runs", g.RequireStaff(h.staff(h.createRun)))
	r.Handle("GET /api/v1/dispatch/runs/{id}", g.RequireStaff(h.staff(h.run)))
	// The NUMBER lookup lives on its OWN TOP-LEVEL path, not under /runs/.
	//
	// Two reasons, one of which cost a boot. (1) Semantics: an id lookup answers 403 across branches
	// and a number lookup answers 404 (service.RunByNo says why), so collapsing them onto one path
	// would make the weaker rule apply to both. (2) Routing: `/runs/by-no/{runNo}` and
	// `/runs/{id}/manifest` are ambiguous to net/http's mux, neither is more specific, so
	// registering both PANICS at startup. That is a server that does not boot, and it was caught by
	// the fence test building the real route table rather than calling the service directly.
	r.Handle("GET /api/v1/dispatch/run-by-no/{runNo}", g.RequireStaff(h.staff(h.runByNo)))
	r.Handle("POST /api/v1/dispatch/runs/{id}/crew", g.RequireStaff(h.staff(h.assignCrew)))
	r.Handle("POST /api/v1/dispatch/runs/{id}/dispatch", g.RequireStaff(h.staff(h.dispatchRun)))
	r.Handle("POST /api/v1/dispatch/runs/{id}/complete", g.RequireStaff(h.staff(h.completeRun)))
	r.Handle("POST /api/v1/dispatch/runs/{id}/cancel", g.RequireStaff(h.staff(h.cancelRun)))
	r.Handle("GET /api/v1/dispatch/runs/{id}/manifest", g.RequireStaff(h.staff(h.manifest)))
	r.Handle("POST /api/v1/dispatch/runs/{id}/manifest/print", g.RequireStaff(h.staff(h.printManifest)))

	// - the LOAD BOARD (0184, the Aug-26 call) -
	//
	// GATING. Reads are RequireStaff for the same reason the day board is: the people who build loads
	// at 06:45 are frontline. What makes that safe here is not the mux, it is the BRANCH fence plus
	// three service-level gates that no route registration can be refactored around: a load's money
	// figures are marshalled only for a caller the dealer's policy allows, only a DISPATCH role may
	// commit a date (identity.CanCommitDelivery, a salesperson is refused whatever they send), and a
	// COMMITTED load refuses its own mutation.
	//
	// Note that /commit stays RequireStaff at the mux and is gated by CAPABILITY in the service. A
	// RequireManager here would be the wrong shape twice over: it would lock out YARD_TEAM, who build
	// the trucks, and let SALES_MANAGER in, who must never commit. The mux cannot express "dispatch",
	// so the mux does not pretend to.
	r.Handle("GET /api/v1/dispatch/loadboard", g.RequireStaff(h.staff(h.loadBoard)))
	r.Handle("GET /api/v1/dispatch/loadboard/calendar", g.RequireStaff(h.staff(h.loadBoardCalendar)))
	r.Handle("GET /api/v1/dispatch/loadboard/gaps", g.RequireStaff(h.staff(h.loadBoardGaps)))
	// The same scan, keyed on ONE order, the yard's view of it. Staff-gated at the door and
	// branch-fenced in the service, exactly like its whole-day sibling above.
	r.Handle("GET /api/v1/dispatch/orders/{id}/gaps", g.RequireStaff(h.staff(h.orderGaps)))
	// The bulk load. Staff-gated at the door and branch-fenced PER STOP in the service, so a list is
	// not a way around the fence.
	r.Handle("POST /api/v1/dispatch/loads/mark-loaded", g.RequireStaff(h.staff(h.markLoaded)))
	// Who changed which rule, and what it was before. Manager-gated in the service to match
	// SavePolicy: somebody who may change the rules may see who changed them.
	r.Handle("GET /api/v1/dispatch/policy/history", g.RequireStaff(h.staff(h.policyHistory)))
	r.Handle("GET /api/v1/dispatch/policy", g.RequireStaff(h.staff(h.readDispatchPolicy)))
	r.Handle("PUT /api/v1/dispatch/policy", g.RequireManager(h.staff(h.saveDispatchPolicy)))
	// REOPENING IS NORMAL, and it is registered as an ordinary route on the LOAD, because that is the
	// object a dispatch manager is looking at when a better-fitting order turns up. RequireManager is
	// the mux's coarse half; canReopenLoad in the service is the real one.
	r.Handle("POST /api/v1/dispatch/stops/{id}/reopen", g.RequireManager(h.staff(h.reopenStop)))
	r.Handle("POST /api/v1/dispatch/runs/{id}/reopen", g.RequireManager(h.staff(h.reopenRun)))
	r.Handle("POST /api/v1/dispatch/runs/{id}/reopen/clear", g.RequireStaff(h.staff(h.clearRunReopen)))
	r.Handle("POST /api/v1/dispatch/stops/{id}/commit", g.RequireStaff(h.staff(h.commitStop)))
	r.Handle("POST /api/v1/dispatch/stops/{id}/load-facts", g.RequireStaff(h.staff(h.setLoadFacts)))
	r.Handle("POST /api/v1/dispatch/freight-split", g.RequireStaff(h.staff(h.splitFreight)))
	// The parked raise behind an approval card (0195). Staff-gated at the door like every other read
	// on this board; branch-fenced AND money-gated in the service, which is what actually decides
	// whether the figures on it reach the caller. An approver who may not see load money gets the
	// subject and no numbers, the same payload, minus a key.
	r.Handle("GET /api/v1/dispatch/freight-raises/{id}", g.RequireStaff(h.staff(h.freightRaise)))
	// The Pro/AI seam. It answers 501 with a sentence in the core build (handler_loadboard.go says
	// why the route exists at all rather than being absent).
	r.Handle("POST /api/v1/dispatch/auto-assign", g.RequireStaff(h.staff(h.autoAssign)))

	// the driver's own day
	r.Handle("GET /api/v1/dispatch/my-runs", g.RequireStaff(h.staff(h.myRuns)))

	// will-call (DIB-79), staff side
	r.Handle("POST /api/v1/dispatch/will-call", g.RequireStaff(h.staff(h.mintWillCall)))
	r.Handle("GET /api/v1/dispatch/will-call", g.RequireStaff(h.staff(h.listWillCall)))
	r.Handle("POST /api/v1/dispatch/will-call/{id}/revoke", g.RequireManager(h.staff(h.revokeWillCall)))
}

// RegisterPublicRoutes registers the UNAUTHENTICATED will-call surface the QR square points at.
//
// Kept a separate method with a separate name so that "these two routes have no guard" is a decision
// someone makes explicitly in main.go, not something that happens because a path started with the
// right prefix. Mirrors quote.Handler.RegisterPublicRoutes.
func (h *Handler) RegisterPublicRoutes(r httpx.Registrar) {
	r.Handle("GET /api/public/v1/will-call/{token}", http.HandlerFunc(h.publicWillCall))
	r.Handle("POST /api/public/v1/will-call/{token}/confirm", http.HandlerFunc(h.publicConfirmWillCall))
}
