package payment

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the write-only config store. It SELECTs/INSERTs ONLY payment_config (RUN-PAY-owned) -
// it never touches payments, till_sessions, orders, etc. (zero-trust).
type Store struct {
	pool  *pgxpool.Pool
	vault *Vault
}

// NewStore wires the pool + the AES-GCM vault.
func NewStore(pool *pgxpool.Pool, vault *Vault) *Store { return &Store{pool: pool, vault: vault} }

// injectedCredKeys are non-secret values activeConfig threads INTO the in-memory creds map for the
// live client (host/currency) plus the call-scoped idempotency key. They are NOT credentials and
// must be stripped before any (re-)encryption so they never leak into the stored ciphertext.
var injectedCredKeys = []string{"__idem", "base_url", "currency"}

// credentialKeys returns a copy of creds with the injected non-secret keys removed, the set that is
// safe to encrypt into payment_config.credentials_ciphertext.
func credentialKeys(creds map[string]string) map[string]string {
	out := make(map[string]string, len(creds))
	for k, v := range creds {
		out[k] = v
	}
	for _, k := range injectedCredKeys {
		delete(out, k)
	}
	return out
}

// activeCfg is the non-secret view of the active config row.
type activeCfg struct {
	Processor  string
	MerchantID string
	Currency   string
	BaseURL    string
	MDRBps     int64
	Surcharge  bool
	// DIB-102 per-tender configuration (0106). SurchargeCardTypes can only NARROW the calculator's
	// CREDIT-only floor (to 'NONE'); it can never widen it to debit, which Ontario prohibits.
	SurchargeCardTypes string
	EFTEnabled         bool
}

