package payment

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the payment module's store. Two tables only, both its own: payment_config (RUN-PAY-owned,
// the write-only encrypted config below) and card_charge_attempts (migration 0361, the attempt store
// in attempts.go). It never reads or writes payments, till_sessions, orders, etc. (zero-trust); the
// attempt row's foreign keys to them are the database's, and BindTx only records a payment id the
// sale's own transaction already created.
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

// activeConfigQuery is the active payment_config row plus its sealed creds.
// The pool read and the open-transaction twin (activeConfigTx) share it, the
// readCustomerQuery pattern: one SELECT, two carriers.
const activeConfigQuery = `
SELECT processor, merchant_id, currency, base_url, mdr_bps, surcharge_enabled,
       surcharge_card_types, eft_enabled, credentials_ciphertext, key_version
  FROM payment_config WHERE status='active' ORDER BY created_at DESC LIMIT 1`

// reencryptActiveCredsQuery re-seals the active row's credentials. The pool
// write and its open-transaction twin (reencryptActiveCredsTx) share it.
const reencryptActiveCredsQuery = `
UPDATE payment_config SET credentials_ciphertext=$1, key_version=$2, updated_at=now()
  WHERE processor=$3 AND status='active'`

// activeConfig loads the active row + DECRYPTS creds IN-MEMORY ONLY (never logged, never returned out).
func (s *Store) activeConfig(ctx context.Context) (cfg activeCfg, creds map[string]string, err error) {
	if s.pool == nil {
		return activeCfg{}, nil, ErrNoConfig
	}
	var ct []byte
	var kv int
	err = s.pool.QueryRow(ctx, activeConfigQuery).
		Scan(&cfg.Processor, &cfg.MerchantID, &cfg.Currency, &cfg.BaseURL, &cfg.MDRBps, &cfg.Surcharge,
			&cfg.SurchargeCardTypes, &cfg.EFTEnabled, &ct, &kv)
	if errors.Is(err, pgx.ErrNoRows) {
		return activeCfg{}, nil, ErrNoConfig
	}
	if err != nil {
		return activeCfg{}, nil, err
	}
	return s.openActiveCreds(cfg, ct, kv)
}

// activeConfigTx is activeConfig's open-transaction twin: the config read
// for a caller INSIDE its own database transaction rides that transaction
// (tx.QueryRow), never the pool. The pos Return transaction holds a
// connection and the per-sale advisory lock while it reverses the card; a
// pool read there would have the lock holder wait for a second connection
// every other connection is a Return waiter that needs the holder's lock,
// deadlocking a full pool until the caller's context ends (the
// FindReturnByKeyTx discipline).
func (s *Store) activeConfigTx(ctx context.Context, tx pgx.Tx) (cfg activeCfg, creds map[string]string, err error) {
	var ct []byte
	var kv int
	err = tx.QueryRow(ctx, activeConfigQuery).
		Scan(&cfg.Processor, &cfg.MerchantID, &cfg.Currency, &cfg.BaseURL, &cfg.MDRBps, &cfg.Surcharge,
			&cfg.SurchargeCardTypes, &cfg.EFTEnabled, &ct, &kv)
	if errors.Is(err, pgx.ErrNoRows) {
		return activeCfg{}, nil, ErrNoConfig
	}
	if err != nil {
		return activeCfg{}, nil, err
	}
	return s.openActiveCreds(cfg, ct, kv)
}

// openActiveCreds is the read's shared tail: open the sealed creds
// in-memory (never logged, never returned out) and thread the resolved
// host/currency so the live client can read them.
func (s *Store) openActiveCreds(cfg activeCfg, ct []byte, kv int) (activeCfg, map[string]string, error) {
	plain, err := s.vault.Decrypt(ct, kv)
	if err != nil {
		return activeCfg{}, nil, err
	}
	var creds map[string]string
	if err := json.Unmarshal(plain, &creds); err != nil {
		return activeCfg{}, nil, err
	}
	if creds == nil {
		creds = map[string]string{}
	}
	if cfg.BaseURL != "" {
		creds["base_url"] = cfg.BaseURL
	}
	if cfg.Currency != "" {
		creds["currency"] = cfg.Currency
	}
	return cfg, creds, nil
}

