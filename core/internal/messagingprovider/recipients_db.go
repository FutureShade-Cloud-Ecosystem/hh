package messagingprovider

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// recipients_db.go, the REAL RecipientResolver (Wave 3, Swarm D). It replaces the Wave 2 default that
// resolved nobody, and it resolves nobody just as readily: the point of this file is that a number
// only comes out of it when a customer has agreed to receive texts at it.
//
// -------------------------------------------------------------------------------------------------
// TWO SOURCES, AND THEY ARE NOT INTERCHANGEABLE.
//
//	customer_sms_prefs (0109, THIS package's table) supplies the NUMBER and the PERMISSION.
//	ContactSource (a seam, satisfied at the composition root) supplies the NAME and the mapping from a
//	business reference (an invoice, an order) to the customer it belongs to.
//
// The number never comes from the contact source, even though customer_contacts has a phone column.
// That column is free text, is a landline as often as a mobile, and nobody agreed to be texted at it.
// Reading it here would turn "we have their number" into "we may text them", which is the exact
// mistake this bundle exists to not make.
//
// WHY ContactSource IS DECLARED HERE AND IMPLEMENTED IN main.go.
// This package must not import internal/customer. Consumer-declared interfaces are how this codebase
// crosses a module boundary (tasks.OrderReader, till.CardCharger, quote.CreditGate, all the same
// shape): the CONSUMER states the narrow thing it needs, the composition root adapts the provider to
// it, and neither module knows the other exists. A nil ContactSource is legal and degrades gracefully
//, see the field comment.
// -------------------------------------------------------------------------------------------------

// Contact is the non-permission half of a recipient: who they are, and which customer they belong to.
// It deliberately carries NO phone number. The seam cannot supply one even by accident, which is what
// keeps "a number is on file" from ever becoming "we may text it".
type Contact struct {
	CustomerID string
	Name       string
}

// ContactSource is the consumer-declared seam over customer contact data. The composition root
// satisfies it by adapting internal/customer (and, for the reference mappings, the order/invoice
// readers); this package neither imports nor names those modules.
//
// SEAM CONTRACT:
//   - A method that cannot find its subject returns ErrNoRecipient, not an error. "This invoice
//     belongs to a customer who has been deleted" is a normal, uninteresting state.
//   - No method may return a phone number. See the Contact doc.
type ContactSource interface {
	// ContactForCustomer names the person to greet for a customer account.
	ContactForCustomer(ctx context.Context, customerID string) (Contact, error)
	// CustomerForInvoice maps an invoice to its customer.
	CustomerForInvoice(ctx context.Context, invoiceID string) (string, error)
	// CustomerForOrder maps an order to its customer.
	CustomerForOrder(ctx context.Context, orderID string) (string, error)
}

// ConsentResolver is the shipping RecipientResolver: consent from 0109, identity from the seam.
type ConsentResolver struct {
	prefs *PrefsStore
	// src may be nil. A nil source means this resolver can still answer any lookup that already
	// carries a customer id (AR notices, quote sends, follow-ups), it simply greets the recipient by
	// "Hi" instead of by name, and cannot answer an order-only lookup. That is a degraded resolver,
	// not a broken one, and it is what an incompletely wired deployment gets.
	src ContactSource
	log *slog.Logger
	// now is injected so the quiet-hours decision is testable to the minute. Never nil after New.
	now Clock
}

// Compile-time proof that the real resolver satisfies the Wave 2 seam unchanged.
var _ RecipientResolver = (*ConsentResolver)(nil)

// NewConsentResolver builds the resolver over the prefs store.
func NewConsentResolver(prefs *PrefsStore, log *slog.Logger) *ConsentResolver {
	return &ConsentResolver{prefs: prefs, log: log, now: time.Now}
}

// WithContactSource wires the composition root's adapter (nil is legal, see the field comment).
func (r *ConsentResolver) WithContactSource(s ContactSource) *ConsentResolver {
	r.src = s
	return r
}

// WithClock injects the clock used for quiet-hours decisions. Tests only.
func (r *ConsentResolver) WithClock(c Clock) *ConsentResolver {
	if c != nil {
		r.now = c
	}
	return r
}

// ARRecipient resolves the recipient of an accounts-receivable notice.
//
// customerID is used when the producer already knew it (the AR sweep always does); otherwise the
// invoice is mapped through the seam. Either way the answer comes from consent, not from the mapping.
func (r *ConsentResolver) ARRecipient(ctx context.Context, invoiceID, customerID string) (Recipient, error) {
	if strings.TrimSpace(customerID) == "" {
		if r.src == nil {
			return Recipient{}, ErrNoRecipient
		}
		id, err := r.src.CustomerForInvoice(ctx, invoiceID)
		if err != nil {
			return Recipient{}, err
		}
		customerID = id
	}
	return r.CustomerRecipient(ctx, customerID)
}

