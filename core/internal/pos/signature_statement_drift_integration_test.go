//go:build integration

// signature_statement_drift_integration_test.go, the database half of the drift test: the SAME
// attestation captured through the pool path (CaptureTypedSignature) and the transaction path
// (CaptureTypedSignatureTx) must land as two pos_signature rows with the same columns and values
// apart from the id, the reference inside the sealed payload and the capture timestamp, because both
// paths persist through the ONE shared statement (Repository.InsertSignatureTx). A second statement
// reintroduced on either path, or an edit to one copy only, shows up here as a column or payload
// mismatch, a stamp that did not come from the database, or a trail row gone missing on one path.
// The unit half (signature_statement_drift_test.go) pins the same rule at the source level; this leg
// proves the stored rows.
//
// Run with: DATABASE_URL=... go test -tags integration ./internal/pos/
package pos

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/jackc/pgx/v5/pgxpool"
)

// capturedAtWindow is how far apart the two inserts' stamps may sit. Both paths take the database
// default now() at insert (pos_signature.captured_at is NOT NULL DEFAULT now(): the database stamps
// the row, no path writes the column), and the two inserts run back to back in this leg, so the
// rule is: both stamps non null and within five seconds of each other. A path that stamped from the
// Go clock, from a fake, or stopped letting the database stamp at all pushes the pair apart or
// empties it, and this window is what notices.
const capturedAtWindow = 5 * time.Second

// sigMaskedRefShape matches the masked reference a signature capture's audit row carries: the sig_
// reference hex, or the audit boundary's own mask, which is what a reference whose hex happens to
// carry a long digit run is replaced with before it is allowed into audit_log.
var sigMaskedRefShape = regexp.MustCompile(`^sig_[0-9a-f]{16}$`)

type storedSignatureRow struct {
	id         string
	purpose    string
	refType    string
	refID      string
	provider   string
	capturedBy string
	enc        []byte
	capturedAt *time.Time
}

// driftLegService builds the real service over the pool with the typed provider bound and the real
// audit recorder wired, so both capture paths write the trail rows they write in production.
func driftLegService(t *testing.T, pool *pgxpool.Pool) (*Service, *payment.Vault) {
	t.Helper()
	vault, err := payment.NewVault("") // random 32-byte key, key version 1
	if err != nil {
		t.Fatalf("vault: %v", err)
	}
	svc := NewService(NewRepository(pool)).
		WithSignatureProvider(NewTypedNameSignatureProvider(vault)).
		WithAudit(audit.NewService(audit.NewRepository(pool), nil))
	return svc, vault
}

// captureSignatureThroughBothPaths captures the attestation once on the pool path and once riding
// the caller's transaction, commits, and returns the two stored rows ordered by id.
func captureSignatureThroughBothPaths(t *testing.T, pool *pgxpool.Pool, ctx context.Context, svc *Service, in TypedAttestation) []storedSignatureRow {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM pos_signature WHERE ref_id = $1::uuid`, in.RefID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_log WHERE entity_type = 'signature' AND entity_id = $1::uuid`, in.RefID)
	})

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

	var out []storedSignatureRow
	rrows, err := pool.Query(ctx, `
		SELECT id::text, purpose, ref_type, ref_id::text, provider, COALESCE(captured_by::text,''),
		       signature_enc, captured_at
		  FROM pos_signature WHERE ref_id = $1::uuid ORDER BY id`, in.RefID)
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
	if len(out) != 2 {
		t.Fatalf("one capture per path must store exactly two rows for the subject, got %d", len(out))
	}
	return out
}

