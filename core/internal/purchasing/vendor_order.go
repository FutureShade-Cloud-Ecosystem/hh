package purchasing

// vendor_order.go, the outbound half of a purchase order: the vendor's ordering desk, the vendor's
// own catalogue vocabulary, the ONE recipient a send is aimed at, and the record that outlives it.
//
// - THE BLAST IS THE DEFECT, ON THE BUY SIDE TOO -
//
// This is billing.ResolveTarget (internal/billing/ar_reminders.go) carried across to purchasing,
// deliberately and almost line for line, because the failure it prevents is the same failure with a
// different victim. A collections notice sent to every address on a customer account reaches the
// customer's foreman. A PURCHASE ORDER sent to every address on a vendor record reaches their
// warehouse, their accounts desk and a rep who left in 2023, three people believe they own it, two
// of them act, and the yard takes two trucks of the same pallet. So ResolveVendorTarget returns ONE
// recipient or an error, and there is no function in this package that can return a list. That is
// the structural version of the requirement: a blast is not something an operator can choose here
// because it is not something this code can express.
//
// THE AMBIGUITY ARM IS THE LOAD-BEARING ONE. Two addresses and no tagged contact is refused with the
// COUNT in the message, not resolved by taking the first. "Pick the first of three" is a guess
// wearing a default's clothes, and the wrong guess here orders forty tonnes of stone from a mailbox
// nobody reads.
//
// - NOTHING IN THIS BUILD DELIVERS EMAIL, AND THIS FILE SAYS SO OUT LOUD -
//
// notify.NewLogNotifier is the wired notifier and its Live() is false. So UnwiredSender, the
// default this module ships on, does NOT report a delivery. It returns Delivered:false with "no
// transport is wired on this server", the row is written first and then marked FAILED, and the
// surface says it in words. That is a deliberate departure from billing.StubSender, which reports
// DELIVERED on purpose because its tracking, cadence and black-hole rules all key off delivery and
// would otherwise be untestable. Nothing here keys off delivery, so nothing here has that excuse:
// a buyer who BELIEVES an order was sent is how a yard waits three weeks for a truck nobody ordered.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// - vocabulary -

const (
	// ChannelEmail is the only channel a purchase order goes out on today. 0011 ships
	// vendors.is_edi, so the second channel is already named in this schema; it is a widening of
	// purchasing_po_sends.channel's CHECK when it lands, not a fact nobody recorded.
	ChannelEmail = "EMAIL"

	// RecipientVendorContact / RecipientFallback are STORED on the send row, never derived. Tagging
	// a contact tomorrow must not rewrite the story of an order that already went out.
	RecipientVendorContact = "VENDOR_CONTACT"
	RecipientFallback      = "FALLBACK"

	SendQueued    = "QUEUED"
	SendDelivered = "DELIVERED"
	SendFailed    = "FAILED"
)

// - the ordering contact -

