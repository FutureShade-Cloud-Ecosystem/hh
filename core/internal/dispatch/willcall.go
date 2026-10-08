package dispatch

// willcall.go, DIB-79: the QR square on a will-call rack tag, and the self-confirmation behind it.
//
// THE GAP IT CLOSES. A WILL_CALL order sits on the rack until someone drives in. Who actually took it
// was recorded nowhere: the counter marked the order fulfilled from memory, often the next morning.
// That is the same evidence gap the delivery POD closes, and it is closed the same way, through the
// SAME typed-name attestation seam, so the two pieces of proof in this system are one kind of thing.
//
// THE SECURITY MODEL IS quote/share.go's, VERBATIM, because a printed QR square and an emailed link
// prove exactly the same thing: possession. Whoever holds it is treated as the collector. Therefore:
// one order and nothing else, an expiry, revocable, sha256-hashed at rest, no staff-only field in the
// public payload, every scan logged, and 410-not-404 on a dead token so the holder learns the CODE
// died rather than being told the order never existed.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// defaultPickupTTL is the fallback tag lifetime. It is a DEFAULT, not a policy: the value is
// persisted per row (0092), so editing this constant never retroactively extends or kills a square
// already printed and hanging on a rack.
const defaultPickupTTL = 14 * 24 * time.Hour

// maxCollectorName bounds the typed name (quote/share.go's bound, same act).
const maxCollectorName = 120

// newPickupToken returns a raw token (shown ONCE, at mint) and its sha256 hex hash (the only thing
// stored). 32 bytes of crypto/rand: guessing a live token is not a threat model worth rate-limiting.
// Mirrors quote.newShareToken / portal.newInviteToken / identity.newResetToken, the three existing
// token primitives here, all of which do exactly this.
func newPickupToken() (raw, hashed string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, hashPickupToken(raw), nil
}

func hashPickupToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// MintedPickup is what the staff member printing the tag gets back: the row, plus the RAW token -
// the only time it exists outside the customer's phone.
type MintedPickup struct {
	WillCall
	Token string `json:"token"`
}

// MintWillCall issues a pickup token for an order.
//
// The order must be WILL_CALL fulfilment. A DELIVERY order gets a POD from the driver and a PICKUP
// order is handed over at the counter with a person present; minting a self-confirmation square for
// either would create a second, weaker path to "someone took this" beside the one that already
// exists.
func (s *Service) MintWillCall(ctx context.Context, orderID string, ttl time.Duration) (MintedPickup, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return MintedPickup{}, err
	}
	// DIB-483 / M3-G12-WILLCALL-UUID: the rack-tag form takes the #SO number off the paperwork, not a
	// UUID. Same grader, same single resolver as the Schedule form (orderref.go).
	resolved, err := s.resolveOrderRef(ctx, caller, orderID)
	if err != nil {
		return MintedPickup{}, err
	}
	brief, err := s.orders.Brief(ctx, resolved)
	if err != nil {
		return MintedPickup{}, ErrNotFound
	}
	if !caller.CanAccessBranch(brief.BranchID) {
		return MintedPickup{}, ErrForbidden
	}
	if brief.Fulfillment != "WILL_CALL" {
		// THE ORDER IS NOT NAMED, and that is the fix rather than an omission. The sentence used to
		// open "order <uuid> is a ...", a row identifier on a counter screen, which is the same
		// defect in a second costume and the one part of the sentence nobody could act on. The
		// operator typed the reference a moment ago; what they lack is the REASON, which is now all
		// the sentence carries. Widening the frozen four-field OrderBrief to carry an order number
		// just to decorate a refusal would make every consumer of the brief pay for it (module.go).
		return MintedPickup{}, conflictf(
			"dispatch: this is %s, not will-call, a rack tag prints only for a will-call order",
			fulfilmentWord(brief.Fulfillment))
	}
	switch brief.Status {
	case "DRAFT":
		return MintedPickup{}, conflictf("dispatch: confirm the order before printing a pickup code")
	case "CANCELLED", "CLOSED":
		return MintedPickup{}, conflictf("dispatch: %s", orderStateWord(brief.Status))
	}
	// THE TAG FOLLOWS THE PICK, not the other way round (module.go, PickCompletionReader). 0092's
	// sentence was "mint a code when the order goes on the rack", which quietly assumed somebody had
	// already pulled the material with nothing recording that they had. Now the pull is a fact the
	// yard wrote down, and the tag is minted from it.
	if err := s.requirePicked(ctx, brief.ID); err != nil {
		return MintedPickup{}, err
	}
	// Item 9.3: no rack tag for an order held from picking under a pay before
	// release shape, the goods wait for the money (pick_hold.go).
	if err := s.refuseHeldFromPicking(ctx, brief.ID); err != nil {
		return MintedPickup{}, err
	}
	if ttl <= 0 {
		ttl = defaultPickupTTL
	}
	raw, hashed, err := newPickupToken()
	if err != nil {
		return MintedPickup{}, err
	}
	w, err := s.repo.InsertPickup(ctx, brief.ID, brief.BranchID, hashed, caller.UserID, s.now().UTC().Add(ttl))
	if err != nil {
		// The partial unique index (one ACTIVE token per order) is the guard against two live squares
		// on two tags in the same yard. Revoke the old one first, deliberately an explicit act, so a
		// reprint is a decision rather than a side effect.
		return MintedPickup{}, asConflict(err, "dispatch: this order already has a live pickup code, revoke it before printing another")
	}
	s.record(ctx, audit.SensitiveAccess{
		Action: "WILL_CALL_MINTED", EntityType: "will_call_pickup", EntityID: w.ID,
		Extra: map[string]any{"order_id": brief.ID},
	})
	return MintedPickup{WillCall: w, Token: raw}, nil
}

