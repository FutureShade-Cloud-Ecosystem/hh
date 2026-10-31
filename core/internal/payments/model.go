// Package payments is the portal-auth-gated payment module (P3.1): stored payment
// methods (vaulted tokens + display metadata) and the multi-invoice pay flow. Module
// boundary: it owns portal_payment_methods (migration 0027) and WRITES payments +
// payment_allocations + customer_transactions (migration 0010/0009) inside one atomic
// transaction. Card/ACH data never touches our DB, only Run vault tokens and display
// metadata (PCI SAQ A, §4.2). The processor is abstracted behind PaymentGateway; the
// deterministic StubGateway is the default and a RunGateway slots in later by config.
package payments

import "time"

// PaymentMethod is a vaulted method: an OPAQUE processor token plus the display
// metadata we are permitted to store. No PAN/account/routing ever lands here, only
// brand/last4/exp/type (§4.2). The token field is unexported and never serialized.
type PaymentMethod struct {
	ID         string    `json:"id"`
	CustomerID string    `json:"customer_id"`
	Gateway    string    `json:"gateway"`     // stub | run
	MethodType string    `json:"method_type"` // CARD | BANK
	Brand      string    `json:"brand"`       // VISA | MASTERCARD | bank name ...
	Last4      string    `json:"last4"`
	ExpMonth   int       `json:"exp_month"`
	ExpYear    int       `json:"exp_year"`
	IsDefault  bool      `json:"is_default"`
	Status     string    `json:"status"` // active | removed
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`

	// token is the opaque durable vault token used to charge this method. It is the one
	// secret-adjacent field and is NEVER marshaled to a client.
	token string
}

// Token returns the opaque vault token (for the charge path). Kept as an accessor so the
// field stays unexported and out of JSON.
func (m PaymentMethod) Token() string { return m.token }

// WithToken returns a copy of m carrying the vault token (used when building a row to
// persist after tokenization).
func (m PaymentMethod) WithToken(token string) PaymentMethod { m.token = token; return m }

// AddMethodRequest carries a one-time hosted-field nonce to exchange for a durable vault
// token. The PAN never reaches our servers, only the nonce (§4.2).
type AddMethodRequest struct {
	Nonce       string `json:"nonce"`
	MakeDefault bool   `json:"make_default"`
}

// PayRequest is a multi-invoice payment: ONE charge for the summed balance of the
// selected invoices, split atomically across payment_allocations (§4.2). Funded by a
// stored method OR a one-time nonce (add-at-checkout). IdempotencyKey from the client is
// advisory only, the server RE-BINDS it to {customer_id, request hash} before charging.
type PayRequest struct {
	InvoiceIDs      []string `json:"invoice_ids"`
	PaymentMethodID string   `json:"payment_method_id"` // a stored method ...
	Nonce           string   `json:"nonce"`             // ... OR add-at-checkout
	IdempotencyKey  string   `json:"idempotency_key"`
}

// Payment is a recorded payments row (0010): method/gateway_ref/status, like the POS
// stub. Allocations link it to the invoices it settled.
type Payment struct {
	ID          string       `json:"id"`
	PaymentNo   string       `json:"payment_no"`
	CustomerID  string       `json:"customer_id"`
	Method      string       `json:"method"` // CARD | EFT | ...
	AmountCents int64        `json:"amount_cents"`
	Status      string       `json:"status"` // RECEIVED | DEPOSITED | BOUNCED | REFUNDED | VOID
	GatewayRef  string       `json:"gateway_ref"`
	ReceivedAt  time.Time    `json:"received_at"`
	Allocations []Allocation `json:"allocations"`
}

// Allocation is one slice of a payment applied to one invoice (0010 payment_allocations).
// Status carries the invoice's status when derived by PayableInvoices so the pay flow can
// reject a non-payable selection BEFORE charging; it is never serialized to the client.
// AllocatedBy/AllocatedByName/CreatedAt are the Q-044 attribution facts (WHO matched this
// payment to this invoice, WHEN): staff-stamped for counter payments (0043 allocated_by),
// empty for a portal (customer-initiated) payment.
type Allocation struct {
	InvoiceID       string    `json:"invoice_id"`
	AmountCents     int64     `json:"amount_cents"`
	AllocatedBy     string    `json:"allocated_by,omitempty"`
	AllocatedByName string    `json:"allocated_by_name,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	Status          string    `json:"-"`
	IsNSFFee        bool      `json:"-"`
	// DiscountCents is the early payment discount this application took beside the money (TM-2):
	// zero on every allocation an unbound repository settles, the invoice's own terms percent
	// otherwise. The balance move is the money and the discount together.
	DiscountCents int64 `json:"discount_cents,omitempty"`
	// DiscountDay is the last house day the quoted discount runs (TM-2b), set only by PayableInvoices
	// when it quotes a discount; empty everywhere else and never read from a client.
	DiscountDay string `json:"-"`
}