// Configure stores merchant id + encrypted creds (write-only; engagement-level). It retires any prior
// active row for the processor and inserts a new active one (ux_payment_config_active enforces one active).
func (s *Store) Configure(ctx context.Context, processor, merchantID, currency, baseURL string, mdrBps int64, surchargeEnabled bool, creds map[string]string, actorID string) error {
	// HH-186: the row can never say enabled while no tender path computes a surcharge and D18's
	// precondition (a recorded jurisdiction and cap) is unmet. Refused before any pool or vault
	// touch so every caller gets the same answer, not just the HTTP route.
	if surchargeEnabled {
		return ErrSurchargeNotEnforced
	}
	if s.pool == nil {
		return errors.New("payment: database unavailable")
	}
	if processor == "" {
		processor = "run_payments"
	}
	if currency == "" {
		currency = "CAD"
	}
	plain, err := json.Marshal(creds)
	if err != nil {
		return err
	}
	ct, kv, err := s.vault.Encrypt(plain)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`UPDATE payment_config SET status='disabled', updated_at=now() WHERE processor=$1 AND status='active'`,
		processor); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO payment_config (processor, merchant_id, currency, credentials_ciphertext, key_version,
			base_url, mdr_bps, surcharge_enabled, status, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'active', NULLIF($9,'')::uuid)`,
		processor, merchantID, currency, ct, kv, baseURL, mdrBps, surchargeEnabled, actorID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Status reports whether a config is active + the non-secret merchant_id/processor/currency. NEVER creds.
func (s *Store) Status(ctx context.Context) (configured bool, merchantID, processor, currency string, err error) {
	if s.pool == nil {
		return false, "", "", "", nil
	}
	err = s.pool.QueryRow(ctx,
		`SELECT merchant_id, processor, currency FROM payment_config WHERE status='active' ORDER BY created_at DESC LIMIT 1`).
		Scan(&merchantID, &processor, &currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", "", "", nil
	}
	if err != nil {
		return false, "", "", "", err
	}
	return true, merchantID, processor, currency, nil
}

// activeConfig loads the active row + DECRYPTS creds IN-MEMORY ONLY (never logged, never returned out).
func (s *Store) activeConfig(ctx context.Context) (cfg activeCfg, creds map[string]string, err error) {
	if s.pool == nil {
		return activeCfg{}, nil, ErrNoConfig
	}
	var ct []byte
	var kv int
	err = s.pool.QueryRow(ctx,
		`SELECT processor, merchant_id, currency, base_url, mdr_bps, surcharge_enabled,
		        surcharge_card_types, eft_enabled, credentials_ciphertext, key_version
		   FROM payment_config WHERE status='active' ORDER BY created_at DESC LIMIT 1`).
		Scan(&cfg.Processor, &cfg.MerchantID, &cfg.Currency, &cfg.BaseURL, &cfg.MDRBps, &cfg.Surcharge,
			&cfg.SurchargeCardTypes, &cfg.EFTEnabled, &ct, &kv)
	if errors.Is(err, pgx.ErrNoRows) {
		return activeCfg{}, nil, ErrNoConfig
	}
	if err != nil {
		return activeCfg{}, nil, err
	}
	plain, err := s.vault.Decrypt(ct, kv)
	if err != nil {
		return activeCfg{}, nil, err
	}
	if err := json.Unmarshal(plain, &creds); err != nil {
		return activeCfg{}, nil, err
	}
	if creds == nil {
		creds = map[string]string{}
	}
	// Thread the resolved host/currency into the creds map so the live client can read them.
	if cfg.BaseURL != "" {
		creds["base_url"] = cfg.BaseURL
	}
	if cfg.Currency != "" {
		creds["currency"] = cfg.Currency
	}
	return cfg, creds, nil
}

// reencryptActiveCreds re-seals the rotating credential set (after the live client refreshed an
// expiring api_key) into the active payment_config row. It writes ONLY payment_config (zero-trust);
// the injected non-secret keys are stripped first so the ciphertext stays creds-only.
func (s *Store) reencryptActiveCreds(ctx context.Context, processor string, creds map[string]string) error {
	if s.pool == nil {
		return errors.New("payment: database unavailable")
	}
	if processor == "" {
		processor = "run_payments"
	}
	plain, err := json.Marshal(credentialKeys(creds))
	if err != nil {
		return err
	}
	ct, kv, err := s.vault.Encrypt(plain)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`UPDATE payment_config SET credentials_ciphertext=$1, key_version=$2, updated_at=now()
		   WHERE processor=$3 AND status='active'`,
		ct, kv, processor)
	return err
}

// configResolver is the read seam Service depends on: it loads the active config + decrypts creds
// in-memory. *Store satisfies it; tests use a DB-free fake. (Keeping Service behind an interface
// lets the pure Charge gateway call be unit-tested without a database, the internal/pricing pattern.)
type configResolver interface {
	activeConfig(ctx context.Context) (activeCfg, map[string]string, error)
}

// credsReencryptor is an OPTIONAL capability a resolver may implement: re-seal the active config's
// rotating creds after the live client refreshes an expired api_key (ARCH_DELTA §2.11). *Store
// implements it; the DB-free fake does not, so unit tests never need a database for the charge path.
type credsReencryptor interface {
	reencryptActiveCreds(ctx context.Context, processor string, creds map[string]string) error
}

// Service is the till-facing seam. It holds the config resolver + the active Processor.
type Service struct {
	cfg  configResolver
	proc Processor
}

// NewService wires the config store + the active Processor (RunPaymentsStub by default).
func NewService(store *Store, proc Processor) *Service { return &Service{cfg: store, proc: proc} }

// Simulated reports whether the wired Processor is the deterministic stub (card TEST MODE, no live
// gateway call). Surfaced on the config-status endpoint so the POS UI can label the card rail honestly;
// it flips with the wiring: false once the composition root wires the live client (buildCardRail, on
// the processor the selector chose), true while the stub is wired.
func (s *Service) Simulated() bool {
	_, ok := s.proc.(RunPaymentsStub)
	return ok
}

// Charge is a PURE GATEWAY CALL, it writes NOTHING to payments (D-RUNPAY-1). It loads the active
// config, decrypts creds in-memory, injects the idempotency key call-scoped, dispatches to the
// Processor, and returns a ChargeReceipt. A nil/zero token or amount fails before any dispatch.
// If the live client rotated the api_key (expiring JWT refresh), the new rotating creds are
// re-encrypted back into the active payment_config row, never to the payments table.
func (s *Service) Charge(ctx context.Context, in ChargeInput) (ChargeReceipt, error) {
	cfg, creds, err := s.cfg.activeConfig(ctx)
	if err != nil {
		return ChargeReceipt{}, err
	}
	if in.PaymentToken == "" || in.AmountCents <= 0 {
		return ChargeReceipt{}, errBadCharge
	}
	prevKey := creds["api_key"]
	creds["__idem"] = in.IdempotencyKey // call-scoped; not persisted
	res, chargeErr := s.proc.Charge(ctx, creds, cfg.MerchantID, in.AmountCents, in.PaymentToken)
	// Persist a rotated api_key (the live client refreshed an expired JWT) into the active config.
	if creds["api_key"] != prevKey && creds["api_key"] != "" {
		if rs, ok := s.cfg.(credsReencryptor); ok {
			_ = rs.reencryptActiveCreds(ctx, cfg.Processor, creds)
		}
	}
	rcpt := ChargeReceipt{Processor: cfg.Processor, ProcessorRef: res.ProcessorRef, Status: res.Status}
	if chargeErr != nil {
		rcpt.Status = "failed"
		return rcpt, chargeErr
	}
	return rcpt, nil
}

// Refund is a PURE GATEWAY CALL mirroring Charge, it writes NOTHING to payments (the till persists
// the REFUNDED row from the receipt). It loads the active config, decrypts creds in-memory, injects
// the idempotency key call-scoped, dispatches Processor.Refund against the ORIGINAL capture's
// processor ref, and returns a RefundReceipt. A rotated api_key is re-sealed exactly as in Charge.
func (s *Service) Refund(ctx context.Context, in RefundInput) (RefundReceipt, error) {
	cfg, creds, err := s.cfg.activeConfig(ctx)
	if err != nil {
		return RefundReceipt{}, err
	}
	if in.ProcessorRef == "" || in.AmountCents <= 0 {
		return RefundReceipt{}, errBadRefund
	}
	prevKey := creds["api_key"]
	creds["__idem"] = in.IdempotencyKey // call-scoped; not persisted
	res, refundErr := s.proc.Refund(ctx, creds, cfg.MerchantID, in.ProcessorRef, in.AmountCents)
	if creds["api_key"] != prevKey && creds["api_key"] != "" {
		if rs, ok := s.cfg.(credsReencryptor); ok {
			_ = rs.reencryptActiveCreds(ctx, cfg.Processor, creds)
		}
	}
	rcpt := RefundReceipt{Processor: cfg.Processor, ProcessorRef: res.ProcessorRef, Status: res.Status}
	if refundErr != nil {
		rcpt.Status = "failed"
		return rcpt, refundErr
	}
	return rcpt, nil
}