// requirePicked is the rack-tag gate. Nil seam ALLOWS, which is 0092's behaviour exactly.
//
// ONLY STAGED PASSES. A pick that is claimed or in progress is a promise, not a pallet; printing a
// square against it puts a tag on a rack before there is anything on the rack, which is the state
// this gate exists to end.
//
// A FAILED READ REFUSES rather than waving the tag through. The whole point of the reversal is that
// the tag is evidence the material was pulled; a tag minted because a query timed out is evidence of
// nothing, and it would be indistinguishable from a real one on the rack.
//
// THE REFUSAL NAMES THE WAY FORWARD in each of its three cases, because "no" sends somebody walking
// out to the yard to find out which one it was.
func (s *Service) requirePicked(ctx context.Context, orderID string) error {
	if s.picks == nil {
		return nil
	}
	state, err := s.picks.OrderPickState(ctx, orderID)
	if err != nil {
		s.log.Warn("dispatch: pick state read failed at will-call mint", "order", orderID, "err", err)
		return conflictf("dispatch: the yard could not be asked whether this order has been picked, try again in a moment")
	}
	if !state.Exists {
		return conflictf("dispatch: nothing has been picked for this order yet, it is in the yard's queue, and the tag prints once the pick is staged")
	}
	if !state.Staged {
		return conflictf("dispatch: this order is %s, the tag prints once the pick is staged", pickStateWord(state.Status))
	}
	return nil
}

// pickStateWord turns the yard's enum into the words a counter person uses out loud.
//
// The house rule is that client-facing copy carries zero internal engineering vocabulary, and
// "ACCEPTED" is a row in a state machine, not a thing anybody says about a pallet. An unmapped status
// falls through to the lowercased enum rather than to an invented phrase: an honest odd word beats a
// confident wrong one, and it makes a new yard state visible instead of silently mistranslated.
func pickStateWord(status string) string {
	switch status {
	case "QUEUED", "PUSHED":
		return "waiting in the yard's queue"
	case "ACCEPTED":
		return "claimed but not pulled yet"
	case "IN_PROGRESS":
		return "being picked now"
	default:
		return strings.ToLower(strings.ReplaceAll(status, "_", " "))
	}
}

