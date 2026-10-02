package payment

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// -- DB-free fakes (the internal/pricing service-test pattern) --------------

// fakeResolver implements configResolver (and the optional credsReencryptor) without a database, so
// the pure Charge gateway call is unit-testable. It hands Charge a FRESH copy of creds each call so
// in-place rotation by the processor is observable. The tx twins are counted apart from the pool
// reads and writes: the in-transaction charge must resolve through them and never through the pool
// shapes. The counters and recorded facts sit behind a mutex because the rotation flusher runs on
// its own goroutine (the HH-181 fakePolicy lesson: a shared test helper must be race-clean).
type fakeResolver struct {
	cfg   activeCfg
	creds map[string]string
	err   error

	mu            sync.Mutex
	calls         int
	txCalls       int
	reencryptCall int
	txReencrypt   int
	reencProc     string
	reencCreds    map[string]string
	failReencrypt int // while above zero, reencryptActiveCreds fails and decrements (the retry leg)
}

// answer is the shared body: a fixed row plus a fresh creds copy.
func (f *fakeResolver) answer() (activeCfg, map[string]string, error) {
	if f.err != nil {
		return activeCfg{}, nil, f.err
	}
	cp := make(map[string]string, len(f.creds))
	for k, v := range f.creds {
		cp[k] = v
	}
	return f.cfg, cp, nil
}

func (f *fakeResolver) activeConfig(_ context.Context) (activeCfg, map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.answer()
}

func (f *fakeResolver) activeConfigTx(_ context.Context, _ pgx.Tx) (activeCfg, map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.txCalls++
	return f.answer()
}

func (f *fakeResolver) reencryptActiveCreds(_ context.Context, processor string, creds map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reencryptCall++
	if f.failReencrypt > 0 {
		f.failReencrypt--
		return errors.New("reencrypt failed: the store is draining (the retry leg's scripted failure)")
	}
	f.reencProc = processor
	f.reencCreds = creds
	return nil
}

func (f *fakeResolver) reencryptActiveCredsTx(_ context.Context, _ pgx.Tx, processor string, creds map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.txReencrypt++
	f.reencProc = processor
	f.reencCreds = creds
	return nil
}

// spyProcessor records what Service.Charge dispatched and can simulate an api_key rotation. Its
// counters and recorded facts sit behind a mutex: the overlapping-charges leg shares one spy across
// goroutines (the HH-181 fakePolicy lesson: a shared test helper must be race-clean).
type spyProcessor struct {
	mu        sync.Mutex
	res       ChargeResult
	err       error
	calls     int
	gotMID    string
	gotAmount int64
	gotToken  string
	gotKey    string // the api_key the dispatch presented (the overlay legs read it)
	mutateKey string // if set, simulates the live client rotating the api_key in-place
}

func (p *spyProcessor) Name() string { return "spy" }
func (p *spyProcessor) Charge(_ context.Context, creds map[string]string, mid string, amount int64, token string) (ChargeResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.gotMID, p.gotAmount, p.gotToken = mid, amount, token
	p.gotKey = creds["api_key"]
	if p.mutateKey != "" {
		creds["api_key"] = p.mutateKey
	}
	return p.res, p.err
}
func (p *spyProcessor) Refund(context.Context, map[string]string, string, string, int64) (RefundResult, error) {
	return RefundResult{}, ErrNotImplemented
}

func validInput() ChargeInput {
	return ChargeInput{AmountCents: 12500, PaymentToken: "tok_runtest_4242", IdempotencyKey: "client-uuid", CardType: "CREDIT"}
}

// T12, Charge before any active config returns ErrNoConfig (US-RP01/02).
func TestService_Charge_NoConfig(t *testing.T) {
	svc := &Service{cfg: &fakeResolver{err: ErrNoConfig}, proc: RunPaymentsStub{}}
	_, err := svc.Charge(context.Background(), validInput())
	if !errors.Is(err, ErrNoConfig) {
		t.Fatalf("err = %v, want ErrNoConfig", err)
	}
}

