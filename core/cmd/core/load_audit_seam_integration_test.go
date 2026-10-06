//go:build integration

// load_audit_seam_integration_test.go proves the load frame's audit seam
// end to end through the real audit service (HH-LOAD-1, ADR-0024's one
// audit row per batch): the row the seam writes lands in audit_log with
// the machine principal as actor and the counts by outcome, written on
// the caller's transaction, and a rolled back attempt lands nothing. All
// fixtures are invented: uuids generated per run, a batch id no caller
// ever sent.
//
// Run (the database URL rides the command line, never a default):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5621/comp_gate?sslmode=disable' \
//	  go test -tags integration ./cmd/core/ -count=1 -run TestLoadAuditSeam
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/jackc/pgx/v5/pgxpool"
)

func seamPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, integration tests skipped")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return pool
}

func seamRow(t *testing.T, pool *pgxpool.Pool, batchUUID string) (action, entityType, actor, changes string) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `
		SELECT action, entity_type, coalesce(actor_id::text, ''), changes::text
		FROM audit_log WHERE entity_id = $1::uuid`, batchUUID).
		Scan(&action, &entityType, &actor, &changes)
	if err != nil {
		t.Fatalf("audit row read: %v", err)
	}
	return action, entityType, actor, changes
}

func TestLoadAuditSeamWritesTheBatchAuditRow(t *testing.T) {
	pool := seamPool(t)
	svc := audit.NewService(audit.NewRepository(pool), slog.New(slog.DiscardHandler))
	seam := loadAuditSeam{svc: svc}
	ctx := context.Background()

	newUUID := func(t *testing.T) string {
		t.Helper()
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			t.Fatalf("uuid: %v", err)
		}
		b[6] = (b[6] & 0x0f) | 0x40
		b[8] = (b[8] & 0x3f) | 0x80
		return fmtSprintfUUID(b)
	}
	batchUUID, actor := newUUID(t), newUUID(t)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := seam.RecordBatchTx(ctx, tx, load.BatchAuditRow{
		BatchUUID:    batchUUID,
		BatchID:      "b-seam-1",
		Kind:         "probe",
		SourceSystem: "testsuite",
		ActorID:      actor,
		Counts:       map[string]int{load.OutcomeCreated: 2, load.OutcomeRefused: 1},
	}); err != nil {
		t.Fatalf("seam write: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	action, entityType, gotActor, changes := seamRow(t, pool, batchUUID)
	if action != load.AuditAction || entityType != load.AuditEntityType {
		t.Fatalf("audit row action %q entity %q, want %s on %s", action, entityType, load.AuditAction, load.AuditEntityType)
	}
	if gotActor != actor {
		t.Fatalf("audit row actor %q, want the machine principal %q", gotActor, actor)
	}
	var changesMap map[string]any
	if err := json.Unmarshal([]byte(changes), &changesMap); err != nil {
		t.Fatalf("changes decode: %v", err)
	}
	want := map[string]any{
		"load_kind":     "probe",
		"load_batch_id": "b-seam-1",
		"load_source":   "testsuite",
		"load_created":  float64(2),
		"load_refused":  float64(1),
		"purpose":       load.AuditPurpose,
	}
	for k, v := range want {
		got, ok := changesMap[k]
		if !ok || got != v {
			t.Fatalf("audit row changes %s: key %q is %v, want %v", changes, k, got, v)
		}
	}

	// The rolled back attempt lands nothing: the row rides the caller's
	// transaction, not its own.
	lostUUID := newUUID(t)
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := seam.RecordBatchTx(ctx, tx, load.BatchAuditRow{
		BatchUUID: lostUUID, BatchID: "b-seam-lost", Kind: "probe",
		SourceSystem: "testsuite", ActorID: actor,
		Counts: map[string]int{load.OutcomeCreated: 1},
	}); err != nil {
		t.Fatalf("seam write: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE entity_id = $1::uuid`, lostUUID).Scan(&n); err != nil {
		t.Fatalf("absent read: %v", err)
	}
	if n != 0 {
		t.Fatalf("the rolled back attempt left %d audit rows, want none", n)
	}
}

func fmtSprintfUUID(b [16]byte) string {
	const hexd = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, c := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out = append(out, '-')
		}
		out = append(out, hexd[c>>4], hexd[c&0x0f])
	}
	return string(out)
}