// sealCreds is the write's shared head: strip the injected non-secret keys
// and seal the credential set.
func (s *Store) sealCreds(creds map[string]string) ([]byte, int, error) {
	plain, err := json.Marshal(credentialKeys(creds))
	if err != nil {
		return nil, 0, err
	}
	return s.vault.Encrypt(plain)
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
	ct, kv, err := s.sealCreds(creds)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, reencryptActiveCredsQuery, ct, kv, processor)
	return err
}

// reencryptActiveCredsTx is the re-seal on the caller's open transaction,
// the twin the in-transaction refund path's rotation write-back rides: no
// pool use is reachable from inside the Return transaction (see
// activeConfigTx for why a pool read or write there deadlocks a full pool).
func (s *Store) reencryptActiveCredsTx(ctx context.Context, tx pgx.Tx, processor string, creds map[string]string) error {
	if processor == "" {
		processor = "run_payments"
	}
	ct, kv, err := s.sealCreds(creds)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, reencryptActiveCredsQuery, ct, kv, processor)
	return err
}

// configResolver is the read seam Service depends on: it loads the active config + decrypts creds
// in-memory. *Store satisfies it; tests use a DB-free fake. (Keeping Service behind an interface
// lets the pure Charge gateway call be unit-tested without a database, the internal/pricing pattern.)
type configResolver interface {
	activeConfig(ctx context.Context) (activeCfg, map[string]string, error)
	// activeConfigTx is the open-transaction twin: the config read for a
	// caller INSIDE its own database transaction rides that transaction,
	// never the pool. The pos Return transaction holds a connection and the
	// per-sale advisory lock; a pool read there would have the lock holder
	// wait for a second connection every other connection is a Return
	// waiter that needs the holder's lock (the FindReturnByKeyTx
	// discipline).
	activeConfigTx(ctx context.Context, tx pgx.Tx) (activeCfg, map[string]string, error)
}

// credsReencryptor is an OPTIONAL capability a resolver may implement: re-seal the active config's
// rotating creds after the live client refreshes an expired api_key (ARCH_DELTA §2.11). *Store
// implements it; the DB-free fake does not, so unit tests never need a database for the charge path.
// The tx twin exists so a refund running INSIDE the caller's transaction can write back a rotation
// on that same transaction, never through the pool (the activeConfigTx discipline on the write side).
type credsReencryptor interface {
	reencryptActiveCreds(ctx context.Context, processor string, creds map[string]string) error
	reencryptActiveCredsTx(ctx context.Context, tx pgx.Tx, processor string, creds map[string]string) error
}

// Service is the till-facing seam. It holds the config resolver + the active Processor, plus the
// rotation ledger (see holdRotation) and the flusher starter (see launchRotationFlusher).
type Service struct {
	cfg  configResolver
	proc Processor
	// attempts is the card charge attempt store Capture, Compensate and BindTx work over; nil on a
	// Service built without one (those entry points then refuse).
	attempts *Store

	rotMu        sync.Mutex
	rotPend      *rotatedCreds
	startFlusher func()
	// flushMu orders the rotation flushes end to end, so a stale flush cannot land over a newer
	// rotation's landed flush (the round 5 review's P2).
	flushMu sync.Mutex
}

// rotatedCreds is one credential rotation observed during an in-transaction charge: the processor
// (the row key) and the fresh credential set, snapshotted so a later flush cannot observe a map the
// dispatch path still holds.
type rotatedCreds struct {
	processor string
	creds     map[string]string
}

// rotationFlushWindow bounds a rotation flush's wait for a free connection: the flusher blocks while
// the caller's transaction still holds every connection (a one-connection pool until the ring ends),
// and the window is wide enough for a saturated pool to drain once the caller's transaction ends.
const rotationFlushWindow = 30 * time.Second