// VendorContact is one vendor's designated ordering desk. One row per vendor (migration 0197).
type VendorContact struct {
	VendorID  string `json:"vendor_id"`
	Name      string `json:"contact_name"`
	Email     string `json:"email"`
	Phone     string `json:"phone"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// Reachable answers whether this contact can be reached on a channel at all.
func (c VendorContact) Reachable(channel string) bool {
	if channel != ChannelEmail {
		return false
	}
	return strings.TrimSpace(c.Email) != ""
}

// ValidateVendorContact refuses an unreachable or oversized contact in words before the CHECKs could
// 500. A contact with neither an address nor a number is not a contact, it is a name in a box.
func ValidateVendorContact(c VendorContact) (VendorContact, error) {
	c.Name = strings.TrimSpace(c.Name)
	c.Email = strings.TrimSpace(c.Email)
	c.Phone = strings.TrimSpace(c.Phone)
	if len([]rune(c.Name)) > 120 {
		return VendorContact{}, badReq("purchasing: contact_name is at most 120 characters")
	}
	if len(c.Email) > 254 || len(c.Phone) > 32 {
		return VendorContact{}, badReq(
			"purchasing: an ordering email is at most 254 characters and a phone at most 32")
	}
	if c.Email != "" && (!strings.Contains(c.Email, "@") || strings.ContainsAny(c.Email, " \t")) {
		return VendorContact{}, badReq("purchasing: that ordering email address is not an address")
	}
	if c.Email == "" && c.Phone == "" {
		return VendorContact{}, badReq(
			"purchasing: an ordering contact needs an email or a phone, one of the two is what makes it a contact")
	}
	return c, nil
}

// - the one recipient a send is aimed at -

// SendTarget is the ONE recipient a send goes to, and the story of how it was chosen.
type SendTarget struct {
	Recipient      string `json:"recipient"`
	Kind           string `json:"recipient_kind"`
	FallbackReason string `json:"fallback_reason,omitempty"`
}

// ResolveVendorTarget picks the single recipient for one outbound purchase order.
//
// It returns ONE address or an error. billing.ResolveTarget's shape and every one of its arms,
// carried over with the vocabulary changed and the reasoning intact:
//
//	tagged     the vendor's ordering contact, or nil when nobody has tagged one.
//	candidates the addresses the vendor record itself carries, in the record's own order. Empty on
//	           this deployment, because 0011's vendors table has no contact column at all, which is
//	           why the untagged path here usually ends at "tag one".
//	confirmed  the operator explicitly chose to send without a tagged contact. Under CONFIRM this is
//	           REQUIRED: an unconfirmed untagged send is refused, so a fallback is always a
//	           deliberate act with words attached rather than a default that happens quietly.
func ResolveVendorTarget(tagged *VendorContact, candidates []string, p Policy, confirmed bool) (SendTarget, error) {
	if tagged != nil && tagged.Reachable(ChannelEmail) {
		return SendTarget{Recipient: strings.TrimSpace(tagged.Email), Kind: RecipientVendorContact}, nil
	}
	// A PHONE-ONLY CONTACT IS NOT SILENTLY SKIPPED. Somebody tagged a desk; the honest answer is
	// that this build cannot mail a phone number, not that nobody is tagged.
	if tagged != nil && strings.TrimSpace(tagged.Phone) != "" {
		return SendTarget{}, badReq(
			"purchasing: this vendor's ordering contact is a phone number, add an email to it and the order can be sent")
	}
	if p.UntaggedFallback == FallbackRefuse {
		return SendTarget{}, badReq(
			"purchasing: no ordering contact is tagged for this vendor, tag one and the order goes to the right desk")
	}
	clean := make([]string, 0, len(candidates))
	seen := map[string]bool{}
	for _, c := range candidates {
		t := strings.TrimSpace(c)
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		clean = append(clean, t)
	}
	if len(clean) == 0 {
		return SendTarget{}, badReq(
			"purchasing: this vendor has no email on file, tag an ordering contact to send anything at all")
	}
	if !confirmed {
		return SendTarget{}, badReq(
			"purchasing: nobody is tagged for ordering at this vendor, confirm the fallback recipient or tag a contact")
	}
	// AMBIGUITY IS REFUSED, not resolved. Picking "the first one" out of three addresses is a guess
	// wearing a default's clothes, and the wrong guess orders forty tonnes from a mailbox nobody
	// reads. The COUNT is in the message because "which three?" is the operator's next question.
	if len(clean) > 1 {
		return SendTarget{}, badReq(
			"purchasing: this vendor record carries %d email addresses and no ordering contact, tag one so the order has one desk to land on",
			len(clean))
	}
	return SendTarget{
		Recipient:      clean[0],
		Kind:           RecipientFallback,
		FallbackReason: "no ordering contact tagged; operator confirmed the vendor record address",
	}, nil
}

// - the transport seam -

// POSendMessage is what crosses the seam. It carries the CSV rendering of the document with it,
// because a purchase order without its document is an email asking the vendor to guess.
type POSendMessage struct {
	Channel      string
	To           string
	Subject      string
	Body         string
	DocumentName string
	DocumentCSV  []byte
}

// POSendReceipt is what comes back. Ref is opaque, whatever the transport calls its own handle.
type POSendReceipt struct {
	Provider  string
	Ref       string
	Delivered bool
	Detail    string
}

// POSender is the whole transport surface: three methods, no vendor named anywhere.
type POSender interface {
	Name() string
	// Live reports whether this transport reaches a human. The send path reads it so the refusal
	// can be written BEFORE anything is attempted, rather than inferred from a silent success.
	Live() bool
	Send(ctx context.Context, m POSendMessage) (POSendReceipt, error)
}

// UnwiredSender is the default, and the one this program ships on.
//
// IT REPORTS A FAILURE ON PURPOSE. See the file header: nothing in this build delivers email, and a
// transport that reported DELIVERED to keep a test green would be teaching a buyer that an order
// went out. The detail string is the exact words the surface shows.
type UnwiredSender struct{}

// Name identifies the transport in the stored row.
func (UnwiredSender) Name() string { return "unwired" }

// Live is false, and every caller of this seam is written to expect that.
func (UnwiredSender) Live() bool { return false }

// Send delivers nothing and says so.
func (UnwiredSender) Send(_ context.Context, _ POSendMessage) (POSendReceipt, error) {
	return POSendReceipt{
		Provider: "unwired",
		Detail:   "no transport is wired on this server, nothing left the building",
	}, nil
}

// VendorAddressBook is the consumer-declared seam for "what addresses does the vendor RECORD carry".
//
// IT IS DELIBERATELY NOT A WIDENING OF VendorDirectory. That interface's four-field projection is
// what purchasing needs to validate and name a vendor, and dispatch's OrderValueReader makes exactly
// this argument for exactly this reason: a different question with a different failure mode gets its
// own interface. A directory that cannot name a vendor means the PO cannot be created; an address
// book that cannot answer means the untagged fallback has nothing to fall back TO, which is a
// refusal in words and not an error.
//
// Nil is honest and is the shipped state: 0011's vendors table has no contact column, so this seam's
// adapter reads the two free-form JSONB blobs a real import populates and returns nothing here.
type VendorAddressBook interface {
	VendorOrderCandidates(ctx context.Context, vendorID string) ([]string, error)
}

// candidatesFor reads the vendor record's own addresses, DEGRADING DOWNWARD: an unwired seam or a
// failing read returns nothing, which lands the caller on "tag an ordering contact" rather than on
// a 500. The one thing it must never do is return a partial list, because a partial list is how the
// ambiguity refusal silently becomes a guess.
func (s *Service) candidatesFor(ctx context.Context, vendorID string) []string {
	if s.addresses == nil {
		return nil
	}
	got, err := s.addresses.VendorOrderCandidates(ctx, vendorID)
	if err != nil {
		s.log.Warn("purchasing: vendor address book unavailable, the untagged fallback has nothing to offer",
			"vendor_id", vendorID, "err", err)
		return nil
	}
	return got
}

// - the send record -

// POSend is one row of purchasing_po_sends: what we decided, and what came back.
//
// NO MONEY ON THIS SHAPE, deliberately. A send record answers "who did this order go to, and did it
// leave", a question a receiving clerk may ask. The order's value is on the order, behind the gate.
type POSend struct {
	ID             string    `json:"id"`
	POID           string    `json:"po_id"`
	Channel        string    `json:"channel"`
	Recipient      string    `json:"recipient"`
	RecipientKind  string    `json:"recipient_kind"`
	FallbackReason string    `json:"fallback_reason,omitempty"`
	Status         string    `json:"status"`
	Provider       string    `json:"provider"`
	ProviderRef    string    `json:"provider_ref,omitempty"`
	Detail         string    `json:"detail,omitempty"`
	DocumentName   string    `json:"document_name,omitempty"`
	DocumentBytes  int       `json:"document_bytes"`
	IdempotencyKey string    `json:"-"`
	SentAt         time.Time `json:"sent_at"`
}

// SendPORequest is the POST /purchasing/pos/{id}/send body.
type SendPORequest struct {
	// ConfirmFallback is the operator's explicit choice to send to an untagged address. It is
	// required under the CONFIRM posture and is recorded on the row forever.
	ConfirmFallback bool   `json:"confirm_fallback"`
	IdempotencyKey  string `json:"idempotency_key"`
}

// sendStore is the persistence seam the send path uses, discovered by type assertion off
// *Repository, the module's established shape (incomingStore, onOrderStore, policyStore), and here
// it earns its keep twice over: the ambiguity refusal, the write-before-transport ordering and the
// FAILED marking are all provable without a database.
// orderStore is the item-5 read seam onto the ORDER itself: the header and its lines.
//
// It exists so the whole outbound surface, the recipient rule, the write-before-transport ordering,
// the document projection and the cost gate over it, can be proved without a database. Those are
// four decisions taken in Go, and the module's own established answer to "where should a Go decision
// be proved" is the same one incomingStore and onOrderStore already give: not inside Postgres.
//
// *Repository satisfies it with two one-line delegates over the reads it already has, so there is no
// second query and no second definition of what a purchase order's lines are.
type orderStore interface {
	POHeader(ctx context.Context, poID string) (PO, error)
	POLinesFor(ctx context.Context, poID string) ([]POLine, error)
}

func (s *Service) orders() (orderStore, error) {
	if s.orderSeam != nil {
		return s.orderSeam, nil
	}
	if s.repo == nil || s.repo.pool == nil {
		return nil, errNoDB
	}
	return s.repo, nil
}

type sendStore interface {
	VendorContact(ctx context.Context, vendorID string) (VendorContact, bool, error)
	InsertPOSend(ctx context.Context, row POSend, actorID string) (POSend, error)
	MarkPOSendResult(ctx context.Context, id, status, provider, ref, detail string) error
	FindPOSendByKey(ctx context.Context, key, poID string) (POSend, bool, error)
	ListPOSends(ctx context.Context, poID string) ([]POSend, error)
}

func (s *Service) sends() (sendStore, error) {
	if s.sendSeam != nil {
		return s.sendSeam, nil
	}
	if s.repo == nil || s.repo.pool == nil {
		return nil, errNoDB
	}
	return s.repo, nil
}

// SendPO sends one purchase order to the vendor's ordering desk and writes the record that outlives
// the attempt.
//
// ORDER MATTERS AND IS NOT AN ACCIDENT (ar_reminders.go:418-423): the row is inserted BEFORE the
// transport is called. An order that left the building with no row behind it is a commitment this
// business has no record of, which is strictly worse than a row for a send that failed, the row can
// say FAILED, and on this deployment it always does.
func (s *Service) SendPO(ctx context.Context, poID string, in SendPORequest) (POSend, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return POSend{}, err
	}
	store, err := s.sends()
	if err != nil {
		return POSend{}, err
	}
	orders, err := s.orders()
	if err != nil {
		return POSend{}, err
	}
	po, err := orders.POHeader(ctx, poID)
	if err != nil {
		return POSend{}, err
	}
	if !caller.CanAccessBranch(po.BranchID) {
		return POSend{}, errForbidden
	}
	// THE REPLAY ANSWER COMES AFTER THE FENCE, NEVER BEFORE IT (HH-181). The key index is global, so a
	// key is only a replay for the order that minted it: for any other order it is a collision the
	// insert below refuses, and for a caller the branch fence just refused it is not an answer at all,
	// because it would be a read of another branch's send history through the one unguarded door.
	if key := strings.TrimSpace(in.IdempotencyKey); key != "" {
		// An identical retry reuses its key and its row: a double-clicked "Send order" is one order.
		if prior, ok, err := store.FindPOSendByKey(ctx, key, po.ID); err == nil && ok {
			return prior, nil
		}
	}
	// A DRAFT IS NOT SENDABLE. The lifecycle already says a placed order is one whose line set is
	// frozen; mailing a draft would hand a vendor a document the buyer can still edit underneath
	// them, which is the three-way match defeated before it begins.
	if po.Status == StatusDraft {
		return POSend{}, conflict("purchasing: place the order first, a draft is still being edited")
	}
	if po.Status == StatusCancelled {
		return POSend{}, conflict("purchasing: this order was cancelled, nothing to send")
	}

	tagged, found, err := store.VendorContact(ctx, po.VendorID)
	if err != nil {
		return POSend{}, err
	}
	var contact *VendorContact
	if found {
		contact = &tagged
	}
	target, err := ResolveVendorTarget(contact, s.candidatesFor(ctx, po.VendorID),
		s.PolicyFor(ctx, po.BranchID), in.ConfirmFallback)
	if err != nil {
		return POSend{}, err
	}

	// THE DOCUMENT IS BUILT FOR THE VENDOR, NOT FOR THE CALLER. It carries cost unconditionally,
	// because a purchase order with no prices is not a purchase order, and the route that reaches
	// this path is RequireManager, so the person choosing to send it is a person the gate already
	// allows. The gate governs who may READ the order on a screen; it does not redact the contract.
	doc, err := s.buildPODocument(ctx, po, costLens{allow: true})
	if err != nil {
		return POSend{}, err
	}
	csv, err := EncodePODocumentCSV(doc)
	if err != nil {
		return POSend{}, err
	}

	row, err := store.InsertPOSend(ctx, POSend{
		POID: po.ID, Channel: ChannelEmail,
		Recipient: target.Recipient, RecipientKind: target.Kind, FallbackReason: target.FallbackReason,
		Status: SendQueued, Provider: s.sender().Name(),
		DocumentName: doc.FileName(), DocumentBytes: len(csv),
		IdempotencyKey: strings.TrimSpace(in.IdempotencyKey),
	}, caller.UserID)
	if err != nil {
		return POSend{}, err
	}

	receipt, sendErr := s.sender().Send(ctx, POSendMessage{
		Channel: ChannelEmail, To: target.Recipient,
		Subject: poSendSubject(doc), Body: poSendBody(doc),
		DocumentName: doc.FileName(), DocumentCSV: csv,
	})
	status, detail := SendDelivered, receipt.Detail
	if sendErr != nil || !receipt.Delivered {
		status = SendFailed
		if sendErr != nil {
			detail = sendErr.Error()
		}
	}
	provider := receipt.Provider
	if provider == "" {
		provider = s.sender().Name()
	}
	if err := store.MarkPOSendResult(ctx, row.ID, status, provider, receipt.Ref, detail); err != nil {
		return row, err
	}
	row.Status, row.Provider, row.ProviderRef, row.Detail = status, provider, receipt.Ref, detail
	s.record(ctx, "purchasing.po.sent", po.ID, map[string]any{
		"recipient_kind": target.Kind, "status": status, "provider": provider,
	})
	return row, nil
}

// sender resolves the transport, defaulting to the honest refusal rather than to nothing.
func (s *Service) sender() POSender {
	if s.poSender == nil {
		return UnwiredSender{}
	}
	return s.poSender
}

// ListPOSends is the send history for one order, "did this go out, to whom, and did it land".
func (s *Service) ListPOSends(ctx context.Context, poID string) ([]POSend, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	store, err := s.sends()
	if err != nil {
		return nil, err
	}
	orders, err := s.orders()
	if err != nil {
		return nil, err
	}
	po, err := orders.POHeader(ctx, poID)
	if err != nil {
		return nil, err
	}
	if !caller.CanAccessBranch(po.BranchID) {
		return nil, errForbidden
	}
	return store.ListPOSends(ctx, poID)
}

// poSendSubject and poSendBody are the message text. They carry ZERO internal vocabulary, no
// status code, no branch id, no ticket number, because a vendor's order desk reads this.
func poSendSubject(d PODocument) string {
	return fmt.Sprintf("Purchase order %s, %s", d.PONo, d.BuyerName)
}

func poSendBody(d PODocument) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Purchase order %s from %s.\n\n", d.PONo, d.BuyerName)
	fmt.Fprintf(&b, "%d line", len(d.Lines))
	if len(d.Lines) != 1 {
		b.WriteString("s")
	}
	if d.ExpectedOn != "" {
		fmt.Fprintf(&b, ", required by %s", d.ExpectedOn)
	}
	b.WriteString(".\n")
	b.WriteString("The order is attached as a spreadsheet. Please quote ")
	b.WriteString(d.PONo)
	b.WriteString(" on your acknowledgement, packing slip and invoice.\n")
	return b.String()
}

// - the vendor's catalogue vocabulary -

// VendorSKU is one (vendor, product) mapping. Every field is optional and every absence degrades to
// our own catalogue in the open, see migration 0197 and PODocument's line mapping.
type VendorSKU struct {
	VendorID  string `json:"vendor_id"`
	ProductID string `json:"product_id"`
	// SKU/Name are OUR catalogue's, carried so the editing surface can name the row without a
	// second read. They are read-only here; purchasing owns no product table.
	SKU         string `json:"sku,omitempty"`
	ProductName string `json:"product_name,omitempty"`

	VendorSKU string `json:"vendor_sku"`
	// VendorUOM + QtyPerVendorUOM are all-or-nothing (the 0197 CHECK): a unit with no factor cannot
	// be counted and a factor with no unit names nothing.
	VendorUOM       string  `json:"vendor_uom,omitempty"`
	QtyPerVendorUOM float64 `json:"qty_per_vendor_uom,omitempty"`
	UpdatedAt       string  `json:"updated_at,omitempty"`
}

// ValidateVendorSKU refuses a half-filled mapping in words before the CHECK could 500.
func ValidateVendorSKU(m VendorSKU) (VendorSKU, error) {
	m.VendorSKU = strings.TrimSpace(m.VendorSKU)
	m.VendorUOM = strings.ToUpper(strings.TrimSpace(m.VendorUOM))
	if m.ProductID == "" {
		return VendorSKU{}, badReq("purchasing: a mapping needs a product")
	}
	if len([]rune(m.VendorSKU)) > 64 {
		return VendorSKU{}, badReq("purchasing: a vendor code is at most 64 characters")
	}
	if (m.VendorUOM == "") != (m.QtyPerVendorUOM == 0) {
		return VendorSKU{}, badReq(
			"purchasing: a vendor unit needs the number of our units it holds, one without the other cannot be counted")
	}
	if m.QtyPerVendorUOM < 0 {
		return VendorSKU{}, badReq("purchasing: a vendor unit cannot hold a negative quantity")
	}
	if m.VendorSKU == "" && m.VendorUOM == "" {
		return VendorSKU{}, badReq(
			"purchasing: an empty mapping is the same as no mapping, remove it instead")
	}
	return m, nil
}

// vendorSKUStore is the persistence seam for the mapping, same shape and same reason as the others.
type vendorSKUStore interface {
	VendorSKUs(ctx context.Context, vendorID string) ([]VendorSKU, error)
	UpsertVendorSKU(ctx context.Context, m VendorSKU) error
	DeleteVendorSKU(ctx context.Context, vendorID, productID string) error
	UpsertVendorContact(ctx context.Context, c VendorContact, actorID string) error
	DeleteVendorContact(ctx context.Context, vendorID string) error
}

func (s *Service) catalogue() (vendorSKUStore, error) {
	if s.catalogueSeam != nil {
		return s.catalogueSeam, nil
	}
	if s.repo == nil || s.repo.pool == nil {
		return nil, errNoDB
	}
	return s.repo, nil
}

// GetVendorContact reads the tagged ordering desk. NOT FOUND IS NOT AN ERROR: "nobody is tagged" is
// the ordinary starting state of every vendor in this deployment and the surface has to draw it.
func (s *Service) GetVendorContact(ctx context.Context, vendorID string) (VendorContact, bool, error) {
	if _, err := s.caller(ctx); err != nil {
		return VendorContact{}, false, err
	}
	store, err := s.sends()
	if err != nil {
		return VendorContact{}, false, err
	}
	return store.VendorContact(ctx, vendorID)
}

// SetVendorContact tags (or retags) the vendor's ordering desk.
func (s *Service) SetVendorContact(ctx context.Context, in VendorContact) (VendorContact, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return VendorContact{}, err
	}
	if in.VendorID == "" {
		return VendorContact{}, badReq("purchasing: vendor_id is required")
	}
	if s.vendor == nil {
		return VendorContact{}, ErrVendorSeamUnwired
	}
	if _, err := s.vendor.Vendor(ctx, in.VendorID); err != nil {
		return VendorContact{}, err
	}
	clean, err := ValidateVendorContact(in)
	if err != nil {
		return VendorContact{}, err
	}
	clean.VendorID = in.VendorID
	store, err := s.catalogue()
	if err != nil {
		return VendorContact{}, err
	}
	if err := store.UpsertVendorContact(ctx, clean, caller.UserID); err != nil {
		return VendorContact{}, err
	}
	s.record(ctx, "purchasing.vendor.contact.tagged", in.VendorID, nil)
	got, _, err := s.GetVendorContact(ctx, in.VendorID)
	if err != nil {
		return clean, nil
	}
	return got, nil
}

// DeleteVendorContact untags the ordering desk. The sends that already went out keep their stored
// recipient_kind, that is the whole reason the column is stored.
func (s *Service) DeleteVendorContact(ctx context.Context, vendorID string) error {
	if _, err := s.caller(ctx); err != nil {
		return err
	}
	store, err := s.catalogue()
	if err != nil {
		return err
	}
	if err := store.DeleteVendorContact(ctx, vendorID); err != nil {
		return err
	}
	s.record(ctx, "purchasing.vendor.contact.untagged", vendorID, nil)
	return nil
}

// ListVendorSKUs reads one vendor's mappings.
func (s *Service) ListVendorSKUs(ctx context.Context, vendorID string) ([]VendorSKU, error) {
	if _, err := s.caller(ctx); err != nil {
		return nil, err
	}
	store, err := s.catalogue()
	if err != nil {
		return nil, err
	}
	return store.VendorSKUs(ctx, vendorID)
}

// SetVendorSKU records the vendor's own code and selling unit for one of our products.
func (s *Service) SetVendorSKU(ctx context.Context, in VendorSKU) (VendorSKU, error) {
	if _, err := s.caller(ctx); err != nil {
		return VendorSKU{}, err
	}
	if in.VendorID == "" {
		return VendorSKU{}, badReq("purchasing: vendor_id is required")
	}
	clean, err := ValidateVendorSKU(in)
	if err != nil {
		return VendorSKU{}, err
	}
	clean.VendorID = in.VendorID
	store, err := s.catalogue()
	if err != nil {
		return VendorSKU{}, err
	}
	if err := store.UpsertVendorSKU(ctx, clean); err != nil {
		return VendorSKU{}, err
	}
	s.record(ctx, "purchasing.vendor.sku.mapped", in.VendorID, map[string]any{"product_id": clean.ProductID})
	return clean, nil
}

// DeleteVendorSKU removes one mapping. The document then degrades to our SKU and our base unit and
// SAYS SO on the line, which is the whole posture: an absent mapping is visible, never papered over.
func (s *Service) DeleteVendorSKU(ctx context.Context, vendorID, productID string) error {
	if _, err := s.caller(ctx); err != nil {
		return err
	}
	store, err := s.catalogue()
	if err != nil {
		return err
	}
	return store.DeleteVendorSKU(ctx, vendorID, productID)
}

// - the echoed reference -

// ResolveEchoedRef finds the purchase order a vendor quoted back at us.
//
// THE PROBLEM IT SOLVES IS A HUMAN ONE. The number on the document is `#PO-TRT-4`; what comes back
// on the vendor's acknowledgement is "PO TRT 4", "po-trt-4", "POTRT4" or the same thing with the
// hash dropped by a mail client. Refusing all four because they are not byte-identical would send
// an AP clerk back to a list to find by eye, which is where mismatched invoices come from.
//
// The normalisation lives in ONE place, the SQL expression in vendorRefMatchSQL, and is applied to
// both sides of the comparison there, so the stored number and the echoed string are folded by the
// same rule and cannot drift apart. Doing it in Go would mean two definitions of "the same number".
func (s *Service) ResolveEchoedRef(ctx context.Context, ref string) (PO, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return PO{}, err
	}
	if strings.TrimSpace(ref) == "" {
		return PO{}, badReq("purchasing: type the order number the vendor quoted")
	}
	if s.repo == nil || s.repo.pool == nil {
		return PO{}, errNoDB
	}
	po, err := s.repo.POByVendorRef(ctx, ref)
	if err != nil {
		return PO{}, err
	}
	if !caller.CanAccessBranch(po.BranchID) {
		return PO{}, errForbidden
	}
	// THE TOTALS ARE COMPUTED, NOT LEFT AT ZERO. The header read carries no line aggregate, and a
	// stamped `subtotal_cents: 0` would be worse than an absent one: absence is a caller who may not
	// see cost, and zero is a purchase order that cost nothing. GetPO does the same sum for the same
	// reason, and this path must not answer differently about the same order.
	if orders, err := s.orders(); err == nil {
		if lines, err := orders.POLinesFor(ctx, po.ID); err == nil {
			po.LineCount = len(lines)
			po.SubtotalCents = 0
			for _, l := range lines {
				po.SubtotalCents += l.LineTotalCents
			}
			po.SubtotalCadCents = toCAD(po.SubtotalCents, po.FXRate)
		}
	}
	if names := s.vendorNames(ctx); names != nil {
		po.VendorName = names[po.VendorID]
	}
	s.costLensFor(ctx, po.BranchID).stampPO(&po)
	return po, nil
}

