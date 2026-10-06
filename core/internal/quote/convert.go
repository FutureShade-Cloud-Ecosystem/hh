// convert.go, DIB-483 (M3-G7 / M3-G12): THE conversion, and the fulfilment choice made at it.
//
// - WHY THIS FILE EXISTS -
// Until now the only door onto quote→order conversion was the CONTRACTOR one: POST
// /api/portal/v1/quotes/{id}/convert, behind a portal session. Everything that makes a conversion
// safe, the credit/hold gate (credit.go), the soft→hard reservation swap (reserve.go), the branch-
// coded order mint, the CONVERTED transition, lives INSIDE Repository.Convert, one transaction deep.
// A phone-accepted quote therefore could not be converted at the counter by the person the customer
// was standing in front of. The M3 gate calls that a blocker (M3-G7-NO-STAFF-CONVERT).
//
// The wrong fix is a second conversion. Two convert paths means two credit gates, and the day they
// disagree is the day someone on an ACTIVE hold walks out with $112,200 of stone because the counter
// door forgot to ask. So this file holds runConvert: ONE function, called by BOTH doors and by nothing
// else. The doors differ only in how they establish WHOSE quote this is -
//
//	portal (Service.Convert)         : the session IS the customer; customerID comes from the token.
//	staff  (StaffService.ConvertQuote): the caller proves BRANCH reach, then the customer is read off
//	                                    the quote itself (QuotePricingContext).
//
// , and then both hand the same (customerID, quoteID, options) to the same function. The tenant
// scoping that Repository.Convert enforces in SQL (`customer_id = $1`, including the FOR UPDATE lock)
// is untouched by the staff door: it is handed the quote's OWN customer, so the statement is as
// narrow as it ever was.
//
// - THE FULFILMENT CHOICE (D-090, ruled) -
// A conversion is where an order first learns how it leaves the yard. orders.fulfillment (0008)
// already carries PICKUP / DELIVERY / WILL_CALL with a DEFAULT of 'PICKUP', and dispatch's will-call
// mint (dispatch/willcall.go:84) refuses any order that is not WILL_CALL, so before this change the
// only way to get a will-call order was to write the column by hand.
//
// ConvertOptions.Fulfillment is therefore OPTIONAL and its zero value is "say nothing": when it is
// empty the INSERT does not name the column at all and the database default applies, which is
// byte-identical to the statement that shipped. That is not a nicety, it is the safety property the
// zero-delta test pins, because "we added a field and every existing conversion changed" is exactly
// the regression a gate is for.
package quote

