// seal.go, why a demo delivery gets REAL evidence.
//
// 0090's POD check refuses a DELIVERED stop that has no signature reference and no timestamp, and
// its header says exactly why: "delivered" is a claim, and a claim with no evidence behind it is
// what the paper clipboard already gave us. The same check guards a CONFIRMED will-call pickup.
//
// A seed that wanted delivered stops could satisfy the letter of that constraint with any non-empty
// string. It would pass the CHECK and teach the wrong lesson: the demo database would then contain
// exactly the unevidenced deliveries the constraint exists to make impossible, and anybody reading
// the data to understand the system would learn that a POD ref is decorative.
//
// So the seed captures a genuine attestation instead. It marshals the SAME pos.SealedAttestation
// envelope the shipped typed-name provider writes, seals it through the SAME AES-256-GCM vault
// primitive, and stores it in pos_signature stamped TYPED_NAME_V1. What the sealed payload says is
// true: a demo receiver, named on the row, affirmed a demo delivery at a stated time.
//
// THE ONE DIFFERENCE FROM THE SERVICE PATH, stated so nobody has to diff two files to find it: the
// reference is DERIVED from the row's key rather than drawn from crypto/rand, and no audit_log row
// is written. Both follow from idempotency, a random ref would differ between two runs of the same
// plan, and an audit trail of a seeding run is noise in the index that real sensitive-access
// queries read. The evidence itself is not weakened by either.
// demoseed_seal carries the pin's cmd/demoseed/seal.go (the fold:
// one-shot subcommand of cmd/core, plan and apply modes preserved).
package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
)

// newSealer returns the plan's sealer, backed by the configured payment vault.
//
// An EMPTY vault key is legal and is what dev runs on: payment.NewVault generates an ephemeral key,
// so the payload is genuinely encrypted but nothing can decrypt it later. That is the same posture
// the running server has with an unset key, and it is fine here because no surface ever needs to
// open a demo POD. main.go says which mode it is in, out loud, so nobody is surprised.
func newSealer(vaultKey string) (sealer, bool, error) {
	vault, err := payment.NewVault(vaultKey)
	if err != nil {
		return nil, false, fmt.Errorf("demoseed: payment vault: %w", err)
	}
	return func(refKey, typedName, purpose, refType, refID string, at time.Time) (string, []byte, error) {
		if typedName == "" {
			return "", nil, fmt.Errorf("demoseed: %s has no receiver name, an attestation with nobody's name on it is not an attestation", refKey)
		}
		ref := demoRef("sig_", refKey)
		payload, err := json.Marshal(pos.SealedAttestation{
			Version:   pos.ProviderTypedNameV1,
			TypedName: typedName,
			Affirmed:  true,
			Purpose:   purpose,
			RefType:   refType,
			RefID:     refID,
			// ActorID is deliberately EMPTY. No staff member witnessed this; the seed did, and
			// attributing it to a real user id would be the one dishonest field on an otherwise
			// honest record.
			ActorID:    "",
			CapturedAt: at.UTC(),
			Ref:        ref,
		})
		if err != nil {
			return "", nil, err
		}
		enc, _, err := vault.Encrypt(payload)
		if err != nil {
			return "", nil, err
		}
		return ref, enc, nil
	}, vaultKey != "", nil
}