// - policy administration -

// GetPolicy reads the effective policy for a branch. It never errors: see PolicyFor.
func (s *Service) GetPolicy(ctx context.Context, branchID string) (Policy, error) {
	if _, err := s.caller(ctx); err != nil {
		return Policy{}, err
	}
	return s.PolicyFor(ctx, branchID), nil
}

// SetPolicyRequest is the PUT /purchasing/policy body. An empty branch_id sets the DEALER DEFAULT.
type SetPolicyRequest struct {
	BranchID         string `json:"branch_id"`
	CostVisibility   string `json:"cost_visibility"`
	UntaggedFallback string `json:"untagged_fallback"`
}

// SetPolicy writes the dealer default or one branch's override, refusing an unknown word in words
// before the CHECK could 500.
func (s *Service) SetPolicy(ctx context.Context, in SetPolicyRequest) (Policy, error) {
	if _, err := s.caller(ctx); err != nil {
		return Policy{}, err
	}
	vis, err := settingsVisibility(in.CostVisibility)
	if err != nil {
		return Policy{}, err
	}
	fb := strings.ToUpper(strings.TrimSpace(in.UntaggedFallback))
	switch fb {
	case "":
		fb = FallbackConfirm
	case FallbackConfirm, FallbackRefuse:
	default:
		return Policy{}, badReq("purchasing: untagged_fallback must be CONFIRM or REFUSE")
	}
	if s.repo == nil || s.repo.pool == nil {
		return Policy{}, errNoDB
	}
	if err := s.repo.UpsertPolicy(ctx, in.BranchID, vis, fb); err != nil {
		return Policy{}, err
	}
	s.record(ctx, "purchasing.policy.set", in.BranchID, map[string]any{"cost_visibility": vis})
	return s.PolicyFor(ctx, in.BranchID), nil
}

// settingsVisibility normalises the cost-visibility word through the SHARED kit validator, then
// re-clothes its refusal in this module's own error taxonomy. The kit is the single definition of
// the vocabulary (internal/settings.ValidateVisibility) so quotes, the counter and purchasing cannot
// drift on the words; the taxonomy is this module's so the handler has one error shape to map.
func settingsVisibility(v string) (string, error) {
	out, err := settings.ValidateVisibility("purchasing", strings.ToUpper(strings.TrimSpace(v)))
	if err != nil {
		return "", badReq("purchasing: cost_visibility must be MANAGER_UP, ALL_STAFF or HIDDEN")
	}
	return out, nil
}