import (
	"context"
	"fmt"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// Fulfilment values a CONVERSION may choose. DELIVERY is deliberately absent: a delivery is not a
// property somebody types at the counter, it is a scheduled stop with a date, an address and a truck
// (internal/dispatch), and offering it here would create a second, weaker way to say "this is a
// delivery" beside the one that already books the run.
const (
	FulfillPickup   = "PICKUP"
	FulfillWillCall = "WILL_CALL"
)

// ConvertOptions is the caller's choice at conversion time.
//
// Fulfillment == "" means UNSPECIFIED, not PICKUP. The distinction is the whole point: unspecified
// leaves the column out of the INSERT and lets 0008's DEFAULT 'PICKUP' apply, so a caller that never
// heard of this field produces the identical row it produced before DIB-483.
type ConvertOptions struct {
	Fulfillment string

	// paidAtCounter and paidTotalCents are QD-5's: the order is paid in full, at the agreed total,
	// in the same counter action (order_payment.go), so the credit gate judges it as a cash sale.
	// Unexported on purpose: only this package's payment door can set them, never the portal door,
	// the plain staff door or a request body. The conversion re-asserts the total under its lock.
	paidAtCounter  bool
	paidTotalCents int64
}

// normalizeFulfillment grades the caller's choice. It is case- and space-forgiving (a counter user
// typing "will call" into a control is not an error worth a 400) but it names the two legal answers
// when it refuses, because "invalid fulfillment" tells the person at the counter nothing.
//
// Empty in, empty out: unspecified stays unspecified all the way to the SQL.
func normalizeFulfillment(v string) (string, error) {
	s := strings.ToUpper(strings.TrimSpace(v))
	if s == "" {
		return "", nil
	}
	s = strings.ReplaceAll(strings.ReplaceAll(s, " ", "_"), "-", "_")
	switch s {
	case FulfillPickup, FulfillWillCall:
		return s, nil
	case "DELIVERY":
		return "", httpx.BadRequestf(
			"a delivery is booked by scheduling a stop, not chosen at conversion, convert as pickup or will-call, then schedule the delivery")
	default:
		return "", httpx.BadRequestf("fulfillment must be %s or %s", FulfillPickup, FulfillWillCall)
	}
}

// convertStore is the ONE persistence method a conversion needs. Both doors' stores satisfy it
// (*Repository does the work; the fakes honour the same contract), which is what lets runConvert be
// written once against an interface rather than twice against two services.
type convertStore interface {
	Convert(ctx context.Context, customerID, quoteID string, opt ConvertOptions) (ConvertResult, error)
}

// runConvert IS the conversion. Both the contractor door and the counter door call this and nothing
// else; there is exactly one `store.Convert(` call site in this package outside tests, and
// convert_seam_test.go fails the build's intent if a second one appears.
//
// The two things it does beyond delegating are the two things that must not diverge between doors:
//
//   - it grades the fulfilment choice (so a bad value is the same 400 with the same words at both
//     doors, not a 400 at one and a CHECK-constraint 500 at the other);
//   - it seeds the DIB-159 order-change history with the creation transition ""→DRAFT, best-effort,
//     AFTER the conversion has committed. A counter-converted order and a portal-converted order
//     therefore have the same timeline, which is the whole reason that history exists.
//
// actorID is who to attribute the creation row to: the customer for the portal door, the staff user
// for the counter door. It never affects the write, only the audit attribution.
func runConvert(ctx context.Context, store convertStore, rec audit.Recorder,
	customerID, quoteID, actorID string, opt ConvertOptions) (ConvertResult, error) {

	if customerID == "" || quoteID == "" {
		return ConvertResult{}, httpx.BadRequestf("customer id and quote id are required")
	}
	f, err := normalizeFulfillment(opt.Fulfillment)
	if err != nil {
		return ConvertResult{}, err
	}
	opt.Fulfillment = f

	res, err := store.Convert(ctx, customerID, quoteID, opt)
	if err != nil {
		return ConvertResult{}, err
	}

	// DIB-159 order-change history: the conversion is a creation writer, the order is INSERTed with
	// no status column, so it defaults to DRAFT (0008). Without this row a converted order has an
	// empty history even though it plainly exists. Best-effort and post-commit: a history failure has
	// no business refusing an order that is already written.
	if rec != nil && res.OrderID != "" {
		actor := actorID
		if actor == "" {
			actor = customerID
		}
		extra := map[string]any{"old_status": "", "new_status": "DRAFT"}
		if f != "" {
			extra["fulfillment"] = f
		}
		entry := audit.SensitiveAccess{
			Action: audit.ActOrderStatusChanged, EntityType: "order", EntityID: res.OrderID,
			ActorID: actor, Purpose: "ORDER_STATUS_TRANSITION", Reason: "quote_converted",
			Extra: extra,
		}
		// QD-5: a conversion that runs on the payment door's transaction writes its history row on that
		// transaction too, so the order and its history exist together or not at all, and a failure
		// here aborts the action. Every other conversion stays best-effort and post-commit.
		if tx, ok := txFrom(ctx); ok {
			if tr, ok := rec.(txRecorder); ok {
				if err := tr.RecordTx(ctx, tx, entry); err != nil {
					return ConvertResult{}, fmt.Errorf("convert quote %q: record the order's creation: %w", quoteID, err)
				}
				return res, nil
			}
		}
		_ = rec.Record(ctx, entry)
	}
	return res, nil
}

// fulfillmentColumns renders the optional fulfilment into the two SQL fragments Repository.Convert
// splices into its INSERT. Unspecified returns two empty strings, so the statement is character-for-
// character the one that shipped before DIB-483, see convert_default_test.go, which pins exactly
// that and dies if this function ever starts naming the column unconditionally.
//
// The value is NOT interpolated: it is normalized to one of two package constants above before it
// reaches here, and what is spliced is the CONSTANT, never caller text.
func fulfillmentColumns(f string) (cols, vals string, err error) {
	switch f {
	case "":
		return "", "", nil
	case FulfillPickup:
		return ", fulfillment", ", 'PICKUP'", nil
	case FulfillWillCall:
		return ", fulfillment", ", 'WILL_CALL'", nil
	default:
		// Unreachable via runConvert (normalizeFulfillment is the only way in). Fail loudly rather
		// than silently dropping a fulfilment the caller asked for.
		return "", "", fmt.Errorf("quote: unsupported fulfillment %q", f)
	}
}

// convertInsertSQL builds the conversion's order-header INSERT.
//
// It is a named function rather than an inline string so the zero-delta test can hold it against a
// frozen copy of the pre-DIB-483 statement and assert they are the SAME BYTES when no fulfilment is
// chosen. A splice point in the middle of a financial INSERT is worth that much ceremony: the failure
// it guards against (every conversion silently changes because a default moved) is invisible in
// review and obvious only in production.
//
// cols/vals arrive from fulfillmentColumns and are package CONSTANTS or empty, never caller text.
//
// - 0194: THE FREIGHT PAIR -
//
// Two columns were added to this statement together, and they must stay together:
//
//	freight_cents         the OPENING ACTUAL, what dispatch will divide across the loads.
//	quoted_freight_cents  the FROZEN BASELINE, what the customer was shown, which never moves again.
//
// They are equal at conversion and diverge the moment somebody adds a truck. Writing only the
// baseline would have left `orders.freight_cents` exactly as it was before 0194: a column with no
// production writer anywhere in the tree, which dispatchOrderValueAdapter reads through a
// `cents <= 0 -> unknown` rule, so SplitFreight refuses every order in words before doing any
// arithmetic. A baseline with nothing to compare against is a gate that can never fire.
//
// THE FEE IS NOT ADDED TO total_cents, and this is the trap to avoid. The quote's own total ALREADY
// contains the delivery fee, builder_repository.go:264-269 folds it in, and the SELECT below copies
// total_cents verbatim. Breaking the fee out into its own column is a RESTATEMENT of a figure the
// total already carries, not an addition to it; anything that later sums total_cents + freight_cents
// double-charges the customer.
//
// The two new columns are spliced BEFORE the fulfilment splice points, so the positional pairing
// fulfillmentColumns depends on is untouched.
func convertInsertSQL(cols, vals string) string {
	return `INSERT INTO orders (order_no, customer_id, branch_id, quote_id, subtotal_cents, tax_cents, total_cents, freight_cents, quoted_freight_cents` + cols + `)
		 SELECT next_order_no(branch_id), customer_id, branch_id, id, subtotal_cents, tax_cents, total_cents, delivery_fee_cents, delivery_fee_cents` + vals + `
		 FROM quotes WHERE id=$2 AND customer_id=$1
		 RETURNING id::text, order_no::text`
}
