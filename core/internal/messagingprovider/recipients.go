package messagingprovider

import (
	"context"
	"errors"
	"strings"
	"sync"
)

// recipients.go, WHO a notice goes to.
//
// This module deliberately does not know. A customer's phone number lives behind the customer/billing
// modules, and reaching into them from here would couple out-of-band delivery to the finance schema.
// So the lookup is a narrow seam satisfied at the composition root, exactly like tasks' OrderReader.
//
// THE DEFAULT RESOLVES NOBODY, AND THAT IS THE CORRECT DEFAULT. FixtureRecipients starts empty and
// returns ErrNoRecipient, so with nothing wired this program queues notices and sends none, instead
// of inventing a number and texting a stranger. The consumer treats ErrNoRecipient as a SKIP (the row
// is published with a log line, not retried into the dead-letter pile), so an unwired resolver is
// quiet and observable rather than a growing heap of failures.

// ErrNoRecipient, no phone number is known for this customer/order/invoice. Not a failure of the
// send; a statement that there is nobody to send to.
var ErrNoRecipient = errors.New("messagingprovider: no recipient known")

// Recipient is who a message goes to. Phone must be E.164 (+1613…), the format every carrier and
// provider agrees on. Name is used only to open the message.
type Recipient struct {
	Name  string
	Phone string
}

// Valid reports whether this recipient can actually be texted.
func (r Recipient) Valid() bool { return looksE164(r.Phone) }

// RecipientResolver maps a business reference to the person who should receive the text.
//
// SEAM CONTRACT (stable; the composition root wires an implementation):
//   - Either method returns ErrNoRecipient when no number is on file. That is NOT an error condition
//     , it is the normal state for a customer who never gave us a mobile number, and the consumer
//     treats it as a skip.
//   - A returned Recipient MUST carry an E.164 phone; the consumer rejects anything else rather than
//     handing a malformed number to a provider that will bill us for the attempt.
type RecipientResolver interface {
	ARRecipient(ctx context.Context, invoiceID, customerID string) (Recipient, error)
	DispatchRecipient(ctx context.Context, orderID string) (Recipient, error)
}

// FixtureRecipients is the default RecipientResolver: an explicit, in-memory map that starts empty.
// It is the honest stand-in for a customer-contact lookup, it resolves exactly who it was told
// about, and nobody else.
type FixtureRecipients struct {
	mu       sync.RWMutex
	byOrder  map[string]Recipient
	byInvoic map[string]Recipient
}

// NewFixtureRecipients builds the empty default.
func NewFixtureRecipients() *FixtureRecipients {
	return &FixtureRecipients{byOrder: map[string]Recipient{}, byInvoic: map[string]Recipient{}}
}

// SeedOrder registers the recipient for one order (dispatch notices).
func (f *FixtureRecipients) SeedOrder(orderID string, r Recipient) *FixtureRecipients {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byOrder[orderID] = r
	return f
}

// SeedInvoice registers the recipient for one invoice (AR notices).
func (f *FixtureRecipients) SeedInvoice(invoiceID string, r Recipient) *FixtureRecipients {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byInvoic[invoiceID] = r
	return f
}

func (f *FixtureRecipients) ARRecipient(_ context.Context, invoiceID, _ string) (Recipient, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if r, ok := f.byInvoic[invoiceID]; ok {
		return r, nil
	}
	return Recipient{}, ErrNoRecipient
}

func (f *FixtureRecipients) DispatchRecipient(_ context.Context, orderID string) (Recipient, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if r, ok := f.byOrder[orderID]; ok {
		return r, nil
	}
	return Recipient{}, ErrNoRecipient
}

// looksE164 is a shape check, not a validation: + followed by 8-15 digits, first digit non-zero. It
// exists to catch "6135551234" and "(613) 555-1234" before a provider charges us for rejecting them -
// whether the number is reachable is the carrier's business, not ours.
func looksE164(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 9 || len(s) > 16 || s[0] != '+' {
		return false
	}
	if s[1] == '0' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