// NewService wires the config store + the active Processor (RunPaymentsStub by default).
func NewService(store *Store, proc Processor) *Service {
	s := &Service{cfg: store, proc: proc, attempts: store}
	s.startFlusher = s.startRotationFlusherGoroutine
	return s
}

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
// Callers holding no database transaction call THIS entry point; the sale transaction's tender
// goes through ChargeTx.
func (s *Service) Charge(ctx context.Context, in ChargeInput) (ChargeReceipt, error) {
	cfg, creds, err := s.cfg.activeConfig(ctx)
	if err != nil {
		return ChargeReceipt{}, err
	}
	return s.charge(ctx, cfg, s.freshestCreds(cfg.Processor, creds), in, s.resealOnPool)
}

// ChargeTx is Charge on the caller's open transaction. The pos Sale
// transaction holds one connection for the whole atomic ring while it
// takes the tender: the config read here rides THAT transaction
// (activeConfigTx), because a pool read there would have the connection
// holder wait for a second connection, stalling a one-connection pool on
// a single sale and deadlocking a full pool of concurrent sales until the
// caller's context ends (the activeConfigTx discipline, RefundTx's shape
// on the charge side). The rotation write-back is DIFFERENT from the
// refund side's: a credential rotation must survive a rolled-back sale
// (the processor has already exchanged the old key, so losing the fresh
// one can lock the rail out), so the carrier records the rotation in the
// ledger and a flusher goroutine writes it through the pool. The flusher
// never runs inside the transaction's call stack, so no pool use is
// reachable from it; on a one-connection pool the flush's acquire waits
// for the ring to release the only connection (the write lands after the
// ring ends, commit or rollback alike), while on a wider pool the flush
// may run on a second connection while the ring is still closing. While
// an entry sits pending, the read side overlays it (freshestCreds), so a
// charge does not present the exchanged key from the stale row. One
// window remains by design: a charge that reads the row in the instant
// the flush lands and clears the entry presents the old key once and
// heals on the next sale (recorded, the round 3 review's P3 2).
func (s *Service) ChargeTx(ctx context.Context, tx pgx.Tx, in ChargeInput) (ChargeReceipt, error) {
	cfg, creds, err := s.cfg.activeConfigTx(ctx, tx)
	if err != nil {
		return ChargeReceipt{}, err
	}
	return s.charge(ctx, cfg, s.freshestCreds(cfg.Processor, creds), in, s.carryRotationOutOfTheTransaction)
}

// holdRotation records the freshest rotated credential set in the ledger. Synchronous, pool-free and
// transaction-free, so the in-transaction carrier can call it: the write itself is the flusher's job.
func (s *Service) holdRotation(processor string, creds map[string]string) {
	snap := make(map[string]string, len(creds))
	for k, v := range creds {
		snap[k] = v
	}
	s.rotMu.Lock()
	s.rotPend = &rotatedCreds{processor: processor, creds: snap}
	s.rotMu.Unlock()
}

// freshestCreds overlays the pending rotation on the row's credentials. While a carried rotation's
// flush is still in flight the row still answers the stale key, and the processor has already
// exchanged that key, so a charge reading the row in the gap would present a dead credential and the
// rail would refuse the refresh (the round 2 review's probe: the sale straight after a rotating
// declined sale failed once on a multi-connection pool). The ledger's snapshot is the complete fresh
// set the dispatch path held, so it replaces the row's answer outright; a nil ledger or a different
// processor's entry leaves the row's answer alone. The answer is a COPY taken under the lock: the
// dispatch path writes its call-scoped __idem into the credentials it is handed and the live client
// rotates keys in place, so the ledger's own map must stay immutable after holdRotation (the round 3
// review's P1: the aliased map aborted the process under overlapping charges).
func (s *Service) freshestCreds(processor string, creds map[string]string) map[string]string {
	s.rotMu.Lock()
	defer s.rotMu.Unlock()
	if s.rotPend == nil || s.rotPend.processor != processor {
		return creds
	}
	cp := make(map[string]string, len(s.rotPend.creds))
	for k, v := range s.rotPend.creds {
		cp[k] = v
	}
	return cp
}

