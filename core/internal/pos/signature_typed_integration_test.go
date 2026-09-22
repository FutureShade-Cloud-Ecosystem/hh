//go:build integration

// Integration test for the typed-name signature provider (DIB-386) against the real Postgres and the
// real AES-256-GCM vault. It proves the sealed payload decrypts to the CONTENT that was typed and
// affirmed (not the stub's "SIMULATED:<purpose>:<ref>" placeholder), that the row is stamped
// TYPED_NAME_V1 (so migration 0089's widened CHECK is live), and that an empty or unaffirmed
// attestation is refused with nothing written.
//
// Run with: DATABASE_URL=... go test -tags integration ./internal/pos/
package pos

import (
	"context"
	"crypto/rand"
	"fmt"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/jackc/pgx/v5/pgxpool"
)

func sigUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// countSignatures counts the rows written against one subject, the "nothing was written" assertion.
func countSignatures(t *testing.T, pool *pgxpool.Pool, refID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pos_signature WHERE ref_id = $1::uuid`, refID).Scan(&n); err != nil {
		t.Fatalf("count signatures: %v", err)
	}
	return n
}

func TestIntegration_Signature_TypedNameSealed(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool) // a real seeded user, captured_by is FK-bound to users

	vault, err := payment.NewVault("") // random 32-byte key, key version 1
	if err != nil {
		t.Fatalf("vault: %v", err)
	}
	svc := NewService(NewRepository(pool)).
		WithSignatureProvider(NewTypedNameSignatureProvider(vault))

	refID := sigUUID()
	in := TypedAttestation{
		Purpose: "TAX_ATTESTATION", RefType: "tax_decision_snapshot", RefID: refID,
		TypedName: "Ryan Rood", Affirmed: true,
	}

	var ref string
	t.Run("a typed, affirmed attestation is captured", func(t *testing.T) {
		ref, err = svc.CaptureTypedSignature(ctx, in)
		if err != nil {
			t.Fatalf("capture: %v", err)
		}
		if ref == "" {
			t.Fatal("a capture must return its ref")
		}
	})

	t.Run("the row is stamped TYPED_NAME_V1 (0089 CHECK admits it)", func(t *testing.T) {
		var provider string
		var capturedBy string
		if err := pool.QueryRow(context.Background(), `
			SELECT provider, COALESCE(captured_by::text,'') FROM pos_signature
			 WHERE ref_id = $1::uuid ORDER BY captured_at DESC LIMIT 1`, refID).
			Scan(&provider, &capturedBy); err != nil {
			t.Fatalf("read row: %v", err)
		}
		if provider != ProviderTypedNameV1 {
			t.Fatalf("provider = %q, want %q, the row must report what actually happened",
				provider, ProviderTypedNameV1)
		}
		if capturedBy == "" {
			t.Fatal("the witnessing staff actor must be on the row")
		}
	})

	t.Run("the sealed payload decrypts to the typed content", func(t *testing.T) {
		var enc []byte
		if err := pool.QueryRow(context.Background(), `
			SELECT signature_enc FROM pos_signature
			 WHERE ref_id = $1::uuid ORDER BY captured_at DESC LIMIT 1`, refID).Scan(&enc); err != nil {
			t.Fatalf("read payload: %v", err)
		}
		// It is genuinely encrypted at rest: the cleartext name is not sitting in the column.
		if string(enc) == "" {
			t.Fatal("signature_enc must not be empty")
		}
		if contains(string(enc), "Ryan Rood") {
			t.Fatal("the payload must be SEALED, the typed name must not be readable in the column")
		}
		if contains(string(enc), "SIMULATED") {
			t.Fatal("the stub placeholder payload must be gone (DIB-386)")
		}
		plain, err := vault.Decrypt(enc, 1)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		att, err := DecodeAttestation(plain)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if att.TypedName != "Ryan Rood" {
			t.Fatalf("typed_name = %q, want the name that was typed", att.TypedName)
		}
		if !att.Affirmed {
			t.Fatal("the payload must record the explicit affirmative")
		}
		if att.Version != ProviderTypedNameV1 {
			t.Fatalf("version = %q, the payload shape is part of the evidence", att.Version)
		}
		if att.Purpose != in.Purpose || att.RefType != in.RefType || att.RefID != in.RefID {
			t.Fatalf("the payload must name what was signed: %+v", att)
		}
		if att.ActorID == "" || att.CapturedAt.IsZero() || att.Ref != ref {
			t.Fatalf("the payload must carry who + when + which ref: %+v", att)
		}
	})

	t.Run("an UNAFFIRMED attestation is refused and writes nothing", func(t *testing.T) {
		otherRef := sigUUID()
		bad := in
		bad.RefID, bad.Affirmed = otherRef, false
		if _, err := svc.CaptureTypedSignature(ctx, bad); err == nil {
			t.Fatal("the affirmative is a REQUIRED separate act, an unaffirmed capture must be refused")
		}
		if n := countSignatures(t, pool, otherRef); n != 0 {
			t.Fatalf("a refused capture must leave no row behind, got %d", n)
		}
	})

	t.Run("an EMPTY name is refused and writes nothing", func(t *testing.T) {
		otherRef := sigUUID()
		bad := in
		bad.RefID, bad.TypedName = otherRef, "   "
		if _, err := svc.CaptureTypedSignature(ctx, bad); err == nil {
			t.Fatal("a blank typed name is not a signature, it must be refused")
		}
		if n := countSignatures(t, pool, otherRef); n != 0 {
			t.Fatalf("a refused capture must leave no row behind, got %d", n)
		}
	})

	t.Run("the un-attested capture seam refuses on the typed provider", func(t *testing.T) {
		otherRef := sigUUID()
		if _, err := svc.CaptureSignature(ctx, "TAX_ATTESTATION", "tax_decision_snapshot", otherRef); err == nil {
			t.Fatal("the old un-attested seam must refuse rather than fabricate a capture")
		}
		if n := countSignatures(t, pool, otherRef); n != 0 {
			t.Fatalf("the refused seam must write nothing, got %d", n)
		}
	})
}
