//go:build integration

// signature_statement_drift_integration_test.go, the database half of the drift test: the SAME
// attestation captured through the pool path (CaptureTypedSignature) and the transaction path
// (CaptureTypedSignatureTx) must land as two pos_signature rows with the same columns and values
// apart from the id, the reference inside the sealed payload and the capture timestamp, because both
// paths persist through the ONE shared statement (Repository.InsertSignatureTx). A second statement
// reintroduced on either path, or an edit to one copy only, shows up here as a column or payload
// mismatch. The unit half (signature_statement_drift_test.go) pins the same rule at the source level;
// this leg proves the stored rows.
//
// Run with: DATABASE_URL=... go test -tags integration ./internal/pos/
package pos

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
)

type storedSignatureRow struct {
	id         string
	purpose    string
	refType    string
	refID      string
	provider   string
	capturedBy string
	enc        []byte
	capturedAt time.Time
}

func TestIntegration_SignaturePoolAndTxPathsShareOneStatement(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool) // a real seeded user, captured_by is FK-bound to users

	vault, err := payment.NewVault("") // random 32-byte key, key version 1
	if err != nil {
		t.Fatalf("vault: %v", err)
	}
	svc := NewService(NewRepository(pool)).
		WithSignatureProvider(NewTypedNameSignatureProvider(vault))

	refID := sigUUID()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM pos_signature WHERE ref_id = $1::uuid`, refID)
	})

	in := TypedAttestation{
		Purpose: "PROOF_OF_DELIVERY", RefType: "will_call_pickup", RefID: refID,
		TypedName: "Statement Drift Leg", Affirmed: true,
	}

	// The pool path: the standalone capture.
	if _, err := svc.CaptureTypedSignature(ctx, in); err != nil {
		t.Fatalf("pool capture: %v", err)
	}

	// The transaction path: the same attestation riding the caller's transaction.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := svc.CaptureTypedSignatureTx(ctx, tx, in); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("tx capture: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	rows := func() []storedSignatureRow {
		t.Helper()
		var out []storedSignatureRow
		rrows, err := pool.Query(ctx, `
			SELECT id::text, purpose, ref_type, ref_id::text, provider, COALESCE(captured_by::text,''),
			       signature_enc, captured_at
			  FROM pos_signature WHERE ref_id = $1::uuid ORDER BY id`, refID)
		if err != nil {
			t.Fatalf("read rows: %v", err)
		}
		defer rrows.Close()
		for rrows.Next() {
			var r storedSignatureRow
			if err := rrows.Scan(&r.id, &r.purpose, &r.refType, &r.refID, &r.provider, &r.capturedBy, &r.enc, &r.capturedAt); err != nil {
				t.Fatalf("scan row: %v", err)
			}
			out = append(out, r)
		}
		if err := rrows.Err(); err != nil {
			t.Fatalf("rows: %v", err)
		}
		return out
	}()

	if len(rows) != 2 {
		t.Fatalf("one capture per path must store exactly two rows for the subject, got %d", len(rows))
	}
	a, b := rows[0], rows[1]
	if a.id == b.id {
		t.Fatalf("the two captures must be distinct rows, got id %s twice", a.id)
	}
	if a.purpose != b.purpose || a.refType != b.refType || a.refID != b.refID || a.provider != b.provider {
		t.Fatalf("the two paths must store the same subject columns, got %+v and %+v", a, b)
	}
	if a.provider != ProviderTypedNameV1 {
		t.Fatalf("provider = %q, want %q", a.provider, ProviderTypedNameV1)
	}
	if a.capturedBy == "" || a.capturedBy != b.capturedBy {
		t.Fatalf("the witnessing staff actor must be identical on both rows, got %q and %q", a.capturedBy, b.capturedBy)
	}

	// The sealed payloads agree apart from the reference and the capture timestamp: both paths sealed
	// the SAME act, so what the row carries must be the same evidence shape.
	pa, err := vault.Decrypt(a.enc, 1)
	if err != nil {
		t.Fatalf("decrypt pool row: %v", err)
	}
	pb, err := vault.Decrypt(b.enc, 1)
	if err != nil {
		t.Fatalf("decrypt tx row: %v", err)
	}
	aa, err := DecodeAttestation(pa)
	if err != nil {
		t.Fatalf("decode pool payload: %v", err)
	}
	bb, err := DecodeAttestation(pb)
	if err != nil {
		t.Fatalf("decode tx payload: %v", err)
	}
	if aa.Version != bb.Version || aa.TypedName != bb.TypedName || aa.Affirmed != bb.Affirmed {
		t.Fatalf("the payloads must carry the same attestation shape, got %+v and %+v", aa, bb)
	}
	if aa.Purpose != bb.Purpose || aa.RefType != bb.RefType || aa.RefID != bb.RefID {
		t.Fatalf("the payloads must name the same subject, got %+v and %+v", aa, bb)
	}
	if aa.ActorID == "" || aa.ActorID != bb.ActorID {
		t.Fatalf("the witnessing actor must be identical in both payloads, got %q and %q", aa.ActorID, bb.ActorID)
	}
}