// flushRotation writes whatever the ledger holds through the pool, best-effort, and KEEPS the entry
// until the write succeeds (the round 2 review's ask: a failed write is retried by a later flush
// instead of dropping the fresh key silently). The write goes through the store DIRECTLY, never
// through resealOnPool: the pool carrier's clear is processor-wide, and a landing flush must never
// clear an entry a NEWER rotation replaced meanwhile (the round 4 review's P2: the flush read set A,
// a second rotation held B, and a processor-wide clear from A's landing wiped B, losing the fresh key
// the rail needs; the pointer compare below is the guard, and it is the ONLY clear on this path).
// The whole flush sits under flushMu, so the flushes land in the order they started (the round 5
// review's P2: with no order between flushes, the first flush parked mid-write on A while a second
// rotation's flush landed B and cleared, then A's stale write landed LAST, leaving the ledger empty
// and the row on A, so the next charge presented the key the processor had already exchanged; the
// lock makes the newest rotation's write the last to land). Called by the flusher goroutine and
// directly by the tests.
func (s *Service) flushRotation(ctx context.Context) {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	s.rotMu.Lock()
	pend := s.rotPend
	s.rotMu.Unlock()
	if pend == nil {
		return
	}
	if rs, ok := s.cfg.(credsReencryptor); ok {
		if err := rs.reencryptActiveCreds(ctx, pend.processor, pend.creds); err != nil {
			slog.Warn("payment: the rotation write-back failed; the fresh credentials stay pending and a later flush retries them",
				"processor", pend.processor, "err", err)
			return
		}
	}
	s.rotMu.Lock()
	if s.rotPend == pend {
		s.rotPend = nil
	}
	s.rotMu.Unlock()
}

// carryRotationOutOfTheTransaction is the write-back carrier for ChargeTx: it records the rotation in
// the ledger and starts the flusher. The flusher's window bounds its wait for a free connection (on a
// one-connection pool that is the ring releasing its connection, so the write lands after the ring
// ends and a rollback cannot discard it).
func (s *Service) carryRotationOutOfTheTransaction(ctx context.Context, processor string, creds map[string]string) error {
	s.holdRotation(processor, creds)
	s.launchRotationFlusher()
	return nil
}

// launchRotationFlusher starts one flusher for the freshest carried rotation. It goes through the
// startFlusher field so a test can hold the flusher and observe the carrier's synchronous half
// deterministically: the production goroutine can be scheduled between ChargeTx returning and the
// test's first read, erasing the very state a leg asserts (the round 2 review's P2).
func (s *Service) launchRotationFlusher() {
	if s.startFlusher != nil {
		s.startFlusher()
		return
	}
	s.startRotationFlusherGoroutine()
}

func (s *Service) startRotationFlusherGoroutine() {
	go func() {
		fctx, cancel := context.WithTimeout(context.Background(), rotationFlushWindow)
		defer cancel()
		s.flushRotation(fctx)
	}()
}

