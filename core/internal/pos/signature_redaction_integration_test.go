//go:build integration

// End to end over the real seams: a typed-name capture writes its SIGNATURE_CAPTURE row into the
// real audit_log through the real audit service, and the reference it minted must come back out of
// that row intact. The provider's random-byte seam is pinned so the reference deterministically
// carries a run of 10 consecutive decimal digits inside its hex, the shape the audit chokepoint's
// cleartext filter used to read as a raw PAN and redact, losing the trail's only reference. Both
// capture paths are proven: the shared-pool capture and the transaction-riding capture.
//
// DATABASE_URL carries the database this suite runs against; the test skips when it is unset. No
// port or database name is pinned here. Run with: DATABASE_URL=... go test -tags integration ./internal/pos/
package pos

import (
	"context"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit/legsweep"
	"os"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/jackc/pgx/v5/pgxpool"
)

// redactionPool dials the suite's database straight from DATABASE_URL and skips when it is unset:
// no fallback URL, nothing defaulted, the run names its own database or it does not run.
func redactionPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if !set || url == "" {
		t.Skip("integration: DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("integration database unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("integration database not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	legsweep.Register(t, pool)
	return pool
}

// pinnedRefBytes encodes to the hex "1234567890abcdef": a minted reference whose opening 10
// characters are one digit run, the case the filter redacted.
var pinnedRefBytes = [8]byte{0x12, 0x34, 0x56, 0x78, 0x90, 0xab, 0xcd, 0xef}

// wantPinnedRef is the reference the pinned bytes mint.
const wantPinnedRef = "sig_1234567890abcdef"

// pinSigRand swaps the provider's random-byte seam for the pinned bytes and restores the real
// source when the test ends.
func pinSigRand(t *testing.T) {
	t.Helper()
	prev := sigRandRead
	sigRandRead = func(p []byte) (int, error) {
		copy(p, pinnedRefBytes[:])
		return len(p), nil
	}
	t.Cleanup(func() { sigRandRead = prev })
}

// cleanupCaptureTrail removes the rows one captured attestation left behind, targeted on the
// attestation's ref id, so the suite is reentrant against a shared database.
func cleanupCaptureTrail(t *testing.T, pool *pgxpool.Pool, refID string) {
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM audit_log WHERE entity_type = 'signature' AND entity_id = $1::uuid`, refID)
		_, _ = pool.Exec(ctx, `DELETE FROM pos_signature WHERE ref_id = $1::uuid`, refID)
	})
}

// capturedMaskedRef reads the SIGNATURE_CAPTURE row for one attestation back out of audit_log
// through the audit service's own reopen read, the same read the history screens use.
func capturedMaskedRef(t *testing.T, svc *audit.Service, refID string) map[string]any {
	t.Helper()
	entries, err := svc.ReopenTransaction(context.Background(), "signature", refID)
	if err != nil {
		t.Fatalf("reopen the signature trail: %v", err)
	}
	for _, e := range entries {
		if e.Action == audit.ActSignatureCapture {
			return e.Changes
		}
	}
	t.Fatalf("no SIGNATURE_CAPTURE row in the trail for %s", refID)
	return nil
}

func TestIntegration_SignatureRef_SurvivesAuditRedaction(t *testing.T) {
	pool := redactionPool(t)
	ctx := globalCtx(t, pool) // a real seeded user: the capture's actor is FK-bound
	pinSigRand(t)

	vault, err := payment.NewVault("") // random 32-byte key, key version 1
	if err != nil {
		t.Fatalf("vault: %v", err)
	}
	auditSvc := audit.NewService(audit.NewRepository(pool), nil)

	// The provider bound exactly as the service binds it lazily: the real pos repository and the
	// real audit recorder, no fakes between the capture and the row.
	p := NewTypedNameSignatureProvider(vault)
	p.bind(NewRepository(pool), auditSvc, nil)

	t.Run("the pool capture's reference survives the audit boundary", func(t *testing.T) {
		refID := sigUUID()
		cleanupCaptureTrail(t, pool, refID)
		in := TypedAttestation{
			Purpose: "CARD_PRESENT", RefType: "order", RefID: refID,
			TypedName: "Onsite Customer", Affirmed: true,
		}
		ref, err := p.CaptureTyped(ctx, in)
		if err != nil {
			t.Fatalf("capture: %v", err)
		}
		if ref != wantPinnedRef {
			t.Fatalf("ref = %q, want the pinned minted reference %q", ref, wantPinnedRef)
		}
		changes := capturedMaskedRef(t, auditSvc, refID)
		if got := changes["masked_ref"]; got != wantPinnedRef {
			t.Fatalf("audit masked_ref = %v, want the minted reference %q intact: the trail lost its reference to the redaction filter", got, wantPinnedRef)
		}
	})

	t.Run("the transaction capture's reference survives the audit boundary", func(t *testing.T) {
		refID := sigUUID()
		cleanupCaptureTrail(t, pool, refID)
		in := TypedAttestation{
			Purpose: "PROOF_OF_DELIVERY", RefType: "dispatch_stop", RefID: refID,
			TypedName: "Onsite Customer", Affirmed: true,
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		ref, err := p.CaptureTypedTx(ctx, tx, in)
		if err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("capture on the caller's transaction: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
		if ref != wantPinnedRef {
			t.Fatalf("ref = %q, want the pinned minted reference %q", ref, wantPinnedRef)
		}
		changes := capturedMaskedRef(t, auditSvc, refID)
		if got := changes["masked_ref"]; got != wantPinnedRef {
			t.Fatalf("audit masked_ref = %v, want the minted reference %q intact: the trail lost its reference to the redaction filter", got, wantPinnedRef)
		}
	})
}