func TestIntegration_SignaturePoolAndTxPathsShareOneStatement(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool) // a real seeded user, captured_by is FK-bound to users
	svc, vault := driftLegService(t, pool)

	in := TypedAttestation{
		Purpose: "PROOF_OF_DELIVERY", RefType: "will_call_pickup", RefID: sigUUID(),
		TypedName: "Statement Drift Leg", Affirmed: true,
	}
	rows := captureSignatureThroughBothPaths(t, pool, ctx, svc, in)

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

	// captured_at: neither path writes the column, both rows carry the database's own now() at
	// insert, so both stamps are non null and the back to back inserts sit inside one small window
	// (the rule capturedAtWindow states). A path that stamped from a Go clock or a fake, or wrote
	// the column itself, breaks the pair apart or empties it.
	if a.capturedAt == nil || b.capturedAt == nil {
		t.Fatalf("both rows must carry the database's captured_at stamp, got %v and %v", a.capturedAt, b.capturedAt)
	}
	if a.capturedAt.IsZero() || b.capturedAt.IsZero() {
		t.Fatalf("both rows must carry a real captured_at stamp, got %v and %v", a.capturedAt, b.capturedAt)
	}
	if d := a.capturedAt.Sub(*b.capturedAt); d < -capturedAtWindow || d > capturedAtWindow {
		t.Fatalf("both inserts are stamped by the database's now() and must sit within %s of each other, got %v apart (%v vs %v)",
			capturedAtWindow, d, a.capturedAt, b.capturedAt)
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

	// The audit rows: the pool path writes its trail row on the pool (best effort there, its caller
	// has nothing to abort), the tx path writes its row ON the transaction so it commits with the
	// capture (fail closed, the ruling the tx capture's own comment carries). What lands must be the
	// same index shape on both: same action, entity type and entity id, and a masked reference of
	// the signature shape, never the typed name.
	type auditTrailRow struct{ action, entityType, entityID, maskedRef string }
	var trail []auditTrailRow
	trows, err := pool.Query(ctx, `
		SELECT action, entity_type, COALESCE(entity_id::text,''), COALESCE(changes->>'masked_ref','')
		  FROM audit_log WHERE entity_type = 'signature' AND entity_id = $1::uuid ORDER BY seq`, in.RefID)
	if err != nil {
		t.Fatalf("read audit rows: %v", err)
	}
	defer trows.Close()
	for trows.Next() {
		var r auditTrailRow
		if err := trows.Scan(&r.action, &r.entityType, &r.entityID, &r.maskedRef); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		trail = append(trail, r)
	}
	if err := trows.Err(); err != nil {
		t.Fatalf("audit rows: %v", err)
	}
	if len(trail) != 2 {
		t.Fatalf("each capture must leave exactly one audit row for its subject, the pool path best effort and the tx path fail closed, got %d", len(trail))
	}
	for _, r := range trail {
		if r.action != audit.ActSignatureCapture {
			t.Fatalf("both trail rows must carry the capture action %q, got %q", audit.ActSignatureCapture, r.action)
		}
		if r.entityType != "signature" {
			t.Fatalf("both trail rows must name the signature entity type, got %q", r.entityType)
		}
		if r.entityID != in.RefID {
			t.Fatalf("both trail rows must name the attested subject %s, got %q", in.RefID, r.entityID)
		}
		if !sigMaskedRefShape.MatchString(r.maskedRef) && r.maskedRef != "[redacted]" {
			t.Fatalf("both trail rows must carry a masked reference of the signature shape, got %q", r.maskedRef)
		}
		if strings.Contains(r.maskedRef, in.TypedName) {
			t.Fatal("the typed name must never reach the audit trail, it lives sealed, once")
		}
	}
}

// TestIntegration_SignatureNoActorStoresNullOnBothPaths covers the capture with no staff actor in
// the context at all: captured_by is empty, and both paths must store SQL NULL for it (the column is
// FK-bound to users, an actorless capture has no row to point at), never an empty string dressed up
// as a value.
func TestIntegration_SignatureNoActorStoresNullOnBothPaths(t *testing.T) {
	pool := intPool(t)
	svc, vault := driftLegService(t, pool)

	in := TypedAttestation{
		Purpose: "PROOF_OF_DELIVERY", RefType: "will_call_pickup", RefID: sigUUID(),
		TypedName: "Statement Drift Leg", Affirmed: true,
	}
	rows := captureSignatureThroughBothPaths(t, pool, context.Background(), svc, in)

	var nullCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM pos_signature WHERE ref_id = $1::uuid AND captured_by IS NULL`, in.RefID).Scan(&nullCount); err != nil {
		t.Fatalf("count null actors: %v", err)
	}
	if nullCount != 2 {
		t.Fatalf("an actorless capture must store captured_by NULL on both paths, got %d of 2 rows NULL", nullCount)
	}
	a, b := rows[0], rows[1]
	if a.capturedBy != "" || b.capturedBy != "" {
		t.Fatalf("an actorless capture must leave no actor on either row, got %q and %q", a.capturedBy, b.capturedBy)
	}
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
	if aa.ActorID != "" || bb.ActorID != "" {
		t.Fatalf("an actorless capture must seal no actor on either path, got %q and %q", aa.ActorID, bb.ActorID)
	}
}