// DispatchRecipient resolves the recipient of a fulfilment notice from an order reference.
func (r *ConsentResolver) DispatchRecipient(ctx context.Context, orderID string) (Recipient, error) {
	if r.src == nil {
		return Recipient{}, ErrNoRecipient
	}
	customerID, err := r.src.CustomerForOrder(ctx, orderID)
	if err != nil {
		return Recipient{}, err
	}
	return r.CustomerRecipient(ctx, customerID)
}

// CustomerRecipient is the one place consent is evaluated. Every other method funnels through it.
//
// It returns ErrNoRecipient, never a bare zero Recipient, never a partially-populated one, for every
// reason we may not text somebody: no consent row, an unverified number, an opted-out customer, or a
// number that is not E.164. The CALLER cannot tell those apart, deliberately: a producer that could
// distinguish "opted out" from "no row" would eventually branch on it.
func (r *ConsentResolver) CustomerRecipient(ctx context.Context, customerID string) (Recipient, error) {
	prefs, found, err := r.prefs.Get(ctx, customerID)
	if err != nil {
		return Recipient{}, err
	}
	if !found || !prefs.Sendable() {
		r.declined(customerID, found, prefs)
		return Recipient{}, ErrNoRecipient
	}

	name := ""
	if r.src != nil {
		if c, cerr := r.src.ContactForCustomer(ctx, customerID); cerr == nil {
			name = c.Name
		}
		// A failed name lookup is not a reason to withhold a message the customer consented to. We
		// greet them with "Hi" and send it.
	}
	return Recipient{Name: name, Phone: prefs.PhoneE164}, nil
}

// MaySendAt reports whether a customer may be texted at instant t, and, when not, the earliest
// instant they may be. It is what a SCHEDULED producer stamps not_before from.
//
// A customer with no consent gets (false, zero time): there is no later instant at which sending
// becomes allowed, and a caller that treats the zero time as "send now" would be wrong in the one
// direction that matters. Callers must check the bool.
func (r *ConsentResolver) MaySendAt(ctx context.Context, customerID string, t time.Time) (bool, time.Time, error) {
	prefs, found, err := r.prefs.Get(ctx, customerID)
	if err != nil {
		return false, time.Time{}, err
	}
	if !found || !prefs.Sendable() {
		return false, time.Time{}, nil
	}
	return true, prefs.QuietHours().NextOpen(t), nil
}

// Now reads the injected clock (producers in this package schedule from it).
func (r *ConsentResolver) Now() time.Time { return r.now() }

// declined logs a refusal WITHOUT the number and without an alarming level. Not texting somebody is
// the normal case, not an incident.
func (r *ConsentResolver) declined(customerID string, found bool, prefs SMSPrefs) {
	if r.log == nil {
		return
	}
	why := "no SMS consent on file"
	switch {
	case found && prefs.OptedOut:
		why = "customer has opted out"
	case found && !prefs.Verified:
		why = "number is not verified for SMS"
	case found && !looksE164(prefs.PhoneE164):
		why = "stored number is not E.164"
	}
	r.log.Info("sms: no recipient (nothing will be sent)", "customer_id", customerID, "why", why)
}

// FixtureConsent is an in-memory ContactSource for tests and for a scaffold deployment: it maps
// exactly what it was told and nothing else. It exists so this package's tests never need the customer
// module, which is the point of the seam.
type FixtureConsent struct {
	names      map[string]string
	byInvoice  map[string]string
	byOrderRef map[string]string
}

// NewFixtureConsent builds the empty fixture source.
func NewFixtureConsent() *FixtureConsent {
	return &FixtureConsent{names: map[string]string{}, byInvoice: map[string]string{}, byOrderRef: map[string]string{}}
}

// SeedCustomer registers a customer's display name.
func (f *FixtureConsent) SeedCustomer(customerID, name string) *FixtureConsent {
	f.names[customerID] = name
	return f
}

// SeedInvoice maps an invoice to its customer.
func (f *FixtureConsent) SeedInvoice(invoiceID, customerID string) *FixtureConsent {
	f.byInvoice[invoiceID] = customerID
	return f
}

// SeedOrder maps an order to its customer.
func (f *FixtureConsent) SeedOrder(orderID, customerID string) *FixtureConsent {
	f.byOrderRef[orderID] = customerID
	return f
}

func (f *FixtureConsent) ContactForCustomer(_ context.Context, customerID string) (Contact, error) {
	n, ok := f.names[customerID]
	if !ok {
		return Contact{}, ErrNoRecipient
	}
	return Contact{CustomerID: customerID, Name: n}, nil
}

func (f *FixtureConsent) CustomerForInvoice(_ context.Context, invoiceID string) (string, error) {
	id, ok := f.byInvoice[invoiceID]
	if !ok {
		return "", fmt.Errorf("%w (invoice %s)", ErrNoRecipient, shortID(invoiceID))
	}
	return id, nil
}

func (f *FixtureConsent) CustomerForOrder(_ context.Context, orderID string) (string, error) {
	id, ok := f.byOrderRef[orderID]
	if !ok {
		return "", fmt.Errorf("%w (order %s)", ErrNoRecipient, shortID(orderID))
	}
	return id, nil
}