// fulfilmentWord names how an order leaves, in the words the counter uses for it. Same rule and same
// default arm as pickStateWord above: an unmapped value degrades to an honest odd phrase rather than
// to a confident wrong one, so a new fulfilment method shows up as strange instead of as a lie.
func fulfilmentWord(fulfillment string) string {
	switch fulfillment {
	case "DELIVERY":
		return "a delivery"
	case "PICKUP":
		return "a counter pickup"
	case "WILL_CALL":
		return "a will-call order"
	default:
		return "a " + strings.ToLower(strings.ReplaceAll(fulfillment, "_", " ")) + " order"
	}
}

// orderStateWord says what has already happened to an order, rather than naming the row's status.
// "order is CLOSED" is a column value; "this order is already closed" is the same fact in a sentence
// the person holding the rack tag can act on.
func orderStateWord(status string) string {
	switch status {
	case "CANCELLED":
		return "this order was cancelled"
	case "CLOSED":
		return "this order is already closed"
	default:
		return "this order is " + strings.ToLower(strings.ReplaceAll(status, "_", " "))
	}
}

// ListWillCall returns an order's pickup tokens (staff view, never a raw token).
func (s *Service) ListWillCall(ctx context.Context, orderID string) ([]WillCall, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	// The list is the same form's other button ("show me this order's tags"), so it must accept the
	// same references the mint does, otherwise one box takes a number and the box beside it does not.
	resolved, err := s.resolveOrderRef(ctx, caller, orderID)
	if err != nil {
		return nil, err
	}
	brief, err := s.orders.Brief(ctx, resolved)
	if err != nil {
		return nil, ErrNotFound
	}
	if !caller.CanAccessBranch(brief.BranchID) {
		return nil, ErrForbidden
	}
	return s.repo.ListPickups(ctx, brief.ID)
}

// RevokeWillCall kills a live token (manager-gated at the route).
func (s *Service) RevokeWillCall(ctx context.Context, pickupID string) error {
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	if !looksLikeUUID(pickupID) {
		return ErrNotFound
	}
	pool, err := s.repo.db()
	if err != nil {
		return err
	}
	var branchID, status string
	if err := pool.QueryRow(ctx,
		`SELECT branch_id::text, status FROM will_call_pickups WHERE id = $1`, pickupID).
		Scan(&branchID, &status); err != nil {
		return ErrNotFound
	}
	if !caller.CanAccessBranch(branchID) {
		return ErrForbidden
	}
	ok, err := s.repo.RevokePickup(ctx, pickupID, s.now().UTC())
	if err != nil {
		return err
	}
	if !ok {
		return conflictf("dispatch: this pickup code is already %s", strings.ToLower(status))
	}
	s.record(ctx, audit.SensitiveAccess{Action: "WILL_CALL_REVOKED", EntityType: "will_call_pickup", EntityID: pickupID})
	return nil
}

// resolvePickup turns a raw scanned token into a live row, or into the right refusal.
//
// THE THREE ANSWERS, AND WHY THEY DIFFER. No such token, ever → 404. A token that exists but is
// expired, revoked or already used → 410 GONE. A live one → the row. The 404/410 split is the whole
// point: "this code has expired, ask the counter" is actionable, and "not found" for a code the
// customer is literally holding is the kind of answer that produces a phone call.
func (s *Service) resolvePickup(ctx context.Context, rawToken string) (WillCall, error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return WillCall{}, ErrTokenNotFound
	}
	w, err := s.repo.PickupByTokenHash(ctx, hashPickupToken(rawToken))
	if err != nil {
		return WillCall{}, err
	}
	if w.Status != "ACTIVE" {
		return WillCall{}, ErrTokenGone
	}
	if !w.ExpiresAt.After(s.now().UTC()) {
		return WillCall{}, ErrTokenGone
	}
	return w, nil
}

// PublicWillCall is the scan: what the token holder sees before they confirm.
//
// It carries the order number, the branch, the customer and a line count, enough to know they are
// confirming the right pickup, and NOTHING a staff member would see. No prices, no cost, no margin,
// no other order. The payload is written on the assumption that whoever is reading it is not
// authenticated, because that is true.
func (s *Service) PublicWillCall(ctx context.Context, rawToken string) (PublicWillCall, error) {
	w, err := s.resolvePickup(ctx, rawToken)
	if err != nil {
		return PublicWillCall{}, err
	}
	_ = s.repo.LogPickupEvent(ctx, w.ID, "VIEW") // best-effort: a scan is never failed by its own log
	return s.publicView(ctx, w)
}