// T13, configured (stub) → captured ChargeReceipt with the processor name + a non-empty ref (US-RP02).
func TestService_Charge_Captured_StubDefault(t *testing.T) {
	fr := &fakeResolver{
		cfg:   activeCfg{Processor: "run_payments", MerchantID: "MID-1", Currency: "CAD"},
		creds: map[string]string{"api_key": "tkn_ppc_test"},
	}
	svc := &Service{cfg: fr, proc: RunPaymentsStub{}}
	rcpt, err := svc.Charge(context.Background(), validInput())
	if err != nil {
		t.Fatalf("Charge: %v", err)
	}
	if rcpt.Processor != "run_payments" || rcpt.Status != "captured" || rcpt.ProcessorRef == "" {
		t.Fatalf("receipt = %+v, want run_payments/captured/non-empty ref", rcpt)
	}
}

// T14, the boundary test (unit form): Charge's ONLY side effects are reading payment_config (the
// resolver) and dispatching to the processor. Service holds no payments handle, so it cannot write a
// payments row (US-RP07, D-RUNPAY-1). The real row-count assertion is I2 (integration).
func TestService_Charge_WritesNoPaymentsRow(t *testing.T) {
	fr := &fakeResolver{
		cfg:   activeCfg{Processor: "run_payments", MerchantID: "MID-1"},
		creds: map[string]string{"api_key": "k"},
	}
	sp := &spyProcessor{res: ChargeResult{ProcessorRef: "ref", Status: "captured"}}
	svc := &Service{cfg: fr, proc: sp}
	if _, err := svc.Charge(context.Background(), validInput()); err != nil {
		t.Fatalf("Charge: %v", err)
	}
	if fr.calls != 1 {
		t.Fatalf("activeConfig calls = %d, want exactly 1 (read payment_config once)", fr.calls)
	}
	if sp.calls != 1 {
		t.Fatalf("processor calls = %d, want exactly 1", sp.calls)
	}
	if sp.gotAmount != 12500 || sp.gotToken != "tok_runtest_4242" || sp.gotMID != "MID-1" {
		t.Fatalf("processor dispatched wrong args: %+v", sp)
	}
	// No re-encrypt happened (api_key unchanged), Charge wrote nothing at all.
	if fr.reencryptCall != 0 {
		t.Fatalf("no credential re-encrypt expected, got %d", fr.reencryptCall)
	}
}

// An ill-formed charge fails BEFORE any processor dispatch (no silent capture; US-RP02).
func TestService_Charge_BadInput_NoDispatch(t *testing.T) {
	fr := &fakeResolver{cfg: activeCfg{Processor: "run_payments", MerchantID: "MID-1"}, creds: map[string]string{"api_key": "k"}}
	sp := &spyProcessor{res: ChargeResult{Status: "captured"}}
	svc := &Service{cfg: fr, proc: sp}
	for _, in := range []ChargeInput{
		{AmountCents: 12500, PaymentToken: ""},
		{AmountCents: 0, PaymentToken: "tok"},
	} {
		if _, err := svc.Charge(context.Background(), in); !errors.Is(err, errBadCharge) {
			t.Fatalf("err = %v, want errBadCharge", err)
		}
	}
	if sp.calls != 0 {
		t.Fatalf("processor must NOT be dispatched for ill-formed input, got %d calls", sp.calls)
	}
}

// A processor error surfaces with Status="failed" (US-RP03 the till must not record a RECEIVED row).
func TestService_Charge_ProcessorError_Failed(t *testing.T) {
	fr := &fakeResolver{cfg: activeCfg{Processor: "run_payments", MerchantID: "MID-1"}, creds: map[string]string{"api_key": "k"}}
	sp := &spyProcessor{res: ChargeResult{Status: "failed"}, err: errors.New("declined")}
	svc := &Service{cfg: fr, proc: sp}
	rcpt, err := svc.Charge(context.Background(), validInput())
	if err == nil {
		t.Fatal("a processor error must propagate")
	}
	if rcpt.Status != "failed" {
		t.Fatalf("status = %q, want failed", rcpt.Status)
	}
}

