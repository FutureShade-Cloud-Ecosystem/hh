package reporting

// seams.go, the CONSUMER-DECLARED interfaces this module needs from siblings. Each is declared here,
// by the consumer, over a provider this package must not import; the composition root (cmd/server) is
// the only place the two meet.
//
// There is exactly one this wave, and the reason there is only one is worth stating: a reporting module
// reads TABLES, and tables need no seam. A seam is for BEHAVIOUR, a decision another module owns and
// that this module must not re-implement. Rep attribution is the only such decision here.

import "context"

// RepAssignmentReader is the read seam over the W5-D rep-assignment substrate (0136, DIB-273/DIB-333).
//
// -- WHY A SEAM AT ALL, GIVEN THE SQL BELOW READS THE SAME TABLE --------------------------------------
// Two different questions, answered two different ways, on purpose:
//
//	BULK   sales_by_rep and the commission run attribute thousands of invoice lines in one statement.
//	       They LEFT JOIN rep_assignments directly. A per-row seam call would be a query per line and a
//	       report nobody waits for; and the join is over a plain, stable, read-only table.
//	SINGLE "who owns this order?" on a drill-through, and the CONFORMANCE CHECK below, go through this
//	       seam, because that is the question whose ANSWER the messaging module owns.
//
// The risk this creates is the obvious one: the bulk SQL could drift from the owner's ruling and nobody
// would notice, which would show up as a rep quietly losing commission on split-book orders. That risk
// is closed by a named test rather than by a comment -
// TestIntegration_SalesByRep_AgreesWithRepAssignmentSeam walks every attributed invoice in the fixture
// window, asks THIS seam, and fails if the SQL and the owner disagree on a single one.
//
// UNBOUND BEHAVIOUR. When the composition root does not bind this (scaffold mode, or a deployment where
// messaging is off), the bulk attribution still works, it is SQL, and the conformance test skips with
// a named reason. Nothing silently changes its answer; the seam's absence removes a CHECK, not a
// FEATURE. That is the correct failure: the alternative, making rep attribution depend on messaging
// being wired, would mean a commission run producing zeros on a stack where messaging is disabled.
//
// Bound in cmd/server/main.go by w6bRepAssignmentReader over *messaging.Service.ResolveRep.
type RepAssignmentReader interface {
	// ResolveRep answers "which rep owns this sale?" for one customer, optionally narrowed by the order
	// that carries a split-book override. level is the owner module's own answer for WHY -
	// "ORDER" | "CUSTOMER" | "NONE". An empty repUserID with level "NONE" is the house queue and is a
	// successful answer, not an error.
	ResolveRep(ctx context.Context, customerID, orderID string) (repUserID, level string, err error)
}

// Assignment levels, mirrored from the owner module so this package can name them without importing it.
// They are compared, never produced, here: if messaging ever adds a level, this module reports it
// verbatim rather than mapping it to something it recognises.
const (
	RepLevelOrder    = "ORDER"
	RepLevelCustomer = "CUSTOMER"
	RepLevelNone     = "NONE"
)
