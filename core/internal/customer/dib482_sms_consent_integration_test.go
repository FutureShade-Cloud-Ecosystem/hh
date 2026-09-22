//go:build integration

// dib482_sms_consent_integration_test.go, DIB-482 / M3-G3 half B.
//
// THE GAP. customer_sms_prefs had zero rows and no writer reachable by a member of staff. Every SMS
// this program tried to send was skipped for want of consent, correctly, and forever. These tests
// hold the new control to the standard the table was built to: absence means no, consent is recorded
// with who and when, and the thing that changes is the answer the SEND path actually asks for.
package customer

import (
	"context"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messagingprovider"
	"github.com/jackc/pgx/v5/pgxpool"
)

const dib482Actor = "11111111-2222-3333-4444-555555555555"

// consentCustomer seeds a throwaway customer and returns its id, cleaning up the customer AND any
// consent row the test writes.
func consentCustomer(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	repo := NewRepository(pool)
	acct, err := NewService(repo).WithWriter(repo).CreateCustomer(ctx, CreateInput{
		AccountNo:    "DIB-482-" + randSuffix(),
		Name:         "DIB-482 Consent Test Co",
		CustomerType: "CONTRACTOR",
		BillingAddress: map[string]any{
			"line1": "1 Yard Rd", "city": "Trenton", "province": "ON", "postal": "K8V 5P7",
		},
	})
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM audit_log WHERE entity_id = $1`, acct.CustomerID)
		_, _ = pool.Exec(c, `DELETE FROM customer_sms_prefs WHERE customer_id = $1`, acct.CustomerID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, acct.CustomerID)
	})
	return acct.CustomerID
}

// staffCtx stamps the acting staff member the guard would have stamped.
func staffCtx() context.Context {
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: dib482Actor,
		Status: identity.StatusActive,
	})
}

func consentSvc(pool *pgxpool.Pool) *Service {
	repo := NewRepository(pool)
	return NewService(repo).WithWriter(repo)
}

// TestIntegration_DIB482_ConsentDefaultsToNoAndIsNotAnError. A customer nobody has asked answers
// "no", with 'recorded' false so the surface can tell that apart from a refusal. It must not be a
// not-found: empty is never an error.
func TestIntegration_DIB482_ConsentDefaultsToNoAndIsNotAnError(t *testing.T) {
	pool := intPool(t)
	id := consentCustomer(t, pool)

	got, err := consentSvc(pool).GetSMSConsent(context.Background(), id)
	if err != nil {
		t.Fatalf("a customer with no consent row must read cleanly, got: %v", err)
	}
	if got.Consent {
		t.Fatal("a customer nobody has asked is reported as consenting, the absent state must mean no")
	}
	if got.Recorded {
		t.Fatal("recorded=true with no row on file")
	}
}

// TestIntegration_DIB482_GrantingConsentMakesTheCustomerSendable is the whole point of the control,
// measured at the seam the SEND path actually asks, messagingprovider's ConsentResolver, the same
// object quote's cadence consults through MaySendSMS. Writing a row that the resolver still refuses
// would be a control that appears to work.
func TestIntegration_DIB482_GrantingConsentMakesTheCustomerSendable(t *testing.T) {
	pool := intPool(t)
	id := consentCustomer(t, pool)
	resolver := messagingprovider.NewConsentResolver(messagingprovider.NewPrefsStore(pool), nil)

	if _, err := resolver.CustomerRecipient(context.Background(), id); err == nil {
		t.Fatal("the resolver already accepts a customer who has consented to nothing")
	}

	out, err := consentSvc(pool).SetSMSConsent(staffCtx(), id, true, "+16135550188")
	if err != nil {
		t.Fatalf("record consent: %v", err)
	}
	if !out.Consent || !out.Recorded {
		t.Fatalf("after recording consent the surface reports %+v", out)
	}

	if _, err := resolver.CustomerRecipient(context.Background(), id); err != nil {
		t.Fatalf("the SEND path still refuses this customer after consent was recorded: %v, a "+
			"consent control that does not change the send decision is a control that lies", err)
	}
}

// TestIntegration_DIB482_ConsentIsRecordedWithWhoAndWhen is the A-21 leg, and the mutation anchor for
// the trail: delete recordConsentChange from the store and this test dies.
func TestIntegration_DIB482_ConsentIsRecordedWithWhoAndWhen(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	id := consentCustomer(t, pool)

	if _, err := consentSvc(pool).SetSMSConsent(staffCtx(), id, true, "+16135550189"); err != nil {
		t.Fatalf("record consent: %v", err)
	}

	var (
		actor   string
		action  string
		before  string
		after   string
		hasTime bool
	)
	err := pool.QueryRow(ctx, `
		SELECT COALESCE(actor_id::text,''), action,
		       COALESCE(changes->>'before',''), COALESCE(changes->>'after',''),
		       created_at IS NOT NULL
		FROM audit_log
		WHERE entity_id = $1 AND action = $2
		ORDER BY seq DESC LIMIT 1`, id, audit.ActConsentChanged).
		Scan(&actor, &action, &before, &after, &hasTime)
	if err != nil {
		t.Fatalf("no %s audit row for the consent capture: %v, consent whose provenance is missing "+
			"is not a consent record", audit.ActConsentChanged, err)
	}
	if actor != dib482Actor {
		t.Errorf("audit actor = %q, want the guard-stamped staff member %q", actor, dib482Actor)
	}
	if !hasTime {
		t.Error("the consent audit row carries no timestamp")
	}
	if before != "unrecorded" || after != "yes" {
		t.Errorf("audit before/after = %q/%q, want unrecorded/yes", before, after)
	}
}

// TestIntegration_DIB482_TheTrailCarriesNoPhoneNumber. The audited fact is the PERMISSION. A consent
// trail that copies the mobile number into audit_log has published it to everyone who can read the
// admin viewer, for no gain, the number lives in the consent row itself.
func TestIntegration_DIB482_TheTrailCarriesNoPhoneNumber(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	id := consentCustomer(t, pool)
	const phone = "+16135550190"

	if _, err := consentSvc(pool).SetSMSConsent(staffCtx(), id, true, phone); err != nil {
		t.Fatalf("record consent: %v", err)
	}
	var blob string
	if err := pool.QueryRow(ctx, `
		SELECT changes::text FROM audit_log
		WHERE entity_id = $1 AND action = $2 ORDER BY seq DESC LIMIT 1`,
		id, audit.ActConsentChanged).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(blob, phone) || strings.Contains(blob, "5550190") {
		t.Fatalf("the consent audit row contains the mobile number: %s", blob)
	}
}

// TestIntegration_DIB482_RevokingIsAKillSwitchNotADeletion. Withdrawal must leave the number behind,
// or the next member of staff to type it in re-grants a consent that was withdrawn.
func TestIntegration_DIB482_RevokingIsAKillSwitchNotADeletion(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	id := consentCustomer(t, pool)
	svc := consentSvc(pool)

	if _, err := svc.SetSMSConsent(staffCtx(), id, true, "+16135550191"); err != nil {
		t.Fatalf("grant: %v", err)
	}
	out, err := svc.SetSMSConsent(staffCtx(), id, false, "")
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if out.Consent {
		t.Fatal("still reported as consenting after a revocation")
	}
	if !out.Recorded {
		t.Fatal("the revocation deleted the record, a withdrawal we cannot see is a withdrawal we " +
			"will overwrite")
	}

	var phone string
	var optedOut bool
	if err := pool.QueryRow(ctx,
		`SELECT phone_e164, opted_out FROM customer_sms_prefs WHERE customer_id = $1`, id).
		Scan(&phone, &optedOut); err != nil {
		t.Fatalf("re-read prefs: %v", err)
	}
	if !optedOut {
		t.Error("opted_out was not set, the kill switch did not flip")
	}
	if phone == "" {
		t.Error("the number was erased; you must remember the number you may not text")
	}

	// And the send path agrees.
	resolver := messagingprovider.NewConsentResolver(messagingprovider.NewPrefsStore(pool), nil)
	if _, err := resolver.CustomerRecipient(ctx, id); err == nil {
		t.Fatal("the resolver still accepts a customer whose consent was withdrawn")
	}
}

// TestIntegration_DIB482_RegrantingAfterAWithdrawalWorks. Without clearing the opt-out, a customer
// who once replied STOP and has since asked to be put back on would be recorded as consenting and
// still never receive anything, the exact silent disagreement between screen and system that this
// batch exists to end.
func TestIntegration_DIB482_RegrantingAfterAWithdrawalWorks(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	id := consentCustomer(t, pool)
	svc := consentSvc(pool)

	if _, err := svc.SetSMSConsent(staffCtx(), id, true, "+16135550192"); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if _, err := svc.SetSMSConsent(staffCtx(), id, false, ""); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	out, err := svc.SetSMSConsent(staffCtx(), id, true, "")
	if err != nil {
		t.Fatalf("re-grant: %v", err)
	}
	if !out.Consent {
		t.Fatal("the re-grant did not take, the earlier opt-out is still in force")
	}
	resolver := messagingprovider.NewConsentResolver(messagingprovider.NewPrefsStore(pool), nil)
	if _, err := resolver.CustomerRecipient(ctx, id); err != nil {
		t.Fatalf("the send path still refuses a re-granted customer: %v", err)
	}
	// The re-grant is a visible act with a name against it, like every other.
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE entity_id = $1 AND action = $2`,
		id, audit.ActConsentChanged).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("three consent decisions produced %d audit rows", n)
	}
}

