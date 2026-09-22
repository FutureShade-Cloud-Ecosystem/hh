// sms_consent.go, DIB-482 (M3-G3, half B): the staff control that records SMS consent.
//
// THE GAP
// -------
// Migration 0109 built the consent record (customer_sms_prefs), the resolver that reads it, and the
// rule that an absent row means NO. Everything downstream, the quote follow-up cadence, AR
// collections, dispatch notices, asks that table for permission before it texts anybody.
//
// Nothing in the program could ever WRITE it. PrefsStore has the writers, but the only callers were
// the STOP-reply handler and the tests; the quote side reaches the table through ConsentResolver,
// which is read-only. Migration 0109's own seed insert is guarded by `WHERE c.account_no IN (…)`
// against a customers table that does not exist yet at migrate time, so on any database built the
// normal way (migrate, then seed) it inserts nothing.
//
// The result was a table with zero rows and no door: every SMS the program tried to send was skipped
// for want of consent, correctly, forever, and there was no way for a member of staff to record the
// consent a customer had actually given them over the counter.
//
// WHAT THIS ADDS, AND WHAT IT DELIBERATELY DOES NOT
// -------------------------------------------------
// One manager-gated route that records "this customer agreed to receive text messages, at this
// number, and here is who recorded it and when". It does NOT invent consent, infer it from a phone
// number already on file, or default anything to on. Absence still means no.
//
// The wording shown to staff is NEUTRAL and PARKED (see NOTES-FIX-482 park 1). CASL and the CTIA
// principles both put requirements on the words a customer is asked to agree to, and choosing those
// words is a decision for the client and their counsel, not for this file.
package customer

import (
	"context"
	"errors"
	"strings"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// SMSConsent is one customer's texting permission as the staff surface reports it.
//
// `Consent` is the ONE question every automation asks (messagingprovider.SMSPrefs.Sendable): a
// verified, well-formed number that has not been opted out. It is deliberately a single boolean
// rather than the three underlying flags, a screen that offers staff "verified" and "opted out" as
// separate switches invites the combination that reads as consent and resolves to nobody.
//
// `Recorded` distinguishes "we asked and they said no" from "nobody has ever asked", which are
// different facts about the same customer and lead to different conversations.
type SMSConsent struct {
	CustomerID string `json:"customer_id"`
	Consent    bool   `json:"consent"`
	Recorded   bool   `json:"recorded"`
	// PhoneMasked is the number consent applies to, masked to its last two digits. The cleartext
	// number is never returned by this surface: staff who need to dial have customer_contacts, and a
	// consent screen is not a place to publish a mobile number.
	PhoneMasked string `json:"phone_masked,omitempty"`
	// OptOutReason explains a recorded refusal ('STOP reply', 'staff request'). Empty when consent
	// stands or when nothing is on file.
	OptOutReason string `json:"opt_out_reason,omitempty"`
}

// SMSConsentWrite is one consent decision, as the service hands it to the store.
type SMSConsentWrite struct {
	CustomerID string
	// Grant is the decision itself. False is a REVOCATION, not a deletion, see the store.
	Grant bool
	// PhoneE164 is the number consent applies to. Required to grant when no number is already on
	// file; ignored on a revocation, because you must remember the number you may not text.
	PhoneE164 string
	// ActorID is the staff member recording this, taken from the guard-stamped caller and never from
	// the request body. It is what makes the audit row answer "who".
	ActorID string
}

// smsConsentStore is the OPTIONAL consent seam, discovered by type assertion on the store the Service
// was already built with, the same shape as scopedAccountSearcher above it, and for the same reason:
// accountStore is implemented outside this package (internal/portal composes the Service with its own
// fake), so widening it would break a module this lane does not own.
//
// *Repository satisfies it, so the real deployment always has the capability and needs no line in
// main.go. A store that does not is reported honestly as "not available here" rather than silently
// doing nothing.
type smsConsentStore interface {
	SMSConsent(ctx context.Context, customerID string) (SMSConsent, error)
	// SetSMSConsent writes the consent AND its A-21 audit row together, so a consent change cannot
	// be recorded without the trail that says who changed it.
	SetSMSConsent(ctx context.Context, in SMSConsentWrite) (SMSConsent, error)
}

// ErrConsentUnavailable is returned when the composed store cannot record consent. It is a capability
// that is absent, not an operation that failed, and the surface says so in those terms.
var ErrConsentUnavailable = errors.New("customer: sms consent is not available on this deployment")

// GetSMSConsent reads one customer's texting permission. A customer with no row is NOT an error and
// NOT a default, it is the answer "no consent on file", which is what the whole table means.
func (s *Service) GetSMSConsent(ctx context.Context, customerID string) (SMSConsent, error) {
	customerID = strings.TrimSpace(customerID)
	if customerID == "" {
		return SMSConsent{}, httpx.BadRequestf("customer id is required")
	}
	store, ok := s.store.(smsConsentStore)
	if !ok {
		return SMSConsent{}, ErrConsentUnavailable
	}
	return store.SMSConsent(ctx, customerID)
}

// SetSMSConsent records a consent decision made by a member of staff.
//
// THE ACTOR IS TAKEN FROM THE CONTEXT, NEVER FROM THE BODY. A consent trail whose "who" can be typed
// by the caller records nothing at all.
func (s *Service) SetSMSConsent(ctx context.Context, customerID string, grant bool, phoneE164 string) (SMSConsent, error) {
	customerID = strings.TrimSpace(customerID)
	if customerID == "" {
		return SMSConsent{}, httpx.BadRequestf("customer id is required")
	}
	store, ok := s.store.(smsConsentStore)
	if !ok {
		return SMSConsent{}, ErrConsentUnavailable
	}
	caller, ok := identity.StaffContextFromContext(ctx)
	actor := ""
	if ok {
		actor = strings.TrimSpace(caller.UserID)
	}
	if actor == "" {
		// Refusing here rather than writing an unattributed row. "Somebody said this customer agreed"
		// is not a consent record; it is a rumour with a timestamp. The route is guard-gated, so this
		// is a wiring failure, reported as such (ErrUnauthenticated), never as a 500.
		return SMSConsent{}, ErrUnauthenticated
	}
	return store.SetSMSConsent(ctx, SMSConsentWrite{
		CustomerID: customerID,
		Grant:      grant,
		PhoneE164:  strings.TrimSpace(phoneE164),
		ActorID:    actor,
	})
}