func (s *Service) publicView(ctx context.Context, w WillCall) (PublicWillCall, error) {
	d, err := s.repo.OrderDisplayFor(ctx, w.OrderID)
	if err != nil {
		return PublicWillCall{}, err
	}
	branch, err := s.repo.BranchName(ctx, w.BranchID)
	if err != nil {
		return PublicWillCall{}, err
	}
	return PublicWillCall{
		OrderNo: d.OrderNo, BranchName: branch, CustomerName: d.CustomerName,
		LineCount: d.LineCount, Status: w.Status, CollectedBy: w.CollectedBy,
	}, nil
}

// ConfirmWillCall is the customer's self-confirmation: a typed full name plus an explicit
// affirmative, sealed through the SAME provider that seals a delivery POD.
//
// THE TWO REFUSALS ARE RESTATED HERE ON PURPOSE. A blank name is not a signature, and the
// affirmative is a REQUIRED SEPARATE ACT, not an implied consequence of tapping a button. The
// provider refuses both too; refusing here as well means the customer gets a sentence about what to
// do rather than a generic 400 from two layers down, and it means the rule survives a provider swap.
//
// There is NO staff context on this path. The attestation's actor_id is therefore empty, which is
// honest: nobody on staff witnessed this. What the sealed row does carry is the typed name, the
// affirmative, the subject and the time, which is strictly more than the counter's memory carried.
func (s *Service) ConfirmWillCall(ctx context.Context, rawToken, typedName string, affirmed bool) (PublicWillCall, error) {
	w, err := s.resolvePickup(ctx, rawToken)
	if err != nil {
		return PublicWillCall{}, err
	}
	name := strings.TrimSpace(typedName)
	if name == "" {
		return PublicWillCall{}, httpx.BadRequestf("type your full name to confirm this pickup")
	}
	if len(name) > maxCollectorName {
		return PublicWillCall{}, httpx.BadRequestf("that name is too long")
	}
	if !affirmed {
		return PublicWillCall{}, httpx.BadRequestf("please confirm you are collecting this order before signing")
	}
	if s.sig == nil {
		return PublicWillCall{}, ErrNoSignatureProvider
	}
	// Item 9.3: a held order's goods do not leave through the self
	// confirmation either, whatever live token predates the reopened balance
	// (an NSF bounce can re-owe an order after its tag was minted).
	if err := s.refuseHeldFromPicking(ctx, w.OrderID); err != nil {
		return PublicWillCall{}, err
	}
	ref, err := s.sig.CaptureTyped(ctx, TypedAttestation{
		Purpose: SignaturePurposePOD, RefType: RefTypeWillCall, RefID: w.ID,
		TypedName: name, Affirmed: true,
	})
	if err != nil {
		return PublicWillCall{}, err
	}
	at := s.now().UTC()
	ok, err := s.repo.ConfirmPickup(ctx, w.ID, ref, name, at)
	if err != nil {
		return PublicWillCall{}, err
	}
	if !ok {
		// Someone confirmed or revoked between the resolve and the write. The square is spent.
		return PublicWillCall{}, ErrTokenGone
	}
	_ = s.repo.LogPickupEvent(ctx, w.ID, "CONFIRM")
	s.record(ctx, audit.SensitiveAccess{
		Action: "WILL_CALL_CONFIRMED", EntityType: "will_call_pickup", EntityID: w.ID,
		MaskedRef: ref, Purpose: SignaturePurposePOD,
	})
	// Same handoff as a delivered stop, and for the same reason: the material has left the yard, so
	// the order is fulfilled and billing should hear about it. Best-effort, logged, never rolled back
	// onto the customer.
	if s.fulfil != nil {
		if err := s.fulfil.MarkFulfilled(ctx, w.OrderID, "will-call:"+w.ID); err != nil {
			s.log.Info("dispatch: will-call order advance declined", "order", w.OrderID, "err", err)
		}
	}
	w.Status = "CONFIRMED"
	w.CollectedBy = name
	return s.publicView(ctx, w)
}
