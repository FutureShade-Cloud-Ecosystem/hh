package audit

import "testing"

// The pos provider's minted signature reference also rides the masked_ref of three dispatch audit
// actions (the proof of delivery capture, the photo attach that follows it, and the will call
// confirmation). Each lost the reference about one time in six to the cleartext filter. These legs pin
// the widened exemption per action: the minted shape with a 7 to 12 digit run is kept, a card
// number-length run beside a letter still redacts, and every other action still redacts the same shape.

var sigRefDispatchActions = []string{
	"DELIVERY_POD_CAPTURED",
	"DELIVERY_POD_PHOTOS_ATTACHED",
	"WILL_CALL_CONFIRMED",
}

func TestRecord_DispatchActionKeepsMintedSignatureReference(t *testing.T) {
	for _, action := range sigRefDispatchActions {
		for _, ref := range []string{"sig_1234567890abcdef", "sig_7777777abcdef012", "sig_123456789012abcd"} {
			t.Run(action+"/"+ref, func(t *testing.T) {
				e := recordOne(t, SensitiveAccess{
					Action: action, EntityType: "delivery_stop", EntityID: sampleUUID,
					ActorID: sampleUUID, MaskedRef: ref,
				})
				if got := e.Changes["masked_ref"]; got != ref {
					t.Fatalf("masked_ref = %v, want the minted signature reference %q intact", got, ref)
				}
			})
		}
	}
}

func TestRecord_DispatchActionStillRedactsCardLengthRun(t *testing.T) {
	for _, action := range sigRefDispatchActions {
		for _, card := range []string{"sig_378282246310005a", "sig_30569309025904ab", "sig_4222222222222abc", "sig_4111111111111111"} {
			t.Run(action+"/"+card, func(t *testing.T) {
				e := recordOne(t, SensitiveAccess{
					Action: action, EntityType: "delivery_stop", EntityID: sampleUUID,
					ActorID: sampleUUID, MaskedRef: card,
				})
				if got := e.Changes["masked_ref"]; got != "[redacted]" {
					t.Fatalf("masked_ref %q = %v, want [redacted]", card, got)
				}
			})
		}
	}
}

func TestRecord_OtherActionsStillRedactMintedShape(t *testing.T) {
	for _, action := range []string{ActReturnCompleted, ActSignatureView, ActSaleCompleted, ActPaymentReceived, "DELIVERY_POD_CAPTURED_X", "will_call_confirmed", ""} {
		t.Run(action, func(t *testing.T) {
			e := recordOne(t, SensitiveAccess{
				Action: action, EntityType: "delivery_stop", EntityID: sampleUUID,
				ActorID: sampleUUID, MaskedRef: "sig_1234567890abcdef",
			})
			if got := e.Changes["masked_ref"]; got != "[redacted]" {
				t.Fatalf("masked_ref of action %q = %v, want [redacted]", action, got)
			}
		})
	}
}