// When the live client rotates an expired api_key, Service re-encrypts the rotated creds into the
// active payment_config row (never the payments table), ARCH_DELTA §2.11.
func TestService_Charge_ReencryptsRotatedCreds(t *testing.T) {
	fr := &fakeResolver{
		cfg:   activeCfg{Processor: "run_payments", MerchantID: "MID-1"},
		creds: map[string]string{"api_key": "expired"},
	}
	sp := &spyProcessor{res: ChargeResult{ProcessorRef: "ref", Status: "captured"}, mutateKey: "fresh"}
	svc := &Service{cfg: fr, proc: sp}
	if _, err := svc.Charge(context.Background(), validInput()); err != nil {
		t.Fatalf("Charge: %v", err)
	}
	if fr.reencryptCall != 1 {
		t.Fatalf("re-encrypt calls = %d, want 1 after rotation", fr.reencryptCall)
	}
	if fr.reencProc != "run_payments" {
		t.Fatalf("re-encrypt processor = %q", fr.reencProc)
	}
	if fr.reencCreds["api_key"] != "fresh" {
		t.Fatalf("re-encrypted api_key = %q, want fresh", fr.reencCreds["api_key"])
	}
}

// The tx charge resolves the config ON the caller's transaction, exactly
// once, and never through the pool: the pos Sale transaction holds one
// connection for the whole atomic ring while it takes the tender, and a
// pool read there would have the connection holder wait for a second
// connection, stalling a one-connection pool on a single sale and
// deadlocking a full pool of concurrent sales (the FindReturnByKeyTx
// discipline, RefundTx's shape on the charge side).
func TestService_ChargeTx_RidesTheTransaction(t *testing.T) {
	fr := &fakeResolver{
		cfg:   activeCfg{Processor: "run_payments", MerchantID: "MID-1"},
		creds: map[string]string{"api_key": "k"},
	}
	sp := &spyProcessor{res: ChargeResult{ProcessorRef: "rp_sim_x", Status: "captured"}}
	svc := &Service{cfg: fr, proc: sp}
	rcpt, err := svc.ChargeTx(context.Background(), nil, validInput())
	if err != nil {
		t.Fatalf("ChargeTx: %v", err)
	}
	if rcpt.Status != "captured" || rcpt.ProcessorRef != "rp_sim_x" || rcpt.Processor != "run_payments" {
		t.Fatalf("receipt = %+v, want run_payments/captured/rp_sim_x", rcpt)
	}
	if fr.txCalls != 1 {
		t.Fatalf("activeConfigTx calls = %d, want exactly 1 (the config read rides the transaction)", fr.txCalls)
	}
	if fr.calls != 0 {
		t.Fatalf("activeConfig calls = %d, want 0: an in-transaction charge must never read through the pool", fr.calls)
	}
	if sp.calls != 1 || sp.gotAmount != 12500 || sp.gotToken != "tok_runtest_4242" || sp.gotMID != "MID-1" {
		t.Fatalf("processor dispatched wrong args: %+v", sp)
	}
}

// The pool-shaped Charge still resolves through activeConfig, exactly as it
// always has: its callers hold no transaction, the pool is the right carrier
// for them.
func TestService_Charge_ReadsThroughThePool(t *testing.T) {
	fr := &fakeResolver{
		cfg:   activeCfg{Processor: "run_payments", MerchantID: "MID-1"},
		creds: map[string]string{"api_key": "k"},
	}
	sp := &spyProcessor{res: ChargeResult{ProcessorRef: "rp_sim_x", Status: "captured"}}
	svc := &Service{cfg: fr, proc: sp}
	if _, err := svc.Charge(context.Background(), validInput()); err != nil {
		t.Fatalf("Charge: %v", err)
	}
	if fr.calls != 1 {
		t.Fatalf("activeConfig calls = %d, want exactly 1", fr.calls)
	}
	if fr.txCalls != 0 {
		t.Fatalf("activeConfigTx calls = %d, want 0: the pool caller holds no transaction to ride", fr.txCalls)
	}
}

