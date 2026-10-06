//go:build integration

// load_audit_path_integration_test.go runs ONE FULL BATCH through
// UpsertBatch with the real audit seam (the wiring's own newLoadDoor
// over the platform's audit service) on a ONE CONNECTION pool, the card
// rail wiring leg's shape: a batch that rides one connection from claim
// to finish commits on the pool's only connection; a protocol that
// needs a second connection for the items stalls until its context
// dies, and this leg fails naming the stall. The batch's audit row is
// then read back from audit_log: the machine principal as actor, the
// counts by outcome, and no record value anywhere in it.
//
// All fixtures are invented.
//
// Run (the database URL rides the command line, never a default):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5636/r4_seam?sslmode=disable' \
//	  go test -tags integration ./cmd/core/ -count=1 -run TestLoadBatchThroughTheRealAuditPath
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// auditPathPool opens the leg's pool: exactly one connection, the shape
// where a second-connection need stalls visibly.
func auditPathPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, integration tests skipped")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("audit path pool config: %v", err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("audit path pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("audit path pool ping: %v", err)
	}
	return pool
}

// auditPathWriter is the owning module's write path for the leg's
// scratch shape, an insert-only writer drawing one sequence per write.
type auditPathWriter struct{}

func (auditPathWriter) Apply(ctx context.Context, tx pgx.Tx, in load.Write) (string, error) {
	payload, _ := in.Record["payload"].(string)
	if in.RowID != "" {
		tag, err := tx.Exec(ctx, `
			UPDATE load_audit_probe SET payload = $3, updated_at = now()
			WHERE id = $2::uuid AND org_id = $1::uuid`,
			in.OrgID, in.RowID, payload)
		if err != nil {
			return "", fmt.Errorf("probe update: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return "", &load.Refusal{Code: load.ReasonUpdateTargetMissing, Field: "legacy_id",
				Detail: "the name's row is gone"}
		}
		return in.RowID, nil
	}
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO load_audit_probe (org_id, payload)
		VALUES ($1, $2)
		RETURNING id::text`, in.OrgID, payload).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("probe create: %w", err)
	}
	return id, nil
}

// TestLoadBatchThroughTheRealAuditPathCommits: one batch of three items
// (two that land, one refused on a missing reference) through UpsertBatch
// over the real audit path on the one connection pool; the batch commits,
// and the audit row in audit_log carries the machine principal as actor,
// the counts by outcome, and no record value.
func TestLoadBatchThroughTheRealAuditPathCommits(t *testing.T) {
	pool := auditPathPool(t)
	ctx := context.Background()
	for _, ddl := range []string{
		`DROP TABLE IF EXISTS load_audit_probe`,
		`CREATE TABLE load_audit_probe (
			id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			org_id     UUID NOT NULL,
			payload    TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatalf("setup %q: %v", ddl, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS load_audit_probe`)
	})

	orgID := "5e1f0000-0000-4000-8000-4f0000000001"
	actor := "5e1f0000-0000-4000-8000-4f0000000002"
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx,
			`DELETE FROM load_key_registry WHERE org_id = $1::uuid`, orgID)
		_, _ = pool.Exec(ctx,
			`DELETE FROM load_batch WHERE org_id = $1::uuid`, orgID)
		_, _ = pool.Exec(ctx,
			`DELETE FROM audit_log WHERE entity_id = ANY($1::uuid[])`, []string{orgID})
	})

	svc := audit.NewService(audit.NewRepository(pool), slog.New(slog.DiscardHandler))
	store := load.NewStore(pool)
	door := newLoadDoor(store, "probe", auditPathWriter{}, svc)

	watchdog := time.AfterFunc(90*time.Second, func() {
		t.Fatalf("the one connection audit path leg never finished even past its own watchdog: the batch's claim holds the pool's only connection and an item waited for a second (the stall)")
	})
	defer watchdog.Stop()
	batchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	r, err := door.UpsertBatch(batchCtx, load.Batch{
		OrgID: orgID, SourceSystem: "testsuite", BatchID: "b-audit-path-1", Caller: actor,
		Items: []load.Item{
			{LegacyID: "L-1", DerivedKey: "f1000000-0000-4000-8000-000000000001",
				Record: map[string]any{"payload": "audit-path-payload-one"}},
			{LegacyID: "L-2", DerivedKey: "f1000000-0000-4000-8000-000000000002",
				Record: map[string]any{"payload": "audit-path-payload-two"}},
			{LegacyID: "L-3", DerivedKey: "f1000000-0000-4000-8000-000000000003",
				Record:   map[string]any{"payload": "audit-path-payload-three"},
				Requires: []load.Ref{{Kind: "probe_parent", LegacyID: "parent-absent"}}},
		},
	})
	if err != nil {
		t.Fatalf("the batch through the real audit path: %v", err)
	}
	if len(r.Items) != 3 || r.Counts[load.OutcomeCreated] != 2 || r.Counts[load.OutcomeRefused] != 1 {
		t.Fatalf("the batch answered counts %+v over %d items, want two created and one refused", r.Counts, len(r.Items))
	}

	// The batch record stands, and the audit row is the record's own.
	var batchUUID string
	if err := pool.QueryRow(ctx, `
		SELECT id::text FROM load_batch
		WHERE org_id = $1::uuid AND source_system = $2 AND batch_id = $3`,
		orgID, "testsuite", "b-audit-path-1").Scan(&batchUUID); err != nil {
		t.Fatalf("batch record read: %v", err)
	}
	var action, entityType, changes string
	var actorID *string
	if err := pool.QueryRow(ctx, `
		SELECT action, entity_type, coalesce(actor_id::text, ''), changes::text
		FROM audit_log WHERE entity_id = $1::uuid`, batchUUID).
		Scan(&action, &entityType, &actorID, &changes); err != nil {
		t.Fatalf("audit row read: %v", err)
	}
	if action != load.AuditAction || entityType != load.AuditEntityType {
		t.Fatalf("audit row action %q entity %q, want %s on %s", action, entityType, load.AuditAction, load.AuditEntityType)
	}
	if actorID == nil || *actorID != actor {
		t.Fatalf("audit row actor %v, want the machine principal %s", actorID, actor)
	}
	var changesMap map[string]any
	if err := json.Unmarshal([]byte(changes), &changesMap); err != nil {
		t.Fatalf("changes decode: %v", err)
	}
	want := map[string]any{
		"load_kind":     "probe",
		"load_batch_id": "b-audit-path-1",
		"load_source":   "testsuite",
		"load_created":  float64(2),
		"load_refused":  float64(1),
	}
	for k, v := range want {
		got, ok := changesMap[k]
		if !ok || got != v {
			t.Fatalf("audit row changes %s: key %q is %v, want %v", changes, k, got, v)
		}
	}
	// No record value anywhere in the row: the payloads the items carried
	// appear nowhere in the changes.
	for _, secret := range []string{"audit-path-payload-one", "audit-path-payload-two", "audit-path-payload-three"} {
		if contains(changes, secret) {
			t.Fatalf("the audit row carries a record value (%q): %s", secret, changes)
		}
	}
}

// contains is a tiny substring helper for the record value scan.
func contains(s, sub string) bool {
	for i := 0; len(sub) > 0 && i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