// charge is the shared body: dispatch Processor.Charge and return the
// receipt. reseal is the rotation write-back's carrier, the pool or the
// caller's transaction, chosen by the entry point (its error is ignored
// exactly as the write-back always was: best-effort, the processor's
// answer is the receipt). A nil/zero token or amount fails before any
// dispatch.
func (s *Service) charge(ctx context.Context, cfg activeCfg, creds map[string]string, in ChargeInput, reseal func(context.Context, string, map[string]string) error) (ChargeReceipt, error) {
	if in.PaymentToken == "" || in.AmountCents <= 0 {
		return ChargeReceipt{}, errBadCharge
	}
	prevKey := creds["api_key"]
	creds["__idem"] = in.IdempotencyKey // call-scoped; not persisted
	res, chargeErr := s.proc.Charge(ctx, creds, cfg.MerchantID, in.AmountCents, in.PaymentToken)
	if creds["api_key"] != prevKey && creds["api_key"] != "" {
		_ = reseal(ctx, cfg.Processor, creds)
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
// Callers holding no database transaction call THIS entry point; the pos Return transaction's
// reversal goes through RefundTx.
func (s *Service) Refund(ctx context.Context, in RefundInput) (RefundReceipt, error) {
	cfg, creds, err := s.cfg.activeConfig(ctx)
	if err != nil {
		return RefundReceipt{}, err
	}
	return s.refund(ctx, cfg, creds, in, s.resealOnPool)
}

// RefundTx is Refund on the caller's open transaction. The pos Return
// transaction holds one connection and the per-sale advisory lock while it
// reverses the card: the config read here rides THAT transaction
// (activeConfigTx) and the rotation write-back, if it ever fires, rides it
// too, because a pool read there would have the lock holder wait for a
// second connection every other connection is a Return waiter that needs
// the holder's lock, deadlocking a full pool until the caller's context
// ends (the FindReturnByKeyTx discipline). No pool use is reachable from
// this path at all.
func (s *Service) RefundTx(ctx context.Context, tx pgx.Tx, in RefundInput) (RefundReceipt, error) {
	cfg, creds, err := s.cfg.activeConfigTx(ctx, tx)
	if err != nil {
		return RefundReceipt{}, err
	}
	return s.refund(ctx, cfg, creds, in, func(ctx context.Context, processor string, creds map[string]string) error {
		if rs, ok := s.cfg.(credsReencryptor); ok {
			_ = rs.reencryptActiveCredsTx(ctx, tx, processor, creds)
		}
		return nil
	})
}

// resealOnPool is the write-back carrier for the pool-shaped Charge and
// Refund, the behavior those entry points have always had. Its error surfaces to the caller
// (the shared bodies ignore it, exactly as the write-back always was: best-effort); the flusher
// writes through the store directly and NEVER through this carrier, because this clear is
// processor-wide and a landing flush must not clear a newer rotation's entry (the round 4 review's
// P2; the flusher's own pointer compare is its guard). A successful write clears any pending entry
// for the same processor, so a sitting entry cannot shadow the row the next read sees (the round 3
// review's note: with the overlay handing copies, a stale entry would outlive the pool path's own
// landing). The clear is processor-wide with no pointer compare: it wipes even a NEWER carried
// entry whose flush has not yet read the ledger, and the row then carries this set, not something
// newer (the round 5 review's P3). That reach belongs to the pool entry points alone, which have no
// production caller today (the recorded follow-up).
func (s *Service) resealOnPool(ctx context.Context, processor string, creds map[string]string) error {
	if rs, ok := s.cfg.(credsReencryptor); ok {
		if err := rs.reencryptActiveCreds(ctx, processor, creds); err != nil {
			return err
		}
	}
	s.rotMu.Lock()
	if s.rotPend != nil && s.rotPend.processor == processor {
		s.rotPend = nil
	}
	s.rotMu.Unlock()
	return nil
}

// refund is the shared body: dispatch Processor.Refund against the ORIGINAL
// capture's processor ref and return the receipt. reseal is the rotation
// write-back's carrier, the pool or the caller's transaction, chosen by the
// entry point (its error is ignored exactly as the write-back always was:
// best-effort, the processor's answer is the receipt).
func (s *Service) refund(ctx context.Context, cfg activeCfg, creds map[string]string, in RefundInput, reseal func(context.Context, string, map[string]string) error) (RefundReceipt, error) {
	if in.ProcessorRef == "" || in.AmountCents <= 0 {
		return RefundReceipt{}, errBadRefund
	}
	prevKey := creds["api_key"]
	creds["__idem"] = in.IdempotencyKey // call-scoped; not persisted
	res, refundErr := s.proc.Refund(ctx, creds, cfg.MerchantID, in.ProcessorRef, in.AmountCents)
	if creds["api_key"] != prevKey && creds["api_key"] != "" {
		_ = reseal(ctx, cfg.Processor, creds)
	}
	rcpt := RefundReceipt{Processor: cfg.Processor, ProcessorRef: res.ProcessorRef, Status: res.Status}
	if refundErr != nil {
		rcpt.Status = "failed"
		return rcpt, refundErr
	}
	return rcpt, nil
}
