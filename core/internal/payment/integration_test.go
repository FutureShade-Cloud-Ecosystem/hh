//go:build integration

// Integration tests run the real pgx Store against the Postgres that `make reset` provisions,
// exercising migration 0027 (I1), the write-only encrypted config (T15/T16/T17), and the no-double-
// write boundary (I2, D-RUNPAY-1). Run with: make test-integration (after make migrate/seed).
package payment

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("no integration DB (%v), run make reset first", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func cleanConfig(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	// DIB-396: payment_config is a SINGLETON table (ux_payment_config_active enforces one active
	// row), so wiping it yanks the card rail out from under any other package mid-charge, the gate
	// card-stub test configures a row and then rings a sale against it. Hold the config lease for
	// the rest of the test so the two never interleave (see dib396_lease_test.go).
	dib396LeaseExclusive(t, pool, dib396PaymentConfigLease)
	if _, err := pool.Exec(context.Background(), `DELETE FROM payment_config`); err != nil {
		t.Fatalf("clean payment_config: %v", err)
	}
}

func intStore(t *testing.T, pool *pgxpool.Pool) *Store {
	v, err := NewVault(testVaultKey)
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	return NewStore(pool, v)
}

// I1, migration 0027 applied clean: payment_config + columns + the card_type CHECK + widened status
// + the one-active partial index.
func TestIntegration_Schema_0027(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()

	var n int
	if err := pool.QueryRow(ctx, `SELECT 1 FROM payment_config LIMIT 0`).Scan(&n); err != nil && err.Error() == `no rows in result set` {
		// table exists (the LIMIT 0 returns no rows), acceptable.
	} else if err != nil {
		t.Fatalf("payment_config missing: %v", err)
	}

	for _, col := range []string{"processor", "payment_token", "card_type", "surcharge_cents"} {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='payments' AND column_name=$1)`,
			col).Scan(&exists); err != nil {
			t.Fatalf("column lookup %s: %v", col, err)
		}
		if !exists {
			t.Fatalf("payments.%s missing", col)
		}
	}

	// status now accepts PENDING.
	if _, err := pool.Exec(ctx,
		`SELECT 'PENDING'::text WHERE 'PENDING' IN ('RECEIVED','DEPOSITED','BOUNCED','REFUNDED','VOID','PENDING')`); err != nil {
		t.Fatalf("PENDING reservation check: %v", err)
	}

	// ux_payment_config_active present.
	var idx bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname='ux_payment_config_active')`).Scan(&idx); err != nil {
		t.Fatalf("index lookup: %v", err)
	}
	if !idx {
		t.Fatal("ux_payment_config_active missing")
	}
}

// T15, the credentials_ciphertext column never contains the plaintext secret (US-RP01 PCI).
// T16, Status returns only {configured, merchant_id, processor, currency}; a direct query confirms no creds.
func TestIntegration_Config_CiphertextOnly_And_StatusHidesCreds(t *testing.T) {
	pool := intPool(t)
	cleanConfig(t, pool)
	st := intStore(t, pool)
	ctx := context.Background()

	const secret = "dealer-plaintext-api_key-tkn_ppc_zzz"
	if err := st.Configure(ctx, "run_payments", "MID-1", "CAD", "", 0, false,
		map[string]string{"api_key": secret, "refresh_token": "rt_x"}, ""); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	var ct []byte
	if err := pool.QueryRow(ctx,
		`SELECT credentials_ciphertext FROM payment_config WHERE status='active'`).Scan(&ct); err != nil {
		t.Fatalf("read ciphertext: %v", err)
	}
	if bytes.Contains(ct, []byte(secret)) {
		t.Fatal("credentials_ciphertext contains the plaintext secret, PCI failure")
	}

	configured, mid, proc, currency, err := st.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !configured || mid != "MID-1" || proc != "run_payments" || currency != "CAD" {
		t.Fatalf("Status = %v/%q/%q/%q", configured, mid, proc, currency)
	}
}

// T17, reconfigure retires the prior active row; ux_payment_config_active holds exactly one active.
func TestIntegration_Config_OneActivePerProcessor(t *testing.T) {
	pool := intPool(t)
	cleanConfig(t, pool)
	st := intStore(t, pool)
	ctx := context.Background()

	if err := st.Configure(ctx, "run_payments", "MID-1", "CAD", "", 0, false, map[string]string{"api_key": "k1"}, ""); err != nil {
		t.Fatalf("Configure 1: %v", err)
	}
	if err := st.Configure(ctx, "run_payments", "MID-2", "CAD", "", 0, false, map[string]string{"api_key": "k2"}, ""); err != nil {
		t.Fatalf("Configure 2: %v", err)
	}
	var active int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payment_config WHERE processor='run_payments' AND status='active'`).Scan(&active); err != nil {
		t.Fatalf("count active: %v", err)
	}
	if active != 1 {
		t.Fatalf("active rows = %d, want exactly 1", active)
	}
	_, mid, _, _, _ := st.Status(ctx)
	if mid != "MID-2" {
		t.Fatalf("active merchant = %q, want MID-2 (the latest)", mid)
	}
}

// I2, Configure → Service.Charge (stub) returns captured; assert NO payments row written by RUN-PAY
// and the ciphertext ≠ plaintext (end-to-end boundary + PCI; D-RUNPAY-1).
func TestIntegration_Charge_WritesNoPaymentsRow(t *testing.T) {
	pool := intPool(t)
	cleanConfig(t, pool)
	st := intStore(t, pool)
	ctx := context.Background()

	if err := st.Configure(ctx, "run_payments", "MID-1", "CAD", "", 0, false, map[string]string{"api_key": "k"}, ""); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	svc := NewService(st, RunPaymentsStub{})
	rcpt, err := svc.Charge(ctx, ChargeInput{AmountCents: 12500, PaymentToken: "tok", IdempotencyKey: "uuid", CardType: "CREDIT"})
	if err != nil {
		t.Fatalf("Charge: %v", err)
	}
	if rcpt.Status != "captured" || rcpt.ProcessorRef == "" {
		t.Fatalf("receipt = %+v", rcpt)
	}

	// DIB-396: assert on THIS charge's own processor ref rather than a whole-table count. The old
	// before/after `count(*) FROM payments` form raced every other package that rings or tears down
	// a tendered sale on the shared DB, and it was also weaker, a count that happened to net out
	// would have passed. A row carrying this capture's gateway_ref is the exact thing D-RUNPAY-1
	// forbids the gateway from writing.
	var leaked int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payments WHERE gateway_ref = $1`, rcpt.ProcessorRef).Scan(&leaked); err != nil {
		t.Fatalf("count payments by gateway_ref: %v", err)
	}
	if leaked != 0 {
		t.Fatalf("RUN-PAY wrote %d payments row(s) for its own capture %q, the till owns that write (D-RUNPAY-1)",
			leaked, rcpt.ProcessorRef)
	}
}
