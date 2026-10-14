package payments

import "context"

// ChargeOutcome is the gateway's verdict on a charge. It mirrors Run's tri-state result
// (A/B/C) rather than a boolean, so a transient "retry/processing" state stays
// representable and never collapses into a silent approval or decline (§4.2).
type ChargeOutcome string

const (
	OutcomeApproved ChargeOutcome = "APPROVED" // Run "A"
	OutcomeRetry    ChargeOutcome = "RETRY"    // Run "B", transient; safe to retry, not paid
	OutcomeDeclined ChargeOutcome = "DECLINED" // Run "C"
)

// ChargeRequest is ONE charge for the summed balance of a multi-invoice payment. Amount
// and the Run customer are derived server-side from the session, never trusted from the
// client (§4.2). IdempotencyKey is server-bound to {customer_id, request hash} so a
// retried request can never double-charge or collide across accounts (Run exposes no
// idempotency header on /charge, so this is mandatory).
type ChargeRequest struct {
	CustomerID     string
	AmountCents    int64
	Currency       string // ISO 4217, from the dealer's configuration (USD by default)
	MethodToken    string // durable vault token (a stored method) ...
	Nonce          string // ... OR a one-time hosted-field nonce (add-at-checkout)
	IdempotencyKey string
	Description    string
}

// ChargeResult is the gateway's response. GatewayRef (Run trans_id) is persisted to
// payments.gateway_ref (0010) so a charge is reconcilable against the processor.
type ChargeResult struct {
	Outcome    ChargeOutcome
	GatewayRef string // trans_id
	AuthCode   string
	Message    string
}

// VaultRequest exchanges a one-time hosted-field nonce for a durable vault token plus the
// display metadata we are allowed to store.
type VaultRequest struct {
	CustomerID string
	Nonce      string
}

// VaultedMethod is the non-sensitive result of tokenization: the opaque durable token and
// the display metadata the portal renders. No PAN/account/routing crosses here.
type VaultedMethod struct {
	Token      string
	MethodType string // CARD | BANK
	Brand      string
	Last4      string
	ExpMonth   int
	ExpYear    int
}

// PaymentGateway is the processor port. The deterministic StubGateway is the default; a
// RunGateway (Runner.js hosted fields + POST /api/v1/charge) slots in later by config
// WITHOUT touching callers (§4.2). The boot path must fail closed in prod, refusing to
// register payment routes unless a LIVE gateway + webhook secret resolve from vault.
type PaymentGateway interface {
	// Name identifies the active gateway ("stub", "run") for the fail-closed boot check
	// and audit. A prod boot must refuse a non-live gateway.
	Name() string
	// Live reports whether this gateway moves real money (false for the stub/sandbox).
	Live() bool
	// Charge runs ONE charge for the summed amount. Irreversible/external: the caller
	// persists a durable pending intent BEFORE calling this (§4.2 hardening #1).
	Charge(ctx context.Context, req ChargeRequest) (ChargeResult, error)
	// Vault exchanges a one-time nonce for a durable token + display metadata.
	Vault(ctx context.Context, req VaultRequest) (VaultedMethod, error)
	// DetachMethod removes a vaulted method at the processor, called before the local
	// soft-delete so a removed method can never be charged again.
	DetachMethod(ctx context.Context, token string) error
}