// The rotation write-back on the tx charge is CARRIED OUT of the
// transaction and lands through the pool after the caller's ring ends:
// a credential rotation must survive a rolled-back sale (the processor
// has already exchanged the old key, so losing the fresh one can lock the
// rail out; round 1's P2), and a write riding the caller's transaction is
// exactly what a rollback discards. The stub never rotates, and that is
// exactly why the carrier is pinned here with a rotating spy. The
// flusher is HELD for the synchronous half (startFlusher pinned to a
// no-op): the production goroutine can be scheduled between ChargeTx
// returning and this leg's first read, erasing the very state asserted
// here (round 2's P2 flake); the flush itself is then driven directly,
// and only the last phase hands the start back to the production
// goroutine, whose landing is waited on with a bounded poll.
func TestService_ChargeTx_RotationCarriedOutOfTheTransaction(t *testing.T) {
	fr := &fakeResolver{
		cfg:   activeCfg{Processor: "run_payments", MerchantID: "MID-1"},
		creds: map[string]string{"api_key": "expired"},
	}
	sp := &spyProcessor{res: ChargeResult{ProcessorRef: "rp_sim_x", Status: "captured"}, mutateKey: "fresh"}
	svc := &Service{cfg: fr, proc: sp}
	held := false
	svc.startFlusher = func() { held = true }
	if _, err := svc.ChargeTx(context.Background(), nil, validInput()); err != nil {
		t.Fatalf("ChargeTx: %v", err)
	}
	if !held {
		t.Fatal("the carrier must go through the injectable starter: the held starter was never called")
	}
	// The carrier's synchronous half, observed with the flusher held (nothing else can move the
	// ledger): the rotation is recorded the moment the live client rotates, the fresh key
	// snapshotted. The transaction twin must NOT have been used, and NO write of any shape has run
	// yet: the only actor so far is the carrier itself.
	svc.rotMu.Lock()
	pend := svc.rotPend
	svc.rotMu.Unlock()
	if pend == nil {
		t.Fatal("the rotation was not recorded in the ledger: nothing would survive a rollback")
	}
	if pend.processor != "run_payments" || pend.creds["api_key"] != "fresh" {
		t.Fatalf("the ledger kept wrong facts: processor %q creds %v", pend.processor, pend.creds)
	}
	fr.mu.Lock()
	txReenc, poolReenc := fr.txReencrypt, fr.reencryptCall
	fr.mu.Unlock()
	if txReenc != 0 {
		t.Fatalf("reencryptActiveCredsTx calls = %d, want 0: a write riding the caller's transaction is discarded by a rollback", txReenc)
	}
	if poolReenc != 0 {
		t.Fatalf("reencryptActiveCreds calls = %d, want 0 before the flush is driven: with the flusher held, any write here would be inside the transaction's call stack (the stall shape)", poolReenc)
	}
	// The flusher's target, driven directly: the ledger's rotation lands through the POOL carrier,
	// best-effort, never on the transaction.
	svc.flushRotation(context.Background())
	fr.mu.Lock()
	poolReenc, reencProc, reencCreds := fr.reencryptCall, fr.reencProc, fr.reencCreds
	fr.mu.Unlock()
	if poolReenc != 1 {
		t.Fatalf("reencryptActiveCreds calls after the flush = %d, want 1: the carried rotation must land through the pool", poolReenc)
	}
	if reencProc != "run_payments" || reencCreds["api_key"] != "fresh" {
		t.Fatalf("the flush wrote wrong facts: processor %q creds %v", reencProc, reencCreds)
	}
	svc.rotMu.Lock()
	cleared := svc.rotPend == nil
	svc.rotMu.Unlock()
	if !cleared {
		t.Fatal("a landed flush must clear the ledger entry")
	}
	// The scheduled flusher: a second rotation must land the same way with no help at all, within a
	// bounded wait (the goroutine only has to be scheduled). The starter is handed back to the
	// production goroutine here, after every deterministic assertion above has run.
	sp2 := &spyProcessor{res: ChargeResult{ProcessorRef: "rp_sim_y", Status: "captured"}, mutateKey: "fresher"}
	svc.proc = sp2
	svc.startFlusher = svc.startRotationFlusherGoroutine
	if _, err := svc.ChargeTx(context.Background(), nil, validInput()); err != nil {
		t.Fatalf("ChargeTx (second rotation): %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		fr.mu.Lock()
		n, last := fr.reencryptCall, fr.reencCreds
		fr.mu.Unlock()
		if n >= 2 {
			if last["api_key"] != "fresher" {
				t.Fatalf("the flush wrote a stale rotation: api_key = %q, want the freshest", last["api_key"])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("reencryptActiveCreds calls = %d, want 2: the scheduled flusher did not land the carried rotation", n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// The read side of the ledger (round 2's P3 1): while a carried rotation's flush is still in flight
// the row still answers the stale key, and the processor has already exchanged it, so a charge
// reading the row in that gap would present a dead credential (the reviewer's probe: the sale
// straight after a rotating declined sale failed once on a multi-connection pool). Both entry points
// must overlay the pending rotation on the row's answer, and a DIFFERENT processor's pending entry
// must not touch the read.
func TestService_ChargeTx_OverlaysAPendingRotation(t *testing.T) {
	fr := &fakeResolver{
		cfg:   activeCfg{Processor: "run_payments", MerchantID: "MID-1"},
		creds: map[string]string{"api_key": "expired"},
	}
	rotate := &spyProcessor{res: ChargeResult{ProcessorRef: "rp_rot", Status: "captured"}, mutateKey: "fresh"}
	svc := &Service{cfg: fr, proc: rotate}
	svc.startFlusher = func() {} // hold the flush: the row keeps answering the stale key
	if _, err := svc.ChargeTx(context.Background(), nil, validInput()); err != nil {
		t.Fatalf("ChargeTx (rotating): %v", err)
	}
	see := &spyProcessor{res: ChargeResult{ProcessorRef: "rp_see", Status: "captured"}}
	svc.proc = see
	if _, err := svc.ChargeTx(context.Background(), nil, validInput()); err != nil {
		t.Fatalf("ChargeTx (reading the row while the flush is held): %v", err)
	}
	if see.gotKey != "fresh" {
		t.Fatalf("the charge presented api_key %q from the row while a fresher rotation sat pending: the overlay must answer the pending set (the processor already exchanged the row's key)", see.gotKey)
	}
	// A different processor's pending entry must not leak onto this read: the ledger's latest entry is
	// now the foreign rail's, so this rail's read falls back to the row's own answer.
	svc.holdRotation("other_rail", map[string]string{"api_key": "someone_elses"})
	if _, err := svc.ChargeTx(context.Background(), nil, validInput()); err != nil {
		t.Fatalf("ChargeTx (foreign pending entry): %v", err)
	}
	if see.gotKey != "expired" {
		t.Fatalf("a foreign processor's pending entry leaked onto this rail's read: api_key %q", see.gotKey)
	}
}

// The pool entry point overlays the same way (a caller between transactions reads the row while the
// flush is in flight).
func TestService_Charge_OverlaysAPendingRotation(t *testing.T) {
	fr := &fakeResolver{
		cfg:   activeCfg{Processor: "run_payments", MerchantID: "MID-1"},
		creds: map[string]string{"api_key": "expired"},
	}
	rotate := &spyProcessor{res: ChargeResult{ProcessorRef: "rp_rot", Status: "captured"}, mutateKey: "fresh"}
	svc := &Service{cfg: fr, proc: rotate}
	svc.startFlusher = func() {}
	if _, err := svc.ChargeTx(context.Background(), nil, validInput()); err != nil {
		t.Fatalf("ChargeTx (rotating): %v", err)
	}
	see := &spyProcessor{res: ChargeResult{ProcessorRef: "rp_see", Status: "captured"}}
	svc.proc = see
	if _, err := svc.Charge(context.Background(), validInput()); err != nil {
		t.Fatalf("Charge (reading the row while the flush is held): %v", err)
	}
	if see.gotKey != "fresh" {
		t.Fatalf("the pool charge presented api_key %q from the row while a fresher rotation sat pending: the overlay must answer the pending set", see.gotKey)
	}
}

// A failed flush keeps the entry and a later flush retries it (round 2's P3 2: the write is still
// best-effort, exactly as the write-back always was, but the ledger makes the retry free instead of
// dropping the fresh key silently).
func TestService_FlushRotation_RetriesUntilItLands(t *testing.T) {
	fr := &fakeResolver{
		cfg:           activeCfg{Processor: "run_payments", MerchantID: "MID-1"},
		creds:         map[string]string{"api_key": "expired"},
		failReencrypt: 1,
	}
	rotate := &spyProcessor{res: ChargeResult{ProcessorRef: "rp_rot", Status: "captured"}, mutateKey: "fresh"}
	svc := &Service{cfg: fr, proc: rotate}
	svc.startFlusher = func() {}
	if _, err := svc.ChargeTx(context.Background(), nil, validInput()); err != nil {
		t.Fatalf("ChargeTx (rotating): %v", err)
	}
	// The scripted failure: the write runs and fails.
	svc.flushRotation(context.Background())
	fr.mu.Lock()
	n, creds := fr.reencryptCall, fr.reencCreds
	fr.mu.Unlock()
	if n != 1 {
		t.Fatalf("reencryptActiveCreds calls = %d, want 1 (the scripted failure must still be a write attempt)", n)
	}
	if creds != nil {
		t.Fatalf("the failed write must not record landed creds, got %v", creds)
	}
	// The entry STAYS: nothing was lost.
	svc.rotMu.Lock()
	pend := svc.rotPend
	svc.rotMu.Unlock()
	if pend == nil || pend.creds["api_key"] != "fresh" {
		t.Fatal("a failed flush dropped the rotation: the entry must stay for a later retry")
	}
	// The retry lands and clears.
	svc.flushRotation(context.Background())
	fr.mu.Lock()
	n, creds = fr.reencryptCall, fr.reencCreds
	fr.mu.Unlock()
	if n != 2 {
		t.Fatalf("reencryptActiveCreds calls = %d, want 2 (the retry must write)", n)
	}
	if creds["api_key"] != "fresh" {
		t.Fatalf("the retry wrote wrong facts: %v", creds)
	}
	svc.rotMu.Lock()
	cleared := svc.rotPend == nil
	svc.rotMu.Unlock()
	if !cleared {
		t.Fatal("the landed retry must clear the entry")
	}
}

// spyRefunder records what Service.Refund dispatched and answers a fixed result; it can simulate the
// live client rotating an expiring api_key in place, the shape the write-back twin exists for.
type spyRefunder struct {
	calls     int
	gotMID    string
	gotRef    string
	gotCents  int64
	gotIdem   string
	mutateKey string
	res       RefundResult
}

func (p *spyRefunder) Name() string { return "spy" }
func (p *spyRefunder) Charge(context.Context, map[string]string, string, int64, string) (ChargeResult, error) {
	return ChargeResult{}, ErrNotImplemented
}
func (p *spyRefunder) Refund(_ context.Context, creds map[string]string, mid, ref string, amount int64) (RefundResult, error) {
	p.calls++
	p.gotMID, p.gotRef, p.gotCents, p.gotIdem = mid, ref, amount, creds["__idem"]
	if p.mutateKey != "" {
		creds["api_key"] = p.mutateKey
	}
	return p.res, nil
}

// The tx refund resolves the config ON the caller's transaction, exactly
// once, and never through the pool: the pos Return transaction holds a
// connection and the per-sale advisory lock while the reversal runs, and a
// pool read there would deadlock a full pool of Return waiters needing the
// holder's lock (the FindReturnByKeyTx discipline, one package deeper).
func TestService_RefundTx_RidesTheTransaction(t *testing.T) {
	fr := &fakeResolver{
		cfg:   activeCfg{Processor: "run_payments", MerchantID: "MID-1"},
		creds: map[string]string{"api_key": "k"},
	}
	sp := &spyRefunder{res: RefundResult{ProcessorRef: "rp_ref_x", Status: "refunded"}}
	svc := &Service{cfg: fr, proc: sp}
	rcpt, err := svc.RefundTx(context.Background(), nil, RefundInput{AmountCents: 12500, ProcessorRef: "rp_sim_captured", IdempotencyKey: "refund-key"})
	if err != nil {
		t.Fatalf("RefundTx: %v", err)
	}
	if rcpt.Status != "refunded" || rcpt.ProcessorRef != "rp_ref_x" || rcpt.Processor != "run_payments" {
		t.Fatalf("receipt = %+v, want run_payments/refunded/rp_ref_x", rcpt)
	}
	if fr.txCalls != 1 {
		t.Fatalf("activeConfigTx calls = %d, want exactly 1 (the config read rides the transaction)", fr.txCalls)
	}
	if fr.calls != 0 {
		t.Fatalf("activeConfig calls = %d, want 0: an in-transaction refund must never read through the pool", fr.calls)
	}
	if sp.calls != 1 || sp.gotRef != "rp_sim_captured" || sp.gotCents != 12500 || sp.gotMID != "MID-1" || sp.gotIdem != "refund-key" {
		t.Fatalf("processor dispatched wrong args: %+v", sp)
	}
}

// The pool-shaped Refund still resolves through activeConfig, exactly as it
// always has: its callers hold no transaction, the pool is the right carrier
// for them.
func TestService_Refund_ReadsThroughThePool(t *testing.T) {
	fr := &fakeResolver{
		cfg:   activeCfg{Processor: "run_payments", MerchantID: "MID-1"},
		creds: map[string]string{"api_key": "k"},
	}
	sp := &spyRefunder{res: RefundResult{ProcessorRef: "rp_ref_x", Status: "refunded"}}
	svc := &Service{cfg: fr, proc: sp}
	if _, err := svc.Refund(context.Background(), RefundInput{AmountCents: 12500, ProcessorRef: "rp_sim_captured", IdempotencyKey: "refund-key"}); err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if fr.calls != 1 {
		t.Fatalf("activeConfig calls = %d, want exactly 1", fr.calls)
	}
	if fr.txCalls != 0 {
		t.Fatalf("activeConfigTx calls = %d, want 0: the pool caller holds no transaction to ride", fr.txCalls)
	}
}

// The rotation write-back on the tx refund rides the transaction too. It is
// unreachable on the refund path today (no Processor mutates creds during
// Refund), and that is exactly why it is pinned here: the tx twin must
// already be the carrier the day a live client starts refreshing in Refund,
// so the write-back cannot come back as a pool write inside the Return
// transaction.
func TestService_RefundTx_WriteBackRidesTheTransaction(t *testing.T) {
	fr := &fakeResolver{
		cfg:   activeCfg{Processor: "run_payments", MerchantID: "MID-1"},
		creds: map[string]string{"api_key": "expired"},
	}
	sp := &spyRefunder{res: RefundResult{ProcessorRef: "rp_ref_x", Status: "refunded"}, mutateKey: "fresh"}
	svc := &Service{cfg: fr, proc: sp}
	if _, err := svc.RefundTx(context.Background(), nil, RefundInput{AmountCents: 12500, ProcessorRef: "rp_sim_captured", IdempotencyKey: "refund-key"}); err != nil {
		t.Fatalf("RefundTx: %v", err)
	}
	if fr.txReencrypt != 1 {
		t.Fatalf("reencryptActiveCredsTx calls = %d, want 1: the write-back rides the caller's transaction", fr.txReencrypt)
	}
	if fr.reencryptCall != 0 {
		t.Fatalf("reencryptActiveCreds calls = %d, want 0: a pool write inside the caller's transaction is the stall", fr.reencryptCall)
	}
	if fr.reencProc != "run_payments" || fr.reencCreds["api_key"] != "fresh" {
		t.Fatalf("write-back kept wrong facts: processor %q creds %v", fr.reencProc, fr.reencCreds)
	}
}

// HH-186, Store.Configure refuses surcharge_enabled=true before touching the pool or the vault:
// the row can never say enabled while no tender path computes a surcharge, and D18 requires a
// recorded jurisdiction and cap before enabling (none is). Every caller gets the same answer,
// not just the HTTP route.
func TestStore_Configure_RefusesSurchargeEnable(t *testing.T) {
	st := NewStore(nil, nil)
	err := st.Configure(context.Background(), "run_payments", "MID-1", "CAD", "", 240, true, map[string]string{"api_key": "k"}, "")
	if !errors.Is(err, ErrSurchargeNotEnforced) {
		t.Fatalf("Configure err = %v, want ErrSurchargeNotEnforced", err)
	}
}

// credentialKeys strips the injected non-secret keys so they never reach the ciphertext.
func TestCredentialKeys_StripsInjected(t *testing.T) {
	got := credentialKeys(map[string]string{
		"api_key": "k", "refresh_token": "rt",
		"__idem": "uuid", "base_url": "https://x", "currency": "CAD",
	})
	for _, k := range []string{"__idem", "base_url", "currency"} {
		if _, ok := got[k]; ok {
			t.Fatalf("injected key %q must be stripped before encryption", k)
		}
	}
	if got["api_key"] != "k" || got["refresh_token"] != "rt" {
		t.Fatalf("real credentials must be preserved, got %+v", got)
	}
}

// The ledger map is immutable once held (the round 3 review's P1): the overlay hands every charge a
// COPY, because the dispatch path writes its call-scoped __idem into the credentials it is handed and
// the live client rotates keys in place, so the ledger's own map must never be the map a charge (or
// the flusher) walks. This leg drives the exact abort shape the review reproduced: a pending entry,
// the flusher held, then overlapping charges and a flush against it at once. Under the race detector
// the aliased map aborts the run; with the copy, the run completes and the ledger's snapshot comes out
// clean of every charge's idempotency key.
func TestService_OverlappingChargesAgainstAPendingEntry(t *testing.T) {
	fr := &fakeResolver{
		cfg:   activeCfg{Processor: "run_payments", MerchantID: "MID-1"},
		creds: map[string]string{"api_key": "expired"},
	}
	rotate := &spyProcessor{res: ChargeResult{ProcessorRef: "rp_rot", Status: "captured"}, mutateKey: "fresh"}
	svc := &Service{cfg: fr, proc: rotate}
	svc.startFlusher = func() {} // hold the flusher: the entry sits for the whole leg
	if _, err := svc.ChargeTx(context.Background(), nil, validInput()); err != nil {
		t.Fatalf("ChargeTx (rotating): %v", err)
	}
	// The concurrent phase dispatches through a NON-rotating spy, so the held entry is never
	// re-held: every __idem write below happens on the COPY the overlay handed the charge, and the
	// ledger's snapshot must come out exactly as it was held (its own rotation's __idem from the
	// setup charge, never a concurrent charge's).
	svc.proc = &spyProcessor{res: ChargeResult{ProcessorRef: "rp_see", Status: "captured"}}

	// Script the concurrent flushes to fail, so the entry provably sits pending through the whole
	// leg and the snapshot assertions at the end really inspect it (a successful flush would clear
	// it, which is its own leg).
	fr.mu.Lock()
	fr.failReencrypt = 1000
	fr.mu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 7; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				in := validInput()
				in.IdempotencyKey = "idem-concurrent"
				if _, err := svc.ChargeTx(context.Background(), nil, in); err != nil {
					t.Errorf("overlapping charge %d: %v", i, err)
					return
				}
			}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 20; j++ {
			svc.flushRotation(context.Background())
		}
	}()
	wg.Wait()

	// The ledger's snapshot must carry no trace of the charges' call-scoped key: with the aliased
	// map, __idem landed in the snapshot and one charge could present another's idempotency key.
	svc.rotMu.Lock()
	pend := svc.rotPend
	svc.rotMu.Unlock()
	if pend == nil {
		t.Fatal("the pending entry vanished with the flusher held and every write scripted to fail: nothing should clear it")
	}
	if got := pend.creds["__idem"]; got == "idem-concurrent" {
		t.Fatal("a concurrent charge's call-scoped idempotency key landed in the ledger's snapshot: the overlay handed out the ledger's own map")
	}
	if pend.creds["api_key"] != "fresh" {
		t.Fatalf("the ledger's snapshot was mutated in place: api_key = %q, want the held rotation's", pend.creds["api_key"])
	}
}
