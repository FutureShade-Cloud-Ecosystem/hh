package customer

import (
	"context"
	"fmt"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messagingprovider"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// sms_consent_repository.go, DIB-482 (M3-G3, half B): where the consent decision lands.
//
// NO SQL AGAINST customer_sms_prefs LIVES HERE, AND NONE MAY.
// That table is owned by internal/messagingprovider (migration 0109), and the rules about it are not
// incidental: a verified row must carry an E.164 number, verifying stamps verified_at, opting back in
// clears the opt-out reason as well as the flag, and "may we text them" is ONE expression
// (SMSPrefs.Sendable) so a future rule changes one place instead of four. Reimplementing any of that
// here would be a second, quietly divergent definition of consent. This file calls the owner's store
// over this repository's own pool and does nothing the owner would not do.
//
// The AUDIT ROW IS WRITTEN HERE, beside the consent write, deliberately. A consent record whose
// provenance is optional is not a consent record. Recording it through audit's Recorder (never by
// SELECTing or INSERTing audit_log) keeps the masked-only boundary intact, this is the seam the
// zero-trust posture requires of every sibling.

// prefs builds the owner's consent store over this repository's pool. A nil pool is passed through:
// PrefsStore's own nil-pool posture is "every read reports no consent, every write errors", which is
// the safe answer and the one the rest of the program already expects.
func (r *Repository) prefs() *messagingprovider.PrefsStore {
	return messagingprovider.NewPrefsStore(r.pool)
}

// recorder builds the audit Recorder over this repository's pool.
//
// WHY IT IS CONSTRUCTED HERE RATHER THAN INJECTED. The house pattern is main.go's
// `svc.WithAudit(auditSvc)`, and that is the better shape, but it is a line in the composition root,
// which is outside this lane's fence, and an unwired recorder would mean either an unaudited consent
// write (unacceptable for this particular fact) or a feature that does nothing until somebody applies
// a patch. Constructing it here is NOT a boundary violation: every row still goes through
// audit.Service.Record, so the allowlist, the cleartext detector and the request-id correlation all
// apply exactly as they do for every other caller. Moving to the injected recorder is parked
// (NOTES-FIX-482 park 2) and is a one-line change on both sides.
func (r *Repository) recorder() audit.Recorder {
	return audit.NewService(audit.NewRepository(r.pool), nil)
}

// SMSConsent reads one customer's texting permission. NO ROW IS NOT AN ERROR: it is the answer, and
// the answer is no.
func (r *Repository) SMSConsent(ctx context.Context, customerID string) (SMSConsent, error) {
	p, found, err := r.prefs().Get(ctx, customerID)
	if err != nil {
		return SMSConsent{}, fmt.Errorf("customer: read sms consent: %w", err)
	}
	return consentFrom(customerID, p, found), nil
}

// SetSMSConsent records a staff-captured consent decision, then its audit row.
//
// GRANTING requires a number, because the permission is to text a PERSON AT A NUMBER and the table's
// own CHECK refuses a verified row without one. If the customer already has a number on file the
// caller may omit it; otherwise the request is refused with words rather than storing a consent that
// resolves to nobody.
//
// Granting also CLEARS a prior opt-out. That is the difference between a control that works and one
// that appears to: without it, a customer who once replied STOP and has since asked to be put back on
// would be recorded as consenting and still never receive anything, the exact class of silent
// disagreement between screen and system this batch exists to end. The change is attributed like any
// other, so a re-grant after a STOP is a visible act with a name against it.
//
// REVOKING sets the kill switch rather than deleting the row. You must remember the number you may
// not text, or the next staff member to type it in re-grants a consent that was withdrawn.
func (r *Repository) SetSMSConsent(ctx context.Context, in SMSConsentWrite) (SMSConsent, error) {
	before, err := r.SMSConsent(ctx, in.CustomerID)
	if err != nil {
		return SMSConsent{}, err
	}

	if in.Grant {
		phone := in.PhoneE164
		if phone == "" {
			// Fall back to the number already recorded for this customer. r.prefs().Get is re-read
			// rather than taken from `before`, which carries only the MASKED number by design.
			existing, _, gerr := r.prefs().Get(ctx, in.CustomerID)
			if gerr != nil {
				return SMSConsent{}, fmt.Errorf("customer: read sms consent: %w", gerr)
			}
			phone = strings.TrimSpace(existing.PhoneE164)
		}
		if phone == "" {
			return SMSConsent{}, httpx.BadRequestf(
				"a mobile number in E.164 form (+16135550101) is required to record consent")
		}
		// Upsert refuses a number that is not E.164 rather than storing something a carrier will
		// reject and bill for, that validation stays in the owning package.
		if err := r.prefs().Upsert(ctx, in.CustomerID, phone, true); err != nil {
			return SMSConsent{}, httpx.BadRequestf("%s", err.Error())
		}
		if before.Recorded {
			if err := r.prefs().SetOptOut(ctx, in.CustomerID, false, ""); err != nil {
				return SMSConsent{}, fmt.Errorf("customer: clear sms opt-out: %w", err)
			}
		}
	} else {
		if err := r.prefs().SetOptOut(ctx, in.CustomerID, true, consentRevokedReason); err != nil {
			return SMSConsent{}, fmt.Errorf("customer: revoke sms consent: %w", err)
		}
	}

	after, err := r.SMSConsent(ctx, in.CustomerID)
	if err != nil {
		return SMSConsent{}, err
	}

	if err := r.recordConsentChange(ctx, in, before, after); err != nil {
		// NOT best-effort. A consent change that could not be attributed must not be reported as
		// recorded, the operator would tell the customer they were signed up, and the only evidence
		// of who did it would be missing.
		return SMSConsent{}, fmt.Errorf("customer: record sms consent trail: %w", err)
	}
	return after, nil
}

// consentRevokedReason is the opt_out_reason a staff revocation writes. It matches the vocabulary
// migration 0109 documents for the column ('STOP reply' | 'phoned in' | 'staff request').
const consentRevokedReason = "staff request"

// recordConsentChange writes the A-21 row: WHO changed it, WHEN (audit_log's own timestamp), for
// WHICH customer, and from what to what.
//
// The Extra keys are `field`/`before`/`after`, which are already on audit's allowlist for exactly
// this shape (the order-edit and quote-edit trails use them). Nothing here carries a phone number:
// the value pair is "true"/"false", and the number is not the fact being audited, the permission is.
func (r *Repository) recordConsentChange(ctx context.Context, in SMSConsentWrite, before, after SMSConsent) error {
	return r.recorder().Record(ctx, audit.SensitiveAccess{
		Action:     audit.ActConsentChanged,
		EntityType: "customer.sms_consent",
		EntityID:   in.CustomerID,
		ActorID:    in.ActorID,
		Purpose:    "SMS_CONSENT_CAPTURE",
		Extra: map[string]any{
			"field":  "sms_consent",
			"before": consentWord(before),
			"after":  consentWord(after),
		},
	})
}

// consentWord renders a consent state for the trail. Three words, not two: "unrecorded" is a
// materially different starting point from "no", and a trail that flattens them cannot answer
// whether anybody had ever asked.
func consentWord(c SMSConsent) string {
	switch {
	case !c.Recorded:
		return "unrecorded"
	case c.Consent:
		return "yes"
	default:
		return "no"
	}
}

// consentFrom projects the owner's prefs onto what this surface reports. Sendable() is asked rather
// than reassembled from the flags, so there is one definition of "may we text them".
func consentFrom(customerID string, p messagingprovider.SMSPrefs, found bool) SMSConsent {
	out := SMSConsent{CustomerID: customerID, Recorded: found}
	if !found {
		return out
	}
	out.Consent = p.Sendable()
	out.PhoneMasked = maskPhone(p.PhoneE164)
	if p.OptedOut {
		out.OptOutReason = p.OptOutReason
	}
	return out
}

// maskPhone renders a number for a screen without publishing it: the last two digits only, which is
// enough for a member of staff to confirm they are looking at the right number and not enough to
// dial. It mirrors messagingprovider.redactPhone, which is unexported.
func maskPhone(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) <= 2 {
		return "•••"
	}
	return "••• " + s[len(s)-2:]
}