// TestIntegration_DIB482_ConsentWithoutANumberIsRefusedWithWords. The permission is to text a person
// AT A NUMBER; the table's own CHECK refuses a verified row without one. Refusing here with a
// sentence beats a 500 from a constraint the operator cannot read.
func TestIntegration_DIB482_ConsentWithoutANumberIsRefusedWithWords(t *testing.T) {
	pool := intPool(t)
	id := consentCustomer(t, pool)

	_, err := consentSvc(pool).SetSMSConsent(staffCtx(), id, true, "")
	if err == nil {
		t.Fatal("consent was recorded with no number to text")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "number") {
		t.Fatalf("the refusal does not mention the missing number: %v", err)
	}
}

// TestIntegration_DIB482_ANumberACarrierWouldRejectIsRefused. The E.164 validation lives in the
// owning package and must not be bypassed by coming through this door.
func TestIntegration_DIB482_ANumberACarrierWouldRejectIsRefused(t *testing.T) {
	pool := intPool(t)
	id := consentCustomer(t, pool)

	if _, err := consentSvc(pool).SetSMSConsent(staffCtx(), id, true, "613-555-0193"); err == nil {
		t.Fatal("a free-text phone number was accepted as a verified E.164 consent number")
	}
	got, err := consentSvc(pool).GetSMSConsent(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Recorded {
		t.Fatal("a refused consent left a row behind")
	}
}

// TestIntegration_DIB482_AnUnattributedWriteIsRefused. No caller on the context means no "who", and a
// consent record without a who is a rumour with a timestamp.
func TestIntegration_DIB482_AnUnattributedWriteIsRefused(t *testing.T) {
	pool := intPool(t)
	id := consentCustomer(t, pool)

	if _, err := consentSvc(pool).SetSMSConsent(context.Background(), id, true, "+16135550194"); err == nil {
		t.Fatal("consent was recorded with no acting staff member on the context")
	}
	got, err := consentSvc(pool).GetSMSConsent(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Recorded {
		t.Fatal("the unattributed write landed anyway")
	}
}

// TestIntegration_DIB482_TheSurfaceNeverReturnsTheCleartextNumber. Staff who need to dial have
// customer_contacts; a consent screen is not a place to publish a mobile number.
func TestIntegration_DIB482_TheSurfaceNeverReturnsTheCleartextNumber(t *testing.T) {
	pool := intPool(t)
	id := consentCustomer(t, pool)
	const phone = "+16135550195"

	out, err := consentSvc(pool).SetSMSConsent(staffCtx(), id, true, phone)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if out.PhoneMasked == phone || strings.Contains(out.PhoneMasked, "5550195") {
		t.Fatalf("the consent surface returned the cleartext number: %q", out.PhoneMasked)
	}
	if !strings.HasSuffix(out.PhoneMasked, "95") {
		t.Errorf("the mask %q does not end in the last two digits, so staff cannot confirm the "+
			"number they are looking at", out.PhoneMasked)
	}
}
