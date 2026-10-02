package payments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// StubGateway is the deterministic default processor: it approves every charge EXCEPT
// those whose amount triggers the decline rule, so tests and demos exercise both the
// approve AND decline paths without a live processor (mirrors the POS card stub). It
// returns a real, reconcilable gateway_ref but moves no money, Live() is false, so the
// fail-closed boot guard (§4.2 #3) must refuse to register payment routes against it in
// production.
type StubGateway struct{}

// NewStubGateway returns the default deterministic gateway.
func NewStubGateway() *StubGateway { return &StubGateway{} }

// declineTriggerCents: a charge whose amount ends in ".13" (cents == 13) is declined,
// and any zero/negative amount is declined, a documented, deterministic decline hook so
// a caller can force the decline branch without special-casing the gateway.
const declineTriggerCents = 13

func (g *StubGateway) Name() string { return "stub" }

func (g *StubGateway) Live() bool { return false }

// Charge approves unless the amount triggers the decline rule. The gateway ref is
// derived from the server-bound idempotency key so a replay yields the same ref.
func (g *StubGateway) Charge(_ context.Context, req ChargeRequest) (ChargeResult, error) {
	if req.AmountCents <= 0 || req.AmountCents%100 == declineTriggerCents {
		return ChargeResult{Outcome: OutcomeDeclined, Message: "declined (stub decline rule)"}, nil
	}
	return ChargeResult{
		Outcome:    OutcomeApproved,
		GatewayRef: stubChargeRef(req.CustomerID, req.IdempotencyKey),
		AuthCode:   "STUBOK",
		Message:    "approved (stub)",
	}, nil
}

// Vault returns deterministic display metadata + a stable token derived from the nonce,
// so the same nonce vaults identically in tests. Real metadata comes from the processor
// in a live gateway.
func (g *StubGateway) Vault(_ context.Context, req VaultRequest) (VaultedMethod, error) {
	return VaultedMethod{
		Token:      stubRef("tok", req.CustomerID, req.Nonce),
		MethodType: "CARD",
		Brand:      "VISA",
		Last4:      "4242",
		ExpMonth:   12,
		ExpYear:    2030,
	}, nil
}

// DetachMethod is a no-op for the stub (no remote vault to clear).
func (g *StubGateway) DetachMethod(_ context.Context, _ string) error { return nil }

// stubRef builds a short, stable, opaque reference from two inputs.
func stubRef(prefix, a, b string) string {
	sum := sha256.Sum256([]byte(a + "|" + b))
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(sum[:8]))
}

// stubChargeRef mints the CARD charge ref WITH the exported pieces (StubChargeRefPrefix, the
// exact construction IsStubChargeRef names), so the constant, this mint and the receipt's
// simulated stamp are one thing and cannot drift apart. The value is the prefix, then the
// first 8 bytes of sha256 over the two inputs, hex encoded, exactly 16 lowercase chars. The
// construction is load bearing rather than a style choice: persisted payments rows carry these
// bytes as gateway_ref and reconciliation matches them, so the mint may never vary on its own.
func stubChargeRef(a, b string) string {
	sum := sha256.Sum256([]byte(a + "|" + b))
	return StubChargeRefPrefix + hex.EncodeToString(sum[:8])
}

// compile-time assertion: StubGateway satisfies the port.
var _ PaymentGateway = (*StubGateway)(nil)
